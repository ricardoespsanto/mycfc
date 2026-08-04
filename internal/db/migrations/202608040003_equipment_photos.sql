ALTER TABLE equipment
  ADD COLUMN IF NOT EXISTS image_object_key varchar(512) NULL,
  ADD COLUMN IF NOT EXISTS image_content_type varchar(100) NULL,
  ADD COLUMN IF NOT EXISTS image_size_bytes bigint NULL;

DO $$ BEGIN
  ALTER TABLE equipment ADD CONSTRAINT equipment_image_metadata_complete CHECK (
    (image_object_key IS NULL AND image_content_type IS NULL AND image_size_bytes IS NULL)
    OR (image_object_key IS NOT NULL AND image_content_type IS NOT NULL AND image_size_bytes IS NOT NULL)
  );
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
  ALTER TABLE equipment ADD CONSTRAINT equipment_image_size_valid
    CHECK (image_size_bytes IS NULL OR image_size_bytes BETWEEN 1 AND 10485760);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
