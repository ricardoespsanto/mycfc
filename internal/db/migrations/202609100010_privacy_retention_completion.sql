-- #247: complete the approved consent, audit and repair-object retention
-- clocks and expose one bounded, least-privilege maintenance API. This remains
-- inactive until a separate login is granted the NOLOGIN maintenance role and
-- the host timer is explicitly enabled.

ALTER TABLE consent_forms
 ADD COLUMN ceased_at timestamptz NULL,
 ADD COLUMN cessation_reason varchar(40) NULL,
 ADD COLUMN evidence_expires_at timestamptz NULL,
 ADD CONSTRAINT consent_cessation_complete CHECK (
  (ceased_at IS NULL AND cessation_reason IS NULL AND evidence_expires_at IS NULL)
  OR (ceased_at IS NOT NULL AND ceased_at>=date_signed
      AND cessation_reason IN ('SUPERSEDED','WITHDRAWN','PROCESSING_ENDED','ACCOUNT_ERASURE')
      AND evidence_expires_at=ceased_at+interval '3 years')
 ) NOT VALID;
ALTER TABLE consent_forms VALIDATE CONSTRAINT consent_cessation_complete;
CREATE INDEX consent_forms_evidence_expiry_idx ON consent_forms(evidence_expires_at,id) WHERE evidence_expires_at IS NOT NULL;

CREATE FUNCTION privacy_consent_cessation_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.ceased_at IS NOT NULL AND ROW(NEW.ceased_at,NEW.cessation_reason,NEW.evidence_expires_at)
    IS DISTINCT FROM ROW(OLD.ceased_at,OLD.cessation_reason,OLD.evidence_expires_at) THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='consent_cessation_is_immutable';
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER consent_forms_cessation_immutable BEFORE UPDATE ON consent_forms
 FOR EACH ROW EXECUTE FUNCTION privacy_consent_cessation_immutable();

CREATE FUNCTION privacy_consent_cease(
 p_user_id uuid,p_consent_type text,p_except_id uuid,p_reason text,p_ceased_at timestamptz
) RETURNS integer LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE changed integer;
BEGIN
 IF p_user_id IS NULL OR p_consent_type NOT IN ('Termos_Gerais','Uso_Imagem','Responsabilidade_Menor','Dados_Saude','Foto_Perfil')
    OR p_reason NOT IN ('SUPERSEDED','WITHDRAWN','PROCESSING_ENDED','ACCOUNT_ERASURE')
    OR p_ceased_at IS NULL OR p_ceased_at>clock_timestamp()+interval '1 minute' THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='consent_cessation_rejected';
 END IF;
 UPDATE consent_forms SET ceased_at=p_ceased_at,cessation_reason=p_reason,evidence_expires_at=p_ceased_at+interval '3 years'
 WHERE user_id=p_user_id AND consent_type::text=p_consent_type AND ceased_at IS NULL
   AND id IS DISTINCT FROM p_except_id AND date_signed<=p_ceased_at;
 GET DIAGNOSTICS changed=ROW_COUNT;
 RETURN changed;
END; $$;
REVOKE ALL ON FUNCTION privacy_consent_cease(uuid,text,uuid,text,timestamptz) FROM PUBLIC;

CREATE FUNCTION privacy_consent_cease_on_erasure() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
 IF OLD.erased_at IS NULL AND NEW.erased_at IS NOT NULL THEN
  PERFORM privacy_consent_cease(NEW.id,kind::text,NULL,'ACCOUNT_ERASURE',NEW.erased_at)
  FROM (SELECT DISTINCT consent_type AS kind FROM consent_forms WHERE user_id=NEW.id AND ceased_at IS NULL) active;
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER users_consent_cessation AFTER UPDATE OF erased_at ON users
 FOR EACH ROW EXECUTE FUNCTION privacy_consent_cease_on_erasure();
CREATE FUNCTION privacy_profile_photo_requires_active_consent() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.photo_consent_form_id IS NOT NULL AND NOT EXISTS(
  SELECT 1 FROM consent_forms consent WHERE consent.id=NEW.photo_consent_form_id
    AND consent.user_id=NEW.user_id AND consent.consent_type='Foto_Perfil' AND consent.ceased_at IS NULL
 ) THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='profile_photo_active_consent_required';
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER member_profiles_photo_active_consent BEFORE INSERT OR UPDATE OF user_id,photo_consent_form_id ON member_profiles
 FOR EACH ROW EXECUTE FUNCTION privacy_profile_photo_requires_active_consent();
REVOKE ALL ON FUNCTION privacy_consent_cessation_immutable(),privacy_consent_cease_on_erasure(),privacy_profile_photo_requires_active_consent() FROM PUBLIC;

-- Repair photos are still useful during active triage, but the accepted
-- technical-retention ceiling is 30 days from upload/report creation. Queue at
-- day 23 to leave a full seven-day verification window. Legacy pointers are
-- never guessed; monitoring reports them as a release-blocking backlog.
CREATE FUNCTION privacy_retention_queue_repair_attachments(p_batch_limit integer)
RETURNS integer LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE item record; latest_status text; latest_sequence integer; changed integer:=0; v_now timestamptz:=clock_timestamp();
BEGIN
 IF p_batch_limit NOT BETWEEN 1 AND 10000 THEN RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy retention input rejected'; END IF;
 FOR item IN
  SELECT repair.id,repair.image_upload_intent_id
  FROM repair_requests repair
  WHERE repair.date_reported<=v_now-interval '23 days' AND repair.image_object_key IS NOT NULL
    AND repair.image_upload_intent_id IS NOT NULL
  ORDER BY repair.date_reported,repair.id FOR UPDATE SKIP LOCKED LIMIT p_batch_limit
 LOOP
  PERFORM privacy_upload_source_lock('REPAIR_ATTACHMENT',item.id);
  SELECT status,sequence INTO latest_status,latest_sequence
  FROM privacy_protected.object_upload_intent_events WHERE intent_id=item.image_upload_intent_id
  ORDER BY sequence DESC LIMIT 1 FOR UPDATE;
  IF latest_status='ATTACHED' THEN
   INSERT INTO privacy_protected.object_upload_intent_events(intent_id,sequence,status,reason_code,occurred_at)
   VALUES(item.image_upload_intent_id,latest_sequence+1,'CLEANUP_REQUIRED','RETENTION_EXPIRED',v_now);
   INSERT INTO privacy_protected.object_upload_cleanup_jobs(intent_id,status,next_attempt_at,updated_at)
   VALUES(item.image_upload_intent_id,'PENDING',v_now,v_now) ON CONFLICT(intent_id) DO NOTHING;
   UPDATE repair_requests SET image_object_key=NULL,image_content_type=NULL,image_size_bytes=NULL,image_upload_intent_id=NULL,updated_at=v_now
   WHERE id=item.id AND image_upload_intent_id=item.image_upload_intent_id;
   IF FOUND THEN changed:=changed+1; END IF;
  ELSIF latest_status NOT IN ('CLEANUP_REQUIRED','ABSENCE_VERIFIED') THEN
   RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_repair_retention_state_invalid';
  END IF;
 END LOOP;
 RETURN changed;
END; $$;
REVOKE ALL ON FUNCTION privacy_retention_queue_repair_attachments(integer) FROM PUBLIC;

-- Pseudonymise only audit rows whose own 24-month clock has elapsed. Newer
-- rows for the same person remain accountable until their independent clock.
CREATE FUNCTION privacy_retention_pseudonymize_audit(p_batch_limit integer)
RETURNS integer LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE audit_row record; subject_ref uuid; subject_refs uuid[]; principal_ref uuid;
 subject_name text; subject_email text; subject_login text; subject_erased timestamptz;
 changed integer:=0; cutoff timestamptz:=clock_timestamp()-interval '24 months';
BEGIN
 IF p_batch_limit NOT BETWEEN 1 AND 10000 THEN RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy retention input rejected'; END IF;
 FOR audit_row IN
  SELECT kind,id,occurred_at FROM (
   SELECT 'MINOR'::text kind,id,created_at occurred_at FROM minor_credential_audit WHERE created_at<=cutoff AND (minor_user_id IS NOT NULL OR guardian_user_id IS NOT NULL OR actor_user_id IS NOT NULL)
   UNION ALL SELECT 'EQUIPMENT',id,occurred_at FROM equipment_audit_events WHERE occurred_at<=cutoff AND actor_user_id IS NOT NULL
   UNION ALL SELECT 'PROFILE',id,occurred_at FROM member_profile_audit_events WHERE occurred_at<=cutoff AND (actor_user_id IS NOT NULL OR subject_user_id IS NOT NULL)
   UNION ALL SELECT 'STAFF',id,occurred_at FROM staff_grant_audit_events WHERE occurred_at<=cutoff AND actor_user_id IS NOT NULL
   UNION ALL SELECT 'ALBUM',id,occurred_at FROM photo_album_audit_events WHERE occurred_at<=cutoff AND actor_user_id IS NOT NULL
   UNION ALL SELECT 'ANNOUNCEMENT',id,occurred_at FROM announcement_audit_events WHERE occurred_at<=cutoff AND actor_user_id IS NOT NULL
   UNION ALL SELECT 'FEATURE',id,occurred_at FROM feature_flag_events WHERE occurred_at<=cutoff AND actor_user_id IS NOT NULL
   UNION ALL SELECT 'TRAINING_COPY',id,copied_at FROM training_copy_events WHERE copied_at<=cutoff AND copied_by_id IS NOT NULL
  ) due ORDER BY occurred_at,kind,id LIMIT p_batch_limit
 LOOP
  CASE audit_row.kind
  WHEN 'MINOR' THEN SELECT ARRAY(SELECT DISTINCT x FROM unnest(ARRAY[minor_user_id,guardian_user_id,actor_user_id]) x WHERE x IS NOT NULL) INTO subject_refs FROM minor_credential_audit WHERE id=audit_row.id FOR UPDATE;
  WHEN 'PROFILE' THEN SELECT ARRAY(SELECT DISTINCT x FROM unnest(ARRAY[actor_user_id,subject_user_id]) x WHERE x IS NOT NULL) INTO subject_refs FROM member_profile_audit_events WHERE id=audit_row.id FOR UPDATE;
  WHEN 'EQUIPMENT' THEN SELECT ARRAY[actor_user_id] INTO subject_refs FROM equipment_audit_events WHERE id=audit_row.id FOR UPDATE;
  WHEN 'STAFF' THEN SELECT ARRAY[actor_user_id] INTO subject_refs FROM staff_grant_audit_events WHERE id=audit_row.id FOR UPDATE;
  WHEN 'ALBUM' THEN SELECT ARRAY[actor_user_id] INTO subject_refs FROM photo_album_audit_events WHERE id=audit_row.id FOR UPDATE;
  WHEN 'ANNOUNCEMENT' THEN SELECT ARRAY[actor_user_id] INTO subject_refs FROM announcement_audit_events WHERE id=audit_row.id FOR UPDATE;
  WHEN 'FEATURE' THEN SELECT ARRAY[actor_user_id] INTO subject_refs FROM feature_flag_events WHERE id=audit_row.id FOR UPDATE;
  WHEN 'TRAINING_COPY' THEN SELECT ARRAY[copied_by_id] INTO subject_refs FROM training_copy_events WHERE id=audit_row.id FOR UPDATE;
  END CASE;
  FOREACH subject_ref IN ARRAY subject_refs LOOP
   SELECT name,email::text,minor_login_id::text,erased_at INTO subject_name,subject_email,subject_login,subject_erased FROM users WHERE id=subject_ref;
   IF NOT FOUND OR (subject_erased IS NOT NULL AND audit_row.kind IN ('EQUIPMENT','STAFF')) THEN
    RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_audit_identity_unavailable';
   END IF;
   INSERT INTO privacy_pseudonymous_principals(purpose) VALUES('AUDIT') RETURNING id INTO principal_ref;
   PERFORM set_config('mycfc.privacy_erasure_operation','AUDIT_ACTOR_ANONYMIZE',true);
   CASE audit_row.kind
   WHEN 'MINOR' THEN UPDATE minor_credential_audit SET
     minor_principal_id=CASE WHEN minor_user_id=subject_ref THEN principal_ref ELSE minor_principal_id END,
     minor_user_id=CASE WHEN minor_user_id=subject_ref THEN NULL ELSE minor_user_id END,
     guardian_principal_id=CASE WHEN guardian_user_id=subject_ref THEN principal_ref ELSE guardian_principal_id END,
     guardian_user_id=CASE WHEN guardian_user_id=subject_ref THEN NULL ELSE guardian_user_id END,
     actor_principal_id=CASE WHEN actor_user_id=subject_ref THEN principal_ref ELSE actor_principal_id END,
     actor_user_id=CASE WHEN actor_user_id=subject_ref THEN NULL ELSE actor_user_id END,
     issued_login_id=CASE WHEN minor_user_id=subject_ref THEN 'anonymised' ELSE privacy_scrub_audit_text(issued_login_id::text,subject_ref,subject_name,subject_email,subject_login) END
    WHERE id=audit_row.id;
   WHEN 'EQUIPMENT' THEN UPDATE equipment_audit_events SET actor_user_id=NULL,actor_principal_id=principal_ref,
     before_state=privacy_scrub_audit_json(before_state,subject_ref,subject_name,subject_email,subject_login),
     after_state=privacy_scrub_audit_json(after_state,subject_ref,subject_name,subject_email,subject_login) WHERE id=audit_row.id AND actor_user_id=subject_ref;
   WHEN 'PROFILE' THEN UPDATE member_profile_audit_events SET
     actor_principal_id=CASE WHEN actor_user_id=subject_ref THEN principal_ref ELSE actor_principal_id END,
     actor_user_id=CASE WHEN actor_user_id=subject_ref THEN NULL ELSE actor_user_id END,
     subject_principal_id=CASE WHEN subject_user_id=subject_ref THEN principal_ref ELSE subject_principal_id END,
     subject_user_id=CASE WHEN subject_user_id=subject_ref THEN NULL ELSE subject_user_id END WHERE id=audit_row.id;
   WHEN 'STAFF' THEN UPDATE staff_grant_audit_events SET actor_user_id=NULL,actor_principal_id=principal_ref,
     reason=privacy_scrub_audit_text(reason,subject_ref,subject_name,subject_email,subject_login) WHERE id=audit_row.id AND actor_user_id=subject_ref;
   WHEN 'ALBUM' THEN UPDATE photo_album_audit_events SET actor_user_id=NULL,actor_principal_id=principal_ref WHERE id=audit_row.id AND actor_user_id=subject_ref;
   WHEN 'ANNOUNCEMENT' THEN UPDATE announcement_audit_events SET actor_user_id=NULL,actor_principal_id=principal_ref WHERE id=audit_row.id AND actor_user_id=subject_ref;
   WHEN 'FEATURE' THEN UPDATE feature_flag_events SET actor_user_id=NULL,actor_principal_id=principal_ref WHERE id=audit_row.id AND actor_user_id=subject_ref;
   WHEN 'TRAINING_COPY' THEN UPDATE training_copy_events SET copied_by_id=NULL,copied_by_principal_id=principal_ref WHERE id=audit_row.id AND copied_by_id=subject_ref;
   END CASE;
  END LOOP;
  changed:=changed+1;
 END LOOP;
 RETURN changed;
END; $$;
REVOKE ALL ON FUNCTION privacy_retention_pseudonymize_audit(integer) FROM PUBLIC;

ALTER TABLE privacy_retention_runs
 ADD COLUMN consent_evidence_deleted integer NOT NULL DEFAULT 0 CHECK(consent_evidence_deleted>=0),
 ADD COLUMN audit_events_pseudonymized integer NOT NULL DEFAULT 0 CHECK(audit_events_pseudonymized>=0),
 ADD COLUMN repair_attachments_queued integer NOT NULL DEFAULT 0 CHECK(repair_attachments_queued>=0);

DROP FUNCTION privacy_retention_run(uuid,integer);
CREATE FUNCTION privacy_retention_run(p_worker_ref uuid,p_batch_limit integer)
RETURNS TABLE(
 run_id uuid,sessions_deleted integer,tokens_deleted integer,outbox_stopped integer,
 outbox_payloads_deleted integer,outbox_evidence_deleted integer,consent_network_scrubbed integer,
 consent_evidence_deleted integer,audit_events_pseudonymized integer,repair_attachments_queued integer,
 event_responses_deleted integer,announcement_deliveries_deleted integer,suggestions_deleted integer,
 privacy_working_scrubbed integer,auth_limits_deleted integer
) LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE v_now timestamptz:=clock_timestamp(); v_started timestamptz:=v_now;
BEGIN
 IF p_worker_ref IS NULL OR p_batch_limit NOT BETWEEN 1 AND 10000 THEN RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy retention input rejected'; END IF;
 IF NOT pg_try_advisory_xact_lock(247,247) THEN RAISE EXCEPTION USING ERRCODE='55P03',MESSAGE='privacy retention already running'; END IF;
 run_id:=gen_random_uuid(); sessions_deleted:=0;tokens_deleted:=0;outbox_stopped:=0;outbox_payloads_deleted:=0;outbox_evidence_deleted:=0;
 consent_network_scrubbed:=0;consent_evidence_deleted:=0;audit_events_pseudonymized:=0;repair_attachments_queued:=0;
 event_responses_deleted:=0;announcement_deliveries_deleted:=0;suggestions_deleted:=0;privacy_working_scrubbed:=0;auth_limits_deleted:=0;

 WITH candidate AS(SELECT token FROM sessions WHERE expiry<=v_now ORDER BY expiry,token FOR UPDATE SKIP LOCKED LIMIT p_batch_limit)
 DELETE FROM sessions row USING candidate WHERE row.token=candidate.token;GET DIAGNOSTICS sessions_deleted=ROW_COUNT;
 WITH candidate AS(SELECT id FROM email_outbox WHERE created_at<=v_now-interval '7 days' AND status IN('PENDING','SENDING') ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT p_batch_limit)
 UPDATE email_outbox row SET status='FAILED',claimed_at=NULL,last_error=NULL,updated_at=v_now FROM candidate WHERE row.id=candidate.id;GET DIAGNOSTICS outbox_stopped=ROW_COUNT;
 WITH candidate AS MATERIALIZED(SELECT id FROM email_outbox WHERE created_at<=v_now-interval '30 days' ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT p_batch_limit),
 evidence AS(INSERT INTO privacy_outbox_delivery_evidence(outbox_id,message_type,final_status,attempts,created_at,terminal_at,expires_at)
  SELECT row.id,row.message_type,CASE WHEN row.status IN('SENT','FAILED','CANCELLED') THEN row.status ELSE 'FAILED' END,row.attempts,row.created_at,COALESCE(row.sent_at,row.updated_at,row.created_at),row.created_at+interval '90 days'
  FROM email_outbox row JOIN candidate USING(id) ON CONFLICT(outbox_id) DO NOTHING RETURNING outbox_id)
 DELETE FROM email_outbox row USING candidate WHERE row.id=candidate.id;GET DIAGNOSTICS outbox_payloads_deleted=ROW_COUNT;
 WITH candidate AS(SELECT outbox_id FROM privacy_outbox_delivery_evidence WHERE expires_at<=v_now ORDER BY expires_at,outbox_id FOR UPDATE SKIP LOCKED LIMIT p_batch_limit)
 DELETE FROM privacy_outbox_delivery_evidence row USING candidate WHERE row.outbox_id=candidate.outbox_id;GET DIAGNOSTICS outbox_evidence_deleted=ROW_COUNT;
 WITH verification AS(SELECT id FROM email_verification_tokens WHERE GREATEST(expires_at,COALESCE(consumed_at,expires_at))<=v_now-interval '30 days' ORDER BY GREATEST(expires_at,COALESCE(consumed_at,expires_at)),id FOR UPDATE SKIP LOCKED LIMIT p_batch_limit),
 dv AS(DELETE FROM email_verification_tokens row USING verification WHERE row.id=verification.id RETURNING 1),
 reset AS(SELECT id FROM password_reset_tokens WHERE GREATEST(expires_at,COALESCE(consumed_at,expires_at))<=v_now-interval '30 days' ORDER BY GREATEST(expires_at,COALESCE(consumed_at,expires_at)),id FOR UPDATE SKIP LOCKED LIMIT p_batch_limit),
 dr AS(DELETE FROM password_reset_tokens row USING reset WHERE row.id=reset.id RETURNING 1)
 SELECT (SELECT count(*) FROM dv)+(SELECT count(*) FROM dr) INTO tokens_deleted;
 WITH candidate AS(SELECT id FROM consent_forms WHERE date_signed<=v_now-interval '12 months' AND(ip_address IS NOT NULL OR user_agent<>'') ORDER BY date_signed,id FOR UPDATE SKIP LOCKED LIMIT p_batch_limit)
 UPDATE consent_forms row SET ip_address=NULL,user_agent='' FROM candidate WHERE row.id=candidate.id;GET DIAGNOSTICS consent_network_scrubbed=ROW_COUNT;
 WITH candidate AS(SELECT id FROM consent_forms WHERE evidence_expires_at<=v_now AND NOT EXISTS(SELECT 1 FROM member_profiles p WHERE p.photo_consent_form_id=consent_forms.id) ORDER BY evidence_expires_at,id FOR UPDATE SKIP LOCKED LIMIT p_batch_limit)
 DELETE FROM consent_forms row USING candidate WHERE row.id=candidate.id;GET DIAGNOSTICS consent_evidence_deleted=ROW_COUNT;
 audit_events_pseudonymized:=privacy_retention_pseudonymize_audit(p_batch_limit);
 repair_attachments_queued:=privacy_retention_queue_repair_attachments(p_batch_limit);
 WITH candidate AS(SELECT response.event_id,response.user_id FROM event_responses response JOIN events event ON event.id=response.event_id WHERE event.ends_at<=v_now-interval '90 days' ORDER BY event.ends_at,response.event_id,response.user_id FOR UPDATE OF response SKIP LOCKED LIMIT p_batch_limit)
 DELETE FROM event_responses row USING candidate WHERE row.event_id=candidate.event_id AND row.user_id=candidate.user_id;GET DIAGNOSTICS event_responses_deleted=ROW_COUNT;
 WITH candidate AS(SELECT announcement_id,user_id FROM announcement_deliveries WHERE delivered_at<=v_now-interval '90 days' ORDER BY delivered_at,announcement_id,user_id FOR UPDATE SKIP LOCKED LIMIT p_batch_limit)
 DELETE FROM announcement_deliveries row USING candidate WHERE row.announcement_id=candidate.announcement_id AND row.user_id=candidate.user_id;GET DIAGNOSTICS announcement_deliveries_deleted=ROW_COUNT;
 WITH candidate AS(SELECT id FROM suggestions WHERE status IN('DECLINED','COMPLETED') AND responded_at<=v_now-interval '12 months' ORDER BY responded_at,id FOR UPDATE SKIP LOCKED LIMIT p_batch_limit)
 DELETE FROM suggestions row USING candidate WHERE row.id=candidate.id;GET DIAGNOSTICS suggestions_deleted=ROW_COUNT;
 WITH candidate AS(SELECT id FROM data_erasure_requests WHERE status IN('REFUSED','CANCELLED','COMPLETED') AND working_expires_at<=v_now AND working_erased_at IS NULL ORDER BY working_expires_at,id FOR UPDATE SKIP LOCKED LIMIT p_batch_limit),
 removed_mail AS(DELETE FROM email_outbox row USING candidate WHERE row.privacy_request_id=candidate.id RETURNING row.id),
 removed_dependants AS(DELETE FROM privacy_request_dependant_resolutions row USING candidate WHERE row.request_id=candidate.id RETURNING 1)
 UPDATE data_erasure_requests row SET decision_explanation='',category_decisions='[]',categories='{}',policy_snapshot=NULL,policy_version=NULL,
  identity_verified_at=NULL,identity_verified_by=NULL,identity_method=NULL,representation_verified_at=NULL,representation_verified_by=NULL,representation_method=NULL,
  representation_guardian_id=NULL,representation_relationship_updated_at=NULL,representation_conflict=false,requester_user_id=NULL,subject_user_id=NULL,claimed_by=NULL,decided_by=NULL,working_erased_at=v_now
 FROM candidate WHERE row.id=candidate.id;GET DIAGNOSTICS privacy_working_scrubbed=ROW_COUNT;
 WITH candidate AS(SELECT bucket FROM privacy_request_auth_limits WHERE window_start<v_now-interval '1 day' ORDER BY window_start,bucket FOR UPDATE SKIP LOCKED LIMIT p_batch_limit)
 DELETE FROM privacy_request_auth_limits row USING candidate WHERE row.bucket=candidate.bucket;GET DIAGNOSTICS auth_limits_deleted=ROW_COUNT;
 INSERT INTO privacy_retention_runs(id,worker_ref,started_at,finished_at,batch_limit,sessions_deleted,tokens_deleted,outbox_stopped,outbox_payloads_deleted,outbox_evidence_deleted,
  consent_network_scrubbed,event_responses_deleted,announcement_deliveries_deleted,suggestions_deleted,privacy_working_scrubbed,auth_limits_deleted,
  consent_evidence_deleted,audit_events_pseudonymized,repair_attachments_queued)
 VALUES(run_id,p_worker_ref,v_started,clock_timestamp(),p_batch_limit,sessions_deleted,tokens_deleted,outbox_stopped,outbox_payloads_deleted,outbox_evidence_deleted,
  consent_network_scrubbed,event_responses_deleted,announcement_deliveries_deleted,suggestions_deleted,privacy_working_scrubbed,auth_limits_deleted,
  consent_evidence_deleted,audit_events_pseudonymized,repair_attachments_queued);
 RETURN NEXT;
END; $$;
REVOKE ALL ON FUNCTION privacy_retention_run(uuid,integer) FROM PUBLIC;

CREATE FUNCTION privacy_retention_status() RETURNS TABLE(
 due_count bigint,oldest_due_age_seconds bigint,repair_due_count bigint,repair_overdue_count bigint,
 repair_terminal_failures bigint,repair_legacy_due_count bigint,last_run_age_seconds bigint
) LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 WITH clock AS(SELECT clock_timestamp() now),
 due AS(
  SELECT expiry due_at FROM sessions,clock WHERE expiry<=now
  UNION ALL SELECT created_at+interval '7 days' FROM email_outbox,clock WHERE status IN('PENDING','SENDING') AND created_at+interval '7 days'<=now
  UNION ALL SELECT created_at+interval '30 days' FROM email_outbox,clock WHERE created_at+interval '30 days'<=now
  UNION ALL SELECT expires_at FROM privacy_outbox_delivery_evidence,clock WHERE expires_at<=now
  UNION ALL SELECT GREATEST(expires_at,COALESCE(consumed_at,expires_at))+interval '30 days' FROM email_verification_tokens,clock WHERE GREATEST(expires_at,COALESCE(consumed_at,expires_at))+interval '30 days'<=now
  UNION ALL SELECT GREATEST(expires_at,COALESCE(consumed_at,expires_at))+interval '30 days' FROM password_reset_tokens,clock WHERE GREATEST(expires_at,COALESCE(consumed_at,expires_at))+interval '30 days'<=now
  UNION ALL SELECT date_signed+interval '12 months' FROM consent_forms,clock WHERE (ip_address IS NOT NULL OR user_agent<>'') AND date_signed+interval '12 months'<=now
  UNION ALL SELECT evidence_expires_at FROM consent_forms,clock WHERE evidence_expires_at<=now
  UNION ALL SELECT created_at+interval '24 months' FROM minor_credential_audit,clock WHERE (minor_user_id IS NOT NULL OR guardian_user_id IS NOT NULL OR actor_user_id IS NOT NULL) AND created_at+interval '24 months'<=now
  UNION ALL SELECT occurred_at+interval '24 months' FROM equipment_audit_events,clock WHERE actor_user_id IS NOT NULL AND occurred_at+interval '24 months'<=now
  UNION ALL SELECT occurred_at+interval '24 months' FROM member_profile_audit_events,clock WHERE (actor_user_id IS NOT NULL OR subject_user_id IS NOT NULL) AND occurred_at+interval '24 months'<=now
  UNION ALL SELECT occurred_at+interval '24 months' FROM staff_grant_audit_events,clock WHERE actor_user_id IS NOT NULL AND occurred_at+interval '24 months'<=now
  UNION ALL SELECT occurred_at+interval '24 months' FROM photo_album_audit_events,clock WHERE actor_user_id IS NOT NULL AND occurred_at+interval '24 months'<=now
  UNION ALL SELECT occurred_at+interval '24 months' FROM announcement_audit_events,clock WHERE actor_user_id IS NOT NULL AND occurred_at+interval '24 months'<=now
  UNION ALL SELECT occurred_at+interval '24 months' FROM feature_flag_events,clock WHERE actor_user_id IS NOT NULL AND occurred_at+interval '24 months'<=now
  UNION ALL SELECT copied_at+interval '24 months' FROM training_copy_events,clock WHERE copied_by_id IS NOT NULL AND copied_at+interval '24 months'<=now
  UNION ALL SELECT date_reported+interval '23 days' FROM repair_requests,clock WHERE image_object_key IS NOT NULL AND date_reported+interval '23 days'<=now
  UNION ALL SELECT event.ends_at+interval '90 days' FROM event_responses response JOIN events event ON event.id=response.event_id,clock WHERE event.ends_at+interval '90 days'<=now
  UNION ALL SELECT delivered_at+interval '90 days' FROM announcement_deliveries,clock WHERE delivered_at+interval '90 days'<=now
  UNION ALL SELECT responded_at+interval '12 months' FROM suggestions,clock WHERE status IN('DECLINED','COMPLETED') AND responded_at+interval '12 months'<=now
  UNION ALL SELECT working_expires_at FROM data_erasure_requests,clock WHERE status IN('REFUSED','CANCELLED','COMPLETED') AND working_erased_at IS NULL AND working_expires_at<=now
  UNION ALL SELECT window_start+interval '1 day' FROM privacy_request_auth_limits,clock WHERE window_start+interval '1 day'<=now
 ), repair AS(
  SELECT i.id,r.date_reported,latest.status,job.status job_status
  FROM privacy_protected.object_upload_intents i JOIN repair_requests r ON r.id=i.source_ref AND i.source_kind='REPAIR_ATTACHMENT'
  JOIN LATERAL(SELECT e.status FROM privacy_protected.object_upload_intent_events e WHERE e.intent_id=i.id ORDER BY e.sequence DESC LIMIT 1) latest ON true
  LEFT JOIN privacy_protected.object_upload_cleanup_jobs job ON job.intent_id=i.id
 )
 SELECT (SELECT count(*) FROM due),
  COALESCE((SELECT floor(extract(epoch FROM((SELECT now FROM clock)-min(due.due_at))))::bigint FROM due),0),
  (SELECT count(*) FROM repair,clock WHERE date_reported+interval '23 days'<=clock.now AND status<>'ABSENCE_VERIFIED'),
  (SELECT count(*) FROM repair,clock WHERE date_reported+interval '30 days'<=clock.now AND status<>'ABSENCE_VERIFIED'),
  (SELECT count(*) FROM repair WHERE job_status='TERMINAL_FAILED'),
  (SELECT count(*) FROM repair_requests,clock WHERE image_upload_intent_id IS NULL AND image_object_key IS NOT NULL AND date_reported+interval '23 days'<=clock.now),
  COALESCE((SELECT floor(extract(epoch FROM((SELECT now FROM clock)-max(finished_at))))::bigint FROM privacy_retention_runs),-1);
$$;
REVOKE ALL ON FUNCTION privacy_retention_status() FROM PUBLIC;

ALTER TABLE privacy_protected.object_upload_intent_events DROP CONSTRAINT object_upload_intent_events_reason_code_check;
ALTER TABLE privacy_protected.object_upload_intent_events ADD CONSTRAINT object_upload_intent_events_reason_code_check CHECK(reason_code IN(
 'UPLOAD_RESERVED','PUT_ACKNOWLEDGED','POINTER_ATTACHED','PUT_AMBIGUOUS','PUT_FAILED','ATTACH_FAILED','POINTER_SUPERSEDED','POINTER_REMOVED','STALE_TIMEOUT','RETENTION_EXPIRED','CLEANUP_CONFIRMED'));
ALTER TABLE privacy_protected.object_upload_intent_events DROP CONSTRAINT object_upload_intent_events_check;
ALTER TABLE privacy_protected.object_upload_intent_events ADD CONSTRAINT object_upload_intent_events_check CHECK(
 (status='PREPARED' AND reason_code='UPLOAD_RESERVED') OR(status='PUT_CONFIRMED' AND reason_code='PUT_ACKNOWLEDGED') OR(status='ATTACHED' AND reason_code='POINTER_ATTACHED')
 OR(status='CLEANUP_REQUIRED' AND reason_code IN('PUT_AMBIGUOUS','PUT_FAILED','ATTACH_FAILED','POINTER_SUPERSEDED','POINTER_REMOVED','STALE_TIMEOUT','RETENTION_EXPIRED'))
 OR(status='ABSENCE_VERIFIED' AND reason_code='CLEANUP_CONFIRMED'));
