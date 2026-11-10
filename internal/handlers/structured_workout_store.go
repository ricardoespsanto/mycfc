package handlers

import (
	"context"
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
	state, err := db.VerifyTrainingWorkoutGraphRuntime(ctx, tx, focusedWorkoutStateCapabilities())
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
