//go:build integration

package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
)

func TestPastEventResponsesPreserveStoredParticipation(t *testing.T) {
	ctx, pool := integrationPool(t)
	actor, eventID := uuid.New(), uuid.New()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Past response member',$2,'hash','1990-01-01')`, actor, actor.String()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM events WHERE id=$1`, eventID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, actor)
	})
	if _, err := pool.Exec(ctx, `INSERT INTO events(id,title,event_type,starts_at,ends_at,created_by_id) VALUES($1,'Past event response regression','GENERAL',$2,$3,$4)`, eventID, now.Add(-time.Hour), now, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO event_responses(event_id,user_id,status,responded_by_id,responded_at,checked_in_by_id,checked_in_at) VALUES($1,$2,'Going',$2,$3,$2,$4)`, eventID, actor, now.Add(-2*time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		t.Helper()
		var row string
		if err := pool.QueryRow(ctx, `SELECT row_to_json(r)::text FROM event_responses r WHERE event_id=$1 AND user_id=$2`, eventID, actor).Scan(&row); err != nil {
			t.Fatal(err)
		}
		return row
	}
	before := snapshot()
	for _, status := range []string{"Going", "NotGoing"} {
		for _, crossedBoundary := range []bool{false, true} {
			clockCalls := 0
			h := Events{Store: dbgen.New(pool), DB: pool, Location: time.UTC, Now: func() time.Time {
				clockCalls++
				if crossedBoundary && clockCalls == 1 {
					return now.Add(-time.Microsecond)
				}
				return now
			}}
			r := httptest.NewRequest(http.MethodPost, "/events/"+eventID.String()+"/responses", strings.NewReader("status="+status))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.SetPathValue("id", eventID.String())
			r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actor}))
			w := httptest.NewRecorder()
			h.Respond(w, r)
			if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "O evento terminou") {
				t.Fatalf("status=%s crossedBoundary=%t code=%d body=%s", status, crossedBoundary, w.Code, w.Body.String())
			}
			if got := snapshot(); got != before {
				t.Fatalf("historical participation changed: before=%s after=%s", before, got)
			}
		}
	}
}
