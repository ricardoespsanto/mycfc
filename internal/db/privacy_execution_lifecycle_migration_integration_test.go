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

// This fixture starts from the complete baseline, removes exactly the #244
// additions to reproduce the immediately preceding #243 schema, then applies
// the forward migration. Keeping the subtraction here makes the compatibility
// test small while still exercising the real baseline and real migration SQL.
func TestPrivacyExecutionLifecycleForwardMigrationPreservesPriorRows(t *testing.T) {
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
	schemaName := "privacy_lifecycle_migration_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	schema := pgx.Identifier{schemaName}.Sanitize()
	if _, err = conn.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = conn.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE") }()
	if _, err = conn.Exec(ctx, "SET search_path TO "+schema+",public"); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.PgConn().Exec(ctx, baselineSchema).ReadAll(); err != nil {
		t.Fatalf("create isolated baseline: %v", err)
	}

	rollback244 := `
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
CREATE OR REPLACE FUNCTION prevent_training_publication_mutation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'training publications and prescriptions are immutable'; END; $$;
DROP FUNCTION privacy_worker_sync(uuid,uuid,uuid,bigint,uuid);
DROP FUNCTION privacy_worker_fail_job(uuid,uuid,uuid,bigint,uuid,text,bigint,text,text,bytea);
DROP FUNCTION privacy_worker_complete_job(uuid,uuid,uuid,bigint,uuid);
DROP FUNCTION privacy_worker_complete_checkpoint(uuid,uuid,uuid,bigint,uuid,text,text);
DROP FUNCTION privacy_worker_heartbeat(uuid,uuid,uuid,bigint,uuid,bigint);
DROP FUNCTION privacy_worker_claim(bigint,uuid);
DROP TRIGGER privacy_executor_grants_active_subject_attachment ON privacy_executor_grants;
DROP TRIGGER privacy_reviewer_grants_active_subject_attachment ON privacy_reviewer_grants;
DROP TRIGGER staff_grants_active_subject_attachment ON staff_grants;
DROP TRIGGER user_platform_roles_active_subject_attachment ON user_platform_roles;
DROP TRIGGER password_reset_tokens_active_subject_attachment ON password_reset_tokens;
DROP TRIGGER email_verification_tokens_active_subject_attachment ON email_verification_tokens;
DROP TRIGGER sessions_active_subject_attachment ON sessions;
DROP TRIGGER users_active_guardian_attachment ON users;
DROP FUNCTION require_active_privacy_attachment_subject();
DROP TRIGGER user_platform_roles_guard_active_admin ON user_platform_roles;
DROP TRIGGER users_guard_active_admin ON users;
DROP FUNCTION guard_active_administrator_set();
DROP TABLE privacy_erasure_failures,privacy_erasure_job_checkpoints,privacy_erasure_job_attempts,privacy_erasure_job_leases,privacy_erasure_category_jobs,privacy_erasure_access_revocations,privacy_erasure_executions CASCADE;
DROP TABLE privacy_executor_grant_events,privacy_executor_grants CASCADE;
DROP FUNCTION prevent_privacy_execution_record_delete();
ALTER TABLE privacy_request_execution_plans DROP CONSTRAINT privacy_execution_plans_binding_unique;
DROP INDEX sessions_user_expiry_idx;
DROP INDEX sessions_unindexed_expiry_idx;
ALTER TABLE sessions DROP CONSTRAINT sessions_user_id_fkey;
DO $$ DECLARE row record; BEGIN
 FOR row IN SELECT conname FROM pg_constraint WHERE conrelid='sessions'::regclass AND contype='c' AND pg_get_constraintdef(oid) LIKE '%subject_indexed%' LOOP
  EXECUTE format('ALTER TABLE sessions DROP CONSTRAINT %I',row.conname);
 END LOOP;
END $$;
ALTER TABLE sessions DROP COLUMN user_id;
ALTER TABLE sessions DROP COLUMN subject_indexed;
ALTER TABLE data_erasure_requests DROP CONSTRAINT privacy_case_execution_decision_complete;
ALTER TABLE data_erasure_requests DROP CONSTRAINT privacy_case_closure_state;
ALTER TABLE data_erasure_requests DROP CONSTRAINT data_erasure_requests_status_check;
ALTER TABLE data_erasure_requests ADD CONSTRAINT data_erasure_requests_status_check CHECK(status IN ('RECEIVED','UNDER_REVIEW','AWAITING_EXECUTION','PARTIALLY_APPROVED','COMPLETED','REFUSED','CANCELLED'));
ALTER TABLE data_erasure_requests ADD CONSTRAINT privacy_case_closure_state CHECK((status IN ('REFUSED','CANCELLED','COMPLETED') AND closed_at IS NOT NULL AND evidence_expires_at>closed_at) OR (status NOT IN ('REFUSED','CANCELLED','COMPLETED') AND closed_at IS NULL AND evidence_expires_at IS NULL));
DROP INDEX data_erasure_request_active_uidx;
CREATE UNIQUE INDEX data_erasure_request_active_uidx ON data_erasure_requests(subject_user_id,requester_user_id) WHERE status NOT IN ('REFUSED','CANCELLED','COMPLETED');
ALTER TABLE data_erasure_request_events DROP CONSTRAINT data_erasure_request_events_actor_role_check;
ALTER TABLE data_erasure_request_events DROP CONSTRAINT data_erasure_request_events_action_check;
ALTER TABLE data_erasure_request_events DROP CONSTRAINT data_erasure_request_events_reason_code_check;
ALTER TABLE data_erasure_request_events DROP CONSTRAINT data_erasure_request_events_from_status_check;
ALTER TABLE data_erasure_request_events DROP CONSTRAINT data_erasure_request_events_to_status_check;
ALTER TABLE data_erasure_request_events ADD CONSTRAINT data_erasure_request_events_actor_role_check CHECK(actor_role IN ('REQUESTER','REVIEWER','SYSTEM'));
ALTER TABLE data_erasure_request_events ADD CONSTRAINT data_erasure_request_events_action_check CHECK(action IN ('RECEIVED','CLAIMED','IDENTITY_REQUESTED','IDENTITY_VERIFIED','REPRESENTATION_VERIFIED','REPRESENTATION_CONFLICT','DEADLINE_EXTENDED','DEPENDANT_RESOLVED','APPROVED','PARTIALLY_APPROVED','COMPLETED','REFUSED','CANCELLED'));
ALTER TABLE data_erasure_request_events ADD CONSTRAINT data_erasure_request_events_reason_code_check CHECK(reason_code IN ('REQUEST_RECEIVED','REVIEW_CLAIMED','VERIFICATION_REQUIRED','VERIFICATION_RECORDED','DEPENDANT_RESOLVED','REPRESENTATION_CONFLICT','COMPLEXITY','REQUEST_VOLUME','POLICY_DECISION','EXECUTION_COMPLETED','REQUESTER_CANCELLED'));
ALTER TABLE data_erasure_request_events ADD CONSTRAINT data_erasure_request_events_from_status_check CHECK(from_status IN ('RECEIVED','UNDER_REVIEW','AWAITING_EXECUTION','PARTIALLY_APPROVED','COMPLETED','REFUSED','CANCELLED'));
ALTER TABLE data_erasure_request_events ADD CONSTRAINT data_erasure_request_events_to_status_check CHECK(to_status IN ('RECEIVED','UNDER_REVIEW','AWAITING_EXECUTION','PARTIALLY_APPROVED','COMPLETED','REFUSED','CANCELLED'));
ALTER TABLE email_outbox DROP CONSTRAINT email_outbox_message_valid;
ALTER TABLE email_outbox ADD CONSTRAINT email_outbox_message_valid CHECK(
 (message_type='EMAIL_VERIFICATION' AND verification_token_id IS NOT NULL AND password_reset_token_id IS NULL AND sealed_payload IS NULL AND privacy_request_id IS NULL AND privacy_requester_id IS NULL AND privacy_event_key IS NULL)
 OR (message_type='PASSWORD_RESET' AND verification_token_id IS NULL AND password_reset_token_id IS NOT NULL AND sealed_payload IS NOT NULL AND privacy_request_id IS NULL AND privacy_requester_id IS NULL AND privacy_event_key IS NULL)
 OR (message_type IN ('PRIVACY_ACKNOWLEDGEMENT','PRIVACY_DECISION') AND verification_token_id IS NULL AND password_reset_token_id IS NULL AND sealed_payload IS NOT NULL AND privacy_request_id IS NOT NULL AND privacy_event_key IS NOT NULL)
);`
	if _, err = conn.Exec(ctx, rollback244); err != nil {
		t.Fatalf("build prior lifecycle schema: %v", err)
	}

	userID, reviewerID, requestID, eventID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err = conn.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,is_dependent,date_of_birth,is_active,created_at,updated_at)
		VALUES($1,'Legacy person','legacy-lifecycle@example.test','hash',false,'1990-01-01',true,now(),now()),
		      ($2,'Legacy reviewer','legacy-reviewer@example.test','hash',false,'1990-01-01',true,now(),now())`, userID, reviewerID); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO sessions(token,data,expiry) VALUES('legacy-session','legacy',now()+interval '1 hour')`); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO privacy_request_policies(version,category_catalogue,executor_version,plan_schema_version,account_closure_enabled,working_retention_days,response_months,extension_months,adopted_at,adopted_by,created_at)
		VALUES('legacy-lifecycle','[]','privacy-erasure-executor/v1','privacy-erasure-plan/v1',false,90,1,2,now(),$1,now())`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO data_erasure_requests(id,public_ref,idempotency_key,subject_user_id,requester_user_id,subject_kind,scope_kind,categories,status,version,received_at,due_at,identity_verified_at,identity_method,identity_verified_by,decision_code,decision_explanation,category_decisions,decided_by,decided_at,policy_version,policy_snapshot,updated_at)
		VALUES($1,$2,$3,$4,$4,'SELF','CATEGORIES','{identity-core}','AWAITING_EXECUTION',4,now()-interval '1 day',now()+interval '1 month',now()-interval '1 hour','IN_PERSON',$5,'APPROVED','legacy decision','[]',$5,now()-interval '30 minutes','legacy-lifecycle','{}',now())`, requestID, uuid.New(), uuid.New(), userID, reviewerID); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO data_erasure_request_events(id,request_id,actor_role,actor_ref,action,reason_code,from_status,to_status,version,occurred_at)
		VALUES($1,$2,'REVIEWER',$3,'APPROVED','POLICY_DECISION','UNDER_REVIEW','AWAITING_EXECUTION',4,now())`, eventID, requestID, reviewerID); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"migrations/202609080002_privacy_execution_lifecycle.sql", "migrations/202609080003_privacy_worker_api.sql"} {
		migration, readErr := migrationFiles.ReadFile(name)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = conn.Exec(ctx, string(migration)); err != nil {
			t.Fatalf("apply #244 forward migration %s: %v", name, err)
		}
	}
	var sessionIndexed bool
	var requestStatus string
	var executions, events int
	if err = conn.QueryRow(ctx, `SELECT subject_indexed FROM sessions WHERE token='legacy-session'`).Scan(&sessionIndexed); err != nil {
		t.Fatal(err)
	}
	if err = conn.QueryRow(ctx, `SELECT status FROM data_erasure_requests WHERE id=$1`, requestID).Scan(&requestStatus); err != nil {
		t.Fatal(err)
	}
	if err = conn.QueryRow(ctx, `SELECT (SELECT count(*) FROM privacy_erasure_executions),(SELECT count(*) FROM data_erasure_request_events WHERE id=$1)`, eventID).Scan(&executions, &events); err != nil {
		t.Fatal(err)
	}
	if sessionIndexed || requestStatus != "AWAITING_EXECUTION" || executions != 0 || events != 1 {
		t.Fatalf("migration changed legacy facts: indexed=%v status=%q executions=%d events=%d", sessionIndexed, requestStatus, executions, events)
	}
	var workerAPI bool
	if err = conn.QueryRow(ctx, `SELECT to_regprocedure('privacy_worker_claim(bigint,uuid)') IS NOT NULL`).Scan(&workerAPI); err != nil || !workerAPI {
		t.Fatalf("worker API missing=%v err=%v", workerAPI, err)
	}
}
