-- name: CreateTrainingPlan :one
INSERT INTO training_plans (title, description, programme_id, team_id, created_by_id)
VALUES (sqlc.arg(title), sqlc.arg(description), sqlc.narg(programme_id), sqlc.narg(team_id), sqlc.arg(created_by_id))
RETURNING id, title, description, programme_id, team_id, created_by_id, created_at, updated_at;

-- name: CreateTrainingSession :one
INSERT INTO training_sessions (plan_id, title, description, starts_at, ends_at, modality_id, created_by_id)
VALUES (sqlc.arg(plan_id), sqlc.arg(title), sqlc.arg(description), sqlc.arg(starts_at), sqlc.arg(ends_at), sqlc.narg(modality_id), sqlc.arg(created_by_id))
RETURNING id, plan_id, title, description, starts_at, ends_at, modality_id, created_by_id, created_at, updated_at;

-- name: AssignTrainingSession :execrows
INSERT INTO training_session_assignments (session_id, user_id, assigned_by_id)
SELECT sqlc.arg(session_id), sqlc.arg(user_id), sqlc.arg(assigned_by_id)
WHERE EXISTS (
    SELECT 1 FROM training_sessions s JOIN training_plans p ON p.id = s.plan_id
    JOIN user_memberships m ON m.user_id = sqlc.arg(user_id) AND m.starts_on <= CURRENT_DATE AND (m.ends_on IS NULL OR m.ends_on >= CURRENT_DATE)
    WHERE s.id = sqlc.arg(session_id) AND (p.programme_id IS NULL OR p.programme_id = m.programme_id) AND (p.team_id IS NULL OR p.team_id = m.team_id)
)
ON CONFLICT (session_id, user_id) DO NOTHING;

-- name: CompleteTrainingSession :execrows
UPDATE training_session_assignments SET completed_at = now(), completed_by_id = sqlc.arg(user_id)
WHERE session_id = sqlc.arg(session_id) AND user_id = sqlc.arg(user_id) AND completed_at IS NULL;

-- name: ListTrainingSessionsForAthlete :many
SELECT s.id, p.title AS plan_title, s.title, s.description, s.starts_at, s.ends_at, m.name_pt AS modality_name, a.completed_at
FROM training_session_assignments a
JOIN training_sessions s ON s.id = a.session_id
JOIN training_plans p ON p.id = s.plan_id
LEFT JOIN modalities m ON m.id = s.modality_id
WHERE a.user_id = sqlc.arg(user_id)
ORDER BY s.starts_at DESC, s.id DESC LIMIT sqlc.arg(row_limit);

-- name: ListTrainingPlansForCoach :many
SELECT p.id, p.title, p.description, p.programme_id, p.team_id, p.created_at
FROM training_plans p
WHERE EXISTS (SELECT 1 FROM staff_grants g WHERE g.user_id = sqlc.arg(user_id) AND g.capability = 'COACH' AND g.revoked_at IS NULL AND (g.programme_id = p.programme_id OR g.team_id = p.team_id))
ORDER BY p.created_at DESC, p.id DESC LIMIT sqlc.arg(row_limit);

-- name: ListTrainingPlansForAdmin :many
SELECT id, title, description, programme_id, team_id, created_at FROM training_plans
ORDER BY created_at DESC, id DESC LIMIT sqlc.arg(row_limit);

-- name: CanCoachManageTrainingPlan :one
SELECT EXISTS (SELECT 1 FROM training_plans p WHERE p.id = sqlc.arg(plan_id) AND EXISTS (SELECT 1 FROM staff_grants g WHERE g.user_id = sqlc.arg(user_id) AND g.capability = 'COACH' AND g.revoked_at IS NULL AND (g.programme_id = p.programme_id OR g.team_id = p.team_id)));

-- name: CanCoachManageTrainingSession :one
SELECT EXISTS (SELECT 1 FROM training_sessions s JOIN training_plans p ON p.id = s.plan_id WHERE s.id = sqlc.arg(session_id) AND EXISTS (SELECT 1 FROM staff_grants g WHERE g.user_id = sqlc.arg(user_id) AND g.capability = 'COACH' AND g.revoked_at IS NULL AND (g.programme_id = p.programme_id OR g.team_id = p.team_id)));

-- name: ListTrainingAthletesForCoach :many
SELECT DISTINCT u.id, u.name FROM users u JOIN user_memberships m ON m.user_id = u.id
WHERE u.is_active AND m.starts_on <= CURRENT_DATE AND (m.ends_on IS NULL OR m.ends_on >= CURRENT_DATE)
  AND EXISTS (SELECT 1 FROM staff_grants g WHERE g.user_id = sqlc.arg(user_id) AND g.capability = 'COACH' AND g.revoked_at IS NULL AND (g.programme_id = m.programme_id OR g.team_id = m.team_id))
ORDER BY lower(u.name), u.id LIMIT sqlc.arg(row_limit);

-- name: ListTrainingAthletesForAdmin :many
SELECT DISTINCT u.id, u.name FROM users u JOIN user_memberships m ON m.user_id = u.id
WHERE u.is_active AND m.starts_on <= CURRENT_DATE AND (m.ends_on IS NULL OR m.ends_on >= CURRENT_DATE)
ORDER BY lower(u.name), u.id LIMIT sqlc.arg(row_limit);

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
        SELECT 1 FROM user_memberships m
        WHERE m.user_id = sqlc.arg(user_id) AND m.starts_on <= CURRENT_DATE AND (m.ends_on IS NULL OR m.ends_on >= CURRENT_DATE)
          AND (NOT EXISTS (SELECT 1 FROM event_audiences a WHERE a.event_id = d.event_id)
               OR EXISTS (SELECT 1 FROM event_audiences a WHERE a.event_id = d.event_id AND a.programme_id = m.programme_id)
               OR EXISTS (SELECT 1 FROM event_team_audiences a WHERE a.event_id = d.event_id AND a.team_id = m.team_id))
    )
) OR (
    d.modality_id IS NOT NULL AND EXISTS (
        SELECT 1 FROM user_memberships m JOIN membership_modalities mm ON mm.membership_id = m.id
        WHERE m.user_id = sqlc.arg(user_id) AND m.starts_on <= CURRENT_DATE AND (m.ends_on IS NULL OR m.ends_on >= CURRENT_DATE)
          AND mm.modality_id = d.modality_id AND (d.programme_id IS NULL OR d.programme_id = m.programme_id) AND (d.team_id IS NULL OR d.team_id = m.team_id)
    )
)
ORDER BY d.published_at DESC, d.id DESC LIMIT sqlc.arg(row_limit);
