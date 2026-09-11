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
 COALESCE(profile_complete,false)::boolean AS profile_complete
FROM guardian_authority_guardian_disclosures
WHERE guardian_user_id=sqlc.arg(guardian_id)
ORDER BY CASE WHEN state IN('PENDING','VERIFIED','SUSPENDED') THEN 0 ELSE 1 END,
 created_at DESC,relationship_id DESC
LIMIT sqlc.arg(row_limit);

-- name: ListPendingGuardianAuthorityRequests :many
SELECT relationship.public_ref AS relationship_ref,relationship.guardian_user_id,guardian.name AS guardian_name,
 relationship.subject_user_id,subject.name AS subject_name,subject.date_of_birth,
 disclosure.state,relationship.version,relationship.created_at,
 COALESCE(latest.actor_ref,'00000000-0000-0000-0000-000000000000'::uuid) AS verifier_user_id,
 latest.evidence_type,latest.evidence_reference,latest.evidence_sha256,
 latest.reason_code,latest.occurred_at AS decision_at,relationship.verified_until,relationship.review_due_at,
 relationship.conflict,relationship.conflict_actor_ref
FROM guardian_authority_relationships relationship
JOIN users guardian ON guardian.id=relationship.guardian_user_id
JOIN users subject ON subject.id=relationship.subject_user_id
JOIN guardian_authority_guardian_disclosures disclosure ON disclosure.relationship_id=relationship.id
LEFT JOIN LATERAL (
 SELECT event.actor_ref,event.evidence_type,event.evidence_reference,event.evidence_sha256,event.reason_code,event.occurred_at
 FROM guardian_authority_events event
 WHERE event.relationship_id=relationship.id AND event.actor_role='VERIFIER'
 ORDER BY event.relationship_version DESC LIMIT 1
) latest ON true
WHERE guardian_authority_can_verify(sqlc.arg(actor_id))
 AND disclosure.state IN('PENDING','VERIFIED','SUSPENDED','EXPIRED')
ORDER BY relationship.created_at,relationship.id
LIMIT sqlc.arg(row_limit) OFFSET sqlc.arg(row_offset);

-- name: GetGuardianAuthorityRequestForVerifier :one
SELECT relationship.id AS relationship_id,relationship.public_ref AS relationship_ref,
 relationship.guardian_user_id,guardian.name AS guardian_name,
 relationship.subject_user_id,subject.name AS subject_name,subject.date_of_birth,
 relationship.submitted_label,relationship.state AS stored_state,disclosure.state,relationship.version,relationship.created_at,
 COALESCE(latest.actor_ref,'00000000-0000-0000-0000-000000000000'::uuid) AS verifier_user_id,
 latest.evidence_type,latest.evidence_reference,latest.evidence_sha256,
 latest.reason_code,latest.occurred_at AS decision_at,relationship.verified_until,relationship.review_due_at,
 relationship.conflict,relationship.conflict_actor_ref
FROM guardian_authority_relationships relationship
JOIN users guardian ON guardian.id=relationship.guardian_user_id
JOIN users subject ON subject.id=relationship.subject_user_id
JOIN guardian_authority_guardian_disclosures disclosure ON disclosure.relationship_id=relationship.id
LEFT JOIN LATERAL (
 SELECT event.actor_ref,event.evidence_type,event.evidence_reference,event.evidence_sha256,event.reason_code,event.occurred_at
 FROM guardian_authority_events event
 WHERE event.relationship_id=relationship.id AND event.actor_role='VERIFIER'
 ORDER BY event.relationship_version DESC LIMIT 1
) latest ON true
WHERE relationship.public_ref=sqlc.arg(relationship_ref)
 AND guardian_authority_can_verify(sqlc.arg(actor_id));

-- name: TransitionGuardianAuthority :one
SELECT * FROM guardian_authority_transition(sqlc.arg(actor_id),sqlc.arg(relationship_ref),sqlc.arg(expected_version),
 sqlc.arg(target_state),sqlc.narg(evidence_type),sqlc.narg(evidence_reference),sqlc.narg(evidence_sha256),sqlc.narg(reason_code));
