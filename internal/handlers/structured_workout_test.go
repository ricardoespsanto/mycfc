package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func focusedWorkoutFixture() (FocusedWorkoutContext, focusedWorkoutDraft) {
	week := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	c := FocusedWorkoutContext{ActorID: uuid.New(), PlanID: uuid.New(), GroupID: uuid.New(), SeasonID: uuid.New(), Day: "2026-11-10", GroupName: "Grupo de água", PlanTitle: "Semana sintética", WeekStart: week, PlanUpdatedAt: week.Add(time.Hour), DatabaseIncarnation: uuid.New(), StateEpoch: 2, SchemaContract: "schedule-p1", AuthoringEnabled: true}
	d := newFocusedWorkoutDraft(c.Day)
	d.Values.Set("title", "Treino de água")
	d.Values.Set("schedule.start_time", "16:00")
	d.Values.Set(focusedPhasePrefix("p_2")+".instructions", "Remar com atenção à técnica.")
	return c, d
}
func TestFocusedWaterPlainNormalizationAndCanonicalProjection(t *testing.T) {
	c, d := focusedWorkoutFixture()
	p, errs := normalizeFocusedWater(c, d)
	if len(errs) != 0 || len(p.Phases) != 1 || p.Schedule.Precision != TrainingScheduleStartOnly || p.Schedule.EndsAt != nil {
		t.Fatalf("plain normalization %+v %v", p, errs)
	}
	allocateFocusedWaterIDs(&p)
	view := projectFocusedWater(p)
	if len(view.Segments) != 1 || len(view.Segments[0].Blocks) != 1 || view.Segments[0].Blocks[0].Instructions != p.Phases[0].Instructions || view.Schedule.EndsAt != "" {
		t.Fatalf("projection %+v", view)
	}
	raw, err := focusedWaterArtifact(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeStructuredPrescription(raw, lisbonLocation())
	if err != nil || decoded.Session.Schedule.Precision != "START_ONLY" || decoded.Session.Schedule.EndsAt != "" {
		t.Fatal("typed canonical base artifact roundtrip", err)
	}
	if strings.Contains(string(raw), `"EntryKind"`) {
		t.Fatal("v2 kind is ambiguous")
	}
	d.Values.Set("schedule.end_date", c.Day)
	d.Values.Set("schedule.end_time", "17:00")
	interval, errs := normalizeFocusedWater(c, d)
	if len(errs) != 0 || interval.Schedule.Precision != TrainingScheduleInterval {
		t.Fatal("explicit optional end not derived", errs)
	}
}
func TestFocusedWaterDSTOccurrenceAndExactWeekBoundary(t *testing.T) {
	if got := focusedClockOccurrences("2026-03-29", "01:30"); len(got) != 0 {
		t.Fatal("Lisbon gap invented", got)
	}
	got := focusedClockOccurrences("2026-10-25", "01:30")
	if len(got) != 2 || got[0].Equal(got[1]) {
		t.Fatal("Lisbon fold not explicit", got)
	}
	c, d := focusedWorkoutFixture()
	c.Day = "2026-10-25"
	c.WeekStart = time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	d.Values.Set("schedule.date", c.Day)
	d.Values.Set("schedule.start_time", "01:30")
	_, errs := normalizeFocusedWater(c, d)
	if errs["schedule.start_occurrence"] == "" {
		t.Fatal("fold silently selected")
	}
	d.Values.Set("schedule.start_occurrence", got[1].Format(time.RFC3339Nano))
	_, errs = normalizeFocusedWater(c, d)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	c, d = focusedWorkoutFixture()
	c.Day = "2026-11-15"
	d.Values.Set("schedule.date", c.Day)
	d.Values.Set("schedule.start_time", "23:00")
	d.Values.Set("schedule.end_date", "2026-11-16")
	d.Values.Set("schedule.end_time", "00:00")
	if _, e := normalizeFocusedWater(c, d); len(e) != 0 {
		t.Fatal("exact next Monday boundary refused", e)
	}
	d.Values.Set("schedule.end_time", "00:01")
	if _, e := normalizeFocusedWater(c, d); e["schedule.end_date"] == "" {
		t.Fatal("later than week boundary accepted")
	}
}
func TestFocusedWaterNativeCommandsRetainIncompleteInputAndRestoreOrder(t *testing.T) {
	_, d := focusedWorkoutFixture()
	d.Values.Set("activities[a_1].duration_minutes", "1.")
	d.Values.Set(focusedPhasePrefix("p_1")+".instructions", "Texto não guardado")
	original, _ := json.Marshal(focusedDraftWithRemovalFields(d))
	removed, err := applyFocusedWorkoutCommand(d, "phase_remove:a_1:p_1")
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(focusedDraftWithRemovalFields(d))
	if string(original) != string(after) {
		t.Fatal("command mutated original retained draft")
	}
	parsed, err := parseFocusedWorkoutDraft(focusedDraftWithRemovalFields(removed))
	if err != nil {
		t.Fatal("native tombstone rejected", err)
	}
	restored, err := applyFocusedWorkoutCommand(parsed, "restore:"+parsed.Removed[0].Key)
	if err != nil || restored.Keys[0] != "p_1" || restored.Values.Get(focusedPhasePrefix("p_1")+".instructions") != "Texto não guardado" || restored.Values.Get("activities[a_1].duration_minutes") != "1." {
		t.Fatal("restore lost raw input/order", err)
	}
	for _, command := range []string{"activity_add", "phase_remove:a_2:p_1", "water_effort_add:a_1:p_1:_root", "restore:foreign", "phase_up:a_1:foreign"} {
		if _, err = applyFocusedWorkoutCommand(d, command); err == nil {
			t.Fatal("unsupported/foreign command", command)
		}
	}
}
func TestFocusedWaterDraftRejectsSubstitutionAndDuplicates(t *testing.T) {
	_, d := focusedWorkoutFixture()
	raw := focusedDraftWithRemovalFields(d)
	for _, mutate := range []func(url.Values){func(v url.Values) { v.Add("title", "duplicate") }, func(v url.Values) { v.Set("activities[a_1].id", uuid.NewString()) }, func(v url.Values) { v.Add("activity_order", "a_2") }, func(v url.Values) { v.Add("activities[a_1].phase_order", "p_1") }, func(v url.Values) { v.Set("foreign", "x") }} {
		copy := url.Values{}
		for k, v := range raw {
			copy[k] = append([]string(nil), v...)
		}
		mutate(copy)
		if _, err := parseFocusedWorkoutDraft(copy); err == nil {
			t.Fatal("invalid draft accepted")
		}
	}
}
func TestFocusedWaterSignedReviewIntegrityAndEncodedBounds(t *testing.T) {
	c, d := focusedWorkoutFixture()
	p, errs := normalizeFocusedWater(c, d)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	allocateFocusedWaterIDs(&p)
	payload := focusedWorkoutPayload{p, focusedDraftWithRemovalFields(d)}
	now := time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)
	signer := focusedWorkoutReviews
	raw, token, err := signer.sign(payload, now)
	if err != nil {
		t.Fatal(err)
	}
	_, review, err := signer.verify(raw, token, c.ActorID, c.PlanID, now.Add(time.Minute))
	if err != nil || !review.verified {
		t.Fatal("own review rejected", err)
	}
	for _, bad := range []string{token + "\n", strings.Replace(token, ".", ".\r\n", 1), token[:len(token)-1] + "!"} {
		if _, _, err = signer.verify(raw, bad, c.ActorID, c.PlanID, now); err == nil {
			t.Fatal("noncanonical token accepted")
		}
	}
	if _, _, err = signer.verify(raw, token, uuid.New(), c.PlanID, now); err == nil {
		t.Fatal("crossactor")
	}
	if _, _, err = signer.verify(raw+" ", token, c.ActorID, c.PlanID, now); err == nil {
		t.Fatal("changed raw payload")
	}
	if _, _, err = signer.verify(raw, token, c.ActorID, c.PlanID, now.Add(focusedWorkoutReviewTTL)); err == nil {
		t.Fatal("expired")
	}
	other := focusedWorkoutSigner{}
	if _, _, err = other.verify(raw, token, c.ActorID, c.PlanID, now); err == nil {
		t.Fatal("process restart key")
	}
	bad := strings.Replace(raw, `"title":"Treino de água"`, `"title":"changed"`, 1)
	if _, _, err = signer.verify(bad, token, c.ActorID, c.PlanID, now); err == nil {
		t.Fatal("substituted title")
	}
	if strings.Contains(token, "Treino") || strings.Contains(token, c.ActorID.String()) {
		t.Fatal("token exposes content")
	}
}

func TestFocusedWaterEncodedConfirmAndPriorEditorAreBothBounded(t *testing.T) {
	c, d := focusedWorkoutFixture()
	d.Keys = nil
	for i := 0; i < 32; i++ {
		key := "p_" + strconv.Itoa(i+1)
		d.Keys = append(d.Keys, key)
		d.Values.Set(focusedPhasePrefix(key)+".purpose", "MAIN")
		d.Values.Set(focusedPhasePrefix(key)+".instructions", strings.Repeat("'", 3200))
	}
	d.Values["activities[a_1].phase_order"] = append([]string(nil), d.Keys...)
	makePayload := func() focusedWorkoutPayload {
		p, errs := normalizeFocusedWater(c, d)
		if len(errs) != 0 {
			t.Fatal(errs)
		}
		allocateFocusedWaterIDs(&p)
		return focusedWorkoutPayload{p, focusedDraftWithRemovalFields(d)}
	}
	now := time.Now()
	raw, token, err := focusedWorkoutReviews.sign(makePayload(), now)
	if err != nil {
		t.Fatal("bounded populated proposal rejected", err)
	}
	confirm := url.Values{"intent": {"confirm"}, "proposal": {raw}, "review_token": {token}}
	editor := focusedDraftWithRemovalFields(d)
	editor.Set("prior_proposal", raw)
	editor.Set("prior_review_token", token)
	if len(confirm.Encode()) > focusedWorkoutMaxRequestBytes || len(editor.Encode()) > focusedWorkoutMaxRequestBytes {
		t.Fatal("signed proposal cannot roundtrip native transport")
	}
	for _, key := range d.Keys {
		d.Values.Set(focusedPhasePrefix(key)+".instructions", strings.Repeat("'", 4000))
	}
	if _, _, err = focusedWorkoutReviews.sign(makePayload(), now); !errors.Is(err, errFocusedWorkoutLimit) {
		t.Fatal("oversized encoded editback accepted", err)
	}
}

func TestFocusedWaterOrdinaryEditorAndEditBackDoNotRequireOccurrence(t *testing.T) {
	c, d := focusedWorkoutFixture()
	d.Values.Set("schedule.end_date", c.Day)
	d.Values.Set("schedule.end_time", "16:55")
	h := StructuredWorkout{}
	r := httptest.NewRequest("GET", "/", nil)
	for _, stage := range []string{"editor", "signed-editback"} {
		if stage == "signed-editback" {
			p, errs := normalizeFocusedWater(c, d)
			if len(errs) > 0 {
				t.Fatal(errs)
			}
			allocateFocusedWaterIDs(&p)
			raw, token, err := focusedWorkoutReviews.sign(focusedWorkoutPayload{p, focusedDraftWithRemovalFields(d)}, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			payload, _, err := focusedWorkoutReviews.verify(raw, token, c.ActorID, c.PlanID, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			d = focusedDraftFromPayload(payload)
		}
		page := h.page(r, CurrentUser{ID: c.ActorID}, c, d)
		if len(page.StartOccurrences) != 0 || len(page.EndOccurrences) != 0 {
			t.Fatal(stage, "ordinary local time offered DST choice")
		}
		var body bytes.Buffer
		if err := pages.StructuredWorkoutTask(page).Render(t.Context(), &body); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(body.String(), `<select id="workout-schedule-start_occurrence"`) || strings.Contains(body.String(), `<select id="workout-schedule-end_occurrence"`) {
			t.Fatal(stage, "native occurrence selection blocks ordinary time")
		}
	}
	c.Day = "2026-10-25"
	c.WeekStart = time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	d.Values.Set("schedule.date", c.Day)
	d.Values.Set("schedule.start_time", "01:30")
	d.Values.Set("schedule.end_date", c.Day)
	d.Values.Set("schedule.end_time", "02:30")
	choices := focusedOccurrenceChoices(c.Day, "01:30")
	if len(choices) != 2 {
		t.Fatal("actualfoldchoicesmissing")
	}
	d.Values.Set("schedule.start_occurrence", choices[1].Value)
	page := h.page(r, CurrentUser{ID: c.ActorID}, c, d)
	if len(page.StartOccurrences) != 2 || len(page.EndOccurrences) != 0 {
		t.Fatal("fold versus ordinary end choices", page.StartOccurrences, page.EndOccurrences)
	}
	p, errs := normalizeFocusedWater(c, d)
	if len(errs) != 0 || p.Schedule.StartsAt == nil || p.Schedule.StartsAt.Format(time.RFC3339Nano) != choices[1].Value {
		t.Fatal("explicit actual fold occurrence lost", errs)
	}
	var body bytes.Buffer
	if err := pages.StructuredWorkoutTask(page).Render(t.Context(), &body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body.String(), `value="`+choices[1].Value+`" selected`) {
		t.Fatal("selected fold not retained in editor")
	}
}

type focusedRecoveryStore struct {
	source       FocusedWorkoutContext
	sizes, saves int
}

func (s *focusedRecoveryStore) LoadFocusedWorkout(context.Context, FocusedWorkoutRequest) (FocusedWorkoutContext, error) {
	return s.source, nil
}
func (s *focusedRecoveryStore) FocusedWorkoutArtifactSize(context.Context, FocusedWorkoutRequest, []byte) (int32, error) {
	s.sizes++
	return 1, nil
}
func (s *focusedRecoveryStore) SaveFocusedWorkout(context.Context, focusedWorkoutReviewed) (FocusedWorkoutSaveResult, error) {
	s.saves++
	return FocusedWorkoutSaveResult{}, errors.New("unexpected recovery save")
}

// Real task routing/ingress/rendering with a fake persistence seam: only proof
// expiry/restart/tamper are simulated. Full SCS/CSRF is separately proven by the
// actual app integration test; these tests do not claim authenticated DB proof.
func TestFocusedWaterUnusableProofRecoveryRetainsCopyableRawWithoutFutilePreview(t *testing.T) {
	c, d := focusedWorkoutFixture()
	own := "Texto recuperável\n\tsem guardar."
	d.Values.Set("description", own)
	p, errs := normalizeFocusedWater(c, d)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	allocateFocusedWaterIDs(&p)
	payload := focusedWorkoutPayload{p, focusedDraftWithRemovalFields(d)}
	for _, bad := range []string{"expired", "restarted", "invalid"} {
		t.Run(bad, func(t *testing.T) {
			signer := focusedWorkoutReviews
			issued := time.Now()
			if bad == "expired" {
				issued = issued.Add(-focusedWorkoutReviewTTL - time.Second)
			}
			if bad == "restarted" {
				signer.key[0] ^= 1
			}
			raw, token, err := signer.sign(payload, issued)
			if err != nil {
				t.Fatal(err)
			}
			if bad == "invalid" {
				token += "!"
			}
			source := c
			source.PlanUpdatedAt = source.PlanUpdatedAt.Add(time.Hour)
			store := &focusedRecoveryStore{source: source}
			h := StructuredWorkout{Store: store, HTTPWriteTimeout: 30 * time.Second}
			base := "/admin/treinos/estruturados/semanas/" + c.PlanID.String() + "/treinos/criar"
			mux := http.NewServeMux()
			mux.HandleFunc("POST "+base+"/confirmar", func(w http.ResponseWriter, r *http.Request) { r.SetPathValue("id", c.PlanID.String()); h.Confirm(w, r) })
			mux.HandleFunc("POST "+base+"/prever", func(w http.ResponseWriter, r *http.Request) { r.SetPathValue("id", c.PlanID.String()); h.Preview(w, r) })
			mux.HandleFunc("POST "+base+"/rascunho", func(w http.ResponseWriter, r *http.Request) { r.SetPathValue("id", c.PlanID.String()); h.Draft(w, r) })
			wrapped := httpx.PublicationIngressMiddleware(30 * time.Second)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: c.ActorID}))
				mux.ServeHTTP(w, r)
			}))
			editor := focusedDraftWithRemovalFields(d)
			editor.Set("prior_proposal", raw)
			editor.Set("prior_review_token", token)
			for k, v := range map[string]string{"group_id": c.GroupID.String(), "plan_id": c.PlanID.String(), "day": c.Day, "expected_plan_updated_at": c.PlanUpdatedAt.Format(time.RFC3339Nano), "source_digest": c.Digest(), "database_incarnation": c.DatabaseIncarnation.String(), "state_epoch": strconv.FormatInt(c.StateEpoch, 10)} {
				editor.Set(k, v)
			}
			for _, action := range []string{"confirmar", "prever", "rascunho"} {
				values := editor
				values.Set("command", "preview")
				if action == "rascunho" {
					values.Set("command", "phase_add:a_1")
				}
				if action == "confirmar" {
					values = url.Values{"intent": {"confirm"}, "proposal": {raw}, "review_token": {token}}
				}
				response := publicationPost(t, wrapped, base+"/"+action, values)
				body := response.Body.String()
				if response.Code != 422 || !strings.Contains(body, "Consulte primeiro a semana") || !strings.Contains(body, `<textarea id="workout-description"`) || !strings.Contains(html.UnescapeString(body), own) {
					t.Fatal(action, "no visible raw recovery", response.Code)
				}
				if regexp.MustCompile(`<button\b[^>]*value="preview"`).MatchString(body) || strings.Contains(body, `name="intent" value="confirm"`) {
					t.Fatal(action, "unusable proof offered preview/save")
				}
				if !strings.Contains(body, `name="prior_proposal" value="`+html.EscapeString(raw)+`"`) || !strings.Contains(body, `name="prior_review_token" value="`+html.EscapeString(token)+`"`) {
					t.Fatal(action, "lost create identity proof")
				}
				if !strings.Contains(body, `expected_plan_updated_at" value="`+c.PlanUpdatedAt.Format(time.RFC3339Nano)+`"`) || strings.Contains(body, `phases[p_4]`) {
					t.Fatal(action, "recovery refreshed version/applied draft command")
				}
				if !strings.Contains(html.UnescapeString(body), "day="+c.Day) || !strings.Contains(html.UnescapeString(body), "week_id="+c.PlanID.String()) {
					t.Fatal(action, "missing exact week recovery")
				}
			}
			if store.saves != 0 || store.sizes != 0 {
				t.Fatal("unusable review attempted mutation/new preview")
			}
		})
	}
}
