-- Age-18 handoff for the existing dependent identity. No release gate is opened.

DO $$DECLARE definition text;rewritten text;
BEGIN
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint
 WHERE conrelid='privacy_activation_authenticated_artifacts'::regclass
  AND conname='privacy_activation_authenticated_artifacts_v11_check';
 IF definition IS NULL OR length(definition)-length(replace(definition,'202609120003_guardian_authority_renewal',''))<>length('202609120003_guardian_authority_renewal') THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_age_handoff_activation_constraint_predecessor_mismatch';
 END IF;
 rewritten:=replace(definition,
  '''202609120003_guardian_authority_renewal''',
  '''202609120003_guardian_authority_renewal'', ''202609120004_guardian_age_handoff''');
 ALTER TABLE privacy_activation_authenticated_artifacts DROP CONSTRAINT privacy_activation_authenticated_artifacts_v11_check;
 EXECUTE 'ALTER TABLE privacy_activation_authenticated_artifacts ADD CONSTRAINT privacy_activation_authenticated_artifacts_v12_check '||rewritten||' NOT VALID';
 ALTER TABLE privacy_activation_authenticated_artifacts VALIDATE CONSTRAINT privacy_activation_authenticated_artifacts_v12_check;
END$$;

DO $$DECLARE definition text;old_clause text;new_clause text;
BEGIN
 SELECT pg_get_functiondef('public.privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)'::regprocedure) INTO definition;
 old_clause:='p_artifact->>''baseline_includes_through''<>''202609120003_guardian_authority_renewal''';
 new_clause:='p_artifact->>''baseline_includes_through''<>''202609120004_guardian_age_handoff''';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_age_handoff_activation_evidence_predecessor_mismatch'; END IF;
 EXECUTE replace(definition,old_clause,new_clause);

 SELECT pg_get_functiondef('public.privacy_activation_authenticated_set_digest(text,uuid[])'::regprocedure) INTO definition;
 old_clause:='schema_row.baseline_includes_through=''202609120003_guardian_authority_renewal''';
 new_clause:='schema_row.baseline_includes_through=''202609120004_guardian_age_handoff''';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_age_handoff_activation_digest_predecessor_mismatch'; END IF;
 EXECUTE replace(definition,old_clause,new_clause);
END$$;

CREATE TABLE guardian_age_handoffs(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 public_ref uuid NOT NULL DEFAULT gen_random_uuid() UNIQUE,
 subject_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT UNIQUE,
 status text NOT NULL DEFAULT 'PENDING' CHECK(status IN('PENDING','EMAIL_PENDING','EMAIL_VERIFIED','IDENTITY_CONFIRMED','READY','RECOVERY_REQUIRED','EMAIL_COLLISION','COMPLETED')),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 proposed_email citext NULL,
 email_verified_at timestamptz NULL,
 identity_method text NULL CHECK(identity_method IS NULL OR identity_method='CLUB_REGISTRATION_RECORD'),
 identity_confirmed_by uuid NULL REFERENCES users(id) ON DELETE RESTRICT,
 identity_confirmed_at timestamptz NULL,
 ready_at timestamptz NULL,
 recovery_required_at timestamptz NULL,
 completed_at timestamptz NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK((identity_confirmed_by IS NULL)=(identity_confirmed_at IS NULL)),
 CHECK((identity_method IS NULL)=(identity_confirmed_at IS NULL)),
 CHECK(email_verified_at IS NULL OR proposed_email IS NOT NULL),
 CHECK(ready_at IS NULL OR (email_verified_at IS NOT NULL AND identity_confirmed_at IS NOT NULL)),
 CHECK(completed_at IS NULL OR status='COMPLETED')
);
CREATE UNIQUE INDEX guardian_age_handoffs_active_email_uidx ON guardian_age_handoffs(proposed_email)
 WHERE proposed_email IS NOT NULL AND completed_at IS NULL;
CREATE INDEX guardian_age_handoffs_status_idx ON guardian_age_handoffs(status,updated_at,id);

CREATE TABLE guardian_age_handoff_events(
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 handoff_id uuid NOT NULL REFERENCES guardian_age_handoffs(id) ON DELETE RESTRICT,
 handoff_version bigint NOT NULL CHECK(handoff_version>0),
 actor_ref uuid NULL,
 actor_role text NOT NULL CHECK(actor_role IN('YOUNG_PERSON','ADMIN','SYSTEM')),
 action text NOT NULL CHECK(action IN('STARTED','EMAIL_PROPOSED','EMAIL_VERIFIED','IDENTITY_CONFIRMED','READY','RECOVERY_REQUIRED','EMAIL_COLLISION','COMPLETED')),
 method text NULL CHECK(method IS NULL OR method='CLUB_REGISTRATION_RECORD'),
 occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK((actor_role='SYSTEM' AND actor_ref IS NULL) OR (actor_role<>'SYSTEM' AND actor_ref IS NOT NULL)),
 UNIQUE(handoff_id,handoff_version)
);
CREATE INDEX guardian_age_handoff_events_latest_idx ON guardian_age_handoff_events(handoff_id,id DESC);

CREATE TABLE guardian_age_handoff_email_tokens(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 handoff_id uuid NOT NULL REFERENCES guardian_age_handoffs(id) ON DELETE RESTRICT,
 token_digest bytea NOT NULL UNIQUE CHECK(octet_length(token_digest)=32),
 proposed_email citext NOT NULL,
 expires_at timestamptz NOT NULL,
 consumed_at timestamptz NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK(expires_at>created_at),CHECK(consumed_at IS NULL OR consumed_at>=created_at)
);
CREATE UNIQUE INDEX guardian_age_handoff_email_tokens_active_uidx ON guardian_age_handoff_email_tokens(handoff_id)
 WHERE consumed_at IS NULL;

CREATE TABLE guardian_age_handoff_notices(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 handoff_id uuid NOT NULL REFERENCES guardian_age_handoffs(id) ON DELETE RESTRICT,
 relationship_id uuid NULL REFERENCES guardian_authority_relationships(id) ON DELETE RESTRICT,
 guardian_user_id uuid NULL REFERENCES users(id) ON DELETE RESTRICT,
 recipient_verified_at timestamptz NULL,
 audience text NOT NULL CHECK(audience IN('GUARDIAN_EMAIL','YOUNG_PERSON_IN_APP')),
 notice_kind text NOT NULL CHECK(notice_kind IN('GUARDIAN_AGE_18_30_DAY','GUARDIAN_AGE_18_7_DAY')),
 birthday date NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK((audience='GUARDIAN_EMAIL' AND relationship_id IS NOT NULL AND guardian_user_id IS NOT NULL AND recipient_verified_at IS NOT NULL)
   OR (audience='YOUNG_PERSON_IN_APP' AND relationship_id IS NULL AND guardian_user_id IS NULL AND recipient_verified_at IS NULL))
);
CREATE UNIQUE INDEX guardian_age_handoff_young_notice_uidx ON guardian_age_handoff_notices(handoff_id,birthday,notice_kind)
 WHERE audience='YOUNG_PERSON_IN_APP';
CREATE UNIQUE INDEX guardian_age_handoff_guardian_notice_uidx ON guardian_age_handoff_notices(relationship_id,birthday,notice_kind)
 WHERE audience='GUARDIAN_EMAIL';

CREATE TABLE guardian_age_handoff_access_events(
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 handoff_id uuid NULL REFERENCES guardian_age_handoffs(id) ON DELETE RESTRICT,
 actor_ref uuid NOT NULL,
 view_kind text NOT NULL CHECK(view_kind IN('QUEUE','DETAIL')),
 occurred_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TRIGGER guardian_age_handoff_events_immutable BEFORE UPDATE OR DELETE ON guardian_age_handoff_events
 FOR EACH ROW EXECUTE FUNCTION prevent_guardian_authority_event_mutation();
CREATE TRIGGER guardian_age_handoff_notices_immutable BEFORE UPDATE OR DELETE ON guardian_age_handoff_notices
 FOR EACH ROW EXECUTE FUNCTION prevent_guardian_authority_event_mutation();
CREATE TRIGGER guardian_age_handoff_access_events_immutable BEFORE UPDATE OR DELETE ON guardian_age_handoff_access_events
 FOR EACH ROW EXECUTE FUNCTION prevent_guardian_authority_event_mutation();

ALTER TABLE email_outbox ADD COLUMN guardian_handoff_notice_id uuid NULL REFERENCES guardian_age_handoff_notices(id) ON DELETE RESTRICT;
ALTER TABLE email_outbox ADD COLUMN guardian_handoff_email_token_id uuid NULL REFERENCES guardian_age_handoff_email_tokens(id) ON DELETE RESTRICT;
CREATE UNIQUE INDEX email_outbox_guardian_handoff_notice_uidx ON email_outbox(guardian_handoff_notice_id) WHERE guardian_handoff_notice_id IS NOT NULL;
CREATE UNIQUE INDEX email_outbox_guardian_handoff_token_uidx ON email_outbox(guardian_handoff_email_token_id) WHERE guardian_handoff_email_token_id IS NOT NULL;
ALTER TABLE email_outbox DROP CONSTRAINT email_outbox_message_valid;
ALTER TABLE email_outbox ADD CONSTRAINT email_outbox_message_valid CHECK(
 (message_type='EMAIL_VERIFICATION' AND verification_token_id IS NOT NULL AND password_reset_token_id IS NULL AND sealed_payload IS NULL AND privacy_request_id IS NULL AND privacy_requester_id IS NULL AND privacy_event_key IS NULL AND guardian_reminder_id IS NULL AND guardian_handoff_notice_id IS NULL AND guardian_handoff_email_token_id IS NULL)
 OR (message_type='PASSWORD_RESET' AND verification_token_id IS NULL AND password_reset_token_id IS NOT NULL AND sealed_payload IS NOT NULL AND privacy_request_id IS NULL AND privacy_requester_id IS NULL AND privacy_event_key IS NULL AND guardian_reminder_id IS NULL AND guardian_handoff_notice_id IS NULL AND guardian_handoff_email_token_id IS NULL)
 OR (message_type IN('PRIVACY_ACKNOWLEDGEMENT','PRIVACY_DECISION','PRIVACY_PROCESSING_STARTED','PRIVACY_COMPLETED') AND verification_token_id IS NULL AND password_reset_token_id IS NULL AND sealed_payload IS NOT NULL AND privacy_request_id IS NOT NULL AND privacy_event_key IS NOT NULL AND guardian_reminder_id IS NULL AND guardian_handoff_notice_id IS NULL AND guardian_handoff_email_token_id IS NULL)
 OR (message_type IN('GUARDIAN_RENEWAL_30_DAY','GUARDIAN_RENEWAL_7_DAY') AND verification_token_id IS NULL AND password_reset_token_id IS NULL AND sealed_payload IS NOT NULL AND privacy_request_id IS NULL AND privacy_requester_id IS NULL AND privacy_event_key IS NULL AND guardian_reminder_id IS NOT NULL AND guardian_handoff_notice_id IS NULL AND guardian_handoff_email_token_id IS NULL)
 OR (message_type IN('GUARDIAN_AGE_18_30_DAY','GUARDIAN_AGE_18_7_DAY') AND verification_token_id IS NULL AND password_reset_token_id IS NULL AND sealed_payload IS NOT NULL AND privacy_request_id IS NULL AND privacy_requester_id IS NULL AND privacy_event_key IS NULL AND guardian_reminder_id IS NULL AND guardian_handoff_notice_id IS NOT NULL AND guardian_handoff_email_token_id IS NULL)
 OR (message_type='GUARDIAN_AGE_18_EMAIL_VERIFY' AND verification_token_id IS NULL AND password_reset_token_id IS NULL AND sealed_payload IS NOT NULL AND privacy_request_id IS NULL AND privacy_requester_id IS NULL AND privacy_event_key IS NULL AND guardian_reminder_id IS NULL AND guardian_handoff_notice_id IS NULL AND guardian_handoff_email_token_id IS NOT NULL)
);

CREATE FUNCTION guardian_age_handoff_reconcile() RETURNS bigint
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE item record;relationship guardian_authority_relationships%ROWTYPE;now_at timestamptz:=clock_timestamp();today date:=(clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date;changed bigint:=0;next_status text;
BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended('mycfc/guardian-age-handoff/v1',0));
 WITH inserted AS(
 INSERT INTO guardian_age_handoffs(subject_user_id,status,created_at,updated_at)
  SELECT subject.id,'PENDING',now_at,now_at FROM users subject
  WHERE subject.is_active AND subject.erased_at IS NULL AND subject.is_dependent
   AND subject.minor_login_id IS NOT NULL AND subject.password_hash IS NOT NULL
   AND (subject.date_of_birth+interval '18 years')::date<=today+30
   AND EXISTS(SELECT 1 FROM guardian_authority_relationships authority
    JOIN users guardian ON guardian.id=authority.guardian_user_id
    JOIN guardian_authority_policies policy ON policy.version=authority.policy_version
    WHERE authority.subject_user_id=subject.id AND authority.state='VERIFIED' AND NOT authority.conflict
     AND policy.enabled AND policy.adopted_at<=now_at AND authority.verified_at<=now_at
     AND authority.review_due_at>now_at AND authority.verified_until>now_at
     AND guardian.is_active AND guardian.erased_at IS NULL AND NOT guardian.is_dependent
     AND guardian.date_of_birth<=(today-interval '18 years')::date)
  ON CONFLICT(subject_user_id) DO NOTHING RETURNING id,subject_user_id,version,created_at)
 INSERT INTO guardian_age_handoff_events(handoff_id,handoff_version,actor_role,action,occurred_at)
 SELECT id,version,'SYSTEM','STARTED',created_at FROM inserted;

 INSERT INTO guardian_age_handoff_notices(handoff_id,audience,notice_kind,birthday,created_at)
 SELECT handoff.id,'YOUNG_PERSON_IN_APP',
  CASE WHEN (subject.date_of_birth+interval '18 years')::date<=today+7 THEN 'GUARDIAN_AGE_18_7_DAY' ELSE 'GUARDIAN_AGE_18_30_DAY' END,
  (subject.date_of_birth+interval '18 years')::date,now_at
 FROM guardian_age_handoffs handoff JOIN users subject ON subject.id=handoff.subject_user_id
 WHERE handoff.status<>'COMPLETED' AND subject.is_active AND subject.is_dependent
  AND (subject.date_of_birth+interval '18 years')::date>today AND (subject.date_of_birth+interval '18 years')::date<=today+30
  AND EXISTS(SELECT 1 FROM guardian_authority_relationships authority WHERE authority.subject_user_id=subject.id AND guardian_authority_current(authority.guardian_user_id,subject.id))
 ON CONFLICT DO NOTHING;

 FOR item IN SELECT handoff.*,subject.date_of_birth FROM guardian_age_handoffs handoff JOIN users subject ON subject.id=handoff.subject_user_id
  WHERE handoff.status<>'COMPLETED' AND subject.is_dependent AND (subject.date_of_birth+interval '18 years')::date<=today FOR UPDATE OF handoff,subject
 LOOP
  next_status:=CASE WHEN item.proposed_email IS NOT NULL AND EXISTS(SELECT 1 FROM users collision WHERE collision.email=item.proposed_email AND collision.id<>item.subject_user_id)
    THEN 'EMAIL_COLLISION' WHEN item.email_verified_at IS NOT NULL AND item.identity_confirmed_at IS NOT NULL THEN 'COMPLETED' ELSE 'RECOVERY_REQUIRED' END;
  FOR relationship IN SELECT * FROM guardian_authority_relationships WHERE subject_user_id=item.subject_user_id AND state IN('PENDING','VERIFIED','SUSPENDED') FOR UPDATE
  LOOP
   UPDATE guardian_authority_relationships SET state='EXPIRED',version=relationship.version+1,conflict=false,conflict_actor_ref=NULL,updated_at=now_at WHERE id=relationship.id;
   INSERT INTO guardian_authority_events(relationship_id,relationship_version,actor_ref,actor_role,action,from_state,to_state,policy_version,reason_code,occurred_at,verified_until,review_due_at)
   VALUES(relationship.id,relationship.version+1,NULL,'SYSTEM','EXPIRED',relationship.state,'EXPIRED',relationship.policy_version,'MAJORITY_REACHED',now_at,relationship.verified_until,relationship.review_due_at);
  END LOOP;
  DELETE FROM sessions WHERE subject_indexed AND user_id=item.subject_user_id;
  IF next_status='COMPLETED' THEN
   BEGIN
    UPDATE users SET email=item.proposed_email,email_verified_at=item.email_verified_at,is_dependent=false,guardian_id=NULL,minor_login_id=NULL,
     credential_version=credential_version+1,updated_at=now_at WHERE id=item.subject_user_id;
   EXCEPTION WHEN unique_violation THEN next_status:='EMAIL_COLLISION'; END;
  ELSIF item.status NOT IN('RECOVERY_REQUIRED','EMAIL_COLLISION') THEN
   UPDATE users SET credential_version=credential_version+1,updated_at=now_at WHERE id=item.subject_user_id;
  END IF;
  IF next_status<>item.status THEN
   UPDATE guardian_age_handoffs SET status=next_status,version=version+1,recovery_required_at=CASE WHEN next_status IN('RECOVERY_REQUIRED','EMAIL_COLLISION') THEN COALESCE(recovery_required_at,now_at) ELSE recovery_required_at END,
    completed_at=CASE WHEN next_status='COMPLETED' THEN now_at ELSE completed_at END,updated_at=now_at WHERE id=item.id;
   INSERT INTO guardian_age_handoff_events(handoff_id,handoff_version,actor_role,action,occurred_at)
   VALUES(item.id,item.version+1,'SYSTEM',next_status,now_at);
   changed:=changed+1;
  END IF;
 END LOOP;
 RETURN changed;
END;$$;

CREATE FUNCTION guardian_age_handoff_propose_email(p_actor_id uuid,p_expected_version bigint,p_email citext,p_token_digest bytea,p_expires_at timestamptz,p_sealed_payload bytea)
RETURNS SETOF guardian_age_handoffs LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE handoff guardian_age_handoffs%ROWTYPE;subject users%ROWTYPE;token_id uuid;now_at timestamptz:=clock_timestamp();today date:=(clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date;
BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended('mycfc/guardian-age-handoff/v1',0));
 SELECT * INTO handoff FROM guardian_age_handoffs WHERE subject_user_id=p_actor_id FOR UPDATE;
 SELECT * INTO subject FROM users WHERE id=p_actor_id FOR UPDATE;
 IF handoff.id IS NULL OR handoff.version<>p_expected_version THEN RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='guardian_age_handoff_stale'; END IF;
 IF NOT subject.is_active OR NOT subject.is_dependent OR subject.erased_at IS NOT NULL OR subject.minor_login_id IS NULL OR subject.password_hash IS NULL
  OR (subject.date_of_birth+interval '18 years')::date<=today OR (subject.date_of_birth+interval '18 years')::date>today+30
  OR octet_length(p_token_digest)<>32 OR p_expires_at<=now_at+interval '23 hours' OR p_expires_at>now_at+interval '24 hours 5 minutes'
  OR octet_length(p_sealed_payload)<32 THEN RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='guardian_age_handoff_not_available'; END IF;
 IF EXISTS(SELECT 1 FROM users account WHERE account.email=p_email AND account.id<>subject.id)
  OR EXISTS(SELECT 1 FROM guardian_age_handoffs other WHERE other.proposed_email=p_email AND other.id<>handoff.id AND other.completed_at IS NULL)
  THEN RAISE EXCEPTION USING ERRCODE='23505',MESSAGE='guardian_age_handoff_email_collision'; END IF;
 UPDATE email_outbox SET status='CANCELLED',claimed_at=NULL,updated_at=now_at WHERE guardian_handoff_email_token_id IN
  (SELECT id FROM guardian_age_handoff_email_tokens WHERE handoff_id=handoff.id AND consumed_at IS NULL) AND status IN('PENDING','SENDING');
 UPDATE guardian_age_handoff_email_tokens SET consumed_at=GREATEST(now_at,created_at) WHERE handoff_id=handoff.id AND consumed_at IS NULL;
 INSERT INTO guardian_age_handoff_email_tokens(handoff_id,token_digest,proposed_email,expires_at,created_at)
 VALUES(handoff.id,p_token_digest,p_email,p_expires_at,now_at) RETURNING id INTO token_id;
 UPDATE guardian_age_handoffs SET proposed_email=p_email,email_verified_at=NULL,ready_at=NULL,status='EMAIL_PENDING',version=version+1,updated_at=now_at WHERE id=handoff.id RETURNING * INTO handoff;
 INSERT INTO guardian_age_handoff_events(handoff_id,handoff_version,actor_ref,actor_role,action,occurred_at)
 VALUES(handoff.id,handoff.version,p_actor_id,'YOUNG_PERSON','EMAIL_PROPOSED',now_at);
 INSERT INTO email_outbox(message_type,sealed_payload,guardian_handoff_email_token_id,next_attempt_at,created_at,updated_at)
 VALUES('GUARDIAN_AGE_18_EMAIL_VERIFY',p_sealed_payload,token_id,now_at,now_at,now_at);
 RETURN NEXT handoff;
END;$$;

CREATE FUNCTION guardian_age_handoff_verify_email(p_token_digest bytea) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE token guardian_age_handoff_email_tokens%ROWTYPE;handoff guardian_age_handoffs%ROWTYPE;now_at timestamptz:=clock_timestamp();next_status text;
BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended('mycfc/guardian-age-handoff/v1',0));
 SELECT * INTO token FROM guardian_age_handoff_email_tokens WHERE token_digest=p_token_digest FOR UPDATE;
 IF NOT FOUND OR token.consumed_at IS NOT NULL OR token.expires_at<=now_at THEN RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='guardian_age_handoff_token_invalid'; END IF;
 SELECT * INTO handoff FROM guardian_age_handoffs WHERE id=token.handoff_id FOR UPDATE;
 IF handoff.status='COMPLETED' OR handoff.proposed_email IS DISTINCT FROM token.proposed_email
  OR EXISTS(SELECT 1 FROM users account WHERE account.email=token.proposed_email AND account.id<>handoff.subject_user_id)
  THEN RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='guardian_age_handoff_token_invalid'; END IF;
 UPDATE guardian_age_handoff_email_tokens SET consumed_at=now_at WHERE id=token.id;
 next_status:=CASE WHEN handoff.identity_confirmed_at IS NOT NULL THEN 'READY' ELSE 'EMAIL_VERIFIED' END;
 UPDATE guardian_age_handoffs SET email_verified_at=now_at,ready_at=CASE WHEN next_status='READY' THEN now_at ELSE NULL END,
  status=next_status,version=version+1,updated_at=now_at WHERE id=handoff.id RETURNING * INTO handoff;
 INSERT INTO guardian_age_handoff_events(handoff_id,handoff_version,actor_ref,actor_role,action,occurred_at)
 VALUES(handoff.id,handoff.version,handoff.subject_user_id,'YOUNG_PERSON','EMAIL_VERIFIED',now_at);
 RETURN handoff.subject_user_id;
END;$$;

CREATE FUNCTION guardian_age_handoff_admin_confirm(p_actor_id uuid,p_public_ref uuid,p_expected_version bigint)
RETURNS SETOF guardian_age_handoffs LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE handoff guardian_age_handoffs%ROWTYPE;relationship_ref uuid;now_at timestamptz:=clock_timestamp();next_status text;
BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended('mycfc/guardian-age-handoff/v1',0));
 IF NOT guardian_authority_is_administrator(p_actor_id) THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='guardian_age_handoff_administrator_required'; END IF;
 SELECT * INTO handoff FROM guardian_age_handoffs WHERE public_ref=p_public_ref FOR UPDATE;
 IF NOT FOUND OR handoff.version<>p_expected_version THEN RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='guardian_age_handoff_stale'; END IF;
 SELECT public_ref INTO relationship_ref FROM guardian_authority_relationships WHERE subject_user_id=handoff.subject_user_id ORDER BY created_at LIMIT 1;
 IF p_actor_id=handoff.subject_user_id OR relationship_ref IS NULL OR guardian_authority_personally_involved(p_actor_id,relationship_ref)
  THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='guardian_age_handoff_separation_required'; END IF;
 IF handoff.status='COMPLETED' OR handoff.identity_confirmed_at IS NOT NULL
  THEN RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='guardian_age_handoff_transition_rejected'; END IF;
 next_status:=CASE WHEN handoff.status IN('RECOVERY_REQUIRED','EMAIL_COLLISION') THEN handoff.status WHEN handoff.email_verified_at IS NOT NULL THEN 'READY' ELSE 'IDENTITY_CONFIRMED' END;
 UPDATE guardian_age_handoffs SET identity_method='CLUB_REGISTRATION_RECORD',identity_confirmed_by=p_actor_id,identity_confirmed_at=now_at,
  ready_at=CASE WHEN next_status='READY' THEN now_at ELSE NULL END,status=next_status,version=version+1,updated_at=now_at WHERE id=handoff.id RETURNING * INTO handoff;
 INSERT INTO guardian_age_handoff_events(handoff_id,handoff_version,actor_ref,actor_role,action,method,occurred_at)
 VALUES(handoff.id,handoff.version,p_actor_id,'ADMIN','IDENTITY_CONFIRMED','CLUB_REGISTRATION_RECORD',now_at);
 RETURN NEXT handoff;
END;$$;

CREATE FUNCTION guardian_age_handoff_admin_recovery_email(p_actor_id uuid,p_public_ref uuid,p_expected_version bigint,p_email citext,p_token_digest bytea,p_expires_at timestamptz,p_sealed_payload bytea)
RETURNS SETOF guardian_age_handoffs LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE handoff guardian_age_handoffs%ROWTYPE;token_id uuid;relationship_ref uuid;now_at timestamptz:=clock_timestamp();
BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended('mycfc/guardian-age-handoff/v1',0));
 IF NOT guardian_authority_is_administrator(p_actor_id) THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='guardian_age_handoff_administrator_required'; END IF;
 SELECT * INTO handoff FROM guardian_age_handoffs WHERE public_ref=p_public_ref FOR UPDATE;
 IF NOT FOUND OR handoff.version<>p_expected_version THEN RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='guardian_age_handoff_stale'; END IF;
 SELECT public_ref INTO relationship_ref FROM guardian_authority_relationships WHERE subject_user_id=handoff.subject_user_id ORDER BY created_at LIMIT 1;
 IF relationship_ref IS NULL OR guardian_authority_personally_involved(p_actor_id,relationship_ref) OR handoff.status NOT IN('RECOVERY_REQUIRED','EMAIL_COLLISION')
  OR handoff.identity_confirmed_at IS NULL OR octet_length(p_token_digest)<>32 OR p_expires_at<=now_at+interval '23 hours'
  OR p_expires_at>now_at+interval '24 hours 5 minutes' OR octet_length(p_sealed_payload)<32
  THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='guardian_age_handoff_recovery_rejected'; END IF;
 IF EXISTS(SELECT 1 FROM users account WHERE account.email=p_email AND account.id<>handoff.subject_user_id)
  OR EXISTS(SELECT 1 FROM guardian_age_handoffs other WHERE other.proposed_email=p_email AND other.id<>handoff.id AND other.completed_at IS NULL)
  THEN RAISE EXCEPTION USING ERRCODE='23505',MESSAGE='guardian_age_handoff_email_collision'; END IF;
 UPDATE email_outbox SET status='CANCELLED',claimed_at=NULL,updated_at=now_at WHERE guardian_handoff_email_token_id IN
  (SELECT id FROM guardian_age_handoff_email_tokens WHERE handoff_id=handoff.id AND consumed_at IS NULL) AND status IN('PENDING','SENDING');
 UPDATE guardian_age_handoff_email_tokens SET consumed_at=GREATEST(now_at,created_at) WHERE handoff_id=handoff.id AND consumed_at IS NULL;
 INSERT INTO guardian_age_handoff_email_tokens(handoff_id,token_digest,proposed_email,expires_at,created_at)
 VALUES(handoff.id,p_token_digest,p_email,p_expires_at,now_at) RETURNING id INTO token_id;
 UPDATE guardian_age_handoffs SET proposed_email=p_email,email_verified_at=NULL,ready_at=NULL,status='EMAIL_PENDING',version=version+1,updated_at=now_at WHERE id=handoff.id RETURNING * INTO handoff;
 INSERT INTO guardian_age_handoff_events(handoff_id,handoff_version,actor_ref,actor_role,action,method,occurred_at)
 VALUES(handoff.id,handoff.version,p_actor_id,'ADMIN','EMAIL_PROPOSED','CLUB_REGISTRATION_RECORD',now_at);
 INSERT INTO email_outbox(message_type,sealed_payload,guardian_handoff_email_token_id,next_attempt_at,created_at,updated_at)
 VALUES('GUARDIAN_AGE_18_EMAIL_VERIFY',p_sealed_payload,token_id,now_at,now_at,now_at);
 RETURN NEXT handoff;
END;$$;

CREATE FUNCTION guardian_age_handoff_for_subject(p_actor_id uuid)
RETURNS TABLE(public_ref uuid,status text,version bigint,birthday date,proposed_email text,email_verified_at timestamptz,identity_confirmed_at timestamptz,ready_at timestamptz,recovery_required_at timestamptz,completed_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT handoff.public_ref,handoff.status,handoff.version,(subject.date_of_birth+interval '18 years')::date,handoff.proposed_email::text,
  handoff.email_verified_at,handoff.identity_confirmed_at,handoff.ready_at,handoff.recovery_required_at,handoff.completed_at
 FROM guardian_age_handoffs handoff JOIN users subject ON subject.id=handoff.subject_user_id
 WHERE handoff.subject_user_id=p_actor_id
$$;

CREATE FUNCTION guardian_age_handoff_available_for_subject(p_subject_id uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT EXISTS(SELECT 1 FROM guardian_age_handoffs handoff WHERE handoff.subject_user_id=p_subject_id AND handoff.status<>'COMPLETED')
$$;

CREATE FUNCTION guardian_age_handoff_notices_for_subject(p_actor_id uuid)
RETURNS TABLE(notice_kind text,birthday date,created_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT notice.notice_kind,notice.birthday,notice.created_at FROM guardian_age_handoff_notices notice
 JOIN guardian_age_handoffs handoff ON handoff.id=notice.handoff_id
 WHERE handoff.subject_user_id=p_actor_id AND notice.audience='YOUNG_PERSON_IN_APP' ORDER BY notice.birthday,notice.notice_kind
$$;

CREATE FUNCTION guardian_age_handoff_for_guardian(p_actor_id uuid,p_subject_id uuid)
RETURNS TABLE(status text,birthday date)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT handoff.status,(subject.date_of_birth+interval '18 years')::date
 FROM guardian_age_handoffs handoff JOIN users subject ON subject.id=handoff.subject_user_id
 WHERE handoff.subject_user_id=p_subject_id
  AND EXISTS(SELECT 1 FROM guardian_authority_relationships relationship
   WHERE relationship.guardian_user_id=p_actor_id AND relationship.subject_user_id=p_subject_id)
$$;

CREATE FUNCTION guardian_age_handoff_list_for_admin(p_actor_id uuid,p_limit integer,p_offset integer)
RETURNS TABLE(public_ref uuid,subject_name text,status text,version bigint,birthday date,updated_at timestamptz)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
 IF NOT guardian_authority_is_administrator(p_actor_id) THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='guardian_age_handoff_administrator_required'; END IF;
 INSERT INTO guardian_age_handoff_access_events(actor_ref,view_kind) VALUES(p_actor_id,'QUEUE');
 RETURN QUERY SELECT handoff.public_ref,subject.name::text,handoff.status,handoff.version,(subject.date_of_birth+interval '18 years')::date,handoff.updated_at
  FROM guardian_age_handoffs handoff JOIN users subject ON subject.id=handoff.subject_user_id
  WHERE handoff.status<>'COMPLETED' ORDER BY handoff.updated_at,handoff.id LIMIT LEAST(GREATEST(p_limit,1),100) OFFSET GREATEST(p_offset,0);
END;$$;

CREATE FUNCTION guardian_age_handoff_get_for_admin(p_actor_id uuid,p_public_ref uuid)
RETURNS TABLE(public_ref uuid,subject_name text,status text,version bigint,birthday date,proposed_email text,email_verified_at timestamptz,identity_confirmed_at timestamptz,ready_at timestamptz,recovery_required_at timestamptz,completed_at timestamptz,personal_involvement boolean)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE handoff_id uuid;relationship_ref uuid;
BEGIN
 IF NOT guardian_authority_is_administrator(p_actor_id) THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='guardian_age_handoff_administrator_required'; END IF;
 SELECT handoff.id INTO handoff_id FROM guardian_age_handoffs handoff WHERE handoff.public_ref=p_public_ref;
 IF handoff_id IS NULL THEN RETURN; END IF;
 INSERT INTO guardian_age_handoff_access_events(handoff_id,actor_ref,view_kind) VALUES(handoff_id,p_actor_id,'DETAIL');
 SELECT relationship.public_ref INTO relationship_ref FROM guardian_authority_relationships relationship JOIN guardian_age_handoffs handoff ON handoff.subject_user_id=relationship.subject_user_id WHERE handoff.id=handoff_id ORDER BY relationship.created_at LIMIT 1;
 RETURN QUERY SELECT handoff.public_ref,subject.name::text,handoff.status,handoff.version,(subject.date_of_birth+interval '18 years')::date,handoff.proposed_email::text,
  handoff.email_verified_at,handoff.identity_confirmed_at,handoff.ready_at,handoff.recovery_required_at,handoff.completed_at,
  COALESCE(guardian_authority_personally_involved(p_actor_id,relationship_ref),true)
 FROM guardian_age_handoffs handoff JOIN users subject ON subject.id=handoff.subject_user_id WHERE handoff.id=handoff_id;
END;$$;

CREATE FUNCTION guardian_age_handoff_due_guardian_notices(p_limit integer)
RETURNS TABLE(handoff_ref uuid,relationship_ref uuid,guardian_user_id uuid,recipient text,recipient_verified_at timestamptz,birthday date,notice_kind text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 WITH today_value AS(SELECT (clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date AS value),candidate AS(
  SELECT handoff.id handoff_id,handoff.public_ref,relationship.id relationship_id,relationship.public_ref relationship_ref,
   guardian.id guardian_user_id,guardian.email::text recipient,guardian.email_verified_at,(subject.date_of_birth+interval '18 years')::date birthday,
   CASE WHEN (subject.date_of_birth+interval '18 years')::date<=(SELECT value FROM today_value)+7 THEN 'GUARDIAN_AGE_18_7_DAY' ELSE 'GUARDIAN_AGE_18_30_DAY' END notice_kind
  FROM guardian_age_handoffs handoff JOIN users subject ON subject.id=handoff.subject_user_id
  JOIN guardian_authority_relationships relationship ON relationship.subject_user_id=subject.id
  JOIN users guardian ON guardian.id=relationship.guardian_user_id
  WHERE handoff.status<>'COMPLETED' AND subject.is_active AND subject.is_dependent
   AND (subject.date_of_birth+interval '18 years')::date>(SELECT value FROM today_value)
   AND (subject.date_of_birth+interval '18 years')::date<=(SELECT value FROM today_value)+30
   AND guardian.is_active AND guardian.erased_at IS NULL AND NOT guardian.is_dependent AND guardian.email IS NOT NULL AND guardian.email_verified_at IS NOT NULL
   AND guardian_authority_current(guardian.id,subject.id))
 SELECT candidate.public_ref,candidate.relationship_ref,candidate.guardian_user_id,candidate.recipient,candidate.email_verified_at,candidate.birthday,candidate.notice_kind
 FROM candidate WHERE NOT EXISTS(SELECT 1 FROM guardian_age_handoff_notices notice WHERE notice.relationship_id=candidate.relationship_id AND notice.birthday=candidate.birthday AND notice.notice_kind=candidate.notice_kind)
 ORDER BY candidate.birthday,candidate.relationship_id LIMIT LEAST(GREATEST(p_limit,1),100)
$$;

CREATE FUNCTION guardian_age_handoff_enqueue_guardian_notice(p_handoff_ref uuid,p_relationship_ref uuid,p_guardian_user_id uuid,p_recipient citext,p_recipient_verified_at timestamptz,p_birthday date,p_notice_kind text,p_sealed_payload bytea)
RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE handoff guardian_age_handoffs%ROWTYPE;relationship guardian_authority_relationships%ROWTYPE;guardian users%ROWTYPE;subject users%ROWTYPE;notice_id uuid;today date:=(clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date;expected_kind text;
BEGIN
 IF p_notice_kind NOT IN('GUARDIAN_AGE_18_30_DAY','GUARDIAN_AGE_18_7_DAY') OR octet_length(p_sealed_payload)<32 THEN RETURN false; END IF;
 SELECT * INTO handoff FROM guardian_age_handoffs WHERE public_ref=p_handoff_ref FOR UPDATE;
 SELECT * INTO relationship FROM guardian_authority_relationships WHERE public_ref=p_relationship_ref FOR UPDATE;
 SELECT * INTO guardian FROM users WHERE id=p_guardian_user_id FOR SHARE;
 SELECT * INTO subject FROM users WHERE id=handoff.subject_user_id FOR SHARE;
 expected_kind:=CASE WHEN p_birthday<=today+7 THEN 'GUARDIAN_AGE_18_7_DAY' ELSE 'GUARDIAN_AGE_18_30_DAY' END;
 IF handoff.id IS NULL OR relationship.id IS NULL OR guardian.id IS NULL OR subject.id IS NULL OR handoff.status='COMPLETED'
  OR relationship.subject_user_id<>handoff.subject_user_id OR relationship.guardian_user_id<>p_guardian_user_id
  OR NOT guardian.is_active OR guardian.erased_at IS NOT NULL OR guardian.is_dependent OR guardian.email IS NULL OR guardian.email<>p_recipient OR guardian.email_verified_at IS DISTINCT FROM p_recipient_verified_at
  OR NOT subject.is_active OR NOT subject.is_dependent OR (subject.date_of_birth+interval '18 years')::date<>p_birthday
  OR p_birthday<=today OR p_birthday>today+30 OR p_notice_kind<>expected_kind OR NOT guardian_authority_current(guardian.id,subject.id) THEN RETURN false; END IF;
 INSERT INTO guardian_age_handoff_notices(handoff_id,relationship_id,guardian_user_id,recipient_verified_at,audience,notice_kind,birthday)
 VALUES(handoff.id,relationship.id,p_guardian_user_id,p_recipient_verified_at,'GUARDIAN_EMAIL',p_notice_kind,p_birthday)
 ON CONFLICT DO NOTHING RETURNING id INTO notice_id;
 IF notice_id IS NULL THEN RETURN false; END IF;
 INSERT INTO email_outbox(message_type,sealed_payload,guardian_handoff_notice_id) VALUES(p_notice_kind,p_sealed_payload,notice_id);
 RETURN true;
END;$$;

CREATE FUNCTION guardian_age_handoff_outbox_delivery(p_outbox_id uuid,p_as_of timestamptz)
RETURNS TABLE(deliverable boolean,user_id uuid,email text,expires_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT CASE
  WHEN outbox.message_type IN('GUARDIAN_AGE_18_30_DAY','GUARDIAN_AGE_18_7_DAY') THEN COALESCE(
   relationship.guardian_user_id=notice.guardian_user_id AND guardian.is_active AND guardian.erased_at IS NULL AND NOT guardian.is_dependent
   AND guardian.email IS NOT NULL AND guardian.email_verified_at IS NOT DISTINCT FROM notice.recipient_verified_at
   AND subject.is_active AND subject.is_dependent AND handoff.status<>'COMPLETED'
   AND (subject.date_of_birth+interval '18 years')::date=notice.birthday
   AND notice.birthday>(p_as_of AT TIME ZONE 'Europe/Lisbon')::date
   AND guardian_authority_current(guardian.id,subject.id)
   AND ((outbox.message_type='GUARDIAN_AGE_18_30_DAY' AND notice.notice_kind='GUARDIAN_AGE_18_30_DAY' AND notice.birthday>(p_as_of AT TIME ZONE 'Europe/Lisbon')::date+7)
    OR (outbox.message_type='GUARDIAN_AGE_18_7_DAY' AND notice.notice_kind='GUARDIAN_AGE_18_7_DAY' AND notice.birthday<=(p_as_of AT TIME ZONE 'Europe/Lisbon')::date+7)),false)
  WHEN outbox.message_type='GUARDIAN_AGE_18_EMAIL_VERIFY' THEN COALESCE(token.consumed_at IS NULL AND token.expires_at>p_as_of
   AND handoff.proposed_email=token.proposed_email AND handoff.status NOT IN('COMPLETED','EMAIL_COLLISION')
   AND subject.is_active AND subject.is_dependent
   AND NOT EXISTS(SELECT 1 FROM users collision WHERE collision.email=token.proposed_email AND collision.id<>subject.id),false)
  ELSE false END,
 CASE WHEN outbox.message_type='GUARDIAN_AGE_18_EMAIL_VERIFY' THEN subject.id ELSE guardian.id END,
 CASE WHEN outbox.message_type='GUARDIAN_AGE_18_EMAIL_VERIFY' THEN token.proposed_email::text ELSE guardian.email::text END,
 CASE WHEN outbox.message_type='GUARDIAN_AGE_18_EMAIL_VERIFY' THEN token.expires_at ELSE ((notice.birthday::timestamp AT TIME ZONE 'Europe/Lisbon')) END
 FROM email_outbox outbox
 LEFT JOIN guardian_age_handoff_notices notice ON notice.id=outbox.guardian_handoff_notice_id
 LEFT JOIN guardian_age_handoff_email_tokens token ON token.id=outbox.guardian_handoff_email_token_id
 LEFT JOIN guardian_age_handoffs handoff ON handoff.id=COALESCE(notice.handoff_id,token.handoff_id)
 LEFT JOIN guardian_authority_relationships relationship ON relationship.id=notice.relationship_id
 LEFT JOIN users guardian ON guardian.id=notice.guardian_user_id
 LEFT JOIN users subject ON subject.id=handoff.subject_user_id
 WHERE outbox.id=p_outbox_id
$$;

-- Route majority through the handoff first so incomplete identities are locked,
-- not erased, and ready identities convert before generic authority cutoff.
CREATE OR REPLACE FUNCTION guardian_authority_reconcile_cutoffs() RETURNS bigint
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE relationship guardian_authority_relationships%ROWTYPE;now_at timestamptz:=clock_timestamp();reconciled bigint:=0;end_reason text;
BEGIN
 PERFORM guardian_age_handoff_reconcile();
 PERFORM pg_advisory_xact_lock(hashtextextended('mycfc/guardian-authority',0));
 FOR relationship IN SELECT authority.* FROM guardian_authority_relationships authority
  JOIN users guardian ON guardian.id=authority.guardian_user_id JOIN users subject ON subject.id=authority.subject_user_id
  LEFT JOIN guardian_authority_policies policy ON policy.version=authority.policy_version
  WHERE authority.state IN('VERIFIED','SUSPENDED') AND (policy.id IS NULL OR NOT policy.enabled OR policy.adopted_at>now_at
   OR authority.review_due_at<=now_at OR authority.verified_until<=now_at OR NOT guardian.is_active OR guardian.erased_at IS NOT NULL OR guardian.is_dependent
   OR guardian.date_of_birth>((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-INTERVAL '18 years')::date
   OR NOT subject.is_active OR subject.erased_at IS NOT NULL OR NOT subject.is_dependent
   OR subject.date_of_birth<=((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-INTERVAL '18 years')::date)
  FOR UPDATE OF authority
 LOOP
  end_reason:=CASE WHEN EXISTS(SELECT 1 FROM users subject WHERE subject.id=relationship.subject_user_id AND subject.date_of_birth<=((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-INTERVAL '18 years')::date) THEN 'MAJORITY_REACHED'
   WHEN relationship.review_due_at<=now_at OR relationship.verified_until<=now_at THEN 'REVIEW_EXPIRED' ELSE 'ELIGIBILITY_ENDED' END;
  UPDATE guardian_authority_relationships SET state='EXPIRED',version=relationship.version+1,conflict=false,conflict_actor_ref=NULL,updated_at=now_at WHERE id=relationship.id;
  DELETE FROM sessions WHERE subject_indexed AND user_id=relationship.subject_user_id;
  UPDATE users SET minor_login_id=NULL,password_hash=NULL,credential_version=credential_version+1,updated_at=now_at WHERE id=relationship.subject_user_id;
  INSERT INTO guardian_authority_events(relationship_id,relationship_version,actor_ref,actor_role,action,from_state,to_state,policy_version,reason_code,occurred_at,verified_until,review_due_at)
  VALUES(relationship.id,relationship.version+1,NULL,'SYSTEM','EXPIRED',relationship.state,'EXPIRED',relationship.policy_version,end_reason,now_at,relationship.verified_until,relationship.review_due_at);
  reconciled:=reconciled+1;
 END LOOP;
 RETURN reconciled;
END;$$;

REVOKE ALL ON TABLE guardian_age_handoffs,guardian_age_handoff_events,guardian_age_handoff_email_tokens,guardian_age_handoff_notices,guardian_age_handoff_access_events FROM PUBLIC;
REVOKE ALL PRIVILEGES ON SEQUENCE guardian_age_handoff_events_id_seq,guardian_age_handoff_access_events_id_seq FROM PUBLIC;
REVOKE ALL ON FUNCTION guardian_age_handoff_reconcile(),guardian_age_handoff_propose_email(uuid,bigint,citext,bytea,timestamptz,bytea),guardian_age_handoff_verify_email(bytea),guardian_age_handoff_admin_confirm(uuid,uuid,bigint),guardian_age_handoff_admin_recovery_email(uuid,uuid,bigint,citext,bytea,timestamptz,bytea),guardian_age_handoff_for_subject(uuid),guardian_age_handoff_available_for_subject(uuid),guardian_age_handoff_notices_for_subject(uuid),guardian_age_handoff_for_guardian(uuid,uuid),guardian_age_handoff_list_for_admin(uuid,integer,integer),guardian_age_handoff_get_for_admin(uuid,uuid),guardian_age_handoff_due_guardian_notices(integer),guardian_age_handoff_enqueue_guardian_notice(uuid,uuid,uuid,citext,timestamptz,date,text,bytea),guardian_age_handoff_outbox_delivery(uuid,timestamptz) FROM PUBLIC;

DO $$BEGIN IF to_regrole('mycfc_app') IS NOT NULL THEN
 EXECUTE 'REVOKE ALL PRIVILEGES ON TABLE guardian_age_handoffs,guardian_age_handoff_events,guardian_age_handoff_email_tokens,guardian_age_handoff_notices,guardian_age_handoff_access_events FROM mycfc_app';
 EXECUTE 'REVOKE ALL PRIVILEGES ON SEQUENCE guardian_age_handoff_events_id_seq,guardian_age_handoff_access_events_id_seq FROM mycfc_app';
 EXECUTE 'GRANT EXECUTE ON FUNCTION guardian_age_handoff_reconcile(),guardian_age_handoff_propose_email(uuid,bigint,citext,bytea,timestamptz,bytea),guardian_age_handoff_verify_email(bytea),guardian_age_handoff_admin_confirm(uuid,uuid,bigint),guardian_age_handoff_admin_recovery_email(uuid,uuid,bigint,citext,bytea,timestamptz,bytea),guardian_age_handoff_for_subject(uuid),guardian_age_handoff_available_for_subject(uuid),guardian_age_handoff_notices_for_subject(uuid),guardian_age_handoff_for_guardian(uuid,uuid),guardian_age_handoff_list_for_admin(uuid,integer,integer),guardian_age_handoff_get_for_admin(uuid,uuid),guardian_age_handoff_due_guardian_notices(integer),guardian_age_handoff_enqueue_guardian_notice(uuid,uuid,uuid,citext,timestamptz,date,text,bytea),guardian_age_handoff_outbox_delivery(uuid,timestamptz) TO mycfc_app';
END IF;END$$;

-- Every schema revision invalidates previously signed privacy activation evidence.
UPDATE privacy_request_activation SET enabled=false,fulfilment_ready=false,approval_id=NULL,updated_at=clock_timestamp() WHERE singleton;
UPDATE privacy_worker_kill_switch SET engaged=true,version=version+1,activation_approval_id=NULL,changed_at=clock_timestamp() WHERE singleton;
INSERT INTO privacy_worker_kill_switch_events(version,engaged,occurred_at) SELECT version,true,changed_at FROM privacy_worker_kill_switch WHERE singleton;
UPDATE guardian_application_intake_release SET enabled=false WHERE singleton;
UPDATE guardian_authority_policies SET enabled=false,enabled_at=NULL,enabled_by=NULL WHERE enabled;
