-- name: GetPrivacyActivation :one
SELECT * FROM privacy_request_activation WHERE singleton = true;

-- name: GetPrivacyPolicy :one
SELECT * FROM privacy_request_policies WHERE version = sqlc.arg(version);

-- name: GetPrivacyAccountForUpdate :one
SELECT * FROM users WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: GetPrivacyReviewerGrantForShare :one
SELECT * FROM privacy_reviewer_grants WHERE user_id = sqlc.arg(user_id) AND revoked_at IS NULL FOR SHARE;

-- name: ListPrivacyDependantsForUpdate :many
SELECT * FROM users WHERE guardian_id = sqlc.arg(guardian_id) ORDER BY id FOR UPDATE;

-- name: CountPrivacyActiveAdministrators :one
SELECT count(*) FROM users account JOIN user_platform_roles grant_row ON grant_row.user_id = account.id
JOIN platform_roles role_row ON role_row.id = grant_row.role_id WHERE account.is_active AND role_row.code = 'ADMIN';

-- name: IsPrivacyAdministrator :one
SELECT EXISTS(SELECT 1 FROM user_platform_roles grant_row JOIN platform_roles role_row ON role_row.id = grant_row.role_id WHERE grant_row.user_id = sqlc.arg(user_id) AND role_row.code = 'ADMIN');

-- name: GetPrivacyRequest :one
SELECT * FROM data_erasure_requests WHERE id = sqlc.arg(id);

-- name: GetPrivacyRequestByRef :one
SELECT * FROM data_erasure_requests WHERE public_ref = sqlc.arg(public_ref);

-- name: GetPrivacyRequestForUpdate :one
SELECT * FROM data_erasure_requests WHERE public_ref = sqlc.arg(public_ref) FOR UPDATE;

-- name: GetPrivacyRequestByIdempotency :one
SELECT * FROM data_erasure_requests WHERE requester_user_id = sqlc.arg(requester_user_id) AND idempotency_key = sqlc.arg(idempotency_key);

-- name: GetPrivacyExecutionPlan :one
SELECT * FROM privacy_request_execution_plans WHERE request_id = sqlc.arg(request_id);

-- name: CreatePrivacyExecutionPlan :one
INSERT INTO privacy_request_execution_plans(request_id,policy_version,executor_version,schema_version,plan,plan_sha256,created_at)
VALUES(sqlc.arg(request_id),sqlc.arg(policy_version),sqlc.arg(executor_version),sqlc.arg(schema_version),sqlc.arg(plan),sqlc.arg(plan_sha256),sqlc.arg(created_at)) RETURNING *;

-- name: ListPrivacyRequestsForRequester :many
SELECT * FROM data_erasure_requests WHERE requester_user_id = sqlc.arg(requester_user_id) ORDER BY received_at DESC,id DESC LIMIT 100;

-- name: ListPrivacyReviewQueue :many
SELECT * FROM data_erasure_requests WHERE (sqlc.arg(status_filter)::text = '' OR status = sqlc.arg(status_filter))
AND requester_user_id <> sqlc.arg(reviewer_id) AND subject_user_id <> sqlc.arg(reviewer_id)
AND (sqlc.arg(deadline_filter)::text = '' OR (
    status NOT IN ('REFUSED', 'CANCELLED') AND (
        (sqlc.arg(deadline_filter)::text = 'overdue' AND COALESCE(extended_due_at,due_at) < sqlc.arg(now_at)::timestamptz)
        OR (sqlc.arg(deadline_filter)::text = 'soon' AND COALESCE(extended_due_at,due_at) >= sqlc.arg(now_at)::timestamptz
            AND COALESCE(extended_due_at,due_at) < sqlc.arg(soon_at)::timestamptz)
    )
))
ORDER BY CASE WHEN sqlc.arg(order_filter)::text = 'received' THEN received_at ELSE COALESCE(extended_due_at,due_at) END, received_at,id LIMIT 200;

-- name: CreatePrivacyRequest :one
INSERT INTO data_erasure_requests(public_ref,idempotency_key,subject_user_id,requester_user_id,subject_kind,scope_kind,categories,received_at,due_at,updated_at)
VALUES(sqlc.arg(public_ref),sqlc.arg(idempotency_key),sqlc.arg(subject_user_id),sqlc.arg(requester_user_id),sqlc.arg(subject_kind),sqlc.arg(scope_kind),sqlc.arg(categories),sqlc.arg(received_at),sqlc.arg(due_at),sqlc.arg(received_at)) RETURNING *;

-- name: UpdatePrivacyRequest :one
UPDATE data_erasure_requests SET status = sqlc.arg(status), version = version + 1,
 extended_due_at = sqlc.narg(extended_due_at), extension_reason_code = sqlc.narg(extension_reason_code),
 claimed_by = sqlc.narg(claimed_by), reviewed_at = sqlc.narg(reviewed_at),
 identity_verified_at = sqlc.narg(identity_verified_at), identity_method = sqlc.narg(identity_method), identity_verified_by = sqlc.narg(identity_verified_by),
 representation_relationship_updated_at = sqlc.narg(representation_relationship_updated_at), representation_verified_at = sqlc.narg(representation_verified_at), representation_method = sqlc.narg(representation_method), representation_verified_by = sqlc.narg(representation_verified_by), representation_guardian_id = sqlc.narg(representation_guardian_id), representation_conflict = sqlc.arg(representation_conflict),
 decision_code = sqlc.narg(decision_code), decision_explanation = sqlc.arg(decision_explanation), category_decisions = sqlc.arg(category_decisions), decided_by = sqlc.narg(decided_by), decided_at = sqlc.narg(decided_at),
 policy_version = sqlc.narg(policy_version), policy_snapshot = sqlc.narg(policy_snapshot), closed_at = sqlc.narg(closed_at), cancelled_at = sqlc.narg(cancelled_at), evidence_expires_at = sqlc.narg(evidence_expires_at), working_expires_at = sqlc.narg(working_expires_at), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND version = sqlc.arg(expected_version) RETURNING *;

-- name: AppendPrivacyRequestEvent :one
INSERT INTO data_erasure_request_events(request_id,actor_role,actor_ref,action,reason_code,from_status,to_status,version,occurred_at)
VALUES(sqlc.arg(request_id),sqlc.arg(actor_role),sqlc.arg(actor_ref),sqlc.arg(action),sqlc.arg(reason_code),sqlc.narg(from_status),sqlc.arg(to_status),sqlc.arg(version),sqlc.arg(occurred_at)) RETURNING *;

-- name: ListPrivacyRequestEvents :many
SELECT * FROM data_erasure_request_events WHERE request_id = sqlc.arg(request_id) ORDER BY version;

-- name: EnqueuePrivacyRequestEmail :one
INSERT INTO email_outbox(message_type,privacy_request_id,privacy_requester_id,privacy_event_key,sealed_payload,next_attempt_at,created_at,updated_at)
VALUES(sqlc.arg(message_type),sqlc.arg(privacy_request_id),sqlc.arg(privacy_requester_id),sqlc.arg(privacy_event_key),sqlc.arg(sealed_payload),sqlc.arg(created_at),sqlc.arg(created_at),sqlc.arg(created_at)) RETURNING id;

-- name: GrantPrivacyReviewer :one
INSERT INTO privacy_reviewer_grants(user_id,granted_by,granted_at) VALUES(sqlc.arg(user_id),sqlc.arg(granted_by),sqlc.arg(granted_at)) RETURNING *;

-- name: RevokePrivacyReviewer :one
UPDATE privacy_reviewer_grants SET revoked_by = sqlc.arg(revoked_by), revoked_at = sqlc.arg(revoked_at) WHERE id = sqlc.arg(id) AND revoked_at IS NULL RETURNING *;

-- name: AppendPrivacyReviewerGrantEvent :exec
INSERT INTO privacy_reviewer_grant_events(grant_id,actor_ref,action,occurred_at) VALUES(sqlc.arg(grant_id),sqlc.arg(actor_ref),sqlc.arg(action),sqlc.arg(occurred_at));

-- name: ListPrivacyDependantResolutions :many
SELECT * FROM privacy_request_dependant_resolutions WHERE request_id = sqlc.arg(request_id) ORDER BY dependant_id;

-- name: UpsertPrivacyDependantResolution :one
INSERT INTO privacy_request_dependant_resolutions(request_id,dependant_id,guardian_id_snapshot,relationship_updated_at,resolution_code,related_request_id,verified_by,verified_at,explanation)
VALUES(sqlc.arg(request_id),sqlc.arg(dependant_id),sqlc.arg(guardian_id_snapshot),sqlc.arg(relationship_updated_at),sqlc.arg(resolution_code),sqlc.narg(related_request_id),sqlc.arg(verified_by),sqlc.arg(verified_at),sqlc.arg(explanation))
ON CONFLICT(request_id,dependant_id) DO UPDATE SET guardian_id_snapshot=EXCLUDED.guardian_id_snapshot,relationship_updated_at=EXCLUDED.relationship_updated_at,resolution_code=EXCLUDED.resolution_code,related_request_id=EXCLUDED.related_request_id,verified_by=EXCLUDED.verified_by,verified_at=EXCLUDED.verified_at,explanation=EXCLUDED.explanation RETURNING *;

-- name: CreatePrivacyPolicy :one
INSERT INTO privacy_request_policies(version,category_catalogue,executor_version,plan_schema_version,account_closure_enabled,working_retention_days,response_months,extension_months,adopted_at,adopted_by,created_at)
VALUES(sqlc.arg(version),sqlc.arg(category_catalogue),sqlc.narg(executor_version),sqlc.narg(plan_schema_version),sqlc.arg(account_closure_enabled),sqlc.narg(working_retention_days),sqlc.arg(response_months),sqlc.arg(extension_months),sqlc.narg(adopted_at),sqlc.narg(adopted_by),sqlc.arg(created_at)) RETURNING *;

-- name: GetPrivacyActivationForUpdate :one
SELECT * FROM privacy_request_activation WHERE singleton = true FOR UPDATE;

-- name: SetPrivacyActivation :one
INSERT INTO privacy_request_activation(singleton,policy_version,enabled,fulfilment_ready,updated_by,updated_at)
VALUES(true,sqlc.arg(policy_version),sqlc.arg(enabled),sqlc.arg(fulfilment_ready),sqlc.arg(updated_by),sqlc.arg(updated_at))
ON CONFLICT(singleton) DO UPDATE SET policy_version=EXCLUDED.policy_version,enabled=EXCLUDED.enabled,fulfilment_ready=EXCLUDED.fulfilment_ready,updated_by=EXCLUDED.updated_by,updated_at=EXCLUDED.updated_at RETURNING *;

-- name: AppendPrivacyActivationEvent :exec
INSERT INTO privacy_request_activation_events(policy_version,actor_ref,enabled,fulfilment_ready,occurred_at)
VALUES(sqlc.arg(policy_version),sqlc.arg(actor_ref),sqlc.arg(enabled),sqlc.arg(fulfilment_ready),sqlc.arg(occurred_at));
