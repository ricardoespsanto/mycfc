-- Complete the adopted v2 executor contract for retained and expiring
-- categories. Source facts are captured before account cut-off and kept in
-- the protected schema; the worker can only consume them through the exact
-- lease/attempt/epoch-fenced checkpoint routine below.

CREATE TABLE privacy_protected.execution_retention_sources (
 execution_id uuid NOT NULL,
 category_key varchar(120) NOT NULL,
 subject_ref uuid NOT NULL,
 anchor_at timestamptz NULL,
 retained_data jsonb NOT NULL CHECK(jsonb_typeof(retained_data)='object'),
 captured_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(execution_id,category_key),
 FOREIGN KEY(execution_id) REFERENCES public.privacy_erasure_executions(id) ON DELETE RESTRICT
);
CREATE TRIGGER execution_retention_sources_immutable BEFORE UPDATE OR DELETE
 ON privacy_protected.execution_retention_sources FOR EACH ROW
 EXECUTE FUNCTION public.prevent_privacy_execution_record_delete();

CREATE TABLE privacy_protected.execution_retention_pending (
 execution_id uuid NOT NULL,
 category_key varchar(120) NOT NULL,
 subject_ref uuid NOT NULL,
 anchor_code varchar(80) NOT NULL CHECK(anchor_code='CASE_CLOSURE'),
 retention_unit varchar(40) NOT NULL,
 review_after integer NOT NULL CHECK(review_after>0),
 expire_after integer NOT NULL CHECK(expire_after>=review_after),
 retained_field_codes text[] NOT NULL CHECK(cardinality(retained_field_codes)>0 AND array_position(retained_field_codes,NULL) IS NULL),
 retained_data jsonb NOT NULL CHECK(jsonb_typeof(retained_data)='object'),
 worker_ref uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(execution_id,category_key),
 FOREIGN KEY(execution_id) REFERENCES public.privacy_erasure_executions(id) ON DELETE RESTRICT
);
CREATE TRIGGER execution_retention_pending_immutable BEFORE UPDATE OR DELETE
 ON privacy_protected.execution_retention_pending FOR EACH ROW
 EXECUTE FUNCTION public.prevent_privacy_execution_record_delete();

CREATE FUNCTION public.privacy_retention_add_offset(p_anchor timestamptz,p_amount integer,p_unit text)
RETURNS timestamptz LANGUAGE plpgsql IMMUTABLE AS $$
BEGIN
 CASE p_unit
 WHEN 'HOUR' THEN RETURN p_anchor+make_interval(hours=>p_amount);
 WHEN 'CALENDAR_DAY' THEN RETURN p_anchor+make_interval(days=>p_amount);
 WHEN 'CALENDAR_MONTH' THEN RETURN p_anchor+make_interval(months=>p_amount);
 WHEN 'CALENDAR_YEAR' THEN RETURN p_anchor+make_interval(years=>p_amount);
 ELSE RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy_retention_unit_invalid';
 END CASE;
END;$$;

CREATE FUNCTION public.privacy_retention_select_fields(p_data jsonb,p_fields text[])
RETURNS jsonb LANGUAGE sql IMMUTABLE AS $$
 SELECT COALESCE(jsonb_object_agg(field,p_data->field ORDER BY field),'{}'::jsonb)
 FROM unnest(p_fields) field WHERE p_data ? field
$$;

CREATE FUNCTION public.privacy_execution_capture_retention_sources(p_execution_id uuid,p_subject_id uuid)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE request_ref uuid;accepted timestamptz;retained_categories text[];
BEGIN
 SELECT execution.request_id,execution.accepted_at INTO request_ref,accepted
 FROM privacy_erasure_executions execution JOIN data_erasure_requests request ON request.id=execution.request_id
 WHERE execution.id=p_execution_id AND request.subject_user_id=p_subject_id FOR SHARE OF execution,request;
 IF request_ref IS NULL THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_retention_capture_invalid'; END IF;
 SELECT array_agg(entry->>'category' ORDER BY entry->>'category') INTO retained_categories
 FROM privacy_erasure_executions execution JOIN privacy_request_execution_plans plan ON plan.request_id=execution.request_id
 CROSS JOIN LATERAL jsonb_array_elements(plan.plan->'entries') entry
 WHERE execution.id=p_execution_id AND entry->>'disposition' IN ('RESTRICT','EXPIRE');

 INSERT INTO privacy_protected.execution_retention_sources(execution_id,category_key,subject_ref,anchor_at,retained_data)
 SELECT p_execution_id,'sessions',p_subject_id,max(expiry),jsonb_build_object('users.id',to_jsonb(p_subject_id::text))
 FROM sessions WHERE subject_indexed AND user_id=p_subject_id AND 'sessions'=ANY(retained_categories) HAVING count(*)>0 ON CONFLICT DO NOTHING;

 INSERT INTO privacy_protected.execution_retention_sources(execution_id,category_key,subject_ref,anchor_at,retained_data)
 SELECT p_execution_id,'consent-evidence',p_subject_id,accepted,
  jsonb_build_object(
   'consent.decided_at',COALESCE(jsonb_agg(to_char(date_signed AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"') ORDER BY date_signed,id),'[]'::jsonb),
   'consent.decision',COALESCE(jsonb_agg(is_accepted ORDER BY date_signed,id),'[]'::jsonb),
   'consent.document_hash',COALESCE(jsonb_agg(document_sha256 ORDER BY date_signed,id),'[]'::jsonb),
   'consent.document_type',COALESCE(jsonb_agg(consent_type::text ORDER BY date_signed,id),'[]'::jsonb),
   'consent.document_version',COALESCE(jsonb_agg(document_version ORDER BY date_signed,id),'[]'::jsonb),
   'consent.method',COALESCE(jsonb_agg(CASE WHEN granted_by_user_id IS NULL OR granted_by_user_id=user_id THEN 'SELF' ELSE 'REPRESENTATIVE' END ORDER BY date_signed,id),'[]'::jsonb))
 FROM consent_forms WHERE user_id=p_subject_id AND 'consent-evidence'=ANY(retained_categories) HAVING count(*)>0 ON CONFLICT DO NOTHING;

 INSERT INTO privacy_protected.execution_retention_sources(execution_id,category_key,subject_ref,anchor_at,retained_data)
 SELECT p_execution_id,'consent-network',p_subject_id,max(date_signed),
  jsonb_build_object('consent.decided_at',COALESCE(jsonb_agg(to_char(date_signed AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"') ORDER BY date_signed,id),'[]'::jsonb))
 FROM consent_forms WHERE user_id=p_subject_id AND 'consent-network'=ANY(retained_categories) HAVING count(*)>0 ON CONFLICT DO NOTHING;

 INSERT INTO privacy_protected.execution_retention_sources(execution_id,category_key,subject_ref,anchor_at,retained_data)
 SELECT p_execution_id,'profile-core',p_subject_id,NULL,
  jsonb_build_object('member_profile.federation_id',to_jsonb(federation_licence_number))
 FROM member_profiles WHERE user_id=p_subject_id AND 'profile-core'=ANY(retained_categories) ON CONFLICT DO NOTHING;
 INSERT INTO privacy_protected.execution_retention_sources(execution_id,category_key,subject_ref,anchor_at,retained_data)
 SELECT p_execution_id,'identity-core',p_subject_id,NULL,jsonb_build_object('users.id',p_subject_id::text)
 WHERE 'identity-core'=ANY(retained_categories) ON CONFLICT DO NOTHING;

 INSERT INTO privacy_protected.execution_retention_sources(execution_id,category_key,subject_ref,anchor_at,retained_data)
 SELECT p_execution_id,'outbox-email',p_subject_id,max(outbox.created_at),
  jsonb_build_object('audit.occurred_at',COALESCE(jsonb_agg(to_char(outbox.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"') ORDER BY outbox.created_at,outbox.id),'[]'::jsonb))
 FROM email_outbox outbox LEFT JOIN email_verification_tokens verification ON verification.id=outbox.verification_token_id
 LEFT JOIN password_reset_tokens reset ON reset.id=outbox.password_reset_token_id
 WHERE (verification.user_id=p_subject_id OR reset.user_id=p_subject_id OR outbox.privacy_requester_id=p_subject_id)
  AND 'outbox-email'=ANY(retained_categories)
 HAVING count(*)>0
 ON CONFLICT DO NOTHING;

 INSERT INTO privacy_protected.execution_retention_sources(execution_id,category_key,subject_ref,anchor_at,retained_data)
 SELECT p_execution_id,'privacy-cases',p_subject_id,NULL,jsonb_build_object(
  'privacy_request.public_ref',request.public_ref::text,
  'privacy_request.received_at',to_char(request.received_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
  'privacy_request.decided_at',to_char(request.decided_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
  'privacy_request.decision_code',request.decision_code,
  'privacy_request.completed_at',NULL,
  'privacy_request.evidence_expires_at',NULL,
  'privacy_request.result_code','COMPLETED')
 FROM data_erasure_requests request WHERE request.id=request_ref
  AND 'privacy-cases'=ANY(retained_categories) ON CONFLICT DO NOTHING;

 -- Audit retention stores stable codes and times only; free-text audit payloads
 -- remain governed by the separate anonymisation checkpoint.
 INSERT INTO privacy_protected.execution_retention_sources(execution_id,category_key,subject_ref,anchor_at,retained_data)
 SELECT p_execution_id,'operational-logs',p_subject_id,max(row_at),jsonb_build_object(
  'audit.action',COALESCE(jsonb_agg(action_code ORDER BY row_at,row_id),'[]'::jsonb),
  'audit.occurred_at',COALESCE(jsonb_agg(to_char(row_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"') ORDER BY row_at,row_id),'[]'::jsonb),
  'audit.reason_code',COALESCE(jsonb_agg(reason_code ORDER BY row_at,row_id),'[]'::jsonb))
 FROM (
  SELECT id row_id,created_at row_at,'MINOR_CREDENTIAL'::text action_code,'ACCOUNT_SECURITY'::text reason_code FROM minor_credential_audit WHERE minor_user_id=p_subject_id OR guardian_user_id=p_subject_id OR actor_user_id=p_subject_id
  UNION ALL SELECT id,occurred_at,action::text,'EQUIPMENT' FROM equipment_audit_events WHERE actor_user_id=p_subject_id
  UNION ALL SELECT id,occurred_at,action::text,'MEMBER_PROFILE' FROM member_profile_audit_events WHERE actor_user_id=p_subject_id OR subject_user_id=p_subject_id
  UNION ALL SELECT id,occurred_at,action::text,'STAFF_GRANT' FROM staff_grant_audit_events WHERE actor_user_id=p_subject_id
  UNION ALL SELECT id,occurred_at,action::text,'PHOTO_ALBUM' FROM photo_album_audit_events WHERE actor_user_id=p_subject_id
  UNION ALL SELECT id,occurred_at,action::text,'ANNOUNCEMENT' FROM announcement_audit_events WHERE actor_user_id=p_subject_id
  UNION ALL SELECT id,occurred_at,'FEATURE_FLAG','FEATURE_FLAG' FROM feature_flag_events WHERE actor_user_id=p_subject_id
  UNION ALL SELECT id,copied_at,'TRAINING_COPY','TRAINING' FROM training_copy_events WHERE copied_by_id=p_subject_id
 ) retained_log WHERE 'operational-logs'=ANY(retained_categories)
 HAVING count(*)>0 ON CONFLICT DO NOTHING;
 RETURN p_execution_id;
END;$$;

CREATE FUNCTION public.privacy_worker_execute_retention_checkpoint(
 p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_epoch bigint,p_worker_ref uuid,p_operation_code text,p_action_version text
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE checkpoint_ref uuid;execution_ref uuid;subject_ref uuid;category text;entry jsonb;operation_index integer;
 fields text[];source privacy_protected.execution_retention_sources%ROWTYPE;anchor_at timestamptz;review_at timestamptz;expires_at timestamptz;
 changed bigint:=0;n bigint:=0;result_digest bytea;pending boolean;request_ref uuid;source_exists boolean;
BEGIN
 PERFORM public.privacy_worker_require_activation();
 SELECT checkpoint.id,execution.id,request.id,request.subject_user_id,job.category_key,
  plan.plan->'entries'->(job.plan_entry_position-1),checkpoint.operation_position
 INTO checkpoint_ref,execution_ref,request_ref,subject_ref,category,entry,operation_index
 FROM privacy_erasure_category_jobs job
 JOIN privacy_erasure_job_leases lease ON lease.id=p_lease_id AND lease.job_id=job.id AND lease.epoch=p_epoch
 JOIN privacy_erasure_job_attempts attempt ON attempt.id=p_attempt_id AND attempt.job_id=job.id AND attempt.lease_id=lease.id AND attempt.lease_epoch=p_epoch
 JOIN privacy_erasure_job_checkpoints checkpoint ON checkpoint.job_id=job.id AND checkpoint.operation_code=p_operation_code AND checkpoint.action_version=p_action_version
 JOIN privacy_erasure_executions execution ON execution.id=job.execution_id
 JOIN data_erasure_requests request ON request.id=execution.request_id
 JOIN privacy_request_execution_plans plan ON plan.request_id=request.id AND plan.plan_sha256=execution.plan_sha256
 WHERE job.id=p_job_id AND job.status='LEASED' AND lease.worker_ref=p_worker_ref AND lease.released_at IS NULL
  AND lease.expires_at>clock_timestamp() AND attempt.finished_at IS NULL
 FOR UPDATE OF job,lease,attempt,checkpoint;
 IF checkpoint_ref IS NULL THEN RETURN NULL; END IF;
 IF (SELECT status FROM privacy_erasure_job_checkpoints WHERE id=checkpoint_ref)='SUCCEEDED' THEN RETURN checkpoint_ref; END IF;
 IF subject_ref IS NULL OR entry IS NULL OR entry->>'category'<>category OR entry->>'fallback'<>'BLOCK'
  OR entry->>'action_version'<>p_action_version OR entry->'operations'->>(operation_index-1)<>p_operation_code
  OR entry->>'disposition' NOT IN ('RESTRICT','EXPIRE')
  OR EXISTS(SELECT 1 FROM privacy_erasure_job_checkpoints prior WHERE prior.job_id=p_job_id AND prior.operation_position<operation_index AND prior.status<>'SUCCEEDED')
  OR EXISTS(SELECT 1 FROM privacy_erasure_category_jobs prior WHERE prior.execution_id=execution_ref AND prior.plan_entry_position<(SELECT plan_entry_position FROM privacy_erasure_category_jobs WHERE id=p_job_id) AND prior.status<>'SUCCEEDED')
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_execution_order_or_plan_invalid'; END IF;
 IF (category,p_operation_code) NOT IN (
  ('sessions','AUTH_SESSION_EXPIRE'),('consent-evidence','CONSENT_EVIDENCE_RESTRICT'),('consent-network','CONSENT_NETWORK_EXPIRE'),
  ('identity-core','IDENTITY_RESTRICT'),('operational-logs','LOG_RECORD_EXPIRE'),('outbox-email','OUTBOX_PAYLOAD_EXPIRE'),
  ('privacy-cases','PRIVACY_CASE_RESTRICT'),('profile-core','PROFILE_RESTRICT'))
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_relational_operation_unsupported'; END IF;
 SELECT array_agg(value ORDER BY value) INTO fields FROM jsonb_array_elements_text(entry->'retained_fields');
 IF fields IS NULL OR entry->>'retention_anchor' IS NULL OR (entry->>'review_after')::integer<1
  OR (entry->>'expire_after')::integer<(entry->>'review_after')::integer
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_retention_contract_invalid'; END IF;
 PERFORM public.privacy_execution_capture_retention_sources(execution_ref,subject_ref);
 SELECT * INTO source FROM privacy_protected.execution_retention_sources
 WHERE execution_id=execution_ref AND category_key=category FOR SHARE;
 source_exists:=FOUND;
 IF NOT source_exists THEN
  source.execution_id:=execution_ref;source.category_key:=category;source.subject_ref:=subject_ref;source.retained_data:='{}'::jsonb;
 END IF;
 pending:=entry->>'retention_anchor'='CASE_CLOSURE';
 IF pending AND source_exists THEN
  INSERT INTO privacy_protected.execution_retention_pending(execution_id,category_key,subject_ref,anchor_code,retention_unit,review_after,expire_after,retained_field_codes,retained_data,worker_ref)
  VALUES(execution_ref,category,subject_ref,'CASE_CLOSURE',entry->>'retention_unit',(entry->>'review_after')::integer,(entry->>'expire_after')::integer,fields,
   public.privacy_retention_select_fields(source.retained_data,fields),p_worker_ref);
 ELSIF source_exists THEN
  anchor_at:=source.anchor_at;
  IF category='consent-evidence' THEN anchor_at:=(SELECT accepted_at FROM privacy_erasure_executions WHERE id=execution_ref); END IF;
  IF anchor_at IS NULL THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_retention_anchor_unresolved'; END IF;
  review_at:=public.privacy_retention_add_offset(anchor_at,(entry->>'review_after')::integer,entry->>'retention_unit');
  expires_at:=public.privacy_retention_add_offset(anchor_at,(entry->>'expire_after')::integer,entry->>'retention_unit');
  INSERT INTO privacy_erasure_retention_anchors(execution_id,category_key,anchor_code,anchor_at,review_at,expires_at,retained_field_codes,worker_ref)
  VALUES(execution_ref,category,entry->>'retention_anchor',anchor_at,review_at,expires_at,fields,p_worker_ref);
  INSERT INTO privacy_erasure_restricted_records(execution_id,category_key,subject_ref,retained_data,review_at,expires_at)
  VALUES(execution_ref,category,subject_ref,public.privacy_retention_select_fields(source.retained_data,fields),review_at,expires_at);
 END IF;

 CASE p_operation_code
 WHEN 'AUTH_SESSION_EXPIRE' THEN DELETE FROM sessions WHERE subject_indexed AND user_id=subject_ref; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'CONSENT_NETWORK_EXPIRE' THEN UPDATE consent_forms SET ip_address=NULL,user_agent='' WHERE user_id=subject_ref AND (ip_address IS NOT NULL OR user_agent<>''); GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'CONSENT_EVIDENCE_RESTRICT' THEN
  UPDATE member_profiles SET photo_object_key=NULL,photo_content_type=NULL,photo_size_bytes=NULL,photo_consent_form_id=NULL,photo_upload_intent_id=NULL,updated_at=clock_timestamp()
   WHERE user_id=subject_ref AND photo_consent_form_id IS NOT NULL;
  DELETE FROM consent_forms WHERE user_id=subject_ref; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'OUTBOX_PAYLOAD_EXPIRE' THEN
  DELETE FROM email_outbox outbox USING email_verification_tokens verification WHERE outbox.verification_token_id=verification.id AND verification.user_id=subject_ref;
  GET DIAGNOSTICS changed=ROW_COUNT;
  DELETE FROM email_outbox outbox USING password_reset_tokens reset WHERE outbox.password_reset_token_id=reset.id AND reset.user_id=subject_ref;
  GET DIAGNOSTICS n=ROW_COUNT;changed:=changed+n;
  DELETE FROM email_outbox WHERE privacy_requester_id=subject_ref AND privacy_request_id<>request_ref;
  GET DIAGNOSTICS n=ROW_COUNT;changed:=changed+n;
 WHEN 'PROFILE_RESTRICT' THEN DELETE FROM member_profiles WHERE user_id=subject_ref; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'IDENTITY_RESTRICT' THEN
  IF EXISTS(SELECT 1 FROM users WHERE guardian_id=subject_ref) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_dependants_unresolved'; END IF;
  DELETE FROM sessions WHERE subject_indexed AND user_id=subject_ref;DELETE FROM email_verification_tokens WHERE user_id=subject_ref;
  DELETE FROM password_reset_tokens WHERE user_id=subject_ref;DELETE FROM user_platform_roles WHERE user_id=subject_ref;
  UPDATE users SET name='Conta eliminada',email=NULL,email_verified_at=NULL,minor_login_id=NULL,password_hash=NULL,guardian_id=NULL,is_dependent=false,
   date_of_birth=DATE '1900-01-01',is_active=false,leaderboard_visible=false,credential_version=credential_version+1,erased_at=clock_timestamp(),
   erasure_execution_id=execution_ref,updated_at=clock_timestamp() WHERE id=subject_ref AND erased_at IS NULL; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'LOG_RECORD_EXPIRE','PRIVACY_CASE_RESTRICT' THEN changed:=0;
 END CASE;
 IF p_operation_code='AUTH_SESSION_EXPIRE' AND EXISTS(SELECT 1 FROM sessions WHERE subject_indexed AND user_id=subject_ref)
  OR p_operation_code='CONSENT_NETWORK_EXPIRE' AND EXISTS(SELECT 1 FROM consent_forms WHERE user_id=subject_ref AND (ip_address IS NOT NULL OR user_agent<>''))
  OR p_operation_code='CONSENT_EVIDENCE_RESTRICT' AND EXISTS(SELECT 1 FROM consent_forms WHERE user_id=subject_ref)
  OR p_operation_code='PROFILE_RESTRICT' AND EXISTS(SELECT 1 FROM member_profiles WHERE user_id=subject_ref)
  OR p_operation_code='IDENTITY_RESTRICT' AND NOT EXISTS(SELECT 1 FROM users WHERE id=subject_ref AND erased_at IS NOT NULL AND NOT is_active AND email IS NULL AND minor_login_id IS NULL AND password_hash IS NULL)
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_relational_verification_failed'; END IF;
 result_digest:=digest(convert_to(p_operation_code||':'||subject_ref::text||':'||changed::text||':'||encode(digest(convert_to(public.privacy_retention_select_fields(source.retained_data,fields)::text,'UTF8'),'sha256'),'hex'),'UTF8'),'sha256');
 PERFORM set_config('mycfc.privacy_retention_mutation','CONSUME',true);
 DELETE FROM privacy_protected.execution_retention_sources WHERE execution_id=execution_ref AND category_key=category;
 UPDATE privacy_erasure_job_checkpoints SET status='SUCCEEDED',completed_at=clock_timestamp(),completed_by_attempt_id=p_attempt_id,
  affected_rows=changed,result_sha256=result_digest WHERE id=checkpoint_ref AND status='PENDING';
 RETURN checkpoint_ref;
END;$$;

ALTER FUNCTION public.privacy_worker_execute_checkpoint(uuid,uuid,uuid,bigint,uuid,text,text)
 RENAME TO privacy_worker_execute_checkpoint_inner_017;
CREATE FUNCTION public.privacy_worker_execute_checkpoint(
 p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_lease_epoch bigint,p_worker_ref uuid,p_operation_code text,p_action_version text
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
 PERFORM public.privacy_worker_require_activation();
 IF p_operation_code IN ('AUTH_SESSION_EXPIRE','CONSENT_EVIDENCE_RESTRICT','CONSENT_NETWORK_EXPIRE','IDENTITY_RESTRICT',
  'LOG_RECORD_EXPIRE','OUTBOX_PAYLOAD_EXPIRE','PRIVACY_CASE_RESTRICT','PROFILE_RESTRICT') THEN
  RETURN public.privacy_worker_execute_retention_checkpoint(p_job_id,p_lease_id,p_attempt_id,p_lease_epoch,p_worker_ref,p_operation_code,p_action_version);
 END IF;
 RETURN public.privacy_worker_execute_checkpoint_inner_017(p_job_id,p_lease_id,p_attempt_id,p_lease_epoch,p_worker_ref,p_operation_code,p_action_version);
END;$$;

CREATE FUNCTION public.privacy_completion_resolve_retention(p_execution_id uuid,p_worker_ref uuid)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE row_record record;closure_at timestamptz;review_at timestamptz;expires_at timestamptz;resolved_data jsonb;
BEGIN
 SELECT closed_at INTO closure_at FROM privacy_protected.restore_tombstone_closure_intents WHERE execution_id=p_execution_id FOR SHARE;
 IF closure_at IS NULL THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_retention_closure_unresolved'; END IF;
 FOR row_record IN SELECT * FROM privacy_protected.execution_retention_pending WHERE execution_id=p_execution_id ORDER BY category_key FOR SHARE LOOP
  review_at:=public.privacy_retention_add_offset(closure_at,row_record.review_after,row_record.retention_unit);
  expires_at:=public.privacy_retention_add_offset(closure_at,row_record.expire_after,row_record.retention_unit);
  resolved_data:=row_record.retained_data;
  IF row_record.category_key='privacy-cases' THEN
   resolved_data:=jsonb_set(jsonb_set(jsonb_set(resolved_data,'{"privacy_request.completed_at"}',to_jsonb(to_char(closure_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'))),
    '{"privacy_request.evidence_expires_at"}',to_jsonb(to_char(expires_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'))),
    '{"privacy_request.result_code"}',to_jsonb('COMPLETED'::text));
  END IF;
  INSERT INTO privacy_erasure_retention_anchors(execution_id,category_key,anchor_code,anchor_at,review_at,expires_at,retained_field_codes,worker_ref)
  VALUES(p_execution_id,row_record.category_key,'CASE_CLOSURE',closure_at,review_at,expires_at,row_record.retained_field_codes,p_worker_ref);
  INSERT INTO privacy_erasure_restricted_records(execution_id,category_key,subject_ref,retained_data,review_at,expires_at)
  VALUES(p_execution_id,row_record.category_key,row_record.subject_ref,public.privacy_retention_select_fields(resolved_data,row_record.retained_field_codes),review_at,expires_at);
 END LOOP;
 PERFORM set_config('mycfc.privacy_retention_mutation','RESOLVE',true);
 DELETE FROM privacy_protected.execution_retention_pending WHERE execution_id=p_execution_id;
END;$$;

ALTER FUNCTION public.privacy_completion_finalize(uuid,uuid,bytea,bytea) RENAME TO privacy_completion_finalize_inner_017;
CREATE FUNCTION public.privacy_completion_finalize(p_execution_id uuid,p_worker_ref uuid,p_token_sha256 bytea,p_sealed_delivery bytea)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
 PERFORM public.privacy_worker_require_activation();
 PERFORM public.privacy_completion_resolve_retention(p_execution_id,p_worker_ref);
 IF EXISTS(SELECT 1 FROM privacy_protected.execution_retention_pending WHERE execution_id=p_execution_id) THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_retention_closure_unresolved'; END IF;
 RETURN public.privacy_completion_finalize_inner_017(p_execution_id,p_worker_ref,p_token_sha256,p_sealed_delivery);
END;$$;

CREATE OR REPLACE FUNCTION public.prevent_privacy_retention_record_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' AND current_user<>session_user AND current_setting('mycfc.privacy_retention_mutation',true) IN ('CONSUME','EXPIRE','RESOLVE') THEN RETURN OLD; END IF;
 RAISE EXCEPTION 'privacy execution records are immutable';
END;$$;
DROP TRIGGER privacy_erasure_retention_anchors_immutable ON privacy_erasure_retention_anchors;
CREATE TRIGGER privacy_erasure_retention_anchors_immutable BEFORE UPDATE OR DELETE ON privacy_erasure_retention_anchors
 FOR EACH ROW EXECUTE FUNCTION public.prevent_privacy_retention_record_mutation();
DROP TRIGGER privacy_erasure_restricted_records_immutable ON privacy_erasure_restricted_records;
CREATE TRIGGER privacy_erasure_restricted_records_immutable BEFORE UPDATE OR DELETE ON privacy_erasure_restricted_records
 FOR EACH ROW EXECUTE FUNCTION public.prevent_privacy_retention_record_mutation();
DROP TRIGGER execution_retention_pending_immutable ON privacy_protected.execution_retention_pending;
CREATE TRIGGER execution_retention_pending_immutable BEFORE UPDATE OR DELETE ON privacy_protected.execution_retention_pending
 FOR EACH ROW EXECUTE FUNCTION public.prevent_privacy_retention_record_mutation();
DROP TRIGGER execution_retention_sources_immutable ON privacy_protected.execution_retention_sources;
CREATE TRIGGER execution_retention_sources_immutable BEFORE UPDATE OR DELETE ON privacy_protected.execution_retention_sources
 FOR EACH ROW EXECUTE FUNCTION public.prevent_privacy_retention_record_mutation();

ALTER FUNCTION public.privacy_retention_run(uuid,integer) RENAME TO privacy_retention_run_inner_017;
CREATE FUNCTION public.privacy_retention_run(p_worker_ref uuid,p_batch_limit integer)
RETURNS TABLE(run_id uuid,sessions_deleted integer,tokens_deleted integer,outbox_stopped integer,outbox_payloads_deleted integer,
 outbox_evidence_deleted integer,consent_network_scrubbed integer,consent_evidence_deleted integer,audit_events_pseudonymized integer,
 repair_attachments_queued integer,event_responses_deleted integer,announcement_deliveries_deleted integer,suggestions_deleted integer,
 privacy_working_scrubbed integer,auth_limits_deleted integer)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
 RETURN QUERY SELECT * FROM public.privacy_retention_run_inner_017(p_worker_ref,p_batch_limit);
 PERFORM set_config('mycfc.privacy_retention_mutation','EXPIRE',true);
 WITH due AS (SELECT execution_id,category_key FROM privacy_erasure_restricted_records WHERE expires_at<=clock_timestamp() ORDER BY expires_at,execution_id,category_key LIMIT p_batch_limit)
 DELETE FROM privacy_erasure_restricted_records retained USING due WHERE retained.execution_id=due.execution_id AND retained.category_key=due.category_key;
 WITH due AS (SELECT execution_id,category_key FROM privacy_erasure_retention_anchors WHERE expires_at<=clock_timestamp() AND NOT EXISTS(
  SELECT 1 FROM privacy_erasure_restricted_records retained WHERE retained.execution_id=privacy_erasure_retention_anchors.execution_id AND retained.category_key=privacy_erasure_retention_anchors.category_key)
  ORDER BY expires_at,execution_id,category_key LIMIT p_batch_limit)
 DELETE FROM privacy_erasure_retention_anchors anchor USING due WHERE anchor.execution_id=due.execution_id AND anchor.category_key=due.category_key;
END;$$;

REVOKE ALL ON TABLE privacy_protected.execution_retention_sources,privacy_protected.execution_retention_pending FROM PUBLIC;
REVOKE ALL ON FUNCTION public.privacy_retention_add_offset(timestamptz,integer,text),public.privacy_retention_select_fields(jsonb,text[]),
 public.privacy_execution_capture_retention_sources(uuid,uuid),public.privacy_worker_execute_retention_checkpoint(uuid,uuid,uuid,bigint,uuid,text,text),
 public.privacy_worker_execute_checkpoint_inner_017(uuid,uuid,uuid,bigint,uuid,text,text),public.privacy_completion_resolve_retention(uuid,uuid),
 public.privacy_completion_finalize_inner_017(uuid,uuid,bytea,bytea),public.privacy_retention_run_inner_017(uuid,integer),
 public.prevent_privacy_retention_record_mutation() FROM PUBLIC;
REVOKE ALL ON FUNCTION public.privacy_worker_execute_checkpoint(uuid,uuid,uuid,bigint,uuid,text,text),
 public.privacy_completion_finalize(uuid,uuid,bytea,bytea),public.privacy_retention_run(uuid,integer) FROM PUBLIC;
DO $$BEGIN
 IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='mycfc_privacy_retention') THEN
  REVOKE EXECUTE ON FUNCTION public.privacy_retention_run_inner_017(uuid,integer),public.privacy_retention_add_offset(timestamptz,integer,text),
   public.privacy_retention_select_fields(jsonb,text[]),public.prevent_privacy_retention_record_mutation() FROM mycfc_privacy_retention;
 END IF;
END$$;

DO $$DECLARE definition text;rewritten text;
BEGIN
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint
 WHERE conrelid='privacy_activation_authenticated_artifacts'::regclass
  AND conname='privacy_activation_authenticated_artifacts_v17_check';
 IF definition IS NULL OR length(definition)-length(replace(definition,'202609170001_privacy_synthetic_acceptance',''))<>length('202609170001_privacy_synthetic_acceptance') THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='executor_retention_privacy_constraint_predecessor_mismatch';
 END IF;
 rewritten:=replace(definition,
  '''202609170001_privacy_synthetic_acceptance''',
  '''202609170001_privacy_synthetic_acceptance'', ''202609170002_privacy_executor_retention_handlers''');
 ALTER TABLE privacy_activation_authenticated_artifacts DROP CONSTRAINT privacy_activation_authenticated_artifacts_v17_check;
 EXECUTE 'ALTER TABLE privacy_activation_authenticated_artifacts ADD CONSTRAINT privacy_activation_authenticated_artifacts_v18_check '||rewritten||' NOT VALID';
 ALTER TABLE privacy_activation_authenticated_artifacts VALIDATE CONSTRAINT privacy_activation_authenticated_artifacts_v18_check;
END$$;

DO $$DECLARE definition text;old_clause text;new_clause text;
BEGIN
 SELECT pg_get_functiondef('public.privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)'::regprocedure) INTO definition;
 old_clause:='p_artifact->>''baseline_includes_through''<>''202609170001_privacy_synthetic_acceptance''';
 new_clause:='p_artifact->>''baseline_includes_through''<>''202609170002_privacy_executor_retention_handlers''';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='executor_retention_privacy_evidence_predecessor_mismatch'; END IF;
 EXECUTE replace(definition,old_clause,new_clause);

 SELECT pg_get_functiondef('public.privacy_activation_authenticated_set_digest(text,uuid[])'::regprocedure) INTO definition;
 old_clause:='schema_row.baseline_includes_through=''202609170001_privacy_synthetic_acceptance''';
 new_clause:='schema_row.baseline_includes_through=''202609170002_privacy_executor_retention_handlers''';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='executor_retention_privacy_digest_predecessor_mismatch'; END IF;
 EXECUTE replace(definition,old_clause,new_clause);
END$$;

-- Every schema revision invalidates privacy evidence and both guardian gates.
UPDATE privacy_request_activation SET enabled=false,fulfilment_ready=false,approval_id=NULL,updated_at=clock_timestamp() WHERE singleton;
UPDATE privacy_worker_kill_switch SET engaged=true,version=version+1,activation_approval_id=NULL,changed_at=clock_timestamp() WHERE singleton;
INSERT INTO privacy_worker_kill_switch_events(version,engaged,occurred_at) SELECT version,true,changed_at FROM privacy_worker_kill_switch WHERE singleton;
UPDATE guardian_application_intake_release SET enabled=false,policy_version=NULL,policy_sha256=NULL,approval_sha256=NULL,image_digest=NULL,schema_migration_digest=NULL,enabled_by=NULL,enabled_at=NULL WHERE singleton;
WITH disabled AS (UPDATE guardian_authority_policies SET enabled=false,enabled_at=NULL,enabled_by=NULL WHERE enabled RETURNING id,version)
INSERT INTO guardian_authority_policy_events(policy_id,policy_version,actor_ref,action) SELECT id,version,NULL,'MIGRATION_DISABLED' FROM disabled;
SELECT guardian_authority_reconcile_cutoffs();
DELETE FROM sessions session USING users subject WHERE session.user_id=subject.id AND session.subject_indexed AND subject.is_dependent;
UPDATE users SET minor_login_id=NULL,password_hash=NULL,credential_version=credential_version+1,updated_at=clock_timestamp()
 WHERE is_dependent AND (minor_login_id IS NOT NULL OR password_hash IS NOT NULL);
