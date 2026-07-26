-- +goose Up
CREATE TYPE announcement_status AS ENUM ('DRAFT', 'PUBLISHED', 'EXPIRED');
CREATE TYPE announcement_target_type AS ENUM ('PROGRAMME', 'TEAM', 'CATEGORY', 'MODALITY', 'EVENT', 'GUARDIAN');

CREATE TABLE announcements (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    title varchar(180) NOT NULL,
    body varchar(4000) NOT NULL,
    status announcement_status NOT NULL DEFAULT 'DRAFT',
    author_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    published_by_id uuid NULL REFERENCES users(id) ON DELETE RESTRICT,
    expired_by_id uuid NULL REFERENCES users(id) ON DELETE RESTRICT,
    published_at timestamptz NULL,
    expires_at timestamptz NULL,
    expired_at timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT announcements_title_valid CHECK (title = btrim(title) AND char_length(title) BETWEEN 2 AND 180),
    CONSTRAINT announcements_body_valid CHECK (body = btrim(body) AND char_length(body) BETWEEN 2 AND 4000),
    CONSTRAINT announcements_status_valid CHECK (
        (status = 'DRAFT' AND published_at IS NULL AND published_by_id IS NULL AND expired_at IS NULL AND expired_by_id IS NULL)
        OR (status = 'PUBLISHED' AND published_at IS NOT NULL AND published_by_id IS NOT NULL AND expired_at IS NULL AND expired_by_id IS NULL)
        OR (status = 'EXPIRED' AND published_at IS NOT NULL AND published_by_id IS NOT NULL AND expired_at IS NOT NULL AND expired_by_id IS NOT NULL)
    ),
    CONSTRAINT announcements_expiry_valid CHECK (expires_at IS NULL OR published_at IS NULL OR expires_at > published_at)
);
CREATE INDEX announcements_visible_idx ON announcements (status, published_at DESC, expires_at);

CREATE TABLE announcement_targets (
    announcement_id uuid NOT NULL REFERENCES announcements(id) ON DELETE CASCADE,
    target_type announcement_target_type NOT NULL,
    target_id uuid NULL,
    CONSTRAINT announcement_targets_shape_valid CHECK (
        (target_type = 'GUARDIAN' AND target_id IS NULL)
        OR (target_type <> 'GUARDIAN' AND target_id IS NOT NULL)
    )
);
CREATE UNIQUE INDEX announcement_targets_unique_idx ON announcement_targets (announcement_id, target_type, target_id) NULLS NOT DISTINCT;
CREATE INDEX announcement_targets_lookup_idx ON announcement_targets (target_type, target_id, announcement_id);

-- Delivery is created on first successful visibility. Reading is recorded on the
-- detail page; no background or chat delivery is implied.
CREATE TABLE announcement_deliveries (
    announcement_id uuid NOT NULL REFERENCES announcements(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    delivered_at timestamptz NOT NULL DEFAULT now(),
    read_at timestamptz NULL,
    PRIMARY KEY (announcement_id, user_id)
);
CREATE INDEX announcement_deliveries_user_idx ON announcement_deliveries (user_id, read_at, delivered_at DESC);

CREATE TABLE announcement_audit_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    announcement_id uuid NOT NULL REFERENCES announcements(id) ON DELETE RESTRICT,
    action varchar(20) NOT NULL,
    actor_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    occurred_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT announcement_audit_action_valid CHECK (action IN ('AUTHORED', 'PUBLISHED', 'EXPIRED'))
);
CREATE INDEX announcement_audit_events_announcement_idx ON announcement_audit_events (announcement_id, occurred_at);

CREATE TABLE whatsapp_group_targets (
    whatsapp_group_id uuid NOT NULL REFERENCES whatsapp_groups(id) ON DELETE CASCADE,
    target_type announcement_target_type NOT NULL,
    target_id uuid NULL,
    CONSTRAINT whatsapp_group_targets_shape_valid CHECK (
        (target_type = 'GUARDIAN' AND target_id IS NULL)
        OR (target_type <> 'GUARDIAN' AND target_id IS NOT NULL)
    )
);
CREATE UNIQUE INDEX whatsapp_group_targets_unique_idx ON whatsapp_group_targets (whatsapp_group_id, target_type, target_id) NULLS NOT DISTINCT;
CREATE INDEX whatsapp_group_targets_lookup_idx ON whatsapp_group_targets (target_type, target_id, whatsapp_group_id);

-- Preserve existing programme-curated links while moving audience matching to
-- the same selectors used by announcements.
INSERT INTO whatsapp_group_targets (whatsapp_group_id, target_type, target_id)
SELECT id, 'PROGRAMME', programme_id FROM whatsapp_groups WHERE programme_id IS NOT NULL;

-- +goose StatementBegin
CREATE FUNCTION audit_announcement_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        INSERT INTO announcement_audit_events (announcement_id, action, actor_user_id) VALUES (NEW.id, 'AUTHORED', NEW.author_id);
    ELSIF OLD.status = 'DRAFT' AND NEW.status = 'PUBLISHED' THEN
        INSERT INTO announcement_audit_events (announcement_id, action, actor_user_id) VALUES (NEW.id, 'PUBLISHED', NEW.published_by_id);
    ELSIF OLD.status = 'PUBLISHED' AND NEW.status = 'EXPIRED' THEN
        INSERT INTO announcement_audit_events (announcement_id, action, actor_user_id) VALUES (NEW.id, 'EXPIRED', NEW.expired_by_id);
    ELSE
        RAISE EXCEPTION 'announcements may only be authored, published, or expired';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER announcements_audit_trigger AFTER INSERT OR UPDATE ON announcements FOR EACH ROW EXECUTE FUNCTION audit_announcement_change();

-- +goose Down
DROP TRIGGER IF EXISTS announcements_audit_trigger ON announcements;
DROP FUNCTION IF EXISTS audit_announcement_change;
DROP TABLE IF EXISTS whatsapp_group_targets;
DROP TABLE IF EXISTS announcement_audit_events;
DROP TABLE IF EXISTS announcement_deliveries;
DROP TABLE IF EXISTS announcement_targets;
DROP TABLE IF EXISTS announcements;
DROP TYPE IF EXISTS announcement_target_type;
DROP TYPE IF EXISTS announcement_status;
