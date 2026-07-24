-- name: CreateMaintenanceTask :one
INSERT INTO maintenance_tasks (
    equipment_id,
    scheduled_for,
    description,
    created_by_id
) VALUES (
    sqlc.arg(equipment_id),
    sqlc.arg(scheduled_for),
    sqlc.arg(description),
    sqlc.narg(created_by_id)
)
RETURNING id, equipment_id, scheduled_for, description, status,
          created_by_id, completed_at, created_at, updated_at;

-- name: ScheduleMaintenanceTask :one
WITH eligible_equipment AS (
    SELECT id
    FROM equipment
    WHERE equipment.id = sqlc.arg(equipment_id)
      AND status <> 'Retired'
    FOR UPDATE
), task AS (
    INSERT INTO maintenance_tasks (equipment_id, scheduled_for, description, created_by_id)
    SELECT id, sqlc.arg(scheduled_for), sqlc.arg(description), sqlc.narg(created_by_id)
    FROM eligible_equipment
    RETURNING id, equipment_id, scheduled_for, description, status,
              created_by_id, completed_at, created_at, updated_at
), equipment_status AS (
    UPDATE equipment e
    SET status = 'Maintenance', updated_at = now()
    FROM task
    WHERE e.id = task.equipment_id
      AND task.scheduled_for <= now()
    RETURNING e.id
)
SELECT id, equipment_id, scheduled_for, description, status,
       created_by_id, completed_at, created_at, updated_at
FROM task;

-- name: CompleteMaintenanceTask :one
UPDATE maintenance_tasks
SET status = 'Completed',
    completed_at = COALESCE(completed_at, now()),
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND status NOT IN ('Completed', 'Cancelled')
RETURNING id, equipment_id, scheduled_for, description, status,
          created_by_id, completed_at, created_at, updated_at;

-- name: CancelMaintenanceTask :one
UPDATE maintenance_tasks
SET status = 'Cancelled',
    completed_at = NULL,
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND status NOT IN ('Completed', 'Cancelled')
RETURNING id, equipment_id, scheduled_for, description, status,
          created_by_id, completed_at, created_at, updated_at;
