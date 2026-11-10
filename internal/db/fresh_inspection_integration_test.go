//go:build integration

package db

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
)

func TestFreshLedgerInspectionBindsBackendAndBoundsInventory(t *testing.T) {
	fixture := newRecipientSchemaFixture(t)
	foreign := newRecipientSchemaFixture(t)
	for _, f := range []recipientSchemaFixture{fixture, foreign} {
		recipientSchemaExec(t, f.low, `CREATE TABLE mycfc_meta.schema_migrations(version text PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now()); INSERT INTO mycfc_meta.schema_migrations(version) VALUES('synthetic-marker')`)
	}
	// The foreign connection has the same database name, role and valid ledger
	// but its authenticated backend belongs to a different PostgreSQL server.
	tx, digest, err := inspectFreshInitializationLedger(t.Context(), fixture.root, foreign.low, fixture.roles.MigrationUsername, "mycfc")
	if tx != nil || digest != "" || !errors.Is(err, errFreshInspection) {
		t.Fatalf("foreign ledger inspection was accepted: %v", err)
	}

	tx, digest, err = inspectFreshInitializationLedger(t.Context(), fixture.root, fixture.low, fixture.roles.MigrationUsername, "mycfc")
	want := sha256.Sum256([]byte("synthetic-marker"))
	if err != nil || tx == nil || digest != hex.EncodeToString(want[:]) {
		t.Fatalf("matching backend and bounded inventory rejected: %v", err)
	}
	if err = tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ name, insert string }{
		{"oversized marker", `INSERT INTO mycfc_meta.schema_migrations(version) VALUES(repeat('a',257))`},
		{"too many markers", `INSERT INTO mycfc_meta.schema_migrations(version) SELECT 'synthetic-'||n FROM generate_series(1,10000) n`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recipientSchemaExec(t, fixture.low, tc.insert)
			tx, digest, err := inspectFreshInitializationLedger(t.Context(), fixture.root, fixture.low, fixture.roles.MigrationUsername, "mycfc")
			if tx != nil || digest != "" || !errors.Is(err, errFreshInspection) {
				t.Fatalf("unbounded ledger inspection was accepted: %v", err)
			}
			// Cleanup also proves the rejected inspection released its transaction.
			recipientSchemaExec(t, fixture.low, `DELETE FROM mycfc_meta.schema_migrations WHERE version<>'synthetic-marker'`)
		})
	}
}
