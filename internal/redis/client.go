// Package redis provides Redis client and services for caching and distributed operations.
package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// Config holds Redis connection settings.
type Config struct {
	Host     string
	Port     int
	Password string
	DB       int
}

// Client wraps the go-redis client with logging and connection management.
type Client struct {
	rdb    *redis.Client
	logger *zap.Logger
}

// New creates a new Redis client and verifies connectivity.
func New(ctx context.Context, cfg Config, logger *zap.Logger) (*Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:         fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Password:     cfg.Password,
		DB:           cfg.DB,
		PoolSize:     10,
		MinIdleConns: 2,
		PoolTimeout:  4 * time.Second,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
	})

	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping failed: %w", err)
	}

	logger.Info("redis connection established",
		zap.String("host", cfg.Host),
		zap.Int("port", cfg.Port),
	)

	return &Client{rdb: rdb, logger: logger}, nil
}

// Close gracefully closes the Redis connection.
func (c *Client) Close() error {
	return c.rdb.Close()
}

// Ping checks if Redis is responsive.
func (c *Client) Ping(ctx context.Context) error {
	return c.rdb.Ping(ctx).Err()
}
