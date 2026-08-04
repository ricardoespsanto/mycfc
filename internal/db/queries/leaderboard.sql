-- name: ListDistanceLeaderboard :many
WITH params AS (
    SELECT sqlc.arg(current_user_id)::uuid AS current_user_id
), totals AS (
    SELECT u.id AS user_id, u.name,
           sum(o.distance_metres)::bigint AS total_metres
    FROM training_session_outcomes o
    JOIN training_sessions s ON s.id = o.session_id
    JOIN users u ON u.id = o.user_id
    WHERE o.status = 'COMPLETED'
      AND o.distance_metres IS NOT NULL
      AND u.is_active = true
      AND u.leaderboard_visible = true
      AND EXISTS (
          SELECT 1
          FROM user_memberships membership
          WHERE membership.user_id = u.id
            AND membership.starts_on <= sqlc.arg(active_on)::date
            AND (membership.ends_on IS NULL OR membership.ends_on >= sqlc.arg(active_on)::date)
      )
      AND s.starts_at <= sqlc.arg(as_of)::timestamptz
      AND (sqlc.narg(period_start)::timestamptz IS NULL OR s.starts_at >= sqlc.narg(period_start)::timestamptz)
      AND (sqlc.narg(period_end)::timestamptz IS NULL OR s.starts_at < sqlc.narg(period_end)::timestamptz)
    GROUP BY u.id, u.name
), ranked AS (
    SELECT user_id, name, total_metres,
           rank() OVER (ORDER BY total_metres DESC)::bigint AS position
    FROM totals
)
SELECT ranked.user_id, ranked.name, ranked.total_metres, ranked.position
FROM ranked CROSS JOIN params
WHERE ranked.position <= 10 OR ranked.user_id = params.current_user_id
ORDER BY ranked.position, lower(ranked.name), ranked.user_id;
