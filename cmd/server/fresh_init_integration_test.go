//go:build integration

package main

import (
	"context"
	"fmt"
	urlpkg "net/url"
	"os"
	"strings"
	"testing"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// TEST_FRESH_INIT_URL can point to a dedicated disposable empty database.
// In the normal integration suite, create a uniquely named throwaway database
// using the existing PostgreSQL test superuser; never reuse mycfc_test.
func TestExplicitFreshInitRealPostgres(t *testing.T) {
	url := os.Getenv("TEST_FRESH_INIT_URL")
	ctx := context.Background()
	if url == "" {
		base := os.Getenv("TEST_DATABASE_URL")
		if base == "" {
			t.Skip("requires a dedicated disposable PostgreSQL test database")
		}
		parsedBase, err := urlpkg.Parse(base)
		if err != nil {
			t.Fatal(err)
		}
		parsedBase.Path = "/postgres"
		admin, err := pgx.Connect(ctx, parsedBase.String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = admin.Close(context.Background()) })
		var superuser bool
		if err := admin.QueryRow(ctx, "SELECT rolsuper FROM pg_roles WHERE rolname=current_user").Scan(&superuser); err != nil || !superuser {
			t.Fatalf("fresh test requires existing test superuser: %v", err)
		}
		name := "fresh_contract_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
		if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := admin.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
				t.Errorf("remove disposable fresh test database: %v", err)
			}
		})
		parsedBase.Path = "/" + name
		url = parsedBase.String()
	}
	parsed, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	suffix := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	role := func(kind string) string { return "fresh_" + kind + "_" + suffix }
	app, migration, media, retention := role("web"), role("migration"), role("media"), role("retention")
	t.Setenv("DATABASE_URL", url)
	t.Setenv("APP_DB_USER", app)
	t.Setenv("APP_DB_PASSWORD", "test-web-"+suffix)
	t.Setenv("MIGRATION_DB_USER", migration)
	t.Setenv("MIGRATION_DB_PASSWORD", "test-migration-"+suffix)
	t.Setenv("MEDIA_CLEANUP_DB_USER", media)
	t.Setenv("MEDIA_CLEANUP_DB_PASSWORD", "test-media-"+suffix)
	t.Setenv("DATA_RETENTION_DB_USER", retention)
	t.Setenv("DATA_RETENTION_DB_PASSWORD", "test-retention-"+suffix)
	if err := runServerCommand(ctx, []string{"fresh-init-db"}); err == nil || !strings.Contains(err.Error(), "--confirm-empty") {
		t.Fatalf("explicit confirmation missing: %v", err)
	}
	if _, err := conn.Exec(ctx, `CREATE FUNCTION public.unapproved_control() RETURNS integer LANGUAGE sql AS 'SELECT 1'`); err != nil {
		t.Fatal(err)
	}
	if err := runServerCommand(ctx, []string{"fresh-init-db", "--confirm-empty"}); err == nil || !strings.Contains(err.Error(), "requires the named empty database") {
		t.Fatalf("function-only nonempty database accepted: %v", err)
	}
	var functionRetained bool
	if err := conn.QueryRow(ctx, `SELECT to_regprocedure('public.unapproved_control()') IS NOT NULL`).Scan(&functionRetained); err != nil || !functionRetained {
		t.Fatalf("nonempty function negative control lost: %t %v", functionRetained, err)
	}
	if _, err := conn.Exec(ctx, `DROP FUNCTION public.unapproved_control()`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `CREATE TABLE public.unrelated_control(id integer PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO public.unrelated_control(id) VALUES(1)`); err != nil {
		t.Fatal(err)
	}
	if err := runServerCommand(ctx, []string{"fresh-init-db", "--confirm-empty"}); err == nil || !strings.Contains(err.Error(), "requires the named empty database") {
		t.Fatalf("nonempty database accepted: %v", err)
	}
	var retained int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM public.unrelated_control`).Scan(&retained); err != nil || retained != 1 {
		t.Fatalf("nonempty negative control lost: %d %v", retained, err)
	}
	if _, err := conn.Exec(ctx, `DROP TABLE public.unrelated_control`); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DATA_RETENTION_DB_PASSWORD", "")
	if err := runServerCommand(ctx, []string{"fresh-init-db", "--confirm-empty"}); err == nil {
		t.Fatal("missing maintenance password accepted")
	}
	t.Setenv("DATA_RETENTION_DB_PASSWORD", "test-retention-"+suffix)
	if err := runServerCommand(ctx, []string{"fresh-init-db", "--confirm-empty"}); err != nil {
		t.Fatal(err)
	}
	if err := db.VerifyDatedParticipationContract(ctx, conn); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM mycfc_meta.schema_migrations`).Scan(&count); err != nil || count != len(db.EmbeddedMigrationInventory()) {
		t.Fatalf("ledger count=%d expected=%d err=%v", count, len(db.EmbeddedMigrationInventory()), err)
	}
	matched, err := db.FreshInitializationMatches(ctx, conn, parsed.Database, "", "")
	if err != nil || !matched {
		t.Fatalf("private receipt/ledger invalid: %t %v", matched, err)
	}
	for _, username := range []string{app, migration, media, retention} {
		var login bool
		if err := conn.QueryRow(ctx, `SELECT rolcanlogin FROM pg_roles WHERE rolname=$1`, username).Scan(&login); err != nil || !login {
			t.Fatalf("role %s not available: %v", username, err)
		}
	}
	if err := runServerCommand(ctx, []string{"fresh-init-db", "--confirm-empty"}); err == nil {
		t.Fatal("repeated fresh initialization accepted")
	}
	var after int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM mycfc_meta.schema_migrations`).Scan(&after); err != nil || after != count {
		t.Fatal(fmt.Errorf("negative retry changed ledger: %d %v", after, err))
	}
	// The migration role can reproduce the schema, ledger and matching row,
	// but cannot forge the bootstrap superuser's ownership of the receipt.
	if _, err := conn.Exec(ctx, `DROP SCHEMA mycfc_fresh_init CASCADE`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "SET ROLE "+pgx.Identifier{migration}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `CREATE SCHEMA mycfc_fresh_init`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `CREATE TABLE mycfc_fresh_init.receipt(database_name text PRIMARY KEY, version text NOT NULL, candidate text NOT NULL, schema_digest text NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO mycfc_fresh_init.receipt VALUES ($1,$2,$3,$4)`, parsed.Database, "", "", db.EmbeddedMigrationDigest()); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `RESET ROLE`); err != nil {
		t.Fatal(err)
	}
	if matched, err := db.FreshInitializationMatches(ctx, conn, parsed.Database, "", ""); err == nil || matched || !strings.Contains(err.Error(), "bootstrap-superuser-owned") {
		t.Fatalf("migration-role forged receipt accepted: matched=%t err=%v", matched, err)
	}
}
