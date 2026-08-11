CREATE TYPE suggestion_category AS ENUM ('FACILITIES', 'EQUIPMENT', 'TRAINING', 'EVENTS', 'COMMUNICATION', 'OTHER');
CREATE TYPE suggestion_status AS ENUM ('SUBMITTED', 'UNDER_REVIEW', 'PLANNED', 'DECLINED', 'COMPLETED');

CREATE TABLE suggestions (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 requester_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 category suggestion_category NOT NULL,
 subject varchar(160) NOT NULL,
 description varchar(3000) NOT NULL,
 status suggestion_status NOT NULL DEFAULT 'SUBMITTED',
 staff_response varchar(2000) NULL,
 responded_by_id uuid NULL REFERENCES users(id) ON DELETE RESTRICT,
 responded_at timestamptz NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 CONSTRAINT suggestions_subject_valid CHECK (subject = btrim(subject) AND char_length(subject) BETWEEN 3 AND 160),
 CONSTRAINT suggestions_description_valid CHECK (description = btrim(description) AND char_length(description) BETWEEN 10 AND 3000),
 CONSTRAINT suggestions_response_valid CHECK (staff_response IS NULL OR (staff_response = btrim(staff_response) AND char_length(staff_response) BETWEEN 2 AND 2000)),
 CONSTRAINT suggestions_response_actor_valid CHECK ((staff_response IS NULL AND responded_by_id IS NULL AND responded_at IS NULL) OR (staff_response IS NOT NULL AND responded_by_id IS NOT NULL AND responded_at IS NOT NULL)),
 CONSTRAINT suggestions_terminal_response_valid CHECK (status NOT IN ('DECLINED', 'COMPLETED') OR staff_response IS NOT NULL)
);

CREATE INDEX suggestions_requester_created_idx ON suggestions (requester_id, created_at DESC, id DESC);
CREATE INDEX suggestions_triage_idx ON suggestions (status, updated_at DESC, id DESC);
