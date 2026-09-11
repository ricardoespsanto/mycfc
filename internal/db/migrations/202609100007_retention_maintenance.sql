-- #247: bounded, conservative technical-retention maintenance. No timer or
-- production role is enabled by this migration. Rows without an authoritative
-- lifecycle anchor are deliberately preserved.

CREATE TABLE privacy_retention_runs (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), worker_ref uuid NOT NULL,
 started_at timestamptz NOT NULL, finished_at timestamptz NOT NULL,
 batch_limit integer NOT NULL CHECK(batch_limit BETWEEN 1 AND 10000),
 sessions_deleted integer NOT NULL CHECK(sessions_deleted>=0),
 tokens_deleted integer NOT NULL CHECK(tokens_deleted>=0),
 outbox_stopped integer NOT NULL CHECK(outbox_stopped>=0),
 outbox_payloads_deleted integer NOT NULL CHECK(outbox_payloads_deleted>=0),
 outbox_evidence_deleted integer NOT NULL CHECK(outbox_evidence_deleted>=0),
 consent_network_scrubbed integer NOT NULL CHECK(consent_network_scrubbed>=0),
 event_responses_deleted integer NOT NULL CHECK(event_responses_deleted>=0),
 announcement_deliveries_deleted integer NOT NULL CHECK(announcement_deliveries_deleted>=0),
 suggestions_deleted integer NOT NULL CHECK(suggestions_deleted>=0),
 privacy_working_scrubbed integer NOT NULL CHECK(privacy_working_scrubbed>=0),
 auth_limits_deleted integer NOT NULL CHECK(auth_limits_deleted>=0),
 CHECK(finished_at>=started_at)
);

CREATE TABLE privacy_outbox_delivery_evidence (
 outbox_id uuid PRIMARY KEY, message_type varchar(30) NOT NULL,
 final_status varchar(20) NOT NULL CHECK(final_status IN ('SENT','FAILED','CANCELLED')),
 attempts integer NOT NULL CHECK(attempts>=0), created_at timestamptz NOT NULL,
 terminal_at timestamptz NOT NULL, expires_at timestamptz NOT NULL,
 CHECK(expires_at=created_at+interval '90 days')
);
CREATE INDEX privacy_outbox_delivery_evidence_expiry_idx ON privacy_outbox_delivery_evidence(expires_at,outbox_id);
CREATE INDEX email_verification_tokens_retention_idx ON email_verification_tokens((GREATEST(expires_at,COALESCE(consumed_at,expires_at))),id);
CREATE INDEX password_reset_tokens_retention_idx ON password_reset_tokens((GREATEST(expires_at,COALESCE(consumed_at,expires_at))),id);
CREATE INDEX event_responses_retention_idx ON event_responses(event_id,user_id);
CREATE INDEX announcement_deliveries_retention_idx ON announcement_deliveries(delivered_at,announcement_id,user_id);
CREATE INDEX suggestions_retention_idx ON suggestions(responded_at,id) WHERE status IN ('DECLINED','COMPLETED');

CREATE FUNCTION privacy_retention_run(p_worker_ref uuid,p_batch_limit integer)
RETURNS TABLE(
 run_id uuid,sessions_deleted integer,tokens_deleted integer,outbox_stopped integer,
 outbox_payloads_deleted integer,outbox_evidence_deleted integer,consent_network_scrubbed integer,
 event_responses_deleted integer,announcement_deliveries_deleted integer,suggestions_deleted integer,
 privacy_working_scrubbed integer,auth_limits_deleted integer
) LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE v_now timestamptz:=clock_timestamp(); v_started timestamptz:=v_now; n integer;
BEGIN
 IF p_worker_ref IS NULL OR p_batch_limit NOT BETWEEN 1 AND 10000 THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy retention input rejected';
 END IF;
 IF NOT pg_try_advisory_xact_lock(247,247) THEN
  RAISE EXCEPTION USING ERRCODE='55P03',MESSAGE='privacy retention already running';
 END IF;
 run_id:=gen_random_uuid(); sessions_deleted:=0; tokens_deleted:=0; outbox_stopped:=0;
 outbox_payloads_deleted:=0; outbox_evidence_deleted:=0; consent_network_scrubbed:=0;
 event_responses_deleted:=0; announcement_deliveries_deleted:=0; suggestions_deleted:=0;
 privacy_working_scrubbed:=0; auth_limits_deleted:=0;

 WITH candidate AS (SELECT token FROM sessions WHERE expiry<=v_now ORDER BY expiry,token FOR UPDATE SKIP LOCKED LIMIT p_batch_limit)
 DELETE FROM sessions row USING candidate WHERE row.token=candidate.token;
 GET DIAGNOSTICS sessions_deleted=ROW_COUNT;

 WITH candidate AS (SELECT id FROM email_outbox WHERE created_at<=v_now-interval '7 days' AND status IN ('PENDING','SENDING')
  ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT p_batch_limit)
 UPDATE email_outbox row SET status='FAILED',claimed_at=NULL,last_error=NULL,updated_at=v_now FROM candidate WHERE row.id=candidate.id;
 GET DIAGNOSTICS outbox_stopped=ROW_COUNT;

 WITH candidate AS MATERIALIZED (SELECT id FROM email_outbox WHERE created_at<=v_now-interval '30 days'
  ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT p_batch_limit),
 evidence AS (INSERT INTO privacy_outbox_delivery_evidence(outbox_id,message_type,final_status,attempts,created_at,terminal_at,expires_at)
  SELECT row.id,row.message_type,CASE WHEN row.status IN ('SENT','FAILED','CANCELLED') THEN row.status ELSE 'FAILED' END,row.attempts,row.created_at,
   COALESCE(row.sent_at,row.updated_at,row.created_at),row.created_at+interval '90 days'
  FROM email_outbox row JOIN candidate USING(id) ON CONFLICT(outbox_id) DO NOTHING RETURNING outbox_id)
 DELETE FROM email_outbox row USING candidate WHERE row.id=candidate.id;
 GET DIAGNOSTICS outbox_payloads_deleted=ROW_COUNT;

 WITH candidate AS (SELECT outbox_id FROM privacy_outbox_delivery_evidence WHERE expires_at<=v_now
  ORDER BY expires_at,outbox_id FOR UPDATE SKIP LOCKED LIMIT p_batch_limit)
 DELETE FROM privacy_outbox_delivery_evidence row USING candidate WHERE row.outbox_id=candidate.outbox_id;
 GET DIAGNOSTICS outbox_evidence_deleted=ROW_COUNT;

 WITH verification AS (SELECT id FROM email_verification_tokens
   WHERE GREATEST(expires_at,COALESCE(consumed_at,expires_at))<=v_now-interval '30 days'
   ORDER BY GREATEST(expires_at,COALESCE(consumed_at,expires_at)),id FOR UPDATE SKIP LOCKED LIMIT p_batch_limit),
 deleted_verification AS (DELETE FROM email_verification_tokens row USING verification WHERE row.id=verification.id RETURNING 1),
 reset AS (SELECT id FROM password_reset_tokens
   WHERE GREATEST(expires_at,COALESCE(consumed_at,expires_at))<=v_now-interval '30 days'
   ORDER BY GREATEST(expires_at,COALESCE(consumed_at,expires_at)),id FOR UPDATE SKIP LOCKED LIMIT p_batch_limit),
 deleted_reset AS (DELETE FROM password_reset_tokens row USING reset WHERE row.id=reset.id RETURNING 1)
 SELECT (SELECT count(*) FROM deleted_verification)+(SELECT count(*) FROM deleted_reset) INTO tokens_deleted;

 WITH candidate AS (SELECT id FROM consent_forms WHERE date_signed<=v_now-interval '12 months'
   AND (ip_address IS NOT NULL OR user_agent<>'') ORDER BY date_signed,id FOR UPDATE SKIP LOCKED LIMIT p_batch_limit)
 UPDATE consent_forms row SET ip_address=NULL,user_agent='' FROM candidate WHERE row.id=candidate.id;
 GET DIAGNOSTICS consent_network_scrubbed=ROW_COUNT;

 WITH candidate AS (SELECT response.event_id,response.user_id FROM event_responses response JOIN events event ON event.id=response.event_id
   WHERE event.ends_at<=v_now-interval '90 days' ORDER BY event.ends_at,response.event_id,response.user_id FOR UPDATE OF response SKIP LOCKED LIMIT p_batch_limit)
 DELETE FROM event_responses row USING candidate WHERE row.event_id=candidate.event_id AND row.user_id=candidate.user_id;
 GET DIAGNOSTICS event_responses_deleted=ROW_COUNT;

 WITH candidate AS (SELECT announcement_id,user_id FROM announcement_deliveries WHERE delivered_at<=v_now-interval '90 days'
   ORDER BY delivered_at,announcement_id,user_id FOR UPDATE SKIP LOCKED LIMIT p_batch_limit)
 DELETE FROM announcement_deliveries row USING candidate WHERE row.announcement_id=candidate.announcement_id AND row.user_id=candidate.user_id;
 GET DIAGNOSTICS announcement_deliveries_deleted=ROW_COUNT;

 -- responded_at is written atomically with both currently terminal statuses.
 -- Non-terminal and legacy rows without it remain untouched.
 WITH candidate AS (SELECT id FROM suggestions WHERE status IN ('DECLINED','COMPLETED') AND responded_at<=v_now-interval '12 months'
   ORDER BY responded_at,id FOR UPDATE SKIP LOCKED LIMIT p_batch_limit)
 DELETE FROM suggestions row USING candidate WHERE row.id=candidate.id;
 GET DIAGNOSTICS suggestions_deleted=ROW_COUNT;

 -- Working material can be scrubbed on its exact stored clock for every
 -- terminal case. Evidence deletion for completed executions remains blocked
 -- until #248 creates and binds the completion manifest.
 WITH candidate AS (SELECT id FROM data_erasure_requests WHERE status IN ('REFUSED','CANCELLED','COMPLETED')
   AND working_expires_at<=v_now AND working_erased_at IS NULL ORDER BY working_expires_at,id FOR UPDATE SKIP LOCKED LIMIT p_batch_limit),
 removed_mail AS (DELETE FROM email_outbox row USING candidate WHERE row.privacy_request_id=candidate.id RETURNING row.id),
 removed_dependants AS (DELETE FROM privacy_request_dependant_resolutions row USING candidate WHERE row.request_id=candidate.id RETURNING 1)
 UPDATE data_erasure_requests row SET decision_explanation='',category_decisions='[]',categories='{}',policy_snapshot=NULL,policy_version=NULL,
  identity_verified_at=NULL,identity_verified_by=NULL,identity_method=NULL,
  representation_verified_at=NULL,representation_verified_by=NULL,representation_method=NULL,representation_guardian_id=NULL,
  representation_relationship_updated_at=NULL,representation_conflict=false,
  requester_user_id=NULL,subject_user_id=NULL,claimed_by=NULL,decided_by=NULL,working_erased_at=v_now
 FROM candidate WHERE row.id=candidate.id;
 GET DIAGNOSTICS privacy_working_scrubbed=ROW_COUNT;

 WITH candidate AS (SELECT bucket FROM privacy_request_auth_limits WHERE window_start<v_now-interval '1 day'
  ORDER BY window_start,bucket FOR UPDATE SKIP LOCKED LIMIT p_batch_limit)
 DELETE FROM privacy_request_auth_limits row USING candidate WHERE row.bucket=candidate.bucket;
 GET DIAGNOSTICS auth_limits_deleted=ROW_COUNT;

 INSERT INTO privacy_retention_runs VALUES(run_id,p_worker_ref,v_started,clock_timestamp(),p_batch_limit,
  sessions_deleted,tokens_deleted,outbox_stopped,outbox_payloads_deleted,outbox_evidence_deleted,consent_network_scrubbed,
  event_responses_deleted,announcement_deliveries_deleted,suggestions_deleted,privacy_working_scrubbed,auth_limits_deleted);
 RETURN NEXT;
END;
$$;

REVOKE ALL ON FUNCTION privacy_retention_run(uuid,integer) FROM PUBLIC;
REVOKE ALL ON TABLE privacy_retention_runs,privacy_outbox_delivery_evidence FROM PUBLIC;
