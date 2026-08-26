output "vpc_id" {
  value = aws_vpc.this.id
}

output "public_subnet_ids" {
  description = "For the ALB only — tasks stay private."
  value       = aws_subnet.public[*].id
}

output "private_subnet_ids" {
  description = "ECS tasks + RDS live here; egress via NAT."
  value       = aws_subnet.private[*].id
}
