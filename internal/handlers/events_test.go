package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
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

func TestEventDisplayHelpersDescribeOptionalDeadlineCapacityAndLifecycle(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	deadline := now.Add(time.Hour)
	capacity := int32(12)
	h := Events{Location: time.UTC, Now: func() time.Time { return now }}
	if h.deadline(pgtype.Timestamptz{}) != "Sem limite de resposta" || h.capacity(nil) != "" {
		t.Fatal("optional event values should retain their safe empty-state labels")
	}
	if got := h.deadline(pgtype.Timestamptz{Time: deadline, Valid: true}); got != "Responda até 20/08/2026 13:00" {
		t.Fatalf("deadline=%q", got)
	}
	if got := h.capacity(&capacity); got != " · Lotação: 12" {
		t.Fatalf("capacity=%q", got)
	}
	for status, want := range map[string]string{"Going": "Vou", "NotGoing": "Não vou", "OTHER": "Pendente"} {
		if got := eventStatus(status); got != want {
			t.Errorf("status %q=%q want=%q", status, got, want)
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

func TestEventIndexRendersScopedManagementItemsAndPagination(t *testing.T) {
	programmeID, teamID := uuid.New(), uuid.New()
	startsAt := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	items := make([]dbgen.ListEventsForAdminRow, eventsPageSize+1)
	for i := range items {
		items[i] = dbgen.ListEventsForAdminRow{ID: uuid.New(), Title: "Treino", EventType: "GENERAL", StartsAt: pgtype.Timestamptz{Time: startsAt.AddDate(0, 0, i), Valid: true}, EndsAt: pgtype.Timestamptz{Time: startsAt.AddDate(0, 0, i).Add(time.Hour), Valid: true}, Status: "ACTIVE", GoingCount: int64(i)}
	}
	h := Events{Store: &eventIndexStore{items: items, programmes: []dbgen.Programme{{ID: programmeID, NamePt: "Competição"}}, teams: []dbgen.ListTeamsForEventAuthoringRow{{ID: teamID, ProgrammeID: programmeID, Name: "Senior"}}}, Location: time.UTC, Now: func() time.Time { return startsAt }}
	r := httptest.NewRequest(http.MethodGet, "/admin/eventos?page=2", nil)
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: uuid.New(), Name: "Ana", IsAdmin: true, EmailVerified: true}))
	w := httptest.NewRecorder()

	h.Index(w, r)

	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Treino") || !strings.Contains(w.Body.String(), "Competição") || !strings.Contains(w.Body.String(), "/admin/eventos?page=1") || !strings.Contains(w.Body.String(), "/admin/eventos?page=3") {
		t.Fatalf("response = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestEventIndexRendersCoachAndMemberScopedViews(t *testing.T) {
	userID, programmeID := uuid.New(), uuid.New()
	startsAt := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	documentEvent := "Regata regional"
	modality := "Canoagem"
	store := &eventIndexStore{
		coachItems:  []dbgen.ListEventsForCoachRow{{ID: uuid.New(), Title: "Treino de equipa", EventType: "TRAINING", StartsAt: pgtype.Timestamptz{Time: startsAt, Valid: true}, EndsAt: pgtype.Timestamptz{Time: startsAt.Add(time.Hour), Valid: true}, Status: "ACTIVE"}},
		memberItems: []dbgen.ListEventsForMemberRow{{ID: uuid.New(), Title: "Regata regional", EventType: "COMPETITION", StartsAt: pgtype.Timestamptz{Time: startsAt, Valid: true}, EndsAt: pgtype.Timestamptz{Time: startsAt.Add(time.Hour), Valid: true}, Status: "CANCELLED", ResponseStatus: "Going"}},
		documents:   []dbgen.ListCompetitionDocumentsForAthleteRow{{ID: uuid.New(), Title: "Caderno de prova", Url: "https://example.test/caderno", Source: "FPC", ReviewedOn: pgtype.Date{Time: startsAt, Valid: true}, EventTitle: &documentEvent, ModalityName: &modality}},
		programmes:  []dbgen.Programme{{ID: programmeID, NamePt: "Competição"}},
	}
	h := Events{Store: store, Location: time.UTC, Now: func() time.Time { return startsAt }}

	coach := httptest.NewRequest(http.MethodGet, "/admin/eventos", nil)
	coach = coach.WithContext(context.WithValue(coach.Context(), currentUserKey{}, CurrentUser{ID: userID, CanManageEvents: true, CoachProgrammeIDs: map[uuid.UUID]bool{programmeID: true}}))
	coachResponse := httptest.NewRecorder()
	h.Index(coachResponse, coach)
	if coachResponse.Code != http.StatusOK || !strings.Contains(coachResponse.Body.String(), "Treino de equipa") || !strings.Contains(coachResponse.Body.String(), "Competição") {
		t.Fatalf("coach response=%d body=%s", coachResponse.Code, coachResponse.Body.String())
	}

	member := httptest.NewRequest(http.MethodGet, "/eventos", nil)
	member = member.WithContext(context.WithValue(member.Context(), currentUserKey{}, CurrentUser{ID: userID}))
	memberResponse := httptest.NewRecorder()
	h.Index(memberResponse, member)
	if memberResponse.Code != http.StatusOK || !strings.Contains(memberResponse.Body.String(), "Regata regional") || !strings.Contains(memberResponse.Body.String(), "Cancelado") || !strings.Contains(memberResponse.Body.String(), "Caderno de prova") || !strings.Contains(memberResponse.Body.String(), "Canoagem") {
		t.Fatalf("member response=%d body=%s", memberResponse.Code, memberResponse.Body.String())
	}
}

func TestEventIndexFailsClosedWhenARequiredReadFails(t *testing.T) {
	actor := CurrentUser{ID: uuid.New(), IsAdmin: true}
	for _, tc := range []struct {
		name  string
		path  string
		store eventIndexStore
		user  CurrentUser
	}{
		{"administrator events", "/admin/eventos", eventIndexStore{itemsErr: errors.New("database unavailable")}, actor},
		{"coach events", "/admin/eventos", eventIndexStore{coachItemsErr: errors.New("database unavailable")}, CurrentUser{ID: actor.ID, CanManageEvents: true}},
		{"programmes", "/admin/eventos", eventIndexStore{programmesErr: errors.New("database unavailable")}, actor},
		{"teams", "/admin/eventos", eventIndexStore{teamsErr: errors.New("database unavailable")}, actor},
		{"member events", "/eventos", eventIndexStore{memberItemsErr: errors.New("database unavailable")}, CurrentUser{ID: actor.ID}},
		{"member documents", "/eventos", eventIndexStore{documentsErr: errors.New("database unavailable")}, CurrentUser{ID: actor.ID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := Events{Store: &tc.store, Location: time.UTC}
			r := httptest.NewRequest(http.MethodGet, tc.path, nil)
			r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, tc.user))
			w := httptest.NewRecorder()
			h.Index(w, r)
			if w.Code != http.StatusInternalServerError {
				t.Fatalf("response=%d", w.Code)
			}
		})
	}
}

func TestEventCreateWritesValidatedAudienceThroughTransaction(t *testing.T) {
	programmeID, actorID := uuid.New(), uuid.New()
	tx := &eventTransactionFake{eventID: uuid.New()}
	store := &eventIndexStore{programmes: []dbgen.Programme{{ID: programmeID, NamePt: "Competição"}}}
	h := Events{Store: store, DB: eventMutationDB{tx: tx}, Location: time.UTC}
	values := url.Values{"title": {"Regata regional"}, "description": {"Concentração no cais"}, "event_type": {"GENERAL"}, "starts_at": {"2026-09-08T10:00"}, "ends_at": {"2026-09-08T12:00"}, "response_deadline": {"2026-09-07T12:00"}, "capacity": {"24"}, "programme_id": {programmeID.String()}}
	r := httptest.NewRequest(http.MethodPost, "/admin/eventos", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actorID, IsAdmin: true}))
	w := httptest.NewRecorder()

	h.Create(w, r)

	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/eventos" || !tx.committed {
		t.Fatalf("response=%d location=%q committed=%t", w.Code, w.Header().Get("Location"), tx.committed)
	}
	if len(tx.queryCalls) != 1 || len(tx.execCalls) != 1 {
		t.Fatalf("query calls=%#v exec calls=%#v", tx.queryCalls, tx.execCalls)
	}
	created := tx.queryCalls[0].args
	if created[0] != "Regata regional" || created[1] != "Concentração no cais" || created[2] != "GENERAL" || created[7] != actorID {
		t.Fatalf("create event args=%#v", created)
	}
	audience := tx.execCalls[0].args
	if audience[0] != tx.eventID || audience[1] != programmeID {
		t.Fatalf("audience args=%#v", audience)
	}
}

func TestEventCreateFailsClosedWhenTransactionCannotStart(t *testing.T) {
	programmeID, actorID := uuid.New(), uuid.New()
	store := &eventIndexStore{programmes: []dbgen.Programme{{ID: programmeID, NamePt: "Competição"}}}
	values := url.Values{"title": {"Regata regional"}, "description": {"Concentração no cais"}, "event_type": {"GENERAL"}, "starts_at": {"2026-09-08T10:00"}, "ends_at": {"2026-09-08T12:00"}, "programme_id": {programmeID.String()}}
	request := httptest.NewRequest(http.MethodPost, "/admin/eventos", strings.NewReader(values.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request = request.WithContext(context.WithValue(request.Context(), currentUserKey{}, CurrentUser{ID: actorID, IsAdmin: true}))
	response := httptest.NewRecorder()
	(Events{Store: store, DB: eventMutationDB{beginErr: errors.New("database unavailable")}, Location: time.UTC}).Create(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d", response.Code)
	}
}

func TestEventUpdateWritesFreshRecordWithoutChangingLockedAudience(t *testing.T) {
	eventID, programmeID, actorID := uuid.New(), uuid.New(), uuid.New()
	starts := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	updated := starts.Add(-time.Hour)
	tx := &eventTransactionFake{eventID: eventID}
	store := &eventIndexStore{
		programmes:        []dbgen.Programme{{ID: programmeID, NamePt: "Competição"}},
		edit:              dbgen.GetEventForEditRow{ID: eventID, Title: "Regata", EventType: "GENERAL", StartsAt: pgtype.Timestamptz{Time: starts, Valid: true}, EndsAt: pgtype.Timestamptz{Time: starts.Add(time.Hour), Valid: true}, Status: "ACTIVE", UpdatedAt: pgtype.Timestamptz{Time: updated, Valid: true}},
		programmeAudience: []uuid.UUID{programmeID},
	}
	h := Events{Store: store, DB: eventMutationDB{tx: tx}, Location: time.UTC, Now: func() time.Time { return starts.Add(-24 * time.Hour) }}
	values := url.Values{"title": {"Regata revista"}, "description": {"Novo cais"}, "event_type": {"GENERAL"}, "starts_at": {"2026-09-08T10:30"}, "ends_at": {"2026-09-08T12:30"}, "response_deadline": {"2026-09-07T12:00"}, "capacity": {"30"}, "programme_id": {programmeID.String()}, "expected_updated_at": {updated.Format(time.RFC3339Nano)}}
	r := httptest.NewRequest(http.MethodPost, "/admin/eventos/"+eventID.String(), strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetPathValue("id", eventID.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actorID, IsAdmin: true}))
	w := httptest.NewRecorder()

	h.Update(w, r)

	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/eventos/"+eventID.String() || !tx.committed || len(tx.queryCalls) != 1 || len(tx.execCalls) != 0 {
		t.Fatalf("response=%d location=%q committed=%t query=%#v exec=%#v", w.Code, w.Header().Get("Location"), tx.committed, tx.queryCalls, tx.execCalls)
	}
	args := tx.queryCalls[0].args
	if args[0] != "Regata revista" || args[1] != "Novo cais" || args[7] != eventID || args[10] != false {
		t.Fatalf("update args=%#v", args)
	}
}

func TestEventUpdateReplacesAudienceOnlyAfterFreshEventWrite(t *testing.T) {
	eventID, previousProgrammeID, nextProgrammeID, actorID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	starts := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	updated := starts.Add(-time.Hour)
	tx := &eventTransactionFake{eventID: eventID}
	store := &eventIndexStore{
		programmes:        []dbgen.Programme{{ID: previousProgrammeID}, {ID: nextProgrammeID}},
		edit:              dbgen.GetEventForEditRow{ID: eventID, Title: "Regata", EventType: "GENERAL", StartsAt: pgtype.Timestamptz{Time: starts, Valid: true}, EndsAt: pgtype.Timestamptz{Time: starts.Add(time.Hour), Valid: true}, Status: "ACTIVE", UpdatedAt: pgtype.Timestamptz{Time: updated, Valid: true}},
		programmeAudience: []uuid.UUID{previousProgrammeID},
	}
	h := Events{Store: store, DB: eventMutationDB{tx: tx}, Location: time.UTC, Now: func() time.Time { return starts.Add(-24 * time.Hour) }}
	values := url.Values{"title": {"Regata revista"}, "description": {"Novo cais"}, "event_type": {"GENERAL"}, "starts_at": {"2026-09-08T10:30"}, "ends_at": {"2026-09-08T12:30"}, "programme_id": {nextProgrammeID.String()}, "expected_updated_at": {updated.Format(time.RFC3339Nano)}}
	r := httptest.NewRequest(http.MethodPost, "/admin/eventos/"+eventID.String(), strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetPathValue("id", eventID.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actorID, IsAdmin: true}))
	w := httptest.NewRecorder()

	h.Update(w, r)

	if w.Code != http.StatusSeeOther || !tx.committed || len(tx.queryCalls) != 1 || len(tx.execCalls) != 3 || tx.queryCalls[0].args[10] != true {
		t.Fatalf("response=%d committed=%t query=%#v exec=%#v", w.Code, tx.committed, tx.queryCalls, tx.execCalls)
	}
	if audience := tx.execCalls[2].args; audience[0] != eventID || audience[1] != nextProgrammeID {
		t.Fatalf("replacement audience=%#v", audience)
	}
}

func TestEventUpdateReloadsLatestRecordAfterOptimisticConflict(t *testing.T) {
	eventID, programmeID, actorID := uuid.New(), uuid.New(), uuid.New()
	starts := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	updated := starts.Add(-time.Hour)
	tx := &eventTransactionFake{eventID: eventID, queryErrs: map[string]error{"UpdateEvent": pgx.ErrNoRows}}
	store := &eventIndexStore{
		programmes:        []dbgen.Programme{{ID: programmeID, NamePt: "Competição"}},
		edit:              dbgen.GetEventForEditRow{ID: eventID, Title: "Regata atual", EventType: "GENERAL", StartsAt: pgtype.Timestamptz{Time: starts, Valid: true}, EndsAt: pgtype.Timestamptz{Time: starts.Add(time.Hour), Valid: true}, Status: "ACTIVE", UpdatedAt: pgtype.Timestamptz{Time: updated, Valid: true}},
		programmeAudience: []uuid.UUID{programmeID},
	}
	values := url.Values{"title": {"Regata antiga"}, "description": {"Novo cais"}, "event_type": {"GENERAL"}, "starts_at": {"2026-09-08T10:30"}, "ends_at": {"2026-09-08T12:30"}, "programme_id": {programmeID.String()}, "expected_updated_at": {updated.Format(time.RFC3339Nano)}}
	r := httptest.NewRequest(http.MethodPost, "/admin/eventos/"+eventID.String(), strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetPathValue("id", eventID.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actorID, IsAdmin: true}))
	w := httptest.NewRecorder()

	(Events{Store: store, DB: eventMutationDB{tx: tx}, Location: time.UTC, Now: func() time.Time { return starts.Add(-24 * time.Hour) }}).Update(w, r)

	if w.Code != http.StatusConflict || tx.committed || !strings.Contains(w.Body.String(), "alterado entretanto") || !strings.Contains(w.Body.String(), "Regata atual") {
		t.Fatalf("response=%d committed=%t body=%q", w.Code, tx.committed, w.Body.String())
	}
}

func TestEventResponseLocksEligibilityThenSavesGoingStatus(t *testing.T) {
	eventID, memberID := uuid.New(), uuid.New()
	starts := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	tx := &eventTransactionFake{eventID: eventID, responseEvent: eventTransactionEvent{status: "ACTIVE", startsAt: starts, endsAt: starts.Add(time.Hour)}, eventResponseErr: pgx.ErrNoRows}
	store := &eventIndexStore{respondable: dbgen.Event{ID: eventID, Status: "ACTIVE"}}
	h := Events{Store: store, DB: eventMutationDB{tx: tx}, Location: time.UTC, Now: func() time.Time { return starts.Add(-time.Hour) }}
	r := httptest.NewRequest(http.MethodPost, "/events/"+eventID.String()+"/responder", strings.NewReader("status=Going"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetPathValue("id", eventID.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: memberID}))
	w := httptest.NewRecorder()

	h.Respond(w, r)

	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/events/"+eventID.String() || !tx.committed || len(tx.queryCalls) != 3 || len(tx.execCalls) != 1 {
		t.Fatalf("response=%d location=%q committed=%t query=%#v exec=%#v", w.Code, w.Header().Get("Location"), tx.committed, tx.queryCalls, tx.execCalls)
	}
	args := tx.execCalls[0].args
	if args[0] != eventID || args[1] != memberID || args[2] != dbgen.EventResponseStatusGoing || args[3] != memberID {
		t.Fatalf("saved response args=%#v", args)
	}
}

func TestEventResponseRejectsExpiredDeadlineAndWaitlistsWhenFull(t *testing.T) {
	eventID, memberID := uuid.New(), uuid.New()
	starts := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	request := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/events/"+eventID.String()+"/responder", strings.NewReader("status=Going"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.SetPathValue("id", eventID.String())
		return r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: memberID}))
	}

	t.Run("expired deadline", func(t *testing.T) {
		tx := &eventTransactionFake{eventID: eventID, responseEvent: eventTransactionEvent{status: "ACTIVE", startsAt: starts, endsAt: starts.Add(time.Hour), deadline: starts.Add(-time.Minute), deadlineValid: true}}
		w := httptest.NewRecorder()
		(Events{Store: &eventIndexStore{respondable: dbgen.Event{ID: eventID, Status: "ACTIVE"}}, DB: eventMutationDB{tx: tx}, Location: time.UTC, Now: func() time.Time { return starts }}).Respond(w, request())
		if w.Code != http.StatusConflict || tx.committed || len(tx.execCalls) != 0 || !strings.Contains(w.Body.String(), "prazo") {
			t.Fatalf("response=%d committed=%t exec=%#v body=%q", w.Code, tx.committed, tx.execCalls, w.Body.String())
		}
	})

	t.Run("full event waitlists new response", func(t *testing.T) {
		capacity := int32(1)
		tx := &eventTransactionFake{eventID: eventID, goingCount: 1, responseEvent: eventTransactionEvent{status: "ACTIVE", startsAt: starts, endsAt: starts.Add(time.Hour), capacity: &capacity}, eventResponseErr: pgx.ErrNoRows}
		w := httptest.NewRecorder()
		(Events{Store: &eventIndexStore{respondable: dbgen.Event{ID: eventID, Status: "ACTIVE"}}, DB: eventMutationDB{tx: tx}, Location: time.UTC, Now: func() time.Time { return starts.Add(-time.Hour) }}).Respond(w, request())
		if w.Code != http.StatusSeeOther || !tx.committed || len(tx.execCalls) != 1 || tx.execCalls[0].args[2] != dbgen.EventResponseStatusWaitlisted {
			t.Fatalf("response=%d committed=%t exec=%#v", w.Code, tx.committed, tx.execCalls)
		}
	})
}

func TestEventResponseAndStaffAuthorizationMapLookupFailures(t *testing.T) {
	eventID, subjectID, coachID := uuid.New(), uuid.New(), uuid.New()
	responseRequest := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/events/"+eventID.String()+"/responder", strings.NewReader("status=Going"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.SetPathValue("id", eventID.String())
		return r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: subjectID}))
	}
	for _, tc := range []struct {
		name  string
		store eventIndexStore
		want  int
	}{
		{name: "subject no longer eligible", store: eventIndexStore{respondableErr: pgx.ErrNoRows}, want: http.StatusForbidden},
		{name: "eligibility lookup failure", store: eventIndexStore{respondableErr: errors.New("database unavailable")}, want: http.StatusInternalServerError},
		{name: "event cancelled", store: eventIndexStore{respondable: dbgen.Event{ID: eventID, Status: "CANCELLED"}}, want: http.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			(Events{Store: &tc.store, Location: time.UTC}).Respond(response, responseRequest())
			if response.Code != tc.want {
				t.Fatalf("status=%d, want %d", response.Code, tc.want)
			}
		})
	}

	staffRequest := httptest.NewRequest(http.MethodPost, "/admin/eventos/"+eventID.String()+"/confirmar", strings.NewReader("user_id="+subjectID.String()))
	staffRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	staffRequest.SetPathValue("id", eventID.String())
	staffRequest = staffRequest.WithContext(context.WithValue(staffRequest.Context(), currentUserKey{}, CurrentUser{ID: coachID}))
	for _, tc := range []struct {
		name  string
		store eventIndexStore
		want  int
	}{
		{name: "coach no longer manages event", store: eventIndexStore{coachAllowed: false}, want: http.StatusForbidden},
		{name: "coach scope lookup failure", store: eventIndexStore{coachErr: errors.New("database unavailable")}, want: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			(Events{Store: &tc.store, Location: time.UTC}).Confirm(response, staffRequest)
			if response.Code != tc.want {
				t.Fatalf("status=%d, want %d", response.Code, tc.want)
			}
		})
	}
}

func TestEventStaffActionsCommitConfirmedAndCheckedInStates(t *testing.T) {
	eventID, memberID, staffID := uuid.New(), uuid.New(), uuid.New()
	starts := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		call func(Events, http.ResponseWriter, *http.Request)
		now  time.Time
	}{
		{"confirm", func(h Events, w http.ResponseWriter, r *http.Request) { h.Confirm(w, r) }, starts.Add(-time.Hour)},
		{"check in", func(h Events, w http.ResponseWriter, r *http.Request) { h.CheckIn(w, r) }, starts.Add(time.Hour)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := &eventTransactionFake{eventID: eventID, responseEvent: eventTransactionEvent{status: "ACTIVE", startsAt: starts, endsAt: starts.Add(time.Hour)}}
			h := Events{Store: &eventIndexStore{}, DB: eventMutationDB{tx: tx}, Location: time.UTC, Now: func() time.Time { return tc.now }}
			r := httptest.NewRequest(http.MethodPost, "/admin/eventos/"+eventID.String(), strings.NewReader("user_id="+memberID.String()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.SetPathValue("id", eventID.String())
			r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: staffID, IsAdmin: true}))
			w := httptest.NewRecorder()

			tc.call(h, w, r)

			if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/eventos/"+eventID.String() || !tx.committed || len(tx.queryCalls) != 1 || len(tx.execCalls) != 1 {
				t.Fatalf("response=%d location=%q committed=%t query=%#v exec=%#v", w.Code, w.Header().Get("Location"), tx.committed, tx.queryCalls, tx.execCalls)
			}
			args := tx.execCalls[0].args
			if tc.name == "confirm" && (args[0] != staffID || args[1] != eventID || args[2] != memberID) {
				t.Fatalf("confirm args=%#v", args)
			}
			if tc.name == "check in" {
				staff, ok := args[0].(*uuid.UUID)
				if !ok || *staff != staffID || args[1] != eventID || args[2] != memberID {
					t.Fatalf("check-in args=%#v", args)
				}
			}
		})
	}
}

func TestEventStaffActionsRejectFullConfirmationAndEarlyCheckIn(t *testing.T) {
	eventID, memberID, staffID := uuid.New(), uuid.New(), uuid.New()
	starts := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	capacity := int32(1)
	request := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/admin/eventos/"+eventID.String(), strings.NewReader("user_id="+memberID.String()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.SetPathValue("id", eventID.String())
		return r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: staffID, IsAdmin: true}))
	}

	t.Run("full confirmation", func(t *testing.T) {
		tx := &eventTransactionFake{eventID: eventID, goingCount: 1, responseEvent: eventTransactionEvent{status: "ACTIVE", startsAt: starts, endsAt: starts.Add(time.Hour), capacity: &capacity}}
		w := httptest.NewRecorder()
		(Events{Store: &eventIndexStore{}, DB: eventMutationDB{tx: tx}, Location: time.UTC, Now: func() time.Time { return starts.Add(-time.Hour) }}).Confirm(w, request())
		if w.Code != http.StatusConflict || tx.committed || len(tx.execCalls) != 0 {
			t.Fatalf("response=%d committed=%t exec=%#v", w.Code, tx.committed, tx.execCalls)
		}
	})

	t.Run("early check in", func(t *testing.T) {
		tx := &eventTransactionFake{eventID: eventID, responseEvent: eventTransactionEvent{status: "ACTIVE", startsAt: starts, endsAt: starts.Add(time.Hour)}}
		w := httptest.NewRecorder()
		(Events{Store: &eventIndexStore{}, DB: eventMutationDB{tx: tx}, Location: time.UTC, Now: func() time.Time { return starts.Add(-time.Minute) }}).CheckIn(w, request())
		if w.Code != http.StatusConflict || tx.committed || len(tx.execCalls) != 0 {
			t.Fatalf("response=%d committed=%t exec=%#v", w.Code, tx.committed, tx.execCalls)
		}
	})
}

func TestEventStaffActionsRejectStaleResponseState(t *testing.T) {
	eventID, memberID, staffID := uuid.New(), uuid.New(), uuid.New()
	starts := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		call func(Events, http.ResponseWriter, *http.Request)
		now  time.Time
	}{
		{"confirm", func(h Events, w http.ResponseWriter, r *http.Request) { h.Confirm(w, r) }, starts.Add(-time.Hour)},
		{"check in", func(h Events, w http.ResponseWriter, r *http.Request) { h.CheckIn(w, r) }, starts.Add(time.Hour)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := &eventTransactionFake{eventID: eventID, execTag: "UPDATE 0", responseEvent: eventTransactionEvent{status: "ACTIVE", startsAt: starts, endsAt: starts.Add(time.Hour)}}
			r := httptest.NewRequest(http.MethodPost, "/admin/eventos/"+eventID.String(), strings.NewReader("user_id="+memberID.String()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.SetPathValue("id", eventID.String())
			r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: staffID, IsAdmin: true}))
			w := httptest.NewRecorder()
			tc.call(Events{Store: &eventIndexStore{}, DB: eventMutationDB{tx: tx}, Location: time.UTC, Now: func() time.Time { return tc.now }}, w, r)
			if w.Code != http.StatusConflict || tx.committed || !strings.Contains(w.Body.String(), "operação não é válida") {
				t.Fatalf("response=%d committed=%t body=%q", w.Code, tx.committed, w.Body.String())
			}
		})
	}
}

func TestEventEditRendersAuthorizedFutureEvent(t *testing.T) {
	eventID, programmeID := uuid.New(), uuid.New()
	starts := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	store := &eventIndexStore{programmes: []dbgen.Programme{{ID: programmeID, NamePt: "Competição"}}, edit: dbgen.GetEventForEditRow{ID: eventID, Title: "Regata", EventType: "COMPETITION", StartsAt: pgtype.Timestamptz{Time: starts, Valid: true}, EndsAt: pgtype.Timestamptz{Time: starts.Add(time.Hour), Valid: true}, Status: "ACTIVE", UpdatedAt: pgtype.Timestamptz{Time: starts.Add(-time.Hour), Valid: true}}}
	r := httptest.NewRequest(http.MethodGet, "/admin/eventos/"+eventID.String()+"/editar", nil)
	r.SetPathValue("id", eventID.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: uuid.New(), Name: "Admin", IsAdmin: true, EmailVerified: true}))
	w := httptest.NewRecorder()
	(Events{Store: store, Location: time.UTC, Now: func() time.Time { return starts.Add(-time.Hour) }}).Edit(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Regata") || !strings.Contains(w.Body.String(), "Competição") {
		t.Fatalf("response=%d body=%s", w.Code, w.Body.String())
	}
}

func TestEventEditRejectsStartedEventWithoutAllowingMutation(t *testing.T) {
	eventID := uuid.New()
	starts := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	store := &eventIndexStore{edit: dbgen.GetEventForEditRow{ID: eventID, Title: "Regata", EventType: "GENERAL", StartsAt: pgtype.Timestamptz{Time: starts, Valid: true}, EndsAt: pgtype.Timestamptz{Time: starts.Add(time.Hour), Valid: true}, Status: "ACTIVE", UpdatedAt: pgtype.Timestamptz{Time: starts.Add(-time.Hour), Valid: true}}}
	r := httptest.NewRequest(http.MethodGet, "/admin/eventos/"+eventID.String()+"/editar", nil)
	r.SetPathValue("id", eventID.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: uuid.New(), IsAdmin: true}))
	w := httptest.NewRecorder()
	(Events{Store: store, Location: time.UTC, Now: func() time.Time { return starts }}).Edit(w, r)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "já não pode ser alterado") {
		t.Fatalf("response=%d body=%s", w.Code, w.Body.String())
	}
}

func TestEventCancellationRequiresConfirmedReasonAndFreshVersion(t *testing.T) {
	eventID := uuid.New()
	starts := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	store := &eventIndexStore{edit: dbgen.GetEventForEditRow{ID: eventID, Title: "Regata", EventType: "GENERAL", StartsAt: pgtype.Timestamptz{Time: starts, Valid: true}, EndsAt: pgtype.Timestamptz{Time: starts.Add(time.Hour), Valid: true}, Status: "ACTIVE", UpdatedAt: pgtype.Timestamptz{Time: starts.Add(-time.Hour), Valid: true}}}
	r := httptest.NewRequest(http.MethodPost, "/admin/events/"+eventID.String()+"/cancel", strings.NewReader("cancellation_reason=x&expected_updated_at=stale"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetPathValue("id", eventID.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: uuid.New(), IsAdmin: true}))
	w := httptest.NewRecorder()
	(Events{Store: store, Location: time.UTC}).Cancel(w, r)
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "Confirme que pretende cancelar") || !strings.Contains(w.Body.String(), "O motivo deve ter") {
		t.Fatalf("response=%d body=%s", w.Code, w.Body.String())
	}
}

func TestEventCancellationPersistsConfirmedFutureEvent(t *testing.T) {
	eventID, actorID := uuid.New(), uuid.New()
	starts := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	updated := starts.Add(-time.Hour)
	store := &eventIndexStore{edit: dbgen.GetEventForEditRow{ID: eventID, Title: "Regata", EventType: "GENERAL", StartsAt: pgtype.Timestamptz{Time: starts, Valid: true}, EndsAt: pgtype.Timestamptz{Time: starts.Add(time.Hour), Valid: true}, Status: "ACTIVE", UpdatedAt: pgtype.Timestamptz{Time: updated, Valid: true}}}
	now := starts.Add(-24 * time.Hour)
	h := Events{Store: store, Location: time.UTC, Now: func() time.Time { return now }}
	values := url.Values{"cancellation_reason": {"Condições meteorológicas"}, "confirm_cancellation": {"yes"}, "expected_updated_at": {updated.Format(time.RFC3339Nano)}}
	r := httptest.NewRequest(http.MethodPost, "/admin/eventos/"+eventID.String()+"/cancelar", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetPathValue("id", eventID.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actorID, IsAdmin: true}))
	w := httptest.NewRecorder()

	h.Cancel(w, r)

	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/eventos/"+eventID.String() {
		t.Fatalf("response=%d location=%q", w.Code, w.Header().Get("Location"))
	}
	p := store.cancelled
	if p.ID != eventID || p.CancelledByID == nil || *p.CancelledByID != actorID || p.CancellationReason == nil || *p.CancellationReason != "Condições meteorológicas" || !p.CancelledAt.Time.Equal(now) || !p.ExpectedUpdatedAt.Time.Equal(updated) {
		t.Fatalf("cancel params=%#v", p)
	}
}

func TestEventCancellationMapsStaleAndUnexpectedPersistenceFailures(t *testing.T) {
	eventID, actorID := uuid.New(), uuid.New()
	starts := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	updated := starts.Add(-time.Hour)
	values := url.Values{"cancellation_reason": {"Condições meteorológicas"}, "confirm_cancellation": {"yes"}, "expected_updated_at": {updated.Format(time.RFC3339Nano)}}
	for _, tc := range []struct {
		name     string
		readErr  error
		writeErr error
		want     int
		body     string
	}{
		{name: "event removed before form submit", readErr: pgx.ErrNoRows, want: http.StatusNotFound},
		{name: "stale cancellation", writeErr: pgx.ErrNoRows, want: http.StatusConflict, body: "já começou ou já foi cancelado"},
		{name: "write service failure", writeErr: errors.New("database unavailable"), want: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &eventIndexStore{edit: dbgen.GetEventForEditRow{ID: eventID, Title: "Regata", EventType: "GENERAL", StartsAt: pgtype.Timestamptz{Time: starts, Valid: true}, EndsAt: pgtype.Timestamptz{Time: starts.Add(time.Hour), Valid: true}, Status: "ACTIVE", UpdatedAt: pgtype.Timestamptz{Time: updated, Valid: true}}, editErr: tc.readErr, cancelErr: tc.writeErr}
			request := httptest.NewRequest(http.MethodPost, "/admin/eventos/"+eventID.String()+"/cancelar", strings.NewReader(values.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.SetPathValue("id", eventID.String())
			request = request.WithContext(context.WithValue(request.Context(), currentUserKey{}, CurrentUser{ID: actorID, IsAdmin: true}))
			response := httptest.NewRecorder()
			(Events{Store: store, Location: time.UTC, Now: func() time.Time { return starts.Add(-24 * time.Hour) }}).Cancel(response, request)
			if response.Code != tc.want || (tc.body != "" && !strings.Contains(response.Body.String(), tc.body)) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestAdminEventDetailRendersResponsesAndEditableState(t *testing.T) {
	eventID := uuid.New()
	starts := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	store := &eventIndexStore{adminDetail: dbgen.GetEventDetailForAdminRow{ID: eventID, Title: "Regata", EventType: "COMPETITION", StartsAt: pgtype.Timestamptz{Time: starts, Valid: true}, EndsAt: pgtype.Timestamptz{Time: starts.Add(time.Hour), Valid: true}, Status: "ACTIVE"}, responses: []dbgen.ListEventResponsesForAdminRow{{UserID: uuid.New(), UserName: "Atleta", Status: "Going", RespondedAt: pgtype.Timestamptz{Time: starts.Add(-time.Hour), Valid: true}}}}
	r := httptest.NewRequest(http.MethodGet, "/admin/eventos/"+eventID.String(), nil)
	r.SetPathValue("id", eventID.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: uuid.New(), IsAdmin: true}))
	w := httptest.NewRecorder()
	(Events{Store: store, Location: time.UTC, Now: func() time.Time { return starts.Add(-time.Hour) }}).Detail(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Atleta") || !strings.Contains(w.Body.String(), "Editar evento") {
		t.Fatalf("response=%d body=%s", w.Code, w.Body.String())
	}
}

func TestAdminEventDetailMapsMissingAndDependentReadFailures(t *testing.T) {
	eventID := uuid.New()
	starts := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name  string
		store eventIndexStore
		want  int
	}{
		{name: "missing event", store: eventIndexStore{adminDetailErr: pgx.ErrNoRows}, want: http.StatusNotFound},
		{name: "event read failure", store: eventIndexStore{adminDetailErr: errors.New("database unavailable")}, want: http.StatusInternalServerError},
		{name: "response list failure", store: eventIndexStore{adminDetail: dbgen.GetEventDetailForAdminRow{ID: eventID, Title: "Regata", StartsAt: pgtype.Timestamptz{Time: starts, Valid: true}}, responsesErr: errors.New("database unavailable")}, want: http.StatusInternalServerError},
		{name: "document list failure", store: eventIndexStore{adminDetail: dbgen.GetEventDetailForAdminRow{ID: eventID, Title: "Regata", StartsAt: pgtype.Timestamptz{Time: starts, Valid: true}}, documentsErr: errors.New("database unavailable")}, want: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/admin/eventos/"+eventID.String(), nil)
			request.SetPathValue("id", eventID.String())
			request = request.WithContext(context.WithValue(request.Context(), currentUserKey{}, CurrentUser{ID: uuid.New(), IsAdmin: true}))
			response := httptest.NewRecorder()
			(Events{Store: &tc.store, Location: time.UTC}).Detail(response, request)
			if response.Code != tc.want {
				t.Fatalf("status=%d, want %d", response.Code, tc.want)
			}
		})
	}
}

func TestEventParticipationRejectsInvalidCancelledAndUnmanagedActionsBeforeMutation(t *testing.T) {
	eventID, memberID, coachID := uuid.New(), uuid.New(), uuid.New()
	actorContext := func(r *http.Request, actor CurrentUser) *http.Request {
		return r.WithContext(context.WithValue(r.Context(), currentUserKey{}, actor))
	}

	t.Run("response requires valid event and status", func(t *testing.T) {
		h := Events{Store: &eventIndexStore{}, Location: time.UTC}
		for _, tc := range []struct {
			id, body string
			want     int
		}{{"bad-id", "status=Going", http.StatusNotFound}, {eventID.String(), "status=Maybe", http.StatusBadRequest}} {
			r := httptest.NewRequest(http.MethodPost, "/events/"+tc.id+"/responder", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.SetPathValue("id", tc.id)
			r = actorContext(r, CurrentUser{ID: memberID})
			w := httptest.NewRecorder()
			h.Respond(w, r)
			if w.Code != tc.want {
				t.Fatalf("id=%q body=%q response=%d", tc.id, tc.body, w.Code)
			}
		}
	})

	t.Run("cancelled event cannot accept response", func(t *testing.T) {
		h := Events{Store: &eventIndexStore{respondable: dbgen.Event{ID: eventID, Status: "CANCELLED"}}, Location: time.UTC}
		r := httptest.NewRequest(http.MethodPost, "/events/"+eventID.String()+"/responder", strings.NewReader("status=Going"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.SetPathValue("id", eventID.String())
		r = actorContext(r, CurrentUser{ID: memberID})
		w := httptest.NewRecorder()
		h.Respond(w, r)
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "cancelado") {
			t.Fatalf("response=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("unscoped coach cannot confirm or check in", func(t *testing.T) {
		for _, action := range []func(http.ResponseWriter, *http.Request){Events{Store: &eventIndexStore{coachAllowed: false}, Location: time.UTC}.Confirm, Events{Store: &eventIndexStore{coachAllowed: false}, Location: time.UTC}.CheckIn} {
			r := httptest.NewRequest(http.MethodPost, "/admin/eventos/"+eventID.String(), strings.NewReader("user_id="+memberID.String()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.SetPathValue("id", eventID.String())
			r = actorContext(r, CurrentUser{ID: coachID, CanManageEvents: true})
			w := httptest.NewRecorder()
			action(w, r)
			if w.Code != http.StatusForbidden {
				t.Fatalf("response=%d", w.Code)
			}
		}
	})
}

func TestEventManagementAndAudienceValidationFailClosedForUntrustedScope(t *testing.T) {
	eventID, programmeID, teamID := uuid.New(), uuid.New(), uuid.New()
	store := &eventIndexStore{programmes: []dbgen.Programme{{ID: programmeID}}, teams: []dbgen.ListTeamsForEventAuthoringRow{{ID: teamID, ProgrammeID: programmeID}}, coachAllowed: false}
	h := Events{Store: store, Location: time.UTC}
	request := httptest.NewRequest(http.MethodGet, "/admin/eventos/"+eventID.String(), nil)
	response := httptest.NewRecorder()
	if h.canManageEvent(context.Background(), CurrentUser{ID: uuid.New()}, eventID, response, request) || response.Code != http.StatusForbidden {
		t.Fatalf("unmanaged coach response=%d", response.Code)
	}

	form := eventForm{ProgrammeIDs: []uuid.UUID{programmeID, uuid.New()}, TeamIDs: []uuid.UUID{teamID, uuid.New()}, Errors: validation.FieldErrors{}}
	h.validateEventAudience(context.Background(), CurrentUser{CoachProgrammeIDs: map[uuid.UUID]bool{programmeID: true}}, &form)
	for _, field := range []string{"programme_id", "team_id"} {
		if !form.Errors.Has(field) {
			t.Errorf("missing %s error: %#v", field, form.Errors)
		}
	}
}

func TestEventCreateFailsClosedForUnavailableOrUntrustedAudienceData(t *testing.T) {
	actorID, programmeID, teamID := uuid.New(), uuid.New(), uuid.New()
	values := func() url.Values {
		return url.Values{"title": {"Regata"}, "event_type": {"GENERAL"}, "starts_at": {"2026-09-08T10:00"}, "ends_at": {"2026-09-08T12:00"}, "programme_id": {programmeID.String()}, "team_id": {teamID.String()}}
	}
	request := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/admin/eventos", strings.NewReader(values().Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actorID, IsAdmin: true}))
	}

	for name, store := range map[string]*eventIndexStore{
		"programmes unavailable": {programmesErr: errors.New("down")},
		"teams unavailable":      {programmes: []dbgen.Programme{{ID: programmeID}}, teamsErr: errors.New("down")},
	} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			(Events{Store: store, Location: time.UTC}).Create(w, request())
			if w.Code != http.StatusInternalServerError {
				t.Fatalf("response=%d", w.Code)
			}
		})
	}

	w := httptest.NewRecorder()
	(Events{Store: &eventIndexStore{programmes: []dbgen.Programme{{ID: programmeID}}}, Location: time.UTC}).Create(w, request())
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "equipas válidas") {
		t.Fatalf("response=%d body=%q", w.Code, w.Body.String())
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

type eventIndexStore struct {
	dbgen.Querier
	items             []dbgen.ListEventsForAdminRow
	itemsErr          error
	coachItems        []dbgen.ListEventsForCoachRow
	coachItemsErr     error
	memberItems       []dbgen.ListEventsForMemberRow
	memberItemsErr    error
	documents         []dbgen.ListCompetitionDocumentsForAthleteRow
	programmes        []dbgen.Programme
	programmesErr     error
	teams             []dbgen.ListTeamsForEventAuthoringRow
	teamsErr          error
	edit              dbgen.GetEventForEditRow
	adminDetail       dbgen.GetEventDetailForAdminRow
	responses         []dbgen.ListEventResponsesForAdminRow
	respondable       dbgen.Event
	respondableErr    error
	coachAllowed      bool
	coachErr          error
	programmeAudience []uuid.UUID
	teamAudience      []uuid.UUID
	cancelled         dbgen.CancelEventParams
	editErr           error
	cancelErr         error
	adminDetailErr    error
	responsesErr      error
	documentsErr      error
}

type eventSQLCall struct{ args []any }

type eventTransactionFake struct {
	pgx.Tx
	eventID          uuid.UUID
	responseEvent    eventTransactionEvent
	eventResponseErr error
	queryErrs        map[string]error
	execTag          string
	goingCount       int64
	queryCalls       []eventSQLCall
	execCalls        []eventSQLCall
	committed        bool
}

type eventTransactionEvent struct {
	status                     string
	startsAt, endsAt, deadline time.Time
	deadlineValid              bool
	capacity                   *int32
}

func (tx *eventTransactionFake) QueryRow(_ context.Context, query string, args ...any) pgx.Row {
	tx.queryCalls = append(tx.queryCalls, eventSQLCall{args: args})
	row := eventTransactionRow{id: tx.eventID, event: tx.responseEvent, goingCount: tx.goingCount}
	for name, err := range tx.queryErrs {
		if strings.Contains(query, name) {
			row.err = err
			break
		}
	}
	if strings.Contains(query, "GetEventResponse") {
		row.err = tx.eventResponseErr
	}
	return row
}
func (tx *eventTransactionFake) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	tx.execCalls = append(tx.execCalls, eventSQLCall{args: args})
	tag := tx.execTag
	if tag == "" {
		tag = "INSERT 0 1"
	}
	return pgconn.NewCommandTag(tag), nil
}
func (tx *eventTransactionFake) Commit(context.Context) error   { tx.committed = true; return nil }
func (tx *eventTransactionFake) Rollback(context.Context) error { return nil }

type eventTransactionRow struct {
	id         uuid.UUID
	event      eventTransactionEvent
	goingCount int64
	err        error
}

func (row eventTransactionRow) Scan(dest ...any) error {
	if row.err != nil {
		return row.err
	}
	if len(dest) > 0 {
		if id, ok := dest[0].(*uuid.UUID); ok {
			*id = row.id
		}
		if count, ok := dest[0].(*int64); ok {
			*count = row.goingCount
		}
	}
	if len(dest) > 8 {
		if status, ok := dest[8].(*string); ok {
			*status = row.event.status
		}
	}
	if len(dest) > 4 {
		if starts, ok := dest[4].(*pgtype.Timestamptz); ok && !row.event.startsAt.IsZero() {
			*starts = pgtype.Timestamptz{Time: row.event.startsAt, Valid: true}
		}
	}
	if len(dest) > 5 {
		if ends, ok := dest[5].(*pgtype.Timestamptz); ok && !row.event.endsAt.IsZero() {
			*ends = pgtype.Timestamptz{Time: row.event.endsAt, Valid: true}
		}
	}
	if len(dest) > 6 {
		if deadline, ok := dest[6].(*pgtype.Timestamptz); ok && row.event.deadlineValid {
			*deadline = pgtype.Timestamptz{Time: row.event.deadline, Valid: true}
		}
	}
	if len(dest) > 7 {
		if capacity, ok := dest[7].(**int32); ok {
			*capacity = row.event.capacity
		}
	}
	return nil
}

type eventMutationDB struct {
	tx       *eventTransactionFake
	beginErr error
}

func (db eventMutationDB) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	if db.beginErr != nil {
		return nil, db.beginErr
	}
	return db.tx, nil
}
func (db eventMutationDB) Exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	return db.tx.Exec(ctx, query, args...)
}
func (db eventMutationDB) Query(ctx context.Context, query string, args ...any) (pgx.Rows, error) {
	return db.tx.Query(ctx, query, args...)
}
func (db eventMutationDB) QueryRow(ctx context.Context, query string, args ...any) pgx.Row {
	return db.tx.QueryRow(ctx, query, args...)
}

func (s *eventIndexStore) GetEventForEdit(context.Context, uuid.UUID) (dbgen.GetEventForEditRow, error) {
	return s.edit, s.editErr
}
func (s *eventIndexStore) CancelEvent(_ context.Context, params dbgen.CancelEventParams) (dbgen.Event, error) {
	s.cancelled = params
	return dbgen.Event{ID: params.ID}, s.cancelErr
}
func (s *eventIndexStore) ListEventProgrammeAudienceIDs(context.Context, uuid.UUID) ([]uuid.UUID, error) {
	return s.programmeAudience, nil
}
func (s *eventIndexStore) ListEventTeamAudienceIDs(context.Context, uuid.UUID) ([]uuid.UUID, error) {
	return s.teamAudience, nil
}
func (s *eventIndexStore) GetEventDetailForAdmin(context.Context, uuid.UUID) (dbgen.GetEventDetailForAdminRow, error) {
	return s.adminDetail, s.adminDetailErr
}
func (s *eventIndexStore) ListEventResponsesForAdmin(context.Context, dbgen.ListEventResponsesForAdminParams) ([]dbgen.ListEventResponsesForAdminRow, error) {
	return s.responses, s.responsesErr
}
func (s *eventIndexStore) ListCompetitionDocumentsForEvent(context.Context, *uuid.UUID) ([]dbgen.ListCompetitionDocumentsForEventRow, error) {
	return nil, s.documentsErr
}
func (s *eventIndexStore) GetRespondableEvent(context.Context, dbgen.GetRespondableEventParams) (dbgen.Event, error) {
	return s.respondable, s.respondableErr
}
func (s *eventIndexStore) CanCoachManageEvent(context.Context, dbgen.CanCoachManageEventParams) (bool, error) {
	return s.coachAllowed, s.coachErr
}

func (s *eventIndexStore) ListEventsForAdmin(context.Context, dbgen.ListEventsForAdminParams) ([]dbgen.ListEventsForAdminRow, error) {
	return s.items, s.itemsErr
}

func (s *eventIndexStore) ListEventsForCoach(context.Context, dbgen.ListEventsForCoachParams) ([]dbgen.ListEventsForCoachRow, error) {
	return s.coachItems, s.coachItemsErr
}

func (s *eventIndexStore) ListEventsForMember(context.Context, dbgen.ListEventsForMemberParams) ([]dbgen.ListEventsForMemberRow, error) {
	return s.memberItems, s.memberItemsErr
}

func (s *eventIndexStore) ListCompetitionDocumentsForAthlete(context.Context, dbgen.ListCompetitionDocumentsForAthleteParams) ([]dbgen.ListCompetitionDocumentsForAthleteRow, error) {
	return s.documents, s.documentsErr
}

func (s *eventIndexStore) ListProgrammes(context.Context) ([]dbgen.Programme, error) {
	return s.programmes, s.programmesErr
}

func (s *eventIndexStore) ListTeamsForEventAuthoring(context.Context) ([]dbgen.ListTeamsForEventAuthoringRow, error) {
	return s.teams, s.teamsErr
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
