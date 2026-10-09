-- Synthetic acceptance uses only database-created identities and a 256-bit
-- capability bound to their immutable registry. It cannot adopt an existing user.
CREATE TABLE privacy_protected.acceptance_fixtures (
 id uuid PRIMARY KEY, subject_ref uuid NOT NULL UNIQUE, reviewer_ref uuid NOT NULL UNIQUE,
 executor_ref uuid NOT NULL UNIQUE, worker_ref uuid NOT NULL UNIQUE,
 proof_sha256 bytea NOT NULL CHECK(octet_length(proof_sha256)=32),
 marker_sha256 bytea NOT NULL CHECK(octet_length(marker_sha256)=32),
 image_digest text NOT NULL CHECK(image_digest ~ '^sha256:[0-9a-f]{64}$'),
 schema_digest text NOT NULL CHECK(schema_digest ~ '^[0-9a-f]{64}$'),
 created_at timestamptz NOT NULL, expires_at timestamptz NOT NULL,
 CHECK(expires_at=created_at+interval '1 hour'),
 CHECK(subject_ref<>reviewer_ref AND subject_ref<>executor_ref AND reviewer_ref<>executor_ref)
);
CREATE TABLE privacy_protected.acceptance_finished (fixture_id uuid PRIMARY KEY REFERENCES privacy_protected.acceptance_fixtures(id), finished_at timestamptz NOT NULL DEFAULT clock_timestamp());
CREATE TRIGGER acceptance_finished_immutable BEFORE UPDATE OR DELETE ON privacy_protected.acceptance_finished FOR EACH ROW EXECUTE FUNCTION prevent_privacy_audit_mutation();
CREATE TABLE privacy_protected.acceptance_requests (
 fixture_id uuid PRIMARY KEY REFERENCES privacy_protected.acceptance_fixtures(id),
 request_id uuid NOT NULL UNIQUE
);
CREATE TABLE privacy_protected.acceptance_notice_simulations (
 outbox_id uuid PRIMARY KEY, fixture_id uuid NOT NULL REFERENCES privacy_protected.acceptance_fixtures(id),
 message_type text NOT NULL, payload_sha256 bytea NOT NULL, observed_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TRIGGER acceptance_fixtures_immutable BEFORE UPDATE OR DELETE ON privacy_protected.acceptance_fixtures
 FOR EACH ROW EXECUTE FUNCTION prevent_privacy_audit_mutation();
CREATE TRIGGER acceptance_requests_immutable BEFORE UPDATE OR DELETE ON privacy_protected.acceptance_requests
 FOR EACH ROW EXECUTE FUNCTION prevent_privacy_audit_mutation();
CREATE TRIGGER acceptance_simulations_immutable BEFORE UPDATE OR DELETE ON privacy_protected.acceptance_notice_simulations
 FOR EACH ROW EXECUTE FUNCTION prevent_privacy_audit_mutation();

CREATE FUNCTION privacy_protected.acceptance_create(p_password_hash text,p_image text,p_schema text)
RETURNS TABLE(fixture_id uuid,subject_ref uuid,reviewer_ref uuid,executor_ref uuid,worker_ref uuid,proof bytea,marker_sha256 bytea)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public,pg_temp AS $$
DECLARE f uuid:=gen_random_uuid();s uuid:=gen_random_uuid();r uuid:=gen_random_uuid();e uuid:=gen_random_uuid();w uuid:=gen_random_uuid();
 token bytea:=gen_random_bytes(32);marker bytea;at timestamptz:=clock_timestamp();grant_ref uuid;
BEGIN
 IF session_user<>'mycfc_privacy_acceptance' OR p_password_hash IS NULL OR p_image IS NULL OR p_schema IS NULL OR p_password_hash !~ '^\$2[aby]\$[0-9]{2}\$[./A-Za-z0-9]{53}$'
  OR p_image !~ '^sha256:[0-9a-f]{64}$' OR p_schema !~ '^[0-9a-f]{64}$'
 THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_acceptance_rejected'; END IF;
 PERFORM privacy_worker_require_activation();
 IF NOT EXISTS(SELECT 1 FROM privacy_request_activation a JOIN privacy_activation_approvals approval ON approval.id=a.approval_id
   JOIN privacy_activation_proposals proposal ON proposal.id=approval.proposal_id
   JOIN privacy_activation_authenticated_artifacts artifact ON artifact.evidence_id=ANY(proposal.evidence_ids) AND artifact.kind='SCHEMA'
   WHERE a.singleton AND a.enabled AND artifact.image_digest=p_image AND encode(artifact.schema_migration_digest,'hex')=p_schema) THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_acceptance_activation_required'; END IF;
 marker:=hmac(convert_to('mycfc/privacy-acceptance-fixture/v1:'||f||':'||s||':'||r||':'||e||':'||w||':'||p_image||':'||p_schema,'UTF8'),token,'sha256');
 INSERT INTO privacy_protected.acceptance_fixtures VALUES(f,s,r,e,w,digest(token,'sha256'),marker,p_image,p_schema,at,at+interval '1 hour');
 INSERT INTO users(id,name,email,password_hash,date_of_birth,leaderboard_visible)
 VALUES(s,'Synthetic privacy acceptance fixture','synthetic-'||s||'@invalid.invalid',p_password_hash,'1900-01-01',false),
 (r,'Synthetic privacy acceptance reviewer','synthetic-'||r||'@invalid.invalid','synthetic-disabled','1900-01-01',false),
 (e,'Synthetic privacy acceptance executor','synthetic-'||e||'@invalid.invalid','synthetic-disabled','1900-01-01',false);
 INSERT INTO privacy_reviewer_grants(user_id,granted_by,granted_at) VALUES(r,e,at) RETURNING id INTO grant_ref;
 INSERT INTO privacy_reviewer_grant_events(grant_id,actor_ref,action,occurred_at) VALUES(grant_ref,e,'GRANTED',at);
 INSERT INTO privacy_executor_grants(user_id,granted_by,granted_at) VALUES(e,r,at) RETURNING id INTO grant_ref;
 INSERT INTO privacy_executor_grant_events(grant_id,actor_ref,action,occurred_at) VALUES(grant_ref,r,'GRANTED',at);
 RETURN QUERY SELECT f,s,r,e,w,token,marker;
END;$$;

-- Bind the very first real service-submitted request to its synthetic subject;
-- a marker must never be attached to an existing request or another person.
CREATE FUNCTION privacy_acceptance_bind_request() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public,pg_temp AS $$
DECLARE fixture privacy_protected.acceptance_fixtures%ROWTYPE;
BEGIN
 IF TG_OP='UPDATE' THEN
  SELECT f.* INTO fixture FROM privacy_protected.acceptance_fixtures f JOIN privacy_protected.acceptance_requests r ON r.fixture_id=f.id WHERE r.request_id=OLD.id;
  IF FOUND THEN
   IF NEW.id IS DISTINCT FROM OLD.id OR (NEW.subject_user_id IS NOT NULL AND NEW.subject_user_id<>fixture.subject_ref)
    OR (NEW.requester_user_id IS NOT NULL AND NEW.requester_user_id<>fixture.subject_ref)
   THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_acceptance_request_retarget_forbidden'; END IF;
   RETURN NEW;
  END IF;
  IF EXISTS(SELECT 1 FROM privacy_protected.acceptance_fixtures WHERE subject_ref=NEW.subject_user_id) THEN
   RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_acceptance_existing_request_forbidden'; END IF;
  RETURN NEW;
 END IF;
 SELECT * INTO fixture FROM privacy_protected.acceptance_fixtures WHERE subject_ref=NEW.subject_user_id;
 IF NOT FOUND THEN RETURN NEW; END IF;
 IF NEW.requester_user_id IS DISTINCT FROM fixture.subject_ref OR fixture.expires_at<=clock_timestamp()
  OR EXISTS(SELECT 1 FROM privacy_protected.acceptance_finished WHERE fixture_id=fixture.id)
  OR digest(decode(current_setting('mycfc.acceptance_proof',true),'hex'),'sha256') IS DISTINCT FROM fixture.proof_sha256
 THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_acceptance_request_rejected'; END IF;
 INSERT INTO privacy_protected.acceptance_requests VALUES(fixture.id,NEW.id);
 RETURN NEW;
END;$$;
CREATE TRIGGER privacy_acceptance_request_bound BEFORE INSERT OR UPDATE ON data_erasure_requests
 FOR EACH ROW EXECUTE FUNCTION privacy_acceptance_bind_request();

-- No generated identity may gain a browser session or recovery credential.
CREATE FUNCTION privacy_acceptance_reject_auth() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public,pg_temp AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM privacy_protected.acceptance_fixtures WHERE NEW.user_id IN(subject_ref,reviewer_ref,executor_ref)) THEN
  RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_acceptance_auth_forbidden'; END IF;
 RETURN NEW;
END;$$;
CREATE TRIGGER acceptance_no_session BEFORE INSERT OR UPDATE ON sessions FOR EACH ROW EXECUTE FUNCTION privacy_acceptance_reject_auth();
CREATE TRIGGER acceptance_no_reset BEFORE INSERT OR UPDATE ON password_reset_tokens FOR EACH ROW EXECUTE FUNCTION privacy_acceptance_reject_auth();
CREATE TRIGGER acceptance_no_verify BEFORE INSERT OR UPDATE ON email_verification_tokens FOR EACH ROW EXECUTE FUNCTION privacy_acceptance_reject_auth();

-- Cancel before the row can ever become visible to any email sender. Keep the
-- encrypted processing target for the existing completion machinery, recording
-- only the ciphertext digest in the simulation receipt. Attempts to requeue a
-- synthetic notice remain cancelled. No timestamp or external send is faked.
CREATE FUNCTION privacy_acceptance_simulate_notice() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public,pg_temp AS $$
DECLARE f uuid;
BEGIN
 IF TG_OP='UPDATE' THEN
  SELECT fixture_id INTO f FROM privacy_protected.acceptance_notice_simulations WHERE outbox_id=OLD.id;
  IF FOUND AND (NEW.id IS DISTINCT FROM OLD.id OR NEW.privacy_request_id IS DISTINCT FROM OLD.privacy_request_id) THEN
   RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_acceptance_notice_retarget_forbidden'; END IF;
 END IF;
 IF f IS NULL THEN SELECT fixture_id INTO f FROM privacy_protected.acceptance_requests WHERE request_id=NEW.privacy_request_id; END IF;
 IF f IS NOT NULL THEN
  IF TG_OP='INSERT' AND NEW.sealed_payload IS NULL THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_acceptance_notice_rejected'; END IF;
  NEW.status:='CANCELLED';NEW.claimed_at:=NULL;NEW.sent_at:=NULL;
  IF TG_OP='INSERT' THEN
  INSERT INTO privacy_protected.acceptance_notice_simulations(outbox_id,fixture_id,message_type,payload_sha256)
  VALUES(NEW.id,f,NEW.message_type,digest(NEW.sealed_payload,'sha256')) ON CONFLICT(outbox_id) DO NOTHING;
  END IF;
 END IF;
 RETURN NEW;
END;$$;
CREATE TRIGGER acceptance_no_external_notice BEFORE INSERT OR UPDATE ON email_outbox FOR EACH ROW EXECUTE FUNCTION privacy_acceptance_simulate_notice();

CREATE FUNCTION privacy_acceptance_authorized(p_worker uuid,p_proof bytea) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public,pg_temp AS $$
DECLARE result uuid;
BEGIN
 IF octet_length(p_proof) IS DISTINCT FROM 32 THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_acceptance_rejected'; END IF;
 SELECT request.request_id INTO result FROM privacy_protected.acceptance_fixtures fixture
 JOIN privacy_protected.acceptance_requests request ON request.fixture_id=fixture.id
 WHERE fixture.worker_ref=p_worker AND fixture.proof_sha256=digest(p_proof,'sha256') AND fixture.expires_at>clock_timestamp() AND NOT EXISTS(SELECT 1 FROM privacy_protected.acceptance_finished WHERE fixture_id=fixture.id);
 IF result IS NULL THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_acceptance_rejected'; END IF;
 RETURN result;
END;$$;

-- Clone the reviewed claim implementation with an exact synthetic request
-- predicate. Retain its ordering, dependencies, SKIP LOCKED and lease fences.
DO $$DECLARE source text;candidate text;
BEGIN
 source:=pg_get_functiondef('privacy_worker_claim_inner_013(bigint,uuid)'::regprocedure);
 IF strpos(source,'WHERE p_lease_milliseconds BETWEEN 1000 AND 3600000')=0 THEN
  RAISE EXCEPTION 'privacy_acceptance_claim_predecessor_mismatch'; END IF;
 candidate:=replace(source,'privacy_worker_claim_inner_013(p_lease_milliseconds bigint, p_worker_ref uuid)',
  'privacy_acceptance_claim_inner(p_lease_milliseconds bigint, p_worker_ref uuid, p_request_id uuid)');
 IF candidate=source THEN RAISE EXCEPTION 'privacy_acceptance_claim_signature_mismatch'; END IF;
 candidate:=replace(candidate,'WHERE p_lease_milliseconds BETWEEN 1000 AND 3600000',
  'WHERE job.execution_id IN (SELECT id FROM public.privacy_erasure_executions WHERE request_id=p_request_id) AND p_lease_milliseconds BETWEEN 1000 AND 3600000');
 EXECUTE candidate;
 source:=replace(source,'WHERE p_lease_milliseconds BETWEEN 1000 AND 3600000',
  'WHERE NOT EXISTS(SELECT 1 FROM public.privacy_erasure_executions execution JOIN privacy_protected.acceptance_requests synthetic ON synthetic.request_id=execution.request_id WHERE execution.id=job.execution_id) AND p_lease_milliseconds BETWEEN 1000 AND 3600000');
 EXECUTE source;
END$$;
CREATE FUNCTION privacy_acceptance_claim(p_lease_milliseconds bigint,p_worker_ref uuid,p_proof bytea)
RETURNS TABLE(job_id uuid,lease_id uuid,attempt_id uuid) LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public,pg_temp AS $$
DECLARE request_ref uuid;
BEGIN
 PERFORM privacy_worker_require_activation();
 request_ref:=privacy_acceptance_authorized(p_worker_ref,p_proof);
 RETURN QUERY SELECT * FROM privacy_acceptance_claim_inner(p_lease_milliseconds,p_worker_ref,request_ref);
END;$$;

-- Ordinary workers must not attempt synthetic completion with their delivery
-- key; acceptance uses the same finalizer with a fixture-local delivery key.
DO $$DECLARE source text;
BEGIN
 source:=pg_get_functiondef('privacy_completion_list_pending_inner_013(uuid,integer)'::regprocedure);
 IF strpos(source,'WHERE p_worker_ref IS NOT NULL')=0 THEN RAISE EXCEPTION 'privacy_acceptance_completion_predecessor_mismatch'; END IF;
 EXECUTE replace(source,'WHERE p_worker_ref IS NOT NULL',
  'WHERE NOT EXISTS(SELECT 1 FROM privacy_protected.acceptance_requests fixture WHERE fixture.request_id=execution.request_id) AND p_worker_ref IS NOT NULL');
END$$;
CREATE OR REPLACE FUNCTION privacy_worker_status()
RETURNS TABLE(pending bigint,leased bigint,retryable bigint,terminal bigint,aged_nonterminal bigint)
LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,public,pg_temp AS $$
 SELECT count(*) FILTER(WHERE job.status='PENDING'),count(*) FILTER(WHERE job.status='LEASED'),
 count(*) FILTER(WHERE job.status='RETRY_WAIT'),count(*) FILTER(WHERE job.status='TERMINAL_FAILED'),
 count(*) FILTER(WHERE job.status IN('PENDING','LEASED','RETRY_WAIT') AND job.updated_at<=clock_timestamp()-interval '15 minutes')
 FROM privacy_erasure_category_jobs job WHERE NOT EXISTS(SELECT 1 FROM privacy_erasure_executions execution JOIN privacy_protected.acceptance_requests synthetic ON synthetic.request_id=execution.request_id WHERE execution.id=job.execution_id);
$$;
REVOKE ALL ON TABLE privacy_protected.acceptance_finished,privacy_protected.acceptance_fixtures,privacy_protected.acceptance_requests,privacy_protected.acceptance_notice_simulations FROM PUBLIC;
REVOKE ALL ON FUNCTION privacy_protected.acceptance_create(text,text,text),privacy_acceptance_authorized(uuid,bytea),privacy_acceptance_claim_inner(bigint,uuid,uuid),privacy_acceptance_claim(bigint,uuid,bytea),privacy_acceptance_bind_request() FROM PUBLIC;

CREATE FUNCTION privacy_protected.acceptance_finish(p_worker uuid,p_proof bytea) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public,pg_temp AS $$
DECLARE fixture privacy_protected.acceptance_fixtures%ROWTYPE;at timestamptz:=clock_timestamp();
BEGIN
 SELECT * INTO fixture FROM privacy_protected.acceptance_fixtures WHERE worker_ref=p_worker AND proof_sha256=digest(p_proof,'sha256');
 IF octet_length(p_proof) IS DISTINCT FROM 32 OR fixture.id IS NULL THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_acceptance_rejected'; END IF;
 INSERT INTO privacy_protected.acceptance_finished(fixture_id) VALUES(fixture.id) ON CONFLICT DO NOTHING;
 UPDATE users SET is_active=false,credential_version=credential_version+1,updated_at=at
 WHERE id IN(fixture.subject_ref,fixture.reviewer_ref,fixture.executor_ref) AND is_active;
 WITH revoked AS(UPDATE privacy_reviewer_grants SET revoked_by=fixture.executor_ref,revoked_at=at WHERE user_id=fixture.reviewer_ref AND revoked_at IS NULL RETURNING id)
 INSERT INTO privacy_reviewer_grant_events(grant_id,actor_ref,action,occurred_at) SELECT id,fixture.executor_ref,'REVOKED',at FROM revoked;
 WITH revoked AS(UPDATE privacy_executor_grants SET revoked_by=fixture.reviewer_ref,revoked_at=at WHERE user_id=fixture.executor_ref AND revoked_at IS NULL RETURNING id)
 INSERT INTO privacy_executor_grant_events(grant_id,actor_ref,action,occurred_at) SELECT id,fixture.reviewer_ref,'REVOKED',at FROM revoked;
END;$$;

CREATE FUNCTION privacy_protected.acceptance_observe(p_worker uuid,p_proof bytea)
RETURNS TABLE(marker_sha256 bytea,manifest_sha256 bytea,simulated_notices bigint,checkpoints bigint)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public,pg_temp AS $$
DECLARE request_ref uuid;fixture privacy_protected.acceptance_fixtures%ROWTYPE;execution_ref uuid;
BEGIN
 request_ref:=privacy_acceptance_authorized(p_worker,p_proof);
 SELECT * INTO fixture FROM privacy_protected.acceptance_fixtures WHERE worker_ref=p_worker;
 SELECT id INTO execution_ref FROM privacy_erasure_executions WHERE request_id=request_ref AND status='SUCCEEDED';
 IF execution_ref IS NULL OR NOT EXISTS(SELECT 1 FROM data_erasure_requests WHERE id=request_ref AND status='COMPLETED')
  OR NOT EXISTS(SELECT 1 FROM users WHERE id=fixture.subject_ref AND erased_at IS NOT NULL AND erasure_execution_id=execution_ref AND email IS NULL AND password_hash IS NULL AND NOT is_active)
  OR EXISTS(SELECT 1 FROM sessions WHERE user_id=fixture.subject_ref)
  OR EXISTS(SELECT 1 FROM member_profiles WHERE user_id=fixture.subject_ref)
  OR EXISTS(SELECT 1 FROM activity_connections WHERE user_id=fixture.subject_ref)
  OR EXISTS(SELECT 1 FROM privacy_protected.object_targets WHERE execution_id=execution_ref)
  OR EXISTS(SELECT 1 FROM privacy_protected.provider_targets WHERE execution_id=execution_ref)
  OR EXISTS(SELECT 1 FROM email_outbox WHERE privacy_request_id=request_ref AND (status<>'CANCELLED' OR sent_at IS NOT NULL))
  OR NOT EXISTS(SELECT 1 FROM privacy_protected.acceptance_notice_simulations WHERE fixture_id=fixture.id AND message_type='PRIVACY_COMPLETED')
  OR NOT EXISTS(SELECT 1 FROM privacy_protected.restore_tombstone_closure_receipts WHERE execution_id=execution_ref AND ledger_version='restore-tombstone-closure/v4')
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_acceptance_absence_unverified'; END IF;
 RETURN QUERY SELECT fixture.marker_sha256,manifest.manifest_sha256,
  (SELECT count(*) FROM privacy_protected.acceptance_notice_simulations WHERE fixture_id=fixture.id),
  (SELECT count(*) FROM privacy_erasure_job_checkpoints checkpoint JOIN privacy_erasure_category_jobs job ON job.id=checkpoint.job_id WHERE job.execution_id=execution_ref AND checkpoint.status='SUCCEEDED')
 FROM privacy_erasure_completion_manifests manifest WHERE manifest.execution_id=execution_ref;
END;$$;
REVOKE ALL ON FUNCTION privacy_protected.acceptance_finish(uuid,bytea),privacy_protected.acceptance_observe(uuid,bytea) FROM PUBLIC;

DO $$DECLARE definition text;rewritten text;
BEGIN
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint
 WHERE conrelid='privacy_activation_authenticated_artifacts'::regclass
  AND conname='privacy_activation_authenticated_artifacts_v16_check';
 IF definition IS NULL OR length(definition)-length(replace(definition,'202609130001_event_results_links',''))<>length('202609130001_event_results_links') THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='synthetic_acceptance_privacy_constraint_predecessor_mismatch';
 END IF;
 rewritten:=replace(definition,
  '''202609130001_event_results_links''',
  '''202609130001_event_results_links'', ''202609170001_privacy_synthetic_acceptance''');
 ALTER TABLE privacy_activation_authenticated_artifacts DROP CONSTRAINT privacy_activation_authenticated_artifacts_v16_check;
 EXECUTE 'ALTER TABLE privacy_activation_authenticated_artifacts ADD CONSTRAINT privacy_activation_authenticated_artifacts_v17_check '||rewritten||' NOT VALID';
 ALTER TABLE privacy_activation_authenticated_artifacts VALIDATE CONSTRAINT privacy_activation_authenticated_artifacts_v17_check;
END$$;

DO $$DECLARE definition text;old_clause text;new_clause text;
BEGIN
 SELECT pg_get_functiondef('public.privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)'::regprocedure) INTO definition;
 old_clause:='p_artifact->>''baseline_includes_through''<>''202609130001_event_results_links''';
 new_clause:='p_artifact->>''baseline_includes_through''<>''202609170001_privacy_synthetic_acceptance''';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='synthetic_acceptance_privacy_evidence_predecessor_mismatch'; END IF;
 EXECUTE replace(definition,old_clause,new_clause);

 SELECT pg_get_functiondef('public.privacy_activation_authenticated_set_digest(text,uuid[])'::regprocedure) INTO definition;
 old_clause:='schema_row.baseline_includes_through=''202609130001_event_results_links''';
 new_clause:='schema_row.baseline_includes_through=''202609170001_privacy_synthetic_acceptance''';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='synthetic_acceptance_privacy_digest_predecessor_mismatch'; END IF;
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
