-- Reset-only baseline schema. Apply this file with psql to a newly created database.
CREATE EXTENSION IF NOT EXISTS citext;
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE TYPE repair_status AS ENUM ('Pendente', 'Em_Analise', 'Resolvido');
CREATE TYPE consent_type AS ENUM ('Termos_Gerais', 'Uso_Imagem', 'Responsabilidade_Menor', 'Dados_Saude', 'Foto_Perfil');
CREATE TYPE equipment_type AS ENUM ('Boat', 'Paddle', 'Vehicle');
CREATE TYPE equipment_status AS ENUM ('Operational', 'Maintenance', 'Retired');
CREATE TYPE maintenance_status AS ENUM ('Scheduled', 'In_Progress', 'Completed', 'Cancelled');
CREATE TYPE metric_type AS ENUM ('Distance_Metres', 'Duration_Seconds', 'Sessions', 'Custom');
CREATE TYPE medical_declaration AS ENUM ('UNKNOWN', 'NONE_KNOWN', 'PROVIDED');

CREATE TABLE users (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), name varchar(120) NOT NULL, email citext NULL, email_verified_at timestamptz NULL, minor_login_id citext NULL, password_hash text NULL, credential_version bigint NOT NULL DEFAULT 1, guardian_id uuid NULL REFERENCES users(id) ON DELETE RESTRICT, is_dependent boolean NOT NULL DEFAULT false, date_of_birth date NOT NULL, is_active boolean NOT NULL DEFAULT true, leaderboard_visible boolean NOT NULL DEFAULT true, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 CONSTRAINT users_name_valid CHECK (name = btrim(name) AND char_length(name) BETWEEN 2 AND 120), CONSTRAINT users_guardian_not_self CHECK (guardian_id IS NULL OR guardian_id <> id),
 CONSTRAINT users_credential_version_valid CHECK (credential_version > 0),
 CONSTRAINT users_identity_shape CHECK ((is_dependent AND guardian_id IS NOT NULL AND email IS NULL AND ((minor_login_id IS NULL AND password_hash IS NULL) OR (minor_login_id IS NOT NULL AND password_hash IS NOT NULL))) OR (NOT is_dependent AND guardian_id IS NULL AND email IS NOT NULL AND password_hash IS NOT NULL AND minor_login_id IS NULL))
);
CREATE UNIQUE INDEX users_email_uidx ON users (email) WHERE email IS NOT NULL;
CREATE UNIQUE INDEX users_minor_login_id_uidx ON users (minor_login_id) WHERE minor_login_id IS NOT NULL;
CREATE INDEX users_guardian_id_idx ON users (guardian_id) WHERE guardian_id IS NOT NULL;
CREATE INDEX users_active_idx ON users (is_active);
CREATE TABLE email_verification_tokens (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE, email citext NOT NULL, expires_at timestamptz NOT NULL, consumed_at timestamptz NULL, created_at timestamptz NOT NULL DEFAULT now(),
 CONSTRAINT email_verification_tokens_expiry_valid CHECK (expires_at > created_at),
 CONSTRAINT email_verification_tokens_consumption_valid CHECK (consumed_at IS NULL OR consumed_at >= created_at)
);
CREATE UNIQUE INDEX email_verification_tokens_active_user_uidx ON email_verification_tokens (user_id) WHERE consumed_at IS NULL;
CREATE INDEX email_verification_tokens_expiry_idx ON email_verification_tokens (expires_at) WHERE consumed_at IS NULL;
CREATE TABLE password_reset_tokens (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE, email citext NOT NULL, token_digest bytea NOT NULL UNIQUE, expires_at timestamptz NOT NULL, consumed_at timestamptz NULL, created_at timestamptz NOT NULL DEFAULT now(),
 CONSTRAINT password_reset_tokens_digest_valid CHECK (octet_length(token_digest) = 32),
 CONSTRAINT password_reset_tokens_expiry_valid CHECK (expires_at > created_at),
 CONSTRAINT password_reset_tokens_consumption_valid CHECK (consumed_at IS NULL OR consumed_at >= created_at)
);
CREATE UNIQUE INDEX password_reset_tokens_active_user_uidx ON password_reset_tokens (user_id) WHERE consumed_at IS NULL;
CREATE INDEX password_reset_tokens_expiry_idx ON password_reset_tokens (expires_at) WHERE consumed_at IS NULL;
CREATE TABLE email_outbox (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), message_type varchar(30) NOT NULL DEFAULT 'EMAIL_VERIFICATION', verification_token_id uuid NULL REFERENCES email_verification_tokens(id) ON DELETE CASCADE, password_reset_token_id uuid NULL REFERENCES password_reset_tokens(id) ON DELETE CASCADE, sealed_payload bytea NULL, status varchar(20) NOT NULL DEFAULT 'PENDING', attempts integer NOT NULL DEFAULT 0, next_attempt_at timestamptz NOT NULL DEFAULT now(), claimed_at timestamptz NULL, sent_at timestamptz NULL, last_error varchar(500) NULL, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 CONSTRAINT email_outbox_message_valid CHECK ((message_type = 'EMAIL_VERIFICATION' AND verification_token_id IS NOT NULL AND password_reset_token_id IS NULL AND sealed_payload IS NULL) OR (message_type = 'PASSWORD_RESET' AND verification_token_id IS NULL AND password_reset_token_id IS NOT NULL AND sealed_payload IS NOT NULL)),
 CONSTRAINT email_outbox_status_valid CHECK (status IN ('PENDING', 'SENDING', 'SENT', 'FAILED', 'CANCELLED')),
 CONSTRAINT email_outbox_attempts_valid CHECK (attempts >= 0),
 CONSTRAINT email_outbox_lifecycle_valid CHECK ((status = 'PENDING' AND claimed_at IS NULL AND sent_at IS NULL) OR (status = 'SENDING' AND claimed_at IS NOT NULL AND sent_at IS NULL) OR (status = 'SENT' AND claimed_at IS NULL AND sent_at IS NOT NULL) OR (status IN ('FAILED', 'CANCELLED') AND claimed_at IS NULL AND sent_at IS NULL))
);
CREATE UNIQUE INDEX email_outbox_verification_token_uidx ON email_outbox (verification_token_id) WHERE verification_token_id IS NOT NULL;
CREATE UNIQUE INDEX email_outbox_password_reset_token_uidx ON email_outbox (password_reset_token_id) WHERE password_reset_token_id IS NOT NULL;
CREATE INDEX email_outbox_delivery_idx ON email_outbox (next_attempt_at, created_at) WHERE status = 'PENDING';
CREATE FUNCTION issue_email_verification(p_user_id uuid, p_email citext, p_created_at timestamptz, p_expires_at timestamptz, p_throttle boolean) RETURNS uuid LANGUAGE plpgsql AS $$
DECLARE issued_id uuid;
BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended(p_user_id::text, 0));
 IF p_throttle AND EXISTS (SELECT 1 FROM email_verification_tokens WHERE user_id = p_user_id AND created_at > p_created_at - interval '1 minute') THEN
  RAISE EXCEPTION USING ERRCODE = 'P0001', MESSAGE = 'email_verification_too_soon';
 END IF;
 UPDATE email_outbox SET status = 'CANCELLED', claimed_at = NULL, updated_at = p_created_at WHERE verification_token_id IN (SELECT id FROM email_verification_tokens WHERE user_id = p_user_id AND consumed_at IS NULL) AND status IN ('PENDING', 'SENDING');
 UPDATE email_verification_tokens SET consumed_at = GREATEST(p_created_at, created_at) WHERE user_id = p_user_id AND consumed_at IS NULL;
 INSERT INTO email_verification_tokens (user_id, email, expires_at, created_at) VALUES (p_user_id, p_email, p_expires_at, p_created_at) RETURNING id INTO issued_id;
 INSERT INTO email_outbox (verification_token_id, next_attempt_at, created_at, updated_at) VALUES (issued_id, p_created_at, p_created_at, p_created_at);
 RETURN issued_id;
END;
$$;
CREATE FUNCTION issue_password_reset(p_user_id uuid, p_email citext, p_token_digest bytea, p_sealed_payload bytea, p_created_at timestamptz, p_expires_at timestamptz, p_throttle boolean) RETURNS uuid LANGUAGE plpgsql AS $$
DECLARE issued_id uuid;
BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended('password-reset:' || p_user_id::text, 0));
 IF NOT EXISTS (SELECT 1 FROM users WHERE id = p_user_id AND email = p_email AND is_active = true AND is_dependent = false) THEN
  RAISE EXCEPTION USING ERRCODE = 'P0001', MESSAGE = 'password_reset_ineligible';
 END IF;
 IF p_throttle AND EXISTS (SELECT 1 FROM password_reset_tokens WHERE user_id = p_user_id AND created_at > p_created_at - interval '1 minute') THEN
  RAISE EXCEPTION USING ERRCODE = 'P0001', MESSAGE = 'password_reset_too_soon';
 END IF;
 IF (SELECT count(*) FROM password_reset_tokens WHERE user_id = p_user_id AND created_at > p_created_at - interval '1 hour') >= 5 THEN
  RAISE EXCEPTION USING ERRCODE = 'P0001', MESSAGE = 'password_reset_limit_exceeded';
 END IF;
 UPDATE email_outbox SET status = 'CANCELLED', claimed_at = NULL, updated_at = p_created_at WHERE password_reset_token_id IN (SELECT id FROM password_reset_tokens WHERE user_id = p_user_id AND consumed_at IS NULL) AND status IN ('PENDING', 'SENDING');
 UPDATE password_reset_tokens SET consumed_at = GREATEST(p_created_at, created_at) WHERE user_id = p_user_id AND consumed_at IS NULL;
 INSERT INTO password_reset_tokens (user_id, email, token_digest, expires_at, created_at) VALUES (p_user_id, p_email, p_token_digest, p_expires_at, p_created_at) RETURNING id INTO issued_id;
 INSERT INTO email_outbox (message_type, password_reset_token_id, sealed_payload, next_attempt_at, created_at, updated_at) VALUES ('PASSWORD_RESET', issued_id, p_sealed_payload, p_created_at, p_created_at, p_created_at);
 RETURN issued_id;
END;
$$;
CREATE TABLE minor_credential_audit (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), minor_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT, guardian_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT, actor_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT, action varchar(20) NOT NULL CHECK (action IN ('ISSUED', 'RECOVERED')), issued_login_id citext NOT NULL, created_at timestamptz NOT NULL DEFAULT now());
CREATE INDEX minor_credential_audit_minor_created_idx ON minor_credential_audit (minor_user_id, created_at DESC);
CREATE TABLE platform_roles (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), code varchar(40) NOT NULL UNIQUE, name_pt varchar(120) NOT NULL, CONSTRAINT platform_roles_code_valid CHECK (code = btrim(code) AND code ~ '^[A-Z][A-Z0-9_]*$'), CONSTRAINT platform_roles_name_valid CHECK (name_pt = btrim(name_pt) AND char_length(name_pt) BETWEEN 2 AND 120));
INSERT INTO platform_roles (code, name_pt) VALUES ('ADMIN', 'Administração'), ('STAFF', 'Equipa do clube');
CREATE TABLE user_platform_roles (user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE, role_id uuid NOT NULL REFERENCES platform_roles(id) ON DELETE RESTRICT, granted_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY (user_id, role_id));
CREATE INDEX user_platform_roles_role_idx ON user_platform_roles (role_id, user_id);
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
CREATE TRIGGER email_verification_tokens_active_subject_attachment BEFORE INSERT OR UPDATE OF user_id ON email_verification_tokens FOR EACH ROW EXECUTE FUNCTION require_active_privacy_attachment_subject();
CREATE TRIGGER password_reset_tokens_active_subject_attachment BEFORE INSERT OR UPDATE OF user_id ON password_reset_tokens FOR EACH ROW EXECUTE FUNCTION require_active_privacy_attachment_subject();
CREATE TRIGGER user_platform_roles_active_subject_attachment BEFORE INSERT OR UPDATE OF user_id ON user_platform_roles FOR EACH ROW EXECUTE FUNCTION require_active_privacy_attachment_subject();
CREATE TABLE equipment (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), asset_tag varchar(40) NOT NULL UNIQUE, name varchar(120) NOT NULL, type equipment_type NOT NULL, status equipment_status NOT NULL DEFAULT 'Operational', notes text NOT NULL DEFAULT '', image_object_key varchar(512) NULL, image_content_type varchar(100) NULL, image_size_bytes bigint NULL, image_upload_intent_id uuid NULL, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), CONSTRAINT equipment_asset_tag_valid CHECK (asset_tag = btrim(asset_tag) AND char_length(asset_tag) BETWEEN 2 AND 40), CONSTRAINT equipment_name_valid CHECK (name = btrim(name) AND char_length(name) BETWEEN 2 AND 120), CONSTRAINT equipment_notes_valid CHECK (char_length(notes) <= 4000), CONSTRAINT equipment_image_metadata_complete CHECK ((image_object_key IS NULL AND image_content_type IS NULL AND image_size_bytes IS NULL) OR (image_object_key IS NOT NULL AND image_content_type IS NOT NULL AND image_size_bytes IS NOT NULL)), CONSTRAINT equipment_image_size_valid CHECK (image_size_bytes IS NULL OR image_size_bytes BETWEEN 1 AND 10485760));
CREATE INDEX equipment_status_type_idx ON equipment (status, type);
CREATE TABLE equipment_audit_events (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), equipment_id uuid NOT NULL REFERENCES equipment(id) ON DELETE RESTRICT, actor_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT, action varchar(20) NOT NULL CHECK (action IN ('CREATED', 'UPDATED', 'RETIRED', 'REACTIVATED')), before_state jsonb NULL, after_state jsonb NOT NULL, affected_maintenance_ids uuid[] NOT NULL DEFAULT '{}', occurred_at timestamptz NOT NULL DEFAULT now());
CREATE INDEX equipment_audit_events_equipment_occurred_idx ON equipment_audit_events (equipment_id, occurred_at DESC, id DESC);
CREATE FUNCTION prevent_equipment_audit_mutation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'equipment audit events are append-only'; END; $$;
CREATE TRIGGER equipment_audit_events_immutable_trigger BEFORE UPDATE OR DELETE ON equipment_audit_events FOR EACH ROW EXECUTE FUNCTION prevent_equipment_audit_mutation();
CREATE FUNCTION sanitize_equipment_audit_image_state() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE original_before jsonb; original_after jsonb;
BEGIN
 original_before:=NEW.before_state; original_after:=NEW.after_state;
 NEW.before_state:=CASE WHEN original_before IS NULL THEN NULL ELSE
  (original_before-'image_object_key')||jsonb_build_object('has_image',CASE
   WHEN original_before?'image_object_key' THEN original_before->'image_object_key'<>'null'::jsonb
   WHEN jsonb_typeof(original_before->'has_image')='boolean' THEN (original_before->>'has_image')::boolean
   ELSE false END) END;
 NEW.after_state:=(original_after-'image_object_key')||jsonb_build_object(
  'has_image',CASE
   WHEN original_after?'image_object_key' THEN original_after->'image_object_key'<>'null'::jsonb
   WHEN jsonb_typeof(original_after->'has_image')='boolean' THEN (original_after->>'has_image')::boolean
   ELSE false END,
  'image_changed',CASE
   WHEN original_before?'image_object_key' OR original_after?'image_object_key' THEN original_before->'image_object_key' IS DISTINCT FROM original_after->'image_object_key'
   WHEN jsonb_typeof(original_after->'image_changed')='boolean' THEN (original_after->>'image_changed')::boolean
   ELSE false END);
 RETURN NEW;
END;
$$;
CREATE TRIGGER equipment_audit_image_sanitization_trigger BEFORE INSERT ON equipment_audit_events FOR EACH ROW EXECUTE FUNCTION sanitize_equipment_audit_image_state();
CREATE TABLE repair_requests (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), idempotency_key uuid NOT NULL UNIQUE, equipment_id uuid NOT NULL REFERENCES equipment(id) ON DELETE RESTRICT, reported_by_id uuid NULL REFERENCES users(id) ON DELETE SET NULL, issue_description varchar(2000) NOT NULL, status repair_status NOT NULL DEFAULT 'Pendente', image_object_key varchar(512) NULL, image_content_type varchar(100) NULL, image_size_bytes bigint NULL, image_upload_intent_id uuid NULL, date_reported timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), resolved_at timestamptz NULL, CONSTRAINT repair_description_valid CHECK (issue_description = btrim(issue_description) AND char_length(issue_description) BETWEEN 10 AND 2000), CONSTRAINT repair_image_metadata_complete CHECK ((image_object_key IS NULL AND image_content_type IS NULL AND image_size_bytes IS NULL) OR (image_object_key IS NOT NULL AND image_content_type IS NOT NULL AND image_size_bytes IS NOT NULL)), CONSTRAINT repair_image_size_valid CHECK (image_size_bytes IS NULL OR image_size_bytes BETWEEN 1 AND 10485760), CONSTRAINT repair_resolution_valid CHECK ((status = 'Resolvido' AND resolved_at IS NOT NULL) OR (status <> 'Resolvido' AND resolved_at IS NULL)));
CREATE INDEX repair_status_date_idx ON repair_requests (status, date_reported DESC); CREATE INDEX repair_equipment_id_idx ON repair_requests (equipment_id); CREATE INDEX repair_reported_by_id_idx ON repair_requests (reported_by_id);
CREATE TABLE consent_forms (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE, granted_by_user_id uuid NULL REFERENCES users(id) ON DELETE SET NULL, consent_type consent_type NOT NULL, document_version varchar(40) NOT NULL, document_sha256 char(64) NOT NULL, is_accepted boolean NOT NULL, date_signed timestamptz NOT NULL DEFAULT now(), ip_address inet NULL, user_agent varchar(512) NOT NULL DEFAULT '', CONSTRAINT consent_version_valid CHECK (document_version = btrim(document_version) AND char_length(document_version) BETWEEN 1 AND 40), CONSTRAINT consent_sha256_valid CHECK (document_sha256 ~ '^[0-9a-f]{64}$'), CONSTRAINT consent_accepted_true CHECK (is_accepted), CONSTRAINT consent_user_agent_valid CHECK (char_length(user_agent) <= 512));
CREATE INDEX consent_user_type_date_idx ON consent_forms (user_id, consent_type, date_signed DESC);
CREATE INDEX consent_user_type_version_idx ON consent_forms (user_id, consent_type, document_version, document_sha256, date_signed DESC);
CREATE TABLE member_profiles (
 user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 phone varchar(32) NOT NULL DEFAULT '', address_line1 varchar(200) NOT NULL DEFAULT '', address_line2 varchar(200) NOT NULL DEFAULT '', postcode varchar(20) NOT NULL DEFAULT '', locality varchar(120) NOT NULL DEFAULT '', country_code varchar(2) NOT NULL DEFAULT '', nationality_code varchar(2) NOT NULL DEFAULT '',
 club_member_number varchar(60) NULL, federation_licence_number varchar(60) NULL,
 emergency_contact_name varchar(120) NOT NULL DEFAULT '', emergency_contact_relationship varchar(80) NOT NULL DEFAULT '', emergency_contact_phone varchar(32) NOT NULL DEFAULT '', emergency_contact_alternate_phone varchar(32) NOT NULL DEFAULT '',
 medical_declaration medical_declaration NOT NULL DEFAULT 'UNKNOWN', allergies varchar(2000) NOT NULL DEFAULT '', medical_conditions varchar(2000) NOT NULL DEFAULT '', medication varchar(2000) NOT NULL DEFAULT '', activity_restrictions varchar(2000) NOT NULL DEFAULT '', medical_notes varchar(2000) NOT NULL DEFAULT '',
 photo_object_key varchar(512) NULL, photo_content_type varchar(100) NULL, photo_size_bytes bigint NULL, photo_consent_form_id uuid NULL REFERENCES consent_forms(id) ON DELETE RESTRICT, photo_upload_intent_id uuid NULL,
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 CONSTRAINT member_profiles_phone_valid CHECK (phone = '' OR (phone ~ '^[+]?[0-9][0-9 ().-]*[0-9]$' AND char_length(regexp_replace(phone, '[^0-9]', '', 'g')) BETWEEN 7 AND 15)),
 CONSTRAINT member_profiles_emergency_phone_valid CHECK (emergency_contact_phone = '' OR (emergency_contact_phone ~ '^[+]?[0-9][0-9 ().-]*[0-9]$' AND char_length(regexp_replace(emergency_contact_phone, '[^0-9]', '', 'g')) BETWEEN 7 AND 15)),
 CONSTRAINT member_profiles_emergency_alternate_phone_valid CHECK (emergency_contact_alternate_phone = '' OR (emergency_contact_alternate_phone ~ '^[+]?[0-9][0-9 ().-]*[0-9]$' AND char_length(regexp_replace(emergency_contact_alternate_phone, '[^0-9]', '', 'g')) BETWEEN 7 AND 15)),
 CONSTRAINT member_profiles_address_valid CHECK (char_length(address_line1) <= 200 AND char_length(address_line2) <= 200 AND char_length(postcode) <= 20 AND char_length(locality) <= 120),
 CONSTRAINT member_profiles_country_valid CHECK (country_code = '' OR country_code ~ '^[A-Z]{2}$'),
 CONSTRAINT member_profiles_nationality_valid CHECK (nationality_code = '' OR nationality_code ~ '^[A-Z]{2}$'),
 CONSTRAINT member_profiles_emergency_complete CHECK ((emergency_contact_name = '' AND emergency_contact_relationship = '' AND emergency_contact_phone = '' AND emergency_contact_alternate_phone = '') OR (emergency_contact_name <> '' AND emergency_contact_relationship <> '' AND emergency_contact_phone <> '')),
 CONSTRAINT member_profiles_medical_complete CHECK (medical_declaration <> 'PROVIDED' OR allergies <> '' OR medical_conditions <> '' OR medication <> '' OR activity_restrictions <> '' OR medical_notes <> ''),
 CONSTRAINT member_profiles_photo_complete CHECK ((photo_object_key IS NULL AND photo_content_type IS NULL AND photo_size_bytes IS NULL AND photo_consent_form_id IS NULL) OR (photo_object_key IS NOT NULL AND photo_content_type IS NOT NULL AND photo_size_bytes IS NOT NULL AND photo_consent_form_id IS NOT NULL)),
 CONSTRAINT member_profiles_photo_size_valid CHECK (photo_size_bytes IS NULL OR photo_size_bytes BETWEEN 1 AND 10485760)
);
CREATE UNIQUE INDEX member_profiles_club_number_uidx ON member_profiles (club_member_number) WHERE club_member_number IS NOT NULL;
CREATE UNIQUE INDEX member_profiles_federation_number_uidx ON member_profiles (federation_licence_number) WHERE federation_licence_number IS NOT NULL;
CREATE TABLE member_profile_audit_events (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), actor_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT, subject_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 action varchar(40) NOT NULL CHECK (action IN ('SENSITIVE_VIEW', 'PROFILE_UPDATED', 'IDENTITY_UPDATED', 'PHOTO_UPLOADED', 'PHOTO_REPLACED', 'PHOTO_REMOVED')),
 changed_fields text[] NOT NULL DEFAULT '{}', occurred_at timestamptz NOT NULL DEFAULT now(),
 CONSTRAINT member_profile_audit_changed_fields_valid CHECK (array_position(changed_fields, NULL) IS NULL)
);
CREATE INDEX member_profile_audit_subject_occurred_idx ON member_profile_audit_events (subject_user_id, occurred_at DESC, id DESC);
CREATE FUNCTION prevent_member_profile_audit_mutation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'member profile audit events are append-only'; END; $$;
CREATE TRIGGER member_profile_audit_events_immutable_trigger BEFORE UPDATE OR DELETE ON member_profile_audit_events FOR EACH ROW EXECUTE FUNCTION prevent_member_profile_audit_mutation();
CREATE TABLE whatsapp_groups (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), name varchar(120) NOT NULL, discipline varchar(80) NOT NULL, programme_id uuid NULL, url text NOT NULL, is_active boolean NOT NULL DEFAULT true, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), CONSTRAINT whatsapp_name_valid CHECK (name = btrim(name) AND char_length(name) BETWEEN 2 AND 120), CONSTRAINT whatsapp_discipline_valid CHECK (discipline = btrim(discipline) AND char_length(discipline) BETWEEN 2 AND 80), CONSTRAINT whatsapp_url_valid CHECK (url LIKE 'https://chat.whatsapp.com/%'), CONSTRAINT whatsapp_group_unique UNIQUE NULLS NOT DISTINCT (name, programme_id));
CREATE INDEX whatsapp_programme_active_idx ON whatsapp_groups (programme_id, is_active);
CREATE TABLE training_logs (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE, occurred_at timestamptz NOT NULL, duration_seconds integer NOT NULL, distance_metres integer NOT NULL, notes varchar(2000) NOT NULL DEFAULT '', created_at timestamptz NOT NULL DEFAULT now(), CONSTRAINT training_duration_valid CHECK (duration_seconds BETWEEN 60 AND 86400), CONSTRAINT training_distance_valid CHECK (distance_metres BETWEEN 0 AND 200000), CONSTRAINT training_notes_valid CHECK (char_length(notes) <= 2000));
CREATE INDEX training_user_occurred_idx ON training_logs (user_id, occurred_at DESC);
CREATE TABLE performance_metrics (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE, metric_type metric_type NOT NULL, label_pt varchar(100) NOT NULL, value numeric(12,2) NOT NULL, unit_pt varchar(30) NOT NULL, measured_at timestamptz NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), CONSTRAINT performance_label_valid CHECK (label_pt = btrim(label_pt) AND char_length(label_pt) BETWEEN 1 AND 100), CONSTRAINT performance_unit_valid CHECK (char_length(unit_pt) <= 30));
CREATE INDEX performance_user_measured_idx ON performance_metrics (user_id, measured_at DESC);
CREATE TABLE news_items (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), title_pt varchar(180) NOT NULL, summary_pt varchar(1000) NOT NULL, url text NULL, published_at timestamptz NOT NULL, is_published boolean NOT NULL DEFAULT false, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), CONSTRAINT news_title_valid CHECK (title_pt = btrim(title_pt) AND char_length(title_pt) BETWEEN 2 AND 180), CONSTRAINT news_summary_valid CHECK (summary_pt = btrim(summary_pt) AND char_length(summary_pt) BETWEEN 2 AND 1000), CONSTRAINT news_url_valid CHECK (url IS NULL OR url ~ '^https://'));
CREATE INDEX news_published_date_idx ON news_items (is_published, published_at DESC);
CREATE TABLE maintenance_tasks (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), equipment_id uuid NOT NULL REFERENCES equipment(id) ON DELETE RESTRICT, scheduled_for timestamptz NOT NULL, description varchar(2000) NOT NULL, status maintenance_status NOT NULL DEFAULT 'Scheduled', created_by_id uuid NULL REFERENCES users(id) ON DELETE SET NULL, completed_at timestamptz NULL, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), CONSTRAINT maintenance_description_valid CHECK (description = btrim(description) AND char_length(description) BETWEEN 10 AND 2000), CONSTRAINT maintenance_completion_valid CHECK ((status = 'Completed' AND completed_at IS NOT NULL) OR (status <> 'Completed' AND completed_at IS NULL)));
CREATE INDEX maintenance_status_scheduled_idx ON maintenance_tasks (status, scheduled_for); CREATE INDEX maintenance_equipment_id_idx ON maintenance_tasks (equipment_id);
CREATE TABLE sessions (token text PRIMARY KEY, data bytea NOT NULL, expiry timestamptz NOT NULL, user_id uuid NULL REFERENCES users(id) ON DELETE RESTRICT, subject_indexed boolean NOT NULL DEFAULT false, CHECK (user_id IS NULL OR subject_indexed)); CREATE INDEX sessions_expiry_idx ON sessions (expiry); CREATE INDEX sessions_user_expiry_idx ON sessions (user_id, expiry) WHERE subject_indexed AND user_id IS NOT NULL; CREATE INDEX sessions_unindexed_expiry_idx ON sessions (expiry) WHERE NOT subject_indexed;
CREATE TRIGGER sessions_active_subject_attachment BEFORE INSERT OR UPDATE OF user_id ON sessions FOR EACH ROW EXECUTE FUNCTION require_active_privacy_attachment_subject();
CREATE TABLE seasons (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), code varchar(20) NOT NULL UNIQUE, name varchar(120) NOT NULL, starts_on date NOT NULL, ends_on date NOT NULL, is_current boolean NOT NULL DEFAULT false, created_at timestamptz NOT NULL DEFAULT now(), CONSTRAINT seasons_code_valid CHECK (code = btrim(code) AND char_length(code) BETWEEN 1 AND 20), CONSTRAINT seasons_name_valid CHECK (name = btrim(name) AND char_length(name) BETWEEN 2 AND 120), CONSTRAINT seasons_dates_valid CHECK (starts_on <= ends_on)); CREATE UNIQUE INDEX seasons_current_uidx ON seasons (is_current) WHERE is_current;
CREATE TABLE programmes (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), code varchar(40) NOT NULL UNIQUE, name_pt varchar(120) NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), CONSTRAINT programmes_code_valid CHECK (code = btrim(code) AND code ~ '^[A-Za-z][A-Za-z0-9_]*$'), CONSTRAINT programmes_name_valid CHECK (name_pt = btrim(name_pt) AND char_length(name_pt) BETWEEN 2 AND 120));
INSERT INTO programmes (code, name_pt) VALUES ('Leisure', 'Lazer'), ('Initiation', 'Iniciação'), ('Competition', 'Competição'), ('Kayak_Polo', 'Kayak Polo');
ALTER TABLE whatsapp_groups ADD CONSTRAINT whatsapp_groups_programme_fk FOREIGN KEY (programme_id) REFERENCES programmes(id) ON DELETE RESTRICT;
CREATE TABLE modalities (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), code varchar(20) NOT NULL UNIQUE, name_pt varchar(120) NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), CONSTRAINT modalities_code_valid CHECK (code = btrim(code) AND code ~ '^[A-Z][A-Z0-9]*$'), CONSTRAINT modalities_name_valid CHECK (name_pt = btrim(name_pt) AND char_length(name_pt) BETWEEN 2 AND 120));
INSERT INTO modalities (code, name_pt) VALUES ('K1', 'Caiaque individual'), ('K2', 'Caiaque duplo'), ('K4', 'Caiaque quádruplo'), ('C1', 'Canoa individual'), ('C2', 'Canoa dupla'), ('C4', 'Canoa quádrupla'), ('SUP', 'Stand up paddle');
CREATE TABLE competition_categories (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), season_id uuid NOT NULL REFERENCES seasons(id) ON DELETE RESTRICT, programme_id uuid NOT NULL REFERENCES programmes(id) ON DELETE RESTRICT, code varchar(40) NOT NULL, name_pt varchar(120) NOT NULL, birth_date_from date NULL, birth_date_to date NULL, approved_by_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT, approved_at timestamptz NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), CONSTRAINT competition_categories_code_valid CHECK (code = btrim(code) AND code ~ '^[A-Za-z][A-Za-z0-9_]*$'), CONSTRAINT competition_categories_name_valid CHECK (name_pt = btrim(name_pt) AND char_length(name_pt) BETWEEN 2 AND 120), CONSTRAINT competition_categories_birth_range_valid CHECK (birth_date_from IS NULL OR birth_date_to IS NULL OR birth_date_from <= birth_date_to), CONSTRAINT competition_categories_season_programme_code_unique UNIQUE (season_id, programme_id, code), CONSTRAINT competition_categories_id_season_programme_unique UNIQUE (id, season_id, programme_id));
CREATE TABLE teams (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), season_id uuid NOT NULL REFERENCES seasons(id) ON DELETE RESTRICT, programme_id uuid NOT NULL REFERENCES programmes(id) ON DELETE RESTRICT, code varchar(40) NOT NULL, name varchar(120) NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), CONSTRAINT teams_code_valid CHECK (code = btrim(code) AND code ~ '^[A-Za-z][A-Za-z0-9_]*$'), CONSTRAINT teams_name_valid CHECK (name = btrim(name) AND char_length(name) BETWEEN 2 AND 120), CONSTRAINT teams_season_programme_code_unique UNIQUE (season_id, programme_id, code), CONSTRAINT teams_id_season_programme_unique UNIQUE (id, season_id, programme_id));
CREATE TABLE user_memberships (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE, season_id uuid NOT NULL REFERENCES seasons(id) ON DELETE RESTRICT, programme_id uuid NOT NULL REFERENCES programmes(id) ON DELETE RESTRICT, team_id uuid NULL, competition_category_id uuid NULL, starts_on date NOT NULL, ends_on date NULL, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), CONSTRAINT user_memberships_dates_valid CHECK (ends_on IS NULL OR starts_on <= ends_on), CONSTRAINT user_memberships_user_season_programme_unique UNIQUE (user_id, season_id, programme_id), CONSTRAINT user_memberships_team_same_season_programme_fk FOREIGN KEY (team_id, season_id, programme_id) REFERENCES teams(id, season_id, programme_id) ON DELETE RESTRICT, CONSTRAINT user_memberships_category_same_season_programme_fk FOREIGN KEY (competition_category_id, season_id, programme_id) REFERENCES competition_categories(id, season_id, programme_id) ON DELETE RESTRICT);
CREATE INDEX user_memberships_active_user_idx ON user_memberships (user_id, starts_on, ends_on); CREATE INDEX user_memberships_team_idx ON user_memberships (team_id) WHERE team_id IS NOT NULL;
CREATE TABLE membership_modalities (membership_id uuid NOT NULL REFERENCES user_memberships(id) ON DELETE CASCADE, modality_id uuid NOT NULL REFERENCES modalities(id) ON DELETE RESTRICT, created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY (membership_id, modality_id));
CREATE TYPE event_response_status AS ENUM ('Going', 'NotGoing', 'Waitlisted');
CREATE TABLE events (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), title varchar(180) NOT NULL, description varchar(4000) NOT NULL DEFAULT '', event_type varchar(20) NOT NULL DEFAULT 'GENERAL', starts_at timestamptz NOT NULL, ends_at timestamptz NOT NULL, response_deadline timestamptz NULL, capacity integer NULL, status varchar(20) NOT NULL DEFAULT 'ACTIVE', cancelled_at timestamptz NULL, cancelled_by_id uuid NULL REFERENCES users(id) ON DELETE RESTRICT, cancellation_reason varchar(500) NULL, created_by_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), CONSTRAINT events_title_valid CHECK (title = btrim(title) AND char_length(title) BETWEEN 2 AND 180), CONSTRAINT events_description_valid CHECK (description = btrim(description) AND char_length(description) <= 4000), CONSTRAINT events_type_valid CHECK (event_type IN ('GENERAL', 'COMPETITION')), CONSTRAINT events_times_valid CHECK (starts_at < ends_at), CONSTRAINT events_deadline_valid CHECK (response_deadline IS NULL OR response_deadline <= starts_at), CONSTRAINT events_capacity_valid CHECK (capacity IS NULL OR capacity > 0), CONSTRAINT events_status_valid CHECK (status IN ('ACTIVE', 'CANCELLED')), CONSTRAINT events_cancellation_reason_valid CHECK (cancellation_reason IS NULL OR (cancellation_reason = btrim(cancellation_reason) AND char_length(cancellation_reason) BETWEEN 2 AND 500)), CONSTRAINT events_cancellation_complete CHECK ((status = 'ACTIVE' AND cancelled_at IS NULL AND cancelled_by_id IS NULL AND cancellation_reason IS NULL) OR (status = 'CANCELLED' AND cancelled_at IS NOT NULL AND cancelled_by_id IS NOT NULL AND cancellation_reason IS NOT NULL))); CREATE INDEX events_starts_at_idx ON events (starts_at); CREATE INDEX events_status_starts_idx ON events (status, starts_at);
CREATE TABLE event_audiences (event_id uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE, programme_id uuid NOT NULL REFERENCES programmes(id) ON DELETE RESTRICT, PRIMARY KEY (event_id, programme_id)); CREATE INDEX event_audiences_programme_idx ON event_audiences (programme_id, event_id);
CREATE TABLE event_responses (event_id uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE, user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE, status event_response_status NOT NULL, responded_by_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT, responded_at timestamptz NOT NULL DEFAULT now(), checked_in_at timestamptz NULL, checked_in_by_id uuid NULL REFERENCES users(id) ON DELETE RESTRICT, PRIMARY KEY (event_id, user_id), CONSTRAINT event_responses_checkin_valid CHECK ((checked_in_at IS NULL AND checked_in_by_id IS NULL) OR (checked_in_at IS NOT NULL AND checked_in_by_id IS NOT NULL))); CREATE INDEX event_responses_event_status_idx ON event_responses (event_id, status);
CREATE TABLE training_groups (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), name varchar(120) NOT NULL, programme_id uuid NULL REFERENCES programmes(id) ON DELETE RESTRICT, team_id uuid NULL REFERENCES teams(id) ON DELETE RESTRICT, created_by_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), CONSTRAINT training_groups_name_valid CHECK (name = btrim(name) AND char_length(name) BETWEEN 2 AND 120), CONSTRAINT training_groups_scope_valid CHECK (num_nonnulls(programme_id, team_id) = 1)); CREATE INDEX training_groups_programme_idx ON training_groups (programme_id) WHERE programme_id IS NOT NULL; CREATE INDEX training_groups_team_idx ON training_groups (team_id) WHERE team_id IS NOT NULL;
CREATE TABLE training_group_members (group_id uuid NOT NULL REFERENCES training_groups(id) ON DELETE CASCADE, membership_id uuid NOT NULL REFERENCES user_memberships(id) ON DELETE RESTRICT, added_by_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT, added_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY (group_id, membership_id)); CREATE INDEX training_group_members_membership_idx ON training_group_members (membership_id, group_id);
CREATE TABLE training_cycles (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), training_group_id uuid NOT NULL REFERENCES training_groups(id) ON DELETE RESTRICT,
 season_id uuid NOT NULL REFERENCES seasons(id) ON DELETE RESTRICT, parent_cycle_id uuid NULL,
 name varchar(180) NOT NULL, level_label varchar(80) NOT NULL DEFAULT '', goals varchar(4000) NOT NULL DEFAULT '', phase_focus_notes varchar(4000) NOT NULL DEFAULT '',
 version integer NOT NULL DEFAULT 1, created_by_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 updated_by_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 CONSTRAINT training_cycles_name_valid CHECK (name = btrim(name) AND char_length(name) BETWEEN 2 AND 180),
 CONSTRAINT training_cycles_level_valid CHECK (level_label = btrim(level_label) AND char_length(level_label) <= 80),
 CONSTRAINT training_cycles_goals_valid CHECK (goals = btrim(goals) AND char_length(goals) <= 4000),
 CONSTRAINT training_cycles_focus_valid CHECK (phase_focus_notes = btrim(phase_focus_notes) AND char_length(phase_focus_notes) <= 4000),
 CONSTRAINT training_cycles_version_valid CHECK (version > 0), CONSTRAINT training_cycles_parent_valid CHECK (parent_cycle_id IS NULL OR parent_cycle_id <> id),
 CONSTRAINT training_cycles_scope_unique UNIQUE (id, training_group_id, season_id),
 CONSTRAINT training_cycles_parent_scope_fk FOREIGN KEY (parent_cycle_id, training_group_id, season_id) REFERENCES training_cycles(id, training_group_id, season_id) ON DELETE RESTRICT
);
CREATE INDEX training_cycles_group_season_idx ON training_cycles (training_group_id, season_id, updated_at DESC);
CREATE INDEX training_cycles_parent_idx ON training_cycles (parent_cycle_id) WHERE parent_cycle_id IS NOT NULL;
CREATE TABLE training_cycle_competition_targets (
 cycle_id uuid NOT NULL REFERENCES training_cycles(id) ON DELETE CASCADE, event_id uuid NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
 notes varchar(1000) NOT NULL DEFAULT '', added_by_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT, created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (cycle_id, event_id), CONSTRAINT training_cycle_targets_notes_valid CHECK (notes = btrim(notes) AND char_length(notes) <= 1000)
);
CREATE INDEX training_cycle_targets_event_idx ON training_cycle_competition_targets (event_id, cycle_id);
CREATE TABLE training_plans (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), title varchar(180) NOT NULL, description varchar(4000) NOT NULL DEFAULT '', programme_id uuid NULL REFERENCES programmes(id) ON DELETE RESTRICT, team_id uuid NULL REFERENCES teams(id) ON DELETE RESTRICT, training_group_id uuid NULL REFERENCES training_groups(id) ON DELETE RESTRICT, season_id uuid NULL REFERENCES seasons(id) ON DELETE RESTRICT, cycle_id uuid NULL, week_start date NULL, planned_load_percentage smallint NULL, created_by_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), CONSTRAINT training_plans_title_valid CHECK (title = btrim(title) AND char_length(title) BETWEEN 2 AND 180), CONSTRAINT training_plans_description_valid CHECK (description = btrim(description) AND char_length(description) <= 4000), CONSTRAINT training_plans_scope_valid CHECK (programme_id IS NOT NULL OR team_id IS NOT NULL), CONSTRAINT training_plans_week_valid CHECK ((training_group_id IS NULL AND week_start IS NULL AND season_id IS NULL AND cycle_id IS NULL) OR (training_group_id IS NOT NULL AND week_start IS NOT NULL AND season_id IS NOT NULL AND extract(isodow FROM week_start) = 1)), CONSTRAINT training_plans_cycle_scope_fk FOREIGN KEY (cycle_id, training_group_id, season_id) REFERENCES training_cycles(id, training_group_id, season_id) ON DELETE RESTRICT, CONSTRAINT training_plans_planned_load_percentage_valid CHECK (planned_load_percentage IS NULL OR planned_load_percentage BETWEEN 0 AND 100), CONSTRAINT training_plans_group_week_unique UNIQUE (training_group_id, week_start), CONSTRAINT training_plans_id_group_season_unique UNIQUE (id, training_group_id, season_id)); CREATE INDEX training_plans_programme_idx ON training_plans (programme_id) WHERE programme_id IS NOT NULL; CREATE INDEX training_plans_team_idx ON training_plans (team_id) WHERE team_id IS NOT NULL; CREATE INDEX training_plans_group_week_idx ON training_plans (training_group_id, week_start DESC) WHERE training_group_id IS NOT NULL; CREATE INDEX training_plans_cycle_week_idx ON training_plans (cycle_id, week_start) WHERE cycle_id IS NOT NULL;
CREATE TYPE training_entry_kind AS ENUM ('TRAINING', 'REST', 'COMPETITION', 'LOGISTICS');
CREATE TYPE training_segment_modality AS ENUM ('WATER', 'GYM', 'RUN', 'BIKE', 'ERGOMETER', 'FLEXIBILITY', 'SPORTS_GAMES', 'OTHER');
CREATE TYPE training_block_purpose AS ENUM ('WARM_UP', 'MAIN', 'COOL_DOWN', 'TECHNIQUE', 'CUSTOM');
CREATE TABLE training_sessions (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), plan_id uuid NOT NULL REFERENCES training_plans(id) ON DELETE CASCADE, title varchar(180) NOT NULL, description varchar(4000) NOT NULL DEFAULT '', starts_at timestamptz NOT NULL, ends_at timestamptz NOT NULL, modality_id uuid NULL REFERENCES modalities(id) ON DELETE RESTRICT, entry_kind training_entry_kind NOT NULL DEFAULT 'TRAINING', status varchar(20) NOT NULL DEFAULT 'ACTIVE', cancelled_at timestamptz NULL, cancelled_by_id uuid NULL REFERENCES users(id) ON DELETE RESTRICT, cancellation_reason varchar(500) NULL, created_by_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), CONSTRAINT training_sessions_title_valid CHECK (title = btrim(title) AND char_length(title) BETWEEN 2 AND 180), CONSTRAINT training_sessions_description_valid CHECK (description = btrim(description) AND char_length(description) <= 4000), CONSTRAINT training_sessions_times_valid CHECK (starts_at < ends_at), CONSTRAINT training_sessions_status_valid CHECK (status IN ('ACTIVE', 'CANCELLED')), CONSTRAINT training_sessions_cancellation_reason_valid CHECK (cancellation_reason IS NULL OR (cancellation_reason = btrim(cancellation_reason) AND char_length(cancellation_reason) BETWEEN 2 AND 500)), CONSTRAINT training_sessions_cancellation_complete CHECK ((status = 'ACTIVE' AND cancelled_at IS NULL AND cancelled_by_id IS NULL AND cancellation_reason IS NULL) OR (status = 'CANCELLED' AND cancelled_at IS NOT NULL AND cancelled_by_id IS NOT NULL AND cancellation_reason IS NOT NULL))); CREATE INDEX training_sessions_plan_starts_idx ON training_sessions (plan_id, starts_at); CREATE INDEX training_sessions_status_starts_idx ON training_sessions (status, starts_at);
CREATE TABLE training_session_segments (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), session_id uuid NOT NULL REFERENCES training_sessions(id) ON DELETE CASCADE, position integer NOT NULL, modality training_segment_modality NOT NULL, title varchar(120) NOT NULL DEFAULT '', location varchar(180) NOT NULL DEFAULT '', planned_duration_minutes integer NULL, planned_start_offset_minutes integer NULL, transition_duration_minutes integer NULL, equipment_notes varchar(1000) NOT NULL DEFAULT '', created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), CONSTRAINT training_session_segments_position_valid CHECK (position > 0), CONSTRAINT training_session_segments_title_valid CHECK (title = btrim(title) AND char_length(title) <= 120), CONSTRAINT training_session_segments_location_valid CHECK (location = btrim(location) AND char_length(location) <= 180), CONSTRAINT training_session_segments_duration_valid CHECK (planned_duration_minutes IS NULL OR planned_duration_minutes BETWEEN 1 AND 1440), CONSTRAINT training_session_segments_start_offset_valid CHECK (planned_start_offset_minutes IS NULL OR planned_start_offset_minutes BETWEEN 0 AND 1440), CONSTRAINT training_session_segments_transition_valid CHECK (transition_duration_minutes IS NULL OR transition_duration_minutes BETWEEN 1 AND 1440), CONSTRAINT training_session_segments_equipment_notes_valid CHECK (equipment_notes = btrim(equipment_notes) AND char_length(equipment_notes) <= 1000), CONSTRAINT training_session_segments_position_unique UNIQUE (session_id, position)); CREATE INDEX training_session_segments_session_idx ON training_session_segments (session_id, position);
CREATE TABLE training_segment_blocks (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), segment_id uuid NOT NULL REFERENCES training_session_segments(id) ON DELETE CASCADE, position integer NOT NULL, purpose training_block_purpose NOT NULL, title varchar(120) NOT NULL DEFAULT '', instructions varchar(4000) NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), CONSTRAINT training_segment_blocks_position_valid CHECK (position > 0), CONSTRAINT training_segment_blocks_title_valid CHECK (title = btrim(title) AND char_length(title) <= 120), CONSTRAINT training_segment_blocks_instructions_valid CHECK (instructions = btrim(instructions) AND char_length(instructions) BETWEEN 2 AND 4000), CONSTRAINT training_segment_blocks_position_unique UNIQUE (segment_id, position)); CREATE INDEX training_segment_blocks_segment_idx ON training_segment_blocks (segment_id, position);
CREATE TYPE gym_block_structure AS ENUM ('STRAIGHT_SETS', 'CIRCUIT', 'SUPERSET');
CREATE TYPE training_objective AS ENUM ('MOBILITY', 'ACTIVATION', 'MAX_STRENGTH_HYPERTROPHY', 'MAX_STRENGTH_NEURAL', 'EXPLOSIVE_STRENGTH', 'STRENGTH_ENDURANCE', 'TECHNIQUE', 'CORE', 'CUSTOM');
CREATE TYPE gym_resistance_kind AS ENUM ('KILOGRAMS', 'PERCENT_1RM', 'BODY_WEIGHT', 'BAND', 'RPE', 'RIR', 'COACH_INSTRUCTION');
CREATE TYPE gym_execution_intent AS ENUM ('CONTROLLED', 'EXPLOSIVE', 'MAXIMUM_VELOCITY', 'ISOMETRIC', 'CUSTOM');
CREATE TABLE gym_block_prescriptions (block_id uuid PRIMARY KEY REFERENCES training_segment_blocks(id) ON DELETE CASCADE, structure gym_block_structure NOT NULL, objective training_objective NOT NULL, rounds integer NOT NULL DEFAULT 1, round_recovery_seconds integer NULL, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), CONSTRAINT gym_block_rounds_valid CHECK (rounds BETWEEN 1 AND 100), CONSTRAINT gym_block_recovery_valid CHECK (round_recovery_seconds IS NULL OR round_recovery_seconds BETWEEN 1 AND 86400));
CREATE TABLE gym_exercises (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), block_id uuid NOT NULL REFERENCES gym_block_prescriptions(block_id) ON DELETE CASCADE, position integer NOT NULL, name varchar(180) NOT NULL, sets integer NULL, repetitions integer NULL, duration_seconds integer NULL, distance_metres integer NULL, recovery_seconds integer NULL, resistance_kind gym_resistance_kind NULL, resistance_value double precision NULL, resistance_text varchar(180) NULL, execution_intent gym_execution_intent NULL, tempo varchar(30) NULL, notes varchar(1000) NOT NULL DEFAULT '', created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), CONSTRAINT gym_exercises_position_valid CHECK (position > 0), CONSTRAINT gym_exercises_name_valid CHECK (name = btrim(name) AND char_length(name) BETWEEN 2 AND 180), CONSTRAINT gym_exercises_sets_valid CHECK (sets IS NULL OR sets BETWEEN 1 AND 100), CONSTRAINT gym_exercises_repetitions_valid CHECK (repetitions IS NULL OR repetitions BETWEEN 1 AND 10000), CONSTRAINT gym_exercises_duration_valid CHECK (duration_seconds IS NULL OR duration_seconds BETWEEN 1 AND 86400), CONSTRAINT gym_exercises_distance_valid CHECK (distance_metres IS NULL OR distance_metres BETWEEN 1 AND 100000), CONSTRAINT gym_exercises_recovery_valid CHECK (recovery_seconds IS NULL OR recovery_seconds BETWEEN 1 AND 86400), CONSTRAINT gym_exercises_prescription_valid CHECK (num_nonnulls(repetitions, duration_seconds, distance_metres) > 0), CONSTRAINT gym_exercises_resistance_valid CHECK ((resistance_kind IS NULL AND resistance_value IS NULL AND resistance_text IS NULL) OR (resistance_kind IN ('KILOGRAMS', 'PERCENT_1RM', 'RPE', 'RIR') AND resistance_value IS NOT NULL AND resistance_text IS NULL) OR (resistance_kind = 'BODY_WEIGHT' AND resistance_value IS NULL AND resistance_text IS NULL) OR (resistance_kind IN ('BAND', 'COACH_INSTRUCTION') AND resistance_value IS NULL AND resistance_text = btrim(resistance_text) AND char_length(resistance_text) BETWEEN 1 AND 180)), CONSTRAINT gym_exercises_resistance_range_valid CHECK ((resistance_kind <> 'PERCENT_1RM' OR resistance_value BETWEEN 0.01 AND 200) AND (resistance_kind <> 'RPE' OR resistance_value BETWEEN 1 AND 10) AND (resistance_kind <> 'RIR' OR resistance_value BETWEEN 0 AND 20) AND (resistance_kind <> 'KILOGRAMS' OR resistance_value BETWEEN 0.01 AND 10000)), CONSTRAINT gym_exercises_tempo_valid CHECK (tempo IS NULL OR (tempo = btrim(tempo) AND char_length(tempo) BETWEEN 1 AND 30)), CONSTRAINT gym_exercises_notes_valid CHECK (notes = btrim(notes) AND char_length(notes) <= 1000), CONSTRAINT gym_exercises_position_unique UNIQUE (block_id, position)); CREATE INDEX gym_exercises_block_idx ON gym_exercises (block_id, position);
CREATE FUNCTION move_training_session_segment(p_segment_id uuid, p_direction integer) RETURNS boolean LANGUAGE plpgsql AS $$ DECLARE current_session uuid; current_position integer; target_id uuid; target_position integer; temporary_position integer; BEGIN IF p_direction NOT IN (-1, 1) THEN RETURN false; END IF; SELECT session_id, position INTO current_session, current_position FROM training_session_segments WHERE id = p_segment_id FOR UPDATE; IF current_session IS NULL THEN RETURN false; END IF; SELECT id, position INTO target_id, target_position FROM training_session_segments WHERE session_id = current_session AND position = current_position + p_direction FOR UPDATE; IF target_id IS NULL THEN RETURN false; END IF; SELECT COALESCE(max(position), 0) + 1 INTO temporary_position FROM training_session_segments WHERE session_id = current_session; UPDATE training_session_segments SET position = temporary_position, updated_at = clock_timestamp() WHERE id = p_segment_id; UPDATE training_session_segments SET position = current_position, updated_at = clock_timestamp() WHERE id = target_id; UPDATE training_session_segments SET position = target_position, updated_at = clock_timestamp() WHERE id = p_segment_id; RETURN true; END; $$;
CREATE FUNCTION move_training_segment_block(p_block_id uuid, p_direction integer) RETURNS boolean LANGUAGE plpgsql AS $$ DECLARE current_segment uuid; current_position integer; target_id uuid; target_position integer; temporary_position integer; BEGIN IF p_direction NOT IN (-1, 1) THEN RETURN false; END IF; SELECT segment_id, position INTO current_segment, current_position FROM training_segment_blocks WHERE id = p_block_id FOR UPDATE; IF current_segment IS NULL THEN RETURN false; END IF; SELECT id, position INTO target_id, target_position FROM training_segment_blocks WHERE segment_id = current_segment AND position = current_position + p_direction FOR UPDATE; IF target_id IS NULL THEN RETURN false; END IF; SELECT COALESCE(max(position), 0) + 1 INTO temporary_position FROM training_segment_blocks WHERE segment_id = current_segment; UPDATE training_segment_blocks SET position = temporary_position, updated_at = clock_timestamp() WHERE id = p_block_id; UPDATE training_segment_blocks SET position = current_position, updated_at = clock_timestamp() WHERE id = target_id; UPDATE training_segment_blocks SET position = target_position, updated_at = clock_timestamp() WHERE id = p_block_id; RETURN true; END; $$;
CREATE FUNCTION move_gym_exercise(p_exercise_id uuid, p_direction integer) RETURNS boolean LANGUAGE plpgsql AS $$ DECLARE current_block uuid; current_position integer; target_id uuid; target_position integer; temporary_position integer; BEGIN IF p_direction NOT IN (-1, 1) THEN RETURN false; END IF; SELECT block_id, position INTO current_block, current_position FROM gym_exercises WHERE id = p_exercise_id FOR UPDATE; IF current_block IS NULL THEN RETURN false; END IF; SELECT id, position INTO target_id, target_position FROM gym_exercises WHERE block_id = current_block AND position = current_position + p_direction FOR UPDATE; IF target_id IS NULL THEN RETURN false; END IF; SELECT COALESCE(max(position), 0) + 1 INTO temporary_position FROM gym_exercises WHERE block_id = current_block; UPDATE gym_exercises SET position = temporary_position, updated_at = clock_timestamp() WHERE id = p_exercise_id; UPDATE gym_exercises SET position = current_position, updated_at = clock_timestamp() WHERE id = target_id; UPDATE gym_exercises SET position = target_position, updated_at = clock_timestamp() WHERE id = p_exercise_id; RETURN true; END; $$;
CREATE TYPE water_work_method AS ENUM ('CONTINUOUS', 'INTERVALS', 'FARTLEK', 'TECHNIQUE', 'STARTS', 'RACE_SIMULATION', 'TACTICAL_DRILL', 'CUSTOM');
CREATE TYPE water_step_kind AS ENUM ('EFFORT', 'REPEAT_GROUP');
CREATE TYPE training_measure_certainty AS ENUM ('EXACT', 'ESTIMATED');
CREATE TYPE paddling_craft AS ENUM ('KAYAK', 'CANOE');
CREATE TABLE water_intensity_profiles (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), name varchar(120) NOT NULL, craft paddling_craft NOT NULL, revision integer NOT NULL, supersedes_id uuid NULL REFERENCES water_intensity_profiles(id) ON DELETE RESTRICT, notes varchar(1000) NOT NULL DEFAULT '', is_active boolean NOT NULL DEFAULT true, created_by_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT, created_at timestamptz NOT NULL DEFAULT now(), CONSTRAINT water_intensity_profiles_name_valid CHECK (name = btrim(name) AND char_length(name) BETWEEN 2 AND 120), CONSTRAINT water_intensity_profiles_revision_valid CHECK (revision > 0), CONSTRAINT water_intensity_profiles_notes_valid CHECK (notes = btrim(notes) AND char_length(notes) <= 1000), CONSTRAINT water_intensity_profiles_revision_unique UNIQUE (name, craft, revision));
CREATE UNIQUE INDEX water_intensity_profiles_active_unique ON water_intensity_profiles (name, craft) WHERE is_active;
CREATE TABLE water_intensity_zones (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), profile_id uuid NOT NULL REFERENCES water_intensity_profiles(id) ON DELETE CASCADE, position integer NOT NULL, code varchar(20) NOT NULL, label varchar(120) NOT NULL, cadence_min integer NULL, cadence_max integer NULL, meaning varchar(500) NOT NULL, CONSTRAINT water_intensity_zones_position_valid CHECK (position > 0), CONSTRAINT water_intensity_zones_code_valid CHECK (code = btrim(code) AND char_length(code) BETWEEN 1 AND 20), CONSTRAINT water_intensity_zones_label_valid CHECK (label = btrim(label) AND char_length(label) BETWEEN 2 AND 120), CONSTRAINT water_intensity_zones_cadence_valid CHECK ((cadence_min IS NULL OR cadence_min BETWEEN 0 AND 300) AND (cadence_max IS NULL OR cadence_max BETWEEN 0 AND 300) AND (cadence_min IS NULL OR cadence_max IS NULL OR cadence_min <= cadence_max)), CONSTRAINT water_intensity_zones_meaning_valid CHECK (meaning = btrim(meaning) AND char_length(meaning) BETWEEN 2 AND 500), CONSTRAINT water_intensity_zones_position_unique UNIQUE (profile_id, position), CONSTRAINT water_intensity_zones_code_unique UNIQUE (profile_id, code));
CREATE INDEX water_intensity_zones_profile_idx ON water_intensity_zones (profile_id, position);
CREATE TABLE water_block_prescriptions (block_id uuid PRIMARY KEY REFERENCES training_segment_blocks(id) ON DELETE CASCADE, method water_work_method NOT NULL, intensity_profile_id uuid NULL REFERENCES water_intensity_profiles(id) ON DELETE RESTRICT, target_distance_metres integer NULL, target_distance_certainty training_measure_certainty NULL, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), CONSTRAINT water_block_target_distance_valid CHECK (target_distance_metres IS NULL OR target_distance_metres BETWEEN 1 AND 200000), CONSTRAINT water_block_target_certainty_valid CHECK ((target_distance_metres IS NULL) = (target_distance_certainty IS NULL)));
CREATE TABLE water_work_steps (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), block_id uuid NOT NULL REFERENCES water_block_prescriptions(block_id) ON DELETE CASCADE, parent_step_id uuid NULL REFERENCES water_work_steps(id) ON DELETE CASCADE, position integer NOT NULL, kind water_step_kind NOT NULL, name varchar(180) NOT NULL, repeats integer NULL, duration_seconds integer NULL, duration_certainty training_measure_certainty NULL, distance_metres integer NULL, distance_certainty training_measure_certainty NULL, recovery_seconds integer NULL, intensity_code varchar(20) NULL, cadence_spm integer NULL, drill_focus varchar(180) NULL, drill_format varchar(180) NULL, role_notes varchar(500) NULL, instructions varchar(1000) NOT NULL DEFAULT '', created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), CONSTRAINT water_work_steps_position_valid CHECK (position > 0), CONSTRAINT water_work_steps_name_valid CHECK (name = btrim(name) AND char_length(name) BETWEEN 2 AND 180), CONSTRAINT water_work_steps_shape_valid CHECK ((kind = 'REPEAT_GROUP' AND repeats BETWEEN 1 AND 100 AND duration_seconds IS NULL AND distance_metres IS NULL) OR (kind = 'EFFORT' AND repeats IS NULL AND (duration_seconds IS NOT NULL OR distance_metres IS NOT NULL OR char_length(instructions) >= 2))), CONSTRAINT water_work_steps_duration_valid CHECK (duration_seconds IS NULL OR duration_seconds BETWEEN 1 AND 86400), CONSTRAINT water_work_steps_duration_certainty_valid CHECK ((duration_seconds IS NULL) = (duration_certainty IS NULL)), CONSTRAINT water_work_steps_distance_valid CHECK (distance_metres IS NULL OR distance_metres BETWEEN 1 AND 200000), CONSTRAINT water_work_steps_distance_certainty_valid CHECK ((distance_metres IS NULL) = (distance_certainty IS NULL)), CONSTRAINT water_work_steps_recovery_valid CHECK (recovery_seconds IS NULL OR recovery_seconds BETWEEN 1 AND 86400), CONSTRAINT water_work_steps_intensity_valid CHECK (intensity_code IS NULL OR (intensity_code = btrim(intensity_code) AND char_length(intensity_code) BETWEEN 1 AND 20)), CONSTRAINT water_work_steps_cadence_valid CHECK (cadence_spm IS NULL OR cadence_spm BETWEEN 1 AND 300), CONSTRAINT water_work_steps_drill_focus_valid CHECK (drill_focus IS NULL OR (drill_focus = btrim(drill_focus) AND char_length(drill_focus) BETWEEN 1 AND 180)), CONSTRAINT water_work_steps_drill_format_valid CHECK (drill_format IS NULL OR (drill_format = btrim(drill_format) AND char_length(drill_format) BETWEEN 1 AND 180)), CONSTRAINT water_work_steps_role_notes_valid CHECK (role_notes IS NULL OR (role_notes = btrim(role_notes) AND char_length(role_notes) BETWEEN 1 AND 500)), CONSTRAINT water_work_steps_instructions_valid CHECK (instructions = btrim(instructions) AND char_length(instructions) <= 1000), CONSTRAINT water_work_steps_position_unique UNIQUE NULLS NOT DISTINCT (block_id, parent_step_id, position));
CREATE INDEX water_work_steps_block_idx ON water_work_steps (block_id, parent_step_id, position);
CREATE FUNCTION validate_water_step_parent() RETURNS trigger LANGUAGE plpgsql AS $$ DECLARE parent_block uuid; parent_kind water_step_kind; BEGIN IF NEW.parent_step_id IS NULL THEN RETURN NEW; END IF; SELECT block_id, kind INTO parent_block, parent_kind FROM water_work_steps WHERE id = NEW.parent_step_id; IF parent_block IS DISTINCT FROM NEW.block_id OR parent_kind IS DISTINCT FROM 'REPEAT_GROUP' THEN RAISE EXCEPTION 'water step parent must be a repeat group in the same block' USING ERRCODE = '23514'; END IF; RETURN NEW; END; $$;
CREATE TRIGGER water_work_steps_parent_valid BEFORE INSERT OR UPDATE OF block_id, parent_step_id ON water_work_steps FOR EACH ROW EXECUTE FUNCTION validate_water_step_parent();
CREATE TYPE training_variation_group_kind AS ENUM ('SUBGROUP', 'CREW');
CREATE TYPE training_variation_subject_kind AS ENUM ('SEGMENT', 'BLOCK', 'WATER_STEP', 'GYM_EXERCISE');
CREATE TYPE training_variation_operation AS ENUM ('OMIT', 'REPLACE', 'ADD', 'OVERRIDE');
CREATE TABLE training_variation_groups (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), training_group_id uuid NOT NULL REFERENCES training_groups(id) ON DELETE CASCADE, name varchar(120) NOT NULL, kind training_variation_group_kind NOT NULL, craft_modality_id uuid NULL REFERENCES modalities(id) ON DELETE RESTRICT, effective_from date NOT NULL, effective_until date NULL, competition_event_id uuid NULL REFERENCES events(id) ON DELETE RESTRICT, open_ended_exception boolean NOT NULL DEFAULT false, created_by_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), CONSTRAINT training_variation_groups_name_valid CHECK (name = btrim(name) AND char_length(name) BETWEEN 2 AND 120), CONSTRAINT training_variation_groups_dates_valid CHECK (effective_until IS NULL OR effective_from <= effective_until), CONSTRAINT training_variation_groups_shape_valid CHECK ((kind = 'SUBGROUP' AND craft_modality_id IS NULL AND competition_event_id IS NULL AND NOT open_ended_exception) OR (kind = 'CREW' AND craft_modality_id IS NOT NULL AND (effective_until IS NOT NULL OR competition_event_id IS NOT NULL OR open_ended_exception))), CONSTRAINT training_variation_groups_scope_name_unique UNIQUE (training_group_id, name));
CREATE INDEX training_variation_groups_training_group_idx ON training_variation_groups (training_group_id, effective_from, effective_until);
CREATE TABLE training_variation_group_members (variation_group_id uuid NOT NULL REFERENCES training_variation_groups(id) ON DELETE CASCADE, membership_id uuid NOT NULL REFERENCES user_memberships(id) ON DELETE RESTRICT, added_by_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT, added_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY (variation_group_id, membership_id));
CREATE INDEX training_variation_group_members_membership_idx ON training_variation_group_members (membership_id, variation_group_id);
CREATE TABLE training_variations (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), plan_id uuid NOT NULL REFERENCES training_plans(id) ON DELETE CASCADE, target_membership_id uuid NULL REFERENCES user_memberships(id) ON DELETE RESTRICT, target_group_id uuid NULL REFERENCES training_variation_groups(id) ON DELETE RESTRICT, subject_kind training_variation_subject_kind NOT NULL, subject_id uuid NOT NULL, operation training_variation_operation NOT NULL, change_summary varchar(500) NOT NULL, patch jsonb NOT NULL DEFAULT '{}'::jsonb, version integer NOT NULL DEFAULT 1, is_active boolean NOT NULL DEFAULT true, retired_at timestamptz NULL, retired_by_id uuid NULL REFERENCES users(id) ON DELETE RESTRICT, created_by_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), CONSTRAINT training_variations_target_valid CHECK (num_nonnulls(target_membership_id, target_group_id) = 1), CONSTRAINT training_variations_summary_valid CHECK (change_summary = btrim(change_summary) AND char_length(change_summary) BETWEEN 2 AND 500), CONSTRAINT training_variations_patch_valid CHECK (jsonb_typeof(patch) = 'object' AND octet_length(patch::text) <= 8000), CONSTRAINT training_variations_version_valid CHECK (version > 0), CONSTRAINT training_variations_lifecycle_valid CHECK ((is_active AND retired_at IS NULL AND retired_by_id IS NULL) OR (NOT is_active AND retired_at IS NOT NULL AND retired_by_id IS NOT NULL)));
CREATE UNIQUE INDEX training_variations_active_athlete_subject_idx ON training_variations (plan_id, target_membership_id, subject_kind, subject_id) WHERE is_active AND target_membership_id IS NOT NULL;
CREATE UNIQUE INDEX training_variations_active_group_subject_idx ON training_variations (plan_id, target_group_id, subject_kind, subject_id) WHERE is_active AND target_group_id IS NOT NULL;
CREATE INDEX training_variations_plan_idx ON training_variations (plan_id, is_active, subject_kind, subject_id);
CREATE TABLE training_plan_publications (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), plan_id uuid NOT NULL REFERENCES training_plans(id) ON DELETE RESTRICT, revision integer NOT NULL, source_updated_at timestamptz NOT NULL, change_summary varchar(500) NOT NULL, supersedes_id uuid NULL REFERENCES training_plan_publications(id) ON DELETE RESTRICT, published_by_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT, published_at timestamptz NOT NULL DEFAULT now(), CONSTRAINT training_plan_publications_revision_valid CHECK (revision > 0), CONSTRAINT training_plan_publications_summary_valid CHECK (change_summary = btrim(change_summary) AND char_length(change_summary) BETWEEN 2 AND 500), CONSTRAINT training_plan_publications_revision_unique UNIQUE (plan_id, revision), CONSTRAINT training_plan_publications_source_unique UNIQUE (plan_id, source_updated_at)); CREATE INDEX training_plan_publications_plan_idx ON training_plan_publications (plan_id, revision DESC);
CREATE TABLE training_prescriptions (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), publication_id uuid NOT NULL REFERENCES training_plan_publications(id) ON DELETE RESTRICT, session_id uuid NOT NULL REFERENCES training_sessions(id) ON DELETE RESTRICT, membership_id uuid NOT NULL REFERENCES user_memberships(id) ON DELETE RESTRICT, athlete_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT, snapshot jsonb NOT NULL, snapshot_sha256 varchar(64) NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), CONSTRAINT training_prescriptions_snapshot_valid CHECK (jsonb_typeof(snapshot) = 'object' AND octet_length(snapshot::text) <= 200000), CONSTRAINT training_prescriptions_hash_valid CHECK (snapshot_sha256 ~ '^[0-9a-f]{64}$'), CONSTRAINT training_prescriptions_publication_session_membership_unique UNIQUE (publication_id, session_id, membership_id)); CREATE INDEX training_prescriptions_athlete_idx ON training_prescriptions (athlete_user_id, created_at DESC); CREATE INDEX training_prescriptions_session_idx ON training_prescriptions (session_id, created_at DESC);
CREATE FUNCTION prevent_training_publication_mutation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'training publications and prescriptions are immutable'; END; $$;
CREATE TRIGGER training_plan_publications_immutable_trigger BEFORE UPDATE OR DELETE ON training_plan_publications FOR EACH ROW EXECUTE FUNCTION prevent_training_publication_mutation(); CREATE TRIGGER training_prescriptions_immutable_trigger BEFORE UPDATE OR DELETE ON training_prescriptions FOR EACH ROW EXECUTE FUNCTION prevent_training_publication_mutation();
CREATE FUNCTION touch_structured_training_plan() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE source_id uuid;
BEGIN
 CASE TG_TABLE_NAME
  WHEN 'training_sessions' THEN source_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.plan_id ELSE NEW.plan_id END; UPDATE training_plans SET updated_at = clock_timestamp() WHERE id = source_id;
  WHEN 'training_session_segments' THEN source_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.session_id ELSE NEW.session_id END; UPDATE training_plans plan SET updated_at = clock_timestamp() FROM training_sessions session WHERE session.id = source_id AND plan.id = session.plan_id;
  WHEN 'training_segment_blocks' THEN source_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.segment_id ELSE NEW.segment_id END; UPDATE training_plans plan SET updated_at = clock_timestamp() FROM training_session_segments segment JOIN training_sessions session ON session.id = segment.session_id WHERE segment.id = source_id AND plan.id = session.plan_id;
  WHEN 'gym_block_prescriptions', 'water_block_prescriptions' THEN source_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.block_id ELSE NEW.block_id END; UPDATE training_plans plan SET updated_at = clock_timestamp() FROM training_segment_blocks block JOIN training_session_segments segment ON segment.id = block.segment_id JOIN training_sessions session ON session.id = segment.session_id WHERE block.id = source_id AND plan.id = session.plan_id;
  WHEN 'gym_exercises', 'water_work_steps' THEN source_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.block_id ELSE NEW.block_id END; UPDATE training_plans plan SET updated_at = clock_timestamp() FROM training_segment_blocks block JOIN training_session_segments segment ON segment.id = block.segment_id JOIN training_sessions session ON session.id = segment.session_id WHERE block.id = source_id AND plan.id = session.plan_id;
  WHEN 'training_variations' THEN source_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.plan_id ELSE NEW.plan_id END; UPDATE training_plans SET updated_at = clock_timestamp() WHERE id = source_id;
  WHEN 'training_group_members' THEN source_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.group_id ELSE NEW.group_id END; UPDATE training_plans SET updated_at = clock_timestamp() WHERE training_group_id = source_id;
  WHEN 'training_variation_groups' THEN source_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.training_group_id ELSE NEW.training_group_id END; UPDATE training_plans SET updated_at = clock_timestamp() WHERE training_group_id = source_id;
  WHEN 'training_variation_group_members' THEN source_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.variation_group_id ELSE NEW.variation_group_id END; UPDATE training_plans plan SET updated_at = clock_timestamp() FROM training_variation_groups variation_group WHERE variation_group.id = source_id AND plan.training_group_id = variation_group.training_group_id;
  WHEN 'training_groups' THEN source_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.id ELSE NEW.id END; UPDATE training_plans SET updated_at = clock_timestamp() WHERE training_group_id = source_id;
  WHEN 'user_memberships' THEN source_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.id ELSE NEW.id END; UPDATE training_plans plan SET updated_at = clock_timestamp() FROM training_group_members group_member WHERE group_member.membership_id = source_id AND plan.training_group_id = group_member.group_id;
  WHEN 'users' THEN source_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.id ELSE NEW.id END; UPDATE training_plans plan SET updated_at = clock_timestamp() FROM user_memberships membership JOIN training_group_members group_member ON group_member.membership_id = membership.id WHERE membership.user_id = source_id AND plan.training_group_id = group_member.group_id;
  WHEN 'water_intensity_zones' THEN source_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.profile_id ELSE NEW.profile_id END; UPDATE training_plans plan SET updated_at = clock_timestamp() FROM water_block_prescriptions water JOIN training_segment_blocks block ON block.id = water.block_id JOIN training_session_segments segment ON segment.id = block.segment_id JOIN training_sessions session ON session.id = segment.session_id WHERE water.intensity_profile_id = source_id AND plan.id = session.plan_id;
 END CASE;
 IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER training_sessions_touch_plan AFTER INSERT OR UPDATE OR DELETE ON training_sessions FOR EACH ROW EXECUTE FUNCTION touch_structured_training_plan(); CREATE TRIGGER training_session_segments_touch_plan AFTER INSERT OR UPDATE OR DELETE ON training_session_segments FOR EACH ROW EXECUTE FUNCTION touch_structured_training_plan(); CREATE TRIGGER training_segment_blocks_touch_plan AFTER INSERT OR UPDATE OR DELETE ON training_segment_blocks FOR EACH ROW EXECUTE FUNCTION touch_structured_training_plan(); CREATE TRIGGER gym_block_prescriptions_touch_plan AFTER INSERT OR UPDATE OR DELETE ON gym_block_prescriptions FOR EACH ROW EXECUTE FUNCTION touch_structured_training_plan(); CREATE TRIGGER gym_exercises_touch_plan AFTER INSERT OR UPDATE OR DELETE ON gym_exercises FOR EACH ROW EXECUTE FUNCTION touch_structured_training_plan(); CREATE TRIGGER water_block_prescriptions_touch_plan AFTER INSERT OR UPDATE OR DELETE ON water_block_prescriptions FOR EACH ROW EXECUTE FUNCTION touch_structured_training_plan(); CREATE TRIGGER water_work_steps_touch_plan AFTER INSERT OR UPDATE OR DELETE ON water_work_steps FOR EACH ROW EXECUTE FUNCTION touch_structured_training_plan(); CREATE TRIGGER training_variations_touch_plan AFTER INSERT OR UPDATE OR DELETE ON training_variations FOR EACH ROW EXECUTE FUNCTION touch_structured_training_plan(); CREATE TRIGGER training_group_members_touch_plan AFTER INSERT OR UPDATE OR DELETE ON training_group_members FOR EACH ROW EXECUTE FUNCTION touch_structured_training_plan(); CREATE TRIGGER training_variation_groups_touch_plan AFTER INSERT OR UPDATE OR DELETE ON training_variation_groups FOR EACH ROW EXECUTE FUNCTION touch_structured_training_plan(); CREATE TRIGGER training_variation_group_members_touch_plan AFTER INSERT OR UPDATE OR DELETE ON training_variation_group_members FOR EACH ROW EXECUTE FUNCTION touch_structured_training_plan(); CREATE TRIGGER training_groups_touch_plan AFTER UPDATE OF name, programme_id, team_id ON training_groups FOR EACH ROW EXECUTE FUNCTION touch_structured_training_plan(); CREATE TRIGGER user_memberships_touch_training_plan AFTER UPDATE OF user_id, starts_on, ends_on, programme_id, team_id ON user_memberships FOR EACH ROW EXECUTE FUNCTION touch_structured_training_plan(); CREATE TRIGGER users_touch_training_plan AFTER UPDATE OF is_active ON users FOR EACH ROW EXECUTE FUNCTION touch_structured_training_plan(); CREATE TRIGGER water_intensity_zones_touch_plan AFTER INSERT OR UPDATE OR DELETE ON water_intensity_zones FOR EACH ROW EXECUTE FUNCTION touch_structured_training_plan();
CREATE OR REPLACE FUNCTION training_block_snapshot_water(p_block_id uuid) RETURNS jsonb LANGUAGE sql STABLE AS $$
SELECT jsonb_strip_nulls(jsonb_build_object(
 'purpose', block.purpose::text, 'title', block.title, 'instructions', block.instructions,
 'gym', CASE WHEN gym.block_id IS NULL THEN NULL ELSE jsonb_build_object(
   'structure', gym.structure::text, 'objective', gym.objective::text, 'rounds', gym.rounds,
   'round_recovery_seconds', gym.round_recovery_seconds,
   'exercises', COALESCE((SELECT jsonb_agg(to_jsonb(exercise) - 'id' - 'block_id' - 'created_at' - 'updated_at' ORDER BY exercise.position) FROM gym_exercises exercise WHERE exercise.block_id = gym.block_id), '[]'::jsonb)
 ) END,
 'water', CASE WHEN water.block_id IS NULL THEN NULL ELSE jsonb_build_object(
   'method', water.method::text, 'intensity_profile_id', water.intensity_profile_id,
   'target_distance_metres', water.target_distance_metres,
   'target_distance_certainty', water.target_distance_certainty::text,
   'steps', COALESCE((SELECT jsonb_agg(jsonb_strip_nulls(jsonb_build_object(
      'source_id', step.id, 'source_parent_id', step.parent_step_id, 'position', step.position,
      'kind', step.kind::text, 'name', step.name, 'repeats', step.repeats,
      'duration_seconds', step.duration_seconds, 'duration_certainty', step.duration_certainty::text,
      'distance_metres', step.distance_metres, 'distance_certainty', step.distance_certainty::text,
      'recovery_seconds', step.recovery_seconds, 'intensity_code', step.intensity_code,
      'cadence_spm', step.cadence_spm, 'drill_focus', step.drill_focus,
      'drill_format', step.drill_format, 'role_notes', step.role_notes,
      'instructions', step.instructions)) ORDER BY step.created_at, step.position)
    FROM water_work_steps step WHERE step.block_id = water.block_id), '[]'::jsonb)
 ) END
))
FROM training_segment_blocks block
LEFT JOIN gym_block_prescriptions gym ON gym.block_id = block.id
LEFT JOIN water_block_prescriptions water ON water.block_id = block.id
WHERE block.id = p_block_id
$$;
CREATE OR REPLACE FUNCTION restore_training_block_water(p_snapshot jsonb, p_segment_id uuid) RETURNS uuid LANGUAGE plpgsql AS $$
DECLARE new_block_id uuid; gym jsonb; water jsonb; exercise jsonb; step jsonb; new_step_id uuid; parent_id uuid; step_ids jsonb := '{}'::jsonb;
BEGIN
 gym := p_snapshot->'gym'; water := p_snapshot->'water';
 IF NOT EXISTS (SELECT 1 FROM training_session_segments segment JOIN training_sessions session ON session.id = segment.session_id WHERE segment.id = p_segment_id AND session.status = 'ACTIVE') THEN RAISE NO_DATA_FOUND; END IF;
 IF gym IS NOT NULL AND jsonb_typeof(gym) = 'object' AND NOT EXISTS (SELECT 1 FROM training_session_segments WHERE id = p_segment_id AND modality = 'GYM') THEN RAISE CHECK_VIOLATION USING MESSAGE = 'structured gym blocks require a gym segment'; END IF;
 IF water IS NOT NULL AND jsonb_typeof(water) = 'object' AND NOT EXISTS (SELECT 1 FROM training_session_segments WHERE id = p_segment_id AND modality = 'WATER') THEN RAISE CHECK_VIOLATION USING MESSAGE = 'structured water blocks require a water segment'; END IF;
 INSERT INTO training_segment_blocks (segment_id, position, purpose, title, instructions) SELECT p_segment_id, COALESCE(max(position), 0) + 1, (p_snapshot->>'purpose')::training_block_purpose, COALESCE(p_snapshot->>'title', ''), p_snapshot->>'instructions' FROM training_segment_blocks WHERE segment_id = p_segment_id RETURNING id INTO new_block_id;
 IF gym IS NOT NULL AND jsonb_typeof(gym) = 'object' THEN
  INSERT INTO gym_block_prescriptions (block_id, structure, objective, rounds, round_recovery_seconds) VALUES (new_block_id, (gym->>'structure')::gym_block_structure, (gym->>'objective')::training_objective, (gym->>'rounds')::integer, NULLIF(gym->>'round_recovery_seconds', '')::integer);
  FOR exercise IN SELECT value FROM jsonb_array_elements(COALESCE(gym->'exercises', '[]'::jsonb)) LOOP
   INSERT INTO gym_exercises (block_id, position, name, sets, repetitions, duration_seconds, distance_metres, recovery_seconds, resistance_kind, resistance_value, resistance_text, execution_intent, tempo, notes) VALUES (new_block_id, (exercise->>'position')::integer, exercise->>'name', NULLIF(exercise->>'sets', '')::integer, NULLIF(exercise->>'repetitions', '')::integer, NULLIF(exercise->>'duration_seconds', '')::integer, NULLIF(exercise->>'distance_metres', '')::integer, NULLIF(exercise->>'recovery_seconds', '')::integer, NULLIF(exercise->>'resistance_kind', '')::gym_resistance_kind, NULLIF(exercise->>'resistance_value', '')::double precision, NULLIF(exercise->>'resistance_text', ''), NULLIF(exercise->>'execution_intent', '')::gym_execution_intent, NULLIF(exercise->>'tempo', ''), COALESCE(exercise->>'notes', ''));
  END LOOP;
 END IF;
 IF water IS NOT NULL AND jsonb_typeof(water) = 'object' THEN
  INSERT INTO water_block_prescriptions (block_id, method, intensity_profile_id, target_distance_metres, target_distance_certainty) VALUES (new_block_id, (water->>'method')::water_work_method, NULLIF(water->>'intensity_profile_id', '')::uuid, NULLIF(water->>'target_distance_metres', '')::integer, NULLIF(water->>'target_distance_certainty', '')::training_measure_certainty);
  FOR step IN SELECT value FROM jsonb_array_elements(COALESCE(water->'steps', '[]'::jsonb)) LOOP
   parent_id := NULL;
   IF step ? 'source_parent_id' THEN parent_id := NULLIF(step_ids->>(step->>'source_parent_id'), '')::uuid; END IF;
   IF step ? 'source_parent_id' AND parent_id IS NULL THEN RAISE CHECK_VIOLATION USING MESSAGE = 'water routine contains an invalid parent order'; END IF;
   INSERT INTO water_work_steps (block_id, parent_step_id, position, kind, name, repeats, duration_seconds, duration_certainty, distance_metres, distance_certainty, recovery_seconds, intensity_code, cadence_spm, drill_focus, drill_format, role_notes, instructions) VALUES (new_block_id, parent_id, (step->>'position')::integer, (step->>'kind')::water_step_kind, step->>'name', NULLIF(step->>'repeats', '')::integer, NULLIF(step->>'duration_seconds', '')::integer, NULLIF(step->>'duration_certainty', '')::training_measure_certainty, NULLIF(step->>'distance_metres', '')::integer, NULLIF(step->>'distance_certainty', '')::training_measure_certainty, NULLIF(step->>'recovery_seconds', '')::integer, NULLIF(step->>'intensity_code', ''), NULLIF(step->>'cadence_spm', '')::integer, NULLIF(step->>'drill_focus', ''), NULLIF(step->>'drill_format', ''), NULLIF(step->>'role_notes', ''), COALESCE(step->>'instructions', '')) RETURNING id INTO new_step_id;
   step_ids := step_ids || jsonb_build_object(step->>'source_id', new_step_id);
  END LOOP;
 END IF;
 RETURN new_block_id;
END
$$;

CREATE TYPE training_routine_kind AS ENUM ('BLOCK', 'SEGMENT', 'SESSION');
CREATE TYPE training_routine_visibility AS ENUM ('PRIVATE', 'SHARED');
CREATE TABLE training_routines (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), name varchar(180) NOT NULL, description varchar(1000) NOT NULL DEFAULT '', kind training_routine_kind NOT NULL, visibility training_routine_visibility NOT NULL DEFAULT 'PRIVATE', owner_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT, programme_id uuid NULL REFERENCES programmes(id) ON DELETE RESTRICT, team_id uuid NULL REFERENCES teams(id) ON DELETE RESTRICT, modality training_segment_modality NULL, objective training_objective NULL, method varchar(80) NOT NULL DEFAULT '', tags text[] NOT NULL DEFAULT '{}', source_id uuid NOT NULL, source_updated_at timestamptz NOT NULL, snapshot jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), CONSTRAINT training_routines_name_valid CHECK (name = btrim(name) AND char_length(name) BETWEEN 2 AND 180), CONSTRAINT training_routines_description_valid CHECK (description = btrim(description) AND char_length(description) <= 1000), CONSTRAINT training_routines_method_valid CHECK (method = btrim(method) AND char_length(method) <= 80), CONSTRAINT training_routines_tags_valid CHECK (cardinality(tags) <= 20 AND array_position(tags, NULL) IS NULL AND char_length(array_to_string(tags, ',')) <= 800), CONSTRAINT training_routines_snapshot_valid CHECK (jsonb_typeof(snapshot) = 'object'), CONSTRAINT training_routines_scope_valid CHECK ((visibility = 'PRIVATE' AND programme_id IS NULL AND team_id IS NULL) OR (visibility = 'SHARED' AND num_nonnulls(programme_id, team_id) = 1))); CREATE INDEX training_routines_owner_idx ON training_routines (owner_user_id, updated_at DESC); CREATE INDEX training_routines_programme_idx ON training_routines (programme_id, updated_at DESC) WHERE programme_id IS NOT NULL; CREATE INDEX training_routines_team_idx ON training_routines (team_id, updated_at DESC) WHERE team_id IS NOT NULL; CREATE INDEX training_routines_tags_idx ON training_routines USING gin (tags);
CREATE TABLE training_copy_events (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), source_kind varchar(20) NOT NULL CHECK (source_kind IN ('BLOCK', 'SEGMENT', 'SESSION', 'DAY', 'WEEK', 'ROUTINE', 'CYCLE')), source_id uuid NOT NULL, source_updated_at timestamptz NOT NULL, destination_kind varchar(20) NOT NULL CHECK (destination_kind IN ('BLOCK', 'SEGMENT', 'SESSION', 'WEEK', 'CYCLE')), destination_id uuid NOT NULL, copied_by_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT, copied_at timestamptz NOT NULL DEFAULT now()); CREATE INDEX training_copy_events_destination_idx ON training_copy_events (destination_kind, destination_id); CREATE INDEX training_copy_events_source_idx ON training_copy_events (source_kind, source_id);
CREATE FUNCTION prevent_training_copy_event_mutation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'training copy events are append-only'; END; $$;
CREATE TRIGGER training_copy_events_immutable_trigger BEFORE UPDATE OR DELETE ON training_copy_events FOR EACH ROW EXECUTE FUNCTION prevent_training_copy_event_mutation();
CREATE FUNCTION training_block_snapshot(p_block_id uuid) RETURNS jsonb LANGUAGE sql STABLE AS $$ SELECT training_block_snapshot_water(p_block_id) $$;
CREATE FUNCTION training_segment_snapshot(p_segment_id uuid) RETURNS jsonb LANGUAGE sql STABLE AS $$ SELECT jsonb_strip_nulls(jsonb_build_object('modality', segment.modality::text, 'title', segment.title, 'location', segment.location, 'planned_duration_minutes', segment.planned_duration_minutes, 'planned_start_offset_minutes', segment.planned_start_offset_minutes, 'transition_duration_minutes', segment.transition_duration_minutes, 'equipment_notes', segment.equipment_notes, 'blocks', COALESCE((SELECT jsonb_agg(training_block_snapshot(block.id) ORDER BY block.position) FROM training_segment_blocks block WHERE block.segment_id = segment.id), '[]'::jsonb))) FROM training_session_segments segment WHERE segment.id = p_segment_id $$;
CREATE FUNCTION training_session_snapshot(p_session_id uuid) RETURNS jsonb LANGUAGE sql STABLE AS $$ SELECT jsonb_build_object('title', session.title, 'description', session.description, 'entry_kind', session.entry_kind::text, 'duration_seconds', extract(epoch FROM session.ends_at - session.starts_at)::integer, 'segments', COALESCE((SELECT jsonb_agg(training_segment_snapshot(segment.id) ORDER BY segment.position) FROM training_session_segments segment WHERE segment.session_id = session.id), '[]'::jsonb)) FROM training_sessions session WHERE session.id = p_session_id AND session.status = 'ACTIVE' $$;
CREATE FUNCTION restore_training_block(p_snapshot jsonb, p_segment_id uuid) RETURNS uuid LANGUAGE sql AS $$ SELECT restore_training_block_water(p_snapshot, p_segment_id) $$;
CREATE FUNCTION restore_training_segment(p_snapshot jsonb, p_session_id uuid) RETURNS uuid LANGUAGE plpgsql AS $$ DECLARE new_segment_id uuid; block jsonb; BEGIN IF NOT EXISTS (SELECT 1 FROM training_sessions WHERE id = p_session_id AND status = 'ACTIVE') THEN RAISE NO_DATA_FOUND; END IF; INSERT INTO training_session_segments (session_id, position, modality, title, location, planned_duration_minutes, planned_start_offset_minutes, transition_duration_minutes, equipment_notes) SELECT p_session_id, COALESCE(max(position), 0) + 1, (p_snapshot->>'modality')::training_segment_modality, COALESCE(p_snapshot->>'title', ''), COALESCE(p_snapshot->>'location', ''), NULLIF(p_snapshot->>'planned_duration_minutes', '')::integer, NULLIF(p_snapshot->>'planned_start_offset_minutes', '')::integer, NULLIF(p_snapshot->>'transition_duration_minutes', '')::integer, COALESCE(p_snapshot->>'equipment_notes', '') FROM training_session_segments WHERE session_id = p_session_id RETURNING id INTO new_segment_id; FOR block IN SELECT value FROM jsonb_array_elements(COALESCE(p_snapshot->'blocks', '[]'::jsonb)) LOOP PERFORM restore_training_block(block, new_segment_id); END LOOP; RETURN new_segment_id; END $$;
CREATE FUNCTION restore_training_session(p_snapshot jsonb, p_plan_id uuid, p_starts_at timestamptz, p_created_by_id uuid) RETURNS uuid LANGUAGE plpgsql AS $$ DECLARE new_session_id uuid; segment jsonb; BEGIN INSERT INTO training_sessions (plan_id, title, description, starts_at, ends_at, entry_kind, created_by_id) SELECT plan.id, p_snapshot->>'title', COALESCE(p_snapshot->>'description', ''), p_starts_at, p_starts_at + ((p_snapshot->>'duration_seconds')::integer * interval '1 second'), (p_snapshot->>'entry_kind')::training_entry_kind, p_created_by_id FROM training_plans plan WHERE plan.id = p_plan_id AND plan.training_group_id IS NOT NULL AND p_starts_at >= (plan.week_start::timestamp AT TIME ZONE 'Europe/Lisbon') AND p_starts_at + ((p_snapshot->>'duration_seconds')::integer * interval '1 second') <= ((plan.week_start + 7)::timestamp AT TIME ZONE 'Europe/Lisbon') RETURNING id INTO new_session_id; IF new_session_id IS NULL THEN RAISE NO_DATA_FOUND; END IF; FOR segment IN SELECT value FROM jsonb_array_elements(COALESCE(p_snapshot->'segments', '[]'::jsonb)) LOOP PERFORM restore_training_segment(segment, new_session_id); END LOOP; RETURN new_session_id; END $$;
CREATE TYPE training_outcome_status AS ENUM ('COMPLETED', 'MISSED', 'REPLACED');
CREATE TABLE training_session_outcomes (session_id uuid NOT NULL REFERENCES training_sessions(id) ON DELETE CASCADE, user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE, prescription_id uuid NULL REFERENCES training_prescriptions(id) ON DELETE RESTRICT, status training_outcome_status NOT NULL, replacement_session_id uuid NULL REFERENCES training_sessions(id) ON DELETE RESTRICT, replacement_reason varchar(300) NULL, distance_metres integer NULL, actual_duration_minutes integer NULL, perceived_exertion smallint NULL, recovery_feeling smallint NULL, perception_note varchar(500) NULL, version integer NOT NULL DEFAULT 1, reported_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY (session_id, user_id), CONSTRAINT training_outcomes_replacement_valid CHECK ((status = 'REPLACED' AND replacement_session_id IS NOT NULL AND replacement_session_id <> session_id AND replacement_reason = btrim(replacement_reason) AND char_length(replacement_reason) BETWEEN 2 AND 300) OR (status IN ('COMPLETED', 'MISSED') AND replacement_session_id IS NULL AND replacement_reason IS NULL)), CONSTRAINT training_outcomes_distance_valid CHECK (distance_metres IS NULL OR (status = 'COMPLETED' AND distance_metres BETWEEN 1 AND 200000)), CONSTRAINT training_outcomes_feedback_valid CHECK ((status = 'COMPLETED' AND (actual_duration_minutes IS NULL OR actual_duration_minutes BETWEEN 1 AND 1440) AND (perceived_exertion IS NULL OR perceived_exertion BETWEEN 0 AND 10) AND (recovery_feeling IS NULL OR recovery_feeling BETWEEN 1 AND 5) AND (perception_note IS NULL OR (perception_note = btrim(perception_note) AND char_length(perception_note) BETWEEN 1 AND 500))) OR (status IN ('MISSED', 'REPLACED') AND actual_duration_minutes IS NULL AND perceived_exertion IS NULL AND recovery_feeling IS NULL AND perception_note IS NULL))); CREATE INDEX training_outcomes_user_idx ON training_session_outcomes (user_id, updated_at DESC); CREATE INDEX training_outcomes_prescription_idx ON training_session_outcomes (prescription_id) WHERE prescription_id IS NOT NULL; CREATE INDEX training_outcomes_leaderboard_idx ON training_session_outcomes (user_id, session_id) INCLUDE (distance_metres) WHERE status = 'COMPLETED' AND distance_metres IS NOT NULL;
CREATE TABLE competition_documents (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), title varchar(180) NOT NULL, url text NOT NULL, source varchar(180) NOT NULL, reviewed_on date NOT NULL, event_id uuid NULL REFERENCES events(id) ON DELETE CASCADE, modality_id uuid NULL REFERENCES modalities(id) ON DELETE RESTRICT, programme_id uuid NULL REFERENCES programmes(id) ON DELETE RESTRICT, team_id uuid NULL REFERENCES teams(id) ON DELETE RESTRICT, author_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT, published_at timestamptz NOT NULL DEFAULT now(), created_at timestamptz NOT NULL DEFAULT now(), CONSTRAINT competition_documents_title_valid CHECK (title = btrim(title) AND char_length(title) BETWEEN 2 AND 180), CONSTRAINT competition_documents_url_valid CHECK (url ~ '^https://'), CONSTRAINT competition_documents_source_valid CHECK (source = btrim(source) AND char_length(source) BETWEEN 2 AND 180), CONSTRAINT competition_documents_context_valid CHECK (event_id IS NOT NULL OR modality_id IS NOT NULL), CONSTRAINT competition_documents_scope_valid CHECK (programme_id IS NOT NULL OR team_id IS NOT NULL OR event_id IS NOT NULL), CONSTRAINT competition_documents_modality_scope_valid CHECK (modality_id IS NULL OR programme_id IS NOT NULL OR team_id IS NOT NULL)); CREATE INDEX competition_documents_event_idx ON competition_documents (event_id, published_at DESC) WHERE event_id IS NOT NULL; CREATE INDEX competition_documents_modality_idx ON competition_documents (modality_id, published_at DESC) WHERE modality_id IS NOT NULL;
CREATE TYPE staff_capability AS ENUM ('COACH', 'MODERATOR');
CREATE TABLE staff_grants (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT, capability staff_capability NOT NULL, programme_id uuid NULL REFERENCES programmes(id) ON DELETE RESTRICT, team_id uuid NULL REFERENCES teams(id) ON DELETE RESTRICT, granted_by_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT, granted_at timestamptz NOT NULL DEFAULT now(), revoked_by_id uuid NULL REFERENCES users(id) ON DELETE RESTRICT, revoked_at timestamptz NULL, revoke_reason varchar(500) NULL, CONSTRAINT staff_grants_scope_valid CHECK ((capability = 'COACH' AND (programme_id IS NOT NULL OR team_id IS NOT NULL)) OR (capability = 'MODERATOR' AND programme_id IS NULL AND team_id IS NULL)), CONSTRAINT staff_grants_revocation_valid CHECK ((revoked_at IS NULL AND revoked_by_id IS NULL AND revoke_reason IS NULL) OR (revoked_at IS NOT NULL AND revoked_by_id IS NOT NULL AND revoke_reason = btrim(revoke_reason) AND char_length(revoke_reason) BETWEEN 1 AND 500)));
CREATE TRIGGER staff_grants_active_subject_attachment BEFORE INSERT OR UPDATE OF user_id ON staff_grants FOR EACH ROW EXECUTE FUNCTION require_active_privacy_attachment_subject();
CREATE UNIQUE INDEX staff_grants_active_scope_uidx ON staff_grants (user_id, capability, COALESCE(programme_id, '00000000-0000-0000-0000-000000000000'::uuid), COALESCE(team_id, '00000000-0000-0000-0000-000000000000'::uuid)) WHERE revoked_at IS NULL; CREATE INDEX staff_grants_active_user_idx ON staff_grants (user_id) WHERE revoked_at IS NULL;
CREATE FUNCTION reject_dependent_privileges() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF EXISTS (SELECT 1 FROM users WHERE id = NEW.user_id AND is_dependent) THEN RAISE EXCEPTION 'dependants cannot receive platform or staff privileges'; END IF; RETURN NEW; END; $$;
CREATE TRIGGER user_platform_roles_reject_dependant BEFORE INSERT OR UPDATE OF user_id ON user_platform_roles FOR EACH ROW EXECUTE FUNCTION reject_dependent_privileges(); CREATE TRIGGER staff_grants_reject_dependant BEFORE INSERT OR UPDATE OF user_id ON staff_grants FOR EACH ROW EXECUTE FUNCTION reject_dependent_privileges();
CREATE TABLE staff_grant_audit_events (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), staff_grant_id uuid NOT NULL REFERENCES staff_grants(id) ON DELETE RESTRICT, action varchar(20) NOT NULL, actor_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT, occurred_at timestamptz NOT NULL DEFAULT now(), reason varchar(500) NULL, CONSTRAINT staff_grant_audit_events_action_valid CHECK (action IN ('GRANTED', 'REVOKED')), CONSTRAINT staff_grant_audit_events_reason_valid CHECK (reason IS NULL OR (reason = btrim(reason) AND char_length(reason) BETWEEN 1 AND 500))); CREATE INDEX staff_grant_audit_events_grant_idx ON staff_grant_audit_events (staff_grant_id, occurred_at);
CREATE FUNCTION audit_staff_grant_change() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF TG_OP = 'INSERT' THEN INSERT INTO staff_grant_audit_events (staff_grant_id, action, actor_user_id) VALUES (NEW.id, 'GRANTED', NEW.granted_by_id); RETURN NEW; END IF; IF OLD.user_id <> NEW.user_id OR OLD.capability <> NEW.capability OR OLD.programme_id IS DISTINCT FROM NEW.programme_id OR OLD.team_id IS DISTINCT FROM NEW.team_id OR OLD.granted_by_id <> NEW.granted_by_id OR OLD.granted_at <> NEW.granted_at OR OLD.revoked_at IS NOT NULL OR NEW.revoked_at IS NULL THEN RAISE EXCEPTION 'staff grants are immutable except for one revocation'; END IF; INSERT INTO staff_grant_audit_events (staff_grant_id, action, actor_user_id, occurred_at, reason) VALUES (NEW.id, 'REVOKED', NEW.revoked_by_id, NEW.revoked_at, NEW.revoke_reason); RETURN NEW; END; $$;
CREATE TRIGGER staff_grants_audit_trigger AFTER INSERT OR UPDATE ON staff_grants FOR EACH ROW EXECUTE FUNCTION audit_staff_grant_change();
CREATE FUNCTION prevent_staff_grant_audit_mutation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'staff grant audit events are append-only'; END; $$;
CREATE TRIGGER staff_grant_audit_events_immutable_trigger BEFORE UPDATE OR DELETE ON staff_grant_audit_events FOR EACH ROW EXECUTE FUNCTION prevent_staff_grant_audit_mutation();
CREATE TABLE event_team_audiences (event_id uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE, team_id uuid NOT NULL REFERENCES teams(id) ON DELETE RESTRICT, PRIMARY KEY (event_id, team_id)); CREATE INDEX event_team_audiences_team_idx ON event_team_audiences (team_id, event_id);
CREATE TYPE photo_album_status AS ENUM ('OPEN', 'ARCHIVED');
CREATE TABLE photo_albums (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), title varchar(180) NOT NULL, description varchar(2000) NOT NULL DEFAULT '', status photo_album_status NOT NULL DEFAULT 'OPEN', created_by_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT, archived_by_id uuid NULL REFERENCES users(id) ON DELETE RESTRICT, archived_at timestamptz NULL, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), CONSTRAINT photo_albums_title_valid CHECK (title = btrim(title) AND char_length(title) BETWEEN 2 AND 180), CONSTRAINT photo_albums_description_valid CHECK (description = btrim(description) AND char_length(description) <= 2000), CONSTRAINT photo_albums_lifecycle_valid CHECK ((status = 'OPEN' AND archived_by_id IS NULL AND archived_at IS NULL) OR (status = 'ARCHIVED' AND archived_by_id IS NOT NULL AND archived_at IS NOT NULL))); CREATE INDEX photo_albums_status_created_idx ON photo_albums (status, created_at DESC, id DESC);
CREATE TABLE photo_album_programme_audiences (album_id uuid NOT NULL REFERENCES photo_albums(id) ON DELETE CASCADE, programme_id uuid NOT NULL REFERENCES programmes(id) ON DELETE RESTRICT, PRIMARY KEY (album_id, programme_id)); CREATE INDEX photo_album_programme_audiences_lookup_idx ON photo_album_programme_audiences (programme_id, album_id);
CREATE TABLE photo_album_team_audiences (album_id uuid NOT NULL REFERENCES photo_albums(id) ON DELETE CASCADE, team_id uuid NOT NULL REFERENCES teams(id) ON DELETE RESTRICT, PRIMARY KEY (album_id, team_id)); CREATE INDEX photo_album_team_audiences_lookup_idx ON photo_album_team_audiences (team_id, album_id);
CREATE FUNCTION ensure_photo_album_has_audience() RETURNS trigger LANGUAGE plpgsql AS $$ DECLARE target_album_id uuid; BEGIN IF TG_TABLE_NAME = 'photo_albums' THEN target_album_id := NEW.id; ELSE target_album_id := OLD.album_id; END IF; IF EXISTS (SELECT 1 FROM photo_albums WHERE id = target_album_id) AND NOT EXISTS (SELECT 1 FROM photo_album_programme_audiences WHERE album_id = target_album_id) AND NOT EXISTS (SELECT 1 FROM photo_album_team_audiences WHERE album_id = target_album_id) THEN RAISE EXCEPTION 'photo album requires an audience'; END IF; IF TG_OP = 'DELETE' THEN RETURN OLD; END IF; RETURN NEW; END; $$;
CREATE CONSTRAINT TRIGGER photo_albums_audience_required_trigger AFTER INSERT OR UPDATE ON photo_albums DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION ensure_photo_album_has_audience();
CREATE CONSTRAINT TRIGGER photo_album_programme_audience_required_trigger AFTER DELETE ON photo_album_programme_audiences DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION ensure_photo_album_has_audience();
CREATE CONSTRAINT TRIGGER photo_album_team_audience_required_trigger AFTER DELETE ON photo_album_team_audiences DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION ensure_photo_album_has_audience();
CREATE TABLE photo_album_audit_events (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), album_id uuid NOT NULL REFERENCES photo_albums(id) ON DELETE RESTRICT, action varchar(20) NOT NULL CHECK (action IN ('CREATED', 'ARCHIVED')), actor_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT, occurred_at timestamptz NOT NULL DEFAULT now()); CREATE INDEX photo_album_audit_events_album_idx ON photo_album_audit_events (album_id, occurred_at, id);
CREATE FUNCTION audit_photo_album_change() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF TG_OP = 'INSERT' THEN INSERT INTO photo_album_audit_events (album_id, action, actor_user_id, occurred_at) VALUES (NEW.id, 'CREATED', NEW.created_by_id, NEW.created_at); RETURN NEW; END IF; IF OLD.status = 'OPEN' AND NEW.status = 'ARCHIVED' AND NEW.archived_by_id IS NOT NULL AND NEW.archived_at IS NOT NULL THEN INSERT INTO photo_album_audit_events (album_id, action, actor_user_id, occurred_at) VALUES (NEW.id, 'ARCHIVED', NEW.archived_by_id, NEW.archived_at); RETURN NEW; END IF; RAISE EXCEPTION 'photo album lifecycle transition is invalid'; END; $$;
CREATE TRIGGER photo_albums_audit_trigger AFTER INSERT OR UPDATE OF status ON photo_albums FOR EACH ROW EXECUTE FUNCTION audit_photo_album_change();
CREATE FUNCTION prevent_photo_album_audit_mutation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'photo album audit events are append-only'; END; $$;
CREATE TRIGGER photo_album_audit_events_immutable_trigger BEFORE UPDATE OR DELETE ON photo_album_audit_events FOR EACH ROW EXECUTE FUNCTION prevent_photo_album_audit_mutation();
CREATE TYPE announcement_status AS ENUM ('DRAFT', 'PUBLISHED', 'EXPIRED'); CREATE TYPE announcement_target_type AS ENUM ('PROGRAMME', 'TEAM', 'CATEGORY', 'MODALITY', 'EVENT', 'GUARDIAN');
CREATE TABLE announcements (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), title varchar(180) NOT NULL, body varchar(4000) NOT NULL, status announcement_status NOT NULL DEFAULT 'DRAFT', author_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT, published_by_id uuid NULL REFERENCES users(id) ON DELETE RESTRICT, expired_by_id uuid NULL REFERENCES users(id) ON DELETE RESTRICT, published_at timestamptz NULL, expires_at timestamptz NULL, expired_at timestamptz NULL, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), CONSTRAINT announcements_title_valid CHECK (title = btrim(title) AND char_length(title) BETWEEN 2 AND 180), CONSTRAINT announcements_body_valid CHECK (body = btrim(body) AND char_length(body) BETWEEN 2 AND 4000), CONSTRAINT announcements_status_valid CHECK ((status = 'DRAFT' AND published_at IS NULL AND published_by_id IS NULL AND expired_at IS NULL AND expired_by_id IS NULL) OR (status = 'PUBLISHED' AND published_at IS NOT NULL AND published_by_id IS NOT NULL AND expired_at IS NULL AND expired_by_id IS NULL) OR (status = 'EXPIRED' AND published_at IS NOT NULL AND published_by_id IS NOT NULL AND expired_at IS NOT NULL AND expired_by_id IS NOT NULL)), CONSTRAINT announcements_expiry_valid CHECK (expires_at IS NULL OR published_at IS NULL OR expires_at > published_at)); CREATE INDEX announcements_visible_idx ON announcements (status, published_at DESC, expires_at);
CREATE TABLE announcement_targets (announcement_id uuid NOT NULL REFERENCES announcements(id) ON DELETE CASCADE, target_type announcement_target_type NOT NULL, target_id uuid NULL, CONSTRAINT announcement_targets_shape_valid CHECK ((target_type = 'GUARDIAN' AND target_id IS NULL) OR (target_type <> 'GUARDIAN' AND target_id IS NOT NULL))); CREATE UNIQUE INDEX announcement_targets_unique_idx ON announcement_targets (announcement_id, target_type, target_id) NULLS NOT DISTINCT; CREATE INDEX announcement_targets_lookup_idx ON announcement_targets (target_type, target_id, announcement_id);
CREATE TABLE announcement_deliveries (announcement_id uuid NOT NULL REFERENCES announcements(id) ON DELETE CASCADE, user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE, delivered_at timestamptz NOT NULL DEFAULT now(), read_at timestamptz NULL, PRIMARY KEY (announcement_id, user_id)); CREATE INDEX announcement_deliveries_user_idx ON announcement_deliveries (user_id, read_at, delivered_at DESC);
CREATE TABLE announcement_audit_events (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), announcement_id uuid NOT NULL REFERENCES announcements(id) ON DELETE RESTRICT, action varchar(20) NOT NULL, actor_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT, occurred_at timestamptz NOT NULL DEFAULT now(), CONSTRAINT announcement_audit_action_valid CHECK (action IN ('AUTHORED', 'PUBLISHED', 'EXPIRED'))); CREATE INDEX announcement_audit_events_announcement_idx ON announcement_audit_events (announcement_id, occurred_at);
CREATE TABLE whatsapp_group_targets (whatsapp_group_id uuid NOT NULL REFERENCES whatsapp_groups(id) ON DELETE CASCADE, target_type announcement_target_type NOT NULL, target_id uuid NULL, CONSTRAINT whatsapp_group_targets_shape_valid CHECK ((target_type = 'GUARDIAN' AND target_id IS NULL) OR (target_type <> 'GUARDIAN' AND target_id IS NOT NULL))); CREATE UNIQUE INDEX whatsapp_group_targets_unique_idx ON whatsapp_group_targets (whatsapp_group_id, target_type, target_id) NULLS NOT DISTINCT; CREATE INDEX whatsapp_group_targets_lookup_idx ON whatsapp_group_targets (target_type, target_id, whatsapp_group_id);
INSERT INTO whatsapp_group_targets (whatsapp_group_id, target_type, target_id) SELECT id, 'PROGRAMME', programme_id FROM whatsapp_groups WHERE programme_id IS NOT NULL;
CREATE FUNCTION audit_announcement_change() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF TG_OP = 'INSERT' THEN INSERT INTO announcement_audit_events (announcement_id, action, actor_user_id) VALUES (NEW.id, 'AUTHORED', NEW.author_id); ELSIF OLD.status = 'DRAFT' AND NEW.status = 'PUBLISHED' THEN INSERT INTO announcement_audit_events (announcement_id, action, actor_user_id) VALUES (NEW.id, 'PUBLISHED', NEW.published_by_id); ELSIF OLD.status = 'PUBLISHED' AND NEW.status = 'EXPIRED' THEN INSERT INTO announcement_audit_events (announcement_id, action, actor_user_id) VALUES (NEW.id, 'EXPIRED', NEW.expired_by_id); ELSE RAISE EXCEPTION 'announcements may only be authored, published, or expired'; END IF; RETURN NEW; END; $$;
CREATE TRIGGER announcements_audit_trigger AFTER INSERT OR UPDATE ON announcements FOR EACH ROW EXECUTE FUNCTION audit_announcement_change();

CREATE TABLE activity_connections (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE, provider varchar(40) NOT NULL, provider_user_id varchar(255) NOT NULL, status varchar(40) NOT NULL DEFAULT 'ACTIVE', credentials_ciphertext bytea NULL, credential_key_id varchar(120) NULL, credential_expires_at timestamptz NULL, credential_version bigint NOT NULL DEFAULT 1, scopes text[] NOT NULL DEFAULT '{}', sync_cursor text NULL, last_successful_sync_at timestamptz NULL, last_error_code varchar(120) NULL, last_error_message varchar(2000) NULL, last_error_at timestamptz NULL, disconnected_at timestamptz NULL, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 CONSTRAINT activity_connections_provider_valid CHECK (provider = btrim(provider) AND provider ~ '^[a-z][a-z0-9_-]{1,39}$'),
 CONSTRAINT activity_connections_provider_user_valid CHECK (provider_user_id = btrim(provider_user_id) AND char_length(provider_user_id) BETWEEN 1 AND 255),
 CONSTRAINT activity_connections_status_valid CHECK (status IN ('ACTIVE', 'REAUTHORIZATION_REQUIRED', 'DISCONNECTED')),
 CONSTRAINT activity_connections_credentials_valid CHECK ((status = 'DISCONNECTED' AND credentials_ciphertext IS NULL AND credential_key_id IS NULL AND disconnected_at IS NOT NULL) OR (status <> 'DISCONNECTED' AND credentials_ciphertext IS NOT NULL AND octet_length(credentials_ciphertext) > 0 AND credential_key_id IS NOT NULL AND credential_key_id = btrim(credential_key_id) AND char_length(credential_key_id) BETWEEN 1 AND 120 AND disconnected_at IS NULL)),
 CONSTRAINT activity_connections_credential_version_valid CHECK (credential_version > 0),
 CONSTRAINT activity_connections_scopes_valid CHECK (array_position(scopes, NULL) IS NULL),
 CONSTRAINT activity_connections_error_valid CHECK ((last_error_code IS NULL AND last_error_message IS NULL AND last_error_at IS NULL) OR (last_error_code IS NOT NULL AND last_error_code = btrim(last_error_code) AND char_length(last_error_code) BETWEEN 1 AND 120 AND last_error_message IS NOT NULL AND char_length(last_error_message) BETWEEN 1 AND 2000 AND last_error_at IS NOT NULL)),
 CONSTRAINT activity_connections_user_provider_unique UNIQUE (user_id, provider),
 CONSTRAINT activity_connections_provider_identity_unique UNIQUE (provider, provider_user_id),
 CONSTRAINT activity_connections_identity_unique UNIQUE (id, user_id, provider)
);
CREATE INDEX activity_connections_sync_idx ON activity_connections (status, last_successful_sync_at) WHERE status <> 'DISCONNECTED';

CREATE TABLE activity_sync_jobs (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), idempotency_key uuid NOT NULL UNIQUE, connection_id uuid NOT NULL REFERENCES activity_connections(id) ON DELETE CASCADE, reason varchar(20) NOT NULL, status varchar(20) NOT NULL DEFAULT 'PENDING', attempts integer NOT NULL DEFAULT 0, checkpoint text NULL, last_error_code varchar(120) NULL, last_error_message varchar(2000) NULL, requested_at timestamptz NOT NULL DEFAULT now(), started_at timestamptz NULL, finished_at timestamptz NULL, updated_at timestamptz NOT NULL DEFAULT now(),
 CONSTRAINT activity_sync_jobs_reason_valid CHECK (reason IN ('LOGIN', 'WEBHOOK', 'MANUAL', 'BACKFILL', 'RECONCILIATION')),
 CONSTRAINT activity_sync_jobs_status_valid CHECK (status IN ('PENDING', 'RUNNING', 'SUCCEEDED', 'FAILED', 'CANCELLED')),
 CONSTRAINT activity_sync_jobs_attempts_valid CHECK (attempts >= 0),
 CONSTRAINT activity_sync_jobs_lifecycle_valid CHECK ((status = 'PENDING' AND started_at IS NULL AND finished_at IS NULL) OR (status = 'RUNNING' AND started_at IS NOT NULL AND finished_at IS NULL) OR (status IN ('SUCCEEDED', 'FAILED', 'CANCELLED') AND finished_at IS NOT NULL)),
 CONSTRAINT activity_sync_jobs_error_valid CHECK ((last_error_code IS NULL AND last_error_message IS NULL) OR (last_error_code IS NOT NULL AND last_error_code = btrim(last_error_code) AND char_length(last_error_code) BETWEEN 1 AND 120 AND last_error_message IS NOT NULL AND char_length(last_error_message) BETWEEN 1 AND 2000))
);
CREATE INDEX activity_sync_jobs_pending_idx ON activity_sync_jobs (requested_at, id) WHERE status = 'PENDING';
CREATE INDEX activity_sync_jobs_connection_idx ON activity_sync_jobs (connection_id, requested_at DESC, id DESC);

CREATE TABLE synced_activities (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), connection_id uuid NOT NULL, user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE, provider varchar(40) NOT NULL, provider_activity_id varchar(255) NOT NULL, provider_updated_at timestamptz NULL, starts_at timestamptz NOT NULL, ends_at timestamptz NOT NULL, sport varchar(120) NOT NULL, normalized_sport varchar(80) NOT NULL, duration_seconds integer NOT NULL, moving_duration_seconds integer NULL, distance_metres double precision NULL, average_heart_rate smallint NULL, maximum_heart_rate smallint NULL, provider_metrics jsonb NOT NULL DEFAULT '{}', raw_summary jsonb NOT NULL DEFAULT '{}', payload_sha256 bytea NOT NULL, normalization_version integer NOT NULL DEFAULT 1, deleted_at timestamptz NULL, ingested_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 CONSTRAINT synced_activities_connection_fk FOREIGN KEY (connection_id, user_id, provider) REFERENCES activity_connections(id, user_id, provider) ON DELETE CASCADE,
 CONSTRAINT synced_activities_provider_activity_valid CHECK (provider_activity_id = btrim(provider_activity_id) AND char_length(provider_activity_id) BETWEEN 1 AND 255),
 CONSTRAINT synced_activities_sport_valid CHECK (sport = btrim(sport) AND char_length(sport) BETWEEN 1 AND 120 AND normalized_sport = btrim(normalized_sport) AND char_length(normalized_sport) BETWEEN 1 AND 80),
 CONSTRAINT synced_activities_times_valid CHECK (starts_at < ends_at),
 CONSTRAINT synced_activities_duration_valid CHECK (duration_seconds > 0 AND (moving_duration_seconds IS NULL OR moving_duration_seconds >= 0)),
 CONSTRAINT synced_activities_distance_valid CHECK (distance_metres IS NULL OR distance_metres >= 0),
 CONSTRAINT synced_activities_heart_rate_valid CHECK ((average_heart_rate IS NULL OR average_heart_rate BETWEEN 20 AND 260) AND (maximum_heart_rate IS NULL OR maximum_heart_rate BETWEEN 20 AND 260) AND (average_heart_rate IS NULL OR maximum_heart_rate IS NULL OR average_heart_rate <= maximum_heart_rate)),
 CONSTRAINT synced_activities_payload_hash_valid CHECK (octet_length(payload_sha256) = 32),
 CONSTRAINT synced_activities_normalization_version_valid CHECK (normalization_version > 0),
 CONSTRAINT synced_activities_provider_identity_unique UNIQUE (provider, provider_activity_id),
 CONSTRAINT synced_activities_user_identity_unique UNIQUE (id, user_id)
);
CREATE INDEX synced_activities_user_starts_idx ON synced_activities (user_id, starts_at DESC, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX synced_activities_connection_updated_idx ON synced_activities (connection_id, updated_at DESC, id DESC);

CREATE TABLE training_session_activity_matches (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), session_id uuid NOT NULL REFERENCES training_sessions(id) ON DELETE CASCADE, activity_id uuid NOT NULL, user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE, status varchar(20) NOT NULL DEFAULT 'SUGGESTED', confidence smallint NOT NULL, match_basis jsonb NOT NULL DEFAULT '{}', decided_by_user_id uuid NULL REFERENCES users(id) ON DELETE RESTRICT, decided_at timestamptz NULL, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 CONSTRAINT training_session_activity_matches_activity_fk FOREIGN KEY (activity_id, user_id) REFERENCES synced_activities(id, user_id) ON DELETE CASCADE,
 CONSTRAINT training_session_activity_matches_status_valid CHECK (status IN ('SUGGESTED', 'CONFIRMED', 'REJECTED')),
 CONSTRAINT training_session_activity_matches_confidence_valid CHECK (confidence BETWEEN 0 AND 100),
 CONSTRAINT training_session_activity_matches_decision_valid CHECK ((status = 'SUGGESTED' AND decided_by_user_id IS NULL AND decided_at IS NULL) OR (status IN ('CONFIRMED', 'REJECTED') AND decided_by_user_id IS NOT NULL AND decided_at IS NOT NULL)),
 CONSTRAINT training_session_activity_matches_unique UNIQUE (session_id, activity_id, user_id)
);
CREATE INDEX training_session_activity_matches_user_idx ON training_session_activity_matches (user_id, status, updated_at DESC, id DESC);
CREATE UNIQUE INDEX training_session_activity_matches_confirmed_session_uidx ON training_session_activity_matches (session_id, user_id) WHERE status = 'CONFIRMED';
CREATE UNIQUE INDEX training_session_activity_matches_confirmed_activity_uidx ON training_session_activity_matches (activity_id) WHERE status = 'CONFIRMED';

CREATE TYPE feature_availability_mode AS ENUM ('DISABLED', 'ADMIN_ONLY', 'ENABLED');
CREATE TABLE feature_flags (
 feature_key varchar(80) PRIMARY KEY,
 mode feature_availability_mode NOT NULL,
 updated_by_id uuid NULL REFERENCES users(id) ON DELETE RESTRICT,
 updated_at timestamptz NOT NULL DEFAULT now(),
 CONSTRAINT feature_flags_key_valid CHECK (feature_key = btrim(feature_key) AND feature_key ~ '^[a-z][a-z0-9_]{1,79}$')
);
INSERT INTO feature_flags (feature_key, mode) VALUES ('suggestions', 'ENABLED'), ('photo_submissions', 'DISABLED'), ('structured_training_planning', 'ADMIN_ONLY');
CREATE TABLE feature_flag_events (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 feature_key varchar(80) NOT NULL REFERENCES feature_flags(feature_key) ON DELETE RESTRICT,
 previous_mode feature_availability_mode NOT NULL,
 new_mode feature_availability_mode NOT NULL,
 actor_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 occurred_at timestamptz NOT NULL DEFAULT now(),
 CONSTRAINT feature_flag_events_change_valid CHECK (previous_mode <> new_mode)
);
CREATE INDEX feature_flag_events_feature_idx ON feature_flag_events (feature_key, occurred_at DESC, id DESC);
CREATE FUNCTION audit_feature_flag_change() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.updated_by_id IS NULL THEN RAISE EXCEPTION 'feature flag changes require an administrator'; END IF; IF OLD.mode = NEW.mode THEN RETURN NEW; END IF; INSERT INTO feature_flag_events (feature_key, previous_mode, new_mode, actor_user_id, occurred_at) VALUES (NEW.feature_key, OLD.mode, NEW.mode, NEW.updated_by_id, NEW.updated_at); RETURN NEW; END; $$;
CREATE TRIGGER feature_flags_audit_trigger AFTER UPDATE ON feature_flags FOR EACH ROW EXECUTE FUNCTION audit_feature_flag_change();
CREATE FUNCTION prevent_feature_flag_event_mutation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'feature flag events are append-only'; END; $$;
CREATE TRIGGER feature_flag_events_immutable_trigger BEFORE UPDATE OR DELETE ON feature_flag_events FOR EACH ROW EXECUTE FUNCTION prevent_feature_flag_event_mutation();

CREATE TYPE suggestion_category AS ENUM ('FACILITIES', 'EQUIPMENT', 'TRAINING', 'EVENTS', 'COMMUNICATION', 'OTHER');
CREATE TYPE suggestion_status AS ENUM ('SUBMITTED', 'UNDER_REVIEW', 'PLANNED', 'DECLINED', 'COMPLETED');
CREATE TABLE suggestions (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 requester_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 category suggestion_category NOT NULL,
 subject varchar(160) NOT NULL,
 description varchar(3000) NOT NULL,
 status suggestion_status NOT NULL DEFAULT 'SUBMITTED',
 staff_response varchar(2000) NULL,
 responded_by_id uuid NULL REFERENCES users(id) ON DELETE RESTRICT,
 responded_at timestamptz NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 CONSTRAINT suggestions_subject_valid CHECK (subject = btrim(subject) AND char_length(subject) BETWEEN 3 AND 160),
 CONSTRAINT suggestions_description_valid CHECK (description = btrim(description) AND char_length(description) BETWEEN 10 AND 3000),
 CONSTRAINT suggestions_response_valid CHECK (staff_response IS NULL OR (staff_response = btrim(staff_response) AND char_length(staff_response) BETWEEN 2 AND 2000)),
 CONSTRAINT suggestions_response_actor_valid CHECK ((staff_response IS NULL AND responded_by_id IS NULL AND responded_at IS NULL) OR (staff_response IS NOT NULL AND responded_by_id IS NOT NULL AND responded_at IS NOT NULL)),
 CONSTRAINT suggestions_terminal_response_valid CHECK (status NOT IN ('DECLINED', 'COMPLETED') OR staff_response IS NOT NULL)
);
CREATE INDEX suggestions_requester_created_idx ON suggestions (requester_id, created_at DESC, id DESC);
CREATE INDEX suggestions_triage_idx ON suggestions (status, updated_at DESC, id DESC);

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

-- Erasure execution is a separate four-eyes capability. It is never implied by
-- administrator or reviewer access, and no account is seeded with it.
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

-- Immutable adopted versions contain only controller-approved catalogue metadata.
CREATE TABLE privacy_request_policies (
 version varchar(80) PRIMARY KEY CHECK (char_length(btrim(version)) BETWEEN 1 AND 80),
 category_catalogue jsonb NOT NULL CHECK (jsonb_typeof(category_catalogue) = 'array'),
 executor_version varchar(80) NULL CHECK (executor_version IS NULL OR char_length(btrim(executor_version)) BETWEEN 1 AND 80),
 plan_schema_version varchar(80) NULL CHECK (plan_schema_version IS NULL OR char_length(btrim(plan_schema_version)) BETWEEN 1 AND 80),
 account_closure_enabled boolean NOT NULL DEFAULT false,
 working_retention_days integer NULL CHECK (working_retention_days BETWEEN 1 AND 36500),
 response_months integer NOT NULL DEFAULT 1 CHECK (response_months BETWEEN 1 AND 12),
 extension_months integer NOT NULL DEFAULT 2 CHECK (extension_months BETWEEN 1 AND 12),
 adopted_at timestamptz NULL, adopted_by uuid NULL REFERENCES users(id) ON DELETE RESTRICT,
 created_at timestamptz NOT NULL DEFAULT now(), CHECK ((adopted_at IS NULL) = (adopted_by IS NULL)),
 CHECK ((executor_version IS NULL) = (plan_schema_version IS NULL))
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
 status varchar(30) NOT NULL DEFAULT 'RECEIVED' CHECK (status IN ('RECEIVED','UNDER_REVIEW','AWAITING_EXECUTION','PARTIALLY_APPROVED','PROCESSING','RETRYABLE_FAILED','TERMINAL_FAILED','COMPLETED','REFUSED','CANCELLED')),
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
 CONSTRAINT privacy_case_closure_state CHECK ((status IN ('REFUSED','CANCELLED','COMPLETED') AND closed_at IS NOT NULL AND evidence_expires_at > closed_at) OR (status NOT IN ('REFUSED','CANCELLED','COMPLETED') AND closed_at IS NULL AND evidence_expires_at IS NULL)),
 CHECK ((status IN ('AWAITING_EXECUTION','PARTIALLY_APPROVED','REFUSED') AND decided_at IS NOT NULL AND decided_by IS NOT NULL AND decision_code IS NOT NULL AND policy_version IS NOT NULL) OR (status NOT IN ('AWAITING_EXECUTION','PARTIALLY_APPROVED','REFUSED')) OR working_erased_at IS NOT NULL),
 CONSTRAINT privacy_case_execution_decision_complete CHECK (status NOT IN ('PROCESSING','RETRYABLE_FAILED','TERMINAL_FAILED','COMPLETED') OR (decided_at IS NOT NULL AND decided_by IS NOT NULL AND decision_code IN ('APPROVED','PARTIALLY_APPROVED') AND policy_version IS NOT NULL))
);
CREATE UNIQUE INDEX data_erasure_request_idempotency_uidx ON data_erasure_requests(requester_user_id,idempotency_key);
CREATE UNIQUE INDEX data_erasure_request_active_uidx ON data_erasure_requests(subject_user_id,requester_user_id) WHERE status NOT IN ('REFUSED','CANCELLED','COMPLETED');
CREATE INDEX data_erasure_requests_queue_idx ON data_erasure_requests(status, due_at, received_at);
CREATE INDEX data_erasure_requests_requester_idx ON data_erasure_requests(requester_user_id, received_at DESC);
-- Server-compiled plans contain stable codes only and survive the 90-day
-- working-record scrub so their digest remains auditable until evidence expiry.
CREATE TABLE privacy_request_execution_plans (
 request_id uuid PRIMARY KEY REFERENCES data_erasure_requests(id) ON DELETE RESTRICT,
 policy_version varchar(80) NOT NULL REFERENCES privacy_request_policies(version) ON DELETE RESTRICT,
 executor_version varchar(80) NOT NULL CHECK (char_length(btrim(executor_version)) BETWEEN 1 AND 80),
 schema_version varchar(80) NOT NULL CHECK (char_length(btrim(schema_version)) BETWEEN 1 AND 80),
 plan jsonb NOT NULL CHECK (jsonb_typeof(plan) = 'object' AND octet_length(plan::text) <= 204800),
 plan_sha256 bytea NOT NULL,
 created_at timestamptz NOT NULL,
 CHECK (octet_length(plan_sha256) = 32),
 CHECK (plan->>'policy_version' = policy_version AND plan->>'executor_version' = executor_version AND plan->>'schema_version' = schema_version),
 CONSTRAINT privacy_execution_plans_binding_unique UNIQUE(request_id,plan_sha256,executor_version,schema_version)
);
CREATE TRIGGER privacy_execution_plans_immutable BEFORE UPDATE OR DELETE ON privacy_request_execution_plans FOR EACH ROW EXECUTE FUNCTION prevent_privacy_audit_mutation();

-- Execution rows bind a request to its immutable plan without copying subject
-- identity. Category jobs and operation checkpoints contain stable codes only.
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
 UNIQUE(execution_id,category_key), UNIQUE(execution_id,plan_entry_position),
 CONSTRAINT privacy_erasure_category_jobs_target_binding_unique UNIQUE(id,execution_id,entry_sha256,category_key)
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
 UNIQUE(job_id,operation_position), UNIQUE(job_id,operation_code,action_version),
 CONSTRAINT privacy_erasure_job_checkpoints_target_binding_unique UNIQUE(id,job_id,operation_code,action_version)
);
CREATE INDEX privacy_erasure_job_checkpoints_pending_idx ON privacy_erasure_job_checkpoints(job_id,operation_position) WHERE status='PENDING';

-- Locator envelopes and keyed correlation tokens are kept outside public so
-- neither the web role nor the shared worker role can inherit table access.
-- Future executor/schema versions reach these records only through fenced
-- SECURITY DEFINER routines; this v1-compatible migration is intentionally
-- inert and does not activate object deletion.
CREATE SCHEMA IF NOT EXISTS privacy_protected;
REVOKE ALL ON SCHEMA privacy_protected FROM PUBLIC;
CREATE TABLE IF NOT EXISTS privacy_protected.object_targets (
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
CREATE INDEX IF NOT EXISTS privacy_object_targets_checkpoint_idx ON privacy_protected.object_targets(checkpoint_id,id);
CREATE TABLE IF NOT EXISTS privacy_protected.object_target_digests (
 target_id uuid NOT NULL, execution_id uuid NOT NULL REFERENCES privacy_erasure_executions(id) ON DELETE RESTRICT,
 service_code varchar(120) NOT NULL, target_kind varchar(40) NOT NULL CHECK (target_kind='OBJECT_KEY'),
 digest_key_id varchar(80) NOT NULL, locator_digest bytea NOT NULL CHECK (octet_length(locator_digest)=32), created_at timestamptz NOT NULL,
 PRIMARY KEY(target_id,digest_key_id),
 FOREIGN KEY(target_id,execution_id,service_code,target_kind) REFERENCES privacy_protected.object_targets(id,execution_id,service_code,target_kind) ON DELETE RESTRICT,
 CHECK (service_code=btrim(service_code) AND service_code ~ '^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,119}$'),
 CHECK (digest_key_id=btrim(digest_key_id) AND digest_key_id ~ '^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'),
 UNIQUE(execution_id,service_code,target_kind,digest_key_id,locator_digest)
);
CREATE TABLE IF NOT EXISTS privacy_protected.object_evidence (
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
CREATE TABLE IF NOT EXISTS privacy_protected.object_upload_intents (
 id uuid PRIMARY KEY, subject_user_id uuid NULL REFERENCES users(id) ON DELETE RESTRICT,
 provenance_actor_user_id uuid NULL REFERENCES users(id) ON DELETE RESTRICT,
 source_kind varchar(40) NOT NULL CHECK (source_kind IN ('MEMBER_PROFILE_PHOTO','REPAIR_ATTACHMENT','EQUIPMENT_PHOTO')),
 source_ref uuid NOT NULL, service_code varchar(120) NOT NULL,
 target_kind varchar(40) NOT NULL CHECK (target_kind='OBJECT_KEY'),
 provider_contract_version varchar(40) NOT NULL CHECK (provider_contract_version='s3-versioned/v1'),
 envelope_version varchar(80) NOT NULL CHECK (envelope_version='x25519-aes256gcm-hkdfsha256/upload-intent-v1'),
 algorithm varchar(80) NOT NULL CHECK (algorithm='X25519-HKDF-SHA256-AES-256-GCM'),
 encryption_key_id varchar(80) NOT NULL, encapsulation bytea NOT NULL CHECK (octet_length(encapsulation)=32),
 nonce bytea NOT NULL CHECK (octet_length(nonce)=12), ciphertext bytea NOT NULL CHECK (octet_length(ciphertext) BETWEEN 17 AND 2048),
 digest_key_id varchar(80) NOT NULL, locator_digest bytea NOT NULL CHECK (octet_length(locator_digest)=32),
 locator_commitment bytea NULL CHECK (locator_commitment IS NULL OR octet_length(locator_commitment)=32),
 content_type varchar(100) NOT NULL CHECK (content_type IN ('image/jpeg','image/png','image/webp')),
 size_bytes bigint NOT NULL CHECK (size_bytes BETWEEN 1 AND 10485760), cleanup_after timestamptz NOT NULL, created_at timestamptz NOT NULL,
 CHECK (service_code=btrim(service_code) AND service_code ~ '^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,119}$'),
 CHECK (encryption_key_id=btrim(encryption_key_id) AND encryption_key_id ~ '^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'),
 CHECK (digest_key_id=btrim(digest_key_id) AND digest_key_id ~ '^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'),
 CHECK ((source_kind IN ('MEMBER_PROFILE_PHOTO','REPAIR_ATTACHMENT') AND subject_user_id IS NOT NULL) OR (source_kind='EQUIPMENT_PHOTO' AND subject_user_id IS NULL)),
 CHECK (cleanup_after>created_at), UNIQUE(digest_key_id,locator_digest), UNIQUE(encryption_key_id,encapsulation)
);
CREATE TABLE IF NOT EXISTS privacy_protected.object_upload_intent_events (
 intent_id uuid NOT NULL REFERENCES privacy_protected.object_upload_intents(id) ON DELETE RESTRICT,
 sequence integer NOT NULL CHECK (sequence > 0),
 status varchar(30) NOT NULL CHECK (status IN ('PREPARED','PUT_CONFIRMED','ATTACHED','CLEANUP_REQUIRED','ABSENCE_VERIFIED')),
 reason_code varchar(40) NOT NULL CHECK (reason_code IN ('UPLOAD_RESERVED','PUT_ACKNOWLEDGED','POINTER_ATTACHED','PUT_AMBIGUOUS','PUT_FAILED','ATTACH_FAILED','POINTER_SUPERSEDED','POINTER_REMOVED','STALE_TIMEOUT','CLEANUP_CONFIRMED')),
 occurred_at timestamptz NOT NULL,
 CHECK ((status='PREPARED' AND reason_code='UPLOAD_RESERVED')
     OR (status='PUT_CONFIRMED' AND reason_code='PUT_ACKNOWLEDGED')
     OR (status='ATTACHED' AND reason_code='POINTER_ATTACHED')
     OR (status='CLEANUP_REQUIRED' AND reason_code IN ('PUT_AMBIGUOUS','PUT_FAILED','ATTACH_FAILED','POINTER_SUPERSEDED','POINTER_REMOVED','STALE_TIMEOUT'))
     OR (status='ABSENCE_VERIFIED' AND reason_code='CLEANUP_CONFIRMED')),
 PRIMARY KEY(intent_id,sequence)
);
ALTER TABLE member_profiles ADD CONSTRAINT member_profiles_photo_upload_intent_fk FOREIGN KEY(photo_upload_intent_id) REFERENCES privacy_protected.object_upload_intents(id) ON DELETE RESTRICT;
ALTER TABLE repair_requests ADD CONSTRAINT repair_requests_image_upload_intent_fk FOREIGN KEY(image_upload_intent_id) REFERENCES privacy_protected.object_upload_intents(id) ON DELETE RESTRICT;
ALTER TABLE equipment ADD CONSTRAINT equipment_image_upload_intent_fk FOREIGN KEY(image_upload_intent_id) REFERENCES privacy_protected.object_upload_intents(id) ON DELETE RESTRICT;

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
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='privacy_protected.object_targets'::regclass AND tgname='privacy_object_targets_immutable') THEN
  CREATE TRIGGER privacy_object_targets_immutable BEFORE UPDATE OR DELETE ON privacy_protected.object_targets FOR EACH ROW EXECUTE FUNCTION prevent_privacy_execution_record_delete();
 END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='privacy_protected.object_target_digests'::regclass AND tgname='privacy_object_target_digests_immutable') THEN
  CREATE TRIGGER privacy_object_target_digests_immutable BEFORE UPDATE OR DELETE ON privacy_protected.object_target_digests FOR EACH ROW EXECUTE FUNCTION prevent_privacy_execution_record_delete();
 END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='privacy_protected.object_evidence'::regclass AND tgname='privacy_object_evidence_immutable') THEN
  CREATE TRIGGER privacy_object_evidence_immutable BEFORE UPDATE OR DELETE ON privacy_protected.object_evidence FOR EACH ROW EXECUTE FUNCTION prevent_privacy_execution_record_delete();
 END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='privacy_protected.object_upload_intents'::regclass AND tgname='privacy_object_upload_intents_immutable') THEN
  CREATE TRIGGER privacy_object_upload_intents_immutable BEFORE UPDATE OR DELETE ON privacy_protected.object_upload_intents FOR EACH ROW EXECUTE FUNCTION prevent_privacy_execution_record_delete();
 END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='privacy_protected.object_upload_intent_events'::regclass AND tgname='privacy_object_upload_intent_events_immutable') THEN
  CREATE TRIGGER privacy_object_upload_intent_events_immutable BEFORE UPDATE OR DELETE ON privacy_protected.object_upload_intent_events FOR EACH ROW EXECUTE FUNCTION prevent_privacy_execution_record_delete();
 END IF;
END; $$;
CREATE TABLE data_erasure_request_events (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), request_id uuid NOT NULL REFERENCES data_erasure_requests(id) ON DELETE RESTRICT,
 actor_role varchar(20) NOT NULL CHECK (actor_role IN ('REQUESTER','REVIEWER','EXECUTOR','SYSTEM')), actor_ref uuid NOT NULL,
 action varchar(40) NOT NULL CHECK (action IN ('RECEIVED','CLAIMED','IDENTITY_REQUESTED','IDENTITY_VERIFIED','REPRESENTATION_VERIFIED','REPRESENTATION_CONFLICT','DEADLINE_EXTENDED','DEPENDANT_RESOLVED','APPROVED','PARTIALLY_APPROVED','PROCESSING_STARTED','EXECUTION_RETRYABLE_FAILED','EXECUTION_RESUMED','EXECUTION_TERMINAL_FAILED','COMPLETED','REFUSED','CANCELLED')),
 reason_code varchar(40) NOT NULL CHECK (reason_code IN ('REQUEST_RECEIVED','REVIEW_CLAIMED','VERIFICATION_REQUIRED','VERIFICATION_RECORDED','DEPENDANT_RESOLVED','REPRESENTATION_CONFLICT','COMPLEXITY','REQUEST_VOLUME','POLICY_DECISION','EXECUTION_ACCEPTED','EXECUTION_RETRYABLE_FAILURE','EXECUTION_RETRY_STARTED','EXECUTION_TERMINAL_FAILURE','EXECUTION_COMPLETED','REQUESTER_CANCELLED')),
 from_status varchar(30) NULL CHECK (from_status IN ('RECEIVED','UNDER_REVIEW','AWAITING_EXECUTION','PARTIALLY_APPROVED','PROCESSING','RETRYABLE_FAILED','TERMINAL_FAILED','COMPLETED','REFUSED','CANCELLED')),
 to_status varchar(30) NOT NULL CHECK (to_status IN ('RECEIVED','UNDER_REVIEW','AWAITING_EXECUTION','PARTIALLY_APPROVED','PROCESSING','RETRYABLE_FAILED','TERMINAL_FAILED','COMPLETED','REFUSED','CANCELLED')),
 version bigint NOT NULL CHECK (version > 0), occurred_at timestamptz NOT NULL, UNIQUE(request_id,version)
);
CREATE TRIGGER privacy_case_events_immutable BEFORE UPDATE OR DELETE ON data_erasure_request_events FOR EACH ROW EXECUTE FUNCTION prevent_privacy_audit_mutation();
CREATE INDEX data_erasure_request_events_request_idx ON data_erasure_request_events(request_id,version);

CREATE FUNCTION privacy_worker_claim(p_lease_milliseconds bigint, p_worker_ref uuid)
RETURNS TABLE(job_id uuid, lease_id uuid, attempt_id uuid)
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

CREATE FUNCTION privacy_worker_heartbeat(p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_epoch bigint,p_worker_ref uuid,p_lease_milliseconds bigint)
RETURNS uuid LANGUAGE sql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
WITH authorized AS MATERIALIZED (
 SELECT lease.id
 FROM public.privacy_erasure_job_leases lease
 JOIN public.privacy_erasure_category_jobs job ON lease.job_id=job.id AND lease.epoch=job.lease_epoch
 JOIN public.privacy_erasure_job_attempts attempt ON attempt.lease_id=lease.id
 WHERE job.id=p_job_id AND lease.id=p_lease_id AND attempt.id=p_attempt_id AND lease.epoch=p_epoch
 AND lease.worker_ref=p_worker_ref AND job.status='LEASED' AND lease.released_at IS NULL
 AND attempt.finished_at IS NULL AND lease.expires_at>clock_timestamp()
 AND p_lease_milliseconds BETWEEN 1000 AND 3600000 FOR UPDATE OF job,lease,attempt
), authority_clock AS MATERIALIZED (SELECT clock_timestamp() AS occurred_at FROM authorized LIMIT 1)
UPDATE public.privacy_erasure_job_leases lease SET heartbeat_at=authority_clock.occurred_at,
expires_at=authority_clock.occurred_at+(p_lease_milliseconds*INTERVAL '1 millisecond')
FROM authorized,authority_clock WHERE lease.id=authorized.id RETURNING lease.id;
$$;

CREATE FUNCTION privacy_worker_complete_checkpoint(p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_epoch bigint,p_worker_ref uuid,p_operation_code text,p_action_version text)
RETURNS uuid LANGUAGE sql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
WITH authorized AS MATERIALIZED (
 SELECT job.id AS job_id,attempt.id AS attempt_id
 FROM public.privacy_erasure_category_jobs job
 JOIN public.privacy_erasure_job_leases lease ON lease.job_id=job.id AND lease.epoch=job.lease_epoch
 JOIN public.privacy_erasure_job_attempts attempt ON attempt.lease_id=lease.id
 WHERE job.id=p_job_id AND lease.id=p_lease_id AND attempt.id=p_attempt_id AND lease.epoch=p_epoch
 AND lease.worker_ref=p_worker_ref AND job.status='LEASED' AND lease.released_at IS NULL
 AND lease.expires_at>clock_timestamp() AND attempt.finished_at IS NULL FOR UPDATE OF job,lease,attempt
), authority_clock AS MATERIALIZED (SELECT clock_timestamp() AS occurred_at FROM authorized LIMIT 1)
UPDATE public.privacy_erasure_job_checkpoints checkpoint SET status='SUCCEEDED',completed_by_attempt_id=authorized.attempt_id,completed_at=authority_clock.occurred_at
FROM authorized,authority_clock WHERE checkpoint.job_id=authorized.job_id AND checkpoint.operation_code=p_operation_code
AND checkpoint.action_version=p_action_version AND checkpoint.status='PENDING' RETURNING checkpoint.id;
$$;

CREATE FUNCTION privacy_worker_complete_job(p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_epoch bigint,p_worker_ref uuid)
RETURNS uuid LANGUAGE sql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
WITH authorized AS MATERIALIZED (
 SELECT job.id,lease.id AS lease_id,attempt.id AS attempt_id
 FROM public.privacy_erasure_category_jobs job
 JOIN public.privacy_erasure_job_leases lease ON lease.job_id=job.id AND lease.epoch=job.lease_epoch
 JOIN public.privacy_erasure_job_attempts attempt ON attempt.lease_id=lease.id
 WHERE job.id=p_job_id AND lease.id=p_lease_id AND attempt.id=p_attempt_id AND lease.epoch=p_epoch
 AND lease.worker_ref=p_worker_ref AND job.status='LEASED' AND lease.released_at IS NULL
 AND lease.expires_at>clock_timestamp() AND attempt.finished_at IS NULL
 AND NOT EXISTS(SELECT 1 FROM public.privacy_erasure_job_checkpoints checkpoint WHERE checkpoint.job_id=job.id AND checkpoint.status<>'SUCCEEDED')
 FOR UPDATE OF job,lease,attempt
), authority_clock AS MATERIALIZED (SELECT clock_timestamp() AS occurred_at FROM authorized LIMIT 1),
completed_job AS (
 UPDATE public.privacy_erasure_category_jobs job SET status='SUCCEEDED',completed_at=authority_clock.occurred_at,updated_at=authority_clock.occurred_at
 FROM authorized,authority_clock WHERE job.id=authorized.id RETURNING job.id
), closed_lease AS (
 UPDATE public.privacy_erasure_job_leases lease SET released_at=authority_clock.occurred_at,outcome='SUCCEEDED'
 FROM authorized,completed_job,authority_clock WHERE lease.id=authorized.lease_id RETURNING lease.id
), closed_attempt AS (
 UPDATE public.privacy_erasure_job_attempts attempt SET finished_at=authority_clock.occurred_at,outcome='SUCCEEDED'
 FROM authorized,closed_lease,authority_clock WHERE attempt.id=authorized.attempt_id RETURNING attempt.id
)
SELECT completed_job.id FROM completed_job WHERE EXISTS(SELECT 1 FROM closed_attempt);
$$;

CREATE FUNCTION privacy_worker_fail_job(p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_epoch bigint,p_worker_ref uuid,p_classification text,p_retry_milliseconds bigint,p_stage_code text,p_failure_code text,p_diagnostic_digest bytea)
RETURNS uuid LANGUAGE sql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
WITH authorized AS MATERIALIZED (
 SELECT job.id,lease.id AS lease_id,attempt.id AS attempt_id
 FROM public.privacy_erasure_category_jobs job
 JOIN public.privacy_erasure_job_leases lease ON lease.job_id=job.id AND lease.epoch=job.lease_epoch
 JOIN public.privacy_erasure_job_attempts attempt ON attempt.lease_id=lease.id
 WHERE job.id=p_job_id AND lease.id=p_lease_id AND attempt.id=p_attempt_id AND lease.epoch=p_epoch
 AND lease.worker_ref=p_worker_ref AND job.status='LEASED' AND lease.released_at IS NULL
 AND lease.expires_at>clock_timestamp() AND attempt.finished_at IS NULL
 AND ((p_classification='RETRYABLE' AND p_retry_milliseconds BETWEEN 1 AND 3600000)
   OR (p_classification='TERMINAL' AND p_retry_milliseconds=0)) FOR UPDATE OF job,lease,attempt
), authority_clock AS MATERIALIZED (SELECT clock_timestamp() AS occurred_at FROM authorized LIMIT 1),
recorded_failure AS (
 INSERT INTO public.privacy_erasure_failures(job_id,attempt_id,classification,stage_code,failure_code,diagnostic_digest,occurred_at)
 SELECT id,attempt_id,p_classification,p_stage_code,p_failure_code,p_diagnostic_digest,authority_clock.occurred_at
 FROM authorized,authority_clock RETURNING *
), failed_job AS (
 UPDATE public.privacy_erasure_category_jobs job SET status=CASE WHEN recorded_failure.classification='RETRYABLE' THEN 'RETRY_WAIT' ELSE 'TERMINAL_FAILED' END,
 next_attempt_at=CASE WHEN recorded_failure.classification='RETRYABLE' THEN authority_clock.occurred_at+(p_retry_milliseconds*INTERVAL '1 millisecond') ELSE job.next_attempt_at END,
 updated_at=authority_clock.occurred_at FROM authorized,recorded_failure,authority_clock WHERE job.id=authorized.id RETURNING job.id
), closed_lease AS (
 UPDATE public.privacy_erasure_job_leases lease SET released_at=authority_clock.occurred_at,outcome=CASE WHEN recorded_failure.classification='RETRYABLE' THEN 'RETRYABLE_FAILED' ELSE 'TERMINAL_FAILED' END
 FROM authorized,recorded_failure,failed_job,authority_clock WHERE lease.id=authorized.lease_id RETURNING lease.id
), closed_attempt AS (
 UPDATE public.privacy_erasure_job_attempts attempt SET finished_at=authority_clock.occurred_at,outcome=CASE WHEN recorded_failure.classification='RETRYABLE' THEN 'RETRYABLE_FAILED' ELSE 'TERMINAL_FAILED' END
 FROM authorized,recorded_failure,closed_lease,authority_clock WHERE attempt.id=authorized.attempt_id RETURNING attempt.id
)
SELECT failed_job.id FROM failed_job WHERE EXISTS(SELECT 1 FROM closed_attempt);
$$;

CREATE FUNCTION privacy_worker_sync(p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_epoch bigint,p_worker_ref uuid)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE p_execution_id uuid; authoritative_worker_ref uuid; execution_status text; request_id uuid; request_status text; request_version bigint;
DECLARE target_status text; event_action text; event_reason text; occurred_at timestamptz; updated_version bigint;
BEGIN
 SELECT job.execution_id,lease.worker_ref INTO p_execution_id,authoritative_worker_ref
 FROM public.privacy_erasure_category_jobs job
 JOIN public.privacy_erasure_job_leases lease ON lease.job_id=job.id AND lease.epoch=p_epoch
 JOIN public.privacy_erasure_job_attempts attempt ON attempt.job_id=job.id AND attempt.lease_id=lease.id AND attempt.lease_epoch=p_epoch
 WHERE job.id=p_job_id AND lease.id=p_lease_id AND attempt.id=p_attempt_id AND lease.worker_ref=p_worker_ref
 AND ((job.status='LEASED' AND lease.released_at IS NULL AND attempt.finished_at IS NULL AND lease.expires_at>clock_timestamp())
   OR (job.status IN ('SUCCEEDED','RETRY_WAIT','TERMINAL_FAILED') AND lease.released_at IS NOT NULL AND attempt.finished_at IS NOT NULL))
 FOR UPDATE OF job,lease,attempt;
 IF NOT FOUND THEN RETURN NULL; END IF;
 PERFORM 1 FROM public.privacy_erasure_executions WHERE id=p_execution_id FOR UPDATE;
 SELECT CASE WHEN bool_or(status='TERMINAL_FAILED') THEN 'TERMINAL_FAILED'
             WHEN bool_or(status='RETRY_WAIT') THEN 'RETRYABLE_FAILED'
             WHEN bool_and(status='SUCCEEDED') THEN 'SUCCEEDED'
             WHEN bool_or(status='LEASED') OR bool_or(attempt_count>0) THEN 'RUNNING' ELSE 'QUEUED' END
 INTO execution_status FROM public.privacy_erasure_category_jobs WHERE execution_id=p_execution_id;
 occurred_at:=clock_timestamp();
 UPDATE public.privacy_erasure_executions SET status=execution_status,version=version+1,
  started_at=CASE WHEN execution_status='QUEUED' THEN NULL ELSE COALESCE(started_at,occurred_at) END,
  finished_at=CASE WHEN execution_status IN ('TERMINAL_FAILED','SUCCEEDED') THEN COALESCE(finished_at,occurred_at) ELSE NULL END,
  updated_at=occurred_at WHERE id=p_execution_id RETURNING privacy_erasure_executions.request_id INTO request_id;
 IF execution_status IN ('QUEUED','SUCCEEDED') THEN RETURN p_execution_id; END IF;
 IF execution_status='RUNNING' THEN target_status:='PROCESSING'; event_action:='EXECUTION_RESUMED'; event_reason:='EXECUTION_RETRY_STARTED';
 ELSIF execution_status='RETRYABLE_FAILED' THEN target_status:='RETRYABLE_FAILED'; event_action:='EXECUTION_RETRYABLE_FAILED'; event_reason:='EXECUTION_RETRYABLE_FAILURE';
 ELSIF execution_status='TERMINAL_FAILED' THEN target_status:='TERMINAL_FAILED'; event_action:='EXECUTION_TERMINAL_FAILED'; event_reason:='EXECUTION_TERMINAL_FAILURE';
 ELSE RAISE EXCEPTION 'invalid privacy execution aggregate'; END IF;
 SELECT status,version INTO request_status,request_version FROM public.data_erasure_requests WHERE id=request_id FOR UPDATE;
 IF request_status=target_status OR (execution_status='RUNNING' AND request_status='PROCESSING') THEN RETURN p_execution_id; END IF;
 IF NOT ((target_status='PROCESSING' AND request_status='RETRYABLE_FAILED') OR
         (target_status='RETRYABLE_FAILED' AND request_status='PROCESSING') OR
         (target_status='TERMINAL_FAILED' AND request_status IN ('PROCESSING','RETRYABLE_FAILED'))) THEN
  RAISE EXCEPTION 'invalid privacy request lifecycle transition';
 END IF;
 UPDATE public.data_erasure_requests SET status=target_status,version=version+1,updated_at=occurred_at
 WHERE id=request_id AND version=request_version RETURNING version INTO updated_version;
 INSERT INTO public.data_erasure_request_events(request_id,actor_role,actor_ref,action,reason_code,from_status,to_status,version,occurred_at)
 VALUES(request_id,'SYSTEM',authoritative_worker_ref,event_action,event_reason,request_status,target_status,updated_version,occurred_at);
 RETURN p_execution_id;
END; $$;

REVOKE ALL ON FUNCTION privacy_worker_claim(bigint,uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION privacy_worker_heartbeat(uuid,uuid,uuid,bigint,uuid,bigint) FROM PUBLIC;
REVOKE ALL ON FUNCTION privacy_worker_complete_checkpoint(uuid,uuid,uuid,bigint,uuid,text,text) FROM PUBLIC;
REVOKE ALL ON FUNCTION privacy_worker_complete_job(uuid,uuid,uuid,bigint,uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION privacy_worker_fail_job(uuid,uuid,uuid,bigint,uuid,text,bigint,text,text,bytea) FROM PUBLIC;
REVOKE ALL ON FUNCTION privacy_worker_sync(uuid,uuid,uuid,bigint,uuid) FROM PUBLIC;
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

CREATE FUNCTION privacy_worker_execute_checkpoint_without_tombstone_guard(
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
   UPDATE photo_album_audit_events SET actor_user_id=NULL,actor_principal_id=principal_ref WHERE actor_user_id=subject_ref;
   GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
   UPDATE announcement_audit_events SET actor_user_id=NULL,actor_principal_id=principal_ref WHERE actor_user_id=subject_ref;
   GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
   UPDATE feature_flag_events SET actor_user_id=NULL,actor_principal_id=principal_ref WHERE actor_user_id=subject_ref;
   GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
   UPDATE training_copy_events SET copied_by_id=NULL,copied_by_principal_id=principal_ref WHERE copied_by_id=subject_ref;
   GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
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
   WHERE membership.user_id=subject_ref AND membership.starts_on>=public.privacy_membership_history_effective_date(execution_ref,subject_ref)) THEN
   RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_dependency_unsupported';
  END IF;
  DELETE FROM training_variations WHERE target_membership_id IN(
   SELECT id FROM user_memberships WHERE user_id=subject_ref AND starts_on>=public.privacy_membership_history_effective_date(execution_ref,subject_ref));
  GET DIAGNOSTICS changed=ROW_COUNT;
  DELETE FROM training_variation_group_members WHERE membership_id IN(
   SELECT id FROM user_memberships WHERE user_id=subject_ref AND starts_on>=public.privacy_membership_history_effective_date(execution_ref,subject_ref));
  GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
  DELETE FROM training_group_members WHERE membership_id IN(
   SELECT id FROM user_memberships WHERE user_id=subject_ref AND starts_on>=public.privacy_membership_history_effective_date(execution_ref,subject_ref));
  GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
  DELETE FROM user_memberships WHERE user_id=subject_ref AND starts_on>=public.privacy_membership_history_effective_date(execution_ref,subject_ref);
  GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
  UPDATE user_memberships SET ends_on=public.privacy_membership_history_effective_date(execution_ref,subject_ref)-1,updated_at=clock_timestamp()
   WHERE user_id=subject_ref AND starts_on<public.privacy_membership_history_effective_date(execution_ref,subject_ref)
    AND (ends_on IS NULL OR ends_on>=public.privacy_membership_history_effective_date(execution_ref,subject_ref));
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
 WHEN 'MEMBERSHIP_ACTIVE_REVOKE' THEN IF EXISTS(SELECT 1 FROM user_memberships WHERE user_id=subject_ref AND (starts_on>=public.privacy_membership_history_effective_date(execution_ref,subject_ref) OR ends_on IS NULL OR ends_on>=public.privacy_membership_history_effective_date(execution_ref,subject_ref))) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_relational_verification_failed'; END IF;
 WHEN 'MEMBERSHIP_HISTORY_ANONYMIZE' THEN
  IF EXISTS(SELECT 1 FROM user_memberships WHERE user_id=subject_ref)
   OR EXISTS(SELECT 1 FROM user_memberships WHERE principal_id=principal_ref AND (starts_on>=public.privacy_membership_history_effective_date(execution_ref,subject_ref) OR ends_on IS NULL OR ends_on>=public.privacy_membership_history_effective_date(execution_ref,subject_ref)))
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
REVOKE ALL ON FUNCTION privacy_worker_execute_checkpoint_without_tombstone_guard(uuid,uuid,uuid,bigint,uuid,text,text) FROM PUBLIC;
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


ALTER TABLE email_outbox ADD COLUMN privacy_request_id uuid NULL REFERENCES data_erasure_requests(id) ON DELETE RESTRICT;
ALTER TABLE email_outbox ADD COLUMN privacy_requester_id uuid NULL REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE email_outbox ADD COLUMN privacy_event_key uuid NULL;
ALTER TABLE email_outbox DROP CONSTRAINT email_outbox_message_valid;
ALTER TABLE email_outbox ADD CONSTRAINT email_outbox_message_valid CHECK (
 (message_type = 'EMAIL_VERIFICATION' AND verification_token_id IS NOT NULL AND password_reset_token_id IS NULL AND sealed_payload IS NULL AND privacy_request_id IS NULL AND privacy_requester_id IS NULL AND privacy_event_key IS NULL)
 OR (message_type = 'PASSWORD_RESET' AND verification_token_id IS NULL AND password_reset_token_id IS NOT NULL AND sealed_payload IS NOT NULL AND privacy_request_id IS NULL AND privacy_requester_id IS NULL AND privacy_event_key IS NULL)
 OR (message_type IN ('PRIVACY_ACKNOWLEDGEMENT','PRIVACY_DECISION','PRIVACY_PROCESSING_STARTED') AND verification_token_id IS NULL AND password_reset_token_id IS NULL AND sealed_payload IS NOT NULL AND privacy_request_id IS NOT NULL AND privacy_event_key IS NOT NULL)
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
CREATE FUNCTION expire_privacy_request_records(p_actor uuid) RETURNS TABLE(working_records integer,evidence_records integer) LANGUAGE plpgsql AS $$
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
-- Durable upload provenance and fenced cleanup for the future #246 object
-- executor. The routines remain unused unless separately configured by the
-- application; this migration does not activate privacy execution or cleanup.

CREATE UNIQUE INDEX IF NOT EXISTS privacy_upload_intent_prepared_uidx ON privacy_protected.object_upload_intent_events(intent_id) WHERE status='PREPARED';
CREATE UNIQUE INDEX IF NOT EXISTS privacy_upload_intent_put_uidx ON privacy_protected.object_upload_intent_events(intent_id) WHERE status='PUT_CONFIRMED';
CREATE UNIQUE INDEX IF NOT EXISTS privacy_upload_intent_attached_uidx ON privacy_protected.object_upload_intent_events(intent_id) WHERE status='ATTACHED';
CREATE UNIQUE INDEX IF NOT EXISTS privacy_upload_intent_cleanup_uidx ON privacy_protected.object_upload_intent_events(intent_id) WHERE status='CLEANUP_REQUIRED';
CREATE UNIQUE INDEX IF NOT EXISTS privacy_upload_intent_absent_uidx ON privacy_protected.object_upload_intent_events(intent_id) WHERE status='ABSENCE_VERIFIED';

CREATE TABLE IF NOT EXISTS privacy_protected.object_upload_intent_reservations (
 id uuid PRIMARY KEY,
 subject_user_id uuid NULL REFERENCES public.users(id) ON DELETE RESTRICT,
 provenance_actor_user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE RESTRICT,
 source_kind varchar(40) NOT NULL CHECK (source_kind IN ('MEMBER_PROFILE_PHOTO','REPAIR_ATTACHMENT','EQUIPMENT_PHOTO')),
 source_ref uuid NOT NULL,
 service_code varchar(120) NOT NULL CHECK (service_code='private-media'),
 content_type varchar(100) NOT NULL CHECK (content_type IN ('image/jpeg','image/png','image/webp')),
 size_bytes bigint NOT NULL CHECK (size_bytes BETWEEN 1 AND 10485760),
 token_digest bytea NOT NULL CHECK (octet_length(token_digest)=32),
 created_at timestamptz NOT NULL,
 cleanup_after timestamptz NOT NULL,
 finalized_at timestamptz NULL,
 CHECK (cleanup_after=created_at+interval '24 hours'),
 CHECK ((source_kind IN ('MEMBER_PROFILE_PHOTO','REPAIR_ATTACHMENT') AND subject_user_id IS NOT NULL)
    OR (source_kind='EQUIPMENT_PHOTO' AND subject_user_id IS NULL)),
 UNIQUE(source_kind,source_ref,id)
);

CREATE TABLE IF NOT EXISTS privacy_protected.object_upload_intent_holds (
 intent_id uuid PRIMARY KEY REFERENCES privacy_protected.object_upload_intent_reservations(id) ON DELETE RESTRICT,
 hold_epoch bigint NOT NULL DEFAULT 1 CHECK (hold_epoch>0),
 token_digest bytea NOT NULL CHECK (octet_length(token_digest)=32),
 held_until timestamptz NOT NULL,
 released_at timestamptz NULL,
 updated_at timestamptz NOT NULL,
 CHECK (released_at IS NULL OR released_at<=updated_at)
);

CREATE TABLE IF NOT EXISTS privacy_protected.object_upload_cleanup_jobs (
 intent_id uuid PRIMARY KEY REFERENCES privacy_protected.object_upload_intents(id) ON DELETE RESTRICT,
 status varchar(20) NOT NULL CHECK (status IN ('PENDING','LEASED','RETRY_WAIT','SUCCEEDED','TERMINAL_FAILED')),
 lease_epoch bigint NOT NULL DEFAULT 0 CHECK (lease_epoch>=0),
 attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count>=0),
 next_attempt_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS privacy_protected.object_upload_cleanup_attempts (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 intent_id uuid NOT NULL REFERENCES privacy_protected.object_upload_cleanup_jobs(intent_id) ON DELETE RESTRICT,
 lease_epoch bigint NOT NULL CHECK (lease_epoch>0),
 worker_ref uuid NOT NULL,
 acquired_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL,
 released_at timestamptz NULL,
 outcome varchar(30) NULL CHECK (outcome IN ('SUCCEEDED','RETRYABLE_FAILED','TERMINAL_FAILED','LEASE_EXPIRED')),
 CHECK (expires_at>acquired_at),
 CHECK ((released_at IS NULL)=(outcome IS NULL)),
 UNIQUE(intent_id,lease_epoch),
 UNIQUE(id,intent_id,lease_epoch)
);

CREATE TABLE IF NOT EXISTS privacy_protected.object_upload_absence_evidence (
 intent_id uuid PRIMARY KEY REFERENCES privacy_protected.object_upload_intents(id) ON DELETE RESTRICT,
 attempt_id uuid NOT NULL,
 lease_epoch bigint NOT NULL,
 evidence_version varchar(40) NOT NULL CHECK (evidence_version='s3-absence/v1'),
 deleted_version_count integer NOT NULL CHECK (deleted_version_count>=0),
 deleted_marker_count integer NOT NULL CHECK (deleted_marker_count>=0),
 list_call_count integer NOT NULL CHECK (list_call_count>=2),
 stable_empty_check_count integer NOT NULL CHECK (stable_empty_check_count>=2),
 transcript_key_id varchar(80) NOT NULL,
 transcript_digest bytea NOT NULL CHECK (octet_length(transcript_digest)=32),
 occurred_at timestamptz NOT NULL,
 FOREIGN KEY(attempt_id,intent_id,lease_epoch) REFERENCES privacy_protected.object_upload_cleanup_attempts(id,intent_id,lease_epoch) ON DELETE RESTRICT
);

DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='privacy_protected.object_upload_cleanup_attempts'::regclass AND tgname='privacy_upload_cleanup_attempts_no_delete') THEN
  CREATE TRIGGER privacy_upload_cleanup_attempts_no_delete BEFORE DELETE ON privacy_protected.object_upload_cleanup_attempts FOR EACH ROW EXECUTE FUNCTION public.prevent_privacy_execution_record_delete();
 END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='privacy_protected.object_upload_absence_evidence'::regclass AND tgname='privacy_upload_absence_evidence_immutable') THEN
  CREATE TRIGGER privacy_upload_absence_evidence_immutable BEFORE UPDATE OR DELETE ON privacy_protected.object_upload_absence_evidence FOR EACH ROW EXECUTE FUNCTION public.prevent_privacy_execution_record_delete();
 END IF;
END; $$;

CREATE OR REPLACE FUNCTION public.privacy_upload_source_lock(p_source_kind text,p_source_ref uuid) RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT pg_advisory_xact_lock(hashtextextended('mycfc/object-source/v1:'||p_source_kind||':'||p_source_ref::text,0));
$$;

CREATE OR REPLACE FUNCTION public.privacy_upload_pointer_references(p_intent_id uuid,p_source_kind text,p_source_ref uuid) RETURNS boolean
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT CASE p_source_kind
  WHEN 'MEMBER_PROFILE_PHOTO' THEN EXISTS(SELECT 1 FROM public.member_profiles p WHERE p.user_id=p_source_ref AND p.photo_upload_intent_id=p_intent_id)
  WHEN 'REPAIR_ATTACHMENT' THEN EXISTS(SELECT 1 FROM public.repair_requests p WHERE p.id=p_source_ref AND p.image_upload_intent_id=p_intent_id)
  WHEN 'EQUIPMENT_PHOTO' THEN EXISTS(SELECT 1 FROM public.equipment p WHERE p.id=p_source_ref AND p.image_upload_intent_id=p_intent_id)
  ELSE false END;
$$;

CREATE OR REPLACE FUNCTION public.privacy_upload_pointer_matches(
 p_intent_id uuid,p_source_kind text,p_source_ref uuid,p_locator_commitment bytea,p_content_type text,p_size_bytes bigint
) RETURNS boolean LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT CASE p_source_kind
  WHEN 'MEMBER_PROFILE_PHOTO' THEN EXISTS(SELECT 1 FROM public.member_profiles p WHERE p.user_id=p_source_ref AND p.photo_upload_intent_id=p_intent_id
    AND digest(convert_to(p.photo_object_key,'UTF8'),'sha256')=p_locator_commitment AND p.photo_content_type=p_content_type AND p.photo_size_bytes=p_size_bytes)
  WHEN 'REPAIR_ATTACHMENT' THEN EXISTS(SELECT 1 FROM public.repair_requests p WHERE p.id=p_source_ref AND p.image_upload_intent_id=p_intent_id
    AND digest(convert_to(p.image_object_key,'UTF8'),'sha256')=p_locator_commitment AND p.image_content_type=p_content_type AND p.image_size_bytes=p_size_bytes)
  WHEN 'EQUIPMENT_PHOTO' THEN EXISTS(SELECT 1 FROM public.equipment p WHERE p.id=p_source_ref AND p.image_upload_intent_id=p_intent_id
    AND digest(convert_to(p.image_object_key,'UTF8'),'sha256')=p_locator_commitment AND p.image_content_type=p_content_type AND p.image_size_bytes=p_size_bytes)
  ELSE false END;
$$;

CREATE OR REPLACE FUNCTION public.privacy_upload_enforce_pointer_mutation() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE
 v_source_kind text; v_old_source_ref uuid; v_current_source_ref uuid; v_old_intent uuid; v_old_key text; v_old_content_type text; v_old_size bigint;
 v_current_intent uuid; v_current_key text; v_current_content_type text; v_current_size bigint;
 v_latest_status text; r privacy_protected.object_upload_intents%ROWTYPE;
BEGIN
 CASE TG_TABLE_NAME
  WHEN 'member_profiles' THEN
   v_source_kind:='MEMBER_PROFILE_PHOTO';
   IF TG_OP='INSERT' THEN v_current_source_ref:=NEW.user_id;
   ELSE
    v_old_source_ref:=OLD.user_id; v_current_source_ref:=CASE WHEN TG_OP='DELETE' THEN OLD.user_id ELSE NEW.user_id END;
    v_old_intent:=OLD.photo_upload_intent_id; v_old_key:=OLD.photo_object_key;
    v_old_content_type:=OLD.photo_content_type; v_old_size:=OLD.photo_size_bytes;
    IF TG_OP='UPDATE' AND ROW(OLD.user_id,OLD.photo_object_key,OLD.photo_content_type,OLD.photo_size_bytes,OLD.photo_upload_intent_id)
      IS NOT DISTINCT FROM ROW(NEW.user_id,NEW.photo_object_key,NEW.photo_content_type,NEW.photo_size_bytes,NEW.photo_upload_intent_id) THEN RETURN NULL; END IF;
   END IF;
   SELECT photo_upload_intent_id,photo_object_key,photo_content_type,photo_size_bytes
    INTO v_current_intent,v_current_key,v_current_content_type,v_current_size FROM public.member_profiles WHERE user_id=v_current_source_ref;
  WHEN 'repair_requests' THEN
   v_source_kind:='REPAIR_ATTACHMENT';
   IF TG_OP='INSERT' THEN v_current_source_ref:=NEW.id;
   ELSE
    v_old_source_ref:=OLD.id; v_current_source_ref:=CASE WHEN TG_OP='DELETE' THEN OLD.id ELSE NEW.id END;
    v_old_intent:=OLD.image_upload_intent_id; v_old_key:=OLD.image_object_key;
    v_old_content_type:=OLD.image_content_type; v_old_size:=OLD.image_size_bytes;
    IF TG_OP='UPDATE' AND ROW(OLD.id,OLD.image_object_key,OLD.image_content_type,OLD.image_size_bytes,OLD.image_upload_intent_id)
      IS NOT DISTINCT FROM ROW(NEW.id,NEW.image_object_key,NEW.image_content_type,NEW.image_size_bytes,NEW.image_upload_intent_id) THEN RETURN NULL; END IF;
   END IF;
   SELECT image_upload_intent_id,image_object_key,image_content_type,image_size_bytes
    INTO v_current_intent,v_current_key,v_current_content_type,v_current_size FROM public.repair_requests WHERE id=v_current_source_ref;
  WHEN 'equipment' THEN
   v_source_kind:='EQUIPMENT_PHOTO';
   IF TG_OP='INSERT' THEN v_current_source_ref:=NEW.id;
   ELSE
    v_old_source_ref:=OLD.id; v_current_source_ref:=CASE WHEN TG_OP='DELETE' THEN OLD.id ELSE NEW.id END;
    v_old_intent:=OLD.image_upload_intent_id; v_old_key:=OLD.image_object_key;
    v_old_content_type:=OLD.image_content_type; v_old_size:=OLD.image_size_bytes;
    IF TG_OP='UPDATE' AND ROW(OLD.id,OLD.image_object_key,OLD.image_content_type,OLD.image_size_bytes,OLD.image_upload_intent_id)
      IS NOT DISTINCT FROM ROW(NEW.id,NEW.image_object_key,NEW.image_content_type,NEW.image_size_bytes,NEW.image_upload_intent_id) THEN RETURN NULL; END IF;
   END IF;
   SELECT image_upload_intent_id,image_object_key,image_content_type,image_size_bytes
    INTO v_current_intent,v_current_key,v_current_content_type,v_current_size FROM public.equipment WHERE id=v_current_source_ref;
  ELSE RAISE EXCEPTION 'unsupported upload pointer table';
 END CASE;
 IF v_old_key IS NOT NULL AND v_old_intent IS NULL THEN RAISE EXCEPTION 'legacy upload pointer requires migration'; END IF;
 IF v_current_key IS NOT NULL AND v_current_intent IS NULL THEN RAISE EXCEPTION 'upload pointer requires provenance'; END IF;
 IF v_current_key IS NULL AND v_current_intent IS NOT NULL THEN RAISE EXCEPTION 'upload pointer invariant rejected'; END IF;
 IF v_current_intent IS NOT NULL THEN
  SELECT * INTO r FROM privacy_protected.object_upload_intents WHERE id=v_current_intent;
  SELECT status INTO v_latest_status FROM privacy_protected.object_upload_intent_events WHERE intent_id=v_current_intent ORDER BY sequence DESC LIMIT 1;
  IF r.id IS NULL OR r.source_kind<>v_source_kind OR r.source_ref<>v_current_source_ref OR v_latest_status<>'ATTACHED'
    OR NOT public.privacy_upload_pointer_matches(r.id,r.source_kind,r.source_ref,r.locator_commitment,r.content_type,r.size_bytes) THEN
   RAISE EXCEPTION 'upload pointer invariant rejected';
  END IF;
 END IF;
 IF v_old_intent IS NOT NULL AND v_old_intent IS DISTINCT FROM v_current_intent THEN
  SELECT status INTO v_latest_status FROM privacy_protected.object_upload_intent_events WHERE intent_id=v_old_intent ORDER BY sequence DESC LIMIT 1;
  IF v_latest_status NOT IN ('CLEANUP_REQUIRED','ABSENCE_VERIFIED')
    OR public.privacy_upload_pointer_references(v_old_intent,v_source_kind,v_old_source_ref) THEN
   RAISE EXCEPTION 'upload pointer cleanup invariant rejected';
  END IF;
 END IF;
 RETURN NULL;
END; $$;

DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='member_profiles'::regclass AND tgname='privacy_upload_profile_pointer_deferred') THEN
  CREATE CONSTRAINT TRIGGER privacy_upload_profile_pointer_deferred AFTER INSERT OR UPDATE OR DELETE ON member_profiles
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.privacy_upload_enforce_pointer_mutation();
 END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='repair_requests'::regclass AND tgname='privacy_upload_repair_pointer_deferred') THEN
  CREATE CONSTRAINT TRIGGER privacy_upload_repair_pointer_deferred AFTER INSERT OR UPDATE OR DELETE ON repair_requests
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.privacy_upload_enforce_pointer_mutation();
 END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='equipment'::regclass AND tgname='privacy_upload_equipment_pointer_deferred') THEN
  CREATE CONSTRAINT TRIGGER privacy_upload_equipment_pointer_deferred AFTER INSERT OR UPDATE OR DELETE ON equipment
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.privacy_upload_enforce_pointer_mutation();
 END IF;
END; $$;

CREATE OR REPLACE FUNCTION public.privacy_upload_enforce_pointer_state() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE r privacy_protected.object_upload_intents%ROWTYPE;
BEGIN
 IF NEW.status NOT IN ('ATTACHED','CLEANUP_REQUIRED') OR (NEW.status='CLEANUP_REQUIRED' AND NEW.reason_code NOT IN ('POINTER_SUPERSEDED','POINTER_REMOVED')) THEN RETURN NULL; END IF;
 SELECT * INTO r FROM privacy_protected.object_upload_intents WHERE id=NEW.intent_id;
 IF NEW.status='ATTACHED' AND NOT public.privacy_upload_pointer_matches(r.id,r.source_kind,r.source_ref,r.locator_commitment,r.content_type,r.size_bytes) THEN
  RAISE EXCEPTION 'upload pointer invariant rejected';
 END IF;
 IF NEW.status='CLEANUP_REQUIRED' AND public.privacy_upload_pointer_references(r.id,r.source_kind,r.source_ref) THEN
  RAISE EXCEPTION 'upload pointer invariant rejected';
 END IF;
 RETURN NULL;
END; $$;

DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='privacy_protected.object_upload_intent_events'::regclass AND tgname='privacy_upload_pointer_state_deferred') THEN
  CREATE CONSTRAINT TRIGGER privacy_upload_pointer_state_deferred AFTER INSERT ON privacy_protected.object_upload_intent_events
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.privacy_upload_enforce_pointer_state();
 END IF;
END; $$;

CREATE OR REPLACE FUNCTION public.privacy_upload_begin(
 p_intent_id uuid,p_subject_user_id uuid,p_actor_user_id uuid,p_source_kind text,p_source_ref uuid,
 p_service_code text,p_content_type text,p_size_bytes bigint,p_token bytea
) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE v_now timestamptz:=clock_timestamp(); v_token_digest bytea;
BEGIN
 IF p_intent_id IS NULL OR p_actor_user_id IS NULL OR p_source_ref IS NULL OR octet_length(p_token)<>32 THEN RAISE EXCEPTION 'invalid upload reservation'; END IF;
 IF p_source_kind NOT IN ('MEMBER_PROFILE_PHOTO','REPAIR_ATTACHMENT','EQUIPMENT_PHOTO') OR p_service_code<>'private-media'
    OR p_content_type NOT IN ('image/jpeg','image/png','image/webp') OR p_size_bytes NOT BETWEEN 1 AND 10485760 THEN RAISE EXCEPTION 'invalid upload reservation'; END IF;
 IF p_source_kind IN ('MEMBER_PROFILE_PHOTO','REPAIR_ATTACHMENT') AND p_subject_user_id IS DISTINCT FROM p_actor_user_id THEN RAISE EXCEPTION 'upload actor is not authorized'; END IF;
 IF p_source_kind='EQUIPMENT_PHOTO' AND (p_subject_user_id IS NOT NULL OR NOT EXISTS(
   SELECT 1 FROM public.users u JOIN public.user_platform_roles ur ON ur.user_id=u.id JOIN public.platform_roles r ON r.id=ur.role_id
   WHERE u.id=p_actor_user_id AND u.is_active AND u.erased_at IS NULL AND NOT u.is_dependent AND r.code='ADMIN')) THEN RAISE EXCEPTION 'upload actor is not authorized'; END IF;
 IF p_source_kind='MEMBER_PROFILE_PHOTO' AND p_source_ref<>p_subject_user_id THEN RAISE EXCEPTION 'invalid upload source'; END IF;
 PERFORM public.privacy_upload_source_lock(p_source_kind,p_source_ref);
 v_token_digest:=digest(p_token,'sha256');
 INSERT INTO privacy_protected.object_upload_intent_reservations(id,subject_user_id,provenance_actor_user_id,source_kind,source_ref,service_code,content_type,size_bytes,token_digest,created_at,cleanup_after)
 VALUES(p_intent_id,p_subject_user_id,p_actor_user_id,p_source_kind,p_source_ref,p_service_code,p_content_type,p_size_bytes,v_token_digest,v_now,v_now+interval '24 hours');
 INSERT INTO privacy_protected.object_upload_intent_holds(intent_id,hold_epoch,token_digest,held_until,updated_at)
 VALUES(p_intent_id,1,v_token_digest,v_now+interval '15 minutes',v_now);
 RETURN jsonb_build_object('created_at',v_now,'cleanup_after',v_now+interval '24 hours','hold_epoch',1);
END; $$;

CREATE OR REPLACE FUNCTION public.privacy_upload_finalize(
 p_intent_id uuid,p_token bytea,p_envelope_version text,p_algorithm text,p_encryption_key_id text,
 p_encapsulation bytea,p_nonce bytea,p_ciphertext bytea,p_digest_key_id text,p_locator_digest bytea,p_locator_commitment bytea
) RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE r privacy_protected.object_upload_intent_reservations%ROWTYPE; h privacy_protected.object_upload_intent_holds%ROWTYPE;
BEGIN
 SELECT * INTO r FROM privacy_protected.object_upload_intent_reservations WHERE id=p_intent_id FOR UPDATE;
 SELECT * INTO h FROM privacy_protected.object_upload_intent_holds WHERE intent_id=p_intent_id FOR UPDATE;
 IF r.id IS NULL OR h.intent_id IS NULL OR r.finalized_at IS NOT NULL OR h.released_at IS NOT NULL OR h.held_until<=clock_timestamp()
    OR p_token IS NULL OR octet_length(p_token)<>32 OR h.token_digest IS DISTINCT FROM digest(p_token,'sha256') OR r.token_digest<>h.token_digest
    OR p_locator_commitment IS NULL OR octet_length(p_locator_commitment)<>32 THEN RAISE EXCEPTION 'upload reservation is unavailable'; END IF;
 INSERT INTO privacy_protected.object_upload_intents(id,subject_user_id,provenance_actor_user_id,source_kind,source_ref,service_code,target_kind,provider_contract_version,
  envelope_version,algorithm,encryption_key_id,encapsulation,nonce,ciphertext,digest_key_id,locator_digest,locator_commitment,content_type,size_bytes,cleanup_after,created_at)
 VALUES(r.id,r.subject_user_id,r.provenance_actor_user_id,r.source_kind,r.source_ref,r.service_code,'OBJECT_KEY','s3-versioned/v1',
  p_envelope_version,p_algorithm,p_encryption_key_id,p_encapsulation,p_nonce,p_ciphertext,p_digest_key_id,p_locator_digest,p_locator_commitment,r.content_type,r.size_bytes,r.cleanup_after,r.created_at);
 INSERT INTO privacy_protected.object_upload_intent_events(intent_id,sequence,status,reason_code,occurred_at) VALUES(r.id,1,'PREPARED','UPLOAD_RESERVED',clock_timestamp());
 UPDATE privacy_protected.object_upload_intent_reservations SET finalized_at=clock_timestamp() WHERE id=r.id;
END; $$;

CREATE OR REPLACE FUNCTION public.privacy_upload_confirm_put(p_intent_id uuid,p_token bytea) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE r privacy_protected.object_upload_intents%ROWTYPE; h privacy_protected.object_upload_intent_holds%ROWTYPE; v_status text;
BEGIN
 SELECT * INTO r FROM privacy_protected.object_upload_intents WHERE id=p_intent_id FOR UPDATE;
 IF r.id IS NULL THEN RAISE EXCEPTION 'upload intent is unavailable'; END IF;
 PERFORM public.privacy_upload_source_lock(r.source_kind,r.source_ref);
 SELECT * INTO h FROM privacy_protected.object_upload_intent_holds WHERE intent_id=p_intent_id FOR UPDATE;
 SELECT status INTO v_status FROM privacy_protected.object_upload_intent_events WHERE intent_id=p_intent_id ORDER BY sequence DESC LIMIT 1;
 IF p_token IS NULL OR octet_length(p_token)<>32 OR h.token_digest IS DISTINCT FROM digest(p_token,'sha256') OR h.released_at IS NOT NULL OR h.held_until<=clock_timestamp() OR v_status<>'PREPARED' THEN RAISE EXCEPTION 'upload intent transition rejected'; END IF;
 INSERT INTO privacy_protected.object_upload_intent_events VALUES(p_intent_id,2,'PUT_CONFIRMED','PUT_ACKNOWLEDGED',clock_timestamp());
 UPDATE privacy_protected.object_upload_intent_holds SET held_until=clock_timestamp()+interval '15 minutes',updated_at=clock_timestamp() WHERE intent_id=p_intent_id;
END; $$;

CREATE OR REPLACE FUNCTION public.privacy_upload_mark_cleanup(p_intent_id uuid,p_token bytea,p_reason text) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE r privacy_protected.object_upload_intents%ROWTYPE; h privacy_protected.object_upload_intent_holds%ROWTYPE; v_status text; v_sequence integer;
BEGIN
 SELECT * INTO r FROM privacy_protected.object_upload_intents WHERE id=p_intent_id FOR UPDATE;
 IF r.id IS NULL THEN RAISE EXCEPTION 'upload intent is unavailable'; END IF;
 PERFORM public.privacy_upload_source_lock(r.source_kind,r.source_ref);
 SELECT * INTO h FROM privacy_protected.object_upload_intent_holds WHERE intent_id=p_intent_id FOR UPDATE;
 IF p_token IS NULL OR octet_length(p_token)<>32 OR h.token_digest IS DISTINCT FROM digest(p_token,'sha256') THEN RAISE EXCEPTION 'upload intent transition rejected'; END IF;
 SELECT status,sequence INTO v_status,v_sequence FROM privacy_protected.object_upload_intent_events WHERE intent_id=p_intent_id ORDER BY sequence DESC LIMIT 1;
 IF v_status='CLEANUP_REQUIRED' THEN RETURN; END IF;
 IF p_reason NOT IN ('PUT_AMBIGUOUS','PUT_FAILED','ATTACH_FAILED','POINTER_SUPERSEDED','POINTER_REMOVED')
   OR (v_status='PREPARED' AND p_reason NOT IN ('PUT_AMBIGUOUS','PUT_FAILED'))
   OR (v_status='PUT_CONFIRMED' AND p_reason NOT IN ('PUT_AMBIGUOUS','ATTACH_FAILED'))
   OR (v_status='ATTACHED' AND p_reason NOT IN ('POINTER_SUPERSEDED','POINTER_REMOVED')) THEN RAISE EXCEPTION 'upload intent transition rejected'; END IF;
 INSERT INTO privacy_protected.object_upload_intent_events VALUES(p_intent_id,v_sequence+1,'CLEANUP_REQUIRED',p_reason,clock_timestamp());
 UPDATE privacy_protected.object_upload_intent_holds SET released_at=clock_timestamp(),updated_at=clock_timestamp() WHERE intent_id=p_intent_id AND released_at IS NULL;
 INSERT INTO privacy_protected.object_upload_cleanup_jobs(intent_id,status,next_attempt_at,updated_at) VALUES(p_intent_id,'PENDING',clock_timestamp(),clock_timestamp()) ON CONFLICT(intent_id) DO NOTHING;
END; $$;

CREATE OR REPLACE FUNCTION public.privacy_upload_attach(
 p_intent_id uuid,p_token bytea,p_prior_intent_id uuid,p_expected_source_kind text,p_expected_source_ref uuid,
 p_object_key text,p_content_type text,p_size_bytes bigint
) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE r privacy_protected.object_upload_intents%ROWTYPE; h privacy_protected.object_upload_intent_holds%ROWTYPE; v_status text; v_sequence integer; p_status text; p_sequence integer; v_pointer_attached boolean:=false;
BEGIN
 IF p_intent_id IS NULL AND p_token IS NULL THEN RETURN; END IF;
 SELECT * INTO r FROM privacy_protected.object_upload_intents WHERE id=p_intent_id FOR UPDATE;
 IF r.id IS NULL THEN RAISE EXCEPTION 'upload intent is unavailable'; END IF;
 IF r.source_kind<>p_expected_source_kind OR r.source_ref<>p_expected_source_ref THEN RAISE EXCEPTION 'upload intent source mismatch'; END IF;
 IF p_object_key IS NULL OR digest(convert_to(p_object_key,'UTF8'),'sha256') IS DISTINCT FROM r.locator_commitment
    OR p_content_type IS DISTINCT FROM r.content_type OR p_size_bytes IS DISTINCT FROM r.size_bytes THEN RAISE EXCEPTION 'upload intent pointer mismatch'; END IF;
 PERFORM public.privacy_upload_source_lock(r.source_kind,r.source_ref);
 SELECT * INTO h FROM privacy_protected.object_upload_intent_holds WHERE intent_id=p_intent_id FOR UPDATE;
 SELECT status,sequence INTO v_status,v_sequence FROM privacy_protected.object_upload_intent_events WHERE intent_id=p_intent_id ORDER BY sequence DESC LIMIT 1;
 IF p_token IS NULL OR octet_length(p_token)<>32 OR h.token_digest IS DISTINCT FROM digest(p_token,'sha256') THEN RAISE EXCEPTION 'upload intent transition rejected'; END IF;
 IF v_status='ATTACHED' THEN
  v_pointer_attached:=CASE r.source_kind
   WHEN 'MEMBER_PROFILE_PHOTO' THEN EXISTS(SELECT 1 FROM public.member_profiles p WHERE p.user_id=r.source_ref AND p.photo_upload_intent_id=r.id)
   WHEN 'REPAIR_ATTACHMENT' THEN EXISTS(SELECT 1 FROM public.repair_requests p WHERE p.id=r.source_ref AND p.image_upload_intent_id=r.id)
   WHEN 'EQUIPMENT_PHOTO' THEN EXISTS(SELECT 1 FROM public.equipment p WHERE p.id=r.source_ref AND p.image_upload_intent_id=r.id)
   ELSE false END;
  IF v_pointer_attached THEN RETURN; END IF;
  RAISE EXCEPTION 'upload intent transition rejected';
 END IF;
 IF v_status<>'PUT_CONFIRMED' OR r.cleanup_after<=clock_timestamp() OR h.released_at IS NOT NULL OR h.held_until<=clock_timestamp() THEN RAISE EXCEPTION 'upload intent transition rejected'; END IF;
 IF r.source_kind='MEMBER_PROFILE_PHOTO' AND EXISTS(SELECT 1 FROM public.member_profiles p WHERE p.user_id=r.source_ref AND p.photo_object_key IS NOT NULL AND p.photo_upload_intent_id IS NULL) THEN RAISE EXCEPTION 'legacy upload pointer requires migration'; END IF;
 IF r.source_kind='EQUIPMENT_PHOTO' AND EXISTS(SELECT 1 FROM public.equipment p WHERE p.id=r.source_ref AND p.image_object_key IS NOT NULL AND p.image_upload_intent_id IS NULL) THEN RAISE EXCEPTION 'legacy upload pointer requires migration'; END IF;
 INSERT INTO privacy_protected.object_upload_intent_events VALUES(p_intent_id,v_sequence+1,'ATTACHED','POINTER_ATTACHED',clock_timestamp());
 UPDATE privacy_protected.object_upload_intent_holds SET released_at=clock_timestamp(),updated_at=clock_timestamp() WHERE intent_id=p_intent_id;
 IF p_prior_intent_id IS NOT NULL AND p_prior_intent_id<>p_intent_id THEN
  SELECT e.status,e.sequence INTO p_status,p_sequence FROM privacy_protected.object_upload_intent_events e
  JOIN privacy_protected.object_upload_intents prior ON prior.id=e.intent_id
  WHERE e.intent_id=p_prior_intent_id AND prior.source_kind=r.source_kind AND prior.source_ref=r.source_ref
  ORDER BY e.sequence DESC LIMIT 1 FOR UPDATE OF e;
  IF p_status<>'ATTACHED' THEN RAISE EXCEPTION 'prior upload intent transition rejected'; END IF;
  INSERT INTO privacy_protected.object_upload_intent_events VALUES(p_prior_intent_id,p_sequence+1,'CLEANUP_REQUIRED','POINTER_SUPERSEDED',clock_timestamp());
  INSERT INTO privacy_protected.object_upload_cleanup_jobs(intent_id,status,next_attempt_at,updated_at) VALUES(p_prior_intent_id,'PENDING',clock_timestamp(),clock_timestamp()) ON CONFLICT(intent_id) DO NOTHING;
 END IF;
END; $$;

CREATE OR REPLACE FUNCTION public.privacy_upload_remove(p_intent_id uuid,p_actor_user_id uuid,p_expected_source_kind text,p_expected_source_ref uuid) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE r privacy_protected.object_upload_intents%ROWTYPE; v_status text; v_sequence integer;
BEGIN
 IF p_intent_id IS NULL OR p_actor_user_id IS NULL OR p_expected_source_kind<>'MEMBER_PROFILE_PHOTO' THEN RAISE EXCEPTION 'upload removal rejected'; END IF;
 SELECT * INTO r FROM privacy_protected.object_upload_intents WHERE id=p_intent_id FOR UPDATE;
 IF r.id IS NULL THEN RAISE EXCEPTION 'upload intent is unavailable'; END IF;
 IF r.source_kind<>p_expected_source_kind OR r.source_ref<>p_expected_source_ref THEN RAISE EXCEPTION 'upload removal rejected'; END IF;
 IF p_actor_user_id<>r.source_ref AND NOT EXISTS(
   SELECT 1 FROM public.users subject WHERE subject.id=r.source_ref AND subject.guardian_id=p_actor_user_id
   UNION ALL
   SELECT 1 FROM public.user_platform_roles ur JOIN public.platform_roles role ON role.id=ur.role_id
   JOIN public.users actor ON actor.id=ur.user_id WHERE ur.user_id=p_actor_user_id AND role.code='ADMIN' AND actor.is_active AND actor.erased_at IS NULL
  ) THEN RAISE EXCEPTION 'upload removal rejected'; END IF;
 PERFORM public.privacy_upload_source_lock(r.source_kind,r.source_ref);
 SELECT status,sequence INTO v_status,v_sequence FROM privacy_protected.object_upload_intent_events WHERE intent_id=p_intent_id ORDER BY sequence DESC LIMIT 1;
 IF v_status='CLEANUP_REQUIRED' THEN RETURN; END IF;
 IF v_status<>'ATTACHED' THEN RAISE EXCEPTION 'upload intent transition rejected'; END IF;
 INSERT INTO privacy_protected.object_upload_intent_events VALUES(p_intent_id,v_sequence+1,'CLEANUP_REQUIRED','POINTER_REMOVED',clock_timestamp());
 INSERT INTO privacy_protected.object_upload_cleanup_jobs(intent_id,status,next_attempt_at,updated_at) VALUES(p_intent_id,'PENDING',clock_timestamp(),clock_timestamp()) ON CONFLICT(intent_id) DO NOTHING;
END; $$;

CREATE OR REPLACE FUNCTION public.privacy_upload_cleanup_claim(p_lease_milliseconds bigint,p_worker_ref uuid)
RETURNS TABLE(
 intent_id uuid,lease_epoch bigint,attempt_id uuid,attempt_count integer,
 subject_user_id uuid,provenance_actor_user_id uuid,source_kind text,source_ref uuid,
 service_code text,target_kind text,provider_contract_version text,envelope_version text,
 algorithm text,encryption_key_id text,encapsulation bytea,nonce bytea,ciphertext bytea,
 content_type text,size_bytes bigint,cleanup_after timestamptz,created_at timestamptz
) LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE v_now timestamptz:=clock_timestamp(); v_intent uuid; v_source_kind text; v_source_ref uuid;
 v_status text; v_sequence integer; v_epoch bigint; v_attempt uuid; v_attempt_count integer;
BEGIN
 IF p_worker_ref IS NULL OR p_lease_milliseconds<1000 OR p_lease_milliseconds>3600000 THEN RAISE EXCEPTION 'invalid cleanup claim'; END IF;

 SELECT i.id,i.source_kind,i.source_ref INTO v_intent,v_source_kind,v_source_ref
 FROM privacy_protected.object_upload_intents i
 JOIN LATERAL (SELECT e.status FROM privacy_protected.object_upload_intent_events e WHERE e.intent_id=i.id ORDER BY e.sequence DESC LIMIT 1) latest ON true
 JOIN privacy_protected.object_upload_intent_holds h ON h.intent_id=i.id
 WHERE latest.status IN ('PREPARED','PUT_CONFIRMED') AND i.cleanup_after<=v_now
   AND NOT (h.released_at IS NULL AND h.held_until>v_now)
 ORDER BY i.cleanup_after,i.id FOR UPDATE OF i SKIP LOCKED LIMIT 1;
 IF v_intent IS NOT NULL THEN
  PERFORM public.privacy_upload_source_lock(v_source_kind,v_source_ref);
  SELECT e.status,e.sequence INTO v_status,v_sequence FROM privacy_protected.object_upload_intent_events e WHERE e.intent_id=v_intent ORDER BY e.sequence DESC LIMIT 1;
  IF v_status IN ('PREPARED','PUT_CONFIRMED') AND EXISTS(
    SELECT 1 FROM privacy_protected.object_upload_intent_holds h WHERE h.intent_id=v_intent AND NOT (h.released_at IS NULL AND h.held_until>v_now)
  ) THEN
   INSERT INTO privacy_protected.object_upload_intent_events VALUES(v_intent,v_sequence+1,'CLEANUP_REQUIRED','STALE_TIMEOUT',v_now);
   UPDATE privacy_protected.object_upload_intent_holds SET released_at=COALESCE(released_at,v_now),updated_at=v_now WHERE object_upload_intent_holds.intent_id=v_intent;
   INSERT INTO privacy_protected.object_upload_cleanup_jobs(intent_id,status,next_attempt_at,updated_at) VALUES(v_intent,'PENDING',v_now,v_now) ON CONFLICT ON CONSTRAINT object_upload_cleanup_jobs_pkey DO NOTHING;
  END IF;
 END IF;

 v_intent:=NULL;
 SELECT j.intent_id,i.source_kind,i.source_ref,j.lease_epoch,j.attempt_count
 INTO v_intent,v_source_kind,v_source_ref,v_epoch,v_attempt_count
 FROM privacy_protected.object_upload_cleanup_jobs j
 JOIN privacy_protected.object_upload_intents i ON i.id=j.intent_id
 LEFT JOIN LATERAL (
  SELECT a.expires_at,a.released_at FROM privacy_protected.object_upload_cleanup_attempts a
  WHERE a.intent_id=j.intent_id AND a.lease_epoch=j.lease_epoch ORDER BY a.acquired_at DESC LIMIT 1
 ) active_attempt ON true
 WHERE ((j.status IN ('PENDING','RETRY_WAIT') AND j.next_attempt_at<=v_now)
    OR (j.status='LEASED' AND active_attempt.released_at IS NULL AND active_attempt.expires_at<=v_now))
   AND NOT public.privacy_upload_pointer_references(i.id,i.source_kind,i.source_ref)
 ORDER BY j.next_attempt_at,j.intent_id FOR UPDATE OF j SKIP LOCKED LIMIT 1;
 IF v_intent IS NULL THEN RETURN; END IF;
 PERFORM public.privacy_upload_source_lock(v_source_kind,v_source_ref);
 IF public.privacy_upload_pointer_references(v_intent,v_source_kind,v_source_ref) THEN RETURN; END IF;
 SELECT e.status INTO v_status FROM privacy_protected.object_upload_intent_events e WHERE e.intent_id=v_intent ORDER BY e.sequence DESC LIMIT 1;
 IF v_status<>'CLEANUP_REQUIRED' THEN RAISE EXCEPTION 'cleanup claim state rejected'; END IF;
 UPDATE privacy_protected.object_upload_cleanup_attempts a SET released_at=v_now,outcome='LEASE_EXPIRED'
 WHERE a.intent_id=v_intent AND a.lease_epoch=v_epoch AND a.released_at IS NULL AND a.expires_at<=v_now;
 v_epoch:=v_epoch+1; v_attempt:=gen_random_uuid(); v_attempt_count:=v_attempt_count+1;
 INSERT INTO privacy_protected.object_upload_cleanup_attempts(id,intent_id,lease_epoch,worker_ref,acquired_at,expires_at)
 VALUES(v_attempt,v_intent,v_epoch,p_worker_ref,v_now,v_now+(p_lease_milliseconds*interval '1 millisecond'));
 UPDATE privacy_protected.object_upload_cleanup_jobs j SET status='LEASED',lease_epoch=v_epoch,attempt_count=v_attempt_count,updated_at=v_now WHERE j.intent_id=v_intent;
 RETURN QUERY SELECT i.id,v_epoch,v_attempt,v_attempt_count,i.subject_user_id,i.provenance_actor_user_id,i.source_kind::text,i.source_ref,
  i.service_code::text,i.target_kind::text,i.provider_contract_version::text,i.envelope_version::text,i.algorithm::text,
  i.encryption_key_id::text,i.encapsulation,i.nonce,i.ciphertext,i.content_type::text,i.size_bytes,i.cleanup_after,i.created_at
 FROM privacy_protected.object_upload_intents i WHERE i.id=v_intent;
END; $$;

CREATE OR REPLACE FUNCTION public.privacy_upload_cleanup_complete(
 p_intent_id uuid,p_attempt_id uuid,p_lease_epoch bigint,p_worker_ref uuid,
 p_deleted_versions integer,p_deleted_markers integer,p_list_calls integer,p_stable_checks integer,
 p_transcript_key_id text,p_transcript_digest bytea
) RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE v_now timestamptz:=clock_timestamp(); v_status text; v_sequence integer; v_source_kind text; v_source_ref uuid;
BEGIN
 SELECT i.source_kind,i.source_ref INTO v_source_kind,v_source_ref FROM privacy_protected.object_upload_intents i WHERE i.id=p_intent_id FOR UPDATE;
 IF v_source_ref IS NULL THEN RAISE EXCEPTION 'cleanup completion rejected'; END IF;
 PERFORM public.privacy_upload_source_lock(v_source_kind,v_source_ref);
 PERFORM 1 FROM privacy_protected.object_upload_cleanup_jobs j
 JOIN privacy_protected.object_upload_cleanup_attempts a ON a.intent_id=j.intent_id AND a.lease_epoch=j.lease_epoch
 WHERE j.intent_id=p_intent_id AND j.status='LEASED' AND j.lease_epoch=p_lease_epoch
  AND a.id=p_attempt_id AND a.worker_ref=p_worker_ref AND a.released_at IS NULL AND a.expires_at>v_now FOR UPDATE OF j,a;
 IF NOT FOUND OR p_deleted_versions<0 OR p_deleted_markers<0 OR p_list_calls<2 OR p_stable_checks<2
   OR p_transcript_key_id IS NULL OR p_transcript_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$' OR octet_length(p_transcript_digest)<>32 THEN RAISE EXCEPTION 'cleanup completion rejected'; END IF;
 SELECT e.status,e.sequence INTO v_status,v_sequence FROM privacy_protected.object_upload_intent_events e WHERE e.intent_id=p_intent_id ORDER BY e.sequence DESC LIMIT 1;
 IF v_status<>'CLEANUP_REQUIRED' THEN RAISE EXCEPTION 'cleanup completion rejected'; END IF;
 INSERT INTO privacy_protected.object_upload_absence_evidence(intent_id,attempt_id,lease_epoch,evidence_version,deleted_version_count,deleted_marker_count,list_call_count,stable_empty_check_count,transcript_key_id,transcript_digest,occurred_at)
 VALUES(p_intent_id,p_attempt_id,p_lease_epoch,'s3-absence/v1',p_deleted_versions,p_deleted_markers,p_list_calls,p_stable_checks,p_transcript_key_id,p_transcript_digest,v_now);
 INSERT INTO privacy_protected.object_upload_intent_events VALUES(p_intent_id,v_sequence+1,'ABSENCE_VERIFIED','CLEANUP_CONFIRMED',v_now);
 UPDATE privacy_protected.object_upload_cleanup_attempts SET released_at=v_now,outcome='SUCCEEDED' WHERE id=p_attempt_id;
 UPDATE privacy_protected.object_upload_cleanup_jobs SET status='SUCCEEDED',updated_at=v_now WHERE object_upload_cleanup_jobs.intent_id=p_intent_id;
END; $$;

CREATE OR REPLACE FUNCTION public.privacy_upload_cleanup_fail(
 p_intent_id uuid,p_attempt_id uuid,p_lease_epoch bigint,p_worker_ref uuid,p_retryable boolean,p_retry_delay_milliseconds bigint
) RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE v_now timestamptz:=clock_timestamp();
BEGIN
 PERFORM 1 FROM privacy_protected.object_upload_cleanup_jobs j
 JOIN privacy_protected.object_upload_cleanup_attempts a ON a.intent_id=j.intent_id AND a.lease_epoch=j.lease_epoch
 WHERE j.intent_id=p_intent_id AND j.status='LEASED' AND j.lease_epoch=p_lease_epoch
  AND a.id=p_attempt_id AND a.worker_ref=p_worker_ref AND a.released_at IS NULL AND a.expires_at>v_now FOR UPDATE OF j,a;
 IF NOT FOUND OR p_retry_delay_milliseconds<0 OR p_retry_delay_milliseconds>3600000 THEN RAISE EXCEPTION 'cleanup failure rejected'; END IF;
 UPDATE privacy_protected.object_upload_cleanup_attempts SET released_at=v_now,outcome=CASE WHEN p_retryable THEN 'RETRYABLE_FAILED' ELSE 'TERMINAL_FAILED' END WHERE id=p_attempt_id;
 UPDATE privacy_protected.object_upload_cleanup_jobs SET status=CASE WHEN p_retryable THEN 'RETRY_WAIT' ELSE 'TERMINAL_FAILED' END,
  next_attempt_at=v_now+(p_retry_delay_milliseconds*interval '1 millisecond'),updated_at=v_now WHERE object_upload_cleanup_jobs.intent_id=p_intent_id;
END; $$;

REVOKE ALL ON TABLE privacy_protected.object_upload_intent_reservations,privacy_protected.object_upload_intent_holds,
 privacy_protected.object_upload_cleanup_jobs,privacy_protected.object_upload_cleanup_attempts,privacy_protected.object_upload_absence_evidence FROM PUBLIC;
REVOKE ALL ON FUNCTION public.privacy_upload_source_lock(text,uuid),public.privacy_upload_pointer_references(uuid,text,uuid),
 public.privacy_upload_pointer_matches(uuid,text,uuid,bytea,text,bigint),public.privacy_upload_enforce_pointer_mutation(),public.privacy_upload_enforce_pointer_state(),
 public.privacy_upload_begin(uuid,uuid,uuid,text,uuid,text,text,bigint,bytea),
 public.privacy_upload_finalize(uuid,bytea,text,text,text,bytea,bytea,bytea,text,bytea,bytea),public.privacy_upload_confirm_put(uuid,bytea),
 public.privacy_upload_mark_cleanup(uuid,bytea,text),public.privacy_upload_attach(uuid,bytea,uuid,text,uuid,text,text,bigint),public.privacy_upload_remove(uuid,uuid,text,uuid),
 public.privacy_upload_cleanup_claim(bigint,uuid),public.privacy_upload_cleanup_complete(uuid,uuid,bigint,uuid,integer,integer,integer,integer,text,bytea),
 public.privacy_upload_cleanup_fail(uuid,uuid,bigint,uuid,boolean,bigint) FROM PUBLIC;

-- Connect protected media targets to the v2 execution handoff. Historical v1
-- plans remain immutable and cannot use these routines.
ALTER TABLE privacy_protected.object_targets
 ADD COLUMN IF NOT EXISTS upload_intent_id uuid NULL REFERENCES privacy_protected.object_upload_intents(id) ON DELETE RESTRICT;
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='privacy_protected.object_targets'::regclass AND conname='privacy_object_targets_capture_source_unique') THEN
  ALTER TABLE privacy_protected.object_targets
   ADD CONSTRAINT privacy_object_targets_capture_source_unique UNIQUE(execution_id,source_kind,source_ref,upload_intent_id);
 END IF;
END $$;

CREATE TABLE IF NOT EXISTS privacy_protected.object_capture_sets (
 execution_id uuid NOT NULL REFERENCES privacy_erasure_executions(id) ON DELETE RESTRICT,
 job_id uuid NOT NULL REFERENCES privacy_erasure_category_jobs(id) ON DELETE RESTRICT, checkpoint_id uuid NOT NULL,
 subject_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 category_key varchar(120) NOT NULL,
 source_kinds text[] NOT NULL,
 expected_target_count integer NOT NULL CHECK(expected_target_count>=0),
 created_at timestamptz NOT NULL,
 PRIMARY KEY(checkpoint_id), UNIQUE(execution_id,category_key),
 FOREIGN KEY(checkpoint_id,job_id,operation_code,action_version)
  REFERENCES privacy_erasure_job_checkpoints(id,job_id,operation_code,action_version) ON DELETE RESTRICT,
 operation_code varchar(120) NOT NULL CHECK(operation_code='OBJECT_VERSION_DELETE'),
 action_version varchar(40) NOT NULL CHECK(action_version='v1'),
 CHECK(category_key IN ('profile-photo','object-storage')),
 CHECK(cardinality(source_kinds) BETWEEN 1 AND 2 AND array_position(source_kinds,NULL) IS NULL),
 CHECK((category_key='profile-photo' AND source_kinds=ARRAY['MEMBER_PROFILE_PHOTO']::text[])
    OR (category_key='object-storage' AND source_kinds=ARRAY['EQUIPMENT_PHOTO','REPAIR_ATTACHMENT']::text[]))
);

DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='privacy_protected.object_capture_sets'::regclass AND tgname='privacy_object_capture_sets_immutable') THEN
  CREATE TRIGGER privacy_object_capture_sets_immutable BEFORE UPDATE OR DELETE ON privacy_protected.object_capture_sets
   FOR EACH ROW EXECUTE FUNCTION prevent_privacy_execution_record_delete();
 END IF;
END $$;

CREATE OR REPLACE FUNCTION public.privacy_media_subject_lock(p_subject_user_id uuid) RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT pg_advisory_xact_lock(hashtextextended('mycfc/media-subject/v1:'||p_subject_user_id::text,0));
$$;

CREATE OR REPLACE FUNCTION public.privacy_execution_capture_media_sources(
 p_execution_id uuid,p_subject_user_id uuid,p_category_key text
) RETURNS TABLE(job_id uuid,checkpoint_id uuid,plan_entry_sha256 bytea,category_key text,
 source_kind text,source_ref uuid,upload_intent_id uuid,object_key text)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE v_job uuid; v_checkpoint uuid; v_entry bytea; v_expected integer; v_kind text; v_ref uuid;
BEGIN
 PERFORM public.privacy_media_subject_lock(p_subject_user_id);
 SELECT job.id,checkpoint.id,job.entry_sha256 INTO v_job,v_checkpoint,v_entry
 FROM public.privacy_erasure_executions execution
 JOIN public.data_erasure_requests request ON request.id=execution.request_id
 JOIN public.privacy_erasure_category_jobs job ON job.execution_id=execution.id AND job.category_key=p_category_key
 JOIN public.privacy_erasure_job_checkpoints checkpoint ON checkpoint.job_id=job.id
  AND checkpoint.operation_code='OBJECT_VERSION_DELETE' AND checkpoint.action_version='v1'
 WHERE execution.id=p_execution_id AND execution.executor_version='privacy-erasure-executor/v2'
  AND execution.schema_version='privacy-erasure-plan/v2' AND request.subject_user_id=p_subject_user_id;
 IF v_checkpoint IS NULL OR p_category_key NOT IN ('profile-photo','object-storage') THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_object_capture_invalid';
 END IF;

 IF p_category_key='profile-photo' THEN
  IF EXISTS(SELECT 1 FROM public.member_profiles p WHERE p.user_id=p_subject_user_id AND p.photo_object_key IS NOT NULL AND p.photo_upload_intent_id IS NULL) THEN
   RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_legacy_media_unresolved';
  END IF;
 ELSE
  IF EXISTS(SELECT 1 FROM public.repair_requests r WHERE r.reported_by_id=p_subject_user_id AND r.image_object_key IS NOT NULL AND r.image_upload_intent_id IS NULL)
    OR EXISTS(SELECT 1 FROM public.equipment e WHERE e.image_object_key IS NOT NULL AND e.image_upload_intent_id IS NULL) THEN
   RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_legacy_media_unresolved';
  END IF;
 END IF;

 -- Any unfinished upload for this subject can still own a version that is not
 -- represented by a pointer. Start remains blocked until it attaches or its
 -- cleanup records stable absence.
 IF EXISTS(
  SELECT 1 FROM privacy_protected.object_upload_intents i
  JOIN LATERAL (SELECT e.status FROM privacy_protected.object_upload_intent_events e WHERE e.intent_id=i.id ORDER BY e.sequence DESC LIMIT 1) latest ON true
  WHERE (i.subject_user_id=p_subject_user_id OR (i.source_kind='EQUIPMENT_PHOTO' AND i.provenance_actor_user_id=p_subject_user_id))
    AND ((p_category_key='profile-photo' AND i.source_kind='MEMBER_PROFILE_PHOTO')
      OR (p_category_key='object-storage' AND i.source_kind IN ('REPAIR_ATTACHMENT','EQUIPMENT_PHOTO')))
    AND latest.status NOT IN ('ATTACHED','ABSENCE_VERIFIED')
 ) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_media_upload_in_flight'; END IF;

 SELECT count(*)::integer INTO v_expected FROM (
  SELECT p.photo_upload_intent_id FROM public.member_profiles p
   JOIN privacy_protected.object_upload_intents i ON i.id=p.photo_upload_intent_id
   WHERE p_category_key='profile-photo' AND p.user_id=p_subject_user_id AND p.photo_object_key IS NOT NULL
  UNION ALL
  SELECT r.image_upload_intent_id FROM public.repair_requests r
   JOIN privacy_protected.object_upload_intents i ON i.id=r.image_upload_intent_id
   WHERE p_category_key='object-storage' AND i.subject_user_id=p_subject_user_id AND r.image_object_key IS NOT NULL
  UNION ALL
  SELECT e.image_upload_intent_id FROM public.equipment e
   JOIN privacy_protected.object_upload_intents i ON i.id=e.image_upload_intent_id
   WHERE p_category_key='object-storage' AND i.provenance_actor_user_id=p_subject_user_id AND e.image_object_key IS NOT NULL
 ) candidates;

 INSERT INTO privacy_protected.object_capture_sets(execution_id,job_id,checkpoint_id,subject_user_id,category_key,source_kinds,
  expected_target_count,operation_code,action_version,created_at)
 VALUES(p_execution_id,v_job,v_checkpoint,p_subject_user_id,p_category_key,
  CASE WHEN p_category_key='profile-photo' THEN ARRAY['MEMBER_PROFILE_PHOTO']::text[] ELSE ARRAY['EQUIPMENT_PHOTO','REPAIR_ATTACHMENT']::text[] END,
  v_expected,'OBJECT_VERSION_DELETE','v1',clock_timestamp());

 FOR v_kind,v_ref IN
  SELECT i.source_kind,i.source_ref FROM privacy_protected.object_upload_intents i
  WHERE (i.subject_user_id=p_subject_user_id OR (i.source_kind='EQUIPMENT_PHOTO' AND i.provenance_actor_user_id=p_subject_user_id))
   AND ((p_category_key='profile-photo' AND i.source_kind='MEMBER_PROFILE_PHOTO')
     OR (p_category_key='object-storage' AND i.source_kind IN ('REPAIR_ATTACHMENT','EQUIPMENT_PHOTO')))
  ORDER BY i.source_kind,i.source_ref
 LOOP PERFORM public.privacy_upload_source_lock(v_kind,v_ref); END LOOP;

 RETURN QUERY
  SELECT v_job,v_checkpoint,v_entry,p_category_key::text,'MEMBER_PROFILE_PHOTO'::text,p.user_id,p.photo_upload_intent_id,p.photo_object_key::text
  FROM public.member_profiles p JOIN privacy_protected.object_upload_intents i ON i.id=p.photo_upload_intent_id
  WHERE p_category_key='profile-photo' AND p.user_id=p_subject_user_id AND p.photo_object_key IS NOT NULL
    AND public.privacy_upload_pointer_matches(i.id,i.source_kind,i.source_ref,i.locator_commitment,i.content_type,i.size_bytes)
    AND (SELECT status FROM privacy_protected.object_upload_intent_events x WHERE x.intent_id=i.id ORDER BY sequence DESC LIMIT 1)='ATTACHED'
  UNION ALL
  SELECT v_job,v_checkpoint,v_entry,p_category_key::text,'REPAIR_ATTACHMENT'::text,r.id,r.image_upload_intent_id,r.image_object_key::text
  FROM public.repair_requests r JOIN privacy_protected.object_upload_intents i ON i.id=r.image_upload_intent_id
  WHERE p_category_key='object-storage' AND i.subject_user_id=p_subject_user_id AND r.image_object_key IS NOT NULL
    AND public.privacy_upload_pointer_matches(i.id,i.source_kind,i.source_ref,i.locator_commitment,i.content_type,i.size_bytes)
    AND (SELECT status FROM privacy_protected.object_upload_intent_events x WHERE x.intent_id=i.id ORDER BY sequence DESC LIMIT 1)='ATTACHED'
  UNION ALL
  SELECT v_job,v_checkpoint,v_entry,p_category_key::text,'EQUIPMENT_PHOTO'::text,e.id,e.image_upload_intent_id,e.image_object_key::text
  FROM public.equipment e JOIN privacy_protected.object_upload_intents i ON i.id=e.image_upload_intent_id
  WHERE p_category_key='object-storage' AND i.provenance_actor_user_id=p_subject_user_id AND e.image_object_key IS NOT NULL
    AND public.privacy_upload_pointer_matches(i.id,i.source_kind,i.source_ref,i.locator_commitment,i.content_type,i.size_bytes)
    AND (SELECT status FROM privacy_protected.object_upload_intent_events x WHERE x.intent_id=i.id ORDER BY sequence DESC LIMIT 1)='ATTACHED'
  ORDER BY 5,6;
END; $$;

CREATE OR REPLACE FUNCTION public.privacy_execution_materialize_object_target(
 p_target_id uuid,p_execution_id uuid,p_job_id uuid,p_checkpoint_id uuid,p_plan_entry_sha256 bytea,p_category_key text,
 p_source_kind text,p_source_ref uuid,p_upload_intent_id uuid,p_object_key text,
 p_envelope_version text,p_algorithm text,p_encryption_key_id text,p_encapsulation bytea,p_nonce bytea,p_ciphertext bytea,
 p_digest_key_id text,p_locator_digest bytea
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE c privacy_protected.object_capture_sets%ROWTYPE; i privacy_protected.object_upload_intents%ROWTYPE;
BEGIN
 SELECT * INTO c FROM privacy_protected.object_capture_sets WHERE checkpoint_id=p_checkpoint_id AND execution_id=p_execution_id FOR SHARE;
 SELECT * INTO i FROM privacy_protected.object_upload_intents WHERE id=p_upload_intent_id FOR SHARE;
 IF c.checkpoint_id IS NULL OR c.job_id<>p_job_id OR c.category_key<>p_category_key OR c.expected_target_count<=
    (SELECT count(*) FROM privacy_protected.object_targets t WHERE t.checkpoint_id=p_checkpoint_id)
   OR i.id IS NULL OR i.source_kind<>p_source_kind OR i.source_ref<>p_source_ref
   OR NOT (p_source_kind=ANY(c.source_kinds)) OR digest(convert_to(p_object_key,'UTF8'),'sha256') IS DISTINCT FROM i.locator_commitment
   OR (p_source_kind='MEMBER_PROFILE_PHOTO' AND (i.subject_user_id IS DISTINCT FROM c.subject_user_id OR i.source_ref<>c.subject_user_id))
   OR (p_source_kind='REPAIR_ATTACHMENT' AND i.subject_user_id IS DISTINCT FROM c.subject_user_id)
   OR (p_source_kind='EQUIPMENT_PHOTO' AND i.provenance_actor_user_id IS DISTINCT FROM c.subject_user_id)
   OR NOT public.privacy_upload_pointer_matches(i.id,i.source_kind,i.source_ref,i.locator_commitment,i.content_type,i.size_bytes)
   OR (SELECT status FROM privacy_protected.object_upload_intent_events e WHERE e.intent_id=i.id ORDER BY sequence DESC LIMIT 1)<>'ATTACHED' THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_object_target_invalid';
 END IF;
 INSERT INTO privacy_protected.object_targets(id,execution_id,job_id,checkpoint_id,plan_entry_sha256,category_key,service_code,target_kind,
  source_kind,source_ref,operation_code,action_version,provider_contract_version,envelope_version,algorithm,encryption_key_id,
  encapsulation,nonce,ciphertext,created_at,upload_intent_id)
 VALUES(p_target_id,p_execution_id,p_job_id,p_checkpoint_id,p_plan_entry_sha256,p_category_key,'private-media','OBJECT_KEY',p_source_kind,
  p_source_ref,'OBJECT_VERSION_DELETE','v1','s3-versioned/v1',p_envelope_version,p_algorithm,p_encryption_key_id,
  p_encapsulation,p_nonce,p_ciphertext,clock_timestamp(),p_upload_intent_id);
 INSERT INTO privacy_protected.object_target_digests(target_id,execution_id,service_code,target_kind,digest_key_id,locator_digest,created_at)
 VALUES(p_target_id,p_execution_id,'private-media','OBJECT_KEY',p_digest_key_id,p_locator_digest,clock_timestamp());
 RETURN p_target_id;
END; $$;

CREATE OR REPLACE FUNCTION public.privacy_execution_complete_object_capture(p_execution_id uuid,p_category_key text) RETURNS integer
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE expected integer; actual integer; p_checkpoint_id uuid;
BEGIN
 SELECT checkpoint_id,expected_target_count INTO p_checkpoint_id,expected FROM privacy_protected.object_capture_sets
  WHERE execution_id=p_execution_id AND category_key=p_category_key FOR SHARE;
 SELECT count(*)::integer INTO actual FROM privacy_protected.object_targets WHERE execution_id=p_execution_id AND checkpoint_id=p_checkpoint_id;
 IF expected IS NULL OR actual<>expected THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_object_capture_incomplete'; END IF;
 RETURN actual;
END; $$;

-- Block new in-scope uploads after a v2 capture set commits. The subject lock
-- closes the enumeration/creation gap, including category-only requests.
CREATE OR REPLACE FUNCTION public.privacy_upload_begin(
 p_intent_id uuid,p_subject_user_id uuid,p_actor_user_id uuid,p_source_kind text,p_source_ref uuid,
 p_service_code text,p_content_type text,p_size_bytes bigint,p_token bytea
) RETURNS jsonb LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE v_now timestamptz:=clock_timestamp(); v_token_digest bytea; v_privacy_subject uuid:=COALESCE(p_subject_user_id,p_actor_user_id);
BEGIN
 IF p_intent_id IS NULL OR p_actor_user_id IS NULL OR p_source_ref IS NULL OR octet_length(p_token)<>32 THEN RAISE EXCEPTION 'invalid upload reservation'; END IF;
 IF p_source_kind NOT IN ('MEMBER_PROFILE_PHOTO','REPAIR_ATTACHMENT','EQUIPMENT_PHOTO') OR p_service_code<>'private-media'
    OR p_content_type NOT IN ('image/jpeg','image/png','image/webp') OR p_size_bytes NOT BETWEEN 1 AND 10485760 THEN RAISE EXCEPTION 'invalid upload reservation'; END IF;
 IF p_source_kind IN ('MEMBER_PROFILE_PHOTO','REPAIR_ATTACHMENT') AND p_subject_user_id IS DISTINCT FROM p_actor_user_id THEN RAISE EXCEPTION 'upload actor is not authorized'; END IF;
 IF p_source_kind='EQUIPMENT_PHOTO' AND (p_subject_user_id IS NOT NULL OR NOT EXISTS(
   SELECT 1 FROM public.users u JOIN public.user_platform_roles ur ON ur.user_id=u.id JOIN public.platform_roles r ON r.id=ur.role_id
   WHERE u.id=p_actor_user_id AND u.is_active AND u.erased_at IS NULL AND NOT u.is_dependent AND r.code='ADMIN')) THEN RAISE EXCEPTION 'upload actor is not authorized'; END IF;
 IF p_source_kind='MEMBER_PROFILE_PHOTO' AND p_source_ref<>p_subject_user_id THEN RAISE EXCEPTION 'invalid upload source'; END IF;
 PERFORM public.privacy_media_subject_lock(v_privacy_subject);
 IF EXISTS(SELECT 1 FROM privacy_protected.object_capture_sets c
  JOIN public.privacy_erasure_executions x ON x.id=c.execution_id
  JOIN public.data_erasure_requests r ON r.id=x.request_id
  WHERE c.subject_user_id=v_privacy_subject AND p_source_kind=ANY(c.source_kinds)
    AND r.status IN ('PROCESSING','RETRYABLE_FAILED','TERMINAL_FAILED')) THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_media_execution_in_progress';
 END IF;
 PERFORM public.privacy_upload_source_lock(p_source_kind,p_source_ref);
 v_token_digest:=digest(p_token,'sha256');
 INSERT INTO privacy_protected.object_upload_intent_reservations(id,subject_user_id,provenance_actor_user_id,source_kind,source_ref,service_code,content_type,size_bytes,token_digest,created_at,cleanup_after)
 VALUES(p_intent_id,p_subject_user_id,p_actor_user_id,p_source_kind,p_source_ref,p_service_code,p_content_type,p_size_bytes,v_token_digest,v_now,v_now+interval '24 hours');
 INSERT INTO privacy_protected.object_upload_intent_holds(intent_id,hold_epoch,token_digest,held_until,updated_at)
 VALUES(p_intent_id,1,v_token_digest,v_now+interval '15 minutes',v_now);
 RETURN jsonb_build_object('created_at',v_now,'cleanup_after',v_now+interval '24 hours','hold_epoch',1);
END; $$;

CREATE OR REPLACE FUNCTION public.privacy_worker_list_object_targets(
 p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_epoch bigint,p_worker_ref uuid
) RETURNS TABLE(target_id uuid,execution_id uuid,job_id uuid,checkpoint_id uuid,plan_entry_sha256 bytea,category_key text,
 service_code text,target_kind text,source_kind text,source_ref uuid,operation_code text,action_version text,
 provider_contract_version text,envelope_version text,algorithm text,encryption_key_id text,encapsulation bytea,nonce bytea,ciphertext bytea)
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT t.id,t.execution_id,t.job_id,t.checkpoint_id,t.plan_entry_sha256,t.category_key::text,t.service_code::text,t.target_kind::text,
  t.source_kind::text,t.source_ref,t.operation_code::text,t.action_version::text,t.provider_contract_version::text,t.envelope_version::text,
  t.algorithm::text,t.encryption_key_id::text,t.encapsulation,t.nonce,t.ciphertext
 FROM privacy_protected.object_targets t
 JOIN public.privacy_erasure_category_jobs j ON j.id=t.job_id
 JOIN public.privacy_erasure_job_leases l ON l.id=p_lease_id AND l.job_id=j.id AND l.epoch=p_epoch
 JOIN public.privacy_erasure_job_attempts a ON a.id=p_attempt_id AND a.job_id=j.id AND a.lease_id=l.id AND a.lease_epoch=p_epoch
 LEFT JOIN privacy_protected.object_evidence e ON e.target_id=t.id
 WHERE j.id=p_job_id AND j.status='LEASED' AND l.worker_ref=p_worker_ref AND l.released_at IS NULL
  AND l.expires_at>clock_timestamp() AND a.finished_at IS NULL AND e.target_id IS NULL
 ORDER BY t.id;
$$;

CREATE OR REPLACE FUNCTION public.privacy_worker_record_object_evidence(
 p_target_id uuid,p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_epoch bigint,p_worker_ref uuid,
 p_deleted_versions integer,p_deleted_markers integer,p_list_calls integer,p_stable_checks integer,
 p_transcript_key_id text,p_transcript_digest bytea
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
 PERFORM 1 FROM public.privacy_erasure_category_jobs j
 JOIN public.privacy_erasure_job_leases l ON l.id=p_lease_id AND l.job_id=j.id AND l.epoch=p_epoch
 JOIN public.privacy_erasure_job_attempts a ON a.id=p_attempt_id AND a.job_id=j.id AND a.lease_id=l.id AND a.lease_epoch=p_epoch
 JOIN privacy_protected.object_targets t ON t.id=p_target_id AND t.job_id=j.id
 WHERE j.id=p_job_id AND j.status='LEASED' AND l.worker_ref=p_worker_ref AND l.released_at IS NULL
  AND l.expires_at>clock_timestamp() AND a.finished_at IS NULL FOR UPDATE OF j,l,a;
 IF NOT FOUND OR p_deleted_versions<0 OR p_deleted_markers<0 OR p_list_calls<2 OR p_stable_checks<2
  OR p_transcript_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$' OR octet_length(p_transcript_digest)<>32 THEN RETURN NULL; END IF;
 INSERT INTO privacy_protected.object_evidence(target_id,job_id,attempt_id,evidence_version,outcome_code,deleted_version_count,
  deleted_marker_count,list_call_count,stable_empty_check_count,transcript_key_id,transcript_digest,occurred_at)
 VALUES(p_target_id,p_job_id,p_attempt_id,'s3-absence/v1','ABSENCE_VERIFIED',p_deleted_versions,p_deleted_markers,p_list_calls,
  p_stable_checks,p_transcript_key_id,p_transcript_digest,clock_timestamp()) ON CONFLICT(target_id) DO NOTHING;
 RETURN p_target_id;
END; $$;

CREATE OR REPLACE FUNCTION public.privacy_worker_complete_object_checkpoint(
 p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_epoch bigint,p_worker_ref uuid
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE checkpoint_ref uuid; expected integer; evidenced integer;
BEGIN
 SELECT checkpoint.id,c.expected_target_count INTO checkpoint_ref,expected
 FROM public.privacy_erasure_category_jobs j
 JOIN public.privacy_erasure_job_leases l ON l.id=p_lease_id AND l.job_id=j.id AND l.epoch=p_epoch
 JOIN public.privacy_erasure_job_attempts a ON a.id=p_attempt_id AND a.job_id=j.id AND a.lease_id=l.id AND a.lease_epoch=p_epoch
 JOIN public.privacy_erasure_job_checkpoints checkpoint ON checkpoint.job_id=j.id AND checkpoint.operation_code='OBJECT_VERSION_DELETE' AND checkpoint.action_version='v1'
 JOIN privacy_protected.object_capture_sets c ON c.checkpoint_id=checkpoint.id
 WHERE j.id=p_job_id AND j.status='LEASED' AND l.worker_ref=p_worker_ref AND l.released_at IS NULL
  AND l.expires_at>clock_timestamp() AND a.finished_at IS NULL FOR UPDATE OF j,l,a,checkpoint;
 IF checkpoint_ref IS NULL THEN RETURN NULL; END IF;
 IF (SELECT status FROM public.privacy_erasure_job_checkpoints WHERE id=checkpoint_ref)='SUCCEEDED' THEN RETURN checkpoint_ref; END IF;
 SELECT count(*)::integer INTO evidenced FROM privacy_protected.object_targets t
 JOIN privacy_protected.object_evidence e ON e.target_id=t.id WHERE t.checkpoint_id=checkpoint_ref;
 IF evidenced<>expected OR EXISTS(SELECT 1 FROM public.privacy_erasure_job_checkpoints p
   WHERE p.job_id=p_job_id AND p.operation_position<(SELECT operation_position FROM public.privacy_erasure_job_checkpoints WHERE id=checkpoint_ref)
    AND p.status<>'SUCCEEDED')
  OR EXISTS(SELECT 1 FROM public.privacy_erasure_category_jobs prior
   WHERE prior.execution_id=(SELECT execution_id FROM public.privacy_erasure_category_jobs WHERE id=p_job_id)
    AND prior.plan_entry_position<(SELECT plan_entry_position FROM public.privacy_erasure_category_jobs WHERE id=p_job_id) AND prior.status<>'SUCCEEDED') THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_object_evidence_incomplete';
 END IF;
 UPDATE public.privacy_erasure_job_checkpoints SET status='SUCCEEDED',completed_at=clock_timestamp(),completed_by_attempt_id=p_attempt_id,
  affected_rows=evidenced,result_sha256=digest(convert_to('OBJECT_VERSION_DELETE:'||evidenced::text,'UTF8'),'sha256')
 WHERE id=checkpoint_ref AND status='PENDING';
 RETURN checkpoint_ref;
END; $$;

REVOKE ALL ON TABLE privacy_protected.object_capture_sets FROM PUBLIC;
REVOKE ALL ON FUNCTION public.privacy_media_subject_lock(uuid),public.privacy_execution_capture_media_sources(uuid,uuid,text),
 public.privacy_execution_materialize_object_target(uuid,uuid,uuid,uuid,bytea,text,text,uuid,uuid,text,text,text,text,bytea,bytea,bytea,text,bytea),
 public.privacy_execution_complete_object_capture(uuid,text),public.privacy_worker_list_object_targets(uuid,uuid,uuid,bigint,uuid),
 public.privacy_worker_record_object_evidence(uuid,uuid,uuid,uuid,bigint,uuid,integer,integer,integer,integer,text,bytea),
 public.privacy_worker_complete_object_checkpoint(uuid,uuid,uuid,bigint,uuid) FROM PUBLIC;
-- #247 restore-independent tombstone receipt foundation.
CREATE TABLE privacy_protected.restore_tombstone_receipts (
 execution_id uuid PRIMARY KEY REFERENCES public.privacy_erasure_executions(id) ON DELETE RESTRICT,
 request_id uuid NOT NULL REFERENCES public.data_erasure_requests(id) ON DELETE RESTRICT,
 ledger_version varchar(40) NOT NULL CHECK(ledger_version='restore-tombstone/v1'),
 encryption_key_id varchar(80) NOT NULL CHECK(encryption_key_id=btrim(encryption_key_id) AND encryption_key_id~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'),
 locator_key_id varchar(80) NOT NULL CHECK(locator_key_id=btrim(locator_key_id) AND locator_key_id~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'),
 locator_digest bytea NOT NULL CHECK(octet_length(locator_digest)=32),
 object_version_id varchar(1024) NOT NULL CHECK(object_version_id=btrim(object_version_id) AND char_length(object_version_id) BETWEEN 1 AND 1024),
 ciphertext_sha256 bytea NOT NULL CHECK(octet_length(ciphertext_sha256)=32),
 size_bytes bigint NOT NULL CHECK(size_bytes BETWEEN 1 AND 1048576),
 written_at timestamptz NOT NULL, verified_at timestamptz NOT NULL,
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(), CHECK(verified_at>=written_at),
 UNIQUE(request_id), UNIQUE(locator_key_id,locator_digest)
);
CREATE TRIGGER privacy_restore_tombstone_receipts_immutable BEFORE UPDATE OR DELETE
 ON privacy_protected.restore_tombstone_receipts FOR EACH ROW EXECUTE FUNCTION public.prevent_privacy_execution_record_delete();

CREATE TABLE privacy_protected.restore_tombstone_closure_intents (
 execution_id uuid PRIMARY KEY REFERENCES public.privacy_erasure_executions(id) ON DELETE RESTRICT,
 closed_at timestamptz NOT NULL,evidence_expires_at timestamptz NOT NULL,prepared_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK(evidence_expires_at=closed_at+interval '24 months')
);
CREATE TRIGGER privacy_restore_tombstone_closure_intents_immutable BEFORE UPDATE OR DELETE
 ON privacy_protected.restore_tombstone_closure_intents FOR EACH ROW EXECUTE FUNCTION public.prevent_privacy_execution_record_delete();
CREATE TABLE privacy_protected.restore_tombstone_closure_receipts (
 execution_id uuid PRIMARY KEY REFERENCES privacy_protected.restore_tombstone_closure_intents(execution_id) ON DELETE RESTRICT,
 ledger_version varchar(40) NOT NULL CHECK(ledger_version='restore-tombstone-closure/v1'),
 encryption_key_id varchar(80) NOT NULL CHECK(encryption_key_id=btrim(encryption_key_id) AND encryption_key_id~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'),
 locator_key_id varchar(80) NOT NULL CHECK(locator_key_id=btrim(locator_key_id) AND locator_key_id~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'),
 locator_digest bytea NOT NULL CHECK(octet_length(locator_digest)=32),object_version_id varchar(1024) NOT NULL CHECK(object_version_id=btrim(object_version_id) AND char_length(object_version_id) BETWEEN 1 AND 1024),
 ciphertext_sha256 bytea NOT NULL CHECK(octet_length(ciphertext_sha256)=32),size_bytes bigint NOT NULL CHECK(size_bytes BETWEEN 1 AND 1048576),
 written_at timestamptz NOT NULL,verified_at timestamptz NOT NULL,recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),CHECK(verified_at>=written_at),UNIQUE(locator_key_id,locator_digest)
);
CREATE TRIGGER privacy_restore_tombstone_closure_receipts_immutable BEFORE UPDATE OR DELETE
 ON privacy_protected.restore_tombstone_closure_receipts FOR EACH ROW EXECUTE FUNCTION public.prevent_privacy_execution_record_delete();

CREATE FUNCTION public.privacy_tombstone_prepare(p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_lease_epoch bigint,p_worker_ref uuid)
RETURNS TABLE(execution_id uuid,request_id uuid,request_ref uuid,subject_user_id uuid,plan_sha256 bytea,workset_sha256 bytea,execution_started_at timestamptz)
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT execution.id,request.id,request.public_ref,COALESCE(request.subject_user_id,request.requester_user_id),execution.plan_sha256,
 digest(convert_to(string_agg(job.plan_entry_position::text||':'||encode(job.entry_sha256,'hex')||':'||checkpoint.operation_position::text||':'||checkpoint.operation_code||':'||checkpoint.action_version,'|' ORDER BY job.plan_entry_position,checkpoint.operation_position),'UTF8'),'sha256'),execution.accepted_at
 FROM privacy_erasure_job_leases lease
 JOIN privacy_erasure_category_jobs selected_job ON selected_job.id=lease.job_id AND selected_job.lease_epoch=lease.epoch
 JOIN privacy_erasure_job_attempts attempt ON attempt.lease_id=lease.id AND attempt.job_id=selected_job.id
 JOIN privacy_erasure_executions execution ON execution.id=selected_job.execution_id
 JOIN data_erasure_requests request ON request.id=execution.request_id
 JOIN privacy_erasure_category_jobs job ON job.execution_id=execution.id
 JOIN privacy_erasure_job_checkpoints checkpoint ON checkpoint.job_id=job.id
 WHERE selected_job.id=p_job_id AND lease.id=p_lease_id AND attempt.id=p_attempt_id AND lease.epoch=p_lease_epoch AND lease.worker_ref=p_worker_ref
 AND selected_job.status='LEASED' AND lease.released_at IS NULL AND attempt.finished_at IS NULL AND lease.expires_at>clock_timestamp()
 AND selected_job.category_key='backup-tombstones' AND EXISTS(SELECT 1 FROM privacy_erasure_job_checkpoints selected_checkpoint
  WHERE selected_checkpoint.job_id=selected_job.id AND selected_checkpoint.operation_code='BACKUP_TOMBSTONE_REPLAY' AND selected_checkpoint.action_version='v1' AND selected_checkpoint.status='PENDING')
 AND COALESCE(request.subject_user_id,request.requester_user_id) IS NOT NULL
 GROUP BY execution.id,request.id,request.public_ref,request.subject_user_id,request.requester_user_id;
$$;

CREATE FUNCTION public.privacy_tombstone_confirm(
 p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_lease_epoch bigint,p_worker_ref uuid,p_ledger_version text,p_encryption_key_id text,
 p_locator_key_id text,p_locator_digest bytea,p_object_version_id text,p_ciphertext_sha256 bytea,p_size_bytes bigint,p_written_at timestamptz,p_verified_at timestamptz
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE checkpoint_ref uuid; execution_ref uuid; request_ref uuid; existing privacy_protected.restore_tombstone_receipts%ROWTYPE;
BEGIN
 SELECT checkpoint.id,job.execution_id,execution.request_id INTO checkpoint_ref,execution_ref,request_ref
 FROM privacy_erasure_category_jobs job JOIN privacy_erasure_job_leases lease ON lease.job_id=job.id AND lease.epoch=job.lease_epoch
 JOIN privacy_erasure_job_attempts attempt ON attempt.lease_id=lease.id AND attempt.job_id=job.id
 JOIN privacy_erasure_executions execution ON execution.id=job.execution_id JOIN privacy_erasure_job_checkpoints checkpoint ON checkpoint.job_id=job.id
 WHERE job.id=p_job_id AND lease.id=p_lease_id AND attempt.id=p_attempt_id AND lease.epoch=p_lease_epoch AND lease.worker_ref=p_worker_ref AND job.status='LEASED'
 AND lease.released_at IS NULL AND attempt.finished_at IS NULL AND lease.expires_at>clock_timestamp() AND job.category_key='backup-tombstones'
 AND checkpoint.operation_code='BACKUP_TOMBSTONE_REPLAY' AND checkpoint.action_version='v1' FOR UPDATE OF job,lease,attempt,checkpoint;
 IF checkpoint_ref IS NULL OR p_ledger_version<>'restore-tombstone/v1'
  OR p_encryption_key_id IS NULL OR p_encryption_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'
  OR p_locator_key_id IS NULL OR p_locator_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'
  OR octet_length(p_locator_digest)<>32 OR octet_length(p_ciphertext_sha256)<>32 OR p_object_version_id IS NULL
  OR p_object_version_id<>btrim(p_object_version_id) OR char_length(p_object_version_id) NOT BETWEEN 1 AND 1024
  OR p_size_bytes NOT BETWEEN 1 AND 1048576 OR p_written_at IS NULL OR p_verified_at<p_written_at THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_tombstone_receipt_rejected'; END IF;
 SELECT * INTO existing FROM privacy_protected.restore_tombstone_receipts WHERE execution_id=execution_ref;
 IF existing.execution_id IS NULL THEN
  INSERT INTO privacy_protected.restore_tombstone_receipts(execution_id,request_id,ledger_version,encryption_key_id,locator_key_id,locator_digest,object_version_id,ciphertext_sha256,size_bytes,written_at,verified_at)
  VALUES(execution_ref,request_ref,p_ledger_version,p_encryption_key_id,p_locator_key_id,p_locator_digest,p_object_version_id,p_ciphertext_sha256,p_size_bytes,p_written_at,p_verified_at);
 ELSIF ROW(existing.request_id,existing.ledger_version,existing.encryption_key_id,existing.locator_key_id,existing.locator_digest,existing.object_version_id,existing.ciphertext_sha256,existing.size_bytes,existing.written_at,existing.verified_at)
  IS DISTINCT FROM ROW(request_ref,p_ledger_version,p_encryption_key_id,p_locator_key_id,p_locator_digest,p_object_version_id,p_ciphertext_sha256,p_size_bytes,p_written_at,p_verified_at) THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_tombstone_receipt_conflict'; END IF;
 UPDATE privacy_erasure_job_checkpoints SET status='SUCCEEDED',completed_by_attempt_id=p_attempt_id,completed_at=clock_timestamp(),affected_rows=1,
  result_sha256=digest(p_ciphertext_sha256||convert_to(p_object_version_id,'UTF8'),'sha256') WHERE id=checkpoint_ref AND status='PENDING';
 RETURN checkpoint_ref;
END; $$;

CREATE FUNCTION public.privacy_tombstone_prepare_closure(p_execution_id uuid,p_worker_ref uuid)
RETURNS TABLE(execution_id uuid,request_id uuid,request_ref uuid,subject_user_id uuid,plan_sha256 bytea,workset_sha256 bytea,execution_started_at timestamptz,closed_at timestamptz,evidence_expires_at timestamptz)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
#variable_conflict use_column
DECLARE v_closed_at timestamptz;
BEGIN
 IF p_execution_id IS NULL OR p_worker_ref IS NULL OR NOT EXISTS(SELECT 1 FROM privacy_erasure_executions execution JOIN data_erasure_requests request ON request.id=execution.request_id
  WHERE execution.id=p_execution_id AND execution.status='SUCCEEDED' AND request.status IN ('PROCESSING','RETRYABLE_FAILED')
  AND NOT EXISTS(SELECT 1 FROM privacy_erasure_category_jobs job WHERE job.execution_id=execution.id AND job.status<>'SUCCEEDED')
  AND EXISTS(SELECT 1 FROM privacy_protected.restore_tombstone_receipts receipt WHERE receipt.execution_id=execution.id))
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_tombstone_closure_unavailable'; END IF;
 v_closed_at:=clock_timestamp();
 INSERT INTO privacy_protected.restore_tombstone_closure_intents(execution_id,closed_at,evidence_expires_at)
 VALUES(p_execution_id,v_closed_at,v_closed_at+interval '24 months') ON CONFLICT(execution_id) DO NOTHING;
 RETURN QUERY SELECT execution.id,request.id,request.public_ref,COALESCE(request.subject_user_id,request.requester_user_id),execution.plan_sha256,
  digest(convert_to(string_agg(job.plan_entry_position::text||':'||encode(job.entry_sha256,'hex')||':'||checkpoint.operation_position::text||':'||checkpoint.operation_code||':'||checkpoint.action_version,'|' ORDER BY job.plan_entry_position,checkpoint.operation_position),'UTF8'),'sha256'),
  execution.accepted_at,intent.closed_at,intent.evidence_expires_at
 FROM privacy_erasure_executions execution JOIN data_erasure_requests request ON request.id=execution.request_id
 JOIN privacy_erasure_category_jobs job ON job.execution_id=execution.id JOIN privacy_erasure_job_checkpoints checkpoint ON checkpoint.job_id=job.id
 JOIN privacy_protected.restore_tombstone_closure_intents intent ON intent.execution_id=execution.id
 WHERE execution.id=p_execution_id AND COALESCE(request.subject_user_id,request.requester_user_id) IS NOT NULL
 GROUP BY execution.id,request.id,request.public_ref,request.subject_user_id,request.requester_user_id,intent.closed_at,intent.evidence_expires_at;
END; $$;
CREATE FUNCTION public.privacy_tombstone_confirm_closure(p_execution_id uuid,p_worker_ref uuid,p_ledger_version text,p_encryption_key_id text,p_locator_key_id text,p_locator_digest bytea,p_object_version_id text,p_ciphertext_sha256 bytea,p_size_bytes bigint,p_written_at timestamptz,p_verified_at timestamptz)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE intent privacy_protected.restore_tombstone_closure_intents%ROWTYPE; existing privacy_protected.restore_tombstone_closure_receipts%ROWTYPE;
BEGIN
 SELECT * INTO intent FROM privacy_protected.restore_tombstone_closure_intents WHERE execution_id=p_execution_id;
 IF intent.execution_id IS NULL OR p_worker_ref IS NULL OR p_ledger_version<>'restore-tombstone-closure/v1'
 OR p_encryption_key_id IS NULL OR p_encryption_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$' OR p_locator_key_id IS NULL OR p_locator_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'
 OR octet_length(p_locator_digest)<>32 OR octet_length(p_ciphertext_sha256)<>32 OR p_object_version_id IS NULL OR p_object_version_id<>btrim(p_object_version_id)
 OR char_length(p_object_version_id) NOT BETWEEN 1 AND 1024 OR p_size_bytes NOT BETWEEN 1 AND 1048576 OR p_written_at IS NULL OR p_verified_at<p_written_at OR p_verified_at>intent.evidence_expires_at
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_tombstone_closure_receipt_rejected'; END IF;
 SELECT * INTO existing FROM privacy_protected.restore_tombstone_closure_receipts WHERE execution_id=p_execution_id;
 IF existing.execution_id IS NULL THEN
  INSERT INTO privacy_protected.restore_tombstone_closure_receipts(execution_id,ledger_version,encryption_key_id,locator_key_id,locator_digest,object_version_id,ciphertext_sha256,size_bytes,written_at,verified_at)
  VALUES(p_execution_id,p_ledger_version,p_encryption_key_id,p_locator_key_id,p_locator_digest,p_object_version_id,p_ciphertext_sha256,p_size_bytes,p_written_at,p_verified_at);
 ELSIF ROW(existing.ledger_version,existing.encryption_key_id,existing.locator_key_id,existing.locator_digest,existing.object_version_id,existing.ciphertext_sha256,existing.size_bytes,existing.written_at,existing.verified_at)
 IS DISTINCT FROM ROW(p_ledger_version,p_encryption_key_id,p_locator_key_id,p_locator_digest,p_object_version_id,p_ciphertext_sha256,p_size_bytes,p_written_at,p_verified_at)
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_tombstone_closure_receipt_conflict'; END IF;
 RETURN p_execution_id;
END; $$;

REVOKE ALL ON FUNCTION public.privacy_worker_execute_checkpoint_without_tombstone_guard(uuid,uuid,uuid,bigint,uuid,text,text) FROM PUBLIC;
CREATE FUNCTION public.privacy_worker_execute_checkpoint(p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_lease_epoch bigint,p_worker_ref uuid,p_operation_code text,p_action_version text)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE execution_ref uuid;
BEGIN
	SELECT job.execution_id INTO execution_ref FROM privacy_erasure_category_jobs job
	JOIN privacy_erasure_job_leases lease ON lease.id=p_lease_id AND lease.job_id=job.id AND lease.epoch=p_lease_epoch
	JOIN privacy_erasure_job_attempts attempt ON attempt.id=p_attempt_id AND attempt.job_id=job.id AND attempt.lease_id=lease.id AND attempt.lease_epoch=p_lease_epoch
	WHERE job.id=p_job_id AND job.status='LEASED' AND lease.worker_ref=p_worker_ref AND lease.released_at IS NULL
	 AND lease.expires_at>clock_timestamp() AND attempt.finished_at IS NULL FOR UPDATE OF job,lease,attempt;
	IF NOT FOUND THEN RETURN public.privacy_worker_execute_checkpoint_without_tombstone_guard(p_job_id,p_lease_id,p_attempt_id,p_lease_epoch,p_worker_ref,p_operation_code,p_action_version); END IF;
 IF p_operation_code<>'BACKUP_TOMBSTONE_REPLAY' AND NOT EXISTS(SELECT 1 FROM privacy_protected.restore_tombstone_receipts receipt WHERE receipt.execution_id=execution_ref)
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_tombstone_required'; END IF;
 RETURN public.privacy_worker_execute_checkpoint_without_tombstone_guard(p_job_id,p_lease_id,p_attempt_id,p_lease_epoch,p_worker_ref,p_operation_code,p_action_version);
END; $$;
REVOKE ALL ON FUNCTION public.privacy_tombstone_prepare(uuid,uuid,uuid,bigint,uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.privacy_tombstone_confirm(uuid,uuid,uuid,bigint,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.privacy_tombstone_prepare_closure(uuid,uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.privacy_tombstone_confirm_closure(uuid,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.privacy_worker_execute_checkpoint(uuid,uuid,uuid,bigint,uuid,text,text) FROM PUBLIC;

-- #247 bounded conservative retention maintenance.
CREATE TABLE privacy_retention_runs (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),worker_ref uuid NOT NULL,started_at timestamptz NOT NULL,finished_at timestamptz NOT NULL,
 batch_limit integer NOT NULL CHECK(batch_limit BETWEEN 1 AND 10000),sessions_deleted integer NOT NULL CHECK(sessions_deleted>=0),tokens_deleted integer NOT NULL CHECK(tokens_deleted>=0),
 outbox_stopped integer NOT NULL CHECK(outbox_stopped>=0),outbox_payloads_deleted integer NOT NULL CHECK(outbox_payloads_deleted>=0),outbox_evidence_deleted integer NOT NULL CHECK(outbox_evidence_deleted>=0),
 consent_network_scrubbed integer NOT NULL CHECK(consent_network_scrubbed>=0),event_responses_deleted integer NOT NULL CHECK(event_responses_deleted>=0),
 announcement_deliveries_deleted integer NOT NULL CHECK(announcement_deliveries_deleted>=0),suggestions_deleted integer NOT NULL CHECK(suggestions_deleted>=0),
 privacy_working_scrubbed integer NOT NULL CHECK(privacy_working_scrubbed>=0),auth_limits_deleted integer NOT NULL CHECK(auth_limits_deleted>=0),CHECK(finished_at>=started_at)
);
CREATE TABLE privacy_outbox_delivery_evidence (
 outbox_id uuid PRIMARY KEY,message_type varchar(30) NOT NULL,final_status varchar(20) NOT NULL CHECK(final_status IN ('SENT','FAILED','CANCELLED')),
 attempts integer NOT NULL CHECK(attempts>=0),created_at timestamptz NOT NULL,terminal_at timestamptz NOT NULL,expires_at timestamptz NOT NULL,CHECK(expires_at=created_at+interval '90 days')
);
CREATE INDEX privacy_outbox_delivery_evidence_expiry_idx ON privacy_outbox_delivery_evidence(expires_at,outbox_id);
CREATE INDEX email_verification_tokens_retention_idx ON email_verification_tokens((GREATEST(expires_at,COALESCE(consumed_at,expires_at))),id);
CREATE INDEX password_reset_tokens_retention_idx ON password_reset_tokens((GREATEST(expires_at,COALESCE(consumed_at,expires_at))),id);
CREATE INDEX event_responses_retention_idx ON event_responses(event_id,user_id);
CREATE INDEX announcement_deliveries_retention_idx ON announcement_deliveries(delivered_at,announcement_id,user_id);
CREATE INDEX suggestions_retention_idx ON suggestions(responded_at,id) WHERE status IN ('DECLINED','COMPLETED');
CREATE FUNCTION privacy_retention_run(p_worker_ref uuid,p_batch_limit integer)
RETURNS TABLE(run_id uuid,sessions_deleted integer,tokens_deleted integer,outbox_stopped integer,outbox_payloads_deleted integer,outbox_evidence_deleted integer,
 consent_network_scrubbed integer,event_responses_deleted integer,announcement_deliveries_deleted integer,suggestions_deleted integer,privacy_working_scrubbed integer,auth_limits_deleted integer)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE v_now timestamptz:=clock_timestamp();v_started timestamptz:=v_now;
BEGIN
 IF p_worker_ref IS NULL OR p_batch_limit NOT BETWEEN 1 AND 10000 THEN RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy retention input rejected'; END IF;
 IF NOT pg_try_advisory_xact_lock(247,247) THEN RAISE EXCEPTION USING ERRCODE='55P03',MESSAGE='privacy retention already running'; END IF;
 run_id:=gen_random_uuid();sessions_deleted:=0;tokens_deleted:=0;outbox_stopped:=0;outbox_payloads_deleted:=0;outbox_evidence_deleted:=0;
 consent_network_scrubbed:=0;event_responses_deleted:=0;announcement_deliveries_deleted:=0;suggestions_deleted:=0;privacy_working_scrubbed:=0;auth_limits_deleted:=0;
 WITH candidate AS (SELECT token FROM sessions WHERE expiry<=v_now ORDER BY expiry,token FOR UPDATE SKIP LOCKED LIMIT p_batch_limit)
 DELETE FROM sessions row USING candidate WHERE row.token=candidate.token;GET DIAGNOSTICS sessions_deleted=ROW_COUNT;
 WITH candidate AS (SELECT id FROM email_outbox WHERE created_at<=v_now-interval '7 days' AND status IN ('PENDING','SENDING') ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT p_batch_limit)
 UPDATE email_outbox row SET status='FAILED',claimed_at=NULL,last_error=NULL,updated_at=v_now FROM candidate WHERE row.id=candidate.id;GET DIAGNOSTICS outbox_stopped=ROW_COUNT;
 WITH candidate AS MATERIALIZED (SELECT id FROM email_outbox WHERE created_at<=v_now-interval '30 days' ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT p_batch_limit),
 evidence AS (INSERT INTO privacy_outbox_delivery_evidence(outbox_id,message_type,final_status,attempts,created_at,terminal_at,expires_at)
  SELECT row.id,row.message_type,CASE WHEN row.status IN ('SENT','FAILED','CANCELLED') THEN row.status ELSE 'FAILED' END,row.attempts,row.created_at,COALESCE(row.sent_at,row.updated_at,row.created_at),row.created_at+interval '90 days'
  FROM email_outbox row JOIN candidate USING(id) ON CONFLICT(outbox_id) DO NOTHING RETURNING outbox_id)
 DELETE FROM email_outbox row USING candidate WHERE row.id=candidate.id;GET DIAGNOSTICS outbox_payloads_deleted=ROW_COUNT;
 WITH candidate AS (SELECT outbox_id FROM privacy_outbox_delivery_evidence WHERE expires_at<=v_now ORDER BY expires_at,outbox_id FOR UPDATE SKIP LOCKED LIMIT p_batch_limit)
 DELETE FROM privacy_outbox_delivery_evidence row USING candidate WHERE row.outbox_id=candidate.outbox_id;GET DIAGNOSTICS outbox_evidence_deleted=ROW_COUNT;
 WITH verification AS (SELECT id FROM email_verification_tokens WHERE GREATEST(expires_at,COALESCE(consumed_at,expires_at))<=v_now-interval '30 days' ORDER BY GREATEST(expires_at,COALESCE(consumed_at,expires_at)),id FOR UPDATE SKIP LOCKED LIMIT p_batch_limit),
 deleted_verification AS (DELETE FROM email_verification_tokens row USING verification WHERE row.id=verification.id RETURNING 1),
 reset AS (SELECT id FROM password_reset_tokens WHERE GREATEST(expires_at,COALESCE(consumed_at,expires_at))<=v_now-interval '30 days' ORDER BY GREATEST(expires_at,COALESCE(consumed_at,expires_at)),id FOR UPDATE SKIP LOCKED LIMIT p_batch_limit),
 deleted_reset AS (DELETE FROM password_reset_tokens row USING reset WHERE row.id=reset.id RETURNING 1)
 SELECT (SELECT count(*) FROM deleted_verification)+(SELECT count(*) FROM deleted_reset) INTO tokens_deleted;
 WITH candidate AS (SELECT id FROM consent_forms WHERE date_signed<=v_now-interval '12 months' AND (ip_address IS NOT NULL OR user_agent<>'') ORDER BY date_signed,id FOR UPDATE SKIP LOCKED LIMIT p_batch_limit)
 UPDATE consent_forms row SET ip_address=NULL,user_agent='' FROM candidate WHERE row.id=candidate.id;GET DIAGNOSTICS consent_network_scrubbed=ROW_COUNT;
 WITH candidate AS (SELECT response.event_id,response.user_id FROM event_responses response JOIN events event ON event.id=response.event_id WHERE event.ends_at<=v_now-interval '90 days' ORDER BY event.ends_at,response.event_id,response.user_id FOR UPDATE OF response SKIP LOCKED LIMIT p_batch_limit)
 DELETE FROM event_responses row USING candidate WHERE row.event_id=candidate.event_id AND row.user_id=candidate.user_id;GET DIAGNOSTICS event_responses_deleted=ROW_COUNT;
 WITH candidate AS (SELECT announcement_id,user_id FROM announcement_deliveries WHERE delivered_at<=v_now-interval '90 days' ORDER BY delivered_at,announcement_id,user_id FOR UPDATE SKIP LOCKED LIMIT p_batch_limit)
 DELETE FROM announcement_deliveries row USING candidate WHERE row.announcement_id=candidate.announcement_id AND row.user_id=candidate.user_id;GET DIAGNOSTICS announcement_deliveries_deleted=ROW_COUNT;
 WITH candidate AS (SELECT id FROM suggestions WHERE status IN ('DECLINED','COMPLETED') AND responded_at<=v_now-interval '12 months' ORDER BY responded_at,id FOR UPDATE SKIP LOCKED LIMIT p_batch_limit)
 DELETE FROM suggestions row USING candidate WHERE row.id=candidate.id;GET DIAGNOSTICS suggestions_deleted=ROW_COUNT;
 WITH candidate AS (SELECT id FROM data_erasure_requests WHERE status IN ('REFUSED','CANCELLED','COMPLETED') AND working_expires_at<=v_now AND working_erased_at IS NULL ORDER BY working_expires_at,id FOR UPDATE SKIP LOCKED LIMIT p_batch_limit),
 removed_mail AS (DELETE FROM email_outbox row USING candidate WHERE row.privacy_request_id=candidate.id RETURNING row.id),
 removed_dependants AS (DELETE FROM privacy_request_dependant_resolutions row USING candidate WHERE row.request_id=candidate.id RETURNING 1)
 UPDATE data_erasure_requests row SET decision_explanation='',category_decisions='[]',categories='{}',policy_snapshot=NULL,policy_version=NULL,
 identity_verified_at=NULL,identity_verified_by=NULL,identity_method=NULL,representation_verified_at=NULL,representation_verified_by=NULL,representation_method=NULL,
 representation_guardian_id=NULL,representation_relationship_updated_at=NULL,representation_conflict=false,requester_user_id=NULL,subject_user_id=NULL,claimed_by=NULL,decided_by=NULL,working_erased_at=v_now
 FROM candidate WHERE row.id=candidate.id;GET DIAGNOSTICS privacy_working_scrubbed=ROW_COUNT;
 WITH candidate AS (SELECT bucket FROM privacy_request_auth_limits WHERE window_start<v_now-interval '1 day' ORDER BY window_start,bucket FOR UPDATE SKIP LOCKED LIMIT p_batch_limit)
 DELETE FROM privacy_request_auth_limits row USING candidate WHERE row.bucket=candidate.bucket;GET DIAGNOSTICS auth_limits_deleted=ROW_COUNT;
 INSERT INTO privacy_retention_runs VALUES(run_id,p_worker_ref,v_started,clock_timestamp(),p_batch_limit,sessions_deleted,tokens_deleted,outbox_stopped,outbox_payloads_deleted,
 outbox_evidence_deleted,consent_network_scrubbed,event_responses_deleted,announcement_deliveries_deleted,suggestions_deleted,privacy_working_scrubbed,auth_limits_deleted);
 RETURN NEXT;
END; $$;
REVOKE ALL ON FUNCTION privacy_retention_run(uuid,integer) FROM PUBLIC;
REVOKE ALL ON TABLE privacy_retention_runs,privacy_outbox_delivery_evidence FROM PUBLIC;
-- #247 P0: authenticated v2 tombstones carry a bounded relational-only replay
-- prescription. V1 receipts remain readable evidence, but cannot authorize new
-- destructive work or be imported for replay.
ALTER TABLE privacy_protected.restore_tombstone_receipts
 DROP CONSTRAINT restore_tombstone_receipts_ledger_version_check,
 ADD CONSTRAINT restore_tombstone_receipts_ledger_version_check
 CHECK(ledger_version IN ('restore-tombstone/v1','restore-tombstone/v2'));
ALTER TABLE privacy_protected.restore_tombstone_closure_receipts
 DROP CONSTRAINT restore_tombstone_closure_receipts_ledger_version_check,
 ADD CONSTRAINT restore_tombstone_closure_receipts_ledger_version_check
 CHECK(ledger_version IN ('restore-tombstone-closure/v1','restore-tombstone-closure/v2'));

CREATE FUNCTION public.privacy_relational_replay_operation_supported(p_operation_code text)
RETURNS boolean LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
 SELECT p_operation_code IN (
  'ACTIVITY_CONNECTION_DISCONNECT','ACTIVITY_SUBJECT_DELETE','ANNOUNCEMENT_DELIVERY_DELETE',
  'AUTH_ACCESS_REVOKE','AUTH_TOKEN_DELETE','DEPENDANT_RELATIONSHIP_DELETE','EVENT_RESPONSE_DELETE',
  'IDENTITY_CLEAR','MEMBERSHIP_ACTIVE_REVOKE','MEMBERSHIP_HISTORY_ANONYMIZE','PROFILE_HEALTH_DELETE',
  'PROFILE_IDENTITY_DELETE','REPAIR_REPORTER_ANONYMIZE','SUGGESTION_SUBJECT_DELETE',
  'TRAINING_PRESCRIPTION_DELETE','TRAINING_RESULT_DELETE'
 );
$$;

CREATE FUNCTION public.privacy_tombstone_prepare_v2(
 p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_lease_epoch bigint,p_worker_ref uuid
) RETURNS TABLE(
 execution_id uuid,request_id uuid,request_ref uuid,subject_user_id uuid,plan_sha256 bytea,workset_sha256 bytea,
 execution_started_at timestamptz,replay_operations text[]
) LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT execution.id,request.id,request.public_ref,COALESCE(request.subject_user_id,request.requester_user_id),execution.plan_sha256,
  digest(convert_to(string_agg(job.plan_entry_position::text||':'||encode(job.entry_sha256,'hex')||':'||checkpoint.operation_position::text||':'||checkpoint.operation_code||':'||checkpoint.action_version,
   '|' ORDER BY job.plan_entry_position,checkpoint.operation_position),'UTF8'),'sha256'),execution.accepted_at,
  (array_agg(checkpoint.operation_code ORDER BY job.plan_entry_position,checkpoint.operation_position)
   FILTER(WHERE public.privacy_relational_replay_operation_supported(checkpoint.operation_code)))::text[]
 FROM privacy_erasure_job_leases lease
 JOIN privacy_erasure_category_jobs selected_job ON selected_job.id=lease.job_id AND selected_job.lease_epoch=lease.epoch
 JOIN privacy_erasure_job_attempts attempt ON attempt.lease_id=lease.id AND attempt.job_id=selected_job.id
 JOIN privacy_erasure_executions execution ON execution.id=selected_job.execution_id
 JOIN data_erasure_requests request ON request.id=execution.request_id
 JOIN privacy_erasure_category_jobs job ON job.execution_id=execution.id
 JOIN privacy_erasure_job_checkpoints checkpoint ON checkpoint.job_id=job.id
 WHERE selected_job.id=p_job_id AND lease.id=p_lease_id AND attempt.id=p_attempt_id AND lease.epoch=p_lease_epoch AND lease.worker_ref=p_worker_ref
  AND selected_job.status='LEASED' AND lease.released_at IS NULL AND attempt.finished_at IS NULL AND lease.expires_at>clock_timestamp()
  AND selected_job.category_key='backup-tombstones' AND EXISTS(
   SELECT 1 FROM privacy_erasure_job_checkpoints selected_checkpoint WHERE selected_checkpoint.job_id=selected_job.id
    AND selected_checkpoint.operation_code='BACKUP_TOMBSTONE_REPLAY' AND selected_checkpoint.action_version='v1' AND selected_checkpoint.status='PENDING')
  AND COALESCE(request.subject_user_id,request.requester_user_id) IS NOT NULL
 GROUP BY execution.id,request.id,request.public_ref,request.subject_user_id,request.requester_user_id
 HAVING count(*) FILTER(WHERE public.privacy_relational_replay_operation_supported(checkpoint.operation_code)) BETWEEN 1 AND 16
  AND count(DISTINCT checkpoint.operation_code) FILTER(WHERE public.privacy_relational_replay_operation_supported(checkpoint.operation_code))
   =count(*) FILTER(WHERE public.privacy_relational_replay_operation_supported(checkpoint.operation_code))
  AND count(*) FILTER(WHERE public.privacy_relational_replay_operation_supported(checkpoint.operation_code) AND checkpoint.action_version='v1')
   =count(*) FILTER(WHERE public.privacy_relational_replay_operation_supported(checkpoint.operation_code));
$$;

CREATE FUNCTION public.privacy_tombstone_confirm_v2(
 p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_lease_epoch bigint,p_worker_ref uuid,p_ledger_version text,p_encryption_key_id text,
 p_locator_key_id text,p_locator_digest bytea,p_object_version_id text,p_ciphertext_sha256 bytea,p_size_bytes bigint,p_written_at timestamptz,p_verified_at timestamptz
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE checkpoint_ref uuid; execution_ref uuid; request_ref uuid; existing privacy_protected.restore_tombstone_receipts%ROWTYPE;
BEGIN
 SELECT checkpoint.id,job.execution_id,execution.request_id INTO checkpoint_ref,execution_ref,request_ref
 FROM privacy_erasure_category_jobs job JOIN privacy_erasure_job_leases lease ON lease.job_id=job.id AND lease.epoch=job.lease_epoch
 JOIN privacy_erasure_job_attempts attempt ON attempt.lease_id=lease.id AND attempt.job_id=job.id
 JOIN privacy_erasure_executions execution ON execution.id=job.execution_id JOIN privacy_erasure_job_checkpoints checkpoint ON checkpoint.job_id=job.id
 WHERE job.id=p_job_id AND lease.id=p_lease_id AND attempt.id=p_attempt_id AND lease.epoch=p_lease_epoch AND lease.worker_ref=p_worker_ref AND job.status='LEASED'
  AND lease.released_at IS NULL AND attempt.finished_at IS NULL AND lease.expires_at>clock_timestamp() AND job.category_key='backup-tombstones'
  AND checkpoint.operation_code='BACKUP_TOMBSTONE_REPLAY' AND checkpoint.action_version='v1' FOR UPDATE OF job,lease,attempt,checkpoint;
 IF checkpoint_ref IS NULL OR p_ledger_version<>'restore-tombstone/v2'
  OR p_encryption_key_id IS NULL OR p_encryption_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'
  OR p_locator_key_id IS NULL OR p_locator_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'
  OR octet_length(p_locator_digest)<>32 OR octet_length(p_ciphertext_sha256)<>32 OR p_object_version_id IS NULL
  OR p_object_version_id<>btrim(p_object_version_id) OR char_length(p_object_version_id) NOT BETWEEN 1 AND 1024
  OR p_size_bytes NOT BETWEEN 1 AND 1048576 OR p_written_at IS NULL OR p_verified_at<p_written_at THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_tombstone_receipt_rejected'; END IF;
 SELECT * INTO existing FROM privacy_protected.restore_tombstone_receipts WHERE execution_id=execution_ref;
 IF existing.execution_id IS NULL THEN
  INSERT INTO privacy_protected.restore_tombstone_receipts(execution_id,request_id,ledger_version,encryption_key_id,locator_key_id,locator_digest,object_version_id,ciphertext_sha256,size_bytes,written_at,verified_at)
  VALUES(execution_ref,request_ref,p_ledger_version,p_encryption_key_id,p_locator_key_id,p_locator_digest,p_object_version_id,p_ciphertext_sha256,p_size_bytes,p_written_at,p_verified_at);
 ELSIF ROW(existing.request_id,existing.ledger_version,existing.encryption_key_id,existing.locator_key_id,existing.locator_digest,existing.object_version_id,existing.ciphertext_sha256,existing.size_bytes,existing.written_at,existing.verified_at)
  IS DISTINCT FROM ROW(request_ref,p_ledger_version,p_encryption_key_id,p_locator_key_id,p_locator_digest,p_object_version_id,p_ciphertext_sha256,p_size_bytes,p_written_at,p_verified_at) THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_tombstone_receipt_conflict'; END IF;
 UPDATE privacy_erasure_job_checkpoints SET status='SUCCEEDED',completed_by_attempt_id=p_attempt_id,completed_at=clock_timestamp(),affected_rows=1,
  result_sha256=digest(p_ciphertext_sha256||convert_to(p_object_version_id,'UTF8'),'sha256') WHERE id=checkpoint_ref AND status='PENDING';
 RETURN checkpoint_ref;
END; $$;

CREATE FUNCTION public.privacy_tombstone_prepare_closure_v2(p_execution_id uuid,p_worker_ref uuid)
RETURNS TABLE(execution_id uuid,request_id uuid,request_ref uuid,subject_user_id uuid,plan_sha256 bytea,workset_sha256 bytea,
 execution_started_at timestamptz,closed_at timestamptz,evidence_expires_at timestamptz,replay_operations text[])
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
#variable_conflict use_column
DECLARE v_closed_at timestamptz;
BEGIN
 IF p_execution_id IS NULL OR p_worker_ref IS NULL OR NOT EXISTS(
  SELECT 1 FROM privacy_erasure_executions execution JOIN data_erasure_requests request ON request.id=execution.request_id
  WHERE execution.id=p_execution_id AND execution.status='SUCCEEDED' AND request.status IN ('PROCESSING','RETRYABLE_FAILED')
   AND NOT EXISTS(SELECT 1 FROM privacy_erasure_category_jobs job WHERE job.execution_id=execution.id AND job.status<>'SUCCEEDED')
   AND EXISTS(SELECT 1 FROM privacy_protected.restore_tombstone_receipts receipt WHERE receipt.execution_id=execution.id AND receipt.ledger_version='restore-tombstone/v2'))
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_tombstone_closure_unavailable'; END IF;
 v_closed_at:=clock_timestamp();
 INSERT INTO privacy_protected.restore_tombstone_closure_intents(execution_id,closed_at,evidence_expires_at)
 VALUES(p_execution_id,v_closed_at,v_closed_at+interval '24 months') ON CONFLICT(execution_id) DO NOTHING;
 RETURN QUERY SELECT execution.id,request.id,request.public_ref,COALESCE(request.subject_user_id,request.requester_user_id),execution.plan_sha256,
  digest(convert_to(string_agg(job.plan_entry_position::text||':'||encode(job.entry_sha256,'hex')||':'||checkpoint.operation_position::text||':'||checkpoint.operation_code||':'||checkpoint.action_version,
   '|' ORDER BY job.plan_entry_position,checkpoint.operation_position),'UTF8'),'sha256'),execution.accepted_at,intent.closed_at,intent.evidence_expires_at,
  (array_agg(checkpoint.operation_code ORDER BY job.plan_entry_position,checkpoint.operation_position)
   FILTER(WHERE public.privacy_relational_replay_operation_supported(checkpoint.operation_code)))::text[]
 FROM privacy_erasure_executions execution JOIN data_erasure_requests request ON request.id=execution.request_id
 JOIN privacy_erasure_category_jobs job ON job.execution_id=execution.id JOIN privacy_erasure_job_checkpoints checkpoint ON checkpoint.job_id=job.id
 JOIN privacy_protected.restore_tombstone_closure_intents intent ON intent.execution_id=execution.id
 WHERE execution.id=p_execution_id AND COALESCE(request.subject_user_id,request.requester_user_id) IS NOT NULL
 GROUP BY execution.id,request.id,request.public_ref,request.subject_user_id,request.requester_user_id,intent.closed_at,intent.evidence_expires_at
 HAVING count(*) FILTER(WHERE public.privacy_relational_replay_operation_supported(checkpoint.operation_code)) BETWEEN 1 AND 16
  AND count(DISTINCT checkpoint.operation_code) FILTER(WHERE public.privacy_relational_replay_operation_supported(checkpoint.operation_code))
   =count(*) FILTER(WHERE public.privacy_relational_replay_operation_supported(checkpoint.operation_code))
  AND count(*) FILTER(WHERE public.privacy_relational_replay_operation_supported(checkpoint.operation_code) AND checkpoint.action_version='v1')
   =count(*) FILTER(WHERE public.privacy_relational_replay_operation_supported(checkpoint.operation_code));
END; $$;

CREATE FUNCTION public.privacy_tombstone_confirm_closure_v2(
 p_execution_id uuid,p_worker_ref uuid,p_ledger_version text,p_encryption_key_id text,p_locator_key_id text,p_locator_digest bytea,
 p_object_version_id text,p_ciphertext_sha256 bytea,p_size_bytes bigint,p_written_at timestamptz,p_verified_at timestamptz
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE intent privacy_protected.restore_tombstone_closure_intents%ROWTYPE; existing privacy_protected.restore_tombstone_closure_receipts%ROWTYPE;
BEGIN
 SELECT * INTO intent FROM privacy_protected.restore_tombstone_closure_intents WHERE execution_id=p_execution_id;
 IF intent.execution_id IS NULL OR p_worker_ref IS NULL OR p_ledger_version<>'restore-tombstone-closure/v2'
  OR p_encryption_key_id IS NULL OR p_encryption_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'
  OR p_locator_key_id IS NULL OR p_locator_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'
  OR octet_length(p_locator_digest)<>32 OR octet_length(p_ciphertext_sha256)<>32 OR p_object_version_id IS NULL
  OR p_object_version_id<>btrim(p_object_version_id) OR char_length(p_object_version_id) NOT BETWEEN 1 AND 1024
  OR p_size_bytes NOT BETWEEN 1 AND 1048576 OR p_written_at IS NULL OR p_verified_at<p_written_at OR p_verified_at>intent.evidence_expires_at THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_tombstone_closure_receipt_rejected'; END IF;
 SELECT * INTO existing FROM privacy_protected.restore_tombstone_closure_receipts WHERE execution_id=p_execution_id;
 IF existing.execution_id IS NULL THEN
  INSERT INTO privacy_protected.restore_tombstone_closure_receipts(execution_id,ledger_version,encryption_key_id,locator_key_id,locator_digest,object_version_id,ciphertext_sha256,size_bytes,written_at,verified_at)
  VALUES(p_execution_id,p_ledger_version,p_encryption_key_id,p_locator_key_id,p_locator_digest,p_object_version_id,p_ciphertext_sha256,p_size_bytes,p_written_at,p_verified_at);
 ELSIF ROW(existing.ledger_version,existing.encryption_key_id,existing.locator_key_id,existing.locator_digest,existing.object_version_id,existing.ciphertext_sha256,existing.size_bytes,existing.written_at,existing.verified_at)
  IS DISTINCT FROM ROW(p_ledger_version,p_encryption_key_id,p_locator_key_id,p_locator_digest,p_object_version_id,p_ciphertext_sha256,p_size_bytes,p_written_at,p_verified_at) THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_tombstone_closure_receipt_conflict'; END IF;
 RETURN p_execution_id;
END; $$;

CREATE TABLE privacy_protected.restore_ledger_imports (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),kind varchar(16) NOT NULL CHECK(kind IN ('intent','closure')),
 record_version varchar(40) NOT NULL CHECK(record_version='restore-tombstone/v2'),
 envelope_version varchar(48) NOT NULL CHECK(envelope_version='x25519-aes256gcm-hkdfsha256/v2'),
 encryption_key_id varchar(80) NOT NULL CHECK(encryption_key_id=btrim(encryption_key_id) AND encryption_key_id~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'),
 locator_key_id varchar(80) NOT NULL CHECK(locator_key_id=btrim(locator_key_id) AND locator_key_id~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'),
 locator_digest bytea NOT NULL CHECK(octet_length(locator_digest)=32),ciphertext_sha256 bytea NOT NULL CHECK(octet_length(ciphertext_sha256)=32),
 object_version_id varchar(1024) NOT NULL CHECK(object_version_id=btrim(object_version_id) AND char_length(object_version_id) BETWEEN 1 AND 1024),
 written_at timestamptz NOT NULL,verified_at timestamptz NOT NULL,retain_until timestamptz NULL,
 source_execution_id uuid NOT NULL,source_request_id uuid NOT NULL,source_request_ref uuid NOT NULL,subject_user_id uuid NOT NULL,
 plan_sha256 bytea NOT NULL CHECK(octet_length(plan_sha256)=32),workset_sha256 bytea NOT NULL CHECK(octet_length(workset_sha256)=32),
 execution_started_at timestamptz NOT NULL,replay_version varchar(40) NOT NULL CHECK(replay_version='relational-erasure-replay/v1'),
 action_version varchar(16) NOT NULL CHECK(action_version='v1'),operations text[] NOT NULL,
 prescription_sha256 bytea NOT NULL CHECK(octet_length(prescription_sha256)=32),record_sha256 bytea NOT NULL CHECK(octet_length(record_sha256)=32),
 imported_by_ref uuid NOT NULL,imported_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK(verified_at>=written_at),CHECK((kind='intent' AND retain_until IS NULL) OR (kind='closure' AND retain_until IS NOT NULL)),
 CHECK(cardinality(operations) BETWEEN 1 AND 16),UNIQUE(locator_key_id,locator_digest),UNIQUE(source_execution_id)
);
CREATE TRIGGER privacy_restore_ledger_imports_immutable BEFORE UPDATE OR DELETE ON privacy_protected.restore_ledger_imports
 FOR EACH ROW EXECUTE FUNCTION public.prevent_privacy_execution_record_delete();

CREATE TABLE privacy_protected.restore_replay_runs (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),import_id uuid NOT NULL UNIQUE REFERENCES privacy_protected.restore_ledger_imports(id) ON DELETE RESTRICT,
 worker_ref uuid NOT NULL,status varchar(16) NOT NULL DEFAULT 'PENDING' CHECK(status IN ('PENDING','SUCCEEDED')),
 started_at timestamptz NOT NULL DEFAULT clock_timestamp(),completed_at timestamptz NULL,
 CHECK((status='PENDING' AND completed_at IS NULL) OR (status='SUCCEEDED' AND completed_at IS NOT NULL))
);
CREATE TABLE privacy_protected.restore_replay_checkpoints (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),run_id uuid NOT NULL REFERENCES privacy_protected.restore_replay_runs(id) ON DELETE RESTRICT,
 operation_position smallint NOT NULL CHECK(operation_position>0),operation_code varchar(80) NOT NULL,action_version varchar(16) NOT NULL,
 status varchar(16) NOT NULL DEFAULT 'PENDING' CHECK(status IN ('PENDING','SUCCEEDED')),affected_rows bigint NULL CHECK(affected_rows IS NULL OR affected_rows>=0),
 result_sha256 bytea NULL CHECK(result_sha256 IS NULL OR octet_length(result_sha256)=32),completed_at timestamptz NULL,
 UNIQUE(run_id,operation_position),UNIQUE(run_id,operation_code),
 CHECK((status='PENDING' AND affected_rows IS NULL AND result_sha256 IS NULL AND completed_at IS NULL) OR
       (status='SUCCEEDED' AND affected_rows IS NOT NULL AND result_sha256 IS NOT NULL AND completed_at IS NOT NULL))
);

-- Account cutoff happens before ordinary live checkpoints, so a restore from
-- an older backup must carry those privilege revocations into replay too.
-- Dedicated replay provenance avoids inventing a user actor.
ALTER TABLE staff_grants
 ADD COLUMN revoked_by_replay_run_id uuid NULL REFERENCES privacy_protected.restore_replay_runs(id) ON DELETE RESTRICT,
 DROP CONSTRAINT staff_grants_revocation_valid,
 ADD CONSTRAINT staff_grants_revocation_valid CHECK(
  (revoked_at IS NULL AND revoked_by_id IS NULL AND revoked_by_replay_run_id IS NULL AND revoke_reason IS NULL)
  OR (revoked_at IS NOT NULL AND num_nonnulls(revoked_by_id,revoked_by_replay_run_id)=1
      AND revoke_reason=btrim(revoke_reason) AND char_length(revoke_reason) BETWEEN 1 AND 500)
 ) NOT VALID;
ALTER TABLE privacy_reviewer_grants
 ADD COLUMN revoked_by_replay_run_id uuid NULL REFERENCES privacy_protected.restore_replay_runs(id) ON DELETE RESTRICT,
 DROP CONSTRAINT privacy_reviewer_grants_check,
 ADD CONSTRAINT privacy_reviewer_grants_revocation_actor_exactly_one CHECK(
  (revoked_at IS NULL AND revoked_by IS NULL AND revoked_by_replay_run_id IS NULL)
  OR (revoked_at IS NOT NULL AND num_nonnulls(revoked_by,revoked_by_replay_run_id)=1)
 ) NOT VALID;
ALTER TABLE privacy_executor_grants
 ADD COLUMN revoked_by_replay_run_id uuid NULL REFERENCES privacy_protected.restore_replay_runs(id) ON DELETE RESTRICT,
 DROP CONSTRAINT privacy_executor_grants_check,
 ADD CONSTRAINT privacy_executor_grants_revocation_actor_exactly_one CHECK(
  (revoked_at IS NULL AND revoked_by IS NULL AND revoked_by_replay_run_id IS NULL)
  OR (revoked_at IS NOT NULL AND num_nonnulls(revoked_by,revoked_by_replay_run_id)=1)
 ) NOT VALID;
ALTER TABLE staff_grant_audit_events
 ADD COLUMN actor_replay_run_id uuid NULL REFERENCES privacy_protected.restore_replay_runs(id) ON DELETE RESTRICT,
 DROP CONSTRAINT staff_grant_audit_actor_principal_exactly_one,
 ADD CONSTRAINT staff_grant_audit_actor_principal_exactly_one
  CHECK(num_nonnulls(actor_user_id,actor_principal_id,actor_replay_run_id)=1) NOT VALID;
ALTER TABLE staff_grants VALIDATE CONSTRAINT staff_grants_revocation_valid;
ALTER TABLE privacy_reviewer_grants VALIDATE CONSTRAINT privacy_reviewer_grants_revocation_actor_exactly_one;
ALTER TABLE privacy_executor_grants VALIDATE CONSTRAINT privacy_executor_grants_revocation_actor_exactly_one;
ALTER TABLE staff_grant_audit_events VALIDATE CONSTRAINT staff_grant_audit_actor_principal_exactly_one;

CREATE OR REPLACE FUNCTION public.audit_staff_grant_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  INSERT INTO staff_grant_audit_events(staff_grant_id,action,actor_user_id) VALUES(NEW.id,'GRANTED',NEW.granted_by_id);
  RETURN NEW;
 END IF;
 IF OLD.user_id<>NEW.user_id OR OLD.capability<>NEW.capability OR OLD.programme_id IS DISTINCT FROM NEW.programme_id
  OR OLD.team_id IS DISTINCT FROM NEW.team_id OR OLD.granted_by_id<>NEW.granted_by_id OR OLD.granted_at<>NEW.granted_at
  OR OLD.revoked_at IS NOT NULL OR NEW.revoked_at IS NULL THEN
  RAISE EXCEPTION 'staff grants are immutable except for one revocation';
 END IF;
 INSERT INTO staff_grant_audit_events(staff_grant_id,action,actor_user_id,actor_replay_run_id,occurred_at,reason)
 VALUES(NEW.id,'REVOKED',NEW.revoked_by_id,NEW.revoked_by_replay_run_id,NEW.revoked_at,NEW.revoke_reason);
 RETURN NEW;
END; $$;

ALTER TABLE users ADD COLUMN erasure_replay_run_id uuid NULL
 REFERENCES privacy_protected.restore_replay_runs(id) ON DELETE RESTRICT;
CREATE UNIQUE INDEX users_erasure_replay_run_uidx ON users(erasure_replay_run_id) WHERE erasure_replay_run_id IS NOT NULL;
ALTER TABLE users DROP CONSTRAINT users_identity_shape;
ALTER TABLE users ADD CONSTRAINT users_identity_shape CHECK (
 (erased_at IS NULL AND erasure_execution_id IS NULL AND erasure_replay_run_id IS NULL AND (
   (is_dependent AND guardian_id IS NOT NULL AND email IS NULL AND
    ((minor_login_id IS NULL AND password_hash IS NULL) OR (minor_login_id IS NOT NULL AND password_hash IS NOT NULL)))
   OR
   (NOT is_dependent AND guardian_id IS NULL AND email IS NOT NULL AND password_hash IS NOT NULL AND minor_login_id IS NULL)
 ))
 OR
 (erased_at IS NOT NULL AND num_nonnulls(erasure_execution_id,erasure_replay_run_id)=1 AND NOT is_active
  AND NOT leaderboard_visible AND NOT is_dependent AND guardian_id IS NULL
  AND email IS NULL AND email_verified_at IS NULL AND minor_login_id IS NULL
  AND password_hash IS NULL AND name='Conta eliminada' AND date_of_birth=DATE '1900-01-01')
) NOT VALID;
ALTER TABLE users VALIDATE CONSTRAINT users_identity_shape;

CREATE FUNCTION public.privacy_restore_import_authenticated_v2(
 p_worker_ref uuid,p_kind text,p_record_version text,p_envelope_version text,p_encryption_key_id text,p_locator_key_id text,p_locator_digest bytea,
 p_ciphertext_sha256 bytea,p_object_version_id text,p_written_at timestamptz,p_verified_at timestamptz,p_retain_until timestamptz,
 p_source_execution_id uuid,p_source_request_id uuid,p_source_request_ref uuid,p_subject_user_id uuid,p_plan_sha256 bytea,p_workset_sha256 bytea,
 p_execution_started_at timestamptz,p_replay_version text,p_action_version text,p_operations text[],p_prescription_sha256 bytea,p_record_sha256 bytea
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE import_ref uuid; existing privacy_protected.restore_ledger_imports%ROWTYPE;
BEGIN
 IF p_worker_ref IS NULL OR p_kind NOT IN ('intent','closure') OR p_record_version<>'restore-tombstone/v2'
  OR p_envelope_version<>'x25519-aes256gcm-hkdfsha256/v2' OR p_replay_version<>'relational-erasure-replay/v1' OR p_action_version<>'v1'
  OR p_encryption_key_id IS NULL OR p_encryption_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'
  OR p_locator_key_id IS NULL OR p_locator_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'
  OR octet_length(p_locator_digest)<>32 OR octet_length(p_ciphertext_sha256)<>32 OR octet_length(p_plan_sha256)<>32
  OR octet_length(p_workset_sha256)<>32 OR octet_length(p_prescription_sha256)<>32 OR octet_length(p_record_sha256)<>32
  OR p_object_version_id IS NULL OR p_object_version_id<>btrim(p_object_version_id) OR char_length(p_object_version_id) NOT BETWEEN 1 AND 1024
  OR p_written_at IS NULL OR p_verified_at<p_written_at OR p_source_execution_id IS NULL OR p_source_request_id IS NULL
  OR p_source_request_ref IS NULL OR p_subject_user_id IS NULL OR p_execution_started_at IS NULL
  OR cardinality(p_operations) NOT BETWEEN 1 AND 16 OR EXISTS(SELECT 1 FROM unnest(p_operations) operation WHERE NOT public.privacy_relational_replay_operation_supported(operation))
  OR cardinality(p_operations)<>(SELECT count(DISTINCT operation) FROM unnest(p_operations) operation)
  OR (p_kind='intent' AND p_retain_until IS NOT NULL) OR (p_kind='closure' AND (p_retain_until IS NULL OR p_verified_at>p_retain_until)) THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_import_rejected'; END IF;
 SELECT * INTO existing FROM privacy_protected.restore_ledger_imports
  WHERE (locator_key_id=p_locator_key_id AND locator_digest=p_locator_digest) OR source_execution_id=p_source_execution_id FOR UPDATE;
 IF existing.id IS NULL THEN
  INSERT INTO privacy_protected.restore_ledger_imports(kind,record_version,envelope_version,encryption_key_id,locator_key_id,locator_digest,ciphertext_sha256,
   object_version_id,written_at,verified_at,retain_until,source_execution_id,source_request_id,source_request_ref,subject_user_id,plan_sha256,workset_sha256,
   execution_started_at,replay_version,action_version,operations,prescription_sha256,record_sha256,imported_by_ref)
  VALUES(p_kind,p_record_version,p_envelope_version,p_encryption_key_id,p_locator_key_id,p_locator_digest,p_ciphertext_sha256,p_object_version_id,p_written_at,
   p_verified_at,p_retain_until,p_source_execution_id,p_source_request_id,p_source_request_ref,p_subject_user_id,p_plan_sha256,p_workset_sha256,
   p_execution_started_at,p_replay_version,p_action_version,p_operations,p_prescription_sha256,p_record_sha256,p_worker_ref) RETURNING id INTO import_ref;
 ELSIF ROW(existing.kind,existing.record_version,existing.envelope_version,existing.encryption_key_id,existing.locator_key_id,existing.locator_digest,
   existing.ciphertext_sha256,existing.object_version_id,existing.written_at,existing.verified_at,existing.retain_until,existing.source_execution_id,
   existing.source_request_id,existing.source_request_ref,existing.subject_user_id,existing.plan_sha256,existing.workset_sha256,existing.execution_started_at,
   existing.replay_version,existing.action_version,existing.operations,existing.prescription_sha256,existing.record_sha256)
  IS DISTINCT FROM ROW(p_kind,p_record_version,p_envelope_version,p_encryption_key_id,p_locator_key_id,p_locator_digest,p_ciphertext_sha256,
   p_object_version_id,p_written_at,p_verified_at,p_retain_until,p_source_execution_id,p_source_request_id,p_source_request_ref,p_subject_user_id,
   p_plan_sha256,p_workset_sha256,p_execution_started_at,p_replay_version,p_action_version,p_operations,p_prescription_sha256,p_record_sha256) THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_import_conflict';
 ELSE import_ref:=existing.id; END IF;
 RETURN import_ref;
END; $$;

CREATE FUNCTION public.privacy_restore_begin_replay(p_import_id uuid,p_worker_ref uuid)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE run_ref uuid; imported privacy_protected.restore_ledger_imports%ROWTYPE;
BEGIN
 SELECT * INTO imported FROM privacy_protected.restore_ledger_imports WHERE id=p_import_id;
 IF imported.id IS NULL OR p_worker_ref IS NULL OR imported.record_version<>'restore-tombstone/v2' OR imported.envelope_version<>'x25519-aes256gcm-hkdfsha256/v2'
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_replay_rejected'; END IF;
 INSERT INTO privacy_protected.restore_replay_runs(import_id,worker_ref) VALUES(p_import_id,p_worker_ref) ON CONFLICT(import_id) DO NOTHING;
 SELECT id INTO run_ref FROM privacy_protected.restore_replay_runs WHERE import_id=p_import_id FOR UPDATE;
 -- Isolated replay may resume after a process crash. The authenticated import
 -- is immutable; moving only its execution fence to the new worker preserves
 -- exact idempotency without weakening the prescription.
 UPDATE privacy_protected.restore_replay_runs SET worker_ref=p_worker_ref
  WHERE id=run_ref AND status='PENDING' AND worker_ref<>p_worker_ref;
 INSERT INTO privacy_protected.restore_replay_checkpoints(run_id,operation_position,operation_code,action_version)
 SELECT run_ref,ordinality::smallint,operation,imported.action_version FROM unnest(imported.operations) WITH ORDINALITY item(operation,ordinality)
 ON CONFLICT(run_id,operation_position) DO NOTHING;
 IF (SELECT count(*) FROM privacy_protected.restore_replay_checkpoints WHERE run_id=run_ref)<>cardinality(imported.operations)
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_replay_conflict'; END IF;
 RETURN run_ref;
END; $$;

CREATE FUNCTION public.privacy_restore_apply_relational_operation(
 p_replay_run_id uuid,p_subject_user_id uuid,p_effective_at timestamptz,p_operation_code text
) RETURNS bigint LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE changed bigint:=0; n bigint:=0; principal_ref uuid; subject_name text; subject_email text; subject_login text;
BEGIN
 IF p_replay_run_id IS NULL OR p_subject_user_id IS NULL OR p_effective_at IS NULL OR NOT public.privacy_relational_replay_operation_supported(p_operation_code)
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_operation_rejected'; END IF;
 SELECT name,email::text,minor_login_id::text INTO subject_name,subject_email,subject_login FROM users WHERE id=p_subject_user_id FOR UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_subject_missing'; END IF;
 CASE p_operation_code
 WHEN 'ACTIVITY_CONNECTION_DISCONNECT' THEN
  UPDATE activity_connections SET status='DISCONNECTED',provider_user_id='erased-'||id::text,credentials_ciphertext=NULL,credential_key_id=NULL,
   credential_expires_at=NULL,scopes='{}',sync_cursor=NULL,last_error_code=NULL,last_error_message=NULL,last_error_at=NULL,
   disconnected_at=COALESCE(disconnected_at,clock_timestamp()),updated_at=clock_timestamp()
  WHERE user_id=p_subject_user_id AND status<>'DISCONNECTED'; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'ACTIVITY_SUBJECT_DELETE' THEN
  DELETE FROM activity_connections WHERE user_id=p_subject_user_id; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'ANNOUNCEMENT_DELIVERY_DELETE' THEN
  DELETE FROM announcement_deliveries WHERE user_id=p_subject_user_id; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'AUTH_ACCESS_REVOKE' THEN
  DELETE FROM sessions WHERE subject_indexed AND user_id=p_subject_user_id; GET DIAGNOSTICS changed=ROW_COUNT;
  DELETE FROM user_platform_roles WHERE user_id=p_subject_user_id; GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
  UPDATE staff_grants SET revoked_by_id=NULL,revoked_by_replay_run_id=p_replay_run_id,revoked_at=p_effective_at,
   revoke_reason='PRIVACY_ACCOUNT_CLOSURE' WHERE user_id=p_subject_user_id AND revoked_at IS NULL;
  GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
  WITH revoked AS(UPDATE privacy_reviewer_grants SET revoked_by=NULL,revoked_by_replay_run_id=p_replay_run_id,revoked_at=p_effective_at
   WHERE user_id=p_subject_user_id AND revoked_at IS NULL RETURNING id)
  INSERT INTO privacy_reviewer_grant_events(grant_id,actor_ref,action,occurred_at) SELECT id,p_replay_run_id,'REVOKED',p_effective_at FROM revoked;
  GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
  WITH revoked AS(UPDATE privacy_executor_grants SET revoked_by=NULL,revoked_by_replay_run_id=p_replay_run_id,revoked_at=p_effective_at
   WHERE user_id=p_subject_user_id AND revoked_at IS NULL RETURNING id)
  INSERT INTO privacy_executor_grant_events(grant_id,actor_ref,action,occurred_at) SELECT id,p_replay_run_id,'REVOKED',p_effective_at FROM revoked;
  GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
 WHEN 'AUTH_TOKEN_DELETE' THEN
  DELETE FROM email_verification_tokens WHERE user_id=p_subject_user_id; GET DIAGNOSTICS changed=ROW_COUNT;
  DELETE FROM password_reset_tokens WHERE user_id=p_subject_user_id; GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
 WHEN 'DEPENDANT_RELATIONSHIP_DELETE' THEN
  IF EXISTS(SELECT 1 FROM users WHERE guardian_id=p_subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_dependants_unresolved'; END IF;
 WHEN 'EVENT_RESPONSE_DELETE' THEN
  DELETE FROM event_responses WHERE user_id=p_subject_user_id; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'IDENTITY_CLEAR' THEN
  IF EXISTS(SELECT 1 FROM sessions WHERE NOT subject_indexed AND expiry>clock_timestamp()) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_legacy_sessions_unresolved'; END IF;
  IF EXISTS(SELECT 1 FROM users WHERE guardian_id=p_subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_dependants_unresolved'; END IF;
  DELETE FROM sessions WHERE subject_indexed AND user_id=p_subject_user_id;
  DELETE FROM email_verification_tokens WHERE user_id=p_subject_user_id;
  DELETE FROM password_reset_tokens WHERE user_id=p_subject_user_id;
  DELETE FROM user_platform_roles WHERE user_id=p_subject_user_id;
  UPDATE users SET name='Conta eliminada',email=NULL,email_verified_at=NULL,minor_login_id=NULL,password_hash=NULL,guardian_id=NULL,is_dependent=false,
   date_of_birth=DATE '1900-01-01',is_active=false,leaderboard_visible=false,credential_version=credential_version+1,
   erased_at=clock_timestamp(),erasure_execution_id=NULL,erasure_replay_run_id=p_replay_run_id,updated_at=clock_timestamp()
  WHERE id=p_subject_user_id AND erased_at IS NULL; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'MEMBERSHIP_ACTIVE_REVOKE' THEN
  IF EXISTS(SELECT 1 FROM training_prescriptions prescription JOIN user_memberships membership ON membership.id=prescription.membership_id
   WHERE membership.user_id=p_subject_user_id AND membership.starts_on>=p_effective_at::date) THEN
   RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_dependency_unsupported'; END IF;
  DELETE FROM training_variations WHERE target_membership_id IN(SELECT id FROM user_memberships WHERE user_id=p_subject_user_id AND starts_on>=p_effective_at::date);
  GET DIAGNOSTICS changed=ROW_COUNT;
  DELETE FROM training_variation_group_members WHERE membership_id IN(SELECT id FROM user_memberships WHERE user_id=p_subject_user_id AND starts_on>=p_effective_at::date);
  GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
  DELETE FROM training_group_members WHERE membership_id IN(SELECT id FROM user_memberships WHERE user_id=p_subject_user_id AND starts_on>=p_effective_at::date);
  GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
  DELETE FROM user_memberships WHERE user_id=p_subject_user_id AND starts_on>=p_effective_at::date;
  GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
  UPDATE user_memberships SET ends_on=p_effective_at::date-1,updated_at=clock_timestamp()
   WHERE user_id=p_subject_user_id AND starts_on<p_effective_at::date AND (ends_on IS NULL OR ends_on>=p_effective_at::date);
  GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
 WHEN 'MEMBERSHIP_HISTORY_ANONYMIZE' THEN
  IF EXISTS(SELECT 1 FROM training_prescriptions prescription JOIN user_memberships membership ON membership.id=prescription.membership_id
   WHERE membership.user_id=p_subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_dependency_unsupported'; END IF;
  IF EXISTS(SELECT 1 FROM user_memberships WHERE user_id=p_subject_user_id) THEN
   INSERT INTO privacy_pseudonymous_principals(purpose) VALUES('MEMBERSHIP') RETURNING id INTO principal_ref;
   PERFORM set_config('mycfc.privacy_erasure_operation','MEMBERSHIP_HISTORY_ANONYMIZE',true);
   UPDATE training_variations SET change_summary=privacy_scrub_audit_text(change_summary,p_subject_user_id,subject_name,subject_email,subject_login),
    patch=privacy_scrub_audit_json(patch,p_subject_user_id,subject_name,subject_email,subject_login),updated_at=clock_timestamp()
   WHERE target_membership_id IN(SELECT id FROM user_memberships WHERE user_id=p_subject_user_id); GET DIAGNOSTICS changed=ROW_COUNT;
   UPDATE user_memberships SET user_id=NULL,principal_id=principal_ref,updated_at=clock_timestamp() WHERE user_id=p_subject_user_id;
   GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
  END IF;
 WHEN 'PROFILE_HEALTH_DELETE' THEN
  UPDATE member_profiles SET emergency_contact_name='',emergency_contact_relationship='',emergency_contact_phone='',emergency_contact_alternate_phone='',
   medical_declaration='UNKNOWN',allergies='',medical_conditions='',medication='',activity_restrictions='',medical_notes='',updated_at=clock_timestamp()
  WHERE user_id=p_subject_user_id; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'PROFILE_IDENTITY_DELETE' THEN
  DELETE FROM member_profiles WHERE user_id=p_subject_user_id; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'REPAIR_REPORTER_ANONYMIZE' THEN
  UPDATE repair_requests SET reported_by_id=NULL,updated_at=clock_timestamp() WHERE reported_by_id=p_subject_user_id; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'SUGGESTION_SUBJECT_DELETE' THEN
  DELETE FROM suggestions WHERE requester_id=p_subject_user_id; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'TRAINING_PRESCRIPTION_DELETE' THEN
  UPDATE training_session_outcomes SET prescription_id=NULL,updated_at=clock_timestamp()
   WHERE prescription_id IN(SELECT id FROM training_prescriptions WHERE athlete_user_id=p_subject_user_id);
  PERFORM set_config('mycfc.privacy_erasure_operation','TRAINING_PRESCRIPTION_DELETE',true);
  DELETE FROM training_prescriptions WHERE athlete_user_id=p_subject_user_id; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'TRAINING_RESULT_DELETE' THEN
  DELETE FROM training_session_outcomes WHERE user_id=p_subject_user_id; GET DIAGNOSTICS changed=ROW_COUNT;
  DELETE FROM training_logs WHERE user_id=p_subject_user_id; GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
  DELETE FROM performance_metrics WHERE user_id=p_subject_user_id; GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
 END CASE;

 CASE p_operation_code
 WHEN 'ACTIVITY_CONNECTION_DISCONNECT' THEN
  IF EXISTS(SELECT 1 FROM activity_connections WHERE user_id=p_subject_user_id AND (status<>'DISCONNECTED' OR credentials_ciphertext IS NOT NULL OR credential_key_id IS NOT NULL OR cardinality(scopes)>0 OR sync_cursor IS NOT NULL)) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'ACTIVITY_SUBJECT_DELETE' THEN IF EXISTS(SELECT 1 FROM activity_connections WHERE user_id=p_subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'ANNOUNCEMENT_DELIVERY_DELETE' THEN IF EXISTS(SELECT 1 FROM announcement_deliveries WHERE user_id=p_subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'AUTH_ACCESS_REVOKE' THEN IF EXISTS(SELECT 1 FROM sessions WHERE subject_indexed AND user_id=p_subject_user_id)
  OR EXISTS(SELECT 1 FROM user_platform_roles WHERE user_id=p_subject_user_id)
  OR EXISTS(SELECT 1 FROM staff_grants WHERE user_id=p_subject_user_id AND revoked_at IS NULL)
  OR EXISTS(SELECT 1 FROM privacy_reviewer_grants WHERE user_id=p_subject_user_id AND revoked_at IS NULL)
  OR EXISTS(SELECT 1 FROM privacy_executor_grants WHERE user_id=p_subject_user_id AND revoked_at IS NULL)
  THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'AUTH_TOKEN_DELETE' THEN IF EXISTS(SELECT 1 FROM email_verification_tokens WHERE user_id=p_subject_user_id) OR EXISTS(SELECT 1 FROM password_reset_tokens WHERE user_id=p_subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'DEPENDANT_RELATIONSHIP_DELETE' THEN IF EXISTS(SELECT 1 FROM users WHERE guardian_id=p_subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'EVENT_RESPONSE_DELETE' THEN IF EXISTS(SELECT 1 FROM event_responses WHERE user_id=p_subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'IDENTITY_CLEAR' THEN IF NOT EXISTS(SELECT 1 FROM users WHERE id=p_subject_user_id AND erased_at IS NOT NULL AND erasure_execution_id IS NULL
  AND erasure_replay_run_id=p_replay_run_id AND NOT is_active AND email IS NULL AND minor_login_id IS NULL AND password_hash IS NULL AND guardian_id IS NULL)
  THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'MEMBERSHIP_ACTIVE_REVOKE' THEN IF EXISTS(SELECT 1 FROM user_memberships WHERE user_id=p_subject_user_id AND (starts_on>=p_effective_at::date OR ends_on IS NULL OR ends_on>=p_effective_at::date)) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'MEMBERSHIP_HISTORY_ANONYMIZE' THEN
  IF EXISTS(SELECT 1 FROM user_memberships WHERE user_id=p_subject_user_id)
   OR EXISTS(SELECT 1 FROM user_memberships WHERE principal_id=principal_ref AND (starts_on>=p_effective_at::date OR ends_on IS NULL OR ends_on>=p_effective_at::date))
   OR EXISTS(SELECT 1 FROM training_variations variation JOIN user_memberships membership ON membership.id=variation.target_membership_id
      WHERE membership.principal_id=principal_ref
       AND (variation.change_summary IS DISTINCT FROM privacy_scrub_audit_text(variation.change_summary,p_subject_user_id,subject_name,subject_email,subject_login)
        OR variation.patch IS DISTINCT FROM privacy_scrub_audit_json(variation.patch,p_subject_user_id,subject_name,subject_email,subject_login)))
  THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'PROFILE_HEALTH_DELETE' THEN IF EXISTS(SELECT 1 FROM member_profiles WHERE user_id=p_subject_user_id AND (emergency_contact_name<>'' OR emergency_contact_relationship<>'' OR emergency_contact_phone<>'' OR emergency_contact_alternate_phone<>'' OR medical_declaration<>'UNKNOWN' OR allergies<>'' OR medical_conditions<>'' OR medication<>'' OR activity_restrictions<>'' OR medical_notes<>'')) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'PROFILE_IDENTITY_DELETE' THEN IF EXISTS(SELECT 1 FROM member_profiles WHERE user_id=p_subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'REPAIR_REPORTER_ANONYMIZE' THEN IF EXISTS(SELECT 1 FROM repair_requests WHERE reported_by_id=p_subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'SUGGESTION_SUBJECT_DELETE' THEN IF EXISTS(SELECT 1 FROM suggestions WHERE requester_id=p_subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'TRAINING_PRESCRIPTION_DELETE' THEN IF EXISTS(SELECT 1 FROM training_prescriptions WHERE athlete_user_id=p_subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'TRAINING_RESULT_DELETE' THEN IF EXISTS(SELECT 1 FROM training_session_outcomes WHERE user_id=p_subject_user_id) OR EXISTS(SELECT 1 FROM training_logs WHERE user_id=p_subject_user_id) OR EXISTS(SELECT 1 FROM performance_metrics WHERE user_id=p_subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 END CASE;
 RETURN changed;
END; $$;

CREATE FUNCTION public.privacy_restore_execute_checkpoint(
 p_run_id uuid,p_worker_ref uuid,p_operation_position smallint,p_operation_code text,p_action_version text,p_prescription_sha256 bytea
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE run_row privacy_protected.restore_replay_runs%ROWTYPE; imported privacy_protected.restore_ledger_imports%ROWTYPE;
 checkpoint_row privacy_protected.restore_replay_checkpoints%ROWTYPE; changed bigint; result_digest bytea;
BEGIN
 SELECT * INTO run_row FROM privacy_protected.restore_replay_runs WHERE id=p_run_id FOR UPDATE;
 SELECT * INTO imported FROM privacy_protected.restore_ledger_imports WHERE id=run_row.import_id;
 SELECT * INTO checkpoint_row FROM privacy_protected.restore_replay_checkpoints
  WHERE run_id=p_run_id AND operation_position=p_operation_position FOR UPDATE;
 IF run_row.id IS NULL OR imported.id IS NULL OR checkpoint_row.id IS NULL OR run_row.worker_ref<>p_worker_ref OR run_row.status NOT IN ('PENDING','SUCCEEDED')
  OR checkpoint_row.operation_code<>p_operation_code OR checkpoint_row.action_version<>p_action_version
  OR imported.prescription_sha256<>p_prescription_sha256 OR imported.action_version<>p_action_version
  OR imported.operations[p_operation_position]<>p_operation_code OR NOT public.privacy_relational_replay_operation_supported(p_operation_code)
  OR EXISTS(SELECT 1 FROM privacy_protected.restore_replay_checkpoints prior WHERE prior.run_id=p_run_id AND prior.operation_position<p_operation_position AND prior.status<>'SUCCEEDED')
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_checkpoint_rejected'; END IF;
 IF checkpoint_row.status='SUCCEEDED' THEN RETURN checkpoint_row.id; END IF;
 changed:=public.privacy_restore_apply_relational_operation(p_run_id,imported.subject_user_id,imported.execution_started_at,p_operation_code);
 result_digest:=digest(convert_to(p_operation_position::text||':'||p_operation_code||':'||changed::text,'UTF8'),'sha256');
 UPDATE privacy_protected.restore_replay_checkpoints SET status='SUCCEEDED',affected_rows=changed,result_sha256=result_digest,completed_at=clock_timestamp()
  WHERE id=checkpoint_row.id;
 IF NOT EXISTS(SELECT 1 FROM privacy_protected.restore_replay_checkpoints WHERE run_id=p_run_id AND status<>'SUCCEEDED') THEN
  UPDATE privacy_protected.restore_replay_runs SET status='SUCCEEDED',completed_at=clock_timestamp() WHERE id=p_run_id;
 END IF;
 RETURN checkpoint_row.id;
END; $$;

CREATE OR REPLACE FUNCTION public.privacy_worker_execute_checkpoint(
 p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_lease_epoch bigint,p_worker_ref uuid,p_operation_code text,p_action_version text
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE execution_ref uuid;
BEGIN
 SELECT job.execution_id INTO execution_ref FROM privacy_erasure_category_jobs job
 JOIN privacy_erasure_job_leases lease ON lease.id=p_lease_id AND lease.job_id=job.id AND lease.epoch=p_lease_epoch
 JOIN privacy_erasure_job_attempts attempt ON attempt.id=p_attempt_id AND attempt.job_id=job.id AND attempt.lease_id=lease.id AND attempt.lease_epoch=p_lease_epoch
 WHERE job.id=p_job_id AND job.status='LEASED' AND lease.worker_ref=p_worker_ref AND lease.released_at IS NULL
  AND lease.expires_at>clock_timestamp() AND attempt.finished_at IS NULL FOR UPDATE OF job,lease,attempt;
 IF NOT FOUND THEN RETURN public.privacy_worker_execute_checkpoint_without_tombstone_guard(p_job_id,p_lease_id,p_attempt_id,p_lease_epoch,p_worker_ref,p_operation_code,p_action_version); END IF;
 IF p_operation_code<>'BACKUP_TOMBSTONE_REPLAY' AND NOT EXISTS(
  SELECT 1 FROM privacy_protected.restore_tombstone_receipts receipt WHERE receipt.execution_id=execution_ref AND receipt.ledger_version='restore-tombstone/v2')
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_tombstone_required'; END IF;
 RETURN public.privacy_worker_execute_checkpoint_without_tombstone_guard(p_job_id,p_lease_id,p_attempt_id,p_lease_epoch,p_worker_ref,p_operation_code,p_action_version);
END; $$;

REVOKE ALL ON TABLE privacy_protected.restore_ledger_imports,privacy_protected.restore_replay_runs,privacy_protected.restore_replay_checkpoints FROM PUBLIC;
REVOKE ALL ON FUNCTION public.privacy_relational_replay_operation_supported(text),
 public.privacy_tombstone_prepare_v2(uuid,uuid,uuid,bigint,uuid),
 public.privacy_tombstone_confirm_v2(uuid,uuid,uuid,bigint,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz),
 public.privacy_tombstone_prepare_closure_v2(uuid,uuid),
 public.privacy_tombstone_confirm_closure_v2(uuid,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz),
 public.privacy_restore_import_authenticated_v2(uuid,text,text,text,text,text,bytea,bytea,text,timestamptz,timestamptz,timestamptz,uuid,uuid,uuid,uuid,bytea,bytea,timestamptz,text,text,text[],bytea,bytea),
 public.privacy_restore_begin_replay(uuid,uuid),
 public.privacy_restore_apply_relational_operation(uuid,uuid,timestamptz,text),
 public.privacy_restore_execute_checkpoint(uuid,uuid,smallint,text,text,bytea) FROM PUBLIC;

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
-- #247: complete the approved consent, audit and repair-object retention
-- clocks and expose one bounded, least-privilege maintenance API. This remains
-- inactive until a separate login is granted the NOLOGIN maintenance role and
-- the host timer is explicitly enabled.

ALTER TABLE consent_forms
 ADD COLUMN ceased_at timestamptz NULL,
 ADD COLUMN cessation_reason varchar(40) NULL,
 ADD COLUMN evidence_expires_at timestamptz NULL,
 ADD CONSTRAINT consent_cessation_complete CHECK (
  (ceased_at IS NULL AND cessation_reason IS NULL AND evidence_expires_at IS NULL)
  OR (ceased_at IS NOT NULL AND ceased_at>=date_signed
      AND cessation_reason IN ('SUPERSEDED','WITHDRAWN','PROCESSING_ENDED','ACCOUNT_ERASURE')
      AND evidence_expires_at=ceased_at+interval '3 years')
 ) NOT VALID;
ALTER TABLE consent_forms VALIDATE CONSTRAINT consent_cessation_complete;
CREATE INDEX consent_forms_evidence_expiry_idx ON consent_forms(evidence_expires_at,id) WHERE evidence_expires_at IS NOT NULL;

CREATE FUNCTION privacy_consent_cessation_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.ceased_at IS NOT NULL AND ROW(NEW.ceased_at,NEW.cessation_reason,NEW.evidence_expires_at)
    IS DISTINCT FROM ROW(OLD.ceased_at,OLD.cessation_reason,OLD.evidence_expires_at) THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='consent_cessation_is_immutable';
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER consent_forms_cessation_immutable BEFORE UPDATE ON consent_forms
 FOR EACH ROW EXECUTE FUNCTION privacy_consent_cessation_immutable();

CREATE FUNCTION privacy_consent_cease(
 p_user_id uuid,p_consent_type text,p_except_id uuid,p_reason text,p_ceased_at timestamptz
) RETURNS integer LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE changed integer;
BEGIN
 IF p_user_id IS NULL OR p_consent_type NOT IN ('Termos_Gerais','Uso_Imagem','Responsabilidade_Menor','Dados_Saude','Foto_Perfil')
    OR p_reason NOT IN ('SUPERSEDED','WITHDRAWN','PROCESSING_ENDED','ACCOUNT_ERASURE')
    OR p_ceased_at IS NULL OR p_ceased_at>clock_timestamp()+interval '1 minute' THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='consent_cessation_rejected';
 END IF;
 UPDATE consent_forms SET ceased_at=p_ceased_at,cessation_reason=p_reason,evidence_expires_at=p_ceased_at+interval '3 years'
 WHERE user_id=p_user_id AND consent_type::text=p_consent_type AND ceased_at IS NULL
   AND id IS DISTINCT FROM p_except_id AND date_signed<=p_ceased_at;
 GET DIAGNOSTICS changed=ROW_COUNT;
 RETURN changed;
END; $$;
REVOKE ALL ON FUNCTION privacy_consent_cease(uuid,text,uuid,text,timestamptz) FROM PUBLIC;

CREATE FUNCTION privacy_consent_cease_on_erasure() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
 IF OLD.erased_at IS NULL AND NEW.erased_at IS NOT NULL THEN
  PERFORM privacy_consent_cease(NEW.id,kind::text,NULL,'ACCOUNT_ERASURE',NEW.erased_at)
  FROM (SELECT DISTINCT consent_type AS kind FROM consent_forms WHERE user_id=NEW.id AND ceased_at IS NULL) active;
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER users_consent_cessation AFTER UPDATE OF erased_at ON users
 FOR EACH ROW EXECUTE FUNCTION privacy_consent_cease_on_erasure();
CREATE FUNCTION privacy_profile_photo_requires_active_consent() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.photo_consent_form_id IS NOT NULL AND NOT EXISTS(
  SELECT 1 FROM consent_forms consent WHERE consent.id=NEW.photo_consent_form_id
    AND consent.user_id=NEW.user_id AND consent.consent_type='Foto_Perfil' AND consent.ceased_at IS NULL
 ) THEN
  RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='profile_photo_active_consent_required';
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER member_profiles_photo_active_consent BEFORE INSERT OR UPDATE OF user_id,photo_consent_form_id ON member_profiles
 FOR EACH ROW EXECUTE FUNCTION privacy_profile_photo_requires_active_consent();
REVOKE ALL ON FUNCTION privacy_consent_cessation_immutable(),privacy_consent_cease_on_erasure(),privacy_profile_photo_requires_active_consent() FROM PUBLIC;

-- Repair photos are still useful during active triage, but the accepted
-- technical-retention ceiling is 30 days from upload/report creation. Queue at
-- day 23 to leave a full seven-day verification window. Legacy pointers are
-- never guessed; monitoring reports them as a release-blocking backlog.
CREATE FUNCTION privacy_retention_queue_repair_attachments(p_batch_limit integer)
RETURNS integer LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE item record; latest_status text; latest_sequence integer; changed integer:=0; v_now timestamptz:=clock_timestamp();
BEGIN
 IF p_batch_limit NOT BETWEEN 1 AND 10000 THEN RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy retention input rejected'; END IF;
 FOR item IN
  SELECT repair.id,repair.image_upload_intent_id
  FROM repair_requests repair
  WHERE repair.date_reported<=v_now-interval '23 days' AND repair.image_object_key IS NOT NULL
    AND repair.image_upload_intent_id IS NOT NULL
  ORDER BY repair.date_reported,repair.id FOR UPDATE SKIP LOCKED LIMIT p_batch_limit
 LOOP
  PERFORM privacy_upload_source_lock('REPAIR_ATTACHMENT',item.id);
  SELECT status,sequence INTO latest_status,latest_sequence
  FROM privacy_protected.object_upload_intent_events WHERE intent_id=item.image_upload_intent_id
  ORDER BY sequence DESC LIMIT 1 FOR UPDATE;
  IF latest_status='ATTACHED' THEN
   INSERT INTO privacy_protected.object_upload_intent_events(intent_id,sequence,status,reason_code,occurred_at)
   VALUES(item.image_upload_intent_id,latest_sequence+1,'CLEANUP_REQUIRED','RETENTION_EXPIRED',v_now);
   INSERT INTO privacy_protected.object_upload_cleanup_jobs(intent_id,status,next_attempt_at,updated_at)
   VALUES(item.image_upload_intent_id,'PENDING',v_now,v_now) ON CONFLICT(intent_id) DO NOTHING;
   UPDATE repair_requests SET image_object_key=NULL,image_content_type=NULL,image_size_bytes=NULL,image_upload_intent_id=NULL,updated_at=v_now
   WHERE id=item.id AND image_upload_intent_id=item.image_upload_intent_id;
   IF FOUND THEN changed:=changed+1; END IF;
  ELSIF latest_status NOT IN ('CLEANUP_REQUIRED','ABSENCE_VERIFIED') THEN
   RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_repair_retention_state_invalid';
  END IF;
 END LOOP;
 RETURN changed;
END; $$;
REVOKE ALL ON FUNCTION privacy_retention_queue_repair_attachments(integer) FROM PUBLIC;

-- Pseudonymise only audit rows whose own 24-month clock has elapsed. Newer
-- rows for the same person remain accountable until their independent clock.
CREATE FUNCTION privacy_retention_pseudonymize_audit(p_batch_limit integer)
RETURNS integer LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE audit_row record; subject_ref uuid; subject_refs uuid[]; principal_ref uuid;
 subject_name text; subject_email text; subject_login text; subject_erased timestamptz;
 changed integer:=0; cutoff timestamptz:=clock_timestamp()-interval '24 months';
BEGIN
 IF p_batch_limit NOT BETWEEN 1 AND 10000 THEN RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy retention input rejected'; END IF;
 FOR audit_row IN
  SELECT kind,id,occurred_at FROM (
   SELECT 'MINOR'::text kind,id,created_at occurred_at FROM minor_credential_audit WHERE created_at<=cutoff AND (minor_user_id IS NOT NULL OR guardian_user_id IS NOT NULL OR actor_user_id IS NOT NULL)
   UNION ALL SELECT 'EQUIPMENT',id,occurred_at FROM equipment_audit_events WHERE occurred_at<=cutoff AND actor_user_id IS NOT NULL
   UNION ALL SELECT 'PROFILE',id,occurred_at FROM member_profile_audit_events WHERE occurred_at<=cutoff AND (actor_user_id IS NOT NULL OR subject_user_id IS NOT NULL)
   UNION ALL SELECT 'STAFF',id,occurred_at FROM staff_grant_audit_events WHERE occurred_at<=cutoff AND actor_user_id IS NOT NULL
   UNION ALL SELECT 'ALBUM',id,occurred_at FROM photo_album_audit_events WHERE occurred_at<=cutoff AND actor_user_id IS NOT NULL
   UNION ALL SELECT 'ANNOUNCEMENT',id,occurred_at FROM announcement_audit_events WHERE occurred_at<=cutoff AND actor_user_id IS NOT NULL
   UNION ALL SELECT 'FEATURE',id,occurred_at FROM feature_flag_events WHERE occurred_at<=cutoff AND actor_user_id IS NOT NULL
   UNION ALL SELECT 'TRAINING_COPY',id,copied_at FROM training_copy_events WHERE copied_at<=cutoff AND copied_by_id IS NOT NULL
  ) due ORDER BY occurred_at,kind,id LIMIT p_batch_limit
 LOOP
  CASE audit_row.kind
  WHEN 'MINOR' THEN SELECT ARRAY(SELECT DISTINCT x FROM unnest(ARRAY[minor_user_id,guardian_user_id,actor_user_id]) x WHERE x IS NOT NULL) INTO subject_refs FROM minor_credential_audit WHERE id=audit_row.id FOR UPDATE;
  WHEN 'PROFILE' THEN SELECT ARRAY(SELECT DISTINCT x FROM unnest(ARRAY[actor_user_id,subject_user_id]) x WHERE x IS NOT NULL) INTO subject_refs FROM member_profile_audit_events WHERE id=audit_row.id FOR UPDATE;
  WHEN 'EQUIPMENT' THEN SELECT ARRAY[actor_user_id] INTO subject_refs FROM equipment_audit_events WHERE id=audit_row.id FOR UPDATE;
  WHEN 'STAFF' THEN SELECT ARRAY[actor_user_id] INTO subject_refs FROM staff_grant_audit_events WHERE id=audit_row.id FOR UPDATE;
  WHEN 'ALBUM' THEN SELECT ARRAY[actor_user_id] INTO subject_refs FROM photo_album_audit_events WHERE id=audit_row.id FOR UPDATE;
  WHEN 'ANNOUNCEMENT' THEN SELECT ARRAY[actor_user_id] INTO subject_refs FROM announcement_audit_events WHERE id=audit_row.id FOR UPDATE;
  WHEN 'FEATURE' THEN SELECT ARRAY[actor_user_id] INTO subject_refs FROM feature_flag_events WHERE id=audit_row.id FOR UPDATE;
  WHEN 'TRAINING_COPY' THEN SELECT ARRAY[copied_by_id] INTO subject_refs FROM training_copy_events WHERE id=audit_row.id FOR UPDATE;
  END CASE;
  FOREACH subject_ref IN ARRAY subject_refs LOOP
   SELECT name,email::text,minor_login_id::text,erased_at INTO subject_name,subject_email,subject_login,subject_erased FROM users WHERE id=subject_ref;
   IF NOT FOUND OR (subject_erased IS NOT NULL AND audit_row.kind IN ('EQUIPMENT','STAFF')) THEN
    RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_audit_identity_unavailable';
   END IF;
   INSERT INTO privacy_pseudonymous_principals(purpose) VALUES('AUDIT') RETURNING id INTO principal_ref;
   PERFORM set_config('mycfc.privacy_erasure_operation','AUDIT_ACTOR_ANONYMIZE',true);
   CASE audit_row.kind
   WHEN 'MINOR' THEN UPDATE minor_credential_audit SET
     minor_principal_id=CASE WHEN minor_user_id=subject_ref THEN principal_ref ELSE minor_principal_id END,
     minor_user_id=CASE WHEN minor_user_id=subject_ref THEN NULL ELSE minor_user_id END,
     guardian_principal_id=CASE WHEN guardian_user_id=subject_ref THEN principal_ref ELSE guardian_principal_id END,
     guardian_user_id=CASE WHEN guardian_user_id=subject_ref THEN NULL ELSE guardian_user_id END,
     actor_principal_id=CASE WHEN actor_user_id=subject_ref THEN principal_ref ELSE actor_principal_id END,
     actor_user_id=CASE WHEN actor_user_id=subject_ref THEN NULL ELSE actor_user_id END,
     issued_login_id=CASE WHEN minor_user_id=subject_ref THEN 'anonymised' ELSE privacy_scrub_audit_text(issued_login_id::text,subject_ref,subject_name,subject_email,subject_login) END
    WHERE id=audit_row.id;
   WHEN 'EQUIPMENT' THEN UPDATE equipment_audit_events SET actor_user_id=NULL,actor_principal_id=principal_ref,
     before_state=privacy_scrub_audit_json(before_state,subject_ref,subject_name,subject_email,subject_login),
     after_state=privacy_scrub_audit_json(after_state,subject_ref,subject_name,subject_email,subject_login) WHERE id=audit_row.id AND actor_user_id=subject_ref;
   WHEN 'PROFILE' THEN UPDATE member_profile_audit_events SET
     actor_principal_id=CASE WHEN actor_user_id=subject_ref THEN principal_ref ELSE actor_principal_id END,
     actor_user_id=CASE WHEN actor_user_id=subject_ref THEN NULL ELSE actor_user_id END,
     subject_principal_id=CASE WHEN subject_user_id=subject_ref THEN principal_ref ELSE subject_principal_id END,
     subject_user_id=CASE WHEN subject_user_id=subject_ref THEN NULL ELSE subject_user_id END WHERE id=audit_row.id;
   WHEN 'STAFF' THEN UPDATE staff_grant_audit_events SET actor_user_id=NULL,actor_principal_id=principal_ref,
     reason=privacy_scrub_audit_text(reason,subject_ref,subject_name,subject_email,subject_login) WHERE id=audit_row.id AND actor_user_id=subject_ref;
   WHEN 'ALBUM' THEN UPDATE photo_album_audit_events SET actor_user_id=NULL,actor_principal_id=principal_ref WHERE id=audit_row.id AND actor_user_id=subject_ref;
   WHEN 'ANNOUNCEMENT' THEN UPDATE announcement_audit_events SET actor_user_id=NULL,actor_principal_id=principal_ref WHERE id=audit_row.id AND actor_user_id=subject_ref;
   WHEN 'FEATURE' THEN UPDATE feature_flag_events SET actor_user_id=NULL,actor_principal_id=principal_ref WHERE id=audit_row.id AND actor_user_id=subject_ref;
   WHEN 'TRAINING_COPY' THEN UPDATE training_copy_events SET copied_by_id=NULL,copied_by_principal_id=principal_ref WHERE id=audit_row.id AND copied_by_id=subject_ref;
   END CASE;
  END LOOP;
  changed:=changed+1;
 END LOOP;
 RETURN changed;
END; $$;
REVOKE ALL ON FUNCTION privacy_retention_pseudonymize_audit(integer) FROM PUBLIC;

ALTER TABLE privacy_retention_runs
 ADD COLUMN consent_evidence_deleted integer NOT NULL DEFAULT 0 CHECK(consent_evidence_deleted>=0),
 ADD COLUMN audit_events_pseudonymized integer NOT NULL DEFAULT 0 CHECK(audit_events_pseudonymized>=0),
 ADD COLUMN repair_attachments_queued integer NOT NULL DEFAULT 0 CHECK(repair_attachments_queued>=0);

DROP FUNCTION privacy_retention_run(uuid,integer);
CREATE FUNCTION privacy_retention_run(p_worker_ref uuid,p_batch_limit integer)
RETURNS TABLE(
 run_id uuid,sessions_deleted integer,tokens_deleted integer,outbox_stopped integer,
 outbox_payloads_deleted integer,outbox_evidence_deleted integer,consent_network_scrubbed integer,
 consent_evidence_deleted integer,audit_events_pseudonymized integer,repair_attachments_queued integer,
 event_responses_deleted integer,announcement_deliveries_deleted integer,suggestions_deleted integer,
 privacy_working_scrubbed integer,auth_limits_deleted integer
) LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE v_now timestamptz:=clock_timestamp(); v_started timestamptz:=v_now;
BEGIN
 IF p_worker_ref IS NULL OR p_batch_limit NOT BETWEEN 1 AND 10000 THEN RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy retention input rejected'; END IF;
 IF NOT pg_try_advisory_xact_lock(247,247) THEN RAISE EXCEPTION USING ERRCODE='55P03',MESSAGE='privacy retention already running'; END IF;
 run_id:=gen_random_uuid(); sessions_deleted:=0;tokens_deleted:=0;outbox_stopped:=0;outbox_payloads_deleted:=0;outbox_evidence_deleted:=0;
 consent_network_scrubbed:=0;consent_evidence_deleted:=0;audit_events_pseudonymized:=0;repair_attachments_queued:=0;
 event_responses_deleted:=0;announcement_deliveries_deleted:=0;suggestions_deleted:=0;privacy_working_scrubbed:=0;auth_limits_deleted:=0;

 WITH candidate AS(SELECT token FROM sessions WHERE expiry<=v_now ORDER BY expiry,token FOR UPDATE SKIP LOCKED LIMIT p_batch_limit)
 DELETE FROM sessions row USING candidate WHERE row.token=candidate.token;GET DIAGNOSTICS sessions_deleted=ROW_COUNT;
 WITH candidate AS(SELECT id FROM email_outbox WHERE created_at<=v_now-interval '7 days' AND status IN('PENDING','SENDING') ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT p_batch_limit)
 UPDATE email_outbox row SET status='FAILED',claimed_at=NULL,last_error=NULL,updated_at=v_now FROM candidate WHERE row.id=candidate.id;GET DIAGNOSTICS outbox_stopped=ROW_COUNT;
 WITH candidate AS MATERIALIZED(SELECT id FROM email_outbox WHERE created_at<=v_now-interval '30 days' ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT p_batch_limit),
 evidence AS(INSERT INTO privacy_outbox_delivery_evidence(outbox_id,message_type,final_status,attempts,created_at,terminal_at,expires_at)
  SELECT row.id,row.message_type,CASE WHEN row.status IN('SENT','FAILED','CANCELLED') THEN row.status ELSE 'FAILED' END,row.attempts,row.created_at,COALESCE(row.sent_at,row.updated_at,row.created_at),row.created_at+interval '90 days'
  FROM email_outbox row JOIN candidate USING(id) ON CONFLICT(outbox_id) DO NOTHING RETURNING outbox_id)
 DELETE FROM email_outbox row USING candidate WHERE row.id=candidate.id;GET DIAGNOSTICS outbox_payloads_deleted=ROW_COUNT;
 WITH candidate AS(SELECT outbox_id FROM privacy_outbox_delivery_evidence WHERE expires_at<=v_now ORDER BY expires_at,outbox_id FOR UPDATE SKIP LOCKED LIMIT p_batch_limit)
 DELETE FROM privacy_outbox_delivery_evidence row USING candidate WHERE row.outbox_id=candidate.outbox_id;GET DIAGNOSTICS outbox_evidence_deleted=ROW_COUNT;
 WITH verification AS(SELECT id FROM email_verification_tokens WHERE GREATEST(expires_at,COALESCE(consumed_at,expires_at))<=v_now-interval '30 days' ORDER BY GREATEST(expires_at,COALESCE(consumed_at,expires_at)),id FOR UPDATE SKIP LOCKED LIMIT p_batch_limit),
 dv AS(DELETE FROM email_verification_tokens row USING verification WHERE row.id=verification.id RETURNING 1),
 reset AS(SELECT id FROM password_reset_tokens WHERE GREATEST(expires_at,COALESCE(consumed_at,expires_at))<=v_now-interval '30 days' ORDER BY GREATEST(expires_at,COALESCE(consumed_at,expires_at)),id FOR UPDATE SKIP LOCKED LIMIT p_batch_limit),
 dr AS(DELETE FROM password_reset_tokens row USING reset WHERE row.id=reset.id RETURNING 1)
 SELECT (SELECT count(*) FROM dv)+(SELECT count(*) FROM dr) INTO tokens_deleted;
 WITH candidate AS(SELECT id FROM consent_forms WHERE date_signed<=v_now-interval '12 months' AND(ip_address IS NOT NULL OR user_agent<>'') ORDER BY date_signed,id FOR UPDATE SKIP LOCKED LIMIT p_batch_limit)
 UPDATE consent_forms row SET ip_address=NULL,user_agent='' FROM candidate WHERE row.id=candidate.id;GET DIAGNOSTICS consent_network_scrubbed=ROW_COUNT;
 WITH candidate AS(SELECT id FROM consent_forms WHERE evidence_expires_at<=v_now AND NOT EXISTS(SELECT 1 FROM member_profiles p WHERE p.photo_consent_form_id=consent_forms.id) ORDER BY evidence_expires_at,id FOR UPDATE SKIP LOCKED LIMIT p_batch_limit)
 DELETE FROM consent_forms row USING candidate WHERE row.id=candidate.id;GET DIAGNOSTICS consent_evidence_deleted=ROW_COUNT;
 audit_events_pseudonymized:=privacy_retention_pseudonymize_audit(p_batch_limit);
 repair_attachments_queued:=privacy_retention_queue_repair_attachments(p_batch_limit);
 WITH candidate AS(SELECT response.event_id,response.user_id FROM event_responses response JOIN events event ON event.id=response.event_id WHERE event.ends_at<=v_now-interval '90 days' ORDER BY event.ends_at,response.event_id,response.user_id FOR UPDATE OF response SKIP LOCKED LIMIT p_batch_limit)
 DELETE FROM event_responses row USING candidate WHERE row.event_id=candidate.event_id AND row.user_id=candidate.user_id;GET DIAGNOSTICS event_responses_deleted=ROW_COUNT;
 WITH candidate AS(SELECT announcement_id,user_id FROM announcement_deliveries WHERE delivered_at<=v_now-interval '90 days' ORDER BY delivered_at,announcement_id,user_id FOR UPDATE SKIP LOCKED LIMIT p_batch_limit)
 DELETE FROM announcement_deliveries row USING candidate WHERE row.announcement_id=candidate.announcement_id AND row.user_id=candidate.user_id;GET DIAGNOSTICS announcement_deliveries_deleted=ROW_COUNT;
 WITH candidate AS(SELECT id FROM suggestions WHERE status IN('DECLINED','COMPLETED') AND responded_at<=v_now-interval '12 months' ORDER BY responded_at,id FOR UPDATE SKIP LOCKED LIMIT p_batch_limit)
 DELETE FROM suggestions row USING candidate WHERE row.id=candidate.id;GET DIAGNOSTICS suggestions_deleted=ROW_COUNT;
 WITH candidate AS(SELECT id FROM data_erasure_requests WHERE status IN('REFUSED','CANCELLED','COMPLETED') AND working_expires_at<=v_now AND working_erased_at IS NULL ORDER BY working_expires_at,id FOR UPDATE SKIP LOCKED LIMIT p_batch_limit),
 removed_mail AS(DELETE FROM email_outbox row USING candidate WHERE row.privacy_request_id=candidate.id RETURNING row.id),
 removed_dependants AS(DELETE FROM privacy_request_dependant_resolutions row USING candidate WHERE row.request_id=candidate.id RETURNING 1)
 UPDATE data_erasure_requests row SET decision_explanation='',category_decisions='[]',categories='{}',policy_snapshot=NULL,policy_version=NULL,
  identity_verified_at=NULL,identity_verified_by=NULL,identity_method=NULL,representation_verified_at=NULL,representation_verified_by=NULL,representation_method=NULL,
  representation_guardian_id=NULL,representation_relationship_updated_at=NULL,representation_conflict=false,requester_user_id=NULL,subject_user_id=NULL,claimed_by=NULL,decided_by=NULL,working_erased_at=v_now
 FROM candidate WHERE row.id=candidate.id;GET DIAGNOSTICS privacy_working_scrubbed=ROW_COUNT;
 WITH candidate AS(SELECT bucket FROM privacy_request_auth_limits WHERE window_start<v_now-interval '1 day' ORDER BY window_start,bucket FOR UPDATE SKIP LOCKED LIMIT p_batch_limit)
 DELETE FROM privacy_request_auth_limits row USING candidate WHERE row.bucket=candidate.bucket;GET DIAGNOSTICS auth_limits_deleted=ROW_COUNT;
 INSERT INTO privacy_retention_runs(id,worker_ref,started_at,finished_at,batch_limit,sessions_deleted,tokens_deleted,outbox_stopped,outbox_payloads_deleted,outbox_evidence_deleted,
  consent_network_scrubbed,event_responses_deleted,announcement_deliveries_deleted,suggestions_deleted,privacy_working_scrubbed,auth_limits_deleted,
  consent_evidence_deleted,audit_events_pseudonymized,repair_attachments_queued)
 VALUES(run_id,p_worker_ref,v_started,clock_timestamp(),p_batch_limit,sessions_deleted,tokens_deleted,outbox_stopped,outbox_payloads_deleted,outbox_evidence_deleted,
  consent_network_scrubbed,event_responses_deleted,announcement_deliveries_deleted,suggestions_deleted,privacy_working_scrubbed,auth_limits_deleted,
  consent_evidence_deleted,audit_events_pseudonymized,repair_attachments_queued);
 RETURN NEXT;
END; $$;
REVOKE ALL ON FUNCTION privacy_retention_run(uuid,integer) FROM PUBLIC;

CREATE FUNCTION privacy_retention_status() RETURNS TABLE(
 due_count bigint,oldest_due_age_seconds bigint,repair_due_count bigint,repair_overdue_count bigint,
 repair_terminal_failures bigint,repair_legacy_due_count bigint,last_run_age_seconds bigint
) LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 WITH clock AS(SELECT clock_timestamp() now),
 due AS(
  SELECT expiry due_at FROM sessions,clock WHERE expiry<=now
  UNION ALL SELECT created_at+interval '7 days' FROM email_outbox,clock WHERE status IN('PENDING','SENDING') AND created_at+interval '7 days'<=now
  UNION ALL SELECT created_at+interval '30 days' FROM email_outbox,clock WHERE created_at+interval '30 days'<=now
  UNION ALL SELECT expires_at FROM privacy_outbox_delivery_evidence,clock WHERE expires_at<=now
  UNION ALL SELECT GREATEST(expires_at,COALESCE(consumed_at,expires_at))+interval '30 days' FROM email_verification_tokens,clock WHERE GREATEST(expires_at,COALESCE(consumed_at,expires_at))+interval '30 days'<=now
  UNION ALL SELECT GREATEST(expires_at,COALESCE(consumed_at,expires_at))+interval '30 days' FROM password_reset_tokens,clock WHERE GREATEST(expires_at,COALESCE(consumed_at,expires_at))+interval '30 days'<=now
  UNION ALL SELECT date_signed+interval '12 months' FROM consent_forms,clock WHERE (ip_address IS NOT NULL OR user_agent<>'') AND date_signed+interval '12 months'<=now
  UNION ALL SELECT evidence_expires_at FROM consent_forms,clock WHERE evidence_expires_at<=now
  UNION ALL SELECT created_at+interval '24 months' FROM minor_credential_audit,clock WHERE (minor_user_id IS NOT NULL OR guardian_user_id IS NOT NULL OR actor_user_id IS NOT NULL) AND created_at+interval '24 months'<=now
  UNION ALL SELECT occurred_at+interval '24 months' FROM equipment_audit_events,clock WHERE actor_user_id IS NOT NULL AND occurred_at+interval '24 months'<=now
  UNION ALL SELECT occurred_at+interval '24 months' FROM member_profile_audit_events,clock WHERE (actor_user_id IS NOT NULL OR subject_user_id IS NOT NULL) AND occurred_at+interval '24 months'<=now
  UNION ALL SELECT occurred_at+interval '24 months' FROM staff_grant_audit_events,clock WHERE actor_user_id IS NOT NULL AND occurred_at+interval '24 months'<=now
  UNION ALL SELECT occurred_at+interval '24 months' FROM photo_album_audit_events,clock WHERE actor_user_id IS NOT NULL AND occurred_at+interval '24 months'<=now
  UNION ALL SELECT occurred_at+interval '24 months' FROM announcement_audit_events,clock WHERE actor_user_id IS NOT NULL AND occurred_at+interval '24 months'<=now
  UNION ALL SELECT occurred_at+interval '24 months' FROM feature_flag_events,clock WHERE actor_user_id IS NOT NULL AND occurred_at+interval '24 months'<=now
  UNION ALL SELECT copied_at+interval '24 months' FROM training_copy_events,clock WHERE copied_by_id IS NOT NULL AND copied_at+interval '24 months'<=now
  UNION ALL SELECT date_reported+interval '23 days' FROM repair_requests,clock WHERE image_object_key IS NOT NULL AND date_reported+interval '23 days'<=now
  UNION ALL SELECT event.ends_at+interval '90 days' FROM event_responses response JOIN events event ON event.id=response.event_id,clock WHERE event.ends_at+interval '90 days'<=now
  UNION ALL SELECT delivered_at+interval '90 days' FROM announcement_deliveries,clock WHERE delivered_at+interval '90 days'<=now
  UNION ALL SELECT responded_at+interval '12 months' FROM suggestions,clock WHERE status IN('DECLINED','COMPLETED') AND responded_at+interval '12 months'<=now
  UNION ALL SELECT working_expires_at FROM data_erasure_requests,clock WHERE status IN('REFUSED','CANCELLED','COMPLETED') AND working_erased_at IS NULL AND working_expires_at<=now
  UNION ALL SELECT window_start+interval '1 day' FROM privacy_request_auth_limits,clock WHERE window_start+interval '1 day'<=now
 ), repair AS(
  SELECT i.id,r.date_reported,latest.status,job.status job_status
  FROM privacy_protected.object_upload_intents i JOIN repair_requests r ON r.id=i.source_ref AND i.source_kind='REPAIR_ATTACHMENT'
  JOIN LATERAL(SELECT e.status FROM privacy_protected.object_upload_intent_events e WHERE e.intent_id=i.id ORDER BY e.sequence DESC LIMIT 1) latest ON true
  LEFT JOIN privacy_protected.object_upload_cleanup_jobs job ON job.intent_id=i.id
 )
 SELECT (SELECT count(*) FROM due),
  COALESCE((SELECT floor(extract(epoch FROM((SELECT now FROM clock)-min(due.due_at))))::bigint FROM due),0),
  (SELECT count(*) FROM repair,clock WHERE date_reported+interval '23 days'<=clock.now AND status<>'ABSENCE_VERIFIED'),
  (SELECT count(*) FROM repair,clock WHERE date_reported+interval '30 days'<=clock.now AND status<>'ABSENCE_VERIFIED'),
  (SELECT count(*) FROM repair WHERE job_status='TERMINAL_FAILED'),
  (SELECT count(*) FROM repair_requests,clock WHERE image_upload_intent_id IS NULL AND image_object_key IS NOT NULL AND date_reported+interval '23 days'<=clock.now),
  COALESCE((SELECT floor(extract(epoch FROM((SELECT now FROM clock)-max(finished_at))))::bigint FROM privacy_retention_runs),-1);
$$;
REVOKE ALL ON FUNCTION privacy_retention_status() FROM PUBLIC;

ALTER TABLE privacy_protected.object_upload_intent_events DROP CONSTRAINT object_upload_intent_events_reason_code_check;
ALTER TABLE privacy_protected.object_upload_intent_events ADD CONSTRAINT object_upload_intent_events_reason_code_check CHECK(reason_code IN(
 'UPLOAD_RESERVED','PUT_ACKNOWLEDGED','POINTER_ATTACHED','PUT_AMBIGUOUS','PUT_FAILED','ATTACH_FAILED','POINTER_SUPERSEDED','POINTER_REMOVED','STALE_TIMEOUT','RETENTION_EXPIRED','CLEANUP_CONFIRMED'));
ALTER TABLE privacy_protected.object_upload_intent_events DROP CONSTRAINT object_upload_intent_events_check;
ALTER TABLE privacy_protected.object_upload_intent_events ADD CONSTRAINT object_upload_intent_events_check CHECK(
 (status='PREPARED' AND reason_code='UPLOAD_RESERVED') OR(status='PUT_CONFIRMED' AND reason_code='PUT_ACKNOWLEDGED') OR(status='ATTACHED' AND reason_code='POINTER_ATTACHED')
 OR(status='CLEANUP_REQUIRED' AND reason_code IN('PUT_AMBIGUOUS','PUT_FAILED','ATTACH_FAILED','POINTER_SUPERSEDED','POINTER_REMOVED','STALE_TIMEOUT','RETENTION_EXPIRED'))
 OR(status='ABSENCE_VERIFIED' AND reason_code='CLEANUP_CONFIRMED'));

-- #248 completion, exceptional requeue and evidence-bound activation control.
-- This migration is additive and leaves privacy execution disabled. Migration
-- 202609100010 must be applied first in the release sequence.

ALTER TABLE privacy_erasure_category_jobs
 ADD COLUMN manual_attempt_allowance integer NOT NULL DEFAULT 0 CHECK(manual_attempt_allowance BETWEEN 0 AND 10);

CREATE TABLE privacy_protected.completion_notice_targets (
 execution_id uuid PRIMARY KEY REFERENCES public.privacy_erasure_executions(id) ON DELETE RESTRICT,
 sealed_delivery bytea NOT NULL CHECK(octet_length(sealed_delivery) BETWEEN 29 AND 8192),
 captured_at timestamptz NOT NULL
);
CREATE TRIGGER privacy_completion_notice_targets_immutable BEFORE UPDATE OR DELETE
 ON privacy_protected.completion_notice_targets FOR EACH ROW EXECUTE FUNCTION public.prevent_privacy_execution_record_delete();

CREATE TABLE privacy_erasure_completion_manifests (
 execution_id uuid PRIMARY KEY REFERENCES privacy_erasure_executions(id) ON DELETE RESTRICT,
 request_id uuid NOT NULL UNIQUE REFERENCES data_erasure_requests(id) ON DELETE RESTRICT,
 manifest_version varchar(40) NOT NULL CHECK(manifest_version='privacy-completion/v1'),
 plan_sha256 bytea NOT NULL CHECK(octet_length(plan_sha256)=32),
 manifest jsonb NOT NULL CHECK(jsonb_typeof(manifest)='object' AND octet_length(manifest::text)<=262144),
 manifest_sha256 bytea NOT NULL CHECK(octet_length(manifest_sha256)=32),
 category_count integer NOT NULL CHECK(category_count BETWEEN 1 AND 50),
 checkpoint_count integer NOT NULL CHECK(checkpoint_count BETWEEN 1 AND 5000),
 object_target_count integer NOT NULL CHECK(object_target_count>=0),
 provider_target_count integer NOT NULL CHECK(provider_target_count>=0),
 completed_by_ref uuid NOT NULL,completed_at timestamptz NOT NULL,evidence_expires_at timestamptz NOT NULL,
 CHECK(evidence_expires_at=completed_at+interval '24 months'),
 UNIQUE(manifest_version,manifest_sha256)
);
CREATE TRIGGER privacy_erasure_completion_manifests_immutable BEFORE UPDATE OR DELETE
 ON privacy_erasure_completion_manifests FOR EACH ROW EXECUTE FUNCTION prevent_privacy_execution_record_delete();

CREATE TABLE privacy_completion_access_links (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),execution_id uuid NOT NULL UNIQUE REFERENCES privacy_erasure_completion_manifests(execution_id) ON DELETE RESTRICT,
 token_sha256 bytea NOT NULL UNIQUE CHECK(octet_length(token_sha256)=32),
 created_at timestamptz NOT NULL,expires_at timestamptz NOT NULL,used_at timestamptz NULL,
 CHECK(expires_at=created_at+interval '24 hours'),CHECK(used_at IS NULL OR (used_at>=created_at AND used_at<=expires_at))
);
CREATE INDEX privacy_completion_access_links_expiry_idx ON privacy_completion_access_links(expires_at,id) WHERE used_at IS NULL;
CREATE FUNCTION protect_privacy_completion_access_link() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' OR NEW.id<>OLD.id OR NEW.execution_id<>OLD.execution_id OR NEW.token_sha256<>OLD.token_sha256
  OR NEW.created_at<>OLD.created_at OR NEW.expires_at<>OLD.expires_at OR OLD.used_at IS NOT NULL OR NEW.used_at IS NULL THEN
  RAISE EXCEPTION 'privacy completion access link is immutable';
 END IF;
 RETURN NEW;
END;$$;
CREATE TRIGGER privacy_completion_access_links_protected BEFORE UPDATE OR DELETE ON privacy_completion_access_links
 FOR EACH ROW EXECUTE FUNCTION protect_privacy_completion_access_link();

-- Capture only the already-encrypted generic recipient used for the processing
-- notice. The completion worker can recover it without retaining cleartext
-- identity after account erasure.
CREATE FUNCTION privacy_execution_capture_completion_notice(p_execution_id uuid,p_processing_event_id uuid)
RETURNS uuid LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 INSERT INTO privacy_protected.completion_notice_targets(execution_id,sealed_delivery,captured_at)
 SELECT execution.id,outbox.sealed_payload,clock_timestamp()
 FROM privacy_erasure_executions execution
 JOIN email_outbox outbox ON outbox.privacy_request_id=execution.request_id
 WHERE execution.id=p_execution_id AND outbox.privacy_event_key=p_processing_event_id
  AND outbox.message_type='PRIVACY_PROCESSING_STARTED' AND outbox.sealed_payload IS NOT NULL
 ON CONFLICT(execution_id) DO NOTHING RETURNING execution_id;
$$;

CREATE FUNCTION privacy_completion_prepare(p_execution_id uuid,p_worker_ref uuid)
RETURNS TABLE(sealed_delivery bytea) LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT target.sealed_delivery
 FROM privacy_erasure_executions execution
 JOIN data_erasure_requests request ON request.id=execution.request_id
 JOIN privacy_protected.completion_notice_targets target ON target.execution_id=execution.id
 JOIN privacy_protected.restore_tombstone_closure_intents intent ON intent.execution_id=execution.id
 JOIN privacy_protected.restore_tombstone_closure_receipts receipt ON receipt.execution_id=execution.id
 WHERE execution.id=p_execution_id AND p_worker_ref IS NOT NULL AND execution.status='SUCCEEDED'
  AND request.status IN ('PROCESSING','RETRYABLE_FAILED')
  AND receipt.ledger_version='restore-tombstone-closure/v2'
  AND receipt.verified_at>=receipt.written_at AND receipt.verified_at<=intent.evidence_expires_at
  AND NOT EXISTS(SELECT 1 FROM privacy_erasure_completion_manifests manifest WHERE manifest.execution_id=execution.id)
  AND NOT EXISTS(SELECT 1 FROM privacy_erasure_category_jobs job WHERE job.execution_id=execution.id AND job.status<>'SUCCEEDED')
 FOR SHARE OF execution,request,target,intent,receipt;
$$;

CREATE FUNCTION privacy_completion_list_pending(p_worker_ref uuid,p_limit integer)
RETURNS TABLE(execution_id uuid) LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT execution.id
 FROM privacy_erasure_executions execution
 JOIN data_erasure_requests request ON request.id=execution.request_id
 JOIN privacy_protected.completion_notice_targets target ON target.execution_id=execution.id
 WHERE p_worker_ref IS NOT NULL AND p_limit BETWEEN 1 AND 100
  AND execution.status='SUCCEEDED' AND request.status IN ('PROCESSING','RETRYABLE_FAILED')
  AND NOT EXISTS(SELECT 1 FROM privacy_erasure_completion_manifests manifest WHERE manifest.execution_id=execution.id)
  AND NOT EXISTS(SELECT 1 FROM privacy_erasure_category_jobs job WHERE job.execution_id=execution.id AND job.status<>'SUCCEEDED')
 ORDER BY execution.finished_at,execution.id LIMIT p_limit;
$$;

-- The finalizer locks and reconstructs every evidence-bearing work row. It
-- accepts no caller-supplied counts, category outcomes or completion time.
CREATE FUNCTION privacy_completion_finalize(
 p_execution_id uuid,p_worker_ref uuid,p_token_sha256 bytea,p_sealed_delivery bytea
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE execution privacy_erasure_executions%ROWTYPE;request data_erasure_requests%ROWTYPE;plan privacy_request_execution_plans%ROWTYPE;
 intent privacy_protected.restore_tombstone_closure_intents%ROWTYPE;receipt privacy_protected.restore_tombstone_closure_receipts%ROWTYPE;
 category_total integer;checkpoint_total integer;object_total integer;provider_total integer;object_evidence_total integer;provider_evidence_total integer;
 document jsonb;document_digest bytea;event_id uuid;request_version bigint;finalized_at timestamptz;
BEGIN
 IF p_execution_id IS NULL OR p_worker_ref IS NULL OR octet_length(p_token_sha256)<>32 OR octet_length(p_sealed_delivery) NOT BETWEEN 29 AND 8192 THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy_completion_input_rejected'; END IF;
 SELECT * INTO execution FROM privacy_erasure_executions WHERE id=p_execution_id FOR UPDATE;
 IF execution.id IS NULL THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_completion_unavailable'; END IF;
 SELECT * INTO request FROM data_erasure_requests WHERE id=execution.request_id FOR UPDATE;
 SELECT * INTO plan FROM privacy_request_execution_plans WHERE request_id=execution.request_id FOR SHARE;
 SELECT * INTO intent FROM privacy_protected.restore_tombstone_closure_intents WHERE execution_id=execution.id FOR SHARE;
 SELECT * INTO receipt FROM privacy_protected.restore_tombstone_closure_receipts WHERE execution_id=execution.id FOR SHARE;
 PERFORM 1 FROM privacy_erasure_category_jobs WHERE execution_id=execution.id ORDER BY plan_entry_position FOR UPDATE;
 PERFORM 1 FROM privacy_erasure_job_checkpoints checkpoint JOIN privacy_erasure_category_jobs job ON job.id=checkpoint.job_id
  WHERE job.execution_id=execution.id ORDER BY job.plan_entry_position,checkpoint.operation_position FOR UPDATE OF checkpoint;
 PERFORM 1 FROM privacy_protected.object_targets WHERE execution_id=execution.id ORDER BY id FOR SHARE;
 PERFORM 1 FROM privacy_protected.provider_targets WHERE execution_id=execution.id ORDER BY id FOR SHARE;

 IF execution.status<>'SUCCEEDED' OR request.status NOT IN ('PROCESSING','RETRYABLE_FAILED') OR plan.request_id IS NULL
  OR execution.plan_sha256<>plan.plan_sha256 OR execution.executor_version<>plan.executor_version OR execution.schema_version<>plan.schema_version
  OR intent.execution_id IS NULL OR receipt.execution_id IS NULL OR receipt.ledger_version<>'restore-tombstone-closure/v2'
  OR intent.closed_at<execution.finished_at OR receipt.verified_at<intent.closed_at OR receipt.verified_at<receipt.written_at OR receipt.verified_at>intent.evidence_expires_at
  OR NOT EXISTS(SELECT 1 FROM privacy_protected.restore_tombstone_receipts r WHERE r.execution_id=execution.id AND r.ledger_version='restore-tombstone/v2')
  OR NOT EXISTS(SELECT 1 FROM privacy_protected.completion_notice_targets n WHERE n.execution_id=execution.id)
  OR EXISTS(SELECT 1 FROM privacy_erasure_job_leases lease JOIN privacy_erasure_category_jobs job ON job.id=lease.job_id WHERE job.execution_id=execution.id AND lease.released_at IS NULL)
  OR EXISTS(SELECT 1 FROM privacy_erasure_category_jobs job WHERE job.execution_id=execution.id AND job.status<>'SUCCEEDED')
  OR EXISTS(SELECT 1 FROM privacy_erasure_job_checkpoints checkpoint JOIN privacy_erasure_category_jobs job ON job.id=checkpoint.job_id
      WHERE job.execution_id=execution.id AND (checkpoint.status<>'SUCCEEDED' OR checkpoint.affected_rows IS NULL OR checkpoint.result_sha256 IS NULL))
  OR EXISTS(SELECT 1 FROM privacy_erasure_category_jobs job WHERE job.execution_id=execution.id AND
      (job.category_key IS DISTINCT FROM plan.plan->'entries'->(job.plan_entry_position-1)->>'category'
       OR job.purpose_code IS DISTINCT FROM plan.plan->'entries'->(job.plan_entry_position-1)->>'purpose'))
  OR EXISTS(SELECT 1 FROM privacy_erasure_job_checkpoints checkpoint JOIN privacy_erasure_category_jobs job ON job.id=checkpoint.job_id
      WHERE job.execution_id=execution.id AND (checkpoint.operation_code IS DISTINCT FROM plan.plan->'entries'->(job.plan_entry_position-1)->'operations'->>(checkpoint.operation_position-1)
       OR checkpoint.action_version IS DISTINCT FROM plan.plan->'entries'->(job.plan_entry_position-1)->>'action_version'))
  OR (SELECT count(*) FROM privacy_erasure_category_jobs job WHERE job.execution_id=execution.id)<>(SELECT count(*) FROM jsonb_array_elements(plan.plan->'entries'))
  OR EXISTS(SELECT 1 FROM privacy_erasure_category_jobs job WHERE job.execution_id=execution.id AND
       (SELECT count(*) FROM privacy_erasure_job_checkpoints c WHERE c.job_id=job.id)<>(SELECT count(*) FROM jsonb_array_elements(plan.plan->'entries'->(job.plan_entry_position-1)->'operations')))
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_completion_evidence_incomplete'; END IF;

 SELECT count(DISTINCT job.id),count(checkpoint.*) INTO category_total,checkpoint_total FROM privacy_erasure_category_jobs job
 JOIN privacy_erasure_job_checkpoints checkpoint ON checkpoint.job_id=job.id WHERE job.execution_id=execution.id;
 SELECT count(*),(SELECT count(*) FROM privacy_protected.object_evidence evidence JOIN privacy_protected.object_targets target ON target.id=evidence.target_id WHERE target.execution_id=execution.id)
 INTO object_total,object_evidence_total FROM privacy_protected.object_targets WHERE execution_id=execution.id;
 SELECT count(*),(SELECT count(*) FROM privacy_protected.provider_evidence evidence JOIN privacy_protected.provider_targets target ON target.id=evidence.target_id WHERE target.execution_id=execution.id)
 INTO provider_total,provider_evidence_total FROM privacy_protected.provider_targets WHERE execution_id=execution.id;

 IF object_total<>object_evidence_total
  OR EXISTS(SELECT 1 FROM privacy_protected.object_capture_sets capture WHERE capture.execution_id=execution.id AND
      (capture.expected_target_count<>(SELECT count(*) FROM privacy_protected.object_targets target WHERE target.checkpoint_id=capture.checkpoint_id)
       OR capture.expected_target_count<>(SELECT count(*) FROM privacy_protected.object_evidence evidence JOIN privacy_protected.object_targets target ON target.id=evidence.target_id WHERE target.checkpoint_id=capture.checkpoint_id)
       OR EXISTS(SELECT 1 FROM privacy_protected.object_targets target WHERE target.checkpoint_id=capture.checkpoint_id
          AND NOT EXISTS(SELECT 1 FROM privacy_protected.object_target_digests digest_row WHERE digest_row.target_id=target.id))))
  OR EXISTS(SELECT 1 FROM privacy_erasure_job_checkpoints checkpoint JOIN privacy_erasure_category_jobs job ON job.id=checkpoint.job_id
      WHERE job.execution_id=execution.id AND checkpoint.operation_code='OBJECT_VERSION_DELETE'
       AND NOT EXISTS(SELECT 1 FROM privacy_protected.object_capture_sets capture WHERE capture.checkpoint_id=checkpoint.id))
  OR provider_total<>provider_evidence_total
  OR EXISTS(SELECT 1 FROM privacy_protected.provider_capture_sets capture WHERE capture.execution_id=execution.id AND
      (capture.expected_target_count<>(SELECT count(*) FROM privacy_protected.provider_targets target WHERE target.checkpoint_id=capture.checkpoint_id)
       OR capture.expected_target_count<>(SELECT count(*) FROM privacy_protected.provider_evidence evidence JOIN privacy_protected.provider_targets target ON target.id=evidence.target_id WHERE target.checkpoint_id=capture.checkpoint_id)
       OR EXISTS(SELECT 1 FROM privacy_protected.provider_targets target WHERE target.checkpoint_id=capture.checkpoint_id
          AND NOT EXISTS(SELECT 1 FROM privacy_protected.provider_target_digests digest_row WHERE digest_row.target_id=target.id))
       OR EXISTS(SELECT 1 FROM privacy_protected.provider_credential_quarantine q JOIN privacy_protected.provider_targets target ON target.id=q.target_id WHERE target.checkpoint_id=capture.checkpoint_id)))
  OR EXISTS(SELECT 1 FROM privacy_erasure_job_checkpoints checkpoint JOIN privacy_erasure_category_jobs job ON job.id=checkpoint.job_id
      WHERE job.execution_id=execution.id AND checkpoint.operation_code='PROVIDER_RECIPIENT_NOTIFY'
       AND NOT EXISTS(SELECT 1 FROM privacy_protected.provider_capture_sets capture WHERE capture.checkpoint_id=checkpoint.id))
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_completion_target_evidence_incomplete'; END IF;

 SELECT jsonb_build_object(
  'version','privacy-completion/v1','execution_id',execution.id,'request_id',request.id,'plan_sha256',encode(execution.plan_sha256,'hex'),
  'work',COALESCE((SELECT jsonb_agg(jsonb_build_object('position',job.plan_entry_position,'entry_sha256',encode(job.entry_sha256,'hex'),
    'category',job.category_key,'purpose',job.purpose_code,'checkpoints',(SELECT jsonb_agg(jsonb_build_object('position',checkpoint.operation_position,
      'operation',checkpoint.operation_code,'action_version',checkpoint.action_version,'affected_rows',checkpoint.affected_rows,
      'result_sha256',encode(checkpoint.result_sha256,'hex')) ORDER BY checkpoint.operation_position) FROM privacy_erasure_job_checkpoints checkpoint WHERE checkpoint.job_id=job.id))
    ORDER BY job.plan_entry_position) FROM privacy_erasure_category_jobs job WHERE job.execution_id=execution.id),'[]'::jsonb),
  'objects',jsonb_build_object('targets',object_total,'evidence',object_evidence_total,'sha256',encode(digest(convert_to(COALESCE((SELECT string_agg(
    target.id::text||':'||digest_row.digest_key_id||':'||encode(digest_row.locator_digest,'hex')||':'||evidence.outcome_code||':'||encode(evidence.transcript_digest,'hex'),
    '|' ORDER BY target.id,digest_row.digest_key_id) FROM privacy_protected.object_targets target JOIN privacy_protected.object_target_digests digest_row ON digest_row.target_id=target.id
    JOIN privacy_protected.object_evidence evidence ON evidence.target_id=target.id WHERE target.execution_id=execution.id),''),'UTF8'),'sha256'),'hex')),
  'providers',jsonb_build_object('targets',provider_total,'evidence',provider_evidence_total,'sha256',encode(digest(convert_to(COALESCE((SELECT string_agg(
    target.id::text||':'||digest_row.digest_key_id||':'||encode(digest_row.target_digest,'hex')||':'||evidence.outcome_code||':'||evidence.evidence_code||':'||encode(evidence.transcript_digest,'hex'),
    '|' ORDER BY target.id,digest_row.digest_key_id) FROM privacy_protected.provider_targets target JOIN privacy_protected.provider_target_digests digest_row ON digest_row.target_id=target.id
    JOIN privacy_protected.provider_evidence evidence ON evidence.target_id=target.id WHERE target.execution_id=execution.id),''),'UTF8'),'sha256'),'hex')),
  'ledger',jsonb_build_object('intent_sha256',encode((SELECT ciphertext_sha256 FROM privacy_protected.restore_tombstone_receipts WHERE execution_id=execution.id),'hex'),
    'closure_sha256',encode(receipt.ciphertext_sha256,'hex'))
 ) INTO document;
 document_digest:=digest(convert_to(document::text,'UTF8'),'sha256');
	finalized_at:=clock_timestamp();

 INSERT INTO privacy_erasure_completion_manifests(execution_id,request_id,manifest_version,plan_sha256,manifest,manifest_sha256,
  category_count,checkpoint_count,object_target_count,provider_target_count,completed_by_ref,completed_at,evidence_expires_at)
 VALUES(execution.id,request.id,'privacy-completion/v1',execution.plan_sha256,document,document_digest,category_total,checkpoint_total,
  object_total,provider_total,p_worker_ref,intent.closed_at,intent.evidence_expires_at);
 INSERT INTO privacy_completion_access_links(execution_id,token_sha256,created_at,expires_at)
 VALUES(execution.id,p_token_sha256,finalized_at,finalized_at+interval '24 hours');
 UPDATE data_erasure_requests SET status='COMPLETED',version=version+1,closed_at=intent.closed_at,evidence_expires_at=intent.evidence_expires_at,
  working_expires_at=intent.closed_at+interval '90 days',updated_at=intent.closed_at WHERE id=request.id RETURNING version INTO request_version;
 INSERT INTO data_erasure_request_events(request_id,actor_role,actor_ref,action,reason_code,from_status,to_status,version,occurred_at)
 VALUES(request.id,'SYSTEM',p_worker_ref,'COMPLETED','EXECUTION_COMPLETED',request.status,'COMPLETED',request_version,intent.closed_at) RETURNING id INTO event_id;
 INSERT INTO email_outbox(message_type,privacy_request_id,privacy_requester_id,privacy_event_key,sealed_payload,next_attempt_at,created_at,updated_at)
 VALUES('PRIVACY_COMPLETED',request.id,request.requester_user_id,event_id,p_sealed_delivery,finalized_at,finalized_at,finalized_at);
 RETURN execution.id;
END;$$;

CREATE FUNCTION privacy_completion_consume(p_token_sha256 bytea)
RETURNS TABLE(request_ref uuid,completed_at timestamptz,summary jsonb) LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE link privacy_completion_access_links%ROWTYPE;
BEGIN
 IF octet_length(p_token_sha256)<>32 THEN RETURN; END IF;
 SELECT * INTO link FROM privacy_completion_access_links WHERE token_sha256=p_token_sha256 FOR UPDATE;
 IF link.id IS NULL OR link.used_at IS NOT NULL OR link.expires_at<=clock_timestamp() THEN RETURN; END IF;
 UPDATE privacy_completion_access_links SET used_at=clock_timestamp() WHERE id=link.id;
 RETURN QUERY SELECT request.public_ref,manifest.completed_at,
  jsonb_build_object('status','COMPLETED','manifest_sha256',encode(manifest.manifest_sha256,'hex'),
   'categories',manifest.category_count,'checkpoints',manifest.checkpoint_count,
   'object_targets',manifest.object_target_count,'provider_targets',manifest.provider_target_count)
 FROM privacy_erasure_completion_manifests manifest JOIN data_erasure_requests request ON request.id=manifest.request_id
 WHERE manifest.execution_id=link.execution_id;
END;$$;

CREATE FUNCTION privacy_completion_validate(p_token_sha256 bytea)
RETURNS boolean LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT octet_length(p_token_sha256)=32 AND EXISTS(
  SELECT 1 FROM privacy_completion_access_links link
  WHERE link.token_sha256=p_token_sha256 AND link.used_at IS NULL AND link.expires_at>clock_timestamp()
 );
$$;

CREATE FUNCTION privacy_completion_notice_deliverable(p_request_id uuid,p_at timestamptz)
RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT EXISTS(SELECT 1 FROM privacy_erasure_completion_manifests manifest
  JOIN privacy_completion_access_links link ON link.execution_id=manifest.execution_id
  WHERE manifest.request_id=p_request_id AND link.used_at IS NULL AND link.expires_at>p_at);
$$;

CREATE TABLE privacy_terminal_requeue_proposals (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),job_id uuid NOT NULL REFERENCES privacy_erasure_category_jobs(id) ON DELETE RESTRICT,
 execution_id uuid NOT NULL REFERENCES privacy_erasure_executions(id) ON DELETE RESTRICT,failure_id uuid NOT NULL UNIQUE REFERENCES privacy_erasure_failures(id) ON DELETE RESTRICT,
 expected_execution_version bigint NOT NULL CHECK(expected_execution_version>0),expected_lease_epoch bigint NOT NULL CHECK(expected_lease_epoch>0),
 proposal_sha256 bytea NOT NULL UNIQUE CHECK(octet_length(proposal_sha256)=32),proposed_by_ref uuid NOT NULL,proposed_at timestamptz NOT NULL
);
CREATE TABLE privacy_terminal_requeue_approvals (
 proposal_id uuid PRIMARY KEY REFERENCES privacy_terminal_requeue_proposals(id) ON DELETE RESTRICT,
 proposal_sha256 bytea NOT NULL CHECK(octet_length(proposal_sha256)=32),approved_by_ref uuid NOT NULL,approved_at timestamptz NOT NULL,
 CHECK(octet_length(proposal_sha256)=32)
);
CREATE TRIGGER privacy_terminal_requeue_proposals_immutable BEFORE UPDATE OR DELETE ON privacy_terminal_requeue_proposals
 FOR EACH ROW EXECUTE FUNCTION prevent_privacy_execution_record_delete();
CREATE TRIGGER privacy_terminal_requeue_approvals_immutable BEFORE UPDATE OR DELETE ON privacy_terminal_requeue_approvals
 FOR EACH ROW EXECUTE FUNCTION prevent_privacy_execution_record_delete();

CREATE FUNCTION privacy_terminal_requeue_propose(p_job_id uuid,p_actor uuid)
RETURNS TABLE(proposal_id uuid,proposal_sha256 bytea) LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE job privacy_erasure_category_jobs%ROWTYPE;execution privacy_erasure_executions%ROWTYPE;failure privacy_erasure_failures%ROWTYPE;digest_value bytea;
BEGIN
 IF NOT EXISTS(SELECT 1 FROM users account JOIN privacy_executor_grants grant_row ON grant_row.user_id=account.id AND grant_row.revoked_at IS NULL
   WHERE account.id=p_actor AND account.is_active AND NOT account.is_dependent) THEN
  RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_requeue_forbidden'; END IF;
 SELECT * INTO job FROM privacy_erasure_category_jobs WHERE id=p_job_id FOR UPDATE;
 SELECT * INTO execution FROM privacy_erasure_executions WHERE id=job.execution_id FOR UPDATE;
 SELECT failure_row.* INTO failure FROM privacy_erasure_failures failure_row WHERE failure_row.job_id=job.id AND failure_row.classification='TERMINAL'
  ORDER BY failure_row.occurred_at DESC,failure_row.id DESC LIMIT 1 FOR SHARE;
 IF job.id IS NULL OR job.status<>'TERMINAL_FAILED' OR execution.status<>'TERMINAL_FAILED' OR failure.id IS NULL OR job.manual_attempt_allowance>=10
  OR EXISTS(SELECT 1 FROM privacy_terminal_requeue_proposals proposal WHERE proposal.failure_id=failure.id) THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_requeue_unavailable'; END IF;
 digest_value:=digest(convert_to('privacy-terminal-requeue/v1:'||job.id::text||':'||execution.id::text||':'||failure.id::text||':'||
  execution.version::text||':'||job.lease_epoch::text||':'||job.attempt_count::text,'UTF8'),'sha256');
 RETURN QUERY INSERT INTO privacy_terminal_requeue_proposals(job_id,execution_id,failure_id,expected_execution_version,expected_lease_epoch,
  proposal_sha256,proposed_by_ref,proposed_at) VALUES(job.id,execution.id,failure.id,execution.version,job.lease_epoch,digest_value,p_actor,clock_timestamp())
 RETURNING id,privacy_terminal_requeue_proposals.proposal_sha256;
END;$$;

CREATE FUNCTION privacy_terminal_requeue_approve(p_proposal_id uuid,p_proposal_sha256 bytea,p_actor uuid)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE proposal privacy_terminal_requeue_proposals%ROWTYPE;job privacy_erasure_category_jobs%ROWTYPE;execution privacy_erasure_executions%ROWTYPE;
 request data_erasure_requests%ROWTYPE;new_version bigint;now_at timestamptz;
BEGIN
 IF NOT EXISTS(SELECT 1 FROM users account JOIN user_platform_roles assignment ON assignment.user_id=account.id JOIN platform_roles role ON role.id=assignment.role_id
   WHERE account.id=p_actor AND account.is_active AND NOT account.is_dependent AND role.code='ADMIN') THEN
  RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_requeue_forbidden'; END IF;
 SELECT * INTO proposal FROM privacy_terminal_requeue_proposals WHERE id=p_proposal_id FOR SHARE;
 IF proposal.id IS NULL OR proposal.proposed_by_ref=p_actor OR proposal.proposal_sha256<>p_proposal_sha256
  OR EXISTS(SELECT 1 FROM privacy_terminal_requeue_approvals approval WHERE approval.proposal_id=proposal.id) THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_requeue_unavailable'; END IF;
 SELECT * INTO job FROM privacy_erasure_category_jobs WHERE id=proposal.job_id FOR UPDATE;
 SELECT * INTO execution FROM privacy_erasure_executions WHERE id=proposal.execution_id FOR UPDATE;
 SELECT * INTO request FROM data_erasure_requests WHERE id=execution.request_id FOR UPDATE;
 IF job.status<>'TERMINAL_FAILED' OR execution.status<>'TERMINAL_FAILED' OR request.status<>'TERMINAL_FAILED'
  OR execution.version<>proposal.expected_execution_version OR job.lease_epoch<>proposal.expected_lease_epoch
  OR NOT EXISTS(SELECT 1 FROM privacy_erasure_failures failure WHERE failure.id=proposal.failure_id AND failure.job_id=job.id AND failure.classification='TERMINAL')
  OR EXISTS(SELECT 1 FROM privacy_erasure_failures later WHERE later.job_id=job.id AND later.occurred_at>(SELECT occurred_at FROM privacy_erasure_failures WHERE id=proposal.failure_id))
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_requeue_stale'; END IF;
 now_at:=clock_timestamp();
 INSERT INTO privacy_terminal_requeue_approvals VALUES(proposal.id,proposal.proposal_sha256,p_actor,now_at);
 UPDATE privacy_erasure_category_jobs SET status='PENDING',next_attempt_at=now_at,updated_at=now_at,completed_at=NULL,
  manual_attempt_allowance=manual_attempt_allowance+1 WHERE id=job.id;
 IF EXISTS(SELECT 1 FROM privacy_erasure_category_jobs sibling WHERE sibling.execution_id=execution.id AND sibling.id<>job.id AND sibling.status='TERMINAL_FAILED') THEN
  RETURN job.id;
 END IF;
 UPDATE privacy_erasure_executions SET status='RUNNING',version=version+1,finished_at=NULL,updated_at=now_at WHERE id=execution.id;
 UPDATE data_erasure_requests SET status='PROCESSING',version=version+1,updated_at=now_at WHERE id=request.id RETURNING version INTO new_version;
 INSERT INTO data_erasure_request_events(request_id,actor_role,actor_ref,action,reason_code,from_status,to_status,version,occurred_at)
 VALUES(request.id,'SYSTEM',p_actor,'EXECUTION_RESUMED','EXECUTION_RETRY_STARTED','TERMINAL_FAILED','PROCESSING',new_version,now_at);
 RETURN job.id;
END;$$;

CREATE FUNCTION privacy_completion_control_snapshot(p_actor uuid,p_request_reference uuid)
RETURNS TABLE(request_reference uuid,request_status text,execution_id uuid,execution_status text,job_id uuid,category_code text,purpose_code text,
 job_status text,attempt_count integer,failure_stage text,failure_code text,proposal_id uuid,proposal_sha256 bytea,proposed_at timestamptz,can_propose boolean,can_approve boolean)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE actor_executor boolean;actor_admin boolean;
BEGIN
 SELECT EXISTS(SELECT 1 FROM users account JOIN privacy_executor_grants grant_row ON grant_row.user_id=account.id AND grant_row.revoked_at IS NULL
   WHERE account.id=p_actor AND account.is_active AND NOT account.is_dependent),
  EXISTS(SELECT 1 FROM users account JOIN user_platform_roles assignment ON assignment.user_id=account.id
   JOIN platform_roles role ON role.id=assignment.role_id WHERE account.id=p_actor AND account.is_active AND NOT account.is_dependent AND role.code='ADMIN')
 INTO actor_executor,actor_admin;
 IF NOT actor_executor AND NOT actor_admin THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_control_forbidden'; END IF;
 RETURN QUERY
 SELECT request.public_ref,request.status::text,execution.id,execution.status::text,job.id,job.category_key::text,job.purpose_code::text,job.status::text,job.attempt_count,
  (CASE WHEN failure.stage_code IN ('EXECUTE','VERIFY') THEN failure.stage_code ELSE NULL END)::text,
  (CASE WHEN failure.failure_code IN ('ACTION_FAILED','DEPENDENCY_UNAVAILABLE','UNSUPPORTED_OPERATION','VERIFICATION_FAILED','RETRY_LIMIT_REACHED') THEN failure.failure_code ELSE NULL END)::text,
  proposal.id,proposal.proposal_sha256,proposal.proposed_at,
  (actor_executor AND request.status='TERMINAL_FAILED' AND execution.status='TERMINAL_FAILED' AND job.status='TERMINAL_FAILED'
   AND job.manual_attempt_allowance<10 AND proposal.id IS NULL),
  (actor_admin AND proposal.id IS NOT NULL AND proposal.proposed_by_ref<>p_actor)
 FROM data_erasure_requests request JOIN privacy_erasure_executions execution ON execution.request_id=request.id
 JOIN privacy_erasure_category_jobs job ON job.execution_id=execution.id
 LEFT JOIN LATERAL(SELECT candidate.stage_code,candidate.failure_code FROM privacy_erasure_failures candidate
  WHERE candidate.job_id=job.id ORDER BY candidate.occurred_at DESC,candidate.id DESC LIMIT 1) failure ON true
 LEFT JOIN LATERAL(SELECT candidate.id,candidate.proposal_sha256,candidate.proposed_at,candidate.proposed_by_ref FROM privacy_terminal_requeue_proposals candidate
  WHERE candidate.job_id=job.id AND NOT EXISTS(SELECT 1 FROM privacy_terminal_requeue_approvals approval WHERE approval.proposal_id=candidate.id)
  ORDER BY candidate.proposed_at DESC,candidate.id DESC LIMIT 1) proposal ON true
 WHERE request.public_ref=p_request_reference ORDER BY job.plan_entry_position LIMIT 50;
END;$$;

CREATE TABLE privacy_activation_evidence (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),kind varchar(20) NOT NULL CHECK(kind IN ('RESTORE','INFRASTRUCTURE','PROVIDER','SCHEMA')),
 evidence_sha256 bytea NOT NULL CHECK(octet_length(evidence_sha256)=32),reference_code varchar(120) NOT NULL,
 observed_at timestamptz NOT NULL,expires_at timestamptz NOT NULL,recorded_by_ref uuid NOT NULL,recorded_at timestamptz NOT NULL,
 CHECK(reference_code=btrim(reference_code) AND reference_code~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,119}$'),
 CHECK(expires_at=observed_at+interval '90 days'),UNIQUE(kind,evidence_sha256)
);
CREATE TABLE privacy_activation_proposals (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),policy_version varchar(80) NOT NULL REFERENCES privacy_request_policies(version) ON DELETE RESTRICT,
 evidence_ids uuid[] NOT NULL CHECK(cardinality(evidence_ids)=4 AND array_position(evidence_ids,NULL) IS NULL),
 evidence_set_sha256 bytea NOT NULL CHECK(octet_length(evidence_set_sha256)=32),activation_sha256 bytea NOT NULL UNIQUE CHECK(octet_length(activation_sha256)=32),
 proposed_by_ref uuid NOT NULL,proposed_at timestamptz NOT NULL
);
CREATE TABLE privacy_activation_approvals (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),proposal_id uuid NOT NULL UNIQUE REFERENCES privacy_activation_proposals(id) ON DELETE RESTRICT,
 activation_sha256 bytea NOT NULL UNIQUE CHECK(octet_length(activation_sha256)=32),approved_by_ref uuid NOT NULL,approved_at timestamptz NOT NULL
);
ALTER TABLE privacy_request_activation ADD COLUMN approval_id uuid NULL REFERENCES privacy_activation_approvals(id) ON DELETE RESTRICT;
WITH migration_clock AS (SELECT clock_timestamp() occurred_at), disabled AS (
 UPDATE privacy_request_activation activation SET enabled=false,fulfilment_ready=false,updated_at=migration_clock.occurred_at
 FROM migration_clock WHERE activation.enabled OR activation.fulfilment_ready
 RETURNING activation.policy_version,activation.updated_by,migration_clock.occurred_at
)
INSERT INTO privacy_request_activation_events(policy_version,actor_ref,enabled,fulfilment_ready,occurred_at)
 SELECT policy_version,updated_by,false,false,occurred_at FROM disabled;
ALTER TABLE privacy_request_activation ADD CONSTRAINT privacy_activation_requires_evidence_approval
 CHECK(NOT enabled OR (fulfilment_ready AND approval_id IS NOT NULL));
CREATE TRIGGER privacy_activation_evidence_immutable BEFORE UPDATE OR DELETE ON privacy_activation_evidence FOR EACH ROW EXECUTE FUNCTION prevent_privacy_audit_mutation();
CREATE TRIGGER privacy_activation_proposals_immutable BEFORE UPDATE OR DELETE ON privacy_activation_proposals FOR EACH ROW EXECUTE FUNCTION prevent_privacy_audit_mutation();
CREATE TRIGGER privacy_activation_approvals_immutable BEFORE UPDATE OR DELETE ON privacy_activation_approvals FOR EACH ROW EXECUTE FUNCTION prevent_privacy_audit_mutation();

CREATE FUNCTION guard_privacy_activation_evidence() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
 IF NEW.enabled AND (NOT NEW.fulfilment_ready OR NEW.approval_id IS NULL OR NOT EXISTS(
  SELECT 1 FROM privacy_activation_approvals approval JOIN privacy_activation_proposals proposal ON proposal.id=approval.proposal_id
  WHERE approval.id=NEW.approval_id AND approval.activation_sha256=proposal.activation_sha256 AND proposal.policy_version=NEW.policy_version
   AND (SELECT count(DISTINCT evidence.kind) FROM privacy_activation_evidence evidence WHERE evidence.id=ANY(proposal.evidence_ids) AND evidence.expires_at>clock_timestamp())=4
 )) THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_activation_requires_approval'; END IF;
 RETURN NEW;
END;$$;
CREATE TRIGGER privacy_activation_evidence_guard BEFORE INSERT OR UPDATE OF enabled,fulfilment_ready,approval_id ON privacy_request_activation
 FOR EACH ROW EXECUTE FUNCTION guard_privacy_activation_evidence();

CREATE FUNCTION privacy_activation_ready(p_policy_version text)
RETURNS boolean LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT EXISTS(
  SELECT 1
  FROM privacy_request_activation activation
  JOIN privacy_activation_approvals approval ON approval.id=activation.approval_id
  JOIN privacy_activation_proposals proposal ON proposal.id=approval.proposal_id
  WHERE activation.singleton AND activation.enabled AND activation.fulfilment_ready
   AND activation.policy_version=p_policy_version AND proposal.policy_version=activation.policy_version
   AND approval.activation_sha256=proposal.activation_sha256
   AND approval.approved_by_ref<>proposal.proposed_by_ref
   AND cardinality(proposal.evidence_ids)=4
   AND (SELECT count(*) FROM privacy_activation_evidence evidence
        WHERE evidence.id=ANY(proposal.evidence_ids)
         AND evidence.expires_at>clock_timestamp()
         AND ((evidence.kind='RESTORE' AND evidence.reference_code='mycfc/privacy-restore-drill-attestation/v1')
          OR (evidence.kind='INFRASTRUCTURE' AND evidence.reference_code='mycfc/privacy-infrastructure-posture/v1')
          OR (evidence.kind='PROVIDER' AND evidence.reference_code='mycfc/privacy-provider-registry/v1')
          OR (evidence.kind='SCHEMA' AND evidence.reference_code='mycfc/schema-migration-inventory/v1')))=4
   AND (SELECT count(DISTINCT evidence.kind) FROM privacy_activation_evidence evidence
        WHERE evidence.id=ANY(proposal.evidence_ids) AND evidence.expires_at>clock_timestamp())=4
 );
$$;

CREATE FUNCTION privacy_worker_activation_ready()
RETURNS boolean LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT COALESCE((SELECT privacy_activation_ready(activation.policy_version)
  FROM privacy_request_activation activation WHERE activation.singleton),false);
$$;

CREATE FUNCTION privacy_worker_status()
RETURNS TABLE(pending bigint,leased bigint,retryable bigint,terminal bigint,aged_nonterminal bigint)
LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT count(*) FILTER(WHERE job.status='PENDING'),count(*) FILTER(WHERE job.status='LEASED'),
  count(*) FILTER(WHERE job.status='RETRY_WAIT'),count(*) FILTER(WHERE job.status='TERMINAL_FAILED'),
  count(*) FILTER(WHERE job.status IN ('PENDING','LEASED','RETRY_WAIT') AND job.updated_at<=clock_timestamp()-interval '15 minutes')
 FROM privacy_erasure_category_jobs job;
$$;

CREATE FUNCTION privacy_activation_record_evidence(p_actor uuid,p_kind text,p_evidence_sha256 bytea,p_reference_code text,p_observed_at timestamptz)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE evidence_id uuid;now_at timestamptz:=clock_timestamp();
BEGIN
 IF NOT EXISTS(SELECT 1 FROM users account JOIN user_platform_roles assignment ON assignment.user_id=account.id JOIN platform_roles role ON role.id=assignment.role_id
   WHERE account.id=p_actor AND account.is_active AND NOT account.is_dependent AND role.code='ADMIN')
  OR p_kind NOT IN ('RESTORE','INFRASTRUCTURE','PROVIDER','SCHEMA') OR octet_length(p_evidence_sha256)<>32
  OR p_reference_code!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,119}$' OR p_observed_at>now_at OR p_observed_at<=now_at-interval '90 days' THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy_activation_evidence_rejected'; END IF;
 IF (p_kind='RESTORE' AND p_reference_code<>'mycfc/privacy-restore-drill-attestation/v1')
  OR (p_kind='INFRASTRUCTURE' AND p_reference_code<>'mycfc/privacy-infrastructure-posture/v1')
  OR (p_kind='PROVIDER' AND p_reference_code<>'mycfc/privacy-provider-registry/v1')
  OR (p_kind='SCHEMA' AND p_reference_code<>'mycfc/schema-migration-inventory/v1') THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy_activation_evidence_contract_rejected'; END IF;
 INSERT INTO privacy_activation_evidence(kind,evidence_sha256,reference_code,observed_at,expires_at,recorded_by_ref,recorded_at)
 VALUES(p_kind,p_evidence_sha256,p_reference_code,p_observed_at,p_observed_at+interval '90 days',p_actor,now_at)
 ON CONFLICT(kind,evidence_sha256) DO NOTHING RETURNING id INTO evidence_id;
 IF evidence_id IS NULL THEN
  SELECT evidence.id INTO evidence_id FROM privacy_activation_evidence evidence
  WHERE evidence.kind=p_kind AND evidence.evidence_sha256=p_evidence_sha256 AND evidence.reference_code=p_reference_code
   AND evidence.observed_at=p_observed_at AND evidence.expires_at=p_observed_at+interval '90 days';
  IF evidence_id IS NULL THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_activation_evidence_conflict'; END IF;
 END IF;
 RETURN evidence_id;
END;$$;

CREATE FUNCTION privacy_activation_propose(p_actor uuid,p_policy_version text,p_evidence_ids uuid[])
RETURNS TABLE(proposal_id uuid,activation_sha256 bytea) LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE policy privacy_request_policies%ROWTYPE;evidence_digest bytea;activation_digest bytea;now_at timestamptz:=clock_timestamp();
BEGIN
 IF NOT EXISTS(SELECT 1 FROM users account JOIN privacy_executor_grants grant_row ON grant_row.user_id=account.id AND grant_row.revoked_at IS NULL
   WHERE account.id=p_actor AND account.is_active AND NOT account.is_dependent) OR cardinality(p_evidence_ids)<>4 THEN
  RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_activation_forbidden'; END IF;
 SELECT * INTO policy FROM privacy_request_policies WHERE version=p_policy_version FOR SHARE;
 IF policy.version IS NULL OR policy.adopted_at IS NULL OR policy.working_retention_days<>90
  OR (SELECT count(DISTINCT kind) FROM privacy_activation_evidence WHERE id=ANY(p_evidence_ids) AND expires_at>now_at)<>4
  OR (SELECT count(*) FROM privacy_activation_evidence WHERE id=ANY(p_evidence_ids) AND expires_at>now_at)<>4 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_activation_evidence_incomplete'; END IF;
 SELECT digest(convert_to(string_agg(kind||':'||encode(evidence_sha256,'hex')||':'||to_char(observed_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"')||':'||to_char(expires_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),'|' ORDER BY kind),'UTF8'),'sha256')
 INTO evidence_digest FROM privacy_activation_evidence WHERE id=ANY(p_evidence_ids);
 activation_digest:=digest(convert_to('privacy-activation/v1:'||policy.version||':'||COALESCE(policy.executor_version,'')||':'||COALESCE(policy.plan_schema_version,'')||':'||
  encode(digest(convert_to(policy.category_catalogue::text,'UTF8'),'sha256'),'hex')||':'||encode(evidence_digest,'hex'),'UTF8'),'sha256');
 RETURN QUERY INSERT INTO privacy_activation_proposals(policy_version,evidence_ids,evidence_set_sha256,activation_sha256,proposed_by_ref,proposed_at)
 VALUES(policy.version,(SELECT array_agg(id ORDER BY kind) FROM privacy_activation_evidence WHERE id=ANY(p_evidence_ids)),evidence_digest,activation_digest,p_actor,now_at)
 RETURNING id,privacy_activation_proposals.activation_sha256;
END;$$;

CREATE FUNCTION privacy_activation_approve(p_actor uuid,p_proposal_id uuid,p_activation_sha256 bytea)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE proposal privacy_activation_proposals%ROWTYPE;approval_id uuid;now_at timestamptz:=clock_timestamp();recomputed bytea;
BEGIN
 IF NOT EXISTS(SELECT 1 FROM users account JOIN user_platform_roles assignment ON assignment.user_id=account.id JOIN platform_roles role ON role.id=assignment.role_id
   WHERE account.id=p_actor AND account.is_active AND NOT account.is_dependent AND role.code='ADMIN') THEN
  RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_activation_forbidden'; END IF;
 SELECT * INTO proposal FROM privacy_activation_proposals WHERE id=p_proposal_id FOR SHARE;
 IF proposal.id IS NULL OR proposal.proposed_by_ref=p_actor OR proposal.activation_sha256<>p_activation_sha256
  OR EXISTS(SELECT 1 FROM privacy_activation_approvals approval WHERE approval.proposal_id=proposal.id)
  OR (SELECT count(DISTINCT kind) FROM privacy_activation_evidence WHERE id=ANY(proposal.evidence_ids) AND expires_at>now_at)<>4 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_activation_unavailable'; END IF;
 SELECT digest(convert_to(string_agg(kind||':'||encode(evidence_sha256,'hex')||':'||to_char(observed_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"')||':'||to_char(expires_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),'|' ORDER BY kind),'UTF8'),'sha256')
 INTO recomputed FROM privacy_activation_evidence WHERE id=ANY(proposal.evidence_ids);
 IF recomputed<>proposal.evidence_set_sha256 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_activation_stale'; END IF;
 INSERT INTO privacy_activation_approvals(proposal_id,activation_sha256,approved_by_ref,approved_at)
 VALUES(proposal.id,proposal.activation_sha256,p_actor,now_at) RETURNING id INTO approval_id;
 PERFORM set_config('mycfc.privacy_activation_approval','approved',true);
 INSERT INTO privacy_request_activation(singleton,policy_version,enabled,fulfilment_ready,updated_by,updated_at,approval_id)
 VALUES(true,proposal.policy_version,true,true,p_actor,now_at,approval_id)
 ON CONFLICT(singleton) DO UPDATE SET policy_version=EXCLUDED.policy_version,enabled=true,fulfilment_ready=true,updated_by=EXCLUDED.updated_by,updated_at=EXCLUDED.updated_at,approval_id=EXCLUDED.approval_id;
 INSERT INTO privacy_request_activation_events(policy_version,actor_ref,enabled,fulfilment_ready,occurred_at)
 VALUES(proposal.policy_version,p_actor,true,true,now_at);
 RETURN approval_id;
END;$$;

CREATE FUNCTION privacy_activation_control_snapshot(p_actor uuid)
RETURNS TABLE(policy_version text,ready boolean,evidence_id uuid,evidence_kind text,evidence_observed_at timestamptz,
 proposal_id uuid,proposal_sha256 bytea,proposal_created_at timestamptz,can_propose boolean,can_renew boolean,can_approve boolean)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE actor_executor boolean;actor_admin boolean;selected_policy text;pending privacy_activation_proposals%ROWTYPE;current_evidence integer;renewal_evidence boolean;
BEGIN
 SELECT EXISTS(SELECT 1 FROM users account JOIN privacy_executor_grants grant_row ON grant_row.user_id=account.id AND grant_row.revoked_at IS NULL
   WHERE account.id=p_actor AND account.is_active AND NOT account.is_dependent),
  EXISTS(SELECT 1 FROM users account JOIN user_platform_roles assignment ON assignment.user_id=account.id
   JOIN platform_roles role ON role.id=assignment.role_id WHERE account.id=p_actor AND account.is_active AND NOT account.is_dependent AND role.code='ADMIN')
 INTO actor_executor,actor_admin;
 IF NOT actor_executor AND NOT actor_admin THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_control_forbidden'; END IF;
 SELECT COALESCE((SELECT activation.policy_version FROM privacy_request_activation activation WHERE activation.singleton AND activation.enabled),
  (SELECT policy.version FROM privacy_request_policies policy WHERE policy.adopted_at IS NOT NULL ORDER BY policy.adopted_at DESC,policy.version DESC LIMIT 1))
 INTO selected_policy;
 IF selected_policy IS NULL THEN RETURN; END IF;
 SELECT proposal.* INTO pending FROM privacy_activation_proposals proposal
  WHERE proposal.policy_version=selected_policy AND NOT EXISTS(SELECT 1 FROM privacy_activation_approvals approval WHERE approval.proposal_id=proposal.id)
  ORDER BY proposal.proposed_at DESC,proposal.id DESC LIMIT 1;
 SELECT count(*) INTO current_evidence FROM (
  SELECT DISTINCT ON(evidence.kind) evidence.kind FROM privacy_activation_evidence evidence WHERE evidence.expires_at>clock_timestamp()
  ORDER BY evidence.kind,evidence.observed_at DESC,evidence.id DESC
 ) current_rows;
 SELECT EXISTS(
  SELECT 1 FROM (
   SELECT DISTINCT ON(evidence.kind) evidence.id,evidence.kind
   FROM privacy_activation_evidence evidence WHERE evidence.expires_at>clock_timestamp()
   ORDER BY evidence.kind,evidence.observed_at DESC,evidence.id DESC
  ) latest
  JOIN privacy_request_activation activation ON activation.singleton AND activation.enabled AND activation.policy_version=selected_policy
  JOIN privacy_activation_approvals approval ON approval.id=activation.approval_id
  JOIN privacy_activation_proposals proposal ON proposal.id=approval.proposal_id
  WHERE NOT latest.id=ANY(proposal.evidence_ids)
 ) INTO renewal_evidence;
 RETURN QUERY
 SELECT selected_policy,privacy_activation_ready(selected_policy),evidence.id,evidence.kind::text,evidence.observed_at,
  pending.id,pending.activation_sha256,pending.proposed_at,
  (actor_executor AND current_evidence=4 AND pending.id IS NULL AND NOT privacy_activation_ready(selected_policy)),
  (actor_executor AND current_evidence=4 AND pending.id IS NULL AND privacy_activation_ready(selected_policy) AND renewal_evidence),
  (actor_admin AND pending.id IS NOT NULL AND pending.proposed_by_ref<>p_actor)
 FROM (SELECT true singleton) seed LEFT JOIN LATERAL(
  SELECT DISTINCT ON(candidate.kind) candidate.id,candidate.kind,candidate.observed_at FROM privacy_activation_evidence candidate
  WHERE candidate.expires_at>clock_timestamp() ORDER BY candidate.kind,candidate.observed_at DESC,candidate.id DESC
 ) evidence ON true ORDER BY evidence.kind;
END;$$;

ALTER TABLE email_outbox DROP CONSTRAINT email_outbox_message_valid;
ALTER TABLE email_outbox ADD CONSTRAINT email_outbox_message_valid CHECK (
 (message_type='EMAIL_VERIFICATION' AND verification_token_id IS NOT NULL AND password_reset_token_id IS NULL AND sealed_payload IS NULL AND privacy_request_id IS NULL AND privacy_requester_id IS NULL AND privacy_event_key IS NULL)
 OR (message_type='PASSWORD_RESET' AND verification_token_id IS NULL AND password_reset_token_id IS NOT NULL AND sealed_payload IS NOT NULL AND privacy_request_id IS NULL AND privacy_requester_id IS NULL AND privacy_event_key IS NULL)
 OR (message_type IN ('PRIVACY_ACKNOWLEDGEMENT','PRIVACY_DECISION','PRIVACY_PROCESSING_STARTED','PRIVACY_COMPLETED') AND verification_token_id IS NULL AND password_reset_token_id IS NULL AND sealed_payload IS NOT NULL AND privacy_request_id IS NOT NULL AND privacy_event_key IS NOT NULL)
);

REVOKE ALL ON TABLE privacy_protected.completion_notice_targets,privacy_erasure_completion_manifests,privacy_completion_access_links,
 privacy_terminal_requeue_proposals,privacy_terminal_requeue_approvals,privacy_activation_evidence,privacy_activation_proposals,privacy_activation_approvals FROM PUBLIC;
REVOKE ALL ON FUNCTION privacy_execution_capture_completion_notice(uuid,uuid),privacy_completion_prepare(uuid,uuid),privacy_completion_list_pending(uuid,integer),privacy_completion_finalize(uuid,uuid,bytea,bytea),
 privacy_completion_consume(bytea),privacy_completion_validate(bytea),privacy_completion_notice_deliverable(uuid,timestamptz),privacy_terminal_requeue_propose(uuid,uuid),privacy_terminal_requeue_approve(uuid,bytea,uuid),privacy_completion_control_snapshot(uuid,uuid),
 privacy_activation_ready(text),privacy_worker_activation_ready(),privacy_worker_status(),privacy_activation_record_evidence(uuid,text,bytea,text,timestamptz),privacy_activation_propose(uuid,text,uuid[]),privacy_activation_approve(uuid,uuid,bytea),privacy_activation_control_snapshot(uuid) FROM PUBLIC;
-- #247 replay hardening: source-state verification, local provider fencing,
-- original erasure clocks, and an explicitly synthetic offline drill path.
ALTER TABLE privacy_protected.restore_ledger_imports
 ADD COLUMN erasure_effective_at timestamptz NULL,
 ADD COLUMN closure_version varchar(48) NULL,
 ADD COLUMN synthetic_fixture varchar(80) NULL,
 ADD CONSTRAINT restore_ledger_imports_synthetic_fixture_check
 CHECK(synthetic_fixture IS NULL OR synthetic_fixture='mycfc/privacy-restore-synthetic-fixture/v1') NOT VALID;
ALTER TABLE privacy_protected.restore_ledger_imports VALIDATE CONSTRAINT restore_ledger_imports_synthetic_fixture_check;
ALTER TABLE privacy_protected.restore_ledger_imports ADD CONSTRAINT restore_ledger_imports_closure_version_check
 CHECK((kind='intent' AND closure_version IS NULL) OR (kind='closure' AND closure_version IN ('restore-tombstone-closure/v2','restore-tombstone-closure/v3'))) NOT VALID;
ALTER TABLE privacy_protected.restore_ledger_imports VALIDATE CONSTRAINT restore_ledger_imports_closure_version_check;
ALTER TABLE privacy_protected.restore_tombstone_closure_receipts DROP CONSTRAINT restore_tombstone_closure_receipts_ledger_version_check;
ALTER TABLE privacy_protected.restore_tombstone_closure_receipts ADD CONSTRAINT restore_tombstone_closure_receipts_ledger_version_check
 CHECK(ledger_version IN ('restore-tombstone-closure/v1','restore-tombstone-closure/v2','restore-tombstone-closure/v3')) NOT VALID;
ALTER TABLE privacy_protected.restore_tombstone_closure_receipts VALIDATE CONSTRAINT restore_tombstone_closure_receipts_ledger_version_check;
ALTER TABLE privacy_protected.restore_ledger_imports DROP CONSTRAINT restore_ledger_imports_operations_check;
ALTER TABLE privacy_protected.restore_ledger_imports ADD CONSTRAINT restore_ledger_imports_operation_count CHECK(cardinality(operations) BETWEEN 1 AND 17) NOT VALID;
ALTER TABLE privacy_protected.restore_ledger_imports VALIDATE CONSTRAINT restore_ledger_imports_operation_count;

ALTER TABLE privacy_protected.restore_replay_runs
 ADD COLUMN outcome_code varchar(32) NULL,
 ADD CONSTRAINT restore_replay_runs_outcome_check CHECK(
  (status='PENDING' AND completed_at IS NULL AND outcome_code IS NULL)
  OR (status='SUCCEEDED' AND completed_at IS NOT NULL AND outcome_code IN ('REPLAYED','ALREADY_APPLIED_SOURCE'))
 ) NOT VALID;
UPDATE privacy_protected.restore_replay_runs SET outcome_code='REPLAYED' WHERE status='SUCCEEDED';
ALTER TABLE privacy_protected.restore_replay_runs VALIDATE CONSTRAINT restore_replay_runs_outcome_check;

CREATE TABLE privacy_protected.restore_replay_already_applied_evidence (
 run_id uuid PRIMARY KEY REFERENCES privacy_protected.restore_replay_runs(id) ON DELETE RESTRICT,
 import_id uuid NOT NULL UNIQUE REFERENCES privacy_protected.restore_ledger_imports(id) ON DELETE RESTRICT,
 evidence_code varchar(40) NOT NULL CHECK(evidence_code='ALREADY_APPLIED'),
 source_execution_id uuid NOT NULL,erasure_effective_at timestamptz NOT NULL,
 verified_operations text[] NOT NULL CHECK(cardinality(verified_operations) BETWEEN 1 AND 17),
 verification_sha256 bytea NOT NULL CHECK(octet_length(verification_sha256)=32),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TRIGGER privacy_restore_replay_already_applied_evidence_immutable BEFORE UPDATE OR DELETE
 ON privacy_protected.restore_replay_already_applied_evidence FOR EACH ROW
 EXECUTE FUNCTION public.prevent_privacy_execution_record_delete();

CREATE TABLE privacy_protected.restore_synthetic_fixtures (
 subject_user_id uuid PRIMARY KEY REFERENCES public.users(id) ON DELETE RESTRICT,
 source_execution_id uuid NOT NULL UNIQUE,source_request_id uuid NOT NULL UNIQUE,source_request_ref uuid NOT NULL UNIQUE,
 plan_sha256 bytea NOT NULL CHECK(octet_length(plan_sha256)=32),workset_sha256 bytea NOT NULL CHECK(octet_length(workset_sha256)=32),
 erasure_effective_at timestamptz NOT NULL,operations text[] NOT NULL CHECK(cardinality(operations) BETWEEN 1 AND 17),
 fixture_marker varchar(80) NOT NULL CHECK(fixture_marker='mycfc/privacy-restore-synthetic-fixture/v1'),
 created_by_ref uuid NOT NULL,created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TRIGGER privacy_restore_synthetic_fixtures_immutable BEFORE UPDATE OR DELETE
 ON privacy_protected.restore_synthetic_fixtures FOR EACH ROW
 EXECUTE FUNCTION public.prevent_privacy_execution_record_delete();

CREATE TABLE privacy_protected.restore_replay_inventory_attestations (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),input_source varchar(32) NOT NULL CHECK(input_source IN ('LIVE_LEDGER','SYNTHETIC_BOOTSTRAP')),
 inventory_sha256 bytea NOT NULL CHECK(octet_length(inventory_sha256)=32),object_count integer NOT NULL CHECK(object_count>0),
 policy_version varchar(80) NOT NULL,executor_version varchar(80) NOT NULL,plan_schema_version varchar(80) NOT NULL,
 image_digest varchar(71) NOT NULL CHECK(image_digest~'^sha256:[0-9a-f]{64}$'),schema_migration_digest bytea NOT NULL CHECK(octet_length(schema_migration_digest)=32),
 imported_count integer NOT NULL CHECK(imported_count>=0),replayed_count integer NOT NULL CHECK(replayed_count>0),
 already_applied_count integer NOT NULL CHECK(already_applied_count>=0),absence_verified_count integer NOT NULL,
 synthetic_replayed_count integer NOT NULL CHECK(synthetic_replayed_count>=0),closure_v3_count integer NOT NULL CHECK(closure_v3_count>=0),
 intent_only_count integer NOT NULL CHECK(intent_only_count>=0),legacy_closure_v2_count integer NOT NULL CHECK(legacy_closure_v2_count>=0),
 erasure_effective_at_verified_count integer NOT NULL CHECK(erasure_effective_at_verified_count>=0),
 evidence_sha256 bytea NOT NULL CHECK(octet_length(evidence_sha256)=32),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),UNIQUE(input_source,inventory_sha256),
 CHECK(replayed_count=imported_count+already_applied_count),CHECK(absence_verified_count=replayed_count),
 CHECK(closure_v3_count=replayed_count AND intent_only_count=0 AND legacy_closure_v2_count=0 AND erasure_effective_at_verified_count=replayed_count),
 CHECK((input_source='LIVE_LEDGER' AND synthetic_replayed_count=0) OR (input_source='SYNTHETIC_BOOTSTRAP' AND synthetic_replayed_count=replayed_count))
);
CREATE TABLE privacy_protected.restore_replay_inventory_attestation_runs (
 attestation_id uuid NOT NULL REFERENCES privacy_protected.restore_replay_inventory_attestations(id) ON DELETE RESTRICT,
 run_id uuid NOT NULL UNIQUE REFERENCES privacy_protected.restore_replay_runs(id) ON DELETE RESTRICT,
 PRIMARY KEY(attestation_id,run_id)
);
CREATE TRIGGER privacy_restore_replay_inventory_attestations_immutable BEFORE UPDATE OR DELETE
 ON privacy_protected.restore_replay_inventory_attestations FOR EACH ROW EXECUTE FUNCTION public.prevent_privacy_execution_record_delete();
CREATE TRIGGER privacy_restore_replay_inventory_attestation_runs_immutable BEFORE UPDATE OR DELETE
 ON privacy_protected.restore_replay_inventory_attestation_runs FOR EACH ROW EXECUTE FUNCTION public.prevent_privacy_execution_record_delete();

CREATE FUNCTION public.privacy_tombstone_prepare_closure_v3(p_execution_id uuid,p_worker_ref uuid)
RETURNS TABLE(execution_id uuid,request_id uuid,request_ref uuid,subject_user_id uuid,plan_sha256 bytea,workset_sha256 bytea,
 execution_started_at timestamptz,closed_at timestamptz,evidence_expires_at timestamptz,erasure_effective_at timestamptz,replay_operations text[])
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT prepared.execution_id,prepared.request_id,prepared.request_ref,prepared.subject_user_id,prepared.plan_sha256,prepared.workset_sha256,
  prepared.execution_started_at,prepared.closed_at,prepared.evidence_expires_at,subject.erased_at,prepared.replay_operations
 FROM public.privacy_tombstone_prepare_closure_v2(p_execution_id,p_worker_ref) prepared
 JOIN users subject ON subject.id=prepared.subject_user_id
 WHERE subject.erased_at IS NOT NULL AND subject.erasure_execution_id=prepared.execution_id AND subject.erasure_replay_run_id IS NULL
  AND subject.erased_at>=prepared.execution_started_at AND subject.erased_at<=prepared.closed_at
  AND NOT EXISTS(SELECT 1 FROM consent_forms consent WHERE consent.user_id=subject.id AND consent.ceased_at IS NULL)
  AND NOT EXISTS(SELECT 1 FROM consent_forms consent WHERE consent.user_id=subject.id AND consent.cessation_reason='ACCOUNT_ERASURE'
   AND (consent.ceased_at<>subject.erased_at OR consent.evidence_expires_at<>subject.erased_at+interval '3 years'));
$$;

CREATE FUNCTION public.privacy_tombstone_confirm_closure_v3(
 p_execution_id uuid,p_worker_ref uuid,p_ledger_version text,p_encryption_key_id text,p_locator_key_id text,p_locator_digest bytea,
 p_object_version_id text,p_ciphertext_sha256 bytea,p_size_bytes bigint,p_written_at timestamptz,p_verified_at timestamptz
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE intent privacy_protected.restore_tombstone_closure_intents%ROWTYPE;existing privacy_protected.restore_tombstone_closure_receipts%ROWTYPE;
BEGIN
 SELECT * INTO intent FROM privacy_protected.restore_tombstone_closure_intents WHERE execution_id=p_execution_id;
 IF intent.execution_id IS NULL OR p_worker_ref IS NULL OR p_ledger_version<>'restore-tombstone-closure/v3'
  OR p_encryption_key_id IS NULL OR p_encryption_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'
  OR p_locator_key_id IS NULL OR p_locator_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'
  OR octet_length(p_locator_digest)<>32 OR octet_length(p_ciphertext_sha256)<>32 OR p_object_version_id IS NULL
  OR p_object_version_id<>btrim(p_object_version_id) OR char_length(p_object_version_id) NOT BETWEEN 1 AND 1024
  OR p_size_bytes NOT BETWEEN 1 AND 1048576 OR p_written_at IS NULL OR p_verified_at<p_written_at OR p_verified_at>intent.evidence_expires_at
  OR NOT EXISTS(SELECT 1 FROM users subject JOIN privacy_erasure_executions execution ON execution.id=subject.erasure_execution_id
   WHERE execution.id=p_execution_id AND subject.erased_at BETWEEN execution.accepted_at AND intent.closed_at)
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_tombstone_closure_receipt_rejected'; END IF;
 SELECT * INTO existing FROM privacy_protected.restore_tombstone_closure_receipts WHERE execution_id=p_execution_id;
 IF existing.execution_id IS NULL THEN
  INSERT INTO privacy_protected.restore_tombstone_closure_receipts(execution_id,ledger_version,encryption_key_id,locator_key_id,locator_digest,object_version_id,ciphertext_sha256,size_bytes,written_at,verified_at)
  VALUES(p_execution_id,p_ledger_version,p_encryption_key_id,p_locator_key_id,p_locator_digest,p_object_version_id,p_ciphertext_sha256,p_size_bytes,p_written_at,p_verified_at);
 ELSIF ROW(existing.ledger_version,existing.encryption_key_id,existing.locator_key_id,existing.locator_digest,existing.object_version_id,existing.ciphertext_sha256,existing.size_bytes,existing.written_at,existing.verified_at)
  IS DISTINCT FROM ROW(p_ledger_version,p_encryption_key_id,p_locator_key_id,p_locator_digest,p_object_version_id,p_ciphertext_sha256,p_size_bytes,p_written_at,p_verified_at)
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_tombstone_closure_receipt_conflict'; END IF;
 RETURN p_execution_id;
END; $$;

CREATE FUNCTION public.privacy_restore_create_synthetic_fixture(p_worker_ref uuid)
RETURNS TABLE(source_execution_id uuid,source_request_id uuid,source_request_ref uuid,subject_user_id uuid,
 plan_sha256 bytea,workset_sha256 bytea,erasure_effective_at timestamptz,operations text[])
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE v_subject uuid:=gen_random_uuid();v_execution uuid:=gen_random_uuid();v_request uuid:=gen_random_uuid();v_ref uuid:=gen_random_uuid();
 v_plan bytea:=gen_random_bytes(32);v_workset bytea:=gen_random_bytes(32);v_effective timestamptz:=clock_timestamp();
 v_operations text[]:=ARRAY['AUTH_ACCESS_REVOKE','AUTH_TOKEN_DELETE','PROFILE_IDENTITY_DELETE','PROVIDER_LOCAL_FENCE','IDENTITY_CLEAR'];
BEGIN
 IF p_worker_ref IS NULL OR current_setting('mycfc.privacy_restore_isolated',true)<>'on' THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_synthetic_fixture_rejected'; END IF;
 INSERT INTO users(id,name,email,password_hash,date_of_birth,created_at,updated_at)
 VALUES(v_subject,'Synthetic restore fixture',('synthetic-'||v_subject::text||'@invalid.invalid')::citext,'synthetic-disabled','1900-01-01',v_effective,v_effective);
 INSERT INTO member_profiles(user_id,address_line1,created_at,updated_at) VALUES(v_subject,'Synthetic fixture only',v_effective,v_effective);
 INSERT INTO privacy_protected.provider_connections(id,subject_user_id,service_code,provider_role,provider_contract_version,
  registry_evidence_key_id,registry_evidence_digest,target_key_id,target_opaque,credential_key_id,credential_opaque,state,
  sync_enabled,webhook_enabled,reconnect_enabled,created_at,updated_at)
 VALUES(gen_random_uuid(),v_subject,'synthetic.restore.fixture','PROCESSOR','synthetic-v1','synthetic-key',gen_random_bytes(32),
  'synthetic-key',gen_random_bytes(32),'synthetic-key',gen_random_bytes(32),'ACTIVE',true,true,true,v_effective,v_effective);
 INSERT INTO privacy_protected.restore_synthetic_fixtures(subject_user_id,source_execution_id,source_request_id,source_request_ref,
  plan_sha256,workset_sha256,erasure_effective_at,operations,fixture_marker,created_by_ref,created_at)
 VALUES(v_subject,v_execution,v_request,v_ref,v_plan,v_workset,v_effective,v_operations,'mycfc/privacy-restore-synthetic-fixture/v1',p_worker_ref,v_effective);
 RETURN QUERY SELECT v_execution,v_request,v_ref,v_subject,v_plan,v_workset,v_effective,v_operations;
END; $$;

CREATE FUNCTION public.privacy_restore_import_authenticated_v2_hardened(
 p_worker_ref uuid,p_kind text,p_record_version text,p_envelope_version text,p_encryption_key_id text,p_locator_key_id text,p_locator_digest bytea,
 p_ciphertext_sha256 bytea,p_object_version_id text,p_written_at timestamptz,p_verified_at timestamptz,p_retain_until timestamptz,
 p_source_execution_id uuid,p_source_request_id uuid,p_source_request_ref uuid,p_subject_user_id uuid,p_plan_sha256 bytea,p_workset_sha256 bytea,
 p_execution_started_at timestamptz,p_erasure_effective_at timestamptz,p_closure_version text,p_synthetic_fixture text,p_replay_version text,p_action_version text,
 p_operations text[],p_prescription_sha256 bytea,p_record_sha256 bytea
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE import_ref uuid; existing privacy_protected.restore_ledger_imports%ROWTYPE; synthetic_match boolean;
BEGIN
 SELECT EXISTS(SELECT 1 FROM privacy_protected.restore_synthetic_fixtures fixture
  WHERE fixture.subject_user_id=p_subject_user_id AND fixture.source_execution_id=p_source_execution_id
   AND fixture.source_request_id=p_source_request_id AND fixture.source_request_ref=p_source_request_ref
   AND fixture.plan_sha256=p_plan_sha256 AND fixture.workset_sha256=p_workset_sha256
   AND fixture.erasure_effective_at=p_erasure_effective_at AND fixture.operations=p_operations
   AND fixture.fixture_marker=p_synthetic_fixture) INTO synthetic_match;
 IF p_worker_ref IS NULL OR p_kind NOT IN ('intent','closure') OR p_record_version<>'restore-tombstone/v2'
  OR p_envelope_version<>'x25519-aes256gcm-hkdfsha256/v2' OR p_replay_version<>'relational-erasure-replay/v1' OR p_action_version<>'v1'
  OR p_encryption_key_id IS NULL OR p_encryption_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'
  OR p_locator_key_id IS NULL OR p_locator_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'
  OR octet_length(p_locator_digest)<>32 OR octet_length(p_ciphertext_sha256)<>32 OR octet_length(p_plan_sha256)<>32
  OR octet_length(p_workset_sha256)<>32 OR octet_length(p_prescription_sha256)<>32 OR octet_length(p_record_sha256)<>32
  OR p_object_version_id IS NULL OR p_object_version_id<>btrim(p_object_version_id) OR char_length(p_object_version_id) NOT BETWEEN 1 AND 1024
  OR p_written_at IS NULL OR p_verified_at<p_written_at OR p_source_execution_id IS NULL OR p_source_request_id IS NULL
  OR p_source_request_ref IS NULL OR p_subject_user_id IS NULL OR p_execution_started_at IS NULL OR p_erasure_effective_at IS NULL
  OR p_erasure_effective_at<p_execution_started_at OR cardinality(p_operations) NOT BETWEEN 1 AND 17
  OR (p_kind='intent' AND (p_closure_version IS NOT NULL OR p_erasure_effective_at<>p_execution_started_at))
  OR (p_kind='closure' AND p_closure_version NOT IN ('restore-tombstone-closure/v2','restore-tombstone-closure/v3'))
  OR EXISTS(SELECT 1 FROM unnest(p_operations) operation WHERE NOT(public.privacy_relational_replay_operation_supported(operation) OR operation='PROVIDER_LOCAL_FENCE'))
  OR cardinality(p_operations)<>(SELECT count(DISTINCT operation) FROM unnest(p_operations) operation)
  OR (p_synthetic_fixture IS NULL AND EXISTS(SELECT 1 FROM privacy_protected.restore_synthetic_fixtures WHERE subject_user_id=p_subject_user_id))
  OR (p_synthetic_fixture IS NOT NULL AND (p_synthetic_fixture<>'mycfc/privacy-restore-synthetic-fixture/v1' OR NOT synthetic_match))
  OR (p_kind='intent' AND p_retain_until IS NOT NULL) OR (p_kind='closure' AND (p_retain_until IS NULL OR p_verified_at>p_retain_until)) THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_import_rejected'; END IF;
 SELECT * INTO existing FROM privacy_protected.restore_ledger_imports
  WHERE (locator_key_id=p_locator_key_id AND locator_digest=p_locator_digest) OR source_execution_id=p_source_execution_id FOR UPDATE;
 IF existing.id IS NULL THEN
  INSERT INTO privacy_protected.restore_ledger_imports(kind,record_version,envelope_version,encryption_key_id,locator_key_id,locator_digest,
   ciphertext_sha256,object_version_id,written_at,verified_at,retain_until,source_execution_id,source_request_id,source_request_ref,subject_user_id,
   plan_sha256,workset_sha256,execution_started_at,erasure_effective_at,closure_version,synthetic_fixture,replay_version,action_version,operations,
   prescription_sha256,record_sha256,imported_by_ref)
  VALUES(p_kind,p_record_version,p_envelope_version,p_encryption_key_id,p_locator_key_id,p_locator_digest,p_ciphertext_sha256,p_object_version_id,
   p_written_at,p_verified_at,p_retain_until,p_source_execution_id,p_source_request_id,p_source_request_ref,p_subject_user_id,p_plan_sha256,p_workset_sha256,
   p_execution_started_at,p_erasure_effective_at,p_closure_version,p_synthetic_fixture,p_replay_version,p_action_version,p_operations,p_prescription_sha256,p_record_sha256,p_worker_ref)
  RETURNING id INTO import_ref;
 ELSIF ROW(existing.kind,existing.record_version,existing.envelope_version,existing.encryption_key_id,existing.locator_key_id,existing.locator_digest,
   existing.ciphertext_sha256,existing.object_version_id,existing.written_at,existing.verified_at,existing.retain_until,existing.source_execution_id,
   existing.source_request_id,existing.source_request_ref,existing.subject_user_id,existing.plan_sha256,existing.workset_sha256,existing.execution_started_at,
   existing.erasure_effective_at,existing.closure_version,existing.synthetic_fixture,existing.replay_version,existing.action_version,existing.operations,existing.prescription_sha256,existing.record_sha256)
  IS DISTINCT FROM ROW(p_kind,p_record_version,p_envelope_version,p_encryption_key_id,p_locator_key_id,p_locator_digest,p_ciphertext_sha256,
   p_object_version_id,p_written_at,p_verified_at,p_retain_until,p_source_execution_id,p_source_request_id,p_source_request_ref,p_subject_user_id,
   p_plan_sha256,p_workset_sha256,p_execution_started_at,p_erasure_effective_at,p_closure_version,p_synthetic_fixture,p_replay_version,p_action_version,p_operations,
   p_prescription_sha256,p_record_sha256) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_import_conflict';
 ELSE import_ref:=existing.id; END IF;
 RETURN import_ref;
END; $$;

CREATE FUNCTION public.privacy_restore_verify_operation(p_run_id uuid,p_operation_code text,p_source_already_applied boolean)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE imported privacy_protected.restore_ledger_imports%ROWTYPE; effective_at timestamptz;
BEGIN
 SELECT imported_row.* INTO imported FROM privacy_protected.restore_replay_runs run
 JOIN privacy_protected.restore_ledger_imports imported_row ON imported_row.id=run.import_id WHERE run.id=p_run_id;
 IF imported.id IS NULL OR NOT(p_operation_code=ANY(imported.operations)) OR
  NOT(public.privacy_relational_replay_operation_supported(p_operation_code) OR p_operation_code='PROVIDER_LOCAL_FENCE') THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 effective_at:=imported.erasure_effective_at;
 IF p_source_already_applied AND NOT EXISTS(SELECT 1 FROM users WHERE id=imported.subject_user_id
  AND erasure_execution_id=imported.source_execution_id AND erased_at=imported.erasure_effective_at) THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 CASE p_operation_code
 WHEN 'ACTIVITY_CONNECTION_DISCONNECT' THEN
  IF EXISTS(SELECT 1 FROM activity_connections WHERE user_id=imported.subject_user_id AND (status<>'DISCONNECTED' OR credentials_ciphertext IS NOT NULL OR credential_key_id IS NOT NULL OR cardinality(scopes)>0 OR sync_cursor IS NOT NULL)) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'ACTIVITY_SUBJECT_DELETE' THEN IF EXISTS(SELECT 1 FROM activity_connections WHERE user_id=imported.subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'ANNOUNCEMENT_DELIVERY_DELETE' THEN IF EXISTS(SELECT 1 FROM announcement_deliveries WHERE user_id=imported.subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'AUTH_ACCESS_REVOKE' THEN IF EXISTS(SELECT 1 FROM sessions WHERE subject_indexed AND user_id=imported.subject_user_id) OR EXISTS(SELECT 1 FROM user_platform_roles WHERE user_id=imported.subject_user_id) OR EXISTS(SELECT 1 FROM staff_grants WHERE user_id=imported.subject_user_id AND revoked_at IS NULL) OR EXISTS(SELECT 1 FROM privacy_reviewer_grants WHERE user_id=imported.subject_user_id AND revoked_at IS NULL) OR EXISTS(SELECT 1 FROM privacy_executor_grants WHERE user_id=imported.subject_user_id AND revoked_at IS NULL) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'AUTH_TOKEN_DELETE' THEN IF EXISTS(SELECT 1 FROM email_verification_tokens WHERE user_id=imported.subject_user_id) OR EXISTS(SELECT 1 FROM password_reset_tokens WHERE user_id=imported.subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'DEPENDANT_RELATIONSHIP_DELETE' THEN IF EXISTS(SELECT 1 FROM users WHERE guardian_id=imported.subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'EVENT_RESPONSE_DELETE' THEN IF EXISTS(SELECT 1 FROM event_responses WHERE user_id=imported.subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'IDENTITY_CLEAR' THEN
  IF NOT EXISTS(SELECT 1 FROM users WHERE id=imported.subject_user_id AND erased_at=effective_at AND NOT is_active AND email IS NULL AND minor_login_id IS NULL AND password_hash IS NULL AND guardian_id IS NULL AND ((p_source_already_applied AND erasure_execution_id=imported.source_execution_id AND erasure_replay_run_id IS NULL) OR (NOT p_source_already_applied AND erasure_execution_id IS NULL AND erasure_replay_run_id=p_run_id))) OR EXISTS(SELECT 1 FROM consent_forms WHERE user_id=imported.subject_user_id AND ceased_at IS NULL) OR EXISTS(SELECT 1 FROM consent_forms WHERE user_id=imported.subject_user_id AND cessation_reason='ACCOUNT_ERASURE' AND (ceased_at<>effective_at OR evidence_expires_at<>effective_at+interval '3 years')) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'MEMBERSHIP_ACTIVE_REVOKE','MEMBERSHIP_HISTORY_ANONYMIZE' THEN IF EXISTS(SELECT 1 FROM user_memberships WHERE user_id=imported.subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'PROFILE_HEALTH_DELETE' THEN IF EXISTS(SELECT 1 FROM member_profiles WHERE user_id=imported.subject_user_id AND (emergency_contact_name<>'' OR emergency_contact_relationship<>'' OR emergency_contact_phone<>'' OR emergency_contact_alternate_phone<>'' OR medical_declaration<>'UNKNOWN' OR allergies<>'' OR medical_conditions<>'' OR medication<>'' OR activity_restrictions<>'' OR medical_notes<>'')) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'PROFILE_IDENTITY_DELETE' THEN IF EXISTS(SELECT 1 FROM member_profiles WHERE user_id=imported.subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'PROVIDER_LOCAL_FENCE' THEN IF EXISTS(SELECT 1 FROM privacy_protected.provider_connections WHERE subject_user_id=imported.subject_user_id) OR EXISTS(SELECT 1 FROM privacy_protected.provider_credential_quarantine q JOIN privacy_protected.provider_targets target ON target.id=q.target_id JOIN privacy_protected.provider_capture_sets capture ON capture.execution_id=target.execution_id WHERE capture.subject_user_id=imported.subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'REPAIR_REPORTER_ANONYMIZE' THEN IF EXISTS(SELECT 1 FROM repair_requests WHERE reported_by_id=imported.subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'SUGGESTION_SUBJECT_DELETE' THEN IF EXISTS(SELECT 1 FROM suggestions WHERE requester_id=imported.subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'TRAINING_PRESCRIPTION_DELETE' THEN IF EXISTS(SELECT 1 FROM training_prescriptions WHERE athlete_user_id=imported.subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'TRAINING_RESULT_DELETE' THEN IF EXISTS(SELECT 1 FROM training_session_outcomes WHERE user_id=imported.subject_user_id) OR EXISTS(SELECT 1 FROM training_logs WHERE user_id=imported.subject_user_id) OR EXISTS(SELECT 1 FROM performance_metrics WHERE user_id=imported.subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 ELSE RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed';
 END CASE;
END; $$;

CREATE FUNCTION public.privacy_restore_apply_hardened_operation(p_run_id uuid,p_operation_code text)
RETURNS bigint LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE imported privacy_protected.restore_ledger_imports%ROWTYPE;changed bigint:=0;n bigint:=0;
BEGIN
 SELECT imported_row.* INTO imported FROM privacy_protected.restore_replay_runs run JOIN privacy_protected.restore_ledger_imports imported_row ON imported_row.id=run.import_id WHERE run.id=p_run_id FOR UPDATE OF run;
 IF imported.id IS NULL OR imported.erasure_effective_at IS NULL THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_operation_rejected'; END IF;
 IF p_operation_code='PROVIDER_LOCAL_FENCE' THEN
  DELETE FROM privacy_protected.provider_credential_quarantine q USING privacy_protected.provider_targets target,privacy_protected.provider_capture_sets capture
   WHERE q.target_id=target.id AND target.execution_id=capture.execution_id AND capture.subject_user_id=imported.subject_user_id; GET DIAGNOSTICS changed=ROW_COUNT;
  DELETE FROM privacy_protected.provider_connections WHERE subject_user_id=imported.subject_user_id; GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
 ELSIF p_operation_code='IDENTITY_CLEAR' THEN
  IF EXISTS(SELECT 1 FROM sessions WHERE NOT subject_indexed AND expiry>clock_timestamp()) OR EXISTS(SELECT 1 FROM users WHERE guardian_id=imported.subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_operation_rejected'; END IF;
  DELETE FROM sessions WHERE subject_indexed AND user_id=imported.subject_user_id;
  DELETE FROM email_verification_tokens WHERE user_id=imported.subject_user_id;
  DELETE FROM password_reset_tokens WHERE user_id=imported.subject_user_id;
  DELETE FROM user_platform_roles WHERE user_id=imported.subject_user_id;
  UPDATE users SET name='Conta eliminada',email=NULL,email_verified_at=NULL,minor_login_id=NULL,password_hash=NULL,guardian_id=NULL,is_dependent=false,
   date_of_birth=DATE '1900-01-01',is_active=false,leaderboard_visible=false,credential_version=credential_version+1,
   erased_at=imported.erasure_effective_at,erasure_execution_id=NULL,erasure_replay_run_id=p_run_id,updated_at=clock_timestamp()
  WHERE id=imported.subject_user_id AND erased_at IS NULL; GET DIAGNOSTICS changed=ROW_COUNT;
 ELSE changed:=public.privacy_restore_apply_relational_operation(p_run_id,imported.subject_user_id,imported.erasure_effective_at,p_operation_code);
 END IF;
 PERFORM public.privacy_restore_verify_operation(p_run_id,p_operation_code,false);
 RETURN changed;
END; $$;

CREATE FUNCTION public.privacy_restore_begin_replay_hardened(p_import_id uuid,p_worker_ref uuid)
RETURNS TABLE(run_id uuid,outcome_code text) LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE imported privacy_protected.restore_ledger_imports%ROWTYPE;run_ref uuid;operation text;verification bytea;existing_outcome text;
BEGIN
 SELECT * INTO imported FROM privacy_protected.restore_ledger_imports WHERE id=p_import_id;
 IF imported.id IS NULL OR imported.erasure_effective_at IS NULL THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_replay_rejected'; END IF;
 run_ref:=public.privacy_restore_begin_replay(p_import_id,p_worker_ref);
 SELECT run.outcome_code INTO existing_outcome FROM privacy_protected.restore_replay_runs run WHERE run.id=run_ref FOR UPDATE;
 IF existing_outcome IS NOT NULL THEN RETURN QUERY SELECT run_ref,existing_outcome; RETURN; END IF;
 IF EXISTS(SELECT 1 FROM users WHERE id=imported.subject_user_id AND erased_at IS NOT NULL) THEN
  IF NOT EXISTS(SELECT 1 FROM users WHERE id=imported.subject_user_id AND erasure_execution_id=imported.source_execution_id
   AND erasure_replay_run_id IS NULL AND erased_at=imported.erasure_effective_at) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_source_conflict'; END IF;
  FOREACH operation IN ARRAY imported.operations LOOP PERFORM public.privacy_restore_verify_operation(run_ref,operation,true); END LOOP;
  verification:=digest(convert_to(array_to_string(imported.operations,':')||':ALREADY_APPLIED','UTF8'),'sha256');
  UPDATE privacy_protected.restore_replay_checkpoints SET status='SUCCEEDED',affected_rows=0,
   result_sha256=digest(convert_to(operation_position::text||':'||operation_code||':ALREADY_APPLIED','UTF8'),'sha256'),completed_at=clock_timestamp()
   WHERE restore_replay_checkpoints.run_id=run_ref AND status='PENDING';
  INSERT INTO privacy_protected.restore_replay_already_applied_evidence(run_id,import_id,evidence_code,source_execution_id,
   erasure_effective_at,verified_operations,verification_sha256)
  SELECT run_ref,imported.id,'ALREADY_APPLIED',imported.source_execution_id,imported.erasure_effective_at,imported.operations,verification
   FROM users WHERE users.id=imported.subject_user_id;
  UPDATE privacy_protected.restore_replay_runs SET status='SUCCEEDED',outcome_code='ALREADY_APPLIED_SOURCE',completed_at=clock_timestamp() WHERE id=run_ref;
  existing_outcome:='ALREADY_APPLIED_SOURCE';
 END IF;
 RETURN QUERY SELECT run_ref,existing_outcome;
END; $$;

CREATE OR REPLACE FUNCTION public.privacy_restore_execute_checkpoint(
 p_run_id uuid,p_worker_ref uuid,p_operation_position smallint,p_operation_code text,p_action_version text,p_prescription_sha256 bytea
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE run_row privacy_protected.restore_replay_runs%ROWTYPE;imported privacy_protected.restore_ledger_imports%ROWTYPE;
 checkpoint_row privacy_protected.restore_replay_checkpoints%ROWTYPE;changed bigint;result_digest bytea;
BEGIN
 SELECT * INTO run_row FROM privacy_protected.restore_replay_runs WHERE id=p_run_id FOR UPDATE;
 SELECT * INTO imported FROM privacy_protected.restore_ledger_imports WHERE id=run_row.import_id;
 SELECT * INTO checkpoint_row FROM privacy_protected.restore_replay_checkpoints WHERE run_id=p_run_id AND operation_position=p_operation_position FOR UPDATE;
 IF run_row.id IS NULL OR imported.id IS NULL OR imported.erasure_effective_at IS NULL OR checkpoint_row.id IS NULL OR run_row.worker_ref<>p_worker_ref
  OR run_row.status NOT IN ('PENDING','SUCCEEDED') OR checkpoint_row.operation_code<>p_operation_code OR checkpoint_row.action_version<>p_action_version
  OR imported.prescription_sha256<>p_prescription_sha256 OR imported.action_version<>p_action_version OR imported.operations[p_operation_position]<>p_operation_code
  OR NOT(public.privacy_relational_replay_operation_supported(p_operation_code) OR p_operation_code='PROVIDER_LOCAL_FENCE')
  OR EXISTS(SELECT 1 FROM privacy_protected.restore_replay_checkpoints prior WHERE prior.run_id=p_run_id AND prior.operation_position<p_operation_position AND prior.status<>'SUCCEEDED')
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_checkpoint_rejected'; END IF;
 IF checkpoint_row.status='SUCCEEDED' THEN RETURN checkpoint_row.id; END IF;
 changed:=public.privacy_restore_apply_hardened_operation(p_run_id,p_operation_code);
 result_digest:=digest(convert_to(p_operation_position::text||':'||p_operation_code||':'||changed::text,'UTF8'),'sha256');
 UPDATE privacy_protected.restore_replay_checkpoints SET status='SUCCEEDED',affected_rows=changed,result_sha256=result_digest,completed_at=clock_timestamp() WHERE id=checkpoint_row.id;
 IF NOT EXISTS(SELECT 1 FROM privacy_protected.restore_replay_checkpoints WHERE run_id=p_run_id AND status<>'SUCCEEDED') THEN
  UPDATE privacy_protected.restore_replay_runs SET status='SUCCEEDED',outcome_code='REPLAYED',completed_at=clock_timestamp() WHERE id=p_run_id;
 END IF;
 RETURN checkpoint_row.id;
END; $$;

CREATE FUNCTION public.privacy_restore_record_inventory_attestation(p_input_source text,p_inventory_sha256 bytea,p_schema_migration_digest bytea,
 p_policy_version text,p_executor_version text,p_plan_schema_version text,p_image_digest text,p_run_ids uuid[],
 p_object_count integer,p_imported_count integer,p_replayed_count integer,p_already_applied_count integer,
 p_absence_verified_count integer,p_synthetic_replayed_count integer,p_closure_v3_count integer,p_intent_only_count integer,
 p_legacy_closure_v2_count integer,p_erasure_effective_at_verified_count integer)
RETURNS bytea LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE attestation_ref uuid;evidence bytea;run_ref uuid;operation text;source_mode boolean;
 existing privacy_protected.restore_replay_inventory_attestations%ROWTYPE;
BEGIN
 IF p_input_source NOT IN ('LIVE_LEDGER','SYNTHETIC_BOOTSTRAP') OR octet_length(p_inventory_sha256)<>32 OR octet_length(p_schema_migration_digest)<>32
  OR p_policy_version!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$' OR p_executor_version!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'
  OR p_plan_schema_version!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$' OR p_image_digest!~'^sha256:[0-9a-f]{64}$' OR p_object_count<1
  OR p_replayed_count<1 OR cardinality(p_run_ids)<>p_replayed_count
  OR cardinality(p_run_ids)<>(SELECT count(DISTINCT listed.run_id) FROM unnest(p_run_ids) AS listed(run_id))
  OR p_replayed_count<>p_imported_count+p_already_applied_count OR p_absence_verified_count<>p_replayed_count
  OR (p_input_source='LIVE_LEDGER' AND p_synthetic_replayed_count<>0) OR (p_input_source='SYNTHETIC_BOOTSTRAP' AND p_synthetic_replayed_count<>p_replayed_count)
  OR p_closure_v3_count<>p_replayed_count OR p_intent_only_count<>0 OR p_legacy_closure_v2_count<>0
  OR p_erasure_effective_at_verified_count<>p_replayed_count
  OR EXISTS(SELECT 1 FROM unnest(p_run_ids) AS listed(run_id)
   LEFT JOIN privacy_protected.restore_replay_runs run ON run.id=listed.run_id
   LEFT JOIN privacy_protected.restore_ledger_imports imported ON imported.id=run.import_id
   WHERE run.status<>'SUCCEEDED' OR imported.closure_version<>'restore-tombstone-closure/v3'
    OR (p_input_source='LIVE_LEDGER')<>(imported.synthetic_fixture IS NULL))
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_attestation_rejected'; END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended(p_input_source||':'||encode(p_inventory_sha256,'hex'),0));
 FOREACH run_ref IN ARRAY p_run_ids LOOP
  SELECT outcome_code='ALREADY_APPLIED_SOURCE' INTO source_mode FROM privacy_protected.restore_replay_runs WHERE id=run_ref;
  FOR operation IN SELECT checkpoint.operation_code FROM privacy_protected.restore_replay_checkpoints checkpoint WHERE checkpoint.run_id=run_ref ORDER BY checkpoint.operation_position LOOP
   PERFORM public.privacy_restore_verify_operation(run_ref,operation,source_mode);
  END LOOP;
 END LOOP;
 SELECT digest(convert_to(string_agg(encode(checkpoint.result_sha256,'hex'),':' ORDER BY encode(checkpoint.result_sha256,'hex')),'UTF8'),'sha256') INTO evidence
 FROM privacy_protected.restore_replay_checkpoints checkpoint WHERE checkpoint.run_id=ANY(p_run_ids);
 SELECT * INTO existing FROM privacy_protected.restore_replay_inventory_attestations
 WHERE input_source=p_input_source AND inventory_sha256=p_inventory_sha256;
 IF existing.id IS NOT NULL THEN
  IF ROW(existing.schema_migration_digest,existing.policy_version,existing.executor_version,existing.plan_schema_version,existing.image_digest,
    existing.object_count,existing.imported_count,existing.replayed_count,existing.already_applied_count,existing.absence_verified_count,
    existing.synthetic_replayed_count,existing.closure_v3_count,existing.intent_only_count,existing.legacy_closure_v2_count,
    existing.erasure_effective_at_verified_count,existing.evidence_sha256)
   IS DISTINCT FROM ROW(p_schema_migration_digest,p_policy_version,p_executor_version,p_plan_schema_version,p_image_digest,
    p_object_count,p_imported_count,p_replayed_count,p_already_applied_count,p_absence_verified_count,p_synthetic_replayed_count,
    p_closure_v3_count,p_intent_only_count,p_legacy_closure_v2_count,p_erasure_effective_at_verified_count,evidence)
   OR EXISTS(SELECT link.run_id FROM privacy_protected.restore_replay_inventory_attestation_runs link WHERE link.attestation_id=existing.id
    EXCEPT SELECT listed.run_id FROM unnest(p_run_ids) AS listed(run_id))
   OR EXISTS(SELECT listed.run_id FROM unnest(p_run_ids) AS listed(run_id)
    EXCEPT SELECT link.run_id FROM privacy_protected.restore_replay_inventory_attestation_runs link WHERE link.attestation_id=existing.id)
  THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_attestation_conflict'; END IF;
  RETURN existing.evidence_sha256;
 END IF;
 INSERT INTO privacy_protected.restore_replay_inventory_attestations(input_source,inventory_sha256,schema_migration_digest,policy_version,executor_version,plan_schema_version,image_digest,object_count,imported_count,replayed_count,
  already_applied_count,absence_verified_count,synthetic_replayed_count,closure_v3_count,intent_only_count,legacy_closure_v2_count,
  erasure_effective_at_verified_count,evidence_sha256)
 VALUES(p_input_source,p_inventory_sha256,p_schema_migration_digest,p_policy_version,p_executor_version,p_plan_schema_version,p_image_digest,
  p_object_count,p_imported_count,p_replayed_count,p_already_applied_count,p_absence_verified_count,p_synthetic_replayed_count,
  p_closure_v3_count,p_intent_only_count,p_legacy_closure_v2_count,p_erasure_effective_at_verified_count,evidence)
 RETURNING id INTO attestation_ref;
 INSERT INTO privacy_protected.restore_replay_inventory_attestation_runs(attestation_id,run_id)
 SELECT attestation_ref,listed.run_id FROM unnest(p_run_ids) AS listed(run_id);
 RETURN evidence;
END; $$;

CREATE FUNCTION public.privacy_restore_observe_inventory(p_input_source text,p_inventory_sha256 bytea,p_schema_migration_digest bytea,
 p_policy_version text,p_executor_version text,p_plan_schema_version text,p_image_digest text)
RETURNS TABLE(replay_count integer,source_already_applied_count integer,synthetic_count integer,verified_run_count integer,
 expected_checkpoint_count integer,succeeded_checkpoint_count integer,provider_absent_count integer,consent_clock_verified_count integer,
 closure_v3_count integer,erasure_effective_at_verified_count integer,evidence_sha256 bytea)
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT attestation.replayed_count,
  count(DISTINCT run.id) FILTER(WHERE run.outcome_code='ALREADY_APPLIED_SOURCE')::integer,
  count(DISTINCT run.id) FILTER(WHERE imported.synthetic_fixture='mycfc/privacy-restore-synthetic-fixture/v1')::integer,
  count(DISTINCT run.id) FILTER(WHERE cardinality(imported.operations)=(SELECT count(*) FROM privacy_protected.restore_replay_checkpoints exact_checkpoint WHERE exact_checkpoint.run_id=run.id)
   AND NOT EXISTS(SELECT 1 FROM privacy_protected.restore_replay_checkpoints failed_checkpoint WHERE failed_checkpoint.run_id=run.id AND failed_checkpoint.status<>'SUCCEEDED'))::integer,
  count(checkpoint.*)::integer,
  count(checkpoint.*) FILTER(WHERE checkpoint.status='SUCCEEDED')::integer,
  count(DISTINCT run.id) FILTER(WHERE 'PROVIDER_LOCAL_FENCE'=ANY(imported.operations)
   AND NOT EXISTS(SELECT 1 FROM privacy_protected.provider_connections connection WHERE connection.subject_user_id=imported.subject_user_id)
   AND NOT EXISTS(SELECT 1 FROM privacy_protected.provider_credential_quarantine quarantine
    JOIN privacy_protected.provider_targets target ON target.id=quarantine.target_id
    JOIN privacy_protected.provider_capture_sets capture ON capture.execution_id=target.execution_id
    WHERE capture.subject_user_id=imported.subject_user_id))::integer,
  count(DISTINCT run.id) FILTER(WHERE 'IDENTITY_CLEAR'=ANY(imported.operations)
   AND EXISTS(SELECT 1 FROM users WHERE users.id=imported.subject_user_id AND users.erased_at=imported.erasure_effective_at)
   AND NOT EXISTS(SELECT 1 FROM consent_forms consent WHERE consent.user_id=imported.subject_user_id AND consent.ceased_at IS NULL)
   AND NOT EXISTS(SELECT 1 FROM consent_forms consent WHERE consent.user_id=imported.subject_user_id AND consent.cessation_reason='ACCOUNT_ERASURE'
    AND (consent.ceased_at<>imported.erasure_effective_at OR consent.evidence_expires_at<>imported.erasure_effective_at+interval '3 years')))::integer,
  count(DISTINCT run.id) FILTER(WHERE imported.closure_version='restore-tombstone-closure/v3')::integer,
  count(DISTINCT run.id) FILTER(WHERE EXISTS(SELECT 1 FROM users WHERE users.id=imported.subject_user_id AND users.erased_at=imported.erasure_effective_at)
   AND NOT EXISTS(SELECT 1 FROM consent_forms consent WHERE consent.user_id=imported.subject_user_id AND consent.ceased_at IS NULL)
   AND NOT EXISTS(SELECT 1 FROM consent_forms consent WHERE consent.user_id=imported.subject_user_id AND consent.cessation_reason='ACCOUNT_ERASURE'
    AND (consent.ceased_at<>imported.erasure_effective_at OR consent.evidence_expires_at<>imported.erasure_effective_at+interval '3 years')))::integer,
  attestation.evidence_sha256
 FROM privacy_protected.restore_replay_inventory_attestations attestation
 JOIN privacy_protected.restore_replay_inventory_attestation_runs link ON link.attestation_id=attestation.id
 JOIN privacy_protected.restore_replay_runs run ON run.id=link.run_id
 JOIN privacy_protected.restore_ledger_imports imported ON imported.id=run.import_id
 JOIN privacy_protected.restore_replay_checkpoints checkpoint ON checkpoint.run_id=run.id
 WHERE attestation.input_source=p_input_source AND attestation.inventory_sha256=p_inventory_sha256
  AND attestation.schema_migration_digest=p_schema_migration_digest AND attestation.policy_version=p_policy_version
  AND attestation.executor_version=p_executor_version AND attestation.plan_schema_version=p_plan_schema_version AND attestation.image_digest=p_image_digest
 GROUP BY attestation.id,attestation.replayed_count,attestation.evidence_sha256
 HAVING count(DISTINCT run.id)=attestation.replayed_count;
$$;

REVOKE ALL ON TABLE privacy_protected.restore_replay_already_applied_evidence,privacy_protected.restore_synthetic_fixtures,
 privacy_protected.restore_replay_inventory_attestations,privacy_protected.restore_replay_inventory_attestation_runs FROM PUBLIC;
REVOKE ALL ON FUNCTION public.privacy_restore_create_synthetic_fixture(uuid),
 public.privacy_tombstone_prepare_closure_v3(uuid,uuid),public.privacy_tombstone_confirm_closure_v3(uuid,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz),
 public.privacy_restore_import_authenticated_v2_hardened(uuid,text,text,text,text,text,bytea,bytea,text,timestamptz,timestamptz,timestamptz,uuid,uuid,uuid,uuid,bytea,bytea,timestamptz,timestamptz,text,text,text,text,text[],bytea,bytea),
 public.privacy_restore_verify_operation(uuid,text,boolean),public.privacy_restore_apply_hardened_operation(uuid,text),
 public.privacy_restore_begin_replay_hardened(uuid,uuid),public.privacy_restore_record_inventory_attestation(text,bytea,bytea,text,text,text,text,uuid[],integer,integer,integer,integer,integer,integer,integer,integer,integer,integer),
 public.privacy_restore_observe_inventory(text,bytea,bytea,text,text,text,text) FROM PUBLIC;

-- #248 release guard: fail the privacy erasure executor closed at the
-- database boundary and bind
-- activation to authenticated, release-specific evidence. The switch starts
-- engaged deliberately; only a distinct approval of a complete current
-- evidence set may clear it.

ALTER TABLE privacy_activation_evidence DROP CONSTRAINT privacy_activation_evidence_check;
ALTER TABLE privacy_activation_evidence ADD CONSTRAINT privacy_activation_evidence_expiry_bounded
 CHECK(expires_at>observed_at AND expires_at<=observed_at+interval '2160 hours') NOT VALID;

CREATE TABLE privacy_worker_kill_switch (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
 engaged boolean NOT NULL DEFAULT true,
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 activation_approval_id uuid NULL REFERENCES privacy_activation_approvals(id) ON DELETE RESTRICT,
 changed_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
INSERT INTO privacy_worker_kill_switch(singleton,engaged) VALUES(true,true);

CREATE TABLE privacy_worker_kill_switch_events (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 version bigint NOT NULL UNIQUE CHECK(version>0),
 engaged boolean NOT NULL,
 activation_approval_id uuid NULL REFERENCES privacy_activation_approvals(id) ON DELETE RESTRICT,
 occurred_at timestamptz NOT NULL
);
INSERT INTO privacy_worker_kill_switch_events(version,engaged,occurred_at) VALUES(1,true,clock_timestamp());
CREATE TRIGGER privacy_worker_kill_switch_events_immutable BEFORE UPDATE OR DELETE ON privacy_worker_kill_switch_events
 FOR EACH ROW EXECUTE FUNCTION prevent_privacy_audit_mutation();

CREATE TABLE privacy_activation_authenticated_artifacts (
 evidence_id uuid PRIMARY KEY REFERENCES privacy_activation_evidence(id) ON DELETE RESTRICT,
 kind text NOT NULL CHECK(kind IN ('RESTORE','INFRASTRUCTURE','PROVIDER','SCHEMA')),
 policy_version text NOT NULL REFERENCES privacy_request_policies(version) ON DELETE RESTRICT,
 executor_version text NOT NULL CHECK(executor_version='privacy-erasure-executor/v2'),
 plan_schema_version text NOT NULL CHECK(plan_schema_version='privacy-erasure-plan/v2'),
 image_digest text NOT NULL CHECK(image_digest~'^sha256:[0-9a-f]{64}$'),
 immutable_evidence_ref text NULL,
 immutable_evidence_sha256 bytea NOT NULL CHECK(octet_length(immutable_evidence_sha256)=32),
 signing_key_id text NULL,
 schema_migration_digest bytea NULL CHECK(schema_migration_digest IS NULL OR octet_length(schema_migration_digest)=32),
 baseline_includes_through text NULL,
 production_state_serial bigint NULL,
 hetzner_state_serial bigint NULL,
 production_state_sha256 bytea NULL,
 hetzner_state_sha256 bytea NULL,
 production_plan_sha256 bytea NULL,
 hetzner_plan_sha256 bytea NULL,
 worker_identity_enabled boolean NULL,
 s3_version_deletion_enabled boolean NULL,
 ledger_broker_invoke_enabled boolean NULL,
 worker_monitoring_enabled boolean NULL,
 restore_infrastructure_enabled boolean NULL,
 restore_ledger_write_enabled boolean NULL,
 provider_registry_state text NULL,
 provider_registration_count bigint NULL,
 provider_registry_sha256 bytea NULL,
 restore_input_source text NULL,
 restore_input_contract text NULL,
 restore_replay_contract text NULL,
 restore_closure_contract text NULL,
 restore_candidate_sha256 bytea NULL,
 restore_inventory_sha256 bytea NULL,
 restore_object_count bigint NULL,
 restore_replayed_count bigint NULL,
 restore_synthetic_count bigint NULL,
 restore_observer_sha256 bytea NULL,
 authenticated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(evidence_id,kind),
 CHECK(((kind='RESTORE' AND signing_key_id IS NULL AND immutable_evidence_ref IS NOT NULL
        AND schema_migration_digest IS NOT NULL AND baseline_includes_through IS NULL
        AND restore_input_source IN ('LIVE_LEDGER','SYNTHETIC_BOOTSTRAP') AND restore_input_contract='mycfc/privacy-restore-ledger-input/v2'
        AND restore_replay_contract='relational-erasure-replay/v1' AND restore_closure_contract='restore-tombstone-closure/v3'
        AND restore_candidate_sha256 IS NOT NULL AND octet_length(restore_candidate_sha256)=32
        AND restore_inventory_sha256 IS NOT NULL AND octet_length(restore_inventory_sha256)=32
        AND restore_observer_sha256 IS NOT NULL AND octet_length(restore_observer_sha256)=32
        AND restore_object_count>=restore_replayed_count AND restore_replayed_count>0
        AND ((restore_input_source='LIVE_LEDGER' AND restore_synthetic_count=0)
          OR (restore_input_source='SYNTHETIC_BOOTSTRAP' AND restore_synthetic_count=restore_replayed_count)))
    OR (kind='INFRASTRUCTURE' AND signing_key_id IS NOT NULL AND immutable_evidence_ref IS NOT NULL
        AND production_state_serial>0 AND hetzner_state_serial>0
        AND octet_length(production_state_sha256)=32 AND octet_length(hetzner_state_sha256)=32
        AND octet_length(production_plan_sha256)=32 AND octet_length(hetzner_plan_sha256)=32
        AND worker_identity_enabled AND s3_version_deletion_enabled AND ledger_broker_invoke_enabled
        AND worker_monitoring_enabled AND restore_infrastructure_enabled AND restore_ledger_write_enabled)
    OR (kind='PROVIDER' AND signing_key_id IS NOT NULL AND immutable_evidence_ref IS NOT NULL
        AND provider_registry_state='READY' AND provider_registration_count>0 AND octet_length(provider_registry_sha256)=32)
    OR (kind='SCHEMA' AND signing_key_id IS NOT NULL AND immutable_evidence_ref IS NOT NULL
        AND octet_length(schema_migration_digest)=32
        AND baseline_includes_through='202609100013_privacy_worker_release_guard')) IS TRUE)
);
CREATE TRIGGER privacy_activation_authenticated_artifacts_immutable BEFORE UPDATE OR DELETE ON privacy_activation_authenticated_artifacts
 FOR EACH ROW EXECUTE FUNCTION prevent_privacy_audit_mutation();

-- Existing evidence was accepted without signature and release bindings. It is
-- retained as audit history but cannot be used by the v2 activation contract.
CREATE OR REPLACE FUNCTION privacy_activation_record_evidence(p_actor uuid,p_kind text,p_evidence_sha256 bytea,p_reference_code text,p_observed_at timestamptz)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
 RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_activation_authenticated_evidence_required';
END;$$;

-- Correct the restore verifier without weakening historical preservation.
-- Active membership revocation is an effective-date boundary: strictly
-- historical rows remain until the following anonymization operation. The
-- history verifier then requires every direct subject link and every unique
-- subject token in variation text/JSON to be gone. This same verifier is used
-- for ordinary replay and the already-applied proof path.
ALTER FUNCTION privacy_restore_verify_operation(uuid,text,boolean) RENAME TO privacy_restore_verify_operation_inner_013;
CREATE OR REPLACE FUNCTION privacy_restore_verify_operation(p_run_id uuid,p_operation_code text,p_source_already_applied boolean)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE imported privacy_protected.restore_ledger_imports%ROWTYPE;subject_email text;subject_login text;
BEGIN
 IF p_operation_code NOT IN ('MEMBERSHIP_ACTIVE_REVOKE','MEMBERSHIP_HISTORY_ANONYMIZE') THEN
  PERFORM privacy_restore_verify_operation_inner_013(p_run_id,p_operation_code,p_source_already_applied);
  RETURN;
 END IF;
 SELECT imported_row.* INTO imported FROM privacy_protected.restore_replay_runs run
 JOIN privacy_protected.restore_ledger_imports imported_row ON imported_row.id=run.import_id WHERE run.id=p_run_id;
 IF imported.id IS NULL OR imported.kind<>'closure' OR imported.closure_version<>'restore-tombstone-closure/v3'
  OR NOT(p_operation_code=ANY(imported.operations)) THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 IF p_source_already_applied AND NOT EXISTS(SELECT 1 FROM users WHERE id=imported.subject_user_id
  AND erasure_execution_id=imported.source_execution_id AND erased_at=imported.erasure_effective_at) THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 SELECT email::text,minor_login_id INTO subject_email,subject_login FROM users WHERE id=imported.subject_user_id;
 IF p_operation_code='MEMBERSHIP_ACTIVE_REVOKE' THEN
  IF EXISTS(SELECT 1 FROM user_memberships WHERE user_id=imported.subject_user_id
   AND (starts_on>=imported.erasure_effective_at::date OR ends_on IS NULL OR ends_on>=imported.erasure_effective_at::date)) THEN
   RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 ELSE
  IF EXISTS(SELECT 1 FROM user_memberships WHERE user_id=imported.subject_user_id)
   OR EXISTS(SELECT 1 FROM training_variations variation
    WHERE variation.change_summary IS DISTINCT FROM privacy_scrub_audit_text(variation.change_summary,imported.subject_user_id,NULL,subject_email,subject_login)
     OR variation.patch IS DISTINCT FROM privacy_scrub_audit_json(variation.patch,imported.subject_user_id,NULL,subject_email,subject_login)) THEN
   RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 END IF;
END;$$;

-- Reject intent and legacy closure inventories before a replay run or a
-- checkpoint can be created/mutated. Older formats remain readable only.
ALTER FUNCTION privacy_restore_begin_replay_hardened(uuid,uuid) RENAME TO privacy_restore_begin_replay_hardened_inner_013;
CREATE OR REPLACE FUNCTION privacy_restore_begin_replay_hardened(p_import_id uuid,p_worker_ref uuid)
RETURNS TABLE(run_id uuid,outcome_code text) LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM privacy_protected.restore_ledger_imports imported
  WHERE imported.id=p_import_id AND imported.kind='closure' AND imported.closure_version='restore-tombstone-closure/v3') THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_replay_rejected'; END IF;
 RETURN QUERY SELECT * FROM privacy_restore_begin_replay_hardened_inner_013(p_import_id,p_worker_ref);
END;$$;
ALTER FUNCTION privacy_restore_execute_checkpoint(uuid,uuid,smallint,text,text,bytea) RENAME TO privacy_restore_execute_checkpoint_inner_013;
CREATE OR REPLACE FUNCTION privacy_restore_execute_checkpoint(p_run_id uuid,p_worker_ref uuid,p_operation_position smallint,p_operation_code text,p_action_version text,p_prescription_sha256 bytea)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM privacy_protected.restore_replay_runs run
  JOIN privacy_protected.restore_ledger_imports imported ON imported.id=run.import_id
  WHERE run.id=p_run_id AND imported.kind='closure' AND imported.closure_version='restore-tombstone-closure/v3') THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_checkpoint_rejected'; END IF;
 RETURN privacy_restore_execute_checkpoint_inner_013(p_run_id,p_worker_ref,p_operation_position,p_operation_code,p_action_version,p_prescription_sha256);
END;$$;
REVOKE ALL ON FUNCTION privacy_restore_verify_operation_inner_013(uuid,text,boolean),privacy_restore_begin_replay_hardened_inner_013(uuid,uuid),
 privacy_restore_execute_checkpoint_inner_013(uuid,uuid,smallint,text,text,bytea),privacy_restore_verify_operation(uuid,text,boolean),
 privacy_restore_begin_replay_hardened(uuid,uuid),privacy_restore_execute_checkpoint(uuid,uuid,smallint,text,text,bytea) FROM PUBLIC;

-- Retain the reviewed implementations as owner-only inner routines. The
-- public names become narrow guard wrappers so a stale executor credential
-- cannot bypass a disabled/expired activation. A disabled worker cannot renew
-- or complete an existing lease; the lease remains recoverable and expires by
-- its original clock.
ALTER FUNCTION privacy_upload_cleanup_claim(bigint,uuid) RENAME TO privacy_upload_cleanup_claim_inner_013;
ALTER FUNCTION privacy_upload_cleanup_complete(uuid,uuid,bigint,uuid,integer,integer,integer,integer,text,bytea) RENAME TO privacy_upload_cleanup_complete_inner_013;
ALTER FUNCTION privacy_upload_cleanup_fail(uuid,uuid,bigint,uuid,boolean,bigint) RENAME TO privacy_upload_cleanup_fail_inner_013;
ALTER FUNCTION privacy_worker_claim(bigint,uuid) RENAME TO privacy_worker_claim_inner_013;
ALTER FUNCTION privacy_worker_heartbeat(uuid,uuid,uuid,bigint,uuid,bigint) RENAME TO privacy_worker_heartbeat_inner_013;
ALTER FUNCTION privacy_worker_complete_job(uuid,uuid,uuid,bigint,uuid) RENAME TO privacy_worker_complete_job_inner_013;
ALTER FUNCTION privacy_worker_fail_job(uuid,uuid,uuid,bigint,uuid,text,bigint,text,text,bytea) RENAME TO privacy_worker_fail_job_inner_013;
ALTER FUNCTION privacy_worker_sync(uuid,uuid,uuid,bigint,uuid) RENAME TO privacy_worker_sync_inner_013;
ALTER FUNCTION privacy_worker_execute_checkpoint(uuid,uuid,uuid,bigint,uuid,text,text) RENAME TO privacy_worker_execute_checkpoint_inner_013;
ALTER FUNCTION privacy_worker_list_object_targets(uuid,uuid,uuid,bigint,uuid) RENAME TO privacy_worker_list_object_targets_inner_013;
ALTER FUNCTION privacy_worker_record_object_evidence(uuid,uuid,uuid,uuid,bigint,uuid,integer,integer,integer,integer,text,bytea) RENAME TO privacy_worker_record_object_evidence_inner_013;
ALTER FUNCTION privacy_worker_complete_object_checkpoint(uuid,uuid,uuid,bigint,uuid) RENAME TO privacy_worker_complete_object_checkpoint_inner_013;
ALTER FUNCTION privacy_tombstone_prepare_v2(uuid,uuid,uuid,bigint,uuid) RENAME TO privacy_tombstone_prepare_v2_inner_013;
ALTER FUNCTION privacy_tombstone_confirm_v2(uuid,uuid,uuid,bigint,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz) RENAME TO privacy_tombstone_confirm_v2_inner_013;
ALTER FUNCTION privacy_tombstone_prepare_closure_v2(uuid,uuid) RENAME TO privacy_tombstone_prepare_closure_v2_inner_013;
ALTER FUNCTION privacy_tombstone_confirm_closure_v2(uuid,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz) RENAME TO privacy_tombstone_confirm_closure_v2_inner_013;
ALTER FUNCTION privacy_tombstone_prepare_closure_v3(uuid,uuid) RENAME TO privacy_tombstone_prepare_closure_v3_inner_013;
ALTER FUNCTION privacy_tombstone_confirm_closure_v3(uuid,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz) RENAME TO privacy_tombstone_confirm_closure_v3_inner_013;
ALTER FUNCTION privacy_worker_list_provider_targets(uuid,uuid,uuid,bigint,uuid) RENAME TO privacy_worker_list_provider_targets_inner_013;
ALTER FUNCTION privacy_worker_record_provider_evidence(uuid,uuid,uuid,uuid,bigint,uuid,text,integer,text,text,text,text,text,text,text,bytea) RENAME TO privacy_worker_record_provider_evidence_inner_013;
ALTER FUNCTION privacy_worker_complete_provider_checkpoint(uuid,uuid,uuid,bigint,uuid) RENAME TO privacy_worker_complete_provider_checkpoint_inner_013;
ALTER FUNCTION privacy_completion_prepare(uuid,uuid) RENAME TO privacy_completion_prepare_inner_013;
ALTER FUNCTION privacy_completion_list_pending(uuid,integer) RENAME TO privacy_completion_list_pending_inner_013;
ALTER FUNCTION privacy_completion_finalize(uuid,uuid,bytea,bytea) RENAME TO privacy_completion_finalize_inner_013;

CREATE OR REPLACE FUNCTION privacy_upload_cleanup_claim(p_lease_milliseconds bigint,p_worker_ref uuid)
RETURNS TABLE(intent_id uuid,lease_epoch bigint,attempt_id uuid,attempt_count integer,subject_user_id uuid,provenance_actor_user_id uuid,
 source_kind text,source_ref uuid,service_code text,target_kind text,provider_contract_version text,envelope_version text,algorithm text,
 encryption_key_id text,encapsulation bytea,nonce bytea,ciphertext bytea,content_type text,size_bytes bigint,cleanup_after timestamptz,created_at timestamptz)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN QUERY SELECT * FROM privacy_upload_cleanup_claim_inner_013(p_lease_milliseconds,p_worker_ref); END;$$;
CREATE OR REPLACE FUNCTION privacy_upload_cleanup_complete(p_intent_id uuid,p_attempt_id uuid,p_lease_epoch bigint,p_worker_ref uuid,
 p_deleted_versions integer,p_deleted_markers integer,p_list_calls integer,p_stable_checks integer,p_transcript_key_id text,p_transcript_digest bytea)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); PERFORM privacy_upload_cleanup_complete_inner_013(p_intent_id,p_attempt_id,p_lease_epoch,p_worker_ref,p_deleted_versions,p_deleted_markers,p_list_calls,p_stable_checks,p_transcript_key_id,p_transcript_digest); END;$$;
CREATE OR REPLACE FUNCTION privacy_upload_cleanup_fail(p_intent_id uuid,p_attempt_id uuid,p_lease_epoch bigint,p_worker_ref uuid,p_retryable boolean,p_retry_delay_milliseconds bigint)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); PERFORM privacy_upload_cleanup_fail_inner_013(p_intent_id,p_attempt_id,p_lease_epoch,p_worker_ref,p_retryable,p_retry_delay_milliseconds); END;$$;

CREATE OR REPLACE FUNCTION privacy_worker_claim(p_lease_milliseconds bigint,p_worker_ref uuid)
RETURNS TABLE(job_id uuid,lease_id uuid,attempt_id uuid) LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN QUERY SELECT * FROM privacy_worker_claim_inner_013(p_lease_milliseconds,p_worker_ref); END;$$;
CREATE OR REPLACE FUNCTION privacy_worker_heartbeat(p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_epoch bigint,p_worker_ref uuid,p_lease_milliseconds bigint)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN privacy_worker_heartbeat_inner_013(p_job_id,p_lease_id,p_attempt_id,p_epoch,p_worker_ref,p_lease_milliseconds); END;$$;
CREATE OR REPLACE FUNCTION privacy_worker_complete_job(p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_epoch bigint,p_worker_ref uuid)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN privacy_worker_complete_job_inner_013(p_job_id,p_lease_id,p_attempt_id,p_epoch,p_worker_ref); END;$$;
CREATE OR REPLACE FUNCTION privacy_worker_fail_job(p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_epoch bigint,p_worker_ref uuid,p_classification text,p_retry_milliseconds bigint,p_stage_code text,p_failure_code text,p_diagnostic_digest bytea)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN privacy_worker_fail_job_inner_013(p_job_id,p_lease_id,p_attempt_id,p_epoch,p_worker_ref,p_classification,p_retry_milliseconds,p_stage_code,p_failure_code,p_diagnostic_digest); END;$$;
CREATE OR REPLACE FUNCTION privacy_worker_sync(p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_epoch bigint,p_worker_ref uuid)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN privacy_worker_sync_inner_013(p_job_id,p_lease_id,p_attempt_id,p_epoch,p_worker_ref); END;$$;
CREATE OR REPLACE FUNCTION privacy_worker_execute_checkpoint(p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_lease_epoch bigint,p_worker_ref uuid,p_operation_code text,p_action_version text)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN privacy_worker_execute_checkpoint_inner_013(p_job_id,p_lease_id,p_attempt_id,p_lease_epoch,p_worker_ref,p_operation_code,p_action_version); END;$$;

CREATE OR REPLACE FUNCTION privacy_worker_list_object_targets(p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_epoch bigint,p_worker_ref uuid)
RETURNS TABLE(target_id uuid,execution_id uuid,job_id uuid,checkpoint_id uuid,plan_entry_sha256 bytea,category_key text,service_code text,target_kind text,source_kind text,source_ref uuid,operation_code text,action_version text,provider_contract_version text,envelope_version text,algorithm text,encryption_key_id text,encapsulation bytea,nonce bytea,ciphertext bytea)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN QUERY SELECT * FROM privacy_worker_list_object_targets_inner_013(p_job_id,p_lease_id,p_attempt_id,p_epoch,p_worker_ref); END;$$;
CREATE OR REPLACE FUNCTION privacy_worker_record_object_evidence(p_target_id uuid,p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_epoch bigint,p_worker_ref uuid,p_deleted_versions integer,p_deleted_markers integer,p_list_calls integer,p_stable_checks integer,p_transcript_key_id text,p_transcript_digest bytea)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN privacy_worker_record_object_evidence_inner_013(p_target_id,p_job_id,p_lease_id,p_attempt_id,p_epoch,p_worker_ref,p_deleted_versions,p_deleted_markers,p_list_calls,p_stable_checks,p_transcript_key_id,p_transcript_digest); END;$$;
CREATE OR REPLACE FUNCTION privacy_worker_complete_object_checkpoint(p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_epoch bigint,p_worker_ref uuid)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN privacy_worker_complete_object_checkpoint_inner_013(p_job_id,p_lease_id,p_attempt_id,p_epoch,p_worker_ref); END;$$;

CREATE OR REPLACE FUNCTION privacy_tombstone_prepare_v2(p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_lease_epoch bigint,p_worker_ref uuid)
RETURNS TABLE(execution_id uuid,request_id uuid,request_ref uuid,subject_user_id uuid,plan_sha256 bytea,workset_sha256 bytea,execution_started_at timestamptz,replay_operations text[])
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN QUERY SELECT * FROM privacy_tombstone_prepare_v2_inner_013(p_job_id,p_lease_id,p_attempt_id,p_lease_epoch,p_worker_ref); END;$$;
CREATE OR REPLACE FUNCTION privacy_tombstone_confirm_v2(p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_lease_epoch bigint,p_worker_ref uuid,p_ledger_version text,p_encryption_key_id text,p_locator_key_id text,p_locator_digest bytea,p_object_version_id text,p_ciphertext_sha256 bytea,p_size_bytes bigint,p_written_at timestamptz,p_verified_at timestamptz)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN privacy_tombstone_confirm_v2_inner_013(p_job_id,p_lease_id,p_attempt_id,p_lease_epoch,p_worker_ref,p_ledger_version,p_encryption_key_id,p_locator_key_id,p_locator_digest,p_object_version_id,p_ciphertext_sha256,p_size_bytes,p_written_at,p_verified_at); END;$$;
CREATE OR REPLACE FUNCTION privacy_tombstone_prepare_closure_v2(p_execution_id uuid,p_worker_ref uuid)
RETURNS TABLE(execution_id uuid,request_id uuid,request_ref uuid,subject_user_id uuid,plan_sha256 bytea,workset_sha256 bytea,execution_started_at timestamptz,closed_at timestamptz,evidence_expires_at timestamptz,replay_operations text[])
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN QUERY SELECT * FROM privacy_tombstone_prepare_closure_v2_inner_013(p_execution_id,p_worker_ref); END;$$;
CREATE OR REPLACE FUNCTION privacy_tombstone_confirm_closure_v2(p_execution_id uuid,p_worker_ref uuid,p_ledger_version text,p_encryption_key_id text,p_locator_key_id text,p_locator_digest bytea,p_object_version_id text,p_ciphertext_sha256 bytea,p_size_bytes bigint,p_written_at timestamptz,p_verified_at timestamptz)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN privacy_tombstone_confirm_closure_v2_inner_013(p_execution_id,p_worker_ref,p_ledger_version,p_encryption_key_id,p_locator_key_id,p_locator_digest,p_object_version_id,p_ciphertext_sha256,p_size_bytes,p_written_at,p_verified_at); END;$$;
CREATE OR REPLACE FUNCTION privacy_tombstone_prepare_closure_v3(p_execution_id uuid,p_worker_ref uuid)
RETURNS TABLE(execution_id uuid,request_id uuid,request_ref uuid,subject_user_id uuid,plan_sha256 bytea,workset_sha256 bytea,execution_started_at timestamptz,closed_at timestamptz,evidence_expires_at timestamptz,erasure_effective_at timestamptz,replay_operations text[])
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN QUERY SELECT * FROM privacy_tombstone_prepare_closure_v3_inner_013(p_execution_id,p_worker_ref); END;$$;
CREATE OR REPLACE FUNCTION privacy_tombstone_confirm_closure_v3(p_execution_id uuid,p_worker_ref uuid,p_ledger_version text,p_encryption_key_id text,p_locator_key_id text,p_locator_digest bytea,p_object_version_id text,p_ciphertext_sha256 bytea,p_size_bytes bigint,p_written_at timestamptz,p_verified_at timestamptz)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN privacy_tombstone_confirm_closure_v3_inner_013(p_execution_id,p_worker_ref,p_ledger_version,p_encryption_key_id,p_locator_key_id,p_locator_digest,p_object_version_id,p_ciphertext_sha256,p_size_bytes,p_written_at,p_verified_at); END;$$;

CREATE OR REPLACE FUNCTION privacy_worker_list_provider_targets(p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_epoch bigint,p_worker_ref uuid)
RETURNS TABLE(target_id uuid,execution_id uuid,job_id uuid,checkpoint_id uuid,plan_entry_sha256 bytea,category_key text,service_code text,target_kind text,provider_role text,target_version bigint,operation_code text,action_version text,provider_contract_version text,registry_evidence_key_id text,registry_evidence_digest bytea,local_state text,target_envelope_version text,target_algorithm text,target_encryption_key_id text,target_encapsulation bytea,target_nonce bytea,target_ciphertext bytea,credential_envelope_version text,credential_algorithm text,credential_encryption_key_id text,credential_encapsulation bytea,credential_nonce bytea,credential_ciphertext bytea,credential_commitment_key_id text,credential_source_commitment bytea)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN QUERY SELECT * FROM privacy_worker_list_provider_targets_inner_013(p_job_id,p_lease_id,p_attempt_id,p_epoch,p_worker_ref); END;$$;
CREATE OR REPLACE FUNCTION privacy_worker_record_provider_evidence(p_target_id uuid,p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_epoch bigint,p_worker_ref uuid,p_outcome_code text,p_adapter_attempts integer,p_evidence_code text,p_recipient_role text,p_channel_code text,p_notification_code text,p_reason_code text,p_guidance_code text,p_transcript_key_id text,p_transcript_digest bytea)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN privacy_worker_record_provider_evidence_inner_013(p_target_id,p_job_id,p_lease_id,p_attempt_id,p_epoch,p_worker_ref,p_outcome_code,p_adapter_attempts,p_evidence_code,p_recipient_role,p_channel_code,p_notification_code,p_reason_code,p_guidance_code,p_transcript_key_id,p_transcript_digest); END;$$;
CREATE OR REPLACE FUNCTION privacy_worker_complete_provider_checkpoint(p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_epoch bigint,p_worker_ref uuid)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN privacy_worker_complete_provider_checkpoint_inner_013(p_job_id,p_lease_id,p_attempt_id,p_epoch,p_worker_ref); END;$$;

CREATE OR REPLACE FUNCTION privacy_completion_prepare(p_execution_id uuid,p_worker_ref uuid) RETURNS TABLE(sealed_delivery bytea)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN QUERY SELECT * FROM privacy_completion_prepare_inner_013(p_execution_id,p_worker_ref); END;$$;
CREATE OR REPLACE FUNCTION privacy_completion_list_pending(p_worker_ref uuid,p_limit integer) RETURNS TABLE(execution_id uuid)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN QUERY SELECT * FROM privacy_completion_list_pending_inner_013(p_worker_ref,p_limit); END;$$;
CREATE OR REPLACE FUNCTION privacy_completion_finalize(p_execution_id uuid,p_worker_ref uuid,p_token_sha256 bytea,p_sealed_delivery bytea)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN PERFORM privacy_worker_require_activation(); RETURN privacy_completion_finalize_inner_013(p_execution_id,p_worker_ref,p_token_sha256,p_sealed_delivery); END;$$;

REVOKE ALL ON TABLE privacy_worker_kill_switch,privacy_worker_kill_switch_events,privacy_activation_authenticated_artifacts FROM PUBLIC;
REVOKE ALL ON FUNCTION privacy_upload_cleanup_claim_inner_013(bigint,uuid),privacy_upload_cleanup_complete_inner_013(uuid,uuid,bigint,uuid,integer,integer,integer,integer,text,bytea),
 privacy_upload_cleanup_fail_inner_013(uuid,uuid,bigint,uuid,boolean,bigint),privacy_worker_claim_inner_013(bigint,uuid),privacy_worker_heartbeat_inner_013(uuid,uuid,uuid,bigint,uuid,bigint),
 privacy_worker_complete_job_inner_013(uuid,uuid,uuid,bigint,uuid),privacy_worker_fail_job_inner_013(uuid,uuid,uuid,bigint,uuid,text,bigint,text,text,bytea),
 privacy_worker_sync_inner_013(uuid,uuid,uuid,bigint,uuid),privacy_worker_execute_checkpoint_inner_013(uuid,uuid,uuid,bigint,uuid,text,text),
 privacy_worker_list_object_targets_inner_013(uuid,uuid,uuid,bigint,uuid),privacy_worker_record_object_evidence_inner_013(uuid,uuid,uuid,uuid,bigint,uuid,integer,integer,integer,integer,text,bytea),
 privacy_worker_complete_object_checkpoint_inner_013(uuid,uuid,uuid,bigint,uuid),privacy_tombstone_prepare_v2_inner_013(uuid,uuid,uuid,bigint,uuid),
 privacy_tombstone_confirm_v2_inner_013(uuid,uuid,uuid,bigint,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz),privacy_tombstone_prepare_closure_v2_inner_013(uuid,uuid),
 privacy_tombstone_confirm_closure_v2_inner_013(uuid,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz),privacy_tombstone_prepare_closure_v3_inner_013(uuid,uuid),
 privacy_tombstone_confirm_closure_v3_inner_013(uuid,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz),privacy_worker_list_provider_targets_inner_013(uuid,uuid,uuid,bigint,uuid),
 privacy_worker_record_provider_evidence_inner_013(uuid,uuid,uuid,uuid,bigint,uuid,text,integer,text,text,text,text,text,text,text,bytea),privacy_worker_complete_provider_checkpoint_inner_013(uuid,uuid,uuid,bigint,uuid),
 privacy_completion_prepare_inner_013(uuid,uuid),privacy_completion_list_pending_inner_013(uuid,integer),privacy_completion_finalize_inner_013(uuid,uuid,bytea,bytea) FROM PUBLIC;
REVOKE ALL ON FUNCTION privacy_upload_cleanup_claim(bigint,uuid),privacy_upload_cleanup_complete(uuid,uuid,bigint,uuid,integer,integer,integer,integer,text,bytea),
 privacy_upload_cleanup_fail(uuid,uuid,bigint,uuid,boolean,bigint),privacy_worker_claim(bigint,uuid),privacy_worker_heartbeat(uuid,uuid,uuid,bigint,uuid,bigint),privacy_worker_complete_job(uuid,uuid,uuid,bigint,uuid),
 privacy_worker_fail_job(uuid,uuid,uuid,bigint,uuid,text,bigint,text,text,bytea),privacy_worker_sync(uuid,uuid,uuid,bigint,uuid),privacy_worker_execute_checkpoint(uuid,uuid,uuid,bigint,uuid,text,text),
 privacy_worker_list_object_targets(uuid,uuid,uuid,bigint,uuid),privacy_worker_record_object_evidence(uuid,uuid,uuid,uuid,bigint,uuid,integer,integer,integer,integer,text,bytea),privacy_worker_complete_object_checkpoint(uuid,uuid,uuid,bigint,uuid),
 privacy_tombstone_prepare_v2(uuid,uuid,uuid,bigint,uuid),privacy_tombstone_confirm_v2(uuid,uuid,uuid,bigint,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz),privacy_tombstone_prepare_closure_v2(uuid,uuid),
 privacy_tombstone_confirm_closure_v2(uuid,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz),privacy_tombstone_prepare_closure_v3(uuid,uuid),
 privacy_tombstone_confirm_closure_v3(uuid,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz),privacy_worker_list_provider_targets(uuid,uuid,uuid,bigint,uuid),
 privacy_worker_record_provider_evidence(uuid,uuid,uuid,uuid,bigint,uuid,text,integer,text,text,text,text,text,text,text,bytea),privacy_worker_complete_provider_checkpoint(uuid,uuid,uuid,bigint,uuid),
 privacy_completion_prepare(uuid,uuid),privacy_completion_list_pending(uuid,integer),privacy_completion_finalize(uuid,uuid,bytea,bytea) FROM PUBLIC;

-- The migration deliberately disables any older activation. This also keeps
-- the switch engaged until a v2 evidence proposal is approved.
UPDATE privacy_request_activation SET enabled=false,fulfilment_ready=false,updated_at=clock_timestamp() WHERE singleton;

CREATE OR REPLACE FUNCTION privacy_activation_authenticated_set_digest(p_policy_version text,p_evidence_ids uuid[])
RETURNS bytea LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE policy privacy_request_policies%ROWTYPE;now_at timestamptz:=clock_timestamp();result bytea;
BEGIN
 SELECT * INTO policy FROM privacy_request_policies WHERE version=p_policy_version;
 IF policy.version IS NULL OR policy.adopted_at IS NULL OR policy.working_retention_days<>90 OR cardinality(p_evidence_ids)<>4
  OR policy.executor_version<>'privacy-erasure-executor/v2' OR policy.plan_schema_version<>'privacy-erasure-plan/v2'
  OR (SELECT count(*) FROM privacy_activation_evidence evidence JOIN privacy_activation_authenticated_artifacts artifact ON artifact.evidence_id=evidence.id
      WHERE evidence.id=ANY(p_evidence_ids) AND evidence.expires_at>now_at AND artifact.policy_version=policy.version
       AND artifact.executor_version=policy.executor_version AND artifact.plan_schema_version=policy.plan_schema_version)<>4
  OR (SELECT count(DISTINCT evidence.kind) FROM privacy_activation_evidence evidence JOIN privacy_activation_authenticated_artifacts artifact ON artifact.evidence_id=evidence.id
      WHERE evidence.id=ANY(p_evidence_ids) AND evidence.expires_at>now_at)<>4
  OR (SELECT count(DISTINCT artifact.image_digest) FROM privacy_activation_authenticated_artifacts artifact WHERE artifact.evidence_id=ANY(p_evidence_ids))<>1
  OR (SELECT restore.schema_migration_digest IS DISTINCT FROM schema_row.schema_migration_digest
      FROM privacy_activation_authenticated_artifacts restore CROSS JOIN privacy_activation_authenticated_artifacts schema_row
      WHERE restore.evidence_id=ANY(p_evidence_ids) AND restore.kind='RESTORE' AND schema_row.evidence_id=ANY(p_evidence_ids) AND schema_row.kind='SCHEMA')
 THEN RETURN NULL; END IF;
 SELECT digest(convert_to(string_agg(evidence.kind||':'||encode(evidence.evidence_sha256,'hex')||':'||
   encode(digest(convert_to((to_jsonb(artifact)-'authenticated_at')::text,'UTF8'),'sha256'),'hex')||':'||
   to_char(evidence.observed_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"')||':'||
   to_char(evidence.expires_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),'|' ORDER BY evidence.kind),'UTF8'),'sha256')
 INTO result FROM privacy_activation_evidence evidence JOIN privacy_activation_authenticated_artifacts artifact ON artifact.evidence_id=evidence.id
 WHERE evidence.id=ANY(p_evidence_ids);
 RETURN result;
END;$$;

REVOKE ALL ON FUNCTION privacy_activation_authenticated_set_digest(text,uuid[]) FROM PUBLIC;

CREATE OR REPLACE FUNCTION privacy_activation_propose(p_actor uuid,p_policy_version text,p_evidence_ids uuid[])
RETURNS TABLE(proposal_id uuid,activation_sha256 bytea) LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE policy privacy_request_policies%ROWTYPE;evidence_digest bytea;activation_digest bytea;now_at timestamptz:=clock_timestamp();ordered_ids uuid[];
BEGIN
 IF NOT EXISTS(SELECT 1 FROM users account JOIN privacy_executor_grants grant_row ON grant_row.user_id=account.id AND grant_row.revoked_at IS NULL
   WHERE account.id=p_actor AND account.is_active AND NOT account.is_dependent) THEN
  RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_activation_forbidden'; END IF;
 SELECT * INTO policy FROM privacy_request_policies WHERE version=p_policy_version FOR SHARE;
 evidence_digest:=privacy_activation_authenticated_set_digest(p_policy_version,p_evidence_ids);
 IF evidence_digest IS NULL THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_activation_evidence_incomplete'; END IF;
 SELECT array_agg(evidence.id ORDER BY evidence.kind) INTO ordered_ids FROM privacy_activation_evidence evidence WHERE evidence.id=ANY(p_evidence_ids);
 activation_digest:=digest(convert_to('privacy-activation/v2:'||policy.version||':'||policy.executor_version||':'||policy.plan_schema_version||':'||
  encode(digest(convert_to(policy.category_catalogue::text,'UTF8'),'sha256'),'hex')||':'||encode(evidence_digest,'hex'),'UTF8'),'sha256');
 RETURN QUERY INSERT INTO privacy_activation_proposals(policy_version,evidence_ids,evidence_set_sha256,activation_sha256,proposed_by_ref,proposed_at)
 VALUES(policy.version,ordered_ids,evidence_digest,activation_digest,p_actor,now_at) RETURNING id,privacy_activation_proposals.activation_sha256;
END;$$;

CREATE OR REPLACE FUNCTION privacy_activation_approve(p_actor uuid,p_proposal_id uuid,p_activation_sha256 bytea)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE proposal privacy_activation_proposals%ROWTYPE;approval_id uuid;now_at timestamptz:=clock_timestamp();recomputed bytea;switch_version bigint;
BEGIN
 IF NOT EXISTS(SELECT 1 FROM users account JOIN user_platform_roles assignment ON assignment.user_id=account.id JOIN platform_roles role ON role.id=assignment.role_id
   WHERE account.id=p_actor AND account.is_active AND NOT account.is_dependent AND role.code='ADMIN') THEN
  RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_activation_forbidden'; END IF;
 SELECT * INTO proposal FROM privacy_activation_proposals WHERE id=p_proposal_id FOR SHARE;
 recomputed:=privacy_activation_authenticated_set_digest(proposal.policy_version,proposal.evidence_ids);
 IF proposal.id IS NULL OR proposal.proposed_by_ref=p_actor OR proposal.activation_sha256<>p_activation_sha256
  OR EXISTS(SELECT 1 FROM privacy_activation_approvals approval WHERE approval.proposal_id=proposal.id)
  OR recomputed IS NULL OR recomputed<>proposal.evidence_set_sha256 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_activation_unavailable'; END IF;
 INSERT INTO privacy_activation_approvals(proposal_id,activation_sha256,approved_by_ref,approved_at)
 VALUES(proposal.id,proposal.activation_sha256,p_actor,now_at) RETURNING id INTO approval_id;
 PERFORM set_config('mycfc.privacy_activation_approval','approved',true);
 INSERT INTO privacy_request_activation(singleton,policy_version,enabled,fulfilment_ready,updated_by,updated_at,approval_id)
 VALUES(true,proposal.policy_version,true,true,p_actor,now_at,approval_id)
 ON CONFLICT(singleton) DO UPDATE SET policy_version=EXCLUDED.policy_version,enabled=true,fulfilment_ready=true,updated_by=EXCLUDED.updated_by,updated_at=EXCLUDED.updated_at,approval_id=EXCLUDED.approval_id;
 INSERT INTO privacy_request_activation_events(policy_version,actor_ref,enabled,fulfilment_ready,occurred_at)
 VALUES(proposal.policy_version,p_actor,true,true,now_at);
 UPDATE privacy_worker_kill_switch SET engaged=false,version=version+1,activation_approval_id=approval_id,changed_at=now_at
 WHERE singleton RETURNING version INTO switch_version;
 INSERT INTO privacy_worker_kill_switch_events(version,engaged,activation_approval_id,occurred_at) VALUES(switch_version,false,approval_id,now_at);
 RETURN approval_id;
END;$$;

CREATE OR REPLACE FUNCTION privacy_activation_ready(p_policy_version text)
RETURNS boolean LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT EXISTS(
  SELECT 1 FROM privacy_request_activation activation
  JOIN privacy_activation_approvals approval ON approval.id=activation.approval_id
  JOIN privacy_activation_proposals proposal ON proposal.id=approval.proposal_id
  JOIN privacy_worker_kill_switch switch_row ON switch_row.singleton AND NOT switch_row.engaged
  WHERE activation.singleton AND activation.enabled AND activation.fulfilment_ready AND activation.policy_version=p_policy_version
   AND proposal.policy_version=activation.policy_version AND approval.activation_sha256=proposal.activation_sha256
   AND approval.approved_by_ref<>proposal.proposed_by_ref
   AND privacy_activation_authenticated_set_digest(proposal.policy_version,proposal.evidence_ids)=proposal.evidence_set_sha256
 );
$$;

CREATE OR REPLACE FUNCTION privacy_worker_activation_ready()
RETURNS boolean LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT COALESCE((SELECT privacy_activation_ready(activation.policy_version) FROM privacy_request_activation activation WHERE activation.singleton),false);
$$;

CREATE OR REPLACE FUNCTION privacy_activation_control_snapshot(p_actor uuid)
RETURNS TABLE(policy_version text,ready boolean,evidence_id uuid,evidence_kind text,evidence_observed_at timestamptz,
 proposal_id uuid,proposal_sha256 bytea,proposal_created_at timestamptz,can_propose boolean,can_renew boolean,can_approve boolean)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE actor_executor boolean;actor_admin boolean;selected_policy text;pending privacy_activation_proposals%ROWTYPE;
 current_ids uuid[];current_digest bytea;renewal_evidence boolean;
BEGIN
 SELECT EXISTS(SELECT 1 FROM users account JOIN privacy_executor_grants grant_row ON grant_row.user_id=account.id AND grant_row.revoked_at IS NULL
   WHERE account.id=p_actor AND account.is_active AND NOT account.is_dependent),
  EXISTS(SELECT 1 FROM users account JOIN user_platform_roles assignment ON assignment.user_id=account.id JOIN platform_roles role ON role.id=assignment.role_id
   WHERE account.id=p_actor AND account.is_active AND NOT account.is_dependent AND role.code='ADMIN') INTO actor_executor,actor_admin;
 IF NOT actor_executor AND NOT actor_admin THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_control_forbidden'; END IF;
 SELECT COALESCE((SELECT activation.policy_version FROM privacy_request_activation activation WHERE activation.singleton AND activation.enabled),
  (SELECT policy.version FROM privacy_request_policies policy WHERE policy.adopted_at IS NOT NULL ORDER BY policy.adopted_at DESC,policy.version DESC LIMIT 1)) INTO selected_policy;
 IF selected_policy IS NULL THEN RETURN; END IF;
 SELECT array_agg(latest.id ORDER BY latest.kind) INTO current_ids FROM (
  SELECT DISTINCT ON(evidence.kind) evidence.id,evidence.kind FROM privacy_activation_evidence evidence
  JOIN privacy_activation_authenticated_artifacts artifact ON artifact.evidence_id=evidence.id AND artifact.kind=evidence.kind
  JOIN privacy_request_policies policy ON policy.version=selected_policy AND artifact.policy_version=policy.version
   AND artifact.executor_version=policy.executor_version AND artifact.plan_schema_version=policy.plan_schema_version
  WHERE evidence.expires_at>clock_timestamp() ORDER BY evidence.kind,evidence.observed_at DESC,evidence.id DESC
 ) latest;
 current_digest:=privacy_activation_authenticated_set_digest(selected_policy,current_ids);
 SELECT proposal.* INTO pending FROM privacy_activation_proposals proposal
  WHERE proposal.policy_version=selected_policy AND privacy_activation_authenticated_set_digest(proposal.policy_version,proposal.evidence_ids)=proposal.evidence_set_sha256
   AND NOT EXISTS(SELECT 1 FROM privacy_activation_approvals approval WHERE approval.proposal_id=proposal.id)
  ORDER BY proposal.proposed_at DESC,proposal.id DESC LIMIT 1;
 SELECT EXISTS(SELECT 1 FROM privacy_request_activation activation JOIN privacy_activation_approvals approval ON approval.id=activation.approval_id
  JOIN privacy_activation_proposals proposal ON proposal.id=approval.proposal_id
  WHERE activation.singleton AND activation.enabled AND activation.policy_version=selected_policy AND current_digest IS NOT NULL
   AND EXISTS(SELECT 1 FROM unnest(current_ids) AS current_id(evidence_id) WHERE NOT current_id.evidence_id=ANY(proposal.evidence_ids))) INTO renewal_evidence;
 RETURN QUERY SELECT selected_policy,privacy_activation_ready(selected_policy),evidence.id,evidence.kind::text,evidence.observed_at,
  pending.id,pending.activation_sha256,pending.proposed_at,
  (actor_executor AND current_digest IS NOT NULL AND pending.id IS NULL AND NOT privacy_activation_ready(selected_policy)),
  (actor_executor AND current_digest IS NOT NULL AND pending.id IS NULL AND privacy_activation_ready(selected_policy) AND renewal_evidence),
  (actor_admin AND pending.id IS NOT NULL AND pending.proposed_by_ref<>p_actor)
 FROM (SELECT true singleton) seed LEFT JOIN LATERAL(
  SELECT candidate.id,candidate.kind,candidate.observed_at FROM privacy_activation_evidence candidate
  WHERE candidate.id=ANY(current_ids) ORDER BY candidate.kind
 ) evidence ON true ORDER BY evidence.kind;
END;$$;

CREATE OR REPLACE FUNCTION privacy_worker_engage_kill_switch() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE switch_version bigint;
BEGIN
 IF NEW.enabled=false AND (TG_OP='INSERT' OR OLD.enabled IS DISTINCT FROM NEW.enabled) THEN
  UPDATE privacy_worker_kill_switch SET engaged=true,version=version+1,activation_approval_id=NULL,changed_at=clock_timestamp()
  WHERE singleton AND NOT engaged RETURNING version INTO switch_version;
  IF switch_version IS NOT NULL THEN
   INSERT INTO privacy_worker_kill_switch_events(version,engaged,occurred_at) VALUES(switch_version,true,clock_timestamp());
  END IF;
 END IF;
 RETURN NEW;
END;$$;
CREATE TRIGGER privacy_worker_engage_kill_switch AFTER INSERT OR UPDATE OF enabled ON privacy_request_activation
 FOR EACH ROW EXECUTE FUNCTION privacy_worker_engage_kill_switch();

CREATE OR REPLACE FUNCTION privacy_worker_require_activation() RETURNS void LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
 PERFORM 1 FROM privacy_worker_kill_switch switch_row WHERE switch_row.singleton AND NOT switch_row.engaged FOR SHARE;
 IF NOT FOUND OR NOT privacy_worker_activation_ready() THEN
  RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_worker_disabled';
 END IF;
END;$$;

CREATE OR REPLACE FUNCTION privacy_activation_record_authenticated_evidence(
 p_actor uuid,p_kind text,p_evidence_sha256 bytea,p_reference_code text,p_observed_at timestamptz,p_expires_at timestamptz,p_artifact jsonb
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE v_evidence_id uuid;now_at timestamptz:=clock_timestamp();expected_keys text[];
BEGIN
 IF NOT EXISTS(SELECT 1 FROM users account JOIN user_platform_roles assignment ON assignment.user_id=account.id
   JOIN platform_roles role ON role.id=assignment.role_id WHERE account.id=p_actor AND account.is_active AND NOT account.is_dependent AND role.code='ADMIN')
  OR p_kind NOT IN ('RESTORE','INFRASTRUCTURE','PROVIDER','SCHEMA') OR octet_length(p_evidence_sha256)<>32
  OR p_observed_at>now_at OR p_observed_at<=now_at-interval '2160 hours' OR p_expires_at<=now_at OR p_expires_at>p_observed_at+interval '2160 hours'
  OR jsonb_typeof(p_artifact)<>'object' THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy_activation_evidence_rejected'; END IF;
 expected_keys:=CASE p_kind
  WHEN 'RESTORE' THEN ARRAY['policy_version','executor_version','plan_schema_version','image_digest','evidence_ref','evidence_sha256','schema_migration_digest','restore_input_source','restore_input_contract','restore_replay_contract','restore_closure_contract','restore_candidate_sha256','restore_inventory_sha256','restore_object_count','restore_replayed_count','restore_synthetic_count','restore_observer_sha256']
  WHEN 'INFRASTRUCTURE' THEN ARRAY['policy_version','executor_version','plan_schema_version','image_digest','evidence_ref','evidence_sha256','signing_key_id','production_state_serial','hetzner_state_serial','production_state_sha256','hetzner_state_sha256','production_plan_sha256','hetzner_plan_sha256','worker_identity_enabled','s3_version_deletion_enabled','ledger_broker_invoke_enabled','worker_monitoring_enabled','restore_infrastructure_enabled','restore_ledger_write_enabled']
  WHEN 'PROVIDER' THEN ARRAY['policy_version','executor_version','plan_schema_version','image_digest','evidence_ref','evidence_sha256','signing_key_id','provider_registry_state','provider_registration_count','provider_registry_sha256']
  WHEN 'SCHEMA' THEN ARRAY['policy_version','executor_version','plan_schema_version','image_digest','evidence_ref','evidence_sha256','signing_key_id','schema_migration_digest','baseline_includes_through'] END;
 IF NOT p_artifact ?& expected_keys OR (SELECT count(*) FROM jsonb_object_keys(p_artifact))<>cardinality(expected_keys)
  OR p_artifact->>'policy_version' IS NULL OR p_artifact->>'executor_version'<>'privacy-erasure-executor/v2'
  OR p_artifact->>'plan_schema_version'<>'privacy-erasure-plan/v2' OR p_artifact->>'image_digest'!~'^sha256:[0-9a-f]{64}$' THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy_activation_artifact_rejected'; END IF;
 IF (p_kind='RESTORE' AND p_reference_code<>'mycfc/privacy-restore-drill-attestation/v2')
  OR (p_kind='INFRASTRUCTURE' AND p_reference_code<>'mycfc/privacy-infrastructure-posture/v1')
  OR (p_kind='PROVIDER' AND p_reference_code<>'mycfc/privacy-provider-registry/v1')
  OR (p_kind='SCHEMA' AND p_reference_code<>'mycfc/schema-migration-inventory/v1') THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy_activation_evidence_contract_rejected'; END IF;
 INSERT INTO privacy_activation_evidence(kind,evidence_sha256,reference_code,observed_at,expires_at,recorded_by_ref,recorded_at)
 VALUES(p_kind,p_evidence_sha256,p_reference_code,p_observed_at,p_expires_at,p_actor,now_at)
 ON CONFLICT(kind,evidence_sha256) DO NOTHING RETURNING id INTO v_evidence_id;
 IF v_evidence_id IS NULL THEN
  SELECT evidence.id INTO v_evidence_id FROM privacy_activation_evidence evidence WHERE evidence.kind=p_kind AND evidence.evidence_sha256=p_evidence_sha256
   AND evidence.reference_code=p_reference_code AND evidence.observed_at=p_observed_at AND evidence.expires_at=p_expires_at;
  IF v_evidence_id IS NULL THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_activation_evidence_conflict'; END IF;
 END IF;
 INSERT INTO privacy_activation_authenticated_artifacts(
  evidence_id,kind,policy_version,executor_version,plan_schema_version,image_digest,immutable_evidence_ref,immutable_evidence_sha256,signing_key_id,
  schema_migration_digest,baseline_includes_through,production_state_serial,hetzner_state_serial,production_state_sha256,hetzner_state_sha256,
  production_plan_sha256,hetzner_plan_sha256,worker_identity_enabled,s3_version_deletion_enabled,ledger_broker_invoke_enabled,worker_monitoring_enabled,
  restore_infrastructure_enabled,restore_ledger_write_enabled,provider_registry_state,provider_registration_count,provider_registry_sha256,restore_input_source,
  restore_input_contract,restore_replay_contract,restore_closure_contract,restore_candidate_sha256,restore_inventory_sha256,restore_object_count,restore_replayed_count,
  restore_synthetic_count,restore_observer_sha256,authenticated_at)
 VALUES(v_evidence_id,p_kind,p_artifact->>'policy_version',p_artifact->>'executor_version',p_artifact->>'plan_schema_version',p_artifact->>'image_digest',
  p_artifact->>'evidence_ref',decode(p_artifact->>'evidence_sha256','base64'),p_artifact->>'signing_key_id',decode(p_artifact->>'schema_migration_digest','base64'),
  p_artifact->>'baseline_includes_through',(p_artifact->>'production_state_serial')::bigint,(p_artifact->>'hetzner_state_serial')::bigint,
  decode(p_artifact->>'production_state_sha256','base64'),decode(p_artifact->>'hetzner_state_sha256','base64'),decode(p_artifact->>'production_plan_sha256','base64'),
  decode(p_artifact->>'hetzner_plan_sha256','base64'),(p_artifact->>'worker_identity_enabled')::boolean,(p_artifact->>'s3_version_deletion_enabled')::boolean,
  (p_artifact->>'ledger_broker_invoke_enabled')::boolean,(p_artifact->>'worker_monitoring_enabled')::boolean,(p_artifact->>'restore_infrastructure_enabled')::boolean,
  (p_artifact->>'restore_ledger_write_enabled')::boolean,p_artifact->>'provider_registry_state',(p_artifact->>'provider_registration_count')::bigint,
  decode(p_artifact->>'provider_registry_sha256','base64'),p_artifact->>'restore_input_source',p_artifact->>'restore_input_contract',p_artifact->>'restore_replay_contract',
  p_artifact->>'restore_closure_contract',decode(p_artifact->>'restore_candidate_sha256','base64'),
  decode(p_artifact->>'restore_inventory_sha256','base64'),(p_artifact->>'restore_object_count')::bigint,(p_artifact->>'restore_replayed_count')::bigint,
  (p_artifact->>'restore_synthetic_count')::bigint,decode(p_artifact->>'restore_observer_sha256','base64'),now_at)
 ON CONFLICT ON CONSTRAINT privacy_activation_authenticated_artifacts_pkey DO NOTHING;
 IF NOT EXISTS(SELECT 1 FROM privacy_activation_authenticated_artifacts authenticated WHERE authenticated.evidence_id=v_evidence_id
   AND authenticated.kind=p_kind AND authenticated.policy_version=p_artifact->>'policy_version' AND authenticated.image_digest=p_artifact->>'image_digest') THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_activation_artifact_conflict'; END IF;
 RETURN v_evidence_id;
EXCEPTION WHEN invalid_text_representation OR numeric_value_out_of_range OR check_violation THEN
 RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy_activation_artifact_rejected';
END;$$;

REVOKE ALL ON FUNCTION privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb),privacy_worker_require_activation() FROM PUBLIC;
-- #248 trusted activation boundary. The web and executor roles remain unable
-- to create evidence, propose, approve, enable, or clear the kill switch.

ALTER TABLE privacy_activation_authenticated_artifacts DROP CONSTRAINT privacy_activation_authenticated_artifacts_check;
ALTER TABLE privacy_activation_authenticated_artifacts ADD CONSTRAINT privacy_activation_authenticated_artifacts_check CHECK((
 (kind='RESTORE' AND signing_key_id IS NULL AND immutable_evidence_ref IS NOT NULL AND schema_migration_digest IS NOT NULL AND baseline_includes_through IS NULL
  AND restore_input_source IN ('LIVE_LEDGER','SYNTHETIC_BOOTSTRAP') AND restore_input_contract='mycfc/privacy-restore-ledger-input/v2'
  AND restore_replay_contract='relational-erasure-replay/v1' AND restore_closure_contract='restore-tombstone-closure/v3'
  AND restore_candidate_sha256 IS NOT NULL AND octet_length(restore_candidate_sha256)=32 AND restore_inventory_sha256 IS NOT NULL
  AND octet_length(restore_inventory_sha256)=32 AND restore_observer_sha256 IS NOT NULL AND octet_length(restore_observer_sha256)=32
  AND restore_object_count>=restore_replayed_count AND restore_replayed_count>0
  AND ((restore_input_source='LIVE_LEDGER' AND restore_synthetic_count=0) OR (restore_input_source='SYNTHETIC_BOOTSTRAP' AND restore_synthetic_count=restore_replayed_count)))
 OR (kind='INFRASTRUCTURE' AND signing_key_id IS NOT NULL AND immutable_evidence_ref IS NOT NULL AND production_state_serial>0 AND hetzner_state_serial>0
  AND octet_length(production_state_sha256)=32 AND octet_length(hetzner_state_sha256)=32 AND octet_length(production_plan_sha256)=32
  AND octet_length(hetzner_plan_sha256)=32 AND worker_identity_enabled AND s3_version_deletion_enabled AND ledger_broker_invoke_enabled
  AND worker_monitoring_enabled AND restore_infrastructure_enabled AND restore_ledger_write_enabled)
 OR (kind='PROVIDER' AND signing_key_id IS NOT NULL AND immutable_evidence_ref IS NOT NULL AND provider_registry_state='READY'
  AND provider_registration_count>0 AND octet_length(provider_registry_sha256)=32)
 OR (kind='SCHEMA' AND signing_key_id IS NOT NULL AND immutable_evidence_ref IS NOT NULL AND octet_length(schema_migration_digest)=32
  AND baseline_includes_through='202609100014_privacy_activation_broker')) IS TRUE);

CREATE TABLE privacy_protected.activation_signed_approvals (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 proposal_id uuid NOT NULL REFERENCES privacy_activation_proposals(id) ON DELETE RESTRICT,
 signer_role varchar(20) NOT NULL CHECK(signer_role IN ('EXECUTOR','ADMINISTRATOR')),
 actor_ref uuid NOT NULL,
 signing_key_id varchar(120) NOT NULL CHECK(signing_key_id=btrim(signing_key_id) AND signing_key_id~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,119}$'),
 nonce_sha256 bytea NOT NULL UNIQUE CHECK(octet_length(nonce_sha256)=32),
 envelope_sha256 bytea NOT NULL UNIQUE CHECK(octet_length(envelope_sha256)=32),
 raw_envelope bytea NOT NULL CHECK(octet_length(raw_envelope) BETWEEN 1 AND 16384),
 parsed_envelope jsonb NOT NULL,
 issued_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL,
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(proposal_id,signer_role),
 CHECK(expires_at>issued_at AND expires_at<=issued_at+interval '15 minutes')
);
CREATE TRIGGER privacy_activation_signed_approvals_immutable BEFORE UPDATE OR DELETE
 ON privacy_protected.activation_signed_approvals FOR EACH ROW EXECUTE FUNCTION prevent_privacy_execution_record_delete();

CREATE TABLE privacy_protected.activation_broker_receipts (
 proposal_id uuid PRIMARY KEY REFERENCES privacy_activation_proposals(id) ON DELETE RESTRICT,
 approval_id uuid NOT NULL UNIQUE REFERENCES privacy_activation_approvals(id) ON DELETE RESTRICT,
 executor_envelope_id uuid NOT NULL UNIQUE REFERENCES privacy_protected.activation_signed_approvals(id) ON DELETE RESTRICT,
 administrator_envelope_id uuid NOT NULL UNIQUE REFERENCES privacy_protected.activation_signed_approvals(id) ON DELETE RESTRICT,
 evidence_set_sha256 bytea NOT NULL CHECK(octet_length(evidence_set_sha256)=32),
 activation_sha256 bytea NOT NULL CHECK(octet_length(activation_sha256)=32),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TRIGGER privacy_activation_broker_receipts_immutable BEFORE UPDATE OR DELETE
 ON privacy_protected.activation_broker_receipts FOR EACH ROW EXECUTE FUNCTION prevent_privacy_execution_record_delete();

-- Renaming SECURITY DEFINER functions preserves their ACL. Remove every
-- inherited EXECUTE capability from every non-owner, including PUBLIC, and
-- assert that the cleanup really took effect before this migration commits.
DO $$ DECLARE capability record; grantee_name text;
BEGIN
 FOR capability IN
  SELECT proc.oid,proc.proowner,acl.grantee
  FROM pg_proc proc JOIN pg_namespace namespace ON namespace.oid=proc.pronamespace
  CROSS JOIN LATERAL aclexplode(COALESCE(proc.proacl,acldefault('f',proc.proowner))) acl
  WHERE namespace.nspname='public' AND proc.proname LIKE '%\_inner\_013' ESCAPE '\'
   AND acl.privilege_type='EXECUTE' AND acl.grantee<>proc.proowner
 LOOP
  grantee_name:=CASE WHEN capability.grantee=0 THEN 'PUBLIC' ELSE quote_ident((SELECT rolname FROM pg_roles WHERE oid=capability.grantee)) END;
  IF grantee_name IS NOT NULL THEN
   EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM %s',capability.oid::regprocedure,grantee_name);
  END IF;
 END LOOP;
 IF EXISTS(
  SELECT 1 FROM pg_proc proc JOIN pg_namespace namespace ON namespace.oid=proc.pronamespace
  CROSS JOIN LATERAL aclexplode(COALESCE(proc.proacl,acldefault('f',proc.proowner))) acl
  WHERE namespace.nspname='public' AND proc.proname LIKE '%\_inner\_013' ESCAPE '\'
   AND acl.privilege_type='EXECUTE' AND acl.grantee<>proc.proowner
 ) THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_inner_capability_revoke_failed'; END IF;
END $$;

-- No ordinary application identity may call the raw mutation primitives.
DO $$ DECLARE capability record; grantee_name text;
BEGIN
 FOR capability IN
  SELECT proc.oid,proc.proowner,acl.grantee
  FROM pg_proc proc JOIN pg_namespace namespace ON namespace.oid=proc.pronamespace
  CROSS JOIN LATERAL aclexplode(COALESCE(proc.proacl,acldefault('f',proc.proowner))) acl
  WHERE namespace.nspname='public' AND proc.proname IN (
   'privacy_activation_record_evidence','privacy_activation_record_authenticated_evidence',
   'privacy_activation_propose','privacy_activation_approve','privacy_activation_authenticated_set_digest')
   AND acl.privilege_type='EXECUTE' AND acl.grantee<>proc.proowner
 LOOP
  grantee_name:=CASE WHEN capability.grantee=0 THEN 'PUBLIC' ELSE quote_ident((SELECT rolname FROM pg_roles WHERE oid=capability.grantee)) END;
  IF grantee_name IS NOT NULL THEN
   EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM %s',capability.oid::regprocedure,grantee_name);
  END IF;
 END LOOP;
END $$;

CREATE FUNCTION privacy_activation_broker_record_authenticated_evidence(
 p_actor uuid,p_kind text,p_evidence_sha256 bytea,p_reference_code text,p_observed_at timestamptz,p_expires_at timestamptz,p_artifact jsonb
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
 IF session_user<>'mycfc_privacy_activation_broker' THEN
  RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_activation_broker_required';
 END IF;
 RETURN privacy_activation_record_authenticated_evidence(p_actor,p_kind,p_evidence_sha256,p_reference_code,p_observed_at,p_expires_at,p_artifact);
END;$$;

CREATE FUNCTION privacy_activation_broker_material(p_policy_version text)
RETURNS TABLE(evidence_ids uuid[],evidence_set_sha256 bytea,activation_sha256 bytea,executor_version text,plan_schema_version text,image_digest text,schema_migration_digest bytea)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE policy privacy_request_policies%ROWTYPE;selected_ids uuid[];set_digest bytea;
BEGIN
 IF session_user<>'mycfc_privacy_activation_broker' THEN
  RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_activation_broker_required';
 END IF;
 SELECT * INTO policy FROM privacy_request_policies WHERE version=p_policy_version AND adopted_at IS NOT NULL FOR SHARE;
 SELECT array_agg(latest.id ORDER BY latest.kind) INTO selected_ids FROM (
  SELECT DISTINCT ON(e.kind) e.id,e.kind FROM privacy_activation_evidence e
  JOIN privacy_activation_authenticated_artifacts a ON a.evidence_id=e.id AND a.kind=e.kind
  WHERE a.policy_version=p_policy_version AND e.expires_at>clock_timestamp()
  ORDER BY e.kind,e.observed_at DESC,e.id DESC
 ) latest;
 set_digest:=privacy_activation_authenticated_set_digest(p_policy_version,selected_ids);
 IF policy.version IS NULL OR set_digest IS NULL THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_activation_evidence_incomplete';
 END IF;
 RETURN QUERY SELECT selected_ids,set_digest,
  digest(convert_to('privacy-activation/v2:'||policy.version||':'||policy.executor_version||':'||policy.plan_schema_version||':'||
   encode(digest(convert_to(policy.category_catalogue::text,'UTF8'),'sha256'),'hex')||':'||encode(set_digest,'hex'),'UTF8'),'sha256'),
  policy.executor_version::text,policy.plan_schema_version::text,
  (SELECT a.image_digest FROM privacy_activation_authenticated_artifacts a WHERE a.evidence_id=selected_ids[1]),
  (SELECT a.schema_migration_digest FROM privacy_activation_authenticated_artifacts a WHERE a.evidence_id=ANY(selected_ids) AND a.kind='SCHEMA');
END;$$;

CREATE FUNCTION privacy_activation_broker_activate(
 p_proposal_id uuid,p_policy_version text,p_evidence_ids uuid[],p_evidence_set_sha256 bytea,p_activation_sha256 bytea,
 p_executor_actor uuid,p_administrator_actor uuid,p_executor_key_id text,p_administrator_key_id text,
 p_executor_nonce_sha256 bytea,p_administrator_nonce_sha256 bytea,p_executor_envelope_raw bytea,p_administrator_envelope_raw bytea,
 p_executor_envelope jsonb,p_administrator_envelope jsonb,
 p_executor_issued_at timestamptz,p_executor_expires_at timestamptz,p_administrator_issued_at timestamptz,p_administrator_expires_at timestamptz
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE material record;now_at timestamptz:=clock_timestamp();executor_envelope_id uuid;administrator_envelope_id uuid;approval_id uuid;switch_version bigint;
BEGIN
 IF session_user<>'mycfc_privacy_activation_broker' THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_activation_broker_required'; END IF;
 SELECT * INTO material FROM privacy_activation_broker_material(p_policy_version);
 IF p_proposal_id IS NULL OR p_executor_actor IS NULL OR p_administrator_actor IS NULL OR p_executor_actor=p_administrator_actor
  OR material.evidence_ids IS DISTINCT FROM p_evidence_ids OR material.evidence_set_sha256 IS DISTINCT FROM p_evidence_set_sha256
  OR material.activation_sha256 IS DISTINCT FROM p_activation_sha256 OR octet_length(p_executor_nonce_sha256)<>32 OR octet_length(p_administrator_nonce_sha256)<>32
  OR p_executor_nonce_sha256=p_administrator_nonce_sha256 OR p_executor_issued_at>now_at OR p_administrator_issued_at>now_at
  OR p_executor_expires_at<=now_at OR p_administrator_expires_at<=now_at
  OR p_executor_expires_at>p_executor_issued_at+interval '15 minutes' OR p_administrator_expires_at>p_administrator_issued_at+interval '15 minutes'
  OR jsonb_typeof(p_executor_envelope)<>'object' OR jsonb_typeof(p_administrator_envelope)<>'object'
  OR convert_from(p_executor_envelope_raw,'UTF8')::jsonb IS DISTINCT FROM p_executor_envelope
  OR convert_from(p_administrator_envelope_raw,'UTF8')::jsonb IS DISTINCT FROM p_administrator_envelope
  OR (SELECT array_agg(key ORDER BY key) FROM jsonb_object_keys(p_executor_envelope) key)<>ARRAY['activation_sha256','actor_ref','contract','evidence_ids','evidence_set_sha256','executor_version','expires_at','image_digest','issued_at','nonce','plan_schema_version','policy_version','proposal_id','role','schema_migration_digest','signature_ed25519','signing_key_id']
  OR (SELECT array_agg(key ORDER BY key) FROM jsonb_object_keys(p_administrator_envelope) key)<>ARRAY['activation_sha256','actor_ref','contract','evidence_ids','evidence_set_sha256','executor_version','expires_at','image_digest','issued_at','nonce','plan_schema_version','policy_version','proposal_id','role','schema_migration_digest','signature_ed25519','signing_key_id']
  OR p_executor_envelope->>'contract'<>'mycfc/privacy-activation-approval/v1' OR p_administrator_envelope->>'contract'<>'mycfc/privacy-activation-approval/v1'
  OR p_executor_envelope->>'role'<>'EXECUTOR' OR p_administrator_envelope->>'role'<>'ADMINISTRATOR'
  OR p_executor_envelope->>'proposal_id'<>p_proposal_id::text OR p_administrator_envelope->>'proposal_id'<>p_proposal_id::text
  OR p_executor_envelope->>'actor_ref'<>p_executor_actor::text OR p_administrator_envelope->>'actor_ref'<>p_administrator_actor::text
  OR p_executor_envelope->>'signing_key_id'<>p_executor_key_id OR p_administrator_envelope->>'signing_key_id'<>p_administrator_key_id
  OR (p_executor_envelope->>'issued_at')::timestamptz IS DISTINCT FROM p_executor_issued_at OR (p_administrator_envelope->>'issued_at')::timestamptz IS DISTINCT FROM p_administrator_issued_at
  OR (p_executor_envelope->>'expires_at')::timestamptz IS DISTINCT FROM p_executor_expires_at OR (p_administrator_envelope->>'expires_at')::timestamptz IS DISTINCT FROM p_administrator_expires_at
  OR digest(decode(p_executor_envelope->>'nonce','base64'),'sha256') IS DISTINCT FROM p_executor_nonce_sha256
  OR digest(decode(p_administrator_envelope->>'nonce','base64'),'sha256') IS DISTINCT FROM p_administrator_nonce_sha256
  OR octet_length(decode(p_executor_envelope->>'signature_ed25519','base64'))<>64 OR octet_length(decode(p_administrator_envelope->>'signature_ed25519','base64'))<>64
  OR p_executor_envelope->>'activation_sha256'<>encode(p_activation_sha256,'hex') OR p_administrator_envelope->>'activation_sha256'<>encode(p_activation_sha256,'hex')
  OR p_executor_envelope->>'evidence_set_sha256'<>encode(p_evidence_set_sha256,'hex') OR p_administrator_envelope->>'evidence_set_sha256'<>encode(p_evidence_set_sha256,'hex')
  OR p_executor_envelope->'evidence_ids'<>to_jsonb(p_evidence_ids) OR p_administrator_envelope->'evidence_ids'<>to_jsonb(p_evidence_ids)
  OR p_executor_envelope->>'policy_version'<>p_policy_version OR p_administrator_envelope->>'policy_version'<>p_policy_version
  OR p_executor_envelope->>'executor_version'<>material.executor_version OR p_administrator_envelope->>'executor_version'<>material.executor_version
  OR p_executor_envelope->>'plan_schema_version'<>material.plan_schema_version OR p_administrator_envelope->>'plan_schema_version'<>material.plan_schema_version
  OR p_executor_envelope->>'image_digest'<>material.image_digest OR p_administrator_envelope->>'image_digest'<>material.image_digest
  OR p_executor_envelope->>'schema_migration_digest'<>encode(material.schema_migration_digest,'hex') OR p_administrator_envelope->>'schema_migration_digest'<>encode(material.schema_migration_digest,'hex')
  OR NOT EXISTS(SELECT 1 FROM users u JOIN privacy_executor_grants g ON g.user_id=u.id AND g.revoked_at IS NULL WHERE u.id=p_executor_actor AND u.is_active AND NOT u.is_dependent)
  OR NOT EXISTS(SELECT 1 FROM users u JOIN user_platform_roles a ON a.user_id=u.id JOIN platform_roles r ON r.id=a.role_id WHERE u.id=p_administrator_actor AND u.is_active AND NOT u.is_dependent AND r.code='ADMIN')
 THEN RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy_activation_signed_approval_rejected'; END IF;

 INSERT INTO privacy_activation_proposals(id,policy_version,evidence_ids,evidence_set_sha256,activation_sha256,proposed_by_ref,proposed_at)
 VALUES(p_proposal_id,p_policy_version,p_evidence_ids,p_evidence_set_sha256,p_activation_sha256,p_executor_actor,now_at);
 INSERT INTO privacy_protected.activation_signed_approvals(proposal_id,signer_role,actor_ref,signing_key_id,nonce_sha256,envelope_sha256,raw_envelope,parsed_envelope,issued_at,expires_at)
 VALUES(p_proposal_id,'EXECUTOR',p_executor_actor,p_executor_key_id,p_executor_nonce_sha256,digest(p_executor_envelope_raw,'sha256'),p_executor_envelope_raw,p_executor_envelope,p_executor_issued_at,p_executor_expires_at)
 RETURNING id INTO executor_envelope_id;
 INSERT INTO privacy_protected.activation_signed_approvals(proposal_id,signer_role,actor_ref,signing_key_id,nonce_sha256,envelope_sha256,raw_envelope,parsed_envelope,issued_at,expires_at)
 VALUES(p_proposal_id,'ADMINISTRATOR',p_administrator_actor,p_administrator_key_id,p_administrator_nonce_sha256,digest(p_administrator_envelope_raw,'sha256'),p_administrator_envelope_raw,p_administrator_envelope,p_administrator_issued_at,p_administrator_expires_at)
 RETURNING id INTO administrator_envelope_id;
 INSERT INTO privacy_activation_approvals(proposal_id,activation_sha256,approved_by_ref,approved_at)
 VALUES(p_proposal_id,p_activation_sha256,p_administrator_actor,now_at) RETURNING id INTO approval_id;
 PERFORM set_config('mycfc.privacy_activation_approval','approved',true);
 INSERT INTO privacy_request_activation(singleton,policy_version,enabled,fulfilment_ready,updated_by,updated_at,approval_id)
 VALUES(true,p_policy_version,true,true,p_administrator_actor,now_at,approval_id)
 ON CONFLICT(singleton) DO UPDATE SET policy_version=EXCLUDED.policy_version,enabled=true,fulfilment_ready=true,updated_by=EXCLUDED.updated_by,updated_at=EXCLUDED.updated_at,approval_id=EXCLUDED.approval_id;
 INSERT INTO privacy_request_activation_events(policy_version,actor_ref,enabled,fulfilment_ready,occurred_at)
 VALUES(p_policy_version,p_administrator_actor,true,true,now_at);
 UPDATE privacy_worker_kill_switch SET engaged=false,version=version+1,activation_approval_id=approval_id,changed_at=now_at WHERE singleton RETURNING version INTO switch_version;
 INSERT INTO privacy_worker_kill_switch_events(version,engaged,activation_approval_id,occurred_at) VALUES(switch_version,false,approval_id,now_at);
 INSERT INTO privacy_protected.activation_broker_receipts(proposal_id,approval_id,executor_envelope_id,administrator_envelope_id,evidence_set_sha256,activation_sha256)
 VALUES(p_proposal_id,approval_id,executor_envelope_id,administrator_envelope_id,p_evidence_set_sha256,p_activation_sha256);
 RETURN approval_id;
EXCEPTION
 WHEN unique_violation THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_activation_signed_approval_replay';
 WHEN invalid_text_representation OR character_not_in_repertoire OR datetime_field_overflow THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy_activation_signed_approval_rejected';
END;$$;

CREATE FUNCTION privacy_activation_disable(p_actor uuid) RETURNS bigint LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE now_at timestamptz:=clock_timestamp();switch_version bigint;
BEGIN
 IF session_user<>'mycfc_privacy_activation_disable' THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_activation_disable_role_required'; END IF;
 UPDATE privacy_worker_kill_switch SET engaged=true,version=version+1,activation_approval_id=NULL,changed_at=now_at
 WHERE singleton AND NOT engaged RETURNING version INTO switch_version;
 IF switch_version IS NOT NULL THEN
  INSERT INTO privacy_worker_kill_switch_events(version,engaged,occurred_at) VALUES(switch_version,true,now_at);
 ELSE
  SELECT version INTO switch_version FROM privacy_worker_kill_switch WHERE singleton;
 END IF;
 UPDATE privacy_request_activation SET enabled=false,fulfilment_ready=false,approval_id=NULL,updated_by=p_actor,updated_at=now_at WHERE singleton;
 INSERT INTO privacy_request_activation_events(policy_version,actor_ref,enabled,fulfilment_ready,occurred_at)
 SELECT policy_version,p_actor,false,false,now_at FROM privacy_request_activation WHERE singleton;
 RETURN switch_version;
END;$$;

REVOKE ALL ON TABLE privacy_protected.activation_signed_approvals,privacy_protected.activation_broker_receipts FROM PUBLIC;
REVOKE ALL ON FUNCTION
 privacy_activation_broker_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb),
 privacy_activation_broker_material(text),
 privacy_activation_broker_activate(uuid,text,uuid[],bytea,bytea,uuid,uuid,text,text,bytea,bytea,bytea,bytea,jsonb,jsonb,timestamptz,timestamptz,timestamptz,timestamptz),
 privacy_activation_disable(uuid)
FROM PUBLIC;

-- Every schema upgrade re-engages the switch. Activation must be renewed
-- against the new embedded migration digest by the independent broker path.
UPDATE privacy_request_activation SET enabled=false,fulfilment_ready=false,approval_id=NULL,updated_at=clock_timestamp() WHERE singleton;
UPDATE privacy_worker_kill_switch SET engaged=true,version=version+1,activation_approval_id=NULL,changed_at=clock_timestamp() WHERE singleton;
INSERT INTO privacy_worker_kill_switch_events(version,engaged,occurred_at)
 SELECT version,true,changed_at FROM privacy_worker_kill_switch WHERE singleton;

-- Baseline through 202609100015_privacy_membership_postcondition.
-- #247 closure-v4: authenticate the complete retained membership-history
-- postcondition without retaining a subject or pseudonymous-principal link.
-- Existing v1-v3 ledger material remains readable but cannot authorize a new
-- replay or activation after this migration.

ALTER TABLE privacy_protected.restore_tombstone_closure_receipts
 DROP CONSTRAINT restore_tombstone_closure_receipts_ledger_version_check;
ALTER TABLE privacy_protected.restore_tombstone_closure_receipts
 ADD CONSTRAINT restore_tombstone_closure_receipts_ledger_version_check
 CHECK(ledger_version IN ('restore-tombstone-closure/v1','restore-tombstone-closure/v2','restore-tombstone-closure/v3','restore-tombstone-closure/v4')) NOT VALID;
ALTER TABLE privacy_protected.restore_tombstone_closure_receipts
 VALIDATE CONSTRAINT restore_tombstone_closure_receipts_ledger_version_check;

ALTER TABLE privacy_protected.restore_ledger_imports
 DROP CONSTRAINT restore_ledger_imports_closure_version_check;
ALTER TABLE privacy_protected.restore_ledger_imports
 ADD COLUMN membership_postcondition_contract varchar(64) NULL,
 ADD COLUMN membership_postcondition_sha256 bytea NULL,
 ADD COLUMN membership_count bigint NULL,
 ADD COLUMN variation_count bigint NULL,
 ADD CONSTRAINT restore_ledger_imports_closure_version_check CHECK(
  (kind='intent' AND closure_version IS NULL)
  OR (kind='closure' AND closure_version IN ('restore-tombstone-closure/v2','restore-tombstone-closure/v3','restore-tombstone-closure/v4'))
 ) NOT VALID,
 ADD CONSTRAINT restore_ledger_imports_membership_postcondition_check CHECK((
  (closure_version='restore-tombstone-closure/v4'
   AND membership_postcondition_contract='mycfc/membership-history-postcondition/v1'
   AND octet_length(membership_postcondition_sha256)=32
   AND membership_count BETWEEN 0 AND 10000 AND variation_count BETWEEN 0 AND 100000)
 OR (closure_version IS DISTINCT FROM 'restore-tombstone-closure/v4'
   AND membership_postcondition_contract IS NULL AND membership_postcondition_sha256 IS NULL
   AND membership_count IS NULL AND variation_count IS NULL)
 ) IS TRUE) NOT VALID;
ALTER TABLE privacy_protected.restore_ledger_imports
 VALIDATE CONSTRAINT restore_ledger_imports_closure_version_check;
ALTER TABLE privacy_protected.restore_ledger_imports
 VALIDATE CONSTRAINT restore_ledger_imports_membership_postcondition_check;

CREATE TABLE privacy_protected.membership_history_source_captures (
 execution_id uuid PRIMARY KEY REFERENCES public.privacy_erasure_executions(id) ON DELETE RESTRICT,
 captured_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE privacy_protected.membership_history_source_rows (
 execution_id uuid NOT NULL REFERENCES privacy_protected.membership_history_source_captures(execution_id) ON DELETE RESTRICT,
 membership_id uuid NOT NULL,
 PRIMARY KEY(execution_id,membership_id)
);
CREATE TABLE privacy_protected.membership_history_source_postconditions (
 execution_id uuid PRIMARY KEY REFERENCES privacy_protected.membership_history_source_captures(execution_id) ON DELETE RESTRICT,
 effective_at timestamptz NOT NULL,
 contract varchar(64) NOT NULL CHECK(contract='mycfc/membership-history-postcondition/v1'),
 postcondition_sha256 bytea NOT NULL CHECK(octet_length(postcondition_sha256)=32),
 membership_count bigint NOT NULL CHECK(membership_count BETWEEN 0 AND 10000),
 variation_count bigint NOT NULL CHECK(variation_count BETWEEN 0 AND 100000),
 canonical_size bigint NOT NULL CHECK(canonical_size BETWEEN 1 AND 16777216),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE privacy_protected.membership_history_replay_captures (
 run_id uuid PRIMARY KEY REFERENCES privacy_protected.restore_replay_runs(id) ON DELETE RESTRICT,
 captured_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE privacy_protected.membership_history_replay_rows (
 run_id uuid NOT NULL REFERENCES privacy_protected.membership_history_replay_captures(run_id) ON DELETE RESTRICT,
 membership_id uuid NOT NULL,
 PRIMARY KEY(run_id,membership_id)
);
CREATE TABLE privacy_protected.membership_history_replay_postconditions (
 run_id uuid PRIMARY KEY REFERENCES privacy_protected.membership_history_replay_captures(run_id) ON DELETE RESTRICT,
 contract varchar(64) NOT NULL CHECK(contract='mycfc/membership-history-postcondition/v1'),
 postcondition_sha256 bytea NOT NULL CHECK(octet_length(postcondition_sha256)=32),
 membership_count bigint NOT NULL CHECK(membership_count BETWEEN 0 AND 10000),
 variation_count bigint NOT NULL CHECK(variation_count BETWEEN 0 AND 100000),
 canonical_size bigint NOT NULL CHECK(canonical_size BETWEEN 1 AND 16777216),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TRIGGER privacy_membership_history_source_captures_immutable BEFORE UPDATE OR DELETE
 ON privacy_protected.membership_history_source_captures FOR EACH ROW EXECUTE FUNCTION public.prevent_privacy_execution_record_delete();
CREATE TRIGGER privacy_membership_history_source_rows_immutable BEFORE UPDATE OR DELETE
 ON privacy_protected.membership_history_source_rows FOR EACH ROW EXECUTE FUNCTION public.prevent_privacy_execution_record_delete();
CREATE TRIGGER privacy_membership_history_source_postconditions_immutable BEFORE UPDATE OR DELETE
 ON privacy_protected.membership_history_source_postconditions FOR EACH ROW EXECUTE FUNCTION public.prevent_privacy_execution_record_delete();
CREATE TRIGGER privacy_membership_history_replay_captures_immutable BEFORE UPDATE OR DELETE
 ON privacy_protected.membership_history_replay_captures FOR EACH ROW EXECUTE FUNCTION public.prevent_privacy_execution_record_delete();
CREATE TRIGGER privacy_membership_history_replay_rows_immutable BEFORE UPDATE OR DELETE
 ON privacy_protected.membership_history_replay_rows FOR EACH ROW EXECUTE FUNCTION public.prevent_privacy_execution_record_delete();
CREATE TRIGGER privacy_membership_history_replay_postconditions_immutable BEFORE UPDATE OR DELETE
 ON privacy_protected.membership_history_replay_postconditions FOR EACH ROW EXECUTE FUNCTION public.prevent_privacy_execution_record_delete();

-- Every value is uint32-big-endian length-prefixed UTF-8. NULL uses the
-- reserved 0xffffffff length, so NULL and the empty string never collide.
CREATE FUNCTION public.privacy_membership_history_frame(p_value text)
RETURNS bytea LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
 SELECT CASE WHEN p_value IS NULL THEN decode('ffffffff','hex')
  ELSE int4send(octet_length(convert_to(p_value,'UTF8')))||convert_to(p_value,'UTF8') END;
$$;
CREATE FUNCTION public.privacy_membership_history_frame(p_value bytea)
RETURNS bytea LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
 SELECT CASE WHEN p_value IS NULL THEN decode('ffffffff','hex')
  ELSE int4send(octet_length(p_value))||p_value END;
$$;

-- Both arguments are contractually 32 bytes. The loop always examines all
-- 32 positions and is the database-side companion to Go's constant-time
-- comparison before a replay receipt is returned.
CREATE FUNCTION public.privacy_membership_history_digest_equal(p_left bytea,p_right bytea)
RETURNS boolean LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE AS $$
DECLARE difference integer:=0;position integer;
BEGIN
 IF p_left IS NULL OR p_right IS NULL OR octet_length(p_left)<>32 OR octet_length(p_right)<>32 THEN RETURN false; END IF;
 FOR position IN 0..31 LOOP
  difference:=difference | (get_byte(p_left,position) # get_byte(p_right,position));
 END LOOP;
 RETURN difference=0;
END;$$;

CREATE FUNCTION public.privacy_membership_history_compute(
 p_membership_ids uuid[],p_effective_at timestamptz,p_require_anonymized boolean
) RETURNS TABLE(contract text,postcondition_sha256 bytea,membership_count bigint,variation_count bigint,canonical_size bigint)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE canonical bytea;membership_row record;child record;ids uuid[];actual_count bigint;principal_count bigint;principal_ref uuid;
 membership_total bigint;variation_total bigint;modality_total bigint;group_total bigint;variation_group_total bigint;
BEGIN
 ids:=COALESCE((SELECT array_agg(item.id ORDER BY item.id) FROM unnest(COALESCE(p_membership_ids,ARRAY[]::uuid[])) item(id)),ARRAY[]::uuid[]);
 membership_total:=cardinality(ids);
 IF p_effective_at IS NULL OR membership_total>10000 OR membership_total<>(SELECT count(DISTINCT id) FROM unnest(ids) item(id)) THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_postcondition_rejected'; END IF;
 SELECT count(*),count(DISTINCT membership.principal_id),min(membership.principal_id::text)::uuid
 INTO actual_count,principal_count,principal_ref FROM user_memberships membership WHERE membership.id=ANY(ids);
 IF actual_count<>membership_total OR EXISTS(SELECT 1 FROM user_memberships membership WHERE membership.id=ANY(ids)
   AND (membership.ends_on IS NULL OR membership.ends_on>=(p_effective_at AT TIME ZONE 'UTC')::date))
  OR (p_require_anonymized AND membership_total>0 AND (principal_count<>1 OR EXISTS(
    SELECT 1 FROM user_memberships membership WHERE membership.id=ANY(ids) AND (membership.user_id IS NOT NULL OR membership.principal_id IS NULL))))
  OR (p_require_anonymized AND membership_total>0 AND EXISTS(
    SELECT 1 FROM user_memberships membership WHERE membership.principal_id=principal_ref AND NOT(membership.id=ANY(ids)))) THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_postcondition_rejected'; END IF;
 SELECT count(*) INTO variation_total FROM training_variations variation WHERE variation.target_membership_id=ANY(ids);
 IF variation_total>100000 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_postcondition_rejected'; END IF;
 canonical:=privacy_membership_history_frame('mycfc/membership-history-postcondition/v1')||
  privacy_membership_history_frame((p_effective_at AT TIME ZONE 'UTC')::date::text)||
  privacy_membership_history_frame(membership_total::text);
 FOR membership_row IN SELECT membership.* FROM user_memberships membership WHERE membership.id=ANY(ids) ORDER BY membership.id LOOP
  SELECT count(*) INTO modality_total FROM membership_modalities modality WHERE modality.membership_id=membership_row.id;
  SELECT count(*) INTO group_total FROM training_group_members linked WHERE linked.membership_id=membership_row.id;
  SELECT count(*) INTO variation_group_total FROM training_variation_group_members linked WHERE linked.membership_id=membership_row.id;
  canonical:=canonical||privacy_membership_history_frame('membership')||privacy_membership_history_frame(membership_row.id::text)||
   privacy_membership_history_frame(membership_row.season_id::text)||privacy_membership_history_frame(membership_row.programme_id::text)||
   privacy_membership_history_frame(membership_row.team_id::text)||privacy_membership_history_frame(membership_row.competition_category_id::text)||
   privacy_membership_history_frame(membership_row.starts_on::text)||privacy_membership_history_frame(membership_row.ends_on::text)||
   privacy_membership_history_frame('HISTORICAL')||privacy_membership_history_frame(modality_total::text);
  FOR child IN SELECT modality.modality_id FROM membership_modalities modality WHERE modality.membership_id=membership_row.id ORDER BY modality.modality_id LOOP
   canonical:=canonical||privacy_membership_history_frame('modality')||privacy_membership_history_frame(child.modality_id::text);
   IF octet_length(canonical)>16777216 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_postcondition_rejected'; END IF;
  END LOOP;
  canonical:=canonical||privacy_membership_history_frame(group_total::text);
  FOR child IN SELECT linked.group_id FROM training_group_members linked WHERE linked.membership_id=membership_row.id ORDER BY linked.group_id LOOP
   canonical:=canonical||privacy_membership_history_frame('training-group')||privacy_membership_history_frame(child.group_id::text);
   IF octet_length(canonical)>16777216 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_postcondition_rejected'; END IF;
  END LOOP;
  canonical:=canonical||privacy_membership_history_frame(variation_group_total::text);
  FOR child IN SELECT linked.variation_group_id FROM training_variation_group_members linked WHERE linked.membership_id=membership_row.id ORDER BY linked.variation_group_id LOOP
   canonical:=canonical||privacy_membership_history_frame('variation-group')||privacy_membership_history_frame(child.variation_group_id::text);
   IF octet_length(canonical)>16777216 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_postcondition_rejected'; END IF;
  END LOOP;
  SELECT count(*) INTO actual_count FROM training_variations variation WHERE variation.target_membership_id=membership_row.id;
  canonical:=canonical||privacy_membership_history_frame(actual_count::text);
  FOR child IN SELECT variation.* FROM training_variations variation WHERE variation.target_membership_id=membership_row.id ORDER BY variation.id LOOP
   canonical:=canonical||privacy_membership_history_frame('variation')||privacy_membership_history_frame(child.id::text)||
    privacy_membership_history_frame(child.plan_id::text)||privacy_membership_history_frame(child.subject_kind::text)||
    privacy_membership_history_frame(child.subject_id::text)||privacy_membership_history_frame(child.operation::text)||
    privacy_membership_history_frame(child.change_summary::text)||privacy_membership_history_frame(child.patch::text)||
    privacy_membership_history_frame(child.version::text)||privacy_membership_history_frame(CASE WHEN child.is_active THEN 'true' ELSE 'false' END)||
    privacy_membership_history_frame(CASE WHEN child.retired_at IS NULL THEN NULL ELSE to_char(child.retired_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"') END);
   IF octet_length(canonical)>16777216 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_postcondition_rejected'; END IF;
  END LOOP;
  IF octet_length(canonical)>16777216 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_postcondition_rejected'; END IF;
 END LOOP;
 RETURN QUERY SELECT 'mycfc/membership-history-postcondition/v1'::text,digest(canonical,'sha256'),membership_total,variation_total,octet_length(canonical)::bigint;
END;$$;

CREATE FUNCTION public.privacy_membership_history_compute_source(p_execution_id uuid,p_effective_at timestamptz)
RETURNS TABLE(contract text,postcondition_sha256 bytea,membership_count bigint,variation_count bigint,canonical_size bigint)
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT computed.* FROM privacy_protected.membership_history_source_captures capture
 CROSS JOIN LATERAL public.privacy_membership_history_compute(
  COALESCE((SELECT array_agg(row.membership_id ORDER BY row.membership_id) FROM privacy_protected.membership_history_source_rows row WHERE row.execution_id=capture.execution_id),ARRAY[]::uuid[]),
  p_effective_at,true) computed WHERE capture.execution_id=p_execution_id;
$$;
CREATE FUNCTION public.privacy_membership_history_compute_replay(p_run_id uuid,p_effective_at timestamptz)
RETURNS TABLE(contract text,postcondition_sha256 bytea,membership_count bigint,variation_count bigint,canonical_size bigint)
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT computed.* FROM privacy_protected.membership_history_replay_captures capture
 CROSS JOIN LATERAL public.privacy_membership_history_compute(
  COALESCE((SELECT array_agg(row.membership_id ORDER BY row.membership_id) FROM privacy_protected.membership_history_replay_rows row WHERE row.run_id=capture.run_id),ARRAY[]::uuid[]),
  p_effective_at,true) computed WHERE capture.run_id=p_run_id;
$$;

-- Membership date boundaries are tied to the persisted erasure instant (or
-- execution start before identity clearing), never to a retry's wall clock.
CREATE FUNCTION public.privacy_membership_history_effective_date(p_execution_id uuid,p_subject_user_id uuid)
RETURNS date LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT (CASE WHEN subject.erasure_execution_id=execution.id AND subject.erased_at IS NOT NULL
              THEN subject.erased_at ELSE execution.started_at END AT TIME ZONE 'UTC')::date
 FROM privacy_erasure_executions execution JOIN users subject ON subject.id=p_subject_user_id
 WHERE execution.id=p_execution_id;
$$;

-- Upgrade the pre-v4 executor in place. Assert the exact number of legacy or
-- upgraded cutoff references so an unexpected predecessor cannot migrate.
DO $$DECLARE definition text;legacy_count integer;current_count integer;
 current_call text:='public.privacy_membership_history_effective_date(execution_ref,subject_ref)';
BEGIN
 SELECT pg_get_functiondef('public.privacy_worker_execute_checkpoint_without_tombstone_guard(uuid,uuid,uuid,bigint,uuid,text,text)'::regprocedure) INTO definition;
 legacy_count:=(length(definition)-length(replace(definition,'CURRENT_DATE','')))/length('CURRENT_DATE');
 current_count:=(length(definition)-length(replace(definition,current_call,'')))/length(current_call);
 IF legacy_count=12 AND current_count=0 THEN EXECUTE replace(definition,'CURRENT_DATE',current_call);
 ELSIF legacy_count<>0 OR current_count<>12 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_effective_date_predecessor_mismatch';
 END IF;
END$$;

-- Serialize sealing and every digest-covered mutation by membership ID.
CREATE FUNCTION public.privacy_membership_history_lock_source(p_execution_id uuid)
RETURNS void LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE membership_ref uuid;
BEGIN
 FOR membership_ref IN
  SELECT DISTINCT source_row.membership_id FROM privacy_protected.membership_history_source_rows source_row
  WHERE source_row.execution_id=p_execution_id ORDER BY source_row.membership_id
 LOOP
  PERFORM pg_advisory_xact_lock(hashtextextended('mycfc:privacy-membership-history:'||membership_ref::text,0));
 END LOOP;
END;$$;

CREATE FUNCTION public.privacy_membership_history_source_postcondition_ready(p_execution_id uuid)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
 PERFORM public.privacy_membership_history_lock_source(p_execution_id);
 RETURN EXISTS(
  SELECT 1
  FROM privacy_protected.membership_history_source_postconditions postcondition
  CROSS JOIN LATERAL public.privacy_membership_history_compute_source(postcondition.execution_id,postcondition.effective_at) computed
  WHERE postcondition.execution_id=p_execution_id
   AND postcondition.contract='mycfc/membership-history-postcondition/v1'
   AND computed.contract=postcondition.contract
   AND public.privacy_membership_history_digest_equal(computed.postcondition_sha256,postcondition.postcondition_sha256)
   AND computed.membership_count=postcondition.membership_count
   AND computed.variation_count=postcondition.variation_count
 );
END;
$$;

-- Once closure preparation seals a source postcondition, every digest-covered
-- membership row and relationship becomes immutable. Erasure itself runs
-- before that postcondition exists, so its required anonymisation is not
-- impeded.
CREATE FUNCTION public.prevent_sealed_membership_history_mutation()
RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE old_membership uuid;new_membership uuid;column_name text;
BEGIN
 column_name:=CASE TG_TABLE_NAME WHEN 'user_memberships' THEN 'id' WHEN 'training_variations' THEN 'target_membership_id' ELSE 'membership_id' END;
 IF TG_OP<>'INSERT' THEN old_membership:=NULLIF(to_jsonb(OLD)->>column_name,'')::uuid; END IF;
 IF TG_OP<>'DELETE' THEN new_membership:=NULLIF(to_jsonb(NEW)->>column_name,'')::uuid; END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended('mycfc:privacy-membership-history:'||locked.membership_ref::text,0))
 FROM (SELECT DISTINCT membership_ref FROM unnest(ARRAY[old_membership,new_membership]) membership_ref
       WHERE membership_ref IS NOT NULL ORDER BY membership_ref) locked;
 IF EXISTS(
  SELECT 1 FROM privacy_protected.membership_history_source_rows source_row
  JOIN privacy_protected.membership_history_source_postconditions postcondition ON postcondition.execution_id=source_row.execution_id
  WHERE source_row.membership_id=old_membership OR source_row.membership_id=new_membership
 ) THEN RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='sealed_membership_history_immutable'; END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END;$$;
CREATE TRIGGER user_memberships_sealed_history BEFORE INSERT OR UPDATE OR DELETE ON user_memberships
 FOR EACH ROW EXECUTE FUNCTION public.prevent_sealed_membership_history_mutation();
CREATE TRIGGER membership_modalities_sealed_history BEFORE INSERT OR UPDATE OR DELETE ON membership_modalities
 FOR EACH ROW EXECUTE FUNCTION public.prevent_sealed_membership_history_mutation();
CREATE TRIGGER training_group_members_sealed_history BEFORE INSERT OR UPDATE OR DELETE ON training_group_members
 FOR EACH ROW EXECUTE FUNCTION public.prevent_sealed_membership_history_mutation();
CREATE TRIGGER training_variation_group_members_sealed_history BEFORE INSERT OR UPDATE OR DELETE ON training_variation_group_members
 FOR EACH ROW EXECUTE FUNCTION public.prevent_sealed_membership_history_mutation();
CREATE TRIGGER training_variations_sealed_history BEFORE INSERT OR UPDATE OR DELETE ON training_variations
 FOR EACH ROW EXECUTE FUNCTION public.prevent_sealed_membership_history_mutation();

-- Capture the final retained membership row set immediately before the
-- history operation. Identity clearing runs earlier, so its wrapper scrubs
-- variation canaries while the original name/email/login still exist.
ALTER FUNCTION public.privacy_worker_execute_checkpoint(uuid,uuid,uuid,bigint,uuid,text,text)
 RENAME TO privacy_worker_execute_checkpoint_inner_015;
CREATE OR REPLACE FUNCTION public.privacy_worker_execute_checkpoint(
 p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_lease_epoch bigint,p_worker_ref uuid,p_operation_code text,p_action_version text
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE execution_ref uuid;subject_ref uuid;checkpoint_status text;subject_name text;subject_email text;subject_login text;capture_inserted bigint;
 previous_timezone text;checkpoint_ref uuid;
BEGIN
 IF p_operation_code='MEMBERSHIP_ACTIVE_REVOKE' THEN
  previous_timezone:=current_setting('TimeZone');
  PERFORM set_config('TimeZone','UTC',true);
 END IF;
 PERFORM public.privacy_worker_require_activation();
 SELECT execution.id,request.subject_user_id,checkpoint.status INTO execution_ref,subject_ref,checkpoint_status
 FROM privacy_erasure_category_jobs job
 JOIN privacy_erasure_job_leases lease ON lease.id=p_lease_id AND lease.job_id=job.id AND lease.epoch=p_lease_epoch
 JOIN privacy_erasure_job_attempts attempt ON attempt.id=p_attempt_id AND attempt.job_id=job.id AND attempt.lease_id=lease.id AND attempt.lease_epoch=p_lease_epoch
 JOIN privacy_erasure_job_checkpoints checkpoint ON checkpoint.job_id=job.id AND checkpoint.operation_code=p_operation_code AND checkpoint.action_version=p_action_version
 JOIN privacy_erasure_executions execution ON execution.id=job.execution_id
 JOIN data_erasure_requests request ON request.id=execution.request_id
 WHERE job.id=p_job_id AND job.status='LEASED' AND lease.worker_ref=p_worker_ref AND lease.released_at IS NULL
  AND lease.expires_at>clock_timestamp() AND attempt.finished_at IS NULL
  AND NOT EXISTS(SELECT 1 FROM privacy_erasure_job_checkpoints prior WHERE prior.job_id=job.id AND prior.operation_position<checkpoint.operation_position AND prior.status<>'SUCCEEDED')
  AND NOT EXISTS(SELECT 1 FROM privacy_erasure_category_jobs prior WHERE prior.execution_id=execution.id AND prior.plan_entry_position<job.plan_entry_position AND prior.status<>'SUCCEEDED')
 FOR UPDATE OF job,lease,attempt,checkpoint;
 IF execution_ref IS NULL OR subject_ref IS NULL THEN
  checkpoint_ref:=public.privacy_worker_execute_checkpoint_inner_015(p_job_id,p_lease_id,p_attempt_id,p_lease_epoch,p_worker_ref,p_operation_code,p_action_version);
  IF previous_timezone IS NOT NULL THEN PERFORM set_config('TimeZone',previous_timezone,true); END IF;
  RETURN checkpoint_ref;
 END IF;
 IF checkpoint_status='PENDING' AND p_operation_code='IDENTITY_CLEAR' AND EXISTS(
  SELECT 1 FROM privacy_erasure_category_jobs membership_job JOIN privacy_erasure_job_checkpoints membership_checkpoint ON membership_checkpoint.job_id=membership_job.id
  WHERE membership_job.execution_id=execution_ref AND membership_checkpoint.operation_code='MEMBERSHIP_HISTORY_ANONYMIZE') THEN
  SELECT name,email::text,minor_login_id::text INTO subject_name,subject_email,subject_login FROM users WHERE id=subject_ref FOR UPDATE;
  PERFORM set_config('mycfc.privacy_erasure_operation','MEMBERSHIP_HISTORY_ANONYMIZE',true);
  UPDATE training_variations SET
   change_summary=privacy_scrub_audit_text(change_summary,subject_ref,subject_name,subject_email,subject_login),
   patch=privacy_scrub_audit_json(patch,subject_ref,subject_name,subject_email,subject_login),updated_at=clock_timestamp()
  WHERE target_membership_id IN(SELECT id FROM user_memberships WHERE user_id=subject_ref);
 END IF;
 IF checkpoint_status='PENDING' AND p_operation_code='MEMBERSHIP_HISTORY_ANONYMIZE' THEN
  INSERT INTO privacy_protected.membership_history_source_captures(execution_id) VALUES(execution_ref) ON CONFLICT DO NOTHING;
  GET DIAGNOSTICS capture_inserted=ROW_COUNT;
  IF capture_inserted<>1 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_capture_conflict'; END IF;
  INSERT INTO privacy_protected.membership_history_source_rows(execution_id,membership_id)
   SELECT execution_ref,membership.id FROM user_memberships membership WHERE membership.user_id=subject_ref ORDER BY membership.id;
 END IF;
 checkpoint_ref:=public.privacy_worker_execute_checkpoint_inner_015(p_job_id,p_lease_id,p_attempt_id,p_lease_epoch,p_worker_ref,p_operation_code,p_action_version);
 IF previous_timezone IS NOT NULL THEN PERFORM set_config('TimeZone',previous_timezone,true); END IF;
 RETURN checkpoint_ref;
END;$$;

CREATE FUNCTION public.privacy_tombstone_prepare_closure_v4(p_execution_id uuid,p_worker_ref uuid)
RETURNS TABLE(execution_id uuid,request_id uuid,request_ref uuid,subject_user_id uuid,plan_sha256 bytea,workset_sha256 bytea,
 execution_started_at timestamptz,closed_at timestamptz,evidence_expires_at timestamptz,erasure_effective_at timestamptz,replay_operations text[],
 membership_postcondition_contract text,membership_postcondition_sha256 bytea,membership_count bigint,variation_count bigint)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE prepared record;computed record;existing privacy_protected.membership_history_source_postconditions%ROWTYPE;
BEGIN
 PERFORM public.privacy_worker_require_activation();
 SELECT * INTO prepared FROM public.privacy_tombstone_prepare_closure_v3(p_execution_id,p_worker_ref);
 IF prepared.execution_id IS NULL OR EXISTS(SELECT 1 FROM privacy_protected.restore_tombstone_closure_receipts receipt
   WHERE receipt.execution_id=p_execution_id AND receipt.ledger_version<>'restore-tombstone-closure/v4') THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_tombstone_closure_unavailable'; END IF;
 IF 'MEMBERSHIP_HISTORY_ANONYMIZE'=ANY(prepared.replay_operations) THEN
  IF NOT EXISTS(SELECT 1 FROM privacy_protected.membership_history_source_captures capture WHERE capture.execution_id=p_execution_id) THEN
   RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_postcondition_unavailable'; END IF;
 ELSE INSERT INTO privacy_protected.membership_history_source_captures(execution_id) VALUES(p_execution_id) ON CONFLICT DO NOTHING;
 END IF;
 PERFORM public.privacy_membership_history_lock_source(p_execution_id);
 SELECT * INTO computed FROM public.privacy_membership_history_compute_source(p_execution_id,prepared.erasure_effective_at);
 IF computed.contract IS NULL THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_postcondition_unavailable'; END IF;
 SELECT * INTO existing FROM privacy_protected.membership_history_source_postconditions WHERE membership_history_source_postconditions.execution_id=p_execution_id;
 IF existing.execution_id IS NULL THEN
  INSERT INTO privacy_protected.membership_history_source_postconditions(execution_id,effective_at,contract,postcondition_sha256,membership_count,variation_count,canonical_size)
  VALUES(p_execution_id,prepared.erasure_effective_at,computed.contract,computed.postcondition_sha256,computed.membership_count,computed.variation_count,computed.canonical_size);
 ELSIF existing.effective_at IS DISTINCT FROM prepared.erasure_effective_at OR existing.contract<>computed.contract
  OR NOT public.privacy_membership_history_digest_equal(existing.postcondition_sha256,computed.postcondition_sha256)
  OR existing.membership_count<>computed.membership_count OR existing.variation_count<>computed.variation_count OR existing.canonical_size<>computed.canonical_size THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_postcondition_conflict'; END IF;
 RETURN QUERY SELECT prepared.execution_id,prepared.request_id,prepared.request_ref,prepared.subject_user_id,prepared.plan_sha256,prepared.workset_sha256,
  prepared.execution_started_at,prepared.closed_at,prepared.evidence_expires_at,prepared.erasure_effective_at,prepared.replay_operations,
  computed.contract,computed.postcondition_sha256,computed.membership_count,computed.variation_count;
END;$$;

CREATE FUNCTION public.privacy_tombstone_confirm_closure_v4(
 p_execution_id uuid,p_worker_ref uuid,p_ledger_version text,p_encryption_key_id text,p_locator_key_id text,p_locator_digest bytea,
 p_object_version_id text,p_ciphertext_sha256 bytea,p_size_bytes bigint,p_written_at timestamptz,p_verified_at timestamptz
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE intent privacy_protected.restore_tombstone_closure_intents%ROWTYPE;existing privacy_protected.restore_tombstone_closure_receipts%ROWTYPE;
BEGIN
 PERFORM public.privacy_worker_require_activation();
 SELECT * INTO intent FROM privacy_protected.restore_tombstone_closure_intents WHERE execution_id=p_execution_id;
 IF intent.execution_id IS NULL OR p_worker_ref IS NULL OR p_ledger_version<>'restore-tombstone-closure/v4'
  OR p_encryption_key_id IS NULL OR p_encryption_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'
  OR p_locator_key_id IS NULL OR p_locator_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'
  OR octet_length(p_locator_digest)<>32 OR octet_length(p_ciphertext_sha256)<>32 OR p_object_version_id IS NULL
  OR p_object_version_id<>btrim(p_object_version_id) OR char_length(p_object_version_id) NOT BETWEEN 1 AND 1024
  OR p_size_bytes NOT BETWEEN 1 AND 1048576 OR p_written_at IS NULL OR p_verified_at<p_written_at OR p_verified_at>intent.evidence_expires_at
  OR NOT public.privacy_membership_history_source_postcondition_ready(p_execution_id)
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_tombstone_closure_receipt_rejected'; END IF;
 SELECT * INTO existing FROM privacy_protected.restore_tombstone_closure_receipts WHERE execution_id=p_execution_id;
 IF existing.execution_id IS NULL THEN
  INSERT INTO privacy_protected.restore_tombstone_closure_receipts(execution_id,ledger_version,encryption_key_id,locator_key_id,locator_digest,object_version_id,ciphertext_sha256,size_bytes,written_at,verified_at)
  VALUES(p_execution_id,p_ledger_version,p_encryption_key_id,p_locator_key_id,p_locator_digest,p_object_version_id,p_ciphertext_sha256,p_size_bytes,p_written_at,p_verified_at);
 ELSIF ROW(existing.ledger_version,existing.encryption_key_id,existing.locator_key_id,existing.locator_digest,existing.object_version_id,existing.ciphertext_sha256,existing.size_bytes,existing.written_at,existing.verified_at)
  IS DISTINCT FROM ROW(p_ledger_version,p_encryption_key_id,p_locator_key_id,p_locator_digest,p_object_version_id,p_ciphertext_sha256,p_size_bytes,p_written_at,p_verified_at)
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_tombstone_closure_receipt_conflict'; END IF;
 RETURN p_execution_id;
END;$$;

ALTER TABLE privacy_protected.restore_synthetic_fixtures
 ADD COLUMN membership_postcondition_contract varchar(64) NULL,
 ADD COLUMN membership_postcondition_sha256 bytea NULL,
 ADD COLUMN membership_count bigint NULL,
 ADD COLUMN variation_count bigint NULL,
 ADD CONSTRAINT restore_synthetic_fixtures_membership_postcondition_check CHECK((
  (membership_postcondition_contract IS NULL AND membership_postcondition_sha256 IS NULL AND membership_count IS NULL AND variation_count IS NULL)
  OR (membership_postcondition_contract='mycfc/membership-history-postcondition/v1'
   AND octet_length(membership_postcondition_sha256)=32
   AND membership_count BETWEEN 0 AND 10000 AND variation_count BETWEEN 0 AND 100000)) IS TRUE) NOT VALID;
ALTER TABLE privacy_protected.restore_synthetic_fixtures VALIDATE CONSTRAINT restore_synthetic_fixtures_membership_postcondition_check;

DROP FUNCTION public.privacy_restore_create_synthetic_fixture(uuid);
CREATE FUNCTION public.privacy_restore_create_synthetic_fixture(p_worker_ref uuid)
RETURNS TABLE(source_execution_id uuid,source_request_id uuid,source_request_ref uuid,subject_user_id uuid,
 plan_sha256 bytea,workset_sha256 bytea,erasure_effective_at timestamptz,operations text[],
 membership_postcondition_contract text,membership_postcondition_sha256 bytea,membership_count bigint,variation_count bigint)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE v_subject uuid:=gen_random_uuid();v_execution uuid:=gen_random_uuid();v_request uuid:=gen_random_uuid();v_ref uuid:=gen_random_uuid();
 v_plan bytea:=gen_random_bytes(32);v_workset bytea:=gen_random_bytes(32);v_effective timestamptz:=date_trunc('microseconds',clock_timestamp());
 v_operations text[]:=ARRAY['AUTH_ACCESS_REVOKE','AUTH_TOKEN_DELETE','PROFILE_IDENTITY_DELETE','PROVIDER_LOCAL_FENCE','IDENTITY_CLEAR','MEMBERSHIP_ACTIVE_REVOKE','MEMBERSHIP_HISTORY_ANONYMIZE'];
 v_season uuid:=gen_random_uuid();v_membership uuid:=gen_random_uuid();v_group uuid:=gen_random_uuid();v_plan_id uuid:=gen_random_uuid();v_variation_group uuid:=gen_random_uuid();
 v_programme uuid;v_modality uuid;computed record;original_summary text;original_patch jsonb;
BEGIN
 IF p_worker_ref IS NULL OR current_setting('mycfc.privacy_restore_isolated',true)<>'on' THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_synthetic_fixture_rejected'; END IF;
 SELECT id INTO v_programme FROM programmes ORDER BY id LIMIT 1;
 SELECT id INTO v_modality FROM modalities ORDER BY id LIMIT 1;
 IF v_programme IS NULL OR v_modality IS NULL THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_synthetic_fixture_unavailable'; END IF;
 INSERT INTO users(id,name,email,password_hash,date_of_birth,created_at,updated_at)
 VALUES(v_subject,'Synthetic restore fixture',('synthetic-'||v_subject::text||'@invalid.invalid')::citext,'synthetic-disabled','1900-01-01',v_effective,v_effective);
 INSERT INTO member_profiles(user_id,address_line1,created_at,updated_at) VALUES(v_subject,'Synthetic fixture only',v_effective,v_effective);
 INSERT INTO seasons(id,code,name,starts_on,ends_on,is_current,created_at)
 VALUES(v_season,'SYN-'||substr(v_season::text,1,8),'Synthetic restore fixture',v_effective::date-interval '2 years',v_effective::date-interval '1 year',false,v_effective);
 INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on,ends_on,created_at,updated_at)
 VALUES(v_membership,v_subject,v_season,v_programme,v_effective::date-interval '2 years',v_effective::date-interval '1 year',v_effective,v_effective);
 INSERT INTO membership_modalities(membership_id,modality_id,created_at) VALUES(v_membership,v_modality,v_effective);
 INSERT INTO training_groups(id,name,programme_id,created_by_id,created_at,updated_at)
 VALUES(v_group,'Synthetic restore fixture',v_programme,v_subject,v_effective,v_effective);
 INSERT INTO training_group_members(group_id,membership_id,added_by_id,added_at) VALUES(v_group,v_membership,v_subject,v_effective);
 INSERT INTO training_plans(id,title,programme_id,created_by_id,created_at,updated_at)
 VALUES(v_plan_id,'Synthetic restore fixture',v_programme,v_subject,v_effective,v_effective);
 INSERT INTO training_variation_groups(id,training_group_id,name,kind,effective_from,effective_until,created_by_id,created_at,updated_at)
 VALUES(v_variation_group,v_group,'Synthetic restore fixture','SUBGROUP',v_effective::date-interval '2 years',v_effective::date-interval '1 year',v_subject,v_effective,v_effective);
 INSERT INTO training_variation_group_members(variation_group_id,membership_id,added_by_id,added_at)
 VALUES(v_variation_group,v_membership,v_subject,v_effective);
 original_summary:='Fixture '||v_subject::text||' synthetic-'||v_subject::text||'@invalid.invalid';
 original_patch:=jsonb_build_object('old_name','Synthetic restore fixture','old_login',v_subject::text,'nested',jsonb_build_object('old_email','synthetic-'||v_subject::text||'@invalid.invalid'));
 INSERT INTO training_variations(plan_id,target_membership_id,subject_kind,subject_id,operation,change_summary,patch,created_by_id,created_at,updated_at)
 VALUES(v_plan_id,v_membership,'SEGMENT',gen_random_uuid(),'OVERRIDE',original_summary,original_patch,v_subject,v_effective,v_effective);
 UPDATE training_variations SET change_summary=privacy_scrub_audit_text(change_summary,v_subject,'Synthetic restore fixture','synthetic-'||v_subject::text||'@invalid.invalid',v_subject::text),
  patch=privacy_scrub_audit_json(patch,v_subject,'Synthetic restore fixture','synthetic-'||v_subject::text||'@invalid.invalid',v_subject::text)
 WHERE target_membership_id=v_membership;
 SELECT * INTO computed FROM public.privacy_membership_history_compute(ARRAY[v_membership],v_effective,false);
 UPDATE training_variations SET change_summary=original_summary,patch=original_patch WHERE target_membership_id=v_membership;
 INSERT INTO privacy_protected.provider_connections(id,subject_user_id,service_code,provider_role,provider_contract_version,
  registry_evidence_key_id,registry_evidence_digest,target_key_id,target_opaque,credential_key_id,credential_opaque,state,
  sync_enabled,webhook_enabled,reconnect_enabled,created_at,updated_at)
 VALUES(gen_random_uuid(),v_subject,'synthetic.restore.fixture','PROCESSOR','synthetic-v1','synthetic-key',gen_random_bytes(32),
  'synthetic-key',gen_random_bytes(32),'synthetic-key',gen_random_bytes(32),'ACTIVE',true,true,true,v_effective,v_effective);
 INSERT INTO privacy_protected.restore_synthetic_fixtures(subject_user_id,source_execution_id,source_request_id,source_request_ref,
  plan_sha256,workset_sha256,erasure_effective_at,operations,fixture_marker,created_by_ref,created_at,
  membership_postcondition_contract,membership_postcondition_sha256,membership_count,variation_count)
 VALUES(v_subject,v_execution,v_request,v_ref,v_plan,v_workset,v_effective,v_operations,'mycfc/privacy-restore-synthetic-fixture/v1',p_worker_ref,v_effective,
  computed.contract,computed.postcondition_sha256,computed.membership_count,computed.variation_count);
 RETURN QUERY SELECT v_execution,v_request,v_ref,v_subject,v_plan,v_workset,v_effective,v_operations,
  computed.contract,computed.postcondition_sha256,computed.membership_count,computed.variation_count;
END;$$;

CREATE FUNCTION public.privacy_restore_import_authenticated_v4_hardened(
 p_worker_ref uuid,p_kind text,p_record_version text,p_envelope_version text,p_encryption_key_id text,p_locator_key_id text,p_locator_digest bytea,
 p_ciphertext_sha256 bytea,p_object_version_id text,p_written_at timestamptz,p_verified_at timestamptz,p_retain_until timestamptz,
 p_source_execution_id uuid,p_source_request_id uuid,p_source_request_ref uuid,p_subject_user_id uuid,p_plan_sha256 bytea,p_workset_sha256 bytea,
 p_execution_started_at timestamptz,p_erasure_effective_at timestamptz,p_closure_version text,p_synthetic_fixture text,p_replay_version text,p_action_version text,
 p_operations text[],p_prescription_sha256 bytea,p_record_sha256 bytea,p_membership_postcondition_contract text,p_membership_postcondition_sha256 bytea,
 p_membership_count bigint,p_variation_count bigint
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE import_ref uuid;existing privacy_protected.restore_ledger_imports%ROWTYPE;synthetic_match boolean;
BEGIN
 SELECT EXISTS(SELECT 1 FROM privacy_protected.restore_synthetic_fixtures fixture
  WHERE fixture.subject_user_id=p_subject_user_id AND fixture.source_execution_id=p_source_execution_id
   AND fixture.source_request_id=p_source_request_id AND fixture.source_request_ref=p_source_request_ref
   AND fixture.plan_sha256=p_plan_sha256 AND fixture.workset_sha256=p_workset_sha256
   AND fixture.erasure_effective_at=p_erasure_effective_at AND fixture.operations=p_operations AND fixture.fixture_marker=p_synthetic_fixture
   AND fixture.membership_postcondition_contract=p_membership_postcondition_contract
   AND public.privacy_membership_history_digest_equal(fixture.membership_postcondition_sha256,p_membership_postcondition_sha256)
   AND fixture.membership_count=p_membership_count AND fixture.variation_count=p_variation_count) INTO synthetic_match;
 IF p_worker_ref IS NULL OR p_kind<>'closure' OR p_record_version<>'restore-tombstone/v2'
  OR p_envelope_version<>'x25519-aes256gcm-hkdfsha256/v2' OR p_closure_version<>'restore-tombstone-closure/v4'
  OR p_replay_version<>'relational-erasure-replay/v1' OR p_action_version<>'v1'
  OR p_encryption_key_id IS NULL OR p_encryption_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'
  OR p_locator_key_id IS NULL OR p_locator_key_id!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'
  OR octet_length(p_locator_digest)<>32 OR octet_length(p_ciphertext_sha256)<>32 OR octet_length(p_plan_sha256)<>32
  OR octet_length(p_workset_sha256)<>32 OR octet_length(p_prescription_sha256)<>32 OR octet_length(p_record_sha256)<>32
  OR p_membership_postcondition_contract<>'mycfc/membership-history-postcondition/v1' OR octet_length(p_membership_postcondition_sha256) IS DISTINCT FROM 32
  OR p_membership_count NOT BETWEEN 0 AND 10000 OR p_variation_count NOT BETWEEN 0 AND 100000
  OR p_object_version_id IS NULL OR p_object_version_id<>btrim(p_object_version_id) OR char_length(p_object_version_id) NOT BETWEEN 1 AND 1024
  OR p_written_at IS NULL OR p_verified_at<p_written_at OR p_retain_until IS NULL OR p_verified_at>p_retain_until
  OR p_source_execution_id IS NULL OR p_source_request_id IS NULL OR p_source_request_ref IS NULL OR p_subject_user_id IS NULL
  OR p_execution_started_at IS NULL OR p_erasure_effective_at IS NULL OR p_erasure_effective_at<p_execution_started_at
  OR cardinality(p_operations) NOT BETWEEN 1 AND 17 OR cardinality(p_operations)<>(SELECT count(DISTINCT operation) FROM unnest(p_operations) operation)
  OR EXISTS(SELECT 1 FROM unnest(p_operations) operation WHERE NOT(public.privacy_relational_replay_operation_supported(operation) OR operation='PROVIDER_LOCAL_FENCE'))
  OR (p_synthetic_fixture IS NULL AND EXISTS(SELECT 1 FROM privacy_protected.restore_synthetic_fixtures WHERE subject_user_id=p_subject_user_id))
  OR (p_synthetic_fixture IS NOT NULL AND (p_synthetic_fixture<>'mycfc/privacy-restore-synthetic-fixture/v1' OR NOT synthetic_match))
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_import_rejected'; END IF;
 SELECT * INTO existing FROM privacy_protected.restore_ledger_imports
  WHERE (locator_key_id=p_locator_key_id AND locator_digest=p_locator_digest) OR source_execution_id=p_source_execution_id FOR UPDATE;
 IF existing.id IS NULL THEN
  INSERT INTO privacy_protected.restore_ledger_imports(kind,record_version,envelope_version,encryption_key_id,locator_key_id,locator_digest,
   ciphertext_sha256,object_version_id,written_at,verified_at,retain_until,source_execution_id,source_request_id,source_request_ref,subject_user_id,
   plan_sha256,workset_sha256,execution_started_at,erasure_effective_at,closure_version,synthetic_fixture,replay_version,action_version,operations,
   prescription_sha256,record_sha256,imported_by_ref,membership_postcondition_contract,membership_postcondition_sha256,membership_count,variation_count)
  VALUES(p_kind,p_record_version,p_envelope_version,p_encryption_key_id,p_locator_key_id,p_locator_digest,p_ciphertext_sha256,p_object_version_id,
   p_written_at,p_verified_at,p_retain_until,p_source_execution_id,p_source_request_id,p_source_request_ref,p_subject_user_id,p_plan_sha256,p_workset_sha256,
   p_execution_started_at,p_erasure_effective_at,p_closure_version,p_synthetic_fixture,p_replay_version,p_action_version,p_operations,p_prescription_sha256,p_record_sha256,p_worker_ref,
   p_membership_postcondition_contract,p_membership_postcondition_sha256,p_membership_count,p_variation_count) RETURNING id INTO import_ref;
 ELSIF ROW(existing.kind,existing.record_version,existing.envelope_version,existing.encryption_key_id,existing.locator_key_id,existing.locator_digest,
   existing.ciphertext_sha256,existing.object_version_id,existing.written_at,existing.verified_at,existing.retain_until,existing.source_execution_id,
   existing.source_request_id,existing.source_request_ref,existing.subject_user_id,existing.plan_sha256,existing.workset_sha256,existing.execution_started_at,
   existing.erasure_effective_at,existing.closure_version,existing.synthetic_fixture,existing.replay_version,existing.action_version,existing.operations,
   existing.prescription_sha256,existing.record_sha256,existing.membership_postcondition_contract,existing.membership_postcondition_sha256,existing.membership_count,existing.variation_count)
  IS DISTINCT FROM ROW(p_kind,p_record_version,p_envelope_version,p_encryption_key_id,p_locator_key_id,p_locator_digest,p_ciphertext_sha256,
   p_object_version_id,p_written_at,p_verified_at,p_retain_until,p_source_execution_id,p_source_request_id,p_source_request_ref,p_subject_user_id,
   p_plan_sha256,p_workset_sha256,p_execution_started_at,p_erasure_effective_at,p_closure_version,p_synthetic_fixture,p_replay_version,p_action_version,p_operations,
   p_prescription_sha256,p_record_sha256,p_membership_postcondition_contract,p_membership_postcondition_sha256,p_membership_count,p_variation_count)
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_import_conflict'; ELSE import_ref:=existing.id; END IF;
 RETURN import_ref;
END;$$;

CREATE FUNCTION public.privacy_restore_finalize_membership_postcondition(p_run_id uuid)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE imported privacy_protected.restore_ledger_imports%ROWTYPE;computed record;existing privacy_protected.membership_history_replay_postconditions%ROWTYPE;
BEGIN
 SELECT imported_row.* INTO imported FROM privacy_protected.restore_replay_runs run
 JOIN privacy_protected.restore_ledger_imports imported_row ON imported_row.id=run.import_id WHERE run.id=p_run_id;
 IF imported.id IS NULL OR imported.closure_version<>'restore-tombstone-closure/v4' THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_postcondition_rejected'; END IF;
 SELECT * INTO computed FROM public.privacy_membership_history_compute_replay(p_run_id,imported.erasure_effective_at);
 IF computed.contract IS NULL OR computed.contract<>imported.membership_postcondition_contract
  OR NOT public.privacy_membership_history_digest_equal(computed.postcondition_sha256,imported.membership_postcondition_sha256)
  OR computed.membership_count<>imported.membership_count OR computed.variation_count<>imported.variation_count THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_postcondition_mismatch'; END IF;
 SELECT * INTO existing FROM privacy_protected.membership_history_replay_postconditions WHERE run_id=p_run_id;
 IF existing.run_id IS NULL THEN
  INSERT INTO privacy_protected.membership_history_replay_postconditions(run_id,contract,postcondition_sha256,membership_count,variation_count,canonical_size)
  VALUES(p_run_id,computed.contract,computed.postcondition_sha256,computed.membership_count,computed.variation_count,computed.canonical_size);
 ELSIF existing.contract<>computed.contract OR NOT public.privacy_membership_history_digest_equal(existing.postcondition_sha256,computed.postcondition_sha256)
  OR existing.membership_count<>computed.membership_count OR existing.variation_count<>computed.variation_count OR existing.canonical_size<>computed.canonical_size THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_postcondition_conflict'; END IF;
END;$$;

ALTER FUNCTION public.privacy_restore_verify_operation(uuid,text,boolean) RENAME TO privacy_restore_verify_operation_inner_015;
CREATE OR REPLACE FUNCTION public.privacy_restore_verify_operation(p_run_id uuid,p_operation_code text,p_source_already_applied boolean)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE imported privacy_protected.restore_ledger_imports%ROWTYPE;subject_email text;subject_login text;
BEGIN
 SELECT imported_row.* INTO imported FROM privacy_protected.restore_replay_runs run JOIN privacy_protected.restore_ledger_imports imported_row ON imported_row.id=run.import_id
 WHERE run.id=p_run_id;
 IF imported.id IS NULL OR imported.kind<>'closure' OR imported.closure_version<>'restore-tombstone-closure/v4' OR NOT(p_operation_code=ANY(imported.operations)) THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 IF p_operation_code NOT IN ('MEMBERSHIP_ACTIVE_REVOKE','MEMBERSHIP_HISTORY_ANONYMIZE') THEN
  PERFORM public.privacy_restore_verify_operation_inner_013(p_run_id,p_operation_code,p_source_already_applied); RETURN; END IF;
 IF p_source_already_applied AND NOT EXISTS(SELECT 1 FROM users WHERE id=imported.subject_user_id
  AND erasure_execution_id=imported.source_execution_id AND erased_at=imported.erasure_effective_at) THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 SELECT email::text,minor_login_id INTO subject_email,subject_login FROM users WHERE id=imported.subject_user_id;
 IF p_operation_code='MEMBERSHIP_ACTIVE_REVOKE' THEN
  IF EXISTS(SELECT 1 FROM user_memberships WHERE user_id=imported.subject_user_id
   AND (starts_on>=(imported.erasure_effective_at AT TIME ZONE 'UTC')::date OR ends_on IS NULL OR ends_on>=(imported.erasure_effective_at AT TIME ZONE 'UTC')::date)) THEN
   RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 ELSE
  IF EXISTS(SELECT 1 FROM user_memberships WHERE user_id=imported.subject_user_id)
   OR EXISTS(SELECT 1 FROM training_variations variation
    WHERE variation.target_membership_id IN(SELECT row.membership_id FROM privacy_protected.membership_history_replay_rows row WHERE row.run_id=p_run_id)
     AND (variation.change_summary IS DISTINCT FROM privacy_scrub_audit_text(variation.change_summary,imported.subject_user_id,NULL,subject_email,subject_login)
      OR variation.patch IS DISTINCT FROM privacy_scrub_audit_json(variation.patch,imported.subject_user_id,NULL,subject_email,subject_login))) THEN
   RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
  PERFORM public.privacy_restore_finalize_membership_postcondition(p_run_id);
 END IF;
END;$$;

ALTER FUNCTION public.privacy_restore_begin_replay_hardened(uuid,uuid) RENAME TO privacy_restore_begin_replay_hardened_inner_015;
CREATE OR REPLACE FUNCTION public.privacy_restore_begin_replay_hardened(p_import_id uuid,p_worker_ref uuid)
RETURNS TABLE(run_id uuid,outcome_code text) LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE imported privacy_protected.restore_ledger_imports%ROWTYPE;run_ref uuid;outcome text;has_erasure boolean;
BEGIN
 SELECT * INTO imported FROM privacy_protected.restore_ledger_imports WHERE id=p_import_id;
 IF imported.id IS NULL OR imported.kind<>'closure' OR imported.closure_version<>'restore-tombstone-closure/v4' THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_replay_rejected'; END IF;
 run_ref:=public.privacy_restore_begin_replay(p_import_id,p_worker_ref);
 INSERT INTO privacy_protected.membership_history_replay_captures(run_id) VALUES(run_ref) ON CONFLICT DO NOTHING;
 SELECT EXISTS(SELECT 1 FROM users WHERE id=imported.subject_user_id AND erased_at IS NOT NULL) INTO has_erasure;
 IF has_erasure THEN
  INSERT INTO privacy_protected.membership_history_replay_rows(run_id,membership_id)
   SELECT run_ref,row.membership_id FROM privacy_protected.membership_history_source_rows row WHERE row.execution_id=imported.source_execution_id
   ON CONFLICT DO NOTHING;
 ELSE
  INSERT INTO privacy_protected.membership_history_replay_rows(run_id,membership_id)
   SELECT run_ref,membership.id FROM user_memberships membership
   WHERE membership.user_id=imported.subject_user_id AND membership.starts_on<(imported.erasure_effective_at AT TIME ZONE 'UTC')::date
   ON CONFLICT DO NOTHING;
 END IF;
 SELECT begun.run_id,begun.outcome_code INTO run_ref,outcome FROM public.privacy_restore_begin_replay_hardened_inner_013(p_import_id,p_worker_ref) begun;
 IF outcome='ALREADY_APPLIED_SOURCE' AND NOT EXISTS(SELECT 1 FROM privacy_protected.membership_history_replay_postconditions postcondition WHERE postcondition.run_id=run_ref) THEN
  -- Empty prescriptions do not pass through the history checkpoint.
  IF NOT('MEMBERSHIP_HISTORY_ANONYMIZE'=ANY(imported.operations)) THEN PERFORM public.privacy_restore_finalize_membership_postcondition(run_ref); END IF;
 END IF;
 RETURN QUERY SELECT run_ref,outcome;
END;$$;

ALTER FUNCTION public.privacy_restore_execute_checkpoint(uuid,uuid,smallint,text,text,bytea) RENAME TO privacy_restore_execute_checkpoint_inner_015;
CREATE OR REPLACE FUNCTION public.privacy_restore_execute_checkpoint(p_run_id uuid,p_worker_ref uuid,p_operation_position smallint,p_operation_code text,p_action_version text,p_prescription_sha256 bytea)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE imported privacy_protected.restore_ledger_imports%ROWTYPE;subject_name text;subject_email text;subject_login text;checkpoint_ref uuid;
 previous_timezone text;
BEGIN
 SELECT imported_row.* INTO imported FROM privacy_protected.restore_replay_runs run
 JOIN privacy_protected.restore_ledger_imports imported_row ON imported_row.id=run.import_id WHERE run.id=p_run_id;
 IF imported.id IS NULL OR imported.closure_version<>'restore-tombstone-closure/v4' THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_checkpoint_rejected'; END IF;
 IF p_operation_code='IDENTITY_CLEAR' AND 'MEMBERSHIP_HISTORY_ANONYMIZE'=ANY(imported.operations) THEN
  SELECT name,email::text,minor_login_id::text INTO subject_name,subject_email,subject_login FROM users WHERE id=imported.subject_user_id FOR UPDATE;
  UPDATE training_variations SET
   change_summary=privacy_scrub_audit_text(change_summary,imported.subject_user_id,subject_name,subject_email,subject_login),
   patch=privacy_scrub_audit_json(patch,imported.subject_user_id,subject_name,subject_email,subject_login),updated_at=clock_timestamp()
  WHERE target_membership_id IN(SELECT row.membership_id FROM privacy_protected.membership_history_replay_rows row WHERE row.run_id=p_run_id);
 END IF;
 IF p_operation_code='MEMBERSHIP_ACTIVE_REVOKE' THEN
  previous_timezone:=current_setting('TimeZone');
  PERFORM set_config('TimeZone','UTC',true);
 END IF;
 checkpoint_ref:=public.privacy_restore_execute_checkpoint_inner_013(p_run_id,p_worker_ref,p_operation_position,p_operation_code,p_action_version,p_prescription_sha256);
 IF previous_timezone IS NOT NULL THEN PERFORM set_config('TimeZone',previous_timezone,true); END IF;
 IF NOT('MEMBERSHIP_HISTORY_ANONYMIZE'=ANY(imported.operations)) AND NOT EXISTS(
  SELECT 1 FROM privacy_protected.restore_replay_checkpoints checkpoint WHERE checkpoint.run_id=p_run_id AND checkpoint.status<>'SUCCEEDED') THEN
  PERFORM public.privacy_restore_finalize_membership_postcondition(p_run_id);
 END IF;
 RETURN checkpoint_ref;
END;$$;

CREATE FUNCTION public.privacy_restore_membership_postcondition(p_run_id uuid)
RETURNS TABLE(membership_postcondition_contract text,membership_postcondition_sha256 bytea,membership_count bigint,variation_count bigint)
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT postcondition.contract::text,postcondition.postcondition_sha256,postcondition.membership_count,postcondition.variation_count
 FROM privacy_protected.membership_history_replay_postconditions postcondition
 JOIN privacy_protected.restore_replay_runs run ON run.id=postcondition.run_id AND run.status='SUCCEEDED'
 WHERE postcondition.run_id=p_run_id;
$$;

ALTER TABLE privacy_protected.restore_replay_inventory_attestations
 ADD COLUMN closure_v4_count integer NULL CHECK(closure_v4_count IS NULL OR closure_v4_count>=0),
 ADD COLUMN membership_postcondition_contract varchar(64) NULL,
 ADD COLUMN membership_postcondition_sha256 bytea NULL,
 ADD COLUMN membership_postcondition_verified_count integer NULL CHECK(membership_postcondition_verified_count IS NULL OR membership_postcondition_verified_count>=0),
 ADD COLUMN membership_count bigint NULL CHECK(membership_count IS NULL OR membership_count BETWEEN 0 AND 10240000),
 ADD COLUMN variation_count bigint NULL CHECK(variation_count IS NULL OR variation_count BETWEEN 0 AND 102400000);
DO $$DECLARE constraint_name text;
BEGIN
 SELECT constraint_row.conname INTO constraint_name FROM pg_constraint constraint_row
 WHERE constraint_row.conrelid='privacy_protected.restore_replay_inventory_attestations'::regclass AND constraint_row.contype='c'
  AND pg_get_constraintdef(constraint_row.oid) LIKE '%closure_v3_count = replayed_count%' LIMIT 1;
 IF constraint_name IS NULL THEN RAISE EXCEPTION 'privacy restore attestation compatibility constraint missing'; END IF;
 EXECUTE format('ALTER TABLE privacy_protected.restore_replay_inventory_attestations DROP CONSTRAINT %I',constraint_name);
END$$;
ALTER TABLE privacy_protected.restore_replay_inventory_attestations
 ADD CONSTRAINT restore_replay_inventory_attestations_closure_compatibility CHECK((
  (closure_v4_count IS NULL AND membership_postcondition_contract IS NULL AND membership_postcondition_sha256 IS NULL
   AND membership_postcondition_verified_count IS NULL AND membership_count IS NULL AND variation_count IS NULL
   AND closure_v3_count=replayed_count AND intent_only_count=0 AND legacy_closure_v2_count=0 AND erasure_effective_at_verified_count=replayed_count)
  OR (closure_v4_count=replayed_count AND closure_v3_count=0 AND intent_only_count=0 AND legacy_closure_v2_count=0
   AND erasure_effective_at_verified_count=replayed_count
   AND membership_postcondition_contract='mycfc/membership-history-postcondition/v1'
   AND octet_length(membership_postcondition_sha256)=32 AND membership_postcondition_verified_count=replayed_count
   AND membership_count>=0 AND variation_count>=0)) IS TRUE) NOT VALID;
ALTER TABLE privacy_protected.restore_replay_inventory_attestations
 VALIDATE CONSTRAINT restore_replay_inventory_attestations_closure_compatibility;

CREATE FUNCTION public.privacy_restore_record_inventory_attestation_v4(p_input_source text,p_inventory_sha256 bytea,p_schema_migration_digest bytea,
 p_policy_version text,p_executor_version text,p_plan_schema_version text,p_image_digest text,p_run_ids uuid[],
 p_object_count integer,p_imported_count integer,p_replayed_count integer,p_already_applied_count integer,
 p_absence_verified_count integer,p_synthetic_replayed_count integer,p_closure_v4_count integer,p_intent_only_count integer,
 p_legacy_closure_v2_count integer,p_erasure_effective_at_verified_count integer,p_membership_postcondition_contract text,
 p_membership_postcondition_sha256 bytea,p_membership_postcondition_verified_count integer,p_membership_count bigint,p_variation_count bigint)
RETURNS bytea LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE attestation_ref uuid;evidence bytea;computed_membership_digest bytea;computed_membership_count bigint;computed_variation_count bigint;
 existing privacy_protected.restore_replay_inventory_attestations%ROWTYPE;
BEGIN
 IF p_input_source NOT IN ('LIVE_LEDGER','SYNTHETIC_BOOTSTRAP') OR octet_length(p_inventory_sha256)<>32 OR octet_length(p_schema_migration_digest)<>32
  OR p_policy_version!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$' OR p_executor_version!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'
  OR p_plan_schema_version!~'^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$' OR p_image_digest!~'^sha256:[0-9a-f]{64}$'
  OR p_object_count<1 OR p_replayed_count<1 OR cardinality(p_run_ids)<>p_replayed_count
  OR cardinality(p_run_ids)<>(SELECT count(DISTINCT listed.run_id) FROM unnest(p_run_ids) listed(run_id))
  OR p_replayed_count<>p_imported_count+p_already_applied_count OR p_absence_verified_count<>p_replayed_count
  OR (p_input_source='LIVE_LEDGER' AND p_synthetic_replayed_count<>0) OR (p_input_source='SYNTHETIC_BOOTSTRAP' AND p_synthetic_replayed_count<>p_replayed_count)
  OR p_closure_v4_count<>p_replayed_count OR p_intent_only_count<>0 OR p_legacy_closure_v2_count<>0
  OR p_erasure_effective_at_verified_count<>p_replayed_count OR p_membership_postcondition_contract<>'mycfc/membership-history-postcondition/v1'
  OR octet_length(p_membership_postcondition_sha256) IS DISTINCT FROM 32 OR p_membership_postcondition_verified_count<>p_replayed_count
  OR p_membership_count<0 OR p_variation_count<0
  OR EXISTS(SELECT 1 FROM unnest(p_run_ids) listed(run_id)
   LEFT JOIN privacy_protected.restore_replay_runs run ON run.id=listed.run_id
   LEFT JOIN privacy_protected.restore_ledger_imports imported ON imported.id=run.import_id
   LEFT JOIN privacy_protected.membership_history_replay_postconditions postcondition ON postcondition.run_id=run.id
   WHERE run.status<>'SUCCEEDED' OR imported.closure_version<>'restore-tombstone-closure/v4'
    OR postcondition.contract IS DISTINCT FROM imported.membership_postcondition_contract
    OR NOT public.privacy_membership_history_digest_equal(postcondition.postcondition_sha256,imported.membership_postcondition_sha256)
    OR postcondition.membership_count IS DISTINCT FROM imported.membership_count OR postcondition.variation_count IS DISTINCT FROM imported.variation_count
    OR (p_input_source='LIVE_LEDGER')<>(imported.synthetic_fixture IS NULL))
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_attestation_rejected'; END IF;
 SELECT digest(privacy_membership_history_frame('mycfc/membership-history-postcondition-set/v1')||
  string_agg(privacy_membership_history_frame(postcondition.postcondition_sha256),''::bytea ORDER BY postcondition.postcondition_sha256), 'sha256'),
  sum(postcondition.membership_count),sum(postcondition.variation_count)
 INTO computed_membership_digest,computed_membership_count,computed_variation_count
 FROM privacy_protected.membership_history_replay_postconditions postcondition WHERE postcondition.run_id=ANY(p_run_ids);
 IF NOT public.privacy_membership_history_digest_equal(computed_membership_digest,p_membership_postcondition_sha256)
  OR p_membership_count IS DISTINCT FROM computed_membership_count OR p_variation_count IS DISTINCT FROM computed_variation_count
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_attestation_rejected'; END IF;
 SELECT digest(privacy_membership_history_frame('mycfc/privacy-restore-attestation-evidence/v4')||
   privacy_membership_history_frame(p_inventory_sha256)||privacy_membership_history_frame(computed_membership_digest)||
   privacy_membership_history_frame(COALESCE(string_agg(checkpoint.result_sha256,''::bytea ORDER BY checkpoint.result_sha256),''::bytea)),'sha256') INTO evidence
 FROM privacy_protected.restore_replay_checkpoints checkpoint WHERE checkpoint.run_id=ANY(p_run_ids);
 PERFORM pg_advisory_xact_lock(hashtextextended(p_input_source||':'||encode(p_inventory_sha256,'hex'),0));
 SELECT * INTO existing FROM privacy_protected.restore_replay_inventory_attestations WHERE input_source=p_input_source AND inventory_sha256=p_inventory_sha256;
 IF existing.id IS NOT NULL THEN
  IF existing.closure_v4_count IS DISTINCT FROM p_closure_v4_count OR existing.membership_postcondition_contract IS DISTINCT FROM p_membership_postcondition_contract
   OR NOT public.privacy_membership_history_digest_equal(existing.membership_postcondition_sha256,p_membership_postcondition_sha256)
   OR existing.membership_postcondition_verified_count IS DISTINCT FROM p_membership_postcondition_verified_count OR existing.evidence_sha256<>evidence
  THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_attestation_conflict'; END IF;
  RETURN existing.evidence_sha256;
 END IF;
 INSERT INTO privacy_protected.restore_replay_inventory_attestations(input_source,inventory_sha256,schema_migration_digest,policy_version,executor_version,plan_schema_version,image_digest,
  object_count,imported_count,replayed_count,already_applied_count,absence_verified_count,synthetic_replayed_count,closure_v3_count,intent_only_count,
  legacy_closure_v2_count,erasure_effective_at_verified_count,evidence_sha256,closure_v4_count,membership_postcondition_contract,
  membership_postcondition_sha256,membership_postcondition_verified_count,membership_count,variation_count)
 VALUES(p_input_source,p_inventory_sha256,p_schema_migration_digest,p_policy_version,p_executor_version,p_plan_schema_version,p_image_digest,
  p_object_count,p_imported_count,p_replayed_count,p_already_applied_count,p_absence_verified_count,p_synthetic_replayed_count,0,p_intent_only_count,
  p_legacy_closure_v2_count,p_erasure_effective_at_verified_count,evidence,p_closure_v4_count,p_membership_postcondition_contract,
  p_membership_postcondition_sha256,p_membership_postcondition_verified_count,p_membership_count,p_variation_count) RETURNING id INTO attestation_ref;
 INSERT INTO privacy_protected.restore_replay_inventory_attestation_runs(attestation_id,run_id)
 SELECT attestation_ref,listed.run_id FROM unnest(p_run_ids) listed(run_id);
 RETURN evidence;
END;$$;

DROP FUNCTION public.privacy_restore_observe_inventory(text,bytea,bytea,text,text,text,text);
CREATE FUNCTION public.privacy_restore_observe_inventory(p_input_source text,p_inventory_sha256 bytea,p_schema_migration_digest bytea,
 p_policy_version text,p_executor_version text,p_plan_schema_version text,p_image_digest text)
RETURNS TABLE(replay_count integer,source_already_applied_count integer,synthetic_count integer,verified_run_count integer,
 expected_checkpoint_count integer,succeeded_checkpoint_count integer,provider_absent_count integer,consent_clock_verified_count integer,
 closure_v4_count integer,erasure_effective_at_verified_count integer,membership_postcondition_contract text,
 membership_postcondition_sha256 bytea,membership_postcondition_verified_count integer,membership_count bigint,variation_count bigint,evidence_sha256 bytea)
LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT attestation.replayed_count,
  count(DISTINCT run.id) FILTER(WHERE run.outcome_code='ALREADY_APPLIED_SOURCE')::integer,
  count(DISTINCT run.id) FILTER(WHERE imported.synthetic_fixture='mycfc/privacy-restore-synthetic-fixture/v1')::integer,
  count(DISTINCT run.id) FILTER(WHERE cardinality(imported.operations)=(SELECT count(*) FROM privacy_protected.restore_replay_checkpoints exact_checkpoint WHERE exact_checkpoint.run_id=run.id)
   AND NOT EXISTS(SELECT 1 FROM privacy_protected.restore_replay_checkpoints failed_checkpoint WHERE failed_checkpoint.run_id=run.id AND failed_checkpoint.status<>'SUCCEEDED'))::integer,
  count(checkpoint.*)::integer,count(checkpoint.*) FILTER(WHERE checkpoint.status='SUCCEEDED')::integer,
  count(DISTINCT run.id) FILTER(WHERE 'PROVIDER_LOCAL_FENCE'=ANY(imported.operations)
   AND NOT EXISTS(SELECT 1 FROM privacy_protected.provider_connections connection WHERE connection.subject_user_id=imported.subject_user_id)
   AND NOT EXISTS(SELECT 1 FROM privacy_protected.provider_credential_quarantine quarantine JOIN privacy_protected.provider_targets target ON target.id=quarantine.target_id
    JOIN privacy_protected.provider_capture_sets capture ON capture.execution_id=target.execution_id WHERE capture.subject_user_id=imported.subject_user_id))::integer,
  count(DISTINCT run.id) FILTER(WHERE 'IDENTITY_CLEAR'=ANY(imported.operations) AND EXISTS(SELECT 1 FROM users WHERE users.id=imported.subject_user_id AND users.erased_at=imported.erasure_effective_at)
   AND NOT EXISTS(SELECT 1 FROM consent_forms consent WHERE consent.user_id=imported.subject_user_id AND consent.ceased_at IS NULL)
   AND NOT EXISTS(SELECT 1 FROM consent_forms consent WHERE consent.user_id=imported.subject_user_id AND consent.cessation_reason='ACCOUNT_ERASURE'
    AND (consent.ceased_at<>imported.erasure_effective_at OR consent.evidence_expires_at<>imported.erasure_effective_at+interval '3 years')))::integer,
  count(DISTINCT run.id) FILTER(WHERE imported.closure_version='restore-tombstone-closure/v4')::integer,
  count(DISTINCT run.id) FILTER(WHERE EXISTS(SELECT 1 FROM users WHERE users.id=imported.subject_user_id AND users.erased_at=imported.erasure_effective_at))::integer,
  attestation.membership_postcondition_contract::text,attestation.membership_postcondition_sha256,
  count(DISTINCT run.id) FILTER(WHERE postcondition.contract=imported.membership_postcondition_contract
   AND public.privacy_membership_history_digest_equal(postcondition.postcondition_sha256,imported.membership_postcondition_sha256)
   AND postcondition.membership_count=imported.membership_count AND postcondition.variation_count=imported.variation_count)::integer,
  attestation.membership_count,attestation.variation_count,attestation.evidence_sha256
 FROM privacy_protected.restore_replay_inventory_attestations attestation
 JOIN privacy_protected.restore_replay_inventory_attestation_runs link ON link.attestation_id=attestation.id
 JOIN privacy_protected.restore_replay_runs run ON run.id=link.run_id
 JOIN privacy_protected.restore_ledger_imports imported ON imported.id=run.import_id
 JOIN privacy_protected.restore_replay_checkpoints checkpoint ON checkpoint.run_id=run.id
 JOIN privacy_protected.membership_history_replay_postconditions postcondition ON postcondition.run_id=run.id
 WHERE attestation.input_source=p_input_source AND attestation.inventory_sha256=p_inventory_sha256
  AND attestation.schema_migration_digest=p_schema_migration_digest AND attestation.policy_version=p_policy_version
  AND attestation.executor_version=p_executor_version AND attestation.plan_schema_version=p_plan_schema_version AND attestation.image_digest=p_image_digest
 GROUP BY attestation.id,attestation.replayed_count,attestation.membership_postcondition_contract,attestation.membership_postcondition_sha256,
  attestation.membership_count,attestation.variation_count,attestation.evidence_sha256 HAVING count(DISTINCT run.id)=attestation.replayed_count;
$$;

ALTER TABLE privacy_activation_authenticated_artifacts
 ADD COLUMN restore_membership_postcondition_contract varchar(64) NULL,
 ADD COLUMN restore_membership_postcondition_sha256 bytea NULL,
 ADD COLUMN restore_membership_postcondition_verified_count bigint NULL,
 ADD COLUMN restore_membership_count bigint NULL,
 ADD COLUMN restore_variation_count bigint NULL;
DO $$DECLARE constraint_name text;
BEGIN
 SELECT constraint_row.conname INTO constraint_name FROM pg_constraint constraint_row
 WHERE constraint_row.conrelid='public.privacy_activation_authenticated_artifacts'::regclass AND constraint_row.contype='c'
  AND pg_get_constraintdef(constraint_row.oid) LIKE '%restore_closure_contract%' LIMIT 1;
 IF constraint_name IS NULL THEN RAISE EXCEPTION 'privacy activation artifact compatibility constraint missing'; END IF;
 EXECUTE format('ALTER TABLE public.privacy_activation_authenticated_artifacts DROP CONSTRAINT %I',constraint_name);
END$$;
ALTER TABLE privacy_activation_authenticated_artifacts ADD CONSTRAINT privacy_activation_authenticated_artifacts_v4_check CHECK(
 ((kind='RESTORE' AND signing_key_id IS NULL AND immutable_evidence_ref IS NOT NULL AND schema_migration_digest IS NOT NULL
   AND baseline_includes_through IS NULL AND restore_input_source IN('LIVE_LEDGER','SYNTHETIC_BOOTSTRAP')
   AND restore_input_contract='mycfc/privacy-restore-ledger-input/v2' AND restore_replay_contract='relational-erasure-replay/v1'
   AND restore_closure_contract IN('restore-tombstone-closure/v3','restore-tombstone-closure/v4')
   AND octet_length(restore_candidate_sha256)=32 AND octet_length(restore_inventory_sha256)=32 AND octet_length(restore_observer_sha256)=32
   AND restore_object_count>=restore_replayed_count AND restore_replayed_count>0
   AND ((restore_input_source='LIVE_LEDGER' AND restore_synthetic_count=0) OR (restore_input_source='SYNTHETIC_BOOTSTRAP' AND restore_synthetic_count=restore_replayed_count))
   AND ((restore_closure_contract='restore-tombstone-closure/v3' AND restore_membership_postcondition_contract IS NULL
     AND restore_membership_postcondition_sha256 IS NULL AND restore_membership_postcondition_verified_count IS NULL
     AND restore_membership_count IS NULL AND restore_variation_count IS NULL)
    OR (restore_closure_contract='restore-tombstone-closure/v4'
     AND restore_membership_postcondition_contract='mycfc/membership-history-postcondition/v1'
     AND octet_length(restore_membership_postcondition_sha256)=32
     AND restore_membership_postcondition_verified_count=restore_replayed_count
     AND restore_membership_count>=0 AND restore_variation_count>=0)))
 OR (kind='INFRASTRUCTURE' AND signing_key_id IS NOT NULL AND immutable_evidence_ref IS NOT NULL
   AND production_state_serial>0 AND hetzner_state_serial>0 AND octet_length(production_state_sha256)=32 AND octet_length(hetzner_state_sha256)=32
   AND octet_length(production_plan_sha256)=32 AND octet_length(hetzner_plan_sha256)=32 AND worker_identity_enabled AND s3_version_deletion_enabled
   AND ledger_broker_invoke_enabled AND worker_monitoring_enabled AND restore_infrastructure_enabled AND restore_ledger_write_enabled)
 OR (kind='PROVIDER' AND signing_key_id IS NOT NULL AND immutable_evidence_ref IS NOT NULL AND provider_registry_state='READY'
   AND provider_registration_count>0 AND octet_length(provider_registry_sha256)=32)
 OR (kind='SCHEMA' AND signing_key_id IS NOT NULL AND immutable_evidence_ref IS NOT NULL AND octet_length(schema_migration_digest)=32
   AND baseline_includes_through IN('202609100014_privacy_activation_broker','202609100015_privacy_membership_postcondition'))) IS TRUE) NOT VALID;
ALTER TABLE privacy_activation_authenticated_artifacts VALIDATE CONSTRAINT privacy_activation_authenticated_artifacts_v4_check;
CREATE OR REPLACE FUNCTION privacy_activation_record_authenticated_evidence(
 p_actor uuid,p_kind text,p_evidence_sha256 bytea,p_reference_code text,p_observed_at timestamptz,p_expires_at timestamptz,p_artifact jsonb
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE v_evidence_id uuid;now_at timestamptz:=clock_timestamp();expected_keys text[];
BEGIN
 IF NOT EXISTS(SELECT 1 FROM users account JOIN user_platform_roles assignment ON assignment.user_id=account.id
   JOIN platform_roles role ON role.id=assignment.role_id WHERE account.id=p_actor AND account.is_active AND NOT account.is_dependent AND role.code='ADMIN')
  OR p_kind NOT IN ('RESTORE','INFRASTRUCTURE','PROVIDER','SCHEMA') OR octet_length(p_evidence_sha256)<>32
  OR p_observed_at>now_at OR p_observed_at<=now_at-interval '2160 hours' OR p_expires_at<=now_at OR p_expires_at>p_observed_at+interval '2160 hours'
  OR jsonb_typeof(p_artifact)<>'object' THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy_activation_evidence_rejected'; END IF;
 expected_keys:=CASE p_kind
  WHEN 'RESTORE' THEN ARRAY['policy_version','executor_version','plan_schema_version','image_digest','evidence_ref','evidence_sha256','schema_migration_digest','restore_input_source','restore_input_contract','restore_replay_contract','restore_closure_contract','restore_candidate_sha256','restore_inventory_sha256','restore_object_count','restore_replayed_count','restore_synthetic_count','restore_observer_sha256','restore_membership_postcondition_contract','restore_membership_postcondition_sha256','restore_membership_postcondition_verified_count','restore_membership_count','restore_variation_count']
  WHEN 'INFRASTRUCTURE' THEN ARRAY['policy_version','executor_version','plan_schema_version','image_digest','evidence_ref','evidence_sha256','signing_key_id','production_state_serial','hetzner_state_serial','production_state_sha256','hetzner_state_sha256','production_plan_sha256','hetzner_plan_sha256','worker_identity_enabled','s3_version_deletion_enabled','ledger_broker_invoke_enabled','worker_monitoring_enabled','restore_infrastructure_enabled','restore_ledger_write_enabled']
  WHEN 'PROVIDER' THEN ARRAY['policy_version','executor_version','plan_schema_version','image_digest','evidence_ref','evidence_sha256','signing_key_id','provider_registry_state','provider_registration_count','provider_registry_sha256']
  WHEN 'SCHEMA' THEN ARRAY['policy_version','executor_version','plan_schema_version','image_digest','evidence_ref','evidence_sha256','signing_key_id','schema_migration_digest','baseline_includes_through'] END;
 IF NOT p_artifact ?& expected_keys OR (SELECT count(*) FROM jsonb_object_keys(p_artifact))<>cardinality(expected_keys)
  OR p_artifact->>'policy_version' IS NULL OR p_artifact->>'executor_version'<>'privacy-erasure-executor/v2'
  OR p_artifact->>'plan_schema_version'<>'privacy-erasure-plan/v2' OR p_artifact->>'image_digest'!~'^sha256:[0-9a-f]{64}$' THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy_activation_artifact_rejected'; END IF;
 IF (p_kind='RESTORE' AND p_reference_code<>'mycfc/privacy-restore-drill-attestation/v2')
  OR (p_kind='INFRASTRUCTURE' AND p_reference_code<>'mycfc/privacy-infrastructure-posture/v1')
  OR (p_kind='PROVIDER' AND p_reference_code<>'mycfc/privacy-provider-registry/v1')
  OR (p_kind='SCHEMA' AND p_reference_code<>'mycfc/schema-migration-inventory/v1') THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy_activation_evidence_contract_rejected'; END IF;
 IF (p_kind='RESTORE' AND (p_artifact->>'restore_closure_contract'<>'restore-tombstone-closure/v4'
    OR p_artifact->>'restore_membership_postcondition_contract'<>'mycfc/membership-history-postcondition/v1'
    OR (p_artifact->>'restore_membership_postcondition_verified_count')::bigint<>(p_artifact->>'restore_replayed_count')::bigint))
  OR (p_kind='SCHEMA' AND p_artifact->>'baseline_includes_through'<>'202609100015_privacy_membership_postcondition') THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy_activation_artifact_rejected'; END IF;
 INSERT INTO privacy_activation_evidence(kind,evidence_sha256,reference_code,observed_at,expires_at,recorded_by_ref,recorded_at)
 VALUES(p_kind,p_evidence_sha256,p_reference_code,p_observed_at,p_expires_at,p_actor,now_at)
 ON CONFLICT(kind,evidence_sha256) DO NOTHING RETURNING id INTO v_evidence_id;
 IF v_evidence_id IS NULL THEN
  SELECT evidence.id INTO v_evidence_id FROM privacy_activation_evidence evidence WHERE evidence.kind=p_kind AND evidence.evidence_sha256=p_evidence_sha256
   AND evidence.reference_code=p_reference_code AND evidence.observed_at=p_observed_at AND evidence.expires_at=p_expires_at;
  IF v_evidence_id IS NULL THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_activation_evidence_conflict'; END IF;
 END IF;
 INSERT INTO privacy_activation_authenticated_artifacts(
  evidence_id,kind,policy_version,executor_version,plan_schema_version,image_digest,immutable_evidence_ref,immutable_evidence_sha256,signing_key_id,
  schema_migration_digest,baseline_includes_through,production_state_serial,hetzner_state_serial,production_state_sha256,hetzner_state_sha256,
  production_plan_sha256,hetzner_plan_sha256,worker_identity_enabled,s3_version_deletion_enabled,ledger_broker_invoke_enabled,worker_monitoring_enabled,
  restore_infrastructure_enabled,restore_ledger_write_enabled,provider_registry_state,provider_registration_count,provider_registry_sha256,restore_input_source,
  restore_input_contract,restore_replay_contract,restore_closure_contract,restore_candidate_sha256,restore_inventory_sha256,restore_object_count,restore_replayed_count,
  restore_synthetic_count,restore_observer_sha256,restore_membership_postcondition_contract,restore_membership_postcondition_sha256,restore_membership_postcondition_verified_count,restore_membership_count,restore_variation_count,authenticated_at)
 VALUES(v_evidence_id,p_kind,p_artifact->>'policy_version',p_artifact->>'executor_version',p_artifact->>'plan_schema_version',p_artifact->>'image_digest',
  p_artifact->>'evidence_ref',decode(p_artifact->>'evidence_sha256','base64'),p_artifact->>'signing_key_id',decode(p_artifact->>'schema_migration_digest','base64'),
  p_artifact->>'baseline_includes_through',(p_artifact->>'production_state_serial')::bigint,(p_artifact->>'hetzner_state_serial')::bigint,
  decode(p_artifact->>'production_state_sha256','base64'),decode(p_artifact->>'hetzner_state_sha256','base64'),decode(p_artifact->>'production_plan_sha256','base64'),
  decode(p_artifact->>'hetzner_plan_sha256','base64'),(p_artifact->>'worker_identity_enabled')::boolean,(p_artifact->>'s3_version_deletion_enabled')::boolean,
  (p_artifact->>'ledger_broker_invoke_enabled')::boolean,(p_artifact->>'worker_monitoring_enabled')::boolean,(p_artifact->>'restore_infrastructure_enabled')::boolean,
  (p_artifact->>'restore_ledger_write_enabled')::boolean,p_artifact->>'provider_registry_state',(p_artifact->>'provider_registration_count')::bigint,
  decode(p_artifact->>'provider_registry_sha256','base64'),p_artifact->>'restore_input_source',p_artifact->>'restore_input_contract',p_artifact->>'restore_replay_contract',
  p_artifact->>'restore_closure_contract',decode(p_artifact->>'restore_candidate_sha256','base64'),
  decode(p_artifact->>'restore_inventory_sha256','base64'),(p_artifact->>'restore_object_count')::bigint,(p_artifact->>'restore_replayed_count')::bigint,
  (p_artifact->>'restore_synthetic_count')::bigint,decode(p_artifact->>'restore_observer_sha256','base64'),p_artifact->>'restore_membership_postcondition_contract',decode(p_artifact->>'restore_membership_postcondition_sha256','base64'),(p_artifact->>'restore_membership_postcondition_verified_count')::bigint,(p_artifact->>'restore_membership_count')::bigint,(p_artifact->>'restore_variation_count')::bigint,now_at)
 ON CONFLICT ON CONSTRAINT privacy_activation_authenticated_artifacts_pkey DO NOTHING;
 IF NOT EXISTS(SELECT 1 FROM privacy_activation_authenticated_artifacts authenticated WHERE authenticated.evidence_id=v_evidence_id
   AND authenticated.kind=p_kind AND authenticated.policy_version=p_artifact->>'policy_version' AND authenticated.image_digest=p_artifact->>'image_digest') THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_activation_artifact_conflict'; END IF;
 RETURN v_evidence_id;
EXCEPTION WHEN invalid_text_representation OR numeric_value_out_of_range OR check_violation THEN
 RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy_activation_artifact_rejected';
END;$$;

-- Completion may proceed only from the current authenticated closure and an
-- immutable membership-history postcondition that still recomputes exactly.
CREATE FUNCTION public.privacy_completion_membership_postcondition_ready(p_execution_id uuid)
RETURNS boolean LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
 SELECT public.privacy_membership_history_source_postcondition_ready(p_execution_id);
$$;

-- The completion implementation predates closure v4 and was wrapped by the
-- release guard in 013. Replace only the two exact, asserted version clauses;
-- any unexpected predecessor definition aborts the migration.
DO $$DECLARE definition text;old_clause text;new_clause text;
BEGIN
 SELECT pg_get_functiondef('public.privacy_completion_prepare_inner_013(uuid,uuid)'::regprocedure) INTO definition;
 old_clause:='receipt.ledger_version=''restore-tombstone-closure/v2''';
 new_clause:='receipt.ledger_version=''restore-tombstone-closure/v4'' AND public.privacy_completion_membership_postcondition_ready(execution.id)';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_completion_prepare_predecessor_mismatch'; END IF;
 EXECUTE replace(definition,old_clause,new_clause);

 SELECT pg_get_functiondef('public.privacy_completion_finalize_inner_013(uuid,uuid,bytea,bytea)'::regprocedure) INTO definition;
 old_clause:='receipt.ledger_version<>''restore-tombstone-closure/v2''';
 new_clause:='receipt.ledger_version<>''restore-tombstone-closure/v4'' OR NOT public.privacy_completion_membership_postcondition_ready(execution.id)';
 IF strpos(definition,old_clause)=0 OR strpos(replace(definition,old_clause,''),old_clause)>0 THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_completion_finalize_predecessor_mismatch'; END IF;
 EXECUTE replace(definition,old_clause,new_clause);
END$$;

REVOKE ALL ON TABLE privacy_protected.membership_history_source_captures,privacy_protected.membership_history_source_rows,
 privacy_protected.membership_history_source_postconditions,privacy_protected.membership_history_replay_captures,
 privacy_protected.membership_history_replay_rows,privacy_protected.membership_history_replay_postconditions FROM PUBLIC;
REVOKE ALL ON FUNCTION public.privacy_membership_history_frame(text),public.privacy_membership_history_frame(bytea),
 public.privacy_membership_history_digest_equal(bytea,bytea),public.privacy_membership_history_compute(uuid[],timestamptz,boolean),
 public.privacy_membership_history_compute_source(uuid,timestamptz),public.privacy_membership_history_compute_replay(uuid,timestamptz),
 public.privacy_membership_history_lock_source(uuid),public.privacy_membership_history_source_postcondition_ready(uuid),public.prevent_sealed_membership_history_mutation(),
 public.privacy_completion_membership_postcondition_ready(uuid),
 public.privacy_worker_execute_checkpoint_inner_015(uuid,uuid,uuid,bigint,uuid,text,text),
 public.privacy_worker_execute_checkpoint(uuid,uuid,uuid,bigint,uuid,text,text),
 public.privacy_tombstone_prepare_closure_v4(uuid,uuid),public.privacy_tombstone_confirm_closure_v4(uuid,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz),
 public.privacy_restore_create_synthetic_fixture(uuid),
 public.privacy_restore_import_authenticated_v4_hardened(uuid,text,text,text,text,text,bytea,bytea,text,timestamptz,timestamptz,timestamptz,uuid,uuid,uuid,uuid,bytea,bytea,timestamptz,timestamptz,text,text,text,text,text[],bytea,bytea,text,bytea,bigint,bigint),
 public.privacy_restore_finalize_membership_postcondition(uuid),public.privacy_restore_verify_operation_inner_015(uuid,text,boolean),
 public.privacy_restore_verify_operation(uuid,text,boolean),public.privacy_restore_begin_replay_hardened_inner_015(uuid,uuid),
 public.privacy_restore_begin_replay_hardened(uuid,uuid),public.privacy_restore_execute_checkpoint_inner_015(uuid,uuid,smallint,text,text,bytea),
 public.privacy_restore_execute_checkpoint(uuid,uuid,smallint,text,text,bytea),public.privacy_restore_membership_postcondition(uuid),
 public.privacy_restore_record_inventory_attestation_v4(text,bytea,bytea,text,text,text,text,uuid[],integer,integer,integer,integer,integer,integer,integer,integer,integer,integer,text,bytea,integer,bigint,bigint),
 public.privacy_restore_observe_inventory(text,bytea,bytea,text,text,text,text) FROM PUBLIC;

-- Renaming a previously granted function preserves its ACL. Prove that every
-- newly-created inner/helper capability is owner-only before the migration
-- can commit; bootstrap later grants only the reviewed public wrappers.
DO $$DECLARE capability record;grantee_name text;
BEGIN
 FOR capability IN
  SELECT proc.oid,proc.proowner,acl.grantee FROM pg_proc proc JOIN pg_namespace namespace ON namespace.oid=proc.pronamespace
  CROSS JOIN LATERAL aclexplode(COALESCE(proc.proacl,acldefault('f',proc.proowner))) acl
  WHERE namespace.nspname='public' AND (proc.proname LIKE '%\_inner\_015' ESCAPE '\' OR proc.proname LIKE 'privacy\_membership\_history\_%' ESCAPE '\'
   OR proc.proname IN('privacy_worker_execute_checkpoint','privacy_restore_finalize_membership_postcondition','privacy_restore_membership_postcondition','privacy_restore_import_authenticated_v4_hardened','privacy_restore_record_inventory_attestation_v4','privacy_membership_history_source_postcondition_ready','prevent_sealed_membership_history_mutation','privacy_completion_membership_postcondition_ready','privacy_tombstone_prepare_closure_v2','privacy_tombstone_confirm_closure_v2','privacy_tombstone_prepare_closure_v3','privacy_tombstone_confirm_closure_v3'))
   AND acl.privilege_type='EXECUTE' AND acl.grantee<>proc.proowner
 LOOP
  grantee_name:=CASE WHEN capability.grantee=0 THEN 'PUBLIC' ELSE quote_ident((SELECT rolname FROM pg_roles WHERE oid=capability.grantee)) END;
  IF grantee_name IS NOT NULL THEN EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM %s',capability.oid::regprocedure,grantee_name); END IF;
 END LOOP;
 IF EXISTS(SELECT 1 FROM pg_proc proc JOIN pg_namespace namespace ON namespace.oid=proc.pronamespace
  CROSS JOIN LATERAL aclexplode(COALESCE(proc.proacl,acldefault('f',proc.proowner))) acl
  WHERE namespace.nspname='public' AND (proc.proname LIKE '%\_inner\_015' ESCAPE '\' OR proc.proname LIKE 'privacy\_membership\_history\_%' ESCAPE '\'
   OR proc.proname IN('privacy_worker_execute_checkpoint','privacy_restore_finalize_membership_postcondition','privacy_restore_membership_postcondition','privacy_restore_import_authenticated_v4_hardened','privacy_restore_record_inventory_attestation_v4','privacy_membership_history_source_postcondition_ready','prevent_sealed_membership_history_mutation','privacy_completion_membership_postcondition_ready','privacy_tombstone_prepare_closure_v2','privacy_tombstone_confirm_closure_v2','privacy_tombstone_prepare_closure_v3','privacy_tombstone_confirm_closure_v3'))
   AND acl.privilege_type='EXECUTE' AND acl.grantee<>proc.proowner) THEN
  RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='privacy_inner_capability_revoke_failed'; END IF;
END$$;

-- A schema upgrade invalidates all prior release-specific evidence.
UPDATE privacy_request_activation SET enabled=false,fulfilment_ready=false,approval_id=NULL,updated_at=clock_timestamp() WHERE singleton;
UPDATE privacy_worker_kill_switch SET engaged=true,version=version+1,activation_approval_id=NULL,changed_at=clock_timestamp() WHERE singleton;
INSERT INTO privacy_worker_kill_switch_events(version,engaged,occurred_at)
 SELECT version,true,changed_at FROM privacy_worker_kill_switch WHERE singleton;
CREATE OR REPLACE FUNCTION privacy_activation_authenticated_set_digest(p_policy_version text,p_evidence_ids uuid[])
RETURNS bytea LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE policy privacy_request_policies%ROWTYPE;now_at timestamptz:=clock_timestamp();result bytea;
BEGIN
 SELECT * INTO policy FROM privacy_request_policies WHERE version=p_policy_version;
 IF policy.version IS NULL OR policy.adopted_at IS NULL OR policy.working_retention_days<>90 OR cardinality(p_evidence_ids)<>4
  OR policy.executor_version<>'privacy-erasure-executor/v2' OR policy.plan_schema_version<>'privacy-erasure-plan/v2'
  OR (SELECT count(*) FROM privacy_activation_evidence evidence JOIN privacy_activation_authenticated_artifacts artifact ON artifact.evidence_id=evidence.id
      WHERE evidence.id=ANY(p_evidence_ids) AND evidence.expires_at>now_at AND artifact.policy_version=policy.version
       AND artifact.executor_version=policy.executor_version AND artifact.plan_schema_version=policy.plan_schema_version)<>4
  OR (SELECT count(DISTINCT evidence.kind) FROM privacy_activation_evidence evidence JOIN privacy_activation_authenticated_artifacts artifact ON artifact.evidence_id=evidence.id
      WHERE evidence.id=ANY(p_evidence_ids) AND evidence.expires_at>now_at)<>4
  OR (SELECT count(DISTINCT artifact.image_digest) FROM privacy_activation_authenticated_artifacts artifact WHERE artifact.evidence_id=ANY(p_evidence_ids))<>1
  OR (SELECT restore.schema_migration_digest IS DISTINCT FROM schema_row.schema_migration_digest
      FROM privacy_activation_authenticated_artifacts restore CROSS JOIN privacy_activation_authenticated_artifacts schema_row
      WHERE restore.evidence_id=ANY(p_evidence_ids) AND restore.kind='RESTORE' AND schema_row.evidence_id=ANY(p_evidence_ids) AND schema_row.kind='SCHEMA')

  OR NOT EXISTS(SELECT 1 FROM privacy_activation_authenticated_artifacts restore
   WHERE restore.evidence_id=ANY(p_evidence_ids) AND restore.kind='RESTORE'
    AND restore.restore_closure_contract='restore-tombstone-closure/v4'
    AND restore.restore_membership_postcondition_contract='mycfc/membership-history-postcondition/v1'
    AND octet_length(restore.restore_membership_postcondition_sha256)=32
    AND restore.restore_membership_postcondition_verified_count=restore.restore_replayed_count)
  OR NOT EXISTS(SELECT 1 FROM privacy_activation_authenticated_artifacts schema_row
   WHERE schema_row.evidence_id=ANY(p_evidence_ids) AND schema_row.kind='SCHEMA'
    AND schema_row.baseline_includes_through='202609100015_privacy_membership_postcondition')
 THEN RETURN NULL; END IF;
 SELECT digest(convert_to(string_agg(evidence.kind||':'||encode(evidence.evidence_sha256,'hex')||':'||
   encode(digest(convert_to((to_jsonb(artifact)-'authenticated_at')::text,'UTF8'),'sha256'),'hex')||':'||
   to_char(evidence.observed_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"')||':'||
   to_char(evidence.expires_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),'|' ORDER BY evidence.kind),'UTF8'),'sha256')
 INTO result FROM privacy_activation_evidence evidence JOIN privacy_activation_authenticated_artifacts artifact ON artifact.evidence_id=evidence.id
 WHERE evidence.id=ANY(p_evidence_ids);
 RETURN result;
END;$$;
