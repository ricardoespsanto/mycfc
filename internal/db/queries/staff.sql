-- name: ListActiveStaffGrantsForUser :many
SELECT capability::text AS capability, programme_id, team_id
FROM staff_grants
WHERE user_id = sqlc.arg(user_id) AND revoked_at IS NULL;

-- name: GrantStaffCapability :one
INSERT INTO staff_grants (user_id, capability, programme_id, team_id, granted_by_id)
VALUES (sqlc.arg(user_id), sqlc.arg(capability)::staff_capability, sqlc.narg(programme_id), sqlc.narg(team_id), sqlc.arg(granted_by_id))
RETURNING id, user_id, capability::text AS capability, programme_id, team_id, granted_by_id, granted_at, revoked_by_id, revoked_at, revoke_reason;

-- name: RevokeStaffGrant :execrows
UPDATE staff_grants
SET revoked_by_id = sqlc.arg(revoked_by_id), revoked_at = now(), revoke_reason = sqlc.arg(revoke_reason)
WHERE id = sqlc.arg(id) AND revoked_at IS NULL;

-- name: CanCoachManageEvent :one
SELECT EXISTS (
    SELECT 1
    FROM events e
    WHERE e.id = sqlc.arg(event_id)
      AND (EXISTS (SELECT 1 FROM event_audiences a WHERE a.event_id = e.id) OR EXISTS (SELECT 1 FROM event_team_audiences a WHERE a.event_id = e.id))
      AND NOT EXISTS (
          SELECT 1 FROM event_audiences a
          WHERE a.event_id = e.id
            AND NOT EXISTS (
                SELECT 1 FROM staff_grants g
                WHERE g.user_id = sqlc.arg(user_id) AND g.capability = 'COACH' AND g.revoked_at IS NULL AND g.programme_id = a.programme_id
            )
      )
      AND NOT EXISTS (
          SELECT 1 FROM event_team_audiences a
          JOIN teams t ON t.id = a.team_id
          WHERE a.event_id = e.id
            AND NOT EXISTS (
                SELECT 1 FROM staff_grants g
                WHERE g.user_id = sqlc.arg(user_id) AND g.capability = 'COACH' AND g.revoked_at IS NULL AND (g.team_id = a.team_id OR g.programme_id = t.programme_id)
            )
      )
);
