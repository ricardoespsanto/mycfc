-- #248 release guard: fail the privacy erasure executor closed at the
-- database boundary and bind
-- activation to authenticated, release-specific evidence. The switch starts
-- engaged deliberately; only a distinct approval of a complete current
-- evidence set may clear it.

ALTER TABLE privacy_activation_evidence DROP CONSTRAINT privacy_activation_evidence_check;
ALTER TABLE privacy_activation_evidence ADD CONSTRAINT privacy_activation_evidence_expiry_bounded
 CHECK(expires_at>observed_at AND expires_at<=observed_at+interval '2160 hours') NOT VALID;

CREATE TABLE privacy_worker_kill_switch (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
 engaged boolean NOT NULL DEFAULT true,
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 activation_approval_id uuid NULL REFERENCES privacy_activation_approvals(id) ON DELETE RESTRICT,
 changed_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
INSERT INTO privacy_worker_kill_switch(singleton,engaged) VALUES(true,true);

CREATE TABLE privacy_worker_kill_switch_events (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 version bigint NOT NULL UNIQUE CHECK(version>0),
 engaged boolean NOT NULL,
 activation_approval_id uuid NULL REFERENCES privacy_activation_approvals(id) ON DELETE RESTRICT,
 occurred_at timestamptz NOT NULL
);
INSERT INTO privacy_worker_kill_switch_events(version,engaged,occurred_at) VALUES(1,true,clock_timestamp());
CREATE TRIGGER privacy_worker_kill_switch_events_immutable BEFORE UPDATE OR DELETE ON privacy_worker_kill_switch_events
 FOR EACH ROW EXECUTE FUNCTION prevent_privacy_audit_mutation();

CREATE TABLE privacy_activation_authenticated_artifacts (
 evidence_id uuid PRIMARY KEY REFERENCES privacy_activation_evidence(id) ON DELETE RESTRICT,
 kind text NOT NULL CHECK(kind IN ('RESTORE','INFRASTRUCTURE','PROVIDER','SCHEMA')),
 policy_version text NOT NULL REFERENCES privacy_request_policies(version) ON DELETE RESTRICT,
 executor_version text NOT NULL CHECK(executor_version='privacy-erasure-executor/v2'),
 plan_schema_version text NOT NULL CHECK(plan_schema_version='privacy-erasure-plan/v2'),
 image_digest text NOT NULL CHECK(image_digest~'^sha256:[0-9a-f]{64}$'),
 immutable_evidence_ref text NULL,
 immutable_evidence_sha256 bytea NOT NULL CHECK(octet_length(immutable_evidence_sha256)=32),
 signing_key_id text NULL,
 schema_migration_digest bytea NULL CHECK(schema_migration_digest IS NULL OR octet_length(schema_migration_digest)=32),
 baseline_includes_through text NULL,
 production_state_serial bigint NULL,
 hetzner_state_serial bigint NULL,
 production_state_sha256 bytea NULL,
 hetzner_state_sha256 bytea NULL,
 production_plan_sha256 bytea NULL,
 hetzner_plan_sha256 bytea NULL,
 worker_identity_enabled boolean NULL,
 s3_version_deletion_enabled boolean NULL,
 ledger_broker_invoke_enabled boolean NULL,
 worker_monitoring_enabled boolean NULL,
 restore_infrastructure_enabled boolean NULL,
 restore_ledger_write_enabled boolean NULL,
 provider_registry_state text NULL,
 provider_registration_count bigint NULL,
 provider_registry_sha256 bytea NULL,
 restore_input_source text NULL,
 restore_input_contract text NULL,
 restore_replay_contract text NULL,
 restore_closure_contract text NULL,
 restore_candidate_sha256 bytea NULL,
 restore_inventory_sha256 bytea NULL,
 restore_object_count bigint NULL,
 restore_replayed_count bigint NULL,
 restore_synthetic_count bigint NULL,
 restore_observer_sha256 bytea NULL,
 authenticated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(evidence_id,kind),
 CHECK(((kind='RESTORE' AND signing_key_id IS NULL AND immutable_evidence_ref IS NOT NULL
        AND schema_migration_digest IS NOT NULL AND baseline_includes_through IS NULL
        AND restore_input_source IN ('LIVE_LEDGER','SYNTHETIC_BOOTSTRAP') AND restore_input_contract='mycfc/privacy-restore-ledger-input/v2'
        AND restore_replay_contract='relational-erasure-replay/v1' AND restore_closure_contract='restore-tombstone-closure/v3'
        AND restore_candidate_sha256 IS NOT NULL AND octet_length(restore_candidate_sha256)=32
        AND restore_inventory_sha256 IS NOT NULL AND octet_length(restore_inventory_sha256)=32
        AND restore_observer_sha256 IS NOT NULL AND octet_length(restore_observer_sha256)=32
        AND restore_object_count>=restore_replayed_count AND restore_replayed_count>0
        AND ((restore_input_source='LIVE_LEDGER' AND restore_synthetic_count=0)
          OR (restore_input_source='SYNTHETIC_BOOTSTRAP' AND restore_synthetic_count=restore_replayed_count)))
    OR (kind='INFRASTRUCTURE' AND signing_key_id IS NOT NULL AND immutable_evidence_ref IS NOT NULL
        AND production_state_serial>0 AND hetzner_state_serial>0
        AND octet_length(production_state_sha256)=32 AND octet_length(hetzner_state_sha256)=32
        AND octet_length(production_plan_sha256)=32 AND octet_length(hetzner_plan_sha256)=32
        AND worker_identity_enabled AND s3_version_deletion_enabled AND ledger_broker_invoke_enabled
        AND worker_monitoring_enabled AND restore_infrastructure_enabled AND restore_ledger_write_enabled)
    OR (kind='PROVIDER' AND signing_key_id IS NOT NULL AND immutable_evidence_ref IS NOT NULL
        AND provider_registry_state='READY' AND provider_registration_count>0 AND octet_length(provider_registry_sha256)=32)
    OR (kind='SCHEMA' AND signing_key_id IS NOT NULL AND immutable_evidence_ref IS NOT NULL
        AND octet_length(schema_migration_digest)=32
        AND baseline_includes_through='202609100013_privacy_worker_release_guard')) IS TRUE)
);
CREATE TRIGGER privacy_activation_authenticated_artifacts_immutable BEFORE UPDATE OR DELETE ON privacy_activation_authenticated_artifacts
 FOR EACH ROW EXECUTE FUNCTION prevent_privacy_audit_mutation();

-- Existing evidence was accepted without signature and release bindings. It is
-- retained as audit history but cannot be used by the v2 activation contract.
CREATE OR REPLACE FUNCTION privacy_activation_record_evidence(p_actor uuid,p_kind text,p_evidence_sha256 bytea,p_reference_code text,p_observed_at timestamptz)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
 RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_activation_authenticated_evidence_required';
END;$$;

-- Correct the restore verifier without weakening historical preservation.
-- Active membership revocation is an effective-date boundary: strictly
-- historical rows remain until the following anonymization operation. The
-- history verifier then requires every direct subject link and every unique
-- subject token in variation text/JSON to be gone. This same verifier is used
-- for ordinary replay and the already-applied proof path.
ALTER FUNCTION privacy_restore_verify_operation(uuid,text,boolean) RENAME TO privacy_restore_verify_operation_inner_013;
CREATE OR REPLACE FUNCTION privacy_restore_verify_operation(p_run_id uuid,p_operation_code text,p_source_already_applied boolean)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE imported privacy_protected.restore_ledger_imports%ROWTYPE;subject_email text;subject_login text;
BEGIN
 IF p_operation_code NOT IN ('MEMBERSHIP_ACTIVE_REVOKE','MEMBERSHIP_HISTORY_ANONYMIZE') THEN
  PERFORM privacy_restore_verify_operation_inner_013(p_run_id,p_operation_code,p_source_already_applied);
  RETURN;
 END IF;
 SELECT imported_row.* INTO imported FROM privacy_protected.restore_replay_runs run
 JOIN privacy_protected.restore_ledger_imports imported_row ON imported_row.id=run.import_id WHERE run.id=p_run_id;
 IF imported.id IS NULL OR imported.kind<>'closure' OR imported.closure_version<>'restore-tombstone-closure/v3'
  OR NOT(p_operation_code=ANY(imported.operations)) THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 IF p_source_already_applied AND NOT EXISTS(SELECT 1 FROM users WHERE id=imported.subject_user_id
  AND erasure_execution_id=imported.source_execution_id AND erased_at=imported.erasure_effective_at) THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 SELECT email::text,minor_login_id INTO subject_email,subject_login FROM users WHERE id=imported.subject_user_id;
 IF p_operation_code='MEMBERSHIP_ACTIVE_REVOKE' THEN
  IF EXISTS(SELECT 1 FROM user_memberships WHERE user_id=imported.subject_user_id
   AND (starts_on>=imported.erasure_effective_at::date OR ends_on IS NULL OR ends_on>=imported.erasure_effective_at::date)) THEN
   RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 ELSE
  IF EXISTS(SELECT 1 FROM user_memberships WHERE user_id=imported.subject_user_id)
   OR EXISTS(SELECT 1 FROM training_variations variation
    WHERE variation.change_summary IS DISTINCT FROM privacy_scrub_audit_text(variation.change_summary,imported.subject_user_id,NULL,subject_email,subject_login)
     OR variation.patch IS DISTINCT FROM privacy_scrub_audit_json(variation.patch,imported.subject_user_id,NULL,subject_email,subject_login)) THEN
   RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 END IF;
END;$$;

-- Reject intent and legacy closure inventories before a replay run or a
-- checkpoint can be created/mutated. Older formats remain readable only.
ALTER FUNCTION privacy_restore_begin_replay_hardened(uuid,uuid) RENAME TO privacy_restore_begin_replay_hardened_inner_013;
CREATE OR REPLACE FUNCTION privacy_restore_begin_replay_hardened(p_import_id uuid,p_worker_ref uuid)
RETURNS TABLE(run_id uuid,outcome_code text) LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM privacy_protected.restore_ledger_imports imported
  WHERE imported.id=p_import_id AND imported.kind='closure' AND imported.closure_version='restore-tombstone-closure/v3') THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_replay_rejected'; END IF;
 RETURN QUERY SELECT * FROM privacy_restore_begin_replay_hardened_inner_013(p_import_id,p_worker_ref);
END;$$;
ALTER FUNCTION privacy_restore_execute_checkpoint(uuid,uuid,smallint,text,text,bytea) RENAME TO privacy_restore_execute_checkpoint_inner_013;
CREATE OR REPLACE FUNCTION privacy_restore_execute_checkpoint(p_run_id uuid,p_worker_ref uuid,p_operation_position smallint,p_operation_code text,p_action_version text,p_prescription_sha256 bytea)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM privacy_protected.restore_replay_runs run
  JOIN privacy_protected.restore_ledger_imports imported ON imported.id=run.import_id
  WHERE run.id=p_run_id AND imported.kind='closure' AND imported.closure_version='restore-tombstone-closure/v3') THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_checkpoint_rejected'; END IF;
 RETURN privacy_restore_execute_checkpoint_inner_013(p_run_id,p_worker_ref,p_operation_position,p_operation_code,p_action_version,p_prescription_sha256);
END;$$;
REVOKE ALL ON FUNCTION privacy_restore_verify_operation_inner_013(uuid,text,boolean),privacy_restore_begin_replay_hardened_inner_013(uuid,uuid),
 privacy_restore_execute_checkpoint_inner_013(uuid,uuid,smallint,text,text,bytea),privacy_restore_verify_operation(uuid,text,boolean),
 privacy_restore_begin_replay_hardened(uuid,uuid),privacy_restore_execute_checkpoint(uuid,uuid,smallint,text,text,bytea) FROM PUBLIC;

-- Retain the reviewed implementations as owner-only inner routines. The
-- public names become narrow guard wrappers so a stale executor credential
-- cannot bypass a disabled/expired activation. A disabled worker cannot renew
-- or complete an existing lease; the lease remains recoverable and expires by
-- its original clock.
ALTER FUNCTION privacy_upload_cleanup_claim(bigint,uuid) RENAME TO privacy_upload_cleanup_claim_inner_013;
ALTER FUNCTION privacy_upload_cleanup_complete(uuid,uuid,bigint,uuid,integer,integer,integer,integer,text,bytea) RENAME TO privacy_upload_cleanup_complete_inner_013;
ALTER FUNCTION privacy_upload_cleanup_fail(uuid,uuid,bigint,uuid,boolean,bigint) RENAME TO privacy_upload_cleanup_fail_inner_013;
ALTER FUNCTION privacy_worker_claim(bigint,uuid) RENAME TO privacy_worker_claim_inner_013;
ALTER FUNCTION privacy_worker_heartbeat(uuid,uuid,uuid,bigint,uuid,bigint) RENAME TO privacy_worker_heartbeat_inner_013;
ALTER FUNCTION privacy_worker_complete_job(uuid,uuid,uuid,bigint,uuid) RENAME TO privacy_worker_complete_job_inner_013;
ALTER FUNCTION privacy_worker_fail_job(uuid,uuid,uuid,bigint,uuid,text,bigint,text,text,bytea) RENAME TO privacy_worker_fail_job_inner_013;
ALTER FUNCTION privacy_worker_sync(uuid,uuid,uuid,bigint,uuid) RENAME TO privacy_worker_sync_inner_013;
ALTER FUNCTION privacy_worker_execute_checkpoint(uuid,uuid,uuid,bigint,uuid,text,text) RENAME TO privacy_worker_execute_checkpoint_inner_013;
ALTER FUNCTION privacy_worker_list_object_targets(uuid,uuid,uuid,bigint,uuid) RENAME TO privacy_worker_list_object_targets_inner_013;
ALTER FUNCTION privacy_worker_record_object_evidence(uuid,uuid,uuid,uuid,bigint,uuid,integer,integer,integer,integer,text,bytea) RENAME TO privacy_worker_record_object_evidence_inner_013;
ALTER FUNCTION privacy_worker_complete_object_checkpoint(uuid,uuid,uuid,bigint,uuid) RENAME TO privacy_worker_complete_object_checkpoint_inner_013;
ALTER FUNCTION privacy_tombstone_prepare_v2(uuid,uuid,uuid,bigint,uuid) RENAME TO privacy_tombstone_prepare_v2_inner_013;
ALTER FUNCTION privacy_tombstone_confirm_v2(uuid,uuid,uuid,bigint,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz) RENAME TO privacy_tombstone_confirm_v2_inner_013;
ALTER FUNCTION privacy_tombstone_prepare_closure_v2(uuid,uuid) RENAME TO privacy_tombstone_prepare_closure_v2_inner_013;
ALTER FUNCTION privacy_tombstone_confirm_closure_v2(uuid,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz) RENAME TO privacy_tombstone_confirm_closure_v2_inner_013;
ALTER FUNCTION privacy_tombstone_prepare_closure_v3(uuid,uuid) RENAME TO privacy_tombstone_prepare_closure_v3_inner_013;
ALTER FUNCTION privacy_tombstone_confirm_closure_v3(uuid,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz) RENAME TO privacy_tombstone_confirm_closure_v3_inner_013;
ALTER FUNCTION privacy_worker_list_provider_targets(uuid,uuid,uuid,bigint,uuid) RENAME TO privacy_worker_list_provider_targets_inner_013;
ALTER FUNCTION privacy_worker_record_provider_evidence(uuid,uuid,uuid,uuid,bigint,uuid,text,integer,text,text,text,text,text,text,text,bytea) RENAME TO privacy_worker_record_provider_evidence_inner_013;
ALTER FUNCTION privacy_worker_complete_provider_checkpoint(uuid,uuid,uuid,bigint,uuid) RENAME TO privacy_worker_complete_provider_checkpoint_inner_013;
ALTER FUNCTION privacy_completion_prepare(uuid,uuid) RENAME TO privacy_completion_prepare_inner_013;
ALTER FUNCTION privacy_completion_list_pending(uuid,integer) RENAME TO privacy_completion_list_pending_inner_013;
ALTER FUNCTION privacy_completion_finalize(uuid,uuid,bytea,bytea) RENAME TO privacy_completion_finalize_inner_013;

CREATE OR REPLACE FUNCTION privacy_upload_cleanup_claim(p_lease_milliseconds bigint,p_worker_ref uuid)
RETURNS TABLE(intent_id uuid,lease_epoch bigint,attempt_id uuid,attempt_count integer,subject_user_id uuid,provenance_actor_user_id uuid,
 source_kind text,source_ref uuid,service_code text,target_kind text,provider_contract_version text,envelope_version text,algorithm text,
 encryption_key_id text,encapsulation bytea,nonce bytea,ciphertext bytea,content_type text,size_bytes bigint,cleanup_after timestamptz,created_at timestamptz)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN QUERY SELECT * FROM privacy_upload_cleanup_claim_inner_013(p_lease_milliseconds,p_worker_ref); END;$$;
CREATE OR REPLACE FUNCTION privacy_upload_cleanup_complete(p_intent_id uuid,p_attempt_id uuid,p_lease_epoch bigint,p_worker_ref uuid,
 p_deleted_versions integer,p_deleted_markers integer,p_list_calls integer,p_stable_checks integer,p_transcript_key_id text,p_transcript_digest bytea)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); PERFORM privacy_upload_cleanup_complete_inner_013(p_intent_id,p_attempt_id,p_lease_epoch,p_worker_ref,p_deleted_versions,p_deleted_markers,p_list_calls,p_stable_checks,p_transcript_key_id,p_transcript_digest); END;$$;
CREATE OR REPLACE FUNCTION privacy_upload_cleanup_fail(p_intent_id uuid,p_attempt_id uuid,p_lease_epoch bigint,p_worker_ref uuid,p_retryable boolean,p_retry_delay_milliseconds bigint)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); PERFORM privacy_upload_cleanup_fail_inner_013(p_intent_id,p_attempt_id,p_lease_epoch,p_worker_ref,p_retryable,p_retry_delay_milliseconds); END;$$;

CREATE OR REPLACE FUNCTION privacy_worker_claim(p_lease_milliseconds bigint,p_worker_ref uuid)
RETURNS TABLE(job_id uuid,lease_id uuid,attempt_id uuid) LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN QUERY SELECT * FROM privacy_worker_claim_inner_013(p_lease_milliseconds,p_worker_ref); END;$$;
CREATE OR REPLACE FUNCTION privacy_worker_heartbeat(p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_epoch bigint,p_worker_ref uuid,p_lease_milliseconds bigint)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN privacy_worker_heartbeat_inner_013(p_job_id,p_lease_id,p_attempt_id,p_epoch,p_worker_ref,p_lease_milliseconds); END;$$;
CREATE OR REPLACE FUNCTION privacy_worker_complete_job(p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_epoch bigint,p_worker_ref uuid)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN privacy_worker_complete_job_inner_013(p_job_id,p_lease_id,p_attempt_id,p_epoch,p_worker_ref); END;$$;
CREATE OR REPLACE FUNCTION privacy_worker_fail_job(p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_epoch bigint,p_worker_ref uuid,p_classification text,p_retry_milliseconds bigint,p_stage_code text,p_failure_code text,p_diagnostic_digest bytea)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN privacy_worker_fail_job_inner_013(p_job_id,p_lease_id,p_attempt_id,p_epoch,p_worker_ref,p_classification,p_retry_milliseconds,p_stage_code,p_failure_code,p_diagnostic_digest); END;$$;
CREATE OR REPLACE FUNCTION privacy_worker_sync(p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_epoch bigint,p_worker_ref uuid)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN privacy_worker_sync_inner_013(p_job_id,p_lease_id,p_attempt_id,p_epoch,p_worker_ref); END;$$;
CREATE OR REPLACE FUNCTION privacy_worker_execute_checkpoint(p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_lease_epoch bigint,p_worker_ref uuid,p_operation_code text,p_action_version text)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN privacy_worker_execute_checkpoint_inner_013(p_job_id,p_lease_id,p_attempt_id,p_lease_epoch,p_worker_ref,p_operation_code,p_action_version); END;$$;

CREATE OR REPLACE FUNCTION privacy_worker_list_object_targets(p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_epoch bigint,p_worker_ref uuid)
RETURNS TABLE(target_id uuid,execution_id uuid,job_id uuid,checkpoint_id uuid,plan_entry_sha256 bytea,category_key text,service_code text,target_kind text,source_kind text,source_ref uuid,operation_code text,action_version text,provider_contract_version text,envelope_version text,algorithm text,encryption_key_id text,encapsulation bytea,nonce bytea,ciphertext bytea)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN QUERY SELECT * FROM privacy_worker_list_object_targets_inner_013(p_job_id,p_lease_id,p_attempt_id,p_epoch,p_worker_ref); END;$$;
CREATE OR REPLACE FUNCTION privacy_worker_record_object_evidence(p_target_id uuid,p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_epoch bigint,p_worker_ref uuid,p_deleted_versions integer,p_deleted_markers integer,p_list_calls integer,p_stable_checks integer,p_transcript_key_id text,p_transcript_digest bytea)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN privacy_worker_record_object_evidence_inner_013(p_target_id,p_job_id,p_lease_id,p_attempt_id,p_epoch,p_worker_ref,p_deleted_versions,p_deleted_markers,p_list_calls,p_stable_checks,p_transcript_key_id,p_transcript_digest); END;$$;
CREATE OR REPLACE FUNCTION privacy_worker_complete_object_checkpoint(p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_epoch bigint,p_worker_ref uuid)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN privacy_worker_complete_object_checkpoint_inner_013(p_job_id,p_lease_id,p_attempt_id,p_epoch,p_worker_ref); END;$$;

CREATE OR REPLACE FUNCTION privacy_tombstone_prepare_v2(p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_lease_epoch bigint,p_worker_ref uuid)
RETURNS TABLE(execution_id uuid,request_id uuid,request_ref uuid,subject_user_id uuid,plan_sha256 bytea,workset_sha256 bytea,execution_started_at timestamptz,replay_operations text[])
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN QUERY SELECT * FROM privacy_tombstone_prepare_v2_inner_013(p_job_id,p_lease_id,p_attempt_id,p_lease_epoch,p_worker_ref); END;$$;
CREATE OR REPLACE FUNCTION privacy_tombstone_confirm_v2(p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_lease_epoch bigint,p_worker_ref uuid,p_ledger_version text,p_encryption_key_id text,p_locator_key_id text,p_locator_digest bytea,p_object_version_id text,p_ciphertext_sha256 bytea,p_size_bytes bigint,p_written_at timestamptz,p_verified_at timestamptz)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN privacy_tombstone_confirm_v2_inner_013(p_job_id,p_lease_id,p_attempt_id,p_lease_epoch,p_worker_ref,p_ledger_version,p_encryption_key_id,p_locator_key_id,p_locator_digest,p_object_version_id,p_ciphertext_sha256,p_size_bytes,p_written_at,p_verified_at); END;$$;
CREATE OR REPLACE FUNCTION privacy_tombstone_prepare_closure_v2(p_execution_id uuid,p_worker_ref uuid)
RETURNS TABLE(execution_id uuid,request_id uuid,request_ref uuid,subject_user_id uuid,plan_sha256 bytea,workset_sha256 bytea,execution_started_at timestamptz,closed_at timestamptz,evidence_expires_at timestamptz,replay_operations text[])
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN QUERY SELECT * FROM privacy_tombstone_prepare_closure_v2_inner_013(p_execution_id,p_worker_ref); END;$$;
CREATE OR REPLACE FUNCTION privacy_tombstone_confirm_closure_v2(p_execution_id uuid,p_worker_ref uuid,p_ledger_version text,p_encryption_key_id text,p_locator_key_id text,p_locator_digest bytea,p_object_version_id text,p_ciphertext_sha256 bytea,p_size_bytes bigint,p_written_at timestamptz,p_verified_at timestamptz)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN privacy_tombstone_confirm_closure_v2_inner_013(p_execution_id,p_worker_ref,p_ledger_version,p_encryption_key_id,p_locator_key_id,p_locator_digest,p_object_version_id,p_ciphertext_sha256,p_size_bytes,p_written_at,p_verified_at); END;$$;
CREATE OR REPLACE FUNCTION privacy_tombstone_prepare_closure_v3(p_execution_id uuid,p_worker_ref uuid)
RETURNS TABLE(execution_id uuid,request_id uuid,request_ref uuid,subject_user_id uuid,plan_sha256 bytea,workset_sha256 bytea,execution_started_at timestamptz,closed_at timestamptz,evidence_expires_at timestamptz,erasure_effective_at timestamptz,replay_operations text[])
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN QUERY SELECT * FROM privacy_tombstone_prepare_closure_v3_inner_013(p_execution_id,p_worker_ref); END;$$;
CREATE OR REPLACE FUNCTION privacy_tombstone_confirm_closure_v3(p_execution_id uuid,p_worker_ref uuid,p_ledger_version text,p_encryption_key_id text,p_locator_key_id text,p_locator_digest bytea,p_object_version_id text,p_ciphertext_sha256 bytea,p_size_bytes bigint,p_written_at timestamptz,p_verified_at timestamptz)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN privacy_tombstone_confirm_closure_v3_inner_013(p_execution_id,p_worker_ref,p_ledger_version,p_encryption_key_id,p_locator_key_id,p_locator_digest,p_object_version_id,p_ciphertext_sha256,p_size_bytes,p_written_at,p_verified_at); END;$$;

CREATE OR REPLACE FUNCTION privacy_worker_list_provider_targets(p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_epoch bigint,p_worker_ref uuid)
RETURNS TABLE(target_id uuid,execution_id uuid,job_id uuid,checkpoint_id uuid,plan_entry_sha256 bytea,category_key text,service_code text,target_kind text,provider_role text,target_version bigint,operation_code text,action_version text,provider_contract_version text,registry_evidence_key_id text,registry_evidence_digest bytea,local_state text,target_envelope_version text,target_algorithm text,target_encryption_key_id text,target_encapsulation bytea,target_nonce bytea,target_ciphertext bytea,credential_envelope_version text,credential_algorithm text,credential_encryption_key_id text,credential_encapsulation bytea,credential_nonce bytea,credential_ciphertext bytea,credential_commitment_key_id text,credential_source_commitment bytea)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN QUERY SELECT * FROM privacy_worker_list_provider_targets_inner_013(p_job_id,p_lease_id,p_attempt_id,p_epoch,p_worker_ref); END;$$;
CREATE OR REPLACE FUNCTION privacy_worker_record_provider_evidence(p_target_id uuid,p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_epoch bigint,p_worker_ref uuid,p_outcome_code text,p_adapter_attempts integer,p_evidence_code text,p_recipient_role text,p_channel_code text,p_notification_code text,p_reason_code text,p_guidance_code text,p_transcript_key_id text,p_transcript_digest bytea)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN privacy_worker_record_provider_evidence_inner_013(p_target_id,p_job_id,p_lease_id,p_attempt_id,p_epoch,p_worker_ref,p_outcome_code,p_adapter_attempts,p_evidence_code,p_recipient_role,p_channel_code,p_notification_code,p_reason_code,p_guidance_code,p_transcript_key_id,p_transcript_digest); END;$$;
CREATE OR REPLACE FUNCTION privacy_worker_complete_provider_checkpoint(p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_epoch bigint,p_worker_ref uuid)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN privacy_worker_complete_provider_checkpoint_inner_013(p_job_id,p_lease_id,p_attempt_id,p_epoch,p_worker_ref); END;$$;

CREATE OR REPLACE FUNCTION privacy_completion_prepare(p_execution_id uuid,p_worker_ref uuid) RETURNS TABLE(sealed_delivery bytea)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN QUERY SELECT * FROM privacy_completion_prepare_inner_013(p_execution_id,p_worker_ref); END;$$;
CREATE OR REPLACE FUNCTION privacy_completion_list_pending(p_worker_ref uuid,p_limit integer) RETURNS TABLE(execution_id uuid)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN QUERY SELECT * FROM privacy_completion_list_pending_inner_013(p_worker_ref,p_limit); END;$$;
CREATE OR REPLACE FUNCTION privacy_completion_finalize(p_execution_id uuid,p_worker_ref uuid,p_token_sha256 bytea,p_sealed_delivery bytea)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN privacy_completion_finalize_inner_013(p_execution_id,p_worker_ref,p_token_sha256,p_sealed_delivery); END;$$;

REVOKE ALL ON TABLE privacy_worker_kill_switch,privacy_worker_kill_switch_events,privacy_activation_authenticated_artifacts FROM PUBLIC;
REVOKE ALL ON FUNCTION privacy_upload_cleanup_claim_inner_013(bigint,uuid),privacy_upload_cleanup_complete_inner_013(uuid,uuid,bigint,uuid,integer,integer,integer,integer,text,bytea),
 privacy_upload_cleanup_fail_inner_013(uuid,uuid,bigint,uuid,boolean,bigint),privacy_worker_claim_inner_013(bigint,uuid),privacy_worker_heartbeat_inner_013(uuid,uuid,uuid,bigint,uuid,bigint),
 privacy_worker_complete_job_inner_013(uuid,uuid,uuid,bigint,uuid),privacy_worker_fail_job_inner_013(uuid,uuid,uuid,bigint,uuid,text,bigint,text,text,bytea),
 privacy_worker_sync_inner_013(uuid,uuid,uuid,bigint,uuid),privacy_worker_execute_checkpoint_inner_013(uuid,uuid,uuid,bigint,uuid,text,text),
 privacy_worker_list_object_targets_inner_013(uuid,uuid,uuid,bigint,uuid),privacy_worker_record_object_evidence_inner_013(uuid,uuid,uuid,uuid,bigint,uuid,integer,integer,integer,integer,text,bytea),
 privacy_worker_complete_object_checkpoint_inner_013(uuid,uuid,uuid,bigint,uuid),privacy_tombstone_prepare_v2_inner_013(uuid,uuid,uuid,bigint,uuid),
 privacy_tombstone_confirm_v2_inner_013(uuid,uuid,uuid,bigint,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz),privacy_tombstone_prepare_closure_v2_inner_013(uuid,uuid),
 privacy_tombstone_confirm_closure_v2_inner_013(uuid,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz),privacy_tombstone_prepare_closure_v3_inner_013(uuid,uuid),
 privacy_tombstone_confirm_closure_v3_inner_013(uuid,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz),privacy_worker_list_provider_targets_inner_013(uuid,uuid,uuid,bigint,uuid),
 privacy_worker_record_provider_evidence_inner_013(uuid,uuid,uuid,uuid,bigint,uuid,text,integer,text,text,text,text,text,text,text,bytea),privacy_worker_complete_provider_checkpoint_inner_013(uuid,uuid,uuid,bigint,uuid),
 privacy_completion_prepare_inner_013(uuid,uuid),privacy_completion_list_pending_inner_013(uuid,integer),privacy_completion_finalize_inner_013(uuid,uuid,bytea,bytea) FROM PUBLIC;
REVOKE ALL ON FUNCTION privacy_upload_cleanup_claim(bigint,uuid),privacy_upload_cleanup_complete(uuid,uuid,bigint,uuid,integer,integer,integer,integer,text,bytea),
 privacy_upload_cleanup_fail(uuid,uuid,bigint,uuid,boolean,bigint),privacy_worker_claim(bigint,uuid),privacy_worker_heartbeat(uuid,uuid,uuid,bigint,uuid,bigint),privacy_worker_complete_job(uuid,uuid,uuid,bigint,uuid),
 privacy_worker_fail_job(uuid,uuid,uuid,bigint,uuid,text,bigint,text,text,bytea),privacy_worker_sync(uuid,uuid,uuid,bigint,uuid),privacy_worker_execute_checkpoint(uuid,uuid,uuid,bigint,uuid,text,text),
 privacy_worker_list_object_targets(uuid,uuid,uuid,bigint,uuid),privacy_worker_record_object_evidence(uuid,uuid,uuid,uuid,bigint,uuid,integer,integer,integer,integer,text,bytea),privacy_worker_complete_object_checkpoint(uuid,uuid,uuid,bigint,uuid),
 privacy_tombstone_prepare_v2(uuid,uuid,uuid,bigint,uuid),privacy_tombstone_confirm_v2(uuid,uuid,uuid,bigint,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz),privacy_tombstone_prepare_closure_v2(uuid,uuid),
 privacy_tombstone_confirm_closure_v2(uuid,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz),privacy_tombstone_prepare_closure_v3(uuid,uuid),
 privacy_tombstone_confirm_closure_v3(uuid,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz),privacy_worker_list_provider_targets(uuid,uuid,uuid,bigint,uuid),
 privacy_worker_record_provider_evidence(uuid,uuid,uuid,uuid,bigint,uuid,text,integer,text,text,text,text,text,text,text,bytea),privacy_worker_complete_provider_checkpoint(uuid,uuid,uuid,bigint,uuid),
 privacy_completion_prepare(uuid,uuid),privacy_completion_list_pending(uuid,integer),privacy_completion_finalize(uuid,uuid,bytea,bytea) FROM PUBLIC;

-- The migration deliberately disables any older activation. This also keeps
-- the switch engaged until a v2 evidence proposal is approved.
UPDATE privacy_request_activation SET enabled=false,fulfilment_ready=false,updated_at=clock_timestamp() WHERE singleton;

CREATE OR REPLACE FUNCTION privacy_activation_authenticated_set_digest(p_policy_version text,p_evidence_ids uuid[])
RETURNS bytea LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE policy privacy_request_policies%ROWTYPE;now_at timestamptz:=clock_timestamp();result bytea;
BEGIN
 SELECT * INTO policy FROM privacy_request_policies WHERE version=p_policy_version;
 IF policy.version IS NULL OR policy.adopted_at IS NULL OR policy.working_retention_days<>90 OR cardinality(p_evidence_ids)<>4
  OR policy.executor_version<>'privacy-erasure-executor/v2' OR policy.plan_schema_version<>'privacy-erasure-plan/v2'
  OR (SELECT count(*) FROM privacy_activation_evidence evidence JOIN privacy_activation_authenticated_artifacts artifact ON artifact.evidence_id=evidence.id
      WHERE evidence.id=ANY(p_evidence_ids) AND evidence.expires_at>now_at AND artifact.policy_version=policy.version
       AND artifact.executor_version=policy.executor_version AND artifact.plan_schema_version=policy.plan_schema_version)<>4
  OR (SELECT count(DISTINCT evidence.kind) FROM privacy_activation_evidence evidence JOIN privacy_activation_authenticated_artifacts artifact ON artifact.evidence_id=evidence.id
      WHERE evidence.id=ANY(p_evidence_ids) AND evidence.expires_at>now_at)<>4
  OR (SELECT count(DISTINCT artifact.image_digest) FROM privacy_activation_authenticated_artifacts artifact WHERE artifact.evidence_id=ANY(p_evidence_ids))<>1
  OR (SELECT restore.schema_migration_digest IS DISTINCT FROM schema_row.schema_migration_digest
      FROM privacy_activation_authenticated_artifacts restore CROSS JOIN privacy_activation_authenticated_artifacts schema_row
      WHERE restore.evidence_id=ANY(p_evidence_ids) AND restore.kind='RESTORE' AND schema_row.evidence_id=ANY(p_evidence_ids) AND schema_row.kind='SCHEMA')
 THEN RETURN NULL; END IF;
 SELECT digest(convert_to(string_agg(evidence.kind||':'||encode(evidence.evidence_sha256,'hex')||':'||
   encode(digest(convert_to((to_jsonb(artifact)-'authenticated_at')::text,'UTF8'),'sha256'),'hex')||':'||
   to_char(evidence.observed_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"')||':'||
   to_char(evidence.expires_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),'|' ORDER BY evidence.kind),'UTF8'),'sha256')
 INTO result FROM privacy_activation_evidence evidence JOIN privacy_activation_authenticated_artifacts artifact ON artifact.evidence_id=evidence.id
 WHERE evidence.id=ANY(p_evidence_ids);
 RETURN result;
END;$$;

REVOKE ALL ON FUNCTION privacy_activation_authenticated_set_digest(text,uuid[]) FROM PUBLIC;

CREATE OR REPLACE FUNCTION privacy_activation_propose(p_actor uuid,p_policy_version text,p_evidence_ids uuid[])
RETURNS TABLE(proposal_id uuid,activation_sha256 bytea) LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE policy privacy_request_policies%ROWTYPE;evidence_digest bytea;activation_digest bytea;now_at timestamptz:=clock_timestamp();ordered_ids uuid[];
BEGIN
 IF NOT EXISTS(SELECT 1 FROM users account JOIN privacy_executor_grants grant_row ON grant_row.user_id=account.id AND grant_row.revoked_at IS NULL
   WHERE account.id=p_actor AND account.is_active AND NOT account.is_dependent) THEN
  RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_activation_forbidden'; END IF;
 SELECT * INTO policy FROM privacy_request_policies WHERE version=p_policy_version FOR SHARE;
 evidence_digest:=privacy_activation_authenticated_set_digest(p_policy_version,p_evidence_ids);
 IF evidence_digest IS NULL THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_activation_evidence_incomplete'; END IF;
 SELECT array_agg(evidence.id ORDER BY evidence.kind) INTO ordered_ids FROM privacy_activation_evidence evidence WHERE evidence.id=ANY(p_evidence_ids);
 activation_digest:=digest(convert_to('privacy-activation/v2:'||policy.version||':'||policy.executor_version||':'||policy.plan_schema_version||':'||
  encode(digest(convert_to(policy.category_catalogue::text,'UTF8'),'sha256'),'hex')||':'||encode(evidence_digest,'hex'),'UTF8'),'sha256');
 RETURN QUERY INSERT INTO privacy_activation_proposals(policy_version,evidence_ids,evidence_set_sha256,activation_sha256,proposed_by_ref,proposed_at)
 VALUES(policy.version,ordered_ids,evidence_digest,activation_digest,p_actor,now_at) RETURNING id,privacy_activation_proposals.activation_sha256;
END;$$;

CREATE OR REPLACE FUNCTION privacy_activation_approve(p_actor uuid,p_proposal_id uuid,p_activation_sha256 bytea)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE proposal privacy_activation_proposals%ROWTYPE;approval_id uuid;now_at timestamptz:=clock_timestamp();recomputed bytea;switch_version bigint;
BEGIN
 IF NOT EXISTS(SELECT 1 FROM users account JOIN user_platform_roles assignment ON assignment.user_id=account.id JOIN platform_roles role ON role.id=assignment.role_id
   WHERE account.id=p_actor AND account.is_active AND NOT account.is_dependent AND role.code='ADMIN') THEN
  RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_activation_forbidden'; END IF;
 SELECT * INTO proposal FROM privacy_activation_proposals WHERE id=p_proposal_id FOR SHARE;
 recomputed:=privacy_activation_authenticated_set_digest(proposal.policy_version,proposal.evidence_ids);
 IF proposal.id IS NULL OR proposal.proposed_by_ref=p_actor OR proposal.activation_sha256<>p_activation_sha256
  OR EXISTS(SELECT 1 FROM privacy_activation_approvals approval WHERE approval.proposal_id=proposal.id)
  OR recomputed IS NULL OR recomputed<>proposal.evidence_set_sha256 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_activation_unavailable'; END IF;
 INSERT INTO privacy_activation_approvals(proposal_id,activation_sha256,approved_by_ref,approved_at)
 VALUES(proposal.id,proposal.activation_sha256,p_actor,now_at) RETURNING id INTO approval_id;
 PERFORM set_config('mycfc.privacy_activation_approval','approved',true);
 INSERT INTO privacy_request_activation(singleton,policy_version,enabled,fulfilment_ready,updated_by,updated_at,approval_id)
 VALUES(true,proposal.policy_version,true,true,p_actor,now_at,approval_id)
 ON CONFLICT(singleton) DO UPDATE SET policy_version=EXCLUDED.policy_version,enabled=true,fulfilment_ready=true,updated_by=EXCLUDED.updated_by,updated_at=EXCLUDED.updated_at,approval_id=EXCLUDED.approval_id;
 INSERT INTO privacy_request_activation_events(policy_version,actor_ref,enabled,fulfilment_ready,occurred_at)
 VALUES(proposal.policy_version,p_actor,true,true,now_at);
 UPDATE privacy_worker_kill_switch SET engaged=false,version=version+1,activation_approval_id=approval_id,changed_at=now_at
 WHERE singleton RETURNING version INTO switch_version;
 INSERT INTO privacy_worker_kill_switch_events(version,engaged,activation_approval_id,occurred_at) VALUES(switch_version,false,approval_id,now_at);
 RETURN approval_id;
END;$$;

CREATE OR REPLACE FUNCTION privacy_activation_ready(p_policy_version text)
RETURNS boolean LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT EXISTS(
  SELECT 1 FROM privacy_request_activation activation
  JOIN privacy_activation_approvals approval ON approval.id=activation.approval_id
  JOIN privacy_activation_proposals proposal ON proposal.id=approval.proposal_id
  JOIN privacy_worker_kill_switch switch_row ON switch_row.singleton AND NOT switch_row.engaged
  WHERE activation.singleton AND activation.enabled AND activation.fulfilment_ready AND activation.policy_version=p_policy_version
   AND proposal.policy_version=activation.policy_version AND approval.activation_sha256=proposal.activation_sha256
   AND approval.approved_by_ref<>proposal.proposed_by_ref
   AND privacy_activation_authenticated_set_digest(proposal.policy_version,proposal.evidence_ids)=proposal.evidence_set_sha256
 );
$$;

CREATE OR REPLACE FUNCTION privacy_worker_activation_ready()
RETURNS boolean LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT COALESCE((SELECT privacy_activation_ready(activation.policy_version) FROM privacy_request_activation activation WHERE activation.singleton),false);
$$;

CREATE OR REPLACE FUNCTION privacy_activation_control_snapshot(p_actor uuid)
RETURNS TABLE(policy_version text,ready boolean,evidence_id uuid,evidence_kind text,evidence_observed_at timestamptz,
 proposal_id uuid,proposal_sha256 bytea,proposal_created_at timestamptz,can_propose boolean,can_renew boolean,can_approve boolean)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE actor_executor boolean;actor_admin boolean;selected_policy text;pending privacy_activation_proposals%ROWTYPE;
 current_ids uuid[];current_digest bytea;renewal_evidence boolean;
BEGIN
 SELECT EXISTS(SELECT 1 FROM users account JOIN privacy_executor_grants grant_row ON grant_row.user_id=account.id AND grant_row.revoked_at IS NULL
   WHERE account.id=p_actor AND account.is_active AND NOT account.is_dependent),
  EXISTS(SELECT 1 FROM users account JOIN user_platform_roles assignment ON assignment.user_id=account.id JOIN platform_roles role ON role.id=assignment.role_id
   WHERE account.id=p_actor AND account.is_active AND NOT account.is_dependent AND role.code='ADMIN') INTO actor_executor,actor_admin;
 IF NOT actor_executor AND NOT actor_admin THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_control_forbidden'; END IF;
 SELECT COALESCE((SELECT activation.policy_version FROM privacy_request_activation activation WHERE activation.singleton AND activation.enabled),
  (SELECT policy.version FROM privacy_request_policies policy WHERE policy.adopted_at IS NOT NULL ORDER BY policy.adopted_at DESC,policy.version DESC LIMIT 1)) INTO selected_policy;
 IF selected_policy IS NULL THEN RETURN; END IF;
 SELECT array_agg(latest.id ORDER BY latest.kind) INTO current_ids FROM (
  SELECT DISTINCT ON(evidence.kind) evidence.id,evidence.kind FROM privacy_activation_evidence evidence
  JOIN privacy_activation_authenticated_artifacts artifact ON artifact.evidence_id=evidence.id AND artifact.kind=evidence.kind
  JOIN privacy_request_policies policy ON policy.version=selected_policy AND artifact.policy_version=policy.version
   AND artifact.executor_version=policy.executor_version AND artifact.plan_schema_version=policy.plan_schema_version
  WHERE evidence.expires_at>clock_timestamp() ORDER BY evidence.kind,evidence.observed_at DESC,evidence.id DESC
 ) latest;
 current_digest:=privacy_activation_authenticated_set_digest(selected_policy,current_ids);
 SELECT proposal.* INTO pending FROM privacy_activation_proposals proposal
  WHERE proposal.policy_version=selected_policy AND privacy_activation_authenticated_set_digest(proposal.policy_version,proposal.evidence_ids)=proposal.evidence_set_sha256
   AND NOT EXISTS(SELECT 1 FROM privacy_activation_approvals approval WHERE approval.proposal_id=proposal.id)
  ORDER BY proposal.proposed_at DESC,proposal.id DESC LIMIT 1;
 SELECT EXISTS(SELECT 1 FROM privacy_request_activation activation JOIN privacy_activation_approvals approval ON approval.id=activation.approval_id
  JOIN privacy_activation_proposals proposal ON proposal.id=approval.proposal_id
  WHERE activation.singleton AND activation.enabled AND activation.policy_version=selected_policy AND current_digest IS NOT NULL
   AND EXISTS(SELECT 1 FROM unnest(current_ids) AS current_id(evidence_id) WHERE NOT current_id.evidence_id=ANY(proposal.evidence_ids))) INTO renewal_evidence;
 RETURN QUERY SELECT selected_policy,privacy_activation_ready(selected_policy),evidence.id,evidence.kind::text,evidence.observed_at,
  pending.id,pending.activation_sha256,pending.proposed_at,
  (actor_executor AND current_digest IS NOT NULL AND pending.id IS NULL AND NOT privacy_activation_ready(selected_policy)),
  (actor_executor AND current_digest IS NOT NULL AND pending.id IS NULL AND privacy_activation_ready(selected_policy) AND renewal_evidence),
  (actor_admin AND pending.id IS NOT NULL AND pending.proposed_by_ref<>p_actor)
 FROM (SELECT true singleton) seed LEFT JOIN LATERAL(
  SELECT candidate.id,candidate.kind,candidate.observed_at FROM privacy_activation_evidence candidate
  WHERE candidate.id=ANY(current_ids) ORDER BY candidate.kind
 ) evidence ON true ORDER BY evidence.kind;
END;$$;

CREATE OR REPLACE FUNCTION privacy_worker_engage_kill_switch() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE switch_version bigint;
BEGIN
 IF NEW.enabled=false AND (TG_OP='INSERT' OR OLD.enabled IS DISTINCT FROM NEW.enabled) THEN
  UPDATE privacy_worker_kill_switch SET engaged=true,version=version+1,activation_approval_id=NULL,changed_at=clock_timestamp()
  WHERE singleton AND NOT engaged RETURNING version INTO switch_version;
  IF switch_version IS NOT NULL THEN
   INSERT INTO privacy_worker_kill_switch_events(version,engaged,occurred_at) VALUES(switch_version,true,clock_timestamp());
  END IF;
 END IF;
 RETURN NEW;
END;$$;
CREATE TRIGGER privacy_worker_engage_kill_switch AFTER INSERT OR UPDATE OF enabled ON privacy_request_activation
 FOR EACH ROW EXECUTE FUNCTION privacy_worker_engage_kill_switch();

CREATE OR REPLACE FUNCTION privacy_worker_require_activation() RETURNS void LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
 PERFORM 1 FROM privacy_worker_kill_switch switch_row WHERE switch_row.singleton AND NOT switch_row.engaged FOR SHARE;
 IF NOT FOUND OR NOT privacy_worker_activation_ready() THEN
  RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_worker_disabled';
 END IF;
END;$$;

CREATE OR REPLACE FUNCTION privacy_activation_record_authenticated_evidence(
 p_actor uuid,p_kind text,p_evidence_sha256 bytea,p_reference_code text,p_observed_at timestamptz,p_expires_at timestamptz,p_artifact jsonb
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE v_evidence_id uuid;now_at timestamptz:=clock_timestamp();expected_keys text[];
BEGIN
 IF NOT EXISTS(SELECT 1 FROM users account JOIN user_platform_roles assignment ON assignment.user_id=account.id
   JOIN platform_roles role ON role.id=assignment.role_id WHERE account.id=p_actor AND account.is_active AND NOT account.is_dependent AND role.code='ADMIN')
  OR p_kind NOT IN ('RESTORE','INFRASTRUCTURE','PROVIDER','SCHEMA') OR octet_length(p_evidence_sha256)<>32
  OR p_observed_at>now_at OR p_observed_at<=now_at-interval '2160 hours' OR p_expires_at<=now_at OR p_expires_at>p_observed_at+interval '2160 hours'
  OR jsonb_typeof(p_artifact)<>'object' THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy_activation_evidence_rejected'; END IF;
 expected_keys:=CASE p_kind
  WHEN 'RESTORE' THEN ARRAY['policy_version','executor_version','plan_schema_version','image_digest','evidence_ref','evidence_sha256','schema_migration_digest','restore_input_source','restore_input_contract','restore_replay_contract','restore_closure_contract','restore_candidate_sha256','restore_inventory_sha256','restore_object_count','restore_replayed_count','restore_synthetic_count','restore_observer_sha256']
  WHEN 'INFRASTRUCTURE' THEN ARRAY['policy_version','executor_version','plan_schema_version','image_digest','evidence_ref','evidence_sha256','signing_key_id','production_state_serial','hetzner_state_serial','production_state_sha256','hetzner_state_sha256','production_plan_sha256','hetzner_plan_sha256','worker_identity_enabled','s3_version_deletion_enabled','ledger_broker_invoke_enabled','worker_monitoring_enabled','restore_infrastructure_enabled','restore_ledger_write_enabled']
  WHEN 'PROVIDER' THEN ARRAY['policy_version','executor_version','plan_schema_version','image_digest','evidence_ref','evidence_sha256','signing_key_id','provider_registry_state','provider_registration_count','provider_registry_sha256']
  WHEN 'SCHEMA' THEN ARRAY['policy_version','executor_version','plan_schema_version','image_digest','evidence_ref','evidence_sha256','signing_key_id','schema_migration_digest','baseline_includes_through'] END;
 IF NOT p_artifact ?& expected_keys OR (SELECT count(*) FROM jsonb_object_keys(p_artifact))<>cardinality(expected_keys)
  OR p_artifact->>'policy_version' IS NULL OR p_artifact->>'executor_version'<>'privacy-erasure-executor/v2'
  OR p_artifact->>'plan_schema_version'<>'privacy-erasure-plan/v2' OR p_artifact->>'image_digest'!~'^sha256:[0-9a-f]{64}$' THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy_activation_artifact_rejected'; END IF;
 IF (p_kind='RESTORE' AND p_reference_code<>'mycfc/privacy-restore-drill-attestation/v2')
  OR (p_kind='INFRASTRUCTURE' AND p_reference_code<>'mycfc/privacy-infrastructure-posture/v1')
  OR (p_kind='PROVIDER' AND p_reference_code<>'mycfc/privacy-provider-registry/v1')
  OR (p_kind='SCHEMA' AND p_reference_code<>'mycfc/schema-migration-inventory/v1') THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy_activation_evidence_contract_rejected'; END IF;
 INSERT INTO privacy_activation_evidence(kind,evidence_sha256,reference_code,observed_at,expires_at,recorded_by_ref,recorded_at)
 VALUES(p_kind,p_evidence_sha256,p_reference_code,p_observed_at,p_expires_at,p_actor,now_at)
 ON CONFLICT(kind,evidence_sha256) DO NOTHING RETURNING id INTO v_evidence_id;
 IF v_evidence_id IS NULL THEN
  SELECT evidence.id INTO v_evidence_id FROM privacy_activation_evidence evidence WHERE evidence.kind=p_kind AND evidence.evidence_sha256=p_evidence_sha256
   AND evidence.reference_code=p_reference_code AND evidence.observed_at=p_observed_at AND evidence.expires_at=p_expires_at;
  IF v_evidence_id IS NULL THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_activation_evidence_conflict'; END IF;
 END IF;
 INSERT INTO privacy_activation_authenticated_artifacts(
  evidence_id,kind,policy_version,executor_version,plan_schema_version,image_digest,immutable_evidence_ref,immutable_evidence_sha256,signing_key_id,
  schema_migration_digest,baseline_includes_through,production_state_serial,hetzner_state_serial,production_state_sha256,hetzner_state_sha256,
  production_plan_sha256,hetzner_plan_sha256,worker_identity_enabled,s3_version_deletion_enabled,ledger_broker_invoke_enabled,worker_monitoring_enabled,
  restore_infrastructure_enabled,restore_ledger_write_enabled,provider_registry_state,provider_registration_count,provider_registry_sha256,restore_input_source,
  restore_input_contract,restore_replay_contract,restore_closure_contract,restore_candidate_sha256,restore_inventory_sha256,restore_object_count,restore_replayed_count,
  restore_synthetic_count,restore_observer_sha256,authenticated_at)
 VALUES(v_evidence_id,p_kind,p_artifact->>'policy_version',p_artifact->>'executor_version',p_artifact->>'plan_schema_version',p_artifact->>'image_digest',
  p_artifact->>'evidence_ref',decode(p_artifact->>'evidence_sha256','base64'),p_artifact->>'signing_key_id',decode(p_artifact->>'schema_migration_digest','base64'),
  p_artifact->>'baseline_includes_through',(p_artifact->>'production_state_serial')::bigint,(p_artifact->>'hetzner_state_serial')::bigint,
  decode(p_artifact->>'production_state_sha256','base64'),decode(p_artifact->>'hetzner_state_sha256','base64'),decode(p_artifact->>'production_plan_sha256','base64'),
  decode(p_artifact->>'hetzner_plan_sha256','base64'),(p_artifact->>'worker_identity_enabled')::boolean,(p_artifact->>'s3_version_deletion_enabled')::boolean,
  (p_artifact->>'ledger_broker_invoke_enabled')::boolean,(p_artifact->>'worker_monitoring_enabled')::boolean,(p_artifact->>'restore_infrastructure_enabled')::boolean,
  (p_artifact->>'restore_ledger_write_enabled')::boolean,p_artifact->>'provider_registry_state',(p_artifact->>'provider_registration_count')::bigint,
  decode(p_artifact->>'provider_registry_sha256','base64'),p_artifact->>'restore_input_source',p_artifact->>'restore_input_contract',p_artifact->>'restore_replay_contract',
  p_artifact->>'restore_closure_contract',decode(p_artifact->>'restore_candidate_sha256','base64'),
  decode(p_artifact->>'restore_inventory_sha256','base64'),(p_artifact->>'restore_object_count')::bigint,(p_artifact->>'restore_replayed_count')::bigint,
  (p_artifact->>'restore_synthetic_count')::bigint,decode(p_artifact->>'restore_observer_sha256','base64'),now_at)
 ON CONFLICT ON CONSTRAINT privacy_activation_authenticated_artifacts_pkey DO NOTHING;
 IF NOT EXISTS(SELECT 1 FROM privacy_activation_authenticated_artifacts authenticated WHERE authenticated.evidence_id=v_evidence_id
   AND authenticated.kind=p_kind AND authenticated.policy_version=p_artifact->>'policy_version' AND authenticated.image_digest=p_artifact->>'image_digest') THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_activation_artifact_conflict'; END IF;
 RETURN v_evidence_id;
EXCEPTION WHEN invalid_text_representation OR numeric_value_out_of_range OR check_violation THEN
 RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy_activation_artifact_rejected';
END;$$;

REVOKE ALL ON FUNCTION privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb),privacy_worker_require_activation() FROM PUBLIC;
