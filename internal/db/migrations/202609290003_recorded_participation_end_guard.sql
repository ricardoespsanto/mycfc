-- Forward-only guard for published prescriptions and recorded event/training dates.
-- 002 may already be present in an installed ledger; never rewrite that version.
CREATE OR REPLACE FUNCTION prevent_dated_participation_rewrite() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
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
