package main

import (
	"context"
	"errors"
	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/cfcoimbra/mycfc/internal/releasecontract"
	"testing"
)

type incompleteDisposableRow struct{}

func (incompleteDisposableRow) Scan(values ...any) error { *(values[0].(*bool)) = false; return nil }

func TestDisposableCommandsRequireExactPermanentCompletion(t *testing.T) {
	oldVersion, oldCandidate := releasecontract.Version, releasecontract.Candidate
	t.Cleanup(func() { releasecontract.Version, releasecontract.Candidate = oldVersion, oldCandidate })
	releasecontract.Version, releasecontract.Candidate = "v0.0.0-ci", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for _, tc := range []struct {
		name              string
		conn              *databaseCommandConnectionFake
		version, database string
		want              bool
	}{
		{"matching completion", &databaseCommandConnectionFake{}, releasecontract.Version, releasecontract.Database, true},
		{"missing completion", &databaseCommandConnectionFake{row: incompleteDisposableRow{}}, releasecontract.Version, releasecontract.Database, false},
		{"private driver error", &databaseCommandConnectionFake{row: databaseErrorRowFake{err: errors.New("secret-detail")}}, releasecontract.Version, releasecontract.Database, false},
		{"wrong version", &databaseCommandConnectionFake{}, "v1.25.9", releasecontract.Database, false},
		{"wrong database", &databaseCommandConnectionFake{}, releasecontract.Version, "other", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := requireDisposableCompletion(context.Background(), tc.conn, tc.database, tc.version, releasecontract.Candidate)
			if (err == nil) != tc.want {
				t.Fatalf("completion=%v", err)
			}
			if err != nil && err.Error() == "secret-detail" {
				t.Fatal("driver detail disclosed")
			}
		})
	}
	conn := &databaseCommandConnectionFake{}
	if err := bootstrapRelease(t.Context(), conn, "mycfc", db.RoleCredentials{}, "v1.25.9", releasecontract.Candidate); err == nil || conn.statements != 0 {
		t.Fatal("mismatched bootstrap touched DB")
	}
	if err := bootstrapRelease(t.Context(), conn, "mycfc", db.RoleCredentials{}, releasecontract.Version, releasecontract.Candidate); err == nil {
		t.Fatal("invalid reset credentials accepted")
	}
	if err := runServerCommand(t.Context(), []string{"disposable-release-contract"}); err != nil {
		t.Fatal(err)
	}
}
