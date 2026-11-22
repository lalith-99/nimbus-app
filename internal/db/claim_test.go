package db

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

func TestReclaimedNotificationRejectsStaleOutcomes(t *testing.T) {
	dsn := os.Getenv("NIMBUS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set NIMBUS_TEST_DATABASE_URL to a migrated disposable PostgreSQL database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	logger := zap.NewNop()
	repo := NewRepository(&DB{pool: pool, logger: logger}, logger)

	// Seed the exact state held by a live worker whose lease elapsed.
	var stale Notification
	err = pool.QueryRow(ctx, `INSERT INTO notifications
		(tenant_id, user_id, channel, payload, status, updated_at)
		VALUES ($1, $2, 'webhook', '{}', 'processing', now() - interval '1 hour')
		RETURNING id, updated_at`, uuid.New(), uuid.New()).Scan(&stale.ID, &stale.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, "DELETE FROM notifications WHERE id=$1", stale.ID)
	defer pool.Exec(ctx, "DELETE FROM dead_letter_notifications WHERE original_notification_id=$1", stale.ID)

	current, err := repo.ClaimNotificationByID(ctx, stale.ID)
	if err != nil || current == nil {
		t.Fatalf("reclaim returned %v, %v", current, err)
	}
	// Both handles now contain valid row data. Only their claim timestamps differ.
	oldTimestamp := stale.UpdatedAt
	stale = *current
	stale.UpdatedAt = oldTimestamp
	retryAt := time.Now().Add(time.Minute)
	reason := "webhook failed"
	if err := repo.FinishClaim(ctx, &stale, "pending", 1, &reason, &retryAt); !errors.Is(err, ErrClaimLost) {
		t.Fatalf("stale retry returned %v", err)
	}
	if err := repo.FinishClaim(ctx, &stale, "sent", 1, nil, nil); !errors.Is(err, ErrClaimLost) {
		t.Fatalf("stale success returned %v", err)
	}
	if _, err := repo.MoveToDeadLetter(ctx, &stale, reason); !errors.Is(err, ErrClaimLost) {
		t.Fatalf("stale dead-letter transition returned %v", err)
	}
	if err := repo.FinishClaim(ctx, current, "sent", 1, nil, nil); err != nil {
		t.Fatalf("current claim failed: %v", err)
	}
	if _, err := repo.MoveToDeadLetter(ctx, &stale, reason); !errors.Is(err, ErrClaimLost) {
		t.Fatalf("stale write after completion returned %v", err)
	}
	var status string
	var attempts, deadLetters int
	err = pool.QueryRow(ctx, `SELECT status, attempt, (SELECT COUNT(*) FROM dead_letter_notifications
		WHERE original_notification_id=$1) FROM notifications WHERE id=$1`, stale.ID).Scan(&status, &attempts, &deadLetters)
	if err != nil || status != "sent" || attempts != 1 || deadLetters != 0 {
		t.Fatalf("final status=%s attempts=%d dead letters=%d err=%v", status, attempts, deadLetters, err)
	}
}
