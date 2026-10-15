package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cfcoimbra/mycfc/internal/config"
	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/cfcoimbra/mycfc/internal/releasecontract"
	"github.com/jackc/pgx/v5"
)

func TestFreshInitCommandDispatchAndConfigurationBoundaries(t *testing.T) {
	if err := runServerCommand(t.Context(), []string{"fresh-init-db"}); err == nil || !strings.Contains(err.Error(), "--confirm-empty") {
		t.Fatalf("missing explicit confirmation accepted: %v", err)
	}
	if err := runServerCommand(t.Context(), []string{"fresh-init-db", "--confirm-empty", "extra"}); err == nil || !strings.Contains(err.Error(), "exactly --confirm-empty") {
		t.Fatalf("extra argument accepted: %v", err)
	}
	previousConnect, previousLoad := connectDatabaseCommand, loadDatabaseCommandConfig
	t.Cleanup(func() { connectDatabaseCommand, loadDatabaseCommandConfig = previousConnect, previousLoad })

	t.Setenv("DATABASE_URL", "postgres://%")
	if err := runServerCommand(t.Context(), []string{"fresh-init-db", "--confirm-empty"}); err == nil || !strings.Contains(err.Error(), "parse fresh initialization URL") {
		t.Fatalf("malformed connection accepted: %v", err)
	}

	t.Setenv("DATABASE_URL", "postgres://fresh_admin:synthetic@localhost:5432/mycfc?sslmode=disable")
	connectDatabaseCommand = func(context.Context, string) (databaseCommandConnection, error) {
		return nil, errors.New("synthetic connection failure")
	}
	if err := runServerCommand(t.Context(), []string{"fresh-init-db", "--confirm-empty"}); err == nil || !strings.Contains(err.Error(), "connect fresh initialization database") {
		t.Fatalf("connection error hidden: %v", err)
	}

	connectDatabaseCommand = func(context.Context, string) (databaseCommandConnection, error) {
		return &databaseCommandConnectionFake{}, nil
	}
	t.Setenv("APP_DB_USER", "fresh_web")
	t.Setenv("APP_DB_PASSWORD", "synthetic")
	t.Setenv("MIGRATION_DB_USER", "fresh_migration")
	t.Setenv("MIGRATION_DB_PASSWORD", "synthetic")
	t.Setenv("MEDIA_CLEANUP_DB_USER", "")
	t.Setenv("MEDIA_CLEANUP_DB_PASSWORD", "")
	t.Setenv("DATA_RETENTION_DB_USER", "")
	t.Setenv("DATA_RETENTION_DB_PASSWORD", "")
	if err := runServerCommand(t.Context(), []string{"fresh-init-db", "--confirm-empty"}); err == nil || !strings.Contains(err.Error(), "requires both maintenance") {
		t.Fatalf("missing maintenance credentials accepted: %v", err)
	}

	// Production remote-config selection must not fall back to the URL branch.
	t.Setenv("DATABASE_URL", "")
	for _, name := range []string{"DB_HOST", "DB_PORT", "DB_NAME", "DB_USER", "DB_PASSWORD"} {
		t.Setenv(name, "")
	}
	loadDatabaseCommandConfig = func(context.Context) (config.Config, error) {
		return config.Config{}, errors.New("synthetic configuration failure")
	}
	if err := runServerCommand(t.Context(), []string{"fresh-init-db", "--confirm-empty"}); err == nil || !strings.Contains(err.Error(), "load fresh initialization configuration") {
		t.Fatalf("configuration error hidden: %v", err)
	}
	loadDatabaseCommandConfig = func(context.Context) (config.Config, error) {
		return config.Config{
			DBHost: "postgres", DBPort: 5432, DBName: "mycfc", DBSSLMode: "disable",
			PostgresUser: "postgres", PostgresPassword: config.Secret("synthetic"),
			DBUser: "fresh_web", DBPassword: config.Secret("synthetic"),
			MigrationDBUser: "fresh_migration", MigrationDBPassword: config.Secret("synthetic"),
		}, nil
	}
	if err := runServerCommand(t.Context(), []string{"fresh-init-db", "--confirm-empty"}); err == nil || !strings.Contains(err.Error(), "requires both maintenance") {
		t.Fatalf("remote config missing maintenance accepted: %v", err)
	}
}

type freshReceiptErrorConn struct{ *databaseCommandConnectionFake }

func (freshReceiptErrorConn) QueryRow(context.Context, string, ...any) pgx.Row {
	return databaseErrorRowFake{err: errors.New("synthetic receipt query failure")}
}

type freshLedgerRow struct{}

func (freshLedgerRow) Scan(dest ...any) error {
	*dest[0].(*string) = strings.Join(db.EmbeddedMigrationInventory(), "\n")
	return nil
}

type freshReceiptReadyConn struct{ *databaseCommandConnectionFake }

func (freshReceiptReadyConn) QueryRow(_ context.Context, statement string, _ ...any) pgx.Row {
	if strings.Contains(statement, "string_agg(version") {
		return freshLedgerRow{}
	}
	return databaseMigrationRowFake{}
}

func TestFreshReceiptAllowsStampedMigrationAndRoleBootstrap(t *testing.T) {
	oldVersion, oldCandidate := releasecontract.Version, releasecontract.Candidate
	releasecontract.Version, releasecontract.Candidate = "v2.0.2", strings.Repeat("a", 40)
	t.Cleanup(func() { releasecontract.Version, releasecontract.Candidate = oldVersion, oldCandidate })
	conn := freshReceiptReadyConn{&databaseCommandConnectionFake{}}
	if err := requireDisposableCompletion(t.Context(), conn, "mycfc", "v2.0.2", strings.Repeat("a", 40)); err != nil {
		t.Fatalf("valid fresh receipt rejected by migration: %v", err)
	}
	roles := db.RoleCredentials{AppUsername: releasecontract.OldWebRole, AppPassword: "synthetic", MigrationUsername: "fresh_migration", MigrationPassword: "synthetic"}
	if err := bootstrapRelease(t.Context(), conn, "mycfc", roles, "v2.0.2", strings.Repeat("a", 40)); err != nil || conn.statements == 0 {
		t.Fatalf("valid fresh receipt rejected by role bootstrap: %v; statements=%d", err, conn.statements)
	}
}

func TestFreshReceiptInspectionFailureStopsMigrationAndBootstrap(t *testing.T) {
	oldVersion, oldCandidate := releasecontract.Version, releasecontract.Candidate
	releasecontract.Version, releasecontract.Candidate = "v2.0.2", strings.Repeat("a", 40)
	t.Cleanup(func() { releasecontract.Version, releasecontract.Candidate = oldVersion, oldCandidate })
	conn := freshReceiptErrorConn{&databaseCommandConnectionFake{}}
	for _, action := range []struct {
		name string
		call func() error
	}{
		{"migration", func() error {
			return requireDisposableCompletion(t.Context(), conn, "mycfc", "v2.0.2", strings.Repeat("a", 40))
		}},
		{"bootstrap", func() error {
			return bootstrapRelease(t.Context(), conn, "mycfc", db.RoleCredentials{}, "v2.0.2", strings.Repeat("a", 40))
		}},
	} {
		t.Run(action.name, func(t *testing.T) {
			if err := action.call(); err == nil || err.Error() != "fresh initialization receipt inspection failed" || conn.statements != 0 {
				t.Fatalf("err=%v statements=%d", err, conn.statements)
			}
		})
	}
}

func TestFreshInitRejectsInvalidEnvironmentDatabasePort(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DB_HOST", "postgres")
	t.Setenv("DB_PORT", "not-a-port")
	t.Setenv("DB_NAME", "mycfc")
	t.Setenv("DB_USER", "fresh_admin")
	t.Setenv("DB_PASSWORD", "synthetic")
	if err := runServerCommand(t.Context(), []string{"fresh-init-db", "--confirm-empty"}); err == nil || !strings.Contains(err.Error(), "parse fresh initialization URL") {
		t.Fatalf("invalid database port accepted: %v", err)
	}
}

func TestFreshInitRejectsWrongStampedRuntimeIdentity(t *testing.T) {
	previousVersion, previousCandidate := releasecontract.Version, releasecontract.Candidate
	releasecontract.Version, releasecontract.Candidate = "v2.0.2", strings.Repeat("a", 40)
	t.Cleanup(func() { releasecontract.Version, releasecontract.Candidate = previousVersion, previousCandidate })
	t.Setenv("DATABASE_URL", "postgres://fresh_admin:synthetic@localhost:5432/mycfc?sslmode=disable")
	t.Setenv("APP_VERSION", "wrong-version")
	t.Setenv("GIT_SHA", strings.Repeat("a", 40))
	if err := runServerCommand(t.Context(), []string{"fresh-init-db", "--confirm-empty"}); err == nil || !strings.Contains(err.Error(), "binary/runtime identity mismatch") {
		t.Fatalf("wrong stamped release accepted: %v", err)
	}
}
