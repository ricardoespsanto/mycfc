package handlers

import (
	"context"
	"errors"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type workoutEditRouteStore struct {
	focusedEditHTTPStore
	readErr, afterWriteErr, previewErr, sizeErr error
	previewSource                               *focusedEditSource
	artifactSize                                int32
	previewReads, reads                         int
	saved                                       focusedEditReviewed
	cancelRequest                               FocusedWorkoutEditRequest
	cancelExpected                              time.Time
	cancelReason                                string
}

func (s *workoutEditRouteStore) LoadFocusedEdit(ctx context.Context, request FocusedWorkoutEditRequest) (focusedEditSource, error) {
	s.reads++
	if s.readErr != nil {
		return focusedEditSource{}, s.readErr
	}
	if s.writes != 0 && s.afterWriteErr != nil {
		return focusedEditSource{}, s.afterWriteErr
	}
	return s.focusedEditHTTPStore.LoadFocusedEdit(ctx, request)
}

func (s *workoutEditRouteStore) LoadFocusedEditSession(ctx context.Context, request FocusedWorkoutEditRequest) (focusedEditSource, error) {
	return s.LoadFocusedEdit(ctx, request)
}

func (s *workoutEditRouteStore) ReadFocusedEditPreview(context.Context, FocusedWorkoutEditRequest, []uuid.UUID) (focusedEditSource, error) {
	s.previewReads++
	if s.previewSource != nil {
		return *s.previewSource, s.previewErr
	}
	return s.source, s.previewErr
}

func (s *workoutEditRouteStore) FocusedWorkoutArtifactSize(context.Context, FocusedWorkoutRequest, []byte) (int32, error) {
	s.sizes++
	return s.artifactSize, s.sizeErr
}

func (s *workoutEditRouteStore) SaveFocusedEdit(_ context.Context, reviewed focusedEditReviewed) (FocusedWorkoutSaveResult, error) {
	s.writes++
	s.saved = reviewed
	return FocusedWorkoutSaveResult{SessionID: reviewed.Payload.Original.SessionID}, s.failure
}

func (s *workoutEditRouteStore) CancelFocusedEdit(_ context.Context, request FocusedWorkoutEditRequest, expected time.Time, reason string) (FocusedWorkoutSaveResult, error) {
	s.writes++
	s.cancelRequest, s.cancelExpected, s.cancelReason = request, expected, reason
	return FocusedWorkoutSaveResult{SessionID: request.SessionID}, s.failure
}

func newWorkoutEditRouteStore(t *testing.T) *workoutEditRouteStore {
	t.Helper()
	return &workoutEditRouteStore{focusedEditHTTPStore: focusedEditHTTPStore{source: focusedEditFixture(t)}, artifactSize: 1}
}

func workoutEditNativeRoutes(store *workoutEditRouteStore) (StructuredWorkout, http.Handler, http.Handler) {
	h := StructuredWorkout{Store: store, HTTPWriteTimeout: 30 * time.Second}
	mux := http.NewServeMux()
	const base = "/admin/treinos/estruturados/semanas/{plan}/treinos/{id}"
	mux.HandleFunc("GET "+base+"/editar", h.Edit)
	mux.HandleFunc("POST "+base+"/editar/rascunho", h.EditDraft)
	mux.HandleFunc("POST "+base+"/editar/prever", h.EditPreview)
	mux.HandleFunc("POST "+base+"/editar/confirmar", h.EditConfirm)
	mux.HandleFunc("GET "+base+"/cancelar", h.CancelEdit)
	mux.HandleFunc("POST "+base+"/cancelar", h.CancelEdit)
	withUser := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: store.source.Context.ActorID})))
	})
	return h, httpx.PublicationIngressMiddleware(30 * time.Second)(withUser), withUser
}

func workoutEditNativeInput(t *testing.T, h StructuredWorkout, source focusedEditSource) url.Values {
	t.Helper()
	draft, err := focusedEditLosslessDraft(source)
	if err != nil {
		t.Fatal(err)
	}
	page := h.editPage(httptest.NewRequest(http.MethodGet, "/", nil), CurrentUser{ID: source.Context.ActorID}, source, draft)
	return focusedRenderedEditor(t, page)
}

func workoutEditNativeConfirm(t *testing.T, body string) url.Values {
	t.Helper()
	values := url.Values{}
	for _, field := range []string{"intent", "proposal", "review_token"} {
		matches := regexp.MustCompile(`name="` + field + `" value="([^"]+)"`).FindStringSubmatch(body)
		if len(matches) != 2 {
			t.Fatalf("native confirmation is missing %q", field)
		}
		values.Set(field, html.UnescapeString(matches[1]))
	}
	return values
}

func workoutEditNativeGET(handler http.Handler, path string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	return response
}

func workoutEditMalformedPOST(handler http.Handler, path string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader("command=%zz"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	handler.ServeHTTP(response, r)
	return response
}

func TestWorkoutEditNativeDraftAndPreviewStayNonMutating(t *testing.T) {
	const own = "Instruções revistas para a sessão."
	for _, tc := range []struct {
		name, action string
		change       func(*workoutEditRouteStore, url.Values)
		status       int
		previewReads int
		sizes        int
	}{
		{"changed own source", "/prever", func(_ *workoutEditRouteStore, values url.Values) {
			values.Set("source_digest", strings.Repeat("a", 64))
		}, 409, 0, 0},
		{"duplicate activity order key", "/prever", func(_ *workoutEditRouteStore, values url.Values) {
			values.Add("activity_order", values.Get("activity_order"))
		}, 422, 0, 0},
		{"closed plan epoch", "/rascunho", func(_ *workoutEditRouteStore, values url.Values) { values.Set("state_epoch", "9999") }, 409, 0, 0},
		{"draft command invalid", "/rascunho", func(_ *workoutEditRouteStore, values url.Values) { values.Set("command", "invalid") }, 422, 0, 0},
		{"draft command permitted", "/rascunho", func(_ *workoutEditRouteStore, values url.Values) { values.Set("command", "phase_add:a_1") }, 200, 0, 0},
		{"preview command invalid", "/prever", func(_ *workoutEditRouteStore, values url.Values) { values.Set("command", "edit") }, 422, 0, 0},
		{"incomplete title", "/prever", func(_ *workoutEditRouteStore, values url.Values) { values.Set("title", "") }, 422, 0, 0},
		{"preview access revoked", "/prever", func(s *workoutEditRouteStore, _ url.Values) { s.previewErr = pgx.ErrNoRows }, 404, 1, 0},
		{"source changed during preview", "/prever", func(s *workoutEditRouteStore, _ url.Values) {
			current := s.source
			current.Raw.Session.UpdatedAt.Time = current.Raw.Session.UpdatedAt.Time.Add(time.Microsecond)
			s.previewSource = &current
		}, 409, 1, 0},
		{"artifact size unavailable", "/prever", func(s *workoutEditRouteStore, _ url.Values) { s.sizeErr = errors.New("synthetic storage error") }, 503, 1, 1},
		{"oversized artifact", "/prever", func(s *workoutEditRouteStore, _ url.Values) { s.artifactSize = focusedWorkoutMaxArtifactBytes + 1 }, 422, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newWorkoutEditRouteStore(t)
			h, routes, _ := workoutEditNativeRoutes(store)
			base := focusedWorkoutEditURL(store.source.Context.PlanID.String(), store.source.Graph.SessionID.String())
			fields := workoutEditNativeInput(t, h, store.source)
			fields.Set("command", "preview")
			fields.Set("description", own)
			tc.change(store, fields)
			result := publicationPost(t, routes, base+tc.action, fields)
			body := result.Body.String()
			if result.Code != tc.status || store.writes != 0 || store.previewReads != tc.previewReads || store.sizes != tc.sizes {
				t.Fatalf("status=%d writes=%d preview=%d size=%d", result.Code, store.writes, store.previewReads, store.sizes)
			}
			if strings.Contains(body, `name="intent" value="confirm"`) || strings.Contains(body, own) != (tc.status != 404) {
				t.Fatal("draft recovery either disclosed revoked content or offered confirmation")
			}
		})
	}
}

func TestWorkoutEditNativeConfirmChecksFormAndFreshAccess(t *testing.T) {
	const own = "Revisão privada do treinador."
	for _, tc := range []struct {
		name, responseText string
		change             func(*workoutEditRouteStore, url.Values)
		status, writes     int
		containsOwn        bool
	}{
		{"confirmed native form", "", func(_ *workoutEditRouteStore, _ url.Values) {}, 303, 1, false},
		{"missing proof", "", func(_ *workoutEditRouteStore, f url.Values) { f.Del("proposal"); f.Del("review_token") }, 422, 0, false},
		{"duplicate intent", "", func(_ *workoutEditRouteStore, f url.Values) { f.Add("intent", "confirm") }, 422, 0, true},
		{"wrong intent", "", func(_ *workoutEditRouteStore, f url.Values) { f.Set("intent", "preview") }, 422, 0, true},
		{"uncertain commit", "Não foi possível confirmar o resultado", func(s *workoutEditRouteStore, _ url.Values) {
			s.failure = db.ErrCommitOutcomeUnknown
		}, 409, 1, true},
		{"authority revoked after failed write", "", func(s *workoutEditRouteStore, _ url.Values) {
			s.failure, s.afterWriteErr = errFocusedWorkoutConflict, pgx.ErrNoRows
		}, 404, 1, false},
		{"failed authorized reread", "", func(s *workoutEditRouteStore, _ url.Values) {
			s.failure, s.afterWriteErr = errFocusedWorkoutConflict, errors.New("synthetic access read failure")
		}, 500, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newWorkoutEditRouteStore(t)
			h, routes, _ := workoutEditNativeRoutes(store)
			source := store.source
			base := focusedWorkoutEditURL(source.Context.PlanID.String(), source.Graph.SessionID.String())
			fields := workoutEditNativeInput(t, h, source)
			fields.Set("command", "preview")
			fields.Set("description", own)
			preview := publicationPost(t, routes, base+"/prever", fields)
			if preview.Code != 200 || store.writes != 0 || store.sizes != 1 {
				t.Fatalf("preview status=%d writes=%d artifact sizing=%d", preview.Code, store.writes, store.sizes)
			}
			confirm := workoutEditNativeConfirm(t, preview.Body.String())
			tc.change(store, confirm)
			result := publicationPost(t, routes, base+"/confirmar", confirm)
			body := result.Body.String()
			if result.Code != tc.status || store.writes != tc.writes || strings.Contains(body, own) != tc.containsOwn || !strings.Contains(body, tc.responseText) {
				t.Fatalf("status=%d writes=%d owns-content=%t", result.Code, store.writes, strings.Contains(body, own))
			}
			if result.Code == http.StatusSeeOther {
				want := structuredPlannerURL(source.Context.GroupID.String(), source.Context.PlanID.String(), source.Graph.SessionID.String(), source.Context.Day)
				if result.Header().Get("Location") != want || !store.saved.verified || store.saved.Payload.Proposal.Description != own {
					t.Fatal("confirmation lost verified changes or the original return context")
				}
			}
			if errors.Is(store.failure, db.ErrCommitOutcomeUnknown) && (store.reads != 2 || strings.Contains(body, `name="intent" value="confirm"`)) {
				t.Fatal("unknown outcome offered a second attempt or made a new scope read")
			}
		})
	}
}

func TestWorkoutEditNativeRoutesRefuseOutOfScopeAndUnadmittedRequests(t *testing.T) {
	for _, suffix := range []string{"/editar", "/cancelar"} {
		t.Run(suffix, func(t *testing.T) {
			store := newWorkoutEditRouteStore(t)
			_, routes, withoutIngress := workoutEditNativeRoutes(store)
			source := store.source
			plan, session := source.Context.PlanID.String(), source.Graph.SessionID.String()
			base := structuredPlannerPath + "/semanas/" + plan + "/treinos/" + session + suffix
			write := base
			if suffix == "/editar" {
				write += "/prever"
			}
			badPath := strings.Replace(base, session, "missing-id", 1)
			if result := workoutEditNativeGET(routes, badPath); result.Code != 404 || store.reads != 0 {
				t.Fatalf("invalid route id queried store: %d reads=%d", result.Code, store.reads)
			}
			if result := publicationPost(t, withoutIngress, write, nil); result.Code != 403 || store.reads != 0 {
				t.Fatalf("missing request deadline queried store: %d reads=%d", result.Code, store.reads)
			}
			if result := workoutEditMalformedPOST(routes, write); result.Code != 403 || store.writes != 0 {
				t.Fatalf("malformed form attempted write: %d writes=%d", result.Code, store.writes)
			}
			for _, tc := range []struct {
				err  error
				code int
			}{{pgx.ErrNoRows, 404}, {errors.New("synthetic unavailable read"), 500}} {
				store.readErr = tc.err
				result := workoutEditNativeGET(routes, base)
				if result.Code != tc.code || strings.Contains(result.Body.String(), source.Raw.Session.Title) {
					t.Fatalf("failed scoped read returned workout content: %d", result.Code)
				}
				if result.Header().Get("Cache-Control") != "private, no-store" || result.Header().Get("Referrer-Policy") != "no-referrer" {
					t.Fatal("failed scoped read lacked private response headers")
				}
			}
			store.readErr = nil
			store.source.DecisionAt = *source.Graph.Schedule.StartsAt
			result := workoutEditNativeGET(routes, base)
			body := result.Body.String()
			if result.Code != 200 || !strings.Contains(body, "disponível para consulta") || strings.Contains(body, `value="preview"`) || strings.Contains(body, `name="confirm_cancellation"`) {
				t.Fatalf("started workout offered write controls: %d", result.Code)
			}
			if suffix == "/editar" {
				result := publicationPost(t, routes, write, nil)
				if result.Code != 409 || store.previewReads != 0 || store.writes != 0 {
					t.Fatalf("started workout advanced edit preview: %d", result.Code)
				}
			}
		})
	}
}

func TestWorkoutEditNativeCancellationValidatesAndContainsFailedWrites(t *testing.T) {
	const reason = "Condições meteorológicas para revisão."
	for _, tc := range []struct {
		name, message string
		change        func(*workoutEditRouteStore, url.Values)
		status        int
		writes        int
		containsOwn   bool
	}{
		{"confirmed", "", func(_ *workoutEditRouteStore, _ url.Values) {}, 303, 1, false},
		{"missing reason and assent", "Confirme que pretende cancelar a sessão.", func(_ *workoutEditRouteStore, f url.Values) {
			f.Set("cancellation_reason", "x")
			f.Del("confirm_cancellation")
		}, 422, 0, false},
		{"invalid version", "formulário deixou de ser válido", func(_ *workoutEditRouteStore, f url.Values) { f.Set("expected_updated_at", "tomorrow") }, 422, 0, true},
		{"duplicate assent", "Pedido recusado", func(_ *workoutEditRouteStore, f url.Values) { f.Add("confirm_cancellation", "yes") }, 403, 0, false},
		{"uncertain commit", "Não foi possível confirmar o resultado", func(s *workoutEditRouteStore, _ url.Values) {
			s.failure = db.ErrCommitOutcomeUnknown
		}, 409, 1, true},
		{"stale cancellation", "O treino mudou", func(s *workoutEditRouteStore, _ url.Values) {
			s.failure = errFocusedWorkoutConflict
		}, 409, 1, true},
		{"authority revoked after failed cancellation", "", func(s *workoutEditRouteStore, _ url.Values) {
			s.failure, s.afterWriteErr = errFocusedWorkoutConflict, pgx.ErrNoRows
		}, 404, 1, false},
		{"unavailable cancellation recovery", "", func(s *workoutEditRouteStore, _ url.Values) {
			s.failure, s.afterWriteErr = errFocusedWorkoutConflict, errors.New("synthetic access read failure")
		}, 500, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newWorkoutEditRouteStore(t)
			_, routes, _ := workoutEditNativeRoutes(store)
			source := store.source
			base := focusedWorkoutCancelURL(source.Context.PlanID.String(), source.Graph.SessionID.String())
			initial := workoutEditNativeGET(routes, base)
			stamp := regexp.MustCompile(`name="expected_updated_at" value="([^"]+)"`).FindStringSubmatch(initial.Body.String())
			if initial.Code != 200 || len(stamp) != 2 || !strings.Contains(initial.Body.String(), `name="confirm_cancellation"`) {
				t.Fatalf("eligible cancellation form missing: %d", initial.Code)
			}
			form := url.Values{"expected_updated_at": {stamp[1]}, "cancellation_reason": {" " + reason + " "}, "confirm_cancellation": {"yes"}}
			tc.change(store, form)
			result := publicationPost(t, routes, base, form)
			body := result.Body.String()
			if result.Code != tc.status || store.writes != tc.writes || strings.Contains(body, reason) != tc.containsOwn || !strings.Contains(body, tc.message) {
				t.Fatalf("status=%d writes=%d retains-reason=%t", result.Code, store.writes, strings.Contains(body, reason))
			}
			if tc.name == "missing reason and assent" && !strings.Contains(body, "O motivo deve ter entre 2 e 500 caracteres.") {
				t.Fatal("invalid cancellation reason had no field error")
			}
			if result.Code == http.StatusSeeOther {
				wantURL := structuredPlannerURL(source.Context.GroupID.String(), source.Context.PlanID.String(), source.Graph.SessionID.String(), source.Context.Day)
				wantRequest := FocusedWorkoutEditRequest{source.Context.ActorID, source.Context.PlanID, source.Graph.SessionID}
				if result.Header().Get("Location") != wantURL || store.cancelRequest != wantRequest || !store.cancelExpected.Equal(source.Raw.Session.UpdatedAt.Time) || store.cancelReason != reason {
					t.Fatal("cancellation lost scoped request, revision, reason, or return context")
				}
			}
			if errors.Is(store.failure, db.ErrCommitOutcomeUnknown) && (store.reads != 2 || strings.Contains(body, `name="confirm_cancellation"`)) {
				t.Fatal("uncertain cancellation offered another submission or new scoped read")
			}
		})
	}
}
