###############################################################################
# ecs: Fargate cluster, CloudMap namespace (sauron.local) + per-service
# registry entries, tasks SG. One service per component per ARCHITECTURE §2.
###############################################################################

resource "aws_ecs_cluster" "this" {
  name = "${var.name_prefix}-cluster"

  setting {
    name  = "containerInsights"
    value = "disabled" # COST: off at v0.2 scale; structured app logs carry signals
  }
}

resource "aws_cloudwatch_log_group" "services" {
  for_each          = toset(["ingest", "control-plane", "runner-fleet", "github-connector", "web"])
  name              = "/ecs/${var.name_prefix}/${each.value}"
  retention_in_days = var.log_retention_days
}

# Shared log config shape injected into every container definition.
locals {
  logs = {
    for svc in ["ingest", "control-plane", "runner-fleet", "github-connector", "web"] :
    svc => {
      logDriver = "awslogs"
      options = {
        awslogs-group         = aws_cloudwatch_log_group.services[svc].name
        awslogs-region        = var.region
        awslogs-stream-prefix = svc
      }
    }
  }
}

resource "aws_service_discovery_private_dns_namespace" "this" {
  name        = "sauron.local"
  description = "Internal service-to-service DNS (compose parity: http://control-plane:8081 etc.)"
  vpc         = var.vpc_id
}

resource "aws_service_discovery_service" "internal" {
  # ingest is included so the connector (or future W3 services) can reach it
  # internally without traversing the public ALB.
  for_each = toset(["control-plane", "runner-fleet", "github-connector", "ingest"])

  name = each.value

  dns_config {
    namespace_id = aws_service_discovery_private_dns_namespace.this.id

    dns_records {
      ttl  = 10
      type = "A"
    }
    routing_policy = "MULTIVALUE"
  }

  health_check_custom_config {
    failure_threshold = 3 # /healthz-backed ALB checks are the source of truth
  }
}
