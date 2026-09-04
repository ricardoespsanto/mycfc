package db

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type bootstrapTransactionFake struct {
	pgx.Tx
	versions  []string
	legacy    map[string]bool
	execErr   error
	rowErr    error
	commitErr error
	installed bool
	objects   int
}

func (t *bootstrapTransactionFake) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	if t.execErr != nil {
		return pgconn.CommandTag{}, t.execErr
	}
	if len(args) == 1 {
		if version, ok := args[0].(string); ok {
			t.versions = append(t.versions, version)
		}
	}
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

func (t *bootstrapTransactionFake) QueryRow(_ context.Context, _ string, args ...any) pgx.Row {
	if t.rowErr != nil {
		return bootstrapRow{err: t.rowErr}
	}
	if len(args) == 1 {
		if table, ok := args[0].(string); ok && strings.HasPrefix(table, "public.") {
			return bootstrapRow{exists: t.legacy[table]}
		}
	}
	return bootstrapRow{exists: t.installed, integer: t.objects}
}

type bootstrapRow struct {
	exists  bool
	err     error
	integer int
}

func (r bootstrapRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	switch value := dest[0].(type) {
	case *bool:
		*value = r.exists
	case *int:
		*value = r.integer
	}
	return nil
}

func (t *bootstrapTransactionFake) Rollback(context.Context) error { return nil }
func (t *bootstrapTransactionFake) Commit(context.Context) error   { return t.commitErr }

type bootstrapConnectionFake struct {
	tx  pgx.Tx
	err error
}

func (c bootstrapConnectionFake) Begin(context.Context) (pgx.Tx, error) { return c.tx, c.err }

type bootstrapRoleConnectionFake struct {
	err        error
	statements []string
}

func (c *bootstrapRoleConnectionFake) Exec(_ context.Context, statement string, _ ...any) (pgconn.CommandTag, error) {
	c.statements = append(c.statements, statement)
	return pgconn.NewCommandTag("OK"), c.err
}

func TestMigrationHelpersPropagateDatabaseFailures(t *testing.T) {
	databaseErr := errors.New("database unavailable")
	for _, tc := range []struct {
		name string
		run  func(*bootstrapTransactionFake) error
		fake bootstrapTransactionFake
		want string
	}{
		{"record baseline", func(tx *bootstrapTransactionFake) error { return recordBaselineMigrations(t.Context(), tx) }, bootstrapTransactionFake{execErr: databaseErr}, "record baseline migration"},
		{"inspect legacy table", func(tx *bootstrapTransactionFake) error { return verifyLegacyBaseline(t.Context(), tx) }, bootstrapTransactionFake{rowErr: databaseErr}, "inspect legacy baseline table"},
		{"check incremental migration", func(tx *bootstrapTransactionFake) error { return applyIncrementalMigrations(t.Context(), tx) }, bootstrapTransactionFake{rowErr: databaseErr}, "check migration"},
		{"apply incremental migration", func(tx *bootstrapTransactionFake) error { return applyIncrementalMigrations(t.Context(), tx) }, bootstrapTransactionFake{execErr: databaseErr}, "apply migration"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run(&tc.fake)
			if err == nil || !errors.Is(err, databaseErr) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v, want %q wrapping database error", err, tc.want)
			}
		})
	}
}

func TestBootstrapRolesExecutesAllRoleHardeningStatementsAndStopsOnFailure(t *testing.T) {
	credentials := RoleCredentials{AppUsername: "mycfc_app", AppPassword: "app-password", MigrationUsername: "mycfc_migrate", MigrationPassword: "migration-password"}
	conn := &bootstrapRoleConnectionFake{}
	if err := BootstrapRoles(t.Context(), conn, "mycfc", credentials); err != nil {
		t.Fatal(err)
	}
	if len(conn.statements) < 15 || !strings.Contains(conn.statements[0], "CREATE EXTENSION") || !strings.Contains(conn.statements[len(conn.statements)-1], "ALTER DEFAULT PRIVILEGES") {
		t.Fatalf("bootstrap statements=%#v", conn.statements)
	}
	conn.err = errors.New("permission denied")
	if err := BootstrapRoles(t.Context(), conn, "mycfc", credentials); !errors.Is(err, conn.err) || !strings.Contains(err.Error(), "enable citext") {
		t.Fatalf("BootstrapRoles() error=%v", err)
	}
}

func TestApplyBaselineCoversTransactionAndInstalledMigrationOutcomes(t *testing.T) {
	databaseErr := errors.New("database unavailable")
	if err := ApplyBaseline(t.Context(), bootstrapConnectionFake{err: databaseErr}); !errors.Is(err, databaseErr) || !strings.Contains(err.Error(), "begin baseline") {
		t.Fatalf("begin error=%v", err)
	}
	for _, tc := range []struct {
		name string
		tx   bootstrapTransactionFake
		want string
	}{
		{"lock failure", bootstrapTransactionFake{execErr: databaseErr}, "lock baseline"},
		{"marker query failure", bootstrapTransactionFake{rowErr: databaseErr}, "check baseline"},
		{"installed migration commit failure", bootstrapTransactionFake{installed: true, commitErr: databaseErr}, "database unavailable"},
		{"legacy schema missing core table", bootstrapTransactionFake{objects: 1, legacy: map[string]bool{"public.users": true}}, "training_session_outcomes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ApplyBaseline(t.Context(), bootstrapConnectionFake{tx: &tc.tx})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v want=%q", err, tc.want)
			}
		})
	}
}

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

func TestRecordBaselineMigrationsRecordsEmbeddedHistoryThroughCutoff(t *testing.T) {
	tx := &bootstrapTransactionFake{}
	if err := recordBaselineMigrations(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	if len(tx.versions) == 0 || tx.versions[len(tx.versions)-1] != baselineIncludesThrough {
		t.Fatalf("recorded versions=%#v cutoff=%q", tx.versions, baselineIncludesThrough)
	}
	seen := map[string]bool{}
	for _, version := range tx.versions {
		if version > baselineIncludesThrough || seen[version] {
			t.Fatalf("invalid baseline migration history=%#v", tx.versions)
		}
		seen[version] = true
	}
	if !seen[baselineIncludesThrough] {
		t.Fatalf("cutoff %q was not recorded", baselineIncludesThrough)
	}
}

func TestVerifyLegacyBaselineRequiresCoreTablesBeforeAdoption(t *testing.T) {
	t.Run("complete", func(t *testing.T) {
		tx := &bootstrapTransactionFake{legacy: map[string]bool{"public.users": true, "public.training_session_outcomes": true, "public.sessions": true}}
		if err := verifyLegacyBaseline(context.Background(), tx); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("missing core table", func(t *testing.T) {
		tx := &bootstrapTransactionFake{legacy: map[string]bool{"public.users": true, "public.training_session_outcomes": false, "public.sessions": true}}
		err := verifyLegacyBaseline(context.Background(), tx)
		if err == nil || !strings.Contains(err.Error(), "training_session_outcomes") {
			t.Fatalf("error=%v", err)
		}
	})
}

func TestApplyIncrementalMigrationsExecutesAndRecordsEveryUninstalledFile(t *testing.T) {
	entries, err := fs.Glob(migrationFiles, "migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx := &bootstrapTransactionFake{}
	if err := applyIncrementalMigrations(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	if len(tx.versions) != len(entries) {
		t.Fatalf("recorded=%d migrations=%d versions=%#v", len(tx.versions), len(entries), tx.versions)
	}
	for index, name := range entries {
		if tx.versions[index] != migrationVersion(name) {
			t.Fatalf("recorded version %d=%q want=%q", index, tx.versions[index], migrationVersion(name))
		}
	}
}

func TestBootstrapSQLQuotesIdentifiersAndPasswordsWithoutInterpolationLeaks(t *testing.T) {
	if got, want := quoteIdentifier(`role"name`), `"role""name"`; got != want {
		t.Fatalf("quoted identifier=%q want=%q", got, want)
	}
	if got, want := quoteLiteral(`pa'ssword`), `'pa''ssword'`; got != want {
		t.Fatalf("quoted literal=%q want=%q", got, want)
	}
	roleSQL := roleStatement("app_user", "pa'ssword")
	if !strings.Contains(roleSQL, `CREATE ROLE "app_user"`) || !strings.Contains(roleSQL, `'pa''ssword'`) || strings.Contains(roleSQL, `pa'ssword`) {
		t.Fatalf("role SQL=%q", roleSQL)
	}
	ownershipSQL := transferOwnershipStatement("migrator")
	for _, expected := range []string{"ALTER TABLE", "ALTER FUNCTION", "ALTER TYPE", "'migrator'"} {
		if !strings.Contains(ownershipSQL, expected) {
			t.Errorf("ownership SQL missing %q", expected)
		}
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

func TestFeatureFlagsExistInBaselineAndUseBaselineSafeMigration(t *testing.T) {
	migration, err := migrationFiles.ReadFile("migrations/202608120002_feature_flags.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"feature_availability_mode", "CREATE TABLE feature_flags", "CREATE TABLE feature_flag_events", "feature_flag_events_immutable_trigger"} {
		if !strings.Contains(baselineSchema, expected) {
			t.Errorf("baseline does not contain %q", expected)
		}
	}
	for _, expected := range []string{"EXCEPTION WHEN duplicate_object THEN NULL", "CREATE TABLE IF NOT EXISTS feature_flags", "CREATE TABLE IF NOT EXISTS feature_flag_events", "ON CONFLICT (feature_key) DO NOTHING", "CREATE OR REPLACE FUNCTION audit_feature_flag_change", "IF NOT EXISTS (SELECT 1 FROM pg_trigger"} {
		if !strings.Contains(string(migration), expected) {
			t.Errorf("forward migration is not baseline-safe: missing %q", expected)
		}
	}
	for _, prohibited := range []string{"DROP TABLE", "TRUNCATE", "DELETE FROM suggestions", "DELETE FROM feature_flag"} {
		if strings.Contains(strings.ToUpper(string(migration)), strings.ToUpper(prohibited)) {
			t.Errorf("forward migration contains prohibited statement %q", prohibited)
		}
	}
}

func TestPrivatePhotoAlbumMigrationCanFollowCurrentBaseline(t *testing.T) {
	migration, err := migrationFiles.ReadFile("migrations/202608120001_private_photo_albums.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"EXCEPTION WHEN duplicate_object THEN NULL", "CREATE TABLE IF NOT EXISTS photo_albums", "CREATE TABLE IF NOT EXISTS photo_album_programme_audiences", "CREATE TABLE IF NOT EXISTS photo_album_team_audiences", "CREATE TABLE IF NOT EXISTS photo_album_audit_events", "CREATE OR REPLACE FUNCTION", "IF NOT EXISTS (SELECT 1 FROM pg_trigger"} {
		if !strings.Contains(string(migration), expected) {
			t.Errorf("photo-album migration is not baseline-safe: missing %q", expected)
		}
	}
}

func TestStructuredTrainingFoundationExistsInBaselineAndForwardMigration(t *testing.T) {
	migration, err := migrationFiles.ReadFile("migrations/202608120003_structured_training_foundation.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"CREATE TABLE training_groups", "CREATE TABLE training_group_members", "season_id uuid NULL REFERENCES seasons", "CREATE TABLE training_session_segments", "CREATE TABLE training_segment_blocks", "move_training_session_segment", "move_training_segment_block", "structured_training_planning"} {
		if !strings.Contains(baselineSchema, expected) {
			t.Errorf("baseline does not contain %q", expected)
		}
	}
	for _, expected := range []string{"EXCEPTION WHEN duplicate_object THEN NULL", "CREATE TABLE IF NOT EXISTS training_groups", "CREATE TABLE IF NOT EXISTS training_group_members", "ADD COLUMN IF NOT EXISTS training_group_id", "ADD COLUMN IF NOT EXISTS season_id", "training_plans_structured_season_valid", "ADD COLUMN IF NOT EXISTS entry_kind", "CREATE TABLE IF NOT EXISTS training_session_segments", "CREATE TABLE IF NOT EXISTS training_segment_blocks", "CREATE OR REPLACE FUNCTION move_training_session_segment", "CREATE OR REPLACE FUNCTION move_training_segment_block", "ON CONFLICT (feature_key) DO NOTHING"} {
		if !strings.Contains(string(migration), expected) {
			t.Errorf("forward migration is not baseline-safe: missing %q", expected)
		}
	}
	for _, prohibited := range []string{"DROP TABLE", "TRUNCATE", "DELETE FROM training_", "DELETE FROM feature_flag"} {
		if strings.Contains(strings.ToUpper(string(migration)), strings.ToUpper(prohibited)) {
			t.Errorf("forward migration contains prohibited statement %q", prohibited)
		}
	}
}

func TestStructuredTrainingSeasonCanFollowTheFoundationMigration(t *testing.T) {
	migration, err := migrationFiles.ReadFile("migrations/202608130001_structured_training_season.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"ADD COLUMN IF NOT EXISTS season_id", "REFERENCES seasons", "training_plans_structured_season_valid", "training_group_id IS NOT NULL AND season_id IS NOT NULL"} {
		if !strings.Contains(string(migration), expected) {
			t.Errorf("season migration is not foundation-safe: missing %q", expected)
		}
	}
	for _, prohibited := range []string{"DROP TABLE", "TRUNCATE", "DELETE FROM training_"} {
		if strings.Contains(strings.ToUpper(string(migration)), strings.ToUpper(prohibited)) {
			t.Errorf("season migration contains prohibited statement %q", prohibited)
		}
	}
}

func TestStructuredGymWorkoutsExistInBaselineAndForwardMigration(t *testing.T) {
	migration, err := migrationFiles.ReadFile("migrations/202608130002_structured_gym_workouts.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"CREATE TYPE gym_block_structure", "CREATE TYPE training_objective", "CREATE TABLE gym_block_prescriptions", "CREATE TABLE gym_exercises", "planned_start_offset_minutes", "transition_duration_minutes", "equipment_notes", "move_gym_exercise"} {
		if !strings.Contains(baselineSchema, expected) {
			t.Errorf("baseline does not contain %q", expected)
		}
	}
	for _, expected := range []string{"EXCEPTION WHEN duplicate_object THEN NULL", "ADD COLUMN IF NOT EXISTS planned_start_offset_minutes", "CREATE TABLE IF NOT EXISTS gym_block_prescriptions", "CREATE TABLE IF NOT EXISTS gym_exercises", "CREATE OR REPLACE FUNCTION move_gym_exercise"} {
		if !strings.Contains(string(migration), expected) {
			t.Errorf("gym migration is not baseline-safe: missing %q", expected)
		}
	}
	for _, prohibited := range []string{"DROP TABLE", "TRUNCATE", "DELETE FROM training_"} {
		if strings.Contains(strings.ToUpper(string(migration)), strings.ToUpper(prohibited)) {
			t.Errorf("gym migration contains prohibited statement %q", prohibited)
		}
	}
}

func TestReusableTrainingRoutinesExistInBaselineAndForwardMigration(t *testing.T) {
	migration, err := migrationFiles.ReadFile("migrations/202608130003_reusable_training_routines.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"CREATE TYPE training_routine_kind", "CREATE TABLE training_routines", "CREATE TABLE training_copy_events", "training_block_snapshot", "restore_training_session"} {
		if !strings.Contains(baselineSchema, expected) {
			t.Errorf("baseline does not contain %q", expected)
		}
	}
	for _, expected := range []string{"EXCEPTION WHEN duplicate_object THEN NULL", "CREATE TABLE IF NOT EXISTS training_routines", "CREATE TABLE IF NOT EXISTS training_copy_events", "CREATE OR REPLACE FUNCTION training_session_snapshot", "CREATE OR REPLACE FUNCTION restore_training_session"} {
		if !strings.Contains(string(migration), expected) {
			t.Errorf("routine migration is not baseline-safe: missing %q", expected)
		}
	}
	for _, prohibited := range []string{"DROP TABLE", "TRUNCATE", "DELETE FROM training_"} {
		if strings.Contains(strings.ToUpper(string(migration)), strings.ToUpper(prohibited)) {
			t.Errorf("routine migration contains prohibited statement %q", prohibited)
		}
	}
}

func TestTrainingPrescriptionPublicationExistsInBaselineAndForwardMigration(t *testing.T) {
	migration, err := migrationFiles.ReadFile("migrations/202608140002_training_prescriptions.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"CREATE TABLE training_plan_publications", "CREATE TABLE training_prescriptions", "prescription_id uuid NULL REFERENCES training_prescriptions", "prevent_training_publication_mutation", "touch_structured_training_plan"} {
		if !strings.Contains(baselineSchema, expected) {
			t.Errorf("baseline does not contain %q", expected)
		}
		if !strings.Contains(string(migration), expected) {
			t.Errorf("publication migration does not contain %q", expected)
		}
	}
	for _, prohibited := range []string{"DROP TABLE", "TRUNCATE", "DELETE FROM training_"} {
		if strings.Contains(strings.ToUpper(string(migration)), strings.ToUpper(prohibited)) {
			t.Errorf("publication migration contains prohibited statement %q", prohibited)
		}
	}
}

func TestTrainingFeedbackExistsInBaselineAndForwardMigration(t *testing.T) {
	migration, err := migrationFiles.ReadFile("migrations/202608170001_training_feedback.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"actual_duration_minutes", "perceived_exertion", "recovery_feeling", "perception_note", "version integer NOT NULL DEFAULT 1", "training_outcomes_feedback_valid"} {
		if !strings.Contains(baselineSchema, expected) {
			t.Errorf("baseline does not contain %q", expected)
		}
		if !strings.Contains(string(migration), expected) {
			t.Errorf("feedback migration does not contain %q", expected)
		}
	}
	for _, prohibited := range []string{"DROP TABLE", "TRUNCATE", "DELETE FROM training_"} {
		if strings.Contains(strings.ToUpper(string(migration)), strings.ToUpper(prohibited)) {
			t.Errorf("feedback migration contains prohibited statement %q", prohibited)
		}
	}
}

func TestResetBaselineIncludesEveryBundledMigration(t *testing.T) {
	entries, err := fs.Glob(migrationFiles, "migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no embedded migrations")
	}
	latest := migrationVersion(entries[len(entries)-1])
	if baselineIncludesThrough != latest {
		t.Fatalf("baseline cutoff = %q, latest migration = %q; update the complete reset baseline and cutoff together", baselineIncludesThrough, latest)
	}
}
