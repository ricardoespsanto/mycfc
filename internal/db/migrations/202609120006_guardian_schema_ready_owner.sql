-- #280 guardian control-plane owner dependency repair.
-- Extend authenticated privacy evidence to this exact schema inventory while
-- keeping both privacy and guardian activation fail closed after the change.
DO $$DECLARE definition text;rewritten text;
BEGIN
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint
 WHERE conrelid='privacy_activation_authenticated_artifacts'::regclass
  AND conname='privacy_activation_authenticated_artifacts_v13_check';
 IF definition IS NULL OR length(definition)-length(replace(definition,'202609120005_guardian_authority_activation',''))<>length('202609120005_guardian_authority_activation') THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_schema_ready_privacy_constraint_predecessor_mismatch';
 END IF;
 rewritten:=replace(definition,
  '''202609120005_guardian_authority_activation''',
  '''202609120005_guardian_authority_activation'', ''202609120006_guardian_schema_ready_owner''');
 ALTER TABLE privacy_activation_authenticated_artifacts DROP CONSTRAINT privacy_activation_authenticated_artifacts_v13_check;
 EXECUTE 'ALTER TABLE privacy_activation_authenticated_artifacts ADD CONSTRAINT privacy_activation_authenticated_artifacts_v14_check '||rewritten||' NOT VALID';
 ALTER TABLE privacy_activation_authenticated_artifacts VALIDATE CONSTRAINT privacy_activation_authenticated_artifacts_v14_check;
END$$;

DO $$DECLARE definition text;old_clause text;new_clause text;
BEGIN
 SELECT pg_get_functiondef('public.privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)'::regprocedure) INTO definition;
 old_clause:='p_artifact->>''baseline_includes_through''<>''202609120005_guardian_authority_activation''';
 new_clause:='p_artifact->>''baseline_includes_through''<>''202609120006_guardian_schema_ready_owner''';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_schema_ready_privacy_evidence_predecessor_mismatch'; END IF;
 EXECUTE replace(definition,old_clause,new_clause);

 SELECT pg_get_functiondef('public.privacy_activation_authenticated_set_digest(text,uuid[])'::regprocedure) INTO definition;
 old_clause:='schema_row.baseline_includes_through=''202609120005_guardian_authority_activation''';
 new_clause:='schema_row.baseline_includes_through=''202609120006_guardian_schema_ready_owner''';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='guardian_schema_ready_privacy_digest_predecessor_mismatch'; END IF;
 EXECUTE replace(definition,old_clause,new_clause);
END$$;

-- Restore the one private helper capability required by the SECURITY DEFINER
-- guardian control-plane routines. Migration 005 revoked EXECUTE on every
-- guardian_ops function from their owner during hardening, which prevented the
-- routines from calling schema_ready even though callers retained only their
-- intended narrow entry points.
DO $$
DECLARE
 schema_ready_owner name;
 release_bind_owner name;
BEGIN
 SELECT pg_get_userbyid(proowner) INTO schema_ready_owner
 FROM pg_proc WHERE oid='guardian_ops.schema_ready(text)'::regprocedure;
 SELECT pg_get_userbyid(proowner) INTO release_bind_owner
 FROM pg_proc WHERE oid='guardian_ops.release_disable_and_bind(text,text,text)'::regprocedure;

 IF schema_ready_owner IS NULL OR release_bind_owner IS NULL OR schema_ready_owner<>release_bind_owner THEN
  RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='guardian_schema_ready_owner_mismatch';
 END IF;

 EXECUTE format('GRANT EXECUTE ON FUNCTION guardian_ops.schema_ready(text) TO %I',schema_ready_owner);
END$$;

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
