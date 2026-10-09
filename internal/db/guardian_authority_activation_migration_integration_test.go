//go:build integration

package db

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func reconstructExactGuardian004(t *testing.T, ctx context.Context, tx pgx.Tx) {
	t.Helper()
	rewindGuardianSchemaReadyOwnerMigration(t, ctx, tx)
	verification, err := migrationFiles.ReadFile("migrations/202609110004_guardian_authority_verification.sql")
	if err != nil {
		t.Fatal(err)
	}
	eligibility, err := migrationFiles.ReadFile("migrations/202609120001_guardian_application_eligibility.sql")
	if err != nil {
		t.Fatal(err)
	}
	handoff, err := migrationFiles.ReadFile("migrations/202609120004_guardian_age_handoff.sql")
	if err != nil {
		t.Fatal(err)
	}
	current := migrationFunctionSegment(t, string(verification), "CREATE FUNCTION guardian_authority_current(", "CREATE FUNCTION guardian_authority_create_dependent(")
	current = strings.Replace(current, "CREATE FUNCTION guardian_authority_current(", "CREATE OR REPLACE FUNCTION guardian_authority_current(", 1)
	createDependent := migrationFunctionSegment(t, string(eligibility), "CREATE FUNCTION guardian_authority_create_dependent(p_name text,p_date_of_birth date,p_guardian_user_id uuid,p_invitation_digest bytea)", "REVOKE ALL ON TABLE guardian_application_intake_release")
	createDependent = strings.Replace(createDependent, "CREATE FUNCTION guardian_authority_create_dependent(", "CREATE OR REPLACE FUNCTION guardian_authority_create_dependent(", 1)
	reconcile := migrationFunctionSegment(t, string(handoff), "CREATE OR REPLACE FUNCTION guardian_authority_reconcile_cutoffs()", "REVOKE ALL ON TABLE guardian_age_handoffs")
	if _, err = tx.Exec(ctx, current+"\n"+createDependent+"\n"+reconcile); err != nil {
		t.Fatalf("restore predecessor functions: %v", err)
	}
	if _, err = tx.Exec(ctx, `
		DROP FUNCTION guardian_authority_admin_transition(uuid,uuid,bigint,text,text,text);
		ALTER FUNCTION guardian_authority_admin_transition_inner_004(uuid,uuid,bigint,text,text,text) RENAME TO guardian_authority_admin_transition;
		UPDATE guardian_application_intake_release SET enabled=false,policy_version=NULL,policy_sha256=NULL,approval_sha256=NULL,image_digest=NULL,schema_migration_digest=NULL,enabled_by=NULL,enabled_at=NULL WHERE singleton;
		DROP SCHEMA guardian_ops CASCADE;
		DROP FUNCTION guardian_authority_policy_operational(text);
		DROP TABLE guardian_authority_policy_approvals;
		ALTER TABLE guardian_application_intake_release_events DISABLE TRIGGER guardian_application_intake_release_events_immutable;
		DELETE FROM guardian_application_intake_release_events WHERE action<>'CREATED_DISABLED';
		ALTER TABLE guardian_application_intake_release_events ENABLE TRIGGER guardian_application_intake_release_events_immutable;
		ALTER TABLE guardian_application_intake_release DROP CONSTRAINT guardian_application_intake_release_binding_check,
		 DROP COLUMN policy_version,DROP COLUMN policy_sha256,DROP COLUMN approval_sha256,DROP COLUMN image_digest,
		 DROP COLUMN schema_migration_digest,DROP COLUMN enabled_by,DROP COLUMN enabled_at;
		ALTER TABLE guardian_application_intake_release ADD CONSTRAINT guardian_application_intake_release_disabled_check CHECK(NOT enabled);
		ALTER TABLE guardian_application_intake_release_events DROP CONSTRAINT guardian_application_intake_release_events_action_v2_check,
		 DROP CONSTRAINT guardian_application_intake_release_events_actor_v2_check,
		 DROP COLUMN policy_version,DROP COLUMN policy_sha256,DROP COLUMN approval_sha256,DROP COLUMN image_digest,DROP COLUMN schema_migration_digest,
		 DROP COLUMN relationship_count,DROP COLUMN credential_count,DROP COLUMN session_count;
		ALTER TABLE guardian_application_intake_release_events ADD CONSTRAINT guardian_application_intake_release_events_action_check CHECK(action='CREATED_DISABLED'),
		 ADD CONSTRAINT guardian_application_intake_release_events_actor_ref_check CHECK(actor_ref IS NULL);`); err != nil {
		t.Fatalf("reconstruct 004 guardian boundary: %v", err)
	}
	if _, err = tx.Exec(ctx, `DO $$DECLARE definition text;rewritten text;BEGIN
		ALTER TABLE privacy_activation_authenticated_artifacts DISABLE TRIGGER privacy_activation_authenticated_artifacts_immutable;
		DELETE FROM privacy_activation_authenticated_artifacts;
		ALTER TABLE privacy_activation_authenticated_artifacts ENABLE TRIGGER privacy_activation_authenticated_artifacts_immutable;
		SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint WHERE conrelid='privacy_activation_authenticated_artifacts'::regclass AND conname='privacy_activation_authenticated_artifacts_v13_check';
		IF definition IS NULL OR length(definition)-length(replace(definition,'202609120005_guardian_authority_activation',''))<>length('202609120005_guardian_authority_activation') THEN RAISE EXCEPTION 'guardian activation rewind mismatch'; END IF;
		rewritten:=replace(definition,', ''202609120005_guardian_authority_activation''','');
		ALTER TABLE privacy_activation_authenticated_artifacts DROP CONSTRAINT privacy_activation_authenticated_artifacts_v13_check;
		EXECUTE 'ALTER TABLE privacy_activation_authenticated_artifacts ADD CONSTRAINT privacy_activation_authenticated_artifacts_v12_check '||rewritten;
		SELECT pg_get_functiondef('privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)'::regprocedure) INTO definition;
		EXECUTE replace(definition,'202609120005_guardian_authority_activation','202609120004_guardian_age_handoff');
		SELECT pg_get_functiondef('privacy_activation_authenticated_set_digest(text,uuid[])'::regprocedure) INTO definition;
		EXECUTE replace(definition,'202609120005_guardian_authority_activation','202609120004_guardian_age_handoff');
	END$$`); err != nil {
		t.Fatalf("reconstruct 004 privacy boundary: %v", err)
	}
}
