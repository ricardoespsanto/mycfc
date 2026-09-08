-- Additive #244 durable erasure execution handoff and lease/fencing lifecycle.
-- No existing approved request is started or backfilled by this migration.

ALTER TABLE sessions ADD COLUMN user_id uuid NULL;
ALTER TABLE sessions ADD CONSTRAINT sessions_user_id_fkey FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE RESTRICT NOT VALID;
ALTER TABLE sessions VALIDATE CONSTRAINT sessions_user_id_fkey;
ALTER TABLE sessions ADD COLUMN subject_indexed boolean NOT NULL DEFAULT false;
ALTER TABLE sessions ADD CONSTRAINT sessions_subject_index_complete CHECK(user_id IS NULL OR subject_indexed) NOT VALID;
ALTER TABLE sessions VALIDATE CONSTRAINT sessions_subject_index_complete;
CREATE INDEX sessions_user_expiry_idx ON sessions(user_id,expiry) WHERE subject_indexed AND user_id IS NOT NULL;
CREATE INDEX sessions_unindexed_expiry_idx ON sessions(expiry) WHERE NOT subject_indexed;

-- Every path that can reduce or mutate the active administrator set shares this
-- transaction lock. The trigger prevents direct SQL and older application paths
-- from racing privacy execution or removing the last active administrator.
CREATE FUNCTION guard_active_administrator_set() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE old_admin boolean := false; new_admin boolean := false; active_admins bigint;
BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended('mycfc-active-admin-set/v1',0));
 IF TG_TABLE_NAME='user_platform_roles' THEN
  IF TG_OP<>'INSERT' THEN old_admin:=EXISTS(SELECT 1 FROM platform_roles WHERE id=OLD.role_id AND code='ADMIN') AND EXISTS(SELECT 1 FROM users WHERE id=OLD.user_id AND is_active AND NOT is_dependent AND date_of_birth<=(((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-INTERVAL '18 years')::date) AND email IS NOT NULL AND password_hash IS NOT NULL); END IF;
  IF TG_OP<>'DELETE' THEN new_admin:=EXISTS(SELECT 1 FROM platform_roles WHERE id=NEW.role_id AND code='ADMIN') AND EXISTS(SELECT 1 FROM users WHERE id=NEW.user_id AND is_active AND NOT is_dependent AND date_of_birth<=(((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-INTERVAL '18 years')::date) AND email IS NOT NULL AND password_hash IS NOT NULL); END IF;
  IF old_admin AND NOT new_admin THEN
   SELECT count(*) INTO active_admins FROM user_platform_roles assignment JOIN platform_roles role ON role.id=assignment.role_id JOIN users account ON account.id=assignment.user_id WHERE role.code='ADMIN' AND account.is_active AND NOT account.is_dependent AND account.date_of_birth<=(((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-INTERVAL '18 years')::date) AND account.email IS NOT NULL AND account.password_hash IS NOT NULL;
   IF active_admins<=1 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='last_active_administrator'; END IF;
  END IF;
  IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
 END IF;
 old_admin:=OLD.is_active AND NOT OLD.is_dependent AND OLD.date_of_birth<=(((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-INTERVAL '18 years')::date) AND OLD.email IS NOT NULL AND OLD.password_hash IS NOT NULL AND EXISTS(SELECT 1 FROM user_platform_roles assignment JOIN platform_roles role ON role.id=assignment.role_id WHERE assignment.user_id=OLD.id AND role.code='ADMIN');
 IF TG_OP<>'DELETE' THEN new_admin:=NEW.is_active AND NOT NEW.is_dependent AND NEW.date_of_birth<=(((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-INTERVAL '18 years')::date) AND NEW.email IS NOT NULL AND NEW.password_hash IS NOT NULL AND EXISTS(SELECT 1 FROM user_platform_roles assignment JOIN platform_roles role ON role.id=assignment.role_id WHERE assignment.user_id=NEW.id AND role.code='ADMIN'); END IF;
 IF old_admin AND NOT new_admin THEN
  SELECT count(*) INTO active_admins FROM user_platform_roles assignment JOIN platform_roles role ON role.id=assignment.role_id JOIN users account ON account.id=assignment.user_id WHERE role.code='ADMIN' AND account.is_active AND NOT account.is_dependent AND account.date_of_birth<=(((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-INTERVAL '18 years')::date) AND account.email IS NOT NULL AND account.password_hash IS NOT NULL;
  IF active_admins<=1 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='last_active_administrator'; END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END; $$;
CREATE TRIGGER user_platform_roles_guard_active_admin BEFORE INSERT OR UPDATE OR DELETE ON user_platform_roles FOR EACH ROW EXECUTE FUNCTION guard_active_administrator_set();
CREATE TRIGGER users_guard_active_admin BEFORE UPDATE OF is_active,is_dependent,date_of_birth,email,password_hash OR DELETE ON users FOR EACH ROW EXECUTE FUNCTION guard_active_administrator_set();

CREATE FUNCTION require_active_privacy_attachment_subject() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE subject_id uuid;
BEGIN
 IF TG_TABLE_NAME='users' THEN subject_id:=NULLIF(to_jsonb(NEW)->>'guardian_id','')::uuid;
 ELSE subject_id:=NULLIF(to_jsonb(NEW)->>'user_id','')::uuid;
 END IF;
 IF subject_id IS NULL THEN RETURN NEW; END IF;
 PERFORM 1 FROM users WHERE id=subject_id AND is_active FOR KEY SHARE;
 IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='inactive_privacy_attachment_subject'; END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER users_active_guardian_attachment BEFORE INSERT OR UPDATE OF guardian_id ON users FOR EACH ROW EXECUTE FUNCTION require_active_privacy_attachment_subject();
CREATE TRIGGER sessions_active_subject_attachment BEFORE INSERT OR UPDATE OF user_id ON sessions FOR EACH ROW EXECUTE FUNCTION require_active_privacy_attachment_subject();
CREATE TRIGGER email_verification_tokens_active_subject_attachment BEFORE INSERT OR UPDATE OF user_id ON email_verification_tokens FOR EACH ROW EXECUTE FUNCTION require_active_privacy_attachment_subject();
CREATE TRIGGER password_reset_tokens_active_subject_attachment BEFORE INSERT OR UPDATE OF user_id ON password_reset_tokens FOR EACH ROW EXECUTE FUNCTION require_active_privacy_attachment_subject();
CREATE TRIGGER user_platform_roles_active_subject_attachment BEFORE INSERT OR UPDATE OF user_id ON user_platform_roles FOR EACH ROW EXECUTE FUNCTION require_active_privacy_attachment_subject();
CREATE TRIGGER staff_grants_active_subject_attachment BEFORE INSERT OR UPDATE OF user_id ON staff_grants FOR EACH ROW EXECUTE FUNCTION require_active_privacy_attachment_subject();

CREATE TABLE privacy_executor_grants (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 granted_by uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT, granted_at timestamptz NOT NULL,
 revoked_by uuid NULL REFERENCES users(id) ON DELETE RESTRICT, revoked_at timestamptz NULL,
 CHECK ((revoked_at IS NULL) = (revoked_by IS NULL)), CHECK (revoked_at IS NULL OR revoked_at >= granted_at)
);
CREATE UNIQUE INDEX privacy_executor_grants_active_uidx ON privacy_executor_grants(user_id) WHERE revoked_at IS NULL;
CREATE TABLE privacy_executor_grant_events (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), grant_id uuid NOT NULL REFERENCES privacy_executor_grants(id) ON DELETE RESTRICT,
 actor_ref uuid NOT NULL, action varchar(20) NOT NULL CHECK (action IN ('GRANTED','REVOKED')), occurred_at timestamptz NOT NULL
);
CREATE TRIGGER privacy_executor_grant_events_immutable BEFORE UPDATE OR DELETE ON privacy_executor_grant_events FOR EACH ROW EXECUTE FUNCTION prevent_privacy_audit_mutation();
CREATE TRIGGER privacy_reviewer_grants_active_subject_attachment BEFORE INSERT OR UPDATE OF user_id ON privacy_reviewer_grants FOR EACH ROW EXECUTE FUNCTION require_active_privacy_attachment_subject();
CREATE TRIGGER privacy_executor_grants_active_subject_attachment BEFORE INSERT OR UPDATE OF user_id ON privacy_executor_grants FOR EACH ROW EXECUTE FUNCTION require_active_privacy_attachment_subject();

ALTER TABLE privacy_request_execution_plans ADD CONSTRAINT privacy_execution_plans_binding_unique UNIQUE(request_id,plan_sha256,executor_version,schema_version);

CREATE TABLE privacy_erasure_executions (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), request_id uuid NOT NULL UNIQUE REFERENCES data_erasure_requests(id) ON DELETE RESTRICT,
 plan_sha256 bytea NOT NULL, executor_version varchar(80) NOT NULL, schema_version varchar(80) NOT NULL,
 request_version_at_start bigint NOT NULL CHECK (request_version_at_start > 1),
 status varchar(30) NOT NULL DEFAULT 'QUEUED' CHECK (status IN ('QUEUED','RUNNING','RETRYABLE_FAILED','TERMINAL_FAILED','SUCCEEDED')),
 version bigint NOT NULL DEFAULT 1 CHECK (version > 0), started_by_ref uuid NOT NULL,
 accepted_at timestamptz NOT NULL, started_at timestamptz NULL, finished_at timestamptz NULL, updated_at timestamptz NOT NULL,
 FOREIGN KEY(request_id,plan_sha256,executor_version,schema_version) REFERENCES privacy_request_execution_plans(request_id,plan_sha256,executor_version,schema_version) ON DELETE RESTRICT,
 CHECK (octet_length(plan_sha256)=32), CHECK (updated_at>=accepted_at),
 CHECK ((status='QUEUED' AND started_at IS NULL AND finished_at IS NULL) OR (status IN ('RUNNING','RETRYABLE_FAILED') AND started_at IS NOT NULL AND finished_at IS NULL) OR (status IN ('TERMINAL_FAILED','SUCCEEDED') AND started_at IS NOT NULL AND finished_at IS NOT NULL)),
 CHECK (finished_at IS NULL OR finished_at>=started_at)
);
CREATE INDEX privacy_erasure_executions_status_idx ON privacy_erasure_executions(status,updated_at,id);

CREATE TABLE privacy_erasure_access_revocations (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), execution_id uuid NOT NULL REFERENCES privacy_erasure_executions(id) ON DELETE RESTRICT,
 grant_kind varchar(30) NOT NULL CHECK (grant_kind IN ('PLATFORM_ROLE','STAFF_GRANT','PRIVACY_REVIEWER','PRIVACY_EXECUTOR')),
 capability_code varchar(120) NOT NULL, revoked_count integer NOT NULL CHECK (revoked_count>0), actor_ref uuid NOT NULL, occurred_at timestamptz NOT NULL,
 CHECK (capability_code=btrim(capability_code) AND capability_code ~ '^[A-Z][A-Z0-9_]{0,119}$'),
 UNIQUE(execution_id,grant_kind,capability_code)
);
CREATE INDEX privacy_erasure_access_revocations_execution_idx ON privacy_erasure_access_revocations(execution_id,grant_kind,capability_code);

CREATE TABLE privacy_erasure_category_jobs (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), execution_id uuid NOT NULL REFERENCES privacy_erasure_executions(id) ON DELETE RESTRICT,
 plan_entry_position smallint NOT NULL CHECK (plan_entry_position BETWEEN 1 AND 50), entry_sha256 bytea NOT NULL CHECK (octet_length(entry_sha256)=32),
 category_key varchar(120) NOT NULL, purpose_code varchar(120) NOT NULL,
 status varchar(30) NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','LEASED','RETRY_WAIT','SUCCEEDED','TERMINAL_FAILED')),
 next_attempt_at timestamptz NOT NULL, lease_epoch bigint NOT NULL DEFAULT 0 CHECK (lease_epoch>=0), attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count>=0),
 created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, completed_at timestamptz NULL,
 CHECK (category_key=btrim(category_key) AND category_key ~ '^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,119}$'),
 CHECK (purpose_code=btrim(purpose_code) AND purpose_code ~ '^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,119}$'),
 CHECK (updated_at>=created_at), CHECK ((status='SUCCEEDED')=(completed_at IS NOT NULL)), CHECK (completed_at IS NULL OR completed_at>=created_at),
 UNIQUE(execution_id,category_key), UNIQUE(execution_id,plan_entry_position)
);
CREATE INDEX privacy_erasure_category_jobs_claim_idx ON privacy_erasure_category_jobs(status,next_attempt_at,created_at,id) WHERE status IN ('PENDING','RETRY_WAIT','LEASED');

CREATE TABLE privacy_erasure_job_leases (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), job_id uuid NOT NULL REFERENCES privacy_erasure_category_jobs(id) ON DELETE RESTRICT,
 epoch bigint NOT NULL CHECK (epoch>0), worker_ref uuid NOT NULL,
 acquired_at timestamptz NOT NULL, heartbeat_at timestamptz NOT NULL, expires_at timestamptz NOT NULL,
 released_at timestamptz NULL, outcome varchar(30) NULL CHECK (outcome IN ('SUCCEEDED','RETRYABLE_FAILED','TERMINAL_FAILED','EXPIRED')),
 CHECK (heartbeat_at>=acquired_at AND expires_at>heartbeat_at),
 CHECK ((released_at IS NULL)=(outcome IS NULL)), CHECK (released_at IS NULL OR released_at>=acquired_at),
 UNIQUE(job_id,epoch), UNIQUE(id,job_id,epoch)
);
CREATE UNIQUE INDEX privacy_erasure_job_leases_active_uidx ON privacy_erasure_job_leases(job_id) WHERE released_at IS NULL;
CREATE INDEX privacy_erasure_job_leases_expiry_idx ON privacy_erasure_job_leases(expires_at,id) WHERE released_at IS NULL;

CREATE TABLE privacy_erasure_job_attempts (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), job_id uuid NOT NULL REFERENCES privacy_erasure_category_jobs(id) ON DELETE RESTRICT,
 lease_id uuid NOT NULL, lease_epoch bigint NOT NULL, attempt_number integer NOT NULL CHECK (attempt_number>0),
 started_at timestamptz NOT NULL, finished_at timestamptz NULL,
 outcome varchar(30) NULL CHECK (outcome IN ('SUCCEEDED','RETRYABLE_FAILED','TERMINAL_FAILED','LEASE_EXPIRED')),
 FOREIGN KEY(lease_id,job_id,lease_epoch) REFERENCES privacy_erasure_job_leases(id,job_id,epoch) ON DELETE RESTRICT,
 CHECK ((finished_at IS NULL)=(outcome IS NULL)), CHECK (finished_at IS NULL OR finished_at>=started_at),
 UNIQUE(job_id,attempt_number), UNIQUE(lease_id), UNIQUE(id,job_id)
);
CREATE INDEX privacy_erasure_job_attempts_job_idx ON privacy_erasure_job_attempts(job_id,attempt_number);

CREATE TABLE privacy_erasure_job_checkpoints (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), job_id uuid NOT NULL REFERENCES privacy_erasure_category_jobs(id) ON DELETE RESTRICT,
 operation_position smallint NOT NULL CHECK (operation_position BETWEEN 1 AND 100), operation_code varchar(120) NOT NULL, action_version varchar(40) NOT NULL,
 status varchar(20) NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','SUCCEEDED')),
 completed_by_attempt_id uuid NULL, created_at timestamptz NOT NULL, completed_at timestamptz NULL,
 FOREIGN KEY(completed_by_attempt_id,job_id) REFERENCES privacy_erasure_job_attempts(id,job_id) ON DELETE RESTRICT,
 CHECK (operation_code=btrim(operation_code) AND operation_code ~ '^[A-Z][A-Z0-9_]{0,119}$'),
 CHECK (action_version=btrim(action_version) AND action_version ~ '^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,39}$'),
 CHECK ((status='PENDING' AND completed_by_attempt_id IS NULL AND completed_at IS NULL) OR (status='SUCCEEDED' AND completed_by_attempt_id IS NOT NULL AND completed_at IS NOT NULL)),
 CHECK (completed_at IS NULL OR completed_at>=created_at),
 UNIQUE(job_id,operation_position), UNIQUE(job_id,operation_code,action_version)
);
CREATE INDEX privacy_erasure_job_checkpoints_pending_idx ON privacy_erasure_job_checkpoints(job_id,operation_position) WHERE status='PENDING';

CREATE TABLE privacy_erasure_failures (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), job_id uuid NOT NULL REFERENCES privacy_erasure_category_jobs(id) ON DELETE RESTRICT,
 attempt_id uuid NOT NULL, classification varchar(20) NOT NULL CHECK (classification IN ('RETRYABLE','TERMINAL')),
 stage_code varchar(80) NOT NULL, failure_code varchar(120) NOT NULL, diagnostic_digest bytea NULL,
 occurred_at timestamptz NOT NULL,
 FOREIGN KEY(attempt_id,job_id) REFERENCES privacy_erasure_job_attempts(id,job_id) ON DELETE RESTRICT,
 CHECK (stage_code=btrim(stage_code) AND stage_code ~ '^[A-Z][A-Z0-9_]{0,79}$'),
 CHECK (failure_code=btrim(failure_code) AND failure_code ~ '^[A-Z][A-Z0-9_]{0,119}$'),
 CHECK (diagnostic_digest IS NULL OR octet_length(diagnostic_digest)=32), UNIQUE(attempt_id)
);
CREATE INDEX privacy_erasure_failures_job_idx ON privacy_erasure_failures(job_id,occurred_at,id);

CREATE FUNCTION prevent_privacy_execution_record_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'privacy execution records cannot be deleted before approved evidence expiry'; END; $$;
CREATE TRIGGER privacy_erasure_executions_no_delete BEFORE DELETE ON privacy_erasure_executions FOR EACH ROW EXECUTE FUNCTION prevent_privacy_execution_record_delete();
CREATE TRIGGER privacy_erasure_access_revocations_immutable BEFORE UPDATE OR DELETE ON privacy_erasure_access_revocations FOR EACH ROW EXECUTE FUNCTION prevent_privacy_execution_record_delete();
CREATE TRIGGER privacy_erasure_category_jobs_no_delete BEFORE DELETE ON privacy_erasure_category_jobs FOR EACH ROW EXECUTE FUNCTION prevent_privacy_execution_record_delete();
CREATE TRIGGER privacy_erasure_job_leases_no_delete BEFORE DELETE ON privacy_erasure_job_leases FOR EACH ROW EXECUTE FUNCTION prevent_privacy_execution_record_delete();
CREATE TRIGGER privacy_erasure_job_attempts_no_delete BEFORE DELETE ON privacy_erasure_job_attempts FOR EACH ROW EXECUTE FUNCTION prevent_privacy_execution_record_delete();
CREATE TRIGGER privacy_erasure_job_checkpoints_no_delete BEFORE DELETE ON privacy_erasure_job_checkpoints FOR EACH ROW EXECUTE FUNCTION prevent_privacy_execution_record_delete();
CREATE TRIGGER privacy_erasure_failures_immutable BEFORE UPDATE OR DELETE ON privacy_erasure_failures FOR EACH ROW EXECUTE FUNCTION prevent_privacy_execution_record_delete();

ALTER TABLE data_erasure_requests DROP CONSTRAINT data_erasure_requests_status_check;
ALTER TABLE data_erasure_requests ADD CONSTRAINT data_erasure_requests_status_check CHECK(status IN ('RECEIVED','UNDER_REVIEW','AWAITING_EXECUTION','PARTIALLY_APPROVED','PROCESSING','RETRYABLE_FAILED','TERMINAL_FAILED','COMPLETED','REFUSED','CANCELLED')) NOT VALID;
ALTER TABLE data_erasure_requests VALIDATE CONSTRAINT data_erasure_requests_status_check;

DO $$ DECLARE constraint_row record; BEGIN
 FOR constraint_row IN SELECT conname FROM pg_constraint WHERE conrelid='data_erasure_requests'::regclass AND contype='c' AND pg_get_constraintdef(oid) LIKE '%evidence_expires_at > closed_at%' LOOP
  EXECUTE format('ALTER TABLE data_erasure_requests DROP CONSTRAINT %I',constraint_row.conname);
 END LOOP;
END $$;
ALTER TABLE data_erasure_requests ADD CONSTRAINT privacy_case_closure_state CHECK((status IN ('REFUSED','CANCELLED','COMPLETED') AND closed_at IS NOT NULL AND evidence_expires_at>closed_at) OR (status NOT IN ('REFUSED','CANCELLED','COMPLETED') AND closed_at IS NULL AND evidence_expires_at IS NULL)) NOT VALID;
ALTER TABLE data_erasure_requests VALIDATE CONSTRAINT privacy_case_closure_state;
ALTER TABLE data_erasure_requests ADD CONSTRAINT privacy_case_execution_decision_complete CHECK(status NOT IN ('PROCESSING','RETRYABLE_FAILED','TERMINAL_FAILED','COMPLETED') OR (decided_at IS NOT NULL AND decided_by IS NOT NULL AND decision_code IN ('APPROVED','PARTIALLY_APPROVED') AND policy_version IS NOT NULL)) NOT VALID;
ALTER TABLE data_erasure_requests VALIDATE CONSTRAINT privacy_case_execution_decision_complete;

CREATE UNIQUE INDEX data_erasure_request_active_v2_uidx ON data_erasure_requests(subject_user_id,requester_user_id) WHERE status NOT IN ('REFUSED','CANCELLED','COMPLETED');
DROP INDEX data_erasure_request_active_uidx;
ALTER INDEX data_erasure_request_active_v2_uidx RENAME TO data_erasure_request_active_uidx;

ALTER TABLE data_erasure_request_events DROP CONSTRAINT data_erasure_request_events_actor_role_check;
ALTER TABLE data_erasure_request_events ADD CONSTRAINT data_erasure_request_events_actor_role_check CHECK(actor_role IN ('REQUESTER','REVIEWER','EXECUTOR','SYSTEM')) NOT VALID;
ALTER TABLE data_erasure_request_events VALIDATE CONSTRAINT data_erasure_request_events_actor_role_check;
ALTER TABLE data_erasure_request_events DROP CONSTRAINT data_erasure_request_events_action_check;
ALTER TABLE data_erasure_request_events ADD CONSTRAINT data_erasure_request_events_action_check CHECK(action IN ('RECEIVED','CLAIMED','IDENTITY_REQUESTED','IDENTITY_VERIFIED','REPRESENTATION_VERIFIED','REPRESENTATION_CONFLICT','DEADLINE_EXTENDED','DEPENDANT_RESOLVED','APPROVED','PARTIALLY_APPROVED','PROCESSING_STARTED','EXECUTION_RETRYABLE_FAILED','EXECUTION_RESUMED','EXECUTION_TERMINAL_FAILED','COMPLETED','REFUSED','CANCELLED')) NOT VALID;
ALTER TABLE data_erasure_request_events VALIDATE CONSTRAINT data_erasure_request_events_action_check;
ALTER TABLE data_erasure_request_events DROP CONSTRAINT data_erasure_request_events_reason_code_check;
ALTER TABLE data_erasure_request_events ADD CONSTRAINT data_erasure_request_events_reason_code_check CHECK(reason_code IN ('REQUEST_RECEIVED','REVIEW_CLAIMED','VERIFICATION_REQUIRED','VERIFICATION_RECORDED','DEPENDANT_RESOLVED','REPRESENTATION_CONFLICT','COMPLEXITY','REQUEST_VOLUME','POLICY_DECISION','EXECUTION_ACCEPTED','EXECUTION_RETRYABLE_FAILURE','EXECUTION_RETRY_STARTED','EXECUTION_TERMINAL_FAILURE','EXECUTION_COMPLETED','REQUESTER_CANCELLED')) NOT VALID;
ALTER TABLE data_erasure_request_events VALIDATE CONSTRAINT data_erasure_request_events_reason_code_check;
ALTER TABLE data_erasure_request_events DROP CONSTRAINT data_erasure_request_events_from_status_check;
ALTER TABLE data_erasure_request_events ADD CONSTRAINT data_erasure_request_events_from_status_check CHECK(from_status IN ('RECEIVED','UNDER_REVIEW','AWAITING_EXECUTION','PARTIALLY_APPROVED','PROCESSING','RETRYABLE_FAILED','TERMINAL_FAILED','COMPLETED','REFUSED','CANCELLED')) NOT VALID;
ALTER TABLE data_erasure_request_events VALIDATE CONSTRAINT data_erasure_request_events_from_status_check;
ALTER TABLE data_erasure_request_events DROP CONSTRAINT data_erasure_request_events_to_status_check;
ALTER TABLE data_erasure_request_events ADD CONSTRAINT data_erasure_request_events_to_status_check CHECK(to_status IN ('RECEIVED','UNDER_REVIEW','AWAITING_EXECUTION','PARTIALLY_APPROVED','PROCESSING','RETRYABLE_FAILED','TERMINAL_FAILED','COMPLETED','REFUSED','CANCELLED')) NOT VALID;
ALTER TABLE data_erasure_request_events VALIDATE CONSTRAINT data_erasure_request_events_to_status_check;

ALTER TABLE email_outbox DROP CONSTRAINT email_outbox_message_valid;
ALTER TABLE email_outbox ADD CONSTRAINT email_outbox_message_valid CHECK(
 (message_type='EMAIL_VERIFICATION' AND verification_token_id IS NOT NULL AND password_reset_token_id IS NULL AND sealed_payload IS NULL AND privacy_request_id IS NULL AND privacy_requester_id IS NULL AND privacy_event_key IS NULL)
 OR (message_type='PASSWORD_RESET' AND verification_token_id IS NULL AND password_reset_token_id IS NOT NULL AND sealed_payload IS NOT NULL AND privacy_request_id IS NULL AND privacy_requester_id IS NULL AND privacy_event_key IS NULL)
 OR (message_type IN ('PRIVACY_ACKNOWLEDGEMENT','PRIVACY_DECISION','PRIVACY_PROCESSING_STARTED') AND verification_token_id IS NULL AND password_reset_token_id IS NULL AND sealed_payload IS NOT NULL AND privacy_request_id IS NOT NULL AND privacy_event_key IS NOT NULL)
) NOT VALID;
ALTER TABLE email_outbox VALIDATE CONSTRAINT email_outbox_message_valid;
