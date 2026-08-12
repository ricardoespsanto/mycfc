-- name: ListFeatureFlags :many
SELECT f.feature_key, f.mode::text AS mode, f.updated_at, f.updated_by_id,
       u.name AS updated_by_name
FROM feature_flags f
LEFT JOIN users u ON u.id = f.updated_by_id
ORDER BY f.feature_key;

-- name: UpdateFeatureFlag :execrows
INSERT INTO feature_flags (feature_key, mode, updated_by_id, updated_at)
VALUES (sqlc.arg(feature_key), sqlc.arg(mode)::feature_availability_mode,
        sqlc.arg(actor_user_id), clock_timestamp())
ON CONFLICT (feature_key) DO UPDATE
SET mode = EXCLUDED.mode,
    updated_by_id = EXCLUDED.updated_by_id,
    updated_at = EXCLUDED.updated_at
WHERE feature_flags.updated_at = sqlc.narg(expected_updated_at)::timestamptz;

-- name: ListFeatureFlagEvents :many
SELECT e.id, e.feature_key, e.previous_mode::text AS previous_mode,
       e.new_mode::text AS new_mode, e.actor_user_id, u.name AS actor_name,
       e.occurred_at
FROM feature_flag_events e
JOIN users u ON u.id = e.actor_user_id
ORDER BY e.occurred_at DESC, e.id DESC
LIMIT sqlc.arg(row_limit);
