-- #248 completion, exceptional requeue and evidence-bound activation control.
-- This migration is additive and leaves privacy execution disabled. Migration
-- 202609100010 must be applied first in the release sequence.

ALTER TABLE privacy_erasure_category_jobs
 ADD COLUMN manual_attempt_allowance integer NOT NULL DEFAULT 0 CHECK(manual_attempt_allowance BETWEEN 0 AND 10);

CREATE TABLE privacy_protected.completion_notice_targets (
 execution_id uuid PRIMARY KEY REFERENCES public.privacy_erasure_executions(id) ON DELETE RESTRICT,
 sealed_delivery bytea NOT NULL CHECK(octet_length(sealed_delivery) BETWEEN 29 AND 8192),
 captured_at timestamptz NOT NULL
);
CREATE TRIGGER privacy_completion_notice_targets_immutable BEFORE UPDATE OR DELETE
 ON privacy_protected.completion_notice_targets FOR EACH ROW EXECUTE FUNCTION public.prevent_privacy_execution_record_delete();

CREATE TABLE privacy_erasure_completion_manifests (
 execution_id uuid PRIMARY KEY REFERENCES privacy_erasure_executions(id) ON DELETE RESTRICT,
 request_id uuid NOT NULL UNIQUE REFERENCES data_erasure_requests(id) ON DELETE RESTRICT,
 manifest_version varchar(40) NOT NULL CHECK(manifest_version='privacy-completion/v1'),
 plan_sha256 bytea NOT NULL CHECK(octet_length(plan_sha256)=32),
 manifest jsonb NOT NULL CHECK(jsonb_typeof(manifest)='object' AND octet_length(manifest::text)<=262144),
 manifest_sha256 bytea NOT NULL CHECK(octet_length(manifest_sha256)=32),
 category_count integer NOT NULL CHECK(category_count BETWEEN 1 AND 50),
 checkpoint_count integer NOT NULL CHECK(checkpoint_count BETWEEN 1 AND 5000),
 object_target_count integer NOT NULL CHECK(object_target_count>=0),
 provider_target_count integer NOT NULL CHECK(provider_target_count>=0),
 completed_by_ref uuid NOT NULL,completed_at timestamptz NOT NULL,evidence_expires_at timestamptz NOT NULL,
 CHECK(evidence_expires_at=completed_at+interval '24 months'),
 UNIQUE(manifest_version,manifest_sha256)
);
CREATE TRIGGER privacy_erasure_completion_manifests_immutable BEFORE UPDATE OR DELETE
 ON privacy_erasure_completion_manifests FOR EACH ROW EXECUTE FUNCTION prevent_privacy_execution_record_delete();

CREATE TABLE privacy_completion_access_links (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),execution_id uuid NOT NULL UNIQUE REFERENCES privacy_erasure_completion_manifests(execution_id) ON DELETE RESTRICT,
 token_sha256 bytea NOT NULL UNIQUE CHECK(octet_length(token_sha256)=32),
 created_at timestamptz NOT NULL,expires_at timestamptz NOT NULL,used_at timestamptz NULL,
 CHECK(expires_at=created_at+interval '24 hours'),CHECK(used_at IS NULL OR (used_at>=created_at AND used_at<=expires_at))
);
CREATE INDEX privacy_completion_access_links_expiry_idx ON privacy_completion_access_links(expires_at,id) WHERE used_at IS NULL;
CREATE FUNCTION protect_privacy_completion_access_link() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' OR NEW.id<>OLD.id OR NEW.execution_id<>OLD.execution_id OR NEW.token_sha256<>OLD.token_sha256
  OR NEW.created_at<>OLD.created_at OR NEW.expires_at<>OLD.expires_at OR OLD.used_at IS NOT NULL OR NEW.used_at IS NULL THEN
  RAISE EXCEPTION 'privacy completion access link is immutable';
 END IF;
 RETURN NEW;
END;$$;
CREATE TRIGGER privacy_completion_access_links_protected BEFORE UPDATE OR DELETE ON privacy_completion_access_links
 FOR EACH ROW EXECUTE FUNCTION protect_privacy_completion_access_link();

-- Capture only the already-encrypted generic recipient used for the processing
-- notice. The completion worker can recover it without retaining cleartext
-- identity after account erasure.
CREATE FUNCTION privacy_execution_capture_completion_notice(p_execution_id uuid,p_processing_event_id uuid)
RETURNS uuid LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 INSERT INTO privacy_protected.completion_notice_targets(execution_id,sealed_delivery,captured_at)
 SELECT execution.id,outbox.sealed_payload,clock_timestamp()
 FROM privacy_erasure_executions execution
 JOIN email_outbox outbox ON outbox.privacy_request_id=execution.request_id
 WHERE execution.id=p_execution_id AND outbox.privacy_event_key=p_processing_event_id
  AND outbox.message_type='PRIVACY_PROCESSING_STARTED' AND outbox.sealed_payload IS NOT NULL
 ON CONFLICT(execution_id) DO NOTHING RETURNING execution_id;
$$;

CREATE FUNCTION privacy_completion_prepare(p_execution_id uuid,p_worker_ref uuid)
RETURNS TABLE(sealed_delivery bytea) LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT target.sealed_delivery
 FROM privacy_erasure_executions execution
 JOIN data_erasure_requests request ON request.id=execution.request_id
 JOIN privacy_protected.completion_notice_targets target ON target.execution_id=execution.id
 JOIN privacy_protected.restore_tombstone_closure_intents intent ON intent.execution_id=execution.id
 JOIN privacy_protected.restore_tombstone_closure_receipts receipt ON receipt.execution_id=execution.id
 WHERE execution.id=p_execution_id AND p_worker_ref IS NOT NULL AND execution.status='SUCCEEDED'
  AND request.status IN ('PROCESSING','RETRYABLE_FAILED')
  AND receipt.ledger_version='restore-tombstone-closure/v2'
  AND receipt.verified_at>=receipt.written_at AND receipt.verified_at<=intent.evidence_expires_at
  AND NOT EXISTS(SELECT 1 FROM privacy_erasure_completion_manifests manifest WHERE manifest.execution_id=execution.id)
  AND NOT EXISTS(SELECT 1 FROM privacy_erasure_category_jobs job WHERE job.execution_id=execution.id AND job.status<>'SUCCEEDED')
 FOR SHARE OF execution,request,target,intent,receipt;
$$;

CREATE FUNCTION privacy_completion_list_pending(p_worker_ref uuid,p_limit integer)
RETURNS TABLE(execution_id uuid) LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT execution.id
 FROM privacy_erasure_executions execution
 JOIN data_erasure_requests request ON request.id=execution.request_id
 JOIN privacy_protected.completion_notice_targets target ON target.execution_id=execution.id
 WHERE p_worker_ref IS NOT NULL AND p_limit BETWEEN 1 AND 100
  AND execution.status='SUCCEEDED' AND request.status IN ('PROCESSING','RETRYABLE_FAILED')
  AND NOT EXISTS(SELECT 1 FROM privacy_erasure_completion_manifests manifest WHERE manifest.execution_id=execution.id)
  AND NOT EXISTS(SELECT 1 FROM privacy_erasure_category_jobs job WHERE job.execution_id=execution.id AND job.status<>'SUCCEEDED')
 ORDER BY execution.finished_at,execution.id LIMIT p_limit;
$$;

-- The finalizer locks and reconstructs every evidence-bearing work row. It
-- accepts no caller-supplied counts, category outcomes or completion time.
CREATE FUNCTION privacy_completion_finalize(
 p_execution_id uuid,p_worker_ref uuid,p_token_sha256 bytea,p_sealed_delivery bytea
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE execution privacy_erasure_executions%ROWTYPE;request data_erasure_requests%ROWTYPE;plan privacy_request_execution_plans%ROWTYPE;
 intent privacy_protected.restore_tombstone_closure_intents%ROWTYPE;receipt privacy_protected.restore_tombstone_closure_receipts%ROWTYPE;
 category_total integer;checkpoint_total integer;object_total integer;provider_total integer;object_evidence_total integer;provider_evidence_total integer;
 document jsonb;document_digest bytea;event_id uuid;request_version bigint;finalized_at timestamptz;
BEGIN
 IF p_execution_id IS NULL OR p_worker_ref IS NULL OR octet_length(p_token_sha256)<>32 OR octet_length(p_sealed_delivery) NOT BETWEEN 29 AND 8192 THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy_completion_input_rejected'; END IF;
 SELECT * INTO execution FROM privacy_erasure_executions WHERE id=p_execution_id FOR UPDATE;
 IF execution.id IS NULL THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_completion_unavailable'; END IF;
 SELECT * INTO request FROM data_erasure_requests WHERE id=execution.request_id FOR UPDATE;
 SELECT * INTO plan FROM privacy_request_execution_plans WHERE request_id=execution.request_id FOR SHARE;
 SELECT * INTO intent FROM privacy_protected.restore_tombstone_closure_intents WHERE execution_id=execution.id FOR SHARE;
 SELECT * INTO receipt FROM privacy_protected.restore_tombstone_closure_receipts WHERE execution_id=execution.id FOR SHARE;
 PERFORM 1 FROM privacy_erasure_category_jobs WHERE execution_id=execution.id ORDER BY plan_entry_position FOR UPDATE;
 PERFORM 1 FROM privacy_erasure_job_checkpoints checkpoint JOIN privacy_erasure_category_jobs job ON job.id=checkpoint.job_id
  WHERE job.execution_id=execution.id ORDER BY job.plan_entry_position,checkpoint.operation_position FOR UPDATE OF checkpoint;
 PERFORM 1 FROM privacy_protected.object_targets WHERE execution_id=execution.id ORDER BY id FOR SHARE;
 PERFORM 1 FROM privacy_protected.provider_targets WHERE execution_id=execution.id ORDER BY id FOR SHARE;

 IF execution.status<>'SUCCEEDED' OR request.status NOT IN ('PROCESSING','RETRYABLE_FAILED') OR plan.request_id IS NULL
  OR execution.plan_sha256<>plan.plan_sha256 OR execution.executor_version<>plan.executor_version OR execution.schema_version<>plan.schema_version
  OR intent.execution_id IS NULL OR receipt.execution_id IS NULL OR receipt.ledger_version<>'restore-tombstone-closure/v2'
  OR intent.closed_at<execution.finished_at OR receipt.verified_at<intent.closed_at OR receipt.verified_at<receipt.written_at OR receipt.verified_at>intent.evidence_expires_at
  OR NOT EXISTS(SELECT 1 FROM privacy_protected.restore_tombstone_receipts r WHERE r.execution_id=execution.id AND r.ledger_version='restore-tombstone/v2')
  OR NOT EXISTS(SELECT 1 FROM privacy_protected.completion_notice_targets n WHERE n.execution_id=execution.id)
  OR EXISTS(SELECT 1 FROM privacy_erasure_job_leases lease JOIN privacy_erasure_category_jobs job ON job.id=lease.job_id WHERE job.execution_id=execution.id AND lease.released_at IS NULL)
  OR EXISTS(SELECT 1 FROM privacy_erasure_category_jobs job WHERE job.execution_id=execution.id AND job.status<>'SUCCEEDED')
  OR EXISTS(SELECT 1 FROM privacy_erasure_job_checkpoints checkpoint JOIN privacy_erasure_category_jobs job ON job.id=checkpoint.job_id
      WHERE job.execution_id=execution.id AND (checkpoint.status<>'SUCCEEDED' OR checkpoint.affected_rows IS NULL OR checkpoint.result_sha256 IS NULL))
  OR EXISTS(SELECT 1 FROM privacy_erasure_category_jobs job WHERE job.execution_id=execution.id AND
      (job.category_key IS DISTINCT FROM plan.plan->'entries'->(job.plan_entry_position-1)->>'category'
       OR job.purpose_code IS DISTINCT FROM plan.plan->'entries'->(job.plan_entry_position-1)->>'purpose'))
  OR EXISTS(SELECT 1 FROM privacy_erasure_job_checkpoints checkpoint JOIN privacy_erasure_category_jobs job ON job.id=checkpoint.job_id
      WHERE job.execution_id=execution.id AND (checkpoint.operation_code IS DISTINCT FROM plan.plan->'entries'->(job.plan_entry_position-1)->'operations'->>(checkpoint.operation_position-1)
       OR checkpoint.action_version IS DISTINCT FROM plan.plan->'entries'->(job.plan_entry_position-1)->>'action_version'))
  OR (SELECT count(*) FROM privacy_erasure_category_jobs job WHERE job.execution_id=execution.id)<>(SELECT count(*) FROM jsonb_array_elements(plan.plan->'entries'))
  OR EXISTS(SELECT 1 FROM privacy_erasure_category_jobs job WHERE job.execution_id=execution.id AND
       (SELECT count(*) FROM privacy_erasure_job_checkpoints c WHERE c.job_id=job.id)<>(SELECT count(*) FROM jsonb_array_elements(plan.plan->'entries'->(job.plan_entry_position-1)->'operations')))
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_completion_evidence_incomplete'; END IF;

 SELECT count(DISTINCT job.id),count(checkpoint.*) INTO category_total,checkpoint_total FROM privacy_erasure_category_jobs job
 JOIN privacy_erasure_job_checkpoints checkpoint ON checkpoint.job_id=job.id WHERE job.execution_id=execution.id;
 SELECT count(*),(SELECT count(*) FROM privacy_protected.object_evidence evidence JOIN privacy_protected.object_targets target ON target.id=evidence.target_id WHERE target.execution_id=execution.id)
 INTO object_total,object_evidence_total FROM privacy_protected.object_targets WHERE execution_id=execution.id;
 SELECT count(*),(SELECT count(*) FROM privacy_protected.provider_evidence evidence JOIN privacy_protected.provider_targets target ON target.id=evidence.target_id WHERE target.execution_id=execution.id)
 INTO provider_total,provider_evidence_total FROM privacy_protected.provider_targets WHERE execution_id=execution.id;

 IF object_total<>object_evidence_total
  OR EXISTS(SELECT 1 FROM privacy_protected.object_capture_sets capture WHERE capture.execution_id=execution.id AND
      (capture.expected_target_count<>(SELECT count(*) FROM privacy_protected.object_targets target WHERE target.checkpoint_id=capture.checkpoint_id)
       OR capture.expected_target_count<>(SELECT count(*) FROM privacy_protected.object_evidence evidence JOIN privacy_protected.object_targets target ON target.id=evidence.target_id WHERE target.checkpoint_id=capture.checkpoint_id)
       OR EXISTS(SELECT 1 FROM privacy_protected.object_targets target WHERE target.checkpoint_id=capture.checkpoint_id
          AND NOT EXISTS(SELECT 1 FROM privacy_protected.object_target_digests digest_row WHERE digest_row.target_id=target.id))))
  OR EXISTS(SELECT 1 FROM privacy_erasure_job_checkpoints checkpoint JOIN privacy_erasure_category_jobs job ON job.id=checkpoint.job_id
      WHERE job.execution_id=execution.id AND checkpoint.operation_code='OBJECT_VERSION_DELETE'
       AND NOT EXISTS(SELECT 1 FROM privacy_protected.object_capture_sets capture WHERE capture.checkpoint_id=checkpoint.id))
  OR provider_total<>provider_evidence_total
  OR EXISTS(SELECT 1 FROM privacy_protected.provider_capture_sets capture WHERE capture.execution_id=execution.id AND
      (capture.expected_target_count<>(SELECT count(*) FROM privacy_protected.provider_targets target WHERE target.checkpoint_id=capture.checkpoint_id)
       OR capture.expected_target_count<>(SELECT count(*) FROM privacy_protected.provider_evidence evidence JOIN privacy_protected.provider_targets target ON target.id=evidence.target_id WHERE target.checkpoint_id=capture.checkpoint_id)
       OR EXISTS(SELECT 1 FROM privacy_protected.provider_targets target WHERE target.checkpoint_id=capture.checkpoint_id
          AND NOT EXISTS(SELECT 1 FROM privacy_protected.provider_target_digests digest_row WHERE digest_row.target_id=target.id))
       OR EXISTS(SELECT 1 FROM privacy_protected.provider_credential_quarantine q JOIN privacy_protected.provider_targets target ON target.id=q.target_id WHERE target.checkpoint_id=capture.checkpoint_id)))
  OR EXISTS(SELECT 1 FROM privacy_erasure_job_checkpoints checkpoint JOIN privacy_erasure_category_jobs job ON job.id=checkpoint.job_id
      WHERE job.execution_id=execution.id AND checkpoint.operation_code='PROVIDER_RECIPIENT_NOTIFY'
       AND NOT EXISTS(SELECT 1 FROM privacy_protected.provider_capture_sets capture WHERE capture.checkpoint_id=checkpoint.id))
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_completion_target_evidence_incomplete'; END IF;

 SELECT jsonb_build_object(
  'version','privacy-completion/v1','execution_id',execution.id,'request_id',request.id,'plan_sha256',encode(execution.plan_sha256,'hex'),
  'work',COALESCE((SELECT jsonb_agg(jsonb_build_object('position',job.plan_entry_position,'entry_sha256',encode(job.entry_sha256,'hex'),
    'category',job.category_key,'purpose',job.purpose_code,'checkpoints',(SELECT jsonb_agg(jsonb_build_object('position',checkpoint.operation_position,
      'operation',checkpoint.operation_code,'action_version',checkpoint.action_version,'affected_rows',checkpoint.affected_rows,
      'result_sha256',encode(checkpoint.result_sha256,'hex')) ORDER BY checkpoint.operation_position) FROM privacy_erasure_job_checkpoints checkpoint WHERE checkpoint.job_id=job.id))
    ORDER BY job.plan_entry_position) FROM privacy_erasure_category_jobs job WHERE job.execution_id=execution.id),'[]'::jsonb),
  'objects',jsonb_build_object('targets',object_total,'evidence',object_evidence_total,'sha256',encode(digest(convert_to(COALESCE((SELECT string_agg(
    target.id::text||':'||digest_row.digest_key_id||':'||encode(digest_row.locator_digest,'hex')||':'||evidence.outcome_code||':'||encode(evidence.transcript_digest,'hex'),
    '|' ORDER BY target.id,digest_row.digest_key_id) FROM privacy_protected.object_targets target JOIN privacy_protected.object_target_digests digest_row ON digest_row.target_id=target.id
    JOIN privacy_protected.object_evidence evidence ON evidence.target_id=target.id WHERE target.execution_id=execution.id),''),'UTF8'),'sha256'),'hex')),
  'providers',jsonb_build_object('targets',provider_total,'evidence',provider_evidence_total,'sha256',encode(digest(convert_to(COALESCE((SELECT string_agg(
    target.id::text||':'||digest_row.digest_key_id||':'||encode(digest_row.target_digest,'hex')||':'||evidence.outcome_code||':'||evidence.evidence_code||':'||encode(evidence.transcript_digest,'hex'),
    '|' ORDER BY target.id,digest_row.digest_key_id) FROM privacy_protected.provider_targets target JOIN privacy_protected.provider_target_digests digest_row ON digest_row.target_id=target.id
    JOIN privacy_protected.provider_evidence evidence ON evidence.target_id=target.id WHERE target.execution_id=execution.id),''),'UTF8'),'sha256'),'hex')),
  'ledger',jsonb_build_object('intent_sha256',encode((SELECT ciphertext_sha256 FROM privacy_protected.restore_tombstone_receipts WHERE execution_id=execution.id),'hex'),
    'closure_sha256',encode(receipt.ciphertext_sha256,'hex'))
 ) INTO document;
 document_digest:=digest(convert_to(document::text,'UTF8'),'sha256');
	finalized_at:=clock_timestamp();

 INSERT INTO privacy_erasure_completion_manifests(execution_id,request_id,manifest_version,plan_sha256,manifest,manifest_sha256,
  category_count,checkpoint_count,object_target_count,provider_target_count,completed_by_ref,completed_at,evidence_expires_at)
 VALUES(execution.id,request.id,'privacy-completion/v1',execution.plan_sha256,document,document_digest,category_total,checkpoint_total,
  object_total,provider_total,p_worker_ref,intent.closed_at,intent.evidence_expires_at);
 INSERT INTO privacy_completion_access_links(execution_id,token_sha256,created_at,expires_at)
 VALUES(execution.id,p_token_sha256,finalized_at,finalized_at+interval '24 hours');
 UPDATE data_erasure_requests SET status='COMPLETED',version=version+1,closed_at=intent.closed_at,evidence_expires_at=intent.evidence_expires_at,
  working_expires_at=intent.closed_at+interval '90 days',updated_at=intent.closed_at WHERE id=request.id RETURNING version INTO request_version;
 INSERT INTO data_erasure_request_events(request_id,actor_role,actor_ref,action,reason_code,from_status,to_status,version,occurred_at)
 VALUES(request.id,'SYSTEM',p_worker_ref,'COMPLETED','EXECUTION_COMPLETED',request.status,'COMPLETED',request_version,intent.closed_at) RETURNING id INTO event_id;
 INSERT INTO email_outbox(message_type,privacy_request_id,privacy_requester_id,privacy_event_key,sealed_payload,next_attempt_at,created_at,updated_at)
 VALUES('PRIVACY_COMPLETED',request.id,request.requester_user_id,event_id,p_sealed_delivery,finalized_at,finalized_at,finalized_at);
 RETURN execution.id;
END;$$;

CREATE FUNCTION privacy_completion_consume(p_token_sha256 bytea)
RETURNS TABLE(request_ref uuid,completed_at timestamptz,summary jsonb) LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE link privacy_completion_access_links%ROWTYPE;
BEGIN
 IF octet_length(p_token_sha256)<>32 THEN RETURN; END IF;
 SELECT * INTO link FROM privacy_completion_access_links WHERE token_sha256=p_token_sha256 FOR UPDATE;
 IF link.id IS NULL OR link.used_at IS NOT NULL OR link.expires_at<=clock_timestamp() THEN RETURN; END IF;
 UPDATE privacy_completion_access_links SET used_at=clock_timestamp() WHERE id=link.id;
 RETURN QUERY SELECT request.public_ref,manifest.completed_at,
  jsonb_build_object('status','COMPLETED','manifest_sha256',encode(manifest.manifest_sha256,'hex'),
   'categories',manifest.category_count,'checkpoints',manifest.checkpoint_count,
   'object_targets',manifest.object_target_count,'provider_targets',manifest.provider_target_count)
 FROM privacy_erasure_completion_manifests manifest JOIN data_erasure_requests request ON request.id=manifest.request_id
 WHERE manifest.execution_id=link.execution_id;
END;$$;

CREATE FUNCTION privacy_completion_validate(p_token_sha256 bytea)
RETURNS boolean LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT octet_length(p_token_sha256)=32 AND EXISTS(
  SELECT 1 FROM privacy_completion_access_links link
  WHERE link.token_sha256=p_token_sha256 AND link.used_at IS NULL AND link.expires_at>clock_timestamp()
 );
$$;

CREATE FUNCTION privacy_completion_notice_deliverable(p_request_id uuid,p_at timestamptz)
RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT EXISTS(SELECT 1 FROM privacy_erasure_completion_manifests manifest
  JOIN privacy_completion_access_links link ON link.execution_id=manifest.execution_id
  WHERE manifest.request_id=p_request_id AND link.used_at IS NULL AND link.expires_at>p_at);
$$;

CREATE TABLE privacy_terminal_requeue_proposals (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),job_id uuid NOT NULL REFERENCES privacy_erasure_category_jobs(id) ON DELETE RESTRICT,
 execution_id uuid NOT NULL REFERENCES privacy_erasure_executions(id) ON DELETE RESTRICT,failure_id uuid NOT NULL UNIQUE REFERENCES privacy_erasure_failures(id) ON DELETE RESTRICT,
 expected_execution_version bigint NOT NULL CHECK(expected_execution_version>0),expected_lease_epoch bigint NOT NULL CHECK(expected_lease_epoch>0),
 proposal_sha256 bytea NOT NULL UNIQUE CHECK(octet_length(proposal_sha256)=32),proposed_by_ref uuid NOT NULL,proposed_at timestamptz NOT NULL
);
CREATE TABLE privacy_terminal_requeue_approvals (
 proposal_id uuid PRIMARY KEY REFERENCES privacy_terminal_requeue_proposals(id) ON DELETE RESTRICT,
 proposal_sha256 bytea NOT NULL CHECK(octet_length(proposal_sha256)=32),approved_by_ref uuid NOT NULL,approved_at timestamptz NOT NULL,
 CHECK(octet_length(proposal_sha256)=32)
);
CREATE TRIGGER privacy_terminal_requeue_proposals_immutable BEFORE UPDATE OR DELETE ON privacy_terminal_requeue_proposals
 FOR EACH ROW EXECUTE FUNCTION prevent_privacy_execution_record_delete();
CREATE TRIGGER privacy_terminal_requeue_approvals_immutable BEFORE UPDATE OR DELETE ON privacy_terminal_requeue_approvals
 FOR EACH ROW EXECUTE FUNCTION prevent_privacy_execution_record_delete();

CREATE FUNCTION privacy_terminal_requeue_propose(p_job_id uuid,p_actor uuid)
RETURNS TABLE(proposal_id uuid,proposal_sha256 bytea) LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE job privacy_erasure_category_jobs%ROWTYPE;execution privacy_erasure_executions%ROWTYPE;failure privacy_erasure_failures%ROWTYPE;digest_value bytea;
BEGIN
 IF NOT EXISTS(SELECT 1 FROM users account JOIN privacy_executor_grants grant_row ON grant_row.user_id=account.id AND grant_row.revoked_at IS NULL
   WHERE account.id=p_actor AND account.is_active AND NOT account.is_dependent) THEN
  RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_requeue_forbidden'; END IF;
 SELECT * INTO job FROM privacy_erasure_category_jobs WHERE id=p_job_id FOR UPDATE;
 SELECT * INTO execution FROM privacy_erasure_executions WHERE id=job.execution_id FOR UPDATE;
 SELECT failure_row.* INTO failure FROM privacy_erasure_failures failure_row WHERE failure_row.job_id=job.id AND failure_row.classification='TERMINAL'
  ORDER BY failure_row.occurred_at DESC,failure_row.id DESC LIMIT 1 FOR SHARE;
 IF job.id IS NULL OR job.status<>'TERMINAL_FAILED' OR execution.status<>'TERMINAL_FAILED' OR failure.id IS NULL OR job.manual_attempt_allowance>=10
  OR EXISTS(SELECT 1 FROM privacy_terminal_requeue_proposals proposal WHERE proposal.failure_id=failure.id) THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_requeue_unavailable'; END IF;
 digest_value:=digest(convert_to('privacy-terminal-requeue/v1:'||job.id::text||':'||execution.id::text||':'||failure.id::text||':'||
  execution.version::text||':'||job.lease_epoch::text||':'||job.attempt_count::text,'UTF8'),'sha256');
 RETURN QUERY INSERT INTO privacy_terminal_requeue_proposals(job_id,execution_id,failure_id,expected_execution_version,expected_lease_epoch,
  proposal_sha256,proposed_by_ref,proposed_at) VALUES(job.id,execution.id,failure.id,execution.version,job.lease_epoch,digest_value,p_actor,clock_timestamp())
 RETURNING id,privacy_terminal_requeue_proposals.proposal_sha256;
END;$$;

CREATE FUNCTION privacy_terminal_requeue_approve(p_proposal_id uuid,p_proposal_sha256 bytea,p_actor uuid)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE proposal privacy_terminal_requeue_proposals%ROWTYPE;job privacy_erasure_category_jobs%ROWTYPE;execution privacy_erasure_executions%ROWTYPE;
 request data_erasure_requests%ROWTYPE;new_version bigint;now_at timestamptz;
BEGIN
 IF NOT EXISTS(SELECT 1 FROM users account JOIN user_platform_roles assignment ON assignment.user_id=account.id JOIN platform_roles role ON role.id=assignment.role_id
   WHERE account.id=p_actor AND account.is_active AND NOT account.is_dependent AND role.code='ADMIN') THEN
  RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_requeue_forbidden'; END IF;
 SELECT * INTO proposal FROM privacy_terminal_requeue_proposals WHERE id=p_proposal_id FOR SHARE;
 IF proposal.id IS NULL OR proposal.proposed_by_ref=p_actor OR proposal.proposal_sha256<>p_proposal_sha256
  OR EXISTS(SELECT 1 FROM privacy_terminal_requeue_approvals approval WHERE approval.proposal_id=proposal.id) THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_requeue_unavailable'; END IF;
 SELECT * INTO job FROM privacy_erasure_category_jobs WHERE id=proposal.job_id FOR UPDATE;
 SELECT * INTO execution FROM privacy_erasure_executions WHERE id=proposal.execution_id FOR UPDATE;
 SELECT * INTO request FROM data_erasure_requests WHERE id=execution.request_id FOR UPDATE;
 IF job.status<>'TERMINAL_FAILED' OR execution.status<>'TERMINAL_FAILED' OR request.status<>'TERMINAL_FAILED'
  OR execution.version<>proposal.expected_execution_version OR job.lease_epoch<>proposal.expected_lease_epoch
  OR NOT EXISTS(SELECT 1 FROM privacy_erasure_failures failure WHERE failure.id=proposal.failure_id AND failure.job_id=job.id AND failure.classification='TERMINAL')
  OR EXISTS(SELECT 1 FROM privacy_erasure_failures later WHERE later.job_id=job.id AND later.occurred_at>(SELECT occurred_at FROM privacy_erasure_failures WHERE id=proposal.failure_id))
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_requeue_stale'; END IF;
 now_at:=clock_timestamp();
 INSERT INTO privacy_terminal_requeue_approvals VALUES(proposal.id,proposal.proposal_sha256,p_actor,now_at);
 UPDATE privacy_erasure_category_jobs SET status='PENDING',next_attempt_at=now_at,updated_at=now_at,completed_at=NULL,
  manual_attempt_allowance=manual_attempt_allowance+1 WHERE id=job.id;
 IF EXISTS(SELECT 1 FROM privacy_erasure_category_jobs sibling WHERE sibling.execution_id=execution.id AND sibling.id<>job.id AND sibling.status='TERMINAL_FAILED') THEN
  RETURN job.id;
 END IF;
 UPDATE privacy_erasure_executions SET status='RUNNING',version=version+1,finished_at=NULL,updated_at=now_at WHERE id=execution.id;
 UPDATE data_erasure_requests SET status='PROCESSING',version=version+1,updated_at=now_at WHERE id=request.id RETURNING version INTO new_version;
 INSERT INTO data_erasure_request_events(request_id,actor_role,actor_ref,action,reason_code,from_status,to_status,version,occurred_at)
 VALUES(request.id,'SYSTEM',p_actor,'EXECUTION_RESUMED','EXECUTION_RETRY_STARTED','TERMINAL_FAILED','PROCESSING',new_version,now_at);
 RETURN job.id;
END;$$;

CREATE FUNCTION privacy_completion_control_snapshot(p_actor uuid,p_request_reference uuid)
RETURNS TABLE(request_reference uuid,request_status text,execution_id uuid,execution_status text,job_id uuid,category_code text,purpose_code text,
 job_status text,attempt_count integer,failure_stage text,failure_code text,proposal_id uuid,proposal_sha256 bytea,proposed_at timestamptz,can_propose boolean,can_approve boolean)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE actor_executor boolean;actor_admin boolean;
BEGIN
 SELECT EXISTS(SELECT 1 FROM users account JOIN privacy_executor_grants grant_row ON grant_row.user_id=account.id AND grant_row.revoked_at IS NULL
   WHERE account.id=p_actor AND account.is_active AND NOT account.is_dependent),
  EXISTS(SELECT 1 FROM users account JOIN user_platform_roles assignment ON assignment.user_id=account.id
   JOIN platform_roles role ON role.id=assignment.role_id WHERE account.id=p_actor AND account.is_active AND NOT account.is_dependent AND role.code='ADMIN')
 INTO actor_executor,actor_admin;
 IF NOT actor_executor AND NOT actor_admin THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_control_forbidden'; END IF;
 RETURN QUERY
 SELECT request.public_ref,request.status::text,execution.id,execution.status::text,job.id,job.category_key::text,job.purpose_code::text,job.status::text,job.attempt_count,
  (CASE WHEN failure.stage_code IN ('EXECUTE','VERIFY') THEN failure.stage_code ELSE NULL END)::text,
  (CASE WHEN failure.failure_code IN ('ACTION_FAILED','DEPENDENCY_UNAVAILABLE','UNSUPPORTED_OPERATION','VERIFICATION_FAILED','RETRY_LIMIT_REACHED') THEN failure.failure_code ELSE NULL END)::text,
  proposal.id,proposal.proposal_sha256,proposal.proposed_at,
  (actor_executor AND request.status='TERMINAL_FAILED' AND execution.status='TERMINAL_FAILED' AND job.status='TERMINAL_FAILED'
   AND job.manual_attempt_allowance<10 AND proposal.id IS NULL),
  (actor_admin AND proposal.id IS NOT NULL AND proposal.proposed_by_ref<>p_actor)
 FROM data_erasure_requests request JOIN privacy_erasure_executions execution ON execution.request_id=request.id
 JOIN privacy_erasure_category_jobs job ON job.execution_id=execution.id
 LEFT JOIN LATERAL(SELECT candidate.stage_code,candidate.failure_code FROM privacy_erasure_failures candidate
  WHERE candidate.job_id=job.id ORDER BY candidate.occurred_at DESC,candidate.id DESC LIMIT 1) failure ON true
 LEFT JOIN LATERAL(SELECT candidate.id,candidate.proposal_sha256,candidate.proposed_at,candidate.proposed_by_ref FROM privacy_terminal_requeue_proposals candidate
  WHERE candidate.job_id=job.id AND NOT EXISTS(SELECT 1 FROM privacy_terminal_requeue_approvals approval WHERE approval.proposal_id=candidate.id)
  ORDER BY candidate.proposed_at DESC,candidate.id DESC LIMIT 1) proposal ON true
 WHERE request.public_ref=p_request_reference ORDER BY job.plan_entry_position LIMIT 50;
END;$$;

CREATE TABLE privacy_activation_evidence (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),kind varchar(20) NOT NULL CHECK(kind IN ('RESTORE','INFRASTRUCTURE','PROVIDER','SCHEMA')),
 evidence_sha256 bytea NOT NULL CHECK(octet_length(evidence_sha256)=32),reference_code varchar(120) NOT NULL,
 observed_at timestamptz NOT NULL,expires_at timestamptz NOT NULL,recorded_by_ref uuid NOT NULL,recorded_at timestamptz NOT NULL,
 CHECK(reference_code=btrim(reference_code) AND reference_code~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,119}$'),
 CHECK(expires_at=observed_at+interval '90 days'),UNIQUE(kind,evidence_sha256)
);
CREATE TABLE privacy_activation_proposals (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),policy_version varchar(80) NOT NULL REFERENCES privacy_request_policies(version) ON DELETE RESTRICT,
 evidence_ids uuid[] NOT NULL CHECK(cardinality(evidence_ids)=4 AND array_position(evidence_ids,NULL) IS NULL),
 evidence_set_sha256 bytea NOT NULL CHECK(octet_length(evidence_set_sha256)=32),activation_sha256 bytea NOT NULL UNIQUE CHECK(octet_length(activation_sha256)=32),
 proposed_by_ref uuid NOT NULL,proposed_at timestamptz NOT NULL
);
CREATE TABLE privacy_activation_approvals (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),proposal_id uuid NOT NULL UNIQUE REFERENCES privacy_activation_proposals(id) ON DELETE RESTRICT,
 activation_sha256 bytea NOT NULL UNIQUE CHECK(octet_length(activation_sha256)=32),approved_by_ref uuid NOT NULL,approved_at timestamptz NOT NULL
);
ALTER TABLE privacy_request_activation ADD COLUMN approval_id uuid NULL REFERENCES privacy_activation_approvals(id) ON DELETE RESTRICT;
WITH migration_clock AS (SELECT clock_timestamp() occurred_at), disabled AS (
 UPDATE privacy_request_activation activation SET enabled=false,fulfilment_ready=false,updated_at=migration_clock.occurred_at
 FROM migration_clock WHERE activation.enabled OR activation.fulfilment_ready
 RETURNING activation.policy_version,activation.updated_by,migration_clock.occurred_at
)
INSERT INTO privacy_request_activation_events(policy_version,actor_ref,enabled,fulfilment_ready,occurred_at)
 SELECT policy_version,updated_by,false,false,occurred_at FROM disabled;
ALTER TABLE privacy_request_activation ADD CONSTRAINT privacy_activation_requires_evidence_approval
 CHECK(NOT enabled OR (fulfilment_ready AND approval_id IS NOT NULL));
CREATE TRIGGER privacy_activation_evidence_immutable BEFORE UPDATE OR DELETE ON privacy_activation_evidence FOR EACH ROW EXECUTE FUNCTION prevent_privacy_audit_mutation();
CREATE TRIGGER privacy_activation_proposals_immutable BEFORE UPDATE OR DELETE ON privacy_activation_proposals FOR EACH ROW EXECUTE FUNCTION prevent_privacy_audit_mutation();
CREATE TRIGGER privacy_activation_approvals_immutable BEFORE UPDATE OR DELETE ON privacy_activation_approvals FOR EACH ROW EXECUTE FUNCTION prevent_privacy_audit_mutation();

CREATE FUNCTION guard_privacy_activation_evidence() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
 IF NEW.enabled AND (NOT NEW.fulfilment_ready OR NEW.approval_id IS NULL OR NOT EXISTS(
  SELECT 1 FROM privacy_activation_approvals approval JOIN privacy_activation_proposals proposal ON proposal.id=approval.proposal_id
  WHERE approval.id=NEW.approval_id AND approval.activation_sha256=proposal.activation_sha256 AND proposal.policy_version=NEW.policy_version
   AND (SELECT count(DISTINCT evidence.kind) FROM privacy_activation_evidence evidence WHERE evidence.id=ANY(proposal.evidence_ids) AND evidence.expires_at>clock_timestamp())=4
 )) THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_activation_requires_approval'; END IF;
 RETURN NEW;
END;$$;
CREATE TRIGGER privacy_activation_evidence_guard BEFORE INSERT OR UPDATE OF enabled,fulfilment_ready,approval_id ON privacy_request_activation
 FOR EACH ROW EXECUTE FUNCTION guard_privacy_activation_evidence();

CREATE FUNCTION privacy_activation_ready(p_policy_version text)
RETURNS boolean LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT EXISTS(
  SELECT 1
  FROM privacy_request_activation activation
  JOIN privacy_activation_approvals approval ON approval.id=activation.approval_id
  JOIN privacy_activation_proposals proposal ON proposal.id=approval.proposal_id
  WHERE activation.singleton AND activation.enabled AND activation.fulfilment_ready
   AND activation.policy_version=p_policy_version AND proposal.policy_version=activation.policy_version
   AND approval.activation_sha256=proposal.activation_sha256
   AND approval.approved_by_ref<>proposal.proposed_by_ref
   AND cardinality(proposal.evidence_ids)=4
   AND (SELECT count(*) FROM privacy_activation_evidence evidence
        WHERE evidence.id=ANY(proposal.evidence_ids)
         AND evidence.expires_at>clock_timestamp()
         AND ((evidence.kind='RESTORE' AND evidence.reference_code='mycfc/privacy-restore-drill-attestation/v1')
          OR (evidence.kind='INFRASTRUCTURE' AND evidence.reference_code='mycfc/privacy-infrastructure-posture/v1')
          OR (evidence.kind='PROVIDER' AND evidence.reference_code='mycfc/privacy-provider-registry/v1')
          OR (evidence.kind='SCHEMA' AND evidence.reference_code='mycfc/schema-migration-inventory/v1')))=4
   AND (SELECT count(DISTINCT evidence.kind) FROM privacy_activation_evidence evidence
        WHERE evidence.id=ANY(proposal.evidence_ids) AND evidence.expires_at>clock_timestamp())=4
 );
$$;

CREATE FUNCTION privacy_worker_activation_ready()
RETURNS boolean LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT COALESCE((SELECT privacy_activation_ready(activation.policy_version)
  FROM privacy_request_activation activation WHERE activation.singleton),false);
$$;

CREATE FUNCTION privacy_worker_status()
RETURNS TABLE(pending bigint,leased bigint,retryable bigint,terminal bigint,aged_nonterminal bigint)
LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT count(*) FILTER(WHERE job.status='PENDING'),count(*) FILTER(WHERE job.status='LEASED'),
  count(*) FILTER(WHERE job.status='RETRY_WAIT'),count(*) FILTER(WHERE job.status='TERMINAL_FAILED'),
  count(*) FILTER(WHERE job.status IN ('PENDING','LEASED','RETRY_WAIT') AND job.updated_at<=clock_timestamp()-interval '15 minutes')
 FROM privacy_erasure_category_jobs job;
$$;

CREATE FUNCTION privacy_activation_record_evidence(p_actor uuid,p_kind text,p_evidence_sha256 bytea,p_reference_code text,p_observed_at timestamptz)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE evidence_id uuid;now_at timestamptz:=clock_timestamp();
BEGIN
 IF NOT EXISTS(SELECT 1 FROM users account JOIN user_platform_roles assignment ON assignment.user_id=account.id JOIN platform_roles role ON role.id=assignment.role_id
   WHERE account.id=p_actor AND account.is_active AND NOT account.is_dependent AND role.code='ADMIN')
  OR p_kind NOT IN ('RESTORE','INFRASTRUCTURE','PROVIDER','SCHEMA') OR octet_length(p_evidence_sha256)<>32
  OR p_reference_code!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,119}$' OR p_observed_at>now_at OR p_observed_at<=now_at-interval '90 days' THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy_activation_evidence_rejected'; END IF;
 IF (p_kind='RESTORE' AND p_reference_code<>'mycfc/privacy-restore-drill-attestation/v1')
  OR (p_kind='INFRASTRUCTURE' AND p_reference_code<>'mycfc/privacy-infrastructure-posture/v1')
  OR (p_kind='PROVIDER' AND p_reference_code<>'mycfc/privacy-provider-registry/v1')
  OR (p_kind='SCHEMA' AND p_reference_code<>'mycfc/schema-migration-inventory/v1') THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy_activation_evidence_contract_rejected'; END IF;
 INSERT INTO privacy_activation_evidence(kind,evidence_sha256,reference_code,observed_at,expires_at,recorded_by_ref,recorded_at)
 VALUES(p_kind,p_evidence_sha256,p_reference_code,p_observed_at,p_observed_at+interval '90 days',p_actor,now_at)
 ON CONFLICT(kind,evidence_sha256) DO NOTHING RETURNING id INTO evidence_id;
 IF evidence_id IS NULL THEN
  SELECT evidence.id INTO evidence_id FROM privacy_activation_evidence evidence
  WHERE evidence.kind=p_kind AND evidence.evidence_sha256=p_evidence_sha256 AND evidence.reference_code=p_reference_code
   AND evidence.observed_at=p_observed_at AND evidence.expires_at=p_observed_at+interval '90 days';
  IF evidence_id IS NULL THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_activation_evidence_conflict'; END IF;
 END IF;
 RETURN evidence_id;
END;$$;

CREATE FUNCTION privacy_activation_propose(p_actor uuid,p_policy_version text,p_evidence_ids uuid[])
RETURNS TABLE(proposal_id uuid,activation_sha256 bytea) LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE policy privacy_request_policies%ROWTYPE;evidence_digest bytea;activation_digest bytea;now_at timestamptz:=clock_timestamp();
BEGIN
 IF NOT EXISTS(SELECT 1 FROM users account JOIN privacy_executor_grants grant_row ON grant_row.user_id=account.id AND grant_row.revoked_at IS NULL
   WHERE account.id=p_actor AND account.is_active AND NOT account.is_dependent) OR cardinality(p_evidence_ids)<>4 THEN
  RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_activation_forbidden'; END IF;
 SELECT * INTO policy FROM privacy_request_policies WHERE version=p_policy_version FOR SHARE;
 IF policy.version IS NULL OR policy.adopted_at IS NULL OR policy.working_retention_days<>90
  OR (SELECT count(DISTINCT kind) FROM privacy_activation_evidence WHERE id=ANY(p_evidence_ids) AND expires_at>now_at)<>4
  OR (SELECT count(*) FROM privacy_activation_evidence WHERE id=ANY(p_evidence_ids) AND expires_at>now_at)<>4 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_activation_evidence_incomplete'; END IF;
 SELECT digest(convert_to(string_agg(kind||':'||encode(evidence_sha256,'hex')||':'||to_char(observed_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"')||':'||to_char(expires_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),'|' ORDER BY kind),'UTF8'),'sha256')
 INTO evidence_digest FROM privacy_activation_evidence WHERE id=ANY(p_evidence_ids);
 activation_digest:=digest(convert_to('privacy-activation/v1:'||policy.version||':'||COALESCE(policy.executor_version,'')||':'||COALESCE(policy.plan_schema_version,'')||':'||
  encode(digest(convert_to(policy.category_catalogue::text,'UTF8'),'sha256'),'hex')||':'||encode(evidence_digest,'hex'),'UTF8'),'sha256');
 RETURN QUERY INSERT INTO privacy_activation_proposals(policy_version,evidence_ids,evidence_set_sha256,activation_sha256,proposed_by_ref,proposed_at)
 VALUES(policy.version,(SELECT array_agg(id ORDER BY kind) FROM privacy_activation_evidence WHERE id=ANY(p_evidence_ids)),evidence_digest,activation_digest,p_actor,now_at)
 RETURNING id,privacy_activation_proposals.activation_sha256;
END;$$;

CREATE FUNCTION privacy_activation_approve(p_actor uuid,p_proposal_id uuid,p_activation_sha256 bytea)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE proposal privacy_activation_proposals%ROWTYPE;approval_id uuid;now_at timestamptz:=clock_timestamp();recomputed bytea;
BEGIN
 IF NOT EXISTS(SELECT 1 FROM users account JOIN user_platform_roles assignment ON assignment.user_id=account.id JOIN platform_roles role ON role.id=assignment.role_id
   WHERE account.id=p_actor AND account.is_active AND NOT account.is_dependent AND role.code='ADMIN') THEN
  RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_activation_forbidden'; END IF;
 SELECT * INTO proposal FROM privacy_activation_proposals WHERE id=p_proposal_id FOR SHARE;
 IF proposal.id IS NULL OR proposal.proposed_by_ref=p_actor OR proposal.activation_sha256<>p_activation_sha256
  OR EXISTS(SELECT 1 FROM privacy_activation_approvals approval WHERE approval.proposal_id=proposal.id)
  OR (SELECT count(DISTINCT kind) FROM privacy_activation_evidence WHERE id=ANY(proposal.evidence_ids) AND expires_at>now_at)<>4 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_activation_unavailable'; END IF;
 SELECT digest(convert_to(string_agg(kind||':'||encode(evidence_sha256,'hex')||':'||to_char(observed_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"')||':'||to_char(expires_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),'|' ORDER BY kind),'UTF8'),'sha256')
 INTO recomputed FROM privacy_activation_evidence WHERE id=ANY(proposal.evidence_ids);
 IF recomputed<>proposal.evidence_set_sha256 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_activation_stale'; END IF;
 INSERT INTO privacy_activation_approvals(proposal_id,activation_sha256,approved_by_ref,approved_at)
 VALUES(proposal.id,proposal.activation_sha256,p_actor,now_at) RETURNING id INTO approval_id;
 PERFORM set_config('mycfc.privacy_activation_approval','approved',true);
 INSERT INTO privacy_request_activation(singleton,policy_version,enabled,fulfilment_ready,updated_by,updated_at,approval_id)
 VALUES(true,proposal.policy_version,true,true,p_actor,now_at,approval_id)
 ON CONFLICT(singleton) DO UPDATE SET policy_version=EXCLUDED.policy_version,enabled=true,fulfilment_ready=true,updated_by=EXCLUDED.updated_by,updated_at=EXCLUDED.updated_at,approval_id=EXCLUDED.approval_id;
 INSERT INTO privacy_request_activation_events(policy_version,actor_ref,enabled,fulfilment_ready,occurred_at)
 VALUES(proposal.policy_version,p_actor,true,true,now_at);
 RETURN approval_id;
END;$$;

CREATE FUNCTION privacy_activation_control_snapshot(p_actor uuid)
RETURNS TABLE(policy_version text,ready boolean,evidence_id uuid,evidence_kind text,evidence_observed_at timestamptz,
 proposal_id uuid,proposal_sha256 bytea,proposal_created_at timestamptz,can_propose boolean,can_renew boolean,can_approve boolean)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE actor_executor boolean;actor_admin boolean;selected_policy text;pending privacy_activation_proposals%ROWTYPE;current_evidence integer;
BEGIN
 SELECT EXISTS(SELECT 1 FROM users account JOIN privacy_executor_grants grant_row ON grant_row.user_id=account.id AND grant_row.revoked_at IS NULL
   WHERE account.id=p_actor AND account.is_active AND NOT account.is_dependent),
  EXISTS(SELECT 1 FROM users account JOIN user_platform_roles assignment ON assignment.user_id=account.id
   JOIN platform_roles role ON role.id=assignment.role_id WHERE account.id=p_actor AND account.is_active AND NOT account.is_dependent AND role.code='ADMIN')
 INTO actor_executor,actor_admin;
 IF NOT actor_executor AND NOT actor_admin THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_control_forbidden'; END IF;
 SELECT COALESCE((SELECT activation.policy_version FROM privacy_request_activation activation WHERE activation.singleton AND activation.enabled),
  (SELECT policy.version FROM privacy_request_policies policy WHERE policy.adopted_at IS NOT NULL ORDER BY policy.adopted_at DESC,policy.version DESC LIMIT 1))
 INTO selected_policy;
 IF selected_policy IS NULL THEN RETURN; END IF;
 SELECT proposal.* INTO pending FROM privacy_activation_proposals proposal
  WHERE proposal.policy_version=selected_policy AND NOT EXISTS(SELECT 1 FROM privacy_activation_approvals approval WHERE approval.proposal_id=proposal.id)
  ORDER BY proposal.proposed_at DESC,proposal.id DESC LIMIT 1;
 SELECT count(*) INTO current_evidence FROM (
  SELECT DISTINCT ON(evidence.kind) evidence.kind FROM privacy_activation_evidence evidence WHERE evidence.expires_at>clock_timestamp()
  ORDER BY evidence.kind,evidence.observed_at DESC,evidence.id DESC
 ) current_rows;
 RETURN QUERY
 SELECT selected_policy,privacy_activation_ready(selected_policy),evidence.id,evidence.kind::text,evidence.observed_at,
  pending.id,pending.activation_sha256,pending.proposed_at,
  (actor_executor AND current_evidence=4 AND pending.id IS NULL AND NOT privacy_activation_ready(selected_policy)),
  (actor_executor AND current_evidence=4 AND pending.id IS NULL AND privacy_activation_ready(selected_policy)),
  (actor_admin AND pending.id IS NOT NULL AND pending.proposed_by_ref<>p_actor)
 FROM (SELECT true singleton) seed LEFT JOIN LATERAL(
  SELECT DISTINCT ON(candidate.kind) candidate.id,candidate.kind,candidate.observed_at FROM privacy_activation_evidence candidate
  WHERE candidate.expires_at>clock_timestamp() ORDER BY candidate.kind,candidate.observed_at DESC,candidate.id DESC
 ) evidence ON true ORDER BY evidence.kind;
END;$$;

ALTER TABLE email_outbox DROP CONSTRAINT email_outbox_message_valid;
ALTER TABLE email_outbox ADD CONSTRAINT email_outbox_message_valid CHECK (
 (message_type='EMAIL_VERIFICATION' AND verification_token_id IS NOT NULL AND password_reset_token_id IS NULL AND sealed_payload IS NULL AND privacy_request_id IS NULL AND privacy_requester_id IS NULL AND privacy_event_key IS NULL)
 OR (message_type='PASSWORD_RESET' AND verification_token_id IS NULL AND password_reset_token_id IS NOT NULL AND sealed_payload IS NOT NULL AND privacy_request_id IS NULL AND privacy_requester_id IS NULL AND privacy_event_key IS NULL)
 OR (message_type IN ('PRIVACY_ACKNOWLEDGEMENT','PRIVACY_DECISION','PRIVACY_PROCESSING_STARTED','PRIVACY_COMPLETED') AND verification_token_id IS NULL AND password_reset_token_id IS NULL AND sealed_payload IS NOT NULL AND privacy_request_id IS NOT NULL AND privacy_event_key IS NOT NULL)
);

REVOKE ALL ON TABLE privacy_protected.completion_notice_targets,privacy_erasure_completion_manifests,privacy_completion_access_links,
 privacy_terminal_requeue_proposals,privacy_terminal_requeue_approvals,privacy_activation_evidence,privacy_activation_proposals,privacy_activation_approvals FROM PUBLIC;
REVOKE ALL ON FUNCTION privacy_execution_capture_completion_notice(uuid,uuid),privacy_completion_prepare(uuid,uuid),privacy_completion_list_pending(uuid,integer),privacy_completion_finalize(uuid,uuid,bytea,bytea),
 privacy_completion_consume(bytea),privacy_completion_validate(bytea),privacy_completion_notice_deliverable(uuid,timestamptz),privacy_terminal_requeue_propose(uuid,uuid),privacy_terminal_requeue_approve(uuid,bytea,uuid),privacy_completion_control_snapshot(uuid,uuid),
 privacy_activation_ready(text),privacy_worker_activation_ready(),privacy_worker_status(),privacy_activation_record_evidence(uuid,text,bytea,text,timestamptz),privacy_activation_propose(uuid,text,uuid[]),privacy_activation_approve(uuid,uuid,bytea),privacy_activation_control_snapshot(uuid) FROM PUBLIC;
