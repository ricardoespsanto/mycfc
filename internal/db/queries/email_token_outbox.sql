-- Token mail must not parse or plan joins against guardian-only relations:
-- the runtime web role has no SELECT privilege on those relations.
-- name: ClaimTokenEmailOutbox :one
WITH candidate AS (
  SELECT outbox.id
  FROM email_outbox outbox
  LEFT JOIN email_verification_tokens verification ON verification.id = outbox.verification_token_id
  LEFT JOIN password_reset_tokens reset ON reset.id = outbox.password_reset_token_id
  JOIN users account ON account.id = COALESCE(verification.user_id, reset.user_id)
  WHERE outbox.message_type IN ('EMAIL_VERIFICATION', 'PASSWORD_RESET')
    AND ((outbox.status = 'PENDING' AND outbox.next_attempt_at <= sqlc.arg(claimed_at))
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
  outbox.sealed_payload, outbox.attempts, COALESCE(verification.user_id, reset.user_id, '00000000-0000-0000-0000-000000000000'::uuid) AS user_id,
  COALESCE(verification.email, reset.email, '')::text AS email,
  COALESCE(verification.expires_at, reset.expires_at) AS expires_at;

-- name: CancelUndeliverableTokenEmailOutbox :execrows
UPDATE email_outbox outbox SET status = 'CANCELLED', claimed_at = NULL, updated_at = sqlc.arg(cancelled_at)
WHERE outbox.status IN ('PENDING', 'SENDING')
  AND ((outbox.message_type = 'EMAIL_VERIFICATION' AND EXISTS (
      SELECT 1 FROM email_verification_tokens token JOIN users account ON account.id = token.user_id
      WHERE token.id = outbox.verification_token_id
        AND (token.consumed_at IS NOT NULL OR token.expires_at <= sqlc.arg(cancelled_at)
          OR account.is_active = false OR account.is_dependent = true OR account.email <> token.email)
    )) OR (outbox.message_type = 'PASSWORD_RESET' AND EXISTS (
      SELECT 1 FROM password_reset_tokens token JOIN users account ON account.id = token.user_id
      WHERE token.id = outbox.password_reset_token_id
        AND (token.consumed_at IS NOT NULL OR token.expires_at <= sqlc.arg(cancelled_at)
          OR account.is_active = false OR account.is_dependent = true OR account.email <> token.email)
    )));
