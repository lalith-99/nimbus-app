package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"

	"github.com/lalithlochan/nimbus/internal/db"
)

// Sender is the unified interface for all notification channels
// Implementations: Email (SES), SMS (SNS), Webhooks
type Sender interface {
	Send(ctx context.Context, notif *db.Notification) error
	SupportsChannel(channel string) bool
}

// EmailPayload represents the structure of an email notification
type EmailPayload struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// SMSPayload represents the structure of an SMS notification
type SMSPayload struct {
	PhoneNumber string `json:"phone_number"`
	Message     string `json:"message"`
}

// WebhookPayload represents the structure of a webhook notification
type WebhookPayload struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`      // POST, PUT, etc. Defaults to POST
	Headers map[string]string `json:"headers"`     // Custom headers
	Body    json.RawMessage   `json:"body"`        // Raw JSON body
	Timeout int               `json:"timeout_sec"` // Timeout in seconds, default 30
}

// MultiSender routes notifications to the appropriate channel sender
// This implements the Strategy pattern for extensibility
type MultiSender struct {
	senders []Sender
	logger  *zap.Logger
}

// NewMultiSender creates a router that uses multiple underlying senders
func NewMultiSender(logger *zap.Logger, senders ...Sender) *MultiSender {
	return &MultiSender{
		senders: senders,
		logger:  logger,
	}
}

// Send routes the notification to the appropriate sender based on channel
func (m *MultiSender) Send(ctx context.Context, notif *db.Notification) error {
	for _, sender := range m.senders {
		if sender.SupportsChannel(notif.Channel) {
			m.logger.Debug("routing notification to sender",
				zap.String("channel", notif.Channel),
				zap.String("notification_id", notif.ID.String()),
			)
			return sender.Send(ctx, notif)
		}
	}

	return fmt.Errorf("no sender found for channel: %s", notif.Channel)
}

// SupportsChannel checks if any underlying sender supports the channel
func (m *MultiSender) SupportsChannel(channel string) bool {
	for _, sender := range m.senders {
		if sender.SupportsChannel(channel) {
			return true
		}
	}
	return false
}

// LogSender is a simple sender that logs notifications (for testing/development)
type LogSender struct {
	logger *zap.Logger
}

func NewLogSender(logger *zap.Logger) *LogSender {
	return &LogSender{logger: logger}
}

func (s *LogSender) Send(ctx context.Context, notif *db.Notification) error {
	s.logger.Info("logging notification (development mode)",
		zap.String("id", notif.ID.String()),
		zap.String("channel", notif.Channel),
		zap.String("user_id", notif.UserID.String()),
		zap.Any("payload", json.RawMessage(notif.Payload)),
	)
	return nil
}

func (s *LogSender) SupportsChannel(channel string) bool {
	// LogSender supports all channels for development/testing
	return channel == "email" || channel == "sms" || channel == "webhook"
}
