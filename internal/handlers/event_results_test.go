package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestEventResultsURLValidation(t *testing.T) {
	for _, path := range []string{"%C2%85", "%ff", "%C0%AF"} {
		if validEventResultsURL("https://fpcanoagem.pt/" + path) {
			t.Errorf("encoded control or malformed UTF-8 accepted: %s", path)
		}
	}
	for _, value := range []string{"https://fpcanoagem.pt", "https://www.fpcanoagem.pt/resultados/prova-2026.pdf", "https://fpcanoagem.pt/uploads/Resultados%20Finais.pdf"} {
		if !validEventResultsURL(value) {
			t.Errorf("valid link rejected: %q", value)
		}
	}
	for _, value := range []string{"", "http://fpcanoagem.pt/a", "javascript:alert(1)", "//fpcanoagem.pt/a", "https://fpcanoagem.pt.evil.test/a", "https://fpcanoagem.pt@evil.test/a", "https://a@fpcanoagem.pt/a", "https://fpcanoagem.pt:443/a", "https://sub.fpcanoagem.pt/a", "https://fpcanoagem.pt./a", "https://fpcanoagem.pt/a?token=secret", "https://fpcanoagem.pt/a?", "https://fpcanoagem.pt/a#secret", "https://fpcanoagem.pt/a\\b", "https://fpcanoagem.pt/a\nb", "https://fpcanoagem.pt/%0a", "https://fpcanoagem.pt/%0D", "https://fpcanoagem.pt/%7f", "https://fpcanoagem.pt/%5C", "https://fpcanoagem.pt/%252f", "https://fpcanoagem.pt/%2f", "https://fpcanoagem.pt/%23token", "https://fpcanoagem.pt/%3ftoken", "https://fpcanoagem.pt/%GG", "https://fpcanoagem.pt/%", "https://fpcanoagem.pt/" + strings.Repeat("a", 2048)} {
		if validEventResultsURL(value) || validatedEventResultsURL(value) != "" {
			t.Errorf("unsafe link accepted: %q", value)
		}
	}
}

type eventResultsStore struct {
	dbgen.Querier
	event             dbgen.GetEventResultsLinkRow
	reads, writes     int
	readErr, writeErr error
	count             int64
	saved             dbgen.UpdateEventResultsLinkParams
}

func (s *eventResultsStore) GetEventResultsLink(context.Context, uuid.UUID) (dbgen.GetEventResultsLinkRow, error) {
	s.reads++
	return s.event, s.readErr
}
func (s *eventResultsStore) UpdateEventResultsLink(_ context.Context, p dbgen.UpdateEventResultsLinkParams) (int64, error) {
	s.writes++
	s.saved = p
	return s.count, s.writeErr
}

func TestEventResultsActionAuthorizationValidationAndConflict(t *testing.T) {
	for _, tc := range []struct {
		name, eventType, link, version string
		admin                          bool
		count                          int64
		status                         int
		writes                         int
	}{
		{"member denied", "COMPETITION", "https://fpcanoagem.pt/a", "0", false, 1, 403, 0},
		{"general denied", "GENERAL", "https://fpcanoagem.pt/a", "0", true, 1, 409, 0},
		{"invalid preserved", "COMPETITION", "https://evil.test/a?secret=x", "0", true, 1, 422, 0},
		{"stale preserved", "COMPETITION", "https://fpcanoagem.pt/proposed.pdf", "3", true, 0, 409, 1},
		{"version required", "COMPETITION", "https://fpcanoagem.pt/a", "", true, 1, 422, 0},
		{"save", "COMPETITION", "https://fpcanoagem.pt/a", "4", true, 1, 303, 1},
		{"remove", "COMPETITION", "", "4", true, 1, 303, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := uuid.New()
			store := &eventResultsStore{event: dbgen.GetEventResultsLinkRow{ID: id, Title: "Prova terminada", EventType: tc.eventType, ResultsVersion: 4}, count: tc.count}
			r := httptest.NewRequest(http.MethodPost, "/admin/events/"+id.String()+"/results", strings.NewReader(url.Values{"results_url": {tc.link}, "expected_version": {tc.version}}.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.SetPathValue("id", id.String())
			r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: uuid.New(), IsAdmin: tc.admin}))
			w := httptest.NewRecorder()
			(Events{Store: store}).UpdateResultsLink(w, r)
			if w.Code != tc.status || store.writes != tc.writes {
				t.Fatalf("status/writes=%d/%d body=%s", w.Code, store.writes, w.Body.String())
			}
			if !tc.admin && store.reads != 0 {
				t.Fatal("unauthorized event lookup")
			}
			if tc.name == "invalid preserved" && (!strings.Contains(w.Body.String(), `value="https://evil.test/a?secret=x"`) || !strings.Contains(w.Body.String(), `aria-invalid`)) {
				t.Fatal("invalid draft not preserved accessibly")
			}
			if tc.name == "stale preserved" && (!strings.Contains(w.Body.String(), `value="3"`) || !strings.Contains(w.Body.String(), tc.link) || !strings.Contains(w.Body.String(), "Carregar a ligação atual")) {
				t.Fatal("conflict silently reset draft/version")
			}
			if tc.name == "remove" && store.saved.OfficialResultsUrl != nil {
				t.Fatal("removal did not clear URL")
			}
		})
	}
}

func TestEventResultsUnavailableResourcesAndStoreFailures(t *testing.T) {
	for _, tc := range []struct {
		name, method, body string
		invalidID          bool
		readErr, writeErr  error
		want, writes       int
	}{
		{name: "display current", method: http.MethodGet, want: 200},
		{name: "invalid identity", method: http.MethodGet, invalidID: true, want: 404},
		{name: "deleted competition", method: http.MethodGet, readErr: pgx.ErrNoRows, want: 404},
		{name: "read failure", method: http.MethodGet, readErr: errors.New("private-store-error"), want: 500},
		{name: "write failure", method: http.MethodPost, body: "expected_version=0&results_url=https%3A%2F%2Ffpcanoagem.pt%2Fa", writeErr: errors.New("private-store-error"), want: 500, writes: 1},
		{name: "malformed submission", method: http.MethodPost, body: "results_url=%zz", want: 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := uuid.New()
			link := "https://fpcanoagem.pt/resultados/final.pdf"
			store := &eventResultsStore{event: dbgen.GetEventResultsLinkRow{ID: id, Title: "Competition", EventType: "COMPETITION", OfficialResultsUrl: &link}, readErr: tc.readErr, writeErr: tc.writeErr}
			r := httptest.NewRequest(tc.method, "/admin/events/"+id.String()+"/results", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.SetPathValue("id", id.String())
			if tc.invalidID {
				r.SetPathValue("id", "invalid")
			}
			r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: uuid.New(), IsAdmin: true}))
			w := httptest.NewRecorder()
			h := Events{Store: store}
			if tc.method == http.MethodGet {
				h.ResultsLink(w, r)
			} else {
				h.UpdateResultsLink(w, r)
			}
			if w.Code != tc.want || store.writes != tc.writes {
				t.Fatalf("status=%d writes=%d", w.Code, store.writes)
			}
			if strings.Contains(w.Body.String(), "private-store-error") {
				t.Fatal("private storage failure exposed")
			}
			if tc.invalidID && store.reads != 0 {
				t.Fatal("invalid identifier reached storage")
			}
			if tc.name == "display current" && !strings.Contains(w.Body.String(), link) {
				t.Fatal("current link missing from edit form")
			}
		})
	}
}
