-- Materialize time/eligibility cutoffs and prevent dormant minor credentials
-- from becoming usable again after authority renewal.

ALTER TABLE privacy_activation_authenticated_artifacts DROP CONSTRAINT privacy_activation_authenticated_artifacts_v7_check;
ALTER TABLE privacy_activation_authenticated_artifacts ADD CONSTRAINT privacy_activation_authenticated_artifacts_v8_check CHECK(
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
    '202609110003_privacy_activation_emergency_fence','202609110004_guardian_authority_verification',
    '202609110005_guardian_authority_cutoff_reconciliation'))) IS TRUE) NOT VALID;
ALTER TABLE privacy_activation_authenticated_artifacts VALIDATE CONSTRAINT privacy_activation_authenticated_artifacts_v8_check;

DO $$DECLARE definition text;old_clause text;new_clause text;
BEGIN
 SELECT pg_get_functiondef('public.privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)'::regprocedure) INTO definition;
 old_clause:='p_artifact->>''baseline_includes_through''<>''202609110004_guardian_authority_verification''';
 new_clause:='p_artifact->>''baseline_includes_through''<>''202609110005_guardian_authority_cutoff_reconciliation''';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_authority_cutoff_activation_evidence_predecessor_mismatch'; END IF;
 EXECUTE replace(definition,old_clause,new_clause);

 SELECT pg_get_functiondef('public.privacy_activation_authenticated_set_digest(text,uuid[])'::regprocedure) INTO definition;
 old_clause:='schema_row.baseline_includes_through=''202609110004_guardian_authority_verification''';
 new_clause:='schema_row.baseline_includes_through=''202609110005_guardian_authority_cutoff_reconciliation''';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_authority_cutoff_activation_digest_predecessor_mismatch'; END IF;
 EXECUTE replace(definition,old_clause,new_clause);
END$$;

CREATE FUNCTION guardian_authority_codes_valid(p_codes text[]) RETURNS boolean
LANGUAGE sql IMMUTABLE PARALLEL SAFE SET search_path=pg_catalog AS $$
 SELECT cardinality(p_codes)>0 AND array_position(p_codes,NULL) IS NULL
  AND NOT EXISTS(SELECT 1 FROM unnest(p_codes) code WHERE code!~'^[A-Z][A-Z0-9_]{1,39}$')
$$;

ALTER TABLE guardian_authority_policies
 DROP CONSTRAINT guardian_authority_policies_evidence_types_check,
 DROP CONSTRAINT guardian_authority_policies_reason_codes_check;
ALTER TABLE guardian_authority_policies
 ADD CONSTRAINT guardian_authority_policies_evidence_types_v2_check CHECK(guardian_authority_codes_valid(evidence_types)) NOT VALID,
 ADD CONSTRAINT guardian_authority_policies_reason_codes_v2_check CHECK(guardian_authority_codes_valid(reason_codes) AND 'CONFLICT'=ANY(reason_codes)) NOT VALID;
ALTER TABLE guardian_authority_policies VALIDATE CONSTRAINT guardian_authority_policies_evidence_types_v2_check;
ALTER TABLE guardian_authority_policies VALIDATE CONSTRAINT guardian_authority_policies_reason_codes_v2_check;

ALTER TABLE guardian_authority_events ALTER COLUMN actor_ref DROP NOT NULL;
ALTER TABLE guardian_authority_events
 ADD CONSTRAINT guardian_authority_events_actor_v2_check
 CHECK((actor_role='SYSTEM' AND actor_ref IS NULL) OR (actor_role<>'SYSTEM' AND actor_ref IS NOT NULL)) NOT VALID;

-- The predecessor converted legacy guardian pointers into version-one pending
-- declarations. Its exact created-at equality distinguishes those imported
-- rows from declarations made by guardian_authority_create_dependent().
CREATE TEMP TABLE guardian_authority_legacy_imports ON COMMIT DROP AS
 SELECT relationship.id,relationship.subject_user_id
 FROM guardian_authority_relationships relationship
 JOIN users subject ON subject.id=relationship.subject_user_id
 JOIN guardian_authority_events event ON event.relationship_id=relationship.id
  AND event.relationship_version=1
 WHERE relationship.version=1 AND relationship.state='PENDING'
  AND relationship.created_at=subject.created_at
  AND event.actor_role='GUARDIAN' AND event.actor_ref=relationship.guardian_user_id
  AND event.action='DECLARED' AND event.from_state IS NULL AND event.to_state='PENDING';

ALTER TABLE guardian_authority_events DISABLE TRIGGER guardian_authority_events_immutable;
UPDATE guardian_authority_events event SET actor_ref=NULL,actor_role='SYSTEM',occurred_at=clock_timestamp()
 FROM guardian_authority_legacy_imports legacy WHERE legacy.id=event.relationship_id AND event.relationship_version=1;
ALTER TABLE guardian_authority_events ENABLE TRIGGER guardian_authority_events_immutable;
ALTER TABLE guardian_authority_events VALIDATE CONSTRAINT guardian_authority_events_actor_v2_check;

DELETE FROM sessions session_row USING guardian_authority_relationships relationship
 WHERE session_row.subject_indexed AND session_row.user_id=relationship.subject_user_id
  AND relationship.state='PENDING';
UPDATE users subject SET minor_login_id=NULL,password_hash=NULL,
 credential_version=credential_version+1,updated_at=clock_timestamp()
 FROM guardian_authority_relationships relationship
 WHERE relationship.subject_user_id=subject.id AND relationship.state='PENDING'
  AND (subject.minor_login_id IS NOT NULL OR subject.password_hash IS NOT NULL);

-- This schema revision invalidates every prior privacy activation artifact.
UPDATE privacy_request_activation SET enabled=false,fulfilment_ready=false,approval_id=NULL,updated_at=clock_timestamp() WHERE singleton;
UPDATE privacy_worker_kill_switch SET engaged=true,version=version+1,activation_approval_id=NULL,changed_at=clock_timestamp() WHERE singleton;
INSERT INTO privacy_worker_kill_switch_events(version,engaged,occurred_at)
 SELECT version,true,changed_at FROM privacy_worker_kill_switch WHERE singleton;

CREATE OR REPLACE FUNCTION guardian_authority_reconcile_cutoffs() RETURNS bigint
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE relationship guardian_authority_relationships%ROWTYPE;now_at timestamptz:=clock_timestamp();reconciled bigint:=0;
BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended('mycfc/guardian-authority',0));
 FOR relationship IN
  SELECT authority.* FROM guardian_authority_relationships authority
  JOIN users guardian ON guardian.id=authority.guardian_user_id
  JOIN users subject ON subject.id=authority.subject_user_id
  LEFT JOIN guardian_authority_policies policy ON policy.version=authority.policy_version
  WHERE authority.state IN('VERIFIED','SUSPENDED')
   AND (policy.id IS NULL OR NOT policy.enabled OR policy.adopted_at>now_at
    OR authority.review_due_at<=now_at OR authority.verified_until<=now_at
    OR NOT guardian.is_active OR guardian.erased_at IS NOT NULL OR guardian.is_dependent
    OR guardian.date_of_birth>((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-INTERVAL '18 years')::date
    OR NOT subject.is_active OR subject.erased_at IS NOT NULL OR NOT subject.is_dependent
    OR subject.date_of_birth<=((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-INTERVAL '18 years')::date)
  FOR UPDATE OF authority
 LOOP
  UPDATE guardian_authority_relationships SET state='EXPIRED',version=relationship.version+1,
   conflict=false,conflict_actor_ref=NULL,updated_at=now_at WHERE id=relationship.id;
  DELETE FROM sessions WHERE subject_indexed AND user_id=relationship.subject_user_id;
  UPDATE users SET minor_login_id=NULL,password_hash=NULL,credential_version=credential_version+1,updated_at=now_at
   WHERE id=relationship.subject_user_id;
  INSERT INTO guardian_authority_events(relationship_id,relationship_version,actor_ref,actor_role,action,from_state,to_state,
   policy_version,occurred_at,verified_until,review_due_at)
  VALUES(relationship.id,relationship.version+1,NULL,'SYSTEM','EXPIRED',relationship.state,'EXPIRED',
   relationship.policy_version,now_at,relationship.verified_until,relationship.review_due_at);
  reconciled:=reconciled+1;
 END LOOP;
 RETURN reconciled;
END;
$$;

CREATE OR REPLACE FUNCTION guardian_authority_transition(
 p_actor_id uuid,p_relationship_ref uuid,p_expected_version bigint,p_target_state text,
 p_evidence_type text,p_evidence_reference text,p_evidence_sha256 bytea,p_reason_code text)
RETURNS SETOF guardian_authority_relationships
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE current_row guardian_authority_relationships%ROWTYPE;policy_row guardian_authority_policies%ROWTYPE;
 now_at timestamptz;new_version bigint;new_verified_until timestamptz;new_review_due_at timestamptz;
 new_conflict boolean;new_conflict_actor uuid;old_state text;
BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended('mycfc/guardian-verifier-grants',0));
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
 SELECT * INTO policy_row FROM guardian_authority_policies WHERE enabled AND adopted_at<=now_at FOR SHARE;
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
 IF p_reason_code IS NULL OR NOT (p_reason_code=ANY(policy_row.reason_codes)) THEN
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
 IF p_target_state='VERIFIED' AND NOT guardian_authority_current(current_row.guardian_user_id,current_row.subject_user_id) THEN
  DELETE FROM sessions WHERE subject_indexed AND user_id=current_row.subject_user_id;
  UPDATE users SET minor_login_id=NULL,password_hash=NULL,credential_version=credential_version+1,updated_at=now_at
   WHERE id=current_row.subject_user_id;
 END IF;
 UPDATE guardian_authority_relationships SET state=p_target_state,version=new_version,policy_version=policy_row.version,
  verified_at=CASE WHEN p_target_state='VERIFIED' THEN now_at ELSE verified_at END,
  verified_by=CASE WHEN p_target_state='VERIFIED' THEN p_actor_id ELSE verified_by END,
  verified_until=new_verified_until,review_due_at=new_review_due_at,
  conflict=new_conflict,conflict_actor_ref=new_conflict_actor,updated_at=now_at
 WHERE id=current_row.id AND version=p_expected_version RETURNING * INTO current_row;
 IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='guardian_authority_stale'; END IF;
 IF p_target_state IN('SUSPENDED','EXPIRED','REJECTED') THEN
  DELETE FROM sessions WHERE subject_indexed AND user_id=current_row.subject_user_id;
  UPDATE users SET minor_login_id=NULL,password_hash=NULL,credential_version=credential_version+1,updated_at=now_at
   WHERE id=current_row.subject_user_id;
 END IF;
 INSERT INTO guardian_authority_events(relationship_id,relationship_version,actor_ref,actor_role,action,from_state,to_state,
  policy_version,evidence_type,evidence_reference,evidence_sha256,reason_code,occurred_at,verified_until,review_due_at)
 VALUES(current_row.id,new_version,p_actor_id,'VERIFIER',p_target_state,old_state,p_target_state,
  policy_row.version,p_evidence_type,p_evidence_reference,p_evidence_sha256,p_reason_code,now_at,new_verified_until,new_review_due_at);
 RETURN NEXT current_row;
END;
$$;

REVOKE ALL ON FUNCTION guardian_authority_codes_valid(text[]),guardian_authority_reconcile_cutoffs() FROM PUBLIC;
