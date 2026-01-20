package db

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Notification represents a notification in the database
type Notification struct {
	Payload      json.RawMessage `json:"payload"` // 24 bytes (slice)
	ID           uuid.UUID       `json:"id"`      // 16 bytes
	TenantID     uuid.UUID       `json:"tenant_id"`
	UserID       uuid.UUID       `json:"user_id"`
	CreatedAt    time.Time       `json:"created_at"` // 24 bytes
	UpdatedAt    time.Time       `json:"updated_at"`
	NextRetryAt  *time.Time      `json:"next_retry_at,omitempty"` // 8 bytes
	ErrorMessage *string         `json:"error_message,omitempty"`
	Channel      string          `json:"channel"`   // 16 bytes
	Status       string          `json:"status"`
	Attempt      int             `json:"attempt"`   // 8 bytes
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
	Payload                json.RawMessage `json:"payload"` // 24 bytes
	ID                     uuid.UUID       `json:"id"`      // 16 bytes
	OriginalNotificationID uuid.UUID       `json:"original_notification_id"`
	TenantID               uuid.UUID       `json:"tenant_id"`
	UserID                 uuid.UUID       `json:"user_id"`
	CreatedAt              time.Time       `json:"created_at"` // 24 bytes
	UpdatedAt              time.Time       `json:"updated_at"`
	RetriedNotificationID  *uuid.UUID      `json:"retried_notification_id,omitempty"` // 8 bytes
	Channel                string          `json:"channel"`                           // 16 bytes
	LastError              string          `json:"last_error"`
	Status                 string          `json:"status"`
	Attempts               int             `json:"attempts"` // 8 bytes
}
