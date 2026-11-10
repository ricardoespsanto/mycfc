package handlers

import (
	"errors"
	"testing"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
)

func TestWorkoutReadbackRefusesInconsistentStoredGraph(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*focusedEditRaw)
		want   error
	}{
		{"invalid schedule", func(r *focusedEditRaw) { r.Session.StartsAt.Valid = false }, ErrTrainingScheduleInvalid},
		{"too many water steps", func(r *focusedEditRaw) { r.Steps = make([]dbgen.WaterWorkStep, focusedMaxWaterNodes+1) }, errFocusedWorkoutLimit},
		{"duplicate water prescription", func(r *focusedEditRaw) { r.Water = append(r.Water, r.Water[0]) }, errFocusedWorkoutConflict},
		{"duplicate gym prescription", func(r *focusedEditRaw) {
			gym := dbgen.GymBlockPrescription{BlockID: r.Water[0].BlockID}
			r.Gym = []dbgen.GymBlockPrescription{gym, gym}
		}, errFocusedWorkoutConflict},
		{"two prescription types for one phase", func(r *focusedEditRaw) {
			r.Gym = []dbgen.GymBlockPrescription{{BlockID: r.Water[0].BlockID}}
		}, errFocusedWorkoutConflict},
		{"segment from another session", func(r *focusedEditRaw) { r.Segments[0].SessionID = uuid.New() }, errFocusedWorkoutConflict},
		{"phase identity missing from review", func(r *focusedEditRaw) { r.Blocks[0].ID = uuid.New() }, errFocusedWorkoutConflict},
		{"water step identity missing from review", func(r *focusedEditRaw) { r.Steps[0].ID = uuid.New() }, errFocusedWorkoutConflict},
		{"unknown parent", func(r *focusedEditRaw) { r.Steps[1].ParentStepID = focusedPointer(uuid.New()) }, errFocusedWorkoutConflict},
		{"unexpected sibling position", func(r *focusedEditRaw) { r.Steps[1].Position++ }, errFocusedWorkoutConflict},
		{"unconsumed exercise", func(r *focusedEditRaw) {
			r.Exercises = []dbgen.GymExercise{{BlockID: uuid.New()}}
		}, errFocusedWorkoutConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := focusedEditFixture(t)
			graph, err := focusedGraphFromRaw(source.Raw, source.Graph, true)
			if err != nil || len(graph.Activities) != 1 {
				t.Fatalf("complete graph fixture failed: %v", err)
			}
			tc.change(&source.Raw)
			if _, err = focusedGraphFromRaw(source.Raw, source.Graph, true); !errors.Is(err, tc.want) {
				t.Fatalf("inconsistent stored graph returned %v, want %v", err, tc.want)
			}
		})
	}
}
