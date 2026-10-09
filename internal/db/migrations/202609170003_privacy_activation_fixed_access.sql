-- The application can inspect and lock only the activation fields required to
-- gate execution. SECURITY DEFINER keeps the activation control row private
-- while preserving the transaction-scoped row lock used by StartExecution.
CREATE FUNCTION public.privacy_activation_snapshot()
RETURNS TABLE(policy_version text,enabled boolean,fulfilment_ready boolean,approval_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT activation.policy_version::text,activation.enabled,activation.fulfilment_ready,
  COALESCE(activation.approval_id,'00000000-0000-0000-0000-000000000000'::uuid)
 FROM public.privacy_request_activation activation
 WHERE activation.singleton
$$;

CREATE FUNCTION public.privacy_activation_lock()
RETURNS TABLE(policy_version text,enabled boolean,fulfilment_ready boolean,approval_id uuid)
LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
 RETURN QUERY
 SELECT activation.policy_version::text,activation.enabled,activation.fulfilment_ready,
  COALESCE(activation.approval_id,'00000000-0000-0000-0000-000000000000'::uuid)
 FROM public.privacy_request_activation activation
 WHERE activation.singleton
 FOR UPDATE OF activation;
END;$$;

REVOKE ALL ON FUNCTION public.privacy_activation_snapshot(),public.privacy_activation_lock() FROM PUBLIC;

DO $$DECLARE definition text;rewritten text;
BEGIN
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint
 WHERE conrelid='privacy_activation_authenticated_artifacts'::regclass
  AND conname='privacy_activation_authenticated_artifacts_v18_check';
 IF definition IS NULL OR length(definition)-length(replace(definition,'202609170002_privacy_executor_retention_handlers',''))<>length('202609170002_privacy_executor_retention_handlers') THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='activation_fixed_access_privacy_constraint_predecessor_mismatch';
 END IF;
 rewritten:=replace(definition,
  '''202609170002_privacy_executor_retention_handlers''',
  '''202609170002_privacy_executor_retention_handlers'', ''202609170003_privacy_activation_fixed_access''');
 ALTER TABLE privacy_activation_authenticated_artifacts DROP CONSTRAINT privacy_activation_authenticated_artifacts_v18_check;
 EXECUTE 'ALTER TABLE privacy_activation_authenticated_artifacts ADD CONSTRAINT privacy_activation_authenticated_artifacts_v19_check '||rewritten||' NOT VALID';
 ALTER TABLE privacy_activation_authenticated_artifacts VALIDATE CONSTRAINT privacy_activation_authenticated_artifacts_v19_check;
END$$;

DO $$DECLARE definition text;old_clause text;new_clause text;
BEGIN
 SELECT pg_get_functiondef('public.privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)'::regprocedure) INTO definition;
 old_clause:='p_artifact->>''baseline_includes_through''<>''202609170002_privacy_executor_retention_handlers''';
 new_clause:='p_artifact->>''baseline_includes_through''<>''202609170003_privacy_activation_fixed_access''';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='activation_fixed_access_privacy_evidence_predecessor_mismatch'; END IF;
 EXECUTE replace(definition,old_clause,new_clause);

 SELECT pg_get_functiondef('public.privacy_activation_authenticated_set_digest(text,uuid[])'::regprocedure) INTO definition;
 old_clause:='schema_row.baseline_includes_through=''202609170002_privacy_executor_retention_handlers''';
 new_clause:='schema_row.baseline_includes_through=''202609170003_privacy_activation_fixed_access''';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='activation_fixed_access_privacy_digest_predecessor_mismatch'; END IF;
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
