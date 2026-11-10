-- #392/#393 dedicated group tasks. Exact scope authority is independent of athlete counts.

-- name: TrainingGroupScopeOptions :many
WITH actor AS (
 SELECT u.id, EXISTS(SELECT 1 FROM user_platform_roles r JOIN platform_roles p ON p.id=r.role_id WHERE r.user_id=u.id AND p.code='ADMIN') AS admin
 FROM users u WHERE u.id=sqlc.arg(actor_id)::uuid AND u.is_active AND u.erased_at IS NULL
 AND NOT u.is_dependent AND u.date_of_birth <= ((now() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date
)
SELECT p.id AS programme_id,t.id AS team_id,
 CASE WHEN p.id IS NOT NULL THEN p.name_pt ELSE t.name || ' — ' || tp.name_pt || ' (' || season.name || ')' END::text AS name
FROM (SELECT id AS programme_id,NULL::uuid AS team_id FROM programmes UNION ALL SELECT NULL::uuid,id FROM teams) choice
LEFT JOIN programmes p ON p.id=choice.programme_id LEFT JOIN teams t ON t.id=choice.team_id
LEFT JOIN programmes tp ON tp.id=t.programme_id LEFT JOIN seasons season ON season.id=t.season_id CROSS JOIN actor
WHERE actor.admin OR EXISTS(SELECT 1 FROM staff_grants grant_row WHERE grant_row.user_id=actor.id AND grant_row.capability='COACH' AND grant_row.revoked_at IS NULL
 AND ((choice.programme_id IS NOT NULL AND grant_row.programme_id=choice.programme_id) OR (choice.team_id IS NOT NULL AND grant_row.team_id=choice.team_id)))
ORDER BY 3;

-- name: TrainingGroupRecords :many
WITH actor AS (
 SELECT u.id, EXISTS(SELECT 1 FROM user_platform_roles r JOIN platform_roles p ON p.id=r.role_id WHERE r.user_id=u.id AND p.code='ADMIN') AS admin
 FROM users u WHERE u.id=sqlc.arg(actor_id)::uuid AND u.is_active AND u.erased_at IS NULL
 AND NOT u.is_dependent AND u.date_of_birth <= ((now() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date
)
SELECT g.id,g.name,g.programme_id,g.team_id,g.updated_at,
 COALESCE(latest_week.id::text,'')::text AS latest_week_id,
 COALESCE(p.name_pt,t.name || ' — ' || tp.name_pt)::text AS scope_name,
 (SELECT count(DISTINCT gm.subject_user_id) FROM training_group_placements gm JOIN user_memberships m ON m.user_id=gm.subject_user_id JOIN users u ON u.id=m.user_id WHERE gm.group_id=g.id AND gm.superseded_by_change_id IS NULL AND gm.cancelled_by_change_id IS NULL AND (clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date BETWEEN gm.starts_on AND COALESCE(gm.ends_on,'infinity'::date) AND u.is_active AND u.erased_at IS NULL AND (NOT u.is_dependent OR EXISTS(SELECT 1 FROM guardian_authority_relationships relationship WHERE relationship.subject_user_id=u.id AND guardian_authority_current(relationship.guardian_user_id,u.id)))
 AND m.starts_on <= (now() AT TIME ZONE 'Europe/Lisbon')::date AND (m.ends_on IS NULL OR m.ends_on >= (now() AT TIME ZONE 'Europe/Lisbon')::date)
 AND ((g.programme_id IS NOT NULL AND m.programme_id=g.programme_id) OR (g.team_id IS NOT NULL AND m.team_id=g.team_id)))::bigint AS member_count
FROM training_groups g LEFT JOIN programmes p ON p.id=g.programme_id LEFT JOIN teams t ON t.id=g.team_id LEFT JOIN programmes tp ON tp.id=t.programme_id LEFT JOIN LATERAL (SELECT plan.id FROM training_plans plan WHERE plan.training_group_id=g.id ORDER BY plan.week_start DESC,plan.id DESC LIMIT 1) latest_week ON true CROSS JOIN actor
WHERE (sqlc.narg(group_id)::uuid IS NULL OR g.id=sqlc.narg(group_id)::uuid) AND (actor.admin OR EXISTS(SELECT 1 FROM staff_grants grant_row WHERE grant_row.user_id=actor.id AND grant_row.capability='COACH' AND grant_row.revoked_at IS NULL AND
 ((g.programme_id IS NOT NULL AND grant_row.programme_id=g.programme_id) OR (g.team_id IS NOT NULL AND grant_row.team_id=g.team_id))))
ORDER BY g.name,g.id LIMIT sqlc.arg(row_limit)::integer OFFSET sqlc.arg(row_offset)::integer;

-- name: TrainingGroupAthleteChoices :many
WITH actor AS (
 SELECT u.id, EXISTS(SELECT 1 FROM user_platform_roles r JOIN platform_roles p ON p.id=r.role_id WHERE r.user_id=u.id AND p.code='ADMIN') AS admin
 FROM users u WHERE u.id=sqlc.arg(actor_id)::uuid AND u.is_active AND u.erased_at IS NULL
 AND NOT u.is_dependent AND u.date_of_birth <= ((now() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date
)
SELECT m.id AS membership_id,u.id AS user_id,u.name,
 EXISTS(SELECT 1 FROM training_group_placements gm WHERE gm.group_id=g.id AND gm.subject_user_id=m.user_id AND gm.superseded_by_change_id IS NULL AND gm.cancelled_by_change_id IS NULL AND (clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date BETWEEN gm.starts_on AND COALESCE(gm.ends_on,'infinity'::date)) AS already_member
FROM training_groups g JOIN user_memberships m ON true JOIN users u ON u.id=m.user_id CROSS JOIN actor
WHERE g.id=sqlc.arg(group_id)::uuid AND (actor.admin OR EXISTS(SELECT 1 FROM staff_grants grant_row WHERE grant_row.user_id=actor.id AND grant_row.capability='COACH' AND grant_row.revoked_at IS NULL AND
 ((g.programme_id IS NOT NULL AND grant_row.programme_id=g.programme_id) OR (g.team_id IS NOT NULL AND grant_row.team_id=g.team_id)))) AND u.is_active AND u.erased_at IS NULL AND (NOT u.is_dependent OR EXISTS(SELECT 1 FROM guardian_authority_relationships relationship WHERE relationship.subject_user_id=u.id AND guardian_authority_current(relationship.guardian_user_id,u.id)))
 AND m.starts_on <= (now() AT TIME ZONE 'Europe/Lisbon')::date AND (m.ends_on IS NULL OR m.ends_on >= (now() AT TIME ZONE 'Europe/Lisbon')::date)
 AND ((g.programme_id IS NOT NULL AND m.programme_id=g.programme_id) OR (g.team_id IS NOT NULL AND m.team_id=g.team_id))
 AND ((sqlc.arg(only_members)::boolean AND EXISTS(SELECT 1 FROM training_group_placements gm WHERE gm.group_id=g.id AND gm.subject_user_id=m.user_id AND gm.superseded_by_change_id IS NULL AND gm.cancelled_by_change_id IS NULL AND (clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date BETWEEN gm.starts_on AND COALESCE(gm.ends_on,'infinity'::date)))
 OR (NOT sqlc.arg(only_members)::boolean AND NOT EXISTS(SELECT 1 FROM training_group_placements other_member
 WHERE other_member.subject_user_id=u.id AND other_member.group_id<>g.id AND other_member.programme_id=m.programme_id
 AND other_member.superseded_by_change_id IS NULL AND other_member.cancelled_by_change_id IS NULL
 AND (clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date BETWEEN other_member.starts_on AND COALESCE(other_member.ends_on,'infinity'::date)) AND length(btrim(sqlc.arg(name_query)::text))>0 AND u.name ILIKE '%' || replace(replace(replace(sqlc.arg(name_query)::text,E'\\',E'\\\\'),'%','\%'),'_','\_') || '%' ESCAPE E'\\'))
ORDER BY u.name,u.id,m.id LIMIT 20 OFFSET sqlc.arg(row_offset)::integer;

-- name: InsertTaskTrainingGroup :one
INSERT INTO training_groups(name,programme_id,team_id,created_by_id)
VALUES(sqlc.arg(name),sqlc.narg(programme_id),sqlc.narg(team_id),sqlc.arg(actor_id)) RETURNING *;

-- name: RenameTaskTrainingGroup :exec
UPDATE training_groups SET name=sqlc.arg(name),updated_at=clock_timestamp() WHERE id=sqlc.arg(group_id);

-- name: TouchTaskTrainingGroup :exec
UPDATE training_groups SET updated_at=clock_timestamp() WHERE id=sqlc.arg(group_id);

-- name: AddTaskTrainingGroupAthlete :execrows
INSERT INTO training_group_members(group_id,membership_id,added_by_id)
VALUES(sqlc.arg(group_id),sqlc.arg(membership_id),sqlc.arg(actor_id)) ON CONFLICT DO NOTHING;

-- name: ValidateTaskTrainingGroupAthlete :one
WITH actor AS (
 SELECT u.id, EXISTS(SELECT 1 FROM user_platform_roles r JOIN platform_roles p ON p.id=r.role_id WHERE r.user_id=u.id AND p.code='ADMIN') AS admin
 FROM users u WHERE u.id=sqlc.arg(actor_id)::uuid AND u.is_active AND u.erased_at IS NULL
 AND NOT u.is_dependent AND u.date_of_birth <= ((now() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date
)
SELECT m.id FROM training_groups g JOIN user_memberships m ON m.id=sqlc.arg(membership_id)::uuid JOIN users u ON u.id=m.user_id CROSS JOIN actor
WHERE g.id=sqlc.arg(group_id)::uuid AND (actor.admin OR EXISTS(SELECT 1 FROM staff_grants grant_row WHERE grant_row.user_id=actor.id AND grant_row.capability='COACH' AND grant_row.revoked_at IS NULL AND
 ((g.programme_id IS NOT NULL AND grant_row.programme_id=g.programme_id) OR (g.team_id IS NOT NULL AND grant_row.team_id=g.team_id)))) AND u.is_active AND u.erased_at IS NULL AND (NOT u.is_dependent OR EXISTS(SELECT 1 FROM guardian_authority_relationships relationship WHERE relationship.subject_user_id=u.id AND guardian_authority_current(relationship.guardian_user_id,u.id)))
 AND m.starts_on <= (now() AT TIME ZONE 'Europe/Lisbon')::date AND (m.ends_on IS NULL OR m.ends_on >= (now() AT TIME ZONE 'Europe/Lisbon')::date)
 AND ((g.programme_id IS NOT NULL AND m.programme_id=g.programme_id) OR (g.team_id IS NOT NULL AND m.team_id=g.team_id)) AND (NOT EXISTS(SELECT 1 FROM training_group_placements other_member
 WHERE other_member.subject_user_id=u.id AND other_member.group_id<>g.id AND other_member.programme_id=m.programme_id
 AND other_member.superseded_by_change_id IS NULL AND other_member.cancelled_by_change_id IS NULL
 AND (clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date BETWEEN other_member.starts_on AND COALESCE(other_member.ends_on,'infinity'::date)) OR EXISTS(SELECT 1 FROM training_group_placements gm WHERE gm.group_id=g.id AND gm.subject_user_id=m.user_id AND gm.superseded_by_change_id IS NULL AND gm.cancelled_by_change_id IS NULL AND (clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date BETWEEN gm.starts_on AND COALESCE(gm.ends_on,'infinity'::date)))
FOR SHARE OF m;

-- name: TrainingGroupAllowsWeek :one
WITH actor AS (
 SELECT u.id, EXISTS(SELECT 1 FROM user_platform_roles r JOIN platform_roles p ON p.id=r.role_id WHERE r.user_id=u.id AND p.code='ADMIN') AS admin
 FROM users u WHERE u.id=sqlc.arg(actor_id)::uuid AND u.is_active AND u.erased_at IS NULL
 AND NOT u.is_dependent AND u.date_of_birth <= ((now() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date
)
SELECT EXISTS(SELECT 1 FROM training_groups g CROSS JOIN actor WHERE g.id=sqlc.arg(group_id)::uuid AND (actor.admin OR EXISTS(SELECT 1 FROM staff_grants grant_row WHERE grant_row.user_id=actor.id AND grant_row.capability='COACH' AND grant_row.revoked_at IS NULL AND
 ((g.programme_id IS NOT NULL AND grant_row.programme_id=g.programme_id) OR (g.team_id IS NOT NULL AND grant_row.team_id=g.team_id)))) AND EXISTS(SELECT 1 FROM training_plans plan WHERE plan.training_group_id=g.id AND plan.id=sqlc.arg(week_id)::uuid));
