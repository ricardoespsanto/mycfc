package handlers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type placementTxFake struct {
	*placementReadFake
	state                             db.TrainingScheduleState
	clock, lastClock                  time.Time
	replayErr, applyErr, errorRuntime error
	replay                            dbgen.TrainingPlacementApplyReplayRow
	result                            dbgen.ApplyTrainingPlacementChangeRow
	replayParams                      dbgen.TrainingPlacementApplyReplayParams
	params                            dbgen.ApplyTrainingPlacementChangeParams
	ops                               []string
	applyCalls, replayCalls           int
}

func (q *placementTxFake) VerifyPlacementRuntime(context.Context) (db.TrainingScheduleState, error) {
	q.ops = append(q.ops, "runtime")
	return q.state, q.errorRuntime
}
func (q *placementTxFake) PlacementDecisionAt(context.Context) (time.Time, error) {
	q.ops = append(q.ops, "clock")
	if q.applyCalls > 0 && !q.lastClock.IsZero() {
		return q.lastClock, nil
	}
	return q.clock, nil
}
func (q *placementTxFake) TrainingPlacementApplyReplay(_ context.Context, p dbgen.TrainingPlacementApplyReplayParams) (dbgen.TrainingPlacementApplyReplayRow, error) {
	q.ops = append(q.ops, "readonly-replay")
	q.replayCalls++
	q.replayParams = p
	return q.replay, q.replayErr
}
func (q *placementTxFake) ApplyTrainingPlacementChange(_ context.Context, p dbgen.ApplyTrainingPlacementChangeParams) (dbgen.ApplyTrainingPlacementChangeRow, error) {
	q.ops = append(q.ops, "apply")
	q.applyCalls++
	q.params = p
	return q.result, q.applyErr
}
func placementTxFixture(t *testing.T) (*placementTxFake, trainingPlacementReviewed) {
	t.Helper()
	f, x, c, state := placementReadFixture()
	read, e := readTrainingPlacement(t.Context(), f, x.ActorID, c, state, false)
	if e != nil {
		t.Fatal(e)
	}
	payload, e := read.proposal(x.ActorID, c)
	if e != nil {
		t.Fatal(e)
	}
	now := f.subject.DecisionAt.Time
	signer := focusedWorkoutSigner{key: [32]byte{7}}
	raw, token, e := signer.signPlacement(payload, now)
	if e != nil {
		t.Fatal(e)
	}
	reviewed, e := signer.verifyPlacement(raw, token, x.ActorID, c.ContextGroupID, c.SubjectID, now)
	if e != nil {
		t.Fatal(e)
	}
	return &placementTxFake{placementReadFake: f, state: state, clock: now, replayErr: pgx.ErrNoRows, result: dbgen.ApplyTrainingPlacementChangeRow{ChangeID: c.ID, HeadRevision: c.ExpectedRevision + 1, SourcePlacementID: c.SourcePlacementID, DestinationPlacementID: uuid.New(), ReturnPlacementID: uuid.New()}}, reviewed
}
func TestTrainingPlacementTransactionReplaysBeforeChangedOwnFacts(t *testing.T) {
	q, r := placementTxFixture(t)
	q.replayErr = nil
	q.replay = dbgen.TrainingPlacementApplyReplayRow{ChangeID: q.result.ChangeID, HeadRevision: q.result.HeadRevision, SourcePlacementID: q.result.SourcePlacementID, DestinationPlacementID: q.result.DestinationPlacementID, ReturnPlacementID: q.result.ReturnPlacementID, Replayed: true}
	q.subject.Revision += 5
	q.source.Revision += 5
	q.timeline = nil
	q.instructions = nil
	reads := q.subjectReads
	out, e := saveTrainingPlacement(t.Context(), q, r)
	if e != nil || !out.Replayed || q.applyCalls != 0 || q.subjectReads != reads || q.replayParams.ReviewSha256 != r.reviewSHA256 {
		t.Fatalf("replay %+v %v apply%d", out, e, q.applyCalls)
	}
	if len(q.ops) < 3 || q.ops[0] != "runtime" || q.ops[1] != "clock" || q.ops[2] != "readonly-replay" {
		t.Fatalf("wrong first admission/replay order: %v", q.ops)
	}
}
func TestTrainingPlacementTransactionMatchesWholeFactsBeforeFirstWrite(t *testing.T) {
	q, r := placementTxFixture(t)
	out, e := saveTrainingPlacement(t.Context(), q, r)
	if e != nil || out.Replayed || q.applyCalls != 1 || q.params.CommandID != r.Payload.Command.ID || q.params.ReviewSha256 != r.reviewSHA256 {
		t.Fatalf("save %+v %v", out, e)
	}
	// A publication may add withdrawal instructions without changing the person
	// head. The full signed facts, not only the revision/date/target, must differ.
	q, r = placementTxFixture(t)
	q.instructions = append(q.instructions, dbgen.TrainingPlacementInstructionReviewRow{PrescriptionID: uuid.New(), SessionID: uuid.New(), SessionOn: placementDate("2027-04-02"), DecisionAt: q.subject.DecisionAt, WillWithdraw: true})
	if _, e = saveTrainingPlacement(t.Context(), q, r); !errors.Is(e, errTrainingPlacementChanged) || q.applyCalls != 0 {
		t.Fatalf("unreviewed publication was silently included: %v apply%d", e, q.applyCalls)
	}
	q, r = placementTxFixture(t)
	q.state.StateEpoch++
	if _, e = saveTrainingPlacement(t.Context(), q, r); !errors.Is(e, errTrainingPlacementChanged) || q.replayCalls != 0 || q.applyCalls != 0 {
		t.Fatalf("changed runtime admitted: %v", e)
	}
	q, r = placementTxFixture(t)
	q.replayErr = &pgconn.PgError{Code: "42501", Message: "private SQL diagnostic"}
	if _, e = saveTrainingPlacement(t.Context(), q, r); e == nil || q.applyCalls != 0 {
		t.Fatal("denied replay fell through to write")
	}
}
func TestTrainingPlacementTransactionChecksClockAfterSQLBeforeCommit(t *testing.T) {
	q, r := placementTxFixture(t)
	q.lastClock = r.expiresAt
	if _, e := saveTrainingPlacement(t.Context(), q, r); !errors.Is(e, errTrainingPlacementInvalid) || q.applyCalls != 1 {
		t.Fatalf("expired while SQL held locks: %v calls%d", e, q.applyCalls)
	}
	q, r = placementTxFixture(t)
	night := time.Date(2027, 3, 1, 23, 59, 0, 0, time.UTC)
	q.clock = night
	q.subject.DecisionAt.Time = night
	q.source.DecisionAt.Time = night
	for i := range q.instructions {
		q.instructions[i].DecisionAt.Time = night
	}
	signer := focusedWorkoutSigner{key: [32]byte{3}}
	raw, token, e := signer.signPlacement(r.Payload, night)
	if e != nil {
		t.Fatal(e)
	}
	r, e = signer.verifyPlacement(raw, token, r.Payload.ActorID, r.Payload.Command.ContextGroupID, r.Payload.Command.SubjectID, night)
	if e != nil {
		t.Fatal(e)
	}
	q.lastClock = night.Add(2 * time.Minute)
	if _, e = saveTrainingPlacement(t.Context(), q, r); !errors.Is(e, errTrainingPlacementInvalid) || q.applyCalls != 1 {
		t.Fatalf("Lisbon day changed in SQL: %v", e)
	}
}
func TestTrainingPlacementTransactionOutcomeIsNeverFalseRollback(t *testing.T) {
	server := &pgconn.PgError{Code: "40001", Message: "sensitive diagnostic"}
	e := trainingPlacementTransactionError(db.SessionTxResult{Outcome: db.SessionTxOutcomeUnknown}, server)
	if !errors.Is(e, db.ErrCommitOutcomeUnknown) || errors.Is(e, errTrainingPlacementChanged) {
		t.Fatal("unknown COMMIT became acknowledged rollback")
	}
	if e := trainingPlacementTransactionError(db.SessionTxResult{Outcome: db.SessionTxCommitted, CleanupFailed: true}, errors.New("late cleanup")); e != nil {
		t.Fatal("acknowledged commit became failure")
	}
	for _, code := range []string{"40001", "40P01", "55P03", "23P01"} {
		if e := trainingPlacementTransactionError(db.SessionTxResult{}, &pgconn.PgError{Code: code}); !errors.Is(e, errTrainingPlacementChanged) {
			t.Fatalf("unmapped conflict%s: %v", code, e)
		}
	}
	if e := trainingPlacementTransactionError(db.SessionTxResult{}, &pgconn.PgError{Code: "42501"}); !errors.Is(e, pgx.ErrNoRows) {
		t.Fatal("scope denial not genericnotfound")
	}
}

type placementCancelTxFake struct {
	*placementReadFake
	change                                dbgen.TrainingPlacementChangeForReviewRow
	state                                 db.TrainingScheduleState
	clock                                 time.Time
	replayErr                             error
	replay                                dbgen.TrainingPlacementCancelReplayRow
	result                                dbgen.CancelTrainingPlacementChangeRow
	replayCalls, cancelCalls, changeReads int
	fingerprint                           string
}

func (q *placementCancelTxFake) VerifyPlacementRuntime(context.Context) (db.TrainingScheduleState, error) {
	return q.state, nil
}
func (q *placementCancelTxFake) PlacementDecisionAt(context.Context) (time.Time, error) {
	return q.clock, nil
}
func (q *placementCancelTxFake) TrainingPlacementChangeForReview(context.Context, dbgen.TrainingPlacementChangeForReviewParams) (dbgen.TrainingPlacementChangeForReviewRow, error) {
	q.changeReads++
	return q.change, nil
}
func (q *placementCancelTxFake) TrainingPlacementCancelReplay(_ context.Context, p dbgen.TrainingPlacementCancelReplayParams) (dbgen.TrainingPlacementCancelReplayRow, error) {
	q.replayCalls++
	q.fingerprint = p.ReviewSha256
	return q.replay, q.replayErr
}
func (q *placementCancelTxFake) CancelTrainingPlacementChange(_ context.Context, p dbgen.CancelTrainingPlacementChangeParams) (dbgen.CancelTrainingPlacementChangeRow, error) {
	q.cancelCalls++
	q.fingerprint = p.ReviewSha256
	return q.result, nil
}
func placementCancelTxFixture(t *testing.T) (*placementCancelTxFake, trainingPlacementCancelReviewed) {
	t.Helper()
	s, _ := placementHTTPFixture(t)
	x := s.cancellation
	q := &placementCancelTxFake{placementReadFake: s.q, change: x.Change, state: s.state, clock: x.Change.DecisionAt.Time, replayErr: pgx.ErrNoRows}
	q.source.Version = *x.Change.SourceVersion
	q.source.EndsOn = x.Change.SourceEndsOn
	q.source.Revision = x.Change.Revision
	q.timeline[0].Version = q.source.Version
	q.timeline[0].EndsOn = q.source.EndsOn
	q.timeline[0].Revision = q.source.Revision
	read, e := readTrainingPlacementCancellation(t.Context(), q, s.actor, s.command.ContextGroupID, s.command.SubjectID, s.command.ID, s.state)
	if e != nil {
		t.Fatal(e)
	}
	c := trainingPlacementCancellation{ID: uuid.New(), ChangeID: s.command.ID, SubjectID: s.command.SubjectID, ProgrammeID: s.command.ProgrammeID, SeasonID: s.command.SeasonID, ContextGroupID: s.command.ContextGroupID, ExpectedRevision: x.Change.Revision}
	p, e := read.proposal(s.actor, c)
	if e != nil {
		t.Fatal(e)
	}
	signer := focusedWorkoutSigner{key: [32]byte{9}}
	raw, token, e := signer.signPlacementCancel(p, q.clock)
	if e != nil {
		t.Fatal(e)
	}
	r, e := signer.verifyPlacementCancel(raw, token, s.actor, c.ContextGroupID, c.SubjectID, q.clock)
	if e != nil {
		t.Fatal(e)
	}
	q.result = dbgen.CancelTrainingPlacementChangeRow{ChangeID: c.ID, HeadRevision: c.ExpectedRevision + 1, SourcePlacementID: optionalPlacementUUID(x.Change.SourcePlacementID), DestinationPlacementID: optionalPlacementUUID(x.Change.DestinationPlacementID)}
	return q, r
}
func TestTrainingPlacementCancellationTransactionNewAndExactProofReplay(t *testing.T) {
	q, r := placementCancelTxFixture(t)
	out, e := saveTrainingPlacementCancellation(t.Context(), q, r)
	if e != nil || out.ChangeID != r.Payload.Command.ID || q.cancelCalls != 1 || q.fingerprint != r.reviewSHA256 || out.ReturnPlacementID != uuid.Nil {
		t.Fatalf("cancel %+v %v", out, e)
	}
	q, r = placementCancelTxFixture(t)
	q.replayErr = nil
	q.replay = dbgen.TrainingPlacementCancelReplayRow{ChangeID: q.result.ChangeID, HeadRevision: q.result.HeadRevision, SourcePlacementID: q.result.SourcePlacementID, DestinationPlacementID: q.result.DestinationPlacementID, Replayed: true}
	q.change.CanCancel = false
	q.change.CancellationID = &q.result.ChangeID
	reads := q.changeReads
	out, e = saveTrainingPlacementCancellation(t.Context(), q, r)
	if e != nil || !out.Replayed || q.cancelCalls != 0 || q.changeReads != reads {
		t.Fatalf("cancel replay reread alreadycancelledfacts %+v %v", out, e)
	}
	q, r = placementCancelTxFixture(t)
	q.change.CanCancel = false
	if _, e = saveTrainingPlacementCancellation(t.Context(), q, r); !errors.Is(e, errTrainingPlacementChanged) || q.cancelCalls != 0 {
		t.Fatalf("cancellation eligibility drift ignored: %v", e)
	}
}

func TestTrainingPlacementRetainedReplayCrossesLisbonMidnightWithinLifetime(t *testing.T) {
	q, r := placementTxFixture(t)
	night := time.Date(2027, 3, 1, 23, 59, 0, 0, lisbonLocation())
	signer := focusedWorkoutSigner{key: [32]byte{21}}
	raw, token, e := signer.signPlacement(r.Payload, night)
	if e != nil {
		t.Fatal(e)
	}
	r, e = signer.verifyPlacement(raw, token, r.Payload.ActorID, r.Payload.Command.ContextGroupID, r.Payload.Command.SubjectID, night)
	if e != nil {
		t.Fatal(e)
	}
	q.clock = night.Add(2 * time.Minute)
	q.replayErr = nil
	q.replay = dbgen.TrainingPlacementApplyReplayRow{ChangeID: q.result.ChangeID, HeadRevision: q.result.HeadRevision, SourcePlacementID: q.result.SourcePlacementID, DestinationPlacementID: q.result.DestinationPlacementID, ReturnPlacementID: q.result.ReturnPlacementID, Replayed: true}
	reads := q.subjectReads
	out, e := saveTrainingPlacement(t.Context(), q, r)
	if e != nil || !out.Replayed || q.applyCalls != 0 || q.subjectReads != reads {
		t.Fatalf("retained apply receipt rejected/re-read acrossmidnight: %+v %v", out, e)
	}
	q.clock = r.expiresAt
	probes := q.replayCalls
	if _, e = saveTrainingPlacement(t.Context(), q, r); !errors.Is(e, errTrainingPlacementInvalid) || q.replayCalls != probes {
		t.Fatal("expired apply receipt reached lookup")
	}
	// No retained result means a new operation: the changed calendar boundary
	// must refuse before any fresh business read/write.
	q.clock = night.Add(2 * time.Minute)
	q.replayErr = pgx.ErrNoRows
	reads = q.subjectReads
	if _, e = saveTrainingPlacement(t.Context(), q, r); !errors.Is(e, errTrainingPlacementInvalid) || q.applyCalls != 0 || q.subjectReads != reads {
		t.Fatal("new operation silently crossed reviewed day")
	}
}
func TestTrainingPlacementCancelRetainedReplayCrossesLisbonMidnightWithinLifetime(t *testing.T) {
	q, r := placementCancelTxFixture(t)
	day, e := time.ParseInLocation("2006-01-02", r.Payload.DecisionOn, lisbonLocation())
	if e != nil {
		t.Fatal(e)
	}
	night := time.Date(day.Year(), day.Month(), day.Day(), 23, 59, 0, 0, lisbonLocation())
	signer := focusedWorkoutSigner{key: [32]byte{22}}
	raw, token, e := signer.signPlacementCancel(r.Payload, night)
	if e != nil {
		t.Fatal(e)
	}
	r, e = signer.verifyPlacementCancel(raw, token, r.Payload.ActorID, r.Payload.Command.ContextGroupID, r.Payload.Command.SubjectID, night)
	if e != nil {
		t.Fatal(e)
	}
	q.clock = night.Add(2 * time.Minute)
	q.replayErr = nil
	q.replay = dbgen.TrainingPlacementCancelReplayRow{ChangeID: q.result.ChangeID, HeadRevision: q.result.HeadRevision, SourcePlacementID: q.result.SourcePlacementID, DestinationPlacementID: q.result.DestinationPlacementID, Replayed: true}
	q.change.CanCancel = false
	reads := q.changeReads
	out, e := saveTrainingPlacementCancellation(t.Context(), q, r)
	if e != nil || !out.Replayed || q.cancelCalls != 0 || q.changeReads != reads {
		t.Fatalf("retained cancel receipt rejected/re-read acrossmidnight: %+v %v", out, e)
	}
	q.clock = r.expiresAt
	probes := q.replayCalls
	if _, e = saveTrainingPlacementCancellation(t.Context(), q, r); !errors.Is(e, errTrainingPlacementInvalid) || q.replayCalls != probes {
		t.Fatal("expired cancel receipt reached lookup")
	}
	q.clock = night.Add(2 * time.Minute)
	q.replayErr = pgx.ErrNoRows
	reads = q.changeReads
	if _, e = saveTrainingPlacementCancellation(t.Context(), q, r); !errors.Is(e, errTrainingPlacementInvalid) || q.cancelCalls != 0 || q.changeReads != reads {
		t.Fatal("new cancellation silently crossed reviewed day")
	}
}
