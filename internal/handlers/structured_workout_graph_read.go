package handlers

import (
	"context"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
)

// Complete scoped raw reread: every count+1 sentinel or unconsumed detail row
// refuses the whole graph. This is not an overview join and never multiplies or
// silently filters water/exercise rows.
func readFocusedGraph(ctx context.Context, q *dbgen.Queries, expected focusedWorkoutGraph) (focusedWorkoutGraph, error) {
	c := expected.Context
	p := focusedWorkoutGraph{Context: c, References: expected.References, Activities: []focusedGraphActivityInput{}}
	row, err := q.GetFocusedWorkoutSession(ctx, dbgen.GetFocusedWorkoutSessionParams{ActorID: c.ActorID, PlanID: c.PlanID, SessionID: expected.SessionID})
	if err != nil {
		return p, err
	}
	if row.CreatedByID != c.ActorID || row.PlanID != c.PlanID || row.Status != "ACTIVE" || row.ModalityID != nil || row.CancelledAt.Valid || row.CancelledByID != nil || row.CancellationReason != nil {
		return p, errFocusedWorkoutConflict
	}
	p.SessionID, p.Kind, p.Title, p.Description = row.ID, row.EntryKind, row.Title, row.Description
	p.Schedule, err = trainingScheduleFromRecord(row.SchedulePrecision, row.ScheduledOn, row.StartsAt, row.EndsAt, row.DayBand)
	if err != nil {
		return p, err
	}
	segments, err := q.ListFocusedWorkoutSegments(ctx, dbgen.ListFocusedWorkoutSegmentsParams{ActorID: c.ActorID, PlanID: c.PlanID, SessionID: row.ID})
	if err != nil {
		return p, err
	}
	blocks, err := q.ListFocusedWorkoutBlocks(ctx, dbgen.ListFocusedWorkoutBlocksParams{ActorID: c.ActorID, PlanID: c.PlanID, SessionID: row.ID})
	if err != nil {
		return p, err
	}
	waters, err := q.ListFocusedWorkoutWaterPrescriptions(ctx, dbgen.ListFocusedWorkoutWaterPrescriptionsParams{ActorID: c.ActorID, PlanID: c.PlanID, SessionID: row.ID})
	if err != nil {
		return p, err
	}
	steps, err := q.ListFocusedWorkoutWaterSteps(ctx, dbgen.ListFocusedWorkoutWaterStepsParams{ActorID: c.ActorID, PlanID: c.PlanID, SessionID: row.ID})
	if err != nil {
		return p, err
	}
	gyms, err := q.ListFocusedWorkoutGymPrescriptions(ctx, dbgen.ListFocusedWorkoutGymPrescriptionsParams{ActorID: c.ActorID, PlanID: c.PlanID, SessionID: row.ID})
	if err != nil {
		return p, err
	}
	exercises, err := q.ListFocusedWorkoutGymExercises(ctx, dbgen.ListFocusedWorkoutGymExercisesParams{ActorID: c.ActorID, PlanID: c.PlanID, SessionID: row.ID})
	if err != nil {
		return p, err
	}
	if len(segments) > 8 || len(blocks) > 32 || len(waters)+len(gyms) > 32 || len(steps) > 256 || len(exercises) > 256 {
		return p, errFocusedWorkoutLimit
	}
	return focusedGraphFromRaw(focusedEditRaw{row, segments, blocks, waters, steps, gyms, exercises}, expected, true)
}

// The same complete raw mapper serves create readback and saved-workout editing.
// Editing preserves identity independently of sparse historical position values.
func focusedGraphFromRaw(raw focusedEditRaw, expected focusedWorkoutGraph, contiguous bool) (focusedWorkoutGraph, error) {
	c := expected.Context
	p := focusedWorkoutGraph{Context: c, References: expected.References, Activities: []focusedGraphActivityInput{}}
	row := raw.Session
	p.SessionID, p.Kind, p.Title, p.Description = row.ID, row.EntryKind, row.Title, row.Description
	var err error
	p.Schedule, err = trainingScheduleFromRecord(row.SchedulePrecision, row.ScheduledOn, row.StartsAt, row.EndsAt, row.DayBand)
	if err != nil {
		return p, err
	}
	segments, blocks, waters, steps, gyms, exercises := raw.Segments, raw.Blocks, raw.Water, raw.Steps, raw.Gym, raw.Exercises
	if len(segments) > 8 || len(blocks) > 32 || len(waters)+len(gyms) > 32 || len(steps) > 256 || len(exercises) > 256 {
		return p, errFocusedWorkoutLimit
	}
	aByID := map[uuid.UUID]focusedGraphActivityInput{}
	bByID := map[uuid.UUID]focusedGraphPhaseInput{}
	nByID := map[uuid.UUID]focusedGraphWaterNodeInput{}
	xByID := map[uuid.UUID]focusedGraphExerciseInput{}
	for _, a := range expected.Activities {
		aByID[a.ID] = a
		for _, b := range a.Phases {
			bByID[b.ID] = b
			if b.Water != nil {
				for _, n := range b.Water.Nodes {
					nByID[n.ID] = n
				}
			}
			if b.Gym != nil {
				for _, x := range b.Gym.Exercises {
					xByID[x.ID] = x
				}
			}
		}
	}
	bySegment := map[uuid.UUID][]dbgen.TrainingSegmentBlock{}
	for _, b := range blocks {
		bySegment[b.SegmentID] = append(bySegment[b.SegmentID], b)
	}
	byWater := map[uuid.UUID]dbgen.WaterBlockPrescription{}
	for _, w := range waters {
		if _, dup := byWater[w.BlockID]; dup {
			return p, errFocusedWorkoutConflict
		}
		byWater[w.BlockID] = w
	}
	byGym := map[uuid.UUID]dbgen.GymBlockPrescription{}
	for _, g := range gyms {
		if _, dup := byGym[g.BlockID]; dup {
			return p, errFocusedWorkoutConflict
		}
		byGym[g.BlockID] = g
	}
	stepBlocks := map[uuid.UUID]uuid.UUID{}
	byBlockStep := map[uuid.UUID][]dbgen.WaterWorkStep{}
	for _, n := range steps {
		byBlockStep[n.BlockID] = append(byBlockStep[n.BlockID], n)
		stepBlocks[n.ID] = n.BlockID
	}
	byBlockExercise := map[uuid.UUID][]dbgen.GymExercise{}
	for _, x := range exercises {
		byBlockExercise[x.BlockID] = append(byBlockExercise[x.BlockID], x)
	}
	usedBlocks, usedWaters, usedSteps, usedGyms, usedExercises := 0, 0, 0, 0, 0
	for ai, a := range segments {
		original, ok := aByID[a.ID]
		if !ok || (contiguous && a.Position != int32(ai+1)) || a.SessionID != row.ID {
			return p, errFocusedWorkoutConflict
		}
		activity := focusedGraphActivityInput{Key: original.Key, ID: a.ID, Modality: a.Modality, Title: a.Title, Location: a.Location, Equipment: a.EquipmentNotes, DurationMinutes: a.PlannedDurationMinutes, StartOffsetMinutes: a.PlannedStartOffsetMinutes, TransitionMinutes: a.TransitionDurationMinutes, Phases: []focusedGraphPhaseInput{}}
		for bi, b := range bySegment[a.ID] {
			old, ok := bByID[b.ID]
			if !ok || (contiguous && b.Position != int32(bi+1)) || b.SegmentID != a.ID {
				return p, errFocusedWorkoutConflict
			}
			usedBlocks++
			block := focusedGraphPhaseInput{Key: old.Key, ID: b.ID, Purpose: b.Purpose, Title: b.Title, Instructions: b.Instructions}
			w, hasWater := byWater[b.ID]
			g, hasGym := byGym[b.ID]
			if hasWater && hasGym {
				return p, errFocusedWorkoutConflict
			}
			if hasWater {
				usedWaters++
				water := &focusedGraphWaterInput{Method: w.Method, ProfileID: w.IntensityProfileID, TargetDistance: w.TargetDistanceMetres, TargetCertainty: w.TargetDistanceCertainty, Nodes: []focusedGraphWaterNodeInput{}}
				positions := map[uuid.UUID]int32{}
				for _, n := range byBlockStep[b.ID] {
					old, ok := nByID[n.ID]
					if !ok {
						return p, errFocusedWorkoutConflict
					}
					parent := ""
					parentID := uuid.Nil
					if n.ParentStepID != nil {
						parentNode, ok := nByID[*n.ParentStepID]
						if !ok || stepBlocks[*n.ParentStepID] != b.ID {
							return p, errFocusedWorkoutConflict
						}
						parent = parentNode.Key
						parentID = *n.ParentStepID
					}
					positions[parentID]++
					if contiguous && n.Position != positions[parentID] {
						return p, errFocusedWorkoutConflict
					}
					water.Nodes = append(water.Nodes, focusedGraphWaterNodeInput{Key: old.Key, ID: n.ID, ParentKey: parent, Kind: n.Kind, Name: n.Name, Repeats: n.Repeats, Duration: n.DurationSeconds, DurationCertainty: n.DurationCertainty, Distance: n.DistanceMetres, DistanceCertainty: n.DistanceCertainty, Recovery: n.RecoverySeconds, Intensity: n.IntensityCode, Cadence: n.CadenceSpm, DrillFocus: n.DrillFocus, DrillFormat: n.DrillFormat, RoleNotes: n.RoleNotes, Instructions: n.Instructions})
					usedSteps++
				}
				block.Water = water
			}
			if hasGym {
				usedGyms++
				gym := &focusedGraphGymInput{Structure: g.Structure, Objective: g.Objective, Rounds: g.Rounds, RoundRecovery: g.RoundRecoverySeconds, Exercises: []focusedGraphExerciseInput{}}
				for xi, x := range byBlockExercise[b.ID] {
					old, ok := xByID[x.ID]
					if !ok || (contiguous && x.Position != int32(xi+1)) {
						return p, errFocusedWorkoutConflict
					}
					gym.Exercises = append(gym.Exercises, focusedGraphExerciseInput{Key: old.Key, ID: x.ID, Name: x.Name, Sets: x.Sets, Repetitions: x.Repetitions, Duration: x.DurationSeconds, Distance: x.DistanceMetres, Recovery: x.RecoverySeconds, ResistanceKind: x.ResistanceKind, ResistanceValue: x.ResistanceValue, ResistanceText: x.ResistanceText, Intent: x.ExecutionIntent, Tempo: x.Tempo, Notes: x.Notes})
					usedExercises++
				}
				block.Gym = gym
			}
			activity.Phases = append(activity.Phases, block)
		}
		p.Activities = append(p.Activities, activity)
	}
	if usedBlocks != len(blocks) || usedWaters != len(waters) || usedSteps != len(steps) || usedGyms != len(gyms) || usedExercises != len(exercises) {
		return p, errFocusedWorkoutConflict
	}
	return p, nil
}
