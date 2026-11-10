package handlers

import (
	"context"
	"github.com/google/uuid"
	"sort"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/jackc/pgx/v5"
)

// PostgresTrainingScheduleStore keeps the generated read interface, while exact-only writes acquire
// fresh authority and stable target locks before touching a recorded schedule.
type PostgresTrainingScheduleStore struct {
	dbgen.Querier
	Pool structuredTrainingDB
}

func (s PostgresTrainingScheduleStore) CreateTrainingSession(ctx context.Context, p dbgen.CreateTrainingSessionParams) (row dbgen.TrainingSession, err error) {
	if !p.StartsAt.Valid || !p.EndsAt.Valid || !p.EndsAt.Time.After(p.StartsAt.Time) {
		return row, ErrTrainingScheduleInvalid
	}
	adapter := PostgresStructuredTrainingStore{Pool: s.Pool}
	return withTrainingScheduleTarget(ctx, adapter, p.CreatedByID, "PLAN", p.PlanID, func(q *dbgen.Queries) (dbgen.TrainingSession, error) { return q.CreateTrainingSession(ctx, p) })
}
func (s PostgresTrainingScheduleStore) UpdateTrainingSession(ctx context.Context, p dbgen.UpdateTrainingSessionParams) (row dbgen.TrainingSession, err error) {
	defer func() { err = trainingScheduleWriteError(err) }()
	if !p.StartsAt.Valid || !p.EndsAt.Valid || !p.EndsAt.Time.After(p.StartsAt.Time) {
		return row, ErrTrainingScheduleInvalid
	}
	actor := trainingWriteActor(ctx)
	tx, e := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if e != nil {
		return row, e
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if e = lockTrainingGroupActor(ctx, tx, actor); e != nil {
		return row, trainingScopeWriteError(e)
	}
	q := dbgen.New(tx)
	current, e := q.GetTrainingSessionForEdit(ctx, p.ID)
	if e != nil {
		return row, e
	}
	// PLAN locks carry scope only, never whole-plan artifact assertions. Editing
	// this exact target neither rewrites nor drops another partial session.
	ids := []uuid.UUID{current.PlanID, p.PlanID}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	for _, id := range ids {
		if _, e = lockTrainingScheduleTarget(ctx, q, actor, "PLAN", id); e != nil {
			return row, e
		}
	}
	if _, e = lockTrainingScheduleTarget(ctx, q, actor, "SESSION", p.ID); e != nil {
		return row, e
	}
	latest, e := q.GetTrainingSessionForEdit(ctx, p.ID)
	if e != nil {
		return row, e
	}
	if latest.PlanID != current.PlanID {
		return row, errStructuredTrainingPublicationConflict
	}
	row, e = q.UpdateTrainingSession(ctx, p)
	if e != nil {
		return row, e
	}
	return row, tx.Commit(ctx)
}
func (s PostgresTrainingScheduleStore) CancelTrainingSession(ctx context.Context, p dbgen.CancelTrainingSessionParams) (dbgen.TrainingSession, error) {
	return withTrainingScheduleTarget(ctx, PostgresStructuredTrainingStore{Pool: s.Pool}, trainingWriteActor(ctx), "SESSION", p.ID, func(q *dbgen.Queries) (dbgen.TrainingSession, error) { return q.CancelTrainingSession(ctx, p) })
}
