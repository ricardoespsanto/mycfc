-- name: CreateTrainingPlan :one
INSERT INTO training_plans (title, description, programme_id, team_id, created_by_id)
VALUES (sqlc.arg(title), sqlc.arg(description), sqlc.narg(programme_id), sqlc.narg(team_id), sqlc.arg(created_by_id))
RETURNING id, title, description, programme_id, team_id, created_by_id, created_at, updated_at;

-- name: CreateTrainingSession :one
INSERT INTO training_sessions (plan_id, title, description, starts_at, ends_at, modality_id, created_by_id)
VALUES (sqlc.arg(plan_id), sqlc.arg(title), sqlc.arg(description), sqlc.arg(starts_at), sqlc.arg(ends_at), sqlc.narg(modality_id), sqlc.arg(created_by_id))
RETURNING id, plan_id, title, description, starts_at, ends_at, modality_id, created_by_id, created_at, updated_at;

-- name: SaveTrainingSessionOutcome :execrows
INSERT INTO training_session_outcomes (session_id, user_id, status, replacement_session_id, replacement_reason, distance_metres)
SELECT sqlc.arg(session_id), sqlc.arg(user_id), sqlc.arg(status)::training_outcome_status,
       sqlc.narg(replacement_session_id), sqlc.narg(replacement_reason), sqlc.narg(distance_metres)
FROM training_sessions s
JOIN training_plans p ON p.id = s.plan_id
WHERE s.id = sqlc.arg(session_id)
  AND EXISTS (
      SELECT 1 FROM user_memberships m
      WHERE m.user_id = sqlc.arg(user_id) AND m.starts_on <= CURRENT_DATE AND (m.ends_on IS NULL OR m.ends_on >= CURRENT_DATE)
        AND (p.programme_id IS NULL OR p.programme_id = m.programme_id)
        AND (p.team_id IS NULL OR p.team_id = m.team_id)
  )
  AND (
      sqlc.arg(status)::training_outcome_status <> 'REPLACED'
      OR EXISTS (
          SELECT 1 FROM training_sessions replacement_session
          JOIN training_plans replacement_plan ON replacement_plan.id = replacement_session.plan_id
          JOIN user_memberships m ON m.user_id = sqlc.arg(user_id)
              AND m.starts_on <= CURRENT_DATE AND (m.ends_on IS NULL OR m.ends_on >= CURRENT_DATE)
          WHERE replacement_session.id = sqlc.narg(replacement_session_id)
            AND replacement_session.id <> s.id
            AND (replacement_plan.programme_id IS NULL OR replacement_plan.programme_id = m.programme_id)
            AND (replacement_plan.team_id IS NULL OR replacement_plan.team_id = m.team_id)
      )
  )
ON CONFLICT (session_id, user_id) DO UPDATE SET
    status = EXCLUDED.status,
    replacement_session_id = EXCLUDED.replacement_session_id,
    replacement_reason = EXCLUDED.replacement_reason,
    distance_metres = EXCLUDED.distance_metres,
    updated_at = now();

-- name: ListTrainingSessionsForAthlete :many
SELECT s.id, p.title AS plan_title, s.title, s.description, s.starts_at, s.ends_at, m.name_pt AS modality_name,
       COALESCE(o.status::text, ''::text) AS outcome_status, o.distance_metres
FROM training_sessions s
JOIN training_plans p ON p.id = s.plan_id
LEFT JOIN modalities m ON m.id = s.modality_id
LEFT JOIN training_session_outcomes o ON o.session_id = s.id AND o.user_id = sqlc.arg(user_id)
WHERE EXISTS (
    SELECT 1 FROM user_memberships membership
    WHERE membership.user_id = sqlc.arg(user_id) AND membership.starts_on <= CURRENT_DATE AND (membership.ends_on IS NULL OR membership.ends_on >= CURRENT_DATE)
      AND (p.programme_id IS NULL OR p.programme_id = membership.programme_id)
      AND (p.team_id IS NULL OR p.team_id = membership.team_id)
)
ORDER BY s.starts_at DESC, s.id DESC LIMIT sqlc.arg(row_limit);

-- name: ListUpcomingTrainingSessionsForDashboard :many
SELECT s.id, p.title AS plan_title, s.title, s.starts_at, s.ends_at, m.name_pt AS modality_name
FROM training_sessions s
JOIN training_plans p ON p.id = s.plan_id
LEFT JOIN modalities m ON m.id = s.modality_id
WHERE s.ends_at >= sqlc.arg(from_time)
  AND EXISTS (
      SELECT 1 FROM user_memberships membership
      JOIN users subject ON subject.id = membership.user_id
      WHERE (subject.id = sqlc.arg(user_id) OR subject.guardian_id = sqlc.arg(user_id))
        AND membership.starts_on <= CURRENT_DATE AND (membership.ends_on IS NULL OR membership.ends_on >= CURRENT_DATE)
        AND (p.programme_id IS NULL OR p.programme_id = membership.programme_id)
        AND (p.team_id IS NULL OR p.team_id = membership.team_id)
  )
ORDER BY s.starts_at, s.id
LIMIT sqlc.arg(row_limit);

-- name: UpdateOwnCompletedSessionDistance :execrows
UPDATE training_session_outcomes
SET distance_metres = sqlc.narg(distance_metres), updated_at = now()
WHERE session_id = sqlc.arg(session_id)
  AND user_id = sqlc.arg(user_id)
  AND status = 'COMPLETED';

-- name: ListTrainingPlansForCoach :many
SELECT p.id, p.title, p.description, p.programme_id, p.team_id, p.created_at
FROM training_plans p
WHERE EXISTS (SELECT 1 FROM staff_grants g WHERE g.user_id = sqlc.arg(user_id) AND g.capability = 'COACH' AND g.revoked_at IS NULL AND (g.programme_id = p.programme_id OR g.team_id = p.team_id))
ORDER BY p.created_at DESC, p.id DESC LIMIT sqlc.arg(row_limit);

-- name: ListTrainingPlansForAdmin :many
SELECT id, title, description, programme_id, team_id, created_at FROM training_plans
ORDER BY created_at DESC, id DESC LIMIT sqlc.arg(row_limit);

-- name: ListTrainingPlansForAuthoring :many
WITH scoped_plans AS (
    SELECT p.id, p.title, p.description, p.created_at
    FROM training_plans p
    WHERE sqlc.arg(is_admin)::boolean
       OR EXISTS (
           SELECT 1 FROM staff_grants g
           WHERE g.user_id = sqlc.arg(user_id) AND g.capability = 'COACH' AND g.revoked_at IS NULL
             AND (g.programme_id = p.programme_id OR g.team_id = p.team_id)
       )
    ORDER BY p.created_at DESC, p.id DESC
    LIMIT sqlc.arg(plan_limit)
    OFFSET sqlc.arg(plan_offset)
)
SELECT p.id AS plan_id, p.title AS plan_title, p.description AS plan_description,
       s.id AS session_id, s.title AS session_title, s.description AS session_description,
       s.starts_at, s.ends_at, m.name_pt AS modality_name
FROM scoped_plans p
LEFT JOIN training_sessions s ON s.plan_id = p.id
LEFT JOIN modalities m ON m.id = s.modality_id
ORDER BY p.created_at DESC, p.id DESC, s.starts_at ASC, s.id ASC;

-- name: CanCoachManageTrainingPlan :one
SELECT EXISTS (SELECT 1 FROM training_plans p WHERE p.id = sqlc.arg(plan_id) AND EXISTS (SELECT 1 FROM staff_grants g WHERE g.user_id = sqlc.arg(user_id) AND g.capability = 'COACH' AND g.revoked_at IS NULL AND (g.programme_id = p.programme_id OR g.team_id = p.team_id)));

-- name: CreateCompetitionDocument :one
INSERT INTO competition_documents (title, url, source, reviewed_on, event_id, modality_id, programme_id, team_id, author_id)
VALUES (sqlc.arg(title), sqlc.arg(url), sqlc.arg(source), sqlc.arg(reviewed_on), sqlc.narg(event_id), sqlc.narg(modality_id), sqlc.narg(programme_id), sqlc.narg(team_id), sqlc.arg(author_id))
RETURNING id, title, url, source, reviewed_on, event_id, modality_id, programme_id, team_id, author_id, published_at, created_at;

-- name: ListCompetitionDocumentsForAthlete :many
SELECT DISTINCT d.id, d.title, d.url, d.source, d.reviewed_on, d.published_at, e.title AS event_title, mo.name_pt AS modality_name
FROM competition_documents d
LEFT JOIN events e ON e.id = d.event_id
LEFT JOIN modalities mo ON mo.id = d.modality_id
WHERE (
    d.event_id IS NOT NULL AND EXISTS (
        SELECT 1 FROM user_memberships m JOIN users subject ON subject.id = m.user_id
        WHERE (subject.id = sqlc.arg(user_id) OR subject.guardian_id = sqlc.arg(user_id)) AND m.starts_on <= CURRENT_DATE AND (m.ends_on IS NULL OR m.ends_on >= CURRENT_DATE)
          AND (NOT EXISTS (SELECT 1 FROM event_audiences a WHERE a.event_id = d.event_id)
               OR EXISTS (SELECT 1 FROM event_audiences a WHERE a.event_id = d.event_id AND a.programme_id = m.programme_id)
               OR EXISTS (SELECT 1 FROM event_team_audiences a WHERE a.event_id = d.event_id AND a.team_id = m.team_id))
    )
) OR (
    d.modality_id IS NOT NULL AND EXISTS (
        SELECT 1 FROM user_memberships m JOIN users subject ON subject.id = m.user_id JOIN membership_modalities mm ON mm.membership_id = m.id
        WHERE (subject.id = sqlc.arg(user_id) OR subject.guardian_id = sqlc.arg(user_id)) AND m.starts_on <= CURRENT_DATE AND (m.ends_on IS NULL OR m.ends_on >= CURRENT_DATE)
          AND mm.modality_id = d.modality_id AND (d.programme_id IS NULL OR d.programme_id = m.programme_id) AND (d.team_id IS NULL OR d.team_id = m.team_id)
    )
)
ORDER BY d.published_at DESC, d.id DESC LIMIT sqlc.arg(row_limit);

-- name: ListCompetitionDocumentsForEvent :many
SELECT id, title, url, source, reviewed_on, published_at
FROM competition_documents
WHERE event_id = sqlc.arg(event_id)
ORDER BY published_at DESC, id DESC;
