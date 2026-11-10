package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/cfcoimbra/mycfc/internal/trainingcompat"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/google/uuid"
)

const focusedWorkoutMaxRequestBytes = 1 << 20
const focusedWorkoutMaxPhases = 32
const focusedWorkoutMaxArtifactBytes = 200000

var errFocusedWorkoutInvalid = errors.New("invalid workout review")
var errFocusedWorkoutConflict = errors.New("workout source changed")
var errFocusedWorkoutLimit = errors.New("workout technical limit exceeded")
var errFocusedWorkoutClosed = errors.New("workout authoring is closed")

type FocusedWorkoutRequest struct {
	ActorID, PlanID uuid.UUID
	Day             string
}
type FocusedWorkoutContext struct {
	ActorID, PlanID, GroupID, SeasonID uuid.UUID
	Day, GroupName, PlanTitle          string
	WeekStart                          time.Time
	PlanUpdatedAt                      time.Time
	DatabaseIncarnation                uuid.UUID
	StateEpoch                         int64
	SchemaContract                     string
	AuthoringEnabled                   bool
}

func (c FocusedWorkoutContext) Digest() string {
	b, _ := json.Marshal(c)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

type focusedWaterActivity struct {
	ID                 uuid.UUID `json:"id"`
	Title              string    `json:"title"`
	Location           string    `json:"location"`
	Equipment          string    `json:"equipment"`
	DurationMinutes    *int32    `json:"duration_minutes"`
	StartOffsetMinutes *int32    `json:"start_offset_minutes"`
	TransitionMinutes  *int32    `json:"transition_minutes"`
}
type focusedWaterPhase struct {
	Key          string    `json:"key"`
	ID           uuid.UUID `json:"id"`
	Purpose      string    `json:"purpose"`
	Title        string    `json:"title"`
	Instructions string    `json:"instructions"`
}
type focusedWaterProposal struct {
	Context     FocusedWorkoutContext   `json:"context"`
	SessionID   uuid.UUID               `json:"session_id"`
	Title       string                  `json:"title"`
	Description string                  `json:"description"`
	Schedule    TrainingSessionSchedule `json:"schedule"`
	Activity    focusedWaterActivity    `json:"activity"`
	Phases      []focusedWaterPhase     `json:"phases"`
}
type focusedWorkoutDraft struct {
	Values  url.Values
	Keys    []string
	Removed []focusedWorkoutRemoval
}
type focusedWorkoutRemoval struct{ Key, PhaseKey, Payload string }
type focusedWorkoutReviewed struct {
	Proposal  focusedWaterProposal
	Token     string
	verified  bool
	expiresAt time.Time
}
type FocusedWorkoutSaveResult struct {
	SessionID                     uuid.UUID
	AlreadyCreated, CleanupFailed bool
}
type FocusedWorkoutStore = FocusedWorkoutGraphStore

type StructuredWorkout struct {
	Store            FocusedWorkoutStore
	System           System
	PageMeta         components.PageMeta
	HTTPWriteTimeout time.Duration
}

func focusedWorkoutStateCapabilities() db.TrainingScheduleCapabilities {
	return db.TrainingScheduleCapabilities{ReaderVersion: trainingcompat.ReaderGeneration, WriterVersion: trainingcompat.WriterGeneration}
}
func focusedWorkoutErrors() validation.FieldErrors { return validation.FieldErrors{} }
