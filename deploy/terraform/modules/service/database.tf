# PostgreSQL for the review server (docs/server.md#database). The server
# creates its own tables at startup.

resource "random_password" "database" {
  length  = 40
  special = false # used unescaped in the connection URL
}

resource "aws_db_subnet_group" "this" {
  name       = var.name
  subnet_ids = var.private_subnet_ids
}

resource "aws_db_instance" "this" {
  identifier     = var.name
  engine         = "postgres"
  engine_version = var.db_engine_version
  instance_class = var.db_instance_class

  allocated_storage     = var.db_allocated_storage
  max_allocated_storage = var.db_max_allocated_storage
  storage_type          = "gp3"
  storage_encrypted     = true

  db_name  = "canopy"
  username = "canopy"
  password = random_password.database.result

  db_subnet_group_name   = aws_db_subnet_group.this.name
  vpc_security_group_ids = [aws_security_group.database.id]
  publicly_accessible    = false
  multi_az               = var.db_multi_az

  backup_retention_period    = var.db_backup_retention_days
  auto_minor_version_upgrade = true
  deletion_protection        = var.db_deletion_protection
  skip_final_snapshot        = !var.db_final_snapshot
  final_snapshot_identifier  = var.db_final_snapshot ? "${var.name}-final" : null
}

# The URL verifies the server's certificate against the RDS bundle the image
# carries (see Dockerfile).
resource "aws_secretsmanager_secret" "database_url" {
  name                    = "${var.name}/database_url"
  recovery_window_in_days = var.secret_recovery_window_in_days
}

resource "aws_secretsmanager_secret_version" "database_url" {
  secret_id = aws_secretsmanager_secret.database_url.id
  secret_string = format(
    "postgres://%s:%s@%s:%d/%s?sslmode=verify-full&sslrootcert=/etc/ssl/certs/rds-ca.pem",
    aws_db_instance.this.username,
    random_password.database.result,
    aws_db_instance.this.address,
    aws_db_instance.this.port,
    aws_db_instance.this.db_name,
  )
}

resource "aws_security_group" "database" {
  name        = "${var.name}-database"
  description = "canopyweb database"
  vpc_id      = var.vpc_id
}

resource "aws_vpc_security_group_ingress_rule" "database_from_service" {
  security_group_id            = aws_security_group.database.id
  referenced_security_group_id = aws_security_group.service.id
  ip_protocol                  = "tcp"
  from_port                    = 5432
  to_port                      = 5432
}
