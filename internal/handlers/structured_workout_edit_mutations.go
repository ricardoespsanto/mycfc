package handlers

import (
	"context"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func writeFocusedEditGraph(ctx context.Context, q *dbgen.Queries, p focusedWorkoutGraph, raw focusedEditRaw) error {
	c := p.Context
	date, _ := focusedDay(p.Schedule.Date)
	starts, ends := pgtype.Timestamptz{}, pgtype.Timestamptz{}
	var band *string
	if p.Schedule.StartsAt != nil {
		starts = pgtype.Timestamptz{Time: *p.Schedule.StartsAt, Valid: true}
	}
	if p.Schedule.EndsAt != nil {
		ends = pgtype.Timestamptz{Time: *p.Schedule.EndsAt, Valid: true}
	}
	if p.Schedule.Band != nil {
		band = focusedPointer(string(*p.Schedule.Band))
	}
	// A text-only edit retains a native NULL scheduled_on rather than backfilling.
	if focusedGraphJSONEqual(p.Schedule, func() TrainingSessionSchedule {
		s, _ := trainingScheduleFromRecord(raw.Session.SchedulePrecision, raw.Session.ScheduledOn, raw.Session.StartsAt, raw.Session.EndsAt, raw.Session.DayBand)
		return s
	}()) {
		date = raw.Session.ScheduledOn
	}
	_, err := q.UpdateFocusedWorkoutSession(ctx, dbgen.UpdateFocusedWorkoutSessionParams{ActorID: c.ActorID, PlanID: c.PlanID, SessionID: p.SessionID, ExpectedUpdatedAt: raw.Session.UpdatedAt, Title: p.Title, Description: p.Description, SchedulePrecision: string(p.Schedule.Precision), ScheduledOn: date, StartsAt: starts, EndsAt: ends, DayBand: band})
	if err != nil {
		return err
	}
	segments := map[uuid.UUID]dbgen.TrainingSessionSegment{}
	for _, r := range raw.Segments {
		segments[r.ID] = r
	}
	blocks := map[uuid.UUID]dbgen.TrainingSegmentBlock{}
	for _, r := range raw.Blocks {
		blocks[r.ID] = r
	}
	water := map[uuid.UUID]dbgen.WaterBlockPrescription{}
	for _, r := range raw.Water {
		water[r.BlockID] = r
	}
	steps := map[uuid.UUID]dbgen.WaterWorkStep{}
	for _, r := range raw.Steps {
		steps[r.ID] = r
	}
	gym := map[uuid.UUID]dbgen.GymBlockPrescription{}
	for _, r := range raw.Gym {
		gym[r.BlockID] = r
	}
	exercises := map[uuid.UUID]dbgen.GymExercise{}
	for _, r := range raw.Exercises {
		exercises[r.ID] = r
	}
	for ai, a := range p.Activities {
		if old, retained := segments[a.ID]; retained {
			_, err = q.UpdateFocusedWorkoutSegment(ctx, dbgen.UpdateFocusedWorkoutSegmentParams{Position: int32(ai + 1), Modality: a.Modality, Title: a.Title, Location: a.Location, PlannedDurationMinutes: a.DurationMinutes, PlannedStartOffsetMinutes: a.StartOffsetMinutes, TransitionDurationMinutes: a.TransitionMinutes, EquipmentNotes: a.Equipment, SegmentID: a.ID, ExpectedUpdatedAt: old.UpdatedAt, ActorID: c.ActorID, PlanID: c.PlanID, SessionID: p.SessionID})
		} else {
			_, err = q.InsertFocusedWorkoutSegment(ctx, dbgen.InsertFocusedWorkoutSegmentParams{ActorID: c.ActorID, PlanID: c.PlanID, SessionID: p.SessionID, SegmentID: a.ID, Position: int32(ai + 1), Modality: a.Modality, Title: a.Title, Location: a.Location, PlannedDurationMinutes: a.DurationMinutes, PlannedStartOffsetMinutes: a.StartOffsetMinutes, TransitionDurationMinutes: a.TransitionMinutes, EquipmentNotes: a.Equipment})
		}
		if err != nil {
			return err
		}
		for bi, b := range a.Phases {
			if old, retained := blocks[b.ID]; retained {
				_, err = q.UpdateFocusedWorkoutBlock(ctx, dbgen.UpdateFocusedWorkoutBlockParams{Position: int32(bi + 1), Purpose: b.Purpose, Title: b.Title, Instructions: b.Instructions, BlockID: b.ID, SegmentID: a.ID, ExpectedUpdatedAt: old.UpdatedAt, ActorID: c.ActorID, PlanID: c.PlanID, SessionID: p.SessionID})
			} else {
				_, err = q.InsertFocusedWorkoutBlock(ctx, dbgen.InsertFocusedWorkoutBlockParams{ActorID: c.ActorID, PlanID: c.PlanID, SessionID: p.SessionID, SegmentID: a.ID, BlockID: b.ID, Position: int32(bi + 1), Purpose: b.Purpose, Title: b.Title, Instructions: b.Instructions})
			}
			if err != nil {
				return err
			}
			if w := b.Water; w != nil {
				if old, retained := water[b.ID]; retained {
					_, err = q.UpdateFocusedWorkoutWaterPrescription(ctx, dbgen.UpdateFocusedWorkoutWaterPrescriptionParams{Method: w.Method, IntensityProfileID: w.ProfileID, TargetDistanceMetres: w.TargetDistance, TargetDistanceCertainty: w.TargetCertainty, BlockID: b.ID, SegmentID: a.ID, ExpectedUpdatedAt: old.UpdatedAt, ActorID: c.ActorID, PlanID: c.PlanID, SessionID: p.SessionID})
				} else {
					_, err = q.InsertFocusedWorkoutWaterPrescription(ctx, dbgen.InsertFocusedWorkoutWaterPrescriptionParams{ActorID: c.ActorID, PlanID: c.PlanID, SessionID: p.SessionID, SegmentID: a.ID, BlockID: b.ID, Method: w.Method, IntensityProfileID: w.ProfileID, TargetDistanceMetres: w.TargetDistance, TargetDistanceCertainty: w.TargetCertainty})
				}
				if err != nil {
					return err
				}
				byKey := map[string]uuid.UUID{}
				positions := map[string]int32{}
				for _, n := range w.Nodes {
					byKey[n.Key] = n.ID
				}
				for _, n := range w.Nodes {
					positions[n.ParentKey]++
					var parent *uuid.UUID
					if n.ParentKey != "" {
						parent = focusedPointer(byKey[n.ParentKey])
					}
					if old, retained := steps[n.ID]; retained {
						_, err = q.UpdateFocusedWorkoutWaterStep(ctx, dbgen.UpdateFocusedWorkoutWaterStepParams{Position: positions[n.ParentKey], Kind: n.Kind, Name: n.Name, Repeats: n.Repeats, DurationSeconds: n.Duration, DurationCertainty: n.DurationCertainty, DistanceMetres: n.Distance, DistanceCertainty: n.DistanceCertainty, RecoverySeconds: n.Recovery, IntensityCode: n.Intensity, CadenceSpm: n.Cadence, DrillFocus: n.DrillFocus, DrillFormat: n.DrillFormat, RoleNotes: n.RoleNotes, Instructions: n.Instructions, StepID: n.ID, BlockID: b.ID, SegmentID: a.ID, ExpectedUpdatedAt: old.UpdatedAt, ParentStepID: parent, ActorID: c.ActorID, PlanID: c.PlanID, SessionID: p.SessionID})
					} else {
						_, err = q.InsertFocusedWorkoutWaterStep(ctx, dbgen.InsertFocusedWorkoutWaterStepParams{ActorID: c.ActorID, PlanID: c.PlanID, SessionID: p.SessionID, SegmentID: a.ID, BlockID: b.ID, StepID: n.ID, ParentStepID: parent, Position: positions[n.ParentKey], Kind: n.Kind, Name: n.Name, Repeats: n.Repeats, DurationSeconds: n.Duration, DurationCertainty: n.DurationCertainty, DistanceMetres: n.Distance, DistanceCertainty: n.DistanceCertainty, RecoverySeconds: n.Recovery, IntensityCode: n.Intensity, CadenceSpm: n.Cadence, DrillFocus: n.DrillFocus, DrillFormat: n.DrillFormat, RoleNotes: n.RoleNotes, Instructions: n.Instructions})
					}
					if err != nil {
						return err
					}
				}
			}
			if g := b.Gym; g != nil {
				if old, retained := gym[b.ID]; retained {
					_, err = q.UpdateFocusedWorkoutGymPrescription(ctx, dbgen.UpdateFocusedWorkoutGymPrescriptionParams{Structure: g.Structure, Objective: g.Objective, Rounds: g.Rounds, RoundRecoverySeconds: g.RoundRecovery, BlockID: b.ID, SegmentID: a.ID, ExpectedUpdatedAt: old.UpdatedAt, ActorID: c.ActorID, PlanID: c.PlanID, SessionID: p.SessionID})
				} else {
					_, err = q.InsertFocusedWorkoutGymPrescription(ctx, dbgen.InsertFocusedWorkoutGymPrescriptionParams{ActorID: c.ActorID, PlanID: c.PlanID, SessionID: p.SessionID, SegmentID: a.ID, BlockID: b.ID, Structure: g.Structure, Objective: g.Objective, Rounds: g.Rounds, RoundRecoverySeconds: g.RoundRecovery})
				}
				if err != nil {
					return err
				}
				for xi, x := range g.Exercises {
					if old, retained := exercises[x.ID]; retained {
						_, err = q.UpdateFocusedWorkoutGymExercise(ctx, dbgen.UpdateFocusedWorkoutGymExerciseParams{Position: int32(xi + 1), Name: x.Name, Sets: x.Sets, Repetitions: x.Repetitions, DurationSeconds: x.Duration, DistanceMetres: x.Distance, RecoverySeconds: x.Recovery, ResistanceKind: x.ResistanceKind, ResistanceValue: x.ResistanceValue, ResistanceText: x.ResistanceText, ExecutionIntent: x.Intent, Tempo: x.Tempo, Notes: x.Notes, ExerciseID: x.ID, BlockID: b.ID, SegmentID: a.ID, ExpectedUpdatedAt: old.UpdatedAt, ActorID: c.ActorID, PlanID: c.PlanID, SessionID: p.SessionID})
					} else {
						_, err = q.InsertFocusedWorkoutGymExercise(ctx, dbgen.InsertFocusedWorkoutGymExerciseParams{ActorID: c.ActorID, PlanID: c.PlanID, SessionID: p.SessionID, SegmentID: a.ID, BlockID: b.ID, ExerciseID: x.ID, Position: int32(xi + 1), Name: x.Name, Sets: x.Sets, Repetitions: x.Repetitions, DurationSeconds: x.Duration, DistanceMetres: x.Distance, RecoverySeconds: x.Recovery, ResistanceKind: x.ResistanceKind, ResistanceValue: x.ResistanceValue, ResistanceText: x.ResistanceText, ExecutionIntent: x.Intent, Tempo: x.Tempo, Notes: x.Notes})
					}
					if err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}
