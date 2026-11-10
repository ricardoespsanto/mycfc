package handlers

import (
	"bytes"
	"encoding/json"
	"strconv"
	"time"

	"github.com/google/uuid"
)

func validateReuseEnvelope(p reuseReviewPayload) error {
	if p.ActorID == uuid.Nil || p.Incarnation == uuid.Nil || p.Epoch < 1 {
		return errFocusedWorkoutInvalid
	}
	if err := p.Source.Selector.validate(); err != nil {
		return err
	}
	if len(p.Source.Items) == 0 {
		return errReuseNoWork
	}
	if len(p.Source.Items) > reuseMaxSessions || len(p.Proposal.Destinations) > reuseMaxSessions || len(p.Proposal.Events) > reuseMaxSessions+1 || len(p.References.Profiles) > 32 || len(p.References.Zones) > 256 {
		return errFocusedWorkoutLimit
	}
	graphs := make([]focusedWorkoutGraph, 0, len(p.Source.Items)+len(p.Proposal.Destinations))
	for _, item := range p.Source.Items {
		graphs = append(graphs, item.Graph)
	}
	for _, destination := range p.Proposal.Destinations {
		graphs = append(graphs, destination.Graph)
	}
	expected, err := reuseExpectedProfileIDs(graphs)
	if err != nil {
		return err
	}
	if err = validateReuseReferenceClosure(expected, p.References); err != nil {
		return err
	}
	if p.Proposal.TargetPlanID == uuid.Nil || p.Target.Selector.GroupID == uuid.Nil || p.Target.Selector.SeasonID == uuid.Nil {
		return errFocusedWorkoutInvalid
	}
	if p.Target.Configuration.GroupID != p.Target.Selector.GroupID || p.Target.Configuration.SeasonID != p.Target.Selector.SeasonID || p.Target.Configuration.WeekStart.Time.Format("2006-01-02") != p.Target.Selector.WeekStart {
		return errFocusedWorkoutInvalid
	}
	if p.Proposal.NewWeek != (p.Target.Configuration.ExistingPlanID == nil) {
		return errFocusedWorkoutInvalid
	}
	if p.Target.Configuration.ExistingPlanID != nil && *p.Target.Configuration.ExistingPlanID != p.Proposal.TargetPlanID {
		return errFocusedWorkoutInvalid
	}
	for _, name := range []string{"command", "prior_proposal", "prior_review_token", "gorilla.csrf.Token"} {
		if _, exists := p.Values[name]; exists {
			return errFocusedWorkoutInvalid
		}
	}
	sourceIDs := map[uuid.UUID]bool{}
	sourceKeys := map[string]bool{}
	for _, item := range p.Source.Items {
		if sourceKeys[item.Key] || !focusedWorkoutKey.MatchString(item.Key) {
			return errFocusedWorkoutInvalid
		}
		sourceKeys[item.Key] = true
		sourceIDs[item.Graph.SessionID] = true
		entities, err := focusedEditEntities(item.Graph)
		if err != nil {
			return err
		}
		for id := range entities {
			sourceIDs[id] = true
		}
	}
	keys := map[string]bool{}
	ids := map[uuid.UUID]bool{}
	take := func(id uuid.UUID) bool {
		if id == uuid.Nil || ids[id] {
			return false
		}
		ids[id] = true
		return true
	}
	for _, d := range p.Proposal.Destinations {
		if !sourceKeys[d.Key] || !focusedWorkoutKey.MatchString(d.Key) || keys[d.Key] {
			return errFocusedWorkoutInvalid
		}
		keys[d.Key] = true
		if d.Graph.Context.ActorID != p.ActorID || d.Graph.Context.DatabaseIncarnation != p.Incarnation || d.Graph.Context.StateEpoch != p.Epoch || d.Graph.Context.PlanID != p.Proposal.TargetPlanID || d.Graph.Context.GroupID != p.Target.Selector.GroupID || d.Graph.Context.SeasonID != p.Target.Selector.SeasonID {
			return errFocusedWorkoutInvalid
		}
		if sourceIDs[d.Graph.SessionID] || !take(d.Graph.SessionID) {
			return errFocusedWorkoutInvalid
		}
		if d.Graph.Schedule.ValidateKind(string(d.Graph.Kind)) != nil {
			return errFocusedWorkoutInvalid
		}
		if err := validateReuseGraphLimits(d.Graph); err != nil {
			return err
		}
		entities, err := focusedEditEntities(d.Graph)
		if err != nil {
			return err
		}
		for id := range entities {
			if sourceIDs[id] || !take(id) {
				return errFocusedWorkoutInvalid
			}
		}
		for _, a := range d.Graph.Activities {
			for _, b := range a.Phases {
				if b.Water != nil {
					origin, ok := d.Origins[b.ID]
					if !ok {
						retained := false
						if p.Target.Existing != nil {
							for _, old := range p.Target.Existing.Water {
								if old.BlockID == b.ID && focusedGraphJSONEqual(old.IntensityProfileID, b.Water.ProfileID) {
									retained = true
								}
							}
						}
						if retained {
							continue
						}
						return errFocusedWorkoutInvalid
					}
					native := origin.NativePlanID != nil && origin.NativeBlockID != nil && origin.RoutineID == nil && len(origin.RoutinePath) == 0
					routine := origin.NativePlanID == nil && origin.NativeBlockID == nil && origin.RoutineID != nil && len(origin.RoutinePath) <= 2
					if native && (*origin.NativePlanID != p.Source.Selector.PlanID || p.Source.Selector.Kind == "ROUTINE") {
						return errFocusedWorkoutInvalid
					}
					if routine && (*origin.RoutineID != p.Source.Selector.RoutineID || p.Source.Selector.Kind != "ROUTINE") {
						return errFocusedWorkoutInvalid
					}
					if !native && !routine {
						return errFocusedWorkoutInvalid
					}
				}
			}
		}
		if _, err = focusedGraphCheckedTotals(d.Graph); err != nil {
			return err
		}
	}
	for _, e := range p.Proposal.Events {
		if !take(e.ID) || e.SourceID == uuid.Nil || e.DestinationID == uuid.Nil || e.SourceUpdatedAt.IsZero() {
			return errFocusedWorkoutInvalid
		}
	}
	_, err = reuseBoundedJSON(p)
	return err
}
func (s focusedWorkoutSigner) signReuse(p reuseReviewPayload, now time.Time) (string, string, error) {
	if err := validateReuseEnvelope(p); err != nil {
		return "", "", err
	}
	raw, err := reuseBoundedJSON(p)
	if err != nil {
		return "", "", err
	}
	return s.signNativeFields(raw, p.Values, map[string]string{"source_kind": p.Source.Selector.Kind, "target_group_id": p.Target.Selector.GroupID.String(), "season_id": p.Target.Selector.SeasonID.String(), "week_start": p.Target.Selector.WeekStart, "state_epoch": strconv.FormatInt(p.Epoch, 10)}, reuseReviewPurpose, now)
}
func (s focusedWorkoutSigner) verifyReuse(raw, token string, actor uuid.UUID, source reuseSourceSelector, now time.Time) (reuseReviewPayload, reuseReviewed, error) {
	var p reuseReviewPayload
	var reviewed reuseReviewed
	expiry, err := s.verifyNative(raw, token, reuseReviewPurpose, now)
	if err != nil {
		return p, reviewed, err
	}
	if focusedStrictJSON([]byte(raw), &p) != nil || p.ActorID != actor || !focusedGraphJSONEqual(p.Source.Selector, source) {
		return p, reviewed, errFocusedWorkoutInvalid
	}
	encoded, err := json.Marshal(p)
	if err != nil || !bytes.Equal(encoded, []byte(raw)) {
		return p, reviewed, errFocusedWorkoutInvalid
	}
	if err = validateReuseEnvelope(p); err != nil {
		return p, reviewed, err
	}
	return p, reuseReviewed{Payload: p, verified: true, expiresAt: expiry}, nil
}

func validateReuseGraphLimits(p focusedWorkoutGraph) error {
	if len(p.Activities) > focusedMaxActivities {
		return errFocusedWorkoutLimit
	}
	phases, nodes, exercises := 0, 0, 0
	for _, a := range p.Activities {
		phases += len(a.Phases)
		for _, b := range a.Phases {
			if b.Water != nil && b.Gym != nil {
				return errFocusedWorkoutInvalid
			}
			if b.Water != nil {
				nodes += len(b.Water.Nodes)
			}
			if b.Gym != nil {
				exercises += len(b.Gym.Exercises)
			}
		}
	}
	if phases > focusedWorkoutMaxPhases || nodes > focusedMaxWaterNodes || exercises > focusedMaxGymExercises {
		return errFocusedWorkoutLimit
	}
	return nil
}
