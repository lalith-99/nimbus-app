package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// RateLimitConfig defines rate limiting parameters.
type RateLimitConfig struct {
	Limit  int           // Maximum requests allowed
	Window time.Duration // Time window for the limit
}

// RateLimitResult contains the result of a rate limit check.
type RateLimitResult struct {
	Allowed   bool
	Remaining int
	ResetAt   time.Time
}

// RateLimiter implements sliding window rate limiting using Redis.
type RateLimiter struct {
	client *Client
	logger *zap.Logger
	config RateLimitConfig
}

// NewRateLimiter creates a new rate limiter with the given configuration.
func NewRateLimiter(client *Client, logger *zap.Logger, config RateLimitConfig) *RateLimiter {
	return &RateLimiter{
		client: client,
		logger: logger,
		config: config,
	}
}

// Allow checks if a request is allowed under the rate limit.
// Uses sliding window algorithm with Redis sorted sets for accuracy.
func (r *RateLimiter) Allow(ctx context.Context, key string) (*RateLimitResult, error) {
	return r.AllowN(ctx, key, 1)
}

// AllowN checks if n requests are allowed under the rate limit.
func (r *RateLimiter) AllowN(ctx context.Context, key string, n int) (*RateLimitResult, error) {
	now := time.Now()
	windowStart := now.Add(-r.config.Window)
	resetAt := now.Add(r.config.Window)

	redisKey := fmt.Sprintf("ratelimit:%s", key)

	// Use Redis pipeline for atomic operations
	pipe := r.client.rdb.Pipeline()

	// Remove entries outside the window
	pipe.ZRemRangeByScore(ctx, redisKey, "0", fmt.Sprintf("%d", windowStart.UnixNano()))

	// Count current requests in window
	countCmd := pipe.ZCard(ctx, redisKey)

	// Execute pipeline
	_, err := pipe.Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("redis pipeline failed: %w", err)
	}

	currentCount := int(countCmd.Val())
	remaining := r.config.Limit - currentCount

	// Check if request would exceed limit
	if currentCount+n > r.config.Limit {
		r.logger.Debug("rate limit exceeded",
			zap.String("key", key),
			zap.Int("current", currentCount),
			zap.Int("limit", r.config.Limit),
		)
		return &RateLimitResult{
			Allowed:   false,
			Remaining: max(0, remaining),
			ResetAt:   resetAt,
		}, nil
	}

	// Add new request(s) to the window
	members := make([]interface{}, n*2)
	for i := 0; i < n; i++ {
		score := float64(now.UnixNano()) + float64(i)
		members[i*2] = score
		members[i*2+1] = fmt.Sprintf("%d-%d", now.UnixNano(), i)
	}

	pipe2 := r.client.rdb.Pipeline()
	for i := 0; i < n; i++ {
		score := float64(now.UnixNano()) + float64(i)
		member := fmt.Sprintf("%d-%d", now.UnixNano(), i)
		pipe2.ZAdd(ctx, redisKey, redis.Z{Score: score, Member: member})
	}
	pipe2.Expire(ctx, redisKey, r.config.Window+time.Second)

	if _, err := pipe2.Exec(ctx); err != nil {
		return nil, fmt.Errorf("redis zadd failed: %w", err)
	}

	return &RateLimitResult{
		Allowed:   true,
		Remaining: remaining - n,
		ResetAt:   resetAt,
	}, nil
}
