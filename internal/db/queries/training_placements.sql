-- name: ApplyTrainingPlacementChange :one
SELECT result.change_id::uuid AS change_id,result.head_revision::bigint AS head_revision,(COALESCE(result.source_placement_id::uuid,'00000000-0000-0000-0000-000000000000'::uuid))::uuid AS source_placement_id,(COALESCE(result.destination_placement_id::uuid,'00000000-0000-0000-0000-000000000000'::uuid))::uuid AS destination_placement_id,(COALESCE(result.return_placement_id::uuid,'00000000-0000-0000-0000-000000000000'::uuid))::uuid AS return_placement_id,result.replayed::boolean AS replayed
FROM public.apply_training_placement_change(sqlc.arg(actor_id)::uuid,sqlc.arg(subject_id)::uuid,sqlc.arg(programme_id)::uuid,sqlc.arg(season_id)::uuid,
 sqlc.arg(command_id)::uuid,sqlc.arg(kind)::text,sqlc.narg(source_placement_id)::uuid,sqlc.narg(target_group_id)::uuid,
 sqlc.arg(effective_on)::date,sqlc.narg(temporary_until)::date,sqlc.arg(expected_revision)::bigint,sqlc.arg(review_sha256)::text) AS result(change_id,head_revision,source_placement_id,destination_placement_id,return_placement_id,replayed);

-- name: CancelTrainingPlacementChange :one
SELECT result.change_id::uuid AS change_id,result.head_revision::bigint AS head_revision,(COALESCE(result.source_placement_id::uuid,'00000000-0000-0000-0000-000000000000'::uuid))::uuid AS source_placement_id,(COALESCE(result.destination_placement_id::uuid,'00000000-0000-0000-0000-000000000000'::uuid))::uuid AS destination_placement_id,(COALESCE(result.return_placement_id::uuid,'00000000-0000-0000-0000-000000000000'::uuid))::uuid AS return_placement_id,result.replayed::boolean AS replayed
FROM public.cancel_training_placement_change(sqlc.arg(actor_id)::uuid,sqlc.arg(command_id)::uuid,sqlc.arg(cancellation_id)::uuid,sqlc.arg(expected_revision)::bigint,sqlc.arg(review_sha256)::text) AS result(change_id,head_revision,source_placement_id,destination_placement_id,return_placement_id,replayed);

-- name: TrainingPlacementTimeline :many
SELECT placement.id,placement.subject_user_id,placement.programme_id,placement.group_id,group_row.name AS group_name,
 placement.starts_on,placement.ends_on,placement.change_id,placement.version,placement.superseded_by_change_id,placement.cancelled_by_change_id,
 head.revision,placement.created_at,placement.updated_at
FROM public.training_group_placements placement JOIN public.training_groups group_row ON group_row.id=placement.group_id
JOIN public.users subject ON subject.id=placement.subject_user_id AND subject.is_active AND subject.erased_at IS NULL
JOIN public.training_placement_heads head ON head.subject_user_id=placement.subject_user_id AND head.programme_id=placement.programme_id
WHERE placement.subject_user_id=sqlc.arg(subject_id)::uuid AND placement.programme_id=sqlc.arg(programme_id)::uuid
 AND(NOT subject.is_dependent OR EXISTS(SELECT 1 FROM public.guardian_authority_relationships r WHERE r.subject_user_id=subject.id AND public.guardian_authority_current(r.guardian_user_id,subject.id)))
 AND public.training_placement_actor_scope(sqlc.arg(actor_id)::uuid,placement.group_id)
ORDER BY placement.starts_on,placement.id LIMIT sqlc.arg(row_limit)::integer;

-- name: TrainingPlacementGroupChoices :many
SELECT group_row.id,group_row.name,public.training_placement_group_programme(group_row.id)::uuid AS programme_id,
 group_row.team_id,season.id AS season_id,season.name AS season_name,season.starts_on AS season_starts_on,season.ends_on AS season_ends_on
FROM public.training_groups group_row CROSS JOIN public.seasons season LEFT JOIN public.teams team ON team.id=group_row.team_id
WHERE public.training_placement_group_programme(group_row.id)=sqlc.arg(programme_id)::uuid
 AND season.id=sqlc.arg(season_id)::uuid AND (team.id IS NULL OR team.season_id=season.id)
 AND public.training_placement_actor_scope(sqlc.arg(actor_id)::uuid,group_row.id)
ORDER BY group_row.name,group_row.id LIMIT sqlc.arg(row_limit)::integer;

-- name: LockPublicationPlacementHeads :many
SELECT head.subject_user_id,head.programme_id,head.revision FROM public.training_placement_heads head
JOIN public.lock_training_publication_placement_heads(sqlc.arg(actor_id)::uuid,sqlc.arg(plan_id)::uuid) locked
 ON locked.subject_user_id=head.subject_user_id AND locked.programme_id=head.programme_id
ORDER BY head.subject_user_id,head.programme_id;

-- name: TrainingPlacementSubjectForReview :one
SELECT clock_timestamp()::timestamptz AS decision_at,subject.id AS subject_id,subject.name AS subject_name,group_row.id AS group_id,group_row.name AS group_name,
 public.training_placement_group_programme(group_row.id)::uuid AS programme_id,season.id AS season_id,
 season.starts_on AS season_starts_on,season.ends_on AS season_ends_on,COALESCE(head.revision,0)::bigint AS revision,
 membership.id AS membership_id
FROM public.training_groups group_row JOIN public.users subject ON subject.id=sqlc.arg(subject_id)::uuid
JOIN public.seasons season ON season.id=sqlc.arg(season_id)::uuid
JOIN public.user_memberships membership ON membership.user_id=subject.id AND membership.season_id=season.id
 AND membership.programme_id=public.training_placement_group_programme(group_row.id)
 AND(group_row.team_id IS NULL OR membership.team_id=group_row.team_id)
LEFT JOIN public.teams team ON team.id=group_row.team_id
LEFT JOIN public.training_placement_heads head ON head.subject_user_id=subject.id AND head.programme_id=membership.programme_id
WHERE group_row.id=sqlc.arg(group_id)::uuid AND public.training_placement_actor_scope(sqlc.arg(actor_id)::uuid,group_row.id)
 AND subject.is_active AND subject.erased_at IS NULL AND(NOT subject.is_dependent OR EXISTS(SELECT 1 FROM public.guardian_authority_relationships r WHERE r.subject_user_id=subject.id AND public.guardian_authority_current(r.guardian_user_id,subject.id)))
 AND sqlc.arg(effective_on)::date BETWEEN season.starts_on AND season.ends_on
 AND sqlc.arg(effective_on)::date BETWEEN membership.starts_on AND COALESCE(membership.ends_on,'infinity'::date)
 AND(team.id IS NULL OR team.season_id=season.id);

-- name: TrainingPlacementChangeForReview :one
WITH captured AS MATERIALIZED(SELECT clock_timestamp()::timestamptz AS decision_at)
SELECT captured.decision_at,change_row.id,change_row.subject_user_id,change_row.programme_id,change_row.season_id,
 change_row.kind,change_row.effective_on,change_row.temporary_until,change_row.original_source_starts_on,change_row.original_source_ends_on,change_row.original_source_version,
 change_row.source_placement_id,change_row.destination_placement_id,change_row.return_placement_id,change_row.canonical_command,
 source.group_id AS source_group_id,source.starts_on AS source_starts_on,source.ends_on AS source_ends_on,source.version AS source_version,
 source.superseded_by_change_id AS source_superseded_by_change_id,source.cancelled_by_change_id AS source_cancelled_by_change_id,
 destination.group_id AS destination_group_id,destination.starts_on AS destination_starts_on,destination.ends_on AS destination_ends_on,destination.version AS destination_version,
 destination.superseded_by_change_id AS destination_superseded_by_change_id,destination.cancelled_by_change_id AS destination_cancelled_by_change_id,
 return_leg.group_id AS return_group_id,return_leg.starts_on AS return_starts_on,return_leg.ends_on AS return_ends_on,return_leg.version AS return_version,
 return_leg.superseded_by_change_id AS return_superseded_by_change_id,return_leg.cancelled_by_change_id AS return_cancelled_by_change_id,
 head.revision,change_row.head_revision AS applied_revision,cancelled.id AS cancellation_id,
 COALESCE((cancelled.id IS NULL AND change_row.effective_on>(captured.decision_at AT TIME ZONE 'Europe/Lisbon')::date
 AND(source.id IS NULL OR(source.version=change_row.original_source_version+1 AND source.starts_on=change_row.original_source_starts_on
 AND source.cancelled_by_change_id IS NULL AND((source.superseded_by_change_id=change_row.id) OR(source.superseded_by_change_id IS NULL AND source.ends_on=change_row.effective_on-1))))
 AND(destination.id IS NULL OR(destination.version=1 AND destination.superseded_by_change_id IS NULL AND destination.cancelled_by_change_id IS NULL))
 AND(return_leg.id IS NULL OR(return_leg.version=1 AND return_leg.superseded_by_change_id IS NULL AND return_leg.cancelled_by_change_id IS NULL))),false)::boolean AS can_cancel
FROM public.training_placement_changes change_row
JOIN public.users subject ON subject.id=change_row.subject_user_id AND subject.is_active AND subject.erased_at IS NULL
JOIN public.training_placement_heads head ON head.subject_user_id=subject.id AND head.programme_id=change_row.programme_id
LEFT JOIN public.training_group_placements source ON source.id=change_row.source_placement_id
LEFT JOIN public.training_group_placements destination ON destination.id=change_row.destination_placement_id
LEFT JOIN public.training_group_placements return_leg ON return_leg.id=change_row.return_placement_id
LEFT JOIN public.training_placement_changes cancelled ON cancelled.cancels_change_id=change_row.id
CROSS JOIN captured
WHERE change_row.id=sqlc.arg(change_id)::uuid AND change_row.kind<>'CANCEL'
 AND(NOT subject.is_dependent OR EXISTS(SELECT 1 FROM public.guardian_authority_relationships r WHERE r.subject_user_id=subject.id AND public.guardian_authority_current(r.guardian_user_id,subject.id)))
 AND(source.id IS NULL OR public.training_placement_actor_scope(sqlc.arg(actor_id)::uuid,source.group_id))
 AND(destination.id IS NULL OR public.training_placement_actor_scope(sqlc.arg(actor_id)::uuid,destination.group_id))
 AND(return_leg.id IS NULL OR public.training_placement_actor_scope(sqlc.arg(actor_id)::uuid,return_leg.group_id));

-- name: TrainingPlacementInstructionReview :many
WITH captured AS MATERIALIZED(SELECT clock_timestamp()::timestamptz AS decision_at)
SELECT prescription.id AS prescription_id,prescription.session_id,session.title,session.entry_kind,session.schedule_precision,
 public.training_session_local_date(session.scheduled_on,session.starts_at) AS session_on,session.starts_at,session.ends_at,
 captured.decision_at,
 COALESCE((public.training_session_local_date(session.scheduled_on,session.starts_at)>sqlc.arg(effective_on)::date
 OR(public.training_session_local_date(session.scheduled_on,session.starts_at)=sqlc.arg(effective_on)::date AND(sqlc.arg(effective_on)::date>(captured.decision_at AT TIME ZONE 'Europe/Lisbon')::date OR session.starts_at>captured.decision_at))),false)::boolean AS will_withdraw,
 (sqlc.arg(effective_on)::date=(captured.decision_at AT TIME ZONE 'Europe/Lisbon')::date
 AND public.training_session_local_date(session.scheduled_on,session.starts_at)=sqlc.arg(effective_on)::date
 AND(session.starts_at IS NULL OR session.starts_at<=captured.decision_at))::boolean AS requires_policy
FROM public.training_group_placements placement JOIN public.training_prescriptions prescription ON prescription.placement_id=placement.id
JOIN public.users subject ON subject.id=placement.subject_user_id AND subject.is_active AND subject.erased_at IS NULL
JOIN public.training_sessions session ON session.id=prescription.session_id CROSS JOIN captured
WHERE placement.id=sqlc.arg(source_placement_id)::uuid AND placement.subject_user_id IS NOT NULL
 AND(NOT subject.is_dependent OR EXISTS(SELECT 1 FROM public.guardian_authority_relationships r WHERE r.subject_user_id=subject.id AND public.guardian_authority_current(r.guardian_user_id,subject.id)))
 AND public.training_placement_actor_scope(sqlc.arg(actor_id)::uuid,placement.group_id)
ORDER BY prescription.id LIMIT 4097;

-- name: TrainingPlacementReturnForReview :many
SELECT membership.id AS membership_id,membership.season_id,season.starts_on AS season_starts_on,season.ends_on AS season_ends_on,
 membership.starts_on,membership.ends_on
FROM public.training_groups group_row JOIN public.user_memberships membership ON membership.user_id=sqlc.arg(subject_id)::uuid
 AND membership.programme_id=public.training_placement_group_programme(group_row.id)
JOIN public.seasons season ON season.id=membership.season_id AND sqlc.arg(return_on)::date BETWEEN season.starts_on AND season.ends_on
LEFT JOIN public.teams team ON team.id=group_row.team_id
JOIN public.users subject ON subject.id=membership.user_id AND subject.is_active AND subject.erased_at IS NULL
WHERE group_row.id=sqlc.arg(group_id)::uuid AND public.training_placement_actor_scope(sqlc.arg(actor_id)::uuid,group_row.id)
 AND(group_row.team_id IS NULL OR membership.team_id=group_row.team_id) AND(team.id IS NULL OR team.season_id=membership.season_id)
 AND sqlc.arg(return_on)::date BETWEEN membership.starts_on AND COALESCE(membership.ends_on,'infinity'::date)
 AND(NOT subject.is_dependent OR EXISTS(SELECT 1 FROM public.guardian_authority_relationships r WHERE r.subject_user_id=subject.id AND public.guardian_authority_current(r.guardian_user_id,subject.id)))
ORDER BY membership.id LIMIT 2;

-- name: TrainingPlacementSourceForReview :one
SELECT clock_timestamp()::timestamptz AS decision_at,placement.id,subject.id AS subject_id,subject.name AS subject_name,
 placement.programme_id,placement.group_id,group_row.name AS group_name,placement.starts_on,placement.ends_on,placement.version,
 placement.superseded_by_change_id,placement.cancelled_by_change_id,placement.change_id,creation.season_id AS creation_season_id,head.revision
FROM public.training_group_placements placement JOIN public.training_groups group_row ON group_row.id=placement.group_id
JOIN public.users subject ON subject.id=placement.subject_user_id AND subject.is_active AND subject.erased_at IS NULL
JOIN public.training_placement_heads head ON head.subject_user_id=subject.id AND head.programme_id=placement.programme_id
JOIN public.training_placement_changes creation ON creation.id=placement.change_id
WHERE placement.id=sqlc.arg(source_placement_id)::uuid AND public.training_placement_actor_scope(sqlc.arg(actor_id)::uuid,placement.group_id)
 AND(NOT subject.is_dependent OR EXISTS(SELECT 1 FROM public.guardian_authority_relationships r WHERE r.subject_user_id=subject.id AND public.guardian_authority_current(r.guardian_user_id,subject.id)));


-- name: TrainingGroupAthleteChoicesForDate :many
WITH eligible AS MATERIALIZED (
 SELECT DISTINCT ON (subject.id) membership.id AS membership_id,subject.id AS user_id,subject.name,
 EXISTS(SELECT 1 FROM public.training_group_placements placement WHERE placement.subject_user_id=subject.id AND placement.group_id=group_row.id
 AND placement.superseded_by_change_id IS NULL AND placement.cancelled_by_change_id IS NULL
 AND sqlc.arg(effective_on)::date BETWEEN placement.starts_on AND COALESCE(placement.ends_on,'infinity'::date)) AS already_member,
 EXISTS(SELECT 1 FROM public.training_group_placements conflicting WHERE conflicting.subject_user_id=subject.id AND conflicting.programme_id=membership.programme_id
 AND conflicting.superseded_by_change_id IS NULL AND conflicting.cancelled_by_change_id IS NULL AND(conflicting.ends_on IS NULL OR conflicting.ends_on>=sqlc.arg(effective_on)::date)) AS has_conflicting_placement
 FROM public.training_groups group_row JOIN public.seasons season ON season.id=sqlc.arg(season_id)::uuid
 JOIN public.user_memberships membership ON membership.season_id=season.id AND membership.programme_id=public.training_placement_group_programme(group_row.id)
 JOIN public.users subject ON subject.id=membership.user_id
 LEFT JOIN public.teams team ON team.id=group_row.team_id
 WHERE group_row.id=sqlc.arg(group_id)::uuid AND public.training_placement_actor_scope(sqlc.arg(actor_id)::uuid,group_row.id)
 AND subject.is_active AND subject.erased_at IS NULL
 AND(NOT subject.is_dependent OR EXISTS(SELECT 1 FROM public.guardian_authority_relationships relationship WHERE relationship.subject_user_id=subject.id AND public.guardian_authority_current(relationship.guardian_user_id,subject.id)))
 AND(group_row.team_id IS NULL OR(membership.team_id=group_row.team_id AND team.season_id=season.id))
 AND sqlc.arg(effective_on)::date BETWEEN season.starts_on AND season.ends_on
 AND sqlc.arg(effective_on)::date BETWEEN membership.starts_on AND COALESCE(membership.ends_on,'infinity'::date)
 AND char_length(btrim(sqlc.arg(name_query)::text)) BETWEEN 1 AND 120
 AND subject.name ILIKE '%'||replace(replace(replace(sqlc.arg(name_query)::text,E'\\',E'\\\\'),'%','\%'),'_','\_')||'%' ESCAPE E'\\'
 ORDER BY subject.id,membership.id
)
SELECT membership_id,user_id,name,already_member,(NOT already_member AND has_conflicting_placement)::boolean AS unavailable FROM eligible ORDER BY name,user_id,membership_id LIMIT 20 OFFSET sqlc.arg(row_offset)::integer;

-- name: TrainingPlacementApplyReplay :one
SELECT change_row.id AS change_id,change_row.head_revision,
 (COALESCE(change_row.source_placement_id,'00000000-0000-0000-0000-000000000000'::uuid))::uuid AS source_placement_id,
 (COALESCE(change_row.destination_placement_id,'00000000-0000-0000-0000-000000000000'::uuid))::uuid AS destination_placement_id,
 (COALESCE(change_row.return_placement_id,'00000000-0000-0000-0000-000000000000'::uuid))::uuid AS return_placement_id,true::boolean AS replayed
FROM public.training_placement_changes change_row JOIN public.users subject ON subject.id=change_row.subject_user_id
LEFT JOIN public.training_group_placements source ON source.id=change_row.source_placement_id
LEFT JOIN public.training_group_placements destination ON destination.id=change_row.destination_placement_id
WHERE change_row.id=sqlc.arg(command_id)::uuid AND change_row.subject_user_id=sqlc.arg(subject_id)::uuid AND change_row.actor_user_id=sqlc.arg(actor_id)::uuid
 AND change_row.programme_id=sqlc.arg(programme_id)::uuid AND change_row.season_id=sqlc.arg(season_id)::uuid AND change_row.kind=sqlc.arg(kind)::text
 AND change_row.source_placement_id IS NOT DISTINCT FROM sqlc.narg(source_placement_id)::uuid
 AND destination.group_id IS NOT DISTINCT FROM sqlc.narg(target_group_id)::uuid
 AND change_row.canonical_command=jsonb_build_object('kind',sqlc.arg(kind)::text,'programme',sqlc.arg(programme_id)::uuid,'season',sqlc.arg(season_id)::uuid,
 'source',sqlc.narg(source_placement_id)::uuid,'target',sqlc.narg(target_group_id)::uuid,'effective_on',sqlc.arg(effective_on)::date,'temporary_until',sqlc.narg(temporary_until)::date,'expected_revision',sqlc.arg(expected_revision)::bigint,'review_sha256',sqlc.arg(review_sha256)::text)
 AND subject.is_active AND subject.erased_at IS NULL AND(NOT subject.is_dependent OR EXISTS(SELECT 1 FROM public.guardian_authority_relationships r WHERE r.subject_user_id=subject.id AND public.guardian_authority_current(r.guardian_user_id,subject.id)))
 AND(source.id IS NULL OR public.training_placement_actor_scope(sqlc.arg(actor_id)::uuid,source.group_id))
 AND(destination.id IS NULL OR public.training_placement_actor_scope(sqlc.arg(actor_id)::uuid,destination.group_id));

-- name: TrainingPlacementCancelReplay :one
SELECT cancellation.id AS change_id,cancellation.head_revision,
 (COALESCE(cancellation.source_placement_id,'00000000-0000-0000-0000-000000000000'::uuid))::uuid AS source_placement_id,
 (COALESCE(cancellation.destination_placement_id,'00000000-0000-0000-0000-000000000000'::uuid))::uuid AS destination_placement_id,
 (COALESCE(cancellation.return_placement_id,'00000000-0000-0000-0000-000000000000'::uuid))::uuid AS return_placement_id,true::boolean AS replayed
FROM public.training_placement_changes cancellation JOIN public.training_placement_changes original ON original.id=cancellation.cancels_change_id
JOIN public.users subject ON subject.id=cancellation.subject_user_id
LEFT JOIN public.training_group_placements source ON source.id=original.source_placement_id
LEFT JOIN public.training_group_placements destination ON destination.id=original.destination_placement_id
LEFT JOIN public.training_group_placements return_leg ON return_leg.id=original.return_placement_id
WHERE cancellation.id=sqlc.arg(cancellation_id)::uuid AND cancellation.cancels_change_id=sqlc.arg(command_id)::uuid AND cancellation.kind='CANCEL'
 AND cancellation.actor_user_id=sqlc.arg(actor_id)::uuid AND cancellation.subject_user_id=original.subject_user_id
 AND cancellation.programme_id=original.programme_id AND cancellation.season_id=original.season_id
 AND cancellation.canonical_command=jsonb_build_object('cancel',sqlc.arg(command_id)::uuid,'expected_revision',sqlc.arg(expected_revision)::bigint,'review_sha256',sqlc.arg(review_sha256)::text)
 AND subject.is_active AND subject.erased_at IS NULL AND(NOT subject.is_dependent OR EXISTS(SELECT 1 FROM public.guardian_authority_relationships r WHERE r.subject_user_id=subject.id AND public.guardian_authority_current(r.guardian_user_id,subject.id)))
 AND(source.id IS NULL OR public.training_placement_actor_scope(sqlc.arg(actor_id)::uuid,source.group_id))
 AND(destination.id IS NULL OR public.training_placement_actor_scope(sqlc.arg(actor_id)::uuid,destination.group_id))
 AND(return_leg.id IS NULL OR public.training_placement_actor_scope(sqlc.arg(actor_id)::uuid,return_leg.group_id));

-- name: TrainingGroupPlacementRosterForReview :many
WITH captured AS MATERIALIZED(SELECT clock_timestamp()::timestamptz AS decision_at),anchors AS MATERIALIZED (
 SELECT DISTINCT ON (placement.subject_user_id) placement.*,captured.decision_at,
 CASE WHEN placement.starts_on<=(captured.decision_at AT TIME ZONE 'Europe/Lisbon')::date THEN (captured.decision_at AT TIME ZONE 'Europe/Lisbon')::date ELSE placement.starts_on END::date AS display_on,
 (placement.starts_on<=(captured.decision_at AT TIME ZONE 'Europe/Lisbon')::date)::boolean AS is_current
 FROM public.training_groups group_row JOIN public.training_group_placements placement ON placement.group_id=group_row.id
 JOIN public.users subject ON subject.id=placement.subject_user_id CROSS JOIN captured
 WHERE group_row.id=sqlc.arg(group_id)::uuid AND public.training_placement_actor_scope(sqlc.arg(actor_id)::uuid,group_row.id)
 AND placement.superseded_by_change_id IS NULL AND placement.cancelled_by_change_id IS NULL
 AND(placement.ends_on IS NULL OR placement.ends_on>=(captured.decision_at AT TIME ZONE 'Europe/Lisbon')::date)
 AND subject.is_active AND subject.erased_at IS NULL AND(NOT subject.is_dependent OR EXISTS(SELECT 1 FROM public.guardian_authority_relationships r WHERE r.subject_user_id=subject.id AND public.guardian_authority_current(r.guardian_user_id,subject.id)))
 ORDER BY placement.subject_user_id,(placement.starts_on<=(captured.decision_at AT TIME ZONE 'Europe/Lisbon')::date) DESC,placement.starts_on,placement.id
)
SELECT anchor.id AS placement_id,subject.id AS user_id,subject.name,anchor.group_id,anchor.programme_id,anchor.starts_on,anchor.ends_on,anchor.version,head.revision,
 anchor.decision_at,anchor.display_on,anchor.is_current,
 EXISTS(SELECT 1 FROM public.training_group_placements scheduled WHERE scheduled.subject_user_id=subject.id AND scheduled.group_id=anchor.group_id
 AND scheduled.superseded_by_change_id IS NULL AND scheduled.cancelled_by_change_id IS NULL AND scheduled.starts_on>(anchor.decision_at AT TIME ZONE 'Europe/Lisbon')::date) AS has_scheduled,
 membership.id AS membership_id,membership.season_id,
 CASE WHEN resolution.match_count=1 THEN 'AVAILABLE' WHEN resolution.match_count=0 THEN 'UNAVAILABLE' ELSE 'AMBIGUOUS' END::text AS availability
FROM anchors anchor JOIN public.users subject ON subject.id=anchor.subject_user_id
JOIN public.training_groups group_row ON group_row.id=anchor.group_id
JOIN public.training_placement_heads head ON head.subject_user_id=subject.id AND head.programme_id=anchor.programme_id
LEFT JOIN public.teams team ON team.id=group_row.team_id
CROSS JOIN LATERAL (
 SELECT count(*)::bigint AS match_count,min(candidate.id::text)::uuid AS single_membership_id FROM (
 SELECT actual.id FROM public.user_memberships actual JOIN public.seasons season ON season.id=actual.season_id
 WHERE actual.user_id=subject.id AND actual.programme_id=anchor.programme_id
 AND(group_row.team_id IS NULL OR(actual.team_id=group_row.team_id AND season.id=team.season_id))
 AND anchor.display_on BETWEEN actual.starts_on AND COALESCE(actual.ends_on,'infinity'::date)
 AND anchor.display_on BETWEEN season.starts_on AND season.ends_on ORDER BY actual.id LIMIT 2
 ) candidate
) resolution
LEFT JOIN public.user_memberships membership ON resolution.match_count=1 AND membership.id=resolution.single_membership_id
ORDER BY subject.name,subject.id,anchor.id LIMIT 20 OFFSET sqlc.arg(row_offset)::integer;


-- name: LockTrainingPlacementExistingHeadsForJoin :many
SELECT head.subject_user_id,head.programme_id,head.revision FROM public.training_placement_heads head
JOIN public.lock_training_placement_existing_heads_for_join(sqlc.arg(actor_id)::uuid,sqlc.arg(group_id)::uuid,sqlc.arg(subject_ids)::uuid[]) locked
 ON locked.subject_user_id=head.subject_user_id AND locked.programme_id=head.programme_id
ORDER BY head.subject_user_id,head.programme_id;
