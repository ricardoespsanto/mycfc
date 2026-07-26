-- name: ListWhatsAppGroupsForUserProgramme :many
SELECT DISTINCT id, name, discipline, programme_id, url,
       is_active, created_at, updated_at
FROM whatsapp_groups
WHERE (
    NOT EXISTS (SELECT 1 FROM whatsapp_group_targets target WHERE target.whatsapp_group_id = whatsapp_groups.id)
    OR EXISTS (
        SELECT 1
        FROM whatsapp_group_targets target
        JOIN users subject ON subject.is_active AND (subject.id = sqlc.arg(user_id) OR subject.guardian_id = sqlc.arg(user_id))
        LEFT JOIN user_memberships membership ON membership.user_id = subject.id
            AND membership.starts_on <= CURRENT_DATE AND (membership.ends_on IS NULL OR membership.ends_on >= CURRENT_DATE)
        LEFT JOIN programmes programme ON programme.id = membership.programme_id
        LEFT JOIN membership_modalities assignment ON assignment.membership_id = membership.id
        WHERE target.whatsapp_group_id = whatsapp_groups.id
          AND programme.code = sqlc.arg(programme_code)
          AND (
              (target.target_type = 'GUARDIAN' AND subject.guardian_id = sqlc.arg(user_id))
              OR (target.target_type = 'PROGRAMME' AND target.target_id = membership.programme_id)
              OR (target.target_type = 'TEAM' AND target.target_id = membership.team_id)
              OR (target.target_type = 'CATEGORY' AND target.target_id = membership.competition_category_id)
              OR (target.target_type = 'MODALITY' AND target.target_id = assignment.modality_id)
              OR (target.target_type = 'EVENT' AND EXISTS (SELECT 1 FROM events event WHERE event.id = target.target_id))
          )
    )
)
  AND is_active = true
ORDER BY lower(discipline), lower(name), id
LIMIT sqlc.arg(row_limit);
