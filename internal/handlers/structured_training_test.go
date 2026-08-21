package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type structuredTrainingStoreStub struct {
	StructuredTrainingStore
	manageable         bool
	manageArgs         []dbgen.CanManageStructuredTrainingGroupParams
	created            []dbgen.CreateStructuredTrainingWeekParams
	updatedLoads       []dbgen.UpdateStructuredTrainingWeekLoadParams
	groups             []StructuredTrainingGroupInput
	weekOK             bool
	sessions           []dbgen.CreateStructuredTrainingSessionParams
	segments           []StructuredTrainingSegmentInput
	blocks             []dbgen.CreateTrainingSegmentBlockParams
	gymBlocks          []StructuredGymBlockInput
	gymExercises       []dbgen.CreateGymExerciseParams
	waterBlocks        []StructuredWaterBlockInput
	waterSteps         []dbgen.CreateWaterWorkStepParams
	waterProfiles      []dbgen.CreateWaterIntensityProfileParams
	waterZones         []dbgen.CreateWaterIntensityZoneParams
	planID             uuid.UUID
	movedSegments      []dbgen.MoveTrainingSessionSegmentParams
	movedBlocks        []dbgen.MoveTrainingSegmentBlockParams
	movedExercises     []dbgen.MoveGymExerciseParams
	routineSource      StructuredRoutineSource
	routineSourceErr   error
	createdRoutines    []dbgen.CreateTrainingRoutineParams
	createRoutineErr   error
	visibleRoutine     dbgen.TrainingRoutine
	visibleRoutineErr  error
	insertedRoutines   []StructuredRoutineInsertInput
	insertRoutineErr   error
	copiedBlocks       [][3]uuid.UUID
	variationInputs    []dbgen.CreateTrainingVariationParams
	prescriptionLinks  []dbgen.ListTrainingPrescriptionLinksForSessionViewerRow
	prescriptionRow    dbgen.GetTrainingPrescriptionForViewerRow
	prescriptionErr    error
	publicationMembers []dbgen.ListStructuredTrainingPublicationMembersRow
	publicationHashes  []dbgen.ListLatestTrainingPrescriptionHashesForPlanRow
	publicationStates  []dbgen.ListManagedTrainingPublicationStatesRow
	overviewRows       []dbgen.ListStructuredTrainingOverviewForManagerRow
	variationMatches   []dbgen.ListTrainingVariationMatchesForManagerRow
	retiredVariation   dbgen.RetireTrainingVariationParams
	retiredRows        int64
	variationGroup     StructuredVariationGroupInput
	publishedInput     StructuredPublicationInput
	copiedSessions     []struct {
		SourceID, TargetID, ActorID uuid.UUID
		StartsAt                    pgtype.Timestamptz
	}
	dayCopy                 StructuredDayCopyInput
	dayCopyCount            int
	weekCopy                StructuredWeekCopyInput
	createGroupErr          error
	createWeekErr           error
	createSessionErr        error
	updateWeekLoadErr       error
	getSessionPlanErr       error
	getSegmentPlanErr       error
	getBlockPlanErr         error
	getGymExercisePlanErr   error
	createSegmentErr        error
	createBlockErr          error
	moveGymErr              error
	dayCopyErr              error
	weekCopyErr             error
	createWaterBlockErr     error
	createVariationGroupErr error
	publishErr              error
	listProfilesErr         error
	createWaterStepErr      error
	createWaterProfileErr   error
	createWaterZoneErr      error
	createGymBlockErr       error
	createGymExerciseErr    error
	createVariationErr      error
	variationPlanErr        error
	retireVariationErr      error
	overviewErr             error
	routinesErr             error
	membershipsErr          error
	variationMembersErr     error
	crewModalitiesErr       error
	competitionEventsErr    error
	publicationMembersErr   error
	publicationHashesErr    error
	crewModalities          []dbgen.ListStructuredCrewModalitiesRow
	competitionEvents       []dbgen.ListManagedStructuredCompetitionEventsRow
	variationGroupsErr      error
	variationMatchesErr     error
	publicationStatesErr    error
}

type structuredTrainingDBFake struct {
	err error
	tx  pgx.Tx
}

func (db structuredTrainingDBFake) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, db.err
}
func (db structuredTrainingDBFake) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, db.err
}
func (db structuredTrainingDBFake) QueryRow(context.Context, string, ...any) pgx.Row {
	if db.err == nil && db.tx != nil {
		return structuredTrainingSuccessRowFake{}
	}
	return structuredTrainingRowFake{err: db.err}
}
func (db structuredTrainingDBFake) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	if db.tx != nil {
		return db.tx, nil
	}
	return nil, db.err
}

type structuredTrainingRowFake struct{ err error }

func (row structuredTrainingRowFake) Scan(...any) error { return row.err }

type structuredTrainingTransactionFake struct {
	pgx.Tx
	err error
}

func (tx structuredTrainingTransactionFake) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, tx.err
}
func (tx structuredTrainingTransactionFake) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, tx.err
}
func (tx structuredTrainingTransactionFake) QueryRow(context.Context, string, ...any) pgx.Row {
	return structuredTrainingRowFake{err: tx.err}
}
func (structuredTrainingTransactionFake) Rollback(context.Context) error  { return nil }
func (tx structuredTrainingTransactionFake) Commit(context.Context) error { return tx.err }

type structuredTrainingSuccessTransactionFake struct {
	pgx.Tx
	committed bool
}

func (tx *structuredTrainingSuccessTransactionFake) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}
func (*structuredTrainingSuccessTransactionFake) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return structuredTrainingEmptyRowsFake{}, nil
}
func (*structuredTrainingSuccessTransactionFake) QueryRow(context.Context, string, ...any) pgx.Row {
	return structuredTrainingSuccessRowFake{}
}
func (*structuredTrainingSuccessTransactionFake) Rollback(context.Context) error { return nil }
func (tx *structuredTrainingSuccessTransactionFake) Commit(context.Context) error {
	tx.committed = true
	return nil
}

type structuredTrainingSuccessRowFake struct{}

func (structuredTrainingSuccessRowFake) Scan(dest ...any) error {
	for _, target := range dest {
		switch value := target.(type) {
		case *uuid.UUID:
			*value = uuid.New()
		case *pgtype.Timestamptz:
			*value = pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
		case *pgtype.Date:
			*value = pgtype.Date{Time: time.Now().UTC(), Valid: true}
		case *string:
			*value = "copied"
		case **uuid.UUID:
			id := uuid.New()
			*value = &id
		case **int16:
			load := int16(50)
			*value = &load
		case *[]byte:
			*value = []byte(`{}`)
		case *dbgen.TrainingSegmentModality:
			*value = dbgen.TrainingSegmentModalityWATER
		}
	}
	return nil
}

type structuredTrainingPublicationTransactionFake struct {
	structuredTrainingSuccessTransactionFake
	updated time.Time
}

func (tx *structuredTrainingPublicationTransactionFake) QueryRow(context.Context, string, ...any) pgx.Row {
	return structuredTrainingPublicationRowFake{updated: tx.updated}
}

type structuredTrainingPublicationRowFake struct{ updated time.Time }

func (row structuredTrainingPublicationRowFake) Scan(dest ...any) error {
	for _, target := range dest {
		switch value := target.(type) {
		case *uuid.UUID:
			*value = uuid.New()
		case *int32:
			*value = 1
		case *pgtype.Timestamptz:
			*value = pgtype.Timestamptz{Time: row.updated, Valid: true}
		case *string:
			*value = "publication"
		}
	}
	return nil
}

type structuredTrainingEmptyRowsFake struct{ pgx.Rows }

func (structuredTrainingEmptyRowsFake) Close()                                       {}
func (structuredTrainingEmptyRowsFake) Err() error                                   { return nil }
func (structuredTrainingEmptyRowsFake) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (structuredTrainingEmptyRowsFake) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (structuredTrainingEmptyRowsFake) Next() bool                                   { return false }
func (structuredTrainingEmptyRowsFake) Scan(...any) error                            { return errors.New("no rows") }
func (structuredTrainingEmptyRowsFake) Values() ([]any, error)                       { return nil, errors.New("no rows") }
func (structuredTrainingEmptyRowsFake) RawValues() [][]byte                          { return nil }
func (structuredTrainingEmptyRowsFake) Conn() *pgx.Conn                              { return nil }

func (s *structuredTrainingStoreStub) CreateTrainingVariation(_ context.Context, params dbgen.CreateTrainingVariationParams) (dbgen.TrainingVariation, error) {
	s.variationInputs = append(s.variationInputs, params)
	return dbgen.TrainingVariation{ID: uuid.New()}, s.createVariationErr
}

func (s *structuredTrainingStoreStub) GetTrainingVariationPlanID(context.Context, uuid.UUID) (uuid.UUID, error) {
	return s.planID, s.variationPlanErr
}

func (s *structuredTrainingStoreStub) RetireTrainingVariation(_ context.Context, params dbgen.RetireTrainingVariationParams) (int64, error) {
	s.retiredVariation = params
	return s.retiredRows, s.retireVariationErr
}

func (s *structuredTrainingStoreStub) CreateTrainingVariationGroup(_ context.Context, input StructuredVariationGroupInput) (dbgen.TrainingVariationGroup, error) {
	s.variationGroup = input
	return dbgen.TrainingVariationGroup{ID: uuid.New(), Name: input.Params.Name}, s.createVariationGroupErr
}

func (s *structuredTrainingStoreStub) PublishStructuredTrainingPlan(_ context.Context, input StructuredPublicationInput) (dbgen.TrainingPlanPublication, error) {
	s.publishedInput = input
	return dbgen.TrainingPlanPublication{ID: uuid.New(), PlanID: input.PlanID, Revision: 1}, s.publishErr
}

func (s *structuredTrainingStoreStub) CreateGroup(_ context.Context, input StructuredTrainingGroupInput) (dbgen.TrainingGroup, error) {
	s.groups = append(s.groups, input)
	return dbgen.TrainingGroup{ID: uuid.New(), Name: input.Params.Name}, s.createGroupErr
}

func (s *structuredTrainingStoreStub) CanManageStructuredTrainingGroup(_ context.Context, params dbgen.CanManageStructuredTrainingGroupParams) (bool, error) {
	s.manageArgs = append(s.manageArgs, params)
	return s.manageable || params.IsAdmin, nil
}

func (s *structuredTrainingStoreStub) CreateStructuredTrainingWeek(_ context.Context, params dbgen.CreateStructuredTrainingWeekParams) (dbgen.TrainingPlan, error) {
	s.created = append(s.created, params)
	return dbgen.TrainingPlan{ID: uuid.New(), Title: params.Title, TrainingGroupID: &params.GroupID, WeekStart: params.WeekStart}, s.createWeekErr
}

func (s *structuredTrainingStoreStub) CanManageStructuredTrainingWeek(_ context.Context, _ dbgen.CanManageStructuredTrainingWeekParams) (bool, error) {
	return s.weekOK, nil
}

func (s *structuredTrainingStoreStub) UpdateStructuredTrainingWeekLoad(_ context.Context, params dbgen.UpdateStructuredTrainingWeekLoadParams) (int64, error) {
	s.updatedLoads = append(s.updatedLoads, params)
	return 1, s.updateWeekLoadErr
}

func (s *structuredTrainingStoreStub) CreateStructuredTrainingSession(_ context.Context, params dbgen.CreateStructuredTrainingSessionParams) (dbgen.TrainingSession, error) {
	s.sessions = append(s.sessions, params)
	return dbgen.TrainingSession{ID: uuid.New(), PlanID: params.PlanID, Title: params.Title}, s.createSessionErr
}

func (s *structuredTrainingStoreStub) GetStructuredSessionPlanID(context.Context, uuid.UUID) (uuid.UUID, error) {
	return s.planID, s.getSessionPlanErr
}

func (s *structuredTrainingStoreStub) GetStructuredSegmentPlanID(context.Context, uuid.UUID) (uuid.UUID, error) {
	return s.planID, s.getSegmentPlanErr
}

func (s *structuredTrainingStoreStub) GetStructuredBlockPlanID(context.Context, uuid.UUID) (uuid.UUID, error) {
	return s.planID, s.getBlockPlanErr
}

func (s *structuredTrainingStoreStub) GetGymExercisePlanID(context.Context, uuid.UUID) (uuid.UUID, error) {
	return s.planID, s.getGymExercisePlanErr
}

func (s *structuredTrainingStoreStub) CopyStructuredTrainingDay(_ context.Context, input StructuredDayCopyInput) (int, error) {
	s.dayCopy = input
	return s.dayCopyCount, s.dayCopyErr
}

func (s *structuredTrainingStoreStub) CopyStructuredTrainingWeek(_ context.Context, input StructuredWeekCopyInput) (dbgen.TrainingPlan, error) {
	s.weekCopy = input
	return dbgen.TrainingPlan{ID: uuid.New(), Title: input.Title}, s.weekCopyErr
}

func (s *structuredTrainingStoreStub) CreateSegment(_ context.Context, input StructuredTrainingSegmentInput) (uuid.UUID, error) {
	s.segments = append(s.segments, input)
	return uuid.New(), s.createSegmentErr
}

func (s *structuredTrainingStoreStub) CreateTrainingSegmentBlock(_ context.Context, params dbgen.CreateTrainingSegmentBlockParams) (uuid.UUID, error) {
	s.blocks = append(s.blocks, params)
	return uuid.New(), s.createBlockErr
}

func (s *structuredTrainingStoreStub) CreateGymBlock(_ context.Context, input StructuredGymBlockInput) (uuid.UUID, error) {
	s.gymBlocks = append(s.gymBlocks, input)
	return uuid.New(), s.createGymBlockErr
}

func (s *structuredTrainingStoreStub) CreateGymExercise(_ context.Context, params dbgen.CreateGymExerciseParams) (uuid.UUID, error) {
	s.gymExercises = append(s.gymExercises, params)
	return uuid.New(), s.createGymExerciseErr
}

func (s *structuredTrainingStoreStub) CreateWaterBlock(_ context.Context, input StructuredWaterBlockInput) (uuid.UUID, error) {
	s.waterBlocks = append(s.waterBlocks, input)
	return uuid.New(), s.createWaterBlockErr
}

func (s *structuredTrainingStoreStub) CreateWaterWorkStep(_ context.Context, params dbgen.CreateWaterWorkStepParams) (uuid.UUID, error) {
	s.waterSteps = append(s.waterSteps, params)
	return uuid.New(), s.createWaterStepErr
}

func (s *structuredTrainingStoreStub) CreateWaterIntensityProfile(_ context.Context, params dbgen.CreateWaterIntensityProfileParams) (dbgen.CreateWaterIntensityProfileRow, error) {
	s.waterProfiles = append(s.waterProfiles, params)
	return dbgen.CreateWaterIntensityProfileRow{ID: uuid.New(), Name: params.Name, Craft: params.Craft, Revision: 1}, s.createWaterProfileErr
}

func (s *structuredTrainingStoreStub) CreateWaterIntensityZone(_ context.Context, params dbgen.CreateWaterIntensityZoneParams) (uuid.UUID, error) {
	s.waterZones = append(s.waterZones, params)
	return uuid.New(), s.createWaterZoneErr
}

func (s *structuredTrainingStoreStub) ListActiveWaterIntensityProfiles(context.Context) ([]dbgen.ListActiveWaterIntensityProfilesRow, error) {
	return nil, s.listProfilesErr
}

func (s *structuredTrainingStoreStub) ListStructuredTrainingPublicationMembers(context.Context, dbgen.ListStructuredTrainingPublicationMembersParams) ([]dbgen.ListStructuredTrainingPublicationMembersRow, error) {
	return s.publicationMembers, s.publicationMembersErr
}

func (s *structuredTrainingStoreStub) ListLatestTrainingPrescriptionHashesForPlan(context.Context, uuid.UUID) ([]dbgen.ListLatestTrainingPrescriptionHashesForPlanRow, error) {
	return s.publicationHashes, s.publicationHashesErr
}

func TestStructuredWaterBlockTaskFormPreservesSafeValuesAndMapsErrors(t *testing.T) {
	form := structuredWaterBlockTaskForm(url.Values{
		"purpose": {"MAIN"}, "title": {"Intervalos"}, "instructions": {"Manter ritmo"}, "method": {"INTERVALS"},
		"step_kind": {"EFFORT"}, "step_name": {"500 m"}, "distance_metres": {"500"}, "distance_certainty": {"EXACT"},
		"intensity_code": {"R7"}, "step_instructions": {"Sair rápido"},
	}, validation.FieldErrors{"step_name": "Indique um esforço válido."})
	if form.Title != "Intervalos" || form.StepName != "500 m" || form.DistanceMetres != "500" || form.IntensityCode != "R7" || form.Errors["step_name"] == "" {
		t.Fatalf("safe values or validation feedback were not preserved: %#v", form)
	}
	if errors := structuredWaterTaskErrors(httptest.NewRequest(http.MethodPost, "/", nil), errors.New("invalid water step")); errors["step_name"] == "" {
		t.Fatalf("water-step validation did not target the task field: %#v", errors)
	}
}

func TestStructuredWaterBlockTaskShowsOnlyAnAuthorizedPlannerSegment(t *testing.T) {
	groupID, planID, sessionID, segmentID, userID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	weekTitle, sessionTitle, segmentTitle := "Semana 1", "Intervalos", "Água principal"
	modality := dbgen.TrainingSegmentModalityWATER
	startsAt := time.Date(2026, 9, 8, 18, 0, 0, 0, time.UTC)
	store := &structuredTrainingStoreStub{
		planID: planID, weekOK: true,
		overviewRows: []dbgen.ListStructuredTrainingOverviewForManagerRow{{
			GroupID: groupID, GroupName: "Competição", ProgrammeName: "Competição", PlanID: &planID, PlanTitle: &weekTitle,
			WeekStart: pgtype.Date{Time: startsAt, Valid: true}, SessionID: &sessionID, SessionTitle: &sessionTitle,
			StartsAt: pgtype.Timestamptz{Time: startsAt, Valid: true}, EndsAt: pgtype.Timestamptz{Time: startsAt.Add(time.Hour), Valid: true},
			SegmentID: &segmentID, SegmentModality: &modality, SegmentTitle: &segmentTitle,
		}},
	}
	h := StructuredTraining{Store: store, Location: time.UTC}
	r := httptest.NewRequest(http.MethodGet, structuredWaterTaskPath(sessionID, segmentID), nil)
	r.SetPathValue("session_id", sessionID.String())
	r.SetPathValue("segment_id", segmentID.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: userID, Name: "Treinadora", IsAdmin: true}))
	w := httptest.NewRecorder()

	h.WaterBlockTask(w, r)

	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Água principal") || !strings.Contains(w.Body.String(), "Competição") || !strings.Contains(w.Body.String(), structuredWaterTaskPath(sessionID, segmentID)) {
		t.Fatalf("response=%d body=%s", w.Code, w.Body.String())
	}
}

func TestCopyStructuredTrainingDayRequiresManagedWeeksAndCapturesIndependentCopyIntent(t *testing.T) {
	sourcePlanID, targetPlanID, actorID := uuid.New(), uuid.New(), uuid.New()
	store := &structuredTrainingStoreStub{weekOK: true, dayCopyCount: 2}
	h := StructuredTraining{Store: store, Location: time.UTC}
	values := url.Values{"source_plan_id": {sourcePlanID.String()}, "target_plan_id": {targetPlanID.String()}, "source_date": {"2026-09-08"}, "target_date": {"2026-09-10"}}
	r := httptest.NewRequest(http.MethodPost, "/admin/treinos/estruturados/copiar-dia", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actorID, IsAdmin: true}))
	w := httptest.NewRecorder()

	h.CopyDay(w, r)

	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/treinos/estruturados" {
		t.Fatalf("response=%d location=%q", w.Code, w.Header().Get("Location"))
	}
	if store.dayCopy.SourcePlanID != sourcePlanID || store.dayCopy.TargetPlanID != targetPlanID || store.dayCopy.ActorID != actorID || !store.dayCopy.SourceDate.Equal(time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)) || !store.dayCopy.TargetDate.Equal(time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("copy input=%#v", store.dayCopy)
	}

	store.dayCopyCount = 0
	w = httptest.NewRecorder()
	h.CopyDay(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("zero-copy response=%d", w.Code)
	}
}

func TestCopyStructuredTrainingWeekAcceptsOnlyMondayAndPersistsCopyIntent(t *testing.T) {
	sourcePlanID, actorID := uuid.New(), uuid.New()
	store := &structuredTrainingStoreStub{weekOK: true}
	h := StructuredTraining{Store: store, Location: time.UTC}
	request := func(weekStart string) *http.Request {
		values := url.Values{"week_start": {weekStart}, "title": {"Semana de carga"}}
		r := httptest.NewRequest(http.MethodPost, "/admin/treinos/estruturados/semanas/"+sourcePlanID.String()+"/copiar", strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.SetPathValue("id", sourcePlanID.String())
		return r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actorID, IsAdmin: true}))
	}

	w := httptest.NewRecorder()
	h.CopyWeek(w, request("2026-09-07"))
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/treinos/estruturados" {
		t.Fatalf("response=%d location=%q", w.Code, w.Header().Get("Location"))
	}
	if store.weekCopy.SourcePlanID != sourcePlanID || store.weekCopy.ActorID != actorID || store.weekCopy.Title != "Semana de carga" || !store.weekCopy.WeekStart.Equal(time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("copy input=%#v", store.weekCopy)
	}

	w = httptest.NewRecorder()
	h.CopyWeek(w, request("2026-09-08"))
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-Monday response=%d", w.Code)
	}
}

func TestStructuredTrainingCopiesMapStaleAndServiceFailures(t *testing.T) {
	sourcePlanID, targetPlanID, actorID := uuid.New(), uuid.New(), uuid.New()
	dayValues := url.Values{"source_plan_id": {sourcePlanID.String()}, "target_plan_id": {targetPlanID.String()}, "source_date": {"2026-09-08"}, "target_date": {"2026-09-10"}}
	for _, tc := range []struct {
		name    string
		handler func(StructuredTraining) http.HandlerFunc
		values  url.Values
		pathKey string
		pathID  string
		mutate  func(*structuredTrainingStoreStub, error)
		want    int
	}{
		{name: "day stale", handler: func(h StructuredTraining) http.HandlerFunc { return h.CopyDay }, values: dayValues, mutate: func(s *structuredTrainingStoreStub, err error) { s.dayCopyErr = err }, want: http.StatusForbidden},
		{name: "day service", handler: func(h StructuredTraining) http.HandlerFunc { return h.CopyDay }, values: dayValues, mutate: func(s *structuredTrainingStoreStub, err error) { s.dayCopyErr = err }, want: http.StatusInternalServerError},
		{name: "week stale", handler: func(h StructuredTraining) http.HandlerFunc { return h.CopyWeek }, values: url.Values{"week_start": {"2026-09-14"}, "title": {"Semana seguinte"}}, pathKey: "id", pathID: sourcePlanID.String(), mutate: func(s *structuredTrainingStoreStub, err error) { s.weekCopyErr = err }, want: http.StatusForbidden},
		{name: "week service", handler: func(h StructuredTraining) http.HandlerFunc { return h.CopyWeek }, values: url.Values{"week_start": {"2026-09-14"}, "title": {"Semana seguinte"}}, pathKey: "id", pathID: sourcePlanID.String(), mutate: func(s *structuredTrainingStoreStub, err error) { s.weekCopyErr = err }, want: http.StatusInternalServerError},
	} {
		for _, failure := range []struct {
			name string
			err  error
		}{{"missing", pgx.ErrNoRows}, {"unavailable", errors.New("database unavailable")}} {
			if (tc.want == http.StatusForbidden) != errors.Is(failure.err, pgx.ErrNoRows) {
				continue
			}
			t.Run(tc.name+" "+failure.name, func(t *testing.T) {
				store := &structuredTrainingStoreStub{weekOK: true, dayCopyCount: 1}
				tc.mutate(store, failure.err)
				response := performStructuredTrainingRequest(t, CurrentUser{ID: actorID, IsAdmin: true}, http.MethodPost, "/admin/treinos/estruturados", tc.values, tc.pathKey, tc.pathID, tc.handler(StructuredTraining{Store: store, Location: time.UTC}))
				if response.Code != tc.want {
					t.Fatalf("status=%d want=%d", response.Code, tc.want)
				}
			})
		}
	}
}

func TestCreateStructuredVariationWritesScopedWaterStepPatch(t *testing.T) {
	planID, membershipID, stepID, actorID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	store := &structuredTrainingStoreStub{weekOK: true}
	h := StructuredTraining{Store: store, Location: time.UTC}
	values := url.Values{
		"plan_id":          {planID.String()},
		"target":           {"ATHLETE:" + membershipID.String()},
		"subject":          {"WATER_STEP:" + stepID.String()},
		"operation":        {"OVERRIDE"},
		"change_summary":   {"Reduzir o esforço para recuperar"},
		"duration_seconds": {"75"},
		"intensity_code":   {"R4"},
	}
	r := httptest.NewRequest(http.MethodPost, "/admin/treinos/estruturados/variacoes", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actorID, IsAdmin: true}))
	w := httptest.NewRecorder()

	h.CreateVariation(w, r)

	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/treinos/estruturados#training-variations" {
		t.Fatalf("response=%d location=%q", w.Code, w.Header().Get("Location"))
	}
	if len(store.variationInputs) != 1 {
		t.Fatalf("variation inputs=%#v", store.variationInputs)
	}
	p := store.variationInputs[0]
	if p.PlanID != planID || p.SubjectID != stepID || p.TargetMembershipID == nil || *p.TargetMembershipID != membershipID || p.TargetGroupID != nil || p.CreatedByID != actorID || p.Operation != dbgen.TrainingVariationOperationOVERRIDE {
		t.Fatalf("variation params=%#v", p)
	}
	var patch map[string]any
	if err := json.Unmarshal(p.Patch, &patch); err != nil || patch["duration_seconds"] != float64(75) || patch["intensity_code"] != "R4" {
		t.Fatalf("patch=%s decoded=%#v error=%v", p.Patch, patch, err)
	}
}

func TestRetireStructuredVariationUsesOptimisticVersion(t *testing.T) {
	variationID, planID, actorID := uuid.New(), uuid.New(), uuid.New()
	store := &structuredTrainingStoreStub{planID: planID, weekOK: true, retiredRows: 1}
	h := StructuredTraining{Store: store, Location: time.UTC}
	r := httptest.NewRequest(http.MethodPost, "/admin/treinos/estruturados/variacoes/"+variationID.String(), strings.NewReader("version=3"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetPathValue("id", variationID.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actorID, IsAdmin: true}))
	w := httptest.NewRecorder()

	h.RetireVariation(w, r)

	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/treinos/estruturados#training-variations" {
		t.Fatalf("response=%d location=%q", w.Code, w.Header().Get("Location"))
	}
	if p := store.retiredVariation; p.ID != variationID || p.Version != 3 || p.RetiredByID == nil || *p.RetiredByID != actorID {
		t.Fatalf("retirement params=%#v", p)
	}

	store.retiredRows = 0
	w = httptest.NewRecorder()
	h.RetireVariation(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("stale retirement response=%d", w.Code)
	}
}

func TestStructuredVariationMutationsMapPersistenceFailures(t *testing.T) {
	planID, membershipID, stepID, variationID, actorID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	variationValues := url.Values{"plan_id": {planID.String()}, "target": {"ATHLETE:" + membershipID.String()}, "subject": {"WATER_STEP:" + stepID.String()}, "operation": {"OVERRIDE"}, "change_summary": {"Reduzir o esforço para recuperar"}, "duration_seconds": {"75"}, "intensity_code": {"R4"}}
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{name: "stale variation subject", err: pgx.ErrNoRows, want: http.StatusForbidden},
		{name: "duplicate variation", err: &pgconn.PgError{Code: "23505"}, want: http.StatusForbidden},
		{name: "variation write failure", err: errors.New("database unavailable"), want: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &structuredTrainingStoreStub{weekOK: true, createVariationErr: tc.err}
			response := performStructuredTrainingRequest(t, CurrentUser{ID: actorID, IsAdmin: true}, http.MethodPost, "/admin/treinos/estruturados/variacoes", variationValues, "", "", (StructuredTraining{Store: store, Location: time.UTC}).CreateVariation)
			if response.Code != tc.want {
				t.Fatalf("status=%d, want %d", response.Code, tc.want)
			}
		})
	}

	for _, tc := range []struct {
		name      string
		planErr   error
		retireErr error
		want      int
	}{
		{name: "missing variation", planErr: pgx.ErrNoRows, want: http.StatusNotFound},
		{name: "variation lookup failure", planErr: errors.New("database unavailable"), want: http.StatusInternalServerError},
		{name: "retirement write failure", retireErr: errors.New("database unavailable"), want: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &structuredTrainingStoreStub{planID: planID, weekOK: true, retiredRows: 1, variationPlanErr: tc.planErr, retireVariationErr: tc.retireErr}
			response := performStructuredTrainingRequest(t, CurrentUser{ID: actorID, IsAdmin: true}, http.MethodPost, "/admin/treinos/estruturados/variacoes/"+variationID.String()+"/retirar", url.Values{"version": {"1"}}, "id", variationID.String(), (StructuredTraining{Store: store, Location: time.UTC}).RetireVariation)
			if response.Code != tc.want {
				t.Fatalf("status=%d, want %d", response.Code, tc.want)
			}
		})
	}
}

func TestCreateStructuredVariationGroupPersistsManagedSubgroup(t *testing.T) {
	groupID, membershipID, actorID := uuid.New(), uuid.New(), uuid.New()
	store := &structuredTrainingStoreStub{manageable: true}
	h := StructuredTraining{Store: store, Location: time.UTC}
	values := url.Values{"training_group_id": {groupID.String()}, "kind": {"SUBGROUP"}, "name": {"Regata juvenil"}, "effective_from": {"2026-09-07"}, "effective_until": {"2026-09-21"}, "membership_id": {membershipID.String()}}
	r := httptest.NewRequest(http.MethodPost, "/admin/treinos/estruturados/grupos-variacao", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actorID, IsAdmin: true}))
	w := httptest.NewRecorder()

	h.CreateVariationGroup(w, r)

	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/treinos/estruturados#training-variations" {
		t.Fatalf("response=%d location=%q", w.Code, w.Header().Get("Location"))
	}
	input := store.variationGroup
	if input.Params.TrainingGroupID != groupID || input.Params.Name != "Regata juvenil" || input.Params.Kind != dbgen.TrainingVariationGroupKindSUBGROUP || input.Params.CreatedByID != actorID || !input.Params.EffectiveUntil.Valid || !input.Params.EffectiveUntil.Time.Equal(time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)) || len(input.MembershipIDs) != 1 || input.MembershipIDs[0] != membershipID {
		t.Fatalf("variation group input=%#v", input)
	}
}

func TestCreateStructuredVariationGroupPersistsCompetitionBoundCrew(t *testing.T) {
	groupID, craftID, competitionID, firstMember, secondMember, actorID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	store := &structuredTrainingStoreStub{manageable: true, crewModalities: []dbgen.ListStructuredCrewModalitiesRow{{ID: craftID, Code: "K2", NamePt: "Kayak duplo"}}, competitionEvents: []dbgen.ListManagedStructuredCompetitionEventsRow{{ID: competitionID, Title: "Regata", StartsAt: pgtype.Timestamptz{Time: time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC), Valid: true}}}}
	values := url.Values{"training_group_id": {groupID.String()}, "kind": {"CREW"}, "name": {"K2 regata"}, "effective_from": {"2026-09-07"}, "craft_modality_id": {craftID.String()}, "competition_event_id": {competitionID.String()}, "membership_id": {firstMember.String(), secondMember.String()}}
	response := performStructuredTrainingRequest(t, CurrentUser{ID: actorID, IsAdmin: true}, http.MethodPost, "/admin/treinos/estruturados/grupos-variacao", values, "", "", (StructuredTraining{Store: store, Location: time.UTC}).CreateVariationGroup)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("status=%d", response.Code)
	}
	input := store.variationGroup
	if input.Params.Kind != dbgen.TrainingVariationGroupKindCREW || input.Params.CraftModalityID == nil || *input.Params.CraftModalityID != craftID || input.Params.CompetitionEventID == nil || *input.Params.CompetitionEventID != competitionID || len(input.MembershipIDs) != 2 {
		t.Fatalf("crew input=%#v", input)
	}
}

func TestCreateStructuredVariationGroupRejectsInvalidCrewAndSurfacesLookupFailures(t *testing.T) {
	groupID, craftID, competitionID, firstMember, secondMember, actorID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	base := url.Values{
		"training_group_id": {groupID.String()}, "kind": {"CREW"}, "name": {"K2 regata"}, "effective_from": {"2026-09-07"},
		"craft_modality_id": {craftID.String()}, "competition_event_id": {competitionID.String()}, "membership_id": {firstMember.String(), secondMember.String()},
	}
	allowedCraft := []dbgen.ListStructuredCrewModalitiesRow{{ID: craftID, Code: "K2", NamePt: "Kayak duplo"}}
	allowedEvent := []dbgen.ListManagedStructuredCompetitionEventsRow{{ID: competitionID, Title: "Regata", StartsAt: pgtype.Timestamptz{Time: time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC), Valid: true}}}
	for _, tc := range []struct {
		name   string
		values url.Values
		store  structuredTrainingStoreStub
		want   int
	}{
		{name: "crew capacity mismatch", values: url.Values{"training_group_id": {groupID.String()}, "kind": {"CREW"}, "name": {"K2 regata"}, "effective_from": {"2026-09-07"}, "craft_modality_id": {craftID.String()}, "competition_event_id": {competitionID.String()}, "membership_id": {firstMember.String()}}, store: structuredTrainingStoreStub{manageable: true, crewModalities: allowedCraft}, want: http.StatusForbidden},
		{name: "crew modality lookup failure", values: base, store: structuredTrainingStoreStub{manageable: true, crewModalitiesErr: errors.New("database unavailable")}, want: http.StatusInternalServerError},
		{name: "competition lookup failure", values: base, store: structuredTrainingStoreStub{manageable: true, crewModalities: allowedCraft, competitionEventsErr: errors.New("database unavailable")}, want: http.StatusInternalServerError},
		{name: "competition before effective date", values: base, store: structuredTrainingStoreStub{manageable: true, crewModalities: allowedCraft, competitionEvents: []dbgen.ListManagedStructuredCompetitionEventsRow{{ID: competitionID, StartsAt: pgtype.Timestamptz{Time: time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC), Valid: true}}}}, want: http.StatusForbidden},
		{name: "unknown competition", values: base, store: structuredTrainingStoreStub{manageable: true, crewModalities: allowedCraft, competitionEvents: allowedEvent[:0]}, want: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := tc.store
			response := performStructuredTrainingRequest(t, CurrentUser{ID: actorID, IsAdmin: true}, http.MethodPost, "/admin/treinos/estruturados/grupos-variacao", tc.values, "", "", (StructuredTraining{Store: &store, Location: time.UTC}).CreateVariationGroup)
			if response.Code != tc.want {
				t.Fatalf("status=%d, want %d", response.Code, tc.want)
			}
			if store.variationGroup.Params.TrainingGroupID != uuid.Nil {
				t.Fatalf("rejected crew must not be persisted: %#v", store.variationGroup)
			}
		})
	}
}

func TestCreateStructuredVariationGroupMapsDomainAndServiceWriteFailures(t *testing.T) {
	groupID, membershipID, actorID := uuid.New(), uuid.New(), uuid.New()
	values := url.Values{"training_group_id": {groupID.String()}, "kind": {"SUBGROUP"}, "name": {"Regata juvenil"}, "effective_from": {"2026-09-07"}, "effective_until": {"2026-09-21"}, "membership_id": {membershipID.String()}}
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{name: "membership scope conflict", err: errStructuredVariationMemberScope, want: http.StatusForbidden},
		{name: "crew capacity conflict", err: errStructuredVariationCrewCapacity, want: http.StatusForbidden},
		{name: "unexpected persistence failure", err: errors.New("database unavailable"), want: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &structuredTrainingStoreStub{manageable: true, createVariationGroupErr: tc.err}
			response := performStructuredTrainingRequest(t, CurrentUser{ID: actorID, IsAdmin: true}, http.MethodPost, "/admin/treinos/estruturados/grupos-variacao", values, "", "", (StructuredTraining{Store: store, Location: time.UTC}).CreateVariationGroup)
			if response.Code != tc.want {
				t.Fatalf("status=%d, want %d", response.Code, tc.want)
			}
		})
	}
}

func TestStructuredTrainingIndexRendersEmptyAdministratorPlanningWorkspace(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/admin/treinos/estruturados?routine_query=tecnica", nil)
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: uuid.New(), Name: "Coordenadora", IsAdmin: true, EmailVerified: true}))
	w := httptest.NewRecorder()

	(StructuredTraining{Store: &structuredTrainingStoreStub{}, Location: time.UTC}).Index(w, r)

	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Planeamento semanal") || !strings.Contains(w.Body.String(), "Coordenadora") {
		t.Fatalf("response = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestStructuredTrainingManagerIndexFailsClosedForEveryRequiredRead(t *testing.T) {
	user := CurrentUser{ID: uuid.New(), IsAdmin: true}
	for _, tc := range []struct {
		name  string
		store structuredTrainingStoreStub
	}{
		{"overview", structuredTrainingStoreStub{overviewErr: errors.New("database unavailable")}},
		{"routine library", structuredTrainingStoreStub{routinesErr: errors.New("database unavailable")}},
		{"water profiles", structuredTrainingStoreStub{listProfilesErr: errors.New("database unavailable")}},
		{"eligible memberships", structuredTrainingStoreStub{membershipsErr: errors.New("database unavailable")}},
		{"variation members", structuredTrainingStoreStub{variationMembersErr: errors.New("database unavailable")}},
		{"crew modalities", structuredTrainingStoreStub{crewModalitiesErr: errors.New("database unavailable")}},
		{"competition events", structuredTrainingStoreStub{competitionEventsErr: errors.New("database unavailable")}},
		{"variation groups", structuredTrainingStoreStub{variationGroupsErr: errors.New("database unavailable")}},
		{"variation matches", structuredTrainingStoreStub{variationMatchesErr: errors.New("database unavailable")}},
		{"publication states", structuredTrainingStoreStub{publicationStatesErr: errors.New("database unavailable")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/admin/treinos/estruturados", nil)
			r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, user))
			w := httptest.NewRecorder()
			(StructuredTraining{Store: &tc.store, Location: time.UTC}).Index(w, r)
			if w.Code != http.StatusInternalServerError {
				t.Fatalf("response=%d", w.Code)
			}
		})
	}
}

func TestPostgresStructuredTrainingStorePropagatesDirectDatabaseFailures(t *testing.T) {
	want := errors.New("database unavailable")
	store := PostgresStructuredTrainingStore{Pool: structuredTrainingDBFake{err: want}}
	id := uuid.New()
	ctx := context.Background()
	tests := []struct {
		name string
		call func() error
	}{
		{"eligible memberships", func() error {
			_, err := store.ListEligibleTrainingGroupMemberships(ctx, dbgen.ListEligibleTrainingGroupMembershipsParams{})
			return err
		}},
		{"manager overview", func() error {
			_, err := store.ListStructuredTrainingOverviewForManager(ctx, dbgen.ListStructuredTrainingOverviewForManagerParams{})
			return err
		}},
		{"subject overview", func() error { _, err := store.ListStructuredTrainingOverviewForSubject(ctx, id); return err }},
		{"publication states", func() error {
			_, err := store.ListManagedTrainingPublicationStates(ctx, dbgen.ListManagedTrainingPublicationStatesParams{})
			return err
		}},
		{"publication members", func() error {
			_, err := store.ListStructuredTrainingPublicationMembers(ctx, dbgen.ListStructuredTrainingPublicationMembersParams{})
			return err
		}},
		{"viewer prescriptions", func() error {
			_, err := store.ListTrainingPrescriptionsForViewer(ctx, dbgen.ListTrainingPrescriptionsForViewerParams{})
			return err
		}},
		{"latest hashes", func() error { _, err := store.ListLatestTrainingPrescriptionHashesForPlan(ctx, id); return err }},
		{"viewer prescription", func() error {
			_, err := store.GetTrainingPrescriptionForViewer(ctx, dbgen.GetTrainingPrescriptionForViewerParams{})
			return err
		}},
		{"prescription links", func() error {
			_, err := store.ListTrainingPrescriptionLinksForSessionViewer(ctx, dbgen.ListTrainingPrescriptionLinksForSessionViewerParams{})
			return err
		}},
		{"manage group", func() error {
			_, err := store.CanManageStructuredTrainingGroup(ctx, dbgen.CanManageStructuredTrainingGroupParams{})
			return err
		}},
		{"create week", func() error {
			_, err := store.CreateStructuredTrainingWeek(ctx, dbgen.CreateStructuredTrainingWeekParams{})
			return err
		}},
		{"manage week", func() error {
			_, err := store.CanManageStructuredTrainingWeek(ctx, dbgen.CanManageStructuredTrainingWeekParams{})
			return err
		}},
		{"update week load", func() error {
			_, err := store.UpdateStructuredTrainingWeekLoad(ctx, dbgen.UpdateStructuredTrainingWeekLoadParams{})
			return err
		}},
		{"create session", func() error {
			_, err := store.CreateStructuredTrainingSession(ctx, dbgen.CreateStructuredTrainingSessionParams{})
			return err
		}},
		{"create segment block", func() error {
			_, err := store.CreateTrainingSegmentBlock(ctx, dbgen.CreateTrainingSegmentBlockParams{})
			return err
		}},
		{"create gym exercise", func() error { _, err := store.CreateGymExercise(ctx, dbgen.CreateGymExerciseParams{}); return err }},
		{"create water step", func() error { _, err := store.CreateWaterWorkStep(ctx, dbgen.CreateWaterWorkStepParams{}); return err }},
		{"create water profile", func() error {
			_, err := store.CreateWaterIntensityProfile(ctx, dbgen.CreateWaterIntensityProfileParams{})
			return err
		}},
		{"create water zone", func() error {
			_, err := store.CreateWaterIntensityZone(ctx, dbgen.CreateWaterIntensityZoneParams{})
			return err
		}},
		{"water profiles", func() error { _, err := store.ListActiveWaterIntensityProfiles(ctx); return err }},
		{"session plan", func() error { _, err := store.GetStructuredSessionPlanID(ctx, id); return err }},
		{"segment plan", func() error { _, err := store.GetStructuredSegmentPlanID(ctx, id); return err }},
		{"block plan", func() error { _, err := store.GetStructuredBlockPlanID(ctx, id); return err }},
		{"exercise plan", func() error { _, err := store.GetGymExercisePlanID(ctx, id); return err }},
		{"move segment", func() error {
			_, err := store.MoveTrainingSessionSegment(ctx, dbgen.MoveTrainingSessionSegmentParams{})
			return err
		}},
		{"move block", func() error {
			_, err := store.MoveTrainingSegmentBlock(ctx, dbgen.MoveTrainingSegmentBlockParams{})
			return err
		}},
		{"move exercise", func() error { _, err := store.MoveGymExercise(ctx, dbgen.MoveGymExerciseParams{}); return err }},
		{"routine source", func() error { _, err := store.GetRoutineSource(ctx, dbgen.TrainingRoutineKindBLOCK, id); return err }},
		{"create routine", func() error {
			_, err := store.CreateTrainingRoutine(ctx, dbgen.CreateTrainingRoutineParams{})
			return err
		}},
		{"visible routines", func() error {
			_, err := store.ListVisibleTrainingRoutines(ctx, dbgen.ListVisibleTrainingRoutinesParams{})
			return err
		}},
		{"visible routine", func() error {
			_, err := store.GetVisibleTrainingRoutine(ctx, dbgen.GetVisibleTrainingRoutineParams{})
			return err
		}},
		{"copy source", func() error { _, err := store.GetStructuredPlanCopySource(ctx, id); return err }},
		{"managed members", func() error {
			_, err := store.ListManagedTrainingGroupMembers(ctx, dbgen.ListManagedTrainingGroupMembersParams{})
			return err
		}},
		{"crew modalities", func() error { _, err := store.ListStructuredCrewModalities(ctx); return err }},
		{"competition events", func() error {
			_, err := store.ListManagedStructuredCompetitionEvents(ctx, dbgen.ListManagedStructuredCompetitionEventsParams{})
			return err
		}},
		{"variation groups", func() error {
			_, err := store.ListManagedTrainingVariationGroups(ctx, dbgen.ListManagedTrainingVariationGroupsParams{})
			return err
		}},
		{"create variation", func() error {
			_, err := store.CreateTrainingVariation(ctx, dbgen.CreateTrainingVariationParams{})
			return err
		}},
		{"variation matches", func() error {
			_, err := store.ListTrainingVariationMatchesForManager(ctx, dbgen.ListTrainingVariationMatchesForManagerParams{})
			return err
		}},
		{"variation plan", func() error { _, err := store.GetTrainingVariationPlanID(ctx, id); return err }},
		{"retire variation", func() error {
			_, err := store.RetireTrainingVariation(ctx, dbgen.RetireTrainingVariationParams{})
			return err
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); !errors.Is(err, want) {
				t.Fatalf("error=%v want=%v", err, want)
			}
		})
	}
}

func TestPostgresStructuredTrainingStorePropagatesTransactionStartFailures(t *testing.T) {
	want := errors.New("database unavailable")
	store := PostgresStructuredTrainingStore{Pool: structuredTrainingDBFake{err: want}}
	id := uuid.New()
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"publish", func() error {
			_, err := store.PublishStructuredTrainingPlan(ctx, StructuredPublicationInput{})
			return err
		}},
		{"create group", func() error { _, err := store.CreateGroup(ctx, StructuredTrainingGroupInput{}); return err }},
		{"create segment", func() error { _, err := store.CreateSegment(ctx, StructuredTrainingSegmentInput{}); return err }},
		{"create gym block", func() error { _, err := store.CreateGymBlock(ctx, StructuredGymBlockInput{}); return err }},
		{"create water block", func() error { _, err := store.CreateWaterBlock(ctx, StructuredWaterBlockInput{}); return err }},
		{"insert routine", func() error { _, err := store.InsertTrainingRoutine(ctx, StructuredRoutineInsertInput{}); return err }},
		{"copy block", func() error { _, err := store.CopyTrainingBlock(ctx, id, id, id); return err }},
		{"copy session", func() error { _, err := store.CopyTrainingSession(ctx, id, id, pgtype.Timestamptz{}, id); return err }},
		{"copy day", func() error { _, err := store.CopyStructuredTrainingDay(ctx, StructuredDayCopyInput{}); return err }},
		{"copy week", func() error { _, err := store.CopyStructuredTrainingWeek(ctx, StructuredWeekCopyInput{}); return err }},
		{"create variation group", func() error {
			_, err := store.CreateTrainingVariationGroup(ctx, StructuredVariationGroupInput{})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); !errors.Is(err, want) {
				t.Fatalf("error=%v want=%v", err, want)
			}
		})
	}
}

func TestPostgresStructuredTrainingStorePropagatesFirstTransactionalOperationFailure(t *testing.T) {
	want := errors.New("database unavailable")
	store := PostgresStructuredTrainingStore{Pool: structuredTrainingDBFake{tx: structuredTrainingTransactionFake{err: want}}}
	id := uuid.New()
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"publish", func() error {
			_, err := store.PublishStructuredTrainingPlan(ctx, StructuredPublicationInput{PlanID: id})
			return err
		}},
		{"create group", func() error { _, err := store.CreateGroup(ctx, StructuredTrainingGroupInput{}); return err }},
		{"create segment", func() error { _, err := store.CreateSegment(ctx, StructuredTrainingSegmentInput{}); return err }},
		{"create gym block", func() error { _, err := store.CreateGymBlock(ctx, StructuredGymBlockInput{}); return err }},
		{"create water block", func() error { _, err := store.CreateWaterBlock(ctx, StructuredWaterBlockInput{}); return err }},
		{"insert routine", func() error {
			_, err := store.InsertTrainingRoutine(ctx, StructuredRoutineInsertInput{Routine: dbgen.TrainingRoutine{Kind: dbgen.TrainingRoutineKindBLOCK}})
			return err
		}},
		{"copy block", func() error { _, err := store.CopyTrainingBlock(ctx, id, id, id); return err }},
		{"copy session", func() error { _, err := store.CopyTrainingSession(ctx, id, id, pgtype.Timestamptz{}, id); return err }},
		{"copy day", func() error {
			_, err := store.CopyStructuredTrainingDay(ctx, StructuredDayCopyInput{SourcePlanID: id, TargetPlanID: id})
			return err
		}},
		{"copy week", func() error {
			_, err := store.CopyStructuredTrainingWeek(ctx, StructuredWeekCopyInput{SourcePlanID: id})
			return err
		}},
		{"create variation group", func() error {
			_, err := store.CreateTrainingVariationGroup(ctx, StructuredVariationGroupInput{})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expected := want
			if tc.name == "copy week" {
				expected = pgx.ErrNoRows
			}
			if err := tc.call(); !errors.Is(err, expected) {
				t.Fatalf("error=%v want=%v", err, expected)
			}
		})
	}
}

func TestPostgresStructuredTrainingStoreCompletesAtomicSegmentAndBlockCreation(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		call func(PostgresStructuredTrainingStore) (uuid.UUID, error)
	}{
		{"segment with initial block", func(store PostgresStructuredTrainingStore) (uuid.UUID, error) {
			return store.CreateSegment(ctx, StructuredTrainingSegmentInput{})
		}},
		{"gym block with prescription and exercise", func(store PostgresStructuredTrainingStore) (uuid.UUID, error) {
			return store.CreateGymBlock(ctx, StructuredGymBlockInput{})
		}},
		{"water block with prescription and step", func(store PostgresStructuredTrainingStore) (uuid.UUID, error) {
			return store.CreateWaterBlock(ctx, StructuredWaterBlockInput{})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := &structuredTrainingSuccessTransactionFake{}
			id, err := tc.call(PostgresStructuredTrainingStore{Pool: structuredTrainingDBFake{tx: tx}})
			if err != nil || id == uuid.Nil || !tx.committed {
				t.Fatalf("id=%s committed=%v error=%v", id, tx.committed, err)
			}
		})
	}
}

func TestPostgresStructuredTrainingStoreRestoresEveryRoutineKindAtomically(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		routine dbgen.TrainingRoutine
		starts  pgtype.Timestamptz
	}{
		{"block", dbgen.TrainingRoutine{ID: uuid.New(), Kind: dbgen.TrainingRoutineKindBLOCK, Snapshot: []byte(`{}`)}, pgtype.Timestamptz{}},
		{"segment", dbgen.TrainingRoutine{ID: uuid.New(), Kind: dbgen.TrainingRoutineKindSEGMENT, Snapshot: []byte(`{}`)}, pgtype.Timestamptz{}},
		{"session", dbgen.TrainingRoutine{ID: uuid.New(), Kind: dbgen.TrainingRoutineKindSESSION, Snapshot: []byte(`{}`)}, pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := &structuredTrainingSuccessTransactionFake{}
			id, err := (PostgresStructuredTrainingStore{Pool: structuredTrainingDBFake{tx: tx}}).InsertTrainingRoutine(ctx, StructuredRoutineInsertInput{Routine: tc.routine, TargetID: uuid.New(), StartsAt: tc.starts, ActorID: uuid.New()})
			if err != nil || id == uuid.Nil || !tx.committed {
				t.Fatalf("id=%s committed=%v error=%v", id, tx.committed, err)
			}
		})
	}
	for _, tc := range []struct {
		name  string
		input StructuredRoutineInsertInput
	}{
		{"unknown kind", StructuredRoutineInsertInput{Routine: dbgen.TrainingRoutine{Kind: dbgen.TrainingRoutineKind("UNKNOWN")}}},
		{"session without start", StructuredRoutineInsertInput{Routine: dbgen.TrainingRoutine{Kind: dbgen.TrainingRoutineKindSESSION}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := (PostgresStructuredTrainingStore{Pool: structuredTrainingDBFake{tx: &structuredTrainingSuccessTransactionFake{}}}).InsertTrainingRoutine(ctx, tc.input)
			if !errors.Is(err, pgx.ErrNoRows) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestPostgresStructuredTrainingStoreCopiesActiveBlocksAndSessionsAtomically(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		call func(PostgresStructuredTrainingStore) (uuid.UUID, error)
	}{
		{"block", func(store PostgresStructuredTrainingStore) (uuid.UUID, error) {
			return store.CopyTrainingBlock(ctx, uuid.New(), uuid.New(), uuid.New())
		}},
		{"session", func(store PostgresStructuredTrainingStore) (uuid.UUID, error) {
			return store.CopyTrainingSession(ctx, uuid.New(), uuid.New(), pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}, uuid.New())
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := &structuredTrainingSuccessTransactionFake{}
			id, err := tc.call(PostgresStructuredTrainingStore{Pool: structuredTrainingDBFake{tx: tx}})
			if err != nil || id == uuid.Nil || !tx.committed {
				t.Fatalf("id=%s committed=%v error=%v", id, tx.committed, err)
			}
		})
	}
}

func TestPostgresStructuredTrainingStoreCommitsEmptyDayAndWeekCopies(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		call func(PostgresStructuredTrainingStore) error
	}{
		{"day", func(store PostgresStructuredTrainingStore) error {
			copied, err := store.CopyStructuredTrainingDay(ctx, StructuredDayCopyInput{SourcePlanID: uuid.New(), TargetPlanID: uuid.New(), SourceDate: time.Now().UTC(), TargetDate: time.Now().UTC(), ActorID: uuid.New()})
			if copied != 0 {
				return errors.New("unexpected copied sessions")
			}
			return err
		}},
		{"week", func(store PostgresStructuredTrainingStore) error {
			_, err := store.CopyStructuredTrainingWeek(ctx, StructuredWeekCopyInput{SourcePlanID: uuid.New(), WeekStart: time.Now().UTC(), Title: "Copied", ActorID: uuid.New()})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := &structuredTrainingSuccessTransactionFake{}
			if err := tc.call(PostgresStructuredTrainingStore{Pool: structuredTrainingDBFake{tx: tx}}); err != nil || !tx.committed {
				t.Fatalf("committed=%v error=%v", tx.committed, err)
			}
		})
	}
}

func TestPostgresStructuredTrainingStoreCreatesScopedGroupsAtomically(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		call func(PostgresStructuredTrainingStore) error
	}{
		{"training group", func(store PostgresStructuredTrainingStore) error {
			group, err := store.CreateGroup(ctx, StructuredTrainingGroupInput{Params: dbgen.CreateStructuredTrainingGroupParams{CreatedByID: uuid.New()}, MembershipIDs: []uuid.UUID{uuid.New()}})
			if group.ID == uuid.Nil {
				return errors.New("missing training group ID")
			}
			return err
		}},
		{"variation subgroup", func(store PostgresStructuredTrainingStore) error {
			group, err := store.CreateTrainingVariationGroup(ctx, StructuredVariationGroupInput{Params: dbgen.CreateTrainingVariationGroupParams{CreatedByID: uuid.New(), Kind: dbgen.TrainingVariationGroupKindSUBGROUP}, MembershipIDs: []uuid.UUID{uuid.New()}})
			if group.ID == uuid.Nil {
				return errors.New("missing variation group ID")
			}
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := &structuredTrainingSuccessTransactionFake{}
			if err := tc.call(PostgresStructuredTrainingStore{Pool: structuredTrainingDBFake{tx: tx}}); err != nil || !tx.committed {
				t.Fatalf("committed=%v error=%v", tx.committed, err)
			}
		})
	}
}

func TestPostgresStructuredTrainingStoreReadsEverySupportedRoutineSourceKind(t *testing.T) {
	store := PostgresStructuredTrainingStore{Pool: structuredTrainingDBFake{tx: &structuredTrainingSuccessTransactionFake{}}}
	for _, kind := range []dbgen.TrainingRoutineKind{dbgen.TrainingRoutineKindBLOCK, dbgen.TrainingRoutineKindSEGMENT, dbgen.TrainingRoutineKindSESSION} {
		source, err := store.GetRoutineSource(t.Context(), kind, uuid.New())
		if err != nil || source.PlanID == uuid.Nil || len(source.Snapshot) == 0 {
			t.Fatalf("kind=%s source=%#v error=%v", kind, source, err)
		}
	}
	if _, err := store.GetRoutineSource(t.Context(), dbgen.TrainingRoutineKind("UNKNOWN"), uuid.New()); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("unknown routine kind error=%v", err)
	}
}

func TestPostgresStructuredTrainingStorePublishesFreshPlanAtomically(t *testing.T) {
	updated := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	tx := &structuredTrainingPublicationTransactionFake{updated: updated}
	publication, err := (PostgresStructuredTrainingStore{Pool: structuredTrainingDBFake{tx: tx}}).PublishStructuredTrainingPlan(t.Context(), StructuredPublicationInput{PlanID: uuid.New(), SourceUpdatedAt: pgtype.Timestamptz{Time: updated, Valid: true}, ChangeSummary: "Initial publication", PublishedByID: uuid.New()})
	if err != nil || publication.ID == uuid.Nil || !tx.committed {
		t.Fatalf("publication=%#v committed=%v error=%v", publication, tx.committed, err)
	}
}

func (s *structuredTrainingStoreStub) ListStructuredTrainingOverviewForManager(context.Context, dbgen.ListStructuredTrainingOverviewForManagerParams) ([]dbgen.ListStructuredTrainingOverviewForManagerRow, error) {
	return s.overviewRows, s.overviewErr
}

func (s *structuredTrainingStoreStub) ListVisibleTrainingRoutines(context.Context, dbgen.ListVisibleTrainingRoutinesParams) ([]dbgen.ListVisibleTrainingRoutinesRow, error) {
	return nil, s.routinesErr
}

func (s *structuredTrainingStoreStub) ListEligibleTrainingGroupMemberships(context.Context, dbgen.ListEligibleTrainingGroupMembershipsParams) ([]dbgen.ListEligibleTrainingGroupMembershipsRow, error) {
	return nil, s.membershipsErr
}

func (s *structuredTrainingStoreStub) ListManagedTrainingGroupMembers(context.Context, dbgen.ListManagedTrainingGroupMembersParams) ([]dbgen.ListManagedTrainingGroupMembersRow, error) {
	return nil, s.variationMembersErr
}

func (s *structuredTrainingStoreStub) ListStructuredCrewModalities(context.Context) ([]dbgen.ListStructuredCrewModalitiesRow, error) {
	return s.crewModalities, s.crewModalitiesErr
}

func (s *structuredTrainingStoreStub) ListManagedStructuredCompetitionEvents(context.Context, dbgen.ListManagedStructuredCompetitionEventsParams) ([]dbgen.ListManagedStructuredCompetitionEventsRow, error) {
	return s.competitionEvents, s.competitionEventsErr
}

func (s *structuredTrainingStoreStub) ListManagedTrainingVariationGroups(context.Context, dbgen.ListManagedTrainingVariationGroupsParams) ([]dbgen.ListManagedTrainingVariationGroupsRow, error) {
	return nil, s.variationGroupsErr
}

func (s *structuredTrainingStoreStub) ListTrainingVariationMatchesForManager(context.Context, dbgen.ListTrainingVariationMatchesForManagerParams) ([]dbgen.ListTrainingVariationMatchesForManagerRow, error) {
	return s.variationMatches, s.variationMatchesErr
}

func (s *structuredTrainingStoreStub) ListManagedTrainingPublicationStates(context.Context, dbgen.ListManagedTrainingPublicationStatesParams) ([]dbgen.ListManagedTrainingPublicationStatesRow, error) {
	return s.publicationStates, s.publicationStatesErr
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
	return true, s.moveGymErr
}

func (s *structuredTrainingStoreStub) GetRoutineSource(context.Context, dbgen.TrainingRoutineKind, uuid.UUID) (StructuredRoutineSource, error) {
	return s.routineSource, s.routineSourceErr
}

func (s *structuredTrainingStoreStub) CreateTrainingRoutine(_ context.Context, params dbgen.CreateTrainingRoutineParams) (dbgen.TrainingRoutine, error) {
	s.createdRoutines = append(s.createdRoutines, params)
	return dbgen.TrainingRoutine{ID: uuid.New()}, s.createRoutineErr
}

func (s *structuredTrainingStoreStub) GetVisibleTrainingRoutine(context.Context, dbgen.GetVisibleTrainingRoutineParams) (dbgen.TrainingRoutine, error) {
	return s.visibleRoutine, s.visibleRoutineErr
}

func (s *structuredTrainingStoreStub) InsertTrainingRoutine(_ context.Context, input StructuredRoutineInsertInput) (uuid.UUID, error) {
	s.insertedRoutines = append(s.insertedRoutines, input)
	return uuid.New(), s.insertRoutineErr
}

func (s *structuredTrainingStoreStub) CopyTrainingBlock(_ context.Context, sourceID, targetID, actorID uuid.UUID) (uuid.UUID, error) {
	s.copiedBlocks = append(s.copiedBlocks, [3]uuid.UUID{sourceID, targetID, actorID})
	return uuid.New(), nil
}

func TestFilterStructuredTrainingSubjectRowsUsesOnlyAuthorizedQueryResults(t *testing.T) {
	actorID, dependentID := uuid.New(), uuid.New()
	rows := []dbgen.ListTrainingPrescriptionsForViewerRow{
		{AthleteUserID: actorID, AthleteName: "Marta"},
		{AthleteUserID: dependentID, AthleteName: "Leonor"},
		{AthleteUserID: dependentID, AthleteName: "Leonor"},
	}
	filtered, subjects, selected, err := filterStructuredTrainingSubjectRows(rows, dependentID.String())
	if err != nil || selected == nil || selected.ID != dependentID || selected.Name != "Leonor" || len(subjects) != 2 || len(filtered) != 2 {
		t.Fatalf("filtered = %#v, subjects = %#v, selected = %#v, err = %v", filtered, subjects, selected, err)
	}
	for _, row := range filtered {
		if row.AthleteUserID != dependentID {
			t.Fatalf("foreign row survived filter: %#v", row)
		}
	}
}

func TestFilterStructuredTrainingSubjectRowsFailsClosed(t *testing.T) {
	rows := []dbgen.ListTrainingPrescriptionsForViewerRow{{AthleteUserID: uuid.New(), AthleteName: "Marta"}}
	for _, requested := range []string{"invalid", uuid.NewString()} {
		if _, _, _, err := filterStructuredTrainingSubjectRows(rows, requested); !errors.Is(err, errStructuredTrainingSubjectNotFound) {
			t.Errorf("requested %q error = %v", requested, err)
		}
	}
	all, subjects, selected, err := filterStructuredTrainingSubjectRows(rows, "")
	if err != nil || len(all) != 1 || len(subjects) != 1 || selected != nil {
		t.Fatalf("unfiltered rows = %#v, subjects = %#v, selected = %#v, err = %v", all, subjects, selected, err)
	}
}

func (s *structuredTrainingStoreStub) CopyTrainingSession(_ context.Context, sourceID, targetID uuid.UUID, startsAt pgtype.Timestamptz, actorID uuid.UUID) (uuid.UUID, error) {
	s.copiedSessions = append(s.copiedSessions, struct {
		SourceID, TargetID, ActorID uuid.UUID
		StartsAt                    pgtype.Timestamptz
	}{sourceID, targetID, actorID, startsAt})
	return uuid.New(), nil
}

func (s *structuredTrainingStoreStub) ListTrainingPrescriptionLinksForSessionViewer(context.Context, dbgen.ListTrainingPrescriptionLinksForSessionViewerParams) ([]dbgen.ListTrainingPrescriptionLinksForSessionViewerRow, error) {
	return s.prescriptionLinks, nil
}

func (s *structuredTrainingStoreStub) GetTrainingPrescriptionForViewer(context.Context, dbgen.GetTrainingPrescriptionForViewerParams) (dbgen.GetTrainingPrescriptionForViewerRow, error) {
	return s.prescriptionRow, s.prescriptionErr
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

func TestStructuredVariationPreviewResolvesPrecedenceAndFlagsPeerConflicts(t *testing.T) {
	planID, sessionID, subjectID, membershipID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	startsAt := time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)
	row := func(priority int32, target string) dbgen.ListTrainingVariationMatchesForManagerRow {
		return dbgen.ListTrainingVariationMatchesForManagerRow{
			VariationID: uuid.New(), PlanID: planID, PlanTitle: "M34", SessionID: sessionID,
			SessionTitle: "Água", StartsAt: pgtype.Timestamptz{Time: startsAt, Valid: true},
			SubjectKind: dbgen.TrainingVariationSubjectKindWATERSTEP, SubjectID: subjectID, SubjectLabel: "Passo de água · Séries",
			Operation: dbgen.TrainingVariationOperationOVERRIDE, ChangeSummary: "Carga ajustada", Patch: []byte(`{"intensity_code":"R4"}`), Version: 1,
			MembershipID: &membershipID, AthleteName: "Ana", Priority: priority, TargetKind: target, TargetName: target,
		}
	}

	resolved := structuredVariationPreviews([]dbgen.ListTrainingVariationMatchesForManagerRow{row(2, "Atleta"), row(1, "Tripulação")}, time.UTC)
	if len(resolved) != 1 || resolved[0].Conflict || !resolved[0].Rules[0].Applied || resolved[0].Rules[1].Applied {
		t.Fatalf("resolved precedence = %#v", resolved)
	}

	conflicted := structuredVariationPreviews([]dbgen.ListTrainingVariationMatchesForManagerRow{row(1, "Tripulação"), row(1, "Subgrupo")}, time.UTC)
	if len(conflicted) != 1 || !conflicted[0].Conflict || !conflicted[0].Rules[0].Applied || !conflicted[0].Rules[1].Applied {
		t.Fatalf("peer conflict = %#v", conflicted)
	}
}

func TestParseTrainingVariationPatchKeepsSubjectFieldsBounded(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(url.Values{
		"duration_seconds": {"120"}, "intensity_code": {"R7"}, "sets": {"4"},
	}.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := request.ParseForm(); err != nil {
		t.Fatal(err)
	}
	if _, err := parseTrainingVariationPatch(request, dbgen.TrainingVariationSubjectKindWATERSTEP); err == nil {
		t.Fatal("water step accepted a gym-only sets field")
	}

	delete(request.PostForm, "sets")
	patch, err := parseTrainingVariationPatch(request, dbgen.TrainingVariationSubjectKindWATERSTEP)
	if err != nil || patch["duration_seconds"] != int32(120) || patch["intensity_code"] != "R7" {
		t.Fatalf("patch = %#v, err = %v", patch, err)
	}
}

func TestStructuredCrewSizeUsesConfiguredCraftCapacity(t *testing.T) {
	craftID := uuid.New()
	rows := []dbgen.ListStructuredCrewModalitiesRow{{ID: craftID, Code: "C4", NamePt: "Canoa de quatro"}}
	if size, valid := structuredCrewSize(rows, &craftID); !valid || size != 4 {
		t.Fatalf("size = %d, valid = %t", size, valid)
	}
	unknown := uuid.New()
	if _, valid := structuredCrewSize(rows, &unknown); valid {
		t.Fatal("unknown craft was accepted")
	}
}

func TestStructuredVariationChoicesDescribeTargetsGroupsAndSubjects(t *testing.T) {
	groupID, membershipID, variationGroupID := uuid.New(), uuid.New(), uuid.New()
	craftID, competitionID := uuid.New(), uuid.New()
	craftCode, competitionTitle := "C2", "Taça de Portugal"
	members := []dbgen.ListManagedTrainingGroupMembersRow{{
		TrainingGroupID: groupID, TrainingGroupName: "Seniores", MembershipID: membershipID, AthleteName: "Ana",
	}}
	groups := []dbgen.ListManagedTrainingVariationGroupsRow{
		{
			ID: variationGroupID, TrainingGroupID: groupID, TrainingGroupName: "Seniores", Name: "C2 principal",
			Kind: dbgen.TrainingVariationGroupKindCREW, CraftCode: &craftCode,
			EffectiveFrom:      pgtype.Date{Time: time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC), Valid: true},
			CompetitionEventID: &competitionID, CompetitionEventTitle: &competitionTitle,
			MembershipID: membershipID, AthleteName: "Ana",
		},
		{
			ID: variationGroupID, TrainingGroupID: groupID, TrainingGroupName: "Seniores", Name: "C2 principal",
			Kind: dbgen.TrainingVariationGroupKindCREW, CraftCode: &craftCode,
			EffectiveFrom:      pgtype.Date{Time: time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC), Valid: true},
			CompetitionEventID: &competitionID, CompetitionEventTitle: &competitionTitle,
			MembershipID: uuid.New(), AthleteName: "Beatriz",
		},
	}

	memberChoices := structuredVariationMembers(members)
	if len(memberChoices) != 1 || memberChoices[0].ID != membershipID.String() || memberChoices[0].Athlete != "Ana" {
		t.Fatalf("member choices = %#v", memberChoices)
	}
	crewChoices := structuredCrewModalities([]dbgen.ListStructuredCrewModalitiesRow{{ID: craftID, Code: craftCode, NamePt: "Canoa de dois"}})
	if len(crewChoices) != 1 || crewChoices[0].Name != "C2 · Canoa de dois" {
		t.Fatalf("crew choices = %#v", crewChoices)
	}
	eventChoices := structuredCompetitionEvents([]dbgen.ListManagedStructuredCompetitionEventsRow{{
		ID: competitionID, Title: competitionTitle,
		StartsAt: pgtype.Timestamptz{Time: time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC), Valid: true},
	}}, time.UTC)
	if len(eventChoices) != 1 || eventChoices[0].Name != "05/09/2026 · Taça de Portugal" {
		t.Fatalf("event choices = %#v", eventChoices)
	}
	groupChoices := structuredVariationGroups(groups, time.UTC)
	if len(groupChoices) != 1 || groupChoices[0].Kind != "Tripulação" || groupChoices[0].Craft != craftCode || len(groupChoices[0].Members) != 2 || groupChoices[0].Period != "desde 17/08/2026 até à competição" {
		t.Fatalf("group choices = %#v", groupChoices)
	}
	targets := structuredVariationTargets(members, groups)
	if len(targets) != 2 || targets[0].Value != "ATHLETE:"+membershipID.String() || targets[1].Value != "GROUP:"+variationGroupID.String() {
		t.Fatalf("targets = %#v", targets)
	}

	audiences := []pages.StructuredTrainingAudience{{Weeks: []pages.StructuredTrainingWeek{{
		ID: "plan-1", Title: "M34", Sessions: []pages.StructuredTrainingSession{{
			Title: "Sessão híbrida", When: "18/08/2026 10:00", Segments: []pages.StructuredTrainingSegment{{
				ID: "segment-1", Position: 1, Modality: "WATER", Title: "Técnica", Blocks: []pages.StructuredTrainingBlock{{
					ID: "block-1", Position: 1, Purpose: "Principal", WaterSteps: []pages.StructuredWaterStep{{ID: "step-1", Position: 1, Name: "Séries"}},
					Exercises: []pages.StructuredGymExercise{{ID: "exercise-1", Position: 1, Name: "Remada"}},
				}},
			}},
		}},
	}}}}
	subjects := structuredVariationSubjects(audiences)
	if len(subjects) != 4 || subjects[0].Value != "SEGMENT:segment-1" || subjects[3].Value != "GYM_EXERCISE:exercise-1" {
		t.Fatalf("subjects = %#v", subjects)
	}
}

func TestTrainingVariationLabelsRemainReadable(t *testing.T) {
	operations := map[dbgen.TrainingVariationOperation]string{
		dbgen.TrainingVariationOperationOMIT:     "Omitir",
		dbgen.TrainingVariationOperationREPLACE:  "Substituir",
		dbgen.TrainingVariationOperationADD:      "Adicionar alternativa",
		dbgen.TrainingVariationOperationOVERRIDE: "Alterar campos",
	}
	for operation, want := range operations {
		if got := trainingVariationOperationLabel(operation); got != want {
			t.Errorf("operation %s = %q, want %q", operation, got, want)
		}
	}
	if got := trainingVariationPatchLabel([]byte(`{"intensity_code":"R7","duration_seconds":120}`)); got != "duração (s): 120 · intensidade: R7" {
		t.Fatalf("patch label = %q", got)
	}
	if got := trainingVariationPatchLabel([]byte(`not-json`)); got != "" {
		t.Fatalf("invalid patch label = %q", got)
	}
}

func TestCreateStructuredTrainingVariationPersistsBoundedAthleteOverride(t *testing.T) {
	userID, planID, membershipID, stepID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	store := &structuredTrainingStoreStub{weekOK: true}
	handler := StructuredTraining{Store: store, Location: time.UTC, System: System{}}
	values := url.Values{
		"plan_id": {planID.String()}, "target": {"ATHLETE:" + membershipID.String()},
		"subject": {"WATER_STEP:" + stepID.String()}, "operation": {"OVERRIDE"},
		"change_summary": {"Ritmo individual"}, "duration_seconds": {"120"}, "intensity_code": {"R7"},
	}
	response := performStructuredTrainingRequest(t, CurrentUser{ID: userID}, http.MethodPost, "/admin/treinos/estruturados/variacoes", values, "", "", handler.CreateVariation)
	if response.Code != http.StatusSeeOther || len(store.variationInputs) != 1 {
		t.Fatalf("response=%d variations=%#v", response.Code, store.variationInputs)
	}
	created := store.variationInputs[0]
	if created.PlanID != planID || created.TargetMembershipID == nil || *created.TargetMembershipID != membershipID || created.TargetGroupID != nil || created.SubjectID != stepID {
		t.Fatalf("created variation = %#v", created)
	}
	var patch map[string]any
	if err := json.Unmarshal(created.Patch, &patch); err != nil || patch["duration_seconds"] != float64(120) || patch["intensity_code"] != "R7" {
		t.Fatalf("patch = %#v, err = %v", patch, err)
	}
}

func TestCreateStructuredTrainingVariationRechecksWeekAuthorization(t *testing.T) {
	values := url.Values{
		"plan_id": {uuid.NewString()}, "target": {"ATHLETE:" + uuid.NewString()},
		"subject": {"BLOCK:" + uuid.NewString()}, "operation": {"OMIT"}, "change_summary": {"Omitir para este atleta"},
	}
	store := &structuredTrainingStoreStub{}
	handler := StructuredTraining{Store: store, Location: time.UTC, System: System{}}
	response := performStructuredTrainingRequest(t, CurrentUser{ID: uuid.New()}, http.MethodPost, "/admin/treinos/estruturados/variacoes", values, "", "", handler.CreateVariation)
	if response.Code != http.StatusForbidden || len(store.variationInputs) != 0 {
		t.Fatalf("response=%d variations=%#v", response.Code, store.variationInputs)
	}
}

func TestCreateStructuredTrainingVariationSupportsGroupOmissionsAndRejectsFailedWrites(t *testing.T) {
	planID, groupID, segmentID, actorID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	values := url.Values{"plan_id": {planID.String()}, "target": {"GROUP:" + groupID.String()}, "subject": {"SEGMENT:" + segmentID.String()}, "operation": {"OMIT"}, "change_summary": {"Retirar da tripulação"}}
	store := &structuredTrainingStoreStub{weekOK: true}
	response := performStructuredTrainingRequest(t, CurrentUser{ID: actorID, IsAdmin: true}, http.MethodPost, "/admin/treinos/estruturados/variacoes", values, "", "", (StructuredTraining{Store: store, Location: time.UTC}).CreateVariation)
	if response.Code != http.StatusSeeOther || len(store.variationInputs) != 1 || store.variationInputs[0].TargetGroupID == nil || *store.variationInputs[0].TargetGroupID != groupID || store.variationInputs[0].TargetMembershipID != nil || len(store.variationInputs[0].Patch) != 2 {
		t.Fatalf("response=%d variations=%#v", response.Code, store.variationInputs)
	}
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{name: "stale source", err: pgx.ErrNoRows, want: http.StatusForbidden},
		{name: "duplicate rule", err: &pgconn.PgError{Code: "23505"}, want: http.StatusForbidden},
		{name: "service failure", err: errors.New("database unavailable"), want: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failed := &structuredTrainingStoreStub{weekOK: true, createVariationErr: tc.err}
			response := performStructuredTrainingRequest(t, CurrentUser{ID: actorID, IsAdmin: true}, http.MethodPost, "/admin/treinos/estruturados/variacoes", values, "", "", (StructuredTraining{Store: failed, Location: time.UTC}).CreateVariation)
			if response.Code != tc.want || len(failed.variationInputs) != 1 {
				t.Fatalf("status=%d writes=%#v", response.Code, failed.variationInputs)
			}
		})
	}
}

func TestCreateStructuredWeekEnforcesGroupAuthorization(t *testing.T) {
	groupID, userID := uuid.New(), uuid.New()
	body := "group_id=" + groupID.String() + "&title=Microciclo+41&week_start=2026-08-17&planned_load_percentage=70"
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
			if tc.wantCreate && (store.created[0].PlannedLoadPercentage == nil || *store.created[0].PlannedLoadPercentage != 70) {
				t.Fatalf("planned load = %#v", store.created[0].PlannedLoadPercentage)
			}
		})
	}
}

func TestCreateStructuredWeekRejectsInvalidPlannedLoadPercentage(t *testing.T) {
	store := &structuredTrainingStoreStub{manageable: true}
	handler := StructuredTraining{Store: store, Location: time.UTC, System: System{}}
	groupID := uuid.New()
	response := performStructuredTrainingRequest(t, CurrentUser{ID: uuid.New()}, http.MethodPost, "/admin/treinos/estruturados/semanas", url.Values{
		"group_id": {groupID.String()}, "title": {"Microciclo 41"}, "week_start": {"2026-08-17"}, "planned_load_percentage": {"101"},
	}, "", "", handler.CreateWeek)
	if response.Code != http.StatusUnprocessableEntity || len(store.created) != 0 || !strings.Contains(response.Body.String(), "percentagem entre 0 e 100") {
		t.Fatalf("response=%d created=%d body=%q", response.Code, len(store.created), response.Body.String())
	}
}

func TestCreateStructuredWeekMapsSeasonAndPersistenceFailuresWithoutRedirecting(t *testing.T) {
	groupID, actorID := uuid.New(), uuid.New()
	values := url.Values{"group_id": {groupID.String()}, "title": {"Microciclo 41"}, "week_start": {"2026-08-17"}, "planned_load_percentage": {"70"}}
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{name: "week has no registered season", err: pgx.ErrNoRows, want: http.StatusUnprocessableEntity},
		{name: "week write failure", err: errors.New("database unavailable"), want: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &structuredTrainingStoreStub{manageable: true, createWeekErr: tc.err}
			response := performStructuredTrainingRequest(t, CurrentUser{ID: actorID, IsAdmin: true}, http.MethodPost, "/admin/treinos/estruturados/semanas", values, "", "", (StructuredTraining{Store: store, Location: time.UTC}).CreateWeek)
			if response.Code != tc.want || len(store.created) != 1 {
				t.Fatalf("status=%d created=%#v", response.Code, store.created)
			}
		})
	}
}

func TestUpdateStructuredWeekLoadRequiresScopedCoachAndValidPercentage(t *testing.T) {
	weekID, userID := uuid.New(), uuid.New()
	for _, tc := range []struct {
		name       string
		weekOK     bool
		value      string
		wantStatus int
		wantLoad   *int16
	}{
		{name: "scoped coach updates", weekOK: true, value: "65", wantStatus: http.StatusSeeOther, wantLoad: int16Pointer(65)},
		{name: "unrelated coach denied", value: "65", wantStatus: http.StatusForbidden},
		{name: "invalid percentage", weekOK: true, value: "101", wantStatus: http.StatusUnprocessableEntity},
		{name: "blank clears percentage", weekOK: true, wantStatus: http.StatusSeeOther},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &structuredTrainingStoreStub{weekOK: tc.weekOK}
			handler := StructuredTraining{Store: store, Location: time.UTC, System: System{}}
			response := performStructuredTrainingRequest(t, CurrentUser{ID: userID}, http.MethodPost, "/admin/treinos/estruturados/semanas/"+weekID.String()+"/carga", url.Values{"planned_load_percentage": {tc.value}}, "id", weekID.String(), handler.UpdateWeekLoad)
			if response.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, tc.wantStatus)
			}
			if tc.wantStatus == http.StatusSeeOther {
				if len(store.updatedLoads) != 1 || store.updatedLoads[0].PlanID != weekID || !reflect.DeepEqual(store.updatedLoads[0].PlannedLoadPercentage, tc.wantLoad) {
					t.Fatalf("updated loads = %#v, want %#v", store.updatedLoads, tc.wantLoad)
				}
			} else if len(store.updatedLoads) != 0 {
				t.Fatalf("updated loads = %#v", store.updatedLoads)
			}
		})
	}
}

func TestUpdateStructuredWeekLoadMapsPersistenceFailure(t *testing.T) {
	weekID := uuid.New()
	store := &structuredTrainingStoreStub{weekOK: true, updateWeekLoadErr: errors.New("database unavailable")}
	response := performStructuredTrainingRequest(t, CurrentUser{ID: uuid.New(), IsAdmin: true}, http.MethodPost, "/admin/treinos/estruturados/semanas/"+weekID.String()+"/carga", url.Values{"planned_load_percentage": {"65"}}, "id", weekID.String(), (StructuredTraining{Store: store, Location: time.UTC}).UpdateWeekLoad)
	if response.Code != http.StatusInternalServerError || len(store.updatedLoads) != 1 {
		t.Fatalf("status=%d updates=%#v", response.Code, store.updatedLoads)
	}
}

func int16Pointer(value int16) *int16 { return &value }

func TestStructuredTrainingAuthoringHandlersPersistValidHierarchy(t *testing.T) {
	userID, programmeID, membershipID, groupID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
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
		"return_to":   {structuredPlannerURL(groupID.String(), planID.String(), "")},
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
	if location := response.Header().Get("Location"); !strings.Contains(location, "group_id="+groupID.String()) || !strings.Contains(location, "week_id="+planID.String()) || !strings.Contains(location, "session_id=") {
		t.Fatalf("session redirect lost planner context: %q", location)
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

	returnTo := structuredPlannerURL(uuid.New().String(), uuid.New().String(), uuid.New().String())
	exerciseValues := url.Values{"exercise_name": {"Prancha"}, "duration_seconds": {"45"}, "resistance_kind": {"BODY_WEIGHT"}, "execution_intent": {"ISOMETRIC"}, "return_to": {returnTo}}
	response = performStructuredTrainingRequest(t, user, http.MethodPost, "/admin/treinos/estruturados/blocos/"+blockID.String()+"/exercicios", exerciseValues, "id", blockID.String(), handler.CreateGymExercise)
	if response.Code != http.StatusSeeOther || len(store.gymExercises) != 1 || store.gymExercises[0].BlockID != blockID || store.gymExercises[0].DurationSeconds == nil || *store.gymExercises[0].DurationSeconds != 45 {
		t.Fatalf("gym exercise response=%d inputs=%#v", response.Code, store.gymExercises)
	}
	if got := response.Header().Get("Location"); got != returnTo {
		t.Fatalf("gym exercise return location = %q, want %q", got, returnTo)
	}

	exerciseID := uuid.New()
	response = performStructuredTrainingRequest(t, user, http.MethodPost, "/admin/treinos/estruturados/exercicios/"+exerciseID.String()+"/mover", url.Values{"direction": {"up"}}, "id", exerciseID.String(), handler.MoveGymExercise)
	if response.Code != http.StatusSeeOther || len(store.movedExercises) != 1 || store.movedExercises[0].Direction != -1 {
		t.Fatalf("move exercise response=%d inputs=%#v", response.Code, store.movedExercises)
	}
}

func TestStructuredTrainingGroupAndSessionSurfacePersistenceOutcomes(t *testing.T) {
	programmeID, membershipID, planID, actorID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	user := CurrentUser{ID: actorID, CoachProgrammeIDs: map[uuid.UUID]bool{programmeID: true}}
	groupValues := url.Values{"name": {"Cadetes"}, "programme_id": {programmeID.String()}, "membership_id": {membershipID.String()}}
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{name: "membership moved out of scope", err: errStructuredTrainingMembershipScope, want: http.StatusUnprocessableEntity},
		{name: "group service failure", err: errors.New("database unavailable"), want: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &structuredTrainingStoreStub{createGroupErr: tc.err}
			response := performStructuredTrainingRequest(t, user, http.MethodPost, "/admin/treinos/estruturados/grupos", groupValues, "", "", (StructuredTraining{Store: store, Location: time.UTC}).CreateGroup)
			if response.Code != tc.want {
				t.Fatalf("group status=%d, want %d", response.Code, tc.want)
			}
		})
	}

	sessionValues := url.Values{"plan_id": {planID.String()}, "title": {"Ginásio + água"}, "description": {"Sessão híbrida"}, "starts_at": {"2026-08-18T17:00"}, "ends_at": {"2026-08-18T19:00"}, "entry_kind": {"TRAINING"}}
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{name: "outside selected week", err: pgx.ErrNoRows, want: http.StatusUnprocessableEntity},
		{name: "session service failure", err: errors.New("database unavailable"), want: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &structuredTrainingStoreStub{weekOK: true, createSessionErr: tc.err}
			response := performStructuredTrainingRequest(t, CurrentUser{ID: actorID, IsAdmin: true}, http.MethodPost, "/admin/treinos/estruturados/sessoes", sessionValues, "", "", (StructuredTraining{Store: store, Location: time.UTC}).CreateSession)
			if response.Code != tc.want {
				t.Fatalf("session status=%d, want %d", response.Code, tc.want)
			}
		})
	}
}

func TestStructuredTrainingSegmentAndBlockMapLookupAndPersistenceFailures(t *testing.T) {
	planID, sessionID, segmentID, actorID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	segmentValues := url.Values{"modality": {"WATER"}, "title": {"Técnica"}, "purpose": {"MAIN"}, "block_title": {"Remada"}, "instructions": {"Manter a técnica"}}
	blockValues := url.Values{"purpose": {"MAIN"}, "title": {"Remada"}, "instructions": {"Manter a técnica"}}
	for _, tc := range []struct {
		name    string
		handler func(StructuredTraining) http.HandlerFunc
		values  url.Values
		pathID  uuid.UUID
		mutate  func(*structuredTrainingStoreStub)
		want    int
	}{
		{name: "segment source removed", handler: func(h StructuredTraining) http.HandlerFunc { return h.CreateSegment }, values: segmentValues, pathID: sessionID, mutate: func(s *structuredTrainingStoreStub) { s.getSessionPlanErr = pgx.ErrNoRows }, want: http.StatusNotFound},
		{name: "segment source failure", handler: func(h StructuredTraining) http.HandlerFunc { return h.CreateSegment }, values: segmentValues, pathID: sessionID, mutate: func(s *structuredTrainingStoreStub) { s.getSessionPlanErr = errors.New("database unavailable") }, want: http.StatusInternalServerError},
		{name: "segment stale write", handler: func(h StructuredTraining) http.HandlerFunc { return h.CreateSegment }, values: segmentValues, pathID: sessionID, mutate: func(s *structuredTrainingStoreStub) { s.createSegmentErr = pgx.ErrNoRows }, want: http.StatusUnprocessableEntity},
		{name: "segment write failure", handler: func(h StructuredTraining) http.HandlerFunc { return h.CreateSegment }, values: segmentValues, pathID: sessionID, mutate: func(s *structuredTrainingStoreStub) { s.createSegmentErr = errors.New("database unavailable") }, want: http.StatusInternalServerError},
		{name: "block source removed", handler: func(h StructuredTraining) http.HandlerFunc { return h.CreateBlock }, values: blockValues, pathID: segmentID, mutate: func(s *structuredTrainingStoreStub) { s.getSegmentPlanErr = pgx.ErrNoRows }, want: http.StatusNotFound},
		{name: "block source failure", handler: func(h StructuredTraining) http.HandlerFunc { return h.CreateBlock }, values: blockValues, pathID: segmentID, mutate: func(s *structuredTrainingStoreStub) { s.getSegmentPlanErr = errors.New("database unavailable") }, want: http.StatusInternalServerError},
		{name: "block write failure", handler: func(h StructuredTraining) http.HandlerFunc { return h.CreateBlock }, values: blockValues, pathID: segmentID, mutate: func(s *structuredTrainingStoreStub) { s.createBlockErr = errors.New("database unavailable") }, want: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &structuredTrainingStoreStub{planID: planID, weekOK: true}
			tc.mutate(store)
			response := performStructuredTrainingRequest(t, CurrentUser{ID: actorID, IsAdmin: true}, http.MethodPost, "/admin/treinos/estruturados", tc.values, "id", tc.pathID.String(), tc.handler(StructuredTraining{Store: store, Location: time.UTC}))
			if response.Code != tc.want {
				t.Fatalf("status=%d want=%d", response.Code, tc.want)
			}
		})
	}
}

func TestMoveGymExerciseMapsParentAndPersistenceFailures(t *testing.T) {
	exerciseID, planID := uuid.New(), uuid.New()
	for _, tc := range []struct {
		name   string
		mutate func(*structuredTrainingStoreStub)
		want   int
	}{
		{name: "exercise removed", mutate: func(s *structuredTrainingStoreStub) { s.getGymExercisePlanErr = pgx.ErrNoRows }, want: http.StatusNotFound},
		{name: "exercise read failure", mutate: func(s *structuredTrainingStoreStub) { s.getGymExercisePlanErr = errors.New("database unavailable") }, want: http.StatusInternalServerError},
		{name: "move failure", mutate: func(s *structuredTrainingStoreStub) { s.moveGymErr = errors.New("database unavailable") }, want: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &structuredTrainingStoreStub{planID: planID, weekOK: true}
			tc.mutate(store)
			response := performStructuredTrainingRequest(t, CurrentUser{ID: uuid.New(), IsAdmin: true}, http.MethodPost, "/admin/treinos/estruturados/exercicios/"+exerciseID.String()+"/mover", url.Values{"direction": {"down"}}, "id", exerciseID.String(), (StructuredTraining{Store: store, Location: time.UTC}).MoveGymExercise)
			if response.Code != tc.want {
				t.Fatalf("status=%d want=%d", response.Code, tc.want)
			}
		})
	}
}

func TestStructuredWaterAuthoringMutationsMapStaleAndServiceFailures(t *testing.T) {
	blockID, planID, profileID, actorID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	stepValues := url.Values{"step_kind": {"EFFORT"}, "step_name": {"500 m"}, "duration_seconds": {"120"}, "duration_certainty": {"EXACT"}, "step_instructions": {"Ritmo controlado"}}
	profileValues := url.Values{"name": {"Perfil do clube"}, "craft": {"KAYAK"}, "notes": {"R5 por confirmar"}}
	zoneValues := url.Values{"code": {"R7"}, "label": {"Ritmo de prova"}, "meaning": {"Ritmo sustentável para a duração prescrita"}}
	for _, tc := range []struct {
		name    string
		err     error
		handler func(StructuredTraining) http.HandlerFunc
		values  url.Values
		pathKey string
		pathID  uuid.UUID
		want    int
	}{
		{name: "stale water step", err: pgx.ErrNoRows, handler: func(h StructuredTraining) http.HandlerFunc { return h.CreateWaterWorkStep }, values: stepValues, pathKey: "id", pathID: blockID, want: http.StatusForbidden},
		{name: "water step service failure", err: errors.New("database unavailable"), handler: func(h StructuredTraining) http.HandlerFunc { return h.CreateWaterWorkStep }, values: stepValues, pathKey: "id", pathID: blockID, want: http.StatusInternalServerError},
		{name: "profile service failure", err: errors.New("database unavailable"), handler: func(h StructuredTraining) http.HandlerFunc { return h.CreateWaterIntensityProfile }, values: profileValues, want: http.StatusInternalServerError},
		{name: "stale intensity zone", err: &pgconn.PgError{Code: "23514"}, handler: func(h StructuredTraining) http.HandlerFunc { return h.CreateWaterIntensityZone }, values: zoneValues, pathKey: "id", pathID: profileID, want: http.StatusForbidden},
		{name: "zone service failure", err: errors.New("database unavailable"), handler: func(h StructuredTraining) http.HandlerFunc { return h.CreateWaterIntensityZone }, values: zoneValues, pathKey: "id", pathID: profileID, want: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &structuredTrainingStoreStub{planID: planID, weekOK: true, createWaterStepErr: tc.err, createWaterProfileErr: tc.err, createWaterZoneErr: tc.err}
			h := StructuredTraining{Store: store, Location: time.UTC}
			response := performStructuredTrainingRequest(t, CurrentUser{ID: actorID, IsAdmin: true}, http.MethodPost, "/admin/treinos/estruturados", tc.values, tc.pathKey, tc.pathID.String(), tc.handler(h))
			if response.Code != tc.want {
				t.Fatalf("status=%d, want %d", response.Code, tc.want)
			}
		})
	}
}

func TestStructuredGymAndWaterAuthoringMapsParentLookupFailures(t *testing.T) {
	segmentID, blockID, planID, actorID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	gymBlock := url.Values{"purpose": {"MAIN"}, "title": {"Força"}, "instructions": {"Controlado"}, "structure": {"STRAIGHT_SETS"}, "objective": {"TECHNIQUE"}, "rounds": {"3"}, "exercise_name": {"Remada"}, "sets": {"3"}, "repetitions": {"8"}, "resistance_kind": {"BODY_WEIGHT"}, "execution_intent": {"CONTROLLED"}}
	waterBlock := url.Values{"purpose": {"MAIN"}, "title": {"Água"}, "instructions": {"Ritmo controlado"}, "method": {"INTERVALS"}, "step_kind": {"EFFORT"}, "step_name": {"500 m"}, "duration_seconds": {"120"}, "duration_certainty": {"EXACT"}}
	step := url.Values{"step_kind": {"EFFORT"}, "step_name": {"500 m"}, "duration_seconds": {"120"}, "duration_certainty": {"EXACT"}}
	for _, tc := range []struct {
		name    string
		handler func(StructuredTraining) http.HandlerFunc
		values  url.Values
		id      uuid.UUID
		mutate  func(*structuredTrainingStoreStub, error)
	}{
		{name: "gym block", handler: func(h StructuredTraining) http.HandlerFunc { return h.CreateGymBlock }, values: gymBlock, id: segmentID, mutate: func(s *structuredTrainingStoreStub, err error) { s.getSegmentPlanErr = err }},
		{name: "water block", handler: func(h StructuredTraining) http.HandlerFunc { return h.CreateWaterBlock }, values: waterBlock, id: segmentID, mutate: func(s *structuredTrainingStoreStub, err error) { s.getSegmentPlanErr = err }},
		{name: "gym exercise", handler: func(h StructuredTraining) http.HandlerFunc { return h.CreateGymExercise }, values: url.Values{"exercise_name": {"Prancha"}, "duration_seconds": {"45"}, "resistance_kind": {"BODY_WEIGHT"}, "execution_intent": {"ISOMETRIC"}}, id: blockID, mutate: func(s *structuredTrainingStoreStub, err error) { s.getBlockPlanErr = err }},
		{name: "water step", handler: func(h StructuredTraining) http.HandlerFunc { return h.CreateWaterWorkStep }, values: step, id: blockID, mutate: func(s *structuredTrainingStoreStub, err error) { s.getBlockPlanErr = err }},
	} {
		for _, failure := range []struct {
			name string
			err  error
			want int
		}{{"removed", pgx.ErrNoRows, http.StatusNotFound}, {"unavailable", errors.New("database unavailable"), http.StatusInternalServerError}} {
			t.Run(tc.name+" "+failure.name, func(t *testing.T) {
				store := &structuredTrainingStoreStub{planID: planID, weekOK: true}
				tc.mutate(store, failure.err)
				response := performStructuredTrainingRequest(t, CurrentUser{ID: actorID, IsAdmin: true}, http.MethodPost, "/admin/treinos/estruturados", tc.values, "id", tc.id.String(), tc.handler(StructuredTraining{Store: store, Location: time.UTC}))
				if response.Code != failure.want {
					t.Fatalf("status=%d want=%d", response.Code, failure.want)
				}
			})
		}
	}
}

func TestStructuredGymAuthoringMutationsMapStaleAndServiceFailures(t *testing.T) {
	segmentID, blockID, planID, actorID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	blockValues := url.Values{
		"purpose": {"WARM_UP"}, "title": {"Mobilidade e força"}, "instructions": {"Duas voltas controladas"},
		"structure": {"SUPERSET"}, "objective": {"ACTIVATION"}, "rounds": {"3"}, "round_recovery_seconds": {"120"},
		"exercise_name": {"Supino + elevação"}, "sets": {"3"}, "repetitions": {"10"}, "recovery_seconds": {"60"},
		"resistance_kind": {"PERCENT_1RM"}, "resistance_value": {"75"}, "execution_intent": {"EXPLOSIVE"}, "tempo": {"2-0-X-1"},
	}
	exerciseValues := url.Values{"exercise_name": {"Prancha"}, "duration_seconds": {"45"}, "resistance_kind": {"BODY_WEIGHT"}, "execution_intent": {"ISOMETRIC"}}
	for _, tc := range []struct {
		name   string
		block  bool
		err    error
		values url.Values
		pathID uuid.UUID
		want   int
	}{
		{name: "stale gym segment", block: true, err: pgx.ErrNoRows, values: blockValues, pathID: segmentID, want: http.StatusForbidden},
		{name: "gym block service failure", block: true, err: errors.New("database unavailable"), values: blockValues, pathID: segmentID, want: http.StatusInternalServerError},
		{name: "gym exercise service failure", err: errors.New("database unavailable"), values: exerciseValues, pathID: blockID, want: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &structuredTrainingStoreStub{planID: planID, weekOK: true, createGymBlockErr: tc.err, createGymExerciseErr: tc.err}
			h := StructuredTraining{Store: store, Location: time.UTC}
			handler := h.CreateGymExercise
			if tc.block {
				handler = h.CreateGymBlock
			}
			response := performStructuredTrainingRequest(t, CurrentUser{ID: actorID, IsAdmin: true}, http.MethodPost, "/admin/treinos/estruturados", tc.values, "id", tc.pathID.String(), handler)
			if response.Code != tc.want {
				t.Fatalf("status=%d, want %d", response.Code, tc.want)
			}
		})
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

	returnTo := structuredPlannerURL(uuid.New().String(), uuid.New().String(), uuid.New().String())
	values := url.Values{"source_kind": {"SEGMENT"}, "source_id": {sourceID.String()}, "name": {"Mobilidade habitual"}, "visibility": {"SHARED"}, "method": {"Ativação"}, "tags": {"ginásio, aquecimento, Ginásio"}, "return_to": {returnTo}}
	response := performStructuredTrainingRequest(t, user, http.MethodPost, "/admin/treinos/estruturados/rotinas", values, "", "", handler.CreateRoutine)
	if response.Code != http.StatusSeeOther || len(store.createdRoutines) != 1 {
		t.Fatalf("create routine response=%d routines=%#v", response.Code, store.createdRoutines)
	}
	created := store.createdRoutines[0]
	if created.ProgrammeID == nil || *created.ProgrammeID != programmeID || created.TeamID != nil || len(created.Tags) != 2 || created.Snapshot == nil {
		t.Fatalf("created routine scope/tags = %#v", created)
	}
	if got := response.Header().Get("Location"); got != returnTo {
		t.Fatalf("create routine return location = %q, want %q", got, returnTo)
	}

	store.visibleRoutine = dbgen.TrainingRoutine{ID: uuid.New(), Kind: dbgen.TrainingRoutineKindBLOCK, Snapshot: []byte(`{"purpose":"MAIN","instructions":"3x5"}`), UpdatedAt: sourceUpdatedAt}
	response = performStructuredTrainingRequest(t, user, http.MethodPost, "/admin/treinos/estruturados/rotinas/"+store.visibleRoutine.ID.String()+"/inserir", url.Values{"target_id": {targetID.String()}, "return_to": {returnTo}}, "id", store.visibleRoutine.ID.String(), handler.InsertRoutine)
	if response.Code != http.StatusSeeOther || len(store.insertedRoutines) != 1 || store.insertedRoutines[0].TargetID != targetID || store.insertedRoutines[0].ActorID != userID {
		t.Fatalf("insert routine response=%d inputs=%#v", response.Code, store.insertedRoutines)
	}
	if got := response.Header().Get("Location"); got != returnTo {
		t.Fatalf("insert routine return location = %q, want %q", got, returnTo)
	}
}

func TestCreateStructuredRoutineFailsClosedForUnavailableSourceScopeAndWriteFailure(t *testing.T) {
	userID, planID, sourceID := uuid.New(), uuid.New(), uuid.New()
	modality := dbgen.TrainingSegmentModalityGYM
	values := url.Values{"source_kind": {"SEGMENT"}, "source_id": {sourceID.String()}, "name": {"Mobilidade habitual"}, "visibility": {"SHARED"}, "method": {"Ativação"}, "tags": {"ginásio"}}
	base := func() structuredTrainingStoreStub {
		return structuredTrainingStoreStub{weekOK: true, routineSource: StructuredRoutineSource{PlanID: planID, Modality: &modality, Snapshot: []byte(`{"title":"Mobilidade"}`)}}
	}
	for _, tc := range []struct {
		name   string
		mutate func(*structuredTrainingStoreStub)
		want   int
	}{
		{name: "source removed", mutate: func(s *structuredTrainingStoreStub) { s.routineSourceErr = pgx.ErrNoRows }, want: http.StatusNotFound},
		{name: "source read failure", mutate: func(s *structuredTrainingStoreStub) { s.routineSourceErr = errors.New("database unavailable") }, want: http.StatusInternalServerError},
		{name: "shared source without club scope", mutate: func(s *structuredTrainingStoreStub) {}, want: http.StatusForbidden},
		{name: "routine write failure", mutate: func(s *structuredTrainingStoreStub) {
			programmeID := uuid.New()
			s.routineSource.ProgrammeID = &programmeID
			s.createRoutineErr = errors.New("database unavailable")
		}, want: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := base()
			tc.mutate(&store)
			response := performStructuredTrainingRequest(t, CurrentUser{ID: userID, IsAdmin: true}, http.MethodPost, "/admin/treinos/estruturados/rotinas", values, "", "", (StructuredTraining{Store: &store, Location: time.UTC}).CreateRoutine)
			if response.Code != tc.want || len(store.createdRoutines) > 0 && tc.want != http.StatusInternalServerError {
				t.Fatalf("status=%d routines=%#v", response.Code, store.createdRoutines)
			}
		})
	}
}

func TestInsertStructuredRoutineFailsClosedForInvalidScheduleKindAndScope(t *testing.T) {
	routineID, targetID, userID, planID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	request := func(values url.Values) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/admin/treinos/estruturados/rotinas/"+routineID.String()+"/inserir", strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.SetPathValue("id", routineID.String())
		return r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: userID, IsAdmin: true}))
	}

	t.Run("session needs valid schedule", func(t *testing.T) {
		store := &structuredTrainingStoreStub{planID: planID, weekOK: true, visibleRoutine: dbgen.TrainingRoutine{ID: routineID, Kind: dbgen.TrainingRoutineKindSESSION}}
		w := httptest.NewRecorder()
		(StructuredTraining{Store: store, Location: time.UTC}).InsertRoutine(w, request(url.Values{"target_id": {targetID.String()}, "starts_at": {"not-a-date"}}))
		if w.Code != http.StatusForbidden || len(store.insertedRoutines) != 0 {
			t.Fatalf("response=%d inserted=%#v", w.Code, store.insertedRoutines)
		}
	})

	t.Run("unknown kind", func(t *testing.T) {
		store := &structuredTrainingStoreStub{planID: planID, weekOK: true, visibleRoutine: dbgen.TrainingRoutine{ID: routineID, Kind: dbgen.TrainingRoutineKind("UNKNOWN")}}
		w := httptest.NewRecorder()
		(StructuredTraining{Store: store, Location: time.UTC}).InsertRoutine(w, request(url.Values{"target_id": {targetID.String()}}))
		if w.Code != http.StatusNotFound || len(store.insertedRoutines) != 0 {
			t.Fatalf("response=%d inserted=%#v", w.Code, store.insertedRoutines)
		}
	})

	t.Run("unmanaged target", func(t *testing.T) {
		store := &structuredTrainingStoreStub{planID: planID, weekOK: false, visibleRoutine: dbgen.TrainingRoutine{ID: routineID, Kind: dbgen.TrainingRoutineKindBLOCK}}
		w := httptest.NewRecorder()
		(StructuredTraining{Store: store, Location: time.UTC}).InsertRoutine(w, request(url.Values{"target_id": {targetID.String()}}))
		if w.Code != http.StatusForbidden || len(store.insertedRoutines) != 0 {
			t.Fatalf("response=%d inserted=%#v", w.Code, store.insertedRoutines)
		}
	})
}

func TestInsertStructuredRoutineMapsVisibilityTargetAndWriteFailures(t *testing.T) {
	routineID, targetID, planID, actorID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	requestValues := url.Values{"target_id": {targetID.String()}}
	for _, tc := range []struct {
		name   string
		mutate func(*structuredTrainingStoreStub)
		want   int
	}{
		{name: "routine no longer visible", mutate: func(s *structuredTrainingStoreStub) { s.visibleRoutineErr = pgx.ErrNoRows }, want: http.StatusNotFound},
		{name: "routine lookup failure", mutate: func(s *structuredTrainingStoreStub) { s.visibleRoutineErr = errors.New("database unavailable") }, want: http.StatusInternalServerError},
		{name: "target removed", mutate: func(s *structuredTrainingStoreStub) { s.getSegmentPlanErr = pgx.ErrNoRows }, want: http.StatusNotFound},
		{name: "target lookup failure", mutate: func(s *structuredTrainingStoreStub) { s.getSegmentPlanErr = errors.New("database unavailable") }, want: http.StatusInternalServerError},
		{name: "stale insertion", mutate: func(s *structuredTrainingStoreStub) { s.insertRoutineErr = pgx.ErrNoRows }, want: http.StatusForbidden},
		{name: "insertion failure", mutate: func(s *structuredTrainingStoreStub) { s.insertRoutineErr = errors.New("database unavailable") }, want: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &structuredTrainingStoreStub{planID: planID, weekOK: true, visibleRoutine: dbgen.TrainingRoutine{ID: routineID, Kind: dbgen.TrainingRoutineKindBLOCK}}
			tc.mutate(store)
			response := performStructuredTrainingRequest(t, CurrentUser{ID: actorID, IsAdmin: true}, http.MethodPost, "/admin/treinos/estruturados/rotinas/"+routineID.String()+"/inserir", requestValues, "id", routineID.String(), (StructuredTraining{Store: store, Location: time.UTC}).InsertRoutine)
			if response.Code != tc.want || (tc.want != http.StatusSeeOther && len(store.insertedRoutines) > 1) {
				t.Fatalf("status=%d inserts=%#v", response.Code, store.insertedRoutines)
			}
		})
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

func TestStructuredPlannerSelectionKeepsAValidDeepLinkedContext(t *testing.T) {
	audiences := []pages.StructuredTrainingAudience{
		{GroupID: "group-a", GroupName: "Cadetes", Weeks: []pages.StructuredTrainingWeek{{ID: "week-a", Sessions: []pages.StructuredTrainingSession{{ID: "session-a"}}}}},
		{GroupID: "group-b", GroupName: "Seniores", Weeks: []pages.StructuredTrainingWeek{{ID: "week-b", Sessions: []pages.StructuredTrainingSession{{ID: "session-b"}, {ID: "session-c"}}}}},
	}

	if groupID, weekID, sessionID := structuredPlannerSelection(audiences, "group-b", "week-b", "session-c"); groupID != "group-b" || weekID != "week-b" || sessionID != "session-c" {
		t.Fatalf("deep-linked selection = %q, %q, %q", groupID, weekID, sessionID)
	}
	if groupID, weekID, sessionID := structuredPlannerSelection(audiences, "group-b", "week-a", "session-a"); groupID != "group-b" || weekID != "week-b" || sessionID != "session-b" {
		t.Fatalf("cross-group selection should fall back within its selected group, got %q, %q, %q", groupID, weekID, sessionID)
	}
	if groupID, weekID, sessionID := structuredPlannerSelection(audiences, "unknown", "unknown", "unknown"); groupID != "group-a" || weekID != "week-a" || sessionID != "session-a" {
		t.Fatalf("invalid selection should fall back to the first available context, got %q, %q, %q", groupID, weekID, sessionID)
	}
}

func TestStructuredPlannerContextAudienceBoundsRenderedWorkspace(t *testing.T) {
	audiences := []pages.StructuredTrainingAudience{
		{GroupID: "group-a", Weeks: []pages.StructuredTrainingWeek{{ID: "week-a", Sessions: []pages.StructuredTrainingSession{{ID: "session-a"}, {ID: "session-b"}}}}},
		{GroupID: "group-b", Weeks: []pages.StructuredTrainingWeek{{ID: "week-b", Sessions: []pages.StructuredTrainingSession{{ID: "session-c"}}}}},
	}

	got := structuredPlannerContextAudience(audiences, "group-a", "week-a", "session-b")
	if len(got) != 1 || got[0].GroupID != "group-a" || len(got[0].Weeks) != 1 || got[0].Weeks[0].ID != "week-a" || len(got[0].Weeks[0].Sessions) != 1 || got[0].Weeks[0].Sessions[0].ID != "session-b" {
		t.Fatalf("bounded context = %#v", got)
	}
	if got := structuredPlannerContextAudience(audiences, "missing", "week-a", "session-a"); got != nil {
		t.Fatalf("missing context = %#v, want nil", got)
	}
}

func TestStructuredPlannerReturnRejectsForeignOrMalformedContext(t *testing.T) {
	groupID, weekID, sessionID := uuid.New(), uuid.New(), uuid.New()
	valid := structuredPlannerURL(groupID.String(), weekID.String(), sessionID.String())
	if got := structuredPlannerReturn(valid); got != valid {
		t.Fatalf("valid planner return = %q, want %q", got, valid)
	}
	if got := structuredPlannerWeekReturn(valid, weekID.String()); got != valid {
		t.Fatalf("week return = %q, want %q", got, valid)
	}
	if got := structuredPlannerSessionReturn(valid, weekID.String(), uuid.New().String()); !strings.Contains(got, "session_id=") {
		t.Fatalf("session return did not retain the selected context: %q", got)
	}
	for _, raw := range []string{"https://example.test/admin/treinos/estruturados", "/admin/treinos/estruturados?group_id=" + groupID.String(), "/admin/treinos/estruturados?group_id=" + groupID.String() + "&unknown=value#training-plan", "/admin/treinos/estruturados?group_id=invalid#training-plan"} {
		if got := structuredPlannerReturn(raw); got != "" {
			t.Fatalf("unsafe return %q normalized to %q", raw, got)
		}
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
	for objective, want := range map[string]string{"MOBILITY": "Mobilidade", "ACTIVATION": "Ativação", "MAX_STRENGTH_HYPERTROPHY": "Força máxima e hipertrofia", "MAX_STRENGTH_NEURAL": "Força máxima neural", "EXPLOSIVE_STRENGTH": "Força explosiva", "STRENGTH_ENDURANCE": "Força-resistência", "TECHNIQUE": "Técnica", "CORE": "Core", "OTHER": "Personalizado"} {
		if got := structuredTrainingObjectiveName(objective); got != want {
			t.Errorf("objective %s = %q, want %q", objective, got, want)
		}
	}
	if got := formatTrainingDuration(3665); got != "61 min 5 s" {
		t.Fatalf("duration = %q", got)
	}
	if optionalIntLabel(nil) != "—" || waterMethodLabel("INTERVALS") != "Intervalos" || trainingMeasureCertaintyLabel("ESTIMATED") != "estimado" || paddlingCraftLabel("CANOE") != "Canoa" {
		t.Fatal("water display helpers did not preserve configured labels")
	}
}

func TestStructuredPublicationHelpersLocateWeeksAndDetectOnlyTopPriorityConflicts(t *testing.T) {
	planID, otherPlanID, membershipID, subjectID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	audiences := []pages.StructuredTrainingAudience{{GroupName: "Competição", Weeks: []pages.StructuredTrainingWeek{{ID: planID.String(), Title: "Semana 1"}}}}
	audience, week, found := structuredPublicationWeek(audiences, planID)
	if !found || audience.GroupName != "Competição" || week.Title != "Semana 1" {
		t.Fatalf("publication week = %#v %#v found=%t", audience, week, found)
	}
	if _, _, found := structuredPublicationWeek(audiences, otherPlanID); found {
		t.Fatal("foreign plan must not resolve to a publication week")
	}
	rows := []dbgen.ListTrainingVariationMatchesForManagerRow{
		{PlanID: planID, MembershipID: &membershipID, SubjectKind: dbgen.TrainingVariationSubjectKindSEGMENT, SubjectID: subjectID, Priority: 1},
		{PlanID: planID, MembershipID: &membershipID, SubjectKind: dbgen.TrainingVariationSubjectKindSEGMENT, SubjectID: subjectID, Priority: 2},
		{PlanID: planID, MembershipID: &membershipID, SubjectKind: dbgen.TrainingVariationSubjectKindSEGMENT, SubjectID: subjectID, Priority: 2},
		{PlanID: otherPlanID, MembershipID: &membershipID, SubjectKind: dbgen.TrainingVariationSubjectKindSEGMENT, SubjectID: subjectID, Priority: 2},
	}
	if got := structuredPublicationConflictCount(rows, planID); got != 1 {
		t.Fatalf("conflicts = %d", got)
	}
}

func TestStructuredPublicationStatesCalculateRecipientChangeCounts(t *testing.T) {
	planID, sessionID, membershipID, athleteID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	updated := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	week := pages.StructuredTrainingWeek{ID: planID.String(), Title: "Semana 1", Sessions: []pages.StructuredTrainingSession{{ID: sessionID.String(), Title: "Água"}}}
	store := &structuredTrainingStoreStub{publicationMembers: []dbgen.ListStructuredTrainingPublicationMembersRow{{SessionID: sessionID, MembershipID: membershipID, AthleteUserID: athleteID}}}
	h := StructuredTraining{Store: store, Location: time.UTC}
	states := h.structuredPublicationStates(context.Background(), []pages.StructuredTrainingAudience{{GroupName: "Competição", Weeks: []pages.StructuredTrainingWeek{week}}}, []dbgen.ListManagedTrainingPublicationStatesRow{{ID: planID, Title: "Semana 1", SourceUpdatedAt: pgtype.Timestamptz{Time: updated, Valid: true}}}, nil)
	if len(states) != 1 || states[0].Status != "Rascunho nunca publicado" || states[0].AthleteCount != 1 || states[0].PrescriptionCount != 1 || states[0].AddedCount != 1 {
		t.Fatalf("states = %#v", states)
	}
}

func TestStructuredPublicationStatesDistinguishesAddedChangedRemovedAndUnchangedSnapshots(t *testing.T) {
	planID, sessionID := uuid.New(), uuid.New()
	firstMembership, secondMembership, thirdMembership, removedMembership := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	firstAthlete, secondAthlete, thirdAthlete := uuid.New(), uuid.New(), uuid.New()
	updated := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	week := pages.StructuredTrainingWeek{ID: planID.String(), Title: "Semana 1", Sessions: []pages.StructuredTrainingSession{{ID: sessionID.String(), Title: "Água"}}}
	recipients := []dbgen.ListStructuredTrainingPublicationMembersRow{
		{SessionID: sessionID, MembershipID: firstMembership, AthleteUserID: firstAthlete},
		{SessionID: sessionID, MembershipID: secondMembership, AthleteUserID: secondAthlete},
		{SessionID: sessionID, MembershipID: thirdMembership, AthleteUserID: thirdAthlete},
	}
	candidates, err := buildStructuredPrescriptionInputs(planID, pages.StructuredTrainingAudience{GroupName: "Competição"}, week, recipients, nil)
	if err != nil || len(candidates) != 3 {
		t.Fatalf("candidates=%#v err=%v", candidates, err)
	}
	store := &structuredTrainingStoreStub{publicationMembers: recipients, publicationHashes: []dbgen.ListLatestTrainingPrescriptionHashesForPlanRow{
		{MembershipID: firstMembership, SessionID: sessionID, SnapshotSha256: candidates[0].SnapshotSHA256},
		{MembershipID: secondMembership, SessionID: sessionID, SnapshotSha256: "outdated"},
		{MembershipID: removedMembership, SessionID: sessionID, SnapshotSha256: "removed"},
	}}
	stateCurrent := true
	states := (StructuredTraining{Store: store, Location: time.UTC}).structuredPublicationStates(context.Background(), []pages.StructuredTrainingAudience{{GroupName: "Competição", Weeks: []pages.StructuredTrainingWeek{week}}}, []dbgen.ListManagedTrainingPublicationStatesRow{{ID: planID, Title: "Semana 1", SourceUpdatedAt: pgtype.Timestamptz{Time: updated, Valid: true}, PublishedRevision: 2, PublicationCurrent: &stateCurrent, PublishedAt: pgtype.Timestamptz{Time: updated.Add(-time.Hour), Valid: true}}}, nil)
	if len(states) != 1 || states[0].Status != "Publicado · sem alterações pendentes" || states[0].AthleteCount != 3 || states[0].PrescriptionCount != 3 || states[0].AddedCount != 1 || states[0].ChangedCount != 1 || states[0].RemovedCount != 1 || states[0].UnchangedCount != 1 || states[0].PublishedAt == "" {
		t.Fatalf("states=%#v", states)
	}
}

func TestBuildStructuredPublicationRejectsStaleSourceVersionBeforeWriting(t *testing.T) {
	planID, actorID := uuid.New(), uuid.New()
	updated := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	store := &structuredTrainingStoreStub{publicationStates: []dbgen.ListManagedTrainingPublicationStatesRow{{ID: planID, SourceUpdatedAt: pgtype.Timestamptz{Time: updated, Valid: true}}}}
	h := StructuredTraining{Store: store, Location: time.UTC}
	_, err := h.buildStructuredPublication(context.Background(), CurrentUser{ID: actorID, IsAdmin: true}, planID, updated.Add(time.Second), "Publicação inicial")
	if !errors.Is(err, errStructuredTrainingPublicationConflict) {
		t.Fatalf("error=%v", err)
	}
}

func TestBuildStructuredPublicationBuildsImmutableRecipientSnapshot(t *testing.T) {
	planID, sessionID, membershipID, athleteID, actorID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	updated := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	planTitle, sessionTitle := "Semana 1", "Água"
	store := &structuredTrainingStoreStub{
		publicationStates:  []dbgen.ListManagedTrainingPublicationStatesRow{{ID: planID, SourceUpdatedAt: pgtype.Timestamptz{Time: updated, Valid: true}}},
		publicationMembers: []dbgen.ListStructuredTrainingPublicationMembersRow{{SessionID: sessionID, MembershipID: membershipID, AthleteUserID: athleteID}},
		overviewRows:       []dbgen.ListStructuredTrainingOverviewForManagerRow{{GroupID: uuid.New(), GroupName: "Competição", ProgrammeName: "Competição", PlanID: &planID, PlanTitle: &planTitle, WeekStart: pgtype.Date{Time: updated, Valid: true}, SessionID: &sessionID, SessionTitle: &sessionTitle, StartsAt: pgtype.Timestamptz{Time: updated.Add(24 * time.Hour), Valid: true}, EndsAt: pgtype.Timestamptz{Time: updated.Add(25 * time.Hour), Valid: true}}},
	}
	h := StructuredTraining{Store: store, Location: time.UTC}
	input, err := h.buildStructuredPublication(context.Background(), CurrentUser{ID: actorID, IsAdmin: true}, planID, updated, "Publicação inicial")
	if err != nil || input.PlanID != planID || input.PublishedByID != actorID || len(input.Prescriptions) != 1 || input.Prescriptions[0].AthleteUserID != athleteID || len(input.Prescriptions[0].Snapshot) == 0 {
		t.Fatalf("input=%#v error=%v", input, err)
	}
}

func TestPublishStructuredPlanPersistsFreshPrivateRecipientRevision(t *testing.T) {
	planID, sessionID, membershipID, athleteID, actorID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	updated := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	planTitle, sessionTitle := "Semana 1", "Água"
	store := &structuredTrainingStoreStub{
		weekOK:             true,
		publicationStates:  []dbgen.ListManagedTrainingPublicationStatesRow{{ID: planID, SourceUpdatedAt: pgtype.Timestamptz{Time: updated, Valid: true}}},
		publicationMembers: []dbgen.ListStructuredTrainingPublicationMembersRow{{SessionID: sessionID, MembershipID: membershipID, AthleteUserID: athleteID}},
		overviewRows:       []dbgen.ListStructuredTrainingOverviewForManagerRow{{GroupID: uuid.New(), GroupName: "Competição", ProgrammeName: "Competição", PlanID: &planID, PlanTitle: &planTitle, WeekStart: pgtype.Date{Time: updated, Valid: true}, SessionID: &sessionID, SessionTitle: &sessionTitle, StartsAt: pgtype.Timestamptz{Time: updated.Add(24 * time.Hour), Valid: true}, EndsAt: pgtype.Timestamptz{Time: updated.Add(25 * time.Hour), Valid: true}}},
	}
	h := StructuredTraining{Store: store, Location: time.UTC}
	values := url.Values{"source_updated_at": {updated.Format(time.RFC3339Nano)}, "change_summary": {"Publicação inicial"}}
	r := httptest.NewRequest(http.MethodPost, "/admin/treinos/estruturados/planos/"+planID.String()+"/publicar", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetPathValue("id", planID.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actorID, IsAdmin: true}))
	w := httptest.NewRecorder()

	h.PublishPlan(w, r)

	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/treinos/estruturados#training-publication" {
		t.Fatalf("response=%d location=%q", w.Code, w.Header().Get("Location"))
	}
	input := store.publishedInput
	if input.PlanID != planID || input.PublishedByID != actorID || !input.SourceUpdatedAt.Time.Equal(updated) || input.ChangeSummary != "Publicação inicial" || len(input.Prescriptions) != 1 || input.Prescriptions[0].AthleteUserID != athleteID || input.Prescriptions[0].MembershipID != membershipID {
		t.Fatalf("publication input=%#v", input)
	}
}

func TestPublishStructuredPlanReportsOptimisticConflictAndServiceFailures(t *testing.T) {
	planID, sessionID, membershipID, athleteID, actorID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	updated := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	planTitle, sessionTitle := "Semana 1", "Água"
	values := url.Values{"source_updated_at": {updated.Format(time.RFC3339Nano)}, "change_summary": {"Publicação inicial"}}
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{name: "concurrent publication", err: errStructuredTrainingPublicationConflict, want: http.StatusConflict},
		{name: "unique publication", err: &pgconn.PgError{Code: "23505"}, want: http.StatusConflict},
		{name: "service failure", err: errors.New("database unavailable"), want: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &structuredTrainingStoreStub{weekOK: true, publishErr: tc.err,
				publicationStates:  []dbgen.ListManagedTrainingPublicationStatesRow{{ID: planID, SourceUpdatedAt: pgtype.Timestamptz{Time: updated, Valid: true}}},
				publicationMembers: []dbgen.ListStructuredTrainingPublicationMembersRow{{SessionID: sessionID, MembershipID: membershipID, AthleteUserID: athleteID}},
				overviewRows:       []dbgen.ListStructuredTrainingOverviewForManagerRow{{GroupID: uuid.New(), GroupName: "Competição", ProgrammeName: "Competição", PlanID: &planID, PlanTitle: &planTitle, WeekStart: pgtype.Date{Time: updated, Valid: true}, SessionID: &sessionID, SessionTitle: &sessionTitle, StartsAt: pgtype.Timestamptz{Time: updated.Add(24 * time.Hour), Valid: true}, EndsAt: pgtype.Timestamptz{Time: updated.Add(25 * time.Hour), Valid: true}}},
			}
			response := performStructuredTrainingRequest(t, CurrentUser{ID: actorID, IsAdmin: true}, http.MethodPost, "/admin/treinos/estruturados/planos/"+planID.String()+"/publicar", values, "id", planID.String(), (StructuredTraining{Store: store, Location: time.UTC}).PublishPlan)
			if response.Code != tc.want {
				t.Fatalf("status=%d, want %d", response.Code, tc.want)
			}
		})
	}
}

func TestPublishStructuredPlanFailsClosedBeforePersistingInvalidPublicationInputs(t *testing.T) {
	planID, sessionID, membershipID, athleteID, actorID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	updated := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	planTitle, sessionTitle := "Semana 1", "Água"
	base := func() structuredTrainingStoreStub {
		return structuredTrainingStoreStub{
			weekOK:             true,
			publicationStates:  []dbgen.ListManagedTrainingPublicationStatesRow{{ID: planID, SourceUpdatedAt: pgtype.Timestamptz{Time: updated, Valid: true}}},
			publicationMembers: []dbgen.ListStructuredTrainingPublicationMembersRow{{SessionID: sessionID, MembershipID: membershipID, AthleteUserID: athleteID}},
			overviewRows: []dbgen.ListStructuredTrainingOverviewForManagerRow{{
				GroupID: uuid.New(), GroupName: "Competição", ProgrammeName: "Competição", PlanID: &planID, PlanTitle: &planTitle, WeekStart: pgtype.Date{Time: updated, Valid: true},
				SessionID: &sessionID, SessionTitle: &sessionTitle, StartsAt: pgtype.Timestamptz{Time: updated.Add(24 * time.Hour), Valid: true}, EndsAt: pgtype.Timestamptz{Time: updated.Add(25 * time.Hour), Valid: true},
			}},
		}
	}
	values := url.Values{"source_updated_at": {updated.Format(time.RFC3339Nano)}, "change_summary": {"Publicação inicial"}}
	for _, tc := range []struct {
		name   string
		mutate func(*structuredTrainingStoreStub)
		want   int
	}{
		{name: "unknown source plan", mutate: func(s *structuredTrainingStoreStub) { s.publicationStates = nil }, want: http.StatusNotFound},
		{name: "source-state read failure", mutate: func(s *structuredTrainingStoreStub) { s.publicationStatesErr = errors.New("database unavailable") }, want: http.StatusInternalServerError},
		{name: "overview read failure", mutate: func(s *structuredTrainingStoreStub) { s.overviewErr = errors.New("database unavailable") }, want: http.StatusInternalServerError},
		{name: "missing assembled week", mutate: func(s *structuredTrainingStoreStub) { s.overviewRows = nil }, want: http.StatusNotFound},
		{name: "recipient read failure", mutate: func(s *structuredTrainingStoreStub) { s.publicationMembersErr = errors.New("database unavailable") }, want: http.StatusInternalServerError},
		{name: "variation-match read failure", mutate: func(s *structuredTrainingStoreStub) { s.variationMatchesErr = errors.New("database unavailable") }, want: http.StatusInternalServerError},
		{name: "equal priority variation conflict", mutate: func(s *structuredTrainingStoreStub) {
			subjectID := uuid.New()
			s.variationMatches = []dbgen.ListTrainingVariationMatchesForManagerRow{{PlanID: planID, MembershipID: &membershipID, SubjectKind: dbgen.TrainingVariationSubjectKindSEGMENT, SubjectID: subjectID, Priority: 1}, {PlanID: planID, MembershipID: &membershipID, SubjectKind: dbgen.TrainingVariationSubjectKindSEGMENT, SubjectID: subjectID, Priority: 1}}
		}, want: http.StatusConflict},
		{name: "empty new publication", mutate: func(s *structuredTrainingStoreStub) { s.publicationMembers = nil }, want: http.StatusNotFound},
		{name: "previous-hash read failure", mutate: func(s *structuredTrainingStoreStub) {
			s.publicationMembers = nil
			s.publicationHashesErr = errors.New("database unavailable")
		}, want: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := base()
			tc.mutate(&store)
			response := performStructuredTrainingRequest(t, CurrentUser{ID: actorID, IsAdmin: true}, http.MethodPost, "/admin/treinos/estruturados/planos/"+planID.String()+"/publicar", values, "id", planID.String(), (StructuredTraining{Store: &store, Location: time.UTC}).PublishPlan)
			if response.Code != tc.want {
				t.Fatalf("status=%d, want %d", response.Code, tc.want)
			}
			if store.publishedInput.PlanID != uuid.Nil {
				t.Fatalf("failed publication was persisted: %#v", store.publishedInput)
			}
		})
	}

	store := base()
	store.publicationMembers = nil
	store.publicationHashes = []dbgen.ListLatestTrainingPrescriptionHashesForPlanRow{{}}
	response := performStructuredTrainingRequest(t, CurrentUser{ID: actorID, IsAdmin: true}, http.MethodPost, "/admin/treinos/estruturados/planos/"+planID.String()+"/publicar", values, "id", planID.String(), (StructuredTraining{Store: &store, Location: time.UTC}).PublishPlan)
	if response.Code != http.StatusSeeOther || store.publishedInput.PlanID != planID || len(store.publishedInput.Prescriptions) != 0 {
		t.Fatalf("valid republish without recipients: status=%d input=%#v", response.Code, store.publishedInput)
	}
}

func TestCreateStructuredWaterBlockPersistsValidatedPrescriptionAndEffort(t *testing.T) {
	segmentID, planID, actorID := uuid.New(), uuid.New(), uuid.New()
	store := &structuredTrainingStoreStub{planID: planID, weekOK: true}
	h := StructuredTraining{Store: store, Location: time.UTC}
	values := url.Values{"purpose": {"MAIN"}, "title": {"Intervalos"}, "instructions": {"Manter a técnica"}, "method": {"INTERVALS"}, "target_distance_metres": {"4000"}, "target_distance_certainty": {"EXACT"}, "step_kind": {"EFFORT"}, "step_name": {"500 m"}, "duration_seconds": {"120"}, "duration_certainty": {"ESTIMATED"}, "intensity_code": {"R6"}, "step_instructions": {"Ritmo controlado"}}
	r := httptest.NewRequest(http.MethodPost, "/admin/treinos/estruturados/segmentos/"+segmentID.String()+"/agua", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetPathValue("id", segmentID.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actorID, IsAdmin: true}))
	w := httptest.NewRecorder()

	h.CreateWaterBlock(w, r)

	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/treinos/estruturados" || len(store.waterBlocks) != 1 {
		t.Fatalf("response=%d location=%q blocks=%#v", w.Code, w.Header().Get("Location"), store.waterBlocks)
	}
	input := store.waterBlocks[0]
	if input.Block.SegmentID != segmentID || input.Block.Purpose != dbgen.TrainingBlockPurposeMAIN || input.Prescription.Method != dbgen.WaterWorkMethodINTERVALS || input.Prescription.TargetDistanceMetres == nil || *input.Prescription.TargetDistanceMetres != 4000 || input.Step.Kind != dbgen.WaterStepKindEFFORT || input.Step.DurationSeconds == nil || *input.Step.DurationSeconds != 120 || input.Step.IntensityCode == nil || *input.Step.IntensityCode != "R6" {
		t.Fatalf("water block input=%#v", input)
	}
}

func TestCreateStructuredWaterBlockMapsStaleAndUnexpectedWrites(t *testing.T) {
	segmentID, planID, actorID := uuid.New(), uuid.New(), uuid.New()
	values := url.Values{"purpose": {"MAIN"}, "title": {"Intervalos"}, "instructions": {"Manter a técnica"}, "method": {"INTERVALS"}, "step_kind": {"EFFORT"}, "step_name": {"500 m"}, "duration_seconds": {"120"}, "duration_certainty": {"EXACT"}, "step_instructions": {"Ritmo controlado"}}
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{name: "stale segment", err: pgx.ErrNoRows, want: http.StatusForbidden},
		{name: "copy rejection", err: &pgconn.PgError{Code: "23514"}, want: http.StatusForbidden},
		{name: "service failure", err: errors.New("database unavailable"), want: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &structuredTrainingStoreStub{planID: planID, weekOK: true, createWaterBlockErr: tc.err}
			response := performStructuredTrainingRequest(t, CurrentUser{ID: actorID, IsAdmin: true}, http.MethodPost, "/admin/treinos/estruturados/segmentos/"+segmentID.String()+"/agua", values, "id", segmentID.String(), (StructuredTraining{Store: store, Location: time.UTC}).CreateWaterBlock)
			if response.Code != tc.want {
				t.Fatalf("status=%d, want %d", response.Code, tc.want)
			}
		})
	}
}

func TestCreateStructuredWaterBlockTaskReturnsToItsPlannerContext(t *testing.T) {
	groupID, planID, sessionID, segmentID, actorID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	weekTitle, sessionTitle, segmentTitle := "Semana 1", "Intervalos", "Água principal"
	modality := dbgen.TrainingSegmentModalityWATER
	startsAt := time.Date(2026, 9, 8, 18, 0, 0, 0, time.UTC)
	store := &structuredTrainingStoreStub{planID: planID, weekOK: true, overviewRows: []dbgen.ListStructuredTrainingOverviewForManagerRow{{GroupID: groupID, GroupName: "Competição", ProgrammeName: "Competição", PlanID: &planID, PlanTitle: &weekTitle, WeekStart: pgtype.Date{Time: startsAt, Valid: true}, SessionID: &sessionID, SessionTitle: &sessionTitle, StartsAt: pgtype.Timestamptz{Time: startsAt, Valid: true}, EndsAt: pgtype.Timestamptz{Time: startsAt.Add(time.Hour), Valid: true}, SegmentID: &segmentID, SegmentModality: &modality, SegmentTitle: &segmentTitle}}}
	h := StructuredTraining{Store: store, Location: time.UTC}
	values := url.Values{"purpose": {"MAIN"}, "title": {"Intervalos"}, "instructions": {"Manter a técnica"}, "method": {"INTERVALS"}, "step_kind": {"EFFORT"}, "step_name": {"500 m"}, "duration_seconds": {"120"}, "duration_certainty": {"EXACT"}, "step_instructions": {"Ritmo controlado"}}
	r := httptest.NewRequest(http.MethodPost, structuredWaterTaskPath(sessionID, segmentID), strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetPathValue("session_id", sessionID.String())
	r.SetPathValue("segment_id", segmentID.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actorID, IsAdmin: true}))
	w := httptest.NewRecorder()

	h.CreateWaterBlockTask(w, r)

	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != structuredPlannerURL(groupID.String(), planID.String(), sessionID.String()) || len(store.waterBlocks) != 1 || store.waterBlocks[0].Block.SegmentID != segmentID {
		t.Fatalf("response=%d location=%q blocks=%#v", w.Code, w.Header().Get("Location"), store.waterBlocks)
	}
}

func TestGymBlockTaskRendersStructuredPlannerContext(t *testing.T) {
	groupID, planID, sessionID, segmentID, actorID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	weekTitle, sessionTitle, segmentTitle := "Semana 1", "Força", "Ginásio principal"
	modality := dbgen.TrainingSegmentModalityGYM
	startsAt := time.Date(2026, 9, 8, 18, 0, 0, 0, time.UTC)
	store := &structuredTrainingStoreStub{planID: planID, weekOK: true, overviewRows: []dbgen.ListStructuredTrainingOverviewForManagerRow{{GroupID: groupID, GroupName: "Competição", ProgrammeName: "Competição", PlanID: &planID, PlanTitle: &weekTitle, WeekStart: pgtype.Date{Time: startsAt, Valid: true}, SessionID: &sessionID, SessionTitle: &sessionTitle, StartsAt: pgtype.Timestamptz{Time: startsAt, Valid: true}, EndsAt: pgtype.Timestamptz{Time: startsAt.Add(time.Hour), Valid: true}, SegmentID: &segmentID, SegmentModality: &modality, SegmentTitle: &segmentTitle}}}
	r := httptest.NewRequest(http.MethodGet, "/admin/treinos/estruturados/sessoes/"+sessionID.String()+"/segmentos/"+segmentID.String()+"/ginasio", nil)
	r.SetPathValue("session_id", sessionID.String())
	r.SetPathValue("segment_id", segmentID.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actorID, IsAdmin: true}))
	w := httptest.NewRecorder()

	(StructuredTraining{Store: store, Location: time.UTC}).GymBlockTask(w, r)

	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "text/html; charset=utf-8" || !strings.Contains(w.Body.String(), "Adicionar bloco de ginásio") || !strings.Contains(w.Body.String(), "Ginásio principal") {
		t.Fatalf("response=%d headers=%v body=%s", w.Code, w.Header(), w.Body.String())
	}
}

func TestCreateGymBlockTaskPersistsValidatedExerciseInPlannerContext(t *testing.T) {
	groupID, planID, sessionID, segmentID, actorID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	weekTitle, sessionTitle, segmentTitle := "Semana 1", "Força", "Ginásio principal"
	modality := dbgen.TrainingSegmentModalityGYM
	startsAt := time.Date(2026, 9, 8, 18, 0, 0, 0, time.UTC)
	store := &structuredTrainingStoreStub{planID: planID, weekOK: true, overviewRows: []dbgen.ListStructuredTrainingOverviewForManagerRow{{GroupID: groupID, GroupName: "Competição", ProgrammeName: "Competição", PlanID: &planID, PlanTitle: &weekTitle, WeekStart: pgtype.Date{Time: startsAt, Valid: true}, SessionID: &sessionID, SessionTitle: &sessionTitle, StartsAt: pgtype.Timestamptz{Time: startsAt, Valid: true}, EndsAt: pgtype.Timestamptz{Time: startsAt.Add(time.Hour), Valid: true}, SegmentID: &segmentID, SegmentModality: &modality, SegmentTitle: &segmentTitle}}}
	values := url.Values{"purpose": {"MAIN"}, "title": {"Força geral"}, "instructions": {"Movimentos controlados"}, "structure": {"STRAIGHT_SETS"}, "objective": {"MAX_STRENGTH_HYPERTROPHY"}, "rounds": {"3"}, "exercise_name": {"Agachamento"}, "sets": {"3"}, "repetitions": {"8"}, "resistance_kind": {"KILOGRAMS"}, "resistance_value": {"60"}, "execution_intent": {"CONTROLLED"}}
	r := httptest.NewRequest(http.MethodPost, "/admin/treinos/estruturados/sessoes/"+sessionID.String()+"/segmentos/"+segmentID.String()+"/ginasio", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetPathValue("session_id", sessionID.String())
	r.SetPathValue("segment_id", segmentID.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actorID, IsAdmin: true}))
	w := httptest.NewRecorder()

	(StructuredTraining{Store: store, Location: time.UTC}).CreateGymBlockTask(w, r)

	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != structuredPlannerPath || len(store.gymBlocks) != 1 {
		t.Fatalf("response=%d location=%q blocks=%#v", w.Code, w.Header().Get("Location"), store.gymBlocks)
	}
	input := store.gymBlocks[0]
	if input.Block.SegmentID != segmentID || input.Block.Purpose != dbgen.TrainingBlockPurposeMAIN || input.Prescription.Rounds != 3 || input.Exercise.Name != "Agachamento" || input.Exercise.Repetitions == nil || *input.Exercise.Repetitions != 8 || input.Exercise.ResistanceValue == nil || *input.Exercise.ResistanceValue != 60 {
		t.Fatalf("input=%#v", input)
	}
}

func TestParseGymExerciseValidatesResistanceModes(t *testing.T) {
	newRequest := func(values url.Values) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/admin/treinos/estruturados", strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		return r
	}
	for _, tc := range []struct {
		name    string
		values  url.Values
		wantErr bool
	}{
		{"bodyweight", url.Values{"exercise_name": {"Prancha"}, "duration_seconds": {"45"}, "resistance_kind": {"BODY_WEIGHT"}}, false},
		{"band instruction", url.Values{"exercise_name": {"Remada"}, "repetitions": {"12"}, "resistance_kind": {"BAND"}, "resistance_text": {"Banda média"}}, false},
		{"invalid resistance payload", url.Values{"exercise_name": {"Agachamento"}, "repetitions": {"8"}, "resistance_kind": {"KILOGRAMS"}, "resistance_text": {"pesado"}}, true},
		{"missing measurable work", url.Values{"exercise_name": {"Agachamento"}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseGymExercise(newRequest(tc.values))
			if (err != nil) != tc.wantErr {
				t.Fatalf("error=%v wantErr=%t", err, tc.wantErr)
			}
		})
	}
}

func TestGymValidationAcceptsDocumentedObjectiveAndResistanceRanges(t *testing.T) {
	for _, objective := range []dbgen.TrainingObjective{
		dbgen.TrainingObjectiveMOBILITY, dbgen.TrainingObjectiveACTIVATION, dbgen.TrainingObjectiveMAXSTRENGTHHYPERTROPHY,
		dbgen.TrainingObjectiveMAXSTRENGTHNEURAL, dbgen.TrainingObjectiveEXPLOSIVESTRENGTH, dbgen.TrainingObjectiveSTRENGTHENDURANCE,
		dbgen.TrainingObjectiveTECHNIQUE, dbgen.TrainingObjectiveCORE, dbgen.TrainingObjectiveCUSTOM,
	} {
		if !validTrainingObjective(objective) {
			t.Fatalf("objective %q rejected", objective)
		}
	}
	for _, tc := range []struct {
		kind  dbgen.GymResistanceKind
		value float64
	}{
		{dbgen.GymResistanceKindPERCENT1RM, 85}, {dbgen.GymResistanceKindRPE, 8}, {dbgen.GymResistanceKindRIR, 2},
	} {
		if !validGymResistanceValue(tc.kind, tc.value) {
			t.Fatalf("resistance %q=%v rejected", tc.kind, tc.value)
		}
	}
}

func TestCreateStructuredWaterBlockTaskPreservesFeedbackForInvalidAndStaleWrites(t *testing.T) {
	groupID, planID, sessionID, segmentID, actorID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	weekTitle, sessionTitle, segmentTitle := "Semana 1", "Intervalos", "Água principal"
	modality := dbgen.TrainingSegmentModalityWATER
	startsAt := time.Date(2026, 9, 8, 18, 0, 0, 0, time.UTC)
	row := dbgen.ListStructuredTrainingOverviewForManagerRow{GroupID: groupID, GroupName: "Competição", ProgrammeName: "Competição", PlanID: &planID, PlanTitle: &weekTitle, WeekStart: pgtype.Date{Time: startsAt, Valid: true}, SessionID: &sessionID, SessionTitle: &sessionTitle, StartsAt: pgtype.Timestamptz{Time: startsAt, Valid: true}, EndsAt: pgtype.Timestamptz{Time: startsAt.Add(time.Hour), Valid: true}, SegmentID: &segmentID, SegmentModality: &modality, SegmentTitle: &segmentTitle}
	valid := url.Values{"purpose": {"MAIN"}, "title": {"Intervalos"}, "instructions": {"Manter a técnica"}, "method": {"INTERVALS"}, "step_kind": {"EFFORT"}, "step_name": {"500 m"}, "duration_seconds": {"120"}, "duration_certainty": {"EXACT"}, "step_instructions": {"Ritmo controlado"}}
	for _, tc := range []struct {
		name        string
		values      url.Values
		writeErr    error
		profilesErr error
		want        int
		contains    string
	}{
		{name: "invalid water form", values: url.Values{"purpose": {"MAIN"}}, want: http.StatusUnprocessableEntity, contains: "Corrija os seguintes campos"},
		{name: "stale segment", values: valid, writeErr: pgx.ErrNoRows, want: http.StatusConflict, contains: "segmento foi alterado"},
		{name: "profile lookup failure", values: url.Values{"purpose": {"MAIN"}}, profilesErr: errors.New("database unavailable"), want: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &structuredTrainingStoreStub{weekOK: true, overviewRows: []dbgen.ListStructuredTrainingOverviewForManagerRow{row}, createWaterBlockErr: tc.writeErr, listProfilesErr: tc.profilesErr}
			request := httptest.NewRequest(http.MethodPost, structuredWaterTaskPath(sessionID, segmentID), strings.NewReader(tc.values.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.SetPathValue("session_id", sessionID.String())
			request.SetPathValue("segment_id", segmentID.String())
			request = request.WithContext(context.WithValue(request.Context(), currentUserKey{}, CurrentUser{ID: actorID, IsAdmin: true}))
			response := httptest.NewRecorder()
			(StructuredTraining{Store: store, Location: time.UTC}).CreateWaterBlockTask(response, request)
			if response.Code != tc.want || (tc.contains != "" && !strings.Contains(response.Body.String(), tc.contains)) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestStructuredWaterBlockTaskFailsClosedForUnknownAndUnauthorizedSegments(t *testing.T) {
	groupID, planID, sessionID, segmentID, actorID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	weekTitle, sessionTitle, segmentTitle := "Semana 1", "Intervalos", "Água principal"
	modality := dbgen.TrainingSegmentModalityWATER
	startsAt := time.Date(2026, 9, 8, 18, 0, 0, 0, time.UTC)
	row := dbgen.ListStructuredTrainingOverviewForManagerRow{GroupID: groupID, GroupName: "Competição", ProgrammeName: "Competição", PlanID: &planID, PlanTitle: &weekTitle, WeekStart: pgtype.Date{Time: startsAt, Valid: true}, SessionID: &sessionID, SessionTitle: &sessionTitle, StartsAt: pgtype.Timestamptz{Time: startsAt, Valid: true}, EndsAt: pgtype.Timestamptz{Time: startsAt.Add(time.Hour), Valid: true}, SegmentID: &segmentID, SegmentModality: &modality, SegmentTitle: &segmentTitle}
	request := func(targetSegmentID uuid.UUID) *http.Request {
		r := httptest.NewRequest(http.MethodPost, structuredWaterTaskPath(sessionID, targetSegmentID), strings.NewReader("purpose=MAIN"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.SetPathValue("session_id", sessionID.String())
		r.SetPathValue("segment_id", targetSegmentID.String())
		return r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actorID, IsAdmin: true}))
	}

	t.Run("unknown segment", func(t *testing.T) {
		store := &structuredTrainingStoreStub{planID: planID, weekOK: true, overviewRows: []dbgen.ListStructuredTrainingOverviewForManagerRow{row}}
		w := httptest.NewRecorder()
		(StructuredTraining{Store: store, Location: time.UTC}).CreateWaterBlockTask(w, request(uuid.New()))
		if w.Code != http.StatusNotFound || len(store.waterBlocks) != 0 {
			t.Fatalf("response=%d writes=%#v", w.Code, store.waterBlocks)
		}
	})

	t.Run("unauthorized segment", func(t *testing.T) {
		store := &structuredTrainingStoreStub{planID: planID, weekOK: false, overviewRows: []dbgen.ListStructuredTrainingOverviewForManagerRow{row}}
		w := httptest.NewRecorder()
		(StructuredTraining{Store: store, Location: time.UTC}).CreateWaterBlockTask(w, request(segmentID))
		if w.Code != http.StatusForbidden || len(store.waterBlocks) != 0 {
			t.Fatalf("response=%d writes=%#v", w.Code, store.waterBlocks)
		}
	})
}

func TestStructuredWaterTaskHelpersMapMalformedContextAndFieldErrors(t *testing.T) {
	sessionID, segmentID := uuid.New(), uuid.New()
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.SetPathValue("session_id", sessionID.String())
	r.SetPathValue("segment_id", segmentID.String())
	if gotSession, gotSegment, err := structuredWaterTaskIDs(r); err != nil || gotSession != sessionID || gotSegment != segmentID {
		t.Fatalf("ids=%s,%s err=%v", gotSession, gotSegment, err)
	}
	r.SetPathValue("segment_id", "invalid")
	if _, _, err := structuredWaterTaskIDs(r); err == nil {
		t.Fatal("invalid segment id accepted")
	}

	for err, field := range map[error]string{
		errors.New("invalid water step"):  "step_name",
		errors.New("invalid water block"): "title",
		errors.New("invalid measure"):     "form",
	} {
		if mapped := structuredWaterTaskErrors(r, err); mapped[field] == "" || len(mapped) != 1 {
			t.Errorf("error=%v mapped=%#v", err, mapped)
		}
	}
	if structuredSegmentTaskTitle(pages.StructuredTrainingSegment{Modality: "WATER"}) != "WATER" || structuredSegmentTaskTitle(pages.StructuredTrainingSegment{Title: "Técnica", Modality: "WATER"}) != "Técnica" {
		t.Fatal("segment task title did not preserve the usable label")
	}
}

func TestStructuredAuthoringMutationsRejectMalformedResourceIDs(t *testing.T) {
	h := StructuredTraining{Store: &structuredTrainingStoreStub{}}
	for _, tc := range []struct {
		name string
		call func(http.ResponseWriter, *http.Request)
		want int
	}{
		{"segment", h.CreateSegment, http.StatusNotFound},
		{"block", h.CreateBlock, http.StatusNotFound},
		{"gym block", h.CreateGymBlock, http.StatusForbidden},
		{"gym exercise", h.CreateGymExercise, http.StatusForbidden},
		{"water block", h.CreateWaterBlock, http.StatusForbidden},
		{"water step", h.CreateWaterWorkStep, http.StatusForbidden},
		{"intensity zone", h.CreateWaterIntensityZone, http.StatusForbidden},
		{"copy block", h.CopyBlock, http.StatusForbidden},
		{"copy week", h.CopyWeek, http.StatusForbidden},
		{"retire variation", h.RetireVariation, http.StatusForbidden},
		{"move segment", h.MoveSegment, http.StatusNotFound},
		{"move block", h.MoveBlock, http.StatusNotFound},
		{"move exercise", h.MoveGymExercise, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/admin/treinos/estruturados/invalid", nil)
			r.SetPathValue("id", "invalid")
			r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: uuid.New(), IsAdmin: true}))
			w := httptest.NewRecorder()
			tc.call(w, r)
			if w.Code != tc.want {
				t.Fatalf("response=%d want=%d", w.Code, tc.want)
			}
		})
	}
}

func TestStructuredAuthoringMutationsRejectMalformedFormsBeforeStoreAccess(t *testing.T) {
	h := StructuredTraining{Store: &structuredTrainingStoreStub{}}
	id := uuid.New().String()
	for _, tc := range []struct {
		name string
		call func(http.ResponseWriter, *http.Request)
	}{
		{"segment", h.CreateSegment}, {"block", h.CreateBlock}, {"gym block", h.CreateGymBlock}, {"gym exercise", h.CreateGymExercise},
		{"water block", h.CreateWaterBlock}, {"water step", h.CreateWaterWorkStep}, {"water profile", h.CreateWaterIntensityProfile},
		{"water zone", h.CreateWaterIntensityZone}, {"routine", h.CreateRoutine}, {"insert routine", h.InsertRoutine},
		{"copy block", h.CopyBlock}, {"copy session", h.CopySession}, {"copy day", h.CopyDay}, {"copy week", h.CopyWeek},
		{"variation group", h.CreateVariationGroup}, {"variation", h.CreateVariation}, {"retire variation", h.RetireVariation},
		{"move segment", h.MoveSegment}, {"move block", h.MoveBlock}, {"move exercise", h.MoveGymExercise},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/admin/treinos/estruturados/"+id, nil)
			r.SetPathValue("id", id)
			r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: uuid.New(), IsAdmin: true}))
			w := httptest.NewRecorder()
			tc.call(w, r)
			if w.Code != http.StatusForbidden {
				t.Fatalf("response=%d", w.Code)
			}
		})
	}
}

func TestStructuredWaterRowBuildsReadablePrescriptionsAndDrillMetadata(t *testing.T) {
	method := dbgen.WaterWorkMethodINTERVALS
	certainty := dbgen.TrainingMeasureCertaintyESTIMATED
	craft := dbgen.PaddlingCraftCANOE
	stepKind := dbgen.WaterStepKindEFFORT
	profile, zone, meaning := "Perfil 2 km", "R4", "Ritmo sustentável"
	focus, format, roles, notes := "Viragens", "K2", "Proa", "Técnica"
	name, intensity := "500 m", "R4"
	target, revision, position, repeats, duration, distance, recovery, cadence, cadenceMin, cadenceMax := int32(12000), int32(2), int32(1), int32(4), int32(110), int32(500), int32(60), int32(92), int32(88), int32(96)
	row := structuredWaterRow(structuredTrainingRow{}, &method, &target, &certainty, &profile, &revision, &craft, ptr(uuid.New()), nil, &position, &stepKind, &name, &repeats, &duration, &certainty, &distance, &certainty, &recovery, &intensity, &cadence, &zone, &cadenceMin, &cadenceMax, &meaning, &focus, &format, &roles, &notes)
	for _, want := range []string{"Continuar até a sessão atingir 12 km (estimado)", "Perfil 2 km · Canoa · revisão 2", "110 s (estimado)", "0,5 km (estimado)", "R4 · 92 remadas/min · R4 · orientação 88–96 remadas/min · Ritmo sustentável", "Viragens · K2 · Proa"} {
		joined := row.waterTarget + "|" + row.waterProfile + "|" + row.waterStepPrescription + "|" + row.waterStepIntensity + "|" + row.waterStepDrill
		if !strings.Contains(joined, want) {
			t.Errorf("water row missing %q: %#v", want, row)
		}
	}
}

func TestStructuredWaterProfilesGroupZonesByProfileAndKeepCadenceBounds(t *testing.T) {
	profileID, firstZone, secondZone := uuid.New(), uuid.New(), uuid.New()
	code, label, meaning := "R4", "Ritmo", "Sustentável"
	min, max := int32(80), int32(90)
	rows := []dbgen.ListActiveWaterIntensityProfilesRow{
		{ID: profileID, Name: "Kayak base", Craft: dbgen.PaddlingCraftKAYAK, Notes: "Clube", Revision: 2, ZoneID: &firstZone, Code: &code, Label: &label, Meaning: &meaning, CadenceMin: &min, CadenceMax: &max},
		{ID: profileID, Name: "Kayak base", Craft: dbgen.PaddlingCraftKAYAK, Notes: "Clube", Revision: 2, ZoneID: &secondZone, Code: &code, Label: &label, Meaning: &meaning},
	}
	profiles := structuredWaterProfiles(rows)
	if len(profiles) != 1 || profiles[0].Craft != "Kayak" || len(profiles[0].Zones) != 2 || profiles[0].Zones[0].Cadence != "80–90 remadas/min" || profiles[0].Zones[1].Cadence != "Sem cadência fixa" {
		t.Fatalf("profiles = %#v", profiles)
	}
}

func TestStructuredWaterStepPrescriptionDistinguishesExactAndEstimatedMeasures(t *testing.T) {
	if got, want := structuredPrescriptionWaterStep(pages.StructuredWaterStep{DurationSeconds: 90, DurationCertainty: "ESTIMATED", DistanceMetres: 1250, DistanceCertainty: "EXACT"}), "90 s (estimado) · 1,25 km (exato)"; got != want {
		t.Fatalf("prescription = %q, want %q", got, want)
	}
	if got := structuredPrescriptionWaterStep(pages.StructuredWaterStep{}); got != "" {
		t.Fatalf("empty prescription = %q", got)
	}
}

func TestParseWaterWorkStepPreservesNestedPrescriptionSemanticsAndRejectsInvalidShapes(t *testing.T) {
	parentID := uuid.New()
	request := func(values url.Values) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		return r
	}
	repeat, err := parseWaterWorkStep(request(url.Values{
		"parent_step_id": {parentID.String()}, "step_kind": {"REPEAT_GROUP"}, "step_name": {"Série principal"}, "repeats": {"4"}, "recovery_seconds": {"75"},
		"intensity_code": {"R5"}, "drill_focus": {"Partidas"}, "drill_format": {"3 contra 2"}, "role_notes": {"GR roda"},
	}))
	if err != nil || repeat.ParentStepID == nil || *repeat.ParentStepID != parentID || repeat.Repeats == nil || *repeat.Repeats != 4 || repeat.RecoverySeconds == nil || *repeat.RecoverySeconds != 75 || repeat.IntensityCode == nil || *repeat.IntensityCode != "R5" || repeat.DrillFocus == nil || repeat.DrillFormat == nil || repeat.RoleNotes == nil {
		t.Fatalf("repeat=%#v err=%v", repeat, err)
	}
	effort, err := parseWaterWorkStep(request(url.Values{"step_kind": {"EFFORT"}, "step_name": {"Remar solto"}, "step_instructions": {"Manter a técnica e recuperar"}}))
	if err != nil || effort.DurationSeconds != nil || effort.DistanceMetres != nil || effort.DurationCertainty != nil || effort.DistanceCertainty != nil {
		t.Fatalf("coached effort=%#v err=%v", effort, err)
	}
	effort, err = parseWaterWorkStep(request(url.Values{"step_kind": {"EFFORT"}, "step_name": {"500 m"}, "duration_seconds": {"120"}, "duration_certainty": {"EXACT"}, "distance_metres": {"500"}, "distance_certainty": {"ESTIMATED"}, "cadence_spm": {"96"}}))
	if err != nil || effort.DurationCertainty == nil || *effort.DurationCertainty != dbgen.TrainingMeasureCertaintyEXACT || effort.DistanceCertainty == nil || *effort.DistanceCertainty != dbgen.TrainingMeasureCertaintyESTIMATED || effort.CadenceSpm == nil || *effort.CadenceSpm != 96 {
		t.Fatalf("measured effort=%#v err=%v", effort, err)
	}
	for _, tc := range []struct {
		name   string
		values url.Values
	}{
		{name: "malformed parent", values: url.Values{"parent_step_id": {"not-a-uuid"}, "step_kind": {"EFFORT"}, "step_name": {"500 m"}}},
		{name: "certainty without measure", values: url.Values{"step_kind": {"EFFORT"}, "step_name": {"500 m"}, "duration_certainty": {"EXACT"}}},
		{name: "repeat group with effort duration", values: url.Values{"step_kind": {"REPEAT_GROUP"}, "step_name": {"Série"}, "repeats": {"3"}, "duration_seconds": {"60"}, "duration_certainty": {"EXACT"}}},
		{name: "effort with repeats", values: url.Values{"step_kind": {"EFFORT"}, "step_name": {"Série"}, "repeats": {"3"}}},
		{name: "unknown step kind", values: url.Values{"step_kind": {"UNKNOWN"}, "step_name": {"Série"}}},
		{name: "invalid numeric range", values: url.Values{"step_kind": {"EFFORT"}, "step_name": {"Série"}, "cadence_spm": {"301"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseWaterWorkStep(request(tc.values)); err == nil {
				t.Fatal("invalid step was accepted")
			}
		})
	}
}

func TestManagerStructuredRowsMapsTrustedReadModelIntoPlanningRows(t *testing.T) {
	groupID, planID, sessionID, segmentID, blockID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	planTitle, sessionTitle, segmentTitle, blockTitle := "Semana 1", "Água", "Técnica", "Aquecimento"
	starts := time.Date(2026, 9, 8, 9, 0, 0, 0, time.UTC)
	position := int32(1)
	rows := managerStructuredRows([]dbgen.ListStructuredTrainingOverviewForManagerRow{{GroupID: groupID, GroupName: "Competição", ProgrammeName: "Competição", MemberCount: 4, PlanID: &planID, PlanTitle: &planTitle, SessionID: &sessionID, SessionTitle: &sessionTitle, StartsAt: pgtype.Timestamptz{Time: starts, Valid: true}, EndsAt: pgtype.Timestamptz{Time: starts.Add(time.Hour), Valid: true}, SegmentID: &segmentID, SegmentPosition: &position, SegmentTitle: &segmentTitle, BlockID: &blockID, BlockPosition: &position, BlockTitle: &blockTitle}})
	if len(rows) != 1 || rows[0].groupName != "Competição" || rows[0].scope != "Competição" || rows[0].planID == nil || *rows[0].planID != planID || rows[0].sessionID == nil || *rows[0].sessionID != sessionID || rows[0].segmentTitle != segmentTitle || rows[0].blockTitle != blockTitle {
		t.Fatalf("rows = %#v", rows)
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

	returnTo := structuredPlannerURL(uuid.New().String(), uuid.New().String(), uuid.New().String())
	stepValues := url.Values{
		"parent_step_id": {parentID.String()}, "step_kind": {"EFFORT"}, "step_name": {"Ataque 3 contra 2"},
		"duration_seconds": {"120"}, "duration_certainty": {"EXACT"}, "recovery_seconds": {"60"},
		"intensity_code": {"R7"}, "drill_focus": {"Ataque"}, "drill_format": {"3 contra 2"},
		"role_notes": {"GR e pivot"}, "step_instructions": {"Ritmo de uma prova de dois minutos"}, "return_to": {returnTo},
	}
	response = performStructuredTrainingRequest(t, user, http.MethodPost, "/admin/treinos/estruturados/blocos/"+blockID.String()+"/agua/passos", stepValues, "id", blockID.String(), handler.CreateWaterWorkStep)
	if response.Code != http.StatusSeeOther || len(store.waterSteps) != 1 {
		t.Fatalf("create water step: status=%d writes=%d", response.Code, len(store.waterSteps))
	}
	if got := store.waterSteps[0]; got.ParentStepID == nil || *got.ParentStepID != parentID || got.IntensityCode == nil || *got.IntensityCode != "R7" || got.DrillFormat == nil || *got.DrillFormat != "3 contra 2" || got.RecoverySeconds == nil || *got.RecoverySeconds != 60 {
		t.Fatalf("water step lost nesting or drill metadata: %+v", got)
	}
	if got := response.Header().Get("Location"); got != returnTo {
		t.Fatalf("water step return location = %q, want %q", got, returnTo)
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

func TestCalculateStructuredWeekSummaryKeepsPlannedActualAndTargetsDistinct(t *testing.T) {
	summary := calculateStructuredWeekSummary([]pages.StructuredTrainingSession{
		{EntryKind: "TRAINING", ActualDurationMinutes: 70, ActualDistanceMetres: 9800, Segments: []pages.StructuredTrainingSegment{
			{Modality: "GYM"},
			{Modality: "WATER", Blocks: []pages.StructuredTrainingBlock{{
				WaterTargetDistanceMetres: 12000, WaterTargetCertainty: "EXACT",
				WaterSteps: []pages.StructuredWaterStep{{ID: "warmup", Kind: "EFFORT", DurationSeconds: 600, DurationCertainty: "EXACT", DistanceMetres: 2000, DistanceCertainty: "EXACT", Intensity: "R2"}},
			}}},
		}},
		{EntryKind: "LOGISTICS", ActualDurationMinutes: 50, ActualDistanceMetres: 1000, Segments: []pages.StructuredTrainingSegment{{Modality: "RUN"}}},
	})
	if len(summary.PlannedWater) != 5 || summary.PlannedWater[2].Value != "2 km" || summary.PlannedWater[4].Value != "12 km" || !strings.Contains(summary.PlannedWater[4].Certainty, "não somada") {
		t.Fatalf("planned water = %#v", summary.PlannedWater)
	}
	if len(summary.SupportingWork) != 1 || summary.SupportingWork[0].Label != "Ginásio" || summary.SupportingWork[0].Value != "1 segmento" {
		t.Fatalf("supporting = %#v", summary.SupportingWork)
	}
	if len(summary.Actual) != 2 || summary.Actual[0].Value != "70 min" || summary.Actual[1].Value != "9,8 km" || !strings.Contains(summary.Actual[0].Certainty, "1 sessão") {
		t.Fatalf("actual = %#v", summary.Actual)
	}
}

func TestResolveStructuredPrescriptionAppliesAthleteOverrideAndPreservesGymValues(t *testing.T) {
	planID, sessionID, membershipID := uuid.New(), uuid.New(), uuid.New()
	segmentID, exerciseID := uuid.New(), uuid.New()
	session := pages.StructuredTrainingSession{
		ID: sessionID.String(), Modalities: []string{"GYM"},
		Segments: []pages.StructuredTrainingSegment{{ID: segmentID.String(), Modality: "GYM", Blocks: []pages.StructuredTrainingBlock{{Exercises: []pages.StructuredGymExercise{{ID: exerciseID.String(), Name: "Supino", Sets: 4, Repetitions: 10, RecoverySeconds: 90, Prescription: "4 × 10 · recuperação 90 s"}}}}}},
	}
	rows := []dbgen.ListTrainingVariationMatchesForManagerRow{
		{PlanID: planID, SessionID: sessionID, MembershipID: &membershipID, SubjectKind: dbgen.TrainingVariationSubjectKindGYMEXERCISE, SubjectID: exerciseID, SubjectLabel: "Supino", Operation: dbgen.TrainingVariationOperationOVERRIDE, ChangeSummary: "Carga do subgrupo", Patch: []byte(`{"sets":3}`), Priority: 1},
		{PlanID: planID, SessionID: sessionID, MembershipID: &membershipID, SubjectKind: dbgen.TrainingVariationSubjectKindGYMEXERCISE, SubjectID: exerciseID, SubjectLabel: "Supino", Operation: dbgen.TrainingVariationOperationOVERRIDE, ChangeSummary: "Carga individual", Patch: []byte(`{"repetitions":8}`), Priority: 2},
	}
	if err := resolveStructuredPrescription(&session, rows, planID, sessionID, membershipID); err != nil {
		t.Fatal(err)
	}
	exercise := session.Segments[0].Blocks[0].Exercises[0]
	if exercise.Sets != 4 || exercise.Repetitions != 8 || exercise.RecoverySeconds != 90 {
		t.Fatalf("override lost base gym values: %#v", exercise)
	}
	if exercise.Prescription != "4 séries · 8 repetições · recuperação 90 s" || len(session.Changes) != 1 || session.Changes[0].Summary != "Carga individual" {
		t.Fatalf("resolved prescription = %#v changes=%#v", exercise, session.Changes)
	}
}

func TestResolveStructuredPrescriptionRejectsPeerConflict(t *testing.T) {
	planID, sessionID, membershipID, segmentID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	session := pages.StructuredTrainingSession{ID: sessionID.String(), Segments: []pages.StructuredTrainingSegment{{ID: segmentID.String(), Modality: "WATER"}}}
	rows := []dbgen.ListTrainingVariationMatchesForManagerRow{
		{PlanID: planID, SessionID: sessionID, MembershipID: &membershipID, SubjectKind: dbgen.TrainingVariationSubjectKindSEGMENT, SubjectID: segmentID, Priority: 1},
		{PlanID: planID, SessionID: sessionID, MembershipID: &membershipID, SubjectKind: dbgen.TrainingVariationSubjectKindSEGMENT, SubjectID: segmentID, Priority: 1},
	}
	if !errors.Is(resolveStructuredPrescription(&session, rows, planID, sessionID, membershipID), errStructuredTrainingPublicationVariationConflict) {
		t.Fatal("same-priority variations must block publication")
	}
}

func TestApplyStructuredPrescriptionVariationOmitsOnlyTargetedElement(t *testing.T) {
	sessionID, segmentID, keepID, omitID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	session := pages.StructuredTrainingSession{ID: sessionID.String(), Modalities: []string{"WATER"}, Segments: []pages.StructuredTrainingSegment{{ID: segmentID.String(), Modality: "WATER", Blocks: []pages.StructuredTrainingBlock{{WaterSteps: []pages.StructuredWaterStep{{ID: keepID.String(), Name: "Aquecimento"}, {ID: omitID.String(), Name: "Série"}}}}}}}
	err := applyStructuredPrescriptionVariation(&session, dbgen.ListTrainingVariationMatchesForManagerRow{SubjectKind: dbgen.TrainingVariationSubjectKindWATERSTEP, SubjectID: omitID, SubjectLabel: "Série", Operation: dbgen.TrainingVariationOperationOMIT, ChangeSummary: "Retirar série"})
	if err != nil {
		t.Fatal(err)
	}
	steps := session.Segments[0].Blocks[0].WaterSteps
	if len(steps) != 1 || steps[0].ID != keepID.String() || len(session.Changes) != 1 {
		t.Fatalf("omit result = %#v changes=%#v", steps, session.Changes)
	}
}

func TestApplyStructuredPrescriptionVariationRecalculatesWaterOverride(t *testing.T) {
	segmentID, stepID := uuid.New(), uuid.New()
	session := pages.StructuredTrainingSession{ID: uuid.NewString(), Modalities: []string{"WATER"}, Segments: []pages.StructuredTrainingSegment{{ID: segmentID.String(), Modality: "WATER", Blocks: []pages.StructuredTrainingBlock{{WaterSteps: []pages.StructuredWaterStep{{ID: stepID.String(), Name: "500 m", Instructions: "Ritmo controlado", DurationSeconds: 120, DurationCertainty: "EXACT", DistanceMetres: 500, DistanceCertainty: "EXACT", Recovery: 60, Intensity: "R5"}}}}}}}
	row := dbgen.ListTrainingVariationMatchesForManagerRow{
		SubjectKind: dbgen.TrainingVariationSubjectKindWATERSTEP, SubjectID: stepID, SubjectLabel: "500 m", Operation: dbgen.TrainingVariationOperationOVERRIDE, ChangeSummary: "Reduzir para recuperação",
		Patch: []byte(`{"title":"300 m","instructions":"Técnica limpa","duration_seconds":90,"distance_metres":300,"recovery_seconds":45,"intensity_code":"R3"}`),
	}
	if err := applyStructuredPrescriptionVariation(&session, row); err != nil {
		t.Fatal(err)
	}
	step := session.Segments[0].Blocks[0].WaterSteps[0]
	if step.Name != "300 m" || step.Instructions != "Técnica limpa" || step.DurationSeconds != 90 || step.DistanceMetres != 300 || step.Recovery != 45 || step.Intensity != "R3" {
		t.Fatalf("water step=%#v", step)
	}
	if step.Prescription != "90 s (exato) · 0,3 km (exato)" || len(session.Changes) != 1 || session.Changes[0].Summary != "Reduzir para recuperação" {
		t.Fatalf("prescription=%q changes=%#v", step.Prescription, session.Changes)
	}
}

func TestApplyStructuredPrescriptionVariationChangesSegmentBlockAndGymExercise(t *testing.T) {
	segmentID, blockID, exerciseID, omittedExerciseID, omittedBlockID, removedID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	session := pages.StructuredTrainingSession{Segments: []pages.StructuredTrainingSegment{
		{ID: segmentID.String(), Title: "Ginásio", Modality: "GYM", Blocks: []pages.StructuredTrainingBlock{
			{ID: blockID.String(), Title: "Força", Instructions: "Controlado", Exercises: []pages.StructuredGymExercise{
				{ID: exerciseID.String(), Name: "Remada", Sets: 3, Repetitions: 10, Resistance: "40 kg"},
				{ID: omittedExerciseID.String(), Name: "Prancha", Sets: 3, Repetitions: 30},
			}},
			{ID: omittedBlockID.String(), Title: "A remover", Instructions: "Não publicar"},
		}},
		{ID: removedID.String(), Title: "Corrida", Modality: "RUN"},
	}}
	for _, row := range []dbgen.ListTrainingVariationMatchesForManagerRow{
		{SubjectKind: dbgen.TrainingVariationSubjectKindSEGMENT, SubjectID: segmentID, SubjectLabel: "Ginásio", Operation: dbgen.TrainingVariationOperationOVERRIDE, Patch: []byte(`{"title":"Potência","modality":"WATER"}`)},
		{SubjectKind: dbgen.TrainingVariationSubjectKindBLOCK, SubjectID: blockID, SubjectLabel: "Força", Operation: dbgen.TrainingVariationOperationOVERRIDE, Patch: []byte(`{"title":"Explosão","instructions":"Rápido"}`)},
		{SubjectKind: dbgen.TrainingVariationSubjectKindGYMEXERCISE, SubjectID: exerciseID, SubjectLabel: "Remada", Operation: dbgen.TrainingVariationOperationOVERRIDE, Patch: []byte(`{"title":"Remada alta","sets":4,"repetitions":6,"resistance":"50 kg"}`)},
		{SubjectKind: dbgen.TrainingVariationSubjectKindGYMEXERCISE, SubjectID: omittedExerciseID, SubjectLabel: "Prancha", Operation: dbgen.TrainingVariationOperationOMIT},
		{SubjectKind: dbgen.TrainingVariationSubjectKindBLOCK, SubjectID: omittedBlockID, SubjectLabel: "A remover", Operation: dbgen.TrainingVariationOperationOMIT},
		{SubjectKind: dbgen.TrainingVariationSubjectKindSEGMENT, SubjectID: removedID, SubjectLabel: "Corrida", Operation: dbgen.TrainingVariationOperationOMIT},
	} {
		if err := applyStructuredPrescriptionVariation(&session, row); err != nil {
			t.Fatal(err)
		}
	}
	if len(session.Segments) != 1 || session.Segments[0].Title != "Potência" || session.Segments[0].Modality != "WATER" {
		t.Fatalf("segments=%#v", session.Segments)
	}
	if len(session.Segments[0].Blocks) != 1 {
		t.Fatalf("blocks=%#v", session.Segments[0].Blocks)
	}
	block := session.Segments[0].Blocks[0]
	exercise := block.Exercises[0]
	if len(block.Exercises) != 1 || block.Title != "Explosão" || block.Instructions != "Rápido" || exercise.Name != "Remada alta" || exercise.Sets != 4 || exercise.Repetitions != 6 || exercise.Resistance != "50 kg" || exercise.Prescription != "4 séries · 6 repetições" {
		t.Fatalf("block=%#v exercise=%#v", block, exercise)
	}
	if len(session.Changes) != 6 || len(session.Modalities) != 1 || session.Modalities[0] != "WATER" {
		t.Fatalf("changes=%#v modalities=%#v", session.Changes, session.Modalities)
	}
}

func TestBuildStructuredPrescriptionInputsCreatesPrivateSnapshotAndSkipsUnknownSession(t *testing.T) {
	planID, sessionID, membershipID, athleteID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	week := pages.StructuredTrainingWeek{
		ID: planID.String(), Title: "M42", Description: "Transformação", Season: "2025/2026", DateRange: "15–21 junho", PlannedLoad: "70%",
		Sessions: []pages.StructuredTrainingSession{{ID: sessionID.String(), Title: "Água", Modalities: []string{"WATER"}}},
	}
	recipients := []dbgen.ListStructuredTrainingPublicationMembersRow{
		{SessionID: sessionID, MembershipID: membershipID, AthleteUserID: athleteID},
		{SessionID: uuid.New(), MembershipID: uuid.New(), AthleteUserID: uuid.New()},
	}

	inputs, err := buildStructuredPrescriptionInputs(planID, pages.StructuredTrainingAudience{GroupName: "Cadetes"}, week, recipients, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 1 || inputs[0].SessionID != sessionID || inputs[0].MembershipID != membershipID || inputs[0].AthleteUserID != athleteID || len(inputs[0].SnapshotSHA256) != 64 {
		t.Fatalf("inputs = %#v", inputs)
	}
	var snapshot structuredPrescriptionSnapshot
	if err := json.Unmarshal(inputs[0].Snapshot, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.SchemaVersion != structuredPrescriptionSchemaVersion || snapshot.PlanID != planID.String() || snapshot.GroupName != "Cadetes" || snapshot.PlannedLoad != "70%" || snapshot.Session.ID != sessionID.String() {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestStructuredPublishedTrainingGroupsCurrentAndHistoricRevisions(t *testing.T) {
	planID, firstSessionID, secondSessionID := uuid.New(), uuid.New(), uuid.New()
	publishedAt := pgtype.Timestamptz{Time: time.Date(2026, time.August, 14, 10, 30, 0, 0, time.UTC), Valid: true}
	snapshot := func(sessionID uuid.UUID, title string) []byte {
		encoded, err := json.Marshal(structuredPrescriptionSnapshot{
			SchemaVersion: structuredPrescriptionSchemaVersion,
			PlanID:        planID.String(), GroupName: "Cadetes", WeekTitle: "M42", WeekDescription: "Transformação", Season: "2025/2026", DateRange: "15–21 junho", PlannedLoad: "70%",
			Session: pages.StructuredTrainingSession{ID: sessionID.String(), Title: title},
		})
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	rows := []dbgen.ListTrainingPrescriptionsForViewerRow{
		{ID: uuid.New(), AthleteName: "Ana", Revision: 2, PublishedAt: publishedAt, PublishedByName: "Treinador", Snapshot: snapshot(firstSessionID, "Água"), IsCurrent: true, OutcomeStatus: "COMPLETED", ActualDurationMinutes: int32Ptr(72), PerceivedExertion: int16Ptr(7), RecoveryFeeling: int16Ptr(4), PerceptionNote: stringPtr("Boa sessão"), OutcomeUpdatedAt: publishedAt},
		{ID: uuid.New(), AthleteName: "Ana", Revision: 2, PublishedAt: publishedAt, PublishedByName: "Treinador", Snapshot: snapshot(secondSessionID, "Ginásio"), IsCurrent: true},
		{ID: uuid.New(), AthleteName: "Ana", Revision: 1, PublishedAt: publishedAt, PublishedByName: "Treinador", Snapshot: snapshot(firstSessionID, "Água anterior")},
		{ID: uuid.New(), AthleteName: "Ignorada", Snapshot: []byte(`{"schema_version":99}`)},
	}

	audiences := structuredPublishedTraining(rows, time.UTC)
	if len(audiences) != 2 || len(audiences[0].Weeks) != 1 || len(audiences[0].Weeks[0].Sessions) != 2 || len(audiences[1].Weeks) != 1 {
		t.Fatalf("audiences = %#v", audiences)
	}
	if audiences[0].GroupName != "Cadetes" || !strings.Contains(audiences[0].Scope, "revisão 2") || !strings.HasPrefix(audiences[1].Scope, "Histórico") {
		t.Fatalf("scopes = %q / %q", audiences[0].Scope, audiences[1].Scope)
	}
	if audiences[0].Weeks[0].PlannedLoad != "70%" {
		t.Fatalf("planned load = %q", audiences[0].Weeks[0].PlannedLoad)
	}
	if audiences[0].Weeks[0].Sessions[0].PrescriptionID != rows[0].ID.String() {
		t.Fatalf("prescription id = %q", audiences[0].Weeks[0].Sessions[0].PrescriptionID)
	}
	feedback := audiences[0].Weeks[0].Sessions[0]
	if feedback.Outcome != "COMPLETED" || feedback.ActualDuration != "72 min" || feedback.PerceivedEffort != "7/10" || feedback.RecoveryFeeling != "bem" || feedback.PerceptionNote != "Boa sessão" || feedback.FeedbackUpdatedAt == "" {
		t.Fatalf("feedback = %#v", feedback)
	}
}

func TestPrescriptionForSessionRedirectsWithoutExposingAthleteIdentity(t *testing.T) {
	prescriptionID, sessionID := uuid.New(), uuid.New()
	store := &structuredTrainingStoreStub{prescriptionLinks: []dbgen.ListTrainingPrescriptionLinksForSessionViewerRow{{ID: prescriptionID, AthleteName: "Atleta privado"}}}
	handler := StructuredTraining{Store: store, System: System{}}
	response := performStructuredTrainingRequest(t, CurrentUser{ID: uuid.New()}, http.MethodGet, "/treinos/prescricoes/sessoes/"+sessionID.String(), nil, "id", sessionID.String(), handler.PrescriptionForSession)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/treinos/prescricoes/"+prescriptionID.String() {
		t.Fatalf("response=%d location=%q", response.Code, response.Header().Get("Location"))
	}
	if strings.Contains(response.Body.String(), "Atleta privado") {
		t.Fatal("single-prescription redirect leaked the athlete name")
	}
}

func TestPrescriptionForSessionLetsGuardianChooseAuthorizedMinor(t *testing.T) {
	sessionID := uuid.New()
	now := pgtype.Timestamptz{Time: time.Date(2026, time.August, 14, 10, 0, 0, 0, time.UTC), Valid: true}
	store := &structuredTrainingStoreStub{prescriptionLinks: []dbgen.ListTrainingPrescriptionLinksForSessionViewerRow{
		{ID: uuid.New(), AthleteName: "Menor A", Revision: 2, PublishedAt: now},
		{ID: uuid.New(), AthleteName: "Menor B", Revision: 2, PublishedAt: now},
	}}
	handler := StructuredTraining{Store: store, System: System{}, Location: time.UTC}
	response := performStructuredTrainingRequest(t, CurrentUser{ID: uuid.New()}, http.MethodGet, "/treinos/prescricoes/sessoes/"+sessionID.String(), nil, "id", sessionID.String(), handler.PrescriptionForSession)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Menor A") || !strings.Contains(response.Body.String(), "Menor B") {
		t.Fatalf("response=%d body=%q", response.Code, response.Body.String())
	}
}

func TestPrescriptionDetailTreatsUnauthorizedAsNotFound(t *testing.T) {
	prescriptionID := uuid.New()
	store := &structuredTrainingStoreStub{prescriptionErr: pgx.ErrNoRows}
	handler := StructuredTraining{Store: store, System: System{}}
	response := performStructuredTrainingRequest(t, CurrentUser{ID: uuid.New()}, http.MethodGet, "/treinos/prescricoes/"+prescriptionID.String(), nil, "id", prescriptionID.String(), handler.PrescriptionDetail)
	if response.Code != http.StatusNotFound {
		t.Fatalf("response=%d", response.Code)
	}
}

func TestPrescriptionDetailRendersAuthorizedPublishedSnapshot(t *testing.T) {
	prescriptionID, sessionID, planID := uuid.New(), uuid.New(), uuid.New()
	snapshot, err := json.Marshal(structuredPrescriptionSnapshot{
		SchemaVersion: structuredPrescriptionSchemaVersion,
		PlanID:        planID.String(), GroupName: "Cadetes", WeekTitle: "M42", Season: "2025/2026", DateRange: "15–21 junho",
		Session: pages.StructuredTrainingSession{ID: sessionID.String(), Title: "Água R4", Modalities: []string{"WATER"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	store := &structuredTrainingStoreStub{prescriptionRow: dbgen.GetTrainingPrescriptionForViewerRow{
		ID: prescriptionID, SessionID: sessionID, AthleteUserID: uuid.New(), AthleteName: "Ana", PlanID: planID, PlanTitle: "M42",
		Revision: 3, PublishedAt: pgtype.Timestamptz{Time: time.Date(2026, time.August, 14, 10, 30, 0, 0, time.UTC), Valid: true}, PublishedByName: "Treinador", Snapshot: snapshot, IsCurrent: true,
		OutcomeStatus: "COMPLETED", ActualDurationMinutes: int32Ptr(71), PerceivedExertion: int16Ptr(8), RecoveryFeeling: int16Ptr(3), PerceptionNote: stringPtr("Vento lateral"), OutcomeUpdatedAt: pgtype.Timestamptz{Time: time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC), Valid: true},
	}}
	handler := StructuredTraining{Store: store, System: System{}, Location: time.UTC}
	response := performStructuredTrainingRequest(t, CurrentUser{ID: uuid.New()}, http.MethodGet, "/treinos/prescricoes/"+prescriptionID.String(), nil, "id", prescriptionID.String(), handler.PrescriptionDetail)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Água R4") || !strings.Contains(response.Body.String(), "revisão 3") || !strings.Contains(response.Body.String(), "Vento lateral") || !strings.Contains(response.Body.String(), "8/10") {
		t.Fatalf("response=%d body=%q", response.Code, response.Body.String())
	}
}

func TestStructuredTrainingPolicyAndDisplayHelpersFailClosed(t *testing.T) {
	programmeID, teamID := uuid.New(), uuid.New()
	foreignProgrammeID := uuid.New()
	coach := CurrentUser{CoachProgrammeIDs: map[uuid.UUID]bool{programmeID: true}, CoachTeamIDs: map[uuid.UUID]bool{teamID: true}}
	if !structuredScopeAllowed(coach, &programmeID, nil) || !structuredScopeAllowed(coach, nil, &teamID) {
		t.Fatal("assigned coaching scopes were rejected")
	}
	if structuredScopeAllowed(coach, nil, nil) || structuredScopeAllowed(coach, &programmeID, &teamID) || structuredScopeAllowed(coach, &foreignProgrammeID, nil) {
		t.Fatal("ambiguous or foreign scope was accepted")
	}
	if !structuredScopeAllowed(CurrentUser{IsAdmin: true}, &programmeID, nil) {
		t.Fatal("administrator scope was rejected")
	}

	for _, kind := range []dbgen.TrainingVariationSubjectKind{dbgen.TrainingVariationSubjectKindSEGMENT, dbgen.TrainingVariationSubjectKindBLOCK, dbgen.TrainingVariationSubjectKindWATERSTEP, dbgen.TrainingVariationSubjectKindGYMEXERCISE} {
		if !validTrainingVariationSubject(kind) {
			t.Errorf("subject %q rejected", kind)
		}
	}
	for _, operation := range []dbgen.TrainingVariationOperation{dbgen.TrainingVariationOperationOMIT, dbgen.TrainingVariationOperationREPLACE, dbgen.TrainingVariationOperationADD, dbgen.TrainingVariationOperationOVERRIDE} {
		if !validTrainingVariationOperation(operation) {
			t.Errorf("operation %q rejected", operation)
		}
	}
	if validTrainingVariationSubject(dbgen.TrainingVariationSubjectKind("UNKNOWN")) || validTrainingVariationOperation(dbgen.TrainingVariationOperation("UNKNOWN")) {
		t.Fatal("unknown variation value accepted")
	}

	for input, want := range map[int64]string{59: "59 s", 61: "1 min 1 s", 3600: "1 h", 3660: "61 min"} {
		if got := formatTrainingDuration(input); got != want {
			t.Errorf("formatTrainingDuration(%d)=%q, want %q", input, got, want)
		}
	}
	if trainingRoutineKindName(dbgen.TrainingRoutineKindBLOCK) != "um bloco" || trainingRoutineKindName(dbgen.TrainingRoutineKindSESSION) != "uma sessão" {
		t.Fatal("routine kind labels are not readable")
	}
}

func TestStructuredTrainingAuthoringEnumsAcceptOnlySupportedValues(t *testing.T) {
	for _, objective := range []dbgen.TrainingObjective{
		dbgen.TrainingObjectiveMOBILITY, dbgen.TrainingObjectiveACTIVATION, dbgen.TrainingObjectiveMAXSTRENGTHHYPERTROPHY,
		dbgen.TrainingObjectiveMAXSTRENGTHNEURAL, dbgen.TrainingObjectiveEXPLOSIVESTRENGTH, dbgen.TrainingObjectiveSTRENGTHENDURANCE,
		dbgen.TrainingObjectiveTECHNIQUE, dbgen.TrainingObjectiveCORE, dbgen.TrainingObjectiveCUSTOM,
	} {
		if !validTrainingObjective(objective) {
			t.Errorf("objective %q rejected", objective)
		}
	}
	for _, kind := range []dbgen.GymResistanceKind{
		dbgen.GymResistanceKindKILOGRAMS, dbgen.GymResistanceKindPERCENT1RM, dbgen.GymResistanceKindBODYWEIGHT,
		dbgen.GymResistanceKindBAND, dbgen.GymResistanceKindRPE, dbgen.GymResistanceKindRIR, dbgen.GymResistanceKindCOACHINSTRUCTION,
	} {
		if !validGymResistanceKind(kind) {
			t.Errorf("resistance kind %q rejected", kind)
		}
	}
	for _, method := range []dbgen.WaterWorkMethod{
		dbgen.WaterWorkMethodCONTINUOUS, dbgen.WaterWorkMethodINTERVALS, dbgen.WaterWorkMethodFARTLEK, dbgen.WaterWorkMethodTECHNIQUE,
		dbgen.WaterWorkMethodSTARTS, dbgen.WaterWorkMethodRACESIMULATION, dbgen.WaterWorkMethodTACTICALDRILL, dbgen.WaterWorkMethodCUSTOM,
	} {
		if !validWaterWorkMethod(method) {
			t.Errorf("water method %q rejected", method)
		}
	}
	if validTrainingObjective(dbgen.TrainingObjective("UNKNOWN")) || validGymResistanceKind(dbgen.GymResistanceKind("UNKNOWN")) || validWaterWorkMethod(dbgen.WaterWorkMethod("UNKNOWN")) {
		t.Fatal("unknown authoring enum accepted")
	}
}

func TestStructuredTrainingDisplayLabelsDescribeEverySupportedValue(t *testing.T) {
	for raw, want := range map[string]string{
		"WATER": "Água", "GYM": "Ginásio", "RUN": "Corrida", "BIKE": "Bicicleta", "ERGOMETER": "Ergómetro", "FLEXIBILITY": "Flexibilidade e mobilidade", "SPORTS_GAMES": "Jogos desportivos", "OTHER": "Outra",
	} {
		if got := structuredModalityName(raw); got != want {
			t.Errorf("modality %q = %q, want %q", raw, got, want)
		}
	}
	for raw, want := range map[string]string{
		"MOBILITY": "Mobilidade", "ACTIVATION": "Ativação", "MAX_STRENGTH_HYPERTROPHY": "Força máxima e hipertrofia", "MAX_STRENGTH_NEURAL": "Força máxima neural", "EXPLOSIVE_STRENGTH": "Força explosiva", "STRENGTH_ENDURANCE": "Força-resistência", "TECHNIQUE": "Técnica", "CORE": "Core", "CUSTOM": "Personalizado",
	} {
		if got := structuredTrainingObjectiveName(raw); got != want {
			t.Errorf("objective %q = %q, want %q", raw, got, want)
		}
	}
	for raw, want := range map[string]string{
		"CONTINUOUS": "Contínuo", "INTERVALS": "Intervalos", "FARTLEK": "Fartlek", "TECHNIQUE": "Técnica", "STARTS": "Partidas", "RACE_SIMULATION": "Simulação de prova", "TACTICAL_DRILL": "Exercício técnico-tático", "CUSTOM": "Outro método",
	} {
		if got := waterMethodLabel(raw); got != want {
			t.Errorf("method %q = %q, want %q", raw, got, want)
		}
	}
	if trainingRoutineKindName(dbgen.TrainingRoutineKindSEGMENT) != "um segmento" || trainingMeasureCertaintyLabel("ESTIMATED") != "estimado" || paddlingCraftLabel("CANOE") != "Canoa" {
		t.Fatal("supported structured-training labels are not readable")
	}
}

func TestParseGymExerciseAcceptsEachResistanceModeAtItsBoundary(t *testing.T) {
	for name, values := range map[string]url.Values{
		"kilograms":         {"exercise_name": {"Remada"}, "sets": {"3"}, "repetitions": {"5"}, "resistance_kind": {"KILOGRAMS"}, "resistance_value": {"0.01"}},
		"percent one rm":    {"exercise_name": {"Remada"}, "sets": {"3"}, "repetitions": {"5"}, "resistance_kind": {"PERCENT_1RM"}, "resistance_value": {"200"}},
		"rpe":               {"exercise_name": {"Remada"}, "sets": {"3"}, "repetitions": {"5"}, "resistance_kind": {"RPE"}, "resistance_value": {"10"}},
		"rir":               {"exercise_name": {"Remada"}, "sets": {"3"}, "repetitions": {"5"}, "resistance_kind": {"RIR"}, "resistance_value": {"0"}},
		"body weight":       {"exercise_name": {"Remada"}, "sets": {"3"}, "repetitions": {"5"}, "resistance_kind": {"BODY_WEIGHT"}},
		"band instruction":  {"exercise_name": {"Remada"}, "sets": {"3"}, "repetitions": {"5"}, "resistance_kind": {"BAND"}, "resistance_text": {"banda média"}},
		"coach instruction": {"exercise_name": {"Remada"}, "sets": {"3"}, "repetitions": {"5"}, "resistance_kind": {"COACH_INSTRUCTION"}, "resistance_text": {"carga guiada"}},
	} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(values.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if _, err := parseGymExercise(r); err != nil {
				t.Fatalf("valid %s exercise rejected: %v", name, err)
			}
		})
	}
}

func int16Ptr(value int16) *int16 { return &value }
