-- Permit an active programme coach to record a first age exception without a subject anchor.
CREATE OR REPLACE FUNCTION validate_dated_participation() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE s seasons%ROWTYPE; code text; c competition_categories%ROWTYPE; birth date; mismatch boolean; scoped boolean;
BEGIN
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
  IF mismatch AND NEW.age_exception_reason IS NULL THEN RAISE EXCEPTION 'age exception reason required' USING ERRCODE='23514'; END IF;
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
