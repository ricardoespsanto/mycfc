package handlers

import (
	"context"
	"errors"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type trainingPlacementBatchQueries interface {
	trainingPlacementCommandQueries
	// Actual actor/authority then all sorted subjects, all existing heads, then
	// group/version. No membership/group business lock may precede those heads.
	PreparePlacementBatch(context.Context, TrainingGroupRosterInput, []trainingPlacementReviewed) error
	PlacementBatchGroup(context.Context, uuid.UUID, uuid.UUID, bool) (TrainingGroupRecord, error)
}

func saveTrainingPlacementBatch(ctx context.Context, q trainingPlacementBatchQueries, in TrainingGroupRosterInput, reviews []trainingPlacementReviewed) (TrainingGroupRosterResult, error) {
	var out TrainingGroupRosterResult
	if len(reviews) == 0 || len(reviews) > 20 || len(reviews) != len(in.MembershipIDs) {
		return out, errTrainingPlacementInvalid
	}
	reviews = append([]trainingPlacementReviewed(nil), reviews...)
	sort.Slice(reviews, func(i, j int) bool {
		return reviews[i].Payload.Command.SubjectID.String() < reviews[j].Payload.Command.SubjectID.String()
	})
	state, e := q.VerifyPlacementRuntime(ctx)
	if e != nil {
		return out, e
	}
	now, e := q.PlacementDecisionAt(ctx)
	if e != nil {
		return out, e
	}
	replayed := 0
	for i, r := range reviews {
		c := r.Payload.Command
		if !r.verified || validateTrainingPlacementReview(r.Payload) != nil || !placementFingerprintValid(r.reviewSHA256) || c.Kind != placementJoin || c.ProgrammeID != reviews[0].Payload.Command.ProgrammeID || r.Payload.ActorID != in.ActorID || c.ContextGroupID != in.GroupID || c.TargetGroupID != in.GroupID || c.SeasonID != in.SeasonID || c.EffectiveOn != in.EffectiveOn.Format("2006-01-02") || c.ID != placementSelectionCommand(in.CommandID, c.SubjectID) || i > 0 && reviews[i-1].Payload.Command.SubjectID == c.SubjectID {
			return out, errTrainingPlacementInvalid
		}
		if state.RequiredReader < 4 || state.DatabaseIncarnation != r.Payload.Incarnation || state.StateEpoch != r.Payload.Epoch {
			return out, errTrainingPlacementChanged
		}
		if !placementReviewLifetimeValid(now, r.issuedAt, r.expiresAt) {
			return out, errTrainingPlacementInvalid
		}
		row, e := q.TrainingPlacementApplyReplay(ctx, placementReplayParams(placementApplyParams(r)))
		if e == nil {
			result := trainingPlacementSaveResult{ChangeID: row.ChangeID, SourcePlacementID: row.SourcePlacementID, DestinationPlacementID: row.DestinationPlacementID, ReturnPlacementID: row.ReturnPlacementID, Revision: row.HeadRevision, Replayed: row.Replayed}
			if !result.Replayed || !placementResultValid(result, c) {
				return out, errTrainingPlacementInvalid
			}
			replayed++
		} else if !errors.Is(e, pgx.ErrNoRows) {
			return out, e
		}
	}
	if replayed > 0 {
		if replayed != len(reviews) {
			return out, errTrainingPlacementChanged
		}
		// Reload protected group only; no subject/head/group lock or touch on replay.
		group, e := q.PlacementBatchGroup(ctx, in.ActorID, in.GroupID, false)
		if e != nil {
			return out, e
		}
		out.Group = group
		out.Added = replayed
		final, e := q.PlacementDecisionAt(ctx)
		if e != nil {
			return TrainingGroupRosterResult{}, e
		}
		for _, r := range reviews {
			if !placementReviewLifetimeValid(final, r.issuedAt, r.expiresAt) {
				return TrainingGroupRosterResult{}, errTrainingPlacementInvalid
			}
		}
		return out, nil
	}
	for _, r := range reviews {
		if !placementReviewClockValid(now, r.issuedAt, r.expiresAt, r.Payload.DecisionOn) {
			return out, errTrainingPlacementInvalid
		}
	}
	if e = q.PreparePlacementBatch(ctx, in, reviews); e != nil {
		return out, e
	}
	// Validate every displayed JOIN against fresh authoritative facts before the
	// first business mutation. The per-command SQL still checks at its own locks.
	for _, r := range reviews {
		if !placementReviewClockValid(now, r.issuedAt, r.expiresAt, r.Payload.DecisionOn) {
			return out, errTrainingPlacementInvalid
		}
		fresh, e := readTrainingPlacement(ctx, q, in.ActorID, r.Payload.Command, state, false)
		if e != nil {
			return out, e
		}
		payload, e := fresh.proposal(in.ActorID, r.Payload.Command)
		if e != nil {
			return out, e
		}
		decision, e := fresh.decisionAt()
		if e != nil {
			return out, e
		}
		if e = r.matches(payload, decision); e != nil {
			return out, e
		}
	}
	for _, r := range reviews {
		result, e := saveTrainingPlacement(ctx, q, r)
		if e != nil {
			return out, e
		}
		if result.Replayed {
			return out, errTrainingPlacementChanged
		}
		out.Added++
	}
	group, e := q.PlacementBatchGroup(ctx, in.ActorID, in.GroupID, true)
	if e != nil {
		return out, e
	}
	out.Group = group
	final, e := q.PlacementDecisionAt(ctx)
	if e != nil {
		return TrainingGroupRosterResult{}, e
	}
	for _, r := range reviews {
		if !placementReviewClockValid(final, r.issuedAt, r.expiresAt, r.Payload.DecisionOn) {
			return TrainingGroupRosterResult{}, errTrainingPlacementInvalid
		}
	}
	return out, nil
}
