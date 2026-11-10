package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"time"

	"github.com/google/uuid"
)

const focusedGraphReviewPurpose = "mycfc/focused-workout-create/v1"

type focusedGraphReviewPayload struct {
	Proposal focusedWorkoutGraph `json:"proposal"`
	Draft    url.Values          `json:"draft"`
}
type focusedGraphReviewed struct {
	Proposal  focusedWorkoutGraph
	verified  bool
	expiresAt time.Time
}

func (s focusedWorkoutSigner) signGraph(payload focusedGraphReviewPayload, now time.Time) (string, string, error) {
	if err := validateFocusedGraphPayload(payload); err != nil {
		return "", "", err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", "", err
	}
	return s.signNative(raw, payload.Draft, payload.Proposal.Context, focusedGraphReviewPurpose, now)
}
func (s focusedWorkoutSigner) verifyGraph(raw, token string, actor, plan uuid.UUID, now time.Time) (focusedGraphReviewPayload, focusedGraphReviewed, error) {
	var p focusedGraphReviewPayload
	var r focusedGraphReviewed
	expires, err := s.verifyNative(raw, token, focusedGraphReviewPurpose, now)
	if err != nil {
		return p, r, err
	}
	if focusedStrictJSON([]byte(raw), &p) != nil {
		return p, r, errFocusedWorkoutInvalid
	}
	encoded, _ := json.Marshal(p)
	if !bytes.Equal(encoded, []byte(raw)) || p.Proposal.Context.ActorID != actor || p.Proposal.Context.PlanID != plan || validateFocusedGraphPayload(p) != nil {
		return focusedGraphReviewPayload{}, r, errFocusedWorkoutInvalid
	}
	return p, focusedGraphReviewed{p.Proposal, true, expires}, nil
}
func focusedGraphSelectedProfileIDs(p focusedWorkoutGraph) []uuid.UUID {
	seen := map[uuid.UUID]bool{}
	out := []uuid.UUID{}
	for _, a := range p.Activities {
		for _, b := range a.Phases {
			if b.Water != nil && b.Water.ProfileID != nil && !seen[*b.Water.ProfileID] {
				id := *b.Water.ProfileID
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i][:], out[j][:]) < 0 })
	return out
}
func validateFocusedGraphReferences(p focusedWorkoutGraph) error {
	for _, profile := range p.References.Profiles {
		if !profile.IsActive {
			return errFocusedWorkoutInvalid
		}
	}
	return validateFocusedReferenceShape(p)
}
func validateFocusedReferenceShape(p focusedWorkoutGraph) error {
	ids := focusedGraphSelectedProfileIDs(p)
	if len(ids) > 32 || len(p.References.Profiles) != len(ids) || len(p.References.Zones) > 256 {
		return errFocusedWorkoutInvalid
	}
	selected := map[uuid.UUID]bool{}
	for i, profile := range p.References.Profiles {
		if profile.ID == uuid.Nil || profile.ID != ids[i] || profile.Revision < 1 {
			return errFocusedWorkoutInvalid
		}
		selected[profile.ID] = true
	}
	zones := map[uuid.UUID]bool{}
	codes := map[string]bool{}
	var previous string
	for _, z := range p.References.Zones {
		order := z.ProfileID.String() + ":" + fmt.Sprintf("%010d", z.Position) + ":" + z.ID.String()
		if !selected[z.ProfileID] || z.ID == uuid.Nil || zones[z.ID] || codes[z.ProfileID.String()+":"+z.Code] || z.Position < 1 || previous != "" && order <= previous {
			return errFocusedWorkoutInvalid
		}
		zones[z.ID] = true
		codes[z.ProfileID.String()+":"+z.Code] = true
		previous = order
	}
	return nil
}
func validateFocusedGraphPayload(payload focusedGraphReviewPayload) error {
	p := payload.Proposal
	c := p.Context
	if c.ActorID == uuid.Nil || c.PlanID == uuid.Nil || c.GroupID == uuid.Nil || c.SeasonID == uuid.Nil || c.DatabaseIncarnation == uuid.Nil || c.StateEpoch < 0 || c.SchemaContract != "schedule-p1" || !c.AuthoringEnabled || c.PlanUpdatedAt.IsZero() || !c.PlanUpdatedAt.Equal(c.PlanUpdatedAt.Truncate(time.Microsecond)) || !validWorkspaceDate(c.Day) {
		return errFocusedWorkoutInvalid
	}
	day, _ := time.Parse("2006-01-02", c.Day)
	if day.Before(c.WeekStart) || !day.Before(c.WeekStart.AddDate(0, 0, 7)) {
		return errFocusedWorkoutInvalid
	}
	d, err := parseFocusedGraph(payload.Draft)
	if err != nil {
		return err
	}
	expected, e := normalizeFocusedGraph(c, d)
	if len(e) > 0 {
		return errFocusedWorkoutInvalid
	}
	if err = validateFocusedGraphReferences(p); err != nil {
		return err
	}
	expected.References = p.References
	raw, _ := json.Marshal(p)
	var copy focusedWorkoutGraph
	if json.Unmarshal(raw, &copy) != nil {
		return errFocusedWorkoutInvalid
	}
	seen := map[uuid.UUID]bool{}
	clear := func(id *uuid.UUID) bool {
		if *id == uuid.Nil || seen[*id] {
			return false
		}
		seen[*id] = true
		*id = uuid.Nil
		return true
	}
	if !clear(&copy.SessionID) {
		return errFocusedWorkoutInvalid
	}
	for ai := range copy.Activities {
		a := &copy.Activities[ai]
		if !clear(&a.ID) {
			return errFocusedWorkoutInvalid
		}
		for bi := range a.Phases {
			b := &a.Phases[bi]
			if !clear(&b.ID) {
				return errFocusedWorkoutInvalid
			}
			if b.Water != nil {
				for ni := range b.Water.Nodes {
					if !clear(&b.Water.Nodes[ni].ID) {
						return errFocusedWorkoutInvalid
					}
				}
			}
			if b.Gym != nil {
				for xi := range b.Gym.Exercises {
					if !clear(&b.Gym.Exercises[xi].ID) {
						return errFocusedWorkoutInvalid
					}
				}
			}
		}
	}
	a, _ := json.Marshal(copy)
	b, _ := json.Marshal(expected)
	if !bytes.Equal(a, b) {
		return errFocusedWorkoutInvalid
	}
	return nil
}
