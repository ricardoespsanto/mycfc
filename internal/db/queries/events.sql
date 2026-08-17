-- name: ListProgrammes :many
SELECT id, code, name_pt, created_at FROM programmes ORDER BY name_pt;

-- name: CreateEvent :one
INSERT INTO events (title, description, event_type, starts_at, ends_at, response_deadline, capacity, created_by_id)
VALUES (sqlc.arg(title), sqlc.arg(description), sqlc.arg(event_type), sqlc.arg(starts_at), sqlc.arg(ends_at), sqlc.narg(response_deadline), sqlc.narg(capacity), sqlc.arg(created_by_id))
RETURNING id, title, description, event_type, starts_at, ends_at, response_deadline, capacity, status, cancelled_at, cancelled_by_id, cancellation_reason, created_by_id, created_at, updated_at;

-- name: AddEventAudience :exec
INSERT INTO event_audiences (event_id, programme_id) VALUES (sqlc.arg(event_id), sqlc.arg(programme_id));

-- name: AddEventTeamAudience :exec
INSERT INTO event_team_audiences (event_id, team_id) VALUES (sqlc.arg(event_id), sqlc.arg(team_id));

-- name: ListEventProgrammeAudienceIDs :many
SELECT programme_id FROM event_audiences WHERE event_id = sqlc.arg(event_id) ORDER BY programme_id;

-- name: ListEventTeamAudienceIDs :many
SELECT team_id FROM event_team_audiences WHERE event_id = sqlc.arg(event_id) ORDER BY team_id;

-- name: DeleteEventProgrammeAudiences :exec
DELETE FROM event_audiences WHERE event_id = sqlc.arg(event_id);

-- name: DeleteEventTeamAudiences :exec
DELETE FROM event_team_audiences WHERE event_id = sqlc.arg(event_id);

-- name: GetEventForEdit :one
SELECT e.id, e.title, e.description, e.event_type, e.starts_at, e.ends_at, e.response_deadline, e.capacity,
       e.status, e.cancelled_at, e.cancelled_by_id, e.cancellation_reason, e.created_by_id, e.created_at, e.updated_at,
       EXISTS (SELECT 1 FROM event_responses r WHERE r.event_id = e.id) AS has_responses,
       EXISTS (SELECT 1 FROM competition_documents d WHERE d.event_id = e.id) AS has_document,
       (SELECT count(*)::bigint FROM event_responses r WHERE r.event_id = e.id AND r.status = 'Going') AS going_count
FROM events e
WHERE e.id = sqlc.arg(id);

-- name: UpdateEvent :one
UPDATE events e
SET title = sqlc.arg(title), description = sqlc.arg(description), event_type = sqlc.arg(event_type),
    starts_at = sqlc.arg(starts_at), ends_at = sqlc.arg(ends_at), response_deadline = sqlc.narg(response_deadline),
    capacity = sqlc.narg(capacity), updated_at = clock_timestamp()
WHERE e.id = sqlc.arg(id)
  AND e.status = 'ACTIVE'
  AND e.starts_at > sqlc.arg(as_of)
  AND e.updated_at = sqlc.arg(expected_updated_at)
  AND (sqlc.narg(capacity)::integer IS NULL OR sqlc.narg(capacity)::integer >= (SELECT count(*) FROM event_responses r WHERE r.event_id = e.id AND r.status = 'Going'))
  AND (sqlc.arg(event_type)::text = 'COMPETITION' OR NOT EXISTS (SELECT 1 FROM competition_documents d WHERE d.event_id = e.id))
  AND (NOT sqlc.arg(audience_changed)::boolean OR NOT EXISTS (SELECT 1 FROM event_responses r WHERE r.event_id = e.id))
RETURNING id, title, description, event_type, starts_at, ends_at, response_deadline, capacity, status, cancelled_at, cancelled_by_id, cancellation_reason, created_by_id, created_at, updated_at;

-- name: CancelEvent :one
UPDATE events
SET status = 'CANCELLED', cancelled_at = sqlc.arg(cancelled_at), cancelled_by_id = sqlc.arg(cancelled_by_id),
    cancellation_reason = sqlc.arg(cancellation_reason), updated_at = clock_timestamp()
WHERE id = sqlc.arg(id)
  AND status = 'ACTIVE'
  AND starts_at > sqlc.arg(cancelled_at)
  AND updated_at = sqlc.arg(expected_updated_at)
RETURNING id, title, description, event_type, starts_at, ends_at, response_deadline, capacity, status, cancelled_at, cancelled_by_id, cancellation_reason, created_by_id, created_at, updated_at;

-- name: ListTeamsForEventAuthoring :many
SELECT id, programme_id, name FROM teams ORDER BY name, id;

-- name: ListEventsForMember :many
SELECT e.id, e.title, e.event_type, e.starts_at, e.ends_at, e.response_deadline, e.capacity, e.status, e.cancellation_reason,
       COALESCE(r.status::text, 'Pending') AS response_status
FROM events e
LEFT JOIN event_responses r ON r.event_id = e.id AND r.user_id = sqlc.arg(user_id)
WHERE e.starts_at >= now()
  AND (
      (
          NOT EXISTS (SELECT 1 FROM event_audiences a WHERE a.event_id = e.id)
          AND NOT EXISTS (SELECT 1 FROM event_team_audiences a WHERE a.event_id = e.id)
      )
       OR EXISTS (
           SELECT 1 FROM user_memberships m
           JOIN event_audiences a ON a.programme_id = m.programme_id
          JOIN users subject ON subject.id = m.user_id
          WHERE a.event_id = e.id AND (subject.id = sqlc.arg(user_id) OR subject.guardian_id = sqlc.arg(user_id))
             AND m.starts_on <= CURRENT_DATE AND (m.ends_on IS NULL OR m.ends_on >= CURRENT_DATE)
       )
       OR EXISTS (
           SELECT 1 FROM user_memberships m
           JOIN event_team_audiences a ON a.team_id = m.team_id
           JOIN users subject ON subject.id = m.user_id
           WHERE a.event_id = e.id AND (subject.id = sqlc.arg(user_id) OR subject.guardian_id = sqlc.arg(user_id))
             AND m.starts_on <= CURRENT_DATE AND (m.ends_on IS NULL OR m.ends_on >= CURRENT_DATE)
       )
  )
ORDER BY e.starts_at, e.id
LIMIT sqlc.arg(row_limit);

-- name: ListEventsForToday :many
SELECT e.id, e.title, e.starts_at, e.ends_at, e.status, e.cancellation_reason
FROM events e
WHERE e.starts_at < sqlc.arg(day_ends_at)
  AND e.ends_at >= sqlc.arg(day_starts_at)
  AND (
      sqlc.arg(is_admin)::boolean
      OR (
          NOT EXISTS (SELECT 1 FROM event_audiences a WHERE a.event_id = e.id)
          AND NOT EXISTS (SELECT 1 FROM event_team_audiences a WHERE a.event_id = e.id)
      )
      OR (
          EXISTS (
              SELECT 1
              FROM user_memberships m
              JOIN event_audiences a ON a.programme_id = m.programme_id
              JOIN users subject ON subject.id = m.user_id
              WHERE a.event_id = e.id
                AND (subject.id = sqlc.arg(user_id) OR subject.guardian_id = sqlc.arg(user_id))
                AND m.starts_on <= CURRENT_DATE AND (m.ends_on IS NULL OR m.ends_on >= CURRENT_DATE)
          )
          OR EXISTS (
              SELECT 1
              FROM user_memberships m
              JOIN event_team_audiences a ON a.team_id = m.team_id
              JOIN users subject ON subject.id = m.user_id
              WHERE a.event_id = e.id
                AND (subject.id = sqlc.arg(user_id) OR subject.guardian_id = sqlc.arg(user_id))
                AND m.starts_on <= CURRENT_DATE AND (m.ends_on IS NULL OR m.ends_on >= CURRENT_DATE)
          )
      )
      OR (
          (EXISTS (SELECT 1 FROM event_audiences a WHERE a.event_id = e.id) OR EXISTS (SELECT 1 FROM event_team_audiences a WHERE a.event_id = e.id))
          AND NOT EXISTS (
              SELECT 1 FROM event_audiences a
              WHERE a.event_id = e.id AND NOT EXISTS (
                  SELECT 1 FROM staff_grants g
                  WHERE g.user_id = sqlc.arg(user_id) AND g.capability = 'COACH' AND g.revoked_at IS NULL AND g.programme_id = a.programme_id
              )
          )
          AND NOT EXISTS (
              SELECT 1 FROM event_team_audiences a
              JOIN teams t ON t.id = a.team_id
              WHERE a.event_id = e.id AND NOT EXISTS (
                  SELECT 1 FROM staff_grants g
                  WHERE g.user_id = sqlc.arg(user_id) AND g.capability = 'COACH' AND g.revoked_at IS NULL AND (g.team_id = a.team_id OR g.programme_id = t.programme_id)
              )
          )
      )
  )
ORDER BY e.starts_at, e.id;

-- name: ListEventsForAdmin :many
SELECT e.id, e.title, e.event_type, e.starts_at, e.ends_at, e.response_deadline, e.capacity, e.status, e.cancellation_reason,
       (SELECT count(*)::bigint FROM event_responses r WHERE r.event_id = e.id AND r.status = 'Going') AS going_count
FROM events e
ORDER BY e.starts_at DESC, e.id
LIMIT sqlc.arg(row_limit)
OFFSET sqlc.arg(row_offset);

-- name: ListEventsForCoach :many
SELECT e.id, e.title, e.event_type, e.starts_at, e.ends_at, e.response_deadline, e.capacity, e.status, e.cancellation_reason,
       (SELECT count(*)::bigint FROM event_responses r WHERE r.event_id = e.id AND r.status = 'Going') AS going_count
FROM events e
WHERE (EXISTS (SELECT 1 FROM event_audiences a WHERE a.event_id = e.id) OR EXISTS (SELECT 1 FROM event_team_audiences a WHERE a.event_id = e.id))
  AND NOT EXISTS (
      SELECT 1 FROM event_audiences a
      WHERE a.event_id = e.id AND NOT EXISTS (
          SELECT 1 FROM staff_grants g
          WHERE g.user_id = sqlc.arg(user_id) AND g.capability = 'COACH' AND g.revoked_at IS NULL AND g.programme_id = a.programme_id
      )
  )
  AND NOT EXISTS (
      SELECT 1 FROM event_team_audiences a JOIN teams t ON t.id = a.team_id
      WHERE a.event_id = e.id AND NOT EXISTS (
          SELECT 1 FROM staff_grants g
          WHERE g.user_id = sqlc.arg(user_id) AND g.capability = 'COACH' AND g.revoked_at IS NULL AND (g.team_id = a.team_id OR g.programme_id = t.programme_id)
      )
  )
ORDER BY e.starts_at DESC, e.id
LIMIT sqlc.arg(row_limit);

-- name: GetEventForResponse :one
SELECT id, title, description, event_type, starts_at, ends_at, response_deadline, capacity, status, cancelled_at, cancelled_by_id, cancellation_reason, created_by_id, created_at, updated_at
FROM events WHERE id = sqlc.arg(id) AND status = 'ACTIVE' FOR UPDATE;

-- name: GetRespondableEvent :one
SELECT e.id, e.title, e.description, e.event_type, e.starts_at, e.ends_at, e.response_deadline, e.capacity, e.status, e.cancelled_at, e.cancelled_by_id, e.cancellation_reason, e.created_by_id, e.created_at, e.updated_at
FROM events e
JOIN users subject ON subject.id = sqlc.arg(subject_user_id) AND subject.is_active
WHERE e.id = sqlc.arg(event_id)
  AND (subject.id = sqlc.arg(actor_user_id) OR subject.guardian_id = sqlc.arg(actor_user_id))
  AND (
      (
          NOT EXISTS (SELECT 1 FROM event_audiences a WHERE a.event_id = e.id)
          AND NOT EXISTS (SELECT 1 FROM event_team_audiences a WHERE a.event_id = e.id)
      )
       OR EXISTS (
           SELECT 1 FROM user_memberships m JOIN event_audiences a ON a.programme_id = m.programme_id
          WHERE a.event_id = e.id AND m.user_id = subject.id
             AND m.starts_on <= CURRENT_DATE AND (m.ends_on IS NULL OR m.ends_on >= CURRENT_DATE)
       )
       OR EXISTS (
           SELECT 1 FROM user_memberships m JOIN event_team_audiences a ON a.team_id = m.team_id
           WHERE a.event_id = e.id AND m.user_id = subject.id
             AND m.starts_on <= CURRENT_DATE AND (m.ends_on IS NULL OR m.ends_on >= CURRENT_DATE)
       )
  );

-- name: GetEventResponse :one
SELECT event_id, user_id, status::text AS status, responded_by_id, responded_at, checked_in_at, checked_in_by_id
FROM event_responses WHERE event_id = sqlc.arg(event_id) AND user_id = sqlc.arg(user_id);

-- name: CountGoingEventResponses :one
SELECT count(*)::bigint FROM event_responses WHERE event_id = sqlc.arg(event_id) AND status = 'Going';

-- name: SaveEventResponse :exec
INSERT INTO event_responses (event_id, user_id, status, responded_by_id)
VALUES (sqlc.arg(event_id), sqlc.arg(user_id), sqlc.arg(status)::event_response_status, sqlc.arg(responded_by_id))
ON CONFLICT (event_id, user_id) DO UPDATE SET
    status = EXCLUDED.status, responded_by_id = EXCLUDED.responded_by_id, responded_at = now(),
    checked_in_at = NULL, checked_in_by_id = NULL;

-- name: GetEventDetailForMember :one
SELECT e.id, e.title, e.description, e.event_type, e.starts_at, e.ends_at, e.response_deadline, e.capacity, e.status, e.cancelled_at, e.cancelled_by_id, e.cancellation_reason,
       COALESCE(r.status::text, 'Pending') AS response_status
FROM events e
LEFT JOIN event_responses r ON r.event_id = e.id AND r.user_id = sqlc.arg(user_id)
WHERE e.id = sqlc.arg(event_id)
  AND (
      (
          NOT EXISTS (SELECT 1 FROM event_audiences a WHERE a.event_id = e.id)
          AND NOT EXISTS (SELECT 1 FROM event_team_audiences a WHERE a.event_id = e.id)
      )
       OR EXISTS (
           SELECT 1 FROM user_memberships m JOIN event_audiences a ON a.programme_id = m.programme_id
          JOIN users subject ON subject.id = m.user_id
          WHERE a.event_id = e.id AND (subject.id = sqlc.arg(user_id) OR subject.guardian_id = sqlc.arg(user_id))
             AND m.starts_on <= CURRENT_DATE AND (m.ends_on IS NULL OR m.ends_on >= CURRENT_DATE)
       )
       OR EXISTS (
           SELECT 1 FROM user_memberships m JOIN event_team_audiences a ON a.team_id = m.team_id
           JOIN users subject ON subject.id = m.user_id
           WHERE a.event_id = e.id AND (subject.id = sqlc.arg(user_id) OR subject.guardian_id = sqlc.arg(user_id))
             AND m.starts_on <= CURRENT_DATE AND (m.ends_on IS NULL OR m.ends_on >= CURRENT_DATE)
       )
  );

-- name: GetEventDetailForAdmin :one
SELECT e.id, e.title, e.description, e.event_type, e.starts_at, e.ends_at, e.response_deadline, e.capacity,
       e.status, e.cancelled_at, e.cancelled_by_id, e.cancellation_reason, canceller.name AS cancelled_by_name,
       e.created_by_id, e.created_at, e.updated_at
FROM events e
LEFT JOIN users canceller ON canceller.id = e.cancelled_by_id
WHERE e.id = sqlc.arg(id);

-- name: ListEventResponsesForAdmin :many
SELECT r.event_id, r.user_id, u.name AS user_name, r.status::text AS status, r.responded_at, r.checked_in_at
FROM event_responses r JOIN users u ON u.id = r.user_id
WHERE r.event_id = sqlc.arg(event_id)
ORDER BY lower(u.name), u.id
LIMIT sqlc.arg(row_limit)
OFFSET sqlc.arg(row_offset);

-- name: ConfirmWaitlistedResponse :execrows
UPDATE event_responses r SET status = 'Going', responded_at = now(), responded_by_id = sqlc.arg(staff_user_id)
WHERE r.event_id = sqlc.arg(event_id) AND r.user_id = sqlc.arg(user_id) AND r.status = 'Waitlisted'
  AND EXISTS (SELECT 1 FROM events e WHERE e.id = r.event_id AND e.status = 'ACTIVE');

-- name: CheckInEventResponse :execrows
UPDATE event_responses r SET checked_in_at = now(), checked_in_by_id = sqlc.arg(staff_user_id)
WHERE r.event_id = sqlc.arg(event_id) AND r.user_id = sqlc.arg(user_id) AND r.status = 'Going' AND r.checked_in_at IS NULL
  AND EXISTS (SELECT 1 FROM events e WHERE e.id = r.event_id AND e.status = 'ACTIVE');
