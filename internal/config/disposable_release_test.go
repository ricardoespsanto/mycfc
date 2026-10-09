package config

import (
	"net/url"
	"testing"

	"github.com/cfcoimbra/mycfc/internal/releasecontract"
)

func TestDisposableWebRolePreservesExistingSecretAndRejectsPrivilegedURL(t *testing.T) {
	oldVersion, oldCandidate := releasecontract.Version, releasecontract.Candidate
	t.Cleanup(func() { releasecontract.Version, releasecontract.Candidate = oldVersion, oldCandidate })
	releasecontract.Version, releasecontract.Candidate = "v0.0.0-rehearsal", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	cfg := Config{AppVersion: releasecontract.Version, GITSHA: releasecontract.Candidate, DatabaseURL: Secret("postgres://mycfc_app:existing%3Apassword@localhost:5432/mycfc?sslmode=disable")}
	raw, err := cfg.ResolvedDatabaseURL()
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	password, _ := u.User.Password()
	if u.User.Username() != releasecontract.WebRole || password != "existing:password" {
		t.Fatal("role handoff changed the existing secret")
	}
	cfg.DatabaseURL = Secret("postgres://postgres:synthetic@localhost:5432/mycfc")
	if _, err = cfg.ResolvedDatabaseURL(); err == nil {
		t.Fatal("privileged candidate web URL accepted")
	}
	cfg.DatabaseURL = Secret("postgres://mycfc_app:synthetic@localhost:5432/mycfc?user=postgres")
	if _, err = cfg.ResolvedDatabaseURL(); err == nil {
		t.Fatal("query override escaped the distinct web-role boundary")
	}
	cfg.DatabaseURL = Secret("")
	cfg.DBName, cfg.DBHost, cfg.DBPort, cfg.DBSSLMode = releasecontract.Database, "localhost", 5432, "require"
	cfg.DBUser, cfg.DBPassword = releasecontract.OldWebRole, Secret("existing:password")
	raw, err = cfg.ResolvedDatabaseURL()
	if err != nil {
		t.Fatal(err)
	}
	u, err = url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	password, _ = u.User.Password()
	if u.User.Username() != releasecontract.WebRole || password != "existing:password" {
		t.Fatal("production component handoff changed principal or secret")
	}
}
