package worker

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/lalithlochan/nimbus/internal/db"
)

// LogSender is a simple sender that logs notifications (for testing/development)
type LogSender struct {
	logger *zap.Logger
}

func NewLogSender(logger *zap.Logger) *LogSender {
	return &LogSender{logger: logger}
}

func (s *LogSender) Send(ctx context.Context, notif *db.Notification) error {
	s.logger.Info("sending notification",
		zap.String("id", notif.ID.String()),
		zap.String("channel", notif.Channel),
		zap.String("user_id", notif.UserID.String()),
	)

	switch notif.Channel {
	case "email":
		return s.sendEmail(ctx, notif)
	case "sms":
		return s.sendSMS(ctx, notif)
	case "webhook":
		return s.sendWebhook(ctx, notif)
	default:
		return fmt.Errorf("unsupported channel: %s", notif.Channel)
	}
}

func (s *LogSender) sendEmail(ctx context.Context, notif *db.Notification) error {
	s.logger.Info("email sent",
		zap.String("id", notif.ID.String()),
		zap.Any("payload", notif.Payload),
	)
	return nil
}

func (s *LogSender) sendSMS(ctx context.Context, notif *db.Notification) error {
	s.logger.Info("sms sent",
		zap.String("id", notif.ID.String()),
		zap.Any("payload", notif.Payload),
	)
	return nil
}

func (s *LogSender) sendWebhook(ctx context.Context, notif *db.Notification) error {
	s.logger.Info("webhook sent",
		zap.String("id", notif.ID.String()),
		zap.Any("payload", notif.Payload),
	)
	return nil
}
