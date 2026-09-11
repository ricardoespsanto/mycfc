package guardianauthority

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
)

type workerStore struct {
	calls int
	err   error
}

func (s *workerStore) ReconcileGuardianAuthorityCutoffs(context.Context) (int64, error) {
	s.calls++
	return 1, s.err
}

func TestWorkerReconcilesImmediatelyAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := &workerStore{}
	(Worker{Store: store}).Run(ctx)
	if store.calls != 1 {
		t.Fatalf("calls = %d, want 1", store.calls)
	}
}

func TestWorkerTreatsReconciliationFailureAsRetryable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := &workerStore{err: errors.New("database unavailable")}
	(Worker{Store: store}).Run(ctx)
	if store.calls != 1 {
		t.Fatalf("calls = %d, want 1", store.calls)
	}
}

func TestWorkerUsesInjectedLogger(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if got := (Worker{Logger: logger}).logger(); got != logger {
		t.Fatal("injected logger was not returned")
	}
}
