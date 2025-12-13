# RDS Aurora PostgreSQL Module for EchoStream

variable "name" {
  description = "Name prefix for RDS resources"
  type        = string
}

variable "vpc_id" {
  description = "VPC ID"
  type        = string
}

variable "subnet_ids" {
  description = "Subnet IDs for DB subnet group"
  type        = list(string)
}

variable "allowed_security_group_ids" {
  description = "Security group IDs allowed to access the database"
  type        = list(string)
}

variable "instance_class" {
  description = "Instance class for Aurora instances"
  type        = string
  default     = "db.t3.medium"
}

variable "instance_count" {
  description = "Number of Aurora instances"
  type        = number
  default     = 2
}

variable "database_name" {
  description = "Name of the database"
  type        = string
  default     = "echostream"
}

variable "master_username" {
  description = "Master username"
  type        = string
  default     = "echostream_admin"
}

variable "backup_retention_period" {
  description = "Backup retention period in days"
  type        = number
  default     = 7
}

variable "tags" {
  description = "Tags to apply to resources"
  type        = map(string)
  default     = {}
}

# Random password for master user
resource "random_password" "master" {
  length           = 32
  special          = true
  override_special = "!#$%&*()-_=+[]{}<>:?"
}

# Store password in Secrets Manager
resource "aws_secretsmanager_secret" "db_password" {
  name = "${var.name}-db-password"
  tags = var.tags
}

resource "aws_secretsmanager_secret_version" "db_password" {
  secret_id = aws_secretsmanager_secret.db_password.id
  secret_string = jsonencode({
    username = var.master_username
    password = random_password.master.result
    host     = aws_rds_cluster.main.endpoint
    port     = 5432
    database = var.database_name
  })
}

# DB Subnet Group
resource "aws_db_subnet_group" "main" {
  name        = "${var.name}-db-subnet-group"
  description = "Database subnet group for ${var.name}"
  subnet_ids  = var.subnet_ids

  tags = merge(var.tags, {
    Name = "${var.name}-db-subnet-group"
  })
}

# Security Group for RDS
resource "aws_security_group" "rds" {
  name        = "${var.name}-rds-sg"
  description = "Security group for RDS Aurora"
  vpc_id      = var.vpc_id

  ingress {
    from_port       = 5432
    to_port         = 5432
    protocol        = "tcp"
    security_groups = var.allowed_security_group_ids
    description     = "Allow PostgreSQL from app"
  }

  tags = merge(var.tags, {
    Name = "${var.name}-rds-sg"
  })
}

# Aurora Cluster
resource "aws_rds_cluster" "main" {
  cluster_identifier     = "${var.name}-aurora-cluster"
  engine                 = "aurora-postgresql"
  engine_version         = "15.4"
  database_name          = var.database_name
  master_username        = var.master_username
  master_password        = random_password.master.result
  db_subnet_group_name   = aws_db_subnet_group.main.name
  vpc_security_group_ids = [aws_security_group.rds.id]

  backup_retention_period = var.backup_retention_period
  preferred_backup_window = "03:00-04:00"
  storage_encrypted       = true

  skip_final_snapshot = true # Set to false for production

  tags = merge(var.tags, {
    Name = "${var.name}-aurora-cluster"
  })
}

# Aurora Instances
resource "aws_rds_cluster_instance" "main" {
  count                = var.instance_count
  identifier           = "${var.name}-aurora-instance-${count.index}"
  cluster_identifier   = aws_rds_cluster.main.id
  instance_class       = var.instance_class
  engine               = aws_rds_cluster.main.engine
  engine_version       = aws_rds_cluster.main.engine_version
  db_subnet_group_name = aws_db_subnet_group.main.name

  tags = merge(var.tags, {
    Name = "${var.name}-aurora-instance-${count.index}"
  })
}

# Outputs
output "cluster_endpoint" {
  value = aws_rds_cluster.main.endpoint
}

output "reader_endpoint" {
  value = aws_rds_cluster.main.reader_endpoint
}

output "database_name" {
  value = aws_rds_cluster.main.database_name
}

output "port" {
  value = aws_rds_cluster.main.port
}

output "security_group_id" {
  value = aws_security_group.rds.id
}

output "secret_arn" {
  value = aws_secretsmanager_secret.db_password.arn
}

output "jdbc_url" {
  value = "jdbc:postgresql://${aws_rds_cluster.main.endpoint}:${aws_rds_cluster.main.port}/${aws_rds_cluster.main.database_name}"
}
