###############################################################################
# control-plane task. 1 vCPU / 2 GB, desired=1, NOT autoscaled in v0.2.
#
# WHY single-instance (binding decision): the scheduler is leaderless-by-
# assumption at one replica; a second instance would double-process the
# outbox/scheduler loops and corrupt WIP-cap accounting (leases are Postgres
# rows, but dispatch fencing is not yet multi-writer safe). HA roadmap (v0.3+):
# leader election via Postgres advisory lock or lease row, THEN N replicas.
###############################################################################

locals {
  cp_name = "${var.name_prefix}-control-plane"
}

resource "aws_ecs_task_definition" "control_plane" {
  family                   = local.cp_name
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = 1024
  memory                   = 2048
  execution_role_arn       = aws_iam_role.execution.arn
  task_role_arn            = aws_iam_role.task.arn

  volume {
    name = "keystore"
  }

  container_definitions = jsonencode([
    # Init-style keystore: materializes Ed25519 PEMs onto /keys. B6 interim
    # custody (KMS graduation later). essential=false + SUCCESS gate below.
    {
      name      = "keystore"
      image     = var.keystore_image
      essential = false
      command = [
        "sh", "-c",
        "printf '%s\n' \"$LEDGER_KEY_PEM\" > /keys/ledger_ed25519.key && printf '%s\n' \"$JOBLEASE_KEY_PEM\" > /keys/joblease_ed25519.key",
      ]
      environment = []
      # PEMs arrive as Secrets Manager values -> execution-role-injected env;
      # the container then materializes them onto the shared /keys volume.
      # NEVER inline key material in this task def.
      secrets = [
        { name = "LEDGER_KEY_PEM", valueFrom = var.secret_arns["ledger_key"] },
        { name = "JOBLEASE_KEY_PEM", valueFrom = var.secret_arns["joblease_key"] },
      ]
      mountPoints      = [{ sourceVolume = "keystore", containerPath = "/keys" }]
      logConfiguration = local.logs["control-plane"]
    },
    {
      name         = "control-plane"
      image        = "${var.ecr_urls["control-plane"]}:${var.image_tag}"
      essential    = true
      portMappings = [{ name = "http", containerPort = 8081, protocol = "tcp" }]
      dependsOn    = [{ containerName = "keystore", condition = "SUCCESS" }]
      mountPoints  = [{ sourceVolume = "keystore", containerPath = "/keys", readOnly = true }]

      environment = [
        { name = "SAURON_CTRL_ADDR", value = ":8081" },
        { name = "SAURON_CTRL_FLEET_URL", value = "http://runner-fleet.sauron.local:8082" },
        { name = "SAURON_CTRL_CONNECTOR_URL", value = "http://github-connector.sauron.local:8083" },
        { name = "SAURON_CTRL_LEDGER_KEY_FILE", value = "/keys/ledger_ed25519.key" },
        { name = "SAURON_CTRL_JOBLEASE_KEY_FILE", value = "/keys/joblease_ed25519.key" },
        { name = "SAURON_CTRL_VERIFY_INTERVAL", value = "24h" },     # SPEC §3 H3: nightly chain verify
        { name = "SAURON_CTRL_AUDIT_RETENTION_DAYS", value = "90" }, # B7 floor
        { name = "SAURON_CTRL_TRACKED_BASE_BRANCHES", value = var.tracked_base_branches },
        { name = "SAURON_CTRL_TENANT_ID", value = "org_01ARZ3NDEKTSV4RRFFQ69G5FAV" },
        # Dev-tuned knobs (RATE_LIMIT_PER_MIN/SCHED_BATCH/TICK_INTERVAL/
        # RECONCILE_INTERVAL/STALE_RUN_MAX_AGE/RELAY_*) deliberately LEFT AT
        # PROD DEFAULTS: compose values were harness-window sizing only.
      ]

      secrets = [
        { name = "SAURON_CTRL_PG_DSN", valueFrom = "${var.secret_arns["db_dsns"]}:ctrl_dsn::" },
        { name = "SAURON_CTRL_ADMIN_TOKEN", valueFrom = var.secret_arns["admin_token"] },
        { name = "SAURON_CTRL_WEBHOOK_SECRET", valueFrom = var.secret_arns["webhook_secret"] },
      ]

      logConfiguration = local.logs["control-plane"]
    },
  ])

  tags = { Component = "control-plane" }
}
