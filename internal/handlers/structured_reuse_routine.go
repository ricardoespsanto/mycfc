package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
)

// Existing lower-case routine snapshots are read as their represented facts.
// There is no new persisted codec, invented appointment or parse of frozen When.
type reuseRoutineSession struct {
	Version     int                     `json:"schema_version"`
	Title       string                  `json:"title"`
	Description string                  `json:"description"`
	Kind        dbgen.TrainingEntryKind `json:"entry_kind"`
	Duration    *int64                  `json:"duration_seconds"`
	Schedule    json.RawMessage         `json:"schedule"`
	Segments    []reuseRoutineSegment   `json:"segments"`
}
type reuseRoutineSegment struct {
	Modality    dbgen.TrainingSegmentModality `json:"modality"`
	Title       string                        `json:"title"`
	Location    string                        `json:"location"`
	Duration    *int32                        `json:"planned_duration_minutes"`
	StartOffset *int32                        `json:"planned_start_offset_minutes"`
	Transition  *int32                        `json:"transition_duration_minutes"`
	Equipment   string                        `json:"equipment_notes"`
	Blocks      []reuseRoutineBlock           `json:"blocks"`
}
type reuseRoutineBlock struct {
	Purpose      dbgen.TrainingBlockPurpose `json:"purpose"`
	Title        string                     `json:"title"`
	Instructions string                     `json:"instructions"`
	Water        *reuseRoutineWater         `json:"water"`
	Gym          *reuseRoutineGym           `json:"gym"`
}
type reuseRoutineWater struct {
	Method    dbgen.WaterWorkMethod           `json:"method"`
	ProfileID *uuid.UUID                      `json:"intensity_profile_id"`
	Target    *int32                          `json:"target_distance_metres"`
	Certainty *dbgen.TrainingMeasureCertainty `json:"target_distance_certainty"`
	Steps     []reuseRoutineStep              `json:"steps"`
}
type reuseRoutineStep struct {
	SourceID          uuid.UUID                       `json:"source_id"`
	SourceParent      *uuid.UUID                      `json:"source_parent_id"`
	Position          int32                           `json:"position"`
	Kind              dbgen.WaterStepKind             `json:"kind"`
	Name              string                          `json:"name"`
	Repeats           *int32                          `json:"repeats"`
	Duration          *int32                          `json:"duration_seconds"`
	DurationCertainty *dbgen.TrainingMeasureCertainty `json:"duration_certainty"`
	Distance          *int32                          `json:"distance_metres"`
	DistanceCertainty *dbgen.TrainingMeasureCertainty `json:"distance_certainty"`
	Recovery          *int32                          `json:"recovery_seconds"`
	Intensity         *string                         `json:"intensity_code"`
	Cadence           *int32                          `json:"cadence_spm"`
	DrillFocus        *string                         `json:"drill_focus"`
	DrillFormat       *string                         `json:"drill_format"`
	RoleNotes         *string                         `json:"role_notes"`
	Instructions      string                          `json:"instructions"`
}
type reuseRoutineGym struct {
	Structure dbgen.GymBlockStructure `json:"structure"`
	Objective dbgen.TrainingObjective `json:"objective"`
	Rounds    int32                   `json:"rounds"`
	Recovery  *int32                  `json:"round_recovery_seconds"`
	Exercises []dbgen.GymExercise     `json:"exercises"`
}

func decodeReuseRoutine(row dbgen.GetReuseRoutineRow) (reuseSourceItem, error) {
	item := reuseSourceItem{Key: "m_1", Origins: map[string]reusePinOrigin{}}
	if row.Overflow || row.SnapshotBytes > focusedWorkoutMaxRequestBytes || len(row.Snapshot) > focusedWorkoutMaxRequestBytes {
		return item, errFocusedWorkoutLimit
	}
	if row.ID == uuid.Nil || len(row.Snapshot) == 0 {
		return item, errFocusedWorkoutInvalid
	}
	sum := sha256.Sum256(row.Snapshot)
	namespace := hex.EncodeToString(sum[:])
	id := func(path string) uuid.UUID { return uuid.NewSHA1(row.ID, []byte(namespace+"/"+path)) }
	graph := focusedWorkoutGraph{SessionID: id("session"), Kind: "TRAINING", Title: row.Name, Description: row.Description, Activities: []focusedGraphActivityInput{}}
	segments := []reuseRoutineSegment{}
	switch row.Kind {
	case "SESSION":
		var session reuseRoutineSession
		if focusedStrictJSON(row.Snapshot, &session) != nil {
			return item, ErrTrainingSnapshotUnsupported
		}
		graph.Title, graph.Description, graph.Kind = session.Title, session.Description, session.Kind
		segments = session.Segments
		switch session.Version {
		case 0, 1:
			if len(session.Schedule) > 0 || session.Duration == nil || *session.Duration < 1 || *session.Duration > 7*24*3600 {
				return item, ErrTrainingSnapshotUnsupported
			}
			item.DurationSeconds = session.Duration
		case 2:
			if session.Duration != nil {
				return item, ErrTrainingSnapshotUnsupported
			}
			schedule, err := trainingScheduleJSON(session.Schedule)
			if err != nil || schedule.ValidateKind(string(session.Kind)) != nil {
				return item, ErrTrainingSnapshotUnsupported
			}
			graph.Schedule = schedule
		default:
			return item, ErrTrainingSnapshotUnsupported
		}
	case "SEGMENT":
		var segment reuseRoutineSegment
		if focusedStrictJSON(row.Snapshot, &segment) != nil {
			return item, ErrTrainingSnapshotUnsupported
		}
		segments = []reuseRoutineSegment{segment}
		item.FragmentKind = row.Kind
	case "BLOCK":
		var block reuseRoutineBlock
		if focusedStrictJSON(row.Snapshot, &block) != nil {
			return item, ErrTrainingSnapshotUnsupported
		}
		segment := reuseRoutineSegment{Title: row.Name, Blocks: []reuseRoutineBlock{block}}
		if row.Modality != nil {
			segment.Modality = *row.Modality
		}
		segments = []reuseRoutineSegment{segment}
		item.FragmentKind = row.Kind
	default:
		return item, ErrTrainingSnapshotUnsupported
	}
	if len(segments) > 8 {
		return item, errFocusedWorkoutLimit
	}
	blockCount, nodeCount, exerciseCount := 0, 0, 0
	for ai, a := range segments {
		activity := focusedGraphActivityInput{Key: fmt.Sprintf("a_%d", ai+1), ID: id(fmt.Sprintf("a/%d", ai)), Modality: a.Modality, Title: a.Title, Location: a.Location, Equipment: a.Equipment, DurationMinutes: a.Duration, StartOffsetMinutes: a.StartOffset, TransitionMinutes: a.Transition, Phases: []focusedGraphPhaseInput{}}
		if !validTrainingSegmentModality(a.Modality) && row.Kind != "BLOCK" {
			return item, ErrTrainingSnapshotUnsupported
		}
		blockCount += len(a.Blocks)
		if blockCount > 32 {
			return item, errFocusedWorkoutLimit
		}
		for bi, b := range a.Blocks {
			phase := focusedGraphPhaseInput{Key: fmt.Sprintf("p_%d", bi+1), ID: id(fmt.Sprintf("b/%d/%d", ai, bi)), Purpose: b.Purpose, Title: b.Title, Instructions: b.Instructions}
			if b.Water != nil && b.Gym != nil {
				return item, ErrTrainingSnapshotUnsupported
			}
			if w := b.Water; w != nil {
				nodeCount += len(w.Steps)
				if nodeCount > 256 {
					return item, errFocusedWorkoutLimit
				}
				water := &focusedGraphWaterInput{Method: w.Method, ProfileID: w.ProfileID, TargetDistance: w.Target, TargetCertainty: w.Certainty, Nodes: []focusedGraphWaterNodeInput{}}
				keys := map[uuid.UUID]string{}
				for ni, n := range w.Steps {
					if n.SourceID != uuid.Nil {
						if keys[n.SourceID] != "" {
							return item, ErrTrainingSnapshotUnsupported
						}
						keys[n.SourceID] = fmt.Sprintf("n_%d", ni+1)
					}
				}
				for ni, n := range w.Steps {
					parent := ""
					if n.SourceParent != nil {
						parent = keys[*n.SourceParent]
						if parent == "" {
							return item, ErrTrainingSnapshotUnsupported
						}
					}
					water.Nodes = append(water.Nodes, focusedGraphWaterNodeInput{Key: fmt.Sprintf("n_%d", ni+1), ID: id(fmt.Sprintf("n/%d/%d/%d", ai, bi, ni)), ParentKey: parent, Kind: n.Kind, Name: n.Name, Repeats: n.Repeats, Duration: n.Duration, DurationCertainty: n.DurationCertainty, Distance: n.Distance, DistanceCertainty: n.DistanceCertainty, Recovery: n.Recovery, Intensity: n.Intensity, Cadence: n.Cadence, DrillFocus: n.DrillFocus, DrillFormat: n.DrillFormat, RoleNotes: n.RoleNotes, Instructions: n.Instructions})
				}
				phase.Water = water
				path := []int32{}
				if row.Kind == "SEGMENT" {
					path = []int32{int32(bi)}
				} else if row.Kind == "SESSION" {
					path = []int32{int32(ai), int32(bi)}
				}
				item.Origins[focusedGraphPhasePrefix(activity.Key, phase.Key)] = reusePinOrigin{RoutineID: focusedPointer(row.ID), RoutinePath: path}
			}
			if g := b.Gym; g != nil {
				exerciseCount += len(g.Exercises)
				if exerciseCount > 256 {
					return item, errFocusedWorkoutLimit
				}
				gym := &focusedGraphGymInput{Structure: g.Structure, Objective: g.Objective, Rounds: g.Rounds, RoundRecovery: g.Recovery, Exercises: []focusedGraphExerciseInput{}}
				for xi, x := range g.Exercises {
					gym.Exercises = append(gym.Exercises, focusedGraphExerciseInput{Key: fmt.Sprintf("x_%d", xi+1), ID: id(fmt.Sprintf("x/%d/%d/%d", ai, bi, xi)), Name: x.Name, Sets: x.Sets, Repetitions: x.Repetitions, Duration: x.DurationSeconds, Distance: x.DistanceMetres, Recovery: x.RecoverySeconds, ResistanceKind: x.ResistanceKind, ResistanceValue: x.ResistanceValue, ResistanceText: x.ResistanceText, Intent: x.ExecutionIntent, Tempo: x.Tempo, Notes: x.Notes})
				}
				phase.Gym = gym
			}
			activity.Phases = append(activity.Phases, phase)
		}
		graph.Activities = append(graph.Activities, activity)
	}
	if _, err := focusedGraphCheckedTotals(graph); err != nil {
		return item, err
	}
	item.Graph = graph
	return item, nil
}
