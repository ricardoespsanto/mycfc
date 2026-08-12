DO $$ BEGIN
 CREATE TYPE photo_album_status AS ENUM ('OPEN', 'ARCHIVED');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

CREATE TABLE IF NOT EXISTS photo_albums (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 title varchar(180) NOT NULL,
 description varchar(2000) NOT NULL DEFAULT '',
 status photo_album_status NOT NULL DEFAULT 'OPEN',
 created_by_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 archived_by_id uuid NULL REFERENCES users(id) ON DELETE RESTRICT,
 archived_at timestamptz NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 CONSTRAINT photo_albums_title_valid CHECK (title = btrim(title) AND char_length(title) BETWEEN 2 AND 180),
 CONSTRAINT photo_albums_description_valid CHECK (description = btrim(description) AND char_length(description) <= 2000),
 CONSTRAINT photo_albums_lifecycle_valid CHECK (
  (status = 'OPEN' AND archived_by_id IS NULL AND archived_at IS NULL)
  OR (status = 'ARCHIVED' AND archived_by_id IS NOT NULL AND archived_at IS NOT NULL)
 )
);
CREATE INDEX IF NOT EXISTS photo_albums_status_created_idx ON photo_albums (status, created_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS photo_album_programme_audiences (
 album_id uuid NOT NULL REFERENCES photo_albums(id) ON DELETE CASCADE,
 programme_id uuid NOT NULL REFERENCES programmes(id) ON DELETE RESTRICT,
 PRIMARY KEY (album_id, programme_id)
);
CREATE INDEX IF NOT EXISTS photo_album_programme_audiences_lookup_idx ON photo_album_programme_audiences (programme_id, album_id);

CREATE TABLE IF NOT EXISTS photo_album_team_audiences (
 album_id uuid NOT NULL REFERENCES photo_albums(id) ON DELETE CASCADE,
 team_id uuid NOT NULL REFERENCES teams(id) ON DELETE RESTRICT,
 PRIMARY KEY (album_id, team_id)
);
CREATE INDEX IF NOT EXISTS photo_album_team_audiences_lookup_idx ON photo_album_team_audiences (team_id, album_id);

CREATE OR REPLACE FUNCTION ensure_photo_album_has_audience() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE target_album_id uuid;
BEGIN
 IF TG_TABLE_NAME = 'photo_albums' THEN
  target_album_id := NEW.id;
 ELSE
  target_album_id := OLD.album_id;
 END IF;
 IF EXISTS (SELECT 1 FROM photo_albums WHERE id = target_album_id)
    AND NOT EXISTS (SELECT 1 FROM photo_album_programme_audiences WHERE album_id = target_album_id)
    AND NOT EXISTS (SELECT 1 FROM photo_album_team_audiences WHERE album_id = target_album_id) THEN
  RAISE EXCEPTION 'photo album requires an audience';
 END IF;
 IF TG_OP = 'DELETE' THEN
  RETURN OLD;
 END IF;
 RETURN NEW;
END;
$$;
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'photo_albums_audience_required_trigger' AND tgrelid = 'photo_albums'::regclass) THEN
  CREATE CONSTRAINT TRIGGER photo_albums_audience_required_trigger AFTER INSERT OR UPDATE ON photo_albums DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION ensure_photo_album_has_audience();
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'photo_album_programme_audience_required_trigger' AND tgrelid = 'photo_album_programme_audiences'::regclass) THEN
  CREATE CONSTRAINT TRIGGER photo_album_programme_audience_required_trigger AFTER DELETE ON photo_album_programme_audiences DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION ensure_photo_album_has_audience();
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'photo_album_team_audience_required_trigger' AND tgrelid = 'photo_album_team_audiences'::regclass) THEN
  CREATE CONSTRAINT TRIGGER photo_album_team_audience_required_trigger AFTER DELETE ON photo_album_team_audiences DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION ensure_photo_album_has_audience();
 END IF;
END $$;

CREATE TABLE IF NOT EXISTS photo_album_audit_events (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 album_id uuid NOT NULL REFERENCES photo_albums(id) ON DELETE RESTRICT,
 action varchar(20) NOT NULL CHECK (action IN ('CREATED', 'ARCHIVED')),
 actor_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 occurred_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS photo_album_audit_events_album_idx ON photo_album_audit_events (album_id, occurred_at, id);

CREATE OR REPLACE FUNCTION audit_photo_album_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP = 'INSERT' THEN
  INSERT INTO photo_album_audit_events (album_id, action, actor_user_id, occurred_at)
  VALUES (NEW.id, 'CREATED', NEW.created_by_id, NEW.created_at);
  RETURN NEW;
 END IF;
 IF OLD.status = 'OPEN' AND NEW.status = 'ARCHIVED' AND NEW.archived_by_id IS NOT NULL AND NEW.archived_at IS NOT NULL THEN
  INSERT INTO photo_album_audit_events (album_id, action, actor_user_id, occurred_at)
  VALUES (NEW.id, 'ARCHIVED', NEW.archived_by_id, NEW.archived_at);
  RETURN NEW;
 END IF;
 RAISE EXCEPTION 'photo album lifecycle transition is invalid';
END;
$$;
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'photo_albums_audit_trigger' AND tgrelid = 'photo_albums'::regclass) THEN
  CREATE TRIGGER photo_albums_audit_trigger AFTER INSERT OR UPDATE OF status ON photo_albums FOR EACH ROW EXECUTE FUNCTION audit_photo_album_change();
 END IF;
END $$;

CREATE OR REPLACE FUNCTION prevent_photo_album_audit_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'photo album audit events are append-only';
END;
$$;
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'photo_album_audit_events_immutable_trigger' AND tgrelid = 'photo_album_audit_events'::regclass) THEN
  CREATE TRIGGER photo_album_audit_events_immutable_trigger BEFORE UPDATE OR DELETE ON photo_album_audit_events FOR EACH ROW EXECUTE FUNCTION prevent_photo_album_audit_mutation();
 END IF;
END $$;
