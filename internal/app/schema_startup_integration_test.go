//go:build integration

package app

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"net/url"
	"os"
	"testing"
)

func TestStartupChecksFinalDatedSchema(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("TEST_DATABASE_URL required")
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	user, password := os.Getenv("APP_DB_USER"), os.Getenv("APP_DB_PASSWORD")
	if user == "" || password == "" {
		t.Fatal("dedicated runtime credentials required")
	}
	parsed.User = url.UserPassword(user, password)
	pool, err := pgxpool.New(t.Context(), parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pingApplicationPool(t.Context(), pool); err != nil {
		t.Fatalf("final baseline rejected: %v", err)
	}
}
