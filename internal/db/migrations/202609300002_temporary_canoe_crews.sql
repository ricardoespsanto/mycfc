-- Adapt #162's dated variation crews to #336 Canoagem craft IDs without touching published prescriptions.
ALTER TABLE training_variation_groups ADD COLUMN craft_code varchar(2) REFERENCES canoe_craft_classes(code) ON UPDATE RESTRICT;
UPDATE training_variation_groups crew SET craft_code = modality.code
FROM modalities modality WHERE crew.craft_modality_id = modality.id AND crew.kind = 'CREW'
  AND modality.code IN ('C2','K2','K4');
-- Fail closed rather than silently reclassifying an existing unsupported crew.
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM training_variation_groups WHERE kind = 'CREW' AND craft_code IS NULL) THEN
  RAISE EXCEPTION 'unsupported legacy crew modality requires explicit correction before migration';
 END IF;
END $$;
ALTER TABLE training_variation_groups DROP CONSTRAINT training_variation_groups_shape_valid;
ALTER TABLE training_variation_groups DROP COLUMN craft_modality_id;
ALTER TABLE training_variation_groups ADD CONSTRAINT training_variation_groups_shape_valid
 CHECK ((kind = 'SUBGROUP' AND craft_code IS NULL AND competition_event_id IS NULL AND NOT open_ended_exception)
 OR (kind = 'CREW' AND craft_code IS NOT NULL AND craft_code IN ('C2','K2','K4')
 AND (effective_until IS NOT NULL OR competition_event_id IS NOT NULL OR open_ended_exception)));
