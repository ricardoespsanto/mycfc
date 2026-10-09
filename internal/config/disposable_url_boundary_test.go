package config

import (
	"github.com/cfcoimbra/mycfc/internal/releasecontract"
	"testing"
)

func TestDisposableDatabaseURLRejectsRuntimeAndComponentOverrides(t *testing.T) {
	oldVersion, oldCandidate := releasecontract.Version, releasecontract.Candidate
	t.Cleanup(func() { releasecontract.Version, releasecontract.Candidate = oldVersion, oldCandidate })
	releasecontract.Version, releasecontract.Candidate = "v0.0.0-ci", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for _, tc := range []struct {
		name string
		cfg  Config
	}{
		{"invalid URL", Config{DatabaseURL: Secret("postgres://%zz")}},
		{"missing user", Config{DatabaseURL: Secret("postgres://localhost/mycfc")}},
		{"other URL database", Config{AppVersion: releasecontract.Version, GITSHA: releasecontract.Candidate, DatabaseURL: Secret("postgres://mycfc_app:synthetic@localhost/other")}},
		{"mismatched components", Config{DBName: "other", DBUser: releasecontract.OldWebRole}},
		{"privileged components", Config{AppVersion: releasecontract.Version, GITSHA: releasecontract.Candidate, DBName: "mycfc", DBUser: "postgres"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.cfg.ResolvedDatabaseURL(); err == nil {
				t.Fatal("unsafe URL accepted")
			}
		})
	}
}
