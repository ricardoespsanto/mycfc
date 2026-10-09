//go:build integration

package app

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"testing"
)

func TestStartupChecksFinalDatedSchema(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("TEST_DATABASE_URL required")
	}
	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pingApplicationPool(t.Context(), pool); err != nil {
		t.Fatalf("final baseline rejected: %v", err)
	}
}
