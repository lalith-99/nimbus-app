package sqs

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/lalithlochan/nimbus/internal/db"
)

func TestMessage_Marshal(t *testing.T) {
	payload := json.RawMessage(`{"to":"user@example.com"}`)
	notif := &db.Notification{
		ID:       uuid.New(),
		TenantID: uuid.New(),
		UserID:   uuid.New(),
		Channel:  db.ChannelEmail,
		Payload:  payload,
		Attempt:  1,
	}

	msg := Message{
		NotificationID: notif.ID.String(),
		TenantID:       notif.TenantID.String(),
		UserID:         notif.UserID.String(),
		Channel:        notif.Channel,
		Payload:        notif.Payload,
		Attempt:        notif.Attempt,
		EnqueuedAt:     1234567890,
	}

	body, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded Message
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.NotificationID != msg.NotificationID {
		t.Errorf("notification id mismatch: got %s, want %s", decoded.NotificationID, msg.NotificationID)
	}
	if decoded.Channel != msg.Channel {
		t.Errorf("channel mismatch: got %s, want %s", decoded.Channel, msg.Channel)
	}
	if decoded.Attempt != msg.Attempt {
		t.Errorf("attempt mismatch: got %d, want %d", decoded.Attempt, msg.Attempt)
	}
}

func TestMessage_PreservesPayload(t *testing.T) {
	payload := json.RawMessage(`{"email":"test@example.com","name":"Alice"}`)

	msg := Message{
		NotificationID: uuid.New().String(),
		TenantID:       uuid.New().String(),
		UserID:         uuid.New().String(),
		Channel:        db.ChannelEmail,
		Payload:        payload,
		Attempt:        0,
		EnqueuedAt:     1234567890,
	}

	body, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded Message
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if string(decoded.Payload) != string(payload) {
		t.Errorf("payload mismatch: got %s, want %s", string(decoded.Payload), string(payload))
	}
}

func TestEnqueueBatchEmpty(t *testing.T) {
	ctx := context.Background()

	producer := &Producer{
		client:   nil,
		queueURL: "https://sqs.us-east-1.amazonaws.com/123456789/test",
		logger:   nil,
	}

	result, err := producer.EnqueueBatch(ctx, []*db.Notification{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != 0 {
		t.Errorf("expected empty result, got %d items", len(result))
	}
}
