package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/lalithlochan/nimbus/internal/db"
)

type MockRepository struct {
	notifications []*db.Notification
	updateCalls   []updateCall
	shouldFail    bool
}

type updateCall struct {
	id       uuid.UUID
	status   string
	attempt  int
	errorMsg *string
}

func (m *MockRepository) ClaimPendingNotifications(ctx context.Context, limit int) ([]*db.Notification, error) {
	if m.shouldFail {
		return nil, errors.New("database error")
	}
	if len(m.notifications) > limit {
		return m.notifications[:limit], nil
	}
	return m.notifications, nil
}

func (m *MockRepository) UpdateNotificationStatus(ctx context.Context, id uuid.UUID, status string, attempt int, errorMsg *string, nextRetryAt *time.Time) error {
	if m.shouldFail {
		return errors.New("database error")
	}
	m.updateCalls = append(m.updateCalls, updateCall{id, status, attempt, errorMsg})
	return nil
}

func (m *MockRepository) MoveToDeadLetter(ctx context.Context, notif *db.Notification, lastError string) (*db.DeadLetterNotification, error) {
	if m.shouldFail {
		return nil, errors.New("database error")
	}
	m.updateCalls = append(m.updateCalls, updateCall{notif.ID, db.StatusDeadLettered, notif.Attempt + 1, &lastError})
	return &db.DeadLetterNotification{
		ID:                     uuid.New(),
		OriginalNotificationID: notif.ID,
		Status:                 db.DLQStatusPending,
	}, nil
}

type MockSender struct {
	shouldFail bool
	sendCalls  int
}

func (m *MockSender) Send(ctx context.Context, notif *db.Notification) error {
	m.sendCalls++
	if m.shouldFail {
		return errors.New("send failed")
	}
	return nil
}

func (m *MockSender) SupportsChannel(channel string) bool {
	return true // MockSender supports all channels
}

func TestWorker_ProcessNotification_Success(t *testing.T) {
	notifID := uuid.New()
	repo := &MockRepository{}
	sender := &MockSender{}
	logger := zap.NewNop()

	w := New(repo, sender, Config{MaxRetries: 3}, logger)

	notif := &db.Notification{
		ID:      notifID,
		Status:  "pending",
		Attempt: 0,
	}

	w.processNotification(context.Background(), notif)

	if sender.sendCalls != 1 {
		t.Errorf("expected 1 send call, got %d", sender.sendCalls)
	}

	// processNotification now makes exactly ONE status write — the terminal
	// status. The 'processing' mark is done atomically by ClaimPendingNotifications
	// (verified in repository-level tests), not here.
	if len(repo.updateCalls) != 1 {
		t.Fatalf("expected 1 update call, got %d", len(repo.updateCalls))
	}

	// The single call marks the notification as sent.
	if repo.updateCalls[0].status != "sent" {
		t.Errorf("expected status 'sent', got '%s'", repo.updateCalls[0].status)
	}
	if repo.updateCalls[0].attempt != 1 {
		t.Errorf("expected attempt 1, got %d", repo.updateCalls[0].attempt)
	}
}

func TestWorker_ProcessNotification_FailWithRetry(t *testing.T) {
	notifID := uuid.New()
	repo := &MockRepository{}
	sender := &MockSender{shouldFail: true}
	logger := zap.NewNop()

	w := New(repo, sender, Config{MaxRetries: 3}, logger)

	notif := &db.Notification{
		ID:      notifID,
		Status:  "pending",
		Attempt: 0,
	}

	w.processNotification(context.Background(), notif)

	// One terminal update: back to 'pending' for retry.
	if len(repo.updateCalls) != 1 {
		t.Fatalf("expected 1 update call, got %d", len(repo.updateCalls))
	}

	if repo.updateCalls[0].status != "pending" {
		t.Errorf("expected status 'pending' for retry, got '%s'", repo.updateCalls[0].status)
	}
	if repo.updateCalls[0].errorMsg == nil {
		t.Error("expected error message to be set")
	}
}

func TestWorker_ProcessNotification_FailMaxRetries(t *testing.T) {
	notifID := uuid.New()
	repo := &MockRepository{}
	sender := &MockSender{shouldFail: true}
	logger := zap.NewNop()

	w := New(repo, sender, Config{MaxRetries: 3}, logger)

	notif := &db.Notification{
		ID:      notifID,
		Status:  "pending",
		Attempt: 2, // Already tried twice
	}

	w.processNotification(context.Background(), notif)

	// After the 'processing' mark moved into ClaimPendingNotifications, the only
	// update recorded here is the DLQ move (via MoveToDeadLetter) after max retries.
	if repo.updateCalls[0].status != db.StatusDeadLettered {
		t.Errorf("expected status '%s' after max retries, got '%s'", db.StatusDeadLettered, repo.updateCalls[0].status)
	}
	if repo.updateCalls[0].attempt != 3 {
		t.Errorf("expected attempt 3, got %d", repo.updateCalls[0].attempt)
	}
}

func TestWorker_ProcessBatch(t *testing.T) {
	notif1 := &db.Notification{ID: uuid.New(), Status: "pending", Attempt: 0}
	notif2 := &db.Notification{ID: uuid.New(), Status: "pending", Attempt: 0}

	repo := &MockRepository{
		notifications: []*db.Notification{notif1, notif2},
	}
	sender := &MockSender{}
	logger := zap.NewNop()

	w := New(repo, sender, Config{BatchSize: 10, MaxRetries: 3}, logger)
	w.processBatch(context.Background())

	if sender.sendCalls != 2 {
		t.Errorf("expected 2 send calls, got %d", sender.sendCalls)
	}
}

func TestWorker_ProcessBatch_EmptyQueue(t *testing.T) {
	repo := &MockRepository{notifications: []*db.Notification{}}
	sender := &MockSender{}
	logger := zap.NewNop()

	w := New(repo, sender, Config{}, logger)
	w.processBatch(context.Background())

	if sender.sendCalls != 0 {
		t.Errorf("expected 0 send calls for empty queue, got %d", sender.sendCalls)
	}
}

func TestWorker_ProcessBatch_DatabaseError(t *testing.T) {
	repo := &MockRepository{shouldFail: true}
	sender := &MockSender{}
	logger := zap.NewNop()

	w := New(repo, sender, Config{}, logger)
	w.processBatch(context.Background())

	if sender.sendCalls != 0 {
		t.Errorf("expected 0 send calls on db error, got %d", sender.sendCalls)
	}
}

func TestWorker_Start_GracefulShutdown(t *testing.T) {
	repo := &MockRepository{}
	sender := &MockSender{}
	logger := zap.NewNop()

	w := New(repo, sender, Config{PollInterval: 10 * time.Millisecond}, logger)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan bool)
	go func() {
		w.Start(ctx)
		done <- true
	}()

	// Let it run briefly
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// Success - worker stopped
	case <-time.After(1 * time.Second):
		t.Error("worker did not stop within timeout")
	}
}

func TestNew_Defaults(t *testing.T) {
	repo := &MockRepository{}
	sender := &MockSender{}
	logger := zap.NewNop()

	w := New(repo, sender, Config{}, logger)

	if w.config.PollInterval != 5*time.Second {
		t.Errorf("expected default PollInterval 5s, got %v", w.config.PollInterval)
	}
	if w.config.BatchSize != 5 {
		t.Errorf("expected default BatchSize 5, got %d", w.config.BatchSize)
	}
	if w.config.MaxRetries != 3 {
		t.Errorf("expected default MaxRetries 3, got %d", w.config.MaxRetries)
	}
}
