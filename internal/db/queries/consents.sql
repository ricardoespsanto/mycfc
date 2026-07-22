-- name: CreateConsentForm :one
INSERT INTO consent_forms (
    user_id,
    granted_by_user_id,
    consent_type,
    document_version,
    document_sha256,
    is_accepted,
    ip_address,
    user_agent
) VALUES (
    sqlc.arg(user_id),
    sqlc.narg(granted_by_user_id),
    sqlc.arg(consent_type),
    sqlc.arg(document_version),
    sqlc.arg(document_sha256),
    true,
    sqlc.narg(ip_address),
    sqlc.arg(user_agent)
)
RETURNING id, user_id, granted_by_user_id, consent_type, document_version,
          document_sha256, is_accepted, date_signed, ip_address, user_agent;

-- name: ListConsentFormsForUser :many
SELECT id, user_id, granted_by_user_id, consent_type, document_version,
       document_sha256, is_accepted, date_signed, ip_address, user_agent
FROM consent_forms
WHERE user_id = sqlc.arg(user_id)
ORDER BY date_signed DESC, id DESC
LIMIT sqlc.arg(row_limit);

-- name: HasConsentVersion :one
SELECT EXISTS (
    SELECT 1
    FROM consent_forms
    WHERE user_id = sqlc.arg(user_id)
      AND consent_type = sqlc.arg(consent_type)
      AND document_version = sqlc.arg(document_version)
      AND is_accepted = true
)::boolean;
