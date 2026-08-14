package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type structuredTrainingStoreStub struct {
	StructuredTrainingStore
	manageable       bool
	manageArgs       []dbgen.CanManageStructuredTrainingGroupParams
	created          []dbgen.CreateStructuredTrainingWeekParams
	groups           []StructuredTrainingGroupInput
	weekOK           bool
	sessions         []dbgen.CreateStructuredTrainingSessionParams
	segments         []StructuredTrainingSegmentInput
	blocks           []dbgen.CreateTrainingSegmentBlockParams
	gymBlocks        []StructuredGymBlockInput
	gymExercises     []dbgen.CreateGymExerciseParams
	waterBlocks      []StructuredWaterBlockInput
	waterSteps       []dbgen.CreateWaterWorkStepParams
	waterProfiles    []dbgen.CreateWaterIntensityProfileParams
	waterZones       []dbgen.CreateWaterIntensityZoneParams
	planID           uuid.UUID
	movedSegments    []dbgen.MoveTrainingSessionSegmentParams
	movedBlocks      []dbgen.MoveTrainingSegmentBlockParams
	movedExercises   []dbgen.MoveGymExerciseParams
	routineSource    StructuredRoutineSource
	createdRoutines  []dbgen.CreateTrainingRoutineParams
	visibleRoutine   dbgen.TrainingRoutine
	insertedRoutines []StructuredRoutineInsertInput
	copiedBlocks     [][3]uuid.UUID
	copiedSessions   []struct {
		SourceID, TargetID, ActorID uuid.UUID
		StartsAt                    pgtype.Timestamptz
	}
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

func (s *structuredTrainingStoreStub) CreateWaterBlock(_ context.Context, input StructuredWaterBlockInput) (uuid.UUID, error) {
	s.waterBlocks = append(s.waterBlocks, input)
	return uuid.New(), nil
}

func (s *structuredTrainingStoreStub) CreateWaterWorkStep(_ context.Context, params dbgen.CreateWaterWorkStepParams) (uuid.UUID, error) {
	s.waterSteps = append(s.waterSteps, params)
	return uuid.New(), nil
}

func (s *structuredTrainingStoreStub) CreateWaterIntensityProfile(_ context.Context, params dbgen.CreateWaterIntensityProfileParams) (dbgen.CreateWaterIntensityProfileRow, error) {
	s.waterProfiles = append(s.waterProfiles, params)
	return dbgen.CreateWaterIntensityProfileRow{ID: uuid.New(), Name: params.Name, Craft: params.Craft, Revision: 1}, nil
}

func (s *structuredTrainingStoreStub) CreateWaterIntensityZone(_ context.Context, params dbgen.CreateWaterIntensityZoneParams) (uuid.UUID, error) {
	s.waterZones = append(s.waterZones, params)
	return uuid.New(), nil
}

func (s *structuredTrainingStoreStub) ListActiveWaterIntensityProfiles(context.Context) ([]dbgen.ListActiveWaterIntensityProfilesRow, error) {
	return nil, nil
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

func (s *structuredTrainingStoreStub) GetRoutineSource(context.Context, dbgen.TrainingRoutineKind, uuid.UUID) (StructuredRoutineSource, error) {
	return s.routineSource, nil
}

func (s *structuredTrainingStoreStub) CreateTrainingRoutine(_ context.Context, params dbgen.CreateTrainingRoutineParams) (dbgen.TrainingRoutine, error) {
	s.createdRoutines = append(s.createdRoutines, params)
	return dbgen.TrainingRoutine{ID: uuid.New()}, nil
}

func (s *structuredTrainingStoreStub) GetVisibleTrainingRoutine(context.Context, dbgen.GetVisibleTrainingRoutineParams) (dbgen.TrainingRoutine, error) {
	return s.visibleRoutine, nil
}

func (s *structuredTrainingStoreStub) InsertTrainingRoutine(_ context.Context, input StructuredRoutineInsertInput) (uuid.UUID, error) {
	s.insertedRoutines = append(s.insertedRoutines, input)
	return uuid.New(), nil
}

func (s *structuredTrainingStoreStub) CopyTrainingBlock(_ context.Context, sourceID, targetID, actorID uuid.UUID) (uuid.UUID, error) {
	s.copiedBlocks = append(s.copiedBlocks, [3]uuid.UUID{sourceID, targetID, actorID})
	return uuid.New(), nil
}

func (s *structuredTrainingStoreStub) CopyTrainingSession(_ context.Context, sourceID, targetID uuid.UUID, startsAt pgtype.Timestamptz, actorID uuid.UUID) (uuid.UUID, error) {
	s.copiedSessions = append(s.copiedSessions, struct {
		SourceID, TargetID, ActorID uuid.UUID
		StartsAt                    pgtype.Timestamptz
	}{sourceID, targetID, actorID, startsAt})
	return uuid.New(), nil
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

func TestStructuredTrainingRoutineHandlersPreserveScopeAndRequireTargetAuthorization(t *testing.T) {
	userID, planID, programmeID, sourceID, targetID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	modality := dbgen.TrainingSegmentModalityGYM
	sourceUpdatedAt := pgtype.Timestamptz{Time: time.Now(), Valid: true}
	store := &structuredTrainingStoreStub{weekOK: true, planID: planID, routineSource: StructuredRoutineSource{PlanID: planID, SourceUpdatedAt: sourceUpdatedAt, ProgrammeID: &programmeID, Modality: &modality, Snapshot: []byte(`{"title":"Mobilidade","blocks":[]}`)}}
	handler := StructuredTraining{Store: store, Location: time.UTC, System: System{}}
	user := CurrentUser{ID: userID}

	values := url.Values{"source_kind": {"SEGMENT"}, "source_id": {sourceID.String()}, "name": {"Mobilidade habitual"}, "visibility": {"SHARED"}, "method": {"Ativação"}, "tags": {"ginásio, aquecimento, Ginásio"}}
	response := performStructuredTrainingRequest(t, user, http.MethodPost, "/admin/treinos/estruturados/rotinas", values, "", "", handler.CreateRoutine)
	if response.Code != http.StatusSeeOther || len(store.createdRoutines) != 1 {
		t.Fatalf("create routine response=%d routines=%#v", response.Code, store.createdRoutines)
	}
	created := store.createdRoutines[0]
	if created.ProgrammeID == nil || *created.ProgrammeID != programmeID || created.TeamID != nil || len(created.Tags) != 2 || created.Snapshot == nil {
		t.Fatalf("created routine scope/tags = %#v", created)
	}

	store.visibleRoutine = dbgen.TrainingRoutine{ID: uuid.New(), Kind: dbgen.TrainingRoutineKindBLOCK, Snapshot: []byte(`{"purpose":"MAIN","instructions":"3x5"}`), UpdatedAt: sourceUpdatedAt}
	response = performStructuredTrainingRequest(t, user, http.MethodPost, "/admin/treinos/estruturados/rotinas/"+store.visibleRoutine.ID.String()+"/inserir", url.Values{"target_id": {targetID.String()}}, "id", store.visibleRoutine.ID.String(), handler.InsertRoutine)
	if response.Code != http.StatusSeeOther || len(store.insertedRoutines) != 1 || store.insertedRoutines[0].TargetID != targetID || store.insertedRoutines[0].ActorID != userID {
		t.Fatalf("insert routine response=%d inputs=%#v", response.Code, store.insertedRoutines)
	}
}

func TestStructuredTrainingDirectCopiesAuthorizeBothPlans(t *testing.T) {
	userID, planID, sourceID, targetID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	store := &structuredTrainingStoreStub{weekOK: true, planID: planID}
	handler := StructuredTraining{Store: store, Location: time.UTC, System: System{}}
	user := CurrentUser{ID: userID}

	response := performStructuredTrainingRequest(t, user, http.MethodPost, "/admin/treinos/estruturados/blocos/"+sourceID.String()+"/copiar", url.Values{"target_id": {targetID.String()}}, "id", sourceID.String(), handler.CopyBlock)
	if response.Code != http.StatusSeeOther || len(store.copiedBlocks) != 1 || store.copiedBlocks[0] != [3]uuid.UUID{sourceID, targetID, userID} {
		t.Fatalf("copy block response=%d inputs=%#v", response.Code, store.copiedBlocks)
	}

	response = performStructuredTrainingRequest(t, user, http.MethodPost, "/admin/treinos/estruturados/sessoes/"+sourceID.String()+"/copiar", url.Values{"target_id": {targetID.String()}, "starts_at": {"2026-08-18T17:00"}}, "id", sourceID.String(), handler.CopySession)
	if response.Code != http.StatusSeeOther || len(store.copiedSessions) != 1 || !store.copiedSessions[0].StartsAt.Valid {
		t.Fatalf("copy session response=%d inputs=%#v", response.Code, store.copiedSessions)
	}
}

func TestParseTrainingRoutineTagsDeduplicatesAndRejectsInvalidInput(t *testing.T) {
	tags, err := parseTrainingRoutineTags("ginásio, Aquecimento, GINÁSIO")
	if err != nil || len(tags) != 2 || tags[0] != "ginásio" || tags[1] != "Aquecimento" {
		t.Fatalf("tags = %#v, %v", tags, err)
	}
	if _, err := parseTrainingRoutineTags(strings.Repeat("x", 41)); err == nil {
		t.Fatal("overlong tag should be rejected")
	}
}

func TestStructuredTrainingViewHelpersBuildReadableChoicesAndRoutines(t *testing.T) {
	programmeID, teamID := uuid.New(), uuid.New()
	teamName, programmeName := "K1 cadetes", "Competição"
	rows := []dbgen.ListEligibleTrainingGroupMembershipsRow{
		{ID: uuid.New(), AthleteName: "Atleta A", ProgrammeID: programmeID, ProgrammeName: programmeName},
		{ID: uuid.New(), AthleteName: "Atleta B", ProgrammeID: programmeID, ProgrammeName: programmeName, TeamID: &teamID, TeamName: &teamName},
	}
	members, programmes, teams := structuredChoices(rows)
	if len(members) != 2 || members[1].Scope != "Competição · K1 cadetes" || len(programmes) != 1 || len(teams) != 1 {
		t.Fatalf("choices = members %#v, programmes %#v, teams %#v", members, programmes, teams)
	}

	audiences := []pages.StructuredTrainingAudience{{
		GroupID: "group-1", GroupName: "Cadetes",
		Weeks: []pages.StructuredTrainingWeek{{ID: "week-1", Title: "M41", DateRange: "17/08/2026–23/08/2026", Sessions: []pages.StructuredTrainingSession{{
			ID: "session-1", Title: "Ginásio + água", When: "18/08/2026 17:00–19:00",
			Segments: []pages.StructuredTrainingSegment{{ID: "segment-1", Modality: "WATER", Title: "Técnica"}},
		}}}},
	}}
	groups, weeks, sessions, segments := structuredPlanChoices(audiences)
	if len(groups) != 1 || len(weeks) != 1 || len(sessions) != 1 || len(segments) != 1 || !strings.Contains(segments[0].Name, "Água · Técnica") {
		t.Fatalf("plan choices = %#v %#v %#v %#v", groups, weeks, sessions, segments)
	}

	modality, objective := dbgen.TrainingSegmentModalityGYM, dbgen.TrainingObjectiveACTIVATION
	sourceTime := time.Date(2026, 8, 13, 9, 30, 0, 0, time.UTC)
	routines := structuredRoutineRows([]dbgen.ListVisibleTrainingRoutinesRow{{
		ID: uuid.New(), Name: "Ativação", Kind: dbgen.TrainingRoutineKindSEGMENT,
		Visibility: dbgen.TrainingRoutineVisibilitySHARED, OwnerName: "Treinador",
		ProgrammeName: &programmeName, Modality: &modality, Objective: &objective,
		Tags: []string{"ginásio", "aquecimento"}, SourceUpdatedAt: pgtype.Timestamptz{Time: sourceTime, Valid: true},
		Snapshot: []byte(`{"title":"Mobilidade","blocks":[{}]}`),
	}}, time.UTC)
	if len(routines) != 1 || routines[0].Visibility != "Partilhada" || routines[0].Scope != programmeName || routines[0].Preview != "Mobilidade · 1 blocos" || !strings.Contains(routines[0].Provenance, "um segmento") {
		t.Fatalf("routines = %#v", routines)
	}
}

func TestStructuredTrainingFormattingHelpersCoverSupportedPrescriptions(t *testing.T) {
	if got := gymExercisePrescription(3, 8, 45, 500, 60); got != "3 séries · 8 repetições · 45 s · 500 m · recuperação 60 s" {
		t.Fatalf("prescription = %q", got)
	}
	value := 75.0
	for _, tc := range []struct{ kind, text, want string }{
		{"KILOGRAMS", "", "75 kg"}, {"PERCENT_1RM", "", "75% de 1RM"}, {"RPE", "", "RPE 75"}, {"RIR", "", "RIR 75"},
		{"BODY_WEIGHT", "", "Peso corporal"}, {"BAND", "forte", "Banda · forte"}, {"COACH_INSTRUCTION", "carga técnica", "carga técnica"},
	} {
		var resistanceValue *float64
		if tc.kind == "KILOGRAMS" || tc.kind == "PERCENT_1RM" || tc.kind == "RPE" || tc.kind == "RIR" {
			resistanceValue = &value
		}
		if got := gymResistanceLabel(tc.kind, resistanceValue, tc.text); got != tc.want {
			t.Errorf("%s label = %q, want %q", tc.kind, got, tc.want)
		}
	}
	if got := gymResistanceLabel("UNKNOWN", nil, ""); got != "" {
		t.Fatalf("unknown resistance = %q", got)
	}

	for _, tc := range []struct {
		kind     dbgen.TrainingRoutineKind
		snapshot string
		want     string
	}{
		{dbgen.TrainingRoutineKindBLOCK, `{"title":"Série","instructions":"3x5"}`, "Série · 3x5"},
		{dbgen.TrainingRoutineKindBLOCK, `{"instructions":"3x5"}`, "3x5"},
		{dbgen.TrainingRoutineKindSEGMENT, `{"blocks":[{},{}]}`, "Segmento · 2 blocos"},
		{dbgen.TrainingRoutineKindSESSION, `{"segments":[{}]}`, "Sessão · 1 segmentos"},
		{dbgen.TrainingRoutineKindSESSION, `{`, "Conteúdo estruturado"},
	} {
		if got := trainingRoutinePreview([]byte(tc.snapshot), tc.kind); got != tc.want {
			t.Errorf("preview = %q, want %q", got, tc.want)
		}
	}

	for modality, want := range map[string]string{"WATER": "Água", "GYM": "Ginásio", "RUN": "Corrida", "BIKE": "Bicicleta", "ERGOMETER": "Ergómetro", "FLEXIBILITY": "Flexibilidade e mobilidade", "SPORTS_GAMES": "Jogos desportivos", "OTHER": "Outra"} {
		if got := structuredModalityName(modality); got != want {
			t.Errorf("modality %s = %q, want %q", modality, got, want)
		}
	}
}

func TestStructuredCopyRejectionRecognisesSafeDatabaseFailures(t *testing.T) {
	if isStructuredCopyRejection(errors.New("ordinary failure")) {
		t.Fatal("ordinary errors must not be treated as copy validation failures")
	}
	for _, code := range []string{"P0002", "23503", "23505", "23514"} {
		if !isStructuredCopyRejection(&pgconn.PgError{Code: code}) {
			t.Errorf("database code %s should be recognised", code)
		}
	}
	if isStructuredCopyRejection(&pgconn.PgError{Code: "XX000"}) {
		t.Fatal("unexpected database failures must not be converted to validation errors")
	}
}

func TestStructuredWaterAuthoringPreservesNestedRecoveryAndTacticalMetadata(t *testing.T) {
	store := &structuredTrainingStoreStub{weekOK: true, planID: uuid.New()}
	handler := StructuredTraining{Store: store, System: System{}}
	user := CurrentUser{ID: uuid.New(), IsAdmin: true}
	segmentID, blockID, parentID := uuid.New(), uuid.New(), uuid.New()

	blockValues := url.Values{
		"purpose": {"MAIN"}, "title": {"Bloco técnico-tático"}, "instructions": {"Manter qualidade sob pressão"},
		"method": {"TACTICAL_DRILL"}, "target_distance_metres": {"12000"}, "target_distance_certainty": {"ESTIMATED"},
		"step_kind": {"REPEAT_GROUP"}, "step_name": {"Série principal"}, "repeats": {"3"}, "recovery_seconds": {"180"},
	}
	response := performStructuredTrainingRequest(t, user, http.MethodPost, "/admin/treinos/estruturados/segmentos/"+segmentID.String()+"/agua", blockValues, "id", segmentID.String(), handler.CreateWaterBlock)
	if response.Code != http.StatusSeeOther || len(store.waterBlocks) != 1 {
		t.Fatalf("create water block: status=%d writes=%d", response.Code, len(store.waterBlocks))
	}
	if got := store.waterBlocks[0]; got.Prescription.Method != dbgen.WaterWorkMethodTACTICALDRILL || got.Prescription.TargetDistanceCertainty == nil || *got.Prescription.TargetDistanceCertainty != dbgen.TrainingMeasureCertaintyESTIMATED || got.Step.Repeats == nil || *got.Step.Repeats != 3 || got.Step.RecoverySeconds == nil || *got.Step.RecoverySeconds != 180 {
		t.Fatalf("water block lost structured semantics: %+v", got)
	}

	stepValues := url.Values{
		"parent_step_id": {parentID.String()}, "step_kind": {"EFFORT"}, "step_name": {"Ataque 3 contra 2"},
		"duration_seconds": {"120"}, "duration_certainty": {"EXACT"}, "recovery_seconds": {"60"},
		"intensity_code": {"R7"}, "drill_focus": {"Ataque"}, "drill_format": {"3 contra 2"},
		"role_notes": {"GR e pivot"}, "step_instructions": {"Ritmo de uma prova de dois minutos"},
	}
	response = performStructuredTrainingRequest(t, user, http.MethodPost, "/admin/treinos/estruturados/blocos/"+blockID.String()+"/agua/passos", stepValues, "id", blockID.String(), handler.CreateWaterWorkStep)
	if response.Code != http.StatusSeeOther || len(store.waterSteps) != 1 {
		t.Fatalf("create water step: status=%d writes=%d", response.Code, len(store.waterSteps))
	}
	if got := store.waterSteps[0]; got.ParentStepID == nil || *got.ParentStepID != parentID || got.IntensityCode == nil || *got.IntensityCode != "R7" || got.DrillFormat == nil || *got.DrillFormat != "3 contra 2" || got.RecoverySeconds == nil || *got.RecoverySeconds != 60 {
		t.Fatalf("water step lost nesting or drill metadata: %+v", got)
	}
}

func TestWaterProfilesAreVersionableWithoutAssumingTheDisputedR5Boundary(t *testing.T) {
	store := &structuredTrainingStoreStub{}
	handler := StructuredTraining{Store: store, System: System{}}
	user := CurrentUser{ID: uuid.New(), IsAdmin: true}
	response := performStructuredTrainingRequest(t, user, http.MethodPost, "/admin/treinos/estruturados/agua/perfis", url.Values{"name": {"Perfil do clube"}, "craft": {"KAYAK"}, "notes": {"R5 por confirmar com o treinador"}}, "", "", handler.CreateWaterIntensityProfile)
	if response.Code != http.StatusSeeOther || len(store.waterProfiles) != 1 {
		t.Fatalf("create profile: status=%d writes=%d", response.Code, len(store.waterProfiles))
	}
	profileID := uuid.New()
	response = performStructuredTrainingRequest(t, user, http.MethodPost, "/admin/treinos/estruturados/agua/perfis/"+profileID.String()+"/zonas", url.Values{"code": {"R7"}, "label": {"Ritmo de prova"}, "meaning": {"Ritmo sustentável para a duração ou distância prescrita"}}, "id", profileID.String(), handler.CreateWaterIntensityZone)
	if response.Code != http.StatusSeeOther || len(store.waterZones) != 1 || store.waterZones[0].CadenceMin != nil || store.waterZones[0].CadenceMax != nil {
		t.Fatalf("duration-relative zone should not invent cadence: status=%d zone=%+v", response.Code, store.waterZones)
	}
}

func TestCalculateWaterTotalsHandlesNestedRecoveryAndProvenance(t *testing.T) {
	steps := []pages.StructuredWaterStep{
		{ID: "outer", Kind: "REPEAT_GROUP", Repeats: 3, Recovery: 180},
		{ID: "inner", ParentID: "outer", Kind: "REPEAT_GROUP", Repeats: 3, Recovery: 60},
		{ID: "effort", ParentID: "inner", Kind: "EFFORT", DurationSeconds: 180, DurationCertainty: "EXACT", DistanceMetres: 500, DistanceCertainty: "ESTIMATED", Intensity: "R3 · Limiar"},
	}
	totals := calculateWaterTotals(steps)
	want := []pages.StructuredWaterTotal{
		{Label: "Esforço planeado", Value: "27 min", Certainty: "exato"},
		{Label: "Recuperação planeada", Value: "12 min", Certainty: "exato"},
		{Label: "Distância planeada", Value: "4,5 km", Certainty: "estimado"},
		{Label: "Tempo em R3", Value: "27 min", Certainty: "exato"},
	}
	if !reflect.DeepEqual(totals, want) {
		t.Fatalf("nested totals = %#v, want %#v", totals, want)
	}
}

func TestCalculateWaterTotalsDoesNotInventMissingPace(t *testing.T) {
	totals := calculateWaterTotals([]pages.StructuredWaterStep{{ID: "distance", Kind: "EFFORT", DistanceMetres: 2000, DistanceCertainty: "EXACT", Intensity: "R7 · Ritmo de prova"}})
	if totals[0].Value != "Desconhecido" || totals[0].Certainty != "faltam dados; não foi inferido" {
		t.Fatalf("duration should stay unknown without pace: %#v", totals[0])
	}
	if totals[2].Value != "2 km" || totals[2].Certainty != "exato" {
		t.Fatalf("known distance should remain exact: %#v", totals[2])
	}
	if totals[3].Label != "Tempo em R7" || totals[3].Value != "Desconhecido" {
		t.Fatalf("intensity duration should stay unknown: %#v", totals[3])
	}
}
