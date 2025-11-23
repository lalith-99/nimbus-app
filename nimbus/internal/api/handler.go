package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/lalithlochan/nimbus/internal/db"
)

// NotificationRequest represents the incoming request body
type NotificationRequest struct {
	TenantID string          `json:"tenant_id"`
	UserID   string          `json:"user_id"`
	Channel  string          `json:"channel"`
	Payload  json.RawMessage `json:"payload"`
}

// NotificationResponse is returned after creating a notification
type NotificationResponse struct {
	ID string `json:"id"`
}

// ErrorResponse represents an error in problem+json format
type ErrorResponse struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// Handler holds dependencies for API handlers
type Handler struct {
	logger *zap.Logger
	repo   *db.Repository
}

// NewHandler creates a new API handler
func NewHandler(logger *zap.Logger, repo *db.Repository) *Handler {
	return &Handler{
		logger: logger,
		repo:   repo,
	}
}

// CreateNotification handles POST /v1/notifications
func (h *Handler) CreateNotification(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req NotificationRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "Malformed JSON body", err.Error())
		return
	}

	// Validate required fields
	if req.TenantID == "" || req.UserID == "" || req.Channel == "" {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "Missing required fields", "tenant_id, user_id, and channel are required")
		return
	}

	// Validate channel
	if req.Channel != db.ChannelEmail && req.Channel != db.ChannelSMS && req.Channel != db.ChannelWebhook {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "Invalid channel", "channel must be email, sms, or webhook")
		return
	}

	// Validate payload is valid JSON
	if !json.Valid(req.Payload) {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "Invalid payload", "payload must be valid JSON")
		return
	}

	// Parse tenant and user IDs
	tenantID, err := uuid.Parse(req.TenantID)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "Invalid tenant_id", "tenant_id must be a valid UUID")
		return
	}

	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "Invalid user_id", "user_id must be a valid UUID")
		return
	}

	// Create notification
	notif := &db.Notification{
		ID:       uuid.New(),
		TenantID: tenantID,
		UserID:   userID,
		Channel:  req.Channel,
		Payload:  req.Payload,
		Status:   db.StatusPending,
		Attempt:  0,
	}

	// Save to database
	if err := h.repo.CreateNotification(ctx, notif); err != nil {
		h.logger.Error("failed to create notification",
			zap.Error(err),
			zap.String("tenant_id", req.TenantID),
			zap.String("channel", req.Channel),
		)
		h.writeError(w, http.StatusInternalServerError, "database_error", "Failed to create notification", "")
		return
	}

	h.logger.Info("notification created",
		zap.String("id", notif.ID.String()),
		zap.String("tenant_id", req.TenantID),
		zap.String("user_id", req.UserID),
		zap.String("channel", req.Channel),
	)

	// TODO: Enqueue to SQS

	resp := NotificationResponse{
		ID: notif.ID.String(),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(resp)
}

// GetNotification handles GET /v1/notifications/{id}
func (h *Handler) GetNotification(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Extract ID from URL using chi
	idStr := chi.URLParam(r, "id")

	notifID, err := uuid.Parse(idStr)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "Invalid notification ID", "ID must be a valid UUID")
		return
	}

	// Fetch from database
	notif, err := h.repo.GetNotification(ctx, notifID)
	if err != nil {
		h.logger.Error("failed to get notification",
			zap.Error(err),
			zap.String("id", idStr),
		)
		h.writeError(w, http.StatusNotFound, "not_found", "Notification not found", "")
		return
	}

	h.logger.Info("notification retrieved",
		zap.String("id", notif.ID.String()),
		zap.String("channel", notif.Channel),
		zap.String("status", notif.Status),
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(notif)
}

func (h *Handler) writeError(w http.ResponseWriter, status int, errType, title, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)

	json.NewEncoder(w).Encode(ErrorResponse{
		Type:   errType,
		Title:  title,
		Status: status,
		Detail: detail,
	})
}
