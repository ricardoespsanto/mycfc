-- Forward-only additive migration. Existing identity, membership, training,
-- dashboard, and operational records are intentionally untouched.
CREATE TABLE IF NOT EXISTS activity_connections (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  provider varchar(40) NOT NULL,
  provider_user_id varchar(255) NOT NULL,
  status varchar(40) NOT NULL DEFAULT 'ACTIVE',
  credentials_ciphertext bytea NULL,
  credential_key_id varchar(120) NULL,
  credential_expires_at timestamptz NULL,
  credential_version bigint NOT NULL DEFAULT 1,
  scopes text[] NOT NULL DEFAULT '{}',
  sync_cursor text NULL,
  last_successful_sync_at timestamptz NULL,
  last_error_code varchar(120) NULL,
  last_error_message varchar(2000) NULL,
  last_error_at timestamptz NULL,
  disconnected_at timestamptz NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT activity_connections_provider_valid CHECK (provider = btrim(provider) AND provider ~ '^[a-z][a-z0-9_-]{1,39}$'),
  CONSTRAINT activity_connections_provider_user_valid CHECK (provider_user_id = btrim(provider_user_id) AND char_length(provider_user_id) BETWEEN 1 AND 255),
  CONSTRAINT activity_connections_status_valid CHECK (status IN ('ACTIVE', 'REAUTHORIZATION_REQUIRED', 'DISCONNECTED')),
  CONSTRAINT activity_connections_credentials_valid CHECK (
    (status = 'DISCONNECTED' AND credentials_ciphertext IS NULL AND credential_key_id IS NULL AND disconnected_at IS NOT NULL)
    OR (status <> 'DISCONNECTED' AND credentials_ciphertext IS NOT NULL AND octet_length(credentials_ciphertext) > 0 AND credential_key_id IS NOT NULL AND credential_key_id = btrim(credential_key_id) AND char_length(credential_key_id) BETWEEN 1 AND 120 AND disconnected_at IS NULL)
  ),
  CONSTRAINT activity_connections_credential_version_valid CHECK (credential_version > 0),
  CONSTRAINT activity_connections_scopes_valid CHECK (array_position(scopes, NULL) IS NULL),
  CONSTRAINT activity_connections_error_valid CHECK (
    (last_error_code IS NULL AND last_error_message IS NULL AND last_error_at IS NULL)
    OR (last_error_code IS NOT NULL AND last_error_code = btrim(last_error_code) AND char_length(last_error_code) BETWEEN 1 AND 120 AND last_error_message IS NOT NULL AND char_length(last_error_message) BETWEEN 1 AND 2000 AND last_error_at IS NOT NULL)
  ),
  CONSTRAINT activity_connections_user_provider_unique UNIQUE (user_id, provider),
  CONSTRAINT activity_connections_provider_identity_unique UNIQUE (provider, provider_user_id),
  CONSTRAINT activity_connections_identity_unique UNIQUE (id, user_id, provider)
);
CREATE INDEX IF NOT EXISTS activity_connections_sync_idx ON activity_connections (status, last_successful_sync_at) WHERE status <> 'DISCONNECTED';

CREATE TABLE IF NOT EXISTS activity_sync_jobs (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  idempotency_key uuid NOT NULL UNIQUE,
  connection_id uuid NOT NULL REFERENCES activity_connections(id) ON DELETE CASCADE,
  reason varchar(20) NOT NULL,
  status varchar(20) NOT NULL DEFAULT 'PENDING',
  attempts integer NOT NULL DEFAULT 0,
  checkpoint text NULL,
  last_error_code varchar(120) NULL,
  last_error_message varchar(2000) NULL,
  requested_at timestamptz NOT NULL DEFAULT now(),
  started_at timestamptz NULL,
  finished_at timestamptz NULL,
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT activity_sync_jobs_reason_valid CHECK (reason IN ('LOGIN', 'WEBHOOK', 'MANUAL', 'BACKFILL', 'RECONCILIATION')),
  CONSTRAINT activity_sync_jobs_status_valid CHECK (status IN ('PENDING', 'RUNNING', 'SUCCEEDED', 'FAILED', 'CANCELLED')),
  CONSTRAINT activity_sync_jobs_attempts_valid CHECK (attempts >= 0),
  CONSTRAINT activity_sync_jobs_lifecycle_valid CHECK (
    (status = 'PENDING' AND started_at IS NULL AND finished_at IS NULL)
    OR (status = 'RUNNING' AND started_at IS NOT NULL AND finished_at IS NULL)
    OR (status IN ('SUCCEEDED', 'FAILED', 'CANCELLED') AND finished_at IS NOT NULL)
  ),
  CONSTRAINT activity_sync_jobs_error_valid CHECK (
    (last_error_code IS NULL AND last_error_message IS NULL)
    OR (last_error_code IS NOT NULL AND last_error_code = btrim(last_error_code) AND char_length(last_error_code) BETWEEN 1 AND 120 AND last_error_message IS NOT NULL AND char_length(last_error_message) BETWEEN 1 AND 2000)
  )
);
CREATE INDEX IF NOT EXISTS activity_sync_jobs_pending_idx ON activity_sync_jobs (requested_at, id) WHERE status = 'PENDING';
CREATE INDEX IF NOT EXISTS activity_sync_jobs_connection_idx ON activity_sync_jobs (connection_id, requested_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS synced_activities (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  connection_id uuid NOT NULL,
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  provider varchar(40) NOT NULL,
  provider_activity_id varchar(255) NOT NULL,
  provider_updated_at timestamptz NULL,
  starts_at timestamptz NOT NULL,
  ends_at timestamptz NOT NULL,
  sport varchar(120) NOT NULL,
  normalized_sport varchar(80) NOT NULL,
  duration_seconds integer NOT NULL,
  moving_duration_seconds integer NULL,
  distance_metres double precision NULL,
  average_heart_rate smallint NULL,
  maximum_heart_rate smallint NULL,
  provider_metrics jsonb NOT NULL DEFAULT '{}',
  raw_summary jsonb NOT NULL DEFAULT '{}',
  payload_sha256 bytea NOT NULL,
  normalization_version integer NOT NULL DEFAULT 1,
  deleted_at timestamptz NULL,
  ingested_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT synced_activities_connection_fk FOREIGN KEY (connection_id, user_id, provider) REFERENCES activity_connections(id, user_id, provider) ON DELETE CASCADE,
  CONSTRAINT synced_activities_provider_activity_valid CHECK (provider_activity_id = btrim(provider_activity_id) AND char_length(provider_activity_id) BETWEEN 1 AND 255),
  CONSTRAINT synced_activities_sport_valid CHECK (sport = btrim(sport) AND char_length(sport) BETWEEN 1 AND 120 AND normalized_sport = btrim(normalized_sport) AND char_length(normalized_sport) BETWEEN 1 AND 80),
  CONSTRAINT synced_activities_times_valid CHECK (starts_at < ends_at),
  CONSTRAINT synced_activities_duration_valid CHECK (duration_seconds > 0 AND (moving_duration_seconds IS NULL OR moving_duration_seconds >= 0)),
  CONSTRAINT synced_activities_distance_valid CHECK (distance_metres IS NULL OR distance_metres >= 0),
  CONSTRAINT synced_activities_heart_rate_valid CHECK ((average_heart_rate IS NULL OR average_heart_rate BETWEEN 20 AND 260) AND (maximum_heart_rate IS NULL OR maximum_heart_rate BETWEEN 20 AND 260) AND (average_heart_rate IS NULL OR maximum_heart_rate IS NULL OR average_heart_rate <= maximum_heart_rate)),
  CONSTRAINT synced_activities_payload_hash_valid CHECK (octet_length(payload_sha256) = 32),
  CONSTRAINT synced_activities_normalization_version_valid CHECK (normalization_version > 0),
  CONSTRAINT synced_activities_provider_identity_unique UNIQUE (provider, provider_activity_id),
  CONSTRAINT synced_activities_user_identity_unique UNIQUE (id, user_id)
);
CREATE INDEX IF NOT EXISTS synced_activities_user_starts_idx ON synced_activities (user_id, starts_at DESC, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS synced_activities_connection_updated_idx ON synced_activities (connection_id, updated_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS training_session_activity_matches (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  session_id uuid NOT NULL REFERENCES training_sessions(id) ON DELETE CASCADE,
  activity_id uuid NOT NULL,
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  status varchar(20) NOT NULL DEFAULT 'SUGGESTED',
  confidence smallint NOT NULL,
  match_basis jsonb NOT NULL DEFAULT '{}',
  decided_by_user_id uuid NULL REFERENCES users(id) ON DELETE RESTRICT,
  decided_at timestamptz NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT training_session_activity_matches_activity_fk FOREIGN KEY (activity_id, user_id) REFERENCES synced_activities(id, user_id) ON DELETE CASCADE,
  CONSTRAINT training_session_activity_matches_status_valid CHECK (status IN ('SUGGESTED', 'CONFIRMED', 'REJECTED')),
  CONSTRAINT training_session_activity_matches_confidence_valid CHECK (confidence BETWEEN 0 AND 100),
  CONSTRAINT training_session_activity_matches_decision_valid CHECK (
    (status = 'SUGGESTED' AND decided_by_user_id IS NULL AND decided_at IS NULL)
    OR (status IN ('CONFIRMED', 'REJECTED') AND decided_by_user_id IS NOT NULL AND decided_at IS NOT NULL)
  ),
  CONSTRAINT training_session_activity_matches_unique UNIQUE (session_id, activity_id, user_id)
);
CREATE INDEX IF NOT EXISTS training_session_activity_matches_user_idx ON training_session_activity_matches (user_id, status, updated_at DESC, id DESC);
CREATE UNIQUE INDEX IF NOT EXISTS training_session_activity_matches_confirmed_session_uidx ON training_session_activity_matches (session_id, user_id) WHERE status = 'CONFIRMED';
CREATE UNIQUE INDEX IF NOT EXISTS training_session_activity_matches_confirmed_activity_uidx ON training_session_activity_matches (activity_id) WHERE status = 'CONFIRMED';
