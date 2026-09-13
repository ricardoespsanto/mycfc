-- Optional outbound link and latest-change provenance; no result ingestion.
ALTER TABLE events
  ADD COLUMN official_results_url text NULL,
  ADD COLUMN results_updated_by_id uuid NULL REFERENCES users(id) ON DELETE RESTRICT,
  ADD COLUMN results_updated_at timestamptz NULL,
  ADD COLUMN results_version bigint NOT NULL DEFAULT 0,
  ADD CONSTRAINT events_results_type_valid CHECK (official_results_url IS NULL OR event_type = 'COMPETITION'),
  ADD CONSTRAINT events_results_url_valid CHECK (official_results_url IS NULL OR (
    octet_length(official_results_url) <= 2048
    AND official_results_url ~ $url$^https://(www\.)?fpcanoagem\.pt(/[A-Za-z0-9._~!$&'()*+,;=:@%/-]*)?$$url$
    AND official_results_url !~* '%($|[^0-9a-f]|[0-9a-f]($|[^0-9a-f]))'
    AND official_results_url !~* '%(0[0-9a-f]|1[0-9a-f]|7f|5c|3f|23|2f|25)'
    AND official_results_url !~* '%c2%[89][0-9a-f]'
  )),
  ADD CONSTRAINT events_results_provenance_valid CHECK (
    (results_version = 0 AND official_results_url IS NULL AND results_updated_by_id IS NULL AND results_updated_at IS NULL)
    OR (results_version > 0 AND results_updated_by_id IS NOT NULL AND results_updated_at IS NOT NULL)
  );

DO $$DECLARE definition text;rewritten text;
BEGIN
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint
 WHERE conrelid='privacy_activation_authenticated_artifacts'::regclass
  AND conname='privacy_activation_authenticated_artifacts_v15_check';
 IF definition IS NULL OR length(definition)-length(replace(definition,'202609120007_guardian_release_status',''))<>length('202609120007_guardian_release_status') THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='event_results_privacy_constraint_predecessor_mismatch';
 END IF;
 rewritten:=replace(definition,
  '''202609120007_guardian_release_status''',
  '''202609120007_guardian_release_status'', ''202609130001_event_results_links''');
 ALTER TABLE privacy_activation_authenticated_artifacts DROP CONSTRAINT privacy_activation_authenticated_artifacts_v15_check;
 EXECUTE 'ALTER TABLE privacy_activation_authenticated_artifacts ADD CONSTRAINT privacy_activation_authenticated_artifacts_v16_check '||rewritten||' NOT VALID';
 ALTER TABLE privacy_activation_authenticated_artifacts VALIDATE CONSTRAINT privacy_activation_authenticated_artifacts_v16_check;
END$$;

DO $$DECLARE definition text;old_clause text;new_clause text;
BEGIN
 SELECT pg_get_functiondef('public.privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)'::regprocedure) INTO definition;
 old_clause:='p_artifact->>''baseline_includes_through''<>''202609120007_guardian_release_status''';
 new_clause:='p_artifact->>''baseline_includes_through''<>''202609130001_event_results_links''';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='event_results_privacy_evidence_predecessor_mismatch'; END IF;
 EXECUTE replace(definition,old_clause,new_clause);

 SELECT pg_get_functiondef('public.privacy_activation_authenticated_set_digest(text,uuid[])'::regprocedure) INTO definition;
 old_clause:='schema_row.baseline_includes_through=''202609120007_guardian_release_status''';
 new_clause:='schema_row.baseline_includes_through=''202609130001_event_results_links''';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='event_results_privacy_digest_predecessor_mismatch'; END IF;
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
