package handlers

import (
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
)

func TestWorkoutGraphDraftRejectsInvalidNativeFormBoundaries(t *testing.T) {
	phase := focusedGraphPhasePrefix("a_1", "p_1")
	tests := []struct {
		name   string
		change func(url.Values)
		want   error
	}{
		{"oversized request", func(v url.Values) { v.Set("title", strings.Repeat("a", focusedWorkoutMaxRequestBytes)) }, errFocusedWorkoutLimit},
		{"too many activities", func(v url.Values) { v["activity_order"] = make([]string, focusedMaxActivities+1) }, errFocusedWorkoutLimit},
		{"invalid activity key", func(v url.Values) { v.Set("activity_order", "a 1") }, errFocusedWorkoutInvalid},
		{"too many phases", func(v url.Values) {
			v[focusedActivityPrefix("a_1")+".phase_order"] = make([]string, focusedWorkoutMaxPhases+1)
		}, errFocusedWorkoutLimit},
		{"duplicate phase", func(v url.Values) { v.Add(focusedActivityPrefix("a_1")+".phase_order", "p_1") }, errFocusedWorkoutInvalid},
		{"too many exercises", func(v url.Values) { v[phase+".gym.exercise_order"] = make([]string, focusedMaxGymExercises+1) }, errFocusedWorkoutLimit},
		{"child before parent", func(v url.Values) { v[phase+".water.node_order"] = []string{"n_2", "n_1"} }, errFocusedWorkoutInvalid},
		{"repeated removal marker", func(v url.Values) { v["removed_order"] = []string{"r_1", "r_1"} }, errFocusedWorkoutInvalid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, v := focusedGraphWaterExample()
			tc.change(v)
			if _, err := parseFocusedGraph(v); !errors.Is(err, tc.want) {
				t.Fatalf("malformed native form returned %v, want %v", err, tc.want)
			}
		})
	}
}

func TestWorkoutGraphRemovalCannotRestoreAnotherBranch(t *testing.T) {
	phase := focusedGraphPhasePrefix("a_1", "p_1")
	node := focusedNodePrefix("a_1", "p_1", "n_2")
	order := phase + ".water.node_order"
	for _, tc := range []struct {
		name   string
		change func(*focusedGraphTombstone)
	}{
		{"wrong activity", func(r *focusedGraphTombstone) { r.Activity = "a 1" }},
		{"wrong removal position", func(r *focusedGraphTombstone) { r.Index = -1 }},
		{"unknown kind", func(r *focusedGraphTombstone) { r.Kind = "unrecognized" }},
		{"another item in the removed order", func(r *focusedGraphTombstone) { r.Values.Set(order, "n_other") }},
		{"forged parent", func(r *focusedGraphTombstone) { r.Values.Set(node+".parent_key", "n_other") }},
		{"out-of-branch field", func(r *focusedGraphTombstone) { r.Values.Set("title", "other draft") }},
		{"independent node hidden in the subtree", func(r *focusedGraphTombstone) {
			r.Values.Add(order, "n_other")
			r.Values.Set(focusedNodePrefix("a_1", "p_1", "n_other")+".kind", "EFFORT")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := focusedGraphTombstone{Kind: "water", Activity: "a_1", Phase: "p_1", Item: "n_2", Parent: "n_1", Values: url.Values{
				order:                {"n_2"},
				node + ".parent_key": {"n_1"},
				node + ".kind":       {"EFFORT"},
			}}
			if _, counts, path, err := focusedGraphTombstoneGraph(r); err != nil || counts.Nodes != 1 || path != node {
				t.Fatalf("valid removal fixture got counts=%+v path=%q err=%v", counts, path, err)
			}
			tc.change(&r)
			if _, _, _, err := focusedGraphTombstoneGraph(r); !errors.Is(err, errFocusedWorkoutInvalid) {
				t.Fatalf("invalid removal returned %v", err)
			}
		})
	}
}

func TestWorkoutGraphSigningRejectsInconsistentReviewedFacts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*focusedGraphReviewPayload)
	}{
		{"authoring disabled", func(p *focusedGraphReviewPayload) { p.Proposal.Context.AuthoringEnabled = false }},
		{"day outside the reviewed week", func(p *focusedGraphReviewPayload) {
			p.Proposal.Context.WeekStart = p.Proposal.Context.WeekStart.AddDate(0, 0, 7)
		}},
		{"unexpected draft field", func(p *focusedGraphReviewPayload) { p.Draft.Set("outside", "unexpected") }},
		{"missing required title", func(p *focusedGraphReviewPayload) { p.Draft.Set("title", "") }},
		{"unselected reference", func(p *focusedGraphReviewPayload) {
			p.Proposal.References.Profiles = []dbgen.WaterIntensityProfile{{ID: uuid.New(), Revision: 1, IsActive: true}}
		}},
		{"inactive reference", func(p *focusedGraphReviewPayload) {
			p.Proposal.References.Profiles = []dbgen.WaterIntensityProfile{{ID: uuid.New(), Revision: 1}}
		}},
		{"missing session identity", func(p *focusedGraphReviewPayload) { p.Proposal.SessionID = uuid.Nil }},
		{"substituted phase identity", func(p *focusedGraphReviewPayload) {
			p.Proposal.Activities[0].Phases[0].ID = p.Proposal.Activities[0].ID
		}},
		{"substituted water identity", func(p *focusedGraphReviewPayload) {
			p.Proposal.Activities[0].Phases[0].Water.Nodes[0].ID = p.Proposal.SessionID
		}},
		{"proposal describes different work", func(p *focusedGraphReviewPayload) { p.Proposal.Title = "Different workout" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, v := focusedGraphWaterExample()
			p := normalizedGraphExample(t, c, v)
			allocateFocusedGraphIDs(&p, nil)
			d, err := parseFocusedGraph(v)
			if err != nil {
				t.Fatal(err)
			}
			payload := focusedGraphReviewPayload{Proposal: p, Draft: focusedGraphValues(d)}
			tc.change(&payload)
			raw, token, err := focusedWorkoutReviews.signGraph(payload, time.Now())
			if !errors.Is(err, errFocusedWorkoutInvalid) || raw != "" || token != "" {
				t.Fatalf("inconsistent facts returned raw=%t token=%t err=%v", raw != "", token != "", err)
			}
		})
	}
}

func TestWorkoutGraphVerificationRejectsUnknownSignedPayload(t *testing.T) {
	c, _ := focusedGraphWaterExample()
	now := time.Now()
	raw, token, err := focusedWorkoutReviews.signNativeFields([]byte(`{"unknown":true}`), url.Values{}, nil, focusedGraphReviewPurpose, now)
	if err != nil {
		t.Fatal(err)
	}
	_, review, err := focusedWorkoutReviews.verifyGraph(raw, token, c.ActorID, c.PlanID, now)
	if !errors.Is(err, errFocusedWorkoutInvalid) || review.verified {
		t.Fatalf("unknown payload verified=%t err=%v", review.verified, err)
	}
}
