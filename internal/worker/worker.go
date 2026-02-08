package worker

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/google/uuid"
	"github.com/lalithlochan/nimbus/internal/db"
)

type Repository interface {
	GetPendingNotifications(ctx context.Context, limit int) ([]*db.Notification, error)
	UpdateNotificationStatus(ctx context.Context, id uuid.UUID, status string, attempt int, errorMsg *string, nextRetryAt *time.Time) error
	MoveToDeadLetter(ctx context.Context, notif *db.Notification, lastError string) (*db.DeadLetterNotification, error)
}

type Worker struct {
	repo   Repository
	sender Sender
	config Config
	logger *zap.Logger
}

type Config struct {
	PollInterval time.Duration
	BatchSize    int
	MaxRetries   int
}

func New(repo Repository, sender Sender, cfg Config, logger *zap.Logger) *Worker {

	if cfg.PollInterval == 0 {
		cfg.PollInterval = 5 * time.Second
	}
	if cfg.BatchSize == 0 {
		cfg.BatchSize = 5
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 3
	}

	return &Worker{
		repo:   repo,
		sender: sender,
		config: cfg,
		logger: logger,
	}
}

func (w *Worker) Start(ctx context.Context) {
	ticker := time.NewTicker(w.config.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("worker stopping")
			return
		case <-ticker.C:
			w.logger.Info("checking for notifications")
			w.processBatch(ctx)
		}
	}
}

func (w *Worker) processBatch(ctx context.Context) {
	// Get pending notifications
	notifications, err := w.repo.GetPendingNotifications(ctx, w.config.BatchSize)
	if err != nil {
		w.logger.Error("failed to get pending notifications", zap.Error(err))
		return
	}
	if len(notifications) == 0 {
		return
	}
	// Loop through each notification from the list of notifications
	for _, notif := range notifications {
		// Process each notification
		w.processNotification(ctx, notif)
	}
}

func (w *Worker) processNotification(ctx context.Context, notif *db.Notification) {
	// Mark as processing first to prevent duplicate picks
	_ = w.repo.UpdateNotificationStatus(ctx, notif.ID, "processing", notif.Attempt, nil, notif.NextRetryAt)

	err := w.sender.Send(ctx, notif)
	newAttempt := notif.Attempt + 1

	if err != nil {
		w.logger.Error("failed to send notification",
			zap.Error(err),
			zap.String("id", notif.ID.String()),
			zap.Int("attempt", newAttempt),
		)

		errMsg := err.Error()

		if newAttempt >= w.config.MaxRetries {
			// Max retries reached, move to dead letter queue
			_, dlqErr := w.repo.MoveToDeadLetter(ctx, notif, errMsg)
			if dlqErr != nil {
				w.logger.Error("failed to move notification to dead letter queue",
					zap.String("id", notif.ID.String()),
					zap.Error(dlqErr),
				)
			} else {
				w.logger.Info("notification moved to dead letter queue",
					zap.String("id", notif.ID.String()),
					zap.Int("attempts", newAttempt),
				)
			}
		} else {
			nextRetry := w.calculateNextRetry(newAttempt)
			_ = w.repo.UpdateNotificationStatus(ctx, notif.ID, "pending", newAttempt, &errMsg, &nextRetry)
		}
	} else {
		w.logger.Info("notification sent",
			zap.String("id", notif.ID.String()),
		)
		_ = w.repo.UpdateNotificationStatus(ctx, notif.ID, "sent", newAttempt, nil, nil)
	}
}

// Calculate next retry time based on attempt
func (w *Worker) calculateNextRetry(attempt int) time.Time {
	delays := []time.Duration{
		1 * time.Minute,  // attempt 1 → wait 1 min
		5 * time.Minute,  // attempt 2 → wait 5 min
		15 * time.Minute, // attempt 3 → w–––
	}

	idx := attempt - 1
	if idx >= len(delays) {
		idx = len(delays) - 1
	}

	return time.Now().Add(delays[idx])
}
