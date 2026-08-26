variable "name_prefix" {
  description = "Resource name prefix."
  type        = string
}

variable "vpc_id" {
  description = "VPC for ALB + target groups."
  type        = string
}

variable "public_subnet_ids" {
  description = "ALB lives in public subnets only."
  type        = list(string)
}

variable "certificate_arn" {
  description = "ACM cert ARN (must be ISSUED in-region before apply)."
  type        = string
}

variable "allowed_cidrs" {
  description = "CIDRs permitted to the HTTPS/HTTP listeners."
  type        = list(string)
}

variable "domain_name" {
  description = "Public hostname; empty disables Route53 auto-record."
  type        = string
  default     = ""
}

variable "hosted_zone_id" {
  description = "Route53 zone for auto-record; requires domain_name."
  type        = string
  default     = ""
}

variable "ingest_service_port" {
  description = "ingest container port (8080)."
  type        = number
}

variable "web_service_port" {
  description = "web container port (3000)."
  type        = number
}

output "alb_dns_name" {
  value = aws_lb.this.dns_name
}

output "alb_sg_id" {
  description = "Consumed by the ECS tasks SG ingress rules."
  value       = aws_security_group.alb.id
}

output "ingest_target_group_arn" {
  value = aws_lb_target_group.ingest.arn
}

output "web_target_group_arn" {
  value = aws_lb_target_group.web.arn
}
