package ai

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"

	"github.com/lalithlochan/nimbus/internal/db"
	"github.com/lalithlochan/nimbus/internal/worker"
)

// EnrichmentSender wraps an existing Sender and uses AI to generate
// email content when a notification payload contains a "template" field
// instead of a pre-written "body".
//
// If the payload has a "body" already, it passes through unchanged.
// This implements the Decorator pattern on the worker.Sender interface.
//
// Example payload that triggers AI:
//
//	{
//	    "to": "alice@example.com",
//	    "subject": "Welcome!",
//	    "template": "welcome_email",
//	    "context": {"name": "Alice", "plan": "Pro"}
//	}
type EnrichmentSender struct {
	inner  worker.Sender
	client *Client
	logger *zap.Logger
}

// NewEnrichmentSender wraps a sender with AI content generation.
func NewEnrichmentSender(inner worker.Sender, client *Client, logger *zap.Logger) *EnrichmentSender {
	return &EnrichmentSender{
		inner:  inner,
		client: client,
		logger: logger,
	}
}

// templatePayload is the payload format that triggers AI generation.
type templatePayload struct {
	To       string            `json:"to"`
	Subject  string            `json:"subject"`
	Template string            `json:"template"`
	Context  map[string]string `json:"context"`
}

// Send checks if the notification needs AI content generation.
// For email notifications with a "template" field, it generates the body
// using GPT, then replaces the payload before passing to the real sender.
func (e *EnrichmentSender) Send(ctx context.Context, notif *db.Notification) error {
	// Only enrich email notifications
	if notif.Channel != db.ChannelEmail {
		return e.inner.Send(ctx, notif)
	}

	// Check if payload has a template field (needs AI generation)
	var tp templatePayload
	if err := json.Unmarshal(notif.Payload, &tp); err != nil || tp.Template == "" {
		// No template — pass through unchanged
		return e.inner.Send(ctx, notif)
	}

	e.logger.Info("AI enriching notification content",
		zap.String("id", notif.ID.String()),
		zap.String("template", tp.Template),
	)

	// Build prompt from template + context
	contextStr := ""
	for k, v := range tp.Context {
		contextStr += fmt.Sprintf("- %s: %s\n", k, v)
	}

	systemPrompt := `You are a professional email content writer for a notification platform.
Generate clear, concise, professional email body text. Return ONLY the email body, no subject line.
Keep it under 200 words. Use a friendly but professional tone.`

	userPrompt := fmt.Sprintf("Template: %s\nSubject: %s\nContext:\n%s\nGenerate the email body.",
		tp.Template, tp.Subject, contextStr)

	body, err := e.client.GenerateText(ctx, systemPrompt, userPrompt)
	if err != nil {
		e.logger.Error("AI content generation failed, sending without enrichment",
			zap.String("id", notif.ID.String()),
			zap.Error(err),
		)
		// Fallback: set body to a simple message so it still sends
		body = fmt.Sprintf("This is an automated %s notification.", tp.Template)
	}

	// Replace the payload with the generated body
	enrichedPayload, _ := json.Marshal(map[string]string{
		"to":      tp.To,
		"subject": tp.Subject,
		"body":    body,
	})
	notif.Payload = enrichedPayload

	e.logger.Info("AI content generated",
		zap.String("id", notif.ID.String()),
		zap.Int("body_length", len(body)),
	)

	return e.inner.Send(ctx, notif)
}

// SupportsChannel delegates to the inner sender.
func (e *EnrichmentSender) SupportsChannel(channel string) bool {
	return e.inner.SupportsChannel(channel)
}
