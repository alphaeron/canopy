variable "localstack_endpoint" {
  type    = string
  default = "http://localhost:4566"
}

# up.sh supplies a throw-away app by default; set TF_VAR_github_* to a real
# development GitHub App to receive its webhooks.

variable "github_app_id" {
  type      = string
  sensitive = true
}

variable "github_private_key" {
  type      = string
  sensitive = true
}

variable "github_webhook_secret" {
  type      = string
  sensitive = true
}
