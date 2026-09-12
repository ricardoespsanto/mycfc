-- Give the release-only identity one non-identifying observation: whether the
-- guardian intake gate is enabled for the exact running database/image/schema.
-- Invalid or stale identity inputs return NULL so callers must fail closed.
DO $$DECLARE definition text;rewritten text;
BEGIN
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint
 WHERE conrelid='privacy_activation_authenticated_artifacts'::regclass
  AND conname='privacy_activation_authenticated_artifacts_v14_check';
 IF definition IS NULL OR length(definition)-length(replace(definition,'202609120006_guardian_schema_ready_owner',''))<>length('202609120006_guardian_schema_ready_owner') THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_release_status_privacy_constraint_predecessor_mismatch';
 END IF;
 rewritten:=replace(definition,
  '''202609120006_guardian_schema_ready_owner''',
  '''202609120006_guardian_schema_ready_owner'', ''202609120007_guardian_release_status''');
 ALTER TABLE privacy_activation_authenticated_artifacts DROP CONSTRAINT privacy_activation_authenticated_artifacts_v14_check;
 EXECUTE 'ALTER TABLE privacy_activation_authenticated_artifacts ADD CONSTRAINT privacy_activation_authenticated_artifacts_v15_check '||rewritten||' NOT VALID';
 ALTER TABLE privacy_activation_authenticated_artifacts VALIDATE CONSTRAINT privacy_activation_authenticated_artifacts_v15_check;
END$$;

CREATE FUNCTION guardian_ops.release_intake_enabled(p_expected_database text,p_current_image_digest text,p_embedded_schema_digest text)
RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT CASE
  WHEN current_database() IS DISTINCT FROM p_expected_database OR NOT guardian_ops.schema_ready(p_embedded_schema_digest)
   OR runtime.database_name IS DISTINCT FROM current_database() OR runtime.image_digest IS DISTINCT FROM p_current_image_digest
   OR runtime.schema_migration_digest IS DISTINCT FROM p_embedded_schema_digest OR gate.gate_version IS DISTINCT FROM 'guardian-intake-v2'
  THEN NULL
  ELSE gate.enabled
 END
 FROM guardian_application_intake_release gate
 JOIN guardian_ops.runtime_release_binding runtime ON runtime.singleton
 WHERE gate.singleton;
$$;
REVOKE ALL ON FUNCTION guardian_ops.release_intake_enabled(text,text,text) FROM PUBLIC;

DO $$BEGIN
 IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='mycfc_guardian_release_bind') THEN
  GRANT USAGE ON SCHEMA guardian_ops TO mycfc_guardian_release_bind;
  GRANT EXECUTE ON FUNCTION guardian_ops.release_intake_enabled(text,text,text) TO mycfc_guardian_release_bind;
 END IF;
END$$;

DO $$DECLARE definition text;old_clause text;new_clause text;
BEGIN
 SELECT pg_get_functiondef('public.privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)'::regprocedure) INTO definition;
 old_clause:='p_artifact->>''baseline_includes_through''<>''202609120006_guardian_schema_ready_owner''';
 new_clause:='p_artifact->>''baseline_includes_through''<>''202609120007_guardian_release_status''';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_release_status_privacy_evidence_predecessor_mismatch'; END IF;
 EXECUTE replace(definition,old_clause,new_clause);

 SELECT pg_get_functiondef('public.privacy_activation_authenticated_set_digest(text,uuid[])'::regprocedure) INTO definition;
 old_clause:='schema_row.baseline_includes_through=''202609120006_guardian_schema_ready_owner''';
 new_clause:='schema_row.baseline_includes_through=''202609120007_guardian_release_status''';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_release_status_privacy_digest_predecessor_mismatch'; END IF;
 EXECUTE replace(definition,old_clause,new_clause);
END$$;

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
