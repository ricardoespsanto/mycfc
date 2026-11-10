package handlers

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/url"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const focusedWorkoutReviewPurpose = "mycfc/focused-water-create/v1"
const focusedWorkoutReviewTTL = 15 * time.Minute

type focusedWorkoutPayload struct {
	Proposal focusedWaterProposal `json:"proposal"`
	Draft    url.Values           `json:"draft"`
}
type focusedWorkoutSigner struct{ key [32]byte }

var focusedWorkoutReviews = func() focusedWorkoutSigner {
	var s focusedWorkoutSigner
	if _, err := rand.Read(s.key[:]); err != nil {
		panic("workout signing key unavailable")
	}
	return s
}()

func (s focusedWorkoutSigner) sign(payload focusedWorkoutPayload, now time.Time) (string, string, error) {
	if err := validateFocusedPayload(payload); err != nil {
		return "", "", err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", "", err
	}
	return s.signNative(raw, payload.Draft, payload.Proposal.Context, focusedWorkoutReviewPurpose, now)
}
func (s focusedWorkoutSigner) verify(raw, token string, actor, plan uuid.UUID, now time.Time) (focusedWorkoutPayload, focusedWorkoutReviewed, error) {
	var payload focusedWorkoutPayload
	var reviewed focusedWorkoutReviewed
	expires, err := s.verifyNative(raw, token, focusedWorkoutReviewPurpose, now)
	if err != nil {
		return payload, reviewed, err
	}
	if focusedStrictJSON([]byte(raw), &payload) != nil {
		return payload, reviewed, errFocusedWorkoutInvalid
	}
	canonical, _ := json.Marshal(payload)
	if !bytes.Equal(canonical, []byte(raw)) || payload.Proposal.Context.ActorID != actor || payload.Proposal.Context.PlanID != plan || validateFocusedPayload(payload) != nil {
		return focusedWorkoutPayload{}, reviewed, errFocusedWorkoutInvalid
	}
	reviewed = focusedWorkoutReviewed{Proposal: payload.Proposal, Token: token, verified: true, expiresAt: expires}
	return payload, reviewed, nil
}

func validateFocusedPayload(payload focusedWorkoutPayload) error {
	p := payload.Proposal
	c := p.Context
	if c.ActorID == uuid.Nil || c.PlanID == uuid.Nil || c.GroupID == uuid.Nil || c.SeasonID == uuid.Nil || c.DatabaseIncarnation == uuid.Nil || c.StateEpoch < 0 || c.SchemaContract != "schedule-p1" || !c.AuthoringEnabled || c.PlanUpdatedAt.IsZero() || !c.PlanUpdatedAt.Equal(c.PlanUpdatedAt.Truncate(time.Microsecond)) || !validWorkspaceDate(c.Day) {
		return errFocusedWorkoutInvalid
	}
	day, _ := time.Parse("2006-01-02", c.Day)
	if day.Before(c.WeekStart) || !day.Before(c.WeekStart.AddDate(0, 0, 7)) {
		return errFocusedWorkoutInvalid
	}
	draft, err := parseFocusedWorkoutDraft(payload.Draft)
	if err != nil {
		return err
	}
	expected, errs := normalizeFocusedWater(c, draft)
	if len(errs) > 0 {
		return errFocusedWorkoutInvalid
	}
	ids := map[uuid.UUID]bool{}
	for _, id := range []uuid.UUID{p.SessionID, p.Activity.ID} {
		if id == uuid.Nil || ids[id] {
			return errFocusedWorkoutInvalid
		}
		ids[id] = true
	}
	for _, phase := range p.Phases {
		if phase.ID == uuid.Nil || ids[phase.ID] {
			return errFocusedWorkoutInvalid
		}
		ids[phase.ID] = true
	}
	comparison := p
	comparison.SessionID = uuid.Nil
	comparison.Activity.ID = uuid.Nil
	comparison.Phases = append([]focusedWaterPhase(nil), p.Phases...)
	if comparison.Phases == nil {
		comparison.Phases = []focusedWaterPhase{}
	}
	for i := range comparison.Phases {
		comparison.Phases[i].ID = uuid.Nil
	}
	a, _ := json.Marshal(comparison)
	b, _ := json.Marshal(expected)
	if !bytes.Equal(a, b) {
		return errFocusedWorkoutInvalid
	}
	return nil
}
func focusedStrictJSON(raw []byte, dest any) error {
	if !utf8.Valid(raw) || len(raw) > focusedWorkoutMaxRequestBytes {
		return errFocusedWorkoutInvalid
	}
	tokens := json.NewDecoder(bytes.NewReader(raw))
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 64 {
			return errFocusedWorkoutInvalid
		}
		token, err := tokens.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for tokens.More() {
				keyToken, e := tokens.Token()
				if e != nil {
					return e
				}
				key, ok := keyToken.(string)
				if !ok || seen[key] {
					return errFocusedWorkoutInvalid
				}
				seen[key] = true
				if e = walk(depth + 1); e != nil {
					return e
				}
			}
		case '[':
			for tokens.More() {
				if e := walk(depth + 1); e != nil {
					return e
				}
			}
		default:
			return errFocusedWorkoutInvalid
		}
		_, err = tokens.Token()
		return err
	}
	if err := walk(0); err != nil {
		return err
	}
	if _, err := tokens.Token(); err != io.EOF {
		return errFocusedWorkoutInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(dest) != nil {
		return errFocusedWorkoutInvalid
	}
	return nil
}
