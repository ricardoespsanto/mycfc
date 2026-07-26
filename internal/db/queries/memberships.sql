-- name: GetProgrammeByCode :one
SELECT id, code, name_pt, created_at
FROM programmes
WHERE code = sqlc.arg(code);

-- name: GetTeamByID :one
SELECT id, season_id, programme_id, code, name, created_at
FROM teams
WHERE id = sqlc.arg(id);

-- name: GetModalityByCode :one
SELECT id, code, name_pt, created_at
FROM modalities
WHERE code = sqlc.arg(code);

-- name: CreateSeason :one
INSERT INTO seasons (code, name, starts_on, ends_on, is_current)
VALUES (sqlc.arg(code), sqlc.arg(name), sqlc.arg(starts_on), sqlc.arg(ends_on), sqlc.arg(is_current))
RETURNING id, code, name, starts_on, ends_on, is_current, created_at;

-- name: CreateCompetitionCategory :one
INSERT INTO competition_categories (
    season_id, programme_id, code, name_pt, birth_date_from, birth_date_to, approved_by_user_id, approved_at
) VALUES (
    sqlc.arg(season_id), sqlc.arg(programme_id), sqlc.arg(code), sqlc.arg(name_pt),
    sqlc.narg(birth_date_from), sqlc.narg(birth_date_to), sqlc.arg(approved_by_user_id), sqlc.arg(approved_at)
)
RETURNING id, season_id, programme_id, code, name_pt, birth_date_from, birth_date_to,
          approved_by_user_id, approved_at, created_at;

-- name: CreateTeam :one
INSERT INTO teams (season_id, programme_id, code, name)
VALUES (sqlc.arg(season_id), sqlc.arg(programme_id), sqlc.arg(code), sqlc.arg(name))
RETURNING id, season_id, programme_id, code, name, created_at;

-- name: CreateUserMembership :one
INSERT INTO user_memberships (
    user_id, season_id, programme_id, team_id, competition_category_id, starts_on, ends_on
) VALUES (
    sqlc.arg(user_id), sqlc.arg(season_id), sqlc.arg(programme_id), sqlc.narg(team_id),
    sqlc.narg(competition_category_id), sqlc.arg(starts_on), sqlc.narg(ends_on)
)
RETURNING id, user_id, season_id, programme_id, team_id, competition_category_id,
          starts_on, ends_on, created_at, updated_at;

-- name: AddMembershipModality :exec
INSERT INTO membership_modalities (membership_id, modality_id)
VALUES (sqlc.arg(membership_id), sqlc.arg(modality_id));

-- name: ListActiveMembershipsForUser :many
SELECT
    membership.id,
    membership.season_id,
    season.code AS season_code,
    membership.programme_id,
    programme.code AS programme_code,
    programme.name_pt AS programme_name_pt,
    membership.team_id,
    team.code AS team_code,
    team.name AS team_name,
    membership.competition_category_id,
    category.code AS competition_category_code,
    category.name_pt AS competition_category_name_pt,
    membership.starts_on,
    membership.ends_on
FROM user_memberships membership
JOIN seasons season ON season.id = membership.season_id
JOIN programmes programme ON programme.id = membership.programme_id
LEFT JOIN teams team ON team.id = membership.team_id
LEFT JOIN competition_categories category ON category.id = membership.competition_category_id
WHERE membership.user_id = sqlc.arg(user_id)
  AND membership.starts_on <= CURRENT_DATE
  AND (membership.ends_on IS NULL OR membership.ends_on >= CURRENT_DATE)
ORDER BY season.starts_on DESC, programme.name_pt, team.name NULLS FIRST;

-- name: ListActiveMembershipProgrammeCodesForUser :many
SELECT programme.code
FROM user_memberships membership
JOIN programmes programme ON programme.id = membership.programme_id
WHERE membership.user_id = sqlc.arg(user_id)
  AND membership.starts_on <= CURRENT_DATE
  AND (membership.ends_on IS NULL OR membership.ends_on >= CURRENT_DATE)
ORDER BY programme.code;

-- name: ListModalitiesForMembership :many
SELECT modality.id, modality.code, modality.name_pt, modality.created_at
FROM membership_modalities assignment
JOIN modalities modality ON modality.id = assignment.modality_id
WHERE assignment.membership_id = sqlc.arg(membership_id)
ORDER BY modality.code;
