-- name: GetPrivacyActivation :one
SELECT * FROM privacy_request_activation WHERE singleton = true;

-- name: PrivacyActivationReady :one
SELECT privacy_activation_ready(sqlc.arg(policy_version));

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
JOIN platform_roles role_row ON role_row.id = grant_row.role_id
WHERE account.is_active AND NOT account.is_dependent
AND account.date_of_birth<=(((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-INTERVAL '18 years')::date)
AND account.email IS NOT NULL AND account.password_hash IS NOT NULL AND role_row.code = 'ADMIN';

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

-- name: GetPrivacyExecutorGrantForShare :one
SELECT * FROM privacy_executor_grants WHERE user_id=sqlc.arg(user_id) AND revoked_at IS NULL FOR SHARE;

-- name: GrantPrivacyExecutor :one
INSERT INTO privacy_executor_grants(user_id,granted_by,granted_at)
VALUES(sqlc.arg(user_id),sqlc.arg(granted_by),sqlc.arg(granted_at)) RETURNING *;

-- name: RevokePrivacyExecutor :one
UPDATE privacy_executor_grants SET revoked_by=sqlc.arg(revoked_by),revoked_at=sqlc.arg(revoked_at)
WHERE id=sqlc.arg(id) AND revoked_at IS NULL RETURNING *;

-- name: AppendPrivacyExecutorGrantEvent :exec
INSERT INTO privacy_executor_grant_events(grant_id,actor_ref,action,occurred_at)
VALUES(sqlc.arg(grant_id),sqlc.arg(actor_ref),sqlc.arg(action),sqlc.arg(occurred_at));

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

-- name: LockPrivacyActiveAdministratorSet :exec
SELECT pg_advisory_xact_lock(hashtextextended('mycfc-active-admin-set/v1',0));

-- name: CountActiveUnindexedPrivacySessions :one
SELECT count(*) FROM sessions WHERE NOT subject_indexed AND expiry>clock_timestamp();

-- name: MarkPrivacySessionIndexed :execrows
UPDATE sessions SET user_id=sqlc.narg(user_id),subject_indexed=true WHERE token=sqlc.arg(token);

-- name: DeletePrivacySessionsByUser :execrows
DELETE FROM sessions WHERE subject_indexed AND user_id=sqlc.arg(user_id);

-- name: ExpireLegacyPrivacySessionsBy :execrows
WITH authority_clock AS MATERIALIZED (SELECT clock_timestamp() AS occurred_at)
UPDATE sessions SET expiry=authority_clock.occurred_at
FROM authority_clock WHERE NOT sessions.subject_indexed AND sessions.expiry>authority_clock.occurred_at;

-- name: CreatePrivacyErasureExecution :one
INSERT INTO privacy_erasure_executions(request_id,plan_sha256,executor_version,schema_version,request_version_at_start,started_by_ref,accepted_at,updated_at)
VALUES(sqlc.arg(request_id),sqlc.arg(plan_sha256),sqlc.arg(executor_version),sqlc.arg(schema_version),sqlc.arg(request_version_at_start),sqlc.arg(started_by_ref),sqlc.arg(accepted_at),sqlc.arg(accepted_at))
RETURNING *;

-- name: GetPrivacyErasureExecutionByRequest :one
SELECT * FROM privacy_erasure_executions WHERE request_id=sqlc.arg(request_id);

-- name: GetPrivacyErasureExecutionForUpdate :one
SELECT * FROM privacy_erasure_executions WHERE id=sqlc.arg(id) FOR UPDATE;

-- name: GetPrivacyErasureExecution :one
SELECT * FROM privacy_erasure_executions WHERE id=sqlc.arg(id);

-- name: CreatePrivacyErasureAccessRevocation :one
INSERT INTO privacy_erasure_access_revocations(execution_id,grant_kind,capability_code,revoked_count,actor_ref,occurred_at)
VALUES(sqlc.arg(execution_id),sqlc.arg(grant_kind),sqlc.arg(capability_code),sqlc.arg(revoked_count),sqlc.arg(actor_ref),sqlc.arg(occurred_at))
RETURNING *;

-- name: ListPrivacyErasureAccessRevocations :many
SELECT * FROM privacy_erasure_access_revocations WHERE execution_id=sqlc.arg(execution_id) ORDER BY grant_kind,capability_code;

-- name: CreatePrivacyErasureCategoryJob :one
INSERT INTO privacy_erasure_category_jobs(execution_id,plan_entry_position,entry_sha256,category_key,purpose_code,next_attempt_at,created_at,updated_at)
VALUES(sqlc.arg(execution_id),sqlc.arg(plan_entry_position),sqlc.arg(entry_sha256),sqlc.arg(category_key),sqlc.arg(purpose_code),sqlc.arg(next_attempt_at),sqlc.arg(created_at),sqlc.arg(created_at))
RETURNING *;

-- name: CreatePrivacyErasureJobCheckpoint :one
INSERT INTO privacy_erasure_job_checkpoints(job_id,operation_position,operation_code,action_version,created_at)
VALUES(sqlc.arg(job_id),sqlc.arg(operation_position),sqlc.arg(operation_code),sqlc.arg(action_version),sqlc.arg(created_at))
RETURNING *;

-- name: ListPrivacyErasureCategoryJobs :many
SELECT * FROM privacy_erasure_category_jobs WHERE execution_id=sqlc.arg(execution_id) ORDER BY plan_entry_position;

-- name: ListPrivacyErasureJobCheckpoints :many
SELECT * FROM privacy_erasure_job_checkpoints WHERE job_id=sqlc.arg(job_id) ORDER BY operation_position;

-- name: GetPrivacyErasureWorkSetCounts :one
SELECT
 (SELECT count(*) FROM privacy_erasure_category_jobs counted_job WHERE counted_job.execution_id=sqlc.arg(execution_ref))::bigint AS job_count,
 (SELECT count(*) FROM privacy_erasure_job_checkpoints checkpoint JOIN privacy_erasure_category_jobs job ON job.id=checkpoint.job_id WHERE job.execution_id=sqlc.arg(execution_ref))::bigint AS checkpoint_count;

-- name: MaterializePrivacyObjectTarget :one
SELECT privacy_execution_materialize_object_target(
 sqlc.arg(target_id),sqlc.arg(execution_id),sqlc.arg(job_id),sqlc.arg(checkpoint_id),sqlc.arg(plan_entry_sha256),sqlc.arg(category_key),
 sqlc.arg(source_kind),sqlc.arg(source_ref),sqlc.arg(upload_intent_id),sqlc.arg(object_key),
 sqlc.arg(envelope_version),sqlc.arg(algorithm),sqlc.arg(encryption_key_id),sqlc.arg(encapsulation),sqlc.arg(nonce),sqlc.arg(ciphertext),
 sqlc.arg(digest_key_id),sqlc.arg(locator_digest)
) AS target_id;

-- name: CompletePrivacyObjectCapture :one
SELECT privacy_execution_complete_object_capture(sqlc.arg(execution_id),sqlc.arg(category_key)) AS target_count;

-- name: MaterializePrivacyProviderTarget :one
SELECT privacy_execution_materialize_provider_target(
 sqlc.arg(target_id),sqlc.arg(connection_id),sqlc.arg(execution_id),sqlc.arg(job_id),sqlc.arg(checkpoint_id),sqlc.arg(plan_entry_sha256),sqlc.arg(category_key),
 sqlc.arg(service_code),sqlc.arg(provider_role),sqlc.arg(provider_contract_version),sqlc.arg(target_version),sqlc.arg(local_state),
 sqlc.arg(registry_evidence_key_id),sqlc.arg(registry_evidence_digest),sqlc.arg(target_envelope_version),sqlc.arg(target_algorithm),
 sqlc.arg(target_encryption_key_id),sqlc.arg(target_encapsulation),sqlc.arg(target_nonce),sqlc.arg(target_ciphertext),
 sqlc.narg(credential_envelope_version),sqlc.narg(credential_algorithm),sqlc.narg(credential_encryption_key_id),sqlc.narg(credential_encapsulation),
 sqlc.narg(credential_nonce),sqlc.narg(credential_ciphertext),sqlc.narg(credential_commitment_key_id),sqlc.narg(credential_source_commitment),
 sqlc.arg(digest_key_id),sqlc.arg(target_digest)
) AS target_id;

-- name: CompletePrivacyProviderCapture :one
SELECT privacy_execution_complete_provider_capture(sqlc.arg(execution_id),sqlc.arg(category_key)) AS target_count;

-- name: RecordPrivacyWorkerObjectEvidence :one
SELECT id FROM (SELECT privacy_worker_record_object_evidence(
 sqlc.arg(target_id),sqlc.arg(job_id),sqlc.arg(lease_id),sqlc.arg(attempt_id),sqlc.arg(lease_epoch),sqlc.arg(worker_ref),
 sqlc.arg(deleted_versions),sqlc.arg(deleted_markers),sqlc.arg(list_calls),sqlc.arg(stable_checks),
 sqlc.arg(transcript_key_id),sqlc.arg(transcript_digest)
) AS id) recorded WHERE id IS NOT NULL;

-- name: CompletePrivacyWorkerObjectCheckpoint :one
SELECT id FROM (SELECT privacy_worker_complete_object_checkpoint(
 sqlc.arg(job_id),sqlc.arg(lease_id),sqlc.arg(attempt_id),sqlc.arg(lease_epoch),sqlc.arg(worker_ref)
) AS id) completed WHERE id IS NOT NULL;

-- name: RecordPrivacyWorkerProviderEvidence :one
SELECT id FROM (SELECT privacy_worker_record_provider_evidence(
 sqlc.arg(target_id),sqlc.arg(job_id),sqlc.arg(lease_id),sqlc.arg(attempt_id),sqlc.arg(lease_epoch),sqlc.arg(worker_ref),
 sqlc.arg(outcome_code),sqlc.arg(adapter_attempts),sqlc.arg(evidence_code),sqlc.narg(recipient_role),sqlc.narg(channel_code),
 sqlc.narg(notification_code),sqlc.narg(reason_code),sqlc.narg(guidance_code),sqlc.arg(transcript_key_id),sqlc.arg(transcript_digest)
) AS id) recorded WHERE id IS NOT NULL;

-- name: CompletePrivacyWorkerProviderCheckpoint :one
SELECT id FROM (SELECT privacy_worker_complete_provider_checkpoint(
 sqlc.arg(job_id),sqlc.arg(lease_id),sqlc.arg(attempt_id),sqlc.arg(lease_epoch),sqlc.arg(worker_ref)
) AS id) completed WHERE id IS NOT NULL;

-- name: GetPrivacyErasureCategoryJob :one
SELECT * FROM privacy_erasure_category_jobs WHERE id=sqlc.arg(id);

-- name: GetPrivacyErasureJobLease :one
SELECT * FROM privacy_erasure_job_leases WHERE id=sqlc.arg(id);

-- name: GetPrivacyErasureJobCheckpoint :one
SELECT * FROM privacy_erasure_job_checkpoints WHERE id=sqlc.arg(id);

-- name: PreparePrivacyRestoreTombstone :one
SELECT prepared.execution_id::uuid AS execution_id,
 prepared.request_id::uuid AS request_id,
 prepared.request_ref::uuid AS request_ref,
 prepared.subject_user_id::uuid AS subject_user_id,
 prepared.plan_sha256::bytea AS plan_sha256,
 prepared.workset_sha256::bytea AS workset_sha256,
 prepared.execution_started_at::timestamptz AS execution_started_at,
 prepared.replay_operations::text[] AS replay_operations
FROM privacy_tombstone_prepare_v2(
 sqlc.arg(job_id),sqlc.arg(lease_id),sqlc.arg(attempt_id),sqlc.arg(lease_epoch),sqlc.arg(worker_ref)
) AS prepared;

-- name: ConfirmPrivacyRestoreTombstone :one
SELECT privacy_tombstone_confirm_v2(
 sqlc.arg(job_id),sqlc.arg(lease_id),sqlc.arg(attempt_id),sqlc.arg(lease_epoch),sqlc.arg(worker_ref),
 sqlc.arg(ledger_version),sqlc.arg(encryption_key_id),sqlc.arg(locator_key_id),sqlc.arg(locator_digest),
 sqlc.arg(object_version_id),sqlc.arg(ciphertext_sha256),sqlc.arg(size_bytes),sqlc.arg(written_at),sqlc.arg(verified_at)
)::uuid;

-- name: RunPrivacyRetention :one
SELECT retained.run_id::uuid AS run_id,
 retained.sessions_deleted::integer AS sessions_deleted,
 retained.tokens_deleted::integer AS tokens_deleted,
 retained.outbox_stopped::integer AS outbox_stopped,
 retained.outbox_payloads_deleted::integer AS outbox_payloads_deleted,
 retained.outbox_evidence_deleted::integer AS outbox_evidence_deleted,
 retained.consent_network_scrubbed::integer AS consent_network_scrubbed,
 retained.consent_evidence_deleted::integer AS consent_evidence_deleted,
 retained.audit_events_pseudonymized::integer AS audit_events_pseudonymized,
 retained.repair_attachments_queued::integer AS repair_attachments_queued,
 retained.event_responses_deleted::integer AS event_responses_deleted,
 retained.announcement_deliveries_deleted::integer AS announcement_deliveries_deleted,
 retained.suggestions_deleted::integer AS suggestions_deleted,
 retained.privacy_working_scrubbed::integer AS privacy_working_scrubbed,
 retained.auth_limits_deleted::integer AS auth_limits_deleted
FROM privacy_retention_run(sqlc.arg(worker_ref),sqlc.arg(batch_limit)) AS retained;

-- name: GetPrivacyRetentionStatus :one
SELECT status.due_count::bigint AS due_count,
 status.oldest_due_age_seconds::bigint AS oldest_due_age_seconds,
 status.repair_due_count::bigint AS repair_due_count,
 status.repair_overdue_count::bigint AS repair_overdue_count,
 status.repair_terminal_failures::bigint AS repair_terminal_failures,
 status.repair_legacy_due_count::bigint AS repair_legacy_due_count,
 status.last_run_age_seconds::bigint AS last_run_age_seconds
FROM privacy_retention_status() AS status;

-- name: PreparePrivacyTombstoneClosure :one
SELECT prepared.execution_id::uuid AS execution_id,
 prepared.request_id::uuid AS request_id,
 prepared.request_ref::uuid AS request_ref,
 prepared.subject_user_id::uuid AS subject_user_id,
 prepared.plan_sha256::bytea AS plan_sha256,
 prepared.workset_sha256::bytea AS workset_sha256,
 prepared.execution_started_at::timestamptz AS execution_started_at,
 prepared.closed_at::timestamptz AS closed_at,
 prepared.evidence_expires_at::timestamptz AS evidence_expires_at,
 prepared.replay_operations::text[] AS replay_operations
FROM privacy_tombstone_prepare_closure_v2(sqlc.arg(execution_id),sqlc.arg(worker_ref)) AS prepared;

-- name: PreparePrivacyTombstoneClosureV3 :one
SELECT prepared.execution_id::uuid AS execution_id,prepared.request_id::uuid AS request_id,prepared.request_ref::uuid AS request_ref,
 prepared.subject_user_id::uuid AS subject_user_id,prepared.plan_sha256::bytea AS plan_sha256,
 prepared.workset_sha256::bytea AS workset_sha256,prepared.execution_started_at::timestamptz AS execution_started_at,
 prepared.closed_at::timestamptz AS closed_at,prepared.evidence_expires_at::timestamptz AS evidence_expires_at,
 prepared.erasure_effective_at::timestamptz AS erasure_effective_at,prepared.replay_operations::text[] AS replay_operations
FROM privacy_tombstone_prepare_closure_v3(sqlc.arg(execution_id),sqlc.arg(worker_ref)) AS prepared;

-- name: ConfirmPrivacyTombstoneClosure :one
SELECT privacy_tombstone_confirm_closure_v2(
 sqlc.arg(execution_id),sqlc.arg(worker_ref),sqlc.arg(ledger_version),sqlc.arg(encryption_key_id),
 sqlc.arg(locator_key_id),sqlc.arg(locator_digest),sqlc.arg(object_version_id),sqlc.arg(ciphertext_sha256),
 sqlc.arg(size_bytes),sqlc.arg(written_at),sqlc.arg(verified_at)
)::uuid;

-- name: ConfirmPrivacyTombstoneClosureV3 :one
SELECT privacy_tombstone_confirm_closure_v3(
 sqlc.arg(execution_id),sqlc.arg(worker_ref),sqlc.arg(ledger_version),sqlc.arg(encryption_key_id),sqlc.arg(locator_key_id),
 sqlc.arg(locator_digest),sqlc.arg(object_version_id),sqlc.arg(ciphertext_sha256),sqlc.arg(size_bytes),sqlc.arg(written_at),sqlc.arg(verified_at)
)::uuid;

-- name: ImportAuthenticatedPrivacyRestoreTombstoneV2 :one
SELECT privacy_restore_import_authenticated_v2(
 sqlc.arg(worker_ref),sqlc.arg(kind),sqlc.arg(record_version),sqlc.arg(envelope_version),sqlc.arg(encryption_key_id),
 sqlc.arg(locator_key_id),sqlc.arg(locator_digest),sqlc.arg(ciphertext_sha256),sqlc.arg(object_version_id),
 sqlc.arg(written_at),sqlc.arg(verified_at),sqlc.narg(retain_until),sqlc.arg(source_execution_id),sqlc.arg(source_request_id),
 sqlc.arg(source_request_ref),sqlc.arg(subject_user_id),sqlc.arg(plan_sha256),sqlc.arg(workset_sha256),sqlc.arg(execution_started_at),
 sqlc.arg(replay_version),sqlc.arg(action_version),sqlc.arg(operations)::text[],sqlc.arg(prescription_sha256),sqlc.arg(record_sha256)
)::uuid;

-- name: ImportAuthenticatedPrivacyRestoreTombstoneV2Hardened :one
SELECT privacy_restore_import_authenticated_v2_hardened(
 sqlc.arg(worker_ref),sqlc.arg(kind),sqlc.arg(record_version),sqlc.arg(envelope_version),sqlc.arg(encryption_key_id),
 sqlc.arg(locator_key_id),sqlc.arg(locator_digest),sqlc.arg(ciphertext_sha256),sqlc.arg(object_version_id),
 sqlc.arg(written_at),sqlc.arg(verified_at),sqlc.narg(retain_until),sqlc.arg(source_execution_id),sqlc.arg(source_request_id),
 sqlc.arg(source_request_ref),sqlc.arg(subject_user_id),sqlc.arg(plan_sha256),sqlc.arg(workset_sha256),sqlc.arg(execution_started_at),
 sqlc.arg(erasure_effective_at),sqlc.narg(closure_version),sqlc.narg(synthetic_fixture),sqlc.arg(replay_version),sqlc.arg(action_version),sqlc.arg(operations)::text[],
 sqlc.arg(prescription_sha256),sqlc.arg(record_sha256)
)::uuid;

-- name: BeginPrivacyRestoreReplay :one
SELECT privacy_restore_begin_replay(sqlc.arg(import_id),sqlc.arg(worker_ref))::uuid;

-- name: BeginPrivacyRestoreReplayHardened :one
SELECT begun.run_id::uuid, COALESCE(begun.outcome_code,'')::text AS outcome_code
FROM privacy_restore_begin_replay_hardened(sqlc.arg(import_id),sqlc.arg(worker_ref)) AS begun;

-- name: CreatePrivacyRestoreSyntheticFixture :one
SELECT fixture.source_execution_id::uuid,fixture.source_request_id::uuid,fixture.source_request_ref::uuid,
 fixture.subject_user_id::uuid,fixture.plan_sha256::bytea,fixture.workset_sha256::bytea,
 fixture.erasure_effective_at::timestamptz,fixture.operations::text[]
FROM privacy_restore_create_synthetic_fixture(sqlc.arg(worker_ref)) AS fixture;

-- name: PrivacyRestoreReplayAlreadyApplied :one
SELECT EXISTS(
 SELECT 1 FROM privacy_protected.restore_ledger_imports imported
 JOIN privacy_protected.restore_replay_runs run ON run.import_id=imported.id
 WHERE imported.locator_key_id=sqlc.arg(locator_key_id) AND imported.locator_digest=sqlc.arg(locator_digest)
  AND run.status='SUCCEEDED'
)::boolean;

-- name: ExecutePrivacyRestoreReplayCheckpoint :one
SELECT privacy_restore_execute_checkpoint(
 sqlc.arg(run_id),sqlc.arg(worker_ref),sqlc.arg(operation_position),sqlc.arg(operation_code),sqlc.arg(action_version),sqlc.arg(prescription_sha256)
)::uuid;

-- name: RecordPrivacyRestoreReplayInventoryAttestation :one
SELECT privacy_restore_record_inventory_attestation(
 sqlc.arg(input_source),sqlc.arg(inventory_sha256),sqlc.arg(schema_migration_digest),sqlc.arg(policy_version),sqlc.arg(executor_version),
 sqlc.arg(plan_schema_version),sqlc.arg(image_digest),sqlc.arg(run_ids)::uuid[],sqlc.arg(object_count),sqlc.arg(imported_count),
 sqlc.arg(replayed_count),sqlc.arg(already_applied_count),sqlc.arg(absence_verified_count),sqlc.arg(synthetic_replayed_count)
 ,sqlc.arg(closure_v3_count),sqlc.arg(intent_only_count),sqlc.arg(legacy_closure_v2_count),sqlc.arg(erasure_effective_at_verified_count)
)::bytea;

-- name: AuthorizePrivacyErasureJobLease :one
SELECT lease.*
FROM privacy_erasure_job_leases lease
JOIN privacy_erasure_category_jobs job ON lease.job_id=job.id AND lease.epoch=job.lease_epoch
JOIN privacy_erasure_job_attempts attempt ON attempt.lease_id=lease.id
WHERE job.id=sqlc.arg(job_id) AND lease.id=sqlc.arg(lease_id) AND attempt.id=sqlc.arg(attempt_id)
AND lease.epoch=sqlc.arg(lease_epoch) AND lease.worker_ref=sqlc.arg(worker_ref)
AND job.status='LEASED' AND lease.released_at IS NULL AND attempt.finished_at IS NULL
AND lease.expires_at>clock_timestamp()
FOR UPDATE OF job,lease,attempt;

-- name: GetPrivacyRequestExecutionLifecycle :one
SELECT id,status,version FROM data_erasure_requests WHERE id=sqlc.arg(id);

-- name: CallPrivacyWorkerSync :one
SELECT privacy_worker_sync(sqlc.arg(job_id),sqlc.arg(lease_id),sqlc.arg(attempt_id),sqlc.arg(lease_epoch),sqlc.arg(worker_ref))::uuid;

-- name: CallPrivacyWorkerExecuteCheckpoint :one
SELECT id FROM (
 SELECT privacy_worker_execute_checkpoint(sqlc.arg(job_id),sqlc.arg(lease_id),sqlc.arg(attempt_id),sqlc.arg(lease_epoch),sqlc.arg(worker_ref),sqlc.arg(operation_code),sqlc.arg(action_version))::uuid AS id
) result WHERE id IS NOT NULL;

-- name: TransitionPrivacyRequestExecutionStatus :one
UPDATE data_erasure_requests SET status=sqlc.arg(to_status),version=version+1,
closed_at=sqlc.narg(closed_at),evidence_expires_at=sqlc.narg(evidence_expires_at),working_expires_at=sqlc.narg(working_expires_at),updated_at=sqlc.arg(updated_at)
WHERE id=sqlc.arg(id) AND version=sqlc.arg(expected_version) AND status=ANY(sqlc.arg(from_statuses)::text[])
RETURNING *;

-- name: DisablePrivacyAccountForExecution :one
UPDATE users SET is_active=false,credential_version=credential_version+1,updated_at=sqlc.arg(disabled_at)
WHERE id=sqlc.arg(user_id) AND is_active RETURNING *;

-- name: RevokePrivacyPlatformRolesForExecution :many
WITH removed AS (
 DELETE FROM user_platform_roles assignment WHERE assignment.user_id=sqlc.arg(user_id) RETURNING assignment.role_id
)
SELECT role.code FROM removed JOIN platform_roles role ON role.id=removed.role_id ORDER BY role.code;

-- name: RevokePrivacyStaffGrantsForExecution :many
UPDATE staff_grants SET revoked_by_id=sqlc.arg(revoked_by_id),revoked_at=sqlc.arg(revoked_at),revoke_reason='PRIVACY_ACCOUNT_CLOSURE'
WHERE user_id=sqlc.arg(user_id) AND revoked_at IS NULL RETURNING *;

-- name: RevokePrivacyReviewerGrantsForExecution :many
UPDATE privacy_reviewer_grants SET revoked_by=sqlc.arg(revoked_by),revoked_at=sqlc.arg(revoked_at)
WHERE user_id=sqlc.arg(user_id) AND revoked_at IS NULL RETURNING *;

-- name: RevokePrivacyExecutorGrantsForExecution :many
UPDATE privacy_executor_grants SET revoked_by=sqlc.arg(revoked_by),revoked_at=sqlc.arg(revoked_at)
WHERE user_id=sqlc.arg(user_id) AND revoked_at IS NULL RETURNING *;

-- name: InvalidatePrivacyAccountTokens :one
WITH verification AS (
 UPDATE email_verification_tokens token SET consumed_at=GREATEST(sqlc.arg(invalidated_at),token.created_at)
 WHERE token.user_id=sqlc.arg(subject_user_id) AND token.consumed_at IS NULL RETURNING token.id
), reset AS (
 UPDATE password_reset_tokens token SET consumed_at=GREATEST(sqlc.arg(invalidated_at),token.created_at)
 WHERE token.user_id=sqlc.arg(subject_user_id) AND token.consumed_at IS NULL RETURNING token.id
), cancelled AS (
 UPDATE email_outbox SET status='CANCELLED',claimed_at=NULL,updated_at=sqlc.arg(invalidated_at)
 WHERE status IN ('PENDING','SENDING') AND (verification_token_id IN (SELECT id FROM verification) OR password_reset_token_id IN (SELECT id FROM reset)) RETURNING id
)
SELECT (SELECT count(*) FROM verification)::bigint AS verification_tokens,
       (SELECT count(*) FROM reset)::bigint AS reset_tokens,
       (SELECT count(*) FROM cancelled)::bigint AS cancelled_deliveries;
