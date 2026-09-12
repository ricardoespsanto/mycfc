package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/cfcoimbra/mycfc/internal/config"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type databaseCommandConnectionFake struct {
	pgx.Tx
	statements int
	sql        []string
	tx         pgx.Tx
	commitErr  error
}

func (c *databaseCommandConnectionFake) Close(context.Context) error { return nil }
func (c *databaseCommandConnectionFake) Exec(_ context.Context, statement string, _ ...any) (pgconn.CommandTag, error) {
	c.statements++
	c.sql = append(c.sql, statement)
	return pgconn.NewCommandTag("OK"), nil
}
func (c *databaseCommandConnectionFake) Begin(context.Context) (pgx.Tx, error) {
	if c.tx != nil {
		return c.tx, nil
	}
	return c, nil
}
func (c *databaseCommandConnectionFake) Commit(context.Context) error   { return c.commitErr }
func (c *databaseCommandConnectionFake) Rollback(context.Context) error { return nil }

type databaseMigrationTransactionFake struct{ pgx.Tx }

func (databaseMigrationTransactionFake) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag("OK"), nil
}
func (databaseMigrationTransactionFake) QueryRow(context.Context, string, ...any) pgx.Row {
	return databaseMigrationRowFake{}
}
func (databaseMigrationTransactionFake) Commit(context.Context) error   { return nil }
func (databaseMigrationTransactionFake) Rollback(context.Context) error { return nil }

type databaseMigrationRowFake struct{}

func (databaseMigrationRowFake) Scan(dest ...any) error {
	for _, destination := range dest {
		if installed, ok := destination.(*bool); ok {
			*installed = true
		}
	}
	return nil
}

func TestConfigDatabaseURLEscapesCredentials(t *testing.T) {
	cfg := config.Config{
		DBHost:     "database.example.internal",
		DBPort:     5432,
		DBName:     "mycfc",
		DBUser:     "master",
		DBPassword: config.Secret("password:/?#"),
		DBSSLMode:  "disable",
	}

	raw, err := cfg.ResolvedDatabaseURL()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	password, _ := parsed.User.Password()
	if parsed.User.Username() != "master" || password != "password:/?#" || parsed.Query().Get("sslmode") != "disable" {
		t.Fatalf("database URL = %q", raw)
	}
}

func TestMainDispatchesPrivacyCommand(t *testing.T) {
	previousArgs := os.Args
	previousCommand := executePrivacyCommand
	t.Cleanup(func() {
		os.Args = previousArgs
		executePrivacyCommand = previousCommand
	})
	os.Args = []string{"mycfc", "privacy", "expire", "--actor", "test-actor"}
	var got []string
	executePrivacyCommand = func(_ context.Context, args []string) error {
		got = append(got, args...)
		return nil
	}

	main()

	if strings.Join(got, " ") != "expire --actor test-actor" {
		t.Fatalf("privacy arguments = %q", got)
	}
}

func TestRunServerCommandDispatchesDatabaseCommand(t *testing.T) {
	if err := runServerCommand(context.Background(), []string{"not-a-command"}); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("database command error = %v", err)
	}
}

func TestRunServerCommandRedactsPrivacyErrors(t *testing.T) {
	previousCommand := executePrivacyCommand
	t.Cleanup(func() { executePrivacyCommand = previousCommand })
	executePrivacyCommand = func(context.Context, []string) error {
		return errors.New("private database detail")
	}

	err := runServerCommand(context.Background(), []string{"privacy", "expire"})
	if err == nil || err.Error() != "privacy operator command failed" {
		t.Fatalf("privacy command error = %v", err)
	}
}

func TestConfigDatabaseURLAcceptsDatabaseURL(t *testing.T) {
	cfg := config.Config{DatabaseURL: config.Secret("postgres://user:password@localhost:5432/mycfc?sslmode=disable")}

	raw, err := cfg.ResolvedDatabaseURL()
	if err != nil {
		t.Fatal(err)
	}
	if raw != "postgres://user:password@localhost:5432/mycfc?sslmode=disable" {
		t.Fatalf("database URL = %q", raw)
	}
}

func TestDatabaseURLFromEnvironmentEscapesComponents(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DB_HOST", "postgres")
	t.Setenv("DB_PORT", "5432")
	t.Setenv("DB_NAME", "mycfc")
	t.Setenv("DB_USER", "mycfc_app")
	t.Setenv("DB_PASSWORD", "password:/?#")
	t.Setenv("DB_SSLMODE", "disable")

	raw, ok, err := databaseURLFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("databaseURLFromEnvironment() ok = false")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	password, _ := parsed.User.Password()
	if parsed.User.Username() != "mycfc_app" || password != "password:/?#" || parsed.Query().Get("sslmode") != "disable" {
		t.Fatalf("database URL = %q", raw)
	}
}

func TestDatabaseURLFromEnvironmentUsesRawURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://raw:secret@localhost:5432/mycfc?sslmode=disable")
	t.Setenv("DB_HOST", "")

	raw, ok, err := databaseURLFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	if !ok || raw != os.Getenv("DATABASE_URL") {
		t.Fatalf("databaseURLFromEnvironment() = %q, %t", raw, ok)
	}
}

func TestDatabaseURLFromEnvironmentMissingComponents(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DB_HOST", "postgres")
	t.Setenv("DB_PORT", "")

	raw, ok, err := databaseURLFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	if ok || raw != "" {
		t.Fatalf("databaseURLFromEnvironment() = %q, %t", raw, ok)
	}
}

func TestDatabaseURLFromEnvironmentDefaultsSSLModeForComponentConfiguration(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DB_HOST", "postgres")
	t.Setenv("DB_PORT", "5432")
	t.Setenv("DB_NAME", "mycfc")
	t.Setenv("DB_USER", "mycfc_app")
	t.Setenv("DB_PASSWORD", "password")
	t.Setenv("DB_SSLMODE", "")

	raw, ok, err := databaseURLFromEnvironment()
	if err != nil || !ok {
		t.Fatalf("databaseURLFromEnvironment() = %q, %t, %v", raw, ok, err)
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Query().Get("sslmode") != "require" {
		t.Fatalf("database URL = %q, error=%v", raw, err)
	}
}

func TestRunDatabaseCommandRejectsUnknownCommandsBeforeAccessingDatabase(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://unreachable.example.test/mycfc")
	err := runDatabaseCommand(context.Background(), "destroy-everything")
	if err == nil || err.Error() != `unknown command "destroy-everything"` {
		t.Fatalf("error=%v", err)
	}
}

func TestRunDatabaseCommandWrapsDatabaseConnectionFailure(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://127.0.0.1:1/mycfc?connect_timeout=1")
	err := runDatabaseCommand(context.Background(), "migrate")
	if err == nil || !strings.Contains(err.Error(), "connect to database") {
		t.Fatalf("error=%v", err)
	}
}

func TestRunDatabaseCommandBootstrapsUsingExplicitEnvironmentConnection(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://mycfc:secret@localhost:5432/mycfc?sslmode=disable")
	t.Setenv("DB_NAME", "mycfc")
	t.Setenv("APP_DB_USER", "mycfc_app")
	t.Setenv("APP_DB_PASSWORD", "app-password")
	t.Setenv("MIGRATION_DB_USER", "mycfc_migrate")
	t.Setenv("MIGRATION_DB_PASSWORD", "migration-password")
	original := connectDatabaseCommand
	t.Cleanup(func() { connectDatabaseCommand = original })
	connection := &databaseCommandConnectionFake{}
	connectDatabaseCommand = func(context.Context, string) (databaseCommandConnection, error) { return connection, nil }
	if err := runDatabaseCommand(t.Context(), "bootstrap-db"); err != nil || connection.statements < 15 {
		t.Fatalf("statements=%d error=%v", connection.statements, err)
	}
}

func TestRunDatabaseCommandExplicitlyProvisionsBreakGlassDisableRole(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://postgres:admin@localhost:5432/mycfc?sslmode=disable")
	t.Setenv("PRIVACY_ACTIVATION_DISABLE_DATABASE_URL", "postgres://mycfc_privacy_activation_disable:independent@postgres:5432/mycfc?sslmode=disable")
	original := connectDatabaseCommand
	t.Cleanup(func() { connectDatabaseCommand = original })
	connection := &databaseCommandConnectionFake{}
	connectDatabaseCommand = func(context.Context, string) (databaseCommandConnection, error) { return connection, nil }
	if err := runDatabaseCommand(t.Context(), "provision-privacy-activation-disable"); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(connection.sql, "\n")
	if !strings.Contains(joined, `CREATE ROLE "mycfc_privacy_activation_disable" LOGIN`) ||
		!strings.Contains(joined, `GRANT EXECUTE ON FUNCTION privacy_disable.privacy_activation_disable(uuid,text)`) {
		t.Fatalf("provisioning statements=%#v", connection.sql)
	}

	t.Setenv("PRIVACY_ACTIVATION_DISABLE_DATABASE_URL", "postgres://wrong:independent@postgres:5432/mycfc?sslmode=disable")
	if err := runDatabaseCommand(t.Context(), "provision-privacy-activation-disable"); err == nil {
		t.Fatal("wrong break-glass role was accepted")
	}
}

func TestRunDatabaseCommandExplicitlyProvisionsGuardianActivationRole(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://postgres:admin@localhost:5432/mycfc?sslmode=disable")
	t.Setenv("GUARDIAN_ACTIVATION_DATABASE_URL", "postgres://mycfc_guardian_activation_operator:independent@postgres:5432/mycfc?sslmode=disable")
	original := connectDatabaseCommand
	t.Cleanup(func() { connectDatabaseCommand = original })
	connection := &databaseCommandConnectionFake{}
	connectDatabaseCommand = func(context.Context, string) (databaseCommandConnection, error) { return connection, nil }
	if err := runDatabaseCommand(t.Context(), "provision-guardian-activation"); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(connection.sql, "\n")
	if !strings.Contains(joined, `CREATE ROLE "mycfc_guardian_activation_operator" LOGIN`) ||
		!strings.Contains(joined, `GRANT EXECUTE ON FUNCTION guardian_ops.status(text,text,text)`) {
		t.Fatalf("provisioning statements=%#v", connection.sql)
	}

	t.Setenv("GUARDIAN_ACTIVATION_DATABASE_URL", "postgres://mycfc_app:independent@postgres:5432/mycfc?sslmode=disable")
	if err := runDatabaseCommand(t.Context(), "provision-guardian-activation"); err == nil {
		t.Fatal("web role was accepted as guardian activation operator")
	}
}

func TestRunDatabaseCommandBindsGuardianReleaseThroughDisableOnlyAPI(t *testing.T) {
	releaseURL := "postgres://mycfc_guardian_release_bind:independent@postgres:5432/mycfc?sslmode=disable"
	t.Setenv("DATABASE_URL", "postgres://mycfc_migrator:forbidden@localhost:5432/mycfc?sslmode=disable")
	t.Setenv("GUARDIAN_RELEASE_BIND_DATABASE_URL", releaseURL)
	t.Setenv("GUARDIAN_RELEASE_BIND_EXPECTED_DATABASE", "mycfc")
	t.Setenv("GUARDIAN_RUNTIME_IMAGE_DIGEST", "sha256:"+strings.Repeat("a", 64))
	original := connectDatabaseCommand
	t.Cleanup(func() { connectDatabaseCommand = original })
	connection := &databaseCommandConnectionFake{}
	connectedURL := ""
	connectDatabaseCommand = func(_ context.Context, url string) (databaseCommandConnection, error) {
		connectedURL = url
		return connection, nil
	}
	if err := runDatabaseCommand(t.Context(), "bind-guardian-release"); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(connection.sql, "\n")
	if connectedURL != releaseURL || !strings.Contains(joined, "guardian_ops.release_disable_and_bind") || strings.Contains(joined, "guardian_ops.enable") {
		t.Fatalf("connected=%q release binding statements=%#v", connectedURL, connection.sql)
	}

	t.Setenv("GUARDIAN_RUNTIME_IMAGE_DIGEST", "sha256:invalid")
	if err := runDatabaseCommand(t.Context(), "bind-guardian-release"); err == nil {
		t.Fatal("invalid release image digest accepted")
	}
	t.Setenv("GUARDIAN_RUNTIME_IMAGE_DIGEST", "sha256:"+strings.Repeat("a", 64))
	t.Setenv("GUARDIAN_RELEASE_BIND_DATABASE_URL", "postgres://mycfc_migrate:forbidden@postgres:5432/mycfc?sslmode=disable")
	if err := runDatabaseCommand(t.Context(), "bind-guardian-release"); err == nil {
		t.Fatal("migration credential was accepted for release binding")
	}
}

func TestRunDatabaseCommandExplicitlyProvisionsGuardianReleaseBindRole(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://postgres:admin@localhost:5432/mycfc?sslmode=disable")
	t.Setenv("GUARDIAN_RELEASE_BIND_DATABASE_URL", "postgres://mycfc_guardian_release_bind:independent@postgres:5432/mycfc?sslmode=disable")
	original := connectDatabaseCommand
	t.Cleanup(func() { connectDatabaseCommand = original })
	connection := &databaseCommandConnectionFake{}
	connectDatabaseCommand = func(context.Context, string) (databaseCommandConnection, error) { return connection, nil }
	if err := runDatabaseCommand(t.Context(), "provision-guardian-release-bind"); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(connection.sql, "\n")
	if !strings.Contains(joined, `CREATE ROLE "mycfc_guardian_release_bind" LOGIN`) ||
		!strings.Contains(joined, `GRANT EXECUTE ON FUNCTION guardian_ops.release_disable_and_bind(text,text,text)`) ||
		strings.Contains(joined, `guardian_ops.enable`) {
		t.Fatalf("provisioning statements=%#v", connection.sql)
	}

	t.Setenv("GUARDIAN_RELEASE_BIND_DATABASE_URL", "postgres://mycfc_migrate:forbidden@postgres:5432/mycfc?sslmode=disable")
	if err := runDatabaseCommand(t.Context(), "provision-guardian-release-bind"); err == nil {
		t.Fatal("migration role was accepted as guardian release bind identity")
	}
}

func TestProvisionGuardianReleaseBindEmitsSuccessOnlyAfterCommit(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://postgres:bootstrap-secret@localhost:5432/mycfc?sslmode=disable")
	t.Setenv("GUARDIAN_RELEASE_BIND_DATABASE_URL", "postgres://mycfc_guardian_release_bind:independent-secret@postgres:5432/mycfc?sslmode=disable")
	originalConnect := connectDatabaseCommand
	t.Cleanup(func() { connectDatabaseCommand = originalConnect })
	var output bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, &slog.HandlerOptions{ReplaceAttr: func(_ []string, attribute slog.Attr) slog.Attr {
		if attribute.Key == slog.TimeKey {
			return slog.Attr{}
		}
		return attribute
	}})))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	connection := &databaseCommandConnectionFake{}
	connectDatabaseCommand = func(context.Context, string) (databaseCommandConnection, error) { return connection, nil }
	for range 2 {
		if err := runDatabaseCommand(t.Context(), "provision-guardian-release-bind"); err != nil {
			t.Fatal(err)
		}
	}
	const success = "event=guardian_release_bind_role_provisioned"
	if count := strings.Count(output.String(), success); count != 2 {
		t.Fatalf("success event count=%d output=%q", count, output.String())
	}
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		if !strings.Contains(line, success) {
			continue
		}
		for _, forbidden := range []string{"bootstrap-secret", "independent-secret", "postgres://", "mycfc_guardian_release_bind", "database_name=", "database_host=", "connection_role="} {
			if strings.Contains(line, forbidden) {
				t.Fatalf("success event contains forbidden identity or credential %q: %q", forbidden, line)
			}
		}
	}

	output.Reset()
	connection.commitErr = errors.New("commit failed")
	if err := runDatabaseCommand(t.Context(), "provision-guardian-release-bind"); err == nil {
		t.Fatal("commit failure was accepted")
	}
	if strings.Contains(output.String(), success) {
		t.Fatalf("success emitted before commit: %q", output.String())
	}
}

func TestRunDatabaseCommandHardensUsingOptionalExecutorCredentials(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://mycfc:secret@localhost:5432/mycfc?sslmode=disable")
	t.Setenv("DB_NAME", "mycfc")
	t.Setenv("APP_DB_USER", "mycfc_app")
	t.Setenv("APP_DB_PASSWORD", "app-password")
	t.Setenv("MIGRATION_DB_USER", "mycfc_migrate")
	t.Setenv("MIGRATION_DB_PASSWORD", "migration-password")
	t.Setenv("PRIVACY_EXECUTOR_DB_USER", "mycfc_privacy_executor")
	t.Setenv("PRIVACY_EXECUTOR_DB_PASSWORD", "executor-password")
	t.Setenv("PRIVACY_RESTORE_OBSERVER_DB_USER", "mycfc_privacy_restore_observer")
	t.Setenv("PRIVACY_RESTORE_OBSERVER_DB_PASSWORD", "observer-password")
	original := connectDatabaseCommand
	t.Cleanup(func() { connectDatabaseCommand = original })
	connection := &databaseCommandConnectionFake{}
	connectDatabaseCommand = func(context.Context, string) (databaseCommandConnection, error) { return connection, nil }
	if err := runDatabaseCommand(t.Context(), "harden-db"); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(connection.sql, "\n")
	if !strings.Contains(joined, `REVOKE ALL PRIVILEGES ON TABLE privacy_pseudonymous_principals, privacy_erasure_executions`) ||
		!strings.Contains(joined, `TO "mycfc_privacy_executor"`) ||
		!strings.Contains(joined, `privacy_restore_observe_inventory(text,bytea,bytea,text,text,text,text) TO "mycfc_privacy_restore_observer"`) {
		t.Fatalf("hardening statements=%#v", connection.sql)
	}
}

func TestRunDatabaseCommandMigratesUsingExplicitEnvironmentConnection(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://mycfc:secret@localhost:5432/mycfc?sslmode=disable")
	t.Setenv("DB_NAME", "mycfc")
	t.Setenv("APP_DB_USER", "mycfc_app")
	t.Setenv("APP_DB_PASSWORD", "app-secret")
	t.Setenv("MIGRATION_DB_USER", "mycfc_migrate")
	t.Setenv("MIGRATION_DB_PASSWORD", "migration-secret")
	original := connectDatabaseCommand
	t.Cleanup(func() { connectDatabaseCommand = original })
	connection := &databaseCommandConnectionFake{tx: databaseMigrationTransactionFake{}}
	connectDatabaseCommand = func(context.Context, string) (databaseCommandConnection, error) { return connection, nil }
	if err := runDatabaseCommand(t.Context(), "migrate"); err != nil {
		t.Fatal(err)
	}
}

func TestRunDatabaseCommandUsesLegacyRoleContractWhenMigrationConnectionHasNoRoles(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://mycfc_migrate:connection-secret@localhost:5432/mycfc?sslmode=disable")
	t.Setenv("APP_DB_USER", "")
	t.Setenv("APP_DB_PASSWORD", "")
	t.Setenv("MIGRATION_DB_USER", "")
	t.Setenv("MIGRATION_DB_PASSWORD", "")
	originalConnect, originalLoad := connectDatabaseCommand, loadDatabaseCommandConfig
	t.Cleanup(func() {
		connectDatabaseCommand, loadDatabaseCommandConfig = originalConnect, originalLoad
	})
	loadDatabaseCommandConfig = func(context.Context) (config.Config, error) {
		t.Fatal("legacy migration compatibility must not require AWS configuration")
		return config.Config{}, nil
	}
	connection := &databaseCommandConnectionFake{tx: databaseMigrationTransactionFake{}}
	connectDatabaseCommand = func(_ context.Context, rawURL string) (databaseCommandConnection, error) {
		if rawURL != "postgres://mycfc_migrate:connection-secret@localhost:5432/mycfc?sslmode=disable" {
			t.Fatalf("database URL=%q", rawURL)
		}
		return connection, nil
	}
	if err := runDatabaseCommand(t.Context(), "migrate"); err != nil {
		t.Fatal(err)
	}
}

func TestRunDatabaseCommandFallsBackToValidatedConfiguration(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DB_HOST", "")
	originalConnect, originalLoad := connectDatabaseCommand, loadDatabaseCommandConfig
	t.Cleanup(func() {
		connectDatabaseCommand, loadDatabaseCommandConfig = originalConnect, originalLoad
	})
	loadDatabaseCommandConfig = func(context.Context) (config.Config, error) {
		return config.Config{DBHost: "localhost", DBPort: 5432, DBName: "mycfc", DBSSLMode: "disable", DBUser: "mycfc_app", DBPassword: config.Secret("app-secret"), MigrationDBUser: "mycfc_migrate", MigrationDBPassword: config.Secret("secret")}, nil
	}
	connection := &databaseCommandConnectionFake{tx: databaseMigrationTransactionFake{}}
	connectDatabaseCommand = func(_ context.Context, rawURL string) (databaseCommandConnection, error) {
		if rawURL != "postgres://mycfc_migrate:secret@localhost:5432/mycfc?sslmode=disable" {
			t.Fatalf("database URL=%q", rawURL)
		}
		return connection, nil
	}
	if err := runDatabaseCommand(t.Context(), "migrate"); err != nil {
		t.Fatal(err)
	}
}

func TestRunDatabaseCommandReturnsFallbackConfigurationFailure(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DB_HOST", "")
	original := loadDatabaseCommandConfig
	t.Cleanup(func() { loadDatabaseCommandConfig = original })
	loadDatabaseCommandConfig = func(context.Context) (config.Config, error) {
		return config.Config{}, errors.New("configuration unavailable")
	}
	if err := runDatabaseCommand(t.Context(), "migrate"); err == nil || !strings.Contains(err.Error(), "configuration unavailable") {
		t.Fatalf("error=%v", err)
	}
}

func TestRunDatabaseCommandBootstrapsUsingFallbackConfiguration(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DB_HOST", "")
	originalConnect, originalLoad := connectDatabaseCommand, loadDatabaseCommandConfig
	t.Cleanup(func() {
		connectDatabaseCommand, loadDatabaseCommandConfig = originalConnect, originalLoad
	})
	loadDatabaseCommandConfig = func(context.Context) (config.Config, error) {
		return config.Config{DBHost: "localhost", DBPort: 5432, DBName: "mycfc", DBSSLMode: "disable", PostgresUser: "postgres", PostgresPassword: config.Secret("bootstrap-secret"), DBUser: "mycfc_app", DBPassword: config.Secret("app-secret"), MigrationDBUser: "mycfc_migrate", MigrationDBPassword: config.Secret("migration-secret")}, nil
	}
	connection := &databaseCommandConnectionFake{}
	connectDatabaseCommand = func(context.Context, string) (databaseCommandConnection, error) { return connection, nil }
	if err := runDatabaseCommand(t.Context(), "bootstrap-db"); err != nil || connection.statements < 15 {
		t.Fatalf("statements=%d error=%v", connection.statements, err)
	}
}
