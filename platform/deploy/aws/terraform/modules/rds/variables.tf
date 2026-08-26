variable "name_prefix" {
  description = "Resource name prefix, e.g. cisync-prod."
  type        = string
}

variable "vpc_id" {
  description = "VPC hosting the database."
  type        = string
}

variable "private_subnet_ids" {
  description = "DB subnet group members (private only; DB is never public)."
  type        = list(string)
}

variable "allowed_ingress_sg_ids" {
  description = "Security groups permitted to open 5432. Exactly the ECS tasks SG — no ALB, no bastion."
  type        = list(string)
}

variable "instance_class" {
  description = "RDS instance class (brief: db.t4g.small)."
  type        = string
}

variable "engine_version" {
  description = "Postgres engine version (PG16 line per ARCHITECTURE §2)."
  type        = string
}

variable "allocated_storage" {
  description = "Initial gp3 GiB."
  type        = number
}

variable "max_allocated_storage" {
  description = "Storage autoscale ceiling GiB (ledger grows forever by design)."
  type        = number
}

variable "multi_az" {
  description = "COST TOGGLE: synchronous standby. v0.2 single-AZ."
  type        = bool
  default     = false
}

variable "backup_retention_days" {
  description = "PITR window days (brief: 14)."
  type        = number
  default     = 14
}

variable "deletion_protection" {
  description = "API-level delete block (brief: true)."
  type        = bool
  default     = true
}

variable "skip_final_snapshot" {
  description = "Keep false: final snapshot is the last-resort ledger copy."
  type        = bool
  default     = false
}

output "endpoint" {
  description = "host:port for DSN construction."
  value       = aws_db_instance.postgres.address
}

output "master_secret_arn" {
  description = "ARN of AWS-managed master credential secret {username,password}."
  value       = aws_db_instance.postgres.master_user_secret[0].secret_arn
}
