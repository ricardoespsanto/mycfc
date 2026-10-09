-- #335 expand-only stage. Old ON CONFLICT writers still require
-- user_memberships_user_season_programme_unique and legacy membership semantics.
-- No dated writer or sensitive exception evidence is enabled by this migration.
ALTER TABLE user_memberships
  ADD COLUMN age_exception_reason varchar(500),
  ADD COLUMN age_exception_by_id uuid REFERENCES users(id) ON DELETE RESTRICT,
  ADD COLUMN age_exception_at timestamptz,
  ADD CONSTRAINT user_memberships_age_exception_disabled CHECK (
    age_exception_reason IS NULL AND age_exception_by_id IS NULL AND age_exception_at IS NULL
  );
