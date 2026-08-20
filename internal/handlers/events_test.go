package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

func TestValidateCompetitionEventDocument(t *testing.T) {
	events := Events{Location: time.UTC, Now: func() time.Time { return time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC) }}
	request := httptest.NewRequest(http.MethodPost, "/admin/events", strings.NewReader("title=Prova+regional&event_type=COMPETITION&starts_at=2026-08-06T10%3A00&ends_at=2026-08-06T12%3A00&document_title=Caderno&document_url=https%3A%2F%2Fexample.test%2Fcaderno.pdf&document_source=FPC&document_reviewed_on=2026-08-05"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := request.ParseForm(); err != nil {
		t.Fatal(err)
	}
	form := events.validateEvent(request)
	if !form.Errors.Empty() || form.EventType != "COMPETITION" || form.DocumentTitle != "Caderno" {
		t.Fatalf("form = %+v", form)
	}

	request = httptest.NewRequest(http.MethodPost, "/admin/events", strings.NewReader("title=Convivio&event_type=GENERAL&starts_at=2026-08-06T10%3A00&ends_at=2026-08-06T12%3A00&document_title=Caderno"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_ = request.ParseForm()
	form = events.validateEvent(request)
	if form.Errors["document"] == "" {
		t.Fatal("general event accepted a competition document")
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

func TestSameUUIDSetIgnoresOrderAndRejectsDifferences(t *testing.T) {
	one, two, three := uuid.New(), uuid.New(), uuid.New()
	if !sameUUIDSet([]uuid.UUID{one, two}, []uuid.UUID{two, one}) {
		t.Fatal("equal audience sets with different order were rejected")
	}
	if sameUUIDSet([]uuid.UUID{one, two}, []uuid.UUID{one, three}) || sameUUIDSet([]uuid.UUID{one}, []uuid.UUID{one, two}) {
		t.Fatal("different audience sets were accepted")
	}
}

func TestEventCapacityParsesOptionalPositiveInteger(t *testing.T) {
	if value, ok := eventCapacity(""); !ok || value != nil {
		t.Fatalf("empty capacity = %v, %t", value, ok)
	}
	if value, ok := eventCapacity("12"); !ok || value == nil || *value != 12 {
		t.Fatalf("capacity = %v, %t", value, ok)
	}
	for _, value := range []string{"0", "-1", "1.5", "invalid"} {
		if _, ok := eventCapacity(value); ok {
			t.Errorf("eventCapacity(%q) accepted", value)
		}
	}
}

func TestEventCreateTaskRendersSafeFormValuesAfterValidationFailure(t *testing.T) {
	programmeID, teamID := uuid.New(), uuid.New()
	h := Events{Store: eventCreateStore{programmeID: programmeID, teamID: teamID}, Location: time.UTC}
	r := httptest.NewRequest(http.MethodPost, "/admin/events", nil)
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: uuid.New(), IsAdmin: true, Name: "Direção"}))
	w := httptest.NewRecorder()

	h.renderCreate(w, r, http.StatusUnprocessableEntity, eventForm{Title: "Regata regional", EventType: "COMPETITION", ProgrammeIDs: []uuid.UUID{programmeID}, TeamIDs: []uuid.UUID{teamID}, Errors: validation.FieldErrors{"ends_at": "O fim tem de ser posterior ao início."}})

	for _, want := range []string{"Regata regional", "COMPETITION", "O fim tem de ser posterior ao início.", programmeID.String(), teamID.String()} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("body does not contain %q: %s", want, w.Body.String())
		}
	}
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", w.Code)
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
	page := (Events{Location: time.UTC}).adminDetailPage(dbgen.GetEventDetailForAdminRow{ID: eventID}, responses, 1)
	if got, want := len(page.Responses), eventResponsesPageSize; got != want {
		t.Errorf("response count = %d, want %d", got, want)
	}
	if got, want := page.ResponsesNextURL, "/admin/eventos/018f3d5e-8f1d-7f5b-b308-e5391f03e7de?response_page=2"; got != want {
		t.Errorf("next URL = %q, want %q", got, want)
	}
	if page.ResponsesPreviousURL != "" {
		t.Errorf("previous URL = %q, want empty", page.ResponsesPreviousURL)
	}

	page = (Events{Location: time.UTC}).adminDetailPage(dbgen.GetEventDetailForAdminRow{ID: eventID}, responses[:1], 2)
	if got, want := page.ResponsesPreviousURL, "/admin/eventos/018f3d5e-8f1d-7f5b-b308-e5391f03e7de?response_page=1"; got != want {
		t.Errorf("previous URL = %q, want %q", got, want)
	}
	if page.ResponsesNextURL != "" {
		t.Errorf("next URL = %q, want empty", page.ResponsesNextURL)
	}
}

func TestResolveEventSubjectDefaultsToSelfThenAuthorizedDependent(t *testing.T) {
	actorID, dependentID, eventID := uuid.New(), uuid.New(), uuid.New()
	store := &eventSubjectStore{
		dependents: []dbgen.ListDependentsByGuardianRow{{ID: dependentID, Name: "Leonor"}},
		authorized: map[uuid.UUID]bool{actorID: true, dependentID: true},
	}
	h := Events{Store: store}
	actor := CurrentUser{ID: actorID, Name: "Marta"}

	selected, subjects, err := h.resolveEventSubject(context.Background(), actor, eventID, "")
	if err != nil || selected.ID != actorID || len(subjects) != 2 {
		t.Fatalf("self selection = %#v, subjects = %#v, err = %v", selected, subjects, err)
	}

	store.authorized[actorID] = false
	selected, subjects, err = h.resolveEventSubject(context.Background(), actor, eventID, "")
	if err != nil || selected.ID != dependentID || len(subjects) != 1 {
		t.Fatalf("dependent fallback = %#v, subjects = %#v, err = %v", selected, subjects, err)
	}
	page := h.memberDetailPage(dbgen.GetEventDetailForMemberRow{ID: eventID}, selected, subjects, actorID)
	if len(page.Subjects) != 1 || page.Subjects[0].Self || page.SelectedSubject.ID != dependentID.String() {
		t.Fatalf("ineligible self leaked into page subjects: %#v", page.Subjects)
	}
}

func TestResolveEventSubjectFailsClosedForMalformedOrForeignSubject(t *testing.T) {
	actorID, dependentID, eventID := uuid.New(), uuid.New(), uuid.New()
	h := Events{Store: &eventSubjectStore{
		dependents: []dbgen.ListDependentsByGuardianRow{{ID: dependentID, Name: "Leonor"}},
		authorized: map[uuid.UUID]bool{actorID: true, dependentID: true},
	}}
	actor := CurrentUser{ID: actorID, Name: "Marta"}
	for _, requested := range []string{"not-a-uuid", uuid.NewString()} {
		if _, _, err := h.resolveEventSubject(context.Background(), actor, eventID, requested); !errors.Is(err, errEventSubjectNotFound) {
			t.Errorf("requested %q error = %v", requested, err)
		}
	}
}

func TestEventSubjectURLPreservesOnlyDependentContext(t *testing.T) {
	actorID, dependentID, eventID := uuid.New(), uuid.New(), uuid.New()
	if got, want := eventSubjectURL(eventID, actorID, actorID), "/events/"+eventID.String(); got != want {
		t.Fatalf("self URL = %q, want %q", got, want)
	}
	if got, want := eventSubjectURL(eventID, dependentID, actorID), "/events/"+eventID.String()+"?subject_user_id="+dependentID.String(); got != want {
		t.Fatalf("dependent URL = %q, want %q", got, want)
	}
}

func TestEventDetailUsesSelectedAuthorizedSubjectForStatusAndContext(t *testing.T) {
	actorID, dependentID, eventID := uuid.New(), uuid.New(), uuid.New()
	store := &eventSubjectStore{
		dependents: []dbgen.ListDependentsByGuardianRow{{ID: dependentID, Name: "Leonor"}},
		authorized: map[uuid.UUID]bool{actorID: true, dependentID: true},
		detail:     dbgen.GetEventDetailForMemberRow{ID: eventID, Title: "Convívio", Status: "ACTIVE", ResponseStatus: "Going"},
	}
	h := Events{Store: store, Location: time.UTC}
	r := httptest.NewRequest(http.MethodGet, "/events/"+eventID.String()+"?subject_user_id="+dependentID.String(), nil)
	r.SetPathValue("id", eventID.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actorID, Name: "Marta"}))
	w := httptest.NewRecorder()

	h.Detail(w, r)

	if w.Code != http.StatusOK || store.detailParams.UserID != dependentID || !strings.Contains(w.Body.String(), "A atuar sobre") || !strings.Contains(w.Body.String(), "Leonor") || !strings.Contains(w.Body.String(), "Vou") {
		t.Fatalf("response = %d, params = %#v, body = %s", w.Code, store.detailParams, w.Body.String())
	}
}

func TestMemberEventDetailModelsAuthorizedSubjectsAndSelectedStatus(t *testing.T) {
	actorID, selectedID, otherID := uuid.New(), uuid.New(), uuid.New()
	page := (Events{Location: time.UTC}).memberDetailPage(
		dbgen.GetEventDetailForMemberRow{ID: uuid.New(), ResponseStatus: "Going"},
		eventSubject{ID: selectedID, Name: "Leonor"},
		[]eventSubject{{ID: actorID, Name: "Marta"}, {ID: otherID, Name: "Gonçalo"}, {ID: selectedID, Name: "Leonor"}},
		actorID,
	)
	if page.Status != "Vou" || len(page.Subjects) != 3 || page.Subjects[0].ID != actorID.String() || !page.Subjects[0].Self || page.Subjects[0].Selected || page.Subjects[1].ID != otherID.String() || !page.Subjects[2].Selected || page.SelectedSubject.ID != selectedID.String() {
		t.Fatalf("page = %#v", page)
	}
}

type eventSubjectStore struct {
	dbgen.Querier
	dependents   []dbgen.ListDependentsByGuardianRow
	authorized   map[uuid.UUID]bool
	detail       dbgen.GetEventDetailForMemberRow
	detailParams dbgen.GetEventDetailForMemberParams
}

type eventCreateStore struct {
	dbgen.Querier
	programmeID uuid.UUID
	teamID      uuid.UUID
}

func (s eventCreateStore) ListProgrammes(context.Context) ([]dbgen.Programme, error) {
	return []dbgen.Programme{{ID: s.programmeID, NamePt: "Competição"}}, nil
}

func (s eventCreateStore) ListTeamsForEventAuthoring(context.Context) ([]dbgen.ListTeamsForEventAuthoringRow, error) {
	return []dbgen.ListTeamsForEventAuthoringRow{{ID: s.teamID, ProgrammeID: s.programmeID, Name: "Juniores"}}, nil
}

func (s *eventSubjectStore) ListDependentsByGuardian(context.Context, dbgen.ListDependentsByGuardianParams) ([]dbgen.ListDependentsByGuardianRow, error) {
	return s.dependents, nil
}

func (s *eventSubjectStore) GetRespondableEvent(_ context.Context, params dbgen.GetRespondableEventParams) (dbgen.Event, error) {
	if !s.authorized[params.SubjectUserID] {
		return dbgen.Event{}, pgx.ErrNoRows
	}
	return dbgen.Event{ID: params.EventID, Status: "ACTIVE"}, nil
}

func (s *eventSubjectStore) GetEventDetailForMember(_ context.Context, params dbgen.GetEventDetailForMemberParams) (dbgen.GetEventDetailForMemberRow, error) {
	s.detailParams = params
	return s.detail, nil
}

func (s *eventSubjectStore) ListCompetitionDocumentsForEvent(context.Context, *uuid.UUID) ([]dbgen.ListCompetitionDocumentsForEventRow, error) {
	return nil, nil
}
