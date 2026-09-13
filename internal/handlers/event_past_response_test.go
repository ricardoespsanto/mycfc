package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestPastEventDetailPreservesResponsesWithoutResponseControls(t *testing.T) {
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	for _, guardian := range []bool{false, true} {
		for _, status := range []string{"", "Going", "NotGoing", "Waitlisted"} {
			for _, deadline := range []pgtype.Timestamptz{{}, {Time: now.Add(-time.Hour), Valid: true}, {Time: now.Add(time.Hour), Valid: true}} {
				actor, subject, eventID := uuid.New(), uuid.New(), uuid.New()
				if !guardian {
					subject = actor
				}
				store := &eventSubjectStore{
					dependents: []dbgen.ListDependentsByGuardianRow{{ID: subject, Name: "Leonor"}},
					authorized: map[uuid.UUID]bool{actor: true, subject: true},
					detail:     dbgen.GetEventDetailForMemberRow{ID: eventID, Title: "Evento terminado", Status: "ACTIVE", StartsAt: pgtype.Timestamptz{Time: now.Add(-time.Hour), Valid: true}, EndsAt: pgtype.Timestamptz{Time: now, Valid: true}, ResponseStatus: status, ResponseDeadline: deadline},
				}
				h := Events{Store: store, Location: time.UTC, Now: func() time.Time { return now }}
				r := httptest.NewRequest(http.MethodGet, "/events/"+eventID.String()+"?subject_user_id="+subject.String(), nil)
				r.SetPathValue("id", eventID.String())
				r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actor, Name: "Marta"}))
				w := httptest.NewRecorder()
				h.Detail(w, r)
				body := w.Body.String()
				if w.Code != http.StatusOK || !strings.Contains(body, "Estado: "+eventStatus(status)) || !strings.Contains(body, "Este evento terminou.") || strings.Contains(body, `name="status"`) || strings.Contains(body, `method="post" action="/events/`+eventID.String()+`/responses"`) {
					t.Fatalf("guardian=%t status=%q deadline=%v code=%d body=%s", guardian, status, deadline, w.Code, body)
				}
				if guardian && (!strings.Contains(body, `id="event-subject-switcher"`) || store.detailParams.UserID != subject) {
					t.Fatal("historical guardian subject selection missing")
				}
			}
		}
	}
}

func TestEventResponseEndBoundaryAndLockRecheck(t *testing.T) {
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name                               string
		preflightEnd, lockedEnd, lockedNow time.Time
		want                               int
		queries                            int
	}{
		{"already ended", now.Add(-time.Nanosecond), now.Add(-time.Nanosecond), now, http.StatusConflict, 0},
		{"exact end", now, now, now, http.StatusConflict, 0},
		{"end changed before lock", now.Add(time.Hour), now, now, http.StatusConflict, 2},
		{"ended while waiting for lock", now.Add(time.Nanosecond), now.Add(time.Nanosecond), now.Add(time.Nanosecond), http.StatusConflict, 2},
		{"ongoing", now.Add(time.Hour), now.Add(time.Hour), now, http.StatusSeeOther, -1},
	} {
		for _, guardian := range []bool{false, true} {
			for _, status := range []string{"Going", "NotGoing"} {
				t.Run(tc.name+"/"+status+"/guardian="+map[bool]string{true: "yes", false: "no"}[guardian], func(t *testing.T) {
					actor, subject, eventID := uuid.New(), uuid.New(), uuid.New()
					if !guardian {
						subject = actor
					}
					tx := &eventTransactionFake{eventID: eventID, responseEvent: eventTransactionEvent{status: "ACTIVE", startsAt: now.Add(-time.Hour), endsAt: tc.lockedEnd}}
					store := &eventIndexStore{respondable: dbgen.GetRespondableEventRow{ID: eventID, Status: "ACTIVE", EndsAt: pgtype.Timestamptz{Time: tc.preflightEnd, Valid: true}}}
					calls := 0
					h := Events{Store: store, DB: eventMutationDB{tx: tx}, Location: time.UTC, Now: func() time.Time {
						calls++
						if calls == 1 {
							return now
						}
						return tc.lockedNow
					}}
					r := httptest.NewRequest(http.MethodPost, "/events/"+eventID.String()+"/responses", strings.NewReader(url.Values{"status": {status}, "subject_user_id": {subject.String()}}.Encode()))
					r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					r.SetPathValue("id", eventID.String())
					r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actor}))
					w := httptest.NewRecorder()
					h.Respond(w, r)
					if w.Code != tc.want {
						t.Fatalf("code=%d want=%d body=%s", w.Code, tc.want, w.Body.String())
					}
					if tc.want == http.StatusConflict {
						if tx.committed || len(tx.execCalls) != 0 || len(tx.queryCalls) != tc.queries || !strings.Contains(w.Body.String(), "O evento terminou") {
							t.Fatalf("ended event wrote response: committed=%t exec=%v query=%v body=%s", tx.committed, tx.execCalls, tx.queryCalls, w.Body.String())
						}
					} else if !tx.committed || len(tx.execCalls) != 1 || tx.execCalls[0].args[1] != subject || tx.execCalls[0].args[3] != actor {
						t.Fatalf("ongoing response missing: %v", tx.execCalls)
					}
				})
			}
		}
	}
}

func TestCurrentEventDetailRetainsResponseControlsAndDeadline(t *testing.T) {
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name            string
		start, deadline time.Time
		wantControls    bool
	}{
		{"future without deadline", now.Add(time.Hour), time.Time{}, true},
		{"ongoing without deadline", now.Add(-time.Hour), time.Time{}, true},
		{"future expired deadline", now.Add(time.Hour), now.Add(-time.Second), false},
		{"exact deadline stays open", now.Add(time.Hour), now, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actor, eventID := uuid.New(), uuid.New()
			store := &eventSubjectStore{authorized: map[uuid.UUID]bool{actor: true}, detail: dbgen.GetEventDetailForMemberRow{ID: eventID, Status: "ACTIVE", StartsAt: pgtype.Timestamptz{Time: tc.start, Valid: true}, EndsAt: pgtype.Timestamptz{Time: now.Add(2 * time.Hour), Valid: true}, ResponseDeadline: pgtype.Timestamptz{Time: tc.deadline, Valid: !tc.deadline.IsZero()}}}
			h := Events{Store: store, Location: time.UTC, Now: func() time.Time { return now }}
			r := httptest.NewRequest(http.MethodGet, "/events/"+eventID.String(), nil)
			r.SetPathValue("id", eventID.String())
			r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actor}))
			w := httptest.NewRecorder()
			h.Detail(w, r)
			body := w.Body.String()
			if w.Code != http.StatusOK || strings.Contains(body, `name="status"`) != tc.wantControls || strings.Contains(body, "Este evento terminou") || !strings.Contains(body, "A responder por") {
				t.Fatalf("code=%d body=%s", w.Code, body)
			}
		})
	}
}
