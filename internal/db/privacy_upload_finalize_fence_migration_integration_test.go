//go:build integration

package db

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestPrivacyUploadFinalizeFenceForwardMigrationClosesBothCaptureRaces(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	rewindGuardianAgeHandoffMigration(t, ctx, tx)

	migration, err := migrationFiles.ReadFile("migrations/202609110001_privacy_upload_finalize_execution_fence.sql")
	if err != nil {
		t.Fatal(err)
	}
	const migrationMarker = "-- #246 closes the reservation-to-finalisation race with execution capture."
	baselineIndex := strings.LastIndex(baselineSchema, migrationMarker)
	nextIndex := strings.LastIndex(baselineSchema, "-- Baseline through 202609110002_privacy_empty_provider_registry_activation.")
	if baselineIndex < 0 || nextIndex <= baselineIndex || strings.TrimSpace(baselineSchema[baselineIndex:nextIndex]) != strings.TrimSpace(string(migration)) {
		t.Fatal("privacy fence migration is not the exact baseline segment")
	}
	if _, err = tx.Exec(ctx, `ALTER TABLE privacy_activation_authenticated_artifacts DISABLE TRIGGER privacy_activation_authenticated_artifacts_immutable;
		DELETE FROM privacy_activation_authenticated_artifacts;
		ALTER TABLE privacy_activation_authenticated_artifacts ENABLE TRIGGER privacy_activation_authenticated_artifacts_immutable`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `ALTER TABLE privacy_activation_authenticated_artifacts DROP CONSTRAINT privacy_activation_authenticated_artifacts_v11_check`); err != nil {
		t.Fatal(err)
	}
	constraintPredecessor := migrationFunctionSegment(t, string(migration),
		"ALTER TABLE privacy_activation_authenticated_artifacts ADD CONSTRAINT privacy_activation_authenticated_artifacts_v4_check",
		"DO $$DECLARE definition text;old_clause text;new_clause text;")
	if _, err = tx.Exec(ctx, constraintPredecessor); err != nil {
		t.Fatal(err)
	}

	// Recreate the actual predecessor media routines so the migration is tested
	// against the schema version it will encounter in an upgraded database.
	objectPredecessor, err := migrationFiles.ReadFile("migrations/202609100005_privacy_object_execution.sql")
	if err != nil {
		t.Fatal(err)
	}
	capturePredecessor := migrationFunctionSegment(t, string(objectPredecessor),
		"CREATE OR REPLACE FUNCTION public.privacy_execution_capture_media_sources(",
		"CREATE FUNCTION public.privacy_execution_materialize_object_target(")
	if _, err = tx.Exec(ctx, capturePredecessor); err != nil {
		t.Fatal(err)
	}
	uploadPredecessor, err := migrationFiles.ReadFile("migrations/202609100004_privacy_upload_provenance.sql")
	if err != nil {
		t.Fatal(err)
	}
	finalizePredecessor := migrationFunctionSegment(t, string(uploadPredecessor),
		"CREATE FUNCTION public.privacy_upload_finalize(",
		"CREATE FUNCTION public.privacy_upload_confirm_put(")
	finalizePredecessor = strings.Replace(finalizePredecessor, "CREATE FUNCTION", "CREATE OR REPLACE FUNCTION", 1)
	if _, err = tx.Exec(ctx, finalizePredecessor); err != nil {
		t.Fatal(err)
	}

	// Recreate the exact predecessor clauses asserted by the forward migration.
	if _, err = tx.Exec(ctx, `DO $$DECLARE definition text;
BEGIN
 SELECT pg_get_functiondef('privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)'::regprocedure) INTO definition;
 EXECUTE replace(definition,'202609120003_guardian_authority_renewal','202609100015_privacy_membership_postcondition');
 SELECT pg_get_functiondef('privacy_activation_authenticated_set_digest(text,uuid[])'::regprocedure) INTO definition;
 EXECUTE replace(definition,'202609120003_guardian_authority_renewal','202609100015_privacy_membership_postcondition');
END$$`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}

	var captureDefinition, finalizeDefinition, evidenceDefinition, digestDefinition string
	if err = tx.QueryRow(ctx, `SELECT pg_get_functiondef('privacy_execution_capture_media_sources(uuid,uuid,text)'::regprocedure)`).Scan(&captureDefinition); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT pg_get_functiondef('privacy_upload_finalize(uuid,bytea,text,text,text,bytea,bytea,bytea,text,bytea,bytea)'::regprocedure)`).Scan(&finalizeDefinition); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT pg_get_functiondef('privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)'::regprocedure)`).Scan(&evidenceDefinition); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT pg_get_functiondef('privacy_activation_authenticated_set_digest(text,uuid[])'::regprocedure)`).Scan(&digestDefinition); err != nil {
		t.Fatal(err)
	}

	intentLock := strings.Index(captureDefinition, "PERFORM i.id FROM")
	sourceLock := strings.Index(captureDefinition, "FOR v_kind,v_ref IN")
	unfinishedCheck := strings.Index(captureDefinition, "privacy_media_upload_in_flight")
	expectedCount := strings.Index(captureDefinition, "SELECT count(*)::integer INTO v_expected")
	captureInsert := strings.Index(captureDefinition, "INSERT INTO privacy_protected.object_capture_sets")
	if intentLock < 0 || sourceLock < 0 || unfinishedCheck < 0 || expectedCount < 0 || captureInsert < 0 ||
		!(intentLock < sourceLock && sourceLock < unfinishedCheck && sourceLock < expectedCount && sourceLock < captureInsert) {
		t.Fatalf("capture locks are not ordered before classification/count/insert: intent=%d source=%d unfinished=%d count=%d insert=%d", intentLock, sourceLock, unfinishedCheck, expectedCount, captureInsert)
	}
	for _, required := range []string{"privacy_media_subject_lock(v_privacy_subject)", "privacy_media_execution_in_progress", "request.status IN ('PROCESSING','RETRYABLE_FAILED','TERMINAL_FAILED')"} {
		if !strings.Contains(finalizeDefinition, required) {
			t.Fatalf("finalize fence missing %q", required)
		}
	}
	for name, definition := range map[string]string{"evidence": evidenceDefinition, "digest": digestDefinition} {
		if !strings.Contains(definition, "202609110001_privacy_upload_finalize_execution_fence") {
			t.Fatalf("%s activation function retained an old schema boundary", name)
		}
	}
}

func migrationFunctionSegment(t *testing.T, migration, start, next string) string {
	t.Helper()
	startIndex := strings.Index(migration, start)
	if startIndex < 0 {
		t.Fatalf("migration function start %q not found", start)
	}
	nextIndex := strings.Index(migration[startIndex:], next)
	if nextIndex < 0 {
		t.Fatalf("migration function terminator %q not found", next)
	}
	return strings.TrimSpace(migration[startIndex : startIndex+nextIndex])
}
