package privacyrequests

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type retentionErrorStore struct{ err error }

func (s retentionErrorStore) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, s.err
}
func (s retentionErrorStore) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, s.err
}
func (s retentionErrorStore) QueryRow(context.Context, string, ...any) pgx.Row {
	return retentionErrorRow(s)
}

type retentionErrorRow struct{ err error }

func (r retentionErrorRow) Scan(...any) error { return r.err }

func TestRetentionMaintenancePreservesDatabaseFailure(t *testing.T) {
	cause := errors.New("retention database unavailable")
	_, err := (RetentionMaintenance{Store: retentionErrorStore{err: cause}, Enabled: true, WorkerRef: uuid.New(), BatchLimit: 1}).Run(context.Background())
	if !errors.Is(err, cause) {
		t.Fatalf("retention error=%v", err)
	}
}
