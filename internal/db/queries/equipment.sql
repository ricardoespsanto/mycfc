-- name: ListOperationalEquipment :many
SELECT id, asset_tag, name, type, status, notes, image_object_key, image_content_type, image_size_bytes, created_at, updated_at
FROM equipment
WHERE status <> 'Retired'
ORDER BY type, lower(name), asset_tag, id
LIMIT sqlc.arg(row_limit);

-- name: ListEquipmentForAdmin :many
SELECT id, asset_tag, name, type, status, notes, image_object_key, image_content_type, image_size_bytes, created_at, updated_at
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
SELECT id, asset_tag, name, type, status, notes, image_object_key, image_content_type, image_size_bytes, created_at, updated_at
FROM equipment
WHERE id = sqlc.arg(id);

-- name: CreateEquipmentWithAudit :one
WITH created AS (
    INSERT INTO equipment (asset_tag, name, type, status, notes, image_object_key, image_content_type, image_size_bytes)
    VALUES (sqlc.arg(asset_tag), sqlc.arg(name), sqlc.arg(type), sqlc.arg(status), sqlc.arg(notes), sqlc.narg(image_object_key), sqlc.narg(image_content_type), sqlc.narg(image_size_bytes))
    RETURNING id, asset_tag, name, type, status, notes, image_object_key, image_content_type, image_size_bytes, created_at, updated_at
), audited AS (
    INSERT INTO equipment_audit_events (equipment_id, actor_user_id, action, after_state)
    SELECT id, sqlc.arg(actor_user_id), 'CREATED',
           jsonb_build_object('asset_tag', asset_tag, 'name', name, 'type', type, 'status', status, 'notes', notes, 'image_object_key', image_object_key, 'image_content_type', image_content_type, 'image_size_bytes', image_size_bytes)
    FROM created
)
SELECT id, asset_tag, name, type, status, notes, image_object_key, image_content_type, image_size_bytes, created_at, updated_at
FROM created;

-- name: UpdateEquipmentWithAudit :one
WITH previous AS MATERIALIZED (
    SELECT id, asset_tag, name, type, status, notes, image_object_key, image_content_type, image_size_bytes, created_at, updated_at
    FROM equipment
    WHERE equipment.id = sqlc.arg(equipment_id) AND equipment.updated_at = sqlc.arg(expected_updated_at)
    FOR UPDATE
), updated AS (
    UPDATE equipment e
    SET asset_tag = sqlc.arg(asset_tag), name = sqlc.arg(name), type = sqlc.arg(type),
        status = sqlc.arg(status), notes = sqlc.arg(notes), image_object_key = sqlc.narg(image_object_key),
        image_content_type = sqlc.narg(image_content_type), image_size_bytes = sqlc.narg(image_size_bytes), updated_at = now()
    FROM previous p
    WHERE e.id = p.id
    RETURNING e.id, e.asset_tag, e.name, e.type, e.status, e.notes, e.image_object_key, e.image_content_type, e.image_size_bytes, e.created_at, e.updated_at
), audited AS (
    INSERT INTO equipment_audit_events (equipment_id, actor_user_id, action, before_state, after_state)
    SELECT u.id, sqlc.arg(actor_user_id), 'UPDATED',
           jsonb_build_object('asset_tag', p.asset_tag, 'name', p.name, 'type', p.type, 'status', p.status, 'notes', p.notes, 'image_object_key', p.image_object_key, 'image_content_type', p.image_content_type, 'image_size_bytes', p.image_size_bytes),
           jsonb_build_object('asset_tag', u.asset_tag, 'name', u.name, 'type', u.type, 'status', u.status, 'notes', u.notes, 'image_object_key', u.image_object_key, 'image_content_type', u.image_content_type, 'image_size_bytes', u.image_size_bytes)
    FROM updated u JOIN previous p ON p.id = u.id
)
SELECT id, asset_tag, name, type, status, notes, image_object_key, image_content_type, image_size_bytes, created_at, updated_at
FROM updated;

-- name: RetireEquipmentWithAudit :one
WITH previous AS MATERIALIZED (
    SELECT id, asset_tag, name, type, status, notes, image_object_key, image_content_type, image_size_bytes, created_at, updated_at
    FROM equipment
    WHERE equipment.id = sqlc.arg(equipment_id)
      AND equipment.updated_at = sqlc.arg(expected_updated_at)
      AND equipment.status <> 'Retired'
    FOR UPDATE
), updated AS (
    UPDATE equipment e SET status = 'Retired', updated_at = now()
    FROM previous p WHERE e.id = p.id
    RETURNING e.id, e.asset_tag, e.name, e.type, e.status, e.notes, e.image_object_key, e.image_content_type, e.image_size_bytes, e.created_at, e.updated_at
), cancelled AS (
    UPDATE maintenance_tasks mt SET status = 'Cancelled', completed_at = NULL, updated_at = now()
    FROM updated u
    WHERE mt.equipment_id = u.id AND mt.status IN ('Scheduled', 'In_Progress')
    RETURNING mt.id
), cancelled_ids AS (
    SELECT COALESCE(array_agg(id), '{}'::uuid[]) AS ids FROM cancelled
), audited AS (
    INSERT INTO equipment_audit_events (equipment_id, actor_user_id, action, before_state, after_state, affected_maintenance_ids)
    SELECT u.id, sqlc.arg(actor_user_id), 'RETIRED',
           jsonb_build_object('asset_tag', p.asset_tag, 'name', p.name, 'type', p.type, 'status', p.status, 'notes', p.notes, 'image_object_key', p.image_object_key, 'image_content_type', p.image_content_type, 'image_size_bytes', p.image_size_bytes),
           jsonb_build_object('asset_tag', u.asset_tag, 'name', u.name, 'type', u.type, 'status', u.status, 'notes', u.notes, 'image_object_key', u.image_object_key, 'image_content_type', u.image_content_type, 'image_size_bytes', u.image_size_bytes), c.ids
    FROM updated u JOIN previous p ON p.id = u.id CROSS JOIN cancelled_ids c
)
SELECT id, asset_tag, name, type, status, notes, image_object_key, image_content_type, image_size_bytes, created_at, updated_at
FROM updated;

-- name: ReactivateEquipmentWithAudit :one
WITH previous AS MATERIALIZED (
    SELECT id, asset_tag, name, type, status, notes, image_object_key, image_content_type, image_size_bytes, created_at, updated_at
    FROM equipment
    WHERE equipment.id = sqlc.arg(equipment_id) AND equipment.status = 'Retired'
    FOR UPDATE
), updated AS (
    UPDATE equipment e SET status = 'Operational', updated_at = now()
    FROM previous p WHERE e.id = p.id
    RETURNING e.id, e.asset_tag, e.name, e.type, e.status, e.notes, e.image_object_key, e.image_content_type, e.image_size_bytes, e.created_at, e.updated_at
), audited AS (
    INSERT INTO equipment_audit_events (equipment_id, actor_user_id, action, before_state, after_state)
    SELECT u.id, sqlc.arg(actor_user_id), 'REACTIVATED',
           jsonb_build_object('asset_tag', p.asset_tag, 'name', p.name, 'type', p.type, 'status', p.status, 'notes', p.notes, 'image_object_key', p.image_object_key, 'image_content_type', p.image_content_type, 'image_size_bytes', p.image_size_bytes),
           jsonb_build_object('asset_tag', u.asset_tag, 'name', u.name, 'type', u.type, 'status', u.status, 'notes', u.notes, 'image_object_key', u.image_object_key, 'image_content_type', u.image_content_type, 'image_size_bytes', u.image_size_bytes)
    FROM updated u JOIN previous p ON p.id = u.id
)
SELECT id, asset_tag, name, type, status, notes, image_object_key, image_content_type, image_size_bytes, created_at, updated_at
FROM updated;

-- name: ListEquipmentAuditEvents :many
SELECT a.id, a.equipment_id, a.action, a.before_state, a.after_state,
       a.affected_maintenance_ids, a.occurred_at, u.name AS actor_name
FROM equipment_audit_events a
JOIN users u ON u.id = a.actor_user_id
WHERE a.equipment_id = sqlc.arg(equipment_id)
ORDER BY a.occurred_at DESC, a.id DESC
LIMIT sqlc.arg(row_limit);
