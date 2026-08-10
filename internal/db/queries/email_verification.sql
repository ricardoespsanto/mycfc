-- name: CreateEmailVerification :one
SELECT issue_email_verification(sqlc.arg(user_id), sqlc.arg(email), sqlc.arg(created_at), sqlc.arg(expires_at), sqlc.arg(throttle)) AS id;

-- name: ConsumeEmailVerification :one
WITH consumed AS (
  UPDATE email_verification_tokens token
  SET consumed_at = sqlc.arg(consumed_at)
  FROM users account
  WHERE token.id = sqlc.arg(id)
    AND token.user_id = account.id
    AND token.consumed_at IS NULL
    AND token.expires_at > sqlc.arg(consumed_at)
    AND account.is_active = true
    AND account.is_dependent = false
    AND account.email = token.email
  RETURNING token.user_id
)
UPDATE users account SET email_verified_at = sqlc.arg(consumed_at), updated_at = now()
FROM consumed
WHERE account.id = consumed.user_id
RETURNING account.id;

-- name: GetEmailVerificationToken :one
SELECT id, user_id, email, expires_at, consumed_at, created_at
FROM email_verification_tokens WHERE id = sqlc.arg(id);

-- name: ClaimEmailOutbox :one
WITH candidate AS (
  SELECT outbox.id
  FROM email_outbox outbox
  LEFT JOIN email_verification_tokens verification ON verification.id = outbox.verification_token_id
  LEFT JOIN password_reset_tokens reset ON reset.id = outbox.password_reset_token_id
  JOIN users account ON account.id = COALESCE(verification.user_id, reset.user_id)
  WHERE ((outbox.status = 'PENDING' AND outbox.next_attempt_at <= sqlc.arg(claimed_at))
      OR (outbox.status = 'SENDING' AND outbox.claimed_at < sqlc.arg(stale_before)))
    AND COALESCE(verification.consumed_at, reset.consumed_at) IS NULL
    AND COALESCE(verification.expires_at, reset.expires_at) > sqlc.arg(claimed_at)
    AND account.is_active = true AND account.is_dependent = false
    AND account.email = COALESCE(verification.email, reset.email)
  ORDER BY outbox.next_attempt_at, outbox.created_at, outbox.id
  FOR UPDATE OF outbox SKIP LOCKED
  LIMIT 1
)
UPDATE email_outbox outbox
SET status = 'SENDING', attempts = attempts + 1, claimed_at = sqlc.arg(claimed_at), updated_at = sqlc.arg(claimed_at)
FROM candidate
LEFT JOIN email_verification_tokens verification ON verification.id = (SELECT verification_token_id FROM email_outbox WHERE id = candidate.id)
LEFT JOIN password_reset_tokens reset ON reset.id = (SELECT password_reset_token_id FROM email_outbox WHERE id = candidate.id)
WHERE outbox.id = candidate.id
RETURNING outbox.id, outbox.message_type, outbox.verification_token_id, outbox.password_reset_token_id,
  outbox.sealed_payload, outbox.attempts, COALESCE(verification.user_id, reset.user_id) AS user_id,
  COALESCE(verification.email, reset.email)::text AS email, COALESCE(verification.expires_at, reset.expires_at) AS expires_at;

-- name: CompleteEmailOutbox :execrows
UPDATE email_outbox SET status = 'SENT', claimed_at = NULL, sent_at = sqlc.arg(completed_at), last_error = NULL, updated_at = sqlc.arg(completed_at)
WHERE id = sqlc.arg(id) AND status = 'SENDING';

-- name: RetryEmailOutbox :execrows
UPDATE email_outbox SET status = 'PENDING', claimed_at = NULL, next_attempt_at = sqlc.arg(next_attempt_at), last_error = sqlc.arg(last_error), updated_at = sqlc.arg(failed_at)
WHERE id = sqlc.arg(id) AND status = 'SENDING';

-- name: FailEmailOutbox :execrows
UPDATE email_outbox SET status = 'FAILED', claimed_at = NULL, last_error = sqlc.arg(last_error), updated_at = sqlc.arg(failed_at)
WHERE id = sqlc.arg(id) AND status = 'SENDING';

-- name: CancelUndeliverableEmailOutbox :execrows
UPDATE email_outbox outbox SET status = 'CANCELLED', claimed_at = NULL, updated_at = sqlc.arg(cancelled_at)
WHERE outbox.status IN ('PENDING', 'SENDING')
  AND (
    (outbox.message_type = 'EMAIL_VERIFICATION' AND EXISTS (
      SELECT 1 FROM email_verification_tokens token JOIN users account ON account.id = token.user_id
      WHERE token.id = outbox.verification_token_id
        AND (token.consumed_at IS NOT NULL OR token.expires_at <= sqlc.arg(cancelled_at)
          OR account.is_active = false OR account.is_dependent = true OR account.email <> token.email)
    ))
    OR
    (outbox.message_type = 'PASSWORD_RESET' AND EXISTS (
      SELECT 1 FROM password_reset_tokens token JOIN users account ON account.id = token.user_id
      WHERE token.id = outbox.password_reset_token_id
        AND (token.consumed_at IS NOT NULL OR token.expires_at <= sqlc.arg(cancelled_at)
          OR account.is_active = false OR account.is_dependent = true OR account.email <> token.email)
    ))
  );
