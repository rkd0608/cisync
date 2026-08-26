###############################################################################
# runner-fleet task: 1 vCPU / 2 GB, desired=1.
#
# !! NOT-FOR-PRODUCTION BANNER (THREAT_MODEL B5) !!
# CISYNC_FLEET_PROVIDER is pinned to `sim` here. The `docker` provider runs
# sibling containers via the host socket — IMPOSSIBLE on Fargate (no socket)
# and NOT-FOR-PRODUCTION everywhere until the B5 graduation checklist passes
# (gVisor/Firecracker isolation, egress allowlists). Real-isolation providers
# are interfaces-only per ARCHITECTURE §6 OUT-list; this deployment therefore
# validates orchestration, NOT sandbox hardening.
###############################################################################

resource "aws_ecs_task_definition" "runner_fleet" {
  family                   = "${var.name_prefix}-runner-fleet"
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
    {
      name      = "keystore"
      image     = var.keystore_image
      essential = false
      command = [
        "sh", "-c",
        "printf '%s\n' \"$JOBLEASE_PUB_PEM\" > /keys/joblease_ed25519.pub",
      ]
      environment      = []
      secrets          = [{ name = "JOBLEASE_PUB_PEM", valueFrom = var.secret_arns["joblease_pub_key"] }]
      mountPoints      = [{ sourceVolume = "keystore", containerPath = "/keys" }]
      logConfiguration = local.logs["runner-fleet"]
    },
    {
      name         = "runner-fleet"
      image        = "${var.ecr_urls["runner-fleet"]}:${var.image_tag}"
      essential    = true
      portMappings = [{ name = "http", containerPort = 8082, protocol = "tcp" }]
      dependsOn    = [{ containerName = "keystore", condition = "SUCCESS" }]
      mountPoints  = [{ sourceVolume = "keystore", containerPath = "/keys", readOnly = true }]

      environment = [
        { name = "CISYNC_FLEET_ADDR", value = ":8082" },
        # B2/I-04: lease verification MUST be on in prod — empty key disables
        # it and mutating fleet endpoints would trust unsigned claims.
        { name = "CISYNC_FLEET_JOBLEASYPUB_KEY_FILE", value = "/keys/joblease_ed25519.pub" },
        { name = "CISYNC_FLEET_PROVIDER", value = "sim" }, # NOT-FOR-PRODUCTION posture, see header
        # SIM_WORKERS left at default: compose's 48 was harness-window sizing.
      ]

      secrets = [
        { name = "CISYNC_FLEET_PG_DSN", valueFrom = "${var.secret_arns["db_dsns"]}:fleet_dsn::" },
      ]

      logConfiguration = local.logs["runner-fleet"]
    },
  ])

  tags = { Component = "runner-fleet" }
}
