-- name: ListOperationalEquipment :many
SELECT id, asset_tag, name, type, status, notes, created_at, updated_at
FROM equipment
WHERE status <> 'Retired'
ORDER BY type, lower(name), asset_tag, id
LIMIT sqlc.arg(row_limit);

-- name: ListEquipmentForAdmin :many
SELECT id, asset_tag, name, type, status, notes, created_at, updated_at
FROM equipment
ORDER BY
    CASE status
        WHEN 'Maintenance' THEN 1
        WHEN 'Operational' THEN 2
        WHEN 'Retired' THEN 3
    END,
    type,
    lower(name),
    asset_tag,
    id
LIMIT sqlc.arg(row_limit)
OFFSET sqlc.arg(row_offset);

-- name: GetEquipmentByID :one
SELECT id, asset_tag, name, type, status, notes, created_at, updated_at
FROM equipment
WHERE id = sqlc.arg(id);
