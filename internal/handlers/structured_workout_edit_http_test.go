package handlers

import (
	"context"
	"errors"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type focusedEditHTTPStore struct {
	focusedRecoveryStore
	source    focusedEditSource
	afterSave *focusedEditSource
	failure   error
	revoked   bool
	writes    int
}

func (s *focusedEditHTTPStore) LoadFocusedEdit(context.Context, FocusedWorkoutEditRequest) (focusedEditSource, error) {
	if s.revoked {
		return focusedEditSource{}, pgx.ErrNoRows
	}
	if s.writes > 0 && s.afterSave != nil {
		return *s.afterSave, nil
	}
	return s.source, nil
}
func (s *focusedEditHTTPStore) LoadFocusedEditSession(context.Context, FocusedWorkoutEditRequest) (focusedEditSource, error) {
	if s.revoked {
		return focusedEditSource{}, pgx.ErrNoRows
	}
	return s.source, nil
}
func (s *focusedEditHTTPStore) ReadFocusedEditPreview(context.Context, FocusedWorkoutEditRequest, []uuid.UUID) (focusedEditSource, error) {
	return s.source, nil
}
func (s *focusedEditHTTPStore) SaveFocusedEdit(context.Context, focusedEditReviewed) (FocusedWorkoutSaveResult, error) {
	s.writes++
	return FocusedWorkoutSaveResult{}, s.failure
}
func (s *focusedEditHTTPStore) CancelFocusedEdit(context.Context, FocusedWorkoutEditRequest, time.Time, string) (FocusedWorkoutSaveResult, error) {
	s.writes++
	return FocusedWorkoutSaveResult{}, s.failure
}

// Real native routes/ingress/render with only persistence simulated: the final
// store clock rejection cannot hide an authorized user's own unsaved content.
func TestFocusedEditPostCutoffRecoveryRetainsOwnGraphAndOriginalProof(t *testing.T) {
	source := focusedEditFixture(t)
	d, err := focusedEditLosslessDraft(source)
	if err != nil {
		t.Fatal(err)
	}
	own := "Texto próprio\n\tpara copiar antes de recarregar."
	d.Values.Set("description", own)
	proposal, e := normalizeFocusedEdit(source, d)
	if len(e) > 0 {
		t.Fatal(e)
	}
	if err = allocateFocusedEditIDs(&proposal, source.Graph, nil); err != nil {
		t.Fatal(err)
	}
	proposal.References = source.Graph.References
	baseline, _ := focusedEditBaselineFor(source)
	payload := focusedEditReviewPayload{baseline, proposal, focusedGraphValues(d)}
	raw, token, err := focusedWorkoutReviews.signEdit(payload, source, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, failure := range []error{errFocusedEditReadOnly, errFocusedEditSourceChanged} {
		store := &focusedEditHTTPStore{source: source, failure: failure}
		h := StructuredWorkout{Store: store, HTTPWriteTimeout: 30 * time.Second}
		base := focusedWorkoutEditURL(source.Context.PlanID.String(), source.Graph.SessionID.String())
		mux := http.NewServeMux()
		mux.HandleFunc("POST /admin/treinos/estruturados/semanas/{plan}/treinos/{id}/editar/confirmar", h.EditConfirm)
		wrapped := httpx.PublicationIngressMiddleware(30 * time.Second)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: source.Context.ActorID}))
			mux.ServeHTTP(w, r)
		}))
		response := publicationPost(t, wrapped, base+"/confirmar", url.Values{"intent": {"confirm"}, "proposal": {raw}, "review_token": {token}})
		body := response.Body.String()
		if response.Code != 409 || !strings.Contains(body, `<textarea id="workout-description"`) || !strings.Contains(html.UnescapeString(body), own) || !strings.Contains(body, `href="`+base+`"`) {
			t.Fatal("lost visible authorized own input/reload", response.Code)
		}
		if regexp.MustCompile(`<button\b[^>]*value="preview"`).MatchString(body) || strings.Contains(body, `name="intent" value="confirm"`) {
			t.Fatal("final cutoff offered futile overwrite")
		}
		if !strings.Contains(body, html.EscapeString(raw)) || !strings.Contains(body, html.EscapeString(token)) || store.writes != 1 {
			t.Fatal("lost original proof")
		}
		store.revoked = true
		response = publicationPost(t, wrapped, base+"/confirmar", url.Values{"intent": {"confirm"}, "proposal": {raw}, "review_token": {token}})
		if response.Code != 404 || strings.Contains(response.Body.String(), own) || strings.Contains(response.Body.String(), token) {
			t.Fatal("revoked request exposed retained draft", response.Code)
		}
	}
}
func TestFocusedEditInvalidPriorProofCannotRefreshThroughDraftCommands(t *testing.T) {
	source := focusedEditFixture(t)
	d, err := focusedEditLosslessDraft(source)
	if err != nil {
		t.Fatal(err)
	}
	d.Values.Set("description", "Instruções próprias recuperáveis.")
	proposal, e := normalizeFocusedEdit(source, d)
	if len(e) > 0 {
		t.Fatal(e)
	}
	_ = allocateFocusedEditIDs(&proposal, source.Graph, nil)
	proposal.References = source.Graph.References
	baseline, _ := focusedEditBaselineFor(source)
	for _, bad := range []string{"expiry", "restart"} {
		signer, issued := focusedWorkoutReviews, time.Now()
		if bad == "expiry" {
			issued = issued.Add(-focusedWorkoutReviewTTL - time.Second)
		} else {
			signer.key[0] ^= 1
		}
		raw, token, err := signer.signEdit(focusedEditReviewPayload{baseline, proposal, focusedGraphValues(d)}, source, issued)
		if err != nil {
			t.Fatal(err)
		}
		store := &focusedEditHTTPStore{source: source, failure: errors.New("unexpected save")}
		h := StructuredWorkout{Store: store, HTTPWriteTimeout: 30 * time.Second}
		mux := http.NewServeMux()
		mux.HandleFunc("POST /admin/treinos/estruturados/semanas/{plan}/treinos/{id}/editar/rascunho", h.EditDraft)
		wrapped := httpx.PublicationIngressMiddleware(30 * time.Second)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: source.Context.ActorID}))
			mux.ServeHTTP(w, r)
		}))
		values := focusedGraphValues(d)
		for k, v := range focusedEditMetadata(focusedEditReviewPayload{Original: baseline, Proposal: proposal}) {
			values.Set(k, v)
		}
		values.Set("command", "phase_add:a_1")
		values.Set("prior_proposal", raw)
		values.Set("prior_review_token", token)
		result := publicationPost(t, wrapped, focusedWorkoutEditURL(source.Context.PlanID.String(), source.Graph.SessionID.String())+"/rascunho", values)
		if result.Code != 422 || regexp.MustCompile(`<button\b[^>]*value="preview"`).MatchString(result.Body.String()) || !strings.Contains(result.Body.String(), "Instruções próprias recuperáveis.") || store.writes != 0 {
			t.Fatal(bad, "unusable proof renewed/lost draft", result.Code)
		}
	}
}

func TestFocusedEditPostReadRaceRequiresFreshOwnProofBeforeRepreview(t *testing.T) {
	source := focusedEditFixture(t)
	draft, err := focusedEditLosslessDraft(source)
	if err != nil {
		t.Fatal(err)
	}
	own := "Texto próprio conservado durante a concorrência."
	draft.Values.Set("description", own)
	proposal, e := normalizeFocusedEdit(source, draft)
	if len(e) > 0 {
		t.Fatal(e)
	}
	_ = allocateFocusedEditIDs(&proposal, source.Graph, nil)
	proposal.References = source.Graph.References
	baseline, _ := focusedEditBaselineFor(source)
	raw, token, err := focusedWorkoutReviews.signEdit(focusedEditReviewPayload{baseline, proposal, focusedGraphValues(draft)}, source, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, caseName := range []string{"plan-only", "own-source", "serialization", "nowait", "cutoff"} {
		t.Run(caseName, func(t *testing.T) {
			current := source
			current.Context.PlanUpdatedAt = current.Context.PlanUpdatedAt.Add(time.Second)
			failure := errFocusedWorkoutConflict
			switch caseName {
			case "own-source", "serialization", "nowait":
				current.Raw.Session.Title = "Uma alteração concorrente depois da leitura"
				current.Raw.Session.UpdatedAt.Time = current.Raw.Session.UpdatedAt.Time.Add(time.Microsecond)
			case "cutoff":
				current.DecisionAt = *current.Graph.Schedule.StartsAt
			}
			if caseName == "serialization" {
				failure = publicationTransactionError(db.SessionTxResult{Outcome: db.SessionTxNotCommitted}, &pgconn.PgError{Code: "40001"})
			}
			if caseName == "nowait" {
				failure = publicationNowaitError(&pgconn.PgError{Code: "55P03"})
			}
			store := &focusedEditHTTPStore{source: source, afterSave: &current, failure: failure}
			h := StructuredWorkout{Store: store, HTTPWriteTimeout: 30 * time.Second}
			mux := http.NewServeMux()
			mux.HandleFunc("POST /admin/treinos/estruturados/semanas/{plan}/treinos/{id}/editar/confirmar", h.EditConfirm)
			wrapped := httpx.PublicationIngressMiddleware(30 * time.Second)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: source.Context.ActorID}))
				mux.ServeHTTP(w, r)
			}))
			result := publicationPost(t, wrapped, focusedWorkoutEditURL(source.Context.PlanID.String(), source.Graph.SessionID.String())+"/confirmar", url.Values{"intent": {"confirm"}, "proposal": {raw}, "review_token": {token}})
			body := result.Body.String()
			if result.Code != 409 || !strings.Contains(body, own) || !strings.Contains(body, html.EscapeString(raw)) {
				t.Fatal("lost safe own input/proof", result.Code)
			}
			hasPreview := regexp.MustCompile(`<button\b[^>]*value="preview"`).MatchString(body)
			if hasPreview != (caseName == "plan-only") {
				t.Fatal("offered repreview without fresh unchanged own proof", caseName, hasPreview)
			}
		})
	}
}
