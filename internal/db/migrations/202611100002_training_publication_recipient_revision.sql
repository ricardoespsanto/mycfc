-- Audience/reference-only changes may require a separately reviewed revision
-- without editing a plan. Preserve all row IDs, immutable history and revision
-- uniqueness; never repair an unreviewed parent by silently ignoring drift.
DO $recipient_revision$
BEGIN
 IF NOT EXISTS (
  SELECT 1 FROM pg_catalog.pg_constraint AS constraint_row
  JOIN pg_catalog.pg_index AS index_row ON index_row.indexrelid OPERATOR(pg_catalog.=) constraint_row.conindid
  WHERE constraint_row.conrelid OPERATOR(pg_catalog.=) 'public.training_plan_publications'::pg_catalog.regclass
   AND constraint_row.conname OPERATOR(pg_catalog.=) 'training_plan_publications_source_unique'::pg_catalog.name
   AND constraint_row.contype OPERATOR(pg_catalog.=) 'u'::pg_catalog."char"
   AND constraint_row.convalidated AND NOT constraint_row.condeferrable AND NOT constraint_row.condeferred
   AND pg_catalog.pg_get_constraintdef(constraint_row.oid) OPERATOR(pg_catalog.=) 'UNIQUE (plan_id, source_updated_at)'::pg_catalog.text
   AND index_row.indisunique AND index_row.indisvalid AND index_row.indisready AND index_row.indislive
   AND index_row.indnkeyatts OPERATOR(pg_catalog.=) 2::pg_catalog.int2
   AND index_row.indnatts OPERATOR(pg_catalog.=) 2::pg_catalog.int2
   AND index_row.indpred IS NULL AND index_row.indexprs IS NULL
 ) THEN
  RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='training_recipient_parent_constraint_mismatch';
 END IF;
END;
$recipient_revision$;
ALTER TABLE public.training_plan_publications DROP CONSTRAINT training_plan_publications_source_unique;
