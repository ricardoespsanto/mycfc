-- #245: lease-fenced relational erasure, tombstones, retention anchors, and
-- controlled immutable-record handling. Additive except for strengthened
-- checks/functions; no existing subject row is rewritten by this migration.

CREATE TABLE privacy_pseudonymous_principals (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 purpose varchar(30) NOT NULL CHECK(purpose IN ('AUDIT','MEMBERSHIP')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TRIGGER privacy_pseudonymous_principals_immutable BEFORE UPDATE OR DELETE
 ON privacy_pseudonymous_principals FOR EACH ROW EXECUTE FUNCTION prevent_privacy_execution_record_delete();

ALTER TABLE minor_credential_audit
 ADD COLUMN minor_principal_id uuid NULL REFERENCES privacy_pseudonymous_principals(id) ON DELETE RESTRICT,
 ADD COLUMN guardian_principal_id uuid NULL REFERENCES privacy_pseudonymous_principals(id) ON DELETE RESTRICT,
 ADD COLUMN actor_principal_id uuid NULL REFERENCES privacy_pseudonymous_principals(id) ON DELETE RESTRICT,
 ALTER COLUMN minor_user_id DROP NOT NULL,
 ALTER COLUMN guardian_user_id DROP NOT NULL,
 ALTER COLUMN actor_user_id DROP NOT NULL,
 ADD CONSTRAINT minor_credential_audit_minor_principal_exactly_one CHECK(num_nonnulls(minor_user_id,minor_principal_id)=1) NOT VALID,
 ADD CONSTRAINT minor_credential_audit_guardian_principal_exactly_one CHECK(num_nonnulls(guardian_user_id,guardian_principal_id)=1) NOT VALID,
 ADD CONSTRAINT minor_credential_audit_actor_principal_exactly_one CHECK(num_nonnulls(actor_user_id,actor_principal_id)=1) NOT VALID;
ALTER TABLE equipment_audit_events ADD COLUMN actor_principal_id uuid NULL REFERENCES privacy_pseudonymous_principals(id) ON DELETE RESTRICT,
 ALTER COLUMN actor_user_id DROP NOT NULL,
 ADD CONSTRAINT equipment_audit_actor_principal_exactly_one CHECK(num_nonnulls(actor_user_id,actor_principal_id)=1) NOT VALID;
ALTER TABLE member_profile_audit_events
 ADD COLUMN actor_principal_id uuid NULL REFERENCES privacy_pseudonymous_principals(id) ON DELETE RESTRICT,
 ADD COLUMN subject_principal_id uuid NULL REFERENCES privacy_pseudonymous_principals(id) ON DELETE RESTRICT,
 ALTER COLUMN actor_user_id DROP NOT NULL,
 ALTER COLUMN subject_user_id DROP NOT NULL,
 ADD CONSTRAINT member_profile_audit_actor_principal_exactly_one CHECK(num_nonnulls(actor_user_id,actor_principal_id)=1) NOT VALID,
 ADD CONSTRAINT member_profile_audit_subject_principal_exactly_one CHECK(num_nonnulls(subject_user_id,subject_principal_id)=1) NOT VALID;
ALTER TABLE staff_grant_audit_events ADD COLUMN actor_principal_id uuid NULL REFERENCES privacy_pseudonymous_principals(id) ON DELETE RESTRICT,
 ALTER COLUMN actor_user_id DROP NOT NULL,
 ADD CONSTRAINT staff_grant_audit_actor_principal_exactly_one CHECK(num_nonnulls(actor_user_id,actor_principal_id)=1) NOT VALID;
ALTER TABLE photo_album_audit_events ADD COLUMN actor_principal_id uuid NULL REFERENCES privacy_pseudonymous_principals(id) ON DELETE RESTRICT,
 ALTER COLUMN actor_user_id DROP NOT NULL,
 ADD CONSTRAINT photo_album_audit_actor_principal_exactly_one CHECK(num_nonnulls(actor_user_id,actor_principal_id)=1) NOT VALID;
ALTER TABLE announcement_audit_events ADD COLUMN actor_principal_id uuid NULL REFERENCES privacy_pseudonymous_principals(id) ON DELETE RESTRICT,
 ALTER COLUMN actor_user_id DROP NOT NULL,
 ADD CONSTRAINT announcement_audit_actor_principal_exactly_one CHECK(num_nonnulls(actor_user_id,actor_principal_id)=1) NOT VALID;
ALTER TABLE feature_flag_events ADD COLUMN actor_principal_id uuid NULL REFERENCES privacy_pseudonymous_principals(id) ON DELETE RESTRICT,
 ALTER COLUMN actor_user_id DROP NOT NULL,
 ADD CONSTRAINT feature_flag_audit_actor_principal_exactly_one CHECK(num_nonnulls(actor_user_id,actor_principal_id)=1) NOT VALID;
ALTER TABLE training_copy_events ADD COLUMN copied_by_principal_id uuid NULL REFERENCES privacy_pseudonymous_principals(id) ON DELETE RESTRICT,
 ALTER COLUMN copied_by_id DROP NOT NULL,
 ADD CONSTRAINT training_copy_actor_principal_exactly_one CHECK(num_nonnulls(copied_by_id,copied_by_principal_id)=1) NOT VALID;

ALTER TABLE minor_credential_audit VALIDATE CONSTRAINT minor_credential_audit_minor_principal_exactly_one;
ALTER TABLE minor_credential_audit VALIDATE CONSTRAINT minor_credential_audit_guardian_principal_exactly_one;
ALTER TABLE minor_credential_audit VALIDATE CONSTRAINT minor_credential_audit_actor_principal_exactly_one;
ALTER TABLE equipment_audit_events VALIDATE CONSTRAINT equipment_audit_actor_principal_exactly_one;
ALTER TABLE member_profile_audit_events VALIDATE CONSTRAINT member_profile_audit_actor_principal_exactly_one;
ALTER TABLE member_profile_audit_events VALIDATE CONSTRAINT member_profile_audit_subject_principal_exactly_one;
ALTER TABLE staff_grant_audit_events VALIDATE CONSTRAINT staff_grant_audit_actor_principal_exactly_one;
ALTER TABLE photo_album_audit_events VALIDATE CONSTRAINT photo_album_audit_actor_principal_exactly_one;
ALTER TABLE announcement_audit_events VALIDATE CONSTRAINT announcement_audit_actor_principal_exactly_one;
ALTER TABLE feature_flag_events VALIDATE CONSTRAINT feature_flag_audit_actor_principal_exactly_one;
ALTER TABLE training_copy_events VALIDATE CONSTRAINT training_copy_actor_principal_exactly_one;

ALTER TABLE user_memberships
 ADD COLUMN principal_id uuid NULL REFERENCES privacy_pseudonymous_principals(id) ON DELETE RESTRICT,
 ALTER COLUMN user_id DROP NOT NULL,
 ADD CONSTRAINT user_memberships_subject_principal_exactly_one CHECK(num_nonnulls(user_id,principal_id)=1) NOT VALID;
ALTER TABLE user_memberships VALIDATE CONSTRAINT user_memberships_subject_principal_exactly_one;

ALTER TABLE users ADD COLUMN erased_at timestamptz NULL;
ALTER TABLE users ADD COLUMN erasure_execution_id uuid NULL
 REFERENCES privacy_erasure_executions(id) ON DELETE RESTRICT;
CREATE UNIQUE INDEX users_erasure_execution_uidx ON users(erasure_execution_id)
 WHERE erasure_execution_id IS NOT NULL;

ALTER TABLE users DROP CONSTRAINT users_name_valid;
ALTER TABLE users DROP CONSTRAINT users_identity_shape;
ALTER TABLE users ADD CONSTRAINT users_name_valid CHECK (
 (erased_at IS NULL AND name=btrim(name) AND char_length(name) BETWEEN 2 AND 120)
 OR (erased_at IS NOT NULL AND name='Conta eliminada')
) NOT VALID;
ALTER TABLE users ADD CONSTRAINT users_identity_shape CHECK (
 (erased_at IS NULL AND erasure_execution_id IS NULL AND (
   (is_dependent AND guardian_id IS NOT NULL AND email IS NULL AND
    ((minor_login_id IS NULL AND password_hash IS NULL) OR (minor_login_id IS NOT NULL AND password_hash IS NOT NULL)))
   OR
   (NOT is_dependent AND guardian_id IS NULL AND email IS NOT NULL AND password_hash IS NOT NULL AND minor_login_id IS NULL)
 ))
 OR
 (erased_at IS NOT NULL AND erasure_execution_id IS NOT NULL AND NOT is_active
  AND NOT leaderboard_visible AND NOT is_dependent AND guardian_id IS NULL
  AND email IS NULL AND email_verified_at IS NULL AND minor_login_id IS NULL
  AND password_hash IS NULL AND name='Conta eliminada' AND date_of_birth=DATE '1900-01-01')
) NOT VALID;
ALTER TABLE users VALIDATE CONSTRAINT users_name_valid;
ALTER TABLE users VALIDATE CONSTRAINT users_identity_shape;

CREATE FUNCTION prevent_erased_user_reidentification() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.erased_at IS NOT NULL AND NEW IS DISTINCT FROM OLD THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='erased_user_is_immutable';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER users_erased_immutable BEFORE UPDATE ON users FOR EACH ROW
 EXECUTE FUNCTION prevent_erased_user_reidentification();

-- Every subject-owned insert/update takes a key-share lock on the user. The
-- erasure routine takes FOR UPDATE, closing the insert-vs-erasure race. Actor
-- and author references deliberately remain opaque historical references.
CREATE OR REPLACE FUNCTION require_active_privacy_attachment_subject() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE subject_id uuid; old_subject_id uuid; subject_column text;
BEGIN
 subject_column:=COALESCE(NULLIF(TG_ARGV[0],''),'user_id');
 subject_id:=NULLIF(to_jsonb(NEW)->>subject_column,'')::uuid;
 IF TG_OP='UPDATE' THEN old_subject_id:=NULLIF(to_jsonb(OLD)->>subject_column,'')::uuid; END IF;
 IF current_user<>session_user AND current_setting('mycfc.privacy_erasure_operation',true)='MEMBERSHIP_HISTORY_ANONYMIZE' THEN RETURN NEW; END IF;
 IF subject_id IS NOT NULL THEN
  PERFORM 1 FROM users WHERE id=subject_id AND is_active AND erased_at IS NULL FOR KEY SHARE;
  IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='inactive_privacy_attachment_subject'; END IF;
 END IF;
 IF TG_TABLE_NAME IN ('user_memberships','training_prescriptions') AND EXISTS(
  SELECT 1 FROM privacy_erasure_category_jobs job
  JOIN privacy_erasure_executions execution ON execution.id=job.execution_id
  JOIN data_erasure_requests request ON request.id=execution.request_id
  WHERE job.category_key='membership-history' AND job.status IN ('PENDING','RETRY_WAIT','LEASED')
   AND COALESCE(request.subject_user_id,request.requester_user_id) IN (subject_id,old_subject_id)
 ) THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='privacy_membership_erasure_in_progress'; END IF;
 RETURN NEW;
END;
$$;

DROP TRIGGER users_active_guardian_attachment ON users;
CREATE TRIGGER users_active_guardian_attachment BEFORE INSERT OR UPDATE OF guardian_id ON users
 FOR EACH ROW EXECUTE FUNCTION require_active_privacy_attachment_subject('guardian_id');
DROP TRIGGER email_verification_tokens_active_subject_attachment ON email_verification_tokens;
CREATE TRIGGER email_verification_tokens_active_subject_attachment BEFORE INSERT OR UPDATE OF user_id ON email_verification_tokens
 FOR EACH ROW EXECUTE FUNCTION require_active_privacy_attachment_subject('user_id');
DROP TRIGGER password_reset_tokens_active_subject_attachment ON password_reset_tokens;
CREATE TRIGGER password_reset_tokens_active_subject_attachment BEFORE INSERT OR UPDATE OF user_id ON password_reset_tokens
 FOR EACH ROW EXECUTE FUNCTION require_active_privacy_attachment_subject('user_id');
DROP TRIGGER user_platform_roles_active_subject_attachment ON user_platform_roles;
CREATE TRIGGER user_platform_roles_active_subject_attachment BEFORE INSERT OR UPDATE OF user_id ON user_platform_roles
 FOR EACH ROW EXECUTE FUNCTION require_active_privacy_attachment_subject('user_id');

CREATE TRIGGER consent_forms_active_subject_attachment BEFORE INSERT OR UPDATE OF user_id ON consent_forms
 FOR EACH ROW EXECUTE FUNCTION require_active_privacy_attachment_subject('user_id');
CREATE TRIGGER member_profiles_active_subject_attachment BEFORE INSERT OR UPDATE OF user_id ON member_profiles
 FOR EACH ROW EXECUTE FUNCTION require_active_privacy_attachment_subject('user_id');
CREATE TRIGGER training_logs_active_subject_attachment BEFORE INSERT OR UPDATE OF user_id ON training_logs
 FOR EACH ROW EXECUTE FUNCTION require_active_privacy_attachment_subject('user_id');
CREATE TRIGGER performance_metrics_active_subject_attachment BEFORE INSERT OR UPDATE OF user_id ON performance_metrics
 FOR EACH ROW EXECUTE FUNCTION require_active_privacy_attachment_subject('user_id');
CREATE TRIGGER user_memberships_active_subject_attachment BEFORE INSERT OR UPDATE OF user_id ON user_memberships
 FOR EACH ROW EXECUTE FUNCTION require_active_privacy_attachment_subject('user_id');
CREATE TRIGGER event_responses_active_subject_attachment BEFORE INSERT OR UPDATE OF user_id ON event_responses
 FOR EACH ROW EXECUTE FUNCTION require_active_privacy_attachment_subject('user_id');
CREATE TRIGGER announcement_deliveries_active_subject_attachment BEFORE INSERT OR UPDATE OF user_id ON announcement_deliveries
 FOR EACH ROW EXECUTE FUNCTION require_active_privacy_attachment_subject('user_id');
CREATE TRIGGER activity_connections_active_subject_attachment BEFORE INSERT OR UPDATE OF user_id ON activity_connections
 FOR EACH ROW EXECUTE FUNCTION require_active_privacy_attachment_subject('user_id');
CREATE TRIGGER synced_activities_active_subject_attachment BEFORE INSERT OR UPDATE OF user_id ON synced_activities
 FOR EACH ROW EXECUTE FUNCTION require_active_privacy_attachment_subject('user_id');
CREATE TRIGGER training_activity_matches_active_subject_attachment BEFORE INSERT OR UPDATE OF user_id ON training_session_activity_matches
 FOR EACH ROW EXECUTE FUNCTION require_active_privacy_attachment_subject('user_id');
CREATE TRIGGER suggestions_active_subject_attachment BEFORE INSERT OR UPDATE OF requester_id ON suggestions
 FOR EACH ROW EXECUTE FUNCTION require_active_privacy_attachment_subject('requester_id');
CREATE TRIGGER training_prescriptions_active_subject_attachment BEFORE INSERT OR UPDATE OF athlete_user_id ON training_prescriptions
 FOR EACH ROW EXECUTE FUNCTION require_active_privacy_attachment_subject('athlete_user_id');
CREATE TRIGGER training_outcomes_active_subject_attachment BEFORE INSERT OR UPDATE OF user_id ON training_session_outcomes
 FOR EACH ROW EXECUTE FUNCTION require_active_privacy_attachment_subject('user_id');

-- A retained category is quarantined outside the web role's table grants.
-- The payload is constrained to the exact frozen retained-field allowlist by
-- the only SECURITY DEFINER writer below.
CREATE TABLE privacy_erasure_retention_anchors (
 execution_id uuid NOT NULL REFERENCES privacy_erasure_executions(id) ON DELETE RESTRICT,
 category_key varchar(80) NOT NULL,
 anchor_code varchar(80) NOT NULL,
 anchor_at timestamptz NOT NULL,
 review_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL,
 retained_field_codes text[] NOT NULL,
 worker_ref uuid NOT NULL,
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(execution_id,category_key),
 CONSTRAINT privacy_erasure_retention_anchor_dates CHECK(anchor_at<review_at AND review_at<=expires_at),
 CONSTRAINT privacy_erasure_retention_anchor_fields CHECK(cardinality(retained_field_codes)>0 AND array_position(retained_field_codes,NULL) IS NULL)
);
CREATE TRIGGER privacy_erasure_retention_anchors_immutable BEFORE UPDATE OR DELETE
 ON privacy_erasure_retention_anchors FOR EACH ROW EXECUTE FUNCTION prevent_privacy_execution_record_delete();

CREATE TABLE privacy_erasure_restricted_records (
 execution_id uuid NOT NULL,
 category_key varchar(80) NOT NULL,
 subject_ref uuid NOT NULL,
 retained_data jsonb NOT NULL,
 review_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(execution_id,category_key),
 FOREIGN KEY(execution_id,category_key) REFERENCES privacy_erasure_retention_anchors(execution_id,category_key) ON DELETE RESTRICT,
 CONSTRAINT privacy_erasure_restricted_records_payload CHECK(jsonb_typeof(retained_data)='object'),
 CONSTRAINT privacy_erasure_restricted_records_dates CHECK(review_at<=expires_at)
);
CREATE TRIGGER privacy_erasure_restricted_records_immutable BEFORE UPDATE OR DELETE
 ON privacy_erasure_restricted_records FOR EACH ROW EXECUTE FUNCTION prevent_privacy_execution_record_delete();

ALTER TABLE privacy_erasure_job_checkpoints ADD COLUMN affected_rows bigint NULL
 CHECK(affected_rows IS NULL OR affected_rows>=0);
ALTER TABLE privacy_erasure_job_checkpoints ADD COLUMN result_sha256 bytea NULL
 CHECK(result_sha256 IS NULL OR octet_length(result_sha256)=32);
ALTER TABLE privacy_erasure_job_checkpoints ADD CONSTRAINT privacy_erasure_checkpoint_result_complete CHECK(
 (status='PENDING' AND completed_at IS NULL AND completed_by_attempt_id IS NULL AND affected_rows IS NULL AND result_sha256 IS NULL)
 OR
 (status='SUCCEEDED' AND completed_at IS NOT NULL AND completed_by_attempt_id IS NOT NULL AND affected_rows IS NOT NULL AND result_sha256 IS NOT NULL)
) NOT VALID;
ALTER TABLE privacy_erasure_job_checkpoints VALIDATE CONSTRAINT privacy_erasure_checkpoint_result_complete;

CREATE FUNCTION privacy_scrub_audit_text(p_value text,p_subject_id uuid,p_name text,p_email text,p_login text)
RETURNS text LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE result text:=p_value; canary text; at_position int; search_from int;
BEGIN
 IF result IS NULL THEN RETURN NULL; END IF;
 FOREACH canary IN ARRAY ARRAY[p_subject_id::text,p_name,p_email,p_login] LOOP
 IF canary IS NULL OR canary='' THEN CONTINUE; END IF;
  search_from:=1;
  LOOP
   at_position:=strpos(lower(substr(result,search_from)),lower(canary));
   EXIT WHEN at_position=0;
   at_position:=search_from+at_position-1;
   result:=overlay(result PLACING '[redacted]' FROM at_position FOR char_length(canary));
   search_from:=at_position+char_length('[redacted]');
  END LOOP;
 END LOOP;
 RETURN result;
END;
$$;

CREATE FUNCTION privacy_scrub_audit_json(p_value jsonb,p_subject_id uuid,p_name text,p_email text,p_login text)
RETURNS jsonb LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE result jsonb;
BEGIN
 IF p_value IS NULL THEN RETURN NULL; END IF;
 CASE jsonb_typeof(p_value)
 WHEN 'object' THEN
  SELECT COALESCE(jsonb_object_agg(privacy_scrub_audit_text(item.key,p_subject_id,p_name,p_email,p_login),privacy_scrub_audit_json(item.value,p_subject_id,p_name,p_email,p_login)),'{}'::jsonb)
  INTO result FROM jsonb_each(p_value) item;
 WHEN 'array' THEN
  SELECT COALESCE(jsonb_agg(privacy_scrub_audit_json(item.value,p_subject_id,p_name,p_email,p_login) ORDER BY item.ordinality),'[]'::jsonb)
  INTO result FROM jsonb_array_elements(p_value) WITH ORDINALITY item(value,ordinality);
 WHEN 'string' THEN result:=to_jsonb(privacy_scrub_audit_text(p_value#>>'{}',p_subject_id,p_name,p_email,p_login));
 ELSE result:=p_value;
 END CASE;
 RETURN result;
END;
$$;

CREATE FUNCTION prevent_minor_credential_audit_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF current_user<>session_user AND current_setting('mycfc.privacy_erasure_operation',true)='AUDIT_ACTOR_ANONYMIZE' THEN RETURN NEW; END IF;
 RAISE EXCEPTION 'minor credential audit events are append-only';
END;
$$;
CREATE TRIGGER minor_credential_audit_immutable_trigger BEFORE UPDATE OR DELETE ON minor_credential_audit
 FOR EACH ROW EXECUTE FUNCTION prevent_minor_credential_audit_mutation();

CREATE FUNCTION prevent_announcement_audit_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF current_user<>session_user AND current_setting('mycfc.privacy_erasure_operation',true)='AUDIT_ACTOR_ANONYMIZE' THEN RETURN NEW; END IF;
 RAISE EXCEPTION 'announcement audit events are append-only';
END;
$$;
CREATE TRIGGER announcement_audit_events_immutable_trigger BEFORE UPDATE OR DELETE ON announcement_audit_events
 FOR EACH ROW EXECUTE FUNCTION prevent_announcement_audit_mutation();

CREATE OR REPLACE FUNCTION prevent_equipment_audit_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF current_user<>session_user AND current_setting('mycfc.privacy_erasure_operation',true)='AUDIT_ACTOR_ANONYMIZE' THEN RETURN NEW; END IF;
 RAISE EXCEPTION 'equipment audit events are append-only';
END;
$$;
CREATE OR REPLACE FUNCTION prevent_member_profile_audit_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF current_user<>session_user AND current_setting('mycfc.privacy_erasure_operation',true)='AUDIT_ACTOR_ANONYMIZE' THEN RETURN NEW; END IF;
 RAISE EXCEPTION 'member profile audit events are append-only';
END;
$$;
CREATE OR REPLACE FUNCTION prevent_staff_grant_audit_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF current_user<>session_user AND current_setting('mycfc.privacy_erasure_operation',true)='AUDIT_ACTOR_ANONYMIZE' THEN RETURN NEW; END IF;
 RAISE EXCEPTION 'staff grant audit events are append-only';
END;
$$;
CREATE OR REPLACE FUNCTION prevent_photo_album_audit_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF current_user<>session_user AND current_setting('mycfc.privacy_erasure_operation',true)='AUDIT_ACTOR_ANONYMIZE' THEN RETURN NEW; END IF;
 RAISE EXCEPTION 'photo album audit events are append-only';
END;
$$;
CREATE OR REPLACE FUNCTION prevent_feature_flag_event_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF current_user<>session_user AND current_setting('mycfc.privacy_erasure_operation',true)='AUDIT_ACTOR_ANONYMIZE' THEN RETURN NEW; END IF;
 RAISE EXCEPTION 'feature flag events are append-only';
END;
$$;
CREATE OR REPLACE FUNCTION prevent_training_copy_event_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF current_user<>session_user AND current_setting('mycfc.privacy_erasure_operation',true)='AUDIT_ACTOR_ANONYMIZE' THEN RETURN NEW; END IF;
 RAISE EXCEPTION 'training copy events are append-only';
END;
$$;

-- Ordinary application writes remain immutable. Only a revoked-from-PUBLIC
-- SECURITY DEFINER call (current_user differs from session_user) may set the
-- transaction-local exact-operation marker used for prescription deletion.
CREATE OR REPLACE FUNCTION prevent_training_publication_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
 IF TG_TABLE_NAME='training_prescriptions'
    AND current_user<>session_user
    AND current_setting('mycfc.privacy_erasure_operation',true)='TRAINING_PRESCRIPTION_DELETE' THEN
  RETURN OLD;
 END IF;
 RAISE EXCEPTION 'training publications and prescriptions are immutable';
END;
$$;

CREATE FUNCTION privacy_worker_execute_checkpoint(
 p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_epoch bigint,p_worker_ref uuid,p_operation_code text,p_action_version text
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE checkpoint_ref uuid; subject_ref uuid; execution_ref uuid; principal_ref uuid; category text; entry jsonb;
 subject_name text; subject_email text; subject_login text;
 operation_index int; changed bigint:=0; n bigint:=0; result_digest bytea; anchor privacy_erasure_retention_anchors%ROWTYPE;
BEGIN
 SELECT checkpoint.id,execution.id,request.subject_user_id,job.category_key,
        plan.plan->'entries'->(job.plan_entry_position-1),checkpoint.operation_position
 INTO checkpoint_ref,execution_ref,subject_ref,category,entry,operation_index
 FROM privacy_erasure_category_jobs job
 JOIN privacy_erasure_job_leases lease ON lease.id=p_lease_id AND lease.job_id=job.id AND lease.epoch=p_epoch
 JOIN privacy_erasure_job_attempts attempt ON attempt.id=p_attempt_id AND attempt.job_id=job.id AND attempt.lease_id=lease.id AND attempt.lease_epoch=p_epoch
 JOIN privacy_erasure_job_checkpoints checkpoint ON checkpoint.job_id=job.id AND checkpoint.operation_code=p_operation_code AND checkpoint.action_version=p_action_version
 JOIN privacy_erasure_executions execution ON execution.id=job.execution_id
 JOIN data_erasure_requests request ON request.id=execution.request_id
 JOIN privacy_request_execution_plans plan ON plan.request_id=request.id AND plan.plan_sha256=execution.plan_sha256
 WHERE job.id=p_job_id AND job.status='LEASED' AND lease.worker_ref=p_worker_ref
  AND lease.released_at IS NULL AND lease.expires_at>clock_timestamp() AND attempt.finished_at IS NULL
 FOR UPDATE OF job,lease,attempt,checkpoint;
 IF NOT FOUND THEN RETURN NULL; END IF;
 IF (SELECT status FROM privacy_erasure_job_checkpoints WHERE id=checkpoint_ref)='SUCCEEDED' THEN RETURN checkpoint_ref; END IF;
 IF subject_ref IS NULL OR entry IS NULL OR entry->>'category'<>category OR entry->>'fallback'<>'BLOCK'
    OR entry->>'action_version'<>p_action_version
    OR entry->'operations'->>(operation_index-1)<>p_operation_code
    OR EXISTS(SELECT 1 FROM privacy_erasure_job_checkpoints prior WHERE prior.job_id=p_job_id AND prior.operation_position<operation_index AND prior.status<>'SUCCEEDED')
    OR EXISTS(SELECT 1 FROM privacy_erasure_category_jobs prior WHERE prior.execution_id=execution_ref AND prior.plan_entry_position<(SELECT plan_entry_position FROM privacy_erasure_category_jobs WHERE id=p_job_id) AND prior.status<>'SUCCEEDED') THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_execution_order_or_plan_invalid';
 END IF;
 SELECT name,email::text,minor_login_id::text INTO subject_name,subject_email,subject_login
 FROM users WHERE id=subject_ref FOR UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_subject_missing'; END IF;
 IF entry->>'disposition' IN ('RESTRICT','EXPIRE') THEN
  SELECT * INTO anchor FROM privacy_erasure_retention_anchors a WHERE a.execution_id=execution_ref AND a.category_key=category;
  IF NOT FOUND OR anchor.anchor_code<>entry->>'retention_anchor' OR anchor.retained_field_codes<>(SELECT array_agg(value ORDER BY value) FROM jsonb_array_elements_text(entry->'retained_fields')) THEN
   RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_retention_anchor_unresolved';
  END IF;
 END IF;

 CASE p_operation_code
 WHEN 'AUDIT_ACTOR_ANONYMIZE' THEN
  IF EXISTS(SELECT 1 FROM minor_credential_audit WHERE minor_user_id=subject_ref OR guardian_user_id=subject_ref OR actor_user_id=subject_ref)
   OR EXISTS(SELECT 1 FROM equipment_audit_events WHERE actor_user_id=subject_ref)
   OR EXISTS(SELECT 1 FROM equipment_audit_events WHERE before_state IS DISTINCT FROM privacy_scrub_audit_json(before_state,subject_ref,subject_name,subject_email,subject_login) OR after_state IS DISTINCT FROM privacy_scrub_audit_json(after_state,subject_ref,subject_name,subject_email,subject_login))
   OR EXISTS(SELECT 1 FROM member_profile_audit_events WHERE actor_user_id=subject_ref OR subject_user_id=subject_ref)
   OR EXISTS(SELECT 1 FROM staff_grant_audit_events WHERE actor_user_id=subject_ref)
   OR EXISTS(SELECT 1 FROM staff_grant_audit_events WHERE reason IS DISTINCT FROM privacy_scrub_audit_text(reason,subject_ref,subject_name,subject_email,subject_login))
   OR EXISTS(SELECT 1 FROM photo_album_audit_events WHERE actor_user_id=subject_ref)
   OR EXISTS(SELECT 1 FROM announcement_audit_events WHERE actor_user_id=subject_ref)
   OR EXISTS(SELECT 1 FROM feature_flag_events WHERE actor_user_id=subject_ref)
   OR EXISTS(SELECT 1 FROM training_copy_events WHERE copied_by_id=subject_ref) THEN
   INSERT INTO privacy_pseudonymous_principals(purpose) VALUES('AUDIT') RETURNING id INTO principal_ref;
   PERFORM set_config('mycfc.privacy_erasure_operation','AUDIT_ACTOR_ANONYMIZE',true);

   UPDATE minor_credential_audit SET
    minor_principal_id=CASE WHEN minor_user_id=subject_ref THEN principal_ref ELSE minor_principal_id END,
    minor_user_id=CASE WHEN minor_user_id=subject_ref THEN NULL ELSE minor_user_id END,
    guardian_principal_id=CASE WHEN guardian_user_id=subject_ref THEN principal_ref ELSE guardian_principal_id END,
    guardian_user_id=CASE WHEN guardian_user_id=subject_ref THEN NULL ELSE guardian_user_id END,
    actor_principal_id=CASE WHEN actor_user_id=subject_ref THEN principal_ref ELSE actor_principal_id END,
    actor_user_id=CASE WHEN actor_user_id=subject_ref THEN NULL ELSE actor_user_id END,
    issued_login_id=CASE WHEN minor_user_id=subject_ref THEN 'anonymised' ELSE privacy_scrub_audit_text(issued_login_id::text,subject_ref,subject_name,subject_email,subject_login) END
   WHERE minor_user_id=subject_ref OR guardian_user_id=subject_ref OR actor_user_id=subject_ref;
   GET DIAGNOSTICS changed=ROW_COUNT;

   UPDATE equipment_audit_events SET
    actor_principal_id=CASE WHEN actor_user_id=subject_ref THEN principal_ref ELSE actor_principal_id END,
    actor_user_id=CASE WHEN actor_user_id=subject_ref THEN NULL ELSE actor_user_id END,
    before_state=privacy_scrub_audit_json(before_state,subject_ref,subject_name,subject_email,subject_login),
    after_state=privacy_scrub_audit_json(after_state,subject_ref,subject_name,subject_email,subject_login)
   WHERE actor_user_id=subject_ref
    OR before_state IS DISTINCT FROM privacy_scrub_audit_json(before_state,subject_ref,subject_name,subject_email,subject_login)
    OR after_state IS DISTINCT FROM privacy_scrub_audit_json(after_state,subject_ref,subject_name,subject_email,subject_login);
   GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
   UPDATE member_profile_audit_events SET
    actor_principal_id=CASE WHEN actor_user_id=subject_ref THEN principal_ref ELSE actor_principal_id END,
    actor_user_id=CASE WHEN actor_user_id=subject_ref THEN NULL ELSE actor_user_id END,
    subject_principal_id=CASE WHEN subject_user_id=subject_ref THEN principal_ref ELSE subject_principal_id END,
    subject_user_id=CASE WHEN subject_user_id=subject_ref THEN NULL ELSE subject_user_id END
   WHERE actor_user_id=subject_ref OR subject_user_id=subject_ref; GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
   UPDATE staff_grant_audit_events SET
    actor_principal_id=CASE WHEN actor_user_id=subject_ref THEN principal_ref ELSE actor_principal_id END,
    actor_user_id=CASE WHEN actor_user_id=subject_ref THEN NULL ELSE actor_user_id END,
    reason=privacy_scrub_audit_text(reason,subject_ref,subject_name,subject_email,subject_login)
   WHERE actor_user_id=subject_ref OR reason IS DISTINCT FROM privacy_scrub_audit_text(reason,subject_ref,subject_name,subject_email,subject_login);
   GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
   UPDATE photo_album_audit_events SET actor_user_id=NULL,actor_principal_id=principal_ref
   WHERE actor_user_id=subject_ref; GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
   UPDATE announcement_audit_events SET actor_user_id=NULL,actor_principal_id=principal_ref
   WHERE actor_user_id=subject_ref; GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
   UPDATE feature_flag_events SET actor_user_id=NULL,actor_principal_id=principal_ref
   WHERE actor_user_id=subject_ref; GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
   UPDATE training_copy_events SET copied_by_id=NULL,copied_by_principal_id=principal_ref
   WHERE copied_by_id=subject_ref; GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
  END IF;
 WHEN 'ACTIVITY_CONNECTION_DISCONNECT' THEN
  UPDATE activity_connections SET status='DISCONNECTED',provider_user_id='erased-'||id::text,credentials_ciphertext=NULL,credential_key_id=NULL,
   credential_expires_at=NULL,scopes='{}',sync_cursor=NULL,last_error_code=NULL,last_error_message=NULL,last_error_at=NULL,
   disconnected_at=COALESCE(disconnected_at,clock_timestamp()),updated_at=clock_timestamp()
  WHERE user_id=subject_ref AND status<>'DISCONNECTED'; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'ACTIVITY_SUBJECT_DELETE' THEN
  DELETE FROM activity_connections WHERE user_id=subject_ref; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'ANNOUNCEMENT_DELIVERY_DELETE' THEN
  DELETE FROM announcement_deliveries WHERE user_id=subject_ref; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'AUTH_ACCESS_REVOKE' THEN
  DELETE FROM sessions WHERE subject_indexed AND user_id=subject_ref; GET DIAGNOSTICS changed=ROW_COUNT;
  DELETE FROM user_platform_roles WHERE user_id=subject_ref; GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
 WHEN 'AUTH_TOKEN_DELETE' THEN
  DELETE FROM email_verification_tokens WHERE user_id=subject_ref; GET DIAGNOSTICS changed=ROW_COUNT;
  DELETE FROM password_reset_tokens WHERE user_id=subject_ref; GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
 WHEN 'DEPENDANT_RELATIONSHIP_DELETE' THEN
  IF EXISTS(SELECT 1 FROM users WHERE guardian_id=subject_ref) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_dependants_unresolved'; END IF;
  changed:=0;
 WHEN 'EVENT_RESPONSE_DELETE' THEN
  DELETE FROM event_responses WHERE user_id=subject_ref; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'IDENTITY_CLEAR' THEN
  IF EXISTS(SELECT 1 FROM sessions WHERE NOT subject_indexed AND expiry>clock_timestamp()) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_legacy_sessions_unresolved'; END IF;
  IF EXISTS(SELECT 1 FROM users WHERE guardian_id=subject_ref) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_dependants_unresolved'; END IF;
  DELETE FROM sessions WHERE subject_indexed AND user_id=subject_ref;
  DELETE FROM email_verification_tokens WHERE user_id=subject_ref;
  DELETE FROM password_reset_tokens WHERE user_id=subject_ref;
  DELETE FROM user_platform_roles WHERE user_id=subject_ref;
  UPDATE users SET name='Conta eliminada',email=NULL,email_verified_at=NULL,minor_login_id=NULL,password_hash=NULL,
   guardian_id=NULL,is_dependent=false,date_of_birth=DATE '1900-01-01',is_active=false,leaderboard_visible=false,
   credential_version=credential_version+1,erased_at=clock_timestamp(),erasure_execution_id=execution_ref,updated_at=clock_timestamp()
  WHERE id=subject_ref AND erased_at IS NULL; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'MEMBERSHIP_ACTIVE_REVOKE' THEN
  IF EXISTS(SELECT 1 FROM training_prescriptions prescription JOIN user_memberships membership ON membership.id=prescription.membership_id
   WHERE membership.user_id=subject_ref AND membership.starts_on>=CURRENT_DATE) THEN
   RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_dependency_unsupported';
  END IF;
  DELETE FROM training_variations WHERE target_membership_id IN(
   SELECT id FROM user_memberships WHERE user_id=subject_ref AND starts_on>=CURRENT_DATE);
  GET DIAGNOSTICS changed=ROW_COUNT;
  DELETE FROM training_variation_group_members WHERE membership_id IN(
   SELECT id FROM user_memberships WHERE user_id=subject_ref AND starts_on>=CURRENT_DATE);
  GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
  DELETE FROM training_group_members WHERE membership_id IN(
   SELECT id FROM user_memberships WHERE user_id=subject_ref AND starts_on>=CURRENT_DATE);
  GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
  DELETE FROM user_memberships WHERE user_id=subject_ref AND starts_on>=CURRENT_DATE;
  GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
  UPDATE user_memberships SET ends_on=CURRENT_DATE-1,updated_at=clock_timestamp()
   WHERE user_id=subject_ref AND starts_on<CURRENT_DATE AND (ends_on IS NULL OR ends_on>=CURRENT_DATE);
  GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
 WHEN 'MEMBERSHIP_HISTORY_ANONYMIZE' THEN
  IF EXISTS(SELECT 1 FROM training_prescriptions prescription JOIN user_memberships membership ON membership.id=prescription.membership_id
   WHERE membership.user_id=subject_ref) THEN
   RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_dependency_unsupported';
  END IF;
  IF EXISTS(SELECT 1 FROM user_memberships WHERE user_id=subject_ref) THEN
   INSERT INTO privacy_pseudonymous_principals(purpose) VALUES('MEMBERSHIP') RETURNING id INTO principal_ref;
   PERFORM set_config('mycfc.privacy_erasure_operation','MEMBERSHIP_HISTORY_ANONYMIZE',true);
   UPDATE training_variations SET
    change_summary=privacy_scrub_audit_text(change_summary,subject_ref,subject_name,subject_email,subject_login),
    patch=privacy_scrub_audit_json(patch,subject_ref,subject_name,subject_email,subject_login),
    updated_at=clock_timestamp()
   WHERE target_membership_id IN(SELECT id FROM user_memberships WHERE user_id=subject_ref);
   GET DIAGNOSTICS changed=ROW_COUNT;
   UPDATE user_memberships SET user_id=NULL,principal_id=principal_ref,updated_at=clock_timestamp()
   WHERE user_id=subject_ref;
   GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
  END IF;
 WHEN 'PROFILE_HEALTH_DELETE' THEN
  UPDATE member_profiles SET emergency_contact_name='',emergency_contact_relationship='',emergency_contact_phone='',emergency_contact_alternate_phone='',
   medical_declaration='UNKNOWN',allergies='',medical_conditions='',medication='',activity_restrictions='',medical_notes='',updated_at=clock_timestamp()
  WHERE user_id=subject_ref; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'PROFILE_IDENTITY_DELETE' THEN
  DELETE FROM member_profiles WHERE user_id=subject_ref; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'REPAIR_REPORTER_ANONYMIZE' THEN
  UPDATE repair_requests SET reported_by_id=NULL,updated_at=clock_timestamp() WHERE reported_by_id=subject_ref;
  GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'SUGGESTION_SUBJECT_DELETE' THEN
  DELETE FROM suggestions WHERE requester_id=subject_ref; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'TRAINING_PRESCRIPTION_DELETE' THEN
  UPDATE training_session_outcomes SET prescription_id=NULL,updated_at=clock_timestamp()
   WHERE prescription_id IN(SELECT id FROM training_prescriptions WHERE athlete_user_id=subject_ref);
  PERFORM set_config('mycfc.privacy_erasure_operation','TRAINING_PRESCRIPTION_DELETE',true);
  DELETE FROM training_prescriptions WHERE athlete_user_id=subject_ref; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'TRAINING_RESULT_DELETE' THEN
  DELETE FROM training_session_outcomes WHERE user_id=subject_ref; GET DIAGNOSTICS changed=ROW_COUNT;
  DELETE FROM training_logs WHERE user_id=subject_ref; GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
  DELETE FROM performance_metrics WHERE user_id=subject_ref; GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
 ELSE
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_relational_operation_unsupported';
 END CASE;

 -- Verification is in the same transaction as mutation and checkpoint
 -- success. Any residual subject row aborts the whole checkpoint.
 CASE p_operation_code
 WHEN 'AUDIT_ACTOR_ANONYMIZE' THEN
  IF EXISTS(SELECT 1 FROM minor_credential_audit WHERE minor_user_id=subject_ref OR guardian_user_id=subject_ref OR actor_user_id=subject_ref)
   OR EXISTS(SELECT 1 FROM equipment_audit_events WHERE actor_user_id=subject_ref)
   OR EXISTS(SELECT 1 FROM member_profile_audit_events WHERE actor_user_id=subject_ref OR subject_user_id=subject_ref)
   OR EXISTS(SELECT 1 FROM staff_grant_audit_events WHERE actor_user_id=subject_ref)
   OR EXISTS(SELECT 1 FROM photo_album_audit_events WHERE actor_user_id=subject_ref)
   OR EXISTS(SELECT 1 FROM announcement_audit_events WHERE actor_user_id=subject_ref)
   OR EXISTS(SELECT 1 FROM feature_flag_events WHERE actor_user_id=subject_ref)
   OR EXISTS(SELECT 1 FROM training_copy_events WHERE copied_by_id=subject_ref)
   OR EXISTS(SELECT 1 FROM minor_credential_audit WHERE (minor_principal_id=principal_ref OR guardian_principal_id=principal_ref OR actor_principal_id=principal_ref)
      AND (position(lower(subject_ref::text) in lower(issued_login_id::text))>0
       OR (subject_name<>'' AND position(lower(subject_name) in lower(issued_login_id::text))>0)
       OR (subject_email IS NOT NULL AND position(lower(subject_email) in lower(issued_login_id::text))>0)
       OR (subject_login IS NOT NULL AND position(lower(subject_login) in lower(issued_login_id::text))>0)))
   OR EXISTS(SELECT 1 FROM equipment_audit_events WHERE before_state IS DISTINCT FROM privacy_scrub_audit_json(before_state,subject_ref,subject_name,subject_email,subject_login)
       OR after_state IS DISTINCT FROM privacy_scrub_audit_json(after_state,subject_ref,subject_name,subject_email,subject_login))
   OR EXISTS(SELECT 1 FROM staff_grant_audit_events WHERE reason IS DISTINCT FROM privacy_scrub_audit_text(reason,subject_ref,subject_name,subject_email,subject_login)) THEN
   RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_relational_verification_failed';
  END IF;
 WHEN 'ACTIVITY_CONNECTION_DISCONNECT' THEN
  IF EXISTS(SELECT 1 FROM activity_connections WHERE user_id=subject_ref AND (status<>'DISCONNECTED' OR credentials_ciphertext IS NOT NULL OR credential_key_id IS NOT NULL OR cardinality(scopes)>0 OR sync_cursor IS NOT NULL)) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_relational_verification_failed'; END IF;
 WHEN 'ACTIVITY_SUBJECT_DELETE' THEN IF EXISTS(SELECT 1 FROM activity_connections WHERE user_id=subject_ref) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_relational_verification_failed'; END IF;
 WHEN 'ANNOUNCEMENT_DELIVERY_DELETE' THEN IF EXISTS(SELECT 1 FROM announcement_deliveries WHERE user_id=subject_ref) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_relational_verification_failed'; END IF;
 WHEN 'AUTH_ACCESS_REVOKE' THEN IF EXISTS(SELECT 1 FROM sessions WHERE subject_indexed AND user_id=subject_ref) OR EXISTS(SELECT 1 FROM user_platform_roles WHERE user_id=subject_ref) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_relational_verification_failed'; END IF;
 WHEN 'AUTH_TOKEN_DELETE' THEN IF EXISTS(SELECT 1 FROM email_verification_tokens WHERE user_id=subject_ref) OR EXISTS(SELECT 1 FROM password_reset_tokens WHERE user_id=subject_ref) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_relational_verification_failed'; END IF;
 WHEN 'DEPENDANT_RELATIONSHIP_DELETE' THEN IF EXISTS(SELECT 1 FROM users WHERE guardian_id=subject_ref) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_relational_verification_failed'; END IF;
 WHEN 'EVENT_RESPONSE_DELETE' THEN IF EXISTS(SELECT 1 FROM event_responses WHERE user_id=subject_ref) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_relational_verification_failed'; END IF;
 WHEN 'IDENTITY_CLEAR' THEN IF NOT EXISTS(SELECT 1 FROM users WHERE id=subject_ref AND erased_at IS NOT NULL AND NOT is_active AND email IS NULL AND minor_login_id IS NULL AND password_hash IS NULL AND guardian_id IS NULL) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_relational_verification_failed'; END IF;
 WHEN 'MEMBERSHIP_ACTIVE_REVOKE' THEN IF EXISTS(SELECT 1 FROM user_memberships WHERE user_id=subject_ref AND (starts_on>=CURRENT_DATE OR ends_on IS NULL OR ends_on>=CURRENT_DATE)) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_relational_verification_failed'; END IF;
 WHEN 'MEMBERSHIP_HISTORY_ANONYMIZE' THEN
  IF EXISTS(SELECT 1 FROM user_memberships WHERE user_id=subject_ref)
   OR EXISTS(SELECT 1 FROM user_memberships WHERE principal_id=principal_ref AND (starts_on>=CURRENT_DATE OR ends_on IS NULL OR ends_on>=CURRENT_DATE))
   OR EXISTS(SELECT 1 FROM training_variations variation JOIN user_memberships membership ON membership.id=variation.target_membership_id
      WHERE membership.principal_id=principal_ref
       AND (variation.change_summary IS DISTINCT FROM privacy_scrub_audit_text(variation.change_summary,subject_ref,subject_name,subject_email,subject_login)
        OR variation.patch IS DISTINCT FROM privacy_scrub_audit_json(variation.patch,subject_ref,subject_name,subject_email,subject_login))) THEN
   RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_relational_verification_failed';
  END IF;
 WHEN 'PROFILE_HEALTH_DELETE' THEN IF EXISTS(SELECT 1 FROM member_profiles WHERE user_id=subject_ref AND (emergency_contact_name<>'' OR emergency_contact_relationship<>'' OR emergency_contact_phone<>'' OR emergency_contact_alternate_phone<>'' OR medical_declaration<>'UNKNOWN' OR allergies<>'' OR medical_conditions<>'' OR medication<>'' OR activity_restrictions<>'' OR medical_notes<>'')) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_relational_verification_failed'; END IF;
 WHEN 'PROFILE_IDENTITY_DELETE' THEN IF EXISTS(SELECT 1 FROM member_profiles WHERE user_id=subject_ref) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_relational_verification_failed'; END IF;
 WHEN 'REPAIR_REPORTER_ANONYMIZE' THEN IF EXISTS(SELECT 1 FROM repair_requests WHERE reported_by_id=subject_ref) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_relational_verification_failed'; END IF;
 WHEN 'SUGGESTION_SUBJECT_DELETE' THEN IF EXISTS(SELECT 1 FROM suggestions WHERE requester_id=subject_ref) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_relational_verification_failed'; END IF;
 WHEN 'TRAINING_PRESCRIPTION_DELETE' THEN IF EXISTS(SELECT 1 FROM training_prescriptions WHERE athlete_user_id=subject_ref) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_relational_verification_failed'; END IF;
 WHEN 'TRAINING_RESULT_DELETE' THEN IF EXISTS(SELECT 1 FROM training_session_outcomes WHERE user_id=subject_ref) OR EXISTS(SELECT 1 FROM training_logs WHERE user_id=subject_ref) OR EXISTS(SELECT 1 FROM performance_metrics WHERE user_id=subject_ref) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_relational_verification_failed'; END IF;
 ELSE NULL;
 END CASE;

 result_digest:=sha256(convert_to(p_operation_code||':'||subject_ref::text||':'||changed::text,'UTF8'));
 UPDATE privacy_erasure_job_checkpoints SET status='SUCCEEDED',completed_at=clock_timestamp(),completed_by_attempt_id=p_attempt_id,
  affected_rows=changed,result_sha256=result_digest WHERE id=checkpoint_ref AND status='PENDING';
 RETURN checkpoint_ref;
END;
$$;

-- A job cannot overtake an earlier entry in the frozen plan. This preserves
-- deterministic partial-failure recovery while still allowing different
-- executions to be claimed concurrently.
CREATE OR REPLACE FUNCTION privacy_worker_claim(p_lease_milliseconds bigint, p_worker_ref uuid)
RETURNS TABLE(job_id uuid,lease_id uuid,attempt_id uuid)
LANGUAGE sql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
WITH candidate AS MATERIALIZED (
 SELECT job.id,job.status,job.lease_epoch
 FROM public.privacy_erasure_category_jobs job
 WHERE p_lease_milliseconds BETWEEN 1000 AND 3600000
 AND ((job.status IN ('PENDING','RETRY_WAIT') AND job.next_attempt_at<=clock_timestamp())
    OR (job.status='LEASED' AND EXISTS(
      SELECT 1 FROM public.privacy_erasure_job_leases lease
      WHERE lease.job_id=job.id AND lease.epoch=job.lease_epoch AND lease.released_at IS NULL AND lease.expires_at<=clock_timestamp()
    )))
 AND NOT EXISTS(
  SELECT 1 FROM public.privacy_erasure_category_jobs earlier
  WHERE earlier.execution_id=job.execution_id AND earlier.plan_entry_position<job.plan_entry_position
   AND earlier.status<>'SUCCEEDED'
 )
 ORDER BY job.next_attempt_at,job.created_at,job.id
 FOR UPDATE SKIP LOCKED LIMIT 1
), authority_clock AS MATERIALIZED (
 SELECT clock_timestamp() AS occurred_at FROM candidate LIMIT 1
), expired_lease AS (
 UPDATE public.privacy_erasure_job_leases lease SET released_at=authority_clock.occurred_at,outcome='EXPIRED'
 FROM candidate,authority_clock WHERE lease.job_id=candidate.id AND lease.epoch=candidate.lease_epoch
 AND candidate.status='LEASED' AND lease.released_at IS NULL AND lease.expires_at<=authority_clock.occurred_at
 RETURNING lease.id,lease.job_id
), expired_attempt AS (
 UPDATE public.privacy_erasure_job_attempts attempt SET finished_at=authority_clock.occurred_at,outcome='LEASE_EXPIRED'
 FROM expired_lease,authority_clock WHERE attempt.lease_id=expired_lease.id AND attempt.finished_at IS NULL
 RETURNING attempt.id
), claimed_job AS (
 UPDATE public.privacy_erasure_category_jobs job SET status='LEASED',lease_epoch=job.lease_epoch+1,
 attempt_count=job.attempt_count+1,updated_at=authority_clock.occurred_at
 FROM candidate,authority_clock WHERE job.id=candidate.id AND (candidate.status<>'LEASED' OR EXISTS(SELECT 1 FROM expired_lease))
 RETURNING job.*
), new_lease AS (
 INSERT INTO public.privacy_erasure_job_leases(job_id,epoch,worker_ref,acquired_at,heartbeat_at,expires_at)
 SELECT id,lease_epoch,p_worker_ref,authority_clock.occurred_at,authority_clock.occurred_at,
 authority_clock.occurred_at+(p_lease_milliseconds*INTERVAL '1 millisecond')
 FROM claimed_job,authority_clock RETURNING *
), new_attempt AS (
 INSERT INTO public.privacy_erasure_job_attempts(job_id,lease_id,lease_epoch,attempt_number,started_at)
 SELECT claimed_job.id,new_lease.id,new_lease.epoch,claimed_job.attempt_count,authority_clock.occurred_at
 FROM claimed_job JOIN new_lease ON new_lease.job_id=claimed_job.id CROSS JOIN authority_clock RETURNING *
), execution_started AS (
 UPDATE public.privacy_erasure_executions execution SET status='RUNNING',version=execution.version+1,
 started_at=COALESCE(execution.started_at,authority_clock.occurred_at),finished_at=NULL,updated_at=authority_clock.occurred_at
 FROM claimed_job,authority_clock WHERE execution.id=claimed_job.execution_id AND execution.status NOT IN ('SUCCEEDED','TERMINAL_FAILED') RETURNING execution.id
)
SELECT claimed_job.id,new_lease.id,new_attempt.id
FROM claimed_job JOIN new_lease ON new_lease.job_id=claimed_job.id JOIN new_attempt ON new_attempt.job_id=claimed_job.id
WHERE EXISTS(SELECT 1 FROM execution_started);
$$;

REVOKE ALL ON TABLE privacy_pseudonymous_principals,privacy_erasure_retention_anchors,privacy_erasure_restricted_records FROM PUBLIC;
REVOKE ALL ON FUNCTION privacy_worker_execute_checkpoint(uuid,uuid,uuid,bigint,uuid,text,text) FROM PUBLIC;
DO $$
DECLARE role_name text;
BEGIN
 FOR role_name IN
  SELECT rolname FROM pg_roles
  WHERE NOT rolsuper AND rolname<>current_user
   AND has_function_privilege(rolname,'privacy_worker_complete_checkpoint(uuid,uuid,uuid,bigint,uuid,text,text)','EXECUTE')
 LOOP
  EXECUTE format('REVOKE EXECUTE ON FUNCTION privacy_worker_complete_checkpoint(uuid,uuid,uuid,bigint,uuid,text,text) FROM %I',role_name);
 END LOOP;
END;
$$;
