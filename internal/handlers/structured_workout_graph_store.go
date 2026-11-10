package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type focusedGraphProfileChoice struct {
	ID       uuid.UUID
	Name     string
	Craft    dbgen.PaddlingCraft
	Revision int32
}
type focusedGraphPreviewSource struct {
	Context    FocusedWorkoutContext
	References focusedGraphProfileFacts
}
type FocusedWorkoutGraphStore interface {
	LoadFocusedWorkout(context.Context, FocusedWorkoutRequest) (FocusedWorkoutContext, error)
	FocusedWorkoutArtifactSize(context.Context, FocusedWorkoutRequest, []byte) (int32, error)
	ListFocusedGraphProfileChoices(context.Context, FocusedWorkoutRequest) ([]focusedGraphProfileChoice, bool, error)
	ReadFocusedGraphPreview(context.Context, FocusedWorkoutRequest, []uuid.UUID) (focusedGraphPreviewSource, error)
	SaveFocusedGraph(context.Context, focusedGraphReviewed) (FocusedWorkoutSaveResult, error)
}

func (s PostgresFocusedWorkoutStore) ListFocusedGraphProfileChoices(ctx context.Context, in FocusedWorkoutRequest) ([]focusedGraphProfileChoice, bool, error) {
	rows, err := dbgen.New(s.Pool).ListFocusedWorkoutActiveProfiles(ctx, dbgen.ListFocusedWorkoutActiveProfilesParams{ActorID: in.ActorID, PlanID: in.PlanID})
	if err != nil {
		return nil, false, err
	}
	if len(rows) > 100 {
		return nil, true, nil
	}
	out := []focusedGraphProfileChoice{}
	for _, r := range rows {
		out = append(out, focusedGraphProfileChoice{r.ID, r.Name, r.Craft, r.Revision})
	}
	return out, false, nil
}
func (s PostgresFocusedWorkoutStore) ReadFocusedGraphPreview(ctx context.Context, in FocusedWorkoutRequest, ids []uuid.UUID) (focusedGraphPreviewSource, error) {
	var out focusedGraphPreviewSource
	day, err := focusedDay(in.Day)
	if err != nil {
		return out, pgx.ErrNoRows
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	state, err := db.VerifyTrainingWorkoutGraphRuntime(ctx, tx, focusedWorkoutStateCapabilities())
	if err != nil {
		return out, err
	}
	q := dbgen.New(tx)
	row, err := q.GetFocusedWaterCreateContext(ctx, dbgen.GetFocusedWaterCreateContextParams{ActorID: in.ActorID, PlanID: in.PlanID, Day: day})
	if err != nil {
		return out, err
	}
	out.Context = focusedContext(in.ActorID, row, state)
	out.References, err = readFocusedGraphReferences(ctx, q, in, ids, nil)
	if err != nil {
		return out, err
	}
	if err = tx.Commit(ctx); err != nil {
		return focusedGraphPreviewSource{}, err
	}
	return out, nil
}
func focusedOrderedProfileIDs(ids []uuid.UUID) ([]uuid.UUID, error) {
	if len(ids) > 32 {
		return nil, errFocusedWorkoutLimit
	}
	out := append([]uuid.UUID(nil), ids...)
	sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i][:], out[j][:]) < 0 })
	for i, id := range out {
		if id == uuid.Nil || i > 0 && id == out[i-1] {
			return nil, errFocusedWorkoutInvalid
		}
	}
	return out, nil
}
func readFocusedGraphReferences(ctx context.Context, q *dbgen.Queries, in FocusedWorkoutRequest, ids []uuid.UUID, locked []dbgen.WaterIntensityProfile) (focusedGraphProfileFacts, error) {
	facts := focusedGraphProfileFacts{Profiles: []dbgen.WaterIntensityProfile{}, Zones: []dbgen.WaterIntensityZone{}}
	ordered, err := focusedOrderedProfileIDs(ids)
	if err != nil {
		return facts, err
	}
	if locked != nil {
		if len(locked) != len(ordered) {
			return facts, errFocusedWorkoutConflict
		}
		facts.Profiles = append(facts.Profiles, locked...)
	} else {
		for _, id := range ordered {
			r, err := q.GetFocusedWorkoutSelectedProfile(ctx, dbgen.GetFocusedWorkoutSelectedProfileParams{ActorID: in.ActorID, PlanID: in.PlanID, ProfileID: id})
			if errors.Is(err, pgx.ErrNoRows) {
				return facts, errFocusedWorkoutConflict
			}
			if err != nil {
				return facts, err
			}
			facts.Profiles = append(facts.Profiles, r)
		}
	}
	for i, r := range facts.Profiles {
		if r.ID != ordered[i] || !r.IsActive {
			return facts, errFocusedWorkoutConflict
		}
		r.CreatedAt.Time = r.CreatedAt.Time.UTC().Truncate(time.Microsecond)
		facts.Profiles[i] = r
	}
	if len(ordered) > 0 {
		facts.Zones, err = q.ListFocusedWorkoutSelectedZones(ctx, dbgen.ListFocusedWorkoutSelectedZonesParams{ActorID: in.ActorID, PlanID: in.PlanID, ProfileIds: ordered})
		if err != nil {
			return facts, err
		}
		if len(facts.Zones) > 256 {
			return facts, errFocusedWorkoutLimit
		}
	}
	return facts, nil
}
func focusedGraphJSONEqual(a, b any) bool {
	x, xe := json.Marshal(a)
	y, ye := json.Marshal(b)
	return xe == nil && ye == nil && bytes.Equal(x, y)
}
func (s PostgresFocusedWorkoutStore) SaveFocusedGraph(ctx context.Context, in focusedGraphReviewed) (FocusedWorkoutSaveResult, error) {
	var result FocusedWorkoutSaveResult
	if !in.verified || !time.Now().Before(in.expiresAt) {
		return result, errFocusedWorkoutInvalid
	}
	p := in.Proposal
	c := p.Context
	request := FocusedWorkoutRequest{c.ActorID, c.PlanID, c.Day}
	outcome, err := db.WithRecipientPublicationTx(ctx, s.Pool, db.RecipientTxOptions{AdmissionTimeout: 5 * time.Second, TransactionTimeout: 10 * time.Second, CleanupTimeout: 5 * time.Second}, func(txCtx context.Context, commands dbgen.DBTX) error {
		// The capability row is admitted before runtime, actor, plan or references.
		if _, err := commands.Exec(txCtx, `SELECT public.training_schedule_admit_partial($1,$2)`, c.DatabaseIncarnation, c.StateEpoch); err != nil {
			return trainingScheduleWriteError(err)
		}
		state, err := db.VerifyTrainingWorkoutGraphRuntime(txCtx, commands, focusedWorkoutStateCapabilities())
		if err != nil {
			return err
		}
		if state.SchemaContract != "schedule-p1" || !state.PartialV2Enabled || state.StateEpoch != c.StateEpoch || state.DatabaseIncarnation != c.DatabaseIncarnation {
			return errFocusedWorkoutClosed
		}
		if err = lockTrainingGroupActor(txCtx, commands, c.ActorID); err != nil {
			return err
		}
		q := dbgen.New(commands)
		locked, err := q.LockFocusedWaterCreatePlan(txCtx, dbgen.LockFocusedWaterCreatePlanParams{ActorID: c.ActorID, PlanID: c.PlanID})
		if err != nil {
			return err
		}
		if locked.GroupID != c.GroupID || locked.SeasonID != c.SeasonID || locked.GroupName != c.GroupName || locked.PlanTitle != c.PlanTitle || !locked.WeekStart.Time.Equal(c.WeekStart) {
			return errFocusedWorkoutConflict
		}
		ids := focusedGraphSelectedProfileIDs(p)
		var profiles []dbgen.WaterIntensityProfile
		if len(ids) > 0 {
			profiles, err = q.LockFocusedWorkoutSelectedProfiles(txCtx, dbgen.LockFocusedWorkoutSelectedProfilesParams{ActorID: c.ActorID, PlanID: c.PlanID, ProfileIds: ids})
			if err != nil {
				return publicationNowaitError(err)
			}
			if len(profiles) != len(ids) {
				return errFocusedWorkoutConflict
			}
		}
		refs, err := readFocusedGraphReferences(txCtx, q, request, ids, profiles)
		if err != nil {
			return err
		}
		if !focusedGraphJSONEqual(refs, p.References) {
			return errFocusedWorkoutConflict
		}
		existing, readErr := readFocusedGraph(txCtx, q, p)
		if readErr == nil {
			if !focusedGraphJSONEqual(p, existing) {
				return errFocusedWorkoutConflict
			}
			result = FocusedWorkoutSaveResult{SessionID: p.SessionID, AlreadyCreated: true}
		} else {
			if !errors.Is(readErr, pgx.ErrNoRows) {
				return readErr
			}
			if !locked.PlanUpdatedAt.Valid || !locked.PlanUpdatedAt.Time.Equal(c.PlanUpdatedAt) {
				return errFocusedWorkoutConflict
			}
			if err = insertFocusedGraph(txCtx, q, p); err != nil {
				return err
			}
			saved, err := readFocusedGraph(txCtx, q, p)
			if err != nil {
				return err
			}
			if !focusedGraphJSONEqual(p, saved) {
				return errFocusedWorkoutConflict
			}
			result = FocusedWorkoutSaveResult{SessionID: p.SessionID}
		}
		raw, err := focusedGraphArtifact(p)
		if err != nil {
			return err
		}
		size, err := q.FocusedWaterArtifactSize(txCtx, dbgen.FocusedWaterArtifactSizeParams{ActorID: c.ActorID, PlanID: c.PlanID, Snapshot: raw})
		if err != nil {
			return err
		}
		if size > focusedWorkoutMaxArtifactBytes {
			return errFocusedWorkoutLimit
		}
		day, _ := focusedDay(c.Day)
		final, err := q.GetFocusedWaterCreateContext(txCtx, dbgen.GetFocusedWaterCreateContextParams{ActorID: c.ActorID, PlanID: c.PlanID, Day: day})
		if err != nil {
			return err
		}
		if final.GroupID != c.GroupID || final.SeasonID != c.SeasonID || final.PlanTitle != c.PlanTitle || final.GroupName != c.GroupName {
			return errFocusedWorkoutConflict
		}
		// Profiles are held SHARE; supported zone writers must first version that
		// profile. Re-read the complete selected facts, never a caller-supplied hash.
		finalRefs, err := readFocusedGraphReferences(txCtx, q, request, ids, nil)
		if err != nil {
			return err
		}
		if !focusedGraphJSONEqual(finalRefs, p.References) {
			return errFocusedWorkoutConflict
		}
		var decision time.Time
		if err = commands.QueryRow(txCtx, `SELECT pg_catalog.clock_timestamp()`).Scan(&decision); err != nil {
			return err
		}
		if !decision.Before(in.expiresAt) {
			return errFocusedWorkoutInvalid
		}
		return nil
	})
	result.CleanupFailed = outcome.CleanupFailed
	if err = publicationTransactionError(outcome, err); err != nil {
		return FocusedWorkoutSaveResult{}, err
	}
	return result, nil
}
func insertFocusedGraph(ctx context.Context, q *dbgen.Queries, p focusedWorkoutGraph) error {
	c := p.Context
	date, _ := focusedDay(c.Day)
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
	_, err := q.InsertFocusedWorkoutSession(ctx, dbgen.InsertFocusedWorkoutSessionParams{ActorID: c.ActorID, PlanID: c.PlanID, SessionID: p.SessionID, ExpectedPlanUpdatedAt: pgtype.Timestamptz{Time: c.PlanUpdatedAt, Valid: true}, Title: p.Title, Description: p.Description, EntryKind: p.Kind, SchedulePrecision: string(p.Schedule.Precision), ScheduledOn: date, StartsAt: starts, EndsAt: ends, DayBand: band})
	if err != nil {
		return err
	}
	return insertFocusedGraphContents(ctx, q, p, nil)
}

// The same explicit-ID content writer is reused for source-bound copy inserts.
// A supplied water adapter derives the pin from an authorized source path.
type focusedGraphWaterInsert func(context.Context, *dbgen.Queries, focusedWorkoutGraph, focusedGraphActivityInput, focusedGraphPhaseInput) error

func insertFocusedGraphContents(ctx context.Context, q *dbgen.Queries, p focusedWorkoutGraph, waterInsert focusedGraphWaterInsert) error {
	c := p.Context
	var err error
	for ai, a := range p.Activities {
		_, err = q.InsertFocusedWorkoutSegment(ctx, dbgen.InsertFocusedWorkoutSegmentParams{ActorID: c.ActorID, PlanID: c.PlanID, SessionID: p.SessionID, SegmentID: a.ID, Position: int32(ai + 1), Modality: a.Modality, Title: a.Title, Location: a.Location, PlannedDurationMinutes: a.DurationMinutes, PlannedStartOffsetMinutes: a.StartOffsetMinutes, TransitionDurationMinutes: a.TransitionMinutes, EquipmentNotes: a.Equipment})
		if err != nil {
			return err
		}
		for bi, b := range a.Phases {
			_, err = q.InsertFocusedWorkoutBlock(ctx, dbgen.InsertFocusedWorkoutBlockParams{ActorID: c.ActorID, PlanID: c.PlanID, SessionID: p.SessionID, SegmentID: a.ID, BlockID: b.ID, Position: int32(bi + 1), Purpose: b.Purpose, Title: b.Title, Instructions: b.Instructions})
			if err != nil {
				return err
			}
			if w := b.Water; w != nil {
				if waterInsert != nil {
					err = waterInsert(ctx, q, p, a, b)
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
					_, err = q.InsertFocusedWorkoutWaterStep(ctx, dbgen.InsertFocusedWorkoutWaterStepParams{ActorID: c.ActorID, PlanID: c.PlanID, SessionID: p.SessionID, SegmentID: a.ID, BlockID: b.ID, StepID: n.ID, ParentStepID: parent, Position: positions[n.ParentKey], Kind: n.Kind, Name: n.Name, Repeats: n.Repeats, DurationSeconds: n.Duration, DurationCertainty: n.DurationCertainty, DistanceMetres: n.Distance, DistanceCertainty: n.DistanceCertainty, RecoverySeconds: n.Recovery, IntensityCode: n.Intensity, CadenceSpm: n.Cadence, DrillFocus: n.DrillFocus, DrillFormat: n.DrillFormat, RoleNotes: n.RoleNotes, Instructions: n.Instructions})
					if err != nil {
						return err
					}
				}
			}
			if g := b.Gym; g != nil {
				_, err = q.InsertFocusedWorkoutGymPrescription(ctx, dbgen.InsertFocusedWorkoutGymPrescriptionParams{ActorID: c.ActorID, PlanID: c.PlanID, SessionID: p.SessionID, SegmentID: a.ID, BlockID: b.ID, Structure: g.Structure, Objective: g.Objective, Rounds: g.Rounds, RoundRecoverySeconds: g.RoundRecovery})
				if err != nil {
					return err
				}
				for xi, x := range g.Exercises {
					_, err = q.InsertFocusedWorkoutGymExercise(ctx, dbgen.InsertFocusedWorkoutGymExerciseParams{ActorID: c.ActorID, PlanID: c.PlanID, SessionID: p.SessionID, SegmentID: a.ID, BlockID: b.ID, ExerciseID: x.ID, Position: int32(xi + 1), Name: x.Name, Sets: x.Sets, Repetitions: x.Repetitions, DurationSeconds: x.Duration, DistanceMetres: x.Distance, RecoverySeconds: x.Recovery, ResistanceKind: x.ResistanceKind, ResistanceValue: x.ResistanceValue, ResistanceText: x.ResistanceText, ExecutionIntent: x.Intent, Tempo: x.Tempo, Notes: x.Notes})
					if err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}
