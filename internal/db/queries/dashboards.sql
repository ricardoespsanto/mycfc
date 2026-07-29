-- name: ListRecentTrainingLogs :many
SELECT id, user_id, occurred_at, duration_seconds, distance_metres, notes, created_at
FROM training_logs
WHERE user_id = sqlc.arg(user_id)
  AND occurred_at >= sqlc.arg(since)
ORDER BY occurred_at DESC, id DESC
LIMIT sqlc.arg(row_limit)
OFFSET sqlc.arg(row_offset);

-- name: ListRecentPerformanceMetrics :many
SELECT id, user_id, metric_type, label_pt, value, unit_pt, measured_at, created_at
FROM performance_metrics
WHERE user_id = sqlc.arg(user_id)
  AND measured_at >= sqlc.arg(since)
ORDER BY measured_at DESC, id DESC
LIMIT sqlc.arg(row_limit);

-- name: ListPublishedNews :many
SELECT id, title_pt, summary_pt, url, published_at, is_published, created_at, updated_at
FROM news_items
WHERE is_published = true
  AND published_at <= now()
ORDER BY published_at DESC, id DESC
LIMIT sqlc.arg(row_limit);

-- name: ListNewsForAdmin :many
SELECT id, title_pt, summary_pt, url, published_at, is_published, created_at, updated_at
FROM news_items
ORDER BY published_at DESC, id DESC
LIMIT sqlc.arg(row_limit)
OFFSET sqlc.arg(row_offset);

-- name: CreateNews :one
INSERT INTO news_items (title_pt, summary_pt, url, published_at)
VALUES (sqlc.arg(title_pt), sqlc.arg(summary_pt), sqlc.arg(url), sqlc.arg(published_at))
RETURNING id, title_pt, summary_pt, url, published_at, is_published, created_at, updated_at;

-- name: PublishNews :execrows
UPDATE news_items
SET is_published = true, updated_at = now()
WHERE id = sqlc.arg(id) AND is_published = false;

-- name: ExpireNews :execrows
UPDATE news_items
SET is_published = false, updated_at = now()
WHERE id = sqlc.arg(id) AND is_published = true;

-- name: CountEquipmentByStatus :many
SELECT status, count(*)::bigint AS total
FROM equipment
GROUP BY status
ORDER BY status;

-- name: ListUpcomingMaintenance :many
SELECT
    mt.id,
    mt.equipment_id,
    e.asset_tag,
    e.name AS equipment_name,
    e.type AS equipment_type,
    mt.scheduled_for,
    mt.description,
    mt.status,
    mt.created_by_id,
    mt.completed_at,
    mt.created_at,
    mt.updated_at
FROM maintenance_tasks mt
JOIN equipment e ON e.id = mt.equipment_id
WHERE mt.status IN ('Scheduled', 'In_Progress')
  AND mt.scheduled_for >= sqlc.arg(from_time)
  AND mt.scheduled_for < sqlc.arg(to_time)
ORDER BY mt.scheduled_for ASC, mt.id ASC
LIMIT sqlc.arg(row_limit)
OFFSET sqlc.arg(row_offset);
