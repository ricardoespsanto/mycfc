-- The executor role may mutate lifecycle state only through these fenced,
-- owner-controlled routines. Direct table DML is revoked by role hardening.

CREATE FUNCTION privacy_worker_claim(p_lease_milliseconds bigint, p_worker_ref uuid)
RETURNS TABLE(job_id uuid, lease_id uuid, attempt_id uuid)
LANGUAGE sql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
WITH candidate AS MATERIALIZED (
 SELECT job.id,job.status,job.lease_epoch
 FROM public.privacy_erasure_category_jobs job
 WHERE p_lease_milliseconds BETWEEN 1000 AND 3600000
 AND ((job.status IN ('PENDING','RETRY_WAIT') AND job.next_attempt_at<=clock_timestamp())
    OR (job.status='LEASED' AND EXISTS(
      SELECT 1 FROM public.privacy_erasure_job_leases lease
      WHERE lease.job_id=job.id AND lease.epoch=job.lease_epoch AND lease.released_at IS NULL AND lease.expires_at<=clock_timestamp()
    )))
 ORDER BY job.next_attempt_at,job.created_at,job.id
 FOR UPDATE SKIP LOCKED LIMIT 1
), authority_clock AS MATERIALIZED (
 SELECT clock_timestamp() AS occurred_at FROM candidate LIMIT 1
), expired_lease AS (
 UPDATE public.privacy_erasure_job_leases lease SET released_at=authority_clock.occurred_at,outcome='EXPIRED'
 FROM candidate,authority_clock WHERE lease.job_id=candidate.id AND lease.epoch=candidate.lease_epoch
 AND candidate.status='LEASED' AND lease.released_at IS NULL AND lease.expires_at<=authority_clock.occurred_at
 RETURNING lease.id,lease.job_id
), expired_attempt AS (
 UPDATE public.privacy_erasure_job_attempts attempt SET finished_at=authority_clock.occurred_at,outcome='LEASE_EXPIRED'
 FROM expired_lease,authority_clock WHERE attempt.lease_id=expired_lease.id AND attempt.finished_at IS NULL
 RETURNING attempt.id
), claimed_job AS (
 UPDATE public.privacy_erasure_category_jobs job SET status='LEASED',lease_epoch=job.lease_epoch+1,
 attempt_count=job.attempt_count+1,updated_at=authority_clock.occurred_at
 FROM candidate,authority_clock WHERE job.id=candidate.id AND (candidate.status<>'LEASED' OR EXISTS(SELECT 1 FROM expired_lease))
 RETURNING job.*
), new_lease AS (
 INSERT INTO public.privacy_erasure_job_leases(job_id,epoch,worker_ref,acquired_at,heartbeat_at,expires_at)
 SELECT id,lease_epoch,p_worker_ref,authority_clock.occurred_at,authority_clock.occurred_at,
 authority_clock.occurred_at+(p_lease_milliseconds*INTERVAL '1 millisecond')
 FROM claimed_job,authority_clock RETURNING *
), new_attempt AS (
 INSERT INTO public.privacy_erasure_job_attempts(job_id,lease_id,lease_epoch,attempt_number,started_at)
 SELECT claimed_job.id,new_lease.id,new_lease.epoch,claimed_job.attempt_count,authority_clock.occurred_at
 FROM claimed_job JOIN new_lease ON new_lease.job_id=claimed_job.id CROSS JOIN authority_clock RETURNING *
), execution_started AS (
 UPDATE public.privacy_erasure_executions execution SET status='RUNNING',version=execution.version+1,
 started_at=COALESCE(execution.started_at,authority_clock.occurred_at),finished_at=NULL,updated_at=authority_clock.occurred_at
 FROM claimed_job,authority_clock WHERE execution.id=claimed_job.execution_id AND execution.status NOT IN ('SUCCEEDED','TERMINAL_FAILED') RETURNING execution.id
)
SELECT claimed_job.id,new_lease.id,new_attempt.id
FROM claimed_job JOIN new_lease ON new_lease.job_id=claimed_job.id JOIN new_attempt ON new_attempt.job_id=claimed_job.id
WHERE EXISTS(SELECT 1 FROM execution_started);
$$;

CREATE FUNCTION privacy_worker_heartbeat(p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_epoch bigint,p_worker_ref uuid,p_lease_milliseconds bigint)
RETURNS uuid LANGUAGE sql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
WITH authorized AS MATERIALIZED (
 SELECT lease.id
 FROM public.privacy_erasure_job_leases lease
 JOIN public.privacy_erasure_category_jobs job ON lease.job_id=job.id AND lease.epoch=job.lease_epoch
 JOIN public.privacy_erasure_job_attempts attempt ON attempt.lease_id=lease.id
 WHERE job.id=p_job_id AND lease.id=p_lease_id AND attempt.id=p_attempt_id AND lease.epoch=p_epoch
 AND lease.worker_ref=p_worker_ref AND job.status='LEASED' AND lease.released_at IS NULL
 AND attempt.finished_at IS NULL AND lease.expires_at>clock_timestamp()
 AND p_lease_milliseconds BETWEEN 1000 AND 3600000 FOR UPDATE OF job,lease,attempt
), authority_clock AS MATERIALIZED (SELECT clock_timestamp() AS occurred_at FROM authorized LIMIT 1)
UPDATE public.privacy_erasure_job_leases lease SET heartbeat_at=authority_clock.occurred_at,
expires_at=authority_clock.occurred_at+(p_lease_milliseconds*INTERVAL '1 millisecond')
FROM authorized,authority_clock WHERE lease.id=authorized.id RETURNING lease.id;
$$;

CREATE FUNCTION privacy_worker_complete_checkpoint(p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_epoch bigint,p_worker_ref uuid,p_operation_code text,p_action_version text)
RETURNS uuid LANGUAGE sql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
WITH authorized AS MATERIALIZED (
 SELECT job.id AS job_id,attempt.id AS attempt_id
 FROM public.privacy_erasure_category_jobs job
 JOIN public.privacy_erasure_job_leases lease ON lease.job_id=job.id AND lease.epoch=job.lease_epoch
 JOIN public.privacy_erasure_job_attempts attempt ON attempt.lease_id=lease.id
 WHERE job.id=p_job_id AND lease.id=p_lease_id AND attempt.id=p_attempt_id AND lease.epoch=p_epoch
 AND lease.worker_ref=p_worker_ref AND job.status='LEASED' AND lease.released_at IS NULL
 AND lease.expires_at>clock_timestamp() AND attempt.finished_at IS NULL FOR UPDATE OF job,lease,attempt
), authority_clock AS MATERIALIZED (SELECT clock_timestamp() AS occurred_at FROM authorized LIMIT 1)
UPDATE public.privacy_erasure_job_checkpoints checkpoint SET status='SUCCEEDED',completed_by_attempt_id=authorized.attempt_id,completed_at=authority_clock.occurred_at
FROM authorized,authority_clock WHERE checkpoint.job_id=authorized.job_id AND checkpoint.operation_code=p_operation_code
AND checkpoint.action_version=p_action_version AND checkpoint.status='PENDING' RETURNING checkpoint.id;
$$;

CREATE FUNCTION privacy_worker_complete_job(p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_epoch bigint,p_worker_ref uuid)
RETURNS uuid LANGUAGE sql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
WITH authorized AS MATERIALIZED (
 SELECT job.id,lease.id AS lease_id,attempt.id AS attempt_id
 FROM public.privacy_erasure_category_jobs job
 JOIN public.privacy_erasure_job_leases lease ON lease.job_id=job.id AND lease.epoch=job.lease_epoch
 JOIN public.privacy_erasure_job_attempts attempt ON attempt.lease_id=lease.id
 WHERE job.id=p_job_id AND lease.id=p_lease_id AND attempt.id=p_attempt_id AND lease.epoch=p_epoch
 AND lease.worker_ref=p_worker_ref AND job.status='LEASED' AND lease.released_at IS NULL
 AND lease.expires_at>clock_timestamp() AND attempt.finished_at IS NULL
 AND NOT EXISTS(SELECT 1 FROM public.privacy_erasure_job_checkpoints checkpoint WHERE checkpoint.job_id=job.id AND checkpoint.status<>'SUCCEEDED')
 FOR UPDATE OF job,lease,attempt
), authority_clock AS MATERIALIZED (SELECT clock_timestamp() AS occurred_at FROM authorized LIMIT 1),
completed_job AS (
 UPDATE public.privacy_erasure_category_jobs job SET status='SUCCEEDED',completed_at=authority_clock.occurred_at,updated_at=authority_clock.occurred_at
 FROM authorized,authority_clock WHERE job.id=authorized.id RETURNING job.id
), closed_lease AS (
 UPDATE public.privacy_erasure_job_leases lease SET released_at=authority_clock.occurred_at,outcome='SUCCEEDED'
 FROM authorized,completed_job,authority_clock WHERE lease.id=authorized.lease_id RETURNING lease.id
), closed_attempt AS (
 UPDATE public.privacy_erasure_job_attempts attempt SET finished_at=authority_clock.occurred_at,outcome='SUCCEEDED'
 FROM authorized,closed_lease,authority_clock WHERE attempt.id=authorized.attempt_id RETURNING attempt.id
)
SELECT completed_job.id FROM completed_job WHERE EXISTS(SELECT 1 FROM closed_attempt);
$$;

CREATE FUNCTION privacy_worker_fail_job(p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_epoch bigint,p_worker_ref uuid,p_classification text,p_retry_milliseconds bigint,p_stage_code text,p_failure_code text,p_diagnostic_digest bytea)
RETURNS uuid LANGUAGE sql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
WITH authorized AS MATERIALIZED (
 SELECT job.id,lease.id AS lease_id,attempt.id AS attempt_id
 FROM public.privacy_erasure_category_jobs job
 JOIN public.privacy_erasure_job_leases lease ON lease.job_id=job.id AND lease.epoch=job.lease_epoch
 JOIN public.privacy_erasure_job_attempts attempt ON attempt.lease_id=lease.id
 WHERE job.id=p_job_id AND lease.id=p_lease_id AND attempt.id=p_attempt_id AND lease.epoch=p_epoch
 AND lease.worker_ref=p_worker_ref AND job.status='LEASED' AND lease.released_at IS NULL
 AND lease.expires_at>clock_timestamp() AND attempt.finished_at IS NULL
 AND ((p_classification='RETRYABLE' AND p_retry_milliseconds BETWEEN 1 AND 3600000)
   OR (p_classification='TERMINAL' AND p_retry_milliseconds=0)) FOR UPDATE OF job,lease,attempt
), authority_clock AS MATERIALIZED (SELECT clock_timestamp() AS occurred_at FROM authorized LIMIT 1),
recorded_failure AS (
 INSERT INTO public.privacy_erasure_failures(job_id,attempt_id,classification,stage_code,failure_code,diagnostic_digest,occurred_at)
 SELECT id,attempt_id,p_classification,p_stage_code,p_failure_code,p_diagnostic_digest,authority_clock.occurred_at
 FROM authorized,authority_clock RETURNING *
), failed_job AS (
 UPDATE public.privacy_erasure_category_jobs job SET status=CASE WHEN recorded_failure.classification='RETRYABLE' THEN 'RETRY_WAIT' ELSE 'TERMINAL_FAILED' END,
 next_attempt_at=CASE WHEN recorded_failure.classification='RETRYABLE' THEN authority_clock.occurred_at+(p_retry_milliseconds*INTERVAL '1 millisecond') ELSE job.next_attempt_at END,
 updated_at=authority_clock.occurred_at FROM authorized,recorded_failure,authority_clock WHERE job.id=authorized.id RETURNING job.id
), closed_lease AS (
 UPDATE public.privacy_erasure_job_leases lease SET released_at=authority_clock.occurred_at,outcome=CASE WHEN recorded_failure.classification='RETRYABLE' THEN 'RETRYABLE_FAILED' ELSE 'TERMINAL_FAILED' END
 FROM authorized,recorded_failure,failed_job,authority_clock WHERE lease.id=authorized.lease_id RETURNING lease.id
), closed_attempt AS (
 UPDATE public.privacy_erasure_job_attempts attempt SET finished_at=authority_clock.occurred_at,outcome=CASE WHEN recorded_failure.classification='RETRYABLE' THEN 'RETRYABLE_FAILED' ELSE 'TERMINAL_FAILED' END
 FROM authorized,recorded_failure,closed_lease,authority_clock WHERE attempt.id=authorized.attempt_id RETURNING attempt.id
)
SELECT failed_job.id FROM failed_job WHERE EXISTS(SELECT 1 FROM closed_attempt);
$$;

CREATE FUNCTION privacy_worker_sync(p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_epoch bigint,p_worker_ref uuid)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE p_execution_id uuid; authoritative_worker_ref uuid; execution_status text; request_id uuid; request_status text; request_version bigint;
DECLARE target_status text; event_action text; event_reason text; occurred_at timestamptz; updated_version bigint;
BEGIN
 SELECT job.execution_id,lease.worker_ref INTO p_execution_id,authoritative_worker_ref
 FROM public.privacy_erasure_category_jobs job
 JOIN public.privacy_erasure_job_leases lease ON lease.job_id=job.id AND lease.epoch=p_epoch
 JOIN public.privacy_erasure_job_attempts attempt ON attempt.job_id=job.id AND attempt.lease_id=lease.id AND attempt.lease_epoch=p_epoch
 WHERE job.id=p_job_id AND lease.id=p_lease_id AND attempt.id=p_attempt_id AND lease.worker_ref=p_worker_ref
 AND ((job.status='LEASED' AND lease.released_at IS NULL AND attempt.finished_at IS NULL AND lease.expires_at>clock_timestamp())
   OR (job.status IN ('SUCCEEDED','RETRY_WAIT','TERMINAL_FAILED') AND lease.released_at IS NOT NULL AND attempt.finished_at IS NOT NULL))
 FOR UPDATE OF job,lease,attempt;
 IF NOT FOUND THEN RETURN NULL; END IF;
 PERFORM 1 FROM public.privacy_erasure_executions WHERE id=p_execution_id FOR UPDATE;
 SELECT CASE WHEN bool_or(status='TERMINAL_FAILED') THEN 'TERMINAL_FAILED'
             WHEN bool_or(status='RETRY_WAIT') THEN 'RETRYABLE_FAILED'
             WHEN bool_and(status='SUCCEEDED') THEN 'SUCCEEDED'
             WHEN bool_or(status='LEASED') OR bool_or(attempt_count>0) THEN 'RUNNING' ELSE 'QUEUED' END
 INTO execution_status FROM public.privacy_erasure_category_jobs WHERE execution_id=p_execution_id;
 occurred_at:=clock_timestamp();
 UPDATE public.privacy_erasure_executions SET status=execution_status,version=version+1,
  started_at=CASE WHEN execution_status='QUEUED' THEN NULL ELSE COALESCE(started_at,occurred_at) END,
  finished_at=CASE WHEN execution_status IN ('TERMINAL_FAILED','SUCCEEDED') THEN COALESCE(finished_at,occurred_at) ELSE NULL END,
  updated_at=occurred_at WHERE id=p_execution_id RETURNING privacy_erasure_executions.request_id INTO request_id;
 IF execution_status IN ('QUEUED','SUCCEEDED') THEN RETURN p_execution_id; END IF;
 IF execution_status='RUNNING' THEN target_status:='PROCESSING'; event_action:='EXECUTION_RESUMED'; event_reason:='EXECUTION_RETRY_STARTED';
 ELSIF execution_status='RETRYABLE_FAILED' THEN target_status:='RETRYABLE_FAILED'; event_action:='EXECUTION_RETRYABLE_FAILED'; event_reason:='EXECUTION_RETRYABLE_FAILURE';
 ELSIF execution_status='TERMINAL_FAILED' THEN target_status:='TERMINAL_FAILED'; event_action:='EXECUTION_TERMINAL_FAILED'; event_reason:='EXECUTION_TERMINAL_FAILURE';
 ELSE RAISE EXCEPTION 'invalid privacy execution aggregate'; END IF;
 SELECT status,version INTO request_status,request_version FROM public.data_erasure_requests WHERE id=request_id FOR UPDATE;
 IF request_status=target_status OR (execution_status='RUNNING' AND request_status='PROCESSING') THEN RETURN p_execution_id; END IF;
 IF NOT ((target_status='PROCESSING' AND request_status='RETRYABLE_FAILED') OR
         (target_status='RETRYABLE_FAILED' AND request_status='PROCESSING') OR
         (target_status='TERMINAL_FAILED' AND request_status IN ('PROCESSING','RETRYABLE_FAILED'))) THEN
  RAISE EXCEPTION 'invalid privacy request lifecycle transition';
 END IF;
 UPDATE public.data_erasure_requests SET status=target_status,version=version+1,updated_at=occurred_at
 WHERE id=request_id AND version=request_version RETURNING version INTO updated_version;
 INSERT INTO public.data_erasure_request_events(request_id,actor_role,actor_ref,action,reason_code,from_status,to_status,version,occurred_at)
 VALUES(request_id,'SYSTEM',authoritative_worker_ref,event_action,event_reason,request_status,target_status,updated_version,occurred_at);
 RETURN p_execution_id;
END; $$;

REVOKE ALL ON FUNCTION privacy_worker_claim(bigint,uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION privacy_worker_heartbeat(uuid,uuid,uuid,bigint,uuid,bigint) FROM PUBLIC;
REVOKE ALL ON FUNCTION privacy_worker_complete_checkpoint(uuid,uuid,uuid,bigint,uuid,text,text) FROM PUBLIC;
REVOKE ALL ON FUNCTION privacy_worker_complete_job(uuid,uuid,uuid,bigint,uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION privacy_worker_fail_job(uuid,uuid,uuid,bigint,uuid,text,bigint,text,text,bytea) FROM PUBLIC;
REVOKE ALL ON FUNCTION privacy_worker_sync(uuid,uuid,uuid,bigint,uuid) FROM PUBLIC;
