package handlers

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPublicationPreviewSignsExactShownCurrentSet(t *testing.T) {
	source := publicationSourceFixture()
	actor := uuid.New()
	now := source.Header.SourceUpdatedAt.Time.Add(time.Second)
	returned := trainingPublicationReturnContext{GroupID: source.Header.GroupID, WeekID: source.Header.PlanID, Day: "2026-11-14"}
	state, err := makePublicationPreview(source, actor, "Resumo confirmado", returned, time.UTC, now)
	if err != nil || !state.CanConfirm || len(state.Recipients) != 1 || state.Recipients[0].Name != "Ana" || state.Recipients[0].SessionCount != 1 {
		t.Fatal("authorized recipient review not produced", err)
	}
	claims, err := trainingPublicationReviews.verify(state.Token, state.Metadata, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := claims.matches(trainingPublicationReviewInput{state.Metadata, state.Materialization.Prescriptions}, now); err != nil {
		t.Fatal("shown set differs from token", err)
	}
	if state.Metadata.Return.Day != "2026-11-14" || state.Metadata.Return.SessionID != uuid.Nil {
		t.Fatal("empty selected day lost")
	}
	changed := state.Metadata
	changed.ChangeSummary = "Outro resumo"
	if _, err := trainingPublicationReviews.verify(state.Token, changed, now); err == nil {
		t.Fatal("approved summary was editable without review")
	}
}

func TestPublicationPreviewDoesNotCollapseIdenticalNames(t *testing.T) {
	source := publicationSourceFixture()
	second := source.EligibleRecipients[0]
	second.MembershipID = uuid.New()
	second.AthleteUserID = testUUIDPtr(uuid.New())
	source.EligibleRecipients = append(source.EligibleRecipients, second)
	original := source.Candidates[0]
	original.MembershipID = second.MembershipID
	original.SubjectID = second.AthleteUserID
	source.Candidates = append(source.Candidates, original)
	state, err := makePublicationPreview(source, uuid.New(), "Resumo confirmado", trainingPublicationReturnContext{GroupID: source.Header.GroupID, WeekID: source.Header.PlanID}, time.UTC, source.Header.SourceUpdatedAt.Time)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Recipients) != 2 || state.Recipients[0].Name != "Ana" || state.Recipients[1].Name != "Ana" || len(state.Materialization.Prescriptions) != 2 {
		t.Fatal("distinct people collapsed by name")
	}
}
