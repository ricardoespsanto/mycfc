package handlers

import (
	"context"
	"encoding/json"

	"github.com/cfcoimbra/mycfc/internal/db"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Variations change instructions, never schedule precision. Use the existing
// modern publication admission and recipient fences rather than the v1 retiming guard.
func (s PostgresStructuredTrainingStore) createModernTrainingVariation(ctx context.Context, in dbgen.CreateTrainingVariationParams) (dbgen.TrainingVariation, error) {
	var result dbgen.TrainingVariation
	err := s.withModernTrainingVariation(ctx, in.CreatedByID, func(context.Context, dbgen.DBTX) (dbgen.CreateTrainingVariationParams, error) { return in, nil }, func(ctx context.Context, commands dbgen.DBTX, q *dbgen.Queries, current dbgen.CreateTrainingVariationParams) error {
		var patch map[string]json.RawMessage
		if err := json.Unmarshal(current.Patch, &patch); err != nil {
			return errPublicationRhythmReference
		}
		if raw, exists := patch["intensity_profile_id"]; exists {
			var text string
			if json.Unmarshal(raw, &text) != nil {
				return errPublicationRhythmReference
			}
			id, err := canonicalRhythmProfileID(text)
			if err != nil {
				return err
			}
			profiles, err := q.LockPublicationRhythmProfiles(ctx, dbgen.LockPublicationRhythmProfilesParams{ActorID: current.CreatedByID, PlanID: current.PlanID, ProfileIds: []uuid.UUID{id}})
			if err != nil {
				return publicationNowaitError(err)
			}
			if len(profiles) != 1 || profiles[0].ID != id || !profiles[0].IsActive {
				return errPublicationRhythmReference
			}
		}
		var err error
		result, err = q.CreateTrainingVariation(ctx, current)
		return err // Exact current actor, target, placement and subject scope are SQL-owned.
	})
	return result, err
}

func (s PostgresStructuredTrainingStore) retireModernTrainingVariation(ctx context.Context, in dbgen.RetireTrainingVariationParams) (int64, error) {
	var result int64
	actor := trainingWriteActor(ctx)
	if actor == uuid.Nil || in.RetiredByID == nil || *in.RetiredByID != actor {
		return result, pgx.ErrNoRows
	}
	err := s.withModernTrainingVariation(ctx, actor, func(ctx context.Context, commands dbgen.DBTX) (dbgen.CreateTrainingVariationParams, error) {
		var current dbgen.CreateTrainingVariationParams
		err := commands.QueryRow(ctx, `SELECT plan_id,target_membership_id,target_group_id,subject_kind,subject_id FROM public.training_variations WHERE id=$1`, in.ID).Scan(&current.PlanID, &current.TargetMembershipID, &current.TargetGroupID, &current.SubjectKind, &current.SubjectID)
		current.CreatedByID = actor
		return current, err
	}, func(ctx context.Context, commands dbgen.DBTX, q *dbgen.Queries, current dbgen.CreateTrainingVariationParams) error {
		var id uuid.UUID
		if err := commands.QueryRow(ctx, `SELECT id FROM public.training_variations WHERE id=$1 AND plan_id=$2 FOR UPDATE NOWAIT`, in.ID, current.PlanID).Scan(&id); err != nil {
			return publicationNowaitError(err)
		}
		var err error
		result, err = q.RetireTrainingVariation(ctx, in)
		return err
	})
	return result, err
}

// The same existing P/A/H session and scoped publication lock protocol protects
// both native instruction mutations. No schedule values are changed or inferred.
func (s PostgresStructuredTrainingStore) withModernTrainingVariation(ctx context.Context, actor uuid.UUID, read func(context.Context, dbgen.DBTX) (dbgen.CreateTrainingVariationParams, error), write func(context.Context, dbgen.DBTX, *dbgen.Queries, dbgen.CreateTrainingVariationParams) error) error {
	pool, ok := s.Pool.(*pgxpool.Pool)
	if !ok {
		return errPublicationSourceUnavailable
	}
	outcome, err := db.WithRecipientPublicationTx(ctx, pool, focusedEditTxOptions(), func(ctx context.Context, commands dbgen.DBTX) error {
		q := dbgen.New(commands)
		adapter := publicationPostgresQueries{commands: commands, queries: q}
		partial, err := adapter.AdmitScheduleWrites(ctx)
		if err != nil {
			return err
		}
		if err = adapter.LockActor(ctx, actor); err != nil {
			return err
		}
		in, err := read(ctx, commands)
		if err != nil {
			return err
		}
		if !partial {
			if err = adapter.GuardPlanV1(ctx, actor, in.PlanID); err != nil {
				return err
			}
		}
		if _, err = adapter.LockPlan(ctx, actor, in.PlanID); err != nil {
			return err
		}
		candidates, err := adapter.StructuralCandidates(ctx, actor, in.PlanID)
		if err != nil {
			return err
		}
		if len(candidates) > trainingPublicationReviewMaxTuples {
			return errTrainingPublicationReviewLimit
		}
		heads, err := adapter.LockPlacementHeads(ctx, actor, in.PlanID)
		if err != nil {
			return publicationNowaitError(err)
		}
		if !publicationCandidateHeadsLocked(candidates, heads) {
			return errStructuredTrainingPublicationConflict
		}
		memberships, err := adapter.LockMemberships(ctx, actor, in.PlanID)
		if err != nil {
			return publicationNowaitError(err)
		}
		if !publicationSameIDs(memberships, publicationMembershipIDs(publicationSourceEnvelope{Candidates: candidates})) {
			return errStructuredTrainingPublicationConflict
		}
		users, err := adapter.LockUsers(ctx, actor, in.PlanID)
		if err != nil {
			return publicationNowaitError(err)
		}
		if !publicationCandidateUsersLocked(candidates, users) {
			return errStructuredTrainingPublicationConflict
		}
		if err = lockModernVariationSubject(ctx, commands, in); err != nil {
			return err
		}
		return write(ctx, commands, q, in)
	})
	return publicationTransactionError(outcome, err)
}

func lockModernVariationSubject(ctx context.Context, commands dbgen.DBTX, in dbgen.CreateTrainingVariationParams) error {
	// Only fixed server-side relations enter this SQL; submitted kinds select no arbitrary identifier.
	var query string
	switch in.SubjectKind {
	case dbgen.TrainingVariationSubjectKindSEGMENT:
		query = `SELECT target.id FROM public.training_session_segments target JOIN public.training_sessions session ON session.id=target.session_id WHERE target.id=$1 AND session.plan_id=$2 FOR SHARE OF session,target NOWAIT`
	case dbgen.TrainingVariationSubjectKindBLOCK:
		query = `SELECT target.id FROM public.training_segment_blocks target JOIN public.training_session_segments segment ON segment.id=target.segment_id JOIN public.training_sessions session ON session.id=segment.session_id WHERE target.id=$1 AND session.plan_id=$2 FOR SHARE OF session,segment,target NOWAIT`
	case dbgen.TrainingVariationSubjectKindWATERSTEP:
		query = `SELECT target.id FROM public.water_work_steps target JOIN public.training_segment_blocks block ON block.id=target.block_id JOIN public.training_session_segments segment ON segment.id=block.segment_id JOIN public.training_sessions session ON session.id=segment.session_id WHERE target.id=$1 AND session.plan_id=$2 FOR SHARE OF session,segment,block,target NOWAIT`
	case dbgen.TrainingVariationSubjectKindGYMEXERCISE:
		query = `SELECT target.id FROM public.gym_exercises target JOIN public.training_segment_blocks block ON block.id=target.block_id JOIN public.training_session_segments segment ON segment.id=block.segment_id JOIN public.training_sessions session ON session.id=segment.session_id WHERE target.id=$1 AND session.plan_id=$2 FOR SHARE OF session,segment,block,target NOWAIT`
	default:
		return pgx.ErrNoRows
	}
	var id uuid.UUID
	return publicationNowaitError(commands.QueryRow(ctx, query, in.SubjectID, in.PlanID).Scan(&id))
}
