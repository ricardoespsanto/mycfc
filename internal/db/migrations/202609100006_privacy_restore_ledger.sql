-- #247: restore-independent erasure tombstone receipts and a mandatory guard
-- before destructive relational checkpoints. The external ledger and worker
-- remain unconfigured and disabled by default.

CREATE TABLE privacy_protected.restore_tombstone_receipts (
 execution_id uuid PRIMARY KEY REFERENCES public.privacy_erasure_executions(id) ON DELETE RESTRICT,
 request_id uuid NOT NULL REFERENCES public.data_erasure_requests(id) ON DELETE RESTRICT,
 ledger_version varchar(40) NOT NULL CHECK(ledger_version='restore-tombstone/v1'),
 encryption_key_id varchar(80) NOT NULL CHECK(encryption_key_id=btrim(encryption_key_id) AND encryption_key_id~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'),
 locator_key_id varchar(80) NOT NULL CHECK(locator_key_id=btrim(locator_key_id) AND locator_key_id~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'),
 locator_digest bytea NOT NULL CHECK(octet_length(locator_digest)=32),
 object_version_id varchar(1024) NOT NULL CHECK(object_version_id=btrim(object_version_id) AND char_length(object_version_id) BETWEEN 1 AND 1024),
 ciphertext_sha256 bytea NOT NULL CHECK(octet_length(ciphertext_sha256)=32),
 size_bytes bigint NOT NULL CHECK(size_bytes BETWEEN 1 AND 1048576),
 written_at timestamptz NOT NULL,
 verified_at timestamptz NOT NULL,
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK(verified_at>=written_at),
 UNIQUE(request_id), UNIQUE(locator_key_id,locator_digest)
);
CREATE TRIGGER privacy_restore_tombstone_receipts_immutable BEFORE UPDATE OR DELETE
 ON privacy_protected.restore_tombstone_receipts FOR EACH ROW EXECUTE FUNCTION public.prevent_privacy_execution_record_delete();

CREATE TABLE privacy_protected.restore_tombstone_closure_intents (
 execution_id uuid PRIMARY KEY REFERENCES public.privacy_erasure_executions(id) ON DELETE RESTRICT,
 closed_at timestamptz NOT NULL, evidence_expires_at timestamptz NOT NULL,
 prepared_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK(evidence_expires_at=closed_at+interval '24 months')
);
CREATE TRIGGER privacy_restore_tombstone_closure_intents_immutable BEFORE UPDATE OR DELETE
 ON privacy_protected.restore_tombstone_closure_intents FOR EACH ROW EXECUTE FUNCTION public.prevent_privacy_execution_record_delete();

CREATE TABLE privacy_protected.restore_tombstone_closure_receipts (
 execution_id uuid PRIMARY KEY REFERENCES privacy_protected.restore_tombstone_closure_intents(execution_id) ON DELETE RESTRICT,
 ledger_version varchar(40) NOT NULL CHECK(ledger_version='restore-tombstone-closure/v1'),
 encryption_key_id varchar(80) NOT NULL CHECK(encryption_key_id=btrim(encryption_key_id) AND encryption_key_id~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'),
 locator_key_id varchar(80) NOT NULL CHECK(locator_key_id=btrim(locator_key_id) AND locator_key_id~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'),
 locator_digest bytea NOT NULL CHECK(octet_length(locator_digest)=32),
 object_version_id varchar(1024) NOT NULL CHECK(object_version_id=btrim(object_version_id) AND char_length(object_version_id) BETWEEN 1 AND 1024),
 ciphertext_sha256 bytea NOT NULL CHECK(octet_length(ciphertext_sha256)=32),
 size_bytes bigint NOT NULL CHECK(size_bytes BETWEEN 1 AND 1048576),
 written_at timestamptz NOT NULL, verified_at timestamptz NOT NULL,
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(), CHECK(verified_at>=written_at),
 UNIQUE(locator_key_id,locator_digest)
);
CREATE TRIGGER privacy_restore_tombstone_closure_receipts_immutable BEFORE UPDATE OR DELETE
 ON privacy_protected.restore_tombstone_closure_receipts FOR EACH ROW EXECUTE FUNCTION public.prevent_privacy_execution_record_delete();

CREATE FUNCTION public.privacy_tombstone_prepare(
 p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_lease_epoch bigint,p_worker_ref uuid
) RETURNS TABLE(
 execution_id uuid,request_id uuid,request_ref uuid,subject_user_id uuid,
 plan_sha256 bytea,workset_sha256 bytea,execution_started_at timestamptz
) LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT execution.id,request.id,request.public_ref,COALESCE(request.subject_user_id,request.requester_user_id),
        execution.plan_sha256,
        digest(convert_to(string_agg(
          job.plan_entry_position::text||':'||encode(job.entry_sha256,'hex')||':'||checkpoint.operation_position::text||':'||checkpoint.operation_code||':'||checkpoint.action_version,
          '|' ORDER BY job.plan_entry_position,checkpoint.operation_position
        ),'UTF8'),'sha256'),
        execution.accepted_at
 FROM privacy_erasure_job_leases lease
 JOIN privacy_erasure_category_jobs selected_job ON selected_job.id=lease.job_id AND selected_job.lease_epoch=lease.epoch
 JOIN privacy_erasure_job_attempts attempt ON attempt.lease_id=lease.id AND attempt.job_id=selected_job.id
 JOIN privacy_erasure_executions execution ON execution.id=selected_job.execution_id
 JOIN data_erasure_requests request ON request.id=execution.request_id
 JOIN privacy_erasure_category_jobs job ON job.execution_id=execution.id
 JOIN privacy_erasure_job_checkpoints checkpoint ON checkpoint.job_id=job.id
 WHERE selected_job.id=p_job_id AND lease.id=p_lease_id AND attempt.id=p_attempt_id
   AND lease.epoch=p_lease_epoch AND lease.worker_ref=p_worker_ref
   AND selected_job.status='LEASED' AND lease.released_at IS NULL AND attempt.finished_at IS NULL
   AND lease.expires_at>clock_timestamp()
   AND selected_job.category_key='backup-tombstones'
   AND EXISTS(SELECT 1 FROM privacy_erasure_job_checkpoints selected_checkpoint
     WHERE selected_checkpoint.job_id=selected_job.id AND selected_checkpoint.operation_code='BACKUP_TOMBSTONE_REPLAY'
       AND selected_checkpoint.action_version='v1' AND selected_checkpoint.status='PENDING')
   AND COALESCE(request.subject_user_id,request.requester_user_id) IS NOT NULL
 GROUP BY execution.id,request.id,request.public_ref,request.subject_user_id,request.requester_user_id;
$$;

CREATE FUNCTION public.privacy_tombstone_confirm(
 p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_lease_epoch bigint,p_worker_ref uuid,
 p_ledger_version text,p_encryption_key_id text,p_locator_key_id text,p_locator_digest bytea,
 p_object_version_id text,p_ciphertext_sha256 bytea,p_size_bytes bigint,p_written_at timestamptz,p_verified_at timestamptz
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE checkpoint_ref uuid; execution_ref uuid; request_ref uuid; existing privacy_protected.restore_tombstone_receipts%ROWTYPE;
BEGIN
 SELECT checkpoint.id,job.execution_id,execution.request_id INTO checkpoint_ref,execution_ref,request_ref
 FROM privacy_erasure_category_jobs job
 JOIN privacy_erasure_job_leases lease ON lease.job_id=job.id AND lease.epoch=job.lease_epoch
 JOIN privacy_erasure_job_attempts attempt ON attempt.lease_id=lease.id AND attempt.job_id=job.id
 JOIN privacy_erasure_executions execution ON execution.id=job.execution_id
 JOIN privacy_erasure_job_checkpoints checkpoint ON checkpoint.job_id=job.id
 WHERE job.id=p_job_id AND lease.id=p_lease_id AND attempt.id=p_attempt_id
  AND lease.epoch=p_lease_epoch AND lease.worker_ref=p_worker_ref AND job.status='LEASED'
  AND lease.released_at IS NULL AND attempt.finished_at IS NULL AND lease.expires_at>clock_timestamp()
  AND job.category_key='backup-tombstones' AND checkpoint.operation_code='BACKUP_TOMBSTONE_REPLAY'
  AND checkpoint.action_version='v1' FOR UPDATE OF job,lease,attempt,checkpoint;
 IF checkpoint_ref IS NULL OR p_ledger_version<>'restore-tombstone/v1'
  OR p_encryption_key_id IS NULL OR p_encryption_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'
  OR p_locator_key_id IS NULL OR p_locator_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'
  OR octet_length(p_locator_digest)<>32 OR octet_length(p_ciphertext_sha256)<>32
  OR p_object_version_id IS NULL OR p_object_version_id<>btrim(p_object_version_id) OR char_length(p_object_version_id) NOT BETWEEN 1 AND 1024
  OR p_size_bytes NOT BETWEEN 1 AND 1048576 OR p_written_at IS NULL OR p_verified_at<p_written_at THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_tombstone_receipt_rejected';
 END IF;
 SELECT * INTO existing FROM privacy_protected.restore_tombstone_receipts WHERE execution_id=execution_ref;
 IF existing.execution_id IS NULL THEN
  INSERT INTO privacy_protected.restore_tombstone_receipts(
   execution_id,request_id,ledger_version,encryption_key_id,locator_key_id,locator_digest,object_version_id,
   ciphertext_sha256,size_bytes,written_at,verified_at
  ) VALUES(execution_ref,request_ref,p_ledger_version,p_encryption_key_id,p_locator_key_id,p_locator_digest,p_object_version_id,
    p_ciphertext_sha256,p_size_bytes,p_written_at,p_verified_at);
 ELSIF ROW(existing.request_id,existing.ledger_version,existing.encryption_key_id,existing.locator_key_id,existing.locator_digest,
   existing.object_version_id,existing.ciphertext_sha256,existing.size_bytes,existing.written_at,existing.verified_at)
   IS DISTINCT FROM ROW(request_ref,p_ledger_version,p_encryption_key_id,p_locator_key_id,p_locator_digest,
   p_object_version_id,p_ciphertext_sha256,p_size_bytes,p_written_at,p_verified_at) THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_tombstone_receipt_conflict';
 END IF;
 UPDATE privacy_erasure_job_checkpoints SET status='SUCCEEDED',completed_by_attempt_id=p_attempt_id,completed_at=clock_timestamp(),
  affected_rows=1,result_sha256=digest(p_ciphertext_sha256||convert_to(p_object_version_id,'UTF8'),'sha256')
 WHERE id=checkpoint_ref AND status='PENDING';
 RETURN checkpoint_ref;
END;
$$;

CREATE FUNCTION public.privacy_tombstone_prepare_closure(p_execution_id uuid,p_worker_ref uuid)
RETURNS TABLE(
 execution_id uuid,request_id uuid,request_ref uuid,subject_user_id uuid,plan_sha256 bytea,workset_sha256 bytea,
 execution_started_at timestamptz,closed_at timestamptz,evidence_expires_at timestamptz
) LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
#variable_conflict use_column
DECLARE v_closed_at timestamptz;
BEGIN
 IF p_execution_id IS NULL OR p_worker_ref IS NULL OR NOT EXISTS(
  SELECT 1 FROM privacy_erasure_executions execution
  JOIN data_erasure_requests request ON request.id=execution.request_id
  WHERE execution.id=p_execution_id AND execution.status='SUCCEEDED' AND request.status IN ('PROCESSING','RETRYABLE_FAILED')
   AND NOT EXISTS(SELECT 1 FROM privacy_erasure_category_jobs job WHERE job.execution_id=execution.id AND job.status<>'SUCCEEDED')
   AND EXISTS(SELECT 1 FROM privacy_protected.restore_tombstone_receipts receipt WHERE receipt.execution_id=execution.id)
 ) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_tombstone_closure_unavailable'; END IF;
	 v_closed_at:=clock_timestamp();
 INSERT INTO privacy_protected.restore_tombstone_closure_intents(execution_id,closed_at,evidence_expires_at)
 VALUES(p_execution_id,v_closed_at,v_closed_at+interval '24 months') ON CONFLICT(execution_id) DO NOTHING;
 RETURN QUERY
 SELECT execution.id,request.id,request.public_ref,COALESCE(request.subject_user_id,request.requester_user_id),execution.plan_sha256,
  digest(convert_to(string_agg(job.plan_entry_position::text||':'||encode(job.entry_sha256,'hex')||':'||checkpoint.operation_position::text||':'||checkpoint.operation_code||':'||checkpoint.action_version,
   '|' ORDER BY job.plan_entry_position,checkpoint.operation_position),'UTF8'),'sha256'),execution.accepted_at,intent.closed_at,intent.evidence_expires_at
 FROM privacy_erasure_executions execution JOIN data_erasure_requests request ON request.id=execution.request_id
 JOIN privacy_erasure_category_jobs job ON job.execution_id=execution.id JOIN privacy_erasure_job_checkpoints checkpoint ON checkpoint.job_id=job.id
 JOIN privacy_protected.restore_tombstone_closure_intents intent ON intent.execution_id=execution.id
 WHERE execution.id=p_execution_id AND COALESCE(request.subject_user_id,request.requester_user_id) IS NOT NULL
 GROUP BY execution.id,request.id,request.public_ref,request.subject_user_id,request.requester_user_id,intent.closed_at,intent.evidence_expires_at;
END; $$;

CREATE FUNCTION public.privacy_tombstone_confirm_closure(
 p_execution_id uuid,p_worker_ref uuid,p_ledger_version text,p_encryption_key_id text,p_locator_key_id text,p_locator_digest bytea,
 p_object_version_id text,p_ciphertext_sha256 bytea,p_size_bytes bigint,p_written_at timestamptz,p_verified_at timestamptz
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE intent privacy_protected.restore_tombstone_closure_intents%ROWTYPE; existing privacy_protected.restore_tombstone_closure_receipts%ROWTYPE;
BEGIN
 SELECT * INTO intent FROM privacy_protected.restore_tombstone_closure_intents WHERE execution_id=p_execution_id;
 IF intent.execution_id IS NULL OR p_worker_ref IS NULL OR p_ledger_version<>'restore-tombstone-closure/v1'
  OR p_encryption_key_id IS NULL OR p_encryption_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'
  OR p_locator_key_id IS NULL OR p_locator_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'
  OR octet_length(p_locator_digest)<>32 OR octet_length(p_ciphertext_sha256)<>32 OR p_object_version_id IS NULL
  OR p_object_version_id<>btrim(p_object_version_id) OR char_length(p_object_version_id) NOT BETWEEN 1 AND 1024
  OR p_size_bytes NOT BETWEEN 1 AND 1048576 OR p_written_at IS NULL OR p_verified_at<p_written_at
  OR p_verified_at>intent.evidence_expires_at THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_tombstone_closure_receipt_rejected'; END IF;
 SELECT * INTO existing FROM privacy_protected.restore_tombstone_closure_receipts WHERE execution_id=p_execution_id;
 IF existing.execution_id IS NULL THEN
  INSERT INTO privacy_protected.restore_tombstone_closure_receipts(execution_id,ledger_version,encryption_key_id,locator_key_id,locator_digest,
   object_version_id,ciphertext_sha256,size_bytes,written_at,verified_at)
  VALUES(p_execution_id,p_ledger_version,p_encryption_key_id,p_locator_key_id,p_locator_digest,p_object_version_id,p_ciphertext_sha256,p_size_bytes,p_written_at,p_verified_at);
 ELSIF ROW(existing.ledger_version,existing.encryption_key_id,existing.locator_key_id,existing.locator_digest,existing.object_version_id,
   existing.ciphertext_sha256,existing.size_bytes,existing.written_at,existing.verified_at)
  IS DISTINCT FROM ROW(p_ledger_version,p_encryption_key_id,p_locator_key_id,p_locator_digest,p_object_version_id,
   p_ciphertext_sha256,p_size_bytes,p_written_at,p_verified_at) THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_tombstone_closure_receipt_conflict'; END IF;
 RETURN p_execution_id;
END; $$;

-- Preserve the reviewed relational function as the implementation detail and
-- put the restore-safety invariant in a small auditable wrapper. Every
-- destructive checkpoint now requires the independently verified receipt.
ALTER FUNCTION public.privacy_worker_execute_checkpoint(uuid,uuid,uuid,bigint,uuid,text,text)
 RENAME TO privacy_worker_execute_checkpoint_without_tombstone_guard;
REVOKE ALL ON FUNCTION public.privacy_worker_execute_checkpoint_without_tombstone_guard(uuid,uuid,uuid,bigint,uuid,text,text) FROM PUBLIC;

CREATE FUNCTION public.privacy_worker_execute_checkpoint(
 p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_lease_epoch bigint,p_worker_ref uuid,p_operation_code text,p_action_version text
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE execution_ref uuid;
BEGIN
	SELECT job.execution_id INTO execution_ref
	FROM privacy_erasure_category_jobs job
	JOIN privacy_erasure_job_leases lease ON lease.id=p_lease_id AND lease.job_id=job.id AND lease.epoch=p_lease_epoch
	JOIN privacy_erasure_job_attempts attempt ON attempt.id=p_attempt_id AND attempt.job_id=job.id AND attempt.lease_id=lease.id AND attempt.lease_epoch=p_lease_epoch
	WHERE job.id=p_job_id AND job.status='LEASED' AND lease.worker_ref=p_worker_ref
	 AND lease.released_at IS NULL AND lease.expires_at>clock_timestamp() AND attempt.finished_at IS NULL
	FOR UPDATE OF job,lease,attempt;
	IF NOT FOUND THEN
	 RETURN public.privacy_worker_execute_checkpoint_without_tombstone_guard(
	  p_job_id,p_lease_id,p_attempt_id,p_lease_epoch,p_worker_ref,p_operation_code,p_action_version);
	END IF;
 IF p_operation_code<>'BACKUP_TOMBSTONE_REPLAY' AND NOT EXISTS(
  SELECT 1 FROM privacy_protected.restore_tombstone_receipts receipt WHERE receipt.execution_id=execution_ref
 ) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_tombstone_required'; END IF;
 RETURN public.privacy_worker_execute_checkpoint_without_tombstone_guard(
  p_job_id,p_lease_id,p_attempt_id,p_lease_epoch,p_worker_ref,p_operation_code,p_action_version);
END;
$$;

REVOKE ALL ON FUNCTION public.privacy_tombstone_prepare(uuid,uuid,uuid,bigint,uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.privacy_tombstone_confirm(uuid,uuid,uuid,bigint,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.privacy_tombstone_prepare_closure(uuid,uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.privacy_tombstone_confirm_closure(uuid,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.privacy_worker_execute_checkpoint(uuid,uuid,uuid,bigint,uuid,text,text) FROM PUBLIC;
