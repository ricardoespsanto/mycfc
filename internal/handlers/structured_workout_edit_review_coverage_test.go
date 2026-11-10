package handlers

import (
	"errors"
	"net/url"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
)

func workoutEditReviewCoverageFixture(t *testing.T) (focusedEditSource, focusedEditReviewPayload) {
	t.Helper()
	source := focusedEditFixture(t)
	draft, err := focusedEditLosslessDraft(source)
	if err != nil {
		t.Fatal(err)
	}
	proposal, fields := normalizeFocusedEdit(source, draft)
	if len(fields) != 0 {
		t.Fatal(fields)
	}
	if err = allocateFocusedEditIDs(&proposal, source.Graph, nil); err != nil {
		t.Fatal(err)
	}
	proposal.References = source.Graph.References
	baseline, err := focusedEditBaselineFor(source)
	if err != nil {
		t.Fatal(err)
	}
	return source, focusedEditReviewPayload{Original: baseline, Proposal: proposal, Draft: focusedGraphValues(draft)}
}

func TestWorkoutEditSigningBindsValidatedDraftAndExactGraph(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*focusedEditSource, *focusedEditReviewPayload)
		want   error
	}{
		{"started since draft", func(s *focusedEditSource, _ *focusedEditReviewPayload) { s.DecisionAt = *s.Graph.Schedule.StartsAt }, errFocusedEditReadOnly},
		{"substituted retained activity", func(_ *focusedEditSource, p *focusedEditReviewPayload) {
			p.Draft.Set(focusedActivityPrefix("a_1")+".id", uuid.NewString())
		}, errFocusedWorkoutInvalid},
		{"missing required title", func(_ *focusedEditSource, p *focusedEditReviewPayload) { p.Draft.Set("title", "") }, errFocusedWorkoutInvalid},
		{"unselected profile reference", func(_ *focusedEditSource, p *focusedEditReviewPayload) {
			p.Proposal.References.Profiles = []dbgen.WaterIntensityProfile{{ID: uuid.New(), Revision: 1, IsActive: true}}
		}, errFocusedWorkoutInvalid},
		{"saved graph node removed without review", func(_ *focusedEditSource, p *focusedEditReviewPayload) {
			water := p.Proposal.Activities[0].Phases[0].Water
			water.Nodes = water.Nodes[:len(water.Nodes)-1]
		}, errFocusedWorkoutInvalid},
		{"proposal describes different work", func(_ *focusedEditSource, p *focusedEditReviewPayload) {
			p.Proposal.Description = "Texto alheio aos campos revistos."
		}, errFocusedWorkoutInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, payload := workoutEditReviewCoverageFixture(t)
			tc.change(&source, &payload)
			raw, token, err := focusedWorkoutReviews.signEdit(payload, source, time.Now())
			if !errors.Is(err, tc.want) || raw != "" || token != "" {
				t.Fatalf("unvalidated draft returned proof=%t payload=%t err=%v", token != "", raw != "", err)
			}
		})
	}
}

func TestWorkoutEditReviewRequiresExpectedJSONEvenWithAuthenticToken(t *testing.T) {
	source := focusedEditFixture(t)
	now := time.Now()
	raw, token, err := focusedWorkoutReviews.signNativeFields([]byte(`{"unknown":true}`), url.Values{}, nil, focusedEditReviewPurpose, now)
	if err != nil {
		t.Fatal(err)
	}
	_, reviewed, err := focusedWorkoutReviews.verifyEdit(raw, token, source.Context.ActorID, source.Context.PlanID, source.Graph.SessionID, now)
	if !errors.Is(err, errFocusedWorkoutInvalid) || reviewed.verified {
		t.Fatalf("unexpected review schema acquired verified state: %t %v", reviewed.verified, err)
	}
}
