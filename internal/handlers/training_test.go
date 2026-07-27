package handlers

import (
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
