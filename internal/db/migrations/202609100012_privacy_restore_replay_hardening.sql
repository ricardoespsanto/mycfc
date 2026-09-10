-- #247 replay hardening: source-state verification, local provider fencing,
-- original erasure clocks, and an explicitly synthetic offline drill path.
ALTER TABLE privacy_protected.restore_ledger_imports
 ADD COLUMN erasure_effective_at timestamptz NULL,
 ADD COLUMN closure_version varchar(48) NULL,
 ADD COLUMN synthetic_fixture varchar(80) NULL,
 ADD CONSTRAINT restore_ledger_imports_synthetic_fixture_check
 CHECK(synthetic_fixture IS NULL OR synthetic_fixture='mycfc/privacy-restore-synthetic-fixture/v1') NOT VALID;
ALTER TABLE privacy_protected.restore_ledger_imports VALIDATE CONSTRAINT restore_ledger_imports_synthetic_fixture_check;
ALTER TABLE privacy_protected.restore_ledger_imports ADD CONSTRAINT restore_ledger_imports_closure_version_check
 CHECK((kind='intent' AND closure_version IS NULL) OR (kind='closure' AND closure_version IN ('restore-tombstone-closure/v2','restore-tombstone-closure/v3'))) NOT VALID;
ALTER TABLE privacy_protected.restore_ledger_imports VALIDATE CONSTRAINT restore_ledger_imports_closure_version_check;
ALTER TABLE privacy_protected.restore_tombstone_closure_receipts DROP CONSTRAINT restore_tombstone_closure_receipts_ledger_version_check;
ALTER TABLE privacy_protected.restore_tombstone_closure_receipts ADD CONSTRAINT restore_tombstone_closure_receipts_ledger_version_check
 CHECK(ledger_version IN ('restore-tombstone-closure/v1','restore-tombstone-closure/v2','restore-tombstone-closure/v3')) NOT VALID;
ALTER TABLE privacy_protected.restore_tombstone_closure_receipts VALIDATE CONSTRAINT restore_tombstone_closure_receipts_ledger_version_check;
ALTER TABLE privacy_protected.restore_ledger_imports DROP CONSTRAINT restore_ledger_imports_operations_check;
ALTER TABLE privacy_protected.restore_ledger_imports ADD CONSTRAINT restore_ledger_imports_operation_count CHECK(cardinality(operations) BETWEEN 1 AND 17) NOT VALID;
ALTER TABLE privacy_protected.restore_ledger_imports VALIDATE CONSTRAINT restore_ledger_imports_operation_count;

ALTER TABLE privacy_protected.restore_replay_runs
 ADD COLUMN outcome_code varchar(32) NULL,
 ADD CONSTRAINT restore_replay_runs_outcome_check CHECK(
  (status='PENDING' AND completed_at IS NULL AND outcome_code IS NULL)
  OR (status='SUCCEEDED' AND completed_at IS NOT NULL AND outcome_code IN ('REPLAYED','ALREADY_APPLIED_SOURCE'))
 ) NOT VALID;
UPDATE privacy_protected.restore_replay_runs SET outcome_code='REPLAYED' WHERE status='SUCCEEDED';
ALTER TABLE privacy_protected.restore_replay_runs VALIDATE CONSTRAINT restore_replay_runs_outcome_check;

CREATE TABLE privacy_protected.restore_replay_already_applied_evidence (
 run_id uuid PRIMARY KEY REFERENCES privacy_protected.restore_replay_runs(id) ON DELETE RESTRICT,
 import_id uuid NOT NULL UNIQUE REFERENCES privacy_protected.restore_ledger_imports(id) ON DELETE RESTRICT,
 evidence_code varchar(40) NOT NULL CHECK(evidence_code='ALREADY_APPLIED'),
 source_execution_id uuid NOT NULL,erasure_effective_at timestamptz NOT NULL,
 verified_operations text[] NOT NULL CHECK(cardinality(verified_operations) BETWEEN 1 AND 17),
 verification_sha256 bytea NOT NULL CHECK(octet_length(verification_sha256)=32),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TRIGGER privacy_restore_replay_already_applied_evidence_immutable BEFORE UPDATE OR DELETE
 ON privacy_protected.restore_replay_already_applied_evidence FOR EACH ROW
 EXECUTE FUNCTION public.prevent_privacy_execution_record_delete();

CREATE TABLE privacy_protected.restore_synthetic_fixtures (
 subject_user_id uuid PRIMARY KEY REFERENCES public.users(id) ON DELETE RESTRICT,
 source_execution_id uuid NOT NULL UNIQUE,source_request_id uuid NOT NULL UNIQUE,source_request_ref uuid NOT NULL UNIQUE,
 plan_sha256 bytea NOT NULL CHECK(octet_length(plan_sha256)=32),workset_sha256 bytea NOT NULL CHECK(octet_length(workset_sha256)=32),
 erasure_effective_at timestamptz NOT NULL,operations text[] NOT NULL CHECK(cardinality(operations) BETWEEN 1 AND 17),
 fixture_marker varchar(80) NOT NULL CHECK(fixture_marker='mycfc/privacy-restore-synthetic-fixture/v1'),
 created_by_ref uuid NOT NULL,created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TRIGGER privacy_restore_synthetic_fixtures_immutable BEFORE UPDATE OR DELETE
 ON privacy_protected.restore_synthetic_fixtures FOR EACH ROW
 EXECUTE FUNCTION public.prevent_privacy_execution_record_delete();

CREATE TABLE privacy_protected.restore_replay_inventory_attestations (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),input_source varchar(32) NOT NULL CHECK(input_source IN ('LIVE_LEDGER','SYNTHETIC_BOOTSTRAP')),
 inventory_sha256 bytea NOT NULL CHECK(octet_length(inventory_sha256)=32),object_count integer NOT NULL CHECK(object_count>0),
 policy_version varchar(80) NOT NULL,executor_version varchar(80) NOT NULL,plan_schema_version varchar(80) NOT NULL,
 image_digest varchar(71) NOT NULL CHECK(image_digest~'^sha256:[0-9a-f]{64}$'),schema_migration_digest bytea NOT NULL CHECK(octet_length(schema_migration_digest)=32),
 imported_count integer NOT NULL CHECK(imported_count>=0),replayed_count integer NOT NULL CHECK(replayed_count>0),
 already_applied_count integer NOT NULL CHECK(already_applied_count>=0),absence_verified_count integer NOT NULL,
 synthetic_replayed_count integer NOT NULL CHECK(synthetic_replayed_count>=0),closure_v3_count integer NOT NULL CHECK(closure_v3_count>=0),
 intent_only_count integer NOT NULL CHECK(intent_only_count>=0),legacy_closure_v2_count integer NOT NULL CHECK(legacy_closure_v2_count>=0),
 erasure_effective_at_verified_count integer NOT NULL CHECK(erasure_effective_at_verified_count>=0),
 evidence_sha256 bytea NOT NULL CHECK(octet_length(evidence_sha256)=32),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),UNIQUE(input_source,inventory_sha256),
 CHECK(replayed_count=imported_count+already_applied_count),CHECK(absence_verified_count=replayed_count),
 CHECK(closure_v3_count=replayed_count AND intent_only_count=0 AND legacy_closure_v2_count=0 AND erasure_effective_at_verified_count=replayed_count),
 CHECK((input_source='LIVE_LEDGER' AND synthetic_replayed_count=0) OR (input_source='SYNTHETIC_BOOTSTRAP' AND synthetic_replayed_count=replayed_count))
);
CREATE TABLE privacy_protected.restore_replay_inventory_attestation_runs (
 attestation_id uuid NOT NULL REFERENCES privacy_protected.restore_replay_inventory_attestations(id) ON DELETE RESTRICT,
 run_id uuid NOT NULL UNIQUE REFERENCES privacy_protected.restore_replay_runs(id) ON DELETE RESTRICT,
 PRIMARY KEY(attestation_id,run_id)
);
CREATE TRIGGER privacy_restore_replay_inventory_attestations_immutable BEFORE UPDATE OR DELETE
 ON privacy_protected.restore_replay_inventory_attestations FOR EACH ROW EXECUTE FUNCTION public.prevent_privacy_execution_record_delete();
CREATE TRIGGER privacy_restore_replay_inventory_attestation_runs_immutable BEFORE UPDATE OR DELETE
 ON privacy_protected.restore_replay_inventory_attestation_runs FOR EACH ROW EXECUTE FUNCTION public.prevent_privacy_execution_record_delete();

CREATE FUNCTION public.privacy_tombstone_prepare_closure_v3(p_execution_id uuid,p_worker_ref uuid)
RETURNS TABLE(execution_id uuid,request_id uuid,request_ref uuid,subject_user_id uuid,plan_sha256 bytea,workset_sha256 bytea,
 execution_started_at timestamptz,closed_at timestamptz,evidence_expires_at timestamptz,erasure_effective_at timestamptz,replay_operations text[])
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT prepared.execution_id,prepared.request_id,prepared.request_ref,prepared.subject_user_id,prepared.plan_sha256,prepared.workset_sha256,
  prepared.execution_started_at,prepared.closed_at,prepared.evidence_expires_at,subject.erased_at,prepared.replay_operations
 FROM public.privacy_tombstone_prepare_closure_v2(p_execution_id,p_worker_ref) prepared
 JOIN users subject ON subject.id=prepared.subject_user_id
 WHERE subject.erased_at IS NOT NULL AND subject.erasure_execution_id=prepared.execution_id AND subject.erasure_replay_run_id IS NULL
  AND subject.erased_at>=prepared.execution_started_at AND subject.erased_at<=prepared.closed_at
  AND NOT EXISTS(SELECT 1 FROM consent_forms consent WHERE consent.user_id=subject.id AND consent.ceased_at IS NULL)
  AND NOT EXISTS(SELECT 1 FROM consent_forms consent WHERE consent.user_id=subject.id AND consent.cessation_reason='ACCOUNT_ERASURE'
   AND (consent.ceased_at<>subject.erased_at OR consent.evidence_expires_at<>subject.erased_at+interval '3 years'));
$$;

CREATE FUNCTION public.privacy_tombstone_confirm_closure_v3(
 p_execution_id uuid,p_worker_ref uuid,p_ledger_version text,p_encryption_key_id text,p_locator_key_id text,p_locator_digest bytea,
 p_object_version_id text,p_ciphertext_sha256 bytea,p_size_bytes bigint,p_written_at timestamptz,p_verified_at timestamptz
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE intent privacy_protected.restore_tombstone_closure_intents%ROWTYPE;existing privacy_protected.restore_tombstone_closure_receipts%ROWTYPE;
BEGIN
 SELECT * INTO intent FROM privacy_protected.restore_tombstone_closure_intents WHERE execution_id=p_execution_id;
 IF intent.execution_id IS NULL OR p_worker_ref IS NULL OR p_ledger_version<>'restore-tombstone-closure/v3'
  OR p_encryption_key_id IS NULL OR p_encryption_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'
  OR p_locator_key_id IS NULL OR p_locator_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'
  OR octet_length(p_locator_digest)<>32 OR octet_length(p_ciphertext_sha256)<>32 OR p_object_version_id IS NULL
  OR p_object_version_id<>btrim(p_object_version_id) OR char_length(p_object_version_id) NOT BETWEEN 1 AND 1024
  OR p_size_bytes NOT BETWEEN 1 AND 1048576 OR p_written_at IS NULL OR p_verified_at<p_written_at OR p_verified_at>intent.evidence_expires_at
  OR NOT EXISTS(SELECT 1 FROM users subject JOIN privacy_erasure_executions execution ON execution.id=subject.erasure_execution_id
   WHERE execution.id=p_execution_id AND subject.erased_at BETWEEN execution.accepted_at AND intent.closed_at)
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_tombstone_closure_receipt_rejected'; END IF;
 SELECT * INTO existing FROM privacy_protected.restore_tombstone_closure_receipts WHERE execution_id=p_execution_id;
 IF existing.execution_id IS NULL THEN
  INSERT INTO privacy_protected.restore_tombstone_closure_receipts(execution_id,ledger_version,encryption_key_id,locator_key_id,locator_digest,object_version_id,ciphertext_sha256,size_bytes,written_at,verified_at)
  VALUES(p_execution_id,p_ledger_version,p_encryption_key_id,p_locator_key_id,p_locator_digest,p_object_version_id,p_ciphertext_sha256,p_size_bytes,p_written_at,p_verified_at);
 ELSIF ROW(existing.ledger_version,existing.encryption_key_id,existing.locator_key_id,existing.locator_digest,existing.object_version_id,existing.ciphertext_sha256,existing.size_bytes,existing.written_at,existing.verified_at)
  IS DISTINCT FROM ROW(p_ledger_version,p_encryption_key_id,p_locator_key_id,p_locator_digest,p_object_version_id,p_ciphertext_sha256,p_size_bytes,p_written_at,p_verified_at)
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_tombstone_closure_receipt_conflict'; END IF;
 RETURN p_execution_id;
END; $$;

CREATE FUNCTION public.privacy_restore_create_synthetic_fixture(p_worker_ref uuid)
RETURNS TABLE(source_execution_id uuid,source_request_id uuid,source_request_ref uuid,subject_user_id uuid,
 plan_sha256 bytea,workset_sha256 bytea,erasure_effective_at timestamptz,operations text[])
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE v_subject uuid:=gen_random_uuid();v_execution uuid:=gen_random_uuid();v_request uuid:=gen_random_uuid();v_ref uuid:=gen_random_uuid();
 v_plan bytea:=gen_random_bytes(32);v_workset bytea:=gen_random_bytes(32);v_effective timestamptz:=clock_timestamp();
 v_operations text[]:=ARRAY['AUTH_ACCESS_REVOKE','AUTH_TOKEN_DELETE','PROFILE_IDENTITY_DELETE','PROVIDER_LOCAL_FENCE','IDENTITY_CLEAR'];
BEGIN
 IF p_worker_ref IS NULL OR current_setting('mycfc.privacy_restore_isolated',true)<>'on' THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_synthetic_fixture_rejected'; END IF;
 INSERT INTO users(id,name,email,password_hash,date_of_birth,created_at,updated_at)
 VALUES(v_subject,'Synthetic restore fixture',('synthetic-'||v_subject::text||'@invalid.invalid')::citext,'synthetic-disabled','1900-01-01',v_effective,v_effective);
 INSERT INTO member_profiles(user_id,address_line1,created_at,updated_at) VALUES(v_subject,'Synthetic fixture only',v_effective,v_effective);
 INSERT INTO privacy_protected.provider_connections(id,subject_user_id,service_code,provider_role,provider_contract_version,
  registry_evidence_key_id,registry_evidence_digest,target_key_id,target_opaque,credential_key_id,credential_opaque,state,
  sync_enabled,webhook_enabled,reconnect_enabled,created_at,updated_at)
 VALUES(gen_random_uuid(),v_subject,'synthetic.restore.fixture','PROCESSOR','synthetic-v1','synthetic-key',gen_random_bytes(32),
  'synthetic-key',gen_random_bytes(32),'synthetic-key',gen_random_bytes(32),'ACTIVE',true,true,true,v_effective,v_effective);
 INSERT INTO privacy_protected.restore_synthetic_fixtures(subject_user_id,source_execution_id,source_request_id,source_request_ref,
  plan_sha256,workset_sha256,erasure_effective_at,operations,fixture_marker,created_by_ref,created_at)
 VALUES(v_subject,v_execution,v_request,v_ref,v_plan,v_workset,v_effective,v_operations,'mycfc/privacy-restore-synthetic-fixture/v1',p_worker_ref,v_effective);
 RETURN QUERY SELECT v_execution,v_request,v_ref,v_subject,v_plan,v_workset,v_effective,v_operations;
END; $$;

CREATE FUNCTION public.privacy_restore_import_authenticated_v2_hardened(
 p_worker_ref uuid,p_kind text,p_record_version text,p_envelope_version text,p_encryption_key_id text,p_locator_key_id text,p_locator_digest bytea,
 p_ciphertext_sha256 bytea,p_object_version_id text,p_written_at timestamptz,p_verified_at timestamptz,p_retain_until timestamptz,
 p_source_execution_id uuid,p_source_request_id uuid,p_source_request_ref uuid,p_subject_user_id uuid,p_plan_sha256 bytea,p_workset_sha256 bytea,
 p_execution_started_at timestamptz,p_erasure_effective_at timestamptz,p_closure_version text,p_synthetic_fixture text,p_replay_version text,p_action_version text,
 p_operations text[],p_prescription_sha256 bytea,p_record_sha256 bytea
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE import_ref uuid; existing privacy_protected.restore_ledger_imports%ROWTYPE; synthetic_match boolean;
BEGIN
 SELECT EXISTS(SELECT 1 FROM privacy_protected.restore_synthetic_fixtures fixture
  WHERE fixture.subject_user_id=p_subject_user_id AND fixture.source_execution_id=p_source_execution_id
   AND fixture.source_request_id=p_source_request_id AND fixture.source_request_ref=p_source_request_ref
   AND fixture.plan_sha256=p_plan_sha256 AND fixture.workset_sha256=p_workset_sha256
   AND fixture.erasure_effective_at=p_erasure_effective_at AND fixture.operations=p_operations
   AND fixture.fixture_marker=p_synthetic_fixture) INTO synthetic_match;
 IF p_worker_ref IS NULL OR p_kind NOT IN ('intent','closure') OR p_record_version<>'restore-tombstone/v2'
  OR p_envelope_version<>'x25519-aes256gcm-hkdfsha256/v2' OR p_replay_version<>'relational-erasure-replay/v1' OR p_action_version<>'v1'
  OR p_encryption_key_id IS NULL OR p_encryption_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'
  OR p_locator_key_id IS NULL OR p_locator_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'
  OR octet_length(p_locator_digest)<>32 OR octet_length(p_ciphertext_sha256)<>32 OR octet_length(p_plan_sha256)<>32
  OR octet_length(p_workset_sha256)<>32 OR octet_length(p_prescription_sha256)<>32 OR octet_length(p_record_sha256)<>32
  OR p_object_version_id IS NULL OR p_object_version_id<>btrim(p_object_version_id) OR char_length(p_object_version_id) NOT BETWEEN 1 AND 1024
  OR p_written_at IS NULL OR p_verified_at<p_written_at OR p_source_execution_id IS NULL OR p_source_request_id IS NULL
  OR p_source_request_ref IS NULL OR p_subject_user_id IS NULL OR p_execution_started_at IS NULL OR p_erasure_effective_at IS NULL
  OR p_erasure_effective_at<p_execution_started_at OR cardinality(p_operations) NOT BETWEEN 1 AND 17
  OR (p_kind='intent' AND (p_closure_version IS NOT NULL OR p_erasure_effective_at<>p_execution_started_at))
  OR (p_kind='closure' AND p_closure_version NOT IN ('restore-tombstone-closure/v2','restore-tombstone-closure/v3'))
  OR EXISTS(SELECT 1 FROM unnest(p_operations) operation WHERE NOT(public.privacy_relational_replay_operation_supported(operation) OR operation='PROVIDER_LOCAL_FENCE'))
  OR cardinality(p_operations)<>(SELECT count(DISTINCT operation) FROM unnest(p_operations) operation)
  OR (p_synthetic_fixture IS NULL AND EXISTS(SELECT 1 FROM privacy_protected.restore_synthetic_fixtures WHERE subject_user_id=p_subject_user_id))
  OR (p_synthetic_fixture IS NOT NULL AND (p_synthetic_fixture<>'mycfc/privacy-restore-synthetic-fixture/v1' OR NOT synthetic_match))
  OR (p_kind='intent' AND p_retain_until IS NOT NULL) OR (p_kind='closure' AND (p_retain_until IS NULL OR p_verified_at>p_retain_until)) THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_import_rejected'; END IF;
 SELECT * INTO existing FROM privacy_protected.restore_ledger_imports
  WHERE (locator_key_id=p_locator_key_id AND locator_digest=p_locator_digest) OR source_execution_id=p_source_execution_id FOR UPDATE;
 IF existing.id IS NULL THEN
  INSERT INTO privacy_protected.restore_ledger_imports(kind,record_version,envelope_version,encryption_key_id,locator_key_id,locator_digest,
   ciphertext_sha256,object_version_id,written_at,verified_at,retain_until,source_execution_id,source_request_id,source_request_ref,subject_user_id,
   plan_sha256,workset_sha256,execution_started_at,erasure_effective_at,closure_version,synthetic_fixture,replay_version,action_version,operations,
   prescription_sha256,record_sha256,imported_by_ref)
  VALUES(p_kind,p_record_version,p_envelope_version,p_encryption_key_id,p_locator_key_id,p_locator_digest,p_ciphertext_sha256,p_object_version_id,
   p_written_at,p_verified_at,p_retain_until,p_source_execution_id,p_source_request_id,p_source_request_ref,p_subject_user_id,p_plan_sha256,p_workset_sha256,
   p_execution_started_at,p_erasure_effective_at,p_closure_version,p_synthetic_fixture,p_replay_version,p_action_version,p_operations,p_prescription_sha256,p_record_sha256,p_worker_ref)
  RETURNING id INTO import_ref;
 ELSIF ROW(existing.kind,existing.record_version,existing.envelope_version,existing.encryption_key_id,existing.locator_key_id,existing.locator_digest,
   existing.ciphertext_sha256,existing.object_version_id,existing.written_at,existing.verified_at,existing.retain_until,existing.source_execution_id,
   existing.source_request_id,existing.source_request_ref,existing.subject_user_id,existing.plan_sha256,existing.workset_sha256,existing.execution_started_at,
   existing.erasure_effective_at,existing.closure_version,existing.synthetic_fixture,existing.replay_version,existing.action_version,existing.operations,existing.prescription_sha256,existing.record_sha256)
  IS DISTINCT FROM ROW(p_kind,p_record_version,p_envelope_version,p_encryption_key_id,p_locator_key_id,p_locator_digest,p_ciphertext_sha256,
   p_object_version_id,p_written_at,p_verified_at,p_retain_until,p_source_execution_id,p_source_request_id,p_source_request_ref,p_subject_user_id,
   p_plan_sha256,p_workset_sha256,p_execution_started_at,p_erasure_effective_at,p_closure_version,p_synthetic_fixture,p_replay_version,p_action_version,p_operations,
   p_prescription_sha256,p_record_sha256) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_import_conflict';
 ELSE import_ref:=existing.id; END IF;
 RETURN import_ref;
END; $$;

CREATE FUNCTION public.privacy_restore_verify_operation(p_run_id uuid,p_operation_code text,p_source_already_applied boolean)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE imported privacy_protected.restore_ledger_imports%ROWTYPE; effective_at timestamptz;
BEGIN
 SELECT imported_row.* INTO imported FROM privacy_protected.restore_replay_runs run
 JOIN privacy_protected.restore_ledger_imports imported_row ON imported_row.id=run.import_id WHERE run.id=p_run_id;
 IF imported.id IS NULL OR NOT(p_operation_code=ANY(imported.operations)) OR
  NOT(public.privacy_relational_replay_operation_supported(p_operation_code) OR p_operation_code='PROVIDER_LOCAL_FENCE') THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 effective_at:=imported.erasure_effective_at;
 IF p_source_already_applied AND NOT EXISTS(SELECT 1 FROM users WHERE id=imported.subject_user_id
  AND erasure_execution_id=imported.source_execution_id AND erased_at=imported.erasure_effective_at) THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 CASE p_operation_code
 WHEN 'ACTIVITY_CONNECTION_DISCONNECT' THEN
  IF EXISTS(SELECT 1 FROM activity_connections WHERE user_id=imported.subject_user_id AND (status<>'DISCONNECTED' OR credentials_ciphertext IS NOT NULL OR credential_key_id IS NOT NULL OR cardinality(scopes)>0 OR sync_cursor IS NOT NULL)) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'ACTIVITY_SUBJECT_DELETE' THEN IF EXISTS(SELECT 1 FROM activity_connections WHERE user_id=imported.subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'ANNOUNCEMENT_DELIVERY_DELETE' THEN IF EXISTS(SELECT 1 FROM announcement_deliveries WHERE user_id=imported.subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'AUTH_ACCESS_REVOKE' THEN IF EXISTS(SELECT 1 FROM sessions WHERE subject_indexed AND user_id=imported.subject_user_id) OR EXISTS(SELECT 1 FROM user_platform_roles WHERE user_id=imported.subject_user_id) OR EXISTS(SELECT 1 FROM staff_grants WHERE user_id=imported.subject_user_id AND revoked_at IS NULL) OR EXISTS(SELECT 1 FROM privacy_reviewer_grants WHERE user_id=imported.subject_user_id AND revoked_at IS NULL) OR EXISTS(SELECT 1 FROM privacy_executor_grants WHERE user_id=imported.subject_user_id AND revoked_at IS NULL) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'AUTH_TOKEN_DELETE' THEN IF EXISTS(SELECT 1 FROM email_verification_tokens WHERE user_id=imported.subject_user_id) OR EXISTS(SELECT 1 FROM password_reset_tokens WHERE user_id=imported.subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'DEPENDANT_RELATIONSHIP_DELETE' THEN IF EXISTS(SELECT 1 FROM users WHERE guardian_id=imported.subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'EVENT_RESPONSE_DELETE' THEN IF EXISTS(SELECT 1 FROM event_responses WHERE user_id=imported.subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'IDENTITY_CLEAR' THEN
  IF NOT EXISTS(SELECT 1 FROM users WHERE id=imported.subject_user_id AND erased_at=effective_at AND NOT is_active AND email IS NULL AND minor_login_id IS NULL AND password_hash IS NULL AND guardian_id IS NULL AND ((p_source_already_applied AND erasure_execution_id=imported.source_execution_id AND erasure_replay_run_id IS NULL) OR (NOT p_source_already_applied AND erasure_execution_id IS NULL AND erasure_replay_run_id=p_run_id))) OR EXISTS(SELECT 1 FROM consent_forms WHERE user_id=imported.subject_user_id AND ceased_at IS NULL) OR EXISTS(SELECT 1 FROM consent_forms WHERE user_id=imported.subject_user_id AND cessation_reason='ACCOUNT_ERASURE' AND (ceased_at<>effective_at OR evidence_expires_at<>effective_at+interval '3 years')) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'MEMBERSHIP_ACTIVE_REVOKE','MEMBERSHIP_HISTORY_ANONYMIZE' THEN IF EXISTS(SELECT 1 FROM user_memberships WHERE user_id=imported.subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'PROFILE_HEALTH_DELETE' THEN IF EXISTS(SELECT 1 FROM member_profiles WHERE user_id=imported.subject_user_id AND (emergency_contact_name<>'' OR emergency_contact_relationship<>'' OR emergency_contact_phone<>'' OR emergency_contact_alternate_phone<>'' OR medical_declaration<>'UNKNOWN' OR allergies<>'' OR medical_conditions<>'' OR medication<>'' OR activity_restrictions<>'' OR medical_notes<>'')) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'PROFILE_IDENTITY_DELETE' THEN IF EXISTS(SELECT 1 FROM member_profiles WHERE user_id=imported.subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'PROVIDER_LOCAL_FENCE' THEN IF EXISTS(SELECT 1 FROM privacy_protected.provider_connections WHERE subject_user_id=imported.subject_user_id) OR EXISTS(SELECT 1 FROM privacy_protected.provider_credential_quarantine q JOIN privacy_protected.provider_targets target ON target.id=q.target_id JOIN privacy_protected.provider_capture_sets capture ON capture.execution_id=target.execution_id WHERE capture.subject_user_id=imported.subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'REPAIR_REPORTER_ANONYMIZE' THEN IF EXISTS(SELECT 1 FROM repair_requests WHERE reported_by_id=imported.subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'SUGGESTION_SUBJECT_DELETE' THEN IF EXISTS(SELECT 1 FROM suggestions WHERE requester_id=imported.subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'TRAINING_PRESCRIPTION_DELETE' THEN IF EXISTS(SELECT 1 FROM training_prescriptions WHERE athlete_user_id=imported.subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'TRAINING_RESULT_DELETE' THEN IF EXISTS(SELECT 1 FROM training_session_outcomes WHERE user_id=imported.subject_user_id) OR EXISTS(SELECT 1 FROM training_logs WHERE user_id=imported.subject_user_id) OR EXISTS(SELECT 1 FROM performance_metrics WHERE user_id=imported.subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 ELSE RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed';
 END CASE;
END; $$;

CREATE FUNCTION public.privacy_restore_apply_hardened_operation(p_run_id uuid,p_operation_code text)
RETURNS bigint LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE imported privacy_protected.restore_ledger_imports%ROWTYPE;changed bigint:=0;n bigint:=0;
BEGIN
 SELECT imported_row.* INTO imported FROM privacy_protected.restore_replay_runs run JOIN privacy_protected.restore_ledger_imports imported_row ON imported_row.id=run.import_id WHERE run.id=p_run_id FOR UPDATE OF run;
 IF imported.id IS NULL OR imported.erasure_effective_at IS NULL THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_operation_rejected'; END IF;
 IF p_operation_code='PROVIDER_LOCAL_FENCE' THEN
  DELETE FROM privacy_protected.provider_credential_quarantine q USING privacy_protected.provider_targets target,privacy_protected.provider_capture_sets capture
   WHERE q.target_id=target.id AND target.execution_id=capture.execution_id AND capture.subject_user_id=imported.subject_user_id; GET DIAGNOSTICS changed=ROW_COUNT;
  DELETE FROM privacy_protected.provider_connections WHERE subject_user_id=imported.subject_user_id; GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
 ELSIF p_operation_code='IDENTITY_CLEAR' THEN
  IF EXISTS(SELECT 1 FROM sessions WHERE NOT subject_indexed AND expiry>clock_timestamp()) OR EXISTS(SELECT 1 FROM users WHERE guardian_id=imported.subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_operation_rejected'; END IF;
  DELETE FROM sessions WHERE subject_indexed AND user_id=imported.subject_user_id;
  DELETE FROM email_verification_tokens WHERE user_id=imported.subject_user_id;
  DELETE FROM password_reset_tokens WHERE user_id=imported.subject_user_id;
  DELETE FROM user_platform_roles WHERE user_id=imported.subject_user_id;
  UPDATE users SET name='Conta eliminada',email=NULL,email_verified_at=NULL,minor_login_id=NULL,password_hash=NULL,guardian_id=NULL,is_dependent=false,
   date_of_birth=DATE '1900-01-01',is_active=false,leaderboard_visible=false,credential_version=credential_version+1,
   erased_at=imported.erasure_effective_at,erasure_execution_id=NULL,erasure_replay_run_id=p_run_id,updated_at=clock_timestamp()
  WHERE id=imported.subject_user_id AND erased_at IS NULL; GET DIAGNOSTICS changed=ROW_COUNT;
 ELSE changed:=public.privacy_restore_apply_relational_operation(p_run_id,imported.subject_user_id,imported.erasure_effective_at,p_operation_code);
 END IF;
 PERFORM public.privacy_restore_verify_operation(p_run_id,p_operation_code,false);
 RETURN changed;
END; $$;

CREATE FUNCTION public.privacy_restore_begin_replay_hardened(p_import_id uuid,p_worker_ref uuid)
RETURNS TABLE(run_id uuid,outcome_code text) LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE imported privacy_protected.restore_ledger_imports%ROWTYPE;run_ref uuid;operation text;verification bytea;existing_outcome text;
BEGIN
 SELECT * INTO imported FROM privacy_protected.restore_ledger_imports WHERE id=p_import_id;
 IF imported.id IS NULL OR imported.erasure_effective_at IS NULL THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_replay_rejected'; END IF;
 run_ref:=public.privacy_restore_begin_replay(p_import_id,p_worker_ref);
 SELECT run.outcome_code INTO existing_outcome FROM privacy_protected.restore_replay_runs run WHERE run.id=run_ref FOR UPDATE;
 IF existing_outcome IS NOT NULL THEN RETURN QUERY SELECT run_ref,existing_outcome; RETURN; END IF;
 IF EXISTS(SELECT 1 FROM users WHERE id=imported.subject_user_id AND erased_at IS NOT NULL) THEN
  IF NOT EXISTS(SELECT 1 FROM users WHERE id=imported.subject_user_id AND erasure_execution_id=imported.source_execution_id
   AND erasure_replay_run_id IS NULL AND erased_at=imported.erasure_effective_at) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_source_conflict'; END IF;
  FOREACH operation IN ARRAY imported.operations LOOP PERFORM public.privacy_restore_verify_operation(run_ref,operation,true); END LOOP;
  verification:=digest(convert_to(array_to_string(imported.operations,':')||':ALREADY_APPLIED','UTF8'),'sha256');
  UPDATE privacy_protected.restore_replay_checkpoints SET status='SUCCEEDED',affected_rows=0,
   result_sha256=digest(convert_to(operation_position::text||':'||operation_code||':ALREADY_APPLIED','UTF8'),'sha256'),completed_at=clock_timestamp()
   WHERE restore_replay_checkpoints.run_id=run_ref AND status='PENDING';
  INSERT INTO privacy_protected.restore_replay_already_applied_evidence(run_id,import_id,evidence_code,source_execution_id,
   erasure_effective_at,verified_operations,verification_sha256)
  SELECT run_ref,imported.id,'ALREADY_APPLIED',imported.source_execution_id,imported.erasure_effective_at,imported.operations,verification
   FROM users WHERE users.id=imported.subject_user_id;
  UPDATE privacy_protected.restore_replay_runs SET status='SUCCEEDED',outcome_code='ALREADY_APPLIED_SOURCE',completed_at=clock_timestamp() WHERE id=run_ref;
  existing_outcome:='ALREADY_APPLIED_SOURCE';
 END IF;
 RETURN QUERY SELECT run_ref,existing_outcome;
END; $$;

CREATE OR REPLACE FUNCTION public.privacy_restore_execute_checkpoint(
 p_run_id uuid,p_worker_ref uuid,p_operation_position smallint,p_operation_code text,p_action_version text,p_prescription_sha256 bytea
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE run_row privacy_protected.restore_replay_runs%ROWTYPE;imported privacy_protected.restore_ledger_imports%ROWTYPE;
 checkpoint_row privacy_protected.restore_replay_checkpoints%ROWTYPE;changed bigint;result_digest bytea;
BEGIN
 SELECT * INTO run_row FROM privacy_protected.restore_replay_runs WHERE id=p_run_id FOR UPDATE;
 SELECT * INTO imported FROM privacy_protected.restore_ledger_imports WHERE id=run_row.import_id;
 SELECT * INTO checkpoint_row FROM privacy_protected.restore_replay_checkpoints WHERE run_id=p_run_id AND operation_position=p_operation_position FOR UPDATE;
 IF run_row.id IS NULL OR imported.id IS NULL OR imported.erasure_effective_at IS NULL OR checkpoint_row.id IS NULL OR run_row.worker_ref<>p_worker_ref
  OR run_row.status NOT IN ('PENDING','SUCCEEDED') OR checkpoint_row.operation_code<>p_operation_code OR checkpoint_row.action_version<>p_action_version
  OR imported.prescription_sha256<>p_prescription_sha256 OR imported.action_version<>p_action_version OR imported.operations[p_operation_position]<>p_operation_code
  OR NOT(public.privacy_relational_replay_operation_supported(p_operation_code) OR p_operation_code='PROVIDER_LOCAL_FENCE')
  OR EXISTS(SELECT 1 FROM privacy_protected.restore_replay_checkpoints prior WHERE prior.run_id=p_run_id AND prior.operation_position<p_operation_position AND prior.status<>'SUCCEEDED')
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_checkpoint_rejected'; END IF;
 IF checkpoint_row.status='SUCCEEDED' THEN RETURN checkpoint_row.id; END IF;
 changed:=public.privacy_restore_apply_hardened_operation(p_run_id,p_operation_code);
 result_digest:=digest(convert_to(p_operation_position::text||':'||p_operation_code||':'||changed::text,'UTF8'),'sha256');
 UPDATE privacy_protected.restore_replay_checkpoints SET status='SUCCEEDED',affected_rows=changed,result_sha256=result_digest,completed_at=clock_timestamp() WHERE id=checkpoint_row.id;
 IF NOT EXISTS(SELECT 1 FROM privacy_protected.restore_replay_checkpoints WHERE run_id=p_run_id AND status<>'SUCCEEDED') THEN
  UPDATE privacy_protected.restore_replay_runs SET status='SUCCEEDED',outcome_code='REPLAYED',completed_at=clock_timestamp() WHERE id=p_run_id;
 END IF;
 RETURN checkpoint_row.id;
END; $$;

CREATE FUNCTION public.privacy_restore_record_inventory_attestation(p_input_source text,p_inventory_sha256 bytea,p_schema_migration_digest bytea,
 p_policy_version text,p_executor_version text,p_plan_schema_version text,p_image_digest text,p_run_ids uuid[],
 p_object_count integer,p_imported_count integer,p_replayed_count integer,p_already_applied_count integer,
 p_absence_verified_count integer,p_synthetic_replayed_count integer,p_closure_v3_count integer,p_intent_only_count integer,
 p_legacy_closure_v2_count integer,p_erasure_effective_at_verified_count integer)
RETURNS bytea LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE attestation_ref uuid;evidence bytea;run_ref uuid;operation text;source_mode boolean;
 existing privacy_protected.restore_replay_inventory_attestations%ROWTYPE;
BEGIN
 IF p_input_source NOT IN ('LIVE_LEDGER','SYNTHETIC_BOOTSTRAP') OR octet_length(p_inventory_sha256)<>32 OR octet_length(p_schema_migration_digest)<>32
  OR p_policy_version!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$' OR p_executor_version!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'
  OR p_plan_schema_version!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$' OR p_image_digest!~'^sha256:[0-9a-f]{64}$' OR p_object_count<1
  OR p_replayed_count<1 OR cardinality(p_run_ids)<>p_replayed_count
  OR cardinality(p_run_ids)<>(SELECT count(DISTINCT listed.run_id) FROM unnest(p_run_ids) AS listed(run_id))
  OR p_replayed_count<>p_imported_count+p_already_applied_count OR p_absence_verified_count<>p_replayed_count
  OR (p_input_source='LIVE_LEDGER' AND p_synthetic_replayed_count<>0) OR (p_input_source='SYNTHETIC_BOOTSTRAP' AND p_synthetic_replayed_count<>p_replayed_count)
  OR p_closure_v3_count<>p_replayed_count OR p_intent_only_count<>0 OR p_legacy_closure_v2_count<>0
  OR p_erasure_effective_at_verified_count<>p_replayed_count
  OR EXISTS(SELECT 1 FROM unnest(p_run_ids) AS listed(run_id)
   LEFT JOIN privacy_protected.restore_replay_runs run ON run.id=listed.run_id
   LEFT JOIN privacy_protected.restore_ledger_imports imported ON imported.id=run.import_id
   WHERE run.status<>'SUCCEEDED' OR imported.closure_version<>'restore-tombstone-closure/v3'
    OR (p_input_source='LIVE_LEDGER')<>(imported.synthetic_fixture IS NULL))
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_attestation_rejected'; END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended(p_input_source||':'||encode(p_inventory_sha256,'hex'),0));
 FOREACH run_ref IN ARRAY p_run_ids LOOP
  SELECT outcome_code='ALREADY_APPLIED_SOURCE' INTO source_mode FROM privacy_protected.restore_replay_runs WHERE id=run_ref;
  FOR operation IN SELECT checkpoint.operation_code FROM privacy_protected.restore_replay_checkpoints checkpoint WHERE checkpoint.run_id=run_ref ORDER BY checkpoint.operation_position LOOP
   PERFORM public.privacy_restore_verify_operation(run_ref,operation,source_mode);
  END LOOP;
 END LOOP;
 SELECT digest(convert_to(string_agg(encode(checkpoint.result_sha256,'hex'),':' ORDER BY encode(checkpoint.result_sha256,'hex')),'UTF8'),'sha256') INTO evidence
 FROM privacy_protected.restore_replay_checkpoints checkpoint WHERE checkpoint.run_id=ANY(p_run_ids);
 SELECT * INTO existing FROM privacy_protected.restore_replay_inventory_attestations
 WHERE input_source=p_input_source AND inventory_sha256=p_inventory_sha256;
 IF existing.id IS NOT NULL THEN
  IF ROW(existing.schema_migration_digest,existing.policy_version,existing.executor_version,existing.plan_schema_version,existing.image_digest,
    existing.object_count,existing.imported_count,existing.replayed_count,existing.already_applied_count,existing.absence_verified_count,
    existing.synthetic_replayed_count,existing.closure_v3_count,existing.intent_only_count,existing.legacy_closure_v2_count,
    existing.erasure_effective_at_verified_count,existing.evidence_sha256)
   IS DISTINCT FROM ROW(p_schema_migration_digest,p_policy_version,p_executor_version,p_plan_schema_version,p_image_digest,
    p_object_count,p_imported_count,p_replayed_count,p_already_applied_count,p_absence_verified_count,p_synthetic_replayed_count,
    p_closure_v3_count,p_intent_only_count,p_legacy_closure_v2_count,p_erasure_effective_at_verified_count,evidence)
   OR EXISTS(SELECT link.run_id FROM privacy_protected.restore_replay_inventory_attestation_runs link WHERE link.attestation_id=existing.id
    EXCEPT SELECT listed.run_id FROM unnest(p_run_ids) AS listed(run_id))
   OR EXISTS(SELECT listed.run_id FROM unnest(p_run_ids) AS listed(run_id)
    EXCEPT SELECT link.run_id FROM privacy_protected.restore_replay_inventory_attestation_runs link WHERE link.attestation_id=existing.id)
  THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_attestation_conflict'; END IF;
  RETURN existing.evidence_sha256;
 END IF;
 INSERT INTO privacy_protected.restore_replay_inventory_attestations(input_source,inventory_sha256,schema_migration_digest,policy_version,executor_version,plan_schema_version,image_digest,object_count,imported_count,replayed_count,
  already_applied_count,absence_verified_count,synthetic_replayed_count,closure_v3_count,intent_only_count,legacy_closure_v2_count,
  erasure_effective_at_verified_count,evidence_sha256)
 VALUES(p_input_source,p_inventory_sha256,p_schema_migration_digest,p_policy_version,p_executor_version,p_plan_schema_version,p_image_digest,
  p_object_count,p_imported_count,p_replayed_count,p_already_applied_count,p_absence_verified_count,p_synthetic_replayed_count,
  p_closure_v3_count,p_intent_only_count,p_legacy_closure_v2_count,p_erasure_effective_at_verified_count,evidence)
 RETURNING id INTO attestation_ref;
 INSERT INTO privacy_protected.restore_replay_inventory_attestation_runs(attestation_id,run_id)
 SELECT attestation_ref,listed.run_id FROM unnest(p_run_ids) AS listed(run_id);
 RETURN evidence;
END; $$;

CREATE FUNCTION public.privacy_restore_observe_inventory(p_input_source text,p_inventory_sha256 bytea,p_schema_migration_digest bytea,
 p_policy_version text,p_executor_version text,p_plan_schema_version text,p_image_digest text)
RETURNS TABLE(replay_count integer,source_already_applied_count integer,synthetic_count integer,verified_run_count integer,
 expected_checkpoint_count integer,succeeded_checkpoint_count integer,provider_absent_count integer,consent_clock_verified_count integer,
 closure_v3_count integer,erasure_effective_at_verified_count integer,evidence_sha256 bytea)
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT attestation.replayed_count,
  count(DISTINCT run.id) FILTER(WHERE run.outcome_code='ALREADY_APPLIED_SOURCE')::integer,
  count(DISTINCT run.id) FILTER(WHERE imported.synthetic_fixture='mycfc/privacy-restore-synthetic-fixture/v1')::integer,
  count(DISTINCT run.id) FILTER(WHERE cardinality(imported.operations)=(SELECT count(*) FROM privacy_protected.restore_replay_checkpoints exact_checkpoint WHERE exact_checkpoint.run_id=run.id)
   AND NOT EXISTS(SELECT 1 FROM privacy_protected.restore_replay_checkpoints failed_checkpoint WHERE failed_checkpoint.run_id=run.id AND failed_checkpoint.status<>'SUCCEEDED'))::integer,
  count(checkpoint.*)::integer,
  count(checkpoint.*) FILTER(WHERE checkpoint.status='SUCCEEDED')::integer,
  count(DISTINCT run.id) FILTER(WHERE 'PROVIDER_LOCAL_FENCE'=ANY(imported.operations)
   AND NOT EXISTS(SELECT 1 FROM privacy_protected.provider_connections connection WHERE connection.subject_user_id=imported.subject_user_id)
   AND NOT EXISTS(SELECT 1 FROM privacy_protected.provider_credential_quarantine quarantine
    JOIN privacy_protected.provider_targets target ON target.id=quarantine.target_id
    JOIN privacy_protected.provider_capture_sets capture ON capture.execution_id=target.execution_id
    WHERE capture.subject_user_id=imported.subject_user_id))::integer,
  count(DISTINCT run.id) FILTER(WHERE 'IDENTITY_CLEAR'=ANY(imported.operations)
   AND EXISTS(SELECT 1 FROM users WHERE users.id=imported.subject_user_id AND users.erased_at=imported.erasure_effective_at)
   AND NOT EXISTS(SELECT 1 FROM consent_forms consent WHERE consent.user_id=imported.subject_user_id AND consent.ceased_at IS NULL)
   AND NOT EXISTS(SELECT 1 FROM consent_forms consent WHERE consent.user_id=imported.subject_user_id AND consent.cessation_reason='ACCOUNT_ERASURE'
    AND (consent.ceased_at<>imported.erasure_effective_at OR consent.evidence_expires_at<>imported.erasure_effective_at+interval '3 years')))::integer,
  count(DISTINCT run.id) FILTER(WHERE imported.closure_version='restore-tombstone-closure/v3')::integer,
  count(DISTINCT run.id) FILTER(WHERE EXISTS(SELECT 1 FROM users WHERE users.id=imported.subject_user_id AND users.erased_at=imported.erasure_effective_at)
   AND NOT EXISTS(SELECT 1 FROM consent_forms consent WHERE consent.user_id=imported.subject_user_id AND consent.ceased_at IS NULL)
   AND NOT EXISTS(SELECT 1 FROM consent_forms consent WHERE consent.user_id=imported.subject_user_id AND consent.cessation_reason='ACCOUNT_ERASURE'
    AND (consent.ceased_at<>imported.erasure_effective_at OR consent.evidence_expires_at<>imported.erasure_effective_at+interval '3 years')))::integer,
  attestation.evidence_sha256
 FROM privacy_protected.restore_replay_inventory_attestations attestation
 JOIN privacy_protected.restore_replay_inventory_attestation_runs link ON link.attestation_id=attestation.id
 JOIN privacy_protected.restore_replay_runs run ON run.id=link.run_id
 JOIN privacy_protected.restore_ledger_imports imported ON imported.id=run.import_id
 JOIN privacy_protected.restore_replay_checkpoints checkpoint ON checkpoint.run_id=run.id
 WHERE attestation.input_source=p_input_source AND attestation.inventory_sha256=p_inventory_sha256
  AND attestation.schema_migration_digest=p_schema_migration_digest AND attestation.policy_version=p_policy_version
  AND attestation.executor_version=p_executor_version AND attestation.plan_schema_version=p_plan_schema_version AND attestation.image_digest=p_image_digest
 GROUP BY attestation.id,attestation.replayed_count,attestation.evidence_sha256
 HAVING count(DISTINCT run.id)=attestation.replayed_count;
$$;

REVOKE ALL ON TABLE privacy_protected.restore_replay_already_applied_evidence,privacy_protected.restore_synthetic_fixtures,
 privacy_protected.restore_replay_inventory_attestations,privacy_protected.restore_replay_inventory_attestation_runs FROM PUBLIC;
REVOKE ALL ON FUNCTION public.privacy_restore_create_synthetic_fixture(uuid),
 public.privacy_tombstone_prepare_closure_v3(uuid,uuid),public.privacy_tombstone_confirm_closure_v3(uuid,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz),
 public.privacy_restore_import_authenticated_v2_hardened(uuid,text,text,text,text,text,bytea,bytea,text,timestamptz,timestamptz,timestamptz,uuid,uuid,uuid,uuid,bytea,bytea,timestamptz,timestamptz,text,text,text,text,text[],bytea,bytea),
 public.privacy_restore_verify_operation(uuid,text,boolean),public.privacy_restore_apply_hardened_operation(uuid,text),
 public.privacy_restore_begin_replay_hardened(uuid,uuid),public.privacy_restore_record_inventory_attestation(text,bytea,bytea,text,text,text,text,uuid[],integer,integer,integer,integer,integer,integer,integer,integer,integer,integer),
 public.privacy_restore_observe_inventory(text,bytea,bytea,text,text,text,text) FROM PUBLIC;
