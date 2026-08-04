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
	request := httptest.NewRequest(http.MethodPost, "/treinos/sessoes/resultados", strings.NewReader("session_id="+sessionID.String()+"&status=COMPLETED&distance_km=12.34"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request = request.WithContext(context.WithValue(request.Context(), currentUserKey{}, CurrentUser{ID: userID}))
	response := httptest.NewRecorder()
	training.ReportOutcome(response, request)
	if response.Code != http.StatusSeeOther || store.saveParams.DistanceMetres == nil || *store.saveParams.DistanceMetres != 12340 {
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

func TestUpdateTrainingDistanceRequiresOwnCompletedOutcome(t *testing.T) {
	store := &trainingOutcomeStore{updateRows: 1}
	training := Training{Store: store}
	userID, sessionID := uuid.New(), uuid.New()
	request := httptest.NewRequest(http.MethodPost, "/treinos/sessoes/distancia", strings.NewReader("session_id="+sessionID.String()+"&distance_km=7.5"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request = request.WithContext(context.WithValue(request.Context(), currentUserKey{}, CurrentUser{ID: userID}))
	response := httptest.NewRecorder()
	training.UpdateDistance(response, request)
	if response.Code != http.StatusSeeOther || store.updateParams.UserID != userID || store.updateParams.DistanceMetres == nil || *store.updateParams.DistanceMetres != 7500 {
		t.Fatalf("response = %d, params = %+v", response.Code, store.updateParams)
	}
}

type trainingOutcomeStore struct {
	dbgen.Querier
	saveParams   dbgen.SaveTrainingSessionOutcomeParams
	updateParams dbgen.UpdateOwnCompletedSessionDistanceParams
	saveRows     int64
	updateRows   int64
}

func (s *trainingOutcomeStore) SaveTrainingSessionOutcome(_ context.Context, params dbgen.SaveTrainingSessionOutcomeParams) (int64, error) {
	s.saveParams = params
	return s.saveRows, nil
}

func (s *trainingOutcomeStore) UpdateOwnCompletedSessionDistance(_ context.Context, params dbgen.UpdateOwnCompletedSessionDistanceParams) (int64, error) {
	s.updateParams = params
	return s.updateRows, nil
}

func int32Ptr(value int32) *int32 { return &value }

func equalInt32Pointers(left, right *int32) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}
