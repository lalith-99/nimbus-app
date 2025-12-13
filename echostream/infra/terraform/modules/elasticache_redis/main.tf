# ElastiCache Redis Module for EchoStream

variable "name" {
  description = "Name prefix for ElastiCache resources"
  type        = string
}

variable "vpc_id" {
  description = "VPC ID"
  type        = string
}

variable "subnet_ids" {
  description = "Subnet IDs for ElastiCache subnet group"
  type        = list(string)
}

variable "allowed_security_group_ids" {
  description = "Security group IDs allowed to access Redis"
  type        = list(string)
}

variable "node_type" {
  description = "ElastiCache node type"
  type        = string
  default     = "cache.t3.medium"
}

variable "num_cache_clusters" {
  description = "Number of cache clusters (nodes)"
  type        = number
  default     = 2
}

variable "engine_version" {
  description = "Redis engine version"
  type        = string
  default     = "7.0"
}

variable "tags" {
  description = "Tags to apply to resources"
  type        = map(string)
  default     = {}
}

# Subnet Group
resource "aws_elasticache_subnet_group" "main" {
  name        = "${var.name}-redis-subnet-group"
  description = "Subnet group for ${var.name} Redis"
  subnet_ids  = var.subnet_ids

  tags = merge(var.tags, {
    Name = "${var.name}-redis-subnet-group"
  })
}

# Security Group for Redis
resource "aws_security_group" "redis" {
  name        = "${var.name}-redis-sg"
  description = "Security group for ElastiCache Redis"
  vpc_id      = var.vpc_id

  ingress {
    from_port       = 6379
    to_port         = 6379
    protocol        = "tcp"
    security_groups = var.allowed_security_group_ids
    description     = "Allow Redis from app"
  }

  tags = merge(var.tags, {
    Name = "${var.name}-redis-sg"
  })
}

# Parameter Group for Redis 7
resource "aws_elasticache_parameter_group" "main" {
  name   = "${var.name}-redis-params"
  family = "redis7"

  # Enable keyspace notifications for pub/sub
  parameter {
    name  = "notify-keyspace-events"
    value = "AKE"
  }

  tags = var.tags
}

# Redis Replication Group
resource "aws_elasticache_replication_group" "main" {
  replication_group_id       = "${var.name}-redis"
  description                = "Redis cluster for ${var.name}"
  node_type                  = var.node_type
  num_cache_clusters         = var.num_cache_clusters
  engine_version             = var.engine_version
  port                       = 6379
  parameter_group_name       = aws_elasticache_parameter_group.main.name
  subnet_group_name          = aws_elasticache_subnet_group.main.name
  security_group_ids         = [aws_security_group.redis.id]
  automatic_failover_enabled = var.num_cache_clusters > 1
  multi_az_enabled           = var.num_cache_clusters > 1
  at_rest_encryption_enabled = true
  transit_encryption_enabled = true

  tags = merge(var.tags, {
    Name = "${var.name}-redis"
  })
}

# Outputs
output "primary_endpoint" {
  value = aws_elasticache_replication_group.main.primary_endpoint_address
}

output "reader_endpoint" {
  value = aws_elasticache_replication_group.main.reader_endpoint_address
}

output "port" {
  value = aws_elasticache_replication_group.main.port
}

output "security_group_id" {
  value = aws_security_group.redis.id
}
