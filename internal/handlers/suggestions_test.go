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

func TestValidateSuggestionDefinesBoundedCategoriesAndContent(t *testing.T) {
	valid := suggestionRequest(http.MethodPost, "/sugestoes", url.Values{"category": {"EQUIPMENT"}, "subject": {"Mais suportes"}, "description": {"Precisamos de mais suportes para os barcos."}})
	if form := validateSuggestion(valid); !form.Errors.Empty() {
		t.Fatalf("valid errors = %#v", form.Errors)
	}

	invalid := suggestionRequest(http.MethodPost, "/sugestoes", url.Values{"category": {"UNKNOWN"}, "subject": {"x"}, "description": {"curta"}})
	form := validateSuggestion(invalid)
	for _, field := range []string{"category", "subject", "description"} {
		if form.Errors[field] == "" {
			t.Fatalf("missing %s error: %#v", field, form.Errors)
		}
	}
}

func TestSuggestionCreateUsesAuthenticatedRequester(t *testing.T) {
	requesterID := uuid.New()
	store := &suggestionsStoreFake{}
	h := Suggestions{Store: store}
	r := suggestionRequest(http.MethodPost, "/sugestoes", url.Values{"category": {"TRAINING"}, "subject": {"Treino aberto"}, "description": {"Criar um treino aberto mensal para todos."}})
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: requesterID, Name: "Membro"}))
	w := httptest.NewRecorder()

	h.Create(w, r)

	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/sugestoes" {
		t.Fatalf("response = %d, location = %q", w.Code, w.Header().Get("Location"))
	}
	if store.created.RequesterID != requesterID || store.created.Category != "TRAINING" || store.created.Subject != "Treino aberto" {
		t.Fatalf("created = %#v", store.created)
	}
}

func TestSuggestionMemberPageUsesRequesterScopedQuery(t *testing.T) {
	requesterID := uuid.New()
	now := time.Now().UTC()
	store := &suggestionsStoreFake{own: []dbgen.ListSuggestionsForRequesterRow{{ID: uuid.New(), Category: "OTHER", Subject: "Ideia privada", Description: "Uma descrição suficientemente longa.", Status: "SUBMITTED", CreatedAt: pgtype.Timestamptz{Time: now, Valid: true}, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}}}
	h := Suggestions{Store: store, Location: time.UTC}
	r := httptest.NewRequest(http.MethodGet, "/sugestoes", nil)
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: requesterID, Name: "Membro"}))
	w := httptest.NewRecorder()

	h.Index(w, r)

	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Ideia privada") || !strings.Contains(w.Body.String(), "visível apenas para si") || !strings.Contains(w.Body.String(), "data-create-panel") {
		t.Fatalf("response = %d, body = %s", w.Code, w.Body.String())
	}
	if store.ownParams.RequesterID != requesterID || store.triageCalled {
		t.Fatalf("own params = %#v, triage called = %v", store.ownParams, store.triageCalled)
	}
}

func TestSuggestionValidationReopensCreatePanel(t *testing.T) {
	requesterID := uuid.New()
	h := Suggestions{Store: &suggestionsStoreFake{}}
	r := suggestionRequest(http.MethodPost, "/sugestoes", url.Values{"category": {"UNKNOWN"}, "subject": {"x"}, "description": {"curta"}})
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: requesterID, Name: "Membro"}))
	w := httptest.NewRecorder()

	h.Create(w, r)

	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), `<details id="nova-sugestao" class="module disclosure create-panel" data-create-panel open`) {
		t.Fatalf("response = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestSuggestionTriageRequiresDecisionResponse(t *testing.T) {
	now := time.Now().UTC()
	r := suggestionRequest(http.MethodPost, "/admin/sugestoes/id", url.Values{"status": {"DECLINED"}, "updated_at": {now.Format(time.RFC3339Nano)}})
	form := validateSuggestionTriage(r)
	if form.Errors["staff_response"] == "" {
		t.Fatalf("errors = %#v", form.Errors)
	}
}

func TestSuggestionTriageRejectsStaleWrite(t *testing.T) {
	id, actorID := uuid.New(), uuid.New()
	now := time.Now().UTC().Truncate(time.Microsecond)
	store := &suggestionsStoreFake{updatedRows: 0, triage: []dbgen.ListSuggestionsForTriageRow{{ID: id, RequesterName: "Ana", Category: "FACILITIES", Subject: "Balneário", Description: "Melhorar os bancos do balneário feminino.", Status: "UNDER_REVIEW", CreatedAt: pgtype.Timestamptz{Time: now, Valid: true}, UpdatedAt: pgtype.Timestamptz{Time: now.Add(time.Second), Valid: true}}}}
	h := Suggestions{Store: store, Location: time.UTC}
	r := suggestionRequest(http.MethodPost, "/admin/sugestoes/"+id.String(), url.Values{"status": {"PLANNED"}, "staff_response": {"Incluída no próximo plano."}, "updated_at": {now.Format(time.RFC3339Nano)}})
	r.SetPathValue("id", id.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actorID, Name: "Moderadora", CanModerateContent: true}))
	w := httptest.NewRecorder()

	h.Update(w, r)

	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "atualizada por outra pessoa") {
		t.Fatalf("response = %d, body = %s", w.Code, w.Body.String())
	}
	if store.updated.ID != id || store.updated.ActorUserID != actorID {
		t.Fatalf("updated = %#v", store.updated)
	}
}

func TestAdminSuggestionsURLRetainsOnlyValidFilters(t *testing.T) {
	values := url.Values{"status": {"PLANNED"}, "category": {"EVENTS"}, "ignored": {"secret"}}
	if got, want := adminSuggestionsURL(values, 2), "/admin/sugestoes?category=EVENTS&page=2&status=PLANNED"; got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}
}

func suggestionRequest(method, target string, values url.Values) *http.Request {
	r := httptest.NewRequest(method, target, strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_ = r.ParseForm()
	return r
}

type suggestionsStoreFake struct {
	created      dbgen.CreateSuggestionParams
	ownParams    dbgen.ListSuggestionsForRequesterParams
	updated      dbgen.UpdateSuggestionTriageParams
	own          []dbgen.ListSuggestionsForRequesterRow
	triage       []dbgen.ListSuggestionsForTriageRow
	updatedRows  int64
	triageCalled bool
}

func (s *suggestionsStoreFake) CreateSuggestion(_ context.Context, input dbgen.CreateSuggestionParams) (dbgen.CreateSuggestionRow, error) {
	s.created = input
	return dbgen.CreateSuggestionRow{}, nil
}
func (s *suggestionsStoreFake) ListSuggestionsForRequester(_ context.Context, input dbgen.ListSuggestionsForRequesterParams) ([]dbgen.ListSuggestionsForRequesterRow, error) {
	s.ownParams = input
	return s.own, nil
}
func (s *suggestionsStoreFake) ListSuggestionsForTriage(_ context.Context, _ dbgen.ListSuggestionsForTriageParams) ([]dbgen.ListSuggestionsForTriageRow, error) {
	s.triageCalled = true
	return s.triage, nil
}
func (s *suggestionsStoreFake) UpdateSuggestionTriage(_ context.Context, input dbgen.UpdateSuggestionTriageParams) (int64, error) {
	s.updated = input
	return s.updatedRows, nil
}
