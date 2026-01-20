terraform {
  required_version = ">= 1.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }

  # Using local backend for simplicity. For production/team use, uncomment S3 backend below.
  # backend "s3" {
  #   bucket         = "nimbus-terraform-state-ACCOUNT_ID"  # Replace ACCOUNT_ID
  #   key            = "nimbus/terraform.tfstate"
  #   region         = "us-east-1"
  #   encrypt        = true
  # }
}

provider "aws" {
  region = var.aws_region

  default_tags {
    tags = {
      Project     = "nimbus"
      Environment = var.environment
      ManagedBy   = "terraform"
    }
  }
}

data "aws_availability_zones" "available" {
  state = "available"
}

locals {
  name = "nimbus-${var.environment}"
  azs  = slice(data.aws_availability_zones.available.names, 0, 2)
}
