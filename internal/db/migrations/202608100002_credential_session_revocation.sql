ALTER TABLE users
  ADD COLUMN IF NOT EXISTS credential_version bigint NOT NULL DEFAULT 1;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1
    FROM pg_constraint
    WHERE conname = 'users_credential_version_valid'
      AND conrelid = 'users'::regclass
  ) THEN
    ALTER TABLE users
      ADD CONSTRAINT users_credential_version_valid CHECK (credential_version > 0);
  END IF;
END
$$;
