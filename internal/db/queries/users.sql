-- name: CreateAdultUser :one
INSERT INTO users (
    name,
    email,
    password_hash,
    guardian_id,
    is_dependent,
    date_of_birth
) VALUES (
    sqlc.arg(name),
    sqlc.arg(email),
    sqlc.arg(password_hash),
    NULL,
    false,
    sqlc.arg(date_of_birth)
)
RETURNING id, name, email, password_hash, guardian_id,
          is_dependent, date_of_birth, is_active, created_at, updated_at;

-- name: CreateDependentUser :one
INSERT INTO users (
    name,
    email,
    password_hash,
    guardian_id,
    is_dependent,
    date_of_birth
) VALUES (
    sqlc.arg(name),
    NULL,
    NULL,
    sqlc.arg(guardian_id),
    true,
    sqlc.arg(date_of_birth)
)
RETURNING id, name, email, password_hash, guardian_id,
          is_dependent, date_of_birth, is_active, created_at, updated_at;

-- name: GetUserByID :one
SELECT id, name, email, password_hash, guardian_id,
       is_dependent, date_of_birth, is_active, created_at, updated_at
FROM users
WHERE id = sqlc.arg(id);

-- name: GetActiveUserByEmail :one
SELECT id, name, email, password_hash, guardian_id,
       is_dependent, date_of_birth, is_active, created_at, updated_at
FROM users
WHERE email = sqlc.arg(email)
  AND is_active = true
   AND is_dependent = false;

-- name: GetUserByEmail :one
SELECT id, name, email, minor_login_id, password_hash, guardian_id,
       is_dependent, date_of_birth, is_active, created_at, updated_at
FROM users
WHERE email = sqlc.arg(email)
  AND is_dependent = false;

-- name: ListDependentsByGuardian :many
SELECT u.id, u.name, u.guardian_id, u.is_dependent,
       u.date_of_birth, u.is_active, u.leaderboard_visible, u.created_at, u.updated_at, u.minor_login_id,
       COALESCE(p.emergency_contact_name <> '' AND p.emergency_contact_relationship <> '' AND p.emergency_contact_phone <> '' AND p.medical_declaration <> 'UNKNOWN', false)::boolean AS profile_complete,
       COALESCE(p.photo_object_key IS NOT NULL, false)::boolean AS has_profile_photo
FROM users u
LEFT JOIN member_profiles p ON p.user_id = u.id
WHERE u.guardian_id = sqlc.arg(guardian_id)
  AND u.is_dependent = true
  AND u.is_active = true
ORDER BY lower(u.name), u.id
LIMIT sqlc.arg(row_limit);

-- name: CountDependentsByGuardian :one
SELECT count(*)::bigint
FROM users
WHERE guardian_id = sqlc.arg(guardian_id)
  AND is_dependent = true
  AND is_active = true;

-- name: LockActiveAdult :one
SELECT id
FROM users
WHERE id = sqlc.arg(id)
  AND is_active = true
  AND is_dependent = false
FOR UPDATE;

-- name: SetUserPasswordHash :exec
UPDATE users
SET password_hash = sqlc.arg(password_hash),
    updated_at = now()
WHERE id = sqlc.arg(id)
   AND is_dependent = false;

-- name: GetActiveDependentByLoginID :one
SELECT u.id, u.name, u.email, u.password_hash, u.guardian_id,
       u.is_dependent, u.date_of_birth, u.is_active, u.created_at, u.updated_at
FROM users u
JOIN users guardian ON guardian.id = u.guardian_id AND guardian.is_active = true AND guardian.is_dependent = false
WHERE u.minor_login_id = sqlc.arg(minor_login_id)
  AND u.is_active = true
  AND u.is_dependent = true;

-- name: IssueMinorCredential :one
WITH issued AS (
    UPDATE users minor
    SET minor_login_id = sqlc.arg(minor_login_id), password_hash = sqlc.arg(password_hash), updated_at = now()
    FROM users guardian
    WHERE minor.id = sqlc.arg(minor_user_id)
      AND minor.is_dependent = true
      AND minor.is_active = true
      AND minor.guardian_id = guardian.id
      AND guardian.id = sqlc.arg(guardian_user_id)
      AND guardian.is_active = true
      AND guardian.is_dependent = false
      AND EXISTS (
          SELECT 1 FROM user_platform_roles assignment
          JOIN platform_roles role ON role.id = assignment.role_id
          WHERE assignment.user_id = sqlc.arg(actor_user_id) AND role.code = 'ADMIN'
      )
    RETURNING minor.id
), audited AS (
    INSERT INTO minor_credential_audit (minor_user_id, guardian_user_id, actor_user_id, action, issued_login_id)
    SELECT id, sqlc.arg(guardian_user_id), sqlc.arg(actor_user_id), sqlc.arg(action), sqlc.arg(minor_login_id)
    FROM issued
    RETURNING minor_user_id
)
SELECT minor_user_id FROM audited;

-- name: GetActiveAccountByID :one
SELECT u.id, u.name, u.is_dependent, u.is_active, u.leaderboard_visible,
       EXISTS (
           SELECT 1
           FROM user_platform_roles assignment
           JOIN platform_roles role ON role.id = assignment.role_id
           WHERE assignment.user_id = u.id AND role.code = 'ADMIN'
       ) AS is_admin,
       COALESCE(p.emergency_contact_name <> '' AND p.emergency_contact_relationship <> '' AND p.emergency_contact_phone <> '' AND p.medical_declaration <> 'UNKNOWN', false)::boolean AS profile_complete
FROM users u
LEFT JOIN member_profiles p ON p.user_id = u.id
WHERE u.id = sqlc.arg(id);

-- name: GetActiveAccountByIDWithoutProfile :one
SELECT u.id, u.name, u.is_dependent, u.is_active, u.leaderboard_visible,
       EXISTS (
           SELECT 1
           FROM user_platform_roles assignment
           JOIN platform_roles role ON role.id = assignment.role_id
           WHERE assignment.user_id = u.id AND role.code = 'ADMIN'
       ) AS is_admin
FROM users u
WHERE u.id = sqlc.arg(id);

-- name: UpdateOwnLeaderboardVisibility :execrows
UPDATE users
SET leaderboard_visible = sqlc.arg(leaderboard_visible), updated_at = now()
WHERE id = sqlc.arg(user_id)
  AND is_active = true;

-- name: UpdateDependentLeaderboardVisibility :execrows
UPDATE users
SET leaderboard_visible = sqlc.arg(leaderboard_visible), updated_at = now()
WHERE id = sqlc.arg(dependent_user_id)
  AND guardian_id = sqlc.arg(guardian_user_id)
  AND is_dependent = true
  AND is_active = true;

-- name: GetAccountByEmail :one
SELECT u.id, u.name, u.is_dependent, u.is_active,
       EXISTS (
           SELECT 1
           FROM user_platform_roles assignment
           JOIN platform_roles role ON role.id = assignment.role_id
           WHERE assignment.user_id = u.id AND role.code = 'ADMIN'
       ) AS is_admin
FROM users u
WHERE u.email = sqlc.arg(email)
  AND u.is_dependent = false;

-- name: GrantPlatformRoleByCode :exec
INSERT INTO user_platform_roles (user_id, role_id)
SELECT sqlc.arg(user_id), id
FROM platform_roles
WHERE code = sqlc.arg(role_code)
ON CONFLICT DO NOTHING;

-- name: DeactivateUser :exec
UPDATE users
SET is_active = false,
    updated_at = now()
WHERE id = sqlc.arg(id);

-- name: ListMembersForAdmin :many
SELECT u.id, u.name, u.email, u.minor_login_id, u.guardian_id, guardian.name AS guardian_name,
       u.is_dependent, u.date_of_birth, u.is_active
FROM users u
LEFT JOIN users guardian ON guardian.id = u.guardian_id
WHERE sqlc.narg(search)::text IS NULL
   OR u.name ILIKE '%' || sqlc.narg(search)::text || '%'
   OR u.email::text ILIKE '%' || sqlc.narg(search)::text || '%'
   OR u.minor_login_id::text ILIKE '%' || sqlc.narg(search)::text || '%'
ORDER BY u.is_active DESC, lower(u.name), u.id
LIMIT sqlc.arg(row_limit)
OFFSET sqlc.arg(row_offset);

-- name: GetMemberForAdmin :one
SELECT u.id, u.name, u.email, u.minor_login_id, u.guardian_id, guardian.name AS guardian_name,
       u.is_dependent, u.date_of_birth, u.is_active
FROM users u
LEFT JOIN users guardian ON guardian.id = u.guardian_id
WHERE u.id = sqlc.arg(id);

-- name: ListActiveAdultsForAdmin :many
SELECT id, name, email FROM users
WHERE is_active = true AND is_dependent = false
ORDER BY lower(name), id
LIMIT sqlc.arg(row_limit);
