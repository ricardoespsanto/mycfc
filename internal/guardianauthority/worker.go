package guardianauthority

import (
	"context"
	"log/slog"
	"time"
)

const ReconcileInterval = time.Minute

type Store interface {
	ReconcileGuardianAuthorityCutoffs(context.Context) (int64, error)
}

// Worker materializes time-based authority cutoffs so credentials and indexed
// sessions are removed even when nobody opens the relationship or tries to log in.
type Worker struct {
	Store  Store
	Logger *slog.Logger
}

func (w Worker) Run(ctx context.Context) {
	w.reconcile(ctx)
	ticker := time.NewTicker(ReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.reconcile(ctx)
		}
	}
}

func (w Worker) reconcile(ctx context.Context) {
	count, err := w.Store.ReconcileGuardianAuthorityCutoffs(ctx)
	if err != nil {
		w.logger().Error("guardian authority cutoff reconciliation failed", "error_class", "database")
		return
	}
	if count > 0 {
		w.logger().Info("guardian authority cutoffs reconciled", "relationship_count", count)
	}
}

func (w Worker) logger() *slog.Logger {
	if w.Logger != nil {
		return w.Logger
	}
	return slog.Default()
}
