###############################################################################
# ingest task: GitHub-webhook edge ONLY (D2). 0.5 vCPU / 1 GB, desired=1.
# SCALE ROADMAP (not implemented v0.2): 1->2 on a webhook-backlog alarm metric
# (e.g. deliveries pending-forward gauge); safe because ingest is stateless
# beyond Postgres dedup — add aws_appautoscaling_target/policy when the
# backlog metric ships.
###############################################################################

resource "aws_ecs_task_definition" "ingest" {
  family                   = "${var.name_prefix}-ingest"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = 512
  memory                   = 1024
  execution_role_arn       = aws_iam_role.execution.arn
  task_role_arn            = aws_iam_role.task.arn

  container_definitions = jsonencode([
    {
      name         = "ingest"
      image        = "${var.ecr_urls["ingest"]}:${var.image_tag}"
      essential    = true
      portMappings = [{ name = "http", containerPort = 8080, protocol = "tcp" }]

      environment = [
        { name = "SAURON_INGEST_ADDR", value = ":8080" },
        { name = "SAURON_INGEST_CTRL_URL", value = "http://control-plane.sauron.local:8081" },
        # WEBHOOK_SECRETS (rotation list) is NOT set at boot: singular secret
        # below is primary. Rotation per RUNBOOK §6 adds the dual list via a
        # one-off task-def revision, then removes it after <=24h overlap.
      ]

      secrets = [
        { name = "SAURON_INGEST_PG_DSN", valueFrom = "${var.secret_arns["db_dsns"]}:ingest_dsn::" },
        { name = "SAURON_INGEST_WEBHOOK_SECRET", valueFrom = var.secret_arns["webhook_secret"] },
      ]

      logConfiguration = local.logs["ingest"]
    },
  ])

  tags = { Component = "ingest" }
}
