package handlers

import (
	"context"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type reuseRead struct {
	State       db.TrainingScheduleState
	Source      reuseSourceMaterial
	Target      *reuseTargetMaterial
	References  focusedGraphProfileFacts
	TargetGraph *focusedWorkoutGraph
	DecisionAt  time.Time
}

func readReuseTarget(ctx context.Context, q *dbgen.Queries, actor uuid.UUID, selector reuseTargetSelector, state db.TrainingScheduleState, locked bool) (reuseTargetMaterial, *focusedWorkoutGraph, error) {
	target := reuseTargetMaterial{Selector: selector}
	week, err := focusedDay(selector.WeekStart)
	if err != nil || week.Time.Weekday() != time.Monday {
		return target, nil, errFocusedWorkoutInvalid
	}
	if locked {
		row, err := q.LockReuseTargetContext(ctx, dbgen.LockReuseTargetContextParams{ActorID: actor, GroupID: selector.GroupID, SeasonID: selector.SeasonID, WeekStart: week})
		if err != nil {
			return target, nil, publicationNowaitError(err)
		}
		target.Configuration = dbgen.GetReuseTargetContextRow(row)
	} else {
		target.Configuration, err = q.GetReuseTargetContext(ctx, dbgen.GetReuseTargetContextParams{ActorID: actor, GroupID: selector.GroupID, SeasonID: selector.SeasonID, WeekStart: week})
		if err != nil {
			return target, nil, err
		}
	}
	if target.Configuration.ExistingPlanID != nil {
		row, err := q.GetReusePlan(ctx, dbgen.GetReusePlanParams{ActorID: actor, PlanID: *target.Configuration.ExistingPlanID})
		if err != nil {
			return target, nil, err
		}
		target.Plan = &row
	}
	if selector.SessionID == uuid.Nil {
		if selector.SegmentID != uuid.Nil {
			return target, nil, errFocusedWorkoutInvalid
		}
		return target, nil, nil
	}
	if target.Plan == nil {
		return target, nil, errFocusedWorkoutInvalid
	}
	var raw focusedEditRaw
	if locked {
		row, err := q.LockFocusedWorkoutForEdit(ctx, dbgen.LockFocusedWorkoutForEditParams{ActorID: actor, PlanID: target.Plan.ID, SessionID: selector.SessionID})
		if err != nil {
			return target, nil, err
		}
		raw.Session = focusedEditLockedRow(row)
	} else {
		raw.Session, err = q.GetFocusedWorkoutSession(ctx, dbgen.GetFocusedWorkoutSessionParams{ActorID: actor, PlanID: target.Plan.ID, SessionID: selector.SessionID})
		if err != nil {
			return target, nil, err
		}
	}
	raw.Session.HasOutcomes = false
	if err = readFocusedEditContent(ctx, q, FocusedWorkoutEditRequest{actor, target.Plan.ID, selector.SessionID}, &raw, locked); err != nil {
		return target, nil, err
	}
	schedule, err := trainingScheduleFromRecord(raw.Session.SchedulePrecision, raw.Session.ScheduledOn, raw.Session.StartsAt, raw.Session.EndsAt, raw.Session.DayBand)
	if err != nil {
		return target, nil, err
	}
	identity, err := focusedEditRawIdentity(raw, reusePlanContext(actor, *target.Plan, state, schedule.Date))
	if err != nil {
		return target, nil, err
	}
	graph, err := focusedGraphFromRaw(raw, identity, false)
	if err != nil {
		return target, nil, err
	}
	if selector.SegmentID != uuid.Nil {
		found := false
		for _, a := range raw.Segments {
			if a.ID == selector.SegmentID {
				found = true
			}
		}
		if !found {
			return target, nil, pgx.ErrNoRows
		}
	}
	target.Existing = &raw
	return target, &graph, nil
}
func (s PostgresStructuredReuseStore) ReadReuse(ctx context.Context, actor uuid.UUID, source reuseSourceSelector, target *reuseTargetSelector) (reuseRead, error) {
	var out reuseRead
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	out.State, err = db.VerifyTrainingWorkoutGraphRuntime(ctx, tx, focusedWorkoutStateCapabilities())
	if err != nil {
		return out, err
	}
	q := dbgen.New(tx)
	out.Source, err = readReuseSource(ctx, q, actor, source, out.State, nil, false)
	if err != nil {
		return out, err
	}
	out.References, err = readReuseReferences(ctx, q, actor, out.Source, false)
	if err != nil {
		return out, err
	}
	if target != nil {
		material, graph, err := readReuseTarget(ctx, q, actor, *target, out.State, false)
		if err != nil {
			return out, err
		}
		out.Target = &material
		out.TargetGraph = graph
		if graph != nil {
			refs, err := readFocusedEditReferences(ctx, q, FocusedWorkoutEditRequest{actor, graph.Context.PlanID, graph.SessionID}, *graph, focusedGraphSelectedProfileIDs(*graph), nil)
			if err != nil {
				return out, err
			}
			out.References, err = mergeReuseReferences(out.References, refs)
			if err != nil {
				return out, err
			}
		}
	}
	if _, err = reuseBoundedJSON(struct {
		Source     reuseSourceMaterial
		Target     *reuseTargetMaterial
		References focusedGraphProfileFacts
	}{out.Source, out.Target, out.References}); err != nil {
		return out, err
	}
	if err = tx.QueryRow(ctx, `SELECT pg_catalog.clock_timestamp()`).Scan(&out.DecisionAt); err != nil {
		return out, err
	}
	if err = tx.Commit(ctx); err != nil {
		return reuseRead{}, err
	}
	return out, nil
}
