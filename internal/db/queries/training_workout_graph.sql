-- Full scoped CREATE aggregate. Existing minimum-water APIs are separate and unchanged.

-- name: GetFocusedWorkoutSession :one
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
), scoped_session AS (
 SELECT session.* FROM training_sessions session JOIN scoped_plan plan ON plan.id=session.plan_id WHERE session.id=sqlc.arg(session_id)::uuid
)
SELECT session.*,EXISTS(SELECT 1 FROM training_session_outcomes outcome WHERE outcome.session_id=session.id) AS has_outcomes FROM scoped_session session;

-- name: ListFocusedWorkoutSegments :many
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
), scoped_session AS (
 SELECT session.* FROM training_sessions session JOIN scoped_plan plan ON plan.id=session.plan_id WHERE session.id=sqlc.arg(session_id)::uuid
)
SELECT segment.* FROM training_session_segments segment JOIN scoped_session session ON session.id=segment.session_id ORDER BY segment.position,segment.id LIMIT 9;

-- name: ListFocusedWorkoutBlocks :many
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
), scoped_session AS (
 SELECT session.* FROM training_sessions session JOIN scoped_plan plan ON plan.id=session.plan_id WHERE session.id=sqlc.arg(session_id)::uuid
)
SELECT block.* FROM training_segment_blocks block JOIN training_session_segments segment ON segment.id=block.segment_id JOIN scoped_session session ON session.id=segment.session_id ORDER BY segment.position,block.position,block.id LIMIT 33;

-- name: ListFocusedWorkoutWaterPrescriptions :many
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
), scoped_session AS (
 SELECT session.* FROM training_sessions session JOIN scoped_plan plan ON plan.id=session.plan_id WHERE session.id=sqlc.arg(session_id)::uuid
)
SELECT detail.* FROM training_segment_blocks block JOIN training_session_segments segment ON segment.id=block.segment_id JOIN scoped_session session ON session.id=segment.session_id JOIN water_block_prescriptions detail ON detail.block_id=block.id ORDER BY segment.position,block.position,block.id LIMIT 33;

-- name: ListFocusedWorkoutGymPrescriptions :many
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
), scoped_session AS (
 SELECT session.* FROM training_sessions session JOIN scoped_plan plan ON plan.id=session.plan_id WHERE session.id=sqlc.arg(session_id)::uuid
)
SELECT detail.* FROM training_segment_blocks block JOIN training_session_segments segment ON segment.id=block.segment_id JOIN scoped_session session ON session.id=segment.session_id JOIN gym_block_prescriptions detail ON detail.block_id=block.id ORDER BY segment.position,block.position,block.id LIMIT 33;

-- name: ListFocusedWorkoutGymExercises :many
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
), scoped_session AS (
 SELECT session.* FROM training_sessions session JOIN scoped_plan plan ON plan.id=session.plan_id WHERE session.id=sqlc.arg(session_id)::uuid
)
SELECT exercise.* FROM training_segment_blocks block JOIN training_session_segments segment ON segment.id=block.segment_id JOIN scoped_session session ON session.id=segment.session_id JOIN gym_exercises exercise ON exercise.block_id=block.id ORDER BY segment.position,block.position,exercise.position,exercise.id LIMIT 257;

-- name: ListFocusedWorkoutWaterSteps :many
WITH RECURSIVE actor AS (
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
), scoped_session AS (
 SELECT session.* FROM training_sessions session JOIN scoped_plan plan ON plan.id=session.plan_id WHERE session.id=sqlc.arg(session_id)::uuid
), water_nodes AS MATERIALIZED (
   SELECT step.* FROM water_work_steps step JOIN training_segment_blocks b ON b.id=step.block_id JOIN training_session_segments seg ON seg.id=b.segment_id JOIN scoped_session sess ON sess.id=seg.session_id ORDER BY step.id LIMIT 257
  ), tree(id,order_path,id_path) AS (
   SELECT root.id,ARRAY[root.position]::integer[],ARRAY[root.id]::uuid[] FROM water_nodes root WHERE root.parent_step_id IS NULL
   UNION ALL
   SELECT child.id,tree.order_path||child.position,tree.id_path||child.id FROM water_nodes child JOIN tree ON child.parent_step_id=tree.id WHERE NOT child.id=ANY(tree.id_path)
  )
SELECT water_step.id,water_step.block_id,water_step.parent_step_id,water_step.position,water_step.kind,water_step.name,water_step.repeats,water_step.duration_seconds,water_step.duration_certainty,water_step.distance_metres,water_step.distance_certainty,water_step.recovery_seconds,water_step.intensity_code,water_step.cadence_spm,water_step.drill_focus,water_step.drill_format,water_step.role_notes,water_step.instructions,water_step.created_at,water_step.updated_at FROM training_segment_blocks block JOIN training_session_segments segment ON segment.id=block.segment_id JOIN scoped_session session ON session.id=segment.session_id JOIN water_work_steps water_step ON water_step.block_id=block.id JOIN water_nodes bounded ON bounded.id=water_step.id LEFT JOIN tree ON tree.id=water_step.id WHERE water_step.id IS NOT NULL ORDER BY segment.position,block.position,tree.order_path NULLS LAST,tree.id_path NULLS LAST,water_step.parent_step_id NULLS FIRST,water_step.position,water_step.id LIMIT 257;

-- name: InsertFocusedWorkoutSession :one
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
 SELECT sqlc.arg(session_id)::uuid,plan.id,sqlc.arg(title)::text,sqlc.arg(description)::text,sqlc.narg(starts_at)::timestamptz,sqlc.narg(ends_at)::timestamptz,
 sqlc.arg(entry_kind)::training_entry_kind,sqlc.arg(actor_id)::uuid,sqlc.arg(schedule_precision)::text,sqlc.arg(scheduled_on)::date,sqlc.narg(day_band)::text
 FROM scoped_plan plan WHERE plan.updated_at=sqlc.arg(expected_plan_updated_at)::timestamptz
 AND sqlc.arg(scheduled_on)::date>=plan.week_start AND sqlc.arg(scheduled_on)::date<plan.week_start+7 RETURNING *;

-- name: InsertFocusedWorkoutSegment :one
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
), scoped_session AS (
 SELECT session.* FROM training_sessions session JOIN scoped_plan plan ON plan.id=session.plan_id WHERE session.id=sqlc.arg(session_id)::uuid
)
INSERT INTO training_session_segments(id,session_id,position,modality,title,location,planned_duration_minutes,planned_start_offset_minutes,transition_duration_minutes,equipment_notes)
 SELECT sqlc.arg(segment_id)::uuid,session.id,sqlc.arg(position)::integer,sqlc.arg(modality)::training_segment_modality,sqlc.arg(title)::text,sqlc.arg(location)::text,
 sqlc.narg(planned_duration_minutes)::integer,sqlc.narg(planned_start_offset_minutes)::integer,sqlc.narg(transition_duration_minutes)::integer,sqlc.arg(equipment_notes)::text
 FROM scoped_session session WHERE session.status='ACTIVE' AND session.entry_kind IN ('TRAINING','COMPETITION') AND sqlc.arg(position)::integer BETWEEN 1 AND 8
 AND (SELECT count(*) FROM (SELECT 1 FROM training_session_segments existing WHERE existing.session_id=session.id LIMIT 9) bounded)<8 RETURNING *;

-- name: InsertFocusedWorkoutBlock :one
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
), scoped_session AS (
 SELECT session.* FROM training_sessions session JOIN scoped_plan plan ON plan.id=session.plan_id WHERE session.id=sqlc.arg(session_id)::uuid
)
INSERT INTO training_segment_blocks(id,segment_id,position,purpose,title,instructions)
 SELECT sqlc.arg(block_id)::uuid,segment.id,sqlc.arg(position)::integer,sqlc.arg(purpose)::training_block_purpose,sqlc.arg(title)::text,sqlc.arg(instructions)::text
 FROM training_session_segments segment JOIN scoped_session session ON session.id=segment.session_id
 WHERE segment.id=sqlc.arg(segment_id)::uuid AND session.status='ACTIVE' AND session.entry_kind IN ('TRAINING','COMPETITION') AND sqlc.arg(position)::integer BETWEEN 1 AND 32
 AND (SELECT count(*) FROM (SELECT 1 FROM training_segment_blocks b JOIN training_session_segments s ON s.id=b.segment_id WHERE s.session_id=session.id LIMIT 33) bounded)<32 RETURNING *;

-- name: InsertFocusedWorkoutWaterPrescription :one
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
), scoped_session AS (
 SELECT session.* FROM training_sessions session JOIN scoped_plan plan ON plan.id=session.plan_id WHERE session.id=sqlc.arg(session_id)::uuid
)
INSERT INTO water_block_prescriptions(block_id,method,intensity_profile_id,target_distance_metres,target_distance_certainty)
 SELECT block.id,sqlc.arg(method)::water_work_method,sqlc.narg(intensity_profile_id)::uuid,sqlc.narg(target_distance_metres)::integer,sqlc.narg(target_distance_certainty)::training_measure_certainty
 FROM training_segment_blocks block JOIN training_session_segments segment ON segment.id=block.segment_id JOIN scoped_session session ON session.id=segment.session_id WHERE block.id=sqlc.arg(block_id)::uuid AND segment.id=sqlc.arg(segment_id)::uuid AND segment.modality='WATER' AND session.status='ACTIVE' AND session.entry_kind IN ('TRAINING','COMPETITION')
 AND (sqlc.narg(intensity_profile_id)::uuid IS NULL OR EXISTS(SELECT 1 FROM water_intensity_profiles profile WHERE profile.id=sqlc.narg(intensity_profile_id)::uuid AND profile.is_active)) RETURNING *;

-- name: InsertFocusedWorkoutGymPrescription :one
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
), scoped_session AS (
 SELECT session.* FROM training_sessions session JOIN scoped_plan plan ON plan.id=session.plan_id WHERE session.id=sqlc.arg(session_id)::uuid
)
INSERT INTO gym_block_prescriptions(block_id,structure,objective,rounds,round_recovery_seconds)
 SELECT block.id,sqlc.arg(structure)::gym_block_structure,sqlc.arg(objective)::training_objective,sqlc.arg(rounds)::integer,sqlc.narg(round_recovery_seconds)::integer
 FROM training_segment_blocks block JOIN training_session_segments segment ON segment.id=block.segment_id JOIN scoped_session session ON session.id=segment.session_id WHERE block.id=sqlc.arg(block_id)::uuid AND segment.id=sqlc.arg(segment_id)::uuid AND segment.modality='GYM' AND session.status='ACTIVE' AND session.entry_kind IN ('TRAINING','COMPETITION') RETURNING *;

-- name: InsertFocusedWorkoutWaterStep :one
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
), scoped_session AS (
 SELECT session.* FROM training_sessions session JOIN scoped_plan plan ON plan.id=session.plan_id WHERE session.id=sqlc.arg(session_id)::uuid
)
INSERT INTO water_work_steps(id,block_id,parent_step_id,position,kind,name,repeats,duration_seconds,duration_certainty,distance_metres,distance_certainty,recovery_seconds,intensity_code,cadence_spm,drill_focus,drill_format,role_notes,instructions) SELECT sqlc.arg(step_id)::uuid,block.id,sqlc.narg(parent_step_id)::uuid,sqlc.arg(position)::integer,sqlc.arg(kind)::water_step_kind,sqlc.arg(name)::text,sqlc.narg(repeats)::integer,sqlc.narg(duration_seconds)::integer,sqlc.narg(duration_certainty)::training_measure_certainty,sqlc.narg(distance_metres)::integer,sqlc.narg(distance_certainty)::training_measure_certainty,sqlc.narg(recovery_seconds)::integer,sqlc.narg(intensity_code)::text,sqlc.narg(cadence_spm)::integer,sqlc.narg(drill_focus)::text,sqlc.narg(drill_format)::text,sqlc.narg(role_notes)::text,sqlc.arg(instructions)::text FROM training_segment_blocks block JOIN training_session_segments segment ON segment.id=block.segment_id JOIN scoped_session session ON session.id=segment.session_id JOIN water_block_prescriptions water ON water.block_id=block.id
 WHERE block.id=sqlc.arg(block_id)::uuid AND segment.id=sqlc.arg(segment_id)::uuid AND segment.modality='WATER' AND session.status='ACTIVE' AND session.entry_kind IN ('TRAINING','COMPETITION')
 AND sqlc.arg(position)::integer BETWEEN 1 AND 256
 AND (sqlc.narg(parent_step_id)::uuid IS NULL OR EXISTS(SELECT 1 FROM water_work_steps parent WHERE parent.id=sqlc.narg(parent_step_id)::uuid AND parent.block_id=block.id AND parent.kind='REPEAT_GROUP'))
 AND (SELECT count(*) FROM (SELECT 1 FROM water_work_steps st JOIN training_segment_blocks b ON b.id=st.block_id JOIN training_session_segments s ON s.id=b.segment_id WHERE s.session_id=session.id LIMIT 257) bounded)<256 RETURNING *;

-- name: InsertFocusedWorkoutGymExercise :one
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
), scoped_session AS (
 SELECT session.* FROM training_sessions session JOIN scoped_plan plan ON plan.id=session.plan_id WHERE session.id=sqlc.arg(session_id)::uuid
)
INSERT INTO gym_exercises(id,block_id,position,name,sets,repetitions,duration_seconds,distance_metres,recovery_seconds,resistance_kind,resistance_value,resistance_text,execution_intent,tempo,notes) SELECT sqlc.arg(exercise_id)::uuid,block.id,sqlc.arg(position)::integer,sqlc.arg(name)::text,sqlc.narg(sets)::integer,sqlc.narg(repetitions)::integer,sqlc.narg(duration_seconds)::integer,sqlc.narg(distance_metres)::integer,sqlc.narg(recovery_seconds)::integer,sqlc.narg(resistance_kind)::gym_resistance_kind,sqlc.narg(resistance_value)::double precision,sqlc.narg(resistance_text)::text,sqlc.narg(execution_intent)::gym_execution_intent,sqlc.narg(tempo)::text,sqlc.arg(notes)::text FROM training_segment_blocks block JOIN training_session_segments segment ON segment.id=block.segment_id JOIN scoped_session session ON session.id=segment.session_id JOIN gym_block_prescriptions gym ON gym.block_id=block.id
 WHERE block.id=sqlc.arg(block_id)::uuid AND segment.id=sqlc.arg(segment_id)::uuid AND segment.modality='GYM' AND session.status='ACTIVE' AND session.entry_kind IN ('TRAINING','COMPETITION')
 AND sqlc.arg(position)::integer BETWEEN 1 AND 256
 AND (SELECT count(*) FROM (SELECT 1 FROM gym_exercises ex JOIN training_segment_blocks b ON b.id=ex.block_id JOIN training_session_segments s ON s.id=b.segment_id WHERE s.session_id=session.id LIMIT 257) bounded)<256 RETURNING *;

-- name: GetFocusedWorkoutSelectedProfile :one
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
SELECT profile.* FROM water_intensity_profiles profile CROSS JOIN scoped_plan WHERE profile.id=sqlc.arg(profile_id)::uuid AND profile.is_active;

-- name: LockFocusedWorkoutSelectedProfiles :many
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
SELECT profile.* FROM water_intensity_profiles profile CROSS JOIN scoped_plan WHERE profile.id=ANY(sqlc.arg(profile_ids)::uuid[]) AND profile.is_active AND cardinality(sqlc.arg(profile_ids)::uuid[]) BETWEEN 1 AND 32 AND cardinality(sqlc.arg(profile_ids)::uuid[])=(SELECT count(DISTINCT id) FROM unnest(sqlc.arg(profile_ids)::uuid[]) selected(id)) AND cardinality(sqlc.arg(profile_ids)::uuid[])=(SELECT count(*) FROM water_intensity_profiles p WHERE p.id=ANY(sqlc.arg(profile_ids)::uuid[]) AND p.is_active) ORDER BY profile.id FOR SHARE OF profile NOWAIT;

-- name: ListFocusedWorkoutSelectedZones :many
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
SELECT zone.* FROM water_intensity_zones zone JOIN water_intensity_profiles profile ON profile.id=zone.profile_id CROSS JOIN scoped_plan WHERE profile.id=ANY(sqlc.arg(profile_ids)::uuid[]) AND profile.is_active AND cardinality(sqlc.arg(profile_ids)::uuid[]) BETWEEN 1 AND 32 AND cardinality(sqlc.arg(profile_ids)::uuid[])=(SELECT count(DISTINCT id) FROM unnest(sqlc.arg(profile_ids)::uuid[]) selected(id)) AND cardinality(sqlc.arg(profile_ids)::uuid[])=(SELECT count(*) FROM water_intensity_profiles p WHERE p.id=ANY(sqlc.arg(profile_ids)::uuid[]) AND p.is_active) ORDER BY zone.profile_id,zone.position,zone.id LIMIT 257;

-- name: ListFocusedWorkoutActiveProfiles :many
WITH actor AS (
 SELECT u.id,EXISTS(SELECT 1 FROM user_platform_roles r JOIN platform_roles p ON p.id=r.role_id WHERE r.user_id=u.id AND p.code='ADMIN') AS administrator
 FROM users u WHERE u.id=sqlc.arg(actor_id)::uuid AND u.is_active AND u.erased_at IS NULL AND NOT u.is_dependent
 AND u.date_of_birth<=((now() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date
), scoped_plan AS (
 SELECT plan.id FROM training_plans plan JOIN training_groups group_row ON group_row.id=plan.training_group_id CROSS JOIN actor
 WHERE plan.id=sqlc.arg(plan_id)::uuid AND (actor.administrator OR EXISTS(SELECT 1 FROM staff_grants grant_row
 WHERE grant_row.user_id=actor.id AND grant_row.capability='COACH' AND grant_row.revoked_at IS NULL
 AND ((group_row.programme_id IS NOT NULL AND grant_row.programme_id=group_row.programme_id)
 OR (group_row.team_id IS NOT NULL AND grant_row.team_id=group_row.team_id))))
)
SELECT profile.id,profile.name,profile.craft,profile.revision FROM water_intensity_profiles profile CROSS JOIN scoped_plan
WHERE profile.is_active ORDER BY profile.craft,profile.name,profile.revision DESC,profile.id LIMIT 101;
