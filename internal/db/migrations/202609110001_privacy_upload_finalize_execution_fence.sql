-- #246 closes the reservation-to-finalisation race with execution capture.
-- Reacquiring the subject lock before an upload becomes PUT-eligible ensures
-- that either the prepared intent is visible to capture or the committed
-- capture set rejects finalisation before any object bytes are stored.

CREATE OR REPLACE FUNCTION public.privacy_execution_capture_media_sources(
 p_execution_id uuid,p_subject_user_id uuid,p_category_key text
) RETURNS TABLE(job_id uuid,checkpoint_id uuid,plan_entry_sha256 bytea,category_key text,
 source_kind text,source_ref uuid,upload_intent_id uuid,object_key text)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE v_job uuid; v_checkpoint uuid; v_entry bytea; v_expected integer; v_kind text; v_ref uuid;
BEGIN
 PERFORM public.privacy_media_subject_lock(p_subject_user_id);
 SELECT job.id,checkpoint.id,job.entry_sha256 INTO v_job,v_checkpoint,v_entry
 FROM public.privacy_erasure_executions execution
 JOIN public.data_erasure_requests request ON request.id=execution.request_id
 JOIN public.privacy_erasure_category_jobs job ON job.execution_id=execution.id AND job.category_key=p_category_key
 JOIN public.privacy_erasure_job_checkpoints checkpoint ON checkpoint.job_id=job.id
  AND checkpoint.operation_code='OBJECT_VERSION_DELETE' AND checkpoint.action_version='v1'
 WHERE execution.id=p_execution_id AND execution.executor_version='privacy-erasure-executor/v2'
  AND execution.schema_version='privacy-erasure-plan/v2' AND request.subject_user_id=p_subject_user_id;
 IF v_checkpoint IS NULL OR p_category_key NOT IN ('profile-photo','object-storage') THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_object_capture_invalid';
 END IF;

 -- Existing lifecycle routines lock the intent row before the source advisory
 -- lock. Freeze every relevant intent in the same order before taking source
 -- locks so capture cannot deadlock with removal, replacement, or cleanup.
 PERFORM i.id FROM privacy_protected.object_upload_intents i
  WHERE (i.subject_user_id=p_subject_user_id OR (i.source_kind='EQUIPMENT_PHOTO' AND i.provenance_actor_user_id=p_subject_user_id))
   AND ((p_category_key='profile-photo' AND i.source_kind='MEMBER_PROFILE_PHOTO')
     OR (p_category_key='object-storage' AND i.source_kind IN ('REPAIR_ATTACHMENT','EQUIPMENT_PHOTO')))
  ORDER BY i.id FOR SHARE;

 -- Finalisation cannot commit while the subject lock is held. Source locks now
 -- fence pointer mutation for the stable set of intent rows captured above.
 FOR v_kind,v_ref IN
  SELECT i.source_kind,i.source_ref FROM privacy_protected.object_upload_intents i
  WHERE (i.subject_user_id=p_subject_user_id OR (i.source_kind='EQUIPMENT_PHOTO' AND i.provenance_actor_user_id=p_subject_user_id))
   AND ((p_category_key='profile-photo' AND i.source_kind='MEMBER_PROFILE_PHOTO')
     OR (p_category_key='object-storage' AND i.source_kind IN ('REPAIR_ATTACHMENT','EQUIPMENT_PHOTO')))
  ORDER BY i.source_kind,i.source_ref
 LOOP PERFORM public.privacy_upload_source_lock(v_kind,v_ref); END LOOP;

 IF p_category_key='profile-photo' THEN
  IF EXISTS(SELECT 1 FROM public.member_profiles p WHERE p.user_id=p_subject_user_id AND p.photo_object_key IS NOT NULL AND p.photo_upload_intent_id IS NULL) THEN
   RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_legacy_media_unresolved';
  END IF;
 ELSE
  IF EXISTS(SELECT 1 FROM public.repair_requests r WHERE r.reported_by_id=p_subject_user_id AND r.image_object_key IS NOT NULL AND r.image_upload_intent_id IS NULL)
    OR EXISTS(SELECT 1 FROM public.equipment e WHERE e.image_object_key IS NOT NULL AND e.image_upload_intent_id IS NULL) THEN
   RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_legacy_media_unresolved';
  END IF;
 END IF;

 IF EXISTS(
  SELECT 1 FROM privacy_protected.object_upload_intents i
  JOIN LATERAL (SELECT e.status FROM privacy_protected.object_upload_intent_events e WHERE e.intent_id=i.id ORDER BY e.sequence DESC LIMIT 1) latest ON true
  WHERE (i.subject_user_id=p_subject_user_id OR (i.source_kind='EQUIPMENT_PHOTO' AND i.provenance_actor_user_id=p_subject_user_id))
    AND ((p_category_key='profile-photo' AND i.source_kind='MEMBER_PROFILE_PHOTO')
      OR (p_category_key='object-storage' AND i.source_kind IN ('REPAIR_ATTACHMENT','EQUIPMENT_PHOTO')))
    AND latest.status NOT IN ('ATTACHED','ABSENCE_VERIFIED')
 ) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_media_upload_in_flight'; END IF;

 SELECT count(*)::integer INTO v_expected FROM (
  SELECT p.photo_upload_intent_id FROM public.member_profiles p
   JOIN privacy_protected.object_upload_intents i ON i.id=p.photo_upload_intent_id
   WHERE p_category_key='profile-photo' AND p.user_id=p_subject_user_id AND p.photo_object_key IS NOT NULL
  UNION ALL
  SELECT r.image_upload_intent_id FROM public.repair_requests r
   JOIN privacy_protected.object_upload_intents i ON i.id=r.image_upload_intent_id
   WHERE p_category_key='object-storage' AND i.subject_user_id=p_subject_user_id AND r.image_object_key IS NOT NULL
  UNION ALL
  SELECT e.image_upload_intent_id FROM public.equipment e
   JOIN privacy_protected.object_upload_intents i ON i.id=e.image_upload_intent_id
   WHERE p_category_key='object-storage' AND i.provenance_actor_user_id=p_subject_user_id AND e.image_object_key IS NOT NULL
 ) candidates;

 INSERT INTO privacy_protected.object_capture_sets(execution_id,job_id,checkpoint_id,subject_user_id,category_key,source_kinds,
  expected_target_count,operation_code,action_version,created_at)
 VALUES(p_execution_id,v_job,v_checkpoint,p_subject_user_id,p_category_key,
  CASE WHEN p_category_key='profile-photo' THEN ARRAY['MEMBER_PROFILE_PHOTO']::text[] ELSE ARRAY['EQUIPMENT_PHOTO','REPAIR_ATTACHMENT']::text[] END,
  v_expected,'OBJECT_VERSION_DELETE','v1',clock_timestamp());

 RETURN QUERY
  SELECT v_job,v_checkpoint,v_entry,p_category_key::text,'MEMBER_PROFILE_PHOTO'::text,p.user_id,p.photo_upload_intent_id,p.photo_object_key::text
  FROM public.member_profiles p JOIN privacy_protected.object_upload_intents i ON i.id=p.photo_upload_intent_id
  WHERE p_category_key='profile-photo' AND p.user_id=p_subject_user_id AND p.photo_object_key IS NOT NULL
    AND public.privacy_upload_pointer_matches(i.id,i.source_kind,i.source_ref,i.locator_commitment,i.content_type,i.size_bytes)
    AND (SELECT status FROM privacy_protected.object_upload_intent_events x WHERE x.intent_id=i.id ORDER BY sequence DESC LIMIT 1)='ATTACHED'
  UNION ALL
  SELECT v_job,v_checkpoint,v_entry,p_category_key::text,'REPAIR_ATTACHMENT'::text,r.id,r.image_upload_intent_id,r.image_object_key::text
  FROM public.repair_requests r JOIN privacy_protected.object_upload_intents i ON i.id=r.image_upload_intent_id
  WHERE p_category_key='object-storage' AND i.subject_user_id=p_subject_user_id AND r.image_object_key IS NOT NULL
    AND public.privacy_upload_pointer_matches(i.id,i.source_kind,i.source_ref,i.locator_commitment,i.content_type,i.size_bytes)
    AND (SELECT status FROM privacy_protected.object_upload_intent_events x WHERE x.intent_id=i.id ORDER BY sequence DESC LIMIT 1)='ATTACHED'
  UNION ALL
  SELECT v_job,v_checkpoint,v_entry,p_category_key::text,'EQUIPMENT_PHOTO'::text,e.id,e.image_upload_intent_id,e.image_object_key::text
  FROM public.equipment e JOIN privacy_protected.object_upload_intents i ON i.id=e.image_upload_intent_id
  WHERE p_category_key='object-storage' AND i.provenance_actor_user_id=p_subject_user_id AND e.image_object_key IS NOT NULL
    AND public.privacy_upload_pointer_matches(i.id,i.source_kind,i.source_ref,i.locator_commitment,i.content_type,i.size_bytes)
    AND (SELECT status FROM privacy_protected.object_upload_intent_events x WHERE x.intent_id=i.id ORDER BY sequence DESC LIMIT 1)='ATTACHED'
  ORDER BY 5,6;
END; $$;

REVOKE ALL ON FUNCTION public.privacy_execution_capture_media_sources(uuid,uuid,text) FROM PUBLIC;

CREATE OR REPLACE FUNCTION public.privacy_upload_finalize(
 p_intent_id uuid,p_token bytea,p_envelope_version text,p_algorithm text,p_encryption_key_id text,
 p_encapsulation bytea,p_nonce bytea,p_ciphertext bytea,p_digest_key_id text,p_locator_digest bytea,p_locator_commitment bytea
) RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE r privacy_protected.object_upload_intent_reservations%ROWTYPE; h privacy_protected.object_upload_intent_holds%ROWTYPE; v_privacy_subject uuid;
BEGIN
 SELECT * INTO r FROM privacy_protected.object_upload_intent_reservations WHERE id=p_intent_id FOR UPDATE;
 SELECT * INTO h FROM privacy_protected.object_upload_intent_holds WHERE intent_id=p_intent_id FOR UPDATE;
 IF r.id IS NULL OR h.intent_id IS NULL OR r.finalized_at IS NOT NULL OR h.released_at IS NOT NULL OR h.held_until<=clock_timestamp()
    OR p_token IS NULL OR octet_length(p_token)<>32 OR h.token_digest IS DISTINCT FROM digest(p_token,'sha256') OR r.token_digest<>h.token_digest
    OR p_locator_commitment IS NULL OR octet_length(p_locator_commitment)<>32 THEN RAISE EXCEPTION 'upload reservation is unavailable'; END IF;
 v_privacy_subject:=COALESCE(r.subject_user_id,r.provenance_actor_user_id);
 PERFORM public.privacy_media_subject_lock(v_privacy_subject);
 IF EXISTS(SELECT 1 FROM privacy_protected.object_capture_sets capture
  JOIN public.privacy_erasure_executions execution ON execution.id=capture.execution_id
  JOIN public.data_erasure_requests request ON request.id=execution.request_id
  WHERE capture.subject_user_id=v_privacy_subject AND r.source_kind=ANY(capture.source_kinds)
   AND request.status IN ('PROCESSING','RETRYABLE_FAILED','TERMINAL_FAILED')) THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_media_execution_in_progress';
 END IF;
 INSERT INTO privacy_protected.object_upload_intents(id,subject_user_id,provenance_actor_user_id,source_kind,source_ref,service_code,target_kind,provider_contract_version,
  envelope_version,algorithm,encryption_key_id,encapsulation,nonce,ciphertext,digest_key_id,locator_digest,locator_commitment,content_type,size_bytes,cleanup_after,created_at)
 VALUES(r.id,r.subject_user_id,r.provenance_actor_user_id,r.source_kind,r.source_ref,r.service_code,'OBJECT_KEY','s3-versioned/v1',
  p_envelope_version,p_algorithm,p_encryption_key_id,p_encapsulation,p_nonce,p_ciphertext,p_digest_key_id,p_locator_digest,p_locator_commitment,r.content_type,r.size_bytes,r.cleanup_after,r.created_at);
 INSERT INTO privacy_protected.object_upload_intent_events(intent_id,sequence,status,reason_code,occurred_at) VALUES(r.id,1,'PREPARED','UPLOAD_RESERVED',clock_timestamp());
 UPDATE privacy_protected.object_upload_intent_reservations SET finalized_at=clock_timestamp() WHERE id=r.id;
END; $$;

REVOKE ALL ON FUNCTION public.privacy_upload_finalize(uuid,bytea,text,text,text,bytea,bytea,bytea,text,bytea,bytea) FROM PUBLIC;

ALTER TABLE privacy_activation_authenticated_artifacts DROP CONSTRAINT privacy_activation_authenticated_artifacts_v4_check;
ALTER TABLE privacy_activation_authenticated_artifacts ADD CONSTRAINT privacy_activation_authenticated_artifacts_v4_check CHECK(
 ((kind='RESTORE' AND signing_key_id IS NULL AND immutable_evidence_ref IS NOT NULL AND schema_migration_digest IS NOT NULL
   AND baseline_includes_through IS NULL AND restore_input_source IN('LIVE_LEDGER','SYNTHETIC_BOOTSTRAP')
   AND restore_input_contract='mycfc/privacy-restore-ledger-input/v2' AND restore_replay_contract='relational-erasure-replay/v1'
   AND restore_closure_contract IN('restore-tombstone-closure/v3','restore-tombstone-closure/v4')
   AND octet_length(restore_candidate_sha256)=32 AND octet_length(restore_inventory_sha256)=32 AND octet_length(restore_observer_sha256)=32
   AND restore_object_count>=restore_replayed_count AND restore_replayed_count>0
   AND ((restore_input_source='LIVE_LEDGER' AND restore_synthetic_count=0) OR (restore_input_source='SYNTHETIC_BOOTSTRAP' AND restore_synthetic_count=restore_replayed_count))
   AND ((restore_closure_contract='restore-tombstone-closure/v3' AND restore_membership_postcondition_contract IS NULL
     AND restore_membership_postcondition_sha256 IS NULL AND restore_membership_postcondition_verified_count IS NULL
     AND restore_membership_count IS NULL AND restore_variation_count IS NULL)
    OR (restore_closure_contract='restore-tombstone-closure/v4'
     AND restore_membership_postcondition_contract='mycfc/membership-history-postcondition/v1'
     AND octet_length(restore_membership_postcondition_sha256)=32
     AND restore_membership_postcondition_verified_count=restore_replayed_count
     AND restore_membership_count>=0 AND restore_variation_count>=0)))
 OR (kind='INFRASTRUCTURE' AND signing_key_id IS NOT NULL AND immutable_evidence_ref IS NOT NULL
   AND production_state_serial>0 AND hetzner_state_serial>0 AND octet_length(production_state_sha256)=32 AND octet_length(hetzner_state_sha256)=32
   AND octet_length(production_plan_sha256)=32 AND octet_length(hetzner_plan_sha256)=32 AND worker_identity_enabled AND s3_version_deletion_enabled
   AND ledger_broker_invoke_enabled AND worker_monitoring_enabled AND restore_infrastructure_enabled AND restore_ledger_write_enabled)
 OR (kind='PROVIDER' AND signing_key_id IS NOT NULL AND immutable_evidence_ref IS NOT NULL AND provider_registry_state='READY'
   AND provider_registration_count>0 AND octet_length(provider_registry_sha256)=32)
 OR (kind='SCHEMA' AND signing_key_id IS NOT NULL AND immutable_evidence_ref IS NOT NULL AND octet_length(schema_migration_digest)=32
   AND baseline_includes_through IN('202609100014_privacy_activation_broker','202609100015_privacy_membership_postcondition','202609110001_privacy_upload_finalize_execution_fence'))) IS TRUE) NOT VALID;
ALTER TABLE privacy_activation_authenticated_artifacts VALIDATE CONSTRAINT privacy_activation_authenticated_artifacts_v4_check;

DO $$DECLARE definition text;old_clause text;new_clause text;
BEGIN
 SELECT pg_get_functiondef('public.privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)'::regprocedure) INTO definition;
 old_clause:='p_artifact->>''baseline_includes_through''<>''202609100015_privacy_membership_postcondition''';
 new_clause:='p_artifact->>''baseline_includes_through''<>''202609110001_privacy_upload_finalize_execution_fence''';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_activation_evidence_predecessor_mismatch'; END IF;
 EXECUTE replace(definition,old_clause,new_clause);

 SELECT pg_get_functiondef('public.privacy_activation_authenticated_set_digest(text,uuid[])'::regprocedure) INTO definition;
 old_clause:='schema_row.baseline_includes_through=''202609100015_privacy_membership_postcondition''';
 new_clause:='schema_row.baseline_includes_through=''202609110001_privacy_upload_finalize_execution_fence''';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_activation_digest_predecessor_mismatch'; END IF;
 EXECUTE replace(definition,old_clause,new_clause);
END$$;

-- A schema upgrade invalidates all release-specific activation evidence.
UPDATE privacy_request_activation SET enabled=false,fulfilment_ready=false,approval_id=NULL,updated_at=clock_timestamp() WHERE singleton;
UPDATE privacy_worker_kill_switch SET engaged=true,version=version+1,activation_approval_id=NULL,changed_at=clock_timestamp() WHERE singleton;
INSERT INTO privacy_worker_kill_switch_events(version,engaged,occurred_at)
 SELECT version,true,changed_at FROM privacy_worker_kill_switch WHERE singleton;
