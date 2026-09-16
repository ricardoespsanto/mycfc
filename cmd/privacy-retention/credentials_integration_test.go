//go:build integration

package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestRetentionCredentialProvisionRotateRevokeBoundary(t *testing.T) {
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := t.Context()
	admin, err := pgx.Connect(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	var exists bool
	if err = admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)`, retentionLogin).Scan(&exists); err != nil || exists {
		t.Fatal("test login must be absent", err)
	}
	var capabilityExists bool
	if err = admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)`, retentionRole).Scan(&capabilityExists); err != nil {
		t.Fatal(err)
	}
	if !capabilityExists {
		if _, err = admin.Exec(ctx, `CREATE ROLE mycfc_privacy_retention NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = admin.Exec(ctx, `GRANT USAGE ON SCHEMA public TO mycfc_privacy_retention; GRANT EXECUTE ON FUNCTION privacy_retention_run(uuid,integer),privacy_retention_status() TO mycfc_privacy_retention`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup := context.Background()
		_, _ = admin.Exec(cleanup, `SELECT pg_terminate_backend(pid,5000) FROM pg_stat_activity WHERE usename='mycfc_privacy_retention_login'`)
		_, _ = admin.Exec(cleanup, `DROP OWNED BY mycfc_privacy_retention_login`)
		_, _ = admin.Exec(cleanup, `DROP ROLE IF EXISTS mycfc_privacy_retention_login`)
		if !capabilityExists {
			_, _ = admin.Exec(cleanup, `DROP OWNED BY mycfc_privacy_retention; DROP ROLE mycfc_privacy_retention`)
		}
	})
	// Exercise protection even when the administrator session would normally
	// log all statements and bound parameters. The operator overrides them before
	// submitting password material and restores the session settings on commit.
	if _, err = admin.Exec(ctx, `SET log_statement='all'; SET log_parameter_max_length=-1; SET log_parameter_max_length_on_error=-1`); err != nil {
		t.Fatal(err)
	}
	inputs := credentialInputs{database: admin.Config().Database, password: strings.Repeat("initial-password-", 3)}
	if err = changeRetentionCredential(ctx, admin, "rotate", inputs); err == nil {
		t.Fatal("rotate created absent login")
	}
	if err = changeRetentionCredential(ctx, admin, "provision", inputs); err != nil {
		t.Fatal(err)
	}
	if err = changeRetentionCredential(ctx, admin, "provision", inputs); err == nil {
		t.Fatal("provision silently rotated existing login")
	}
	connect := func(password string) (*pgx.Conn, error) {
		cfg := admin.Config().Copy()
		cfg.User = retentionLogin
		cfg.Password = password
		return pgx.ConnectConfig(ctx, cfg)
	}
	login, err := connect(inputs.password)
	if err != nil {
		t.Fatal(err)
	}
	defer login.Close(context.Background())
	for _, sql := range []string{`SELECT * FROM users LIMIT 1`, `SET ROLE postgres`, `SELECT * FROM privacy_retention_status()`} {
		if _, err = login.Exec(ctx, sql); err == nil {
			t.Fatalf("login bypassed capability: %s", sql)
		}
	}
	if _, err = login.Exec(ctx, `SET ROLE mycfc_privacy_retention`); err != nil {
		t.Fatal(err)
	}
	if _, err = login.Exec(ctx, `SELECT * FROM privacy_retention_status()`); err != nil {
		t.Fatal(err)
	}
	if _, err = login.Exec(ctx, `SELECT * FROM users LIMIT 1`); err == nil {
		t.Fatal("capability has table access")
	}
	if _, err = admin.Exec(ctx, `GRANT SELECT ON users TO mycfc_privacy_retention_login; ALTER ROLE mycfc_privacy_retention_login VALID UNTIL '2000-01-01' CONNECTION LIMIT 0`); err != nil {
		t.Fatal(err)
	}
	oldPassword := inputs.password
	inputs.password = strings.Repeat("replacement-password-", 3)
	if err = changeRetentionCredential(ctx, admin, "rotate", inputs); err != nil {
		t.Fatal(err)
	}
	if err = login.Ping(ctx); err == nil {
		t.Fatal("rotation retained existing session")
	}
	if c, e := connect(oldPassword); e == nil {
		c.Close(ctx)
		t.Fatal("old password still accepted")
	}
	replacement, err := connect(inputs.password)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close(context.Background())
	if _, err = replacement.Exec(ctx, `SELECT * FROM users LIMIT 1`); err == nil {
		t.Fatal("rotation retained a drifted direct grant")
	}
	if err = changeRetentionCredential(ctx, admin, "revoke", credentialInputs{database: inputs.database}); err != nil {
		t.Fatal(err)
	}
	if err = replacement.Ping(ctx); err == nil {
		t.Fatal("revocation retained session")
	}
	if c, e := connect(inputs.password); e == nil {
		c.Close(ctx)
		t.Fatal("revoked login authenticated")
	}
	var canLogin, hasPassword, membership bool
	if err = admin.QueryRow(ctx, `SELECT rolcanlogin,rolpassword IS NOT NULL,EXISTS(SELECT 1 FROM pg_auth_members m WHERE m.member=pg_authid.oid) FROM pg_authid WHERE rolname=$1`, retentionLogin).Scan(&canLogin, &hasPassword, &membership); err != nil || canLogin || hasPassword || membership {
		t.Fatal("revocation left authority", err)
	}
}
