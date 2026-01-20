package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/lalithlochan/nimbus/internal/db"
	"github.com/lalithlochan/nimbus/internal/redis"
	"github.com/lalithlochan/nimbus/internal/sqs"
)

// NotificationRepository defines the interface for notification database operations
type NotificationRepository interface {
	CreateNotification(ctx context.Context, notif *db.Notification) error
	GetNotification(ctx context.Context, id uuid.UUID) (*db.Notification, error)
	ListNotificationsByTenant(ctx context.Context, tenantID uuid.UUID, limit, offset int) ([]*db.Notification, error)
	UpdateNotificationStatus(ctx context.Context, id uuid.UUID, status string, attempt int, errorMsg *string, nextRetryAt *time.Time) error
	// DLQ methods
	ListDeadLetterByTenant(ctx context.Context, tenantID uuid.UUID, limit, offset int) ([]*db.DeadLetterNotification, error)
	GetDeadLetter(ctx context.Context, id uuid.UUID) (*db.DeadLetterNotification, error)
	RetryDeadLetter(ctx context.Context, id uuid.UUID) (*db.Notification, error)
	DiscardDeadLetter(ctx context.Context, id uuid.UUID) error
}

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
	logger      *zap.Logger
	repo        NotificationRepository
	idempotency *redis.IdempotencyService // nil if Redis not configured
	producer    *sqs.Producer              // nil if SQS not configured
}

// NewHandler creates a new API handler
func NewHandler(logger *zap.Logger, repo NotificationRepository) *Handler {
	return &Handler{
		logger:      logger,
		repo:        repo,
		idempotency: nil, // Idempotency disabled by default
		producer:    nil, // SQS disabled by default
	}
}

// NewHandlerWithIdempotency creates a handler with idempotency support
func NewHandlerWithIdempotency(logger *zap.Logger, repo NotificationRepository, idempotency *redis.IdempotencyService) *Handler {
	return &Handler{
		logger:      logger,
		repo:        repo,
		idempotency: idempotency,
		producer:    nil, // SQS disabled by default
	}
}

// NewHandlerWithSQS creates a handler with SQS producer support
func NewHandlerWithSQS(logger *zap.Logger, repo NotificationRepository, idempotency *redis.IdempotencyService, producer *sqs.Producer) *Handler {
	return &Handler{
		logger:      logger,
		repo:        repo,
		idempotency: idempotency,
		producer:    producer,
	}
}

// CreateNotification handles POST /v1/notifications
// Supports idempotency via the Idempotency-Key header.
func (h *Handler) CreateNotification(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Extract Idempotency-Key header (optional but recommended)
	idempotencyKey := r.Header.Get("Idempotency-Key")

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

	// Check idempotency if key provided
	if idempotencyKey != "" && h.idempotency != nil {
		cachedResult, err := h.idempotency.CheckOrReserve(ctx, req.TenantID, idempotencyKey)

		if err != nil {
			if errors.Is(err, redis.ErrDuplicateRequest) {
				h.writeError(w, http.StatusConflict, "duplicate_request",
					"Request is already being processed",
					"Another request with this idempotency key is in progress")
				return
			}
			h.logger.Warn("idempotency check failed, proceeding",
				zap.Error(err),
				zap.String("idempotency_key", idempotencyKey),
			)
		} else if cachedResult != nil {
			resp := NotificationResponse{ID: cachedResult.NotificationID}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Idempotency-Replayed", "true")
			w.WriteHeader(cachedResult.StatusCode)
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
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
		zap.String("channel", req.Channel),
	)

	// Store idempotency result
	if idempotencyKey != "" && h.idempotency != nil {
		result := &redis.IdempotencyResult{
			NotificationID: notif.ID.String(),
			StatusCode:     http.StatusCreated,
		}
		if err := h.idempotency.Store(ctx, req.TenantID, idempotencyKey, result); err != nil {
			h.logger.Warn("failed to store idempotency result",
				zap.Error(err),
				zap.String("idempotency_key", idempotencyKey),
			)
		}
	}

	// Enqueue to SQS for asynchronous processing
	if h.producer != nil {
		if msgID, err := h.producer.Enqueue(ctx, notif); err != nil {
			h.logger.Error("failed to enqueue notification to sqs",
				zap.Error(err),
				zap.String("notification_id", notif.ID.String()),
			)
			// Continue with error response - SQS enqueue failure is not a client error
			h.writeError(w, http.StatusInternalServerError, "enqueue_error", "Failed to enqueue notification", "")
			return
		} else {
			h.logger.Info("notification enqueued to sqs",
				zap.String("notification_id", notif.ID.String()),
				zap.String("sqs_message_id", msgID),
			)
		}
	}

	resp := NotificationResponse{
		ID: notif.ID.String(),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
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
	_ = json.NewEncoder(w).Encode(notif)
}

// ListNotifications handles GET /v1/notifications?tenant_id=xxx&limit=20&offset=0
func (h *Handler) ListNotifications(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Parse query parameters
	tenantIDStr := r.URL.Query().Get("tenant_id")
	if tenantIDStr == "" {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "Missing tenant_id", "tenant_id query parameter is required")
		return
	}

	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "Invalid tenant_id", "tenant_id must be a valid UUID")
		return
	}

	// Parse pagination parameters with defaults
	limit := 20
	offset := 0

	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}

	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		if o, err := strconv.Atoi(offsetStr); err == nil && o >= 0 {
			offset = o
		}
	}

	// Fetch from database
	notifications, err := h.repo.ListNotificationsByTenant(ctx, tenantID, limit, offset)
	if err != nil {
		h.logger.Error("failed to list notifications",
			zap.Error(err),
			zap.String("tenant_id", tenantIDStr),
		)
		h.writeError(w, http.StatusInternalServerError, "database_error", "Failed to list notifications", "")
		return
	}

	h.logger.Info("notifications listed",
		zap.String("tenant_id", tenantIDStr),
		zap.Int("count", len(notifications)),
		zap.Int("limit", limit),
		zap.Int("offset", offset),
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"data":   notifications,
		"limit":  limit,
		"offset": offset,
		"count":  len(notifications),
	})
}

// UpdateNotificationStatus handles PATCH /v1/notifications/{id}/status
func (h *Handler) UpdateNotificationStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Extract ID from URL
	idStr := chi.URLParam(r, "id")
	notifID, err := uuid.Parse(idStr)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "Invalid notification ID", "ID must be a valid UUID")
		return
	}

	// Parse request body
	var req struct {
		Status  string  `json:"status"`
		Error   *string `json:"error,omitempty"`
		Attempt int     `json:"attempt"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "Malformed JSON body", err.Error())
		return
	}

	// Validate status
	validStatuses := map[string]bool{
		db.StatusPending:    true,
		db.StatusProcessing: true,
		db.StatusSent:       true,
		db.StatusFailed:     true,
	}

	if !validStatuses[req.Status] {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "Invalid status",
			"status must be one of: pending, processing, sent, failed")
		return
	}

	// Validate attempt is not negative
	if req.Attempt < 0 {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "Invalid attempt",
			"attempt must be >= 0")
		return
	}

	// Update in database
	err = h.repo.UpdateNotificationStatus(ctx, notifID, req.Status, req.Attempt, req.Error, nil)
	if err != nil {
		h.logger.Error("failed to update notification status",
			zap.Error(err),
			zap.String("id", idStr),
			zap.String("status", req.Status),
		)
		h.writeError(w, http.StatusInternalServerError, "database_error", "Failed to update notification", "")
		return
	}

	h.logger.Info("notification status updated",
		zap.String("id", idStr),
		zap.String("status", req.Status),
		zap.Int("attempt", req.Attempt),
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"id":     idStr,
		"status": req.Status,
	})
}

// ListDeadLetterQueue handles GET /v1/dlq?tenant_id=xxx&limit=20&offset=0
func (h *Handler) ListDeadLetterQueue(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Parse query parameters
	tenantIDStr := r.URL.Query().Get("tenant_id")
	if tenantIDStr == "" {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "Missing tenant_id", "tenant_id query parameter is required")
		return
	}

	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "Invalid tenant_id", "tenant_id must be a valid UUID")
		return
	}

	// Parse pagination parameters with defaults
	limit := 20
	offset := 0

	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}

	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		if o, err := strconv.Atoi(offsetStr); err == nil && o >= 0 {
			offset = o
		}
	}

	// Fetch from database
	dlqItems, err := h.repo.ListDeadLetterByTenant(ctx, tenantID, limit, offset)
	if err != nil {
		h.logger.Error("failed to list dead letter queue",
			zap.Error(err),
			zap.String("tenant_id", tenantIDStr),
		)
		h.writeError(w, http.StatusInternalServerError, "database_error", "Failed to list dead letter queue", "")
		return
	}

	h.logger.Info("dead letter queue listed",
		zap.String("tenant_id", tenantIDStr),
		zap.Int("count", len(dlqItems)),
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"data":   dlqItems,
		"limit":  limit,
		"offset": offset,
		"count":  len(dlqItems),
	})
}

// GetDeadLetterItem handles GET /v1/dlq/{id}
func (h *Handler) GetDeadLetterItem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	idStr := chi.URLParam(r, "id")
	dlqID, err := uuid.Parse(idStr)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "Invalid DLQ ID", "ID must be a valid UUID")
		return
	}

	dlqItem, err := h.repo.GetDeadLetter(ctx, dlqID)
	if err != nil {
		h.logger.Error("failed to get dead letter item",
			zap.Error(err),
			zap.String("id", idStr),
		)
		h.writeError(w, http.StatusNotFound, "not_found", "Dead letter item not found", "")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(dlqItem)
}

// RetryDeadLetterItem handles POST /v1/dlq/{id}/retry
func (h *Handler) RetryDeadLetterItem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	idStr := chi.URLParam(r, "id")
	dlqID, err := uuid.Parse(idStr)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "Invalid DLQ ID", "ID must be a valid UUID")
		return
	}

	// Retry creates a new notification from the DLQ item
	newNotif, err := h.repo.RetryDeadLetter(ctx, dlqID)
	if err != nil {
		h.logger.Error("failed to retry dead letter item",
			zap.Error(err),
			zap.String("id", idStr),
		)
		h.writeError(w, http.StatusInternalServerError, "database_error", "Failed to retry dead letter item", "")
		return
	}

	h.logger.Info("dead letter item retried",
		zap.String("dlq_id", idStr),
		zap.String("new_notification_id", newNotif.ID.String()),
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"id":                  idStr,
		"status":              "retried",
		"new_notification_id": newNotif.ID.String(),
	})
}

// DiscardDeadLetterItem handles POST /v1/dlq/{id}/discard
func (h *Handler) DiscardDeadLetterItem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	idStr := chi.URLParam(r, "id")
	dlqID, err := uuid.Parse(idStr)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "Invalid DLQ ID", "ID must be a valid UUID")
		return
	}

	err = h.repo.DiscardDeadLetter(ctx, dlqID)
	if err != nil {
		h.logger.Error("failed to discard dead letter item",
			zap.Error(err),
			zap.String("id", idStr),
		)
		h.writeError(w, http.StatusInternalServerError, "database_error", "Failed to discard dead letter item", "")
		return
	}

	h.logger.Info("dead letter item discarded",
		zap.String("id", idStr),
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"id":     idStr,
		"status": "discarded",
	})
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
