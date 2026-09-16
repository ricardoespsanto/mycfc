//go:build integration

package db

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestPrivacyExecutorRetentionSurfaceIsFenced(t *testing.T) {
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

	var checkpointDefinition, completionDefinition, retentionDefinition string
	var sources, pending, v18, publicHelper, publicInner bool
	err = conn.QueryRow(ctx, `SELECT
		pg_get_functiondef('privacy_worker_execute_checkpoint(uuid,uuid,uuid,bigint,uuid,text,text)'::regprocedure),
		pg_get_functiondef('privacy_completion_finalize(uuid,uuid,bytea,bytea)'::regprocedure),
		pg_get_functiondef('privacy_retention_run(uuid,integer)'::regprocedure),
		to_regclass('privacy_protected.execution_retention_sources') IS NOT NULL,
		to_regclass('privacy_protected.execution_retention_pending') IS NOT NULL,
		EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='privacy_activation_authenticated_artifacts'::regclass AND conname='privacy_activation_authenticated_artifacts_v18_check'),
		has_function_privilege('public','privacy_worker_execute_retention_checkpoint(uuid,uuid,uuid,bigint,uuid,text,text)','EXECUTE'),
		has_function_privilege('public','privacy_worker_execute_checkpoint_inner_017(uuid,uuid,uuid,bigint,uuid,text,text)','EXECUTE')`).Scan(
		&checkpointDefinition, &completionDefinition, &retentionDefinition, &sources, &pending, &v18, &publicHelper, &publicInner)
	if err != nil {
		t.Fatal(err)
	}
	for operation := range map[string]bool{
		"AUTH_SESSION_EXPIRE": true, "CONSENT_EVIDENCE_RESTRICT": true, "CONSENT_NETWORK_EXPIRE": true,
		"IDENTITY_RESTRICT": true, "LOG_RECORD_EXPIRE": true, "OUTBOX_PAYLOAD_EXPIRE": true,
		"PRIVACY_CASE_RESTRICT": true, "PROFILE_RESTRICT": true,
	} {
		if !strings.Contains(checkpointDefinition, operation) {
			t.Errorf("checkpoint dispatcher missing %s", operation)
		}
	}
	if !strings.Contains(completionDefinition, "privacy_completion_resolve_retention") ||
		!strings.Contains(retentionDefinition, "privacy_retention_run_inner_017") ||
		!sources || !pending || !v18 || publicHelper || publicInner {
		t.Fatalf("surface sources=%v pending=%v v18=%v public_helper=%v public_inner=%v", sources, pending, v18, publicHelper, publicInner)
	}

	var leapYear, calendarMonth string
	if err = conn.QueryRow(ctx, `SELECT
		privacy_retention_add_offset('2024-02-29 12:00:00+00',1,'CALENDAR_YEAR')::text,
		privacy_retention_add_offset('2026-01-31 12:00:00+00',1,'CALENDAR_MONTH')::text`).Scan(&leapYear, &calendarMonth); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(leapYear, "2025-02-28 12:00:00") || !strings.HasPrefix(calendarMonth, "2026-02-28 12:00:00") {
		t.Fatalf("calendar offsets year=%q month=%q", leapYear, calendarMonth)
	}
}

// Older forward-migration fixtures first remove the later privacy schema
// bindings so they can reconstruct their exact predecessor release.
func rewindExecutorAndSyntheticAcceptanceBinding(t *testing.T, ctx context.Context, tx pgx.Tx) {
	t.Helper()
	_, err := tx.Exec(ctx, `DO $$DECLARE d text;BEGIN
 IF EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='privacy_activation_authenticated_artifacts'::regclass AND conname='privacy_activation_authenticated_artifacts_v20_check') THEN
  ALTER TABLE privacy_activation_authenticated_artifacts DISABLE TRIGGER privacy_activation_authenticated_artifacts_immutable;
  DELETE FROM privacy_activation_authenticated_artifacts;
  ALTER TABLE privacy_activation_authenticated_artifacts ENABLE TRIGGER privacy_activation_authenticated_artifacts_immutable;
  SELECT pg_get_constraintdef(oid) INTO d FROM pg_constraint WHERE conrelid='privacy_activation_authenticated_artifacts'::regclass AND conname='privacy_activation_authenticated_artifacts_v20_check';
  IF strpos(d,'202609170004_privacy_empty_provider_execution')=0 THEN RAISE EXCEPTION 'empty provider execution rewind mismatch'; END IF;
  d:=replace(d,', ''202609170004_privacy_empty_provider_execution''::text','');
  ALTER TABLE privacy_activation_authenticated_artifacts DROP CONSTRAINT privacy_activation_authenticated_artifacts_v20_check;
  EXECUTE 'ALTER TABLE privacy_activation_authenticated_artifacts ADD CONSTRAINT privacy_activation_authenticated_artifacts_v19_check '||d;
  EXECUTE replace(pg_get_functiondef('privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)'::regprocedure),'202609170004_privacy_empty_provider_execution','202609170003_privacy_activation_fixed_access');
  EXECUTE replace(pg_get_functiondef('privacy_activation_authenticated_set_digest(text,uuid[])'::regprocedure),'202609170004_privacy_empty_provider_execution','202609170003_privacy_activation_fixed_access');
 END IF;
 IF EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='privacy_activation_authenticated_artifacts'::regclass AND conname='privacy_activation_authenticated_artifacts_v19_check') THEN
  ALTER TABLE privacy_activation_authenticated_artifacts DISABLE TRIGGER privacy_activation_authenticated_artifacts_immutable;
  DELETE FROM privacy_activation_authenticated_artifacts;
  ALTER TABLE privacy_activation_authenticated_artifacts ENABLE TRIGGER privacy_activation_authenticated_artifacts_immutable;
  SELECT pg_get_constraintdef(oid) INTO d FROM pg_constraint WHERE conrelid='privacy_activation_authenticated_artifacts'::regclass AND conname='privacy_activation_authenticated_artifacts_v19_check';
  IF strpos(d,'202609170003_privacy_activation_fixed_access')=0 THEN RAISE EXCEPTION 'activation fixed access rewind mismatch'; END IF;
  d:=replace(d,', ''202609170003_privacy_activation_fixed_access''::text','');
  ALTER TABLE privacy_activation_authenticated_artifacts DROP CONSTRAINT privacy_activation_authenticated_artifacts_v19_check;
  EXECUTE 'ALTER TABLE privacy_activation_authenticated_artifacts ADD CONSTRAINT privacy_activation_authenticated_artifacts_v18_check '||d;
  EXECUTE replace(pg_get_functiondef('privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)'::regprocedure),'202609170003_privacy_activation_fixed_access','202609170002_privacy_executor_retention_handlers');
  EXECUTE replace(pg_get_functiondef('privacy_activation_authenticated_set_digest(text,uuid[])'::regprocedure),'202609170003_privacy_activation_fixed_access','202609170002_privacy_executor_retention_handlers');
 END IF;
 IF EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='privacy_activation_authenticated_artifacts'::regclass AND conname='privacy_activation_authenticated_artifacts_v18_check') THEN
  ALTER TABLE privacy_activation_authenticated_artifacts DISABLE TRIGGER privacy_activation_authenticated_artifacts_immutable;
  DELETE FROM privacy_activation_authenticated_artifacts;
  ALTER TABLE privacy_activation_authenticated_artifacts ENABLE TRIGGER privacy_activation_authenticated_artifacts_immutable;
  SELECT pg_get_constraintdef(oid) INTO d FROM pg_constraint WHERE conrelid='privacy_activation_authenticated_artifacts'::regclass AND conname='privacy_activation_authenticated_artifacts_v18_check';
  IF strpos(d,'202609170002_privacy_executor_retention_handlers')=0 THEN RAISE EXCEPTION 'executor retention rewind mismatch'; END IF;
  d:=replace(d,', ''202609170002_privacy_executor_retention_handlers''::text','');
  ALTER TABLE privacy_activation_authenticated_artifacts DROP CONSTRAINT privacy_activation_authenticated_artifacts_v18_check;
  EXECUTE 'ALTER TABLE privacy_activation_authenticated_artifacts ADD CONSTRAINT privacy_activation_authenticated_artifacts_v17_check '||d;
  EXECUTE replace(pg_get_functiondef('privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)'::regprocedure),'202609170002_privacy_executor_retention_handlers','202609170001_privacy_synthetic_acceptance');
  EXECUTE replace(pg_get_functiondef('privacy_activation_authenticated_set_digest(text,uuid[])'::regprocedure),'202609170002_privacy_executor_retention_handlers','202609170001_privacy_synthetic_acceptance');
 END IF;
 IF EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='privacy_activation_authenticated_artifacts'::regclass AND conname='privacy_activation_authenticated_artifacts_v17_check') THEN
  ALTER TABLE privacy_activation_authenticated_artifacts DISABLE TRIGGER privacy_activation_authenticated_artifacts_immutable;
  DELETE FROM privacy_activation_authenticated_artifacts;
  ALTER TABLE privacy_activation_authenticated_artifacts ENABLE TRIGGER privacy_activation_authenticated_artifacts_immutable;
  SELECT pg_get_constraintdef(oid) INTO d FROM pg_constraint WHERE conrelid='privacy_activation_authenticated_artifacts'::regclass AND conname='privacy_activation_authenticated_artifacts_v17_check';
  IF strpos(d,'202609170001_privacy_synthetic_acceptance')=0 THEN RAISE EXCEPTION 'synthetic rewind mismatch'; END IF;
  d:=replace(d,', ''202609170001_privacy_synthetic_acceptance''::text','');
  ALTER TABLE privacy_activation_authenticated_artifacts DROP CONSTRAINT privacy_activation_authenticated_artifacts_v17_check;
  EXECUTE 'ALTER TABLE privacy_activation_authenticated_artifacts ADD CONSTRAINT privacy_activation_authenticated_artifacts_v16_check '||d;
  EXECUTE replace(pg_get_functiondef('privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)'::regprocedure),'202609170001_privacy_synthetic_acceptance','202609130001_event_results_links');
  EXECUTE replace(pg_get_functiondef('privacy_activation_authenticated_set_digest(text,uuid[])'::regprocedure),'202609170001_privacy_synthetic_acceptance','202609130001_event_results_links');
  EXECUTE replace(pg_get_functiondef('privacy_worker_claim_inner_013(bigint,uuid)'::regprocedure),'NOT EXISTS(SELECT 1 FROM public.privacy_erasure_executions execution JOIN privacy_protected.acceptance_requests synthetic ON synthetic.request_id=execution.request_id WHERE execution.id=job.execution_id) AND ','');
  EXECUTE replace(pg_get_functiondef('privacy_completion_list_pending_inner_013(uuid,integer)'::regprocedure),'NOT EXISTS(SELECT 1 FROM privacy_protected.acceptance_requests fixture WHERE fixture.request_id=execution.request_id) AND ','');
  EXECUTE replace(pg_get_functiondef('privacy_worker_status()'::regprocedure),' WHERE NOT EXISTS(SELECT 1 FROM privacy_erasure_executions execution JOIN privacy_protected.acceptance_requests synthetic ON synthetic.request_id=execution.request_id WHERE execution.id=job.execution_id)','');
  DROP FUNCTION privacy_acceptance_bind_request() CASCADE;
  DROP FUNCTION privacy_acceptance_reject_auth() CASCADE;
  DROP FUNCTION privacy_acceptance_simulate_notice() CASCADE;
  DROP FUNCTION privacy_protected.acceptance_create(text,text,text),privacy_protected.acceptance_finish(uuid,bytea),privacy_protected.acceptance_observe(uuid,bytea);
  DROP FUNCTION privacy_acceptance_claim(bigint,uuid,bytea),privacy_acceptance_claim_inner(bigint,uuid,uuid),privacy_acceptance_authorized(uuid,bytea);
  DROP TABLE privacy_protected.acceptance_notice_simulations,privacy_protected.acceptance_requests,privacy_protected.acceptance_finished,privacy_protected.acceptance_fixtures;
 END IF;
 END$$`)
	if err != nil {
		t.Fatal(err)
	}
}
