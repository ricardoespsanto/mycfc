package handlers

import (
	"context"
	"errors"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type placementBatchFake struct {
	*placementTxFake
	prepared, touched, groupReads int
	replayMode                    string
}

func (q *placementBatchFake) PreparePlacementBatch(_ context.Context, _ TrainingGroupRosterInput, reviews []trainingPlacementReviewed) error {
	q.prepared++
	for i := 1; i < len(reviews); i++ {
		if reviews[i-1].Payload.Command.SubjectID.String() >= reviews[i].Payload.Command.SubjectID.String() {
			return errTrainingPlacementInvalid
		}
	}
	return nil
}
func (q *placementBatchFake) PlacementBatchGroup(_ context.Context, _, group uuid.UUID, touch bool) (TrainingGroupRecord, error) {
	q.groupReads++
	if touch {
		q.touched++
	}
	return TrainingGroupRecord{ID: group}, nil
}
func (q *placementBatchFake) TrainingPlacementApplyReplay(_ context.Context, p dbgen.TrainingPlacementApplyReplayParams) (dbgen.TrainingPlacementApplyReplayRow, error) {
	q.replayCalls++
	if q.replayMode == "all" || q.replayMode == "mixed" && q.replayCalls == 1 {
		return dbgen.TrainingPlacementApplyReplayRow{ChangeID: p.CommandID, HeadRevision: p.ExpectedRevision + 1, DestinationPlacementID: uuid.New(), Replayed: true}, nil
	}
	return dbgen.TrainingPlacementApplyReplayRow{}, pgx.ErrNoRows
}
func placementBatchFixture(t *testing.T) (*placementBatchFake, TrainingGroupRosterInput, []trainingPlacementReviewed) {
	t.Helper()
	q, r := placementTxFixture(t)
	c := r.Payload.Command
	c.Kind = placementJoin
	c.ContextGroupID = c.TargetGroupID
	c.SourcePlacementID = uuid.Nil
	c.TemporaryUntil = ""
	batch := uuid.New()
	c.ID = placementSelectionCommand(batch, c.SubjectID)
	q.timeline = []dbgen.TrainingPlacementTimelineRow{}
	read, e := readTrainingPlacement(t.Context(), q, r.Payload.ActorID, c, q.state, false)
	if e != nil {
		t.Fatal(e)
	}
	payload, e := read.proposal(r.Payload.ActorID, c)
	if e != nil {
		t.Fatal(e)
	}
	signer := focusedWorkoutSigner{key: [32]byte{23}}
	raw, token, e := signer.signPlacement(payload, q.clock)
	if e != nil {
		t.Fatal(e)
	}
	r, e = signer.verifyPlacement(raw, token, payload.ActorID, c.ContextGroupID, c.SubjectID, q.clock)
	if e != nil {
		t.Fatal(e)
	}
	q.result = dbgen.ApplyTrainingPlacementChangeRow{ChangeID: c.ID, HeadRevision: c.ExpectedRevision + 1, DestinationPlacementID: uuid.New()}
	date, _ := time.Parse("2006-01-02", c.EffectiveOn)
	in := TrainingGroupRosterInput{ActorID: payload.ActorID, GroupID: c.ContextGroupID, SeasonID: c.SeasonID, CommandID: batch, EffectiveOn: date, ExpectedUpdatedAt: q.clock, MembershipIDs: []uuid.UUID{q.subject.MembershipID}}
	return &placementBatchFake{placementTxFake: q}, in, []trainingPlacementReviewed{r}
}
func TestTrainingPlacementBatchReplayHasNoHeadOrGroupTouch(t *testing.T) {
	q, in, reviews := placementBatchFixture(t)
	q.replayMode = "all"
	before := q.subjectReads
	out, e := saveTrainingPlacementBatch(t.Context(), q, in, reviews)
	if e != nil || out.Added != 1 || out.Group.ID != in.GroupID || q.prepared != 0 || q.touched != 0 || q.applyCalls != 0 || q.subjectReads != before {
		t.Fatalf("out=%+v err=%v prepare=%d touch=%d writes=%d", out, e, q.prepared, q.touched, q.applyCalls)
	}
}
func TestTrainingPlacementBatchRejectsMixedReceiptsWithoutPartialRepair(t *testing.T) {
	q, in, reviews := placementBatchFixture(t)
	q.replayMode = "mixed"
	second := reviews[0]
	second.Payload.Command.SubjectID = uuid.New()
	second.Payload.Command.ID = placementSelectionCommand(in.CommandID, second.Payload.Command.SubjectID)
	second.Payload.Values = second.Payload.Command.values()
	second.Payload.Projection, _ = projectTrainingPlacement(second.Payload.Command, nil, second.Payload.TargetName)
	reviews = append(reviews, second)
	in.MembershipIDs = append(in.MembershipIDs, uuid.New())
	_, e := saveTrainingPlacementBatch(t.Context(), q, in, reviews)
	if !errors.Is(e, errTrainingPlacementChanged) || q.applyCalls != 0 || q.prepared != 0 || q.touched != 0 {
		t.Fatalf("err=%v writes=%d prepare=%d", e, q.applyCalls, q.prepared)
	}
}
func TestTrainingPlacementBatchChecksFreshFactsBeforeAnyWrite(t *testing.T) {
	q, in, reviews := placementBatchFixture(t)
	q.subject.Revision++
	_, e := saveTrainingPlacementBatch(t.Context(), q, in, reviews)
	if !errors.Is(e, errTrainingPlacementChanged) || q.applyCalls != 0 || q.touched != 0 {
		t.Fatalf("stale batch wrote: %v %d", e, q.applyCalls)
	}
}
func TestTrainingPlacementBatchInitialJoinsAreAtomicCallback(t *testing.T) {
	q, in, reviews := placementBatchFixture(t)
	out, e := saveTrainingPlacementBatch(t.Context(), q, in, reviews)
	if e != nil || out.Added != 1 || q.prepared != 1 || q.applyCalls != 1 || q.touched != 1 {
		t.Fatalf("out=%+v err=%v prepare=%d writes=%d touch=%d", out, e, q.prepared, q.applyCalls, q.touched)
	}
}
