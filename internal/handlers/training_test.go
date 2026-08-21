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

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestTrainingScopeParsesOptionalIDs(t *testing.T) {
	programmeID, teamID := uuid.New(), uuid.New()
	request := httptest.NewRequest(http.MethodPost, "/treinos/planos", strings.NewReader("programme_id="+programmeID.String()+"&team_id="+teamID.String()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := request.ParseForm(); err != nil {
		t.Fatal(err)
	}
	programme, team, err := trainingScope(request)
	if err != nil || programme == nil || team == nil || *programme != programmeID || *team != teamID {
		t.Fatalf("scope = %v, %v, %v", programme, team, err)
	}
}

func TestTrainingScopeRequiresEverySelectedCoachScope(t *testing.T) {
	programmeID, teamID := uuid.New(), uuid.New()
	training := Training{}
	coach := CurrentUser{CoachProgrammeIDs: map[uuid.UUID]bool{programmeID: true}, CoachTeamIDs: map[uuid.UUID]bool{}}
	if !training.canUseScope(coach, &programmeID, nil) {
		t.Fatal("programme grant should be accepted")
	}
	if training.canUseScope(coach, &programmeID, &teamID) {
		t.Fatal("ungranted team must not be widened by a programme grant")
	}
	if training.canUseScope(coach, nil, nil) {
		t.Fatal("empty coach scope must be rejected")
	}
}

func TestCompetitionDocumentModalityNeedsAnAudienceScope(t *testing.T) {
	modalityID := uuid.New()
	reviewed := time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC)
	if validCompetitionDocumentInput("Caderno", "https://example.org/caderno.pdf", "Federação", reviewed, nil, &modalityID, nil, nil) {
		t.Fatal("a modality document without programme or team must be rejected")
	}
	programmeID := uuid.New()
	if !validCompetitionDocumentInput("Caderno", "https://example.org/caderno.pdf", "Federação", reviewed, nil, &modalityID, &programmeID, nil) {
		t.Fatal("a scoped modality document should be valid")
	}
}

func TestTrainingFeedbackDisplayHelpersCoverEveryRecoveryScaleValue(t *testing.T) {
	for value, want := range map[int16]string{1: "muito mal", 2: "mal", 3: "razoavelmente", 4: "bem", 5: "muito bem", 0: ""} {
		if got := trainingFeelingText(value); got != want {
			t.Errorf("trainingFeelingText(%d)=%q, want %q", value, got, want)
		}
	}
	for metres, want := range map[int32]string{0: "0", 10: "0.01", 12500: "12.5", 12340: "12.34", 12000: "12"} {
		if got := kilometreInput(metres); got != want {
			t.Errorf("kilometreInput(%d)=%q, want %q", metres, got, want)
		}
	}
}

func TestTrainingOutcomeRequiresReplacementDetails(t *testing.T) {
	replacementID := uuid.New()
	if !validTrainingOutcome("COMPLETED", nil, "") || !validTrainingOutcome("MISSED", nil, "") {
		t.Fatal("completed and missed outcomes should not need replacement details")
	}
	if !validTrainingOutcome("REPLACED", &replacementID, "Condições meteorológicas") {
		t.Fatal("a replacement requires a session and short reason")
	}
	if validTrainingOutcome("REPLACED", nil, "Condições meteorológicas") || validTrainingOutcome("REPLACED", &replacementID, "") {
		t.Fatal("replacement details must be complete")
	}
	if validTrainingOutcome("COMPLETED", &replacementID, "irrelevante") || validTrainingOutcome("UNKNOWN", nil, "") {
		t.Fatal("only the supported outcome shapes should be accepted")
	}
}

func TestManagedTrainingPlansGroupsSessionsAndKeepsEmptyPlans(t *testing.T) {
	planOne, planTwo, sessionID := uuid.New(), uuid.New(), uuid.New()
	modality := "Remo"
	starts := time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC)
	ends := starts.Add(90 * time.Minute)
	plans := managedTrainingPlans([]dbgen.ListTrainingPlansForAuthoringRow{
		{PlanID: planOne, PlanTitle: "Plano de verão", PlanDescription: "Preparação", SessionID: &sessionID, SessionTitle: stringPtr("Técnica"), SessionDescription: stringPtr("Saída de água"), StartsAt: pgtype.Timestamptz{Time: starts, Valid: true}, EndsAt: pgtype.Timestamptz{Time: ends, Valid: true}, ModalityName: &modality},
		{PlanID: planTwo, PlanTitle: "Plano sem sessões", PlanDescription: "A aguardar calendário"},
	}, time.UTC)
	if len(plans) != 2 || len(plans[0].Sessions) != 1 || len(plans[1].Sessions) != 0 {
		t.Fatalf("plans = %#v", plans)
	}
	if plans[0].Sessions[0].When != "27/07/2026 09:00 - 10:30" || plans[0].Sessions[0].Modality != modality {
		t.Fatalf("session = %#v", plans[0].Sessions[0])
	}
}

func TestManagedTrainingPlansExposeCancellationMetadataWithoutEditControl(t *testing.T) {
	planID, sessionID := uuid.New(), uuid.New()
	status, reason, actor := "CANCELLED", "Cheia prevista", "Treinadora"
	starts := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	rows := []dbgen.ListTrainingPlansForAuthoringRow{{PlanID: planID, PlanTitle: "Plano", SessionID: &sessionID, SessionTitle: stringPtr("Sessão"), SessionDescription: stringPtr(""), StartsAt: pgtype.Timestamptz{Time: starts, Valid: true}, EndsAt: pgtype.Timestamptz{Time: starts.Add(time.Hour), Valid: true}, Status: &status, CancellationReason: &reason, CancelledAt: pgtype.Timestamptz{Time: starts.Add(-24 * time.Hour), Valid: true}, CancelledByName: &actor}}
	plans := managedTrainingPlansAt(rows, time.UTC, starts.Add(-48*time.Hour))
	session := plans[0].Sessions[0]
	if !session.Cancelled || session.Editable || session.CancellationReason != reason || session.CancelledBy != actor || session.CancelledAt == "" {
		t.Fatalf("cancelled session = %#v", session)
	}
}

func TestValidateTrainingSessionEditRequiresFreshWellFormedInput(t *testing.T) {
	planID := uuid.New()
	expected := time.Date(2026, 8, 10, 10, 0, 0, 123, time.UTC)
	request := httptest.NewRequest(http.MethodPost, "/admin/treinos/sessoes/id", strings.NewReader("plan_id="+planID.String()+"&title=Sessao+tecnica&starts_at=2026-08-20T09%3A00&ends_at=2026-08-20T10%3A00&expected_updated_at="+expected.Format(time.RFC3339Nano)))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := request.ParseForm(); err != nil {
		t.Fatal(err)
	}
	form, starts, ends, _, gotExpected := (Training{Location: time.UTC}).validateTrainingSessionEdit(request)
	if !form.Errors.Empty() || !ends.After(starts) || !gotExpected.Equal(expected) {
		t.Fatalf("form = %#v, starts = %v, ends = %v, expected = %v", form, starts, ends, gotExpected)
	}

	request = httptest.NewRequest(http.MethodPost, "/admin/treinos/sessoes/id", strings.NewReader("plan_id=invalid&title=x&starts_at=bad&ends_at=bad&expected_updated_at=stale"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := request.ParseForm(); err != nil {
		t.Fatal(err)
	}
	form, _, _, _, _ = (Training{Location: time.UTC}).validateTrainingSessionEdit(request)
	for _, key := range []string{"plan_id", "title", "starts_at", "ends_at", "state"} {
		if form.Errors[key] == "" {
			t.Errorf("missing %s error: %#v", key, form.Errors)
		}
	}
}

func TestManagedTrainingPlansPageNumber(t *testing.T) {
	for _, test := range []struct {
		input string
		want  int
	}{
		{"", 1}, {"0", 1}, {"invalid", 1}, {"10001", 1}, {"2", 2},
	} {
		if got := managedTrainingPlansPageNumber(test.input); got != test.want {
			t.Errorf("managedTrainingPlansPageNumber(%q) = %d, want %d", test.input, got, test.want)
		}
	}
}

func TestManagedTrainingPlansPageURL(t *testing.T) {
	if got, want := managedTrainingPlansPageURL(2), "/admin/treinos?managed_page=2"; got != want {
		t.Errorf("managedTrainingPlansPageURL(2) = %q, want %q", got, want)
	}
}

func TestTrainingSessionEditDeepLinkMetadataUsesPlanningWorkspace(t *testing.T) {
	sessionID := uuid.New()
	h := Training{Store: &trainingEditStore{}}
	r := httptest.NewRequest(http.MethodGet, "/admin/treinos/sessoes/"+sessionID.String()+"/editar", nil)
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: uuid.New(), Name: "Treinadora"}))
	w := httptest.NewRecorder()
	h.renderSessionEdit(w, r, http.StatusOK, sessionID, pages.TrainingSessionForm{Title: "Técnica de água"}, "", "", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Planear treinos") || !strings.Contains(w.Body.String(), "Coordenação · Treinos") || !strings.Contains(w.Body.String(), `aria-current="page">Editar sessão`) {
		t.Fatalf("response = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestTrainingSessionCancelTaskNamesTheSessionAndPreservesVersion(t *testing.T) {
	sessionID := uuid.New()
	starts := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	updated := starts.Add(-time.Hour)
	h := Training{Location: time.UTC}
	r := httptest.NewRequest(http.MethodGet, "/admin/treinos/sessoes/"+sessionID.String()+"/cancelar", nil)
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: uuid.New(), Name: "Treinadora"}))
	w := httptest.NewRecorder()

	h.renderSessionCancel(w, r, http.StatusOK, sessionID, dbgen.GetTrainingSessionForEditRow{ID: sessionID, Title: "Técnica de água", StartsAt: pgtype.Timestamptz{Time: starts, Valid: true}, EndsAt: pgtype.Timestamptz{Time: starts.Add(time.Hour), Valid: true}, UpdatedAt: pgtype.Timestamptz{Time: updated, Valid: true}}, "", "", validation.FieldErrors{})

	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Técnica de água") || !strings.Contains(w.Body.String(), updated.Format(time.RFC3339Nano)) {
		t.Fatalf("response = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestParseKilometresUsesExactMetres(t *testing.T) {
	tests := []struct {
		value string
		want  *int32
		valid bool
	}{
		{value: "", valid: true},
		{value: "0.01", want: int32Ptr(10), valid: true},
		{value: "12.5", want: int32Ptr(12500), valid: true},
		{value: "12,34", want: int32Ptr(12340), valid: true},
		{value: "200.00", want: int32Ptr(200000), valid: true},
		{value: "0", valid: false},
		{value: "-1", valid: false},
		{value: "1.234", valid: false},
		{value: "200.01", valid: false},
		{value: "1,2.3", valid: false},
	}
	for _, test := range tests {
		got, err := parseKilometres(test.value)
		if (err == nil) != test.valid {
			t.Errorf("parseKilometres(%q) error = %v", test.value, err)
			continue
		}
		if test.valid && !equalInt32Pointers(got, test.want) {
			t.Errorf("parseKilometres(%q) = %v, want %v", test.value, got, test.want)
		}
	}
	if got := formatKilometres(12340); got != "12,34 km" {
		t.Fatalf("formatKilometres = %q", got)
	}
}

func TestReportTrainingOutcomePersistsOnlyCompletedDistance(t *testing.T) {
	sessionID := uuid.New()
	userID := uuid.New()
	store := &trainingOutcomeStore{saveRows: 1}
	training := Training{Store: store}
	request := httptest.NewRequest(http.MethodPost, "/treinos/sessoes/resultados", strings.NewReader("session_id="+sessionID.String()+"&status=COMPLETED&distance_km=12.34&actual_duration_minutes=75&perceived_exertion=7&recovery_feeling=4&perception_note=Boa+sessao&subject_user_id="+uuid.NewString()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request = request.WithContext(context.WithValue(request.Context(), currentUserKey{}, CurrentUser{ID: userID}))
	response := httptest.NewRecorder()
	training.ReportOutcome(response, request)
	if response.Code != http.StatusSeeOther || store.saveParams.UserID != userID || store.saveParams.DistanceMetres == nil || *store.saveParams.DistanceMetres != 12340 || store.saveParams.ActualDurationMinutes == nil || *store.saveParams.ActualDurationMinutes != 75 || store.saveParams.PerceivedExertion == nil || *store.saveParams.PerceivedExertion != 7 || store.saveParams.RecoveryFeeling == nil || *store.saveParams.RecoveryFeeling != 4 || store.saveParams.PerceptionNote == nil || *store.saveParams.PerceptionNote != "Boa sessao" {
		t.Fatalf("response = %d, params = %+v", response.Code, store.saveParams)
	}

	store.saveParams = dbgen.SaveTrainingSessionOutcomeParams{}
	request = httptest.NewRequest(http.MethodPost, "/treinos/sessoes/resultados", strings.NewReader("session_id="+sessionID.String()+"&status=MISSED&distance_km=2"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request = request.WithContext(context.WithValue(request.Context(), currentUserKey{}, CurrentUser{ID: userID}))
	response = httptest.NewRecorder()
	training.ReportOutcome(response, request)
	if response.Code != http.StatusUnprocessableEntity || store.saveParams.SessionID != uuid.Nil {
		t.Fatalf("invalid response = %d, params = %+v", response.Code, store.saveParams)
	}
}

func TestUpdateTrainingFeedbackRequiresOwnFreshCompletedOutcome(t *testing.T) {
	store := &trainingOutcomeStore{updateRows: 1}
	training := Training{Store: store}
	userID, sessionID := uuid.New(), uuid.New()
	request := httptest.NewRequest(http.MethodPost, "/treinos/sessoes/feedback", strings.NewReader("session_id="+sessionID.String()+"&expected_version=3&distance_km=7.5&actual_duration_minutes=62&perceived_exertion=6&recovery_feeling=3&perception_note=Corrente+forte&subject_user_id="+uuid.NewString()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request = request.WithContext(context.WithValue(request.Context(), currentUserKey{}, CurrentUser{ID: userID}))
	response := httptest.NewRecorder()
	training.UpdateFeedback(response, request)
	if response.Code != http.StatusSeeOther || store.updateParams.UserID != userID || store.updateParams.ExpectedVersion != 3 || store.updateParams.DistanceMetres == nil || *store.updateParams.DistanceMetres != 7500 || store.updateParams.ActualDurationMinutes == nil || *store.updateParams.ActualDurationMinutes != 62 || store.updateParams.PerceivedExertion == nil || *store.updateParams.PerceivedExertion != 6 || store.updateParams.RecoveryFeeling == nil || *store.updateParams.RecoveryFeeling != 3 || store.updateParams.PerceptionNote == nil || *store.updateParams.PerceptionNote != "Corrente forte" {
		t.Fatalf("response = %d, params = %+v", response.Code, store.updateParams)
	}
}

func TestUpdateTrainingFeedbackRejectsInvalidScalesAndStaleVersion(t *testing.T) {
	userID, sessionID := uuid.New(), uuid.New()
	for _, values := range []string{
		"expected_version=1&perceived_exertion=11",
		"expected_version=1&recovery_feeling=0",
		"expected_version=1&actual_duration_minutes=2147483648",
		"expected_version=1&perceived_exertion=32768",
		"expected_version=0&perceived_exertion=5",
	} {
		store := &trainingOutcomeStore{updateRows: 1}
		request := httptest.NewRequest(http.MethodPost, "/treinos/sessoes/feedback", strings.NewReader("session_id="+sessionID.String()+"&"+values))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request = request.WithContext(context.WithValue(request.Context(), currentUserKey{}, CurrentUser{ID: userID}))
		response := httptest.NewRecorder()
		(Training{Store: store}).UpdateFeedback(response, request)
		if response.Code != http.StatusUnprocessableEntity || store.updateParams.SessionID != uuid.Nil {
			t.Fatalf("values=%q response=%d params=%+v", values, response.Code, store.updateParams)
		}
	}

	store := &trainingOutcomeStore{updateRows: 0}
	request := httptest.NewRequest(http.MethodPost, "/treinos/sessoes/feedback", strings.NewReader("session_id="+sessionID.String()+"&expected_version=2&perceived_exertion=5"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request = request.WithContext(context.WithValue(request.Context(), currentUserKey{}, CurrentUser{ID: userID}))
	response := httptest.NewRecorder()
	(Training{Store: store}).UpdateFeedback(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("stale response = %d", response.Code)
	}
}

func TestTrainingOutcomeAndFeedbackMapPersistenceFailuresWithoutReportingSuccess(t *testing.T) {
	userID, sessionID := uuid.New(), uuid.New()
	for _, tc := range []struct {
		name    string
		handler func(Training, http.ResponseWriter, *http.Request)
		values  string
		store   *trainingOutcomeStore
	}{
		{"outcome", Training.ReportOutcome, "session_id=" + sessionID.String() + "&status=COMPLETED", &trainingOutcomeStore{saveErr: errors.New("database unavailable")}},
		{"feedback", Training.UpdateFeedback, "session_id=" + sessionID.String() + "&expected_version=1", &trainingOutcomeStore{updateErr: errors.New("database unavailable")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/treinos", strings.NewReader(tc.values))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request = request.WithContext(context.WithValue(request.Context(), currentUserKey{}, CurrentUser{ID: userID}))
			response := httptest.NewRecorder()
			tc.handler(Training{Store: tc.store, System: System{}}, response, request)
			if response.Code != http.StatusInternalServerError {
				t.Fatalf("response=%d", response.Code)
			}
		})
	}
}

func TestTrainingIndexRendersAthleteSessionsAndCancelledState(t *testing.T) {
	userID, sessionID := uuid.New(), uuid.New()
	modality, reason := "Velocidade", "Vento forte"
	distance, duration := int32(12500), int32(75)
	exertion, feeling := int16(7), int16(4)
	starts := time.Date(2026, 9, 4, 9, 0, 0, 0, time.UTC)
	store := &trainingWorkflowStore{athleteSessions: []dbgen.ListTrainingSessionsForAthleteRow{{
		ID: sessionID, PlanTitle: "Preparação", Title: "Série", Description: "500 m", StartsAt: pgtype.Timestamptz{Time: starts, Valid: true}, EndsAt: pgtype.Timestamptz{Time: starts.Add(time.Hour), Valid: true}, ModalityName: &modality, Status: "CANCELLED", CancellationReason: &reason, OutcomeStatus: "COMPLETED", DistanceMetres: &distance, ActualDurationMinutes: &duration, PerceivedExertion: &exertion, RecoveryFeeling: &feeling,
	}}}
	r := httptest.NewRequest(http.MethodGet, "/treinos", nil)
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: userID, Name: "Atleta", EmailVerified: true}))
	w := httptest.NewRecorder()
	(Training{Store: store, Location: time.UTC, Now: func() time.Time { return starts }}).Index(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Série") || !strings.Contains(w.Body.String(), "Cancelada") || !strings.Contains(w.Body.String(), "Vento forte") {
		t.Fatalf("response = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestTrainingAuthoringCreatesPlanSessionAndRendersEditableSession(t *testing.T) {
	userID, planID, sessionID, programmeID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	starts := time.Date(2026, 9, 4, 9, 0, 0, 0, time.UTC)
	store := &trainingWorkflowStore{editSession: dbgen.GetTrainingSessionForEditRow{ID: sessionID, PlanID: planID, Title: "Técnica", Description: "Saída", StartsAt: pgtype.Timestamptz{Time: starts, Valid: true}, EndsAt: pgtype.Timestamptz{Time: starts.Add(time.Hour), Valid: true}, Status: "ACTIVE", UpdatedAt: pgtype.Timestamptz{Time: starts, Valid: true}}, plans: []dbgen.ListTrainingPlansForCoachRow{{ID: planID, Title: "Plano"}}}
	admin := CurrentUser{ID: userID, Name: "Admin", IsAdmin: true, EmailVerified: true}

	planRequest := httptest.NewRequest(http.MethodPost, "/admin/treinos/planos", strings.NewReader("title=Plano+novo&description=Preparação&programme_id="+programmeID.String()))
	planRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	planRequest = planRequest.WithContext(context.WithValue(planRequest.Context(), currentUserKey{}, admin))
	planResponse := httptest.NewRecorder()
	(Training{Store: store}).CreatePlan(planResponse, planRequest)
	if planResponse.Code != http.StatusSeeOther || store.createdPlan.Title != "Plano novo" || store.createdPlan.ProgrammeID == nil || *store.createdPlan.ProgrammeID != programmeID {
		t.Fatalf("plan response=%d params=%+v", planResponse.Code, store.createdPlan)
	}

	sessionRequest := httptest.NewRequest(http.MethodPost, "/admin/treinos/sessoes", strings.NewReader("plan_id="+planID.String()+"&title=Série&description=500m&starts_at=2026-09-04T09%3A00&ends_at=2026-09-04T10%3A00"))
	sessionRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	sessionRequest = sessionRequest.WithContext(context.WithValue(sessionRequest.Context(), currentUserKey{}, admin))
	sessionResponse := httptest.NewRecorder()
	(Training{Store: store, Location: time.UTC}).CreateSession(sessionResponse, sessionRequest)
	if sessionResponse.Code != http.StatusSeeOther || store.createdSession.PlanID != planID || store.createdSession.Title != "Série" {
		t.Fatalf("session response=%d params=%+v", sessionResponse.Code, store.createdSession)
	}

	editRequest := httptest.NewRequest(http.MethodGet, "/admin/treinos/sessoes/"+sessionID.String()+"/editar", nil)
	editRequest.SetPathValue("id", sessionID.String())
	editRequest = editRequest.WithContext(context.WithValue(editRequest.Context(), currentUserKey{}, admin))
	editResponse := httptest.NewRecorder()
	(Training{Store: store, Location: time.UTC, Now: func() time.Time { return starts.Add(-time.Hour) }}).EditSession(editResponse, editRequest)
	if editResponse.Code != http.StatusOK || !strings.Contains(editResponse.Body.String(), "Técnica") {
		t.Fatalf("edit response=%d body=%s", editResponse.Code, editResponse.Body.String())
	}
}

func TestTrainingPlanCreationAndAthleteIndexMapPersistenceFailures(t *testing.T) {
	actorID, programmeID := uuid.New(), uuid.New()
	t.Run("plan write failure", func(t *testing.T) {
		store := &trainingWorkflowStore{createPlanErr: errors.New("database unavailable")}
		values := url.Values{"title": {"Plano de competição"}, "description": {"Preparação semanal"}, "programme_id": {programmeID.String()}}
		response := performTrainingRequest(t, CurrentUser{ID: actorID, IsAdmin: true}, http.MethodPost, "/admin/treinos/planos", values, "", "", (Training{Store: store, Location: time.UTC}).CreatePlan)
		if response.Code != http.StatusInternalServerError || store.createdPlan.ProgrammeID == nil || *store.createdPlan.ProgrammeID != programmeID {
			t.Fatalf("response=%d params=%#v", response.Code, store.createdPlan)
		}
	})

	t.Run("athlete session read failure", func(t *testing.T) {
		store := &trainingWorkflowStore{athleteSessionsErr: errors.New("database unavailable")}
		request := httptest.NewRequest(http.MethodGet, "/treinos", nil)
		request = request.WithContext(context.WithValue(request.Context(), currentUserKey{}, CurrentUser{ID: actorID}))
		response := httptest.NewRecorder()
		(Training{Store: store, Location: time.UTC}).Index(response, request)
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("response=%d", response.Code)
		}
	})
}

func TestTrainingCancellationRejectsUnconfirmedOrStaleRequestsBeforeMutation(t *testing.T) {
	userID, planID, sessionID := uuid.New(), uuid.New(), uuid.New()
	starts := time.Date(2026, 9, 8, 9, 0, 0, 0, time.UTC)
	store := &trainingWorkflowStore{editSession: dbgen.GetTrainingSessionForEditRow{ID: sessionID, PlanID: planID, Title: "Técnica", StartsAt: pgtype.Timestamptz{Time: starts, Valid: true}, EndsAt: pgtype.Timestamptz{Time: starts.Add(time.Hour), Valid: true}, Status: "ACTIVE", UpdatedAt: pgtype.Timestamptz{Time: starts.Add(-time.Hour), Valid: true}}, plans: []dbgen.ListTrainingPlansForCoachRow{{ID: planID, Title: "Plano"}}}
	h := Training{Store: store, Location: time.UTC}
	for _, body := range []string{"cancellation_reason=x", "cancellation_reason=Cheia+prevista&confirm_cancellation=yes&expected_updated_at=stale"} {
		r := httptest.NewRequest(http.MethodPost, "/admin/treinos/sessoes/"+sessionID.String()+"/cancelar", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.SetPathValue("id", sessionID.String())
		r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: userID, IsAdmin: true}))
		w := httptest.NewRecorder()
		h.CancelSession(w, r)
		if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "Corrija os seguintes campos") {
			t.Fatalf("body=%q response=%d output=%s", body, w.Code, w.Body.String())
		}
	}
}

func TestTrainingCancellationPersistsConfirmedFutureSession(t *testing.T) {
	userID, planID, sessionID := uuid.New(), uuid.New(), uuid.New()
	starts := time.Date(2026, 9, 8, 9, 0, 0, 0, time.UTC)
	updated := starts.Add(-time.Hour)
	store := &trainingWorkflowStore{editSession: dbgen.GetTrainingSessionForEditRow{ID: sessionID, PlanID: planID, Title: "Técnica", StartsAt: pgtype.Timestamptz{Time: starts, Valid: true}, EndsAt: pgtype.Timestamptz{Time: starts.Add(time.Hour), Valid: true}, Status: "ACTIVE", UpdatedAt: pgtype.Timestamptz{Time: updated, Valid: true}}}
	now := starts.Add(-24 * time.Hour)
	h := Training{Store: store, Location: time.UTC, Now: func() time.Time { return now }}
	r := httptest.NewRequest(http.MethodPost, "/admin/treinos/sessoes/"+sessionID.String()+"/cancelar", strings.NewReader("cancellation_reason=Cheia+prevista&confirm_cancellation=yes&expected_updated_at="+updated.Format(time.RFC3339Nano)))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetPathValue("id", sessionID.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: userID, IsAdmin: true}))
	w := httptest.NewRecorder()
	h.CancelSession(w, r)
	if w.Code != http.StatusSeeOther || store.cancelled.ID != sessionID || store.cancelled.CancelledByID == nil || *store.cancelled.CancelledByID != userID || store.cancelled.CancellationReason == nil || *store.cancelled.CancellationReason != "Cheia prevista" {
		t.Fatalf("response=%d cancellation=%+v", w.Code, store.cancelled)
	}
}

func TestTrainingSessionAuthoringAndCancellationMapPersistenceFailures(t *testing.T) {
	userID, planID, sessionID := uuid.New(), uuid.New(), uuid.New()
	starts := time.Date(2026, 9, 8, 9, 0, 0, 0, time.UTC)
	updated := starts.Add(-time.Hour)
	base := dbgen.GetTrainingSessionForEditRow{ID: sessionID, PlanID: planID, Title: "Técnica atual", StartsAt: pgtype.Timestamptz{Time: starts, Valid: true}, EndsAt: pgtype.Timestamptz{Time: starts.Add(time.Hour), Valid: true}, Status: "ACTIVE", UpdatedAt: pgtype.Timestamptz{Time: updated, Valid: true}}

	t.Run("session creation service failure", func(t *testing.T) {
		store := &trainingWorkflowStore{createSessionErr: errors.New("database unavailable")}
		values := url.Values{"plan_id": {planID.String()}, "title": {"Série"}, "description": {"500 m"}, "starts_at": {"2026-09-08T09:00"}, "ends_at": {"2026-09-08T10:00"}}
		response := performTrainingRequest(t, CurrentUser{ID: userID, IsAdmin: true}, http.MethodPost, "/admin/treinos/sessoes", values, "", "", (Training{Store: store, Location: time.UTC}).CreateSession)
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d", response.Code)
		}
	})

	values := url.Values{"cancellation_reason": {"Cheia prevista"}, "confirm_cancellation": {"yes"}, "expected_updated_at": {updated.Format(time.RFC3339Nano)}}
	for _, tc := range []struct {
		name      string
		editErr   error
		cancelErr error
		want      int
		body      string
	}{
		{name: "missing session", editErr: pgx.ErrNoRows, want: http.StatusNotFound},
		{name: "session read failure", editErr: errors.New("database unavailable"), want: http.StatusInternalServerError},
		{name: "stale cancellation", cancelErr: pgx.ErrNoRows, want: http.StatusConflict, body: "já começou ou já foi cancelada"},
		{name: "cancellation service failure", cancelErr: errors.New("database unavailable"), want: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &trainingWorkflowStore{editSession: base, editErr: tc.editErr, cancelErr: tc.cancelErr}
			response := performTrainingRequest(t, CurrentUser{ID: userID, IsAdmin: true}, http.MethodPost, "/admin/treinos/sessoes/"+sessionID.String()+"/cancelar", values, "id", sessionID.String(), (Training{Store: store, Location: time.UTC, Now: func() time.Time { return starts.Add(-24 * time.Hour) }}).CancelSession)
			if response.Code != tc.want || (tc.body != "" && !strings.Contains(response.Body.String(), tc.body)) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestTrainingSessionAuthoringAndEditEnforceAuthorizationAndLifecycle(t *testing.T) {
	userID, planID, sessionID := uuid.New(), uuid.New(), uuid.New()
	starts := time.Date(2026, 9, 8, 9, 0, 0, 0, time.UTC)
	values := url.Values{"plan_id": {planID.String()}, "title": {"Série"}, "description": {"500 m"}, "starts_at": {"2026-09-08T09:00"}, "ends_at": {"2026-09-08T10:00"}}

	for _, tc := range []struct {
		name  string
		store *trainingWorkflowStore
		want  int
	}{
		{"coach lookup failure", &trainingWorkflowStore{manageErr: errors.New("database unavailable")}, http.StatusInternalServerError},
		{"coach lacks plan scope", &trainingWorkflowStore{manageAllowed: false}, http.StatusForbidden},
	} {
		t.Run("create "+tc.name, func(t *testing.T) {
			response := performTrainingRequest(t, CurrentUser{ID: userID}, http.MethodPost, "/admin/treinos/sessoes", values, "", "", (Training{Store: tc.store, Location: time.UTC}).CreateSession)
			if response.Code != tc.want || tc.store.createdSession.PlanID != uuid.Nil {
				t.Fatalf("response=%d session=%#v", response.Code, tc.store.createdSession)
			}
		})
	}

	for _, tc := range []struct {
		name  string
		store *trainingWorkflowStore
		want  int
	}{
		{"missing session", &trainingWorkflowStore{editErr: pgx.ErrNoRows}, http.StatusNotFound},
		{"read failure", &trainingWorkflowStore{editErr: errors.New("database unavailable")}, http.StatusInternalServerError},
		{"coach lacks plan scope", &trainingWorkflowStore{editSession: dbgen.GetTrainingSessionForEditRow{ID: sessionID, PlanID: planID}, manageAllowed: false}, http.StatusForbidden},
		{"already started", &trainingWorkflowStore{editSession: dbgen.GetTrainingSessionForEditRow{ID: sessionID, PlanID: planID, Status: "ACTIVE", StartsAt: pgtype.Timestamptz{Time: starts, Valid: true}}, manageAllowed: true}, http.StatusConflict},
		{"cancelled", &trainingWorkflowStore{editSession: dbgen.GetTrainingSessionForEditRow{ID: sessionID, PlanID: planID, Status: "CANCELLED", StartsAt: pgtype.Timestamptz{Time: starts.Add(time.Hour), Valid: true}}, manageAllowed: true}, http.StatusConflict},
	} {
		t.Run("edit "+tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/admin/treinos/sessoes/"+sessionID.String()+"/editar", nil)
			r.SetPathValue("id", sessionID.String())
			r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: userID}))
			w := httptest.NewRecorder()
			(Training{Store: tc.store, Location: time.UTC, Now: func() time.Time { return starts }}).EditSession(w, r)
			if w.Code != tc.want {
				t.Fatalf("response=%d want=%d", w.Code, tc.want)
			}
		})
	}
}

func TestTrainingSessionUpdatePersistsFreshManagedEdit(t *testing.T) {
	userID, planID, sessionID := uuid.New(), uuid.New(), uuid.New()
	starts := time.Date(2026, 9, 8, 9, 0, 0, 0, time.UTC)
	updated := starts.Add(-time.Hour)
	store := &trainingWorkflowStore{editSession: dbgen.GetTrainingSessionForEditRow{ID: sessionID, PlanID: planID, Title: "Técnica", StartsAt: pgtype.Timestamptz{Time: starts, Valid: true}, EndsAt: pgtype.Timestamptz{Time: starts.Add(time.Hour), Valid: true}, Status: "ACTIVE", UpdatedAt: pgtype.Timestamptz{Time: updated, Valid: true}}}
	now := starts.Add(-24 * time.Hour)
	h := Training{Store: store, Location: time.UTC, Now: func() time.Time { return now }}
	values := url.Values{"plan_id": {planID.String()}, "title": {"Técnica de viragem"}, "description": {"Foco na saída"}, "starts_at": {"2026-09-08T10:00"}, "ends_at": {"2026-09-08T11:30"}, "expected_updated_at": {updated.Format(time.RFC3339Nano)}}
	r := httptest.NewRequest(http.MethodPost, "/admin/treinos/sessoes/"+sessionID.String(), strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetPathValue("id", sessionID.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: userID, IsAdmin: true}))
	w := httptest.NewRecorder()

	h.UpdateSession(w, r)

	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/treinos" {
		t.Fatalf("response=%d location=%q", w.Code, w.Header().Get("Location"))
	}
	p := store.updated
	if p.ID != sessionID || p.PlanID != planID || p.Title != "Técnica de viragem" || p.Description != "Foco na saída" || !p.StartsAt.Time.Equal(time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)) || !p.EndsAt.Time.Equal(time.Date(2026, 9, 8, 11, 30, 0, 0, time.UTC)) || !p.AsOf.Time.Equal(now) || !p.ExpectedUpdatedAt.Time.Equal(updated) {
		t.Fatalf("update params=%#v", p)
	}
}

func TestTrainingSessionUpdateProtectsOutcomeLockedPlansAndStaleWrites(t *testing.T) {
	userID, planID, otherPlanID, sessionID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	starts := time.Date(2026, 9, 8, 9, 0, 0, 0, time.UTC)
	updated := starts.Add(-time.Hour)
	base := dbgen.GetTrainingSessionForEditRow{ID: sessionID, PlanID: planID, Title: "Sessão atual", StartsAt: pgtype.Timestamptz{Time: starts, Valid: true}, EndsAt: pgtype.Timestamptz{Time: starts.Add(time.Hour), Valid: true}, Status: "ACTIVE", UpdatedAt: pgtype.Timestamptz{Time: updated, Valid: true}}
	request := func(target uuid.UUID) *http.Request {
		values := url.Values{"plan_id": {target.String()}, "title": {"Sessão alterada"}, "starts_at": {"2026-09-08T10:00"}, "ends_at": {"2026-09-08T11:00"}, "expected_updated_at": {updated.Format(time.RFC3339Nano)}}
		r := httptest.NewRequest(http.MethodPost, "/admin/treinos/sessoes/"+sessionID.String(), strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.SetPathValue("id", sessionID.String())
		return r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: userID, IsAdmin: true}))
	}

	t.Run("outcome locks plan move", func(t *testing.T) {
		store := &trainingWorkflowStore{editSession: func() dbgen.GetTrainingSessionForEditRow { row := base; row.HasOutcomes = true; return row }()}
		w := httptest.NewRecorder()
		(Training{Store: store, Location: time.UTC, Now: func() time.Time { return starts.Add(-time.Hour) }}).UpdateSession(w, request(otherPlanID))
		if w.Code != http.StatusUnprocessableEntity || store.updated.ID != uuid.Nil || !strings.Contains(w.Body.String(), "não pode ser alterado") {
			t.Fatalf("response=%d update=%#v body=%q", w.Code, store.updated, w.Body.String())
		}
	})

	t.Run("stale write reloads current session", func(t *testing.T) {
		store := &trainingWorkflowStore{editSession: base, updateErr: pgx.ErrNoRows}
		w := httptest.NewRecorder()
		(Training{Store: store, Location: time.UTC, Now: func() time.Time { return starts.Add(-time.Hour) }}).UpdateSession(w, request(planID))
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "alterada entretanto") || !strings.Contains(w.Body.String(), "Sessão atual") {
			t.Fatalf("response=%d body=%q", w.Code, w.Body.String())
		}
	})
}

func TestTrainingSessionUpdateMapsDependentReadAndWriteFailures(t *testing.T) {
	userID, planID, sessionID := uuid.New(), uuid.New(), uuid.New()
	starts := time.Date(2026, 9, 8, 9, 0, 0, 0, time.UTC)
	updated := starts.Add(-time.Hour)
	base := dbgen.GetTrainingSessionForEditRow{ID: sessionID, PlanID: planID, Title: "Sessão atual", StartsAt: pgtype.Timestamptz{Time: starts, Valid: true}, EndsAt: pgtype.Timestamptz{Time: starts.Add(time.Hour), Valid: true}, Status: "ACTIVE", UpdatedAt: pgtype.Timestamptz{Time: updated, Valid: true}}
	values := url.Values{"plan_id": {planID.String()}, "title": {"Sessão alterada"}, "starts_at": {"2026-09-08T10:00"}, "ends_at": {"2026-09-08T11:00"}, "expected_updated_at": {updated.Format(time.RFC3339Nano)}}
	for _, tc := range []struct {
		name          string
		editErr       error
		planExistsErr error
		updateErr     error
		want          int
	}{
		{name: "missing session", editErr: pgx.ErrNoRows, want: http.StatusNotFound},
		{name: "session read failure", editErr: errors.New("database unavailable"), want: http.StatusInternalServerError},
		{name: "plan lookup failure", planExistsErr: errors.New("database unavailable"), want: http.StatusInternalServerError},
		{name: "write failure", updateErr: errors.New("database unavailable"), want: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &trainingWorkflowStore{editSession: base, editErr: tc.editErr, planExistsErr: tc.planExistsErr, updateErr: tc.updateErr}
			response := performTrainingRequest(t, CurrentUser{ID: userID, IsAdmin: true}, http.MethodPost, "/admin/treinos/sessoes/"+sessionID.String(), values, "id", sessionID.String(), (Training{Store: store, Location: time.UTC, Now: func() time.Time { return starts.Add(-24 * time.Hour) }}).UpdateSession)
			if response.Code != tc.want {
				t.Fatalf("status=%d, want %d", response.Code, tc.want)
			}
		})
	}
}

func TestTrainingAuthoringFiltersCoachScopeAndPaginatesManagedPlans(t *testing.T) {
	allowedProgramme, deniedProgramme, allowedTeam, deniedTeam, modalityID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	managed := make([]dbgen.ListTrainingPlansForAuthoringRow, managedTrainingPlansPageSize+1)
	for index := range managed {
		managed[index] = dbgen.ListTrainingPlansForAuthoringRow{PlanID: uuid.New(), PlanTitle: "Plano " + string(rune('A'+index))}
	}
	store := &trainingWorkflowStore{
		programmes:   []dbgen.Programme{{ID: allowedProgramme, NamePt: "Competição"}, {ID: deniedProgramme, NamePt: "Lazer"}},
		teams:        []dbgen.ListTeamsForEventAuthoringRow{{ID: allowedTeam, ProgrammeID: deniedProgramme, Name: "K2"}, {ID: deniedTeam, ProgrammeID: deniedProgramme, Name: "C4"}},
		modalities:   []dbgen.ListAnnouncementModalitiesRow{{ID: modalityID, NamePt: "Canoagem"}},
		plans:        []dbgen.ListTrainingPlansForCoachRow{{ID: uuid.New(), Title: "Plano autorizado"}},
		managedPlans: managed,
	}
	page := &pages.TrainingPage{}
	user := CurrentUser{ID: uuid.New(), CoachProgrammeIDs: map[uuid.UUID]bool{allowedProgramme: true}, CoachTeamIDs: map[uuid.UUID]bool{allowedTeam: true}}
	(Training{Store: store, Location: time.UTC, Now: func() time.Time { return time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC) }}).authoring(context.Background(), user, page, 1)

	if len(page.Programmes) != 1 || page.Programmes[0].ID != allowedProgramme.String() || len(page.Teams) != 1 || page.Teams[0].ID != allowedTeam.String() {
		t.Fatalf("scoped choices programmes=%#v teams=%#v", page.Programmes, page.Teams)
	}
	if len(page.Modalities) != 1 || page.Modalities[0].ID != modalityID.String() || len(page.Plans) != 1 || page.Plans[0].Name != "Plano autorizado" {
		t.Fatalf("authoring choices modalities=%#v plans=%#v", page.Modalities, page.Plans)
	}
	if len(page.ManagedPlans) != managedTrainingPlansPageSize || page.ManagedPlansNextURL != "/admin/treinos?managed_page=2" || page.ManagedPlansPreviousURL != "" {
		t.Fatalf("managed plans=%d next=%q previous=%q", len(page.ManagedPlans), page.ManagedPlansNextURL, page.ManagedPlansPreviousURL)
	}
}

func TestCanManageTrainingPlanHonoursAdminAndFailsClosed(t *testing.T) {
	planID, userID := uuid.New(), uuid.New()
	request := httptest.NewRequest(http.MethodGet, "/admin/treinos", nil)

	t.Run("administrator", func(t *testing.T) {
		w := httptest.NewRecorder()
		if !(Training{Store: &trainingWorkflowStore{}}).canManageTrainingPlan(context.Background(), CurrentUser{ID: userID, IsAdmin: true}, planID, w, request) || w.Code != http.StatusOK {
			t.Fatalf("allowed=%t response=%d", false, w.Code)
		}
	})

	for name, store := range map[string]*trainingWorkflowStore{
		"denied": {manageAllowed: false},
		"failed": {manageErr: errors.New("database unavailable")},
	} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			if (Training{Store: store}).canManageTrainingPlan(context.Background(), CurrentUser{ID: userID}, planID, w, request) {
				t.Fatal("unmanaged plan was accepted")
			}
			want := http.StatusForbidden
			if name == "failed" {
				want = http.StatusInternalServerError
			}
			if w.Code != want {
				t.Fatalf("response=%d want=%d", w.Code, want)
			}
		})
	}
}

type trainingOutcomeStore struct {
	dbgen.Querier
	saveParams   dbgen.SaveTrainingSessionOutcomeParams
	updateParams dbgen.UpdateOwnCompletedSessionFeedbackParams
	saveRows     int64
	updateRows   int64
	saveErr      error
	updateErr    error
}

type trainingEditStore struct{ dbgen.Querier }

type trainingWorkflowStore struct {
	dbgen.Querier
	athleteSessions    []dbgen.ListTrainingSessionsForAthleteRow
	athleteSessionsErr error
	programmes         []dbgen.Programme
	teams              []dbgen.ListTeamsForEventAuthoringRow
	modalities         []dbgen.ListAnnouncementModalitiesRow
	managedPlans       []dbgen.ListTrainingPlansForAuthoringRow
	manageAllowed      bool
	manageErr          error
	createdPlan        dbgen.CreateTrainingPlanParams
	createPlanErr      error
	createdSession     dbgen.CreateTrainingSessionParams
	editSession        dbgen.GetTrainingSessionForEditRow
	cancelled          dbgen.CancelTrainingSessionParams
	updated            dbgen.UpdateTrainingSessionParams
	updateErr          error
	createSessionErr   error
	editErr            error
	cancelErr          error
	planExistsErr      error
	planMissing        bool
	plans              []dbgen.ListTrainingPlansForCoachRow
}

func (s *trainingWorkflowStore) ListTrainingSessionsForAthlete(context.Context, dbgen.ListTrainingSessionsForAthleteParams) ([]dbgen.ListTrainingSessionsForAthleteRow, error) {
	return s.athleteSessions, s.athleteSessionsErr
}

func (s *trainingWorkflowStore) CreateTrainingPlan(_ context.Context, params dbgen.CreateTrainingPlanParams) (dbgen.TrainingPlan, error) {
	s.createdPlan = params
	return dbgen.TrainingPlan{ID: uuid.New()}, s.createPlanErr
}

func (s *trainingWorkflowStore) CreateTrainingSession(_ context.Context, params dbgen.CreateTrainingSessionParams) (dbgen.TrainingSession, error) {
	s.createdSession = params
	return dbgen.TrainingSession{ID: uuid.New()}, s.createSessionErr
}

func (s *trainingWorkflowStore) GetTrainingSessionForEdit(context.Context, uuid.UUID) (dbgen.GetTrainingSessionForEditRow, error) {
	return s.editSession, s.editErr
}
func (s *trainingWorkflowStore) CancelTrainingSession(_ context.Context, params dbgen.CancelTrainingSessionParams) (dbgen.TrainingSession, error) {
	s.cancelled = params
	return dbgen.TrainingSession{ID: params.ID}, s.cancelErr
}
func (s *trainingWorkflowStore) TrainingPlanExists(context.Context, uuid.UUID) (bool, error) {
	return !s.planMissing, s.planExistsErr
}
func (s *trainingWorkflowStore) UpdateTrainingSession(_ context.Context, params dbgen.UpdateTrainingSessionParams) (dbgen.TrainingSession, error) {
	s.updated = params
	return dbgen.TrainingSession{ID: params.ID}, s.updateErr
}

func (s *trainingWorkflowStore) ListTrainingPlansForCoach(context.Context, dbgen.ListTrainingPlansForCoachParams) ([]dbgen.ListTrainingPlansForCoachRow, error) {
	return s.plans, nil
}

func (s *trainingWorkflowStore) ListProgrammes(context.Context) ([]dbgen.Programme, error) {
	return s.programmes, nil
}

func (s *trainingWorkflowStore) ListTeamsForEventAuthoring(context.Context) ([]dbgen.ListTeamsForEventAuthoringRow, error) {
	return s.teams, nil
}

func (s *trainingWorkflowStore) ListTrainingPlansForAdmin(context.Context, int32) ([]dbgen.ListTrainingPlansForAdminRow, error) {
	plans := make([]dbgen.ListTrainingPlansForAdminRow, len(s.plans))
	for i, plan := range s.plans {
		plans[i] = dbgen.ListTrainingPlansForAdminRow{ID: plan.ID, Title: plan.Title}
	}
	return plans, nil
}

func (s *trainingWorkflowStore) ListAnnouncementModalities(context.Context) ([]dbgen.ListAnnouncementModalitiesRow, error) {
	return s.modalities, nil
}

func (s *trainingWorkflowStore) ListTrainingPlansForAuthoring(context.Context, dbgen.ListTrainingPlansForAuthoringParams) ([]dbgen.ListTrainingPlansForAuthoringRow, error) {
	return s.managedPlans, nil
}

func (s *trainingWorkflowStore) CanCoachManageTrainingPlan(context.Context, dbgen.CanCoachManageTrainingPlanParams) (bool, error) {
	return s.manageAllowed, s.manageErr
}

func (*trainingEditStore) ListTrainingPlansForCoach(context.Context, dbgen.ListTrainingPlansForCoachParams) ([]dbgen.ListTrainingPlansForCoachRow, error) {
	return nil, nil
}

func (*trainingEditStore) ListAnnouncementModalities(context.Context) ([]dbgen.ListAnnouncementModalitiesRow, error) {
	return nil, nil
}

func (s *trainingOutcomeStore) SaveTrainingSessionOutcome(_ context.Context, params dbgen.SaveTrainingSessionOutcomeParams) (int64, error) {
	s.saveParams = params
	return s.saveRows, s.saveErr
}

func (s *trainingOutcomeStore) UpdateOwnCompletedSessionFeedback(_ context.Context, params dbgen.UpdateOwnCompletedSessionFeedbackParams) (int64, error) {
	s.updateParams = params
	return s.updateRows, s.updateErr
}

func performTrainingRequest(t *testing.T, user CurrentUser, method, target string, values url.Values, pathKey, pathValue string, handler http.HandlerFunc) *httptest.ResponseRecorder {
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

func int32Ptr(value int32) *int32 { return &value }

func equalInt32Pointers(left, right *int32) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}
