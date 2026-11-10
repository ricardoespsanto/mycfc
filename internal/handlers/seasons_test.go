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

	"github.com/alexedwards/scs/v2"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type seasonStoreStub struct {
	authErr, createErr, selectErr error
	listErr, currentErr, readErr  error
	state                         SeasonSelection
	created                       SeasonInput
	createCount, selectCount      int
	offset                        int32
	rows                          []dbgen.Season
	resolve                       func(SeasonReturnContext) (SeasonReturnContext, string, error)
	getSelection                  func() (SeasonSelection, error)
	expected                      string
}

func (s *seasonStoreStub) Authorize(context.Context, uuid.UUID) error { return s.authErr }
func (s *seasonStoreStub) ListSeasons(_ context.Context, _ uuid.UUID, offset int32) ([]dbgen.Season, error) {
	s.offset = offset
	if s.rows != nil || s.listErr != nil {
		return s.rows, s.listErr
	}
	return []dbgen.Season{s.state.Target}, nil
}
func (s *seasonStoreStub) GetSelectionState(context.Context, uuid.UUID, uuid.UUID) (SeasonSelection, error) {
	if s.getSelection != nil {
		return s.getSelection()
	}
	return s.state, s.readErr
}
func (s *seasonStoreStub) CreateSeason(_ context.Context, _ uuid.UUID, in SeasonInput) (dbgen.Season, error) {
	s.created = in
	s.createCount++
	return s.state.Target, s.createErr
}
func (s *seasonStoreStub) SelectCurrentSeason(_ context.Context, _, _ uuid.UUID, v string) error {
	s.selectCount++
	s.expected = v
	return s.selectErr
}
func (s *seasonStoreStub) ResolveReturn(_ context.Context, _ uuid.UUID, c SeasonReturnContext) (SeasonReturnContext, string, error) {
	if s.resolve != nil {
		return s.resolve(c)
	}
	if c.Kind == "group" {
		return c, trainingGroupsPath + "/" + c.ID, nil
	}
	return SeasonReturnContext{Kind: "list"}, seasonsPath, s.authErr
}
func seasonTestState() SeasonSelection {
	date := func(v string) pgtype.Date {
		d, _ := time.Parse("2006-01-02", v)
		return pgtype.Date{Time: d, Valid: true}
	}
	return SeasonSelection{Target: dbgen.Season{ID: uuid.New(), Code: "2026/27", Name: "Época explícita", StartsOn: date("2026-10-13"), EndsOn: date("2027-06-04")}, Version: "opaque-version"}
}
func seasonTestRequest(method, path string, v url.Values, actor uuid.UUID, target uuid.UUID) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetPathValue("id", target.String())
	return r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actor, Name: "Administrator", IsAdmin: true}))
}
func TestSeasonCreateKeepsExplicitDatesAndDoesNotSelect(t *testing.T) {
	s := &seasonStoreStub{state: seasonTestState()}
	actor := uuid.New()
	v := url.Values{"code": {"2026/27"}, "name": {"Época explícita"}, "starts_on": {"2026-10-13"}, "ends_on": {"2027-06-04"}, "return_to": {"https://outside.example"}}
	w := httptest.NewRecorder()
	(Seasons{Store: s}).Create(w, seasonTestRequest("POST", seasonsPath+"/criar", v, actor, uuid.Nil))
	if w.Code != 303 || s.createCount != 1 || s.selectCount != 0 || s.created.StartsOn.Format("2006-01-02") != "2026-10-13" || s.created.EndsOn.Format("2006-01-02") != "2027-06-04" || strings.Contains(w.Header().Get("Location"), "outside") {
		t.Fatalf("status=%d input=%#v location=%s", w.Code, s.created, w.Header().Get("Location"))
	}
}
func TestSeasonCreateValidationAndDuplicateRetainValues(t *testing.T) {
	for _, tc := range []struct {
		name, start, end string
		err              error
	}{{"reverse", "2027-06-04", "2026-10-13", nil}, {"missing", "", "", nil}, {"duplicate", "2024-02-29", "2024-02-29", ErrSeasonDuplicate}} {
		t.Run(tc.name, func(t *testing.T) {
			s := &seasonStoreStub{state: seasonTestState(), createErr: tc.err}
			v := url.Values{"code": {"Duplicate"}, "name": {"Keep proposed name"}, "starts_on": {tc.start}, "ends_on": {tc.end}}
			w := httptest.NewRecorder()
			(Seasons{Store: s}).Create(w, seasonTestRequest("POST", seasonsPath+"/criar", v, uuid.New(), uuid.Nil))
			if w.Code != 422 || !strings.Contains(w.Body.String(), "Keep proposed name") || !strings.Contains(w.Body.String(), `value="Duplicate"`) {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if tc.err == nil && s.createCount != 0 {
				t.Fatal("invalid input wrote")
			}
		})
	}
}
func TestSeasonPreviewConfirmationBindingAndConflict(t *testing.T) {
	actor, other := uuid.New(), uuid.New()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	state := seasonTestState()
	c := SeasonReturnContext{Kind: "group", ID: uuid.NewString()}
	binding := seasonConfirmationBinding(actor, state.Target.ID, c)
	token := signClassificationConfirmation(binding, state.Version, now)
	for _, tc := range []struct {
		name, token    string
		actor          uuid.UUID
		ctx            SeasonReturnContext
		err            error
		status, writes int
	}{{"valid", token, actor, c, nil, 303, 1}, {"tamper", token + "x", actor, c, nil, 422, 0}, {"expired", signClassificationConfirmation(binding, state.Version, now.Add(-16*time.Minute)), actor, c, nil, 422, 0}, {"foreign actor", token, other, c, nil, 422, 0}, {"foreign context", token, actor, SeasonReturnContext{Kind: "group", ID: uuid.NewString()}, nil, 422, 0}, {"stale", token, actor, c, ErrSeasonConflict, 409, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			s := &seasonStoreStub{state: state, selectErr: tc.err}
			v := seasonContextValues(tc.ctx)
			v.Set("confirm", "yes")
			v.Set("preview_token", tc.token)
			w := httptest.NewRecorder()
			(Seasons{Store: s, Now: func() time.Time { return now }}).Select(w, seasonTestRequest("POST", seasonsPath+"/"+state.Target.ID.String()+"/selecionar", v, tc.actor, state.Target.ID))
			if w.Code != tc.status || s.selectCount != tc.writes {
				t.Fatalf("status=%d writes=%d body=%s", w.Code, s.selectCount, w.Body.String())
			}
			if tc.status == 409 && !strings.Contains(w.Body.String(), tc.token) {
				t.Fatal("stale token replaced")
			}
			if tc.status == 422 && (!strings.Contains(w.Body.String(), "Recarregar e rever a seleção") || strings.Contains(w.Body.String(), `name="confirm"`)) {
				t.Fatal("invalid preview lacks explicit recovery")
			}

		})
	}
	s := &seasonStoreStub{state: state}
	w := httptest.NewRecorder()
	(Seasons{Store: s, Now: func() time.Time { return now }}).Select(w, seasonTestRequest("POST", seasonsPath+"/"+state.Target.ID.String()+"/selecionar", seasonContextValues(c), actor, state.Target.ID))
	if w.Code != 200 || s.selectCount != 0 || !strings.Contains(w.Body.String(), "preview_token") {
		t.Fatalf("preview status=%d writes=%d", w.Code, s.selectCount)
	}
}
func TestSeasonRevokedAdminCannotReadOrWrite(t *testing.T) {
	s := &seasonStoreStub{state: seasonTestState(), authErr: pgx.ErrNoRows}
	h := Seasons{Store: s}
	for _, fn := range []func(http.ResponseWriter, *http.Request){h.Index, h.CreatePage, h.Create, h.Selection, h.Select} {
		w := httptest.NewRecorder()
		fn(w, seasonTestRequest("POST", seasonsPath, nil, uuid.New(), s.state.Target.ID))
		if w.Code != 404 || strings.Contains(w.Body.String(), s.state.Target.Name) {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}
	if s.createCount+s.selectCount != 0 {
		t.Fatal("revoked actor wrote")
	}
}
func TestValidSeasonInputRejectsMissingDates(t *testing.T) {
	if validSeasonInput(SeasonInput{Code: "2026/27", Name: "Explicit season"}) {
		t.Fatal("missing explicit dates accepted")
	}
}

func (s *seasonStoreStub) HasCurrentSeason(context.Context, uuid.UUID) (bool, error) {
	return s.state.Current != nil, s.currentErr
}
func TestSeasonListUsesCurrentStateBeyondItsPage(t *testing.T) {
	state := seasonTestState()
	other := dbgen.Season{ID: uuid.New(), IsCurrent: true}
	state.Current = &other
	store := &seasonStoreStub{state: state}
	w := httptest.NewRecorder()
	(Seasons{Store: store}).Index(w, seasonTestRequest("GET", seasonsPath+"?page=1", nil, uuid.New(), uuid.Nil))
	if w.Code != 200 || strings.Contains(w.Body.String(), "Sem época atual") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestSeasonFormsAndPaginationCarryVerifiedContextAndSessionFeedback(t *testing.T) {
	state := seasonTestState()
	current := state.Target
	current.ID, current.Name, current.IsCurrent = uuid.New(), "Atual antes da alteração", true
	state.Current = &current
	actor := uuid.New()
	source := SeasonReturnContext{Kind: "member", ID: uuid.NewString(), Page: "2", Query: "Membro & família"}
	contextQuery := url.Values{"return_kind": {source.Kind}, "return_id": {source.ID}, "return_page": {source.Page}, "return_q": {source.Query}}
	store := &seasonStoreStub{state: state, rows: make([]dbgen.Season, 20)}
	for i := range store.rows {
		store.rows[i] = state.Target
		store.rows[i].ID = uuid.New()
	}
	store.resolve = func(c SeasonReturnContext) (SeasonReturnContext, string, error) {
		if c.Kind == "" {
			return SeasonReturnContext{Kind: "list"}, seasonsPath, nil
		}
		if c != source {
			t.Fatalf("context round trip changed input: %+v", c)
		}
		if store.selectCount > 0 {
			return SeasonReturnContext{Kind: "list"}, seasonsPath, nil
		}
		return c, "/admin/membros/" + c.ID, nil
	}
	sessions := scs.New()
	h := Seasons{Store: store, Sessions: sessions}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+seasonsPath, h.Index)
	mux.HandleFunc("GET "+seasonsPath+"/criar", h.CreatePage)
	mux.HandleFunc("POST "+seasonsPath+"/criar", h.Create)
	mux.HandleFunc("GET "+seasonsPath+"/{id}/selecionar", h.Selection)
	mux.HandleFunc("POST "+seasonsPath+"/{id}/selecionar", h.Select)
	routes := sessions.LoadAndSave(mux)
	var cookie *http.Cookie
	request := func(method, path string, form url.Values, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := seasonTestRequest(method, path, form, actor, state.Target.ID)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		routes.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s = %d, want %d", method, path, w.Code, want)
		}
		for _, c := range w.Result().Cookies() {
			if c.Name == sessions.Cookie.Name {
				cookie = c
			}
		}
		return w
	}
	requireHTML := func(w *httptest.ResponseRecorder, snippets ...string) {
		t.Helper()
		for _, s := range snippets {
			if !strings.Contains(w.Body.String(), s) {
				t.Fatalf("response missing %q", s)
			}
		}
	}
	withContext := func(path string) string { return path + "?" + contextQuery.Encode() }
	list := request("GET", withContext(seasonsPath)+"&page=1", nil, 200)
	next, _ := url.Parse(withContext(seasonsPath))
	nextValues := next.Query()
	nextValues.Set("page", "2")
	next.RawQuery = nextValues.Encode()
	requireHTML(list, html.EscapeString(withContext(seasonsPath)), html.EscapeString(next.String()), html.EscapeString(withContext(seasonsPath+"/criar")))
	if store.offset != 20 {
		t.Fatalf("second page used offset %d", store.offset)
	}
	create := request("GET", withContext(seasonsPath+"/criar"), nil, 200)
	requireHTML(create, html.EscapeString(withContext(seasonsPath+"/criar")), "Introduza ambas as datas.", "Membro &amp; família")
	form := url.Values{"code": {" TEST "}, "name": {" Época de teste "}, "starts_on": {"2035-01-03"}, "ends_on": {"2035-07-10"}}
	response := request("POST", withContext(seasonsPath+"/criar"), form, 303)
	location := withContext(seasonsPath + "/" + state.Target.ID.String() + "/selecionar")
	if response.Header().Get("Location") != location || store.selectCount != 0 || store.created.Code != "TEST" || store.created.Name != "Época de teste" {
		t.Fatalf("create redirect=%q, input=%+v, selections=%d", response.Header().Get("Location"), store.created, store.selectCount)
	}
	const created = "Época criada. Selecione-a explicitamente"
	requireHTML(request("GET", location, nil, 200), created, current.Name, state.Target.Name)
	if second := request("GET", location, nil, 200); strings.Contains(second.Body.String(), created) {
		t.Fatal("creation feedback persisted after being read")
	}
	preview := request("POST", location, nil, 200)
	match := regexp.MustCompile("name=\"preview_token\" value=\"([^\"]+)\"").FindStringSubmatch(preview.Body.String())
	if len(match) != 2 || store.selectCount != 0 {
		t.Fatal("preview did not return a token without changing selection")
	}
	response = request("POST", location, url.Values{"confirm": {"yes"}, "preview_token": {match[1]}}, 303)
	if response.Header().Get("Location") != seasonsPath || store.selectCount != 1 || store.expected != state.Version {
		t.Fatalf("confirmation ignored destination recheck: redirect=%q, writes=%d, version=%q", response.Header().Get("Location"), store.selectCount, store.expected)
	}
	const selected = "Época atual selecionada. O histórico existente foi preservado."
	requireHTML(request("GET", seasonsPath, nil, 200), selected)
	if second := request("GET", seasonsPath, nil, 200); strings.Contains(second.Body.String(), selected) {
		t.Fatal("selection feedback persisted after being read")
	}
}

func TestSeasonRequestFailuresProtectSelectionsAndContext(t *testing.T) {
	state := seasonTestState()
	actor := uuid.New()
	selectionURL := seasonsPath + "/" + state.Target.ID.String() + "/selecionar"
	storageErr := errors.New("synthetic private storage error")
	now := time.Now()
	token := signClassificationConfirmation(seasonConfirmationBinding(actor, state.Target.ID, SeasonReturnContext{Kind: "list"}), state.Version, now)
	validCreate := url.Values{"code": {"TEST"}, "name": {"Época de teste"}, "starts_on": {"2035-01-03"}, "ends_on": {"2035-07-10"}}
	confirm := url.Values{"confirm": {"yes"}, "preview_token": {token}}
	badContext := func(SeasonReturnContext) (SeasonReturnContext, string, error) {
		return SeasonReturnContext{}, "", pgx.ErrNoRows
	}
	type handler func(Seasons, http.ResponseWriter, *http.Request)
	for _, tc := range []struct {
		name, method, path       string
		handle                   handler
		form                     url.Values
		setup                    func(*seasonStoreStub, *http.Request)
		status, creates, selects int
		message                  string
	}{
		{name: "anonymous", method: "GET", path: seasonsPath, handle: Seasons.Index, setup: func(_ *seasonStoreStub, r *http.Request) { *r = *r.WithContext(t.Context()) }, status: 404},
		{name: "invalid page", method: "GET", path: seasonsPath + "?page=100", handle: Seasons.Index, status: 404},
		{name: "collection read fails", method: "GET", path: seasonsPath, handle: Seasons.Index, setup: func(s *seasonStoreStub, _ *http.Request) { s.listErr = storageErr }, status: 500},
		{name: "collection destination revoked", method: "GET", path: seasonsPath, handle: Seasons.Index, setup: func(s *seasonStoreStub, _ *http.Request) { s.resolve = badContext }, status: 404},
		{name: "current marker read fails", method: "GET", path: seasonsPath, handle: Seasons.Index, setup: func(s *seasonStoreStub, _ *http.Request) { s.currentErr = storageErr }, status: 500},
		{name: "new form destination revoked", method: "GET", path: seasonsPath + "/criar", handle: Seasons.CreatePage, setup: func(s *seasonStoreStub, _ *http.Request) { s.resolve = badContext }, status: 404},
		{name: "malformed create form", method: "POST", path: seasonsPath + "/criar", handle: Seasons.Create, form: validCreate, setup: func(_ *seasonStoreStub, r *http.Request) {
			r.Header.Set("Content-Type", "multipart/form-data; boundary")
		}, status: 403},
		{name: "create destination revoked", method: "POST", path: seasonsPath + "/criar", handle: Seasons.Create, form: validCreate, setup: func(s *seasonStoreStub, _ *http.Request) { s.resolve = badContext }, status: 404},
		{name: "name and code validation", method: "POST", path: seasonsPath + "/criar", handle: Seasons.Create, form: url.Values{"code": {strings.Repeat("á", 21)}, "name": {"X"}, "starts_on": {"2035-01-03"}, "ends_on": {"2035-07-10"}}, status: 422, message: "Indique um código de 1 a 20 caracteres."},
		{name: "store validation", method: "POST", path: seasonsPath + "/criar", handle: Seasons.Create, form: validCreate, setup: func(s *seasonStoreStub, _ *http.Request) { s.createErr = ErrSeasonInvalid }, status: 422, creates: 1, message: "Reveja os dados da época."},
		{name: "create authorization revoked", method: "POST", path: seasonsPath + "/criar", handle: Seasons.Create, form: validCreate, setup: func(s *seasonStoreStub, _ *http.Request) { s.createErr = pgx.ErrNoRows }, status: 404, creates: 1},
		{name: "missing target UUID", method: "GET", path: selectionURL, handle: Seasons.Selection, setup: func(_ *seasonStoreStub, r *http.Request) { r.SetPathValue("id", "invalid") }, status: 404},
		{name: "unknown selection target", method: "GET", path: selectionURL, handle: Seasons.Selection, setup: func(s *seasonStoreStub, _ *http.Request) { s.readErr = pgx.ErrNoRows }, status: 404},
		{name: "selection destination revoked", method: "GET", path: selectionURL, handle: Seasons.Selection, setup: func(s *seasonStoreStub, _ *http.Request) { s.resolve = badContext }, status: 404},
		{name: "malformed selection form", method: "POST", path: selectionURL, handle: Seasons.Select, form: confirm, setup: func(_ *seasonStoreStub, r *http.Request) {
			r.Header.Set("Content-Type", "multipart/form-data; boundary")
		}, status: 403},
		{name: "POST selection target unavailable", method: "POST", path: selectionURL, handle: Seasons.Select, form: confirm, setup: func(s *seasonStoreStub, _ *http.Request) { s.readErr = pgx.ErrNoRows }, status: 404},
		{name: "selection authorization revoked", method: "POST", path: selectionURL, handle: Seasons.Select, form: confirm, setup: func(s *seasonStoreStub, _ *http.Request) { s.selectErr = pgx.ErrNoRows }, status: 404, selects: 1},
		{name: "conflict target removed before reread", method: "POST", path: selectionURL, handle: Seasons.Select, form: confirm, setup: func(s *seasonStoreStub, _ *http.Request) {
			s.selectErr = ErrSeasonConflict
			s.getSelection = func() (SeasonSelection, error) {
				if s.selectCount > 0 {
					return SeasonSelection{}, pgx.ErrNoRows
				}
				return state, nil
			}
		}, status: 404, selects: 1},
		{name: "destination recheck fails after commit", method: "POST", path: selectionURL, handle: Seasons.Select, form: confirm, setup: func(s *seasonStoreStub, _ *http.Request) {
			s.resolve = func(c SeasonReturnContext) (SeasonReturnContext, string, error) {
				if s.selectCount > 0 {
					return SeasonReturnContext{}, "", storageErr
				}
				return SeasonReturnContext{Kind: "list"}, seasonsPath, nil
			}
		}, status: 500, selects: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &seasonStoreStub{state: state}
			r := seasonTestRequest(tc.method, tc.path, tc.form, actor, state.Target.ID)
			if tc.setup != nil {
				tc.setup(store, r)
			}
			w := httptest.NewRecorder()
			tc.handle(Seasons{Store: store, Now: func() time.Time { return now }}, w, r)
			if w.Code != tc.status || store.createCount != tc.creates || store.selectCount != tc.selects || w.Header().Get("Location") != "" {
				t.Fatalf("status=%d, creates=%d, selects=%d, location=%q", w.Code, store.createCount, store.selectCount, w.Header().Get("Location"))
			}
			body := w.Body.String()
			if strings.Contains(body, storageErr.Error()) || strings.Contains(body, state.Target.Name) || !strings.Contains(body, tc.message) {
				t.Fatal("response exposed private state or missed the field error")
			}
			if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" {
				t.Fatal("failed season request lost its privacy headers")
			}
		})
	}
}

func TestFailedWeekSeasonGuidancePreservesOnlyVerifiedGroupWeek(t *testing.T) {
	group, week, actor := uuid.New(), uuid.New(), uuid.New()
	for _, tc := range []struct {
		name, returnTo string
		allowed        bool
		want           string
	}{{"verified", structuredPlannerURL(group.String(), week.String(), ""), true, week.String()}, {"forgedweek", structuredPlannerURL(group.String(), week.String(), ""), false, ""}, {"wronggroup", structuredPlannerURL(uuid.NewString(), week.String(), ""), true, ""}} {
		t.Run(tc.name, func(t *testing.T) {
			h := StructuredTraining{Groups: &taskGroupsFake{allowedWeeks: map[uuid.UUID]bool{week: tc.allowed}}}
			got := h.seasonSetupContext(t.Context(), CurrentUser{ID: actor}, group, tc.returnTo)
			if got.WeekID != tc.want || got.ID != group.String() {
				t.Fatalf("context %#v", got)
			}
		})
	}
}
