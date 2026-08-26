###############################################################################
# rds: single Postgres 16 — the SOLE state authority (D1: no Redis). Hardened
# per brief: encrypted storage, 14d PITR, deletion protection, PI OFF (cost),
# creds via native Secrets Manager integration (manage_master_user_password).
# Ingress: 5432 ONLY from the ECS tasks SG. No public accessibility.
###############################################################################

resource "aws_db_subnet_group" "this" {
  name       = "${var.name_prefix}-db"
  subnet_ids = var.private_subnet_ids
}

resource "aws_security_group" "rds" {
  name_prefix = "${var.name_prefix}-rds-"
  vpc_id      = var.vpc_id
  description = "Postgres ingress restricted to CISync ECS tasks"

  ingress {
    description     = "5432 from ECS task ENIs only"
    from_port       = 5432
    to_port         = 5432
    protocol        = "tcp"
    security_groups = var.allowed_ingress_sg_ids
  }

  # No egress rules on purpose: RDS replies over connection state; an
  # empty SG satisfies least-privilege audits.
  lifecycle { create_before_destroy = true }
}

resource "aws_db_instance" "postgres" {
  identifier     = "${var.name_prefix}-pg"
  engine         = "postgres"
  engine_version = var.engine_version
  instance_class = var.instance_class

  db_name  = "cisync"
  username = "cisync_admin"

  # Native Secrets Manager credential custody (no password in tfvars/state-
  # visible plaintext beyond what AWS manages): secret holds {username,password}.
  manage_master_user_password = true

  allocated_storage     = var.allocated_storage
  max_allocated_storage = var.max_allocated_storage
  storage_type          = "gp3"
  storage_encrypted     = true

  db_subnet_group_name   = aws_db_subnet_group.this.name
  vpc_security_group_ids = [aws_security_group.rds.id]
  publicly_accessible    = false
  multi_az               = var.multi_az

  backup_retention_period   = var.backup_retention_days
  backup_window             = "07:00-08:00"
  maintenance_window        = "sun:08:30-sun:09:30"
  copy_tags_to_snapshot     = true
  delete_automated_backups  = false
  skip_final_snapshot       = var.skip_final_snapshot
  final_snapshot_identifier = "${var.name_prefix}-pg-final"

  deletion_protection          = var.deletion_protection
  performance_insights_enabled = false # COST: off per brief; revisit with prod graduation
  auto_minor_version_upgrade   = true
  apply_immediately            = false

  tags = { Component = "rds" }
}
