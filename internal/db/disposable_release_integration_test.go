//go:build integration

package db

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/cfcoimbra/mycfc/internal/releasecontract"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// This owns a separate cluster: the fixed production database/role bindings
// must never cause the test to reset the caller's integration database.
func TestDisposableReleaseAtomicReplacementAndRetiredRetry(t *testing.T) {
	ctx := context.Background()
	name := "mycfc-disposable-ci-" + uuid.NewString()
	cmd := exec.Command("docker", "run", "-d", "--name", name, "-p", "127.0.0.1::5432", "-e", "POSTGRES_PASSWORD=synthetic-ci-only", "postgres:16.9-alpine3.21")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("start owned PostgreSQL: %v %s", err, output)
	}
	t.Cleanup(func() {
		if output, err := exec.Command("docker", "rm", "-f", "-v", name).CombinedOutput(); err != nil {
			t.Errorf("remove owned PostgreSQL: %v %s", err, output)
		}
	})
	output, err := exec.Command("docker", "port", name, "5432/tcp").Output()
	if err != nil {
		t.Fatal(err)
	}
	address := strings.TrimSpace(string(output))
	dsn := "postgres://postgres:synthetic-ci-only@" + address + "/postgres?sslmode=disable"
	var admin *pgx.Conn
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		admin, err = pgx.Connect(ctx, dsn)
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	previousVersion, previousCandidate := releasecontract.Version, releasecontract.Candidate
	releasecontract.Version, releasecontract.Candidate = "v0.0.0-ci", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	t.Cleanup(func() { releasecontract.Version, releasecontract.Candidate = previousVersion, previousCandidate })
	credentials := RoleCredentials{AppUsername: releasecontract.OldWebRole, AppPassword: "synthetic-ci-only", MigrationUsername: "mycfc_migration", MigrationPassword: "synthetic-ci-only", MediaCleanupUsername: "mycfc_media_cleanup_login", MediaCleanupPassword: "synthetic-ci-only", DataRetentionUsername: "mycfc_data_retention_login", DataRetentionPassword: "synthetic-ci-only"}
	compressed, err := os.ReadFile("testdata/disposable-predecessor-v1.25.9.sql.gz")
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	predecessor, err := io.ReadAll(reader)
	reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%x", sha256.Sum256(predecessor)) != "8b660c86b52fa287a9d41b47ca703bc0b8c0789d86ec40dedd0fe1a1673bc4b3" {
		t.Fatal("tagged predecessor fixture changed")
	}
	setup := func(t *testing.T) *pgx.Conn {
		t.Helper()
		for _, sql := range []string{"DROP DATABASE IF EXISTS mycfc WITH (FORCE)", "DROP ROLE IF EXISTS " + releasecontract.WebRole, "CREATE DATABASE mycfc"} {
			if _, err := admin.Exec(ctx, sql); err != nil {
				t.Fatal(err)
			}
		}
		cfg, err := pgx.ParseConfig(dsn)
		if err != nil {
			t.Fatal(err)
		}
		cfg.Database = "mycfc"
		conn, err := pgx.ConnectConfig(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close(context.Background()) })
		if err := BootstrapRoles(ctx, conn, "mycfc", credentials); err != nil {
			t.Fatal(err)
		}
		for _, sql := range []string{"SET ROLE mycfc_migration", string(predecessor), "CREATE TABLE mycfc_meta.schema_migrations(version text PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now())"} {
			if _, err := conn.PgConn().Exec(ctx, sql).ReadAll(); err != nil {
				t.Fatal(err)
			}
		}
		entries, err := fs.Glob(migrationFiles, "migrations/*.sql")
		if err != nil {
			t.Fatal(err)
		}
		inventory := []string{}
		for _, path := range entries {
			v := migrationVersion(path)
			if v <= "202609170005_privacy_activation_dual_signer" {
				inventory = append(inventory, v)
			}
		}
		inventory = append(inventory, "reset-baseline-v1")
		for _, v := range inventory {
			if _, err := conn.Exec(ctx, "INSERT INTO mycfc_meta.schema_migrations(version) VALUES($1)", v); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := conn.Exec(ctx, `INSERT INTO users(name,email,password_hash,date_of_birth,email_verified_at) VALUES('Retained adult','retained@example.test','$2a$12$IQnXrEKbby1M4yt9/NQofOdWrlC7X9ogAGG0yJYfRknRdVdsugeRK','1990-01-01',now()); INSERT INTO user_platform_roles(user_id,role_id) SELECT u.id,r.id FROM users u CROSS JOIN platform_roles r WHERE r.code='ADMIN'; CREATE TABLE synthetic_history(value text); INSERT INTO synthetic_history VALUES('discarded'); RESET ROLE`); err != nil {
			t.Fatal(err)
		}
		return conn
	}
	t.Run("success and permanent retirement", func(t *testing.T) {
		conn := setup(t)
		var id, hash string
		if err := conn.QueryRow(ctx, "SELECT id::text,password_hash FROM users WHERE email='retained@example.test'").Scan(&id, &hash); err != nil {
			t.Fatal(err)
		}
		if err := BootstrapDisposableRelease(ctx, conn, "mycfc", credentials); err != nil {
			t.Fatal(err)
		}
		var keptID, keptHash string
		if err := conn.QueryRow(ctx, "SELECT id::text,password_hash FROM users WHERE email='retained@example.test'").Scan(&keptID, &keptHash); err != nil || id != keptID || hash != keptHash {
			t.Fatalf("administrator recovery: %v", err)
		}
		var gone, fenced, retired bool
		if err := conn.QueryRow(ctx, `SELECT to_regclass('public.synthetic_history') IS NULL,(SELECT NOT rolcanlogin FROM pg_roles WHERE rolname='mycfc_app'),(SELECT reset_retired_at<=completed_at FROM mycfc_disposable_release.completion)`).Scan(&gone, &fenced, &retired); err != nil || !gone || !fenced || !retired {
			t.Fatalf("replacement safety: %v %v %v %v", gone, fenced, retired, err)
		}
		if _, err := conn.Exec(ctx, "INSERT INTO users(name,email,password_hash,date_of_birth) VALUES('Real new adult','new@example.test','synthetic','1990-01-01')"); err != nil {
			t.Fatal(err)
		}
		if err := BootstrapDisposableRelease(ctx, conn, "mycfc", credentials); err != nil {
			t.Fatal(err)
		}
		var count int
		if err := conn.QueryRow(ctx, "SELECT count(*) FROM users WHERE email='new@example.test'").Scan(&count); err != nil || count != 1 {
			t.Fatalf("retry lost new account: %v", err)
		}
		for _, sql := range []string{"DELETE FROM mycfc_disposable_release.completion", "TRUNCATE mycfc_disposable_release.completion", "UPDATE mycfc_disposable_release.completion SET reset_retired_at=now()+interval '1 day'"} {
			if _, err := conn.Exec(ctx, sql); err == nil {
				t.Fatalf("retirement mutation accepted: %s", sql)
			}
		}
		if _, err := conn.Exec(ctx, "UPDATE mycfc_disposable_release.completion SET candidate='bad'"); err != nil {
			t.Fatal(err)
		}
		if err := BootstrapDisposableRelease(ctx, conn, "mycfc", credentials); err == nil {
			t.Fatal("mismatched completion allowed retry")
		}
	})
	for _, tc := range []struct{ name, sql string }{
		{"unprivileged bootstrap", "SET ROLE mycfc_migration"},
		{"wrong schema owner", "ALTER SCHEMA public OWNER TO postgres"},
		{"partial ledger", "DELETE FROM mycfc_meta.schema_migrations WHERE version='reset-baseline-v1'"},
		{"missing ledger", "DROP TABLE mycfc_meta.schema_migrations"},
		{"unexpected tenant", "CREATE SCHEMA another_tenant"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := setup(t)
			if _, err := conn.Exec(ctx, tc.sql); err != nil {
				t.Fatal(err)
			}
			err := BootstrapDisposableRelease(ctx, conn, "mycfc", credentials)
			if err == nil {
				t.Fatal("unsafe predecessor accepted")
			}
			if _, err := conn.Exec(ctx, "RESET ROLE"); err != nil {
				t.Fatal(err)
			}
			var exists, login bool
			if err := conn.QueryRow(ctx, "SELECT to_regclass('public.synthetic_history') IS NOT NULL,(SELECT rolcanlogin FROM pg_roles WHERE rolname='mycfc_app')").Scan(&exists, &login); err != nil || !exists || !login {
				t.Fatalf("preflight changed predecessor: %v", err)
			}
		})
	}
	t.Run("minor administrator cannot be recovered", func(t *testing.T) {
		conn := setup(t)
		if _, err := conn.Exec(ctx, `ALTER TABLE users DISABLE TRIGGER USER; UPDATE users SET date_of_birth=(CURRENT_DATE - INTERVAL '10 years')::date; ALTER TABLE users ENABLE TRIGGER USER`); err != nil {
			t.Fatal(err)
		}
		if err := BootstrapDisposableRelease(ctx, conn, "mycfc", credentials); err == nil {
			t.Fatal("minor administrator permitted reset")
		}
		var exists bool
		if err := conn.QueryRow(ctx, "SELECT to_regclass('public.synthetic_history') IS NOT NULL").Scan(&exists); err != nil || !exists {
			t.Fatal("invalid administrator reset predecessor")
		}
	})
	// Inject driver faults at transaction boundaries, retaining a real PostgreSQL
	// transaction. Every failure must roll back DDL/identity and redact DETAIL.
	for _, stage := range []string{"LOCK TABLE public.users", "CREATE TEMP TABLE retained_disposable_admin", "DROP SCHEMA", "CREATE SCHEMA public", "SET LOCAL ROLE", "CREATE TABLE mycfc_meta.schema_migrations", "INSERT INTO mycfc_meta.schema_migrations", "RESET ROLE", "INSERT INTO public.users", "INSERT INTO public.user_platform_roles", "REVOKE ALL ON DATABASE", "REVOKE ALL ON SCHEMA public", "REVOKE ALL ON ALL TABLES", "CREATE SCHEMA mycfc_disposable_release", "INSERT INTO mycfc_disposable_release.completion", "CREATE FUNCTION mycfc_disposable_release.matches"} {
		t.Run("rollback "+stage, func(t *testing.T) {
			conn := setup(t)
			fault := &disposableFaultConnection{Conn: conn, stage: stage}
			err := BootstrapDisposableRelease(ctx, fault, "mycfc", credentials)
			if err == nil || strings.Contains(err.Error(), "private-driver-detail") {
				t.Fatalf("unsafe fault response: %v", err)
			}
			var exists bool
			if err := conn.QueryRow(ctx, "SELECT to_regclass('public.synthetic_history') IS NOT NULL").Scan(&exists); err != nil || !exists {
				t.Fatalf("failed replacement committed: %v", err)
			}
		})
	}
}

type disposableFaultConnection struct {
	*pgx.Conn
	stage string
}

func (c *disposableFaultConnection) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := c.Conn.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &disposableFaultTx{Tx: tx, stage: c.stage}, nil
}

type disposableFaultTx struct {
	pgx.Tx
	stage string
}

func (tx *disposableFaultTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if strings.HasPrefix(sql, tx.stage) {
		return pgconn.CommandTag{}, errors.New("private-driver-detail")
	}
	return tx.Tx.Exec(ctx, sql, args...)
}
