ALTER TABLE users
  ADD COLUMN IF NOT EXISTS leaderboard_visible boolean NOT NULL DEFAULT true;

ALTER TABLE training_session_outcomes
  ADD COLUMN IF NOT EXISTS distance_metres integer NULL;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1
    FROM pg_constraint
    WHERE conname = 'training_outcomes_distance_valid'
      AND conrelid = 'public.training_session_outcomes'::regclass
  ) THEN
    ALTER TABLE training_session_outcomes
      ADD CONSTRAINT training_outcomes_distance_valid
      CHECK (distance_metres IS NULL OR (status = 'COMPLETED' AND distance_metres BETWEEN 1 AND 200000));
  END IF;
END $$;

CREATE INDEX IF NOT EXISTS training_outcomes_leaderboard_idx
  ON training_session_outcomes (user_id, session_id)
  INCLUDE (distance_metres)
  WHERE status = 'COMPLETED' AND distance_metres IS NOT NULL;
