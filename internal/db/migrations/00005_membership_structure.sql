-- +goose Up
CREATE TABLE seasons (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code varchar(20) NOT NULL UNIQUE,
    name varchar(120) NOT NULL,
    starts_on date NOT NULL,
    ends_on date NOT NULL,
    is_current boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT seasons_code_valid CHECK (code = btrim(code) AND char_length(code) BETWEEN 1 AND 20),
    CONSTRAINT seasons_name_valid CHECK (name = btrim(name) AND char_length(name) BETWEEN 2 AND 120),
    CONSTRAINT seasons_dates_valid CHECK (starts_on <= ends_on)
);

CREATE UNIQUE INDEX seasons_current_uidx ON seasons (is_current) WHERE is_current;

CREATE TABLE programmes (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code varchar(40) NOT NULL UNIQUE,
    name_pt varchar(120) NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT programmes_code_valid CHECK (code = btrim(code) AND code ~ '^[A-Za-z][A-Za-z0-9_]*$'),
    CONSTRAINT programmes_name_valid CHECK (name_pt = btrim(name_pt) AND char_length(name_pt) BETWEEN 2 AND 120)
);

INSERT INTO programmes (code, name_pt) VALUES
    ('Leisure', 'Lazer'),
    ('Initiation', 'Iniciação'),
    ('Competition', 'Competição'),
    ('Kayak_Polo', 'Kayak Polo');

-- Groups reference programmes, so create them after the membership catalogue.
ALTER TABLE whatsapp_groups
    ADD CONSTRAINT whatsapp_groups_programme_fk
    FOREIGN KEY (programme_id) REFERENCES programmes(id) ON DELETE RESTRICT;

CREATE TABLE modalities (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code varchar(20) NOT NULL UNIQUE,
    name_pt varchar(120) NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT modalities_code_valid CHECK (code = btrim(code) AND code ~ '^[A-Z][A-Z0-9]*$'),
    CONSTRAINT modalities_name_valid CHECK (name_pt = btrim(name_pt) AND char_length(name_pt) BETWEEN 2 AND 120)
);

INSERT INTO modalities (code, name_pt) VALUES
    ('K1', 'Caiaque individual'),
    ('K2', 'Caiaque duplo'),
    ('K4', 'Caiaque quádruplo'),
    ('C1', 'Canoa individual'),
    ('C2', 'Canoa dupla'),
    ('C4', 'Canoa quádrupla'),
    ('SUP', 'Stand up paddle');

CREATE TABLE competition_categories (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    season_id uuid NOT NULL REFERENCES seasons(id) ON DELETE RESTRICT,
    programme_id uuid NOT NULL REFERENCES programmes(id) ON DELETE RESTRICT,
    code varchar(40) NOT NULL,
    name_pt varchar(120) NOT NULL,
    birth_date_from date NULL,
    birth_date_to date NULL,
    approved_by_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    approved_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT competition_categories_code_valid CHECK (code = btrim(code) AND code ~ '^[A-Za-z][A-Za-z0-9_]*$'),
    CONSTRAINT competition_categories_name_valid CHECK (name_pt = btrim(name_pt) AND char_length(name_pt) BETWEEN 2 AND 120),
    CONSTRAINT competition_categories_birth_range_valid CHECK (birth_date_from IS NULL OR birth_date_to IS NULL OR birth_date_from <= birth_date_to),
    CONSTRAINT competition_categories_season_programme_code_unique UNIQUE (season_id, programme_id, code),
    CONSTRAINT competition_categories_id_season_programme_unique UNIQUE (id, season_id, programme_id)
);

CREATE TABLE teams (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    season_id uuid NOT NULL REFERENCES seasons(id) ON DELETE RESTRICT,
    programme_id uuid NOT NULL REFERENCES programmes(id) ON DELETE RESTRICT,
    code varchar(40) NOT NULL,
    name varchar(120) NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT teams_code_valid CHECK (code = btrim(code) AND code ~ '^[A-Za-z][A-Za-z0-9_]*$'),
    CONSTRAINT teams_name_valid CHECK (name = btrim(name) AND char_length(name) BETWEEN 2 AND 120),
    CONSTRAINT teams_season_programme_code_unique UNIQUE (season_id, programme_id, code),
    CONSTRAINT teams_id_season_programme_unique UNIQUE (id, season_id, programme_id)
);

CREATE TABLE user_memberships (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    season_id uuid NOT NULL REFERENCES seasons(id) ON DELETE RESTRICT,
    programme_id uuid NOT NULL REFERENCES programmes(id) ON DELETE RESTRICT,
    team_id uuid NULL,
    competition_category_id uuid NULL,
    starts_on date NOT NULL,
    ends_on date NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT user_memberships_dates_valid CHECK (ends_on IS NULL OR starts_on <= ends_on),
    CONSTRAINT user_memberships_user_season_programme_unique UNIQUE (user_id, season_id, programme_id),
    CONSTRAINT user_memberships_team_same_season_programme_fk FOREIGN KEY (team_id, season_id, programme_id) REFERENCES teams(id, season_id, programme_id) ON DELETE RESTRICT,
    CONSTRAINT user_memberships_category_same_season_programme_fk FOREIGN KEY (competition_category_id, season_id, programme_id) REFERENCES competition_categories(id, season_id, programme_id) ON DELETE RESTRICT
);

CREATE INDEX user_memberships_active_user_idx ON user_memberships (user_id, starts_on, ends_on);
CREATE INDEX user_memberships_team_idx ON user_memberships (team_id) WHERE team_id IS NOT NULL;

CREATE TABLE membership_modalities (
    membership_id uuid NOT NULL REFERENCES user_memberships(id) ON DELETE CASCADE,
    modality_id uuid NOT NULL REFERENCES modalities(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (membership_id, modality_id)
);

-- +goose Down
DROP TABLE IF EXISTS membership_modalities;
DROP TABLE IF EXISTS user_memberships;
DROP TABLE IF EXISTS teams;
DROP TABLE IF EXISTS competition_categories;
DROP TABLE IF EXISTS modalities;
ALTER TABLE whatsapp_groups DROP CONSTRAINT IF EXISTS whatsapp_groups_programme_fk;
DROP TABLE IF EXISTS programmes;
DROP TABLE IF EXISTS seasons;
