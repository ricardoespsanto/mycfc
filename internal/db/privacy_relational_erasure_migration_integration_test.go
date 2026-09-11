//go:build integration

package db

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// TestPrivacyRelationalErasureForwardMigrationAppliesToExactPriorSchema starts
// from the current baseline, reverses exactly the #245 baseline delta, and then
// applies the real forward migration. This catches ordering and dependency
// errors that a fresh-baseline test cannot expose.
func TestPrivacyRelationalErasureForwardMigrationAppliesToExactPriorSchema(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	schemaName := "privacy_relational_migration_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	protectedSchemaName := "privacy_protected_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	disableSchemaName := "privacy_disable_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	schema := pgx.Identifier{schemaName}.Sanitize()
	if _, err = conn.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = conn.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		_, _ = conn.Exec(ctx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{protectedSchemaName}.Sanitize()+" CASCADE")
		_, _ = conn.Exec(ctx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{disableSchemaName}.Sanitize()+" CASCADE")
	}()
	if _, err = conn.Exec(ctx, "SET search_path TO "+schema+",public"); err != nil {
		t.Fatal(err)
	}
	isolatedBaseline := strings.ReplaceAll(baselineSchema, "public.", schemaName+".")
	isolatedBaseline = strings.ReplaceAll(isolatedBaseline, "pg_catalog, public", "pg_catalog, "+schemaName+", public")
	isolatedBaseline = strings.ReplaceAll(isolatedBaseline, "pg_catalog,public", "pg_catalog,"+schemaName+",public")
	isolatedBaseline = strings.ReplaceAll(isolatedBaseline, "privacy_protected", protectedSchemaName)
	isolatedBaseline = strings.ReplaceAll(isolatedBaseline, "privacy_disable", disableSchemaName)
	if _, err = conn.PgConn().Exec(ctx, isolatedBaseline).ReadAll(); err != nil {
		t.Fatalf("create isolated baseline: %v", err)
	}
	if _, err = conn.Exec(ctx, pre245SchemaRollback); err != nil {
		t.Fatalf("reconstruct exact pre-#245 schema: %v", err)
	}

	userID := uuid.New()
	if _, err = conn.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,is_dependent,date_of_birth,is_active,created_at,updated_at)
		VALUES($1,'Existing member',$2,'hash',false,'1990-01-01',true,now(),now())`, userID, "pre-245-"+uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	var priorHasErasedAt, priorHasExecutor bool
	if err = conn.QueryRow(ctx, `SELECT
		EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid='users'::regclass AND attname='erased_at' AND NOT attisdropped),
		EXISTS(SELECT 1 FROM pg_proc procedure JOIN pg_namespace namespace ON namespace.oid=procedure.pronamespace
		 WHERE namespace.nspname=current_schema() AND procedure.proname='privacy_worker_execute_checkpoint' AND procedure.pronargs=7)`).Scan(&priorHasErasedAt, &priorHasExecutor); err != nil {
		t.Fatal(err)
	}
	if priorHasErasedAt || priorHasExecutor {
		t.Fatalf("fixture is not pre-#245: erased_at=%v executor=%v", priorHasErasedAt, priorHasExecutor)
	}

	migration, err := migrationFiles.ReadFile("migrations/202609090001_privacy_relational_erasure.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, string(migration)); err != nil {
		t.Fatalf("apply #245 forward migration: %v", err)
	}

	var name, email string
	var erasedAtIsNull, anchorTableExists, executorExists bool
	if err = conn.QueryRow(ctx, `SELECT name,email,erased_at IS NULL FROM users WHERE id=$1`, userID).Scan(&name, &email, &erasedAtIsNull); err != nil {
		t.Fatal(err)
	}
	if err = conn.QueryRow(ctx, `SELECT
		EXISTS(SELECT 1 FROM pg_class relation JOIN pg_namespace namespace ON namespace.oid=relation.relnamespace
		 WHERE namespace.nspname=current_schema() AND relation.relname='privacy_erasure_retention_anchors'),
		EXISTS(SELECT 1 FROM pg_proc procedure JOIN pg_namespace namespace ON namespace.oid=procedure.pronamespace
		 WHERE namespace.nspname=current_schema() AND procedure.proname='privacy_worker_execute_checkpoint' AND procedure.pronargs=7)`).Scan(&anchorTableExists, &executorExists); err != nil {
		t.Fatal(err)
	}
	if name != "Existing member" || !strings.HasPrefix(email, "pre-245-") || !erasedAtIsNull || !anchorTableExists || !executorExists {
		t.Fatalf("migration result name=%q email=%q erased_null=%v anchors=%v executor=%v", name, email, erasedAtIsNull, anchorTableExists, executorExists)
	}
}

// This is the inverse of the #245 additions in schema.sql. Definitions that
// #245 replaces are restored byte-for-byte to their immediately preceding
// #244 shape so the test exercises CREATE OR REPLACE compatibility too.
const pre245SchemaRollback = `
DROP TRIGGER minor_credential_audit_immutable_trigger ON minor_credential_audit;
DROP TRIGGER announcement_audit_events_immutable_trigger ON announcement_audit_events;
DROP FUNCTION prevent_minor_credential_audit_mutation();
DROP FUNCTION prevent_announcement_audit_mutation();
CREATE OR REPLACE FUNCTION prevent_equipment_audit_mutation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'equipment audit events are append-only'; END; $$;
CREATE OR REPLACE FUNCTION prevent_member_profile_audit_mutation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'member profile audit events are append-only'; END; $$;
CREATE OR REPLACE FUNCTION prevent_staff_grant_audit_mutation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'staff grant audit events are append-only'; END; $$;
CREATE OR REPLACE FUNCTION prevent_photo_album_audit_mutation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'photo album audit events are append-only'; END; $$;
CREATE OR REPLACE FUNCTION prevent_feature_flag_event_mutation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'feature flag events are append-only'; END; $$;
CREATE OR REPLACE FUNCTION prevent_training_copy_event_mutation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'training copy events are append-only'; END; $$;
ALTER TABLE minor_credential_audit
 DROP CONSTRAINT minor_credential_audit_minor_principal_exactly_one,
 DROP CONSTRAINT minor_credential_audit_guardian_principal_exactly_one,
 DROP CONSTRAINT minor_credential_audit_actor_principal_exactly_one,
 DROP COLUMN minor_principal_id,DROP COLUMN guardian_principal_id,DROP COLUMN actor_principal_id,
 ALTER COLUMN minor_user_id SET NOT NULL,ALTER COLUMN guardian_user_id SET NOT NULL,ALTER COLUMN actor_user_id SET NOT NULL;
ALTER TABLE equipment_audit_events DROP CONSTRAINT equipment_audit_actor_principal_exactly_one,
 DROP COLUMN actor_principal_id,ALTER COLUMN actor_user_id SET NOT NULL;
ALTER TABLE member_profile_audit_events
 DROP CONSTRAINT member_profile_audit_actor_principal_exactly_one,
 DROP CONSTRAINT member_profile_audit_subject_principal_exactly_one,
 DROP COLUMN actor_principal_id,DROP COLUMN subject_principal_id,
 ALTER COLUMN actor_user_id SET NOT NULL,ALTER COLUMN subject_user_id SET NOT NULL;
ALTER TABLE staff_grant_audit_events DROP CONSTRAINT staff_grant_audit_actor_principal_exactly_one,
 DROP COLUMN actor_principal_id,ALTER COLUMN actor_user_id SET NOT NULL;
ALTER TABLE photo_album_audit_events DROP CONSTRAINT photo_album_audit_actor_principal_exactly_one,
 DROP COLUMN actor_principal_id,ALTER COLUMN actor_user_id SET NOT NULL;
ALTER TABLE announcement_audit_events DROP CONSTRAINT announcement_audit_actor_principal_exactly_one,
 DROP COLUMN actor_principal_id,ALTER COLUMN actor_user_id SET NOT NULL;
ALTER TABLE feature_flag_events DROP CONSTRAINT feature_flag_audit_actor_principal_exactly_one,
 DROP COLUMN actor_principal_id,ALTER COLUMN actor_user_id SET NOT NULL;
ALTER TABLE training_copy_events DROP CONSTRAINT training_copy_actor_principal_exactly_one,
 DROP COLUMN copied_by_principal_id,ALTER COLUMN copied_by_id SET NOT NULL;
ALTER TABLE user_memberships DROP CONSTRAINT user_memberships_subject_principal_exactly_one,
 DROP COLUMN principal_id,ALTER COLUMN user_id SET NOT NULL;
DROP TABLE privacy_pseudonymous_principals;
DROP FUNCTION privacy_scrub_audit_json(jsonb,uuid,text,text,text);
DROP FUNCTION privacy_scrub_audit_text(text,uuid,text,text,text);
DROP FUNCTION privacy_worker_execute_checkpoint(uuid,uuid,uuid,bigint,uuid,text,text);
DROP TRIGGER training_outcomes_active_subject_attachment ON training_session_outcomes;
DROP TRIGGER training_prescriptions_active_subject_attachment ON training_prescriptions;
DROP TRIGGER suggestions_active_subject_attachment ON suggestions;
DROP TRIGGER training_activity_matches_active_subject_attachment ON training_session_activity_matches;
DROP TRIGGER synced_activities_active_subject_attachment ON synced_activities;
DROP TRIGGER activity_connections_active_subject_attachment ON activity_connections;
DROP TRIGGER announcement_deliveries_active_subject_attachment ON announcement_deliveries;
DROP TRIGGER event_responses_active_subject_attachment ON event_responses;
DROP TRIGGER user_memberships_active_subject_attachment ON user_memberships;
DROP TRIGGER performance_metrics_active_subject_attachment ON performance_metrics;
DROP TRIGGER training_logs_active_subject_attachment ON training_logs;
DROP TRIGGER member_profiles_active_subject_attachment ON member_profiles;
DROP TRIGGER consent_forms_active_subject_attachment ON consent_forms;
DROP TRIGGER users_erased_immutable ON users;
DROP TRIGGER users_consent_cessation ON users;
DROP FUNCTION privacy_consent_cease_on_erasure();
DROP FUNCTION prevent_erased_user_reidentification();
DROP TABLE privacy_erasure_restricted_records,privacy_erasure_retention_anchors;
ALTER TABLE privacy_erasure_job_checkpoints DROP CONSTRAINT privacy_erasure_checkpoint_result_complete;
ALTER TABLE privacy_erasure_job_checkpoints DROP COLUMN affected_rows,DROP COLUMN result_sha256;
ALTER TABLE users DROP CONSTRAINT users_identity_shape;
ALTER TABLE users DROP CONSTRAINT users_name_valid;
DROP INDEX users_erasure_execution_uidx;
ALTER TABLE users DROP COLUMN erasure_execution_id,DROP COLUMN erased_at;
ALTER TABLE users ADD CONSTRAINT users_name_valid CHECK(name=btrim(name) AND char_length(name) BETWEEN 2 AND 120);
ALTER TABLE users ADD CONSTRAINT users_identity_shape CHECK((is_dependent AND guardian_id IS NOT NULL AND email IS NULL AND ((minor_login_id IS NULL AND password_hash IS NULL) OR (minor_login_id IS NOT NULL AND password_hash IS NOT NULL))) OR (NOT is_dependent AND guardian_id IS NULL AND email IS NOT NULL AND password_hash IS NOT NULL AND minor_login_id IS NULL));

CREATE OR REPLACE FUNCTION require_active_privacy_attachment_subject() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE subject_id uuid;
BEGIN
 IF TG_TABLE_NAME='users' THEN subject_id:=NULLIF(to_jsonb(NEW)->>'guardian_id','')::uuid;
 ELSE subject_id:=NULLIF(to_jsonb(NEW)->>'user_id','')::uuid;
 END IF;
 IF subject_id IS NULL THEN RETURN NEW; END IF;
 PERFORM 1 FROM users WHERE id=subject_id AND is_active FOR KEY SHARE;
 IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='inactive_privacy_attachment_subject'; END IF;
 RETURN NEW;
END; $$;

CREATE OR REPLACE FUNCTION prevent_training_publication_mutation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'training publications and prescriptions are immutable'; END; $$;

CREATE OR REPLACE FUNCTION privacy_worker_claim(p_lease_milliseconds bigint, p_worker_ref uuid)
RETURNS TABLE(job_id uuid, lease_id uuid, attempt_id uuid)
LANGUAGE sql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
WITH candidate AS MATERIALIZED (
 SELECT job.id,job.status,job.lease_epoch
 FROM public.privacy_erasure_category_jobs job
 WHERE p_lease_milliseconds BETWEEN 1000 AND 3600000
 AND ((job.status IN ('PENDING','RETRY_WAIT') AND job.next_attempt_at<=clock_timestamp())
    OR (job.status='LEASED' AND EXISTS(
      SELECT 1 FROM public.privacy_erasure_job_leases lease
      WHERE lease.job_id=job.id AND lease.epoch=job.lease_epoch AND lease.released_at IS NULL AND lease.expires_at<=clock_timestamp()
    )))
 ORDER BY job.next_attempt_at,job.created_at,job.id
 FOR UPDATE SKIP LOCKED LIMIT 1
), authority_clock AS MATERIALIZED (
 SELECT clock_timestamp() AS occurred_at FROM candidate LIMIT 1
), expired_lease AS (
 UPDATE public.privacy_erasure_job_leases lease SET released_at=authority_clock.occurred_at,outcome='EXPIRED'
 FROM candidate,authority_clock WHERE lease.job_id=candidate.id AND lease.epoch=candidate.lease_epoch
 AND candidate.status='LEASED' AND lease.released_at IS NULL AND lease.expires_at<=authority_clock.occurred_at
 RETURNING lease.id,lease.job_id
), expired_attempt AS (
 UPDATE public.privacy_erasure_job_attempts attempt SET finished_at=authority_clock.occurred_at,outcome='LEASE_EXPIRED'
 FROM expired_lease,authority_clock WHERE attempt.lease_id=expired_lease.id AND attempt.finished_at IS NULL
 RETURNING attempt.id
), claimed_job AS (
 UPDATE public.privacy_erasure_category_jobs job SET status='LEASED',lease_epoch=job.lease_epoch+1,
 attempt_count=job.attempt_count+1,updated_at=authority_clock.occurred_at
 FROM candidate,authority_clock WHERE job.id=candidate.id AND (candidate.status<>'LEASED' OR EXISTS(SELECT 1 FROM expired_lease))
 RETURNING job.*
), new_lease AS (
 INSERT INTO public.privacy_erasure_job_leases(job_id,epoch,worker_ref,acquired_at,heartbeat_at,expires_at)
 SELECT id,lease_epoch,p_worker_ref,authority_clock.occurred_at,authority_clock.occurred_at,
 authority_clock.occurred_at+(p_lease_milliseconds*INTERVAL '1 millisecond')
 FROM claimed_job,authority_clock RETURNING *
), new_attempt AS (
 INSERT INTO public.privacy_erasure_job_attempts(job_id,lease_id,lease_epoch,attempt_number,started_at)
 SELECT claimed_job.id,new_lease.id,new_lease.epoch,claimed_job.attempt_count,authority_clock.occurred_at
 FROM claimed_job JOIN new_lease ON new_lease.job_id=claimed_job.id CROSS JOIN authority_clock RETURNING *
), execution_started AS (
 UPDATE public.privacy_erasure_executions execution SET status='RUNNING',version=execution.version+1,
 started_at=COALESCE(execution.started_at,authority_clock.occurred_at),finished_at=NULL,updated_at=authority_clock.occurred_at
 FROM claimed_job,authority_clock WHERE execution.id=claimed_job.execution_id AND execution.status NOT IN ('SUCCEEDED','TERMINAL_FAILED') RETURNING execution.id
)
SELECT claimed_job.id,new_lease.id,new_attempt.id
FROM claimed_job JOIN new_lease ON new_lease.job_id=claimed_job.id JOIN new_attempt ON new_attempt.job_id=claimed_job.id
WHERE EXISTS(SELECT 1 FROM execution_started);
$$;
`
