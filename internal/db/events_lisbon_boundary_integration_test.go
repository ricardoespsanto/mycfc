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

// The spring clock change skips 01:00 local, but the membership day remains
// Europe/Lisbon's calendar date, not the event timestamp's UTC date.
func TestHistoricalEventAudienceUsesLisbonDateAcrossDST(t *testing.T) {
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
	if _, err = tx.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Lisbon boundary fixture',$2,'hash','1990-01-01')`, member, member.String()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO programmes(id,code,name_pt) VALUES($1,$2,'Lisbon boundary programme')`, programme, "LISBON_"+uuid.NewString()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO seasons(id,code,name,starts_on,ends_on) VALUES($1,$2,'Lisbon boundary season','2024-03-30','2024-04-01')`, season, "LISBON_"+uuid.NewString()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO user_memberships(user_id,programme_id,season_id,starts_on,ends_on) VALUES($1,$2,$3,'2024-03-31','2024-03-31')`, member, programme, season); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name, utcStart string
		visible        bool
	}{
		{"before Lisbon midnight", "2024-03-30T23:30:00Z", false},
		{"after Lisbon midnight before clock jump", "2024-03-31T00:30:00Z", true},
		{"after skipped local hour", "2024-03-31T01:30:00Z", true},
		{"after next Lisbon midnight", "2024-03-31T23:30:00Z", false},
	}
	ids := make(map[uuid.UUID]bool, len(cases))
	for _, tc := range cases {
		id := uuid.New()
		ids[id] = tc.visible
		start, err := time.Parse(time.RFC3339, tc.utcStart)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO events(id,title,starts_at,ends_at,created_by_id) VALUES($1,$2,$3,$4,$5)`, id, tc.name, start, start.Add(15*time.Minute), member); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO event_audiences(event_id,programme_id) VALUES($1,$2)`, id, programme); err != nil {
			t.Fatal(err)
		}
	}
	q := dbgen.New(tx)
	rows, err := q.ListEventsForMember(ctx, dbgen.ListEventsForMemberParams{UserID: member, Past: true, AsOf: pgtype.Timestamptz{Time: time.Now(), Valid: true}, RowLimit: 100})
	if err != nil {
		t.Fatal(err)
	}
	listed := make(map[uuid.UUID]bool)
	for _, row := range rows {
		listed[row.ID] = true
	}
	for id, want := range ids {
		if listed[id] != want {
			t.Fatalf("Lisbon-date list mismatch for event %s: listed=%v want=%v", id, listed[id], want)
		}
		_, err := q.GetEventDetailForMember(ctx, dbgen.GetEventDetailForMemberParams{UserID: member, EventID: id})
		if want && err != nil || !want && err != pgx.ErrNoRows {
			t.Fatalf("Lisbon-date direct detail mismatch for event %s: %v", id, err)
		}
	}
}
