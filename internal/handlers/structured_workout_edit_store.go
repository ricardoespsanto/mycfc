package handlers

import (
	"context"
	"fmt"
	"github.com/cfcoimbra/mycfc/internal/db"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"sort"
	"time"
)

type FocusedWorkoutEditRequest struct{ ActorID, PlanID, SessionID uuid.UUID }
type FocusedWorkoutEditStore interface {
	LoadFocusedEditSession(context.Context, FocusedWorkoutEditRequest) (focusedEditSource, error)
	LoadFocusedEdit(context.Context, FocusedWorkoutEditRequest) (focusedEditSource, error)
	ReadFocusedEditPreview(context.Context, FocusedWorkoutEditRequest, []uuid.UUID) (focusedEditSource, error)
	SaveFocusedEdit(context.Context, focusedEditReviewed) (FocusedWorkoutSaveResult, error)
	CancelFocusedEdit(context.Context, FocusedWorkoutEditRequest, time.Time, string) (FocusedWorkoutSaveResult, error)
}

func (s PostgresFocusedWorkoutStore) LoadFocusedEditSession(ctx context.Context, in FocusedWorkoutEditRequest) (focusedEditSource, error) {
	var out focusedEditSource
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
	out.Raw.Session, err = q.GetFocusedWorkoutSession(ctx, dbgen.GetFocusedWorkoutSessionParams{ActorID: in.ActorID, PlanID: in.PlanID, SessionID: in.SessionID})
	if err != nil {
		return out, err
	}
	schedule, err := trainingScheduleFromRecord(out.Raw.Session.SchedulePrecision, out.Raw.Session.ScheduledOn, out.Raw.Session.StartsAt, out.Raw.Session.EndsAt, out.Raw.Session.DayBand)
	if err != nil {
		return out, err
	}
	day, err := focusedDay(schedule.Date)
	if err != nil {
		return out, err
	}
	row, err := q.GetFocusedWaterCreateContext(ctx, dbgen.GetFocusedWaterCreateContextParams{ActorID: in.ActorID, PlanID: in.PlanID, Day: day})
	if err != nil {
		return out, err
	}
	out.Context = focusedContext(in.ActorID, row, state)
	if err = tx.QueryRow(ctx, `SELECT pg_catalog.clock_timestamp()`).Scan(&out.DecisionAt); err != nil {
		return out, err
	}
	if err = tx.Commit(ctx); err != nil {
		return focusedEditSource{}, err
	}
	return out, nil
}
func (s PostgresFocusedWorkoutStore) LoadFocusedEdit(ctx context.Context, in FocusedWorkoutEditRequest) (focusedEditSource, error) {
	return s.ReadFocusedEditPreview(ctx, in, nil)
}
func (s PostgresFocusedWorkoutStore) ReadFocusedEditPreview(ctx context.Context, in FocusedWorkoutEditRequest, selected []uuid.UUID) (focusedEditSource, error) {
	var out focusedEditSource
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
	out.Raw.Session, err = q.GetFocusedWorkoutSession(ctx, dbgen.GetFocusedWorkoutSessionParams{ActorID: in.ActorID, PlanID: in.PlanID, SessionID: in.SessionID})
	if err != nil {
		return out, err
	}
	if err = readFocusedEditContent(ctx, q, in, &out.Raw, false); err != nil {
		return out, err
	}
	if err = tx.QueryRow(ctx, `SELECT pg_catalog.clock_timestamp()`).Scan(&out.DecisionAt); err != nil {
		return out, err
	}
	if err = completeFocusedEditSource(ctx, q, in, &out, state, selected, nil); err != nil {
		return out, err
	}
	if err = tx.Commit(ctx); err != nil {
		return focusedEditSource{}, err
	}
	return out, nil
}
func readFocusedEditContent(ctx context.Context, q *dbgen.Queries, in FocusedWorkoutEditRequest, raw *focusedEditRaw, locked bool) error {
	var err error
	if locked {
		raw.Segments, err = q.LockFocusedWorkoutSegments(ctx, dbgen.LockFocusedWorkoutSegmentsParams{ActorID: in.ActorID, PlanID: in.PlanID, SessionID: in.SessionID})
	} else {
		raw.Segments, err = q.ListFocusedWorkoutSegments(ctx, dbgen.ListFocusedWorkoutSegmentsParams{ActorID: in.ActorID, PlanID: in.PlanID, SessionID: in.SessionID})
	}
	if err != nil {
		return err
	}
	if locked {
		raw.Blocks, err = q.LockFocusedWorkoutBlocks(ctx, dbgen.LockFocusedWorkoutBlocksParams{ActorID: in.ActorID, PlanID: in.PlanID, SessionID: in.SessionID})
	} else {
		raw.Blocks, err = q.ListFocusedWorkoutBlocks(ctx, dbgen.ListFocusedWorkoutBlocksParams{ActorID: in.ActorID, PlanID: in.PlanID, SessionID: in.SessionID})
	}
	if err != nil {
		return err
	}
	if locked {
		raw.Water, err = q.LockFocusedWorkoutWaterPrescriptions(ctx, dbgen.LockFocusedWorkoutWaterPrescriptionsParams{ActorID: in.ActorID, PlanID: in.PlanID, SessionID: in.SessionID})
	} else {
		raw.Water, err = q.ListFocusedWorkoutWaterPrescriptions(ctx, dbgen.ListFocusedWorkoutWaterPrescriptionsParams{ActorID: in.ActorID, PlanID: in.PlanID, SessionID: in.SessionID})
	}
	if err != nil {
		return err
	}
	if locked {
		raw.Steps, err = q.LockFocusedWorkoutWaterSteps(ctx, dbgen.LockFocusedWorkoutWaterStepsParams{ActorID: in.ActorID, PlanID: in.PlanID, SessionID: in.SessionID})
	} else {
		raw.Steps, err = q.ListFocusedWorkoutWaterSteps(ctx, dbgen.ListFocusedWorkoutWaterStepsParams{ActorID: in.ActorID, PlanID: in.PlanID, SessionID: in.SessionID})
	}
	if err != nil {
		return err
	}
	if locked {
		raw.Gym, err = q.LockFocusedWorkoutGymPrescriptions(ctx, dbgen.LockFocusedWorkoutGymPrescriptionsParams{ActorID: in.ActorID, PlanID: in.PlanID, SessionID: in.SessionID})
	} else {
		raw.Gym, err = q.ListFocusedWorkoutGymPrescriptions(ctx, dbgen.ListFocusedWorkoutGymPrescriptionsParams{ActorID: in.ActorID, PlanID: in.PlanID, SessionID: in.SessionID})
	}
	if err != nil {
		return err
	}
	if locked {
		raw.Exercises, err = q.LockFocusedWorkoutGymExercises(ctx, dbgen.LockFocusedWorkoutGymExercisesParams{ActorID: in.ActorID, PlanID: in.PlanID, SessionID: in.SessionID})
	} else {
		raw.Exercises, err = q.ListFocusedWorkoutGymExercises(ctx, dbgen.ListFocusedWorkoutGymExercisesParams{ActorID: in.ActorID, PlanID: in.PlanID, SessionID: in.SessionID})
	}
	if err != nil {
		return err
	}
	_, err = focusedEditOwnDigest(FocusedWorkoutContext{}, *raw)
	return err
}
func completeFocusedEditSource(ctx context.Context, q *dbgen.Queries, in FocusedWorkoutEditRequest, out *focusedEditSource, state db.TrainingScheduleState, selected []uuid.UUID, locked []dbgen.WaterIntensityProfile) error {
	schedule, err := trainingScheduleFromRecord(out.Raw.Session.SchedulePrecision, out.Raw.Session.ScheduledOn, out.Raw.Session.StartsAt, out.Raw.Session.EndsAt, out.Raw.Session.DayBand)
	if err != nil {
		return err
	}
	day, err := focusedDay(schedule.Date)
	if err != nil {
		return err
	}
	row, err := q.GetFocusedWaterCreateContext(ctx, dbgen.GetFocusedWaterCreateContextParams{ActorID: in.ActorID, PlanID: in.PlanID, Day: day})
	if err != nil {
		return err
	}
	out.Context = focusedContext(in.ActorID, row, state)
	identity, err := focusedEditRawIdentity(out.Raw, out.Context)
	if err != nil {
		return err
	}
	out.Graph, err = focusedGraphFromRaw(out.Raw, identity, false)
	if err != nil {
		return err
	}
	if selected == nil {
		selected = focusedGraphSelectedProfileIDs(out.Graph)
	}
	out.Graph.References, err = readFocusedEditReferences(ctx, q, in, out.Graph, selected, locked)
	return err
}
func focusedEditPinnedBlocks(source focusedWorkoutGraph) []uuid.UUID {
	out := []uuid.UUID{}
	for _, a := range source.Activities {
		for _, b := range a.Phases {
			if b.Water != nil && b.Water.ProfileID != nil {
				out = append(out, b.ID)
			}
		}
	}
	return focusedEditSortedIDs(out)
}
func readFocusedEditReferences(ctx context.Context, q *dbgen.Queries, in FocusedWorkoutEditRequest, source focusedWorkoutGraph, selected []uuid.UUID, locked []dbgen.WaterIntensityProfile) (focusedGraphProfileFacts, error) {
	facts := focusedGraphProfileFacts{Profiles: []dbgen.WaterIntensityProfile{}, Zones: []dbgen.WaterIntensityZone{}}
	ids, err := focusedOrderedProfileIDs(selected)
	if err != nil {
		return facts, err
	}
	if len(ids) == 0 {
		return facts, nil
	}
	pins := focusedEditPinnedBlocks(source)
	if locked == nil {
		facts.Profiles, err = q.ListFocusedWorkoutEditProfiles(ctx, dbgen.ListFocusedWorkoutEditProfilesParams{ActorID: in.ActorID, PlanID: in.PlanID, SessionID: in.SessionID, ProfileIds: ids, PinnedBlockIds: pins})
	} else {
		facts.Profiles = locked
	}
	if err != nil {
		return facts, err
	}
	if len(facts.Profiles) != len(ids) {
		return facts, errFocusedWorkoutConflict
	}
	for i, r := range facts.Profiles {
		if r.ID != ids[i] {
			return facts, errFocusedWorkoutConflict
		}
		r.CreatedAt.Time = r.CreatedAt.Time.UTC().Truncate(time.Microsecond)
		facts.Profiles[i] = r
	}
	facts.Zones, err = q.ListFocusedWorkoutEditZones(ctx, dbgen.ListFocusedWorkoutEditZonesParams{ActorID: in.ActorID, PlanID: in.PlanID, SessionID: in.SessionID, ProfileIds: ids, PinnedBlockIds: pins})
	if err != nil {
		return facts, err
	}
	if len(facts.Zones) > 256 {
		return facts, errFocusedWorkoutLimit
	}
	return facts, nil
}

// Stable editor keys are assigned only from complete scoped native rows. Native
// positions remain in OwnRawDigest; ordering never changes identities/parents.
func focusedEditRawIdentity(raw focusedEditRaw, c FocusedWorkoutContext) (focusedWorkoutGraph, error) {
	p := focusedWorkoutGraph{Context: c, SessionID: raw.Session.ID, Activities: []focusedGraphActivityInput{}}
	sort.Slice(raw.Segments, func(i, j int) bool {
		if raw.Segments[i].Position != raw.Segments[j].Position {
			return raw.Segments[i].Position < raw.Segments[j].Position
		}
		return raw.Segments[i].ID.String() < raw.Segments[j].ID.String()
	})
	sort.Slice(raw.Blocks, func(i, j int) bool {
		if raw.Blocks[i].Position != raw.Blocks[j].Position {
			return raw.Blocks[i].Position < raw.Blocks[j].Position
		}
		return raw.Blocks[i].ID.String() < raw.Blocks[j].ID.String()
	})
	sort.Slice(raw.Exercises, func(i, j int) bool {
		if raw.Exercises[i].Position != raw.Exercises[j].Position {
			return raw.Exercises[i].Position < raw.Exercises[j].Position
		}
		return raw.Exercises[i].ID.String() < raw.Exercises[j].ID.String()
	})
	for ai, a := range raw.Segments {
		activity := focusedGraphActivityInput{Key: fmt.Sprintf("a_%d", ai+1), ID: a.ID, Phases: []focusedGraphPhaseInput{}}
		for _, b := range raw.Blocks {
			if b.SegmentID != a.ID {
				continue
			}
			phase := focusedGraphPhaseInput{Key: fmt.Sprintf("p_%d", len(activity.Phases)+1), ID: b.ID}
			keys := map[uuid.UUID]string{}
			stepIDs := []uuid.UUID{}
			for _, n := range raw.Steps {
				if n.BlockID == b.ID {
					stepIDs = append(stepIDs, n.ID)
				}
			}
			focusedEditSortedIDs(stepIDs)
			for i, id := range stepIDs {
				keys[id] = fmt.Sprintf("n_%d", i+1)
			}
			nodes := []focusedGraphWaterNodeInput{}
			for _, n := range raw.Steps {
				if n.BlockID != b.ID {
					continue
				}
				parent := ""
				if n.ParentStepID != nil {
					parent = keys[*n.ParentStepID]
					if parent == "" {
						return p, errFocusedWorkoutInvalid
					}
				}
				nodes = append(nodes, focusedGraphWaterNodeInput{Key: keys[n.ID], ID: n.ID, ParentKey: parent})
			}
			for _, w := range raw.Water {
				if w.BlockID == b.ID {
					phase.Water = &focusedGraphWaterInput{Nodes: nodes}
				}
			}
			for _, g := range raw.Gym {
				if g.BlockID == b.ID {
					phase.Gym = &focusedGraphGymInput{Exercises: []focusedGraphExerciseInput{}}
					for _, x := range raw.Exercises {
						if x.BlockID == b.ID {
							phase.Gym.Exercises = append(phase.Gym.Exercises, focusedGraphExerciseInput{Key: fmt.Sprintf("x_%d", len(phase.Gym.Exercises)+1), ID: x.ID})
						}
					}
				}
			}
			activity.Phases = append(activity.Phases, phase)
		}
		p.Activities = append(p.Activities, activity)
	}
	// Lock queries intentionally order by UUID. Reorder the native tree before
	// mapping, retaining every row and rejecting cycles/unreachable descendants.
	ordered := []dbgen.WaterWorkStep{}
	used := map[uuid.UUID]bool{}
	var visit func(uuid.UUID, *uuid.UUID, int) error
	visit = func(block uuid.UUID, parent *uuid.UUID, depth int) error {

		siblings := []dbgen.WaterWorkStep{}
		for _, n := range raw.Steps {
			if n.BlockID != block {
				continue
			}
			if parent == nil && n.ParentStepID == nil || parent != nil && n.ParentStepID != nil && *parent == *n.ParentStepID {
				siblings = append(siblings, n)
			}
		}
		if len(siblings) > 0 && depth > 8 {
			return errFocusedWorkoutLimit
		}
		sort.Slice(siblings, func(i, j int) bool {
			if siblings[i].Position != siblings[j].Position {
				return siblings[i].Position < siblings[j].Position
			}
			return siblings[i].ID.String() < siblings[j].ID.String()
		})
		for _, n := range siblings {
			if used[n.ID] {
				return errFocusedWorkoutInvalid
			}
			used[n.ID] = true
			ordered = append(ordered, n)
			if err := visit(block, &n.ID, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	for _, b := range raw.Blocks {
		if err := visit(b.ID, nil, 1); err != nil {
			return p, err
		}
	}
	if len(ordered) != len(raw.Steps) {
		return p, errFocusedWorkoutInvalid
	}
	copy(raw.Steps, ordered)
	return p, nil
}
