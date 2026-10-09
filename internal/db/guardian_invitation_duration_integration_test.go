//go:build integration

package db

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestGuardianInvitationDurationForwardMigrationPreservesExistingRowsAndPrivileges(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	// The original CREATE segment is the exact c42ab16 parent-baseline issuer;
	// the appended forward segment uses CREATE OR REPLACE and is excluded.
	original := migrationFunctionSegment(t, baselineSchema, "CREATE FUNCTION guardian_authority_issue_invitation(", "CREATE FUNCTION guardian_authority_revoke_invitation(")
	if _, err = tx.Exec(ctx, strings.Replace(original, "CREATE FUNCTION", "CREATE OR REPLACE FUNCTION", 1)); err != nil {
		t.Fatal(err)
	}
	actor := uuid.New()
	if _, err = tx.Exec(ctx, `INSERT INTO users(id,name,email,email_verified_at,password_hash,date_of_birth) VALUES($1,'Forward administrator',$2,clock_timestamp(),'hash','1980-01-01')`, actor, "forward-"+actor.String()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, actor); err != nil {
		t.Fatal(err)
	}
	var before, after, aclBefore, aclAfter, definitionBefore, definitionAfter string
	if err = tx.QueryRow(ctx, `SELECT row_to_json(i)::text FROM guardian_authority_issue_invitation($1,'previous@example.test',decode(repeat('b',64),'hex')) i`, actor).Scan(&before); err != nil {
		t.Fatal(err)
	}
	metadata := `SELECT coalesce(proacl::text,''),pg_get_functiondef(oid) FROM pg_proc WHERE oid='guardian_authority_issue_invitation(uuid,citext,bytea)'::regprocedure`
	if err = tx.QueryRow(ctx, metadata).Scan(&aclBefore, &definitionBefore); err != nil {
		t.Fatal(err)
	}
	migration, err := migrationFiles.ReadFile("migrations/202610010002_guardian_invitation_fixed_duration.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT row_to_json(i)::text FROM guardian_authority_invitations i WHERE issued_by=$1`, actor).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, metadata).Scan(&aclAfter, &definitionAfter); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("migration modified a previously issued invitation")
	}
	if aclBefore != aclAfter {
		t.Fatal("migration changed issuer execute privileges")
	}
	if strings.Replace(definitionBefore, "interval '30 days'", "interval '720 hours'", 1) != definitionAfter {
		t.Fatal("issuer changed beyond the approved duration")
	}
}

// Exercise the installed issuer with a transaction-local clock substitution.
// Production continues to use database time; no clock parameter is exposed.
func TestGuardianInvitationFixedDurationAcrossLisbonDST(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SET LOCAL TIME ZONE 'Europe/Lisbon'`); err != nil {
		t.Fatal(err)
	}
	actor := uuid.New()
	if _, err = tx.Exec(ctx, `INSERT INTO users(id,name,email,email_verified_at,password_hash,date_of_birth) VALUES($1,'Duration administrator',$2,clock_timestamp(),'hash','1980-01-01');`, actor, "duration-"+actor.String()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, actor); err != nil {
		t.Fatal(err)
	}
	var definition string
	if err = tx.QueryRow(ctx, `SELECT pg_get_functiondef('guardian_authority_issue_invitation(uuid,citext,bytea)'::regprocedure)`).Scan(&definition); err != nil {
		t.Fatal(err)
	}
	if strings.Count(definition, "clock_timestamp()") != 1 {
		t.Fatal("issuer clock substitution must be exact")
	}
	for _, tc := range []struct {
		name, instant string
		calendarHours int
	}{
		{"spring", "2026-03-01 12:00:00+00", 719},
		{"autumn", "2026-10-01 12:00:00+01", 721},
		{"no_DST", "2026-01-01 12:00:00+00", 720},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixed := strings.Replace(definition, "clock_timestamp()", "TIMESTAMPTZ '"+tc.instant+"'", 1)
			if _, err := tx.Exec(ctx, fixed); err != nil {
				t.Fatal(err)
			}
			var issued, expires time.Time
			if err := tx.QueryRow(ctx, `SELECT issued_at,expires_at FROM guardian_authority_issue_invitation($1,$2,decode($3,'hex'))`, actor, "invited@example.test", strings.Repeat("a", 62)+map[string]string{"spring": "01", "autumn": "02", "no_DST": "03"}[tc.name]).Scan(&issued, &expires); err != nil {
				t.Fatal(err)
			}
			if got := expires.Sub(issued); got != 720*time.Hour {
				t.Fatalf("actual issuer duration=%s, want 720h", got)
			}
			var calendarHours int
			if err := tx.QueryRow(ctx, `SELECT extract(epoch FROM (($1::timestamptz+interval '30 days')-$1::timestamptz))/3600`, issued).Scan(&calendarHours); err != nil {
				t.Fatal(err)
			}
			if calendarHours != tc.calendarHours {
				t.Fatalf("Lisbon calendar control=%d, want %d", calendarHours, tc.calendarHours)
			}
		})
	}
}
