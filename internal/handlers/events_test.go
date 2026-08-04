package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
)

func TestValidateEventRejectsInvalidTimesAndCapacity(t *testing.T) {
	events := Events{Location: time.UTC}
	request := httptest.NewRequest(http.MethodPost, "/admin/events", strings.NewReader("title=R&description=x&starts_at=2026-07-24T16%3A00&ends_at=2026-07-24T15%3A00&response_deadline=2026-07-24T17%3A00&capacity=0"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := request.ParseForm(); err != nil {
		t.Fatal(err)
	}
	form := events.validateEvent(request)
	for _, field := range []string{"title", "ends_at", "response_deadline", "capacity"} {
		if form.Errors[field] == "" {
			t.Errorf("expected error for %s", field)
		}
	}
}

func TestCanAuthorEventRespectsCoachScope(t *testing.T) {
	programmeID, otherProgrammeID, teamID, otherTeamID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	user := CurrentUser{CoachProgrammeIDs: map[uuid.UUID]bool{programmeID: true}, CoachTeamIDs: map[uuid.UUID]bool{teamID: true}}
	teams := map[uuid.UUID]uuid.UUID{teamID: otherProgrammeID, otherTeamID: otherProgrammeID}
	for _, tc := range []struct {
		name       string
		programmes []uuid.UUID
		teams      []uuid.UUID
		want       bool
	}{
		{"programme grant", []uuid.UUID{programmeID}, nil, true},
		{"assigned team", nil, []uuid.UUID{teamID}, true},
		{"unassigned programme", []uuid.UUID{otherProgrammeID}, nil, false},
		{"unassigned team", nil, []uuid.UUID{otherTeamID}, false},
		{"global event", nil, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := canAuthorEvent(user, tc.programmes, tc.teams, teams); got != tc.want {
				t.Fatalf("canAuthorEvent() = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestEventStatusText(t *testing.T) {
	for input, want := range map[string]string{"Going": "Vou", "NotGoing": "Não vou", "Waitlisted": "Em lista de espera", "anything": "Pendente"} {
		if got := eventStatusText(input); got != want {
			t.Errorf("eventStatusText(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestEventsPageNumber(t *testing.T) {
	for input, want := range map[string]int{"": 1, "0": 1, "invalid": 1, "2": 2, "10001": 1} {
		if got := eventsPageNumber(input); got != want {
			t.Errorf("eventsPageNumber(%q) = %d, want %d", input, got, want)
		}
	}
}

func TestEventResponsesPageURL(t *testing.T) {
	eventID := uuid.MustParse("018f3d5e-8f1d-7f5b-b308-e5391f03e7de")
	if got, want := eventResponsesPageURL(eventID, 2), "/events/018f3d5e-8f1d-7f5b-b308-e5391f03e7de?response_page=2"; got != want {
		t.Errorf("eventResponsesPageURL() = %q, want %q", got, want)
	}
}

func TestAdminDetailPagePaginatesResponses(t *testing.T) {
	eventID := uuid.MustParse("018f3d5e-8f1d-7f5b-b308-e5391f03e7de")
	responses := make([]dbgen.ListEventResponsesForAdminRow, eventResponsesPageSize+1)
	page := (Events{Location: time.UTC}).adminDetailPage(dbgen.Event{ID: eventID}, responses, 1)
	if got, want := len(page.Responses), eventResponsesPageSize; got != want {
		t.Errorf("response count = %d, want %d", got, want)
	}
	if got, want := page.ResponsesNextURL, "/admin/eventos/018f3d5e-8f1d-7f5b-b308-e5391f03e7de?response_page=2"; got != want {
		t.Errorf("next URL = %q, want %q", got, want)
	}
	if page.ResponsesPreviousURL != "" {
		t.Errorf("previous URL = %q, want empty", page.ResponsesPreviousURL)
	}

	page = (Events{Location: time.UTC}).adminDetailPage(dbgen.Event{ID: eventID}, responses[:1], 2)
	if got, want := page.ResponsesPreviousURL, "/admin/eventos/018f3d5e-8f1d-7f5b-b308-e5391f03e7de?response_page=1"; got != want {
		t.Errorf("previous URL = %q, want %q", got, want)
	}
	if page.ResponsesNextURL != "" {
		t.Errorf("next URL = %q, want empty", page.ResponsesNextURL)
	}
}
