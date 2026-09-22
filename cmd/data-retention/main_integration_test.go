//go:build integration

package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestManualRunFailsClosedWhenCapabilityIsRevoked(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	admin, err := pgx.Connect(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	if err = activateCapabilityRole("mycfc_absent_retention_role")(t.Context(), admin); err == nil {
		t.Fatal("missing capability role accepted")
	}
	if _, err = admin.Exec(t.Context(), `GRANT EXECUTE ON FUNCTION data_retention_run(uuid,integer),
		data_retention_status() TO mycfc_data_retention`); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{"DATA_RETENTION_ENABLED": "true", "DATA_RETENTION_DATABASE_URL": dsn}
	getenv := func(key string) string { return values[key] }
	args := []string{"run", "--confirmed-manual-review"}
	for _, tc := range []struct{ name, signature string }{
		{"run", "data_retention_run(uuid,integer)"},
		{"status", "data_retention_status()"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := admin.Exec(t.Context(), "REVOKE EXECUTE ON FUNCTION "+tc.signature+" FROM mycfc_data_retention"); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := admin.Exec(context.Background(), "GRANT EXECUTE ON FUNCTION "+tc.signature+" TO mycfc_data_retention"); err != nil {
					t.Error(err)
				}
			})
			if err := run(t.Context(), args, getenv, io.Discard); err == nil {
				t.Fatal("revoked capability accepted")
			}
		})
	}
}

func TestManualRunUsesNarrowRetentionRole(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	admin, err := pgx.Connect(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(t.Context())
	// Earlier migration tests replace these routines; restore the fixed test grant.
	if _, err = admin.Exec(t.Context(), `GRANT EXECUTE ON FUNCTION data_retention_run(uuid,integer),
		data_retention_status() TO mycfc_data_retention`); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{
		"DATA_RETENTION_ENABLED":      "true",
		"DATA_RETENTION_DATABASE_URL": dsn,
	}
	var output bytes.Buffer
	if err = run(t.Context(), []string{"run", "--confirmed-manual-review"}, func(key string) string { return values[key] }, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "data_retention_succeeded") || !strings.Contains(output.String(), "due_count=") {
		t.Fatalf("manual retention result missing: %s", output.String())
	}
}
