//go:build integration

package db

import (
	"context"
	"os"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestMemberEventHistoryBoundaryPaginationAndCurrentMembership(t *testing.T) {
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
	member, programme, season := uuid.New(), uuid.New(), uuid.New()
	if _, err = tx.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Event history member',$2,'hash','1990-01-01')`, member, member.String()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO programmes(id,code,name_pt) VALUES($1,$2,'Event history programme')`, programme, "HISTORY_"+programme.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO seasons(id,code,name,starts_on,ends_on) VALUES($1,$2,'Event history season',CURRENT_DATE-30,CURRENT_DATE+30)`, season, "HISTORY_"+season.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO user_memberships(user_id,programme_id,season_id,starts_on) VALUES($1,$2,$3,CURRENT_DATE-20)`, member, programme, season); err != nil {
		t.Fatal(err)
	}
	asOf := time.Now().UTC().Truncate(time.Microsecond)
	ids := make([]uuid.UUID, 11)
	fixture := map[uuid.UUID]bool{}
	for i := range ids {
		ids[i] = uuid.New()
		fixture[ids[i]] = true
		start, end := asOf.Add(-time.Duration(i+2)*time.Hour), asOf.Add(-time.Duration(i)*time.Hour)
		if i == 1 {
			start = asOf.Add(-2 * time.Hour)
		} // Equal start times need the deterministic ID tie-break.
		if i == 9 {
			start = asOf.Add(-time.Hour)
			end = asOf.Add(time.Hour)
		}
		if i == 10 {
			start = asOf.Add(time.Hour)
			end = asOf.Add(2 * time.Hour)
		}
		eventType := "GENERAL"
		if i%2 == 0 {
			eventType = "COMPETITION"
		}
		if _, err = tx.Exec(ctx, `INSERT INTO events(id,title,event_type,starts_at,ends_at,created_by_id) VALUES($1,'History event',$2,$3,$4,$5)`, ids[i], eventType, start, end, member); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO event_audiences(event_id,programme_id) VALUES($1,$2)`, ids[i], programme); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE events SET status='CANCELLED',cancelled_at=now(),cancelled_by_id=$2,cancellation_reason='Historic cancellation' WHERE id=$1`, ids[3], member); err != nil {
		t.Fatal(err)
	}
	q := dbgen.New(tx)
	list := func(past bool, limit, offset int32) []dbgen.ListEventsForMemberRow {
		t.Helper()
		rows, e := q.ListEventsForMember(ctx, dbgen.ListEventsForMemberParams{UserID: member, Past: past, AsOf: pgtype.Timestamptz{Time: asOf, Valid: true}, RowLimit: limit, RowOffset: offset})
		if e != nil {
			t.Fatal(e)
		}
		return rows
	}
	past := list(true, 10000, 0)
	seen := map[uuid.UUID]bool{}
	for i, row := range past {
		if fixture[row.ID] {
			seen[row.ID] = true
		}
		if i > 0 && (past[i-1].StartsAt.Time.Before(row.StartsAt.Time) || (past[i-1].StartsAt.Time.Equal(row.StartsAt.Time) && past[i-1].ID.String() > row.ID.String())) {
			t.Fatal("past ordering unstable")
		}
	}
	if len(seen) != 9 || !seen[ids[0]] || !seen[ids[3]] {
		t.Fatalf("past boundary/types/cancellation fixture count=%d", len(seen))
	}
	page := list(true, 6, 6)
	want := len(past) - 6
	if want > 6 {
		want = 6
	}
	if len(page) != want {
		t.Fatalf("page length=%d want=%d", len(page), want)
	}
	for i := range page {
		if page[i].ID != past[i+6].ID {
			t.Fatal("past pagination unstable")
		}
	}
	upcoming := list(false, 10000, 0)
	seen = map[uuid.UUID]bool{}
	for _, row := range upcoming {
		if fixture[row.ID] {
			seen[row.ID] = true
		}
	}
	if len(seen) != 2 || !seen[ids[9]] || !seen[ids[10]] {
		t.Fatalf("ongoing/upcoming boundary=%v", seen)
	}
	if _, err = tx.Exec(ctx, `UPDATE user_memberships SET ends_on=CURRENT_DATE-1 WHERE user_id=$1`, member); err != nil {
		t.Fatal(err)
	}
	for _, past := range []bool{true, false} {
		for _, row := range list(past, 10000, 0) {
			if fixture[row.ID] {
				t.Fatal("expired membership granted event history access")
			}
		}
	}
}
