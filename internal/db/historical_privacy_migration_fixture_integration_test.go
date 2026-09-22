//go:build integration

package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

// rewindExecutorAndSyntheticAcceptanceBinding reconstructs a retired historical
// activation inventory solely for predecessor-migration integration fixtures.
func rewindExecutorAndSyntheticAcceptanceBinding(t *testing.T, ctx context.Context, tx pgx.Tx) {
	t.Helper()
	_, err := tx.Exec(ctx, `DO $$DECLARE d text;BEGIN
 IF EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='privacy_activation_authenticated_artifacts'::regclass AND conname='privacy_activation_authenticated_artifacts_v21_check') THEN
  ALTER TABLE privacy_activation_authenticated_artifacts DISABLE TRIGGER privacy_activation_authenticated_artifacts_immutable;
  DELETE FROM privacy_activation_authenticated_artifacts;
  ALTER TABLE privacy_activation_authenticated_artifacts ENABLE TRIGGER privacy_activation_authenticated_artifacts_immutable;
  SELECT pg_get_constraintdef(oid) INTO d FROM pg_constraint WHERE conrelid='privacy_activation_authenticated_artifacts'::regclass AND conname='privacy_activation_authenticated_artifacts_v21_check';
  IF strpos(d,'202609170005_privacy_activation_dual_signer')=0 THEN RAISE EXCEPTION 'dual signer rewind mismatch'; END IF;
  d:=replace(d,', ''202609170005_privacy_activation_dual_signer''::text','');
  ALTER TABLE privacy_activation_authenticated_artifacts DROP CONSTRAINT privacy_activation_authenticated_artifacts_v21_check;
  EXECUTE 'ALTER TABLE privacy_activation_authenticated_artifacts ADD CONSTRAINT privacy_activation_authenticated_artifacts_v20_check '||d;
  EXECUTE replace(pg_get_functiondef('privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)'::regprocedure),'202609170005_privacy_activation_dual_signer','202609170004_privacy_empty_provider_execution');
  EXECUTE replace(pg_get_functiondef('privacy_activation_authenticated_set_digest(text,uuid[])'::regprocedure),'202609170005_privacy_activation_dual_signer','202609170004_privacy_empty_provider_execution');
 END IF;
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
