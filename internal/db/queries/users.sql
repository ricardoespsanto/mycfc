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
SELECT id, name, email, password_hash, guardian_id,
       is_dependent, date_of_birth, is_active, created_at, updated_at
FROM users
WHERE email = sqlc.arg(email)
  AND is_dependent = false;

-- name: ListDependentsByGuardian :many
SELECT id, name, guardian_id, is_dependent,
       date_of_birth, is_active, created_at, updated_at
FROM users
WHERE guardian_id = sqlc.arg(guardian_id)
  AND is_dependent = true
  AND is_active = true
ORDER BY lower(name), id
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

-- name: GetActiveAccountByID :one
SELECT u.id, u.name, u.is_dependent, u.is_active,
       EXISTS (
           SELECT 1
           FROM user_platform_roles assignment
           JOIN platform_roles role ON role.id = assignment.role_id
           WHERE assignment.user_id = u.id AND role.code = 'ADMIN'
       ) AS is_admin
FROM users u
WHERE u.id = sqlc.arg(id);

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
