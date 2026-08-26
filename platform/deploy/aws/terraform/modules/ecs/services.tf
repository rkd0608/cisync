###############################################################################
# ECS services: one per component, private-subnet awsvpc ENIs, CloudMap
# registration for internal hops. Circuit breaker + auto-ROLLBACK keeps a bad
# deploy from wedging the fleet (rollback notes: RUNBOOK §8).
###############################################################################

locals {
  services = {
    control-plane    = { task = aws_ecs_task_definition.control_plane.arn, port = 8081, registry = true }
    ingest           = { task = aws_ecs_task_definition.ingest.arn, port = 8080, registry = true }
    runner-fleet     = { task = aws_ecs_task_definition.runner_fleet.arn, port = 8082, registry = true }
    github-connector = { task = aws_ecs_task_definition.github_connector.arn, port = 8083, registry = true }
    web              = { task = aws_ecs_task_definition.web.arn, port = 3000, registry = false }
  }

  # ALB-attached services register targets; the rest are internal-only.
  lb_attachments = {
    ingest = { tg = var.ingest_target_group_arn, container = "ingest", port = 8080 }
    web    = { tg = var.web_target_group_arn, container = "web", port = 3000 }
  }
}

resource "aws_ecs_service" "this" {
  for_each = var.enable_services ? local.services : {}

  name            = "${var.name_prefix}-${each.key}"
  cluster         = aws_ecs_cluster.this.id
  task_definition = each.value.task
  desired_count   = 1

  launch_type = "FARGATE"

  network_configuration {
    subnets          = var.private_subnet_ids
    security_groups  = [aws_security_group.tasks.id]
    assign_public_ip = false
  }

  # Migrations + key materialization run before /healthz is served.
  health_check_grace_period_seconds = 120

  deployment_circuit_breaker {
    enable   = true
    rollback = true
  }

  deployment_maximum_percent         = 100 # single-instance ctrl: never 2 at once
  deployment_minimum_healthy_percent = 0   # allow stop-before-start on replacements

  dynamic "load_balancer" {
    for_each = { for k, v in local.lb_attachments : k => v if k == each.key }
    content {
      target_group_arn = load_balancer.value.tg
      container_name   = load_balancer.value.container
      container_port   = load_balancer.value.port
    }
  }

  dynamic "service_registries" {
    for_each = each.value.registry ? [1] : []
    content {
      registry_arn   = aws_service_discovery_service.internal[each.key].arn
      container_name = each.key
      container_port = each.value.port
    }
  }

  depends_on = [aws_cloudwatch_log_group.services]
}
