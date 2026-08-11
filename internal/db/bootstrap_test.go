package db

import (
	"strings"
	"testing"
)

func TestValidateBootstrapInput(t *testing.T) {
	valid := RoleCredentials{AppUsername: "mycfc_app", AppPassword: "app-password", MigrationUsername: "mycfc_migrate", MigrationPassword: "migration-password"}
	if err := validateBootstrapInput("mycfc", valid); err != nil {
		t.Fatal(err)
	}
	valid.MigrationUsername = valid.AppUsername
	if err := validateBootstrapInput("mycfc", valid); err == nil || !strings.Contains(err.Error(), "must differ") {
		t.Fatalf("error = %v", err)
	}
}

func TestRoleStatementQuotesCredentials(t *testing.T) {
	statement := roleStatement("app_user", "secret'with quote")
	if !strings.Contains(statement, `"app_user"`) || !strings.Contains(statement, `'secret''with quote'`) {
		t.Fatalf("statement = %q", statement)
	}
}

func TestTransferOwnershipStatementQuotesRole(t *testing.T) {
	statement := transferOwnershipStatement("migration_user")
	if !strings.Contains(statement, "ALTER FUNCTION") || !strings.Contains(statement, "ALTER TYPE") || !strings.Contains(statement, "'migration_user'") {
		t.Fatalf("statement = %q", statement)
	}
}

func TestMigrationVersion(t *testing.T) {
	version := migrationVersion("migrations/202608040001_leaderboard_distance.sql")
	if version != "202608040001_leaderboard_distance" {
		t.Fatalf("version = %q", version)
	}
}

func TestActivityFoundationExistsInBaselineAndForwardMigration(t *testing.T) {
	migration, err := migrationFiles.ReadFile("migrations/202608060001_activity_integration_foundation.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"activity_connections", "activity_sync_jobs", "synced_activities", "training_session_activity_matches"} {
		statement := "CREATE TABLE " + table
		if !strings.Contains(baselineSchema, statement) {
			t.Errorf("baseline does not contain %q", statement)
		}
		if !strings.Contains(string(migration), "CREATE TABLE IF NOT EXISTS "+table) {
			t.Errorf("forward migration does not contain %q", table)
		}
	}
	for _, destructive := range []string{"DROP TABLE", "TRUNCATE", "DELETE FROM users", "DELETE FROM training_"} {
		if strings.Contains(strings.ToUpper(string(migration)), strings.ToUpper(destructive)) {
			t.Errorf("forward migration contains destructive statement %q", destructive)
		}
	}
}

func TestEmailVerificationExistsInBaselineAndTrustsExistingAdults(t *testing.T) {
	migration, err := migrationFiles.ReadFile("migrations/202608060002_email_verification.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"email_verified_at", "CREATE TABLE IF NOT EXISTS email_verification_tokens", "CREATE TABLE IF NOT EXISTS email_outbox", "CREATE OR REPLACE FUNCTION issue_email_verification", "UPDATE users SET email_verified_at = now()"} {
		if !strings.Contains(string(migration), expected) {
			t.Errorf("forward migration does not contain %q", expected)
		}
		if !strings.Contains(baselineSchema, strings.TrimPrefix(expected, "IF NOT EXISTS ")) && expected == "email_verified_at" {
			t.Errorf("baseline does not contain %q", expected)
		}
	}
	for _, destructive := range []string{"DROP TABLE", "TRUNCATE", "DELETE FROM users"} {
		if strings.Contains(strings.ToUpper(string(migration)), destructive) {
			t.Errorf("forward migration contains destructive statement %q", destructive)
		}
	}
}

func TestLegacyAdultsWithoutVerificationHistoryBecomeUnverified(t *testing.T) {
	migration, err := migrationFiles.ReadFile("migrations/202608060003_require_legacy_email_verification.sql")
	if err != nil {
		t.Fatal(err)
	}
	statement := string(migration)
	for _, expected := range []string{"SET email_verified_at = NULL", "account.is_dependent = false", "NOT EXISTS", "email_verification_tokens"} {
		if !strings.Contains(statement, expected) {
			t.Errorf("forward migration does not contain %q", expected)
		}
	}
	for _, prohibited := range []string{"INSERT INTO email_outbox", "DELETE FROM users", "TRUNCATE", "DROP TABLE"} {
		if strings.Contains(strings.ToUpper(statement), strings.ToUpper(prohibited)) {
			t.Errorf("forward migration contains prohibited statement %q", prohibited)
		}
	}
}

func TestPasswordResetExistsInBaselineAndForwardMigration(t *testing.T) {
	migration, err := migrationFiles.ReadFile("migrations/202608100001_password_reset_tokens.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"CREATE TABLE IF NOT EXISTS password_reset_tokens", "token_digest bytea NOT NULL UNIQUE", "message_type", "password_reset_token_id", "sealed_payload", "CREATE OR REPLACE FUNCTION issue_password_reset", "password_reset_too_soon", "password_reset_limit_exceeded"} {
		if !strings.Contains(string(migration), expected) {
			t.Errorf("forward migration does not contain %q", expected)
		}
		baselineExpected := strings.TrimPrefix(expected, "CREATE TABLE IF NOT EXISTS ")
		if expected == "CREATE TABLE IF NOT EXISTS password_reset_tokens" {
			baselineExpected = "CREATE TABLE password_reset_tokens"
		}
		if expected == "CREATE OR REPLACE FUNCTION issue_password_reset" {
			baselineExpected = "CREATE FUNCTION issue_password_reset"
		}
		if !strings.Contains(baselineSchema, baselineExpected) {
			t.Errorf("baseline does not contain %q", baselineExpected)
		}
	}
	for _, prohibited := range []string{"DROP TABLE", "TRUNCATE", "DELETE FROM users"} {
		if strings.Contains(strings.ToUpper(string(migration)), prohibited) {
			t.Errorf("forward migration contains prohibited statement %q", prohibited)
		}
	}
}

func TestCredentialVersionExistsInBaselineAndForwardMigration(t *testing.T) {
	migration, err := migrationFiles.ReadFile("migrations/202608100002_credential_session_revocation.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"credential_version bigint NOT NULL DEFAULT 1", "users_credential_version_valid", "credential_version > 0"} {
		if !strings.Contains(baselineSchema, expected) {
			t.Errorf("baseline does not contain %q", expected)
		}
		if !strings.Contains(string(migration), expected) {
			t.Errorf("forward migration does not contain %q", expected)
		}
	}
	for _, expected := range []string{"ADD COLUMN IF NOT EXISTS credential_version", "FROM pg_constraint", "conrelid = 'users'::regclass"} {
		if !strings.Contains(string(migration), expected) {
			t.Errorf("forward migration is not baseline-safe: missing %q", expected)
		}
	}
	for _, prohibited := range []string{"DROP TABLE", "TRUNCATE", "DELETE FROM users"} {
		if strings.Contains(strings.ToUpper(string(migration)), prohibited) {
			t.Errorf("forward migration contains prohibited statement %q", prohibited)
		}
	}
}

func TestMemberSuggestionsExistInBaselineAndUseBaselineSafeMigration(t *testing.T) {
	migration, err := migrationFiles.ReadFile("migrations/202608110001_member_suggestions.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"suggestion_category", "suggestion_status", "CREATE TABLE suggestions"} {
		if !strings.Contains(baselineSchema, expected) {
			t.Errorf("baseline does not contain %q", expected)
		}
	}
	for _, expected := range []string{"EXCEPTION WHEN duplicate_object THEN NULL", "CREATE TABLE IF NOT EXISTS suggestions", "CREATE INDEX IF NOT EXISTS suggestions_requester_created_idx", "CREATE INDEX IF NOT EXISTS suggestions_triage_idx"} {
		if !strings.Contains(string(migration), expected) {
			t.Errorf("forward migration is not baseline-safe: missing %q", expected)
		}
	}
	for _, prohibited := range []string{"DROP TABLE", "TRUNCATE", "DELETE FROM suggestions"} {
		if strings.Contains(strings.ToUpper(string(migration)), strings.ToUpper(prohibited)) {
			t.Errorf("forward migration contains prohibited statement %q", prohibited)
		}
	}
}
