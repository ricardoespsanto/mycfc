package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type taskGroupsFake struct {
	group                             TrainingGroupRecord
	scopes                            []TrainingGroupScope
	rows                              []TrainingGroupAthlete
	groupErr, writeErr                error
	create                            TrainingGroupCreateInput
	rename                            TrainingGroupRenameInput
	roster                            TrainingGroupRosterInput
	created, renamed, added, searched int
	addedCount                        int
	gets                              int
	revokeAfterWrite                  bool
	allowedWeeks                      map[uuid.UUID]bool
}

func (s *taskGroupsFake) AllowsWeek(_ context.Context, _, _, week uuid.UUID) (bool, error) {
	return s.allowedWeeks[week], nil
}

func (s *taskGroupsFake) ListGroups(context.Context, uuid.UUID, int32) ([]TrainingGroupRecord, error) {
	return []TrainingGroupRecord{s.group}, nil
}
func (s *taskGroupsFake) ListScopes(context.Context, uuid.UUID) ([]TrainingGroupScope, error) {
	return s.scopes, nil
}
func (s *taskGroupsFake) GetGroup(context.Context, uuid.UUID, uuid.UUID) (TrainingGroupRecord, error) {
	s.gets++
	if s.revokeAfterWrite && s.added > 0 {
		return TrainingGroupRecord{}, pgx.ErrNoRows
	}
	return s.group, s.groupErr
}
func (s *taskGroupsFake) ListRoster(context.Context, uuid.UUID, uuid.UUID, int32) ([]TrainingGroupAthlete, error) {
	return s.rows, nil
}
func (s *taskGroupsFake) SearchAthletes(context.Context, uuid.UUID, uuid.UUID, string, int32) ([]TrainingGroupAthlete, error) {
	s.searched++
	return s.rows, nil
}
func (s *taskGroupsFake) CreateGroup(_ context.Context, in TrainingGroupCreateInput) (TrainingGroupRecord, error) {
	s.created++
	s.create = in
	return s.group, s.writeErr
}
func (s *taskGroupsFake) RenameGroup(_ context.Context, in TrainingGroupRenameInput) (TrainingGroupRecord, error) {
	s.renamed++
	s.rename = in
	return s.group, s.writeErr
}
func (s *taskGroupsFake) AddAthletes(_ context.Context, in TrainingGroupRosterInput) (TrainingGroupRosterResult, error) {
	s.added++
	s.roster = in
	return TrainingGroupRosterResult{Group: s.group, Added: s.addedCount}, s.writeErr
}
func groupRequest(t *testing.T, s *taskGroupsFake, method, path string, values url.Values, handler func(TrainingGroups, http.ResponseWriter, *http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetPathValue("id", s.group.ID.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: uuid.MustParse("39100000-0000-0000-0000-000000000001"), Name: "Coach", IsAdmin: true}))
	w := httptest.NewRecorder()
	handler(TrainingGroups{Store: s}, w, r)
	return w
}
func taskGroupsFixture() *taskGroupsFake {
	p := uuid.New()
	return &taskGroupsFake{group: TrainingGroupRecord{ID: uuid.New(), Name: "Iniciados e Infantis", ScopeName: "Competição", ProgrammeID: &p, UpdatedAt: time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)}, scopes: []TrainingGroupScope{{ProgrammeID: &p, Name: "Competição"}}}
}
func TestTrainingGroupEmptyCreateUsesConfiguredScopeAndActor(t *testing.T) {
	s := taskGroupsFixture()
	w := groupRequest(t, s, "POST", trainingGroupsPath+"/criar", url.Values{"name": {"  Iniciados e Infantis  "}, "scope": {"programme:" + s.scopes[0].ProgrammeID.String()}, "return_to": {"https://example.test/steal"}}, func(h TrainingGroups, w http.ResponseWriter, r *http.Request) { h.Create(w, r) })
	if w.Code != 303 || s.created != 1 || s.create.Name != "Iniciados e Infantis" || s.create.ActorID != uuid.MustParse("39100000-0000-0000-0000-000000000001") || s.create.ProgrammeID == nil {
		t.Fatalf("status=%d create=%#v", w.Code, s.create)
	}
	if w.Header().Get("Location") != trainingGroupsPath+"/"+s.group.ID.String() {
		t.Fatal(w.Header())
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal(w.Header())
	}
}
func TestTrainingGroupCreationRetainsInvalidInputWithoutWrite(t *testing.T) {
	s := taskGroupsFixture()
	w := groupRequest(t, s, "POST", trainingGroupsPath+"/criar", url.Values{"name": {"Proposta retida"}, "scope": {"team:not-an-id"}}, func(h TrainingGroups, w http.ResponseWriter, r *http.Request) { h.Create(w, r) })
	if w.Code != 422 || s.created != 0 || !strings.Contains(w.Body.String(), "Proposta retida") {
		t.Fatalf("status=%d writes=%d %s", w.Code, s.created, w.Body.String())
	}
}
func TestTrainingGroupRosterBlankAndBoundedSearch(t *testing.T) {
	for _, q := range []string{"", strings.Repeat("x", 101)} {
		s := taskGroupsFixture()
		w := groupRequest(t, s, "GET", trainingGroupsPath+"/"+s.group.ID.String()+"/atletas?q="+url.QueryEscape(q), nil, func(h TrainingGroups, w http.ResponseWriter, r *http.Request) { h.Athletes(w, r) })
		if w.Code != 200 || s.searched != 0 {
			t.Fatalf("status=%d search=%d", w.Code, s.searched)
		}
	}
}
func TestTrainingGroupAddConflictRetainsOriginalVersionAndSafeProposal(t *testing.T) {
	s := taskGroupsFixture()
	s.writeErr = ErrTrainingGroupConflict
	member := uuid.New()
	s.rows = []TrainingGroupAthlete{{MembershipID: member, Name: "Atleta elegível"}}
	original := s.group.UpdatedAt.Add(-time.Hour).Format(time.RFC3339Nano)
	w := groupRequest(t, s, "POST", trainingGroupsPath+"/"+s.group.ID.String()+"/atletas", url.Values{"version": {original}, "q": {"Atleta"}, "membership_id": {member.String()}}, func(h TrainingGroups, w http.ResponseWriter, r *http.Request) { h.AddAthletes(w, r) })
	if w.Code != 409 || s.added != 1 || s.roster.ExpectedUpdatedAt.Format(time.RFC3339Nano) != original || !strings.Contains(w.Body.String(), `value="`+original+`"`) || !strings.Contains(w.Body.String(), "Atleta elegível") {
		t.Fatalf("status=%d %s", w.Code, w.Body.String())
	}
}
func TestTrainingGroupRevocationDoesNotRediscloseRetainedRoster(t *testing.T) {
	s := taskGroupsFixture()
	s.writeErr = ErrTrainingGroupConflict
	s.revokeAfterWrite = true
	member := uuid.New()
	s.rows = []TrainingGroupAthlete{{MembershipID: member, Name: "PRIVATE PERSON"}}
	w := groupRequest(t, s, "POST", trainingGroupsPath+"/"+s.group.ID.String()+"/atletas", url.Values{"version": {s.group.UpdatedAt.Format(time.RFC3339Nano)}, "q": {"PRIVATE PERSON"}, "membership_id": {member.String()}}, func(h TrainingGroups, w http.ResponseWriter, r *http.Request) { h.AddAthletes(w, r) })
	if w.Code != 404 || s.searched != 0 || strings.Contains(w.Body.String(), "PRIVATE PERSON") {
		t.Fatalf("status=%d search=%d %s", w.Code, s.searched, w.Body.String())
	}
}
func TestTrainingGroupDuplicateAndOversizeBatchNeverReachWrite(t *testing.T) {
	for _, ids := range [][]string{{uuid.Nil.String()}, {"not-a-uuid"}, {uuid.MustParse("39100000-0000-0000-0000-000000000002").String(), uuid.MustParse("39100000-0000-0000-0000-000000000002").String()}, make([]string, 21)} {
		s := taskGroupsFixture()
		w := groupRequest(t, s, "POST", trainingGroupsPath+"/"+s.group.ID.String()+"/atletas", url.Values{"version": {s.group.UpdatedAt.Format(time.RFC3339Nano)}, "membership_id": ids}, func(h TrainingGroups, w http.ResponseWriter, r *http.Request) { h.AddAthletes(w, r) })
		if w.Code != 422 || s.added != 0 {
			t.Fatalf("status=%d writes=%d", w.Code, s.added)
		}
	}
}
func TestTrainingGroupRenameConflictKeepsOriginalVersion(t *testing.T) {
	s := taskGroupsFixture()
	s.writeErr = ErrTrainingGroupConflict
	original := s.group.UpdatedAt.Add(-time.Hour).Format(time.RFC3339Nano)
	w := groupRequest(t, s, "POST", trainingGroupsPath+"/"+s.group.ID.String()+"/nome", url.Values{"version": {original}, "name": {"Nome proposto"}}, func(h TrainingGroups, w http.ResponseWriter, r *http.Request) { h.Rename(w, r) })
	if w.Code != 409 || s.renamed != 1 || s.rename.Name != "Nome proposto" || !strings.Contains(w.Body.String(), `value="`+original+`"`) || !strings.Contains(w.Body.String(), "Nome proposto") {
		t.Fatalf("status=%d %s", w.Code, w.Body.String())
	}
}
func TestTrainingGroupInternalStoreFailureIsNotTreatedAsEligibility(t *testing.T) {
	s := taskGroupsFixture()
	s.writeErr = errors.New("private diagnostic")
	w := groupRequest(t, s, "POST", trainingGroupsPath+"/criar", url.Values{"name": {"Grupo válido"}, "scope": {"programme:" + s.scopes[0].ProgrammeID.String()}}, func(h TrainingGroups, w http.ResponseWriter, r *http.Request) { h.Create(w, r) })
	if w.Code != 500 || strings.Contains(w.Body.String(), "private diagnostic") {
		t.Fatalf("status=%d %s", w.Code, w.Body.String())
	}
}

func TestTrainingGroupReturnContextRequiresVerifiedWeekAndIgnoresArbitraryURL(t *testing.T) {
	for _, allowed := range []bool{false, true} {
		s := taskGroupsFixture()
		week := uuid.New()
		s.allowedWeeks = map[uuid.UUID]bool{week: allowed}
		w := groupRequest(t, s, "POST", trainingGroupsPath+"/"+s.group.ID.String()+"/nome", url.Values{"name": {"Nome novo"}, "version": {s.group.UpdatedAt.Format(time.RFC3339Nano)}, "week_id": {week.String()}, "return_to": {"https://example.test/untrusted"}}, func(h TrainingGroups, w http.ResponseWriter, r *http.Request) { h.Rename(w, r) })
		expected := trainingGroupsPath + "/" + s.group.ID.String()
		if allowed {
			expected += "?week_id=" + week.String()
		}
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != expected {
			t.Fatalf("allowed=%v status=%d location=%s", allowed, w.Code, w.Header().Get("Location"))
		}
	}
}
