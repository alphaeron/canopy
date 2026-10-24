# The local environment's AWS resources, in LocalStack (deploy/local). The
# free LocalStack emulates Secrets Manager but not ECS, RDS, or load
# balancers, so this applies the same secrets module as production and
# docker compose stands in for the rest. Run through deploy/local/up.sh.

terraform {
  required_version = ">= 1.10"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.6"
    }
  }
}

provider "aws" {
  region     = "us-east-1"
  access_key = "test"
  secret_key = "test"

  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true

  endpoints {
    secretsmanager = var.localstack_endpoint
    sts            = var.localstack_endpoint
  }
}

module "secrets" {
  source = "../../modules/secrets"

  name                    = "canopy-local"
  recovery_window_in_days = 0

  manage_github_values  = true
  github_app_id         = var.github_app_id
  github_private_key    = var.github_private_key
  github_webhook_secret = var.github_webhook_secret
}

# ENV=secret-name pairs, space-separated, for up.sh's shell loop.
output "secret_env_pairs" {
  value = join(" ", [for env, name in module.secrets.env_secret_names : "${env}=${name}"])
}
