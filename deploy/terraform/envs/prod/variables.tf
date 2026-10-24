variable "region" {
  type    = string
  default = "us-east-1"
}

variable "environment" {
  description = "Environment name; resources are named canopy-<environment>."
  type        = string
  default     = "prod"
}

variable "public_url" {
  description = "The server's URL as users reach it, e.g. \"https://canopy.example.com\"."
  type        = string
}

variable "certificate_arn" {
  description = "ACM certificate in var.region covering public_url's host."
  type        = string
}

variable "ingress_cidrs" {
  description = "IPv4 ranges allowed to reach the load balancer."
  type        = list(string)
  default     = ["0.0.0.0/0"]
}

variable "image_tag" {
  description = "canopyweb image tag to run; see deploy/README.md."
  type        = string
}

variable "desired_count" {
  description = "Tasks to run; 0 for the first apply, before an image and the GitHub secrets exist."
  type        = number
  default     = 2
}

variable "canopy_environment" {
  description = "Non-secret CANOPY_* settings, e.g. { CANOPY_REVIEW_WORKERS = \"16\" }."
  type        = map(string)
  default     = {}
}

variable "single_nat_gateway" {
  type    = bool
  default = true
}

variable "db_instance_class" {
  type    = string
  default = "db.t4g.small"
}

variable "db_multi_az" {
  type    = bool
  default = false
}
