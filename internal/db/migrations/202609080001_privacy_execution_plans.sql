-- Additive #243 executable decision plans and scoped retention exceptions.
-- Existing policies and cases are deliberately not backfilled or guessed.
ALTER TABLE privacy_request_policies ADD COLUMN executor_version varchar(80) NULL CHECK (executor_version IS NULL OR char_length(btrim(executor_version)) BETWEEN 1 AND 80);
ALTER TABLE privacy_request_policies ADD COLUMN plan_schema_version varchar(80) NULL CHECK (plan_schema_version IS NULL OR char_length(btrim(plan_schema_version)) BETWEEN 1 AND 80);
ALTER TABLE privacy_request_policies ADD CONSTRAINT privacy_policy_execution_versions_complete CHECK ((executor_version IS NULL) = (plan_schema_version IS NULL));

CREATE TABLE privacy_request_execution_plans (
 request_id uuid PRIMARY KEY REFERENCES data_erasure_requests(id) ON DELETE RESTRICT,
 policy_version varchar(80) NOT NULL REFERENCES privacy_request_policies(version) ON DELETE RESTRICT,
 executor_version varchar(80) NOT NULL CHECK (char_length(btrim(executor_version)) BETWEEN 1 AND 80),
 schema_version varchar(80) NOT NULL CHECK (char_length(btrim(schema_version)) BETWEEN 1 AND 80),
 plan jsonb NOT NULL CHECK (jsonb_typeof(plan) = 'object' AND octet_length(plan::text) <= 204800),
 plan_sha256 bytea NOT NULL,
 created_at timestamptz NOT NULL,
 CHECK (octet_length(plan_sha256) = 32),
 CHECK (plan->>'policy_version' = policy_version AND plan->>'executor_version' = executor_version AND plan->>'schema_version' = schema_version)
);
CREATE TRIGGER privacy_execution_plans_immutable BEFORE UPDATE OR DELETE ON privacy_request_execution_plans FOR EACH ROW EXECUTE FUNCTION prevent_privacy_audit_mutation();

CREATE TABLE privacy_request_retention_exceptions (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), request_id uuid NOT NULL REFERENCES data_erasure_requests(id) ON DELETE RESTRICT,
 owner_ref uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT, actor_ref uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 category_key varchar(120) NOT NULL, purpose_code varchar(120) NOT NULL,
 retained_field_codes text[] NOT NULL, evidence_ref varchar(120) NOT NULL,
 reason_code varchar(20) NOT NULL CHECK (reason_code IN ('COMPLAINT','LEGAL_HOLD')),
 review_at timestamptz NOT NULL, expires_at timestamptz NOT NULL, created_at timestamptz NOT NULL,
 CHECK (array_position(retained_field_codes,NULL) IS NULL AND cardinality(retained_field_codes) BETWEEN 1 AND 50),
 CHECK (review_at > created_at AND expires_at >= review_at),
 UNIQUE(request_id,category_key,reason_code,evidence_ref)
);
CREATE INDEX privacy_request_retention_exceptions_request_idx ON privacy_request_retention_exceptions(request_id,expires_at);
CREATE TRIGGER privacy_retention_exceptions_immutable BEFORE UPDATE OR DELETE ON privacy_request_retention_exceptions FOR EACH ROW EXECUTE FUNCTION prevent_privacy_audit_mutation();

CREATE OR REPLACE FUNCTION prevent_privacy_audit_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP = 'DELETE' THEN
  IF TG_TABLE_NAME = 'privacy_request_retention_exceptions' THEN
   IF OLD.expires_at <= now() AND EXISTS (
    SELECT 1 FROM data_erasure_requests WHERE id=OLD.request_id AND status IN ('CANCELLED','REFUSED') AND evidence_expires_at <= now()
   ) THEN RETURN OLD; END IF;
  ELSIF TG_TABLE_NAME IN ('data_erasure_request_events','privacy_request_execution_plans') THEN
   IF EXISTS (
    SELECT 1 FROM data_erasure_requests request_row WHERE request_row.id=OLD.request_id AND request_row.status IN ('CANCELLED','REFUSED') AND request_row.evidence_expires_at <= now()
    AND NOT EXISTS(SELECT 1 FROM privacy_request_retention_exceptions exception_row WHERE exception_row.request_id=request_row.id AND exception_row.expires_at>now())
   ) THEN RETURN OLD; END IF;
  END IF;
 END IF;
 RAISE EXCEPTION 'privacy audit events are append-only until approved evidence expiry';
END; $$;

CREATE OR REPLACE FUNCTION expire_privacy_request_records(p_actor uuid) RETURNS TABLE(working_records integer,evidence_records integer) LANGUAGE plpgsql AS $$
DECLARE r record; n integer; BEGIN
 IF NOT EXISTS(SELECT 1 FROM users u JOIN user_platform_roles ur ON ur.user_id=u.id JOIN platform_roles role ON role.id=ur.role_id
 WHERE u.id=p_actor AND u.is_active AND NOT u.is_dependent AND (u.date_of_birth IS NULL OR u.date_of_birth <= (now() AT TIME ZONE 'Europe/Lisbon')::date - interval '18 years') AND role.code='ADMIN') THEN RAISE EXCEPTION 'privacy operator is not authorized'; END IF;
 PERFORM pg_advisory_xact_lock(110,110);
 working_records:=0; evidence_records:=0;
 -- A category hold controls the future subject-data executor, not this case's
 -- identifying working copy. The latter is always scrubbed on its own clock.
 FOR r IN SELECT request_row.id FROM data_erasure_requests request_row WHERE request_row.status IN ('REFUSED','CANCELLED') AND request_row.working_expires_at<=now() AND request_row.working_erased_at IS NULL FOR UPDATE LOOP
  DELETE FROM email_outbox WHERE privacy_request_id=r.id;
  DELETE FROM privacy_request_dependant_resolutions WHERE request_id=r.id;
  UPDATE data_erasure_requests SET decision_explanation='', category_decisions='[]', categories='{}', policy_snapshot=NULL, policy_version=NULL,
   identity_verified_at=NULL,identity_verified_by=NULL,identity_method=NULL,
   representation_verified_at=NULL,representation_verified_by=NULL,representation_method=NULL,representation_guardian_id=NULL,representation_relationship_updated_at=NULL,representation_conflict=false,
   requester_user_id=NULL,subject_user_id=NULL,claimed_by=NULL,decided_by=NULL,working_erased_at=now()
  WHERE id=r.id;
  working_records:=working_records+1;
 END LOOP;
 FOR r IN SELECT request_row.id FROM data_erasure_requests request_row WHERE request_row.status IN ('REFUSED','CANCELLED') AND request_row.evidence_expires_at<=now()
  AND NOT EXISTS(SELECT 1 FROM privacy_request_retention_exceptions exception_row WHERE exception_row.request_id=request_row.id AND exception_row.expires_at>now()) FOR UPDATE LOOP
  DELETE FROM email_outbox WHERE privacy_request_id=r.id;
  DELETE FROM privacy_request_dependant_resolutions WHERE request_id=r.id OR related_request_id=r.id;
  DELETE FROM data_erasure_request_events WHERE request_id=r.id;
  DELETE FROM privacy_request_retention_exceptions WHERE request_id=r.id;
  DELETE FROM privacy_request_execution_plans WHERE request_id=r.id;
  DELETE FROM data_erasure_requests WHERE id=r.id;
  evidence_records:=evidence_records+1;
 END LOOP;
 DELETE FROM privacy_request_auth_limits WHERE window_start < now()-interval '1 day';
 INSERT INTO privacy_request_maintenance_events(actor_ref,occurred_at,working_records,evidence_records) VALUES(p_actor,now(),working_records,evidence_records);
 RETURN NEXT;
END; $$;
