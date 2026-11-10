package handlers

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

var errFocusedEditSourceChanged = errors.New("saved workout source changed; reload required")
var errFocusedEditReadOnly = errors.New("saved workout is not currently editable")

// Native scoped rows, not display strings or a prescription snapshot. The
// separate table slices retain every raw nullable scalar and immutable relation.
type focusedEditRaw struct {
	Session   dbgen.GetFocusedWorkoutSessionRow `json:"session"`
	Segments  []dbgen.TrainingSessionSegment    `json:"segments"`
	Blocks    []dbgen.TrainingSegmentBlock      `json:"blocks"`
	Water     []dbgen.WaterBlockPrescription    `json:"water"`
	Steps     []dbgen.WaterWorkStep             `json:"steps"`
	Gym       []dbgen.GymBlockPrescription      `json:"gym"`
	Exercises []dbgen.GymExercise               `json:"exercises"`
}
type focusedEditSource struct {
	Context    FocusedWorkoutContext
	Raw        focusedEditRaw
	Graph      focusedWorkoutGraph
	DecisionAt time.Time
}
type focusedEditBaseline struct {
	SessionID      uuid.UUID `json:"session_id"`
	SourceDay      string    `json:"source_day"`
	SessionVersion time.Time `json:"session_version"`
	OwnRawDigest   string    `json:"own_raw_digest"`
}

func focusedEditInstant(t *pgtype.Timestamptz) {
	if t.Valid && t.InfinityModifier == pgtype.Finite {
		t.Time = t.Time.UTC()
	}
}
func focusedEditOwnDigest(c FocusedWorkoutContext, raw focusedEditRaw) (string, error) {
	if len(raw.Segments) > focusedMaxActivities || len(raw.Blocks) > focusedWorkoutMaxPhases || len(raw.Water)+len(raw.Gym) > focusedWorkoutMaxPhases || len(raw.Steps) > focusedMaxWaterNodes || len(raw.Exercises) > focusedMaxGymExercises {
		return "", errFocusedWorkoutLimit
	}
	// Query order is not authority: raw positions/parents are explicitly hashed.
	// Sort copies by ID so different bounded row-lock/read order is equivalent.
	raw.Session.HasOutcomes = false
	focusedEditInstant(&raw.Session.StartsAt)
	focusedEditInstant(&raw.Session.EndsAt)
	focusedEditInstant(&raw.Session.CancelledAt)
	focusedEditInstant(&raw.Session.CreatedAt)
	focusedEditInstant(&raw.Session.UpdatedAt)
	raw.Segments = append([]dbgen.TrainingSessionSegment{}, raw.Segments...)
	sort.Slice(raw.Segments, func(i, j int) bool { return bytes.Compare(raw.Segments[i].ID[:], raw.Segments[j].ID[:]) < 0 })
	for i := range raw.Segments {
		focusedEditInstant(&raw.Segments[i].CreatedAt)
		focusedEditInstant(&raw.Segments[i].UpdatedAt)
	}
	raw.Blocks = append([]dbgen.TrainingSegmentBlock{}, raw.Blocks...)
	sort.Slice(raw.Blocks, func(i, j int) bool { return bytes.Compare(raw.Blocks[i].ID[:], raw.Blocks[j].ID[:]) < 0 })
	for i := range raw.Blocks {
		focusedEditInstant(&raw.Blocks[i].CreatedAt)
		focusedEditInstant(&raw.Blocks[i].UpdatedAt)
	}
	raw.Water = append([]dbgen.WaterBlockPrescription{}, raw.Water...)
	sort.Slice(raw.Water, func(i, j int) bool { return bytes.Compare(raw.Water[i].BlockID[:], raw.Water[j].BlockID[:]) < 0 })
	for i := range raw.Water {
		focusedEditInstant(&raw.Water[i].CreatedAt)
		focusedEditInstant(&raw.Water[i].UpdatedAt)
	}
	raw.Steps = append([]dbgen.WaterWorkStep{}, raw.Steps...)
	sort.Slice(raw.Steps, func(i, j int) bool { return bytes.Compare(raw.Steps[i].ID[:], raw.Steps[j].ID[:]) < 0 })
	for i := range raw.Steps {
		focusedEditInstant(&raw.Steps[i].CreatedAt)
		focusedEditInstant(&raw.Steps[i].UpdatedAt)
	}
	raw.Gym = append([]dbgen.GymBlockPrescription{}, raw.Gym...)
	sort.Slice(raw.Gym, func(i, j int) bool { return bytes.Compare(raw.Gym[i].BlockID[:], raw.Gym[j].BlockID[:]) < 0 })
	for i := range raw.Gym {
		focusedEditInstant(&raw.Gym[i].CreatedAt)
		focusedEditInstant(&raw.Gym[i].UpdatedAt)
	}
	raw.Exercises = append([]dbgen.GymExercise{}, raw.Exercises...)
	sort.Slice(raw.Exercises, func(i, j int) bool { return bytes.Compare(raw.Exercises[i].ID[:], raw.Exercises[j].ID[:]) < 0 })
	for i := range raw.Exercises {
		focusedEditInstant(&raw.Exercises[i].CreatedAt)
		focusedEditInstant(&raw.Exercises[i].UpdatedAt)
	}
	fixed := struct {
		Plan, Group, Season, Incarnation uuid.UUID
		Week                             string
		Schema                           string
	}{c.PlanID, c.GroupID, c.SeasonID, c.DatabaseIncarnation, c.WeekStart.Format("2006-01-02"), c.SchemaContract}
	encoded, err := json.Marshal(struct {
		Scope any            `json:"scope"`
		Raw   focusedEditRaw `json:"raw"`
	}{fixed, raw})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}
func focusedEditBaselineFor(source focusedEditSource) (focusedEditBaseline, error) {
	digest, err := focusedEditOwnDigest(source.Context, source.Raw)
	if err != nil {
		return focusedEditBaseline{}, err
	}
	schedule, err := trainingScheduleFromRecord(source.Raw.Session.SchedulePrecision, source.Raw.Session.ScheduledOn, source.Raw.Session.StartsAt, source.Raw.Session.EndsAt, source.Raw.Session.DayBand)
	if err != nil {
		return focusedEditBaseline{}, err
	}
	return focusedEditBaseline{SessionID: source.Raw.Session.ID, SourceDay: schedule.Date, SessionVersion: source.Raw.Session.UpdatedAt.Time.UTC(), OwnRawDigest: digest}, nil
}
func (baseline focusedEditBaseline) matches(source focusedEditSource) error {
	current, err := focusedEditBaselineFor(source)
	if err != nil {
		return err
	}
	if current.SessionID != baseline.SessionID || current.SourceDay != baseline.SourceDay || !current.SessionVersion.Equal(baseline.SessionVersion) || current.OwnRawDigest != baseline.OwnRawDigest {
		return errFocusedEditSourceChanged
	}
	return nil
}
func focusedEditEligible(session dbgen.GetFocusedWorkoutSessionRow, decision time.Time) bool {
	if session.Status != "ACTIVE" || decision.IsZero() {
		return false
	}
	schedule, err := trainingScheduleFromRecord(session.SchedulePrecision, session.ScheduledOn, session.StartsAt, session.EndsAt, session.DayBand)
	if err != nil {
		return false
	}
	switch schedule.Precision {
	case TrainingScheduleInterval, TrainingScheduleStartOnly:
		return session.StartsAt.Valid && session.StartsAt.InfinityModifier == pgtype.Finite && session.StartsAt.Time.After(decision)
	case TrainingScheduleDay, TrainingScheduleBand:
		return schedule.Date > decision.In(lisbonLocation()).Format("2006-01-02")
	default:
		return false
	}
}
