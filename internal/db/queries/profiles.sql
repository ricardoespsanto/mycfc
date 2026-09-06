-- name: EnsureMemberProfile :exec
INSERT INTO member_profiles (user_id)
SELECT id FROM users WHERE id = sqlc.arg(user_id)
ON CONFLICT (user_id) DO NOTHING;

-- name: GetMemberProfile :one
SELECT u.id, u.name, u.email, u.email_verified_at, u.minor_login_id, u.guardian_id, u.is_dependent,
       u.date_of_birth, u.is_active, u.updated_at AS identity_updated_at,
       p.phone, p.address_line1, p.address_line2, p.postcode, p.locality,
       p.country_code, p.nationality_code, p.club_member_number,
       p.federation_licence_number, p.emergency_contact_name,
       p.emergency_contact_relationship, p.emergency_contact_phone,
       p.emergency_contact_alternate_phone, p.medical_declaration,
       p.allergies, p.medical_conditions, p.medication,
       p.activity_restrictions, p.medical_notes, p.photo_object_key,
       p.photo_content_type, p.photo_size_bytes, p.photo_consent_form_id,
       p.created_at, p.updated_at
FROM users u
JOIN member_profiles p ON p.user_id = u.id
WHERE u.id = sqlc.arg(user_id);

-- name: UpdateMemberProfile :one
UPDATE member_profiles SET
    phone = sqlc.arg(phone), address_line1 = sqlc.arg(address_line1),
    address_line2 = sqlc.arg(address_line2), postcode = sqlc.arg(postcode),
    locality = sqlc.arg(locality), country_code = sqlc.arg(country_code),
    nationality_code = sqlc.arg(nationality_code),
    club_member_number = sqlc.narg(club_member_number),
    federation_licence_number = sqlc.narg(federation_licence_number),
    emergency_contact_name = sqlc.arg(emergency_contact_name),
    emergency_contact_relationship = sqlc.arg(emergency_contact_relationship),
    emergency_contact_phone = sqlc.arg(emergency_contact_phone),
    emergency_contact_alternate_phone = sqlc.arg(emergency_contact_alternate_phone),
    medical_declaration = sqlc.arg(medical_declaration),
    allergies = sqlc.arg(allergies), medical_conditions = sqlc.arg(medical_conditions),
    medication = sqlc.arg(medication), activity_restrictions = sqlc.arg(activity_restrictions),
    medical_notes = sqlc.arg(medical_notes), updated_at = clock_timestamp()
WHERE user_id = sqlc.arg(user_id) AND updated_at = sqlc.arg(expected_updated_at)
RETURNING user_id, phone, address_line1, address_line2, postcode, locality,
          country_code, nationality_code, club_member_number, federation_licence_number,
          emergency_contact_name, emergency_contact_relationship, emergency_contact_phone,
          emergency_contact_alternate_phone, medical_declaration, allergies,
          medical_conditions, medication, activity_restrictions, medical_notes,
          photo_object_key, photo_content_type, photo_size_bytes, photo_consent_form_id,
          created_at, updated_at;

-- name: UpdateMemberIdentity :one
UPDATE users SET name = sqlc.arg(name), email = sqlc.narg(email),
    email_verified_at = CASE WHEN email IS DISTINCT FROM sqlc.narg(email) THEN NULL ELSE email_verified_at END,
    date_of_birth = sqlc.arg(date_of_birth), updated_at = clock_timestamp()
WHERE id = sqlc.arg(user_id) AND updated_at = sqlc.arg(expected_updated_at)
RETURNING updated_at;

-- name: CreateMemberProfileAudit :one
INSERT INTO member_profile_audit_events (actor_user_id, subject_user_id, action, changed_fields)
VALUES (sqlc.arg(actor_user_id), sqlc.arg(subject_user_id), sqlc.arg(action), sqlc.arg(changed_fields))
RETURNING id;

-- name: UpdateMemberProfilePhoto :one
UPDATE member_profiles SET photo_object_key = sqlc.arg(photo_object_key),
    photo_content_type = sqlc.arg(photo_content_type), photo_size_bytes = sqlc.arg(photo_size_bytes),
    photo_consent_form_id = sqlc.arg(photo_consent_form_id), updated_at = clock_timestamp()
WHERE user_id = sqlc.arg(user_id)
RETURNING updated_at;

-- name: ClearMemberProfilePhoto :one
UPDATE member_profiles SET photo_object_key = NULL, photo_content_type = NULL,
    photo_size_bytes = NULL, photo_consent_form_id = NULL, updated_at = clock_timestamp()
WHERE user_id = sqlc.arg(user_id) AND photo_object_key IS NOT NULL
RETURNING updated_at;

-- name: GetMemberAvatar :one
SELECT u.name,
       p.photo_object_key, p.photo_content_type, p.photo_size_bytes,
       (c.id IS NOT NULL)::boolean AS consent_current
FROM users u
LEFT JOIN member_profiles p ON p.user_id = u.id
LEFT JOIN consent_forms c ON c.id = p.photo_consent_form_id
  AND c.user_id = u.id AND c.consent_type = 'Foto_Perfil' AND c.is_accepted = true
  AND c.document_version = sqlc.arg(document_version)
  AND c.document_sha256 = sqlc.arg(document_sha256)
WHERE u.id = sqlc.arg(user_id)
  AND (u.is_active OR sqlc.arg(is_admin)::boolean)
;

-- name: ListDependentProfileCompleteness :many
SELECT u.id,
       (p.emergency_contact_name <> '' AND p.emergency_contact_relationship <> '' AND p.emergency_contact_phone <> '' AND p.medical_declaration <> 'UNKNOWN')::boolean AS is_complete,
       (p.photo_object_key IS NOT NULL)::boolean AS has_photo
FROM users u
LEFT JOIN member_profiles p ON p.user_id = u.id
WHERE u.guardian_id = sqlc.arg(guardian_id) AND u.is_active AND u.is_dependent
ORDER BY lower(u.name), u.id;
