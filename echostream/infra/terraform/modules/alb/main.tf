# Application Load Balancer Module for EchoStream

variable "name" {
  description = "Name prefix for ALB resources"
  type        = string
}

variable "vpc_id" {
  description = "VPC ID"
  type        = string
}

variable "public_subnet_ids" {
  description = "Public subnet IDs for the ALB"
  type        = list(string)
}

variable "certificate_arn" {
  description = "ACM certificate ARN for HTTPS (optional)"
  type        = string
  default     = null
}

variable "tags" {
  description = "Tags to apply to resources"
  type        = map(string)
  default     = {}
}

# Security Group for ALB
resource "aws_security_group" "alb" {
  name        = "${var.name}-alb-sg"
  description = "Security group for ALB"
  vpc_id      = var.vpc_id

  ingress {
    from_port   = 80
    to_port     = 80
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
    description = "Allow HTTP"
  }

  ingress {
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
    description = "Allow HTTPS"
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
    description = "Allow all outbound"
  }

  tags = merge(var.tags, {
    Name = "${var.name}-alb-sg"
  })
}

# Application Load Balancer
resource "aws_lb" "main" {
  name               = "${var.name}-alb"
  internal           = false
  load_balancer_type = "application"
  security_groups    = [aws_security_group.alb.id]
  subnets            = var.public_subnet_ids

  enable_deletion_protection = false # Set to true for production

  tags = merge(var.tags, {
    Name = "${var.name}-alb"
  })
}

# HTTP Listener (redirect to HTTPS or direct to target)
resource "aws_lb_listener" "http" {
  load_balancer_arn = aws_lb.main.arn
  port              = 80
  protocol          = "HTTP"

  default_action {
    type = var.certificate_arn != null ? "redirect" : "fixed-response"

    dynamic "redirect" {
      for_each = var.certificate_arn != null ? [1] : []
      content {
        port        = "443"
        protocol    = "HTTPS"
        status_code = "HTTP_301"
      }
    }

    dynamic "fixed_response" {
      for_each = var.certificate_arn == null ? [1] : []
      content {
        content_type = "text/plain"
        message_body = "Use target groups"
        status_code  = "200"
      }
    }
  }
}

# HTTPS Listener (if certificate provided)
resource "aws_lb_listener" "https" {
  count             = var.certificate_arn != null ? 1 : 0
  load_balancer_arn = aws_lb.main.arn
  port              = 443
  protocol          = "HTTPS"
  ssl_policy        = "ELBSecurityPolicy-TLS13-1-2-2021-06"
  certificate_arn   = var.certificate_arn

  default_action {
    type = "fixed-response"
    fixed_response {
      content_type = "text/plain"
      message_body = "Not Found"
      status_code  = "404"
    }
  }
}

# Target Group for Gateway Service (REST + WebSocket)
resource "aws_lb_target_group" "gateway" {
  name        = "${var.name}-gateway-tg"
  port        = 8080
  protocol    = "HTTP"
  vpc_id      = var.vpc_id
  target_type = "ip"

  health_check {
    enabled             = true
    healthy_threshold   = 2
    interval            = 30
    matcher             = "200"
    path                = "/actuator/health"
    port                = "traffic-port"
    protocol            = "HTTP"
    timeout             = 5
    unhealthy_threshold = 3
  }

  # Stickiness for WebSocket
  stickiness {
    type            = "lb_cookie"
    cookie_duration = 86400
    enabled         = true
  }

  tags = merge(var.tags, {
    Name = "${var.name}-gateway-tg"
  })
}

# Listener Rules for Gateway
resource "aws_lb_listener_rule" "gateway_api" {
  listener_arn = var.certificate_arn != null ? aws_lb_listener.https[0].arn : aws_lb_listener.http.arn
  priority     = 100

  action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.gateway.arn
  }

  condition {
    path_pattern {
      values = ["/api/*", "/ws", "/actuator/*"]
    }
  }
}

# Outputs
output "alb_arn" {
  value = aws_lb.main.arn
}

output "alb_dns_name" {
  value = aws_lb.main.dns_name
}

output "alb_zone_id" {
  value = aws_lb.main.zone_id
}

output "alb_security_group_id" {
  value = aws_security_group.alb.id
}

output "gateway_target_group_arn" {
  value = aws_lb_target_group.gateway.arn
}

output "http_listener_arn" {
  value = aws_lb_listener.http.arn
}

output "https_listener_arn" {
  value = var.certificate_arn != null ? aws_lb_listener.https[0].arn : null
}
