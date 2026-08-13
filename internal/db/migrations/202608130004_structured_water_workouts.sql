DO $$ BEGIN CREATE TYPE water_work_method AS ENUM ('CONTINUOUS', 'INTERVALS', 'FARTLEK', 'TECHNIQUE', 'STARTS', 'RACE_SIMULATION', 'TACTICAL_DRILL', 'CUSTOM'); EXCEPTION WHEN duplicate_object THEN NULL; END $$;
DO $$ BEGIN CREATE TYPE water_step_kind AS ENUM ('EFFORT', 'REPEAT_GROUP'); EXCEPTION WHEN duplicate_object THEN NULL; END $$;
DO $$ BEGIN CREATE TYPE training_measure_certainty AS ENUM ('EXACT', 'ESTIMATED'); EXCEPTION WHEN duplicate_object THEN NULL; END $$;
DO $$ BEGIN CREATE TYPE paddling_craft AS ENUM ('KAYAK', 'CANOE'); EXCEPTION WHEN duplicate_object THEN NULL; END $$;

CREATE TABLE IF NOT EXISTS water_intensity_profiles (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), name varchar(120) NOT NULL, craft paddling_craft NOT NULL,
 revision integer NOT NULL, supersedes_id uuid NULL REFERENCES water_intensity_profiles(id) ON DELETE RESTRICT,
 notes varchar(1000) NOT NULL DEFAULT '', is_active boolean NOT NULL DEFAULT true,
 created_by_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT, created_at timestamptz NOT NULL DEFAULT now(),
 CONSTRAINT water_intensity_profiles_name_valid CHECK (name = btrim(name) AND char_length(name) BETWEEN 2 AND 120),
 CONSTRAINT water_intensity_profiles_revision_valid CHECK (revision > 0),
 CONSTRAINT water_intensity_profiles_notes_valid CHECK (notes = btrim(notes) AND char_length(notes) <= 1000),
 CONSTRAINT water_intensity_profiles_revision_unique UNIQUE (name, craft, revision)
);
CREATE UNIQUE INDEX IF NOT EXISTS water_intensity_profiles_active_unique ON water_intensity_profiles (name, craft) WHERE is_active;

CREATE TABLE IF NOT EXISTS water_intensity_zones (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), profile_id uuid NOT NULL REFERENCES water_intensity_profiles(id) ON DELETE CASCADE,
 position integer NOT NULL, code varchar(20) NOT NULL, label varchar(120) NOT NULL,
 cadence_min integer NULL, cadence_max integer NULL, meaning varchar(500) NOT NULL,
 CONSTRAINT water_intensity_zones_position_valid CHECK (position > 0),
 CONSTRAINT water_intensity_zones_code_valid CHECK (code = btrim(code) AND char_length(code) BETWEEN 1 AND 20),
 CONSTRAINT water_intensity_zones_label_valid CHECK (label = btrim(label) AND char_length(label) BETWEEN 2 AND 120),
 CONSTRAINT water_intensity_zones_cadence_valid CHECK ((cadence_min IS NULL OR cadence_min BETWEEN 0 AND 300) AND (cadence_max IS NULL OR cadence_max BETWEEN 0 AND 300) AND (cadence_min IS NULL OR cadence_max IS NULL OR cadence_min <= cadence_max)),
 CONSTRAINT water_intensity_zones_meaning_valid CHECK (meaning = btrim(meaning) AND char_length(meaning) BETWEEN 2 AND 500),
 CONSTRAINT water_intensity_zones_position_unique UNIQUE (profile_id, position),
 CONSTRAINT water_intensity_zones_code_unique UNIQUE (profile_id, code)
);
CREATE INDEX IF NOT EXISTS water_intensity_zones_profile_idx ON water_intensity_zones (profile_id, position);

CREATE TABLE IF NOT EXISTS water_block_prescriptions (
 block_id uuid PRIMARY KEY REFERENCES training_segment_blocks(id) ON DELETE CASCADE,
 method water_work_method NOT NULL, intensity_profile_id uuid NULL REFERENCES water_intensity_profiles(id) ON DELETE RESTRICT,
 target_distance_metres integer NULL, target_distance_certainty training_measure_certainty NULL,
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 CONSTRAINT water_block_target_distance_valid CHECK (target_distance_metres IS NULL OR target_distance_metres BETWEEN 1 AND 200000),
 CONSTRAINT water_block_target_certainty_valid CHECK ((target_distance_metres IS NULL) = (target_distance_certainty IS NULL))
);

CREATE TABLE IF NOT EXISTS water_work_steps (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), block_id uuid NOT NULL REFERENCES water_block_prescriptions(block_id) ON DELETE CASCADE,
 parent_step_id uuid NULL REFERENCES water_work_steps(id) ON DELETE CASCADE, position integer NOT NULL,
 kind water_step_kind NOT NULL, name varchar(180) NOT NULL, repeats integer NULL,
 duration_seconds integer NULL, duration_certainty training_measure_certainty NULL,
 distance_metres integer NULL, distance_certainty training_measure_certainty NULL,
 recovery_seconds integer NULL, intensity_code varchar(20) NULL, cadence_spm integer NULL,
 drill_focus varchar(180) NULL, drill_format varchar(180) NULL, role_notes varchar(500) NULL,
 instructions varchar(1000) NOT NULL DEFAULT '', created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 CONSTRAINT water_work_steps_position_valid CHECK (position > 0),
 CONSTRAINT water_work_steps_name_valid CHECK (name = btrim(name) AND char_length(name) BETWEEN 2 AND 180),
 CONSTRAINT water_work_steps_shape_valid CHECK ((kind = 'REPEAT_GROUP' AND repeats BETWEEN 1 AND 100 AND duration_seconds IS NULL AND distance_metres IS NULL) OR (kind = 'EFFORT' AND repeats IS NULL AND (duration_seconds IS NOT NULL OR distance_metres IS NOT NULL OR char_length(instructions) >= 2))),
 CONSTRAINT water_work_steps_duration_valid CHECK (duration_seconds IS NULL OR duration_seconds BETWEEN 1 AND 86400),
 CONSTRAINT water_work_steps_duration_certainty_valid CHECK ((duration_seconds IS NULL) = (duration_certainty IS NULL)),
 CONSTRAINT water_work_steps_distance_valid CHECK (distance_metres IS NULL OR distance_metres BETWEEN 1 AND 200000),
 CONSTRAINT water_work_steps_distance_certainty_valid CHECK ((distance_metres IS NULL) = (distance_certainty IS NULL)),
 CONSTRAINT water_work_steps_recovery_valid CHECK (recovery_seconds IS NULL OR recovery_seconds BETWEEN 1 AND 86400),
 CONSTRAINT water_work_steps_intensity_valid CHECK (intensity_code IS NULL OR (intensity_code = btrim(intensity_code) AND char_length(intensity_code) BETWEEN 1 AND 20)),
 CONSTRAINT water_work_steps_cadence_valid CHECK (cadence_spm IS NULL OR cadence_spm BETWEEN 1 AND 300),
 CONSTRAINT water_work_steps_drill_focus_valid CHECK (drill_focus IS NULL OR (drill_focus = btrim(drill_focus) AND char_length(drill_focus) BETWEEN 1 AND 180)),
 CONSTRAINT water_work_steps_drill_format_valid CHECK (drill_format IS NULL OR (drill_format = btrim(drill_format) AND char_length(drill_format) BETWEEN 1 AND 180)),
 CONSTRAINT water_work_steps_role_notes_valid CHECK (role_notes IS NULL OR (role_notes = btrim(role_notes) AND char_length(role_notes) BETWEEN 1 AND 500)),
 CONSTRAINT water_work_steps_instructions_valid CHECK (instructions = btrim(instructions) AND char_length(instructions) <= 1000),
 CONSTRAINT water_work_steps_position_unique UNIQUE NULLS NOT DISTINCT (block_id, parent_step_id, position)
);
CREATE INDEX IF NOT EXISTS water_work_steps_block_idx ON water_work_steps (block_id, parent_step_id, position);

CREATE OR REPLACE FUNCTION validate_water_step_parent() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE parent_block uuid; parent_kind water_step_kind;
BEGIN
 IF NEW.parent_step_id IS NULL THEN RETURN NEW; END IF;
 SELECT block_id, kind INTO parent_block, parent_kind FROM water_work_steps WHERE id = NEW.parent_step_id;
 IF parent_block IS DISTINCT FROM NEW.block_id OR parent_kind IS DISTINCT FROM 'REPEAT_GROUP' THEN
  RAISE EXCEPTION 'water step parent must be a repeat group in the same block' USING ERRCODE = '23514';
 END IF;
 RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS water_work_steps_parent_valid ON water_work_steps;
CREATE TRIGGER water_work_steps_parent_valid BEFORE INSERT OR UPDATE OF block_id, parent_step_id ON water_work_steps FOR EACH ROW EXECUTE FUNCTION validate_water_step_parent();
