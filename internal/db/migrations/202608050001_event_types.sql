ALTER TABLE events
  ADD COLUMN IF NOT EXISTS event_type varchar(20) NOT NULL DEFAULT 'GENERAL';

DO $$ BEGIN
  ALTER TABLE events ADD CONSTRAINT events_type_valid CHECK (event_type IN ('GENERAL', 'COMPETITION'));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
