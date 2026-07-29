-- name: ListAnnouncementProgrammes :many
SELECT id, name_pt FROM programmes ORDER BY name_pt;

-- name: ListAnnouncementTeams :many
SELECT id, programme_id, name FROM teams ORDER BY name, id;

-- name: ListAnnouncementCategories :many
SELECT id, programme_id, name_pt FROM competition_categories ORDER BY name_pt, id;

-- name: ListAnnouncementModalities :many
SELECT id, name_pt FROM modalities ORDER BY name_pt;

-- name: ListAnnouncementEvents :many
SELECT id, title FROM events WHERE starts_at >= now() ORDER BY starts_at, id LIMIT 100;

-- name: CreateAnnouncement :one
INSERT INTO announcements (title, body, author_id, expires_at)
VALUES (sqlc.arg(title), sqlc.arg(body), sqlc.arg(author_id), sqlc.narg(expires_at))
RETURNING id, title, body, status::text AS status, author_id, published_by_id, expired_by_id, published_at, expires_at, expired_at, created_at, updated_at;

-- name: AddAnnouncementTarget :exec
INSERT INTO announcement_targets (announcement_id, target_type, target_id)
VALUES (sqlc.arg(announcement_id), sqlc.arg(target_type)::announcement_target_type, sqlc.narg(target_id));

-- name: PublishAnnouncement :execrows
UPDATE announcements SET status = 'PUBLISHED', published_at = now(), published_by_id = sqlc.arg(actor_user_id), updated_at = now()
WHERE id = sqlc.arg(id) AND status = 'DRAFT';

-- name: ExpireAnnouncement :execrows
UPDATE announcements SET status = 'EXPIRED', expired_at = now(), expired_by_id = sqlc.arg(actor_user_id), updated_at = now()
WHERE id = sqlc.arg(id) AND status = 'PUBLISHED';

-- name: ListAnnouncementsForAuthor :many
SELECT id, title, status::text AS status, published_at, expires_at
FROM announcements
WHERE author_id = sqlc.arg(author_id)
ORDER BY created_at DESC, id
LIMIT sqlc.arg(row_limit)
OFFSET sqlc.arg(row_offset);

-- name: GetAnnouncementAuthor :one
SELECT author_id FROM announcements WHERE id = sqlc.arg(id);

-- name: ListVisibleAnnouncements :many
SELECT DISTINCT a.id, a.title, a.body, a.published_at, a.expires_at, d.read_at
FROM announcements a
LEFT JOIN announcement_deliveries d ON d.announcement_id = a.id AND d.user_id = sqlc.arg(user_id)
WHERE a.status = 'PUBLISHED' AND (a.expires_at IS NULL OR a.expires_at > now())
  AND (
    NOT EXISTS (SELECT 1 FROM announcement_targets t WHERE t.announcement_id = a.id)
    OR EXISTS (
      SELECT 1 FROM announcement_targets t
      JOIN users subject ON subject.is_active AND (subject.id = sqlc.arg(user_id) OR subject.guardian_id = sqlc.arg(user_id))
      LEFT JOIN user_memberships m ON m.user_id = subject.id AND m.starts_on <= CURRENT_DATE AND (m.ends_on IS NULL OR m.ends_on >= CURRENT_DATE)
      LEFT JOIN membership_modalities mm ON mm.membership_id = m.id
      WHERE t.announcement_id = a.id
        AND (NOT EXISTS (SELECT 1 FROM announcement_targets g WHERE g.announcement_id = a.id AND g.target_type = 'GUARDIAN') OR subject.guardian_id = sqlc.arg(user_id))
        AND (NOT EXISTS (SELECT 1 FROM announcement_targets n WHERE n.announcement_id = a.id AND n.target_type <> 'GUARDIAN') OR (
        (t.target_type = 'PROGRAMME' AND m.programme_id = t.target_id)
        OR (t.target_type = 'TEAM' AND m.team_id = t.target_id)
        OR (t.target_type = 'CATEGORY' AND m.competition_category_id = t.target_id)
        OR (t.target_type = 'MODALITY' AND mm.modality_id = t.target_id)
        OR (t.target_type = 'EVENT' AND EXISTS (
          SELECT 1 FROM events e WHERE e.id = t.target_id AND (
            NOT EXISTS (SELECT 1 FROM event_audiences ea WHERE ea.event_id = e.id)
            OR EXISTS (SELECT 1 FROM event_audiences ea WHERE ea.event_id = e.id AND ea.programme_id = m.programme_id)
            OR EXISTS (SELECT 1 FROM event_team_audiences eta WHERE eta.event_id = e.id AND eta.team_id = m.team_id)
          )
        ))
        ))
    )
  )
ORDER BY a.published_at DESC, a.id
LIMIT sqlc.arg(row_limit)
OFFSET sqlc.arg(row_offset);

-- name: GetVisibleAnnouncement :one
SELECT DISTINCT a.id, a.title, a.body, a.published_at, a.expires_at, d.read_at
FROM announcements a
LEFT JOIN announcement_deliveries d ON d.announcement_id = a.id AND d.user_id = sqlc.arg(user_id)
WHERE a.id = sqlc.arg(id)
  AND a.status = 'PUBLISHED' AND (a.expires_at IS NULL OR a.expires_at > now())
  AND (
    NOT EXISTS (SELECT 1 FROM announcement_targets t WHERE t.announcement_id = a.id)
    OR EXISTS (
      SELECT 1 FROM announcement_targets t
      JOIN users subject ON subject.is_active AND (subject.id = sqlc.arg(user_id) OR subject.guardian_id = sqlc.arg(user_id))
      LEFT JOIN user_memberships m ON m.user_id = subject.id AND m.starts_on <= CURRENT_DATE AND (m.ends_on IS NULL OR m.ends_on >= CURRENT_DATE)
      LEFT JOIN membership_modalities mm ON mm.membership_id = m.id
      WHERE t.announcement_id = a.id
        AND (NOT EXISTS (SELECT 1 FROM announcement_targets g WHERE g.announcement_id = a.id AND g.target_type = 'GUARDIAN') OR subject.guardian_id = sqlc.arg(user_id))
        AND (NOT EXISTS (SELECT 1 FROM announcement_targets n WHERE n.announcement_id = a.id AND n.target_type <> 'GUARDIAN') OR (
        (t.target_type = 'PROGRAMME' AND m.programme_id = t.target_id)
        OR (t.target_type = 'TEAM' AND m.team_id = t.target_id)
        OR (t.target_type = 'CATEGORY' AND m.competition_category_id = t.target_id)
        OR (t.target_type = 'MODALITY' AND mm.modality_id = t.target_id)
        OR (t.target_type = 'EVENT' AND EXISTS (
          SELECT 1 FROM events e WHERE e.id = t.target_id AND (
            NOT EXISTS (SELECT 1 FROM event_audiences ea WHERE ea.event_id = e.id)
            OR EXISTS (SELECT 1 FROM event_audiences ea WHERE ea.event_id = e.id AND ea.programme_id = m.programme_id)
            OR EXISTS (SELECT 1 FROM event_team_audiences eta WHERE eta.event_id = e.id AND eta.team_id = m.team_id)
          )
        ))
        ))
    )
  );

-- name: RecordAnnouncementDelivery :exec
INSERT INTO announcement_deliveries (announcement_id, user_id) VALUES (sqlc.arg(announcement_id), sqlc.arg(user_id))
ON CONFLICT (announcement_id, user_id) DO NOTHING;

-- name: MarkAnnouncementRead :exec
UPDATE announcement_deliveries SET read_at = COALESCE(read_at, now())
WHERE announcement_id = sqlc.arg(announcement_id) AND user_id = sqlc.arg(user_id);
