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
  SELECT outbox.id, guardian_reminder.guardian_user_id,
    guardian.email::text AS guardian_email, guardian_reminder.expiry_anchor AS guardian_expiry,
    handoff_delivery.user_id AS handoff_user_id, handoff_delivery.email AS handoff_email,
    handoff_delivery.expires_at AS handoff_expires_at
  FROM email_outbox outbox
  LEFT JOIN email_verification_tokens verification ON verification.id = outbox.verification_token_id
  LEFT JOIN password_reset_tokens reset ON reset.id = outbox.password_reset_token_id
  LEFT JOIN users account ON account.id = COALESCE(verification.user_id, reset.user_id)
  LEFT JOIN guardian_authority_renewal_reminders guardian_reminder ON guardian_reminder.id = outbox.guardian_reminder_id
  LEFT JOIN guardian_authority_relationships guardian_relationship ON guardian_relationship.id = guardian_reminder.relationship_id
  LEFT JOIN users guardian ON guardian.id = guardian_reminder.guardian_user_id
  LEFT JOIN LATERAL guardian_age_handoff_outbox_delivery(outbox.id,sqlc.arg(claimed_at))
    handoff_delivery(deliverable,user_id,email,expires_at) ON true
  WHERE ((outbox.status = 'PENDING' AND outbox.next_attempt_at <= sqlc.arg(claimed_at))
      OR (outbox.status = 'SENDING' AND outbox.claimed_at < sqlc.arg(stale_before)))
    AND (outbox.message_type IN ('PRIVACY_ACKNOWLEDGEMENT', 'PRIVACY_DECISION', 'PRIVACY_PROCESSING_STARTED', 'PRIVACY_COMPLETED') OR (
      COALESCE(verification.consumed_at, reset.consumed_at) IS NULL
    AND COALESCE(verification.expires_at, reset.expires_at) > sqlc.arg(claimed_at)
    AND account.is_active = true AND account.is_dependent = false
    AND account.email = COALESCE(verification.email, reset.email)) OR (
      outbox.message_type IN ('GUARDIAN_RENEWAL_30_DAY', 'GUARDIAN_RENEWAL_7_DAY')
      AND guardian_relationship.id IS NOT NULL
      AND guardian_relationship.guardian_user_id = guardian_reminder.guardian_user_id
      AND guardian.is_active = true AND guardian.erased_at IS NULL AND guardian.is_dependent = false
      AND guardian.email IS NOT NULL AND guardian.email_verified_at IS NOT DISTINCT FROM guardian_reminder.recipient_verified_at
      AND guardian_relationship.state = 'VERIFIED'
      AND guardian_relationship.verified_until = guardian_reminder.expiry_anchor
      AND guardian_relationship.verified_until > sqlc.arg(claimed_at)
      AND guardian_relationship.verified_until <= sqlc.arg(claimed_at) + interval '30 days'
      AND guardian_authority_current(guardian_relationship.guardian_user_id, guardian_relationship.subject_user_id)
      AND NOT EXISTS (SELECT 1 FROM guardian_authority_renewal_requests renewal
        WHERE renewal.relationship_id = guardian_relationship.id AND renewal.expiry_anchor = guardian_reminder.expiry_anchor)
      AND ((outbox.message_type = 'GUARDIAN_RENEWAL_30_DAY'
          AND guardian_reminder.reminder_kind = 'GUARDIAN_RENEWAL_30_DAY'
          AND guardian_relationship.verified_until > sqlc.arg(claimed_at) + interval '7 days')
        OR (outbox.message_type = 'GUARDIAN_RENEWAL_7_DAY'
          AND guardian_reminder.reminder_kind = 'GUARDIAN_RENEWAL_7_DAY'
          AND guardian_relationship.verified_until <= sqlc.arg(claimed_at) + interval '7 days'))
    ) OR (
      outbox.message_type IN ('GUARDIAN_AGE_18_30_DAY','GUARDIAN_AGE_18_7_DAY','GUARDIAN_AGE_18_EMAIL_VERIFY')
      AND handoff_delivery.deliverable
    ))
    AND (outbox.message_type <> 'PRIVACY_COMPLETED' OR privacy_completion_notice_deliverable(outbox.privacy_request_id,sqlc.arg(claimed_at)))
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
  outbox.sealed_payload, outbox.attempts, COALESCE(verification.user_id, reset.user_id, outbox.privacy_requester_id, candidate.guardian_user_id, candidate.handoff_user_id, '00000000-0000-0000-0000-000000000000'::uuid) AS user_id,
  COALESCE(verification.email, reset.email, candidate.guardian_email, candidate.handoff_email, '')::text AS email,
  COALESCE(verification.expires_at, reset.expires_at, candidate.guardian_expiry, candidate.handoff_expires_at) AS expires_at;

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
    OR
    (outbox.message_type IN ('GUARDIAN_RENEWAL_30_DAY', 'GUARDIAN_RENEWAL_7_DAY') AND EXISTS (
      SELECT 1
      FROM guardian_authority_renewal_reminders reminder
      LEFT JOIN guardian_authority_relationships relationship ON relationship.id = reminder.relationship_id
      LEFT JOIN users guardian ON guardian.id = reminder.guardian_user_id
      WHERE reminder.id = outbox.guardian_reminder_id
        AND (relationship.id IS NULL OR guardian.id IS NULL
          OR relationship.guardian_user_id <> reminder.guardian_user_id
          OR guardian.is_active = false OR guardian.erased_at IS NOT NULL OR guardian.is_dependent = true
          OR guardian.email IS NULL OR guardian.email_verified_at IS DISTINCT FROM reminder.recipient_verified_at
          OR relationship.state <> 'VERIFIED' OR relationship.verified_until <> reminder.expiry_anchor
          OR relationship.verified_until <= sqlc.arg(cancelled_at)
          OR relationship.verified_until > sqlc.arg(cancelled_at) + interval '30 days'
          OR NOT guardian_authority_current(relationship.guardian_user_id, relationship.subject_user_id)
          OR EXISTS (SELECT 1 FROM guardian_authority_renewal_requests renewal
            WHERE renewal.relationship_id = relationship.id AND renewal.expiry_anchor = reminder.expiry_anchor)
          OR (outbox.message_type = 'GUARDIAN_RENEWAL_30_DAY'
            AND (reminder.reminder_kind <> 'GUARDIAN_RENEWAL_30_DAY'
              OR relationship.verified_until <= sqlc.arg(cancelled_at) + interval '7 days'))
          OR (outbox.message_type = 'GUARDIAN_RENEWAL_7_DAY'
            AND (reminder.reminder_kind <> 'GUARDIAN_RENEWAL_7_DAY'
              OR relationship.verified_until > sqlc.arg(cancelled_at) + interval '7 days')))
    ))
    OR
    (outbox.message_type IN ('GUARDIAN_AGE_18_30_DAY','GUARDIAN_AGE_18_7_DAY','GUARDIAN_AGE_18_EMAIL_VERIFY')
      AND NOT COALESCE((SELECT delivery.deliverable FROM guardian_age_handoff_outbox_delivery(outbox.id,sqlc.arg(cancelled_at)) delivery),false))
    OR
    (outbox.message_type = 'PRIVACY_COMPLETED' AND NOT privacy_completion_notice_deliverable(outbox.privacy_request_id,sqlc.arg(cancelled_at)))
  );
