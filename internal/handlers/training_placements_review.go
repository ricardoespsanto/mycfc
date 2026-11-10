package handlers

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"time"

	"github.com/google/uuid"
)

// The digest commits to complete bounded, authoritative read facts. It is not a
// permission grant and never replaces SQL's current authority/head/clock checks.
type trainingPlacementReviewPayload struct {
	ActorID     uuid.UUID                   `json:"actor_id"`
	Incarnation uuid.UUID                   `json:"database_incarnation"`
	Epoch       int64                       `json:"state_epoch"`
	DecisionOn  string                      `json:"decision_on"`
	Command     trainingPlacementCommand    `json:"command"`
	Source      *trainingPlacementSpan      `json:"source"`
	TargetName  string                      `json:"target_name"`
	FactsDigest string                      `json:"facts_digest"`
	Projection  trainingPlacementProjection `json:"projection"`
	Values      url.Values                  `json:"values"`
}
type trainingPlacementReviewed struct {
	Payload      trainingPlacementReviewPayload
	issuedAt     time.Time
	expiresAt    time.Time
	reviewSHA256 string
	verified     bool
}

func validateTrainingPlacementReview(p trainingPlacementReviewPayload) error {
	if p.ActorID == uuid.Nil || p.Incarnation == uuid.Nil || p.Epoch < 1 || !validWorkspaceDate(p.DecisionOn) || p.Command.validate() != nil || p.DecisionOn > p.Command.EffectiveOn {
		return errTrainingPlacementInvalid
	}
	digest, e := hex.DecodeString(p.FactsDigest)
	if e != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != p.FactsDigest {
		return errTrainingPlacementInvalid
	}
	if !focusedGraphJSONEqual(p.Values, p.Command.values()) {
		return errTrainingPlacementInvalid
	}
	expected, e := projectTrainingPlacement(p.Command, p.Source, p.TargetName)
	if e != nil || !focusedGraphJSONEqual(expected, p.Projection) {
		return errTrainingPlacementInvalid
	}
	if p.Source != nil && p.Source.Version < 1 {
		return errTrainingPlacementInvalid
	}
	return nil
}
func (s focusedWorkoutSigner) signPlacement(p trainingPlacementReviewPayload, now time.Time) (string, string, error) {
	if err := validateTrainingPlacementReview(p); err != nil {
		return "", "", err
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return "", "", err
	}
	return s.signNativeFields(raw, p.Values, map[string]string{"group_id": p.Command.ContextGroupID.String(), "person_id": p.Command.SubjectID.String(), "command_id": p.Command.ID.String()}, trainingPlacementReviewPurpose, now)
}
func (s focusedWorkoutSigner) verifyPlacement(raw, token string, actor, group, person uuid.UUID, now time.Time) (trainingPlacementReviewed, error) {
	var out trainingPlacementReviewed
	expiry, err := s.verifyNative(raw, token, trainingPlacementReviewPurpose, now)
	if err != nil {
		return out, errTrainingPlacementInvalid
	}
	var p trainingPlacementReviewPayload
	if focusedStrictJSON([]byte(raw), &p) != nil || p.ActorID != actor || p.Command.ContextGroupID != group || p.Command.SubjectID != person {
		return out, errTrainingPlacementInvalid
	}
	encoded, err := json.Marshal(p)
	if err != nil || !bytes.Equal(encoded, []byte(raw)) || validateTrainingPlacementReview(p) != nil {
		return out, errTrainingPlacementInvalid
	}
	fingerprint := sha256.Sum256([]byte(token))
	return trainingPlacementReviewed{Payload: p, issuedAt: expiry.Add(-focusedWorkoutReviewTTL), expiresAt: expiry, reviewSHA256: hex.EncodeToString(fingerprint[:]), verified: true}, nil
}
func (r trainingPlacementReviewed) matches(p trainingPlacementReviewPayload, decisionAt time.Time) error {
	if !r.verified || decisionAt.IsZero() || decisionAt.Before(r.issuedAt) || !decisionAt.Before(r.expiresAt) {
		return errTrainingPlacementInvalid
	}
	if p.DecisionOn != decisionAt.In(lisbonLocation()).Format("2006-01-02") || validateTrainingPlacementReview(p) != nil || !focusedGraphJSONEqual(r.Payload, p) {
		return errTrainingPlacementChanged
	}
	return nil
}
