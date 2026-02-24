package ai

import (
	"encoding/json"
	"net/http"

	"go.uber.org/zap"
)

// Handler exposes AI features as HTTP endpoints.
type Handler struct {
	compose *ComposeService
	logger  *zap.Logger
}

// NewHandler creates a new AI HTTP handler.
func NewHandler(compose *ComposeService, logger *zap.Logger) *Handler {
	return &Handler{
		compose: compose,
		logger:  logger,
	}
}

// HandleCompose handles POST /v1/ai/compose
// Accepts a natural language prompt and creates notifications via LLM function calling.
//
// Request body:
//
//	{
//	    "prompt": "Send a welcome email to alice@example.com",
//	    "tenant_id": "uuid",
//	    "user_id": "uuid"
//	}
//
// Response:
//
//	{
//	    "message": "I've sent a welcome email to alice@example.com.",
//	    "notification_ids": ["uuid"]
//	}
func (h *Handler) HandleCompose(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req ComposeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", "Malformed JSON body", err.Error())
		return
	}

	if req.Prompt == "" {
		writeErr(w, http.StatusBadRequest, "invalid_request", "Missing prompt", "prompt field is required")
		return
	}
	if req.TenantID == "" || req.UserID == "" {
		writeErr(w, http.StatusBadRequest, "invalid_request", "Missing required fields", "tenant_id and user_id are required")
		return
	}

	h.logger.Info("AI compose request",
		zap.String("tenant_id", req.TenantID),
		zap.String("prompt", req.Prompt),
	)

	resp, err := h.compose.Compose(ctx, req)
	if err != nil {
		h.logger.Error("AI compose failed",
			zap.Error(err),
			zap.String("tenant_id", req.TenantID),
		)
		writeErr(w, http.StatusInternalServerError, "ai_error", "AI processing failed", err.Error())
		return
	}

	h.logger.Info("AI compose completed",
		zap.String("tenant_id", req.TenantID),
		zap.Int("notifications_created", len(resp.NotificationIDs)),
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// ErrorResponse represents an error in problem+json format.
type ErrorResponse struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
}

func writeErr(w http.ResponseWriter, status int, errType, title, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(ErrorResponse{
		Type:   errType,
		Title:  title,
		Status: status,
		Detail: detail,
	})
}
