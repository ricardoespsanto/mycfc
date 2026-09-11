//go:build integration

package privacyrequests

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRetentionMaintenancePublicBoundary(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	worker := uuid.New()
	result, err := (RetentionMaintenance{Store: pool, Enabled: true, WorkerRef: worker, BatchLimit: 1}).Run(ctx)
	if err != nil || result.RunID == uuid.Nil {
		t.Fatalf("retention result=%+v err=%v", result, err)
	}
	for _, invalid := range []RetentionMaintenance{
		{}, {Store: pool, WorkerRef: worker, BatchLimit: 1}, {Store: pool, Enabled: true, BatchLimit: 1},
		{Store: pool, Enabled: true, WorkerRef: worker}, {Store: pool, Enabled: true, WorkerRef: worker, BatchLimit: 10001},
	} {
		if _, err = invalid.Run(ctx); !errors.Is(err, ErrRetentionUnavailable) {
			t.Fatalf("invalid retention boundary %+v err=%v", invalid, err)
		}
	}
}
