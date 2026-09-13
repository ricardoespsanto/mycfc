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
	"github.com/jackc/pgx/v5/pgtype"
)

func TestMemberEventHistoryPaginationAndViewDefaults(t *testing.T) {
	now := time.Now().UTC()
	store := &eventIndexStore{}
	for i := 0; i < eventsPageSize+1; i++ {
		store.memberItems = append(store.memberItems, dbgen.ListEventsForMemberRow{ID: uuid.New(), Title: "Evento anterior", EventType: "GENERAL", StartsAt: pgtype.Timestamptz{Time: now.Add(-time.Hour), Valid: true}, EndsAt: pgtype.Timestamptz{Time: now, Valid: true}})
	}
	for _, tc := range []struct {
		query string
		past  bool
		page  int
	}{{"?view=past&page=2", true, 2}, {"?view=unexpected&page=bad", false, 1}, {"?view=past&page=-1", true, 1}} {
		r := httptest.NewRequest(http.MethodGet, "/events"+tc.query, nil)
		r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: uuid.New()}))
		w := httptest.NewRecorder()
		(Events{Store: store, Now: func() time.Time { return now }}).Index(w, r)
		if w.Code != 200 || store.memberParams.Past != tc.past || store.memberParams.RowOffset != int32((tc.page-1)*eventsPageSize) || store.memberParams.RowLimit != eventsPageSize+1 || !store.memberParams.AsOf.Time.Equal(now) {
			t.Fatalf("status=%d params=%+v", w.Code, store.memberParams)
		}
		body := w.Body.String()
		if tc.past {
			if strings.Contains(body, `aria-label="Calendário de eventos`) || !strings.Contains(body, `href="/events?view=past&amp;page=`) || !strings.Contains(body, `?view=past&amp;page=`) {
				t.Fatal("past calendar/pagination/context incorrect")
			}
		} else if !strings.Contains(body, "Calendário de eventos") {
			t.Fatal("upcoming calendar missing")
		}
	}
}

func TestPastEventDetailRetainsAllowlistedPageAcrossSubjectSwitch(t *testing.T) {
	actor, dependent, eventID := uuid.New(), uuid.New(), uuid.New()
	store := &eventSubjectStore{authorized: map[uuid.UUID]bool{actor: true, dependent: true}, dependents: []dbgen.ListDependentsByGuardianRow{{ID: dependent, Name: "Dependente"}}, detail: dbgen.GetEventDetailForMemberRow{ID: eventID, Title: "Evento passado", EventType: "GENERAL", Status: "ACTIVE"}}
	r := httptest.NewRequest(http.MethodGet, "/events/"+eventID.String()+"?view=past&page=3&return=https://evil.test", nil)
	r.SetPathValue("id", eventID.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actor, Name: "Tutor"}))
	w := httptest.NewRecorder()
	(Events{Store: store}).Detail(w, r)
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, `href="/events?view=past&amp;page=3"`) || !strings.Contains(body, `name="view" value="past"`) || !strings.Contains(body, `name="page" value="3"`) || strings.Contains(body, "evil.test") {
		t.Fatalf("detail context=%d %s", w.Code, body)
	}
}
