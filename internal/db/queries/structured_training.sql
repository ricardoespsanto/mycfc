-- name: ListEligibleTrainingGroupMemberships :many
SELECT membership.id, subject.name AS athlete_name, programme.name_pt AS programme_name,
       team.name AS team_name, membership.programme_id, membership.team_id
FROM user_memberships membership
JOIN users subject ON subject.id = membership.user_id AND subject.is_active = true
JOIN programmes programme ON programme.id = membership.programme_id
LEFT JOIN teams team ON team.id = membership.team_id
WHERE membership.starts_on <= CURRENT_DATE
  AND (membership.ends_on IS NULL OR membership.ends_on >= CURRENT_DATE)
  AND (
      sqlc.arg(is_admin)::boolean
      OR EXISTS (
          SELECT 1 FROM staff_grants grant_row
          WHERE grant_row.user_id = sqlc.arg(user_id)
            AND grant_row.capability = 'COACH'
            AND grant_row.revoked_at IS NULL
            AND (grant_row.programme_id = membership.programme_id OR grant_row.team_id = membership.team_id)
      )
  )
ORDER BY programme.name_pt, team.name NULLS FIRST, subject.name, membership.id;

-- name: CreateStructuredTrainingGroup :one
INSERT INTO training_groups (name, programme_id, team_id, created_by_id)
VALUES (sqlc.arg(name), sqlc.narg(programme_id), sqlc.narg(team_id), sqlc.arg(created_by_id))
RETURNING id, name, programme_id, team_id, created_by_id, created_at, updated_at;

-- name: AddStructuredTrainingGroupMember :execrows
INSERT INTO training_group_members (group_id, membership_id, added_by_id)
SELECT group_row.id, membership.id, sqlc.arg(added_by_id)
FROM training_groups group_row
JOIN user_memberships membership ON membership.id = sqlc.arg(membership_id)
WHERE group_row.id = sqlc.arg(group_id)
  AND membership.starts_on <= CURRENT_DATE
  AND (membership.ends_on IS NULL OR membership.ends_on >= CURRENT_DATE)
  AND (group_row.programme_id IS NULL OR group_row.programme_id = membership.programme_id)
  AND (group_row.team_id IS NULL OR group_row.team_id = membership.team_id)
ON CONFLICT (group_id, membership_id) DO NOTHING;

-- name: CanManageStructuredTrainingGroup :one
SELECT EXISTS (
    SELECT 1 FROM training_groups group_row
    WHERE group_row.id = sqlc.arg(group_id)
      AND (
          sqlc.arg(is_admin)::boolean
          OR EXISTS (
              SELECT 1 FROM staff_grants grant_row
              WHERE grant_row.user_id = sqlc.arg(user_id)
                AND grant_row.capability = 'COACH'
                AND grant_row.revoked_at IS NULL
                AND (grant_row.programme_id = group_row.programme_id OR grant_row.team_id = group_row.team_id)
          )
      )
);

-- name: CreateStructuredTrainingWeek :one
INSERT INTO training_plans (title, description, programme_id, team_id, training_group_id, season_id, week_start, created_by_id)
SELECT sqlc.arg(title), sqlc.arg(description), group_row.programme_id, group_row.team_id,
       group_row.id, season.id, sqlc.arg(week_start), sqlc.arg(created_by_id)
FROM training_groups group_row
JOIN LATERAL (
    SELECT season_row.id
    FROM seasons season_row
    WHERE sqlc.arg(week_start) BETWEEN season_row.starts_on AND season_row.ends_on
    ORDER BY season_row.is_current DESC, season_row.starts_on DESC, season_row.id
    LIMIT 1
) season ON true
WHERE group_row.id = sqlc.arg(group_id)
RETURNING id, title, description, programme_id, team_id, training_group_id, season_id, week_start, created_by_id, created_at, updated_at;

-- name: CanManageStructuredTrainingWeek :one
SELECT EXISTS (
    SELECT 1
    FROM training_plans plan
    JOIN training_groups group_row ON group_row.id = plan.training_group_id
    WHERE plan.id = sqlc.arg(plan_id)
      AND (
          sqlc.arg(is_admin)::boolean
          OR EXISTS (
              SELECT 1 FROM staff_grants grant_row
              WHERE grant_row.user_id = sqlc.arg(user_id)
                AND grant_row.capability = 'COACH'
                AND grant_row.revoked_at IS NULL
                AND (grant_row.programme_id = group_row.programme_id OR grant_row.team_id = group_row.team_id)
          )
      )
);

-- name: CreateStructuredTrainingSession :one
INSERT INTO training_sessions (plan_id, title, description, starts_at, ends_at, entry_kind, created_by_id)
SELECT plan.id, sqlc.arg(title), sqlc.arg(description), sqlc.arg(starts_at), sqlc.arg(ends_at),
       sqlc.arg(entry_kind)::training_entry_kind, sqlc.arg(created_by_id)
FROM training_plans plan
WHERE plan.id = sqlc.arg(plan_id)
  AND plan.training_group_id IS NOT NULL
  AND sqlc.arg(starts_at) >= (plan.week_start::timestamp AT TIME ZONE 'Europe/Lisbon')
  AND sqlc.arg(ends_at) <= ((plan.week_start + 7)::timestamp AT TIME ZONE 'Europe/Lisbon')
RETURNING id, plan_id, title, description, starts_at, ends_at, modality_id, entry_kind, status, cancelled_at, cancelled_by_id, cancellation_reason, created_by_id, created_at, updated_at;

-- name: CreateTrainingSessionSegment :one
INSERT INTO training_session_segments (session_id, position, modality, title, location, planned_duration_minutes,
                                       planned_start_offset_minutes, transition_duration_minutes, equipment_notes)
SELECT sqlc.arg(session_id), COALESCE(max(segment.position), 0) + 1,
       sqlc.arg(modality)::training_segment_modality, sqlc.arg(title), sqlc.arg(location),
       sqlc.narg(planned_duration_minutes), sqlc.narg(planned_start_offset_minutes),
       sqlc.narg(transition_duration_minutes), sqlc.arg(equipment_notes)
FROM training_sessions session
LEFT JOIN training_session_segments segment ON segment.session_id = session.id
JOIN training_plans plan ON plan.id = session.plan_id AND plan.training_group_id IS NOT NULL
WHERE session.id = sqlc.arg(session_id) AND session.entry_kind = 'TRAINING'
GROUP BY session.id
RETURNING id;

-- name: CreateTrainingSegmentBlock :one
INSERT INTO training_segment_blocks (segment_id, position, purpose, title, instructions)
SELECT sqlc.arg(segment_id), COALESCE(max(block.position), 0) + 1,
       sqlc.arg(purpose)::training_block_purpose, sqlc.arg(title), sqlc.arg(instructions)
FROM training_session_segments segment
LEFT JOIN training_segment_blocks block ON block.segment_id = segment.id
WHERE segment.id = sqlc.arg(segment_id)
GROUP BY segment.id
RETURNING id;

-- name: CreateGymBlockPrescription :execrows
INSERT INTO gym_block_prescriptions (block_id, structure, objective, rounds, round_recovery_seconds)
SELECT block.id, sqlc.arg(structure)::gym_block_structure, sqlc.arg(objective)::training_objective,
       sqlc.arg(rounds), sqlc.narg(round_recovery_seconds)
FROM training_segment_blocks block
JOIN training_session_segments segment ON segment.id = block.segment_id
WHERE block.id = sqlc.arg(block_id) AND segment.modality = 'GYM';

-- name: CreateGymExercise :one
INSERT INTO gym_exercises (block_id, position, name, sets, repetitions, duration_seconds, distance_metres,
                           recovery_seconds, resistance_kind, resistance_value, resistance_text,
                           execution_intent, tempo, notes)
SELECT prescription.block_id, COALESCE(max(exercise.position), 0) + 1, sqlc.arg(name), sqlc.narg(sets),
       sqlc.narg(repetitions), sqlc.narg(duration_seconds), sqlc.narg(distance_metres),
       sqlc.narg(recovery_seconds), sqlc.narg(resistance_kind)::gym_resistance_kind,
       sqlc.narg(resistance_value), sqlc.narg(resistance_text),
       sqlc.narg(execution_intent)::gym_execution_intent, sqlc.narg(tempo), sqlc.arg(notes)
FROM gym_block_prescriptions prescription
LEFT JOIN gym_exercises exercise ON exercise.block_id = prescription.block_id
WHERE prescription.block_id = sqlc.arg(block_id)
GROUP BY prescription.block_id
RETURNING id;

-- name: CreateWaterBlockPrescription :execrows
INSERT INTO water_block_prescriptions (block_id, method, intensity_profile_id, target_distance_metres, target_distance_certainty)
SELECT block.id, sqlc.arg(method)::water_work_method, sqlc.narg(intensity_profile_id),
       sqlc.narg(target_distance_metres), sqlc.narg(target_distance_certainty)::training_measure_certainty
FROM training_segment_blocks block
JOIN training_session_segments segment ON segment.id = block.segment_id
WHERE block.id = sqlc.arg(block_id) AND segment.modality = 'WATER';

-- name: CreateWaterWorkStep :one
INSERT INTO water_work_steps (block_id, parent_step_id, position, kind, name, repeats,
                              duration_seconds, duration_certainty, distance_metres, distance_certainty,
                              recovery_seconds, intensity_code, cadence_spm, drill_focus, drill_format,
                              role_notes, instructions)
SELECT prescription.block_id, sqlc.narg(parent_step_id), COALESCE(max(step.position), 0) + 1,
       sqlc.arg(kind)::water_step_kind, sqlc.arg(name), sqlc.narg(repeats),
       sqlc.narg(duration_seconds), sqlc.narg(duration_certainty)::training_measure_certainty,
       sqlc.narg(distance_metres), sqlc.narg(distance_certainty)::training_measure_certainty,
       sqlc.narg(recovery_seconds), sqlc.narg(intensity_code), sqlc.narg(cadence_spm),
       sqlc.narg(drill_focus), sqlc.narg(drill_format), sqlc.narg(role_notes), sqlc.arg(instructions)
FROM water_block_prescriptions prescription
LEFT JOIN water_work_steps step ON step.block_id = prescription.block_id
 AND step.parent_step_id IS NOT DISTINCT FROM sqlc.narg(parent_step_id)
WHERE prescription.block_id = sqlc.arg(block_id)
GROUP BY prescription.block_id
RETURNING id;

-- name: CreateWaterIntensityProfile :one
WITH previous AS (
 UPDATE water_intensity_profiles AS profile SET is_active = false
 WHERE profile.name = sqlc.arg(name) AND profile.craft = sqlc.arg(craft)::paddling_craft AND profile.is_active
 RETURNING profile.id, profile.revision
), created AS (
 INSERT INTO water_intensity_profiles (name, craft, revision, supersedes_id, notes, created_by_id)
 VALUES (sqlc.arg(name), sqlc.arg(craft)::paddling_craft,
         COALESCE((SELECT max(revision) + 1 FROM previous), 1),
         (SELECT id FROM previous ORDER BY revision DESC LIMIT 1), sqlc.arg(notes), sqlc.arg(created_by_id))
 RETURNING id, name, craft, revision, supersedes_id, notes, is_active, created_by_id, created_at
), copied_zones AS (
 INSERT INTO water_intensity_zones (profile_id, position, code, label, cadence_min, cadence_max, meaning)
 SELECT created.id, zone.position, zone.code, zone.label, zone.cadence_min, zone.cadence_max, zone.meaning
 FROM created
 JOIN previous ON true
 JOIN water_intensity_zones zone ON zone.profile_id = previous.id
 RETURNING id
)
SELECT created.id, created.name, created.craft, created.revision, created.supersedes_id,
       created.notes, created.is_active, created.created_by_id, created.created_at
FROM created;

-- name: CreateWaterIntensityZone :one
INSERT INTO water_intensity_zones (profile_id, position, code, label, cadence_min, cadence_max, meaning)
SELECT profile.id, COALESCE(max(zone.position), 0) + 1, sqlc.arg(code), sqlc.arg(label),
       sqlc.narg(cadence_min), sqlc.narg(cadence_max), sqlc.arg(meaning)
FROM water_intensity_profiles profile
LEFT JOIN water_intensity_zones zone ON zone.profile_id = profile.id
WHERE profile.id = sqlc.arg(profile_id) AND profile.is_active
GROUP BY profile.id
ON CONFLICT (profile_id, code) DO UPDATE SET
 label = EXCLUDED.label,
 cadence_min = EXCLUDED.cadence_min,
 cadence_max = EXCLUDED.cadence_max,
 meaning = EXCLUDED.meaning
RETURNING id;

-- name: ListActiveWaterIntensityProfiles :many
SELECT profile.id, profile.name, profile.craft, profile.revision, profile.notes,
       zone.id AS zone_id, zone.position, zone.code, zone.label, zone.cadence_min, zone.cadence_max, zone.meaning
FROM water_intensity_profiles profile
LEFT JOIN water_intensity_zones zone ON zone.profile_id = profile.id
WHERE profile.is_active
ORDER BY profile.craft, profile.name, profile.revision DESC, zone.position;

-- name: MoveTrainingSessionSegment :one
SELECT move_training_session_segment(sqlc.arg(segment_id), sqlc.arg(direction));

-- name: MoveTrainingSegmentBlock :one
SELECT move_training_segment_block(sqlc.arg(block_id), sqlc.arg(direction));

-- name: MoveGymExercise :one
SELECT move_gym_exercise(sqlc.arg(exercise_id), sqlc.arg(direction));

-- name: ListStructuredTrainingOverviewForManager :many
SELECT group_row.id AS group_id, group_row.name AS group_name,
       programme.name_pt AS programme_name, team.name AS team_name,
       (SELECT count(*)::integer FROM training_group_members member WHERE member.group_id = group_row.id) AS member_count,
       plan.id AS plan_id, plan.title AS plan_title, plan.description AS plan_description, season.name AS season_name, plan.week_start,
       session.id AS session_id, session.title AS session_title, session.description AS session_description,
       session.starts_at, session.ends_at, session.entry_kind,
       segment.id AS segment_id, segment.position AS segment_position, segment.modality AS segment_modality,
       segment.title AS segment_title, segment.location AS segment_location,
       segment.planned_duration_minutes, segment.planned_start_offset_minutes,
       segment.transition_duration_minutes, segment.equipment_notes,
       block.id AS block_id, block.position AS block_position, block.purpose AS block_purpose,
       block.title AS block_title, block.instructions AS block_instructions,
       gym.structure AS gym_structure, gym.objective AS gym_objective, gym.rounds AS gym_rounds,
       gym.round_recovery_seconds,
       water.method AS water_method, water.intensity_profile_id AS water_intensity_profile_id,
       water_profile.name AS water_profile_name, water_profile.revision AS water_profile_revision,
       water_profile.craft AS water_profile_craft,
       water.target_distance_metres AS water_target_distance_metres,
       water.target_distance_certainty AS water_target_distance_certainty,
       water_step.id AS water_step_id, water_step.parent_step_id AS water_parent_step_id,
       water_step.position AS water_step_position, water_step.kind AS water_step_kind,
       water_step.name AS water_step_name, water_step.repeats AS water_step_repeats,
       water_step.duration_seconds AS water_step_duration_seconds,
       water_step.duration_certainty AS water_step_duration_certainty,
       water_step.distance_metres AS water_step_distance_metres,
       water_step.distance_certainty AS water_step_distance_certainty,
       water_step.recovery_seconds AS water_step_recovery_seconds,
       water_step.intensity_code AS water_step_intensity_code, water_step.cadence_spm AS water_step_cadence_spm,
       water_zone.label AS water_zone_label, water_zone.cadence_min AS water_zone_cadence_min,
       water_zone.cadence_max AS water_zone_cadence_max, water_zone.meaning AS water_zone_meaning,
       water_step.drill_focus AS water_step_drill_focus, water_step.drill_format AS water_step_drill_format,
       water_step.role_notes AS water_step_role_notes, water_step.instructions AS water_step_instructions,
       exercise.id AS exercise_id, exercise.position AS exercise_position, exercise.name AS exercise_name,
       exercise.sets AS exercise_sets, exercise.repetitions AS exercise_repetitions,
       exercise.duration_seconds AS exercise_duration_seconds, exercise.distance_metres AS exercise_distance_metres,
       exercise.recovery_seconds AS exercise_recovery_seconds, exercise.resistance_kind,
       exercise.resistance_value, exercise.resistance_text, exercise.execution_intent, exercise.tempo,
       exercise.notes AS exercise_notes
FROM training_groups group_row
LEFT JOIN teams team ON team.id = group_row.team_id
JOIN programmes programme ON programme.id = COALESCE(group_row.programme_id, team.programme_id)
LEFT JOIN training_plans plan ON plan.training_group_id = group_row.id
LEFT JOIN seasons season ON season.id = plan.season_id
LEFT JOIN training_sessions session ON session.plan_id = plan.id
LEFT JOIN training_session_segments segment ON segment.session_id = session.id
LEFT JOIN training_segment_blocks block ON block.segment_id = segment.id
LEFT JOIN gym_block_prescriptions gym ON gym.block_id = block.id
LEFT JOIN gym_exercises exercise ON exercise.block_id = gym.block_id
LEFT JOIN water_block_prescriptions water ON water.block_id = block.id
LEFT JOIN water_intensity_profiles water_profile ON water_profile.id = water.intensity_profile_id
LEFT JOIN water_work_steps water_step ON water_step.block_id = water.block_id
LEFT JOIN water_intensity_zones water_zone ON water_zone.profile_id = water.intensity_profile_id
 AND water_zone.code = water_step.intensity_code
WHERE sqlc.arg(is_admin)::boolean
   OR EXISTS (
       SELECT 1 FROM staff_grants grant_row
       WHERE grant_row.user_id = sqlc.arg(user_id)
         AND grant_row.capability = 'COACH'
         AND grant_row.revoked_at IS NULL
         AND (grant_row.programme_id = group_row.programme_id OR grant_row.team_id = group_row.team_id)
   )
ORDER BY group_row.name, group_row.id, plan.week_start DESC NULLS LAST, plan.id,
         session.starts_at NULLS LAST, session.id, segment.position, block.position,
         water_step.created_at NULLS FIRST, exercise.position;

-- name: ListStructuredTrainingOverviewForSubject :many
SELECT subject.id AS athlete_id, subject.name AS athlete_name,
       group_row.id AS group_id, group_row.name AS group_name,
       plan.id AS plan_id, plan.title AS plan_title, plan.description AS plan_description, season.name AS season_name, plan.week_start,
       session.id AS session_id, session.title AS session_title, session.description AS session_description,
       session.starts_at, session.ends_at, session.entry_kind,
       segment.id AS segment_id, segment.position AS segment_position, segment.modality AS segment_modality,
       segment.title AS segment_title, segment.location AS segment_location,
       segment.planned_duration_minutes, segment.planned_start_offset_minutes,
       segment.transition_duration_minutes, segment.equipment_notes,
       block.id AS block_id, block.position AS block_position, block.purpose AS block_purpose,
       block.title AS block_title, block.instructions AS block_instructions,
       gym.structure AS gym_structure, gym.objective AS gym_objective, gym.rounds AS gym_rounds,
       gym.round_recovery_seconds,
       water.method AS water_method, water.intensity_profile_id AS water_intensity_profile_id,
       water_profile.name AS water_profile_name, water_profile.revision AS water_profile_revision,
       water_profile.craft AS water_profile_craft,
       water.target_distance_metres AS water_target_distance_metres,
       water.target_distance_certainty AS water_target_distance_certainty,
       water_step.id AS water_step_id, water_step.parent_step_id AS water_parent_step_id,
       water_step.position AS water_step_position, water_step.kind AS water_step_kind,
       water_step.name AS water_step_name, water_step.repeats AS water_step_repeats,
       water_step.duration_seconds AS water_step_duration_seconds,
       water_step.duration_certainty AS water_step_duration_certainty,
       water_step.distance_metres AS water_step_distance_metres,
       water_step.distance_certainty AS water_step_distance_certainty,
       water_step.recovery_seconds AS water_step_recovery_seconds,
       water_step.intensity_code AS water_step_intensity_code, water_step.cadence_spm AS water_step_cadence_spm,
       water_zone.label AS water_zone_label, water_zone.cadence_min AS water_zone_cadence_min,
       water_zone.cadence_max AS water_zone_cadence_max, water_zone.meaning AS water_zone_meaning,
       water_step.drill_focus AS water_step_drill_focus, water_step.drill_format AS water_step_drill_format,
       water_step.role_notes AS water_step_role_notes, water_step.instructions AS water_step_instructions,
       exercise.id AS exercise_id, exercise.position AS exercise_position, exercise.name AS exercise_name,
       exercise.sets AS exercise_sets, exercise.repetitions AS exercise_repetitions,
       exercise.duration_seconds AS exercise_duration_seconds, exercise.distance_metres AS exercise_distance_metres,
       exercise.recovery_seconds AS exercise_recovery_seconds, exercise.resistance_kind,
       exercise.resistance_value, exercise.resistance_text, exercise.execution_intent, exercise.tempo,
       exercise.notes AS exercise_notes
FROM training_group_members group_member
JOIN user_memberships membership ON membership.id = group_member.membership_id
JOIN users subject ON subject.id = membership.user_id
JOIN training_groups group_row ON group_row.id = group_member.group_id
JOIN training_plans plan ON plan.training_group_id = group_row.id
JOIN seasons season ON season.id = plan.season_id
LEFT JOIN training_sessions session ON session.plan_id = plan.id
LEFT JOIN training_session_segments segment ON segment.session_id = session.id
LEFT JOIN training_segment_blocks block ON block.segment_id = segment.id
LEFT JOIN gym_block_prescriptions gym ON gym.block_id = block.id
LEFT JOIN gym_exercises exercise ON exercise.block_id = gym.block_id
LEFT JOIN water_block_prescriptions water ON water.block_id = block.id
LEFT JOIN water_intensity_profiles water_profile ON water_profile.id = water.intensity_profile_id
LEFT JOIN water_work_steps water_step ON water_step.block_id = water.block_id
LEFT JOIN water_intensity_zones water_zone ON water_zone.profile_id = water.intensity_profile_id
 AND water_zone.code = water_step.intensity_code
WHERE (subject.id = sqlc.arg(user_id)
       OR (subject.guardian_id = sqlc.arg(user_id) AND subject.date_of_birth > CURRENT_DATE - INTERVAL '18 years'))
  AND subject.is_active
  AND membership.starts_on <= CURRENT_DATE
  AND (membership.ends_on IS NULL OR membership.ends_on >= CURRENT_DATE)
ORDER BY subject.name, subject.id, group_row.name, group_row.id, plan.week_start DESC, plan.id,
         session.starts_at NULLS LAST, session.id, segment.position, block.position,
         water_step.created_at NULLS FIRST, exercise.position;

-- name: GetStructuredSessionPlanID :one
SELECT session.plan_id
FROM training_sessions session
JOIN training_plans plan ON plan.id = session.plan_id
WHERE session.id = sqlc.arg(session_id) AND plan.training_group_id IS NOT NULL;

-- name: GetStructuredSegmentPlanID :one
SELECT session.plan_id
FROM training_session_segments segment
JOIN training_sessions session ON session.id = segment.session_id
JOIN training_plans plan ON plan.id = session.plan_id
WHERE segment.id = sqlc.arg(segment_id) AND plan.training_group_id IS NOT NULL;

-- name: GetStructuredBlockPlanID :one
SELECT session.plan_id
FROM training_segment_blocks block
JOIN training_session_segments segment ON segment.id = block.segment_id
JOIN training_sessions session ON session.id = segment.session_id
JOIN training_plans plan ON plan.id = session.plan_id
WHERE block.id = sqlc.arg(block_id) AND plan.training_group_id IS NOT NULL;

-- name: GetGymExercisePlanID :one
SELECT session.plan_id
FROM gym_exercises exercise
JOIN training_segment_blocks block ON block.id = exercise.block_id
JOIN training_session_segments segment ON segment.id = block.segment_id
JOIN training_sessions session ON session.id = segment.session_id
JOIN training_plans plan ON plan.id = session.plan_id
WHERE exercise.id = sqlc.arg(exercise_id) AND plan.training_group_id IS NOT NULL;

-- name: GetBlockRoutineSource :one
SELECT session.plan_id, block.updated_at AS source_updated_at, plan.programme_id, plan.team_id,
       segment.modality, gym.objective, training_block_snapshot(block.id) AS snapshot
FROM training_segment_blocks block
JOIN training_session_segments segment ON segment.id = block.segment_id
JOIN training_sessions session ON session.id = segment.session_id
JOIN training_plans plan ON plan.id = session.plan_id AND plan.training_group_id IS NOT NULL
LEFT JOIN gym_block_prescriptions gym ON gym.block_id = block.id
WHERE block.id = sqlc.arg(source_id) AND session.status = 'ACTIVE';

-- name: GetSegmentRoutineSource :one
SELECT session.plan_id, segment.updated_at AS source_updated_at, plan.programme_id, plan.team_id,
       segment.modality, NULL::training_objective AS objective, training_segment_snapshot(segment.id) AS snapshot
FROM training_session_segments segment
JOIN training_sessions session ON session.id = segment.session_id
JOIN training_plans plan ON plan.id = session.plan_id AND plan.training_group_id IS NOT NULL
WHERE segment.id = sqlc.arg(source_id) AND session.status = 'ACTIVE';

-- name: GetSessionRoutineSource :one
SELECT session.plan_id, session.updated_at AS source_updated_at, plan.programme_id, plan.team_id,
       NULL::training_segment_modality AS modality, NULL::training_objective AS objective,
       training_session_snapshot(session.id) AS snapshot
FROM training_sessions session
JOIN training_plans plan ON plan.id = session.plan_id AND plan.training_group_id IS NOT NULL
WHERE session.id = sqlc.arg(source_id) AND session.status = 'ACTIVE';

-- name: CreateTrainingRoutine :one
INSERT INTO training_routines (name, description, kind, visibility, owner_user_id, programme_id, team_id,
                               modality, objective, method, tags, source_id, source_updated_at, snapshot)
VALUES (sqlc.arg(name), sqlc.arg(description), sqlc.arg(kind)::training_routine_kind,
        sqlc.arg(visibility)::training_routine_visibility, sqlc.arg(owner_user_id),
        sqlc.narg(programme_id), sqlc.narg(team_id), sqlc.narg(modality)::training_segment_modality,
        sqlc.narg(objective)::training_objective, sqlc.arg(method), sqlc.arg(tags),
        sqlc.arg(source_id), sqlc.arg(source_updated_at), sqlc.arg(snapshot))
RETURNING *;

-- name: ListVisibleTrainingRoutines :many
SELECT routine.id, routine.name, routine.description, routine.kind, routine.visibility,
       routine.owner_user_id, owner.name AS owner_name, routine.programme_id, programme.name_pt AS programme_name,
       routine.team_id, team.name AS team_name, routine.modality, routine.objective, routine.method, routine.tags,
       routine.source_id, routine.source_updated_at, routine.snapshot, routine.created_at, routine.updated_at
FROM training_routines routine
JOIN users owner ON owner.id = routine.owner_user_id
LEFT JOIN programmes programme ON programme.id = routine.programme_id
LEFT JOIN teams team ON team.id = routine.team_id
WHERE (routine.owner_user_id = sqlc.arg(user_id)
       OR sqlc.arg(is_admin)::boolean
       OR (routine.visibility = 'SHARED' AND EXISTS (
           SELECT 1 FROM staff_grants grant_row
           WHERE grant_row.user_id = sqlc.arg(user_id) AND grant_row.capability = 'COACH'
             AND grant_row.revoked_at IS NULL
             AND (grant_row.programme_id = routine.programme_id OR grant_row.team_id = routine.team_id))))
  AND (sqlc.arg(query)::text = '' OR routine.name ILIKE '%' || sqlc.arg(query) || '%'
       OR routine.description ILIKE '%' || sqlc.arg(query) || '%' OR routine.method ILIKE '%' || sqlc.arg(query) || '%')
  AND (sqlc.arg(modality)::text = '' OR routine.modality::text = sqlc.arg(modality))
  AND (sqlc.arg(objective)::text = '' OR routine.objective::text = sqlc.arg(objective))
  AND (sqlc.arg(tag)::text = '' OR EXISTS (
      SELECT 1 FROM unnest(routine.tags) routine_tag
      WHERE lower(routine_tag) = lower(sqlc.arg(tag))))
ORDER BY routine.updated_at DESC, routine.id;

-- name: GetVisibleTrainingRoutine :one
SELECT routine.*
FROM training_routines routine
WHERE routine.id = sqlc.arg(routine_id)
  AND (routine.owner_user_id = sqlc.arg(user_id)
       OR sqlc.arg(is_admin)::boolean
       OR (routine.visibility = 'SHARED' AND EXISTS (
           SELECT 1 FROM staff_grants grant_row
           WHERE grant_row.user_id = sqlc.arg(user_id) AND grant_row.capability = 'COACH'
             AND grant_row.revoked_at IS NULL
             AND (grant_row.programme_id = routine.programme_id OR grant_row.team_id = routine.team_id))));

-- name: RestoreTrainingBlock :one
SELECT restore_training_block(sqlc.arg(snapshot)::jsonb, sqlc.arg(segment_id));

-- name: RestoreTrainingSegment :one
SELECT restore_training_segment(sqlc.arg(snapshot)::jsonb, sqlc.arg(session_id));

-- name: RestoreTrainingSession :one
SELECT restore_training_session(sqlc.arg(snapshot)::jsonb, sqlc.arg(plan_id), sqlc.arg(starts_at), sqlc.arg(created_by_id));

-- name: CreateTrainingCopyEvent :exec
INSERT INTO training_copy_events (source_kind, source_id, source_updated_at, destination_kind, destination_id, copied_by_id)
VALUES (sqlc.arg(source_kind), sqlc.arg(source_id), sqlc.arg(source_updated_at), sqlc.arg(destination_kind),
        sqlc.arg(destination_id), sqlc.arg(copied_by_id));

-- name: GetStructuredPlanCopySource :one
SELECT plan.id, plan.training_group_id, plan.title, plan.description, plan.week_start, plan.updated_at
FROM training_plans plan
WHERE plan.id = sqlc.arg(plan_id) AND plan.training_group_id IS NOT NULL;

-- name: ListStructuredSessionSnapshotsForPlan :many
SELECT session.id, session.starts_at, session.updated_at, training_session_snapshot(session.id) AS snapshot
FROM training_sessions session
WHERE session.plan_id = sqlc.arg(plan_id) AND session.status = 'ACTIVE'
ORDER BY session.starts_at, session.id;

-- name: ListStructuredSessionSnapshotsForDay :many
SELECT session.id, session.starts_at, session.updated_at, training_session_snapshot(session.id) AS snapshot
FROM training_sessions session
WHERE session.plan_id = sqlc.arg(plan_id) AND session.status = 'ACTIVE'
  AND (session.starts_at AT TIME ZONE 'Europe/Lisbon')::date = sqlc.arg(source_date)::date
ORDER BY session.starts_at, session.id;

-- name: ListManagedTrainingGroupMembers :many
SELECT group_row.id AS training_group_id, group_row.name AS training_group_name,
       membership.id AS membership_id, subject.name AS athlete_name
FROM training_groups group_row
JOIN training_group_members group_member ON group_member.group_id = group_row.id
JOIN user_memberships membership ON membership.id = group_member.membership_id
JOIN users subject ON subject.id = membership.user_id AND subject.is_active
WHERE membership.starts_on <= CURRENT_DATE
  AND (membership.ends_on IS NULL OR membership.ends_on >= CURRENT_DATE)
  AND (sqlc.arg(is_admin)::boolean
   OR EXISTS (
       SELECT 1 FROM staff_grants grant_row
       WHERE grant_row.user_id = sqlc.arg(user_id)
         AND grant_row.capability = 'COACH'
         AND grant_row.revoked_at IS NULL
         AND (grant_row.programme_id = group_row.programme_id OR grant_row.team_id = group_row.team_id)
   ))
ORDER BY group_row.name, subject.name, membership.id;

-- name: ListStructuredCrewModalities :many
SELECT id, code, name_pt
FROM modalities
WHERE code ~ '^[A-Z]+[2-9][0-9]*$'
ORDER BY code;

-- name: ListManagedStructuredCompetitionEvents :many
SELECT event_row.id, event_row.title, event_row.starts_at
FROM events event_row
WHERE event_row.event_type = 'COMPETITION'
  AND event_row.status = 'ACTIVE'
  AND event_row.starts_at >= CURRENT_DATE
  AND (
      sqlc.arg(is_admin)::boolean
      OR EXISTS (
          SELECT 1
          FROM event_audiences audience
          JOIN staff_grants grant_row ON true
          LEFT JOIN teams grant_team ON grant_team.id = grant_row.team_id
          WHERE audience.event_id = event_row.id
            AND grant_row.user_id = sqlc.arg(user_id)
            AND grant_row.capability = 'COACH'
            AND grant_row.revoked_at IS NULL
            AND (grant_row.programme_id = audience.programme_id OR grant_team.programme_id = audience.programme_id)
      )
  )
ORDER BY event_row.starts_at, event_row.id;

-- name: CreateTrainingVariationGroup :one
INSERT INTO training_variation_groups (training_group_id, name, kind, craft_modality_id,
                                       effective_from, effective_until, competition_event_id,
                                       open_ended_exception, created_by_id)
VALUES (sqlc.arg(training_group_id), sqlc.arg(name), sqlc.arg(kind)::training_variation_group_kind,
        sqlc.narg(craft_modality_id), sqlc.arg(effective_from), sqlc.narg(effective_until),
        sqlc.narg(competition_event_id), sqlc.arg(open_ended_exception), sqlc.arg(created_by_id))
RETURNING *;

-- name: AddTrainingVariationGroupMember :execrows
INSERT INTO training_variation_group_members (variation_group_id, membership_id, added_by_id)
SELECT variation_group.id, group_member.membership_id, sqlc.arg(added_by_id)
FROM training_variation_groups variation_group
JOIN training_group_members group_member ON group_member.group_id = variation_group.training_group_id
JOIN user_memberships membership ON membership.id = group_member.membership_id
WHERE variation_group.id = sqlc.arg(variation_group_id)
  AND group_member.membership_id = sqlc.arg(membership_id)
  AND membership.starts_on <= CURRENT_DATE
  AND (membership.ends_on IS NULL OR membership.ends_on >= CURRENT_DATE)
ON CONFLICT (variation_group_id, membership_id) DO NOTHING;

-- name: ListManagedTrainingVariationGroups :many
SELECT variation_group.id, variation_group.training_group_id, group_row.name AS training_group_name,
       variation_group.name, variation_group.kind, modality.code AS craft_code,
       variation_group.effective_from, variation_group.effective_until,
       event_row.id AS competition_event_id, event_row.title AS competition_event_title,
       variation_group.open_ended_exception,
       membership.id AS membership_id, subject.name AS athlete_name
FROM training_variation_groups variation_group
JOIN training_groups group_row ON group_row.id = variation_group.training_group_id
LEFT JOIN modalities modality ON modality.id = variation_group.craft_modality_id
LEFT JOIN events event_row ON event_row.id = variation_group.competition_event_id
JOIN training_variation_group_members variation_member ON variation_member.variation_group_id = variation_group.id
JOIN user_memberships membership ON membership.id = variation_member.membership_id
JOIN users subject ON subject.id = membership.user_id
WHERE sqlc.arg(is_admin)::boolean
   OR EXISTS (
       SELECT 1 FROM staff_grants grant_row
       WHERE grant_row.user_id = sqlc.arg(user_id)
         AND grant_row.capability = 'COACH'
         AND grant_row.revoked_at IS NULL
         AND (grant_row.programme_id = group_row.programme_id OR grant_row.team_id = group_row.team_id)
   )
ORDER BY group_row.name, variation_group.name, subject.name, membership.id;

-- name: CreateTrainingVariation :one
WITH subject_plan AS (
    SELECT segment.id AS subject_id, 'SEGMENT'::training_variation_subject_kind AS subject_kind, session.plan_id,
           (session.starts_at AT TIME ZONE 'Europe/Lisbon')::date AS session_date
    FROM training_session_segments segment
    JOIN training_sessions session ON session.id = segment.session_id
    UNION ALL
    SELECT block.id, 'BLOCK'::training_variation_subject_kind, session.plan_id,
           (session.starts_at AT TIME ZONE 'Europe/Lisbon')::date
    FROM training_segment_blocks block
    JOIN training_session_segments segment ON segment.id = block.segment_id
    JOIN training_sessions session ON session.id = segment.session_id
    UNION ALL
    SELECT step.id, 'WATER_STEP'::training_variation_subject_kind, session.plan_id,
           (session.starts_at AT TIME ZONE 'Europe/Lisbon')::date
    FROM water_work_steps step
    JOIN training_segment_blocks block ON block.id = step.block_id
    JOIN training_session_segments segment ON segment.id = block.segment_id
    JOIN training_sessions session ON session.id = segment.session_id
    UNION ALL
    SELECT exercise.id, 'GYM_EXERCISE'::training_variation_subject_kind, session.plan_id,
           (session.starts_at AT TIME ZONE 'Europe/Lisbon')::date
    FROM gym_exercises exercise
    JOIN training_segment_blocks block ON block.id = exercise.block_id
    JOIN training_session_segments segment ON segment.id = block.segment_id
    JOIN training_sessions session ON session.id = segment.session_id
)
INSERT INTO training_variations (plan_id, target_membership_id, target_group_id, subject_kind,
                                 subject_id, operation, change_summary, patch, created_by_id)
SELECT plan.id, sqlc.narg(target_membership_id), sqlc.narg(target_group_id),
       sqlc.arg(subject_kind)::training_variation_subject_kind, sqlc.arg(subject_id),
       sqlc.arg(operation)::training_variation_operation, sqlc.arg(change_summary),
       sqlc.arg(patch)::jsonb, sqlc.arg(created_by_id)
FROM training_plans plan
JOIN subject_plan subject ON subject.plan_id = plan.id
                         AND subject.subject_id = sqlc.arg(subject_id)
                         AND subject.subject_kind = sqlc.arg(subject_kind)::training_variation_subject_kind
WHERE plan.id = sqlc.arg(plan_id)
  AND plan.training_group_id IS NOT NULL
  AND (
      (sqlc.narg(target_membership_id)::uuid IS NOT NULL AND EXISTS (
          SELECT 1 FROM training_group_members group_member
          JOIN user_memberships membership ON membership.id = group_member.membership_id
          WHERE group_member.group_id = plan.training_group_id
            AND group_member.membership_id = sqlc.narg(target_membership_id)
            AND membership.starts_on <= subject.session_date
            AND (membership.ends_on IS NULL OR membership.ends_on >= subject.session_date)
      ))
      OR
      (sqlc.narg(target_group_id)::uuid IS NOT NULL AND EXISTS (
          SELECT 1 FROM training_variation_groups variation_group
          WHERE variation_group.id = sqlc.narg(target_group_id)
            AND variation_group.training_group_id = plan.training_group_id
      ))
  )
RETURNING *;

-- name: GetTrainingVariationPlanID :one
SELECT plan_id FROM training_variations WHERE id = sqlc.arg(id);

-- name: RetireTrainingVariation :execrows
UPDATE training_variations
SET is_active = false, retired_at = clock_timestamp(), retired_by_id = sqlc.arg(retired_by_id),
    version = version + 1, updated_at = clock_timestamp()
WHERE id = sqlc.arg(id) AND version = sqlc.arg(version) AND is_active;

-- name: ListTrainingVariationMatchesForManager :many
WITH subject_details AS (
    SELECT segment.id AS subject_id, 'SEGMENT'::training_variation_subject_kind AS subject_kind,
           session.plan_id, session.id AS session_id, session.title AS session_title, session.starts_at,
           concat('Segmento · ', segment.position, ' · ', segment.modality::text,
                  CASE WHEN segment.title = '' THEN '' ELSE ' · ' || segment.title END)::text AS subject_label
    FROM training_session_segments segment
    JOIN training_sessions session ON session.id = segment.session_id
    UNION ALL
    SELECT block.id, 'BLOCK'::training_variation_subject_kind, session.plan_id, session.id,
           session.title, session.starts_at,
           concat('Bloco · ', block.position, ' · ', block.purpose::text,
                  CASE WHEN block.title = '' THEN '' ELSE ' · ' || block.title END)::text
    FROM training_segment_blocks block
    JOIN training_session_segments segment ON segment.id = block.segment_id
    JOIN training_sessions session ON session.id = segment.session_id
    UNION ALL
    SELECT step.id, 'WATER_STEP'::training_variation_subject_kind, session.plan_id, session.id,
           session.title, session.starts_at,
           concat('Passo de água · ', step.position, ' · ', step.name)::text
    FROM water_work_steps step
    JOIN training_segment_blocks block ON block.id = step.block_id
    JOIN training_session_segments segment ON segment.id = block.segment_id
    JOIN training_sessions session ON session.id = segment.session_id
    UNION ALL
    SELECT exercise.id, 'GYM_EXERCISE'::training_variation_subject_kind, session.plan_id, session.id,
           session.title, session.starts_at,
           concat('Exercício · ', exercise.position, ' · ', exercise.name)::text
    FROM gym_exercises exercise
    JOIN training_segment_blocks block ON block.id = exercise.block_id
    JOIN training_session_segments segment ON segment.id = block.segment_id
    JOIN training_sessions session ON session.id = segment.session_id
), target_members AS (
    SELECT variation.id AS variation_id, variation.target_membership_id AS membership_id,
           2::integer AS priority, 'Atleta'::text AS target_kind, subject.name AS target_name,
           NULL::date AS effective_from, NULL::date AS effective_until
    FROM training_variations variation
    JOIN user_memberships membership ON membership.id = variation.target_membership_id
    JOIN users subject ON subject.id = membership.user_id
    WHERE variation.target_membership_id IS NOT NULL
    UNION ALL
    SELECT variation.id, variation_member.membership_id, 1,
           CASE variation_group.kind WHEN 'CREW' THEN 'Tripulação' ELSE 'Subgrupo' END,
           variation_group.name, variation_group.effective_from,
           COALESCE(variation_group.effective_until, (event_row.starts_at AT TIME ZONE sqlc.arg(time_zone)::text)::date)
    FROM training_variations variation
    JOIN training_variation_groups variation_group ON variation_group.id = variation.target_group_id
    JOIN training_variation_group_members variation_member ON variation_member.variation_group_id = variation_group.id
    LEFT JOIN events event_row ON event_row.id = variation_group.competition_event_id
    WHERE variation.target_group_id IS NOT NULL
)
SELECT variation.id AS variation_id, variation.plan_id, plan.title AS plan_title,
       details.session_id, details.session_title, details.starts_at,
       variation.subject_kind, variation.subject_id, details.subject_label,
       variation.operation, variation.change_summary, variation.patch, variation.version,
       targets.membership_id, athlete.name AS athlete_name, targets.priority,
       targets.target_kind, targets.target_name, targets.effective_from, targets.effective_until
FROM training_variations variation
JOIN training_plans plan ON plan.id = variation.plan_id
JOIN training_groups group_row ON group_row.id = plan.training_group_id
JOIN subject_details details ON details.plan_id = variation.plan_id
                            AND details.subject_id = variation.subject_id
                            AND details.subject_kind = variation.subject_kind
JOIN target_members targets ON targets.variation_id = variation.id
JOIN user_memberships membership ON membership.id = targets.membership_id
JOIN training_group_members current_group_member
  ON current_group_member.group_id = plan.training_group_id
 AND current_group_member.membership_id = targets.membership_id
JOIN users athlete ON athlete.id = membership.user_id
WHERE variation.is_active
  AND athlete.is_active
  AND membership.starts_on <= (details.starts_at AT TIME ZONE sqlc.arg(time_zone)::text)::date
  AND (membership.ends_on IS NULL OR membership.ends_on >= (details.starts_at AT TIME ZONE sqlc.arg(time_zone)::text)::date)
  AND (targets.effective_from IS NULL OR (details.starts_at AT TIME ZONE sqlc.arg(time_zone)::text)::date >= targets.effective_from)
  AND (targets.effective_until IS NULL OR (details.starts_at AT TIME ZONE sqlc.arg(time_zone)::text)::date <= targets.effective_until)
  AND (
      sqlc.arg(is_admin)::boolean
      OR EXISTS (
          SELECT 1 FROM staff_grants grant_row
          WHERE grant_row.user_id = sqlc.arg(user_id)
            AND grant_row.capability = 'COACH'
            AND grant_row.revoked_at IS NULL
            AND (grant_row.programme_id = group_row.programme_id OR grant_row.team_id = group_row.team_id)
      )
  )
ORDER BY plan.week_start DESC, details.starts_at, athlete.name, details.subject_label,
         targets.priority DESC, variation.created_at, variation.id;

-- name: ListManagedTrainingPublicationStates :many
SELECT plan.id, plan.title, plan.updated_at AS source_updated_at,
       COALESCE(latest.revision, 0)::integer AS published_revision,
       latest.published_at, publisher.name AS published_by_name,
       (latest.source_updated_at IS NOT NULL AND latest.source_updated_at = plan.updated_at) AS publication_current
FROM training_plans plan
LEFT JOIN LATERAL (
 SELECT publication.revision, publication.source_updated_at, publication.published_at, publication.published_by_id
 FROM training_plan_publications publication
 WHERE publication.plan_id = plan.id
 ORDER BY publication.revision DESC
 LIMIT 1
) latest ON true
LEFT JOIN users publisher ON publisher.id = latest.published_by_id
WHERE plan.training_group_id IS NOT NULL
  AND (sqlc.arg(is_admin)::boolean OR EXISTS (
    SELECT 1 FROM staff_grants grant_row
    WHERE grant_row.user_id = sqlc.arg(user_id)
      AND grant_row.capability = 'COACH' AND grant_row.revoked_at IS NULL
      AND (grant_row.programme_id = plan.programme_id OR grant_row.team_id = plan.team_id)
  ))
ORDER BY plan.week_start DESC, plan.id;

-- name: ListStructuredTrainingPublicationMembers :many
SELECT DISTINCT membership.id AS membership_id, membership.user_id AS athlete_user_id,
       subject.name AS athlete_name, membership.starts_on, membership.ends_on, session.id AS session_id
FROM training_plans plan
JOIN training_group_members group_member ON group_member.group_id = plan.training_group_id
JOIN user_memberships membership ON membership.id = group_member.membership_id
JOIN users subject ON subject.id = membership.user_id AND subject.is_active
JOIN training_sessions session ON session.plan_id = plan.id
WHERE plan.id = sqlc.arg(plan_id)
  AND (session.starts_at AT TIME ZONE sqlc.arg(time_zone)::text)::date >= membership.starts_on
  AND (membership.ends_on IS NULL OR (session.starts_at AT TIME ZONE sqlc.arg(time_zone)::text)::date <= membership.ends_on)
ORDER BY subject.name, membership.id, session.id;

-- name: LockStructuredTrainingPlanForPublication :one
SELECT plan.id, plan.updated_at,
       COALESCE(latest.id, '00000000-0000-0000-0000-000000000000'::uuid) AS latest_publication_id,
       COALESCE(latest.revision, 0)::integer AS latest_revision
FROM training_plans plan
LEFT JOIN LATERAL (
 SELECT publication.id, publication.revision
 FROM training_plan_publications publication
 WHERE publication.plan_id = plan.id
 ORDER BY publication.revision DESC
 LIMIT 1
) latest ON true
WHERE plan.id = sqlc.arg(plan_id) AND plan.training_group_id IS NOT NULL
FOR UPDATE OF plan;

-- name: CreateTrainingPlanPublication :one
INSERT INTO training_plan_publications (
 plan_id, revision, source_updated_at, change_summary, supersedes_id, published_by_id
) VALUES (
 sqlc.arg(plan_id), sqlc.arg(revision), sqlc.arg(source_updated_at), sqlc.arg(change_summary),
 sqlc.narg(supersedes_id), sqlc.arg(published_by_id)
)
RETURNING id, plan_id, revision, source_updated_at, change_summary, supersedes_id, published_by_id, published_at;

-- name: CreateTrainingPrescription :one
INSERT INTO training_prescriptions (
 publication_id, session_id, membership_id, athlete_user_id, snapshot, snapshot_sha256
) SELECT sqlc.arg(publication_id), session.id, membership.id, athlete.id,
         sqlc.arg(snapshot), sqlc.arg(snapshot_sha256)
FROM training_plan_publications publication
JOIN training_plans plan ON plan.id = publication.plan_id
JOIN training_sessions session ON session.id = sqlc.arg(session_id) AND session.plan_id = plan.id
JOIN user_memberships membership ON membership.id = sqlc.arg(membership_id) AND membership.user_id = sqlc.arg(athlete_user_id)
JOIN users athlete ON athlete.id = membership.user_id AND athlete.is_active
JOIN training_group_members group_member ON group_member.group_id = plan.training_group_id AND group_member.membership_id = membership.id
WHERE publication.id = sqlc.arg(publication_id)
  AND membership.starts_on <= (session.starts_at AT TIME ZONE 'Europe/Lisbon')::date
  AND (membership.ends_on IS NULL OR membership.ends_on >= (session.starts_at AT TIME ZONE 'Europe/Lisbon')::date)
RETURNING id, publication_id, session_id, membership_id, athlete_user_id, snapshot, snapshot_sha256, created_at;

-- name: ListTrainingPrescriptionsForViewer :many
SELECT prescription.id, prescription.session_id, prescription.athlete_user_id, athlete.name AS athlete_name,
       publication.plan_id, plan.title AS plan_title, plan.week_start, season.name AS season_name,
       publication.revision, publication.change_summary, publication.published_at,
       publisher.name AS published_by_name, prescription.snapshot,
       publication.id = latest.id AS is_current,
       COALESCE(outcome.status::text, ''::text) AS outcome_status,
       outcome.distance_metres, outcome.actual_duration_minutes, outcome.perceived_exertion,
       outcome.recovery_feeling, outcome.perception_note, outcome.updated_at AS outcome_updated_at,
       COALESCE(outcome.version, 0)::integer AS outcome_version
FROM training_prescriptions prescription
JOIN training_plan_publications publication ON publication.id = prescription.publication_id
JOIN training_plans plan ON plan.id = publication.plan_id
JOIN seasons season ON season.id = plan.season_id
JOIN users athlete ON athlete.id = prescription.athlete_user_id
JOIN users publisher ON publisher.id = publication.published_by_id
LEFT JOIN training_session_outcomes outcome
  ON outcome.prescription_id = prescription.id
 AND outcome.user_id = prescription.athlete_user_id
JOIN LATERAL (
 SELECT current_publication.id
 FROM training_plan_publications current_publication
 WHERE current_publication.plan_id = publication.plan_id
 ORDER BY current_publication.revision DESC
 LIMIT 1
) latest ON true
WHERE (
    athlete.id = sqlc.arg(user_id)
    OR (athlete.guardian_id = sqlc.arg(user_id) AND athlete.date_of_birth > CURRENT_DATE - INTERVAL '18 years')
    OR sqlc.arg(is_admin)::boolean
    OR EXISTS (
      SELECT 1 FROM staff_grants grant_row
      WHERE grant_row.user_id = sqlc.arg(user_id)
        AND grant_row.capability = 'COACH' AND grant_row.revoked_at IS NULL
        AND (grant_row.programme_id = plan.programme_id OR grant_row.team_id = plan.team_id)
    )
  )
ORDER BY publication.published_at DESC, athlete.name, prescription.session_id, prescription.id;

-- name: ListLatestTrainingPrescriptionHashesForPlan :many
SELECT prescription.membership_id, prescription.session_id, prescription.snapshot_sha256
FROM training_prescriptions prescription
JOIN training_plan_publications publication ON publication.id = prescription.publication_id
WHERE publication.plan_id = sqlc.arg(plan_id)
  AND publication.revision = (
    SELECT max(current_publication.revision)
    FROM training_plan_publications current_publication
    WHERE current_publication.plan_id = sqlc.arg(plan_id)
  )
ORDER BY prescription.membership_id, prescription.session_id;

-- name: GetTrainingPrescriptionForViewer :one
SELECT prescription.id, prescription.session_id, prescription.athlete_user_id, athlete.name AS athlete_name,
       publication.plan_id, plan.title AS plan_title, plan.week_start, season.name AS season_name,
       publication.revision, publication.change_summary, publication.published_at,
       publisher.name AS published_by_name, prescription.snapshot,
       publication.id = latest.id AS is_current,
       COALESCE(outcome.status::text, ''::text) AS outcome_status,
       outcome.distance_metres, outcome.actual_duration_minutes, outcome.perceived_exertion,
       outcome.recovery_feeling, outcome.perception_note, outcome.updated_at AS outcome_updated_at,
       COALESCE(outcome.version, 0)::integer AS outcome_version
FROM training_prescriptions prescription
JOIN training_plan_publications publication ON publication.id = prescription.publication_id
JOIN training_plans plan ON plan.id = publication.plan_id
JOIN seasons season ON season.id = plan.season_id
JOIN users athlete ON athlete.id = prescription.athlete_user_id
JOIN users publisher ON publisher.id = publication.published_by_id
LEFT JOIN training_session_outcomes outcome
  ON outcome.prescription_id = prescription.id
 AND outcome.user_id = prescription.athlete_user_id
JOIN LATERAL (
 SELECT current_publication.id
 FROM training_plan_publications current_publication
 WHERE current_publication.plan_id = publication.plan_id
 ORDER BY current_publication.revision DESC
 LIMIT 1
) latest ON true
WHERE prescription.id = sqlc.arg(id)
  AND (
    athlete.id = sqlc.arg(user_id)
    OR (athlete.guardian_id = sqlc.arg(user_id) AND athlete.date_of_birth > CURRENT_DATE - INTERVAL '18 years')
    OR sqlc.arg(is_admin)::boolean
    OR EXISTS (
      SELECT 1 FROM staff_grants grant_row
      WHERE grant_row.user_id = sqlc.arg(user_id)
        AND grant_row.capability = 'COACH' AND grant_row.revoked_at IS NULL
        AND (grant_row.programme_id = plan.programme_id OR grant_row.team_id = plan.team_id)
    )
  );

-- name: ListTrainingPrescriptionLinksForSessionViewer :many
SELECT prescription.id, prescription.athlete_user_id, athlete.name AS athlete_name,
       publication.revision, publication.published_at
FROM training_prescriptions prescription
JOIN training_plan_publications publication ON publication.id = prescription.publication_id
JOIN training_plans plan ON plan.id = publication.plan_id
JOIN users athlete ON athlete.id = prescription.athlete_user_id
WHERE prescription.session_id = sqlc.arg(session_id)
  AND publication.id = (
    SELECT current_publication.id FROM training_plan_publications current_publication
    WHERE current_publication.plan_id = publication.plan_id
    ORDER BY current_publication.revision DESC LIMIT 1
  )
  AND (
    athlete.id = sqlc.arg(user_id)
    OR (athlete.guardian_id = sqlc.arg(user_id) AND athlete.date_of_birth > CURRENT_DATE - INTERVAL '18 years')
    OR sqlc.arg(is_admin)::boolean
    OR EXISTS (
      SELECT 1 FROM staff_grants grant_row
      WHERE grant_row.user_id = sqlc.arg(user_id)
        AND grant_row.capability = 'COACH' AND grant_row.revoked_at IS NULL
        AND (grant_row.programme_id = plan.programme_id OR grant_row.team_id = plan.team_id)
    )
  )
ORDER BY athlete.name, prescription.id;
