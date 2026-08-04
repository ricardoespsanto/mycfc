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

func TestMigrationVersion(t *testing.T) {
	version := migrationVersion("migrations/202608040001_leaderboard_distance.sql")
	if version != "202608040001_leaderboard_distance" {
		t.Fatalf("version = %q", version)
	}
}
