-- Connect protected media targets to the v2 execution handoff. Historical v1
-- plans remain immutable and cannot use these routines.
ALTER TABLE privacy_protected.object_targets
 ADD COLUMN upload_intent_id uuid NULL REFERENCES privacy_protected.object_upload_intents(id) ON DELETE RESTRICT;
ALTER TABLE privacy_protected.object_targets
 ADD CONSTRAINT privacy_object_targets_capture_source_unique UNIQUE(execution_id,source_kind,source_ref,upload_intent_id);

CREATE TABLE privacy_protected.object_capture_sets (
 execution_id uuid NOT NULL REFERENCES privacy_erasure_executions(id) ON DELETE RESTRICT,
 job_id uuid NOT NULL REFERENCES privacy_erasure_category_jobs(id) ON DELETE RESTRICT, checkpoint_id uuid NOT NULL,
 subject_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 category_key varchar(120) NOT NULL,
 source_kinds text[] NOT NULL,
 expected_target_count integer NOT NULL CHECK(expected_target_count>=0),
 created_at timestamptz NOT NULL,
 PRIMARY KEY(checkpoint_id), UNIQUE(execution_id,category_key),
 FOREIGN KEY(checkpoint_id,job_id,operation_code,action_version)
  REFERENCES privacy_erasure_job_checkpoints(id,job_id,operation_code,action_version) ON DELETE RESTRICT,
 operation_code varchar(120) NOT NULL CHECK(operation_code='OBJECT_VERSION_DELETE'),
 action_version varchar(40) NOT NULL CHECK(action_version='v1'),
 CHECK(category_key IN ('profile-photo','object-storage')),
 CHECK(cardinality(source_kinds) BETWEEN 1 AND 2 AND array_position(source_kinds,NULL) IS NULL),
 CHECK((category_key='profile-photo' AND source_kinds=ARRAY['MEMBER_PROFILE_PHOTO']::text[])
    OR (category_key='object-storage' AND source_kinds=ARRAY['EQUIPMENT_PHOTO','REPAIR_ATTACHMENT']::text[]))
);

CREATE TRIGGER privacy_object_capture_sets_immutable BEFORE UPDATE OR DELETE ON privacy_protected.object_capture_sets
 FOR EACH ROW EXECUTE FUNCTION prevent_privacy_execution_record_delete();

CREATE OR REPLACE FUNCTION public.privacy_media_subject_lock(p_subject_user_id uuid) RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT pg_advisory_xact_lock(hashtextextended('mycfc/media-subject/v1:'||p_subject_user_id::text,0));
$$;

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

 -- Any unfinished upload for this subject can still own a version that is not
 -- represented by a pointer. Start remains blocked until it attaches or its
 -- cleanup records stable absence.
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

 FOR v_kind,v_ref IN
  SELECT i.source_kind,i.source_ref FROM privacy_protected.object_upload_intents i
  WHERE (i.subject_user_id=p_subject_user_id OR (i.source_kind='EQUIPMENT_PHOTO' AND i.provenance_actor_user_id=p_subject_user_id))
   AND ((p_category_key='profile-photo' AND i.source_kind='MEMBER_PROFILE_PHOTO')
     OR (p_category_key='object-storage' AND i.source_kind IN ('REPAIR_ATTACHMENT','EQUIPMENT_PHOTO')))
  ORDER BY i.source_kind,i.source_ref
 LOOP PERFORM public.privacy_upload_source_lock(v_kind,v_ref); END LOOP;

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

CREATE FUNCTION public.privacy_execution_materialize_object_target(
 p_target_id uuid,p_execution_id uuid,p_job_id uuid,p_checkpoint_id uuid,p_plan_entry_sha256 bytea,p_category_key text,
 p_source_kind text,p_source_ref uuid,p_upload_intent_id uuid,p_object_key text,
 p_envelope_version text,p_algorithm text,p_encryption_key_id text,p_encapsulation bytea,p_nonce bytea,p_ciphertext bytea,
 p_digest_key_id text,p_locator_digest bytea
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE c privacy_protected.object_capture_sets%ROWTYPE; i privacy_protected.object_upload_intents%ROWTYPE;
BEGIN
 SELECT * INTO c FROM privacy_protected.object_capture_sets WHERE checkpoint_id=p_checkpoint_id AND execution_id=p_execution_id FOR SHARE;
 SELECT * INTO i FROM privacy_protected.object_upload_intents WHERE id=p_upload_intent_id FOR SHARE;
 IF c.checkpoint_id IS NULL OR c.job_id<>p_job_id OR c.category_key<>p_category_key OR c.expected_target_count<=
    (SELECT count(*) FROM privacy_protected.object_targets t WHERE t.checkpoint_id=p_checkpoint_id)
   OR i.id IS NULL OR i.source_kind<>p_source_kind OR i.source_ref<>p_source_ref
   OR NOT (p_source_kind=ANY(c.source_kinds)) OR digest(convert_to(p_object_key,'UTF8'),'sha256') IS DISTINCT FROM i.locator_commitment
   OR (p_source_kind='MEMBER_PROFILE_PHOTO' AND (i.subject_user_id IS DISTINCT FROM c.subject_user_id OR i.source_ref<>c.subject_user_id))
   OR (p_source_kind='REPAIR_ATTACHMENT' AND i.subject_user_id IS DISTINCT FROM c.subject_user_id)
   OR (p_source_kind='EQUIPMENT_PHOTO' AND i.provenance_actor_user_id IS DISTINCT FROM c.subject_user_id)
   OR NOT public.privacy_upload_pointer_matches(i.id,i.source_kind,i.source_ref,i.locator_commitment,i.content_type,i.size_bytes)
   OR (SELECT status FROM privacy_protected.object_upload_intent_events e WHERE e.intent_id=i.id ORDER BY sequence DESC LIMIT 1)<>'ATTACHED' THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_object_target_invalid';
 END IF;
 INSERT INTO privacy_protected.object_targets(id,execution_id,job_id,checkpoint_id,plan_entry_sha256,category_key,service_code,target_kind,
  source_kind,source_ref,operation_code,action_version,provider_contract_version,envelope_version,algorithm,encryption_key_id,
  encapsulation,nonce,ciphertext,created_at,upload_intent_id)
 VALUES(p_target_id,p_execution_id,p_job_id,p_checkpoint_id,p_plan_entry_sha256,p_category_key,'private-media','OBJECT_KEY',p_source_kind,
  p_source_ref,'OBJECT_VERSION_DELETE','v1','s3-versioned/v1',p_envelope_version,p_algorithm,p_encryption_key_id,
  p_encapsulation,p_nonce,p_ciphertext,clock_timestamp(),p_upload_intent_id);
 INSERT INTO privacy_protected.object_target_digests(target_id,execution_id,service_code,target_kind,digest_key_id,locator_digest,created_at)
 VALUES(p_target_id,p_execution_id,'private-media','OBJECT_KEY',p_digest_key_id,p_locator_digest,clock_timestamp());
 RETURN p_target_id;
END; $$;

CREATE FUNCTION public.privacy_execution_complete_object_capture(p_execution_id uuid,p_category_key text) RETURNS integer
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE expected integer; actual integer; p_checkpoint_id uuid;
BEGIN
 SELECT checkpoint_id,expected_target_count INTO p_checkpoint_id,expected FROM privacy_protected.object_capture_sets
  WHERE execution_id=p_execution_id AND category_key=p_category_key FOR SHARE;
 SELECT count(*)::integer INTO actual FROM privacy_protected.object_targets WHERE execution_id=p_execution_id AND checkpoint_id=p_checkpoint_id;
 IF expected IS NULL OR actual<>expected THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_object_capture_incomplete'; END IF;
 RETURN actual;
END; $$;

-- Block new in-scope uploads after a v2 capture set commits. The subject lock
-- closes the enumeration/creation gap, including category-only requests.
CREATE OR REPLACE FUNCTION public.privacy_upload_begin(
 p_intent_id uuid,p_subject_user_id uuid,p_actor_user_id uuid,p_source_kind text,p_source_ref uuid,
 p_service_code text,p_content_type text,p_size_bytes bigint,p_token bytea
) RETURNS jsonb LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE v_now timestamptz:=clock_timestamp(); v_token_digest bytea; v_privacy_subject uuid:=COALESCE(p_subject_user_id,p_actor_user_id);
BEGIN
 IF p_intent_id IS NULL OR p_actor_user_id IS NULL OR p_source_ref IS NULL OR octet_length(p_token)<>32 THEN RAISE EXCEPTION 'invalid upload reservation'; END IF;
 IF p_source_kind NOT IN ('MEMBER_PROFILE_PHOTO','REPAIR_ATTACHMENT','EQUIPMENT_PHOTO') OR p_service_code<>'private-media'
    OR p_content_type NOT IN ('image/jpeg','image/png','image/webp') OR p_size_bytes NOT BETWEEN 1 AND 10485760 THEN RAISE EXCEPTION 'invalid upload reservation'; END IF;
 IF p_source_kind IN ('MEMBER_PROFILE_PHOTO','REPAIR_ATTACHMENT') AND p_subject_user_id IS DISTINCT FROM p_actor_user_id THEN RAISE EXCEPTION 'upload actor is not authorized'; END IF;
 IF p_source_kind='EQUIPMENT_PHOTO' AND (p_subject_user_id IS NOT NULL OR NOT EXISTS(
   SELECT 1 FROM public.users u JOIN public.user_platform_roles ur ON ur.user_id=u.id JOIN public.platform_roles r ON r.id=ur.role_id
   WHERE u.id=p_actor_user_id AND u.is_active AND u.erased_at IS NULL AND NOT u.is_dependent AND r.code='ADMIN')) THEN RAISE EXCEPTION 'upload actor is not authorized'; END IF;
 IF p_source_kind='MEMBER_PROFILE_PHOTO' AND p_source_ref<>p_subject_user_id THEN RAISE EXCEPTION 'invalid upload source'; END IF;
 PERFORM public.privacy_media_subject_lock(v_privacy_subject);
 IF EXISTS(SELECT 1 FROM privacy_protected.object_capture_sets c
  JOIN public.privacy_erasure_executions x ON x.id=c.execution_id
  JOIN public.data_erasure_requests r ON r.id=x.request_id
  WHERE c.subject_user_id=v_privacy_subject AND p_source_kind=ANY(c.source_kinds)
    AND r.status IN ('PROCESSING','RETRYABLE_FAILED','TERMINAL_FAILED')) THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_media_execution_in_progress';
 END IF;
 PERFORM public.privacy_upload_source_lock(p_source_kind,p_source_ref);
 v_token_digest:=digest(p_token,'sha256');
 INSERT INTO privacy_protected.object_upload_intent_reservations(id,subject_user_id,provenance_actor_user_id,source_kind,source_ref,service_code,content_type,size_bytes,token_digest,created_at,cleanup_after)
 VALUES(p_intent_id,p_subject_user_id,p_actor_user_id,p_source_kind,p_source_ref,p_service_code,p_content_type,p_size_bytes,v_token_digest,v_now,v_now+interval '24 hours');
 INSERT INTO privacy_protected.object_upload_intent_holds(intent_id,hold_epoch,token_digest,held_until,updated_at)
 VALUES(p_intent_id,1,v_token_digest,v_now+interval '15 minutes',v_now);
 RETURN jsonb_build_object('created_at',v_now,'cleanup_after',v_now+interval '24 hours','hold_epoch',1);
END; $$;

CREATE FUNCTION public.privacy_worker_list_object_targets(
 p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_epoch bigint,p_worker_ref uuid
) RETURNS TABLE(target_id uuid,execution_id uuid,job_id uuid,checkpoint_id uuid,plan_entry_sha256 bytea,category_key text,
 service_code text,target_kind text,source_kind text,source_ref uuid,operation_code text,action_version text,
 provider_contract_version text,envelope_version text,algorithm text,encryption_key_id text,encapsulation bytea,nonce bytea,ciphertext bytea)
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT t.id,t.execution_id,t.job_id,t.checkpoint_id,t.plan_entry_sha256,t.category_key::text,t.service_code::text,t.target_kind::text,
  t.source_kind::text,t.source_ref,t.operation_code::text,t.action_version::text,t.provider_contract_version::text,t.envelope_version::text,
  t.algorithm::text,t.encryption_key_id::text,t.encapsulation,t.nonce,t.ciphertext
 FROM privacy_protected.object_targets t
 JOIN public.privacy_erasure_category_jobs j ON j.id=t.job_id
 JOIN public.privacy_erasure_job_leases l ON l.id=p_lease_id AND l.job_id=j.id AND l.epoch=p_epoch
 JOIN public.privacy_erasure_job_attempts a ON a.id=p_attempt_id AND a.job_id=j.id AND a.lease_id=l.id AND a.lease_epoch=p_epoch
 LEFT JOIN privacy_protected.object_evidence e ON e.target_id=t.id
 WHERE j.id=p_job_id AND j.status='LEASED' AND l.worker_ref=p_worker_ref AND l.released_at IS NULL
  AND l.expires_at>clock_timestamp() AND a.finished_at IS NULL AND e.target_id IS NULL
 ORDER BY t.id;
$$;

CREATE FUNCTION public.privacy_worker_record_object_evidence(
 p_target_id uuid,p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_epoch bigint,p_worker_ref uuid,
 p_deleted_versions integer,p_deleted_markers integer,p_list_calls integer,p_stable_checks integer,
 p_transcript_key_id text,p_transcript_digest bytea
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
 PERFORM 1 FROM public.privacy_erasure_category_jobs j
 JOIN public.privacy_erasure_job_leases l ON l.id=p_lease_id AND l.job_id=j.id AND l.epoch=p_epoch
 JOIN public.privacy_erasure_job_attempts a ON a.id=p_attempt_id AND a.job_id=j.id AND a.lease_id=l.id AND a.lease_epoch=p_epoch
 JOIN privacy_protected.object_targets t ON t.id=p_target_id AND t.job_id=j.id
 WHERE j.id=p_job_id AND j.status='LEASED' AND l.worker_ref=p_worker_ref AND l.released_at IS NULL
  AND l.expires_at>clock_timestamp() AND a.finished_at IS NULL FOR UPDATE OF j,l,a;
 IF NOT FOUND OR p_deleted_versions<0 OR p_deleted_markers<0 OR p_list_calls<2 OR p_stable_checks<2
  OR p_transcript_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$' OR octet_length(p_transcript_digest)<>32 THEN RETURN NULL; END IF;
 INSERT INTO privacy_protected.object_evidence(target_id,job_id,attempt_id,evidence_version,outcome_code,deleted_version_count,
  deleted_marker_count,list_call_count,stable_empty_check_count,transcript_key_id,transcript_digest,occurred_at)
 VALUES(p_target_id,p_job_id,p_attempt_id,'s3-absence/v1','ABSENCE_VERIFIED',p_deleted_versions,p_deleted_markers,p_list_calls,
  p_stable_checks,p_transcript_key_id,p_transcript_digest,clock_timestamp()) ON CONFLICT(target_id) DO NOTHING;
 RETURN p_target_id;
END; $$;

CREATE FUNCTION public.privacy_worker_complete_object_checkpoint(
 p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_epoch bigint,p_worker_ref uuid
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE checkpoint_ref uuid; expected integer; evidenced integer;
BEGIN
 SELECT checkpoint.id,c.expected_target_count INTO checkpoint_ref,expected
 FROM public.privacy_erasure_category_jobs j
 JOIN public.privacy_erasure_job_leases l ON l.id=p_lease_id AND l.job_id=j.id AND l.epoch=p_epoch
 JOIN public.privacy_erasure_job_attempts a ON a.id=p_attempt_id AND a.job_id=j.id AND a.lease_id=l.id AND a.lease_epoch=p_epoch
 JOIN public.privacy_erasure_job_checkpoints checkpoint ON checkpoint.job_id=j.id AND checkpoint.operation_code='OBJECT_VERSION_DELETE' AND checkpoint.action_version='v1'
 JOIN privacy_protected.object_capture_sets c ON c.checkpoint_id=checkpoint.id
 WHERE j.id=p_job_id AND j.status='LEASED' AND l.worker_ref=p_worker_ref AND l.released_at IS NULL
  AND l.expires_at>clock_timestamp() AND a.finished_at IS NULL FOR UPDATE OF j,l,a,checkpoint;
 IF checkpoint_ref IS NULL THEN RETURN NULL; END IF;
 IF (SELECT status FROM public.privacy_erasure_job_checkpoints WHERE id=checkpoint_ref)='SUCCEEDED' THEN RETURN checkpoint_ref; END IF;
 SELECT count(*)::integer INTO evidenced FROM privacy_protected.object_targets t
 JOIN privacy_protected.object_evidence e ON e.target_id=t.id WHERE t.checkpoint_id=checkpoint_ref;
 IF evidenced<>expected OR EXISTS(SELECT 1 FROM public.privacy_erasure_job_checkpoints p
   WHERE p.job_id=p_job_id AND p.operation_position<(SELECT operation_position FROM public.privacy_erasure_job_checkpoints WHERE id=checkpoint_ref)
    AND p.status<>'SUCCEEDED')
  OR EXISTS(SELECT 1 FROM public.privacy_erasure_category_jobs prior
   WHERE prior.execution_id=(SELECT execution_id FROM public.privacy_erasure_category_jobs WHERE id=p_job_id)
    AND prior.plan_entry_position<(SELECT plan_entry_position FROM public.privacy_erasure_category_jobs WHERE id=p_job_id) AND prior.status<>'SUCCEEDED') THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_object_evidence_incomplete';
 END IF;
 UPDATE public.privacy_erasure_job_checkpoints SET status='SUCCEEDED',completed_at=clock_timestamp(),completed_by_attempt_id=p_attempt_id,
  affected_rows=evidenced,result_sha256=digest(convert_to('OBJECT_VERSION_DELETE:'||evidenced::text,'UTF8'),'sha256')
 WHERE id=checkpoint_ref AND status='PENDING';
 RETURN checkpoint_ref;
END; $$;

REVOKE ALL ON TABLE privacy_protected.object_capture_sets FROM PUBLIC;
REVOKE ALL ON FUNCTION public.privacy_media_subject_lock(uuid),public.privacy_execution_capture_media_sources(uuid,uuid,text),
 public.privacy_execution_materialize_object_target(uuid,uuid,uuid,uuid,bytea,text,text,uuid,uuid,text,text,text,text,bytea,bytea,bytea,text,bytea),
 public.privacy_execution_complete_object_capture(uuid,text),public.privacy_worker_list_object_targets(uuid,uuid,uuid,bigint,uuid),
 public.privacy_worker_record_object_evidence(uuid,uuid,uuid,uuid,bigint,uuid,integer,integer,integer,integer,text,bytea),
 public.privacy_worker_complete_object_checkpoint(uuid,uuid,uuid,bigint,uuid) FROM PUBLIC;
