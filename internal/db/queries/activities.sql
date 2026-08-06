-- name: UpsertActivityConnection :one
INSERT INTO activity_connections (
    user_id, provider, provider_user_id, status, credentials_ciphertext,
    credential_key_id, credential_expires_at, scopes
)
VALUES (
    sqlc.arg(user_id), sqlc.arg(provider), sqlc.arg(provider_user_id), 'ACTIVE',
    sqlc.arg(credentials_ciphertext), sqlc.arg(credential_key_id),
    sqlc.narg(credential_expires_at), sqlc.arg(scopes)
)
ON CONFLICT (user_id, provider) DO UPDATE SET
    provider_user_id = EXCLUDED.provider_user_id,
    status = 'ACTIVE',
    credentials_ciphertext = EXCLUDED.credentials_ciphertext,
    credential_key_id = EXCLUDED.credential_key_id,
    credential_expires_at = EXCLUDED.credential_expires_at,
    credential_version = activity_connections.credential_version + 1,
    scopes = EXCLUDED.scopes,
    last_error_code = NULL,
    last_error_message = NULL,
    last_error_at = NULL,
    disconnected_at = NULL,
    updated_at = now()
RETURNING *;

-- name: GetActivityConnectionForUser :one
SELECT * FROM activity_connections
WHERE user_id = sqlc.arg(user_id) AND provider = sqlc.arg(provider);

-- name: GetActivityConnectionByProviderIdentity :one
SELECT * FROM activity_connections
WHERE provider = sqlc.arg(provider) AND provider_user_id = sqlc.arg(provider_user_id);

-- name: UpdateActivityConnectionCredentials :one
UPDATE activity_connections SET
    credentials_ciphertext = sqlc.arg(credentials_ciphertext),
    credential_key_id = sqlc.arg(credential_key_id),
    credential_expires_at = sqlc.narg(credential_expires_at),
    credential_version = credential_version + 1,
    scopes = sqlc.arg(scopes),
    status = 'ACTIVE',
    last_error_code = NULL,
    last_error_message = NULL,
    last_error_at = NULL,
    updated_at = now()
WHERE id = sqlc.arg(id) AND credential_version = sqlc.arg(expected_credential_version) AND status <> 'DISCONNECTED'
RETURNING *;

-- name: RecordActivityConnectionSyncSuccess :one
UPDATE activity_connections SET
    sync_cursor = sqlc.narg(sync_cursor),
    last_successful_sync_at = sqlc.arg(succeeded_at),
    last_error_code = NULL,
    last_error_message = NULL,
    last_error_at = NULL,
    updated_at = now()
WHERE id = sqlc.arg(id) AND status <> 'DISCONNECTED'
RETURNING *;

-- name: RecordActivityConnectionError :one
UPDATE activity_connections SET
    status = CASE WHEN sqlc.arg(requires_reauthorization)::boolean THEN 'REAUTHORIZATION_REQUIRED' ELSE status END,
    last_error_code = sqlc.arg(error_code),
    last_error_message = sqlc.arg(error_message),
    last_error_at = sqlc.arg(failed_at),
    updated_at = now()
WHERE id = sqlc.arg(id) AND status <> 'DISCONNECTED'
RETURNING *;

-- name: DisconnectActivityConnection :one
WITH disconnected AS (
    UPDATE activity_connections SET
        status = 'DISCONNECTED',
        credentials_ciphertext = NULL,
        credential_key_id = NULL,
        credential_expires_at = NULL,
        credential_version = credential_version + 1,
        sync_cursor = NULL,
        disconnected_at = sqlc.arg(disconnected_at),
        updated_at = now()
    WHERE activity_connections.id = sqlc.arg(id) AND activity_connections.user_id = sqlc.arg(user_id)
    RETURNING *
), cancelled_jobs AS (
    UPDATE activity_sync_jobs SET
        status = 'CANCELLED', finished_at = sqlc.arg(disconnected_at), updated_at = now()
    WHERE connection_id IN (SELECT id FROM disconnected) AND status IN ('PENDING', 'RUNNING')
)
SELECT * FROM disconnected;

-- name: CreateActivitySyncJob :one
INSERT INTO activity_sync_jobs (idempotency_key, connection_id, reason, checkpoint)
VALUES (sqlc.arg(idempotency_key), sqlc.arg(connection_id), sqlc.arg(reason), sqlc.narg(checkpoint))
ON CONFLICT (idempotency_key) DO UPDATE SET idempotency_key = EXCLUDED.idempotency_key
WHERE activity_sync_jobs.connection_id = EXCLUDED.connection_id
  AND activity_sync_jobs.reason = EXCLUDED.reason
RETURNING *;

-- name: ClaimNextActivitySyncJob :one
WITH candidate AS (
    SELECT job.id FROM activity_sync_jobs job
    JOIN activity_connections connection ON connection.id = job.connection_id
    WHERE job.status = 'PENDING' AND connection.status <> 'DISCONNECTED'
    ORDER BY job.requested_at, job.id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE activity_sync_jobs job SET
    status = 'RUNNING', attempts = attempts + 1, started_at = sqlc.arg(started_at), updated_at = now()
FROM candidate
WHERE job.id = candidate.id
RETURNING job.*;

-- name: CompleteActivitySyncJob :one
UPDATE activity_sync_jobs SET
    status = 'SUCCEEDED', checkpoint = sqlc.narg(checkpoint),
    last_error_code = NULL, last_error_message = NULL,
    finished_at = sqlc.arg(finished_at), updated_at = now()
WHERE id = sqlc.arg(id) AND status = 'RUNNING'
RETURNING *;

-- name: FailActivitySyncJob :one
UPDATE activity_sync_jobs SET
    status = 'FAILED', checkpoint = sqlc.narg(checkpoint),
    last_error_code = sqlc.arg(error_code), last_error_message = sqlc.arg(error_message),
    finished_at = sqlc.arg(finished_at), updated_at = now()
WHERE id = sqlc.arg(id) AND status = 'RUNNING'
RETURNING *;

-- name: GetActivitySyncJob :one
SELECT * FROM activity_sync_jobs WHERE id = sqlc.arg(id);

-- name: UpsertSyncedActivity :one
INSERT INTO synced_activities (
    connection_id, user_id, provider, provider_activity_id, provider_updated_at,
    starts_at, ends_at, sport, normalized_sport, duration_seconds,
    moving_duration_seconds, distance_metres, average_heart_rate, maximum_heart_rate,
    provider_metrics, raw_summary, payload_sha256, normalization_version, deleted_at
)
VALUES (
    sqlc.arg(connection_id), sqlc.arg(user_id), sqlc.arg(provider), sqlc.arg(provider_activity_id), sqlc.narg(provider_updated_at),
    sqlc.arg(starts_at), sqlc.arg(ends_at), sqlc.arg(sport), sqlc.arg(normalized_sport), sqlc.arg(duration_seconds),
    sqlc.narg(moving_duration_seconds), sqlc.narg(distance_metres), sqlc.narg(average_heart_rate), sqlc.narg(maximum_heart_rate),
    sqlc.arg(provider_metrics), sqlc.arg(raw_summary), sqlc.arg(payload_sha256), sqlc.arg(normalization_version), sqlc.narg(deleted_at)
)
ON CONFLICT (provider, provider_activity_id) DO UPDATE SET
    provider_updated_at = EXCLUDED.provider_updated_at,
    starts_at = EXCLUDED.starts_at,
    ends_at = EXCLUDED.ends_at,
    sport = EXCLUDED.sport,
    normalized_sport = EXCLUDED.normalized_sport,
    duration_seconds = EXCLUDED.duration_seconds,
    moving_duration_seconds = EXCLUDED.moving_duration_seconds,
    distance_metres = EXCLUDED.distance_metres,
    average_heart_rate = EXCLUDED.average_heart_rate,
    maximum_heart_rate = EXCLUDED.maximum_heart_rate,
    provider_metrics = EXCLUDED.provider_metrics,
    raw_summary = EXCLUDED.raw_summary,
    payload_sha256 = EXCLUDED.payload_sha256,
    normalization_version = EXCLUDED.normalization_version,
    deleted_at = EXCLUDED.deleted_at,
    updated_at = now()
WHERE synced_activities.connection_id = EXCLUDED.connection_id
  AND synced_activities.user_id = EXCLUDED.user_id
RETURNING *;

-- name: GetSyncedActivityByProviderID :one
SELECT * FROM synced_activities
WHERE provider = sqlc.arg(provider) AND provider_activity_id = sqlc.arg(provider_activity_id);

-- name: ListRecentSyncedActivitiesForUser :many
SELECT * FROM synced_activities
WHERE user_id = sqlc.arg(user_id) AND deleted_at IS NULL
ORDER BY starts_at DESC, id DESC
LIMIT sqlc.arg(row_limit);

-- name: MarkSyncedActivityDeleted :one
UPDATE synced_activities SET deleted_at = sqlc.arg(deleted_at), updated_at = now()
WHERE provider = sqlc.arg(provider) AND provider_activity_id = sqlc.arg(provider_activity_id)
RETURNING *;

-- name: UpsertSuggestedActivityMatch :one
INSERT INTO training_session_activity_matches (session_id, activity_id, user_id, status, confidence, match_basis)
SELECT sqlc.arg(session_id), activity.id, activity.user_id, 'SUGGESTED', sqlc.arg(confidence), sqlc.arg(match_basis)
FROM synced_activities activity
WHERE activity.id = sqlc.arg(activity_id) AND activity.user_id = sqlc.arg(user_id) AND activity.deleted_at IS NULL
ON CONFLICT (session_id, activity_id, user_id) DO UPDATE SET
    confidence = EXCLUDED.confidence,
    match_basis = EXCLUDED.match_basis,
    updated_at = now()
WHERE training_session_activity_matches.status = 'SUGGESTED'
RETURNING *;

-- name: DecideActivityMatch :one
UPDATE training_session_activity_matches SET
    status = sqlc.arg(status),
    decided_by_user_id = sqlc.arg(actor_user_id),
    decided_at = sqlc.arg(decided_at),
    updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id) AND status = 'SUGGESTED'
  AND sqlc.arg(status) IN ('CONFIRMED', 'REJECTED')
RETURNING *;

-- name: ListSuggestedActivityMatchesForUser :many
SELECT match.* FROM training_session_activity_matches match
JOIN synced_activities activity ON activity.id = match.activity_id
WHERE match.user_id = sqlc.arg(user_id) AND match.status = 'SUGGESTED' AND activity.deleted_at IS NULL
ORDER BY match.confidence DESC, match.created_at DESC, match.id DESC
LIMIT sqlc.arg(row_limit);
