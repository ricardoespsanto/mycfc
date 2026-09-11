//go:build integration

package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestRunUsesRetentionCapabilityAndEmitsAggregateEvidence(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	admin, err := pgx.Connect(t.Context(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	roleIdentifier := pgx.Identifier{retentionRole}.Sanitize()
	var roleExists bool
	if err = admin.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)`, retentionRole).Scan(&roleExists); err != nil {
		t.Fatal(err)
	}
	if !roleExists {
		if _, err = admin.Exec(t.Context(), `CREATE ROLE `+roleIdentifier+` NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS`); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			cleanupCtx := context.Background()
			if _, cleanupErr := admin.Exec(cleanupCtx, `DROP OWNED BY `+roleIdentifier); cleanupErr != nil {
				t.Errorf("drop retention role privileges: %v", cleanupErr)
				return
			}
			if _, cleanupErr := admin.Exec(cleanupCtx, `DROP ROLE `+roleIdentifier); cleanupErr != nil {
				t.Errorf("drop retention role: %v", cleanupErr)
			}
		})
	}
	for _, routine := range []string{"privacy_retention_run(uuid,integer)", "privacy_retention_status()"} {
		var alreadyGranted bool
		if err = admin.QueryRow(t.Context(), `SELECT has_function_privilege($1,$2,'EXECUTE')`, retentionRole, routine).Scan(&alreadyGranted); err != nil {
			t.Fatal(err)
		}
		if alreadyGranted {
			continue
		}
		if _, err = admin.Exec(t.Context(), `GRANT EXECUTE ON FUNCTION `+routine+` TO `+retentionRole); err != nil {
			t.Fatal(err)
		}
		routine := routine
		t.Cleanup(func() {
			if _, cleanupErr := admin.Exec(context.Background(), `REVOKE EXECUTE ON FUNCTION `+routine+` FROM `+retentionRole); cleanupErr != nil {
				t.Errorf("restore retention routine ACL: %v", cleanupErr)
			}
		})
	}
	var publicUsageGranted bool
	if err = admin.QueryRow(t.Context(), `SELECT has_schema_privilege($1,'public','USAGE')`, retentionRole).Scan(&publicUsageGranted); err != nil {
		t.Fatal(err)
	}
	if !publicUsageGranted {
		if _, err = admin.Exec(t.Context(), `GRANT USAGE ON SCHEMA public TO `+roleIdentifier); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, cleanupErr := admin.Exec(context.Background(), `REVOKE USAGE ON SCHEMA public FROM `+roleIdentifier); cleanupErr != nil {
				t.Errorf("restore retention schema ACL: %v", cleanupErr)
			}
		})
	}
	env := map[string]string{
		"PRIVACY_RETENTION_ENABLED":      "true",
		"PRIVACY_RETENTION_DATABASE_URL": databaseURL,
		"PRIVACY_RETENTION_WORKER_REF":   uuid.NewString(),
		"PRIVACY_RETENTION_BATCH_LIMIT":  "1",
	}
	var output bytes.Buffer
	if err = run(t.Context(), func(name string) string { return env[name] }, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(output.String(), "privacy_retention_succeeded ") || strings.Contains(output.String(), env["PRIVACY_RETENTION_WORKER_REF"]) {
		t.Fatalf("unsafe or missing aggregate evidence %q", output.String())
	}
	originalURL := env["PRIVACY_RETENTION_DATABASE_URL"]
	env["PRIVACY_RETENTION_DATABASE_URL"] = "://invalid"
	if err = run(t.Context(), func(name string) string { return env[name] }, &bytes.Buffer{}); err == nil {
		t.Fatal("invalid retention database URL accepted")
	}
	env["PRIVACY_RETENTION_DATABASE_URL"] = originalURL
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err = run(canceled, func(name string) string { return env[name] }, &bytes.Buffer{}); err == nil {
		t.Fatal("canceled retention run accepted")
	}
	if _, err = admin.Exec(t.Context(), `REVOKE EXECUTE ON FUNCTION privacy_retention_run(uuid,integer) FROM `+retentionRole); err != nil {
		t.Fatal(err)
	}
	if err = run(t.Context(), func(name string) string { return env[name] }, &bytes.Buffer{}); err == nil {
		t.Fatal("missing retention execution capability accepted")
	}
	if _, err = admin.Exec(t.Context(), `GRANT EXECUTE ON FUNCTION privacy_retention_run(uuid,integer) TO `+retentionRole); err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(t.Context(), `REVOKE EXECUTE ON FUNCTION privacy_retention_status() FROM `+retentionRole); err != nil {
		t.Fatal(err)
	}
	if err = run(t.Context(), func(name string) string { return env[name] }, &bytes.Buffer{}); err == nil {
		t.Fatal("missing retention status capability accepted")
	}
	if _, err = admin.Exec(t.Context(), `GRANT EXECUTE ON FUNCTION privacy_retention_status() TO `+retentionRole); err != nil {
		t.Fatal(err)
	}
}
