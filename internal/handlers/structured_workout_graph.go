package handlers

import (
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/google/uuid"
)

// One normalized relational graph, shared by preview, signed proposal, inserts
// and complete readback. Raw editor strings remain separate in the same draft.
type focusedWorkoutGraph struct {
	Context     FocusedWorkoutContext       `json:"context"`
	SessionID   uuid.UUID                   `json:"session_id"`
	Kind        dbgen.TrainingEntryKind     `json:"kind"`
	Title       string                      `json:"title"`
	Description string                      `json:"description"`
	Schedule    TrainingSessionSchedule     `json:"schedule"`
	Activities  []focusedGraphActivityInput `json:"activities"`
	References  focusedGraphProfileFacts    `json:"references"`
}
type focusedGraphActivityInput struct {
	Key                string                        `json:"key"`
	ID                 uuid.UUID                     `json:"id"`
	Modality           dbgen.TrainingSegmentModality `json:"modality"`
	Title              string                        `json:"title"`
	Location           string                        `json:"location"`
	Equipment          string                        `json:"equipment"`
	DurationMinutes    *int32                        `json:"duration_minutes"`
	StartOffsetMinutes *int32                        `json:"start_offset_minutes"`
	TransitionMinutes  *int32                        `json:"transition_minutes"`
	Phases             []focusedGraphPhaseInput      `json:"phases"`
}
type focusedGraphPhaseInput struct {
	Key          string                     `json:"key"`
	ID           uuid.UUID                  `json:"id"`
	Purpose      dbgen.TrainingBlockPurpose `json:"purpose"`
	Title        string                     `json:"title"`
	Instructions string                     `json:"instructions"`
	Water        *focusedGraphWaterInput    `json:"water"`
	Gym          *focusedGraphGymInput      `json:"gym"`
}
type focusedGraphWaterInput struct {
	Method          dbgen.WaterWorkMethod           `json:"method"`
	ProfileID       *uuid.UUID                      `json:"profile_id"`
	TargetDistance  *int32                          `json:"target_distance"`
	TargetCertainty *dbgen.TrainingMeasureCertainty `json:"target_certainty"`
	Nodes           []focusedGraphWaterNodeInput    `json:"nodes"`
}
type focusedGraphWaterNodeInput struct {
	Key               string                          `json:"key"`
	ID                uuid.UUID                       `json:"id"`
	ParentKey         string                          `json:"parent_key"`
	Kind              dbgen.WaterStepKind             `json:"kind"`
	Name              string                          `json:"name"`
	Repeats           *int32                          `json:"repeats"`
	Duration          *int32                          `json:"duration"`
	DurationCertainty *dbgen.TrainingMeasureCertainty `json:"duration_certainty"`
	Distance          *int32                          `json:"distance"`
	DistanceCertainty *dbgen.TrainingMeasureCertainty `json:"distance_certainty"`
	Recovery          *int32                          `json:"recovery"`
	Intensity         *string                         `json:"intensity"`
	Cadence           *int32                          `json:"cadence"`
	DrillFocus        *string                         `json:"drill_focus"`
	DrillFormat       *string                         `json:"drill_format"`
	RoleNotes         *string                         `json:"role_notes"`
	Instructions      string                          `json:"instructions"`
}
type focusedGraphGymInput struct {
	Structure     dbgen.GymBlockStructure     `json:"structure"`
	Objective     dbgen.TrainingObjective     `json:"objective"`
	Rounds        int32                       `json:"rounds"`
	RoundRecovery *int32                      `json:"round_recovery"`
	Exercises     []focusedGraphExerciseInput `json:"exercises"`
}
type focusedGraphExerciseInput struct {
	Key             string                    `json:"key"`
	ID              uuid.UUID                 `json:"id"`
	Name            string                    `json:"name"`
	Sets            *int32                    `json:"sets"`
	Repetitions     *int32                    `json:"repetitions"`
	Duration        *int32                    `json:"duration"`
	Distance        *int32                    `json:"distance"`
	Recovery        *int32                    `json:"recovery"`
	ResistanceKind  *dbgen.GymResistanceKind  `json:"resistance_kind"`
	ResistanceValue *float64                  `json:"resistance_value"`
	ResistanceText  *string                   `json:"resistance_text"`
	Intent          *dbgen.GymExecutionIntent `json:"intent"`
	Tempo           *string                   `json:"tempo"`
	Notes           string                    `json:"notes"`
}
type focusedGraphProfileFacts struct {
	Profiles []dbgen.WaterIntensityProfile `json:"profiles"`
	Zones    []dbgen.WaterIntensityZone    `json:"zones"`
}

func focusedGraphError(e validation.FieldErrors, name, message string) {
	e.Add(focusedFieldKey(name), message)
}
