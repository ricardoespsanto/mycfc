# Password-recovery operations

Password recovery is deliberately non-enumerating. The public request page returns the same confirmation for eligible, unknown, inactive, malformed, minor `CFC-`, throttled, and delivery-failure outcomes. Diagnose delivery from privacy-safe events and aggregate database state; never ask a member to send a reset URL and never place an email address, raw identifier, client IP, or token in a ticket or log query.

## Expected lifecycle

1. `password_recovery_requested` records receipt of a form submission.
2. `password_recovery_queued` means a current adult account produced a pending outbox item. `password_recovery_not_queued`, `password_recovery_throttled`, or `password_recovery_queue_failed` explain safe non-delivery classes without identifying the requester.
3. `password_recovery_delivered` means the SMTP server accepted the message. `password_recovery_delivery_failed` records either a scheduled retry or a permanent stop.
4. `password_recovery_link_rejected` covers malformed, expired, superseded, and replayed links without exposing which case matched an account.
5. `password_recovery_consumed` means the password and durable credential version changed atomically. Every older session is rejected on its next request; a later login captures the new version.

Application logs intentionally contain only the event and outcome fields. Generic email-outbox warnings may also contain an opaque outbox row UUID, but never recipient or token material.

The credential-version migration seeds existing accounts at version `1`. Sessions created by an older application release do not contain that version marker, so deploying this release signs those sessions out on their next request. This is expected and avoids preserving unverifiable pre-migration sessions.

## Delivery diagnosis

On the production host, inspect recent application events:

```sh
sudo docker compose --env-file /etc/mycfc/mycfc.env -f /opt/mycfc/deployment/compose.yaml logs --since 30m app
```

Check aggregate backlog state with the restricted operational database procedure; do not select `email`, `sealed_payload`, `token_digest`, or full message rows:

```sql
SELECT message_type, status, count(*) AS messages,
       min(next_attempt_at) AS oldest_next_attempt,
       max(updated_at) AS latest_update
FROM email_outbox
GROUP BY message_type, status
ORDER BY message_type, status;

SELECT status, attempts, COALESCE(last_error, '') AS safe_error_class, count(*) AS messages
FROM email_outbox
WHERE message_type = 'PASSWORD_RESET'
  AND created_at >= now() - interval '24 hours'
GROUP BY status, attempts, COALESCE(last_error, '')
ORDER BY status, attempts;

SELECT count(*) FILTER (WHERE consumed_at IS NULL AND expires_at > now()) AS active,
       count(*) FILTER (WHERE consumed_at IS NULL AND expires_at <= now()) AS expired,
       count(*) FILTER (WHERE consumed_at IS NOT NULL) AS closed
FROM password_reset_tokens
WHERE created_at >= now() - interval '24 hours';
```

For Amazon SES, verify that sending is enabled and production access is active, then use the repository smoke test. The smoke recipient is the SES simulator and does not contact a real member:

```sh
aws sesv2 get-account --region eu-west-1
sudo /opt/mycfc/deployment/verify-ses.sh
```

Interpretation:

- `queued` without `delivered`, with `retry_scheduled`: check network access, SES SMTP credentials, TLS mode, and the next-attempt time.
- `permanent`: check SES production access, verified domain/DKIM and MAIL FROM health, suppression or rejection information, and SMTP credentials.
- no `queued`: the request was ineligible, malformed, or throttled; the public response must remain generic.
- `delivered` without inbox arrival: the SMTP server accepted the message. Check the recipient's junk/quarantine rules and SES suppression/bounce data without copying the address into application logs.
- active-token backlog after SMTP recovery: ask the member to make a new request. Do not reconstruct, decrypt, print, or manually resend an existing reset link.

## Credential boundaries

Self-service reset applies only to active adult email accounts. Minor `CFC-` credentials remain guardian/administrator recovery only. Administrator `set-password` increments the administrator account's credential version. Issuing or recovering a minor login increments that minor account's version. These operations revoke sessions only for the directly authenticated account whose credential changed; guardian and acting-administrator sessions are unaffected.
