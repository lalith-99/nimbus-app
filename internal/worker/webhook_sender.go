package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/lalithlochan/nimbus/internal/db"
)

// WebhookSender sends notifications via HTTP webhooks
type WebhookSender struct {
	client *http.Client
	logger *zap.Logger
}

type WebhookConfig struct {
	DefaultTimeout time.Duration // Default timeout for webhook requests
	MaxRetries     int           // Max retries for webhook requests (separate from notification retries)
}

// NewWebhookSender creates a new webhook sender
func NewWebhookSender(logger *zap.Logger, cfg WebhookConfig) *WebhookSender {
	timeout := cfg.DefaultTimeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	return &WebhookSender{
		client: &http.Client{
			Timeout: timeout,
			// Consider adding transport settings for keep-alive, max connections, etc.
		},
		logger: logger,
	}
}

// Send sends a notification via HTTP webhook
func (s *WebhookSender) Send(ctx context.Context, notif *db.Notification) error {
	if notif.Channel != db.ChannelWebhook {
		return fmt.Errorf("webhook sender only supports webhooks, got: %s", notif.Channel)
	}

	// Parse payload
	var payload WebhookPayload
	if err := json.Unmarshal(notif.Payload, &payload); err != nil {
		return fmt.Errorf("invalid webhook payload: %w", err)
	}

	// Validate required fields
	if payload.URL == "" {
		return fmt.Errorf("webhook payload missing url")
	}

	// Set defaults
	method := payload.Method
	if method == "" {
		method = "POST"
	}

	if method != "POST" && method != "PUT" && method != "PATCH" {
		return fmt.Errorf("webhook method not supported: %s (only POST, PUT, PATCH)", method)
	}

	timeout := 30 * time.Second
	if payload.Timeout > 0 {
		timeout = time.Duration(payload.Timeout) * time.Second
	}

	// Create request with timeout context
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, method, payload.URL, bytes.NewReader(payload.Body))
	if err != nil {
		return fmt.Errorf("failed to create webhook request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Nimbus/1.0.0")
	req.Header.Set("X-Nimbus-Notification-ID", notif.ID.String())
	req.Header.Set("X-Nimbus-Tenant-ID", notif.TenantID.String())

	// Add custom headers from payload
	for key, value := range payload.Headers {
		req.Header.Set(key, value)
	}

	// Send webhook
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("webhook request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response body for logging/debugging
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))

	// Accept 2xx status codes as success
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned non-2xx status: %d, body: %s", resp.StatusCode, string(bodyBytes))
	}

	s.logger.Info("webhook delivered successfully",
		zap.String("id", notif.ID.String()),
		zap.String("url", payload.URL),
		zap.Int("status_code", resp.StatusCode),
		zap.String("response_preview", string(bodyBytes)),
	)

	return nil
}

// SupportsChannel checks if this sender supports webhooks
func (s *WebhookSender) SupportsChannel(channel string) bool {
	return channel == db.ChannelWebhook
}
