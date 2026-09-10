-- Durable upload provenance and fenced cleanup for the future #246 object
-- executor. The routines remain unused unless separately configured by the
-- application; this migration does not activate privacy execution or cleanup.

ALTER TABLE privacy_protected.object_upload_intent_events
 DROP CONSTRAINT object_upload_intent_events_reason_code_check;
ALTER TABLE privacy_protected.object_upload_intent_events
 ADD CONSTRAINT object_upload_intent_events_reason_code_check CHECK (reason_code IN (
  'UPLOAD_RESERVED','PUT_ACKNOWLEDGED','POINTER_ATTACHED','PUT_AMBIGUOUS','PUT_FAILED',
  'ATTACH_FAILED','POINTER_SUPERSEDED','POINTER_REMOVED','STALE_TIMEOUT','CLEANUP_CONFIRMED'
 ));
ALTER TABLE privacy_protected.object_upload_intent_events
 DROP CONSTRAINT object_upload_intent_events_check;
ALTER TABLE privacy_protected.object_upload_intent_events
 ADD CONSTRAINT object_upload_intent_events_check CHECK (
  (status='PREPARED' AND reason_code='UPLOAD_RESERVED')
  OR (status='PUT_CONFIRMED' AND reason_code='PUT_ACKNOWLEDGED')
  OR (status='ATTACHED' AND reason_code='POINTER_ATTACHED')
  OR (status='CLEANUP_REQUIRED' AND reason_code IN ('PUT_AMBIGUOUS','PUT_FAILED','ATTACH_FAILED','POINTER_SUPERSEDED','POINTER_REMOVED','STALE_TIMEOUT'))
  OR (status='ABSENCE_VERIFIED' AND reason_code='CLEANUP_CONFIRMED')
 );

ALTER TABLE privacy_protected.object_upload_intents
 ADD COLUMN locator_commitment bytea NULL CHECK (locator_commitment IS NULL OR octet_length(locator_commitment)=32);

CREATE UNIQUE INDEX privacy_upload_intent_prepared_uidx ON privacy_protected.object_upload_intent_events(intent_id) WHERE status='PREPARED';
CREATE UNIQUE INDEX privacy_upload_intent_put_uidx ON privacy_protected.object_upload_intent_events(intent_id) WHERE status='PUT_CONFIRMED';
CREATE UNIQUE INDEX privacy_upload_intent_attached_uidx ON privacy_protected.object_upload_intent_events(intent_id) WHERE status='ATTACHED';
CREATE UNIQUE INDEX privacy_upload_intent_cleanup_uidx ON privacy_protected.object_upload_intent_events(intent_id) WHERE status='CLEANUP_REQUIRED';
CREATE UNIQUE INDEX privacy_upload_intent_absent_uidx ON privacy_protected.object_upload_intent_events(intent_id) WHERE status='ABSENCE_VERIFIED';

CREATE TABLE privacy_protected.object_upload_intent_reservations (
 id uuid PRIMARY KEY,
 subject_user_id uuid NULL REFERENCES public.users(id) ON DELETE RESTRICT,
 provenance_actor_user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE RESTRICT,
 source_kind varchar(40) NOT NULL CHECK (source_kind IN ('MEMBER_PROFILE_PHOTO','REPAIR_ATTACHMENT','EQUIPMENT_PHOTO')),
 source_ref uuid NOT NULL,
 service_code varchar(120) NOT NULL CHECK (service_code='private-media'),
 content_type varchar(100) NOT NULL CHECK (content_type IN ('image/jpeg','image/png','image/webp')),
 size_bytes bigint NOT NULL CHECK (size_bytes BETWEEN 1 AND 10485760),
 token_digest bytea NOT NULL CHECK (octet_length(token_digest)=32),
 created_at timestamptz NOT NULL,
 cleanup_after timestamptz NOT NULL,
 finalized_at timestamptz NULL,
 CHECK (cleanup_after=created_at+interval '24 hours'),
 CHECK ((source_kind IN ('MEMBER_PROFILE_PHOTO','REPAIR_ATTACHMENT') AND subject_user_id IS NOT NULL)
    OR (source_kind='EQUIPMENT_PHOTO' AND subject_user_id IS NULL)),
 UNIQUE(source_kind,source_ref,id)
);

CREATE TABLE privacy_protected.object_upload_intent_holds (
 intent_id uuid PRIMARY KEY REFERENCES privacy_protected.object_upload_intent_reservations(id) ON DELETE RESTRICT,
 hold_epoch bigint NOT NULL DEFAULT 1 CHECK (hold_epoch>0),
 token_digest bytea NOT NULL CHECK (octet_length(token_digest)=32),
 held_until timestamptz NOT NULL,
 released_at timestamptz NULL,
 updated_at timestamptz NOT NULL,
 CHECK (released_at IS NULL OR released_at<=updated_at)
);

CREATE TABLE privacy_protected.object_upload_cleanup_jobs (
 intent_id uuid PRIMARY KEY REFERENCES privacy_protected.object_upload_intents(id) ON DELETE RESTRICT,
 status varchar(20) NOT NULL CHECK (status IN ('PENDING','LEASED','RETRY_WAIT','SUCCEEDED','TERMINAL_FAILED')),
 lease_epoch bigint NOT NULL DEFAULT 0 CHECK (lease_epoch>=0),
 attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count>=0),
 next_attempt_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL
);

CREATE TABLE privacy_protected.object_upload_cleanup_attempts (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 intent_id uuid NOT NULL REFERENCES privacy_protected.object_upload_cleanup_jobs(intent_id) ON DELETE RESTRICT,
 lease_epoch bigint NOT NULL CHECK (lease_epoch>0),
 worker_ref uuid NOT NULL,
 acquired_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL,
 released_at timestamptz NULL,
 outcome varchar(30) NULL CHECK (outcome IN ('SUCCEEDED','RETRYABLE_FAILED','TERMINAL_FAILED','LEASE_EXPIRED')),
 CHECK (expires_at>acquired_at),
 CHECK ((released_at IS NULL)=(outcome IS NULL)),
 UNIQUE(intent_id,lease_epoch),
 UNIQUE(id,intent_id,lease_epoch)
);

CREATE TABLE privacy_protected.object_upload_absence_evidence (
 intent_id uuid PRIMARY KEY REFERENCES privacy_protected.object_upload_intents(id) ON DELETE RESTRICT,
 attempt_id uuid NOT NULL,
 lease_epoch bigint NOT NULL,
 evidence_version varchar(40) NOT NULL CHECK (evidence_version='s3-absence/v1'),
 deleted_version_count integer NOT NULL CHECK (deleted_version_count>=0),
 deleted_marker_count integer NOT NULL CHECK (deleted_marker_count>=0),
 list_call_count integer NOT NULL CHECK (list_call_count>=2),
 stable_empty_check_count integer NOT NULL CHECK (stable_empty_check_count>=2),
 transcript_key_id varchar(80) NOT NULL,
 transcript_digest bytea NOT NULL CHECK (octet_length(transcript_digest)=32),
 occurred_at timestamptz NOT NULL,
 FOREIGN KEY(attempt_id,intent_id,lease_epoch) REFERENCES privacy_protected.object_upload_cleanup_attempts(id,intent_id,lease_epoch) ON DELETE RESTRICT
);

CREATE TRIGGER privacy_upload_cleanup_attempts_no_delete BEFORE DELETE ON privacy_protected.object_upload_cleanup_attempts FOR EACH ROW EXECUTE FUNCTION public.prevent_privacy_execution_record_delete();
CREATE TRIGGER privacy_upload_absence_evidence_immutable BEFORE UPDATE OR DELETE ON privacy_protected.object_upload_absence_evidence FOR EACH ROW EXECUTE FUNCTION public.prevent_privacy_execution_record_delete();

CREATE FUNCTION public.privacy_upload_source_lock(p_source_kind text,p_source_ref uuid) RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT pg_advisory_xact_lock(hashtextextended('mycfc/object-source/v1:'||p_source_kind||':'||p_source_ref::text,0));
$$;

CREATE FUNCTION public.privacy_upload_pointer_references(p_intent_id uuid,p_source_kind text,p_source_ref uuid) RETURNS boolean
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT CASE p_source_kind
  WHEN 'MEMBER_PROFILE_PHOTO' THEN EXISTS(SELECT 1 FROM public.member_profiles p WHERE p.user_id=p_source_ref AND p.photo_upload_intent_id=p_intent_id)
  WHEN 'REPAIR_ATTACHMENT' THEN EXISTS(SELECT 1 FROM public.repair_requests p WHERE p.id=p_source_ref AND p.image_upload_intent_id=p_intent_id)
  WHEN 'EQUIPMENT_PHOTO' THEN EXISTS(SELECT 1 FROM public.equipment p WHERE p.id=p_source_ref AND p.image_upload_intent_id=p_intent_id)
  ELSE false END;
$$;

CREATE FUNCTION public.privacy_upload_pointer_matches(
 p_intent_id uuid,p_source_kind text,p_source_ref uuid,p_locator_commitment bytea,p_content_type text,p_size_bytes bigint
) RETURNS boolean LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT CASE p_source_kind
  WHEN 'MEMBER_PROFILE_PHOTO' THEN EXISTS(SELECT 1 FROM public.member_profiles p WHERE p.user_id=p_source_ref AND p.photo_upload_intent_id=p_intent_id
    AND digest(convert_to(p.photo_object_key,'UTF8'),'sha256')=p_locator_commitment AND p.photo_content_type=p_content_type AND p.photo_size_bytes=p_size_bytes)
  WHEN 'REPAIR_ATTACHMENT' THEN EXISTS(SELECT 1 FROM public.repair_requests p WHERE p.id=p_source_ref AND p.image_upload_intent_id=p_intent_id
    AND digest(convert_to(p.image_object_key,'UTF8'),'sha256')=p_locator_commitment AND p.image_content_type=p_content_type AND p.image_size_bytes=p_size_bytes)
  WHEN 'EQUIPMENT_PHOTO' THEN EXISTS(SELECT 1 FROM public.equipment p WHERE p.id=p_source_ref AND p.image_upload_intent_id=p_intent_id
    AND digest(convert_to(p.image_object_key,'UTF8'),'sha256')=p_locator_commitment AND p.image_content_type=p_content_type AND p.image_size_bytes=p_size_bytes)
  ELSE false END;
$$;

CREATE FUNCTION public.privacy_upload_enforce_pointer_mutation() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE
 v_source_kind text; v_old_source_ref uuid; v_current_source_ref uuid; v_old_intent uuid; v_old_key text; v_old_content_type text; v_old_size bigint;
 v_current_intent uuid; v_current_key text; v_current_content_type text; v_current_size bigint;
 v_latest_status text; r privacy_protected.object_upload_intents%ROWTYPE;
BEGIN
 CASE TG_TABLE_NAME
  WHEN 'member_profiles' THEN
   v_source_kind:='MEMBER_PROFILE_PHOTO';
   IF TG_OP='INSERT' THEN
    v_current_source_ref:=NEW.user_id;
   ELSE
    v_old_source_ref:=OLD.user_id; v_current_source_ref:=CASE WHEN TG_OP='DELETE' THEN OLD.user_id ELSE NEW.user_id END;
    v_old_intent:=OLD.photo_upload_intent_id; v_old_key:=OLD.photo_object_key;
    v_old_content_type:=OLD.photo_content_type; v_old_size:=OLD.photo_size_bytes;
    IF TG_OP='UPDATE' AND ROW(OLD.user_id,OLD.photo_object_key,OLD.photo_content_type,OLD.photo_size_bytes,OLD.photo_upload_intent_id)
      IS NOT DISTINCT FROM ROW(NEW.user_id,NEW.photo_object_key,NEW.photo_content_type,NEW.photo_size_bytes,NEW.photo_upload_intent_id) THEN RETURN NULL; END IF;
   END IF;
   SELECT photo_upload_intent_id,photo_object_key,photo_content_type,photo_size_bytes
    INTO v_current_intent,v_current_key,v_current_content_type,v_current_size FROM public.member_profiles WHERE user_id=v_current_source_ref;
  WHEN 'repair_requests' THEN
   v_source_kind:='REPAIR_ATTACHMENT';
   IF TG_OP='INSERT' THEN
    v_current_source_ref:=NEW.id;
   ELSE
    v_old_source_ref:=OLD.id; v_current_source_ref:=CASE WHEN TG_OP='DELETE' THEN OLD.id ELSE NEW.id END;
    v_old_intent:=OLD.image_upload_intent_id; v_old_key:=OLD.image_object_key;
    v_old_content_type:=OLD.image_content_type; v_old_size:=OLD.image_size_bytes;
    IF TG_OP='UPDATE' AND ROW(OLD.id,OLD.image_object_key,OLD.image_content_type,OLD.image_size_bytes,OLD.image_upload_intent_id)
      IS NOT DISTINCT FROM ROW(NEW.id,NEW.image_object_key,NEW.image_content_type,NEW.image_size_bytes,NEW.image_upload_intent_id) THEN RETURN NULL; END IF;
   END IF;
   SELECT image_upload_intent_id,image_object_key,image_content_type,image_size_bytes
    INTO v_current_intent,v_current_key,v_current_content_type,v_current_size FROM public.repair_requests WHERE id=v_current_source_ref;
  WHEN 'equipment' THEN
   v_source_kind:='EQUIPMENT_PHOTO';
   IF TG_OP='INSERT' THEN
    v_current_source_ref:=NEW.id;
   ELSE
    v_old_source_ref:=OLD.id; v_current_source_ref:=CASE WHEN TG_OP='DELETE' THEN OLD.id ELSE NEW.id END;
    v_old_intent:=OLD.image_upload_intent_id; v_old_key:=OLD.image_object_key;
    v_old_content_type:=OLD.image_content_type; v_old_size:=OLD.image_size_bytes;
    IF TG_OP='UPDATE' AND ROW(OLD.id,OLD.image_object_key,OLD.image_content_type,OLD.image_size_bytes,OLD.image_upload_intent_id)
      IS NOT DISTINCT FROM ROW(NEW.id,NEW.image_object_key,NEW.image_content_type,NEW.image_size_bytes,NEW.image_upload_intent_id) THEN RETURN NULL; END IF;
   END IF;
   SELECT image_upload_intent_id,image_object_key,image_content_type,image_size_bytes
    INTO v_current_intent,v_current_key,v_current_content_type,v_current_size FROM public.equipment WHERE id=v_current_source_ref;
  ELSE RAISE EXCEPTION 'unsupported upload pointer table';
 END CASE;

 IF v_old_key IS NOT NULL AND v_old_intent IS NULL THEN RAISE EXCEPTION 'legacy upload pointer requires migration'; END IF;
 IF v_current_key IS NOT NULL AND v_current_intent IS NULL THEN RAISE EXCEPTION 'upload pointer requires provenance'; END IF;
 IF v_current_key IS NULL AND v_current_intent IS NOT NULL THEN RAISE EXCEPTION 'upload pointer invariant rejected'; END IF;

 IF v_current_intent IS NOT NULL THEN
  SELECT * INTO r FROM privacy_protected.object_upload_intents WHERE id=v_current_intent;
  SELECT status INTO v_latest_status FROM privacy_protected.object_upload_intent_events
   WHERE intent_id=v_current_intent ORDER BY sequence DESC LIMIT 1;
  IF r.id IS NULL OR r.source_kind<>v_source_kind OR r.source_ref<>v_current_source_ref OR v_latest_status<>'ATTACHED'
    OR NOT public.privacy_upload_pointer_matches(r.id,r.source_kind,r.source_ref,r.locator_commitment,r.content_type,r.size_bytes) THEN
   RAISE EXCEPTION 'upload pointer invariant rejected';
  END IF;
 END IF;

 IF v_old_intent IS NOT NULL AND v_old_intent IS DISTINCT FROM v_current_intent THEN
  SELECT status INTO v_latest_status FROM privacy_protected.object_upload_intent_events
   WHERE intent_id=v_old_intent ORDER BY sequence DESC LIMIT 1;
  IF v_latest_status NOT IN ('CLEANUP_REQUIRED','ABSENCE_VERIFIED')
    OR public.privacy_upload_pointer_references(v_old_intent,v_source_kind,v_old_source_ref) THEN
   RAISE EXCEPTION 'upload pointer cleanup invariant rejected';
  END IF;
 END IF;
 RETURN NULL;
END; $$;

CREATE CONSTRAINT TRIGGER privacy_upload_profile_pointer_deferred
AFTER INSERT OR UPDATE OR DELETE ON public.member_profiles DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.privacy_upload_enforce_pointer_mutation();
CREATE CONSTRAINT TRIGGER privacy_upload_repair_pointer_deferred
AFTER INSERT OR UPDATE OR DELETE ON public.repair_requests DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.privacy_upload_enforce_pointer_mutation();
CREATE CONSTRAINT TRIGGER privacy_upload_equipment_pointer_deferred
AFTER INSERT OR UPDATE OR DELETE ON public.equipment DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.privacy_upload_enforce_pointer_mutation();

CREATE FUNCTION public.privacy_upload_enforce_pointer_state() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE r privacy_protected.object_upload_intents%ROWTYPE;
BEGIN
 IF NEW.status NOT IN ('ATTACHED','CLEANUP_REQUIRED') OR (NEW.status='CLEANUP_REQUIRED' AND NEW.reason_code NOT IN ('POINTER_SUPERSEDED','POINTER_REMOVED')) THEN RETURN NULL; END IF;
 SELECT * INTO r FROM privacy_protected.object_upload_intents WHERE id=NEW.intent_id;
 IF NEW.status='ATTACHED' AND NOT public.privacy_upload_pointer_matches(r.id,r.source_kind,r.source_ref,r.locator_commitment,r.content_type,r.size_bytes) THEN
  RAISE EXCEPTION 'upload pointer invariant rejected';
 END IF;
 IF NEW.status='CLEANUP_REQUIRED' AND public.privacy_upload_pointer_references(r.id,r.source_kind,r.source_ref) THEN
  RAISE EXCEPTION 'upload pointer invariant rejected';
 END IF;
 RETURN NULL;
END; $$;

CREATE CONSTRAINT TRIGGER privacy_upload_pointer_state_deferred
AFTER INSERT ON privacy_protected.object_upload_intent_events DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.privacy_upload_enforce_pointer_state();

CREATE FUNCTION public.privacy_upload_begin(
 p_intent_id uuid,p_subject_user_id uuid,p_actor_user_id uuid,p_source_kind text,p_source_ref uuid,
 p_service_code text,p_content_type text,p_size_bytes bigint,p_token bytea
) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE v_now timestamptz:=clock_timestamp(); v_token_digest bytea;
BEGIN
 IF p_intent_id IS NULL OR p_actor_user_id IS NULL OR p_source_ref IS NULL OR octet_length(p_token)<>32 THEN RAISE EXCEPTION 'invalid upload reservation'; END IF;
 IF p_source_kind NOT IN ('MEMBER_PROFILE_PHOTO','REPAIR_ATTACHMENT','EQUIPMENT_PHOTO') OR p_service_code<>'private-media'
    OR p_content_type NOT IN ('image/jpeg','image/png','image/webp') OR p_size_bytes NOT BETWEEN 1 AND 10485760 THEN RAISE EXCEPTION 'invalid upload reservation'; END IF;
 IF p_source_kind IN ('MEMBER_PROFILE_PHOTO','REPAIR_ATTACHMENT') AND p_subject_user_id IS DISTINCT FROM p_actor_user_id THEN RAISE EXCEPTION 'upload actor is not authorized'; END IF;
 IF p_source_kind='EQUIPMENT_PHOTO' AND (p_subject_user_id IS NOT NULL OR NOT EXISTS(
   SELECT 1 FROM public.users u JOIN public.user_platform_roles ur ON ur.user_id=u.id JOIN public.platform_roles r ON r.id=ur.role_id
   WHERE u.id=p_actor_user_id AND u.is_active AND u.erased_at IS NULL AND NOT u.is_dependent AND r.code='ADMIN')) THEN RAISE EXCEPTION 'upload actor is not authorized'; END IF;
 IF p_source_kind='MEMBER_PROFILE_PHOTO' AND p_source_ref<>p_subject_user_id THEN RAISE EXCEPTION 'invalid upload source'; END IF;
 PERFORM public.privacy_upload_source_lock(p_source_kind,p_source_ref);
 v_token_digest:=digest(p_token,'sha256');
 INSERT INTO privacy_protected.object_upload_intent_reservations(id,subject_user_id,provenance_actor_user_id,source_kind,source_ref,service_code,content_type,size_bytes,token_digest,created_at,cleanup_after)
 VALUES(p_intent_id,p_subject_user_id,p_actor_user_id,p_source_kind,p_source_ref,p_service_code,p_content_type,p_size_bytes,v_token_digest,v_now,v_now+interval '24 hours');
 INSERT INTO privacy_protected.object_upload_intent_holds(intent_id,hold_epoch,token_digest,held_until,updated_at)
 VALUES(p_intent_id,1,v_token_digest,v_now+interval '15 minutes',v_now);
 RETURN jsonb_build_object('created_at',v_now,'cleanup_after',v_now+interval '24 hours','hold_epoch',1);
END; $$;

CREATE FUNCTION public.privacy_upload_finalize(
 p_intent_id uuid,p_token bytea,p_envelope_version text,p_algorithm text,p_encryption_key_id text,
 p_encapsulation bytea,p_nonce bytea,p_ciphertext bytea,p_digest_key_id text,p_locator_digest bytea,p_locator_commitment bytea
) RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE r privacy_protected.object_upload_intent_reservations%ROWTYPE; h privacy_protected.object_upload_intent_holds%ROWTYPE;
BEGIN
 SELECT * INTO r FROM privacy_protected.object_upload_intent_reservations WHERE id=p_intent_id FOR UPDATE;
 SELECT * INTO h FROM privacy_protected.object_upload_intent_holds WHERE intent_id=p_intent_id FOR UPDATE;
 IF r.id IS NULL OR h.intent_id IS NULL OR r.finalized_at IS NOT NULL OR h.released_at IS NOT NULL OR h.held_until<=clock_timestamp()
    OR p_token IS NULL OR octet_length(p_token)<>32 OR h.token_digest IS DISTINCT FROM digest(p_token,'sha256') OR r.token_digest<>h.token_digest
    OR p_locator_commitment IS NULL OR octet_length(p_locator_commitment)<>32 THEN RAISE EXCEPTION 'upload reservation is unavailable'; END IF;
 INSERT INTO privacy_protected.object_upload_intents(id,subject_user_id,provenance_actor_user_id,source_kind,source_ref,service_code,target_kind,provider_contract_version,
  envelope_version,algorithm,encryption_key_id,encapsulation,nonce,ciphertext,digest_key_id,locator_digest,locator_commitment,content_type,size_bytes,cleanup_after,created_at)
 VALUES(r.id,r.subject_user_id,r.provenance_actor_user_id,r.source_kind,r.source_ref,r.service_code,'OBJECT_KEY','s3-versioned/v1',
  p_envelope_version,p_algorithm,p_encryption_key_id,p_encapsulation,p_nonce,p_ciphertext,p_digest_key_id,p_locator_digest,p_locator_commitment,r.content_type,r.size_bytes,r.cleanup_after,r.created_at);
 INSERT INTO privacy_protected.object_upload_intent_events(intent_id,sequence,status,reason_code,occurred_at) VALUES(r.id,1,'PREPARED','UPLOAD_RESERVED',clock_timestamp());
 UPDATE privacy_protected.object_upload_intent_reservations SET finalized_at=clock_timestamp() WHERE id=r.id;
END; $$;

CREATE FUNCTION public.privacy_upload_confirm_put(p_intent_id uuid,p_token bytea) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE r privacy_protected.object_upload_intents%ROWTYPE; h privacy_protected.object_upload_intent_holds%ROWTYPE; v_status text;
BEGIN
 SELECT * INTO r FROM privacy_protected.object_upload_intents WHERE id=p_intent_id FOR UPDATE;
 IF r.id IS NULL THEN RAISE EXCEPTION 'upload intent is unavailable'; END IF;
 PERFORM public.privacy_upload_source_lock(r.source_kind,r.source_ref);
 SELECT * INTO h FROM privacy_protected.object_upload_intent_holds WHERE intent_id=p_intent_id FOR UPDATE;
 SELECT status INTO v_status FROM privacy_protected.object_upload_intent_events WHERE intent_id=p_intent_id ORDER BY sequence DESC LIMIT 1;
 IF p_token IS NULL OR octet_length(p_token)<>32 OR h.token_digest IS DISTINCT FROM digest(p_token,'sha256') OR h.released_at IS NOT NULL OR h.held_until<=clock_timestamp() OR v_status<>'PREPARED' THEN RAISE EXCEPTION 'upload intent transition rejected'; END IF;
 INSERT INTO privacy_protected.object_upload_intent_events VALUES(p_intent_id,2,'PUT_CONFIRMED','PUT_ACKNOWLEDGED',clock_timestamp());
 UPDATE privacy_protected.object_upload_intent_holds SET held_until=clock_timestamp()+interval '15 minutes',updated_at=clock_timestamp() WHERE intent_id=p_intent_id;
END; $$;

CREATE FUNCTION public.privacy_upload_mark_cleanup(p_intent_id uuid,p_token bytea,p_reason text) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE r privacy_protected.object_upload_intents%ROWTYPE; h privacy_protected.object_upload_intent_holds%ROWTYPE; v_status text; v_sequence integer;
BEGIN
 SELECT * INTO r FROM privacy_protected.object_upload_intents WHERE id=p_intent_id FOR UPDATE;
 IF r.id IS NULL THEN RAISE EXCEPTION 'upload intent is unavailable'; END IF;
 PERFORM public.privacy_upload_source_lock(r.source_kind,r.source_ref);
 SELECT * INTO h FROM privacy_protected.object_upload_intent_holds WHERE intent_id=p_intent_id FOR UPDATE;
 IF p_token IS NULL OR octet_length(p_token)<>32 OR h.token_digest IS DISTINCT FROM digest(p_token,'sha256') THEN RAISE EXCEPTION 'upload intent transition rejected'; END IF;
 SELECT status,sequence INTO v_status,v_sequence FROM privacy_protected.object_upload_intent_events WHERE intent_id=p_intent_id ORDER BY sequence DESC LIMIT 1;
 IF v_status='CLEANUP_REQUIRED' THEN RETURN; END IF;
 IF p_reason NOT IN ('PUT_AMBIGUOUS','PUT_FAILED','ATTACH_FAILED','POINTER_SUPERSEDED','POINTER_REMOVED')
   OR (v_status='PREPARED' AND p_reason NOT IN ('PUT_AMBIGUOUS','PUT_FAILED'))
   OR (v_status='PUT_CONFIRMED' AND p_reason NOT IN ('PUT_AMBIGUOUS','ATTACH_FAILED'))
   OR (v_status='ATTACHED' AND p_reason NOT IN ('POINTER_SUPERSEDED','POINTER_REMOVED')) THEN RAISE EXCEPTION 'upload intent transition rejected'; END IF;
 INSERT INTO privacy_protected.object_upload_intent_events VALUES(p_intent_id,v_sequence+1,'CLEANUP_REQUIRED',p_reason,clock_timestamp());
 UPDATE privacy_protected.object_upload_intent_holds SET released_at=clock_timestamp(),updated_at=clock_timestamp() WHERE intent_id=p_intent_id AND released_at IS NULL;
 INSERT INTO privacy_protected.object_upload_cleanup_jobs(intent_id,status,next_attempt_at,updated_at) VALUES(p_intent_id,'PENDING',clock_timestamp(),clock_timestamp()) ON CONFLICT(intent_id) DO NOTHING;
END; $$;

CREATE FUNCTION public.privacy_upload_attach(
 p_intent_id uuid,p_token bytea,p_prior_intent_id uuid,p_expected_source_kind text,p_expected_source_ref uuid,
 p_object_key text,p_content_type text,p_size_bytes bigint
) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE r privacy_protected.object_upload_intents%ROWTYPE; h privacy_protected.object_upload_intent_holds%ROWTYPE; v_status text; v_sequence integer; p_status text; p_sequence integer; v_pointer_attached boolean:=false;
BEGIN
 IF p_intent_id IS NULL AND p_token IS NULL THEN RETURN; END IF;
 SELECT * INTO r FROM privacy_protected.object_upload_intents WHERE id=p_intent_id FOR UPDATE;
 IF r.id IS NULL THEN RAISE EXCEPTION 'upload intent is unavailable'; END IF;
 IF r.source_kind<>p_expected_source_kind OR r.source_ref<>p_expected_source_ref THEN RAISE EXCEPTION 'upload intent source mismatch'; END IF;
 IF p_object_key IS NULL OR digest(convert_to(p_object_key,'UTF8'),'sha256') IS DISTINCT FROM r.locator_commitment
    OR p_content_type IS DISTINCT FROM r.content_type OR p_size_bytes IS DISTINCT FROM r.size_bytes THEN RAISE EXCEPTION 'upload intent pointer mismatch'; END IF;
 PERFORM public.privacy_upload_source_lock(r.source_kind,r.source_ref);
 SELECT * INTO h FROM privacy_protected.object_upload_intent_holds WHERE intent_id=p_intent_id FOR UPDATE;
 SELECT status,sequence INTO v_status,v_sequence FROM privacy_protected.object_upload_intent_events WHERE intent_id=p_intent_id ORDER BY sequence DESC LIMIT 1;
 IF p_token IS NULL OR octet_length(p_token)<>32 OR h.token_digest IS DISTINCT FROM digest(p_token,'sha256') THEN RAISE EXCEPTION 'upload intent transition rejected'; END IF;
 IF v_status='ATTACHED' THEN
  v_pointer_attached:=CASE r.source_kind
   WHEN 'MEMBER_PROFILE_PHOTO' THEN EXISTS(SELECT 1 FROM public.member_profiles p WHERE p.user_id=r.source_ref AND p.photo_upload_intent_id=r.id)
   WHEN 'REPAIR_ATTACHMENT' THEN EXISTS(SELECT 1 FROM public.repair_requests p WHERE p.id=r.source_ref AND p.image_upload_intent_id=r.id)
   WHEN 'EQUIPMENT_PHOTO' THEN EXISTS(SELECT 1 FROM public.equipment p WHERE p.id=r.source_ref AND p.image_upload_intent_id=r.id)
   ELSE false END;
  IF v_pointer_attached THEN RETURN; END IF;
  RAISE EXCEPTION 'upload intent transition rejected';
 END IF;
 IF v_status<>'PUT_CONFIRMED' OR r.cleanup_after<=clock_timestamp() OR h.released_at IS NOT NULL OR h.held_until<=clock_timestamp() THEN RAISE EXCEPTION 'upload intent transition rejected'; END IF;
 IF r.source_kind='MEMBER_PROFILE_PHOTO' AND EXISTS(SELECT 1 FROM public.member_profiles p WHERE p.user_id=r.source_ref AND p.photo_object_key IS NOT NULL AND p.photo_upload_intent_id IS NULL) THEN RAISE EXCEPTION 'legacy upload pointer requires migration'; END IF;
 IF r.source_kind='EQUIPMENT_PHOTO' AND EXISTS(SELECT 1 FROM public.equipment p WHERE p.id=r.source_ref AND p.image_object_key IS NOT NULL AND p.image_upload_intent_id IS NULL) THEN RAISE EXCEPTION 'legacy upload pointer requires migration'; END IF;
 INSERT INTO privacy_protected.object_upload_intent_events VALUES(p_intent_id,v_sequence+1,'ATTACHED','POINTER_ATTACHED',clock_timestamp());
 UPDATE privacy_protected.object_upload_intent_holds SET released_at=clock_timestamp(),updated_at=clock_timestamp() WHERE intent_id=p_intent_id;
 IF p_prior_intent_id IS NOT NULL AND p_prior_intent_id<>p_intent_id THEN
  SELECT e.status,e.sequence INTO p_status,p_sequence FROM privacy_protected.object_upload_intent_events e
  JOIN privacy_protected.object_upload_intents prior ON prior.id=e.intent_id
  WHERE e.intent_id=p_prior_intent_id AND prior.source_kind=r.source_kind AND prior.source_ref=r.source_ref
  ORDER BY e.sequence DESC LIMIT 1 FOR UPDATE OF e;
  IF p_status<>'ATTACHED' THEN RAISE EXCEPTION 'prior upload intent transition rejected'; END IF;
  INSERT INTO privacy_protected.object_upload_intent_events VALUES(p_prior_intent_id,p_sequence+1,'CLEANUP_REQUIRED','POINTER_SUPERSEDED',clock_timestamp());
  INSERT INTO privacy_protected.object_upload_cleanup_jobs(intent_id,status,next_attempt_at,updated_at) VALUES(p_prior_intent_id,'PENDING',clock_timestamp(),clock_timestamp()) ON CONFLICT(intent_id) DO NOTHING;
 END IF;
END; $$;

CREATE FUNCTION public.privacy_upload_remove(p_intent_id uuid,p_actor_user_id uuid,p_expected_source_kind text,p_expected_source_ref uuid) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE r privacy_protected.object_upload_intents%ROWTYPE; v_status text; v_sequence integer;
BEGIN
 IF p_intent_id IS NULL OR p_actor_user_id IS NULL OR p_expected_source_kind<>'MEMBER_PROFILE_PHOTO' THEN RAISE EXCEPTION 'upload removal rejected'; END IF;
 SELECT * INTO r FROM privacy_protected.object_upload_intents WHERE id=p_intent_id FOR UPDATE;
 IF r.id IS NULL THEN RAISE EXCEPTION 'upload intent is unavailable'; END IF;
 IF r.source_kind<>p_expected_source_kind OR r.source_ref<>p_expected_source_ref THEN RAISE EXCEPTION 'upload removal rejected'; END IF;
 IF p_actor_user_id<>r.source_ref AND NOT EXISTS(
   SELECT 1 FROM public.users subject WHERE subject.id=r.source_ref AND subject.guardian_id=p_actor_user_id
   UNION ALL
   SELECT 1 FROM public.user_platform_roles ur JOIN public.platform_roles role ON role.id=ur.role_id
   JOIN public.users actor ON actor.id=ur.user_id WHERE ur.user_id=p_actor_user_id AND role.code='ADMIN' AND actor.is_active AND actor.erased_at IS NULL
  ) THEN RAISE EXCEPTION 'upload removal rejected'; END IF;
 PERFORM public.privacy_upload_source_lock(r.source_kind,r.source_ref);
 SELECT status,sequence INTO v_status,v_sequence FROM privacy_protected.object_upload_intent_events WHERE intent_id=p_intent_id ORDER BY sequence DESC LIMIT 1;
 IF v_status='CLEANUP_REQUIRED' THEN RETURN; END IF;
 IF v_status<>'ATTACHED' THEN RAISE EXCEPTION 'upload intent transition rejected'; END IF;
 INSERT INTO privacy_protected.object_upload_intent_events VALUES(p_intent_id,v_sequence+1,'CLEANUP_REQUIRED','POINTER_REMOVED',clock_timestamp());
 INSERT INTO privacy_protected.object_upload_cleanup_jobs(intent_id,status,next_attempt_at,updated_at) VALUES(p_intent_id,'PENDING',clock_timestamp(),clock_timestamp()) ON CONFLICT(intent_id) DO NOTHING;
END; $$;

CREATE FUNCTION public.privacy_upload_cleanup_claim(p_lease_milliseconds bigint,p_worker_ref uuid)
RETURNS TABLE(
 intent_id uuid,lease_epoch bigint,attempt_id uuid,attempt_count integer,
 subject_user_id uuid,provenance_actor_user_id uuid,source_kind text,source_ref uuid,
 service_code text,target_kind text,provider_contract_version text,envelope_version text,
 algorithm text,encryption_key_id text,encapsulation bytea,nonce bytea,ciphertext bytea,
 content_type text,size_bytes bigint,cleanup_after timestamptz,created_at timestamptz
) LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE v_now timestamptz:=clock_timestamp(); v_intent uuid; v_source_kind text; v_source_ref uuid;
 v_status text; v_sequence integer; v_epoch bigint; v_attempt uuid; v_attempt_count integer;
BEGIN
 IF p_worker_ref IS NULL OR p_lease_milliseconds<1000 OR p_lease_milliseconds>3600000 THEN RAISE EXCEPTION 'invalid cleanup claim'; END IF;

 SELECT i.id,i.source_kind,i.source_ref INTO v_intent,v_source_kind,v_source_ref
 FROM privacy_protected.object_upload_intents i
 JOIN LATERAL (SELECT e.status FROM privacy_protected.object_upload_intent_events e WHERE e.intent_id=i.id ORDER BY e.sequence DESC LIMIT 1) latest ON true
 JOIN privacy_protected.object_upload_intent_holds h ON h.intent_id=i.id
 WHERE latest.status IN ('PREPARED','PUT_CONFIRMED') AND i.cleanup_after<=v_now
   AND NOT (h.released_at IS NULL AND h.held_until>v_now)
 ORDER BY i.cleanup_after,i.id FOR UPDATE OF i SKIP LOCKED LIMIT 1;
 IF v_intent IS NOT NULL THEN
  PERFORM public.privacy_upload_source_lock(v_source_kind,v_source_ref);
  SELECT e.status,e.sequence INTO v_status,v_sequence FROM privacy_protected.object_upload_intent_events e WHERE e.intent_id=v_intent ORDER BY e.sequence DESC LIMIT 1;
  IF v_status IN ('PREPARED','PUT_CONFIRMED') AND EXISTS(
    SELECT 1 FROM privacy_protected.object_upload_intent_holds h WHERE h.intent_id=v_intent AND NOT (h.released_at IS NULL AND h.held_until>v_now)
  ) THEN
   INSERT INTO privacy_protected.object_upload_intent_events VALUES(v_intent,v_sequence+1,'CLEANUP_REQUIRED','STALE_TIMEOUT',v_now);
   UPDATE privacy_protected.object_upload_intent_holds SET released_at=COALESCE(released_at,v_now),updated_at=v_now WHERE object_upload_intent_holds.intent_id=v_intent;
   INSERT INTO privacy_protected.object_upload_cleanup_jobs(intent_id,status,next_attempt_at,updated_at) VALUES(v_intent,'PENDING',v_now,v_now) ON CONFLICT ON CONSTRAINT object_upload_cleanup_jobs_pkey DO NOTHING;
  END IF;
 END IF;

 v_intent:=NULL;
 SELECT j.intent_id,i.source_kind,i.source_ref,j.lease_epoch,j.attempt_count
 INTO v_intent,v_source_kind,v_source_ref,v_epoch,v_attempt_count
 FROM privacy_protected.object_upload_cleanup_jobs j
 JOIN privacy_protected.object_upload_intents i ON i.id=j.intent_id
 LEFT JOIN LATERAL (
  SELECT a.expires_at,a.released_at FROM privacy_protected.object_upload_cleanup_attempts a
  WHERE a.intent_id=j.intent_id AND a.lease_epoch=j.lease_epoch ORDER BY a.acquired_at DESC LIMIT 1
 ) active_attempt ON true
 WHERE ((j.status IN ('PENDING','RETRY_WAIT') AND j.next_attempt_at<=v_now)
    OR (j.status='LEASED' AND active_attempt.released_at IS NULL AND active_attempt.expires_at<=v_now))
   AND NOT public.privacy_upload_pointer_references(i.id,i.source_kind,i.source_ref)
 ORDER BY j.next_attempt_at,j.intent_id FOR UPDATE OF j SKIP LOCKED LIMIT 1;
 IF v_intent IS NULL THEN RETURN; END IF;
 PERFORM public.privacy_upload_source_lock(v_source_kind,v_source_ref);
 IF public.privacy_upload_pointer_references(v_intent,v_source_kind,v_source_ref) THEN RETURN; END IF;
 SELECT e.status INTO v_status FROM privacy_protected.object_upload_intent_events e WHERE e.intent_id=v_intent ORDER BY e.sequence DESC LIMIT 1;
 IF v_status<>'CLEANUP_REQUIRED' THEN RAISE EXCEPTION 'cleanup claim state rejected'; END IF;
 UPDATE privacy_protected.object_upload_cleanup_attempts a SET released_at=v_now,outcome='LEASE_EXPIRED'
 WHERE a.intent_id=v_intent AND a.lease_epoch=v_epoch AND a.released_at IS NULL AND a.expires_at<=v_now;
 v_epoch:=v_epoch+1; v_attempt:=gen_random_uuid(); v_attempt_count:=v_attempt_count+1;
 INSERT INTO privacy_protected.object_upload_cleanup_attempts(id,intent_id,lease_epoch,worker_ref,acquired_at,expires_at)
 VALUES(v_attempt,v_intent,v_epoch,p_worker_ref,v_now,v_now+(p_lease_milliseconds*interval '1 millisecond'));
 UPDATE privacy_protected.object_upload_cleanup_jobs j SET status='LEASED',lease_epoch=v_epoch,attempt_count=v_attempt_count,updated_at=v_now WHERE j.intent_id=v_intent;
 RETURN QUERY SELECT i.id,v_epoch,v_attempt,v_attempt_count,i.subject_user_id,i.provenance_actor_user_id,i.source_kind::text,i.source_ref,
  i.service_code::text,i.target_kind::text,i.provider_contract_version::text,i.envelope_version::text,i.algorithm::text,
  i.encryption_key_id::text,i.encapsulation,i.nonce,i.ciphertext,i.content_type::text,i.size_bytes,i.cleanup_after,i.created_at
 FROM privacy_protected.object_upload_intents i WHERE i.id=v_intent;
END; $$;

CREATE FUNCTION public.privacy_upload_cleanup_complete(
 p_intent_id uuid,p_attempt_id uuid,p_lease_epoch bigint,p_worker_ref uuid,
 p_deleted_versions integer,p_deleted_markers integer,p_list_calls integer,p_stable_checks integer,
 p_transcript_key_id text,p_transcript_digest bytea
) RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE v_now timestamptz:=clock_timestamp(); v_status text; v_sequence integer; v_source_kind text; v_source_ref uuid;
BEGIN
 SELECT i.source_kind,i.source_ref INTO v_source_kind,v_source_ref FROM privacy_protected.object_upload_intents i WHERE i.id=p_intent_id FOR UPDATE;
 IF v_source_ref IS NULL THEN RAISE EXCEPTION 'cleanup completion rejected'; END IF;
 PERFORM public.privacy_upload_source_lock(v_source_kind,v_source_ref);
 PERFORM 1 FROM privacy_protected.object_upload_cleanup_jobs j
 JOIN privacy_protected.object_upload_cleanup_attempts a ON a.intent_id=j.intent_id AND a.lease_epoch=j.lease_epoch
 WHERE j.intent_id=p_intent_id AND j.status='LEASED' AND j.lease_epoch=p_lease_epoch
  AND a.id=p_attempt_id AND a.worker_ref=p_worker_ref AND a.released_at IS NULL AND a.expires_at>v_now FOR UPDATE OF j,a;
 IF NOT FOUND OR p_deleted_versions<0 OR p_deleted_markers<0 OR p_list_calls<2 OR p_stable_checks<2
   OR p_transcript_key_id IS NULL OR p_transcript_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$' OR octet_length(p_transcript_digest)<>32 THEN RAISE EXCEPTION 'cleanup completion rejected'; END IF;
 SELECT e.status,e.sequence INTO v_status,v_sequence FROM privacy_protected.object_upload_intent_events e WHERE e.intent_id=p_intent_id ORDER BY e.sequence DESC LIMIT 1;
 IF v_status<>'CLEANUP_REQUIRED' THEN RAISE EXCEPTION 'cleanup completion rejected'; END IF;
 INSERT INTO privacy_protected.object_upload_absence_evidence(intent_id,attempt_id,lease_epoch,evidence_version,deleted_version_count,deleted_marker_count,list_call_count,stable_empty_check_count,transcript_key_id,transcript_digest,occurred_at)
 VALUES(p_intent_id,p_attempt_id,p_lease_epoch,'s3-absence/v1',p_deleted_versions,p_deleted_markers,p_list_calls,p_stable_checks,p_transcript_key_id,p_transcript_digest,v_now);
 INSERT INTO privacy_protected.object_upload_intent_events VALUES(p_intent_id,v_sequence+1,'ABSENCE_VERIFIED','CLEANUP_CONFIRMED',v_now);
 UPDATE privacy_protected.object_upload_cleanup_attempts SET released_at=v_now,outcome='SUCCEEDED' WHERE id=p_attempt_id;
 UPDATE privacy_protected.object_upload_cleanup_jobs SET status='SUCCEEDED',updated_at=v_now WHERE object_upload_cleanup_jobs.intent_id=p_intent_id;
END; $$;

CREATE FUNCTION public.privacy_upload_cleanup_fail(
 p_intent_id uuid,p_attempt_id uuid,p_lease_epoch bigint,p_worker_ref uuid,p_retryable boolean,p_retry_delay_milliseconds bigint
) RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE v_now timestamptz:=clock_timestamp();
BEGIN
 PERFORM 1 FROM privacy_protected.object_upload_cleanup_jobs j
 JOIN privacy_protected.object_upload_cleanup_attempts a ON a.intent_id=j.intent_id AND a.lease_epoch=j.lease_epoch
 WHERE j.intent_id=p_intent_id AND j.status='LEASED' AND j.lease_epoch=p_lease_epoch
  AND a.id=p_attempt_id AND a.worker_ref=p_worker_ref AND a.released_at IS NULL AND a.expires_at>v_now FOR UPDATE OF j,a;
 IF NOT FOUND OR p_retry_delay_milliseconds<0 OR p_retry_delay_milliseconds>3600000 THEN RAISE EXCEPTION 'cleanup failure rejected'; END IF;
 UPDATE privacy_protected.object_upload_cleanup_attempts SET released_at=v_now,outcome=CASE WHEN p_retryable THEN 'RETRYABLE_FAILED' ELSE 'TERMINAL_FAILED' END WHERE id=p_attempt_id;
 UPDATE privacy_protected.object_upload_cleanup_jobs SET status=CASE WHEN p_retryable THEN 'RETRY_WAIT' ELSE 'TERMINAL_FAILED' END,
  next_attempt_at=v_now+(p_retry_delay_milliseconds*interval '1 millisecond'),updated_at=v_now WHERE object_upload_cleanup_jobs.intent_id=p_intent_id;
END; $$;

REVOKE ALL ON TABLE privacy_protected.object_upload_intent_reservations,privacy_protected.object_upload_intent_holds,
 privacy_protected.object_upload_cleanup_jobs,privacy_protected.object_upload_cleanup_attempts,privacy_protected.object_upload_absence_evidence FROM PUBLIC;
REVOKE ALL ON FUNCTION public.privacy_upload_source_lock(text,uuid),public.privacy_upload_pointer_references(uuid,text,uuid),
 public.privacy_upload_pointer_matches(uuid,text,uuid,bytea,text,bigint),public.privacy_upload_enforce_pointer_mutation(),public.privacy_upload_enforce_pointer_state(),
 public.privacy_upload_begin(uuid,uuid,uuid,text,uuid,text,text,bigint,bytea),
 public.privacy_upload_finalize(uuid,bytea,text,text,text,bytea,bytea,bytea,text,bytea,bytea),public.privacy_upload_confirm_put(uuid,bytea),
 public.privacy_upload_mark_cleanup(uuid,bytea,text),public.privacy_upload_attach(uuid,bytea,uuid,text,uuid,text,text,bigint),public.privacy_upload_remove(uuid,uuid,text,uuid),
 public.privacy_upload_cleanup_claim(bigint,uuid),public.privacy_upload_cleanup_complete(uuid,uuid,bigint,uuid,integer,integer,integer,integer,text,bytea),
 public.privacy_upload_cleanup_fail(uuid,uuid,bigint,uuid,boolean,bigint) FROM PUBLIC;
