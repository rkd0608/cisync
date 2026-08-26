variable "environment" {
  description = "Environment slug used in the secret path prefix /cisync/<env>/...."
  type        = string
}

output "secret_arns" {
  description = "Map of logical name -> ARN; consumed by task definitions and RUNBOOK populate commands."
  value       = { for k, s in aws_secretsmanager_secret.this : k => s.arn }
}
