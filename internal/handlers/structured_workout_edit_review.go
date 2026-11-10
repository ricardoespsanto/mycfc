package handlers

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"
)

const focusedEditReviewPurpose = "mycfc/focused-workout-edit/v1"

type focusedEditReviewPayload struct {
	Original focusedEditBaseline `json:"original"`
	Proposal focusedWorkoutGraph `json:"proposal"`
	Draft    url.Values          `json:"draft"`
}
type focusedEditReviewed struct {
	Payload   focusedEditReviewPayload
	verified  bool
	expiresAt time.Time
}

func focusedEditMetadata(payload focusedEditReviewPayload) map[string]string {
	c, b := payload.Proposal.Context, payload.Original
	return map[string]string{"group_id": c.GroupID.String(), "plan_id": c.PlanID.String(), "session_id": b.SessionID.String(), "day": b.SourceDay, "expected_plan_updated_at": c.PlanUpdatedAt.Format(time.RFC3339Nano), "expected_session_updated_at": b.SessionVersion.Format(time.RFC3339Nano), "source_digest": b.OwnRawDigest, "database_incarnation": c.DatabaseIncarnation.String(), "state_epoch": strconv.FormatInt(c.StateEpoch, 10)}
}
func (s focusedWorkoutSigner) signEdit(payload focusedEditReviewPayload, source focusedEditSource, now time.Time) (string, string, error) {
	if !focusedEditEligible(source.Raw.Session, source.DecisionAt) {
		return "", "", errFocusedEditReadOnly
	}
	if err := validateFocusedEditPayload(payload, source); err != nil {
		return "", "", err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", "", err
	}
	return s.signNativeFields(raw, payload.Draft, focusedEditMetadata(payload), focusedEditReviewPurpose, now)
}

// Verifying the process-local token establishes no authority. The handler/store
// must compare its original baseline and validate against fresh scoped source;
// changed source may still need the verified own raw draft for terminal recovery.
func (s focusedWorkoutSigner) verifyEdit(raw, token string, actor, plan, session uuid.UUID, now time.Time) (focusedEditReviewPayload, focusedEditReviewed, error) {
	var payload focusedEditReviewPayload
	var reviewed focusedEditReviewed
	expires, err := s.verifyNative(raw, token, focusedEditReviewPurpose, now)
	if err != nil {
		return payload, reviewed, err
	}
	if focusedStrictJSON([]byte(raw), &payload) != nil {
		return payload, reviewed, errFocusedWorkoutInvalid
	}
	encoded, _ := json.Marshal(payload)
	p, b := payload.Proposal, payload.Original
	decoded, decodeErr := hex.DecodeString(b.OwnRawDigest)
	if !bytes.Equal(encoded, []byte(raw)) || decodeErr != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != b.OwnRawDigest || p.Context.ActorID != actor || p.Context.PlanID != plan || p.SessionID != session || b.SessionID != session || b.SessionVersion.IsZero() || !b.SessionVersion.Equal(b.SessionVersion.Truncate(time.Microsecond)) || !validWorkspaceDate(b.SourceDay) || p.Context.Day != b.SourceDay {
		return focusedEditReviewPayload{}, reviewed, errFocusedWorkoutInvalid
	}
	return payload, focusedEditReviewed{payload, true, expires}, nil
}
func validateFocusedEditReferences(source, proposed focusedWorkoutGraph) error {
	if err := validateFocusedReferenceShape(proposed); err != nil {
		return err
	}
	active := map[uuid.UUID]bool{}
	for _, r := range proposed.References.Profiles {
		active[r.ID] = r.IsActive
	}
	pins := map[uuid.UUID]uuid.UUID{}
	for _, a := range source.Activities {
		for _, b := range a.Phases {
			if b.Water != nil && b.Water.ProfileID != nil {
				pins[b.ID] = *b.Water.ProfileID
			}
		}
	}
	for _, a := range proposed.Activities {
		for _, b := range a.Phases {
			if b.Water == nil || b.Water.ProfileID == nil {
				continue
			}
			id := *b.Water.ProfileID
			if !active[id] && pins[b.ID] != id {
				return errFocusedWorkoutInvalid
			}
		}
	}
	return nil
}
func focusedEditWithoutIDs(p focusedWorkoutGraph) (focusedWorkoutGraph, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return focusedWorkoutGraph{}, err
	}
	var out focusedWorkoutGraph
	if err = json.Unmarshal(raw, &out); err != nil {
		return out, err
	}
	out.SessionID = uuid.Nil
	for ai := range out.Activities {
		a := &out.Activities[ai]
		a.ID = uuid.Nil
		for bi := range a.Phases {
			b := &a.Phases[bi]
			b.ID = uuid.Nil
			if b.Water != nil {
				for ni := range b.Water.Nodes {
					b.Water.Nodes[ni].ID = uuid.Nil
				}
			}
			if b.Gym != nil {
				for xi := range b.Gym.Exercises {
					b.Gym.Exercises[xi].ID = uuid.Nil
				}
			}
		}
	}
	return out, nil
}
func validateFocusedEditPayload(payload focusedEditReviewPayload, source focusedEditSource) error {
	if err := payload.Original.matches(source); err != nil {
		return err
	}
	p := payload.Proposal
	c := source.Context
	if !focusedSameCreateIdentity(c, p.Context) || !p.Context.PlanUpdatedAt.Equal(c.PlanUpdatedAt) || p.Context.StateEpoch != c.StateEpoch || !focusedGraphJSONEqual(p.Context, c) || p.SessionID != source.Raw.Session.ID || p.Kind != source.Raw.Session.EntryKind {
		return errFocusedWorkoutConflict
	}
	d, err := parseFocusedEditGraph(payload.Draft, source.Graph)
	if err != nil {
		return err
	}
	expected, e := normalizeFocusedEdit(source, d)
	if len(e) > 0 {
		return errFocusedWorkoutInvalid
	}
	if err = validateFocusedEditReferences(source.Graph, p); err != nil {
		return err
	}
	expected.References = p.References
	if _, err = buildFocusedEditDiff(source.Graph, p, d); err != nil {
		return err
	}
	a, err := focusedEditWithoutIDs(p)
	if err != nil {
		return err
	}
	b, err := focusedEditWithoutIDs(expected)
	if err != nil {
		return err
	}
	if !focusedGraphJSONEqual(a, b) {
		return errFocusedWorkoutInvalid
	}
	return nil
}
