-- Remove private object locators from immutable fleet audit snapshots while
-- preserving the only audit meaning the UI needs: whether an image existed.
-- The trigger is dropped and restored in this transaction; PostgreSQL's
-- transactional DDL means no committed state exists without the guard.
CREATE OR REPLACE FUNCTION sanitize_equipment_audit_image_state() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE original_before jsonb; original_after jsonb;
BEGIN
  original_before := NEW.before_state;
  original_after := NEW.after_state;
  NEW.before_state := CASE WHEN original_before IS NULL THEN NULL ELSE
    (original_before - 'image_object_key') || jsonb_build_object(
      'has_image', CASE
        WHEN original_before ? 'image_object_key' THEN original_before->'image_object_key' <> 'null'::jsonb
        WHEN jsonb_typeof(original_before->'has_image') = 'boolean' THEN (original_before->>'has_image')::boolean
        ELSE false
      END)
    END;
  NEW.after_state := (original_after - 'image_object_key') || jsonb_build_object(
    'has_image', CASE
      WHEN original_after ? 'image_object_key' THEN original_after->'image_object_key' <> 'null'::jsonb
      WHEN jsonb_typeof(original_after->'has_image') = 'boolean' THEN (original_after->>'has_image')::boolean
      ELSE false
    END,
    'image_changed', CASE
      WHEN original_before ? 'image_object_key' OR original_after ? 'image_object_key'
        THEN original_before->'image_object_key' IS DISTINCT FROM original_after->'image_object_key'
      WHEN jsonb_typeof(original_after->'image_changed') = 'boolean' THEN (original_after->>'image_changed')::boolean
      ELSE false
    END);
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS equipment_audit_image_sanitization_trigger ON equipment_audit_events;
CREATE TRIGGER equipment_audit_image_sanitization_trigger
 BEFORE INSERT ON equipment_audit_events
 FOR EACH ROW EXECUTE FUNCTION sanitize_equipment_audit_image_state();

DROP TRIGGER equipment_audit_events_immutable_trigger ON equipment_audit_events;

UPDATE equipment_audit_events
SET before_state = CASE WHEN before_state IS NULL THEN NULL ELSE
    (before_state - 'image_object_key') || jsonb_build_object(
      'has_image', CASE
        WHEN before_state ? 'image_object_key' THEN before_state->'image_object_key' <> 'null'::jsonb
        WHEN jsonb_typeof(before_state->'has_image') = 'boolean' THEN (before_state->>'has_image')::boolean
        ELSE false
      END)
    END,
    after_state = (after_state - 'image_object_key') || jsonb_build_object(
      'has_image', CASE
        WHEN after_state ? 'image_object_key' THEN after_state->'image_object_key' <> 'null'::jsonb
        WHEN jsonb_typeof(after_state->'has_image') = 'boolean' THEN (after_state->>'has_image')::boolean
        ELSE false
      END,
      'image_changed', CASE
        WHEN before_state ? 'image_object_key' OR after_state ? 'image_object_key'
          THEN before_state->'image_object_key' IS DISTINCT FROM after_state->'image_object_key'
        WHEN jsonb_typeof(after_state->'image_changed') = 'boolean' THEN (after_state->>'image_changed')::boolean
        ELSE false
      END);

CREATE TRIGGER equipment_audit_events_immutable_trigger
 BEFORE UPDATE OR DELETE ON equipment_audit_events
 FOR EACH ROW EXECUTE FUNCTION prevent_equipment_audit_mutation();
