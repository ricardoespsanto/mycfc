package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type trainingSetupFake struct {
	status             TrainingSetupStatus
	err                error
	resolves           int
	capabilities       int
	statusErr          error
	revokeOnCapability bool
}

func (s *trainingSetupFake) Capabilities(context.Context, uuid.UUID, uuid.UUID) (TrainingSetupStatus, error) {
	s.capabilities++
	if s.revokeOnCapability {
		s.err = pgx.ErrNoRows
	}
	if s.statusErr != nil {
		return s.status, s.statusErr
	}
	return s.status, s.err
}
func (s *trainingSetupFake) ResolveContext(_ context.Context, _ uuid.UUID, c TrainingSetupContext) (TrainingSetupContext, string, error) {
	s.resolves++
	if s.err != nil {
		return TrainingSetupContext{}, "", s.err
	}
	dest := trainingGroupsPath + "/" + c.GroupID
	if c.WeekID != "" {
		dest = structuredPlannerURL(c.GroupID, c.WeekID, "", c.Day)
	}
	return c, dest, nil
}
func setupFixtureContext() TrainingSetupContext {
	return TrainingSetupContext{GroupID: uuid.NewString(), WeekID: uuid.NewString(), Day: "2026-11-14"}
}
func setupValues(c TrainingSetupContext) url.Values {
	return url.Values{"origin_group": {c.GroupID}, "origin_week": {c.WeekID}, "origin_day": {c.Day}}
}
func setupRequest(method, path string, v url.Values, actor, member uuid.UUID) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if member != uuid.Nil {
		r.SetPathValue("id", member.String())
	}
	return r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actor, IsAdmin: true}))
}
func TestGroupSetupLinksMatchActualCapabilitiesAndConfiguredState(t *testing.T) {
	c := setupFixtureContext()
	g := TrainingGroupRecord{ID: uuid.MustParse(c.GroupID)}
	for _, tc := range []struct {
		name                                           string
		status                                         TrainingSetupStatus
		classify, define, season, applicable, required bool
	}{{"admin no season", TrainingSetupStatus{ProgrammeCode: "Competition", Administrator: true, ProgrammeAuthority: true, MissingSeason: true}, true, false, true, true, true}, {"coach no season", TrainingSetupStatus{ProgrammeCode: "Competition", ProgrammeAuthority: true, MissingSeason: true}, false, false, false, true, true}, {"team only", TrainingSetupStatus{ProgrammeCode: "Competition"}, false, false, false, true, true}, {"competition coach", TrainingSetupStatus{ProgrammeCode: "Competition", ProgrammeAuthority: true}, true, true, false, true, true}, {"initiation optional", TrainingSetupStatus{ProgrammeCode: "Initiation", ProgrammeAuthority: true, CategoryConfigured: true}, true, true, false, true, false}, {"leisure no category prerequisite", TrainingSetupStatus{ProgrammeCode: "Leisure", ProgrammeAuthority: true}, true, false, false, false, false}} {
		t.Run(tc.name, func(t *testing.T) {
			p := pages.TrainingGroupsPage{Mode: "add", WeekID: c.WeekID}
			r := setupRequest("GET", "/group?day="+c.Day, nil, uuid.New(), uuid.Nil)
			h := TrainingGroups{Setup: &trainingSetupFake{status: tc.status}}
			if err := h.applySetup(t.Context(), r, CurrentUser{ID: uuid.New(), IsAdmin: true}, g, &p); err != nil {
				t.Fatal(err)
			}
			if (p.Setup.ClassificationURL != "") != tc.classify || (p.Setup.DefinitionsURL != "") != tc.define || (p.Setup.SeasonURL != "") != tc.season || p.Setup.CategoryApplicable != tc.applicable || p.Setup.CategoryRequired != tc.required {
				t.Fatalf("capability links %#v", p.Setup)
			}
			if p.ContextDay != c.Day || !strings.Contains(p.AddURL, "day="+c.Day) {
				t.Fatal("group task lost exact day")
			}
			if tc.classify && !strings.Contains(p.Setup.ClassificationURL, "origin_day="+c.Day) {
				t.Fatal("classification loses origin day")
			}
		})
	}
}
func TestClassificationOriginRetainsSearchAndBindsPreviewConfirmation(t *testing.T) {
	c := setupFixtureContext()
	actor, member := uuid.New(), uuid.New()
	store := &classificationFake{allowed: true, name: "Private subject", code: "Leisure"}
	setup := &trainingSetupFake{}
	h := Classification{Store: store, Setup: setup}
	values := setupValues(c)
	values.Set("scope", "11111111-1111-1111-1111-111111111111:22222222-2222-2222-2222-222222222222")
	values.Set("starts_on", time.Now().In(lisbonLocation()).Format("2006-01-02"))
	post := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.Post(w, setupRequest("POST", "/equipa/classificacao/"+member.String()+"?q=Ana", values, actor, member))
		return w
	}
	preview := post()
	if preview.Code != 200 || store.writes != 0 {
		t.Fatalf("preview status=%d writes=%d", preview.Code, store.writes)
	}
	token := extractPreviewToken(t, preview.Body.String())
	values.Set("preview_token", token)
	values.Set("confirm", "yes")
	values.Set("origin_day", "2026-11-15")
	denied := post()
	if denied.Code != 409 || store.writes != 0 {
		t.Fatalf("changed origin status=%d writes=%d", denied.Code, store.writes)
	}
	values.Set("origin_day", c.Day)
	confirmed := post()
	if confirmed.Code != 200 || store.writes != 1 || !strings.Contains(confirmed.Body.String(), "origin_day="+c.Day) || !strings.Contains(confirmed.Body.String(), "Voltar ao grupo e à semana") {
		t.Fatalf("confirmation status=%d writes=%d", confirmed.Code, store.writes)
	}
	if confirmed.Header().Get("Cache-Control") != "no-store" || confirmed.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("missing privacy headers")
	}
	// A lost originating permission cannot retain a previously visible person.
	setup.err = pgx.ErrNoRows
	blocked := post()
	if blocked.Code != 404 || strings.Contains(blocked.Body.String(), store.name) || store.writes != 1 {
		t.Fatalf("revoked origin status=%d writes=%d", blocked.Code, store.writes)
	}
}
func TestClassificationSearchAndChangePersonKeepBoundedOrigin(t *testing.T) {
	c := setupFixtureContext()
	actor, member := uuid.New(), uuid.New()
	store := &classificationSearchFake{classificationFake: classificationFake{allowed: true, name: "Selected name"}, people: []ClassificationPerson{{ID: member, Name: "Selected name"}}, more: true}
	h := Classification{Store: store, Setup: &trainingSetupFake{}}
	target := setupContextURL("/equipa/classificacao?q=Ana&page=2", c)
	r := setupRequest("GET", target, nil, actor, uuid.Nil)
	w := httptest.NewRecorder()
	h.Search(w, r)
	for _, part := range []string{"origin_day=" + c.Day, `name="origin_group" value="` + c.GroupID + `"`, "Voltar ao grupo e à semana"} {
		if w.Code != 200 || !strings.Contains(w.Body.String(), part) {
			t.Fatalf("search status=%d missing %q", w.Code, part)
		}
	}
	r = setupRequest("GET", classificationPersonSetupURL(member, "Ana", c), nil, actor, member)
	w = httptest.NewRecorder()
	h.Get(w, r)
	for _, part := range []string{"origin_day=" + c.Day, `name="origin_day" value="` + c.Day + `"`, "Alterar pessoa"} {
		if w.Code != 200 || !strings.Contains(w.Body.String(), part) {
			t.Fatalf("person status=%d missing %q", w.Code, part)
		}
	}
}
func TestDefinitionsOriginSuccessAndValidationReturnToExactGroup(t *testing.T) {
	c := setupFixtureContext()
	actor, season, programme := uuid.New(), uuid.New(), uuid.New()
	store := &definitionFake{scopes: []DefinitionScope{{SeasonID: season, ProgrammeID: programme, Season: "Configured season", Programme: "Competição"}}}
	setup := &trainingSetupFake{}
	h := Definitions{Store: store, Setup: setup}
	v := setupValues(c)
	v.Set("scope", season.String()+":"+programme.String())
	v.Set("code", "JOVENS")
	v.Set("name", "Jovens")
	w := httptest.NewRecorder()
	h.Post(w, setupRequest("POST", "/equipa/escaloes", v, actor, uuid.Nil))
	want := structuredPlannerURL(c.GroupID, c.WeekID, "", c.Day)
	if w.Code != 303 || w.Header().Get("Location") != want || store.created != 1 {
		t.Fatalf("save status=%d return=%s count=%d", w.Code, w.Header().Get("Location"), store.created)
	}
	v.Set("code", "!")
	w = httptest.NewRecorder()
	h.Post(w, setupRequest("POST", "/equipa/escaloes", v, actor, uuid.Nil))
	if w.Code != 422 || !strings.Contains(w.Body.String(), `name="origin_day" value="`+c.Day+`"`) {
		t.Fatalf("invalid definitions status=%d", w.Code)
	}
	setup.err = pgx.ErrNoRows
	v.Set("code", "VALID")
	w = httptest.NewRecorder()
	h.Post(w, setupRequest("POST", "/equipa/escaloes", v, actor, uuid.Nil))
	if w.Code != 404 || store.created != 1 {
		t.Fatalf("revoked origin status=%d creates=%d", w.Code, store.created)
	}
}
func TestOriginTokenKeepsLegacyBindingsAndSeasonContextTransport(t *testing.T) {
	actor, member := uuid.New(), uuid.New()
	old := confirmationBinding(actor, member, "SPORT", nil)
	if setupConfirmationBinding(actor, member, "SPORT", nil, TrainingSetupContext{}) != old {
		t.Fatal("legacy correction binding changed")
	}
	c := setupFixtureContext()
	if setupConfirmationBinding(actor, member, "SPORT", nil, c) == old {
		t.Fatal("origin omitted from scoped correction token")
	}
	sc := setupSeasonContext("classification", member.String(), c)
	encoded := seasonContextValues(sc)
	if encoded.Get("origin_group") != c.GroupID || encoded.Get("origin_day") != c.Day {
		t.Fatal("season handoff lost originating context")
	}
}

func TestCategorySetupMessagesDoNotInferMaskedAgeEligibility(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		categories []ClassificationCategory
		want       bool
	}{{"masked", "Competition", []ClassificationCategory{{Masked: true}}, false}, {"visible no match", "Competition", []ClassificationCategory{{Eligible: false}}, true}, {"not applicable", "Leisure", nil, false}, {"configured eligible", "Initiation", []ClassificationCategory{{Eligible: true}}, false}} {
		message := classificationCategorySetupMessage([]ClassificationOption{{Code: tc.code, Programme: "Programme", Season: "Configured season", Categories: tc.categories}})
		if (message != "") != tc.want {
			t.Fatalf("%s message=%q", tc.name, message)
		}
	}
}

func TestOptionalSetupReadCannotBlockReadyGroupTasks(t *testing.T) {
	group, week, actor := uuid.New(), uuid.New(), uuid.New()
	g := TrainingGroupRecord{ID: group, Name: "Ready group", MemberCount: 1, LatestWeekID: &week, UpdatedAt: time.Now()}
	for _, tc := range []struct {
		mode               string
		statusErr, authErr error
		revoke             bool
		empty              bool
		want, calls        int
	}{{mode: "detail", statusErr: errors.New("private optional query diagnostic"), want: 200}, {mode: "rename", statusErr: errors.New("private optional query diagnostic"), want: 200}, {mode: "rename", statusErr: errors.New("private optional query diagnostic"), want: 200, empty: true}, {mode: "add", statusErr: errors.New("private optional query diagnostic"), want: 200, calls: 1}, {mode: "add", statusErr: pgx.ErrNoRows, want: 404, calls: 1}, {mode: "detail", authErr: pgx.ErrNoRows, want: 404}, {mode: "add", statusErr: errors.New("private optional query diagnostic"), revoke: true, want: 404, calls: 1}} {
		t.Run(tc.mode+fmt.Sprint(tc.want, tc.revoke), func(t *testing.T) {
			setup := &trainingSetupFake{statusErr: tc.statusErr, err: tc.authErr, revokeOnCapability: tc.revoke}
			currentGroup := g
			if tc.empty {
				currentGroup.MemberCount = 0
			}
			groups := &taskGroupsFake{group: currentGroup, rows: []TrainingGroupAthlete{{MembershipID: uuid.New(), Name: "Visible eligible athlete"}}}
			h := TrainingGroups{Store: groups, Setup: setup}
			target := trainingGroupsPath + "/" + group.String() + "?week_id=" + week.String() + "&day=2026-11-14&q=Ana&season_id=" + taskGroupSeason.String() + "&effective_on=2026-11-14"
			r := setupRequest("GET", target, nil, actor, group)
			w := httptest.NewRecorder()
			switch tc.mode {
			case "detail":
				h.Detail(w, r)
			case "rename":
				h.RenamePage(w, r)
			case "add":
				h.Athletes(w, r)
			}
			if w.Code != tc.want || setup.capabilities != tc.calls || strings.Contains(w.Body.String(), "private optional query diagnostic") {
				t.Fatalf("status=%d expected=%d optional reads=%d expected=%d", w.Code, tc.want, setup.capabilities, tc.calls)
			}
			if tc.want == 200 && !strings.Contains(w.Body.String(), "day=2026-11-14") {
				t.Fatal("ready task lost exact context")
			}
			if tc.want == 404 && (strings.Contains(w.Body.String(), g.Name) || strings.Contains(w.Body.String(), "Visible eligible athlete")) {
				t.Fatal("revoked scope retained protected task data")
			}
			if tc.mode == "add" && tc.want == 200 && (!strings.Contains(w.Body.String(), "Visible eligible athlete") || !strings.Contains(w.Body.String(), "Não foi possível confirmar as opções")) {
				t.Fatal("optional help failure blocked authorized roster/search")
			}
		})
	}
}
func TestDefinitionsEarlyReadErrorsRetainPrivacyHeaders(t *testing.T) {
	c := setupFixtureContext()
	w := httptest.NewRecorder()
	h := Definitions{Store: &definitionFake{err: errors.New("private definitions failure")}, Setup: &trainingSetupFake{}}
	h.Get(w, setupRequest("GET", setupContextURL("/equipa/escaloes", c), nil, uuid.New(), uuid.Nil))
	if w.Code != 503 || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" || strings.Contains(w.Body.String(), "private definitions failure") {
		t.Fatalf("status=%d headers=%v", w.Code, w.Header())
	}
}
