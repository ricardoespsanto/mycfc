CREATE TABLE IF NOT EXISTS equipment_audit_events (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  equipment_id uuid NOT NULL REFERENCES equipment(id) ON DELETE RESTRICT,
  actor_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  action varchar(20) NOT NULL CHECK (action IN ('CREATED', 'UPDATED', 'RETIRED', 'REACTIVATED')),
  before_state jsonb NULL,
  after_state jsonb NOT NULL,
  affected_maintenance_ids uuid[] NOT NULL DEFAULT '{}',
  occurred_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS equipment_audit_events_equipment_occurred_idx
  ON equipment_audit_events (equipment_id, occurred_at DESC, id DESC);

CREATE OR REPLACE FUNCTION prevent_equipment_audit_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'equipment audit events are append-only';
END;
$$;

DROP TRIGGER IF EXISTS equipment_audit_events_immutable_trigger ON equipment_audit_events;
CREATE TRIGGER equipment_audit_events_immutable_trigger
BEFORE UPDATE OR DELETE ON equipment_audit_events
FOR EACH ROW EXECUTE FUNCTION prevent_equipment_audit_mutation();
