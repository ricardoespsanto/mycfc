CREATE UNIQUE INDEX IF NOT EXISTS activity_sync_jobs_one_active_per_connection_idx ON activity_sync_jobs (connection_id) WHERE status IN ('PENDING', 'RUNNING');

ALTER TABLE activity_connections ADD COLUMN IF NOT EXISTS provider_retry_after timestamptz NULL;

CREATE TABLE IF NOT EXISTS activity_load_observations (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), connection_id uuid NOT NULL, user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE, provider varchar(40) NOT NULL, load_kind varchar(80) NOT NULL, observed_on date NOT NULL, availability varchar(20) NOT NULL, provider_status varchar(120) NOT NULL, load_value double precision NULL, short_term_load double precision NULL, short_term_window_days smallint NULL, long_term_load double precision NULL, long_term_window_days smallint NULL, load_ratio double precision NULL, provider_metrics jsonb NOT NULL DEFAULT '{}', payload_sha256 bytea NOT NULL, source_updated_at timestamptz NULL, fetched_at timestamptz NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 CONSTRAINT activity_load_observations_connection_fk FOREIGN KEY (connection_id, user_id, provider) REFERENCES activity_connections(id, user_id, provider) ON DELETE CASCADE,
 CONSTRAINT activity_load_observations_kind_valid CHECK (load_kind = btrim(load_kind) AND char_length(load_kind) BETWEEN 1 AND 80),
 CONSTRAINT activity_load_observations_availability_valid CHECK (availability IN ('AVAILABLE', 'UNAVAILABLE')),
 CONSTRAINT activity_load_observations_status_valid CHECK (provider_status = btrim(provider_status) AND char_length(provider_status) BETWEEN 1 AND 120),
 CONSTRAINT activity_load_observations_values_valid CHECK ((availability = 'UNAVAILABLE' AND load_value IS NULL AND short_term_load IS NULL AND short_term_window_days IS NULL AND long_term_load IS NULL AND long_term_window_days IS NULL AND load_ratio IS NULL) OR (availability = 'AVAILABLE' AND (load_value IS NULL OR load_value >= 0) AND (short_term_load IS NULL OR short_term_load >= 0) AND (long_term_load IS NULL OR long_term_load >= 0) AND (load_ratio IS NULL OR load_ratio >= 0))),
 CONSTRAINT activity_load_observations_windows_valid CHECK ((short_term_load IS NULL) = (short_term_window_days IS NULL) AND (long_term_load IS NULL) = (long_term_window_days IS NULL) AND (short_term_window_days IS NULL OR short_term_window_days > 0) AND (long_term_window_days IS NULL OR long_term_window_days > 0)),
 CONSTRAINT activity_load_observations_payload_hash_valid CHECK (octet_length(payload_sha256) = 32),
 CONSTRAINT activity_load_observations_identity_unique UNIQUE (connection_id, load_kind, observed_on)
);
CREATE INDEX IF NOT EXISTS activity_load_observations_user_date_idx ON activity_load_observations (user_id, observed_on DESC, id DESC);
CREATE INDEX IF NOT EXISTS activity_load_observations_connection_fetched_idx ON activity_load_observations (connection_id, fetched_at DESC, id DESC);
