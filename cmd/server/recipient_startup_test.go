package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/cfcoimbra/mycfc/internal/config"
	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/cfcoimbra/mycfc/internal/releasecontract"
	"github.com/cfcoimbra/mycfc/internal/trainingcompat"
	"github.com/jackc/pgx/v5"
)

func TestRecipientCommandTargetUsesValidatedRoleAndEndpoint(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("APP_VERSION", "dev")
	t.Setenv("GIT_SHA", strings.Repeat("0", 40))
	t.Setenv("TRAINING_SCHEDULE_ADMISSION_FILE", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DB_HOST", "")
	t.Setenv("APP_DB_USER", "")
	t.Setenv("MIGRATION_DB_USER", "")
	t.Setenv("POSTGRES_USER", "")
	original := loadDatabaseCommandConfig
	t.Cleanup(func() { loadDatabaseCommandConfig = original })
	base := config.Config{AppEnv: "test", AppVersion: "dev", GITSHA: strings.Repeat("0", 40), DBHost: "127.0.0.1", DBPort: 55432, DBName: "mycfc_test", DBSSLMode: "disable", DBUser: "web", PostgresUser: "bootstrap", PostgresPassword: config.Secret("synthetic"), MigrationDBUser: "migration", MigrationDBPassword: config.Secret("synthetic")}
	for _, role := range []string{"bootstrap", "migration"} {
		t.Run(role, func(t *testing.T) {
			loadDatabaseCommandConfig = func(context.Context) (config.Config, error) { return base, nil }
			target, err := loadRecipientCommandTarget(t.Context(), role)
			if err != nil {
				t.Fatal(err)
			}
			if target.Connection.User != role || target.Database != "mycfc_test" || target.Connection.Host != "127.0.0.1" || target.Connection.Port != 55432 || target.BootstrapRole != "bootstrap" || target.Roles.AppUsername != "web" {
				t.Fatal("maintenance identity or target changed")
			}
		})
	}

	for _, tc := range []struct {
		name, endpoint, role, want string
		config                     func(config.Config) (config.Config, error)
	}{
		{"unavailable configuration", "", "migration", "configuration unavailable", func(c config.Config) (config.Config, error) { return c, errors.New("unavailable") }},
		{"unresolved endpoint", "", "migration", "endpoint unavailable", func(c config.Config) (config.Config, error) {
			c.DatabaseURL = config.Secret("postgres://bootstrap:synthetic@localhost/mycfc_test?hostaddr=127.0.0.2")
			return c, nil
		}},
		{"missing configured app identity", "postgres://migration:synthetic@localhost/mycfc_test", "migration", "identities required", func(c config.Config) (config.Config, error) { c.DBUser = ""; return c, nil }},
		{"invalid explicit port", "postgres://migration:synthetic@localhost:wrong/mycfc_test", "migration", "endpoint rejected", nil},
		{"wrong migration principal", "postgres://bootstrap:synthetic@localhost/mycfc_test", "migration", "migration authentication", nil},
		{"wrong bootstrap principal", "postgres://migration:synthetic@localhost/mycfc_test", "bootstrap", "bootstrap authentication", nil},
		{"unexpected remote host", "postgres://migration:synthetic@db.example.invalid/mycfc_test", "migration", "target rejected before authentication", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", tc.endpoint)
			loadDatabaseCommandConfig = func(context.Context) (config.Config, error) {
				if tc.config != nil {
					return tc.config(base)
				}
				return base, nil
			}
			if _, err := loadRecipientCommandTarget(t.Context(), tc.role); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestRecipientCLIRejectsUnsafePreparationBeforeDatabaseWork(t *testing.T) {
	t.Setenv("APP_ENV", "local")
	t.Setenv("APP_VERSION", "dev")
	t.Setenv("GIT_SHA", strings.Repeat("0", 40))
	t.Setenv("TRAINING_SCHEDULE_ADMISSION_FILE", "")
	t.Setenv("APP_DB_USER", "web")
	t.Setenv("MIGRATION_DB_USER", "migration")
	t.Setenv("DATABASE_URL", "postgres://bootstrap:synthetic@localhost/mycfc_test")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, "server command required"},
		{[]string{"prepare-local-recipient-floor", "extra"}, "accepts no arguments"},
		{[]string{"prepare-local-recipient-floor"}, "migration authentication"},
	} {
		if err := runServerCommand(t.Context(), tc.args); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("command %v: %v, want %q", tc.args, err, tc.want)
		}
	}
	t.Setenv("DATABASE_URL", "postgres://migration:synthetic@localhost/mycfc_test")
	t.Setenv("APP_VERSION", "v2.1.0")
	if err := runServerCommand(t.Context(), []string{"prepare-local-recipient-floor"}); err == nil || !strings.Contains(err.Error(), "wholly unstamped") {
		t.Fatalf("stamped runtime accepted as local preparation: %v", err)
	}
}

func TestPrecisionStatusReportsConfigurationAndReadFailures(t *testing.T) {
	load, connect := loadDatabaseCommandConfig, connectDatabaseCommand
	t.Cleanup(func() { loadDatabaseCommandConfig, connectDatabaseCommand = load, connect })
	failed := errors.New("fixture refused")
	for _, tc := range []struct {
		name, want       string
		cfg              config.Config
		loadErr, dialErr error
		connection       databaseCommandConnection
		stamped          bool
	}{
		{name: "configuration", want: "load precision status configuration", loadErr: failed},
		{name: "wrong stamped serving identity", want: "disposable web release identity mismatch", stamped: true},
		{name: "connection", want: "connect precision status database", cfg: config.Config{DatabaseURL: config.Secret("postgres://web:synthetic@localhost/mycfc_test")}, dialErr: failed},
		{name: "unprivileged read proof unavailable", want: "independently authenticated unprivileged session", cfg: config.Config{DatabaseURL: config.Secret("postgres://web:synthetic@localhost/mycfc_test")}, connection: freshReceiptErrorConn{&databaseCommandConnectionFake{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.stamped {
				version, candidate := releasecontract.Version, releasecontract.Candidate
				releasecontract.Version, releasecontract.Candidate = "v2.1.0", strings.Repeat("a", 40)
				t.Cleanup(func() { releasecontract.Version, releasecontract.Candidate = version, candidate })
			}
			loadDatabaseCommandConfig = func(context.Context) (config.Config, error) { return tc.cfg, tc.loadErr }
			connectDatabaseCommand = func(_ context.Context, raw string) (databaseCommandConnection, error) {
				if raw != "postgres://web:synthetic@localhost/mycfc_test" {
					t.Fatal("precision status did not resolve the serving endpoint")
				}
				return tc.connection, tc.dialErr
			}
			if err := runServerCommand(t.Context(), []string{"precision-status"}); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	if trainingFloorID(db.TrainingScheduleState{}) != nil {
		t.Fatal("an uncommitted floor acquired a synthetic ID")
	}
}

func TestPrecisionCapabilitiesCLIEmitsCompiledIdentity(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "capabilities")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	previous := os.Stdout
	os.Stdout = file
	t.Cleanup(func() { os.Stdout = previous })
	if err := runServerCommand(t.Context(), []string{"precision-capabilities"}); err != nil {
		t.Fatal(err)
	}
	os.Stdout = previous
	if _, err := file.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	var cap trainingcompat.Capabilities
	if err := json.NewDecoder(file).Decode(&cap); err != nil {
		t.Fatal(err)
	}
	if cap.Protocol != 1 || cap.Version != releasecontract.Version || cap.Candidate != releasecontract.Candidate ||
		cap.ReaderGeneration != trainingcompat.ReaderGeneration || cap.WriterGeneration != trainingcompat.WriterGeneration {
		t.Fatalf("unexpected compiled capability identity: %+v", cap)
	}
}

type configuredFreshReceipt struct {
	freshReceiptReadyConn
	config *pgx.ConnConfig
}

func (r configuredFreshReceipt) Config() *pgx.ConnConfig { return r.config }

func TestFreshInspectorRejectsUnboundTargetBeforeDial(t *testing.T) {
	cfg, err := pgx.ParseConfig("postgres://bootstrap:synthetic@localhost/mycfc")
	if err != nil {
		t.Fatal(err)
	}
	receipt := configuredFreshReceipt{freshReceiptReadyConn{&databaseCommandConnectionFake{}}, cfg}
	old := connectFreshInspector
	t.Cleanup(func() { connectFreshInspector = old })
	connectFreshInspector = func(context.Context, *pgx.ConnConfig) (databaseCommandConnection, error) {
		t.Fatal("an invalid fresh inspection target reached authentication")
		return nil, nil
	}
	for _, tc := range []struct {
		database, want string
		roles          db.RoleCredentials
	}{
		{"different", "database identity rejected", db.RoleCredentials{MigrationUsername: "migration"}},
		{"mycfc", "migration identity required", db.RoleCredentials{}},
		{"mycfc", "migration credentials required", db.RoleCredentials{MigrationUsername: "migration"}},
	} {
		if matches, err := inspectFreshInitialization(t.Context(), receipt, tc.roles, tc.database, "", ""); matches || err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("matches = %v, err = %v, want %q", matches, err, tc.want)
		}
	}
}

func TestMaintenanceCommandsRefuseInvalidConfiguredEndpointBeforeDial(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DB_HOST", "")
	oldLoad, oldConnect := loadDatabaseCommandConfig, connectDatabaseCommand
	t.Cleanup(func() { loadDatabaseCommandConfig, connectDatabaseCommand = oldLoad, oldConnect })
	loadDatabaseCommandConfig = func(context.Context) (config.Config, error) {
		return config.Config{
			DatabaseURL:     config.Secret("postgres://bootstrap:synthetic@localhost/mycfc?hostaddr=127.0.0.2"),
			PostgresUser:    "bootstrap",
			MigrationDBUser: "migration",
		}, nil
	}
	connectDatabaseCommand = func(context.Context, string) (databaseCommandConnection, error) {
		t.Fatal("ambiguous maintenance target reached authentication")
		return nil, nil
	}
	for _, command := range []string{"bootstrap-db", "migrate", "harden-db"} {
		t.Run(command, func(t *testing.T) {
			if err := runServerCommand(t.Context(), []string{command}); err == nil || !strings.Contains(err.Error(), "override rejected") {
				t.Fatalf("expected endpoint rejection, got %v", err)
			}
		})
	}
}

func TestRecipientInspectionRejectsFabricatedSession(t *testing.T) {
	_, err := inspectRecipientFromOriginal(t.Context(), &databaseCommandConnectionFake{}, "mycfc", db.RoleCredentials{MigrationUsername: "migration"}, db.RecipientInspectFinal)
	if err == nil || !strings.Contains(err.Error(), "requires original physical session") {
		t.Fatalf("expected physical session rejection, got %v", err)
	}
}

func TestDatabaseCommandConnectionRefusesMalformedPort(t *testing.T) {
	conn, err := connectDatabaseCommand(t.Context(), "postgres://fixture:synthetic@127.0.0.1:invalid/mycfc_test")
	if err == nil || conn != nil {
		t.Fatalf("malformed port produced a database connection: %v", err)
	}
}
