//go:build integration

package handlers

import (
	"context"
	"errors"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
)

func graphRealReview(t *testing.T, f *publicationDomainFixture, store PostgresFocusedWorkoutStore, raw url.Values) (focusedGraphReviewPayload, focusedGraphReviewed) {
	t.Helper()
	var day time.Time
	if err := f.web.QueryRow(t.Context(), `SELECT week_start FROM training_plans WHERE id=$1`, f.plan).Scan(&day); err != nil {
		t.Fatal(err)
	}
	req := FocusedWorkoutRequest{f.actor, f.plan, day.Format("2006-01-02")}
	c, err := store.LoadFocusedWorkout(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	raw = cloneFocusedValues(raw)
	raw.Set("schedule.date", c.Day)
	if raw.Get("schedule.end_date") != "" {
		raw.Set("schedule.end_date", c.Day)
	}
	draft, err := parseFocusedGraph(raw)
	if err != nil {
		t.Fatal(err)
	}
	p, e := normalizeFocusedGraph(c, draft)
	if len(e) > 0 {
		t.Fatal(e)
	}
	source, err := store.ReadFocusedGraphPreview(t.Context(), req, focusedGraphSelectedProfileIDs(p))
	if err != nil {
		t.Fatal(err)
	}
	p.Context, p.References = source.Context, source.References
	allocateFocusedGraphIDs(&p, nil)
	payload := focusedGraphReviewPayload{p, focusedGraphValues(draft)}
	encoded, token, err := focusedWorkoutReviews.signGraph(payload, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, review, err := focusedWorkoutReviews.verifyGraph(encoded, token, f.actor, f.plan, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return payload, review
}
func enableGraphFixture(t *testing.T, f *publicationDomainFixture) {
	t.Helper()
	state, err := db.TrainingScheduleStatus(t.Context(), f.low)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.low.Exec(t.Context(), `SELECT training_schedule_control.set_enabled($1,$2,true)`, state.DatabaseIncarnation, state.StateEpoch); err != nil {
		t.Fatal(err)
	}
}
func TestFocusedGraphFreshStructuredWaterAtomicRoundtripAndReplay(t *testing.T) {
	f := newPublicationDomainFixture(t)
	enableGraphFixture(t, f)
	store := PostgresFocusedWorkoutStore{Pool: f.web}
	// Existing exact history/outcomes are immutable when this new draft is saved.
	publication, err := (PostgresStructuredTrainingStore{Pool: f.web}).ConfirmPublication(t.Context(), f.review(t, "Existing exact history before structured authoring"))
	if err != nil {
		t.Fatal(err)
	}
	f.exec(t, `INSERT INTO training_session_outcomes(session_id,user_id,prescription_id,status) SELECT $1,$2,id,'COMPLETED' FROM training_prescriptions WHERE publication_id=$3`, f.session, f.subject, publication.PublicationID)
	var historyBefore string
	if err = f.web.QueryRow(t.Context(), `SELECT jsonb_build_array((SELECT jsonb_agg(to_jsonb(p)) FROM training_prescriptions p),(SELECT jsonb_agg(to_jsonb(o)) FROM training_session_outcomes o))::text`).Scan(&historyBefore); err != nil {
		t.Fatal(err)
	}
	_, raw := focusedGraphWaterExample()
	payload, review := graphRealReview(t, f, store, raw)
	result, err := store.SaveFocusedGraph(t.Context(), review)
	if err != nil || result.SessionID != payload.Proposal.SessionID {
		t.Fatal("structured save", result, err)
	}
	actual, err := readFocusedGraph(t.Context(), dbgen.New(f.web), payload.Proposal)
	if err != nil || !focusedGraphJSONEqual(actual, payload.Proposal) {
		t.Fatal("complete raw mismatch", err)
	}
	repeated, err := store.SaveFocusedGraph(t.Context(), review)
	if err != nil || !repeated.AlreadyCreated || repeated.SessionID != result.SessionID {
		t.Fatal("same graph replay", err)
	}
	expectedView, err := projectFocusedGraph(payload.Proposal)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := dbgen.New(f.web).ListStructuredTrainingOverviewForManager(t.Context(), dbgen.ListStructuredTrainingOverviewForManagerParams{UserID: f.actor, IsAdmin: true})
	if err != nil {
		t.Fatal(err)
	}
	audiences := assembleStructuredTraining(managerStructuredRows(rows), lisbonLocation())
	var storedView *pages.StructuredTrainingSession
	for _, a := range audiences {
		for _, w := range a.Weeks {
			for _, s := range w.Sessions {
				if s.ID == result.SessionID.String() {
					copy := s
					storedView = &copy
				}
			}
		}
	}
	if storedView == nil || !focusedGraphJSONEqual(expectedView.Segments, storedView.Segments) {
		t.Fatal("canonical preview/manager saved instructions differ")
	}
	// Existing publication semantics refuse the whole partial source, not an
	// exact-only subset; CREATE itself must not manufacture a publication.
	var publications int
	if err = f.web.QueryRow(t.Context(), `SELECT count(*) FROM training_plan_publications WHERE plan_id=$1`, f.plan).Scan(&publications); err != nil || publications != 1 {
		t.Fatal("draft published", err)
	}
	var historyAfter string
	if err = f.web.QueryRow(t.Context(), `SELECT jsonb_build_array((SELECT jsonb_agg(to_jsonb(p)) FROM training_prescriptions p),(SELECT jsonb_agg(to_jsonb(o)) FROM training_session_outcomes o))::text`).Scan(&historyAfter); err != nil || historyBefore != historyAfter {
		t.Fatal("structured draft rewrote frozen history/outcomes", err)
	}
}
func TestFocusedGraphSelectedProfileFactsAndScopeConflict(t *testing.T) {
	f := newPublicationDomainFixture(t)
	enableGraphFixture(t, f)
	store := PostgresFocusedWorkoutStore{Pool: f.web}
	q := dbgen.New(f.low)
	profile, err := q.CreateWaterIntensityProfile(t.Context(), dbgen.CreateWaterIntensityProfileParams{Name: "Perfil selecionado sintético", Craft: "KAYAK", Notes: "Valores só para teste", CreatedByID: f.actor})
	if err != nil {
		t.Fatal(err)
	}
	_, err = q.CreateWaterIntensityZone(t.Context(), dbgen.CreateWaterIntensityZoneParams{ProfileID: profile.ID, Code: "R2", Label: "Zona inicial", Meaning: "Orientação inicial"})
	if err != nil {
		t.Fatal(err)
	}
	_, raw := focusedGraphWaterExample()
	raw.Set("activities[a_1].phases[p_1].water.profile_id", profile.ID.String())
	payload, review := graphRealReview(t, f, store, raw)
	if len(payload.Proposal.References.Profiles) != 1 || len(payload.Proposal.References.Zones) != 1 {
		t.Fatal("selected source incomplete")
	}
	busy, err := f.low.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = busy.Exec(t.Context(), `SELECT id FROM water_intensity_profiles WHERE id=$1 FOR UPDATE`, profile.ID); err != nil {
		t.Fatal(err)
	}
	_, busyErr := store.SaveFocusedGraph(t.Context(), review)
	if rollbackErr := busy.Rollback(t.Context()); rollbackErr != nil {
		t.Fatal(rollbackErr)
	}
	if !errors.Is(busyErr, errStructuredTrainingPublicationConflict) {
		t.Fatal("intentional profile NOWAIT did not return retained conflict", busyErr)
	}

	_, err = q.CreateWaterIntensityZone(t.Context(), dbgen.CreateWaterIntensityZoneParams{ProfileID: profile.ID, Code: "R3", Label: "Nova zona", Meaning: "Inserção posterior à revisão"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.SaveFocusedGraph(t.Context(), review); !errors.Is(err, errFocusedWorkoutConflict) {
		t.Fatal("new-zone source drift silently accepted", err)
	}
	var count int
	if err = f.web.QueryRow(t.Context(), `SELECT count(*) FROM training_sessions WHERE id=$1`, payload.Proposal.SessionID).Scan(&count); err != nil || count != 0 {
		t.Fatal("reference conflict inserted draft", err)
	}
	_, fresh := graphRealReview(t, f, store, raw)
	if _, err = store.SaveFocusedGraph(t.Context(), fresh); err != nil {
		t.Fatal("fresh explicit profile review", err)
	}
}
func TestFocusedGraphLateDetailFailureRollsBackSessionAndAllParents(t *testing.T) {
	f := newPublicationDomainFixture(t)
	enableGraphFixture(t, f)
	store := PostgresFocusedWorkoutStore{Pool: f.web}
	_, raw := focusedGraphWaterExample()
	raw.Add("activity_order", "a_2")
	raw.Set("activities[a_2].modality", "GYM")
	raw.Add("activities[a_2].phase_order", "p_2")
	pp := "activities[a_2].phases[p_2]"
	raw.Set(pp+".purpose", "MAIN")
	raw.Set(pp+".instructions", "Força nas próprias palavras.")
	raw.Set(pp+".detail_mode", "GYM")
	raw.Set(pp+".gym.structure", "SUPERSET")
	raw.Set(pp+".gym.objective", "CORE")
	raw.Set(pp+".gym.rounds", "3")
	raw.Add(pp+".gym.exercise_order", "e_1")
	raw.Set(pp+".gym.exercises[e_1].name", "Prancha lateral")
	raw.Set(pp+".gym.exercises[e_1].duration_seconds", "30")
	payload, review := graphRealReview(t, f, store, raw)
	// A test-only last-detail error must not preserve the already inserted water
	// tree, gym prescription or any intermediate parent.
	name := "graph_fault_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := f.root.Exec(t.Context(), `CREATE FUNCTION `+name+`() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'synthetic final detail fault';END$$;CREATE TRIGGER `+name+` BEFORE INSERT ON gym_exercises FOR EACH ROW EXECUTE FUNCTION `+name+`() `); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveFocusedGraph(context.Background(), review); err == nil {
		t.Fatal("last detail failure accepted")
	}
	var count int
	if err := f.web.QueryRow(t.Context(), `SELECT count(*) FROM training_sessions WHERE id=$1`, payload.Proposal.SessionID).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial mixed graph committed", err)
	}
}

func TestFocusedGraphFreshRestLogisticsCompetitionRawScheduleMeaning(t *testing.T) {
	f := newPublicationDomainFixture(t)
	enableGraphFixture(t, f)
	store := PostgresFocusedWorkoutStore{Pool: f.web}
	for _, test := range []struct{ kind, mode, band, start, end string }{{"REST", "DAY", "", "", ""}, {"LOGISTICS", "BAND", "AFTERNOON", "", ""}, {"COMPETITION", "START_ONLY", "", "10:00", ""}, {"TRAINING", "START_ONLY", "", "11:00", "12:00"}} {
		raw := url.Values{"title": {"Compromisso explícito de teste"}, "description": {"Instruções próprias do compromisso."}, "entry_kind": {test.kind}, "schedule.mode": {test.mode}}
		if test.band != "" {
			raw.Set("schedule.band", test.band)
		}
		if test.start != "" {
			raw.Set("schedule.start_time", test.start)
		}
		if test.end != "" {
			raw.Set("schedule.end_date", "2026-11-10")
			raw.Set("schedule.end_time", test.end)
		}
		payload, review := graphRealReview(t, f, store, raw)
		result, err := store.SaveFocusedGraph(t.Context(), review)
		if err != nil {
			t.Fatal(test.kind, test.mode, err)
		}
		actual, err := readFocusedGraph(t.Context(), dbgen.New(f.web), payload.Proposal)
		if err != nil || !focusedGraphJSONEqual(actual, payload.Proposal) {
			t.Fatal("raw kind/schedule roundtrip", test.kind, err)
		}
		if test.start == "" && (actual.Schedule.StartsAt != nil || actual.Schedule.EndsAt != nil) {
			t.Fatal("untimed commitment acquired fake clock")
		}
		if test.end != "" && actual.Schedule.Precision != TrainingScheduleInterval {
			t.Fatal("actual optional endpoint not preserved")
		}
		if actual.SessionID != result.SessionID || len(actual.Activities) != 0 {
			t.Fatal("empty-content commitment changed identity/content")
		}
		artifact, err := focusedGraphArtifact(actual)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := decodeStructuredPrescription(artifact, lisbonLocation())
		if err != nil || decoded.Session.EntryKind != test.kind {
			t.Fatal("typed canonical base reader", test.kind, err)
		}
	}
}
