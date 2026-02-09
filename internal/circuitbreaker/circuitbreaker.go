package circuitbreaker

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

// State represents the current state of the circuit breaker.
//
// State transitions:
//
//	Closed -> Open:      When failure count >= threshold
//	Open -> HalfOpen:    After recovery timeout expires
//	HalfOpen -> Closed:  When a probe request succeeds
//	HalfOpen -> Open:    When a probe request fails
type State int

const (
	StateClosed   State = iota // Normal operation - requests pass through
	StateOpen                  // Circuit tripped - requests fail fast
	StateHalfOpen              // Recovery probe - allow one request to test
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// ErrCircuitOpen is returned when the circuit breaker is open and
// requests are being rejected to protect the downstream service.
var ErrCircuitOpen = errors.New("circuit breaker is open")

// Config holds the configuration for a CircuitBreaker.
type Config struct {
	// Name identifies this circuit breaker (e.g., "ses", "sns", "webhook").
	Name string

	// MaxFailures is the number of consecutive failures before the circuit opens.
	// Industry standard: 5 (Netflix Hystrix default)
	MaxFailures int

	// RecoveryTimeout is how long to wait in Open state before probing.
	// Industry standard: 30-60 seconds
	RecoveryTimeout time.Duration

	// HalfOpenMaxRequests is the max requests allowed in half-open state.
	// Typically 1 - send a single probe request to test recovery.
	HalfOpenMaxRequests int
}

// DefaultConfig returns sensible defaults matching Netflix/Uber patterns.
func DefaultConfig(name string) Config {
	return Config{
		Name:                name,
		MaxFailures:         5,
		RecoveryTimeout:     30 * time.Second,
		HalfOpenMaxRequests: 1,
	}
}

// CircuitBreaker implements the circuit breaker pattern to protect
// downstream services (SES, SNS, webhooks) from cascade failures.
//
// When a service starts failing, the circuit "opens" and immediately
// rejects requests instead of wasting time/resources on a dead service.
// After a recovery timeout, it allows one probe request through.
// If the probe succeeds, the circuit closes and normal traffic resumes.
//
// Used by: Netflix (Hystrix), Uber (circuit breakers per service),
// AWS SDK (built-in retry with circuit breaker), Stripe (per-endpoint).
type CircuitBreaker struct {
	mu     sync.RWMutex
	config Config
	logger *zap.Logger

	state            State
	failureCount     int
	successCount     int
	lastFailureTime  time.Time
	lastStateChange  time.Time
	halfOpenRequests int

	// Metrics
	totalRequests  int64
	totalFailures  int64
	totalSuccesses int64
	totalRejected  int64
}

// New creates a new CircuitBreaker with the given configuration.
func New(cfg Config, logger *zap.Logger) *CircuitBreaker {
	if cfg.MaxFailures <= 0 {
		cfg.MaxFailures = 5
	}
	if cfg.RecoveryTimeout <= 0 {
		cfg.RecoveryTimeout = 30 * time.Second
	}
	if cfg.HalfOpenMaxRequests <= 0 {
		cfg.HalfOpenMaxRequests = 1
	}

	cb := &CircuitBreaker{
		config:          cfg,
		logger:          logger,
		state:           StateClosed,
		lastStateChange: time.Now(),
	}

	logger.Info("circuit breaker created",
		zap.String("name", cfg.Name),
		zap.Int("max_failures", cfg.MaxFailures),
		zap.Duration("recovery_timeout", cfg.RecoveryTimeout),
	)

	return cb
}

// Allow checks if a request should be allowed through the circuit breaker.
// Returns true if the request can proceed, false if it should be rejected.
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.totalRequests++

	switch cb.state {
	case StateClosed:
		return true

	case StateOpen:
		// Check if recovery timeout has elapsed
		if time.Since(cb.lastFailureTime) >= cb.config.RecoveryTimeout {
			cb.transitionTo(StateHalfOpen)
			cb.halfOpenRequests = 1
			cb.logger.Info("circuit breaker allowing probe request",
				zap.String("name", cb.config.Name),
			)
			return true
		}
		cb.totalRejected++
		return false

	case StateHalfOpen:
		// Only allow limited requests in half-open state
		if cb.halfOpenRequests < cb.config.HalfOpenMaxRequests {
			cb.halfOpenRequests++
			return true
		}
		cb.totalRejected++
		return false

	default:
		return false
	}
}

// RecordSuccess records a successful request.
// In HalfOpen state, this closes the circuit (service recovered).
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.totalSuccesses++
	cb.successCount++
	cb.failureCount = 0

	if cb.state == StateHalfOpen {
		cb.transitionTo(StateClosed)
		cb.logger.Info("circuit breaker closed - service recovered",
			zap.String("name", cb.config.Name),
		)
	}
}

// RecordFailure records a failed request.
// In Closed state, opens the circuit after MaxFailures consecutive failures.
// In HalfOpen state, immediately re-opens the circuit.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.totalFailures++
	cb.failureCount++
	cb.lastFailureTime = time.Now()

	switch cb.state {
	case StateClosed:
		if cb.failureCount >= cb.config.MaxFailures {
			cb.transitionTo(StateOpen)
			cb.logger.Warn("circuit breaker OPENED - too many failures",
				zap.String("name", cb.config.Name),
				zap.Int("failures", cb.failureCount),
				zap.Int("threshold", cb.config.MaxFailures),
			)
		}

	case StateHalfOpen:
		// Probe failed - service still down, reopen circuit
		cb.transitionTo(StateOpen)
		cb.logger.Warn("circuit breaker re-opened - probe failed",
			zap.String("name", cb.config.Name),
		)
	}
}

// State returns the current state of the circuit breaker.
func (cb *CircuitBreaker) GetState() State {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// Stats returns current metrics for monitoring/dashboards.
type Stats struct {
	Name            string `json:"name"`
	State           string `json:"state"`
	FailureCount    int    `json:"failure_count"`
	TotalRequests   int64  `json:"total_requests"`
	TotalFailures   int64  `json:"total_failures"`
	TotalSuccesses  int64  `json:"total_successes"`
	TotalRejected   int64  `json:"total_rejected"`
	LastFailure     string `json:"last_failure,omitempty"`
	LastStateChange string `json:"last_state_change"`
}

// Stats returns current circuit breaker statistics.
func (cb *CircuitBreaker) Stats() Stats {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	s := Stats{
		Name:            cb.config.Name,
		State:           cb.state.String(),
		FailureCount:    cb.failureCount,
		TotalRequests:   cb.totalRequests,
		TotalFailures:   cb.totalFailures,
		TotalSuccesses:  cb.totalSuccesses,
		TotalRejected:   cb.totalRejected,
		LastStateChange: cb.lastStateChange.Format(time.RFC3339),
	}

	if !cb.lastFailureTime.IsZero() {
		s.LastFailure = cb.lastFailureTime.Format(time.RFC3339)
	}

	return s
}

// Reset manually resets the circuit breaker to Closed state.
// Useful for admin/operator override.
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.transitionTo(StateClosed)
	cb.failureCount = 0
	cb.successCount = 0
	cb.halfOpenRequests = 0

	cb.logger.Info("circuit breaker manually reset",
		zap.String("name", cb.config.Name),
	)
}

// transitionTo changes state (must be called with lock held).
func (cb *CircuitBreaker) transitionTo(newState State) {
	if cb.state == newState {
		return
	}

	oldState := cb.state
	cb.state = newState
	cb.lastStateChange = time.Now()
	cb.halfOpenRequests = 0

	cb.logger.Debug("circuit breaker state transition",
		zap.String("name", cb.config.Name),
		zap.String("from", oldState.String()),
		zap.String("to", newState.String()),
	)
}

// String returns a human-readable representation.
func (cb *CircuitBreaker) String() string {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return fmt.Sprintf("CircuitBreaker[%s] state=%s failures=%d/%d",
		cb.config.Name, cb.state, cb.failureCount, cb.config.MaxFailures)
}
