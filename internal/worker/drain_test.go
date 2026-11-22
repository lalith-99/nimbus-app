package worker

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"

	"github.com/lalithlochan/nimbus/internal/db"
)

type drainRepository struct {
	MockRepository
	failDeadLetter bool
}

func (r *drainRepository) MoveToDeadLetter(ctx context.Context, n *db.Notification, reason string) (*db.DeadLetterNotification, error) {
	if r.failDeadLetter {
		return nil, errors.New("write failed")
	}
	return r.MockRepository.MoveToDeadLetter(ctx, n, reason)
}

func TestBatchDrainDecision(t *testing.T) {
	for _, c := range []struct {
		name          string
		size          int
		claimFails    bool
		persistFails  bool
		cancel        bool
		wantMore      bool
		wantSends     int
	}{
		{"full", 3, false, false, false, true, 3},
		{"short", 2, false, false, false, false, 2},
		{"empty", 0, false, false, false, false, 0},
		{"claim error", 3, true, false, false, false, 0},
		{"outcome error", 3, false, true, false, false, 3},
		{"shutdown", 3, false, false, true, false, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			repo := &drainRepository{failDeadLetter: c.persistFails}
			repo.shouldFail = c.claimFails
			for i := 0; i < c.size; i++ {
				repo.notifications = append(repo.notifications, &db.Notification{})
			}
			sender := &MockSender{shouldFail: c.persistFails}
			w := New(repo, sender, Config{BatchSize: 3, MaxRetries: 1}, zap.NewNop())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if c.cancel {
				cancel()
			}
			if more := w.processBatch(ctx); more != c.wantMore || sender.sendCalls != c.wantSends {
				t.Fatalf("more=%v sends=%d, want %v and %d", more, sender.sendCalls, c.wantMore, c.wantSends)
			}
		})
	}
}
