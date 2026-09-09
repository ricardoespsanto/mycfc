//go:build integration

package privacyrequests

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestRelationalErasureInventoryMatchesSchema(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	rows, err := conn.Query(ctx, `
		SELECT tc.table_name||'.'||kcu.column_name
		FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu
		  ON kcu.constraint_schema=tc.constraint_schema AND kcu.constraint_name=tc.constraint_name
		JOIN information_schema.constraint_column_usage ccu
		  ON ccu.constraint_schema=tc.constraint_schema AND ccu.constraint_name=tc.constraint_name
		WHERE tc.constraint_schema='public' AND tc.constraint_type='FOREIGN KEY'
		  AND ccu.table_name='users' AND ccu.column_name='id'
		ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	if len(actual) != len(relationalUserReferenceInventory) {
		t.Fatalf("users(id) FK inventory drift: schema=%d manifest=%d", len(actual), len(relationalUserReferenceInventory))
	}
	for _, reference := range actual {
		if relationalUserReferenceInventory[reference] == "" {
			t.Errorf("users(id) FK has no reviewed treatment: %s", reference)
		}
	}
	for column, treatment := range relationalIndirectInventory {
		var exists bool
		if err = conn.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM information_schema.columns
			WHERE table_schema='public' AND table_name=split_part($1,'.',1) AND column_name=split_part($1,'.',2)
		)`, column).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists || treatment == "" {
			t.Errorf("indirect identifier inventory drift: %s exists=%v treatment=%q", column, exists, treatment)
		}
	}
}
