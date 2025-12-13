# ECS Cluster Module for EchoStream

variable "name" {
  description = "Name of the ECS cluster"
  type        = string
}

variable "tags" {
  description = "Tags to apply to resources"
  type        = map(string)
  default     = {}
}

# ECS Cluster
resource "aws_ecs_cluster" "main" {
  name = var.name

  setting {
    name  = "containerInsights"
    value = "enabled"
  }

  tags = merge(var.tags, {
    Name = var.name
  })
}

# Cluster Capacity Providers
resource "aws_ecs_cluster_capacity_providers" "main" {
  cluster_name = aws_ecs_cluster.main.name

  capacity_providers = ["FARGATE", "FARGATE_SPOT"]

  default_capacity_provider_strategy {
    base              = 1
    weight            = 100
    capacity_provider = "FARGATE"
  }
}

# Outputs
output "cluster_id" {
  value = aws_ecs_cluster.main.id
}

output "cluster_arn" {
  value = aws_ecs_cluster.main.arn
}

output "cluster_name" {
  value = aws_ecs_cluster.main.name
}
