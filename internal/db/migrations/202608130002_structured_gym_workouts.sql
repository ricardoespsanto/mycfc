DO $$ BEGIN CREATE TYPE gym_block_structure AS ENUM ('STRAIGHT_SETS', 'CIRCUIT', 'SUPERSET'); EXCEPTION WHEN duplicate_object THEN NULL; END $$;
DO $$ BEGIN CREATE TYPE training_objective AS ENUM ('MOBILITY', 'ACTIVATION', 'MAX_STRENGTH_HYPERTROPHY', 'MAX_STRENGTH_NEURAL', 'EXPLOSIVE_STRENGTH', 'STRENGTH_ENDURANCE', 'TECHNIQUE', 'CORE', 'CUSTOM'); EXCEPTION WHEN duplicate_object THEN NULL; END $$;
DO $$ BEGIN CREATE TYPE gym_resistance_kind AS ENUM ('KILOGRAMS', 'PERCENT_1RM', 'BODY_WEIGHT', 'BAND', 'RPE', 'RIR', 'COACH_INSTRUCTION'); EXCEPTION WHEN duplicate_object THEN NULL; END $$;
DO $$ BEGIN CREATE TYPE gym_execution_intent AS ENUM ('CONTROLLED', 'EXPLOSIVE', 'MAXIMUM_VELOCITY', 'ISOMETRIC', 'CUSTOM'); EXCEPTION WHEN duplicate_object THEN NULL; END $$;

ALTER TABLE training_session_segments
 ADD COLUMN IF NOT EXISTS planned_start_offset_minutes integer NULL,
 ADD COLUMN IF NOT EXISTS transition_duration_minutes integer NULL,
 ADD COLUMN IF NOT EXISTS equipment_notes varchar(1000) NOT NULL DEFAULT '';

DO $$ BEGIN
 ALTER TABLE training_session_segments ADD CONSTRAINT training_session_segments_start_offset_valid CHECK (planned_start_offset_minutes IS NULL OR planned_start_offset_minutes BETWEEN 0 AND 1440);
EXCEPTION WHEN duplicate_object THEN NULL; END $$;
DO $$ BEGIN
 ALTER TABLE training_session_segments ADD CONSTRAINT training_session_segments_transition_valid CHECK (transition_duration_minutes IS NULL OR transition_duration_minutes BETWEEN 1 AND 1440);
EXCEPTION WHEN duplicate_object THEN NULL; END $$;
DO $$ BEGIN
 ALTER TABLE training_session_segments ADD CONSTRAINT training_session_segments_equipment_notes_valid CHECK (equipment_notes = btrim(equipment_notes) AND char_length(equipment_notes) <= 1000);
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

CREATE TABLE IF NOT EXISTS gym_block_prescriptions (
 block_id uuid PRIMARY KEY REFERENCES training_segment_blocks(id) ON DELETE CASCADE,
 structure gym_block_structure NOT NULL, objective training_objective NOT NULL,
 rounds integer NOT NULL DEFAULT 1, round_recovery_seconds integer NULL,
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 CONSTRAINT gym_block_rounds_valid CHECK (rounds BETWEEN 1 AND 100),
 CONSTRAINT gym_block_recovery_valid CHECK (round_recovery_seconds IS NULL OR round_recovery_seconds BETWEEN 1 AND 86400)
);

CREATE TABLE IF NOT EXISTS gym_exercises (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), block_id uuid NOT NULL REFERENCES gym_block_prescriptions(block_id) ON DELETE CASCADE,
 position integer NOT NULL, name varchar(180) NOT NULL, sets integer NULL, repetitions integer NULL,
 duration_seconds integer NULL, distance_metres integer NULL, recovery_seconds integer NULL,
 resistance_kind gym_resistance_kind NULL, resistance_value double precision NULL, resistance_text varchar(180) NULL,
 execution_intent gym_execution_intent NULL, tempo varchar(30) NULL, notes varchar(1000) NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 CONSTRAINT gym_exercises_position_valid CHECK (position > 0),
 CONSTRAINT gym_exercises_name_valid CHECK (name = btrim(name) AND char_length(name) BETWEEN 2 AND 180),
 CONSTRAINT gym_exercises_sets_valid CHECK (sets IS NULL OR sets BETWEEN 1 AND 100),
 CONSTRAINT gym_exercises_repetitions_valid CHECK (repetitions IS NULL OR repetitions BETWEEN 1 AND 10000),
 CONSTRAINT gym_exercises_duration_valid CHECK (duration_seconds IS NULL OR duration_seconds BETWEEN 1 AND 86400),
 CONSTRAINT gym_exercises_distance_valid CHECK (distance_metres IS NULL OR distance_metres BETWEEN 1 AND 100000),
 CONSTRAINT gym_exercises_recovery_valid CHECK (recovery_seconds IS NULL OR recovery_seconds BETWEEN 1 AND 86400),
 CONSTRAINT gym_exercises_prescription_valid CHECK (num_nonnulls(repetitions, duration_seconds, distance_metres) > 0),
 CONSTRAINT gym_exercises_resistance_valid CHECK ((resistance_kind IS NULL AND resistance_value IS NULL AND resistance_text IS NULL) OR (resistance_kind IN ('KILOGRAMS', 'PERCENT_1RM', 'RPE', 'RIR') AND resistance_value IS NOT NULL AND resistance_text IS NULL) OR (resistance_kind = 'BODY_WEIGHT' AND resistance_value IS NULL AND resistance_text IS NULL) OR (resistance_kind IN ('BAND', 'COACH_INSTRUCTION') AND resistance_value IS NULL AND resistance_text = btrim(resistance_text) AND char_length(resistance_text) BETWEEN 1 AND 180)),
 CONSTRAINT gym_exercises_resistance_range_valid CHECK ((resistance_kind <> 'PERCENT_1RM' OR resistance_value BETWEEN 0.01 AND 200) AND (resistance_kind <> 'RPE' OR resistance_value BETWEEN 1 AND 10) AND (resistance_kind <> 'RIR' OR resistance_value BETWEEN 0 AND 20) AND (resistance_kind <> 'KILOGRAMS' OR resistance_value BETWEEN 0.01 AND 10000)),
 CONSTRAINT gym_exercises_tempo_valid CHECK (tempo IS NULL OR (tempo = btrim(tempo) AND char_length(tempo) BETWEEN 1 AND 30)),
 CONSTRAINT gym_exercises_notes_valid CHECK (notes = btrim(notes) AND char_length(notes) <= 1000),
 CONSTRAINT gym_exercises_position_unique UNIQUE (block_id, position)
);
CREATE INDEX IF NOT EXISTS gym_exercises_block_idx ON gym_exercises (block_id, position);

CREATE OR REPLACE FUNCTION move_gym_exercise(p_exercise_id uuid, p_direction integer) RETURNS boolean LANGUAGE plpgsql AS $$
DECLARE current_block uuid; current_position integer; target_id uuid; target_position integer; temporary_position integer;
BEGIN
 IF p_direction NOT IN (-1, 1) THEN RETURN false; END IF;
 SELECT block_id, position INTO current_block, current_position FROM gym_exercises WHERE id = p_exercise_id FOR UPDATE;
 IF current_block IS NULL THEN RETURN false; END IF;
 SELECT id, position INTO target_id, target_position FROM gym_exercises WHERE block_id = current_block AND position = current_position + p_direction FOR UPDATE;
 IF target_id IS NULL THEN RETURN false; END IF;
 SELECT COALESCE(max(position), 0) + 1 INTO temporary_position FROM gym_exercises WHERE block_id = current_block;
 UPDATE gym_exercises SET position = temporary_position, updated_at = clock_timestamp() WHERE id = p_exercise_id;
 UPDATE gym_exercises SET position = current_position, updated_at = clock_timestamp() WHERE id = target_id;
 UPDATE gym_exercises SET position = target_position, updated_at = clock_timestamp() WHERE id = p_exercise_id;
 RETURN true;
END;
$$;
