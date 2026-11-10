package handlers

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/cfcoimbra/mycfc/ui/pages"
)

// v1 keeps its original DTO and field order. Typed v2 publication uses this
// separate envelope, preserving explicit nullable schedule facts.
type structuredSessionSnapshotV2 struct {
	pages.StructuredTrainingSession
	Schedule  TrainingSessionSchedule `json:"schedule"`
	EntryKind *string                 `json:"entry_kind,omitempty"`
}
type structuredPrescriptionSnapshotV2 struct {
	SchemaVersion   int                         `json:"schema_version"`
	PlanID          string                      `json:"plan_id"`
	PlanTitle       string                      `json:"plan_title"`
	GroupName       string                      `json:"group_name"`
	WeekTitle       string                      `json:"week_title"`
	WeekDescription string                      `json:"week_description"`
	Season          string                      `json:"season"`
	DateRange       string                      `json:"date_range"`
	PlannedLoad     string                      `json:"planned_load"`
	Session         structuredSessionSnapshotV2 `json:"session"`
}

func decodeStructuredPrescription(raw []byte, location *time.Location) (structuredPrescriptionSnapshot, error) {
	var out structuredPrescriptionSnapshot
	version, err := trainingArtifactVersion(raw)
	if err != nil {
		return out, err
	}
	switch version {
	case 1:
		// Legacy text is frozen history, not an authoritative typed schedule. Keep
		// permissive legacy fields for old records; never parse When or read live rows.
		d := json.NewDecoder(bytes.NewReader(raw))
		if err = d.Decode(&out); err != nil || out.SchemaVersion != 1 {
			return out, ErrTrainingSnapshotUnsupported
		}
		out.Session.Schedule = pages.TrainingScheduleView{}
		return out, nil
	case 2:
		var typed structuredPrescriptionSnapshotV2
		if err = strictTrainingJSON(raw, &typed); err != nil || typed.SchemaVersion != 2 {
			return out, ErrTrainingSnapshotUnsupported
		}
		// Require the exact typed schedule keys, including explicit nullable values.
		var root map[string]json.RawMessage
		var session map[string]json.RawMessage
		if json.Unmarshal(raw, &root) != nil || json.Unmarshal(root["session"], &session) != nil {
			return out, ErrTrainingSnapshotUnsupported
		}
		_, legacyKind := session["EntryKind"]
		_, snakeKind := session["entry_kind"]
		if legacyKind == snakeKind {
			return out, ErrTrainingSnapshotUnsupported
		}
		kind := typed.Session.StructuredTrainingSession.EntryKind
		if typed.Session.EntryKind != nil {
			if kind != "" {
				return out, ErrTrainingSnapshotUnsupported
			}
			kind = *typed.Session.EntryKind
		}
		schedule, e := trainingScheduleJSON(session["schedule"])
		if e != nil || schedule.ValidateKind(kind) != nil {
			return out, ErrTrainingSnapshotUnsupported
		}
		out = structuredPrescriptionSnapshot{SchemaVersion: 2, PlanID: typed.PlanID, PlanTitle: typed.PlanTitle, GroupName: typed.GroupName, WeekTitle: typed.WeekTitle, WeekDescription: typed.WeekDescription, Season: typed.Season, DateRange: typed.DateRange, PlannedLoad: typed.PlannedLoad, Session: typed.Session.StructuredTrainingSession}
		out.Session.EntryKind = kind
		out.Session.WriteUnsupported = true
		out.Session.UnsupportedMessage = trainingScheduleUnsupportedMessage
		out.Session.Schedule = trainingScheduleView(schedule, location)
		out.Session.When = out.Session.Schedule.Label
		out.Session.StartsOn = schedule.Date
		out.Session.StartsAt = out.Session.Schedule.StartsAt
		out.Session.EndsOn = out.Session.Schedule.EndsOn
		out.Session.EndsAt = out.Session.Schedule.EndsAt
		return out, nil
	default:
		return out, ErrTrainingSnapshotUnsupported
	}
}

// SQL routine snapshots are a distinct lower-case envelope. They are never
// decoded into the PascalCase prescription DTO or persisted by this C reader.
func decodeRoutineSchedule(raw []byte, location *time.Location) (pages.TrainingScheduleView, error) {
	version, e := trainingArtifactVersion(raw)
	if e != nil {
		return pages.TrainingScheduleView{}, e
	}
	if version == 1 {
		return pages.TrainingScheduleView{}, nil
	}
	if version != 2 {
		return pages.TrainingScheduleView{}, ErrTrainingSnapshotUnsupported
	}
	var envelope struct {
		Version     int               `json:"schema_version"`
		Title       string            `json:"title"`
		Description string            `json:"description"`
		EntryKind   string            `json:"entry_kind"`
		Schedule    json.RawMessage   `json:"schedule"`
		Segments    []json.RawMessage `json:"segments"`
	}
	if strictTrainingJSON(raw, &envelope) != nil || envelope.Version != 2 {
		return pages.TrainingScheduleView{}, ErrTrainingSnapshotUnsupported
	}
	schedule, e := trainingScheduleJSON(envelope.Schedule)
	if e != nil || schedule.ValidateKind(envelope.EntryKind) != nil {
		return pages.TrainingScheduleView{}, ErrTrainingSnapshotUnsupported
	}
	return trainingScheduleView(schedule, location), nil
}
