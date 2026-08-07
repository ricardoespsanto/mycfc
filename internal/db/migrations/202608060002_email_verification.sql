-- Additive email verification rollout. Addresses that predate this migration
-- are trusted; every adult created afterwards starts unverified.
ALTER TABLE users ADD COLUMN IF NOT EXISTS email_verified_at timestamptz NULL;
UPDATE users SET email_verified_at = now()
WHERE is_dependent = false AND email IS NOT NULL AND email_verified_at IS NULL;

CREATE TABLE IF NOT EXISTS email_verification_tokens (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  email citext NOT NULL,
  expires_at timestamptz NOT NULL,
  consumed_at timestamptz NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT email_verification_tokens_expiry_valid CHECK (expires_at > created_at),
  CONSTRAINT email_verification_tokens_consumption_valid CHECK (consumed_at IS NULL OR consumed_at >= created_at)
);
CREATE UNIQUE INDEX IF NOT EXISTS email_verification_tokens_active_user_uidx ON email_verification_tokens (user_id) WHERE consumed_at IS NULL;
CREATE INDEX IF NOT EXISTS email_verification_tokens_expiry_idx ON email_verification_tokens (expires_at) WHERE consumed_at IS NULL;

CREATE TABLE IF NOT EXISTS email_outbox (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  verification_token_id uuid NOT NULL REFERENCES email_verification_tokens(id) ON DELETE CASCADE,
  status varchar(20) NOT NULL DEFAULT 'PENDING',
  attempts integer NOT NULL DEFAULT 0,
  next_attempt_at timestamptz NOT NULL DEFAULT now(),
  claimed_at timestamptz NULL,
  sent_at timestamptz NULL,
  last_error varchar(500) NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT email_outbox_status_valid CHECK (status IN ('PENDING', 'SENDING', 'SENT', 'FAILED', 'CANCELLED')),
  CONSTRAINT email_outbox_attempts_valid CHECK (attempts >= 0),
  CONSTRAINT email_outbox_lifecycle_valid CHECK (
    (status = 'PENDING' AND claimed_at IS NULL AND sent_at IS NULL)
    OR (status = 'SENDING' AND claimed_at IS NOT NULL AND sent_at IS NULL)
    OR (status = 'SENT' AND claimed_at IS NULL AND sent_at IS NOT NULL)
    OR (status IN ('FAILED', 'CANCELLED') AND claimed_at IS NULL AND sent_at IS NULL)
  )
);
CREATE UNIQUE INDEX IF NOT EXISTS email_outbox_token_uidx ON email_outbox (verification_token_id);
CREATE INDEX IF NOT EXISTS email_outbox_delivery_idx ON email_outbox (next_attempt_at, created_at) WHERE status = 'PENDING';

CREATE OR REPLACE FUNCTION issue_email_verification(p_user_id uuid, p_email citext, p_created_at timestamptz, p_expires_at timestamptz, p_throttle boolean) RETURNS uuid LANGUAGE plpgsql AS $$
DECLARE issued_id uuid;
BEGIN
  PERFORM pg_advisory_xact_lock(hashtextextended(p_user_id::text, 0));
  IF p_throttle AND EXISTS (SELECT 1 FROM email_verification_tokens WHERE user_id = p_user_id AND created_at > p_created_at - interval '1 minute') THEN
    RAISE EXCEPTION USING ERRCODE = 'P0001', MESSAGE = 'email_verification_too_soon';
  END IF;
  UPDATE email_outbox SET status = 'CANCELLED', claimed_at = NULL, updated_at = p_created_at
  WHERE verification_token_id IN (SELECT id FROM email_verification_tokens WHERE user_id = p_user_id AND consumed_at IS NULL) AND status IN ('PENDING', 'SENDING');
  UPDATE email_verification_tokens SET consumed_at = GREATEST(p_created_at, created_at) WHERE user_id = p_user_id AND consumed_at IS NULL;
  INSERT INTO email_verification_tokens (user_id, email, expires_at, created_at)
  VALUES (p_user_id, p_email, p_expires_at, p_created_at) RETURNING id INTO issued_id;
  INSERT INTO email_outbox (verification_token_id, next_attempt_at, created_at, updated_at)
  VALUES (issued_id, p_created_at, p_created_at, p_created_at);
  RETURN issued_id;
END;
$$;
