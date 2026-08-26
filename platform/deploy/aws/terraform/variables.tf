###############################################################################
# CISync AWS kit — variables. Every knob an operator may need, with the WHY.
# Never put secret VALUES here; secrets are populated post-apply via
# `aws secretsmanager put-secret-value` (see RUNBOOK.md §5).
###############################################################################

variable "aws_region" {
  description = "AWS region for all resources. Pick one close to users AND with your ACM cert."
  type        = string
  default     = "us-east-1"
}

variable "environment" {
  description = "Deployment environment name (staging|prod). Used in resource names, tags, and Secrets Manager paths /cisync/<environment>/...."
  type        = string
  default     = "prod"

  validation {
    condition     = contains(["staging", "prod"], var.environment)
    error_message = "environment must be 'staging' or 'prod' (secret paths and names key off it)."
  }
}

# --- Network ---------------------------------------------------------------

variable "vpc_cidr" {
  description = "VPC CIDR. Two AZs each get a /20 slice of this; /16 leaves ample room."
  type        = string
  default     = "10.42.0.0/16"
}

variable "single_nat_gateway" {
  description = <<EOT
COST TOGGLE (flagged): one shared NAT gateway (~$33/mo + data) instead of one
per AZ (~$66/mo). True = cross-AZ NAT traffic on failover, acceptable for v0.2.
Set false for full per-AZ isolation at 2x NAT cost.
EOT
  type        = bool
  default     = true
}

# --- RDS --------------------------------------------------------------------

variable "db_instance_class" {
  description = "RDS instance class. db.t4g.small is the v0.2 stated size (~$12/mo single-AZ)."
  type        = string
  default     = "db.t4g.small"
}

variable "db_engine_version" {
  description = "Postgres major version line. ARCHITECTURE pins PG16; patch auto-managed by AWS within the line."
  type        = string
  default     = "16.6"
}

variable "db_allocated_storage" {
  description = "Initial storage GiB (gp3). max_allocated_storage lets it autoscale to db_max_allocated_storage without downtime."
  type        = number
  default     = 20
}

variable "db_max_allocated_storage" {
  description = "Storage autoscale ceiling GiB. Ledger rows are retained FOREVER by design (tamper-evidence IS the product) — size headroom accordingly."
  type        = number
  default     = 100
}

variable "db_multi_az" {
  description = "COST TOGGLE: Multi-AZ RDS doubles instance cost (~$12 -> ~$25/mo). v0.2 posture accepts single-AZ + 14d backups; flip for prod graduation."
  type        = bool
  default     = false
}

variable "db_backup_retention_days" {
  description = "PITR retention. 14d per deployment brief; dev posture graduation wants defined RPO — 14d PITR satisfies it."
  type        = number
  default     = 14
}

variable "db_deletion_protection" {
  description = "Blocks DeleteDBInstance API calls. Keep TRUE in prod; the append-only ledger must not be deletable by fat-finger."
  type        = bool
  default     = true
}

variable "db_skip_final_snapshot" {
  description = "Always false in prod: final snapshot on any (protected) teardown is the last-resort ledger copy."
  type        = bool
  default     = false
}

# --- ALB / DNS --------------------------------------------------------------

variable "certificate_arn" {
  description = "ACM certificate ARN for the public HTTPS listener. Operator prereq: cert requested + VALIDATED (DNS) in the same region BEFORE apply."
  type        = string
}

variable "domain_name" {
  description = "Public hostname for CISync (e.g. cisync.example.com). Empty = no Route53 record; operator wires DNS manually. Used as connector details-url base."
  type        = string
  default     = ""
}

variable "hosted_zone_id" {
  description = "Route53 hosted zone ID. Set together with domain_name to AUTO-create the A-alias record to the ALB; leave empty to manage DNS yourself."
  type        = string
  default     = ""
}

variable "allowed_admin_cidrs" {
  description = "CIDRs allowed to reach the ALB HTTPS listener. Default world-open because GitHub webhook source IPs are unlisted ranges; tighten to GitHub pubsub lists + office CIDR if you accept the operational risk of missed hooks."
  type        = list(string)
  default     = ["0.0.0.0/0"]
}

# --- Images / services ------------------------------------------------------

variable "image_tag" {
  description = "Image tag pulled by ECS tasks. Pass the git SHA you pushed via push-images.sh: -var image_tag=$(git rev-parse --short HEAD). 'latest' only for first bootstrapping."
  type        = string
  default     = "latest"
}

variable "enable_services" {
  description = <<EOT
Create the 5 ECS services. FIRST apply should pass FALSE: images are not pushed
and secrets are empty yet, so tasks would crash-loop into the circuit breaker.
Order per RUNBOOK: apply(enable_services=false) -> push-images -> populate
secrets -> apply(enable_services=true).
EOT
  type        = bool
  default     = false
}

variable "enable_connector_live_mode" {
  description = <<EOT
GitHub App LIVE check publishing for github-connector (G14 trio). False keeps
the compose-parity dry-run posture (would-be payloads logged, nothing written
to GitHub). Flipping true requires github_app_id/github_installation_id set and
the github_app_private_key_pem secret populated.
EOT
  type        = bool
  default     = false
}

variable "github_app_id" {
  description = "GitHub App numeric ID (CISYNC_CONN_GITHUB_APP_ID). Required iff enable_connector_live_mode."
  type        = string
  default     = ""
}

variable "github_installation_id" {
  description = "Default installation ID from the App install URL (CISYNC_CONN_GITHUB_INSTALLATION_ID). Required iff enable_connector_live_mode."
  type        = string
  default     = ""
}

variable "connector_details_url" {
  description = "Public URL stamped into GitHub Check details links (CISYNC_CONN_DETAILS_URL). Should be https://<domain_name>; kept separate so staging/prod diverge cleanly."
  type        = string
  default     = ""
}

variable "tracked_base_branches" {
  description = "Base branches whose pushes advance merge-base and supersede stale candidates (CISYNC_CTRL_TRACKED_BASE_BRANCHES). Comma-separated; keep in sync with branch protection."
  type        = string
  default     = "main,master"
}

variable "keystore_image" {
  description = "Init-container image that materializes PEM secrets onto the shared /keys volume. Public ECR mirror of alpine avoids Docker Hub rate limits."
  type        = string
  default     = "public.ecr.aws/docker/library/alpine:3.20"
}

variable "log_retention_days" {
  description = "CloudWatch log retention per service. THREAT_MODEL B7 audit-grade events live in structured logs until B7-dedicated-stream graduation; 90d matches the audit floor."
  type        = number
  default     = 90
}
