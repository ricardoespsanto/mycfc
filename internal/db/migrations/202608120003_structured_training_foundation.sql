DO $$ BEGIN
 CREATE TYPE training_entry_kind AS ENUM ('TRAINING', 'REST', 'COMPETITION', 'LOGISTICS');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
DO $$ BEGIN
 CREATE TYPE training_segment_modality AS ENUM ('WATER', 'GYM', 'RUN', 'BIKE', 'ERGOMETER', 'FLEXIBILITY', 'SPORTS_GAMES', 'OTHER');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
DO $$ BEGIN
 CREATE TYPE training_block_purpose AS ENUM ('WARM_UP', 'MAIN', 'COOL_DOWN', 'TECHNIQUE', 'CUSTOM');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

CREATE TABLE IF NOT EXISTS training_groups (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 name varchar(120) NOT NULL,
 programme_id uuid NULL REFERENCES programmes(id) ON DELETE RESTRICT,
 team_id uuid NULL REFERENCES teams(id) ON DELETE RESTRICT,
 created_by_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 CONSTRAINT training_groups_name_valid CHECK (name = btrim(name) AND char_length(name) BETWEEN 2 AND 120),
 CONSTRAINT training_groups_scope_valid CHECK (num_nonnulls(programme_id, team_id) = 1)
);
CREATE INDEX IF NOT EXISTS training_groups_programme_idx ON training_groups (programme_id) WHERE programme_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS training_groups_team_idx ON training_groups (team_id) WHERE team_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS training_group_members (
 group_id uuid NOT NULL REFERENCES training_groups(id) ON DELETE CASCADE,
 membership_id uuid NOT NULL REFERENCES user_memberships(id) ON DELETE RESTRICT,
 added_by_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 added_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (group_id, membership_id)
);
CREATE INDEX IF NOT EXISTS training_group_members_membership_idx ON training_group_members (membership_id, group_id);

ALTER TABLE training_plans ADD COLUMN IF NOT EXISTS training_group_id uuid NULL REFERENCES training_groups(id) ON DELETE RESTRICT;
ALTER TABLE training_plans ADD COLUMN IF NOT EXISTS season_id uuid NULL REFERENCES seasons(id) ON DELETE RESTRICT;
ALTER TABLE training_plans ADD COLUMN IF NOT EXISTS week_start date NULL;
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'training_plans_week_valid' AND conrelid = 'training_plans'::regclass) THEN
  ALTER TABLE training_plans ADD CONSTRAINT training_plans_week_valid CHECK ((training_group_id IS NULL AND week_start IS NULL) OR (training_group_id IS NOT NULL AND week_start IS NOT NULL AND extract(isodow FROM week_start) = 1));
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'training_plans_group_week_unique' AND conrelid = 'training_plans'::regclass) THEN
  ALTER TABLE training_plans ADD CONSTRAINT training_plans_group_week_unique UNIQUE (training_group_id, week_start);
 END IF;
END $$;
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'training_plans_structured_season_valid' AND conrelid = 'training_plans'::regclass) THEN
  ALTER TABLE training_plans ADD CONSTRAINT training_plans_structured_season_valid CHECK ((training_group_id IS NULL AND season_id IS NULL) OR (training_group_id IS NOT NULL AND season_id IS NOT NULL));
 END IF;
END $$;
CREATE INDEX IF NOT EXISTS training_plans_group_week_idx ON training_plans (training_group_id, week_start DESC) WHERE training_group_id IS NOT NULL;

ALTER TABLE training_sessions ADD COLUMN IF NOT EXISTS entry_kind training_entry_kind NOT NULL DEFAULT 'TRAINING';

CREATE TABLE IF NOT EXISTS training_session_segments (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 session_id uuid NOT NULL REFERENCES training_sessions(id) ON DELETE CASCADE,
 position integer NOT NULL,
 modality training_segment_modality NOT NULL,
 title varchar(120) NOT NULL DEFAULT '',
 location varchar(180) NOT NULL DEFAULT '',
 planned_duration_minutes integer NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 CONSTRAINT training_session_segments_position_valid CHECK (position > 0),
 CONSTRAINT training_session_segments_title_valid CHECK (title = btrim(title) AND char_length(title) <= 120),
 CONSTRAINT training_session_segments_location_valid CHECK (location = btrim(location) AND char_length(location) <= 180),
 CONSTRAINT training_session_segments_duration_valid CHECK (planned_duration_minutes IS NULL OR planned_duration_minutes BETWEEN 1 AND 1440),
 CONSTRAINT training_session_segments_position_unique UNIQUE (session_id, position)
);
CREATE INDEX IF NOT EXISTS training_session_segments_session_idx ON training_session_segments (session_id, position);

CREATE TABLE IF NOT EXISTS training_segment_blocks (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 segment_id uuid NOT NULL REFERENCES training_session_segments(id) ON DELETE CASCADE,
 position integer NOT NULL,
 purpose training_block_purpose NOT NULL,
 title varchar(120) NOT NULL DEFAULT '',
 instructions varchar(4000) NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 CONSTRAINT training_segment_blocks_position_valid CHECK (position > 0),
 CONSTRAINT training_segment_blocks_title_valid CHECK (title = btrim(title) AND char_length(title) <= 120),
 CONSTRAINT training_segment_blocks_instructions_valid CHECK (instructions = btrim(instructions) AND char_length(instructions) BETWEEN 2 AND 4000),
 CONSTRAINT training_segment_blocks_position_unique UNIQUE (segment_id, position)
);
CREATE INDEX IF NOT EXISTS training_segment_blocks_segment_idx ON training_segment_blocks (segment_id, position);

CREATE OR REPLACE FUNCTION move_training_session_segment(p_segment_id uuid, p_direction integer) RETURNS boolean LANGUAGE plpgsql AS $$
DECLARE current_session uuid; current_position integer; target_id uuid; target_position integer; temporary_position integer;
BEGIN
 IF p_direction NOT IN (-1, 1) THEN RETURN false; END IF;
 SELECT session_id, position INTO current_session, current_position FROM training_session_segments WHERE id = p_segment_id FOR UPDATE;
 IF current_session IS NULL THEN RETURN false; END IF;
 SELECT id, position INTO target_id, target_position FROM training_session_segments WHERE session_id = current_session AND position = current_position + p_direction FOR UPDATE;
 IF target_id IS NULL THEN RETURN false; END IF;
 SELECT COALESCE(max(position), 0) + 1 INTO temporary_position FROM training_session_segments WHERE session_id = current_session;
 UPDATE training_session_segments SET position = temporary_position, updated_at = clock_timestamp() WHERE id = p_segment_id;
 UPDATE training_session_segments SET position = current_position, updated_at = clock_timestamp() WHERE id = target_id;
 UPDATE training_session_segments SET position = target_position, updated_at = clock_timestamp() WHERE id = p_segment_id;
 RETURN true;
END;
$$;

CREATE OR REPLACE FUNCTION move_training_segment_block(p_block_id uuid, p_direction integer) RETURNS boolean LANGUAGE plpgsql AS $$
DECLARE current_segment uuid; current_position integer; target_id uuid; target_position integer; temporary_position integer;
BEGIN
 IF p_direction NOT IN (-1, 1) THEN RETURN false; END IF;
 SELECT segment_id, position INTO current_segment, current_position FROM training_segment_blocks WHERE id = p_block_id FOR UPDATE;
 IF current_segment IS NULL THEN RETURN false; END IF;
 SELECT id, position INTO target_id, target_position FROM training_segment_blocks WHERE segment_id = current_segment AND position = current_position + p_direction FOR UPDATE;
 IF target_id IS NULL THEN RETURN false; END IF;
 SELECT COALESCE(max(position), 0) + 1 INTO temporary_position FROM training_segment_blocks WHERE segment_id = current_segment;
 UPDATE training_segment_blocks SET position = temporary_position, updated_at = clock_timestamp() WHERE id = p_block_id;
 UPDATE training_segment_blocks SET position = current_position, updated_at = clock_timestamp() WHERE id = target_id;
 UPDATE training_segment_blocks SET position = target_position, updated_at = clock_timestamp() WHERE id = p_block_id;
 RETURN true;
END;
$$;

INSERT INTO feature_flags (feature_key, mode)
VALUES ('structured_training_planning', 'ADMIN_ONLY')
ON CONFLICT (feature_key) DO NOTHING;
