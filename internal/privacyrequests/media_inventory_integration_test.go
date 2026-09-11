//go:build integration

package privacyrequests

import (
	"context"
	"os"
	"sort"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestMediaSourceInventoryReconcilesEveryDatabaseObjectPointer(t *testing.T) {
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
	rows, err := conn.Query(ctx, `SELECT table_name,column_name FROM information_schema.columns
WHERE table_schema=current_schema() AND column_name LIKE '%object_key' ORDER BY table_name,column_name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var actual []string
	for rows.Next() {
		var table, column string
		if err = rows.Scan(&table, &column); err != nil {
			t.Fatal(err)
		}
		actual = append(actual, table+"."+column)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	expected := make([]string, 0, len(mediaSourceContracts))
	for _, contract := range mediaSourceContracts {
		expected = append(expected, contract.Table+"."+contract.ObjectKeyColumn)
		var columns int
		if err = conn.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=$1
AND column_name=ANY($2::text[])`, contract.Table, []string{contract.ReferenceColumn, contract.ObjectKeyColumn, contract.ContentTypeColumn, contract.SizeColumn, contract.IntentColumn}).Scan(&columns); err != nil {
			t.Fatal(err)
		}
		if columns != 5 {
			t.Fatalf("media contract columns drifted for %s: count=%d", contract.SourceKind, columns)
		}
	}
	sort.Strings(expected)
	if len(actual) != len(expected) {
		t.Fatalf("object pointer inventory drift: actual=%v expected=%v", actual, expected)
	}
	for index := range expected {
		if actual[index] != expected[index] {
			t.Fatalf("object pointer inventory drift: actual=%v expected=%v", actual, expected)
		}
	}
}
