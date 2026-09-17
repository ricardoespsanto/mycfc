package db

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type acceptanceRoleConnectionFake struct {
	txs       []*acceptanceRoleTxFake
	beginErrs []error
	begins    int
}

func (c *acceptanceRoleConnectionFake) Begin(context.Context) (pgx.Tx, error) {
	index := c.begins
	c.begins++
	if index < len(c.beginErrs) && c.beginErrs[index] != nil {
		return nil, c.beginErrs[index]
	}
	if index >= len(c.txs) {
		return nil, errors.New("unexpected transaction")
	}
	return c.txs[index], nil
}

type acceptanceRoleTxFake struct {
	pgx.Tx
	database      string
	exists        bool
	databaseErr   error
	existsErr     error
	execFailMatch string
	execErr       error
	commitErr     error
	statements    []string
	committed     bool
	rolledBack    bool
}

func (t *acceptanceRoleTxFake) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	if strings.Contains(sql, "current_database") {
		return acceptanceRoleRow{value: t.database, err: t.databaseErr}
	}
	return acceptanceRoleRow{value: t.exists, err: t.existsErr}
}

func (t *acceptanceRoleTxFake) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	t.statements = append(t.statements, sql)
	if t.execFailMatch != "" && strings.Contains(sql, t.execFailMatch) {
		return pgconn.CommandTag{}, t.execErr
	}
	return pgconn.NewCommandTag("SELECT 1"), nil
}

func (t *acceptanceRoleTxFake) Commit(context.Context) error {
	t.committed = t.commitErr == nil
	return t.commitErr
}

func (t *acceptanceRoleTxFake) Rollback(context.Context) error {
	t.rolledBack = true
	return nil
}

type acceptanceRoleRow struct {
	value any
	err   error
}

func (r acceptanceRoleRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	switch target := dest[0].(type) {
	case *string:
		*target = r.value.(string)
	case *bool:
		*target = r.value.(bool)
	}
	return nil
}

func TestConfigurePrivacyAcceptanceRoleAppliesLeastPrivilegeAndRotatesSessions(t *testing.T) {
	main := &acceptanceRoleTxFake{database: "mycfc"}
	cleanup := &acceptanceRoleTxFake{}
	conn := &acceptanceRoleConnectionFake{txs: []*acceptanceRoleTxFake{main, cleanup}}
	if err := ConfigurePrivacyAcceptanceRole(t.Context(), conn, "mycfc", strings.Repeat("s", 32), false); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(main.statements, "\n")
	for _, required := range []string{
		"CREATE ROLE mycfc_privacy_acceptance NOLOGIN",
		"REVOKE ALL ON DATABASE \"mycfc\" FROM mycfc_privacy_acceptance",
		"GRANT EXECUTE ON FUNCTION privacy_protected.acceptance_create",
		"ALTER ROLE \"mycfc_privacy_acceptance\" WITH LOGIN NOSUPERUSER",
		"pg_terminate_backend",
	} {
		if !strings.Contains(joined, required) {
			t.Errorf("role configuration missing %q", required)
		}
	}
	if !main.committed || !cleanup.committed || len(cleanup.statements) != 1 || !strings.Contains(cleanup.statements[0], "pg_terminate_backend") {
		t.Fatalf("main committed=%t cleanup committed=%t cleanup=%v", main.committed, cleanup.committed, cleanup.statements)
	}
}

func TestConfigurePrivacyAcceptanceRolePropagatesEachDatabaseBoundaryFailure(t *testing.T) {
	databaseErr := errors.New("database unavailable")
	tests := []struct {
		name string
		conn *acceptanceRoleConnectionFake
	}{
		{"begin", &acceptanceRoleConnectionFake{beginErrs: []error{databaseErr}}},
		{"database query", &acceptanceRoleConnectionFake{txs: []*acceptanceRoleTxFake{{database: "mycfc", databaseErr: databaseErr}}}},
		{"database mismatch", &acceptanceRoleConnectionFake{txs: []*acceptanceRoleTxFake{{database: "other"}}}},
		{"advisory lock", &acceptanceRoleConnectionFake{txs: []*acceptanceRoleTxFake{{database: "mycfc", execFailMatch: "pg_advisory_xact_lock", execErr: databaseErr}}}},
		{"role query", &acceptanceRoleConnectionFake{txs: []*acceptanceRoleTxFake{{database: "mycfc", existsErr: databaseErr}}}},
		{"role creation", &acceptanceRoleConnectionFake{txs: []*acceptanceRoleTxFake{{database: "mycfc", execFailMatch: "CREATE ROLE", execErr: databaseErr}}}},
		{"privilege statement", &acceptanceRoleConnectionFake{txs: []*acceptanceRoleTxFake{{database: "mycfc", execFailMatch: "REVOKE USAGE", execErr: databaseErr}}}},
		{"first termination", &acceptanceRoleConnectionFake{txs: []*acceptanceRoleTxFake{{database: "mycfc", execFailMatch: "pg_terminate_backend", execErr: databaseErr}}}},
		{"first commit", &acceptanceRoleConnectionFake{txs: []*acceptanceRoleTxFake{{database: "mycfc", commitErr: databaseErr}}}},
		{"cleanup begin", &acceptanceRoleConnectionFake{txs: []*acceptanceRoleTxFake{{database: "mycfc"}}, beginErrs: []error{nil, databaseErr}}},
		{"cleanup termination", &acceptanceRoleConnectionFake{txs: []*acceptanceRoleTxFake{{database: "mycfc"}, {execFailMatch: "pg_terminate_backend", execErr: databaseErr}}}},
		{"cleanup commit", &acceptanceRoleConnectionFake{txs: []*acceptanceRoleTxFake{{database: "mycfc"}, {commitErr: databaseErr}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ConfigurePrivacyAcceptanceRole(t.Context(), test.conn, "mycfc", strings.Repeat("s", 32), false); err == nil {
				t.Fatal("database boundary failure was ignored")
			}
		})
	}
}

func TestConfigurePrivacyAcceptanceRoleValidatesCredentialAndIdempotentRevocation(t *testing.T) {
	if err := ConfigurePrivacyAcceptanceRole(t.Context(), &acceptanceRoleConnectionFake{}, "invalid-name", strings.Repeat("s", 32), false); err == nil {
		t.Fatal("invalid database identifier accepted")
	}
	if err := ConfigurePrivacyAcceptanceRole(t.Context(), &acceptanceRoleConnectionFake{}, "mycfc", "short", false); err == nil {
		t.Fatal("short credential accepted")
	}
	main := &acceptanceRoleTxFake{database: "mycfc", exists: false}
	conn := &acceptanceRoleConnectionFake{txs: []*acceptanceRoleTxFake{main}}
	if err := ConfigurePrivacyAcceptanceRole(t.Context(), conn, "mycfc", "", true); err != nil || !main.committed {
		t.Fatalf("absent role revocation error=%v committed=%t", err, main.committed)
	}
}
