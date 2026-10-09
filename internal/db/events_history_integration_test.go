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
	// A former programme must not expose ongoing or future events after the
	// transition, but the member keeps events from the time it was assigned.
	for _, row := range list(false, 10000, 0) {
		if fixture[row.ID] {
			t.Fatal("expired membership granted current event access")
		}
	}
	historicalID := uuid.New()
	if _, err = tx.Exec(ctx, `INSERT INTO events(id,title,starts_at,ends_at,created_by_id)
	  VALUES($1,'Previously authorised event',CURRENT_DATE-3 + time '10:00',CURRENT_DATE-3 + time '11:00',$2)`, historicalID, member); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO event_audiences(event_id,programme_id) VALUES($1,$2)`, historicalID, programme); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range list(true, 10000, 0) {
		if row.ID == historicalID {
			found = true
		}
	}
	if !found {
		t.Fatal("former member lost previously authorised event history")
	}
	if _, err = q.GetEventDetailForMember(ctx, dbgen.GetEventDetailForMemberParams{UserID: member, EventID: historicalID}); err != nil {
		t.Fatalf("former member lost historical event detail: %v", err)
	}
	if _, err = q.GetEventDetailForMember(ctx, dbgen.GetEventDetailForMemberParams{UserID: member, EventID: ids[10]}); err == nil {
		t.Fatal("former member retained future event detail")
	}
}

func TestFormerTeamHistoryRetainedButRevokedGuardianDenied(t *testing.T) {
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

	guardian, subject, author, season, oldTeam, newTeam := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{guardian, author} {
		if _, err = tx.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Team history fixture',$2,'hash','1980-01-01')`, id, id.String()+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	insertVerifiedDependentFixture(t, ctx, tx, guardian, subject, "Team history dependent", time.Now().AddDate(-14, 0, 0))
	var programme uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT id FROM programmes WHERE code='Leisure'`).Scan(&programme); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO seasons(id,code,name,starts_on,ends_on) VALUES($1,$2,'Historical teams',
	 (now() AT TIME ZONE 'Europe/Lisbon')::date-20,(now() AT TIME ZONE 'Europe/Lisbon')::date+20)`, season, "HIST_"+uuid.NewString()[:8]); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct {
		id   uuid.UUID
		code string
	}{{oldTeam, "OLD_" + uuid.NewString()[:8]}, {newTeam, "NEW_" + uuid.NewString()[:8]}} {
		if _, err = tx.Exec(ctx, `INSERT INTO teams(id,season_id,programme_id,code,name) VALUES($1,$2,$3,$4,'Historical team')`, entry.id, season, programme, entry.code); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO user_memberships(user_id,season_id,programme_id,team_id,starts_on,ends_on)
	 VALUES($1,$2,$3,$4,(now() AT TIME ZONE 'Europe/Lisbon')::date-10,(now() AT TIME ZONE 'Europe/Lisbon')::date-1),
	       ($1,$2,$3,$5,(now() AT TIME ZONE 'Europe/Lisbon')::date,NULL)`, subject, season, programme, oldTeam, newTeam); err != nil {
		t.Fatal(err)
	}
	past, future := uuid.New(), uuid.New()
	for _, event := range []struct {
		id  uuid.UUID
		day int
	}{{past, -3}, {future, 1}} {
		if _, err = tx.Exec(ctx, `INSERT INTO events(id,title,starts_at,ends_at,created_by_id)
	 VALUES($1,'Former team event',((now() AT TIME ZONE 'Europe/Lisbon')::date+$2::integer+time '10:00') AT TIME ZONE 'Europe/Lisbon',
	 ((now() AT TIME ZONE 'Europe/Lisbon')::date+$2::integer+time '11:00') AT TIME ZONE 'Europe/Lisbon',$3)`, event.id, event.day, author); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO event_team_audiences(event_id,team_id) VALUES($1,$2)`, event.id, oldTeam); err != nil {
			t.Fatal(err)
		}
	}
	q := dbgen.New(tx)
	contains := func(viewer uuid.UUID, historical bool, id uuid.UUID) bool {
		t.Helper()
		rows, e := q.ListEventsForMember(ctx, dbgen.ListEventsForMemberParams{
			UserID: viewer, Past: historical, AsOf: pgtype.Timestamptz{Time: time.Now(), Valid: true}, RowLimit: 100,
		})
		if e != nil {
			t.Fatal(e)
		}
		for _, row := range rows {
			if row.ID == id {
				return true
			}
		}
		return false
	}
	for _, viewer := range []uuid.UUID{subject, guardian} {
		if !contains(viewer, true, past) || contains(viewer, false, future) {
			t.Fatalf("former-team list scope incorrect for %s", viewer)
		}
		if _, err = q.GetEventDetailForMember(ctx, dbgen.GetEventDetailForMemberParams{UserID: viewer, EventID: past}); err != nil {
			t.Fatalf("historical detail for %s: %v", viewer, err)
		}
		if _, err = q.GetEventDetailForMember(ctx, dbgen.GetEventDetailForMemberParams{UserID: viewer, EventID: future}); err != pgx.ErrNoRows {
			t.Fatalf("former-team future detail for %s: %v", viewer, err)
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE guardian_authority_relationships SET state='EXPIRED',version=version+1,
	 conflict=false,conflict_actor_ref=NULL,updated_at=now() WHERE guardian_user_id=$1 AND subject_user_id=$2`, guardian, subject); err != nil {
		t.Fatal(err)
	}
	if contains(guardian, true, past) {
		t.Fatal("revoked guardian retained historical team event in list")
	}
	if _, err = q.GetEventDetailForMember(ctx, dbgen.GetEventDetailForMemberParams{UserID: guardian, EventID: past}); err != pgx.ErrNoRows {
		t.Fatalf("revoked guardian retained historical direct detail: %v", err)
	}
	if !contains(subject, true, past) {
		t.Fatal("guardian revocation removed subject's own historical event")
	}
}
