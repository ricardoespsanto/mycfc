-- name: CanVerifyGuardianAuthority :one
SELECT guardian_authority_can_verify(sqlc.arg(actor_id));

-- name: HasVerifiedGuardianAuthority :one
SELECT COALESCE(EXISTS(
 SELECT 1 FROM guardian_authority_relationships relationship
 WHERE relationship.guardian_user_id=sqlc.arg(actor_id)
  AND guardian_authority_current(relationship.guardian_user_id,relationship.subject_user_id)
),false)::boolean;

-- name: IsGuardianAuthorityCurrent :one
SELECT guardian_authority_current(sqlc.arg(guardian_id),sqlc.arg(subject_id));

-- name: ReconcileGuardianAuthorityCutoffs :one
SELECT guardian_authority_reconcile_cutoffs();

-- name: HasCurrentGuardianAuthorityForSubject :one
SELECT EXISTS(SELECT 1 FROM guardian_authority_relationships relationship
 WHERE relationship.subject_user_id=sqlc.arg(subject_id)
  AND guardian_authority_current(relationship.guardian_user_id,relationship.subject_user_id));

-- name: ListActiveGuardianAuthorityEvidenceTypes :many
SELECT unnest(policy.evidence_types)::text AS evidence_type
FROM guardian_authority_policies policy
WHERE policy.enabled AND policy.adopted_at<=clock_timestamp()
ORDER BY evidence_type;

-- name: ListActiveGuardianAuthorityReasonCodes :many
SELECT unnest(policy.reason_codes)::text AS reason_code
FROM guardian_authority_policies policy
WHERE policy.enabled AND policy.adopted_at<=clock_timestamp()
ORDER BY reason_code;

-- name: AdoptGuardianAuthorityPolicy :one
SELECT * FROM guardian_authority_adopt_policy(sqlc.arg(actor_id),sqlc.arg(version),sqlc.arg(evidence_types),
 sqlc.arg(reason_codes),sqlc.arg(validity_days),sqlc.arg(review_days));

-- name: SetGuardianAuthorityPolicyEnabled :one
SELECT * FROM guardian_authority_set_policy_enabled(sqlc.arg(actor_id),sqlc.arg(version),sqlc.arg(enabled));

-- name: GrantGuardianVerifier :one
SELECT * FROM guardian_authority_grant_verifier(sqlc.arg(actor_id),sqlc.arg(user_id));

-- name: RevokeGuardianVerifier :one
SELECT * FROM guardian_authority_revoke_verifier(sqlc.arg(actor_id),sqlc.arg(user_id));

-- name: ListGuardianRelationshipsForGuardian :many
SELECT relationship_id,relationship_ref,subject_user_id,submitted_label,state,version,created_at,
 verified_until,review_due_at,conflict,COALESCE(subject_name,'')::text AS subject_name,date_of_birth,
 COALESCE(minor_login_id,'')::text AS minor_login_id,
 COALESCE(leaderboard_visible,false)::boolean AS leaderboard_visible,
 COALESCE(profile_complete,false)::boolean AS profile_complete,
 renewal_ref,response_code AS renewal_response,renewal_status,renewal_expiry_anchor,
 COALESCE(handoff.status::text,'') AS age_handoff_status,handoff.birthday::date AS age_handoff_birthday
FROM guardian_authority_guardian_disclosures disclosure
LEFT JOIN LATERAL guardian_age_handoff_for_guardian(sqlc.arg(guardian_id),disclosure.subject_user_id)
 AS handoff(status,birthday) ON true
WHERE disclosure.guardian_user_id=sqlc.arg(guardian_id)
ORDER BY CASE WHEN state IN('PENDING','VERIFIED','SUSPENDED') THEN 0 ELSE 1 END,
 created_at DESC,relationship_id DESC
LIMIT sqlc.arg(row_limit);

-- name: ListPendingGuardianAuthorityRequests :many
WITH access AS (SELECT guardian_authority_record_review_view(sqlc.arg(actor_id),NULL::uuid) AS allowed)
SELECT relationship.public_ref AS relationship_ref,relationship.guardian_user_id,guardian.name AS guardian_name,
 relationship.subject_user_id,subject.name AS subject_name,subject.date_of_birth,
 disclosure.state,relationship.version,relationship.created_at,
 COALESCE(latest.actor_ref,'00000000-0000-0000-0000-000000000000'::uuid) AS deciding_admin_user_id,
 latest.evidence_type AS evidence_category,
 latest.reason_code,latest.occurred_at AS decision_at,relationship.verified_until,relationship.review_due_at,
 relationship.conflict,relationship.conflict_actor_ref,
 renewal.renewal_ref,renewal.response_code AS renewal_response,renewal.renewal_status,renewal.expiry_anchor AS renewal_expiry_anchor,
 guardian_authority_personally_involved(sqlc.arg(actor_id),relationship.public_ref) AS personal_involvement
FROM guardian_authority_relationships relationship
JOIN users guardian ON guardian.id=relationship.guardian_user_id
JOIN users subject ON subject.id=relationship.subject_user_id
JOIN guardian_authority_guardian_disclosures disclosure ON disclosure.relationship_id=relationship.id
LEFT JOIN LATERAL (
 SELECT event.actor_ref,event.evidence_type,event.reason_code,event.occurred_at
 FROM guardian_authority_events event
 WHERE event.relationship_id=relationship.id AND event.actor_role='ADMIN'
 ORDER BY event.relationship_version DESC LIMIT 1
) latest ON true
LEFT JOIN guardian_authority_latest_renewals renewal ON renewal.relationship_id=relationship.id
WHERE (SELECT allowed FROM access)
 AND disclosure.state IN('PENDING','VERIFIED','SUSPENDED','EXPIRED')
ORDER BY relationship.created_at,relationship.id
LIMIT sqlc.arg(row_limit) OFFSET sqlc.arg(row_offset);

-- name: GetGuardianAuthorityRequestForVerifier :one
WITH access AS (SELECT guardian_authority_record_review_view(sqlc.arg(actor_id),sqlc.arg(relationship_ref)) AS allowed)
SELECT relationship.id AS relationship_id,relationship.public_ref AS relationship_ref,
 relationship.guardian_user_id,guardian.name AS guardian_name,
 relationship.subject_user_id,subject.name AS subject_name,subject.date_of_birth,
 relationship.submitted_label,relationship.state AS stored_state,disclosure.state,relationship.version,relationship.created_at,
 COALESCE(latest.actor_ref,'00000000-0000-0000-0000-000000000000'::uuid) AS deciding_admin_user_id,
 latest.evidence_type AS evidence_category,
 latest.reason_code,latest.occurred_at AS decision_at,relationship.verified_until,relationship.review_due_at,
 relationship.conflict,relationship.conflict_actor_ref,
 renewal.renewal_ref,renewal.response_code AS renewal_response,renewal.renewal_status,renewal.expiry_anchor AS renewal_expiry_anchor,
 guardian_authority_personally_involved(sqlc.arg(actor_id),relationship.public_ref) AS personal_involvement
FROM guardian_authority_relationships relationship
JOIN users guardian ON guardian.id=relationship.guardian_user_id
JOIN users subject ON subject.id=relationship.subject_user_id
JOIN guardian_authority_guardian_disclosures disclosure ON disclosure.relationship_id=relationship.id
LEFT JOIN LATERAL (
 SELECT event.actor_ref,event.evidence_type,event.reason_code,event.occurred_at
 FROM guardian_authority_events event
 WHERE event.relationship_id=relationship.id AND event.actor_role='ADMIN'
 ORDER BY event.relationship_version DESC LIMIT 1
) latest ON true
LEFT JOIN guardian_authority_latest_renewals renewal ON renewal.relationship_id=relationship.id
WHERE relationship.public_ref=sqlc.arg(relationship_ref)
 AND (SELECT allowed FROM access);

-- name: TransitionGuardianAuthority :one
SELECT * FROM guardian_authority_transition(sqlc.arg(actor_id),sqlc.arg(relationship_ref),sqlc.arg(expected_version),
 sqlc.arg(target_state),sqlc.narg(evidence_type),sqlc.narg(evidence_reference),sqlc.narg(evidence_sha256),sqlc.narg(reason_code));

-- name: AdminTransitionGuardianAuthority :one
SELECT * FROM guardian_authority_admin_transition(sqlc.arg(actor_id),sqlc.arg(relationship_ref),sqlc.arg(expected_version),
 sqlc.arg(action),sqlc.arg(evidence_category),sqlc.arg(reason_code));

-- name: ReserveGuardianApplicationRate :exec
SELECT guardian_application_reserve(sqlc.arg(account_digest),sqlc.arg(network_digest),sqlc.arg(kind));

-- name: PruneGuardianApplicationRateEvents :one
SELECT guardian_application_prune();

-- name: IssueGuardianAuthorityInvitation :one
SELECT public_ref,invited_email,issued_at,expires_at,revoked_at,consumed_at
FROM guardian_authority_issue_invitation(sqlc.arg(actor_id),sqlc.arg(invited_email),sqlc.arg(token_digest));

-- name: ListGuardianAuthorityInvitations :many
SELECT invitation.public_ref,invitation.invited_email,invitation.issued_at,invitation.expires_at,
 invitation.revoked_at,invitation.consumed_at
FROM guardian_authority_list_invitations(sqlc.arg(actor_id),sqlc.arg(row_limit)) invitation;

-- name: RevokeGuardianAuthorityInvitation :one
SELECT guardian_authority_revoke_invitation(sqlc.arg(actor_id),sqlc.arg(public_ref));

-- name: SubmitGuardianAuthorityRenewal :one
SELECT guardian_authority_submit_renewal(sqlc.arg(actor_id),sqlc.arg(relationship_ref),sqlc.arg(expected_version),sqlc.arg(response_code))::text AS result;

-- name: ListDueGuardianRenewalReminders :many
SELECT reminder.relationship_ref::uuid AS relationship_ref,reminder.guardian_user_id::uuid AS guardian_user_id,
 reminder.recipient::text AS recipient,reminder.recipient_verified_at::timestamptz AS recipient_verified_at,
 reminder.expiry_anchor::timestamptz AS expiry_anchor,reminder.reminder_kind::text AS reminder_kind
FROM guardian_authority_due_renewal_reminders(sqlc.arg(row_limit))
 AS reminder(relationship_ref,guardian_user_id,recipient,recipient_verified_at,expiry_anchor,reminder_kind);

-- name: EnqueueGuardianRenewalReminder :one
SELECT guardian_authority_enqueue_renewal_reminder(sqlc.arg(relationship_ref),sqlc.arg(guardian_user_id),sqlc.arg(recipient),
 sqlc.arg(recipient_verified_at),sqlc.arg(expiry_anchor),sqlc.arg(reminder_kind),sqlc.arg(sealed_payload));
