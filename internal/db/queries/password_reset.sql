-- name: FindPasswordResetAccountByEmail :one
SELECT id, email::text AS email
FROM users
WHERE email = sqlc.arg(email) AND is_active = true AND is_dependent = false;

-- name: CreatePasswordReset :one
SELECT issue_password_reset(sqlc.arg(user_id), sqlc.arg(email), sqlc.arg(token_digest), sqlc.arg(sealed_payload), sqlc.arg(created_at), sqlc.arg(expires_at), sqlc.arg(throttle)) AS id;

-- name: ResolvePasswordResetToken :one
SELECT token.id, token.user_id, token.email::text AS email, token.expires_at, token.consumed_at, token.created_at
FROM password_reset_tokens token
JOIN users account ON account.id = token.user_id
WHERE token.token_digest = sqlc.arg(token_digest)
  AND token.consumed_at IS NULL
  AND token.expires_at > sqlc.arg(resolved_at)
  AND account.is_active = true
  AND account.is_dependent = false
  AND account.email = token.email;

-- name: ConsumePasswordResetToken :one
WITH consumed AS (
  UPDATE password_reset_tokens token
  SET consumed_at = sqlc.arg(consumed_at)
  FROM users account
  WHERE token.token_digest = sqlc.arg(token_digest)
    AND token.user_id = account.id
    AND token.consumed_at IS NULL
    AND token.expires_at > sqlc.arg(consumed_at)
    AND account.is_active = true
    AND account.is_dependent = false
    AND account.email = token.email
  RETURNING token.id, token.user_id
), cancelled AS (
  UPDATE email_outbox outbox
  SET status = 'CANCELLED', claimed_at = NULL, updated_at = sqlc.arg(consumed_at)
  FROM consumed
  WHERE outbox.password_reset_token_id = consumed.id AND outbox.status IN ('PENDING', 'SENDING')
  RETURNING outbox.id
)
UPDATE users account
SET password_hash = sqlc.arg(password_hash), credential_version = credential_version + 1, updated_at = sqlc.arg(consumed_at)
FROM consumed
WHERE account.id = consumed.user_id
RETURNING account.id;
