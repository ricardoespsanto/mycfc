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
)

var errPlacementTestClockUnavailable = errors.New("synthetic placement clock unavailable")

type placementTransactionTestClock struct {
	calls, failAt int
	final         time.Time
}

func (c *placementTransactionTestClock) read(initial time.Time) (time.Time, error) {
	c.calls++
	if c.calls == c.failAt {
		return time.Time{}, errPlacementTestClockUnavailable
	}
	if c.calls > 1 && !c.final.IsZero() {
		return c.final, nil
	}
	return initial, nil
}

type placementTransactionFailureQueries struct {
	*placementTxFake
	decisionClock placementTransactionTestClock
}

func (q *placementTransactionFailureQueries) PlacementDecisionAt(context.Context) (time.Time, error) {
	return q.decisionClock.read(q.clock)
}

func (q *placementTransactionFailureQueries) useReceipt() {
	q.replayErr = nil
	r := q.result
	q.replay = dbgen.TrainingPlacementApplyReplayRow{ChangeID: r.ChangeID, HeadRevision: r.HeadRevision, SourcePlacementID: r.SourcePlacementID, DestinationPlacementID: r.DestinationPlacementID, ReturnPlacementID: r.ReturnPlacementID, Replayed: true}
}

func TestPlacementTransactionFailuresExposeNoSuccessfulReceipt(t *testing.T) {
	for _, tc := range []struct {
		name      string
		change    func(*placementTransactionFailureQueries, *trainingPlacementReviewed)
		want      error
		probes    int
		mutations int
	}{
		{"unverified proof", func(_ *placementTransactionFailureQueries, r *trainingPlacementReviewed) { r.verified = false }, errTrainingPlacementInvalid, 0, 0},
		{"runtime unavailable", func(q *placementTransactionFailureQueries, _ *trainingPlacementReviewed) {
			q.errorRuntime = errTrainingPlacementUnavailable
		}, errTrainingPlacementUnavailable, 0, 0},
		{"database clock unavailable", func(q *placementTransactionFailureQueries, _ *trainingPlacementReviewed) { q.decisionClock.failAt = 1 }, errPlacementTestClockUnavailable, 0, 0},
		{"receipt omitted the previous-group return", func(q *placementTransactionFailureQueries, _ *trainingPlacementReviewed) {
			q.useReceipt()
			q.replay.ReturnPlacementID = uuid.Nil
		}, errTrainingPlacementInvalid, 1, 0},
		{"receipt expired during lookup", func(q *placementTransactionFailureQueries, r *trainingPlacementReviewed) {
			q.useReceipt()
			q.decisionClock.final = r.expiresAt
		}, errTrainingPlacementInvalid, 1, 0},
		{"clock unavailable after receipt lookup", func(q *placementTransactionFailureQueries, _ *trainingPlacementReviewed) {
			q.useReceipt()
			q.decisionClock.failAt = 2
		}, errPlacementTestClockUnavailable, 1, 0},
		{"subject authority lost before write", func(q *placementTransactionFailureQueries, _ *trainingPlacementReviewed) {
			q.readErrors = map[string]error{"subject": pgx.ErrNoRows}
		}, pgx.ErrNoRows, 1, 0},
		{"write reports duplicate placement legs", func(q *placementTransactionFailureQueries, _ *trainingPlacementReviewed) {
			q.result.ReturnPlacementID = q.result.DestinationPlacementID
		}, errTrainingPlacementInvalid, 1, 1},
		{"clock unavailable after write", func(q *placementTransactionFailureQueries, _ *trainingPlacementReviewed) { q.decisionClock.failAt = 2 }, errPlacementTestClockUnavailable, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake, review := placementTxFixture(t)
			q := &placementTransactionFailureQueries{placementTxFake: fake}
			tc.change(q, &review)
			out, err := saveTrainingPlacement(t.Context(), q, review)
			if !errors.Is(err, tc.want) || out != (trainingPlacementSaveResult{}) || q.replayCalls != tc.probes || q.applyCalls != tc.mutations {
				t.Fatalf("result=%+v err=%v probes=%d writes=%d", out, err, q.replayCalls, q.applyCalls)
			}
		})
	}
}

type placementCancelFailureQueries struct {
	*placementCancelTxFake
	decisionClock         placementTransactionTestClock
	runtimeErr, cancelErr error
}

func (q *placementCancelFailureQueries) VerifyPlacementRuntime(context.Context) (db.TrainingScheduleState, error) {
	return q.state, q.runtimeErr
}

func (q *placementCancelFailureQueries) PlacementDecisionAt(context.Context) (time.Time, error) {
	return q.decisionClock.read(q.clock)
}

func (q *placementCancelFailureQueries) CancelTrainingPlacementChange(ctx context.Context, params dbgen.CancelTrainingPlacementChangeParams) (dbgen.CancelTrainingPlacementChangeRow, error) {
	out, _ := q.placementCancelTxFake.CancelTrainingPlacementChange(ctx, params)
	return out, q.cancelErr
}

func (q *placementCancelFailureQueries) useReceipt() {
	q.replayErr = nil
	r := q.result
	q.replay = dbgen.TrainingPlacementCancelReplayRow{ChangeID: r.ChangeID, HeadRevision: r.HeadRevision, SourcePlacementID: r.SourcePlacementID, DestinationPlacementID: r.DestinationPlacementID, Replayed: true}
}

func TestPlacementCancellationTransactionFailuresExposeNoSuccessfulReceipt(t *testing.T) {
	for _, tc := range []struct {
		name      string
		change    func(*placementCancelFailureQueries, *trainingPlacementCancelReviewed)
		want      error
		probes    int
		mutations int
	}{
		{"unverified proof", func(_ *placementCancelFailureQueries, r *trainingPlacementCancelReviewed) { r.verified = false }, errTrainingPlacementInvalid, 0, 0},
		{"runtime unavailable", func(q *placementCancelFailureQueries, _ *trainingPlacementCancelReviewed) {
			q.runtimeErr = errTrainingPlacementUnavailable
		}, errTrainingPlacementUnavailable, 0, 0},
		{"database clock unavailable", func(q *placementCancelFailureQueries, _ *trainingPlacementCancelReviewed) { q.decisionClock.failAt = 1 }, errPlacementTestClockUnavailable, 0, 0},
		{"authority denied during receipt lookup", func(q *placementCancelFailureQueries, _ *trainingPlacementCancelReviewed) {
			q.replayErr = errTrainingPlacementUnavailable
		}, errTrainingPlacementUnavailable, 1, 0},
		{"receipt head did not advance", func(q *placementCancelFailureQueries, r *trainingPlacementCancelReviewed) {
			q.useReceipt()
			q.replay.HeadRevision = r.Payload.Command.ExpectedRevision
		}, errTrainingPlacementInvalid, 1, 0},
		{"receipt expired during lookup", func(q *placementCancelFailureQueries, r *trainingPlacementCancelReviewed) {
			q.useReceipt()
			q.decisionClock.final = r.expiresAt
		}, errTrainingPlacementInvalid, 1, 0},
		{"clock unavailable after receipt lookup", func(q *placementCancelFailureQueries, _ *trainingPlacementCancelReviewed) {
			q.useReceipt()
			q.decisionClock.failAt = 2
		}, errPlacementTestClockUnavailable, 1, 0},
		{"authority lost before fresh cancellation read", func(q *placementCancelFailureQueries, _ *trainingPlacementCancelReviewed) {
			q.changeErr = pgx.ErrNoRows
		}, pgx.ErrNoRows, 1, 0},
		{"cancellation SQL rejected write", func(q *placementCancelFailureQueries, _ *trainingPlacementCancelReviewed) {
			q.cancelErr = errTrainingPlacementChanged
		}, errTrainingPlacementChanged, 1, 1},
		{"cancellation SQL returned a different restored placement", func(q *placementCancelFailureQueries, _ *trainingPlacementCancelReviewed) {
			q.result.SourcePlacementID = uuid.New()
		}, errTrainingPlacementInvalid, 1, 1},
		{"clock unavailable after write", func(q *placementCancelFailureQueries, _ *trainingPlacementCancelReviewed) { q.decisionClock.failAt = 2 }, errPlacementTestClockUnavailable, 1, 1},
		{"proof expired before commit", func(q *placementCancelFailureQueries, r *trainingPlacementCancelReviewed) {
			q.decisionClock.final = r.expiresAt
		}, errTrainingPlacementInvalid, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake, review := placementCancelTxFixture(t)
			q := &placementCancelFailureQueries{placementCancelTxFake: fake}
			tc.change(q, &review)
			out, err := saveTrainingPlacementCancellation(t.Context(), q, review)
			if !errors.Is(err, tc.want) || out != (trainingPlacementSaveResult{}) || q.replayCalls != tc.probes || q.cancelCalls != tc.mutations {
				t.Fatalf("result=%+v err=%v probes=%d writes=%d", out, err, q.replayCalls, q.cancelCalls)
			}
		})
	}
}
