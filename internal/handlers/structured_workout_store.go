package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresFocusedWorkoutStore struct{ Pool *pgxpool.Pool }

func focusedDay(raw string) (pgtype.Date, error) {
	if !validWorkspaceDate(raw) {
		return pgtype.Date{}, errFocusedWorkoutInvalid
	}
	d, _ := time.Parse("2006-01-02", raw)
	return pgtype.Date{Time: d, Valid: true}, nil
}
func focusedContext(actor uuid.UUID, row dbgen.GetFocusedWaterCreateContextRow, state db.TrainingScheduleState) FocusedWorkoutContext {
	return FocusedWorkoutContext{ActorID: actor, PlanID: row.PlanID, GroupID: row.GroupID, SeasonID: row.SeasonID, Day: row.SelectedDay.Time.Format("2006-01-02"), GroupName: row.GroupName, PlanTitle: row.PlanTitle, WeekStart: row.WeekStart.Time, PlanUpdatedAt: row.PlanUpdatedAt.Time.UTC().Truncate(time.Microsecond), DatabaseIncarnation: state.DatabaseIncarnation, StateEpoch: state.StateEpoch, SchemaContract: state.SchemaContract, AuthoringEnabled: state.PartialV2Enabled}
}
func (s PostgresFocusedWorkoutStore) LoadFocusedWorkout(ctx context.Context, in FocusedWorkoutRequest) (FocusedWorkoutContext, error) {
	var zero FocusedWorkoutContext
	date, err := focusedDay(in.Day)
	if err != nil {
		return zero, pgx.ErrNoRows
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return zero, err
	}
	defer tx.Rollback(ctx)
	state, err := db.VerifyTrainingRecipientRuntime(ctx, tx, focusedWorkoutStateCapabilities())
	if err != nil {
		return zero, err
	}
	row, err := dbgen.New(tx).GetFocusedWaterCreateContext(ctx, dbgen.GetFocusedWaterCreateContextParams{ActorID: in.ActorID, PlanID: in.PlanID, Day: date})
	if err != nil {
		return zero, err
	}
	if err = tx.Commit(ctx); err != nil {
		return zero, err
	}
	return focusedContext(in.ActorID, row, state), nil
}
func (s PostgresFocusedWorkoutStore) FocusedWorkoutArtifactSize(ctx context.Context, in FocusedWorkoutRequest, raw []byte) (int32, error) {
	return dbgen.New(s.Pool).FocusedWaterArtifactSize(ctx, dbgen.FocusedWaterArtifactSizeParams{ActorID: in.ActorID, PlanID: in.PlanID, Snapshot: raw})
}
func (s PostgresFocusedWorkoutStore) SaveFocusedWorkout(ctx context.Context, in focusedWorkoutReviewed) (FocusedWorkoutSaveResult, error) {
	var result FocusedWorkoutSaveResult
	if !in.verified || !time.Now().Before(in.expiresAt) {
		return result, errFocusedWorkoutInvalid
	}
	p := in.Proposal
	c := p.Context
	outcome, err := db.WithRecipientPublicationTx(ctx, s.Pool, db.RecipientTxOptions{AdmissionTimeout: 5 * time.Second, TransactionTimeout: 10 * time.Second, CleanupTimeout: 5 * time.Second}, func(txCtx context.Context, commands dbgen.DBTX) error {
		// First statement in the newly admitted SERIALIZABLE transaction. No actor,
		// plan or scope query precedes this capability/state lock.
		if _, err := commands.Exec(txCtx, `SELECT public.training_schedule_admit_partial($1,$2)`, c.DatabaseIncarnation, c.StateEpoch); err != nil {
			return trainingScheduleWriteError(err)
		}
		state, err := db.VerifyTrainingRecipientRuntime(txCtx, commands, focusedWorkoutStateCapabilities())
		if err != nil {
			return err
		}
		if state.SchemaContract != "schedule-p1" || !state.PartialV2Enabled || state.StateEpoch != c.StateEpoch || state.DatabaseIncarnation != c.DatabaseIncarnation {
			return errFocusedWorkoutClosed
		}
		if err = lockTrainingGroupActor(txCtx, commands, c.ActorID); err != nil {
			return err
		}
		q := dbgen.New(commands)
		locked, err := q.LockFocusedWaterCreatePlan(txCtx, dbgen.LockFocusedWaterCreatePlanParams{ActorID: c.ActorID, PlanID: c.PlanID})
		if err != nil {
			return err
		}
		if locked.GroupID != c.GroupID || locked.SeasonID != c.SeasonID || locked.GroupName != c.GroupName || locked.PlanTitle != c.PlanTitle || !locked.WeekStart.Time.Equal(c.WeekStart) {
			return errFocusedWorkoutConflict
		}
		existing, readErr := readFocusedWaterGraph(txCtx, q, p)
		if readErr == nil {
			if !focusedWaterSameGraph(p, existing) {
				return errFocusedWorkoutConflict
			}
			result = FocusedWorkoutSaveResult{SessionID: p.SessionID, AlreadyCreated: true}
		} else {
			if !errors.Is(readErr, pgx.ErrNoRows) {
				return readErr
			}
			if !locked.PlanUpdatedAt.Valid || !locked.PlanUpdatedAt.Time.Equal(c.PlanUpdatedAt) {
				return errFocusedWorkoutConflict
			}
			if err = insertFocusedWaterGraph(txCtx, q, p); err != nil {
				return err
			}
			saved, err := readFocusedWaterGraph(txCtx, q, p)
			if err != nil {
				return err
			}
			if !focusedWaterSameGraph(p, saved) {
				return errFocusedWorkoutConflict
			}
			result = FocusedWorkoutSaveResult{SessionID: p.SessionID}
		}
		raw, err := focusedWaterArtifact(p)
		if err != nil {
			return err
		}
		size, err := q.FocusedWaterArtifactSize(txCtx, dbgen.FocusedWaterArtifactSizeParams{ActorID: c.ActorID, PlanID: c.PlanID, Snapshot: raw})
		if err != nil {
			return err
		}
		if size > focusedWorkoutMaxArtifactBytes {
			return errFocusedWorkoutLimit
		}
		day, err := focusedDay(c.Day)
		if err != nil {
			return err
		}
		final, err := q.GetFocusedWaterCreateContext(txCtx, dbgen.GetFocusedWaterCreateContextParams{ActorID: c.ActorID, PlanID: c.PlanID, Day: day})
		if err != nil {
			return err
		}
		if final.GroupID != c.GroupID || final.SeasonID != c.SeasonID || final.PlanTitle != c.PlanTitle || final.GroupName != c.GroupName {
			return errFocusedWorkoutConflict
		}
		var decision time.Time
		if err = commands.QueryRow(txCtx, `SELECT pg_catalog.clock_timestamp()`).Scan(&decision); err != nil {
			return err
		}
		if !decision.Before(in.expiresAt) {
			return errFocusedWorkoutInvalid
		}
		return nil
	})
	result.CleanupFailed = outcome.CleanupFailed
	if err = publicationTransactionError(outcome, err); err != nil {
		return FocusedWorkoutSaveResult{}, err
	}
	return result, nil
}
func insertFocusedWaterGraph(ctx context.Context, q *dbgen.Queries, p focusedWaterProposal) error {
	c := p.Context
	day, _ := focusedDay(c.Day)
	starts, ends := pgtype.Timestamptz{}, pgtype.Timestamptz{}
	if p.Schedule.StartsAt != nil {
		starts = pgtype.Timestamptz{Time: *p.Schedule.StartsAt, Valid: true}
	}
	if p.Schedule.EndsAt != nil {
		ends = pgtype.Timestamptz{Time: *p.Schedule.EndsAt, Valid: true}
	}
	_, err := q.InsertFocusedWaterSession(ctx, dbgen.InsertFocusedWaterSessionParams{ActorID: c.ActorID, PlanID: c.PlanID, SessionID: p.SessionID, ExpectedPlanUpdatedAt: pgtype.Timestamptz{Time: c.PlanUpdatedAt, Valid: true}, Title: p.Title, Description: p.Description, SchedulePrecision: string(p.Schedule.Precision), ScheduledOn: day, StartsAt: starts, EndsAt: ends})
	if err != nil {
		return err
	}
	_, err = q.InsertFocusedWaterSegment(ctx, dbgen.InsertFocusedWaterSegmentParams{ActorID: c.ActorID, PlanID: c.PlanID, SessionID: p.SessionID, SegmentID: p.Activity.ID, Title: p.Activity.Title, Location: p.Activity.Location, PlannedDurationMinutes: p.Activity.DurationMinutes, PlannedStartOffsetMinutes: p.Activity.StartOffsetMinutes, TransitionDurationMinutes: p.Activity.TransitionMinutes, EquipmentNotes: p.Activity.Equipment})
	if err != nil {
		return err
	}
	for i, phase := range p.Phases {
		if _, err = q.InsertFocusedWaterPhase(ctx, dbgen.InsertFocusedWaterPhaseParams{ActorID: c.ActorID, PlanID: c.PlanID, SessionID: p.SessionID, SegmentID: p.Activity.ID, PhaseID: phase.ID, Position: int32(i + 1), Purpose: dbgen.TrainingBlockPurpose(phase.Purpose), Title: phase.Title, Instructions: phase.Instructions}); err != nil {
			return err
		}
	}
	return nil
}
func readFocusedWaterGraph(ctx context.Context, q *dbgen.Queries, expected focusedWaterProposal) (focusedWaterProposal, error) {
	p := focusedWaterProposal{Context: expected.Context, SessionID: expected.SessionID, Phases: []focusedWaterPhase{}}
	c := p.Context
	row, err := q.GetFocusedWaterSession(ctx, dbgen.GetFocusedWaterSessionParams{ActorID: c.ActorID, PlanID: c.PlanID, SessionID: p.SessionID})
	if err != nil {
		return p, err
	}
	if row.CreatedByID != c.ActorID || row.HasExtraDetail || row.EntryKind != "TRAINING" || row.Status != "ACTIVE" || row.ModalityID != nil || row.CancelledAt.Valid || row.CancelledByID != nil || row.CancellationReason != nil {
		return p, errFocusedWorkoutConflict
	}
	p.Title, p.Description = row.Title, row.Description
	p.Schedule, err = trainingScheduleFromRecord(row.SchedulePrecision, row.ScheduledOn, row.StartsAt, row.EndsAt, row.DayBand)
	if err != nil {
		return p, err
	}
	segments, err := q.ListFocusedWaterSegments(ctx, dbgen.ListFocusedWaterSegmentsParams{ActorID: c.ActorID, PlanID: c.PlanID, SessionID: p.SessionID})
	if err != nil {
		return p, err
	}
	if len(segments) != 1 || segments[0].Modality != "WATER" || segments[0].Position != 1 {
		return p, errFocusedWorkoutConflict
	}
	segment := segments[0]
	p.Activity = focusedWaterActivity{ID: segment.ID, Title: segment.Title, Location: segment.Location, Equipment: segment.EquipmentNotes, DurationMinutes: segment.PlannedDurationMinutes, StartOffsetMinutes: segment.PlannedStartOffsetMinutes, TransitionMinutes: segment.TransitionDurationMinutes}
	blocks, err := q.ListFocusedWaterPhases(ctx, dbgen.ListFocusedWaterPhasesParams{ActorID: c.ActorID, PlanID: c.PlanID, SessionID: p.SessionID})
	if err != nil {
		return p, err
	}
	if len(blocks) != len(expected.Phases) || len(blocks) > focusedWorkoutMaxPhases {
		return p, errFocusedWorkoutConflict
	}
	for i, block := range blocks {
		if block.SegmentID != segment.ID || block.Position != int32(i+1) {
			return p, errFocusedWorkoutConflict
		}
		p.Phases = append(p.Phases, focusedWaterPhase{Key: expected.Phases[i].Key, ID: block.ID, Purpose: string(block.Purpose), Title: block.Title, Instructions: block.Instructions})
	}
	return p, nil
}
func focusedWaterSameGraph(a, b focusedWaterProposal) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}
