# The review server's secrets in Secrets Manager. The service receives each
# as the CANOPY_* environment variable it configures (docs/configuration.md).
#
# The keys canopyweb only needs to be long and the same on every replica
# (database_key, link_key) are generated here. The GitHub App's credentials
# come from GitHub: by default this module creates their secrets empty and an
# operator sets their values (see deploy/README.md), which keeps them out of
# the Terraform state. manage_github_values sets them from variables instead,
# for throw-away environments such as LocalStack.

# Names and values are kept apart because for_each cannot iterate sensitive
# values.
locals {
  github = {
    CANOPY_GITHUB_APP_ID         = "github/app_id"
    CANOPY_GITHUB_PRIVATE_KEY    = "github/private_key"
    CANOPY_GITHUB_WEBHOOK_SECRET = "github/webhook_secret"
  }
  github_values = {
    CANOPY_GITHUB_APP_ID         = var.github_app_id
    CANOPY_GITHUB_PRIVATE_KEY    = var.github_private_key
    CANOPY_GITHUB_WEBHOOK_SECRET = var.github_webhook_secret
  }
  generated = {
    CANOPY_DATABASE_KEY = "database_key"
    CANOPY_LINK_KEY     = "link_key"
  }
}

resource "aws_secretsmanager_secret" "github" {
  for_each = local.github

  name                    = "${var.name}/${each.value}"
  recovery_window_in_days = var.recovery_window_in_days
}

resource "aws_secretsmanager_secret_version" "github" {
  for_each = var.manage_github_values ? local.github : {}

  secret_id     = aws_secretsmanager_secret.github[each.key].id
  secret_string = local.github_values[each.key]
}

# Alphanumeric, so a value is safe in any configuration format; 64 such
# characters carry about 380 bits.
resource "random_password" "generated" {
  for_each = local.generated

  length  = 64
  special = false
}

resource "aws_secretsmanager_secret" "generated" {
  for_each = local.generated

  name                    = "${var.name}/${each.value}"
  recovery_window_in_days = var.recovery_window_in_days
}

resource "aws_secretsmanager_secret_version" "generated" {
  for_each = local.generated

  secret_id     = aws_secretsmanager_secret.generated[each.key].id
  secret_string = random_password.generated[each.key].result
}
