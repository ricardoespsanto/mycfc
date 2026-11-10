-- TEST ONLY: C's final-reader contract fixture. Not embedded under migrations.
-- P must acquire state before conflicting application DDL and remain closed.
SELECT 1 FROM training_schedule_control.state WHERE singleton FOR UPDATE;
ALTER TABLE public.training_sessions ALTER COLUMN starts_at DROP NOT NULL;
ALTER TABLE public.training_sessions ALTER COLUMN ends_at DROP NOT NULL;
ALTER TABLE public.training_sessions DROP CONSTRAINT training_sessions_schedule_compatibility_only;
UPDATE training_schedule_control.state SET schema_contract='schedule-p1',state_epoch=state_epoch+1,changed_at=clock_timestamp() WHERE singleton;
