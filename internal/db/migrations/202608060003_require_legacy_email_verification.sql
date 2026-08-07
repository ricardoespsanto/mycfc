-- Correct the initial rollout for legacy adults. Only addresses that have never
-- participated in the verification flow are reset, so genuinely confirmed
-- addresses remain verified. Links are issued on demand from the profile page
-- to avoid sending an unsolicited bulk email during migration.
UPDATE users AS account
SET email_verified_at = NULL,
    updated_at = now()
WHERE account.is_dependent = false
  AND account.email IS NOT NULL
  AND account.email_verified_at IS NOT NULL
  AND NOT EXISTS (
    SELECT 1
    FROM email_verification_tokens AS token
    WHERE token.user_id = account.id
  );
