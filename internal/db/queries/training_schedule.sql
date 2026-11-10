-- name: TrainingSchedulePlanWriteStatus :one
-- C preflight must inspect the complete authorized plan, never filter out
-- partial sessions or downgrade a latest v2 publication to a v1 subset.
WITH actor AS (
 SELECT u.id,EXISTS(SELECT 1 FROM user_platform_roles r JOIN platform_roles p ON p.id=r.role_id WHERE r.user_id=u.id AND p.code='ADMIN') AS administrator
 FROM users u WHERE u.id=sqlc.arg(actor_id)::uuid AND u.is_active AND u.erased_at IS NULL AND NOT u.is_dependent
 AND u.date_of_birth<=((now() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date
)
SELECT EXISTS(SELECT 1 FROM training_sessions session WHERE session.plan_id=plan.id AND session.schedule_precision<>'INTERVAL') AS has_partial,
 EXISTS(SELECT 1 FROM training_prescriptions prescription JOIN training_plan_publications publication ON publication.id=prescription.publication_id
 WHERE publication.plan_id=plan.id AND publication.id=(SELECT latest.id FROM training_plan_publications latest WHERE latest.plan_id=plan.id ORDER BY latest.revision DESC LIMIT 1)
 AND (prescription.snapshot->>'schema_version' IS DISTINCT FROM '1')) AS has_v2_publication
FROM training_plans plan JOIN training_groups group_row ON group_row.id=plan.training_group_id CROSS JOIN actor
WHERE plan.id=sqlc.arg(plan_id)::uuid AND (actor.administrator OR EXISTS(
 SELECT 1 FROM staff_grants grant_row WHERE grant_row.user_id=actor.id AND grant_row.capability='COACH' AND grant_row.revoked_at IS NULL
 AND ((group_row.programme_id IS NOT NULL AND grant_row.programme_id=group_row.programme_id)
 OR (group_row.team_id IS NOT NULL AND grant_row.team_id=group_row.team_id))));

-- name: TrainingScheduleGuardPlanV1 :one
WITH actor AS (
 SELECT u.id,EXISTS(SELECT 1 FROM user_platform_roles r JOIN platform_roles p ON p.id=r.role_id WHERE r.user_id=u.id AND p.code='ADMIN') AS administrator
 FROM users u WHERE u.id=sqlc.arg(actor_id)::uuid AND u.is_active AND u.erased_at IS NULL AND NOT u.is_dependent
 AND u.date_of_birth<=((now() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date
)
SELECT public.training_schedule_assert_plan_v1(plan.id) AS supported
FROM training_plans plan LEFT JOIN training_groups group_row ON group_row.id=plan.training_group_id CROSS JOIN actor
WHERE plan.id=sqlc.arg(plan_id)::uuid AND (actor.administrator OR EXISTS(SELECT 1 FROM staff_grants grant_row
 WHERE grant_row.user_id=actor.id AND grant_row.capability='COACH' AND grant_row.revoked_at IS NULL
 AND ((CASE WHEN plan.training_group_id IS NULL THEN plan.programme_id ELSE group_row.programme_id END IS NOT NULL
  AND grant_row.programme_id=CASE WHEN plan.training_group_id IS NULL THEN plan.programme_id ELSE group_row.programme_id END)
 OR (CASE WHEN plan.training_group_id IS NULL THEN plan.team_id ELSE group_row.team_id END IS NOT NULL
  AND grant_row.team_id=CASE WHEN plan.training_group_id IS NULL THEN plan.team_id ELSE group_row.team_id END))));

-- name: TrainingScheduleGuardSessionV1 :one
WITH actor AS (
 SELECT u.id,EXISTS(SELECT 1 FROM user_platform_roles r JOIN platform_roles p ON p.id=r.role_id WHERE r.user_id=u.id AND p.code='ADMIN') AS administrator
 FROM users u WHERE u.id=sqlc.arg(actor_id)::uuid AND u.is_active AND u.erased_at IS NULL AND NOT u.is_dependent
 AND u.date_of_birth<=((now() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date
)
SELECT public.training_schedule_assert_session_v1(session.id) AS supported
FROM training_sessions session JOIN training_plans plan ON plan.id=session.plan_id
LEFT JOIN training_groups group_row ON group_row.id=plan.training_group_id CROSS JOIN actor
WHERE session.id=sqlc.arg(session_id)::uuid AND (actor.administrator OR EXISTS(SELECT 1 FROM staff_grants grant_row
 WHERE grant_row.user_id=actor.id AND grant_row.capability='COACH' AND grant_row.revoked_at IS NULL
 AND ((CASE WHEN plan.training_group_id IS NULL THEN plan.programme_id ELSE group_row.programme_id END IS NOT NULL
  AND grant_row.programme_id=CASE WHEN plan.training_group_id IS NULL THEN plan.programme_id ELSE group_row.programme_id END)
 OR (CASE WHEN plan.training_group_id IS NULL THEN plan.team_id ELSE group_row.team_id END IS NOT NULL
  AND grant_row.team_id=CASE WHEN plan.training_group_id IS NULL THEN plan.team_id ELSE group_row.team_id END))));

-- name: TrainingScheduleGuardRoutineV1 :one
WITH actor AS (
 SELECT u.id,EXISTS(SELECT 1 FROM user_platform_roles r JOIN platform_roles p ON p.id=r.role_id WHERE r.user_id=u.id AND p.code='ADMIN') AS administrator
 FROM users u WHERE u.id=sqlc.arg(actor_id)::uuid AND u.is_active AND u.erased_at IS NULL AND NOT u.is_dependent
 AND u.date_of_birth<=((now() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date
)
SELECT public.training_schedule_assert_routine_v1(routine.id) AS supported
FROM training_routines routine CROSS JOIN actor
WHERE routine.id=sqlc.arg(routine_id)::uuid AND (routine.owner_user_id=actor.id OR actor.administrator OR
 (routine.visibility='SHARED' AND EXISTS(SELECT 1 FROM staff_grants grant_row
 WHERE grant_row.user_id=actor.id AND grant_row.capability='COACH' AND grant_row.revoked_at IS NULL
 AND ((routine.programme_id IS NOT NULL AND grant_row.programme_id=routine.programme_id)
 OR (routine.team_id IS NOT NULL AND grant_row.team_id=routine.team_id))))) FOR SHARE OF routine;

-- name: TrainingScheduleLockTargetV1 :one
SELECT plan_id::uuid FROM public.training_schedule_lock_target_v1(sqlc.arg(actor_id)::uuid,sqlc.arg(target_kind)::text,sqlc.arg(target_id)::uuid);
