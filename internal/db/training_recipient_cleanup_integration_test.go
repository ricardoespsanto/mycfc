//go:build integration

package db

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type recipientCancelReset struct {
	*pgx.Conn
	cancel context.CancelFunc
}

func (c recipientCancelReset) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if sql == "RESET ROLE" {
		c.cancel()
	}
	return c.Conn.Exec(ctx, sql, args...)
}
func TestTrainingRecipientCleanupCancellation(t *testing.T) {
	t.Run("inspector wait is clipped and closed", func(t *testing.T) {
		f := newRecipientSchemaFixture(t)
		f.install79(t)
		blocker, err := f.low.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer blocker.Rollback(t.Context())
		recipientSchemaExec(t, blocker, `LOCK TABLE mycfc_meta.schema_migrations IN ACCESS EXCLUSIVE MODE`)
		low := f.connectLow(t)
		input := f.inspectionInput(RecipientInspectAdmission)
		input.InspectionTimeout = 100 * time.Millisecond
		start := time.Now()
		_, err = InspectTrainingRecipientDatabase(t.Context(), low, input)
		if err == nil || !low.IsClosed() || time.Since(start) > time.Second {
			t.Fatalf("cancelled inspection escaped cleanup: closed=%v elapsed=%v err=%v", low.IsClosed(), time.Since(start), err)
		}
		if err = blocker.Rollback(t.Context()); err != nil {
			t.Fatal(err)
		}
		replacement := f.connectLow(t)
		if _, err = InspectTrainingRecipientDatabase(t.Context(), replacement, f.inspectionInput(RecipientInspectAdmission)); err != nil {
			t.Fatal(err)
		}
		if replacement.PgConn().TxStatus() != 'I' || replacement.PgConn().IsBusy() {
			t.Fatal("successful inspection left transaction/busy state")
		}
	})
	t.Run("fresh reset cancellation refuses success and discards original", func(t *testing.T) {
		recipientStampedFixture(t)
		f := newRecipientSchemaFixture(t, true)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		result, err := WithGuardianPolicyWriter(ctx, f.root, PolicyWriterOptions{AdmissionTimeout: time.Second, CleanupTimeout: time.Second}, func(ctx context.Context, c *pgx.Conn) error {
			return FreshInitializeDatabase(ctx, recipientCancelReset{c, cancel}, "mycfc", f.roles)
		})
		if err == nil || !f.root.IsClosed() || result.OperationCompleted {
			t.Fatalf("nested failed cleanup reported reusable success: %+v closed=%v err=%v", result, f.root.IsClosed(), err)
		}
		low := f.connectLow(t)
		var receipt bool
		if err = low.QueryRow(t.Context(), `SELECT to_regclass('mycfc_fresh_init.receipt') IS NOT NULL`).Scan(&receipt); err != nil || receipt {
			t.Fatalf("failed fresh operation sealed success receipt %v %v", receipt, err)
		}
		// Baseline commit can precede cancelled RESET. This is partial initialization,
		// never a zero-write claim; subsequent stamped admission still needs receipt.
		var count int
		if err = low.QueryRow(t.Context(), `SELECT count(*) FROM mycfc_meta.schema_migrations`).Scan(&count); err != nil || count != len(EmbeddedMigrationInventory()) {
			t.Fatalf("actual committed initialization state missing %d %v", count, err)
		}
	})
}
