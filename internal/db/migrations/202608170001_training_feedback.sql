ALTER TABLE training_session_outcomes
  ADD COLUMN actual_duration_minutes integer NULL,
  ADD COLUMN perceived_exertion smallint NULL,
  ADD COLUMN recovery_feeling smallint NULL,
  ADD COLUMN perception_note varchar(500) NULL,
  ADD COLUMN version integer NOT NULL DEFAULT 1;

ALTER TABLE training_session_outcomes
  ADD CONSTRAINT training_outcomes_feedback_valid CHECK (
    (
      status = 'COMPLETED'
      AND (actual_duration_minutes IS NULL OR actual_duration_minutes BETWEEN 1 AND 1440)
      AND (perceived_exertion IS NULL OR perceived_exertion BETWEEN 0 AND 10)
      AND (recovery_feeling IS NULL OR recovery_feeling BETWEEN 1 AND 5)
      AND (
        perception_note IS NULL
        OR (
          perception_note = btrim(perception_note)
          AND char_length(perception_note) BETWEEN 1 AND 500
        )
      )
    )
    OR (
      status IN ('MISSED', 'REPLACED')
      AND actual_duration_minutes IS NULL
      AND perceived_exertion IS NULL
      AND recovery_feeling IS NULL
      AND perception_note IS NULL
    )
  );
