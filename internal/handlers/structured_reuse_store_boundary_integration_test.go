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
	"github.com/jackc/pgx/v5/pgconn"
)

func TestReuseStorePreviewKeepsSourceAndDestinationScoped(t *testing.T) {
	f := newPublicationDomainFixture(t)
	enableGraphFixture(t, f)
	ctx := t.Context()
	store := PostgresStructuredReuseStore{Pool: f.web}
	var week time.Time
	if err := f.web.QueryRow(ctx, `SELECT week_start FROM training_plans WHERE id=$1`, f.plan).Scan(&week); err != nil {
		t.Fatal(err)
	}
	nextWeek := week.AddDate(0, 0, 7).Format(time.DateOnly)
	target := reuseTargetSelector{GroupID: f.group, SeasonID: f.season, WeekStart: week.Format(time.DateOnly)}
	source := reuseSourceSelector{Kind: "SESSION", PlanID: f.plan, SessionID: f.session}
	f.exec(t, `INSERT INTO staff_grants(user_id,capability,programme_id,granted_by_id) VALUES($1,'COACH',$2,$3)`, f.subject, f.programme, f.actor)
	otherGroup := uuid.New()
	f.exec(t, `INSERT INTO training_groups(id,name,programme_id,created_by_id) SELECT $1,'Other programme',id,$2 FROM programmes WHERE code='Competition'`, otherGroup, f.actor)
	privateRoutine := uuid.New()
	f.exec(t, `INSERT INTO training_routines(id,name,kind,owner_user_id,modality,source_id,source_updated_at,snapshot)
		VALUES($1,'Rotina privada','BLOCK',$2,'GYM',$3,now(),'{"purpose":"MAIN","instructions":"Treino privado"}')`, privateRoutine, f.actor, f.session)
	malformedRoutine := uuid.New()
	f.exec(t, `INSERT INTO training_routines(id,name,kind,owner_user_id,modality,source_id,source_updated_at,snapshot)
		VALUES($1,'Rotina inválida','BLOCK',$2,'GYM',$3,now(),'{"unknown_field":true}')`, malformedRoutine, f.subject, f.session)

	read, err := store.ReadReuse(ctx, f.subject, source, nil)
	if err != nil || read.Target != nil || len(read.Source.Items) != 1 || read.Source.Items[0].Graph.SessionID != f.session {
		t.Fatalf("scoped source preview = %+v, %v", read.Source.Selector, err)
	}
	for _, tc := range []struct {
		name   string
		change func(*reuseTargetSelector)
		want   error
	}{
		{"outside coach programme", func(s *reuseTargetSelector) { s.GroupID = otherGroup }, pgx.ErrNoRows},
		{"starts on Tuesday", func(s *reuseTargetSelector) { s.WeekStart = week.AddDate(0, 0, 1).Format(time.DateOnly) }, errFocusedWorkoutInvalid},
		{"outside configured season", func(s *reuseTargetSelector) { s.WeekStart = week.AddDate(0, 0, 700).Format(time.DateOnly) }, pgx.ErrNoRows},
		{"segment requires session", func(s *reuseTargetSelector) { s.SegmentID = uuid.New() }, errFocusedWorkoutInvalid},
		{"session requires existing week", func(s *reuseTargetSelector) { s.WeekStart, s.SessionID = nextWeek, f.session }, errFocusedWorkoutInvalid},
		{"session outside selected week", func(s *reuseTargetSelector) { s.SessionID = uuid.New() }, pgx.ErrNoRows},
		{"segment outside selected session", func(s *reuseTargetSelector) { s.SessionID, s.SegmentID = f.session, uuid.New() }, pgx.ErrNoRows},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selection := target
			tc.change(&selection)
			if result, err := store.ReadReuse(t.Context(), f.subject, source, &selection); !errors.Is(err, tc.want) || result.Target != nil || result.TargetGraph != nil {
				t.Fatalf("target or graph exposed, error = %v, want %v", err, tc.want)
			}
		})
	}
	for _, tc := range []struct {
		name   string
		source reuseSourceSelector
		want   error
	}{
		{"invalid source selector", reuseSourceSelector{Kind: "SESSION", PlanID: f.plan}, errFocusedWorkoutInvalid},
		{"missing source plan", reuseSourceSelector{Kind: "WEEK", PlanID: uuid.New()}, pgx.ErrNoRows},
		{"session outside source plan", reuseSourceSelector{Kind: "SESSION", PlanID: f.plan, SessionID: uuid.New()}, pgx.ErrNoRows},
		{"private routine", reuseSourceSelector{Kind: "ROUTINE", RoutineID: privateRoutine}, pgx.ErrNoRows},
		{"malformed routine", reuseSourceSelector{Kind: "ROUTINE", RoutineID: malformedRoutine}, ErrTrainingSnapshotUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if result, err := store.ReadReuse(t.Context(), f.subject, tc.source, nil); !errors.Is(err, tc.want) || len(result.Source.Items) != 0 {
				t.Fatalf("source exposed or error = %v, want %v", err, tc.want)
			}
		})
	}
	f.exec(t, `UPDATE training_sessions SET status='CANCELLED',cancelled_at=now(),cancelled_by_id=$2,cancellation_reason='Synthetic cancellation' WHERE id=$1`, f.session, f.actor)
	read, err = store.ReadReuse(ctx, f.subject, source, &target)
	if err != nil || len(read.Source.Items) != 0 {
		t.Fatalf("cancelled source remained reusable: %v", err)
	}
}

func TestReuseStoreConfirmConflictsRequireFreshReview(t *testing.T) {
	f := newPublicationDomainFixture(t)
	enableGraphFixture(t, f)
	ctx := t.Context()
	store := PostgresStructuredReuseStore{Pool: f.web}
	var week time.Time
	if err := f.web.QueryRow(ctx, `SELECT week_start FROM training_plans WHERE id=$1`, f.plan).Scan(&week); err != nil {
		t.Fatal(err)
	}
	target := reuseTargetSelector{GroupID: f.group, SeasonID: f.season, WeekStart: week.AddDate(0, 0, 7).Format(time.DateOnly), Title: "Synthetic reuse destination"}
	source := reuseSourceSelector{Kind: "WEEK", PlanID: f.plan}
	payload, reviewed := reuseRealReview(t, store, f.actor, source, target)
	expired := reviewed
	expired.expiresAt = time.Now().Add(-time.Minute)
	if _, err := store.ConfirmReuse(ctx, expired); !errors.Is(err, errFocusedWorkoutInvalid) {
		t.Fatalf("expired review error = %v", err)
	}

	lock, err := f.low.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback(ctx)
	if _, err = lock.Exec(ctx, `SELECT id FROM training_groups WHERE id=$1 FOR UPDATE`, f.group); err != nil {
		t.Fatal(err)
	}
	lockCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err = store.ConfirmReuse(lockCtx, reviewed); !errors.Is(err, errStructuredTrainingPublicationConflict) {
		t.Fatalf("locked target configuration error = %v", err)
	}
	if err = lock.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	assertReuseStoreCounts(t, f, payload.Proposal.TargetPlanID, 0, 0, 0)

	competing := uuid.New()
	f.exec(t, `INSERT INTO training_plans(id,title,programme_id,training_group_id,season_id,week_start,created_by_id)
		SELECT $1,'Competing draft',programme_id,training_group_id,season_id,week_start+7,created_by_id FROM training_plans WHERE id=$2`, competing, f.plan)
	if _, err = store.ConfirmReuse(ctx, reviewed); !errors.Is(err, errReuseChanged) {
		t.Fatalf("previously absent target was silently adopted: %v", err)
	}
	assertReuseStoreCounts(t, f, payload.Proposal.TargetPlanID, 0, 0, 0)
	assertReuseStoreCounts(t, f, competing, 1, 0, 0)

	_, fresh := reuseRealReview(t, store, f.actor, source, target)
	copied, err := store.ConfirmReuse(ctx, fresh)
	if err != nil || copied.PlanID != competing || copied.Count != 1 || copied.AlreadyCopied {
		t.Fatalf("fresh target review result = %+v, %v", copied, err)
	}
	assertReuseStoreCounts(t, f, competing, 1, 1, 1)

	setPublicationFixtureEnabled(t, f, false)
	if _, err = store.ConfirmReuse(ctx, fresh); !errors.Is(err, ErrTrainingScheduleWriteUnsupported) {
		t.Fatalf("closed authoring admitted a replay: %v", err)
	}
	assertReuseStoreCounts(t, f, competing, 1, 1, 1)
}

func TestReuseStoreHistoryFailureRollsBackNewWeekAndAllCopies(t *testing.T) {
	f := newPublicationDomainFixture(t)
	enableGraphFixture(t, f)
	ctx := t.Context()
	store := PostgresStructuredReuseStore{Pool: f.web}
	var week time.Time
	if err := f.web.QueryRow(ctx, `SELECT week_start FROM training_plans WHERE id=$1`, f.plan).Scan(&week); err != nil {
		t.Fatal(err)
	}
	source := reuseSourceSelector{Kind: "WEEK", PlanID: f.plan}
	target := reuseTargetSelector{GroupID: f.group, SeasonID: f.season, WeekStart: week.AddDate(0, 0, 7).Format(time.DateOnly), Title: "Rollback fixture target"}
	payload, reviewed := reuseRealReview(t, store, f.actor, source, target)
	if _, err := f.low.Exec(ctx, `
		CREATE FUNCTION reuse_store_history_fault() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.destination_kind = 'SESSION' THEN
				RAISE EXCEPTION 'synthetic reuse history insert failure';
			END IF;
			RETURN NEW;
		END $$;
		CREATE TRIGGER reuse_store_history_fault BEFORE INSERT ON training_copy_events
		FOR EACH ROW EXECUTE FUNCTION reuse_store_history_fault()
	`); err != nil {
		t.Fatal(err)
	}
	_, err := store.ConfirmReuse(ctx, reviewed)
	var pgError *pgconn.PgError
	if !errors.As(err, &pgError) || pgError.Code != "P0001" || pgError.Message != "synthetic reuse history insert failure" {
		t.Fatalf("copy history error = %v", err)
	}
	assertReuseStoreCounts(t, f, payload.Proposal.TargetPlanID, 0, 0, 0)
	assertReuseStoreCounts(t, f, f.plan, 1, 1, 0)
	if _, err = f.low.Exec(ctx, `DROP TRIGGER reuse_store_history_fault ON training_copy_events; DROP FUNCTION reuse_store_history_fault()`); err != nil {
		t.Fatal(err)
	}
	copied, err := store.ConfirmReuse(ctx, reviewed)
	if err != nil || copied.PlanID != payload.Proposal.TargetPlanID || copied.Count != 1 || copied.AlreadyCopied {
		t.Fatalf("copy after rollback = %+v, %v", copied, err)
	}
	assertReuseStoreCounts(t, f, copied.PlanID, 1, 1, 2)
	f.exec(t, `UPDATE training_sessions SET title='Destination subsequently edited' WHERE id=$1`, copied.SessionID)
	if _, err = store.ConfirmReuse(ctx, reviewed); !errors.Is(err, errReuseDestinationChanged) {
		t.Fatalf("changed retained destination replay error = %v", err)
	}
	destination, err := dbgen.New(f.web).GetFocusedWorkoutSession(ctx, dbgen.GetFocusedWorkoutSessionParams{ActorID: f.actor, PlanID: copied.PlanID, SessionID: copied.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	if destination.Title != "Destination subsequently edited" {
		t.Fatalf("replay overwrote destination: %q", destination.Title)
	}
	assertReuseStoreCounts(t, f, copied.PlanID, 1, 1, 2)
}

func assertReuseStoreCounts(t *testing.T, f *publicationDomainFixture, plan uuid.UUID, plans, sessions, events int) {
	t.Helper()
	var gotPlans, gotSessions, gotEvents int
	if err := f.web.QueryRow(t.Context(), `SELECT
		(SELECT count(*) FROM training_plans WHERE id=$1),
		(SELECT count(*) FROM training_sessions WHERE plan_id=$1),
		(SELECT count(*) FROM training_copy_events WHERE destination_id=$1 OR destination_id IN (SELECT id FROM training_sessions WHERE plan_id=$1))`, plan).
		Scan(&gotPlans, &gotSessions, &gotEvents); err != nil {
		t.Fatal(err)
	}
	if gotPlans != plans || gotSessions != sessions || gotEvents != events {
		t.Fatalf("plan, session, copy event counts = %d, %d, %d, want %d, %d, %d", gotPlans, gotSessions, gotEvents, plans, sessions, events)
	}
}

func TestReuseStoreFragmentReviewLocksTargetRhythmAndDetectsDrift(t *testing.T) {
	f := newPublicationDomainFixture(t)
	enableGraphFixture(t, f)
	ctx := t.Context()
	q := dbgen.New(f.low)
	profile, err := q.CreateWaterIntensityProfile(ctx, dbgen.CreateWaterIntensityProfileParams{Name: "Ritmo fixado no destino", Craft: "KAYAK", CreatedByID: f.actor})
	if err != nil {
		t.Fatal(err)
	}
	addZone := func(code string) {
		t.Helper()
		if _, err := q.CreateWaterIntensityZone(ctx, dbgen.CreateWaterIntensityZoneParams{ProfileID: profile.ID, Code: code, Label: "Zona " + code, Meaning: "Synthetic rhythm guidance"}); err != nil {
			t.Fatal(err)
		}
	}
	addZone("R2")
	_, raw := focusedGraphWaterExample()
	raw.Set("activities[a_1].phases[p_1].water.profile_id", profile.ID.String())
	workout := PostgresFocusedWorkoutStore{Pool: f.web}
	created, createReview := graphRealReview(t, f, workout, raw)
	if _, err = workout.SaveFocusedGraph(ctx, createReview); err != nil {
		t.Fatal(err)
	}
	routine := uuid.New()
	f.exec(t, `INSERT INTO training_routines(id,name,kind,owner_user_id,modality,source_id,source_updated_at,snapshot)
		VALUES($1,'Rotina de mobilidade','SEGMENT',$2,'GYM',$3,now(),
		'{"modality":"GYM","title":"Mobilidade","blocks":[{"purpose":"COOL_DOWN","instructions":"Movimentos controlados"}]}')`, routine, f.actor, f.session)
	source := reuseSourceSelector{Kind: "ROUTINE", RoutineID: routine}
	target := reuseTargetSelector{GroupID: f.group, SeasonID: f.season, WeekStart: created.Proposal.Schedule.Date, SessionID: created.Proposal.SessionID}
	store := PostgresStructuredReuseStore{Pool: f.web}
	payload, reviewed := reuseRealReview(t, store, f.actor, source, target)
	if len(payload.References.Profiles) != 1 || payload.References.Profiles[0].ID != profile.ID || len(payload.References.Zones) != 1 {
		t.Fatal("preview did not carry the existing workout's pinned rhythm")
	}
	for _, lockCase := range []struct {
		name  string
		query string
		id    uuid.UUID
	}{
		{"source routine", `SELECT id FROM training_routines WHERE id=$1 FOR UPDATE`, routine},
		{"target rhythm", `SELECT id FROM water_intensity_profiles WHERE id=$1 FOR UPDATE`, profile.ID},
	} {
		t.Run(lockCase.name, func(t *testing.T) {
			lock, err := f.low.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Rollback(t.Context())
			if _, err = lock.Exec(t.Context(), lockCase.query, lockCase.id); err != nil {
				t.Fatal(err)
			}
			bounded, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			if _, err = store.ConfirmReuse(bounded, reviewed); !errors.Is(err, errStructuredTrainingPublicationConflict) {
				t.Fatalf("busy %s error = %v", lockCase.name, err)
			}
		})
	}
	addZone("R3")
	if _, err = store.ConfirmReuse(ctx, reviewed); !errors.Is(err, errReuseChanged) {
		t.Fatalf("target rhythm drift error = %v", err)
	}
	_, fresh := reuseRealReview(t, store, f.actor, source, target)
	copied, err := store.ConfirmReuse(ctx, fresh)
	if err != nil || copied.SessionID != target.SessionID || copied.Count != 1 || copied.AlreadyCopied {
		t.Fatalf("append with refreshed rhythm = %+v, %v", copied, err)
	}
	again, err := store.ConfirmReuse(ctx, fresh)
	if err != nil || !again.AlreadyCopied {
		t.Fatalf("unchanged fragment replay = %+v, %v", again, err)
	}
	addZone("R4")
	if _, err = store.ConfirmReuse(ctx, fresh); !errors.Is(err, errReuseChanged) {
		t.Fatalf("replayed target rhythm drift error = %v", err)
	}
	var segments, events int
	if err = f.web.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM training_session_segments WHERE session_id=$1),
		(SELECT count(*) FROM training_copy_events WHERE source_id=$2)`, target.SessionID, routine).Scan(&segments, &events); err != nil || segments != 2 || events != 1 {
		t.Fatalf("fragment or history duplicated: segments=%d events=%d error=%v", segments, events, err)
	}
}
