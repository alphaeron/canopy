output "repository_url" {
  description = "Push canopyweb images here."
  value       = aws_ecr_repository.this.repository_url
}

output "load_balancer_dns_name" {
  description = "Point public_url's host here (CNAME or alias)."
  value       = aws_lb.this.dns_name
}

output "load_balancer_zone_id" {
  value = aws_lb.this.zone_id
}

output "cluster_name" {
  value = aws_ecs_cluster.this.name
}

output "service_name" {
  value = aws_ecs_service.this.name
}

output "log_group_name" {
  value = aws_cloudwatch_log_group.this.name
}
