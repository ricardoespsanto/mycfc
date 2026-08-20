package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
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

type trainingOutcomeStore struct {
	dbgen.Querier
	saveParams   dbgen.SaveTrainingSessionOutcomeParams
	updateParams dbgen.UpdateOwnCompletedSessionFeedbackParams
	saveRows     int64
	updateRows   int64
}

type trainingEditStore struct{ dbgen.Querier }

func (*trainingEditStore) ListTrainingPlansForCoach(context.Context, dbgen.ListTrainingPlansForCoachParams) ([]dbgen.ListTrainingPlansForCoachRow, error) {
	return nil, nil
}

func (*trainingEditStore) ListAnnouncementModalities(context.Context) ([]dbgen.ListAnnouncementModalitiesRow, error) {
	return nil, nil
}

func (s *trainingOutcomeStore) SaveTrainingSessionOutcome(_ context.Context, params dbgen.SaveTrainingSessionOutcomeParams) (int64, error) {
	s.saveParams = params
	return s.saveRows, nil
}

func (s *trainingOutcomeStore) UpdateOwnCompletedSessionFeedback(_ context.Context, params dbgen.UpdateOwnCompletedSessionFeedbackParams) (int64, error) {
	s.updateParams = params
	return s.updateRows, nil
}

func int32Ptr(value int32) *int32 { return &value }

func equalInt32Pointers(left, right *int32) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}
