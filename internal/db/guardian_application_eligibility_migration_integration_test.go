//go:build integration

package db

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestGuardianApplicationEligibilityForwardMigrationRequiresExactPredecessorAndDisablesLegacyPolicy(t *testing.T) {
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

	migration, err := migrationFiles.ReadFile("migrations/202609120001_guardian_application_eligibility.sql")
	if err != nil {
		t.Fatal(err)
	}
	predecessor, err := migrationFiles.ReadFile("migrations/202609110005_guardian_authority_cutoff_reconciliation.sql")
	if err != nil {
		t.Fatal(err)
	}

	if _, err = tx.Exec(ctx, `
		DROP FUNCTION guardian_authority_admin_transition(uuid,uuid,bigint,text,text,text);
		DROP FUNCTION guardian_authority_record_review_view(uuid,uuid);
		DROP FUNCTION guardian_authority_personally_involved(uuid,uuid);
		DROP TABLE guardian_authority_review_access_events;
		ALTER TABLE guardian_authority_events DISABLE TRIGGER guardian_authority_events_immutable;
		DELETE FROM guardian_authority_events WHERE actor_role='ADMIN';
		ALTER TABLE guardian_authority_events ENABLE TRIGGER guardian_authority_events_immutable;
		ALTER TABLE guardian_authority_events DROP CONSTRAINT guardian_authority_events_check2;
		ALTER TABLE guardian_authority_events DROP CONSTRAINT guardian_authority_events_check3;
		ALTER TABLE guardian_authority_events DROP CONSTRAINT guardian_authority_events_actor_role_check;
		ALTER TABLE guardian_authority_events DROP CONSTRAINT guardian_authority_events_check1;
		ALTER TABLE guardian_authority_events DROP CONSTRAINT guardian_authority_events_check;
		ALTER TABLE guardian_authority_events ADD CONSTRAINT guardian_authority_events_actor_role_check CHECK(actor_role IN('GUARDIAN','VERIFIER','SYSTEM'));
		ALTER TABLE guardian_authority_events ADD CONSTRAINT guardian_authority_events_check CHECK((evidence_type IS NULL AND evidence_reference IS NULL AND evidence_sha256 IS NULL) OR (evidence_type IS NOT NULL AND evidence_reference IS NOT NULL AND octet_length(evidence_sha256)=32));
		ALTER TABLE guardian_authority_events ADD CONSTRAINT guardian_authority_events_actor_v2_check CHECK((actor_role='SYSTEM' AND actor_ref IS NULL) OR (actor_role<>'SYSTEM' AND actor_ref IS NOT NULL));
		DROP FUNCTION guardian_authority_create_dependent(text,date,uuid,bytea);
		DROP FUNCTION guardian_authority_issue_invitation(uuid,citext,bytea);
		DROP FUNCTION guardian_authority_revoke_invitation(uuid,uuid);
		DROP FUNCTION guardian_authority_list_invitations(uuid,integer);
		DROP FUNCTION guardian_application_reserve(bytea,bytea,text);
		DROP FUNCTION guardian_application_prune();
		DROP TABLE guardian_application_rate_events;
		DROP TABLE guardian_authority_invitations;
		DROP TABLE guardian_application_intake_release_events;
		DROP TABLE guardian_application_intake_release;
		ALTER TABLE privacy_activation_authenticated_artifacts DROP CONSTRAINT privacy_activation_authenticated_artifacts_v11_check;
		ALTER TABLE guardian_authority_policy_events DROP CONSTRAINT guardian_authority_policy_events_action_check;
		ALTER TABLE guardian_authority_policy_events DROP CONSTRAINT guardian_authority_policy_events_check;
		ALTER TABLE guardian_authority_policy_events ALTER COLUMN actor_ref SET NOT NULL;
		ALTER TABLE guardian_authority_policy_events ADD CONSTRAINT guardian_authority_policy_events_action_check CHECK(action IN('ADOPTED','ENABLED','DISABLED'))`); err != nil {
		t.Fatal(err)
	}
	v8Constraint := migrationFunctionSegment(t, string(predecessor),
		"ALTER TABLE privacy_activation_authenticated_artifacts ADD CONSTRAINT privacy_activation_authenticated_artifacts_v8_check",
		"DO $$DECLARE definition text;old_clause text;new_clause text;")
	if _, err = tx.Exec(ctx, v8Constraint); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `DO $$DECLARE definition text;
	BEGIN
	 SELECT pg_get_functiondef('privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)'::regprocedure) INTO definition;
	 EXECUTE replace(definition,'202609120003_guardian_authority_renewal','202609110005_guardian_authority_cutoff_reconciliation');
	 SELECT pg_get_functiondef('privacy_activation_authenticated_set_digest(text,uuid[])'::regprocedure) INTO definition;
	 EXECUTE replace(definition,'202609120003_guardian_authority_renewal','202609110005_guardian_authority_cutoff_reconciliation');
	END$$`); err != nil {
		t.Fatal(err)
	}

	guardianPredecessor, err := migrationFiles.ReadFile("migrations/202609110004_guardian_authority_verification.sql")
	if err != nil {
		t.Fatal(err)
	}
	createPredecessor := migrationFunctionSegment(t, string(guardianPredecessor),
		"CREATE FUNCTION guardian_authority_create_dependent(",
		"CREATE FUNCTION guardian_authority_transition(")
	policyTogglePredecessor := migrationFunctionSegment(t, string(guardianPredecessor),
		"CREATE FUNCTION guardian_authority_set_policy_enabled(",
		"CREATE FUNCTION guardian_authority_grant_verifier(")
	policyTogglePredecessor = strings.Replace(policyTogglePredecessor, "CREATE FUNCTION", "CREATE OR REPLACE FUNCTION", 1)
	if _, err = tx.Exec(ctx, createPredecessor+"\n"+policyTogglePredecessor); err != nil {
		t.Fatal(err)
	}

	adminID := uuid.New()
	policy := "legacy-policy-" + uuid.NewString()
	if _, err = tx.Exec(ctx, `INSERT INTO users(id,name,email,email_verified_at,password_hash,date_of_birth) VALUES($1,'Legacy policy administrator',$2,clock_timestamp(),'hash','1980-01-01')`, adminID, "legacy-policy-"+uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, adminID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT guardian_authority_adopt_policy($1,$2,'{CIVIL_REGISTRY}','{CONFLICT,LOSS}',30,15)`, adminID, policy); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT guardian_authority_set_policy_enabled($1,$2,true)`, adminID, policy); err != nil {
		t.Fatal(err)
	}

	if _, err = tx.Exec(ctx, `SAVEPOINT wrong_guardian_predecessor;
		DO $$DECLARE definition text;BEGIN
		 SELECT pg_get_functiondef('privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)'::regprocedure) INTO definition;
		 EXECUTE replace(definition,'202609110005_guardian_authority_cutoff_reconciliation','202609110004_guardian_authority_verification');
		END$$`); err != nil {
		t.Fatal(err)
	}
	_, wrongErr := tx.Exec(ctx, string(migration))
	var wrongDatabaseError *pgconn.PgError
	if !errors.As(wrongErr, &wrongDatabaseError) || wrongDatabaseError.Message != "guardian_application_activation_evidence_predecessor_mismatch" {
		t.Fatalf("migration accepted a non-exact predecessor: %v", wrongErr)
	}
	if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT wrong_guardian_predecessor`); err != nil {
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
		if strings.Count(definition, "202609120001_guardian_application_eligibility") != 1 || strings.Contains(definition, "202609110005_guardian_authority_cutoff_reconciliation") {
			t.Fatalf("%s function was not rewritten to the exact new boundary", name)
		}
	}

	var enabled bool
	var migrationEvents int
	if err = tx.QueryRow(ctx, `SELECT enabled FROM guardian_authority_policies WHERE version=$1`, policy).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM guardian_authority_policy_events event JOIN guardian_authority_policies policy_row ON policy_row.id=event.policy_id WHERE policy_row.version=$1 AND event.action='MIGRATION_DISABLED' AND event.actor_ref IS NULL`, policy).Scan(&migrationEvents); err != nil {
		t.Fatal(err)
	}
	if enabled || migrationEvents != 1 {
		t.Fatalf("legacy policy disabled=%t migration audit events=%d", !enabled, migrationEvents)
	}
	var gateEnabled bool
	if err = tx.QueryRow(ctx, `SELECT enabled FROM guardian_application_intake_release WHERE singleton AND gate_version='guardian-intake-v2'`).Scan(&gateEnabled); err != nil {
		t.Fatal(err)
	}
	if gateEnabled {
		t.Fatal("V2 intake release gate was enabled by the migration")
	}
	var appRoleExists bool
	if err = tx.QueryRow(ctx, `SELECT to_regrole('mycfc_app') IS NOT NULL`).Scan(&appRoleExists); err != nil {
		t.Fatal(err)
	}
	if appRoleExists {
		for _, table := range []string{
			"guardian_authority_invitations",
			"guardian_application_rate_events",
			"guardian_application_intake_release",
			"guardian_application_intake_release_events",
		} {
			for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
				var allowed bool
				if err = tx.QueryRow(ctx, `SELECT has_table_privilege('mycfc_app',$1,$2)`, table, privilege).Scan(&allowed); err != nil {
					t.Fatal(err)
				}
				if allowed {
					t.Fatalf("web role retains %s on %s after forward migration", privilege, table)
				}
			}
		}
	}
	if _, err = tx.Exec(ctx, `SAVEPOINT gate_enable_rejected`); err != nil {
		t.Fatal(err)
	}
	if _, gateErr := tx.Exec(ctx, `UPDATE guardian_application_intake_release SET enabled=true WHERE singleton`); gateErr == nil {
		t.Fatal("V2 intake release gate could be activated without a later migration")
	}
	if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT gate_enable_rejected`); err != nil {
		t.Fatal(err)
	}
	_, enableErr := tx.Exec(ctx, `SELECT guardian_authority_set_policy_enabled($1,$2,true)`, adminID, policy)
	var databaseError *pgconn.PgError
	if !errors.As(enableErr, &databaseError) || databaseError.Message != "guardian_authority_policy_replacement_required" {
		t.Fatalf("legacy policy could be re-enabled: %v", enableErr)
	}
}

func TestGuardianAuthorityAdminReviewForwardMigrationRequiresExactEligibilityPredecessor(t *testing.T) {
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

	migration, err := migrationFiles.ReadFile("migrations/202609120002_guardian_authority_admin_review.sql")
	if err != nil {
		t.Fatal(err)
	}
	predecessor, err := migrationFiles.ReadFile("migrations/202609120001_guardian_application_eligibility.sql")
	if err != nil {
		t.Fatal(err)
	}
	reconciliationPredecessor, err := migrationFiles.ReadFile("migrations/202609110005_guardian_authority_cutoff_reconciliation.sql")
	if err != nil {
		t.Fatal(err)
	}

	if _, err = tx.Exec(ctx, `
		DROP FUNCTION guardian_authority_admin_transition(uuid,uuid,bigint,text,text,text);
		DROP FUNCTION guardian_authority_record_review_view(uuid,uuid);
		DROP FUNCTION guardian_authority_personally_involved(uuid,uuid);
		DROP TABLE guardian_authority_review_access_events;
		ALTER TABLE guardian_authority_events DISABLE TRIGGER guardian_authority_events_immutable;
		DELETE FROM guardian_authority_events WHERE actor_role='ADMIN';
		ALTER TABLE guardian_authority_events ENABLE TRIGGER guardian_authority_events_immutable;
		ALTER TABLE guardian_authority_events DROP CONSTRAINT guardian_authority_events_check2;
		ALTER TABLE guardian_authority_events DROP CONSTRAINT guardian_authority_events_check3;
		ALTER TABLE guardian_authority_events DROP CONSTRAINT guardian_authority_events_actor_role_check;
		ALTER TABLE guardian_authority_events DROP CONSTRAINT guardian_authority_events_check1;
		ALTER TABLE guardian_authority_events DROP CONSTRAINT guardian_authority_events_check;
		ALTER TABLE guardian_authority_events ADD CONSTRAINT guardian_authority_events_actor_role_check CHECK(actor_role IN('GUARDIAN','VERIFIER','SYSTEM'));
		ALTER TABLE guardian_authority_events ADD CONSTRAINT guardian_authority_events_check CHECK((evidence_type IS NULL AND evidence_reference IS NULL AND evidence_sha256 IS NULL) OR (evidence_type IS NOT NULL AND evidence_reference IS NOT NULL AND octet_length(evidence_sha256)=32));
		ALTER TABLE guardian_authority_events ADD CONSTRAINT guardian_authority_events_actor_v2_check CHECK((actor_role='SYSTEM' AND actor_ref IS NULL) OR (actor_role<>'SYSTEM' AND actor_ref IS NOT NULL));
		ALTER TABLE privacy_activation_authenticated_artifacts DROP CONSTRAINT privacy_activation_authenticated_artifacts_v11_check`); err != nil {
		t.Fatal(err)
	}
	v9Constraint := migrationFunctionSegment(t, string(predecessor),
		"ALTER TABLE privacy_activation_authenticated_artifacts ADD CONSTRAINT privacy_activation_authenticated_artifacts_v9_check",
		"DO $$DECLARE definition text;old_clause text;new_clause text;")
	if _, err = tx.Exec(ctx, v9Constraint); err != nil {
		t.Fatal(err)
	}
	reconcilePredecessor := migrationFunctionSegment(t, string(reconciliationPredecessor),
		"CREATE OR REPLACE FUNCTION guardian_authority_reconcile_cutoffs()",
		"REVOKE ALL ON FUNCTION guardian_authority_codes_valid(text[]),guardian_authority_reconcile_cutoffs()")
	if _, err = tx.Exec(ctx, reconcilePredecessor); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `DO $$DECLARE definition text;
	BEGIN
	 SELECT pg_get_functiondef('privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)'::regprocedure) INTO definition;
	 EXECUTE replace(definition,'202609120003_guardian_authority_renewal','202609120001_guardian_application_eligibility');
	 SELECT pg_get_functiondef('privacy_activation_authenticated_set_digest(text,uuid[])'::regprocedure) INTO definition;
	 EXECUTE replace(definition,'202609120003_guardian_authority_renewal','202609120001_guardian_application_eligibility');
	END$$`); err != nil {
		t.Fatal(err)
	}

	if _, err = tx.Exec(ctx, `SAVEPOINT wrong_admin_review_predecessor;
		DO $$DECLARE definition text;BEGIN
		 SELECT pg_get_functiondef('privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)'::regprocedure) INTO definition;
		 EXECUTE replace(definition,'202609120001_guardian_application_eligibility','202609110005_guardian_authority_cutoff_reconciliation');
		END$$`); err != nil {
		t.Fatal(err)
	}
	_, wrongErr := tx.Exec(ctx, string(migration))
	var wrongDatabaseError *pgconn.PgError
	if !errors.As(wrongErr, &wrongDatabaseError) || wrongDatabaseError.Message != "guardian_authority_review_activation_evidence_predecessor_mismatch" {
		t.Fatalf("migration accepted a non-exact predecessor: %v", wrongErr)
	}
	if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT wrong_admin_review_predecessor`); err != nil {
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
		if strings.Count(definition, "202609120002_guardian_authority_admin_review") != 1 || strings.Contains(definition, "202609120001_guardian_application_eligibility") {
			t.Fatalf("%s function was not rewritten to the exact new boundary", name)
		}
	}
	var gateEnabled, legacyPolicyEnabled bool
	if err = tx.QueryRow(ctx, `SELECT enabled FROM guardian_application_intake_release WHERE singleton AND gate_version='guardian-intake-v2'`).Scan(&gateEnabled); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM guardian_authority_policies WHERE enabled)`).Scan(&legacyPolicyEnabled); err != nil {
		t.Fatal(err)
	}
	if gateEnabled || legacyPolicyEnabled {
		t.Fatalf("review migration opened intake=%t or legacy policy=%t", gateEnabled, legacyPolicyEnabled)
	}
	for _, routine := range []string{
		"guardian_authority_personally_involved(uuid,uuid)",
		"guardian_authority_record_review_view(uuid,uuid)",
		"guardian_authority_admin_transition(uuid,uuid,bigint,text,text,text)",
	} {
		var exists bool
		if err = tx.QueryRow(ctx, `SELECT to_regprocedure($1) IS NOT NULL`, routine).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Fatalf("review routine %s was not installed", routine)
		}
	}
}
