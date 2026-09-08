-- Additive #110 workflow. No grants, policy adoption or activation are seeded.
-- Privacy review is an explicit capability; no administrator or account is seeded.
CREATE TABLE privacy_reviewer_grants (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 granted_by uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT, granted_at timestamptz NOT NULL,
 revoked_by uuid NULL REFERENCES users(id) ON DELETE RESTRICT, revoked_at timestamptz NULL,
 CHECK ((revoked_at IS NULL) = (revoked_by IS NULL)), CHECK (revoked_at IS NULL OR revoked_at >= granted_at)
);
CREATE UNIQUE INDEX privacy_reviewer_grants_active_uidx ON privacy_reviewer_grants(user_id) WHERE revoked_at IS NULL;
CREATE TABLE privacy_reviewer_grant_events (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), grant_id uuid NOT NULL REFERENCES privacy_reviewer_grants(id) ON DELETE RESTRICT,
 actor_ref uuid NOT NULL, action varchar(20) NOT NULL CHECK (action IN ('GRANTED','REVOKED')), occurred_at timestamptz NOT NULL
);
CREATE FUNCTION prevent_privacy_audit_mutation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'privacy audit events are append-only'; END; $$;
CREATE TRIGGER privacy_grant_events_immutable BEFORE UPDATE OR DELETE ON privacy_reviewer_grant_events FOR EACH ROW EXECUTE FUNCTION prevent_privacy_audit_mutation();

-- Immutable adopted versions contain only controller-approved catalogue metadata.
CREATE TABLE privacy_request_policies (
 version varchar(80) PRIMARY KEY CHECK (char_length(btrim(version)) BETWEEN 1 AND 80),
 category_catalogue jsonb NOT NULL CHECK (jsonb_typeof(category_catalogue) = 'array'),
 account_closure_enabled boolean NOT NULL DEFAULT false,
 working_retention_days integer NULL CHECK (working_retention_days BETWEEN 1 AND 36500),
 response_months integer NOT NULL DEFAULT 1 CHECK (response_months BETWEEN 1 AND 12),
 extension_months integer NOT NULL DEFAULT 2 CHECK (extension_months BETWEEN 1 AND 12),
 adopted_at timestamptz NULL, adopted_by uuid NULL REFERENCES users(id) ON DELETE RESTRICT,
 created_at timestamptz NOT NULL DEFAULT now(), CHECK ((adopted_at IS NULL) = (adopted_by IS NULL))
);
CREATE FUNCTION protect_adopted_privacy_policy() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF OLD.adopted_at IS NOT NULL THEN RAISE EXCEPTION 'adopted privacy policy is immutable'; END IF; IF TG_OP = 'DELETE' THEN RETURN OLD; END IF; RETURN NEW; END; $$;
CREATE TRIGGER privacy_policy_immutable BEFORE UPDATE OR DELETE ON privacy_request_policies FOR EACH ROW EXECUTE FUNCTION protect_adopted_privacy_policy();
CREATE TABLE privacy_request_activation (
 singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton), policy_version varchar(80) NOT NULL REFERENCES privacy_request_policies(version) ON DELETE RESTRICT,
 enabled boolean NOT NULL DEFAULT false, fulfilment_ready boolean NOT NULL DEFAULT false,
 updated_by uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT, updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE data_erasure_requests (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), public_ref uuid NOT NULL DEFAULT gen_random_uuid() UNIQUE,
 idempotency_key uuid NOT NULL, subject_user_id uuid NULL REFERENCES users(id) ON DELETE RESTRICT,
 requester_user_id uuid NULL REFERENCES users(id) ON DELETE RESTRICT, subject_kind varchar(20) NOT NULL CHECK (subject_kind IN ('SELF','DEPENDANT')),
 scope_kind varchar(20) NOT NULL CHECK (scope_kind IN ('ACCOUNT_CLOSURE','CATEGORIES')), categories text[] NOT NULL DEFAULT '{}',
 status varchar(30) NOT NULL DEFAULT 'RECEIVED' CHECK (status IN ('RECEIVED','UNDER_REVIEW','AWAITING_EXECUTION','PARTIALLY_APPROVED','REFUSED','CANCELLED')),
 version bigint NOT NULL DEFAULT 1 CHECK (version > 0), received_at timestamptz NOT NULL, due_at timestamptz NOT NULL,
 extended_due_at timestamptz NULL, extension_reason_code varchar(40) NULL CHECK (extension_reason_code IN ('COMPLEXITY','REQUEST_VOLUME')),
 claimed_by uuid NULL REFERENCES users(id) ON DELETE RESTRICT, reviewed_at timestamptz NULL,
 identity_verified_at timestamptz NULL, identity_method varchar(40) NULL CHECK (identity_method IN ('IN_PERSON','EXISTING_CHANNEL','DOCUMENT_CHECK')),
 identity_verified_by uuid NULL REFERENCES users(id) ON DELETE RESTRICT,
 representation_verified_at timestamptz NULL, representation_method varchar(40) NULL CHECK (representation_method IN ('IN_PERSON','DOCUMENT_CHECK')),
 representation_verified_by uuid NULL REFERENCES users(id) ON DELETE RESTRICT,
 representation_guardian_id uuid NULL REFERENCES users(id) ON DELETE RESTRICT,
 representation_conflict boolean NOT NULL DEFAULT false,
 decision_code varchar(40) NULL CHECK (decision_code IN ('APPROVED','PARTIALLY_APPROVED','REFUSED')),
 decision_explanation varchar(2000) NOT NULL DEFAULT '', category_decisions jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(category_decisions) = 'array'),
 decided_by uuid NULL REFERENCES users(id) ON DELETE RESTRICT, decided_at timestamptz NULL,
 policy_version varchar(80) NULL REFERENCES privacy_request_policies(version) ON DELETE RESTRICT, policy_snapshot jsonb NULL CHECK (policy_snapshot IS NULL OR jsonb_typeof(policy_snapshot) = 'object'),
 closed_at timestamptz NULL, cancelled_at timestamptz NULL, evidence_expires_at timestamptz NULL, working_expires_at timestamptz NULL, working_erased_at timestamptz NULL,
 updated_at timestamptz NOT NULL,
 CHECK (public_ref <> id), CHECK (due_at > received_at AND updated_at >= received_at),
 CHECK (extended_due_at IS NULL OR extended_due_at > due_at), CHECK ((extended_due_at IS NULL) = (extension_reason_code IS NULL)),
 CHECK (array_position(categories, NULL) IS NULL AND cardinality(categories) <= 50),
 CHECK (working_erased_at IS NOT NULL OR (scope_kind = 'ACCOUNT_CLOSURE' AND cardinality(categories) = 0) OR (scope_kind = 'CATEGORIES' AND cardinality(categories) > 0)),
 CHECK (closed_at IS NOT NULL OR (subject_user_id IS NOT NULL AND requester_user_id IS NOT NULL)),
 CHECK (subject_kind <> 'SELF' OR subject_user_id IS NOT DISTINCT FROM requester_user_id),
 CHECK (claimed_by IS NULL OR (claimed_by <> subject_user_id AND claimed_by <> requester_user_id)),
 CHECK (decided_by IS NULL OR (decided_by <> subject_user_id AND decided_by <> requester_user_id)),
 CHECK ((identity_verified_at IS NULL AND identity_method IS NULL AND identity_verified_by IS NULL) OR (identity_verified_at IS NOT NULL AND identity_method IS NOT NULL AND identity_verified_by IS NOT NULL)),
 CHECK ((representation_verified_at IS NULL AND representation_method IS NULL AND representation_verified_by IS NULL AND representation_guardian_id IS NULL) OR (representation_verified_at IS NOT NULL AND representation_method IS NOT NULL AND representation_verified_by IS NOT NULL AND representation_guardian_id IS NOT NULL)),
 CHECK ((policy_version IS NULL) = (policy_snapshot IS NULL)),
 CHECK ((status IN ('REFUSED','CANCELLED') AND closed_at IS NOT NULL AND evidence_expires_at > closed_at) OR (status NOT IN ('REFUSED','CANCELLED') AND closed_at IS NULL AND evidence_expires_at IS NULL)),
 CHECK ((status IN ('AWAITING_EXECUTION','PARTIALLY_APPROVED','REFUSED') AND decided_at IS NOT NULL AND decided_by IS NOT NULL AND decision_code IS NOT NULL AND policy_version IS NOT NULL) OR (status NOT IN ('AWAITING_EXECUTION','PARTIALLY_APPROVED','REFUSED')) OR working_erased_at IS NOT NULL)
);
CREATE UNIQUE INDEX data_erasure_request_idempotency_uidx ON data_erasure_requests(requester_user_id,idempotency_key);
CREATE UNIQUE INDEX data_erasure_request_active_uidx ON data_erasure_requests(subject_user_id,requester_user_id) WHERE status NOT IN ('REFUSED','CANCELLED');
CREATE INDEX data_erasure_requests_queue_idx ON data_erasure_requests(status, due_at, received_at);
CREATE INDEX data_erasure_requests_requester_idx ON data_erasure_requests(requester_user_id, received_at DESC);
CREATE TABLE data_erasure_request_events (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), request_id uuid NOT NULL REFERENCES data_erasure_requests(id) ON DELETE RESTRICT,
 actor_role varchar(20) NOT NULL CHECK (actor_role IN ('REQUESTER','REVIEWER','SYSTEM')), actor_ref uuid NOT NULL,
 action varchar(40) NOT NULL CHECK (action IN ('RECEIVED','CLAIMED','IDENTITY_REQUESTED','IDENTITY_VERIFIED','REPRESENTATION_VERIFIED','REPRESENTATION_CONFLICT','DEADLINE_EXTENDED','DEPENDANT_RESOLVED','APPROVED','PARTIALLY_APPROVED','REFUSED','CANCELLED')),
 reason_code varchar(40) NOT NULL CHECK (reason_code IN ('REQUEST_RECEIVED','REVIEW_CLAIMED','VERIFICATION_REQUIRED','VERIFICATION_RECORDED','DEPENDANT_RESOLVED','REPRESENTATION_CONFLICT','COMPLEXITY','REQUEST_VOLUME','POLICY_DECISION','REQUESTER_CANCELLED')),
 from_status varchar(30) NULL CHECK (from_status IN ('RECEIVED','UNDER_REVIEW','AWAITING_EXECUTION','PARTIALLY_APPROVED','REFUSED','CANCELLED')),
 to_status varchar(30) NOT NULL CHECK (to_status IN ('RECEIVED','UNDER_REVIEW','AWAITING_EXECUTION','PARTIALLY_APPROVED','REFUSED','CANCELLED')),
 version bigint NOT NULL CHECK (version > 0), occurred_at timestamptz NOT NULL, UNIQUE(request_id,version)
);
CREATE TRIGGER privacy_case_events_immutable BEFORE UPDATE OR DELETE ON data_erasure_request_events FOR EACH ROW EXECUTE FUNCTION prevent_privacy_audit_mutation();
CREATE INDEX data_erasure_request_events_request_idx ON data_erasure_request_events(request_id,version);

ALTER TABLE email_outbox ADD COLUMN privacy_request_id uuid NULL REFERENCES data_erasure_requests(id) ON DELETE RESTRICT;
ALTER TABLE email_outbox ADD COLUMN privacy_requester_id uuid NULL REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE email_outbox ADD COLUMN privacy_event_key uuid NULL;
ALTER TABLE email_outbox DROP CONSTRAINT email_outbox_message_valid;
ALTER TABLE email_outbox ADD CONSTRAINT email_outbox_message_valid CHECK (
 (message_type = 'EMAIL_VERIFICATION' AND verification_token_id IS NOT NULL AND password_reset_token_id IS NULL AND sealed_payload IS NULL AND privacy_request_id IS NULL AND privacy_requester_id IS NULL AND privacy_event_key IS NULL)
 OR (message_type = 'PASSWORD_RESET' AND verification_token_id IS NULL AND password_reset_token_id IS NOT NULL AND sealed_payload IS NOT NULL AND privacy_request_id IS NULL AND privacy_requester_id IS NULL AND privacy_event_key IS NULL)
 OR (message_type IN ('PRIVACY_ACKNOWLEDGEMENT','PRIVACY_DECISION') AND verification_token_id IS NULL AND password_reset_token_id IS NULL AND sealed_payload IS NOT NULL AND privacy_request_id IS NOT NULL AND privacy_event_key IS NOT NULL)
);
CREATE UNIQUE INDEX email_outbox_privacy_event_uidx ON email_outbox(privacy_event_key) WHERE privacy_event_key IS NOT NULL;

ALTER TABLE data_erasure_requests ADD COLUMN representation_relationship_updated_at timestamptz NULL;
CREATE TABLE privacy_request_dependant_resolutions (
 request_id uuid NOT NULL REFERENCES data_erasure_requests(id) ON DELETE RESTRICT,
 dependant_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 guardian_id_snapshot uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 relationship_updated_at timestamptz NOT NULL,
 resolution_code varchar(40) NOT NULL CHECK (resolution_code IN ('VERIFIED_TRANSFER','SEPARATE_APPROVED_REQUEST','FORMAL_RESOLUTION')),
 related_request_id uuid NULL REFERENCES data_erasure_requests(id) ON DELETE RESTRICT,
 verified_by uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT, verified_at timestamptz NOT NULL,
 explanation varchar(2000) NOT NULL CHECK (char_length(btrim(explanation)) BETWEEN 1 AND 2000),
 PRIMARY KEY(request_id,dependant_id), CHECK (verified_by <> dependant_id AND verified_by <> guardian_id_snapshot),
 CHECK (resolution_code <> 'SEPARATE_APPROVED_REQUEST' OR related_request_id IS NOT NULL)
);

ALTER TABLE data_erasure_requests ADD CONSTRAINT privacy_case_decision_matches_status CHECK (
 (status <> 'AWAITING_EXECUTION' OR decision_code = 'APPROVED') AND
 (status <> 'PARTIALLY_APPROVED' OR decision_code = 'PARTIALLY_APPROVED') AND
 (status <> 'REFUSED' OR decision_code = 'REFUSED')
);
ALTER TABLE data_erasure_requests ADD CONSTRAINT privacy_case_independent_verification CHECK (
 (identity_verified_by IS NULL OR (identity_verified_by <> subject_user_id AND identity_verified_by <> requester_user_id)) AND
 (representation_verified_by IS NULL OR (representation_verified_by <> subject_user_id AND representation_verified_by <> requester_user_id))
);
ALTER TABLE data_erasure_requests ADD CONSTRAINT privacy_case_representation_snapshot CHECK (
 (representation_verified_at IS NULL) = (representation_relationship_updated_at IS NULL)
);

CREATE TABLE privacy_request_activation_events (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), policy_version varchar(80) NOT NULL REFERENCES privacy_request_policies(version) ON DELETE RESTRICT,
 actor_ref uuid NOT NULL, enabled boolean NOT NULL, fulfilment_ready boolean NOT NULL, occurred_at timestamptz NOT NULL
);
CREATE TRIGGER privacy_activation_events_immutable BEFORE UPDATE OR DELETE ON privacy_request_activation_events FOR EACH ROW EXECUTE FUNCTION prevent_privacy_audit_mutation();

-- Reconfirmation throttles contain only an account bucket or a keyed IP digest.
CREATE TABLE privacy_request_auth_limits (
 bucket text PRIMARY KEY, window_start timestamptz NOT NULL, attempts integer NOT NULL CHECK(attempts > 0)
);
ALTER TABLE privacy_request_policies DROP CONSTRAINT privacy_request_policies_response_months_check;
ALTER TABLE privacy_request_policies ADD CONSTRAINT privacy_request_policies_response_months_check CHECK(response_months = 1);
ALTER TABLE privacy_request_policies DROP CONSTRAINT privacy_request_policies_extension_months_check;
ALTER TABLE privacy_request_policies ADD CONSTRAINT privacy_request_policies_extension_months_check CHECK(extension_months = 2);

CREATE TABLE privacy_request_maintenance_events (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), actor_ref uuid NOT NULL, occurred_at timestamptz NOT NULL,
 working_records integer NOT NULL, evidence_records integer NOT NULL
);
CREATE TRIGGER privacy_maintenance_events_immutable BEFORE UPDATE OR DELETE ON privacy_request_maintenance_events FOR EACH ROW EXECUTE FUNCTION prevent_privacy_audit_mutation();
CREATE OR REPLACE FUNCTION prevent_privacy_audit_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_TABLE_NAME = 'data_erasure_request_events' AND TG_OP = 'DELETE' AND EXISTS (
 SELECT 1 FROM data_erasure_requests WHERE id=OLD.request_id AND status IN ('CANCELLED','REFUSED') AND evidence_expires_at <= now()
 ) THEN RETURN OLD; END IF;
 RAISE EXCEPTION 'privacy audit events are append-only until approved evidence expiry';
END; $$;
CREATE FUNCTION expire_privacy_request_records(p_actor uuid) RETURNS TABLE(working_records integer,evidence_records integer) LANGUAGE plpgsql AS $$
DECLARE r record; n integer; BEGIN
 IF NOT EXISTS(SELECT 1 FROM users u JOIN user_platform_roles ur ON ur.user_id=u.id JOIN platform_roles role ON role.id=ur.role_id
 WHERE u.id=p_actor AND u.is_active AND NOT u.is_dependent AND (u.date_of_birth IS NULL OR u.date_of_birth <= (now() AT TIME ZONE 'Europe/Lisbon')::date - interval '18 years') AND role.code='ADMIN') THEN RAISE EXCEPTION 'privacy operator is not authorized'; END IF;
 PERFORM pg_advisory_xact_lock(110,110);
 working_records:=0; evidence_records:=0;
 FOR r IN SELECT id FROM data_erasure_requests WHERE status IN ('REFUSED','CANCELLED') AND working_expires_at<=now() AND working_erased_at IS NULL FOR UPDATE LOOP
  DELETE FROM email_outbox WHERE privacy_request_id=r.id;
  DELETE FROM privacy_request_dependant_resolutions WHERE request_id=r.id;
  UPDATE data_erasure_requests SET decision_explanation='', category_decisions='[]', categories='{}', policy_snapshot=NULL, policy_version=NULL,
   identity_verified_at=NULL,identity_verified_by=NULL,identity_method=NULL,
   representation_verified_at=NULL,representation_verified_by=NULL,representation_method=NULL,representation_guardian_id=NULL,representation_relationship_updated_at=NULL,representation_conflict=false,
   requester_user_id=NULL,subject_user_id=NULL,claimed_by=NULL,decided_by=NULL,working_erased_at=now()
  WHERE id=r.id;
  working_records:=working_records+1;
 END LOOP;
 FOR r IN SELECT id FROM data_erasure_requests WHERE status IN ('REFUSED','CANCELLED') AND evidence_expires_at<=now() FOR UPDATE LOOP
  DELETE FROM email_outbox WHERE privacy_request_id=r.id;
  DELETE FROM privacy_request_dependant_resolutions WHERE request_id=r.id OR related_request_id=r.id;
  DELETE FROM data_erasure_request_events WHERE request_id=r.id;
  DELETE FROM data_erasure_requests WHERE id=r.id;
  evidence_records:=evidence_records+1;
 END LOOP;
 DELETE FROM privacy_request_auth_limits WHERE window_start < now()-interval '1 day';
 INSERT INTO privacy_request_maintenance_events(actor_ref,occurred_at,working_records,evidence_records) VALUES(p_actor,now(),working_records,evidence_records);
 RETURN NEXT;
END; $$;
