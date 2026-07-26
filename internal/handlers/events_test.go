package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
