-- Scoped ID-preserving EDIT. Original lifecycle/version/digest and complete transaction protocol are store-owned.
-- No owner/parent/creator/created_at/legacy modality reassignment; no DDL.

-- name: LockFocusedWorkoutForEdit :one
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
), locked_original AS MATERIALIZED (SELECT original.* FROM training_sessions original JOIN scoped_session allowed ON allowed.id=original.id FOR UPDATE OF original), observed AS MATERIALIZED (SELECT original.id,pg_catalog.clock_timestamp() AS decision_at FROM locked_original original)
SELECT original.*,EXISTS(SELECT 1 FROM training_session_outcomes outcome WHERE outcome.session_id=original.id) AS has_outcomes,tick.decision_at::timestamptz AS decision_at,(tick.decision_at AT TIME ZONE 'Europe/Lisbon')::date AS decision_on FROM locked_original original JOIN observed tick ON tick.id=original.id;

-- name: CheckFocusedWorkoutOriginalEligibility :one
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
), tick AS MATERIALIZED (SELECT pg_catalog.clock_timestamp() AS decision_at)
SELECT tick.decision_at::timestamptz AS decision_at,(tick.decision_at AT TIME ZONE 'Europe/Lisbon')::date AS decision_on,COALESCE(sqlc.arg(original_status)::text='ACTIVE' AND ((sqlc.arg(original_schedule_precision)::text IN ('INTERVAL','START_ONLY') AND sqlc.narg(original_starts_at)::timestamptz IS NOT NULL AND sqlc.narg(original_starts_at)::timestamptz>tick.decision_at) OR (sqlc.arg(original_schedule_precision)::text IN ('DAY','BAND') AND sqlc.narg(original_scheduled_on)::date IS NOT NULL AND sqlc.narg(original_scheduled_on)::date>(tick.decision_at AT TIME ZONE 'Europe/Lisbon')::date)),false)::boolean AS eligible FROM scoped_session CROSS JOIN tick;

-- name: LockFocusedWorkoutSegments :many
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
SELECT target.* FROM training_session_segments target JOIN scoped_session session ON session.id=target.session_id ORDER BY target.id LIMIT 9 FOR UPDATE OF target;

-- name: LockFocusedWorkoutBlocks :many
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
SELECT target.* FROM training_segment_blocks target JOIN training_session_segments segment ON segment.id=target.segment_id JOIN scoped_session session ON session.id=segment.session_id ORDER BY target.id LIMIT 33 FOR UPDATE OF target;

-- name: LockFocusedWorkoutWaterPrescriptions :many
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
SELECT target.* FROM water_block_prescriptions target JOIN training_segment_blocks block ON block.id=target.block_id JOIN training_session_segments segment ON segment.id=block.segment_id JOIN scoped_session session ON session.id=segment.session_id ORDER BY target.block_id LIMIT 33 FOR UPDATE OF target;

-- name: LockFocusedWorkoutWaterSteps :many
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
SELECT target.* FROM water_work_steps target JOIN training_segment_blocks block ON block.id=target.block_id JOIN training_session_segments segment ON segment.id=block.segment_id JOIN scoped_session session ON session.id=segment.session_id ORDER BY target.id LIMIT 257 FOR UPDATE OF target;

-- name: LockFocusedWorkoutGymPrescriptions :many
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
SELECT target.* FROM gym_block_prescriptions target JOIN training_segment_blocks block ON block.id=target.block_id JOIN training_session_segments segment ON segment.id=block.segment_id JOIN scoped_session session ON session.id=segment.session_id ORDER BY target.block_id LIMIT 33 FOR UPDATE OF target;

-- name: LockFocusedWorkoutGymExercises :many
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
SELECT target.* FROM gym_exercises target JOIN training_segment_blocks block ON block.id=target.block_id JOIN training_session_segments segment ON segment.id=block.segment_id JOIN scoped_session session ON session.id=segment.session_id ORDER BY target.id LIMIT 257 FOR UPDATE OF target;

-- name: UpdateFocusedWorkoutSession :one
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
UPDATE training_sessions target SET title=sqlc.arg(title)::text,description=sqlc.arg(description)::text,schedule_precision=sqlc.arg(schedule_precision)::text,scheduled_on=sqlc.narg(scheduled_on)::date,starts_at=sqlc.narg(starts_at)::timestamptz,ends_at=sqlc.narg(ends_at)::timestamptz,day_band=sqlc.narg(day_band)::text,updated_at=pg_catalog.clock_timestamp()
 FROM scoped_session session WHERE target.id=session.id AND target.status='ACTIVE' AND target.updated_at=sqlc.arg(expected_updated_at)::timestamptz RETURNING target.*;

-- name: UpdateFocusedWorkoutSegment :one
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
UPDATE training_session_segments target SET position=sqlc.arg(position)::integer,modality=sqlc.arg(modality)::training_segment_modality,title=sqlc.arg(title)::text,location=sqlc.arg(location)::text,planned_duration_minutes=sqlc.narg(planned_duration_minutes)::integer,planned_start_offset_minutes=sqlc.narg(planned_start_offset_minutes)::integer,transition_duration_minutes=sqlc.narg(transition_duration_minutes)::integer,equipment_notes=sqlc.arg(equipment_notes)::text,updated_at=pg_catalog.clock_timestamp() FROM scoped_session session WHERE target.id=sqlc.arg(segment_id)::uuid AND target.session_id=session.id AND session.status='ACTIVE' AND session.entry_kind IN ('TRAINING','COMPETITION') AND target.updated_at=sqlc.arg(expected_updated_at)::timestamptz AND sqlc.arg(position)::integer BETWEEN 1 AND 8 AND (sqlc.arg(modality)::training_segment_modality='WATER' OR NOT EXISTS(SELECT 1 FROM water_block_prescriptions p JOIN training_segment_blocks b ON b.id=p.block_id WHERE b.segment_id=target.id)) AND (sqlc.arg(modality)::training_segment_modality='GYM' OR NOT EXISTS(SELECT 1 FROM gym_block_prescriptions p JOIN training_segment_blocks b ON b.id=p.block_id WHERE b.segment_id=target.id)) RETURNING target.*;

-- name: StageFocusedWorkoutSegmentPosition :one
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
UPDATE training_session_segments target SET position=sqlc.arg(temporary_position)::integer FROM scoped_session session WHERE target.id=sqlc.arg(segment_id)::uuid AND target.session_id=session.id AND session.status='ACTIVE' AND session.entry_kind IN ('TRAINING','COMPETITION') AND target.position=sqlc.arg(expected_position)::integer AND target.updated_at=sqlc.arg(expected_updated_at)::timestamptz AND sqlc.arg(temporary_position)::integer BETWEEN 1 AND 24 RETURNING target.*;

-- name: UpdateFocusedWorkoutBlock :one
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
UPDATE training_segment_blocks target SET position=sqlc.arg(position)::integer,purpose=sqlc.arg(purpose)::training_block_purpose,title=sqlc.arg(title)::text,instructions=sqlc.arg(instructions)::text,updated_at=pg_catalog.clock_timestamp() FROM training_session_segments segment JOIN scoped_session session ON session.id=segment.session_id WHERE target.id=sqlc.arg(block_id)::uuid AND target.segment_id=segment.id AND segment.id=sqlc.arg(segment_id)::uuid AND session.status='ACTIVE' AND session.entry_kind IN ('TRAINING','COMPETITION') AND target.updated_at=sqlc.arg(expected_updated_at)::timestamptz AND sqlc.arg(position)::integer BETWEEN 1 AND 32 RETURNING target.*;

-- name: StageFocusedWorkoutBlockPosition :one
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
UPDATE training_segment_blocks target SET position=sqlc.arg(temporary_position)::integer FROM training_session_segments segment JOIN scoped_session session ON session.id=segment.session_id WHERE target.id=sqlc.arg(block_id)::uuid AND target.segment_id=segment.id AND segment.id=sqlc.arg(segment_id)::uuid AND session.status='ACTIVE' AND session.entry_kind IN ('TRAINING','COMPETITION') AND target.position=sqlc.arg(expected_position)::integer AND target.updated_at=sqlc.arg(expected_updated_at)::timestamptz AND sqlc.arg(temporary_position)::integer BETWEEN 1 AND 96 RETURNING target.*;

-- name: UpdateFocusedWorkoutWaterPrescription :one
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
UPDATE water_block_prescriptions target SET method=sqlc.arg(method)::water_work_method,intensity_profile_id=sqlc.narg(intensity_profile_id)::uuid,target_distance_metres=sqlc.narg(target_distance_metres)::integer,target_distance_certainty=sqlc.narg(target_distance_certainty)::training_measure_certainty,updated_at=pg_catalog.clock_timestamp() FROM training_segment_blocks block JOIN training_session_segments segment ON segment.id=block.segment_id JOIN scoped_session session ON session.id=segment.session_id WHERE target.block_id=sqlc.arg(block_id)::uuid AND target.block_id=block.id AND block.id=sqlc.arg(block_id)::uuid AND segment.id=sqlc.arg(segment_id)::uuid AND session.status='ACTIVE' AND session.entry_kind IN ('TRAINING','COMPETITION') AND target.updated_at=sqlc.arg(expected_updated_at)::timestamptz AND segment.modality='WATER' AND (sqlc.narg(intensity_profile_id)::uuid IS NULL OR sqlc.narg(intensity_profile_id)::uuid IS NOT DISTINCT FROM target.intensity_profile_id OR EXISTS(SELECT 1 FROM water_intensity_profiles p WHERE p.id=sqlc.narg(intensity_profile_id)::uuid AND p.is_active)) RETURNING target.*;

-- name: UpdateFocusedWorkoutWaterStep :one
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
UPDATE water_work_steps target SET position=sqlc.arg(position)::integer,kind=sqlc.arg(kind)::water_step_kind,name=sqlc.arg(name)::text,repeats=sqlc.narg(repeats)::integer,duration_seconds=sqlc.narg(duration_seconds)::integer,duration_certainty=sqlc.narg(duration_certainty)::training_measure_certainty,distance_metres=sqlc.narg(distance_metres)::integer,distance_certainty=sqlc.narg(distance_certainty)::training_measure_certainty,recovery_seconds=sqlc.narg(recovery_seconds)::integer,intensity_code=sqlc.narg(intensity_code)::text,cadence_spm=sqlc.narg(cadence_spm)::integer,drill_focus=sqlc.narg(drill_focus)::text,drill_format=sqlc.narg(drill_format)::text,role_notes=sqlc.narg(role_notes)::text,instructions=sqlc.arg(instructions)::text,updated_at=pg_catalog.clock_timestamp() FROM training_segment_blocks block JOIN training_session_segments segment ON segment.id=block.segment_id JOIN scoped_session session ON session.id=segment.session_id WHERE target.id=sqlc.arg(step_id)::uuid AND target.block_id=block.id AND block.id=sqlc.arg(block_id)::uuid AND segment.id=sqlc.arg(segment_id)::uuid AND session.status='ACTIVE' AND session.entry_kind IN ('TRAINING','COMPETITION') AND target.updated_at=sqlc.arg(expected_updated_at)::timestamptz AND segment.modality='WATER' AND sqlc.arg(position)::integer BETWEEN 1 AND 256 AND target.parent_step_id IS NOT DISTINCT FROM sqlc.narg(parent_step_id)::uuid AND (sqlc.arg(kind)::water_step_kind='REPEAT_GROUP' OR NOT EXISTS(SELECT 1 FROM water_work_steps child WHERE child.parent_step_id=target.id)) AND (target.parent_step_id IS NULL OR EXISTS(SELECT 1 FROM water_work_steps parent WHERE parent.id=target.parent_step_id AND parent.block_id=target.block_id AND parent.kind='REPEAT_GROUP')) RETURNING target.*;

-- name: StageFocusedWorkoutWaterStepPosition :one
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
UPDATE water_work_steps target SET position=sqlc.arg(temporary_position)::integer FROM training_segment_blocks block JOIN training_session_segments segment ON segment.id=block.segment_id JOIN scoped_session session ON session.id=segment.session_id WHERE target.id=sqlc.arg(step_id)::uuid AND target.block_id=block.id AND block.id=sqlc.arg(block_id)::uuid AND segment.id=sqlc.arg(segment_id)::uuid AND session.status='ACTIVE' AND session.entry_kind IN ('TRAINING','COMPETITION') AND target.position=sqlc.arg(expected_position)::integer AND target.updated_at=sqlc.arg(expected_updated_at)::timestamptz AND sqlc.arg(temporary_position)::integer BETWEEN 1 AND 768 AND target.parent_step_id IS NOT DISTINCT FROM sqlc.narg(parent_step_id)::uuid RETURNING target.*;

-- name: UpdateFocusedWorkoutGymPrescription :one
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
UPDATE gym_block_prescriptions target SET structure=sqlc.arg(structure)::gym_block_structure,objective=sqlc.arg(objective)::training_objective,rounds=sqlc.arg(rounds)::integer,round_recovery_seconds=sqlc.narg(round_recovery_seconds)::integer,updated_at=pg_catalog.clock_timestamp() FROM training_segment_blocks block JOIN training_session_segments segment ON segment.id=block.segment_id JOIN scoped_session session ON session.id=segment.session_id WHERE target.block_id=sqlc.arg(block_id)::uuid AND target.block_id=block.id AND block.id=sqlc.arg(block_id)::uuid AND segment.id=sqlc.arg(segment_id)::uuid AND session.status='ACTIVE' AND session.entry_kind IN ('TRAINING','COMPETITION') AND target.updated_at=sqlc.arg(expected_updated_at)::timestamptz AND segment.modality='GYM' RETURNING target.*;

-- name: UpdateFocusedWorkoutGymExercise :one
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
UPDATE gym_exercises target SET position=sqlc.arg(position)::integer,name=sqlc.arg(name)::text,sets=sqlc.narg(sets)::integer,repetitions=sqlc.narg(repetitions)::integer,duration_seconds=sqlc.narg(duration_seconds)::integer,distance_metres=sqlc.narg(distance_metres)::integer,recovery_seconds=sqlc.narg(recovery_seconds)::integer,resistance_kind=sqlc.narg(resistance_kind)::gym_resistance_kind,resistance_value=sqlc.narg(resistance_value)::double precision,resistance_text=sqlc.narg(resistance_text)::text,execution_intent=sqlc.narg(execution_intent)::gym_execution_intent,tempo=sqlc.narg(tempo)::text,notes=sqlc.arg(notes)::text,updated_at=pg_catalog.clock_timestamp() FROM training_segment_blocks block JOIN training_session_segments segment ON segment.id=block.segment_id JOIN scoped_session session ON session.id=segment.session_id WHERE target.id=sqlc.arg(exercise_id)::uuid AND target.block_id=block.id AND block.id=sqlc.arg(block_id)::uuid AND segment.id=sqlc.arg(segment_id)::uuid AND session.status='ACTIVE' AND session.entry_kind IN ('TRAINING','COMPETITION') AND target.updated_at=sqlc.arg(expected_updated_at)::timestamptz AND segment.modality='GYM' AND sqlc.arg(position)::integer BETWEEN 1 AND 256 RETURNING target.*;

-- name: StageFocusedWorkoutGymExercisePosition :one
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
UPDATE gym_exercises target SET position=sqlc.arg(temporary_position)::integer FROM training_segment_blocks block JOIN training_session_segments segment ON segment.id=block.segment_id JOIN scoped_session session ON session.id=segment.session_id WHERE target.id=sqlc.arg(exercise_id)::uuid AND target.block_id=block.id AND block.id=sqlc.arg(block_id)::uuid AND segment.id=sqlc.arg(segment_id)::uuid AND session.status='ACTIVE' AND session.entry_kind IN ('TRAINING','COMPETITION') AND target.position=sqlc.arg(expected_position)::integer AND target.updated_at=sqlc.arg(expected_updated_at)::timestamptz AND sqlc.arg(temporary_position)::integer BETWEEN 1 AND 768 RETURNING target.*;

-- name: HasFocusedWorkoutRemovalReferences :one
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
), removed AS (SELECT COALESCE(sqlc.arg(segment_ids)::uuid[],'{}'::uuid[]) AS segment_ids,COALESCE(sqlc.arg(block_ids)::uuid[],'{}'::uuid[]) AS block_ids,COALESCE(sqlc.arg(step_ids)::uuid[],'{}'::uuid[]) AS step_ids,COALESCE(sqlc.arg(exercise_ids)::uuid[],'{}'::uuid[]) AS exercise_ids)
SELECT EXISTS(SELECT 1 FROM training_variations v WHERE v.plan_id=session.plan_id AND ((v.subject_kind='SEGMENT' AND v.subject_id=ANY(removed.segment_ids)) OR (v.subject_kind='BLOCK' AND v.subject_id=ANY(removed.block_ids)) OR (v.subject_kind='WATER_STEP' AND v.subject_id=ANY(removed.step_ids)) OR (v.subject_kind='GYM_EXERCISE' AND v.subject_id=ANY(removed.exercise_ids)))) AS has_references FROM scoped_session session CROSS JOIN removed WHERE cardinality(removed.segment_ids)<=8 AND cardinality(removed.segment_ids)=(SELECT count(DISTINCT id) FROM unnest(removed.segment_ids) selected(id)) AND cardinality(removed.segment_ids)=(SELECT count(*) FROM training_session_segments target JOIN scoped_session session ON session.id=target.session_id WHERE target.id=ANY(removed.segment_ids)) AND cardinality(removed.block_ids)<=32 AND cardinality(removed.block_ids)=(SELECT count(DISTINCT id) FROM unnest(removed.block_ids) selected(id)) AND cardinality(removed.block_ids)=(SELECT count(*) FROM training_segment_blocks target JOIN training_session_segments segment ON segment.id=target.segment_id JOIN scoped_session session ON session.id=segment.session_id WHERE target.id=ANY(removed.block_ids)) AND cardinality(removed.step_ids)<=256 AND cardinality(removed.step_ids)=(SELECT count(DISTINCT id) FROM unnest(removed.step_ids) selected(id)) AND cardinality(removed.step_ids)=(SELECT count(*) FROM water_work_steps target JOIN training_segment_blocks block ON block.id=target.block_id JOIN training_session_segments segment ON segment.id=block.segment_id JOIN scoped_session session ON session.id=segment.session_id WHERE target.id=ANY(removed.step_ids)) AND cardinality(removed.exercise_ids)<=256 AND cardinality(removed.exercise_ids)=(SELECT count(DISTINCT id) FROM unnest(removed.exercise_ids) selected(id)) AND cardinality(removed.exercise_ids)=(SELECT count(*) FROM gym_exercises target JOIN training_segment_blocks block ON block.id=target.block_id JOIN training_session_segments segment ON segment.id=block.segment_id JOIN scoped_session session ON session.id=segment.session_id WHERE target.id=ANY(removed.exercise_ids));

-- name: DeleteFocusedWorkoutSegments :many
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
), removed AS (SELECT COALESCE(sqlc.arg(segment_ids)::uuid[],'{}'::uuid[]) AS ids), eligible AS MATERIALIZED (SELECT target.id AS id FROM training_session_segments target JOIN scoped_session session ON session.id=target.session_id CROSS JOIN removed WHERE target.id=ANY(removed.ids) AND session.status='ACTIVE' AND session.entry_kind IN ('TRAINING','COMPETITION') AND NOT EXISTS(SELECT 1 FROM training_segment_blocks child WHERE child.segment_id=target.id) AND NOT EXISTS(SELECT 1 FROM training_variations v WHERE v.plan_id=session.plan_id AND v.subject_kind='SEGMENT' AND v.subject_id=target.id))
DELETE FROM training_session_segments target USING removed WHERE target.id IN (SELECT id FROM eligible) AND cardinality(removed.ids)<=8 AND cardinality(removed.ids)=(SELECT count(DISTINCT id) FROM unnest(removed.ids) selected(id)) AND cardinality(removed.ids)=(SELECT count(*) FROM eligible) RETURNING target.id;

-- name: DeleteFocusedWorkoutBlocks :many
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
), removed AS (SELECT COALESCE(sqlc.arg(block_ids)::uuid[],'{}'::uuid[]) AS ids), eligible AS MATERIALIZED (SELECT target.id AS id FROM training_segment_blocks target JOIN training_session_segments segment ON segment.id=target.segment_id JOIN scoped_session session ON session.id=segment.session_id CROSS JOIN removed WHERE target.id=ANY(removed.ids) AND session.status='ACTIVE' AND session.entry_kind IN ('TRAINING','COMPETITION') AND NOT EXISTS(SELECT 1 FROM water_block_prescriptions child WHERE child.block_id=target.id) AND NOT EXISTS(SELECT 1 FROM gym_block_prescriptions child WHERE child.block_id=target.id) AND NOT EXISTS(SELECT 1 FROM training_variations v WHERE v.plan_id=session.plan_id AND v.subject_kind='BLOCK' AND v.subject_id=target.id))
DELETE FROM training_segment_blocks target USING removed WHERE target.id IN (SELECT id FROM eligible) AND cardinality(removed.ids)<=32 AND cardinality(removed.ids)=(SELECT count(DISTINCT id) FROM unnest(removed.ids) selected(id)) AND cardinality(removed.ids)=(SELECT count(*) FROM eligible) RETURNING target.id;

-- name: DeleteFocusedWorkoutWaterPrescriptions :many
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
), removed AS (SELECT COALESCE(sqlc.arg(block_ids)::uuid[],'{}'::uuid[]) AS ids), eligible AS MATERIALIZED (SELECT target.block_id AS id FROM water_block_prescriptions target JOIN training_segment_blocks block ON block.id=target.block_id JOIN training_session_segments segment ON segment.id=block.segment_id JOIN scoped_session session ON session.id=segment.session_id CROSS JOIN removed WHERE target.block_id=ANY(removed.ids) AND session.status='ACTIVE' AND session.entry_kind IN ('TRAINING','COMPETITION') AND NOT EXISTS(SELECT 1 FROM water_work_steps child WHERE child.block_id=target.block_id))
DELETE FROM water_block_prescriptions target USING removed WHERE target.block_id IN (SELECT id FROM eligible) AND cardinality(removed.ids)<=32 AND cardinality(removed.ids)=(SELECT count(DISTINCT id) FROM unnest(removed.ids) selected(id)) AND cardinality(removed.ids)=(SELECT count(*) FROM eligible) RETURNING target.block_id;

-- name: DeleteFocusedWorkoutWaterSteps :many
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
), removed AS (SELECT COALESCE(sqlc.arg(step_ids)::uuid[],'{}'::uuid[]) AS ids), eligible AS MATERIALIZED (SELECT target.id AS id FROM water_work_steps target JOIN training_segment_blocks block ON block.id=target.block_id JOIN training_session_segments segment ON segment.id=block.segment_id JOIN scoped_session session ON session.id=segment.session_id CROSS JOIN removed WHERE target.id=ANY(removed.ids) AND session.status='ACTIVE' AND session.entry_kind IN ('TRAINING','COMPETITION') AND NOT EXISTS(SELECT 1 FROM water_work_steps child WHERE child.parent_step_id=target.id) AND NOT EXISTS(SELECT 1 FROM training_variations v WHERE v.plan_id=session.plan_id AND v.subject_kind='WATER_STEP' AND v.subject_id=target.id))
DELETE FROM water_work_steps target USING removed WHERE target.id IN (SELECT id FROM eligible) AND cardinality(removed.ids)<=256 AND cardinality(removed.ids)=(SELECT count(DISTINCT id) FROM unnest(removed.ids) selected(id)) AND cardinality(removed.ids)=(SELECT count(*) FROM eligible) RETURNING target.id;

-- name: DeleteFocusedWorkoutGymPrescriptions :many
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
), removed AS (SELECT COALESCE(sqlc.arg(block_ids)::uuid[],'{}'::uuid[]) AS ids), eligible AS MATERIALIZED (SELECT target.block_id AS id FROM gym_block_prescriptions target JOIN training_segment_blocks block ON block.id=target.block_id JOIN training_session_segments segment ON segment.id=block.segment_id JOIN scoped_session session ON session.id=segment.session_id CROSS JOIN removed WHERE target.block_id=ANY(removed.ids) AND session.status='ACTIVE' AND session.entry_kind IN ('TRAINING','COMPETITION') AND NOT EXISTS(SELECT 1 FROM gym_exercises child WHERE child.block_id=target.block_id))
DELETE FROM gym_block_prescriptions target USING removed WHERE target.block_id IN (SELECT id FROM eligible) AND cardinality(removed.ids)<=32 AND cardinality(removed.ids)=(SELECT count(DISTINCT id) FROM unnest(removed.ids) selected(id)) AND cardinality(removed.ids)=(SELECT count(*) FROM eligible) RETURNING target.block_id;

-- name: DeleteFocusedWorkoutGymExercises :many
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
), removed AS (SELECT COALESCE(sqlc.arg(exercise_ids)::uuid[],'{}'::uuid[]) AS ids), eligible AS MATERIALIZED (SELECT target.id AS id FROM gym_exercises target JOIN training_segment_blocks block ON block.id=target.block_id JOIN training_session_segments segment ON segment.id=block.segment_id JOIN scoped_session session ON session.id=segment.session_id CROSS JOIN removed WHERE target.id=ANY(removed.ids) AND session.status='ACTIVE' AND session.entry_kind IN ('TRAINING','COMPETITION') AND NOT EXISTS(SELECT 1 FROM training_variations v WHERE v.plan_id=session.plan_id AND v.subject_kind='GYM_EXERCISE' AND v.subject_id=target.id))
DELETE FROM gym_exercises target USING removed WHERE target.id IN (SELECT id FROM eligible) AND cardinality(removed.ids)<=256 AND cardinality(removed.ids)=(SELECT count(DISTINCT id) FROM unnest(removed.ids) selected(id)) AND cardinality(removed.ids)=(SELECT count(*) FROM eligible) RETURNING target.id;

-- name: GetFocusedWorkoutPinnedProfile :one
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
SELECT profile.* FROM water_intensity_profiles profile JOIN water_block_prescriptions water ON water.intensity_profile_id=profile.id JOIN training_segment_blocks block ON block.id=water.block_id JOIN training_session_segments segment ON segment.id=block.segment_id JOIN scoped_session session ON session.id=segment.session_id WHERE block.id=sqlc.arg(block_id)::uuid;

-- name: ListFocusedWorkoutEditProfiles :many
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
), selection AS (SELECT COALESCE(sqlc.arg(profile_ids)::uuid[],'{}'::uuid[]) AS profile_ids,COALESCE(sqlc.arg(pinned_block_ids)::uuid[],'{}'::uuid[]) AS pinned_block_ids), source_pins AS (SELECT block.id AS block_id,water.intensity_profile_id AS profile_id FROM water_block_prescriptions water JOIN training_segment_blocks block ON block.id=water.block_id JOIN training_session_segments segment ON segment.id=block.segment_id JOIN scoped_session session ON session.id=segment.session_id CROSS JOIN selection WHERE block.id=ANY(selection.pinned_block_ids) AND water.intensity_profile_id IS NOT NULL), eligible_profiles AS (SELECT profile.* FROM water_intensity_profiles profile CROSS JOIN scoped_session CROSS JOIN selection WHERE profile.id=ANY(selection.profile_ids) AND (profile.is_active OR EXISTS(SELECT 1 FROM source_pins pin WHERE pin.profile_id=profile.id)))
SELECT profile.* FROM water_intensity_profiles profile JOIN eligible_profiles allowed ON allowed.id=profile.id CROSS JOIN selection WHERE cardinality(selection.profile_ids)<=32 AND cardinality(selection.profile_ids)=(SELECT count(DISTINCT id) FROM unnest(selection.profile_ids) selected(id)) AND cardinality(selection.profile_ids)=(SELECT count(*) FROM eligible_profiles) AND cardinality(selection.pinned_block_ids)<=32 AND cardinality(selection.pinned_block_ids)=(SELECT count(DISTINCT id) FROM unnest(selection.pinned_block_ids) selected(id)) AND cardinality(selection.pinned_block_ids)=(SELECT count(*) FROM source_pins) ORDER BY profile.id;

-- name: LockFocusedWorkoutEditProfiles :many
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
), selection AS (SELECT COALESCE(sqlc.arg(profile_ids)::uuid[],'{}'::uuid[]) AS profile_ids,COALESCE(sqlc.arg(pinned_block_ids)::uuid[],'{}'::uuid[]) AS pinned_block_ids), source_pins AS (SELECT block.id AS block_id,water.intensity_profile_id AS profile_id FROM water_block_prescriptions water JOIN training_segment_blocks block ON block.id=water.block_id JOIN training_session_segments segment ON segment.id=block.segment_id JOIN scoped_session session ON session.id=segment.session_id CROSS JOIN selection WHERE block.id=ANY(selection.pinned_block_ids) AND water.intensity_profile_id IS NOT NULL), eligible_profiles AS (SELECT profile.* FROM water_intensity_profiles profile CROSS JOIN scoped_session CROSS JOIN selection WHERE profile.id=ANY(selection.profile_ids) AND (profile.is_active OR EXISTS(SELECT 1 FROM source_pins pin WHERE pin.profile_id=profile.id)))
SELECT profile.* FROM water_intensity_profiles profile JOIN eligible_profiles allowed ON allowed.id=profile.id CROSS JOIN selection WHERE cardinality(selection.profile_ids)<=32 AND cardinality(selection.profile_ids)=(SELECT count(DISTINCT id) FROM unnest(selection.profile_ids) selected(id)) AND cardinality(selection.profile_ids)=(SELECT count(*) FROM eligible_profiles) AND cardinality(selection.pinned_block_ids)<=32 AND cardinality(selection.pinned_block_ids)=(SELECT count(DISTINCT id) FROM unnest(selection.pinned_block_ids) selected(id)) AND cardinality(selection.pinned_block_ids)=(SELECT count(*) FROM source_pins) ORDER BY profile.id FOR SHARE OF profile NOWAIT;

-- name: ListFocusedWorkoutEditZones :many
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
), selection AS (SELECT COALESCE(sqlc.arg(profile_ids)::uuid[],'{}'::uuid[]) AS profile_ids,COALESCE(sqlc.arg(pinned_block_ids)::uuid[],'{}'::uuid[]) AS pinned_block_ids), source_pins AS (SELECT block.id AS block_id,water.intensity_profile_id AS profile_id FROM water_block_prescriptions water JOIN training_segment_blocks block ON block.id=water.block_id JOIN training_session_segments segment ON segment.id=block.segment_id JOIN scoped_session session ON session.id=segment.session_id CROSS JOIN selection WHERE block.id=ANY(selection.pinned_block_ids) AND water.intensity_profile_id IS NOT NULL), eligible_profiles AS (SELECT profile.* FROM water_intensity_profiles profile CROSS JOIN scoped_session CROSS JOIN selection WHERE profile.id=ANY(selection.profile_ids) AND (profile.is_active OR EXISTS(SELECT 1 FROM source_pins pin WHERE pin.profile_id=profile.id)))
SELECT zone.* FROM water_intensity_zones zone JOIN eligible_profiles profile ON profile.id=zone.profile_id CROSS JOIN selection WHERE cardinality(selection.profile_ids)<=32 AND cardinality(selection.profile_ids)=(SELECT count(DISTINCT id) FROM unnest(selection.profile_ids) selected(id)) AND cardinality(selection.profile_ids)=(SELECT count(*) FROM eligible_profiles) AND cardinality(selection.pinned_block_ids)<=32 AND cardinality(selection.pinned_block_ids)=(SELECT count(DISTINCT id) FROM unnest(selection.pinned_block_ids) selected(id)) AND cardinality(selection.pinned_block_ids)=(SELECT count(*) FROM source_pins) ORDER BY zone.profile_id,zone.position,zone.id LIMIT 257;

-- name: CancelFocusedWorkoutSession :one
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
), locked_original AS MATERIALIZED (SELECT original.* FROM training_sessions original JOIN scoped_session allowed ON allowed.id=original.id WHERE original.status='ACTIVE' AND original.updated_at=sqlc.arg(expected_updated_at)::timestamptz FOR UPDATE OF original), observed AS MATERIALIZED (SELECT original.id,pg_catalog.clock_timestamp() AS decision_at FROM locked_original original)
UPDATE training_sessions target SET status='CANCELLED',cancelled_at=tick.decision_at,cancelled_by_id=sqlc.arg(actor_id)::uuid,cancellation_reason=sqlc.arg(cancellation_reason)::text,updated_at=pg_catalog.clock_timestamp() FROM locked_original original JOIN observed tick ON tick.id=original.id WHERE target.id=original.id AND target.status='ACTIVE' AND target.updated_at=sqlc.arg(expected_updated_at)::timestamptz AND ((target.schedule_precision IN ('INTERVAL','START_ONLY') AND target.starts_at IS NOT NULL AND target.starts_at>tick.decision_at) OR (target.schedule_precision IN ('DAY','BAND') AND target.scheduled_on IS NOT NULL AND target.scheduled_on>(tick.decision_at AT TIME ZONE 'Europe/Lisbon')::date)) RETURNING target.*;
