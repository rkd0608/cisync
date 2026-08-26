###############################################################################
# github-connector task: 0.5 vCPU / 1 GB, desired=1.
# Dry-run by default (compose parity): without the GitHub App trio the
# connector logs would-be check payloads. enable_connector_live_mode=true
# wires CISYNC_CONN_GITHUB_{APP_ID,PRIVATE_KEY_FILE,INSTALLATION_ID} — config
# refuses to boot on a HALF-set trio (all-or-nothing), so all three flip
# together from tfvars + the populated PEM secret.
###############################################################################

locals {
  conn_keystore_secrets = var.connector_live_enabled ? [
    { name = "GITHUB_APP_PEM", valueFrom = var.secret_arns["github_app_private_key_pem"] },
  ] : []

  conn_live_env = var.connector_live_enabled ? [
    { name = "CISYNC_CONN_GITHUB_APP_ID", value = var.github_app_id },
    { name = "CISYNC_CONN_GITHUB_PRIVATE_KEY_FILE", value = "/keys/github-app.pem" },
    { name = "CISYNC_CONN_GITHUB_INSTALLATION_ID", value = var.github_installation_id },
  ] : []
}

resource "aws_ecs_task_definition" "github_connector" {
  family                   = "${var.name_prefix}-github-connector"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = 512
  memory                   = 1024
  execution_role_arn       = aws_iam_role.execution.arn
  task_role_arn            = aws_iam_role.task.arn

  volume {
    name = "keystore"
  }

  container_definitions = jsonencode([
    {
      name      = "keystore"
      image     = var.keystore_image
      essential = false
      # Live-mode only: no-op exit 0 when the PEM env is absent (dry-run).
      command = [
        "sh", "-c",
        "if [ -n \"$GITHUB_APP_PEM\" ]; then printf '%s\n' \"$GITHUB_APP_PEM\" > /keys/github-app.pem; fi",
      ]
      environment      = []
      secrets          = local.conn_keystore_secrets
      mountPoints      = [{ sourceVolume = "keystore", containerPath = "/keys" }]
      logConfiguration = local.logs["github-connector"]
    },
    {
      name         = "github-connector"
      image        = "${var.ecr_urls["github-connector"]}:${var.image_tag}"
      essential    = true
      portMappings = [{ name = "http", containerPort = 8083, protocol = "tcp" }]
      dependsOn    = [{ containerName = "keystore", condition = "SUCCESS" }]
      mountPoints  = [{ sourceVolume = "keystore", containerPath = "/keys", readOnly = true }]

      environment = concat([
        { name = "CISYNC_CONN_ADDR", value = ":8083" },
        # Rerun replan client (plan §4.5): ctrl endpoint + admin bearer.
        { name = "CISYNC_CONN_CTRL_URL", value = "http://control-plane.cisync.local:8081" },
        { name = "CISYNC_CONN_DETAILS_URL", value = var.connector_details_url },
      ], local.conn_live_env)

      secrets = [
        { name = "CISYNC_CONN_PG_DSN", valueFrom = "${var.secret_arns["db_dsns"]}:conn_dsn::" },
        { name = "CISYNC_CONN_WEBHOOK_SECRET", valueFrom = var.secret_arns["conn_webhook_secret"] },
        { name = "CISYNC_CONN_ADMIN_TOKEN", valueFrom = var.secret_arns["conn_admin_token"] },
        { name = "CISYNC_CONN_CTRL_TOKEN", valueFrom = var.secret_arns["admin_token"] },
      ]

      logConfiguration = local.logs["github-connector"]
    },
  ])

  tags = { Component = "github-connector" }
}
