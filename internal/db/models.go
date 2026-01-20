package db

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Notification represents a notification in the database
type Notification struct {
	ID           uuid.UUID       `json:"id"`
	TenantID     uuid.UUID       `json:"tenant_id"`
	UserID       uuid.UUID       `json:"user_id"`
	Payload      json.RawMessage `json:"payload"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
	NextRetryAt  *time.Time      `json:"next_retry_at,omitempty"`
	ErrorMessage *string         `json:"error_message,omitempty"`
	Channel      string          `json:"channel"`
	Status       string          `json:"status"`
	Attempt      int             `json:"attempt"`
}

// Status constants
const (
	StatusPending      = "pending"
	StatusProcessing   = "processing"
	StatusSent         = "sent"
	StatusFailed       = "failed"
	StatusDeadLettered = "dead_lettered"
)

// Channel constants
const (
	ChannelEmail   = "email"
	ChannelSMS     = "sms"
	ChannelWebhook = "webhook"
)

// DLQ Status constants
const (
	DLQStatusPending   = "pending"
	DLQStatusRetried   = "retried"
	DLQStatusDiscarded = "discarded"
)

// DeadLetterNotification represents a failed notification in the DLQ
type DeadLetterNotification struct {
	ID                     uuid.UUID       `json:"id"`
	OriginalNotificationID uuid.UUID       `json:"original_notification_id"`
	TenantID               uuid.UUID       `json:"tenant_id"`
	UserID                 uuid.UUID       `json:"user_id"`
	Payload                json.RawMessage `json:"payload"`
	CreatedAt              time.Time       `json:"created_at"`
	UpdatedAt              time.Time       `json:"updated_at"`
	RetriedNotificationID  *uuid.UUID      `json:"retried_notification_id,omitempty"`
	Channel                string          `json:"channel"`
	LastError              string          `json:"last_error"`
	Status                 string          `json:"status"`
	Attempts               int             `json:"attempts"`
}
