-- +goose Up
CREATE TABLE users (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name varchar(120) NOT NULL,
    email citext NULL,
    password_hash text NULL,
    role role NOT NULL,
    squad_category squad_category NOT NULL DEFAULT 'None',
    guardian_id uuid NULL REFERENCES users(id) ON DELETE RESTRICT,
    is_dependent boolean NOT NULL DEFAULT false,
    date_of_birth date NOT NULL,
    is_active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT users_name_valid CHECK (
        name = btrim(name)
        AND char_length(name) BETWEEN 2 AND 120
    ),
    CONSTRAINT users_guardian_not_self CHECK (guardian_id IS NULL OR guardian_id <> id),
    CONSTRAINT users_identity_shape CHECK (
        (
            is_dependent
            AND guardian_id IS NOT NULL
            AND email IS NULL
            AND password_hash IS NULL
            AND role IN ('Competitor', 'Leisure')
        )
        OR
        (
            NOT is_dependent
            AND guardian_id IS NULL
            AND email IS NOT NULL
            AND password_hash IS NOT NULL
        )
    ),
    CONSTRAINT users_role_squad_valid CHECK (
        (role IN ('Admin', 'Guardian') AND squad_category = 'None')
        OR (role = 'Leisure' AND squad_category = 'Lazer')
        OR (role = 'Competitor' AND squad_category IN ('Iniciante', 'Polo_Senior', 'Master_A'))
    )
);

CREATE UNIQUE INDEX users_email_uidx ON users (email) WHERE email IS NOT NULL;
CREATE INDEX users_guardian_id_idx ON users (guardian_id) WHERE guardian_id IS NOT NULL;
CREATE INDEX users_role_active_idx ON users (role, is_active);

CREATE TABLE equipment (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    asset_tag varchar(40) NOT NULL UNIQUE,
    name varchar(120) NOT NULL,
    type equipment_type NOT NULL,
    status equipment_status NOT NULL DEFAULT 'Operational',
    notes text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT equipment_asset_tag_valid CHECK (
        asset_tag = btrim(asset_tag)
        AND char_length(asset_tag) BETWEEN 2 AND 40
    ),
    CONSTRAINT equipment_name_valid CHECK (
        name = btrim(name)
        AND char_length(name) BETWEEN 2 AND 120
    ),
    CONSTRAINT equipment_notes_valid CHECK (char_length(notes) <= 4000)
);

CREATE INDEX equipment_status_type_idx ON equipment (status, type);

CREATE TABLE repair_requests (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    idempotency_key uuid NOT NULL UNIQUE,
    equipment_id uuid NOT NULL REFERENCES equipment(id) ON DELETE RESTRICT,
    reported_by_id uuid NULL REFERENCES users(id) ON DELETE SET NULL,
    issue_description varchar(2000) NOT NULL,
    status repair_status NOT NULL DEFAULT 'Pendente',
    image_object_key varchar(512) NULL,
    image_content_type varchar(100) NULL,
    image_size_bytes bigint NULL,
    date_reported timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    resolved_at timestamptz NULL,
    CONSTRAINT repair_description_valid CHECK (
        issue_description = btrim(issue_description)
        AND char_length(issue_description) BETWEEN 10 AND 2000
    ),
    CONSTRAINT repair_image_metadata_complete CHECK (
        (image_object_key IS NULL AND image_content_type IS NULL AND image_size_bytes IS NULL)
        OR
        (image_object_key IS NOT NULL AND image_content_type IS NOT NULL AND image_size_bytes IS NOT NULL)
    ),
    CONSTRAINT repair_image_size_valid CHECK (
        image_size_bytes IS NULL OR image_size_bytes BETWEEN 1 AND 10485760
    ),
    CONSTRAINT repair_resolution_valid CHECK (
        (status = 'Resolvido' AND resolved_at IS NOT NULL)
        OR (status <> 'Resolvido' AND resolved_at IS NULL)
    )
);

CREATE INDEX repair_status_date_idx ON repair_requests (status, date_reported DESC);
CREATE INDEX repair_equipment_id_idx ON repair_requests (equipment_id);
CREATE INDEX repair_reported_by_id_idx ON repair_requests (reported_by_id);

CREATE TABLE consent_forms (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    granted_by_user_id uuid NULL REFERENCES users(id) ON DELETE SET NULL,
    consent_type consent_type NOT NULL,
    document_version varchar(40) NOT NULL,
    document_sha256 char(64) NOT NULL,
    is_accepted boolean NOT NULL,
    date_signed timestamptz NOT NULL DEFAULT now(),
    ip_address inet NULL,
    user_agent varchar(512) NOT NULL DEFAULT '',
    CONSTRAINT consent_version_valid CHECK (
        document_version = btrim(document_version)
        AND char_length(document_version) BETWEEN 1 AND 40
    ),
    CONSTRAINT consent_sha256_valid CHECK (document_sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT consent_accepted_true CHECK (is_accepted),
    CONSTRAINT consent_user_agent_valid CHECK (char_length(user_agent) <= 512),
    CONSTRAINT consent_version_unique UNIQUE (user_id, consent_type, document_version)
);

CREATE INDEX consent_user_type_date_idx ON consent_forms (user_id, consent_type, date_signed DESC);

CREATE TABLE whatsapp_groups (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name varchar(120) NOT NULL,
    discipline varchar(80) NOT NULL,
    target_role role NOT NULL,
    squad_category squad_category NULL,
    url text NOT NULL,
    is_active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT whatsapp_name_valid CHECK (
        name = btrim(name)
        AND char_length(name) BETWEEN 2 AND 120
    ),
    CONSTRAINT whatsapp_discipline_valid CHECK (
        discipline = btrim(discipline)
        AND char_length(discipline) BETWEEN 2 AND 80
    ),
    CONSTRAINT whatsapp_url_valid CHECK (url LIKE 'https://chat.whatsapp.com/%'),
    CONSTRAINT whatsapp_role_squad_valid CHECK (
        (target_role = 'Competitor' AND squad_category IN ('Iniciante', 'Polo_Senior', 'Master_A'))
        OR (target_role = 'Leisure' AND (squad_category = 'Lazer' OR squad_category IS NULL))
        OR (target_role IN ('Admin', 'Guardian') AND squad_category IS NULL)
    ),
    CONSTRAINT whatsapp_group_unique UNIQUE NULLS NOT DISTINCT (name, target_role, squad_category)
);

CREATE INDEX whatsapp_role_active_idx ON whatsapp_groups (target_role, is_active);

-- +goose Down
DROP TABLE IF EXISTS whatsapp_groups;
DROP TABLE IF EXISTS consent_forms;
DROP TABLE IF EXISTS repair_requests;
DROP TABLE IF EXISTS equipment;
DROP TABLE IF EXISTS users;
