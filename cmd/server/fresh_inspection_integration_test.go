//go:build integration

package main

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/cfcoimbra/mycfc/internal/releasecontract"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// The canary only emits a notice identifying its invoker. It never changes
// roles, accesses files/secrets, or executes external commands.
func TestFreshLedgerInspectionRejectsMigrationOwnedCodeRealPostgres(t *testing.T) {
	ctx := context.Background()
	raw := isolatedFreshInitURL(t)
	previousVersion, previousCandidate := releasecontract.Version, releasecontract.Candidate
	releasecontract.Version, releasecontract.Candidate = "v2.0.8", strings.Repeat("e", 40)
	t.Cleanup(func() { releasecontract.Version, releasecontract.Candidate = previousVersion, previousCandidate })
	t.Setenv("DATABASE_URL", raw)
	t.Setenv("APP_VERSION", releasecontract.Version)
	t.Setenv("GIT_SHA", releasecontract.Candidate)
	for key, value := range map[string]string{
		"APP_DB_USER": "inspection_web", "APP_DB_PASSWORD": "synthetic-web",
		"MIGRATION_DB_USER": "inspection_migration", "MIGRATION_DB_PASSWORD": "synthetic-migration",
		"MEDIA_CLEANUP_DB_USER": "inspection_media", "MEDIA_CLEANUP_DB_PASSWORD": "synthetic-media",
		"DATA_RETENTION_DB_USER": "inspection_retention", "DATA_RETENTION_DB_PASSWORD": "synthetic-retention",
	} {
		t.Setenv(key, value)
	}
	if err := runServerCommand(ctx, []string{"fresh-init-db", "--confirm-empty"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgx.ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	var canaryInvokers []string
	cfg.OnNotice = func(_ *pgconn.PgConn, n *pgconn.Notice) {
		if strings.HasPrefix(n.Message, "harmless-ledger-canary:") {
			canaryInvokers = append(canaryInvokers, n.Message)
		}
	}
	receipt, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer receipt.Close(ctx)
	lowcfg := cfg.Copy()
	lowcfg.User = "inspection_migration"
	lowcfg.Password = "synthetic-migration"
	inspector, err := pgx.ConnectConfig(ctx, lowcfg)
	if err != nil {
		t.Fatal(err)
	}
	defer inspector.Close(ctx)
	roles := databaseRoleCredentialsFromEnvironment()
	match := func() (bool, error) {
		return db.FreshInitializationMatchesWithInspector(ctx, receipt, inspector, roles.MigrationUsername, "mycfc", releasecontract.Version, releasecontract.Candidate)
	}
	if ok, err := match(); err != nil || !ok {
		t.Fatalf("legitimate split inspection: %t %v", ok, err)
	}
	if ok, err := db.FreshInitializationMatches(ctx, inspector, "mycfc", releasecontract.Version, releasecontract.Candidate); err != nil || !ok {
		t.Fatalf("migration compatibility wrapper: %t %v", ok, err)
	}
	if ok, err := db.FreshInitializationMatches(ctx, receipt, "mycfc", releasecontract.Version, releasecontract.Candidate); err == nil || ok {
		t.Fatal("bootstrap-authenticated compatibility inspection accepted")
	}
	if _, err := receipt.Exec(ctx, `SET ROLE inspection_migration`); err != nil {
		t.Fatal(err)
	}
	if ok, err := db.FreshInitializationMatches(ctx, receipt, "mycfc", releasecontract.Version, releasecontract.Candidate); err == nil || ok {
		t.Fatal("SET ROLE substituted for independent authentication")
	}
	if _, err := receipt.Exec(ctx, `RESET ROLE`); err != nil {
		t.Fatal(err)
	}
	if ok, err := db.FreshInitializationMatchesWithInspector(ctx, receipt, inspector, "wrong_migration", "mycfc", releasecontract.Version, releasecontract.Candidate); err == nil || ok {
		t.Fatal("wrong expected migration identity accepted")
	}
	// Fresh schema mutation is fenced even when no publisher has served yet.
	if _, err := receipt.Exec(ctx, `ALTER ROLE inspection_web NOLOGIN`); err != nil {
		t.Fatal(err)
	}
	// Legitimate command dispatch opens the separate migration connection for
	// privileged bootstrap/hardening and reuses migration authentication for migrate.
	for _, command := range []string{"bootstrap-db", "harden-db"} {
		if err := runServerCommand(ctx, []string{command}); err != nil {
			t.Fatalf("legitimate %s: %v", command, err)
		}
	}
	lowURL, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	lowURL.User = url.UserPassword(lowcfg.User, lowcfg.Password)
	t.Setenv("DATABASE_URL", lowURL.String())
	if err := runServerCommand(ctx, []string{"migrate"}); err != nil {
		t.Fatalf("legitimate migrate: %v", err)
	}
	t.Setenv("DATABASE_URL", raw)
	// Observe real authentication/role catalogs, but fail the regression if
	// either refused command reaches any migration statement. Catalog-only
	// inspection now legitimately uses its own read-only transaction.
	previousConnect := connectDatabaseCommand
	var deniedExecutionAttempts int
	connectDatabaseCommand = func(ctx context.Context, raw string) (databaseCommandConnection, error) {
		cfg, err := pgx.ParseConfig(raw)
		if err != nil {
			return nil, err
		}
		cfg.Tracer = migrationExecutionCanaryTracer{attempts: &deniedExecutionAttempts}
		return pgx.ConnectConfig(ctx, cfg)
	}
	t.Cleanup(func() { connectDatabaseCommand = previousConnect })
	if err := runServerCommand(ctx, []string{"migrate"}); err == nil || err.Error() != "migration execution requires its authenticated non-superuser identity" {
		t.Fatalf("bootstrap execution of stamped migrate: %v", err)
	}
	roleURL, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	options := roleURL.Query()
	options.Set("options", "-c role=inspection_migration")
	roleURL.RawQuery = options.Encode()
	t.Setenv("DATABASE_URL", roleURL.String())
	if err := runServerCommand(ctx, []string{"migrate"}); err == nil || err.Error() != "migration execution requires its authenticated non-superuser identity" {
		t.Fatalf("SET ROLE execution of stamped migrate: %v", err)
	}
	connectDatabaseCommand = previousConnect
	if deniedExecutionAttempts != 0 {
		t.Fatalf("refused migration reached %d execution operations", deniedExecutionAttempts)
	}
	t.Setenv("DATABASE_URL", raw)
	if _, err := inspector.Exec(ctx, `CREATE FUNCTION mycfc_meta.ledger_canary() RETURNS text LANGUAGE plpgsql SECURITY INVOKER AS $$ BEGIN RAISE NOTICE 'harmless-ledger-canary:%',current_user; RETURN 'canary'; END $$`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, create, drop string }{
		{"invoker view", `CREATE VIEW mycfc_meta.schema_migrations AS SELECT mycfc_meta.ledger_canary() AS version,clock_timestamp() AS applied_at`, `DROP VIEW mycfc_meta.schema_migrations`},
		{"varchar version", `CREATE TABLE mycfc_meta.schema_migrations(version varchar PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now())`, `DROP TABLE mycfc_meta.schema_migrations`},
		{"domain version", `CREATE DOMAIN mycfc_meta.ledger_version AS text; CREATE TABLE mycfc_meta.schema_migrations(version mycfc_meta.ledger_version PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now())`, `DROP TABLE mycfc_meta.schema_migrations; DROP DOMAIN mycfc_meta.ledger_version`},
		{"wrong applied timestamp", `CREATE TABLE mycfc_meta.schema_migrations(version text PRIMARY KEY,applied_at text NOT NULL DEFAULT '')`, `DROP TABLE mycfc_meta.schema_migrations`},
		{"no primary key", `CREATE TABLE mycfc_meta.schema_migrations(version text NOT NULL,applied_at timestamptz NOT NULL DEFAULT now())`, `DROP TABLE mycfc_meta.schema_migrations`},
		{"row security", `CREATE TABLE mycfc_meta.schema_migrations(version text PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now()); ALTER TABLE mycfc_meta.schema_migrations ENABLE ROW LEVEL SECURITY`, `DROP TABLE mycfc_meta.schema_migrations`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := inspector.Exec(ctx, `ALTER TABLE mycfc_meta.schema_migrations RENAME TO saved_schema_migrations`); err != nil {
				t.Fatal(err)
			}
			if _, err := inspector.Exec(ctx, tc.create); err != nil {
				t.Fatal(err)
			}
			if tc.name == "invoker view" {
				var value string
				if err := inspector.QueryRow(ctx, `SELECT version FROM mycfc_meta.schema_migrations`).Scan(&value); err != nil {
					t.Fatal(err)
				}
				if len(canaryInvokers) != 1 || canaryInvokers[0] != "harmless-ledger-canary:inspection_migration" {
					t.Fatalf("canary control: %v", canaryInvokers)
				}
				canaryInvokers = nil
			}
			for _, call := range []struct {
				name string
				run  func() error
			}{
				{"bootstrap", func() error {
					return bootstrapRelease(ctx, receipt, "mycfc", roles, releasecontract.Version, releasecontract.Candidate)
				}},
				{"harden admission", func() error {
					return requireDisposableCompletion(ctx, receipt, "mycfc", releasecontract.Version, releasecontract.Candidate, roles)
				}},
				{"migration admission", func() error {
					return requireDisposableCompletion(ctx, inspector, "mycfc", releasecontract.Version, releasecontract.Candidate, roles)
				}},
			} {
				if err := call.run(); err == nil || err.Error() != "fresh initialization receipt inspection failed" {
					t.Fatalf("%s malformed ledger error: %v", call.name, err)
				}
			}
			if len(canaryInvokers) != 0 {
				t.Fatalf("untrusted view executed during inspection: %v", canaryInvokers)
			}
			if _, err := inspector.Exec(ctx, tc.drop+`; ALTER TABLE mycfc_meta.saved_schema_migrations RENAME TO schema_migrations`); err != nil {
				t.Fatal(err)
			}
			if ok, err := match(); err != nil || !ok {
				t.Fatalf("restored original ledger: %t %v", ok, err)
			}
		})
	}
	// Swap the catalog-validated table immediately before its name is locked.
	// Revalidation must reject the replacement without evaluating its view.
	var swapErr error
	racecfg := lowcfg.Copy()
	swapTracer := &freshLedgerSwapTracer{swap: func() {
		_, swapErr = inspector.Exec(ctx, `ALTER TABLE mycfc_meta.schema_migrations RENAME TO saved_schema_migrations; CREATE VIEW mycfc_meta.schema_migrations AS SELECT mycfc_meta.ledger_canary() AS version,clock_timestamp() AS applied_at`)
	}}
	racecfg.Tracer = swapTracer
	raceInspector, err := pgx.ConnectConfig(ctx, racecfg)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := db.FreshInitializationMatchesWithInspector(ctx, receipt, raceInspector, roles.MigrationUsername, "mycfc", releasecontract.Version, releasecontract.Candidate); err == nil || ok {
		t.Fatal("ledger replacement race accepted")
	}
	if swapErr != nil {
		t.Fatal(swapErr)
	}
	if !swapTracer.swapped {
		t.Fatal("ledger race control did not run")
	}
	if len(canaryInvokers) != 0 {
		t.Fatalf("replacement view evaluated: %v", canaryInvokers)
	}
	if err := raceInspector.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := inspector.Exec(ctx, `DROP VIEW mycfc_meta.schema_migrations; ALTER TABLE mycfc_meta.saved_schema_migrations RENAME TO schema_migrations`); err != nil {
		t.Fatal(err)
	}
	for _, alter := range []string{`ALTER SCHEMA mycfc_meta OWNER TO inspection_web`, `ALTER TABLE mycfc_meta.schema_migrations OWNER TO inspection_web`} {
		if _, err := receipt.Exec(ctx, alter); err != nil {
			t.Fatal(err)
		}
		if ok, err := match(); err == nil || ok {
			t.Fatal("wrong schema/table owner accepted")
		}
		if _, err := receipt.Exec(ctx, `ALTER SCHEMA mycfc_meta OWNER TO inspection_migration; ALTER TABLE mycfc_meta.schema_migrations OWNER TO inspection_migration`); err != nil {
			t.Fatal(err)
		}
	}
	// The receipt and ledger must refer to the same actual database/backend.
	if _, err := receipt.Exec(ctx, `CREATE DATABASE other_inspection OWNER inspection_migration`); err != nil {
		t.Fatal(err)
	}
	othercfg := lowcfg.Copy()
	othercfg.Database = "other_inspection"
	other, err := pgx.ConnectConfig(ctx, othercfg)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close(ctx)
	if ok, err := db.FreshInitializationMatchesWithInspector(ctx, receipt, other, roles.MigrationUsername, "mycfc", releasecontract.Version, releasecontract.Candidate); err == nil || ok {
		t.Fatal("different database inspector accepted")
	}
	// An elevated membership is not a safe inspection identity, even when it
	// is not the active role. These fixture roles never execute privileged code.
	if _, err := receipt.Exec(ctx, `CREATE ROLE inspection_elevated NOLOGIN CREATEROLE; GRANT inspection_elevated TO inspection_migration`); err != nil {
		t.Fatal(err)
	}
	if ok, err := match(); err == nil || ok {
		t.Fatal("elevated migration membership accepted")
	}
	if _, err := receipt.Exec(ctx, `REVOKE inspection_elevated FROM inspection_migration; DROP ROLE inspection_elevated`); err != nil {
		t.Fatal(err)
	}
	if ok, err := match(); err != nil || !ok {
		t.Fatalf("restored safe role: %t %v", ok, err)
	}
	// A failed second authentication must not disclose its DSN/password.
	roles.MigrationPassword = "do-not-log-this-password"
	if err := bootstrapRelease(ctx, receipt, "mycfc", roles, releasecontract.Version, releasecontract.Candidate); err == nil || strings.Contains(err.Error(), roles.MigrationPassword) || strings.Contains(err.Error(), raw) {
		t.Fatalf("unsafe connection failure: %v", err)
	}
}

type freshLedgerSwapTracer struct {
	swap    func()
	swapped bool
}

func (t *freshLedgerSwapTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if !t.swapped && strings.HasPrefix(data.SQL, "LOCK TABLE ONLY mycfc_meta.schema_migrations") {
		t.swapped = true
		t.swap()
	}
	return ctx
}
func (*freshLedgerSwapTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

type migrationExecutionCanaryTracer struct{ attempts *int }

func (c migrationExecutionCanaryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, query pgx.TraceQueryStartData) context.Context {
	sql := strings.ToUpper(strings.TrimSpace(query.SQL))
	for _, prefix := range []string{"CREATE", "ALTER", "INSERT", "UPDATE", "DELETE", "DROP", "TRUNCATE", "DO "} {
		if strings.HasPrefix(sql, prefix) {
			*c.attempts++
			denied, cancel := context.WithCancel(ctx)
			cancel()
			return denied
		}
	}
	return ctx
}
func (migrationExecutionCanaryTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {
}
