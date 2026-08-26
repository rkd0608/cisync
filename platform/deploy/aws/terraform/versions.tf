# Sauron AWS deployment kit — root Terraform (v0.2, ECS Fargate).
# Native-first charter: NO third-party modules; only hashicorp/aws provider.

terraform {
  required_version = ">= 1.6.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

provider "aws" {
  region = var.aws_region

  # Every resource in this kit is tagged Project=sauron (+ Environment) via
  # default_tags so cost reporting and teardown stay one query wide.
  default_tags {
    tags = {
      Project     = "sauron"
      Environment = var.environment
      ManagedBy   = "terraform"
    }
  }
}
