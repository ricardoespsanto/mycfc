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

func TestGuardianActivationForwardMigrationFromExact004DisablesEveryGate(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	reconstructExactGuardian004(t, ctx, tx)

	actor, guardian, subject := uuid.New(), uuid.New(), uuid.New()
	if _, err = tx.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES
		($1,'Migration administrator',$2,'hash','1990-01-01'),($3,'Migration guardian',$4,'hash','1990-01-01')`, actor, "migration-admin-"+uuid.NewString()+"@example.test", guardian, "migration-guardian-"+uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO users(id,name,is_dependent,date_of_birth,password_hash,minor_login_id) VALUES($1,'Migration dependent',true,'2012-01-01','minor-hash',$2)`, subject, "migration-minor-"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, actor); err != nil {
		t.Fatal(err)
	}
	version := "guardian-004-representative-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	if _, err = tx.Exec(ctx, `INSERT INTO guardian_authority_policies(version,evidence_types,reason_codes,validity_days,review_days,adopted_at,adopted_by,enabled,enabled_at,enabled_by)
		VALUES($1,ARRAY['CLUB_REGISTRATION_RECORD','IN_PERSON_ID_AND_CIVIL_RECORD','COURT_OR_LEGAL_AUTHORITY'],
		ARRAY['RELATIONSHIP_CONFIRMED','EVIDENCE_INSUFFICIENT','AUTHORITY_NOT_ESTABLISHED','CONFLICT','AUTHORITY_CHANGED','UNCERTAINTY','AUTHORITY_ENDED','ELIGIBILITY_ENDED','REVIEW_EXPIRED','MAJORITY_REACHED'],
		365,365,clock_timestamp(),$2,true,clock_timestamp(),$2)`, version, actor); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO guardian_authority_relationships(guardian_user_id,subject_user_id,submitted_label,state,policy_version,verified_at,verified_by,verified_until,review_due_at)
		VALUES($1,$2,'Migration dependent','VERIFIED',$3,clock_timestamp(),$4,clock_timestamp()+interval '365 days',clock_timestamp()+interval '300 days')`, guardian, subject, version, actor); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO sessions(token,data,expiry,user_id,subject_indexed) VALUES($1,'migration',clock_timestamp()+interval '1 hour',$2,true)`, "migration-session-"+uuid.NewString(), subject); err != nil {
		t.Fatal(err)
	}
	// Build a representative pre-005 active privacy row. The current privacy
	// evidence trigger is temporarily bypassed only to avoid coupling this
	// guardian migration proof to the independent signed-evidence fixture.
	privacyVersion := "privacy-004-representative-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	if _, err = tx.Exec(ctx, `INSERT INTO privacy_request_policies(version,category_catalogue,adopted_at,adopted_by) VALUES($1,'[]'::jsonb,clock_timestamp(),$2)`, privacyVersion, actor); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO privacy_request_activation(singleton,policy_version,enabled,fulfilment_ready,updated_by) VALUES(true,$1,false,false,$2)`, privacyVersion, actor); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `ALTER TABLE privacy_request_activation DISABLE TRIGGER privacy_activation_evidence_guard;
		ALTER TABLE privacy_request_activation DROP CONSTRAINT privacy_activation_requires_evidence_approval;
		UPDATE privacy_request_activation SET enabled=true,fulfilment_ready=true;
		ALTER TABLE privacy_request_activation ADD CONSTRAINT privacy_activation_requires_evidence_approval CHECK(NOT enabled OR (fulfilment_ready AND approval_id IS NOT NULL)) NOT VALID;
		ALTER TABLE privacy_request_activation ENABLE TRIGGER privacy_activation_evidence_guard;`); err != nil {
		t.Fatal(err)
	}

	migration, err := migrationFiles.ReadFile("migrations/202609120005_guardian_authority_activation.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, string(migration)); err != nil {
		t.Fatalf("apply exact 004 -> 005 migration: %v", err)
	}
	var intake, policyEnabled, privacyEnabled, fulfilmentReady, killSwitch, runtimeBound bool
	var relationshipState string
	var credential, session bool
	if err = tx.QueryRow(ctx, `SELECT
		(SELECT enabled FROM guardian_application_intake_release WHERE singleton),
		EXISTS(SELECT 1 FROM guardian_authority_policies WHERE enabled),
		(SELECT enabled FROM privacy_request_activation WHERE singleton),
		(SELECT fulfilment_ready FROM privacy_request_activation WHERE singleton),
		(SELECT engaged FROM privacy_worker_kill_switch WHERE singleton),
		EXISTS(SELECT 1 FROM guardian_ops.runtime_release_binding WHERE singleton AND image_digest IS NOT NULL),
		relationship.state,(subject.minor_login_id IS NOT NULL OR subject.password_hash IS NOT NULL),
		EXISTS(SELECT 1 FROM sessions WHERE user_id=$1)
		FROM guardian_authority_relationships relationship JOIN users subject ON subject.id=relationship.subject_user_id WHERE relationship.subject_user_id=$1`, subject).
		Scan(&intake, &policyEnabled, &privacyEnabled, &fulfilmentReady, &killSwitch, &runtimeBound, &relationshipState, &credential, &session); err != nil {
		t.Fatal(err)
	}
	if intake || policyEnabled || privacyEnabled || fulfilmentReady || !killSwitch || runtimeBound || relationshipState != "EXPIRED" || credential || session {
		t.Fatalf("005 postcondition intake=%t policy=%t privacy=%t ready=%t kill=%t runtime=%t relationship=%s credential=%t session=%t",
			intake, policyEnabled, privacyEnabled, fulfilmentReady, killSwitch, runtimeBound, relationshipState, credential, session)
	}
	var currentAuthority bool
	if err = tx.QueryRow(ctx, `SELECT guardian_authority_current($1,$2)`, guardian, subject).Scan(&currentAuthority); err != nil || currentAuthority {
		t.Fatalf("post-migration current=%t err=%v", currentAuthority, err)
	}
}

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
