ALTER TABLE events
  ADD COLUMN IF NOT EXISTS status varchar(20) NOT NULL DEFAULT 'ACTIVE',
  ADD COLUMN IF NOT EXISTS cancelled_at timestamptz NULL,
  ADD COLUMN IF NOT EXISTS cancelled_by_id uuid NULL,
  ADD COLUMN IF NOT EXISTS cancellation_reason varchar(500) NULL;

DO $$ BEGIN
  ALTER TABLE events ADD CONSTRAINT events_cancelled_by_fk FOREIGN KEY (cancelled_by_id) REFERENCES users(id) ON DELETE RESTRICT;
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
  ALTER TABLE events ADD CONSTRAINT events_status_valid CHECK (status IN ('ACTIVE', 'CANCELLED'));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
  ALTER TABLE events ADD CONSTRAINT events_cancellation_reason_valid CHECK (cancellation_reason IS NULL OR (cancellation_reason = btrim(cancellation_reason) AND char_length(cancellation_reason) BETWEEN 2 AND 500));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
  ALTER TABLE events ADD CONSTRAINT events_cancellation_complete CHECK ((status = 'ACTIVE' AND cancelled_at IS NULL AND cancelled_by_id IS NULL AND cancellation_reason IS NULL) OR (status = 'CANCELLED' AND cancelled_at IS NOT NULL AND cancelled_by_id IS NOT NULL AND cancellation_reason IS NOT NULL));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

CREATE INDEX IF NOT EXISTS events_status_starts_idx ON events (status, starts_at);
