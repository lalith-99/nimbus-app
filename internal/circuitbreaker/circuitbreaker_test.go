package circuitbreaker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/lalithlochan/nimbus/internal/db"
)

func testLogger() *zap.Logger {
	logger, _ := zap.NewDevelopment()
	return logger
}

func TestCircuitBreaker_StartsInClosedState(t *testing.T) {
	cb := New(DefaultConfig("test"), testLogger())
	if cb.GetState() != StateClosed {
		t.Fatalf("expected StateClosed, got %s", cb.GetState())
	}
}

func TestCircuitBreaker_AllowsRequestsWhenClosed(t *testing.T) {
	cb := New(DefaultConfig("test"), testLogger())
	for i := 0; i < 10; i++ {
		if !cb.Allow() {
			t.Fatalf("request %d should be allowed", i)
		}
	}
}

func TestCircuitBreaker_OpensAfterMaxFailures(t *testing.T) {
	cb := New(Config{Name: "test", MaxFailures: 3, RecoveryTimeout: 1 * time.Second}, testLogger())
	for i := 0; i < 3; i++ {
		cb.Allow()
		cb.RecordFailure()
	}
	if cb.GetState() != StateOpen {
		t.Fatalf("expected StateOpen, got %s", cb.GetState())
	}
}

func TestCircuitBreaker_RejectsWhenOpen(t *testing.T) {
	cb := New(Config{Name: "test", MaxFailures: 2, RecoveryTimeout: 5 * time.Second}, testLogger())
	cb.Allow()
	cb.RecordFailure()
	cb.Allow()
	cb.RecordFailure()
	if cb.Allow() {
		t.Fatal("should reject when open")
	}
}

func TestCircuitBreaker_HalfOpenAfterTimeout(t *testing.T) {
	cb := New(Config{Name: "test", MaxFailures: 2, RecoveryTimeout: 50 * time.Millisecond}, testLogger())
	cb.Allow()
	cb.RecordFailure()
	cb.Allow()
	cb.RecordFailure()
	time.Sleep(60 * time.Millisecond)
	if !cb.Allow() {
		t.Fatal("should allow probe after timeout")
	}
	if cb.GetState() != StateHalfOpen {
		t.Fatalf("expected StateHalfOpen, got %s", cb.GetState())
	}
}

func TestCircuitBreaker_ClosesOnSuccessfulProbe(t *testing.T) {
	cb := New(Config{Name: "test", MaxFailures: 2, RecoveryTimeout: 50 * time.Millisecond}, testLogger())
	cb.Allow()
	cb.RecordFailure()
	cb.Allow()
	cb.RecordFailure()
	time.Sleep(60 * time.Millisecond)
	cb.Allow()
	cb.RecordSuccess()
	if cb.GetState() != StateClosed {
		t.Fatalf("expected StateClosed, got %s", cb.GetState())
	}
}

func TestCircuitBreaker_ReopensOnFailedProbe(t *testing.T) {
	cb := New(Config{Name: "test", MaxFailures: 2, RecoveryTimeout: 50 * time.Millisecond}, testLogger())
	cb.Allow()
	cb.RecordFailure()
	cb.Allow()
	cb.RecordFailure()
	time.Sleep(60 * time.Millisecond)
	cb.Allow()
	cb.RecordFailure()
	if cb.GetState() != StateOpen {
		t.Fatalf("expected StateOpen, got %s", cb.GetState())
	}
}

func TestCircuitBreaker_SuccessResetsFailureCount(t *testing.T) {
	cb := New(Config{Name: "test", MaxFailures: 3}, testLogger())
	cb.Allow()
	cb.RecordFailure()
	cb.Allow()
	cb.RecordFailure()
	cb.Allow()
	cb.RecordSuccess()
	cb.Allow()
	cb.RecordFailure()
	cb.Allow()
	cb.RecordFailure()
	if cb.GetState() != StateClosed {
		t.Fatal("success should have reset failure count")
	}
}

func TestCircuitBreaker_HalfOpenLimitsRequests(t *testing.T) {
	cb := New(Config{Name: "test", MaxFailures: 2, RecoveryTimeout: 50 * time.Millisecond, HalfOpenMaxRequests: 1}, testLogger())
	cb.Allow()
	cb.RecordFailure()
	cb.Allow()
	cb.RecordFailure()
	time.Sleep(60 * time.Millisecond)
	if !cb.Allow() {
		t.Fatal("first half-open request should be allowed")
	}
	if cb.Allow() {
		t.Fatal("second half-open request should be rejected")
	}
}

func TestCircuitBreaker_Reset(t *testing.T) {
	cb := New(Config{Name: "test", MaxFailures: 2, RecoveryTimeout: 5 * time.Second}, testLogger())
	cb.Allow()
	cb.RecordFailure()
	cb.Allow()
	cb.RecordFailure()
	cb.Reset()
	if cb.GetState() != StateClosed {
		t.Fatalf("expected StateClosed after reset, got %s", cb.GetState())
	}
	if !cb.Allow() {
		t.Fatal("should allow after reset")
	}
}

func TestCircuitBreaker_Stats(t *testing.T) {
	cb := New(Config{Name: "stats-test", MaxFailures: 5, RecoveryTimeout: 5 * time.Second}, testLogger())
	cb.Allow()
	cb.RecordSuccess()
	cb.Allow()
	cb.RecordFailure()
	cb.Allow()
	cb.RecordSuccess()
	stats := cb.Stats()
	if stats.Name != "stats-test" {
		t.Fatalf("name = %s", stats.Name)
	}
	if stats.TotalRequests != 3 {
		t.Fatalf("total_requests = %d", stats.TotalRequests)
	}
	if stats.TotalSuccesses != 2 {
		t.Fatalf("total_successes = %d", stats.TotalSuccesses)
	}
	if stats.TotalFailures != 1 {
		t.Fatalf("total_failures = %d", stats.TotalFailures)
	}
}

func TestCircuitBreaker_DefaultConfig(t *testing.T) {
	cfg := DefaultConfig("svc")
	if cfg.MaxFailures != 5 {
		t.Fatalf("max_failures = %d", cfg.MaxFailures)
	}
	if cfg.RecoveryTimeout != 30*time.Second {
		t.Fatalf("recovery_timeout = %v", cfg.RecoveryTimeout)
	}
}

func TestStateString(t *testing.T) {
	tests := []struct {
		s    State
		want string
	}{
		{StateClosed, "closed"},
		{StateOpen, "open"},
		{StateHalfOpen, "half-open"},
		{State(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.s.String(); got != tt.want {
			t.Errorf("State(%d) = %s, want %s", tt.s, got, tt.want)
		}
	}
}

// --- ProtectedSender Tests ---

type mockSender struct {
	sendErr   error
	channel   string
	sendCalls int
}

func (m *mockSender) Send(ctx context.Context, notif *db.Notification) error {
	m.sendCalls++
	return m.sendErr
}

func (m *mockSender) SupportsChannel(channel string) bool {
	return channel == m.channel
}

func testNotif(ch string) *db.Notification {
	return &db.Notification{ID: uuid.New(), Channel: ch}
}

func TestProtectedSender_PassesThrough(t *testing.T) {
	mock := &mockSender{channel: "email"}
	cb := New(Config{Name: "test", MaxFailures: 5}, testLogger())
	ps := NewProtectedSender(mock, cb, testLogger())
	if err := ps.Send(context.Background(), testNotif("email")); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if mock.sendCalls != 1 {
		t.Fatalf("calls = %d", mock.sendCalls)
	}
}

func TestProtectedSender_FailFastWhenOpen(t *testing.T) {
	mock := &mockSender{sendErr: errors.New("down"), channel: "email"}
	cb := New(Config{Name: "test", MaxFailures: 2}, testLogger())
	ps := NewProtectedSender(mock, cb, testLogger())
	ps.Send(context.Background(), testNotif("email"))
	ps.Send(context.Background(), testNotif("email"))
	mock.sendCalls = 0
	err := ps.Send(context.Background(), testNotif("email"))
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen, got: %v", err)
	}
	if mock.sendCalls != 0 {
		t.Fatalf("sender called %d times when circuit open", mock.sendCalls)
	}
}

func TestProtectedSender_RecordsMetrics(t *testing.T) {
	mock := &mockSender{channel: "sms"}
	cb := New(Config{Name: "test", MaxFailures: 5}, testLogger())
	ps := NewProtectedSender(mock, cb, testLogger())
	ps.Send(context.Background(), testNotif("sms"))
	if cb.Stats().TotalSuccesses != 1 {
		t.Fatal("expected 1 success")
	}
	mock.sendErr = errors.New("fail")
	ps.Send(context.Background(), testNotif("sms"))
	if cb.Stats().TotalFailures != 1 {
		t.Fatal("expected 1 failure")
	}
}

func TestProtectedSender_SupportsChannel(t *testing.T) {
	mock := &mockSender{channel: "webhook"}
	ps := NewProtectedSender(mock, New(DefaultConfig("t"), testLogger()), testLogger())
	if !ps.SupportsChannel("webhook") {
		t.Fatal("should support webhook")
	}
	if ps.SupportsChannel("email") {
		t.Fatal("should not support email")
	}
}

func TestProtectedSender_FullLifecycle(t *testing.T) {
	mock := &mockSender{channel: "email"}
	cb := New(Config{Name: "lifecycle", MaxFailures: 3, RecoveryTimeout: 50 * time.Millisecond}, testLogger())
	ps := NewProtectedSender(mock, cb, testLogger())
	n := testNotif("email")

	// Phase 1: working
	if err := ps.Send(context.Background(), n); err != nil {
		t.Fatalf("phase1: %v", err)
	}

	// Phase 2: service fails, circuit opens
	mock.sendErr = errors.New("SES down")
	for i := 0; i < 3; i++ {
		ps.Send(context.Background(), n)
	}
	if cb.GetState() != StateOpen {
		t.Fatalf("phase2: expected open, got %s", cb.GetState())
	}

	// Phase 3: fail fast
	mock.sendCalls = 0
	err := ps.Send(context.Background(), n)
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("phase3: %v", err)
	}
	if mock.sendCalls != 0 {
		t.Fatal("phase3: sender should not be called")
	}

	// Phase 4: wait for recovery
	time.Sleep(60 * time.Millisecond)

	// Phase 5: service recovers
	mock.sendErr = nil
	if err := ps.Send(context.Background(), n); err != nil {
		t.Fatalf("phase5: %v", err)
	}
	if cb.GetState() != StateClosed {
		t.Fatalf("phase5: expected closed, got %s", cb.GetState())
	}

	// Phase 6: normal traffic
	for i := 0; i < 5; i++ {
		if err := ps.Send(context.Background(), n); err != nil {
			t.Fatalf("phase6[%d]: %v", i, err)
		}
	}
}
