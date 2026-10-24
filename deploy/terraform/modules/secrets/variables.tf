variable "name" {
  description = "Prefix of the secrets' names, e.g. \"canopy-prod\" names \"canopy-prod/link_key\"."
  type        = string
}

variable "recovery_window_in_days" {
  description = "How long a deleted secret can be restored; 0 deletes at once, so its name is free again."
  type        = number
  default     = 30
}

variable "manage_github_values" {
  description = "Set the GitHub App secrets from the github_* variables, which stores them in the Terraform state. Leave false outside throw-away environments."
  type        = bool
  default     = false
}

variable "github_app_id" {
  description = "The GitHub App's numeric ID; used only with manage_github_values."
  type        = string
  default     = null
  sensitive   = true
}

variable "github_private_key" {
  description = "The GitHub App's RSA private key (PEM); used only with manage_github_values."
  type        = string
  default     = null
  sensitive   = true
}

variable "github_webhook_secret" {
  description = "The GitHub App's webhook secret; used only with manage_github_values."
  type        = string
  default     = null
  sensitive   = true
}
