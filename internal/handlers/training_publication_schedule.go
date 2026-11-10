package handlers

import (
	"context"
	"encoding/json"

	"github.com/cfcoimbra/mycfc/internal/db"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
)

// Publication uses native source timestamps, never its minute-only presentation.
// Repeated overview rows must describe exactly one schedule for each session.
func publicationSourceSchedules(rows []dbgen.ListStructuredTrainingOverviewForManagerRow) (map[uuid.UUID]TrainingSessionSchedule, error) {
	result := map[uuid.UUID]TrainingSessionSchedule{}
	kinds := map[uuid.UUID]dbgen.TrainingEntryKind{}
	for _, row := range rows {
		if row.SessionID == nil {
			continue
		}
		if *row.SessionID == uuid.Nil || row.SchedulePrecision == nil || row.EntryKind == nil || !row.SessionOn.Valid {
			return nil, errPublicationSourceUnavailable
		}
		schedule, err := trainingScheduleFromRecord(*row.SchedulePrecision, row.ScheduledOn, row.StartsAt, row.EndsAt, row.DayBand)
		if err != nil || schedule.ValidateKind(string(*row.EntryKind)) != nil || schedule.Date != row.SessionOn.Time.Format("2006-01-02") {
			return nil, ErrTrainingScheduleInvalid
		}
		if prior, exists := result[*row.SessionID]; exists && (!focusedGraphJSONEqual(prior, schedule) || kinds[*row.SessionID] != *row.EntryKind) {
			return nil, errPublicationSourceUnavailable
		}
		result[*row.SessionID], kinds[*row.SessionID] = schedule, *row.EntryKind
	}
	return result, nil
}

func validatePublicationScheduleWriteState(source publicationSourceEnvelope, state db.TrainingScheduleState) error {
	for _, row := range source.Overview {
		if row.SessionID != nil && row.SchedulePrecision != nil && *row.SchedulePrecision != "INTERVAL" && !state.PartialV2Enabled {
			return ErrTrainingScheduleWriteUnsupported
		}
	}
	return nil
}

func checkPublicationArtifactSizes(ctx context.Context, q db.TrainingScheduleQueryReader, inputs []StructuredPrescriptionInput) error {
	if len(inputs) > trainingPublicationReviewMaxTuples {
		return errTrainingPublicationReviewLimit
	}
	artifacts := []json.RawMessage{}
	total := 0
	for _, input := range inputs {
		if len(input.Snapshot) > trainingPublicationReviewMaxSnapshotBytes-total {
			return errTrainingPublicationReviewLimit
		}
		total += len(input.Snapshot)
		version, err := trainingArtifactVersion(input.Snapshot)
		if err != nil {
			return err
		}
		if version == 2 {
			artifacts = append(artifacts, input.Snapshot)
		}
	}
	if len(artifacts) == 0 {
		return nil
	}
	raw, err := json.Marshal(artifacts)
	if err != nil {
		return err
	}
	// One array adds at most one comma per tuple plus its two brackets.
	if len(raw) > trainingPublicationReviewMaxSnapshotBytes+trainingPublicationReviewMaxTuples+1 {
		return errTrainingPublicationReviewLimit
	}
	var max int32
	if err = q.QueryRow(ctx, `SELECT COALESCE(max(pg_catalog.octet_length(value::text)),0)::integer FROM pg_catalog.jsonb_array_elements($1::jsonb)`, raw).Scan(&max); err != nil {
		return err
	}
	if max > focusedWorkoutMaxArtifactBytes {
		return errTrainingPublicationReviewLimit
	}
	return nil
}

func marshalPublicationPrescription(snapshot structuredPrescriptionSnapshot, schedule TrainingSessionSchedule) ([]byte, error) {
	if schedule.Precision == "" || schedule.Precision == TrainingScheduleInterval {
		return json.Marshal(snapshot) // Preserve exact existing v1 bytes and field order.
	}
	if err := schedule.ValidateKind(snapshot.Session.EntryKind); err != nil {
		return nil, err
	}
	return json.Marshal(structuredPrescriptionSnapshotV2{
		SchemaVersion: 2, PlanID: snapshot.PlanID, PlanTitle: snapshot.PlanTitle,
		GroupName: snapshot.GroupName, WeekTitle: snapshot.WeekTitle,
		WeekDescription: snapshot.WeekDescription, Season: snapshot.Season,
		DateRange: snapshot.DateRange, PlannedLoad: snapshot.PlannedLoad,
		Session: structuredSessionSnapshotV2{StructuredTrainingSession: snapshot.Session, Schedule: schedule},
	})
}
