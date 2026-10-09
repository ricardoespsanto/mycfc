-- Authenticate the one provider case that needs no adapter: the active
-- approval is bound to a current, complete, zero-registration inventory.
CREATE FUNCTION public.privacy_provider_empty_inventory_ready()
RETURNS boolean LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT EXISTS(
  SELECT 1
  FROM public.privacy_request_activation activation
  JOIN public.privacy_activation_approvals approval ON approval.id=activation.approval_id
  JOIN public.privacy_activation_proposals proposal ON proposal.id=approval.proposal_id
  JOIN public.privacy_worker_kill_switch switch_row ON switch_row.singleton AND NOT switch_row.engaged
   AND switch_row.activation_approval_id=approval.id
  JOIN public.privacy_activation_evidence evidence ON evidence.id=ANY(proposal.evidence_ids) AND evidence.kind='PROVIDER'
   AND evidence.expires_at>clock_timestamp() AND evidence.reference_code='mycfc/privacy-provider-registry/v2'
  JOIN public.privacy_activation_authenticated_artifacts artifact ON artifact.evidence_id=evidence.id AND artifact.kind='PROVIDER'
  WHERE activation.singleton AND activation.enabled AND activation.fulfilment_ready
   AND proposal.policy_version=activation.policy_version AND approval.activation_sha256=proposal.activation_sha256
   AND approval.approved_by_ref<>proposal.proposed_by_ref
   AND public.privacy_activation_authenticated_set_digest(proposal.policy_version,proposal.evidence_ids)=proposal.evidence_set_sha256
   AND artifact.policy_version=activation.policy_version
   AND artifact.provider_registry_state='READY' AND artifact.provider_registration_count=0
   AND artifact.provider_inventory_contract='mycfc/privacy-provider-registry-source/v2'
   AND octet_length(artifact.provider_registry_sha256)=32
 );
$$;

REVOKE ALL ON FUNCTION public.privacy_provider_empty_inventory_ready() FROM PUBLIC;

DO $$DECLARE definition text;rewritten text;
BEGIN
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint
 WHERE conrelid='privacy_activation_authenticated_artifacts'::regclass
  AND conname='privacy_activation_authenticated_artifacts_v19_check';
 IF definition IS NULL OR length(definition)-length(replace(definition,'202609170003_privacy_activation_fixed_access',''))<>length('202609170003_privacy_activation_fixed_access') THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='empty_provider_execution_privacy_constraint_predecessor_mismatch';
 END IF;
 rewritten:=replace(definition,
  '''202609170003_privacy_activation_fixed_access''',
  '''202609170003_privacy_activation_fixed_access'', ''202609170004_privacy_empty_provider_execution''');
 ALTER TABLE privacy_activation_authenticated_artifacts DROP CONSTRAINT privacy_activation_authenticated_artifacts_v19_check;
 EXECUTE 'ALTER TABLE privacy_activation_authenticated_artifacts ADD CONSTRAINT privacy_activation_authenticated_artifacts_v20_check '||rewritten||' NOT VALID';
 ALTER TABLE privacy_activation_authenticated_artifacts VALIDATE CONSTRAINT privacy_activation_authenticated_artifacts_v20_check;
END$$;

DO $$DECLARE definition text;old_clause text;new_clause text;
BEGIN
 SELECT pg_get_functiondef('public.privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)'::regprocedure) INTO definition;
 old_clause:='p_artifact->>''baseline_includes_through''<>''202609170003_privacy_activation_fixed_access''';
 new_clause:='p_artifact->>''baseline_includes_through''<>''202609170004_privacy_empty_provider_execution''';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='empty_provider_execution_privacy_evidence_predecessor_mismatch'; END IF;
 EXECUTE replace(definition,old_clause,new_clause);

 SELECT pg_get_functiondef('public.privacy_activation_authenticated_set_digest(text,uuid[])'::regprocedure) INTO definition;
 old_clause:='schema_row.baseline_includes_through=''202609170003_privacy_activation_fixed_access''';
 new_clause:='schema_row.baseline_includes_through=''202609170004_privacy_empty_provider_execution''';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='empty_provider_execution_privacy_digest_predecessor_mismatch'; END IF;
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
