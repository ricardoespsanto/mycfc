package handlers

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type reuseSaveResult struct {
	PlanID, GroupID, SessionID   uuid.UUID
	Day                          string
	Count                        int
	AlreadyCopied, CleanupFailed bool
}

func reuseOriginBranch(source reuseSourceMaterial) (*uuid.UUID, *uuid.UUID) {
	if source.Selector.Kind == "ROUTINE" {
		return nil, focusedPointer(source.Selector.RoutineID)
	}
	return focusedPointer(source.Selector.PlanID), nil
}
func reuseWaterInsert(destination reuseDestination) focusedGraphWaterInsert {
	return func(ctx context.Context, q *dbgen.Queries, p focusedWorkoutGraph, a focusedGraphActivityInput, b focusedGraphPhaseInput) error {
		origin, ok := destination.Origins[b.ID]
		if !ok {
			return errFocusedWorkoutInvalid
		}
		row, err := q.InsertCopiedWaterPrescription(ctx, dbgen.InsertCopiedWaterPrescriptionParams{ActorID: p.Context.ActorID, TargetPlanID: p.Context.PlanID, TargetSessionID: p.SessionID, TargetSegmentID: a.ID, TargetBlockID: b.ID, NativeSourcePlanID: origin.NativePlanID, NativeSourceBlockID: origin.NativeBlockID, RoutineID: origin.RoutineID, RoutinePath: origin.RoutinePath})
		if err != nil {
			return err
		}
		if b.Water == nil || row.Method != b.Water.Method || !focusedGraphJSONEqual(row.IntensityProfileID, b.Water.ProfileID) || !focusedGraphJSONEqual(row.TargetDistanceMetres, b.Water.TargetDistance) || !focusedGraphJSONEqual(row.TargetDistanceCertainty, b.Water.TargetCertainty) {
			return errReuseChanged
		}
		return nil
	}
}
func insertReuseDestination(ctx context.Context, q *dbgen.Queries, d reuseDestination, expected time.Time) error {
	p, c := d.Graph, d.Graph.Context
	date, err := focusedDay(p.Schedule.Date)
	if err != nil {
		return err
	}
	nativeDate := date
	if d.NativeNullDate {
		nativeDate = pgtype.Date{}
	}
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
	_, err = q.InsertCopiedWorkoutSession(ctx, dbgen.InsertCopiedWorkoutSessionParams{ID: p.SessionID, ActorID: c.ActorID, TargetPlanID: c.PlanID, ExpectedPlanUpdatedAt: pgtype.Timestamptz{Time: expected, Valid: true}, TargetDay: date, Title: p.Title, Description: p.Description, EntryKind: p.Kind, SchedulePrecision: string(p.Schedule.Precision), ScheduledOn: nativeDate, StartsAt: starts, EndsAt: ends, DayBand: band, ModalityID: d.ModalityID})
	if err != nil {
		return err
	}
	return insertFocusedGraphContents(ctx, q, p, reuseWaterInsert(d))
}
func readReuseDestination(ctx context.Context, q *dbgen.Queries, actor uuid.UUID, d reuseDestination) (focusedEditRaw, error) {
	var raw focusedEditRaw
	p := d.Graph
	in := FocusedWorkoutEditRequest{actor, p.Context.PlanID, p.SessionID}
	var err error
	raw.Session, err = q.GetFocusedWorkoutSession(ctx, dbgen.GetFocusedWorkoutSessionParams{ActorID: actor, PlanID: in.PlanID, SessionID: in.SessionID})
	if err != nil {
		return raw, err
	}
	if raw.Session.Status != "ACTIVE" || raw.Session.ScheduledOn.Valid == d.NativeNullDate || !focusedGraphJSONEqual(raw.Session.ModalityID, d.ModalityID) {
		return raw, errReuseChanged
	}
	if err = readFocusedEditContent(ctx, q, in, &raw, false); err != nil {
		return raw, err
	}
	if _, err = focusedEditRawIdentity(raw, p.Context); err != nil {
		return raw, err
	}
	graph, err := focusedGraphFromRaw(raw, p, true)
	if err != nil {
		return raw, err
	}
	if !focusedGraphJSONEqual(graph, p) {
		return raw, errReuseChanged
	}
	return raw, nil
}
func reuseExpectedEvent(row dbgen.TrainingCopyEvent, e reuseEvent, actor uuid.UUID) bool {
	return row.ID == e.ID && row.SourceKind == e.SourceKind && row.SourceID == e.SourceID && row.SourceUpdatedAt.Valid && row.SourceUpdatedAt.Time.Equal(e.SourceUpdatedAt) && row.DestinationKind == e.DestinationKind && row.DestinationID == e.DestinationID && row.CopiedByID != nil && *row.CopiedByID == actor
}
func reuseReplay(ctx context.Context, q *dbgen.Queries, p reuseReviewPayload) (bool, error) {
	ids := []uuid.UUID{}
	for _, e := range p.Proposal.Events {
		ids = append(ids, e.ID)
	}
	native, routine := reuseOriginBranch(p.Source)
	rows, err := q.GetReuseCopyEventsByIDs(ctx, dbgen.GetReuseCopyEventsByIDsParams{ActorID: p.ActorID, EventIds: ids, SourcePlanID: native, RoutineID: routine, TargetPlanID: p.Proposal.TargetPlanID})
	if err != nil {
		return false, err
	}
	if len(rows) == 0 {
		return false, nil
	}
	if len(rows) != len(ids) {
		return false, errReuseChanged
	}
	events := map[uuid.UUID]reuseEvent{}
	for _, e := range p.Proposal.Events {
		events[e.ID] = e
	}
	for _, row := range rows {
		e, ok := events[row.ID]
		if !ok || !reuseExpectedEvent(row, e, p.ActorID) {
			return false, errReuseChanged
		}
	}
	for _, d := range p.Proposal.Destinations {
		if _, err = readReuseDestination(ctx, q, p.ActorID, d); err != nil {
			return false, errors.Join(errReuseDestinationChanged, err)
		}
	}
	return true, nil
}
func readReuseTargetReferences(ctx context.Context, q *dbgen.Queries, actor uuid.UUID, target *focusedWorkoutGraph, locked bool) (focusedGraphProfileFacts, error) {
	empty := focusedGraphProfileFacts{Profiles: []dbgen.WaterIntensityProfile{}, Zones: []dbgen.WaterIntensityZone{}}
	if target == nil {
		return empty, nil
	}
	in := FocusedWorkoutEditRequest{actor, target.Context.PlanID, target.SessionID}
	ids := focusedGraphSelectedProfileIDs(*target)
	profiles := []dbgen.WaterIntensityProfile{}
	if locked && len(ids) > 0 {
		var err error
		profiles, err = q.LockFocusedWorkoutEditProfiles(ctx, dbgen.LockFocusedWorkoutEditProfilesParams{ActorID: actor, PlanID: in.PlanID, SessionID: in.SessionID, ProfileIds: ids, PinnedBlockIds: focusedEditPinnedBlocks(*target)})
		if err != nil {
			return empty, publicationNowaitError(err)
		}
	} else if !locked {
		profiles = nil
	}
	return readFocusedEditReferences(ctx, q, in, *target, ids, profiles)
}
func reuseSourceAfterOwnWrites(current, original reuseSourceMaterial, targetPlan uuid.UUID) reuseSourceMaterial {
	if original.Plan == nil || current.Plan == nil || original.Plan.ID != targetPlan || current.Plan.ID != targetPlan {
		return current
	}
	// This plan has remained locked since its exact initial baseline check. Only
	// our retained destination IDs are excluded from the final source set; only
	// our own plan-touch versions are normalized. All native source rows remain exact.
	plan := *current.Plan
	plan.UpdatedAt = original.Plan.UpdatedAt
	current.Plan = &plan
	for i := range current.Items {
		for _, old := range original.Items {
			if current.Items[i].Key == old.Key {
				current.Items[i].Graph.Context.PlanUpdatedAt = old.Graph.Context.PlanUpdatedAt
			}
		}
	}
	return current
}
func reuseSaveError(outcome db.SessionTxResult, err error) error {
	normalized := publicationTransactionError(outcome, err)
	if errors.Is(normalized, db.ErrCommitOutcomeUnknown) {
		return normalized
	}
	var pgError *pgconn.PgError
	if errors.As(normalized, &pgError) && pgError.Code == "23505" && (strings.HasPrefix(pgError.ConstraintName, "training_") || strings.HasPrefix(pgError.ConstraintName, "water_") || strings.HasPrefix(pgError.ConstraintName, "gym_")) {
		return errReuseChanged
	}
	return normalized
}
func (s PostgresStructuredReuseStore) ConfirmReuse(ctx context.Context, in reuseReviewed) (reuseSaveResult, error) {
	var result reuseSaveResult
	p := in.Payload
	if !in.verified || !time.Now().Before(in.expiresAt) {
		return result, errFocusedWorkoutInvalid
	}
	if err := validateReuseEnvelope(p); err != nil {
		return result, err
	}
	c := FocusedWorkoutContext{ActorID: p.ActorID, DatabaseIncarnation: p.Incarnation, StateEpoch: p.Epoch}
	outcome, err := db.WithRecipientPublicationTx(ctx, s.Pool, focusedEditTxOptions(), func(txCtx context.Context, commands dbgen.DBTX) error {
		state, err := admitFocusedEdit(txCtx, commands, c)
		if err != nil {
			return err
		}
		q := dbgen.New(commands)
		// Configuration locks are NOWAIT before the sorted existing-plan locks. A
		// missing target week is never implicitly adopted after another actor creates it.
		week, err := focusedDay(p.Target.Selector.WeekStart)
		if err != nil {
			return err
		}
		if _, err = q.LockReuseTargetContext(txCtx, dbgen.LockReuseTargetContextParams{ActorID: p.ActorID, GroupID: p.Target.Selector.GroupID, SeasonID: p.Target.Selector.SeasonID, WeekStart: week}); err != nil {
			return publicationNowaitError(err)
		}
		planIDs := []uuid.UUID{}
		if p.Source.Plan != nil {
			planIDs = append(planIDs, p.Source.Plan.ID)
		}
		if p.Target.Configuration.ExistingPlanID != nil {
			planIDs = append(planIDs, *p.Target.Configuration.ExistingPlanID)
		}
		if p.Proposal.NewWeek {
			if _, probeErr := q.GetReusePlan(txCtx, dbgen.GetReusePlanParams{ActorID: p.ActorID, PlanID: p.Proposal.TargetPlanID}); probeErr == nil {
				planIDs = append(planIDs, p.Proposal.TargetPlanID)
			} else if !errors.Is(probeErr, pgx.ErrNoRows) {
				return probeErr
			}
		}
		sort.Slice(planIDs, func(i, j int) bool { return planIDs[i].String() < planIDs[j].String() })
		last := uuid.Nil
		for _, id := range planIDs {
			if id == last {
				continue
			}
			last = id
			if _, err = q.LockFocusedWaterCreatePlan(txCtx, dbgen.LockFocusedWaterCreatePlanParams{ActorID: p.ActorID, PlanID: id}); err != nil {
				return err
			}
		}
		if p.Source.Selector.Kind == "ROUTINE" {
			if _, err = q.LockReuseRoutine(txCtx, dbgen.LockReuseRoutineParams{ActorID: p.ActorID, RoutineID: p.Source.Selector.RoutineID}); err != nil {
				return publicationNowaitError(err)
			}
		}
		if replay, replayErr := reuseReplay(txCtx, q, p); replayErr != nil {
			return replayErr
		} else if replay {
			refs, refErr := readReuseReferences(txCtx, q, p.ActorID, p.Source, true)
			if refErr != nil {
				return refErr
			}
			if p.Target.Existing != nil {
				old := *p.Target.Existing
				identity, refErr := focusedEditRawIdentity(old, p.Proposal.Destinations[0].Graph.Context)
				if refErr != nil {
					return refErr
				}
				graph, refErr := focusedGraphFromRaw(old, identity, false)
				if refErr != nil {
					return refErr
				}
				targetRefs, refErr := readReuseTargetReferences(txCtx, q, p.ActorID, &graph, true)
				if refErr != nil {
					return refErr
				}
				refs, refErr = mergeReuseReferences(refs, targetRefs)
				if refErr != nil {
					return refErr
				}
			}
			if !focusedGraphJSONEqual(refs, p.References) {
				return errReuseChanged
			}
			finished, finishErr := finishReuse(txCtx, q, commands, p, in.expiresAt, true)
			if finishErr != nil {
				return finishErr
			}
			result = finished
			return nil
		}
		source, err := readReuseSource(txCtx, q, p.ActorID, p.Source.Selector, state, nil, true)
		if err != nil {
			return err
		}
		target, targetGraph, err := readReuseTarget(txCtx, q, p.ActorID, p.Target.Selector, state, true)
		if err != nil {
			return err
		}
		refs, err := readReuseReferences(txCtx, q, p.ActorID, source, true)
		if err != nil {
			return err
		}
		targetRefs, err := readReuseTargetReferences(txCtx, q, p.ActorID, targetGraph, true)
		if err != nil {
			return err
		}
		refs, err = mergeReuseReferences(refs, targetRefs)
		if err != nil {
			return err
		}
		if !focusedGraphJSONEqual(refs, p.References) {
			return errReuseChanged
		}
		if replay, err := reuseReplay(txCtx, q, p); err != nil {
			return err
		} else if replay {
			result.AlreadyCopied = true
		} else {
			sourceHash, err := reuseDigest(source)
			if err != nil {
				return err
			}
			reviewedSourceHash, err := reuseDigest(p.Source)
			if err != nil {
				return err
			}
			targetHash, err := reuseDigest(target)
			if err != nil {
				return err
			}
			reviewedTargetHash, err := reuseDigest(p.Target)
			if err != nil {
				return err
			}
			if sourceHash != reviewedSourceHash || targetHash != reviewedTargetHash {
				return errReuseChanged
			}
			var decision time.Time
			if err = commands.QueryRow(txCtx, `SELECT pg_catalog.clock_timestamp()`).Scan(&decision); err != nil {
				return err
			}
			rebuilt, fields := buildReuseProposal(p.ActorID, reuseRead{State: state, Source: source, Target: &target, References: refs, TargetGraph: targetGraph, DecisionAt: decision}, p.Values, &p)
			if len(fields) > 0 || !focusedGraphJSONEqual(rebuilt.Proposal, p.Proposal) {
				return errReuseChanged
			}
			if p.Proposal.NewWeek {
				if _, err = q.CreateReuseWeek(txCtx, dbgen.CreateReuseWeekParams{ID: p.Proposal.TargetPlanID, ActorID: p.ActorID, GroupID: p.Target.Selector.GroupID, SeasonID: p.Target.Selector.SeasonID, WeekStart: week, Title: p.Target.Selector.Title, Description: p.Target.Selector.Description, PlannedLoadPercentage: p.Target.Selector.Load}); err != nil {
					return err
				}
			}
			for _, destination := range p.Proposal.Destinations {
				if p.Target.Existing != nil {
					if targetGraph == nil {
						return errReuseChanged
					}
					diff, err := buildFocusedEditDiff(*targetGraph, destination.Graph, focusedGraphDraft{})
					if err != nil {
						return err
					}
					if len(diff.RemovedSegments)+len(diff.RemovedBlocks)+len(diff.RemovedSteps)+len(diff.RemovedExercises)+len(diff.RemovedWater)+len(diff.RemovedGym) != 0 {
						return errFocusedWorkoutInvalid
					}
					if err = stageFocusedEdit(txCtx, q, destination.Graph, *target.Existing, diff); err != nil {
						return err
					}
					if err = writeFocusedEditGraphUsing(txCtx, q, destination.Graph, *target.Existing, reuseWaterInsert(destination)); err != nil {
						return err
					}
				} else {
					plan, err := q.GetReusePlan(txCtx, dbgen.GetReusePlanParams{ActorID: p.ActorID, PlanID: p.Proposal.TargetPlanID})
					if err != nil {
						return err
					}
					if err = insertReuseDestination(txCtx, q, destination, plan.UpdatedAt.Time); err != nil {
						return err
					}
				}
				if _, err = readReuseDestination(txCtx, q, p.ActorID, destination); err != nil {
					return err
				}
			}
			native, routine := reuseOriginBranch(source)
			for _, event := range p.Proposal.Events {
				if _, err = q.InsertReuseCopyEvent(txCtx, dbgen.InsertReuseCopyEventParams{ID: event.ID, ActorID: p.ActorID, TargetPlanID: p.Proposal.TargetPlanID, SourcePlanID: native, RoutineID: routine, SourceKind: event.SourceKind, SourceID: event.SourceID, SourceUpdatedAt: pgtype.Timestamptz{Time: event.SourceUpdatedAt, Valid: true}, DestinationKind: event.DestinationKind, DestinationID: event.DestinationID}); err != nil {
					return err
				}
			}
			ownIDs := []uuid.UUID{}
			if p.Target.Existing == nil {
				for _, d := range p.Proposal.Destinations {
					ownIDs = append(ownIDs, d.Graph.SessionID)
				}
			}
			finalSource, err := readReuseSource(txCtx, q, p.ActorID, p.Source.Selector, state, ownIDs, false)
			if err != nil {
				return err
			}
			finalSource = reuseSourceAfterOwnWrites(finalSource, p.Source, p.Proposal.TargetPlanID)
			if !focusedGraphJSONEqual(finalSource, p.Source) {
				return errReuseChanged
			}
			finalRefs, err := readReuseReferences(txCtx, q, p.ActorID, source, false)
			if err != nil {
				return err
			}
			oldTargetRefs, err := readReuseTargetReferences(txCtx, q, p.ActorID, targetGraph, false)
			if err != nil {
				return err
			}
			finalRefs, err = mergeReuseReferences(finalRefs, oldTargetRefs)
			if err != nil {
				return err
			}
			if !focusedGraphJSONEqual(finalRefs, p.References) {
				return errReuseChanged
			}
		}
		finished, finishErr := finishReuse(txCtx, q, commands, p, in.expiresAt, result.AlreadyCopied)
		if finishErr != nil {
			return finishErr
		}
		result = finished
		return nil
	})
	result.CleanupFailed = outcome.CleanupFailed
	if err = reuseSaveError(outcome, err); err != nil {
		return reuseSaveResult{}, err
	}
	return result, nil
}

// Shared completion also covers an already-completed read-only repeat. A repeat
// never needs to enumerate a source set enlarged by our own committed copies.
func finishReuse(ctx context.Context, q *dbgen.Queries, commands dbgen.DBTX, p reuseReviewPayload, expires time.Time, already bool) (reuseSaveResult, error) {
	result := reuseSaveResult{AlreadyCopied: already}
	var err error
	for _, d := range p.Proposal.Destinations {
		graph := d.Graph
		graph.References = p.References
		artifact, err := focusedGraphArtifact(graph)
		if err != nil {
			return reuseSaveResult{}, err
		}
		size, err := q.FocusedWaterArtifactSize(ctx, dbgen.FocusedWaterArtifactSizeParams{ActorID: p.ActorID, PlanID: p.Proposal.TargetPlanID, Snapshot: artifact})
		if err != nil {
			return reuseSaveResult{}, err
		}
		if size > focusedWorkoutMaxArtifactBytes {
			return reuseSaveResult{}, errFocusedWorkoutLimit
		}
	}
	if _, err = reuseBoundedJSON(p); err != nil {
		return reuseSaveResult{}, err
	}
	if p.Target.Existing != nil && !result.AlreadyCopied {
		if err = focusedEditFinalEligibility(ctx, q, FocusedWorkoutEditRequest{p.ActorID, p.Proposal.TargetPlanID, p.Target.Existing.Session.ID}, p.Target.Existing.Session, expires); err != nil {
			return reuseSaveResult{}, err
		}
	} else {
		var now time.Time
		if err = commands.QueryRow(ctx, `SELECT pg_catalog.clock_timestamp()`).Scan(&now); err != nil {
			return reuseSaveResult{}, err
		}
		if !now.Before(expires) {
			return reuseSaveResult{}, errFocusedWorkoutInvalid
		}
	}
	result.PlanID, result.GroupID = p.Proposal.TargetPlanID, p.Target.Selector.GroupID
	result.Count = len(p.Proposal.Destinations)
	if len(p.Proposal.Destinations) > 0 {
		first := p.Proposal.Destinations[0].Graph
		result.SessionID, result.Day = first.SessionID, first.Schedule.Date
	}
	return result, nil
}
