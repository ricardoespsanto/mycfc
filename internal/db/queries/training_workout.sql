-- name: GetFocusedWaterCreateContext :one
WITH actor AS (
 SELECT u.id,EXISTS(SELECT 1 FROM user_platform_roles r JOIN platform_roles p ON p.id=r.role_id WHERE r.user_id=u.id AND p.code='ADMIN') AS administrator
 FROM users u WHERE u.id=sqlc.arg(actor_id)::uuid AND u.is_active AND u.erased_at IS NULL AND NOT u.is_dependent
 AND u.date_of_birth<=((now() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date
), scoped_plan AS (
 SELECT plan.* FROM training_plans plan JOIN training_groups group_row ON group_row.id=plan.training_group_id
 JOIN seasons season ON season.id=plan.season_id CROSS JOIN actor
 WHERE plan.id=sqlc.arg(plan_id)::uuid AND (actor.administrator OR EXISTS(SELECT 1 FROM staff_grants grant_row
 WHERE grant_row.user_id=actor.id AND grant_row.capability='COACH' AND grant_row.revoked_at IS NULL
 AND ((group_row.programme_id IS NOT NULL AND grant_row.programme_id=group_row.programme_id)
 OR (group_row.team_id IS NOT NULL AND grant_row.team_id=group_row.team_id))))
)
SELECT plan.id AS plan_id,group_row.id AS group_id,season.id AS season_id,plan.week_start,plan.updated_at AS plan_updated_at,plan.title AS plan_title,group_row.name AS group_name,sqlc.arg(day)::date AS selected_day FROM scoped_plan plan JOIN training_groups group_row ON group_row.id=plan.training_group_id JOIN seasons season ON season.id=plan.season_id WHERE sqlc.arg(day)::date>=plan.week_start AND sqlc.arg(day)::date<plan.week_start+7;

-- name: LockFocusedWaterCreatePlan :one
WITH actor AS (
 SELECT u.id,EXISTS(SELECT 1 FROM user_platform_roles r JOIN platform_roles p ON p.id=r.role_id WHERE r.user_id=u.id AND p.code='ADMIN') AS administrator
 FROM users u WHERE u.id=sqlc.arg(actor_id)::uuid AND u.is_active AND u.erased_at IS NULL AND NOT u.is_dependent
 AND u.date_of_birth<=((now() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date
), scoped_plan AS (
 SELECT plan.* FROM training_plans plan JOIN training_groups group_row ON group_row.id=plan.training_group_id
 JOIN seasons season ON season.id=plan.season_id CROSS JOIN actor
 WHERE plan.id=sqlc.arg(plan_id)::uuid AND (actor.administrator OR EXISTS(SELECT 1 FROM staff_grants grant_row
 WHERE grant_row.user_id=actor.id AND grant_row.capability='COACH' AND grant_row.revoked_at IS NULL
 AND ((group_row.programme_id IS NOT NULL AND grant_row.programme_id=group_row.programme_id)
 OR (group_row.team_id IS NOT NULL AND grant_row.team_id=group_row.team_id))))
)
SELECT original.id AS plan_id,group_row.id AS group_id,season.id AS season_id,original.week_start,original.updated_at AS plan_updated_at,original.title AS plan_title,group_row.name AS group_name FROM training_plans original JOIN scoped_plan authorized ON authorized.id=original.id JOIN training_groups group_row ON group_row.id=original.training_group_id JOIN seasons season ON season.id=original.season_id FOR UPDATE OF original;

-- name: InsertFocusedWaterSession :one
WITH actor AS (
 SELECT u.id,EXISTS(SELECT 1 FROM user_platform_roles r JOIN platform_roles p ON p.id=r.role_id WHERE r.user_id=u.id AND p.code='ADMIN') AS administrator
 FROM users u WHERE u.id=sqlc.arg(actor_id)::uuid AND u.is_active AND u.erased_at IS NULL AND NOT u.is_dependent
 AND u.date_of_birth<=((now() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date
), scoped_plan AS (
 SELECT plan.* FROM training_plans plan JOIN training_groups group_row ON group_row.id=plan.training_group_id
 JOIN seasons season ON season.id=plan.season_id CROSS JOIN actor
 WHERE plan.id=sqlc.arg(plan_id)::uuid AND (actor.administrator OR EXISTS(SELECT 1 FROM staff_grants grant_row
 WHERE grant_row.user_id=actor.id AND grant_row.capability='COACH' AND grant_row.revoked_at IS NULL
 AND ((group_row.programme_id IS NOT NULL AND grant_row.programme_id=group_row.programme_id)
 OR (group_row.team_id IS NOT NULL AND grant_row.team_id=group_row.team_id))))
)
INSERT INTO training_sessions(id,plan_id,title,description,starts_at,ends_at,entry_kind,created_by_id,schedule_precision,scheduled_on,day_band)
 SELECT sqlc.arg(session_id)::uuid,plan.id,sqlc.arg(title)::text,sqlc.arg(description)::text,
 sqlc.narg(starts_at)::timestamptz,sqlc.narg(ends_at)::timestamptz,'TRAINING',sqlc.arg(actor_id)::uuid,
 sqlc.arg(schedule_precision)::text,sqlc.arg(scheduled_on)::date,NULL
 FROM scoped_plan plan WHERE plan.updated_at=sqlc.arg(expected_plan_updated_at)::timestamptz
 AND sqlc.arg(schedule_precision)::text IN ('INTERVAL','START_ONLY')
 AND sqlc.arg(scheduled_on)::date>=plan.week_start AND sqlc.arg(scheduled_on)::date<plan.week_start+7
 RETURNING *;

-- name: InsertFocusedWaterSegment :one
WITH actor AS (
 SELECT u.id,EXISTS(SELECT 1 FROM user_platform_roles r JOIN platform_roles p ON p.id=r.role_id WHERE r.user_id=u.id AND p.code='ADMIN') AS administrator
 FROM users u WHERE u.id=sqlc.arg(actor_id)::uuid AND u.is_active AND u.erased_at IS NULL AND NOT u.is_dependent
 AND u.date_of_birth<=((now() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date
), scoped_plan AS (
 SELECT plan.* FROM training_plans plan JOIN training_groups group_row ON group_row.id=plan.training_group_id
 JOIN seasons season ON season.id=plan.season_id CROSS JOIN actor
 WHERE plan.id=sqlc.arg(plan_id)::uuid AND (actor.administrator OR EXISTS(SELECT 1 FROM staff_grants grant_row
 WHERE grant_row.user_id=actor.id AND grant_row.capability='COACH' AND grant_row.revoked_at IS NULL
 AND ((group_row.programme_id IS NOT NULL AND grant_row.programme_id=group_row.programme_id)
 OR (group_row.team_id IS NOT NULL AND grant_row.team_id=group_row.team_id))))
)
INSERT INTO training_session_segments(id,session_id,position,modality,title,location,planned_duration_minutes,planned_start_offset_minutes,transition_duration_minutes,equipment_notes)
 SELECT sqlc.arg(segment_id)::uuid,session.id,1,'WATER',sqlc.arg(title)::text,sqlc.arg(location)::text,
 sqlc.narg(planned_duration_minutes)::integer,sqlc.narg(planned_start_offset_minutes)::integer,sqlc.narg(transition_duration_minutes)::integer,sqlc.arg(equipment_notes)::text
 FROM training_sessions session JOIN scoped_plan plan ON plan.id=session.plan_id
 WHERE session.id=sqlc.arg(session_id)::uuid AND session.entry_kind='TRAINING' AND session.status='ACTIVE'
 AND NOT EXISTS(SELECT 1 FROM training_session_segments existing WHERE existing.session_id=session.id)
 RETURNING *;

-- name: InsertFocusedWaterPhase :one
WITH actor AS (
 SELECT u.id,EXISTS(SELECT 1 FROM user_platform_roles r JOIN platform_roles p ON p.id=r.role_id WHERE r.user_id=u.id AND p.code='ADMIN') AS administrator
 FROM users u WHERE u.id=sqlc.arg(actor_id)::uuid AND u.is_active AND u.erased_at IS NULL AND NOT u.is_dependent
 AND u.date_of_birth<=((now() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date
), scoped_plan AS (
 SELECT plan.* FROM training_plans plan JOIN training_groups group_row ON group_row.id=plan.training_group_id
 JOIN seasons season ON season.id=plan.season_id CROSS JOIN actor
 WHERE plan.id=sqlc.arg(plan_id)::uuid AND (actor.administrator OR EXISTS(SELECT 1 FROM staff_grants grant_row
 WHERE grant_row.user_id=actor.id AND grant_row.capability='COACH' AND grant_row.revoked_at IS NULL
 AND ((group_row.programme_id IS NOT NULL AND grant_row.programme_id=group_row.programme_id)
 OR (group_row.team_id IS NOT NULL AND grant_row.team_id=group_row.team_id))))
)
INSERT INTO training_segment_blocks(id,segment_id,position,purpose,title,instructions)
 SELECT sqlc.arg(phase_id)::uuid,segment.id,sqlc.arg(position)::integer,sqlc.arg(purpose)::training_block_purpose,sqlc.arg(title)::text,sqlc.arg(instructions)::text
 FROM training_session_segments segment JOIN training_sessions session ON session.id=segment.session_id JOIN scoped_plan plan ON plan.id=session.plan_id
 WHERE segment.id=sqlc.arg(segment_id)::uuid AND session.id=sqlc.arg(session_id)::uuid
 AND session.entry_kind='TRAINING' AND session.status='ACTIVE' AND segment.modality='WATER' AND segment.position=1
 AND sqlc.arg(position)::integer BETWEEN 1 AND 32
 RETURNING *;

-- name: GetFocusedWaterSession :one
WITH actor AS (
 SELECT u.id,EXISTS(SELECT 1 FROM user_platform_roles r JOIN platform_roles p ON p.id=r.role_id WHERE r.user_id=u.id AND p.code='ADMIN') AS administrator
 FROM users u WHERE u.id=sqlc.arg(actor_id)::uuid AND u.is_active AND u.erased_at IS NULL AND NOT u.is_dependent
 AND u.date_of_birth<=((now() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date
), scoped_plan AS (
 SELECT plan.* FROM training_plans plan JOIN training_groups group_row ON group_row.id=plan.training_group_id
 JOIN seasons season ON season.id=plan.season_id CROSS JOIN actor
 WHERE plan.id=sqlc.arg(plan_id)::uuid AND (actor.administrator OR EXISTS(SELECT 1 FROM staff_grants grant_row
 WHERE grant_row.user_id=actor.id AND grant_row.capability='COACH' AND grant_row.revoked_at IS NULL
 AND ((group_row.programme_id IS NOT NULL AND grant_row.programme_id=group_row.programme_id)
 OR (group_row.team_id IS NOT NULL AND grant_row.team_id=group_row.team_id))))
)
SELECT session.*,EXISTS(SELECT 1 FROM training_session_segments segment JOIN training_segment_blocks block ON block.segment_id=segment.id
 WHERE segment.session_id=session.id AND (EXISTS(SELECT 1 FROM water_block_prescriptions water WHERE water.block_id=block.id)
 OR EXISTS(SELECT 1 FROM gym_block_prescriptions gym WHERE gym.block_id=block.id))) AS has_extra_detail
 FROM training_sessions session JOIN scoped_plan plan ON plan.id=session.plan_id WHERE session.id=sqlc.arg(session_id)::uuid;

-- name: ListFocusedWaterSegments :many
WITH actor AS (
 SELECT u.id,EXISTS(SELECT 1 FROM user_platform_roles r JOIN platform_roles p ON p.id=r.role_id WHERE r.user_id=u.id AND p.code='ADMIN') AS administrator
 FROM users u WHERE u.id=sqlc.arg(actor_id)::uuid AND u.is_active AND u.erased_at IS NULL AND NOT u.is_dependent
 AND u.date_of_birth<=((now() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date
), scoped_plan AS (
 SELECT plan.* FROM training_plans plan JOIN training_groups group_row ON group_row.id=plan.training_group_id
 JOIN seasons season ON season.id=plan.season_id CROSS JOIN actor
 WHERE plan.id=sqlc.arg(plan_id)::uuid AND (actor.administrator OR EXISTS(SELECT 1 FROM staff_grants grant_row
 WHERE grant_row.user_id=actor.id AND grant_row.capability='COACH' AND grant_row.revoked_at IS NULL
 AND ((group_row.programme_id IS NOT NULL AND grant_row.programme_id=group_row.programme_id)
 OR (group_row.team_id IS NOT NULL AND grant_row.team_id=group_row.team_id))))
)
SELECT segment.* FROM training_session_segments segment JOIN training_sessions session ON session.id=segment.session_id
 JOIN scoped_plan plan ON plan.id=session.plan_id WHERE session.id=sqlc.arg(session_id)::uuid ORDER BY segment.position,segment.id LIMIT 9;

-- name: ListFocusedWaterPhases :many
WITH actor AS (
 SELECT u.id,EXISTS(SELECT 1 FROM user_platform_roles r JOIN platform_roles p ON p.id=r.role_id WHERE r.user_id=u.id AND p.code='ADMIN') AS administrator
 FROM users u WHERE u.id=sqlc.arg(actor_id)::uuid AND u.is_active AND u.erased_at IS NULL AND NOT u.is_dependent
 AND u.date_of_birth<=((now() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date
), scoped_plan AS (
 SELECT plan.* FROM training_plans plan JOIN training_groups group_row ON group_row.id=plan.training_group_id
 JOIN seasons season ON season.id=plan.season_id CROSS JOIN actor
 WHERE plan.id=sqlc.arg(plan_id)::uuid AND (actor.administrator OR EXISTS(SELECT 1 FROM staff_grants grant_row
 WHERE grant_row.user_id=actor.id AND grant_row.capability='COACH' AND grant_row.revoked_at IS NULL
 AND ((group_row.programme_id IS NOT NULL AND grant_row.programme_id=group_row.programme_id)
 OR (group_row.team_id IS NOT NULL AND grant_row.team_id=group_row.team_id))))
)
SELECT block.* FROM training_segment_blocks block JOIN training_session_segments segment ON segment.id=block.segment_id
 JOIN training_sessions session ON session.id=segment.session_id JOIN scoped_plan plan ON plan.id=session.plan_id
 WHERE session.id=sqlc.arg(session_id)::uuid ORDER BY segment.position,segment.id,block.position,block.id LIMIT 33;

-- name: FocusedWaterArtifactSize :one
WITH actor AS (
 SELECT u.id,EXISTS(SELECT 1 FROM user_platform_roles r JOIN platform_roles p ON p.id=r.role_id WHERE r.user_id=u.id AND p.code='ADMIN') AS administrator
 FROM users u WHERE u.id=sqlc.arg(actor_id)::uuid AND u.is_active AND u.erased_at IS NULL AND NOT u.is_dependent
 AND u.date_of_birth<=((now() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date
), scoped_plan AS (
 SELECT plan.* FROM training_plans plan JOIN training_groups group_row ON group_row.id=plan.training_group_id
 JOIN seasons season ON season.id=plan.season_id CROSS JOIN actor
 WHERE plan.id=sqlc.arg(plan_id)::uuid AND (actor.administrator OR EXISTS(SELECT 1 FROM staff_grants grant_row
 WHERE grant_row.user_id=actor.id AND grant_row.capability='COACH' AND grant_row.revoked_at IS NULL
 AND ((group_row.programme_id IS NOT NULL AND grant_row.programme_id=group_row.programme_id)
 OR (group_row.team_id IS NOT NULL AND grant_row.team_id=group_row.team_id))))
)
SELECT octet_length(convert_to(sqlc.arg(snapshot)::jsonb::text,'UTF8'))::integer AS artifact_bytes FROM scoped_plan;
