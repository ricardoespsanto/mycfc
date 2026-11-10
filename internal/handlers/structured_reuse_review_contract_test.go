package handlers

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestReuseReviewContractRequiresCoherentSourceTargetAndEventIdentity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*reuseReviewPayload)
		want   error
	}{
		{"missing actor", func(p *reuseReviewPayload) { p.ActorID = uuid.Nil }, errFocusedWorkoutInvalid},
		{"source selector omits plan", func(p *reuseReviewPayload) { p.Source.Selector.PlanID = uuid.Nil }, errFocusedWorkoutInvalid},
		{"no active source", func(p *reuseReviewPayload) { p.Source.Items = nil }, errReuseNoWork},
		{"too many sources", func(p *reuseReviewPayload) { p.Source.Items = make([]reuseSourceItem, reuseMaxSessions+1) }, errFocusedWorkoutLimit},
		{"missing target plan", func(p *reuseReviewPayload) { p.Proposal.TargetPlanID = uuid.Nil }, errFocusedWorkoutInvalid},
		{"inconsistent destination configuration", func(p *reuseReviewPayload) { p.Target.Configuration.GroupID = uuid.New() }, errFocusedWorkoutInvalid},
		{"new week claimed as existing", func(p *reuseReviewPayload) { p.Proposal.NewWeek = false }, errFocusedWorkoutInvalid},
		{"existing week plan differs", func(p *reuseReviewPayload) {
			p.Proposal.NewWeek = false
			p.Target.Configuration.ExistingPlanID = focusedPointer(uuid.New())
		}, errFocusedWorkoutInvalid},
		{"duplicate source mapping", func(p *reuseReviewPayload) { p.Source.Items = append(p.Source.Items, p.Source.Items[0]) }, errFocusedWorkoutInvalid},
		{"unbound destination mapping", func(p *reuseReviewPayload) { p.Proposal.Destinations[0].Key = "m_2" }, errFocusedWorkoutInvalid},
		{"destination state epoch differs", func(p *reuseReviewPayload) { p.Proposal.Destinations[0].Graph.Context.StateEpoch++ }, errFocusedWorkoutInvalid},
		{"destination reuses source session ID", func(p *reuseReviewPayload) {
			p.Proposal.Destinations[0].Graph.SessionID = p.Source.Items[0].Graph.SessionID
		}, errFocusedWorkoutInvalid},
		{"destination reuses source segment ID", func(p *reuseReviewPayload) {
			p.Proposal.Destinations[0].Graph.Activities[0].ID = p.Source.Items[0].Graph.Activities[0].ID
		}, errFocusedWorkoutInvalid},
		{"event reuses destination ID", func(p *reuseReviewPayload) {
			p.Proposal.Events[0].ID = p.Proposal.Destinations[0].Graph.SessionID
		}, errFocusedWorkoutInvalid},
		{"event lacks reviewed source version", func(p *reuseReviewPayload) { p.Proposal.Events[0].SourceUpdatedAt = time.Time{} }, errFocusedWorkoutInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := reuseExample(t)
			tc.change(&p)
			raw, token, err := focusedWorkoutReviews.signReuse(p, time.Now())
			if !errors.Is(err, tc.want) || raw != "" || token != "" {
				t.Fatalf("inconsistent review was signable: error=%v, want %v", err, tc.want)
			}
		})
	}
}

func TestReuseReviewContractRefusesIncompleteOrUnrepresentableWorkoutGraph(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*reuseReviewPayload)
		want   error
	}{
		{"source entity lacks stable identity", func(p *reuseReviewPayload) { p.Source.Items[0].Graph.Activities[0].ID = uuid.Nil }, errFocusedWorkoutInvalid},
		{"destination schedule cannot represent workout kind", func(p *reuseReviewPayload) {
			d := &p.Proposal.Destinations[0]
			d.Graph.Schedule = TrainingSessionSchedule{Precision: TrainingScheduleDay, Date: d.Graph.Schedule.Date}
		}, errFocusedWorkoutInvalid},
		{"destination exceeds activity budget", func(p *reuseReviewPayload) {
			p.Proposal.Destinations[0].Graph.Activities = make([]focusedGraphActivityInput, focusedMaxActivities+1)
		}, errFocusedWorkoutLimit},
		{"destination phase carries both typed prescriptions", func(p *reuseReviewPayload) {
			p.Proposal.Destinations[0].Graph.Activities[0].Phases[0].Gym = &focusedGraphGymInput{}
		}, errFocusedWorkoutInvalid},
		{"destination step has missing parent", func(p *reuseReviewPayload) {
			p.Proposal.Destinations[0].Graph.Activities[0].Phases[0].Water.Nodes[1].ParentKey = "n_99"
		}, errFocusedWorkoutInvalid},
		{"destination workload has negative effort", func(p *reuseReviewPayload) {
			p.Proposal.Destinations[0].Graph.Activities[0].Phases[0].Water.Nodes[1].Duration = focusedPointer(int32(-1))
		}, errFocusedArithmetic},
		{"water copy lacks provenance", func(p *reuseReviewPayload) { p.Proposal.Destinations[0].Origins = nil }, errFocusedWorkoutInvalid},
		{"native provenance identifies another plan", func(p *reuseReviewPayload) {
			d := &p.Proposal.Destinations[0]
			block := d.Graph.Activities[0].Phases[0].ID
			origin := d.Origins[block]
			origin.NativePlanID = focusedPointer(uuid.New())
			d.Origins[block] = origin
		}, errFocusedWorkoutInvalid},
		{"native provenance mixes in a routine", func(p *reuseReviewPayload) {
			d := &p.Proposal.Destinations[0]
			block := d.Graph.Activities[0].Phases[0].ID
			origin := d.Origins[block]
			origin.RoutineID = focusedPointer(uuid.New())
			d.Origins[block] = origin
		}, errFocusedWorkoutInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := reuseExample(t)
			tc.change(&p)
			if err := validateReuseEnvelope(p); !errors.Is(err, tc.want) {
				t.Fatalf("invalid graph error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestReuseReviewContractWaterOriginMustNameSelectedRoutine(t *testing.T) {
	actor, read := reuseFragmentProposalFixture(t, "BLOCK")
	p, fields := buildReuseProposal(actor, read, nil, nil)
	if len(fields) != 0 || len(p.Proposal.Destinations) != 1 {
		t.Fatalf("valid fragment could not be reviewed: %v", fields)
	}
	d := &p.Proposal.Destinations[0]
	block := p.Proposal.Events[0].DestinationID
	origin := d.Origins[block]
	origin.RoutineID = focusedPointer(uuid.New())
	d.Origins[block] = origin
	if err := validateReuseEnvelope(p); !errors.Is(err, errFocusedWorkoutInvalid) {
		t.Fatalf("different routine accepted as a water prescription origin: %v", err)
	}
}

func TestReuseReviewContractAuthenticityDoesNotSubstituteForCanonicalValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*reuseReviewPayload) ([]byte, error)
		want   error
	}{
		{"valid signature over noncanonical JSON", func(p *reuseReviewPayload) ([]byte, error) {
			return json.MarshalIndent(p, "", " ")
		}, errFocusedWorkoutInvalid},
		{"valid signature over source with no active work", func(p *reuseReviewPayload) ([]byte, error) {
			p.Source.Items = nil
			return json.Marshal(p)
		}, errReuseNoWork},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := reuseExample(t)
			encoded, err := tc.change(&p)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			raw, proof, err := focusedWorkoutReviews.signNativeFields(encoded, p.Values, nil, reuseReviewPurpose, now)
			if err != nil {
				t.Fatal(err)
			}
			_, reviewed, err := focusedWorkoutReviews.verifyReuse(raw, proof, p.ActorID, p.Source.Selector, now)
			if !errors.Is(err, tc.want) || reviewed.verified {
				t.Fatalf("valid MAC was accepted without semantic validation: verified=%t error=%v want %v", reviewed.verified, err, tc.want)
			}
		})
	}
}
