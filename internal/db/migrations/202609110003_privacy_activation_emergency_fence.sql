-- Serialize activation and emergency disable, and fence every activation
-- artifact created before the latest kill-switch engagement.
ALTER TABLE privacy_activation_authenticated_artifacts DROP CONSTRAINT privacy_activation_authenticated_artifacts_v5_check;
ALTER TABLE privacy_activation_authenticated_artifacts ADD CONSTRAINT privacy_activation_authenticated_artifacts_v6_check CHECK(
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
    '202609110003_privacy_activation_emergency_fence'))) IS TRUE) NOT VALID;
ALTER TABLE privacy_activation_authenticated_artifacts VALIDATE CONSTRAINT privacy_activation_authenticated_artifacts_v6_check;

DO $$DECLARE definition text;old_clause text;new_clause text;
BEGIN
 SELECT pg_get_functiondef('public.privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)'::regprocedure) INTO definition;
 old_clause:='p_artifact->>''baseline_includes_through''<>''202609110002_privacy_empty_provider_registry_activation''';
 new_clause:='p_artifact->>''baseline_includes_through''<>''202609110003_privacy_activation_emergency_fence''';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_activation_evidence_predecessor_mismatch'; END IF;
 EXECUTE replace(definition,old_clause,new_clause);

 SELECT pg_get_functiondef('public.privacy_activation_authenticated_set_digest(text,uuid[])'::regprocedure) INTO definition;
 old_clause:='schema_row.baseline_includes_through=''202609110002_privacy_empty_provider_registry_activation''';
 new_clause:='schema_row.baseline_includes_through=''202609110003_privacy_activation_emergency_fence''';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_activation_digest_predecessor_mismatch'; END IF;
 EXECUTE replace(definition,old_clause,new_clause);
END$$;

DO $$DECLARE definition text;old_clause text;new_clause text;
BEGIN
 SELECT pg_get_functiondef('public.privacy_activation_broker_activate(uuid,text,uuid[],bytea,bytea,uuid,uuid,text,text,bytea,bytea,bytea,bytea,jsonb,jsonb,timestamptz,timestamptz,timestamptz,timestamptz)'::regprocedure) INTO definition;

 old_clause:='IF session_user<>''mycfc_privacy_activation_broker'' THEN RAISE EXCEPTION USING ERRCODE=''42501'',MESSAGE=''privacy_activation_broker_required''; END IF;
 SELECT * INTO material FROM privacy_activation_broker_material(p_policy_version);';
 new_clause:='IF session_user<>''mycfc_privacy_activation_broker'' THEN RAISE EXCEPTION USING ERRCODE=''42501'',MESSAGE=''privacy_activation_broker_required''; END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended(''mycfc/privacy-activation'',0));
 now_at:=clock_timestamp();
 SELECT * INTO material FROM privacy_activation_broker_material(p_policy_version);';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_activation_lock_predecessor_mismatch'; END IF;
 definition:=replace(definition,old_clause,new_clause);

 old_clause:='OR p_executor_expires_at<=now_at OR p_administrator_expires_at<=now_at
  OR p_executor_expires_at>p_executor_issued_at+interval ''15 minutes'' OR p_administrator_expires_at>p_administrator_issued_at+interval ''15 minutes''';
 new_clause:='OR p_executor_expires_at<=now_at OR p_administrator_expires_at<=now_at
  OR p_executor_issued_at<=(SELECT changed_at FROM privacy_worker_kill_switch WHERE singleton)
  OR p_administrator_issued_at<=(SELECT changed_at FROM privacy_worker_kill_switch WHERE singleton)
  OR EXISTS(SELECT 1 FROM privacy_activation_evidence evidence
    WHERE evidence.id=ANY(p_evidence_ids) AND evidence.recorded_at<=(SELECT changed_at FROM privacy_worker_kill_switch WHERE singleton))
  OR p_executor_expires_at>p_executor_issued_at+interval ''15 minutes'' OR p_administrator_expires_at>p_administrator_issued_at+interval ''15 minutes''';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_activation_generation_predecessor_mismatch'; END IF;
 EXECUTE replace(definition,old_clause,new_clause);
END$$;

REVOKE ALL ON FUNCTION privacy_activation_disable(uuid) FROM PUBLIC;
DROP FUNCTION privacy_activation_disable(uuid);

CREATE SCHEMA IF NOT EXISTS privacy_disable;
REVOKE ALL ON SCHEMA privacy_disable FROM PUBLIC;
REVOKE USAGE ON SCHEMA public FROM PUBLIC;

CREATE FUNCTION privacy_disable.privacy_activation_disable(p_actor uuid,p_expected_database text)
RETURNS TABLE(switch_version bigint,database_name text,engaged boolean,fulfilment_ready boolean)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE now_at timestamptz;v_switch_version bigint;v_policy_version text;
BEGIN
 IF session_user<>'mycfc_privacy_activation_disable' THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_activation_disable_role_required'; END IF;
 IF p_actor IS NULL OR p_expected_database IS NULL OR p_expected_database<>btrim(p_expected_database)
  OR p_expected_database!~'^[A-Za-z0-9_][A-Za-z0-9_-]{0,62}$' OR current_database()<>p_expected_database THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy_activation_disable_database_rejected'; END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended('mycfc/privacy-activation',0));
 now_at:=clock_timestamp();
 UPDATE privacy_worker_kill_switch switch SET engaged=true,version=switch.version+1,activation_approval_id=NULL,changed_at=now_at
 WHERE switch.singleton RETURNING switch.version INTO v_switch_version;
 INSERT INTO privacy_worker_kill_switch_events(version,engaged,occurred_at) VALUES(v_switch_version,true,now_at);
 UPDATE privacy_request_activation activation SET enabled=false,fulfilment_ready=false,approval_id=NULL,updated_by=p_actor,updated_at=now_at
 WHERE activation.singleton AND (activation.enabled OR activation.fulfilment_ready OR activation.approval_id IS NOT NULL)
 RETURNING activation.policy_version INTO v_policy_version;
 IF v_policy_version IS NOT NULL THEN
  INSERT INTO privacy_request_activation_events(policy_version,actor_ref,enabled,fulfilment_ready,occurred_at)
  VALUES(v_policy_version,p_actor,false,false,now_at);
 END IF;
 RETURN QUERY SELECT v_switch_version,current_database()::text,switch.engaged,privacy_worker_activation_ready()
  FROM privacy_worker_kill_switch switch WHERE switch.singleton;
END;$$;

REVOKE ALL ON FUNCTION privacy_disable.privacy_activation_disable(uuid,text) FROM PUBLIC;
DO $$BEGIN
 IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='mycfc_privacy_activation_disable') THEN
  REVOKE EXECUTE ON ALL FUNCTIONS IN SCHEMA public FROM mycfc_privacy_activation_disable;
  REVOKE ALL ON SCHEMA public FROM mycfc_privacy_activation_disable;
  GRANT USAGE ON SCHEMA privacy_disable TO mycfc_privacy_activation_disable;
  GRANT EXECUTE ON FUNCTION privacy_disable.privacy_activation_disable(uuid,text) TO mycfc_privacy_activation_disable;
 END IF;
END$$;

ALTER DEFAULT PRIVILEGES IN SCHEMA public REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC;

-- This schema change itself invalidates every earlier activation artifact.
UPDATE privacy_request_activation SET enabled=false,fulfilment_ready=false,approval_id=NULL,updated_at=clock_timestamp() WHERE singleton;
UPDATE privacy_worker_kill_switch SET engaged=true,version=version+1,activation_approval_id=NULL,changed_at=clock_timestamp() WHERE singleton;
INSERT INTO privacy_worker_kill_switch_events(version,engaged,occurred_at)
 SELECT version,true,changed_at FROM privacy_worker_kill_switch WHERE singleton;
