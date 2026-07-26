-- +goose Up
CREATE TYPE event_response_status AS ENUM ('Going', 'NotGoing', 'Waitlisted');

CREATE TABLE events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    title varchar(180) NOT NULL,
    description varchar(4000) NOT NULL DEFAULT '',
    starts_at timestamptz NOT NULL,
    ends_at timestamptz NOT NULL,
    response_deadline timestamptz NULL,
    capacity integer NULL,
    created_by_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT events_title_valid CHECK (title = btrim(title) AND char_length(title) BETWEEN 2 AND 180),
    CONSTRAINT events_description_valid CHECK (description = btrim(description) AND char_length(description) <= 4000),
    CONSTRAINT events_times_valid CHECK (starts_at < ends_at),
    CONSTRAINT events_deadline_valid CHECK (response_deadline IS NULL OR response_deadline <= starts_at),
    CONSTRAINT events_capacity_valid CHECK (capacity IS NULL OR capacity > 0)
);

CREATE INDEX events_starts_at_idx ON events (starts_at);

-- No rows means that the event is for every active member. Otherwise the
-- event is limited to members with an active membership in a listed programme.
CREATE TABLE event_audiences (
    event_id uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    programme_id uuid NOT NULL REFERENCES programmes(id) ON DELETE RESTRICT,
    PRIMARY KEY (event_id, programme_id)
);

CREATE INDEX event_audiences_programme_idx ON event_audiences (programme_id, event_id);

CREATE TABLE event_responses (
    event_id uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status event_response_status NOT NULL,
    responded_by_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    responded_at timestamptz NOT NULL DEFAULT now(),
    checked_in_at timestamptz NULL,
    checked_in_by_id uuid NULL REFERENCES users(id) ON DELETE RESTRICT,
    PRIMARY KEY (event_id, user_id),
    CONSTRAINT event_responses_checkin_valid CHECK (
        (checked_in_at IS NULL AND checked_in_by_id IS NULL)
        OR (checked_in_at IS NOT NULL AND checked_in_by_id IS NOT NULL)
    )
);

CREATE INDEX event_responses_event_status_idx ON event_responses (event_id, status);

CREATE TABLE training_plans (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    title varchar(180) NOT NULL,
    description varchar(4000) NOT NULL DEFAULT '',
    programme_id uuid NULL REFERENCES programmes(id) ON DELETE RESTRICT,
    team_id uuid NULL REFERENCES teams(id) ON DELETE RESTRICT,
    created_by_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT training_plans_title_valid CHECK (title = btrim(title) AND char_length(title) BETWEEN 2 AND 180),
    CONSTRAINT training_plans_description_valid CHECK (description = btrim(description) AND char_length(description) <= 4000),
    CONSTRAINT training_plans_scope_valid CHECK (programme_id IS NOT NULL OR team_id IS NOT NULL)
);
CREATE INDEX training_plans_programme_idx ON training_plans (programme_id) WHERE programme_id IS NOT NULL;
CREATE INDEX training_plans_team_idx ON training_plans (team_id) WHERE team_id IS NOT NULL;

CREATE TABLE training_sessions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_id uuid NOT NULL REFERENCES training_plans(id) ON DELETE CASCADE,
    title varchar(180) NOT NULL,
    description varchar(4000) NOT NULL DEFAULT '',
    starts_at timestamptz NOT NULL,
    ends_at timestamptz NOT NULL,
    modality_id uuid NULL REFERENCES modalities(id) ON DELETE RESTRICT,
    created_by_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT training_sessions_title_valid CHECK (title = btrim(title) AND char_length(title) BETWEEN 2 AND 180),
    CONSTRAINT training_sessions_description_valid CHECK (description = btrim(description) AND char_length(description) <= 4000),
    CONSTRAINT training_sessions_times_valid CHECK (starts_at < ends_at)
);
CREATE INDEX training_sessions_plan_starts_idx ON training_sessions (plan_id, starts_at);

CREATE TABLE training_session_assignments (
    session_id uuid NOT NULL REFERENCES training_sessions(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    assigned_by_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    assigned_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz NULL,
    completed_by_id uuid NULL REFERENCES users(id) ON DELETE RESTRICT,
    PRIMARY KEY (session_id, user_id),
    CONSTRAINT training_assignment_completion_valid CHECK (
        (completed_at IS NULL AND completed_by_id IS NULL)
        OR (completed_at IS NOT NULL AND completed_by_id IS NOT NULL)
    )
);
CREATE INDEX training_assignments_user_idx ON training_session_assignments (user_id, completed_at, assigned_at DESC);

CREATE TABLE competition_documents (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    title varchar(180) NOT NULL,
    url text NOT NULL,
    source varchar(180) NOT NULL,
    reviewed_on date NOT NULL,
    event_id uuid NULL REFERENCES events(id) ON DELETE CASCADE,
    modality_id uuid NULL REFERENCES modalities(id) ON DELETE RESTRICT,
    programme_id uuid NULL REFERENCES programmes(id) ON DELETE RESTRICT,
    team_id uuid NULL REFERENCES teams(id) ON DELETE RESTRICT,
    author_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    published_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT competition_documents_title_valid CHECK (title = btrim(title) AND char_length(title) BETWEEN 2 AND 180),
    CONSTRAINT competition_documents_url_valid CHECK (url ~ '^https://'),
    CONSTRAINT competition_documents_source_valid CHECK (source = btrim(source) AND char_length(source) BETWEEN 2 AND 180),
    CONSTRAINT competition_documents_context_valid CHECK (event_id IS NOT NULL OR modality_id IS NOT NULL),
    CONSTRAINT competition_documents_scope_valid CHECK (programme_id IS NOT NULL OR team_id IS NOT NULL OR event_id IS NOT NULL),
    CONSTRAINT competition_documents_modality_scope_valid CHECK (modality_id IS NULL OR programme_id IS NOT NULL OR team_id IS NOT NULL)
);
CREATE INDEX competition_documents_event_idx ON competition_documents (event_id, published_at DESC) WHERE event_id IS NOT NULL;
CREATE INDEX competition_documents_modality_idx ON competition_documents (modality_id, published_at DESC) WHERE modality_id IS NOT NULL;

-- +goose Down
DROP TABLE IF EXISTS competition_documents;
DROP TABLE IF EXISTS training_session_assignments;
DROP TABLE IF EXISTS training_sessions;
DROP TABLE IF EXISTS training_plans;
DROP TABLE IF EXISTS event_responses;
DROP TABLE IF EXISTS event_audiences;
DROP TABLE IF EXISTS events;
DROP TYPE IF EXISTS event_response_status;
