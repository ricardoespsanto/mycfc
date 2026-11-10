//go:build integration

package handlers

import (
	"context"
	"errors"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestExactTrainingScheduleStorePreservesPartialSiblingAndScope(t *testing.T) {
	f := newPublicationDomainFixture(t)
	setPublicationFixtureEnabled(t, f, true)
	mixed := addPublicationMixedSchedules(t, f)
	setPublicationFixtureEnabled(t, f, false)
	q := dbgen.New(f.web)
	store := PostgresTrainingScheduleStore{Querier: q, Pool: f.web}
	ctx := context.WithValue(t.Context(), currentUserKey{}, CurrentUser{ID: f.actor})
	current, err := q.GetTrainingSessionForEdit(ctx, f.session)
	if err != nil {
		t.Fatal(err)
	}
	now := pgtype.Timestamptz{Time: time.Now(), Valid: true}
	create := dbgen.CreateTrainingSessionParams{PlanID: f.plan, CreatedByID: f.actor, Title: "Scoped exact sibling", StartsAt: current.StartsAt, EndsAt: current.EndsAt}
	invalid := create
	invalid.EndsAt = invalid.StartsAt
	if _, err := store.CreateTrainingSession(ctx, invalid); !errors.Is(err, ErrTrainingScheduleInvalid) {
		t.Fatal("nonpositive interval was accepted", err)
	}
	invalid = create
	invalid.CreatedByID = uuid.New()
	if _, err := store.CreateTrainingSession(ctx, invalid); !errors.Is(err, errTrainingScheduleUnavailable) {
		t.Fatal("a nonexistent actor created a session", err)
	}
	invalid = create
	invalid.CreatedByID = f.subject
	if _, err := store.CreateTrainingSession(ctx, invalid); !errors.Is(err, errTrainingScheduleUnavailable) {
		t.Fatal("actor without coaching scope created a session", err)
	}
	created, err := store.CreateTrainingSession(ctx, create)
	if err != nil || created.ID == uuid.Nil || created.SchedulePrecision != "INTERVAL" {
		t.Fatalf("exact session creation failed: %v", err)
	}
	input := dbgen.UpdateTrainingSessionParams{ID: created.ID, PlanID: f.plan, Title: "Explicit exact session edit", StartsAt: created.StartsAt, EndsAt: created.EndsAt, ExpectedUpdatedAt: created.UpdatedAt, AsOf: now}
	bad := input
	bad.EndsAt.Valid = false
	if _, err := store.UpdateTrainingSession(ctx, bad); !errors.Is(err, ErrTrainingScheduleInvalid) {
		t.Fatal("missing end passed exact editor", err)
	}
	bad = input
	bad.ID = uuid.New()
	if _, err := store.UpdateTrainingSession(ctx, bad); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("missing session did not preserve absent-row semantics", err)
	}
	bad = input
	bad.PlanID = uuid.New()
	if _, err := store.UpdateTrainingSession(ctx, bad); !errors.Is(err, errTrainingScheduleUnavailable) {
		t.Fatal("missing destination plan was accepted", err)
	}
	if _, err := store.UpdateTrainingSession(t.Context(), input); !errors.Is(err, errTrainingScheduleUnavailable) {
		t.Fatal("update trusted an absent actor", err)
	}
	bad = input
	bad.ID = mixed[1]
	if _, err := store.UpdateTrainingSession(ctx, bad); !errors.Is(err, ErrTrainingScheduleWriteUnsupported) {
		t.Fatal("exact editor could overwrite a partial session", err)
	}
	updated, err := store.UpdateTrainingSession(ctx, input)
	if err != nil || updated.ID != created.ID || updated.Title != input.Title || updated.PlanID != f.plan {
		t.Fatalf("exact session update failed: %v", err)
	}
	if _, err := store.UpdateTrainingSession(ctx, input); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("stale session update did not fail", err)
	}
	cancel := dbgen.CancelTrainingSessionParams{ID: created.ID, CancelledAt: now, CancelledByID: &f.actor, CancellationReason: focusedPointer("Treino cancelado"), ExpectedUpdatedAt: updated.UpdatedAt}
	cancelled, err := store.CancelTrainingSession(ctx, cancel)
	if err != nil || cancelled.Status != "CANCELLED" || cancelled.CancelledByID == nil || *cancelled.CancelledByID != f.actor {
		t.Fatalf("scoped cancellation failed: %v", err)
	}
	if _, err := store.CancelTrainingSession(ctx, cancel); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("repeated stale cancellation was applied", err)
	}
	partial, err := q.GetTrainingSessionForEdit(ctx, mixed[1])
	if err != nil || partial.SchedulePrecision != "START_ONLY" || partial.EndsAt.Valid || partial.Status != "ACTIVE" {
		t.Fatalf("exact writes altered the partial sibling: %v", err)
	}
}

func TestStructuredScheduleStoreRequiresCurrentScopeAndCompleteWeek(t *testing.T) {
	f := newPublicationDomainFixture(t)
	ctx := t.Context()
	store := PostgresStructuredTrainingStore{Pool: f.web}
	status := dbgen.TrainingSchedulePlanWriteStatusParams{ActorID: f.actor, PlanID: f.plan}
	observed, err := store.TrainingSchedulePlanWriteStatus(ctx, status)
	if err != nil || observed.HasPartial || observed.HasV2Publication {
		t.Fatalf("fresh exact week status is wrong: %+v %v", observed, err)
	}
	load := dbgen.UpdateStructuredTrainingWeekLoadParams{PlanID: f.plan, UserID: f.actor, IsAdmin: true, PlannedLoadPercentage: focusedPointer(int16(42))}
	n, err := store.UpdateStructuredTrainingWeekLoad(ctx, load)
	if err != nil || n != 1 {
		t.Fatalf("authorized exact plan load update affected %d rows: %v", n, err)
	}
	bad := load
	bad.UserID = uuid.New()
	if _, err := store.UpdateStructuredTrainingWeekLoad(ctx, bad); !errors.Is(err, errTrainingScheduleUnavailable) {
		t.Fatal("a caller admin flag replaced current actor authority", err)
	}
	bad.UserID = f.subject
	if _, err := store.UpdateStructuredTrainingWeekLoad(ctx, bad); !errors.Is(err, errTrainingScheduleUnavailable) {
		t.Fatal("unscoped week mutation did not fail closed", err)
	}
	bad = load
	bad.PlannedLoadPercentage = focusedPointer(int16(-1))
	if _, err := store.UpdateStructuredTrainingWeekLoad(ctx, bad); err == nil {
		t.Fatal("invalid load committed")
	}
	setPublicationFixtureEnabled(t, f, true)
	addPublicationMixedSchedules(t, f)
	setPublicationFixtureEnabled(t, f, false)
	observed, err = store.TrainingSchedulePlanWriteStatus(ctx, status)
	if err != nil || !observed.HasPartial {
		t.Fatalf("the full plan status hid its partial sessions: %+v %v", observed, err)
	}
	if _, err := store.UpdateStructuredTrainingWeekLoad(ctx, load); !errors.Is(err, ErrTrainingScheduleWriteUnsupported) {
		t.Fatal("whole plan write ignored partial content", err)
	}
	var saved int16
	if err := f.web.QueryRow(ctx, "SELECT planned_load_percentage FROM training_plans WHERE id=$1", f.plan).Scan(&saved); err != nil || saved != 42 {
		t.Fatalf("refused mutations changed the saved load: %d %v", saved, err)
	}
}

func TestStructuredScheduleRoutineRequiresCurrentStoredVersion(t *testing.T) {
	f := newPublicationDomainFixture(t)
	ctx := t.Context()
	store := PostgresStructuredTrainingStore{Pool: f.web}
	id := uuid.New()
	f.exec(t, `INSERT INTO training_routines(id,name,kind,owner_user_id,source_id,source_updated_at,snapshot)
		SELECT $1,'Scoped routine','SESSION',$2,id,updated_at,
		jsonb_build_object('title','Retained routine title','entry_kind','TRAINING','duration_seconds',2700,'segments','[]'::jsonb)
		FROM training_sessions WHERE id=$3`, id, f.actor, f.session)
	q := dbgen.New(f.web)
	session, err := q.GetTrainingSessionForEdit(ctx, f.session)
	if err != nil {
		t.Fatal(err)
	}
	routine, err := q.GetVisibleTrainingRoutine(ctx, dbgen.GetVisibleTrainingRoutineParams{RoutineID: id, UserID: f.actor})
	if err != nil {
		t.Fatal(err)
	}
	input := StructuredRoutineInsertInput{ActorID: f.actor, TargetID: f.plan, Routine: routine, StartsAt: session.StartsAt}
	foreign := input
	foreign.ActorID = f.subject
	if _, err := store.InsertTrainingRoutine(ctx, foreign); !errors.Is(err, errTrainingScheduleUnavailable) {
		t.Fatal("caller-provided routine was used outside its visibility", err)
	}
	f.exec(t, `UPDATE training_routines SET snapshot=jsonb_set(snapshot,'{title}','"Current routine title"'),updated_at=updated_at+interval '1 second' WHERE id=$1`, id)
	if _, err := store.InsertTrainingRoutine(ctx, input); !errors.Is(err, errStructuredTrainingPublicationConflict) {
		t.Fatal("stale routine version was inserted", err)
	}
	input.Routine, err = q.GetVisibleTrainingRoutine(ctx, dbgen.GetVisibleTrainingRoutineParams{RoutineID: id, UserID: f.actor})
	if err != nil {
		t.Fatal(err)
	}
	// Retaining the current timestamp cannot replace the current source bytes.
	input.Routine.Snapshot = routine.Snapshot
	destination, err := store.InsertTrainingRoutine(ctx, input)
	if err != nil || destination == uuid.Nil {
		t.Fatal("current routine could not be inserted", err)
	}
	got, err := q.GetTrainingSessionForEdit(ctx, destination)
	if err != nil || got.Title != "Current routine title" || got.EndsAt.Time.Sub(got.StartsAt.Time) != 45*time.Minute {
		t.Fatalf("insert used stale caller bytes: %+v %v", got, err)
	}
	var copies int
	if err := f.web.QueryRow(ctx, `SELECT count(*) FROM training_copy_events WHERE source_id=$1`, id).Scan(&copies); err != nil || copies != 1 {
		t.Fatalf("refused attempts wrote copy history: %d %v", copies, err)
	}
}
