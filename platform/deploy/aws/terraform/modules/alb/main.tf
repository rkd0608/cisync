###############################################################################
# alb: public edge. HTTPS 443 (ACM var) + HTTP->HTTPS redirect.
# Routing contract (ARCHITECTURE D2: ingest is GitHub-webhooks-ONLY):
#   /hooks/github*   -> ingest TG (:8080)   [the only ingest exposure]
#   /api/sauron/*    -> web TG (:3000)      [same-origin proxy path]
#   default          -> web TG              [UI]
# control-plane/fleet/connector are NEVER internet-exposed; they are reached
# internally via CloudMap sauron.local.
###############################################################################

resource "aws_security_group" "alb" {
  name_prefix = "${var.name_prefix}-alb-"
  vpc_id      = var.vpc_id
  description = "Public ALB ingress"

  ingress {
    description = "HTTPS from allowed CIDRs"
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = var.allowed_cidrs
  }

  ingress {
    description = "HTTP for redirect only"
    from_port   = 80
    to_port     = 80
    protocol    = "tcp"
    cidr_blocks = var.allowed_cidrs
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_lb" "this" {
  name               = substr(var.name_prefix, 0, 26)
  internal           = false
  load_balancer_type = "application"
  security_groups    = [aws_security_group.alb.id]
  subnets            = var.public_subnet_ids

  drop_invalid_header_fields = true # hygiene for webhook signature parsing
}

resource "aws_lb_target_group" "ingest" {
  name                 = substr("${var.name_prefix}-ing", 0, 32)
  port                 = var.ingest_service_port
  protocol             = "HTTP"
  vpc_id               = var.vpc_id
  target_type          = "ip" # awsvpc mode: tasks register by ENI IP
  deregistration_delay = 15

  health_check {
    path                = "/healthz"
    matcher             = "200"
    interval            = 15
    healthy_threshold   = 2
    unhealthy_threshold = 5
  }
}

resource "aws_lb_target_group" "web" {
  name                 = substr("${var.name_prefix}-web", 0, 32)
  port                 = var.web_service_port
  protocol             = "HTTP"
  vpc_id               = var.vpc_id
  target_type          = "ip"
  deregistration_delay = 15

  # Next.js has no /healthz route; "/" is the honest liveness probe here.
  health_check {
    path                = "/"
    matcher             = "200-399"
    interval            = 30
    healthy_threshold   = 2
    unhealthy_threshold = 5
  }
}

resource "aws_lb_listener" "http_redirect" {
  load_balancer_arn = aws_lb.this.arn
  port              = 80
  protocol          = "HTTP"

  default_action {
    type = "redirect"
    redirect {
      port        = "443"
      protocol    = "HTTPS"
      status_code = "HTTP_301"
    }
  }
}

resource "aws_lb_listener" "https" {
  load_balancer_arn = aws_lb.this.arn
  port              = 443
  protocol          = "HTTPS"
  ssl_policy        = "ELBSecurityPolicy-TLS13-1-2-2021-06"
  certificate_arn   = var.certificate_arn

  # Rule ORDER matters: webhook path must win over the UI catch-all.
  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.web.arn
  }
}

resource "aws_lb_listener_rule" "github_webhooks" {
  listener_arn = aws_lb_listener.https.arn
  priority     = 10

  action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.ingest.arn
  }

  condition {
    path_pattern {
      values = ["/hooks/github*"]
    }
  }
}

resource "aws_lb_listener_rule" "api_proxy" {
  listener_arn = aws_lb_listener.https.arn
  priority     = 20

  action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.web.arn
  }

  condition {
    path_pattern {
      values = ["/api/sauron/*"]
    }
  }
}

# Optional auto-record: operator may prefer manual DNS (brief allows either).
data "aws_route53_zone" "this" {
  count        = var.domain_name != "" && var.hosted_zone_id != "" ? 1 : 0
  zone_id      = var.hosted_zone_id
  private_zone = false
}

resource "aws_route53_record" "alias" {
  count   = var.domain_name != "" && var.hosted_zone_id != "" ? 1 : 0
  zone_id = data.aws_route53_zone.this[0].zone_id
  name    = var.domain_name
  type    = "A"

  alias {
    name                   = aws_lb.this.dns_name
    zone_id                = aws_lb.this.zone_id
    evaluate_target_health = true
  }
}
