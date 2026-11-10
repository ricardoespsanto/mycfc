package handlers

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type workoutEditDeleteBatch struct {
	name string
	ids  []uuid.UUID
}

type workoutEditDeleteFixture struct {
	dbgen.DBTX
	batches []workoutEditDeleteBatch
	failAt  string
	failure error
}

func (q *workoutEditDeleteFixture) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	line, _, _ := strings.Cut(sql, "\n")
	name := strings.Fields(line)[2]
	ids := slices.Clone(args[3].([]uuid.UUID))
	q.batches = append(q.batches, workoutEditDeleteBatch{name, ids})
	if name == q.failAt {
		if q.failure != nil {
			return nil, q.failure
		}
		return structuredTrainingEmptyRowsFake{}, nil
	}
	return &workoutEditDeletedRows{ids: ids}, nil
}

type workoutEditDeletedRows struct {
	pgx.Rows
	ids []uuid.UUID
}

func (r *workoutEditDeletedRows) Next() bool { return len(r.ids) != 0 }
func (r *workoutEditDeletedRows) Close()     {}
func (r *workoutEditDeletedRows) Err() error { return nil }
func (r *workoutEditDeletedRows) Scan(dest ...any) error {
	*dest[0].(*uuid.UUID) = r.ids[0]
	r.ids = r.ids[1:]
	return nil
}

func TestWorkoutEditDeletesDependentsInOrderAndRequiresExactResult(t *testing.T) {
	for _, tc := range []struct {
		name, stop string
		failure    error
		want       error
	}{
		{name: "complete closure"},
		{name: "delete conflict", stop: "DeleteFocusedWorkoutGymExercises", want: errFocusedWorkoutConflict},
		{name: "phase write failed", stop: "DeleteFocusedWorkoutBlocks", failure: pgx.ErrNoRows, want: pgx.ErrNoRows},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := focusedEditFixture(t)
			root, child := source.Raw.Steps[0].ID, source.Raw.Steps[1].ID
			water, gym := source.Raw.Water[0].BlockID, uuid.New()
			exercise := uuid.New()
			segment := source.Raw.Segments[0].ID
			input := FocusedWorkoutEditRequest{ActorID: source.Context.ActorID, PlanID: source.Context.PlanID, SessionID: source.Graph.SessionID}
			diff := focusedEditDiff{
				RemovedSteps: []uuid.UUID{root, child}, RemovedExercises: []uuid.UUID{exercise},
				RemovedWater: []uuid.UUID{water}, RemovedGym: []uuid.UUID{gym},
				RemovedBlocks: []uuid.UUID{water, gym}, RemovedSegments: []uuid.UUID{segment},
			}
			expected := []workoutEditDeleteBatch{
				{"DeleteFocusedWorkoutWaterSteps", []uuid.UUID{child}},
				{"DeleteFocusedWorkoutWaterSteps", []uuid.UUID{root}},
				{"DeleteFocusedWorkoutGymExercises", []uuid.UUID{exercise}},
				{"DeleteFocusedWorkoutWaterPrescriptions", []uuid.UUID{water}},
				{"DeleteFocusedWorkoutGymPrescriptions", []uuid.UUID{gym}},
				{"DeleteFocusedWorkoutBlocks", []uuid.UUID{water, gym}},
				{"DeleteFocusedWorkoutSegments", []uuid.UUID{segment}},
			}
			if tc.stop != "" {
				for i, batch := range expected {
					if batch.name == tc.stop {
						expected = expected[:i+1]
						break
					}
				}
			}
			q := &workoutEditDeleteFixture{failAt: tc.stop, failure: tc.failure}
			err := deleteFocusedEdit(t.Context(), dbgen.New(q), input, source.Raw, diff)
			if !errors.Is(err, tc.want) || !slices.EqualFunc(q.batches, expected, func(a, b workoutEditDeleteBatch) bool {
				return a.name == b.name && slices.Equal(a.ids, b.ids)
			}) {
				t.Fatalf("deletion returned %v, batches %+v, want %v and %+v", err, q.batches, tc.want, expected)
			}
		})
	}
}

func TestWorkoutEditStagingStopsAtFailedSiblingWrite(t *testing.T) {
	for _, name := range []string{
		"StageFocusedWorkoutSegmentPosition", "StageFocusedWorkoutBlockPosition",
		"StageFocusedWorkoutWaterStepPosition",
	} {
		t.Run(name, func(t *testing.T) {
			source := focusedEditFixture(t)
			diff, err := buildFocusedEditDiff(source.Graph, source.Graph, focusedGraphDraft{})
			if err != nil {
				t.Fatal(err)
			}
			fault := &workoutEditWriteFault{failAt: name, failure: pgx.ErrNoRows}
			err = stageFocusedEdit(t.Context(), dbgen.New(fault), source.Graph, source.Raw, diff)
			if !errors.Is(err, pgx.ErrNoRows) || len(fault.writes) == 0 || !strings.Contains(fault.writes[len(fault.writes)-1], name+" ") {
				t.Fatalf("staging did not stop at %s: %v, %v", name, err, fault.writes)
			}
		})
	}
}
