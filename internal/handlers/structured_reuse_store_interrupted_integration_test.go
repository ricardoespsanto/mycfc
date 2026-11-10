//go:build integration

package handlers

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type reuseInterruptedQuery struct {
	marker     string
	occurrence int32
	seen       atomic.Int32
}

func (f *reuseInterruptedQuery) TraceQueryStart(ctx context.Context, _ *pgx.Conn, query pgx.TraceQueryStartData) context.Context {
	matches := query.SQL == f.marker || (f.marker != "commit" && strings.Contains(query.SQL, f.marker))
	if matches && f.seen.Add(1) == f.occurrence {
		failed, cancel := context.WithCancel(ctx)
		cancel()
		return failed
	}
	return ctx
}

func (*reuseInterruptedQuery) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func interruptedReuseStore(t *testing.T, f *publicationDomainFixture, marker string, occurrence int32) (PostgresStructuredReuseStore, *reuseInterruptedQuery) {
	t.Helper()
	fault := &reuseInterruptedQuery{marker: marker, occurrence: occurrence}
	cfg := f.web.Config()
	cfg.ConnConfig.Tracer = fault
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return PostgresStructuredReuseStore{Pool: pool}, fault
}

type interruptedReuseFixture struct {
	*publicationDomainFixture
	store         PostgresStructuredReuseStore
	nativeSource  reuseSourceSelector
	routineSource reuseSourceSelector
	weekTarget    reuseTargetSelector
	workoutTarget reuseTargetSelector
}

func newInterruptedReuseFixture(t *testing.T) interruptedReuseFixture {
	t.Helper()
	f := newPublicationDomainFixture(t)
	enableGraphFixture(t, f)
	ctx := t.Context()
	q := dbgen.New(f.low)
	profile, err := q.CreateWaterIntensityProfile(ctx, dbgen.CreateWaterIntensityProfileParams{Name: "Ritmo para teste de interrupção", Craft: "KAYAK", CreatedByID: f.actor})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = q.CreateWaterIntensityZone(ctx, dbgen.CreateWaterIntensityZoneParams{ProfileID: profile.ID, Code: "R2", Label: "Zona de referência", Meaning: "Guidance for the interruption fixture"}); err != nil {
		t.Fatal(err)
	}
	_, raw := focusedGraphWaterExample()
	raw.Set("activities[a_1].phases[p_1].water.profile_id", profile.ID.String())
	workout := PostgresFocusedWorkoutStore{Pool: f.web}
	created, review := graphRealReview(t, f, workout, raw)
	if _, err = workout.SaveFocusedGraph(ctx, review); err != nil {
		t.Fatal(err)
	}
	routine := uuid.New()
	f.exec(t, `INSERT INTO training_routines(id,name,kind,owner_user_id,modality,source_id,source_updated_at,snapshot)
		VALUES($1,'Rotina de recuperação','SEGMENT',$2,'GYM',$3,now(),
		'{"modality":"GYM","title":"Recuperação","blocks":[{"purpose":"COOL_DOWN","instructions":"Recuperação técnica"}]}')`, routine, f.actor, f.session)
	week := created.Proposal.Context.WeekStart
	return interruptedReuseFixture{
		publicationDomainFixture: f,
		store:                    PostgresStructuredReuseStore{Pool: f.web},
		nativeSource:             reuseSourceSelector{Kind: "SESSION", PlanID: f.plan, SessionID: created.Proposal.SessionID},
		routineSource:            reuseSourceSelector{Kind: "ROUTINE", RoutineID: routine},
		weekTarget:               reuseTargetSelector{GroupID: f.group, SeasonID: f.season, WeekStart: week.AddDate(0, 0, 7).Format(time.DateOnly), Title: "Semana de destino"},
		workoutTarget:            reuseTargetSelector{GroupID: f.group, SeasonID: f.season, WeekStart: week.Format(time.DateOnly), SessionID: created.Proposal.SessionID},
	}
}

func TestReuseStoreInterruptedPreviewCannotAuthorizeCopy(t *testing.T) {
	f := newInterruptedReuseFixture(t)
	for _, tc := range []struct {
		name, marker string
		want         error
	}{
		{"begin read snapshot", "begin isolation level repeatable read read only", context.Canceled},
		{"inspect existing target week", "-- name: GetReusePlan ", context.Canceled},
		{"read existing target graph", "-- name: ListFocusedWorkoutSegments ", context.Canceled},
		{"read existing target rhythm", "-- name: ListFocusedWorkoutEditProfiles ", context.Canceled},
		{"read decision time", "SELECT pg_catalog.clock_timestamp()", context.Canceled},
		{"commit preview snapshot", "commit", context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, fault := interruptedReuseStore(t, f.publicationDomainFixture, tc.marker, 1)
			_, err := store.ReadReuse(t.Context(), f.actor, f.routineSource, &f.workoutTarget)
			if fault.seen.Load() < 1 || !errors.Is(err, tc.want) {
				t.Fatalf("preview succeeded or hid interrupted %s: observed=%d error=%v", tc.name, fault.seen.Load(), err)
			}
		})
	}
	// A fresh snapshot after all interruptions is still usable.
	_, review := reuseRealReview(t, f.store, f.actor, f.routineSource, f.workoutTarget)
	if saved, err := f.store.ConfirmReuse(t.Context(), review); err != nil || saved.SessionID != f.workoutTarget.SessionID || saved.Count != 1 {
		t.Fatalf("healthy preview could not be confirmed: %+v, %v", saved, err)
	}
}

func TestReuseStoreInterruptedWeekCopyLeavesNoDraftOrHistory(t *testing.T) {
	f := newInterruptedReuseFixture(t)
	payload, review := reuseRealReview(t, f.store, f.actor, f.nativeSource, f.weekTarget)
	for _, tc := range []struct {
		name, marker string
		occurrence   int32
	}{
		{"probe reviewed destination", "-- name: GetReusePlan ", 1},
		{"lock source week", "-- name: LockFocusedWaterCreatePlan ", 1},
		{"inspect existing history", "-- name: GetReuseCopyEventsByIDs ", 1},
		{"read locked source workout", "-- name: LockFocusedWorkoutForEdit ", 1},
		{"read locked source rhythm", "-- name: LockReusePinnedProfiles ", 1},
		{"recheck history before writing", "-- name: GetReuseCopyEventsByIDs ", 2},
		{"read confirmation time", "SELECT pg_catalog.clock_timestamp()", 1},
		{"create reviewed week", "-- name: CreateReuseWeek ", 1},
		{"load created week for workout insert", "-- name: GetReusePlan ", 3},
		{"insert copied workout", "-- name: InsertCopiedWorkoutSession ", 1},
		{"insert native copied rhythm", "-- name: InsertCopiedWaterPrescription ", 1},
		{"read inserted workout", "-- name: GetFocusedWorkoutSession ", 2},
		{"read inserted workout graph", "-- name: ListFocusedWorkoutSegments ", 1},
		{"reread source after destination writes", "-- name: GetReusePlan ", 4},
		{"reread source rhythm after writes", "-- name: ListReusePinnedProfiles ", 1},
		{"verify copied artifact budget", "-- name: FocusedWaterArtifactSize ", 1},
		{"verify expiry after writes", "SELECT pg_catalog.clock_timestamp()", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, fault := interruptedReuseStore(t, f.publicationDomainFixture, tc.marker, tc.occurrence)
			result, err := store.ConfirmReuse(t.Context(), review)
			if fault.seen.Load() < tc.occurrence || !errors.Is(err, context.Canceled) || result != (reuseSaveResult{}) {
				t.Fatalf("confirmation succeeded or hid interrupted %s: observed=%d result=%+v error=%v", tc.name, fault.seen.Load(), result, err)
			}
			assertReuseStoreCounts(t, f.publicationDomainFixture, payload.Proposal.TargetPlanID, 0, 0, 0)
		})
	}
	result, err := f.store.ConfirmReuse(t.Context(), review)
	if err != nil || result.PlanID != payload.Proposal.TargetPlanID || result.Count != 1 || result.AlreadyCopied {
		t.Fatalf("healthy confirmation could not reuse unchanged review: %+v, %v", result, err)
	}
	assertReuseStoreCounts(t, f.publicationDomainFixture, result.PlanID, 1, 1, 2)
	for _, tc := range []struct {
		name, marker string
	}{
		{"read replay source rhythm", "-- name: LockReusePinnedProfiles "},
		{"verify replay artifact", "-- name: FocusedWaterArtifactSize "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, fault := interruptedReuseStore(t, f.publicationDomainFixture, tc.marker, 1)
			replayed, err := store.ConfirmReuse(t.Context(), review)
			if fault.seen.Load() < 1 || !errors.Is(err, context.Canceled) || replayed != (reuseSaveResult{}) {
				t.Fatalf("replay succeeded or hid interrupted %s: observed=%d result=%+v error=%v", tc.name, fault.seen.Load(), replayed, err)
			}
			assertReuseStoreCounts(t, f.publicationDomainFixture, result.PlanID, 1, 1, 2)
		})
	}
}

func TestReuseStoreInterruptedFragmentPreservesOriginalWorkout(t *testing.T) {
	f := newInterruptedReuseFixture(t)
	_, review := reuseRealReview(t, f.store, f.actor, f.routineSource, f.workoutTarget)
	workout := PostgresFocusedWorkoutStore{Pool: f.web}
	input := FocusedWorkoutEditRequest{f.actor, f.plan, f.workoutTarget.SessionID}
	original, err := workout.LoadFocusedEdit(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := focusedEditBaselineFor(original)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, marker string
	}{
		{"lock target workout", "-- name: LockFocusedWorkoutForEdit "},
		{"read locked target graph", "-- name: LockFocusedWorkoutSegments "},
		{"stage retained segment", "-- name: StageFocusedWorkoutSegmentPosition "},
		{"write new fragment", "-- name: InsertFocusedWorkoutSegment "},
		{"reread original target rhythm", "-- name: ListFocusedWorkoutEditProfiles "},
		{"check final eligibility after writing", "-- name: CheckFocusedWorkoutOriginalEligibility "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, fault := interruptedReuseStore(t, f.publicationDomainFixture, tc.marker, 1)
			result, err := store.ConfirmReuse(t.Context(), review)
			if fault.seen.Load() < 1 || !errors.Is(err, context.Canceled) || result != (reuseSaveResult{}) {
				t.Fatalf("append succeeded or hid interrupted %s: observed=%d result=%+v error=%v", tc.name, fault.seen.Load(), result, err)
			}
			current, err := workout.LoadFocusedEdit(t.Context(), input)
			if err != nil || baseline.matches(current) != nil {
				t.Fatalf("failed append changed the retained workout: %v", err)
			}
			var events int
			if err = f.web.QueryRow(t.Context(), `SELECT count(*) FROM training_copy_events WHERE source_id=$1`, f.routineSource.RoutineID).Scan(&events); err != nil || events != 0 {
				t.Fatalf("failed append retained %d history rows: %v", events, err)
			}
		})
	}
	result, err := f.store.ConfirmReuse(t.Context(), review)
	if err != nil || result.SessionID != f.workoutTarget.SessionID || result.Count != 1 || result.AlreadyCopied {
		t.Fatalf("healthy confirmation could not append to unchanged workout: %+v, %v", result, err)
	}
	t.Run("read replay target rhythm", func(t *testing.T) {
		store, fault := interruptedReuseStore(t, f.publicationDomainFixture, "-- name: LockFocusedWorkoutEditProfiles ", 1)
		replayed, err := store.ConfirmReuse(t.Context(), review)
		if fault.seen.Load() != 1 || !errors.Is(err, context.Canceled) || replayed != (reuseSaveResult{}) {
			t.Fatalf("replay succeeded or hid interrupted target rhythm: observed=%d result=%+v error=%v", fault.seen.Load(), replayed, err)
		}
	})
}

// Database-side changes to a reviewed copy must roll back the graph and history.
func TestReuseStoreReadbackRejectsDatabaseChanges(t *testing.T) {
	f := newInterruptedReuseFixture(t)
	payload, review := reuseRealReview(t, f.store, f.actor, f.nativeSource, f.weekTarget)
	var originalTitle string
	if err := f.web.QueryRow(t.Context(), `SELECT title FROM training_sessions WHERE id=$1`, f.nativeSource.SessionID).Scan(&originalTitle); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, timing, relation, statement string
	}{
		{"copied water prescription differs", "BEFORE", "water_block_prescriptions", `
			NEW.target_distance_metres := 7200;
			NEW.target_distance_certainty := 'EXACT';`},
		{"history insert changes reviewed source", "AFTER", "training_copy_events", `
			IF NEW.destination_kind = 'WEEK' THEN
				UPDATE training_sessions SET title='Unreviewed source change' WHERE id=TG_ARGV[0]::uuid;
			END IF;`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			sql := `CREATE FUNCTION reuse_fixture_writeback() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN ` + tc.statement + ` RETURN NEW; END $$;
				CREATE TRIGGER reuse_fixture_writeback ` + tc.timing + ` INSERT ON ` + tc.relation +
				` FOR EACH ROW EXECUTE FUNCTION reuse_fixture_writeback('` + f.nativeSource.SessionID.String() + `');`
			if _, err := f.low.Exec(ctx, sql); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := f.low.Exec(ctx, `DROP TRIGGER reuse_fixture_writeback ON `+tc.relation+`; DROP FUNCTION reuse_fixture_writeback()`); err != nil {
					t.Error(err)
				}
			}()
			result, err := f.store.ConfirmReuse(ctx, review)
			if !errors.Is(err, errReuseChanged) || result != (reuseSaveResult{}) {
				t.Fatalf("database divergence result=%+v error=%v", result, err)
			}
			assertReuseStoreCounts(t, f.publicationDomainFixture, payload.Proposal.TargetPlanID, 0, 0, 0)
			var title string
			if err = f.web.QueryRow(ctx, `SELECT title FROM training_sessions WHERE id=$1`, f.nativeSource.SessionID).Scan(&title); err != nil || title != originalTitle {
				t.Fatalf("failed copy changed the source title: %q, %v", title, err)
			}
		})
	}

	result, err := f.store.ConfirmReuse(t.Context(), review)
	if err != nil || result.PlanID != payload.Proposal.TargetPlanID || result.Count != 1 || result.AlreadyCopied {
		t.Fatalf("healthy copy after rollback = %+v, %v", result, err)
	}
	assertReuseStoreCounts(t, f.publicationDomainFixture, result.PlanID, 1, 1, 2)
	f.exec(t, `UPDATE training_sessions SET status='CANCELLED',cancelled_at=now(),cancelled_by_id=$2,cancellation_reason='Cancelled synthetic copy' WHERE id=$1`, result.SessionID, f.actor)
	if replay, err := f.store.ConfirmReuse(t.Context(), review); !errors.Is(err, errReuseDestinationChanged) || replay != (reuseSaveResult{}) {
		t.Fatalf("cancelled destination replay result = %+v, %v", replay, err)
	}
	assertReuseStoreCounts(t, f.publicationDomainFixture, result.PlanID, 1, 1, 2)
}

func TestReuseStoreIdentityCollisionPreservesExistingWorkout(t *testing.T) {
	f := newInterruptedReuseFixture(t)
	ctx := t.Context()
	payload, review := reuseRealReview(t, f.store, f.actor, f.nativeSource, f.weekTarget)
	occupiedPlan, occupiedSession := uuid.New(), payload.Proposal.Destinations[0].Graph.SessionID
	f.exec(t, `INSERT INTO training_plans(id,title,programme_id,training_group_id,season_id,week_start,created_by_id)
		SELECT $1,'Independent draft',programme_id,training_group_id,season_id,week_start+14,created_by_id FROM training_plans WHERE id=$2`, occupiedPlan, f.plan)
	f.exec(t, `INSERT INTO training_sessions(id,plan_id,title,starts_at,ends_at,created_by_id)
		SELECT $1,id,'Independent workout',(week_start::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '10 hours',
		(week_start::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '11 hours',created_by_id FROM training_plans WHERE id=$2`, occupiedSession, occupiedPlan)
	if result, err := f.store.ConfirmReuse(ctx, review); !errors.Is(err, errReuseChanged) || result != (reuseSaveResult{}) {
		t.Fatalf("occupied reviewed ID result = %+v, %v", result, err)
	}
	assertReuseStoreCounts(t, f.publicationDomainFixture, payload.Proposal.TargetPlanID, 0, 0, 0)
	occupied, err := dbgen.New(f.web).GetFocusedWorkoutSession(ctx, dbgen.GetFocusedWorkoutSessionParams{ActorID: f.actor, PlanID: occupiedPlan, SessionID: occupiedSession})
	if err != nil || occupied.Title != "Independent workout" {
		t.Fatalf("conflicting copy changed existing workout: %+v, %v", occupied, err)
	}

	// A fresh review can retain a native afternoon with no invented start or end.
	workout := PostgresFocusedWorkoutStore{Pool: f.web}
	logistics, createReview := graphRealReview(t, f.publicationDomainFixture, workout, url.Values{
		"title": {"Logística da tarde"}, "description": {"Ponto de encontro indicado pelo treinador"},
		"entry_kind": {"LOGISTICS"}, "schedule.mode": {"BAND"}, "schedule.band": {"AFTERNOON"},
	})
	if _, err = workout.SaveFocusedGraph(ctx, createReview); err != nil {
		t.Fatal(err)
	}
	afternoon := reuseSourceSelector{Kind: "SESSION", PlanID: f.plan, SessionID: logistics.Proposal.SessionID}
	payload, review = reuseRealReview(t, f.store, f.actor, afternoon, f.weekTarget)
	result, err := f.store.ConfirmReuse(ctx, review)
	if err != nil || result.PlanID != payload.Proposal.TargetPlanID || result.Count != 1 {
		t.Fatalf("afternoon copy failed: %+v, %v", result, err)
	}
	row, err := dbgen.New(f.web).GetFocusedWorkoutSession(ctx, dbgen.GetFocusedWorkoutSessionParams{ActorID: f.actor, PlanID: result.PlanID, SessionID: result.SessionID})
	if err != nil || row.EntryKind != "LOGISTICS" || row.SchedulePrecision != "BAND" || row.DayBand == nil || *row.DayBand != "AFTERNOON" || row.StartsAt.Valid || row.EndsAt.Valid {
		t.Fatalf("stored afternoon did not preserve schedule precision: %+v, %v", row, err)
	}
}
