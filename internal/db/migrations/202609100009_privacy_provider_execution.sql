-- #246 external provider/recipient execution foundation. The source registry is
-- deliberately empty in production. These protected rows and routines remain
-- inert until #109 facts, runtime adapters, worker credentials, and activation
-- are separately approved.

CREATE TABLE privacy_protected.provider_connections (
 id uuid PRIMARY KEY, subject_user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE RESTRICT,
 service_code varchar(120) NOT NULL, provider_role varchar(30) NOT NULL CHECK(provider_role IN ('PROCESSOR','AUTONOMOUS_RECIPIENT')),
 provider_contract_version varchar(80) NOT NULL, registry_evidence_key_id varchar(80) NOT NULL,
 registry_evidence_digest bytea NOT NULL CHECK(octet_length(registry_evidence_digest)=32),
 target_key_id varchar(80) NOT NULL, target_opaque bytea NOT NULL CHECK(octet_length(target_opaque) BETWEEN 1 AND 16384),
 credential_key_id varchar(80) NULL, credential_opaque bytea NULL,
 state varchar(20) NOT NULL CHECK(state IN ('ACTIVE','QUARANTINED','DISCONNECTED')),
 state_version bigint NOT NULL DEFAULT 1 CHECK(state_version>0), sync_enabled boolean NOT NULL DEFAULT false,
 webhook_enabled boolean NOT NULL DEFAULT false, reconnect_enabled boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
 CHECK(service_code=btrim(service_code) AND service_code~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,119}$'),
 CHECK(provider_contract_version=btrim(provider_contract_version) AND provider_contract_version~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'),
 CHECK(registry_evidence_key_id=btrim(registry_evidence_key_id) AND registry_evidence_key_id~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'),
 CHECK(target_key_id=btrim(target_key_id) AND target_key_id~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'),
 CHECK((credential_key_id IS NULL)=(credential_opaque IS NULL)),
 CHECK(credential_key_id IS NULL OR (credential_key_id=btrim(credential_key_id) AND credential_key_id~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$' AND octet_length(credential_opaque) BETWEEN 1 AND 16384)),
 CHECK((state='ACTIVE' AND credential_opaque IS NOT NULL)
    OR (state IN ('QUARANTINED','DISCONNECTED') AND credential_opaque IS NULL AND NOT sync_enabled AND NOT webhook_enabled AND NOT reconnect_enabled)),
 CHECK(updated_at>=created_at)
);
CREATE INDEX privacy_provider_connections_subject_idx ON privacy_protected.provider_connections(subject_user_id,state,service_code,id);

CREATE TABLE privacy_protected.provider_capture_sets (
 execution_id uuid NOT NULL REFERENCES public.privacy_erasure_executions(id) ON DELETE RESTRICT,
 job_id uuid NOT NULL, checkpoint_id uuid PRIMARY KEY, subject_user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE RESTRICT,
 category_key varchar(120) NOT NULL, expected_target_count integer NOT NULL CHECK(expected_target_count>=0),
 operation_code varchar(120) NOT NULL CHECK(operation_code='PROVIDER_RECIPIENT_NOTIFY'),
 action_version varchar(40) NOT NULL CHECK(action_version='v1'), created_at timestamptz NOT NULL,
 FOREIGN KEY(job_id) REFERENCES public.privacy_erasure_category_jobs(id) ON DELETE RESTRICT,
 FOREIGN KEY(checkpoint_id,job_id,operation_code,action_version) REFERENCES public.privacy_erasure_job_checkpoints(id,job_id,operation_code,action_version) ON DELETE RESTRICT,
 UNIQUE(execution_id,category_key)
);

CREATE TABLE privacy_protected.provider_targets (
 id uuid PRIMARY KEY, connection_id uuid NOT NULL, execution_id uuid NOT NULL, job_id uuid NOT NULL, checkpoint_id uuid NOT NULL,
 plan_entry_sha256 bytea NOT NULL CHECK(octet_length(plan_entry_sha256)=32), category_key varchar(120) NOT NULL,
 service_code varchar(120) NOT NULL, target_kind varchar(40) NOT NULL CHECK(target_kind='REMOTE_ACCOUNT'),
 provider_role varchar(30) NOT NULL CHECK(provider_role IN ('PROCESSOR','AUTONOMOUS_RECIPIENT')),
 target_version bigint NOT NULL CHECK(target_version>0), operation_code varchar(120) NOT NULL CHECK(operation_code='PROVIDER_RECIPIENT_NOTIFY'),
 action_version varchar(40) NOT NULL CHECK(action_version='v1'), provider_contract_version varchar(80) NOT NULL,
 registry_evidence_key_id varchar(80) NOT NULL, registry_evidence_digest bytea NOT NULL CHECK(octet_length(registry_evidence_digest)=32),
 local_state varchar(20) NOT NULL CHECK(local_state IN ('ACTIVE','DISCONNECTED')),
 target_envelope_version varchar(100) NOT NULL CHECK(target_envelope_version='x25519-aes256gcm-hkdfsha256/provider-target-v1'),
 target_algorithm varchar(80) NOT NULL CHECK(target_algorithm='X25519-HKDF-SHA256-AES-256-GCM'), target_encryption_key_id varchar(80) NOT NULL,
 target_encapsulation bytea NOT NULL CHECK(octet_length(target_encapsulation)=32), target_nonce bytea NOT NULL CHECK(octet_length(target_nonce)=12),
 target_ciphertext bytea NOT NULL CHECK(octet_length(target_ciphertext) BETWEEN 17 AND 18432), created_at timestamptz NOT NULL,
 FOREIGN KEY(job_id,execution_id,plan_entry_sha256,category_key) REFERENCES public.privacy_erasure_category_jobs(id,execution_id,entry_sha256,category_key) ON DELETE RESTRICT,
 FOREIGN KEY(checkpoint_id,job_id,operation_code,action_version) REFERENCES public.privacy_erasure_job_checkpoints(id,job_id,operation_code,action_version) ON DELETE RESTRICT,
 CHECK(service_code=btrim(service_code) AND service_code~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,119}$'),
 CHECK(provider_contract_version=btrim(provider_contract_version) AND provider_contract_version~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'),
 CHECK(registry_evidence_key_id=btrim(registry_evidence_key_id) AND registry_evidence_key_id~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'),
 CHECK(target_encryption_key_id=btrim(target_encryption_key_id) AND target_encryption_key_id~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'),
 UNIQUE(id,job_id), UNIQUE(connection_id), UNIQUE(id,execution_id,service_code,target_kind)
);
CREATE INDEX privacy_provider_targets_checkpoint_idx ON privacy_protected.provider_targets(checkpoint_id,id);

CREATE TABLE privacy_protected.provider_target_digests (
 target_id uuid NOT NULL, execution_id uuid NOT NULL REFERENCES public.privacy_erasure_executions(id) ON DELETE RESTRICT,
 service_code varchar(120) NOT NULL, target_kind varchar(40) NOT NULL CHECK(target_kind='REMOTE_ACCOUNT'),
 digest_key_id varchar(80) NOT NULL, target_digest bytea NOT NULL CHECK(octet_length(target_digest)=32), created_at timestamptz NOT NULL,
 PRIMARY KEY(target_id,digest_key_id),
 FOREIGN KEY(target_id,execution_id,service_code,target_kind) REFERENCES privacy_protected.provider_targets(id,execution_id,service_code,target_kind) ON DELETE RESTRICT,
 CHECK(digest_key_id=btrim(digest_key_id) AND digest_key_id~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'),
 UNIQUE(execution_id,service_code,target_kind,digest_key_id,target_digest)
);

CREATE TABLE privacy_protected.provider_credential_quarantine (
 target_id uuid PRIMARY KEY REFERENCES privacy_protected.provider_targets(id) ON DELETE RESTRICT,
 source_commitment_key_id varchar(80) NOT NULL, source_commitment bytea NOT NULL CHECK(octet_length(source_commitment)=32),
 envelope_version varchar(100) NOT NULL CHECK(envelope_version='x25519-aes256gcm-hkdfsha256/provider-target-v1'),
 algorithm varchar(80) NOT NULL CHECK(algorithm='X25519-HKDF-SHA256-AES-256-GCM'), encryption_key_id varchar(80) NOT NULL,
 encapsulation bytea NOT NULL CHECK(octet_length(encapsulation)=32), nonce bytea NOT NULL CHECK(octet_length(nonce)=12),
 ciphertext bytea NOT NULL CHECK(octet_length(ciphertext) BETWEEN 17 AND 18432), quarantined_at timestamptz NOT NULL,
 CHECK(source_commitment_key_id=btrim(source_commitment_key_id) AND source_commitment_key_id~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'),
 CHECK(encryption_key_id=btrim(encryption_key_id) AND encryption_key_id~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$')
);

CREATE TABLE privacy_protected.provider_evidence (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), target_id uuid NOT NULL UNIQUE, job_id uuid NOT NULL, attempt_id uuid NOT NULL,
 evidence_version varchar(50) NOT NULL CHECK(evidence_version='provider-structured-evidence/v1'),
 outcome_code varchar(40) NOT NULL CHECK(outcome_code IN ('REMOTE_DELETION_VERIFIED','ALREADY_DISCONNECTED','NOT_CONTROLLABLE')),
 adapter_attempts integer NOT NULL CHECK(adapter_attempts BETWEEN 1 AND 5),
 evidence_code varchar(50) NOT NULL CHECK(evidence_code IN ('REMOTE_DELETION_RECEIPT','REMOTE_ALREADY_DISCONNECTED','RECIPIENT_NOTIFICATION_CONFIRMED')),
 recipient_role varchar(40) NULL CHECK(recipient_role='AUTONOMOUS_RECIPIENT'),
 channel_code varchar(30) NULL CHECK(channel_code IN ('API','EMAIL','PORTAL','REGISTERED_POST')),
 notification_code varchar(30) NULL CHECK(notification_code IN ('ACCEPTED','DELIVERED')),
 reason_code varchar(40) NULL CHECK(reason_code='REGISTRY_DECLARED_AUTONOMOUS'),
 guidance_code varchar(40) NULL CHECK(guidance_code='CONTACT_RECIPIENT'),
 transcript_key_id varchar(80) NOT NULL, transcript_digest bytea NOT NULL CHECK(octet_length(transcript_digest)=32), occurred_at timestamptz NOT NULL,
 FOREIGN KEY(target_id,job_id) REFERENCES privacy_protected.provider_targets(id,job_id) ON DELETE RESTRICT,
 FOREIGN KEY(attempt_id,job_id) REFERENCES public.privacy_erasure_job_attempts(id,job_id) ON DELETE RESTRICT,
 CHECK(transcript_key_id=btrim(transcript_key_id) AND transcript_key_id~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'),
 CHECK((outcome_code IN ('REMOTE_DELETION_VERIFIED','ALREADY_DISCONNECTED') AND recipient_role IS NULL AND channel_code IS NULL AND notification_code IS NULL AND reason_code IS NULL AND guidance_code IS NULL)
    OR (outcome_code='NOT_CONTROLLABLE' AND evidence_code='RECIPIENT_NOTIFICATION_CONFIRMED' AND recipient_role IS NOT NULL AND channel_code IS NOT NULL AND notification_code IS NOT NULL AND reason_code IS NOT NULL AND guidance_code IS NOT NULL)),
 CHECK((outcome_code='REMOTE_DELETION_VERIFIED')=(evidence_code='REMOTE_DELETION_RECEIPT') OR outcome_code NOT IN ('REMOTE_DELETION_VERIFIED','ALREADY_DISCONNECTED')),
 CHECK((outcome_code='ALREADY_DISCONNECTED')=(evidence_code='REMOTE_ALREADY_DISCONNECTED') OR outcome_code NOT IN ('REMOTE_DELETION_VERIFIED','ALREADY_DISCONNECTED'))
);

CREATE TRIGGER privacy_provider_capture_sets_immutable BEFORE UPDATE OR DELETE ON privacy_protected.provider_capture_sets FOR EACH ROW EXECUTE FUNCTION public.prevent_privacy_execution_record_delete();
CREATE TRIGGER privacy_provider_targets_immutable BEFORE UPDATE OR DELETE ON privacy_protected.provider_targets FOR EACH ROW EXECUTE FUNCTION public.prevent_privacy_execution_record_delete();
CREATE TRIGGER privacy_provider_target_digests_immutable BEFORE UPDATE OR DELETE ON privacy_protected.provider_target_digests FOR EACH ROW EXECUTE FUNCTION public.prevent_privacy_execution_record_delete();
CREATE TRIGGER privacy_provider_evidence_immutable BEFORE UPDATE OR DELETE ON privacy_protected.provider_evidence FOR EACH ROW EXECUTE FUNCTION public.prevent_privacy_execution_record_delete();

CREATE FUNCTION public.privacy_execution_capture_provider_connections(p_execution_id uuid,p_subject_user_id uuid,p_category_key text)
RETURNS TABLE(connection_id uuid,job_id uuid,checkpoint_id uuid,plan_entry_sha256 bytea,category_key text,service_code text,
 provider_role text,provider_contract_version text,target_version bigint,local_state text,registry_evidence_key_id text,registry_evidence_digest bytea,
 target_source_key_id text,target_opaque bytea,credential_source_key_id text,credential_opaque bytea)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE v_job uuid;v_checkpoint uuid;v_entry bytea;v_expected integer;
BEGIN
 SELECT job.id,checkpoint.id,job.entry_sha256 INTO v_job,v_checkpoint,v_entry
 FROM privacy_erasure_executions execution JOIN data_erasure_requests request ON request.id=execution.request_id
 JOIN privacy_erasure_category_jobs job ON job.execution_id=execution.id AND job.category_key=p_category_key
 JOIN privacy_erasure_job_checkpoints checkpoint ON checkpoint.job_id=job.id AND checkpoint.operation_code='PROVIDER_RECIPIENT_NOTIFY' AND checkpoint.action_version='v1'
 WHERE execution.id=p_execution_id AND execution.status='RUNNING' AND execution.request_version_at_start=request.version
 AND execution.executor_version='privacy-erasure-executor/v2' AND execution.schema_version='privacy-erasure-plan/v2'
 AND request.subject_user_id=p_subject_user_id AND request.status IN ('AWAITING_EXECUTION','PARTIALLY_APPROVED') FOR SHARE OF execution,request,job,checkpoint;
 IF v_checkpoint IS NULL OR EXISTS(SELECT 1 FROM privacy_protected.provider_capture_sets WHERE execution_id=p_execution_id) THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_provider_capture_invalid'; END IF;
 IF EXISTS(SELECT 1 FROM privacy_protected.provider_connections WHERE subject_user_id=p_subject_user_id AND state='QUARANTINED') THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_provider_quarantine_unresolved'; END IF;
 SELECT count(*)::integer INTO v_expected FROM privacy_protected.provider_connections WHERE subject_user_id=p_subject_user_id AND state IN ('ACTIVE','DISCONNECTED');
 INSERT INTO privacy_protected.provider_capture_sets(execution_id,job_id,checkpoint_id,subject_user_id,category_key,expected_target_count,operation_code,action_version,created_at)
 VALUES(p_execution_id,v_job,v_checkpoint,p_subject_user_id,p_category_key,v_expected,'PROVIDER_RECIPIENT_NOTIFY','v1',clock_timestamp());
 RETURN QUERY WITH selected AS MATERIALIZED (
  SELECT c.*,c.state AS prior_state,c.credential_opaque AS prior_credential FROM privacy_protected.provider_connections c
  WHERE c.subject_user_id=p_subject_user_id AND c.state IN ('ACTIVE','DISCONNECTED') ORDER BY c.service_code,c.id FOR UPDATE
 ), fenced AS (
 UPDATE privacy_protected.provider_connections c SET state=CASE WHEN selected.prior_state='ACTIVE' THEN 'QUARANTINED' ELSE 'DISCONNECTED' END,
   state_version=c.state_version+1,sync_enabled=false,webhook_enabled=false,reconnect_enabled=false,
   credential_key_id=NULL,credential_opaque=NULL,updated_at=clock_timestamp()
  FROM selected WHERE c.id=selected.id
  RETURNING c.id,c.service_code,c.provider_role,c.provider_contract_version,c.state_version,c.registry_evidence_key_id,c.registry_evidence_digest,
   c.target_key_id,c.target_opaque
 )
 SELECT fenced.id,v_job,v_checkpoint,v_entry,p_category_key::text,fenced.service_code::text,fenced.provider_role::text,
  fenced.provider_contract_version::text,fenced.state_version,selected.prior_state::text,fenced.registry_evidence_key_id::text,fenced.registry_evidence_digest,
  fenced.target_key_id::text,fenced.target_opaque,selected.credential_key_id::text,selected.prior_credential
 FROM fenced JOIN selected ON selected.id=fenced.id ORDER BY fenced.service_code,fenced.id;
END;$$;

CREATE FUNCTION public.privacy_execution_materialize_provider_target(
 p_target_id uuid,p_connection_id uuid,p_execution_id uuid,p_job_id uuid,p_checkpoint_id uuid,p_plan_entry_sha256 bytea,p_category_key text,
 p_service_code text,p_provider_role text,p_provider_contract_version text,p_target_version bigint,p_local_state text,
 p_registry_evidence_key_id text,p_registry_evidence_digest bytea,p_target_envelope_version text,p_target_algorithm text,
 p_target_encryption_key_id text,p_target_encapsulation bytea,p_target_nonce bytea,p_target_ciphertext bytea,
 p_credential_envelope_version text,p_credential_algorithm text,p_credential_encryption_key_id text,p_credential_encapsulation bytea,
 p_credential_nonce bytea,p_credential_ciphertext bytea,p_credential_commitment_key_id text,p_credential_source_commitment bytea,
 p_digest_key_id text,p_target_digest bytea
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE c privacy_protected.provider_capture_sets%ROWTYPE;s privacy_protected.provider_connections%ROWTYPE;
BEGIN
 SELECT * INTO c FROM privacy_protected.provider_capture_sets WHERE checkpoint_id=p_checkpoint_id AND execution_id=p_execution_id FOR SHARE;
 SELECT * INTO s FROM privacy_protected.provider_connections WHERE id=p_connection_id FOR SHARE;
 IF c.checkpoint_id IS NULL OR c.job_id<>p_job_id OR c.category_key<>p_category_key OR c.expected_target_count<=(SELECT count(*) FROM privacy_protected.provider_targets WHERE checkpoint_id=p_checkpoint_id)
  OR s.id IS NULL OR s.subject_user_id<>c.subject_user_id OR s.service_code<>p_service_code OR s.provider_role<>p_provider_role
  OR s.provider_contract_version<>p_provider_contract_version OR s.state_version<>p_target_version
  OR s.registry_evidence_key_id<>p_registry_evidence_key_id OR s.registry_evidence_digest<>p_registry_evidence_digest
  OR p_local_state NOT IN ('ACTIVE','DISCONNECTED') OR (p_local_state='ACTIVE' AND s.state<>'QUARANTINED')
  OR (p_local_state='DISCONNECTED' AND (s.state<>'DISCONNECTED' OR p_credential_ciphertext IS NOT NULL)) THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_provider_target_invalid'; END IF;
 INSERT INTO privacy_protected.provider_targets(id,connection_id,execution_id,job_id,checkpoint_id,plan_entry_sha256,category_key,service_code,target_kind,
  provider_role,target_version,operation_code,action_version,provider_contract_version,registry_evidence_key_id,registry_evidence_digest,local_state,
  target_envelope_version,target_algorithm,target_encryption_key_id,target_encapsulation,target_nonce,target_ciphertext,created_at)
 VALUES(p_target_id,p_connection_id,p_execution_id,p_job_id,p_checkpoint_id,p_plan_entry_sha256,p_category_key,p_service_code,'REMOTE_ACCOUNT',p_provider_role,
  p_target_version,'PROVIDER_RECIPIENT_NOTIFY','v1',p_provider_contract_version,p_registry_evidence_key_id,p_registry_evidence_digest,p_local_state,
  p_target_envelope_version,p_target_algorithm,p_target_encryption_key_id,p_target_encapsulation,p_target_nonce,p_target_ciphertext,clock_timestamp());
 INSERT INTO privacy_protected.provider_target_digests VALUES(p_target_id,p_execution_id,p_service_code,'REMOTE_ACCOUNT',p_digest_key_id,p_target_digest,clock_timestamp());
 IF p_local_state='ACTIVE' THEN
  IF p_credential_envelope_version IS NULL OR p_credential_algorithm IS NULL OR p_credential_encryption_key_id IS NULL OR p_credential_encapsulation IS NULL OR p_credential_nonce IS NULL OR p_credential_ciphertext IS NULL
   OR p_credential_commitment_key_id IS NULL OR p_credential_source_commitment IS NULL THEN
   RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_provider_quarantine_invalid'; END IF;
  INSERT INTO privacy_protected.provider_credential_quarantine VALUES(p_target_id,p_credential_commitment_key_id,p_credential_source_commitment,p_credential_envelope_version,p_credential_algorithm,
   p_credential_encryption_key_id,p_credential_encapsulation,p_credential_nonce,p_credential_ciphertext,clock_timestamp());
 END IF;
 RETURN p_target_id;
END;$$;

CREATE FUNCTION public.privacy_execution_complete_provider_capture(p_execution_id uuid,p_category_key text) RETURNS integer
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE expected integer;actual integer;checkpoint_ref uuid;
BEGIN
 SELECT checkpoint_id,expected_target_count INTO checkpoint_ref,expected FROM privacy_protected.provider_capture_sets WHERE execution_id=p_execution_id AND category_key=p_category_key FOR SHARE;
 SELECT count(*)::integer INTO actual FROM privacy_protected.provider_targets WHERE execution_id=p_execution_id AND checkpoint_id=checkpoint_ref;
 IF expected IS NULL OR actual<>expected THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_provider_capture_incomplete'; END IF;
 RETURN actual;
END;$$;

CREATE FUNCTION public.privacy_worker_list_provider_targets(p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_epoch bigint,p_worker_ref uuid)
RETURNS TABLE(target_id uuid,execution_id uuid,job_id uuid,checkpoint_id uuid,plan_entry_sha256 bytea,category_key text,service_code text,target_kind text,
 provider_role text,target_version bigint,operation_code text,action_version text,provider_contract_version text,registry_evidence_key_id text,
 registry_evidence_digest bytea,local_state text,
 target_envelope_version text,target_algorithm text,target_encryption_key_id text,target_encapsulation bytea,target_nonce bytea,target_ciphertext bytea,
 credential_envelope_version text,credential_algorithm text,credential_encryption_key_id text,credential_encapsulation bytea,credential_nonce bytea,
 credential_ciphertext bytea,credential_commitment_key_id text,credential_source_commitment bytea)
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT t.id,t.execution_id,t.job_id,t.checkpoint_id,t.plan_entry_sha256,t.category_key::text,t.service_code::text,t.target_kind::text,t.provider_role::text,
  t.target_version,t.operation_code::text,t.action_version::text,t.provider_contract_version::text,t.registry_evidence_key_id::text,t.registry_evidence_digest,t.local_state::text,
  t.target_envelope_version::text,t.target_algorithm::text,t.target_encryption_key_id::text,t.target_encapsulation,t.target_nonce,t.target_ciphertext,
  q.envelope_version::text,q.algorithm::text,q.encryption_key_id::text,q.encapsulation,q.nonce,q.ciphertext,q.source_commitment_key_id::text,q.source_commitment
 FROM privacy_protected.provider_targets t JOIN privacy_erasure_category_jobs j ON j.id=t.job_id
 JOIN privacy_erasure_job_leases l ON l.id=p_lease_id AND l.job_id=j.id AND l.epoch=p_epoch
 JOIN privacy_erasure_job_attempts a ON a.id=p_attempt_id AND a.job_id=j.id AND a.lease_id=l.id AND a.lease_epoch=p_epoch
 LEFT JOIN privacy_protected.provider_credential_quarantine q ON q.target_id=t.id LEFT JOIN privacy_protected.provider_evidence e ON e.target_id=t.id
 WHERE j.id=p_job_id AND j.status='LEASED' AND l.worker_ref=p_worker_ref AND l.released_at IS NULL AND l.expires_at>clock_timestamp()
  AND a.finished_at IS NULL AND e.target_id IS NULL AND ((t.local_state='ACTIVE' AND q.target_id IS NOT NULL) OR (t.local_state='DISCONNECTED' AND q.target_id IS NULL)) ORDER BY t.id;
$$;

CREATE FUNCTION public.privacy_worker_record_provider_evidence(p_target_id uuid,p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_epoch bigint,p_worker_ref uuid,
 p_outcome_code text,p_adapter_attempts integer,p_evidence_code text,p_recipient_role text,p_channel_code text,p_notification_code text,p_reason_code text,p_guidance_code text,
 p_transcript_key_id text,p_transcript_digest bytea) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE target privacy_protected.provider_targets%ROWTYPE;
BEGIN
 SELECT t.* INTO target FROM privacy_protected.provider_targets t JOIN privacy_erasure_category_jobs j ON j.id=t.job_id
 JOIN privacy_erasure_job_leases l ON l.id=p_lease_id AND l.job_id=j.id AND l.epoch=p_epoch
 JOIN privacy_erasure_job_attempts a ON a.id=p_attempt_id AND a.job_id=j.id AND a.lease_id=l.id AND a.lease_epoch=p_epoch
 WHERE t.id=p_target_id AND j.id=p_job_id AND j.status='LEASED' AND l.worker_ref=p_worker_ref AND l.released_at IS NULL
 AND l.expires_at>clock_timestamp() AND a.finished_at IS NULL FOR UPDATE OF j,l,a;
 IF target.id IS NULL OR p_adapter_attempts NOT BETWEEN 1 AND 5 OR p_transcript_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$' OR octet_length(p_transcript_digest)<>32
  OR p_outcome_code NOT IN ('REMOTE_DELETION_VERIFIED','ALREADY_DISCONNECTED','NOT_CONTROLLABLE')
  OR (p_outcome_code='NOT_CONTROLLABLE' AND target.provider_role<>'AUTONOMOUS_RECIPIENT')
  OR (p_outcome_code='REMOTE_DELETION_VERIFIED' AND p_evidence_code<>'REMOTE_DELETION_RECEIPT')
  OR (p_outcome_code='ALREADY_DISCONNECTED' AND p_evidence_code<>'REMOTE_ALREADY_DISCONNECTED')
  OR (p_outcome_code IN ('REMOTE_DELETION_VERIFIED','ALREADY_DISCONNECTED') AND (p_recipient_role IS NOT NULL OR p_channel_code IS NOT NULL OR p_notification_code IS NOT NULL OR p_reason_code IS NOT NULL OR p_guidance_code IS NOT NULL))
  OR (p_outcome_code='NOT_CONTROLLABLE' AND (p_evidence_code<>'RECIPIENT_NOTIFICATION_CONFIRMED' OR p_recipient_role<>'AUTONOMOUS_RECIPIENT'
   OR p_channel_code NOT IN ('API','EMAIL','PORTAL','REGISTERED_POST') OR p_notification_code NOT IN ('ACCEPTED','DELIVERED')
   OR p_reason_code<>'REGISTRY_DECLARED_AUTONOMOUS' OR p_guidance_code<>'CONTACT_RECIPIENT')) THEN RETURN NULL; END IF;
 INSERT INTO privacy_protected.provider_evidence(target_id,job_id,attempt_id,evidence_version,outcome_code,adapter_attempts,evidence_code,recipient_role,channel_code,notification_code,reason_code,guidance_code,transcript_key_id,transcript_digest,occurred_at)
 VALUES(p_target_id,p_job_id,p_attempt_id,'provider-structured-evidence/v1',p_outcome_code,p_adapter_attempts,p_evidence_code,p_recipient_role,p_channel_code,p_notification_code,p_reason_code,p_guidance_code,p_transcript_key_id,p_transcript_digest,clock_timestamp())
 ON CONFLICT(target_id) DO NOTHING;
 DELETE FROM privacy_protected.provider_credential_quarantine WHERE target_id=p_target_id;
 DELETE FROM privacy_protected.provider_connections WHERE id=target.connection_id;
 RETURN p_target_id;
END;$$;

CREATE FUNCTION public.privacy_worker_complete_provider_checkpoint(p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_epoch bigint,p_worker_ref uuid)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE checkpoint_ref uuid;expected integer;evidenced integer;
BEGIN
 SELECT checkpoint.id,c.expected_target_count INTO checkpoint_ref,expected FROM privacy_erasure_category_jobs j
 JOIN privacy_erasure_job_leases l ON l.id=p_lease_id AND l.job_id=j.id AND l.epoch=p_epoch
 JOIN privacy_erasure_job_attempts a ON a.id=p_attempt_id AND a.job_id=j.id AND a.lease_id=l.id AND a.lease_epoch=p_epoch
 JOIN privacy_erasure_job_checkpoints checkpoint ON checkpoint.job_id=j.id AND checkpoint.operation_code='PROVIDER_RECIPIENT_NOTIFY' AND checkpoint.action_version='v1'
 JOIN privacy_protected.provider_capture_sets c ON c.checkpoint_id=checkpoint.id WHERE j.id=p_job_id AND j.status='LEASED' AND l.worker_ref=p_worker_ref
 AND l.released_at IS NULL AND l.expires_at>clock_timestamp() AND a.finished_at IS NULL FOR UPDATE OF j,l,a,checkpoint;
 IF checkpoint_ref IS NULL THEN RETURN NULL; END IF;
 IF (SELECT status FROM privacy_erasure_job_checkpoints WHERE id=checkpoint_ref)='SUCCEEDED' THEN RETURN checkpoint_ref; END IF;
 SELECT count(*)::integer INTO evidenced FROM privacy_protected.provider_targets t JOIN privacy_protected.provider_evidence e ON e.target_id=t.id WHERE t.checkpoint_id=checkpoint_ref;
 IF evidenced<>expected OR EXISTS(SELECT 1 FROM privacy_protected.provider_credential_quarantine q JOIN privacy_protected.provider_targets t ON t.id=q.target_id WHERE t.checkpoint_id=checkpoint_ref)
 OR EXISTS(SELECT 1 FROM privacy_erasure_job_checkpoints prior WHERE prior.job_id=p_job_id AND prior.operation_position<(SELECT operation_position FROM privacy_erasure_job_checkpoints WHERE id=checkpoint_ref) AND prior.status<>'SUCCEEDED')
 OR EXISTS(SELECT 1 FROM privacy_erasure_category_jobs prior WHERE prior.execution_id=(SELECT execution_id FROM privacy_erasure_category_jobs WHERE id=p_job_id)
  AND prior.plan_entry_position<(SELECT plan_entry_position FROM privacy_erasure_category_jobs WHERE id=p_job_id) AND prior.status<>'SUCCEEDED') THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_provider_evidence_incomplete'; END IF;
 UPDATE privacy_erasure_job_checkpoints SET status='SUCCEEDED',completed_at=clock_timestamp(),completed_by_attempt_id=p_attempt_id,affected_rows=evidenced,
  result_sha256=digest(convert_to('PROVIDER_RECIPIENT_NOTIFY:'||evidenced::text,'UTF8'),'sha256') WHERE id=checkpoint_ref AND status='PENDING';
 RETURN checkpoint_ref;
END;$$;

REVOKE ALL ON TABLE privacy_protected.provider_connections,privacy_protected.provider_capture_sets,privacy_protected.provider_targets,
 privacy_protected.provider_target_digests,privacy_protected.provider_credential_quarantine,privacy_protected.provider_evidence FROM PUBLIC;
REVOKE ALL ON FUNCTION public.privacy_execution_capture_provider_connections(uuid,uuid,text),
 public.privacy_execution_materialize_provider_target(uuid,uuid,uuid,uuid,uuid,bytea,text,text,text,text,bigint,text,text,bytea,text,text,text,bytea,bytea,bytea,text,text,text,bytea,bytea,bytea,text,bytea,text,bytea),
 public.privacy_execution_complete_provider_capture(uuid,text),public.privacy_worker_list_provider_targets(uuid,uuid,uuid,bigint,uuid),
 public.privacy_worker_record_provider_evidence(uuid,uuid,uuid,uuid,bigint,uuid,text,integer,text,text,text,text,text,text,text,bytea),
 public.privacy_worker_complete_provider_checkpoint(uuid,uuid,uuid,bigint,uuid) FROM PUBLIC;
