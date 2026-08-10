ALTER TABLE users
  ADD COLUMN credential_version bigint NOT NULL DEFAULT 1;

ALTER TABLE users
  ADD CONSTRAINT users_credential_version_valid CHECK (credential_version > 0);
