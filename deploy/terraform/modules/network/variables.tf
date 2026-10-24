variable "name" {
  description = "Name prefix for the network's resources."
  type        = string
}

variable "cidr_block" {
  description = "The VPC's IPv4 range; split into 16 subnets, public first, private from the ninth."
  type        = string
  default     = "10.40.0.0/16"
}

variable "az_count" {
  description = "How many availability zones to span. RDS and the load balancer need at least two."
  type        = number
  default     = 2

  validation {
    condition     = var.az_count >= 2 && var.az_count <= 8
    error_message = "az_count must be between 2 and 8."
  }
}

variable "single_nat_gateway" {
  description = "Share one NAT gateway between zones (cheaper) instead of one per zone (survives a zone outage)."
  type        = bool
  default     = true
}
