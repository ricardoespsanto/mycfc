package app

import (
	"context"
	"github.com/cfcoimbra/mycfc/internal/config"
	"github.com/cfcoimbra/mycfc/internal/releasecontract"
	"github.com/jackc/pgx/v5/pgxpool"
	"testing"
)

func TestStartupRejectsDisposableIdentityBeforeOpeningPool(t *testing.T) {
	oldLoad := loadApplicationConfig
	oldVersion, oldCandidate := releasecontract.Version, releasecontract.Candidate
	t.Cleanup(func() {
		loadApplicationConfig = oldLoad
		releasecontract.Version, releasecontract.Candidate = oldVersion, oldCandidate
	})
	releasecontract.Version, releasecontract.Candidate = "v0.0.0-ci", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	loadApplicationConfig = func(_ context.Context) (config.Config, error) {
		return config.Config{AppVersion: "v1.25.9", GITSHA: releasecontract.Candidate, DatabaseURL: config.Secret("postgres://mycfc_app:synthetic@localhost/mycfc")}, nil
	}
	if _, err := New(t.Context()); err == nil {
		t.Fatal("mismatched startup identity accepted")
	}
}

func TestStartupPingPropagatesUnavailableDatabase(t *testing.T) {
	pool, err := pgxpool.New(t.Context(), "postgres://synthetic@localhost/mycfc")
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()
	if err := pingApplicationPool(t.Context(), pool); err == nil {
		t.Fatal("closed database passed startup")
	}
}
