-- Guardian authority is an explicit, reviewed capability. Legacy users.guardian_id
-- pointers are migrated to PENDING and then cleared so an old application binary
-- fails closed during a rolling deployment.

ALTER TABLE privacy_activation_authenticated_artifacts DROP CONSTRAINT privacy_activation_authenticated_artifacts_v6_check;
ALTER TABLE privacy_activation_authenticated_artifacts ADD CONSTRAINT privacy_activation_authenticated_artifacts_v7_check CHECK(
 ((kind='RESTORE' AND provider_inventory_contract IS NULL AND signing_key_id IS NULL AND immutable_evidence_ref IS NOT NULL AND schema_migration_digest IS NOT NULL
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
 OR (kind='INFRASTRUCTURE' AND provider_inventory_contract IS NULL AND signing_key_id IS NOT NULL AND immutable_evidence_ref IS NOT NULL
   AND production_state_serial>0 AND hetzner_state_serial>0 AND octet_length(production_state_sha256)=32 AND octet_length(hetzner_state_sha256)=32
   AND octet_length(production_plan_sha256)=32 AND octet_length(hetzner_plan_sha256)=32 AND worker_identity_enabled AND s3_version_deletion_enabled
   AND ledger_broker_invoke_enabled AND worker_monitoring_enabled AND restore_infrastructure_enabled AND restore_ledger_write_enabled)
 OR (kind='PROVIDER' AND signing_key_id IS NOT NULL AND immutable_evidence_ref IS NOT NULL AND provider_registry_state='READY'
   AND octet_length(provider_registry_sha256)=32
   AND ((provider_inventory_contract IS NULL AND provider_registration_count>0)
     OR (provider_inventory_contract='mycfc/privacy-provider-registry-source/v2' AND provider_registration_count=0)))
 OR (kind='SCHEMA' AND provider_inventory_contract IS NULL AND signing_key_id IS NOT NULL AND immutable_evidence_ref IS NOT NULL
   AND octet_length(schema_migration_digest)=32
   AND baseline_includes_through IN('202609100014_privacy_activation_broker','202609100015_privacy_membership_postcondition',
    '202609110001_privacy_upload_finalize_execution_fence','202609110002_privacy_empty_provider_registry_activation',
    '202609110003_privacy_activation_emergency_fence','202609110004_guardian_authority_verification'))) IS TRUE) NOT VALID;
ALTER TABLE privacy_activation_authenticated_artifacts VALIDATE CONSTRAINT privacy_activation_authenticated_artifacts_v7_check;

DO $$DECLARE definition text;old_clause text;new_clause text;
BEGIN
 SELECT pg_get_functiondef('public.privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)'::regprocedure) INTO definition;
 old_clause:='p_artifact->>''baseline_includes_through''<>''202609110003_privacy_activation_emergency_fence''';
 new_clause:='p_artifact->>''baseline_includes_through''<>''202609110004_guardian_authority_verification''';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_authority_activation_evidence_predecessor_mismatch'; END IF;
 EXECUTE replace(definition,old_clause,new_clause);

 SELECT pg_get_functiondef('public.privacy_activation_authenticated_set_digest(text,uuid[])'::regprocedure) INTO definition;
 old_clause:='schema_row.baseline_includes_through=''202609110003_privacy_activation_emergency_fence''';
 new_clause:='schema_row.baseline_includes_through=''202609110004_guardian_authority_verification''';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_authority_activation_digest_predecessor_mismatch'; END IF;
 EXECUTE replace(definition,old_clause,new_clause);
END$$;

CREATE TABLE guardian_authority_policies (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 version varchar(80) NOT NULL UNIQUE,
 evidence_types text[] NOT NULL,
 reason_codes text[] NOT NULL,
 validity_days integer NOT NULL,
 review_days integer NOT NULL,
 adopted_at timestamptz NOT NULL,
 adopted_by uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 enabled boolean NOT NULL DEFAULT false,
 enabled_at timestamptz NULL,
 enabled_by uuid NULL REFERENCES users(id) ON DELETE RESTRICT,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK(version=btrim(version) AND version~'^[A-Za-z0-9][A-Za-z0-9._/-]{0,79}$'),
 CHECK(cardinality(evidence_types)>0 AND array_position(evidence_types,NULL) IS NULL),
 CHECK(array_position(reason_codes,NULL) IS NULL),
 CHECK(validity_days BETWEEN 1 AND 3650 AND review_days BETWEEN 1 AND validity_days),
 CHECK((enabled AND enabled_at IS NOT NULL AND enabled_by IS NOT NULL) OR
       (NOT enabled AND enabled_at IS NULL AND enabled_by IS NULL))
);
CREATE UNIQUE INDEX guardian_authority_one_enabled_policy_uidx
 ON guardian_authority_policies((true)) WHERE enabled;

CREATE TABLE guardian_authority_policy_events (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 policy_id uuid NOT NULL REFERENCES guardian_authority_policies(id) ON DELETE RESTRICT,
 policy_version varchar(80) NOT NULL,
 actor_ref uuid NOT NULL,
 action varchar(20) NOT NULL CHECK(action IN('ADOPTED','ENABLED','DISABLED')),
 occurred_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE guardian_verifier_grants (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 granted_by uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 granted_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 revoked_by uuid NULL REFERENCES users(id) ON DELETE RESTRICT,
 revoked_at timestamptz NULL,
 CHECK(user_id<>granted_by),
 CHECK((revoked_by IS NULL AND revoked_at IS NULL) OR
       (revoked_by IS NOT NULL AND revoked_at IS NOT NULL AND revoked_at>=granted_at))
);
CREATE UNIQUE INDEX guardian_verifier_active_grant_uidx
 ON guardian_verifier_grants(user_id) WHERE revoked_at IS NULL;

CREATE TABLE guardian_verifier_grant_events (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 grant_id uuid NOT NULL REFERENCES guardian_verifier_grants(id) ON DELETE RESTRICT,
 actor_ref uuid NOT NULL,
 action varchar(20) NOT NULL CHECK(action IN('GRANTED','REVOKED')),
 occurred_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE guardian_authority_relationships (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 public_ref uuid NOT NULL DEFAULT gen_random_uuid() UNIQUE,
 guardian_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 subject_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT UNIQUE,
 submitted_label varchar(120) NOT NULL,
 state varchar(20) NOT NULL DEFAULT 'PENDING'
  CHECK(state IN('PENDING','VERIFIED','SUSPENDED','EXPIRED','REJECTED')),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 policy_version varchar(80) NULL REFERENCES guardian_authority_policies(version) ON DELETE RESTRICT,
 verified_at timestamptz NULL,
 verified_by uuid NULL REFERENCES users(id) ON DELETE RESTRICT,
 verified_until timestamptz NULL,
 review_due_at timestamptz NULL,
 conflict boolean NOT NULL DEFAULT false,
 conflict_actor_ref uuid NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK(guardian_user_id<>subject_user_id),
 CHECK(submitted_label=btrim(submitted_label) AND char_length(submitted_label) BETWEEN 1 AND 120),
 CHECK((state='PENDING' AND policy_version IS NULL AND verified_at IS NULL AND verified_by IS NULL
        AND verified_until IS NULL AND review_due_at IS NULL AND NOT conflict AND conflict_actor_ref IS NULL)
    OR state<>'PENDING'),
 CHECK((state<>'VERIFIED') OR
       (policy_version IS NOT NULL AND verified_at IS NOT NULL AND verified_by IS NOT NULL
        AND verified_until IS NOT NULL AND review_due_at IS NOT NULL
        AND verified_at<review_due_at AND review_due_at<=verified_until
        AND NOT conflict AND conflict_actor_ref IS NULL)),
 CHECK((conflict AND conflict_actor_ref IS NOT NULL) OR
       (NOT conflict AND conflict_actor_ref IS NULL))
);
CREATE INDEX guardian_authority_guardian_idx
 ON guardian_authority_relationships(guardian_user_id,state,created_at,id);
CREATE INDEX guardian_authority_queue_idx
 ON guardian_authority_relationships(state,created_at,id)
 WHERE state IN('PENDING','SUSPENDED');

CREATE TABLE guardian_authority_events (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 relationship_id uuid NOT NULL REFERENCES guardian_authority_relationships(id) ON DELETE RESTRICT,
 relationship_version bigint NOT NULL CHECK(relationship_version>0),
 actor_ref uuid NOT NULL,
 actor_role varchar(20) NOT NULL CHECK(actor_role IN('GUARDIAN','VERIFIER','SYSTEM')),
 action varchar(24) NOT NULL CHECK(action IN('DECLARED','VERIFIED','SUSPENDED','EXPIRED','REJECTED')),
 from_state varchar(20) NULL CHECK(from_state IS NULL OR from_state IN('PENDING','VERIFIED','SUSPENDED','EXPIRED','REJECTED')),
 to_state varchar(20) NOT NULL CHECK(to_state IN('PENDING','VERIFIED','SUSPENDED','EXPIRED','REJECTED')),
 policy_version varchar(80) NULL,
 evidence_type varchar(40) NULL,
 evidence_reference varchar(200) NULL,
 evidence_sha256 bytea NULL,
 reason_code varchar(40) NULL,
 occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 verified_until timestamptz NULL,
 review_due_at timestamptz NULL,
 UNIQUE(relationship_id,relationship_version),
 CHECK((evidence_type IS NULL AND evidence_reference IS NULL AND evidence_sha256 IS NULL) OR
       (evidence_type IS NOT NULL AND evidence_reference IS NOT NULL AND octet_length(evidence_sha256)=32)),
 CHECK(evidence_type IS NULL OR (evidence_type=btrim(evidence_type) AND evidence_type~'^[A-Z][A-Z0-9_]{1,39}$')),
 CHECK(evidence_reference IS NULL OR (evidence_reference=btrim(evidence_reference) AND char_length(evidence_reference) BETWEEN 1 AND 200
       AND evidence_reference~'^[A-Za-z0-9][A-Za-z0-9._:/-]*$')),
 CHECK(reason_code IS NULL OR (reason_code=btrim(reason_code) AND reason_code~'^[A-Z][A-Z0-9_]{1,39}$'))
);
CREATE INDEX guardian_authority_events_relationship_idx
 ON guardian_authority_events(relationship_id,relationship_version);

CREATE FUNCTION prevent_guardian_authority_event_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='guardian_authority_event_is_immutable';
END;
$$;
CREATE TRIGGER guardian_authority_events_immutable BEFORE UPDATE OR DELETE ON guardian_authority_events
 FOR EACH ROW EXECUTE FUNCTION prevent_guardian_authority_event_mutation();
CREATE TRIGGER guardian_verifier_grant_events_immutable BEFORE UPDATE OR DELETE ON guardian_verifier_grant_events
 FOR EACH ROW EXECUTE FUNCTION prevent_guardian_authority_event_mutation();
CREATE TRIGGER guardian_authority_policy_events_immutable BEFORE UPDATE OR DELETE ON guardian_authority_policy_events
 FOR EACH ROW EXECUTE FUNCTION prevent_guardian_authority_event_mutation();

CREATE FUNCTION enforce_guardian_authority_policy_immutability() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' OR NEW.id<>OLD.id OR NEW.version<>OLD.version OR NEW.evidence_types<>OLD.evidence_types
  OR NEW.reason_codes<>OLD.reason_codes OR NEW.validity_days<>OLD.validity_days OR NEW.review_days<>OLD.review_days
  OR NEW.adopted_at<>OLD.adopted_at OR NEW.adopted_by<>OLD.adopted_by OR NEW.created_at<>OLD.created_at THEN
  RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='guardian_authority_policy_is_immutable';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER guardian_authority_policy_immutable BEFORE UPDATE OR DELETE ON guardian_authority_policies
 FOR EACH ROW EXECUTE FUNCTION enforce_guardian_authority_policy_immutability();

CREATE FUNCTION enforce_guardian_verifier_grant_audit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM guardian_verifier_grant_events event
  WHERE event.grant_id=NEW.id AND event.action='GRANTED' AND event.actor_ref=NEW.granted_by) THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='guardian_verifier_grant_audit_required';
 END IF;
 IF NEW.revoked_at IS NOT NULL AND NOT EXISTS(SELECT 1 FROM guardian_verifier_grant_events event
  WHERE event.grant_id=NEW.id AND event.action='REVOKED' AND event.actor_ref=NEW.revoked_by) THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='guardian_verifier_revoke_audit_required';
 END IF;
 RETURN NEW;
END;
$$;
CREATE CONSTRAINT TRIGGER guardian_verifier_grants_audit_required
 AFTER INSERT OR UPDATE ON guardian_verifier_grants DEFERRABLE INITIALLY DEFERRED
 FOR EACH ROW EXECUTE FUNCTION enforce_guardian_verifier_grant_audit();

CREATE FUNCTION guardian_authority_is_administrator(p_actor_id uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT COALESCE(EXISTS(SELECT 1 FROM users actor
  JOIN user_platform_roles assignment ON assignment.user_id=actor.id
  JOIN platform_roles role ON role.id=assignment.role_id
  WHERE actor.id=p_actor_id AND actor.is_active AND actor.erased_at IS NULL AND NOT actor.is_dependent
   AND actor.date_of_birth<=((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-INTERVAL '18 years')::date
   AND role.code='ADMIN'),false);
$$;

CREATE FUNCTION guardian_authority_adopt_policy(p_actor_id uuid,p_version text,p_evidence_types text[],p_reason_codes text[],
 p_validity_days integer,p_review_days integer) RETURNS SETOF guardian_authority_policies
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE policy guardian_authority_policies%ROWTYPE;now_at timestamptz:=clock_timestamp();
BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended('mycfc/guardian-authority-policy',0));
 IF NOT guardian_authority_is_administrator(p_actor_id) THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='guardian_authority_policy_administrator_required'; END IF;
 INSERT INTO guardian_authority_policies(version,evidence_types,reason_codes,validity_days,review_days,adopted_at,adopted_by,enabled)
 VALUES(p_version,p_evidence_types,p_reason_codes,p_validity_days,p_review_days,now_at,p_actor_id,false) RETURNING * INTO policy;
 INSERT INTO guardian_authority_policy_events(policy_id,policy_version,actor_ref,action,occurred_at)
 VALUES(policy.id,policy.version,p_actor_id,'ADOPTED',now_at);
 RETURN NEXT policy;
END;
$$;

CREATE FUNCTION guardian_authority_set_policy_enabled(p_actor_id uuid,p_version text,p_enabled boolean)
RETURNS SETOF guardian_authority_policies LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE policy guardian_authority_policies%ROWTYPE;now_at timestamptz:=clock_timestamp();
BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended('mycfc/guardian-authority-policy',0));
 IF NOT guardian_authority_is_administrator(p_actor_id) THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='guardian_authority_policy_administrator_required'; END IF;
 UPDATE guardian_authority_policies SET enabled=p_enabled,enabled_at=CASE WHEN p_enabled THEN now_at ELSE NULL END,
  enabled_by=CASE WHEN p_enabled THEN p_actor_id ELSE NULL END
 WHERE version=p_version AND enabled IS DISTINCT FROM p_enabled RETURNING * INTO policy;
 IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_authority_policy_transition_rejected'; END IF;
 INSERT INTO guardian_authority_policy_events(policy_id,policy_version,actor_ref,action,occurred_at)
 VALUES(policy.id,policy.version,p_actor_id,CASE WHEN p_enabled THEN 'ENABLED' ELSE 'DISABLED' END,now_at);
 RETURN NEXT policy;
END;
$$;

CREATE FUNCTION guardian_authority_grant_verifier(p_actor_id uuid,p_user_id uuid)
RETURNS SETOF guardian_verifier_grants LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE grant_row guardian_verifier_grants%ROWTYPE;now_at timestamptz:=clock_timestamp();
BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended('mycfc/guardian-verifier-grants',0));
 IF NOT guardian_authority_is_administrator(p_actor_id) OR p_actor_id=p_user_id THEN
  RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='guardian_verifier_grant_rejected'; END IF;
 IF NOT EXISTS(SELECT 1 FROM users candidate WHERE candidate.id=p_user_id AND candidate.is_active
  AND candidate.erased_at IS NULL AND NOT candidate.is_dependent
  AND candidate.date_of_birth<=((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-INTERVAL '18 years')::date) THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='guardian_verifier_candidate_rejected'; END IF;
 INSERT INTO guardian_verifier_grants(user_id,granted_by,granted_at) VALUES(p_user_id,p_actor_id,now_at) RETURNING * INTO grant_row;
 INSERT INTO guardian_verifier_grant_events(grant_id,actor_ref,action,occurred_at) VALUES(grant_row.id,p_actor_id,'GRANTED',now_at);
 RETURN NEXT grant_row;
END;
$$;

CREATE FUNCTION guardian_authority_revoke_verifier(p_actor_id uuid,p_user_id uuid)
RETURNS SETOF guardian_verifier_grants LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE grant_row guardian_verifier_grants%ROWTYPE;now_at timestamptz:=clock_timestamp();
BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended('mycfc/guardian-verifier-grants',0));
 IF NOT guardian_authority_is_administrator(p_actor_id) OR p_actor_id=p_user_id THEN
  RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='guardian_verifier_revoke_rejected'; END IF;
 UPDATE guardian_verifier_grants SET revoked_by=p_actor_id,revoked_at=now_at
 WHERE user_id=p_user_id AND revoked_at IS NULL RETURNING * INTO grant_row;
 IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_verifier_revoke_rejected'; END IF;
 INSERT INTO guardian_verifier_grant_events(grant_id,actor_ref,action,occurred_at) VALUES(grant_row.id,p_actor_id,'REVOKED',now_at);
 RETURN NEXT grant_row;
END;
$$;

CREATE FUNCTION guardian_authority_can_verify(p_actor_id uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT COALESCE(EXISTS(
  SELECT 1 FROM guardian_verifier_grants verifier
  JOIN users actor ON actor.id=verifier.user_id
  WHERE verifier.user_id=p_actor_id AND verifier.revoked_at IS NULL
   AND EXISTS(SELECT 1 FROM guardian_verifier_grant_events event
              WHERE event.grant_id=verifier.id AND event.action='GRANTED' AND event.actor_ref=verifier.granted_by)
   AND actor.is_active AND actor.erased_at IS NULL AND NOT actor.is_dependent
   AND actor.date_of_birth<=((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-INTERVAL '18 years')::date
   AND EXISTS(SELECT 1 FROM guardian_authority_policies policy
              WHERE policy.enabled AND policy.adopted_at<=clock_timestamp())
 ),false);
$$;

CREATE FUNCTION guardian_authority_current(p_guardian_user_id uuid,p_subject_user_id uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT COALESCE(EXISTS(
  SELECT 1 FROM guardian_authority_relationships relationship
  JOIN users guardian ON guardian.id=relationship.guardian_user_id
  JOIN users subject ON subject.id=relationship.subject_user_id
  JOIN guardian_authority_policies policy ON policy.version=relationship.policy_version
  WHERE relationship.guardian_user_id=p_guardian_user_id
   AND relationship.subject_user_id=p_subject_user_id
   AND relationship.state='VERIFIED' AND NOT relationship.conflict
   AND policy.enabled AND policy.adopted_at<=clock_timestamp()
   AND relationship.verified_at<=clock_timestamp()
   AND relationship.review_due_at>clock_timestamp()
   AND relationship.verified_until>clock_timestamp()
   AND guardian.is_active AND guardian.erased_at IS NULL AND NOT guardian.is_dependent
   AND guardian.date_of_birth<=((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-INTERVAL '18 years')::date
   AND subject.is_active AND subject.erased_at IS NULL AND subject.is_dependent
   AND subject.date_of_birth>((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-INTERVAL '18 years')::date
 ),false);
$$;

CREATE FUNCTION guardian_authority_create_dependent(p_name text,p_date_of_birth date,p_guardian_user_id uuid)
RETURNS SETOF users LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE guardian_row users%ROWTYPE;subject_row users%ROWTYPE;relationship_ref uuid;now_at timestamptz;
BEGIN
 PERFORM pg_advisory_xact_lock(110,110);
 now_at:=clock_timestamp();
 IF (SELECT count(*) FROM guardian_authority_policies WHERE enabled AND adopted_at<=now_at)<>1 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_authority_policy_unavailable';
 END IF;
 SELECT * INTO guardian_row FROM users WHERE id=p_guardian_user_id FOR UPDATE;
 IF NOT FOUND OR NOT guardian_row.is_active OR guardian_row.erased_at IS NOT NULL OR guardian_row.is_dependent
  OR guardian_row.date_of_birth>((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-INTERVAL '18 years')::date THEN
  RETURN;
 END IF;
 IF p_name IS NULL OR p_name<>btrim(p_name) OR char_length(p_name) NOT BETWEEN 2 AND 120
  OR p_date_of_birth IS NULL OR p_date_of_birth>((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date)
  OR p_date_of_birth<=((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-INTERVAL '18 years')::date THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='guardian_authority_subject_rejected';
 END IF;
 IF (SELECT count(*) FROM guardian_authority_relationships r JOIN users u ON u.id=r.subject_user_id
     WHERE r.guardian_user_id=p_guardian_user_id AND r.state NOT IN('EXPIRED','REJECTED')
      AND u.is_active AND u.erased_at IS NULL)>=10 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_authority_limit_reached';
 END IF;
 INSERT INTO users(name,email,password_hash,guardian_id,is_dependent,date_of_birth)
 VALUES(p_name,NULL,NULL,NULL,true,p_date_of_birth) RETURNING * INTO subject_row;
 INSERT INTO guardian_authority_relationships(guardian_user_id,subject_user_id,submitted_label,state,created_at,updated_at)
 VALUES(p_guardian_user_id,subject_row.id,p_name,'PENDING',now_at,now_at) RETURNING id INTO relationship_ref;
 INSERT INTO guardian_authority_events(relationship_id,relationship_version,actor_ref,actor_role,action,from_state,to_state,occurred_at)
 VALUES(relationship_ref,1,p_guardian_user_id,'GUARDIAN','DECLARED',NULL,'PENDING',now_at);
 RETURN NEXT subject_row;
END;
$$;

CREATE FUNCTION guardian_authority_transition(
 p_actor_id uuid,p_relationship_ref uuid,p_expected_version bigint,p_target_state text,
 p_evidence_type text,p_evidence_reference text,p_evidence_sha256 bytea,p_reason_code text)
RETURNS SETOF guardian_authority_relationships
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE current_row guardian_authority_relationships%ROWTYPE;policy_row guardian_authority_policies%ROWTYPE;
 now_at timestamptz;new_version bigint;new_verified_until timestamptz;new_review_due_at timestamptz;
 new_conflict boolean;new_conflict_actor uuid;old_state text;
BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended('mycfc/guardian-authority',0));
 now_at:=clock_timestamp();
 IF NOT guardian_authority_can_verify(p_actor_id) THEN
  RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='guardian_authority_verifier_required';
 END IF;
 SELECT * INTO current_row FROM guardian_authority_relationships WHERE public_ref=p_relationship_ref FOR UPDATE;
 IF NOT FOUND OR current_row.version<>p_expected_version THEN
  RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='guardian_authority_stale';
 END IF;
 IF p_actor_id IN(current_row.guardian_user_id,current_row.subject_user_id)
  OR current_row.conflict_actor_ref=p_actor_id THEN
  RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='guardian_authority_separation_required';
 END IF;
 SELECT * INTO policy_row FROM guardian_authority_policies
  WHERE enabled AND adopted_at<=now_at FOR SHARE;
 IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_authority_policy_unavailable'; END IF;
 IF p_target_state NOT IN('VERIFIED','SUSPENDED','EXPIRED','REJECTED') THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='guardian_authority_transition_rejected';
 END IF;
 IF current_row.state='REJECTED'
  OR (current_row.state='PENDING' AND p_target_state NOT IN('VERIFIED','SUSPENDED','REJECTED'))
  OR (current_row.state='VERIFIED' AND p_target_state NOT IN('VERIFIED','SUSPENDED','EXPIRED'))
  OR (current_row.state='SUSPENDED' AND p_target_state NOT IN('VERIFIED','EXPIRED'))
  OR (current_row.state='EXPIRED' AND p_target_state<>'VERIFIED') THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='guardian_authority_transition_rejected';
 END IF;
 IF p_target_state='VERIFIED' AND NOT EXISTS(
  SELECT 1 FROM users guardian JOIN users subject ON subject.id=current_row.subject_user_id
  WHERE guardian.id=current_row.guardian_user_id AND guardian.is_active AND guardian.erased_at IS NULL AND NOT guardian.is_dependent
   AND guardian.date_of_birth<=((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-INTERVAL '18 years')::date
   AND subject.is_active AND subject.erased_at IS NULL AND subject.is_dependent
   AND subject.date_of_birth>((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-INTERVAL '18 years')::date
 ) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_authority_relationship_ineligible'; END IF;
 IF p_target_state='EXPIRED' AND current_row.state='VERIFIED'
  AND current_row.review_due_at>now_at AND current_row.verified_until>now_at THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='guardian_authority_not_due';
 END IF;
 IF p_target_state IN('VERIFIED','REJECTED') THEN
  IF p_evidence_type IS NULL OR NOT (p_evidence_type=ANY(policy_row.evidence_types))
   OR p_evidence_reference IS NULL OR p_evidence_reference<>btrim(p_evidence_reference)
   OR char_length(p_evidence_reference) NOT BETWEEN 1 AND 200
   OR p_evidence_reference!~'^[A-Za-z0-9][A-Za-z0-9._:/-]*$'
   OR octet_length(p_evidence_sha256)<>32 THEN
   RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='guardian_authority_evidence_rejected';
  END IF;
 ELSIF p_evidence_type IS NOT NULL OR p_evidence_reference IS NOT NULL OR p_evidence_sha256 IS NOT NULL THEN
  IF p_evidence_type IS NULL OR NOT (p_evidence_type=ANY(policy_row.evidence_types))
   OR p_evidence_reference IS NULL OR p_evidence_reference<>btrim(p_evidence_reference)
   OR char_length(p_evidence_reference) NOT BETWEEN 1 AND 200 OR octet_length(p_evidence_sha256)<>32 THEN
   RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='guardian_authority_evidence_rejected';
  END IF;
 END IF;
 IF p_target_state IN('SUSPENDED','REJECTED') AND
  (p_reason_code IS NULL OR NOT (p_reason_code=ANY(policy_row.reason_codes))) THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='guardian_authority_reason_rejected';
 END IF;
 IF p_target_state NOT IN('SUSPENDED','REJECTED') AND p_reason_code IS NOT NULL
  AND NOT (p_reason_code=ANY(policy_row.reason_codes)) THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='guardian_authority_reason_rejected';
 END IF;
 new_version:=current_row.version+1;
 IF p_target_state='VERIFIED' THEN
  new_review_due_at:=now_at+make_interval(days=>policy_row.review_days);
  new_verified_until:=now_at+make_interval(days=>policy_row.validity_days);
  new_conflict:=false;new_conflict_actor:=NULL;
 ELSE
  new_review_due_at:=current_row.review_due_at;new_verified_until:=current_row.verified_until;
  new_conflict:=(p_target_state='SUSPENDED' AND p_reason_code='CONFLICT');
  new_conflict_actor:=CASE WHEN new_conflict THEN p_actor_id ELSE NULL END;
 END IF;
 old_state:=current_row.state;
 UPDATE guardian_authority_relationships SET state=p_target_state,version=new_version,policy_version=policy_row.version,
  verified_at=CASE WHEN p_target_state='VERIFIED' THEN now_at ELSE verified_at END,
  verified_by=CASE WHEN p_target_state='VERIFIED' THEN p_actor_id ELSE verified_by END,
  verified_until=new_verified_until,review_due_at=new_review_due_at,
  conflict=new_conflict,conflict_actor_ref=new_conflict_actor,updated_at=now_at
 WHERE id=current_row.id AND version=p_expected_version RETURNING * INTO current_row;
 IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='guardian_authority_stale'; END IF;
 INSERT INTO guardian_authority_events(relationship_id,relationship_version,actor_ref,actor_role,action,from_state,to_state,
 policy_version,evidence_type,evidence_reference,evidence_sha256,reason_code,occurred_at,verified_until,review_due_at)
 VALUES(current_row.id,new_version,p_actor_id,'VERIFIER',p_target_state,old_state,p_target_state,
  policy_row.version,p_evidence_type,p_evidence_reference,p_evidence_sha256,p_reason_code,now_at,new_verified_until,new_review_due_at);
 RETURN NEXT current_row;
END;
$$;

CREATE FUNCTION guardian_authority_privacy_account_for_update(p_user_id uuid) RETURNS SETOF users
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE account users%ROWTYPE;relationship guardian_authority_relationships%ROWTYPE;
BEGIN
 SELECT * INTO account FROM users WHERE id=p_user_id FOR UPDATE;
 IF NOT FOUND THEN RETURN; END IF;
 IF account.is_dependent THEN
  SELECT * INTO relationship FROM guardian_authority_relationships
   WHERE subject_user_id=account.id FOR UPDATE;
  IF FOUND AND guardian_authority_current(relationship.guardian_user_id,account.id) THEN
   account.guardian_id:=relationship.guardian_user_id;
   account.updated_at:=relationship.updated_at;
  ELSE account.guardian_id:=NULL;
  END IF;
 END IF;
 RETURN NEXT account;
END;
$$;

CREATE FUNCTION guardian_authority_privacy_dependants_for_update(p_guardian_user_id uuid) RETURNS SETOF users
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE account users%ROWTYPE;relationship guardian_authority_relationships%ROWTYPE;
BEGIN
 FOR relationship IN SELECT * FROM guardian_authority_relationships
  WHERE guardian_user_id=p_guardian_user_id ORDER BY subject_user_id FOR UPDATE
 LOOP
  CONTINUE WHEN NOT guardian_authority_current(p_guardian_user_id,relationship.subject_user_id);
  SELECT * INTO account FROM users WHERE id=relationship.subject_user_id FOR UPDATE;
  account.guardian_id:=p_guardian_user_id;
  account.updated_at:=relationship.updated_at;
  RETURN NEXT account;
 END LOOP;
END;
$$;

CREATE OR REPLACE FUNCTION public.privacy_upload_remove(p_intent_id uuid,p_actor_user_id uuid,p_expected_source_kind text,p_expected_source_ref uuid) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE r privacy_protected.object_upload_intents%ROWTYPE; v_status text; v_sequence integer;
BEGIN
 IF p_intent_id IS NULL OR p_actor_user_id IS NULL OR p_expected_source_kind<>'MEMBER_PROFILE_PHOTO' THEN RAISE EXCEPTION 'upload removal rejected'; END IF;
 SELECT * INTO r FROM privacy_protected.object_upload_intents WHERE id=p_intent_id FOR UPDATE;
 IF r.id IS NULL THEN RAISE EXCEPTION 'upload intent is unavailable'; END IF;
 IF r.source_kind<>p_expected_source_kind OR r.source_ref<>p_expected_source_ref THEN RAISE EXCEPTION 'upload removal rejected'; END IF;
 IF p_actor_user_id<>r.source_ref AND NOT EXISTS(
   SELECT 1 WHERE public.guardian_authority_current(p_actor_user_id,r.source_ref)
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

CREATE VIEW guardian_authority_guardian_disclosures AS
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
  COALESCE(profile.emergency_contact_name<>'' AND profile.emergency_contact_relationship<>''
   AND profile.emergency_contact_phone<>'' AND profile.medical_declaration<>'UNKNOWN',false)
  ELSE NULL::boolean END AS profile_complete
FROM guardian_authority_relationships relationship
JOIN users subject ON subject.id=relationship.subject_user_id
LEFT JOIN member_profiles profile ON profile.user_id=subject.id;

-- Capture every legacy declaration before removing the authorization pointer.
INSERT INTO guardian_authority_relationships(guardian_user_id,subject_user_id,submitted_label,state,created_at,updated_at)
 SELECT guardian_id,id,name,'PENDING',created_at,clock_timestamp()
 FROM users WHERE is_dependent AND guardian_id IS NOT NULL AND erased_at IS NULL;
INSERT INTO guardian_authority_events(relationship_id,relationship_version,actor_ref,actor_role,action,from_state,to_state,occurred_at)
 SELECT id,1,guardian_user_id,'GUARDIAN','DECLARED',NULL,'PENDING',created_at
 FROM guardian_authority_relationships;

DROP TRIGGER users_active_guardian_attachment ON users;
ALTER TABLE users DROP CONSTRAINT users_identity_shape;
UPDATE users SET guardian_id=NULL WHERE guardian_id IS NOT NULL;
ALTER TABLE users ADD CONSTRAINT users_identity_shape CHECK (
 (erased_at IS NULL AND erasure_execution_id IS NULL AND erasure_replay_run_id IS NULL AND (
   (is_dependent AND guardian_id IS NULL AND email IS NULL AND
    ((minor_login_id IS NULL AND password_hash IS NULL) OR (minor_login_id IS NOT NULL AND password_hash IS NOT NULL)))
   OR
   (NOT is_dependent AND guardian_id IS NULL AND email IS NOT NULL AND password_hash IS NOT NULL AND minor_login_id IS NULL)
 ))
 OR
 (erased_at IS NOT NULL AND NOT is_active AND NOT leaderboard_visible AND NOT is_dependent AND guardian_id IS NULL
  AND email IS NULL AND email_verified_at IS NULL AND minor_login_id IS NULL AND password_hash IS NULL
  AND name='Conta eliminada' AND date_of_birth=DATE '1900-01-01'
  AND num_nonnulls(erasure_execution_id,erasure_replay_run_id)=1)
) NOT VALID;
ALTER TABLE users VALIDATE CONSTRAINT users_identity_shape;

CREATE FUNCTION reject_legacy_guardian_pointer() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.guardian_id IS NOT NULL THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='legacy_guardian_pointer_rejected';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER users_legacy_guardian_pointer_guard BEFORE INSERT OR UPDATE OF guardian_id ON users
 FOR EACH ROW EXECUTE FUNCTION reject_legacy_guardian_pointer();

REVOKE ALL ON TABLE guardian_authority_policies,guardian_authority_policy_events,guardian_verifier_grants,guardian_verifier_grant_events,
 guardian_authority_relationships,guardian_authority_events FROM PUBLIC;
REVOKE ALL ON FUNCTION guardian_authority_can_verify(uuid),guardian_authority_current(uuid,uuid),
 guardian_authority_create_dependent(text,date,uuid),guardian_authority_transition(uuid,uuid,bigint,text,text,text,bytea,text),
 guardian_authority_privacy_account_for_update(uuid),guardian_authority_privacy_dependants_for_update(uuid),
 guardian_authority_is_administrator(uuid),guardian_authority_adopt_policy(uuid,text,text[],text[],integer,integer),
 guardian_authority_set_policy_enabled(uuid,text,boolean),guardian_authority_grant_verifier(uuid,uuid),guardian_authority_revoke_verifier(uuid,uuid)
 FROM PUBLIC;

-- This schema revision invalidates every prior privacy activation artifact.
UPDATE privacy_request_activation SET enabled=false,fulfilment_ready=false,approval_id=NULL,updated_at=clock_timestamp() WHERE singleton;
UPDATE privacy_worker_kill_switch SET engaged=true,version=version+1,activation_approval_id=NULL,changed_at=clock_timestamp() WHERE singleton;
INSERT INTO privacy_worker_kill_switch_events(version,engaged,occurred_at)
 SELECT version,true,changed_at FROM privacy_worker_kill_switch WHERE singleton;
