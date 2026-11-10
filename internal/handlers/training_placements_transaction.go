package handlers

import (
	"context"
	"encoding/hex"
	"errors"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type trainingPlacementCommandQueries interface {
	trainingPlacementReadQueries
	VerifyPlacementRuntime(context.Context) (db.TrainingScheduleState, error)
	PlacementDecisionAt(context.Context) (time.Time, error)
	TrainingPlacementApplyReplay(context.Context, dbgen.TrainingPlacementApplyReplayParams) (dbgen.TrainingPlacementApplyReplayRow, error)
	ApplyTrainingPlacementChange(context.Context, dbgen.ApplyTrainingPlacementChangeParams) (dbgen.ApplyTrainingPlacementChangeRow, error)
}
type trainingPlacementCancelQueries interface {
	trainingPlacementCancellationQueries
	VerifyPlacementRuntime(context.Context) (db.TrainingScheduleState, error)
	PlacementDecisionAt(context.Context) (time.Time, error)
	TrainingPlacementCancelReplay(context.Context, dbgen.TrainingPlacementCancelReplayParams) (dbgen.TrainingPlacementCancelReplayRow, error)
	CancelTrainingPlacementChange(context.Context, dbgen.CancelTrainingPlacementChangeParams) (dbgen.CancelTrainingPlacementChangeRow, error)
}

func placementFingerprintValid(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && hex.EncodeToString(b) == s
}
func placementNullableID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}
func placementApplyParams(r trainingPlacementReviewed) dbgen.ApplyTrainingPlacementChangeParams {
	c := r.Payload.Command
	return dbgen.ApplyTrainingPlacementChangeParams{ActorID: r.Payload.ActorID, SubjectID: c.SubjectID, ProgrammeID: c.ProgrammeID, SeasonID: c.SeasonID, CommandID: c.ID, Kind: string(c.Kind), SourcePlacementID: placementNullableID(c.SourcePlacementID), TargetGroupID: placementNullableID(c.TargetGroupID), EffectiveOn: placementDate(c.EffectiveOn), TemporaryUntil: placementDate(c.TemporaryUntil), ExpectedRevision: c.ExpectedRevision, ReviewSha256: r.reviewSHA256}
}
func placementReplayParams(p dbgen.ApplyTrainingPlacementChangeParams) dbgen.TrainingPlacementApplyReplayParams {
	return dbgen.TrainingPlacementApplyReplayParams{ActorID: p.ActorID, SubjectID: p.SubjectID, ProgrammeID: p.ProgrammeID, SeasonID: p.SeasonID, CommandID: p.CommandID, Kind: p.Kind, SourcePlacementID: p.SourcePlacementID, TargetGroupID: p.TargetGroupID, EffectiveOn: p.EffectiveOn, TemporaryUntil: p.TemporaryUntil, ExpectedRevision: p.ExpectedRevision, ReviewSha256: p.ReviewSha256}
}
func placementReviewLifetimeValid(now, issued, expiry time.Time) bool {
	return !now.IsZero() && !now.Before(issued) && now.Before(expiry)
}
func placementReviewClockValid(now, issued, expiry time.Time, day string) bool {
	return placementReviewLifetimeValid(now, issued, expiry) && now.In(lisbonLocation()).Format("2006-01-02") == day
}
func placementResultValid(result trainingPlacementSaveResult, c trainingPlacementCommand) bool {
	if result.ChangeID != c.ID || result.Revision <= c.ExpectedRevision || result.SourcePlacementID != c.SourcePlacementID {
		return false
	}
	destRequired := c.Kind != placementLeave
	returnRequired := c.Kind == placementTemporary
	if (result.DestinationPlacementID != uuid.Nil) != destRequired || (result.ReturnPlacementID != uuid.Nil) != returnRequired {
		return false
	}
	seen := map[uuid.UUID]bool{result.ChangeID: true}
	for _, id := range []uuid.UUID{result.SourcePlacementID, result.DestinationPlacementID, result.ReturnPlacementID} {
		if id == uuid.Nil {
			continue
		}
		if seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

// Called only by the existing pinned SERIALIZABLE helper. It owns no COMMIT,
// retry, pool or session control and takes no actor SHARE before SQL's self lock.
func saveTrainingPlacement(ctx context.Context, q trainingPlacementCommandQueries, r trainingPlacementReviewed) (trainingPlacementSaveResult, error) {
	var out trainingPlacementSaveResult
	if !r.verified || !placementFingerprintValid(r.reviewSHA256) || validateTrainingPlacementReview(r.Payload) != nil {
		return out, errTrainingPlacementInvalid
	}
	state, e := q.VerifyPlacementRuntime(ctx)
	if e != nil {
		return out, e
	}
	if state.RequiredReader < 4 || state.DatabaseIncarnation != r.Payload.Incarnation || state.StateEpoch != r.Payload.Epoch {
		return out, errTrainingPlacementChanged
	}
	now, e := q.PlacementDecisionAt(ctx)
	if e != nil {
		return out, e
	}
	if !placementReviewLifetimeValid(now, r.issuedAt, r.expiresAt) {
		return out, errTrainingPlacementInvalid
	}
	params := placementApplyParams(r)
	// Read-only, current-authority/canonical/exact-token probe comes before facts
	// changed by this command's own successful commit. No write is used as a probe.
	replay, e := q.TrainingPlacementApplyReplay(ctx, placementReplayParams(params))
	if e == nil {
		out = trainingPlacementSaveResult{ChangeID: replay.ChangeID, SourcePlacementID: replay.SourcePlacementID, DestinationPlacementID: replay.DestinationPlacementID, ReturnPlacementID: replay.ReturnPlacementID, Revision: replay.HeadRevision, Replayed: replay.Replayed}
		if !out.Replayed || !placementResultValid(out, r.Payload.Command) {
			return trainingPlacementSaveResult{}, errTrainingPlacementInvalid
		}
		final, e := q.PlacementDecisionAt(ctx)
		if e != nil {
			return trainingPlacementSaveResult{}, e
		}
		if !placementReviewLifetimeValid(final, r.issuedAt, r.expiresAt) {
			return trainingPlacementSaveResult{}, errTrainingPlacementInvalid
		}
		return out, nil
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return out, e
	}
	if !placementReviewClockValid(now, r.issuedAt, r.expiresAt, r.Payload.DecisionOn) {
		return out, errTrainingPlacementInvalid
	}
	fresh, e := readTrainingPlacement(ctx, q, r.Payload.ActorID, r.Payload.Command, state, false)
	if e != nil {
		return out, e
	}
	current, e := fresh.proposal(r.Payload.ActorID, r.Payload.Command)
	if e != nil {
		return out, e
	}
	decision, e := fresh.decisionAt()
	if e != nil {
		return out, e
	}
	if e = r.matches(current, decision); e != nil {
		return out, e
	}
	row, e := q.ApplyTrainingPlacementChange(ctx, params)
	if e != nil {
		return out, e
	}
	out = trainingPlacementSaveResult{ChangeID: row.ChangeID, SourcePlacementID: row.SourcePlacementID, DestinationPlacementID: row.DestinationPlacementID, ReturnPlacementID: row.ReturnPlacementID, Revision: row.HeadRevision, Replayed: row.Replayed}
	if !placementResultValid(out, r.Payload.Command) {
		return trainingPlacementSaveResult{}, errTrainingPlacementInvalid
	}
	// SQL owns final date/scope/head/withdrawal checks. Check review lifetime at
	// the actual DB clock AFTER its locks/writes too; callback failure rolls all
	// writes back before the sole outer helper may commit.
	final, e := q.PlacementDecisionAt(ctx)
	if e != nil {
		return trainingPlacementSaveResult{}, e
	}
	if !placementReviewClockValid(final, r.issuedAt, r.expiresAt, r.Payload.DecisionOn) {
		return trainingPlacementSaveResult{}, errTrainingPlacementInvalid
	}
	return out, nil
}
func saveTrainingPlacementCancellation(ctx context.Context, q trainingPlacementCancelQueries, r trainingPlacementCancelReviewed) (trainingPlacementSaveResult, error) {
	var out trainingPlacementSaveResult
	if !r.verified || !placementFingerprintValid(r.reviewSHA256) || validateTrainingPlacementCancel(r.Payload) != nil {
		return out, errTrainingPlacementInvalid
	}
	state, e := q.VerifyPlacementRuntime(ctx)
	if e != nil {
		return out, e
	}
	if state.RequiredReader < 4 || state.DatabaseIncarnation != r.Payload.Incarnation || state.StateEpoch != r.Payload.Epoch {
		return out, errTrainingPlacementChanged
	}
	now, e := q.PlacementDecisionAt(ctx)
	if e != nil {
		return out, e
	}
	if !placementReviewLifetimeValid(now, r.issuedAt, r.expiresAt) {
		return out, errTrainingPlacementInvalid
	}
	c := r.Payload.Command
	params := dbgen.CancelTrainingPlacementChangeParams{ActorID: r.Payload.ActorID, CommandID: c.ChangeID, CancellationID: c.ID, ExpectedRevision: c.ExpectedRevision, ReviewSha256: r.reviewSHA256}
	replay, e := q.TrainingPlacementCancelReplay(ctx, dbgen.TrainingPlacementCancelReplayParams{ActorID: params.ActorID, CommandID: params.CommandID, CancellationID: params.CancellationID, ExpectedRevision: params.ExpectedRevision, ReviewSha256: params.ReviewSha256})
	if e == nil {
		out = trainingPlacementSaveResult{ChangeID: replay.ChangeID, SourcePlacementID: replay.SourcePlacementID, DestinationPlacementID: replay.DestinationPlacementID, ReturnPlacementID: replay.ReturnPlacementID, Revision: replay.HeadRevision, Replayed: replay.Replayed}
		if !out.Replayed || !placementCancelResultValid(out, r.Payload) {
			return trainingPlacementSaveResult{}, errTrainingPlacementInvalid
		}
		final, e := q.PlacementDecisionAt(ctx)
		if e != nil {
			return trainingPlacementSaveResult{}, e
		}
		if !placementReviewLifetimeValid(final, r.issuedAt, r.expiresAt) {
			return trainingPlacementSaveResult{}, errTrainingPlacementInvalid
		}
		return out, nil
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return out, e
	}
	if !placementReviewClockValid(now, r.issuedAt, r.expiresAt, r.Payload.DecisionOn) {
		return out, errTrainingPlacementInvalid
	}
	fresh, e := readTrainingPlacementCancellation(ctx, q, r.Payload.ActorID, c.ContextGroupID, c.SubjectID, c.ChangeID, state)
	if e != nil {
		return out, e
	}
	current, e := fresh.proposal(r.Payload.ActorID, c)
	if e != nil {
		return out, e
	}
	decision, e := placementClock(fresh.Change.DecisionAt)
	if e != nil {
		return out, e
	}
	if e = r.matches(current, decision); e != nil {
		return out, e
	}
	row, e := q.CancelTrainingPlacementChange(ctx, params)
	if e != nil {
		return out, e
	}
	out = trainingPlacementSaveResult{ChangeID: row.ChangeID, SourcePlacementID: row.SourcePlacementID, DestinationPlacementID: row.DestinationPlacementID, ReturnPlacementID: row.ReturnPlacementID, Revision: row.HeadRevision, Replayed: row.Replayed}
	if !placementCancelResultValid(out, r.Payload) {
		return trainingPlacementSaveResult{}, errTrainingPlacementInvalid
	}
	final, e := q.PlacementDecisionAt(ctx)
	if e != nil {
		return trainingPlacementSaveResult{}, e
	}
	if !placementReviewClockValid(final, r.issuedAt, r.expiresAt, r.Payload.DecisionOn) {
		return trainingPlacementSaveResult{}, errTrainingPlacementInvalid
	}
	return out, nil
}

func placementCancelResultValid(result trainingPlacementSaveResult, p trainingPlacementCancelPayload) bool {
	if result.ChangeID != p.Command.ID || result.Revision <= p.Command.ExpectedRevision || result.ReturnPlacementID != uuid.Nil {
		return false
	}
	var source, destination uuid.UUID
	for _, row := range p.After {
		if !row.Cancelled {
			if source != uuid.Nil {
				return false
			}
			source = row.ID
		} else if destination == uuid.Nil {
			destination = row.ID
		}
	}
	return result.SourcePlacementID == source && result.DestinationPlacementID == destination
}
