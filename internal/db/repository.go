package db

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// Repository handles database operations for notifications
type Repository struct {
	db     *DB
	logger *zap.Logger
}

// NewRepository creates a new notification repository
func NewRepository(db *DB, logger *zap.Logger) *Repository {
	return &Repository{
		db:     db,
		logger: logger,
	}
}

// CreateNotification inserts a new notification into the database
func (r *Repository) CreateNotification(ctx context.Context, notif *Notification) error {
	query := `
		INSERT INTO notifications (
			id, tenant_id, user_id, channel, payload, 
			status, attempt, next_retry_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8
		)
		RETURNING created_at, updated_at
	`

	err := r.db.Pool().QueryRow(
		ctx,
		query,
		notif.ID,
		notif.TenantID,
		notif.UserID,
		notif.Channel,
		notif.Payload,
		notif.Status,
		notif.Attempt,
		notif.NextRetryAt,
	).Scan(&notif.CreatedAt, &notif.UpdatedAt)

	if err != nil {
		r.logger.Error("failed to create notification",
			zap.Error(err),
			zap.String("notification_id", notif.ID.String()),
		)
		return fmt.Errorf("insert notification: %w", err)
	}

	r.logger.Info("notification created",
		zap.String("notification_id", notif.ID.String()),
		zap.String("tenant_id", notif.TenantID.String()),
		zap.String("channel", notif.Channel),
	)

	return nil
}

// GetNotification retrieves a notification by ID
func (r *Repository) GetNotification(ctx context.Context, id uuid.UUID) (*Notification, error) {
	query := `
		SELECT 
			id, tenant_id, user_id, channel, payload,
			status, attempt, error_message, next_retry_at,
			created_at, updated_at
		FROM notifications
		WHERE id = $1
	`

	var notif Notification
	err := r.db.Pool().QueryRow(ctx, query, id).Scan(
		&notif.ID,
		&notif.TenantID,
		&notif.UserID,
		&notif.Channel,
		&notif.Payload,
		&notif.Status,
		&notif.Attempt,
		&notif.ErrorMessage,
		&notif.NextRetryAt,
		&notif.CreatedAt,
		&notif.UpdatedAt,
	)

	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("notification not found: %s", id)
	}

	if err != nil {
		r.logger.Error("failed to get notification",
			zap.Error(err),
			zap.String("notification_id", id.String()),
		)
		return nil, fmt.Errorf("query notification: %w", err)
	}

	return &notif, nil
}

// UpdateNotificationStatus updates the status and error message of a notification
func (r *Repository) UpdateNotificationStatus(
	ctx context.Context,
	id uuid.UUID,
	status string,
	attempt int,
	errorMsg *string,
	nextRetryAt *time.Time,
) error {
	query := `
		UPDATE notifications
		SET status = $1, attempt = $2, error_message = $3, next_retry_at = $4
		WHERE id = $5
	`

	result, err := r.db.Pool().Exec(ctx, query, status, attempt, errorMsg, nextRetryAt, id)
	if err != nil {
		r.logger.Error("failed to update notification status",
			zap.Error(err),
			zap.String("notification_id", id.String()),
		)
		return fmt.Errorf("update notification status: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("notification not found: %s", id)
	}

	return nil
}

// ErrClaimLost means the notification was completed, changed, or reclaimed.
var ErrClaimLost = errors.New("notification claim is no longer current")

// FinishClaim uses the database timestamp returned by the claim, shared by
// both claiming paths. A timed-out worker must not replace a later outcome.
func (r *Repository) FinishClaim(ctx context.Context, notif *Notification, status string, attempt int, errorMsg *string, nextRetryAt *time.Time) error {
	result, err := r.db.Pool().Exec(ctx, `UPDATE notifications
		SET status=$1, attempt=$2, error_message=$3, next_retry_at=$4
		WHERE id=$5 AND status='processing' AND updated_at=$6`,
		status, attempt, errorMsg, nextRetryAt, notif.ID, notif.UpdatedAt)
	if err != nil {
		return fmt.Errorf("finish claimed notification: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrClaimLost
	}
	return nil
}

// ListNotificationsByTenant retrieves notifications for a tenant with pagination
func (r *Repository) ListNotificationsByTenant(
	ctx context.Context,
	tenantID uuid.UUID,
	limit int,
	offset int,
) ([]*Notification, error) {
	query := `
		SELECT 
			id, tenant_id, user_id, channel, payload,
			status, attempt, error_message, next_retry_at,
			created_at, updated_at
		FROM notifications
		WHERE tenant_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`

	rows, err := r.db.Pool().Query(ctx, query, tenantID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("query notifications: %w", err)
	}
	defer rows.Close()

	var notifications []*Notification
	for rows.Next() {
		var notif Notification
		err := rows.Scan(
			&notif.ID,
			&notif.TenantID,
			&notif.UserID,
			&notif.Channel,
			&notif.Payload,
			&notif.Status,
			&notif.Attempt,
			&notif.ErrorMessage,
			&notif.NextRetryAt,
			&notif.CreatedAt,
			&notif.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan notification: %w", err)
		}
		notifications = append(notifications, &notif)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows: %w", err)
	}

	return notifications, nil
}

func (r *Repository) GetPendingNotifications(ctx context.Context, limit int) ([]*Notification, error) {
	query := `
		SELECT 
			id, tenant_id, user_id, channel, payload,
			status, attempt, error_message, next_retry_at,
			created_at, updated_at
		FROM notifications
		WHERE status = 'pending' AND (next_retry_at IS NULL OR next_retry_at <= NOW())
		ORDER BY created_at ASC
		LIMIT $1
	`

	rows, err := r.db.Pool().Query(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("query pending notifications: %w", err)
	}
	defer rows.Close()

	var notifications []*Notification
	for rows.Next() {
		var notif Notification
		err := rows.Scan(
			&notif.ID,
			&notif.TenantID,
			&notif.UserID,
			&notif.Channel,
			&notif.Payload,
			&notif.Status,
			&notif.Attempt,
			&notif.ErrorMessage,
			&notif.NextRetryAt,
			&notif.CreatedAt,
			&notif.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan notification: %w", err)
		}
		notifications = append(notifications, &notif)
	}

	return notifications, nil
}

//go:embed sql/claim_pending.sql
var claimPendingSQL string

// ClaimPendingNotifications locks a batch ordered by eligibility and marks its
// rows processing in one statement. SKIP LOCKED lets overlapping workers take
// disjoint rows. Expired work becomes eligible again after five minutes.
func (r *Repository) ClaimPendingNotifications(ctx context.Context, limit int) ([]*Notification, error) {
	rows, err := r.db.Pool().Query(ctx, claimPendingSQL, limit)
	if err != nil {
		return nil, fmt.Errorf("claim pending notifications: %w", err)
	}
	defer rows.Close()

	var notifications []*Notification
	for rows.Next() {
		var notif Notification
		if err := rows.Scan(
			&notif.ID,
			&notif.TenantID,
			&notif.UserID,
			&notif.Channel,
			&notif.Payload,
			&notif.Status,
			&notif.Attempt,
			&notif.ErrorMessage,
			&notif.NextRetryAt,
			&notif.CreatedAt,
			&notif.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan claimed notification: %w", err)
		}
		notifications = append(notifications, &notif)
	}

	return notifications, rows.Err()
}

// ClaimNotificationByID atomically claims a SINGLE notification by id, marking it
// 'processing'. This is the entry point for the SQS-driven hot path.
//
// WHY IT EXISTS (claim coordination):
// Both the SQS consumer and the DB poller claim through the notification row.
// This conditional UPDATE marks an eligible row as processing. A competing
// claim that finds the row no longer eligible returns no rows. Unlike the batch
// query, this statement does not use SKIP LOCKED. Claims coordinate active work;
// they cannot prevent every duplicate provider effect after a crash or timeout.
//
// It returns (nil, nil) — NOT an error — when the row is not claimable, i.e.:
//   - already 'sent' / 'dead_lettered' (a duplicate SQS redelivery), or
//   - already claimed by another worker/consumer ('processing', not yet stuck), or
//   - a scheduled retry whose next_retry_at is still in the future.
//
// The caller (SQS consumer) treats (nil, nil) as "someone else owns this" and
// simply deletes the SQS message. Delayed retries are intentionally left for the
// poller backstop, since an SQS message is one-shot and can't be re-delivered at
// next_retry_at.
func (r *Repository) ClaimNotificationByID(ctx context.Context, id uuid.UUID) (*Notification, error) {
	query := `
		UPDATE notifications
		SET status = 'processing', updated_at = NOW()
		WHERE id = $1
		  AND status IN ('pending', 'processing')
		  AND nimbus_claimable_at(status, created_at, next_retry_at, updated_at) <= now() AT TIME ZONE 'UTC'
		RETURNING
			id, tenant_id, user_id, channel, payload,
			status, attempt, error_message, next_retry_at,
			created_at, updated_at
	`

	var notif Notification
	err := r.db.Pool().QueryRow(ctx, query, id).Scan(
		&notif.ID,
		&notif.TenantID,
		&notif.UserID,
		&notif.Channel,
		&notif.Payload,
		&notif.Status,
		&notif.Attempt,
		&notif.ErrorMessage,
		&notif.NextRetryAt,
		&notif.CreatedAt,
		&notif.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		// Not claimable — another path owns it, it's already terminal, or the
		// retry isn't due yet. Not an error: the caller should skip and ack.
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim notification %s: %w", id, err)
	}

	return &notif, nil
}

// MoveToDeadLetter moves a failed notification to the dead letter queue
func (r *Repository) MoveToDeadLetter(ctx context.Context, notif *Notification, lastError string) (*DeadLetterNotification, error) {
	// Start a transaction
	tx, err := r.db.Pool().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	result, err := tx.Exec(ctx, `UPDATE notifications SET status=$1, attempt=$2, error_message=$3, next_retry_at=NULL
		WHERE id=$4 AND status='processing' AND updated_at=$5`, StatusDeadLettered, notif.Attempt, lastError, notif.ID, notif.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("finish dead-lettered notification: %w", err)
	}
	if result.RowsAffected() == 0 {
		return nil, ErrClaimLost
	}

	// Insert into dead letter queue
	dlq := &DeadLetterNotification{
		ID:                     uuid.New(),
		OriginalNotificationID: notif.ID,
		TenantID:               notif.TenantID,
		UserID:                 notif.UserID,
		Channel:                notif.Channel,
		Payload:                notif.Payload,
		Attempts:               notif.Attempt,
		LastError:              lastError,
		Status:                 DLQStatusPending,
	}

	insertQuery := `
		INSERT INTO dead_letter_notifications (
			id, original_notification_id, tenant_id, user_id, channel,
			payload, attempts, last_error, status
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING created_at, updated_at
	`

	err = tx.QueryRow(ctx, insertQuery,
		dlq.ID,
		dlq.OriginalNotificationID,
		dlq.TenantID,
		dlq.UserID,
		dlq.Channel,
		dlq.Payload,
		dlq.Attempts,
		dlq.LastError,
		dlq.Status,
	).Scan(&dlq.CreatedAt, &dlq.UpdatedAt)

	if err != nil {
		return nil, fmt.Errorf("insert dead letter: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}

	r.logger.Info("notification moved to dead letter queue",
		zap.String("notification_id", notif.ID.String()),
		zap.String("dlq_id", dlq.ID.String()),
		zap.String("last_error", lastError),
	)

	return dlq, nil
}

// ListDeadLetterByTenant retrieves DLQ items for a tenant
func (r *Repository) ListDeadLetterByTenant(ctx context.Context, tenantID uuid.UUID, limit, offset int) ([]*DeadLetterNotification, error) {
	query := `
		SELECT 
			id, original_notification_id, tenant_id, user_id, channel,
			payload, attempts, last_error, status, retried_notification_id,
			created_at, updated_at
		FROM dead_letter_notifications
		WHERE tenant_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`

	rows, err := r.db.Pool().Query(ctx, query, tenantID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("query dead letter notifications: %w", err)
	}
	defer rows.Close()

	var items []*DeadLetterNotification
	for rows.Next() {
		var dlq DeadLetterNotification
		err := rows.Scan(
			&dlq.ID,
			&dlq.OriginalNotificationID,
			&dlq.TenantID,
			&dlq.UserID,
			&dlq.Channel,
			&dlq.Payload,
			&dlq.Attempts,
			&dlq.LastError,
			&dlq.Status,
			&dlq.RetriedNotificationID,
			&dlq.CreatedAt,
			&dlq.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan dead letter: %w", err)
		}
		items = append(items, &dlq)
	}

	return items, nil
}

// GetDeadLetter retrieves a single DLQ item by ID
func (r *Repository) GetDeadLetter(ctx context.Context, id uuid.UUID) (*DeadLetterNotification, error) {
	query := `
		SELECT 
			id, original_notification_id, tenant_id, user_id, channel,
			payload, attempts, last_error, status, retried_notification_id,
			created_at, updated_at
		FROM dead_letter_notifications
		WHERE id = $1
	`

	var dlq DeadLetterNotification
	err := r.db.Pool().QueryRow(ctx, query, id).Scan(
		&dlq.ID,
		&dlq.OriginalNotificationID,
		&dlq.TenantID,
		&dlq.UserID,
		&dlq.Channel,
		&dlq.Payload,
		&dlq.Attempts,
		&dlq.LastError,
		&dlq.Status,
		&dlq.RetriedNotificationID,
		&dlq.CreatedAt,
		&dlq.UpdatedAt,
	)

	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("dead letter not found: %s", id)
	}

	if err != nil {
		return nil, fmt.Errorf("query dead letter: %w", err)
	}

	return &dlq, nil
}

var ErrDeadLetterProcessed = errors.New("dead letter already processed")

// RetryDeadLetter creates a new notification from a DLQ item and marks it as retried
func (r *Repository) RetryDeadLetter(ctx context.Context, dlqID uuid.UUID) (*Notification, error) {
	// Get the DLQ item
	dlq, err := r.GetDeadLetter(ctx, dlqID)
	if err != nil {
		return nil, err
	}

	if dlq.Status != DLQStatusPending {
		return nil, ErrDeadLetterProcessed
	}

	// Start transaction
	tx, err := r.db.Pool().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Create new notification
	newNotif := &Notification{
		ID:       uuid.New(),
		TenantID: dlq.TenantID,
		UserID:   dlq.UserID,
		Channel:  dlq.Channel,
		Payload:  dlq.Payload,
		Status:   StatusPending,
		Attempt:  0,
	}

	insertQuery := `
		INSERT INTO notifications (
			id, tenant_id, user_id, channel, payload, status, attempt
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING created_at, updated_at
	`

	err = tx.QueryRow(ctx, insertQuery,
		newNotif.ID,
		newNotif.TenantID,
		newNotif.UserID,
		newNotif.Channel,
		newNotif.Payload,
		newNotif.Status,
		newNotif.Attempt,
	).Scan(&newNotif.CreatedAt, &newNotif.UpdatedAt)

	if err != nil {
		return nil, fmt.Errorf("insert retry notification: %w", err)
	}

	// Update DLQ item
	updateQuery := `
		UPDATE dead_letter_notifications 
		SET status = $1, retried_notification_id = $2
		WHERE id = $3 AND status = $4
	`
	result, err := tx.Exec(ctx, updateQuery, DLQStatusRetried, newNotif.ID, dlqID, DLQStatusPending)
	if err != nil {
		return nil, fmt.Errorf("update dead letter: %w", err)
	}
	if result.RowsAffected() != 1 {
		// A concurrent retry or discard won. Roll back the speculative insert.
		return nil, ErrDeadLetterProcessed
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}

	r.logger.Info("dead letter retried",
		zap.String("dlq_id", dlqID.String()),
		zap.String("new_notification_id", newNotif.ID.String()),
	)

	return newNotif, nil
}

// DiscardDeadLetter marks a DLQ item as discarded (won't be retried)
func (r *Repository) DiscardDeadLetter(ctx context.Context, dlqID uuid.UUID) error {
	query := `
		UPDATE dead_letter_notifications 
		SET status = $1
		WHERE id = $2 AND status = $3
	`

	result, err := r.db.Pool().Exec(ctx, query, DLQStatusDiscarded, dlqID, DLQStatusPending)
	if err != nil {
		return fmt.Errorf("discard dead letter: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("dead letter not found or already processed")
	}

	r.logger.Info("dead letter discarded", zap.String("dlq_id", dlqID.String()))

	return nil
}
