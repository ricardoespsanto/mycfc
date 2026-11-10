-- name: SeasonAdminAllowed :one
SELECT guardian_authority_is_administrator($1)::boolean;

-- name: ListManagedSeasons :many
SELECT id,code,name,starts_on,ends_on,is_current,created_at
FROM seasons WHERE guardian_authority_is_administrator(sqlc.arg(actor_id))
ORDER BY starts_on DESC,id LIMIT 20 OFFSET sqlc.arg(row_offset);

-- name: SeasonSelectionRows :many
SELECT id,code,name,starts_on,ends_on,is_current,created_at,xmin::text AS row_version
FROM seasons WHERE (id=sqlc.arg(target_id) OR is_current)
 AND guardian_authority_is_administrator(sqlc.arg(actor_id)) ORDER BY id;

-- name: CreateManagedSeason :one
INSERT INTO seasons(code,name,starts_on,ends_on,is_current)
SELECT sqlc.arg(code),sqlc.arg(name),sqlc.arg(starts_on),sqlc.arg(ends_on),false
WHERE guardian_authority_is_administrator(sqlc.arg(actor_id)) RETURNING *;

-- name: ClearManagedCurrentSeason :exec
UPDATE seasons SET is_current=false WHERE is_current
AND guardian_authority_is_administrator(sqlc.arg(actor_id));

-- name: SetManagedCurrentSeason :execrows
UPDATE seasons SET is_current=true WHERE id=sqlc.arg(target_id)
AND guardian_authority_is_administrator(sqlc.arg(actor_id));

-- name: SeasonReturnMemberAllowed :one
SELECT EXISTS(SELECT 1 FROM users u
 WHERE u.id=sqlc.arg(member_id) AND u.is_active AND u.erased_at IS NULL
 AND guardian_authority_is_administrator(sqlc.arg(actor_id))
 AND (NOT u.is_dependent OR EXISTS(SELECT 1 FROM guardian_authority_relationships rel
 WHERE rel.subject_user_id=u.id AND guardian_authority_current(rel.guardian_user_id,u.id))))::boolean;

-- name: SeasonSetupStatus :one
SELECT NOT EXISTS(SELECT 1 FROM seasons WHERE is_current)::boolean AS missing,
 guardian_authority_is_administrator(sqlc.arg(actor_id))::boolean AS administrator,
 EXISTS(SELECT 1 FROM users u WHERE u.id=sqlc.arg(actor_id) AND u.is_active AND u.erased_at IS NULL
 AND NOT u.is_dependent AND u.date_of_birth<=((now() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date
 AND (guardian_authority_is_administrator(u.id) OR EXISTS(SELECT 1 FROM staff_grants g
 WHERE g.user_id=u.id AND g.capability='COACH' AND g.revoked_at IS NULL)))::boolean AS allowed;

-- name: ManagedSeasonCurrentExists :one
SELECT EXISTS(SELECT 1 FROM seasons WHERE is_current AND guardian_authority_is_administrator(sqlc.arg(actor_id)))::boolean;
