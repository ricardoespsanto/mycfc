-- Manual, externally approved classification-only full deletion. No automation or ledger.
-- Owned by the existing migration owner; never grant this schema to web/retired roles.
CREATE SCHEMA classification_erasure;
REVOKE ALL ON SCHEMA classification_erasure FROM PUBLIC;
CREATE TABLE classification_erasure.erased_memberships (
 membership_id uuid PRIMARY KEY REFERENCES public.user_memberships(id) ON DELETE CASCADE
);
-- Transient capability, not a case/tombstone ledger. Empty after each successful call.
CREATE TABLE classification_erasure.fence (
 transaction_id bigint PRIMARY KEY,
 event_ids bigint[] NOT NULL,
 membership_ids uuid[] NOT NULL
);
REVOKE ALL ON ALL TABLES IN SCHEMA classification_erasure FROM PUBLIC;

CREATE FUNCTION classification_erasure.permitted_clear(old_row public.user_memberships, new_row public.user_memberships)
RETURNS boolean LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog,public,pg_temp AS $$
 SELECT new_row.age_exception_reason IS NULL AND new_row.age_exception_by_id IS NULL
 AND new_row.age_exception_at IS NULL AND old_row.age_exception_reason IS NOT NULL
 AND (to_jsonb(old_row)-ARRAY['age_exception_reason','age_exception_by_id','age_exception_at'])
   = (to_jsonb(new_row)-ARRAY['age_exception_reason','age_exception_by_id','age_exception_at'])
 AND EXISTS(SELECT 1 FROM classification_erasure.fence f
 WHERE f.transaction_id=txid_current() AND old_row.id=ANY(f.membership_ids))
$$;

CREATE FUNCTION classification_erasure.validate_scope(target uuid, reviewed_events jsonb, reviewed_memberships jsonb, replay boolean)
RETURNS void LANGUAGE plpgsql SET search_path=pg_catalog,public,pg_temp AS $$
DECLARE item jsonb; eid bigint; mid uuid; subject uuid;
BEGIN
 IF target IS NULL OR replay IS NULL OR NOT EXISTS(SELECT 1 FROM public.users WHERE id=target) THEN
  RAISE EXCEPTION 'invalid classification erasure target'; END IF;
 IF reviewed_events IS NULL OR reviewed_memberships IS NULL OR jsonb_typeof(reviewed_events)<>'array'
 OR jsonb_typeof(reviewed_memberships)<>'array' THEN RAISE EXCEPTION 'invalid reviewed scope'; END IF;
 FOR item IN SELECT value FROM jsonb_array_elements(reviewed_events) LOOP
  IF jsonb_typeof(item)<>'object' OR NOT item ?& ARRAY['id','subject'] OR item-ARRAY['id','subject']<>'{}'::jsonb
  OR item->>'id' IS NULL OR item->>'subject' IS NULL THEN RAISE EXCEPTION 'invalid reviewed event'; END IF;
  eid:=(item->>'id')::bigint; subject:=(item->>'subject')::uuid;
  IF eid<=0 OR NOT EXISTS(SELECT 1 FROM public.users WHERE id=subject)
   OR EXISTS(SELECT 1 FROM jsonb_array_elements(reviewed_events) other WHERE (other->>'id')::bigint=eid AND (other->>'subject')::uuid<>subject)
   OR EXISTS(SELECT 1 FROM public.person_sport_assignment_events WHERE id=eid AND subject_user_id<>subject)
   OR (NOT replay AND NOT EXISTS(SELECT 1 FROM public.person_sport_assignment_events WHERE id=eid)) THEN
   RAISE EXCEPTION 'reviewed event outside supplied scope'; END IF;
 END LOOP;
 FOR item IN SELECT value FROM jsonb_array_elements(reviewed_memberships) LOOP
  IF jsonb_typeof(item)<>'object' OR NOT item ?& ARRAY['id','subject'] OR item-ARRAY['id','subject']<>'{}'::jsonb
  OR item->>'id' IS NULL OR item->>'subject' IS NULL THEN RAISE EXCEPTION 'invalid reviewed membership'; END IF;
  mid:=(item->>'id')::uuid; subject:=(item->>'subject')::uuid;
  IF NOT EXISTS(SELECT 1 FROM public.user_memberships WHERE id=mid AND user_id=subject
   AND (age_exception_reason IS NOT NULL OR EXISTS(SELECT 1 FROM classification_erasure.erased_memberships WHERE membership_id=mid))) THEN
   RAISE EXCEPTION 'reviewed membership outside supplied scope'; END IF;
 END LOOP;
 -- Repeated identical IDs are selected only once; conflicting event subjects fail even on replay.
END $$;

CREATE FUNCTION classification_erasure.check_remaining(target uuid, reviewed_events jsonb, reviewed_memberships jsonb)
RETURNS TABLE(audit_events bigint, exception_reasons bigint, shared_definition_approver_links bigint)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public,pg_temp AS $$
BEGIN
 IF current_setting('transaction_isolation')<>'read committed' THEN RAISE EXCEPTION 'read committed required'; END IF;
 LOCK TABLE public.person_sport_assignment_events,public.user_memberships IN SHARE ROW EXCLUSIVE MODE;
 PERFORM classification_erasure.validate_scope(target,reviewed_events,reviewed_memberships,true);
 RETURN QUERY SELECT
 (SELECT count(*) FROM public.person_sport_assignment_events e WHERE e.subject_user_id=target OR e.actor_user_id=target
  OR e.id IN (SELECT (value->>'id')::bigint FROM jsonb_array_elements(reviewed_events))),
 (SELECT count(*) FROM public.user_memberships m WHERE m.age_exception_reason IS NOT NULL AND
  (m.user_id=target OR m.age_exception_by_id=target OR m.id IN
   (SELECT (value->>'id')::uuid FROM jsonb_array_elements(reviewed_memberships)))),
 (SELECT count(*) FROM public.competition_categories WHERE approved_by_user_id=target);
END $$;

CREATE FUNCTION classification_erasure.erase(target uuid, reviewed_events jsonb, reviewed_memberships jsonb, replay boolean DEFAULT false)
RETURNS TABLE(deleted_events bigint, cleared_exceptions bigint)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public,pg_temp AS $$
DECLARE events bigint[]; memberships uuid[]; remaining record;
BEGIN
 IF current_setting('transaction_isolation')<>'read committed' THEN RAISE EXCEPTION 'read committed required'; END IF;
 LOCK TABLE public.person_sport_assignment_events,public.user_memberships IN SHARE ROW EXCLUSIVE MODE;
 PERFORM classification_erasure.validate_scope(target,reviewed_events,reviewed_memberships,replay);
 SELECT coalesce(array_agg(id),'{}'::bigint[]) INTO events FROM public.person_sport_assignment_events
 WHERE subject_user_id=target OR actor_user_id=target
 OR id IN (SELECT (value->>'id')::bigint FROM jsonb_array_elements(reviewed_events));
 SELECT coalesce(array_agg(id),'{}'::uuid[]) INTO memberships FROM public.user_memberships
 WHERE age_exception_reason IS NOT NULL AND (user_id=target OR age_exception_by_id=target
 OR id IN (SELECT (value->>'id')::uuid FROM jsonb_array_elements(reviewed_memberships)));
 INSERT INTO classification_erasure.fence VALUES(txid_current(),events,memberships);
 INSERT INTO classification_erasure.erased_memberships SELECT unnest(memberships) ON CONFLICT DO NOTHING;
 DELETE FROM public.person_sport_assignment_events WHERE id=ANY(events);
 GET DIAGNOSTICS deleted_events=ROW_COUNT;
 UPDATE public.user_memberships SET age_exception_reason=NULL,age_exception_by_id=NULL,age_exception_at=NULL
 WHERE id=ANY(memberships);
 GET DIAGNOSTICS cleared_exceptions=ROW_COUNT;
 DELETE FROM classification_erasure.fence WHERE transaction_id=txid_current();
 SELECT * INTO remaining FROM classification_erasure.check_remaining(target,reviewed_events,reviewed_memberships);
 IF remaining.audit_events<>0 OR remaining.exception_reasons<>0 THEN RAISE EXCEPTION 'classification erasure postcheck failed'; END IF;
 RETURN NEXT;
END $$;
REVOKE ALL ON ALL FUNCTIONS IN SCHEMA classification_erasure FROM PUBLIC;

CREATE OR REPLACE FUNCTION validate_dated_participation() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public,pg_temp AS $$
DECLARE s seasons%ROWTYPE; code text; c competition_categories%ROWTYPE; birth date; mismatch boolean; scoped boolean;
BEGIN
 IF TG_OP='UPDATE' AND classification_erasure.permitted_clear(OLD,NEW) THEN RETURN NEW; END IF;
 IF EXISTS(SELECT 1 FROM classification_erasure.erased_memberships WHERE membership_id=NEW.id) THEN
  IF TG_OP<>'UPDATE' THEN RAISE EXCEPTION 'erased evidence cannot authorize new exception'; END IF;
  IF NEW.user_id IS DISTINCT FROM OLD.user_id OR NEW.season_id IS DISTINCT FROM OLD.season_id
   OR NEW.programme_id IS DISTINCT FROM OLD.programme_id OR NEW.competition_category_id IS DISTINCT FROM OLD.competition_category_id
   OR NEW.age_exception_reason IS NOT NULL OR NEW.age_exception_by_id IS NOT NULL OR NEW.age_exception_at IS NOT NULL THEN
   RAISE EXCEPTION 'erased evidence cannot authorize new exception'; END IF;
 END IF;
 SELECT * INTO s FROM seasons WHERE id=NEW.season_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'participation season missing' USING ERRCODE='23514'; END IF;
 NEW.ends_on:=coalesce(NEW.ends_on,s.ends_on);
 IF NEW.starts_on<s.starts_on OR NEW.starts_on>s.ends_on OR NEW.ends_on>s.ends_on THEN
  RAISE EXCEPTION 'participation dates outside season' USING ERRCODE='23514'; END IF;
 SELECT p.code INTO code FROM programmes p WHERE p.id=NEW.programme_id;
 IF code='Competition' AND NEW.competition_category_id IS NULL
   OR code IN ('Leisure','Kayak_Polo') AND NEW.competition_category_id IS NOT NULL THEN
  RAISE EXCEPTION 'invalid participation category' USING ERRCODE='23514'; END IF;
 IF NEW.competition_category_id IS NOT NULL THEN
  SELECT * INTO c FROM competition_categories WHERE id=NEW.competition_category_id;
  IF NOT FOUND OR c.season_id<>NEW.season_id OR c.programme_id<>NEW.programme_id THEN
   RAISE EXCEPTION 'category outside participation scope' USING ERRCODE='23514'; END IF;
  SELECT date_of_birth INTO birth FROM users WHERE id=NEW.user_id;
  mismatch:=(c.birth_date_from IS NOT NULL AND birth<c.birth_date_from)
         OR (c.birth_date_to IS NOT NULL AND birth>c.birth_date_to);
  IF mismatch AND NEW.age_exception_reason IS NULL AND NOT EXISTS(SELECT 1 FROM classification_erasure.erased_memberships WHERE membership_id=NEW.id) THEN RAISE EXCEPTION 'age exception reason required' USING ERRCODE='23514'; END IF;
  IF NOT mismatch AND NEW.age_exception_reason IS NOT NULL THEN RAISE EXCEPTION 'age exception without eligibility mismatch' USING ERRCODE='23514'; END IF;
 ELSIF NEW.age_exception_reason IS NOT NULL THEN
  RAISE EXCEPTION 'age exception without category' USING ERRCODE='23514'; END IF;
 IF NEW.age_exception_reason IS NOT NULL THEN
  IF NEW.age_exception_by_id IS NULL OR NEW.age_exception_at IS NULL
    OR (TG_OP='INSERT' AND NEW.age_exception_at IS DISTINCT FROM now()) THEN
   RAISE EXCEPTION 'exception provenance missing' USING ERRCODE='23514'; END IF;
  IF TG_OP='INSERT' THEN
   SELECT guardian_authority_is_administrator(NEW.age_exception_by_id) INTO scoped;
  IF NOT scoped THEN
   SELECT EXISTS(SELECT 1 FROM staff_grants g JOIN users actor ON actor.id=g.user_id
    JOIN seasons current_season ON current_season.id=NEW.season_id AND current_season.is_current
    WHERE g.user_id=NEW.age_exception_by_id AND actor.is_active AND actor.erased_at IS NULL
     AND g.capability='COACH' AND g.revoked_at IS NULL AND g.programme_id=NEW.programme_id
     AND g.programme_id IS NOT NULL) INTO scoped;
  END IF;
  IF NOT scoped THEN RAISE EXCEPTION 'exception actor outside scope' USING ERRCODE='23514'; END IF;
  END IF;
 ELSIF NEW.age_exception_by_id IS NOT NULL OR NEW.age_exception_at IS NOT NULL THEN
  RAISE EXCEPTION 'exception provenance without reason' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE OR REPLACE FUNCTION prevent_dated_participation_rewrite() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public,pg_temp AS $$
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
    AND (session.starts_at AT TIME ZONE 'Europe/Lisbon')::date>NEW.ends_on)
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
     AND (session.starts_at AT TIME ZONE 'Europe/Lisbon')::date BETWEEN OLD.starts_on AND OLD.ends_on
     AND (session.starts_at AT TIME ZONE 'Europe/Lisbon')::date>NEW.ends_on) THEN
   RAISE EXCEPTION 'recorded event, training or published prescription date requires audited correction'
    USING ERRCODE='23514', CONSTRAINT='user_memberships_recorded_history_end';
  END IF;
 END IF;
 RETURN NEW;
END $$;


CREATE OR REPLACE FUNCTION public.prevent_person_sport_assignment_event_mutation() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public,pg_temp AS $$
BEGIN
 IF TG_OP='DELETE' AND EXISTS(SELECT 1 FROM classification_erasure.fence f
  WHERE f.transaction_id=txid_current() AND OLD.id=ANY(f.event_ids)) THEN RETURN OLD; END IF;
 RAISE EXCEPTION 'person sport assignment audit is append-only' USING ERRCODE='23514';
END $$;
