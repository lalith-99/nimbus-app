# Multi-Channel Notification System

## Overview

Nimbus now supports sending notifications across **three distinct channels**:
1. **Email** (AWS SES)
2. **SMS** (AWS SNS)
3. **Webhooks** (HTTP POST/PUT/PATCH)

This is implemented using the **Strategy Pattern** with a unified `Sender` interface, allowing easy addition of new channels without modifying core business logic.

## Architecture

### Sender Interface

```go
type Sender interface {
    Send(ctx context.Context, notif *db.Notification) error
    SupportsChannel(channel string) bool
}
```

All channel implementations (SES, SNS, Webhook) implement this interface.

### Multi-Channel Router

The `MultiSender` routes notifications to the appropriate handler based on the channel:

```go
multiSender := NewMultiSender(logger, sesSender, snsSender, webhookSender)

// Automatically routes to correct sender based on notification.Channel
err := multiSender.Send(ctx, notification)
```

## Channel Implementations

### 1. Email (SES Sender)

**Supported by:** AWS Simple Email Service (SES)

**Payload Structure:**
```json
{
  "tenant_id": "uuid",
  "user_id": "uuid",
  "channel": "email",
  "payload": {
    "to": "recipient@example.com",
    "subject": "Hello World",
    "body": "This is the email body"
  }
}
```

**Features:**
- Validates recipient email address
- Requires subject and body
- Integrates with AWS SES for reliable delivery
- Automatic retries on failure

**Example Request:**
```bash
curl -X POST http://localhost:8080/v1/notifications \
  -H "Content-Type: application/json" \
  -d '{
    "tenant_id": "00000000-0000-0000-0000-000000000001",
    "user_id": "00000000-0000-0000-0000-000000000002",
    "channel": "email",
    "payload": {
      "to": "user@example.com",
      "subject": "Welcome!",
      "body": "Thanks for signing up."
    }
  }'
```

---

### 2. SMS (SNS Sender)

**Supported by:** AWS Simple Notification Service (SNS)

**Payload Structure:**
```json
{
  "tenant_id": "uuid",
  "user_id": "uuid",
  "channel": "sms",
  "payload": {
    "phone_number": "+1234567890",
    "message": "Your verification code is 123456"
  }
}
```

**Features:**
- Validates phone number format
- Message must be under 160 characters (SMS standard)
- Integrates with AWS SNS for global SMS delivery
- Supports international numbers with country codes

**Example Request:**
```bash
curl -X POST http://localhost:8080/v1/notifications \
  -H "Content-Type: application/json" \
  -d '{
    "tenant_id": "00000000-0000-0000-0000-000000000001",
    "user_id": "00000000-0000-0000-0000-000000000002",
    "channel": "sms",
    "payload": {
      "phone_number": "+1234567890",
      "message": "Your code is: 123456"
    }
  }'
```

---

### 3. Webhooks (HTTP Sender)

**Supported by:** Custom HTTP endpoints

**Payload Structure:**
```json
{
  "tenant_id": "uuid",
  "user_id": "uuid",
  "channel": "webhook",
  "payload": {
    "url": "https://customer.example.com/webhooks/notify",
    "method": "POST",
    "headers": {
      "Authorization": "Bearer secret123",
      "X-Custom-Header": "custom-value"
    },
    "body": {
      "event": "order_shipped",
      "order_id": "12345",
      "tracking_url": "https://tracking.example.com/12345"
    },
    "timeout_sec": 30
  }
}
```

**Features:**
- Supports POST, PUT, PATCH methods
- Custom headers for authentication
- Arbitrary JSON payload
- Configurable timeout (default 30 seconds)
- Automatic retries with exponential backoff
- Validation: rejects GET/HEAD requests (safety)
- Adds Nimbus headers for tracking:
  - `X-Nimbus-Notification-ID`: Notification UUID
  - `X-Nimbus-Tenant-ID`: Tenant UUID

**Example Request:**
```bash
curl -X POST http://localhost:8080/v1/notifications \
  -H "Content-Type: application/json" \
  -d '{
    "tenant_id": "00000000-0000-0000-0000-000000000001",
    "user_id": "00000000-0000-0000-0000-000000000002",
    "channel": "webhook",
    "payload": {
      "url": "https://api.customer.com/events",
      "method": "POST",
      "headers": {
        "Authorization": "Bearer abc123",
        "Content-Type": "application/json"
      },
      "body": {
        "event_type": "user_created",
        "user_id": "user-123",
        "timestamp": "2026-02-07T10:30:00Z"
      },
      "timeout_sec": 30
    }
  }'
```

---

## Configuration

### Environment Variables

```bash
# AWS Credentials (auto-discovered from AWS SDK)
AWS_REGION=us-east-1

# Email (SES)
SES_FROM_EMAIL=noreply@yourcompany.com

# SMS (SNS)
SNS_REGION=us-east-1

# Webhook
WEBHOOK_TIMEOUT=30  # Default timeout in seconds
```

### Initialization in main.go

```go
// Create individual senders
sesSender, _ := worker.NewSESSender(ctx, worker.SESConfig{
    Region:    cfg.AWSRegion,
    FromEmail: cfg.SESFromEmail,
}, logger)

snsSender, _ := worker.NewSNSSender(ctx, worker.SNSConfig{
    Region: cfg.SNSRegion,
}, logger)

webhookSender := worker.NewWebhookSender(logger, worker.WebhookConfig{
    DefaultTimeout: time.Duration(cfg.WebhookTimeout) * time.Second,
})

// Create multi-sender router
multiSender := worker.NewMultiSender(logger, sesSender, snsSender, webhookSender)

// Use in worker
w := worker.New(repo, multiSender, config, logger)
```

---

## Graceful Degradation

If a service is unavailable:

- **SNS down** → SES and Webhook still work
- **SES down** → SNS and Webhook still work  
- **All external services down** → Notifications stay in queue with retries

Example:
```go
// SNS is optional
if snsSender != nil {
    multiSender = worker.NewMultiSender(logger, sesSender, snsSender, webhookSender)
} else {
    // Fallback: email and webhook only
    multiSender = worker.NewMultiSender(logger, sesSender, webhookSender)
}
```

---

## Error Handling & Retries

### Automatic Retry Logic

For each channel, failures are handled consistently:

1. **First attempt fails** → Mark as `pending`, schedule for retry
2. **Exponential backoff** → Wait 1s, 2s, 4s, 8s, 16s between retries
3. **Max retries exceeded** → Move to Dead Letter Queue
4. **Manual retry** → Use `/v1/dlq/{id}/retry` endpoint

### Channel-Specific Error Handling

**Email (SES):**
- Invalid recipient email → Permanent failure → DLQ
- SES rate limit → Temporary → Retry
- SES quota exceeded → Temporary → Retry

**SMS (SNS):**
- Invalid phone number → Permanent failure → DLQ
- Unsupported country code → Permanent failure → DLQ
- SNS rate limit → Temporary → Retry

**Webhook:**
- Network timeout → Temporary → Retry
- 5xx response → Temporary → Retry
- 4xx response → Permanent failure → DLQ
- Webhook unreachable → Temporary → Retry

---

## Testing

### Unit Tests

All senders have comprehensive test coverage:

```bash
go test ./internal/worker -v

# Sample output:
# === RUN TestMultiSenderRouting
# === RUN TestSESSenderSupportsChannel
# === RUN TestSNSSenderSupportsChannel
# === RUN TestWebhookSenderHTTPCall
# === RUN TestWebhookSenderHTTPError
```

### Integration Testing

Test with actual AWS services:

```bash
# Test email
curl -X POST http://localhost:8080/v1/notifications \
  -d '{...email payload...}'

# Test SMS
curl -X POST http://localhost:8080/v1/notifications \
  -d '{...sms payload...}'

# Test webhook (using webhook.site for testing)
curl -X POST http://localhost:8080/v1/notifications \
  -d '{
    "channel": "webhook",
    "payload": {
      "url": "https://webhook.site/unique-id",
      "method": "POST",
      "body": {"test": "data"}
    }
  }'
```

---

## Design Patterns Used

### Strategy Pattern
Each channel is a strategy for delivering notifications. New channels can be added by implementing the `Sender` interface:

```go
type Sender interface {
    Send(ctx context.Context, notif *db.Notification) error
    SupportsChannel(channel string) bool
}
```

### Factory Pattern
The `MultiSender` acts as a router/factory, selecting the correct sender based on notification channel.

### Decorator Pattern
Could be used to add cross-cutting concerns:
- Logging decorator
- Metrics decorator
- Circuit breaker decorator

---

## Adding a New Channel

To add support for a new channel (e.g., Slack, Push Notifications):

1. **Create new sender:**
```go
type SlackSender struct {
    webhookURL string
    logger     *zap.Logger
}

func (s *SlackSender) Send(ctx context.Context, notif *db.Notification) error {
    // Implement Slack sending logic
}

func (s *SlackSender) SupportsChannel(channel string) bool {
    return channel == "slack"
}
```

2. **Update config** (if needed new env vars)

3. **Initialize in main.go:**
```go
slackSender := worker.NewSlackSender(slackWebhookURL, logger)
multiSender := worker.NewMultiSender(logger, sesSender, snsSender, webhookSender, slackSender)
```

4. **Add tests:**
```go
func TestSlackSenderSupportsChannel(t *testing.T) {
    // Test implementation
}
```

5. **Update API constants:**
```go
// internal/db/models.go
const ChannelSlack = "slack"
```

That's it! The rest of the system works automatically.

---

## Performance Considerations

### Channel Comparison

| Aspect | Email (SES) | SMS (SNS) | Webhook |
|--------|-------------|----------|---------|
| Speed | ~1-2s | ~1-3s | <30s (configurable) |
| Cost | $0.10 per K | $0.01-0.02 per | Free |
| Scale | Millions/day | Millions/day | Depends on endpoint |
| Latency P99 | ~500ms | ~800ms | Variable |
| Retry Overhead | Low | Low | Medium (timeout) |

### Optimization Tips

1. **Batch webhook calls** → Reduce timeout risk
2. **Use shorter SMS messages** → Faster validation
3. **Monitor Dead Letter Queue** → Fix permanent failures early
4. **Use idempotency keys** → Prevent duplicate sends
5. **Set appropriate timeouts** → Balance reliability vs latency

---

## Monitoring

### Metrics to Track

```
# Prometheus metrics available at /metrics
nimbus_notifications_total{channel="email",status="sent"}
nimbus_notifications_total{channel="sms",status="sent"}
nimbus_notifications_total{channel="webhook",status="sent"}

nimbus_notifications_failed{channel="email"}
nimbus_notifications_failed{channel="sms"}
nimbus_notifications_failed{channel="webhook"}

nimbus_notification_delivery_seconds{channel="email"}
nimbus_notification_delivery_seconds{channel="sms"}
nimbus_notification_delivery_seconds{channel="webhook"}
```

### Logging

Each sender logs:
- Successful sends with message IDs
- Failures with error details
- Retry attempts with delay

Example log:
```json
{
  "level": "info",
  "timestamp": "2026-02-07T10:30:15.123Z",
  "message": "email sent via SES",
  "notification_id": "550e8400-e29b-41d4-a716-446655440000",
  "to": "user@example.com",
  "message_id": "00000146a8f2-12345..."
}
```

---

## Troubleshooting

### Email not being sent
- Check `SES_FROM_EMAIL` is verified in AWS SES
- Verify recipient email is not in SES sandbox
- Check DLQ for permanent failures

### SMS not being sent
- Validate phone number format (+1234567890)
- Check SNS quotas in AWS
- Verify region has SMS support

### Webhooks timing out
- Increase `WEBHOOK_TIMEOUT` env var
- Ensure customer endpoint is accessible
- Check network connectivity/firewalls

### All channels failing
- Verify AWS credentials are configured
- Check AWS service quotas
- Review CloudWatch logs in AWS console
