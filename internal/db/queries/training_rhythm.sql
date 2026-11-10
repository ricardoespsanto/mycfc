-- Complete explicit references for canonical #402 publication; bounded sentinel rows.

-- name: ListPublicationRhythmProfiles :many
WITH scoped_plan AS MATERIALIZED (
 SELECT plan.* FROM public.training_plans plan JOIN public.training_groups group_row ON group_row.id=plan.training_group_id
 JOIN public.seasons season ON season.id=plan.season_id
 WHERE plan.id=sqlc.arg(plan_id)::uuid AND public.training_placement_actor_scope(sqlc.arg(actor_id)::uuid,group_row.id)
)
SELECT profile.* FROM public.water_intensity_profiles profile CROSS JOIN scoped_plan WHERE profile.id=ANY(sqlc.arg(profile_ids)::uuid[])
 AND cardinality(sqlc.arg(profile_ids)::uuid[]) BETWEEN 1 AND 32
 AND cardinality(sqlc.arg(profile_ids)::uuid[])=(SELECT count(DISTINCT id) FROM unnest(sqlc.arg(profile_ids)::uuid[]) selected(id)) ORDER BY profile.id LIMIT 33;

-- name: LockPublicationRhythmProfiles :many
WITH scoped_plan AS MATERIALIZED (
 SELECT plan.* FROM public.training_plans plan JOIN public.training_groups group_row ON group_row.id=plan.training_group_id
 JOIN public.seasons season ON season.id=plan.season_id
 WHERE plan.id=sqlc.arg(plan_id)::uuid AND public.training_placement_actor_scope(sqlc.arg(actor_id)::uuid,group_row.id)
)
SELECT profile.* FROM public.water_intensity_profiles profile CROSS JOIN scoped_plan WHERE profile.id=ANY(sqlc.arg(profile_ids)::uuid[])
 AND cardinality(sqlc.arg(profile_ids)::uuid[]) BETWEEN 1 AND 32
 AND cardinality(sqlc.arg(profile_ids)::uuid[])=(SELECT count(DISTINCT id) FROM unnest(sqlc.arg(profile_ids)::uuid[]) selected(id)) ORDER BY profile.id LIMIT 33 FOR SHARE OF profile NOWAIT;

-- name: ListPublicationRhythmZones :many
WITH scoped_plan AS MATERIALIZED (
 SELECT plan.* FROM public.training_plans plan JOIN public.training_groups group_row ON group_row.id=plan.training_group_id
 JOIN public.seasons season ON season.id=plan.season_id
 WHERE plan.id=sqlc.arg(plan_id)::uuid AND public.training_placement_actor_scope(sqlc.arg(actor_id)::uuid,group_row.id)
)
SELECT zone.* FROM public.water_intensity_zones zone CROSS JOIN scoped_plan WHERE zone.profile_id=ANY(sqlc.arg(profile_ids)::uuid[])
 AND cardinality(sqlc.arg(profile_ids)::uuid[]) BETWEEN 1 AND 32
 AND cardinality(sqlc.arg(profile_ids)::uuid[])=(SELECT count(DISTINCT id) FROM unnest(sqlc.arg(profile_ids)::uuid[]) selected(id)) ORDER BY zone.profile_id,zone.position,zone.id LIMIT 257;

-- name: PublicationRecipientCrafts :many
WITH scoped_plan AS MATERIALIZED (
 SELECT plan.* FROM public.training_plans plan JOIN public.training_groups group_row ON group_row.id=plan.training_group_id
 JOIN public.seasons season ON season.id=plan.season_id
 WHERE plan.id=sqlc.arg(plan_id)::uuid AND public.training_placement_actor_scope(sqlc.arg(actor_id)::uuid,group_row.id)
), subjects AS MATERIALIZED (
 SELECT DISTINCT structural_subject.id AS subject_id FROM scoped_plan plan
 JOIN public.training_group_placements placement ON placement.group_id=plan.training_group_id
 JOIN public.users structural_subject ON structural_subject.id=placement.subject_user_id
 JOIN public.training_sessions session ON session.plan_id=plan.id
 WHERE placement.subject_user_id IS NOT NULL AND placement.superseded_by_change_id IS NULL AND placement.cancelled_by_change_id IS NULL
 AND public.training_session_local_date(session.scheduled_on,session.starts_at) BETWEEN placement.starts_on AND COALESCE(placement.ends_on,'infinity'::date)
 ORDER BY structural_subject.id LIMIT 4097
)
SELECT subject.subject_id,
 (EXISTS(SELECT 1 FROM public.person_canoe_craft_classes craft JOIN public.person_sporting_modalities modality ON modality.user_id=craft.user_id AND modality.modality_code='CANOEING' WHERE craft.user_id=subject.subject_id AND craft.craft_code IN('K1','K2','K4')) OR EXISTS(SELECT 1 FROM public.person_sporting_modalities modality WHERE modality.user_id=subject.subject_id AND modality.modality_code='KAYAK_POLO'))::boolean AS kayak,
 EXISTS(SELECT 1 FROM public.person_canoe_craft_classes craft JOIN public.person_sporting_modalities modality ON modality.user_id=craft.user_id AND modality.modality_code='CANOEING' WHERE craft.user_id=subject.subject_id AND craft.craft_code IN('C1','C2','C4'))::boolean AS canoe
FROM subjects subject ORDER BY subject.subject_id;

-- name: LockPublicationRecipientCrafts :many
WITH scoped_plan AS MATERIALIZED (
 SELECT plan.* FROM public.training_plans plan JOIN public.training_groups group_row ON group_row.id=plan.training_group_id
 JOIN public.seasons season ON season.id=plan.season_id
 WHERE plan.id=sqlc.arg(plan_id)::uuid AND public.training_placement_actor_scope(sqlc.arg(actor_id)::uuid,group_row.id)
), subjects AS MATERIALIZED (
 SELECT DISTINCT structural_subject.id AS subject_id FROM scoped_plan plan
 JOIN public.training_group_placements placement ON placement.group_id=plan.training_group_id
 JOIN public.users structural_subject ON structural_subject.id=placement.subject_user_id
 JOIN public.training_sessions session ON session.plan_id=plan.id
 WHERE placement.subject_user_id IS NOT NULL AND placement.superseded_by_change_id IS NULL AND placement.cancelled_by_change_id IS NULL
 AND public.training_session_local_date(session.scheduled_on,session.starts_at) BETWEEN placement.starts_on AND COALESCE(placement.ends_on,'infinity'::date)
 ORDER BY structural_subject.id LIMIT 4097
)
, locked_modalities AS MATERIALIZED (
 SELECT modality.user_id,modality.modality_code FROM public.person_sporting_modalities modality JOIN subjects subject ON subject.subject_id=modality.user_id
 WHERE modality.modality_code IN('CANOEING','KAYAK_POLO') ORDER BY modality.user_id,modality.modality_code FOR SHARE OF modality NOWAIT
),locked_crafts AS MATERIALIZED (
 SELECT craft.user_id,craft.craft_code FROM public.person_canoe_craft_classes craft JOIN subjects subject ON subject.subject_id=craft.user_id
 ORDER BY craft.user_id,craft.craft_code FOR SHARE OF craft NOWAIT
)
SELECT permission.user_id,permission.permission_kind,permission.permission_code FROM (
 SELECT user_id,'MODALITY'::text AS permission_kind,modality_code::text AS permission_code FROM locked_modalities
 UNION ALL SELECT user_id,'CRAFT'::text,craft_code::text FROM locked_crafts
) permission ORDER BY permission.user_id,permission.permission_kind,permission.permission_code LIMIT 32769;
