-- Shape definitions and private schedule metadata only. No application rows
-- are converted, backfilled, published, removed or assigned guessed times.
DO $$
DECLARE current_state training_schedule_control.state%ROWTYPE;
BEGIN
 SELECT * INTO STRICT current_state FROM training_schedule_control.state WHERE singleton FOR UPDATE;
 IF current_state.schema_contract<>'schedule-c1' OR current_state.partial_v2_enabled OR current_state.ever_partial_v2_enabled THEN
  RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='training_partial_authoring_requires_closed_parent';
 END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_catalog.pg_constraint WHERE conrelid='public.training_sessions'::pg_catalog.regclass
  AND conname='training_sessions_schedule_compatibility_only' AND convalidated
  AND pg_catalog.pg_get_constraintdef(oid)='CHECK ((schedule_precision = ''INTERVAL''::text))') THEN
  RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='training_partial_authoring_parent_shape_mismatch';
 END IF;
 ALTER TABLE public.training_sessions ALTER COLUMN starts_at DROP NOT NULL;
 ALTER TABLE public.training_sessions ALTER COLUMN ends_at DROP NOT NULL;
 ALTER TABLE public.training_sessions DROP CONSTRAINT training_sessions_schedule_compatibility_only;
 ALTER TABLE training_schedule_control.state DROP CONSTRAINT training_schedule_enable_valid;
 ALTER TABLE training_schedule_control.state DROP CONSTRAINT training_schedule_ever_floor;
 ALTER TABLE training_schedule_control.state ADD CONSTRAINT training_schedule_enable_valid CHECK(NOT partial_v2_enabled OR
  (schema_contract='schedule-p1' AND required_reader>=3 AND floor_commit_id IS NOT NULL AND ever_partial_v2_enabled));
 ALTER TABLE training_schedule_control.state ADD CONSTRAINT training_schedule_ever_floor CHECK(NOT ever_partial_v2_enabled OR required_reader>=3);
 ALTER TABLE training_schedule_control.state ALTER COLUMN schema_contract SET DEFAULT 'schedule-p1';
 UPDATE training_schedule_control.state SET schema_contract='schedule-p1',state_epoch=state_epoch+1,changed_at=clock_timestamp() WHERE singleton;
END $$;
