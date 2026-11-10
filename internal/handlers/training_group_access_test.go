package handlers

import (
	"errors"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestTrainingGroupIndexKeepsScopedPagination(t *testing.T) {
	s := taskGroupsFixture()
	week := uuid.New()
	s.group.LatestWeekID = &week
	s.groups = make([]TrainingGroupRecord, 20)
	for i := range s.groups {
		s.groups[i] = s.group
		s.groups[i].ID = uuid.New()
		s.groups[i].Name = fmt.Sprintf("Grupo autorizado %d", i)
	}
	w := groupRequest(t, s, "GET", trainingGroupsPath+"?page=2", nil, TrainingGroups.Index)
	body := w.Body.String()
	if w.Code != 200 || s.listOffset != 40 || !strings.Contains(body, "Grupo autorizado 19") || !strings.Contains(body, "page=1") || !strings.Contains(body, "page=3") || !strings.Contains(body, trainingGroupsPath+"/"+s.groups[19].ID.String()) {
		t.Fatalf("index status=%d offset=%d", w.Code, s.listOffset)
	}
	for _, tc := range []struct {
		name, path         string
		listErr, scopesErr error
		status             int
	}{
		{"outside bounds", trainingGroupsPath + "?page=100", nil, nil, 404},
		{"scope revoked", trainingGroupsPath, pgx.ErrNoRows, nil, 404},
		{"scope lookup failed", trainingGroupsPath, nil, errors.New("private scope"), 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s.listErr, s.scopesErr = tc.listErr, tc.scopesErr
			w := groupRequest(t, s, "GET", tc.path, nil, TrainingGroups.Index)
			if w.Code != tc.status || strings.Contains(w.Body.String(), "Grupo autorizado") || strings.Contains(w.Body.String(), "private scope") {
				t.Fatalf("index status=%d leaked scope", w.Code)
			}
		})
	}
}

func TestTrainingGroupRosterPaginationRetainsOnlyValidatedWeek(t *testing.T) {
	s := taskGroupsFixture()
	week := uuid.New()
	s.allowedWeeks = map[uuid.UUID]bool{week: true}
	s.rows = make([]TrainingGroupAthlete, 20)
	for i := range s.rows {
		s.rows[i] = TrainingGroupAthlete{UserID: uuid.New(), GroupID: s.group.ID, Name: fmt.Sprintf("Pessoa autorizada %d", i), AlreadyMember: true}
	}
	path := trainingGroupsPath + "/" + s.group.ID.String() + "?page=2&week_id=" + week.String()
	w := groupRequest(t, s, "GET", path, nil, TrainingGroups.Detail)
	body := w.Body.String()
	if w.Code != 200 || s.rosterOffset != 40 || !strings.Contains(body, "Pessoa autorizada 19") || !strings.Contains(body, "page=1") || !strings.Contains(body, "page=3") || !strings.Contains(body, "week_id="+week.String()) {
		t.Fatalf("roster status=%d offset=%d", w.Code, s.rosterOffset)
	}
	s.rosterErr = pgx.ErrNoRows
	denied := groupRequest(t, s, "GET", path, nil, TrainingGroups.Detail)
	if denied.Code != 404 || strings.Contains(denied.Body.String(), "Pessoa autorizada") {
		t.Fatal("revoked roster retained private people")
	}
}

func TestTrainingGroupTeamScopeAndInvalidRenameStayInTheirTasks(t *testing.T) {
	s := taskGroupsFixture()
	team := uuid.New()
	s.scopes = []TrainingGroupScope{{TeamID: &team, Name: "Equipa sintética"}}
	opened := groupRequest(t, s, "GET", trainingGroupsPath+"/criar", nil, TrainingGroups.CreatePage)
	if opened.Code != 200 || !strings.Contains(opened.Body.String(), "team:"+team.String()) {
		t.Fatal("configured team scope unavailable")
	}
	s.writeErr = ErrTrainingGroupEligibility
	response := groupRequest(t, s, "POST", trainingGroupsPath+"/criar", url.Values{"name": {"Nome proposto"}, "scope": {"team:" + team.String()}}, TrainingGroups.Create)
	if response.Code != 422 || s.create.TeamID == nil || *s.create.TeamID != team || s.create.ProgrammeID != nil || !strings.Contains(response.Body.String(), "Nome proposto") {
		t.Fatalf("stale team scope status=%d input=%+v", response.Code, s.create)
	}
	s.scopes = nil
	response = groupRequest(t, s, "POST", trainingGroupsPath+"/criar", url.Values{"name": {"Nome privado"}, "scope": {"foreign:" + team.String()}}, TrainingGroups.Create)
	if response.Code != 404 || strings.Contains(response.Body.String(), "Nome privado") {
		t.Fatal("revoked scope retained proposed name")
	}
	path := trainingGroupsPath + "/" + s.group.ID.String() + "/nome"
	rename := groupRequest(t, s, "GET", path, nil, TrainingGroups.RenamePage)
	if rename.Code != 200 || !strings.Contains(rename.Body.String(), s.group.Name) {
		t.Fatal("authorized rename editor unavailable")
	}
	invalid := groupRequest(t, s, "POST", path, url.Values{"name": {"x"}, "version": {"bad"}}, TrainingGroups.Rename)
	if invalid.Code != 422 || s.renamed != 0 {
		t.Fatal("invalid rename reached store")
	}
	stale := groupRequest(t, s, "POST", path, url.Values{"name": {"Nome válido"}, "version": {s.group.UpdatedAt.Format(time.RFC3339Nano)}}, TrainingGroups.Rename)
	if stale.Code != 422 || s.renamed != 1 || !strings.Contains(stale.Body.String(), "Nome válido") {
		t.Fatal("eligibility failure lost proposal")
	}
}

func TestTrainingGroupRosterWritesRespectProofAndFailureOutcome(t *testing.T) {
	for _, tc := range []struct {
		name           string
		storeErr       error
		status, writes int
		duplicate      bool
	}{
		{"confirmed", nil, 303, 1, false},
		{"unknown commit", db.ErrCommitOutcomeUnknown, 503, 1, false},
		{"no longer eligible", ErrTrainingGroupEligibility, 422, 1, false},
		{"denied", pgx.ErrNoRows, 404, 1, false},
		{"repeated proof field", nil, 422, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := taskGroupsFixture()
			s.writeErr = tc.storeErr
			member := uuid.New()
			s.rows = []TrainingGroupAthlete{{MembershipID: member, Name: "Pessoa privada da seleção"}}
			v := url.Values{"version": {s.group.UpdatedAt.Format(time.RFC3339Nano)}, "q": {"Pessoa"}, "season_id": {taskGroupSeason.String()}, "effective_on": {"2026-12-15"}, "command_id": {uuid.NewString()}, "selection_proof": {"opaque-fixture-selection-proof"}, "membership_id": {member.String()}}
			if tc.duplicate {
				v.Add("command_id", uuid.NewString())
			}
			w := groupRequest(t, s, "POST", trainingGroupsPath+"/"+s.group.ID.String()+"/atletas", v, TrainingGroups.AddAthletes)
			if w.Code != tc.status || s.added != tc.writes {
				t.Fatalf("status=%d attempts=%d", w.Code, s.added)
			}
			if (tc.status == 404 || tc.status == 503) && (strings.Contains(w.Body.String(), "Pessoa privada da seleção") || s.searched != 0) {
				t.Fatal("failed authorization or unknown outcome redisclosed roster")
			}
			if tc.status == 303 && w.Header().Get("Location") != trainingGroupsPath+"/"+s.group.ID.String() {
				t.Fatal("successful add did not return to scoped group")
			}
		})
	}
}

func TestTrainingGroupRosterSearchPageBindsContextAndRefusesRevokedProof(t *testing.T) {
	s := taskGroupsFixture()
	s.rows = make([]TrainingGroupAthlete, 20)
	for i := range s.rows {
		s.rows[i] = TrainingGroupAthlete{MembershipID: uuid.New(), UserID: uuid.New(), GroupID: s.group.ID, Name: fmt.Sprintf("Pessoa elegível %d", i)}
	}
	path := trainingGroupsPath + "/" + s.group.ID.String() + "/atletas?" + url.Values{"page": {"2"}, "q": {"Pessoa"}, "season_id": {taskGroupSeason.String()}, "effective_on": {"2026-12-15"}}.Encode()
	w := groupRequest(t, s, "GET", path, nil, TrainingGroups.Athletes)
	body := w.Body.String()
	if w.Code != 200 || s.searchOffset != 40 || !strings.Contains(body, "opaque-fixture-selection-proof") || !strings.Contains(body, "page=1") || !strings.Contains(body, "page=3") || !strings.Contains(body, "effective_on=2026-12-15") {
		t.Fatalf("search status=%d offset=%d", w.Code, s.searchOffset)
	}
	for _, tc := range []struct {
		name                    string
		seasons, search, review error
		status                  int
	}{
		{"revoked offer", nil, nil, pgx.ErrNoRows, 404},
		{"search unavailable", nil, errTrainingPlacementUnavailable, nil, 503},
		{"seasons unavailable", errTrainingPlacementUnavailable, nil, nil, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s.seasonsErr, s.searchErr, s.reviewErr = tc.seasons, tc.search, tc.review
			w := groupRequest(t, s, "GET", path, nil, TrainingGroups.Athletes)
			if w.Code != tc.status || strings.Contains(w.Body.String(), "Pessoa elegível") || strings.Contains(w.Body.String(), "opaque-fixture-selection-proof") {
				t.Fatalf("status=%d disclosed offer", w.Code)
			}
		})
	}
	denied := httptest.NewRecorder()
	(TrainingGroups{Store: s}).Index(denied, httptest.NewRequest("GET", trainingGroupsPath, nil))
	if denied.Code != 404 || denied.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("anonymous list was not denied")
	}
}
