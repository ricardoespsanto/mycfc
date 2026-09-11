-- #248 distinguishes a complete zero-registration provider inventory from an
-- incomplete empty registry. Historical v1 provider evidence remains immutable
-- audit history, but only signed v2 evidence for the exact closed inventory can
-- participate in a new activation set.

ALTER TABLE privacy_activation_authenticated_artifacts
 ADD COLUMN provider_inventory_contract text NULL;

ALTER TABLE privacy_activation_authenticated_artifacts DROP CONSTRAINT privacy_activation_authenticated_artifacts_v4_check;
ALTER TABLE privacy_activation_authenticated_artifacts ADD CONSTRAINT privacy_activation_authenticated_artifacts_v5_check CHECK(
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
    '202609110001_privacy_upload_finalize_execution_fence','202609110002_privacy_empty_provider_registry_activation'))) IS TRUE) NOT VALID;
ALTER TABLE privacy_activation_authenticated_artifacts VALIDATE CONSTRAINT privacy_activation_authenticated_artifacts_v5_check;

DO $$DECLARE definition text;old_clause text;new_clause text;
BEGIN
 SELECT pg_get_functiondef('public.privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)'::regprocedure) INTO definition;

 old_clause:='WHEN ''PROVIDER'' THEN ARRAY[''policy_version'',''executor_version'',''plan_schema_version'',''image_digest'',''evidence_ref'',''evidence_sha256'',''signing_key_id'',''provider_registry_state'',''provider_registration_count'',''provider_registry_sha256'']';
 new_clause:='WHEN ''PROVIDER'' THEN ARRAY[''policy_version'',''executor_version'',''plan_schema_version'',''image_digest'',''evidence_ref'',''evidence_sha256'',''signing_key_id'',''provider_registry_state'',''provider_registration_count'',''provider_registry_sha256'',''provider_inventory_contract'']';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_provider_keys_predecessor_mismatch'; END IF;
 definition:=replace(definition,old_clause,new_clause);

 old_clause:='OR p_artifact->>''plan_schema_version''<>''privacy-erasure-plan/v2'' OR p_artifact->>''image_digest''!~''^sha256:[0-9a-f]{64}$'' THEN';
 new_clause:='OR p_artifact->>''plan_schema_version''<>''privacy-erasure-plan/v2'' OR p_artifact->>''image_digest''!~''^sha256:[0-9a-f]{64}$''
  OR (p_kind=''PROVIDER'' AND (p_artifact->>''provider_registry_state''<>''READY''
   OR (p_artifact->>''provider_registration_count'')::bigint<>0
   OR p_artifact->>''provider_inventory_contract''<>''mycfc/privacy-provider-registry-source/v2'')) THEN';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_provider_validation_predecessor_mismatch'; END IF;
 definition:=replace(definition,old_clause,new_clause);

 old_clause:='(p_kind=''PROVIDER'' AND p_reference_code<>''mycfc/privacy-provider-registry/v1'')';
 new_clause:='(p_kind=''PROVIDER'' AND p_reference_code<>''mycfc/privacy-provider-registry/v2'')';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_provider_contract_predecessor_mismatch'; END IF;
 definition:=replace(definition,old_clause,new_clause);

 old_clause:='provider_registry_state,provider_registration_count,provider_registry_sha256,restore_input_source';
 new_clause:='provider_registry_state,provider_registration_count,provider_registry_sha256,provider_inventory_contract,restore_input_source';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_provider_columns_predecessor_mismatch'; END IF;
 definition:=replace(definition,old_clause,new_clause);

 old_clause:='decode(p_artifact->>''provider_registry_sha256'',''base64''),p_artifact->>''restore_input_source''';
 new_clause:='decode(p_artifact->>''provider_registry_sha256'',''base64''),p_artifact->>''provider_inventory_contract'',p_artifact->>''restore_input_source''';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_provider_values_predecessor_mismatch'; END IF;
 definition:=replace(definition,old_clause,new_clause);

 old_clause:='p_artifact->>''baseline_includes_through''<>''202609110001_privacy_upload_finalize_execution_fence''';
 new_clause:='p_artifact->>''baseline_includes_through''<>''202609110002_privacy_empty_provider_registry_activation''';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_activation_evidence_predecessor_mismatch'; END IF;
 EXECUTE replace(definition,old_clause,new_clause);
END$$;

CREATE OR REPLACE FUNCTION privacy_activation_authenticated_set_digest(p_policy_version text,p_evidence_ids uuid[])
RETURNS bytea LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE policy privacy_request_policies%ROWTYPE;now_at timestamptz:=clock_timestamp();result bytea;
BEGIN
 SELECT * INTO policy FROM privacy_request_policies WHERE version=p_policy_version;
 IF policy.version IS NULL OR policy.adopted_at IS NULL OR policy.working_retention_days<>90 OR cardinality(p_evidence_ids)<>4
  OR policy.executor_version<>'privacy-erasure-executor/v2' OR policy.plan_schema_version<>'privacy-erasure-plan/v2'
  OR (SELECT count(*) FROM privacy_activation_evidence evidence JOIN privacy_activation_authenticated_artifacts artifact ON artifact.evidence_id=evidence.id
      WHERE evidence.id=ANY(p_evidence_ids) AND evidence.expires_at>now_at AND artifact.policy_version=policy.version
       AND artifact.executor_version=policy.executor_version AND artifact.plan_schema_version=policy.plan_schema_version)<>4
  OR (SELECT count(DISTINCT evidence.kind) FROM privacy_activation_evidence evidence JOIN privacy_activation_authenticated_artifacts artifact ON artifact.evidence_id=evidence.id
      WHERE evidence.id=ANY(p_evidence_ids) AND evidence.expires_at>now_at)<>4
  OR (SELECT count(DISTINCT artifact.image_digest) FROM privacy_activation_authenticated_artifacts artifact WHERE artifact.evidence_id=ANY(p_evidence_ids))<>1
  OR (SELECT restore.schema_migration_digest IS DISTINCT FROM schema_row.schema_migration_digest
      FROM privacy_activation_authenticated_artifacts restore CROSS JOIN privacy_activation_authenticated_artifacts schema_row
      WHERE restore.evidence_id=ANY(p_evidence_ids) AND restore.kind='RESTORE' AND schema_row.evidence_id=ANY(p_evidence_ids) AND schema_row.kind='SCHEMA')
  OR NOT EXISTS(SELECT 1 FROM privacy_activation_authenticated_artifacts restore
   WHERE restore.evidence_id=ANY(p_evidence_ids) AND restore.kind='RESTORE'
    AND restore.restore_closure_contract='restore-tombstone-closure/v4'
    AND restore.restore_membership_postcondition_contract='mycfc/membership-history-postcondition/v1'
    AND octet_length(restore.restore_membership_postcondition_sha256)=32
    AND restore.restore_membership_postcondition_verified_count=restore.restore_replayed_count)
  OR NOT EXISTS(SELECT 1 FROM privacy_activation_authenticated_artifacts provider
   JOIN privacy_activation_evidence evidence ON evidence.id=provider.evidence_id
   WHERE provider.evidence_id=ANY(p_evidence_ids) AND provider.kind='PROVIDER'
    AND evidence.reference_code='mycfc/privacy-provider-registry/v2'
    AND provider.provider_registry_state='READY' AND provider.provider_registration_count=0
    AND provider.provider_inventory_contract='mycfc/privacy-provider-registry-source/v2'
    AND octet_length(provider.provider_registry_sha256)=32)
  OR NOT EXISTS(SELECT 1 FROM privacy_activation_authenticated_artifacts schema_row
   WHERE schema_row.evidence_id=ANY(p_evidence_ids) AND schema_row.kind='SCHEMA'
    AND schema_row.baseline_includes_through='202609110002_privacy_empty_provider_registry_activation')
 THEN RETURN NULL; END IF;
 SELECT digest(convert_to(string_agg(evidence.kind||':'||encode(evidence.evidence_sha256,'hex')||':'||
   encode(digest(convert_to((to_jsonb(artifact)-'authenticated_at')::text,'UTF8'),'sha256'),'hex')||':'||
   to_char(evidence.observed_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"')||':'||
   to_char(evidence.expires_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),'|' ORDER BY evidence.kind),'UTF8'),'sha256')
 INTO result FROM privacy_activation_evidence evidence JOIN privacy_activation_authenticated_artifacts artifact ON artifact.evidence_id=evidence.id
 WHERE evidence.id=ANY(p_evidence_ids);
 RETURN result;
END;$$;

REVOKE ALL ON FUNCTION privacy_activation_authenticated_set_digest(text,uuid[]) FROM PUBLIC;

-- The schema and provider contract changed. Old release evidence is preserved
-- for audit, but all activation must be re-established against this boundary.
UPDATE privacy_request_activation SET enabled=false,fulfilment_ready=false,approval_id=NULL,updated_at=clock_timestamp() WHERE singleton;
UPDATE privacy_worker_kill_switch SET engaged=true,version=version+1,activation_approval_id=NULL,changed_at=clock_timestamp() WHERE singleton;
INSERT INTO privacy_worker_kill_switch_events(version,engaged,occurred_at)
 SELECT version,true,changed_at FROM privacy_worker_kill_switch WHERE singleton;
