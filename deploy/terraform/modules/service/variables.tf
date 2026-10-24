variable "name" {
  description = "Name of the service and prefix of its resources."
  type        = string
}

variable "vpc_id" {
  type = string
}

variable "public_subnet_ids" {
  description = "Subnets of the load balancer."
  type        = list(string)
}

variable "private_subnet_ids" {
  description = "Subnets of the tasks and the database; they need a route to the internet (NAT)."
  type        = list(string)
}

variable "certificate_arn" {
  description = "ACM certificate of the HTTPS listener, covering public_url's host."
  type        = string
}

variable "ssl_policy" {
  type    = string
  default = "ELBSecurityPolicy-TLS13-1-2-2021-06"
}

variable "ingress_cidrs" {
  description = "IPv4 ranges allowed to reach the load balancer, e.g. GitHub's hook ranges plus your users'."
  type        = list(string)
  default     = ["0.0.0.0/0"]
}

variable "public_url" {
  description = "The server's URL as users reach it, e.g. \"https://canopy.example.com\"; review page links point here."
  type        = string

  validation {
    condition     = can(regex("^https?://[^/?#]+(/[^?#]*)?$", var.public_url))
    error_message = "public_url must be an absolute http(s) URL without a query or fragment."
  }
}

variable "secret_arns" {
  description = "Secrets Manager ARNs by the environment variable they configure (the secrets module's env_secret_arns)."
  type        = map(string)
}

variable "environment" {
  description = "Further non-secret CANOPY_* settings, e.g. { CANOPY_BLOCKING_LABELS = \"do not merge\" }."
  type        = map(string)
  default     = {}
}

variable "image_tag" {
  description = "Tag of the canopyweb image in the service's ECR repository."
  type        = string
}

variable "desired_count" {
  description = "How many tasks to run; 0 until an image has been pushed."
  type        = number
  default     = 2
}

variable "cpu" {
  type    = number
  default = 512
}

variable "memory" {
  type    = number
  default = 1024
}

variable "cpu_architecture" {
  description = "ARM64 or X86_64; the pushed image must include it."
  type        = string
  default     = "ARM64"
}

variable "log_retention_days" {
  type    = number
  default = 30
}

variable "ecr_keep_images" {
  type    = number
  default = 30
}

variable "secret_recovery_window_in_days" {
  type    = number
  default = 30
}

variable "db_engine_version" {
  description = "PostgreSQL major version; minor versions upgrade automatically."
  type        = string
  default     = "17"
}

variable "db_instance_class" {
  description = "Each task opens review_workers + 8 connections, so size max_connections to tasks × that."
  type        = string
  default     = "db.t4g.small"
}

variable "db_allocated_storage" {
  type    = number
  default = 20
}

variable "db_max_allocated_storage" {
  description = "Storage autoscaling's ceiling in GiB."
  type        = number
  default     = 100
}

variable "db_multi_az" {
  type    = bool
  default = false
}

variable "db_backup_retention_days" {
  type    = number
  default = 7
}

variable "db_deletion_protection" {
  type    = bool
  default = true
}

variable "db_final_snapshot" {
  description = "Snapshot the database when it is destroyed."
  type        = bool
  default     = true
}
