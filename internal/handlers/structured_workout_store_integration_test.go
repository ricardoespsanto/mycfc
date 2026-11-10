//go:build integration

package handlers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func focusedRealReview(t *testing.T, store PostgresFocusedWorkoutStore, request FocusedWorkoutRequest) (focusedGraphReviewPayload, focusedGraphReviewed) {
	t.Helper()
	c, err := store.LoadFocusedWorkout(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	_, draft := focusedWorkoutFixture()
	draft.Values.Set("schedule.date", c.Day)
	draft.Values.Set("schedule.start_time", "16:00")
	draft.Values.Set(focusedPhasePrefix("p_1")+".instructions", "Preparar a técnica e aquecer.")
	draft.Values.Set(focusedPhasePrefix("p_3")+".instructions", "Regressar com remada leve.")
	graphDraft, parseErr := parseFocusedGraph(focusedDraftWithRemovalFields(draft))
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	proposal, errs := normalizeFocusedGraph(c, graphDraft)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	allocateFocusedGraphIDs(&proposal, nil)
	payload := focusedGraphReviewPayload{Proposal: proposal, Draft: focusedGraphValues(graphDraft)}
	raw, token, err := focusedWorkoutReviews.signGraph(payload, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, reviewed, err := focusedWorkoutReviews.verifyGraph(raw, token, c.ActorID, c.PlanID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return payload, reviewed
}
func TestFocusedWaterFreshAtomicCreateReplayGateAndHistory(t *testing.T) {
	f := newPublicationDomainFixture(t)
	store := PostgresFocusedWorkoutStore{Pool: f.web}
	// Existing exact publication/outcome remain immutable when adding a draft.
	publication, err := (PostgresStructuredTrainingStore{Pool: f.web}).ConfirmPublication(t.Context(), f.review(t, "Existing exact athlete instructions"))
	if err != nil {
		t.Fatal(err)
	}
	f.exec(t, `INSERT INTO training_session_outcomes(session_id,user_id,prescription_id,status) SELECT $1,$2,id,'COMPLETED' FROM training_prescriptions WHERE publication_id=$3`, f.session, f.subject, publication.PublicationID)
	var outcomesBefore string
	if err = f.web.QueryRow(t.Context(), `SELECT coalesce(jsonb_agg(to_jsonb(x)),'[]'::jsonb)::text FROM training_session_outcomes x`).Scan(&outcomesBefore); err != nil {
		t.Fatal(err)
	}
	var day time.Time
	if err := f.web.QueryRow(t.Context(), `SELECT week_start+1 FROM training_plans WHERE id=$1`, f.plan).Scan(&day); err != nil {
		t.Fatal(err)
	}
	request := FocusedWorkoutRequest{ActorID: f.actor, PlanID: f.plan, Day: day.Format("2006-01-02")}
	closed, err := store.LoadFocusedWorkout(t.Context(), request)
	if err != nil || closed.AuthoringEnabled {
		t.Fatal("fresh gate should be closed", err)
	}
	// Web cannot turn a blocked capability into authoring authority.
	if _, err = f.web.Exec(t.Context(), `SELECT training_schedule_control.set_enabled($1,$2,true)`, closed.DatabaseIncarnation, closed.StateEpoch); err == nil {
		t.Fatal("web enabled private gate")
	}
	if _, err = f.low.Exec(t.Context(), `SELECT training_schedule_control.set_enabled($1,$2,true)`, closed.DatabaseIncarnation, closed.StateEpoch); err != nil {
		t.Fatal(err)
	}
	payload, reviewed := focusedRealReview(t, store, request)
	var historyBefore string
	if err = f.web.QueryRow(t.Context(), `SELECT coalesce(jsonb_agg(to_jsonb(x)),'[]'::jsonb)::text FROM training_prescriptions x`).Scan(&historyBefore); err != nil {
		t.Fatal(err)
	}
	result, err := store.SaveFocusedGraph(t.Context(), reviewed)
	if err != nil || result.AlreadyCreated || result.SessionID != payload.Proposal.SessionID {
		t.Fatalf("save %+v %v", result, err)
	}
	repeated, err := store.SaveFocusedGraph(t.Context(), reviewed)
	if err != nil || !repeated.AlreadyCreated || repeated.SessionID != result.SessionID {
		t.Fatalf("exact replay %+v %v", repeated, err)
	}
	var end *time.Time
	var phases int
	var precision string
	if err = f.web.QueryRow(t.Context(), `SELECT ends_at,schedule_precision,(SELECT count(*) FROM training_segment_blocks block JOIN training_session_segments segment ON segment.id=block.segment_id WHERE segment.session_id=session.id) FROM training_sessions session WHERE id=$1`, result.SessionID).Scan(&end, &precision, &phases); err != nil || end != nil || precision != "START_ONLY" || phases != 3 {
		t.Fatalf("raw meaning changed end%v precision%s phases%d %v", end, precision, phases, err)
	}
	var snapshotsAfter string
	if err = f.web.QueryRow(t.Context(), `SELECT coalesce(jsonb_agg(to_jsonb(x)),'[]'::jsonb)::text FROM training_prescriptions x`).Scan(&snapshotsAfter); err != nil || historyBefore != snapshotsAfter {
		t.Fatal("draft rewrote prescription history", err)
	}
	var outcomesAfter string
	if err = f.web.QueryRow(t.Context(), `SELECT coalesce(jsonb_agg(to_jsonb(x)),'[]'::jsonb)::text FROM training_session_outcomes x`).Scan(&outcomesAfter); err != nil || outcomesBefore != outcomesAfter {
		t.Fatal("draft rewrote existing outcome", err)
	}
	// Another draft modification after review is an honest stale conflict.
	_, stale := focusedRealReview(t, store, request)
	f.exec(t, `UPDATE training_plans SET title='Changed source week' WHERE id=$1`, f.plan)
	if _, err = store.SaveFocusedGraph(t.Context(), stale); !errors.Is(err, errFocusedWorkoutConflict) {
		t.Fatal("stale source accepted", err)
	}
	_, fresh := focusedRealReview(t, store, request)
	if _, err = f.low.Exec(t.Context(), `SELECT training_schedule_control.set_enabled($1,$2,false)`, fresh.Proposal.Context.DatabaseIncarnation, fresh.Proposal.Context.StateEpoch); err != nil {
		t.Fatal(err)
	}
	if _, err = store.SaveFocusedGraph(t.Context(), fresh); err == nil {
		t.Fatal("closed/epochchanged gate accepted")
	}
}
func TestFocusedWaterCreateFinalPhaseFailureRollsBackWholeGraph(t *testing.T) {
	f := newPublicationDomainFixture(t)
	store := PostgresFocusedWorkoutStore{Pool: f.web}
	state, err := db.TrainingScheduleStatus(t.Context(), f.low)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.low.Exec(t.Context(), `SELECT training_schedule_control.set_enabled($1,$2,true)`, state.DatabaseIncarnation, state.StateEpoch); err != nil {
		t.Fatal(err)
	}
	var day time.Time
	if err = f.web.QueryRow(t.Context(), `SELECT week_start FROM training_plans WHERE id=$1`, f.plan).Scan(&day); err != nil {
		t.Fatal(err)
	}
	_, reviewed := focusedRealReview(t, store, FocusedWorkoutRequest{f.actor, f.plan, day.Format("2006-01-02")})
	// Test-only late DB fault, outside protected publication catalog: rollback
	// must remove the new session and all previously inserted plain phases.
	name := "focused_fault_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = f.root.Exec(t.Context(), `CREATE FUNCTION `+name+`() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.id='`+reviewed.Proposal.Activities[0].Phases[2].ID.String()+`'::uuid THEN RAISE EXCEPTION 'synthetic final phase fault'; END IF; RETURN NEW; END $$;CREATE TRIGGER `+name+` BEFORE INSERT ON training_segment_blocks FOR EACH ROW EXECUTE FUNCTION `+name+`() `); err != nil {
		t.Fatal(err)
	}
	if _, err = store.SaveFocusedGraph(t.Context(), reviewed); err == nil {
		t.Fatal("late phase fault accepted")
	}
	var sessions, segments, phases int
	if err = f.web.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM training_sessions WHERE id=$1),(SELECT count(*) FROM training_session_segments WHERE session_id=$1),(SELECT count(*) FROM training_segment_blocks WHERE id=ANY($2::uuid[]))`, reviewed.Proposal.SessionID, []uuid.UUID{reviewed.Proposal.Activities[0].Phases[0].ID, reviewed.Proposal.Activities[0].Phases[1].ID, reviewed.Proposal.Activities[0].Phases[2].ID}).Scan(&sessions, &segments, &phases); err != nil || sessions != 0 || segments != 0 || phases != 0 {
		t.Fatalf("partial graph %d/%d/%d %v", sessions, segments, phases, err)
	}
}
func TestFocusedWaterRealScopedRevocation(t *testing.T) {
	f := newPublicationDomainFixture(t)
	store := PostgresFocusedWorkoutStore{Pool: f.web}
	state, err := db.TrainingScheduleStatus(t.Context(), f.low)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.low.Exec(t.Context(), `SELECT training_schedule_control.set_enabled($1,$2,true)`, state.DatabaseIncarnation, state.StateEpoch); err != nil {
		t.Fatal(err)
	}
	var day time.Time
	if err = f.web.QueryRow(t.Context(), `SELECT week_start FROM training_plans WHERE id=$1`, f.plan).Scan(&day); err != nil {
		t.Fatal(err)
	}
	_, reviewed := focusedRealReview(t, store, FocusedWorkoutRequest{f.actor, f.plan, day.Format("2006-01-02")})
	spare := uuid.New()
	f.exec(t, `INSERT INTO users(id,name,email,password_hash,date_of_birth)VALUES($1,'Spare test administrator',$2,'hash','1980-01-01')`, spare, spare.String()+"@example.test")
	f.exec(t, `INSERT INTO user_platform_roles(user_id,role_id)SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, spare)
	if _, err = f.root.Exec(t.Context(), `DELETE FROM user_platform_roles WHERE user_id=$1`, f.actor); err != nil {
		t.Fatal(err)
	}
	if _, err = store.SaveFocusedGraph(context.Background(), reviewed); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("fresh revoked scope not denied", err)
	}
}

// The actual PostgreSQL jsonb-text size is a separate defensive limit from the
// earlier native encoded transport limit; this internal-store test pins it.
func TestFocusedWaterActualArtifactBoundaryAndWholeRollback(t *testing.T) {
	f := newPublicationDomainFixture(t)
	state, err := db.TrainingScheduleStatus(t.Context(), f.low)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.low.Exec(t.Context(), `SELECT training_schedule_control.set_enabled($1,$2,true)`, state.DatabaseIncarnation, state.StateEpoch); err != nil {
		t.Fatal(err)
	}
	store := PostgresFocusedWorkoutStore{Pool: f.web}
	var day time.Time
	if err = f.web.QueryRow(t.Context(), `SELECT week_start FROM training_plans WHERE id=$1`, f.plan).Scan(&day); err != nil {
		t.Fatal(err)
	}
	req := FocusedWorkoutRequest{f.actor, f.plan, day.Format("2006-01-02")}
	payload, _ := focusedRealReview(t, store, req)
	p := payload.Proposal
	p.Description = ""
	p.Activities[0].Phases = nil
	for i := 0; i < 32; i++ {
		p.Activities[0].Phases = append(p.Activities[0].Phases, focusedGraphPhaseInput{Key: fmt.Sprintf("p_%d", i+1), ID: uuid.New(), Purpose: "MAIN", Instructions: "\\\\"})
	}
	setSize := func(extra int, pad bool) int32 {
		for i := range p.Activities[0].Phases {
			n := min(extra, 3998)
			extra -= n
			p.Activities[0].Phases[i].Instructions = strings.Repeat("\\", 2+n)
		}
		p.Description = ""
		if pad {
			p.Description = "x"
		}
		raw, e := focusedGraphArtifact(p)
		if e != nil {
			t.Fatal(e)
		}
		n, e := store.FocusedWorkoutArtifactSize(t.Context(), req, raw)
		if e != nil {
			t.Fatal(e)
		}
		return n
	}
	lo, hi := 0, 32*3998
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if setSize(mid, false) <= focusedWorkoutMaxArtifactBytes {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	size := setSize(lo, false)
	if size == focusedWorkoutMaxArtifactBytes-1 {
		size = setSize(lo, true)
	}
	if size != focusedWorkoutMaxArtifactBytes {
		t.Fatalf("could not construct actual boundary: %d", size)
	}
	accepted := focusedGraphReviewed{Proposal: p, verified: true, expiresAt: time.Now().Add(time.Minute)}
	if _, err = store.SaveFocusedGraph(t.Context(), accepted); err != nil {
		t.Fatal("actual 200000B boundary rejected", err)
	}
	c, err := store.LoadFocusedWorkout(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	p.Context = c
	p.SessionID = uuid.New()
	p.Activities[0].ID = uuid.New()
	for i := range p.Activities[0].Phases {
		p.Activities[0].Phases[i].ID = uuid.New()
	}
	// One ordinary ASCII byte changes actual JSONB size by exactly one.
	p.Description += "x"
	raw, err := focusedGraphArtifact(p)
	if err != nil {
		t.Fatal(err)
	}
	over, err := store.FocusedWorkoutArtifactSize(t.Context(), req, raw)
	if err != nil || over != focusedWorkoutMaxArtifactBytes+1 {
		t.Fatal("actual overboundary", over, err)
	}
	rejected := focusedGraphReviewed{Proposal: p, verified: true, expiresAt: time.Now().Add(time.Minute)}
	if _, err = store.SaveFocusedGraph(t.Context(), rejected); !errors.Is(err, errFocusedWorkoutLimit) {
		t.Fatal("artifact overflow not rejected", err)
	}
	var count int
	if err = f.web.QueryRow(t.Context(), `SELECT count(*) FROM training_sessions WHERE id=$1`, p.SessionID).Scan(&count); err != nil || count != 0 {
		t.Fatal("oversize graph partially committed", count, err)
	}
}
