# EchoStream Dev Environment
# Terraform configuration for development deployment

terraform {
  required_version = ">= 1.5.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.5"
    }
  }

  # Uncomment for remote state
  # backend "s3" {
  #   bucket         = "echostream-terraform-state"
  #   key            = "dev/terraform.tfstate"
  #   region         = "us-east-1"
  #   encrypt        = true
  #   dynamodb_table = "echostream-terraform-locks"
  # }
}

provider "aws" {
  region = var.aws_region

  default_tags {
    tags = {
      Project     = "echostream"
      Environment = "dev"
      ManagedBy   = "terraform"
    }
  }
}

# Variables
variable "aws_region" {
  description = "AWS region"
  type        = string
  default     = "us-east-1"
}

variable "environment" {
  description = "Environment name"
  type        = string
  default     = "dev"
}

variable "gateway_image" {
  description = "Gateway service Docker image"
  type        = string
  default     = "echostream/gateway:latest"
}

variable "fanout_image" {
  description = "Fanout service Docker image"
  type        = string
  default     = "echostream/fanout:latest"
}

locals {
  name = "echostream-${var.environment}"
  tags = {
    Project     = "echostream"
    Environment = var.environment
  }
}

# VPC
module "vpc" {
  source = "../../modules/vpc"

  name = local.name
  cidr = "10.0.0.0/16"
  azs  = ["${var.aws_region}a", "${var.aws_region}b", "${var.aws_region}c"]

  private_subnets  = ["10.0.1.0/24", "10.0.2.0/24", "10.0.3.0/24"]
  public_subnets   = ["10.0.101.0/24", "10.0.102.0/24", "10.0.103.0/24"]
  database_subnets = ["10.0.201.0/24", "10.0.202.0/24", "10.0.203.0/24"]

  tags = local.tags
}

# RDS Aurora PostgreSQL
module "rds" {
  source = "../../modules/rds_aurora"

  name                       = local.name
  vpc_id                     = module.vpc.vpc_id
  subnet_ids                 = module.vpc.database_subnet_ids
  allowed_security_group_ids = [module.vpc.app_security_group_id]

  instance_class = "db.t3.medium"
  instance_count = 1 # Single instance for dev

  tags = local.tags
}

# ElastiCache Redis
module "redis" {
  source = "../../modules/elasticache_redis"

  name                       = local.name
  vpc_id                     = module.vpc.vpc_id
  subnet_ids                 = module.vpc.private_subnet_ids
  allowed_security_group_ids = [module.vpc.app_security_group_id]

  node_type          = "cache.t3.micro"
  num_cache_clusters = 1 # Single node for dev

  tags = local.tags
}

# ECS Cluster
module "ecs_cluster" {
  source = "../../modules/ecs_cluster"

  name = local.name
  tags = local.tags
}

# Application Load Balancer
module "alb" {
  source = "../../modules/alb"

  name              = local.name
  vpc_id            = module.vpc.vpc_id
  public_subnet_ids = module.vpc.public_subnet_ids

  # certificate_arn = "arn:aws:acm:..." # Uncomment for HTTPS

  tags = local.tags
}

# SQS Queues
module "sqs_messages" {
  source = "../../modules/sqs"

  name                       = "${local.name}-message-events"
  visibility_timeout_seconds = 60
  max_receive_count          = 3

  tags = local.tags
}

# Gateway Service
module "gateway_service" {
  source = "../../modules/ecs_service"

  name               = "${local.name}-gateway"
  cluster_arn        = module.ecs_cluster.cluster_arn
  vpc_id             = module.vpc.vpc_id
  subnet_ids         = module.vpc.private_subnet_ids
  security_group_ids = [module.vpc.app_security_group_id]

  container_image  = var.gateway_image
  container_port   = 8080
  cpu              = 512
  memory           = 1024
  desired_count    = 2
  target_group_arn = module.alb.gateway_target_group_arn

  environment_variables = {
    SPRING_PROFILES_ACTIVE = "dev"
    DB_URL                 = module.rds.jdbc_url
    REDIS_HOST             = module.redis.primary_endpoint
    REDIS_PORT             = "6379"
    SQS_MESSAGE_QUEUE_URL  = module.sqs_messages.queue_url
    SQS_ENABLED            = "true"
    AWS_REGION             = var.aws_region
  }

  secrets = [
    {
      name      = "DB_USERNAME"
      valueFrom = "${module.rds.secret_arn}:username::"
    },
    {
      name      = "DB_PASSWORD"
      valueFrom = "${module.rds.secret_arn}:password::"
    }
  ]

  tags = local.tags
}

# Fanout Service
module "fanout_service" {
  source = "../../modules/ecs_service"

  name               = "${local.name}-fanout"
  cluster_arn        = module.ecs_cluster.cluster_arn
  vpc_id             = module.vpc.vpc_id
  subnet_ids         = module.vpc.private_subnet_ids
  security_group_ids = [module.vpc.app_security_group_id]

  container_image = var.fanout_image
  container_port  = 8081
  cpu             = 256
  memory          = 512
  desired_count   = 2

  environment_variables = {
    SPRING_PROFILES_ACTIVE  = "dev"
    DB_URL                  = module.rds.jdbc_url
    REDIS_HOST              = module.redis.primary_endpoint
    REDIS_PORT              = "6379"
    SQS_MESSAGE_QUEUE_NAME  = module.sqs_messages.queue_name
    SQS_ENABLED             = "true"
    AWS_REGION              = var.aws_region
  }

  secrets = [
    {
      name      = "DB_USERNAME"
      valueFrom = "${module.rds.secret_arn}:username::"
    },
    {
      name      = "DB_PASSWORD"
      valueFrom = "${module.rds.secret_arn}:password::"
    }
  ]

  tags = local.tags
}

# Outputs
output "alb_dns_name" {
  description = "ALB DNS name for accessing the API"
  value       = module.alb.alb_dns_name
}

output "api_endpoint" {
  description = "API endpoint"
  value       = "http://${module.alb.alb_dns_name}/api/v1"
}

output "websocket_endpoint" {
  description = "WebSocket endpoint"
  value       = "ws://${module.alb.alb_dns_name}/ws"
}

output "rds_endpoint" {
  description = "RDS endpoint"
  value       = module.rds.cluster_endpoint
}

output "redis_endpoint" {
  description = "Redis endpoint"
  value       = module.redis.primary_endpoint
}

output "sqs_queue_url" {
  description = "SQS queue URL for messages"
  value       = module.sqs_messages.queue_url
}
