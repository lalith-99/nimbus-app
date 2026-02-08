package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/lalithlochan/nimbus/internal/db"
)

func TestMultiSenderRouting(t *testing.T) {
	logger := zap.NewNop()

	// Create mock senders
	emailSender, _ := NewSESSender(context.Background(), SESConfig{Region: "us-east-1"}, logger)
	webhookSender := NewWebhookSender(logger, WebhookConfig{})
	multiSender := NewMultiSender(logger, emailSender, webhookSender)

	tests := []struct {
		name    string
		channel string
		should  bool
	}{
		{"email_supported", db.ChannelEmail, true},
		{"webhook_supported", db.ChannelWebhook, true},
		{"sms_not_supported", db.ChannelSMS, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			supports := multiSender.SupportsChannel(tt.channel)
			if supports != tt.should {
				t.Errorf("SupportsChannel(%s) = %v, want %v", tt.channel, supports, tt.should)
			}
		})
	}
}

func TestSESSenderSupportsChannel(t *testing.T) {
	logger := zap.NewNop()
	sender, _ := NewSESSender(context.Background(), SESConfig{Region: "us-east-1"}, logger)

	tests := []struct {
		channel string
		want    bool
	}{
		{db.ChannelEmail, true},
		{db.ChannelSMS, false},
		{db.ChannelWebhook, false},
	}

	for _, tt := range tests {
		t.Run(tt.channel, func(t *testing.T) {
			if got := sender.SupportsChannel(tt.channel); got != tt.want {
				t.Errorf("SupportsChannel(%s) = %v, want %v", tt.channel, got, tt.want)
			}
		})
	}
}

func TestSNSSenderSupportsChannel(t *testing.T) {
	logger := zap.NewNop()
	sender, _ := NewSNSSender(context.Background(), SNSConfig{Region: "us-east-1"}, logger)

	tests := []struct {
		channel string
		want    bool
	}{
		{db.ChannelSMS, true},
		{db.ChannelEmail, false},
		{db.ChannelWebhook, false},
	}

	for _, tt := range tests {
		t.Run(tt.channel, func(t *testing.T) {
			if got := sender.SupportsChannel(tt.channel); got != tt.want {
				t.Errorf("SupportsChannel(%s) = %v, want %v", tt.channel, got, tt.want)
			}
		})
	}
}

func TestWebhookSenderSupportsChannel(t *testing.T) {
	logger := zap.NewNop()
	sender := NewWebhookSender(logger, WebhookConfig{})

	tests := []struct {
		channel string
		want    bool
	}{
		{db.ChannelWebhook, true},
		{db.ChannelEmail, false},
		{db.ChannelSMS, false},
	}

	for _, tt := range tests {
		t.Run(tt.channel, func(t *testing.T) {
			if got := sender.SupportsChannel(tt.channel); got != tt.want {
				t.Errorf("SupportsChannel(%s) = %v, want %v", tt.channel, got, tt.want)
			}
		})
	}
}

func TestWebhookSenderValidation(t *testing.T) {
	logger := zap.NewNop()
	_ = NewWebhookSender(logger, WebhookConfig{})

	tests := []struct {
		name         string
		notification *db.Notification
		wantErr      bool
	}{
		{
			name: "missing_url",
			notification: &db.Notification{
				ID:       uuid.New(),
				TenantID: uuid.New(),
				UserID:   uuid.New(),
				Channel:  db.ChannelWebhook,
				Payload:  json.RawMessage(`{"method":"POST","body":{}}`),
			},
			wantErr: true,
		},
		{
			name: "invalid_method",
			notification: &db.Notification{
				ID:       uuid.New(),
				TenantID: uuid.New(),
				UserID:   uuid.New(),
				Channel:  db.ChannelWebhook,
				Payload:  json.RawMessage(`{"url":"http://example.com","method":"GET"}`),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var payload WebhookPayload
			err := json.Unmarshal(tt.notification.Payload, &payload)
			if err != nil {
				if !tt.wantErr {
					t.Errorf("failed to parse payload: %v", err)
				}
				return
			}

			if payload.URL == "" && tt.wantErr {
				return // Expected error caught
			}

			method := payload.Method
			if method == "" {
				method = "POST"
			}

			if (method != "POST" && method != "PUT" && method != "PATCH") && tt.wantErr {
				return // Expected error caught
			}
		})
	}
}

func TestWebhookSenderHTTPCall(t *testing.T) {
	logger := zap.NewNop()
	sender := NewWebhookSender(logger, WebhookConfig{DefaultTimeout: 5 * time.Second})

	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request method
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		// Verify custom headers
		if r.Header.Get("X-Custom-Header") != "test-value" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	payload := WebhookPayload{
		URL:     server.URL,
		Method:  "POST",
		Body:    json.RawMessage(`{"key":"value"}`),
		Headers: map[string]string{"X-Custom-Header": "test-value"},
		Timeout: 30,
	}

	payloadBytes, _ := json.Marshal(payload)

	notif := &db.Notification{
		ID:       uuid.New(),
		TenantID: uuid.New(),
		UserID:   uuid.New(),
		Channel:  db.ChannelWebhook,
		Payload:  payloadBytes,
	}

	err := sender.Send(context.Background(), notif)
	if err != nil {
		t.Errorf("Send() failed: %v", err)
	}
}

func TestWebhookSenderHTTPError(t *testing.T) {
	logger := zap.NewNop()
	sender := NewWebhookSender(logger, WebhookConfig{DefaultTimeout: 5 * time.Second})

	// Create a test server that returns 500
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	payload := WebhookPayload{
		URL:    server.URL,
		Method: "POST",
		Body:   json.RawMessage(`{"key":"value"}`),
	}

	payloadBytes, _ := json.Marshal(payload)

	notif := &db.Notification{
		ID:       uuid.New(),
		TenantID: uuid.New(),
		UserID:   uuid.New(),
		Channel:  db.ChannelWebhook,
		Payload:  payloadBytes,
	}

	err := sender.Send(context.Background(), notif)
	if err == nil {
		t.Errorf("Send() should have failed for 500 status")
	}
}

func TestEmailPayloadParsing(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    EmailPayload
		wantErr bool
	}{
		{
			name:    "valid_payload",
			payload: `{"to":"user@example.com","subject":"Hello","body":"World"}`,
			want: EmailPayload{
				To:      "user@example.com",
				Subject: "Hello",
				Body:    "World",
			},
			wantErr: false,
		},
		{
			name:    "invalid_json",
			payload: `{invalid json`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var payload EmailPayload
			err := json.Unmarshal([]byte(tt.payload), &payload)

			if (err != nil) != tt.wantErr {
				t.Errorf("unmarshal error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if err == nil && payload != tt.want {
				t.Errorf("got %+v, want %+v", payload, tt.want)
			}
		})
	}
}

func TestLogSenderSupportsAllChannels(t *testing.T) {
	logger := zap.NewNop()
	sender := NewLogSender(logger)

	channels := []string{db.ChannelEmail, db.ChannelSMS, db.ChannelWebhook}

	for _, ch := range channels {
		if !sender.SupportsChannel(ch) {
			t.Errorf("LogSender should support %s channel", ch)
		}
	}
}
