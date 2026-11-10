package handlers

import (
	"context"
	"errors"
	"sort"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func trainingWriteActor(ctx context.Context) uuid.UUID {
	user, _ := CurrentUserFromContext(ctx)
	return user.ID
}

// The target resolver locks plan, session and required ancestors and repeats
// actual actor/scope checks. No state lock is added to an exact/v1 operation.
func lockTrainingScheduleTarget(ctx context.Context, q *dbgen.Queries, actor uuid.UUID, kind string, id uuid.UUID) (uuid.UUID, error) {
	id, err := q.TrainingScheduleLockTargetV1(ctx, dbgen.TrainingScheduleLockTargetV1Params{ActorID: actor, TargetKind: kind, TargetID: id})
	return id, trainingScopeWriteError(err)
}

type trainingSchedulePlanQueries interface {
	TrainingScheduleGuardPlanV1(context.Context, dbgen.TrainingScheduleGuardPlanV1Params) (bool, error)
	LockStructuredTrainingPlanForPublication(context.Context, uuid.UUID) (dbgen.LockStructuredTrainingPlanForPublicationRow, error)
}

func guardTrainingSchedulePlans(ctx context.Context, q trainingSchedulePlanQueries, actor uuid.UUID, ids ...uuid.UUID) error {
	ordered := append([]uuid.UUID(nil), ids...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].String() < ordered[j].String() })
	seen := map[uuid.UUID]bool{}
	for _, id := range ordered {
		if seen[id] {
			continue
		}
		seen[id] = true
		ok, e := q.TrainingScheduleGuardPlanV1(ctx, dbgen.TrainingScheduleGuardPlanV1Params{ActorID: actor, PlanID: id})
		if e != nil {
			return trainingScopeWriteError(e)
		}
		if !ok {
			return errTrainingScheduleUnavailable
		}
	}
	for _, id := range ordered {
		if _, e := q.LockStructuredTrainingPlanForPublication(ctx, id); e != nil {
			return e
		}
	}
	// Authority, source version and complete plan are fresh after all locks.
	for _, id := range ordered {
		ok, e := q.TrainingScheduleGuardPlanV1(ctx, dbgen.TrainingScheduleGuardPlanV1Params{ActorID: actor, PlanID: id})
		if e != nil {
			return trainingScopeWriteError(e)
		}
		if !ok {
			return errTrainingScheduleUnavailable
		}
	}
	return nil
}

func withTrainingScheduleTarget[T any](ctx context.Context, s PostgresStructuredTrainingStore, actor uuid.UUID, kind string, id uuid.UUID, write func(*dbgen.Queries) (T, error)) (value T, err error) {
	defer func() { err = trainingScheduleWriteError(err) }()
	tx, e := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if e != nil {
		return value, e
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if e = lockTrainingGroupActor(ctx, tx, actor); e != nil {
		return value, trainingScopeWriteError(e)
	}
	q := dbgen.New(tx)
	if _, e = lockTrainingScheduleTarget(ctx, q, actor, kind, id); e != nil {
		return value, e
	}
	value, e = write(q)
	if e != nil {
		return value, e
	}
	return value, tx.Commit(ctx)
}

func withTrainingSchedulePlan[T any](ctx context.Context, s PostgresStructuredTrainingStore, actor, plan uuid.UUID, write func(*dbgen.Queries) (T, error)) (value T, err error) {
	defer func() { err = trainingScheduleWriteError(err) }()
	tx, e := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if e != nil {
		return value, e
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if e = lockTrainingGroupActor(ctx, tx, actor); e != nil {
		return value, trainingScopeWriteError(e)
	}
	q := dbgen.New(tx)
	if e = guardTrainingSchedulePlans(ctx, q, actor, plan); e != nil {
		return value, e
	}
	value, e = write(q)
	if e != nil {
		return value, e
	}
	return value, tx.Commit(ctx)
}

func requireTrainingRoutineV1(ctx context.Context, q *dbgen.Queries, actor, routine uuid.UUID, raw []byte) error {
	ok, e := q.TrainingScheduleGuardRoutineV1(ctx, dbgen.TrainingScheduleGuardRoutineV1Params{ActorID: actor, RoutineID: routine})
	if e != nil {
		return trainingScopeWriteError(e)
	}
	if !ok {
		return errTrainingScheduleUnavailable
	}
	return unsupportedTrainingArtifact(raw)
}

// Only a freshly fetched source is accepted; retained request blobs cannot
// substitute a current routine after its scope/version has changed.
func currentTrainingRoutineV1(ctx context.Context, q *dbgen.Queries, input StructuredRoutineInsertInput) (dbgen.TrainingRoutine, error) {
	if e := requireTrainingRoutineV1(ctx, q, input.ActorID, input.Routine.ID, input.Routine.Snapshot); e != nil {
		return dbgen.TrainingRoutine{}, e
	}
	row, e := q.GetVisibleTrainingRoutine(ctx, dbgen.GetVisibleTrainingRoutineParams{RoutineID: input.Routine.ID, UserID: input.ActorID, IsAdmin: true})
	if e != nil {
		return row, e
	}
	if row.ID != input.Routine.ID {
		return row, pgx.ErrNoRows
	}
	if !row.UpdatedAt.Time.Equal(input.Routine.UpdatedAt.Time) {
		return row, errStructuredTrainingPublicationConflict
	}
	if e = unsupportedTrainingArtifact(row.Snapshot); e != nil {
		return row, e
	}
	return row, nil
}

func trainingScopeWriteError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return errTrainingScheduleUnavailable
	}
	return err
}

type trainingScheduleWriteStatusStore interface {
	TrainingSchedulePlanWriteStatus(context.Context, dbgen.TrainingSchedulePlanWriteStatusParams) (dbgen.TrainingSchedulePlanWriteStatusRow, error)
}

func (s PostgresStructuredTrainingStore) TrainingSchedulePlanWriteStatus(ctx context.Context, p dbgen.TrainingSchedulePlanWriteStatusParams) (dbgen.TrainingSchedulePlanWriteStatusRow, error) {
	return s.queries().TrainingSchedulePlanWriteStatus(ctx, p)
}
