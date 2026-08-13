ALTER TABLE training_plans
ADD COLUMN IF NOT EXISTS season_id uuid NULL REFERENCES seasons(id) ON DELETE RESTRICT;

DO $$ BEGIN
 IF NOT EXISTS (
  SELECT 1 FROM pg_constraint
  WHERE conname = 'training_plans_structured_season_valid'
    AND conrelid = 'training_plans'::regclass
 ) THEN
  ALTER TABLE training_plans
  ADD CONSTRAINT training_plans_structured_season_valid
  CHECK ((training_group_id IS NULL AND season_id IS NULL) OR (training_group_id IS NOT NULL AND season_id IS NOT NULL));
 END IF;
END $$;
