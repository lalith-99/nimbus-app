package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

const (
	// IdempotencyTTL is how long idempotency keys are retained.
	// Industry standard: 5 minutes for auto-generated (content-based) keys
	// to catch network retries without blocking intentional re-sends.
	// Client-provided keys use a longer TTL (24h) since the client
	// explicitly controls uniqueness — matching Stripe's approach.
	IdempotencyTTL      = 5 * time.Minute // Auto-generated keys (retry protection)
	IdempotencyTTLExact = 24 * time.Hour  // Client-provided keys (explicit dedup)

	// processingTTL is the lock duration while a request is being processed.
	processingTTL = 5 * time.Minute

	processingMarker = "processing"
)

// ErrDuplicateRequest indicates an idempotency key collision.
var ErrDuplicateRequest = errors.New("duplicate request: idempotency key already exists")

// IdempotencyResult stores the cached response for an idempotent request.
type IdempotencyResult struct {
	NotificationID string `json:"notification_id"`
	StatusCode     int    `json:"status_code"`
	CreatedAt      int64  `json:"created_at"`
}

// IdempotencyService provides idempotency guarantees using Redis.
type IdempotencyService struct {
	client *Client
	logger *zap.Logger
}

// NewIdempotencyService creates a new idempotency service.
func NewIdempotencyService(client *Client, logger *zap.Logger) *IdempotencyService {
	return &IdempotencyService{
		client: client,
		logger: logger,
	}
}

func (s *IdempotencyService) buildKey(tenantID, idempotencyKey string) string {
	return fmt.Sprintf("idempotency:%s:%s", tenantID, idempotencyKey)
}

// Check retrieves a cached result for an idempotency key.
// Returns (nil, nil) if key doesn't exist, (result, nil) if found,
// or ErrDuplicateRequest if the key is currently being processed.
func (s *IdempotencyService) Check(ctx context.Context, tenantID, idempotencyKey string) (*IdempotencyResult, error) {
	key := s.buildKey(tenantID, idempotencyKey)

	val, err := s.client.rdb.Get(ctx, key).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("redis get failed: %w", err)
	}

	if val == processingMarker {
		return nil, ErrDuplicateRequest
	}

	var result IdempotencyResult
	if err := json.Unmarshal([]byte(val), &result); err != nil {
		s.logger.Error("failed to unmarshal idempotency result", zap.Error(err))
		return nil, fmt.Errorf("invalid cached result: %w", err)
	}

	s.logger.Debug("idempotency cache hit",
		zap.String("tenant_id", tenantID),
		zap.String("notification_id", result.NotificationID),
	)

	return &result, nil
}

// Store saves the result of a successfully processed request.
// ttl controls how long the key is cached:
//   - Auto-generated keys (content-based): 5 min — catches network retries
//   - Client-provided keys (Idempotency-Key header): 24h — explicit dedup control
func (s *IdempotencyService) Store(ctx context.Context, tenantID, idempotencyKey string, result *IdempotencyResult, ttl time.Duration) error {
	key := s.buildKey(tenantID, idempotencyKey)

	if result.CreatedAt == 0 {
		result.CreatedAt = time.Now().Unix()
	}

	data, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("failed to marshal result: %w", err)
	}

	if err := s.client.rdb.Set(ctx, key, data, ttl).Err(); err != nil {
		return fmt.Errorf("redis set failed: %w", err)
	}

	return nil
}

// Reserve acquires an idempotency lock using SET NX (atomic set-if-not-exists).
// Returns true if lock acquired, false if key already exists.
func (s *IdempotencyService) Reserve(ctx context.Context, tenantID, idempotencyKey string) (bool, error) {
	key := s.buildKey(tenantID, idempotencyKey)

	set, err := s.client.rdb.SetNX(ctx, key, processingMarker, processingTTL).Result()
	if err != nil {
		return false, fmt.Errorf("redis setnx failed: %w", err)
	}

	return set, nil
}

// CheckOrReserve atomically checks for an existing result or reserves the key.
// Returns cached result if found, nil if reserved successfully, or error.
func (s *IdempotencyService) CheckOrReserve(ctx context.Context, tenantID, idempotencyKey string) (*IdempotencyResult, error) {
	result, err := s.Check(ctx, tenantID, idempotencyKey)
	if err != nil {
		return nil, err
	}
	if result != nil {
		return result, nil
	}

	reserved, err := s.Reserve(ctx, tenantID, idempotencyKey)
	if err != nil {
		return nil, err
	}

	if !reserved {
		return nil, ErrDuplicateRequest
	}

	return nil, nil
}
