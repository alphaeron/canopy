output "env_secret_arns" {
  description = "Each secret's ARN by the environment variable it configures."
  value = merge(
    { for env, s in aws_secretsmanager_secret.github : env => s.arn },
    { for env, s in aws_secretsmanager_secret.generated : env => s.arn },
  )
}

output "env_secret_names" {
  description = "Each secret's name by the environment variable it configures."
  value = merge(
    { for env, s in aws_secretsmanager_secret.github : env => s.name },
    { for env, s in aws_secretsmanager_secret.generated : env => s.name },
  )
}
