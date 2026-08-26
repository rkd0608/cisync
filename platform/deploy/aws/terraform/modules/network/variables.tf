variable "name_prefix" {
  description = "Resource name prefix, e.g. cisync-prod."
  type        = string
}

variable "vpc_cidr" {
  description = "VPC CIDR block."
  type        = string
}

variable "az_count" {
  description = "Number of AZs (v0.2 brief: exactly 2)."
  type        = number
  default     = 2
}

variable "single_nat_gateway" {
  description = "COST TOGGLE: one shared NAT gateway vs one per AZ."
  type        = bool
  default     = true
}
