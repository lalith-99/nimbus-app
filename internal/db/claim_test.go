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

func TestClaimBatchOrdersEligibleWorkAndHonorsLeases(t *testing.T) {
	dsn := os.Getenv("NIMBUS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires a migrated disposable PostgreSQL database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	// The temporary fixture shadows the real table only on this session.
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `CREATE TEMP TABLE notifications (LIKE notifications INCLUDING ALL);
		CREATE TRIGGER touch BEFORE UPDATE ON notifications FOR EACH ROW EXECUTE FUNCTION update_updated_at_column()`); err != nil {
		t.Fatal(err)
	}

	logger := zap.NewNop()
	repo := NewRepository(&DB{pool: pool, logger: logger}, logger)
	insert := func(state, created, retry, updated string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		err := pool.QueryRow(ctx, `INSERT INTO notifications
			(tenant_id, user_id, channel, payload, status, created_at, next_retry_at, updated_at)
			VALUES ($1, $2, 'webhook', '{}', $3, now() + $4::text::interval,
			        now() + nullif($5::text, '')::interval, now() + $6::text::interval) RETURNING id`,
			uuid.New(), uuid.New(), state, created, retry, updated).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	// Creation order and eligibility order intentionally differ.
	retry := insert("pending", "-3 days", "-1 minute", "-1 day")
	fresh := insert("pending", "-1 hour", "", "-1 hour")
	stale := insert("processing", "-2 days", "", "-15 minutes")
	future := insert("pending", "-4 days", "1 day", "-1 day")
	active := insert("processing", "-5 days", "", "-1 minute")
	for _, id := range []uuid.UUID{fresh, stale, retry} {
		got, err := repo.ClaimPendingNotifications(ctx, 1)
		if err != nil || len(got) != 1 || got[0].ID != id {
			t.Fatalf("expected %v, got %v, err %v", id, got, err)
		}
	}
	if got, err := repo.ClaimPendingNotifications(ctx, 10); err != nil || len(got) != 0 {
		t.Fatalf("expected drained queue, got %v, err %v", got, err)
	}
	for _, id := range []uuid.UUID{future, active, stale} {
		if got, err := repo.ClaimNotificationByID(ctx, id); err != nil || got != nil {
			t.Fatalf("claimed ineligible row %v: %v, err %v", id, got, err)
		}
	}
}
