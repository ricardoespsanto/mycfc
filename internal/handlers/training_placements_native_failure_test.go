package handlers

import (
	"context"
	"errors"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const privatePlacementPerson = "Pessoa privada sintética"
const privatePlacementGroup = "Grupo de contexto privado"

func postPlacementNativePreview(t *testing.T, store *placementHTTPStoreFake, handler http.Handler) url.Values {
	t.Helper()
	result := publicationPost(t, handler, trainingPlacementsPath+"/prever", store.command.values())
	if result.Code != http.StatusOK || store.writes != 0 || !strings.Contains(result.Body.String(), ">Confirmar alteração<") {
		t.Fatalf("valid preview status=%d writes=%d", result.Code, store.writes)
	}
	return placementHTTPProof(t, result.Body.String())
}

func TestPlacementNativeRoutesRequireActorAndAdmittedForm(t *testing.T) {
	for _, route := range []struct {
		name, method, suffix string
		handle               func(TrainingPlacements) http.HandlerFunc
	}{
		{"timeline", http.MethodGet, "", func(h TrainingPlacements) http.HandlerFunc { return h.Index }},
		{"preview", http.MethodPost, "/prever", func(h TrainingPlacements) http.HandlerFunc { return h.Preview }},
		{"confirm", http.MethodPost, "/confirmar", func(h TrainingPlacements) http.HandlerFunc { return h.Confirm }},
		{"editor", http.MethodPost, "", func(h TrainingPlacements) http.HandlerFunc { return h.Editor }},
	} {
		t.Run(route.name, func(t *testing.T) {
			store, _ := placementHTTPFixture(t)
			h := TrainingPlacements{Store: store, HTTPWriteTimeout: 30 * time.Second}
			handler := route.handle(h)
			path := trainingPlacementsPath + route.suffix
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(route.method, path, nil))
			if response.Code != http.StatusNotFound || response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Referrer-Policy") != "no-referrer" {
				t.Fatalf("anonymous status or privacy headers: %d", response.Code)
			}
			if route.method == http.MethodPost {
				withUser := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					handler(w, r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: store.actor})))
				})
				if result := publicationPost(t, withUser, path, store.command.values()); result.Code != http.StatusForbidden {
					t.Fatalf("POST with no ingress deadline was admitted: %d", result.Code)
				}
				malformed := httptest.NewRequest(http.MethodPost, path, strings.NewReader("person_id=%zz"))
				malformed.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				result := httptest.NewRecorder()
				httpx.PublicationIngressMiddleware(30*time.Second)(withUser).ServeHTTP(result, malformed)
				if result.Code != http.StatusForbidden {
					t.Fatalf("malformed form was admitted: %d", result.Code)
				}
			}
			if store.contextCalls+store.viewCalls+store.readCalls+store.confirmCalls+store.writes != 0 {
				t.Fatal("rejected request accessed placement storage")
			}
		})
	}
}

func TestPlacementNativePreviewKeepsOnlyAuthorizedEditableContext(t *testing.T) {
	for _, tc := range []struct {
		name, wantText           string
		change                   func(*placementHTTPStoreFake, url.Values)
		status, viewCalls, reads int
		retainsPerson            bool
	}{
		{"invalid person", "", func(_ *placementHTTPStoreFake, v url.Values) { v.Set("person_id", "invalid") }, 404, 0, 0, false},
		{"group access denied", "", func(s *placementHTTPStoreFake, _ url.Values) { s.contextErr = pgx.ErrNoRows }, 404, 0, 0, false},
		{"group lookup failed", "Não foi possível concluir o pedido", func(s *placementHTTPStoreFake, _ url.Values) {
			s.contextErr = errors.New("synthetic group lookup error")
		}, 500, 0, 0, false},
		{"unconfigured season", "A época selecionada não está disponível", func(s *placementHTTPStoreFake, v url.Values) {
			v.Set("season_id", uuid.NewString())
			s.viewErr = errTrainingPlacementInvalid
		}, 422, 1, 0, false},
		{"missing destination for valid timeline", "Selecione o grupo de destino.", func(_ *placementHTTPStoreFake, v url.Values) { v.Del("target_group_id") }, 422, 1, 0, true},
		{"source authority denied", "", func(s *placementHTTPStoreFake, _ url.Values) {
			s.q.readErrors = map[string]error{"source": pgx.ErrNoRows}
		}, 404, 0, 1, false},
		{"placement backend unavailable", "temporariamente indisponível", func(s *placementHTTPStoreFake, _ url.Values) {
			s.q.readErrors = map[string]error{"source": errTrainingPlacementUnavailable}
		}, 503, 0, 1, false},
		{"return eligibility cannot be confirmed", "Não foi possível confirmar o regresso ao grupo anterior", func(s *placementHTTPStoreFake, _ url.Values) { s.q.returns = nil }, 422, 0, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, handler := placementHTTPFixture(t)
			fields := store.command.values()
			tc.change(store, fields)
			result := publicationPost(t, handler, trainingPlacementsPath+"/prever", fields)
			body := result.Body.String()
			if result.Code != tc.status || store.viewCalls != tc.viewCalls || store.readCalls != tc.reads || store.writes != 0 || store.confirmCalls != 0 {
				t.Fatalf("status=%d views=%d reads=%d confirmations=%d writes=%d", result.Code, store.viewCalls, store.readCalls, store.confirmCalls, store.writes)
			}
			if !strings.Contains(body, tc.wantText) || strings.Contains(body, privatePlacementPerson) != tc.retainsPerson || strings.Contains(body, ">Confirmar alteração<") {
				t.Fatal("failed preview exposed a private timeline, hid its field feedback, or offered confirmation")
			}
			if tc.status == 503 && result.Header().Get("Retry-After") != "5" {
				t.Fatal("runtime failure omitted the retry hint")
			}
		})
	}
}

func TestPlacementNativeConfirmRejectsInvalidTransportAndChangedProgramme(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*testing.T, *placementHTTPStoreFake, url.Values)
		status int
	}{
		{"duplicate proof", func(_ *testing.T, _ *placementHTTPStoreFake, v url.Values) { v.Add("proposal", v.Get("proposal")) }, 422},
		{"missing token", func(_ *testing.T, _ *placementHTTPStoreFake, v url.Values) { v.Del("review_token") }, 422},
		{"unexpected signed review shape", func(t *testing.T, _ *placementHTTPStoreFake, v url.Values) {
			raw, token, err := focusedWorkoutReviews.signNativeFields([]byte(`{"unexpected":true}`), nil, nil, trainingPlacementReviewPurpose, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			v.Set("proposal", raw)
			v.Set("review_token", token)
		}, 422},
		{"group access revoked", func(_ *testing.T, s *placementHTTPStoreFake, _ url.Values) { s.contextErr = pgx.ErrNoRows }, 404},
		{"group changed programme", func(_ *testing.T, s *placementHTTPStoreFake, _ url.Values) { s.context.ProgrammeID = uuid.New() }, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, handler := placementHTTPFixture(t)
			proof := postPlacementNativePreview(t, store, handler)
			tc.change(t, store, proof)
			result := publicationPost(t, handler, trainingPlacementsPath+"/confirmar", proof)
			body := result.Body.String()
			if result.Code != tc.status || store.confirmCalls != 0 || store.writes != 0 || strings.Contains(body, ">Confirmar alteração<") || strings.Contains(body, privatePlacementPerson) || strings.Contains(body, privatePlacementGroup) {
				t.Fatalf("invalid confirmation reached writer or disclosed context: status=%d calls=%d", result.Code, store.confirmCalls)
			}
		})
	}
}

func TestPlacementNativeConfirmRecoveryRequiresCurrentAccess(t *testing.T) {
	for _, tc := range []struct {
		name, wantText        string
		writeErr              error
		change                func(*placementHTTPStoreFake)
		status, recoveryReads int
		retainsProof          bool
	}{
		{"subject access revoked after conflict", "", errTrainingPlacementChanged, func(s *placementHTTPStoreFake) {
			s.q.readErrors = map[string]error{"subject": pgx.ErrNoRows}
		}, 404, 1, false},
		{"group access revoked after conflict", "", errTrainingPlacementChanged, func(s *placementHTTPStoreFake) {
			s.contextErrAfterConfirm = pgx.ErrNoRows
		}, 404, 1, false},
		{"unknown commit remains distinct from revoked access", "pedido não será repetido automaticamente", db.ErrCommitOutcomeUnknown, func(s *placementHTTPStoreFake) {
			s.contextErrAfterConfirm = pgx.ErrNoRows
		}, 503, 0, true},
		{"eligibility error on temporary date", "Não foi possível confirmar o regresso ao grupo anterior", errTrainingPlacementReturn, func(*placementHTTPStoreFake) {}, 422, 1, false},
		{"placement limits", "Esta alteração excede o limite", errTrainingPlacementLimit, func(*placementHTTPStoreFake) {}, 422, 1, false},
		{"review no longer valid", "Reveja os dados da alteração", errTrainingPlacementInvalid, func(*placementHTTPStoreFake) {}, 422, 1, false},
		{"unexpected write failure", "Não foi possível concluir o pedido", errors.New("synthetic internal failure"), func(*placementHTTPStoreFake) {}, 500, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, handler := placementHTTPFixture(t)
			proof := postPlacementNativePreview(t, store, handler)
			store.confirmErr = tc.writeErr
			tc.change(store)
			reads := store.readCalls
			result := publicationPost(t, handler, trainingPlacementsPath+"/confirmar", proof)
			body := result.Body.String()
			hasProof := strings.Contains(body, html.EscapeString(proof.Get("proposal"))) && strings.Contains(body, html.EscapeString(proof.Get("review_token")))
			if result.Code != tc.status || store.confirmCalls != 1 || store.writes != 0 || store.readCalls != reads+tc.recoveryReads {
				t.Fatalf("status=%d confirmations=%d writes=%d recovery reads=%d", result.Code, store.confirmCalls, store.writes, store.readCalls-reads)
			}
			if !strings.Contains(body, tc.wantText) || hasProof != tc.retainsProof || strings.Contains(body, ">Confirmar alteração<") {
				t.Fatal("unexpected recovery message, proof retention, or confirmation control")
			}
			if tc.status == 422 {
				effective := `name="effective_on" type="date" value="` + store.command.EffectiveOn + `"`
				temporary := `name="temporary_until" type="date" value="` + store.command.TemporaryUntil + `"`
				if !strings.Contains(body, ">Rever alteração<") || !strings.Contains(body, effective) || !strings.Contains(body, temporary) {
					t.Fatal("recoverable validation error lost the original dates or fresh-review action")
				}
			}
			if (tc.status != 422) && (strings.Contains(body, privatePlacementPerson) || strings.Contains(body, privatePlacementGroup)) {
				t.Fatal("failed placement disclosed cached person or group labels")
			}
		})
	}
}

func TestPlacementNativeBackRechecksOriginalProofAndLatestScope(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		change        func(*placementHTTPStoreFake, url.Values)
		status        int
		retains       bool
	}{
		{"invalid token", "A revisão não é válida", func(_ *placementHTTPStoreFake, v url.Values) { v.Set("review_token", "invalid") }, 422, false},
		{"changed command id", "A revisão não é válida", func(_ *placementHTTPStoreFake, v url.Values) { v.Set("command_id", uuid.NewString()) }, 422, false},
		{"group access revoked", "", func(s *placementHTTPStoreFake, _ url.Values) { s.contextErr = pgx.ErrNoRows }, 404, false},
		{"subject access revoked", "", func(s *placementHTTPStoreFake, _ url.Values) {
			s.q.readErrors = map[string]error{"subject": pgx.ErrNoRows}
		}, 404, false},
		{"authoritative head advanced", "O contexto mudou desde a revisão", func(s *placementHTTPStoreFake, _ url.Values) {
			s.q.subject.Revision++
			s.q.source.Revision++
			s.q.timeline[0].Revision++
		}, 200, true},
		{"latest placement could not be confirmed", "O contexto mudou. Reveja os dados", func(s *placementHTTPStoreFake, _ url.Values) {
			s.q.readErrors = map[string]error{"source": errTrainingPlacementChanged}
		}, 200, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, handler := placementHTTPFixture(t)
			proof := postPlacementNativePreview(t, store, handler)
			fields := store.command.values()
			for name, values := range proof {
				fields[name] = values
			}
			tc.change(store, fields)
			result := publicationPost(t, handler, trainingPlacementsPath, fields)
			body := result.Body.String()
			if result.Code != tc.status || store.confirmCalls != 0 || store.writes != 0 || !strings.Contains(body, tc.message) || strings.Contains(body, ">Confirmar alteração<") {
				t.Fatalf("unexpected Back behavior: status=%d confirmations=%d writes=%d", result.Code, store.confirmCalls, store.writes)
			}
			if tc.retains && (placementHTTPField(t, body, "head_revision") != strconv.FormatInt(store.command.ExpectedRevision, 10) || !strings.Contains(body, `id="placement-retained-token" readonly`) || !strings.Contains(body, html.EscapeString(proof.Get("review_token")))) {
				t.Fatal("Back changed the original head or replaced the proof")
			}
			if tc.status == 404 && (strings.Contains(body, html.EscapeString(proof.Get("review_token"))) || strings.Contains(body, privatePlacementGroup)) {
				t.Fatal("revoked Back response retained the original proof or group")
			}
		})
	}
}

func TestPlacementNativeSaveFlashEmphasizesExplicitPublicationAndIsConsumed(t *testing.T) {
	store, _ := placementHTTPFixture(t)
	sessions := scs.New()
	h := TrainingPlacements{Store: store, Sessions: sessions, HTTPWriteTimeout: 30 * time.Second}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+trainingPlacementsPath, h.Index)
	mux.HandleFunc("POST "+trainingPlacementsPath+"/prever", h.Preview)
	mux.HandleFunc("POST "+trainingPlacementsPath+"/confirmar", h.Confirm)
	withUser := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: store.actor})))
	})
	handler := httpx.PublicationIngressMiddleware(30 * time.Second)(sessions.LoadAndSave(withUser))
	proof := postPlacementNativePreview(t, store, handler)
	saved := publicationPost(t, handler, trainingPlacementsPath+"/confirmar", proof)
	want := placementURL(store.command.ContextGroupID, store.command.SubjectID, store.command.SeasonID, store.command.EffectiveOn)
	if saved.Code != http.StatusSeeOther || saved.Header().Get("Location") != want || store.writes != 1 || len(saved.Result().Cookies()) != 1 {
		t.Fatalf("confirmation did not save and redirect to its original scope: %d writes=%d", saved.Code, store.writes)
	}
	cookie := saved.Result().Cookies()[0]
	for _, show := range []bool{true, false} {
		r := httptest.NewRequest(http.MethodGet, want, nil)
		r.AddCookie(cookie)
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, r)
		if result.Code != http.StatusOK || strings.Contains(result.Body.String(), "as instruções dos grupos precisam de publicação explícita") != show {
			t.Fatalf("session flash status=%d, wanted message visibility %t", result.Code, show)
		}
	}
}
