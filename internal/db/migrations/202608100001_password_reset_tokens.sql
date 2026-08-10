CREATE TABLE IF NOT EXISTS password_reset_tokens (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  email citext NOT NULL,
  token_digest bytea NOT NULL UNIQUE,
  expires_at timestamptz NOT NULL,
  consumed_at timestamptz NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT password_reset_tokens_digest_valid CHECK (octet_length(token_digest) = 32),
  CONSTRAINT password_reset_tokens_expiry_valid CHECK (expires_at > created_at),
  CONSTRAINT password_reset_tokens_consumption_valid CHECK (consumed_at IS NULL OR consumed_at >= created_at)
);
CREATE UNIQUE INDEX IF NOT EXISTS password_reset_tokens_active_user_uidx ON password_reset_tokens (user_id) WHERE consumed_at IS NULL;
CREATE INDEX IF NOT EXISTS password_reset_tokens_expiry_idx ON password_reset_tokens (expires_at) WHERE consumed_at IS NULL;

ALTER TABLE email_outbox ALTER COLUMN verification_token_id DROP NOT NULL;
ALTER TABLE email_outbox ADD COLUMN IF NOT EXISTS message_type varchar(30) NOT NULL DEFAULT 'EMAIL_VERIFICATION';
ALTER TABLE email_outbox ADD COLUMN IF NOT EXISTS password_reset_token_id uuid NULL REFERENCES password_reset_tokens(id) ON DELETE CASCADE;
ALTER TABLE email_outbox ADD COLUMN IF NOT EXISTS sealed_payload bytea NULL;
ALTER TABLE email_outbox DROP CONSTRAINT IF EXISTS email_outbox_message_valid;
ALTER TABLE email_outbox ADD CONSTRAINT email_outbox_message_valid CHECK (
  (message_type = 'EMAIL_VERIFICATION' AND verification_token_id IS NOT NULL AND password_reset_token_id IS NULL AND sealed_payload IS NULL)
  OR (message_type = 'PASSWORD_RESET' AND verification_token_id IS NULL AND password_reset_token_id IS NOT NULL AND sealed_payload IS NOT NULL)
);
DROP INDEX IF EXISTS email_outbox_token_uidx;
CREATE UNIQUE INDEX IF NOT EXISTS email_outbox_verification_token_uidx ON email_outbox (verification_token_id) WHERE verification_token_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS email_outbox_password_reset_token_uidx ON email_outbox (password_reset_token_id) WHERE password_reset_token_id IS NOT NULL;

CREATE OR REPLACE FUNCTION issue_password_reset(p_user_id uuid, p_email citext, p_token_digest bytea, p_sealed_payload bytea, p_created_at timestamptz, p_expires_at timestamptz, p_throttle boolean) RETURNS uuid LANGUAGE plpgsql AS $$
DECLARE issued_id uuid;
BEGIN
  PERFORM pg_advisory_xact_lock(hashtextextended('password-reset:' || p_user_id::text, 0));
  IF NOT EXISTS (SELECT 1 FROM users WHERE id = p_user_id AND email = p_email AND is_active = true AND is_dependent = false) THEN
    RAISE EXCEPTION USING ERRCODE = 'P0001', MESSAGE = 'password_reset_ineligible';
  END IF;
  IF p_throttle AND EXISTS (SELECT 1 FROM password_reset_tokens WHERE user_id = p_user_id AND created_at > p_created_at - interval '1 minute') THEN
    RAISE EXCEPTION USING ERRCODE = 'P0001', MESSAGE = 'password_reset_too_soon';
  END IF;
  IF (SELECT count(*) FROM password_reset_tokens WHERE user_id = p_user_id AND created_at > p_created_at - interval '1 hour') >= 5 THEN
    RAISE EXCEPTION USING ERRCODE = 'P0001', MESSAGE = 'password_reset_limit_exceeded';
  END IF;
  UPDATE email_outbox SET status = 'CANCELLED', claimed_at = NULL, updated_at = p_created_at
  WHERE password_reset_token_id IN (SELECT id FROM password_reset_tokens WHERE user_id = p_user_id AND consumed_at IS NULL) AND status IN ('PENDING', 'SENDING');
  UPDATE password_reset_tokens SET consumed_at = GREATEST(p_created_at, created_at) WHERE user_id = p_user_id AND consumed_at IS NULL;
  INSERT INTO password_reset_tokens (user_id, email, token_digest, expires_at, created_at)
  VALUES (p_user_id, p_email, p_token_digest, p_expires_at, p_created_at) RETURNING id INTO issued_id;
  INSERT INTO email_outbox (message_type, password_reset_token_id, sealed_payload, next_attempt_at, created_at, updated_at)
  VALUES ('PASSWORD_RESET', issued_id, p_sealed_payload, p_created_at, p_created_at, p_created_at);
  RETURN issued_id;
END;
$$;
