//go:build integration

package handlers

import (
	"errors"
	"fmt"
	"net/url"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
)

func TestReusePostgresCompleteProfileUnion32Then33WholeRefusal(t *testing.T) {
	f := newPublicationDomainFixture(t)
	enableGraphFixture(t, f)
	store := PostgresStructuredReuseStore{Pool: f.web}
	q := dbgen.New(f.web)
	second, firstSegment, secondSegment := uuid.New(), uuid.New(), uuid.New()
	f.exec(t, `INSERT INTO training_sessions(id,plan_id,title,description,starts_at,ends_at,modality_id,entry_kind,status,created_by_id,schedule_precision,scheduled_on,day_band)
 SELECT $1,plan_id,'Segundo treino sintético',description,starts_at,ends_at,modality_id,entry_kind,'ACTIVE',created_by_id,schedule_precision,scheduled_on,day_band FROM training_sessions WHERE id=$2`, second, f.session)
	f.exec(t, `INSERT INTO training_session_segments(id,session_id,position,modality) VALUES($1,$2,1,'WATER'),($3,$4,1,'WATER')`, firstSegment, f.session, secondSegment, second)
	addPin := func(i int, segment uuid.UUID, position int) {
		t.Helper()
		profile, err := q.CreateWaterIntensityProfile(t.Context(), dbgen.CreateWaterIntensityProfileParams{Name: fmt.Sprintf("Perfil sintético completo %02d", i), Craft: dbgen.PaddlingCraftKAYAK, CreatedByID: f.actor})
		if err != nil {
			t.Fatal(err)
		}
		block := uuid.New()
		f.exec(t, `INSERT INTO training_segment_blocks(id,segment_id,position,purpose,instructions) VALUES($1,$2,$3,'MAIN','Instruções próprias sintéticas')`, block, segment, position)
		f.exec(t, `INSERT INTO water_block_prescriptions(block_id,method,intensity_profile_id) VALUES($1,'INTERVALS',$2)`, block, profile.ID)
	}
	for i := 0; i < 32; i++ {
		addPin(i, firstSegment, i+1)
	}
	var season uuid.UUID
	var week time.Time
	if err := f.web.QueryRow(t.Context(), `SELECT season_id,week_start+7 FROM training_plans WHERE id=$1`, f.plan).Scan(&season, &week); err != nil {
		t.Fatal(err)
	}
	source := reuseSourceSelector{Kind: "WEEK", PlanID: f.plan}
	target := reuseTargetSelector{GroupID: f.group, SeasonID: season, WeekStart: week.Format("2006-01-02"), Title: "Destino explícito sintético"}
	payload, review := reuseRealReview(t, store, f.actor, source, target)
	if len(payload.References.Profiles) != 32 || len(payload.Source.Items) != 2 {
		t.Fatal("exact32 complete source closure missing")
	}
	addPin(32, secondSegment, 1) // Each workout is bounded; the operation-wide union is not.
	if _, err := store.ReadReuse(t.Context(), f.actor, source, &target); !errors.Is(err, errFocusedWorkoutLimit) {
		t.Fatal("33 pins preview did not refuse whole operation", err)
	}
	if _, err := store.ConfirmReuse(t.Context(), review); !errors.Is(err, errFocusedWorkoutLimit) {
		t.Fatal("33 pins confirm did not refuse whole operation", err)
	}
	var sessions, events, targets int
	if err := f.web.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM training_sessions),(SELECT count(*) FROM training_copy_events),(SELECT count(*) FROM training_plans WHERE id=$1)`, payload.Proposal.TargetPlanID).Scan(&sessions, &events, &targets); err != nil || sessions != 2 || events != 0 || targets != 0 {
		t.Fatal("whole refusal mutated copies", sessions, events, targets, err)
	}
}

func reuseRealReview(t *testing.T, store PostgresStructuredReuseStore, actor uuid.UUID, source reuseSourceSelector, target reuseTargetSelector) (reuseReviewPayload, reuseReviewed) {
	t.Helper()
	read, err := store.ReadReuse(t.Context(), actor, source, &target)
	if err != nil {
		t.Fatal("read reuse", err)
	}
	values := mapReuseInitial(t, read)
	proposal, fields := buildReuseProposal(actor, read, values, nil)
	if len(fields) > 0 {
		t.Fatal("build reuse", fields)
	}
	raw, token, err := focusedWorkoutReviews.signReuse(proposal, time.Now())
	if err != nil {
		t.Fatal("sign reuse", err)
	}
	_, review, err := focusedWorkoutReviews.verifyReuse(raw, token, actor, source, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return proposal, review
}
func mapReuseInitial(t *testing.T, read reuseRead) mapValuesURL {
	t.Helper()
	values := mapValuesURL{}
	if read.Source.Items[0].FragmentKind != "" {
		return values
	}
	for _, item := range read.Source.Items {
		day := read.Target.Selector.WeekStart
		part := initialReuseMapping(item, day)
		for name, vals := range part {
			values[name] = append(values[name], vals...)
		}
		if item.DurationSeconds != nil {
			values.Set(reuseMappingPrefix(item.Key)+".start_time", "16:00")
		}
	}
	return values
}

type mapValuesURL = url.Values

func TestReusePostgresNativeStructuredCopyIndependentReplayAndHistory(t *testing.T) {
	f := newPublicationDomainFixture(t)
	enableGraphFixture(t, f)
	create := PostgresFocusedWorkoutStore{Pool: f.web}
	_, raw := focusedGraphWaterExample()
	created, review := graphRealReview(t, f, create, raw)
	if _, err := create.SaveFocusedGraph(t.Context(), review); err != nil {
		t.Fatal(err)
	}
	var season uuid.UUID
	var week time.Time
	if err := f.web.QueryRow(t.Context(), `SELECT season_id,week_start+7 FROM training_plans WHERE id=$1`, f.plan).Scan(&season, &week); err != nil {
		t.Fatal(err)
	}
	source := reuseSourceSelector{Kind: "SESSION", PlanID: f.plan, SessionID: created.Proposal.SessionID}
	target := reuseTargetSelector{GroupID: f.group, SeasonID: season, WeekStart: week.Format("2006-01-02"), Title: "Semana copiada explicitamente"}
	store := PostgresStructuredReuseStore{Pool: f.web}
	original, err := create.LoadFocusedEdit(t.Context(), FocusedWorkoutEditRequest{f.actor, f.plan, source.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	baseline, _ := focusedEditBaselineFor(original)
	proposal, approval := reuseRealReview(t, store, f.actor, source, target)
	result, err := store.ConfirmReuse(t.Context(), approval)
	if err != nil {
		t.Fatal("copy", err)
	}
	if result.Count != 1 || result.AlreadyCopied || result.PlanID != proposal.Proposal.TargetPlanID {
		t.Fatal(result)
	}
	copied, err := readReuseDestination(t.Context(), dbgen.New(f.web), f.actor, proposal.Proposal.Destinations[0])
	if err != nil {
		t.Fatal("copied raw graph", err)
	}
	if copied.Session.ID == source.SessionID || copied.Session.PlanID == f.plan {
		t.Fatal("not independent")
	}
	again, err := store.ConfirmReuse(t.Context(), approval)
	if err != nil || !again.AlreadyCopied || again.PlanID != result.PlanID {
		t.Fatal("replay", again, err)
	}
	unchanged, err := create.LoadFocusedEdit(t.Context(), FocusedWorkoutEditRequest{f.actor, f.plan, source.SessionID})
	if err != nil || baseline.matches(unchanged) != nil {
		t.Fatal("source changed", err)
	}
	var events, sessions int
	if err = f.web.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM training_copy_events WHERE destination_id IN ($1,$2)),(SELECT count(*) FROM training_sessions WHERE plan_id=$2)`, result.SessionID, result.PlanID).Scan(&events, &sessions); err != nil || events != 2 || sessions != 1 {
		t.Fatal("copy events/session count", events, sessions, err)
	}
	f.exec(t, `UPDATE training_sessions SET title='Edited independent destination' WHERE id=$1`, result.SessionID)
	if _, err = store.ConfirmReuse(t.Context(), approval); err == nil {
		t.Fatal("edited replay overwrote destination")
	}
}
func TestReusePostgresSamePlanDaySetExcludesOnlyOwnDestinations(t *testing.T) {
	f := newPublicationDomainFixture(t)
	enableGraphFixture(t, f)
	store := PostgresStructuredReuseStore{Pool: f.web}
	var season uuid.UUID
	var day string
	if err := f.web.QueryRow(t.Context(), `SELECT season_id,week_start::text FROM training_plans WHERE id=$1`, f.plan).Scan(&season, &day); err != nil {
		t.Fatal(err)
	}
	source := reuseSourceSelector{Kind: "DAY", PlanID: f.plan, Day: day}
	target := reuseTargetSelector{GroupID: f.group, SeasonID: season, WeekStart: day}
	proposal, review := reuseRealReview(t, store, f.actor, source, target)
	result, err := store.ConfirmReuse(t.Context(), review)
	if err != nil {
		t.Fatal("same plan copy", err)
	}
	if result.PlanID != f.plan || result.Count != 1 {
		t.Fatal(result)
	}
	replay, err := store.ConfirmReuse(t.Context(), review)
	if err != nil || !replay.AlreadyCopied {
		t.Fatal("same plan replay", err)
	}
	var count int
	if err = f.web.QueryRow(t.Context(), `SELECT count(*) FROM training_sessions WHERE plan_id=$1`, f.plan).Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	// A separate current source change must conflict before any additional writes.
	_, freshReview := reuseRealReview(t, store, f.actor, source, target)
	f.exec(t, `UPDATE training_sessions SET title='Source changed' WHERE id=$1`, f.session)
	if _, err = store.ConfirmReuse(t.Context(), freshReview); err == nil {
		t.Fatal("source changed before a new confirmation was accepted")
	}
	var unchanged int
	if err = f.web.QueryRow(t.Context(), `SELECT count(*) FROM training_sessions WHERE id=$1`, proposal.Proposal.Destinations[0].Graph.SessionID).Scan(&unchanged); err != nil || unchanged != 1 {
		t.Fatal("stale retry duplicated", err)
	}
}

func TestReusePostgresConfiguredSegmentAndBlockRoutinesAppendUnderEditRules(t *testing.T) {
	f := newPublicationDomainFixture(t)
	enableGraphFixture(t, f)
	store := PostgresStructuredReuseStore{Pool: f.web}
	var season uuid.UUID
	var day string
	if err := f.web.QueryRow(t.Context(), `SELECT season_id,week_start::text FROM training_plans WHERE id=$1`, f.plan).Scan(&season, &day); err != nil {
		t.Fatal(err)
	}
	segmentRoutine, blockRoutine := uuid.New(), uuid.New()
	segmentJSON := `{"modality":"GYM","title":"Força A","location":"Ginásio","blocks":[{"purpose":"MAIN","title":"Circuito","instructions":"Manter execução controlada.","gym":{"structure":"CIRCUIT","objective":"STRENGTH_ENDURANCE","rounds":3,"round_recovery_seconds":90,"exercises":[{"position":1,"name":"Agachamento","sets":2,"repetitions":12,"resistance_kind":"PERCENT_1RM","resistance_value":70,"notes":"Execução controlada."}]}}]}`
	blockJSON := `{"purpose":"COOL_DOWN","title":"Mobilidade","instructions":"Mobilidade suave no final."}`
	f.exec(t, `INSERT INTO training_routines(id,name,kind,owner_user_id,modality,source_id,source_updated_at,snapshot) VALUES($1,'Força A','SEGMENT',$2,'GYM',$3,now(),$4::jsonb),($5,'Mobilidade final','BLOCK',$2,'GYM',$3,now(),$6::jsonb)`, segmentRoutine, f.actor, f.session, segmentJSON, blockRoutine, blockJSON)
	source := reuseSourceSelector{Kind: "ROUTINE", RoutineID: segmentRoutine}
	target := reuseTargetSelector{GroupID: f.group, SeasonID: season, WeekStart: day, SessionID: f.session}
	proposal, review := reuseRealReview(t, store, f.actor, source, target)
	first, err := store.ConfirmReuse(t.Context(), review)
	if err != nil {
		t.Fatal("segment routine", err)
	}
	if first.SessionID != f.session || len(proposal.Proposal.Destinations[0].Graph.Activities) != 1 {
		t.Fatal(first)
	}
	segmentID := proposal.Proposal.Destinations[0].Graph.Activities[0].ID
	source.RoutineID = blockRoutine
	target.SegmentID = segmentID
	_, review = reuseRealReview(t, store, f.actor, source, target)
	if _, err = store.ConfirmReuse(t.Context(), review); err != nil {
		t.Fatal("block routine", err)
	}
	replay, err := store.ConfirmReuse(t.Context(), review)
	if err != nil || !replay.AlreadyCopied {
		t.Fatal("fragment replay", err)
	}
	var segments, blocks, exercises int
	var resistance float64
	if err = f.web.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM training_session_segments WHERE session_id=$1),(SELECT count(*) FROM training_segment_blocks WHERE segment_id=$2),(SELECT count(*) FROM gym_exercises x JOIN training_segment_blocks b ON b.id=x.block_id WHERE b.segment_id=$2),(SELECT resistance_value FROM gym_exercises x JOIN training_segment_blocks b ON b.id=x.block_id WHERE b.segment_id=$2 LIMIT 1)`, f.session, segmentID).Scan(&segments, &blocks, &exercises, &resistance); err != nil || segments != 1 || blocks != 2 || exercises != 1 || resistance != 70 {
		t.Fatal("prescribed counts/load lost", segments, blocks, exercises, resistance, err)
	}
	f.exec(t, `UPDATE training_sessions SET status='CANCELLED',cancelled_at=now(),cancelled_by_id=$2,cancellation_reason='Cancelamento explícito' WHERE id=$1`, f.session, f.actor)
	current, err := store.ReadReuse(t.Context(), f.actor, source, &target)
	if err != nil {
		t.Fatal(err)
	}
	if _, fields := buildReuseProposal(f.actor, current, mapValuesURL{}, nil); len(fields) == 0 {
		t.Fatal("cancelled target accepted")
	}
}

func TestReusePostgresSynthetic64SamePlanRepeatBeforeFreshSourceCap(t *testing.T) {
	f := newPublicationDomainFixture(t)
	enableGraphFixture(t, f)
	store := PostgresStructuredReuseStore{Pool: f.web}
	f.exec(t, `INSERT INTO training_sessions(plan_id,title,starts_at,ends_at,created_by_id) SELECT plan.id,'Synthetic boundary '||ordinal,(plan.week_start::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '10 hours'+ordinal*interval '1 minute',(plan.week_start::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '11 hours'+ordinal*interval '1 minute',$2 FROM training_plans plan CROSS JOIN generate_series(1,63) ordinal WHERE plan.id=$1`, f.plan, f.actor)
	var season uuid.UUID
	var day string
	if err := f.web.QueryRow(t.Context(), `SELECT season_id,week_start::text FROM training_plans WHERE id=$1`, f.plan).Scan(&season, &day); err != nil {
		t.Fatal(err)
	}
	source := reuseSourceSelector{Kind: "DAY", PlanID: f.plan, Day: day}
	target := reuseTargetSelector{GroupID: f.group, SeasonID: season, WeekStart: day}
	payload, review := reuseRealReview(t, store, f.actor, source, target)
	if len(payload.Source.Items) != 64 {
		t.Fatal("not full synthetic boundary")
	}
	canonical, _ := reuseBoundedJSON(payload)
	start := time.Now()
	result, err := store.ConfirmReuse(t.Context(), review)
	t.Logf("SYNTHETIC64 canonical-envelope=%d bytes; composed-confirm=%s (not a club-plan/capacity claim)", len(canonical), time.Since(start))
	if err != nil || result.Count != 64 {
		t.Fatal("whole boundary confirmation", result, err)
	}
	if _, err = store.ReadReuse(t.Context(), f.actor, source, &target); !errors.Is(err, errFocusedWorkoutLimit) {
		t.Fatal("enlarged initial source was not bounded", err)
	}
	start = time.Now()
	repeat, err := store.ConfirmReuse(t.Context(), review)
	t.Logf("SYNTHETIC64 admitted exact-repeat=%s", time.Since(start))
	if err != nil || !repeat.AlreadyCopied || repeat.PlanID != result.PlanID || repeat.SessionID != result.SessionID {
		t.Fatal("repeat required enlarged source enumeration", repeat, err)
	}
	var sessions, events int
	if err = f.web.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM training_sessions WHERE plan_id=$1),(SELECT count(*) FROM training_copy_events WHERE copied_by_id=$2)`, f.plan, f.actor).Scan(&sessions, &events); err != nil || sessions != 128 || events != 64 {
		t.Fatal("repeat duplicated/repaired a batch", sessions, events, err)
	}
}
