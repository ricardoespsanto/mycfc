-- Corrected recipient publication: exact actor/group scope and bounded typed source.
-- Shared projections are intentionally repeated for independently authorized statements.

-- name: PublicationPlanForReview :one
WITH RECURSIVE actor AS (
 SELECT actor_user.id,EXISTS(SELECT 1 FROM public.user_platform_roles assignment JOIN public.platform_roles role ON role.id=assignment.role_id WHERE assignment.user_id=actor_user.id AND role.code='ADMIN') AS admin
 FROM public.users actor_user WHERE actor_user.id=sqlc.arg(actor_id)::uuid AND actor_user.is_active AND actor_user.erased_at IS NULL AND NOT actor_user.is_dependent
 AND actor_user.date_of_birth<=((now() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date
), scoped AS MATERIALIZED (
 SELECT plan.id,plan.title,plan.description,plan.programme_id,plan.team_id,plan.training_group_id,plan.season_id,plan.week_start,plan.planned_load_percentage,plan.updated_at,
 group_row.id AS group_id,group_row.name AS group_name,group_row.programme_id AS group_programme_id,group_row.team_id AS group_team_id
 FROM public.training_plans plan JOIN public.training_groups group_row ON group_row.id=plan.training_group_id JOIN public.seasons season ON season.id=plan.season_id CROSS JOIN actor
 WHERE plan.id=sqlc.arg(plan_id)::uuid AND (actor.admin OR EXISTS(SELECT 1 FROM public.staff_grants grant_row WHERE grant_row.user_id=actor.id AND grant_row.capability='COACH' AND grant_row.revoked_at IS NULL
 AND ((group_row.programme_id IS NOT NULL AND grant_row.programme_id=group_row.programme_id) OR (group_row.team_id IS NOT NULL AND grant_row.team_id=group_row.team_id))))
), header AS (
 SELECT plan.id AS plan_id,plan.group_id,plan.season_id,plan.week_start,plan.updated_at AS source_updated_at,
 latest.id AS latest_publication_id,COALESCE(latest.revision,0)::integer AS latest_revision,latest.supersedes_id AS latest_supersedes_id,latest.source_updated_at AS latest_source_updated_at
 FROM scoped plan LEFT JOIN public.training_plan_publications latest ON latest.id=(SELECT publication.id FROM public.training_plan_publications publication WHERE publication.plan_id=plan.id ORDER BY publication.revision DESC LIMIT 1)
)
SELECT header.plan_id,header.group_id,header.season_id,header.week_start,header.source_updated_at,header.latest_publication_id,header.latest_revision,header.latest_supersedes_id,header.latest_source_updated_at FROM header;

-- name: LockPublicationPlan :one
WITH RECURSIVE actor AS (
 SELECT actor_user.id,EXISTS(SELECT 1 FROM public.user_platform_roles assignment JOIN public.platform_roles role ON role.id=assignment.role_id WHERE assignment.user_id=actor_user.id AND role.code='ADMIN') AS admin
 FROM public.users actor_user WHERE actor_user.id=sqlc.arg(actor_id)::uuid AND actor_user.is_active AND actor_user.erased_at IS NULL AND NOT actor_user.is_dependent
 AND actor_user.date_of_birth<=((now() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date
), scoped AS MATERIALIZED (
 SELECT plan.id,plan.title,plan.description,plan.programme_id,plan.team_id,plan.training_group_id,plan.season_id,plan.week_start,plan.planned_load_percentage,plan.updated_at,
 group_row.id AS group_id,group_row.name AS group_name,group_row.programme_id AS group_programme_id,group_row.team_id AS group_team_id
 FROM public.training_plans plan JOIN public.training_groups group_row ON group_row.id=plan.training_group_id JOIN public.seasons season ON season.id=plan.season_id CROSS JOIN actor
 WHERE plan.id=sqlc.arg(plan_id)::uuid AND (actor.admin OR EXISTS(SELECT 1 FROM public.staff_grants grant_row WHERE grant_row.user_id=actor.id AND grant_row.capability='COACH' AND grant_row.revoked_at IS NULL
 AND ((group_row.programme_id IS NOT NULL AND grant_row.programme_id=group_row.programme_id) OR (group_row.team_id IS NOT NULL AND grant_row.team_id=group_row.team_id))))
), header AS (
 SELECT plan.id AS plan_id,plan.group_id,plan.season_id,plan.week_start,plan.updated_at AS source_updated_at,
 latest.id AS latest_publication_id,COALESCE(latest.revision,0)::integer AS latest_revision,latest.supersedes_id AS latest_supersedes_id,latest.source_updated_at AS latest_source_updated_at
 FROM scoped plan LEFT JOIN public.training_plan_publications latest ON latest.id=(SELECT publication.id FROM public.training_plan_publications publication WHERE publication.plan_id=plan.id ORDER BY publication.revision DESC LIMIT 1)
)
SELECT header.plan_id,header.group_id,header.season_id,header.week_start,header.source_updated_at,header.latest_publication_id,header.latest_revision,header.latest_supersedes_id,header.latest_source_updated_at FROM header JOIN public.training_plans locked_plan ON locked_plan.id=header.plan_id FOR UPDATE OF locked_plan;

-- name: PublicationSourceBudget :one
WITH RECURSIVE actor AS (
 SELECT actor_user.id,EXISTS(SELECT 1 FROM public.user_platform_roles assignment JOIN public.platform_roles role ON role.id=assignment.role_id WHERE assignment.user_id=actor_user.id AND role.code='ADMIN') AS admin
 FROM public.users actor_user WHERE actor_user.id=sqlc.arg(actor_id)::uuid AND actor_user.is_active AND actor_user.erased_at IS NULL AND NOT actor_user.is_dependent
 AND actor_user.date_of_birth<=((now() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date
), scoped AS MATERIALIZED (
 SELECT plan.id,plan.title,plan.description,plan.programme_id,plan.team_id,plan.training_group_id,plan.season_id,plan.week_start,plan.planned_load_percentage,plan.updated_at,
 group_row.id AS group_id,group_row.name AS group_name,group_row.programme_id AS group_programme_id,group_row.team_id AS group_team_id
 FROM public.training_plans plan JOIN public.training_groups group_row ON group_row.id=plan.training_group_id JOIN public.seasons season ON season.id=plan.season_id CROSS JOIN actor
 WHERE plan.id=sqlc.arg(plan_id)::uuid AND (actor.admin OR EXISTS(SELECT 1 FROM public.staff_grants grant_row WHERE grant_row.user_id=actor.id AND grant_row.capability='COACH' AND grant_row.revoked_at IS NULL
 AND ((group_row.programme_id IS NOT NULL AND grant_row.programme_id=group_row.programme_id) OR (group_row.team_id IS NOT NULL AND grant_row.team_id=group_row.team_id))))
), header AS (
 SELECT plan.id AS plan_id,plan.group_id,plan.season_id,plan.week_start,plan.updated_at AS source_updated_at,
 latest.id AS latest_publication_id,COALESCE(latest.revision,0)::integer AS latest_revision,latest.supersedes_id AS latest_supersedes_id,latest.source_updated_at AS latest_source_updated_at
 FROM scoped plan LEFT JOIN public.training_plan_publications latest ON latest.id=(SELECT publication.id FROM public.training_plan_publications publication WHERE publication.plan_id=plan.id ORDER BY publication.revision DESC LIMIT 1)
),
candidates AS MATERIALIZED (
 SELECT DISTINCT session.id AS session_id,placement.id AS placement_id,membership.id AS membership_id,structural_subject.id AS subject_id,membership.starts_on,membership.ends_on,
 public.training_session_local_date(session.scheduled_on,session.starts_at) AS session_on
 FROM scoped plan JOIN public.training_group_placements placement ON placement.group_id=plan.group_id
 JOIN public.users structural_subject ON structural_subject.id=placement.subject_user_id
 JOIN public.training_sessions session ON session.plan_id=plan.id
 LEFT JOIN LATERAL public.training_placement_resolve(placement.subject_user_id,plan.group_id,plan.season_id,public.training_session_local_date(session.scheduled_on,session.starts_at)) resolved ON resolved.placement_id=placement.id
 LEFT JOIN public.user_memberships membership ON membership.id=resolved.membership_id
 AND(plan.programme_id IS NULL OR membership.programme_id=plan.programme_id) AND(plan.team_id IS NULL OR membership.team_id=plan.team_id)
 WHERE placement.subject_user_id IS NOT NULL AND placement.superseded_by_change_id IS NULL AND placement.cancelled_by_change_id IS NULL
 AND public.training_session_local_date(session.scheduled_on,session.starts_at) BETWEEN placement.starts_on AND COALESCE(placement.ends_on,'infinity'::date)
 ORDER BY session.id,placement.id,membership.id LIMIT 4097
), candidate_stats AS MATERIALIZED (SELECT count(*)::bigint AS raw_tuple_count FROM candidates), latest_hashes AS MATERIALIZED (
 SELECT prescription.session_id,prescription.placement_id,prescription.membership_id,historical_subject.id AS athlete_user_id,prescription.snapshot_sha256
 FROM header JOIN public.training_prescriptions prescription ON prescription.publication_id=header.latest_publication_id LEFT JOIN public.users historical_subject ON historical_subject.id=prescription.athlete_user_id ORDER BY prescription.session_id,prescription.membership_id LIMIT 4097
), latest_stats AS MATERIALIZED(SELECT count(*)::bigint AS latest_tuple_count FROM latest_hashes), input_gate AS MATERIALIZED (
 SELECT header.plan_id FROM header CROSS JOIN candidate_stats CROSS JOIN latest_stats WHERE candidate_stats.raw_tuple_count<=4096 AND latest_stats.latest_tuple_count<=4096
),
eligible_recipients AS (
 SELECT candidate.placement_id,candidate.membership_id,candidate.subject_id AS athlete_user_id,subject.name AS athlete_name,candidate.starts_on,candidate.ends_on,candidate.session_id
 FROM candidates candidate JOIN input_gate ON true JOIN public.users subject ON subject.id=candidate.subject_id
 WHERE candidate.membership_id IS NOT NULL AND subject.is_active AND subject.erased_at IS NULL AND (NOT subject.is_dependent OR EXISTS(SELECT 1 FROM public.guardian_authority_relationships relationship WHERE relationship.subject_user_id=subject.id AND public.guardian_authority_current(relationship.guardian_user_id,subject.id)))
),
unordered_overview AS (
SELECT 0::bigint AS _ord,group_row.id AS group_id, group_row.name AS group_name,
       programme.name_pt AS programme_name, team.name AS team_name,
       0::integer AS member_count,
       plan.id AS plan_id, plan.title AS plan_title, plan.description AS plan_description, season.name AS season_name, plan.week_start, plan.planned_load_percentage,
       session.id AS session_id, session.title AS session_title, session.description AS session_description,
       session.starts_at, session.ends_at, session.entry_kind,
       session.schedule_precision,session.scheduled_on,session.day_band,
       public.training_session_local_date(session.scheduled_on,session.starts_at) AS session_on, session.status AS session_status,
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
FROM input_gate JOIN scoped scoped_plan ON scoped_plan.id=input_gate.plan_id JOIN training_groups group_row ON group_row.id=scoped_plan.group_id
LEFT JOIN teams team ON team.id = group_row.team_id
JOIN programmes programme ON programme.id = COALESCE(group_row.programme_id, team.programme_id)
JOIN training_plans plan ON plan.id=scoped_plan.id
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

), water_nodes AS (
 SELECT DISTINCT water_step_id AS id,water_parent_step_id AS parent_id,water_step_position AS position FROM unordered_overview WHERE water_step_id IS NOT NULL
), water_tree(id,order_path,id_path) AS (
 SELECT root.id,ARRAY[root.position]::integer[],ARRAY[root.id]::uuid[] FROM water_nodes root WHERE root.parent_id IS NULL
 UNION ALL
 SELECT child.id,water_tree.order_path||child.position,water_tree.id_path||child.id FROM water_nodes child JOIN water_tree ON child.parent_id=water_tree.id WHERE NOT child.id=ANY(water_tree.id_path)
), overview AS (
 SELECT row_number() OVER(ORDER BY source.session_on NULLS LAST,source.starts_at NULLS LAST,source.session_id,source.segment_position,source.block_position,water_tree.order_path NULLS LAST,water_tree.id_path NULLS LAST,source.water_parent_step_id NULLS FIRST,source.water_step_position,source.water_step_id,source.exercise_position,source.exercise_id) AS _ord,source.group_id,source.group_name,source.programme_name,source.team_name,source.member_count,source.plan_id,source.plan_title,source.plan_description,source.season_name,source.week_start,source.planned_load_percentage,source.session_id,source.session_title,source.session_description,source.starts_at,source.ends_at,source.entry_kind,source.schedule_precision,source.scheduled_on,source.day_band,source.session_on,source.session_status,source.segment_id,source.segment_position,source.segment_modality,source.segment_title,source.segment_location,source.planned_duration_minutes,source.planned_start_offset_minutes,source.transition_duration_minutes,source.equipment_notes,source.block_id,source.block_position,source.block_purpose,source.block_title,source.block_instructions,source.gym_structure,source.gym_objective,source.gym_rounds,source.round_recovery_seconds,source.water_method,source.water_intensity_profile_id,source.water_profile_name,source.water_profile_revision,source.water_profile_craft,source.water_target_distance_metres,source.water_target_distance_certainty,source.water_step_id,source.water_parent_step_id,source.water_step_position,source.water_step_kind,source.water_step_name,source.water_step_repeats,source.water_step_duration_seconds,source.water_step_duration_certainty,source.water_step_distance_metres,source.water_step_distance_certainty,source.water_step_recovery_seconds,source.water_step_intensity_code,source.water_step_cadence_spm,source.water_zone_label,source.water_zone_cadence_min,source.water_zone_cadence_max,source.water_zone_meaning,source.water_step_drill_focus,source.water_step_drill_format,source.water_step_role_notes,source.water_step_instructions,source.exercise_id,source.exercise_position,source.exercise_name,source.exercise_sets,source.exercise_repetitions,source.exercise_duration_seconds,source.exercise_distance_metres,source.exercise_recovery_seconds,source.resistance_kind,source.resistance_value,source.resistance_text,source.execution_intent,source.tempo,source.exercise_notes
 FROM unordered_overview source LEFT JOIN water_tree ON water_tree.id=source.water_step_id
),
variation_definitions AS (
 SELECT variation.id AS variation_id,variation.plan_id,variation.subject_kind,variation.subject_id,variation.target_membership_id,variation.target_group_id,variation.operation,variation.patch,variation.change_summary,variation.version,variation.created_at
 FROM input_gate JOIN public.training_variations variation ON variation.plan_id=input_gate.plan_id WHERE variation.is_active
), variation_targets AS (
 SELECT variation.variation_id,variation.target_membership_id AS membership_id,2::integer AS priority,NULL::uuid AS target_group_id,'Atleta'::text AS target_kind,NULL::text AS target_group_name,NULL::date AS effective_from,NULL::date AS effective_until,NULL::uuid AS competition_event_id
 FROM variation_definitions variation WHERE variation.target_membership_id IS NOT NULL
 UNION ALL
 SELECT variation.variation_id,member.membership_id,1,variation_group.id,CASE variation_group.kind WHEN 'CREW' THEN 'Tripulação' ELSE 'Subgrupo' END,variation_group.name::text,variation_group.effective_from,COALESCE(variation_group.effective_until,(event_row.starts_at AT TIME ZONE 'Europe/Lisbon')::date),variation_group.competition_event_id
 FROM variation_definitions variation JOIN public.training_variation_groups variation_group ON variation_group.id=variation.target_group_id JOIN scoped plan ON plan.group_id=variation_group.training_group_id JOIN public.training_variation_group_members member ON member.variation_group_id=variation_group.id LEFT JOIN public.events event_row ON event_row.id=variation_group.competition_event_id
), referenced_events AS (
 SELECT DISTINCT event_row.id AS event_id,event_row.starts_at FROM variation_definitions variation JOIN public.training_variation_groups variation_group ON variation_group.id=variation.target_group_id JOIN scoped plan ON plan.group_id=variation_group.training_group_id JOIN public.events event_row ON event_row.id=variation_group.competition_event_id
),
subject_details AS (
    SELECT segment.id AS subject_id, 'SEGMENT'::training_variation_subject_kind AS subject_kind,
           session.plan_id, session.id AS session_id, session.title AS session_title, session.starts_at,
           public.training_session_local_date(session.scheduled_on,session.starts_at) AS session_on,
           session.ends_at,session.schedule_precision,session.scheduled_on,session.day_band,
           concat('Segmento · ', segment.position, ' · ', segment.modality::text,
                  CASE WHEN segment.title = '' THEN '' ELSE ' · ' || segment.title END)::text AS subject_label
    FROM input_gate JOIN training_sessions selected_session ON selected_session.plan_id=input_gate.plan_id JOIN training_session_segments segment ON segment.session_id=selected_session.id
    JOIN training_sessions session ON session.id = segment.session_id
    UNION ALL
    SELECT block.id, 'BLOCK'::training_variation_subject_kind, session.plan_id, session.id,
           session.title, session.starts_at,
           public.training_session_local_date(session.scheduled_on,session.starts_at),
           session.ends_at,session.schedule_precision,session.scheduled_on,session.day_band,
           concat('Bloco · ', block.position, ' · ', block.purpose::text,
                  CASE WHEN block.title = '' THEN '' ELSE ' · ' || block.title END)::text
    FROM input_gate JOIN training_sessions selected_session ON selected_session.plan_id=input_gate.plan_id JOIN training_session_segments selected_segment ON selected_segment.session_id=selected_session.id JOIN training_segment_blocks block ON block.segment_id=selected_segment.id
    JOIN training_session_segments segment ON segment.id = block.segment_id
    JOIN training_sessions session ON session.id = segment.session_id
    UNION ALL
    SELECT step.id, 'WATER_STEP'::training_variation_subject_kind, session.plan_id, session.id,
           session.title, session.starts_at,
           public.training_session_local_date(session.scheduled_on,session.starts_at),
           session.ends_at,session.schedule_precision,session.scheduled_on,session.day_band,
           concat('Passo de água · ', step.position, ' · ', step.name)::text
    FROM input_gate JOIN training_sessions selected_session ON selected_session.plan_id=input_gate.plan_id JOIN training_session_segments selected_segment ON selected_segment.session_id=selected_session.id JOIN training_segment_blocks selected_block ON selected_block.segment_id=selected_segment.id JOIN water_work_steps step ON step.block_id=selected_block.id
    JOIN training_segment_blocks block ON block.id = step.block_id
    JOIN training_session_segments segment ON segment.id = block.segment_id
    JOIN training_sessions session ON session.id = segment.session_id
    UNION ALL
    SELECT exercise.id, 'GYM_EXERCISE'::training_variation_subject_kind, session.plan_id, session.id,
           session.title, session.starts_at,
           public.training_session_local_date(session.scheduled_on,session.starts_at),
           session.ends_at,session.schedule_precision,session.scheduled_on,session.day_band,
           concat('Exercício · ', exercise.position, ' · ', exercise.name)::text
    FROM input_gate JOIN training_sessions selected_session ON selected_session.plan_id=input_gate.plan_id JOIN training_session_segments selected_segment ON selected_segment.session_id=selected_session.id JOIN training_segment_blocks selected_block ON selected_block.segment_id=selected_segment.id JOIN gym_exercises exercise ON exercise.block_id=selected_block.id
    JOIN training_segment_blocks block ON block.id = exercise.block_id
    JOIN training_session_segments segment ON segment.id = block.segment_id
    JOIN training_sessions session ON session.id = segment.session_id
),
variation_matches AS (
 SELECT variation.variation_id,variation.plan_id,plan.title AS plan_title,details.session_id,details.session_title,details.starts_at,details.session_on,details.ends_at,details.schedule_precision,details.scheduled_on,details.day_band,
 variation.subject_kind,variation.subject_id,details.subject_label,variation.operation,variation.change_summary,variation.patch,variation.version,targets.membership_id,recipient.athlete_name,targets.priority,targets.target_kind,
 COALESCE(CASE WHEN targets.priority=2 THEN recipient.athlete_name::text ELSE targets.target_group_name END,''::text)::text AS target_name,targets.effective_from,targets.effective_until,
 row_number() OVER(ORDER BY details.session_on,details.starts_at NULLS LAST,recipient.athlete_name,details.subject_label,targets.priority DESC,variation.created_at,variation.variation_id) AS _ord
 FROM variation_definitions variation JOIN scoped plan ON plan.id=variation.plan_id JOIN subject_details details ON details.plan_id=variation.plan_id AND details.subject_id=variation.subject_id AND details.subject_kind=variation.subject_kind
 JOIN variation_targets targets ON targets.variation_id=variation.variation_id JOIN eligible_recipients recipient ON recipient.membership_id=targets.membership_id AND recipient.session_id=details.session_id
 WHERE (targets.effective_from IS NULL OR details.session_on>=targets.effective_from) AND (targets.effective_until IS NULL OR details.session_on<=targets.effective_until)
),
header_record AS MATERIALIZED(SELECT (pg_catalog.jsonb_build_object('plan_id',header.plan_id,'group_id',header.group_id,'season_id',header.season_id,'week_start',header.week_start,'source_updated_at',header.source_updated_at,'latest_publication_id',header.latest_publication_id,'latest_revision',header.latest_revision,'latest_supersedes_id',header.latest_supersedes_id,'latest_source_updated_at',header.latest_source_updated_at))::text AS encoded FROM header),
records AS NOT MATERIALIZED (
SELECT 0::integer AS category,row_number() OVER(ORDER BY _ord) AS ordinal,(pg_catalog.jsonb_build_object('group_id',record.group_id,'group_name',record.group_name,'programme_name',record.programme_name,'team_name',record.team_name,'member_count',record.member_count,'plan_id',record.plan_id,'plan_title',record.plan_title,'plan_description',record.plan_description,'season_name',record.season_name,'week_start',record.week_start,'planned_load_percentage',record.planned_load_percentage,'session_id',record.session_id,'session_title',record.session_title,'session_description',record.session_description,'starts_at',record.starts_at,'ends_at',record.ends_at,'entry_kind',record.entry_kind,'schedule_precision',record.schedule_precision,'scheduled_on',record.scheduled_on,'day_band',record.day_band,'session_on',record.session_on,'session_status',record.session_status,'segment_id',record.segment_id,'segment_position',record.segment_position,'segment_modality',record.segment_modality,'segment_title',record.segment_title,'segment_location',record.segment_location,'planned_duration_minutes',record.planned_duration_minutes,'planned_start_offset_minutes',record.planned_start_offset_minutes,'transition_duration_minutes',record.transition_duration_minutes,'equipment_notes',record.equipment_notes,'block_id',record.block_id,'block_position',record.block_position,'block_purpose',record.block_purpose,'block_title',record.block_title,'block_instructions',record.block_instructions,'gym_structure',record.gym_structure,'gym_objective',record.gym_objective,'gym_rounds',record.gym_rounds,'round_recovery_seconds',record.round_recovery_seconds) OPERATOR(pg_catalog.||) pg_catalog.jsonb_build_object('water_method',record.water_method,'water_intensity_profile_id',record.water_intensity_profile_id,'water_profile_name',record.water_profile_name,'water_profile_revision',record.water_profile_revision,'water_profile_craft',record.water_profile_craft,'water_target_distance_metres',record.water_target_distance_metres,'water_target_distance_certainty',record.water_target_distance_certainty,'water_step_id',record.water_step_id,'water_parent_step_id',record.water_parent_step_id,'water_step_position',record.water_step_position,'water_step_kind',record.water_step_kind,'water_step_name',record.water_step_name,'water_step_repeats',record.water_step_repeats,'water_step_duration_seconds',record.water_step_duration_seconds,'water_step_duration_certainty',record.water_step_duration_certainty,'water_step_distance_metres',record.water_step_distance_metres,'water_step_distance_certainty',record.water_step_distance_certainty,'water_step_recovery_seconds',record.water_step_recovery_seconds,'water_step_intensity_code',record.water_step_intensity_code,'water_step_cadence_spm',record.water_step_cadence_spm,'water_zone_label',record.water_zone_label,'water_zone_cadence_min',record.water_zone_cadence_min,'water_zone_cadence_max',record.water_zone_cadence_max,'water_zone_meaning',record.water_zone_meaning,'water_step_drill_focus',record.water_step_drill_focus,'water_step_drill_format',record.water_step_drill_format,'water_step_role_notes',record.water_step_role_notes,'water_step_instructions',record.water_step_instructions,'exercise_id',record.exercise_id,'exercise_position',record.exercise_position,'exercise_name',record.exercise_name,'exercise_sets',record.exercise_sets,'exercise_repetitions',record.exercise_repetitions,'exercise_duration_seconds',record.exercise_duration_seconds,'exercise_distance_metres',record.exercise_distance_metres,'exercise_recovery_seconds',record.exercise_recovery_seconds,'resistance_kind',record.resistance_kind,'resistance_value',record.resistance_value,'resistance_text',record.resistance_text,'execution_intent',record.execution_intent) OPERATOR(pg_catalog.||) pg_catalog.jsonb_build_object('tempo',record.tempo,'exercise_notes',record.exercise_notes))::text AS encoded FROM overview record JOIN input_gate ON true
UNION ALL
SELECT 1::integer AS category,row_number() OVER(ORDER BY session_id,placement_id,membership_id) AS ordinal,(pg_catalog.jsonb_build_object('session_id',record.session_id,'placement_id',record.placement_id,'membership_id',record.membership_id,'subject_id',record.subject_id,'starts_on',record.starts_on,'ends_on',record.ends_on,'session_on',record.session_on))::text AS encoded FROM candidates record JOIN input_gate ON true
UNION ALL
SELECT 2::integer AS category,row_number() OVER(ORDER BY session_id,membership_id) AS ordinal,(pg_catalog.jsonb_build_object('placement_id',record.placement_id,'membership_id',record.membership_id,'athlete_user_id',record.athlete_user_id,'athlete_name',record.athlete_name,'starts_on',record.starts_on,'ends_on',record.ends_on,'session_id',record.session_id))::text AS encoded FROM eligible_recipients record JOIN input_gate ON true
UNION ALL
SELECT 3::integer AS category,row_number() OVER(ORDER BY created_at,variation_id) AS ordinal,(pg_catalog.jsonb_build_object('variation_id',record.variation_id,'plan_id',record.plan_id,'subject_kind',record.subject_kind,'subject_id',record.subject_id,'target_membership_id',record.target_membership_id,'target_group_id',record.target_group_id,'operation',record.operation,'patch',record.patch,'change_summary',record.change_summary,'version',record.version,'created_at',record.created_at))::text AS encoded FROM variation_definitions record JOIN input_gate ON true
UNION ALL
SELECT 4::integer AS category,row_number() OVER(ORDER BY variation_id,membership_id) AS ordinal,(pg_catalog.jsonb_build_object('variation_id',record.variation_id,'membership_id',record.membership_id,'priority',record.priority,'target_group_id',record.target_group_id,'target_kind',record.target_kind,'target_group_name',record.target_group_name,'effective_from',record.effective_from,'effective_until',record.effective_until,'competition_event_id',record.competition_event_id))::text AS encoded FROM variation_targets record JOIN input_gate ON true
UNION ALL
SELECT 5::integer AS category,row_number() OVER(ORDER BY _ord) AS ordinal,(pg_catalog.jsonb_build_object('variation_id',record.variation_id,'plan_id',record.plan_id,'plan_title',record.plan_title,'session_id',record.session_id,'session_title',record.session_title,'starts_at',record.starts_at,'session_on',record.session_on,'ends_at',record.ends_at,'schedule_precision',record.schedule_precision,'scheduled_on',record.scheduled_on,'day_band',record.day_band,'subject_kind',record.subject_kind,'subject_id',record.subject_id,'subject_label',record.subject_label,'operation',record.operation,'change_summary',record.change_summary,'patch',record.patch,'version',record.version,'membership_id',record.membership_id,'athlete_name',record.athlete_name,'priority',record.priority,'target_kind',record.target_kind,'target_name',record.target_name,'effective_from',record.effective_from,'effective_until',record.effective_until))::text AS encoded FROM variation_matches record JOIN input_gate ON true
UNION ALL
SELECT 6::integer AS category,row_number() OVER(ORDER BY event_id) AS ordinal,(pg_catalog.jsonb_build_object('event_id',record.event_id,'starts_at',record.starts_at))::text AS encoded FROM referenced_events record JOIN input_gate ON true
UNION ALL
SELECT 7::integer AS category,row_number() OVER(ORDER BY session_id,membership_id) AS ordinal,(pg_catalog.jsonb_build_object('session_id',record.session_id,'membership_id',record.membership_id,'athlete_user_id',record.athlete_user_id,'placement_id',record.placement_id,'snapshot_sha256',record.snapshot_sha256))::text AS encoded FROM latest_hashes record JOIN input_gate ON true
),
record_totals AS (SELECT category,count(*) AS records,COALESCE(sum(octet_length(convert_to(encoded,'UTF8'))::numeric),0) AS bytes FROM records GROUP BY category), budget AS MATERIALIZED (
 SELECT LEAST(33554433::numeric,238::numeric+octet_length(convert_to(header_record.encoded,'UTF8'))+COALESCE((SELECT sum(bytes+GREATEST(records-1,0)) FROM record_totals),0))::bigint AS source_bytes,
 candidate_stats.raw_tuple_count,latest_stats.latest_tuple_count,
 COALESCE((candidate_stats.raw_tuple_count>4096 OR latest_stats.latest_tuple_count>4096 OR 238::numeric+octet_length(convert_to(header_record.encoded,'UTF8'))+COALESCE((SELECT sum(bytes+GREATEST(records-1,0)) FROM record_totals),0)>33554432),false)::boolean AS overflow
 FROM header_record CROSS JOIN candidate_stats CROSS JOIN latest_stats
), admitted AS MATERIALIZED(SELECT * FROM budget WHERE NOT overflow)

SELECT source_bytes,raw_tuple_count,latest_tuple_count,overflow FROM budget;

-- name: PublicationRawSource :one
WITH RECURSIVE actor AS (
 SELECT actor_user.id,EXISTS(SELECT 1 FROM public.user_platform_roles assignment JOIN public.platform_roles role ON role.id=assignment.role_id WHERE assignment.user_id=actor_user.id AND role.code='ADMIN') AS admin
 FROM public.users actor_user WHERE actor_user.id=sqlc.arg(actor_id)::uuid AND actor_user.is_active AND actor_user.erased_at IS NULL AND NOT actor_user.is_dependent
 AND actor_user.date_of_birth<=((now() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date
), scoped AS MATERIALIZED (
 SELECT plan.id,plan.title,plan.description,plan.programme_id,plan.team_id,plan.training_group_id,plan.season_id,plan.week_start,plan.planned_load_percentage,plan.updated_at,
 group_row.id AS group_id,group_row.name AS group_name,group_row.programme_id AS group_programme_id,group_row.team_id AS group_team_id
 FROM public.training_plans plan JOIN public.training_groups group_row ON group_row.id=plan.training_group_id JOIN public.seasons season ON season.id=plan.season_id CROSS JOIN actor
 WHERE plan.id=sqlc.arg(plan_id)::uuid AND (actor.admin OR EXISTS(SELECT 1 FROM public.staff_grants grant_row WHERE grant_row.user_id=actor.id AND grant_row.capability='COACH' AND grant_row.revoked_at IS NULL
 AND ((group_row.programme_id IS NOT NULL AND grant_row.programme_id=group_row.programme_id) OR (group_row.team_id IS NOT NULL AND grant_row.team_id=group_row.team_id))))
), header AS (
 SELECT plan.id AS plan_id,plan.group_id,plan.season_id,plan.week_start,plan.updated_at AS source_updated_at,
 latest.id AS latest_publication_id,COALESCE(latest.revision,0)::integer AS latest_revision,latest.supersedes_id AS latest_supersedes_id,latest.source_updated_at AS latest_source_updated_at
 FROM scoped plan LEFT JOIN public.training_plan_publications latest ON latest.id=(SELECT publication.id FROM public.training_plan_publications publication WHERE publication.plan_id=plan.id ORDER BY publication.revision DESC LIMIT 1)
),
candidates AS MATERIALIZED (
 SELECT DISTINCT session.id AS session_id,placement.id AS placement_id,membership.id AS membership_id,structural_subject.id AS subject_id,membership.starts_on,membership.ends_on,
 public.training_session_local_date(session.scheduled_on,session.starts_at) AS session_on
 FROM scoped plan JOIN public.training_group_placements placement ON placement.group_id=plan.group_id
 JOIN public.users structural_subject ON structural_subject.id=placement.subject_user_id
 JOIN public.training_sessions session ON session.plan_id=plan.id
 LEFT JOIN LATERAL public.training_placement_resolve(placement.subject_user_id,plan.group_id,plan.season_id,public.training_session_local_date(session.scheduled_on,session.starts_at)) resolved ON resolved.placement_id=placement.id
 LEFT JOIN public.user_memberships membership ON membership.id=resolved.membership_id
 AND(plan.programme_id IS NULL OR membership.programme_id=plan.programme_id) AND(plan.team_id IS NULL OR membership.team_id=plan.team_id)
 WHERE placement.subject_user_id IS NOT NULL AND placement.superseded_by_change_id IS NULL AND placement.cancelled_by_change_id IS NULL
 AND public.training_session_local_date(session.scheduled_on,session.starts_at) BETWEEN placement.starts_on AND COALESCE(placement.ends_on,'infinity'::date)
 ORDER BY session.id,placement.id,membership.id LIMIT 4097
), candidate_stats AS MATERIALIZED (SELECT count(*)::bigint AS raw_tuple_count FROM candidates), latest_hashes AS MATERIALIZED (
 SELECT prescription.session_id,prescription.placement_id,prescription.membership_id,historical_subject.id AS athlete_user_id,prescription.snapshot_sha256
 FROM header JOIN public.training_prescriptions prescription ON prescription.publication_id=header.latest_publication_id LEFT JOIN public.users historical_subject ON historical_subject.id=prescription.athlete_user_id ORDER BY prescription.session_id,prescription.membership_id LIMIT 4097
), latest_stats AS MATERIALIZED(SELECT count(*)::bigint AS latest_tuple_count FROM latest_hashes), input_gate AS MATERIALIZED (
 SELECT header.plan_id FROM header CROSS JOIN candidate_stats CROSS JOIN latest_stats WHERE candidate_stats.raw_tuple_count<=4096 AND latest_stats.latest_tuple_count<=4096
),
eligible_recipients AS (
 SELECT candidate.placement_id,candidate.membership_id,candidate.subject_id AS athlete_user_id,subject.name AS athlete_name,candidate.starts_on,candidate.ends_on,candidate.session_id
 FROM candidates candidate JOIN input_gate ON true JOIN public.users subject ON subject.id=candidate.subject_id
 WHERE candidate.membership_id IS NOT NULL AND subject.is_active AND subject.erased_at IS NULL AND (NOT subject.is_dependent OR EXISTS(SELECT 1 FROM public.guardian_authority_relationships relationship WHERE relationship.subject_user_id=subject.id AND public.guardian_authority_current(relationship.guardian_user_id,subject.id)))
),
unordered_overview AS (
SELECT 0::bigint AS _ord,group_row.id AS group_id, group_row.name AS group_name,
       programme.name_pt AS programme_name, team.name AS team_name,
       0::integer AS member_count,
       plan.id AS plan_id, plan.title AS plan_title, plan.description AS plan_description, season.name AS season_name, plan.week_start, plan.planned_load_percentage,
       session.id AS session_id, session.title AS session_title, session.description AS session_description,
       session.starts_at, session.ends_at, session.entry_kind,
       session.schedule_precision,session.scheduled_on,session.day_band,
       public.training_session_local_date(session.scheduled_on,session.starts_at) AS session_on, session.status AS session_status,
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
FROM input_gate JOIN scoped scoped_plan ON scoped_plan.id=input_gate.plan_id JOIN training_groups group_row ON group_row.id=scoped_plan.group_id
LEFT JOIN teams team ON team.id = group_row.team_id
JOIN programmes programme ON programme.id = COALESCE(group_row.programme_id, team.programme_id)
JOIN training_plans plan ON plan.id=scoped_plan.id
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

), water_nodes AS (
 SELECT DISTINCT water_step_id AS id,water_parent_step_id AS parent_id,water_step_position AS position FROM unordered_overview WHERE water_step_id IS NOT NULL
), water_tree(id,order_path,id_path) AS (
 SELECT root.id,ARRAY[root.position]::integer[],ARRAY[root.id]::uuid[] FROM water_nodes root WHERE root.parent_id IS NULL
 UNION ALL
 SELECT child.id,water_tree.order_path||child.position,water_tree.id_path||child.id FROM water_nodes child JOIN water_tree ON child.parent_id=water_tree.id WHERE NOT child.id=ANY(water_tree.id_path)
), overview AS (
 SELECT row_number() OVER(ORDER BY source.session_on NULLS LAST,source.starts_at NULLS LAST,source.session_id,source.segment_position,source.block_position,water_tree.order_path NULLS LAST,water_tree.id_path NULLS LAST,source.water_parent_step_id NULLS FIRST,source.water_step_position,source.water_step_id,source.exercise_position,source.exercise_id) AS _ord,source.group_id,source.group_name,source.programme_name,source.team_name,source.member_count,source.plan_id,source.plan_title,source.plan_description,source.season_name,source.week_start,source.planned_load_percentage,source.session_id,source.session_title,source.session_description,source.starts_at,source.ends_at,source.entry_kind,source.schedule_precision,source.scheduled_on,source.day_band,source.session_on,source.session_status,source.segment_id,source.segment_position,source.segment_modality,source.segment_title,source.segment_location,source.planned_duration_minutes,source.planned_start_offset_minutes,source.transition_duration_minutes,source.equipment_notes,source.block_id,source.block_position,source.block_purpose,source.block_title,source.block_instructions,source.gym_structure,source.gym_objective,source.gym_rounds,source.round_recovery_seconds,source.water_method,source.water_intensity_profile_id,source.water_profile_name,source.water_profile_revision,source.water_profile_craft,source.water_target_distance_metres,source.water_target_distance_certainty,source.water_step_id,source.water_parent_step_id,source.water_step_position,source.water_step_kind,source.water_step_name,source.water_step_repeats,source.water_step_duration_seconds,source.water_step_duration_certainty,source.water_step_distance_metres,source.water_step_distance_certainty,source.water_step_recovery_seconds,source.water_step_intensity_code,source.water_step_cadence_spm,source.water_zone_label,source.water_zone_cadence_min,source.water_zone_cadence_max,source.water_zone_meaning,source.water_step_drill_focus,source.water_step_drill_format,source.water_step_role_notes,source.water_step_instructions,source.exercise_id,source.exercise_position,source.exercise_name,source.exercise_sets,source.exercise_repetitions,source.exercise_duration_seconds,source.exercise_distance_metres,source.exercise_recovery_seconds,source.resistance_kind,source.resistance_value,source.resistance_text,source.execution_intent,source.tempo,source.exercise_notes
 FROM unordered_overview source LEFT JOIN water_tree ON water_tree.id=source.water_step_id
),
variation_definitions AS (
 SELECT variation.id AS variation_id,variation.plan_id,variation.subject_kind,variation.subject_id,variation.target_membership_id,variation.target_group_id,variation.operation,variation.patch,variation.change_summary,variation.version,variation.created_at
 FROM input_gate JOIN public.training_variations variation ON variation.plan_id=input_gate.plan_id WHERE variation.is_active
), variation_targets AS (
 SELECT variation.variation_id,variation.target_membership_id AS membership_id,2::integer AS priority,NULL::uuid AS target_group_id,'Atleta'::text AS target_kind,NULL::text AS target_group_name,NULL::date AS effective_from,NULL::date AS effective_until,NULL::uuid AS competition_event_id
 FROM variation_definitions variation WHERE variation.target_membership_id IS NOT NULL
 UNION ALL
 SELECT variation.variation_id,member.membership_id,1,variation_group.id,CASE variation_group.kind WHEN 'CREW' THEN 'Tripulação' ELSE 'Subgrupo' END,variation_group.name::text,variation_group.effective_from,COALESCE(variation_group.effective_until,(event_row.starts_at AT TIME ZONE 'Europe/Lisbon')::date),variation_group.competition_event_id
 FROM variation_definitions variation JOIN public.training_variation_groups variation_group ON variation_group.id=variation.target_group_id JOIN scoped plan ON plan.group_id=variation_group.training_group_id JOIN public.training_variation_group_members member ON member.variation_group_id=variation_group.id LEFT JOIN public.events event_row ON event_row.id=variation_group.competition_event_id
), referenced_events AS (
 SELECT DISTINCT event_row.id AS event_id,event_row.starts_at FROM variation_definitions variation JOIN public.training_variation_groups variation_group ON variation_group.id=variation.target_group_id JOIN scoped plan ON plan.group_id=variation_group.training_group_id JOIN public.events event_row ON event_row.id=variation_group.competition_event_id
),
subject_details AS (
    SELECT segment.id AS subject_id, 'SEGMENT'::training_variation_subject_kind AS subject_kind,
           session.plan_id, session.id AS session_id, session.title AS session_title, session.starts_at,
           public.training_session_local_date(session.scheduled_on,session.starts_at) AS session_on,
           session.ends_at,session.schedule_precision,session.scheduled_on,session.day_band,
           concat('Segmento · ', segment.position, ' · ', segment.modality::text,
                  CASE WHEN segment.title = '' THEN '' ELSE ' · ' || segment.title END)::text AS subject_label
    FROM input_gate JOIN training_sessions selected_session ON selected_session.plan_id=input_gate.plan_id JOIN training_session_segments segment ON segment.session_id=selected_session.id
    JOIN training_sessions session ON session.id = segment.session_id
    UNION ALL
    SELECT block.id, 'BLOCK'::training_variation_subject_kind, session.plan_id, session.id,
           session.title, session.starts_at,
           public.training_session_local_date(session.scheduled_on,session.starts_at),
           session.ends_at,session.schedule_precision,session.scheduled_on,session.day_band,
           concat('Bloco · ', block.position, ' · ', block.purpose::text,
                  CASE WHEN block.title = '' THEN '' ELSE ' · ' || block.title END)::text
    FROM input_gate JOIN training_sessions selected_session ON selected_session.plan_id=input_gate.plan_id JOIN training_session_segments selected_segment ON selected_segment.session_id=selected_session.id JOIN training_segment_blocks block ON block.segment_id=selected_segment.id
    JOIN training_session_segments segment ON segment.id = block.segment_id
    JOIN training_sessions session ON session.id = segment.session_id
    UNION ALL
    SELECT step.id, 'WATER_STEP'::training_variation_subject_kind, session.plan_id, session.id,
           session.title, session.starts_at,
           public.training_session_local_date(session.scheduled_on,session.starts_at),
           session.ends_at,session.schedule_precision,session.scheduled_on,session.day_band,
           concat('Passo de água · ', step.position, ' · ', step.name)::text
    FROM input_gate JOIN training_sessions selected_session ON selected_session.plan_id=input_gate.plan_id JOIN training_session_segments selected_segment ON selected_segment.session_id=selected_session.id JOIN training_segment_blocks selected_block ON selected_block.segment_id=selected_segment.id JOIN water_work_steps step ON step.block_id=selected_block.id
    JOIN training_segment_blocks block ON block.id = step.block_id
    JOIN training_session_segments segment ON segment.id = block.segment_id
    JOIN training_sessions session ON session.id = segment.session_id
    UNION ALL
    SELECT exercise.id, 'GYM_EXERCISE'::training_variation_subject_kind, session.plan_id, session.id,
           session.title, session.starts_at,
           public.training_session_local_date(session.scheduled_on,session.starts_at),
           session.ends_at,session.schedule_precision,session.scheduled_on,session.day_band,
           concat('Exercício · ', exercise.position, ' · ', exercise.name)::text
    FROM input_gate JOIN training_sessions selected_session ON selected_session.plan_id=input_gate.plan_id JOIN training_session_segments selected_segment ON selected_segment.session_id=selected_session.id JOIN training_segment_blocks selected_block ON selected_block.segment_id=selected_segment.id JOIN gym_exercises exercise ON exercise.block_id=selected_block.id
    JOIN training_segment_blocks block ON block.id = exercise.block_id
    JOIN training_session_segments segment ON segment.id = block.segment_id
    JOIN training_sessions session ON session.id = segment.session_id
),
variation_matches AS (
 SELECT variation.variation_id,variation.plan_id,plan.title AS plan_title,details.session_id,details.session_title,details.starts_at,details.session_on,details.ends_at,details.schedule_precision,details.scheduled_on,details.day_band,
 variation.subject_kind,variation.subject_id,details.subject_label,variation.operation,variation.change_summary,variation.patch,variation.version,targets.membership_id,recipient.athlete_name,targets.priority,targets.target_kind,
 COALESCE(CASE WHEN targets.priority=2 THEN recipient.athlete_name::text ELSE targets.target_group_name END,''::text)::text AS target_name,targets.effective_from,targets.effective_until,
 row_number() OVER(ORDER BY details.session_on,details.starts_at NULLS LAST,recipient.athlete_name,details.subject_label,targets.priority DESC,variation.created_at,variation.variation_id) AS _ord
 FROM variation_definitions variation JOIN scoped plan ON plan.id=variation.plan_id JOIN subject_details details ON details.plan_id=variation.plan_id AND details.subject_id=variation.subject_id AND details.subject_kind=variation.subject_kind
 JOIN variation_targets targets ON targets.variation_id=variation.variation_id JOIN eligible_recipients recipient ON recipient.membership_id=targets.membership_id AND recipient.session_id=details.session_id
 WHERE (targets.effective_from IS NULL OR details.session_on>=targets.effective_from) AND (targets.effective_until IS NULL OR details.session_on<=targets.effective_until)
),
header_record AS MATERIALIZED(SELECT (pg_catalog.jsonb_build_object('plan_id',header.plan_id,'group_id',header.group_id,'season_id',header.season_id,'week_start',header.week_start,'source_updated_at',header.source_updated_at,'latest_publication_id',header.latest_publication_id,'latest_revision',header.latest_revision,'latest_supersedes_id',header.latest_supersedes_id,'latest_source_updated_at',header.latest_source_updated_at))::text AS encoded FROM header),
records AS NOT MATERIALIZED (
SELECT 0::integer AS category,row_number() OVER(ORDER BY _ord) AS ordinal,(pg_catalog.jsonb_build_object('group_id',record.group_id,'group_name',record.group_name,'programme_name',record.programme_name,'team_name',record.team_name,'member_count',record.member_count,'plan_id',record.plan_id,'plan_title',record.plan_title,'plan_description',record.plan_description,'season_name',record.season_name,'week_start',record.week_start,'planned_load_percentage',record.planned_load_percentage,'session_id',record.session_id,'session_title',record.session_title,'session_description',record.session_description,'starts_at',record.starts_at,'ends_at',record.ends_at,'entry_kind',record.entry_kind,'schedule_precision',record.schedule_precision,'scheduled_on',record.scheduled_on,'day_band',record.day_band,'session_on',record.session_on,'session_status',record.session_status,'segment_id',record.segment_id,'segment_position',record.segment_position,'segment_modality',record.segment_modality,'segment_title',record.segment_title,'segment_location',record.segment_location,'planned_duration_minutes',record.planned_duration_minutes,'planned_start_offset_minutes',record.planned_start_offset_minutes,'transition_duration_minutes',record.transition_duration_minutes,'equipment_notes',record.equipment_notes,'block_id',record.block_id,'block_position',record.block_position,'block_purpose',record.block_purpose,'block_title',record.block_title,'block_instructions',record.block_instructions,'gym_structure',record.gym_structure,'gym_objective',record.gym_objective,'gym_rounds',record.gym_rounds,'round_recovery_seconds',record.round_recovery_seconds) OPERATOR(pg_catalog.||) pg_catalog.jsonb_build_object('water_method',record.water_method,'water_intensity_profile_id',record.water_intensity_profile_id,'water_profile_name',record.water_profile_name,'water_profile_revision',record.water_profile_revision,'water_profile_craft',record.water_profile_craft,'water_target_distance_metres',record.water_target_distance_metres,'water_target_distance_certainty',record.water_target_distance_certainty,'water_step_id',record.water_step_id,'water_parent_step_id',record.water_parent_step_id,'water_step_position',record.water_step_position,'water_step_kind',record.water_step_kind,'water_step_name',record.water_step_name,'water_step_repeats',record.water_step_repeats,'water_step_duration_seconds',record.water_step_duration_seconds,'water_step_duration_certainty',record.water_step_duration_certainty,'water_step_distance_metres',record.water_step_distance_metres,'water_step_distance_certainty',record.water_step_distance_certainty,'water_step_recovery_seconds',record.water_step_recovery_seconds,'water_step_intensity_code',record.water_step_intensity_code,'water_step_cadence_spm',record.water_step_cadence_spm,'water_zone_label',record.water_zone_label,'water_zone_cadence_min',record.water_zone_cadence_min,'water_zone_cadence_max',record.water_zone_cadence_max,'water_zone_meaning',record.water_zone_meaning,'water_step_drill_focus',record.water_step_drill_focus,'water_step_drill_format',record.water_step_drill_format,'water_step_role_notes',record.water_step_role_notes,'water_step_instructions',record.water_step_instructions,'exercise_id',record.exercise_id,'exercise_position',record.exercise_position,'exercise_name',record.exercise_name,'exercise_sets',record.exercise_sets,'exercise_repetitions',record.exercise_repetitions,'exercise_duration_seconds',record.exercise_duration_seconds,'exercise_distance_metres',record.exercise_distance_metres,'exercise_recovery_seconds',record.exercise_recovery_seconds,'resistance_kind',record.resistance_kind,'resistance_value',record.resistance_value,'resistance_text',record.resistance_text,'execution_intent',record.execution_intent) OPERATOR(pg_catalog.||) pg_catalog.jsonb_build_object('tempo',record.tempo,'exercise_notes',record.exercise_notes))::text AS encoded FROM overview record JOIN input_gate ON true
UNION ALL
SELECT 1::integer AS category,row_number() OVER(ORDER BY session_id,placement_id,membership_id) AS ordinal,(pg_catalog.jsonb_build_object('session_id',record.session_id,'placement_id',record.placement_id,'membership_id',record.membership_id,'subject_id',record.subject_id,'starts_on',record.starts_on,'ends_on',record.ends_on,'session_on',record.session_on))::text AS encoded FROM candidates record JOIN input_gate ON true
UNION ALL
SELECT 2::integer AS category,row_number() OVER(ORDER BY session_id,membership_id) AS ordinal,(pg_catalog.jsonb_build_object('placement_id',record.placement_id,'membership_id',record.membership_id,'athlete_user_id',record.athlete_user_id,'athlete_name',record.athlete_name,'starts_on',record.starts_on,'ends_on',record.ends_on,'session_id',record.session_id))::text AS encoded FROM eligible_recipients record JOIN input_gate ON true
UNION ALL
SELECT 3::integer AS category,row_number() OVER(ORDER BY created_at,variation_id) AS ordinal,(pg_catalog.jsonb_build_object('variation_id',record.variation_id,'plan_id',record.plan_id,'subject_kind',record.subject_kind,'subject_id',record.subject_id,'target_membership_id',record.target_membership_id,'target_group_id',record.target_group_id,'operation',record.operation,'patch',record.patch,'change_summary',record.change_summary,'version',record.version,'created_at',record.created_at))::text AS encoded FROM variation_definitions record JOIN input_gate ON true
UNION ALL
SELECT 4::integer AS category,row_number() OVER(ORDER BY variation_id,membership_id) AS ordinal,(pg_catalog.jsonb_build_object('variation_id',record.variation_id,'membership_id',record.membership_id,'priority',record.priority,'target_group_id',record.target_group_id,'target_kind',record.target_kind,'target_group_name',record.target_group_name,'effective_from',record.effective_from,'effective_until',record.effective_until,'competition_event_id',record.competition_event_id))::text AS encoded FROM variation_targets record JOIN input_gate ON true
UNION ALL
SELECT 5::integer AS category,row_number() OVER(ORDER BY _ord) AS ordinal,(pg_catalog.jsonb_build_object('variation_id',record.variation_id,'plan_id',record.plan_id,'plan_title',record.plan_title,'session_id',record.session_id,'session_title',record.session_title,'starts_at',record.starts_at,'session_on',record.session_on,'ends_at',record.ends_at,'schedule_precision',record.schedule_precision,'scheduled_on',record.scheduled_on,'day_band',record.day_band,'subject_kind',record.subject_kind,'subject_id',record.subject_id,'subject_label',record.subject_label,'operation',record.operation,'change_summary',record.change_summary,'patch',record.patch,'version',record.version,'membership_id',record.membership_id,'athlete_name',record.athlete_name,'priority',record.priority,'target_kind',record.target_kind,'target_name',record.target_name,'effective_from',record.effective_from,'effective_until',record.effective_until))::text AS encoded FROM variation_matches record JOIN input_gate ON true
UNION ALL
SELECT 6::integer AS category,row_number() OVER(ORDER BY event_id) AS ordinal,(pg_catalog.jsonb_build_object('event_id',record.event_id,'starts_at',record.starts_at))::text AS encoded FROM referenced_events record JOIN input_gate ON true
UNION ALL
SELECT 7::integer AS category,row_number() OVER(ORDER BY session_id,membership_id) AS ordinal,(pg_catalog.jsonb_build_object('session_id',record.session_id,'membership_id',record.membership_id,'athlete_user_id',record.athlete_user_id,'placement_id',record.placement_id,'snapshot_sha256',record.snapshot_sha256))::text AS encoded FROM latest_hashes record JOIN input_gate ON true
),
record_totals AS (SELECT category,count(*) AS records,COALESCE(sum(octet_length(convert_to(encoded,'UTF8'))::numeric),0) AS bytes FROM records GROUP BY category), budget AS MATERIALIZED (
 SELECT LEAST(33554433::numeric,238::numeric+octet_length(convert_to(header_record.encoded,'UTF8'))+COALESCE((SELECT sum(bytes+GREATEST(records-1,0)) FROM record_totals),0))::bigint AS source_bytes,
 candidate_stats.raw_tuple_count,latest_stats.latest_tuple_count,
 COALESCE((candidate_stats.raw_tuple_count>4096 OR latest_stats.latest_tuple_count>4096 OR 238::numeric+octet_length(convert_to(header_record.encoded,'UTF8'))+COALESCE((SELECT sum(bytes+GREATEST(records-1,0)) FROM record_totals),0)>33554432),false)::boolean AS overflow
 FROM header_record CROSS JOIN candidate_stats CROSS JOIN latest_stats
), admitted AS MATERIALIZED(SELECT * FROM budget WHERE NOT overflow)

SELECT budget.source_bytes,budget.raw_tuple_count,budget.latest_tuple_count,budget.overflow,materialized.envelope
 FROM budget LEFT JOIN LATERAL (
 SELECT convert_to('{"contract":"mycfc/training-publication-source/v1","header":'||header_record.encoded||',"overview":['||COALESCE((SELECT string_agg(encoded,',' ORDER BY ordinal) FROM records WHERE category=0 AND admitted.source_bytes>=0),'')||']'||',"candidates":['||COALESCE((SELECT string_agg(encoded,',' ORDER BY ordinal) FROM records WHERE category=1 AND admitted.source_bytes>=0),'')||']'||',"eligible_recipients":['||COALESCE((SELECT string_agg(encoded,',' ORDER BY ordinal) FROM records WHERE category=2 AND admitted.source_bytes>=0),'')||']'||',"variation_definitions":['||COALESCE((SELECT string_agg(encoded,',' ORDER BY ordinal) FROM records WHERE category=3 AND admitted.source_bytes>=0),'')||']'||',"variation_targets":['||COALESCE((SELECT string_agg(encoded,',' ORDER BY ordinal) FROM records WHERE category=4 AND admitted.source_bytes>=0),'')||']'||',"variation_matches":['||COALESCE((SELECT string_agg(encoded,',' ORDER BY ordinal) FROM records WHERE category=5 AND admitted.source_bytes>=0),'')||']'||',"referenced_events":['||COALESCE((SELECT string_agg(encoded,',' ORDER BY ordinal) FROM records WHERE category=6 AND admitted.source_bytes>=0),'')||']'||',"latest_prescriptions":['||COALESCE((SELECT string_agg(encoded,',' ORDER BY ordinal) FROM records WHERE category=7 AND admitted.source_bytes>=0),'')||']'||'}','UTF8') AS envelope FROM admitted CROSS JOIN header_record WHERE admitted.source_bytes=budget.source_bytes OFFSET 0
 ) materialized ON true;

-- name: PublicationStructuralCandidates :many
WITH RECURSIVE actor AS (
 SELECT actor_user.id,EXISTS(SELECT 1 FROM public.user_platform_roles assignment JOIN public.platform_roles role ON role.id=assignment.role_id WHERE assignment.user_id=actor_user.id AND role.code='ADMIN') AS admin
 FROM public.users actor_user WHERE actor_user.id=sqlc.arg(actor_id)::uuid AND actor_user.is_active AND actor_user.erased_at IS NULL AND NOT actor_user.is_dependent
 AND actor_user.date_of_birth<=((now() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date
), scoped AS MATERIALIZED (
 SELECT plan.id,plan.title,plan.description,plan.programme_id,plan.team_id,plan.training_group_id,plan.season_id,plan.week_start,plan.planned_load_percentage,plan.updated_at,
 group_row.id AS group_id,group_row.name AS group_name,group_row.programme_id AS group_programme_id,group_row.team_id AS group_team_id
 FROM public.training_plans plan JOIN public.training_groups group_row ON group_row.id=plan.training_group_id JOIN public.seasons season ON season.id=plan.season_id CROSS JOIN actor
 WHERE plan.id=sqlc.arg(plan_id)::uuid AND (actor.admin OR EXISTS(SELECT 1 FROM public.staff_grants grant_row WHERE grant_row.user_id=actor.id AND grant_row.capability='COACH' AND grant_row.revoked_at IS NULL
 AND ((group_row.programme_id IS NOT NULL AND grant_row.programme_id=group_row.programme_id) OR (group_row.team_id IS NOT NULL AND grant_row.team_id=group_row.team_id))))
), header AS (
 SELECT plan.id AS plan_id,plan.group_id,plan.season_id,plan.week_start,plan.updated_at AS source_updated_at,
 latest.id AS latest_publication_id,COALESCE(latest.revision,0)::integer AS latest_revision,latest.supersedes_id AS latest_supersedes_id,latest.source_updated_at AS latest_source_updated_at
 FROM scoped plan LEFT JOIN public.training_plan_publications latest ON latest.id=(SELECT publication.id FROM public.training_plan_publications publication WHERE publication.plan_id=plan.id ORDER BY publication.revision DESC LIMIT 1)
),
candidates AS MATERIALIZED (
 SELECT DISTINCT session.id AS session_id,placement.id AS placement_id,membership.id AS membership_id,structural_subject.id AS subject_id,membership.starts_on,membership.ends_on,
 public.training_session_local_date(session.scheduled_on,session.starts_at) AS session_on
 FROM scoped plan JOIN public.training_group_placements placement ON placement.group_id=plan.group_id
 JOIN public.users structural_subject ON structural_subject.id=placement.subject_user_id
 JOIN public.training_sessions session ON session.plan_id=plan.id
 LEFT JOIN LATERAL public.training_placement_resolve(placement.subject_user_id,plan.group_id,plan.season_id,public.training_session_local_date(session.scheduled_on,session.starts_at)) resolved ON resolved.placement_id=placement.id
 LEFT JOIN public.user_memberships membership ON membership.id=resolved.membership_id
 AND(plan.programme_id IS NULL OR membership.programme_id=plan.programme_id) AND(plan.team_id IS NULL OR membership.team_id=plan.team_id)
 WHERE placement.subject_user_id IS NOT NULL AND placement.superseded_by_change_id IS NULL AND placement.cancelled_by_change_id IS NULL
 AND public.training_session_local_date(session.scheduled_on,session.starts_at) BETWEEN placement.starts_on AND COALESCE(placement.ends_on,'infinity'::date)
 ORDER BY session.id,placement.id,membership.id LIMIT 4097
), candidate_stats AS MATERIALIZED (SELECT count(*)::bigint AS raw_tuple_count FROM candidates), latest_hashes AS MATERIALIZED (
 SELECT prescription.session_id,prescription.placement_id,prescription.membership_id,historical_subject.id AS athlete_user_id,prescription.snapshot_sha256
 FROM header JOIN public.training_prescriptions prescription ON prescription.publication_id=header.latest_publication_id LEFT JOIN public.users historical_subject ON historical_subject.id=prescription.athlete_user_id ORDER BY prescription.session_id,prescription.membership_id LIMIT 4097
), latest_stats AS MATERIALIZED(SELECT count(*)::bigint AS latest_tuple_count FROM latest_hashes), input_gate AS MATERIALIZED (
 SELECT header.plan_id FROM header CROSS JOIN candidate_stats CROSS JOIN latest_stats WHERE candidate_stats.raw_tuple_count<=4096 AND latest_stats.latest_tuple_count<=4096
)
SELECT session_id,placement_id,membership_id,subject_id,starts_on,ends_on,session_on FROM candidates ORDER BY session_id,membership_id;

-- name: LockPublicationCandidateMemberships :many
WITH RECURSIVE actor AS (
 SELECT actor_user.id,EXISTS(SELECT 1 FROM public.user_platform_roles assignment JOIN public.platform_roles role ON role.id=assignment.role_id WHERE assignment.user_id=actor_user.id AND role.code='ADMIN') AS admin
 FROM public.users actor_user WHERE actor_user.id=sqlc.arg(actor_id)::uuid AND actor_user.is_active AND actor_user.erased_at IS NULL AND NOT actor_user.is_dependent
 AND actor_user.date_of_birth<=((now() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date
), scoped AS MATERIALIZED (
 SELECT plan.id,plan.title,plan.description,plan.programme_id,plan.team_id,plan.training_group_id,plan.season_id,plan.week_start,plan.planned_load_percentage,plan.updated_at,
 group_row.id AS group_id,group_row.name AS group_name,group_row.programme_id AS group_programme_id,group_row.team_id AS group_team_id
 FROM public.training_plans plan JOIN public.training_groups group_row ON group_row.id=plan.training_group_id JOIN public.seasons season ON season.id=plan.season_id CROSS JOIN actor
 WHERE plan.id=sqlc.arg(plan_id)::uuid AND (actor.admin OR EXISTS(SELECT 1 FROM public.staff_grants grant_row WHERE grant_row.user_id=actor.id AND grant_row.capability='COACH' AND grant_row.revoked_at IS NULL
 AND ((group_row.programme_id IS NOT NULL AND grant_row.programme_id=group_row.programme_id) OR (group_row.team_id IS NOT NULL AND grant_row.team_id=group_row.team_id))))
), header AS (
 SELECT plan.id AS plan_id,plan.group_id,plan.season_id,plan.week_start,plan.updated_at AS source_updated_at,
 latest.id AS latest_publication_id,COALESCE(latest.revision,0)::integer AS latest_revision,latest.supersedes_id AS latest_supersedes_id,latest.source_updated_at AS latest_source_updated_at
 FROM scoped plan LEFT JOIN public.training_plan_publications latest ON latest.id=(SELECT publication.id FROM public.training_plan_publications publication WHERE publication.plan_id=plan.id ORDER BY publication.revision DESC LIMIT 1)
),
candidates AS MATERIALIZED (
 SELECT DISTINCT session.id AS session_id,placement.id AS placement_id,membership.id AS membership_id,structural_subject.id AS subject_id,membership.starts_on,membership.ends_on,
 public.training_session_local_date(session.scheduled_on,session.starts_at) AS session_on
 FROM scoped plan JOIN public.training_group_placements placement ON placement.group_id=plan.group_id
 JOIN public.users structural_subject ON structural_subject.id=placement.subject_user_id
 JOIN public.training_sessions session ON session.plan_id=plan.id
 LEFT JOIN LATERAL public.training_placement_resolve(placement.subject_user_id,plan.group_id,plan.season_id,public.training_session_local_date(session.scheduled_on,session.starts_at)) resolved ON resolved.placement_id=placement.id
 LEFT JOIN public.user_memberships membership ON membership.id=resolved.membership_id
 AND(plan.programme_id IS NULL OR membership.programme_id=plan.programme_id) AND(plan.team_id IS NULL OR membership.team_id=plan.team_id)
 WHERE placement.subject_user_id IS NOT NULL AND placement.superseded_by_change_id IS NULL AND placement.cancelled_by_change_id IS NULL
 AND public.training_session_local_date(session.scheduled_on,session.starts_at) BETWEEN placement.starts_on AND COALESCE(placement.ends_on,'infinity'::date)
 ORDER BY session.id,placement.id,membership.id LIMIT 4097
), candidate_stats AS MATERIALIZED (SELECT count(*)::bigint AS raw_tuple_count FROM candidates), latest_hashes AS MATERIALIZED (
 SELECT prescription.session_id,prescription.placement_id,prescription.membership_id,historical_subject.id AS athlete_user_id,prescription.snapshot_sha256
 FROM header JOIN public.training_prescriptions prescription ON prescription.publication_id=header.latest_publication_id LEFT JOIN public.users historical_subject ON historical_subject.id=prescription.athlete_user_id ORDER BY prescription.session_id,prescription.membership_id LIMIT 4097
), latest_stats AS MATERIALIZED(SELECT count(*)::bigint AS latest_tuple_count FROM latest_hashes), input_gate AS MATERIALIZED (
 SELECT header.plan_id FROM header CROSS JOIN candidate_stats CROSS JOIN latest_stats WHERE candidate_stats.raw_tuple_count<=4096 AND latest_stats.latest_tuple_count<=4096
)
SELECT membership.id AS membership_id FROM public.user_memberships membership JOIN (SELECT DISTINCT membership_id FROM candidates) needed ON needed.membership_id=membership.id JOIN input_gate ON true ORDER BY membership.id FOR SHARE OF membership;

-- name: LockPublicationCandidateUsers :many
WITH RECURSIVE actor AS (
 SELECT actor_user.id,EXISTS(SELECT 1 FROM public.user_platform_roles assignment JOIN public.platform_roles role ON role.id=assignment.role_id WHERE assignment.user_id=actor_user.id AND role.code='ADMIN') AS admin
 FROM public.users actor_user WHERE actor_user.id=sqlc.arg(actor_id)::uuid AND actor_user.is_active AND actor_user.erased_at IS NULL AND NOT actor_user.is_dependent
 AND actor_user.date_of_birth<=((now() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date
), scoped AS MATERIALIZED (
 SELECT plan.id,plan.title,plan.description,plan.programme_id,plan.team_id,plan.training_group_id,plan.season_id,plan.week_start,plan.planned_load_percentage,plan.updated_at,
 group_row.id AS group_id,group_row.name AS group_name,group_row.programme_id AS group_programme_id,group_row.team_id AS group_team_id
 FROM public.training_plans plan JOIN public.training_groups group_row ON group_row.id=plan.training_group_id JOIN public.seasons season ON season.id=plan.season_id CROSS JOIN actor
 WHERE plan.id=sqlc.arg(plan_id)::uuid AND (actor.admin OR EXISTS(SELECT 1 FROM public.staff_grants grant_row WHERE grant_row.user_id=actor.id AND grant_row.capability='COACH' AND grant_row.revoked_at IS NULL
 AND ((group_row.programme_id IS NOT NULL AND grant_row.programme_id=group_row.programme_id) OR (group_row.team_id IS NOT NULL AND grant_row.team_id=group_row.team_id))))
), header AS (
 SELECT plan.id AS plan_id,plan.group_id,plan.season_id,plan.week_start,plan.updated_at AS source_updated_at,
 latest.id AS latest_publication_id,COALESCE(latest.revision,0)::integer AS latest_revision,latest.supersedes_id AS latest_supersedes_id,latest.source_updated_at AS latest_source_updated_at
 FROM scoped plan LEFT JOIN public.training_plan_publications latest ON latest.id=(SELECT publication.id FROM public.training_plan_publications publication WHERE publication.plan_id=plan.id ORDER BY publication.revision DESC LIMIT 1)
),
candidates AS MATERIALIZED (
 SELECT DISTINCT session.id AS session_id,placement.id AS placement_id,membership.id AS membership_id,structural_subject.id AS subject_id,membership.starts_on,membership.ends_on,
 public.training_session_local_date(session.scheduled_on,session.starts_at) AS session_on
 FROM scoped plan JOIN public.training_group_placements placement ON placement.group_id=plan.group_id
 JOIN public.users structural_subject ON structural_subject.id=placement.subject_user_id
 JOIN public.training_sessions session ON session.plan_id=plan.id
 LEFT JOIN LATERAL public.training_placement_resolve(placement.subject_user_id,plan.group_id,plan.season_id,public.training_session_local_date(session.scheduled_on,session.starts_at)) resolved ON resolved.placement_id=placement.id
 LEFT JOIN public.user_memberships membership ON membership.id=resolved.membership_id
 AND(plan.programme_id IS NULL OR membership.programme_id=plan.programme_id) AND(plan.team_id IS NULL OR membership.team_id=plan.team_id)
 WHERE placement.subject_user_id IS NOT NULL AND placement.superseded_by_change_id IS NULL AND placement.cancelled_by_change_id IS NULL
 AND public.training_session_local_date(session.scheduled_on,session.starts_at) BETWEEN placement.starts_on AND COALESCE(placement.ends_on,'infinity'::date)
 ORDER BY session.id,placement.id,membership.id LIMIT 4097
), candidate_stats AS MATERIALIZED (SELECT count(*)::bigint AS raw_tuple_count FROM candidates), latest_hashes AS MATERIALIZED (
 SELECT prescription.session_id,prescription.placement_id,prescription.membership_id,historical_subject.id AS athlete_user_id,prescription.snapshot_sha256
 FROM header JOIN public.training_prescriptions prescription ON prescription.publication_id=header.latest_publication_id LEFT JOIN public.users historical_subject ON historical_subject.id=prescription.athlete_user_id ORDER BY prescription.session_id,prescription.membership_id LIMIT 4097
), latest_stats AS MATERIALIZED(SELECT count(*)::bigint AS latest_tuple_count FROM latest_hashes), input_gate AS MATERIALIZED (
 SELECT header.plan_id FROM header CROSS JOIN candidate_stats CROSS JOIN latest_stats WHERE candidate_stats.raw_tuple_count<=4096 AND latest_stats.latest_tuple_count<=4096
)
SELECT subject.id AS user_id FROM public.users subject JOIN (
 SELECT subject_id AS user_id FROM candidates WHERE subject_id IS NOT NULL UNION SELECT relationship.guardian_user_id FROM public.guardian_authority_relationships relationship JOIN (SELECT DISTINCT subject_id FROM candidates) needed ON needed.subject_id=relationship.subject_user_id
 ) needed ON needed.user_id=subject.id JOIN input_gate ON true ORDER BY subject.id FOR SHARE OF subject NOWAIT;

-- name: LockPublicationReferencedEvents :many
WITH RECURSIVE actor AS (
 SELECT actor_user.id,EXISTS(SELECT 1 FROM public.user_platform_roles assignment JOIN public.platform_roles role ON role.id=assignment.role_id WHERE assignment.user_id=actor_user.id AND role.code='ADMIN') AS admin
 FROM public.users actor_user WHERE actor_user.id=sqlc.arg(actor_id)::uuid AND actor_user.is_active AND actor_user.erased_at IS NULL AND NOT actor_user.is_dependent
 AND actor_user.date_of_birth<=((now() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date
), scoped AS MATERIALIZED (
 SELECT plan.id,plan.title,plan.description,plan.programme_id,plan.team_id,plan.training_group_id,plan.season_id,plan.week_start,plan.planned_load_percentage,plan.updated_at,
 group_row.id AS group_id,group_row.name AS group_name,group_row.programme_id AS group_programme_id,group_row.team_id AS group_team_id
 FROM public.training_plans plan JOIN public.training_groups group_row ON group_row.id=plan.training_group_id JOIN public.seasons season ON season.id=plan.season_id CROSS JOIN actor
 WHERE plan.id=sqlc.arg(plan_id)::uuid AND (actor.admin OR EXISTS(SELECT 1 FROM public.staff_grants grant_row WHERE grant_row.user_id=actor.id AND grant_row.capability='COACH' AND grant_row.revoked_at IS NULL
 AND ((group_row.programme_id IS NOT NULL AND grant_row.programme_id=group_row.programme_id) OR (group_row.team_id IS NOT NULL AND grant_row.team_id=group_row.team_id))))
), header AS (
 SELECT plan.id AS plan_id,plan.group_id,plan.season_id,plan.week_start,plan.updated_at AS source_updated_at,
 latest.id AS latest_publication_id,COALESCE(latest.revision,0)::integer AS latest_revision,latest.supersedes_id AS latest_supersedes_id,latest.source_updated_at AS latest_source_updated_at
 FROM scoped plan LEFT JOIN public.training_plan_publications latest ON latest.id=(SELECT publication.id FROM public.training_plan_publications publication WHERE publication.plan_id=plan.id ORDER BY publication.revision DESC LIMIT 1)
),
candidates AS MATERIALIZED (
 SELECT DISTINCT session.id AS session_id,placement.id AS placement_id,membership.id AS membership_id,structural_subject.id AS subject_id,membership.starts_on,membership.ends_on,
 public.training_session_local_date(session.scheduled_on,session.starts_at) AS session_on
 FROM scoped plan JOIN public.training_group_placements placement ON placement.group_id=plan.group_id
 JOIN public.users structural_subject ON structural_subject.id=placement.subject_user_id
 JOIN public.training_sessions session ON session.plan_id=plan.id
 LEFT JOIN LATERAL public.training_placement_resolve(placement.subject_user_id,plan.group_id,plan.season_id,public.training_session_local_date(session.scheduled_on,session.starts_at)) resolved ON resolved.placement_id=placement.id
 LEFT JOIN public.user_memberships membership ON membership.id=resolved.membership_id
 AND(plan.programme_id IS NULL OR membership.programme_id=plan.programme_id) AND(plan.team_id IS NULL OR membership.team_id=plan.team_id)
 WHERE placement.subject_user_id IS NOT NULL AND placement.superseded_by_change_id IS NULL AND placement.cancelled_by_change_id IS NULL
 AND public.training_session_local_date(session.scheduled_on,session.starts_at) BETWEEN placement.starts_on AND COALESCE(placement.ends_on,'infinity'::date)
 ORDER BY session.id,placement.id,membership.id LIMIT 4097
), candidate_stats AS MATERIALIZED (SELECT count(*)::bigint AS raw_tuple_count FROM candidates), latest_hashes AS MATERIALIZED (
 SELECT prescription.session_id,prescription.placement_id,prescription.membership_id,historical_subject.id AS athlete_user_id,prescription.snapshot_sha256
 FROM header JOIN public.training_prescriptions prescription ON prescription.publication_id=header.latest_publication_id LEFT JOIN public.users historical_subject ON historical_subject.id=prescription.athlete_user_id ORDER BY prescription.session_id,prescription.membership_id LIMIT 4097
), latest_stats AS MATERIALIZED(SELECT count(*)::bigint AS latest_tuple_count FROM latest_hashes), input_gate AS MATERIALIZED (
 SELECT header.plan_id FROM header CROSS JOIN candidate_stats CROSS JOIN latest_stats WHERE candidate_stats.raw_tuple_count<=4096 AND latest_stats.latest_tuple_count<=4096
),
variation_definitions AS (
 SELECT variation.id AS variation_id,variation.plan_id,variation.subject_kind,variation.subject_id,variation.target_membership_id,variation.target_group_id,variation.operation,variation.patch,variation.change_summary,variation.version,variation.created_at
 FROM input_gate JOIN public.training_variations variation ON variation.plan_id=input_gate.plan_id WHERE variation.is_active
), variation_targets AS (
 SELECT variation.variation_id,variation.target_membership_id AS membership_id,2::integer AS priority,NULL::uuid AS target_group_id,'Atleta'::text AS target_kind,NULL::text AS target_group_name,NULL::date AS effective_from,NULL::date AS effective_until,NULL::uuid AS competition_event_id
 FROM variation_definitions variation WHERE variation.target_membership_id IS NOT NULL
 UNION ALL
 SELECT variation.variation_id,member.membership_id,1,variation_group.id,CASE variation_group.kind WHEN 'CREW' THEN 'Tripulação' ELSE 'Subgrupo' END,variation_group.name::text,variation_group.effective_from,COALESCE(variation_group.effective_until,(event_row.starts_at AT TIME ZONE 'Europe/Lisbon')::date),variation_group.competition_event_id
 FROM variation_definitions variation JOIN public.training_variation_groups variation_group ON variation_group.id=variation.target_group_id JOIN scoped plan ON plan.group_id=variation_group.training_group_id JOIN public.training_variation_group_members member ON member.variation_group_id=variation_group.id LEFT JOIN public.events event_row ON event_row.id=variation_group.competition_event_id
), referenced_events AS (
 SELECT DISTINCT event_row.id AS event_id,event_row.starts_at FROM variation_definitions variation JOIN public.training_variation_groups variation_group ON variation_group.id=variation.target_group_id JOIN scoped plan ON plan.group_id=variation_group.training_group_id JOIN public.events event_row ON event_row.id=variation_group.competition_event_id
)
SELECT event_row.id AS event_id,event_row.starts_at FROM public.events event_row JOIN referenced_events needed ON needed.event_id=event_row.id ORDER BY event_row.id FOR SHARE OF event_row NOWAIT;

-- name: PublicationEligibleRecipients :many
WITH RECURSIVE actor AS (
 SELECT actor_user.id,EXISTS(SELECT 1 FROM public.user_platform_roles assignment JOIN public.platform_roles role ON role.id=assignment.role_id WHERE assignment.user_id=actor_user.id AND role.code='ADMIN') AS admin
 FROM public.users actor_user WHERE actor_user.id=sqlc.arg(actor_id)::uuid AND actor_user.is_active AND actor_user.erased_at IS NULL AND NOT actor_user.is_dependent
 AND actor_user.date_of_birth<=((now() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date
), scoped AS MATERIALIZED (
 SELECT plan.id,plan.title,plan.description,plan.programme_id,plan.team_id,plan.training_group_id,plan.season_id,plan.week_start,plan.planned_load_percentage,plan.updated_at,
 group_row.id AS group_id,group_row.name AS group_name,group_row.programme_id AS group_programme_id,group_row.team_id AS group_team_id
 FROM public.training_plans plan JOIN public.training_groups group_row ON group_row.id=plan.training_group_id JOIN public.seasons season ON season.id=plan.season_id CROSS JOIN actor
 WHERE plan.id=sqlc.arg(plan_id)::uuid AND (actor.admin OR EXISTS(SELECT 1 FROM public.staff_grants grant_row WHERE grant_row.user_id=actor.id AND grant_row.capability='COACH' AND grant_row.revoked_at IS NULL
 AND ((group_row.programme_id IS NOT NULL AND grant_row.programme_id=group_row.programme_id) OR (group_row.team_id IS NOT NULL AND grant_row.team_id=group_row.team_id))))
), header AS (
 SELECT plan.id AS plan_id,plan.group_id,plan.season_id,plan.week_start,plan.updated_at AS source_updated_at,
 latest.id AS latest_publication_id,COALESCE(latest.revision,0)::integer AS latest_revision,latest.supersedes_id AS latest_supersedes_id,latest.source_updated_at AS latest_source_updated_at
 FROM scoped plan LEFT JOIN public.training_plan_publications latest ON latest.id=(SELECT publication.id FROM public.training_plan_publications publication WHERE publication.plan_id=plan.id ORDER BY publication.revision DESC LIMIT 1)
),
candidates AS MATERIALIZED (
 SELECT DISTINCT session.id AS session_id,placement.id AS placement_id,membership.id AS membership_id,structural_subject.id AS subject_id,membership.starts_on,membership.ends_on,
 public.training_session_local_date(session.scheduled_on,session.starts_at) AS session_on
 FROM scoped plan JOIN public.training_group_placements placement ON placement.group_id=plan.group_id
 JOIN public.users structural_subject ON structural_subject.id=placement.subject_user_id
 JOIN public.training_sessions session ON session.plan_id=plan.id
 LEFT JOIN LATERAL public.training_placement_resolve(placement.subject_user_id,plan.group_id,plan.season_id,public.training_session_local_date(session.scheduled_on,session.starts_at)) resolved ON resolved.placement_id=placement.id
 LEFT JOIN public.user_memberships membership ON membership.id=resolved.membership_id
 AND(plan.programme_id IS NULL OR membership.programme_id=plan.programme_id) AND(plan.team_id IS NULL OR membership.team_id=plan.team_id)
 WHERE placement.subject_user_id IS NOT NULL AND placement.superseded_by_change_id IS NULL AND placement.cancelled_by_change_id IS NULL
 AND public.training_session_local_date(session.scheduled_on,session.starts_at) BETWEEN placement.starts_on AND COALESCE(placement.ends_on,'infinity'::date)
 ORDER BY session.id,placement.id,membership.id LIMIT 4097
), candidate_stats AS MATERIALIZED (SELECT count(*)::bigint AS raw_tuple_count FROM candidates), latest_hashes AS MATERIALIZED (
 SELECT prescription.session_id,prescription.placement_id,prescription.membership_id,historical_subject.id AS athlete_user_id,prescription.snapshot_sha256
 FROM header JOIN public.training_prescriptions prescription ON prescription.publication_id=header.latest_publication_id LEFT JOIN public.users historical_subject ON historical_subject.id=prescription.athlete_user_id ORDER BY prescription.session_id,prescription.membership_id LIMIT 4097
), latest_stats AS MATERIALIZED(SELECT count(*)::bigint AS latest_tuple_count FROM latest_hashes), input_gate AS MATERIALIZED (
 SELECT header.plan_id FROM header CROSS JOIN candidate_stats CROSS JOIN latest_stats WHERE candidate_stats.raw_tuple_count<=4096 AND latest_stats.latest_tuple_count<=4096
),
eligible_recipients AS (
 SELECT candidate.placement_id,candidate.membership_id,candidate.subject_id AS athlete_user_id,subject.name AS athlete_name,candidate.starts_on,candidate.ends_on,candidate.session_id
 FROM candidates candidate JOIN input_gate ON true JOIN public.users subject ON subject.id=candidate.subject_id
 WHERE candidate.membership_id IS NOT NULL AND subject.is_active AND subject.erased_at IS NULL AND (NOT subject.is_dependent OR EXISTS(SELECT 1 FROM public.guardian_authority_relationships relationship WHERE relationship.subject_user_id=subject.id AND public.guardian_authority_current(relationship.guardian_user_id,subject.id)))
),
unordered_overview AS (
SELECT 0::bigint AS _ord,group_row.id AS group_id, group_row.name AS group_name,
       programme.name_pt AS programme_name, team.name AS team_name,
       0::integer AS member_count,
       plan.id AS plan_id, plan.title AS plan_title, plan.description AS plan_description, season.name AS season_name, plan.week_start, plan.planned_load_percentage,
       session.id AS session_id, session.title AS session_title, session.description AS session_description,
       session.starts_at, session.ends_at, session.entry_kind,
       session.schedule_precision,session.scheduled_on,session.day_band,
       public.training_session_local_date(session.scheduled_on,session.starts_at) AS session_on, session.status AS session_status,
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
FROM input_gate JOIN scoped scoped_plan ON scoped_plan.id=input_gate.plan_id JOIN training_groups group_row ON group_row.id=scoped_plan.group_id
LEFT JOIN teams team ON team.id = group_row.team_id
JOIN programmes programme ON programme.id = COALESCE(group_row.programme_id, team.programme_id)
JOIN training_plans plan ON plan.id=scoped_plan.id
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

), water_nodes AS (
 SELECT DISTINCT water_step_id AS id,water_parent_step_id AS parent_id,water_step_position AS position FROM unordered_overview WHERE water_step_id IS NOT NULL
), water_tree(id,order_path,id_path) AS (
 SELECT root.id,ARRAY[root.position]::integer[],ARRAY[root.id]::uuid[] FROM water_nodes root WHERE root.parent_id IS NULL
 UNION ALL
 SELECT child.id,water_tree.order_path||child.position,water_tree.id_path||child.id FROM water_nodes child JOIN water_tree ON child.parent_id=water_tree.id WHERE NOT child.id=ANY(water_tree.id_path)
), overview AS (
 SELECT row_number() OVER(ORDER BY source.session_on NULLS LAST,source.starts_at NULLS LAST,source.session_id,source.segment_position,source.block_position,water_tree.order_path NULLS LAST,water_tree.id_path NULLS LAST,source.water_parent_step_id NULLS FIRST,source.water_step_position,source.water_step_id,source.exercise_position,source.exercise_id) AS _ord,source.group_id,source.group_name,source.programme_name,source.team_name,source.member_count,source.plan_id,source.plan_title,source.plan_description,source.season_name,source.week_start,source.planned_load_percentage,source.session_id,source.session_title,source.session_description,source.starts_at,source.ends_at,source.entry_kind,source.schedule_precision,source.scheduled_on,source.day_band,source.session_on,source.session_status,source.segment_id,source.segment_position,source.segment_modality,source.segment_title,source.segment_location,source.planned_duration_minutes,source.planned_start_offset_minutes,source.transition_duration_minutes,source.equipment_notes,source.block_id,source.block_position,source.block_purpose,source.block_title,source.block_instructions,source.gym_structure,source.gym_objective,source.gym_rounds,source.round_recovery_seconds,source.water_method,source.water_intensity_profile_id,source.water_profile_name,source.water_profile_revision,source.water_profile_craft,source.water_target_distance_metres,source.water_target_distance_certainty,source.water_step_id,source.water_parent_step_id,source.water_step_position,source.water_step_kind,source.water_step_name,source.water_step_repeats,source.water_step_duration_seconds,source.water_step_duration_certainty,source.water_step_distance_metres,source.water_step_distance_certainty,source.water_step_recovery_seconds,source.water_step_intensity_code,source.water_step_cadence_spm,source.water_zone_label,source.water_zone_cadence_min,source.water_zone_cadence_max,source.water_zone_meaning,source.water_step_drill_focus,source.water_step_drill_format,source.water_step_role_notes,source.water_step_instructions,source.exercise_id,source.exercise_position,source.exercise_name,source.exercise_sets,source.exercise_repetitions,source.exercise_duration_seconds,source.exercise_distance_metres,source.exercise_recovery_seconds,source.resistance_kind,source.resistance_value,source.resistance_text,source.execution_intent,source.tempo,source.exercise_notes
 FROM unordered_overview source LEFT JOIN water_tree ON water_tree.id=source.water_step_id
),
variation_definitions AS (
 SELECT variation.id AS variation_id,variation.plan_id,variation.subject_kind,variation.subject_id,variation.target_membership_id,variation.target_group_id,variation.operation,variation.patch,variation.change_summary,variation.version,variation.created_at
 FROM input_gate JOIN public.training_variations variation ON variation.plan_id=input_gate.plan_id WHERE variation.is_active
), variation_targets AS (
 SELECT variation.variation_id,variation.target_membership_id AS membership_id,2::integer AS priority,NULL::uuid AS target_group_id,'Atleta'::text AS target_kind,NULL::text AS target_group_name,NULL::date AS effective_from,NULL::date AS effective_until,NULL::uuid AS competition_event_id
 FROM variation_definitions variation WHERE variation.target_membership_id IS NOT NULL
 UNION ALL
 SELECT variation.variation_id,member.membership_id,1,variation_group.id,CASE variation_group.kind WHEN 'CREW' THEN 'Tripulação' ELSE 'Subgrupo' END,variation_group.name::text,variation_group.effective_from,COALESCE(variation_group.effective_until,(event_row.starts_at AT TIME ZONE 'Europe/Lisbon')::date),variation_group.competition_event_id
 FROM variation_definitions variation JOIN public.training_variation_groups variation_group ON variation_group.id=variation.target_group_id JOIN scoped plan ON plan.group_id=variation_group.training_group_id JOIN public.training_variation_group_members member ON member.variation_group_id=variation_group.id LEFT JOIN public.events event_row ON event_row.id=variation_group.competition_event_id
), referenced_events AS (
 SELECT DISTINCT event_row.id AS event_id,event_row.starts_at FROM variation_definitions variation JOIN public.training_variation_groups variation_group ON variation_group.id=variation.target_group_id JOIN scoped plan ON plan.group_id=variation_group.training_group_id JOIN public.events event_row ON event_row.id=variation_group.competition_event_id
),
subject_details AS (
    SELECT segment.id AS subject_id, 'SEGMENT'::training_variation_subject_kind AS subject_kind,
           session.plan_id, session.id AS session_id, session.title AS session_title, session.starts_at,
           public.training_session_local_date(session.scheduled_on,session.starts_at) AS session_on,
           session.ends_at,session.schedule_precision,session.scheduled_on,session.day_band,
           concat('Segmento · ', segment.position, ' · ', segment.modality::text,
                  CASE WHEN segment.title = '' THEN '' ELSE ' · ' || segment.title END)::text AS subject_label
    FROM input_gate JOIN training_sessions selected_session ON selected_session.plan_id=input_gate.plan_id JOIN training_session_segments segment ON segment.session_id=selected_session.id
    JOIN training_sessions session ON session.id = segment.session_id
    UNION ALL
    SELECT block.id, 'BLOCK'::training_variation_subject_kind, session.plan_id, session.id,
           session.title, session.starts_at,
           public.training_session_local_date(session.scheduled_on,session.starts_at),
           session.ends_at,session.schedule_precision,session.scheduled_on,session.day_band,
           concat('Bloco · ', block.position, ' · ', block.purpose::text,
                  CASE WHEN block.title = '' THEN '' ELSE ' · ' || block.title END)::text
    FROM input_gate JOIN training_sessions selected_session ON selected_session.plan_id=input_gate.plan_id JOIN training_session_segments selected_segment ON selected_segment.session_id=selected_session.id JOIN training_segment_blocks block ON block.segment_id=selected_segment.id
    JOIN training_session_segments segment ON segment.id = block.segment_id
    JOIN training_sessions session ON session.id = segment.session_id
    UNION ALL
    SELECT step.id, 'WATER_STEP'::training_variation_subject_kind, session.plan_id, session.id,
           session.title, session.starts_at,
           public.training_session_local_date(session.scheduled_on,session.starts_at),
           session.ends_at,session.schedule_precision,session.scheduled_on,session.day_band,
           concat('Passo de água · ', step.position, ' · ', step.name)::text
    FROM input_gate JOIN training_sessions selected_session ON selected_session.plan_id=input_gate.plan_id JOIN training_session_segments selected_segment ON selected_segment.session_id=selected_session.id JOIN training_segment_blocks selected_block ON selected_block.segment_id=selected_segment.id JOIN water_work_steps step ON step.block_id=selected_block.id
    JOIN training_segment_blocks block ON block.id = step.block_id
    JOIN training_session_segments segment ON segment.id = block.segment_id
    JOIN training_sessions session ON session.id = segment.session_id
    UNION ALL
    SELECT exercise.id, 'GYM_EXERCISE'::training_variation_subject_kind, session.plan_id, session.id,
           session.title, session.starts_at,
           public.training_session_local_date(session.scheduled_on,session.starts_at),
           session.ends_at,session.schedule_precision,session.scheduled_on,session.day_band,
           concat('Exercício · ', exercise.position, ' · ', exercise.name)::text
    FROM input_gate JOIN training_sessions selected_session ON selected_session.plan_id=input_gate.plan_id JOIN training_session_segments selected_segment ON selected_segment.session_id=selected_session.id JOIN training_segment_blocks selected_block ON selected_block.segment_id=selected_segment.id JOIN gym_exercises exercise ON exercise.block_id=selected_block.id
    JOIN training_segment_blocks block ON block.id = exercise.block_id
    JOIN training_session_segments segment ON segment.id = block.segment_id
    JOIN training_sessions session ON session.id = segment.session_id
),
variation_matches AS (
 SELECT variation.variation_id,variation.plan_id,plan.title AS plan_title,details.session_id,details.session_title,details.starts_at,details.session_on,details.ends_at,details.schedule_precision,details.scheduled_on,details.day_band,
 variation.subject_kind,variation.subject_id,details.subject_label,variation.operation,variation.change_summary,variation.patch,variation.version,targets.membership_id,recipient.athlete_name,targets.priority,targets.target_kind,
 COALESCE(CASE WHEN targets.priority=2 THEN recipient.athlete_name::text ELSE targets.target_group_name END,''::text)::text AS target_name,targets.effective_from,targets.effective_until,
 row_number() OVER(ORDER BY details.session_on,details.starts_at NULLS LAST,recipient.athlete_name,details.subject_label,targets.priority DESC,variation.created_at,variation.variation_id) AS _ord
 FROM variation_definitions variation JOIN scoped plan ON plan.id=variation.plan_id JOIN subject_details details ON details.plan_id=variation.plan_id AND details.subject_id=variation.subject_id AND details.subject_kind=variation.subject_kind
 JOIN variation_targets targets ON targets.variation_id=variation.variation_id JOIN eligible_recipients recipient ON recipient.membership_id=targets.membership_id AND recipient.session_id=details.session_id
 WHERE (targets.effective_from IS NULL OR details.session_on>=targets.effective_from) AND (targets.effective_until IS NULL OR details.session_on<=targets.effective_until)
),
header_record AS MATERIALIZED(SELECT (pg_catalog.jsonb_build_object('plan_id',header.plan_id,'group_id',header.group_id,'season_id',header.season_id,'week_start',header.week_start,'source_updated_at',header.source_updated_at,'latest_publication_id',header.latest_publication_id,'latest_revision',header.latest_revision,'latest_supersedes_id',header.latest_supersedes_id,'latest_source_updated_at',header.latest_source_updated_at))::text AS encoded FROM header),
records AS NOT MATERIALIZED (
SELECT 0::integer AS category,row_number() OVER(ORDER BY _ord) AS ordinal,(pg_catalog.jsonb_build_object('group_id',record.group_id,'group_name',record.group_name,'programme_name',record.programme_name,'team_name',record.team_name,'member_count',record.member_count,'plan_id',record.plan_id,'plan_title',record.plan_title,'plan_description',record.plan_description,'season_name',record.season_name,'week_start',record.week_start,'planned_load_percentage',record.planned_load_percentage,'session_id',record.session_id,'session_title',record.session_title,'session_description',record.session_description,'starts_at',record.starts_at,'ends_at',record.ends_at,'entry_kind',record.entry_kind,'schedule_precision',record.schedule_precision,'scheduled_on',record.scheduled_on,'day_band',record.day_band,'session_on',record.session_on,'session_status',record.session_status,'segment_id',record.segment_id,'segment_position',record.segment_position,'segment_modality',record.segment_modality,'segment_title',record.segment_title,'segment_location',record.segment_location,'planned_duration_minutes',record.planned_duration_minutes,'planned_start_offset_minutes',record.planned_start_offset_minutes,'transition_duration_minutes',record.transition_duration_minutes,'equipment_notes',record.equipment_notes,'block_id',record.block_id,'block_position',record.block_position,'block_purpose',record.block_purpose,'block_title',record.block_title,'block_instructions',record.block_instructions,'gym_structure',record.gym_structure,'gym_objective',record.gym_objective,'gym_rounds',record.gym_rounds,'round_recovery_seconds',record.round_recovery_seconds) OPERATOR(pg_catalog.||) pg_catalog.jsonb_build_object('water_method',record.water_method,'water_intensity_profile_id',record.water_intensity_profile_id,'water_profile_name',record.water_profile_name,'water_profile_revision',record.water_profile_revision,'water_profile_craft',record.water_profile_craft,'water_target_distance_metres',record.water_target_distance_metres,'water_target_distance_certainty',record.water_target_distance_certainty,'water_step_id',record.water_step_id,'water_parent_step_id',record.water_parent_step_id,'water_step_position',record.water_step_position,'water_step_kind',record.water_step_kind,'water_step_name',record.water_step_name,'water_step_repeats',record.water_step_repeats,'water_step_duration_seconds',record.water_step_duration_seconds,'water_step_duration_certainty',record.water_step_duration_certainty,'water_step_distance_metres',record.water_step_distance_metres,'water_step_distance_certainty',record.water_step_distance_certainty,'water_step_recovery_seconds',record.water_step_recovery_seconds,'water_step_intensity_code',record.water_step_intensity_code,'water_step_cadence_spm',record.water_step_cadence_spm,'water_zone_label',record.water_zone_label,'water_zone_cadence_min',record.water_zone_cadence_min,'water_zone_cadence_max',record.water_zone_cadence_max,'water_zone_meaning',record.water_zone_meaning,'water_step_drill_focus',record.water_step_drill_focus,'water_step_drill_format',record.water_step_drill_format,'water_step_role_notes',record.water_step_role_notes,'water_step_instructions',record.water_step_instructions,'exercise_id',record.exercise_id,'exercise_position',record.exercise_position,'exercise_name',record.exercise_name,'exercise_sets',record.exercise_sets,'exercise_repetitions',record.exercise_repetitions,'exercise_duration_seconds',record.exercise_duration_seconds,'exercise_distance_metres',record.exercise_distance_metres,'exercise_recovery_seconds',record.exercise_recovery_seconds,'resistance_kind',record.resistance_kind,'resistance_value',record.resistance_value,'resistance_text',record.resistance_text,'execution_intent',record.execution_intent) OPERATOR(pg_catalog.||) pg_catalog.jsonb_build_object('tempo',record.tempo,'exercise_notes',record.exercise_notes))::text AS encoded FROM overview record JOIN input_gate ON true
UNION ALL
SELECT 1::integer AS category,row_number() OVER(ORDER BY session_id,placement_id,membership_id) AS ordinal,(pg_catalog.jsonb_build_object('session_id',record.session_id,'placement_id',record.placement_id,'membership_id',record.membership_id,'subject_id',record.subject_id,'starts_on',record.starts_on,'ends_on',record.ends_on,'session_on',record.session_on))::text AS encoded FROM candidates record JOIN input_gate ON true
UNION ALL
SELECT 2::integer AS category,row_number() OVER(ORDER BY session_id,membership_id) AS ordinal,(pg_catalog.jsonb_build_object('placement_id',record.placement_id,'membership_id',record.membership_id,'athlete_user_id',record.athlete_user_id,'athlete_name',record.athlete_name,'starts_on',record.starts_on,'ends_on',record.ends_on,'session_id',record.session_id))::text AS encoded FROM eligible_recipients record JOIN input_gate ON true
UNION ALL
SELECT 3::integer AS category,row_number() OVER(ORDER BY created_at,variation_id) AS ordinal,(pg_catalog.jsonb_build_object('variation_id',record.variation_id,'plan_id',record.plan_id,'subject_kind',record.subject_kind,'subject_id',record.subject_id,'target_membership_id',record.target_membership_id,'target_group_id',record.target_group_id,'operation',record.operation,'patch',record.patch,'change_summary',record.change_summary,'version',record.version,'created_at',record.created_at))::text AS encoded FROM variation_definitions record JOIN input_gate ON true
UNION ALL
SELECT 4::integer AS category,row_number() OVER(ORDER BY variation_id,membership_id) AS ordinal,(pg_catalog.jsonb_build_object('variation_id',record.variation_id,'membership_id',record.membership_id,'priority',record.priority,'target_group_id',record.target_group_id,'target_kind',record.target_kind,'target_group_name',record.target_group_name,'effective_from',record.effective_from,'effective_until',record.effective_until,'competition_event_id',record.competition_event_id))::text AS encoded FROM variation_targets record JOIN input_gate ON true
UNION ALL
SELECT 5::integer AS category,row_number() OVER(ORDER BY _ord) AS ordinal,(pg_catalog.jsonb_build_object('variation_id',record.variation_id,'plan_id',record.plan_id,'plan_title',record.plan_title,'session_id',record.session_id,'session_title',record.session_title,'starts_at',record.starts_at,'session_on',record.session_on,'ends_at',record.ends_at,'schedule_precision',record.schedule_precision,'scheduled_on',record.scheduled_on,'day_band',record.day_band,'subject_kind',record.subject_kind,'subject_id',record.subject_id,'subject_label',record.subject_label,'operation',record.operation,'change_summary',record.change_summary,'patch',record.patch,'version',record.version,'membership_id',record.membership_id,'athlete_name',record.athlete_name,'priority',record.priority,'target_kind',record.target_kind,'target_name',record.target_name,'effective_from',record.effective_from,'effective_until',record.effective_until))::text AS encoded FROM variation_matches record JOIN input_gate ON true
UNION ALL
SELECT 6::integer AS category,row_number() OVER(ORDER BY event_id) AS ordinal,(pg_catalog.jsonb_build_object('event_id',record.event_id,'starts_at',record.starts_at))::text AS encoded FROM referenced_events record JOIN input_gate ON true
UNION ALL
SELECT 7::integer AS category,row_number() OVER(ORDER BY session_id,membership_id) AS ordinal,(pg_catalog.jsonb_build_object('session_id',record.session_id,'membership_id',record.membership_id,'athlete_user_id',record.athlete_user_id,'placement_id',record.placement_id,'snapshot_sha256',record.snapshot_sha256))::text AS encoded FROM latest_hashes record JOIN input_gate ON true
),
record_totals AS (SELECT category,count(*) AS records,COALESCE(sum(octet_length(convert_to(encoded,'UTF8'))::numeric),0) AS bytes FROM records GROUP BY category), budget AS MATERIALIZED (
 SELECT LEAST(33554433::numeric,238::numeric+octet_length(convert_to(header_record.encoded,'UTF8'))+COALESCE((SELECT sum(bytes+GREATEST(records-1,0)) FROM record_totals),0))::bigint AS source_bytes,
 candidate_stats.raw_tuple_count,latest_stats.latest_tuple_count,
 COALESCE((candidate_stats.raw_tuple_count>4096 OR latest_stats.latest_tuple_count>4096 OR 238::numeric+octet_length(convert_to(header_record.encoded,'UTF8'))+COALESCE((SELECT sum(bytes+GREATEST(records-1,0)) FROM record_totals),0)>33554432),false)::boolean AS overflow
 FROM header_record CROSS JOIN candidate_stats CROSS JOIN latest_stats
), admitted AS MATERIALIZED(SELECT * FROM budget WHERE NOT overflow)

SELECT recipient.placement_id,recipient.membership_id,recipient.athlete_user_id,recipient.athlete_name,recipient.starts_on,recipient.ends_on,recipient.session_id FROM eligible_recipients recipient JOIN admitted ON true ORDER BY recipient.athlete_name,recipient.membership_id,recipient.session_id;

-- name: PublicationVariationMatches :many
WITH RECURSIVE actor AS (
 SELECT actor_user.id,EXISTS(SELECT 1 FROM public.user_platform_roles assignment JOIN public.platform_roles role ON role.id=assignment.role_id WHERE assignment.user_id=actor_user.id AND role.code='ADMIN') AS admin
 FROM public.users actor_user WHERE actor_user.id=sqlc.arg(actor_id)::uuid AND actor_user.is_active AND actor_user.erased_at IS NULL AND NOT actor_user.is_dependent
 AND actor_user.date_of_birth<=((now() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date
), scoped AS MATERIALIZED (
 SELECT plan.id,plan.title,plan.description,plan.programme_id,plan.team_id,plan.training_group_id,plan.season_id,plan.week_start,plan.planned_load_percentage,plan.updated_at,
 group_row.id AS group_id,group_row.name AS group_name,group_row.programme_id AS group_programme_id,group_row.team_id AS group_team_id
 FROM public.training_plans plan JOIN public.training_groups group_row ON group_row.id=plan.training_group_id JOIN public.seasons season ON season.id=plan.season_id CROSS JOIN actor
 WHERE plan.id=sqlc.arg(plan_id)::uuid AND (actor.admin OR EXISTS(SELECT 1 FROM public.staff_grants grant_row WHERE grant_row.user_id=actor.id AND grant_row.capability='COACH' AND grant_row.revoked_at IS NULL
 AND ((group_row.programme_id IS NOT NULL AND grant_row.programme_id=group_row.programme_id) OR (group_row.team_id IS NOT NULL AND grant_row.team_id=group_row.team_id))))
), header AS (
 SELECT plan.id AS plan_id,plan.group_id,plan.season_id,plan.week_start,plan.updated_at AS source_updated_at,
 latest.id AS latest_publication_id,COALESCE(latest.revision,0)::integer AS latest_revision,latest.supersedes_id AS latest_supersedes_id,latest.source_updated_at AS latest_source_updated_at
 FROM scoped plan LEFT JOIN public.training_plan_publications latest ON latest.id=(SELECT publication.id FROM public.training_plan_publications publication WHERE publication.plan_id=plan.id ORDER BY publication.revision DESC LIMIT 1)
),
candidates AS MATERIALIZED (
 SELECT DISTINCT session.id AS session_id,placement.id AS placement_id,membership.id AS membership_id,structural_subject.id AS subject_id,membership.starts_on,membership.ends_on,
 public.training_session_local_date(session.scheduled_on,session.starts_at) AS session_on
 FROM scoped plan JOIN public.training_group_placements placement ON placement.group_id=plan.group_id
 JOIN public.users structural_subject ON structural_subject.id=placement.subject_user_id
 JOIN public.training_sessions session ON session.plan_id=plan.id
 LEFT JOIN LATERAL public.training_placement_resolve(placement.subject_user_id,plan.group_id,plan.season_id,public.training_session_local_date(session.scheduled_on,session.starts_at)) resolved ON resolved.placement_id=placement.id
 LEFT JOIN public.user_memberships membership ON membership.id=resolved.membership_id
 AND(plan.programme_id IS NULL OR membership.programme_id=plan.programme_id) AND(plan.team_id IS NULL OR membership.team_id=plan.team_id)
 WHERE placement.subject_user_id IS NOT NULL AND placement.superseded_by_change_id IS NULL AND placement.cancelled_by_change_id IS NULL
 AND public.training_session_local_date(session.scheduled_on,session.starts_at) BETWEEN placement.starts_on AND COALESCE(placement.ends_on,'infinity'::date)
 ORDER BY session.id,placement.id,membership.id LIMIT 4097
), candidate_stats AS MATERIALIZED (SELECT count(*)::bigint AS raw_tuple_count FROM candidates), latest_hashes AS MATERIALIZED (
 SELECT prescription.session_id,prescription.placement_id,prescription.membership_id,historical_subject.id AS athlete_user_id,prescription.snapshot_sha256
 FROM header JOIN public.training_prescriptions prescription ON prescription.publication_id=header.latest_publication_id LEFT JOIN public.users historical_subject ON historical_subject.id=prescription.athlete_user_id ORDER BY prescription.session_id,prescription.membership_id LIMIT 4097
), latest_stats AS MATERIALIZED(SELECT count(*)::bigint AS latest_tuple_count FROM latest_hashes), input_gate AS MATERIALIZED (
 SELECT header.plan_id FROM header CROSS JOIN candidate_stats CROSS JOIN latest_stats WHERE candidate_stats.raw_tuple_count<=4096 AND latest_stats.latest_tuple_count<=4096
),
eligible_recipients AS (
 SELECT candidate.placement_id,candidate.membership_id,candidate.subject_id AS athlete_user_id,subject.name AS athlete_name,candidate.starts_on,candidate.ends_on,candidate.session_id
 FROM candidates candidate JOIN input_gate ON true JOIN public.users subject ON subject.id=candidate.subject_id
 WHERE candidate.membership_id IS NOT NULL AND subject.is_active AND subject.erased_at IS NULL AND (NOT subject.is_dependent OR EXISTS(SELECT 1 FROM public.guardian_authority_relationships relationship WHERE relationship.subject_user_id=subject.id AND public.guardian_authority_current(relationship.guardian_user_id,subject.id)))
),
unordered_overview AS (
SELECT 0::bigint AS _ord,group_row.id AS group_id, group_row.name AS group_name,
       programme.name_pt AS programme_name, team.name AS team_name,
       0::integer AS member_count,
       plan.id AS plan_id, plan.title AS plan_title, plan.description AS plan_description, season.name AS season_name, plan.week_start, plan.planned_load_percentage,
       session.id AS session_id, session.title AS session_title, session.description AS session_description,
       session.starts_at, session.ends_at, session.entry_kind,
       session.schedule_precision,session.scheduled_on,session.day_band,
       public.training_session_local_date(session.scheduled_on,session.starts_at) AS session_on, session.status AS session_status,
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
FROM input_gate JOIN scoped scoped_plan ON scoped_plan.id=input_gate.plan_id JOIN training_groups group_row ON group_row.id=scoped_plan.group_id
LEFT JOIN teams team ON team.id = group_row.team_id
JOIN programmes programme ON programme.id = COALESCE(group_row.programme_id, team.programme_id)
JOIN training_plans plan ON plan.id=scoped_plan.id
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

), water_nodes AS (
 SELECT DISTINCT water_step_id AS id,water_parent_step_id AS parent_id,water_step_position AS position FROM unordered_overview WHERE water_step_id IS NOT NULL
), water_tree(id,order_path,id_path) AS (
 SELECT root.id,ARRAY[root.position]::integer[],ARRAY[root.id]::uuid[] FROM water_nodes root WHERE root.parent_id IS NULL
 UNION ALL
 SELECT child.id,water_tree.order_path||child.position,water_tree.id_path||child.id FROM water_nodes child JOIN water_tree ON child.parent_id=water_tree.id WHERE NOT child.id=ANY(water_tree.id_path)
), overview AS (
 SELECT row_number() OVER(ORDER BY source.session_on NULLS LAST,source.starts_at NULLS LAST,source.session_id,source.segment_position,source.block_position,water_tree.order_path NULLS LAST,water_tree.id_path NULLS LAST,source.water_parent_step_id NULLS FIRST,source.water_step_position,source.water_step_id,source.exercise_position,source.exercise_id) AS _ord,source.group_id,source.group_name,source.programme_name,source.team_name,source.member_count,source.plan_id,source.plan_title,source.plan_description,source.season_name,source.week_start,source.planned_load_percentage,source.session_id,source.session_title,source.session_description,source.starts_at,source.ends_at,source.entry_kind,source.schedule_precision,source.scheduled_on,source.day_band,source.session_on,source.session_status,source.segment_id,source.segment_position,source.segment_modality,source.segment_title,source.segment_location,source.planned_duration_minutes,source.planned_start_offset_minutes,source.transition_duration_minutes,source.equipment_notes,source.block_id,source.block_position,source.block_purpose,source.block_title,source.block_instructions,source.gym_structure,source.gym_objective,source.gym_rounds,source.round_recovery_seconds,source.water_method,source.water_intensity_profile_id,source.water_profile_name,source.water_profile_revision,source.water_profile_craft,source.water_target_distance_metres,source.water_target_distance_certainty,source.water_step_id,source.water_parent_step_id,source.water_step_position,source.water_step_kind,source.water_step_name,source.water_step_repeats,source.water_step_duration_seconds,source.water_step_duration_certainty,source.water_step_distance_metres,source.water_step_distance_certainty,source.water_step_recovery_seconds,source.water_step_intensity_code,source.water_step_cadence_spm,source.water_zone_label,source.water_zone_cadence_min,source.water_zone_cadence_max,source.water_zone_meaning,source.water_step_drill_focus,source.water_step_drill_format,source.water_step_role_notes,source.water_step_instructions,source.exercise_id,source.exercise_position,source.exercise_name,source.exercise_sets,source.exercise_repetitions,source.exercise_duration_seconds,source.exercise_distance_metres,source.exercise_recovery_seconds,source.resistance_kind,source.resistance_value,source.resistance_text,source.execution_intent,source.tempo,source.exercise_notes
 FROM unordered_overview source LEFT JOIN water_tree ON water_tree.id=source.water_step_id
),
variation_definitions AS (
 SELECT variation.id AS variation_id,variation.plan_id,variation.subject_kind,variation.subject_id,variation.target_membership_id,variation.target_group_id,variation.operation,variation.patch,variation.change_summary,variation.version,variation.created_at
 FROM input_gate JOIN public.training_variations variation ON variation.plan_id=input_gate.plan_id WHERE variation.is_active
), variation_targets AS (
 SELECT variation.variation_id,variation.target_membership_id AS membership_id,2::integer AS priority,NULL::uuid AS target_group_id,'Atleta'::text AS target_kind,NULL::text AS target_group_name,NULL::date AS effective_from,NULL::date AS effective_until,NULL::uuid AS competition_event_id
 FROM variation_definitions variation WHERE variation.target_membership_id IS NOT NULL
 UNION ALL
 SELECT variation.variation_id,member.membership_id,1,variation_group.id,CASE variation_group.kind WHEN 'CREW' THEN 'Tripulação' ELSE 'Subgrupo' END,variation_group.name::text,variation_group.effective_from,COALESCE(variation_group.effective_until,(event_row.starts_at AT TIME ZONE 'Europe/Lisbon')::date),variation_group.competition_event_id
 FROM variation_definitions variation JOIN public.training_variation_groups variation_group ON variation_group.id=variation.target_group_id JOIN scoped plan ON plan.group_id=variation_group.training_group_id JOIN public.training_variation_group_members member ON member.variation_group_id=variation_group.id LEFT JOIN public.events event_row ON event_row.id=variation_group.competition_event_id
), referenced_events AS (
 SELECT DISTINCT event_row.id AS event_id,event_row.starts_at FROM variation_definitions variation JOIN public.training_variation_groups variation_group ON variation_group.id=variation.target_group_id JOIN scoped plan ON plan.group_id=variation_group.training_group_id JOIN public.events event_row ON event_row.id=variation_group.competition_event_id
),
subject_details AS (
    SELECT segment.id AS subject_id, 'SEGMENT'::training_variation_subject_kind AS subject_kind,
           session.plan_id, session.id AS session_id, session.title AS session_title, session.starts_at,
           public.training_session_local_date(session.scheduled_on,session.starts_at) AS session_on,
           session.ends_at,session.schedule_precision,session.scheduled_on,session.day_band,
           concat('Segmento · ', segment.position, ' · ', segment.modality::text,
                  CASE WHEN segment.title = '' THEN '' ELSE ' · ' || segment.title END)::text AS subject_label
    FROM input_gate JOIN training_sessions selected_session ON selected_session.plan_id=input_gate.plan_id JOIN training_session_segments segment ON segment.session_id=selected_session.id
    JOIN training_sessions session ON session.id = segment.session_id
    UNION ALL
    SELECT block.id, 'BLOCK'::training_variation_subject_kind, session.plan_id, session.id,
           session.title, session.starts_at,
           public.training_session_local_date(session.scheduled_on,session.starts_at),
           session.ends_at,session.schedule_precision,session.scheduled_on,session.day_band,
           concat('Bloco · ', block.position, ' · ', block.purpose::text,
                  CASE WHEN block.title = '' THEN '' ELSE ' · ' || block.title END)::text
    FROM input_gate JOIN training_sessions selected_session ON selected_session.plan_id=input_gate.plan_id JOIN training_session_segments selected_segment ON selected_segment.session_id=selected_session.id JOIN training_segment_blocks block ON block.segment_id=selected_segment.id
    JOIN training_session_segments segment ON segment.id = block.segment_id
    JOIN training_sessions session ON session.id = segment.session_id
    UNION ALL
    SELECT step.id, 'WATER_STEP'::training_variation_subject_kind, session.plan_id, session.id,
           session.title, session.starts_at,
           public.training_session_local_date(session.scheduled_on,session.starts_at),
           session.ends_at,session.schedule_precision,session.scheduled_on,session.day_band,
           concat('Passo de água · ', step.position, ' · ', step.name)::text
    FROM input_gate JOIN training_sessions selected_session ON selected_session.plan_id=input_gate.plan_id JOIN training_session_segments selected_segment ON selected_segment.session_id=selected_session.id JOIN training_segment_blocks selected_block ON selected_block.segment_id=selected_segment.id JOIN water_work_steps step ON step.block_id=selected_block.id
    JOIN training_segment_blocks block ON block.id = step.block_id
    JOIN training_session_segments segment ON segment.id = block.segment_id
    JOIN training_sessions session ON session.id = segment.session_id
    UNION ALL
    SELECT exercise.id, 'GYM_EXERCISE'::training_variation_subject_kind, session.plan_id, session.id,
           session.title, session.starts_at,
           public.training_session_local_date(session.scheduled_on,session.starts_at),
           session.ends_at,session.schedule_precision,session.scheduled_on,session.day_band,
           concat('Exercício · ', exercise.position, ' · ', exercise.name)::text
    FROM input_gate JOIN training_sessions selected_session ON selected_session.plan_id=input_gate.plan_id JOIN training_session_segments selected_segment ON selected_segment.session_id=selected_session.id JOIN training_segment_blocks selected_block ON selected_block.segment_id=selected_segment.id JOIN gym_exercises exercise ON exercise.block_id=selected_block.id
    JOIN training_segment_blocks block ON block.id = exercise.block_id
    JOIN training_session_segments segment ON segment.id = block.segment_id
    JOIN training_sessions session ON session.id = segment.session_id
),
variation_matches AS (
 SELECT variation.variation_id,variation.plan_id,plan.title AS plan_title,details.session_id,details.session_title,details.starts_at,details.session_on,details.ends_at,details.schedule_precision,details.scheduled_on,details.day_band,
 variation.subject_kind,variation.subject_id,details.subject_label,variation.operation,variation.change_summary,variation.patch,variation.version,targets.membership_id,recipient.athlete_name,targets.priority,targets.target_kind,
 COALESCE(CASE WHEN targets.priority=2 THEN recipient.athlete_name::text ELSE targets.target_group_name END,''::text)::text AS target_name,targets.effective_from,targets.effective_until,
 row_number() OVER(ORDER BY details.session_on,details.starts_at NULLS LAST,recipient.athlete_name,details.subject_label,targets.priority DESC,variation.created_at,variation.variation_id) AS _ord
 FROM variation_definitions variation JOIN scoped plan ON plan.id=variation.plan_id JOIN subject_details details ON details.plan_id=variation.plan_id AND details.subject_id=variation.subject_id AND details.subject_kind=variation.subject_kind
 JOIN variation_targets targets ON targets.variation_id=variation.variation_id JOIN eligible_recipients recipient ON recipient.membership_id=targets.membership_id AND recipient.session_id=details.session_id
 WHERE (targets.effective_from IS NULL OR details.session_on>=targets.effective_from) AND (targets.effective_until IS NULL OR details.session_on<=targets.effective_until)
),
header_record AS MATERIALIZED(SELECT (pg_catalog.jsonb_build_object('plan_id',header.plan_id,'group_id',header.group_id,'season_id',header.season_id,'week_start',header.week_start,'source_updated_at',header.source_updated_at,'latest_publication_id',header.latest_publication_id,'latest_revision',header.latest_revision,'latest_supersedes_id',header.latest_supersedes_id,'latest_source_updated_at',header.latest_source_updated_at))::text AS encoded FROM header),
records AS NOT MATERIALIZED (
SELECT 0::integer AS category,row_number() OVER(ORDER BY _ord) AS ordinal,(pg_catalog.jsonb_build_object('group_id',record.group_id,'group_name',record.group_name,'programme_name',record.programme_name,'team_name',record.team_name,'member_count',record.member_count,'plan_id',record.plan_id,'plan_title',record.plan_title,'plan_description',record.plan_description,'season_name',record.season_name,'week_start',record.week_start,'planned_load_percentage',record.planned_load_percentage,'session_id',record.session_id,'session_title',record.session_title,'session_description',record.session_description,'starts_at',record.starts_at,'ends_at',record.ends_at,'entry_kind',record.entry_kind,'schedule_precision',record.schedule_precision,'scheduled_on',record.scheduled_on,'day_band',record.day_band,'session_on',record.session_on,'session_status',record.session_status,'segment_id',record.segment_id,'segment_position',record.segment_position,'segment_modality',record.segment_modality,'segment_title',record.segment_title,'segment_location',record.segment_location,'planned_duration_minutes',record.planned_duration_minutes,'planned_start_offset_minutes',record.planned_start_offset_minutes,'transition_duration_minutes',record.transition_duration_minutes,'equipment_notes',record.equipment_notes,'block_id',record.block_id,'block_position',record.block_position,'block_purpose',record.block_purpose,'block_title',record.block_title,'block_instructions',record.block_instructions,'gym_structure',record.gym_structure,'gym_objective',record.gym_objective,'gym_rounds',record.gym_rounds,'round_recovery_seconds',record.round_recovery_seconds) OPERATOR(pg_catalog.||) pg_catalog.jsonb_build_object('water_method',record.water_method,'water_intensity_profile_id',record.water_intensity_profile_id,'water_profile_name',record.water_profile_name,'water_profile_revision',record.water_profile_revision,'water_profile_craft',record.water_profile_craft,'water_target_distance_metres',record.water_target_distance_metres,'water_target_distance_certainty',record.water_target_distance_certainty,'water_step_id',record.water_step_id,'water_parent_step_id',record.water_parent_step_id,'water_step_position',record.water_step_position,'water_step_kind',record.water_step_kind,'water_step_name',record.water_step_name,'water_step_repeats',record.water_step_repeats,'water_step_duration_seconds',record.water_step_duration_seconds,'water_step_duration_certainty',record.water_step_duration_certainty,'water_step_distance_metres',record.water_step_distance_metres,'water_step_distance_certainty',record.water_step_distance_certainty,'water_step_recovery_seconds',record.water_step_recovery_seconds,'water_step_intensity_code',record.water_step_intensity_code,'water_step_cadence_spm',record.water_step_cadence_spm,'water_zone_label',record.water_zone_label,'water_zone_cadence_min',record.water_zone_cadence_min,'water_zone_cadence_max',record.water_zone_cadence_max,'water_zone_meaning',record.water_zone_meaning,'water_step_drill_focus',record.water_step_drill_focus,'water_step_drill_format',record.water_step_drill_format,'water_step_role_notes',record.water_step_role_notes,'water_step_instructions',record.water_step_instructions,'exercise_id',record.exercise_id,'exercise_position',record.exercise_position,'exercise_name',record.exercise_name,'exercise_sets',record.exercise_sets,'exercise_repetitions',record.exercise_repetitions,'exercise_duration_seconds',record.exercise_duration_seconds,'exercise_distance_metres',record.exercise_distance_metres,'exercise_recovery_seconds',record.exercise_recovery_seconds,'resistance_kind',record.resistance_kind,'resistance_value',record.resistance_value,'resistance_text',record.resistance_text,'execution_intent',record.execution_intent) OPERATOR(pg_catalog.||) pg_catalog.jsonb_build_object('tempo',record.tempo,'exercise_notes',record.exercise_notes))::text AS encoded FROM overview record JOIN input_gate ON true
UNION ALL
SELECT 1::integer AS category,row_number() OVER(ORDER BY session_id,placement_id,membership_id) AS ordinal,(pg_catalog.jsonb_build_object('session_id',record.session_id,'placement_id',record.placement_id,'membership_id',record.membership_id,'subject_id',record.subject_id,'starts_on',record.starts_on,'ends_on',record.ends_on,'session_on',record.session_on))::text AS encoded FROM candidates record JOIN input_gate ON true
UNION ALL
SELECT 2::integer AS category,row_number() OVER(ORDER BY session_id,membership_id) AS ordinal,(pg_catalog.jsonb_build_object('placement_id',record.placement_id,'membership_id',record.membership_id,'athlete_user_id',record.athlete_user_id,'athlete_name',record.athlete_name,'starts_on',record.starts_on,'ends_on',record.ends_on,'session_id',record.session_id))::text AS encoded FROM eligible_recipients record JOIN input_gate ON true
UNION ALL
SELECT 3::integer AS category,row_number() OVER(ORDER BY created_at,variation_id) AS ordinal,(pg_catalog.jsonb_build_object('variation_id',record.variation_id,'plan_id',record.plan_id,'subject_kind',record.subject_kind,'subject_id',record.subject_id,'target_membership_id',record.target_membership_id,'target_group_id',record.target_group_id,'operation',record.operation,'patch',record.patch,'change_summary',record.change_summary,'version',record.version,'created_at',record.created_at))::text AS encoded FROM variation_definitions record JOIN input_gate ON true
UNION ALL
SELECT 4::integer AS category,row_number() OVER(ORDER BY variation_id,membership_id) AS ordinal,(pg_catalog.jsonb_build_object('variation_id',record.variation_id,'membership_id',record.membership_id,'priority',record.priority,'target_group_id',record.target_group_id,'target_kind',record.target_kind,'target_group_name',record.target_group_name,'effective_from',record.effective_from,'effective_until',record.effective_until,'competition_event_id',record.competition_event_id))::text AS encoded FROM variation_targets record JOIN input_gate ON true
UNION ALL
SELECT 5::integer AS category,row_number() OVER(ORDER BY _ord) AS ordinal,(pg_catalog.jsonb_build_object('variation_id',record.variation_id,'plan_id',record.plan_id,'plan_title',record.plan_title,'session_id',record.session_id,'session_title',record.session_title,'starts_at',record.starts_at,'session_on',record.session_on,'ends_at',record.ends_at,'schedule_precision',record.schedule_precision,'scheduled_on',record.scheduled_on,'day_band',record.day_band,'subject_kind',record.subject_kind,'subject_id',record.subject_id,'subject_label',record.subject_label,'operation',record.operation,'change_summary',record.change_summary,'patch',record.patch,'version',record.version,'membership_id',record.membership_id,'athlete_name',record.athlete_name,'priority',record.priority,'target_kind',record.target_kind,'target_name',record.target_name,'effective_from',record.effective_from,'effective_until',record.effective_until))::text AS encoded FROM variation_matches record JOIN input_gate ON true
UNION ALL
SELECT 6::integer AS category,row_number() OVER(ORDER BY event_id) AS ordinal,(pg_catalog.jsonb_build_object('event_id',record.event_id,'starts_at',record.starts_at))::text AS encoded FROM referenced_events record JOIN input_gate ON true
UNION ALL
SELECT 7::integer AS category,row_number() OVER(ORDER BY session_id,membership_id) AS ordinal,(pg_catalog.jsonb_build_object('session_id',record.session_id,'membership_id',record.membership_id,'athlete_user_id',record.athlete_user_id,'placement_id',record.placement_id,'snapshot_sha256',record.snapshot_sha256))::text AS encoded FROM latest_hashes record JOIN input_gate ON true
),
record_totals AS (SELECT category,count(*) AS records,COALESCE(sum(octet_length(convert_to(encoded,'UTF8'))::numeric),0) AS bytes FROM records GROUP BY category), budget AS MATERIALIZED (
 SELECT LEAST(33554433::numeric,238::numeric+octet_length(convert_to(header_record.encoded,'UTF8'))+COALESCE((SELECT sum(bytes+GREATEST(records-1,0)) FROM record_totals),0))::bigint AS source_bytes,
 candidate_stats.raw_tuple_count,latest_stats.latest_tuple_count,
 COALESCE((candidate_stats.raw_tuple_count>4096 OR latest_stats.latest_tuple_count>4096 OR 238::numeric+octet_length(convert_to(header_record.encoded,'UTF8'))+COALESCE((SELECT sum(bytes+GREATEST(records-1,0)) FROM record_totals),0)>33554432),false)::boolean AS overflow
 FROM header_record CROSS JOIN candidate_stats CROSS JOIN latest_stats
), admitted AS MATERIALIZED(SELECT * FROM budget WHERE NOT overflow)

SELECT variation.variation_id,variation.plan_id,variation.plan_title,variation.session_id,variation.session_title,variation.starts_at,variation.session_on,variation.ends_at,variation.schedule_precision,variation.scheduled_on,variation.day_band,variation.subject_kind,variation.subject_id,variation.subject_label,variation.operation,variation.change_summary,variation.patch,variation.version,variation.membership_id,variation.athlete_name,variation.priority,variation.target_kind,variation.target_name,variation.effective_from,variation.effective_until FROM variation_matches variation JOIN admitted ON true ORDER BY variation._ord;

-- name: PublicationLatestHashes :many
WITH RECURSIVE actor AS (
 SELECT actor_user.id,EXISTS(SELECT 1 FROM public.user_platform_roles assignment JOIN public.platform_roles role ON role.id=assignment.role_id WHERE assignment.user_id=actor_user.id AND role.code='ADMIN') AS admin
 FROM public.users actor_user WHERE actor_user.id=sqlc.arg(actor_id)::uuid AND actor_user.is_active AND actor_user.erased_at IS NULL AND NOT actor_user.is_dependent
 AND actor_user.date_of_birth<=((now() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date
), scoped AS MATERIALIZED (
 SELECT plan.id,plan.title,plan.description,plan.programme_id,plan.team_id,plan.training_group_id,plan.season_id,plan.week_start,plan.planned_load_percentage,plan.updated_at,
 group_row.id AS group_id,group_row.name AS group_name,group_row.programme_id AS group_programme_id,group_row.team_id AS group_team_id
 FROM public.training_plans plan JOIN public.training_groups group_row ON group_row.id=plan.training_group_id JOIN public.seasons season ON season.id=plan.season_id CROSS JOIN actor
 WHERE plan.id=sqlc.arg(plan_id)::uuid AND (actor.admin OR EXISTS(SELECT 1 FROM public.staff_grants grant_row WHERE grant_row.user_id=actor.id AND grant_row.capability='COACH' AND grant_row.revoked_at IS NULL
 AND ((group_row.programme_id IS NOT NULL AND grant_row.programme_id=group_row.programme_id) OR (group_row.team_id IS NOT NULL AND grant_row.team_id=group_row.team_id))))
), header AS (
 SELECT plan.id AS plan_id,plan.group_id,plan.season_id,plan.week_start,plan.updated_at AS source_updated_at,
 latest.id AS latest_publication_id,COALESCE(latest.revision,0)::integer AS latest_revision,latest.supersedes_id AS latest_supersedes_id,latest.source_updated_at AS latest_source_updated_at
 FROM scoped plan LEFT JOIN public.training_plan_publications latest ON latest.id=(SELECT publication.id FROM public.training_plan_publications publication WHERE publication.plan_id=plan.id ORDER BY publication.revision DESC LIMIT 1)
)
SELECT prescription.session_id,prescription.placement_id,prescription.membership_id,historical_subject.id AS athlete_user_id,prescription.snapshot_sha256 FROM scoped JOIN public.training_plan_publications publication ON publication.plan_id=scoped.id AND publication.id=sqlc.arg(publication_id)::uuid JOIN public.training_prescriptions prescription ON prescription.publication_id=publication.id LEFT JOIN public.users historical_subject ON historical_subject.id=prescription.athlete_user_id ORDER BY prescription.session_id,prescription.membership_id LIMIT 4097;

-- name: InsertPublication :one
WITH RECURSIVE actor AS (
 SELECT actor_user.id,EXISTS(SELECT 1 FROM public.user_platform_roles assignment JOIN public.platform_roles role ON role.id=assignment.role_id WHERE assignment.user_id=actor_user.id AND role.code='ADMIN') AS admin
 FROM public.users actor_user WHERE actor_user.id=sqlc.arg(actor_id)::uuid AND actor_user.is_active AND actor_user.erased_at IS NULL AND NOT actor_user.is_dependent
 AND actor_user.date_of_birth<=((now() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date
), scoped AS MATERIALIZED (
 SELECT plan.id,plan.title,plan.description,plan.programme_id,plan.team_id,plan.training_group_id,plan.season_id,plan.week_start,plan.planned_load_percentage,plan.updated_at,
 group_row.id AS group_id,group_row.name AS group_name,group_row.programme_id AS group_programme_id,group_row.team_id AS group_team_id
 FROM public.training_plans plan JOIN public.training_groups group_row ON group_row.id=plan.training_group_id JOIN public.seasons season ON season.id=plan.season_id CROSS JOIN actor
 WHERE plan.id=sqlc.arg(plan_id)::uuid AND (actor.admin OR EXISTS(SELECT 1 FROM public.staff_grants grant_row WHERE grant_row.user_id=actor.id AND grant_row.capability='COACH' AND grant_row.revoked_at IS NULL
 AND ((group_row.programme_id IS NOT NULL AND grant_row.programme_id=group_row.programme_id) OR (group_row.team_id IS NOT NULL AND grant_row.team_id=group_row.team_id))))
), header AS (
 SELECT plan.id AS plan_id,plan.group_id,plan.season_id,plan.week_start,plan.updated_at AS source_updated_at,
 latest.id AS latest_publication_id,COALESCE(latest.revision,0)::integer AS latest_revision,latest.supersedes_id AS latest_supersedes_id,latest.source_updated_at AS latest_source_updated_at
 FROM scoped plan LEFT JOIN public.training_plan_publications latest ON latest.id=(SELECT publication.id FROM public.training_plan_publications publication WHERE publication.plan_id=plan.id ORDER BY publication.revision DESC LIMIT 1)
), publication_witness AS (
 UPDATE ONLY public.training_plans changed_plan SET updated_at=changed_plan.updated_at FROM header
 WHERE changed_plan.id=header.plan_id AND header.source_updated_at=sqlc.arg(source_updated_at)::timestamptz AND header.latest_revision=sqlc.arg(expected_latest_revision)::integer
 AND header.latest_publication_id IS NOT DISTINCT FROM sqlc.narg(expected_latest_publication_id)::uuid
 RETURNING changed_plan.id,changed_plan.updated_at
)
INSERT INTO public.training_plan_publications(plan_id,revision,source_updated_at,change_summary,supersedes_id,published_by_id)
 SELECT header.plan_id,header.latest_revision+1,witness.updated_at,sqlc.arg(change_summary)::text,header.latest_publication_id,sqlc.arg(actor_id)::uuid FROM header JOIN publication_witness witness ON witness.id=header.plan_id
 RETURNING id,plan_id,revision,source_updated_at,change_summary,supersedes_id,published_by_id,published_at;

-- name: InsertPrescription :one
WITH RECURSIVE actor AS (
 SELECT actor_user.id,EXISTS(SELECT 1 FROM public.user_platform_roles assignment JOIN public.platform_roles role ON role.id=assignment.role_id WHERE assignment.user_id=actor_user.id AND role.code='ADMIN') AS admin
 FROM public.users actor_user WHERE actor_user.id=sqlc.arg(actor_id)::uuid AND actor_user.is_active AND actor_user.erased_at IS NULL AND NOT actor_user.is_dependent
 AND actor_user.date_of_birth<=((now() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date
), scoped AS MATERIALIZED (
 SELECT plan.id,plan.title,plan.description,plan.programme_id,plan.team_id,plan.training_group_id,plan.season_id,plan.week_start,plan.planned_load_percentage,plan.updated_at,
 group_row.id AS group_id,group_row.name AS group_name,group_row.programme_id AS group_programme_id,group_row.team_id AS group_team_id
 FROM public.training_plans plan JOIN public.training_groups group_row ON group_row.id=plan.training_group_id JOIN public.seasons season ON season.id=plan.season_id CROSS JOIN actor
 WHERE EXISTS(SELECT 1 FROM public.training_plan_publications publication WHERE publication.id=sqlc.arg(publication_id)::uuid AND publication.plan_id=plan.id AND publication.published_by_id=sqlc.arg(actor_id)::uuid) AND (actor.admin OR EXISTS(SELECT 1 FROM public.staff_grants grant_row WHERE grant_row.user_id=actor.id AND grant_row.capability='COACH' AND grant_row.revoked_at IS NULL
 AND ((group_row.programme_id IS NOT NULL AND grant_row.programme_id=group_row.programme_id) OR (group_row.team_id IS NOT NULL AND grant_row.team_id=group_row.team_id))))
), header AS (
 SELECT plan.id AS plan_id,plan.group_id,plan.season_id,plan.week_start,plan.updated_at AS source_updated_at,
 latest.id AS latest_publication_id,COALESCE(latest.revision,0)::integer AS latest_revision,latest.supersedes_id AS latest_supersedes_id,latest.source_updated_at AS latest_source_updated_at
 FROM scoped plan LEFT JOIN public.training_plan_publications latest ON latest.id=(SELECT publication.id FROM public.training_plan_publications publication WHERE publication.plan_id=plan.id ORDER BY publication.revision DESC LIMIT 1)
),
candidates AS MATERIALIZED (
 SELECT DISTINCT session.id AS session_id,placement.id AS placement_id,membership.id AS membership_id,structural_subject.id AS subject_id,membership.starts_on,membership.ends_on,
 public.training_session_local_date(session.scheduled_on,session.starts_at) AS session_on
 FROM scoped plan JOIN public.training_group_placements placement ON placement.group_id=plan.group_id
 JOIN public.users structural_subject ON structural_subject.id=placement.subject_user_id
 JOIN public.training_sessions session ON session.plan_id=plan.id
 LEFT JOIN LATERAL public.training_placement_resolve(placement.subject_user_id,plan.group_id,plan.season_id,public.training_session_local_date(session.scheduled_on,session.starts_at)) resolved ON resolved.placement_id=placement.id
 LEFT JOIN public.user_memberships membership ON membership.id=resolved.membership_id
 AND(plan.programme_id IS NULL OR membership.programme_id=plan.programme_id) AND(plan.team_id IS NULL OR membership.team_id=plan.team_id)
 WHERE placement.subject_user_id IS NOT NULL AND placement.superseded_by_change_id IS NULL AND placement.cancelled_by_change_id IS NULL
 AND public.training_session_local_date(session.scheduled_on,session.starts_at) BETWEEN placement.starts_on AND COALESCE(placement.ends_on,'infinity'::date)
 ORDER BY session.id,placement.id,membership.id LIMIT 4097
), candidate_stats AS MATERIALIZED (SELECT count(*)::bigint AS raw_tuple_count FROM candidates), latest_hashes AS MATERIALIZED (
 SELECT prescription.session_id,prescription.placement_id,prescription.membership_id,historical_subject.id AS athlete_user_id,prescription.snapshot_sha256
 FROM header JOIN public.training_prescriptions prescription ON prescription.publication_id=header.latest_publication_id LEFT JOIN public.users historical_subject ON historical_subject.id=prescription.athlete_user_id ORDER BY prescription.session_id,prescription.membership_id LIMIT 4097
), latest_stats AS MATERIALIZED(SELECT count(*)::bigint AS latest_tuple_count FROM latest_hashes), input_gate AS MATERIALIZED (
 SELECT header.plan_id FROM header CROSS JOIN candidate_stats CROSS JOIN latest_stats WHERE candidate_stats.raw_tuple_count<=4096 AND latest_stats.latest_tuple_count<=4096
),
eligible_recipients AS (
 SELECT candidate.placement_id,candidate.membership_id,candidate.subject_id AS athlete_user_id,subject.name AS athlete_name,candidate.starts_on,candidate.ends_on,candidate.session_id
 FROM candidates candidate JOIN input_gate ON true JOIN public.users subject ON subject.id=candidate.subject_id
 WHERE candidate.membership_id IS NOT NULL AND subject.is_active AND subject.erased_at IS NULL AND (NOT subject.is_dependent OR EXISTS(SELECT 1 FROM public.guardian_authority_relationships relationship WHERE relationship.subject_user_id=subject.id AND public.guardian_authority_current(relationship.guardian_user_id,subject.id)))
)
INSERT INTO public.training_prescriptions(publication_id,session_id,membership_id,athlete_user_id,placement_id,snapshot,snapshot_sha256)
 SELECT sqlc.arg(publication_id)::uuid,recipient.session_id,recipient.membership_id,recipient.athlete_user_id,recipient.placement_id,sqlc.arg(snapshot)::jsonb,sqlc.arg(snapshot_sha256)::text FROM eligible_recipients recipient
 WHERE recipient.session_id=sqlc.arg(session_id)::uuid AND recipient.membership_id=sqlc.arg(membership_id)::uuid AND recipient.athlete_user_id=sqlc.arg(athlete_user_id)::uuid AND recipient.placement_id=sqlc.arg(placement_id)::uuid
 RETURNING id,publication_id,session_id,membership_id,athlete_user_id,snapshot,snapshot_sha256,created_at;

-- name: PublicationFinalFacts :one
WITH RECURSIVE actor AS (
 SELECT actor_user.id,EXISTS(SELECT 1 FROM public.user_platform_roles assignment JOIN public.platform_roles role ON role.id=assignment.role_id WHERE assignment.user_id=actor_user.id AND role.code='ADMIN') AS admin
 FROM public.users actor_user WHERE actor_user.id=sqlc.arg(actor_id)::uuid AND actor_user.is_active AND actor_user.erased_at IS NULL AND NOT actor_user.is_dependent
 AND actor_user.date_of_birth<=((now() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date
), scoped AS MATERIALIZED (
 SELECT plan.id,plan.title,plan.description,plan.programme_id,plan.team_id,plan.training_group_id,plan.season_id,plan.week_start,plan.planned_load_percentage,plan.updated_at,
 group_row.id AS group_id,group_row.name AS group_name,group_row.programme_id AS group_programme_id,group_row.team_id AS group_team_id
 FROM public.training_plans plan JOIN public.training_groups group_row ON group_row.id=plan.training_group_id JOIN public.seasons season ON season.id=plan.season_id CROSS JOIN actor
 WHERE plan.id=sqlc.arg(plan_id)::uuid AND (actor.admin OR EXISTS(SELECT 1 FROM public.staff_grants grant_row WHERE grant_row.user_id=actor.id AND grant_row.capability='COACH' AND grant_row.revoked_at IS NULL
 AND ((group_row.programme_id IS NOT NULL AND grant_row.programme_id=group_row.programme_id) OR (group_row.team_id IS NOT NULL AND grant_row.team_id=group_row.team_id))))
), header AS (
 SELECT plan.id AS plan_id,plan.group_id,plan.season_id,plan.week_start,plan.updated_at AS source_updated_at,
 latest.id AS latest_publication_id,COALESCE(latest.revision,0)::integer AS latest_revision,latest.supersedes_id AS latest_supersedes_id,latest.source_updated_at AS latest_source_updated_at
 FROM scoped plan LEFT JOIN public.training_plan_publications latest ON latest.id=(SELECT publication.id FROM public.training_plan_publications publication WHERE publication.plan_id=plan.id ORDER BY publication.revision DESC LIMIT 1)
),
candidates AS MATERIALIZED (
 SELECT DISTINCT session.id AS session_id,placement.id AS placement_id,membership.id AS membership_id,structural_subject.id AS subject_id,membership.starts_on,membership.ends_on,
 public.training_session_local_date(session.scheduled_on,session.starts_at) AS session_on
 FROM scoped plan JOIN public.training_group_placements placement ON placement.group_id=plan.group_id
 JOIN public.users structural_subject ON structural_subject.id=placement.subject_user_id
 JOIN public.training_sessions session ON session.plan_id=plan.id
 LEFT JOIN LATERAL public.training_placement_resolve(placement.subject_user_id,plan.group_id,plan.season_id,public.training_session_local_date(session.scheduled_on,session.starts_at)) resolved ON resolved.placement_id=placement.id
 LEFT JOIN public.user_memberships membership ON membership.id=resolved.membership_id
 AND(plan.programme_id IS NULL OR membership.programme_id=plan.programme_id) AND(plan.team_id IS NULL OR membership.team_id=plan.team_id)
 WHERE placement.subject_user_id IS NOT NULL AND placement.superseded_by_change_id IS NULL AND placement.cancelled_by_change_id IS NULL
 AND public.training_session_local_date(session.scheduled_on,session.starts_at) BETWEEN placement.starts_on AND COALESCE(placement.ends_on,'infinity'::date)
 ORDER BY session.id,placement.id,membership.id LIMIT 4097
), candidate_stats AS MATERIALIZED (SELECT count(*)::bigint AS raw_tuple_count FROM candidates), latest_hashes AS MATERIALIZED (
 SELECT prescription.session_id,prescription.placement_id,prescription.membership_id,historical_subject.id AS athlete_user_id,prescription.snapshot_sha256
 FROM header JOIN public.training_prescriptions prescription ON prescription.publication_id=header.latest_publication_id LEFT JOIN public.users historical_subject ON historical_subject.id=prescription.athlete_user_id ORDER BY prescription.session_id,prescription.membership_id LIMIT 4097
), latest_stats AS MATERIALIZED(SELECT count(*)::bigint AS latest_tuple_count FROM latest_hashes), input_gate AS MATERIALIZED (
 SELECT header.plan_id FROM header CROSS JOIN candidate_stats CROSS JOIN latest_stats WHERE candidate_stats.raw_tuple_count<=4096 AND latest_stats.latest_tuple_count<=4096
),
eligible_recipients AS (
 SELECT candidate.placement_id,candidate.membership_id,candidate.subject_id AS athlete_user_id,subject.name AS athlete_name,candidate.starts_on,candidate.ends_on,candidate.session_id
 FROM candidates candidate JOIN input_gate ON true JOIN public.users subject ON subject.id=candidate.subject_id
 WHERE candidate.membership_id IS NOT NULL AND subject.is_active AND subject.erased_at IS NULL AND (NOT subject.is_dependent OR EXISTS(SELECT 1 FROM public.guardian_authority_relationships relationship WHERE relationship.subject_user_id=subject.id AND public.guardian_authority_current(relationship.guardian_user_id,subject.id)))
), captured AS (SELECT publication.id FROM public.training_plan_publications publication JOIN scoped ON scoped.id=publication.plan_id WHERE publication.id=sqlc.arg(publication_id)::uuid), publication_tuples AS MATERIALIZED (
 SELECT prescription.session_id,prescription.placement_id,prescription.membership_id,historical_subject.id AS athlete_user_id,prescription.snapshot_sha256 FROM captured JOIN public.training_prescriptions prescription ON prescription.publication_id=captured.id LEFT JOIN public.users historical_subject ON historical_subject.id=prescription.athlete_user_id ORDER BY prescription.session_id,prescription.membership_id LIMIT 4097
), final_counts AS MATERIALIZED(SELECT candidate_stats.raw_tuple_count,(SELECT count(*) FROM publication_tuples)::bigint AS publication_tuple_count FROM candidate_stats), final_gate AS MATERIALIZED(SELECT * FROM final_counts WHERE raw_tuple_count<=4096 AND publication_tuple_count<=4096)

SELECT header.plan_id,header.group_id,header.source_updated_at,latest.id AS latest_publication_id,latest.revision AS latest_revision,latest.supersedes_id AS latest_supersedes_id,
 (transaction_timestamp() AT TIME ZONE 'Europe/Lisbon')::date AS transaction_lisbon_date,(clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date AS actual_lisbon_date,clock_timestamp()::timestamptz AS decision_at,
 final_counts.raw_tuple_count,final_counts.publication_tuple_count,COALESCE((final_counts.raw_tuple_count>4096 OR final_counts.publication_tuple_count>4096),false)::boolean AS overflow,
 materialized.recipient_tuples,materialized.publication_tuples
 FROM header CROSS JOIN captured CROSS JOIN final_counts JOIN public.training_plan_publications latest ON latest.id=header.latest_publication_id LEFT JOIN LATERAL (
 SELECT convert_to(COALESCE((SELECT jsonb_agg((pg_catalog.jsonb_build_object('session_id',recipient.session_id,'placement_id',recipient.placement_id,'membership_id',recipient.membership_id,'athlete_user_id',recipient.athlete_user_id)) ORDER BY recipient.session_id,recipient.membership_id) FROM eligible_recipients recipient WHERE final_gate.raw_tuple_count>=0),'[]'::jsonb)::text,'UTF8') AS recipient_tuples,
 convert_to(COALESCE((SELECT jsonb_agg((pg_catalog.jsonb_build_object('session_id',prescription.session_id,'placement_id',prescription.placement_id,'membership_id',prescription.membership_id,'athlete_user_id',prescription.athlete_user_id,'snapshot_sha256',prescription.snapshot_sha256)) ORDER BY prescription.session_id,prescription.membership_id) FROM publication_tuples prescription WHERE final_gate.publication_tuple_count>=0),'[]'::jsonb)::text,'UTF8') AS publication_tuples
 FROM final_gate OFFSET 0) materialized ON true;
