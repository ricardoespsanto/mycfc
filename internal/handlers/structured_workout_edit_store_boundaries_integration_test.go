//go:build integration

package handlers

import (
	"context"
	"errors"
	"strings"
	"testing"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type workoutEditStoreFixture struct {
	*publicationDomainFixture
	store   PostgresFocusedWorkoutStore
	request FocusedWorkoutEditRequest
	profile uuid.UUID
}

func newWorkoutEditStoreFixture(t *testing.T) workoutEditStoreFixture {
	t.Helper()
	f := newPublicationDomainFixture(t)
	enableGraphFixture(t, f)
	ctx := t.Context()
	q := dbgen.New(f.low)
	profile, err := q.CreateWaterIntensityProfile(ctx, dbgen.CreateWaterIntensityProfileParams{Name: "Ritmo fixado para edição", Craft: "KAYAK", CreatedByID: f.actor})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = q.CreateWaterIntensityZone(ctx, dbgen.CreateWaterIntensityZoneParams{ProfileID: profile.ID, Code: "R2", Label: "Zona de referência", Meaning: "Synthetic edit guidance"}); err != nil {
		t.Fatal(err)
	}
	_, raw := focusedGraphWaterExample()
	raw.Set("activities[a_1].phases[p_1].water.profile_id", profile.ID.String())
	raw.Add("activities[a_1].phases[p_1].water.node_order", "n_3")
	raw.Set("activities[a_1].phases[p_1].water.nodes[n_3].kind", "EFFORT")
	raw.Set("activities[a_1].phases[p_1].water.nodes[n_3].name", "Técnica final")
	raw.Set("activities[a_1].phases[p_1].water.nodes[n_3].instructions", "Remar suavemente mantendo a técnica.")
	store := PostgresFocusedWorkoutStore{Pool: f.web}
	created, review := graphRealReview(t, f, store, raw)
	if _, err = store.SaveFocusedGraph(ctx, review); err != nil {
		t.Fatal(err)
	}
	return workoutEditStoreFixture{f, store, FocusedWorkoutEditRequest{f.actor, f.plan, created.Proposal.SessionID}, profile.ID}
}

func workoutEditInterruptedStore(t *testing.T, f workoutEditStoreFixture, marker string, occurrence int32) (PostgresFocusedWorkoutStore, *reuseInterruptedQuery) {
	t.Helper()
	traced, fault := interruptedReuseStore(t, f.publicationDomainFixture, marker, occurrence)
	return PostgresFocusedWorkoutStore{Pool: traced.Pool}, fault
}

func TestWorkoutEditStoreInterruptedSnapshotDoesNotReturnAUsableSource(t *testing.T) {
	f := newWorkoutEditStoreFixture(t)
	for _, tc := range []struct {
		name, marker string
		preview      bool
		runtime      bool
	}{
		{"session transaction begin", "begin isolation level repeatable read read only", false, false},
		{"session runtime contract", "/* workout graph profile version witness */", false, true},
		{"session header", "-- name: GetFocusedWorkoutSession ", false, false},
		{"session context", "-- name: GetFocusedWaterCreateContext ", false, false},
		{"session decision time", "SELECT pg_catalog.clock_timestamp()", false, false},
		{"session transaction commit", "commit", false, false},
		{"preview transaction begin", "begin isolation level repeatable read read only", true, false},
		{"preview runtime contract", "/* workout graph profile version witness */", true, true},
		{"preview header", "-- name: GetFocusedWorkoutSession ", true, false},
		{"preview activities", "-- name: ListFocusedWorkoutSegments ", true, false},
		{"preview phases", "-- name: ListFocusedWorkoutBlocks ", true, false},
		{"preview water prescriptions", "-- name: ListFocusedWorkoutWaterPrescriptions ", true, false},
		{"preview water steps", "-- name: ListFocusedWorkoutWaterSteps ", true, false},
		{"preview gym prescriptions", "-- name: ListFocusedWorkoutGymPrescriptions ", true, false},
		{"preview gym exercises", "-- name: ListFocusedWorkoutGymExercises ", true, false},
		{"preview decision time", "SELECT pg_catalog.clock_timestamp()", true, false},
		{"preview context", "-- name: GetFocusedWaterCreateContext ", true, false},
		{"preview rhythm zones", "-- name: ListFocusedWorkoutEditZones ", true, false},
		{"preview transaction commit", "commit", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, fault := workoutEditInterruptedStore(t, f, tc.marker, 1)
			load := store.LoadFocusedEditSession
			if tc.preview {
				load = store.LoadFocusedEdit
			}
			_, err := load(t.Context(), f.request)
			matched := errors.Is(err, context.Canceled)
			if tc.runtime {
				matched = err != nil && strings.Contains(err.Error(), "workout profile version witness contract mismatch")
			}
			if fault.seen.Load() < 1 || !matched {
				t.Fatalf("interruption hidden: query matches=%d error=%v", fault.seen.Load(), err)
			}
		})
	}
	source, err := f.store.LoadFocusedEdit(t.Context(), f.request)
	if err != nil || source.Raw.Session.ID != f.request.SessionID || len(source.Graph.References.Profiles) != 1 {
		t.Fatalf("healthy snapshot lost selected workout or rhythm: %v", err)
	}
}

func TestWorkoutEditStoreScopesSelectedRhythmAndWorkout(t *testing.T) {
	f := newWorkoutEditStoreFixture(t)
	ctx := t.Context()
	in := f.request
	in.ActorID = f.subject
	for _, load := range []func(context.Context, FocusedWorkoutEditRequest) (focusedEditSource, error){f.store.LoadFocusedEditSession, f.store.LoadFocusedEdit} {
		source, err := load(ctx, in)
		if !errors.Is(err, pgx.ErrNoRows) || source.Raw.Session.ID != uuid.Nil || source.Graph.SessionID != uuid.Nil {
			t.Fatalf("non-coach received scoped workout data: %v", err)
		}
	}
	for _, tc := range []struct {
		name     string
		selected []uuid.UUID
		want     error
	}{
		{"duplicate profile", []uuid.UUID{f.profile, f.profile}, errFocusedWorkoutInvalid},
		{"missing profile", []uuid.UUID{uuid.New()}, errFocusedWorkoutConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, err := f.store.ReadFocusedEditPreview(t.Context(), f.request, tc.selected)
			if !errors.Is(err, tc.want) || len(source.Graph.References.Profiles) != 0 || len(source.Graph.References.Zones) != 0 {
				t.Fatalf("invalid rhythm closure error=%v, want %v", err, tc.want)
			}
		})
	}
}
