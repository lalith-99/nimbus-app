package redis

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

func setupTestRedis(t *testing.T) (*Client, func()) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	client := &Client{rdb: rdb, logger: zap.NewNop()}

	return client, func() {
		rdb.Close()
		mr.Close()
	}
}

func TestIdempotencyService_NewRequest(t *testing.T) {
	client, cleanup := setupTestRedis(t)
	defer cleanup()

	svc := NewIdempotencyService(client, zap.NewNop())
	ctx := context.Background()

	result, err := svc.CheckOrReserve(ctx, "tenant-1", "key-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil result for new request, got: %+v", result)
	}
}

func TestIdempotencyService_DuplicateRequest(t *testing.T) {
	client, cleanup := setupTestRedis(t)
	defer cleanup()

	svc := NewIdempotencyService(client, zap.NewNop())
	ctx := context.Background()

	// First request
	if _, err := svc.CheckOrReserve(ctx, "tenant-1", "key-1"); err != nil {
		t.Fatalf("first request failed: %v", err)
	}

	// Duplicate request
	if _, err := svc.CheckOrReserve(ctx, "tenant-1", "key-1"); err != ErrDuplicateRequest {
		t.Fatalf("expected ErrDuplicateRequest, got: %v", err)
	}
}

func TestIdempotencyService_CachedResult(t *testing.T) {
	client, cleanup := setupTestRedis(t)
	defer cleanup()

	svc := NewIdempotencyService(client, zap.NewNop())
	ctx := context.Background()

	stored := &IdempotencyResult{
		NotificationID: "notif-123",
		StatusCode:     201,
		CreatedAt:      time.Now().Unix(),
	}

	if err := svc.Store(ctx, "tenant-1", "key-1", stored, IdempotencyTTL); err != nil {
		t.Fatalf("store failed: %v", err)
	}

	result, err := svc.Check(ctx, "tenant-1", "key-1")
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}
	if result == nil {
		t.Fatal("expected cached result")
	}
	if result.NotificationID != "notif-123" {
		t.Errorf("expected notif-123, got %s", result.NotificationID)
	}
}

func TestIdempotencyService_TenantIsolation(t *testing.T) {
	client, cleanup := setupTestRedis(t)
	defer cleanup()

	svc := NewIdempotencyService(client, zap.NewNop())
	ctx := context.Background()

	// Tenant A reserves a key
	if _, err := svc.CheckOrReserve(ctx, "tenant-A", "same-key"); err != nil {
		t.Fatalf("tenant A failed: %v", err)
	}

	// Tenant B can use the same key
	result, err := svc.CheckOrReserve(ctx, "tenant-B", "same-key")
	if err != nil {
		t.Fatalf("tenant B should succeed: %v", err)
	}
	if result != nil {
		t.Fatal("tenant B should get nil (new request)")
	}
}

func TestIdempotencyService_ReserveThenStore(t *testing.T) {
	client, cleanup := setupTestRedis(t)
	defer cleanup()

	svc := NewIdempotencyService(client, zap.NewNop())
	ctx := context.Background()

	// Reserve
	reserved, err := svc.Reserve(ctx, "tenant-1", "key-1")
	if err != nil || !reserved {
		t.Fatalf("reserve failed: %v, reserved: %v", err, reserved)
	}

	// Store result
	if err := svc.Store(ctx, "tenant-1", "key-1", &IdempotencyResult{
		NotificationID: "notif-789",
		StatusCode:     201,
	}, IdempotencyTTL); err != nil {
		t.Fatalf("store failed: %v", err)
	}

	// Check returns stored result
	cached, err := svc.Check(ctx, "tenant-1", "key-1")
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}
	if cached.NotificationID != "notif-789" {
		t.Errorf("expected notif-789, got %s", cached.NotificationID)
	}
}

// TestIdempotencyService_ReleaseFreesReservation verifies the fix for the
// reservation-leak bug: after a failed request releases its reservation, a
// retry must be treated as a brand-new request (not blocked with 409).
func TestIdempotencyService_ReleaseFreesReservation(t *testing.T) {
	client, cleanup := setupTestRedis(t)
	defer cleanup()

	svc := NewIdempotencyService(client, zap.NewNop())
	ctx := context.Background()

	// First attempt reserves the key.
	if _, err := svc.CheckOrReserve(ctx, "tenant-1", "key-1"); err != nil {
		t.Fatalf("first reserve failed: %v", err)
	}

	// Simulate the request failing → release the reservation.
	if err := svc.Release(ctx, "tenant-1", "key-1"); err != nil {
		t.Fatalf("release failed: %v", err)
	}

	// A retry must now succeed as a NEW request, not get ErrDuplicateRequest.
	result, err := svc.CheckOrReserve(ctx, "tenant-1", "key-1")
	if err != nil {
		t.Fatalf("retry after release should succeed, got: %v", err)
	}
	if result != nil {
		t.Fatalf("retry after release should be a new request, got cached: %+v", result)
	}
}

// TestIdempotencyService_ReleaseDoesNotClobberStoredResult verifies the
// compare-and-delete safety property: Release must NOT delete a key that already
// holds a real success result, or a duplicate request could re-execute.
func TestIdempotencyService_ReleaseDoesNotClobberStoredResult(t *testing.T) {
	client, cleanup := setupTestRedis(t)
	defer cleanup()

	svc := NewIdempotencyService(client, zap.NewNop())
	ctx := context.Background()

	if _, err := svc.CheckOrReserve(ctx, "tenant-1", "key-1"); err != nil {
		t.Fatalf("reserve failed: %v", err)
	}

	// Request succeeded and stored its result.
	if err := svc.Store(ctx, "tenant-1", "key-1", &IdempotencyResult{
		NotificationID: "notif-keep",
		StatusCode:     201,
	}, IdempotencyTTL); err != nil {
		t.Fatalf("store failed: %v", err)
	}

	// A late/erroneous Release must be a no-op because the value is no longer
	// the processing marker — the stored result has to survive.
	if err := svc.Release(ctx, "tenant-1", "key-1"); err != nil {
		t.Fatalf("release failed: %v", err)
	}

	cached, err := svc.Check(ctx, "tenant-1", "key-1")
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}
	if cached == nil || cached.NotificationID != "notif-keep" {
		t.Fatalf("stored result must survive Release, got: %+v", cached)
	}
}
