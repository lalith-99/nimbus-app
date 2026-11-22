package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
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

// The marker and cached result keep their original wire formats. A separate
// expiring ownership key distinguishes consecutive reservations of the same key.
func (s *IdempotencyService) keys(tenantID, idempotencyKey string) []string {
	key := s.buildKey(tenantID, idempotencyKey)
	return []string{key, "owner:" + key}
}

var reserveScript = redis.NewScript(`
	if redis.call("SET", KEYS[1], ARGV[1], "PX", ARGV[3], "NX") then
		redis.call("SET", KEYS[2], ARGV[2], "PX", ARGV[3])
		return 1
	end
	return 0
`)

// CheckOrReserve returns a cached result, a conflict, or a reservation owned
// by the supplied request ID. Only that reservation may publish or release.
func (s *IdempotencyService) CheckOrReserve(ctx context.Context, tenantID, idempotencyKey string, owner uuid.UUID) (*IdempotencyResult, error) {
	result, err := s.Check(ctx, tenantID, idempotencyKey)
	if err != nil || result != nil {
		return result, err
	}
	acquired, err := reserveScript.Run(ctx, s.client.rdb, s.keys(tenantID, idempotencyKey),
		processingMarker, owner.String(), processingTTL.Milliseconds()).Int()
	if err != nil {
		return nil, fmt.Errorf("reserve idempotency key: %w", err)
	}
	if acquired == 0 {
		return nil, ErrDuplicateRequest
	}
	return nil, nil
}

// finishScript changes a reservation only while both the marker and owner match.
var finishScript = redis.NewScript(`
	if redis.call("GET", KEYS[1]) ~= ARGV[1] or redis.call("GET", KEYS[2]) ~= ARGV[2] then
		return 0
	end
	if #ARGV == 2 then
		redis.call("DEL", KEYS[1])
	else
		redis.call("SET", KEYS[1], ARGV[3], "PX", ARGV[4])
	end
	redis.call("DEL", KEYS[2])
	return 1
`)

var ErrReservationLost = errors.New("request no longer owns idempotency reservation")

func (s *IdempotencyService) Store(ctx context.Context, tenantID, idempotencyKey string, owner uuid.UUID, result *IdempotencyResult, ttl time.Duration) error {
	if result.CreatedAt == 0 {
		result.CreatedAt = time.Now().Unix()
	}
	data, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal idempotency result: %w", err)
	}
	stored, err := finishScript.Run(ctx, s.client.rdb, s.keys(tenantID, idempotencyKey),
		processingMarker, owner.String(), data, ttl.Milliseconds()).Int()
	if err != nil {
		return fmt.Errorf("store idempotency result: %w", err)
	}
	if stored == 0 {
		return ErrReservationLost
	}
	return nil
}

// Release is a no-op when the reservation has expired or another request owns it.
func (s *IdempotencyService) Release(ctx context.Context, tenantID, idempotencyKey string, owner uuid.UUID) error {
	return finishScript.Run(ctx, s.client.rdb, s.keys(tenantID, idempotencyKey), processingMarker, owner.String()).Err()
}
