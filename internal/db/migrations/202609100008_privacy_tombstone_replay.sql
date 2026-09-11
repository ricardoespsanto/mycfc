-- #247 P0: authenticated v2 tombstones carry a bounded relational-only replay
-- prescription. V1 receipts remain readable evidence, but cannot authorize new
-- destructive work or be imported for replay.
ALTER TABLE privacy_protected.restore_tombstone_receipts
 DROP CONSTRAINT restore_tombstone_receipts_ledger_version_check,
 ADD CONSTRAINT restore_tombstone_receipts_ledger_version_check
 CHECK(ledger_version IN ('restore-tombstone/v1','restore-tombstone/v2'));
ALTER TABLE privacy_protected.restore_tombstone_closure_receipts
 DROP CONSTRAINT restore_tombstone_closure_receipts_ledger_version_check,
 ADD CONSTRAINT restore_tombstone_closure_receipts_ledger_version_check
 CHECK(ledger_version IN ('restore-tombstone-closure/v1','restore-tombstone-closure/v2'));

CREATE FUNCTION public.privacy_relational_replay_operation_supported(p_operation_code text)
RETURNS boolean LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
 SELECT p_operation_code IN (
  'ACTIVITY_CONNECTION_DISCONNECT','ACTIVITY_SUBJECT_DELETE','ANNOUNCEMENT_DELIVERY_DELETE',
  'AUTH_ACCESS_REVOKE','AUTH_TOKEN_DELETE','DEPENDANT_RELATIONSHIP_DELETE','EVENT_RESPONSE_DELETE',
  'IDENTITY_CLEAR','MEMBERSHIP_ACTIVE_REVOKE','MEMBERSHIP_HISTORY_ANONYMIZE','PROFILE_HEALTH_DELETE',
  'PROFILE_IDENTITY_DELETE','REPAIR_REPORTER_ANONYMIZE','SUGGESTION_SUBJECT_DELETE',
  'TRAINING_PRESCRIPTION_DELETE','TRAINING_RESULT_DELETE'
 );
$$;

CREATE FUNCTION public.privacy_tombstone_prepare_v2(
 p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_lease_epoch bigint,p_worker_ref uuid
) RETURNS TABLE(
 execution_id uuid,request_id uuid,request_ref uuid,subject_user_id uuid,plan_sha256 bytea,workset_sha256 bytea,
 execution_started_at timestamptz,replay_operations text[]
) LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT execution.id,request.id,request.public_ref,COALESCE(request.subject_user_id,request.requester_user_id),execution.plan_sha256,
  digest(convert_to(string_agg(job.plan_entry_position::text||':'||encode(job.entry_sha256,'hex')||':'||checkpoint.operation_position::text||':'||checkpoint.operation_code||':'||checkpoint.action_version,
   '|' ORDER BY job.plan_entry_position,checkpoint.operation_position),'UTF8'),'sha256'),execution.accepted_at,
  (array_agg(checkpoint.operation_code ORDER BY job.plan_entry_position,checkpoint.operation_position)
   FILTER(WHERE public.privacy_relational_replay_operation_supported(checkpoint.operation_code)))::text[]
 FROM privacy_erasure_job_leases lease
 JOIN privacy_erasure_category_jobs selected_job ON selected_job.id=lease.job_id AND selected_job.lease_epoch=lease.epoch
 JOIN privacy_erasure_job_attempts attempt ON attempt.lease_id=lease.id AND attempt.job_id=selected_job.id
 JOIN privacy_erasure_executions execution ON execution.id=selected_job.execution_id
 JOIN data_erasure_requests request ON request.id=execution.request_id
 JOIN privacy_erasure_category_jobs job ON job.execution_id=execution.id
 JOIN privacy_erasure_job_checkpoints checkpoint ON checkpoint.job_id=job.id
 WHERE selected_job.id=p_job_id AND lease.id=p_lease_id AND attempt.id=p_attempt_id AND lease.epoch=p_lease_epoch AND lease.worker_ref=p_worker_ref
  AND selected_job.status='LEASED' AND lease.released_at IS NULL AND attempt.finished_at IS NULL AND lease.expires_at>clock_timestamp()
  AND selected_job.category_key='backup-tombstones' AND EXISTS(
   SELECT 1 FROM privacy_erasure_job_checkpoints selected_checkpoint WHERE selected_checkpoint.job_id=selected_job.id
    AND selected_checkpoint.operation_code='BACKUP_TOMBSTONE_REPLAY' AND selected_checkpoint.action_version='v1' AND selected_checkpoint.status='PENDING')
  AND COALESCE(request.subject_user_id,request.requester_user_id) IS NOT NULL
 GROUP BY execution.id,request.id,request.public_ref,request.subject_user_id,request.requester_user_id
 HAVING count(*) FILTER(WHERE public.privacy_relational_replay_operation_supported(checkpoint.operation_code)) BETWEEN 1 AND 16
  AND count(DISTINCT checkpoint.operation_code) FILTER(WHERE public.privacy_relational_replay_operation_supported(checkpoint.operation_code))
   =count(*) FILTER(WHERE public.privacy_relational_replay_operation_supported(checkpoint.operation_code))
  AND count(*) FILTER(WHERE public.privacy_relational_replay_operation_supported(checkpoint.operation_code) AND checkpoint.action_version='v1')
   =count(*) FILTER(WHERE public.privacy_relational_replay_operation_supported(checkpoint.operation_code));
$$;

CREATE FUNCTION public.privacy_tombstone_confirm_v2(
 p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_lease_epoch bigint,p_worker_ref uuid,p_ledger_version text,p_encryption_key_id text,
 p_locator_key_id text,p_locator_digest bytea,p_object_version_id text,p_ciphertext_sha256 bytea,p_size_bytes bigint,p_written_at timestamptz,p_verified_at timestamptz
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE checkpoint_ref uuid; execution_ref uuid; request_ref uuid; existing privacy_protected.restore_tombstone_receipts%ROWTYPE;
BEGIN
 SELECT checkpoint.id,job.execution_id,execution.request_id INTO checkpoint_ref,execution_ref,request_ref
 FROM privacy_erasure_category_jobs job JOIN privacy_erasure_job_leases lease ON lease.job_id=job.id AND lease.epoch=job.lease_epoch
 JOIN privacy_erasure_job_attempts attempt ON attempt.lease_id=lease.id AND attempt.job_id=job.id
 JOIN privacy_erasure_executions execution ON execution.id=job.execution_id JOIN privacy_erasure_job_checkpoints checkpoint ON checkpoint.job_id=job.id
 WHERE job.id=p_job_id AND lease.id=p_lease_id AND attempt.id=p_attempt_id AND lease.epoch=p_lease_epoch AND lease.worker_ref=p_worker_ref AND job.status='LEASED'
  AND lease.released_at IS NULL AND attempt.finished_at IS NULL AND lease.expires_at>clock_timestamp() AND job.category_key='backup-tombstones'
  AND checkpoint.operation_code='BACKUP_TOMBSTONE_REPLAY' AND checkpoint.action_version='v1' FOR UPDATE OF job,lease,attempt,checkpoint;
 IF checkpoint_ref IS NULL OR p_ledger_version<>'restore-tombstone/v2'
  OR p_encryption_key_id IS NULL OR p_encryption_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'
  OR p_locator_key_id IS NULL OR p_locator_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'
  OR octet_length(p_locator_digest)<>32 OR octet_length(p_ciphertext_sha256)<>32 OR p_object_version_id IS NULL
  OR p_object_version_id<>btrim(p_object_version_id) OR char_length(p_object_version_id) NOT BETWEEN 1 AND 1024
  OR p_size_bytes NOT BETWEEN 1 AND 1048576 OR p_written_at IS NULL OR p_verified_at<p_written_at THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_tombstone_receipt_rejected'; END IF;
 SELECT * INTO existing FROM privacy_protected.restore_tombstone_receipts WHERE execution_id=execution_ref;
 IF existing.execution_id IS NULL THEN
  INSERT INTO privacy_protected.restore_tombstone_receipts(execution_id,request_id,ledger_version,encryption_key_id,locator_key_id,locator_digest,object_version_id,ciphertext_sha256,size_bytes,written_at,verified_at)
  VALUES(execution_ref,request_ref,p_ledger_version,p_encryption_key_id,p_locator_key_id,p_locator_digest,p_object_version_id,p_ciphertext_sha256,p_size_bytes,p_written_at,p_verified_at);
 ELSIF ROW(existing.request_id,existing.ledger_version,existing.encryption_key_id,existing.locator_key_id,existing.locator_digest,existing.object_version_id,existing.ciphertext_sha256,existing.size_bytes,existing.written_at,existing.verified_at)
  IS DISTINCT FROM ROW(request_ref,p_ledger_version,p_encryption_key_id,p_locator_key_id,p_locator_digest,p_object_version_id,p_ciphertext_sha256,p_size_bytes,p_written_at,p_verified_at) THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_tombstone_receipt_conflict'; END IF;
 UPDATE privacy_erasure_job_checkpoints SET status='SUCCEEDED',completed_by_attempt_id=p_attempt_id,completed_at=clock_timestamp(),affected_rows=1,
  result_sha256=digest(p_ciphertext_sha256||convert_to(p_object_version_id,'UTF8'),'sha256') WHERE id=checkpoint_ref AND status='PENDING';
 RETURN checkpoint_ref;
END; $$;

CREATE FUNCTION public.privacy_tombstone_prepare_closure_v2(p_execution_id uuid,p_worker_ref uuid)
RETURNS TABLE(execution_id uuid,request_id uuid,request_ref uuid,subject_user_id uuid,plan_sha256 bytea,workset_sha256 bytea,
 execution_started_at timestamptz,closed_at timestamptz,evidence_expires_at timestamptz,replay_operations text[])
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
#variable_conflict use_column
DECLARE v_closed_at timestamptz;
BEGIN
 IF p_execution_id IS NULL OR p_worker_ref IS NULL OR NOT EXISTS(
  SELECT 1 FROM privacy_erasure_executions execution JOIN data_erasure_requests request ON request.id=execution.request_id
  WHERE execution.id=p_execution_id AND execution.status='SUCCEEDED' AND request.status IN ('PROCESSING','RETRYABLE_FAILED')
   AND NOT EXISTS(SELECT 1 FROM privacy_erasure_category_jobs job WHERE job.execution_id=execution.id AND job.status<>'SUCCEEDED')
   AND EXISTS(SELECT 1 FROM privacy_protected.restore_tombstone_receipts receipt WHERE receipt.execution_id=execution.id AND receipt.ledger_version='restore-tombstone/v2'))
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_tombstone_closure_unavailable'; END IF;
 v_closed_at:=clock_timestamp();
 INSERT INTO privacy_protected.restore_tombstone_closure_intents(execution_id,closed_at,evidence_expires_at)
 VALUES(p_execution_id,v_closed_at,v_closed_at+interval '24 months') ON CONFLICT(execution_id) DO NOTHING;
 RETURN QUERY SELECT execution.id,request.id,request.public_ref,COALESCE(request.subject_user_id,request.requester_user_id),execution.plan_sha256,
  digest(convert_to(string_agg(job.plan_entry_position::text||':'||encode(job.entry_sha256,'hex')||':'||checkpoint.operation_position::text||':'||checkpoint.operation_code||':'||checkpoint.action_version,
   '|' ORDER BY job.plan_entry_position,checkpoint.operation_position),'UTF8'),'sha256'),execution.accepted_at,intent.closed_at,intent.evidence_expires_at,
  (array_agg(checkpoint.operation_code ORDER BY job.plan_entry_position,checkpoint.operation_position)
   FILTER(WHERE public.privacy_relational_replay_operation_supported(checkpoint.operation_code)))::text[]
 FROM privacy_erasure_executions execution JOIN data_erasure_requests request ON request.id=execution.request_id
 JOIN privacy_erasure_category_jobs job ON job.execution_id=execution.id JOIN privacy_erasure_job_checkpoints checkpoint ON checkpoint.job_id=job.id
 JOIN privacy_protected.restore_tombstone_closure_intents intent ON intent.execution_id=execution.id
 WHERE execution.id=p_execution_id AND COALESCE(request.subject_user_id,request.requester_user_id) IS NOT NULL
 GROUP BY execution.id,request.id,request.public_ref,request.subject_user_id,request.requester_user_id,intent.closed_at,intent.evidence_expires_at
 HAVING count(*) FILTER(WHERE public.privacy_relational_replay_operation_supported(checkpoint.operation_code)) BETWEEN 1 AND 16
  AND count(DISTINCT checkpoint.operation_code) FILTER(WHERE public.privacy_relational_replay_operation_supported(checkpoint.operation_code))
   =count(*) FILTER(WHERE public.privacy_relational_replay_operation_supported(checkpoint.operation_code))
  AND count(*) FILTER(WHERE public.privacy_relational_replay_operation_supported(checkpoint.operation_code) AND checkpoint.action_version='v1')
   =count(*) FILTER(WHERE public.privacy_relational_replay_operation_supported(checkpoint.operation_code));
END; $$;

CREATE FUNCTION public.privacy_tombstone_confirm_closure_v2(
 p_execution_id uuid,p_worker_ref uuid,p_ledger_version text,p_encryption_key_id text,p_locator_key_id text,p_locator_digest bytea,
 p_object_version_id text,p_ciphertext_sha256 bytea,p_size_bytes bigint,p_written_at timestamptz,p_verified_at timestamptz
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE intent privacy_protected.restore_tombstone_closure_intents%ROWTYPE; existing privacy_protected.restore_tombstone_closure_receipts%ROWTYPE;
BEGIN
 SELECT * INTO intent FROM privacy_protected.restore_tombstone_closure_intents WHERE execution_id=p_execution_id;
 IF intent.execution_id IS NULL OR p_worker_ref IS NULL OR p_ledger_version<>'restore-tombstone-closure/v2'
  OR p_encryption_key_id IS NULL OR p_encryption_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'
  OR p_locator_key_id IS NULL OR p_locator_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'
  OR octet_length(p_locator_digest)<>32 OR octet_length(p_ciphertext_sha256)<>32 OR p_object_version_id IS NULL
  OR p_object_version_id<>btrim(p_object_version_id) OR char_length(p_object_version_id) NOT BETWEEN 1 AND 1024
  OR p_size_bytes NOT BETWEEN 1 AND 1048576 OR p_written_at IS NULL OR p_verified_at<p_written_at OR p_verified_at>intent.evidence_expires_at THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_tombstone_closure_receipt_rejected'; END IF;
 SELECT * INTO existing FROM privacy_protected.restore_tombstone_closure_receipts WHERE execution_id=p_execution_id;
 IF existing.execution_id IS NULL THEN
  INSERT INTO privacy_protected.restore_tombstone_closure_receipts(execution_id,ledger_version,encryption_key_id,locator_key_id,locator_digest,object_version_id,ciphertext_sha256,size_bytes,written_at,verified_at)
  VALUES(p_execution_id,p_ledger_version,p_encryption_key_id,p_locator_key_id,p_locator_digest,p_object_version_id,p_ciphertext_sha256,p_size_bytes,p_written_at,p_verified_at);
 ELSIF ROW(existing.ledger_version,existing.encryption_key_id,existing.locator_key_id,existing.locator_digest,existing.object_version_id,existing.ciphertext_sha256,existing.size_bytes,existing.written_at,existing.verified_at)
  IS DISTINCT FROM ROW(p_ledger_version,p_encryption_key_id,p_locator_key_id,p_locator_digest,p_object_version_id,p_ciphertext_sha256,p_size_bytes,p_written_at,p_verified_at) THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_tombstone_closure_receipt_conflict'; END IF;
 RETURN p_execution_id;
END; $$;

CREATE TABLE privacy_protected.restore_ledger_imports (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),kind varchar(16) NOT NULL CHECK(kind IN ('intent','closure')),
 record_version varchar(40) NOT NULL CHECK(record_version='restore-tombstone/v2'),
 envelope_version varchar(48) NOT NULL CHECK(envelope_version='x25519-aes256gcm-hkdfsha256/v2'),
 encryption_key_id varchar(80) NOT NULL CHECK(encryption_key_id=btrim(encryption_key_id) AND encryption_key_id~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'),
 locator_key_id varchar(80) NOT NULL CHECK(locator_key_id=btrim(locator_key_id) AND locator_key_id~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'),
 locator_digest bytea NOT NULL CHECK(octet_length(locator_digest)=32),ciphertext_sha256 bytea NOT NULL CHECK(octet_length(ciphertext_sha256)=32),
 object_version_id varchar(1024) NOT NULL CHECK(object_version_id=btrim(object_version_id) AND char_length(object_version_id) BETWEEN 1 AND 1024),
 written_at timestamptz NOT NULL,verified_at timestamptz NOT NULL,retain_until timestamptz NULL,
 source_execution_id uuid NOT NULL,source_request_id uuid NOT NULL,source_request_ref uuid NOT NULL,subject_user_id uuid NOT NULL,
 plan_sha256 bytea NOT NULL CHECK(octet_length(plan_sha256)=32),workset_sha256 bytea NOT NULL CHECK(octet_length(workset_sha256)=32),
 execution_started_at timestamptz NOT NULL,replay_version varchar(40) NOT NULL CHECK(replay_version='relational-erasure-replay/v1'),
 action_version varchar(16) NOT NULL CHECK(action_version='v1'),operations text[] NOT NULL,
 prescription_sha256 bytea NOT NULL CHECK(octet_length(prescription_sha256)=32),record_sha256 bytea NOT NULL CHECK(octet_length(record_sha256)=32),
 imported_by_ref uuid NOT NULL,imported_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK(verified_at>=written_at),CHECK((kind='intent' AND retain_until IS NULL) OR (kind='closure' AND retain_until IS NOT NULL)),
 CHECK(cardinality(operations) BETWEEN 1 AND 16),UNIQUE(locator_key_id,locator_digest),UNIQUE(source_execution_id)
);
CREATE TRIGGER privacy_restore_ledger_imports_immutable BEFORE UPDATE OR DELETE ON privacy_protected.restore_ledger_imports
 FOR EACH ROW EXECUTE FUNCTION public.prevent_privacy_execution_record_delete();

CREATE TABLE privacy_protected.restore_replay_runs (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),import_id uuid NOT NULL UNIQUE REFERENCES privacy_protected.restore_ledger_imports(id) ON DELETE RESTRICT,
 worker_ref uuid NOT NULL,status varchar(16) NOT NULL DEFAULT 'PENDING' CHECK(status IN ('PENDING','SUCCEEDED')),
 started_at timestamptz NOT NULL DEFAULT clock_timestamp(),completed_at timestamptz NULL,
 CHECK((status='PENDING' AND completed_at IS NULL) OR (status='SUCCEEDED' AND completed_at IS NOT NULL))
);
CREATE TABLE privacy_protected.restore_replay_checkpoints (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),run_id uuid NOT NULL REFERENCES privacy_protected.restore_replay_runs(id) ON DELETE RESTRICT,
 operation_position smallint NOT NULL CHECK(operation_position>0),operation_code varchar(80) NOT NULL,action_version varchar(16) NOT NULL,
 status varchar(16) NOT NULL DEFAULT 'PENDING' CHECK(status IN ('PENDING','SUCCEEDED')),affected_rows bigint NULL CHECK(affected_rows IS NULL OR affected_rows>=0),
 result_sha256 bytea NULL CHECK(result_sha256 IS NULL OR octet_length(result_sha256)=32),completed_at timestamptz NULL,
 UNIQUE(run_id,operation_position),UNIQUE(run_id,operation_code),
 CHECK((status='PENDING' AND affected_rows IS NULL AND result_sha256 IS NULL AND completed_at IS NULL) OR
       (status='SUCCEEDED' AND affected_rows IS NOT NULL AND result_sha256 IS NOT NULL AND completed_at IS NOT NULL))
);

-- Account cutoff happens before ordinary live checkpoints, so a restore from
-- an older backup must carry those privilege revocations into replay too.
-- Dedicated replay provenance avoids inventing a user actor.
ALTER TABLE staff_grants
 ADD COLUMN revoked_by_replay_run_id uuid NULL REFERENCES privacy_protected.restore_replay_runs(id) ON DELETE RESTRICT,
 DROP CONSTRAINT staff_grants_revocation_valid,
 ADD CONSTRAINT staff_grants_revocation_valid CHECK(
  (revoked_at IS NULL AND revoked_by_id IS NULL AND revoked_by_replay_run_id IS NULL AND revoke_reason IS NULL)
  OR (revoked_at IS NOT NULL AND num_nonnulls(revoked_by_id,revoked_by_replay_run_id)=1
      AND revoke_reason=btrim(revoke_reason) AND char_length(revoke_reason) BETWEEN 1 AND 500)
 ) NOT VALID;
ALTER TABLE privacy_reviewer_grants
 ADD COLUMN revoked_by_replay_run_id uuid NULL REFERENCES privacy_protected.restore_replay_runs(id) ON DELETE RESTRICT,
 DROP CONSTRAINT privacy_reviewer_grants_check,
 ADD CONSTRAINT privacy_reviewer_grants_revocation_actor_exactly_one CHECK(
  (revoked_at IS NULL AND revoked_by IS NULL AND revoked_by_replay_run_id IS NULL)
  OR (revoked_at IS NOT NULL AND num_nonnulls(revoked_by,revoked_by_replay_run_id)=1)
 ) NOT VALID;
ALTER TABLE privacy_executor_grants
 ADD COLUMN revoked_by_replay_run_id uuid NULL REFERENCES privacy_protected.restore_replay_runs(id) ON DELETE RESTRICT,
 DROP CONSTRAINT privacy_executor_grants_check,
 ADD CONSTRAINT privacy_executor_grants_revocation_actor_exactly_one CHECK(
  (revoked_at IS NULL AND revoked_by IS NULL AND revoked_by_replay_run_id IS NULL)
  OR (revoked_at IS NOT NULL AND num_nonnulls(revoked_by,revoked_by_replay_run_id)=1)
 ) NOT VALID;
ALTER TABLE staff_grant_audit_events
 ADD COLUMN actor_replay_run_id uuid NULL REFERENCES privacy_protected.restore_replay_runs(id) ON DELETE RESTRICT,
 DROP CONSTRAINT staff_grant_audit_actor_principal_exactly_one,
 ADD CONSTRAINT staff_grant_audit_actor_principal_exactly_one
  CHECK(num_nonnulls(actor_user_id,actor_principal_id,actor_replay_run_id)=1) NOT VALID;
ALTER TABLE staff_grants VALIDATE CONSTRAINT staff_grants_revocation_valid;
ALTER TABLE privacy_reviewer_grants VALIDATE CONSTRAINT privacy_reviewer_grants_revocation_actor_exactly_one;
ALTER TABLE privacy_executor_grants VALIDATE CONSTRAINT privacy_executor_grants_revocation_actor_exactly_one;
ALTER TABLE staff_grant_audit_events VALIDATE CONSTRAINT staff_grant_audit_actor_principal_exactly_one;

CREATE OR REPLACE FUNCTION public.audit_staff_grant_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  INSERT INTO staff_grant_audit_events(staff_grant_id,action,actor_user_id) VALUES(NEW.id,'GRANTED',NEW.granted_by_id);
  RETURN NEW;
 END IF;
 IF OLD.user_id<>NEW.user_id OR OLD.capability<>NEW.capability OR OLD.programme_id IS DISTINCT FROM NEW.programme_id
  OR OLD.team_id IS DISTINCT FROM NEW.team_id OR OLD.granted_by_id<>NEW.granted_by_id OR OLD.granted_at<>NEW.granted_at
  OR OLD.revoked_at IS NOT NULL OR NEW.revoked_at IS NULL THEN
  RAISE EXCEPTION 'staff grants are immutable except for one revocation';
 END IF;
 INSERT INTO staff_grant_audit_events(staff_grant_id,action,actor_user_id,actor_replay_run_id,occurred_at,reason)
 VALUES(NEW.id,'REVOKED',NEW.revoked_by_id,NEW.revoked_by_replay_run_id,NEW.revoked_at,NEW.revoke_reason);
 RETURN NEW;
END; $$;

ALTER TABLE users ADD COLUMN erasure_replay_run_id uuid NULL
 REFERENCES privacy_protected.restore_replay_runs(id) ON DELETE RESTRICT;
CREATE UNIQUE INDEX users_erasure_replay_run_uidx ON users(erasure_replay_run_id) WHERE erasure_replay_run_id IS NOT NULL;
ALTER TABLE users DROP CONSTRAINT users_identity_shape;
ALTER TABLE users ADD CONSTRAINT users_identity_shape CHECK (
 (erased_at IS NULL AND erasure_execution_id IS NULL AND erasure_replay_run_id IS NULL AND (
   (is_dependent AND guardian_id IS NOT NULL AND email IS NULL AND
    ((minor_login_id IS NULL AND password_hash IS NULL) OR (minor_login_id IS NOT NULL AND password_hash IS NOT NULL)))
   OR
   (NOT is_dependent AND guardian_id IS NULL AND email IS NOT NULL AND password_hash IS NOT NULL AND minor_login_id IS NULL)
 ))
 OR
 (erased_at IS NOT NULL AND num_nonnulls(erasure_execution_id,erasure_replay_run_id)=1 AND NOT is_active
  AND NOT leaderboard_visible AND NOT is_dependent AND guardian_id IS NULL
  AND email IS NULL AND email_verified_at IS NULL AND minor_login_id IS NULL
  AND password_hash IS NULL AND name='Conta eliminada' AND date_of_birth=DATE '1900-01-01')
) NOT VALID;
ALTER TABLE users VALIDATE CONSTRAINT users_identity_shape;

CREATE FUNCTION public.privacy_restore_import_authenticated_v2(
 p_worker_ref uuid,p_kind text,p_record_version text,p_envelope_version text,p_encryption_key_id text,p_locator_key_id text,p_locator_digest bytea,
 p_ciphertext_sha256 bytea,p_object_version_id text,p_written_at timestamptz,p_verified_at timestamptz,p_retain_until timestamptz,
 p_source_execution_id uuid,p_source_request_id uuid,p_source_request_ref uuid,p_subject_user_id uuid,p_plan_sha256 bytea,p_workset_sha256 bytea,
 p_execution_started_at timestamptz,p_replay_version text,p_action_version text,p_operations text[],p_prescription_sha256 bytea,p_record_sha256 bytea
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE import_ref uuid; existing privacy_protected.restore_ledger_imports%ROWTYPE;
BEGIN
 IF p_worker_ref IS NULL OR p_kind NOT IN ('intent','closure') OR p_record_version<>'restore-tombstone/v2'
  OR p_envelope_version<>'x25519-aes256gcm-hkdfsha256/v2' OR p_replay_version<>'relational-erasure-replay/v1' OR p_action_version<>'v1'
  OR p_encryption_key_id IS NULL OR p_encryption_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'
  OR p_locator_key_id IS NULL OR p_locator_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'
  OR octet_length(p_locator_digest)<>32 OR octet_length(p_ciphertext_sha256)<>32 OR octet_length(p_plan_sha256)<>32
  OR octet_length(p_workset_sha256)<>32 OR octet_length(p_prescription_sha256)<>32 OR octet_length(p_record_sha256)<>32
  OR p_object_version_id IS NULL OR p_object_version_id<>btrim(p_object_version_id) OR char_length(p_object_version_id) NOT BETWEEN 1 AND 1024
  OR p_written_at IS NULL OR p_verified_at<p_written_at OR p_source_execution_id IS NULL OR p_source_request_id IS NULL
  OR p_source_request_ref IS NULL OR p_subject_user_id IS NULL OR p_execution_started_at IS NULL
  OR cardinality(p_operations) NOT BETWEEN 1 AND 16 OR EXISTS(SELECT 1 FROM unnest(p_operations) operation WHERE NOT public.privacy_relational_replay_operation_supported(operation))
  OR cardinality(p_operations)<>(SELECT count(DISTINCT operation) FROM unnest(p_operations) operation)
  OR (p_kind='intent' AND p_retain_until IS NOT NULL) OR (p_kind='closure' AND (p_retain_until IS NULL OR p_verified_at>p_retain_until)) THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_import_rejected'; END IF;
 SELECT * INTO existing FROM privacy_protected.restore_ledger_imports
  WHERE (locator_key_id=p_locator_key_id AND locator_digest=p_locator_digest) OR source_execution_id=p_source_execution_id FOR UPDATE;
 IF existing.id IS NULL THEN
  INSERT INTO privacy_protected.restore_ledger_imports(kind,record_version,envelope_version,encryption_key_id,locator_key_id,locator_digest,ciphertext_sha256,
   object_version_id,written_at,verified_at,retain_until,source_execution_id,source_request_id,source_request_ref,subject_user_id,plan_sha256,workset_sha256,
   execution_started_at,replay_version,action_version,operations,prescription_sha256,record_sha256,imported_by_ref)
  VALUES(p_kind,p_record_version,p_envelope_version,p_encryption_key_id,p_locator_key_id,p_locator_digest,p_ciphertext_sha256,p_object_version_id,p_written_at,
   p_verified_at,p_retain_until,p_source_execution_id,p_source_request_id,p_source_request_ref,p_subject_user_id,p_plan_sha256,p_workset_sha256,
   p_execution_started_at,p_replay_version,p_action_version,p_operations,p_prescription_sha256,p_record_sha256,p_worker_ref) RETURNING id INTO import_ref;
 ELSIF ROW(existing.kind,existing.record_version,existing.envelope_version,existing.encryption_key_id,existing.locator_key_id,existing.locator_digest,
   existing.ciphertext_sha256,existing.object_version_id,existing.written_at,existing.verified_at,existing.retain_until,existing.source_execution_id,
   existing.source_request_id,existing.source_request_ref,existing.subject_user_id,existing.plan_sha256,existing.workset_sha256,existing.execution_started_at,
   existing.replay_version,existing.action_version,existing.operations,existing.prescription_sha256,existing.record_sha256)
  IS DISTINCT FROM ROW(p_kind,p_record_version,p_envelope_version,p_encryption_key_id,p_locator_key_id,p_locator_digest,p_ciphertext_sha256,
   p_object_version_id,p_written_at,p_verified_at,p_retain_until,p_source_execution_id,p_source_request_id,p_source_request_ref,p_subject_user_id,
   p_plan_sha256,p_workset_sha256,p_execution_started_at,p_replay_version,p_action_version,p_operations,p_prescription_sha256,p_record_sha256) THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_import_conflict';
 ELSE import_ref:=existing.id; END IF;
 RETURN import_ref;
END; $$;

CREATE FUNCTION public.privacy_restore_begin_replay(p_import_id uuid,p_worker_ref uuid)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE run_ref uuid; imported privacy_protected.restore_ledger_imports%ROWTYPE;
BEGIN
 SELECT * INTO imported FROM privacy_protected.restore_ledger_imports WHERE id=p_import_id;
 IF imported.id IS NULL OR p_worker_ref IS NULL OR imported.record_version<>'restore-tombstone/v2' OR imported.envelope_version<>'x25519-aes256gcm-hkdfsha256/v2'
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_replay_rejected'; END IF;
 INSERT INTO privacy_protected.restore_replay_runs(import_id,worker_ref) VALUES(p_import_id,p_worker_ref) ON CONFLICT(import_id) DO NOTHING;
 SELECT id INTO run_ref FROM privacy_protected.restore_replay_runs WHERE import_id=p_import_id FOR UPDATE;
 -- Isolated replay may resume after a process crash. The authenticated import
 -- is immutable; moving only its execution fence to the new worker preserves
 -- exact idempotency without weakening the prescription.
 UPDATE privacy_protected.restore_replay_runs SET worker_ref=p_worker_ref
  WHERE id=run_ref AND status='PENDING' AND worker_ref<>p_worker_ref;
 INSERT INTO privacy_protected.restore_replay_checkpoints(run_id,operation_position,operation_code,action_version)
 SELECT run_ref,ordinality::smallint,operation,imported.action_version FROM unnest(imported.operations) WITH ORDINALITY item(operation,ordinality)
 ON CONFLICT(run_id,operation_position) DO NOTHING;
 IF (SELECT count(*) FROM privacy_protected.restore_replay_checkpoints WHERE run_id=run_ref)<>cardinality(imported.operations)
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_replay_conflict'; END IF;
 RETURN run_ref;
END; $$;

CREATE FUNCTION public.privacy_restore_apply_relational_operation(
 p_replay_run_id uuid,p_subject_user_id uuid,p_effective_at timestamptz,p_operation_code text
) RETURNS bigint LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE changed bigint:=0; n bigint:=0; principal_ref uuid; subject_name text; subject_email text; subject_login text;
BEGIN
 IF p_replay_run_id IS NULL OR p_subject_user_id IS NULL OR p_effective_at IS NULL OR NOT public.privacy_relational_replay_operation_supported(p_operation_code)
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_operation_rejected'; END IF;
 SELECT name,email::text,minor_login_id::text INTO subject_name,subject_email,subject_login FROM users WHERE id=p_subject_user_id FOR UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_subject_missing'; END IF;
 CASE p_operation_code
 WHEN 'ACTIVITY_CONNECTION_DISCONNECT' THEN
  UPDATE activity_connections SET status='DISCONNECTED',provider_user_id='erased-'||id::text,credentials_ciphertext=NULL,credential_key_id=NULL,
   credential_expires_at=NULL,scopes='{}',sync_cursor=NULL,last_error_code=NULL,last_error_message=NULL,last_error_at=NULL,
   disconnected_at=COALESCE(disconnected_at,clock_timestamp()),updated_at=clock_timestamp()
  WHERE user_id=p_subject_user_id AND status<>'DISCONNECTED'; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'ACTIVITY_SUBJECT_DELETE' THEN
  DELETE FROM activity_connections WHERE user_id=p_subject_user_id; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'ANNOUNCEMENT_DELIVERY_DELETE' THEN
  DELETE FROM announcement_deliveries WHERE user_id=p_subject_user_id; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'AUTH_ACCESS_REVOKE' THEN
  DELETE FROM sessions WHERE subject_indexed AND user_id=p_subject_user_id; GET DIAGNOSTICS changed=ROW_COUNT;
  DELETE FROM user_platform_roles WHERE user_id=p_subject_user_id; GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
  UPDATE staff_grants SET revoked_by_id=NULL,revoked_by_replay_run_id=p_replay_run_id,revoked_at=p_effective_at,
   revoke_reason='PRIVACY_ACCOUNT_CLOSURE' WHERE user_id=p_subject_user_id AND revoked_at IS NULL;
  GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
  WITH revoked AS(UPDATE privacy_reviewer_grants SET revoked_by=NULL,revoked_by_replay_run_id=p_replay_run_id,revoked_at=p_effective_at
   WHERE user_id=p_subject_user_id AND revoked_at IS NULL RETURNING id)
  INSERT INTO privacy_reviewer_grant_events(grant_id,actor_ref,action,occurred_at) SELECT id,p_replay_run_id,'REVOKED',p_effective_at FROM revoked;
  GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
  WITH revoked AS(UPDATE privacy_executor_grants SET revoked_by=NULL,revoked_by_replay_run_id=p_replay_run_id,revoked_at=p_effective_at
   WHERE user_id=p_subject_user_id AND revoked_at IS NULL RETURNING id)
  INSERT INTO privacy_executor_grant_events(grant_id,actor_ref,action,occurred_at) SELECT id,p_replay_run_id,'REVOKED',p_effective_at FROM revoked;
  GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
 WHEN 'AUTH_TOKEN_DELETE' THEN
  DELETE FROM email_verification_tokens WHERE user_id=p_subject_user_id; GET DIAGNOSTICS changed=ROW_COUNT;
  DELETE FROM password_reset_tokens WHERE user_id=p_subject_user_id; GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
 WHEN 'DEPENDANT_RELATIONSHIP_DELETE' THEN
  IF EXISTS(SELECT 1 FROM users WHERE guardian_id=p_subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_dependants_unresolved'; END IF;
 WHEN 'EVENT_RESPONSE_DELETE' THEN
  DELETE FROM event_responses WHERE user_id=p_subject_user_id; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'IDENTITY_CLEAR' THEN
  IF EXISTS(SELECT 1 FROM sessions WHERE NOT subject_indexed AND expiry>clock_timestamp()) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_legacy_sessions_unresolved'; END IF;
  IF EXISTS(SELECT 1 FROM users WHERE guardian_id=p_subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_dependants_unresolved'; END IF;
  DELETE FROM sessions WHERE subject_indexed AND user_id=p_subject_user_id;
  DELETE FROM email_verification_tokens WHERE user_id=p_subject_user_id;
  DELETE FROM password_reset_tokens WHERE user_id=p_subject_user_id;
  DELETE FROM user_platform_roles WHERE user_id=p_subject_user_id;
  UPDATE users SET name='Conta eliminada',email=NULL,email_verified_at=NULL,minor_login_id=NULL,password_hash=NULL,guardian_id=NULL,is_dependent=false,
   date_of_birth=DATE '1900-01-01',is_active=false,leaderboard_visible=false,credential_version=credential_version+1,
   erased_at=clock_timestamp(),erasure_execution_id=NULL,erasure_replay_run_id=p_replay_run_id,updated_at=clock_timestamp()
  WHERE id=p_subject_user_id AND erased_at IS NULL; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'MEMBERSHIP_ACTIVE_REVOKE' THEN
  IF EXISTS(SELECT 1 FROM training_prescriptions prescription JOIN user_memberships membership ON membership.id=prescription.membership_id
   WHERE membership.user_id=p_subject_user_id AND membership.starts_on>=p_effective_at::date) THEN
   RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_dependency_unsupported'; END IF;
  DELETE FROM training_variations WHERE target_membership_id IN(SELECT id FROM user_memberships WHERE user_id=p_subject_user_id AND starts_on>=p_effective_at::date);
  GET DIAGNOSTICS changed=ROW_COUNT;
  DELETE FROM training_variation_group_members WHERE membership_id IN(SELECT id FROM user_memberships WHERE user_id=p_subject_user_id AND starts_on>=p_effective_at::date);
  GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
  DELETE FROM training_group_members WHERE membership_id IN(SELECT id FROM user_memberships WHERE user_id=p_subject_user_id AND starts_on>=p_effective_at::date);
  GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
  DELETE FROM user_memberships WHERE user_id=p_subject_user_id AND starts_on>=p_effective_at::date;
  GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
  UPDATE user_memberships SET ends_on=p_effective_at::date-1,updated_at=clock_timestamp()
   WHERE user_id=p_subject_user_id AND starts_on<p_effective_at::date AND (ends_on IS NULL OR ends_on>=p_effective_at::date);
  GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
 WHEN 'MEMBERSHIP_HISTORY_ANONYMIZE' THEN
  IF EXISTS(SELECT 1 FROM training_prescriptions prescription JOIN user_memberships membership ON membership.id=prescription.membership_id
   WHERE membership.user_id=p_subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_dependency_unsupported'; END IF;
  IF EXISTS(SELECT 1 FROM user_memberships WHERE user_id=p_subject_user_id) THEN
   INSERT INTO privacy_pseudonymous_principals(purpose) VALUES('MEMBERSHIP') RETURNING id INTO principal_ref;
   PERFORM set_config('mycfc.privacy_erasure_operation','MEMBERSHIP_HISTORY_ANONYMIZE',true);
   UPDATE training_variations SET change_summary=privacy_scrub_audit_text(change_summary,p_subject_user_id,subject_name,subject_email,subject_login),
    patch=privacy_scrub_audit_json(patch,p_subject_user_id,subject_name,subject_email,subject_login),updated_at=clock_timestamp()
   WHERE target_membership_id IN(SELECT id FROM user_memberships WHERE user_id=p_subject_user_id); GET DIAGNOSTICS changed=ROW_COUNT;
   UPDATE user_memberships SET user_id=NULL,principal_id=principal_ref,updated_at=clock_timestamp() WHERE user_id=p_subject_user_id;
   GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
  END IF;
 WHEN 'PROFILE_HEALTH_DELETE' THEN
  UPDATE member_profiles SET emergency_contact_name='',emergency_contact_relationship='',emergency_contact_phone='',emergency_contact_alternate_phone='',
   medical_declaration='UNKNOWN',allergies='',medical_conditions='',medication='',activity_restrictions='',medical_notes='',updated_at=clock_timestamp()
  WHERE user_id=p_subject_user_id; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'PROFILE_IDENTITY_DELETE' THEN
  DELETE FROM member_profiles WHERE user_id=p_subject_user_id; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'REPAIR_REPORTER_ANONYMIZE' THEN
  UPDATE repair_requests SET reported_by_id=NULL,updated_at=clock_timestamp() WHERE reported_by_id=p_subject_user_id; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'SUGGESTION_SUBJECT_DELETE' THEN
  DELETE FROM suggestions WHERE requester_id=p_subject_user_id; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'TRAINING_PRESCRIPTION_DELETE' THEN
  UPDATE training_session_outcomes SET prescription_id=NULL,updated_at=clock_timestamp()
   WHERE prescription_id IN(SELECT id FROM training_prescriptions WHERE athlete_user_id=p_subject_user_id);
  PERFORM set_config('mycfc.privacy_erasure_operation','TRAINING_PRESCRIPTION_DELETE',true);
  DELETE FROM training_prescriptions WHERE athlete_user_id=p_subject_user_id; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'TRAINING_RESULT_DELETE' THEN
  DELETE FROM training_session_outcomes WHERE user_id=p_subject_user_id; GET DIAGNOSTICS changed=ROW_COUNT;
  DELETE FROM training_logs WHERE user_id=p_subject_user_id; GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
  DELETE FROM performance_metrics WHERE user_id=p_subject_user_id; GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
 END CASE;

 CASE p_operation_code
 WHEN 'ACTIVITY_CONNECTION_DISCONNECT' THEN
  IF EXISTS(SELECT 1 FROM activity_connections WHERE user_id=p_subject_user_id AND (status<>'DISCONNECTED' OR credentials_ciphertext IS NOT NULL OR credential_key_id IS NOT NULL OR cardinality(scopes)>0 OR sync_cursor IS NOT NULL)) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'ACTIVITY_SUBJECT_DELETE' THEN IF EXISTS(SELECT 1 FROM activity_connections WHERE user_id=p_subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'ANNOUNCEMENT_DELIVERY_DELETE' THEN IF EXISTS(SELECT 1 FROM announcement_deliveries WHERE user_id=p_subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'AUTH_ACCESS_REVOKE' THEN IF EXISTS(SELECT 1 FROM sessions WHERE subject_indexed AND user_id=p_subject_user_id)
  OR EXISTS(SELECT 1 FROM user_platform_roles WHERE user_id=p_subject_user_id)
  OR EXISTS(SELECT 1 FROM staff_grants WHERE user_id=p_subject_user_id AND revoked_at IS NULL)
  OR EXISTS(SELECT 1 FROM privacy_reviewer_grants WHERE user_id=p_subject_user_id AND revoked_at IS NULL)
  OR EXISTS(SELECT 1 FROM privacy_executor_grants WHERE user_id=p_subject_user_id AND revoked_at IS NULL)
  THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'AUTH_TOKEN_DELETE' THEN IF EXISTS(SELECT 1 FROM email_verification_tokens WHERE user_id=p_subject_user_id) OR EXISTS(SELECT 1 FROM password_reset_tokens WHERE user_id=p_subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'DEPENDANT_RELATIONSHIP_DELETE' THEN IF EXISTS(SELECT 1 FROM users WHERE guardian_id=p_subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'EVENT_RESPONSE_DELETE' THEN IF EXISTS(SELECT 1 FROM event_responses WHERE user_id=p_subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'IDENTITY_CLEAR' THEN IF NOT EXISTS(SELECT 1 FROM users WHERE id=p_subject_user_id AND erased_at IS NOT NULL AND erasure_execution_id IS NULL
  AND erasure_replay_run_id=p_replay_run_id AND NOT is_active AND email IS NULL AND minor_login_id IS NULL AND password_hash IS NULL AND guardian_id IS NULL)
  THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'MEMBERSHIP_ACTIVE_REVOKE' THEN IF EXISTS(SELECT 1 FROM user_memberships WHERE user_id=p_subject_user_id AND (starts_on>=p_effective_at::date OR ends_on IS NULL OR ends_on>=p_effective_at::date)) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'MEMBERSHIP_HISTORY_ANONYMIZE' THEN
  IF EXISTS(SELECT 1 FROM user_memberships WHERE user_id=p_subject_user_id)
   OR EXISTS(SELECT 1 FROM user_memberships WHERE principal_id=principal_ref AND (starts_on>=p_effective_at::date OR ends_on IS NULL OR ends_on>=p_effective_at::date))
   OR EXISTS(SELECT 1 FROM training_variations variation JOIN user_memberships membership ON membership.id=variation.target_membership_id
      WHERE membership.principal_id=principal_ref
       AND (variation.change_summary IS DISTINCT FROM privacy_scrub_audit_text(variation.change_summary,p_subject_user_id,subject_name,subject_email,subject_login)
        OR variation.patch IS DISTINCT FROM privacy_scrub_audit_json(variation.patch,p_subject_user_id,subject_name,subject_email,subject_login)))
  THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'PROFILE_HEALTH_DELETE' THEN IF EXISTS(SELECT 1 FROM member_profiles WHERE user_id=p_subject_user_id AND (emergency_contact_name<>'' OR emergency_contact_relationship<>'' OR emergency_contact_phone<>'' OR emergency_contact_alternate_phone<>'' OR medical_declaration<>'UNKNOWN' OR allergies<>'' OR medical_conditions<>'' OR medication<>'' OR activity_restrictions<>'' OR medical_notes<>'')) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'PROFILE_IDENTITY_DELETE' THEN IF EXISTS(SELECT 1 FROM member_profiles WHERE user_id=p_subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'REPAIR_REPORTER_ANONYMIZE' THEN IF EXISTS(SELECT 1 FROM repair_requests WHERE reported_by_id=p_subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'SUGGESTION_SUBJECT_DELETE' THEN IF EXISTS(SELECT 1 FROM suggestions WHERE requester_id=p_subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'TRAINING_PRESCRIPTION_DELETE' THEN IF EXISTS(SELECT 1 FROM training_prescriptions WHERE athlete_user_id=p_subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'TRAINING_RESULT_DELETE' THEN IF EXISTS(SELECT 1 FROM training_session_outcomes WHERE user_id=p_subject_user_id) OR EXISTS(SELECT 1 FROM training_logs WHERE user_id=p_subject_user_id) OR EXISTS(SELECT 1 FROM performance_metrics WHERE user_id=p_subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 END CASE;
 RETURN changed;
END; $$;

CREATE FUNCTION public.privacy_restore_execute_checkpoint(
 p_run_id uuid,p_worker_ref uuid,p_operation_position smallint,p_operation_code text,p_action_version text,p_prescription_sha256 bytea
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE run_row privacy_protected.restore_replay_runs%ROWTYPE; imported privacy_protected.restore_ledger_imports%ROWTYPE;
 checkpoint_row privacy_protected.restore_replay_checkpoints%ROWTYPE; changed bigint; result_digest bytea;
BEGIN
 SELECT * INTO run_row FROM privacy_protected.restore_replay_runs WHERE id=p_run_id FOR UPDATE;
 SELECT * INTO imported FROM privacy_protected.restore_ledger_imports WHERE id=run_row.import_id;
 SELECT * INTO checkpoint_row FROM privacy_protected.restore_replay_checkpoints
  WHERE run_id=p_run_id AND operation_position=p_operation_position FOR UPDATE;
 IF run_row.id IS NULL OR imported.id IS NULL OR checkpoint_row.id IS NULL OR run_row.worker_ref<>p_worker_ref OR run_row.status NOT IN ('PENDING','SUCCEEDED')
  OR checkpoint_row.operation_code<>p_operation_code OR checkpoint_row.action_version<>p_action_version
  OR imported.prescription_sha256<>p_prescription_sha256 OR imported.action_version<>p_action_version
  OR imported.operations[p_operation_position]<>p_operation_code OR NOT public.privacy_relational_replay_operation_supported(p_operation_code)
  OR EXISTS(SELECT 1 FROM privacy_protected.restore_replay_checkpoints prior WHERE prior.run_id=p_run_id AND prior.operation_position<p_operation_position AND prior.status<>'SUCCEEDED')
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_checkpoint_rejected'; END IF;
 IF checkpoint_row.status='SUCCEEDED' THEN RETURN checkpoint_row.id; END IF;
 changed:=public.privacy_restore_apply_relational_operation(p_run_id,imported.subject_user_id,imported.execution_started_at,p_operation_code);
 result_digest:=digest(convert_to(p_operation_position::text||':'||p_operation_code||':'||changed::text,'UTF8'),'sha256');
 UPDATE privacy_protected.restore_replay_checkpoints SET status='SUCCEEDED',affected_rows=changed,result_sha256=result_digest,completed_at=clock_timestamp()
  WHERE id=checkpoint_row.id;
 IF NOT EXISTS(SELECT 1 FROM privacy_protected.restore_replay_checkpoints WHERE run_id=p_run_id AND status<>'SUCCEEDED') THEN
  UPDATE privacy_protected.restore_replay_runs SET status='SUCCEEDED',completed_at=clock_timestamp() WHERE id=p_run_id;
 END IF;
 RETURN checkpoint_row.id;
END; $$;

CREATE OR REPLACE FUNCTION public.privacy_worker_execute_checkpoint(
 p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_lease_epoch bigint,p_worker_ref uuid,p_operation_code text,p_action_version text
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE execution_ref uuid;
BEGIN
 SELECT job.execution_id INTO execution_ref FROM privacy_erasure_category_jobs job
 JOIN privacy_erasure_job_leases lease ON lease.id=p_lease_id AND lease.job_id=job.id AND lease.epoch=p_lease_epoch
 JOIN privacy_erasure_job_attempts attempt ON attempt.id=p_attempt_id AND attempt.job_id=job.id AND attempt.lease_id=lease.id AND attempt.lease_epoch=p_lease_epoch
 WHERE job.id=p_job_id AND job.status='LEASED' AND lease.worker_ref=p_worker_ref AND lease.released_at IS NULL
  AND lease.expires_at>clock_timestamp() AND attempt.finished_at IS NULL FOR UPDATE OF job,lease,attempt;
 IF NOT FOUND THEN RETURN public.privacy_worker_execute_checkpoint_without_tombstone_guard(p_job_id,p_lease_id,p_attempt_id,p_lease_epoch,p_worker_ref,p_operation_code,p_action_version); END IF;
 IF p_operation_code<>'BACKUP_TOMBSTONE_REPLAY' AND NOT EXISTS(
  SELECT 1 FROM privacy_protected.restore_tombstone_receipts receipt WHERE receipt.execution_id=execution_ref AND receipt.ledger_version='restore-tombstone/v2')
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_tombstone_required'; END IF;
 RETURN public.privacy_worker_execute_checkpoint_without_tombstone_guard(p_job_id,p_lease_id,p_attempt_id,p_lease_epoch,p_worker_ref,p_operation_code,p_action_version);
END; $$;

REVOKE ALL ON TABLE privacy_protected.restore_ledger_imports,privacy_protected.restore_replay_runs,privacy_protected.restore_replay_checkpoints FROM PUBLIC;
REVOKE ALL ON FUNCTION public.privacy_relational_replay_operation_supported(text),
 public.privacy_tombstone_prepare_v2(uuid,uuid,uuid,bigint,uuid),
 public.privacy_tombstone_confirm_v2(uuid,uuid,uuid,bigint,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz),
 public.privacy_tombstone_prepare_closure_v2(uuid,uuid),
 public.privacy_tombstone_confirm_closure_v2(uuid,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz),
 public.privacy_restore_import_authenticated_v2(uuid,text,text,text,text,text,bytea,bytea,text,timestamptz,timestamptz,timestamptz,uuid,uuid,uuid,uuid,bytea,bytea,timestamptz,text,text,text[],bytea,bytea),
 public.privacy_restore_begin_replay(uuid,uuid),
 public.privacy_restore_apply_relational_operation(uuid,uuid,timestamptz,text),
 public.privacy_restore_execute_checkpoint(uuid,uuid,smallint,text,text,bytea) FROM PUBLIC;
