-- Membership insertion must check erasure-in-progress without granting the
-- web login SELECT on private erasure jobs, executions or requests. Keep the
-- subject row lock and all prior attachment checks inside the trigger.
CREATE OR REPLACE FUNCTION require_active_privacy_attachment_subject() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE subject_id uuid; old_subject_id uuid; subject_column text;
BEGIN
 subject_column:=COALESCE(NULLIF(TG_ARGV[0],''),'user_id');
 subject_id:=NULLIF(to_jsonb(NEW)->>subject_column,'')::uuid;
 IF TG_OP='UPDATE' THEN old_subject_id:=NULLIF(to_jsonb(OLD)->>subject_column,'')::uuid; END IF;
 -- The definer owns the protected erasure tables; only a login with actual
 -- migration-role membership may use the historic SET ROLE erasure bypass.
 IF current_user<>session_user AND pg_has_role(session_user,current_user,'MEMBER')
  AND current_setting('mycfc.privacy_erasure_operation',true)='MEMBERSHIP_HISTORY_ANONYMIZE' THEN RETURN NEW; END IF;
 IF subject_id IS NOT NULL THEN
  PERFORM 1 FROM users WHERE id=subject_id AND is_active AND erased_at IS NULL FOR KEY SHARE;
  IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='inactive_privacy_attachment_subject'; END IF;
 END IF;
 IF TG_TABLE_NAME IN ('user_memberships','training_prescriptions') AND EXISTS(
  SELECT 1 FROM privacy_erasure_category_jobs job
  JOIN privacy_erasure_executions execution ON execution.id=job.execution_id
  JOIN data_erasure_requests request ON request.id=execution.request_id
  WHERE job.category_key='membership-history' AND job.status IN ('PENDING','RETRY_WAIT','LEASED')
   AND COALESCE(request.subject_user_id,request.requester_user_id) IN (subject_id,old_subject_id)
 ) THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='privacy_membership_erasure_in_progress'; END IF;
 RETURN NEW;
END;
$$;
REVOKE EXECUTE ON FUNCTION require_active_privacy_attachment_subject() FROM PUBLIC;
