package handlers

import (
	"errors"
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
	second.PlacementID = uuid.New()
	second.AthleteUserID = testUUIDPtr(uuid.New())
	source.EligibleRecipients = append(source.EligibleRecipients, second)
	original := source.Candidates[0]
	original.MembershipID = &second.MembershipID
	original.PlacementID = second.PlacementID
	original.SubjectID = *second.AthleteUserID
	source.Candidates = append(source.Candidates, original)
	state, err := makePublicationPreview(source, uuid.New(), "Resumo confirmado", trainingPublicationReturnContext{GroupID: source.Header.GroupID, WeekID: source.Header.PlanID}, time.UTC, source.Header.SourceUpdatedAt.Time)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Recipients) != 2 || state.Recipients[0].Name != "Ana" || state.Recipients[1].Name != "Ana" || len(state.Materialization.Prescriptions) != 2 {
		t.Fatal("distinct people collapsed by name")
	}
}

func TestPublicationPreviewDoesNotSignMismatchedWeekOrEmptyFirstReview(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*publicationSourceEnvelope, *trainingPublicationReturnContext)
		want   error
	}{
		{"another week", func(_ *publicationSourceEnvelope, c *trainingPublicationReturnContext) { c.WeekID = uuid.New() }, errTrainingPublicationReviewInvalid},
		{"no dated recipients", func(s *publicationSourceEnvelope, _ *trainingPublicationReturnContext) {
			s.EligibleRecipients = s.EligibleRecipients[:0]
		}, errPublicationFirstEmpty},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := publicationSourceFixture()
			returned := trainingPublicationReturnContext{GroupID: source.Header.GroupID, WeekID: source.Header.PlanID}
			tc.change(&source, &returned)
			state, err := makePublicationPreview(source, uuid.New(), "Resumo revisto", returned, time.UTC, source.Header.SourceUpdatedAt.Time)
			if !errors.Is(err, tc.want) || state.Token != "" || state.CanConfirm {
				t.Fatalf("invalid review produced a confirmation: %t, %v", state.CanConfirm, err)
			}
		})
	}
}

func TestPublicationPreviewRejectsInconsistentPrivateRecipientIdentity(t *testing.T) {
	for _, tc := range []string{"missing athlete", "missing session", "different names for one athlete"} {
		t.Run(tc, func(t *testing.T) {
			source := publicationSourceFixture()
			second := source.EligibleRecipients[0]
			switch tc {
			case "missing athlete":
				second.AthleteUserID = nil
			case "missing session":
				second.SessionID = uuid.Nil
			default:
				second.AthleteName = "Outro nome"
			}
			source.EligibleRecipients = append(source.EligibleRecipients, second)
			recipients, err := publicationPreviewRecipients(source)
			if !errors.Is(err, errPublicationSourceUnavailable) || recipients != nil {
				t.Fatalf("inconsistent recipient data leaked into review: %v", err)
			}
		})
	}
}
