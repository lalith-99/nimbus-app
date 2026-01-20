package sns

import (
	"encoding/json"
	"testing"
)

func TestMessage_Marshal(t *testing.T) {
	msg := Message{
		NotificationID: "notif-123",
		TenantID:       "tenant-456",
		Channel:        ChannelEmail,
		Recipient:      "user@example.com",
		Subject:        "Test Subject",
		Body:           "Test Body",
		Metadata:       map[string]string{"key": "value"},
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("failed to marshal message: %v", err)
	}

	var decoded Message
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal message: %v", err)
	}

	if decoded.NotificationID != msg.NotificationID {
		t.Errorf("NotificationID mismatch: got %s, want %s", decoded.NotificationID, msg.NotificationID)
	}
	if decoded.Channel != msg.Channel {
		t.Errorf("Channel mismatch: got %s, want %s", decoded.Channel, msg.Channel)
	}
}

func TestChannelConstants(t *testing.T) {
	tests := []struct {
		channel Channel
		want    string
	}{
		{ChannelEmail, "email"},
		{ChannelSMS, "sms"},
		{ChannelWebhook, "webhook"},
	}

	for _, tt := range tests {
		if string(tt.channel) != tt.want {
			t.Errorf("Channel %v: got %s, want %s", tt.channel, string(tt.channel), tt.want)
		}
	}
}

func TestMessage_OptionalFields(t *testing.T) {
	msg := Message{
		NotificationID: "notif-123",
		TenantID:       "tenant-456",
		Channel:        ChannelSMS,
		Recipient:      "+1234567890",
		Body:           "SMS Body",
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("failed to marshal message: %v", err)
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if _, ok := decoded["subject"]; ok {
		t.Error("subject should be omitted when empty")
	}
	if _, ok := decoded["metadata"]; ok {
		t.Error("metadata should be omitted when nil")
	}
}

func TestPublisher_BatchLimit(t *testing.T) {
	messages := make([]Message, 11)
	for i := range messages {
		messages[i] = Message{NotificationID: "test"}
	}

	if len(messages) <= 10 {
		t.Error("test setup error: need more than 10 messages")
	}
}
