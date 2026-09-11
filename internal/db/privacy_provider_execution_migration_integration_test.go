//go:build integration

package db

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestPrivacyProviderExecutionMigrationAddsOnlyProtectedFencedSurfaces(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)

	if _, err = tx.Exec(ctx, `
		DROP FUNCTION privacy_worker_complete_provider_checkpoint(uuid,uuid,uuid,bigint,uuid);
		DROP FUNCTION privacy_worker_record_provider_evidence(uuid,uuid,uuid,uuid,bigint,uuid,text,integer,text,text,text,text,text,text,text,bytea);
		DROP FUNCTION privacy_worker_list_provider_targets(uuid,uuid,uuid,bigint,uuid);
		DROP FUNCTION privacy_execution_complete_provider_capture(uuid,text);
		DROP FUNCTION privacy_execution_materialize_provider_target(uuid,uuid,uuid,uuid,uuid,bytea,text,text,text,text,bigint,text,text,bytea,text,text,text,bytea,bytea,bytea,text,text,text,bytea,bytea,bytea,text,bytea,text,bytea);
		DROP FUNCTION privacy_execution_capture_provider_connections(uuid,uuid,text);
		DROP TABLE privacy_protected.provider_evidence;
		DROP TABLE privacy_protected.provider_credential_quarantine;
		DROP TABLE privacy_protected.provider_target_digests;
		DROP TABLE privacy_protected.provider_targets;
		DROP TABLE privacy_protected.provider_capture_sets;
		DROP TABLE privacy_protected.provider_connections;`); err != nil {
		t.Fatal(err)
	}
	migration, err := migrationFiles.ReadFile("migrations/202609100009_privacy_provider_execution.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}

	var tables, routines, publicExecutions int
	if err = tx.QueryRow(ctx, `SELECT
	 (SELECT count(*) FROM unnest(ARRAY[
	  'privacy_protected.provider_connections'::regclass,'privacy_protected.provider_capture_sets'::regclass,
	  'privacy_protected.provider_targets'::regclass,'privacy_protected.provider_target_digests'::regclass,
	  'privacy_protected.provider_credential_quarantine'::regclass,'privacy_protected.provider_evidence'::regclass]) row),
	 (SELECT count(*) FROM unnest(ARRAY[
	  'privacy_execution_capture_provider_connections(uuid,uuid,text)'::regprocedure,
	  'privacy_execution_materialize_provider_target(uuid,uuid,uuid,uuid,uuid,bytea,text,text,text,text,bigint,text,text,bytea,text,text,text,bytea,bytea,bytea,text,text,text,bytea,bytea,bytea,text,bytea,text,bytea)'::regprocedure,
	  'privacy_execution_complete_provider_capture(uuid,text)'::regprocedure,
	  'privacy_worker_list_provider_targets(uuid,uuid,uuid,bigint,uuid)'::regprocedure,
	  'privacy_worker_record_provider_evidence(uuid,uuid,uuid,uuid,bigint,uuid,text,integer,text,text,text,text,text,text,text,bytea)'::regprocedure,
	  'privacy_worker_complete_provider_checkpoint(uuid,uuid,uuid,bigint,uuid)'::regprocedure]) routine),
	 (SELECT count(*) FROM unnest(ARRAY[
	  'privacy_execution_capture_provider_connections(uuid,uuid,text)'::regprocedure,
	  'privacy_execution_materialize_provider_target(uuid,uuid,uuid,uuid,uuid,bytea,text,text,text,text,bigint,text,text,bytea,text,text,text,bytea,bytea,bytea,text,text,text,bytea,bytea,bytea,text,bytea,text,bytea)'::regprocedure,
	  'privacy_execution_complete_provider_capture(uuid,text)'::regprocedure,
	  'privacy_worker_list_provider_targets(uuid,uuid,uuid,bigint,uuid)'::regprocedure,
	  'privacy_worker_record_provider_evidence(uuid,uuid,uuid,uuid,bigint,uuid,text,integer,text,text,text,text,text,text,text,bytea)'::regprocedure,
	  'privacy_worker_complete_provider_checkpoint(uuid,uuid,uuid,bigint,uuid)'::regprocedure]) routine WHERE has_function_privilege('public',routine,'EXECUTE'))`).Scan(&tables, &routines, &publicExecutions); err != nil {
		t.Fatal(err)
	}
	if tables != 6 || routines != 6 || publicExecutions != 0 {
		t.Fatalf("tables=%d routines=%d public_execute=%d", tables, routines, publicExecutions)
	}

	var publicTables int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM unnest(ARRAY[
	 'privacy_protected.provider_connections'::regclass,'privacy_protected.provider_targets'::regclass,
	 'privacy_protected.provider_credential_quarantine'::regclass,'privacy_protected.provider_evidence'::regclass]) relation
	 WHERE has_table_privilege('public',relation,'SELECT') OR has_table_privilege('public',relation,'INSERT') OR has_table_privilege('public',relation,'UPDATE') OR has_table_privilege('public',relation,'DELETE')`).Scan(&publicTables); err != nil {
		t.Fatal(err)
	}
	if publicTables != 0 {
		t.Fatalf("PUBLIC can access %d protected provider tables", publicTables)
	}
}
