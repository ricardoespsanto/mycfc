package handlers

import (
	"context"
	"errors"
	"github.com/cfcoimbra/mycfc/internal/db"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/jackc/pgx/v5"
	"time"
)

func focusedEditLockedRow(row dbgen.LockFocusedWorkoutForEditRow) dbgen.GetFocusedWorkoutSessionRow {
	return dbgen.GetFocusedWorkoutSessionRow{ID: row.ID, PlanID: row.PlanID, Title: row.Title, Description: row.Description, StartsAt: row.StartsAt, EndsAt: row.EndsAt, ModalityID: row.ModalityID, EntryKind: row.EntryKind, Status: row.Status, CancelledAt: row.CancelledAt, CancelledByID: row.CancelledByID, CancellationReason: row.CancellationReason, CreatedByID: row.CreatedByID, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, SchedulePrecision: row.SchedulePrecision, ScheduledOn: row.ScheduledOn, DayBand: row.DayBand, HasOutcomes: row.HasOutcomes}
}
func focusedEditTxOptions() db.RecipientTxOptions {
	return db.RecipientTxOptions{AdmissionTimeout: 5 * time.Second, TransactionTimeout: 10 * time.Second, CleanupTimeout: 5 * time.Second}
}
func admitFocusedEdit(ctx context.Context, commands dbgen.DBTX, c FocusedWorkoutContext) (db.TrainingScheduleState, error) {
	if _, err := commands.Exec(ctx, `SELECT public.training_schedule_admit_partial($1,$2)`, c.DatabaseIncarnation, c.StateEpoch); err != nil {
		return db.TrainingScheduleState{}, trainingScheduleWriteError(err)
	}
	state, err := db.VerifyTrainingWorkoutGraphRuntime(ctx, commands, focusedWorkoutStateCapabilities())
	if err != nil {
		return state, err
	}
	if state.SchemaContract != "schedule-p1" || !state.PartialV2Enabled || state.StateEpoch != c.StateEpoch || state.DatabaseIncarnation != c.DatabaseIncarnation {
		return state, errFocusedWorkoutClosed
	}
	if err = lockTrainingGroupActor(ctx, commands, c.ActorID); err != nil {
		return state, err
	}
	return state, nil
}
func focusedEditFinalEligibility(ctx context.Context, q *dbgen.Queries, in FocusedWorkoutEditRequest, original dbgen.GetFocusedWorkoutSessionRow, expires time.Time) error {
	final, err := q.CheckFocusedWorkoutOriginalEligibility(ctx, dbgen.CheckFocusedWorkoutOriginalEligibilityParams{ActorID: in.ActorID, PlanID: in.PlanID, SessionID: in.SessionID, OriginalStatus: original.Status, OriginalSchedulePrecision: original.SchedulePrecision, OriginalStartsAt: original.StartsAt, OriginalScheduledOn: original.ScheduledOn})
	if err != nil {
		return err
	}
	if !final.Eligible {
		return errFocusedEditReadOnly
	}
	if !expires.IsZero() && (!final.DecisionAt.Valid || !final.DecisionAt.Time.Before(expires)) {
		return errFocusedWorkoutInvalid
	}
	return nil
}
func (s PostgresFocusedWorkoutStore) SaveFocusedEdit(ctx context.Context, in focusedEditReviewed) (FocusedWorkoutSaveResult, error) {
	var result FocusedWorkoutSaveResult
	if !in.verified || !time.Now().Before(in.expiresAt) {
		return result, errFocusedWorkoutInvalid
	}
	payload, p := in.Payload, in.Payload.Proposal
	c := p.Context
	request := FocusedWorkoutEditRequest{c.ActorID, c.PlanID, p.SessionID}
	outcome, err := db.WithRecipientPublicationTx(ctx, s.Pool, focusedEditTxOptions(), func(txCtx context.Context, commands dbgen.DBTX) error {
		state, err := admitFocusedEdit(txCtx, commands, c)
		if err != nil {
			return err
		}
		q := dbgen.New(commands)
		plan, err := q.LockFocusedWaterCreatePlan(txCtx, dbgen.LockFocusedWaterCreatePlanParams{ActorID: c.ActorID, PlanID: c.PlanID})
		if err != nil {
			return err
		}
		row, err := q.LockFocusedWorkoutForEdit(txCtx, dbgen.LockFocusedWorkoutForEditParams{ActorID: c.ActorID, PlanID: c.PlanID, SessionID: p.SessionID})
		if err != nil {
			return err
		}
		source := focusedEditSource{Raw: focusedEditRaw{Session: focusedEditLockedRow(row)}, DecisionAt: row.DecisionAt.Time}
		if err = readFocusedEditContent(txCtx, q, request, &source.Raw, true); err != nil {
			return err
		}
		if err = completeFocusedEditSource(txCtx, q, request, &source, state, nil, nil); err != nil {
			return err
		}
		if err = payload.Original.matches(source); err != nil {
			return err
		}
		if !focusedEditEligible(source.Raw.Session, source.DecisionAt) {
			return errFocusedEditReadOnly
		}
		// The plan lock order is unchanged. Classify this workout's native
		// source before an unrelated plan version: both advance on own edits.
		if !plan.PlanUpdatedAt.Valid || !plan.PlanUpdatedAt.Time.Equal(c.PlanUpdatedAt) {
			return errFocusedWorkoutConflict
		}
		// Only server-derived old block IDs can admit a pinned inactive profile.
		ids := focusedGraphSelectedProfileIDs(p)
		profiles := []dbgen.WaterIntensityProfile{}
		if len(ids) > 0 {
			profiles, err = q.LockFocusedWorkoutEditProfiles(txCtx, dbgen.LockFocusedWorkoutEditProfilesParams{ActorID: c.ActorID, PlanID: c.PlanID, SessionID: p.SessionID, ProfileIds: ids, PinnedBlockIds: focusedEditPinnedBlocks(source.Graph)})
			if err != nil {
				return publicationNowaitError(err)
			}
		}
		refs, err := readFocusedEditReferences(txCtx, q, request, source.Graph, ids, profiles)
		if err != nil {
			return err
		}
		if !focusedGraphJSONEqual(refs, p.References) {
			return errFocusedWorkoutConflict
		}
		if err = validateFocusedEditPayload(payload, source); err != nil {
			return err
		}
		draft, err := parseFocusedEditGraph(payload.Draft, source.Graph)
		if err != nil {
			return err
		}
		diff, err := buildFocusedEditDiff(source.Graph, p, draft)
		if err != nil {
			return err
		}
		referenced, err := q.HasFocusedWorkoutRemovalReferences(txCtx, dbgen.HasFocusedWorkoutRemovalReferencesParams{ActorID: c.ActorID, PlanID: c.PlanID, SessionID: p.SessionID, SegmentIds: diff.RemovedSegments, BlockIds: diff.RemovedBlocks, StepIds: diff.RemovedSteps, ExerciseIds: diff.RemovedExercises})
		if err != nil {
			return err
		}
		if referenced {
			return errFocusedWorkoutConflict
		}
		current := source.Graph
		current.References = p.References
		if !focusedGraphJSONEqual(current, p) {
			if err = stageFocusedEdit(txCtx, q, p, source.Raw, diff); err != nil {
				return err
			}
			if err = deleteFocusedEdit(txCtx, q, request, source.Raw, diff); err != nil {
				return err
			}
			if err = writeFocusedEditGraph(txCtx, q, p, source.Raw); err != nil {
				return err
			}
			var saved focusedEditRaw
			saved.Session, err = q.GetFocusedWorkoutSession(txCtx, dbgen.GetFocusedWorkoutSessionParams{ActorID: c.ActorID, PlanID: c.PlanID, SessionID: p.SessionID})
			if err != nil {
				return err
			}
			if err = readFocusedEditContent(txCtx, q, request, &saved, false); err != nil {
				return err
			}
			if _, err = focusedEditRawIdentity(saved, c); err != nil {
				return err
			}
			reread, err := focusedGraphFromRaw(saved, p, true)
			if err != nil {
				return err
			}
			if !focusedGraphJSONEqual(reread, p) {
				return errFocusedWorkoutConflict
			}
			// Immutable identity and nullable date facts are not part of editable controls.
			if saved.Session.CreatedByID != source.Raw.Session.CreatedByID || !saved.Session.CreatedAt.Time.Equal(source.Raw.Session.CreatedAt.Time) || !focusedGraphJSONEqual(saved.Session.ModalityID, source.Raw.Session.ModalityID) || saved.Session.EntryKind != source.Raw.Session.EntryKind {
				return errFocusedWorkoutConflict
			}
		}
		artifact, err := focusedGraphArtifact(p)
		if err != nil {
			return err
		}
		size, err := q.FocusedWaterArtifactSize(txCtx, dbgen.FocusedWaterArtifactSizeParams{ActorID: c.ActorID, PlanID: c.PlanID, Snapshot: artifact})
		if err != nil {
			return err
		}
		if size > focusedWorkoutMaxArtifactBytes {
			return errFocusedWorkoutLimit
		}
		finalRefs, err := readFocusedEditReferences(txCtx, q, request, p, ids, nil)
		if err != nil {
			return err
		}
		if !focusedGraphJSONEqual(finalRefs, p.References) {
			return errFocusedWorkoutConflict
		}
		if err = focusedEditFinalEligibility(txCtx, q, request, source.Raw.Session, in.expiresAt); err != nil {
			return err
		}
		result.SessionID = p.SessionID
		return nil
	})
	result.CleanupFailed = outcome.CleanupFailed
	if err = publicationTransactionError(outcome, err); err != nil {
		if errors.Is(err, pgx.ErrNoRows) { // A failed scoped mutation must not trigger an unscoped recovery read.
			return FocusedWorkoutSaveResult{}, errFocusedWorkoutConflict
		}
		return FocusedWorkoutSaveResult{}, err
	}
	return result, nil
}
func (s PostgresFocusedWorkoutStore) CancelFocusedEdit(ctx context.Context, in FocusedWorkoutEditRequest, version time.Time, reason string) (FocusedWorkoutSaveResult, error) {
	var result FocusedWorkoutSaveResult
	// This read is only fresh scoped admission metadata, not durable authority.
	source, err := s.LoadFocusedEditSession(ctx, in)
	if err != nil {
		return result, err
	}
	c := source.Context
	outcome, err := db.WithRecipientPublicationTx(ctx, s.Pool, focusedEditTxOptions(), func(txCtx context.Context, commands dbgen.DBTX) error {
		_, err := admitFocusedEdit(txCtx, commands, c)
		if err != nil {
			return err
		}
		q := dbgen.New(commands)
		if _, err = q.LockFocusedWaterCreatePlan(txCtx, dbgen.LockFocusedWaterCreatePlanParams{ActorID: in.ActorID, PlanID: in.PlanID}); err != nil {
			return err
		}
		row, err := q.LockFocusedWorkoutForEdit(txCtx, dbgen.LockFocusedWorkoutForEditParams{ActorID: in.ActorID, PlanID: in.PlanID, SessionID: in.SessionID})
		if err != nil {
			return err
		}
		original := focusedEditLockedRow(row)
		if !original.UpdatedAt.Valid || !original.UpdatedAt.Time.Equal(version) {
			return errFocusedEditSourceChanged
		}
		if !focusedEditEligible(original, row.DecisionAt.Time) {
			return errFocusedEditReadOnly
		}
		if _, err = q.CancelFocusedWorkoutSession(txCtx, dbgen.CancelFocusedWorkoutSessionParams{ActorID: in.ActorID, PlanID: in.PlanID, SessionID: in.SessionID, ExpectedUpdatedAt: original.UpdatedAt, CancellationReason: reason}); err != nil {
			return err
		}
		if err = focusedEditFinalEligibility(txCtx, q, in, original, time.Time{}); err != nil {
			return err
		}
		result.SessionID = in.SessionID
		return nil
	})
	result.CleanupFailed = outcome.CleanupFailed
	if err = publicationTransactionError(outcome, err); err != nil {
		return FocusedWorkoutSaveResult{}, err
	}
	return result, nil
}

var _ FocusedWorkoutEditStore = PostgresFocusedWorkoutStore{}
