-- Administrator guardian-authority review and metadata-only attestation.
-- Invalid credential and invitation probes are limited to 10 attempts per
-- pseudonymous account and network bucket in 15 minutes.

ALTER TABLE privacy_activation_authenticated_artifacts DROP CONSTRAINT privacy_activation_authenticated_artifacts_v9_check;
ALTER TABLE privacy_activation_authenticated_artifacts ADD CONSTRAINT privacy_activation_authenticated_artifacts_v10_check CHECK(
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
    '202609110005_guardian_authority_cutoff_reconciliation','202609120001_guardian_application_eligibility','202609120002_guardian_authority_admin_review'))) IS TRUE) NOT VALID;
ALTER TABLE privacy_activation_authenticated_artifacts VALIDATE CONSTRAINT privacy_activation_authenticated_artifacts_v10_check;

DO $$DECLARE definition text;old_clause text;new_clause text;
BEGIN
 SELECT pg_get_functiondef('public.privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)'::regprocedure) INTO definition;
 old_clause:='p_artifact->>''baseline_includes_through''<>''202609120001_guardian_application_eligibility''';
 new_clause:='p_artifact->>''baseline_includes_through''<>''202609120002_guardian_authority_admin_review''';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_authority_review_activation_evidence_predecessor_mismatch'; END IF;
 EXECUTE replace(definition,old_clause,new_clause);

 SELECT pg_get_functiondef('public.privacy_activation_authenticated_set_digest(text,uuid[])'::regprocedure) INTO definition;
 old_clause:='schema_row.baseline_includes_through=''202609120001_guardian_application_eligibility''';
 new_clause:='schema_row.baseline_includes_through=''202609120002_guardian_authority_admin_review''';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_authority_review_activation_digest_predecessor_mismatch'; END IF;
 EXECUTE replace(definition,old_clause,new_clause);
END$$;

ALTER TABLE guardian_authority_events
 DROP CONSTRAINT guardian_authority_events_actor_role_check,
 DROP CONSTRAINT guardian_authority_events_check;
ALTER TABLE guardian_authority_events
 ADD CONSTRAINT guardian_authority_events_actor_role_v3_check
  CHECK(actor_role IN('GUARDIAN','VERIFIER','ADMIN','SYSTEM')) NOT VALID,
 ADD CONSTRAINT guardian_authority_events_evidence_v3_check
  CHECK((actor_role='ADMIN' AND evidence_type IS NOT NULL AND evidence_reference IS NULL AND evidence_sha256 IS NULL) OR
   (actor_role<>'ADMIN' AND ((evidence_type IS NULL AND evidence_reference IS NULL AND evidence_sha256 IS NULL) OR
   (evidence_type IS NOT NULL AND evidence_reference IS NOT NULL AND octet_length(evidence_sha256)=32)))) NOT VALID,
 ADD CONSTRAINT guardian_authority_events_admin_decision_v3_check
  CHECK(actor_role<>'ADMIN' OR (
   evidence_type IN('CLUB_REGISTRATION_RECORD','IN_PERSON_ID_AND_CIVIL_RECORD','COURT_OR_LEGAL_AUTHORITY') AND
   ((action='VERIFIED' AND reason_code='RELATIONSHIP_CONFIRMED') OR
    (action='REJECTED' AND reason_code IN('EVIDENCE_INSUFFICIENT','AUTHORITY_NOT_ESTABLISHED')) OR
    (action='SUSPENDED' AND reason_code IN('CONFLICT','AUTHORITY_CHANGED','UNCERTAINTY')) OR
    (action='EXPIRED' AND reason_code='AUTHORITY_ENDED')))) NOT VALID,
 ADD CONSTRAINT guardian_authority_events_system_reason_v3_check
  CHECK(actor_role<>'SYSTEM' OR reason_code IS NULL OR reason_code IN('ELIGIBILITY_ENDED','REVIEW_EXPIRED','MAJORITY_REACHED')) NOT VALID;
ALTER TABLE guardian_authority_events VALIDATE CONSTRAINT guardian_authority_events_actor_role_v3_check;
ALTER TABLE guardian_authority_events VALIDATE CONSTRAINT guardian_authority_events_evidence_v3_check;
ALTER TABLE guardian_authority_events VALIDATE CONSTRAINT guardian_authority_events_admin_decision_v3_check;
ALTER TABLE guardian_authority_events VALIDATE CONSTRAINT guardian_authority_events_system_reason_v3_check;

CREATE TABLE guardian_authority_review_access_events (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 relationship_id uuid NULL REFERENCES guardian_authority_relationships(id) ON DELETE RESTRICT,
 actor_ref uuid NOT NULL,
 view_kind varchar(12) NOT NULL CHECK(view_kind IN('QUEUE','DETAIL')),
 occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK((view_kind='QUEUE' AND relationship_id IS NULL) OR (view_kind='DETAIL' AND relationship_id IS NOT NULL))
);
CREATE INDEX guardian_authority_review_access_events_actor_idx
 ON guardian_authority_review_access_events(actor_ref,occurred_at);
CREATE TRIGGER guardian_authority_review_access_events_immutable BEFORE UPDATE OR DELETE ON guardian_authority_review_access_events
 FOR EACH ROW EXECUTE FUNCTION prevent_guardian_authority_event_mutation();

CREATE FUNCTION guardian_authority_personally_involved(p_actor_id uuid,p_relationship_ref uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT COALESCE(EXISTS(
  SELECT 1 FROM guardian_authority_relationships target
  WHERE target.public_ref=p_relationship_ref
   AND (p_actor_id IN(target.guardian_user_id,target.subject_user_id)
    OR EXISTS(SELECT 1 FROM guardian_authority_relationships family
      WHERE family.state<>'REJECTED'
       AND ((family.guardian_user_id=p_actor_id AND family.subject_user_id IN(target.guardian_user_id,target.subject_user_id))
        OR (family.subject_user_id=p_actor_id AND family.guardian_user_id IN(target.guardian_user_id,target.subject_user_id)))))
 ),false);
$$;
CREATE FUNCTION guardian_authority_record_review_view(p_actor_id uuid,p_relationship_ref uuid) RETURNS boolean
LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE relationship_id uuid;
BEGIN
 IF NOT guardian_authority_is_administrator(p_actor_id) THEN
  RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='guardian_authority_administrator_required'; END IF;
 IF p_relationship_ref IS NULL THEN
  INSERT INTO guardian_authority_review_access_events(actor_ref,view_kind) VALUES(p_actor_id,'QUEUE');
  RETURN true;
 END IF;
 SELECT id INTO relationship_id FROM guardian_authority_relationships WHERE public_ref=p_relationship_ref;
 IF NOT FOUND THEN RETURN false; END IF;
 INSERT INTO guardian_authority_review_access_events(relationship_id,actor_ref,view_kind)
 VALUES(relationship_id,p_actor_id,'DETAIL');
 RETURN true;
END;
$$;
CREATE OR REPLACE FUNCTION guardian_authority_reconcile_cutoffs() RETURNS bigint
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE relationship guardian_authority_relationships%ROWTYPE;now_at timestamptz:=clock_timestamp();reconciled bigint:=0;end_reason text;
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
  end_reason:=CASE
   WHEN EXISTS(SELECT 1 FROM users subject WHERE subject.id=relationship.subject_user_id
    AND subject.date_of_birth<=((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-INTERVAL '18 years')::date) THEN 'MAJORITY_REACHED'
   WHEN relationship.review_due_at<=now_at OR relationship.verified_until<=now_at THEN 'REVIEW_EXPIRED'
   ELSE 'ELIGIBILITY_ENDED' END;
  UPDATE guardian_authority_relationships SET state='EXPIRED',version=relationship.version+1,
   conflict=false,conflict_actor_ref=NULL,updated_at=now_at WHERE id=relationship.id;
  DELETE FROM sessions WHERE subject_indexed AND user_id=relationship.subject_user_id;
  UPDATE users SET minor_login_id=NULL,password_hash=NULL,credential_version=credential_version+1,updated_at=now_at
   WHERE id=relationship.subject_user_id;
  INSERT INTO guardian_authority_events(relationship_id,relationship_version,actor_ref,actor_role,action,from_state,to_state,
   policy_version,reason_code,occurred_at,verified_until,review_due_at)
  VALUES(relationship.id,relationship.version+1,NULL,'SYSTEM','EXPIRED',relationship.state,'EXPIRED',
   relationship.policy_version,end_reason,now_at,relationship.verified_until,relationship.review_due_at);
  reconciled:=reconciled+1;
 END LOOP;
 RETURN reconciled;
END;
$$;
CREATE FUNCTION guardian_authority_admin_transition(
 p_actor_id uuid,p_relationship_ref uuid,p_expected_version bigint,p_action text,
 p_evidence_category text,p_reason_code text)
RETURNS SETOF guardian_authority_relationships
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE current_row guardian_authority_relationships%ROWTYPE;policy_row guardian_authority_policies%ROWTYPE;
 now_at timestamptz:=clock_timestamp();new_version bigint;target_state text;old_state text;
 new_verified_until timestamptz;new_review_due_at timestamptz;new_conflict boolean;new_conflict_actor uuid;
BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended('mycfc/guardian-authority',0));
 IF NOT guardian_authority_is_administrator(p_actor_id) THEN
  RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='guardian_authority_administrator_required'; END IF;
 SELECT * INTO current_row FROM guardian_authority_relationships WHERE public_ref=p_relationship_ref FOR UPDATE;
 IF NOT FOUND OR current_row.version<>p_expected_version THEN
  RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='guardian_authority_stale'; END IF;
 IF guardian_authority_personally_involved(p_actor_id,p_relationship_ref)
  OR current_row.conflict_actor_ref=p_actor_id THEN
  RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='guardian_authority_separation_required'; END IF;
 SELECT * INTO policy_row FROM guardian_authority_policies WHERE enabled AND adopted_at<=now_at FOR SHARE;
 IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_authority_policy_unavailable'; END IF;
 IF p_evidence_category NOT IN('CLUB_REGISTRATION_RECORD','IN_PERSON_ID_AND_CIVIL_RECORD','COURT_OR_LEGAL_AUTHORITY') THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='guardian_authority_evidence_rejected'; END IF;
 target_state:=CASE p_action WHEN 'APPROVE' THEN 'VERIFIED' WHEN 'RENEW' THEN 'VERIFIED'
  WHEN 'REJECT' THEN 'REJECTED' WHEN 'SUSPEND' THEN 'SUSPENDED' WHEN 'END' THEN 'EXPIRED' ELSE NULL END;
 IF target_state IS NULL
  OR (p_action='APPROVE' AND current_row.state NOT IN('PENDING','SUSPENDED'))
  OR (p_action='RENEW' AND current_row.state NOT IN('VERIFIED','EXPIRED'))
  OR (p_action='REJECT' AND current_row.state NOT IN('PENDING','SUSPENDED'))
  OR (p_action='SUSPEND' AND current_row.state NOT IN('PENDING','VERIFIED'))
  OR (p_action='END' AND current_row.state NOT IN('VERIFIED','SUSPENDED')) THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='guardian_authority_transition_rejected'; END IF;
 IF (p_action IN('APPROVE','RENEW') AND p_reason_code<>'RELATIONSHIP_CONFIRMED')
  OR (p_action='REJECT' AND p_reason_code NOT IN('EVIDENCE_INSUFFICIENT','AUTHORITY_NOT_ESTABLISHED'))
  OR (p_action='SUSPEND' AND p_reason_code NOT IN('CONFLICT','AUTHORITY_CHANGED','UNCERTAINTY'))
  OR (p_action='END' AND p_reason_code<>'AUTHORITY_ENDED') THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='guardian_authority_reason_rejected'; END IF;
 IF target_state='VERIFIED' AND NOT EXISTS(
  SELECT 1 FROM users guardian JOIN users subject ON subject.id=current_row.subject_user_id
  WHERE guardian.id=current_row.guardian_user_id AND guardian.is_active AND guardian.erased_at IS NULL AND NOT guardian.is_dependent
   AND guardian.date_of_birth<=((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-INTERVAL '18 years')::date
   AND subject.is_active AND subject.erased_at IS NULL AND subject.is_dependent
   AND subject.date_of_birth>((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-INTERVAL '18 years')::date
 ) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_authority_relationship_ineligible'; END IF;
 new_version:=current_row.version+1;old_state:=current_row.state;
 IF target_state='VERIFIED' THEN
  new_review_due_at:=now_at+make_interval(days=>policy_row.review_days);
  new_verified_until:=now_at+make_interval(days=>policy_row.validity_days);
  new_conflict:=false;new_conflict_actor:=NULL;
 ELSE
  new_review_due_at:=current_row.review_due_at;new_verified_until:=current_row.verified_until;
  new_conflict:=(target_state='SUSPENDED' AND p_reason_code='CONFLICT');
  new_conflict_actor:=CASE WHEN new_conflict THEN p_actor_id ELSE NULL END;
 END IF;
 UPDATE guardian_authority_relationships SET state=target_state,version=new_version,policy_version=policy_row.version,
  verified_at=CASE WHEN target_state='VERIFIED' THEN now_at ELSE verified_at END,
  verified_by=CASE WHEN target_state='VERIFIED' THEN p_actor_id ELSE verified_by END,
  verified_until=new_verified_until,review_due_at=new_review_due_at,
  conflict=new_conflict,conflict_actor_ref=new_conflict_actor,updated_at=now_at
 WHERE id=current_row.id AND version=p_expected_version RETURNING * INTO current_row;
 IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='guardian_authority_stale'; END IF;
 IF target_state IN('SUSPENDED','EXPIRED','REJECTED') THEN
  DELETE FROM sessions WHERE subject_indexed AND user_id=current_row.subject_user_id;
  UPDATE users SET minor_login_id=NULL,password_hash=NULL,credential_version=credential_version+1,updated_at=now_at
   WHERE id=current_row.subject_user_id;
 END IF;
 INSERT INTO guardian_authority_events(relationship_id,relationship_version,actor_ref,actor_role,action,from_state,to_state,
  policy_version,evidence_type,reason_code,occurred_at,verified_until,review_due_at)
 VALUES(current_row.id,new_version,p_actor_id,'ADMIN',target_state,old_state,target_state,
  policy_row.version,p_evidence_category,p_reason_code,now_at,new_verified_until,new_review_due_at);
 RETURN NEXT current_row;
END;
$$;

REVOKE ALL ON TABLE guardian_authority_review_access_events FROM PUBLIC;
DO $$BEGIN
 IF to_regrole('mycfc_app') IS NOT NULL THEN
  EXECUTE 'REVOKE ALL PRIVILEGES ON TABLE guardian_authority_review_access_events FROM mycfc_app';
 END IF;
END$$;
REVOKE ALL ON FUNCTION guardian_authority_personally_involved(uuid,uuid),guardian_authority_record_review_view(uuid,uuid),
 guardian_authority_admin_transition(uuid,uuid,bigint,text,text,text) FROM PUBLIC;

-- This schema revision invalidates every prior privacy activation artifact.
UPDATE privacy_request_activation SET enabled=false,fulfilment_ready=false,approval_id=NULL,updated_at=clock_timestamp() WHERE singleton;
UPDATE privacy_worker_kill_switch SET engaged=true,version=version+1,activation_approval_id=NULL,changed_at=clock_timestamp() WHERE singleton;
INSERT INTO privacy_worker_kill_switch_events(version,engaged,occurred_at)
 SELECT version,true,changed_at FROM privacy_worker_kill_switch WHERE singleton;
