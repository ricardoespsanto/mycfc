ALTER TABLE training_plans
  ADD COLUMN planned_load_percentage smallint NULL,
  ADD CONSTRAINT training_plans_planned_load_percentage_valid
    CHECK (planned_load_percentage IS NULL OR planned_load_percentage BETWEEN 0 AND 100);
