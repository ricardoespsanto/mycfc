-- name: ListWhatsAppGroupsForRole :many
SELECT id, name, discipline, target_role, squad_category, url,
       is_active, created_at, updated_at
FROM whatsapp_groups
WHERE target_role = sqlc.arg(target_role)
  AND is_active = true
  AND (
      squad_category IS NULL
      OR squad_category = sqlc.narg(squad_category)
  )
ORDER BY lower(discipline), lower(name), id
LIMIT sqlc.arg(row_limit);
