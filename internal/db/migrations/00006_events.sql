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

-- +goose Down
DROP TABLE IF EXISTS event_responses;
DROP TABLE IF EXISTS event_audiences;
DROP TABLE IF EXISTS events;
DROP TYPE IF EXISTS event_response_status;
