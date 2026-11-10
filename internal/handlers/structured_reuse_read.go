package handlers

import (
	"context"
	"fmt"
	"github.com/cfcoimbra/mycfc/internal/db"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"sort"
)

type PostgresStructuredReuseStore struct{ Pool *pgxpool.Pool }

func reuseSessionRow(row dbgen.ListReuseSourceSessionsRow) dbgen.GetFocusedWorkoutSessionRow {
	return dbgen.GetFocusedWorkoutSessionRow{ID: row.ID, PlanID: row.PlanID, Title: row.Title, Description: row.Description, StartsAt: row.StartsAt, EndsAt: row.EndsAt, ModalityID: row.ModalityID, EntryKind: row.EntryKind, Status: row.Status, CancelledAt: row.CancelledAt, CancelledByID: row.CancelledByID, CancellationReason: row.CancellationReason, CreatedByID: row.CreatedByID, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, SchedulePrecision: row.SchedulePrecision, ScheduledOn: row.ScheduledOn, DayBand: row.DayBand, HasOutcomes: false}
}
func reusePlanContext(actor uuid.UUID, row dbgen.GetReusePlanRow, state db.TrainingScheduleState, day string) FocusedWorkoutContext {
	c := FocusedWorkoutContext{ActorID: actor, PlanID: row.ID, Day: day, GroupName: row.GroupName, PlanTitle: row.Title, WeekStart: row.WeekStart.Time, PlanUpdatedAt: row.UpdatedAt.Time.UTC(), DatabaseIncarnation: state.DatabaseIncarnation, StateEpoch: state.StateEpoch, SchemaContract: state.SchemaContract, AuthoringEnabled: state.PartialV2Enabled}
	if row.TrainingGroupID != nil {
		c.GroupID = *row.TrainingGroupID
	}
	if row.SeasonID != nil {
		c.SeasonID = *row.SeasonID
	}
	return c
}
func readReuseSource(ctx context.Context, q *dbgen.Queries, actor uuid.UUID, selector reuseSourceSelector, state db.TrainingScheduleState, ownIDs []uuid.UUID, locked bool) (reuseSourceMaterial, error) {
	source := reuseSourceMaterial{Selector: selector, Items: []reuseSourceItem{}}
	if err := selector.validate(); err != nil {
		return source, err
	}
	if selector.Kind == "ROUTINE" {
		var routine dbgen.GetReuseRoutineRow
		var err error
		if locked {
			row, e := q.LockReuseRoutine(ctx, dbgen.LockReuseRoutineParams{ActorID: actor, RoutineID: selector.RoutineID})
			err = e
			routine = dbgen.GetReuseRoutineRow(row)
		} else {
			routine, err = q.GetReuseRoutine(ctx, dbgen.GetReuseRoutineParams{ActorID: actor, RoutineID: selector.RoutineID})
		}
		if err != nil {
			return source, publicationNowaitError(err)
		}
		item, err := decodeReuseRoutine(routine)
		if err != nil {
			return source, err
		}
		source.Routine = &routine
		source.Items = append(source.Items, item)
		_, err = reuseBoundedJSON(source)
		return source, err
	}
	plan, err := q.GetReusePlan(ctx, dbgen.GetReusePlanParams{ActorID: actor, PlanID: selector.PlanID})
	if err != nil {
		return source, err
	}
	source.Plan = &plan
	rows := []dbgen.GetFocusedWorkoutSessionRow{}
	if selector.Kind == "SESSION" {
		row, err := q.GetFocusedWorkoutSession(ctx, dbgen.GetFocusedWorkoutSessionParams{ActorID: actor, PlanID: selector.PlanID, SessionID: selector.SessionID})
		if err != nil {
			return source, err
		}
		if row.Status != "ACTIVE" {
			return source, nil
		}
		rows = append(rows, row)
	} else {
		day := pgtype.Date{}
		if selector.Kind == "DAY" {
			day, err = focusedDay(selector.Day)
			if err != nil {
				return source, err
			}
		}
		headers, err := q.ListReuseSourceSessions(ctx, dbgen.ListReuseSourceSessionsParams{ActorID: actor, PlanID: selector.PlanID, SourceDay: day, OwnDestinationSessionIds: ownIDs})
		if err != nil {
			return source, err
		}
		if len(headers) > reuseMaxSessions {
			return source, errFocusedWorkoutLimit
		}
		for _, row := range headers {
			rows = append(rows, reuseSessionRow(row))
		}
	}
	for i, row := range rows {
		if locked {
			lockedRow, err := q.LockFocusedWorkoutForEdit(ctx, dbgen.LockFocusedWorkoutForEditParams{ActorID: actor, PlanID: plan.ID, SessionID: row.ID})
			if err != nil {
				return source, err
			}
			row = focusedEditLockedRow(lockedRow)
			if row.Status != "ACTIVE" {
				return source, errReuseChanged
			}
		}
		schedule, err := trainingScheduleFromRecord(row.SchedulePrecision, row.ScheduledOn, row.StartsAt, row.EndsAt, row.DayBand)
		if err != nil {
			return source, err
		}
		row.HasOutcomes = false
		c := reusePlanContext(actor, plan, state, schedule.Date)
		native := focusedEditRaw{Session: row}
		request := FocusedWorkoutEditRequest{actor, plan.ID, row.ID}
		if err = readFocusedEditContent(ctx, q, request, &native, locked); err != nil {
			return source, err
		}
		identity, err := focusedEditRawIdentity(native, c)
		if err != nil {
			return source, err
		}
		graph, err := focusedGraphFromRaw(native, identity, false)
		if err != nil {
			return source, err
		}
		source.Items = append(source.Items, reuseSourceItem{Key: reuseMappingKey(i), Graph: graph, Raw: &native, Origins: reuseNativeOrigins(graph)})
		if _, err = reuseBoundedJSON(source); err != nil {
			return source, err
		}
	}
	return source, nil
}
func reuseSourceNativeBlocks(source reuseSourceMaterial) []uuid.UUID {
	ids := []uuid.UUID{}
	for _, item := range source.Items {
		for _, a := range item.Graph.Activities {
			for _, b := range a.Phases {
				if b.Water != nil {
					ids = append(ids, b.ID)
				}
			}
		}
	}
	return focusedEditSortedIDs(ids)
}
func readReuseReferences(ctx context.Context, q *dbgen.Queries, actor uuid.UUID, source reuseSourceMaterial, locked bool) (focusedGraphProfileFacts, error) {
	facts := focusedGraphProfileFacts{Profiles: []dbgen.WaterIntensityProfile{}, Zones: []dbgen.WaterIntensityZone{}}
	graphs := make([]focusedWorkoutGraph, 0, len(source.Items))
	for _, item := range source.Items {
		graphs = append(graphs, item.Graph)
	}
	expected, err := reuseExpectedProfileIDs(graphs)
	if err != nil {
		return facts, err
	}
	var nativePlan, routine *uuid.UUID
	blocks := []uuid.UUID{}
	if source.Selector.Kind == "ROUTINE" {
		routine = focusedPointer(source.Selector.RoutineID)
	} else {
		nativePlan = focusedPointer(source.Selector.PlanID)
		blocks = reuseSourceNativeBlocks(source)
		if len(blocks) == 0 {
			return facts, validateReuseReferenceClosure(expected, facts)
		}
	}
	if locked {
		facts.Profiles, err = q.LockReusePinnedProfiles(ctx, dbgen.LockReusePinnedProfilesParams{ActorID: actor, NativeSourcePlanID: nativePlan, NativeSourceBlockIds: blocks, RoutineID: routine})
	} else {
		facts.Profiles, err = q.ListReusePinnedProfiles(ctx, dbgen.ListReusePinnedProfilesParams{ActorID: actor, NativeSourcePlanID: nativePlan, NativeSourceBlockIds: blocks, RoutineID: routine})
	}
	if err != nil {
		return facts, publicationNowaitError(err)
	}
	// SQL deliberately returns an empty set on incomplete/over-budget origin
	// closure. That is a refusal, not evidence that the graph has no pins.
	if err = validateReuseReferenceClosure(expected, facts); err != nil {
		return facts, err
	}
	facts.Zones, err = q.ListReusePinnedZones(ctx, dbgen.ListReusePinnedZonesParams{ActorID: actor, NativeSourcePlanID: nativePlan, NativeSourceBlockIds: blocks, RoutineID: routine})
	if err != nil {
		return facts, err
	}
	if err = validateReuseReferenceClosure(expected, facts); err != nil {
		return facts, err
	}
	normalizeReuseReferences(&facts)
	return facts, nil
}

func reuseExpectedProfileIDs(graphs []focusedWorkoutGraph) ([]uuid.UUID, error) {
	seen := map[uuid.UUID]bool{}
	for _, graph := range graphs {
		for _, id := range focusedGraphSelectedProfileIDs(graph) {
			if id == uuid.Nil {
				return nil, errFocusedWorkoutInvalid
			}
			seen[id] = true
			if len(seen) > 32 {
				return nil, errFocusedWorkoutLimit
			}
		}
	}
	ids := make([]uuid.UUID, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	return focusedEditSortedIDs(ids), nil
}

func validateReuseReferenceClosure(expected []uuid.UUID, facts focusedGraphProfileFacts) error {
	if len(expected) > 32 || len(facts.Profiles) > 32 || len(facts.Zones) > 256 {
		return errFocusedWorkoutLimit
	}
	if len(facts.Profiles) != len(expected) {
		return errReuseChanged
	}
	missing := map[uuid.UUID]bool{}
	for _, id := range expected {
		if id == uuid.Nil || missing[id] {
			return errFocusedWorkoutInvalid
		}
		missing[id] = true
	}
	profiles := map[uuid.UUID]bool{}
	for _, p := range facts.Profiles {
		if !missing[p.ID] || p.Revision < 1 {
			return errReuseChanged
		}
		delete(missing, p.ID)
		profiles[p.ID] = true
	}
	zones, codes, positions := map[uuid.UUID]bool{}, map[string]bool{}, map[string]bool{}
	for _, z := range facts.Zones {
		code, position := z.ProfileID.String()+":"+z.Code, z.ProfileID.String()+":"+fmt.Sprint(z.Position)
		if !profiles[z.ProfileID] || z.ID == uuid.Nil || zones[z.ID] || codes[code] || positions[position] || z.Position < 1 || z.Code == "" {
			return errReuseChanged
		}
		zones[z.ID], codes[code], positions[position] = true, true, true
	}
	return nil
}
func normalizeReuseReferences(facts *focusedGraphProfileFacts) {
	for i := range facts.Profiles {
		facts.Profiles[i].CreatedAt.Time = facts.Profiles[i].CreatedAt.Time.UTC()
	}
	sort.Slice(facts.Profiles, func(i, j int) bool { return facts.Profiles[i].ID.String() < facts.Profiles[j].ID.String() })
	sort.Slice(facts.Zones, func(i, j int) bool {
		if facts.Zones[i].ProfileID != facts.Zones[j].ProfileID {
			return facts.Zones[i].ProfileID.String() < facts.Zones[j].ProfileID.String()
		}
		if facts.Zones[i].Code != facts.Zones[j].Code {
			return facts.Zones[i].Code < facts.Zones[j].Code
		}
		return facts.Zones[i].ID.String() < facts.Zones[j].ID.String()
	})
}
func mergeReuseReferences(a, b focusedGraphProfileFacts) (focusedGraphProfileFacts, error) {
	out := focusedGraphProfileFacts{Profiles: []dbgen.WaterIntensityProfile{}, Zones: []dbgen.WaterIntensityZone{}}
	profiles := map[uuid.UUID]dbgen.WaterIntensityProfile{}
	zones := map[uuid.UUID]dbgen.WaterIntensityZone{}
	for _, set := range []focusedGraphProfileFacts{a, b} {
		for _, p := range set.Profiles {
			if old, ok := profiles[p.ID]; ok && !focusedGraphJSONEqual(old, p) {
				return out, errReuseChanged
			}
			profiles[p.ID] = p
		}
		for _, z := range set.Zones {
			if old, ok := zones[z.ID]; ok && !focusedGraphJSONEqual(old, z) {
				return out, errReuseChanged
			}
			zones[z.ID] = z
		}
	}
	if len(profiles) > 32 || len(zones) > 256 {
		return out, errFocusedWorkoutLimit
	}
	for _, p := range profiles {
		out.Profiles = append(out.Profiles, p)
	}
	for _, z := range zones {
		out.Zones = append(out.Zones, z)
	}
	normalizeReuseReferences(&out)
	return out, nil
}
