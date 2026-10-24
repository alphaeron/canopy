# canopyweb on ECS Fargate: its image repository, logs, task, and service.
# Tasks run in the private subnets and take their secrets from Secrets
# Manager as CANOPY_* environment variables (docs/configuration.md).

data "aws_region" "current" {}

resource "aws_ecr_repository" "this" {
  name                 = var.name
  image_tag_mutability = "IMMUTABLE"

  image_scanning_configuration {
    scan_on_push = true
  }
}

resource "aws_ecr_lifecycle_policy" "this" {
  repository = aws_ecr_repository.this.name
  policy = jsonencode({
    rules = [{
      rulePriority = 1
      description  = "Keep the newest ${var.ecr_keep_images} images"
      selection = {
        tagStatus   = "any"
        countType   = "imageCountMoreThan"
        countNumber = var.ecr_keep_images
      }
      action = { type = "expire" }
    }]
  })
}

resource "aws_cloudwatch_log_group" "this" {
  name              = "/ecs/${var.name}"
  retention_in_days = var.log_retention_days
}

resource "aws_ecs_cluster" "this" {
  name = var.name

  setting {
    name  = "containerInsights"
    value = "enabled"
  }
}

locals {
  container_port = 8080
  secrets = merge(var.secret_arns, {
    CANOPY_DATABASE_URL = aws_secretsmanager_secret.database_url.arn
  })
  environment = merge(var.environment, {
    CANOPY_SERVE_ADDR = ":${local.container_port}"
    CANOPY_PUBLIC_URL = var.public_url
  })
}

resource "aws_ecs_task_definition" "this" {
  family                   = var.name
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = var.cpu
  memory                   = var.memory
  execution_role_arn       = aws_iam_role.execution.arn

  runtime_platform {
    operating_system_family = "LINUX"
    cpu_architecture        = var.cpu_architecture
  }

  container_definitions = jsonencode([{
    name                   = "canopyweb"
    image                  = "${aws_ecr_repository.this.repository_url}:${var.image_tag}"
    essential              = true
    readonlyRootFilesystem = true
    portMappings           = [{ containerPort = local.container_port, protocol = "tcp" }]
    environment            = [for k, v in local.environment : { name = k, value = v }]
    secrets                = [for k, arn in local.secrets : { name = k, valueFrom = arn }]
    # Longer than the server's shutdown_timeout, so it drains before SIGKILL.
    stopTimeout = 30
    logConfiguration = {
      logDriver = "awslogs"
      options = {
        awslogs-group         = aws_cloudwatch_log_group.this.name
        awslogs-region        = data.aws_region.current.region
        awslogs-stream-prefix = "canopyweb"
      }
    }
  }])
}

resource "aws_ecs_service" "this" {
  name                              = var.name
  cluster                           = aws_ecs_cluster.this.id
  task_definition                   = aws_ecs_task_definition.this.arn
  desired_count                     = var.desired_count
  launch_type                       = "FARGATE"
  health_check_grace_period_seconds = 30

  network_configuration {
    subnets          = var.private_subnet_ids
    security_groups  = [aws_security_group.service.id]
    assign_public_ip = false
  }

  load_balancer {
    target_group_arn = aws_lb_target_group.this.arn
    container_name   = "canopyweb"
    container_port   = local.container_port
  }

  deployment_circuit_breaker {
    enable   = true
    rollback = true
  }

  depends_on = [aws_lb_listener.https]
}

data "aws_iam_policy_document" "ecs_tasks_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["ecs-tasks.amazonaws.com"]
    }
  }
}

# The execution role pulls the image, writes logs, and reads the secrets; the
# server itself calls no AWS API, so tasks have no task role.
resource "aws_iam_role" "execution" {
  name               = "${var.name}-execution"
  assume_role_policy = data.aws_iam_policy_document.ecs_tasks_assume.json
}

resource "aws_iam_role_policy_attachment" "execution" {
  role       = aws_iam_role.execution.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
}

data "aws_iam_policy_document" "read_secrets" {
  statement {
    actions   = ["secretsmanager:GetSecretValue"]
    resources = values(local.secrets)
  }
}

resource "aws_iam_role_policy" "read_secrets" {
  name   = "read-secrets"
  role   = aws_iam_role.execution.id
  policy = data.aws_iam_policy_document.read_secrets.json
}

resource "aws_security_group" "service" {
  name        = "${var.name}-service"
  description = "canopyweb tasks"
  vpc_id      = var.vpc_id
}

resource "aws_vpc_security_group_ingress_rule" "service_from_lb" {
  security_group_id            = aws_security_group.service.id
  referenced_security_group_id = aws_security_group.lb.id
  ip_protocol                  = "tcp"
  from_port                    = local.container_port
  to_port                      = local.container_port
}

# The platform's API, ECR, Secrets Manager, logs, and the database.
resource "aws_vpc_security_group_egress_rule" "service_all" {
  security_group_id = aws_security_group.service.id
  cidr_ipv4         = "0.0.0.0/0"
  ip_protocol       = "-1"
}
