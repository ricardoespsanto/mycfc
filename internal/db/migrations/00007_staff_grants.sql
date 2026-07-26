-- +goose Up
CREATE TYPE staff_capability AS ENUM ('COACH', 'MODERATOR');

CREATE TABLE staff_grants (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    capability staff_capability NOT NULL,
    programme_id uuid NULL REFERENCES programmes(id) ON DELETE RESTRICT,
    team_id uuid NULL REFERENCES teams(id) ON DELETE RESTRICT,
    granted_by_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    granted_at timestamptz NOT NULL DEFAULT now(),
    revoked_by_id uuid NULL REFERENCES users(id) ON DELETE RESTRICT,
    revoked_at timestamptz NULL,
    revoke_reason varchar(500) NULL,
    CONSTRAINT staff_grants_scope_valid CHECK (
        (capability = 'COACH' AND (programme_id IS NOT NULL OR team_id IS NOT NULL))
        OR (capability = 'MODERATOR' AND programme_id IS NULL AND team_id IS NULL)
    ),
    CONSTRAINT staff_grants_revocation_valid CHECK (
        (revoked_at IS NULL AND revoked_by_id IS NULL AND revoke_reason IS NULL)
        OR (revoked_at IS NOT NULL AND revoked_by_id IS NOT NULL AND revoke_reason = btrim(revoke_reason) AND char_length(revoke_reason) BETWEEN 1 AND 500)
    )
);

CREATE UNIQUE INDEX staff_grants_active_scope_uidx
    ON staff_grants (user_id, capability, COALESCE(programme_id, '00000000-0000-0000-0000-000000000000'::uuid), COALESCE(team_id, '00000000-0000-0000-0000-000000000000'::uuid))
    WHERE revoked_at IS NULL;
CREATE INDEX staff_grants_active_user_idx ON staff_grants (user_id) WHERE revoked_at IS NULL;

-- Append-only record of every grant and revocation for operational review.
CREATE TABLE staff_grant_audit_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    staff_grant_id uuid NOT NULL REFERENCES staff_grants(id) ON DELETE RESTRICT,
    action varchar(20) NOT NULL,
    actor_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    occurred_at timestamptz NOT NULL DEFAULT now(),
    reason varchar(500) NULL,
    CONSTRAINT staff_grant_audit_events_action_valid CHECK (action IN ('GRANTED', 'REVOKED')),
    CONSTRAINT staff_grant_audit_events_reason_valid CHECK (reason IS NULL OR (reason = btrim(reason) AND char_length(reason) BETWEEN 1 AND 500))
);

CREATE INDEX staff_grant_audit_events_grant_idx ON staff_grant_audit_events (staff_grant_id, occurred_at);

CREATE FUNCTION audit_staff_grant_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        INSERT INTO staff_grant_audit_events (staff_grant_id, action, actor_user_id)
        VALUES (NEW.id, 'GRANTED', NEW.granted_by_id);
        RETURN NEW;
    END IF;
    IF OLD.user_id <> NEW.user_id OR OLD.capability <> NEW.capability
       OR OLD.programme_id IS DISTINCT FROM NEW.programme_id OR OLD.team_id IS DISTINCT FROM NEW.team_id
       OR OLD.granted_by_id <> NEW.granted_by_id OR OLD.granted_at <> NEW.granted_at
       OR OLD.revoked_at IS NOT NULL OR NEW.revoked_at IS NULL THEN
        RAISE EXCEPTION 'staff grants are immutable except for one revocation';
    END IF;
    INSERT INTO staff_grant_audit_events (staff_grant_id, action, actor_user_id, occurred_at, reason)
    VALUES (NEW.id, 'REVOKED', NEW.revoked_by_id, NEW.revoked_at, NEW.revoke_reason);
    RETURN NEW;
END;
$$;

CREATE TRIGGER staff_grants_audit_trigger
AFTER INSERT OR UPDATE ON staff_grants
FOR EACH ROW EXECUTE FUNCTION audit_staff_grant_change();

CREATE FUNCTION prevent_staff_grant_audit_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'staff grant audit events are append-only';
END;
$$;

CREATE TRIGGER staff_grant_audit_events_immutable_trigger
BEFORE UPDATE OR DELETE ON staff_grant_audit_events
FOR EACH ROW EXECUTE FUNCTION prevent_staff_grant_audit_mutation();

CREATE TABLE event_team_audiences (
    event_id uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    team_id uuid NOT NULL REFERENCES teams(id) ON DELETE RESTRICT,
    PRIMARY KEY (event_id, team_id)
);

CREATE INDEX event_team_audiences_team_idx ON event_team_audiences (team_id, event_id);

-- +goose Down
DROP TABLE IF EXISTS event_team_audiences;
DROP FUNCTION IF EXISTS prevent_staff_grant_audit_mutation;
DROP FUNCTION IF EXISTS audit_staff_grant_change;
DROP TABLE IF EXISTS staff_grant_audit_events;
DROP TABLE IF EXISTS staff_grants;
DROP TYPE IF EXISTS staff_capability;
