###############################################################################
# Tasks SG. Ingress matrix:
#   - service ports (8080-8083, 3000) from the ALB only
#   - 8080-8083 self-ingress for INTERNAL hops: ingest->ctrl, ctrl->fleet,
#     ctrl->connector, web->ctrl/connector via sauron.local (compose parity)
# Egress all (NAT): ECR pulls, GitHub API, DB via private path.
###############################################################################

resource "aws_security_group" "tasks" {
  name_prefix = "${var.name_prefix}-tasks-"
  vpc_id      = var.vpc_id
  description = "Sauron ECS task ENIs"

  ingress {
    description     = "Service ports from public ALB"
    from_port       = 3000
    to_port         = 3000
    protocol        = "tcp"
    security_groups = [var.alb_sg_id]
  }

  ingress {
    description     = "ingest 8080 from public ALB (GitHub webhook edge)"
    from_port       = 8080
    to_port         = 8080
    protocol        = "tcp"
    security_groups = [var.alb_sg_id]
  }

  ingress {
    description = "Internal mesh: ctrl<->fleet<->connector, web->ctrl/conn"
    from_port   = 8081
    to_port     = 8083
    protocol    = "tcp"
    self        = true
  }

  egress {
    description = "All egress via NAT (ECR, RDS, GitHub API)"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}
