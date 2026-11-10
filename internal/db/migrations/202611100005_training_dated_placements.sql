-- Fresh dated-placement definitions. No existing records are converted/backfilled.
CREATE TABLE public.training_placement_heads (
 subject_user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE RESTRICT,
 programme_id uuid NOT NULL REFERENCES public.programmes(id) ON DELETE RESTRICT,
 revision bigint NOT NULL DEFAULT 0 CHECK(revision>=0),
 PRIMARY KEY(subject_user_id,programme_id)
);
CREATE TABLE public.training_placement_changes (
 id uuid PRIMARY KEY,
 subject_user_id uuid NULL REFERENCES public.users(id) ON DELETE RESTRICT,
 subject_principal_id uuid NULL REFERENCES public.privacy_pseudonymous_principals(id) ON DELETE RESTRICT,
 programme_id uuid NOT NULL REFERENCES public.programmes(id) ON DELETE RESTRICT,
 season_id uuid NOT NULL REFERENCES public.seasons(id) ON DELETE RESTRICT,
 kind text NOT NULL CHECK(kind IN ('JOIN','LEAVE','MOVE','TEMPORARY','CANCEL')),
 source_placement_id uuid NULL,
 destination_placement_id uuid NULL,
 return_placement_id uuid NULL,
 effective_on date NOT NULL,
 temporary_until date NULL,
 original_source_starts_on date NULL,
 original_source_version bigint NULL,
 original_source_ends_on date NULL,
 cancels_change_id uuid NULL UNIQUE REFERENCES public.training_placement_changes(id) ON DELETE RESTRICT,
 actor_user_id uuid NULL REFERENCES public.users(id) ON DELETE RESTRICT,
 actor_principal_id uuid NULL REFERENCES public.privacy_pseudonymous_principals(id) ON DELETE RESTRICT,
 canonical_command jsonb NOT NULL CHECK(jsonb_typeof(canonical_command)='object' AND octet_length(canonical_command::text)<=4096),
 head_revision bigint NOT NULL CHECK(head_revision>0),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CONSTRAINT training_placement_changes_subject CHECK(num_nonnulls(subject_user_id,subject_principal_id)=1),
 CONSTRAINT training_placement_changes_actor CHECK(num_nonnulls(actor_user_id,actor_principal_id)=1),
 CONSTRAINT training_placement_changes_temporary CHECK((kind='TEMPORARY' AND temporary_until>=effective_on AND return_placement_id IS NOT NULL) OR (kind<>'TEMPORARY' AND temporary_until IS NULL AND return_placement_id IS NULL)),
 CONSTRAINT training_placement_changes_cancel CHECK((kind='CANCEL')=(cancels_change_id IS NOT NULL)),
 FOREIGN KEY(subject_user_id,programme_id) REFERENCES public.training_placement_heads(subject_user_id,programme_id) ON DELETE RESTRICT
);
CREATE INDEX training_placement_changes_subject_idx ON public.training_placement_changes(subject_user_id,programme_id,recorded_at,id);
CREATE TABLE public.training_group_placements (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 subject_user_id uuid NULL REFERENCES public.users(id) ON DELETE RESTRICT,
 subject_principal_id uuid NULL REFERENCES public.privacy_pseudonymous_principals(id) ON DELETE RESTRICT,
 programme_id uuid NOT NULL REFERENCES public.programmes(id) ON DELETE RESTRICT,
 group_id uuid NOT NULL REFERENCES public.training_groups(id) ON DELETE RESTRICT,
 starts_on date NOT NULL,
 ends_on date NULL,
 change_id uuid NOT NULL REFERENCES public.training_placement_changes(id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 superseded_by_change_id uuid NULL REFERENCES public.training_placement_changes(id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
 cancelled_by_change_id uuid NULL REFERENCES public.training_placement_changes(id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CONSTRAINT training_group_placements_subject CHECK(num_nonnulls(subject_user_id,subject_principal_id)=1),
 CONSTRAINT training_group_placements_dates CHECK(ends_on IS NULL OR starts_on<=ends_on),
 FOREIGN KEY(subject_user_id,programme_id) REFERENCES public.training_placement_heads(subject_user_id,programme_id) ON DELETE RESTRICT,
 EXCLUDE USING gist(subject_user_id WITH =,programme_id WITH =,daterange(starts_on,ends_on,'[]') WITH &&)
 WHERE(subject_user_id IS NOT NULL AND superseded_by_change_id IS NULL AND cancelled_by_change_id IS NULL)
);
CREATE INDEX training_group_placements_group_dates_idx ON public.training_group_placements(group_id,starts_on,ends_on);
ALTER TABLE public.training_placement_changes ADD CONSTRAINT training_placement_changes_source_fk FOREIGN KEY(source_placement_id) REFERENCES public.training_group_placements(id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE public.training_placement_changes ADD CONSTRAINT training_placement_changes_destination_fk FOREIGN KEY(destination_placement_id) REFERENCES public.training_group_placements(id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE public.training_placement_changes ADD CONSTRAINT training_placement_changes_return_fk FOREIGN KEY(return_placement_id) REFERENCES public.training_group_placements(id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE public.training_prescriptions ADD COLUMN placement_id uuid NULL REFERENCES public.training_group_placements(id) ON DELETE RESTRICT;
CREATE TABLE public.training_prescription_withdrawals (
 prescription_id uuid NOT NULL,
 live_prescription_id uuid NULL REFERENCES public.training_prescriptions(id) ON DELETE SET NULL,
 change_id uuid NOT NULL REFERENCES public.training_placement_changes(id) ON DELETE RESTRICT,
 leg text NOT NULL CHECK(leg IN ('OUTBOUND','RETURN')),
 effective_on date NOT NULL,
 observed_session_on date NOT NULL,
 observed_starts_at timestamptz NULL,
 observed_precision text NOT NULL CHECK(observed_precision IN ('INTERVAL','START_ONLY','DAY','BAND')),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(prescription_id,change_id,leg)
);

CREATE FUNCTION public.training_placement_group_programme(p_group_id uuid) RETURNS uuid
LANGUAGE sql STABLE SET search_path=pg_catalog,public AS $$
 SELECT COALESCE(g.programme_id,t.programme_id) FROM public.training_groups g LEFT JOIN public.teams t ON t.id=g.team_id WHERE g.id=p_group_id
$$;
CREATE FUNCTION public.training_placement_actor_scope(p_actor uuid,p_group uuid) RETURNS boolean
LANGUAGE sql STABLE SET search_path=pg_catalog,public AS $$
 SELECT EXISTS(SELECT 1 FROM public.users u JOIN public.training_groups g ON g.id=p_group
 WHERE u.id=p_actor AND u.is_active AND u.erased_at IS NULL AND NOT u.is_dependent
 AND u.date_of_birth<=((clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date
 AND (EXISTS(SELECT 1 FROM public.user_platform_roles r JOIN public.platform_roles role ON role.id=r.role_id WHERE r.user_id=u.id AND role.code='ADMIN')
 OR EXISTS(SELECT 1 FROM public.staff_grants grant_row WHERE grant_row.user_id=u.id AND grant_row.capability='COACH' AND grant_row.revoked_at IS NULL
 AND ((g.programme_id IS NOT NULL AND grant_row.programme_id=g.programme_id) OR(g.team_id IS NOT NULL AND grant_row.team_id=g.team_id)))))
$$;
CREATE FUNCTION public.training_placement_resolve(p_subject uuid,p_group uuid,p_season uuid,p_on date)
RETURNS TABLE(placement_id uuid,membership_id uuid,programme_id uuid)
LANGUAGE sql STABLE SET search_path=pg_catalog,public AS $$
 SELECT placement.id,membership.id,placement.programme_id
 FROM public.training_group_placements placement JOIN public.training_groups group_row ON group_row.id=placement.group_id
 JOIN public.seasons season ON season.id=p_season AND p_on BETWEEN season.starts_on AND season.ends_on
 LEFT JOIN public.teams team ON team.id=group_row.team_id
 JOIN public.user_memberships membership ON membership.user_id=placement.subject_user_id AND membership.programme_id=placement.programme_id
 AND membership.season_id=season.id AND (group_row.team_id IS NULL OR membership.team_id=group_row.team_id)
 AND p_on BETWEEN membership.starts_on AND COALESCE(membership.ends_on,'infinity'::date)
 WHERE placement.subject_user_id=p_subject AND placement.group_id=p_group
 AND placement.superseded_by_change_id IS NULL AND placement.cancelled_by_change_id IS NULL
 AND p_on BETWEEN placement.starts_on AND COALESCE(placement.ends_on,'infinity'::date)
 AND (team.id IS NULL OR team.season_id=p_season)
$$;
CREATE FUNCTION public.training_placement_privileged_operation(p_operation text,p_subject uuid,p_principal uuid DEFAULT NULL) RETURNS boolean
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
 RETURN EXISTS(SELECT 1 FROM privacy_protected.training_placement_erasure_fence fence
 WHERE fence.transaction_id=txid_current() AND fence.subject_user_id=p_subject AND fence.operation=p_operation
 AND(p_principal IS NULL OR fence.principal_id=p_principal));
END $$;
CREATE FUNCTION public.guard_training_placement_audit() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog,public AS $$
BEGIN
 IF TG_OP='UPDATE' AND public.training_placement_privileged_operation('MEMBERSHIP_HISTORY_ANONYMIZE',OLD.subject_user_id,NEW.subject_principal_id)
 AND NEW.subject_user_id IS NULL AND NEW.subject_principal_id IS NOT NULL
 AND (to_jsonb(NEW)-'subject_user_id'-'subject_principal_id')=(to_jsonb(OLD)-'subject_user_id'-'subject_principal_id') THEN RETURN NEW; END IF;
 IF TG_OP='UPDATE' AND public.training_placement_privileged_operation('AUDIT_ACTOR_ANONYMIZE',OLD.actor_user_id,NEW.actor_principal_id)
 AND NEW.actor_user_id IS NULL AND NEW.actor_principal_id IS NOT NULL
 AND (to_jsonb(NEW)-'actor_user_id'-'actor_principal_id')=(to_jsonb(OLD)-'actor_user_id'-'actor_principal_id') THEN RETURN NEW; END IF;
 RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='training_placement_audit_immutable';
END $$;
CREATE TRIGGER training_placement_changes_immutable BEFORE UPDATE OR DELETE ON public.training_placement_changes FOR EACH ROW EXECUTE FUNCTION public.guard_training_placement_audit();
CREATE FUNCTION public.guard_training_prescription_withdrawal_immutable() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog,public AS $$
BEGIN
 IF TG_OP='UPDATE' AND EXISTS(SELECT 1 FROM public.training_prescriptions p WHERE p.id=OLD.live_prescription_id AND public.training_placement_privileged_operation('TRAINING_PRESCRIPTION_DELETE',p.athlete_user_id))
 AND NEW.live_prescription_id IS NULL AND OLD.live_prescription_id=OLD.prescription_id
 AND (to_jsonb(NEW)-'live_prescription_id')=(to_jsonb(OLD)-'live_prescription_id') THEN RETURN NEW; END IF;
 RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='training_prescription_withdrawal_immutable';
END $$;
CREATE TRIGGER training_prescription_withdrawals_immutable BEFORE UPDATE OR DELETE ON public.training_prescription_withdrawals FOR EACH ROW EXECUTE FUNCTION public.guard_training_prescription_withdrawal_immutable();
CREATE FUNCTION public.guard_training_group_placement() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog,public AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='training_placement_history_immutable'; END IF;
 IF TG_OP='UPDATE' AND public.training_placement_privileged_operation('MEMBERSHIP_HISTORY_ANONYMIZE',OLD.subject_user_id,NEW.subject_principal_id)
 AND NEW.subject_user_id IS NULL AND NEW.subject_principal_id IS NOT NULL
 AND (to_jsonb(NEW)-'subject_user_id'-'subject_principal_id')=(to_jsonb(OLD)-'subject_user_id'-'subject_principal_id') THEN RETURN NEW; END IF;
 IF NEW.subject_user_id IS NULL OR NEW.subject_principal_id IS NOT NULL
 OR NEW.programme_id IS DISTINCT FROM public.training_placement_group_programme(NEW.group_id) THEN
 RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='training_placement_scope_invalid'; END IF;
 IF TG_OP='UPDATE' AND (NEW.id IS DISTINCT FROM OLD.id OR NEW.subject_user_id IS DISTINCT FROM OLD.subject_user_id
 OR NEW.programme_id IS DISTINCT FROM OLD.programme_id OR NEW.group_id IS DISTINCT FROM OLD.group_id
 OR NEW.starts_on IS DISTINCT FROM OLD.starts_on OR NEW.change_id IS DISTINCT FROM OLD.change_id OR NEW.created_at IS DISTINCT FROM OLD.created_at
 OR NEW.version<>OLD.version+1) THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='training_placement_identity_immutable'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER training_group_placements_guard BEFORE INSERT OR UPDATE OR DELETE ON public.training_group_placements FOR EACH ROW EXECUTE FUNCTION public.guard_training_group_placement();
CREATE TRIGGER training_placement_heads_active_subject_attachment BEFORE INSERT OR UPDATE OF subject_user_id ON public.training_placement_heads FOR EACH ROW EXECUTE FUNCTION public.require_active_privacy_attachment_subject('subject_user_id');
CREATE TRIGGER training_group_placements_active_subject_attachment BEFORE INSERT OR UPDATE OF subject_user_id ON public.training_group_placements FOR EACH ROW EXECUTE FUNCTION public.require_active_privacy_attachment_subject('subject_user_id');
CREATE TRIGGER training_placement_changes_active_subject_attachment BEFORE INSERT OR UPDATE OF subject_user_id ON public.training_placement_changes FOR EACH ROW EXECUTE FUNCTION public.require_active_privacy_attachment_subject('subject_user_id');
CREATE FUNCTION public.guard_training_placement_group_scope() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog,public AS $$
BEGIN
 IF (NEW.programme_id IS DISTINCT FROM OLD.programme_id OR NEW.team_id IS DISTINCT FROM OLD.team_id)
 AND EXISTS(SELECT 1 FROM public.training_group_placements p WHERE p.group_id=OLD.id) THEN
 RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='training_placement_group_scope_immutable'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER training_groups_placement_scope_guard BEFORE UPDATE OF programme_id,team_id ON public.training_groups FOR EACH ROW EXECUTE FUNCTION public.guard_training_placement_group_scope();

CREATE FUNCTION public.training_placement_touch_heads(p_subject uuid,p_programmes uuid[]) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE programme uuid;
BEGIN
 IF p_subject IS NULL THEN RETURN; END IF;
 IF EXISTS(SELECT 1 FROM privacy_protected.training_placement_erasure_fence fence WHERE fence.transaction_id=txid_current() AND fence.subject_user_id=p_subject AND fence.operation IN('MEMBERSHIP_HISTORY_ANONYMIZE','MEMBERSHIP_ACTIVE_REVOKE')) THEN RETURN; END IF;
 FOR programme IN SELECT DISTINCT id FROM unnest(p_programmes) id WHERE id IS NOT NULL ORDER BY id LOOP
 INSERT INTO public.training_placement_heads(subject_user_id,programme_id,revision) VALUES(p_subject,programme,1)
 ON CONFLICT(subject_user_id,programme_id) DO UPDATE SET revision=training_placement_heads.revision+1;
 END LOOP;
END $$;
CREATE FUNCTION public.touch_training_placement_candidates() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE old_subject uuid;new_subject uuid;old_programme uuid;new_programme uuid;plan_ref uuid;plan_count bigint;
BEGIN
 IF TG_OP<>'INSERT' THEN old_programme:=OLD.programme_id; END IF;
 IF TG_OP<>'DELETE' THEN new_programme:=NEW.programme_id; END IF;
 IF TG_TABLE_NAME='user_memberships' THEN
 IF TG_OP<>'INSERT' THEN old_subject:=OLD.user_id; END IF;
 IF TG_OP<>'DELETE' THEN new_subject:=NEW.user_id; END IF;
 ELSE
 IF TG_OP<>'INSERT' THEN old_subject:=OLD.subject_user_id; END IF;
 IF TG_OP<>'DELETE' THEN new_subject:=NEW.subject_user_id; END IF;
 END IF;
 IF old_subject IS NOT NULL THEN PERFORM public.training_placement_touch_heads(old_subject,ARRAY[old_programme]); END IF;
 IF new_subject IS NOT NULL AND (new_subject IS DISTINCT FROM old_subject OR new_programme IS DISTINCT FROM old_programme OR TG_OP='UPDATE') THEN
 PERFORM public.training_placement_touch_heads(new_subject,ARRAY[new_programme]); END IF;
 SELECT count(*) INTO plan_count FROM public.training_plans p LEFT JOIN public.training_groups g ON g.id=p.training_group_id
 LEFT JOIN public.teams t ON t.id=COALESCE(g.team_id,p.team_id)
 WHERE COALESCE(g.programme_id,p.programme_id,t.programme_id)=ANY(ARRAY[old_programme,new_programme]);
 IF plan_count>4096 THEN RAISE EXCEPTION USING ERRCODE='54000',MESSAGE='training_placement_affected_plan_limit'; END IF;
 FOR plan_ref IN SELECT p.id FROM public.training_plans p LEFT JOIN public.training_groups g ON g.id=p.training_group_id
 LEFT JOIN public.teams t ON t.id=COALESCE(g.team_id,p.team_id)
 WHERE COALESCE(g.programme_id,p.programme_id,t.programme_id)=ANY(ARRAY[old_programme,new_programme]) ORDER BY p.id LOOP
 UPDATE public.training_plans SET updated_at=clock_timestamp() WHERE id=plan_ref;
 END LOOP;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;RETURN NEW;
END $$;
CREATE TRIGGER training_group_placements_touch_candidates AFTER INSERT OR UPDATE OR DELETE ON public.training_group_placements FOR EACH ROW EXECUTE FUNCTION public.touch_training_placement_candidates();
CREATE TRIGGER user_memberships_touch_placement_candidates AFTER INSERT OR UPDATE OR DELETE ON public.user_memberships FOR EACH ROW EXECUTE FUNCTION public.touch_training_placement_candidates();

CREATE FUNCTION public.training_placement_lock_actor_subject(p_actor uuid,p_subject uuid) RETURNS void
LANGUAGE plpgsql SET search_path=pg_catalog,public AS $$
BEGIN
 IF p_actor=p_subject THEN
 PERFORM 1 FROM public.users u WHERE u.id=p_actor AND u.is_active AND u.erased_at IS NULL AND NOT u.is_dependent
 AND u.date_of_birth<=((clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date FOR UPDATE NOWAIT;
 ELSE
 PERFORM 1 FROM public.users u WHERE u.id=p_actor AND u.is_active AND u.erased_at IS NULL AND NOT u.is_dependent
 AND u.date_of_birth<=((clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date FOR SHARE NOWAIT;
 END IF;
 IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='training_placement_forbidden'; END IF;
 PERFORM 1 FROM public.user_platform_roles r WHERE r.user_id=p_actor ORDER BY r.role_id FOR SHARE NOWAIT;
 PERFORM 1 FROM public.staff_grants g WHERE g.user_id=p_actor ORDER BY g.id FOR SHARE NOWAIT;
 IF p_actor<>p_subject THEN
 PERFORM 1 FROM public.users u WHERE u.id=p_subject AND u.is_active AND u.erased_at IS NULL FOR UPDATE NOWAIT;
 IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='training_placement_forbidden'; END IF;
 END IF;
 IF NOT EXISTS(SELECT 1 FROM public.users u WHERE u.id=p_subject AND u.is_active AND u.erased_at IS NULL
 AND (NOT u.is_dependent OR EXISTS(SELECT 1 FROM public.guardian_authority_relationships r WHERE r.subject_user_id=u.id AND public.guardian_authority_current(r.guardian_user_id,u.id)))) THEN
 RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='training_placement_subject_ineligible'; END IF;
END $$;
CREATE FUNCTION public.training_placement_latch_withdrawals(p_change uuid,p_placement uuid,p_leg text,p_effective date)
RETURNS void LANGUAGE plpgsql SET search_path=pg_catalog,public AS $$
DECLARE total bigint;actual_now timestamptz:=clock_timestamp();
BEGIN
 SELECT count(*) INTO total FROM public.training_prescriptions p WHERE p.placement_id=p_placement;
 IF total>4096 THEN RAISE EXCEPTION USING ERRCODE='54000',MESSAGE='training_placement_prescription_limit'; END IF;
 IF p_effective=(actual_now AT TIME ZONE 'Europe/Lisbon')::date AND EXISTS(
 SELECT 1 FROM public.training_prescriptions p JOIN public.training_sessions s ON s.id=p.session_id
 WHERE p.placement_id=p_placement AND public.training_session_local_date(s.scheduled_on,s.starts_at)=p_effective
 AND (s.starts_at IS NULL OR s.starts_at<=actual_now)) THEN
 RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='training_placement_started_boundary_unresolved'; END IF;
 INSERT INTO public.training_prescription_withdrawals(prescription_id,live_prescription_id,change_id,leg,effective_on,observed_session_on,observed_starts_at,observed_precision)
 SELECT p.id,p.id,p_change,p_leg,p_effective,public.training_session_local_date(s.scheduled_on,s.starts_at),s.starts_at,s.schedule_precision
 FROM public.training_prescriptions p JOIN public.training_sessions s ON s.id=p.session_id
 WHERE p.placement_id=p_placement AND(public.training_session_local_date(s.scheduled_on,s.starts_at)>p_effective
 OR(public.training_session_local_date(s.scheduled_on,s.starts_at)=p_effective AND(p_effective>(actual_now AT TIME ZONE 'Europe/Lisbon')::date OR s.starts_at>actual_now)))
 ON CONFLICT DO NOTHING;
END $$;
CREATE FUNCTION public.apply_training_placement_change(
 p_actor uuid,p_subject uuid,p_programme uuid,p_season uuid,p_command uuid,p_kind text,p_source uuid,p_target uuid,
 p_effective date,p_temporary_until date,p_expected_revision bigint,p_review_sha256 text)
RETURNS TABLE(change_id uuid,head_revision bigint,source_placement_id uuid,destination_placement_id uuid,return_placement_id uuid,replayed boolean)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE source_row public.training_group_placements%ROWTYPE;old_change public.training_placement_changes%ROWTYPE;required integer;
 target_row public.training_groups%ROWTYPE;destination_id uuid;return_id uuid;revision bigint;canonical jsonb;today date;
BEGIN
 SELECT state.required_reader INTO required FROM training_schedule_control.status() state;
 IF required<4 THEN RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='training_placement_reader_floor_required'; END IF;
 PERFORM public.training_placement_lock_actor_subject(p_actor,p_subject);
 today:=(clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date;
 IF p_kind NOT IN ('JOIN','LEAVE','MOVE','TEMPORARY') OR p_kind IS NULL OR p_effective IS NULL OR p_command IS NULL OR p_programme IS NULL OR p_season IS NULL
 OR p_expected_revision IS NULL OR p_expected_revision<0 OR p_review_sha256 IS NULL OR p_review_sha256!~'^[0-9a-f]{64}$' OR(p_kind='JOIN')<>(p_source IS NULL)
 OR(p_kind='LEAVE')<>(p_target IS NULL) OR(p_kind='TEMPORARY')<>(p_temporary_until IS NOT NULL)
 OR(p_temporary_until IS NOT NULL AND p_temporary_until<p_effective) THEN
 RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='training_placement_command_invalid'; END IF;
 IF p_source IS NOT NULL THEN
 SELECT * INTO source_row FROM public.training_group_placements p WHERE p.id=p_source AND p.subject_user_id=p_subject AND p.programme_id=p_programme;
 IF NOT FOUND OR NOT public.training_placement_actor_scope(p_actor,source_row.group_id) THEN
 RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='training_placement_forbidden'; END IF;
 END IF;
 IF p_target IS NOT NULL THEN
 SELECT * INTO target_row FROM public.training_groups g WHERE g.id=p_target AND public.training_placement_group_programme(g.id)=p_programme;
 IF NOT FOUND OR NOT public.training_placement_actor_scope(p_actor,p_target) THEN
 RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='training_placement_forbidden'; END IF;
 END IF;
 canonical:=jsonb_build_object('kind',p_kind,'programme',p_programme,'season',p_season,'source',p_source,'target',p_target,'effective_on',p_effective,'temporary_until',p_temporary_until,'expected_revision',p_expected_revision,'review_sha256',p_review_sha256);
 SELECT * INTO old_change FROM public.training_placement_changes c WHERE c.id=p_command;
 IF FOUND THEN
 IF old_change.subject_user_id IS DISTINCT FROM p_subject OR old_change.actor_user_id IS DISTINCT FROM p_actor
 OR old_change.canonical_command IS DISTINCT FROM canonical THEN RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='training_placement_replay_conflict'; END IF;
 RETURN QUERY SELECT old_change.id,old_change.head_revision,old_change.source_placement_id,old_change.destination_placement_id,old_change.return_placement_id,true;RETURN;
 END IF;
 IF current_setting('transaction_isolation')<>'serializable' THEN RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='training_placement_serializable_required'; END IF;
 IF p_effective<today OR NOT EXISTS(SELECT 1 FROM public.seasons s WHERE s.id=p_season AND p_effective BETWEEN s.starts_on AND s.ends_on
 AND (p_temporary_until IS NULL OR target_row.team_id IS NULL OR p_temporary_until<=s.ends_on)) THEN
 RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='training_placement_dates_invalid'; END IF;
 INSERT INTO public.training_placement_heads(subject_user_id,programme_id,revision) VALUES(p_subject,p_programme,1)
 ON CONFLICT(subject_user_id,programme_id) DO UPDATE SET revision=training_placement_heads.revision+1 RETURNING training_placement_heads.revision INTO revision;
 IF revision<>p_expected_revision+1 THEN RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='training_placement_revision_conflict'; END IF;
 IF p_source IS NOT NULL THEN
 SELECT * INTO source_row FROM public.training_group_placements p WHERE p.id=p_source FOR UPDATE;
 IF source_row.superseded_by_change_id IS NOT NULL OR source_row.cancelled_by_change_id IS NOT NULL
 OR p_effective<source_row.starts_on OR(source_row.ends_on IS NOT NULL AND p_effective>source_row.ends_on)
 OR(p_target IS NOT NULL AND p_target=source_row.group_id)
 OR(p_kind='TEMPORARY' AND source_row.ends_on IS NOT NULL AND p_temporary_until>=source_row.ends_on) THEN
 RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='training_placement_source_conflict'; END IF;
 END IF;
 PERFORM 1 FROM public.training_groups g WHERE g.id=ANY(ARRAY[source_row.group_id,p_target]) ORDER BY g.id FOR SHARE NOWAIT;
 IF p_target IS NOT NULL AND NOT EXISTS(SELECT 1 FROM public.user_memberships m LEFT JOIN public.teams t ON t.id=target_row.team_id
 WHERE m.user_id=p_subject AND m.programme_id=p_programme AND m.season_id=p_season
 AND(p_target IS NULL OR target_row.team_id IS NULL OR(m.team_id=target_row.team_id AND t.season_id=p_season))
 AND p_effective BETWEEN m.starts_on AND COALESCE(m.ends_on,'infinity'::date)) THEN
 RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='training_placement_participation_unavailable'; END IF;
 IF p_kind='TEMPORARY' AND (SELECT count(*) FROM public.user_memberships m JOIN public.seasons return_season ON return_season.id=m.season_id AND p_temporary_until+1 BETWEEN return_season.starts_on AND return_season.ends_on JOIN public.training_groups g ON g.id=source_row.group_id LEFT JOIN public.teams t ON t.id=g.team_id
 WHERE m.user_id=p_subject AND m.programme_id=p_programme
 AND(g.team_id IS NULL OR(m.team_id=g.team_id AND t.season_id=m.season_id))
 AND p_temporary_until+1 BETWEEN m.starts_on AND COALESCE(m.ends_on,'infinity'::date))<>1 THEN
 RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='training_placement_return_unavailable'; END IF;
 IF p_target IS NOT NULL THEN destination_id:=gen_random_uuid(); END IF;
 IF p_kind='TEMPORARY' THEN return_id:=gen_random_uuid(); END IF;

 IF p_source IS NOT NULL THEN
 IF p_effective=source_row.starts_on THEN
 UPDATE public.training_group_placements SET superseded_by_change_id=p_command,version=version+1,updated_at=clock_timestamp() WHERE id=p_source;
 ELSE UPDATE public.training_group_placements SET ends_on=p_effective-1,version=version+1,updated_at=clock_timestamp() WHERE id=p_source;
 END IF;
 END IF;
 IF destination_id IS NOT NULL THEN
 INSERT INTO public.training_group_placements(id,subject_user_id,programme_id,group_id,starts_on,ends_on,change_id)
 VALUES(destination_id,p_subject,p_programme,p_target,p_effective,CASE WHEN p_kind='TEMPORARY' THEN p_temporary_until ELSE source_row.ends_on END,p_command);
 END IF;
 IF return_id IS NOT NULL THEN
 INSERT INTO public.training_group_placements(id,subject_user_id,programme_id,group_id,starts_on,ends_on,change_id)
 VALUES(return_id,p_subject,p_programme,source_row.group_id,p_temporary_until+1,source_row.ends_on,p_command);
 END IF;
 SELECT h.revision INTO revision FROM public.training_placement_heads h WHERE h.subject_user_id=p_subject AND h.programme_id=p_programme;
 INSERT INTO public.training_placement_changes(id,subject_user_id,programme_id,season_id,kind,source_placement_id,destination_placement_id,return_placement_id,effective_on,temporary_until,original_source_starts_on,original_source_ends_on,original_source_version,actor_user_id,canonical_command,head_revision)
 VALUES(p_command,p_subject,p_programme,p_season,p_kind,p_source,destination_id,return_id,p_effective,p_temporary_until,source_row.starts_on,source_row.ends_on,source_row.version,p_actor,canonical,revision);
 IF p_source IS NOT NULL THEN PERFORM public.training_placement_latch_withdrawals(p_command,p_source,'OUTBOUND',p_effective); END IF;
 PERFORM public.training_placement_lock_actor_subject(p_actor,p_subject);
 IF p_effective<(clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date THEN RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='training_placement_effective_boundary_changed'; END IF;
 -- Head may also be touched by structural triggers; retain the final revision in the result.
 RETURN QUERY SELECT p_command,revision,p_source,destination_id,return_id,false;
END $$;

CREATE FUNCTION public.cancel_training_placement_change(p_actor uuid,p_command uuid,p_cancellation uuid,p_expected_revision bigint,p_review_sha256 text)
RETURNS TABLE(change_id uuid,head_revision bigint,source_placement_id uuid,destination_placement_id uuid,return_placement_id uuid,replayed boolean)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE c public.training_placement_changes%ROWTYPE;prior public.training_placement_changes%ROWTYPE;
 source_row public.training_group_placements%ROWTYPE;revision bigint;canonical jsonb;row_count integer;required integer;
BEGIN
 IF p_review_sha256 IS NULL OR p_review_sha256!~'^[0-9a-f]{64}$' THEN RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='training_placement_review_fingerprint_invalid'; END IF;
 SELECT state.required_reader INTO required FROM training_schedule_control.status() state;
 IF required<4 THEN RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='training_placement_reader_floor_required'; END IF;
 SELECT * INTO c FROM public.training_placement_changes WHERE id=p_command AND kind<>'CANCEL';
 IF NOT FOUND OR c.subject_user_id IS NULL THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='training_placement_forbidden'; END IF;
 PERFORM public.training_placement_lock_actor_subject(p_actor,c.subject_user_id);
 IF EXISTS(SELECT 1 FROM public.training_group_placements p WHERE p.id=ANY(ARRAY[c.source_placement_id,c.destination_placement_id,c.return_placement_id])
 AND NOT public.training_placement_actor_scope(p_actor,p.group_id)) THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='training_placement_forbidden'; END IF;
 canonical:=jsonb_build_object('cancel',p_command,'expected_revision',p_expected_revision,'review_sha256',p_review_sha256);
 SELECT * INTO prior FROM public.training_placement_changes WHERE id=p_cancellation;
 IF FOUND THEN
 IF prior.actor_user_id IS DISTINCT FROM p_actor OR prior.subject_user_id IS DISTINCT FROM c.subject_user_id OR prior.canonical_command IS DISTINCT FROM canonical THEN
 RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='training_placement_replay_conflict'; END IF;
 RETURN QUERY SELECT prior.id,prior.head_revision,prior.source_placement_id,prior.destination_placement_id,prior.return_placement_id,true;RETURN;
 END IF;
 IF current_setting('transaction_isolation')<>'serializable' THEN RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='training_placement_serializable_required'; END IF;
 IF p_cancellation IS NULL OR p_expected_revision IS NULL OR c.effective_on<=(clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date
 OR EXISTS(SELECT 1 FROM public.training_placement_changes cancelled WHERE cancelled.cancels_change_id=c.id) THEN
 RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='training_placement_cancel_conflict'; END IF;
 INSERT INTO public.training_placement_heads(subject_user_id,programme_id,revision) VALUES(c.subject_user_id,c.programme_id,1)
 ON CONFLICT(subject_user_id,programme_id) DO UPDATE SET revision=training_placement_heads.revision+1 RETURNING training_placement_heads.revision INTO revision;
 IF revision<>p_expected_revision+1 THEN RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='training_placement_revision_conflict'; END IF;
 PERFORM 1 FROM public.training_group_placements p WHERE p.id=ANY(ARRAY[c.source_placement_id,c.destination_placement_id,c.return_placement_id]) ORDER BY p.id FOR UPDATE;
 IF EXISTS(SELECT 1 FROM public.training_group_placements p WHERE p.id=ANY(ARRAY[c.destination_placement_id,c.return_placement_id])
 AND(p.change_id<>c.id OR p.superseded_by_change_id IS NOT NULL OR p.cancelled_by_change_id IS NOT NULL OR p.version<>1)) THEN
 RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='training_placement_cancel_conflict'; END IF;
 UPDATE public.training_group_placements SET cancelled_by_change_id=p_cancellation,version=version+1,updated_at=clock_timestamp()
 WHERE id=ANY(ARRAY[c.destination_placement_id,c.return_placement_id]);
 IF c.source_placement_id IS NOT NULL THEN
 SELECT * INTO source_row FROM public.training_group_placements p WHERE p.id=c.source_placement_id;
 IF source_row.cancelled_by_change_id IS NOT NULL OR source_row.version IS DISTINCT FROM c.original_source_version+1 OR source_row.starts_on IS DISTINCT FROM c.original_source_starts_on
 OR(source_row.superseded_by_change_id IS NOT NULL AND source_row.superseded_by_change_id<>c.id)
 OR(source_row.superseded_by_change_id IS NULL AND source_row.ends_on IS DISTINCT FROM c.effective_on-1) THEN
 RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='training_placement_cancel_source_conflict'; END IF;
 UPDATE public.training_group_placements SET ends_on=c.original_source_ends_on,superseded_by_change_id=NULL,version=version+1,updated_at=clock_timestamp()
 WHERE id=c.source_placement_id;
 END IF;
 SELECT h.revision INTO revision FROM public.training_placement_heads h WHERE h.subject_user_id=c.subject_user_id AND h.programme_id=c.programme_id;
 INSERT INTO public.training_placement_changes(id,subject_user_id,programme_id,season_id,kind,source_placement_id,destination_placement_id,effective_on,actor_user_id,canonical_command,head_revision,cancels_change_id)
 VALUES(p_cancellation,c.subject_user_id,c.programme_id,c.season_id,'CANCEL',c.source_placement_id,c.destination_placement_id,c.effective_on,p_actor,canonical,revision,c.id);
 PERFORM public.training_placement_lock_actor_subject(p_actor,c.subject_user_id);
 IF c.effective_on<=(clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date THEN RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='training_placement_effective_boundary_changed'; END IF;
 RETURN QUERY SELECT p_cancellation,revision,c.source_placement_id,c.destination_placement_id,NULL::uuid,false;
END $$;

CREATE FUNCTION public.training_prescription_current_delivery(p_prescription uuid) RETURNS boolean
LANGUAGE sql VOLATILE SET search_path=pg_catalog,public AS $$
 SELECT EXISTS(SELECT 1 FROM public.training_prescriptions prescription
 JOIN public.training_sessions session ON session.id=prescription.session_id JOIN public.training_plans plan ON plan.id=session.plan_id
 LEFT JOIN public.training_groups delivery_group ON delivery_group.id=plan.training_group_id
 LEFT JOIN public.teams delivery_team ON delivery_team.id=delivery_group.team_id
 JOIN public.users subject ON subject.id=prescription.athlete_user_id AND subject.is_active AND subject.erased_at IS NULL
 AND(NOT subject.is_dependent OR EXISTS(SELECT 1 FROM public.guardian_authority_relationships r WHERE r.subject_user_id=subject.id AND public.guardian_authority_current(r.guardian_user_id,subject.id)))
 LEFT JOIN public.training_group_placements placement ON placement.id=prescription.placement_id
 WHERE prescription.id=p_prescription
 AND (plan.training_group_id IS NULL OR(placement.id IS NOT NULL AND placement.subject_user_id=subject.id AND placement.group_id=plan.training_group_id
 AND placement.cancelled_by_change_id IS NULL))
 AND NOT EXISTS(SELECT 1 FROM public.training_prescription_withdrawals withdrawal
 WHERE withdrawal.prescription_id=prescription.id AND withdrawal.effective_on<=(clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date
 AND NOT EXISTS(SELECT 1 FROM public.training_placement_changes cancelled WHERE cancelled.cancels_change_id=withdrawal.change_id))
 AND EXISTS(SELECT 1 FROM public.user_memberships membership WHERE membership.user_id=subject.id AND membership.season_id=plan.season_id
 AND(plan.programme_id IS NULL OR membership.programme_id=plan.programme_id) AND(plan.team_id IS NULL OR membership.team_id=plan.team_id)
 AND(plan.training_group_id IS NULL OR(membership.programme_id=COALESCE(delivery_group.programme_id,delivery_team.programme_id) AND(delivery_group.team_id IS NULL OR membership.team_id=delivery_group.team_id)))
 AND public.training_session_local_date(session.scheduled_on,session.starts_at) BETWEEN membership.starts_on AND COALESCE(membership.ends_on,'infinity'::date)))
$$;
CREATE FUNCTION public.guard_training_prescription_placement() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog,public AS $$
DECLARE plan_row public.training_plans%ROWTYPE;resolved uuid;membership uuid;required integer;
BEGIN
 SELECT plan.* INTO STRICT plan_row FROM public.training_sessions s JOIN public.training_plans plan ON plan.id=s.plan_id WHERE s.id=NEW.session_id;
 IF NOT EXISTS(SELECT 1 FROM public.training_plan_publications publication WHERE publication.id=NEW.publication_id AND publication.plan_id=plan_row.id) THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='training_prescription_publication_plan_invalid'; END IF;
 IF plan_row.training_group_id IS NULL THEN
 IF NEW.placement_id IS NOT NULL THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='training_prescription_placement_invalid'; END IF;
 RETURN NEW;END IF;
 SELECT status.required_reader INTO required FROM training_schedule_control.status() status;
 IF required<4 THEN RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='training_placement_reader_floor_required'; END IF;
 SELECT r.placement_id,r.membership_id INTO resolved,membership FROM public.training_sessions s
 CROSS JOIN LATERAL public.training_placement_resolve(NEW.athlete_user_id,plan_row.training_group_id,plan_row.season_id,public.training_session_local_date(s.scheduled_on,s.starts_at)) r JOIN public.user_memberships membership ON membership.id=r.membership_id
 WHERE s.id=NEW.session_id AND(plan_row.programme_id IS NULL OR membership.programme_id=plan_row.programme_id) AND(plan_row.team_id IS NULL OR membership.team_id=plan_row.team_id);
 IF resolved IS NULL OR NEW.placement_id IS DISTINCT FROM resolved OR NEW.membership_id IS DISTINCT FROM membership THEN
 RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='training_prescription_placement_invalid'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER training_prescriptions_placement_guard BEFORE INSERT ON public.training_prescriptions FOR EACH ROW EXECUTE FUNCTION public.guard_training_prescription_placement();
CREATE FUNCTION public.latch_training_placement_session_departures() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE c record;new_day date:=public.training_session_local_date(NEW.scheduled_on,NEW.starts_at);
BEGIN
 FOR c IN SELECT change_row.id AS change_id,change_row.source_placement_id AS placement_id,'OUTBOUND'::text AS leg,change_row.effective_on
 FROM public.training_placement_changes change_row WHERE change_row.kind IN('LEAVE','MOVE','TEMPORARY')
 AND NOT EXISTS(SELECT 1 FROM public.training_placement_changes cancelled WHERE cancelled.cancels_change_id=change_row.id)
 UNION ALL SELECT change_row.id,change_row.destination_placement_id,'RETURN',change_row.temporary_until+1
 FROM public.training_placement_changes change_row WHERE change_row.kind='TEMPORARY'
 AND NOT EXISTS(SELECT 1 FROM public.training_placement_changes cancelled WHERE cancelled.cancels_change_id=change_row.id)
 LOOP
 IF new_day>=c.effective_on THEN
 INSERT INTO public.training_prescription_withdrawals(prescription_id,live_prescription_id,change_id,leg,effective_on,observed_session_on,observed_starts_at,observed_precision)
 SELECT prescription.id,prescription.id,c.change_id,c.leg,c.effective_on,new_day,NEW.starts_at,NEW.schedule_precision
 FROM public.training_prescriptions prescription WHERE prescription.session_id=NEW.id AND prescription.placement_id=c.placement_id
 ON CONFLICT DO NOTHING;
 END IF;
 END LOOP;
 RETURN NEW;
END $$;
CREATE TRIGGER training_sessions_latch_placement_departure BEFORE UPDATE OF scheduled_on,starts_at,ends_at,schedule_precision ON public.training_sessions FOR EACH ROW EXECUTE FUNCTION public.latch_training_placement_session_departures();

CREATE FUNCTION public.guard_training_placement_head() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog,public AS $$
BEGIN
 IF TG_OP='DELETE' THEN
 IF public.training_placement_privileged_operation('MEMBERSHIP_HISTORY_ANONYMIZE',OLD.subject_user_id) THEN RETURN OLD; END IF;
 RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='training_placement_head_delete_forbidden'; END IF;
 IF TG_OP='UPDATE' AND(NEW.subject_user_id IS DISTINCT FROM OLD.subject_user_id OR NEW.programme_id IS DISTINCT FROM OLD.programme_id OR NEW.revision<=OLD.revision) THEN
 RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='training_placement_head_revision_invalid'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER training_placement_heads_guard BEFORE UPDATE OR DELETE ON public.training_placement_heads FOR EACH ROW EXECUTE FUNCTION public.guard_training_placement_head();
CREATE FUNCTION public.guard_training_prescription_withdrawal() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog,public AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM public.training_placement_changes c JOIN public.training_prescriptions p ON p.id=NEW.prescription_id
 WHERE NEW.live_prescription_id=NEW.prescription_id AND c.id=NEW.change_id AND c.subject_user_id=p.athlete_user_id AND c.programme_id=(SELECT programme_id FROM public.training_group_placements placement WHERE placement.id=p.placement_id)
 AND((NEW.leg='OUTBOUND' AND c.kind IN('LEAVE','MOVE','TEMPORARY') AND p.placement_id=c.source_placement_id AND NEW.effective_on=c.effective_on)
 OR(NEW.leg='RETURN' AND c.kind='TEMPORARY' AND p.placement_id=c.destination_placement_id AND NEW.effective_on=c.temporary_until+1))
 AND NEW.observed_session_on>=NEW.effective_on
 AND NOT EXISTS(SELECT 1 FROM public.training_placement_changes cancelled WHERE cancelled.cancels_change_id=c.id)) THEN
 RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='training_prescription_withdrawal_invalid'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER training_prescription_withdrawals_guard BEFORE INSERT ON public.training_prescription_withdrawals FOR EACH ROW EXECUTE FUNCTION public.guard_training_prescription_withdrawal();

-- Defer only future prescription continuity, retaining the immediate history guards.
CREATE OR REPLACE FUNCTION public.prevent_dated_participation_rewrite() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public,pg_temp AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM privacy_protected.training_placement_erasure_fence fence
 WHERE fence.transaction_id=txid_current() AND fence.subject_user_id=OLD.user_id
 AND((fence.operation='MEMBERSHIP_HISTORY_ANONYMIZE' AND OLD.user_id IS NOT NULL AND OLD.principal_id IS NULL
 AND NEW.user_id IS NULL AND NEW.principal_id=fence.principal_id
 AND(to_jsonb(OLD)-ARRAY['user_id','principal_id','updated_at'])=(to_jsonb(NEW)-ARRAY['user_id','principal_id','updated_at']))
 OR(fence.operation='MEMBERSHIP_ACTIVE_REVOKE' AND OLD.starts_on<fence.effective_date AND NEW.ends_on=fence.effective_date-1
 AND(OLD.ends_on IS NULL OR OLD.ends_on>=fence.effective_date)
 AND(to_jsonb(OLD)-ARRAY['ends_on','updated_at'])=(to_jsonb(NEW)-ARRAY['ends_on','updated_at'])))) THEN RETURN NEW; END IF;
 IF classification_erasure.permitted_clear(OLD,NEW) THEN RETURN NEW; END IF;
 IF NEW.user_id IS DISTINCT FROM OLD.user_id OR NEW.season_id IS DISTINCT FROM OLD.season_id
  OR NEW.programme_id IS DISTINCT FROM OLD.programme_id OR NEW.team_id IS DISTINCT FROM OLD.team_id
  OR NEW.competition_category_id IS DISTINCT FROM OLD.competition_category_id
  OR NEW.starts_on IS DISTINCT FROM OLD.starts_on
  OR NEW.age_exception_reason IS DISTINCT FROM OLD.age_exception_reason
  OR NEW.age_exception_by_id IS DISTINCT FROM OLD.age_exception_by_id
  OR NEW.age_exception_at IS DISTINCT FROM OLD.age_exception_at
  OR (OLD.ends_on IS NOT NULL AND (NEW.ends_on IS NULL OR NEW.ends_on>OLD.ends_on)) THEN
  RAISE EXCEPTION 'participation correction requires audited workflow' USING ERRCODE='23514';
 END IF;
 IF NEW.ends_on IS NOT NULL AND (OLD.ends_on IS NULL OR NEW.ends_on<OLD.ends_on) THEN
  IF NEW.ends_on<(clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='participation_past_boundary_immutable'; END IF;
  IF EXISTS (SELECT 1 FROM event_responses response
    JOIN events event ON event.id=response.event_id
    WHERE response.user_id=OLD.user_id
     AND (event.starts_at AT TIME ZONE 'Europe/Lisbon')::date BETWEEN OLD.starts_on AND COALESCE(OLD.ends_on,'infinity'::date)
     AND (event.starts_at AT TIME ZONE 'Europe/Lisbon')::date>NEW.ends_on
     AND (NOT EXISTS (SELECT 1 FROM event_audiences audience WHERE audience.event_id=event.id)
       OR EXISTS (SELECT 1 FROM event_audiences audience WHERE audience.event_id=event.id AND audience.programme_id=OLD.programme_id)))
   OR EXISTS (SELECT 1 FROM training_session_outcomes outcome
    JOIN training_prescriptions prescription ON prescription.id=outcome.prescription_id
    JOIN training_sessions session ON session.id=outcome.session_id
    WHERE prescription.membership_id=OLD.id AND outcome.user_id=OLD.user_id
    AND public.training_session_local_date(session.scheduled_on,session.starts_at)>NEW.ends_on)
   OR EXISTS (SELECT 1 FROM training_session_outcomes outcome
    JOIN training_sessions session ON session.id=outcome.session_id
    JOIN training_plans plan ON plan.id=session.plan_id
    JOIN training_group_members group_member ON group_member.group_id=plan.training_group_id
    WHERE group_member.membership_id=OLD.id AND outcome.user_id=OLD.user_id
     AND public.training_session_local_date(session.scheduled_on,session.starts_at) BETWEEN OLD.starts_on AND COALESCE(OLD.ends_on,'infinity'::date)
     AND public.training_session_local_date(session.scheduled_on,session.starts_at)>NEW.ends_on) THEN
   RAISE EXCEPTION 'recorded event, training or published prescription date requires audited correction'
    USING ERRCODE='23514', CONSTRAINT='user_memberships_recorded_history_end';
  END IF;
 END IF;
 RETURN NEW;
END $$;

CREATE FUNCTION public.check_training_placement_participation_successor() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE final_row public.user_memberships%ROWTYPE;
BEGIN
 IF NEW.ends_on IS NULL OR(OLD.ends_on IS NOT NULL AND NEW.ends_on>=OLD.ends_on) THEN RETURN NEW; END IF;
 SELECT * INTO final_row FROM public.user_memberships WHERE id=NEW.id;
 IF NOT FOUND THEN RETURN NEW; END IF;
 IF EXISTS(SELECT 1 FROM public.training_prescriptions prescription
 JOIN public.training_sessions session ON session.id=prescription.session_id JOIN public.training_plans plan ON plan.id=session.plan_id
 WHERE prescription.membership_id=OLD.id AND public.training_session_local_date(session.scheduled_on,session.starts_at)>final_row.ends_on
 AND(NOT EXISTS(SELECT 1 FROM public.user_memberships successor WHERE successor.user_id=OLD.user_id
 AND successor.programme_id=OLD.programme_id AND successor.season_id=OLD.season_id AND successor.team_id IS NOT DISTINCT FROM OLD.team_id
 AND successor.starts_on=final_row.ends_on+1
 AND public.training_session_local_date(session.scheduled_on,session.starts_at)<=COALESCE(successor.ends_on,'infinity'::date))
 OR NOT(EXISTS(SELECT 1 FROM public.training_placement_resolve(OLD.user_id,plan.training_group_id,plan.season_id,public.training_session_local_date(session.scheduled_on,session.starts_at)) resolved
 WHERE resolved.placement_id=prescription.placement_id)
 OR EXISTS(SELECT 1 FROM public.training_prescription_withdrawals withdrawal JOIN public.training_placement_changes c ON c.id=withdrawal.change_id
 WHERE withdrawal.prescription_id=prescription.id AND c.subject_user_id=OLD.user_id AND c.programme_id=OLD.programme_id
 AND withdrawal.effective_on<=public.training_session_local_date(session.scheduled_on,session.starts_at)
 AND((withdrawal.leg='OUTBOUND' AND c.kind IN('LEAVE','MOVE','TEMPORARY') AND c.source_placement_id=prescription.placement_id AND withdrawal.effective_on=c.effective_on)
 OR(withdrawal.leg='RETURN' AND c.kind='TEMPORARY' AND c.destination_placement_id=prescription.placement_id AND withdrawal.effective_on=c.temporary_until+1))
 AND NOT EXISTS(SELECT 1 FROM public.training_placement_changes cancelled WHERE cancelled.cancels_change_id=c.id))))) THEN
 RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='training_placement_successor_required',CONSTRAINT='user_memberships_recorded_history_end'; END IF;
 RETURN NEW;
END $$;
CREATE CONSTRAINT TRIGGER user_memberships_placement_successor AFTER UPDATE ON public.user_memberships
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.check_training_placement_participation_successor();

CREATE OR REPLACE FUNCTION require_active_privacy_attachment_subject() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE subject_id uuid; old_subject_id uuid; subject_column text;
BEGIN
 subject_column:=COALESCE(NULLIF(TG_ARGV[0],''),'user_id');
 subject_id:=NULLIF(to_jsonb(NEW)->>subject_column,'')::uuid;
 IF TG_OP='UPDATE' THEN old_subject_id:=NULLIF(to_jsonb(OLD)->>subject_column,'')::uuid; END IF;
 IF subject_id IS NULL AND old_subject_id IS NOT NULL AND EXISTS(SELECT 1 FROM privacy_protected.training_placement_erasure_fence fence WHERE fence.transaction_id=txid_current() AND fence.subject_user_id=old_subject_id AND fence.operation='MEMBERSHIP_HISTORY_ANONYMIZE') THEN RETURN NEW; END IF;
 -- The definer owns the protected erasure tables; only a login with actual
 -- migration-role membership may use the historic SET ROLE erasure bypass.
 IF current_user<>session_user AND pg_has_role(session_user,current_user,'MEMBER')
  AND current_setting('mycfc.privacy_erasure_operation',true)='MEMBERSHIP_HISTORY_ANONYMIZE' THEN RETURN NEW; END IF;
 IF subject_id IS NOT NULL THEN
  PERFORM 1 FROM users WHERE id=subject_id AND is_active AND erased_at IS NULL FOR KEY SHARE;
  IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='inactive_privacy_attachment_subject'; END IF;
 END IF;
 IF TG_TABLE_NAME IN ('user_memberships','training_prescriptions','training_placement_heads','training_group_placements','training_placement_changes')
 AND (subject_id IS NOT NULL OR TG_TABLE_NAME IN ('user_memberships','training_prescriptions')) AND EXISTS(
  SELECT 1 FROM privacy_erasure_category_jobs job
  JOIN privacy_erasure_executions execution ON execution.id=job.execution_id
  JOIN data_erasure_requests request ON request.id=execution.request_id
  WHERE job.category_key='membership-history' AND job.status IN ('PENDING','RETRY_WAIT','LEASED')
   AND COALESCE(request.subject_user_id,request.requester_user_id) IN (subject_id,old_subject_id)
 ) THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='privacy_membership_erasure_in_progress'; END IF;
 RETURN NEW;
END;
$$;

-- Protected proof is populated only inside the already validated private erasure operations.
CREATE TABLE privacy_protected.training_placement_erasure_fence (
 transaction_id bigint NOT NULL,subject_user_id uuid NOT NULL,operation text NOT NULL CHECK(operation IN('MEMBERSHIP_ACTIVE_REVOKE','MEMBERSHIP_HISTORY_ANONYMIZE','TRAINING_PRESCRIPTION_DELETE','AUDIT_ACTOR_ANONYMIZE')),
 effective_date date NOT NULL,principal_id uuid NULL REFERENCES public.privacy_pseudonymous_principals(id) ON DELETE RESTRICT,
 PRIMARY KEY(transaction_id,subject_user_id,operation),
 CHECK((operation IN('MEMBERSHIP_HISTORY_ANONYMIZE','AUDIT_ACTOR_ANONYMIZE'))=(principal_id IS NOT NULL))
);
REVOKE ALL ON TABLE privacy_protected.training_placement_erasure_fence FROM PUBLIC;

-- Extend existing sealed membership-history captures; no new executor or policy.
ALTER TABLE privacy_protected.membership_history_source_captures
 ADD COLUMN placement_ids uuid[] NOT NULL DEFAULT ARRAY[]::uuid[],
 ADD COLUMN placement_change_ids uuid[] NOT NULL DEFAULT ARRAY[]::uuid[],
 ADD COLUMN placement_principal_id uuid NULL REFERENCES public.privacy_pseudonymous_principals(id) ON DELETE RESTRICT,
 ADD CONSTRAINT membership_history_source_placements_bounded CHECK(cardinality(placement_ids)<=4096 AND cardinality(placement_change_ids)<=4096 AND array_position(placement_ids,NULL) IS NULL AND array_position(placement_change_ids,NULL) IS NULL);
ALTER TABLE privacy_protected.membership_history_replay_captures
 ADD COLUMN placement_ids uuid[] NOT NULL DEFAULT ARRAY[]::uuid[],
 ADD COLUMN placement_change_ids uuid[] NOT NULL DEFAULT ARRAY[]::uuid[],
 ADD COLUMN placement_principal_id uuid NULL REFERENCES public.privacy_pseudonymous_principals(id) ON DELETE RESTRICT,
 ADD CONSTRAINT membership_history_replay_placements_bounded CHECK(cardinality(placement_ids)<=4096 AND cardinality(placement_change_ids)<=4096 AND array_position(placement_ids,NULL) IS NULL AND array_position(placement_change_ids,NULL) IS NULL);

CREATE OR REPLACE FUNCTION privacy_worker_execute_checkpoint_without_tombstone_guard(
 p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_epoch bigint,p_worker_ref uuid,p_operation_code text,p_action_version text
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE checkpoint_ref uuid; subject_ref uuid; execution_ref uuid; principal_ref uuid; category text; entry jsonb;
 subject_name text; subject_email text; subject_login text;
 operation_index int; changed bigint:=0; n bigint:=0; result_digest bytea; anchor privacy_erasure_retention_anchors%ROWTYPE;
BEGIN
 SELECT checkpoint.id,execution.id,request.subject_user_id,job.category_key,
        plan.plan->'entries'->(job.plan_entry_position-1),checkpoint.operation_position
 INTO checkpoint_ref,execution_ref,subject_ref,category,entry,operation_index
 FROM privacy_erasure_category_jobs job
 JOIN privacy_erasure_job_leases lease ON lease.id=p_lease_id AND lease.job_id=job.id AND lease.epoch=p_epoch
 JOIN privacy_erasure_job_attempts attempt ON attempt.id=p_attempt_id AND attempt.job_id=job.id AND attempt.lease_id=lease.id AND attempt.lease_epoch=p_epoch
 JOIN privacy_erasure_job_checkpoints checkpoint ON checkpoint.job_id=job.id AND checkpoint.operation_code=p_operation_code AND checkpoint.action_version=p_action_version
 JOIN privacy_erasure_executions execution ON execution.id=job.execution_id
 JOIN data_erasure_requests request ON request.id=execution.request_id
 JOIN privacy_request_execution_plans plan ON plan.request_id=request.id AND plan.plan_sha256=execution.plan_sha256
 WHERE job.id=p_job_id AND job.status='LEASED' AND lease.worker_ref=p_worker_ref
  AND lease.released_at IS NULL AND lease.expires_at>clock_timestamp() AND attempt.finished_at IS NULL
 FOR UPDATE OF job,lease,attempt,checkpoint;
 IF NOT FOUND THEN RETURN NULL; END IF;
 IF (SELECT status FROM privacy_erasure_job_checkpoints WHERE id=checkpoint_ref)='SUCCEEDED' THEN RETURN checkpoint_ref; END IF;
 IF subject_ref IS NULL OR entry IS NULL OR entry->>'category'<>category OR entry->>'fallback'<>'BLOCK'
    OR entry->>'action_version'<>p_action_version
    OR entry->'operations'->>(operation_index-1)<>p_operation_code
    OR EXISTS(SELECT 1 FROM privacy_erasure_job_checkpoints prior WHERE prior.job_id=p_job_id AND prior.operation_position<operation_index AND prior.status<>'SUCCEEDED')
    OR EXISTS(SELECT 1 FROM privacy_erasure_category_jobs prior WHERE prior.execution_id=execution_ref AND prior.plan_entry_position<(SELECT plan_entry_position FROM privacy_erasure_category_jobs WHERE id=p_job_id) AND prior.status<>'SUCCEEDED') THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_execution_order_or_plan_invalid';
 END IF;
 SELECT name,email::text,minor_login_id::text INTO subject_name,subject_email,subject_login
 FROM users WHERE id=subject_ref FOR UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_subject_missing'; END IF;
 IF entry->>'disposition' IN ('RESTRICT','EXPIRE') THEN
  SELECT * INTO anchor FROM privacy_erasure_retention_anchors a WHERE a.execution_id=execution_ref AND a.category_key=category;
  IF NOT FOUND OR anchor.anchor_code<>entry->>'retention_anchor' OR anchor.retained_field_codes<>(SELECT array_agg(value ORDER BY value) FROM jsonb_array_elements_text(entry->'retained_fields')) THEN
   RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_retention_anchor_unresolved';
  END IF;
 END IF;

 CASE p_operation_code
 WHEN 'AUDIT_ACTOR_ANONYMIZE' THEN
  IF EXISTS(SELECT 1 FROM minor_credential_audit WHERE minor_user_id=subject_ref OR guardian_user_id=subject_ref OR actor_user_id=subject_ref)
   OR EXISTS(SELECT 1 FROM equipment_audit_events WHERE actor_user_id=subject_ref)
   OR EXISTS(SELECT 1 FROM equipment_audit_events WHERE before_state IS DISTINCT FROM privacy_scrub_audit_json(before_state,subject_ref,subject_name,subject_email,subject_login) OR after_state IS DISTINCT FROM privacy_scrub_audit_json(after_state,subject_ref,subject_name,subject_email,subject_login))
   OR EXISTS(SELECT 1 FROM member_profile_audit_events WHERE actor_user_id=subject_ref OR subject_user_id=subject_ref)
   OR EXISTS(SELECT 1 FROM staff_grant_audit_events WHERE actor_user_id=subject_ref)
   OR EXISTS(SELECT 1 FROM staff_grant_audit_events WHERE reason IS DISTINCT FROM privacy_scrub_audit_text(reason,subject_ref,subject_name,subject_email,subject_login))
   OR EXISTS(SELECT 1 FROM photo_album_audit_events WHERE actor_user_id=subject_ref)
   OR EXISTS(SELECT 1 FROM announcement_audit_events WHERE actor_user_id=subject_ref)
   OR EXISTS(SELECT 1 FROM feature_flag_events WHERE actor_user_id=subject_ref)
   OR EXISTS(SELECT 1 FROM training_copy_events WHERE copied_by_id=subject_ref)
   OR EXISTS(SELECT 1 FROM training_placement_changes WHERE actor_user_id=subject_ref) THEN
   INSERT INTO privacy_pseudonymous_principals(purpose) VALUES('AUDIT') RETURNING id INTO principal_ref;
   INSERT INTO privacy_protected.training_placement_erasure_fence(transaction_id,subject_user_id,operation,effective_date,principal_id) VALUES(txid_current(),subject_ref,'AUDIT_ACTOR_ANONYMIZE',public.privacy_membership_history_effective_date(execution_ref,subject_ref),principal_ref);
   PERFORM set_config('mycfc.privacy_erasure_operation','AUDIT_ACTOR_ANONYMIZE',true);
   UPDATE minor_credential_audit SET
    minor_principal_id=CASE WHEN minor_user_id=subject_ref THEN principal_ref ELSE minor_principal_id END,
    minor_user_id=CASE WHEN minor_user_id=subject_ref THEN NULL ELSE minor_user_id END,
    guardian_principal_id=CASE WHEN guardian_user_id=subject_ref THEN principal_ref ELSE guardian_principal_id END,
    guardian_user_id=CASE WHEN guardian_user_id=subject_ref THEN NULL ELSE guardian_user_id END,
    actor_principal_id=CASE WHEN actor_user_id=subject_ref THEN principal_ref ELSE actor_principal_id END,
    actor_user_id=CASE WHEN actor_user_id=subject_ref THEN NULL ELSE actor_user_id END,
    issued_login_id=CASE WHEN minor_user_id=subject_ref THEN 'anonymised' ELSE privacy_scrub_audit_text(issued_login_id::text,subject_ref,subject_name,subject_email,subject_login) END
   WHERE minor_user_id=subject_ref OR guardian_user_id=subject_ref OR actor_user_id=subject_ref;
   GET DIAGNOSTICS changed=ROW_COUNT;
   UPDATE equipment_audit_events SET
    actor_principal_id=CASE WHEN actor_user_id=subject_ref THEN principal_ref ELSE actor_principal_id END,
    actor_user_id=CASE WHEN actor_user_id=subject_ref THEN NULL ELSE actor_user_id END,
    before_state=privacy_scrub_audit_json(before_state,subject_ref,subject_name,subject_email,subject_login),
    after_state=privacy_scrub_audit_json(after_state,subject_ref,subject_name,subject_email,subject_login)
   WHERE actor_user_id=subject_ref
    OR before_state IS DISTINCT FROM privacy_scrub_audit_json(before_state,subject_ref,subject_name,subject_email,subject_login)
    OR after_state IS DISTINCT FROM privacy_scrub_audit_json(after_state,subject_ref,subject_name,subject_email,subject_login);
   GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
   UPDATE member_profile_audit_events SET
    actor_principal_id=CASE WHEN actor_user_id=subject_ref THEN principal_ref ELSE actor_principal_id END,
    actor_user_id=CASE WHEN actor_user_id=subject_ref THEN NULL ELSE actor_user_id END,
    subject_principal_id=CASE WHEN subject_user_id=subject_ref THEN principal_ref ELSE subject_principal_id END,
    subject_user_id=CASE WHEN subject_user_id=subject_ref THEN NULL ELSE subject_user_id END
   WHERE actor_user_id=subject_ref OR subject_user_id=subject_ref; GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
   UPDATE staff_grant_audit_events SET
    actor_principal_id=CASE WHEN actor_user_id=subject_ref THEN principal_ref ELSE actor_principal_id END,
    actor_user_id=CASE WHEN actor_user_id=subject_ref THEN NULL ELSE actor_user_id END,
    reason=privacy_scrub_audit_text(reason,subject_ref,subject_name,subject_email,subject_login)
   WHERE actor_user_id=subject_ref OR reason IS DISTINCT FROM privacy_scrub_audit_text(reason,subject_ref,subject_name,subject_email,subject_login);
   GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
   UPDATE photo_album_audit_events SET actor_user_id=NULL,actor_principal_id=principal_ref WHERE actor_user_id=subject_ref;
   GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
   UPDATE announcement_audit_events SET actor_user_id=NULL,actor_principal_id=principal_ref WHERE actor_user_id=subject_ref;
   GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
   UPDATE feature_flag_events SET actor_user_id=NULL,actor_principal_id=principal_ref WHERE actor_user_id=subject_ref;
   GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
   UPDATE training_copy_events SET copied_by_id=NULL,copied_by_principal_id=principal_ref WHERE copied_by_id=subject_ref;
   GET DIAGNOSTICS n=ROW_COUNT;changed:=changed+n;
   UPDATE training_placement_changes SET actor_user_id=NULL,actor_principal_id=principal_ref WHERE actor_user_id=subject_ref;
   GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
  END IF;
  DELETE FROM privacy_protected.training_placement_erasure_fence WHERE transaction_id=txid_current() AND subject_user_id=subject_ref AND operation='AUDIT_ACTOR_ANONYMIZE';
 WHEN 'ACTIVITY_CONNECTION_DISCONNECT' THEN
  UPDATE activity_connections SET status='DISCONNECTED',provider_user_id='erased-'||id::text,credentials_ciphertext=NULL,credential_key_id=NULL,
   credential_expires_at=NULL,scopes='{}',sync_cursor=NULL,last_error_code=NULL,last_error_message=NULL,last_error_at=NULL,
   disconnected_at=COALESCE(disconnected_at,clock_timestamp()),updated_at=clock_timestamp()
  WHERE user_id=subject_ref AND status<>'DISCONNECTED'; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'ACTIVITY_SUBJECT_DELETE' THEN
  DELETE FROM activity_connections WHERE user_id=subject_ref; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'ANNOUNCEMENT_DELIVERY_DELETE' THEN
  DELETE FROM announcement_deliveries WHERE user_id=subject_ref; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'AUTH_ACCESS_REVOKE' THEN
  DELETE FROM sessions WHERE subject_indexed AND user_id=subject_ref; GET DIAGNOSTICS changed=ROW_COUNT;
  DELETE FROM user_platform_roles WHERE user_id=subject_ref; GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
 WHEN 'AUTH_TOKEN_DELETE' THEN
  DELETE FROM email_verification_tokens WHERE user_id=subject_ref; GET DIAGNOSTICS changed=ROW_COUNT;
  DELETE FROM password_reset_tokens WHERE user_id=subject_ref; GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
 WHEN 'DEPENDANT_RELATIONSHIP_DELETE' THEN
  IF EXISTS(SELECT 1 FROM users WHERE guardian_id=subject_ref) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_dependants_unresolved'; END IF;
  changed:=0;
 WHEN 'EVENT_RESPONSE_DELETE' THEN
  DELETE FROM event_responses WHERE user_id=subject_ref; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'IDENTITY_CLEAR' THEN
  IF EXISTS(SELECT 1 FROM sessions WHERE NOT subject_indexed AND expiry>clock_timestamp()) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_legacy_sessions_unresolved'; END IF;
  IF EXISTS(SELECT 1 FROM users WHERE guardian_id=subject_ref) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_dependants_unresolved'; END IF;
  DELETE FROM sessions WHERE subject_indexed AND user_id=subject_ref;
  DELETE FROM email_verification_tokens WHERE user_id=subject_ref;
  DELETE FROM password_reset_tokens WHERE user_id=subject_ref;
  DELETE FROM user_platform_roles WHERE user_id=subject_ref;
  UPDATE users SET name='Conta eliminada',email=NULL,email_verified_at=NULL,minor_login_id=NULL,password_hash=NULL,
   guardian_id=NULL,is_dependent=false,date_of_birth=DATE '1900-01-01',is_active=false,leaderboard_visible=false,
   credential_version=credential_version+1,erased_at=clock_timestamp(),erasure_execution_id=execution_ref,updated_at=clock_timestamp()
  WHERE id=subject_ref AND erased_at IS NULL; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'MEMBERSHIP_ACTIVE_REVOKE' THEN
  INSERT INTO privacy_protected.training_placement_erasure_fence(transaction_id,subject_user_id,operation,effective_date)
  VALUES(txid_current(),subject_ref,'MEMBERSHIP_ACTIVE_REVOKE',public.privacy_membership_history_effective_date(execution_ref,subject_ref));
  PERFORM set_config('mycfc.privacy_erasure_operation','MEMBERSHIP_ACTIVE_REVOKE',true);
  IF EXISTS(SELECT 1 FROM training_prescriptions prescription JOIN user_memberships membership ON membership.id=prescription.membership_id
   WHERE membership.user_id=subject_ref AND membership.starts_on>=public.privacy_membership_history_effective_date(execution_ref,subject_ref)) THEN
   RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_dependency_unsupported';
  END IF;
  DELETE FROM training_variations WHERE target_membership_id IN(
   SELECT id FROM user_memberships WHERE user_id=subject_ref AND starts_on>=public.privacy_membership_history_effective_date(execution_ref,subject_ref));
  GET DIAGNOSTICS changed=ROW_COUNT;
  DELETE FROM training_variation_group_members WHERE membership_id IN(
   SELECT id FROM user_memberships WHERE user_id=subject_ref AND starts_on>=public.privacy_membership_history_effective_date(execution_ref,subject_ref));
  GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
  DELETE FROM training_group_members WHERE membership_id IN(
   SELECT id FROM user_memberships WHERE user_id=subject_ref AND starts_on>=public.privacy_membership_history_effective_date(execution_ref,subject_ref));
  GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
  DELETE FROM user_memberships WHERE user_id=subject_ref AND starts_on>=public.privacy_membership_history_effective_date(execution_ref,subject_ref);
  GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
  UPDATE user_memberships SET ends_on=public.privacy_membership_history_effective_date(execution_ref,subject_ref)-1,updated_at=clock_timestamp()
   WHERE user_id=subject_ref AND starts_on<public.privacy_membership_history_effective_date(execution_ref,subject_ref)
    AND (ends_on IS NULL OR ends_on>=public.privacy_membership_history_effective_date(execution_ref,subject_ref));
  GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
  DELETE FROM privacy_protected.training_placement_erasure_fence WHERE transaction_id=txid_current() AND subject_user_id=subject_ref AND operation='MEMBERSHIP_ACTIVE_REVOKE';
 WHEN 'MEMBERSHIP_HISTORY_ANONYMIZE' THEN
  IF EXISTS(SELECT 1 FROM training_prescriptions prescription JOIN user_memberships membership ON membership.id=prescription.membership_id
   WHERE membership.user_id=subject_ref) THEN
   RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_dependency_unsupported';
  END IF;
  IF EXISTS(SELECT 1 FROM user_memberships WHERE user_id=subject_ref) OR EXISTS(SELECT 1 FROM training_group_placements WHERE subject_user_id=subject_ref) OR EXISTS(SELECT 1 FROM training_placement_changes WHERE subject_user_id=subject_ref) THEN
   SELECT capture.placement_principal_id INTO principal_ref FROM privacy_protected.membership_history_source_captures capture WHERE capture.execution_id=execution_ref;
   IF principal_ref IS NULL THEN INSERT INTO privacy_pseudonymous_principals(purpose) VALUES('MEMBERSHIP') RETURNING id INTO principal_ref; END IF;
  INSERT INTO privacy_protected.training_placement_erasure_fence(transaction_id,subject_user_id,operation,effective_date,principal_id)
  VALUES(txid_current(),subject_ref,'MEMBERSHIP_HISTORY_ANONYMIZE',public.privacy_membership_history_effective_date(execution_ref,subject_ref),principal_ref);
   PERFORM set_config('mycfc.privacy_erasure_operation','MEMBERSHIP_HISTORY_ANONYMIZE',true);
   UPDATE training_variations SET
    change_summary=privacy_scrub_audit_text(change_summary,subject_ref,subject_name,subject_email,subject_login),
    patch=privacy_scrub_audit_json(patch,subject_ref,subject_name,subject_email,subject_login),
    updated_at=clock_timestamp()
   WHERE target_membership_id IN(SELECT id FROM user_memberships WHERE user_id=subject_ref);
   GET DIAGNOSTICS changed=ROW_COUNT;
   UPDATE user_memberships SET user_id=NULL,principal_id=principal_ref,updated_at=clock_timestamp()
   WHERE user_id=subject_ref;
   GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
   UPDATE training_group_placements SET subject_user_id=NULL,subject_principal_id=principal_ref WHERE subject_user_id=subject_ref;
   UPDATE training_placement_changes SET subject_user_id=NULL,subject_principal_id=principal_ref WHERE subject_user_id=subject_ref;
   DELETE FROM training_placement_heads WHERE subject_user_id=subject_ref;
  END IF;
  DELETE FROM privacy_protected.training_placement_erasure_fence WHERE transaction_id=txid_current() AND subject_user_id=subject_ref AND operation='MEMBERSHIP_HISTORY_ANONYMIZE';
 WHEN 'PROFILE_HEALTH_DELETE' THEN
  UPDATE member_profiles SET emergency_contact_name='',emergency_contact_relationship='',emergency_contact_phone='',emergency_contact_alternate_phone='',
   medical_declaration='UNKNOWN',allergies='',medical_conditions='',medication='',activity_restrictions='',medical_notes='',updated_at=clock_timestamp()
  WHERE user_id=subject_ref; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'PROFILE_IDENTITY_DELETE' THEN
  DELETE FROM member_profiles WHERE user_id=subject_ref; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'REPAIR_REPORTER_ANONYMIZE' THEN
  UPDATE repair_requests SET reported_by_id=NULL,updated_at=clock_timestamp() WHERE reported_by_id=subject_ref;
  GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'SUGGESTION_SUBJECT_DELETE' THEN
  DELETE FROM suggestions WHERE requester_id=subject_ref; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'TRAINING_PRESCRIPTION_DELETE' THEN
  INSERT INTO privacy_protected.training_placement_erasure_fence(transaction_id,subject_user_id,operation,effective_date) VALUES(txid_current(),subject_ref,'TRAINING_PRESCRIPTION_DELETE',public.privacy_membership_history_effective_date(execution_ref,subject_ref));
  UPDATE training_session_outcomes SET prescription_id=NULL,updated_at=clock_timestamp()
   WHERE prescription_id IN(SELECT id FROM training_prescriptions WHERE athlete_user_id=subject_ref);
  PERFORM set_config('mycfc.privacy_erasure_operation','TRAINING_PRESCRIPTION_DELETE',true);
  DELETE FROM training_prescriptions WHERE athlete_user_id=subject_ref; GET DIAGNOSTICS changed=ROW_COUNT;
  DELETE FROM privacy_protected.training_placement_erasure_fence WHERE transaction_id=txid_current() AND subject_user_id=subject_ref AND operation='TRAINING_PRESCRIPTION_DELETE';
 WHEN 'TRAINING_RESULT_DELETE' THEN
  DELETE FROM training_session_outcomes WHERE user_id=subject_ref; GET DIAGNOSTICS changed=ROW_COUNT;
  DELETE FROM training_logs WHERE user_id=subject_ref; GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
  DELETE FROM performance_metrics WHERE user_id=subject_ref; GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
 ELSE
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_relational_operation_unsupported';
 END CASE;

 -- Verification is in the same transaction as mutation and checkpoint
 -- success. Any residual subject row aborts the whole checkpoint.
 CASE p_operation_code
 WHEN 'AUDIT_ACTOR_ANONYMIZE' THEN
  IF EXISTS(SELECT 1 FROM minor_credential_audit WHERE minor_user_id=subject_ref OR guardian_user_id=subject_ref OR actor_user_id=subject_ref)
   OR EXISTS(SELECT 1 FROM equipment_audit_events WHERE actor_user_id=subject_ref)
   OR EXISTS(SELECT 1 FROM member_profile_audit_events WHERE actor_user_id=subject_ref OR subject_user_id=subject_ref)
   OR EXISTS(SELECT 1 FROM staff_grant_audit_events WHERE actor_user_id=subject_ref)
   OR EXISTS(SELECT 1 FROM photo_album_audit_events WHERE actor_user_id=subject_ref)
   OR EXISTS(SELECT 1 FROM announcement_audit_events WHERE actor_user_id=subject_ref)
   OR EXISTS(SELECT 1 FROM feature_flag_events WHERE actor_user_id=subject_ref)
   OR EXISTS(SELECT 1 FROM training_copy_events WHERE copied_by_id=subject_ref)
   OR EXISTS(SELECT 1 FROM minor_credential_audit WHERE (minor_principal_id=principal_ref OR guardian_principal_id=principal_ref OR actor_principal_id=principal_ref)
      AND (position(lower(subject_ref::text) in lower(issued_login_id::text))>0
       OR (subject_name<>'' AND position(lower(subject_name) in lower(issued_login_id::text))>0)
       OR (subject_email IS NOT NULL AND position(lower(subject_email) in lower(issued_login_id::text))>0)
       OR (subject_login IS NOT NULL AND position(lower(subject_login) in lower(issued_login_id::text))>0)))
   OR EXISTS(SELECT 1 FROM equipment_audit_events WHERE before_state IS DISTINCT FROM privacy_scrub_audit_json(before_state,subject_ref,subject_name,subject_email,subject_login)
       OR after_state IS DISTINCT FROM privacy_scrub_audit_json(after_state,subject_ref,subject_name,subject_email,subject_login))
   OR EXISTS(SELECT 1 FROM staff_grant_audit_events WHERE reason IS DISTINCT FROM privacy_scrub_audit_text(reason,subject_ref,subject_name,subject_email,subject_login)) THEN
   RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_relational_verification_failed';
  END IF;
 WHEN 'ACTIVITY_CONNECTION_DISCONNECT' THEN
  IF EXISTS(SELECT 1 FROM activity_connections WHERE user_id=subject_ref AND (status<>'DISCONNECTED' OR credentials_ciphertext IS NOT NULL OR credential_key_id IS NOT NULL OR cardinality(scopes)>0 OR sync_cursor IS NOT NULL)) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_relational_verification_failed'; END IF;
 WHEN 'ACTIVITY_SUBJECT_DELETE' THEN IF EXISTS(SELECT 1 FROM activity_connections WHERE user_id=subject_ref) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_relational_verification_failed'; END IF;
 WHEN 'ANNOUNCEMENT_DELIVERY_DELETE' THEN IF EXISTS(SELECT 1 FROM announcement_deliveries WHERE user_id=subject_ref) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_relational_verification_failed'; END IF;
 WHEN 'AUTH_ACCESS_REVOKE' THEN IF EXISTS(SELECT 1 FROM sessions WHERE subject_indexed AND user_id=subject_ref) OR EXISTS(SELECT 1 FROM user_platform_roles WHERE user_id=subject_ref) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_relational_verification_failed'; END IF;
 WHEN 'AUTH_TOKEN_DELETE' THEN IF EXISTS(SELECT 1 FROM email_verification_tokens WHERE user_id=subject_ref) OR EXISTS(SELECT 1 FROM password_reset_tokens WHERE user_id=subject_ref) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_relational_verification_failed'; END IF;
 WHEN 'DEPENDANT_RELATIONSHIP_DELETE' THEN IF EXISTS(SELECT 1 FROM users WHERE guardian_id=subject_ref) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_relational_verification_failed'; END IF;
 WHEN 'EVENT_RESPONSE_DELETE' THEN IF EXISTS(SELECT 1 FROM event_responses WHERE user_id=subject_ref) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_relational_verification_failed'; END IF;
 WHEN 'IDENTITY_CLEAR' THEN IF NOT EXISTS(SELECT 1 FROM users WHERE id=subject_ref AND erased_at IS NOT NULL AND NOT is_active AND email IS NULL AND minor_login_id IS NULL AND password_hash IS NULL AND guardian_id IS NULL) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_relational_verification_failed'; END IF;
 WHEN 'MEMBERSHIP_ACTIVE_REVOKE' THEN IF EXISTS(SELECT 1 FROM user_memberships WHERE user_id=subject_ref AND (starts_on>=public.privacy_membership_history_effective_date(execution_ref,subject_ref) OR ends_on IS NULL OR ends_on>=public.privacy_membership_history_effective_date(execution_ref,subject_ref))) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_relational_verification_failed'; END IF;
 WHEN 'MEMBERSHIP_HISTORY_ANONYMIZE' THEN
  IF EXISTS(SELECT 1 FROM user_memberships WHERE user_id=subject_ref)
   OR EXISTS(SELECT 1 FROM user_memberships WHERE principal_id=principal_ref AND (starts_on>=public.privacy_membership_history_effective_date(execution_ref,subject_ref) OR ends_on IS NULL OR ends_on>=public.privacy_membership_history_effective_date(execution_ref,subject_ref)))
   OR EXISTS(SELECT 1 FROM training_variations variation JOIN user_memberships membership ON membership.id=variation.target_membership_id
      WHERE membership.principal_id=principal_ref
       AND (variation.change_summary IS DISTINCT FROM privacy_scrub_audit_text(variation.change_summary,subject_ref,subject_name,subject_email,subject_login)
        OR variation.patch IS DISTINCT FROM privacy_scrub_audit_json(variation.patch,subject_ref,subject_name,subject_email,subject_login))) THEN
   RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_relational_verification_failed';
  END IF;
 WHEN 'PROFILE_HEALTH_DELETE' THEN IF EXISTS(SELECT 1 FROM member_profiles WHERE user_id=subject_ref AND (emergency_contact_name<>'' OR emergency_contact_relationship<>'' OR emergency_contact_phone<>'' OR emergency_contact_alternate_phone<>'' OR medical_declaration<>'UNKNOWN' OR allergies<>'' OR medical_conditions<>'' OR medication<>'' OR activity_restrictions<>'' OR medical_notes<>'')) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_relational_verification_failed'; END IF;
 WHEN 'PROFILE_IDENTITY_DELETE' THEN IF EXISTS(SELECT 1 FROM member_profiles WHERE user_id=subject_ref) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_relational_verification_failed'; END IF;
 WHEN 'REPAIR_REPORTER_ANONYMIZE' THEN IF EXISTS(SELECT 1 FROM repair_requests WHERE reported_by_id=subject_ref) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_relational_verification_failed'; END IF;
 WHEN 'SUGGESTION_SUBJECT_DELETE' THEN IF EXISTS(SELECT 1 FROM suggestions WHERE requester_id=subject_ref) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_relational_verification_failed'; END IF;
 WHEN 'TRAINING_PRESCRIPTION_DELETE' THEN IF EXISTS(SELECT 1 FROM training_prescriptions WHERE athlete_user_id=subject_ref) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_relational_verification_failed'; END IF;
 WHEN 'TRAINING_RESULT_DELETE' THEN IF EXISTS(SELECT 1 FROM training_session_outcomes WHERE user_id=subject_ref) OR EXISTS(SELECT 1 FROM training_logs WHERE user_id=subject_ref) OR EXISTS(SELECT 1 FROM performance_metrics WHERE user_id=subject_ref) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_relational_verification_failed'; END IF;
 ELSE NULL;
 END CASE;

 result_digest:=sha256(convert_to(p_operation_code||':'||subject_ref::text||':'||changed::text,'UTF8'));
 UPDATE privacy_erasure_job_checkpoints SET status='SUCCEEDED',completed_at=clock_timestamp(),completed_by_attempt_id=p_attempt_id,
  affected_rows=changed,result_sha256=result_digest WHERE id=checkpoint_ref AND status='PENDING';
 RETURN checkpoint_ref;
END;
$$;

CREATE OR REPLACE FUNCTION public.privacy_worker_execute_checkpoint_inner_017(
 p_job_id uuid,p_lease_id uuid,p_attempt_id uuid,p_lease_epoch bigint,p_worker_ref uuid,p_operation_code text,p_action_version text
) RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE execution_ref uuid;subject_ref uuid;checkpoint_status text;subject_name text;subject_email text;subject_login text;capture_inserted bigint;
 previous_timezone text;checkpoint_ref uuid;placement_principal uuid;placement_ids uuid[];placement_change_ids uuid[];
BEGIN
 IF p_operation_code='MEMBERSHIP_ACTIVE_REVOKE' THEN
  previous_timezone:=current_setting('TimeZone');
  PERFORM set_config('TimeZone','UTC',true);
 END IF;
 PERFORM public.privacy_worker_require_activation();
 SELECT execution.id,request.subject_user_id,checkpoint.status INTO execution_ref,subject_ref,checkpoint_status
 FROM privacy_erasure_category_jobs job
 JOIN privacy_erasure_job_leases lease ON lease.id=p_lease_id AND lease.job_id=job.id AND lease.epoch=p_lease_epoch
 JOIN privacy_erasure_job_attempts attempt ON attempt.id=p_attempt_id AND attempt.job_id=job.id AND attempt.lease_id=lease.id AND attempt.lease_epoch=p_lease_epoch
 JOIN privacy_erasure_job_checkpoints checkpoint ON checkpoint.job_id=job.id AND checkpoint.operation_code=p_operation_code AND checkpoint.action_version=p_action_version
 JOIN privacy_erasure_executions execution ON execution.id=job.execution_id
 JOIN data_erasure_requests request ON request.id=execution.request_id
 WHERE job.id=p_job_id AND job.status='LEASED' AND lease.worker_ref=p_worker_ref AND lease.released_at IS NULL
  AND lease.expires_at>clock_timestamp() AND attempt.finished_at IS NULL
  AND NOT EXISTS(SELECT 1 FROM privacy_erasure_job_checkpoints prior WHERE prior.job_id=job.id AND prior.operation_position<checkpoint.operation_position AND prior.status<>'SUCCEEDED')
  AND NOT EXISTS(SELECT 1 FROM privacy_erasure_category_jobs prior WHERE prior.execution_id=execution.id AND prior.plan_entry_position<job.plan_entry_position AND prior.status<>'SUCCEEDED')
 FOR UPDATE OF job,lease,attempt,checkpoint;
 IF execution_ref IS NULL OR subject_ref IS NULL THEN
  checkpoint_ref:=public.privacy_worker_execute_checkpoint_inner_015(p_job_id,p_lease_id,p_attempt_id,p_lease_epoch,p_worker_ref,p_operation_code,p_action_version);
  IF previous_timezone IS NOT NULL THEN PERFORM set_config('TimeZone',previous_timezone,true); END IF;
  RETURN checkpoint_ref;
 END IF;
 IF checkpoint_status='PENDING' AND p_operation_code='IDENTITY_CLEAR' AND EXISTS(
  SELECT 1 FROM privacy_erasure_category_jobs membership_job JOIN privacy_erasure_job_checkpoints membership_checkpoint ON membership_checkpoint.job_id=membership_job.id
  WHERE membership_job.execution_id=execution_ref AND membership_checkpoint.operation_code='MEMBERSHIP_HISTORY_ANONYMIZE') THEN
  SELECT name,email::text,minor_login_id::text INTO subject_name,subject_email,subject_login FROM users WHERE id=subject_ref FOR UPDATE;
  PERFORM set_config('mycfc.privacy_erasure_operation','MEMBERSHIP_HISTORY_ANONYMIZE',true);
  UPDATE training_variations SET
   change_summary=privacy_scrub_audit_text(change_summary,subject_ref,subject_name,subject_email,subject_login),
   patch=privacy_scrub_audit_json(patch,subject_ref,subject_name,subject_email,subject_login),updated_at=clock_timestamp()
  WHERE target_membership_id IN(SELECT id FROM user_memberships WHERE user_id=subject_ref);
 END IF;
 IF checkpoint_status='PENDING' AND p_operation_code='MEMBERSHIP_HISTORY_ANONYMIZE' THEN
  SELECT COALESCE(array_agg(p.id ORDER BY p.id),ARRAY[]::uuid[]) INTO placement_ids FROM public.training_group_placements p WHERE p.subject_user_id=subject_ref;
  SELECT COALESCE(array_agg(c.id ORDER BY c.id),ARRAY[]::uuid[]) INTO placement_change_ids FROM public.training_placement_changes c WHERE c.subject_user_id=subject_ref;
  IF cardinality(placement_ids)>4096 OR cardinality(placement_change_ids)>4096 THEN RAISE EXCEPTION USING ERRCODE='54000',MESSAGE='privacy_placement_history_limit'; END IF;
  IF cardinality(placement_ids)+cardinality(placement_change_ids)>0 THEN INSERT INTO public.privacy_pseudonymous_principals(purpose) VALUES('MEMBERSHIP') RETURNING id INTO placement_principal; END IF;
  INSERT INTO privacy_protected.membership_history_source_captures(execution_id,placement_ids,placement_change_ids,placement_principal_id) VALUES(execution_ref,placement_ids,placement_change_ids,placement_principal) ON CONFLICT DO NOTHING;
  GET DIAGNOSTICS capture_inserted=ROW_COUNT;
  IF capture_inserted<>1 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_capture_conflict'; END IF;
  INSERT INTO privacy_protected.membership_history_source_rows(execution_id,membership_id)
   SELECT execution_ref,membership.id FROM user_memberships membership WHERE membership.user_id=subject_ref ORDER BY membership.id;
 END IF;
 checkpoint_ref:=public.privacy_worker_execute_checkpoint_inner_015(p_job_id,p_lease_id,p_attempt_id,p_lease_epoch,p_worker_ref,p_operation_code,p_action_version);
 IF previous_timezone IS NOT NULL THEN PERFORM set_config('TimeZone',previous_timezone,true); END IF;
 RETURN checkpoint_ref;
END;$$;

CREATE OR REPLACE FUNCTION public.privacy_restore_apply_relational_operation(
 p_replay_run_id uuid,p_subject_user_id uuid,p_effective_at timestamptz,p_operation_code text
) RETURNS bigint LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE changed bigint:=0; n bigint:=0; principal_ref uuid; subject_name text; subject_email text; subject_login text;
BEGIN
 IF p_replay_run_id IS NULL OR p_subject_user_id IS NULL OR p_effective_at IS NULL OR NOT public.privacy_relational_replay_operation_supported(p_operation_code)
 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_operation_rejected'; END IF;
 SELECT name,email::text,minor_login_id::text INTO subject_name,subject_email,subject_login FROM users WHERE id=p_subject_user_id FOR UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_subject_missing'; END IF;
 CASE p_operation_code
 WHEN 'ACTIVITY_CONNECTION_DISCONNECT' THEN
  UPDATE activity_connections SET status='DISCONNECTED',provider_user_id='erased-'||id::text,credentials_ciphertext=NULL,credential_key_id=NULL,
   credential_expires_at=NULL,scopes='{}',sync_cursor=NULL,last_error_code=NULL,last_error_message=NULL,last_error_at=NULL,
   disconnected_at=COALESCE(disconnected_at,clock_timestamp()),updated_at=clock_timestamp()
  WHERE user_id=p_subject_user_id AND status<>'DISCONNECTED'; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'ACTIVITY_SUBJECT_DELETE' THEN
  DELETE FROM activity_connections WHERE user_id=p_subject_user_id; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'ANNOUNCEMENT_DELIVERY_DELETE' THEN
  DELETE FROM announcement_deliveries WHERE user_id=p_subject_user_id; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'AUTH_ACCESS_REVOKE' THEN
  DELETE FROM sessions WHERE subject_indexed AND user_id=p_subject_user_id; GET DIAGNOSTICS changed=ROW_COUNT;
  DELETE FROM user_platform_roles WHERE user_id=p_subject_user_id; GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
  UPDATE staff_grants SET revoked_by_id=NULL,revoked_by_replay_run_id=p_replay_run_id,revoked_at=p_effective_at,
   revoke_reason='PRIVACY_ACCOUNT_CLOSURE' WHERE user_id=p_subject_user_id AND revoked_at IS NULL;
  GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
  WITH revoked AS(UPDATE privacy_reviewer_grants SET revoked_by=NULL,revoked_by_replay_run_id=p_replay_run_id,revoked_at=p_effective_at
   WHERE user_id=p_subject_user_id AND revoked_at IS NULL RETURNING id)
  INSERT INTO privacy_reviewer_grant_events(grant_id,actor_ref,action,occurred_at) SELECT id,p_replay_run_id,'REVOKED',p_effective_at FROM revoked;
  GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
  WITH revoked AS(UPDATE privacy_executor_grants SET revoked_by=NULL,revoked_by_replay_run_id=p_replay_run_id,revoked_at=p_effective_at
   WHERE user_id=p_subject_user_id AND revoked_at IS NULL RETURNING id)
  INSERT INTO privacy_executor_grant_events(grant_id,actor_ref,action,occurred_at) SELECT id,p_replay_run_id,'REVOKED',p_effective_at FROM revoked;
  GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
 WHEN 'AUTH_TOKEN_DELETE' THEN
  DELETE FROM email_verification_tokens WHERE user_id=p_subject_user_id; GET DIAGNOSTICS changed=ROW_COUNT;
  DELETE FROM password_reset_tokens WHERE user_id=p_subject_user_id; GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
 WHEN 'DEPENDANT_RELATIONSHIP_DELETE' THEN
  IF EXISTS(SELECT 1 FROM users WHERE guardian_id=p_subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_dependants_unresolved'; END IF;
 WHEN 'EVENT_RESPONSE_DELETE' THEN
  DELETE FROM event_responses WHERE user_id=p_subject_user_id; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'IDENTITY_CLEAR' THEN
  IF EXISTS(SELECT 1 FROM sessions WHERE NOT subject_indexed AND expiry>clock_timestamp()) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_legacy_sessions_unresolved'; END IF;
  IF EXISTS(SELECT 1 FROM users WHERE guardian_id=p_subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_dependants_unresolved'; END IF;
  DELETE FROM sessions WHERE subject_indexed AND user_id=p_subject_user_id;
  DELETE FROM email_verification_tokens WHERE user_id=p_subject_user_id;
  DELETE FROM password_reset_tokens WHERE user_id=p_subject_user_id;
  DELETE FROM user_platform_roles WHERE user_id=p_subject_user_id;
  UPDATE users SET name='Conta eliminada',email=NULL,email_verified_at=NULL,minor_login_id=NULL,password_hash=NULL,guardian_id=NULL,is_dependent=false,
   date_of_birth=DATE '1900-01-01',is_active=false,leaderboard_visible=false,credential_version=credential_version+1,
   erased_at=clock_timestamp(),erasure_execution_id=NULL,erasure_replay_run_id=p_replay_run_id,updated_at=clock_timestamp()
  WHERE id=p_subject_user_id AND erased_at IS NULL; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'MEMBERSHIP_ACTIVE_REVOKE' THEN
  INSERT INTO privacy_protected.training_placement_erasure_fence(transaction_id,subject_user_id,operation,effective_date)
  VALUES(txid_current(),p_subject_user_id,'MEMBERSHIP_ACTIVE_REVOKE',p_effective_at::date);
  PERFORM set_config('mycfc.privacy_erasure_operation','MEMBERSHIP_ACTIVE_REVOKE',true);
  IF EXISTS(SELECT 1 FROM training_prescriptions prescription JOIN user_memberships membership ON membership.id=prescription.membership_id
   WHERE membership.user_id=p_subject_user_id AND membership.starts_on>=p_effective_at::date) THEN
   RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_dependency_unsupported'; END IF;
  DELETE FROM training_variations WHERE target_membership_id IN(SELECT id FROM user_memberships WHERE user_id=p_subject_user_id AND starts_on>=p_effective_at::date);
  GET DIAGNOSTICS changed=ROW_COUNT;
  DELETE FROM training_variation_group_members WHERE membership_id IN(SELECT id FROM user_memberships WHERE user_id=p_subject_user_id AND starts_on>=p_effective_at::date);
  GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
  DELETE FROM training_group_members WHERE membership_id IN(SELECT id FROM user_memberships WHERE user_id=p_subject_user_id AND starts_on>=p_effective_at::date);
  GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
  DELETE FROM user_memberships WHERE user_id=p_subject_user_id AND starts_on>=p_effective_at::date;
  GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
  UPDATE user_memberships SET ends_on=p_effective_at::date-1,updated_at=clock_timestamp()
   WHERE user_id=p_subject_user_id AND starts_on<p_effective_at::date AND (ends_on IS NULL OR ends_on>=p_effective_at::date);
  GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
  DELETE FROM privacy_protected.training_placement_erasure_fence WHERE transaction_id=txid_current() AND subject_user_id=p_subject_user_id AND operation='MEMBERSHIP_ACTIVE_REVOKE';
 WHEN 'MEMBERSHIP_HISTORY_ANONYMIZE' THEN
  IF EXISTS(SELECT 1 FROM training_prescriptions prescription JOIN user_memberships membership ON membership.id=prescription.membership_id
   WHERE membership.user_id=p_subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_membership_dependency_unsupported'; END IF;
  IF EXISTS(SELECT 1 FROM user_memberships WHERE user_id=p_subject_user_id) OR EXISTS(SELECT 1 FROM training_group_placements WHERE subject_user_id=p_subject_user_id) OR EXISTS(SELECT 1 FROM training_placement_changes WHERE subject_user_id=p_subject_user_id) THEN
   SELECT capture.placement_principal_id INTO principal_ref FROM privacy_protected.membership_history_replay_captures capture WHERE capture.run_id=p_replay_run_id;
   IF principal_ref IS NULL THEN INSERT INTO privacy_pseudonymous_principals(purpose) VALUES('MEMBERSHIP') RETURNING id INTO principal_ref; END IF;
  INSERT INTO privacy_protected.training_placement_erasure_fence(transaction_id,subject_user_id,operation,effective_date,principal_id)
  VALUES(txid_current(),p_subject_user_id,'MEMBERSHIP_HISTORY_ANONYMIZE',p_effective_at::date,principal_ref);
   PERFORM set_config('mycfc.privacy_erasure_operation','MEMBERSHIP_HISTORY_ANONYMIZE',true);
   UPDATE training_variations SET change_summary=privacy_scrub_audit_text(change_summary,p_subject_user_id,subject_name,subject_email,subject_login),
    patch=privacy_scrub_audit_json(patch,p_subject_user_id,subject_name,subject_email,subject_login),updated_at=clock_timestamp()
   WHERE target_membership_id IN(SELECT id FROM user_memberships WHERE user_id=p_subject_user_id); GET DIAGNOSTICS changed=ROW_COUNT;
   UPDATE user_memberships SET user_id=NULL,principal_id=principal_ref,updated_at=clock_timestamp() WHERE user_id=p_subject_user_id;
   GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
   UPDATE training_group_placements SET subject_user_id=NULL,subject_principal_id=principal_ref WHERE subject_user_id=p_subject_user_id;
   UPDATE training_placement_changes SET subject_user_id=NULL,subject_principal_id=principal_ref WHERE subject_user_id=p_subject_user_id;
   DELETE FROM training_placement_heads WHERE subject_user_id=p_subject_user_id;
  END IF;
  DELETE FROM privacy_protected.training_placement_erasure_fence WHERE transaction_id=txid_current() AND subject_user_id=p_subject_user_id AND operation='MEMBERSHIP_HISTORY_ANONYMIZE';
 WHEN 'PROFILE_HEALTH_DELETE' THEN
  UPDATE member_profiles SET emergency_contact_name='',emergency_contact_relationship='',emergency_contact_phone='',emergency_contact_alternate_phone='',
   medical_declaration='UNKNOWN',allergies='',medical_conditions='',medication='',activity_restrictions='',medical_notes='',updated_at=clock_timestamp()
  WHERE user_id=p_subject_user_id; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'PROFILE_IDENTITY_DELETE' THEN
  DELETE FROM member_profiles WHERE user_id=p_subject_user_id; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'REPAIR_REPORTER_ANONYMIZE' THEN
  UPDATE repair_requests SET reported_by_id=NULL,updated_at=clock_timestamp() WHERE reported_by_id=p_subject_user_id; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'SUGGESTION_SUBJECT_DELETE' THEN
  DELETE FROM suggestions WHERE requester_id=p_subject_user_id; GET DIAGNOSTICS changed=ROW_COUNT;
 WHEN 'TRAINING_PRESCRIPTION_DELETE' THEN
  INSERT INTO privacy_protected.training_placement_erasure_fence(transaction_id,subject_user_id,operation,effective_date) VALUES(txid_current(),p_subject_user_id,'TRAINING_PRESCRIPTION_DELETE',p_effective_at::date);
  UPDATE training_session_outcomes SET prescription_id=NULL,updated_at=clock_timestamp()
   WHERE prescription_id IN(SELECT id FROM training_prescriptions WHERE athlete_user_id=p_subject_user_id);
  PERFORM set_config('mycfc.privacy_erasure_operation','TRAINING_PRESCRIPTION_DELETE',true);
  DELETE FROM training_prescriptions WHERE athlete_user_id=p_subject_user_id; GET DIAGNOSTICS changed=ROW_COUNT;
  DELETE FROM privacy_protected.training_placement_erasure_fence WHERE transaction_id=txid_current() AND subject_user_id=p_subject_user_id AND operation='TRAINING_PRESCRIPTION_DELETE';
 WHEN 'TRAINING_RESULT_DELETE' THEN
  DELETE FROM training_session_outcomes WHERE user_id=p_subject_user_id; GET DIAGNOSTICS changed=ROW_COUNT;
  DELETE FROM training_logs WHERE user_id=p_subject_user_id; GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
  DELETE FROM performance_metrics WHERE user_id=p_subject_user_id; GET DIAGNOSTICS n=ROW_COUNT; changed:=changed+n;
 END CASE;

 CASE p_operation_code
 WHEN 'ACTIVITY_CONNECTION_DISCONNECT' THEN
  IF EXISTS(SELECT 1 FROM activity_connections WHERE user_id=p_subject_user_id AND (status<>'DISCONNECTED' OR credentials_ciphertext IS NOT NULL OR credential_key_id IS NOT NULL OR cardinality(scopes)>0 OR sync_cursor IS NOT NULL)) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'ACTIVITY_SUBJECT_DELETE' THEN IF EXISTS(SELECT 1 FROM activity_connections WHERE user_id=p_subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'ANNOUNCEMENT_DELIVERY_DELETE' THEN IF EXISTS(SELECT 1 FROM announcement_deliveries WHERE user_id=p_subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'AUTH_ACCESS_REVOKE' THEN IF EXISTS(SELECT 1 FROM sessions WHERE subject_indexed AND user_id=p_subject_user_id)
  OR EXISTS(SELECT 1 FROM user_platform_roles WHERE user_id=p_subject_user_id)
  OR EXISTS(SELECT 1 FROM staff_grants WHERE user_id=p_subject_user_id AND revoked_at IS NULL)
  OR EXISTS(SELECT 1 FROM privacy_reviewer_grants WHERE user_id=p_subject_user_id AND revoked_at IS NULL)
  OR EXISTS(SELECT 1 FROM privacy_executor_grants WHERE user_id=p_subject_user_id AND revoked_at IS NULL)
  THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'AUTH_TOKEN_DELETE' THEN IF EXISTS(SELECT 1 FROM email_verification_tokens WHERE user_id=p_subject_user_id) OR EXISTS(SELECT 1 FROM password_reset_tokens WHERE user_id=p_subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'DEPENDANT_RELATIONSHIP_DELETE' THEN IF EXISTS(SELECT 1 FROM users WHERE guardian_id=p_subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'EVENT_RESPONSE_DELETE' THEN IF EXISTS(SELECT 1 FROM event_responses WHERE user_id=p_subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'IDENTITY_CLEAR' THEN IF NOT EXISTS(SELECT 1 FROM users WHERE id=p_subject_user_id AND erased_at IS NOT NULL AND erasure_execution_id IS NULL
  AND erasure_replay_run_id=p_replay_run_id AND NOT is_active AND email IS NULL AND minor_login_id IS NULL AND password_hash IS NULL AND guardian_id IS NULL)
  THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'MEMBERSHIP_ACTIVE_REVOKE' THEN IF EXISTS(SELECT 1 FROM user_memberships WHERE user_id=p_subject_user_id AND (starts_on>=p_effective_at::date OR ends_on IS NULL OR ends_on>=p_effective_at::date)) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'MEMBERSHIP_HISTORY_ANONYMIZE' THEN
  IF EXISTS(SELECT 1 FROM user_memberships WHERE user_id=p_subject_user_id)
   OR EXISTS(SELECT 1 FROM user_memberships WHERE principal_id=principal_ref AND (starts_on>=p_effective_at::date OR ends_on IS NULL OR ends_on>=p_effective_at::date))
   OR EXISTS(SELECT 1 FROM training_variations variation JOIN user_memberships membership ON membership.id=variation.target_membership_id
      WHERE membership.principal_id=principal_ref
       AND (variation.change_summary IS DISTINCT FROM privacy_scrub_audit_text(variation.change_summary,p_subject_user_id,subject_name,subject_email,subject_login)
        OR variation.patch IS DISTINCT FROM privacy_scrub_audit_json(variation.patch,p_subject_user_id,subject_name,subject_email,subject_login)))
  THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'PROFILE_HEALTH_DELETE' THEN IF EXISTS(SELECT 1 FROM member_profiles WHERE user_id=p_subject_user_id AND (emergency_contact_name<>'' OR emergency_contact_relationship<>'' OR emergency_contact_phone<>'' OR emergency_contact_alternate_phone<>'' OR medical_declaration<>'UNKNOWN' OR allergies<>'' OR medical_conditions<>'' OR medication<>'' OR activity_restrictions<>'' OR medical_notes<>'')) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'PROFILE_IDENTITY_DELETE' THEN IF EXISTS(SELECT 1 FROM member_profiles WHERE user_id=p_subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'REPAIR_REPORTER_ANONYMIZE' THEN IF EXISTS(SELECT 1 FROM repair_requests WHERE reported_by_id=p_subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'SUGGESTION_SUBJECT_DELETE' THEN IF EXISTS(SELECT 1 FROM suggestions WHERE requester_id=p_subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'TRAINING_PRESCRIPTION_DELETE' THEN IF EXISTS(SELECT 1 FROM training_prescriptions WHERE athlete_user_id=p_subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 WHEN 'TRAINING_RESULT_DELETE' THEN IF EXISTS(SELECT 1 FROM training_session_outcomes WHERE user_id=p_subject_user_id) OR EXISTS(SELECT 1 FROM training_logs WHERE user_id=p_subject_user_id) OR EXISTS(SELECT 1 FROM performance_metrics WHERE user_id=p_subject_user_id) THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_verification_failed'; END IF;
 END CASE;
 RETURN changed;
END; $$;

CREATE OR REPLACE FUNCTION public.privacy_restore_begin_replay_hardened(p_import_id uuid,p_worker_ref uuid)
RETURNS TABLE(run_id uuid,outcome_code text) LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE imported privacy_protected.restore_ledger_imports%ROWTYPE;run_ref uuid;outcome text;has_erasure boolean;placement_ids uuid[];placement_change_ids uuid[];placement_principal uuid;
BEGIN
 SELECT * INTO imported FROM privacy_protected.restore_ledger_imports WHERE id=p_import_id;
 IF imported.id IS NULL OR imported.kind<>'closure' OR imported.closure_version<>'restore-tombstone-closure/v4' THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_restore_replay_rejected'; END IF;
 run_ref:=public.privacy_restore_begin_replay(p_import_id,p_worker_ref);
 SELECT EXISTS(SELECT 1 FROM users WHERE id=imported.subject_user_id AND erased_at IS NOT NULL) INTO has_erasure;
 IF NOT EXISTS(SELECT 1 FROM privacy_protected.membership_history_replay_captures WHERE run_id=run_ref) THEN
 IF has_erasure THEN
  SELECT capture.placement_ids,capture.placement_change_ids,capture.placement_principal_id INTO placement_ids,placement_change_ids,placement_principal
  FROM privacy_protected.membership_history_source_captures capture WHERE capture.execution_id=imported.source_execution_id;
  IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_placement_source_capture_missing'; END IF;
 ELSE
  SELECT COALESCE(array_agg(p.id ORDER BY p.id),ARRAY[]::uuid[]) INTO placement_ids FROM public.training_group_placements p WHERE p.subject_user_id=imported.subject_user_id;
  SELECT COALESCE(array_agg(c.id ORDER BY c.id),ARRAY[]::uuid[]) INTO placement_change_ids FROM public.training_placement_changes c WHERE c.subject_user_id=imported.subject_user_id;
  IF cardinality(placement_ids)>4096 OR cardinality(placement_change_ids)>4096 THEN RAISE EXCEPTION USING ERRCODE='54000',MESSAGE='privacy_placement_history_limit'; END IF;
  IF cardinality(placement_ids)+cardinality(placement_change_ids)>0 THEN INSERT INTO public.privacy_pseudonymous_principals(purpose) VALUES('MEMBERSHIP') RETURNING id INTO placement_principal; END IF;
 END IF;
 INSERT INTO privacy_protected.membership_history_replay_captures(run_id,placement_ids,placement_change_ids,placement_principal_id)
 VALUES(run_ref,placement_ids,placement_change_ids,placement_principal);
 END IF;

 IF has_erasure THEN
  INSERT INTO privacy_protected.membership_history_replay_rows(run_id,membership_id)
   SELECT run_ref,row.membership_id FROM privacy_protected.membership_history_source_rows row WHERE row.execution_id=imported.source_execution_id
   ON CONFLICT DO NOTHING;
 ELSE
  INSERT INTO privacy_protected.membership_history_replay_rows(run_id,membership_id)
   SELECT run_ref,membership.id FROM user_memberships membership
   WHERE membership.user_id=imported.subject_user_id AND membership.starts_on<(imported.erasure_effective_at AT TIME ZONE 'UTC')::date
   ON CONFLICT DO NOTHING;
 END IF;
 SELECT begun.run_id,begun.outcome_code INTO run_ref,outcome FROM public.privacy_restore_begin_replay_hardened_inner_013(p_import_id,p_worker_ref) begun;
 IF outcome='ALREADY_APPLIED_SOURCE' AND NOT EXISTS(SELECT 1 FROM privacy_protected.membership_history_replay_postconditions postcondition WHERE postcondition.run_id=run_ref) THEN
  -- Empty prescriptions do not pass through the history checkpoint.
  IF NOT('MEMBERSHIP_HISTORY_ANONYMIZE'=ANY(imported.operations)) THEN PERFORM public.privacy_restore_finalize_membership_postcondition(run_ref); END IF;
 END IF;
 RETURN QUERY SELECT run_ref,outcome;
END;$$;

CREATE FUNCTION public.training_placement_history_canonical(p_placements uuid[],p_changes uuid[],p_principal uuid)
RETURNS bytea LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE canonical bytea;row_ref record;actual uuid[];
BEGIN
 IF p_placements IS NULL OR p_changes IS NULL OR cardinality(p_placements)>4096 OR cardinality(p_changes)>4096
 OR p_placements IS DISTINCT FROM ARRAY(SELECT DISTINCT id FROM unnest(p_placements) id ORDER BY id)
 OR p_changes IS DISTINCT FROM ARRAY(SELECT DISTINCT id FROM unnest(p_changes) id ORDER BY id)
 OR array_position(p_placements,NULL) IS NOT NULL OR array_position(p_changes,NULL) IS NOT NULL THEN
 RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_placement_capture_invalid'; END IF;
 IF cardinality(p_placements)+cardinality(p_changes)=0 THEN
 IF p_principal IS NOT NULL THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_placement_capture_invalid'; END IF;
 RETURN NULL; END IF;
 IF p_principal IS NULL OR NOT EXISTS(SELECT 1 FROM public.privacy_pseudonymous_principals WHERE id=p_principal AND purpose='MEMBERSHIP') THEN
 RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_placement_capture_invalid'; END IF;
 SELECT COALESCE(array_agg(id ORDER BY id),ARRAY[]::uuid[]) INTO actual FROM public.training_group_placements WHERE subject_principal_id=p_principal AND subject_user_id IS NULL;
 IF actual IS DISTINCT FROM p_placements THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_placement_capture_incomplete'; END IF;
 SELECT COALESCE(array_agg(id ORDER BY id),ARRAY[]::uuid[]) INTO actual FROM public.training_placement_changes WHERE subject_principal_id=p_principal AND subject_user_id IS NULL;
 IF actual IS DISTINCT FROM p_changes THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_placement_capture_incomplete'; END IF;
 canonical:=public.privacy_membership_history_frame('mycfc/training-placement-history/v1')||public.privacy_membership_history_frame(cardinality(p_placements)::text);
 FOR row_ref IN SELECT p.* FROM public.training_group_placements p WHERE p.id=ANY(p_placements) ORDER BY p.id LOOP
 canonical:=canonical||public.privacy_membership_history_frame('placement')||public.privacy_membership_history_frame((to_jsonb(row_ref)-'subject_user_id'-'subject_principal_id')::text);
 IF octet_length(canonical)>16777216 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_placement_history_limit'; END IF;
 END LOOP;
 canonical:=canonical||public.privacy_membership_history_frame(cardinality(p_changes)::text);
 FOR row_ref IN SELECT c.* FROM public.training_placement_changes c WHERE c.id=ANY(p_changes) ORDER BY c.id LOOP
 canonical:=canonical||public.privacy_membership_history_frame('change')||public.privacy_membership_history_frame((to_jsonb(row_ref)-'subject_user_id'-'subject_principal_id'-'actor_user_id'-'actor_principal_id')::text);
 IF octet_length(canonical)>16777216 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_placement_history_limit'; END IF;
 END LOOP;
 FOR row_ref IN SELECT w.* FROM public.training_prescription_withdrawals w WHERE w.change_id=ANY(p_changes) ORDER BY w.prescription_id,w.change_id,w.leg LOOP
 IF row_ref.live_prescription_id IS NOT NULL AND row_ref.live_prescription_id<>row_ref.prescription_id THEN
 RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_placement_withdrawal_invalid'; END IF;
 canonical:=canonical||public.privacy_membership_history_frame('withdrawal')||public.privacy_membership_history_frame((to_jsonb(row_ref)-'live_prescription_id')::text);
 IF octet_length(canonical)>16777216 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_placement_history_limit'; END IF;
 END LOOP;
 RETURN canonical;
END $$;
ALTER FUNCTION public.privacy_membership_history_compute_source(uuid,timestamptz) RENAME TO privacy_membership_history_compute_source_inner_083;
CREATE FUNCTION public.privacy_membership_history_compute_source(p_execution_id uuid,p_effective_at timestamptz)
RETURNS TABLE(contract text,postcondition_sha256 bytea,membership_count bigint,variation_count bigint,canonical_size bigint)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE original record;capture privacy_protected.membership_history_source_captures%ROWTYPE;suffix bytea;
BEGIN
 SELECT * INTO original FROM public.privacy_membership_history_compute_source_inner_083(p_execution_id,p_effective_at);
 IF NOT FOUND THEN RETURN; END IF;
 SELECT * INTO STRICT capture FROM privacy_protected.membership_history_source_captures WHERE execution_id=p_execution_id;
 suffix:=public.training_placement_history_canonical(capture.placement_ids,capture.placement_change_ids,capture.placement_principal_id);
 IF suffix IS NULL THEN RETURN QUERY SELECT original.contract,original.postcondition_sha256,original.membership_count,original.variation_count,original.canonical_size;RETURN; END IF;
 IF original.canonical_size+octet_length(suffix)>16777216 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_placement_history_limit'; END IF;
 RETURN QUERY SELECT original.contract,digest(public.privacy_membership_history_frame(original.contract)||public.privacy_membership_history_frame(encode(original.postcondition_sha256,'hex'))||suffix,'sha256'),original.membership_count,original.variation_count,original.canonical_size+octet_length(suffix);
END $$;
ALTER FUNCTION public.privacy_membership_history_compute_replay(uuid,timestamptz) RENAME TO privacy_membership_history_compute_replay_inner_083;
CREATE FUNCTION public.privacy_membership_history_compute_replay(p_run_id uuid,p_effective_at timestamptz)
RETURNS TABLE(contract text,postcondition_sha256 bytea,membership_count bigint,variation_count bigint,canonical_size bigint)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE original record;capture privacy_protected.membership_history_replay_captures%ROWTYPE;suffix bytea;
BEGIN
 SELECT * INTO original FROM public.privacy_membership_history_compute_replay_inner_083(p_run_id,p_effective_at);
 IF NOT FOUND THEN RETURN; END IF;
 SELECT * INTO STRICT capture FROM privacy_protected.membership_history_replay_captures WHERE run_id=p_run_id;
 suffix:=public.training_placement_history_canonical(capture.placement_ids,capture.placement_change_ids,capture.placement_principal_id);
 IF suffix IS NULL THEN RETURN QUERY SELECT original.contract,original.postcondition_sha256,original.membership_count,original.variation_count,original.canonical_size;RETURN; END IF;
 IF original.canonical_size+octet_length(suffix)>16777216 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_placement_history_limit'; END IF;
 RETURN QUERY SELECT original.contract,digest(public.privacy_membership_history_frame(original.contract)||public.privacy_membership_history_frame(encode(original.postcondition_sha256,'hex'))||suffix,'sha256'),original.membership_count,original.variation_count,original.canonical_size+octet_length(suffix);
END $$;
CREATE FUNCTION public.guard_sealed_training_placement_history() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE placement_ref uuid;change_ref uuid;
BEGIN
 IF TG_TABLE_NAME='training_group_placements' THEN placement_ref:=CASE WHEN TG_OP='INSERT' THEN NEW.id ELSE OLD.id END;
 ELSIF TG_TABLE_NAME='training_placement_changes' THEN change_ref:=CASE WHEN TG_OP='INSERT' THEN NEW.id ELSE OLD.id END;
 ELSE change_ref:=CASE WHEN TG_OP='INSERT' THEN NEW.change_id ELSE OLD.change_id END; END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended('mycfc:privacy-membership-history:'||id::text,0)) FROM(SELECT DISTINCT id FROM unnest(ARRAY[placement_ref,change_ref]) id WHERE id IS NOT NULL ORDER BY id) locked;
 IF EXISTS(SELECT 1 FROM privacy_protected.membership_history_source_captures capture JOIN privacy_protected.membership_history_source_postconditions postcondition USING(execution_id)
 WHERE placement_ref=ANY(capture.placement_ids) OR change_ref=ANY(capture.placement_change_ids)) THEN
 -- Actor unlink and live-prescription detach are normalized out of the canonical digest;
 -- separate invoker guards must prove their actual privileged operation.
 IF TG_OP='UPDATE' AND TG_TABLE_NAME='training_placement_changes' AND (to_jsonb(NEW)-'actor_user_id'-'actor_principal_id')=(to_jsonb(OLD)-'actor_user_id'-'actor_principal_id') THEN RETURN NEW; END IF;
 IF TG_OP='UPDATE' AND TG_TABLE_NAME='training_prescription_withdrawals' AND (to_jsonb(NEW)-'live_prescription_id')=(to_jsonb(OLD)-'live_prescription_id') THEN RETURN NEW; END IF;
 RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='sealed_membership_history_immutable'; END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;RETURN NEW;
END $$;
CREATE TRIGGER training_group_placements_sealed_history BEFORE INSERT OR UPDATE OR DELETE ON public.training_group_placements FOR EACH ROW EXECUTE FUNCTION public.guard_sealed_training_placement_history();
CREATE TRIGGER training_placement_changes_sealed_history BEFORE INSERT OR UPDATE OR DELETE ON public.training_placement_changes FOR EACH ROW EXECUTE FUNCTION public.guard_sealed_training_placement_history();
CREATE TRIGGER training_prescription_withdrawals_sealed_history BEFORE INSERT OR UPDATE OR DELETE ON public.training_prescription_withdrawals FOR EACH ROW EXECUTE FUNCTION public.guard_sealed_training_placement_history();

CREATE OR REPLACE FUNCTION public.privacy_membership_history_lock_source(p_execution_id uuid)
RETURNS void LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE membership_ref uuid;
BEGIN
 FOR membership_ref IN
  SELECT DISTINCT source_row.id FROM(SELECT membership_id AS id FROM privacy_protected.membership_history_source_rows WHERE execution_id=p_execution_id
  UNION ALL SELECT unnest(placement_ids) FROM privacy_protected.membership_history_source_captures WHERE execution_id=p_execution_id
  UNION ALL SELECT unnest(placement_change_ids) FROM privacy_protected.membership_history_source_captures WHERE execution_id=p_execution_id) source_row ORDER BY source_row.id
 LOOP
  PERFORM pg_advisory_xact_lock(hashtextextended('mycfc:privacy-membership-history:'||membership_ref::text,0));
 END LOOP;
END;$$;

CREATE OR REPLACE FUNCTION privacy_retention_pseudonymize_audit(p_batch_limit integer)
RETURNS integer LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE audit_row record; subject_ref uuid; subject_refs uuid[]; principal_ref uuid;
 subject_name text; subject_email text; subject_login text; subject_erased timestamptz;
 changed integer:=0; cutoff timestamptz:=clock_timestamp()-interval '24 months';
BEGIN
 IF p_batch_limit NOT BETWEEN 1 AND 10000 THEN RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='privacy retention input rejected'; END IF;
 FOR audit_row IN
  SELECT kind,id,occurred_at FROM (
   SELECT 'MINOR'::text kind,id,created_at occurred_at FROM minor_credential_audit WHERE created_at<=cutoff AND (minor_user_id IS NOT NULL OR guardian_user_id IS NOT NULL OR actor_user_id IS NOT NULL)
   UNION ALL SELECT 'EQUIPMENT',id,occurred_at FROM equipment_audit_events WHERE occurred_at<=cutoff AND actor_user_id IS NOT NULL
   UNION ALL SELECT 'PROFILE',id,occurred_at FROM member_profile_audit_events WHERE occurred_at<=cutoff AND (actor_user_id IS NOT NULL OR subject_user_id IS NOT NULL)
   UNION ALL SELECT 'STAFF',id,occurred_at FROM staff_grant_audit_events WHERE occurred_at<=cutoff AND actor_user_id IS NOT NULL
   UNION ALL SELECT 'ALBUM',id,occurred_at FROM photo_album_audit_events WHERE occurred_at<=cutoff AND actor_user_id IS NOT NULL
   UNION ALL SELECT 'ANNOUNCEMENT',id,occurred_at FROM announcement_audit_events WHERE occurred_at<=cutoff AND actor_user_id IS NOT NULL
   UNION ALL SELECT 'FEATURE',id,occurred_at FROM feature_flag_events WHERE occurred_at<=cutoff AND actor_user_id IS NOT NULL
   UNION ALL SELECT 'TRAINING_COPY',id,copied_at FROM training_copy_events WHERE copied_at<=cutoff AND copied_by_id IS NOT NULL
   UNION ALL SELECT 'PLACEMENT',id,recorded_at FROM training_placement_changes WHERE recorded_at<=cutoff AND actor_user_id IS NOT NULL
  ) due ORDER BY occurred_at,kind,id LIMIT p_batch_limit
 LOOP
  CASE audit_row.kind
  WHEN 'MINOR' THEN SELECT ARRAY(SELECT DISTINCT x FROM unnest(ARRAY[minor_user_id,guardian_user_id,actor_user_id]) x WHERE x IS NOT NULL) INTO subject_refs FROM minor_credential_audit WHERE id=audit_row.id FOR UPDATE;
  WHEN 'PROFILE' THEN SELECT ARRAY(SELECT DISTINCT x FROM unnest(ARRAY[actor_user_id,subject_user_id]) x WHERE x IS NOT NULL) INTO subject_refs FROM member_profile_audit_events WHERE id=audit_row.id FOR UPDATE;
  WHEN 'EQUIPMENT' THEN SELECT ARRAY[actor_user_id] INTO subject_refs FROM equipment_audit_events WHERE id=audit_row.id FOR UPDATE;
  WHEN 'STAFF' THEN SELECT ARRAY[actor_user_id] INTO subject_refs FROM staff_grant_audit_events WHERE id=audit_row.id FOR UPDATE;
  WHEN 'ALBUM' THEN SELECT ARRAY[actor_user_id] INTO subject_refs FROM photo_album_audit_events WHERE id=audit_row.id FOR UPDATE;
  WHEN 'ANNOUNCEMENT' THEN SELECT ARRAY[actor_user_id] INTO subject_refs FROM announcement_audit_events WHERE id=audit_row.id FOR UPDATE;
  WHEN 'FEATURE' THEN SELECT ARRAY[actor_user_id] INTO subject_refs FROM feature_flag_events WHERE id=audit_row.id FOR UPDATE;
  WHEN 'TRAINING_COPY' THEN SELECT ARRAY[copied_by_id] INTO subject_refs FROM training_copy_events WHERE id=audit_row.id FOR UPDATE;
  WHEN 'PLACEMENT' THEN SELECT ARRAY[actor_user_id] INTO subject_refs FROM training_placement_changes WHERE id=audit_row.id FOR UPDATE;
  END CASE;
  FOREACH subject_ref IN ARRAY subject_refs LOOP
   SELECT name,email::text,minor_login_id::text,erased_at INTO subject_name,subject_email,subject_login,subject_erased FROM users WHERE id=subject_ref;
   IF NOT FOUND OR (subject_erased IS NOT NULL AND audit_row.kind IN ('EQUIPMENT','STAFF')) THEN
    RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='privacy_audit_identity_unavailable';
   END IF;
   INSERT INTO privacy_pseudonymous_principals(purpose) VALUES('AUDIT') RETURNING id INTO principal_ref;
   INSERT INTO privacy_protected.training_placement_erasure_fence(transaction_id,subject_user_id,operation,effective_date,principal_id) VALUES(txid_current(),subject_ref,'AUDIT_ACTOR_ANONYMIZE',(clock_timestamp() AT TIME ZONE 'UTC')::date,principal_ref);
   PERFORM set_config('mycfc.privacy_erasure_operation','AUDIT_ACTOR_ANONYMIZE',true);
   CASE audit_row.kind
   WHEN 'MINOR' THEN UPDATE minor_credential_audit SET
     minor_principal_id=CASE WHEN minor_user_id=subject_ref THEN principal_ref ELSE minor_principal_id END,
     minor_user_id=CASE WHEN minor_user_id=subject_ref THEN NULL ELSE minor_user_id END,
     guardian_principal_id=CASE WHEN guardian_user_id=subject_ref THEN principal_ref ELSE guardian_principal_id END,
     guardian_user_id=CASE WHEN guardian_user_id=subject_ref THEN NULL ELSE guardian_user_id END,
     actor_principal_id=CASE WHEN actor_user_id=subject_ref THEN principal_ref ELSE actor_principal_id END,
     actor_user_id=CASE WHEN actor_user_id=subject_ref THEN NULL ELSE actor_user_id END,
     issued_login_id=CASE WHEN minor_user_id=subject_ref THEN 'anonymised' ELSE privacy_scrub_audit_text(issued_login_id::text,subject_ref,subject_name,subject_email,subject_login) END
    WHERE id=audit_row.id;
   WHEN 'EQUIPMENT' THEN UPDATE equipment_audit_events SET actor_user_id=NULL,actor_principal_id=principal_ref,
     before_state=privacy_scrub_audit_json(before_state,subject_ref,subject_name,subject_email,subject_login),
     after_state=privacy_scrub_audit_json(after_state,subject_ref,subject_name,subject_email,subject_login) WHERE id=audit_row.id AND actor_user_id=subject_ref;
   WHEN 'PROFILE' THEN UPDATE member_profile_audit_events SET
     actor_principal_id=CASE WHEN actor_user_id=subject_ref THEN principal_ref ELSE actor_principal_id END,
     actor_user_id=CASE WHEN actor_user_id=subject_ref THEN NULL ELSE actor_user_id END,
     subject_principal_id=CASE WHEN subject_user_id=subject_ref THEN principal_ref ELSE subject_principal_id END,
     subject_user_id=CASE WHEN subject_user_id=subject_ref THEN NULL ELSE subject_user_id END WHERE id=audit_row.id;
   WHEN 'STAFF' THEN UPDATE staff_grant_audit_events SET actor_user_id=NULL,actor_principal_id=principal_ref,
     reason=privacy_scrub_audit_text(reason,subject_ref,subject_name,subject_email,subject_login) WHERE id=audit_row.id AND actor_user_id=subject_ref;
   WHEN 'ALBUM' THEN UPDATE photo_album_audit_events SET actor_user_id=NULL,actor_principal_id=principal_ref WHERE id=audit_row.id AND actor_user_id=subject_ref;
   WHEN 'ANNOUNCEMENT' THEN UPDATE announcement_audit_events SET actor_user_id=NULL,actor_principal_id=principal_ref WHERE id=audit_row.id AND actor_user_id=subject_ref;
   WHEN 'FEATURE' THEN UPDATE feature_flag_events SET actor_user_id=NULL,actor_principal_id=principal_ref WHERE id=audit_row.id AND actor_user_id=subject_ref;
   WHEN 'TRAINING_COPY' THEN UPDATE training_copy_events SET copied_by_id=NULL,copied_by_principal_id=principal_ref WHERE id=audit_row.id AND copied_by_id=subject_ref;
   WHEN 'PLACEMENT' THEN UPDATE training_placement_changes SET actor_user_id=NULL,actor_principal_id=principal_ref WHERE id=audit_row.id AND actor_user_id=subject_ref;
   END CASE;
   DELETE FROM privacy_protected.training_placement_erasure_fence WHERE transaction_id=txid_current() AND subject_user_id=subject_ref AND operation='AUDIT_ACTOR_ANONYMIZE';
  END LOOP;
  changed:=changed+1;
 END LOOP;
 RETURN changed;
END; $$;

CREATE FUNCTION public.detach_training_placement_withdrawal_live_reference() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
 IF public.training_placement_privileged_operation('TRAINING_PRESCRIPTION_DELETE',OLD.athlete_user_id) THEN
 UPDATE public.training_prescription_withdrawals SET live_prescription_id=NULL WHERE live_prescription_id=OLD.id;
 END IF;
 RETURN OLD;
END $$;
CREATE TRIGGER training_prescriptions_detach_placement_withdrawals BEFORE DELETE ON public.training_prescriptions FOR EACH ROW EXECUTE FUNCTION public.detach_training_placement_withdrawal_live_reference();

-- Row locking needs UPDATE privilege in PostgreSQL. Keep ordinary web tables
-- read-only and expose only the scoped publication head-lock operation.
CREATE FUNCTION public.lock_training_publication_placement_heads(p_actor uuid,p_plan uuid)
RETURNS SETOF public.training_placement_heads LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
 RETURN QUERY WITH scoped AS MATERIALIZED (
 SELECT plan.id,group_row.id AS group_id FROM public.training_plans plan JOIN public.training_groups group_row ON group_row.id=plan.training_group_id
 WHERE plan.id=p_plan AND public.training_placement_actor_scope(p_actor,group_row.id)
 ),needed AS MATERIALIZED (
 SELECT DISTINCT placement.subject_user_id,placement.programme_id
 FROM scoped JOIN public.training_group_placements placement ON placement.group_id=scoped.group_id JOIN public.training_sessions session ON session.plan_id=scoped.id
 WHERE placement.subject_user_id IS NOT NULL AND placement.superseded_by_change_id IS NULL AND placement.cancelled_by_change_id IS NULL
 AND public.training_session_local_date(session.scheduled_on,session.starts_at) BETWEEN placement.starts_on AND COALESCE(placement.ends_on,'infinity'::date)
 ORDER BY placement.subject_user_id,placement.programme_id LIMIT 4097
 ) SELECT head.* FROM public.training_placement_heads head JOIN needed ON needed.subject_user_id=head.subject_user_id AND needed.programme_id=head.programme_id
 ORDER BY head.subject_user_id,head.programme_id FOR SHARE OF head NOWAIT;
END $$;

CREATE FUNCTION public.guard_training_placement_team_scope() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog,public AS $$
BEGIN
 IF(NEW.programme_id IS DISTINCT FROM OLD.programme_id OR NEW.season_id IS DISTINCT FROM OLD.season_id)
 AND EXISTS(SELECT 1 FROM public.training_groups group_row JOIN public.training_group_placements p ON p.group_id=group_row.id WHERE group_row.team_id=OLD.id) THEN
 RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='training_placement_team_scope_immutable'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER teams_placement_scope_guard BEFORE UPDATE OF programme_id,season_id ON public.teams FOR EACH ROW EXECUTE FUNCTION public.guard_training_placement_team_scope();


CREATE FUNCTION public.lock_training_placement_existing_heads_for_join(p_actor uuid,p_group uuid,p_subjects uuid[])
RETURNS SETOF public.training_placement_heads LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
BEGIN
 IF p_subjects IS NULL OR cardinality(p_subjects)<1 OR cardinality(p_subjects)>20 OR array_position(p_subjects,NULL) IS NOT NULL
 OR cardinality(p_subjects)<>(SELECT count(DISTINCT id) FROM unnest(p_subjects) id) THEN
 RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='training_placement_bulk_subjects_invalid'; END IF;
 IF NOT public.training_placement_actor_scope(p_actor,p_group) THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='training_placement_forbidden'; END IF;
 RETURN QUERY SELECT head.* FROM public.training_placement_heads head
 WHERE head.subject_user_id=ANY(p_subjects) AND head.programme_id=public.training_placement_group_programme(p_group)
 ORDER BY head.subject_user_id,head.programme_id FOR UPDATE OF head NOWAIT;
END $$;
