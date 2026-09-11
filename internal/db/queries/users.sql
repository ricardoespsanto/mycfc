-- name: CreateAdultUser :one
WITH account AS (
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
RETURNING id, name, email, password_hash, credential_version, guardian_id,
          is_dependent, date_of_birth, is_active, created_at, updated_at
), token AS (
    INSERT INTO email_verification_tokens (user_id, email, expires_at)
    SELECT id, email, now() + interval '24 hours' FROM account
    RETURNING id
), queued AS (
    INSERT INTO email_outbox (verification_token_id)
    SELECT id FROM token
)
SELECT id, name, email, password_hash, credential_version, guardian_id,
       is_dependent, date_of_birth, is_active, created_at, updated_at
FROM account;

-- name: CreateDependentUser :one
SELECT account.id,account.name,account.email,account.password_hash,
       sqlc.arg(guardian_id)::uuid AS guardian_id,account.is_dependent,
       account.date_of_birth,account.is_active,account.created_at,account.updated_at
FROM guardian_authority_create_dependent(sqlc.arg(name),sqlc.arg(date_of_birth),sqlc.arg(guardian_id)) account;

-- name: GetUserByID :one
SELECT id, name, email, password_hash, guardian_id,
       is_dependent, date_of_birth, is_active, created_at, updated_at, credential_version
FROM users
WHERE id = sqlc.arg(id);

-- name: GetActiveUserByEmail :one
SELECT id, name, email, password_hash, guardian_id,
       is_dependent, date_of_birth, is_active, created_at, updated_at, credential_version
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
       true::boolean AS profile_complete,
       false::boolean AS has_profile_photo
FROM users u
JOIN guardian_authority_relationships relationship ON relationship.subject_user_id=u.id
WHERE relationship.guardian_user_id = sqlc.narg(guardian_id)::uuid
  AND guardian_authority_current(relationship.guardian_user_id,u.id)
  AND u.is_dependent = true
  AND u.is_active = true
ORDER BY lower(u.name), u.id
LIMIT sqlc.arg(row_limit);

-- name: CountDependentsByGuardian :one
SELECT count(*)::bigint
FROM guardian_authority_relationships relationship
JOIN users subject ON subject.id=relationship.subject_user_id
WHERE relationship.guardian_user_id=sqlc.narg(guardian_id)::uuid
  AND relationship.state NOT IN('EXPIRED','REJECTED')
  AND subject.is_dependent AND subject.is_active AND subject.erased_at IS NULL
  AND subject.date_of_birth>((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-INTERVAL '18 years')::date;

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
    credential_version = credential_version + 1,
    updated_at = now()
WHERE id = sqlc.arg(id)
   AND is_dependent = false;

-- name: GetActiveDependentByLoginID :one
SELECT u.id, u.name, u.email, u.password_hash, u.guardian_id,
       u.is_dependent, u.date_of_birth, u.is_active, u.created_at, u.updated_at, u.credential_version
FROM users u
WHERE u.minor_login_id = sqlc.arg(minor_login_id)
  AND u.is_active = true
  AND u.erased_at IS NULL
  AND u.is_dependent = true
  AND u.date_of_birth>((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-INTERVAL '18 years')::date
  AND EXISTS (SELECT 1 FROM guardian_authority_relationships relationship
      WHERE relationship.subject_user_id=u.id
        AND guardian_authority_current(relationship.guardian_user_id,u.id));

-- name: IssueMinorCredential :one
WITH issued AS (
    UPDATE users minor
    SET minor_login_id = sqlc.arg(minor_login_id), password_hash = sqlc.arg(password_hash),
        credential_version = minor.credential_version + 1, updated_at = now()
    FROM guardian_authority_relationships relationship
    JOIN users guardian ON guardian.id=relationship.guardian_user_id
    WHERE minor.id = sqlc.arg(minor_user_id)
      AND minor.is_dependent = true
      AND minor.is_active = true
      AND minor.erased_at IS NULL
      AND relationship.subject_user_id=minor.id
      AND guardian.id = sqlc.arg(guardian_user_id)
      AND guardian_authority_current(guardian.id,minor.id)
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
    RETURNING id
)
SELECT issued.id FROM issued
WHERE EXISTS (SELECT 1 FROM audited);

-- name: GetActiveAccountByID :one
SELECT u.id, u.name, u.email, u.is_dependent, u.is_active, u.leaderboard_visible, (u.email_verified_at IS NOT NULL)::boolean AS email_verified,
       u.credential_version,
       EXISTS (
           SELECT 1
           FROM user_platform_roles assignment
           JOIN platform_roles role ON role.id = assignment.role_id
           WHERE assignment.user_id = u.id AND role.code = 'ADMIN'
       ) AS is_admin,
       COALESCE(p.emergency_contact_name <> '' AND p.emergency_contact_relationship <> '' AND p.emergency_contact_phone <> '' AND p.medical_declaration <> 'UNKNOWN', false)::boolean AS profile_complete
FROM users u
LEFT JOIN member_profiles p ON p.user_id = u.id
WHERE u.id = sqlc.arg(id)
  AND (NOT u.is_dependent OR (u.is_active AND u.erased_at IS NULL
       AND u.date_of_birth>((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-INTERVAL '18 years')::date
       AND EXISTS (SELECT 1 FROM guardian_authority_relationships relationship
           WHERE relationship.subject_user_id=u.id
             AND guardian_authority_current(relationship.guardian_user_id,u.id))));

-- name: GetActiveAccountByIDWithoutProfile :one
SELECT u.id, u.name, u.email, u.is_dependent, u.is_active, u.leaderboard_visible, (u.email_verified_at IS NOT NULL)::boolean AS email_verified,
       u.credential_version,
       EXISTS (
           SELECT 1
           FROM user_platform_roles assignment
           JOIN platform_roles role ON role.id = assignment.role_id
           WHERE assignment.user_id = u.id AND role.code = 'ADMIN'
       ) AS is_admin
FROM users u
WHERE u.id = sqlc.arg(id)
  AND (NOT u.is_dependent OR (u.is_active AND u.erased_at IS NULL
       AND u.date_of_birth>((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-INTERVAL '18 years')::date
       AND EXISTS (SELECT 1 FROM guardian_authority_relationships relationship
           WHERE relationship.subject_user_id=u.id
             AND guardian_authority_current(relationship.guardian_user_id,u.id))));

-- name: UpdateOwnLeaderboardVisibility :execrows
UPDATE users
SET leaderboard_visible = sqlc.arg(leaderboard_visible), updated_at = now()
WHERE id = sqlc.arg(user_id)
  AND is_active = true;

-- name: UpdateDependentLeaderboardVisibility :execrows
UPDATE users
SET leaderboard_visible = sqlc.arg(leaderboard_visible), updated_at = now()
WHERE id = sqlc.arg(dependent_user_id)
  AND guardian_authority_current(sqlc.narg(guardian_user_id)::uuid,id)
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

-- name: DeactivateMemberForAdmin :execrows
UPDATE users AS target
SET is_active = false,
    updated_at = now()
WHERE target.id = sqlc.arg(id)
  AND (NOT target.is_dependent OR EXISTS(
    SELECT 1 FROM guardian_authority_relationships relationship
    WHERE relationship.subject_user_id=target.id
      AND guardian_authority_current(relationship.guardian_user_id,target.id)));

-- name: ListMembersForAdmin :many
SELECT u.id, u.name, u.email, u.minor_login_id, relationship.guardian_user_id AS guardian_id, guardian.name AS guardian_name,
       u.is_dependent, u.date_of_birth, u.is_active
FROM users u
LEFT JOIN guardian_authority_relationships relationship ON relationship.subject_user_id=u.id
LEFT JOIN users guardian ON guardian.id = relationship.guardian_user_id
WHERE (NOT u.is_dependent OR guardian_authority_current(relationship.guardian_user_id,u.id))
 AND (sqlc.narg(search)::text IS NULL
   OR u.name ILIKE '%' || sqlc.narg(search)::text || '%'
   OR u.email::text ILIKE '%' || sqlc.narg(search)::text || '%'
   OR u.minor_login_id::text ILIKE '%' || sqlc.narg(search)::text || '%')
ORDER BY u.is_active DESC, lower(u.name), u.id
LIMIT sqlc.arg(row_limit)
OFFSET sqlc.arg(row_offset);

-- name: GetMemberForAdmin :one
SELECT u.id, u.name, u.email, u.minor_login_id, relationship.guardian_user_id AS guardian_id, guardian.name AS guardian_name,
       u.is_dependent, u.date_of_birth, u.is_active
FROM users u
LEFT JOIN guardian_authority_relationships relationship ON relationship.subject_user_id=u.id
LEFT JOIN users guardian ON guardian.id = relationship.guardian_user_id
WHERE u.id = sqlc.arg(id)
 AND (NOT u.is_dependent OR guardian_authority_current(relationship.guardian_user_id,u.id));

-- name: ListActiveAdultsForAdmin :many
SELECT id, name, email FROM users
WHERE is_active = true AND is_dependent = false
ORDER BY lower(name), id
LIMIT sqlc.arg(row_limit);
