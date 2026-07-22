-- +goose Up
CREATE TABLE training_logs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    occurred_at timestamptz NOT NULL,
    duration_seconds integer NOT NULL,
    distance_metres integer NOT NULL,
    notes varchar(2000) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT training_duration_valid CHECK (duration_seconds BETWEEN 60 AND 86400),
    CONSTRAINT training_distance_valid CHECK (distance_metres BETWEEN 0 AND 200000),
    CONSTRAINT training_notes_valid CHECK (char_length(notes) <= 2000)
);

CREATE INDEX training_user_occurred_idx ON training_logs (user_id, occurred_at DESC);

CREATE TABLE performance_metrics (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    metric_type metric_type NOT NULL,
    label_pt varchar(100) NOT NULL,
    value numeric(12,2) NOT NULL,
    unit_pt varchar(30) NOT NULL,
    measured_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT performance_label_valid CHECK (
        label_pt = btrim(label_pt)
        AND char_length(label_pt) BETWEEN 1 AND 100
    ),
    CONSTRAINT performance_unit_valid CHECK (char_length(unit_pt) <= 30)
);

CREATE INDEX performance_user_measured_idx ON performance_metrics (user_id, measured_at DESC);

CREATE TABLE news_items (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    title_pt varchar(180) NOT NULL,
    summary_pt varchar(1000) NOT NULL,
    url text NULL,
    published_at timestamptz NOT NULL,
    is_published boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT news_title_valid CHECK (
        title_pt = btrim(title_pt)
        AND char_length(title_pt) BETWEEN 2 AND 180
    ),
    CONSTRAINT news_summary_valid CHECK (
        summary_pt = btrim(summary_pt)
        AND char_length(summary_pt) BETWEEN 2 AND 1000
    ),
    CONSTRAINT news_url_valid CHECK (url IS NULL OR url ~ '^https://')
);

CREATE INDEX news_published_date_idx ON news_items (is_published, published_at DESC);

CREATE TABLE maintenance_tasks (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    equipment_id uuid NOT NULL REFERENCES equipment(id) ON DELETE RESTRICT,
    scheduled_for timestamptz NOT NULL,
    description varchar(2000) NOT NULL,
    status maintenance_status NOT NULL DEFAULT 'Scheduled',
    created_by_id uuid NULL REFERENCES users(id) ON DELETE SET NULL,
    completed_at timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT maintenance_description_valid CHECK (
        description = btrim(description)
        AND char_length(description) BETWEEN 10 AND 2000
    ),
    CONSTRAINT maintenance_completion_valid CHECK (
        (status = 'Completed' AND completed_at IS NOT NULL)
        OR (status <> 'Completed' AND completed_at IS NULL)
    )
);

CREATE INDEX maintenance_status_scheduled_idx ON maintenance_tasks (status, scheduled_for);
CREATE INDEX maintenance_equipment_id_idx ON maintenance_tasks (equipment_id);

-- +goose Down
DROP TABLE IF EXISTS maintenance_tasks;
DROP TABLE IF EXISTS news_items;
DROP TABLE IF EXISTS performance_metrics;
DROP TABLE IF EXISTS training_logs;
