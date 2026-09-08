package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	pr "github.com/cfcoimbra/mycfc/internal/privacyrequests"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type privacyHandlerStore struct {
	PrivacyRequestStore
	view        pr.View
	viewErr     error
	changeErr   error
	changeInput pr.ReviewInput
}

func (s *privacyHandlerStore) View(context.Context, uuid.UUID, uuid.UUID, bool) (pr.View, error) {
	return s.view, s.viewErr
}
func (s *privacyHandlerStore) Change(_ context.Context, in pr.ReviewInput) (dbgen.DataErasureRequest, error) {
	s.changeInput = in
	return s.view.Record, s.changeErr
}
func privacyHandlerRequest(method, path string, form url.Values) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetPathValue("ref", "11000000-0000-0000-0000-000000000001")
	return r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: uuid.New(), Name: "Current person"}))
}
func privacyHandlerFixture(t *testing.T) *privacyHandlerStore {
	t.Helper()
	decisions, err := json.Marshal([]pr.CategoryDecision{{Category: "alpha", Outcome: "RETAIN", Ground: "hold"}})
	if err != nil {
		t.Fatal(err)
	}
	return &privacyHandlerStore{view: pr.View{
		Record:  dbgen.DataErasureRequest{PublicRef: uuid.MustParse("11000000-0000-0000-0000-000000000001"), Version: 7, Status: "REFUSED", ScopeKind: "CATEGORIES", Categories: []string{"alpha"}, CategoryDecisions: decisions, DecisionExplanation: "Protected explanation", ReceivedAt: pgtype.Timestamptz{Time: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC), Valid: true}},
		Subject: dbgen.User{ID: uuid.New(), Name: "Protected subject"}, Requester: dbgen.User{ID: uuid.New(), Name: "Protected requester"},
		Policy: pr.AdoptedPolicy{Version: "private-policy", Categories: []pr.CatalogueEntry{{Key: "alpha", Label: "Protected category", Grounds: []pr.Ground{{Code: "hold", Label: "Approved preservation ground"}}}}},
	}}
}
func TestPrivacyDetailRendersSavedDecisionAndApprovedGround(t *testing.T) {
	s := privacyHandlerFixture(t)
	r := privacyHandlerRequest(http.MethodGet, "/perfil/privacidade/11000000-0000-0000-0000-000000000001", nil)
	w := httptest.NewRecorder()
	PrivacyRequests{Service: s}.Detail(w, r)
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	for _, want := range []string{"Protected category", "Approved preservation ground", "Protected explanation"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("missing %q", want)
		}
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "same-origin" {
		t.Fatal("missing privacy headers")
	}
}
func TestPrivacyDetailSafeReceiptOmitsProtectedDecision(t *testing.T) {
	s := privacyHandlerFixture(t)
	s.view.SafeReceipt = true
	w := httptest.NewRecorder()
	PrivacyRequests{Service: s}.Detail(w, privacyHandlerRequest(http.MethodGet, "/perfil/privacidade/11000000-0000-0000-0000-000000000001", nil))
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	for _, secret := range []string{"Protected subject", "Protected requester", "Protected category", "Approved preservation ground", "Protected explanation", "private-policy"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Errorf("safe receipt exposed %q", secret)
		}
	}
}
func TestPrivacyChangeStaleVersionShowsCurrentCase(t *testing.T) {
	s := privacyHandlerFixture(t)
	s.changeErr = pr.ErrStaleVersion
	w := httptest.NewRecorder()
	PrivacyRequests{Service: s}.Change(w, privacyHandlerRequest(http.MethodPost, "/admin/privacidade/11000000-0000-0000-0000-000000000001", url.Values{"version": {"6"}, "action": {"refuse"}, "outcome_alpha": {"RETAIN"}, "ground_alpha": {"hold"}}))
	if w.Code != 409 || !strings.Contains(w.Body.String(), "O pedido foi atualizado") {
		t.Fatalf("stale response %d", w.Code)
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "same-origin" {
		t.Fatal("privacy POST response did not preserve same-origin form policy")
	}
	if s.changeInput.Version != 6 || s.changeInput.Decisions["alpha"].Ground != "hold" {
		t.Fatalf("lost submitted CAS/decision: %+v", s.changeInput)
	}
}
func TestPrivacyDetailDeniedDoesNotExposeCase(t *testing.T) {
	s := privacyHandlerFixture(t)
	s.viewErr = pr.ErrForbidden
	w := httptest.NewRecorder()
	PrivacyRequests{Service: s}.Detail(w, privacyHandlerRequest(http.MethodGet, "/admin/privacidade/11000000-0000-0000-0000-000000000001", nil))
	if w.Code != 404 || strings.Contains(w.Body.String(), "Protected explanation") {
		t.Fatalf("unauthorized response %d", w.Code)
	}
}

func TestPrivacyDetailDoesNotOfferAnotherReviewersCaseActions(t *testing.T) {
	s := privacyHandlerFixture(t)
	s.view.Record.Status = "UNDER_REVIEW"
	s.view.Record.DueAt = pgtype.Timestamptz{Time: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), Valid: true}
	otherReviewer := uuid.New()
	s.view.Record.ClaimedBy = &otherReviewer
	r := privacyHandlerRequest(http.MethodGet, "/admin/privacidade/11000000-0000-0000-0000-000000000001", nil)
	w := httptest.NewRecorder()
	PrivacyRequests{Service: s, Now: func() time.Time { return time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC) }}.Detail(w, r)
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	for _, action := range []string{"Assumir análise", "Guardar verificação", "Prorrogar prazo"} {
		if strings.Contains(w.Body.String(), action) {
			t.Errorf("offered %q for another reviewer's case", action)
		}
	}
}
