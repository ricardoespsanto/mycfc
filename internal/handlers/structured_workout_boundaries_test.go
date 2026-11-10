package handlers

import (
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestFocusedWaterNativeDraftRejectsForgedRemoval(t *testing.T) {
	const removal = "removed[r_1]"
	for _, tc := range []struct {
		name   string
		change func(url.Values, *focusedPhaseTombstone)
		want   error
	}{
		{"oversized draft", func(v url.Values, _ *focusedPhaseTombstone) {
			v.Set("title", strings.Repeat("a", focusedWorkoutMaxRequestBytes))
		}, errFocusedWorkoutLimit},
		{"other activity", func(v url.Values, _ *focusedPhaseTombstone) { v.Set("activity_order", "a_2") }, errFocusedWorkoutInvalid},
		{"too many phases", func(v url.Values, _ *focusedPhaseTombstone) {
			v["activities[a_1].phase_order"] = make([]string, focusedWorkoutMaxPhases+1)
		}, errFocusedWorkoutLimit},
		{"other removal kind", func(v url.Values, _ *focusedPhaseTombstone) { v.Set(removal+".kind", "activity") }, errFocusedWorkoutInvalid},
		{"removal key mismatch", func(v url.Values, _ *focusedPhaseTombstone) { v.Set(removal+".key", "r_2") }, errFocusedWorkoutInvalid},
		{"negative index", func(_ url.Values, tomb *focusedPhaseTombstone) { tomb.Index = -1 }, errFocusedWorkoutInvalid},
		{"active phase inside removal", func(_ url.Values, tomb *focusedPhaseTombstone) { tomb.Key = "p_2" }, errFocusedWorkoutInvalid},
		{"unexpected subtree field", func(_ url.Values, tomb *focusedPhaseTombstone) { tomb.Values["distance"] = "100" }, errFocusedWorkoutInvalid},
		{"stored identity in removed phase", func(_ url.Values, tomb *focusedPhaseTombstone) { tomb.Values["id"] = uuid.NewString() }, errFocusedWorkoutInvalid},
		{"stored identity in activity", func(v url.Values, _ *focusedPhaseTombstone) { v.Set("activities[a_1].id", uuid.NewString()) }, errFocusedWorkoutInvalid},
		{"stored identity in active phase", func(v url.Values, _ *focusedPhaseTombstone) { v.Set(focusedPhasePrefix("p_2")+".id", uuid.NewString()) }, errFocusedWorkoutInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, draft := focusedWorkoutFixture()
			v := focusedDraftWithRemovalFields(draft)
			v.Set("removed_order", "r_1")
			v.Set(removal+".kind", "phase")
			v.Set(removal+".key", "r_1")
			tomb := focusedPhaseTombstone{Key: "p_4", Index: 2, Values: map[string]string{"instructions": "Orientação retida."}}
			valid, err := json.Marshal(tomb)
			if err != nil {
				t.Fatal(err)
			}
			v.Set(removal+".payload", string(valid))
			if parsed, err := parseFocusedWorkoutDraft(v); err != nil || len(parsed.Removed) != 1 {
				t.Fatalf("valid removal fixture got %d removals, err=%v", len(parsed.Removed), err)
			}
			tc.change(v, &tomb)
			raw, err := json.Marshal(tomb)
			if err != nil {
				t.Fatal(err)
			}
			v.Set(removal+".payload", string(raw))
			if _, err = parseFocusedWorkoutDraft(v); !errors.Is(err, tc.want) {
				t.Fatalf("malformed draft returned %v, want %v", err, tc.want)
			}
		})
	}
}

func TestFocusedWaterInvalidCommandPreservesDraft(t *testing.T) {
	for _, command := range []string{"refresh:a_1", "phase_add:a_2", "phase_remove:a_2:p_2", "phase_up:a_1:p_99", "restore:r_1:extra", "restore:r_missing", "publish"} {
		t.Run(command, func(t *testing.T) {
			_, draft := focusedWorkoutFixture()
			before := focusedDraftWithRemovalFields(draft).Encode()
			out, err := applyFocusedWorkoutCommand(draft, command)
			if !errors.Is(err, errFocusedWorkoutInvalid) || focusedDraftWithRemovalFields(out).Encode() != before || !slices.Equal(out.Keys, draft.Keys) {
				t.Fatalf("invalid command changed the draft or returned %v", err)
			}
		})
	}
}

func TestFocusedWaterValidationReportsIncompatibleInstructions(t *testing.T) {
	phase := focusedPhasePrefix("p_2")
	for _, tc := range []struct {
		field string
		value string
		want  string
	}{
		{"entry_kind", "LOGISTICS", "entry_kind"},
		{"activities[a_1].modality", "GYM", "activities.a_1.modality"},
		{"schedule.date", "2026-11-11", "schedule.date"},
		{"schedule.mode", "DAY", "schedule.mode"},
		{"schedule.end_time", "17:00", "schedule.end_date"},
		{"schedule.band", "AFTERNOON", "schedule.band"},
		{"schedule.start_occurrence", "2026-11-10T17:00:00Z", "schedule.start_occurrence"},
		{"activities[a_1].duration_minutes", "-1", "activities.a_1.duration_minutes"},
		{phase + ".purpose", "UNRECOGNIZED", "activities.a_1.phases.p_2.purpose"},
		{phase + ".detail_mode", "INTERVALS", "activities.a_1.phases.p_2.detail_mode"},
		{phase + ".purpose", "CUSTOM", "activities.a_1.phases.p_2.title"},
		{phase + ".instructions", "", "description"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			c, draft := focusedWorkoutFixture()
			draft.Values.Set(tc.field, tc.value)
			_, fields := normalizeFocusedWater(c, draft)
			if fields[tc.want] == "" {
				t.Fatalf("%s=%q reported %v, want field %s", tc.field, tc.value, fields, tc.want)
			}
		})
	}
}

func TestFocusedWaterSignerRejectsChangedReviewInputs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*focusedWorkoutPayload)
	}{
		{"state forbids authoring", func(p *focusedWorkoutPayload) { p.Proposal.Context.AuthoringEnabled = false }},
		{"day outside week", func(p *focusedWorkoutPayload) {
			p.Proposal.Context.WeekStart = p.Proposal.Context.WeekStart.AddDate(0, 0, 7)
		}},
		{"unknown draft field", func(p *focusedWorkoutPayload) { p.Draft.Set("unknown", "x") }},
		{"empty draft title", func(p *focusedWorkoutPayload) { p.Draft.Set("title", "") }},
		{"missing session identity", func(p *focusedWorkoutPayload) { p.Proposal.SessionID = uuid.Nil }},
		{"duplicate phase identity", func(p *focusedWorkoutPayload) { p.Proposal.Phases[0].ID = p.Proposal.Activity.ID }},
		{"different instructions", func(p *focusedWorkoutPayload) { p.Proposal.Phases[0].Instructions = "Different instruction" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, draft := focusedWorkoutFixture()
			p, fields := normalizeFocusedWater(c, draft)
			if len(fields) != 0 {
				t.Fatal(fields)
			}
			allocateFocusedWaterIDs(&p)
			payload := focusedWorkoutPayload{Proposal: p, Draft: focusedDraftWithRemovalFields(draft)}
			tc.change(&payload)
			raw, token, err := focusedWorkoutReviews.sign(payload, time.Now())
			if !errors.Is(err, errFocusedWorkoutInvalid) || raw != "" || token != "" {
				t.Fatalf("changed review got payload=%t token=%t err=%v", raw != "", token != "", err)
			}
		})
	}
}

func TestFocusedReviewJSONRejectsAmbiguousOrUnboundedInput(t *testing.T) {
	for _, raw := range []string{
		`{"outer":[{"id":1,"id":2}]}`,
		`{"outer":[]} {}`,
		`{"outer":[}`,
		strings.Repeat("[", 66) + strings.Repeat("]", 66),
		string([]byte{0xff}),
	} {
		var dest map[string]any
		if err := focusedStrictJSON([]byte(raw), &dest); err == nil {
			t.Fatal("ambiguous or unbounded input reached review decoding")
		}
	}
}

func TestFocusedWaterPhaseLimitCountsDraftsHeldForUndo(t *testing.T) {
	_, draft := focusedWorkoutFixture()
	draft, err := applyFocusedWorkoutCommand(draft, "phase_remove:a_1:p_2")
	if err != nil {
		t.Fatal(err)
	}
	for len(draft.Keys)+len(draft.Removed) < focusedWorkoutMaxPhases {
		draft, err = applyFocusedWorkoutCommand(draft, "phase_add:a_1")
		if err != nil {
			t.Fatal(err)
		}
	}
	before := focusedDraftWithRemovalFields(draft).Encode()
	out, err := applyFocusedWorkoutCommand(draft, "phase_add:a_1")
	if !errors.Is(err, errFocusedWorkoutLimit) || focusedDraftWithRemovalFields(out).Encode() != before {
		t.Fatalf("phase limit discarded a draft or returned %v", err)
	}
	values := focusedDraftWithRemovalFields(draft)
	values["removed_order"] = []string{"r_1", "r_2"}
	if _, err = parseFocusedWorkoutDraft(values); !errors.Is(err, errFocusedWorkoutLimit) {
		t.Fatalf("over-limit removal submission returned %v", err)
	}
}
