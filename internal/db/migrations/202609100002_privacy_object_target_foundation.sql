-- Add the deny-by-default persistence and crypto contract for future protected
-- object-target execution. This is deliberately inert: v1 plans remain
-- unsupported for OBJECT_VERSION_DELETE and no worker routine is granted.
CREATE SCHEMA IF NOT EXISTS privacy_protected;
REVOKE ALL ON SCHEMA privacy_protected FROM PUBLIC;

ALTER TABLE privacy_erasure_category_jobs
 ADD CONSTRAINT privacy_erasure_category_jobs_target_binding_unique UNIQUE(id,execution_id,entry_sha256,category_key);
ALTER TABLE privacy_erasure_job_checkpoints
 ADD CONSTRAINT privacy_erasure_job_checkpoints_target_binding_unique UNIQUE(id,job_id,operation_code,action_version);

CREATE TABLE privacy_protected.object_targets (
 id uuid PRIMARY KEY, execution_id uuid NOT NULL, job_id uuid NOT NULL, checkpoint_id uuid NOT NULL,
 plan_entry_sha256 bytea NOT NULL CHECK (octet_length(plan_entry_sha256)=32),
 category_key varchar(120) NOT NULL, service_code varchar(120) NOT NULL,
 target_kind varchar(40) NOT NULL CHECK (target_kind='OBJECT_KEY'),
 source_kind varchar(40) NOT NULL CHECK (source_kind IN ('MEMBER_PROFILE_PHOTO','REPAIR_ATTACHMENT','EQUIPMENT_PHOTO')),
 source_ref uuid NOT NULL,
 operation_code varchar(120) NOT NULL CHECK (operation_code='OBJECT_VERSION_DELETE'),
 action_version varchar(40) NOT NULL CHECK (action_version='v1'),
 provider_contract_version varchar(40) NOT NULL CHECK (provider_contract_version='s3-versioned/v1'),
 envelope_version varchar(80) NOT NULL CHECK (envelope_version='x25519-aes256gcm-hkdfsha256/v1'),
 algorithm varchar(80) NOT NULL CHECK (algorithm='X25519-HKDF-SHA256-AES-256-GCM'),
 encryption_key_id varchar(80) NOT NULL, encapsulation bytea NOT NULL CHECK (octet_length(encapsulation)=32),
 nonce bytea NOT NULL CHECK (octet_length(nonce)=12), ciphertext bytea NOT NULL CHECK (octet_length(ciphertext) BETWEEN 17 AND 2048),
 created_at timestamptz NOT NULL,
 CHECK (category_key=btrim(category_key) AND category_key ~ '^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,119}$'),
 CHECK (service_code=btrim(service_code) AND service_code ~ '^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,119}$'),
 CHECK (encryption_key_id=btrim(encryption_key_id) AND encryption_key_id ~ '^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'),
 FOREIGN KEY(job_id,execution_id,plan_entry_sha256,category_key) REFERENCES privacy_erasure_category_jobs(id,execution_id,entry_sha256,category_key) ON DELETE RESTRICT,
 FOREIGN KEY(checkpoint_id,job_id,operation_code,action_version) REFERENCES privacy_erasure_job_checkpoints(id,job_id,operation_code,action_version) ON DELETE RESTRICT,
 UNIQUE(id,job_id), UNIQUE(id,execution_id,service_code,target_kind), UNIQUE(encryption_key_id,encapsulation)
);
CREATE INDEX privacy_object_targets_checkpoint_idx ON privacy_protected.object_targets(checkpoint_id,id);

CREATE TABLE privacy_protected.object_target_digests (
 target_id uuid NOT NULL, execution_id uuid NOT NULL REFERENCES privacy_erasure_executions(id) ON DELETE RESTRICT,
 service_code varchar(120) NOT NULL, target_kind varchar(40) NOT NULL CHECK (target_kind='OBJECT_KEY'),
 digest_key_id varchar(80) NOT NULL, locator_digest bytea NOT NULL CHECK (octet_length(locator_digest)=32), created_at timestamptz NOT NULL,
 PRIMARY KEY(target_id,digest_key_id),
 FOREIGN KEY(target_id,execution_id,service_code,target_kind) REFERENCES privacy_protected.object_targets(id,execution_id,service_code,target_kind) ON DELETE RESTRICT,
 CHECK (service_code=btrim(service_code) AND service_code ~ '^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,119}$'),
 CHECK (digest_key_id=btrim(digest_key_id) AND digest_key_id ~ '^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'),
 UNIQUE(execution_id,service_code,target_kind,digest_key_id,locator_digest)
);

CREATE TABLE privacy_protected.object_evidence (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), target_id uuid NOT NULL, job_id uuid NOT NULL,
 attempt_id uuid NOT NULL, evidence_version varchar(40) NOT NULL CHECK (evidence_version='s3-absence/v1'),
 outcome_code varchar(40) NOT NULL CHECK (outcome_code='ABSENCE_VERIFIED'),
 deleted_version_count integer NOT NULL CHECK (deleted_version_count>=0),
 deleted_marker_count integer NOT NULL CHECK (deleted_marker_count>=0),
 list_call_count integer NOT NULL CHECK (list_call_count>=2),
 stable_empty_check_count integer NOT NULL CHECK (stable_empty_check_count>=2),
 transcript_key_id varchar(80) NOT NULL, transcript_digest bytea NOT NULL CHECK (octet_length(transcript_digest)=32),
 occurred_at timestamptz NOT NULL,
 FOREIGN KEY(target_id,job_id) REFERENCES privacy_protected.object_targets(id,job_id) ON DELETE RESTRICT,
 FOREIGN KEY(attempt_id,job_id) REFERENCES privacy_erasure_job_attempts(id,job_id) ON DELETE RESTRICT,
 CHECK (transcript_key_id=btrim(transcript_key_id) AND transcript_key_id ~ '^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'),
 UNIQUE(target_id)
);

CREATE TRIGGER privacy_object_targets_immutable BEFORE UPDATE OR DELETE ON privacy_protected.object_targets FOR EACH ROW EXECUTE FUNCTION prevent_privacy_execution_record_delete();
CREATE TRIGGER privacy_object_target_digests_immutable BEFORE UPDATE OR DELETE ON privacy_protected.object_target_digests FOR EACH ROW EXECUTE FUNCTION prevent_privacy_execution_record_delete();
CREATE TRIGGER privacy_object_evidence_immutable BEFORE UPDATE OR DELETE ON privacy_protected.object_evidence FOR EACH ROW EXECUTE FUNCTION prevent_privacy_execution_record_delete();

REVOKE ALL ON ALL TABLES IN SCHEMA privacy_protected FROM PUBLIC;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA privacy_protected FROM PUBLIC;
