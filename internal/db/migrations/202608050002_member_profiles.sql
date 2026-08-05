DO $$ BEGIN
  CREATE TYPE medical_declaration AS ENUM ('UNKNOWN', 'NONE_KNOWN', 'PROVIDED');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

CREATE TABLE IF NOT EXISTS member_profiles (
 user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 phone varchar(32) NOT NULL DEFAULT '', address_line1 varchar(200) NOT NULL DEFAULT '', address_line2 varchar(200) NOT NULL DEFAULT '', postcode varchar(20) NOT NULL DEFAULT '', locality varchar(120) NOT NULL DEFAULT '', country_code varchar(2) NOT NULL DEFAULT '', nationality_code varchar(2) NOT NULL DEFAULT '',
 club_member_number varchar(60) NULL, federation_licence_number varchar(60) NULL,
 emergency_contact_name varchar(120) NOT NULL DEFAULT '', emergency_contact_relationship varchar(80) NOT NULL DEFAULT '', emergency_contact_phone varchar(32) NOT NULL DEFAULT '', emergency_contact_alternate_phone varchar(32) NOT NULL DEFAULT '',
 medical_declaration medical_declaration NOT NULL DEFAULT 'UNKNOWN', allergies varchar(2000) NOT NULL DEFAULT '', medical_conditions varchar(2000) NOT NULL DEFAULT '', medication varchar(2000) NOT NULL DEFAULT '', activity_restrictions varchar(2000) NOT NULL DEFAULT '', medical_notes varchar(2000) NOT NULL DEFAULT '',
 photo_object_key varchar(512) NULL, photo_content_type varchar(100) NULL, photo_size_bytes bigint NULL, photo_consent_form_id uuid NULL REFERENCES consent_forms(id) ON DELETE RESTRICT,
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
CREATE UNIQUE INDEX IF NOT EXISTS member_profiles_club_number_uidx ON member_profiles (club_member_number) WHERE club_member_number IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS member_profiles_federation_number_uidx ON member_profiles (federation_licence_number) WHERE federation_licence_number IS NOT NULL;

CREATE TABLE IF NOT EXISTS member_profile_audit_events (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), actor_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT, subject_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 action varchar(40) NOT NULL CHECK (action IN ('SENSITIVE_VIEW', 'PROFILE_UPDATED', 'IDENTITY_UPDATED', 'PHOTO_UPLOADED', 'PHOTO_REPLACED', 'PHOTO_REMOVED')),
 changed_fields text[] NOT NULL DEFAULT '{}', occurred_at timestamptz NOT NULL DEFAULT now(),
 CONSTRAINT member_profile_audit_changed_fields_valid CHECK (array_position(changed_fields, NULL) IS NULL)
);
CREATE INDEX IF NOT EXISTS member_profile_audit_subject_occurred_idx ON member_profile_audit_events (subject_user_id, occurred_at DESC, id DESC);

CREATE OR REPLACE FUNCTION prevent_member_profile_audit_mutation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'member profile audit events are append-only'; END; $$;
DROP TRIGGER IF EXISTS member_profile_audit_events_immutable_trigger ON member_profile_audit_events;
CREATE TRIGGER member_profile_audit_events_immutable_trigger BEFORE UPDATE OR DELETE ON member_profile_audit_events FOR EACH ROW EXECUTE FUNCTION prevent_member_profile_audit_mutation();
