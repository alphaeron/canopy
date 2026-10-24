# The public HTTPS endpoint: the platform's webhooks and the review pages.
# Plain HTTP only redirects.

resource "aws_lb" "this" {
  name                       = var.name
  load_balancer_type         = "application"
  subnets                    = var.public_subnet_ids
  security_groups            = [aws_security_group.lb.id]
  drop_invalid_header_fields = true
}

resource "aws_lb_target_group" "this" {
  name        = var.name
  vpc_id      = var.vpc_id
  target_type = "ip"
  protocol    = "HTTP"
  port        = local.container_port
  # A little longer than the server's shutdown_timeout.
  deregistration_delay = 15

  # The server has no health route; its OpenAPI document needs no
  # configuration or credentials to serve.
  health_check {
    path                = "/openapi.json"
    matcher             = "200"
    interval            = 15
    healthy_threshold   = 2
    unhealthy_threshold = 3
  }
}

resource "aws_lb_listener" "https" {
  load_balancer_arn = aws_lb.this.arn
  port              = 443
  protocol          = "HTTPS"
  ssl_policy        = var.ssl_policy
  certificate_arn   = var.certificate_arn

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.this.arn
  }
}

resource "aws_lb_listener" "http" {
  load_balancer_arn = aws_lb.this.arn
  port              = 80
  protocol          = "HTTP"

  default_action {
    type = "redirect"
    redirect {
      port        = "443"
      protocol    = "HTTPS"
      status_code = "HTTP_301"
    }
  }
}

resource "aws_security_group" "lb" {
  name        = "${var.name}-lb"
  description = "canopyweb load balancer"
  vpc_id      = var.vpc_id
}

resource "aws_vpc_security_group_ingress_rule" "lb" {
  for_each = { for pair in setproduct(var.ingress_cidrs, [80, 443]) : "${pair[0]}:${pair[1]}" => pair }

  security_group_id = aws_security_group.lb.id
  cidr_ipv4         = each.value[0]
  ip_protocol       = "tcp"
  from_port         = each.value[1]
  to_port           = each.value[1]
}

resource "aws_vpc_security_group_egress_rule" "lb_to_service" {
  security_group_id            = aws_security_group.lb.id
  referenced_security_group_id = aws_security_group.service.id
  ip_protocol                  = "tcp"
  from_port                    = local.container_port
  to_port                      = local.container_port
}
