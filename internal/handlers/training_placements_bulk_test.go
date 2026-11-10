package handlers

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func placementSelectionFixture(t *testing.T) (trainingPlacementSelection, TrainingGroupRosterInput, time.Time) {
	t.Helper()
	p := placementReviewTestPayload(t)
	now := time.Date(2027, 3, 1, 12, 0, 0, 0, time.UTC)
	batch, member := uuid.New(), uuid.New()
	c := p.Command
	c.Kind = placementJoin
	c.SourcePlacementID = uuid.Nil
	c.TemporaryUntil = ""
	c.ContextGroupID = c.TargetGroupID
	c.ID = placementSelectionCommand(batch, c.SubjectID)
	p.Command = c
	p.Values = c.values()
	p.Source = nil
	p.Projection, _ = projectTrainingPlacement(c, nil, p.TargetName)
	selection := trainingPlacementSelection{ActorID: p.ActorID, GroupID: c.ContextGroupID, SeasonID: c.SeasonID, CommandID: batch, EffectiveOn: c.EffectiveOn, GroupVersion: now.Format(time.RFC3339Nano), Entries: []trainingPlacementSelectionEntry{{MembershipID: member, Review: p}}}
	on, _ := time.Parse("2006-01-02", c.EffectiveOn)
	return selection, TrainingGroupRosterInput{ActorID: p.ActorID, GroupID: c.ContextGroupID, SeasonID: c.SeasonID, CommandID: batch, EffectiveOn: on, ExpectedUpdatedAt: now, MembershipIDs: []uuid.UUID{member}}, now
}
func TestTrainingPlacementSelectionBindsCandidateIntentAndExactToken(t *testing.T) {
	p, in, now := placementSelectionFixture(t)
	s := focusedWorkoutSigner{key: [32]byte{16}}
	proof, e := s.signPlacementSelection(p, now)
	if e != nil {
		t.Fatal(e)
	}
	reviews, e := s.verifyPlacementSelection(proof, in, now)
	if e != nil || len(reviews) != 1 || !reviews[0].verified || reviews[0].Payload.Command.Kind != placementJoin {
		t.Fatal("selection verification", e)
	}
	again, e := s.verifyPlacementSelection(proof, in, now.Add(time.Minute))
	if e != nil || again[0].reviewSHA256 != reviews[0].reviewSHA256 || again[0].Payload.Command.ID != reviews[0].Payload.Command.ID {
		t.Fatal("identical posted proof changed replay identity", e)
	}
	for _, change := range []func(*TrainingGroupRosterInput){func(i *TrainingGroupRosterInput) { i.ActorID = uuid.New() }, func(i *TrainingGroupRosterInput) { i.GroupID = uuid.New() }, func(i *TrainingGroupRosterInput) { i.SeasonID = uuid.New() }, func(i *TrainingGroupRosterInput) { i.CommandID = uuid.New() }, func(i *TrainingGroupRosterInput) { i.EffectiveOn = i.EffectiveOn.AddDate(0, 0, 1) }, func(i *TrainingGroupRosterInput) { i.ExpectedUpdatedAt = i.ExpectedUpdatedAt.Add(time.Second) }, func(i *TrainingGroupRosterInput) { i.MembershipIDs = []uuid.UUID{uuid.New()} }, func(i *TrainingGroupRosterInput) { i.MembershipIDs = append(i.MembershipIDs, i.MembershipIDs[0]) }} {
		altered := in
		change(&altered)
		if _, e = s.verifyPlacementSelection(proof, altered, now); e == nil {
			t.Fatal("altered selection intent accepted")
		}
	}
	if _, e = s.verifyPlacementSelection(proof+"x", in, now); e == nil {
		t.Fatal("tampered proof accepted")
	}
	if _, e = s.verifyPlacementSelection(proof, in, now.Add(focusedWorkoutReviewTTL)); e == nil {
		t.Fatal("expired selection accepted")
	}
	if strings.Contains(proof, p.Entries[0].Review.TargetName) {
		t.Fatal("proof encoding malformed")
	}
}
func TestTrainingPlacementSelectionCannotCarryMovesOrDuplicatePeople(t *testing.T) {
	p, _, now := placementSelectionFixture(t)
	s := focusedWorkoutSigner{key: [32]byte{17}}
	original := p.Entries[0]
	p.Entries = append(p.Entries, original)
	p.Entries[1].MembershipID = uuid.New()
	if _, e := s.signPlacementSelection(p, now); e == nil {
		t.Fatal("same person through two memberships accepted")
	}
	p.Entries = p.Entries[:1]
	p.Entries[0].Review.Command.Kind = placementMove
	if _, e := s.signPlacementSelection(p, now); e == nil {
		t.Fatal("bulk source-ending move accepted")
	}
}

func TestTrainingPlacementSelectionFingerprintBindsExactSubset(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	first := placementSelectionFingerprint("verified-exact-token", []uuid.UUID{a, b})
	if first != placementSelectionFingerprint("verified-exact-token", []uuid.UUID{b, a}) {
		t.Fatal("selection ordering changed canonical intent")
	}
	if first == placementSelectionFingerprint("verified-exact-token", []uuid.UUID{a}) || first == placementSelectionFingerprint("different-token", []uuid.UUID{a, b}) {
		t.Fatal("subset/token substitution retained intent fingerprint")
	}
}
