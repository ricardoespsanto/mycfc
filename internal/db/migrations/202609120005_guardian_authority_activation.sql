-- Source-only guardian-authority V2 activation boundary. The migration remains
-- fail closed: it creates no login and leaves intake and every policy disabled.

DO $$DECLARE definition text;rewritten text;
BEGIN
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint
 WHERE conrelid='privacy_activation_authenticated_artifacts'::regclass
  AND conname='privacy_activation_authenticated_artifacts_v12_check';
 IF definition IS NULL OR length(definition)-length(replace(definition,'202609120004_guardian_age_handoff',''))<>length('202609120004_guardian_age_handoff') THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_activation_privacy_constraint_predecessor_mismatch';
 END IF;
 rewritten:=replace(definition,
  '''202609120004_guardian_age_handoff''',
  '''202609120004_guardian_age_handoff'', ''202609120005_guardian_authority_activation''');
 ALTER TABLE privacy_activation_authenticated_artifacts DROP CONSTRAINT privacy_activation_authenticated_artifacts_v12_check;
 EXECUTE 'ALTER TABLE privacy_activation_authenticated_artifacts ADD CONSTRAINT privacy_activation_authenticated_artifacts_v13_check '||rewritten||' NOT VALID';
 ALTER TABLE privacy_activation_authenticated_artifacts VALIDATE CONSTRAINT privacy_activation_authenticated_artifacts_v13_check;
END$$;

DO $$DECLARE definition text;old_clause text;new_clause text;
BEGIN
 SELECT pg_get_functiondef('public.privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)'::regprocedure) INTO definition;
 old_clause:='p_artifact->>''baseline_includes_through''<>''202609120004_guardian_age_handoff''';
 new_clause:='p_artifact->>''baseline_includes_through''<>''202609120005_guardian_authority_activation''';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_activation_privacy_evidence_predecessor_mismatch'; END IF;
 EXECUTE replace(definition,old_clause,new_clause);

 SELECT pg_get_functiondef('public.privacy_activation_authenticated_set_digest(text,uuid[])'::regprocedure) INTO definition;
 old_clause:='schema_row.baseline_includes_through=''202609120004_guardian_age_handoff''';
 new_clause:='schema_row.baseline_includes_through=''202609120005_guardian_authority_activation''';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_activation_privacy_digest_predecessor_mismatch'; END IF;
 EXECUTE replace(definition,old_clause,new_clause);
END$$;

CREATE SCHEMA guardian_ops;
REVOKE ALL ON SCHEMA guardian_ops FROM PUBLIC;

CREATE TABLE guardian_ops.runtime_release_binding(
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
 database_name text NULL,
 image_digest text NULL CHECK(image_digest IS NULL OR image_digest~'^sha256:[0-9a-f]{64}$'),
 schema_migration_digest text NULL CHECK(schema_migration_digest IS NULL OR schema_migration_digest~'^[0-9a-f]{64}$'),
 generation bigint NOT NULL DEFAULT 0 CHECK(generation>=0),
 bound_at timestamptz NULL,
 CHECK((database_name IS NULL AND image_digest IS NULL AND schema_migration_digest IS NULL AND bound_at IS NULL)
  OR (database_name IS NOT NULL AND image_digest IS NOT NULL AND schema_migration_digest IS NOT NULL AND bound_at IS NOT NULL))
);
CREATE TABLE guardian_ops.runtime_release_binding_events(
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 generation bigint NOT NULL CHECK(generation>0),
 database_name text NOT NULL,
 image_digest text NOT NULL CHECK(image_digest~'^sha256:[0-9a-f]{64}$'),
 schema_migration_digest text NOT NULL CHECK(schema_migration_digest~'^[0-9a-f]{64}$'),
 relationship_count bigint NOT NULL CHECK(relationship_count>=0),
 credential_count bigint NOT NULL CHECK(credential_count>=0),
 session_count bigint NOT NULL CHECK(session_count>=0),
 occurred_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TRIGGER guardian_runtime_release_binding_events_immutable BEFORE UPDATE OR DELETE ON guardian_ops.runtime_release_binding_events
 FOR EACH ROW EXECUTE FUNCTION prevent_guardian_authority_event_mutation();
INSERT INTO guardian_ops.runtime_release_binding(singleton) VALUES(true);

CREATE FUNCTION guardian_ops.schema_ready(p_embedded_schema_digest text)
RETURNS boolean LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE ready boolean:=false;
BEGIN
 IF p_embedded_schema_digest!~'^[0-9a-f]{64}$' OR to_regclass('mycfc_meta.schema_migrations') IS NULL THEN RETURN false; END IF;
 EXECUTE 'SELECT encode(digest(convert_to(string_agg(version,E''\n'' ORDER BY version),''UTF8''),''sha256''),''hex'')=$1
   AND bool_or(version=''202609120005_guardian_authority_activation'') FROM mycfc_meta.schema_migrations'
 INTO ready USING p_embedded_schema_digest;
 RETURN COALESCE(ready,false);
END;$$;
REVOKE ALL ON FUNCTION guardian_ops.schema_ready(text) FROM PUBLIC;

CREATE TABLE guardian_authority_policy_approvals(
 policy_version varchar(80) PRIMARY KEY REFERENCES guardian_authority_policies(version) ON DELETE RESTRICT,
 policy_sha256 bytea NOT NULL CHECK(octet_length(policy_sha256)=32),
 approval_sha256 bytea NOT NULL UNIQUE CHECK(octet_length(approval_sha256)=32),
 approval_contract text NOT NULL CHECK(approval_contract='mycfc/guardian-authority-policy-approval/v1'),
 approval_canonical bytea NOT NULL CHECK(octet_length(approval_canonical) BETWEEN 2 AND 1048576),
 expected_database text NOT NULL CHECK(expected_database=btrim(expected_database) AND expected_database~'^[A-Za-z][A-Za-z0-9_]{0,62}$'),
 authorized_operator_actor_ref uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 controller_role text NOT NULL CHECK(controller_role='CLUB_DIRECTION'),
 controller_approval_reference text NOT NULL CHECK(controller_approval_reference=btrim(controller_approval_reference) AND char_length(controller_approval_reference) BETWEEN 1 AND 200),
 controller_approved_on date NOT NULL,
 effective_on date NOT NULL,
 review_due_on date NOT NULL,
 legal_reviewer_reference text NOT NULL CHECK(legal_reviewer_reference=btrim(legal_reviewer_reference) AND char_length(legal_reviewer_reference) BETWEEN 1 AND 200),
 legal_review_reference text NOT NULL CHECK(legal_review_reference=btrim(legal_review_reference) AND char_length(legal_review_reference) BETWEEN 1 AND 200),
 legal_reviewed_on date NOT NULL,
 legal_review_conclusion text NOT NULL CHECK(legal_review_conclusion='APPROVED'),
 bound_image_digest text NOT NULL CHECK(bound_image_digest~'^sha256:[0-9a-f]{64}$'),
 bound_schema_migration_digest text NOT NULL CHECK(bound_schema_migration_digest~'^[0-9a-f]{64}$'),
 bound_by uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 bound_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK(controller_approved_on<=effective_on AND legal_reviewed_on<=effective_on AND review_due_on>effective_on),
 CHECK(bound_by=authorized_operator_actor_ref)
);
CREATE TRIGGER guardian_authority_policy_approvals_immutable BEFORE UPDATE OR DELETE ON guardian_authority_policy_approvals
 FOR EACH ROW EXECUTE FUNCTION prevent_guardian_authority_event_mutation();

ALTER TABLE guardian_application_intake_release DROP CONSTRAINT guardian_application_intake_release_disabled_check;
ALTER TABLE guardian_application_intake_release
 ADD COLUMN policy_version varchar(80) NULL REFERENCES guardian_authority_policies(version) ON DELETE RESTRICT,
 ADD COLUMN policy_sha256 bytea NULL CHECK(policy_sha256 IS NULL OR octet_length(policy_sha256)=32),
 ADD COLUMN approval_sha256 bytea NULL CHECK(approval_sha256 IS NULL OR octet_length(approval_sha256)=32),
 ADD COLUMN image_digest text NULL CHECK(image_digest IS NULL OR image_digest~'^sha256:[0-9a-f]{64}$'),
 ADD COLUMN schema_migration_digest text NULL CHECK(schema_migration_digest IS NULL OR schema_migration_digest~'^[0-9a-f]{64}$'),
 ADD COLUMN enabled_by uuid NULL REFERENCES users(id) ON DELETE RESTRICT,
 ADD COLUMN enabled_at timestamptz NULL,
 ADD CONSTRAINT guardian_application_intake_release_binding_check CHECK(
  (enabled AND policy_version IS NOT NULL AND policy_sha256 IS NOT NULL AND approval_sha256 IS NOT NULL
   AND image_digest IS NOT NULL AND schema_migration_digest IS NOT NULL AND enabled_by IS NOT NULL AND enabled_at IS NOT NULL)
  OR (NOT enabled AND policy_version IS NULL AND policy_sha256 IS NULL AND approval_sha256 IS NULL
   AND image_digest IS NULL AND schema_migration_digest IS NULL AND enabled_by IS NULL AND enabled_at IS NULL));

ALTER TABLE guardian_application_intake_release_events DROP CONSTRAINT guardian_application_intake_release_events_action_check;
ALTER TABLE guardian_application_intake_release_events DROP CONSTRAINT guardian_application_intake_release_events_actor_ref_check;
ALTER TABLE guardian_application_intake_release_events
 ADD COLUMN policy_version varchar(80) NULL,
 ADD COLUMN policy_sha256 bytea NULL CHECK(policy_sha256 IS NULL OR octet_length(policy_sha256)=32),
 ADD COLUMN approval_sha256 bytea NULL CHECK(approval_sha256 IS NULL OR octet_length(approval_sha256)=32),
 ADD COLUMN image_digest text NULL,
 ADD COLUMN schema_migration_digest text NULL,
 ADD COLUMN relationship_count bigint NOT NULL DEFAULT 0 CHECK(relationship_count>=0),
 ADD COLUMN credential_count bigint NOT NULL DEFAULT 0 CHECK(credential_count>=0),
 ADD COLUMN session_count bigint NOT NULL DEFAULT 0 CHECK(session_count>=0),
 ADD CONSTRAINT guardian_application_intake_release_events_action_v2_check CHECK(action IN('CREATED_DISABLED','ENABLED','DISABLED')),
 ADD CONSTRAINT guardian_application_intake_release_events_actor_v2_check CHECK((action='CREATED_DISABLED' AND actor_ref IS NULL) OR (action<>'CREATED_DISABLED' AND actor_ref IS NOT NULL));

CREATE FUNCTION guardian_authority_policy_operational(p_version text) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT COALESCE(EXISTS(
  SELECT 1 FROM guardian_authority_policies policy
  JOIN guardian_authority_policy_approvals approval ON approval.policy_version=policy.version
  JOIN guardian_application_intake_release gate ON gate.singleton AND gate.enabled AND gate.gate_version='guardian-intake-v2'
  JOIN guardian_ops.runtime_release_binding runtime ON runtime.singleton
  WHERE policy.version=p_version AND policy.enabled AND policy.adopted_at<=clock_timestamp()
   AND gate.policy_version=policy.version AND gate.policy_sha256=approval.policy_sha256 AND gate.approval_sha256=approval.approval_sha256
   AND gate.image_digest=approval.bound_image_digest AND gate.schema_migration_digest=approval.bound_schema_migration_digest
   AND runtime.database_name=current_database() AND approval.expected_database=current_database()
   AND runtime.image_digest=gate.image_digest AND runtime.schema_migration_digest=gate.schema_migration_digest
   AND guardian_ops.schema_ready(gate.schema_migration_digest)
   AND approval.effective_on<=(clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date
   AND approval.review_due_on>(clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date
 ),false);
$$;

CREATE OR REPLACE FUNCTION guardian_authority_current(p_guardian_user_id uuid,p_subject_user_id uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT COALESCE(EXISTS(
  SELECT 1 FROM guardian_authority_relationships relationship
  JOIN users guardian ON guardian.id=relationship.guardian_user_id
  JOIN users subject ON subject.id=relationship.subject_user_id
  JOIN guardian_authority_policies policy ON policy.version=relationship.policy_version
  WHERE relationship.guardian_user_id=p_guardian_user_id AND relationship.subject_user_id=p_subject_user_id
   AND relationship.state='VERIFIED' AND NOT relationship.conflict AND guardian_authority_policy_operational(policy.version)
   AND relationship.verified_at<=clock_timestamp() AND relationship.review_due_at>clock_timestamp() AND relationship.verified_until>clock_timestamp()
   AND guardian.is_active AND guardian.erased_at IS NULL AND NOT guardian.is_dependent
   AND guardian.date_of_birth<=((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-INTERVAL '18 years')::date
   AND subject.is_active AND subject.erased_at IS NULL AND subject.is_dependent
   AND subject.date_of_birth>((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-INTERVAL '18 years')::date
 ),false);
$$;

CREATE OR REPLACE FUNCTION guardian_authority_create_dependent(p_name text,p_date_of_birth date,p_guardian_user_id uuid,p_invitation_digest bytea)
RETURNS SETOF users LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE guardian_row users%ROWTYPE;subject_row users%ROWTYPE;relationship_id uuid;now_at timestamptz:=clock_timestamp();invitation guardian_authority_invitations%ROWTYPE;has_membership boolean;
BEGIN
 PERFORM pg_advisory_xact_lock(110,110);
 IF (SELECT count(*) FROM guardian_authority_policies policy WHERE guardian_authority_policy_operational(policy.version))<>1 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_application_intake_unreleased'; END IF;
 SELECT * INTO guardian_row FROM users WHERE id=p_guardian_user_id FOR UPDATE;
 IF NOT FOUND OR NOT guardian_row.is_active OR guardian_row.erased_at IS NOT NULL OR guardian_row.is_dependent
  OR guardian_row.email IS NULL OR guardian_row.email_verified_at IS NULL
  OR guardian_row.date_of_birth>((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-INTERVAL '18 years')::date THEN RETURN; END IF;
 SELECT EXISTS(SELECT 1 FROM user_memberships membership WHERE membership.user_id=guardian_row.id
  AND membership.starts_on<=CURRENT_DATE AND (membership.ends_on IS NULL OR membership.ends_on>=CURRENT_DATE)) INTO has_membership;
 IF p_invitation_digest IS NOT NULL THEN
  IF octet_length(p_invitation_digest)<>32 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_authority_invitation_invalid'; END IF;
  SELECT * INTO invitation FROM guardian_authority_invitations WHERE token_digest=p_invitation_digest FOR UPDATE;
  IF NOT FOUND OR invitation.invited_email IS NULL OR invitation.invited_email<>guardian_row.email OR invitation.revoked_at IS NOT NULL
   OR invitation.consumed_at IS NOT NULL OR invitation.expires_at<=now_at THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_authority_invitation_invalid'; END IF;
 ELSIF NOT has_membership THEN RETURN; END IF;
 IF p_name IS NULL OR p_name<>btrim(p_name) OR char_length(p_name) NOT BETWEEN 2 AND 120 OR p_date_of_birth IS NULL
  OR p_date_of_birth>((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date)
  OR p_date_of_birth<=((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-INTERVAL '18 years')::date THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='guardian_authority_subject_rejected'; END IF;
 IF (SELECT count(*) FROM guardian_authority_relationships r JOIN users u ON u.id=r.subject_user_id
  WHERE r.guardian_user_id=p_guardian_user_id AND r.state NOT IN('EXPIRED','REJECTED') AND u.is_active AND u.erased_at IS NULL)>=10 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_authority_limit_reached'; END IF;
 INSERT INTO users(name,email,password_hash,guardian_id,is_dependent,date_of_birth) VALUES(p_name,NULL,NULL,NULL,true,p_date_of_birth) RETURNING * INTO subject_row;
 INSERT INTO guardian_authority_relationships(guardian_user_id,subject_user_id,submitted_label,state,created_at,updated_at)
 VALUES(p_guardian_user_id,subject_row.id,p_name,'PENDING',now_at,now_at) RETURNING id INTO relationship_id;
 INSERT INTO guardian_authority_events(relationship_id,relationship_version,actor_ref,actor_role,action,from_state,to_state,occurred_at)
 VALUES(relationship_id,1,p_guardian_user_id,'GUARDIAN','DECLARED',NULL,'PENDING',now_at);
 IF invitation.id IS NOT NULL THEN UPDATE guardian_authority_invitations SET invited_email=NULL,consumed_by=p_guardian_user_id,
  consumed_relationship_id=relationship_id,consumed_at=now_at WHERE id=invitation.id; END IF;
 RETURN NEXT subject_row;
END;$$;

CREATE OR REPLACE FUNCTION guardian_authority_reconcile_cutoffs() RETURNS bigint
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE relationship guardian_authority_relationships%ROWTYPE;now_at timestamptz:=clock_timestamp();reconciled bigint:=0;end_reason text;
BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended('mycfc/guardian-authority',0));
 PERFORM guardian_age_handoff_reconcile();
 FOR relationship IN SELECT authority.* FROM guardian_authority_relationships authority
  JOIN users guardian ON guardian.id=authority.guardian_user_id JOIN users subject ON subject.id=authority.subject_user_id
  LEFT JOIN guardian_authority_policies policy ON policy.version=authority.policy_version
  WHERE authority.state IN('VERIFIED','SUSPENDED') AND (policy.id IS NULL OR NOT guardian_authority_policy_operational(policy.version)
   OR authority.review_due_at<=now_at OR authority.verified_until<=now_at OR NOT guardian.is_active OR guardian.erased_at IS NOT NULL OR guardian.is_dependent
   OR guardian.date_of_birth>((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-INTERVAL '18 years')::date
   OR NOT subject.is_active OR subject.erased_at IS NOT NULL OR NOT subject.is_dependent
   OR subject.date_of_birth<=((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-INTERVAL '18 years')::date) FOR UPDATE OF authority
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
END;$$;

ALTER FUNCTION guardian_authority_admin_transition(uuid,uuid,bigint,text,text,text)
 RENAME TO guardian_authority_admin_transition_inner_004;
CREATE OR REPLACE FUNCTION guardian_authority_admin_transition(p_actor_id uuid,p_relationship_ref uuid,p_expected_version bigint,p_action text,p_evidence_category text,p_reason_code text)
RETURNS SETOF guardian_authority_relationships LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE active_policy_count integer;
BEGIN
 SELECT count(*) INTO active_policy_count FROM guardian_authority_policies policy WHERE guardian_authority_policy_operational(policy.version);
 IF active_policy_count<>1 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_authority_policy_unavailable'; END IF;
 RETURN QUERY SELECT * FROM guardian_authority_admin_transition_inner_004(p_actor_id,p_relationship_ref,p_expected_version,p_action,p_evidence_category,p_reason_code);
END;$$;
REVOKE ALL ON FUNCTION guardian_authority_admin_transition_inner_004(uuid,uuid,bigint,text,text,text) FROM PUBLIC;
DO $$BEGIN IF to_regrole('mycfc_app') IS NOT NULL THEN
 EXECUTE 'REVOKE EXECUTE ON FUNCTION guardian_authority_admin_transition_inner_004(uuid,uuid,bigint,text,text,text) FROM mycfc_app';
 EXECUTE 'GRANT EXECUTE ON FUNCTION guardian_authority_admin_transition(uuid,uuid,bigint,text,text,text) TO mycfc_app';
END IF;END$$;

-- Canonical v1 uses UTF-8, no insignificant whitespace, the exact field order
-- below, Go-compatible JSON string escaping, and the fixed compact policy.
-- Keeping the raw text argument prevents jsonb from erasing caller whitespace
-- or member order before this boundary validates and hashes it.
CREATE FUNCTION guardian_ops.canonical_policy() RETURNS bytea
LANGUAGE sql IMMUTABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT convert_to('{"contract":"guardian-authority-v2","evidence_categories":["CLUB_REGISTRATION_RECORD","IN_PERSON_ID_AND_CIVIL_RECORD","COURT_OR_LEGAL_AUTHORITY"],"reason_codes":["RELATIONSHIP_CONFIRMED","EVIDENCE_INSUFFICIENT","AUTHORITY_NOT_ESTABLISHED","CONFLICT","AUTHORITY_CHANGED","UNCERTAINTY","AUTHORITY_ENDED","ELIGIBILITY_ENDED","REVIEW_EXPIRED","MAJORITY_REACHED"],"validity_days":365,"review_days":365,"fresh_authentication_minutes":60,"invitation_validity_days":30,"submission_account_limit_24h":10,"submission_network_limit_24h":100,"renewal_period_months":12,"adult_age_years":18,"guardians_per_minor":1}','UTF8');
$$;
CREATE FUNCTION guardian_ops.canonical_approval(p_approval jsonb) RETURNS bytea
LANGUAGE sql IMMUTABLE SECURITY DEFINER SET search_path=pg_catalog,guardian_ops AS $$
 SELECT convert_to('{"contract":'||to_jsonb(p_approval->>'contract')::text||
  ',"authorized_operator_actor_ref":'||to_jsonb(p_approval->>'authorized_operator_actor_ref')::text||
  ',"expected_database":'||to_jsonb(p_approval->>'expected_database')::text||
  ',"controller_role":'||to_jsonb(p_approval->>'controller_role')::text||
  ',"controller_approval_reference":'||to_jsonb(p_approval->>'controller_approval_reference')::text||
  ',"controller_approved_on":'||to_jsonb(p_approval->>'controller_approved_on')::text||
  ',"effective_on":'||to_jsonb(p_approval->>'effective_on')::text||
  ',"review_due_on":'||to_jsonb(p_approval->>'review_due_on')::text||
  ',"legal_reviewer_reference":'||to_jsonb(p_approval->>'legal_reviewer_reference')::text||
  ',"legal_review_reference":'||to_jsonb(p_approval->>'legal_review_reference')::text||
  ',"legal_reviewed_on":'||to_jsonb(p_approval->>'legal_reviewed_on')::text||
  ',"legal_review_conclusion":'||to_jsonb(p_approval->>'legal_review_conclusion')::text||
  ',"policy":'||convert_from(guardian_ops.canonical_policy(),'UTF8')||
  ',"policy_sha256":'||to_jsonb(p_approval->>'policy_sha256')::text||
  ',"policy_version":'||to_jsonb(p_approval->>'policy_version')::text||'}','UTF8');
$$;

CREATE FUNCTION guardian_ops.status(p_expected_database text,p_current_image_digest text,p_embedded_schema_digest text)
RETURNS TABLE(state text,database_current boolean,migrations_current boolean,image_current boolean,contract_current boolean,
 policy_version text,policy_sha256 bytea,approval_sha256 bytea,pending_count bigint,current_relationship_count bigint,
 stale_relationship_count bigint,credential_count bigint,session_count bigint,active_invitation_count bigint)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 WITH active AS (
  SELECT gate.*,approval.review_due_on FROM guardian_application_intake_release gate
  LEFT JOIN guardian_authority_policy_approvals approval ON approval.policy_version=gate.policy_version WHERE gate.singleton
 ), counts AS (
  SELECT count(*) FILTER(WHERE relationship.state='PENDING') pending_count,
   count(*) FILTER(WHERE relationship.state='VERIFIED' AND guardian_authority_current(relationship.guardian_user_id,relationship.subject_user_id)) current_count,
   count(*) FILTER(WHERE relationship.state IN('VERIFIED','SUSPENDED') AND NOT guardian_authority_current(relationship.guardian_user_id,relationship.subject_user_id)) stale_count
  FROM guardian_authority_relationships relationship
 )
 SELECT CASE WHEN current_database()<>p_expected_database OR NOT guardian_ops.schema_ready(p_embedded_schema_digest)
	OR active.gate_version<>'guardian-intake-v2' OR (active.enabled AND (runtime.database_name<>current_database()
	 OR runtime.image_digest IS DISTINCT FROM active.image_digest OR runtime.schema_migration_digest IS DISTINCT FROM active.schema_migration_digest
	 OR active.image_digest IS DISTINCT FROM p_current_image_digest)) THEN 'BLOCKED'
	WHEN active.enabled AND guardian_authority_policy_operational(active.policy_version) THEN 'ENABLED'
	WHEN active.enabled THEN 'BLOCKED' ELSE 'DISABLED' END,
  current_database()=p_expected_database,guardian_ops.schema_ready(p_embedded_schema_digest),
  NOT active.enabled OR (active.image_digest=p_current_image_digest AND runtime.image_digest=active.image_digest),active.gate_version='guardian-intake-v2',
  active.policy_version,active.policy_sha256,active.approval_sha256,counts.pending_count,counts.current_count,counts.stale_count,
  (SELECT count(*) FROM users subject WHERE subject.is_dependent AND (subject.minor_login_id IS NOT NULL OR subject.password_hash IS NOT NULL)),
  (SELECT count(*) FROM sessions session JOIN users subject ON subject.id=session.user_id WHERE session.subject_indexed AND subject.is_dependent),
  (SELECT count(*) FROM guardian_authority_invitations invitation WHERE invitation.revoked_at IS NULL AND invitation.consumed_at IS NULL AND invitation.expires_at>clock_timestamp())
 FROM active,counts,guardian_ops.runtime_release_binding runtime WHERE runtime.singleton;
$$;

CREATE FUNCTION guardian_ops.preflight(p_actor uuid,p_approval_text text,p_approval_canonical bytea,p_policy_canonical bytea,
 p_current_image_digest text,p_embedded_schema_digest text,p_expected_database text)
RETURNS TABLE(ready boolean,already_enabled boolean,policy_version text,policy_sha256 bytea,approval_sha256 bytea,
 stale_relationship_count bigint,credential_count bigint,session_count bigint)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE p_approval jsonb;expected_policy jsonb;actual_policy_sha bytea;actual_approval_sha bytea;inventory_sha text;requested_version text;today date:=(clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date;
 gate guardian_application_intake_release%ROWTYPE;existing guardian_authority_policies%ROWTYPE;expected_keys text[];
BEGIN
 BEGIN p_approval:=p_approval_text::jsonb; EXCEPTION WHEN OTHERS THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='guardian_activation_approval_json_rejected'; END;
 expected_policy:='{"contract":"guardian-authority-v2","evidence_categories":["CLUB_REGISTRATION_RECORD","IN_PERSON_ID_AND_CIVIL_RECORD","COURT_OR_LEGAL_AUTHORITY"],"reason_codes":["RELATIONSHIP_CONFIRMED","EVIDENCE_INSUFFICIENT","AUTHORITY_NOT_ESTABLISHED","CONFLICT","AUTHORITY_CHANGED","UNCERTAINTY","AUTHORITY_ENDED","ELIGIBILITY_ENDED","REVIEW_EXPIRED","MAJORITY_REACHED"],"validity_days":365,"review_days":365,"fresh_authentication_minutes":60,"invitation_validity_days":30,"submission_account_limit_24h":10,"submission_network_limit_24h":100,"renewal_period_months":12,"adult_age_years":18,"guardians_per_minor":1}'::jsonb;
 expected_keys:=ARRAY['contract','authorized_operator_actor_ref','expected_database','controller_role','controller_approval_reference','controller_approved_on','effective_on','review_due_on','legal_reviewer_reference','legal_review_reference','legal_reviewed_on','legal_review_conclusion','policy','policy_sha256','policy_version'];
 IF current_database()<>p_expected_database OR p_actor IS NULL OR p_current_image_digest!~'^sha256:[0-9a-f]{64}$' OR p_embedded_schema_digest!~'^[0-9a-f]{64}$'
  OR octet_length(p_approval_canonical) NOT BETWEEN 2 AND 1048576 OR octet_length(p_policy_canonical) NOT BETWEEN 2 AND 1048576
  OR convert_to(p_approval_text,'UTF8')<>p_approval_canonical OR p_approval_canonical<>guardian_ops.canonical_approval(p_approval)
  OR p_policy_canonical<>guardian_ops.canonical_policy() OR convert_from(p_policy_canonical,'UTF8')::jsonb<>p_approval->'policy'
  OR ARRAY(SELECT jsonb_object_keys(p_approval) ORDER BY 1)<>ARRAY(SELECT unnest(expected_keys) ORDER BY 1)
  OR p_approval->>'contract'<>'mycfc/guardian-authority-policy-approval/v1' OR p_approval->>'controller_role'<>'CLUB_DIRECTION'
  OR p_approval->>'legal_review_conclusion'<>'APPROVED' OR p_approval->'policy'<>expected_policy
  OR p_approval->>'authorized_operator_actor_ref'<>p_actor::text
  OR p_approval->>'expected_database'<>p_expected_database
  OR p_approval->>'policy_version' IS NULL OR p_approval->>'policy_version'!~'^[A-Za-z0-9][A-Za-z0-9._/-]{0,79}$'
  OR btrim(COALESCE(p_approval->>'controller_approval_reference',''))='' OR char_length(p_approval->>'controller_approval_reference')>200
  OR btrim(COALESCE(p_approval->>'legal_reviewer_reference',''))='' OR char_length(p_approval->>'legal_reviewer_reference')>200
  OR btrim(COALESCE(p_approval->>'legal_review_reference',''))='' OR char_length(p_approval->>'legal_review_reference')>200 THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='guardian_activation_approval_rejected'; END IF;
 actual_policy_sha:=digest(p_policy_canonical,'sha256');actual_approval_sha:=digest(p_approval_canonical,'sha256');
 IF p_approval->>'policy_sha256'<>encode(actual_policy_sha,'hex') THEN RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='guardian_activation_policy_digest_rejected'; END IF;
 IF (p_approval->>'controller_approved_on')::date>(p_approval->>'effective_on')::date
  OR (p_approval->>'legal_reviewed_on')::date>(p_approval->>'effective_on')::date
  OR (p_approval->>'effective_on')::date>today OR (p_approval->>'review_due_on')::date<=today
  OR (p_approval->>'review_due_on')::date<=(p_approval->>'effective_on')::date THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='guardian_activation_approval_date_rejected'; END IF;
 IF NOT guardian_authority_is_administrator(p_actor) THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='guardian_activation_administrator_required'; END IF;
 inventory_sha:=CASE WHEN guardian_ops.schema_ready(p_embedded_schema_digest) THEN p_embedded_schema_digest ELSE NULL END;
 requested_version:=p_approval->>'policy_version';
 SELECT * INTO gate FROM guardian_application_intake_release WHERE singleton;
 SELECT * INTO existing FROM guardian_authority_policies WHERE version=requested_version;
 policy_version:=requested_version;policy_sha256:=actual_policy_sha;approval_sha256:=actual_approval_sha;
 SELECT count(*) INTO stale_relationship_count FROM guardian_authority_relationships WHERE state IN('VERIFIED','SUSPENDED');
 SELECT count(*) INTO credential_count FROM users subject WHERE subject.is_dependent AND (subject.minor_login_id IS NOT NULL OR subject.password_hash IS NOT NULL);
 SELECT count(*) INTO session_count FROM sessions session JOIN users subject ON subject.id=session.user_id WHERE session.subject_indexed AND subject.is_dependent;
 already_enabled:=gate.enabled AND gate.policy_version=requested_version AND gate.policy_sha256=actual_policy_sha AND gate.approval_sha256=actual_approval_sha
  AND gate.image_digest=p_current_image_digest AND gate.schema_migration_digest=p_embedded_schema_digest AND guardian_authority_policy_operational(requested_version);
 ready:=COALESCE(inventory_sha=p_embedded_schema_digest,false)
  AND gate.gate_version='guardian-intake-v2' AND (already_enabled OR (stale_relationship_count=0 AND credential_count=0 AND session_count=0))
  AND EXISTS(SELECT 1 FROM guardian_ops.runtime_release_binding runtime WHERE runtime.singleton AND runtime.database_name=current_database()
   AND runtime.image_digest=p_current_image_digest AND runtime.schema_migration_digest=p_embedded_schema_digest)
  AND NOT EXISTS(SELECT 1 FROM guardian_authority_policies policy WHERE policy.enabled AND policy.version<>requested_version)
  AND (existing.id IS NULL OR (existing.evidence_types=ARRAY['CLUB_REGISTRATION_RECORD','IN_PERSON_ID_AND_CIVIL_RECORD','COURT_OR_LEGAL_AUTHORITY']::text[]
   AND existing.reason_codes=ARRAY['RELATIONSHIP_CONFIRMED','EVIDENCE_INSUFFICIENT','AUTHORITY_NOT_ESTABLISHED','CONFLICT','AUTHORITY_CHANGED','UNCERTAINTY','AUTHORITY_ENDED','ELIGIBILITY_ENDED','REVIEW_EXPIRED','MAJORITY_REACHED']::text[]
   AND existing.validity_days=365 AND existing.review_days=365))
  AND (NOT gate.enabled OR already_enabled);
 RETURN NEXT;
END;$$;

CREATE FUNCTION guardian_ops.enable(p_actor uuid,p_approval_text text,p_approval_canonical bytea,p_policy_canonical bytea,
 p_current_image_digest text,p_embedded_schema_digest text,p_expected_database text)
RETURNS TABLE(changed boolean,policy_version text,policy_sha256 bytea,approval_sha256 bytea)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE check_row record;policy guardian_authority_policies%ROWTYPE;requested_version text;requested_policy_sha bytea;requested_approval_sha bytea;now_at timestamptz:=clock_timestamp();
BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended('mycfc/guardian-authority-policy',0));
 PERFORM pg_advisory_xact_lock(hashtextextended('mycfc-active-admin-set/v1',0));
 SELECT * INTO check_row FROM guardian_ops.preflight(p_actor,p_approval_text,p_approval_canonical,p_policy_canonical,p_current_image_digest,p_embedded_schema_digest,p_expected_database);
 IF NOT check_row.ready THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_activation_preflight_blocked'; END IF;
 requested_version:=check_row.policy_version;requested_policy_sha:=check_row.policy_sha256;requested_approval_sha:=check_row.approval_sha256;
 policy_version:=requested_version;policy_sha256:=requested_policy_sha;approval_sha256:=requested_approval_sha;
 IF check_row.already_enabled THEN changed:=false;RETURN NEXT;RETURN;END IF;
 INSERT INTO guardian_authority_policies(version,evidence_types,reason_codes,validity_days,review_days,adopted_at,adopted_by,enabled)
 VALUES(requested_version,ARRAY['CLUB_REGISTRATION_RECORD','IN_PERSON_ID_AND_CIVIL_RECORD','COURT_OR_LEGAL_AUTHORITY'],
  ARRAY['RELATIONSHIP_CONFIRMED','EVIDENCE_INSUFFICIENT','AUTHORITY_NOT_ESTABLISHED','CONFLICT','AUTHORITY_CHANGED','UNCERTAINTY','AUTHORITY_ENDED','ELIGIBILITY_ENDED','REVIEW_EXPIRED','MAJORITY_REACHED'],365,365,now_at,p_actor,false)
 ON CONFLICT(version) DO NOTHING;
 SELECT * INTO policy FROM guardian_authority_policies WHERE version=requested_version FOR UPDATE;
 IF NOT EXISTS(SELECT 1 FROM guardian_authority_policy_events event WHERE event.policy_id=policy.id AND event.action='ADOPTED') THEN
  INSERT INTO guardian_authority_policy_events(policy_id,policy_version,actor_ref,action,occurred_at) VALUES(policy.id,policy.version,p_actor,'ADOPTED',now_at); END IF;
 INSERT INTO guardian_authority_policy_approvals(policy_version,policy_sha256,approval_sha256,approval_contract,approval_canonical,expected_database,
  authorized_operator_actor_ref,controller_role,controller_approval_reference,controller_approved_on,effective_on,review_due_on,
  legal_reviewer_reference,legal_review_reference,legal_reviewed_on,legal_review_conclusion,bound_image_digest,bound_schema_migration_digest,bound_by,bound_at)
 VALUES(requested_version,requested_policy_sha,requested_approval_sha,(p_approval_text::jsonb)->>'contract',p_approval_canonical,p_expected_database,p_actor,(p_approval_text::jsonb)->>'controller_role',
  (p_approval_text::jsonb)->>'controller_approval_reference',((p_approval_text::jsonb)->>'controller_approved_on')::date,((p_approval_text::jsonb)->>'effective_on')::date,((p_approval_text::jsonb)->>'review_due_on')::date,
  (p_approval_text::jsonb)->>'legal_reviewer_reference',(p_approval_text::jsonb)->>'legal_review_reference',((p_approval_text::jsonb)->>'legal_reviewed_on')::date,(p_approval_text::jsonb)->>'legal_review_conclusion',
  p_current_image_digest,p_embedded_schema_digest,p_actor,now_at) ON CONFLICT ON CONSTRAINT guardian_authority_policy_approvals_pkey DO NOTHING;
 IF NOT EXISTS(SELECT 1 FROM guardian_authority_policy_approvals approval WHERE approval.policy_version=requested_version
  AND approval.policy_sha256=requested_policy_sha AND approval.approval_sha256=requested_approval_sha AND approval.bound_image_digest=p_current_image_digest
  AND approval.bound_schema_migration_digest=p_embedded_schema_digest) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_activation_binding_conflict'; END IF;
 UPDATE guardian_authority_policies SET enabled=true,enabled_at=now_at,enabled_by=p_actor WHERE version=requested_version AND NOT enabled;
 INSERT INTO guardian_authority_policy_events(policy_id,policy_version,actor_ref,action,occurred_at) VALUES(policy.id,policy.version,p_actor,'ENABLED',now_at);
 UPDATE guardian_application_intake_release SET enabled=true,policy_version=requested_version,policy_sha256=requested_policy_sha,approval_sha256=requested_approval_sha,
  image_digest=p_current_image_digest,schema_migration_digest=p_embedded_schema_digest,enabled_by=p_actor,enabled_at=now_at WHERE singleton AND NOT enabled;
 INSERT INTO guardian_application_intake_release_events(gate_version,action,actor_ref,policy_version,policy_sha256,approval_sha256,image_digest,schema_migration_digest)
 VALUES('guardian-intake-v2','ENABLED',p_actor,requested_version,requested_policy_sha,requested_approval_sha,p_current_image_digest,p_embedded_schema_digest);
 changed:=true;RETURN NEXT;
END;$$;

-- The supported release path can only remove authority and bind the database
-- to the next immutable image/schema. It cannot create approval evidence or
-- enable either the policy or intake gate.
CREATE FUNCTION guardian_ops.release_disable_and_bind(p_image_digest text,p_schema_migration_digest text,p_expected_database text)
RETURNS TABLE(generation bigint,relationship_count bigint,credential_count bigint,session_count bigint)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE gate guardian_application_intake_release%ROWTYPE;
BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended('mycfc/guardian-authority-policy',0));
 IF current_database()<>p_expected_database OR p_image_digest!~'^sha256:[0-9a-f]{64}$'
  OR NOT guardian_ops.schema_ready(p_schema_migration_digest) THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='guardian_release_binding_rejected'; END IF;
 SELECT * INTO gate FROM guardian_application_intake_release WHERE singleton FOR UPDATE;
 SELECT count(*) INTO relationship_count FROM guardian_authority_relationships WHERE state IN('VERIFIED','SUSPENDED');
 SELECT count(*) INTO credential_count FROM users subject WHERE subject.is_dependent AND (subject.minor_login_id IS NOT NULL OR subject.password_hash IS NOT NULL);
 SELECT count(*) INTO session_count FROM sessions session JOIN users subject ON subject.id=session.user_id WHERE session.subject_indexed AND subject.is_dependent;
 UPDATE guardian_application_intake_release SET enabled=false,policy_version=NULL,policy_sha256=NULL,approval_sha256=NULL,
  image_digest=NULL,schema_migration_digest=NULL,enabled_by=NULL,enabled_at=NULL WHERE singleton;
 WITH disabled AS (UPDATE guardian_authority_policies SET enabled=false,enabled_at=NULL,enabled_by=NULL WHERE enabled RETURNING id,version)
 INSERT INTO guardian_authority_policy_events(policy_id,policy_version,actor_ref,action) SELECT id,version,NULL,'MIGRATION_DISABLED' FROM disabled;
 PERFORM guardian_authority_reconcile_cutoffs();
 DELETE FROM sessions session USING users subject WHERE session.user_id=subject.id AND session.subject_indexed AND subject.is_dependent;
 UPDATE users SET minor_login_id=NULL,password_hash=NULL,credential_version=credential_version+1,updated_at=clock_timestamp()
  WHERE is_dependent AND (minor_login_id IS NOT NULL OR password_hash IS NOT NULL);
 UPDATE guardian_ops.runtime_release_binding SET database_name=p_expected_database,image_digest=p_image_digest,
  schema_migration_digest=p_schema_migration_digest,generation=guardian_ops.runtime_release_binding.generation+1,bound_at=clock_timestamp()
  WHERE singleton RETURNING guardian_ops.runtime_release_binding.generation INTO generation;
 INSERT INTO guardian_ops.runtime_release_binding_events(generation,database_name,image_digest,schema_migration_digest,
  relationship_count,credential_count,session_count)
 VALUES(generation,p_expected_database,p_image_digest,p_schema_migration_digest,relationship_count,credential_count,session_count);
 RETURN NEXT;
END;$$;

CREATE FUNCTION guardian_ops.disable(p_actor uuid,p_expected_database text)
RETURNS TABLE(policy_version text,relationship_count bigint,credential_count bigint,session_count bigint)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE gate guardian_application_intake_release%ROWTYPE;
BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended('mycfc/guardian-authority-policy',0));
 IF p_actor IS NULL OR current_database()<>p_expected_database THEN RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='guardian_activation_disable_rejected'; END IF;
 SELECT * INTO gate FROM guardian_application_intake_release WHERE singleton FOR UPDATE;
 IF (gate.enabled AND gate.enabled_by IS DISTINCT FROM p_actor) OR (NOT gate.enabled AND NOT EXISTS(
  SELECT 1 FROM guardian_authority_policy_approvals approval WHERE approval.authorized_operator_actor_ref=p_actor)) THEN
  RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='guardian_activation_disable_actor_rejected'; END IF;
 policy_version:=gate.policy_version;
 SELECT count(*) INTO relationship_count FROM guardian_authority_relationships WHERE state IN('VERIFIED','SUSPENDED');
 SELECT count(*) INTO credential_count FROM users subject WHERE subject.is_dependent AND (subject.minor_login_id IS NOT NULL OR subject.password_hash IS NOT NULL);
 SELECT count(*) INTO session_count FROM sessions session JOIN users subject ON subject.id=session.user_id WHERE session.subject_indexed AND subject.is_dependent;
 UPDATE guardian_application_intake_release SET enabled=false,policy_version=NULL,policy_sha256=NULL,approval_sha256=NULL,image_digest=NULL,schema_migration_digest=NULL,enabled_by=NULL,enabled_at=NULL WHERE singleton;
 WITH disabled AS (UPDATE guardian_authority_policies SET enabled=false,enabled_at=NULL,enabled_by=NULL WHERE enabled RETURNING id,version)
 INSERT INTO guardian_authority_policy_events(policy_id,policy_version,actor_ref,action) SELECT id,version,p_actor,'DISABLED' FROM disabled;
 PERFORM guardian_authority_reconcile_cutoffs();
 DELETE FROM sessions session USING users subject WHERE session.user_id=subject.id AND session.subject_indexed AND subject.is_dependent;
 UPDATE users SET minor_login_id=NULL,password_hash=NULL,credential_version=credential_version+1,updated_at=clock_timestamp()
  WHERE is_dependent AND (minor_login_id IS NOT NULL OR password_hash IS NOT NULL);
 INSERT INTO guardian_application_intake_release_events(gate_version,action,actor_ref,policy_version,policy_sha256,approval_sha256,image_digest,schema_migration_digest,relationship_count,credential_count,session_count)
 VALUES('guardian-intake-v2','DISABLED',p_actor,gate.policy_version,gate.policy_sha256,gate.approval_sha256,gate.image_digest,gate.schema_migration_digest,relationship_count,credential_count,session_count);
 RETURN NEXT;
END;$$;

REVOKE ALL ON TABLE guardian_authority_policy_approvals FROM PUBLIC;
REVOKE ALL ON FUNCTION guardian_authority_policy_operational(text) FROM PUBLIC;
REVOKE ALL ON ALL FUNCTIONS IN SCHEMA guardian_ops FROM PUBLIC;
REVOKE ALL ON ALL TABLES IN SCHEMA guardian_ops FROM PUBLIC;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA guardian_ops FROM PUBLIC;

-- The web role can use the application routines but can neither inspect nor
-- invoke the operational control plane.
DO $$BEGIN IF to_regrole('mycfc_app') IS NOT NULL THEN
 EXECUTE 'REVOKE ALL ON SCHEMA guardian_ops FROM mycfc_app';
 EXECUTE 'REVOKE ALL PRIVILEGES ON TABLE guardian_authority_policy_approvals FROM mycfc_app';
END IF;END$$;

-- Provisioning the fixed release login is a separate root-only operation. If
-- it already exists, migration can only reassert its narrow cutoff API; no
-- password is created or rotated here.
DO $$DECLARE protected_schema text; BEGIN IF to_regrole('mycfc_guardian_release_bind') IS NOT NULL THEN
 EXECUTE 'REVOKE ALL ON SCHEMA public FROM mycfc_guardian_release_bind';
 IF to_regnamespace('mycfc_meta') IS NOT NULL THEN EXECUTE 'REVOKE ALL ON SCHEMA mycfc_meta FROM mycfc_guardian_release_bind'; END IF;
 EXECUTE 'REVOKE EXECUTE ON ALL FUNCTIONS IN SCHEMA public FROM mycfc_guardian_release_bind';
 EXECUTE 'REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM mycfc_guardian_release_bind';
 EXECUTE 'REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public FROM mycfc_guardian_release_bind';
 FOREACH protected_schema IN ARRAY ARRAY['privacy_disable','privacy_protected'] LOOP
  IF to_regnamespace(protected_schema) IS NOT NULL THEN
   EXECUTE format('REVOKE ALL ON SCHEMA %I FROM mycfc_guardian_release_bind',protected_schema);
   EXECUTE format('REVOKE EXECUTE ON ALL FUNCTIONS IN SCHEMA %I FROM mycfc_guardian_release_bind',protected_schema);
   EXECUTE format('REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA %I FROM mycfc_guardian_release_bind',protected_schema);
   EXECUTE format('REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA %I FROM mycfc_guardian_release_bind',protected_schema);
  END IF;
 END LOOP;
 EXECUTE 'REVOKE EXECUTE ON ALL FUNCTIONS IN SCHEMA guardian_ops FROM mycfc_guardian_release_bind';
 EXECUTE 'REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA guardian_ops FROM mycfc_guardian_release_bind';
 EXECUTE 'REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA guardian_ops FROM mycfc_guardian_release_bind';
 EXECUTE 'GRANT USAGE ON SCHEMA guardian_ops TO mycfc_guardian_release_bind';
 EXECUTE 'GRANT EXECUTE ON FUNCTION guardian_ops.release_disable_and_bind(text,text,text) TO mycfc_guardian_release_bind';
END IF;END$$;

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
