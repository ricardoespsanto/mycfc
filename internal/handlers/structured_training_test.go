package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
)

type structuredTrainingStoreStub struct {
	StructuredTrainingStore
	manageable     bool
	manageArgs     []dbgen.CanManageStructuredTrainingGroupParams
	created        []dbgen.CreateStructuredTrainingWeekParams
	groups         []StructuredTrainingGroupInput
	weekOK         bool
	sessions       []dbgen.CreateStructuredTrainingSessionParams
	segments       []StructuredTrainingSegmentInput
	blocks         []dbgen.CreateTrainingSegmentBlockParams
	gymBlocks      []StructuredGymBlockInput
	gymExercises   []dbgen.CreateGymExerciseParams
	planID         uuid.UUID
	movedSegments  []dbgen.MoveTrainingSessionSegmentParams
	movedBlocks    []dbgen.MoveTrainingSegmentBlockParams
	movedExercises []dbgen.MoveGymExerciseParams
}

func (s *structuredTrainingStoreStub) CreateGroup(_ context.Context, input StructuredTrainingGroupInput) (dbgen.TrainingGroup, error) {
	s.groups = append(s.groups, input)
	return dbgen.TrainingGroup{ID: uuid.New(), Name: input.Params.Name}, nil
}

func (s *structuredTrainingStoreStub) CanManageStructuredTrainingGroup(_ context.Context, params dbgen.CanManageStructuredTrainingGroupParams) (bool, error) {
	s.manageArgs = append(s.manageArgs, params)
	return s.manageable || params.IsAdmin, nil
}

func (s *structuredTrainingStoreStub) CreateStructuredTrainingWeek(_ context.Context, params dbgen.CreateStructuredTrainingWeekParams) (dbgen.TrainingPlan, error) {
	s.created = append(s.created, params)
	return dbgen.TrainingPlan{ID: uuid.New(), Title: params.Title, TrainingGroupID: &params.GroupID, WeekStart: params.WeekStart}, nil
}

func (s *structuredTrainingStoreStub) CanManageStructuredTrainingWeek(_ context.Context, _ dbgen.CanManageStructuredTrainingWeekParams) (bool, error) {
	return s.weekOK, nil
}

func (s *structuredTrainingStoreStub) CreateStructuredTrainingSession(_ context.Context, params dbgen.CreateStructuredTrainingSessionParams) (dbgen.TrainingSession, error) {
	s.sessions = append(s.sessions, params)
	return dbgen.TrainingSession{ID: uuid.New(), PlanID: params.PlanID, Title: params.Title}, nil
}

func (s *structuredTrainingStoreStub) GetStructuredSessionPlanID(context.Context, uuid.UUID) (uuid.UUID, error) {
	return s.planID, nil
}

func (s *structuredTrainingStoreStub) GetStructuredSegmentPlanID(context.Context, uuid.UUID) (uuid.UUID, error) {
	return s.planID, nil
}

func (s *structuredTrainingStoreStub) GetStructuredBlockPlanID(context.Context, uuid.UUID) (uuid.UUID, error) {
	return s.planID, nil
}

func (s *structuredTrainingStoreStub) GetGymExercisePlanID(context.Context, uuid.UUID) (uuid.UUID, error) {
	return s.planID, nil
}

func (s *structuredTrainingStoreStub) CreateSegment(_ context.Context, input StructuredTrainingSegmentInput) (uuid.UUID, error) {
	s.segments = append(s.segments, input)
	return uuid.New(), nil
}

func (s *structuredTrainingStoreStub) CreateTrainingSegmentBlock(_ context.Context, params dbgen.CreateTrainingSegmentBlockParams) (uuid.UUID, error) {
	s.blocks = append(s.blocks, params)
	return uuid.New(), nil
}

func (s *structuredTrainingStoreStub) CreateGymBlock(_ context.Context, input StructuredGymBlockInput) (uuid.UUID, error) {
	s.gymBlocks = append(s.gymBlocks, input)
	return uuid.New(), nil
}

func (s *structuredTrainingStoreStub) CreateGymExercise(_ context.Context, params dbgen.CreateGymExerciseParams) (uuid.UUID, error) {
	s.gymExercises = append(s.gymExercises, params)
	return uuid.New(), nil
}

func (s *structuredTrainingStoreStub) MoveTrainingSessionSegment(_ context.Context, params dbgen.MoveTrainingSessionSegmentParams) (bool, error) {
	s.movedSegments = append(s.movedSegments, params)
	return true, nil
}

func (s *structuredTrainingStoreStub) MoveTrainingSegmentBlock(_ context.Context, params dbgen.MoveTrainingSegmentBlockParams) (bool, error) {
	s.movedBlocks = append(s.movedBlocks, params)
	return true, nil
}

func (s *structuredTrainingStoreStub) MoveGymExercise(_ context.Context, params dbgen.MoveGymExerciseParams) (bool, error) {
	s.movedExercises = append(s.movedExercises, params)
	return true, nil
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
	if modalities := audiences[0].Weeks[0].Sessions[0].Modalities; len(modalities) != 2 || modalities[0] != "GYM" || modalities[1] != "WATER" {
		t.Fatalf("derived modalities = %#v", modalities)
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

func TestStructuredTrainingAuthoringHandlersPersistValidHierarchy(t *testing.T) {
	userID, programmeID, membershipID := uuid.New(), uuid.New(), uuid.New()
	planID, sessionID, segmentID, blockID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	store := &structuredTrainingStoreStub{manageable: true, weekOK: true, planID: planID}
	handler := StructuredTraining{Store: store, Location: time.UTC, System: System{}}
	user := CurrentUser{ID: userID, CoachProgrammeIDs: map[uuid.UUID]bool{programmeID: true}}

	groupValues := url.Values{
		"name":          {"Cadetes"},
		"programme_id":  {programmeID.String()},
		"membership_id": {membershipID.String()},
	}
	response := performStructuredTrainingRequest(t, user, http.MethodPost, "/admin/treinos/estruturados/grupos", groupValues, "", "", handler.CreateGroup)
	if response.Code != http.StatusSeeOther || len(store.groups) != 1 || store.groups[0].Params.ProgrammeID == nil || *store.groups[0].Params.ProgrammeID != programmeID || len(store.groups[0].MembershipIDs) != 1 {
		t.Fatalf("group response=%d inputs=%#v", response.Code, store.groups)
	}

	sessionValues := url.Values{
		"plan_id":     {planID.String()},
		"title":       {"Ginásio + água"},
		"description": {"Sessão híbrida"},
		"starts_at":   {"2026-08-18T17:00"},
		"ends_at":     {"2026-08-18T19:00"},
		"entry_kind":  {"TRAINING"},
	}
	response = performStructuredTrainingRequest(t, user, http.MethodPost, "/admin/treinos/estruturados/sessoes", sessionValues, "", "", handler.CreateSession)
	if response.Code != http.StatusSeeOther || len(store.sessions) != 1 || store.sessions[0].PlanID != planID || store.sessions[0].EntryKind != dbgen.TrainingEntryKindTRAINING {
		t.Fatalf("session response=%d inputs=%#v", response.Code, store.sessions)
	}

	segmentValues := url.Values{
		"modality":                     {"WATER"},
		"title":                        {"Ataque e defesa"},
		"location":                     {"Mondego"},
		"planned_duration_minutes":     {"90"},
		"planned_start_offset_minutes": {"20"},
		"transition_duration_minutes":  {"5"},
		"equipment_notes":              {"Colete e bola"},
		"purpose":                      {"MAIN"},
		"block_title":                  {"Jogo condicionado"},
		"instructions":                 {"2x7' HxH com guarda-redes e pivot"},
	}
	response = performStructuredTrainingRequest(t, user, http.MethodPost, "/admin/treinos/estruturados/sessoes/"+sessionID.String()+"/segmentos", segmentValues, "id", sessionID.String(), handler.CreateSegment)
	if response.Code != http.StatusSeeOther || len(store.segments) != 1 || store.segments[0].Segment.Modality != dbgen.TrainingSegmentModalityWATER || store.segments[0].Segment.PlannedDurationMinutes == nil || *store.segments[0].Segment.PlannedDurationMinutes != 90 || store.segments[0].Segment.PlannedStartOffsetMinutes == nil || *store.segments[0].Segment.PlannedStartOffsetMinutes != 20 || store.segments[0].Segment.TransitionDurationMinutes == nil || *store.segments[0].Segment.TransitionDurationMinutes != 5 || store.segments[0].Segment.EquipmentNotes != "Colete e bola" {
		t.Fatalf("segment response=%d inputs=%#v", response.Code, store.segments)
	}

	blockValues := url.Values{"purpose": {"COOL_DOWN"}, "title": {"Retorno à calma"}, "instructions": {"Remar suave até completar 12 km"}}
	response = performStructuredTrainingRequest(t, user, http.MethodPost, "/admin/treinos/estruturados/segmentos/"+segmentID.String()+"/blocos", blockValues, "id", segmentID.String(), handler.CreateBlock)
	if response.Code != http.StatusSeeOther || len(store.blocks) != 1 || store.blocks[0].SegmentID != segmentID || store.blocks[0].Purpose != dbgen.TrainingBlockPurposeCOOLDOWN {
		t.Fatalf("block response=%d inputs=%#v", response.Code, store.blocks)
	}

	response = performStructuredTrainingRequest(t, user, http.MethodPost, "/admin/treinos/estruturados/segmentos/"+segmentID.String()+"/mover", url.Values{"direction": {"up"}}, "id", segmentID.String(), handler.MoveSegment)
	if response.Code != http.StatusSeeOther || len(store.movedSegments) != 1 || store.movedSegments[0].Direction != -1 {
		t.Fatalf("move segment response=%d inputs=%#v", response.Code, store.movedSegments)
	}
	response = performStructuredTrainingRequest(t, user, http.MethodPost, "/admin/treinos/estruturados/blocos/"+blockID.String()+"/mover", url.Values{"direction": {"down"}}, "id", blockID.String(), handler.MoveBlock)
	if response.Code != http.StatusSeeOther || len(store.movedBlocks) != 1 || store.movedBlocks[0].Direction != 1 {
		t.Fatalf("move block response=%d inputs=%#v", response.Code, store.movedBlocks)
	}

	gymValues := url.Values{
		"purpose": {"WARM_UP"}, "title": {"Mobilidade e força"}, "instructions": {"Duas voltas controladas"},
		"structure": {"SUPERSET"}, "objective": {"ACTIVATION"}, "rounds": {"3"}, "round_recovery_seconds": {"120"},
		"exercise_name": {"Supino + elevação"}, "sets": {"3"}, "repetitions": {"10"}, "recovery_seconds": {"60"},
		"resistance_kind": {"PERCENT_1RM"}, "resistance_value": {"75"}, "execution_intent": {"EXPLOSIVE"}, "tempo": {"2-0-X-1"},
	}
	response = performStructuredTrainingRequest(t, user, http.MethodPost, "/admin/treinos/estruturados/segmentos/"+segmentID.String()+"/ginasio", gymValues, "id", segmentID.String(), handler.CreateGymBlock)
	if response.Code != http.StatusSeeOther || len(store.gymBlocks) != 1 || store.gymBlocks[0].Prescription.Structure != dbgen.GymBlockStructureSUPERSET || store.gymBlocks[0].Prescription.Rounds != 3 || store.gymBlocks[0].Exercise.ResistanceValue == nil || *store.gymBlocks[0].Exercise.ResistanceValue != 75 {
		t.Fatalf("gym block response=%d inputs=%#v", response.Code, store.gymBlocks)
	}

	exerciseValues := url.Values{"exercise_name": {"Prancha"}, "duration_seconds": {"45"}, "resistance_kind": {"BODY_WEIGHT"}, "execution_intent": {"ISOMETRIC"}}
	response = performStructuredTrainingRequest(t, user, http.MethodPost, "/admin/treinos/estruturados/blocos/"+blockID.String()+"/exercicios", exerciseValues, "id", blockID.String(), handler.CreateGymExercise)
	if response.Code != http.StatusSeeOther || len(store.gymExercises) != 1 || store.gymExercises[0].BlockID != blockID || store.gymExercises[0].DurationSeconds == nil || *store.gymExercises[0].DurationSeconds != 45 {
		t.Fatalf("gym exercise response=%d inputs=%#v", response.Code, store.gymExercises)
	}

	exerciseID := uuid.New()
	response = performStructuredTrainingRequest(t, user, http.MethodPost, "/admin/treinos/estruturados/exercicios/"+exerciseID.String()+"/mover", url.Values{"direction": {"up"}}, "id", exerciseID.String(), handler.MoveGymExercise)
	if response.Code != http.StatusSeeOther || len(store.movedExercises) != 1 || store.movedExercises[0].Direction != -1 {
		t.Fatalf("move exercise response=%d inputs=%#v", response.Code, store.movedExercises)
	}
}

func performStructuredTrainingRequest(t *testing.T, user CurrentUser, method, target string, values url.Values, pathKey, pathValue string, handler http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, strings.NewReader(values.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if pathKey != "" {
		request.SetPathValue(pathKey, pathValue)
	}
	request = request.WithContext(context.WithValue(request.Context(), currentUserKey{}, user))
	response := httptest.NewRecorder()
	handler(response, request)
	return response
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
	for _, value := range []string{"1441", "2147483648", "999999999999999999999999"} {
		if parsed, err := optionalPositiveInt32(value, 1440); err == nil || parsed != nil {
			t.Fatalf("out-of-range value %q = %v, %v", value, parsed, err)
		}
	}
}

func TestParseGymExerciseValidatesPrescriptionAndResistance(t *testing.T) {
	valid := url.Values{"exercise_name": {"Supino"}, "repetitions": {"5"}, "resistance_kind": {"PERCENT_1RM"}, "resistance_value": {"75"}, "execution_intent": {"EXPLOSIVE"}}
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(valid.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := request.ParseForm(); err != nil {
		t.Fatal(err)
	}
	exercise, err := parseGymExercise(request)
	if err != nil || exercise.Repetitions == nil || *exercise.Repetitions != 5 || exercise.ResistanceValue == nil || *exercise.ResistanceValue != 75 {
		t.Fatalf("valid exercise = %#v, %v", exercise, err)
	}

	for name, values := range map[string]url.Values{
		"missing prescription":     {"exercise_name": {"Supino"}},
		"RPE outside scale":        {"exercise_name": {"Supino"}, "repetitions": {"5"}, "resistance_kind": {"RPE"}, "resistance_value": {"11"}},
		"band without description": {"exercise_name": {"Supino"}, "repetitions": {"5"}, "resistance_kind": {"BAND"}},
		"value without type":       {"exercise_name": {"Supino"}, "repetitions": {"5"}, "resistance_value": {"75"}},
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(values.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if err := request.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if _, err := parseGymExercise(request); err == nil {
				t.Fatal("invalid exercise was accepted")
			}
		})
	}
}
