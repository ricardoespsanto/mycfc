package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
)

type structuredTrainingStoreStub struct {
	StructuredTrainingStore
	manageable bool
	manageArgs []dbgen.CanManageStructuredTrainingGroupParams
	created    []dbgen.CreateStructuredTrainingWeekParams
}

func (s *structuredTrainingStoreStub) CanManageStructuredTrainingGroup(_ context.Context, params dbgen.CanManageStructuredTrainingGroupParams) (bool, error) {
	s.manageArgs = append(s.manageArgs, params)
	return s.manageable || params.IsAdmin, nil
}

func (s *structuredTrainingStoreStub) CreateStructuredTrainingWeek(_ context.Context, params dbgen.CreateStructuredTrainingWeekParams) (dbgen.TrainingPlan, error) {
	s.created = append(s.created, params)
	return dbgen.TrainingPlan{ID: uuid.New(), Title: params.Title, TrainingGroupID: &params.GroupID, WeekStart: params.WeekStart}, nil
}

func TestAssembleStructuredTrainingPreservesHybridHierarchy(t *testing.T) {
	groupID, planID, sessionID := uuid.New(), uuid.New(), uuid.New()
	gymID, waterID, warmupID, mainID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	weekStart := time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC)
	startsAt := time.Date(2026, time.August, 11, 17, 0, 0, 0, time.UTC)
	endsAt := startsAt.Add(2 * time.Hour)
	rows := []structuredTrainingRow{
		{athleteName: "Atleta", groupID: &groupID, groupName: "Cadetes", scope: "Plano atribuído", planID: &planID, planTitle: "M33", seasonName: "2025/2026", weekStart: weekStart, sessionID: &sessionID, sessionTitle: "Ginásio + água", startsAt: startsAt, endsAt: endsAt, entryKind: "TRAINING", segmentID: &gymID, segmentPosition: 1, modality: "GYM", blockID: &warmupID, blockPosition: 1, blockPurpose: "WARM_UP", instructions: "Mobilidade"},
		{athleteName: "Atleta", groupID: &groupID, groupName: "Cadetes", scope: "Plano atribuído", planID: &planID, planTitle: "M33", seasonName: "2025/2026", weekStart: weekStart, sessionID: &sessionID, sessionTitle: "Ginásio + água", startsAt: startsAt, endsAt: endsAt, entryKind: "TRAINING", segmentID: &waterID, segmentPosition: 2, modality: "WATER", blockID: &mainID, blockPosition: 1, blockPurpose: "MAIN", instructions: "3x2' R4"},
	}

	audiences := assembleStructuredTraining(rows, time.UTC)
	if len(audiences) != 1 || len(audiences[0].Weeks) != 1 || len(audiences[0].Weeks[0].Sessions) != 1 {
		t.Fatalf("unexpected hierarchy: %#v", audiences)
	}
	segments := audiences[0].Weeks[0].Sessions[0].Segments
	if len(segments) != 2 || segments[0].Modality != "GYM" || segments[1].Modality != "WATER" {
		t.Fatalf("segments = %#v", segments)
	}
	if segments[1].Blocks[0].Instructions != "3x2' R4" {
		t.Fatalf("water instructions = %q", segments[1].Blocks[0].Instructions)
	}
	if audiences[0].Weeks[0].Season != "2025/2026" {
		t.Fatalf("season = %q", audiences[0].Weeks[0].Season)
	}
}

func TestCreateStructuredWeekEnforcesGroupAuthorization(t *testing.T) {
	groupID, userID := uuid.New(), uuid.New()
	body := "group_id=" + groupID.String() + "&title=Microciclo+41&week_start=2026-08-17"
	for _, tc := range []struct {
		name       string
		user       CurrentUser
		manageable bool
		wantStatus int
		wantCreate bool
	}{
		{name: "unrelated coach", user: CurrentUser{ID: userID}, wantStatus: http.StatusForbidden},
		{name: "scoped coach", user: CurrentUser{ID: userID}, manageable: true, wantStatus: http.StatusSeeOther, wantCreate: true},
		{name: "administrator", user: CurrentUser{ID: userID, IsAdmin: true}, wantStatus: http.StatusSeeOther, wantCreate: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &structuredTrainingStoreStub{manageable: tc.manageable}
			handler := StructuredTraining{Store: store, Location: time.UTC, System: System{}}
			request := httptest.NewRequest(http.MethodPost, "/admin/treinos/estruturados/semanas", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request = request.WithContext(context.WithValue(request.Context(), currentUserKey{}, tc.user))
			response := httptest.NewRecorder()

			handler.CreateWeek(response, request)

			if response.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, tc.wantStatus)
			}
			if (len(store.created) == 1) != tc.wantCreate {
				t.Fatalf("created = %d, wantCreate %t", len(store.created), tc.wantCreate)
			}
			if len(store.manageArgs) != 1 || store.manageArgs[0].GroupID != groupID || store.manageArgs[0].IsAdmin != tc.user.IsAdmin {
				t.Fatalf("authorization args = %#v", store.manageArgs)
			}
			if tc.wantCreate && (!store.created[0].WeekStart.Valid || !store.created[0].WeekStart.Time.Equal(time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC))) {
				t.Fatalf("week start = %#v", store.created[0].WeekStart)
			}
		})
	}
}

func TestStructuredScopeAllowedFailsClosed(t *testing.T) {
	programmeID, teamID := uuid.New(), uuid.New()
	coach := CurrentUser{CoachProgrammeIDs: map[uuid.UUID]bool{programmeID: true}, CoachTeamIDs: map[uuid.UUID]bool{}}
	if !structuredScopeAllowed(coach, &programmeID, nil) {
		t.Fatal("explicit programme grant should be allowed")
	}
	if structuredScopeAllowed(coach, nil, &teamID) || structuredScopeAllowed(coach, nil, nil) || structuredScopeAllowed(coach, &programmeID, &teamID) {
		t.Fatal("ungranted, empty, and ambiguous scopes must be rejected")
	}
}

func TestStructuredTrainingEnumsFailClosed(t *testing.T) {
	if !validTrainingEntryKind(dbgen.TrainingEntryKindTRAINING) || validTrainingEntryKind(dbgen.TrainingEntryKind("UNKNOWN")) {
		t.Fatal("training entry kind validation did not fail closed")
	}
	if !validTrainingSegmentModality(dbgen.TrainingSegmentModalityGYM) || validTrainingSegmentModality(dbgen.TrainingSegmentModality("UNKNOWN")) {
		t.Fatal("segment modality validation did not fail closed")
	}
	if !validTrainingBlockPurpose(dbgen.TrainingBlockPurposeCOOLDOWN) || validTrainingBlockPurpose(dbgen.TrainingBlockPurpose("UNKNOWN")) {
		t.Fatal("block purpose validation did not fail closed")
	}
}

func TestOptionalPositiveInt32(t *testing.T) {
	if value, err := optionalPositiveInt32("", 1440); err != nil || value != nil {
		t.Fatalf("empty value = %v, %v", value, err)
	}
	if _, err := optionalPositiveInt32("0", 1440); err == nil {
		t.Fatal("zero should be rejected")
	}
	if value, err := optionalPositiveInt32("90", 1440); err != nil || value == nil || *value != 90 {
		t.Fatalf("valid value = %v, %v", value, err)
	}
}
