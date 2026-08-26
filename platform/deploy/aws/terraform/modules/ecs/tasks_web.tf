###############################################################################
# web task: Next.js UI, 0.5 vCPU / 1 GB. Server-side proxy (next.config.ts
# rewrites /api/cisync/*) means NOTHING is needed at build time — no
# NEXT_PUBLIC_* build args (runtime proxy decision, SPEC §3 integrator row).
###############################################################################

resource "aws_ecs_task_definition" "web" {
  family                   = "${var.name_prefix}-web"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = 512
  memory                   = 1024
  execution_role_arn       = aws_iam_role.execution.arn
  task_role_arn            = aws_iam_role.task.arn

  container_definitions = jsonencode([
    {
      name         = "web"
      image        = "${var.ecr_urls["web"]}:${var.image_tag}"
      essential    = true
      portMappings = [{ name = "http", containerPort = 3000, protocol = "tcp" }]

      environment = [
        { name = "CISYNC_API_URL", value = "http://control-plane.cisync.local:8081" },
        { name = "CISYNC_CONNECTOR_URL", value = "http://github-connector.cisync.local:8083" },
        { name = "PORT", value = "3000" },
      ]

      secrets = [
        # Same admin bearer as control-plane: injected server-side per SPEC §3
        # UI data-path fix; never shipped to the browser.
        { name = "CISYNC_ADMIN_TOKEN", valueFrom = var.secret_arns["admin_token"] },
      ]

      logConfiguration = local.logs["web"]
    },
  ])

  tags = { Component = "web" }
}
