package db

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cfcoimbra/mycfc/internal/releasecontract"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type freshTestRow struct {
	value any
	err   error
}

func (r freshTestRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != 1 && len(dest) != 3 {
		return errors.New("unexpected scan shape")
	}
	if len(dest) == 3 {
		*dest[0].(*bool) = r.value.(bool)
		*dest[1].(*string) = releasecontract.Version
		*dest[2].(*string) = releasecontract.Candidate
		return nil
	}
	switch out := dest[0].(type) {
	case *bool:
		*out = r.value.(bool)
	case *string:
		*out = r.value.(string)
	default:
		return errors.New("unexpected scan type")
	}
	return nil
}

type freshTestConn struct {
	rows      []freshTestRow
	execErrAt int
	execs     int
}

func (c *freshTestConn) QueryRow(context.Context, string, ...any) pgx.Row {
	if len(c.rows) == 0 {
		return freshTestRow{err: errors.New("unexpected query")}
	}
	row := c.rows[0]
	c.rows = c.rows[1:]
	return row
}
func (c *freshTestConn) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	c.execs++
	if c.execs == c.execErrAt {
		return pgconn.CommandTag{}, errors.New("synthetic query failure")
	}
	return pgconn.NewCommandTag("OK"), nil
}
func (*freshTestConn) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("unexpected transaction")
}

func TestFreshReceiptRejectsUntrustedMetadata(t *testing.T) {
	oldVersion, oldCandidate := releasecontract.Version, releasecontract.Candidate
	releasecontract.Version, releasecontract.Candidate = "v2.0.2", strings.Repeat("a", 40)
	t.Cleanup(func() { releasecontract.Version, releasecontract.Candidate = oldVersion, oldCandidate })
	boom := errors.New("synthetic query failure")
	cases := []struct {
		name string
		rows []freshTestRow
		want string
	}{
		{"missing", []freshTestRow{{value: false}}, ""},
		{"lookup error", []freshTestRow{{err: boom}}, "synthetic query failure"},
		{"unowned", []freshTestRow{{value: true}, {value: false}}, "bootstrap-superuser-owned"},
		{"ownership error", []freshTestRow{{value: true}, {err: boom}}, "synthetic query failure"},
		{"wrong identity", []freshTestRow{{value: true}, {value: true}, {value: false}}, "identity mismatch"},
		{"receipt query error", []freshTestRow{{value: true}, {value: true}, {err: boom}}, "synthetic query failure"},
		{"wrong ledger", []freshTestRow{{value: true}, {value: true}, {value: true}, {value: "wrong"}}, "ledger mismatch"},
		{"ledger query error", []freshTestRow{{value: true}, {value: true}, {value: true}, {err: boom}}, "synthetic query failure"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn := &freshTestConn{rows: tc.rows}
			matched, err := FreshInitializationMatches(t.Context(), conn, "mycfc", releasecontract.Version, releasecontract.Candidate)
			if matched || (tc.want == "" && err != nil) || (tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want))) {
				t.Fatalf("matched=%t err=%v", matched, err)
			}
		})
	}
}

func TestFreshInitRefusesBeforeDatabaseChanges(t *testing.T) {
	roles := RoleCredentials{AppUsername: "fresh_web", AppPassword: "synthetic", MigrationUsername: "fresh_migration", MigrationPassword: "synthetic", MediaCleanupUsername: "fresh_media", MediaCleanupPassword: "synthetic", DataRetentionUsername: "fresh_retention", DataRetentionPassword: "synthetic"}
	cases := []struct {
		name      string
		roles     RoleCredentials
		rows      []freshTestRow
		execErrAt int
		want      string
		maxExec   int
	}{
		{"missing app password", RoleCredentials{AppUsername: "fresh_web", MigrationUsername: "fresh_migration", MigrationPassword: "synthetic"}, nil, 0, "passwords must not be empty", 0},
		{"missing maintenance", RoleCredentials{AppUsername: "fresh_web", AppPassword: "synthetic", MigrationUsername: "fresh_migration", MigrationPassword: "synthetic"}, nil, 0, "requires both maintenance", 0},
		{"lock error", roles, nil, 1, "lock fresh initialization", 1},
		{"empty query error", roles, []freshTestRow{{err: errors.New("synthetic inspect failure")}}, 0, "inspect empty database", 2},
		{"nonempty refusal", roles, []freshTestRow{{value: false}}, 0, "requires the named empty database", 2},
		{"bootstrap error", roles, []freshTestRow{{value: true}}, 2, "fresh bootstrap roles", 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn := &freshTestConn{rows: tc.rows, execErrAt: tc.execErrAt}
			err := FreshInitializeDatabase(t.Context(), conn, "mycfc", tc.roles)
			if err == nil || !strings.Contains(err.Error(), tc.want) || conn.execs > tc.maxExec {
				t.Fatalf("err=%v execs=%d", err, conn.execs)
			}
		})
	}
}
