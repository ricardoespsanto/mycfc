package handlers

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
)

func reuseRoutineSnapshot(t *testing.T, kind dbgen.TrainingRoutineKind, snapshot any) dbgen.GetReuseRoutineRow {
	t.Helper()
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return dbgen.GetReuseRoutineRow{ID: uuid.New(), Kind: kind, Name: "Rotina sintética", Snapshot: raw, SnapshotBytes: int64(len(raw))}
}

func reuseRoutineWaterFixture() reuseRoutineBlock {
	group, effort := uuid.New(), uuid.New()
	return reuseRoutineBlock{
		Purpose: "MAIN", Title: "Séries em água", Instructions: "Mantenha a técnica prescrita.",
		Water: &reuseRoutineWater{
			Method: "INTERVALS", ProfileID: focusedPointer(uuid.New()),
			Target: focusedPointer(int32(8000)), Certainty: focusedPointer(dbgen.TrainingMeasureCertainty("EXACT")),
			Steps: []reuseRoutineStep{
				{SourceID: group, Position: 1, Kind: "REPEAT_GROUP", Name: "Série de duas", Repeats: focusedPointer(int32(2)), Recovery: focusedPointer(int32(60))},
				{SourceID: effort, SourceParent: &group, Position: 1, Kind: "EFFORT", Name: "Pagaie no ritmo R2", Duration: focusedPointer(int32(240)), DurationCertainty: focusedPointer(dbgen.TrainingMeasureCertainty("EXACT")), Intensity: focusedPointer("R2")},
			},
		},
	}
}

func TestReuseRoutineContractPreservesWaterHierarchyAndOriginAtEachLevel(t *testing.T) {
	water := reuseRoutineWaterFixture()
	segment := reuseRoutineSegment{Modality: "WATER", Title: "Água", Blocks: []reuseRoutineBlock{{Purpose: "WARM_UP", Instructions: "Aquecimento próprio."}, water}}
	session := reuseRoutineSession{
		Version: 2, Title: "Treino guardado", Description: "Instruções do treino guardado.", Kind: "TRAINING",
		Schedule: json.RawMessage(`{"precision":"START_ONLY","date":"2026-11-11","starts_at":"2026-11-11T16:00:00Z","ends_at":null,"band":null}`),
		Segments: []reuseRoutineSegment{{Modality: "GYM", Title: "Mobilidade"}, segment},
	}
	for _, tc := range []struct {
		kind            dbgen.TrainingRoutineKind
		snapshot        any
		activity, phase int
		path            []int32
	}{
		{"SESSION", session, 1, 1, []int32{1, 1}},
		{"SEGMENT", segment, 0, 1, []int32{1}},
		{"BLOCK", water, 0, 0, []int32{}},
	} {
		t.Run(string(tc.kind), func(t *testing.T) {
			row := reuseRoutineSnapshot(t, tc.kind, tc.snapshot)
			row.Modality = focusedPointer(dbgen.TrainingSegmentModalityWATER)
			item, err := decodeReuseRoutine(row)
			if err != nil {
				t.Fatal(err)
			}
			activity := item.Graph.Activities[tc.activity]
			phase := activity.Phases[tc.phase]
			origin := item.Origins[focusedGraphPhasePrefix(activity.Key, phase.Key)]
			if origin.RoutineID == nil || *origin.RoutineID != row.ID || !slices.Equal(origin.RoutinePath, tc.path) {
				t.Fatalf("invalid stored origin path: %+v", origin)
			}
			w := phase.Water
			if w == nil || w.ProfileID == nil || *w.ProfileID != *water.Water.ProfileID || w.TargetDistance == nil || *w.TargetDistance != 8000 ||
				len(w.Nodes) != 2 || w.Nodes[1].ParentKey != w.Nodes[0].Key || w.Nodes[1].ID == water.Water.Steps[1].SourceID ||
				w.Nodes[1].Intensity == nil || *w.Nodes[1].Intensity != "R2" || phase.Instructions != water.Instructions {
				t.Fatalf("routine lost water prescription or repeated-step identity: %+v", phase)
			}
			again, err := decodeReuseRoutine(row)
			if err != nil || !focusedGraphJSONEqual(again, item) {
				t.Fatalf("same stored snapshot changed review identity: %v", err)
			}
			if tc.kind == "SESSION" {
				schedule := item.Graph.Schedule
				if item.FragmentKind != "" || item.DurationSeconds != nil || schedule.Precision != TrainingScheduleStartOnly ||
					schedule.Date != "2026-11-11" || schedule.StartsAt == nil || schedule.StartsAt.Hour() != 16 || schedule.EndsAt != nil {
					t.Fatalf("version 2 source lost its represented schedule: %+v", schedule)
				}
			} else if item.FragmentKind != tc.kind || item.Graph.Schedule.Date != "" || item.Graph.Schedule.StartsAt != nil {
				t.Fatalf("fragment was assigned an appointment: %+v", item.Graph.Schedule)
			}
		})
	}
}

func TestReuseRoutineContractRefusesUnknownOrAmbiguousSavedFormats(t *testing.T) {
	for _, tc := range []struct {
		name     string
		kind     dbgen.TrainingRoutineKind
		snapshot string
		want     error
	}{
		{"empty source", "SESSION", "", errFocusedWorkoutInvalid},
		{"unknown session field", "SESSION", `{"entry_kind":"TRAINING","duration_seconds":60,"unknown":true}`, ErrTrainingSnapshotUnsupported},
		{"v1 carries conflicting schedule", "SESSION", `{"schema_version":1,"entry_kind":"TRAINING","duration_seconds":60,"schedule":{}}`, ErrTrainingSnapshotUnsupported},
		{"v1 has no usable duration", "SESSION", `{"schema_version":1,"entry_kind":"TRAINING","duration_seconds":0}`, ErrTrainingSnapshotUnsupported},
		{"v2 carries a second duration", "SESSION", `{"schema_version":2,"entry_kind":"TRAINING","duration_seconds":60,"schedule":{}}`, ErrTrainingSnapshotUnsupported},
		{"v2 lacks schedule facts", "SESSION", `{"schema_version":2,"entry_kind":"TRAINING","schedule":{"precision":"DAY"}}`, ErrTrainingSnapshotUnsupported},
		{"v2 schedule incompatible with kind", "SESSION", `{"schema_version":2,"entry_kind":"TRAINING","schedule":{"precision":"DAY","date":"2026-11-10","starts_at":null,"ends_at":null,"band":null}}`, ErrTrainingSnapshotUnsupported},
		{"future session version", "SESSION", `{"schema_version":3,"entry_kind":"TRAINING","duration_seconds":60}`, ErrTrainingSnapshotUnsupported},
		{"unknown segment field", "SEGMENT", `{"modality":"GYM","unknown":true}`, ErrTrainingSnapshotUnsupported},
		{"unknown segment modality", "SEGMENT", `{"modality":"UNKNOWN","blocks":[]}`, ErrTrainingSnapshotUnsupported},
		{"unknown routine kind", "WEEK", `{}`, ErrTrainingSnapshotUnsupported},
		{"competing typed prescriptions", "BLOCK", `{"purpose":"MAIN","water":{"method":"INTERVALS"},"gym":{"structure":"CIRCUIT"}}`, ErrTrainingSnapshotUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := dbgen.GetReuseRoutineRow{ID: uuid.New(), Kind: tc.kind, Snapshot: []byte(tc.snapshot)}
			item, err := decodeReuseRoutine(row)
			if !errors.Is(err, tc.want) || item.Graph.SessionID != uuid.Nil {
				t.Fatalf("unsupported format yielded a reusable graph: %v, want %v", err, tc.want)
			}
		})
	}
}

func TestReuseRoutineContractRejectsInconsistentStepIdentityAndBudgets(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*reuseRoutineBlock)
		want   error
	}{
		{"duplicate step identity", func(b *reuseRoutineBlock) { b.Water.Steps[1].SourceID = b.Water.Steps[0].SourceID }, ErrTrainingSnapshotUnsupported},
		{"parent outside block", func(b *reuseRoutineBlock) { b.Water.Steps[1].SourceParent = focusedPointer(uuid.New()) }, ErrTrainingSnapshotUnsupported},
		{"water node budget", func(b *reuseRoutineBlock) { b.Water.Steps = make([]reuseRoutineStep, 257) }, errFocusedWorkoutLimit},
		{"gym exercise budget", func(b *reuseRoutineBlock) {
			b.Water = nil
			b.Gym = &reuseRoutineGym{Exercises: make([]dbgen.GymExercise, 257)}
		}, errFocusedWorkoutLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			block := reuseRoutineWaterFixture()
			tc.change(&block)
			if item, err := decodeReuseRoutine(reuseRoutineSnapshot(t, "BLOCK", block)); !errors.Is(err, tc.want) || item.Graph.SessionID != uuid.Nil {
				t.Fatalf("invalid prescription yielded a reusable graph: %v, want %v", err, tc.want)
			}
		})
	}
	for _, tc := range []struct {
		name string
		row  dbgen.GetReuseRoutineRow
	}{
		{"server refused oversized blob", dbgen.GetReuseRoutineRow{ID: uuid.New(), Kind: "SESSION", Overflow: true}},
		{"block budget", reuseRoutineSnapshot(t, "SEGMENT", reuseRoutineSegment{Modality: "WATER", Blocks: make([]reuseRoutineBlock, 33)})},
		{"segment budget", reuseRoutineSnapshot(t, "SESSION", map[string]any{"entry_kind": "TRAINING", "duration_seconds": 60, "segments": make([]reuseRoutineSegment, 9)})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := decodeReuseRoutine(tc.row); !errors.Is(err, errFocusedWorkoutLimit) {
				t.Fatalf("oversized routine error = %v", err)
			}
		})
	}
}
