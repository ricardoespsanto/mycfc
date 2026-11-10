package handlers

import (
	"context"
	"errors"
	"strings"
	"testing"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/jackc/pgx/v5"
)

type workoutEditWriteFault struct {
	dbgen.DBTX
	failAt  string
	failure error
	writes  []string
}

func (f *workoutEditWriteFault) QueryRow(_ context.Context, query string, _ ...any) pgx.Row {
	statement, _, _ := strings.Cut(query, "\n")
	f.writes = append(f.writes, statement)
	if strings.Contains(statement, f.failAt+" ") {
		return structuredTrainingRowFake{err: f.failure}
	}
	return structuredTrainingRowFake{}
}

func TestWorkoutEditStopsAtFirstFailedGraphWrite(t *testing.T) {
	for _, stage := range []string{
		"UpdateFocusedWorkoutSession", "UpdateFocusedWorkoutSegment", "UpdateFocusedWorkoutBlock",
		"UpdateFocusedWorkoutWaterPrescription", "UpdateFocusedWorkoutWaterStep",
	} {
		t.Run(stage, func(t *testing.T) {
			source := focusedEditFixture(t)
			fault := &workoutEditWriteFault{failAt: stage, failure: errors.New("fixture graph write failed")}
			err := writeFocusedEditGraph(t.Context(), dbgen.New(fault), source.Graph, source.Raw)
			if !errors.Is(err, fault.failure) || len(fault.writes) == 0 || !strings.Contains(fault.writes[len(fault.writes)-1], stage+" ") {
				t.Fatalf("writer continued after failure: %v, %v", fault.writes, err)
			}
		})
	}
}
