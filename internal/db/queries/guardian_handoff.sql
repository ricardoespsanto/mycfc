-- name: ReconcileGuardianAgeHandoffs :one
SELECT guardian_age_handoff_reconcile();

-- name: GetGuardianAgeHandoffForSubject :one
SELECT item.public_ref::uuid AS public_ref,item.status::text AS status,item.version::bigint AS version,item.birthday::date AS birthday,
 COALESCE(item.proposed_email::text,'') AS proposed_email,item.email_verified_at::timestamptz AS email_verified_at,
 item.identity_confirmed_at::timestamptz AS identity_confirmed_at,item.ready_at::timestamptz AS ready_at,
 item.recovery_required_at::timestamptz AS recovery_required_at,item.completed_at::timestamptz AS completed_at
FROM guardian_age_handoff_for_subject(sqlc.arg(actor_id))
  AS item(public_ref,status,version,birthday,proposed_email,email_verified_at,identity_confirmed_at,ready_at,recovery_required_at,completed_at);

-- name: ListGuardianAgeHandoffNoticesForSubject :many
SELECT item.notice_kind::text AS notice_kind,item.birthday::date AS birthday,item.created_at::timestamptz AS created_at
FROM guardian_age_handoff_notices_for_subject(sqlc.arg(actor_id))
  AS item(notice_kind,birthday,created_at);

-- name: ProposeGuardianAgeHandoffEmail :one
SELECT * FROM guardian_age_handoff_propose_email(sqlc.arg(actor_id),sqlc.arg(expected_version),sqlc.arg(email),sqlc.arg(token_digest),sqlc.arg(expires_at),sqlc.arg(sealed_payload));

-- name: VerifyGuardianAgeHandoffEmail :one
SELECT guardian_age_handoff_verify_email(sqlc.arg(token_digest));

-- name: ListGuardianAgeHandoffsForAdmin :many
SELECT item.public_ref::uuid AS public_ref,item.subject_name::text AS subject_name,item.status::text AS status,
 item.version::bigint AS version,item.birthday::date AS birthday,item.updated_at::timestamptz AS updated_at
FROM guardian_age_handoff_list_for_admin(sqlc.arg(actor_id),sqlc.arg(row_limit),sqlc.arg(row_offset))
  AS item(public_ref,subject_name,status,version,birthday,updated_at);

-- name: GetGuardianAgeHandoffForAdmin :one
SELECT item.public_ref::uuid AS public_ref,item.subject_name::text AS subject_name,item.status::text AS status,
 item.version::bigint AS version,item.birthday::date AS birthday,COALESCE(item.proposed_email::text,'') AS proposed_email,
 item.email_verified_at::timestamptz AS email_verified_at,item.identity_confirmed_at::timestamptz AS identity_confirmed_at,
 item.ready_at::timestamptz AS ready_at,item.recovery_required_at::timestamptz AS recovery_required_at,
 item.completed_at::timestamptz AS completed_at,item.personal_involvement::boolean AS personal_involvement
FROM guardian_age_handoff_get_for_admin(sqlc.arg(actor_id),sqlc.arg(public_ref))
  AS item(public_ref,subject_name,status,version,birthday,proposed_email,email_verified_at,identity_confirmed_at,ready_at,recovery_required_at,completed_at,personal_involvement);

-- name: ConfirmGuardianAgeHandoffIdentity :one
SELECT * FROM guardian_age_handoff_admin_confirm(sqlc.arg(actor_id),sqlc.arg(public_ref),sqlc.arg(expected_version));

-- name: RecoverGuardianAgeHandoffEmail :one
SELECT * FROM guardian_age_handoff_admin_recovery_email(sqlc.arg(actor_id),sqlc.arg(public_ref),sqlc.arg(expected_version),sqlc.arg(email),sqlc.arg(token_digest),sqlc.arg(expires_at),sqlc.arg(sealed_payload));

-- name: ListDueGuardianAgeHandoffNotices :many
SELECT item.handoff_ref::uuid AS handoff_ref,item.relationship_ref::uuid AS relationship_ref,item.guardian_user_id::uuid AS guardian_user_id,
 item.recipient::text AS recipient,item.recipient_verified_at::timestamptz AS recipient_verified_at,
 item.birthday::date AS birthday,item.notice_kind::text AS notice_kind
FROM guardian_age_handoff_due_guardian_notices(sqlc.arg(row_limit))
  AS item(handoff_ref,relationship_ref,guardian_user_id,recipient,recipient_verified_at,birthday,notice_kind);

-- name: EnqueueGuardianAgeHandoffNotice :one
SELECT guardian_age_handoff_enqueue_guardian_notice(sqlc.arg(handoff_ref),sqlc.arg(relationship_ref),sqlc.arg(guardian_user_id),sqlc.arg(recipient),sqlc.arg(recipient_verified_at),sqlc.arg(birthday),sqlc.arg(notice_kind),sqlc.arg(sealed_payload));
