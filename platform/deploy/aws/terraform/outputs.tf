###############################################################################
# Outputs the operator actually consumes (also echoed in RUNBOOK.md).
###############################################################################

output "alb_dns_name" {
  description = "Point your DNS here (or set hosted_zone_id to auto-record)."
  value       = module.alb.alb_dns_name
}

output "public_url" {
  description = "Canonical public base URL (empty if domain_name unset)."
  value       = var.domain_name != "" ? "https://${var.domain_name}" : ""
}

output "webhook_url" {
  description = "GitHub App webhook payload URL: paste into App settings."
  value       = var.domain_name != "" ? "https://${var.domain_name}/hooks/github" : "https://${module.alb.alb_dns_name}/hooks/github"
}

output "ecr_repository_urls" {
  description = "Push targets for push-images.sh (map of service -> repo URL)."
  value       = module.ecr.repo_urls
}

output "rds_endpoint" {
  description = "Postgres endpoint for DSN construction (RUNBOOK §5 builds per-service DSNs from it + master secret)."
  value       = module.rds.endpoint
}

output "rds_master_secret_arn" {
  description = "Secrets Manager ARN holding {username,password} — source for DSN population. NEVER logged; fetch only via CLI."
  value       = module.rds.master_secret_arn
}

output "secret_arns" {
  description = "ARNs to populate with put-secret-value (exact commands in RUNBOOK §5)."
  value       = module.secrets.secret_arns
}

output "cloudmap_namespace" {
  description = "Internal service discovery namespace; services resolve <name>.cisync.local."
  value       = "cisync.local"
}
