package db

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cfcoimbra/mycfc/internal/releasecontract"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type freshTestRow struct {
	value     any
	err       error
	version   string
	candidate string
	digest    string
}

func (r freshTestRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != 1 && len(dest) != 4 {
		return errors.New("unexpected scan shape")
	}
	if len(dest) == 4 {
		*dest[0].(*bool) = r.value.(bool)
		version, candidate, digest := r.version, r.candidate, r.digest
		if version == "" {
			version = releasecontract.Version
		}
		if candidate == "" {
			candidate = releasecontract.Candidate
		}
		if digest == "" {
			digest = releasecontract.NormalFinalDigest
		}
		*dest[1].(*string) = version
		*dest[2].(*string) = candidate
		*dest[3].(*string) = digest
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

func (c *freshTestConn) QueryRow(_ context.Context, statement string, _ ...any) pgx.Row {
	if strings.Contains(statement, "fresh initialization unlock") {
		return freshTestRow{value: true}
	}
	if strings.Contains(statement, "a.backend_start") {
		return freshTestRow{value: true}
	}
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
func (c *freshTestConn) Begin(context.Context) (pgx.Tx, error) {
	return freshInspectionTestTx{conn: c}, nil
}

type freshInspectionTestTx struct {
	pgx.Tx
	conn *freshTestConn
}

func (tx freshInspectionTestTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (tx freshInspectionTestTx) Rollback(context.Context) error { return nil }
func (tx freshInspectionTestTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if strings.Contains(sql, "FROM training_schedule_control.status()") {
		return freshScheduleStatusRow{}
	}
	if strings.Contains(sql, "training recipient ") {
		return freshTestRow{value: true}
	}
	if strings.Contains(sql, "training schedule ") {
		return freshTestRow{value: true}
	}
	if strings.Contains(sql, "session_user::text") {
		return freshIdentityTestRow{}
	}
	if strings.Contains(sql, "c.oid::pg_catalog.text") {
		return freshTestRow{value: "42"}
	}
	return tx.conn.QueryRow(ctx, sql, args...)
}
func (tx freshInspectionTestTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	var ledger string
	if err := tx.conn.QueryRow(ctx, sql, args...).Scan(&ledger); err != nil {
		return nil, err
	}
	return &freshVersionsTestRows{versions: strings.Split(ledger, "\n")}, nil
}

type freshIdentityTestRow struct{}

func (freshIdentityTestRow) Scan(dest ...any) error {
	*dest[0].(*string) = "migration"
	*dest[1].(*string) = "1"
	*dest[2].(*int32) = 42
	*dest[3].(*time.Time) = time.Unix(1, 0)
	*dest[4].(*bool) = true
	return nil
}

type freshVersionsTestRows struct {
	pgx.Rows
	versions []string
	index    int
}

func (r *freshVersionsTestRows) Next() bool { return r.index < len(r.versions) }
func (r *freshVersionsTestRows) Scan(dest ...any) error {
	*dest[0].(*string) = r.versions[r.index]
	r.index++
	return nil
}
func (*freshVersionsTestRows) Close()     {}
func (*freshVersionsTestRows) Err() error { return nil }

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
		{"ledger query error", []freshTestRow{{value: true}, {value: true}, {value: true}, {err: boom}}, "fresh initialization ledger inspection rejected"},
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

func TestFreshReceiptAcceptsOnlyPinnedGenesisAcrossForwardMigration(t *testing.T) {
	oldVersion, oldCandidate := releasecontract.Version, releasecontract.Candidate
	releasecontract.Version, releasecontract.Candidate = "v2.0.7", strings.Repeat("b", 40)
	t.Cleanup(func() { releasecontract.Version, releasecontract.Candidate = oldVersion, oldCandidate })
	var original []string
	for _, version := range EmbeddedMigrationInventory() {
		if version != "202610010003_privacy_attachment_trigger_runtime" && version != TrainingScheduleCompatibilityMigration && version != TrainingRecipientRevisionMigration && version != TrainingPartialAuthoringMigration && version != TrainingWorkoutGraphIntegrityMigration && version != TrainingDatedPlacementsMigration && version != TrainingRhythmReferencesMigration {
			original = append(original, version)
		}
	}
	for _, tc := range []struct {
		name, ledger, candidate, digest string
		protected, want                 bool
	}{
		{"before migration", strings.Join(original, "\n"), releasecontract.FreshGenesisCandidate, releasecontract.FreshGenesisDigest, true, true},
		{"after migration", strings.Join(EmbeddedMigrationInventory(), "\n"), releasecontract.FreshGenesisCandidate, releasecontract.FreshGenesisDigest, true, true},
		{"forged candidate", strings.Join(original, "\n"), strings.Repeat("c", 40), releasecontract.FreshGenesisDigest, true, false},
		{"forged receipt digest", strings.Join(original, "\n"), releasecontract.FreshGenesisCandidate, releasecontract.FinalDigest, true, false},
		{"tampered ledger", "unexpected", releasecontract.FreshGenesisCandidate, releasecontract.FreshGenesisDigest, true, false},
		{"unowned receipt", strings.Join(original, "\n"), releasecontract.FreshGenesisCandidate, releasecontract.FreshGenesisDigest, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := &freshTestConn{rows: []freshTestRow{
				{value: true}, {value: tc.protected},
				{value: true, version: releasecontract.FreshGenesisVersion, candidate: tc.candidate, digest: tc.digest},
				{value: tc.ledger}, {value: true},
			}}
			matched, err := FreshInitializationMatches(t.Context(), conn, "mycfc", releasecontract.Version, releasecontract.Candidate)
			if matched != tc.want || tc.want && err != nil {
				t.Fatalf("pinned genesis match=%t want=%t error=%v", matched, tc.want, err)
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

// Synthetic status for interface-only receipt tests; actual catalog/role/floor
// proofs are exercised by the isolated PostgreSQL recipient suite.
type freshScheduleStatusRow struct{}

func (freshScheduleStatusRow) Scan(dest ...any) error {
	if len(dest) != 8 {
		return errors.New("unexpected status scan")
	}
	*dest[0].(*uuid.UUID) = uuid.MustParse("00000000-0000-0000-0000-000000000001")
	*dest[1].(*string) = TrainingScheduleContractP
	*dest[2].(*int64) = 0
	*dest[3].(*int32) = 1
	*dest[4].(**uuid.UUID) = nil
	*dest[5].(**string) = nil
	*dest[6].(*bool) = false
	*dest[7].(*bool) = false
	return nil
}

func TestFreshLedgerInspectionRequiresIndependentTransaction(t *testing.T) {
	for _, tc := range []struct {
		name      string
		inspector FreshInitializationInspector
	}{
		{name: "missing inspector"},
		{name: "failed transaction", inspector: bootstrapConnectionFake{err: errors.New("synthetic connection interruption")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The receipt connection must never substitute for the independent
			// inspector when no valid read-only transaction can be started.
			tx, digest, err := inspectFreshInitializationLedger(t.Context(), nil, tc.inspector, "migration", "mycfc")
			if tx != nil || digest != "" || !errors.Is(err, errFreshInspection) {
				t.Fatalf("missing independent transaction accepted: %v", err)
			}
		})
	}
}

func TestRecipientLocalBootstrapRejectsInvalidIdentityOrInspectionFailure(t *testing.T) {
	if phase, err := InspectTrainingRecipientLocalBootstrapTarget(t.Context(), nil, "mycfc", "same_role", "same_role"); phase != "" || err == nil || err.Error() != "local bootstrap inspection identity invalid" {
		t.Fatalf("shared publisher and migration identity accepted: phase=%q error=%v", phase, err)
	}
	conn := &freshTestConn{rows: []freshTestRow{{err: errors.New("synthetic catalog connection interruption")}}}
	if phase, err := InspectTrainingRecipientLocalBootstrapTarget(t.Context(), conn, "mycfc", "migration", "publisher"); phase != "" || err == nil || err.Error() != "training recipient empty target inspection failed" {
		t.Fatalf("failed catalog inspection accepted: phase=%q error=%v", phase, err)
	}
}
