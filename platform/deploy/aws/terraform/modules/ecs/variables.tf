variable "name_prefix" {
  description = "Resource name prefix."
  type        = string
}

variable "environment" {
  description = "Environment slug (tags)."
  type        = string
}

variable "region" {
  description = "Region for awslogs driver options."
  type        = string
}

variable "vpc_id" {
  description = "VPC hosting tasks and CloudMap namespace."
  type        = string
}

variable "private_subnet_ids" {
  description = "Task ENI subnets (private; egress via NAT)."
  type        = list(string)
}

variable "alb_sg_id" {
  description = "ALB SG — the ONLY non-self ingress source to task ports."
  type        = string
}

variable "image_tag" {
  description = "Image tag ECS pulls (git SHA pushed by push-images.sh)."
  type        = string
}

variable "enable_services" {
  description = "Create services? First apply=false until images+secrets exist (RUNBOOK ordering)."
  type        = bool
}

variable "keystore_image" {
  description = "Init image materializing PEM secrets onto /keys."
  type        = string
}

variable "log_retention_days" {
  description = "CloudWatch retention per service log group."
  type        = number
}

variable "tracked_base_branches" {
  description = "SAURON_CTRL_TRACKED_BASE_BRANCHES value."
  type        = string
}

variable "connector_details_url" {
  description = "SAURON_CONN_DETAILS_URL (public web base for check links)."
  type        = string
}

variable "connector_live_enabled" {
  description = "Wire the GitHub App live-mode trio into the connector task."
  type        = bool
}

variable "github_app_id" {
  description = "SAURON_CONN_GITHUB_APP_ID (live mode)."
  type        = string
}

variable "github_installation_id" {
  description = "SAURON_CONN_GITHUB_INSTALLATION_ID (live mode)."
  type        = string
}

variable "ecr_urls" {
  description = "service -> ECR repo URL map from the ecr module."
  type        = map(string)
}

variable "secret_arns" {
  description = "logical name -> Secrets Manager ARN map from the secrets module."
  type        = map(string)
}

variable "ingest_target_group_arn" {
  description = "ALB TG for /hooks/github*."
  type        = string
}

variable "web_target_group_arn" {
  description = "ALB TG for /api/sauron/* + UI default."
  type        = string
}

output "ecs_tasks_sg_id" {
  description = "Consumed by RDS module: 5432 ingress allowed ONLY from this SG."
  value       = aws_security_group.tasks.id
}
