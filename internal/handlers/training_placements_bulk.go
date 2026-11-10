package handlers

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

const placementSelectionPurpose = "mycfc/training-placement-selection/v1"
const placementSelectionMaxBytes = 60 << 10

type trainingPlacementSelectionEntry struct {
	MembershipID uuid.UUID                      `json:"membership_id"`
	Review       trainingPlacementReviewPayload `json:"review"`
}
type trainingPlacementSelection struct {
	ActorID      uuid.UUID                         `json:"actor_id"`
	GroupID      uuid.UUID                         `json:"group_id"`
	SeasonID     uuid.UUID                         `json:"season_id"`
	CommandID    uuid.UUID                         `json:"command_id"`
	EffectiveOn  string                            `json:"effective_on"`
	GroupVersion string                            `json:"group_version"`
	Entries      []trainingPlacementSelectionEntry `json:"entries"`
}

func placementSelectionCommand(batch, person uuid.UUID) uuid.UUID {
	return uuid.NewSHA1(batch, person[:])
}
func validatePlacementSelection(p trainingPlacementSelection) error {
	if p.ActorID == uuid.Nil || p.GroupID == uuid.Nil || p.SeasonID == uuid.Nil || p.CommandID == uuid.Nil || !validWorkspaceDate(p.EffectiveOn) || len(p.Entries) == 0 || len(p.Entries) > 20 {
		return errTrainingPlacementInvalid
	}
	if _, ok := trainingGroupVersion(p.GroupVersion); !ok {
		return errTrainingPlacementInvalid
	}
	members, people := map[uuid.UUID]bool{}, map[uuid.UUID]bool{}
	for _, e := range p.Entries {
		c := e.Review.Command
		if e.MembershipID == uuid.Nil || members[e.MembershipID] || people[c.SubjectID] || validateTrainingPlacementReview(e.Review) != nil || e.Review.ActorID != p.ActorID || c.ContextGroupID != p.GroupID || c.TargetGroupID != p.GroupID || c.SeasonID != p.SeasonID || c.EffectiveOn != p.EffectiveOn || c.Kind != placementJoin || c.SourcePlacementID != uuid.Nil || c.ID != placementSelectionCommand(p.CommandID, c.SubjectID) {
			return errTrainingPlacementInvalid
		}
		members[e.MembershipID] = true
		people[c.SubjectID] = true
	}
	return nil
}
func (s focusedWorkoutSigner) signPlacementSelection(p trainingPlacementSelection, now time.Time) (string, error) {
	if e := validatePlacementSelection(p); e != nil {
		return "", e
	}
	raw, e := json.Marshal(p)
	if e != nil {
		return "", e
	}
	_, token, e := s.signNativeFields(raw, nil, nil, placementSelectionPurpose, now)
	if e != nil {
		return "", e
	}
	proof := base64.RawURLEncoding.EncodeToString(raw) + "." + token
	if len(proof) > placementSelectionMaxBytes {
		return "", errTrainingPlacementLimit
	}
	return proof, nil
}
func (s focusedWorkoutSigner) verifyPlacementSelection(proof string, in TrainingGroupRosterInput, now time.Time) ([]trainingPlacementReviewed, error) {
	if len(proof) > placementSelectionMaxBytes || len(in.MembershipIDs) == 0 || len(in.MembershipIDs) > 20 {
		return nil, errTrainingPlacementInvalid
	}
	encoded, token, ok := strings.Cut(proof, ".")
	if !ok {
		return nil, errTrainingPlacementInvalid
	}
	raw, e := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if e != nil || base64.RawURLEncoding.EncodeToString(raw) != encoded {
		return nil, errTrainingPlacementInvalid
	}
	expiry, e := s.verifyNative(string(raw), token, placementSelectionPurpose, now)
	if e != nil {
		return nil, errTrainingPlacementInvalid
	}
	var p trainingPlacementSelection
	if focusedStrictJSON(raw, &p) != nil || validatePlacementSelection(p) != nil {
		return nil, errTrainingPlacementInvalid
	}
	canonical, e := json.Marshal(p)
	if e != nil || !bytes.Equal(canonical, raw) || p.ActorID != in.ActorID || p.GroupID != in.GroupID || p.SeasonID != in.SeasonID || p.CommandID != in.CommandID || p.EffectiveOn != in.EffectiveOn.Format("2006-01-02") || p.GroupVersion != in.ExpectedUpdatedAt.Format(time.RFC3339Nano) {
		return nil, errTrainingPlacementInvalid
	}
	entries := map[uuid.UUID]trainingPlacementReviewPayload{}
	for _, entry := range p.Entries {
		entries[entry.MembershipID] = entry.Review
	}
	fingerprint := placementSelectionFingerprint(token, in.MembershipIDs)
	out := make([]trainingPlacementReviewed, 0, len(in.MembershipIDs))
	seen := map[uuid.UUID]bool{}
	for _, id := range in.MembershipIDs {
		review, found := entries[id]
		if !found || seen[id] {
			return nil, errTrainingPlacementInvalid
		}
		seen[id] = true
		out = append(out, trainingPlacementReviewed{Payload: review, issuedAt: expiry.Add(-focusedWorkoutReviewTTL), expiresAt: expiry, reviewSHA256: fingerprint, verified: true})
	}
	return out, nil
}

// The verified offer token and exact selected subset form the bulk intent.
// Framing keeps token bytes and the sorted typed UUIDs unambiguous.
func placementSelectionFingerprint(token string, selected []uuid.UUID) string {
	ids := append([]uuid.UUID(nil), selected...)
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	h := sha256.New()
	publicationReviewWriteField(h, []byte(placementSelectionPurpose+"/selected"))
	publicationReviewWriteField(h, []byte(token))
	publicationReviewWriteUint64(h, uint64(len(ids)))
	for _, id := range ids {
		publicationReviewWriteField(h, id[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}
