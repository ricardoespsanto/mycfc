-- name: CreatePhotoAlbum :one
INSERT INTO photo_albums (title, description, created_by_id)
VALUES (sqlc.arg(title), sqlc.arg(description), sqlc.arg(created_by_id))
RETURNING id, title, description, status, created_by_id, archived_by_id, archived_at, created_at, updated_at;

-- name: AddPhotoAlbumProgrammeAudience :exec
INSERT INTO photo_album_programme_audiences (album_id, programme_id)
VALUES (sqlc.arg(album_id), sqlc.arg(programme_id));

-- name: AddPhotoAlbumTeamAudience :exec
INSERT INTO photo_album_team_audiences (album_id, team_id)
VALUES (sqlc.arg(album_id), sqlc.arg(team_id));

-- name: ListVisiblePhotoAlbums :many
SELECT a.id, a.title, a.description, a.status, a.created_at, a.updated_at,
       COALESCE((SELECT string_agg(p.name_pt, ', ' ORDER BY p.name_pt) FROM photo_album_programme_audiences pa JOIN programmes p ON p.id = pa.programme_id WHERE pa.album_id = a.id), '')::text AS programme_names,
       COALESCE((SELECT string_agg(t.name, ', ' ORDER BY t.name) FROM photo_album_team_audiences ta JOIN teams t ON t.id = ta.team_id WHERE ta.album_id = a.id), '')::text AS team_names
FROM photo_albums a
WHERE (a.status = 'OPEN' OR sqlc.arg(privileged)::boolean)
  AND (
    sqlc.arg(privileged)::boolean
    OR EXISTS (
      SELECT 1 FROM user_memberships m
      JOIN users subject ON subject.id = m.user_id AND subject.is_active
      WHERE (subject.id = sqlc.arg(user_id) OR subject.guardian_id = sqlc.arg(user_id))
        AND m.starts_on <= CURRENT_DATE AND (m.ends_on IS NULL OR m.ends_on >= CURRENT_DATE)
        AND (
          EXISTS (SELECT 1 FROM photo_album_programme_audiences pa WHERE pa.album_id = a.id AND pa.programme_id = m.programme_id)
          OR EXISTS (SELECT 1 FROM photo_album_team_audiences ta WHERE ta.album_id = a.id AND ta.team_id = m.team_id)
        )
    )
    OR EXISTS (
      SELECT 1 FROM staff_grants g
      WHERE g.user_id = sqlc.arg(user_id) AND g.capability = 'COACH' AND g.revoked_at IS NULL
        AND (
          EXISTS (SELECT 1 FROM photo_album_programme_audiences pa WHERE pa.album_id = a.id AND pa.programme_id = g.programme_id)
          OR EXISTS (SELECT 1 FROM photo_album_team_audiences ta WHERE ta.album_id = a.id AND ta.team_id = g.team_id)
          OR EXISTS (SELECT 1 FROM photo_album_team_audiences ta JOIN teams t ON t.id = ta.team_id WHERE ta.album_id = a.id AND t.programme_id = g.programme_id)
        )
    )
  )
ORDER BY a.created_at DESC, a.id DESC;

-- name: GetVisiblePhotoAlbum :one
SELECT a.id, a.title, a.description, a.status, a.created_at, a.updated_at,
       COALESCE((SELECT string_agg(p.name_pt, ', ' ORDER BY p.name_pt) FROM photo_album_programme_audiences pa JOIN programmes p ON p.id = pa.programme_id WHERE pa.album_id = a.id), '')::text AS programme_names,
       COALESCE((SELECT string_agg(t.name, ', ' ORDER BY t.name) FROM photo_album_team_audiences ta JOIN teams t ON t.id = ta.team_id WHERE ta.album_id = a.id), '')::text AS team_names
FROM photo_albums a
WHERE a.id = sqlc.arg(id)
  AND (a.status = 'OPEN' OR sqlc.arg(privileged)::boolean)
  AND (
    sqlc.arg(privileged)::boolean
    OR EXISTS (
      SELECT 1 FROM user_memberships m
      JOIN users subject ON subject.id = m.user_id AND subject.is_active
      WHERE (subject.id = sqlc.arg(user_id) OR subject.guardian_id = sqlc.arg(user_id))
        AND m.starts_on <= CURRENT_DATE AND (m.ends_on IS NULL OR m.ends_on >= CURRENT_DATE)
        AND (
          EXISTS (SELECT 1 FROM photo_album_programme_audiences pa WHERE pa.album_id = a.id AND pa.programme_id = m.programme_id)
          OR EXISTS (SELECT 1 FROM photo_album_team_audiences ta WHERE ta.album_id = a.id AND ta.team_id = m.team_id)
        )
    )
    OR EXISTS (
      SELECT 1 FROM staff_grants g
      WHERE g.user_id = sqlc.arg(user_id) AND g.capability = 'COACH' AND g.revoked_at IS NULL
        AND (
          EXISTS (SELECT 1 FROM photo_album_programme_audiences pa WHERE pa.album_id = a.id AND pa.programme_id = g.programme_id)
          OR EXISTS (SELECT 1 FROM photo_album_team_audiences ta WHERE ta.album_id = a.id AND ta.team_id = g.team_id)
          OR EXISTS (SELECT 1 FROM photo_album_team_audiences ta JOIN teams t ON t.id = ta.team_id WHERE ta.album_id = a.id AND t.programme_id = g.programme_id)
        )
    )
  );

-- name: ArchivePhotoAlbum :one
UPDATE photo_albums
SET status = 'ARCHIVED', archived_by_id = sqlc.arg(archived_by_id), archived_at = sqlc.arg(archived_at), updated_at = clock_timestamp()
WHERE id = sqlc.arg(id) AND status = 'OPEN' AND updated_at = sqlc.arg(expected_updated_at)
RETURNING id, title, description, status, created_by_id, archived_by_id, archived_at, created_at, updated_at;

-- name: ListPhotoAlbumAuditEvents :many
SELECT e.id, e.album_id, e.action, e.actor_user_id, u.name AS actor_name, e.occurred_at
FROM photo_album_audit_events e
JOIN users u ON u.id = e.actor_user_id
WHERE e.album_id = sqlc.arg(album_id)
ORDER BY e.occurred_at, e.id;
