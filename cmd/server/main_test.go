package main

import (
	"context"
	"errors"
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
	tx         pgx.Tx
}

func (c *databaseCommandConnectionFake) Close(context.Context) error { return nil }
func (c *databaseCommandConnectionFake) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	c.statements++
	return pgconn.NewCommandTag("OK"), nil
}
func (c *databaseCommandConnectionFake) Begin(context.Context) (pgx.Tx, error) { return c.tx, nil }

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

func TestRunDatabaseCommandMigratesUsingExplicitEnvironmentConnection(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://mycfc:secret@localhost:5432/mycfc?sslmode=disable")
	original := connectDatabaseCommand
	t.Cleanup(func() { connectDatabaseCommand = original })
	connection := &databaseCommandConnectionFake{tx: databaseMigrationTransactionFake{}}
	connectDatabaseCommand = func(context.Context, string) (databaseCommandConnection, error) { return connection, nil }
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
		return config.Config{DBHost: "localhost", DBPort: 5432, DBName: "mycfc", DBSSLMode: "disable", MigrationDBUser: "mycfc_migrate", MigrationDBPassword: config.Secret("secret")}, nil
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
