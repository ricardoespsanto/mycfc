//go:build integration

package db

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestGuardianAuthorityRenewalForwardMigrationRequiresExactAdminReviewPredecessor(t *testing.T) {
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

	migration, err := migrationFiles.ReadFile("migrations/202609120003_guardian_authority_renewal.sql")
	if err != nil {
		t.Fatal(err)
	}
	predecessor, err := migrationFiles.ReadFile("migrations/202609120002_guardian_authority_admin_review.sql")
	if err != nil {
		t.Fatal(err)
	}
	guardianFoundation, err := migrationFiles.ReadFile("migrations/202609110004_guardian_authority_verification.sql")
	if err != nil {
		t.Fatal(err)
	}

	if _, err = tx.Exec(ctx, `
		DROP VIEW guardian_authority_guardian_disclosures;
		DROP VIEW guardian_authority_latest_renewals;
		DROP FUNCTION guardian_authority_due_renewal_reminders(integer);
		DROP FUNCTION guardian_authority_enqueue_renewal_reminder(uuid,uuid,citext,timestamptz,timestamptz,text,bytea);
		DROP FUNCTION guardian_authority_submit_renewal(uuid,uuid,bigint,text);
		ALTER TABLE guardian_authority_events DROP CONSTRAINT guardian_authority_events_action_v4_check;
		ALTER TABLE guardian_authority_events DISABLE TRIGGER guardian_authority_events_immutable;
		DELETE FROM guardian_authority_events WHERE action='RENEWAL_SUBMITTED';
		ALTER TABLE guardian_authority_events ENABLE TRIGGER guardian_authority_events_immutable;
		ALTER TABLE guardian_authority_events ADD CONSTRAINT guardian_authority_events_action_check CHECK(action IN('DECLARED','VERIFIED','SUSPENDED','EXPIRED','REJECTED'));
		ALTER TABLE email_outbox DROP CONSTRAINT email_outbox_message_valid;
		DELETE FROM email_outbox WHERE guardian_reminder_id IS NOT NULL;
		ALTER TABLE email_outbox DROP COLUMN guardian_reminder_id;
		DROP TABLE guardian_authority_renewal_reminders;
		DROP TABLE guardian_authority_renewal_events;
		DROP TABLE guardian_authority_renewal_requests;
		ALTER TABLE email_outbox ADD CONSTRAINT email_outbox_message_valid CHECK (
		 (message_type='EMAIL_VERIFICATION' AND verification_token_id IS NOT NULL AND password_reset_token_id IS NULL AND sealed_payload IS NULL AND privacy_request_id IS NULL AND privacy_requester_id IS NULL AND privacy_event_key IS NULL)
		 OR (message_type='PASSWORD_RESET' AND verification_token_id IS NULL AND password_reset_token_id IS NOT NULL AND sealed_payload IS NOT NULL AND privacy_request_id IS NULL AND privacy_requester_id IS NULL AND privacy_event_key IS NULL)
		 OR (message_type IN('PRIVACY_ACKNOWLEDGEMENT','PRIVACY_DECISION','PRIVACY_PROCESSING_STARTED','PRIVACY_COMPLETED') AND verification_token_id IS NULL AND password_reset_token_id IS NULL AND sealed_payload IS NOT NULL AND privacy_request_id IS NOT NULL AND privacy_event_key IS NOT NULL));
		ALTER TABLE privacy_activation_authenticated_artifacts DROP CONSTRAINT privacy_activation_authenticated_artifacts_v11_check`); err != nil {
		t.Fatal(err)
	}
	v10Constraint := migrationFunctionSegment(t, string(predecessor),
		"ALTER TABLE privacy_activation_authenticated_artifacts ADD CONSTRAINT privacy_activation_authenticated_artifacts_v10_check",
		"DO $$DECLARE definition text;old_clause text;new_clause text;")
	if _, err = tx.Exec(ctx, v10Constraint); err != nil {
		t.Fatal(err)
	}
	disclosure := migrationFunctionSegment(t, string(guardianFoundation),
		"CREATE VIEW guardian_authority_guardian_disclosures AS",
		"-- Capture every legacy declaration before removing the authorization pointer.")
	reconcile := migrationFunctionSegment(t, string(predecessor),
		"CREATE OR REPLACE FUNCTION guardian_authority_reconcile_cutoffs()",
		"CREATE FUNCTION guardian_authority_admin_transition(")
	admin := migrationFunctionSegment(t, string(predecessor),
		"CREATE FUNCTION guardian_authority_admin_transition(",
		"REVOKE ALL ON TABLE guardian_authority_review_access_events")
	admin = strings.Replace(admin, "CREATE FUNCTION", "CREATE OR REPLACE FUNCTION", 1)
	if _, err = tx.Exec(ctx, disclosure+"\n"+reconcile+"\n"+admin); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `DO $$DECLARE definition text;BEGIN
		SELECT pg_get_functiondef('privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)'::regprocedure) INTO definition;
		EXECUTE replace(definition,'202609120003_guardian_authority_renewal','202609120002_guardian_authority_admin_review');
		SELECT pg_get_functiondef('privacy_activation_authenticated_set_digest(text,uuid[])'::regprocedure) INTO definition;
		EXECUTE replace(definition,'202609120003_guardian_authority_renewal','202609120002_guardian_authority_admin_review');
	END$$`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE guardian_application_intake_release SET enabled=false WHERE singleton`); err != nil {
		t.Fatal(err)
	}

	if _, err = tx.Exec(ctx, `SAVEPOINT wrong_renewal_predecessor;
		DO $$DECLARE definition text;BEGIN
		 SELECT pg_get_functiondef('privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)'::regprocedure) INTO definition;
		 EXECUTE replace(definition,'202609120002_guardian_authority_admin_review','202609120001_guardian_application_eligibility');
		END$$`); err != nil {
		t.Fatal(err)
	}
	_, wrongErr := tx.Exec(ctx, string(migration))
	var databaseError *pgconn.PgError
	if !errors.As(wrongErr, &databaseError) || databaseError.Message != "guardian_authority_renewal_activation_evidence_predecessor_mismatch" {
		t.Fatalf("migration accepted wrong predecessor: %v", wrongErr)
	}
	if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT wrong_renewal_predecessor`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	var evidenceDefinition, digestDefinition string
	if err = tx.QueryRow(ctx, `SELECT pg_get_functiondef('privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)'::regprocedure)`).Scan(&evidenceDefinition); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT pg_get_functiondef('privacy_activation_authenticated_set_digest(text,uuid[])'::regprocedure)`).Scan(&digestDefinition); err != nil {
		t.Fatal(err)
	}
	for name, definition := range map[string]string{"evidence": evidenceDefinition, "digest": digestDefinition} {
		if strings.Count(definition, "202609120003_guardian_authority_renewal") != 1 || strings.Contains(definition, "202609120002_guardian_authority_admin_review") {
			t.Fatalf("%s function did not advance to exact renewal boundary", name)
		}
	}
	var intakeEnabled, policyEnabled bool
	if err = tx.QueryRow(ctx, `SELECT enabled FROM guardian_application_intake_release WHERE singleton`).Scan(&intakeEnabled); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT COALESCE(bool_or(enabled),false) FROM guardian_authority_policies`).Scan(&policyEnabled); err != nil {
		t.Fatal(err)
	}
	if intakeEnabled {
		t.Fatal("renewal migration unlocked V2 intake")
	}
	_ = policyEnabled // Existing test policies may be enabled; the migration does not create or unlock a policy activation path.
}
