-- Annual guardian-authority renewal, exactly-once reminders, and fail-closed expiry.

DO $$DECLARE definition text;rewritten text;
BEGIN
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint
 WHERE conrelid='privacy_activation_authenticated_artifacts'::regclass
  AND conname='privacy_activation_authenticated_artifacts_v10_check';
 IF definition IS NULL OR length(definition)-length(replace(definition,'202609120002_guardian_authority_admin_review',''))<>length('202609120002_guardian_authority_admin_review') THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_authority_renewal_activation_constraint_predecessor_mismatch';
 END IF;
 rewritten:=replace(definition,
  '''202609120002_guardian_authority_admin_review''',
  '''202609120002_guardian_authority_admin_review'', ''202609120003_guardian_authority_renewal''');
 ALTER TABLE privacy_activation_authenticated_artifacts DROP CONSTRAINT privacy_activation_authenticated_artifacts_v10_check;
 EXECUTE 'ALTER TABLE privacy_activation_authenticated_artifacts ADD CONSTRAINT privacy_activation_authenticated_artifacts_v11_check '||rewritten||' NOT VALID';
 ALTER TABLE privacy_activation_authenticated_artifacts VALIDATE CONSTRAINT privacy_activation_authenticated_artifacts_v11_check;
END$$;

DO $$DECLARE definition text;old_clause text;new_clause text;
BEGIN
 SELECT pg_get_functiondef('public.privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)'::regprocedure) INTO definition;
 old_clause:='p_artifact->>''baseline_includes_through''<>''202609120002_guardian_authority_admin_review''';
 new_clause:='p_artifact->>''baseline_includes_through''<>''202609120003_guardian_authority_renewal''';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_authority_renewal_activation_evidence_predecessor_mismatch'; END IF;
 EXECUTE replace(definition,old_clause,new_clause);

 SELECT pg_get_functiondef('public.privacy_activation_authenticated_set_digest(text,uuid[])'::regprocedure) INTO definition;
 old_clause:='schema_row.baseline_includes_through=''202609120002_guardian_authority_admin_review''';
 new_clause:='schema_row.baseline_includes_through=''202609120003_guardian_authority_renewal''';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_authority_renewal_activation_digest_predecessor_mismatch'; END IF;
 EXECUTE replace(definition,old_clause,new_clause);
END$$;

CREATE TABLE guardian_authority_renewal_requests (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 public_ref uuid NOT NULL DEFAULT gen_random_uuid() UNIQUE,
 relationship_id uuid NOT NULL REFERENCES guardian_authority_relationships(id) ON DELETE RESTRICT,
 relationship_version bigint NOT NULL CHECK(relationship_version>0),
 expiry_anchor timestamptz NOT NULL,
 response_code text NOT NULL CHECK(response_code IN('NADA_MUDOU','DADOS_MUDARAM')),
 submitted_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(relationship_id,expiry_anchor)
);
ALTER TABLE guardian_authority_events DROP CONSTRAINT guardian_authority_events_action_check;
ALTER TABLE guardian_authority_events ADD CONSTRAINT guardian_authority_events_action_v4_check
 CHECK(action IN('DECLARED','VERIFIED','SUSPENDED','EXPIRED','REJECTED','RENEWAL_SUBMITTED')) NOT VALID;
ALTER TABLE guardian_authority_events VALIDATE CONSTRAINT guardian_authority_events_action_v4_check;
CREATE INDEX guardian_authority_renewal_requests_relationship_idx
 ON guardian_authority_renewal_requests(relationship_id,submitted_at DESC);

CREATE TABLE guardian_authority_renewal_events (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 renewal_id uuid NOT NULL REFERENCES guardian_authority_renewal_requests(id) ON DELETE RESTRICT,
 actor_ref uuid NULL,
 actor_role text NOT NULL CHECK(actor_role IN('GUARDIAN','ADMIN','SYSTEM')),
 action text NOT NULL CHECK(action IN('SUBMITTED','APPROVED','REJECTED','SUSPENDED','ENDED','EXPIRED')),
 relationship_version bigint NOT NULL CHECK(relationship_version>0),
 occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK((actor_role='SYSTEM' AND actor_ref IS NULL) OR (actor_role<>'SYSTEM' AND actor_ref IS NOT NULL)),
 UNIQUE(renewal_id,action,relationship_version)
);
CREATE INDEX guardian_authority_renewal_events_latest_idx
 ON guardian_authority_renewal_events(renewal_id,id DESC);

CREATE TABLE guardian_authority_renewal_reminders (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 relationship_id uuid NOT NULL REFERENCES guardian_authority_relationships(id) ON DELETE RESTRICT,
 guardian_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 recipient_verified_at timestamptz NOT NULL,
 expiry_anchor timestamptz NOT NULL,
 reminder_kind text NOT NULL CHECK(reminder_kind IN('GUARDIAN_RENEWAL_30_DAY','GUARDIAN_RENEWAL_7_DAY')),
 queued_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(relationship_id,expiry_anchor,reminder_kind)
);

ALTER TABLE email_outbox ADD COLUMN guardian_reminder_id uuid NULL
 REFERENCES guardian_authority_renewal_reminders(id) ON DELETE RESTRICT;
CREATE UNIQUE INDEX email_outbox_guardian_reminder_uidx ON email_outbox(guardian_reminder_id)
 WHERE guardian_reminder_id IS NOT NULL;
ALTER TABLE email_outbox DROP CONSTRAINT email_outbox_message_valid;
ALTER TABLE email_outbox ADD CONSTRAINT email_outbox_message_valid CHECK (
 (message_type='EMAIL_VERIFICATION' AND verification_token_id IS NOT NULL AND password_reset_token_id IS NULL AND sealed_payload IS NULL AND privacy_request_id IS NULL AND privacy_requester_id IS NULL AND privacy_event_key IS NULL AND guardian_reminder_id IS NULL)
 OR (message_type='PASSWORD_RESET' AND verification_token_id IS NULL AND password_reset_token_id IS NOT NULL AND sealed_payload IS NOT NULL AND privacy_request_id IS NULL AND privacy_requester_id IS NULL AND privacy_event_key IS NULL AND guardian_reminder_id IS NULL)
 OR (message_type IN('PRIVACY_ACKNOWLEDGEMENT','PRIVACY_DECISION','PRIVACY_PROCESSING_STARTED','PRIVACY_COMPLETED') AND verification_token_id IS NULL AND password_reset_token_id IS NULL AND sealed_payload IS NOT NULL AND privacy_request_id IS NOT NULL AND privacy_event_key IS NOT NULL AND guardian_reminder_id IS NULL)
 OR (message_type IN('GUARDIAN_RENEWAL_30_DAY','GUARDIAN_RENEWAL_7_DAY') AND verification_token_id IS NULL AND password_reset_token_id IS NULL AND sealed_payload IS NOT NULL AND privacy_request_id IS NULL AND privacy_requester_id IS NULL AND privacy_event_key IS NULL AND guardian_reminder_id IS NOT NULL)
);

CREATE TRIGGER guardian_authority_renewal_requests_immutable BEFORE UPDATE OR DELETE ON guardian_authority_renewal_requests
 FOR EACH ROW EXECUTE FUNCTION prevent_guardian_authority_event_mutation();
CREATE TRIGGER guardian_authority_renewal_events_immutable BEFORE UPDATE OR DELETE ON guardian_authority_renewal_events
 FOR EACH ROW EXECUTE FUNCTION prevent_guardian_authority_event_mutation();
CREATE TRIGGER guardian_authority_renewal_reminders_immutable BEFORE UPDATE OR DELETE ON guardian_authority_renewal_reminders
 FOR EACH ROW EXECUTE FUNCTION prevent_guardian_authority_event_mutation();

CREATE VIEW guardian_authority_latest_renewals AS
SELECT DISTINCT ON(request.relationship_id) request.relationship_id,request.public_ref AS renewal_ref,
 request.response_code,event.action AS renewal_status,request.expiry_anchor
FROM guardian_authority_renewal_requests request
JOIN LATERAL(SELECT candidate.action FROM guardian_authority_renewal_events candidate
 WHERE candidate.renewal_id=request.id ORDER BY candidate.id DESC LIMIT 1) event ON true
ORDER BY request.relationship_id,request.submitted_at DESC,request.id DESC;

CREATE OR REPLACE VIEW guardian_authority_guardian_disclosures AS
SELECT relationship.id AS relationship_id,relationship.public_ref AS relationship_ref,
 relationship.guardian_user_id,relationship.subject_user_id,relationship.submitted_label,
 CASE
  WHEN relationship.state='VERIFIED' AND guardian_authority_current(relationship.guardian_user_id,relationship.subject_user_id) THEN 'VERIFIED'
  WHEN relationship.state='VERIFIED' AND EXISTS(SELECT 1 FROM users age_subject WHERE age_subject.id=relationship.subject_user_id
    AND age_subject.date_of_birth<=((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-INTERVAL '18 years')::date) THEN 'EXPIRED'
  WHEN relationship.state='VERIFIED' AND (relationship.review_due_at<=clock_timestamp() OR relationship.verified_until<=clock_timestamp()) THEN 'EXPIRED'
  WHEN relationship.state='VERIFIED' THEN 'SUSPENDED'
  ELSE relationship.state
 END::text AS state,
 relationship.version,relationship.created_at,relationship.verified_until,relationship.review_due_at,relationship.conflict,
 CASE WHEN guardian_authority_current(relationship.guardian_user_id,relationship.subject_user_id) THEN subject.name::text ELSE NULL::text END AS subject_name,
 CASE WHEN guardian_authority_current(relationship.guardian_user_id,relationship.subject_user_id) THEN subject.date_of_birth ELSE NULL::date END AS date_of_birth,
 CASE WHEN guardian_authority_current(relationship.guardian_user_id,relationship.subject_user_id) THEN subject.minor_login_id::text ELSE NULL::text END AS minor_login_id,
 CASE WHEN guardian_authority_current(relationship.guardian_user_id,relationship.subject_user_id) THEN subject.leaderboard_visible ELSE NULL::boolean END AS leaderboard_visible,
 CASE WHEN guardian_authority_current(relationship.guardian_user_id,relationship.subject_user_id) THEN
  COALESCE(profile.emergency_contact_name<>'' AND profile.emergency_contact_relationship<>'' AND profile.emergency_contact_phone<>'' AND profile.medical_declaration<>'UNKNOWN',false)
  ELSE NULL::boolean END AS profile_complete,
 renewal.renewal_ref,renewal.response_code,renewal.renewal_status,renewal.expiry_anchor AS renewal_expiry_anchor
FROM guardian_authority_relationships relationship
JOIN users subject ON subject.id=relationship.subject_user_id
LEFT JOIN member_profiles profile ON profile.user_id=subject.id
LEFT JOIN guardian_authority_latest_renewals renewal ON renewal.relationship_id=relationship.id;

CREATE FUNCTION guardian_authority_submit_renewal(p_actor_id uuid,p_relationship_ref uuid,p_expected_version bigint,p_response_code text)
RETURNS SETOF guardian_authority_relationships
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE relationship guardian_authority_relationships%ROWTYPE;renewal guardian_authority_renewal_requests%ROWTYPE;
 now_at timestamptz:=clock_timestamp();new_version bigint;
BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended('mycfc/guardian-authority',0));
 SELECT * INTO relationship FROM guardian_authority_relationships WHERE public_ref=p_relationship_ref FOR UPDATE;
 IF NOT FOUND OR relationship.version<>p_expected_version THEN
  RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='guardian_authority_stale'; END IF;
 IF relationship.guardian_user_id<>p_actor_id THEN
  RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='guardian_authority_guardian_required'; END IF;
 IF p_response_code NOT IN('NADA_MUDOU','DADOS_MUDARAM') OR relationship.state<>'VERIFIED'
  OR relationship.verified_until IS NULL OR relationship.verified_until<=now_at
  OR now_at<relationship.verified_until-interval '30 days' THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='guardian_authority_renewal_not_due'; END IF;
 IF NOT guardian_authority_current(relationship.guardian_user_id,relationship.subject_user_id) THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='guardian_authority_renewal_not_due'; END IF;
 INSERT INTO guardian_authority_renewal_requests(relationship_id,relationship_version,expiry_anchor,response_code,submitted_at)
 VALUES(relationship.id,relationship.version,relationship.verified_until,p_response_code,now_at)
 ON CONFLICT(relationship_id,expiry_anchor) DO NOTHING RETURNING * INTO renewal;
 IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='guardian_authority_renewal_already_submitted'; END IF;
 new_version:=relationship.version+1;
	 IF p_response_code='DADOS_MUDARAM' THEN
  UPDATE guardian_authority_relationships SET state='SUSPENDED',version=new_version,
   conflict=false,conflict_actor_ref=NULL,updated_at=now_at WHERE id=relationship.id;
  DELETE FROM sessions WHERE subject_indexed AND user_id=relationship.subject_user_id;
  UPDATE users SET minor_login_id=NULL,password_hash=NULL,credential_version=credential_version+1,updated_at=now_at
   WHERE id=relationship.subject_user_id;
  INSERT INTO guardian_authority_events(relationship_id,relationship_version,actor_ref,actor_role,action,from_state,to_state,
   policy_version,reason_code,occurred_at,verified_until,review_due_at)
  VALUES(relationship.id,new_version,p_actor_id,'GUARDIAN','SUSPENDED','VERIFIED','SUSPENDED',
   relationship.policy_version,'AUTHORITY_CHANGED',now_at,relationship.verified_until,relationship.review_due_at);
	  relationship.state:='SUSPENDED';relationship.version:=new_version;relationship.updated_at:=now_at;
	 ELSE
	  UPDATE guardian_authority_relationships SET version=new_version,updated_at=now_at WHERE id=relationship.id;
	  INSERT INTO guardian_authority_events(relationship_id,relationship_version,actor_ref,actor_role,action,from_state,to_state,
	   policy_version,occurred_at,verified_until,review_due_at)
	  VALUES(relationship.id,new_version,p_actor_id,'GUARDIAN','RENEWAL_SUBMITTED','VERIFIED','VERIFIED',
	   relationship.policy_version,now_at,relationship.verified_until,relationship.review_due_at);
	  relationship.version:=new_version;relationship.updated_at:=now_at;
	 END IF;
 INSERT INTO guardian_authority_renewal_events(renewal_id,actor_ref,actor_role,action,relationship_version,occurred_at)
 VALUES(renewal.id,p_actor_id,'GUARDIAN','SUBMITTED',new_version,now_at);
 RETURN NEXT relationship;
END;
$$;

CREATE FUNCTION guardian_authority_due_renewal_reminders(p_limit integer)
RETURNS TABLE(relationship_ref uuid,guardian_user_id uuid,recipient text,recipient_verified_at timestamptz,expiry_anchor timestamptz,reminder_kind text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 WITH now_value AS(SELECT clock_timestamp() AS value),candidate AS(
  SELECT relationship.id,relationship.public_ref,relationship.guardian_user_id,guardian.email::text recipient,
   guardian.email_verified_at,relationship.verified_until,
   CASE WHEN relationship.verified_until<=(SELECT value FROM now_value)+interval '7 days'
    THEN 'GUARDIAN_RENEWAL_7_DAY' ELSE 'GUARDIAN_RENEWAL_30_DAY' END kind
  FROM guardian_authority_relationships relationship
  JOIN users guardian ON guardian.id=relationship.guardian_user_id
  WHERE relationship.state='VERIFIED' AND guardian.is_active AND guardian.erased_at IS NULL
   AND guardian.email IS NOT NULL AND guardian.email_verified_at IS NOT NULL
   AND relationship.verified_until>(SELECT value FROM now_value)
   AND relationship.verified_until<=(SELECT value FROM now_value)+interval '30 days'
   AND guardian_authority_current(relationship.guardian_user_id,relationship.subject_user_id)
   AND NOT EXISTS(SELECT 1 FROM guardian_authority_renewal_requests request
    WHERE request.relationship_id=relationship.id AND request.expiry_anchor=relationship.verified_until)
 )
 SELECT candidate.public_ref AS relationship_ref,candidate.guardian_user_id,candidate.recipient,
  candidate.email_verified_at AS recipient_verified_at,candidate.verified_until AS expiry_anchor,candidate.kind AS reminder_kind
 FROM candidate WHERE NOT EXISTS(SELECT 1 FROM guardian_authority_renewal_reminders reminder
  WHERE reminder.relationship_id=candidate.id AND reminder.expiry_anchor=candidate.verified_until AND reminder.reminder_kind=candidate.kind)
 ORDER BY candidate.verified_until,candidate.id LIMIT LEAST(GREATEST(p_limit,1),100)
$$;

CREATE FUNCTION guardian_authority_enqueue_renewal_reminder(p_relationship_ref uuid,p_guardian_user_id uuid,p_recipient citext,
 p_recipient_verified_at timestamptz,p_expiry_anchor timestamptz,p_reminder_kind text,p_sealed_payload bytea)
RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE relationship guardian_authority_relationships%ROWTYPE;guardian users%ROWTYPE;reminder_id uuid;now_at timestamptz:=clock_timestamp();expected_kind text;
BEGIN
 IF p_reminder_kind NOT IN('GUARDIAN_RENEWAL_30_DAY','GUARDIAN_RENEWAL_7_DAY') OR octet_length(p_sealed_payload)<32 THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='guardian_authority_reminder_rejected'; END IF;
 SELECT * INTO relationship FROM guardian_authority_relationships WHERE public_ref=p_relationship_ref FOR UPDATE;
 SELECT * INTO guardian FROM users WHERE id=p_guardian_user_id FOR SHARE;
 expected_kind:=CASE WHEN relationship.verified_until<=now_at+interval '7 days' THEN 'GUARDIAN_RENEWAL_7_DAY' ELSE 'GUARDIAN_RENEWAL_30_DAY' END;
 IF relationship.id IS NULL OR relationship.guardian_user_id<>p_guardian_user_id OR guardian.id IS NULL
  OR NOT guardian.is_active OR guardian.erased_at IS NOT NULL OR guardian.is_dependent
  OR guardian.email IS NULL OR guardian.email<>p_recipient OR guardian.email_verified_at IS DISTINCT FROM p_recipient_verified_at
  OR relationship.state<>'VERIFIED' OR relationship.verified_until<>p_expiry_anchor
  OR relationship.verified_until<=now_at OR relationship.verified_until>now_at+interval '30 days'
  OR p_reminder_kind<>expected_kind
  OR NOT guardian_authority_current(relationship.guardian_user_id,relationship.subject_user_id)
  OR EXISTS(SELECT 1 FROM guardian_authority_renewal_requests request WHERE request.relationship_id=relationship.id AND request.expiry_anchor=p_expiry_anchor) THEN
  RETURN false; END IF;
 INSERT INTO guardian_authority_renewal_reminders(relationship_id,guardian_user_id,recipient_verified_at,expiry_anchor,reminder_kind,queued_at)
 VALUES(relationship.id,p_guardian_user_id,p_recipient_verified_at,p_expiry_anchor,p_reminder_kind,now_at)
 ON CONFLICT(relationship_id,expiry_anchor,reminder_kind) DO NOTHING RETURNING id INTO reminder_id;
 IF reminder_id IS NULL THEN RETURN false; END IF;
 INSERT INTO email_outbox(message_type,sealed_payload,guardian_reminder_id,next_attempt_at,created_at,updated_at)
 VALUES(p_reminder_kind,p_sealed_payload,reminder_id,now_at,now_at,now_at);
 RETURN true;
END;
$$;

CREATE OR REPLACE FUNCTION guardian_authority_reconcile_cutoffs() RETURNS bigint
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE relationship guardian_authority_relationships%ROWTYPE;now_at timestamptz:=clock_timestamp();reconciled bigint:=0;end_reason text;
BEGIN
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
  end_reason:=CASE WHEN EXISTS(SELECT 1 FROM users subject WHERE subject.id=relationship.subject_user_id
    AND subject.date_of_birth<=((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-INTERVAL '18 years')::date) THEN 'MAJORITY_REACHED'
   WHEN relationship.review_due_at<=now_at OR relationship.verified_until<=now_at THEN 'REVIEW_EXPIRED' ELSE 'ELIGIBILITY_ENDED' END;
  UPDATE guardian_authority_relationships SET state='EXPIRED',version=relationship.version+1,conflict=false,conflict_actor_ref=NULL,updated_at=now_at WHERE id=relationship.id;
  DELETE FROM sessions WHERE subject_indexed AND user_id=relationship.subject_user_id;
  UPDATE users SET minor_login_id=NULL,password_hash=NULL,credential_version=credential_version+1,updated_at=now_at WHERE id=relationship.subject_user_id;
  INSERT INTO guardian_authority_events(relationship_id,relationship_version,actor_ref,actor_role,action,from_state,to_state,policy_version,reason_code,occurred_at,verified_until,review_due_at)
  VALUES(relationship.id,relationship.version+1,NULL,'SYSTEM','EXPIRED',relationship.state,'EXPIRED',relationship.policy_version,end_reason,now_at,relationship.verified_until,relationship.review_due_at);
  reconciled:=reconciled+1;
 END LOOP;
 RETURN reconciled;
END;
$$;

CREATE OR REPLACE FUNCTION guardian_authority_admin_transition(p_actor_id uuid,p_relationship_ref uuid,p_expected_version bigint,p_action text,p_evidence_category text,p_reason_code text)
RETURNS SETOF guardian_authority_relationships LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE current_row guardian_authority_relationships%ROWTYPE;policy_row guardian_authority_policies%ROWTYPE;renewal guardian_authority_renewal_requests%ROWTYPE;
 now_at timestamptz:=clock_timestamp();new_version bigint;target_state text;old_state text;new_verified_until timestamptz;new_review_due_at timestamptz;new_conflict boolean;new_conflict_actor uuid;renewal_action text;
BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended('mycfc/guardian-authority',0));
 IF NOT guardian_authority_is_administrator(p_actor_id) THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='guardian_authority_administrator_required'; END IF;
 SELECT * INTO current_row FROM guardian_authority_relationships WHERE public_ref=p_relationship_ref FOR UPDATE;
 IF NOT FOUND OR current_row.version<>p_expected_version THEN RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='guardian_authority_stale'; END IF;
 IF guardian_authority_personally_involved(p_actor_id,p_relationship_ref) OR current_row.conflict_actor_ref=p_actor_id THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='guardian_authority_separation_required'; END IF;
 SELECT * INTO policy_row FROM guardian_authority_policies WHERE enabled AND adopted_at<=now_at FOR SHARE;
 IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_authority_policy_unavailable'; END IF;
 SELECT request.* INTO renewal FROM guardian_authority_renewal_requests request
  JOIN LATERAL(SELECT event.action FROM guardian_authority_renewal_events event WHERE event.renewal_id=request.id ORDER BY event.id DESC LIMIT 1) latest ON true
  WHERE request.relationship_id=current_row.id AND latest.action='SUBMITTED' ORDER BY request.submitted_at DESC LIMIT 1 FOR UPDATE OF request;
 IF p_evidence_category NOT IN('CLUB_REGISTRATION_RECORD','IN_PERSON_ID_AND_CIVIL_RECORD','COURT_OR_LEGAL_AUTHORITY') THEN RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='guardian_authority_evidence_rejected'; END IF;
 target_state:=CASE p_action WHEN 'APPROVE' THEN 'VERIFIED' WHEN 'RENEW' THEN 'VERIFIED' WHEN 'REJECT' THEN 'REJECTED' WHEN 'SUSPEND' THEN 'SUSPENDED' WHEN 'END' THEN 'EXPIRED' ELSE NULL END;
 IF target_state IS NULL OR (p_action='APPROVE' AND (current_row.state NOT IN('PENDING','SUSPENDED') OR renewal.id IS NOT NULL))
  OR (p_action='RENEW' AND (renewal.id IS NULL OR current_row.state NOT IN('VERIFIED','SUSPENDED','EXPIRED')))
  OR (p_action='REJECT' AND current_row.state NOT IN('PENDING','SUSPENDED')) OR (p_action='SUSPEND' AND current_row.state NOT IN('PENDING','VERIFIED'))
  OR (p_action='END' AND current_row.state NOT IN('VERIFIED','SUSPENDED')) THEN RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='guardian_authority_transition_rejected'; END IF;
 IF (p_action IN('APPROVE','RENEW') AND p_reason_code<>'RELATIONSHIP_CONFIRMED') OR (p_action='REJECT' AND p_reason_code NOT IN('EVIDENCE_INSUFFICIENT','AUTHORITY_NOT_ESTABLISHED'))
  OR (p_action='SUSPEND' AND p_reason_code NOT IN('CONFLICT','AUTHORITY_CHANGED','UNCERTAINTY')) OR (p_action='END' AND p_reason_code<>'AUTHORITY_ENDED') THEN RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='guardian_authority_reason_rejected'; END IF;
 IF target_state='VERIFIED' AND NOT EXISTS(SELECT 1 FROM users guardian JOIN users subject ON subject.id=current_row.subject_user_id WHERE guardian.id=current_row.guardian_user_id
  AND guardian.is_active AND guardian.erased_at IS NULL AND NOT guardian.is_dependent AND guardian.date_of_birth<=((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-INTERVAL '18 years')::date
  AND subject.is_active AND subject.erased_at IS NULL AND subject.is_dependent AND subject.date_of_birth>((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-INTERVAL '18 years')::date)
  THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_authority_relationship_ineligible'; END IF;
 new_version:=current_row.version+1;old_state:=current_row.state;
 IF target_state='VERIFIED' THEN
  IF p_action='RENEW' THEN new_verified_until:=renewal.expiry_anchor+interval '12 months';new_review_due_at:=new_verified_until;
  ELSE new_review_due_at:=now_at+make_interval(days=>policy_row.review_days);new_verified_until:=now_at+make_interval(days=>policy_row.validity_days); END IF;
  new_conflict:=false;new_conflict_actor:=NULL;
 ELSE new_review_due_at:=current_row.review_due_at;new_verified_until:=current_row.verified_until;new_conflict:=(target_state='SUSPENDED' AND p_reason_code='CONFLICT');new_conflict_actor:=CASE WHEN new_conflict THEN p_actor_id ELSE NULL END; END IF;
 UPDATE guardian_authority_relationships SET state=target_state,version=new_version,policy_version=policy_row.version,verified_at=CASE WHEN target_state='VERIFIED' THEN now_at ELSE verified_at END,
  verified_by=CASE WHEN target_state='VERIFIED' THEN p_actor_id ELSE verified_by END,verified_until=new_verified_until,review_due_at=new_review_due_at,conflict=new_conflict,conflict_actor_ref=new_conflict_actor,updated_at=now_at
 WHERE id=current_row.id AND version=p_expected_version RETURNING * INTO current_row;
 IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='guardian_authority_stale'; END IF;
 IF target_state IN('SUSPENDED','EXPIRED','REJECTED') OR (target_state='VERIFIED' AND old_state<>'VERIFIED') THEN
  DELETE FROM sessions WHERE subject_indexed AND user_id=current_row.subject_user_id;
  UPDATE users SET minor_login_id=NULL,password_hash=NULL,credential_version=credential_version+1,updated_at=now_at WHERE id=current_row.subject_user_id;
 END IF;
 INSERT INTO guardian_authority_events(relationship_id,relationship_version,actor_ref,actor_role,action,from_state,to_state,policy_version,evidence_type,reason_code,occurred_at,verified_until,review_due_at)
 VALUES(current_row.id,new_version,p_actor_id,'ADMIN',target_state,old_state,target_state,policy_row.version,p_evidence_category,p_reason_code,now_at,new_verified_until,new_review_due_at);
 IF renewal.id IS NOT NULL THEN renewal_action:=CASE p_action WHEN 'RENEW' THEN 'APPROVED' WHEN 'REJECT' THEN 'REJECTED' WHEN 'SUSPEND' THEN 'SUSPENDED' WHEN 'END' THEN 'ENDED' ELSE NULL END;
  IF renewal_action IS NOT NULL THEN INSERT INTO guardian_authority_renewal_events(renewal_id,actor_ref,actor_role,action,relationship_version,occurred_at)
   VALUES(renewal.id,p_actor_id,'ADMIN',renewal_action,new_version,now_at); END IF;
 END IF;
 RETURN NEXT current_row;
END;
$$;

REVOKE ALL ON TABLE guardian_authority_renewal_requests,guardian_authority_renewal_events,guardian_authority_renewal_reminders,guardian_authority_latest_renewals FROM PUBLIC;
DO $$BEGIN IF to_regrole('mycfc_app') IS NOT NULL THEN
 EXECUTE 'REVOKE ALL PRIVILEGES ON TABLE guardian_authority_renewal_requests,guardian_authority_renewal_events,guardian_authority_renewal_reminders FROM mycfc_app';
 EXECUTE 'GRANT SELECT ON TABLE guardian_authority_latest_renewals TO mycfc_app';
END IF; END$$;
REVOKE ALL ON FUNCTION guardian_authority_submit_renewal(uuid,uuid,bigint,text),guardian_authority_due_renewal_reminders(integer),guardian_authority_enqueue_renewal_reminder(uuid,uuid,citext,timestamptz,timestamptz,text,bytea) FROM PUBLIC;

-- This schema revision invalidates every prior privacy activation artifact.
UPDATE privacy_request_activation SET enabled=false,fulfilment_ready=false,approval_id=NULL,updated_at=clock_timestamp() WHERE singleton;
UPDATE privacy_worker_kill_switch SET engaged=true,version=version+1,activation_approval_id=NULL,changed_at=clock_timestamp() WHERE singleton;
INSERT INTO privacy_worker_kill_switch_events(version,engaged,occurred_at) SELECT version,true,changed_at FROM privacy_worker_kill_switch WHERE singleton;
