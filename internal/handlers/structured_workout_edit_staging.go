package handlers

import (
	"context"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"sort"
)

// Stage only retained rows in unused bounded positive sibling slots. Row versions
// remain original until one final scalar/position update; removed IDs stay closed.
func stageFocusedEdit(ctx context.Context, q *dbgen.Queries, p focusedWorkoutGraph, raw focusedEditRaw, diff focusedEditDiff) error {
	entities, err := focusedEditEntities(p)
	if err != nil {
		return err
	}
	{
		groups := map[string][]dbgen.TrainingSessionSegment{}
		for _, r := range raw.Segments {
			key := r.SessionID.String()
			groups[key] = append(groups[key], r)
		}
		keys := []string{}
		for key := range groups {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			rows := groups[key]
			current := map[uuid.UUID]int32{}
			retained := []uuid.UUID{}
			finalCount := 0
			for _, r := range rows {
				current[r.ID] = r.Position
				for _, id := range diff.RetainedSegments {
					if r.ID == id {
						retained = append(retained, id)
					}
				}
			}
			for _, e := range entities {
				if e.Kind == "segment" && e.Owner == rows[0].SessionID {
					finalCount++
				}
			}
			slots, err := focusedEditStagePositions(current, retained, finalCount, 8)
			if err != nil {
				return err
			}
			for _, r := range rows {
				slot, ok := slots[r.ID]
				if !ok {
					continue
				}
				_, err = q.StageFocusedWorkoutSegmentPosition(ctx, dbgen.StageFocusedWorkoutSegmentPositionParams{TemporaryPosition: slot, SegmentID: r.ID, ExpectedPosition: r.Position, ExpectedUpdatedAt: r.UpdatedAt, ActorID: p.Context.ActorID, PlanID: p.Context.PlanID, SessionID: p.SessionID})
				if err != nil {
					return err
				}
			}
		}
	}
	{
		groups := map[string][]dbgen.TrainingSegmentBlock{}
		for _, r := range raw.Blocks {
			key := r.SegmentID.String()
			groups[key] = append(groups[key], r)
		}
		keys := []string{}
		for key := range groups {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			rows := groups[key]
			current := map[uuid.UUID]int32{}
			retained := []uuid.UUID{}
			finalCount := 0
			for _, r := range rows {
				current[r.ID] = r.Position
				for _, id := range diff.RetainedBlocks {
					if r.ID == id {
						retained = append(retained, id)
					}
				}
			}
			for _, e := range entities {
				if e.Kind == "block" && e.Owner == rows[0].SegmentID {
					finalCount++
				}
			}
			slots, err := focusedEditStagePositions(current, retained, finalCount, 32)
			if err != nil {
				return err
			}
			for _, r := range rows {
				slot, ok := slots[r.ID]
				if !ok {
					continue
				}
				_, err = q.StageFocusedWorkoutBlockPosition(ctx, dbgen.StageFocusedWorkoutBlockPositionParams{TemporaryPosition: slot, BlockID: r.ID, ExpectedPosition: r.Position, ExpectedUpdatedAt: r.UpdatedAt, ActorID: p.Context.ActorID, PlanID: p.Context.PlanID, SessionID: p.SessionID, SegmentID: r.SegmentID})
				if err != nil {
					return err
				}
			}
		}
	}
	{
		groups := map[string][]dbgen.WaterWorkStep{}
		for _, r := range raw.Steps {
			key := r.BlockID.String() + "/" + func() string {
				if r.ParentStepID == nil {
					return ""
				}
				return r.ParentStepID.String()
			}()
			groups[key] = append(groups[key], r)
		}
		keys := []string{}
		for key := range groups {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			rows := groups[key]
			current := map[uuid.UUID]int32{}
			retained := []uuid.UUID{}
			finalCount := 0
			for _, r := range rows {
				current[r.ID] = r.Position
				for _, id := range diff.RetainedSteps {
					if r.ID == id {
						retained = append(retained, id)
					}
				}
			}
			for _, e := range entities {
				if e.Kind == "step" && e.Owner == rows[0].BlockID && e.Parent == func() uuid.UUID {
					if rows[0].ParentStepID == nil {
						return uuid.Nil
					}
					return *rows[0].ParentStepID
				}() {
					finalCount++
				}
			}
			slots, err := focusedEditStagePositions(current, retained, finalCount, 256)
			if err != nil {
				return err
			}
			for _, r := range rows {
				slot, ok := slots[r.ID]
				if !ok {
					continue
				}
				_, err = q.StageFocusedWorkoutWaterStepPosition(ctx, dbgen.StageFocusedWorkoutWaterStepPositionParams{TemporaryPosition: slot, StepID: r.ID, ExpectedPosition: r.Position, ExpectedUpdatedAt: r.UpdatedAt, ActorID: p.Context.ActorID, PlanID: p.Context.PlanID, SessionID: p.SessionID, SegmentID: func() uuid.UUID {
					for _, b := range raw.Blocks {
						if b.ID == r.BlockID {
							return b.SegmentID
						}
					}
					return uuid.Nil
				}(), BlockID: r.BlockID, ParentStepID: r.ParentStepID})
				if err != nil {
					return err
				}
			}
		}
	}
	{
		groups := map[string][]dbgen.GymExercise{}
		for _, r := range raw.Exercises {
			key := r.BlockID.String()
			groups[key] = append(groups[key], r)
		}
		keys := []string{}
		for key := range groups {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			rows := groups[key]
			current := map[uuid.UUID]int32{}
			retained := []uuid.UUID{}
			finalCount := 0
			for _, r := range rows {
				current[r.ID] = r.Position
				for _, id := range diff.RetainedExercises {
					if r.ID == id {
						retained = append(retained, id)
					}
				}
			}
			for _, e := range entities {
				if e.Kind == "exercise" && e.Owner == rows[0].BlockID {
					finalCount++
				}
			}
			slots, err := focusedEditStagePositions(current, retained, finalCount, 256)
			if err != nil {
				return err
			}
			for _, r := range rows {
				slot, ok := slots[r.ID]
				if !ok {
					continue
				}
				_, err = q.StageFocusedWorkoutGymExercisePosition(ctx, dbgen.StageFocusedWorkoutGymExercisePositionParams{TemporaryPosition: slot, ExerciseID: r.ID, ExpectedPosition: r.Position, ExpectedUpdatedAt: r.UpdatedAt, ActorID: p.Context.ActorID, PlanID: p.Context.PlanID, SessionID: p.SessionID, SegmentID: func() uuid.UUID {
					for _, b := range raw.Blocks {
						if b.ID == r.BlockID {
							return b.SegmentID
						}
					}
					return uuid.Nil
				}(), BlockID: r.BlockID})
				if err != nil {
					return err
				}
			}
		}
	}
	return nil
}
func focusedEditDeletedExact(want, got []uuid.UUID, err error) error {
	if err != nil {
		return err
	}
	a, b := focusedEditSortedIDs(want), focusedEditSortedIDs(got)
	if !focusedGraphJSONEqual(a, b) {
		return errFocusedWorkoutConflict
	}
	return nil
}
func deleteFocusedEdit(ctx context.Context, q *dbgen.Queries, in FocusedWorkoutEditRequest, raw focusedEditRaw, d focusedEditDiff) error {
	// Water ancestors are deleted only after every removed descendant.
	pending := map[uuid.UUID]bool{}
	for _, id := range d.RemovedSteps {
		pending[id] = true
	}
	for len(pending) > 0 {
		leaves := []uuid.UUID{}
		for id := range pending {
			hasChild := false
			for _, n := range raw.Steps {
				if n.ParentStepID != nil && *n.ParentStepID == id && pending[n.ID] {
					hasChild = true
				}
			}
			if !hasChild {
				leaves = append(leaves, id)
			}
		}
		if len(leaves) == 0 {
			return errFocusedWorkoutInvalid
		}
		got, err := q.DeleteFocusedWorkoutWaterSteps(ctx, dbgen.DeleteFocusedWorkoutWaterStepsParams{ActorID: in.ActorID, PlanID: in.PlanID, SessionID: in.SessionID, StepIds: leaves})
		if err = focusedEditDeletedExact(leaves, got, err); err != nil {
			return err
		}
		for _, id := range leaves {
			delete(pending, id)
		}
	}
	if len(d.RemovedExercises) > 0 {
		got, err := q.DeleteFocusedWorkoutGymExercises(ctx, dbgen.DeleteFocusedWorkoutGymExercisesParams{ActorID: in.ActorID, PlanID: in.PlanID, SessionID: in.SessionID, ExerciseIds: d.RemovedExercises})
		if err = focusedEditDeletedExact(d.RemovedExercises, got, err); err != nil {
			return err
		}
	}
	if len(d.RemovedWater) > 0 {
		got, err := q.DeleteFocusedWorkoutWaterPrescriptions(ctx, dbgen.DeleteFocusedWorkoutWaterPrescriptionsParams{ActorID: in.ActorID, PlanID: in.PlanID, SessionID: in.SessionID, BlockIds: d.RemovedWater})
		if err = focusedEditDeletedExact(d.RemovedWater, got, err); err != nil {
			return err
		}
	}
	if len(d.RemovedGym) > 0 {
		got, err := q.DeleteFocusedWorkoutGymPrescriptions(ctx, dbgen.DeleteFocusedWorkoutGymPrescriptionsParams{ActorID: in.ActorID, PlanID: in.PlanID, SessionID: in.SessionID, BlockIds: d.RemovedGym})
		if err = focusedEditDeletedExact(d.RemovedGym, got, err); err != nil {
			return err
		}
	}
	if len(d.RemovedBlocks) > 0 {
		got, err := q.DeleteFocusedWorkoutBlocks(ctx, dbgen.DeleteFocusedWorkoutBlocksParams{ActorID: in.ActorID, PlanID: in.PlanID, SessionID: in.SessionID, BlockIds: d.RemovedBlocks})
		if err = focusedEditDeletedExact(d.RemovedBlocks, got, err); err != nil {
			return err
		}
	}
	if len(d.RemovedSegments) > 0 {
		got, err := q.DeleteFocusedWorkoutSegments(ctx, dbgen.DeleteFocusedWorkoutSegmentsParams{ActorID: in.ActorID, PlanID: in.PlanID, SessionID: in.SessionID, SegmentIds: d.RemovedSegments})
		if err = focusedEditDeletedExact(d.RemovedSegments, got, err); err != nil {
			return err
		}
	}
	return nil
}
