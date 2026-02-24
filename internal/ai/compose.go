package ai

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/lalithlochan/nimbus/internal/db"
)

// ComposeService uses LLM function calling to turn natural language
// into Nimbus notifications. It calls the repo directly — no HTTP round-trip.
type ComposeService struct {
	client *Client
	repo   ComposeRepository
	logger *zap.Logger
}

// ComposeRepository is the subset of db operations compose needs.
type ComposeRepository interface {
	CreateNotification(ctx context.Context, notif *db.Notification) error
	GetNotification(ctx context.Context, id uuid.UUID) (*db.Notification, error)
	ListNotificationsByTenant(ctx context.Context, tenantID uuid.UUID, limit, offset int) ([]*db.Notification, error)
}

// ComposeRequest is the incoming request to the AI compose endpoint.
type ComposeRequest struct {
	Prompt   string `json:"prompt"`    // Natural language instruction
	TenantID string `json:"tenant_id"` // Required: which tenant
	UserID   string `json:"user_id"`   // Required: who triggered it
}

// ComposeResponse is returned after AI processes the request.
type ComposeResponse struct {
	Message         string   `json:"message"`                    // LLM's final response
	NotificationIDs []string `json:"notification_ids,omitempty"` // IDs of created notifications
}

// NewComposeService creates a new AI compose service.
func NewComposeService(client *Client, repo ComposeRepository, logger *zap.Logger) *ComposeService {
	return &ComposeService{
		client: client,
		repo:   repo,
		logger: logger,
	}
}

// nimbusTools defines what the LLM can call.
var nimbusTools = []Tool{
	{
		Type: "function",
		Function: ToolDefinition{
			Name:        "create_notification",
			Description: "Create and send a notification via email, SMS, or webhook through Nimbus.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"channel": {
						"type": "string",
						"enum": ["email", "sms", "webhook"],
						"description": "Notification channel"
					},
					"to": {
						"type": "string",
						"description": "Recipient: email address, phone number, or webhook URL"
					},
					"subject": {
						"type": "string",
						"description": "Email subject line (email channel only)"
					},
					"body": {
						"type": "string",
						"description": "Message body or content"
					}
				},
				"required": ["channel", "to", "body"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolDefinition{
			Name:        "list_notifications",
			Description: "List recent notifications for the current tenant. Use to check status or history.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"limit": {
						"type": "integer",
						"description": "Max results to return (default 10, max 50)"
					}
				}
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolDefinition{
			Name:        "get_notification_status",
			Description: "Get the delivery status of a specific notification by ID.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"notification_id": {
						"type": "string",
						"description": "UUID of the notification to check"
					}
				},
				"required": ["notification_id"]
			}`),
		},
	},
}

const systemPrompt = `You are an AI assistant integrated into Nimbus, a multi-channel notification platform.
You help users send notifications (email, SMS, webhook) and check their status using natural language.

When creating notifications:
- For email: always include a subject and body
- For SMS: include just the message body and phone number
- For webhook: include the URL and JSON body

Always confirm what you did after executing tools. Be concise.`

// Compose processes a natural language request through multi-round function calling.
// The LLM decides which Nimbus operations to perform and executes them directly.
func (s *ComposeService) Compose(ctx context.Context, req ComposeRequest) (*ComposeResponse, error) {
	tenantID, err := uuid.Parse(req.TenantID)
	if err != nil {
		return nil, fmt.Errorf("invalid tenant_id: %w", err)
	}
	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		return nil, fmt.Errorf("invalid user_id: %w", err)
	}

	messages := []ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: req.Prompt},
	}

	var createdIDs []string
	maxRounds := 5

	for round := 0; round < maxRounds; round++ {
		msg, err := s.client.ChatCompletion(ctx, messages, nimbusTools, nil)
		if err != nil {
			return nil, fmt.Errorf("LLM call failed (round %d): %w", round, err)
		}

		// Append assistant message to history
		messages = append(messages, *msg)

		// If no tool calls, the LLM is done — return its final message
		if len(msg.ToolCalls) == 0 {
			return &ComposeResponse{
				Message:         msg.Content,
				NotificationIDs: createdIDs,
			}, nil
		}

		// Execute each tool call
		for _, tc := range msg.ToolCalls {
			s.logger.Info("AI executing tool",
				zap.String("tool", tc.Function.Name),
				zap.String("args", tc.Function.Arguments),
				zap.Int("round", round+1),
			)

			result, ids, err := s.executeTool(ctx, tc.Function.Name, tc.Function.Arguments, tenantID, userID)
			if err != nil {
				result = fmt.Sprintf("Error: %s", err.Error())
			}
			createdIDs = append(createdIDs, ids...)

			// Add tool result to conversation
			messages = append(messages, ChatMessage{
				Role:       "tool",
				ToolCallID: tc.ID,
				Name:       tc.Function.Name,
				Content:    result,
			})
		}
	}

	return &ComposeResponse{
		Message:         "Completed (max rounds reached)",
		NotificationIDs: createdIDs,
	}, nil
}

// executeTool dispatches a tool call to the appropriate Nimbus operation.
func (s *ComposeService) executeTool(
	ctx context.Context,
	name, argsJSON string,
	tenantID, userID uuid.UUID,
) (result string, createdIDs []string, err error) {

	switch name {
	case "create_notification":
		return s.toolCreateNotification(ctx, argsJSON, tenantID, userID)
	case "list_notifications":
		return s.toolListNotifications(ctx, argsJSON, tenantID)
	case "get_notification_status":
		return s.toolGetNotificationStatus(ctx, argsJSON)
	default:
		return "", nil, fmt.Errorf("unknown tool: %s", name)
	}
}

func (s *ComposeService) toolCreateNotification(
	ctx context.Context,
	argsJSON string,
	tenantID, userID uuid.UUID,
) (string, []string, error) {

	var args struct {
		Channel string `json:"channel"`
		To      string `json:"to"`
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", nil, fmt.Errorf("invalid arguments: %w", err)
	}

	// Build channel-specific payload
	var payload json.RawMessage
	switch args.Channel {
	case db.ChannelEmail:
		p, _ := json.Marshal(map[string]string{
			"to":      args.To,
			"subject": args.Subject,
			"body":    args.Body,
		})
		payload = p
	case db.ChannelSMS:
		p, _ := json.Marshal(map[string]string{
			"phone_number": args.To,
			"message":      args.Body,
		})
		payload = p
	case db.ChannelWebhook:
		p, _ := json.Marshal(map[string]string{
			"url":  args.To,
			"body": args.Body,
		})
		payload = p
	default:
		return "", nil, fmt.Errorf("invalid channel: %s", args.Channel)
	}

	notif := &db.Notification{
		ID:       uuid.New(),
		TenantID: tenantID,
		UserID:   userID,
		Channel:  args.Channel,
		Payload:  payload,
		Status:   db.StatusPending,
		Attempt:  0,
	}

	if err := s.repo.CreateNotification(ctx, notif); err != nil {
		return "", nil, fmt.Errorf("failed to create notification: %w", err)
	}

	s.logger.Info("AI created notification",
		zap.String("id", notif.ID.String()),
		zap.String("channel", args.Channel),
		zap.String("to", args.To),
	)

	result, _ := json.Marshal(map[string]string{
		"status":          "created",
		"notification_id": notif.ID.String(),
		"channel":         args.Channel,
		"to":              args.To,
	})
	return string(result), []string{notif.ID.String()}, nil
}

func (s *ComposeService) toolListNotifications(
	ctx context.Context,
	argsJSON string,
	tenantID uuid.UUID,
) (string, []string, error) {

	var args struct {
		Limit int `json:"limit"`
	}
	_ = json.Unmarshal([]byte(argsJSON), &args)
	if args.Limit <= 0 || args.Limit > 50 {
		args.Limit = 10
	}

	notifications, err := s.repo.ListNotificationsByTenant(ctx, tenantID, args.Limit, 0)
	if err != nil {
		return "", nil, fmt.Errorf("failed to list notifications: %w", err)
	}

	// Return a summary, not the full payload
	type notifSummary struct {
		ID      string `json:"id"`
		Channel string `json:"channel"`
		Status  string `json:"status"`
		Created string `json:"created_at"`
	}
	summaries := make([]notifSummary, len(notifications))
	for i, n := range notifications {
		summaries[i] = notifSummary{
			ID:      n.ID.String(),
			Channel: n.Channel,
			Status:  n.Status,
			Created: n.CreatedAt.Format("2006-01-02 15:04:05"),
		}
	}

	result, _ := json.Marshal(map[string]interface{}{
		"count":         len(summaries),
		"notifications": summaries,
	})
	return string(result), nil, nil
}

func (s *ComposeService) toolGetNotificationStatus(
	ctx context.Context,
	argsJSON string,
) (string, []string, error) {

	var args struct {
		NotificationID string `json:"notification_id"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", nil, fmt.Errorf("invalid arguments: %w", err)
	}

	id, err := uuid.Parse(args.NotificationID)
	if err != nil {
		return "", nil, fmt.Errorf("invalid notification_id: %w", err)
	}

	notif, err := s.repo.GetNotification(ctx, id)
	if err != nil {
		return "", nil, fmt.Errorf("notification not found: %w", err)
	}

	result, _ := json.Marshal(map[string]interface{}{
		"id":      notif.ID.String(),
		"channel": notif.Channel,
		"status":  notif.Status,
		"attempt": notif.Attempt,
	})
	return string(result), nil, nil
}
