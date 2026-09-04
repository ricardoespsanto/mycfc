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

ALTER TABLE training_plans
  ADD COLUMN cycle_id uuid NULL,
  ADD CONSTRAINT training_plans_id_group_season_unique UNIQUE (id, training_group_id, season_id),
  ADD CONSTRAINT training_plans_cycle_scope_fk FOREIGN KEY (cycle_id, training_group_id, season_id)
    REFERENCES training_cycles(id, training_group_id, season_id) ON DELETE RESTRICT;
CREATE INDEX training_plans_cycle_week_idx ON training_plans (cycle_id, week_start) WHERE cycle_id IS NOT NULL;

ALTER TABLE training_copy_events DROP CONSTRAINT training_copy_events_source_kind_check;
ALTER TABLE training_copy_events DROP CONSTRAINT training_copy_events_destination_kind_check;
ALTER TABLE training_copy_events
  ADD CONSTRAINT training_copy_events_source_kind_check CHECK (source_kind IN ('BLOCK', 'SEGMENT', 'SESSION', 'DAY', 'WEEK', 'ROUTINE', 'CYCLE')),
  ADD CONSTRAINT training_copy_events_destination_kind_check CHECK (destination_kind IN ('BLOCK', 'SEGMENT', 'SESSION', 'WEEK', 'CYCLE'));
