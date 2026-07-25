-- name: ListWhatsAppGroupsForUserProgramme :many
SELECT DISTINCT id, name, discipline, programme_id, url,
       is_active, created_at, updated_at
FROM whatsapp_groups
WHERE (programme_id IS NULL OR programme_id IN (
    SELECT membership.programme_id
    FROM user_memberships membership
    JOIN programmes programme ON programme.id = membership.programme_id
    WHERE membership.user_id = sqlc.arg(user_id)
      AND programme.code = sqlc.arg(programme_code)
      AND membership.starts_on <= CURRENT_DATE
      AND (membership.ends_on IS NULL OR membership.ends_on >= CURRENT_DATE)
))
  AND is_active = true
ORDER BY lower(discipline), lower(name), id
LIMIT sqlc.arg(row_limit);
