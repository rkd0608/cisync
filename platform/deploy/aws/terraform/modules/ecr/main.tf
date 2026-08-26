###############################################################################
# ecr: one repository per deployable (ARCHITECTURE §2 service set + web).
# Scan-on-push gives baseline CVE visibility; MUTABLE tags chosen deliberately
# so push-images.sh can maintain a moving `latest` alongside immutable git-SHA
# tags — ECS deployments always pin the SHA via -var image_tag=..., never latest.
###############################################################################

locals {
  services = ["ingest", "control-plane", "runner-fleet", "github-connector", "web"]
}

resource "aws_ecr_repository" "this" {
  for_each = toset(local.services)

  name = "${var.name_prefix}-${each.value}"

  # `latest` is convenience-only for operators; real deploys pin git SHAs.
  image_tag_mutability = "MUTABLE"

  image_scanning_configuration {
    scan_on_push = true
  }

  # force_delete stays false on purpose: images are the only local copy of
  # deployed artifacts; teardown should be an explicit, eyeballed act.
  force_delete = false

  tags = { Component = each.value }
}
