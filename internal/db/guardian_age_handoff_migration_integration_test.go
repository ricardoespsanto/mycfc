//go:build integration

package db

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// rewindGuardianSchemaReadyOwnerMigration reconstructs the exact 005 privacy
// inventory boundary from the current baseline before older migration tests
// rewind their own predecessor.
func rewindGuardianSchemaReadyOwnerMigration(t *testing.T, ctx context.Context, tx pgx.Tx) {
	t.Helper()
	if _, err := tx.Exec(ctx, `DO $$DECLARE definition text;rewritten text;BEGIN
		IF EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='privacy_activation_authenticated_artifacts'::regclass
			AND conname='privacy_activation_authenticated_artifacts_v14_check') THEN
			ALTER TABLE privacy_activation_authenticated_artifacts DISABLE TRIGGER privacy_activation_authenticated_artifacts_immutable;
			DELETE FROM privacy_activation_authenticated_artifacts;
			ALTER TABLE privacy_activation_authenticated_artifacts ENABLE TRIGGER privacy_activation_authenticated_artifacts_immutable;
			SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint
			WHERE conrelid='privacy_activation_authenticated_artifacts'::regclass AND conname='privacy_activation_authenticated_artifacts_v14_check';
			IF definition IS NULL OR length(definition)-length(replace(definition,'202609120006_guardian_schema_ready_owner',''))
				<>length('202609120006_guardian_schema_ready_owner') THEN RAISE EXCEPTION 'guardian schema-ready rewind mismatch'; END IF;
			rewritten:=replace(definition,', ''202609120006_guardian_schema_ready_owner''','');
			ALTER TABLE privacy_activation_authenticated_artifacts DROP CONSTRAINT privacy_activation_authenticated_artifacts_v14_check;
			EXECUTE 'ALTER TABLE privacy_activation_authenticated_artifacts ADD CONSTRAINT privacy_activation_authenticated_artifacts_v13_check '||rewritten;
			SELECT pg_get_functiondef('privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)'::regprocedure) INTO definition;
			EXECUTE replace(definition,'202609120006_guardian_schema_ready_owner','202609120005_guardian_authority_activation');
			SELECT pg_get_functiondef('privacy_activation_authenticated_set_digest(text,uuid[])'::regprocedure) INTO definition;
			EXECUTE replace(definition,'202609120006_guardian_schema_ready_owner','202609120005_guardian_authority_activation');
		END IF;
	END$$`); err != nil {
		t.Fatal(err)
	}
}

// rewindGuardianAgeHandoffMigration reconstructs the exact 003 boundary from
// the current baseline. Older forward-migration tests call it before rewinding
// their own predecessor so adding 004 does not silently weaken those proofs.
func rewindGuardianAgeHandoffMigration(t *testing.T, ctx context.Context, tx pgx.Tx) {
	t.Helper()
	rewindGuardianSchemaReadyOwnerMigration(t, ctx, tx)
	// The current baseline is one release boundary newer than the handoff
	// migration. Reconstruct its privacy-evidence predecessor before applying
	// the existing 004 -> 003 rewind below. The 005 operational objects may
	// remain in this transaction: the forward-migration proofs exercise the
	// exact privacy boundary and fail-closed gates, not a destructive downgrade.
	if _, err := tx.Exec(ctx, `DO $$DECLARE definition text;rewritten text;BEGIN
		IF EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='privacy_activation_authenticated_artifacts'::regclass
			AND conname='privacy_activation_authenticated_artifacts_v13_check') THEN
			ALTER TABLE privacy_activation_authenticated_artifacts DISABLE TRIGGER privacy_activation_authenticated_artifacts_immutable;
			DELETE FROM privacy_activation_authenticated_artifacts;
			ALTER TABLE privacy_activation_authenticated_artifacts ENABLE TRIGGER privacy_activation_authenticated_artifacts_immutable;
			SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint
			WHERE conrelid='privacy_activation_authenticated_artifacts'::regclass AND conname='privacy_activation_authenticated_artifacts_v13_check';
			IF definition IS NULL OR length(definition)-length(replace(definition,'202609120005_guardian_authority_activation',''))
				<>length('202609120005_guardian_authority_activation') THEN RAISE EXCEPTION 'guardian activation rewind mismatch'; END IF;
			rewritten:=replace(definition,', ''202609120005_guardian_authority_activation''','');
			ALTER TABLE privacy_activation_authenticated_artifacts DROP CONSTRAINT privacy_activation_authenticated_artifacts_v13_check;
			EXECUTE 'ALTER TABLE privacy_activation_authenticated_artifacts ADD CONSTRAINT privacy_activation_authenticated_artifacts_v12_check '||rewritten;
			SELECT pg_get_functiondef('privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)'::regprocedure) INTO definition;
			EXECUTE replace(definition,'202609120005_guardian_authority_activation','202609120004_guardian_age_handoff');
			SELECT pg_get_functiondef('privacy_activation_authenticated_set_digest(text,uuid[])'::regprocedure) INTO definition;
			EXECUTE replace(definition,'202609120005_guardian_authority_activation','202609120004_guardian_age_handoff');
		END IF;
	END$$`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE guardian_application_intake_release
		SET enabled=false,policy_version=NULL,policy_sha256=NULL,approval_sha256=NULL,image_digest=NULL,
			schema_migration_digest=NULL,enabled_by=NULL,enabled_at=NULL WHERE singleton;
		UPDATE guardian_authority_policies SET enabled=false,enabled_at=NULL,enabled_by=NULL WHERE enabled`); err != nil {
		t.Fatal(err)
	}
	predecessor, err := migrationFiles.ReadFile("migrations/202609120003_guardian_authority_renewal.sql")
	if err != nil {
		t.Fatal(err)
	}
	reconcile := migrationFunctionSegment(t, string(predecessor),
		"CREATE OR REPLACE FUNCTION guardian_authority_reconcile_cutoffs()", "CREATE OR REPLACE FUNCTION guardian_authority_admin_transition(")
	if _, err = tx.Exec(ctx, reconcile); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `
		DROP FUNCTION guardian_age_handoff_outbox_delivery(uuid,timestamptz);
		DROP FUNCTION guardian_age_handoff_enqueue_guardian_notice(uuid,uuid,uuid,citext,timestamptz,date,text,bytea);
		DROP FUNCTION guardian_age_handoff_due_guardian_notices(integer);
		DROP FUNCTION guardian_age_handoff_get_for_admin(uuid,uuid);
		DROP FUNCTION guardian_age_handoff_list_for_admin(uuid,integer,integer);
		DROP FUNCTION guardian_age_handoff_for_guardian(uuid,uuid);
		DROP FUNCTION guardian_age_handoff_notices_for_subject(uuid);
		DROP FUNCTION guardian_age_handoff_available_for_subject(uuid);
		DROP FUNCTION guardian_age_handoff_for_subject(uuid);
		DROP FUNCTION guardian_age_handoff_admin_recovery_email(uuid,uuid,bigint,citext,bytea,timestamptz,bytea);
		DROP FUNCTION guardian_age_handoff_admin_confirm(uuid,uuid,bigint);
		DROP FUNCTION guardian_age_handoff_verify_email(bytea);
		DROP FUNCTION guardian_age_handoff_propose_email(uuid,bigint,citext,bytea,timestamptz,bytea);
		DROP FUNCTION guardian_age_handoff_reconcile();
		ALTER TABLE email_outbox DROP CONSTRAINT email_outbox_message_valid;
		DELETE FROM email_outbox WHERE guardian_handoff_notice_id IS NOT NULL OR guardian_handoff_email_token_id IS NOT NULL;
		ALTER TABLE email_outbox DROP COLUMN guardian_handoff_notice_id;
		ALTER TABLE email_outbox DROP COLUMN guardian_handoff_email_token_id;
		DROP TABLE guardian_age_handoff_access_events;
		DROP TABLE guardian_age_handoff_notices;
		DROP TABLE guardian_age_handoff_email_tokens;
		DROP TABLE guardian_age_handoff_events;
		DROP TABLE guardian_age_handoffs;`); err != nil {
		t.Fatal(err)
	}
	emailConstraint := migrationFunctionSegment(t, string(predecessor),
		"ALTER TABLE email_outbox ADD CONSTRAINT email_outbox_message_valid", "CREATE TRIGGER guardian_authority_renewal_requests_immutable")
	if _, err = tx.Exec(ctx, emailConstraint); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `DO $$DECLARE definition text;rewritten text;BEGIN
		ALTER TABLE privacy_activation_authenticated_artifacts DISABLE TRIGGER privacy_activation_authenticated_artifacts_immutable;
		DELETE FROM privacy_activation_authenticated_artifacts;
		ALTER TABLE privacy_activation_authenticated_artifacts ENABLE TRIGGER privacy_activation_authenticated_artifacts_immutable;
		SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint WHERE conrelid='privacy_activation_authenticated_artifacts'::regclass AND conname='privacy_activation_authenticated_artifacts_v12_check';
		IF definition IS NULL OR length(definition)-length(replace(definition,'202609120004_guardian_age_handoff',''))<>length('202609120004_guardian_age_handoff') THEN RAISE EXCEPTION 'age handoff rewind mismatch'; END IF;
		rewritten:=replace(definition,', ''202609120004_guardian_age_handoff''','');
		ALTER TABLE privacy_activation_authenticated_artifacts DROP CONSTRAINT privacy_activation_authenticated_artifacts_v12_check;
		EXECUTE 'ALTER TABLE privacy_activation_authenticated_artifacts ADD CONSTRAINT privacy_activation_authenticated_artifacts_v11_check '||rewritten;
		SELECT pg_get_functiondef('privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)'::regprocedure) INTO definition;
		EXECUTE replace(definition,'202609120004_guardian_age_handoff','202609120003_guardian_authority_renewal');
		SELECT pg_get_functiondef('privacy_activation_authenticated_set_digest(text,uuid[])'::regprocedure) INTO definition;
		EXECUTE replace(definition,'202609120004_guardian_age_handoff','202609120003_guardian_authority_renewal');
	END$$`); err != nil {
		t.Fatal(err)
	}
}

func TestGuardianAgeHandoffForwardMigrationRequiresExactRenewalPredecessorAndStaysLocked(t *testing.T) {
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
	rewindGuardianAgeHandoffMigration(t, ctx, tx)
	migration, err := migrationFiles.ReadFile("migrations/202609120004_guardian_age_handoff.sql")
	if err != nil {
		t.Fatal(err)
	}

	if _, err = tx.Exec(ctx, `SAVEPOINT wrong_predecessor; DO $$DECLARE definition text;BEGIN
		SELECT pg_get_functiondef('privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)'::regprocedure) INTO definition;
		EXECUTE replace(definition,'202609120003_guardian_authority_renewal','wrong-boundary'); END$$`); err != nil {
		t.Fatal(err)
	}
	_, wrongErr := tx.Exec(ctx, string(migration))
	var databaseError *pgconn.PgError
	if !errorsAsPg(wrongErr, &databaseError) || databaseError.Message != "guardian_age_handoff_activation_evidence_predecessor_mismatch" {
		t.Fatalf("wrong predecessor error=%v", wrongErr)
	}
	if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT wrong_predecessor`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	var constraint string
	if err = tx.QueryRow(ctx, `SELECT conname FROM pg_constraint WHERE conrelid='privacy_activation_authenticated_artifacts'::regclass AND conname='privacy_activation_authenticated_artifacts_v12_check'`).Scan(&constraint); err != nil || constraint == "" {
		t.Fatalf("constraint=%q err=%v", constraint, err)
	}
	var intake, policy, privacyEnabled bool
	if err = tx.QueryRow(ctx, `SELECT enabled FROM guardian_application_intake_release WHERE singleton`).Scan(&intake); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM guardian_authority_policies WHERE enabled), COALESCE((SELECT enabled FROM privacy_request_activation WHERE singleton),false)`).Scan(&policy, &privacyEnabled); err != nil {
		t.Fatal(err)
	}
	if intake || policy || privacyEnabled {
		t.Fatalf("release gates intake=%v policy=%v privacy=%v", intake, policy, privacyEnabled)
	}
	if !strings.Contains(string(migration), "guardian_age_handoff_reconcile") {
		t.Fatal("migration missing handoff reconciliation")
	}
}

func errorsAsPg(err error, target **pgconn.PgError) bool {
	if err == nil {
		return false
	}
	value, ok := err.(*pgconn.PgError)
	if ok {
		*target = value
	}
	return ok
}
