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
