package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type nativeReuseRouteStore struct {
	reuseHTTPStore
	seasons       []dbgen.ListReuseTargetSeasonsRow
	routines      []dbgen.ListReuseRoutinesRow
	sessions      []dbgen.ListReuseSourceSessionsRow
	segments      []dbgen.TrainingSessionSegment
	seasonErr     error
	routineErr    error
	sessionErr    error
	segmentErr    error
	sizeErr       error
	afterSaveAuth error
	artifactSize  int32
	sizeCalls     int
	routineQuery  dbgen.ListReuseRoutinesParams
	segmentLookup FocusedWorkoutEditRequest
	confirmed     reuseReviewed
}

func (s *nativeReuseRouteStore) ReuseSeasons(context.Context, uuid.UUID, uuid.UUID) ([]dbgen.ListReuseTargetSeasonsRow, error) {
	return s.seasons, s.seasonErr
}
func (s *nativeReuseRouteStore) ReuseRoutines(_ context.Context, query dbgen.ListReuseRoutinesParams) ([]dbgen.ListReuseRoutinesRow, error) {
	s.routineQuery = query
	return s.routines, s.routineErr
}
func (s *nativeReuseRouteStore) ReuseSessions(context.Context, uuid.UUID, uuid.UUID) ([]dbgen.ListReuseSourceSessionsRow, error) {
	return s.sessions, s.sessionErr
}
func (s *nativeReuseRouteStore) ReuseSegments(_ context.Context, in FocusedWorkoutEditRequest) ([]dbgen.TrainingSessionSegment, error) {
	s.segmentLookup = in
	return s.segments, s.segmentErr
}
func (s *nativeReuseRouteStore) ReuseArtifactSize(context.Context, []byte) (int32, error) {
	s.sizeCalls++
	return s.artifactSize, s.sizeErr
}
func (s *nativeReuseRouteStore) AuthorizeReuse(ctx context.Context, actor uuid.UUID, source reuseSourceSelector, target *reuseTargetSelector) error {
	err := s.reuseHTTPStore.AuthorizeReuse(ctx, actor, source, target)
	if err == nil && s.saves != 0 {
		return s.afterSaveAuth
	}
	return err
}
func (s *nativeReuseRouteStore) ConfirmReuse(ctx context.Context, reviewed reuseReviewed) (reuseSaveResult, error) {
	s.confirmed = reviewed
	return s.reuseHTTPStore.ConfirmReuse(ctx, reviewed)
}

func nativeReuseFixture(t *testing.T) (reuseReviewPayload, *nativeReuseRouteStore) {
	t.Helper()
	p, read := reuseHTTPExample(t)
	return p, &nativeReuseRouteStore{reuseHTTPStore: reuseHTTPStore{read: read}, artifactSize: 1}
}

func nativeReuseRoutes(actor uuid.UUID, store *nativeReuseRouteStore, groups TrainingGroupStore) (http.Handler, http.Handler) {
	h := StructuredReuse{Store: store, Groups: groups, HTTPWriteTimeout: 30 * time.Second}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+structuredReusePath, h.Task)
	mux.HandleFunc("POST "+structuredReusePath+"/rascunho", h.Draft)
	mux.HandleFunc("POST "+structuredReusePath+"/prever", h.Preview)
	mux.HandleFunc("POST "+structuredReusePath+"/confirmar", h.Confirm)
	withActor := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if actor != uuid.Nil {
			r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actor}))
		}
		mux.ServeHTTP(w, r)
	})
	return httpx.PublicationIngressMiddleware(30 * time.Second)(withActor), withActor
}

func nativeReuseGet(handler http.Handler, values url.Values) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, structuredReusePath+"?"+values.Encode(), nil))
	return w
}

func TestNativeReuseRejectsUnadmittedAndMalformedRequests(t *testing.T) {
	p, store := nativeReuseFixture(t)
	anonymous, _ := nativeReuseRoutes(uuid.Nil, store, nil)
	if response := nativeReuseGet(anonymous, p.Values); response.Code != 404 || response.Header().Get("Cache-Control") != "private, no-store" || response.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("anonymous source status=%d", response.Code)
	}
	routes, withoutIngress := nativeReuseRoutes(p.ActorID, store, nil)
	if response := publicationPost(t, withoutIngress, structuredReusePath+"/prever", p.Values); response.Code != 403 {
		t.Fatalf("unadmitted form status=%d", response.Code)
	}
	bad := httptest.NewRequest(http.MethodPost, structuredReusePath+"/prever", strings.NewReader("source_kind=%zz"))
	bad.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	malformed := httptest.NewRecorder()
	routes.ServeHTTP(malformed, bad)
	if malformed.Code != 403 {
		t.Fatalf("malformed encoding status=%d", malformed.Code)
	}
	for _, tc := range []struct {
		name, endpoint string
		fields         url.Values
	}{
		{"unknown confirm field", "/confirmar", url.Values{"intent": {"confirm"}, "week_start": {"2026-10-19"}}},
		{"wrong intent", "/confirmar", url.Values{"intent": {"preview"}}},
		{"malformed proposal", "/confirmar", url.Values{"intent": {"confirm"}, "proposal": {"{"}}},
		{"duplicate destination", "/prever", url.Values{"week_start": {"2026-10-19", "2026-10-26"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if response := publicationPost(t, routes, structuredReusePath+tc.endpoint, tc.fields); response.Code != 403 {
				t.Fatalf("invalid form status=%d", response.Code)
			}
		})
	}
	tooMany := cloneFocusedValues(p.Values)
	for i := 0; i < 1000; i++ {
		tooMany.Set("input"+strconv.Itoa(i), "x")
	}
	if response := publicationPost(t, routes, structuredReusePath+"/prever", tooMany); response.Code != 403 {
		t.Fatalf("oversize form status=%d", response.Code)
	}
	for _, id := range []string{"source_plan_id", "source_session_id", "routine_id"} {
		invalid := cloneFocusedValues(p.Values)
		invalid.Set(id, "invalid")
		if response := nativeReuseGet(routes, invalid); response.Code != 404 {
			t.Fatalf("malformed %s status=%d", id, response.Code)
		}
	}
	if store.reads+store.saves+store.authorizations+store.sizeCalls != 0 {
		t.Fatal("invalid transport read or wrote private content")
	}
}

func TestNativeReusePreviewRequiresCompleteScopedCopy(t *testing.T) {
	for _, tc := range []struct {
		name, route, message string
		change               func(*nativeReuseRouteStore, url.Values)
		status, sizes        int
	}{
		{"wrong draft command", "/rascunho", "Pedido recusado", func(_ *nativeReuseRouteStore, v url.Values) { v.Set("command", "preview") }, 403, 0},
		{"wrong review command", "/prever", "Pedido recusado", func(_ *nativeReuseRouteStore, v url.Values) { v.Set("command", "refresh") }, 403, 0},
		{"changed mapping order", "/prever", "Reveja os campos e a ordem", func(_ *nativeReuseRouteStore, v url.Values) { v.Set("mapping_order", "m_2") }, 422, 0},
		{"closed writer", "/prever", "A origem, o destino ou a configuração mudou", func(s *nativeReuseRouteStore, _ url.Values) { s.read.State.PartialV2Enabled = false }, 409, 0},
		{"missing target", "/prever", "Escolha o grupo, a época e a semana de destino", func(s *nativeReuseRouteStore, v url.Values) { s.read.Target = nil; v.Del("week_start") }, 422, 0},
		{"target format validation", "/rascunho", "Indique a segunda-feira", func(s *nativeReuseRouteStore, v url.Values) {
			s.read.Target = nil
			v.Set("command", "refresh")
			v.Set("week_start", "2026-10-20")
			v.Set("target_session_id", "invalid")
			v.Set("planned_load_percentage", "101")
		}, 200, 0},
		{"artifact sizing unavailable", "/prever", "Não foi possível concluir o pedido", func(s *nativeReuseRouteStore, _ url.Values) { s.sizeErr = errors.New("synthetic sizing error") }, 500, 1},
		{"artifact too large", "/prever", "Nada foi copiado parcialmente", func(s *nativeReuseRouteStore, _ url.Values) { s.artifactSize = focusedWorkoutMaxArtifactBytes + 1 }, 422, 1},
		{"source scope revoked", "/prever", "Página não encontrada", func(s *nativeReuseRouteStore, _ url.Values) { s.revoked = true }, 404, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, store := nativeReuseFixture(t)
			v := cloneFocusedValues(p.Values)
			v.Set("command", "preview")
			tc.change(store, v)
			routes, _ := nativeReuseRoutes(p.ActorID, store, nil)
			response := publicationPost(t, routes, structuredReusePath+tc.route, v)
			body := response.Body.String()
			if response.Code != tc.status || store.saves != 0 || store.sizeCalls != tc.sizes || !strings.Contains(body, tc.message) || strings.Contains(body, `name="intent" value="confirm"`) {
				t.Fatalf("status=%d writes=%d artifact checks=%d message=%t", response.Code, store.saves, store.sizeCalls, strings.Contains(body, tc.message))
			}
			if tc.name == "target format validation" && (!strings.Contains(body, "Escolha uma opção válida.") || !strings.Contains(body, "Indique uma percentagem entre 0 e 100")) {
				t.Fatal("target form lost its contextual validation")
			}
		})
	}
}

func TestNativeReusePreviewConfirmsExactRenderedDraft(t *testing.T) {
	p, store := nativeReuseFixture(t)
	routes, _ := nativeReuseRoutes(p.ActorID, store, nil)
	v := cloneFocusedValues(p.Values)
	v.Set("command", "preview")
	preview := publicationPost(t, routes, structuredReusePath+"/prever", v)
	body := preview.Body.String()
	if preview.Code != 200 || store.saves != 0 || store.sizeCalls != 1 || !strings.Contains(body, "Rever a cópia independente") || !strings.Contains(body, "não publica para os atletas") {
		t.Fatalf("preview status=%d sizing=%d writes=%d", preview.Code, store.sizeCalls, store.saves)
	}
	confirm := workoutEditNativeConfirm(t, body)
	var payload reuseReviewPayload
	if err := json.Unmarshal([]byte(confirm.Get("proposal")), &payload); err != nil || len(payload.Proposal.Destinations) != 1 {
		t.Fatalf("rendered proof omitted the destination: %v", err)
	}
	destination := payload.Proposal.Destinations[0].Graph
	if destination.SessionID == p.Source.Items[0].Graph.SessionID || destination.Schedule.Date != v.Get("mappings[m_1].date") || !strings.Contains(body, destination.Title) {
		t.Fatal("review failed to show the independently scheduled copy")
	}
	store.result = reuseSaveResult{PlanID: payload.Proposal.TargetPlanID, GroupID: payload.Target.Selector.GroupID, SessionID: destination.SessionID, Day: destination.Schedule.Date}
	saved := publicationPost(t, routes, structuredReusePath+"/confirmar", confirm)
	want := structuredPlannerURL(store.result.GroupID.String(), store.result.PlanID.String(), destination.SessionID.String(), destination.Schedule.Date)
	if saved.Code != 303 || saved.Header().Get("Location") != want || store.saves != 1 || store.reads != 1 || !store.confirmed.verified || store.confirmed.Payload.Proposal.Destinations[0].Graph.SessionID != destination.SessionID {
		t.Fatalf("confirmation altered the reviewed destination or did not redirect: %d writes=%d reads=%d", saved.Code, store.saves, store.reads)
	}
}

func TestNativeReuseConfirmationRecoveryReauthorizesBeforeDisplayingProof(t *testing.T) {
	for _, tc := range []struct {
		name, message        string
		change               func(*nativeReuseRouteStore)
		status, authorities  int
		retainsOriginalProof bool
	}{
		{"write reports scope loss", "Página não encontrada", func(s *nativeReuseRouteStore) { s.saveErr = pgx.ErrNoRows }, 404, 1, false},
		{"lost permission after rollback", "Página não encontrada", func(s *nativeReuseRouteStore) { s.afterSaveAuth = pgx.ErrNoRows }, 404, 2, false},
		{"private source reread revoked", "Página não encontrada", func(s *nativeReuseRouteStore) { s.readErr = pgx.ErrNoRows }, 404, 2, false},
		{"source exceeds review cap after rollback", "Nada foi copiado parcialmente", func(s *nativeReuseRouteStore) { s.readErr = errFocusedWorkoutLimit }, 422, 2, true},
		{"private season list unavailable after rollback", "Não foi possível concluir o pedido", func(s *nativeReuseRouteStore) { s.seasonErr = errors.New("synthetic season listing failure") }, 500, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, store := nativeReuseFixture(t)
			store.saveErr = errReuseChanged
			tc.change(store)
			raw, token, err := focusedWorkoutReviews.signReuse(p, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			routes, _ := nativeReuseRoutes(p.ActorID, store, nil)
			response := publicationPost(t, routes, structuredReusePath+"/confirmar", url.Values{"intent": {"confirm"}, "proposal": {raw}, "review_token": {token}})
			body := response.Body.String()
			if response.Code != tc.status || store.saves != 1 || store.authorizations != tc.authorities || !strings.Contains(body, tc.message) || strings.Contains(body, html.EscapeString(token)) != tc.retainsOriginalProof || strings.Contains(body, `value="preview"`) || strings.Contains(body, `name="intent" value="confirm"`) {
				t.Fatalf("recovery status=%d attempts=%d authority checks=%d", response.Code, store.saves, store.authorizations)
			}
		})
	}
}
