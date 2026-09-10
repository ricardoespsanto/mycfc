//go:build integration

package db

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestPrivacyObjectExecutionMigrationAddsProtectedCaptureAndFencedWorkerRoutines(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	// Reconstruct the immediately preceding object-target foundation inside the
	// rollback-only transaction, then prove the forward migration applies.
	if _, err = tx.Exec(ctx, `
DROP FUNCTION privacy_worker_complete_object_checkpoint(uuid,uuid,uuid,bigint,uuid);
DROP FUNCTION privacy_worker_record_object_evidence(uuid,uuid,uuid,uuid,bigint,uuid,integer,integer,integer,integer,text,bytea);
DROP FUNCTION privacy_worker_list_object_targets(uuid,uuid,uuid,bigint,uuid);
DROP FUNCTION privacy_execution_complete_object_capture(uuid,text);
DROP FUNCTION privacy_execution_materialize_object_target(uuid,uuid,uuid,uuid,bytea,text,text,uuid,uuid,text,text,text,text,bytea,bytea,bytea,text,bytea);
	DROP FUNCTION privacy_execution_capture_media_sources(uuid,uuid,text);
	DROP FUNCTION privacy_media_subject_lock(uuid);
	DROP TABLE privacy_protected.object_capture_sets;
	ALTER TABLE privacy_protected.object_targets DROP CONSTRAINT privacy_object_targets_capture_source_unique;
	ALTER TABLE privacy_protected.object_targets DROP COLUMN upload_intent_id`); err != nil {
		t.Fatal(err)
	}
	migration, err := migrationFiles.ReadFile("migrations/202609100005_privacy_object_execution.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}

	var captureTable, intentColumn, immutableTrigger, uniqueCapturedSource bool
	var routines, publicExecutions int
	if err = tx.QueryRow(ctx, `SELECT
  to_regclass('privacy_protected.object_capture_sets') IS NOT NULL,
	  EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='privacy_protected' AND table_name='object_targets' AND column_name='upload_intent_id'),
	  EXISTS(SELECT 1 FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace
	    WHERE n.nspname='privacy_protected' AND c.relname='object_capture_sets' AND t.tgname='privacy_object_capture_sets_immutable' AND NOT t.tgisinternal),
	  EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='privacy_protected.object_targets'::regclass AND conname='privacy_object_targets_capture_source_unique'),
  (SELECT count(*) FROM unnest(ARRAY[
    'privacy_execution_capture_media_sources(uuid,uuid,text)'::regprocedure,
    'privacy_execution_materialize_object_target(uuid,uuid,uuid,uuid,bytea,text,text,uuid,uuid,text,text,text,text,bytea,bytea,bytea,text,bytea)'::regprocedure,
    'privacy_execution_complete_object_capture(uuid,text)'::regprocedure,
    'privacy_worker_list_object_targets(uuid,uuid,uuid,bigint,uuid)'::regprocedure,
    'privacy_worker_record_object_evidence(uuid,uuid,uuid,uuid,bigint,uuid,integer,integer,integer,integer,text,bytea)'::regprocedure,
    'privacy_worker_complete_object_checkpoint(uuid,uuid,uuid,bigint,uuid)'::regprocedure
  ]) routine),
  (SELECT count(*) FROM unnest(ARRAY[
    'privacy_execution_capture_media_sources(uuid,uuid,text)'::regprocedure,
    'privacy_execution_materialize_object_target(uuid,uuid,uuid,uuid,bytea,text,text,uuid,uuid,text,text,text,text,bytea,bytea,bytea,text,bytea)'::regprocedure,
    'privacy_execution_complete_object_capture(uuid,text)'::regprocedure,
    'privacy_worker_list_object_targets(uuid,uuid,uuid,bigint,uuid)'::regprocedure,
    'privacy_worker_record_object_evidence(uuid,uuid,uuid,uuid,bigint,uuid,integer,integer,integer,integer,text,bytea)'::regprocedure,
    'privacy_worker_complete_object_checkpoint(uuid,uuid,uuid,bigint,uuid)'::regprocedure
	  ]) routine WHERE has_function_privilege('public',routine,'EXECUTE'))`).Scan(&captureTable, &intentColumn, &immutableTrigger, &uniqueCapturedSource, &routines, &publicExecutions); err != nil {
		t.Fatal(err)
	}
	if !captureTable || !intentColumn || !immutableTrigger || !uniqueCapturedSource || routines != 6 || publicExecutions != 0 {
		t.Fatalf("capture_table=%t intent_column=%t immutable_trigger=%t unique_source=%t routines=%d public_execute=%d", captureTable, intentColumn, immutableTrigger, uniqueCapturedSource, routines, publicExecutions)
	}
}
