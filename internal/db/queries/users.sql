-- name: CreateAdultUser :one
INSERT INTO users (
    name,
    email,
    password_hash,
    role,
    squad_category,
    guardian_id,
    is_dependent,
    date_of_birth
) VALUES (
    sqlc.arg(name),
    sqlc.arg(email),
    sqlc.arg(password_hash),
    sqlc.arg(role),
    sqlc.arg(squad_category),
    NULL,
    false,
    sqlc.arg(date_of_birth)
)
RETURNING id, name, email, password_hash, role, squad_category, guardian_id,
          is_dependent, date_of_birth, is_active, created_at, updated_at;

-- name: CreateDependentUser :one
INSERT INTO users (
    name,
    email,
    password_hash,
    role,
    squad_category,
    guardian_id,
    is_dependent,
    date_of_birth
) VALUES (
    sqlc.arg(name),
    NULL,
    NULL,
    sqlc.arg(role),
    sqlc.arg(squad_category),
    sqlc.arg(guardian_id),
    true,
    sqlc.arg(date_of_birth)
)
RETURNING id, name, email, password_hash, role, squad_category, guardian_id,
          is_dependent, date_of_birth, is_active, created_at, updated_at;

-- name: GetUserByID :one
SELECT id, name, email, password_hash, role, squad_category, guardian_id,
       is_dependent, date_of_birth, is_active, created_at, updated_at
FROM users
WHERE id = sqlc.arg(id);

-- name: GetActiveUserByEmail :one
SELECT id, name, email, password_hash, role, squad_category, guardian_id,
       is_dependent, date_of_birth, is_active, created_at, updated_at
FROM users
WHERE email = sqlc.arg(email)
  AND is_active = true
   AND is_dependent = false;

-- name: GetUserByEmail :one
SELECT id, name, email, password_hash, role, squad_category, guardian_id,
       is_dependent, date_of_birth, is_active, created_at, updated_at
FROM users
WHERE email = sqlc.arg(email)
  AND is_dependent = false;

-- name: ListDependentsByGuardian :many
SELECT id, name, role, squad_category, guardian_id, is_dependent,
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

-- name: LockActiveGuardian :one
SELECT id
FROM users
WHERE id = sqlc.arg(id)
  AND role = 'Guardian'
  AND is_active = true
  AND is_dependent = false
FOR UPDATE;

-- name: SetUserPasswordHash :exec
UPDATE users
SET password_hash = sqlc.arg(password_hash),
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND is_dependent = false;

-- name: DeactivateUser :exec
UPDATE users
SET is_active = false,
    updated_at = now()
WHERE id = sqlc.arg(id);
