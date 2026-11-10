-- Stage C compatibility only. No row backfill and no nullable cutover.
ALTER TABLE public.training_sessions
 ADD COLUMN schedule_precision text NOT NULL DEFAULT 'INTERVAL',
 ADD COLUMN scheduled_on date NULL,
 ADD COLUMN day_band text NULL;

CREATE FUNCTION public.training_session_local_date(p_date date,p_start timestamptz)
RETURNS date LANGUAGE sql IMMUTABLE PARALLEL SAFE SET search_path=pg_catalog AS $$
 SELECT COALESCE(p_date,(p_start AT TIME ZONE 'Europe/Lisbon')::date)
$$;

CREATE FUNCTION public.training_session_schedule_valid(p_precision text,p_date date,p_start timestamptz,p_end timestamptz,p_band text,p_kind public.training_entry_kind)
RETURNS boolean LANGUAGE sql IMMUTABLE PARALLEL SAFE SET search_path=pg_catalog AS $$
 SELECT p_kind IS NOT NULL AND COALESCE(CASE p_precision
 WHEN 'INTERVAL' THEN p_start IS NOT NULL AND p_end IS NOT NULL AND p_start<p_end AND p_band IS NULL
  AND (p_date IS NULL OR p_date=(p_start AT TIME ZONE 'Europe/Lisbon')::date)
 WHEN 'START_ONLY' THEN p_date IS NOT NULL AND p_start IS NOT NULL AND p_end IS NULL AND p_band IS NULL
  AND p_date=(p_start AT TIME ZONE 'Europe/Lisbon')::date
 WHEN 'DAY' THEN p_date IS NOT NULL AND p_start IS NULL AND p_end IS NULL AND p_band IS NULL
  AND p_kind IN ('REST','LOGISTICS')
 WHEN 'BAND' THEN p_date IS NOT NULL AND p_start IS NULL AND p_end IS NULL AND p_band IN ('MORNING','AFTERNOON')
  AND p_kind IN ('REST','LOGISTICS')
 ELSE false END,false)
$$;

ALTER TABLE public.training_sessions
 ADD CONSTRAINT training_sessions_schedule_shape_valid CHECK(public.training_session_schedule_valid(schedule_precision,scheduled_on,starts_at,ends_at,day_band,entry_kind)),
 ADD CONSTRAINT training_sessions_schedule_compatibility_only CHECK(schedule_precision='INTERVAL');

CREATE SCHEMA training_schedule_control;
REVOKE ALL ON SCHEMA training_schedule_control FROM PUBLIC;
-- A schema-local REVOKE cannot subtract PostgreSQL's global PUBLIC execute
-- default. Future migration-owned functions therefore require explicit grants.
ALTER DEFAULT PRIVILEGES REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC;
ALTER DEFAULT PRIVILEGES IN SCHEMA training_schedule_control REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC;
CREATE TABLE training_schedule_control.state (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
 database_incarnation uuid NOT NULL DEFAULT gen_random_uuid(),
 schema_contract text NOT NULL DEFAULT 'schedule-c1' CHECK(schema_contract IN ('schedule-c1','schedule-p1')),
 state_epoch bigint NOT NULL DEFAULT 0 CHECK(state_epoch>=0),
 required_reader integer NOT NULL DEFAULT 1 CHECK(required_reader>=1),
 floor_commit_id uuid NULL,
 floor_manifest_sha256 text NULL CHECK(floor_manifest_sha256 IS NULL OR floor_manifest_sha256~'^[0-9a-f]{64}$'),
 partial_v2_enabled boolean NOT NULL DEFAULT false,
 ever_partial_v2_enabled boolean NOT NULL DEFAULT false,
 changed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CONSTRAINT training_schedule_floor_complete CHECK((floor_commit_id IS NULL)=(floor_manifest_sha256 IS NULL)),
 CONSTRAINT training_schedule_reader_floor CHECK(required_reader=1 OR floor_commit_id IS NOT NULL),
 CONSTRAINT training_schedule_enable_valid CHECK(NOT partial_v2_enabled OR
  (schema_contract='schedule-p1' AND required_reader>=2 AND floor_commit_id IS NOT NULL AND ever_partial_v2_enabled)),
 CONSTRAINT training_schedule_ever_floor CHECK(NOT ever_partial_v2_enabled OR required_reader>=2)
);
INSERT INTO training_schedule_control.state(singleton) VALUES(true);
REVOKE ALL ON TABLE training_schedule_control.state FROM PUBLIC;

CREATE FUNCTION training_schedule_control.status()
RETURNS TABLE(database_incarnation uuid,schema_contract text,state_epoch bigint,required_reader integer,
 floor_commit_id uuid,floor_manifest_sha256 text,partial_v2_enabled boolean,ever_partial_v2_enabled boolean)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT s.database_incarnation,s.schema_contract,s.state_epoch,s.required_reader,s.floor_commit_id,
  s.floor_manifest_sha256,s.partial_v2_enabled,s.ever_partial_v2_enabled
 FROM training_schedule_control.state s WHERE s.singleton
$$;

-- Supported gated writers call this FIRST, before application locks. Direct SQL
-- trigger calls may arrive late and abort on a deadlock; never bypass admission.
CREATE FUNCTION training_schedule_control.admit_partial(p_incarnation uuid DEFAULT NULL,p_epoch bigint DEFAULT NULL)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE s training_schedule_control.state%ROWTYPE;
BEGIN
 SELECT * INTO STRICT s FROM training_schedule_control.state WHERE singleton FOR SHARE;
 IF NOT s.partial_v2_enabled OR s.schema_contract<>'schedule-p1' OR s.required_reader<2
  OR s.floor_commit_id IS NULL OR p_incarnation IS NOT NULL AND p_incarnation<>s.database_incarnation
  OR p_epoch IS NOT NULL AND p_epoch<>s.state_epoch THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='training_schedule_writes_disabled';
 END IF;
END $$;

-- Offline migration owner only. Host drain/floor proof is external and must
-- precede these state-only transactions; copied DB fields are not restore proof.
CREATE FUNCTION training_schedule_control.commit_floor(p_incarnation uuid,p_epoch bigint,p_floor uuid,p_digest text,p_reader integer DEFAULT 2)
RETURNS bigint LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE s training_schedule_control.state%ROWTYPE;
BEGIN
 SELECT * INTO STRICT s FROM training_schedule_control.state WHERE singleton FOR UPDATE;
 IF p_incarnation IS DISTINCT FROM s.database_incarnation OR p_epoch IS DISTINCT FROM s.state_epoch
  OR p_floor IS NULL OR p_digest IS NULL OR p_digest!~'^[0-9a-f]{64}$' OR p_reader IS NULL OR p_reader<2 OR p_reader<s.required_reader THEN
  RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='training_schedule_state_changed';
 END IF;
 UPDATE training_schedule_control.state SET required_reader=p_reader,floor_commit_id=p_floor,floor_manifest_sha256=p_digest,
  state_epoch=state_epoch+1,changed_at=clock_timestamp() WHERE singleton RETURNING state_epoch INTO p_epoch;
 RETURN p_epoch;
END $$;

CREATE FUNCTION training_schedule_control.set_enabled(p_incarnation uuid,p_epoch bigint,p_enabled boolean)
RETURNS bigint LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE s training_schedule_control.state%ROWTYPE;
BEGIN
 SELECT * INTO STRICT s FROM training_schedule_control.state WHERE singleton FOR UPDATE;
 IF p_incarnation IS DISTINCT FROM s.database_incarnation OR p_epoch IS DISTINCT FROM s.state_epoch OR p_enabled IS NULL THEN
  RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='training_schedule_state_changed';
 END IF;
 IF p_enabled AND (s.schema_contract<>'schedule-p1' OR s.required_reader<2 OR s.floor_commit_id IS NULL) THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='training_schedule_writes_disabled';
 END IF;
 UPDATE training_schedule_control.state SET partial_v2_enabled=p_enabled,
  ever_partial_v2_enabled=ever_partial_v2_enabled OR p_enabled,
  state_epoch=state_epoch+1,changed_at=clock_timestamp() WHERE singleton RETURNING state_epoch INTO p_epoch;
 RETURN p_epoch;
END $$;

CREATE FUNCTION training_schedule_control.guard_session()
RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE plan_row public.training_plans%ROWTYPE; d date;
BEGIN
 IF TG_OP='DELETE' THEN
  IF OLD.schedule_precision<>'INTERVAL' THEN PERFORM training_schedule_control.admit_partial(); END IF;
  RETURN OLD;
 END IF;
 -- Legacy exact paths do not acquire a late state lock. This short circuit is
 -- essential to the supported writer/operator lock-order proof.
 IF NEW.schedule_precision<>'INTERVAL' OR TG_OP='UPDATE' AND OLD.schedule_precision<>'INTERVAL' THEN
  PERFORM training_schedule_control.admit_partial();
 END IF;
 IF NOT public.training_session_schedule_valid(NEW.schedule_precision,NEW.scheduled_on,NEW.starts_at,NEW.ends_at,NEW.day_band,NEW.entry_kind) THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='training_sessions_schedule_shape_valid',MESSAGE='training_session_schedule_invalid';
 END IF;
 SELECT * INTO STRICT plan_row FROM public.training_plans WHERE id=NEW.plan_id;
 IF plan_row.training_group_id IS NULL THEN
  IF NEW.schedule_precision<>'INTERVAL' THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='training_session_schedule_invalid'; END IF;
 ELSE
  d:=public.training_session_local_date(NEW.scheduled_on,NEW.starts_at);
  IF d<plan_row.week_start OR d>=plan_row.week_start+7 OR
   NEW.schedule_precision='INTERVAL' AND
    (NEW.starts_at<(plan_row.week_start::timestamp AT TIME ZONE 'Europe/Lisbon') OR
     NEW.ends_at>((plan_row.week_start+7)::timestamp AT TIME ZONE 'Europe/Lisbon')) THEN
   RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='training_session_outside_week';
  END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER training_sessions_schedule_guard BEFORE INSERT OR UPDATE OR DELETE ON public.training_sessions
 FOR EACH ROW EXECUTE FUNCTION training_schedule_control.guard_session();

CREATE FUNCTION public.training_schedule_artifact_version(p_snapshot jsonb,p_prescription boolean)
RETURNS integer LANGUAGE plpgsql IMMUTABLE SET search_path=pg_catalog AS $$
DECLARE version integer; schedule jsonb; d date; start_at timestamptz; end_at timestamptz; kind public.training_entry_kind; key text;
BEGIN
 IF jsonb_typeof(p_snapshot) IS DISTINCT FROM 'object' THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='training_schedule_artifact_invalid'; END IF;
 IF NOT p_snapshot ? 'schema_version' THEN
  IF p_prescription OR p_snapshot ? 'schedule' THEN RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='training_schedule_artifact_invalid'; END IF;
  RETURN 1;
 END IF;
 IF jsonb_typeof(p_snapshot->'schema_version') IS DISTINCT FROM 'number' OR p_snapshot->>'schema_version' NOT IN ('1','2') THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='training_schedule_write_unsupported'; END IF;
 version:=(p_snapshot->>'schema_version')::integer;
 IF version=1 THEN
  IF p_snapshot ? 'schedule' OR (p_snapshot->'session') ? 'schedule' THEN
   RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='training_schedule_artifact_invalid'; END IF;
  RETURN 1;
 END IF;
 IF octet_length(p_snapshot::text)>200000 THEN RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='training_schedule_artifact_invalid'; END IF;
 schedule:=CASE WHEN p_prescription THEN p_snapshot->'session'->'schedule' ELSE p_snapshot->'schedule' END;
 IF jsonb_typeof(schedule) IS DISTINCT FROM 'object' OR NOT schedule ?& ARRAY['precision','date','starts_at','ends_at','band'] THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='training_schedule_artifact_invalid'; END IF;
 FOR key IN SELECT jsonb_object_keys(schedule) LOOP
  IF key NOT IN ('precision','date','starts_at','ends_at','band') THEN RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='training_schedule_artifact_invalid'; END IF;
 END LOOP;
 IF jsonb_typeof(schedule->'precision') IS DISTINCT FROM 'string' OR jsonb_typeof(schedule->'date') IS DISTINCT FROM 'string'
  OR (schedule->>'date')!~'^[0-9]{4}-[0-9]{2}-[0-9]{2}$'
  OR jsonb_typeof(schedule->'starts_at') NOT IN ('null','string') OR jsonb_typeof(schedule->'ends_at') NOT IN ('null','string')
  OR jsonb_typeof(schedule->'band') NOT IN ('null','string') THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='training_schedule_artifact_invalid'; END IF;
 d:=(schedule->>'date')::date;
 IF schedule->>'starts_at' IS NOT NULL AND (schedule->>'starts_at')!~'T.*(Z|[+-][0-9]{2}:[0-9]{2})$'
  OR schedule->>'ends_at' IS NOT NULL AND (schedule->>'ends_at')!~'T.*(Z|[+-][0-9]{2}:[0-9]{2})$' THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='training_schedule_artifact_invalid'; END IF;
 start_at:=(schedule->>'starts_at')::timestamptz;end_at:=(schedule->>'ends_at')::timestamptz;
 IF p_prescription AND (((p_snapshot->'session') ? 'entry_kind')=((p_snapshot->'session') ? 'EntryKind'))
  OR NOT p_prescription AND p_snapshot ? 'EntryKind' THEN RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='training_schedule_artifact_invalid'; END IF;
 kind:=CASE WHEN p_prescription THEN COALESCE(p_snapshot->'session'->>'entry_kind',p_snapshot->'session'->>'EntryKind') ELSE p_snapshot->>'entry_kind' END;
 IF kind IS NULL THEN RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='training_schedule_artifact_invalid'; END IF;
 IF NOT public.training_session_schedule_valid(schedule->>'precision',d,start_at,end_at,schedule->>'band',kind) THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='training_schedule_artifact_invalid'; END IF;
 RETURN 2;
EXCEPTION WHEN invalid_datetime_format OR datetime_field_overflow OR invalid_text_representation THEN
 RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='training_schedule_artifact_invalid';
END $$;

CREATE FUNCTION training_schedule_control.guard_artifact()
RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE version integer; precision text;
BEGIN
 version:=public.training_schedule_artifact_version(NEW.snapshot,TG_TABLE_NAME='training_prescriptions');
 IF version=2 THEN PERFORM training_schedule_control.admit_partial(); END IF;
 IF TG_TABLE_NAME='training_prescriptions' THEN
  SELECT schedule_precision INTO STRICT precision FROM public.training_sessions WHERE id=NEW.session_id;
  IF precision<>'INTERVAL' AND version<>2 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='training_schedule_write_unsupported'; END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER training_prescriptions_schedule_guard BEFORE INSERT ON public.training_prescriptions
 FOR EACH ROW EXECUTE FUNCTION training_schedule_control.guard_artifact();
CREATE TRIGGER training_routines_schedule_guard BEFORE INSERT OR UPDATE OF snapshot ON public.training_routines
 FOR EACH ROW EXECUTE FUNCTION training_schedule_control.guard_artifact();

CREATE FUNCTION training_schedule_control.guard_publication()
RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM public.training_sessions WHERE plan_id=NEW.plan_id AND schedule_precision<>'INTERVAL') THEN
  PERFORM training_schedule_control.admit_partial();
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER training_plan_publications_schedule_guard BEFORE INSERT ON public.training_plan_publications
 FOR EACH ROW EXECUTE FUNCTION training_schedule_control.guard_publication();

CREATE OR REPLACE FUNCTION public.training_session_snapshot(p_session_id uuid) RETURNS jsonb LANGUAGE plpgsql STABLE SET search_path=pg_catalog,public AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM public.training_sessions WHERE id=p_session_id AND schedule_precision<>'INTERVAL') THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='training_schedule_write_unsupported'; END IF;
 RETURN (SELECT jsonb_build_object('title', session.title, 'description', session.description, 'entry_kind', session.entry_kind::text, 'duration_seconds', extract(epoch FROM session.ends_at - session.starts_at)::integer, 'segments', COALESCE((SELECT jsonb_agg(training_segment_snapshot(segment.id) ORDER BY segment.position) FROM training_session_segments segment WHERE segment.session_id = session.id), '[]'::jsonb)) FROM training_sessions session WHERE session.id = p_session_id AND session.status = 'ACTIVE');
END $$;
CREATE OR REPLACE FUNCTION public.restore_training_session(p_snapshot jsonb, p_plan_id uuid, p_starts_at timestamptz, p_created_by_id uuid) RETURNS uuid LANGUAGE plpgsql SET search_path=pg_catalog,public AS $$ DECLARE new_session_id uuid; segment jsonb; BEGIN IF public.training_schedule_artifact_version(p_snapshot,false)<>1 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='training_schedule_write_unsupported'; END IF; INSERT INTO training_sessions (plan_id, title, description, starts_at, ends_at, entry_kind, created_by_id) SELECT plan.id, p_snapshot->>'title', COALESCE(p_snapshot->>'description', ''), p_starts_at, p_starts_at + ((p_snapshot->>'duration_seconds')::integer * interval '1 second'), (p_snapshot->>'entry_kind')::training_entry_kind, p_created_by_id FROM training_plans plan WHERE plan.id = p_plan_id AND plan.training_group_id IS NOT NULL AND p_starts_at >= (plan.week_start::timestamp AT TIME ZONE 'Europe/Lisbon') AND p_starts_at + ((p_snapshot->>'duration_seconds')::integer * interval '1 second') <= ((plan.week_start + 7)::timestamp AT TIME ZONE 'Europe/Lisbon') RETURNING id INTO new_session_id; IF new_session_id IS NULL THEN RAISE NO_DATA_FOUND; END IF; FOR segment IN SELECT value FROM jsonb_array_elements(COALESCE(p_snapshot->'segments', '[]'::jsonb)) LOOP PERFORM restore_training_segment(segment, new_session_id); END LOOP; RETURN new_session_id; END $$;

CREATE FUNCTION public.training_session_snapshot_v2(p_session_id uuid) RETURNS jsonb LANGUAGE sql STABLE SET search_path=pg_catalog,public AS $$
 SELECT jsonb_build_object('schema_version',2,'title',session.title,'description',session.description,'entry_kind',session.entry_kind::text,
  'schedule',jsonb_build_object('precision',session.schedule_precision,
   'date',public.training_session_local_date(session.scheduled_on,session.starts_at)::text,
   'starts_at',to_char(session.starts_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
   'ends_at',to_char(session.ends_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),'band',session.day_band),
  'segments',COALESCE((SELECT jsonb_agg(public.training_segment_snapshot(segment.id) ORDER BY segment.position)
   FROM public.training_session_segments segment WHERE segment.session_id=session.id),'[]'::jsonb))
 FROM public.training_sessions session WHERE session.id=p_session_id AND session.status='ACTIVE'
$$;

REVOKE ALL ON ALL FUNCTIONS IN SCHEMA training_schedule_control FROM PUBLIC;

-- Keep recorded-history protections when final readers encounter partial times.
CREATE OR REPLACE FUNCTION public.prevent_dated_participation_rewrite() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public,pg_temp AS $$
BEGIN
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
 IF NEW.ends_on < OLD.ends_on THEN
  IF EXISTS (SELECT 1 FROM training_prescriptions prescription
   JOIN training_sessions session ON session.id=prescription.session_id
   WHERE prescription.membership_id=OLD.id
    AND public.training_session_local_date(session.scheduled_on,session.starts_at)>NEW.ends_on)
   OR EXISTS (SELECT 1 FROM event_responses response
    JOIN events event ON event.id=response.event_id
    WHERE response.user_id=OLD.user_id
     AND (event.starts_at AT TIME ZONE 'Europe/Lisbon')::date BETWEEN OLD.starts_on AND OLD.ends_on
     AND (event.starts_at AT TIME ZONE 'Europe/Lisbon')::date>NEW.ends_on
     AND (NOT EXISTS (SELECT 1 FROM event_audiences audience WHERE audience.event_id=event.id)
       OR EXISTS (SELECT 1 FROM event_audiences audience WHERE audience.event_id=event.id AND audience.programme_id=OLD.programme_id)))
   OR EXISTS (SELECT 1 FROM training_session_outcomes outcome
    JOIN training_sessions session ON session.id=outcome.session_id
    JOIN training_plans plan ON plan.id=session.plan_id
    JOIN training_group_members group_member ON group_member.group_id=plan.training_group_id
    WHERE group_member.membership_id=OLD.id AND outcome.user_id=OLD.user_id
     AND public.training_session_local_date(session.scheduled_on,session.starts_at) BETWEEN OLD.starts_on AND OLD.ends_on
     AND public.training_session_local_date(session.scheduled_on,session.starts_at)>NEW.ends_on) THEN
   RAISE EXCEPTION 'recorded event, training or published prescription date requires audited correction'
    USING ERRCODE='23514', CONSTRAINT='user_memberships_recorded_history_end';
  END IF;
 END IF;
 RETURN NEW;
END $$;

-- First-lock admission is exposed without control-table or activation grants.
CREATE FUNCTION public.training_schedule_admit_partial(p_incarnation uuid,p_epoch bigint)
RETURNS void LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT training_schedule_control.admit_partial(p_incarnation,p_epoch)
$$;
REVOKE ALL ON FUNCTION public.training_schedule_admit_partial(uuid,bigint) FROM PUBLIC;

-- C guards are pure and exact/v1 paths take no late state lock.
CREATE FUNCTION public.training_schedule_assert_plan_v1(p_plan_id uuid) RETURNS boolean
LANGUAGE plpgsql STABLE SET search_path=pg_catalog AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM public.training_sessions WHERE plan_id=p_plan_id AND schedule_precision<>'INTERVAL')
 OR EXISTS(SELECT 1 FROM public.training_prescriptions prescription JOIN public.training_plan_publications publication ON publication.id=prescription.publication_id
 WHERE publication.plan_id=p_plan_id AND publication.id=(SELECT latest.id FROM public.training_plan_publications latest WHERE latest.plan_id=p_plan_id ORDER BY latest.revision DESC LIMIT 1)
 AND prescription.snapshot->>'schema_version' IS DISTINCT FROM '1') THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='training_schedule_write_unsupported'; END IF;
 RETURN true;
END $$;
CREATE FUNCTION public.training_schedule_assert_session_v1(p_session_id uuid) RETURNS boolean
LANGUAGE plpgsql STABLE SET search_path=pg_catalog AS $$
DECLARE plan uuid; precision text;
BEGIN
 SELECT plan_id,schedule_precision INTO STRICT plan,precision FROM public.training_sessions WHERE id=p_session_id;
 IF precision<>'INTERVAL' THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='training_schedule_write_unsupported'; END IF;
 RETURN true;
END $$;
CREATE FUNCTION public.training_schedule_assert_routine_v1(p_routine_id uuid) RETURNS boolean
LANGUAGE plpgsql STABLE SET search_path=pg_catalog AS $$
DECLARE snapshot jsonb;
BEGIN
 SELECT r.snapshot INTO STRICT snapshot FROM public.training_routines r WHERE r.id=p_routine_id;
 IF public.training_schedule_artifact_version(snapshot,false)<>1 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='training_schedule_write_unsupported'; END IF;
 RETURN true;
END $$;

CREATE FUNCTION public.training_schedule_lock_target_v1(p_actor uuid,p_kind text,p_id uuid)
RETURNS TABLE(plan_id uuid) LANGUAGE plpgsql VOLATILE SET search_path=pg_catalog AS $$
DECLARE target_plan uuid;locked_plan uuid;target_session uuid;target_segment uuid;target_block uuid;
 variation_kind text;variation_subject uuid;effective_kind text;effective_id uuid;pass integer;
BEGIN
 IF p_kind NOT IN ('PLAN','SESSION','SEGMENT','BLOCK','EXERCISE','WATER_STEP','VARIATION') OR p_kind IS NULL THEN
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='training_schedule_target_invalid'; END IF;
 FOR pass IN 0..1 LOOP

  IF p_kind='PLAN' THEN
   SELECT p.id,NULL::uuid,NULL::uuid,NULL::uuid INTO target_plan,target_session,target_segment,target_block FROM public.training_plans p WHERE p.id=p_id;
  ELSIF p_kind='VARIATION' THEN
   SELECT v.plan_id,v.subject_kind::text,v.subject_id INTO target_plan,variation_kind,variation_subject FROM public.training_variations v WHERE v.id=p_id;
   target_session:=NULL;target_segment:=NULL;target_block:=NULL;
  ELSE
   SELECT target.plan_ref,target.session_ref,target.segment_ref,target.block_ref INTO target_plan,target_session,target_segment,target_block FROM (
    SELECT s.plan_id AS plan_ref,s.id AS session_ref,NULL::uuid AS segment_ref,NULL::uuid AS block_ref FROM public.training_sessions s WHERE p_kind='SESSION' AND s.id=p_id
    UNION ALL SELECT s.plan_id,s.id,seg.id,NULL::uuid FROM public.training_session_segments seg JOIN public.training_sessions s ON s.id=seg.session_id WHERE p_kind='SEGMENT' AND seg.id=p_id
    UNION ALL SELECT s.plan_id,s.id,seg.id,b.id FROM public.training_segment_blocks b JOIN public.training_session_segments seg ON seg.id=b.segment_id JOIN public.training_sessions s ON s.id=seg.session_id WHERE p_kind='BLOCK' AND b.id=p_id
    UNION ALL SELECT s.plan_id,s.id,seg.id,b.id FROM public.gym_exercises e JOIN public.training_segment_blocks b ON b.id=e.block_id JOIN public.training_session_segments seg ON seg.id=b.segment_id JOIN public.training_sessions s ON s.id=seg.session_id WHERE p_kind='EXERCISE' AND e.id=p_id
    UNION ALL SELECT s.plan_id,s.id,seg.id,b.id FROM public.water_work_steps w JOIN public.training_segment_blocks b ON b.id=w.block_id JOIN public.training_session_segments seg ON seg.id=b.segment_id JOIN public.training_sessions s ON s.id=seg.session_id WHERE p_kind='WATER_STEP' AND w.id=p_id
   ) target;
  END IF;

  IF target_plan IS NULL THEN
   IF pass=0 THEN RETURN; END IF;
   RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='training_schedule_target_changed';
  END IF;
  IF pass=0 THEN
   locked_plan:=target_plan;
   IF NOT
  EXISTS(SELECT 1 FROM public.users actor JOIN public.training_plans p ON p.id=locked_plan
   LEFT JOIN public.training_groups g ON g.id=p.training_group_id
   WHERE actor.id=p_actor AND actor.is_active AND actor.erased_at IS NULL AND NOT actor.is_dependent
    AND actor.date_of_birth<=((now() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date
    AND (EXISTS(SELECT 1 FROM public.user_platform_roles r JOIN public.platform_roles role ON role.id=r.role_id WHERE r.user_id=actor.id AND role.code='ADMIN')
     OR EXISTS(SELECT 1 FROM public.staff_grants grant_row WHERE grant_row.user_id=actor.id AND grant_row.capability='COACH' AND grant_row.revoked_at IS NULL
      AND ((CASE WHEN p.training_group_id IS NULL THEN p.programme_id ELSE g.programme_id END IS NOT NULL
        AND grant_row.programme_id=CASE WHEN p.training_group_id IS NULL THEN p.programme_id ELSE g.programme_id END)
       OR (CASE WHEN p.training_group_id IS NULL THEN p.team_id ELSE g.team_id END IS NOT NULL
        AND grant_row.team_id=CASE WHEN p.training_group_id IS NULL THEN p.team_id ELSE g.team_id END)))))
 THEN RETURN; END IF;
   PERFORM 1 FROM public.training_plans p WHERE p.id=locked_plan FOR UPDATE;
   IF NOT FOUND THEN RETURN; END IF;
  ELSIF target_plan<>locked_plan THEN
   RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='training_schedule_target_changed';
  END IF;
 END LOOP;
 IF p_kind='VARIATION' THEN
  effective_kind:=CASE variation_kind WHEN 'GYM_EXERCISE' THEN 'EXERCISE' ELSE variation_kind END;
  effective_id:=variation_subject;
  SELECT t.session_ref,t.segment_ref,t.block_ref INTO target_session,target_segment,target_block FROM (
   SELECT s.plan_id,s.id AS session_ref,seg.id AS segment_ref,NULL::uuid AS block_ref FROM public.training_session_segments seg JOIN public.training_sessions s ON s.id=seg.session_id WHERE effective_kind='SEGMENT' AND seg.id=effective_id
   UNION ALL SELECT s.plan_id,s.id,seg.id,b.id FROM public.training_segment_blocks b JOIN public.training_session_segments seg ON seg.id=b.segment_id JOIN public.training_sessions s ON s.id=seg.session_id WHERE effective_kind='BLOCK' AND b.id=effective_id
   UNION ALL SELECT s.plan_id,s.id,seg.id,b.id FROM public.gym_exercises e JOIN public.training_segment_blocks b ON b.id=e.block_id JOIN public.training_session_segments seg ON seg.id=b.segment_id JOIN public.training_sessions s ON s.id=seg.session_id WHERE effective_kind='EXERCISE' AND e.id=effective_id
   UNION ALL SELECT s.plan_id,s.id,seg.id,b.id FROM public.water_work_steps w JOIN public.training_segment_blocks b ON b.id=w.block_id JOIN public.training_session_segments seg ON seg.id=b.segment_id JOIN public.training_sessions s ON s.id=seg.session_id WHERE effective_kind='WATER_STEP' AND w.id=effective_id
  ) t WHERE t.plan_id=locked_plan;
  IF target_session IS NULL THEN RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='training_schedule_target_changed'; END IF;
 END IF;
 IF target_session IS NOT NULL THEN
  PERFORM 1 FROM public.training_sessions s WHERE s.id=target_session AND s.plan_id=locked_plan FOR UPDATE;
  IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='training_schedule_target_changed'; END IF;
  PERFORM public.training_schedule_assert_session_v1(target_session);
 END IF;
 IF target_segment IS NOT NULL THEN
  PERFORM 1 FROM public.training_session_segments seg WHERE seg.id=target_segment AND seg.session_id=target_session FOR UPDATE;
  IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='training_schedule_target_changed'; END IF;
 END IF;
 IF target_block IS NOT NULL THEN
  PERFORM 1 FROM public.training_segment_blocks b WHERE b.id=target_block AND b.segment_id=target_segment FOR UPDATE;
  IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='training_schedule_target_changed'; END IF;
 END IF;
 IF p_kind='EXERCISE' OR p_kind='VARIATION' AND effective_kind='EXERCISE' THEN
  PERFORM 1 FROM public.gym_exercises e WHERE e.id=CASE WHEN p_kind='VARIATION' THEN effective_id ELSE p_id END AND e.block_id=target_block FOR UPDATE;
  IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='training_schedule_target_changed'; END IF;
 END IF;
 IF p_kind='WATER_STEP' OR p_kind='VARIATION' AND effective_kind='WATER_STEP' THEN
  PERFORM 1 FROM public.water_work_steps w WHERE w.id=CASE WHEN p_kind='VARIATION' THEN effective_id ELSE p_id END AND w.block_id=target_block FOR UPDATE;
  IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='training_schedule_target_changed'; END IF;
 END IF;
 IF p_kind='VARIATION' THEN
  PERFORM 1 FROM public.training_variations v WHERE v.id=p_id AND v.plan_id=locked_plan AND v.subject_id=variation_subject AND v.subject_kind::text=variation_kind FOR UPDATE;
  IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='training_schedule_target_changed'; END IF;
 END IF;
 IF NOT
  EXISTS(SELECT 1 FROM public.users actor JOIN public.training_plans p ON p.id=locked_plan
   LEFT JOIN public.training_groups g ON g.id=p.training_group_id
   WHERE actor.id=p_actor AND actor.is_active AND actor.erased_at IS NULL AND NOT actor.is_dependent
    AND actor.date_of_birth<=((now() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date
    AND (EXISTS(SELECT 1 FROM public.user_platform_roles r JOIN public.platform_roles role ON role.id=r.role_id WHERE r.user_id=actor.id AND role.code='ADMIN')
     OR EXISTS(SELECT 1 FROM public.staff_grants grant_row WHERE grant_row.user_id=actor.id AND grant_row.capability='COACH' AND grant_row.revoked_at IS NULL
      AND ((CASE WHEN p.training_group_id IS NULL THEN p.programme_id ELSE g.programme_id END IS NOT NULL
        AND grant_row.programme_id=CASE WHEN p.training_group_id IS NULL THEN p.programme_id ELSE g.programme_id END)
       OR (CASE WHEN p.training_group_id IS NULL THEN p.team_id ELSE g.team_id END IS NOT NULL
        AND grant_row.team_id=CASE WHEN p.training_group_id IS NULL THEN p.team_id ELSE g.team_id END)))))
 THEN RETURN; END IF;
 RETURN QUERY SELECT locked_plan;
END $$;
