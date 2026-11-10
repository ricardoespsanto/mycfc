package config

import (
	"github.com/jackc/pgx/v5"
	"net/url"
	"testing"
)

func TestMaintenanceIdentityPreservesExplicitPGXTarget(t *testing.T) {
	c := Config{DatabaseURL: Secret("postgres://app:old@127.0.0.1:55487/mycfc_ui_review?sslmode=require&application_name=review"), DBHost: "other", DBPort: 5432, DBName: "other", PostgresUser: "root", PostgresPassword: "bootstrap-secret", MigrationDBUser: "migrator", MigrationDBPassword: "migration-secret"}
	for name, method := range map[string]func() (string, error){"bootstrap": c.BootstrapDatabaseURL, "migration": c.MigrationDatabaseURL} {
		raw, err := method()
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := pgx.ParseConfig(raw)
		if err != nil {
			t.Fatal(err)
		}
		expectedUser, expectedPassword := "root", "bootstrap-secret"
		if name == "migration" {
			expectedUser, expectedPassword = "migrator", "migration-secret"
		}
		if parsed.Host != "127.0.0.1" || parsed.Port != 55487 || parsed.Database != "mycfc_ui_review" || parsed.User != expectedUser || parsed.Password != expectedPassword || parsed.TLSConfig == nil || parsed.RuntimeParams["application_name"] != "review" {
			t.Fatalf("%s target/identity changed", name)
		}
	}
}
func TestMaintenanceIdentityRejectsDecodedPGXOverrides(t *testing.T) {
	for _, key := range []string{"host", "hostaddr", "port", "user", "password", "dbname", "database", "service", "servicefile", "passfile", "options", "sslpassword", "%68ost", "%75ser"} {
		t.Run(key, func(t *testing.T) {
			c := Config{DatabaseURL: Secret("postgres://app:old@127.0.0.1:55487/mycfc_ui_review?" + key + "=" + url.QueryEscape("remote")), PostgresUser: "root", MigrationDBUser: "migrator"}
			if _, err := c.BootstrapDatabaseURL(); err == nil {
				t.Fatal("bootstrap accepted override")
			}
			if _, err := c.MigrationDatabaseURL(); err == nil {
				t.Fatal("migration accepted override")
			}
		})
	}
}

func TestDiscreteServingDatabaseCredentialsCannotEscapeTarget(t *testing.T) {
	for _, database := range []string{"mycfc_test", "mycfc_ui_review"} {
		c := Config{DBHost: "postgres", DBPort: 5432, DBName: database, DBSSLMode: "disable", DBUser: "user@host:5432/foreign?x=y", DBPassword: Secret("p@remote.invalid:5432/foreign?host=x")}
		raw, err := c.ResolvedDatabaseURL()
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := pgx.ParseConfig(raw)
		if err != nil {
			t.Fatal(err)
		}
		if parsed.Host != "postgres" || parsed.Port != 5432 || parsed.Database != database || parsed.User != c.DBUser || parsed.Password != c.DBPassword.Value() {
			t.Fatal("discrete serving credentials escaped target")
		}
	}
}
