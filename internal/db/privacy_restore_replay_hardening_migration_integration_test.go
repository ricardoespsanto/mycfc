//go:build integration

package db

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestPrivacyRestoreReplayHardeningForwardMigrationIsAdditive(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	const marker = "-- #247 replay hardening:"
	migration, err := migrationFiles.ReadFile("migrations/202609100012_privacy_restore_replay_hardening.sql")
	if err != nil {
		t.Fatal(err)
	}
	markerIndex := strings.LastIndex(baselineSchema, marker)
	if markerIndex < 0 || baselineSchema[markerIndex:] != string(migration) {
		t.Fatal("replay-hardening migration is not the final exact baseline segment")
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	schemaName := "privacy_247_hardening_" + suffix
	protectedName := "privacy_247_hardening_protected_" + suffix
	schema := pgx.Identifier{schemaName}.Sanitize()
	protected := pgx.Identifier{protectedName}.Sanitize()
	if _, err = conn.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = conn.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		_, _ = conn.Exec(ctx, "DROP SCHEMA IF EXISTS "+protected+" CASCADE")
	}()
	if _, err = conn.Exec(ctx, "SET search_path TO "+schema+",public"); err != nil {
		t.Fatal(err)
	}
	rewrite := func(sql string) string {
		sql = strings.ReplaceAll(sql, "public.", schemaName+".")
		sql = strings.ReplaceAll(sql, "pg_catalog, public", "pg_catalog, "+schemaName+", public")
		sql = strings.ReplaceAll(sql, "pg_catalog,public", "pg_catalog,"+schemaName+",public")
		return strings.ReplaceAll(sql, "privacy_protected", protectedName)
	}
	if _, err = conn.PgConn().Exec(ctx, rewrite(baselineSchema[:markerIndex])).ReadAll(); err != nil {
		t.Fatalf("create exact 011 baseline: %v", err)
	}
	userID := uuid.New()
	if _, err = conn.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth)
		VALUES($1,'Replay hardening preservation',$2,'hash','1990-01-01')`, userID, userID.String()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.PgConn().Exec(ctx, rewrite(string(migration))).ReadAll(); err != nil {
		t.Fatalf("apply replay hardening forward migration: %v", err)
	}
	var userPreserved, importColumn, evidenceTable, fixtureTable, attestationTable, closureV3, observerAPI bool
	if err = conn.QueryRow(ctx, `SELECT
		EXISTS(SELECT 1 FROM users WHERE id=$1),
		EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=$2 AND table_name='restore_ledger_imports' AND column_name='erasure_effective_at'),
		to_regclass($3) IS NOT NULL,to_regclass($4) IS NOT NULL,to_regclass($5) IS NOT NULL,
		to_regprocedure('privacy_tombstone_prepare_closure_v3(uuid,uuid)') IS NOT NULL,
		to_regprocedure('privacy_restore_observe_inventory(text,bytea,bytea,text,text,text,text)') IS NOT NULL`,
		userID, protectedName, protectedName+".restore_replay_already_applied_evidence",
		protectedName+".restore_synthetic_fixtures", protectedName+".restore_replay_inventory_attestations").
		Scan(&userPreserved, &importColumn, &evidenceTable, &fixtureTable, &attestationTable, &closureV3, &observerAPI); err != nil {
		t.Fatal(err)
	}
	if !userPreserved || !importColumn || !evidenceTable || !fixtureTable || !attestationTable || !closureV3 || !observerAPI {
		t.Fatalf("forward migration state user=%t import_clock=%t evidence=%t fixture=%t attestation=%t closure_v3=%t observer=%t",
			userPreserved, importColumn, evidenceTable, fixtureTable, attestationTable, closureV3, observerAPI)
	}
}
