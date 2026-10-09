-- #335 final contract: forward-only cutover from the immutable expand version
-- 202609290001. Apply only with old writers fenced in an approved interruption.
-- No member or other durable records are deleted. The preflight fails closed.
-- Exception evidence remains DB-disabled until an authorized audited service,
-- manual privacy runbook and a further forward migration are ready. A caller
-- can forge a custom GUC, so it must never be the active authorization gate.

DO $$ BEGIN
 IF EXISTS (
  SELECT 1 FROM user_memberships m JOIN seasons s ON s.id=m.season_id
  WHERE m.starts_on < s.starts_on OR m.starts_on > s.ends_on
     OR m.ends_on > s.ends_on OR m.ends_on IS NULL
 ) THEN RAISE EXCEPTION 'legacy participation dates need identity-preserving review'; END IF;
 IF EXISTS (
  SELECT 1 FROM user_memberships m JOIN programmes p ON p.id=m.programme_id
  WHERE (p.code='Competition' AND m.competition_category_id IS NULL)
     OR (p.code IN ('Leisure','Kayak_Polo') AND m.competition_category_id IS NOT NULL)
 ) THEN RAISE EXCEPTION 'legacy category assignment needs review'; END IF;
 IF EXISTS (
  SELECT 1 FROM user_memberships m
  JOIN competition_categories c ON c.id=m.competition_category_id
  JOIN users u ON u.id=m.user_id
  WHERE (c.birth_date_from IS NOT NULL AND u.date_of_birth<c.birth_date_from)
     OR (c.birth_date_to IS NOT NULL AND u.date_of_birth>c.birth_date_to)
 ) THEN RAISE EXCEPTION 'legacy category eligibility needs review'; END IF;
END $$;

CREATE EXTENSION IF NOT EXISTS btree_gist;
ALTER TABLE user_memberships ADD CONSTRAINT user_memberships_age_exception_complete CHECK (
 (age_exception_reason IS NULL AND age_exception_by_id IS NULL AND age_exception_at IS NULL) OR
 (age_exception_reason IS NOT NULL AND age_exception_reason=btrim(age_exception_reason)
  AND char_length(age_exception_reason) BETWEEN 2 AND 500 AND age_exception_by_id IS NOT NULL AND age_exception_at IS NOT NULL)
);
ALTER TABLE user_memberships DROP CONSTRAINT user_memberships_user_season_programme_unique;
ALTER TABLE user_memberships ADD CONSTRAINT user_memberships_one_participation_per_day
 EXCLUDE USING gist (user_id WITH =, daterange(starts_on, coalesce(ends_on,'infinity'::date),'[]') WITH &&);

CREATE FUNCTION validate_dated_participation() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE s seasons%ROWTYPE; code text; c competition_categories%ROWTYPE; birth date; mismatch boolean;
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
  RAISE EXCEPTION 'exception entry requires authorized audited workflow' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER user_memberships_dated_participation_valid
 BEFORE INSERT OR UPDATE OF user_id,season_id,programme_id,starts_on,ends_on,
 competition_category_id,age_exception_reason,age_exception_by_id,age_exception_at
 ON user_memberships FOR EACH ROW EXECUTE FUNCTION validate_dated_participation();

-- No silent historical correction path: classification identity, scope and start
-- are immutable until an audited/versioned service exists. End dates may only
-- shorten, so a closed interval cannot be reopened by direct SQL.
CREATE FUNCTION prevent_dated_participation_rewrite() RETURNS trigger LANGUAGE plpgsql AS $$
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
 RETURN NEW;
END $$;
CREATE TRIGGER user_memberships_history_immutable
 BEFORE UPDATE ON user_memberships FOR EACH ROW EXECUTE FUNCTION prevent_dated_participation_rewrite();
CREATE FUNCTION prevent_category_definition_rewrite() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS (SELECT 1 FROM user_memberships WHERE competition_category_id=OLD.id) THEN
  RAISE EXCEPTION 'referenced category definition requires versioned correction' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER competition_categories_referenced_immutable
 BEFORE UPDATE OF season_id,programme_id,birth_date_from,birth_date_to ON competition_categories
 FOR EACH ROW EXECUTE FUNCTION prevent_category_definition_rewrite();
CREATE FUNCTION prevent_classified_dob_rewrite() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS (SELECT 1 FROM user_memberships WHERE user_id=OLD.id AND competition_category_id IS NOT NULL) THEN
  RAISE EXCEPTION 'classified birth date requires audited correction' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER users_classified_dob_immutable BEFORE UPDATE OF date_of_birth ON users
 FOR EACH ROW EXECUTE FUNCTION prevent_classified_dob_rewrite();
