# SQS Module for EchoStream

variable "name" {
  description = "Name of the SQS queue"
  type        = string
}

variable "visibility_timeout_seconds" {
  description = "Visibility timeout for messages"
  type        = number
  default     = 30
}

variable "message_retention_seconds" {
  description = "Message retention period"
  type        = number
  default     = 1209600 # 14 days
}

variable "receive_wait_time_seconds" {
  description = "Long polling wait time"
  type        = number
  default     = 20
}

variable "max_receive_count" {
  description = "Max receives before moving to DLQ"
  type        = number
  default     = 3
}

variable "tags" {
  description = "Tags to apply to resources"
  type        = map(string)
  default     = {}
}

# Dead Letter Queue
resource "aws_sqs_queue" "dlq" {
  name                      = "${var.name}-dlq"
  message_retention_seconds = 1209600 # 14 days

  tags = merge(var.tags, {
    Name = "${var.name}-dlq"
  })
}

# Main Queue
resource "aws_sqs_queue" "main" {
  name                       = var.name
  visibility_timeout_seconds = var.visibility_timeout_seconds
  message_retention_seconds  = var.message_retention_seconds
  receive_wait_time_seconds  = var.receive_wait_time_seconds

  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.dlq.arn
    maxReceiveCount     = var.max_receive_count
  })

  tags = merge(var.tags, {
    Name = var.name
  })
}

# CloudWatch Alarm for DLQ
resource "aws_cloudwatch_metric_alarm" "dlq_messages" {
  alarm_name          = "${var.name}-dlq-messages"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 1
  metric_name         = "ApproximateNumberOfMessagesVisible"
  namespace           = "AWS/SQS"
  period              = 300
  statistic           = "Sum"
  threshold           = 0
  alarm_description   = "Messages in DLQ for ${var.name}"

  dimensions = {
    QueueName = aws_sqs_queue.dlq.name
  }

  tags = var.tags
}

# Outputs
output "queue_url" {
  value = aws_sqs_queue.main.url
}

output "queue_arn" {
  value = aws_sqs_queue.main.arn
}

output "queue_name" {
  value = aws_sqs_queue.main.name
}

output "dlq_url" {
  value = aws_sqs_queue.dlq.url
}

output "dlq_arn" {
  value = aws_sqs_queue.dlq.arn
}
