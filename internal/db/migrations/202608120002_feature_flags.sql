DO $$ BEGIN
 CREATE TYPE feature_availability_mode AS ENUM ('DISABLED', 'ADMIN_ONLY', 'ENABLED');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

CREATE TABLE IF NOT EXISTS feature_flags (
 feature_key varchar(80) PRIMARY KEY,
 mode feature_availability_mode NOT NULL,
 updated_by_id uuid NULL REFERENCES users(id) ON DELETE RESTRICT,
 updated_at timestamptz NOT NULL DEFAULT now(),
 CONSTRAINT feature_flags_key_valid CHECK (feature_key = btrim(feature_key) AND feature_key ~ '^[a-z][a-z0-9_]{1,79}$')
);

INSERT INTO feature_flags (feature_key, mode)
VALUES ('suggestions', 'ENABLED'), ('photo_submissions', 'DISABLED')
ON CONFLICT (feature_key) DO NOTHING;

CREATE TABLE IF NOT EXISTS feature_flag_events (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 feature_key varchar(80) NOT NULL REFERENCES feature_flags(feature_key) ON DELETE RESTRICT,
 previous_mode feature_availability_mode NOT NULL,
 new_mode feature_availability_mode NOT NULL,
 actor_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 occurred_at timestamptz NOT NULL DEFAULT now(),
 CONSTRAINT feature_flag_events_change_valid CHECK (previous_mode <> new_mode)
);
CREATE INDEX IF NOT EXISTS feature_flag_events_feature_idx ON feature_flag_events (feature_key, occurred_at DESC, id DESC);

CREATE OR REPLACE FUNCTION audit_feature_flag_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.updated_by_id IS NULL THEN
  RAISE EXCEPTION 'feature flag changes require an administrator';
 END IF;
 IF OLD.mode = NEW.mode THEN
  RETURN NEW;
 END IF;
 INSERT INTO feature_flag_events (feature_key, previous_mode, new_mode, actor_user_id, occurred_at)
 VALUES (NEW.feature_key, OLD.mode, NEW.mode, NEW.updated_by_id, NEW.updated_at);
 RETURN NEW;
END;
$$;
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'feature_flags_audit_trigger' AND tgrelid = 'feature_flags'::regclass) THEN
  CREATE TRIGGER feature_flags_audit_trigger
  AFTER UPDATE ON feature_flags
  FOR EACH ROW EXECUTE FUNCTION audit_feature_flag_change();
 END IF;
END $$;

CREATE OR REPLACE FUNCTION prevent_feature_flag_event_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'feature flag events are append-only';
END;
$$;
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'feature_flag_events_immutable_trigger' AND tgrelid = 'feature_flag_events'::regclass) THEN
  CREATE TRIGGER feature_flag_events_immutable_trigger
  BEFORE UPDATE OR DELETE ON feature_flag_events
  FOR EACH ROW EXECUTE FUNCTION prevent_feature_flag_event_mutation();
 END IF;
END $$;
