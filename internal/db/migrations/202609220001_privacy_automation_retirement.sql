-- Issue #318 permanently retires the inactive automated privacy-operation
-- system while preserving historical records and the protected media-upload
-- lifecycle. This migration is forward-only: old application images must remain
-- unable to reactivate or execute the retired system after rollback.

UPDATE privacy_request_activation
SET enabled=false, fulfilment_ready=false, approval_id=NULL, updated_at=clock_timestamp()
WHERE singleton;

INSERT INTO privacy_request_activation_events(policy_version,actor_ref,enabled,fulfilment_ready,occurred_at)
SELECT policy_version,updated_by,false,false,updated_at
FROM privacy_request_activation
WHERE singleton;

DO $$
DECLARE switch_version bigint; switch_time timestamptz;
BEGIN
 UPDATE privacy_worker_kill_switch
 SET engaged=true,version=version+1,activation_approval_id=NULL,changed_at=clock_timestamp()
 WHERE singleton AND (NOT engaged OR activation_approval_id IS NOT NULL)
 RETURNING version,changed_at INTO switch_version,switch_time;
 IF switch_version IS NOT NULL THEN
  INSERT INTO privacy_worker_kill_switch_events(version,engaged,occurred_at)
  VALUES(switch_version,true,switch_time);
 END IF;
END$$;

WITH revoked AS (
 UPDATE privacy_executor_grants
 SET revoked_by=granted_by,revoked_at=clock_timestamp()
 WHERE revoked_at IS NULL
 RETURNING id,granted_by,revoked_at
)
INSERT INTO privacy_executor_grant_events(grant_id,actor_ref,action,occurred_at)
SELECT id,granted_by,'REVOKED',revoked_at FROM revoked;

UPDATE email_outbox
SET status='CANCELLED',claimed_at=NULL,last_error='privacy automation retired',updated_at=clock_timestamp()
WHERE message_type IN ('PRIVACY_ACKNOWLEDGEMENT','PRIVACY_DECISION','PRIVACY_PROCESSING_STARTED','PRIVACY_COMPLETED')
  AND status IN ('PENDING','SENDING');

WITH revoked AS (
 UPDATE privacy_reviewer_grants
 SET revoked_by=granted_by,revoked_at=clock_timestamp()
 WHERE revoked_at IS NULL
 RETURNING id,granted_by,revoked_at
)
INSERT INTO privacy_reviewer_grant_events(grant_id,actor_ref,action,occurred_at)
SELECT id,granted_by,'REVOKED',revoked_at FROM revoked;

CREATE OR REPLACE FUNCTION privacy_automation_activation_retired()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.enabled OR NEW.fulfilment_ready OR NEW.approval_id IS NOT NULL THEN
  RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_automation_retired';
 END IF;
 RETURN NEW;
END;$$;
DROP TRIGGER IF EXISTS privacy_automation_activation_retired ON privacy_request_activation;
CREATE TRIGGER privacy_automation_activation_retired
 BEFORE INSERT OR UPDATE ON privacy_request_activation
 FOR EACH ROW EXECUTE FUNCTION privacy_automation_activation_retired();

CREATE OR REPLACE FUNCTION privacy_automation_switch_retired()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT NEW.engaged OR NEW.activation_approval_id IS NOT NULL THEN
  RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_automation_retired';
 END IF;
 RETURN NEW;
END;$$;
DROP TRIGGER IF EXISTS privacy_automation_switch_retired ON privacy_worker_kill_switch;
CREATE TRIGGER privacy_automation_switch_retired
 BEFORE INSERT OR UPDATE ON privacy_worker_kill_switch
 FOR EACH ROW EXECUTE FUNCTION privacy_automation_switch_retired();

CREATE OR REPLACE FUNCTION media_upload_cleanup_claim(p_lease_milliseconds bigint,p_worker_ref uuid)
RETURNS TABLE(intent_id uuid,lease_epoch bigint,attempt_id uuid,attempt_count integer,subject_user_id uuid,
 provenance_actor_user_id uuid,source_kind text,source_ref uuid,service_code text,target_kind text,
 provider_contract_version text,envelope_version text,algorithm text,encryption_key_id text,
 encapsulation bytea,nonce bytea,ciphertext bytea,content_type text,size_bytes bigint,
 cleanup_after timestamptz,created_at timestamptz)
LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
 RETURN QUERY SELECT * FROM privacy_upload_cleanup_claim_inner_013(p_lease_milliseconds,p_worker_ref);
END;$$;

CREATE OR REPLACE FUNCTION media_upload_cleanup_complete(
 p_intent_id uuid,p_attempt_id uuid,p_lease_epoch bigint,p_worker_ref uuid,p_deleted_versions integer,
 p_deleted_markers integer,p_list_calls integer,p_stable_checks integer,p_transcript_key_id text,
 p_transcript_digest bytea
) RETURNS void LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
 PERFORM privacy_upload_cleanup_complete_inner_013(p_intent_id,p_attempt_id,p_lease_epoch,p_worker_ref,
  p_deleted_versions,p_deleted_markers,p_list_calls,p_stable_checks,p_transcript_key_id,p_transcript_digest);
END;$$;

CREATE OR REPLACE FUNCTION media_upload_cleanup_fail(
 p_intent_id uuid,p_attempt_id uuid,p_lease_epoch bigint,p_worker_ref uuid,p_retryable boolean,
 p_retry_delay_milliseconds bigint
) RETURNS void LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
 PERFORM privacy_upload_cleanup_fail_inner_013(p_intent_id,p_attempt_id,p_lease_epoch,p_worker_ref,
  p_retryable,p_retry_delay_milliseconds);
END;$$;

CREATE OR REPLACE FUNCTION data_retention_run(p_worker_ref uuid,p_batch_limit integer)
RETURNS TABLE(run_id uuid,sessions_deleted integer,tokens_deleted integer,outbox_stopped integer,outbox_payloads_deleted integer,
 outbox_evidence_deleted integer,consent_network_scrubbed integer,consent_evidence_deleted integer,audit_events_pseudonymized integer,
 repair_attachments_queued integer,event_responses_deleted integer,announcement_deliveries_deleted integer,suggestions_deleted integer,
 privacy_working_scrubbed integer,auth_limits_deleted integer)
LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT * FROM public.privacy_retention_run(p_worker_ref,p_batch_limit);
$$;

CREATE OR REPLACE FUNCTION data_retention_status()
RETURNS TABLE(due_count bigint,oldest_due_age_seconds bigint,repair_due_count bigint,repair_overdue_count bigint,
 repair_terminal_failures bigint,repair_legacy_due_count bigint,last_run_age_seconds bigint)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT * FROM public.privacy_retention_status();
$$;

REVOKE ALL ON FUNCTION data_retention_run(uuid,integer),data_retention_status() FROM PUBLIC;


REVOKE ALL ON FUNCTION media_upload_cleanup_claim(bigint,uuid),
 media_upload_cleanup_complete(uuid,uuid,bigint,uuid,integer,integer,integer,integer,text,bytea),
 media_upload_cleanup_fail(uuid,uuid,bigint,uuid,boolean,bigint) FROM PUBLIC;

DO $$
DECLARE routine record; legacy_role text; member_role record; granted_role record; protected_schema text;
BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='mycfc_media_cleanup') THEN
  CREATE ROLE mycfc_media_cleanup NOLOGIN;
 END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='mycfc_data_retention') THEN
  CREATE ROLE mycfc_data_retention NOLOGIN;
 END IF;
 REVOKE ALL ON SCHEMA public FROM mycfc_media_cleanup;
 GRANT USAGE ON SCHEMA public TO mycfc_media_cleanup;
 REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM mycfc_media_cleanup;
 REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public FROM mycfc_media_cleanup;
 REVOKE EXECUTE ON ALL FUNCTIONS IN SCHEMA public FROM mycfc_media_cleanup;
 GRANT EXECUTE ON FUNCTION media_upload_cleanup_claim(bigint,uuid),
  media_upload_cleanup_complete(uuid,uuid,bigint,uuid,integer,integer,integer,integer,text,bytea),
  media_upload_cleanup_fail(uuid,uuid,bigint,uuid,boolean,bigint) TO mycfc_media_cleanup;
 REVOKE ALL ON SCHEMA public FROM mycfc_data_retention;
 GRANT USAGE ON SCHEMA public TO mycfc_data_retention;
 REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM mycfc_data_retention;
 REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public FROM mycfc_data_retention;
 REVOKE EXECUTE ON ALL FUNCTIONS IN SCHEMA public FROM mycfc_data_retention;
 GRANT EXECUTE ON FUNCTION data_retention_run(uuid,integer),data_retention_status() TO mycfc_data_retention;

 FOR routine IN
  SELECT p.oid::regprocedure AS signature
  FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
  WHERE n.nspname='public'
   AND p.proname~'^privacy_'
 LOOP
  EXECUTE format('REVOKE ALL ON FUNCTION %s FROM PUBLIC',routine.signature);
  FOREACH legacy_role IN ARRAY ARRAY[
   'mycfc_privacy_executor','mycfc_privacy_retention','mycfc_privacy_activation_broker',
   'mycfc_privacy_restore_observer','mycfc_privacy_activation_disable','mycfc_privacy_acceptance'
  ] LOOP
   IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname=legacy_role) THEN
    EXECUTE format('REVOKE ALL ON FUNCTION %s FROM %I',routine.signature,legacy_role);
   END IF;
  END LOOP;
 END LOOP;

 FOREACH legacy_role IN ARRAY ARRAY[
  'mycfc_privacy_executor','mycfc_privacy_retention','mycfc_privacy_activation_broker',
  'mycfc_privacy_restore_observer','mycfc_privacy_activation_disable','mycfc_privacy_acceptance'
  ] LOOP
  IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname=legacy_role) THEN
   FOR member_role IN
    SELECT member.rolname AS member_name
    FROM pg_auth_members membership
    JOIN pg_roles granted ON granted.oid=membership.roleid
    JOIN pg_roles member ON member.oid=membership.member
    WHERE granted.rolname=legacy_role
   LOOP
    EXECUTE format('REVOKE %I FROM %I',legacy_role,member_role.member_name);
   END LOOP;
   FOR granted_role IN
    SELECT granted.rolname AS granted_name FROM pg_auth_members membership
    JOIN pg_roles granted ON granted.oid=membership.roleid
    JOIN pg_roles member ON member.oid=membership.member
    WHERE member.rolname=legacy_role
   LOOP
    EXECUTE format('REVOKE %I FROM %I',granted_role.granted_name,legacy_role);
   END LOOP;
   EXECUTE format('REVOKE CONNECT ON DATABASE %I FROM %I',current_database(),legacy_role);
   EXECUTE format('REVOKE ALL ON SCHEMA public FROM %I',legacy_role);
   EXECUTE format('REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM %I',legacy_role);
   EXECUTE format('REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public FROM %I',legacy_role);
   FOREACH protected_schema IN ARRAY ARRAY['privacy_protected','privacy_disable','mycfc_meta'] LOOP
    IF to_regnamespace(protected_schema) IS NOT NULL THEN
     EXECUTE format('REVOKE ALL ON SCHEMA %I FROM %I',protected_schema,legacy_role);
     EXECUTE format('REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA %I FROM %I',protected_schema,legacy_role);
     EXECUTE format('REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA %I FROM %I',protected_schema,legacy_role);
     EXECUTE format('REVOKE EXECUTE ON ALL FUNCTIONS IN SCHEMA %I FROM %I',protected_schema,legacy_role);
    END IF;
   END LOOP;
   EXECUTE format('ALTER ROLE %I NOLOGIN PASSWORD NULL',legacy_role);
   PERFORM pg_terminate_backend(pid) FROM pg_stat_activity
    WHERE usename=legacy_role AND pid<>pg_backend_pid();
  END IF;
 END LOOP;
END$$;

REVOKE ALL ON FUNCTION privacy_automation_activation_retired(),
 privacy_automation_switch_retired() FROM PUBLIC;
