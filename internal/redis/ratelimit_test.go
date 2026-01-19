package redis

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

func setupTestRateLimiter(t *testing.T, limit int, window time.Duration) (*RateLimiter, func()) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	client := &Client{rdb: rdb, logger: zap.NewNop()}

	limiter := NewRateLimiter(client, zap.NewNop(), RateLimitConfig{
		Limit:  limit,
		Window: window,
	})

	return limiter, func() {
		rdb.Close()
		mr.Close()
	}
}

func TestRateLimiter_AllowsWithinLimit(t *testing.T) {
	limiter, cleanup := setupTestRateLimiter(t, 5, time.Minute)
	defer cleanup()

	ctx := context.Background()

	for i := 0; i < 5; i++ {
		result, err := limiter.Allow(ctx, "test-key")
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
		if !result.Allowed {
			t.Fatalf("request %d should be allowed", i)
		}
		if result.Remaining != 4-i {
			t.Errorf("request %d: expected remaining %d, got %d", i, 4-i, result.Remaining)
		}
	}
}

func TestRateLimiter_BlocksOverLimit(t *testing.T) {
	limiter, cleanup := setupTestRateLimiter(t, 3, time.Minute)
	defer cleanup()

	ctx := context.Background()

	// Use up the limit
	for i := 0; i < 3; i++ {
		result, _ := limiter.Allow(ctx, "test-key")
		if !result.Allowed {
			t.Fatalf("request %d should be allowed", i)
		}
	}

	// Next request should be blocked
	result, err := limiter.Allow(ctx, "test-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Allowed {
		t.Fatal("request should be blocked")
	}
	if result.Remaining != 0 {
		t.Errorf("expected remaining 0, got %d", result.Remaining)
	}
}

func TestRateLimiter_SeparateKeys(t *testing.T) {
	limiter, cleanup := setupTestRateLimiter(t, 2, time.Minute)
	defer cleanup()

	ctx := context.Background()

	// Key A uses its limit
	for i := 0; i < 2; i++ {
		limiter.Allow(ctx, "key-a")
	}

	// Key B should still have full limit
	result, _ := limiter.Allow(ctx, "key-b")
	if !result.Allowed {
		t.Fatal("key-b should be allowed")
	}
	if result.Remaining != 1 {
		t.Errorf("expected remaining 1, got %d", result.Remaining)
	}
}

func TestRateLimiter_AllowN(t *testing.T) {
	limiter, cleanup := setupTestRateLimiter(t, 10, time.Minute)
	defer cleanup()

	ctx := context.Background()

	// Request 5 at once
	result, err := limiter.AllowN(ctx, "test-key", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Allowed {
		t.Fatal("should be allowed")
	}
	if result.Remaining != 5 {
		t.Errorf("expected remaining 5, got %d", result.Remaining)
	}

	// Request 6 more should fail
	result, _ = limiter.AllowN(ctx, "test-key", 6)
	if result.Allowed {
		t.Fatal("should be blocked")
	}
}
