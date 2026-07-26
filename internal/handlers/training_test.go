package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
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
