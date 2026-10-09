package db

import (
	"strings"
	"testing"

	"github.com/cfcoimbra/mycfc/internal/releasecontract"
)

func TestDisposableReleaseRejectsUnsafeInputsBeforeConnection(t *testing.T) {
	oldVersion, oldCandidate := releasecontract.Version, releasecontract.Candidate
	t.Cleanup(func() { releasecontract.Version, releasecontract.Candidate = oldVersion, oldCandidate })
	releasecontract.Version, releasecontract.Candidate = "v0.0.0-rehearsal", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	valid := RoleCredentials{AppUsername: releasecontract.OldWebRole, AppPassword: "synthetic", MigrationUsername: "mycfc_migration", MigrationPassword: "synthetic"}
	for _, tc := range []struct {
		name, database string
		credentials    RoleCredentials
		want           string
	}{
		{"other database", "other", valid, "binding rejected"},
		{"wrong app", releasecontract.Database, RoleCredentials{AppUsername: "postgres"}, "binding rejected"},
		{"missing secret", releasecontract.Database, RoleCredentials{AppUsername: releasecontract.OldWebRole, MigrationUsername: "mycfc_migration"}, "credentials incomplete"},
		{"role collision", releasecontract.Database, RoleCredentials{AppUsername: releasecontract.OldWebRole, AppPassword: "synthetic", MigrationUsername: releasecontract.WebRole, MigrationPassword: "synthetic"}, "role configuration rejected"},
		{"bootstrap dollar delimiter", releasecontract.Database, RoleCredentials{AppUsername: releasecontract.OldWebRole, AppPassword: "synthetic$$only", MigrationUsername: "mycfc_migration", MigrationPassword: "synthetic"}, "credential incompatible"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := BootstrapDisposableRelease(t.Context(), nil, tc.database, tc.credentials)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("preflight error = %v", err)
			}
		})
	}
}
