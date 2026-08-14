DO $$ BEGIN CREATE TYPE training_variation_group_kind AS ENUM ('SUBGROUP', 'CREW'); EXCEPTION WHEN duplicate_object THEN NULL; END $$;
DO $$ BEGIN CREATE TYPE training_variation_subject_kind AS ENUM ('SEGMENT', 'BLOCK', 'WATER_STEP', 'GYM_EXERCISE'); EXCEPTION WHEN duplicate_object THEN NULL; END $$;
DO $$ BEGIN CREATE TYPE training_variation_operation AS ENUM ('OMIT', 'REPLACE', 'ADD', 'OVERRIDE'); EXCEPTION WHEN duplicate_object THEN NULL; END $$;

CREATE TABLE IF NOT EXISTS training_variation_groups (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 training_group_id uuid NOT NULL REFERENCES training_groups(id) ON DELETE CASCADE,
 name varchar(120) NOT NULL,
 kind training_variation_group_kind NOT NULL,
 craft_modality_id uuid NULL REFERENCES modalities(id) ON DELETE RESTRICT,
 effective_from date NOT NULL,
 effective_until date NULL,
 competition_event_id uuid NULL REFERENCES events(id) ON DELETE RESTRICT,
 open_ended_exception boolean NOT NULL DEFAULT false,
 created_by_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 CONSTRAINT training_variation_groups_name_valid CHECK (name = btrim(name) AND char_length(name) BETWEEN 2 AND 120),
 CONSTRAINT training_variation_groups_dates_valid CHECK (effective_until IS NULL OR effective_from <= effective_until),
 CONSTRAINT training_variation_groups_shape_valid CHECK ((kind = 'SUBGROUP' AND craft_modality_id IS NULL AND competition_event_id IS NULL AND NOT open_ended_exception) OR (kind = 'CREW' AND craft_modality_id IS NOT NULL AND (effective_until IS NOT NULL OR competition_event_id IS NOT NULL OR open_ended_exception))),
 CONSTRAINT training_variation_groups_scope_name_unique UNIQUE (training_group_id, name)
);
CREATE INDEX IF NOT EXISTS training_variation_groups_training_group_idx ON training_variation_groups (training_group_id, effective_from, effective_until);

CREATE TABLE IF NOT EXISTS training_variation_group_members (
 variation_group_id uuid NOT NULL REFERENCES training_variation_groups(id) ON DELETE CASCADE,
 membership_id uuid NOT NULL REFERENCES user_memberships(id) ON DELETE RESTRICT,
 added_by_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 added_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (variation_group_id, membership_id)
);
CREATE INDEX IF NOT EXISTS training_variation_group_members_membership_idx ON training_variation_group_members (membership_id, variation_group_id);

CREATE TABLE IF NOT EXISTS training_variations (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 plan_id uuid NOT NULL REFERENCES training_plans(id) ON DELETE CASCADE,
 target_membership_id uuid NULL REFERENCES user_memberships(id) ON DELETE RESTRICT,
 target_group_id uuid NULL REFERENCES training_variation_groups(id) ON DELETE RESTRICT,
 subject_kind training_variation_subject_kind NOT NULL,
 subject_id uuid NOT NULL,
 operation training_variation_operation NOT NULL,
 change_summary varchar(500) NOT NULL,
 patch jsonb NOT NULL DEFAULT '{}'::jsonb,
 version integer NOT NULL DEFAULT 1,
 is_active boolean NOT NULL DEFAULT true,
 retired_at timestamptz NULL,
 retired_by_id uuid NULL REFERENCES users(id) ON DELETE RESTRICT,
 created_by_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 CONSTRAINT training_variations_target_valid CHECK (num_nonnulls(target_membership_id, target_group_id) = 1),
 CONSTRAINT training_variations_summary_valid CHECK (change_summary = btrim(change_summary) AND char_length(change_summary) BETWEEN 2 AND 500),
 CONSTRAINT training_variations_patch_valid CHECK (jsonb_typeof(patch) = 'object' AND octet_length(patch::text) <= 8000),
 CONSTRAINT training_variations_version_valid CHECK (version > 0),
 CONSTRAINT training_variations_lifecycle_valid CHECK ((is_active AND retired_at IS NULL AND retired_by_id IS NULL) OR (NOT is_active AND retired_at IS NOT NULL AND retired_by_id IS NOT NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS training_variations_active_athlete_subject_idx ON training_variations (plan_id, target_membership_id, subject_kind, subject_id) WHERE is_active AND target_membership_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS training_variations_active_group_subject_idx ON training_variations (plan_id, target_group_id, subject_kind, subject_id) WHERE is_active AND target_group_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS training_variations_plan_idx ON training_variations (plan_id, is_active, subject_kind, subject_id);
