-- name: CreateRepairRequest :one
INSERT INTO repair_requests (
    idempotency_key,
    equipment_id,
    reported_by_id,
    issue_description,
    image_object_key,
    image_content_type,
    image_size_bytes
) VALUES (
    sqlc.arg(idempotency_key),
    sqlc.arg(equipment_id),
    sqlc.narg(reported_by_id),
    sqlc.arg(issue_description),
    sqlc.narg(image_object_key),
    sqlc.narg(image_content_type),
    sqlc.narg(image_size_bytes)
)
RETURNING id, idempotency_key, equipment_id, reported_by_id,
          issue_description, status, image_object_key, image_content_type,
          image_size_bytes, date_reported, updated_at, resolved_at;

-- name: GetRepairByIdempotencyKey :one
SELECT id, idempotency_key, equipment_id, reported_by_id,
       issue_description, status, image_object_key, image_content_type,
       image_size_bytes, date_reported, updated_at, resolved_at
FROM repair_requests
WHERE idempotency_key = sqlc.arg(idempotency_key);

-- name: GetRepairRequestByID :one
SELECT id, idempotency_key, equipment_id, reported_by_id,
       issue_description, status, image_object_key, image_content_type,
       image_size_bytes, date_reported, updated_at, resolved_at
FROM repair_requests
WHERE id = sqlc.arg(id);

-- name: ListPendingRepairRequests :many
SELECT
    rr.id,
    rr.idempotency_key,
    rr.equipment_id,
    e.asset_tag,
    e.name AS equipment_name,
    e.type AS equipment_type,
    rr.reported_by_id,
    u.name AS reported_by_name,
    rr.issue_description,
    rr.status,
    rr.image_object_key,
    rr.image_content_type,
    rr.image_size_bytes,
    rr.date_reported,
    rr.updated_at,
    rr.resolved_at
FROM repair_requests rr
JOIN equipment e ON e.id = rr.equipment_id
LEFT JOIN users u ON u.id = rr.reported_by_id
WHERE rr.status IN ('Pendente', 'Em_Analise')
ORDER BY rr.date_reported ASC, rr.id ASC
LIMIT sqlc.arg(row_limit);

-- name: UpdateRepairStatus :one
UPDATE repair_requests
SET status = sqlc.arg(status),
    resolved_at = CASE
        WHEN sqlc.arg(status)::repair_status = 'Resolvido' THEN COALESCE(resolved_at, now())
        ELSE NULL
    END,
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND status = sqlc.arg(expected_status)
RETURNING id, idempotency_key, equipment_id, reported_by_id,
          issue_description, status, image_object_key, image_content_type,
          image_size_bytes, date_reported, updated_at, resolved_at;
