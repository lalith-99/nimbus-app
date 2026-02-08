package worker

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/lalithlochan/nimbus/internal/db"
)

func TestLogSender_SendEmail(t *testing.T) {
	// 1. Create a LogSender with zap.NewNop()
	logger := zap.NewNop()
	sender := NewLogSender(logger)
	// 2. Create a notification with Channel: "email"
	notification := &db.Notification{
		ID:      uuid.New(),
		Channel: "email",
		Payload: []byte(`{"subject":"Test","body":"This is a test email"}`),
	}
	// 3. Call sender.Send()
	err := sender.Send(context.Background(), notification)
	// 4. Check err == nil
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestLogSender_SendSMS(t *testing.T) {
	sender := NewLogSender(zap.NewNop())
	err := sender.Send(context.Background(), makeTestNotification("sms"))
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestLogSender_SendWebhook(t *testing.T) {
	sender := NewLogSender(zap.NewNop())
	err := sender.Send(context.Background(), makeTestNotification("webhook"))
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestLogSender_UnsupportedChannel(t *testing.T) {
	sender := NewLogSender(zap.NewNop())
	// LogSender accepts all channels in development/test mode
	err := sender.Send(context.Background(), makeTestNotification("unsupported"))
	if err != nil {
		t.Errorf("LogSender should accept all channels, got error: %v", err)
	}
}

func makeTestNotification(channel string) *db.Notification {
	return &db.Notification{
		ID:      uuid.New(),
		Channel: channel,
		Payload: []byte(`{"message":"Test notification"}`),
	}
}
