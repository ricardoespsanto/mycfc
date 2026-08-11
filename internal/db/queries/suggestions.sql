-- name: CreateSuggestion :one
INSERT INTO suggestions (requester_id, category, subject, description)
VALUES (sqlc.arg(requester_id), sqlc.arg(category)::suggestion_category, sqlc.arg(subject), sqlc.arg(description))
RETURNING id, requester_id, category::text AS category, subject, description,
          status::text AS status, staff_response, responded_by_id, responded_at,
          created_at, updated_at;

-- name: ListSuggestionsForRequester :many
SELECT id, category::text AS category, subject, description, status::text AS status,
       staff_response, responded_at, created_at, updated_at
FROM suggestions
WHERE requester_id = sqlc.arg(requester_id)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(row_limit)
OFFSET sqlc.arg(row_offset);

-- name: ListSuggestionsForTriage :many
SELECT s.id, s.requester_id, u.name AS requester_name, s.category::text AS category,
       s.subject, s.description, s.status::text AS status, s.staff_response,
       s.responded_by_id, responder.name AS responder_name, s.responded_at,
       s.created_at, s.updated_at
FROM suggestions s
JOIN users u ON u.id = s.requester_id
LEFT JOIN users responder ON responder.id = s.responded_by_id
WHERE (sqlc.narg(status_filter)::text IS NULL OR s.status::text = sqlc.narg(status_filter)::text)
  AND (sqlc.narg(category_filter)::text IS NULL OR s.category::text = sqlc.narg(category_filter)::text)
ORDER BY s.updated_at DESC, s.id DESC
LIMIT sqlc.arg(row_limit)
OFFSET sqlc.arg(row_offset);

-- name: UpdateSuggestionTriage :execrows
UPDATE suggestions
SET status = sqlc.arg(status)::suggestion_status,
    staff_response = sqlc.narg(staff_response)::varchar(2000),
    responded_by_id = CASE WHEN sqlc.narg(staff_response)::varchar(2000) IS NULL THEN NULL ELSE sqlc.arg(actor_user_id)::uuid END,
    responded_at = CASE WHEN sqlc.narg(staff_response)::varchar(2000) IS NULL THEN NULL ELSE clock_timestamp() END,
    updated_at = clock_timestamp()
WHERE id = sqlc.arg(id)
  AND updated_at = sqlc.arg(expected_updated_at);
