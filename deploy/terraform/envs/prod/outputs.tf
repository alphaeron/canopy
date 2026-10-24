output "repository_url" {
  value = module.service.repository_url
}

output "load_balancer_dns_name" {
  value = module.service.load_balancer_dns_name
}

output "cluster_name" {
  value = module.service.cluster_name
}

output "service_name" {
  value = module.service.service_name
}

output "log_group_name" {
  value = module.service.log_group_name
}

output "github_secret_names" {
  description = "Set these secrets' values from the GitHub App before running tasks."
  value = {
    for env, name in module.secrets.env_secret_names : env => name
    if startswith(env, "CANOPY_GITHUB_")
  }
}
