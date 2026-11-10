package handlers

import (
	"context"
	"errors"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type workoutNativeStore struct {
	focusedRecoveryStore
	loadErr, previewErr, sizeErr, saveErr error
	previewContext                        *FocusedWorkoutContext
	previewCalls                          int
	artifactSize                          int32
	saved                                 focusedGraphReviewed
}

func (s *workoutNativeStore) LoadFocusedWorkout(context.Context, FocusedWorkoutRequest) (FocusedWorkoutContext, error) {
	return s.source, s.loadErr
}

func (s *workoutNativeStore) ReadFocusedGraphPreview(context.Context, FocusedWorkoutRequest, []uuid.UUID) (focusedGraphPreviewSource, error) {
	s.previewCalls++
	current := s.source
	if s.previewContext != nil {
		current = *s.previewContext
	}
	return focusedGraphPreviewSource{Context: current, References: focusedGraphProfileFacts{}}, s.previewErr
}

func (s *workoutNativeStore) FocusedWorkoutArtifactSize(context.Context, FocusedWorkoutRequest, []byte) (int32, error) {
	s.sizes++
	return s.artifactSize, s.sizeErr
}

func (s *workoutNativeStore) SaveFocusedGraph(_ context.Context, reviewed focusedGraphReviewed) (FocusedWorkoutSaveResult, error) {
	s.saves++
	s.saved = reviewed
	return FocusedWorkoutSaveResult{SessionID: reviewed.Proposal.SessionID}, s.saveErr
}

func workoutNativeFixture(t *testing.T) (*workoutNativeStore, http.Handler, string, url.Values) {
	t.Helper()
	c, v := focusedGraphWaterExample()
	for key, value := range map[string]string{
		"group_id": c.GroupID.String(), "plan_id": c.PlanID.String(), "day": c.Day,
		"expected_plan_updated_at": c.PlanUpdatedAt.Format(time.RFC3339Nano), "source_digest": c.Digest(),
		"database_incarnation": c.DatabaseIncarnation.String(), "state_epoch": strconv.FormatInt(c.StateEpoch, 10),
		"command": "preview",
	} {
		v.Set(key, value)
	}
	store := &workoutNativeStore{focusedRecoveryStore: focusedRecoveryStore{source: c}, artifactSize: 1}
	h := StructuredWorkout{Store: store, HTTPWriteTimeout: 30 * time.Second}
	base := "/admin/treinos/estruturados/semanas/" + c.PlanID.String() + "/treinos/criar"
	mux := http.NewServeMux()
	for suffix, handler := range map[string]http.HandlerFunc{"/prever": h.Preview, "/rascunho": h.Draft, "/confirmar": h.Confirm} {
		mux.HandleFunc("POST "+base+suffix, func(w http.ResponseWriter, r *http.Request) {
			r.SetPathValue("id", c.PlanID.String())
			handler(w, r)
		})
	}
	native := httpx.PublicationIngressMiddleware(30 * time.Second)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: c.ActorID}))
		mux.ServeHTTP(w, r)
	}))
	return store, native, base, v
}

func TestWorkoutNativePreviewRejectsInvalidOrStaleFactsBeforeSave(t *testing.T) {
	fail := errors.New("synthetic storage failure")
	for _, tc := range []struct {
		name                    string
		change                  func(*workoutNativeStore, url.Values)
		status, previews, sizes int
	}{
		{"invalid command", func(_ *workoutNativeStore, v url.Values) { v.Set("command", "save") }, 422, 0, 0},
		{"malformed draft", func(_ *workoutNativeStore, v url.Values) { v.Set("extra_field", "unsupported") }, 422, 0, 0},
		{"normalization failure", func(_ *workoutNativeStore, v url.Values) { v.Set("title", "") }, 422, 0, 0},
		{"closed at load", func(s *workoutNativeStore, _ url.Values) { s.source.AuthoringEnabled = false }, 409, 0, 0},
		{"revoked at scoped reread", func(s *workoutNativeStore, _ url.Values) { s.previewErr = pgx.ErrNoRows }, 404, 1, 0},
		{"scoped read failed", func(s *workoutNativeStore, _ url.Values) { s.previewErr = fail }, 503, 1, 0},
		{"closed at scoped reread", func(s *workoutNativeStore, _ url.Values) {
			current := s.source
			current.AuthoringEnabled = false
			s.previewContext = &current
		}, 409, 1, 0},
		{"changed database incarnation", func(s *workoutNativeStore, _ url.Values) {
			current := s.source
			current.DatabaseIncarnation = uuid.New()
			s.previewContext = &current
		}, 422, 1, 0},
		{"revoked before artifact read", func(s *workoutNativeStore, _ url.Values) { s.sizeErr = pgx.ErrNoRows }, 404, 1, 1},
		{"artifact read failed", func(s *workoutNativeStore, _ url.Values) { s.sizeErr = fail }, 503, 1, 1},
		{"artifact exceeds limit", func(s *workoutNativeStore, _ url.Values) { s.artifactSize = focusedWorkoutMaxArtifactBytes + 1 }, 422, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, handler, path, values := workoutNativeFixture(t)
			tc.change(store, values)
			response := publicationPost(t, handler, path+"/prever", values)
			if response.Code != tc.status || store.previewCalls != tc.previews || store.sizes != tc.sizes || store.saves != 0 ||
				strings.Contains(response.Body.String(), `name="intent" value="confirm"`) {
				t.Fatalf("status %d, reads %d, sizes %d, writes %d", response.Code, store.previewCalls, store.sizes, store.saves)
			}
		})
	}
}

func TestWorkoutNativeConfirmUsesRenderedProofAndContainsSaveOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
	}{
		{"save", nil, 303}, {"access revoked", pgx.ErrNoRows, 404},
		{"uncertain commit", db.ErrCommitOutcomeUnknown, 409},
		{"invalid proof at save", errFocusedWorkoutInvalid, 422},
		{"concurrent version", errFocusedWorkoutConflict, 409},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, handler, path, values := workoutNativeFixture(t)
			preview := publicationPost(t, handler, path+"/prever", values)
			if preview.Code != 200 || store.saves != 0 {
				t.Fatalf("preview returned %d with %d writes", preview.Code, store.saves)
			}
			confirm := url.Values{"intent": {"confirm"}}
			for _, key := range []string{"proposal", "review_token"} {
				pattern := regexp.MustCompile(`name="` + key + `"\s+value="([^"]+)"`)
				match := pattern.FindStringSubmatch(preview.Body.String())
				if len(match) != 2 {
					t.Fatalf("preview did not render %s", key)
				}
				confirm.Set(key, html.UnescapeString(match[1]))
			}
			store.saveErr = tc.err
			response := publicationPost(t, handler, path+"/confirmar", confirm)
			if response.Code != tc.status || store.saves != 1 || !store.saved.verified {
				t.Fatalf("confirmation returned %d, writes %d, verified %t", response.Code, store.saves, store.saved.verified)
			}
			if tc.err == nil && !strings.Contains(response.Header().Get("Location"), store.saved.Proposal.SessionID.String()) {
				t.Fatalf("successful confirmation did not link to saved workout: %q", response.Header().Get("Location"))
			}
			if tc.err != nil && (response.Header().Get("Location") != "" || strings.Contains(response.Body.String(), `name="intent" value="confirm"`)) {
				t.Fatal("failed confirmation redirected or offered a second confirmation")
			}
		})
	}
}
