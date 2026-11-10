package handlers

import (
	"context"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type seasonStoreStub struct {
	authErr, createErr, selectErr error
	state                         SeasonSelection
	created                       SeasonInput
	createCount, selectCount      int
	expected                      string
}

func (s *seasonStoreStub) Authorize(context.Context, uuid.UUID) error { return s.authErr }
func (s *seasonStoreStub) ListSeasons(context.Context, uuid.UUID, int32) ([]dbgen.Season, error) {
	return []dbgen.Season{s.state.Target}, s.authErr
}
func (s *seasonStoreStub) GetSelectionState(context.Context, uuid.UUID, uuid.UUID) (SeasonSelection, error) {
	return s.state, s.authErr
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
	return s.state.Current != nil, s.authErr
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
