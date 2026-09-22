//go:build integration

package main

import (
	"bytes"
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestRunUsesDedicatedLoginAndBoundedCapability(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	admin, err := pgx.Connect(t.Context(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })

	const loginRole = "mycfc_data_retention_test_login"
	const loginPassword = "data-retention-test-password"
	role := pgx.Identifier{loginRole}.Sanitize()
	if _, err = admin.Exec(t.Context(), `GRANT USAGE ON SCHEMA public TO mycfc_data_retention;
	GRANT EXECUTE ON FUNCTION data_retention_run(uuid,integer), data_retention_status() TO mycfc_data_retention;
	DO $$BEGIN
		IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='mycfc_data_retention_test_login') THEN
			CREATE ROLE mycfc_data_retention_test_login LOGIN NOINHERIT PASSWORD 'data-retention-test-password';
		ELSE
			ALTER ROLE mycfc_data_retention_test_login LOGIN NOINHERIT PASSWORD 'data-retention-test-password';
		END IF;
	END$$;
	GRANT mycfc_data_retention TO mycfc_data_retention_test_login;`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename=$1`, loginRole)
		_, _ = admin.Exec(context.Background(), `REVOKE mycfc_data_retention FROM `+role)
		_, _ = admin.Exec(context.Background(), `DROP ROLE IF EXISTS `+role)
	})

	var tableAccess, runAccess, statusAccess bool
	if err = admin.QueryRow(t.Context(), `SELECT
		has_table_privilege('mycfc_data_retention','users','SELECT'),
		has_function_privilege('mycfc_data_retention','data_retention_run(uuid,integer)','EXECUTE'),
		has_function_privilege('mycfc_data_retention','data_retention_status()','EXECUTE')`).Scan(&tableAccess, &runAccess, &statusAccess); err != nil {
		t.Fatal(err)
	}
	if tableAccess || !runAccess || !statusAccess {
		t.Fatalf("unexpected retention capability table=%t run=%t status=%t", tableAccess, runAccess, statusAccess)
	}

	loginURL, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	loginURL.User = url.UserPassword(loginRole, loginPassword)
	env := map[string]string{
		"DATA_RETENTION_ENABLED":      "true",
		"DATA_RETENTION_DATABASE_URL": loginURL.String(),
	}
	var output bytes.Buffer
	if err = run(t.Context(), []string{"run", "--confirmed-manual-review"}, func(name string) string { return env[name] }, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(output.String(), "data_retention_succeeded ") || strings.Contains(output.String(), loginRole) {
		t.Fatalf("unsafe or missing aggregate evidence %q", output.String())
	}

	login, err := pgx.Connect(t.Context(), loginURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer login.Close(context.Background())
	if _, err = login.Exec(t.Context(), `SELECT count(*) FROM users`); err == nil {
		t.Fatal("dedicated login read application tables without activating its bounded capability")
	}
	if _, err = login.Exec(t.Context(), `SET ROLE mycfc_data_retention`); err != nil {
		t.Fatal(err)
	}
	if _, err = login.Exec(t.Context(), `SELECT count(*) FROM users`); err == nil {
		t.Fatal("retention capability read application tables")
	}
}
