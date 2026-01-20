# SQS Queue
resource "aws_sqs_queue" "notifications" {
  name                       = "${local.name}-notifications"
  visibility_timeout_seconds = 60
  message_retention_seconds  = 86400 # 24 hours
  receive_wait_time_seconds  = 20    # Long polling

  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.dlq.arn
    maxReceiveCount     = 5
  })

  tags = {
    Name = "${local.name}-notifications"
  }
}

resource "aws_sqs_queue" "dlq" {
  name                      = "${local.name}-notifications-dlq"
  message_retention_seconds = 1209600 # 14 days

  tags = {
    Name = "${local.name}-dlq"
  }
}

# SNS Topic for multi-channel routing
resource "aws_sns_topic" "notifications" {
  name = "${local.name}-notifications"

  tags = {
    Name = local.name
  }
}

# SNS Subscriptions - SQS for each channel
resource "aws_sns_topic_subscription" "email_queue" {
  topic_arn            = aws_sns_topic.notifications.arn
  protocol             = "sqs"
  endpoint             = aws_sqs_queue.notifications.arn
  filter_policy        = jsonencode({ channel = ["email"] })
  raw_message_delivery = true
}

# SQS Queue Policy for SNS
resource "aws_sqs_queue_policy" "notifications" {
  queue_url = aws_sqs_queue.notifications.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect    = "Allow"
        Principal = { Service = "sns.amazonaws.com" }
        Action    = "sqs:SendMessage"
        Resource  = aws_sqs_queue.notifications.arn
        Condition = {
          ArnEquals = {
            "aws:SourceArn" = aws_sns_topic.notifications.arn
          }
        }
      }
    ]
  })
}
