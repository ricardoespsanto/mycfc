package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func placementReviewTestPayload(t *testing.T) trainingPlacementReviewPayload {
	t.Helper()
	x, c := placementTestCommand()
	source := &trainingPlacementSpan{ID: c.SourcePlacementID, GroupID: uuid.New(), GroupName: "Origem", StartsOn: "2027-03-01", Version: 4}
	projection, e := projectTrainingPlacement(c, source, "Destino")
	if e != nil {
		t.Fatal(e)
	}
	facts := sha256.Sum256([]byte("actual scoped facts fixture"))
	return trainingPlacementReviewPayload{ActorID: x.ActorID, Incarnation: uuid.New(), Epoch: 9, DecisionOn: "2027-03-01", Command: c, Source: source, TargetName: "Destino", FactsDigest: hex.EncodeToString(facts[:]), Projection: projection, Values: c.values()}
}
func TestTrainingPlacementReviewIsPurposeActorContextAndFactBound(t *testing.T) {
	p := placementReviewTestPayload(t)
	now := time.Date(2027, 3, 1, 12, 0, 0, 0, time.UTC)
	signer := focusedWorkoutSigner{key: [32]byte{1, 2, 3}}
	raw, token, e := signer.signPlacement(p, now)
	if e != nil {
		t.Fatal(e)
	}
	reviewed, e := signer.verifyPlacement(raw, token, p.ActorID, p.Command.ContextGroupID, p.Command.SubjectID, now.Add(time.Minute))
	if e != nil {
		t.Fatal(e)
	}
	if e = reviewed.matches(p, now.Add(2*time.Minute)); e != nil {
		t.Fatal(e)
	}
	if reviewed.matches(p, now.Add(-time.Second)) == nil {
		t.Fatal("accepted a decision before review issuance")
	}
	night := time.Date(2027, 3, 1, 23, 59, 0, 0, time.UTC)
	nightRaw, nightToken, e := signer.signPlacement(p, night)
	if e != nil {
		t.Fatal(e)
	}
	nightReview, e := signer.verifyPlacement(nightRaw, nightToken, p.ActorID, p.Command.ContextGroupID, p.Command.SubjectID, night)
	if e != nil {
		t.Fatal(e)
	}
	if nightReview.matches(p, night.Add(2*time.Minute)) == nil {
		t.Fatal("accepted a changed actual Lisbon day before token expiry")
	}
	for _, tc := range []struct {
		name                 string
		actor, group, person uuid.UUID
	}{
		{"actor", uuid.New(), p.Command.ContextGroupID, p.Command.SubjectID}, {"group", p.ActorID, uuid.New(), p.Command.SubjectID}, {"person", p.ActorID, p.Command.ContextGroupID, uuid.New()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, e := signer.verifyPlacement(raw, token, tc.actor, tc.group, tc.person, now); e == nil {
				t.Fatal("accepted another actor/context")
			}
		})
	}
	if _, e = signer.verifyPlacement(raw, token, p.ActorID, p.Command.ContextGroupID, p.Command.SubjectID, now.Add(focusedWorkoutReviewTTL)); e == nil {
		t.Fatal("accepted expired review")
	}
	altered := strings.Replace(raw, "Destino", "Outro", 1)
	if _, e = signer.verifyPlacement(altered, token, p.ActorID, p.Command.ContextGroupID, p.Command.SubjectID, now); e == nil {
		t.Fatal("accepted altered content")
	}
	otherRaw, otherToken, e := signer.signNativeFields([]byte(raw), p.Values, nil, reuseReviewPurpose, now)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = signer.verifyPlacement(otherRaw, otherToken, p.ActorID, p.Command.ContextGroupID, p.Command.SubjectID, now); e == nil {
		t.Fatal("accepted another task's proof")
	}
	mutations := []func(*trainingPlacementReviewPayload){func(q *trainingPlacementReviewPayload) { q.Epoch++ }, func(q *trainingPlacementReviewPayload) { q.Incarnation = uuid.New() }, func(q *trainingPlacementReviewPayload) { q.DecisionOn = "2027-03-02" }, func(q *trainingPlacementReviewPayload) { q.FactsDigest = strings.Repeat("0", 64) }}
	for i, mutate := range mutations {
		q := p
		mutate(&q)
		if reviewed.matches(q, now.Add(2*time.Minute)) == nil {
			t.Fatalf("accepted changed fact %d", i)
		}
	}
}
func TestTrainingPlacementReviewRejectsInconsistentOrSmuggledPayload(t *testing.T) {
	p := placementReviewTestPayload(t)
	now := time.Date(2027, 3, 1, 12, 0, 0, 0, time.UTC)
	s := focusedWorkoutSigner{key: [32]byte{4}}
	p.Values.Set("is_admin", "true")
	if _, _, e := s.signPlacement(p, now); e == nil {
		t.Fatal("signed hidden authority field")
	}
	p = placementReviewTestPayload(t)
	p.Projection.Return.GroupID = uuid.New()
	if _, _, e := s.signPlacement(p, now); e == nil {
		t.Fatal("signed substituted return group")
	}
	p = placementReviewTestPayload(t)
	p.Command.ExpectedRevision++
	if _, _, e := s.signPlacement(p, now); e == nil {
		t.Fatal("signed raw/head mismatch")
	}
	p = placementReviewTestPayload(t)
	raw, _ := json.Marshal(p)
	invalid := strings.Replace(string(raw), `"state_epoch":9`, `"state_epoch":9,"state_epoch":10`, 1)
	signed, token, e := s.signNativeFields([]byte(invalid), p.Values, nil, trainingPlacementReviewPurpose, now)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.verifyPlacement(signed, token, p.ActorID, p.Command.ContextGroupID, p.Command.SubjectID, now); e == nil {
		t.Fatal("accepted duplicate JSON key")
	}
}
