package circuitbreaker

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/lalithlochan/nimbus/internal/db"
)

// Sender mirrors the worker.Sender interface to avoid circular imports.
type Sender interface {
	Send(ctx context.Context, notif *db.Notification) error
	SupportsChannel(channel string) bool
}

// ProtectedSender wraps any Sender with a CircuitBreaker.
// When the downstream service (SES, SNS, webhook endpoint) starts failing,
// the circuit opens and requests fail fast instead of piling up.
//
// This is the Decorator pattern — transparently adds resilience
// without modifying the underlying sender implementation.
type ProtectedSender struct {
	sender  Sender
	breaker *CircuitBreaker
	logger  *zap.Logger
}

// NewProtectedSender wraps a sender with circuit breaker protection.
func NewProtectedSender(sender Sender, breaker *CircuitBreaker, logger *zap.Logger) *ProtectedSender {
	return &ProtectedSender{
		sender:  sender,
		breaker: breaker,
		logger:  logger,
	}
}

// Send attempts to send a notification through the circuit breaker.
// If the circuit is open, returns ErrCircuitOpen immediately (fail fast).
// If the send succeeds, records success. If it fails, records failure.
func (p *ProtectedSender) Send(ctx context.Context, notif *db.Notification) error {
	if !p.breaker.Allow() {
		p.logger.Warn("circuit breaker rejected request — failing fast",
			zap.String("breaker", p.breaker.config.Name),
			zap.String("notification_id", notif.ID.String()),
			zap.String("channel", notif.Channel),
			zap.String("state", p.breaker.GetState().String()),
		)
		return fmt.Errorf("%w: %s sender unavailable", ErrCircuitOpen, p.breaker.config.Name)
	}

	err := p.sender.Send(ctx, notif)
	if err != nil {
		p.breaker.RecordFailure()
		p.logger.Debug("circuit breaker recorded failure",
			zap.String("breaker", p.breaker.config.Name),
			zap.Error(err),
		)
		return err
	}

	p.breaker.RecordSuccess()
	return nil
}

// SupportsChannel delegates to the underlying sender.
func (p *ProtectedSender) SupportsChannel(channel string) bool {
	return p.sender.SupportsChannel(channel)
}

// Breaker returns the underlying circuit breaker for metrics/monitoring.
func (p *ProtectedSender) Breaker() *CircuitBreaker {
	return p.breaker
}
