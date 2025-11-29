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
	UpdateNotificationStatus(ctx context.Context, id uuid.UUID, status string, attempt int, errorMsg *string) error
}

type Sender interface {
	Send(ctx context.Context, notif *db.Notification) error
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
	w.repo.UpdateNotificationStatus(ctx, notif.ID, "processing", notif.Attempt, nil)

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
			// Max retries reached, mark as failed
			w.repo.UpdateNotificationStatus(ctx, notif.ID, "failed", newAttempt, &errMsg)
		} else {
			// Retry later - set back to pending
			w.repo.UpdateNotificationStatus(ctx, notif.ID, "pending", newAttempt, &errMsg)
		}
	} else {
		w.logger.Info("notification sent",
			zap.String("id", notif.ID.String()),
		)
		w.repo.UpdateNotificationStatus(ctx, notif.ID, "sent", newAttempt, nil)
	}
}
