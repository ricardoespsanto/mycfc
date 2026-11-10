package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"math"
	"net/url"
	"strings"
	"testing"
	"time"
)

func focusedGraphFixture() url.Values {
	return url.Values{
		"title": {"Treino misto"}, "entry_kind": {"TRAINING"}, "schedule.mode": {"START_ONLY"}, "schedule.date": {"2026-11-10"}, "schedule.start_time": {"16:00"},
		"activity_order": {"a_1", "a_2"}, "activities[a_1].modality": {"WATER"}, "activities[a_1].phase_order": {"p_1"}, "activities[a_1].duration_minutes": {"1."},
		"activities[a_1].phases[p_1].purpose": {"MAIN"}, "activities[a_1].phases[p_1].instructions": {"Texto próprio\n\tcom pausa."}, "activities[a_1].phases[p_1].detail_mode": {"WATER"},
		"activities[a_1].phases[p_1].water.node_order":      {"n_1", "n_2", "n_3"},
		"activities[a_1].phases[p_1].water.nodes[n_1].kind": {"REPEAT_GROUP"}, "activities[a_1].phases[p_1].water.nodes[n_1].repeats": {"10"},
		"activities[a_1].phases[p_1].water.nodes[n_2].parent_key": {"n_1"}, "activities[a_1].phases[p_1].water.nodes[n_2].kind": {"EFFORT"}, "activities[a_1].phases[p_1].water.nodes[n_2].name": {"Técnica livre"},
		"activities[a_1].phases[p_1].water.nodes[n_3].kind": {"EFFORT"},
		"activities[a_2].modality":                          {"GYM"}, "activities[a_2].phase_order": {"p_2"}, "activities[a_2].phases[p_2].gym.exercise_order": {"e_1"}, "activities[a_2].phases[p_2].gym.exercises[e_1].resistance_value": {"75,"},
	}
}
func TestFocusedGraphNativeCommandsRestoreWholeSubtreesAndRaw(t *testing.T) {
	original, err := parseFocusedGraph(focusedGraphFixture())
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(focusedGraphValues(original))
	for _, command := range []string{"activity_remove:a_1", "phase_remove:a_1:p_1", "water_remove:a_1:p_1:n_1", "water_remove:a_1:p_1:n_2", "gym_remove:a_2:p_2:e_1"} {
		t.Run(command, func(t *testing.T) {
			removed, e := applyFocusedGraphCommand(original, command)
			if e != nil {
				t.Fatal(e)
			}
			if len(removed.Removed) != 1 {
				t.Fatal("missing tombstone")
			}
			restored, e := applyFocusedGraphCommand(removed, "restore:"+removed.Removed[0].Key)
			if e != nil {
				t.Fatal(e)
			}
			after, _ := json.Marshal(focusedGraphValues(restored))
			if string(before) != string(after) {
				t.Fatalf("raw/order changed on restore\nbefore%s\nafter%s", before, after)
			}
		})
	}
	after, _ := json.Marshal(focusedGraphValues(original))
	if string(before) != string(after) {
		t.Fatal("native command mutated original on failure/success")
	}
	moved, err := applyFocusedGraphCommand(original, "water_down:a_1:p_1:n_1")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(moved.Values["activities[a_1].phases[p_1].water.node_order"], ","); got != "n_3,n_1,n_2" {
		t.Fatal("did not move whole sibling subtree", got)
	}
	for _, command := range []string{"activity_add", "phase_add:a_1", "water_effort_add:a_1:p_1:n_1", "water_group_add:a_1:p_1:_root", "gym_add:a_2:p_2", "activity_down:a_1", "phase_up:a_1:foreign"} {
		changed, e := applyFocusedGraphCommand(original, command)
		if strings.Contains(command, "foreign") {
			if e == nil {
				t.Fatal("foreign command accepted")
			}
			continue
		}
		if e != nil {
			t.Fatal(command, e)
		}
		if changed.Values.Get("activities[a_1].duration_minutes") != "1." || changed.Values.Get("activities[a_2].phases[p_2].gym.exercises[e_1].resistance_value") != "75," {
			t.Fatal("raw incomplete numbers lost", command)
		}
	}
}
func TestFocusedGraphRejectsForeignDuplicateCycleAndNonpreorder(t *testing.T) {
	for _, mutate := range []func(url.Values){
		func(v url.Values) { v.Add("title", "duplicate") },
		func(v url.Values) {
			v.Set("activities[a_1].phases[p_1].water.nodes[n_2].id", "11111111-1111-4111-8111-111111111111")
		},
		func(v url.Values) { v.Set("activities[a_1].phases[p_1].water.nodes[n_2].parent_key", "foreign") },
		func(v url.Values) { v.Set("activities[a_1].phases[p_1].water.nodes[n_1].parent_key", "n_2") },
		func(v url.Values) { v["activities[a_1].phases[p_1].water.node_order"] = []string{"n_2", "n_1", "n_3"} },
		func(v url.Values) { v.Set("activities[a_2].phases[p_2].gym.exercises[foreign].name", "foreign") },
	} {
		v := focusedGraphFixture()
		mutate(v)
		if _, err := parseFocusedGraph(v); err == nil {
			t.Fatal("invalid topology/identity accepted")
		}
	}
	d, err := parseFocusedGraph(focusedGraphFixture())
	if err != nil {
		t.Fatal(err)
	}
	removed, err := applyFocusedGraphCommand(d, "water_remove:a_1:p_1:n_1")
	if err != nil {
		t.Fatal(err)
	}
	// An active node must not also be hidden in a tombstone.
	raw := focusedGraphValues(removed)
	raw["activities[a_1].phases[p_1].water.node_order"] = []string{"n_1", "n_3"}
	raw.Set("activities[a_1].phases[p_1].water.nodes[n_1].kind", "EFFORT")
	if _, err = parseFocusedGraph(raw); err == nil {
		t.Fatal("active+removed node collision accepted")
	}
}

func focusedGraphWaterExample() (FocusedWorkoutContext, url.Values) {
	c, _ := focusedWorkoutFixture()
	v := url.Values{"title": {"Intervalos construídos para teste"}, "entry_kind": {"TRAINING"}, "schedule.date": {c.Day}, "schedule.mode": {"START_ONLY"}, "schedule.start_time": {"16:00"}, "activity_order": {"a_1"}, "activities[a_1].modality": {"WATER"}, "activities[a_1].phase_order": {"p_1"}, "activities[a_1].phases[p_1].purpose": {"MAIN"}, "activities[a_1].phases[p_1].instructions": {"Manter a técnica definida pelo treinador."}, "activities[a_1].phases[p_1].detail_mode": {"WATER"}, "activities[a_1].phases[p_1].water.method": {"INTERVALS"}, "activities[a_1].phases[p_1].water.node_order": {"n_1", "n_2"}, "activities[a_1].phases[p_1].water.nodes[n_1].kind": {"REPEAT_GROUP"}, "activities[a_1].phases[p_1].water.nodes[n_1].name": {"Dez repetições"}, "activities[a_1].phases[p_1].water.nodes[n_1].repeats": {"10"}, "activities[a_1].phases[p_1].water.nodes[n_1].recovery_seconds": {"120"}, "activities[a_1].phases[p_1].water.nodes[n_2].parent_key": {"n_1"}, "activities[a_1].phases[p_1].water.nodes[n_2].kind": {"EFFORT"}, "activities[a_1].phases[p_1].water.nodes[n_2].name": {"Quatro minutos"}, "activities[a_1].phases[p_1].water.nodes[n_2].duration_seconds": {"240"}, "activities[a_1].phases[p_1].water.nodes[n_2].duration_certainty": {"EXACT"}, "activities[a_1].phases[p_1].water.nodes[n_2].intensity_code": {"R2"}}
	return c, v
}
func normalizedGraphExample(t *testing.T, c FocusedWorkoutContext, v url.Values) focusedWorkoutGraph {
	t.Helper()
	d, err := parseFocusedGraph(v)
	if err != nil {
		t.Fatal(err)
	}
	p, e := normalizeFocusedGraph(c, d)
	if len(e) > 0 {
		t.Fatal(e)
	}
	return p
}
func focusedTotalValue(totals []pages.StructuredWaterTotal, label string) pages.StructuredWaterTotal {
	for _, v := range totals {
		if v.Label == label {
			return v
		}
	}
	return pages.StructuredWaterTotal{}
}
func TestFocusedGraphConstructedWaterRecoveryAndTargetSemantics(t *testing.T) {
	c, v := focusedGraphWaterExample()
	p := normalizedGraphExample(t, c, v)
	totals, err := focusedGraphCheckedTotals(p)
	if err != nil {
		t.Fatal(err)
	}
	if focusedTotalValue(totals, "Esforço planeado").Value != "40 min" || focusedTotalValue(totals, "Recuperação planeada").Value != "18 min" || focusedTotalValue(totals, "Tempo em R2").Value != "40 min" {
		t.Fatal("constructed group BETWEEN recovery changed", totals)
	}
	v.Del("activities[a_1].phases[p_1].water.nodes[n_1].recovery_seconds")
	v.Set("activities[a_1].phases[p_1].water.nodes[n_2].recovery_seconds", "120")
	p = normalizedGraphExample(t, c, v)
	totals, err = focusedGraphCheckedTotals(p)
	if err != nil || focusedTotalValue(totals, "Recuperação planeada").Value != "20 min" {
		t.Fatal("leaf AFTER EACH recovery changed", totals, err)
	}
	v.Set("activities[a_1].phases[p_1].water.target_distance_metres", "8000")
	v.Set("activities[a_1].phases[p_1].water.target_distance_certainty", "EXACT")
	p = normalizedGraphExample(t, c, v)
	totals, err = focusedGraphCheckedTotals(p)
	if err != nil || p.Activities[0].Phases[0].Water.TargetDistance == nil || focusedTotalValue(totals, "Distância planeada").Value != "Desconhecido" || p.Schedule.EndsAt != nil {
		t.Fatal("continuation target invented distance/end", totals, err)
	}
	v.Set("activities[a_1].phases[p_1].water.nodes[n_2].distance_metres", "500")
	v.Set("activities[a_1].phases[p_1].water.nodes[n_2].distance_certainty", "ESTIMATED")
	p = normalizedGraphExample(t, c, v)
	totals, _ = focusedGraphCheckedTotals(p)
	distance := focusedTotalValue(totals, "Distância planeada")
	if distance.Certainty != "estimado" || distance.Value != "5 km" {
		t.Fatal("target added as extra distance or certainty lost", distance)
	}
}
func TestFocusedGraphNestedRecoveryAndCheckedOverflow(t *testing.T) {
	c, v := focusedGraphWaterExample()
	wp := "activities[a_1].phases[p_1].water"
	v[wp+".node_order"] = []string{"n_1", "n_2", "n_3"}
	v.Set(wp+".nodes[n_1].repeats", "3")
	v.Set(wp+".nodes[n_1].recovery_seconds", "60")
	v.Set(wp+".nodes[n_2].kind", "REPEAT_GROUP")
	v.Set(wp+".nodes[n_2].repeats", "2")
	v.Set(wp+".nodes[n_2].recovery_seconds", "20")
	v.Del(wp + ".nodes[n_2].duration_seconds")
	v.Del(wp + ".nodes[n_2].duration_certainty")
	v.Set(wp+".nodes[n_3].parent_key", "n_2")
	v.Set(wp+".nodes[n_3].kind", "EFFORT")
	v.Set(wp+".nodes[n_3].name", "Esforço curto")
	v.Set(wp+".nodes[n_3].duration_seconds", "10")
	v.Set(wp+".nodes[n_3].duration_certainty", "EXACT")
	v.Set(wp+".nodes[n_3].recovery_seconds", "5")
	p := normalizedGraphExample(t, c, v)
	totals, err := focusedGraphCheckedTotals(p)
	if err != nil || focusedTotalValue(totals, "Esforço planeado").Value != "1 min" || focusedTotalValue(totals, "Recuperação planeada").Value != "3 min 30 s" {
		t.Fatal("inner/outer/leaf recovery changed", totals, err)
	}
	// Seven valid nested100x groups followed by two bounded leaves fit depth8,
	// yet their sum exceeds int64. Refuse the entire task, not wrapped totals.
	nodes := []focusedGraphWaterNodeInput{}
	var parent string
	for i := 1; i <= 7; i++ {
		key := fmt.Sprintf("n_%d", i)
		r := int32(100)
		nodes = append(nodes, focusedGraphWaterNodeInput{Key: key, ParentKey: parent, Kind: "REPEAT_GROUP", Repeats: &r})
		parent = key
	}
	duration := int32(86400)
	for i := 8; i <= 9; i++ {
		nodes = append(nodes, focusedGraphWaterNodeInput{Key: fmt.Sprintf("n_%d", i), ParentKey: parent, Kind: "EFFORT", Duration: &duration})
	}
	p.Activities[0].Phases[0].Water.Nodes = nodes
	if _, err = focusedGraphCheckedTotals(p); err == nil {
		t.Fatal("leaf sum overflow accepted")
	}
	// External invalid/overflow rows use the same arithmetic and become unknown.
	steps := []pages.StructuredWaterStep{{ID: "g", Kind: "REPEAT_GROUP", Repeats: math.MaxInt}, {ID: "e", ParentID: "g", Kind: "EFFORT", DurationSeconds: 86400}}
	for _, total := range calculateWaterTotals(steps) {
		if total.Value != "Desconhecido" {
			t.Fatal("external malformed graph displayed numeric totals", total)
		}
	}
}
func TestFocusedGraphGymRunKindsAndContradictoryRetention(t *testing.T) {
	c, _ := focusedWorkoutFixture()
	v := url.Values{"title": {"Ginásio e corrida"}, "entry_kind": {"TRAINING"}, "schedule.date": {c.Day}, "schedule.mode": {"INTERVAL"}, "schedule.start_time": {"18:00"}, "schedule.end_date": {c.Day}, "schedule.end_time": {"19:30"}, "activity_order": {"a_1", "a_2"}, "activities[a_1].modality": {"GYM"}, "activities[a_1].phase_order": {"p_1"}, "activities[a_1].phases[p_1].purpose": {"MAIN"}, "activities[a_1].phases[p_1].instructions": {"Força com execução controlada."}, "activities[a_1].phases[p_1].detail_mode": {"GYM"}, "activities[a_1].phases[p_1].gym.structure": {"CIRCUIT"}, "activities[a_1].phases[p_1].gym.objective": {"STRENGTH_ENDURANCE"}, "activities[a_1].phases[p_1].gym.rounds": {"3"}, "activities[a_1].phases[p_1].gym.exercise_order": {"e_1"}, "activities[a_1].phases[p_1].gym.exercises[e_1].name": {"Agachamento"}, "activities[a_1].phases[p_1].gym.exercises[e_1].sets": {"2"}, "activities[a_1].phases[p_1].gym.exercises[e_1].repetitions": {"12"}, "activities[a_1].phases[p_1].gym.exercises[e_1].resistance_kind": {"PERCENT_1RM"}, "activities[a_1].phases[p_1].gym.exercises[e_1].resistance_value": {"70"}, "activities[a_2].modality": {"RUN"}, "activities[a_2].phase_order": {"p_2"}, "activities[a_2].phases[p_2].purpose": {"COOL_DOWN"}, "activities[a_2].phases[p_2].instructions": {"Corrida leve nas próprias palavras."}}
	p := normalizedGraphExample(t, c, v)
	if len(p.Activities) != 2 || p.Activities[0].Phases[0].Gym.Exercises[0].ResistanceValue == nil || p.Activities[1].Phases[0].Gym != nil {
		t.Fatal("mixed identity/detail changed")
	}
	v.Set("activities[a_1].phases[p_1].gym.structure", "SUPERSET")
	_ = normalizedGraphExample(t, c, v)
	v.Set("activities[a_1].phases[p_1].gym.exercises[e_1].resistance_value", "NaN")
	d, err := parseFocusedGraph(v)
	if err != nil {
		t.Fatal(err)
	}
	_, e := normalizeFocusedGraph(c, d)
	if e["activities.a_1.phases.p_1.gym.exercises.e_1.resistance_value"] == "" {
		t.Fatal("nonfinite load accepted")
	}
	for _, kind := range []string{"REST", "LOGISTICS"} {
		for _, mode := range []string{"DAY", "BAND", "START_ONLY", "INTERVAL"} {
			raw := url.Values{"title": {"Compromisso explícito"}, "entry_kind": {kind}, "schedule.date": {c.Day}, "schedule.mode": {mode}}
			if mode == "BAND" {
				raw.Set("schedule.band", "AFTERNOON")
			}
			if mode == "START_ONLY" || mode == "INTERVAL" {
				raw.Set("schedule.start_time", "10:00")
			}
			if mode == "INTERVAL" {
				raw.Set("schedule.end_date", c.Day)
				raw.Set("schedule.end_time", "11:00")
			}
			p = normalizedGraphExample(t, c, raw)
			if len(p.Activities) != 0 || p.Schedule.Precision != TrainingSchedulePrecision(mode) {
				t.Fatal("kind/time meaning changed")
			}
		}
	}
	c2, raw := focusedGraphWaterExample()
	raw.Set("entry_kind", "COMPETITION")
	_ = normalizedGraphExample(t, c2, raw)
	raw.Set("schedule.mode", "DAY")
	draft, err := parseFocusedGraph(raw)
	if err != nil {
		t.Fatal(err)
	}
	_, e = normalizeFocusedGraph(c2, draft)
	if e["schedule.mode"] == "" || e["schedule.start_time"] == "" || draft.Values.Get("schedule.start_time") != "16:00" {
		t.Fatal("contradictory kind/time silently dropped", e)
	}
}

func TestFocusedGraphCanonicalProjectionReviewAndStableCreateIdentity(t *testing.T) {
	c, v := focusedGraphWaterExample()
	p := normalizedGraphExample(t, c, v)
	allocateFocusedGraphIDs(&p, nil)
	d, err := parseFocusedGraph(v)
	if err != nil {
		t.Fatal(err)
	}
	raw, token, err := focusedWorkoutReviews.signGraph(focusedGraphReviewPayload{p, focusedGraphValues(d)}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	payload, review, err := focusedWorkoutReviews.verifyGraph(raw, token, c.ActorID, c.PlanID, time.Now())
	if err != nil || !review.verified {
		t.Fatal(err)
	}
	view, err := projectFocusedGraph(payload.Proposal)
	if err != nil || len(view.Segments) != 1 || len(view.Segments[0].Blocks[0].WaterSteps) != 2 || view.Schedule.EndsAt != "" {
		t.Fatal("canonical graph projection", err)
	}
	artifact, err := focusedGraphArtifact(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeStructuredPrescription(artifact, lisbonLocation())
	if err != nil || len(decoded.Session.Segments[0].Blocks[0].WaterSteps) != 2 {
		t.Fatal("baseartifact typed roundtrip", err)
	}
	// Explicit re-preview may refresh current version but keeps every retained
	// scoped key's allocated identity; no new duplicate task or frozen rewrite.
	c.PlanUpdatedAt = c.PlanUpdatedAt.Add(time.Minute)
	again := normalizedGraphExample(t, c, v)
	allocateFocusedGraphIDs(&again, &p)
	if again.SessionID != p.SessionID || again.Activities[0].ID != p.Activities[0].ID || again.Activities[0].Phases[0].Water.Nodes[1].ID != p.Activities[0].Phases[0].Water.Nodes[1].ID {
		t.Fatal("re-preview changed create identities")
	}
	if _, _, err = focusedWorkoutReviews.verify(raw, token, c.ActorID, c.PlanID, time.Now()); err == nil {
		t.Fatal("graph proof accepted for different purpose")
	}
	duplicate := payload
	duplicate.Proposal.Activities[0].ID = duplicate.Proposal.SessionID
	if _, _, err = focusedWorkoutReviews.signGraph(duplicate, time.Now()); err == nil {
		t.Fatal("duplicate allocated IDs accepted")
	}
}

func TestFocusedGraphWholeTaskCountsIncludeRemovals(t *testing.T) {
	c, v := focusedGraphWaterExample()
	wp := "activities[a_1].phases[p_1].water"
	v[wp+".node_order"] = nil
	for k := range v {
		if strings.Contains(k, ".water.nodes[") {
			delete(v, k)
		}
	}
	for i := 1; i <= 256; i++ {
		key := fmt.Sprintf("n_%d", i)
		v.Add(wp+".node_order", key)
		np := wp + ".nodes[" + key + "]"
		v.Set(np+".kind", "EFFORT")
		v.Set(np+".name", "Livre")
		v.Set(np+".instructions", "Orientação própria sem medidas inventadas.")
	}
	p := normalizedGraphExample(t, c, v)
	if len(p.Activities[0].Phases[0].Water.Nodes) != 256 {
		t.Fatal("exact task bound truncated")
	}
	d, err := parseFocusedGraph(v)
	if err != nil {
		t.Fatal(err)
	}
	removed, err := applyFocusedGraphCommand(d, "water_remove:a_1:p_1:n_1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = applyFocusedGraphCommand(removed, "water_effort_add:a_1:p_1:_root"); !errors.Is(err, errFocusedWorkoutLimit) {
		t.Fatal("removed node bypassed task budget", err)
	}
	v.Add(wp+".node_order", "n_257")
	v.Set(wp+".nodes[n_257].kind", "EFFORT")
	if _, err = parseFocusedGraph(v); !errors.Is(err, errFocusedWorkoutLimit) {
		t.Fatal("257th node accepted", err)
	}
	// Blank initial scaffolding is not effort saved as REST. Actual supplied
	// content is preserved and conflicts instead of being silently removed.
	raw := focusedDraftWithRemovalFields(newFocusedWorkoutDraft(c.Day))
	raw.Set("title", "Descanso explícito")
	raw.Set("entry_kind", "REST")
	raw.Set("schedule.mode", "DAY")
	draft, err := parseFocusedGraph(raw)
	if err != nil {
		t.Fatal(err)
	}
	rest, e := normalizeFocusedGraph(c, draft)
	if len(e) > 0 || len(rest.Activities) != 0 {
		t.Fatal("unused scaffolding blocks notes-only rest", e)
	}
	raw.Set("activities[a_1].phases[p_2].instructions", "Instruções que não podem desaparecer.")
	draft, _ = parseFocusedGraph(raw)
	_, e = normalizeFocusedGraph(c, draft)
	if e["entry_kind"] == "" || draft.Values.Get("activities[a_1].phases[p_2].instructions") == "" {
		t.Fatal("rest silently dropped actual activity content", e)
	}
}

func TestStoredWaterTotalsDoNotReportPartialNumbersForMalformedGraphs(t *testing.T) {
	valid := pages.StructuredWaterStep{ID: "valid", Kind: "EFFORT", DurationSeconds: 60, DistanceMetres: 250}
	for _, tc := range []struct {
		name  string
		steps []pages.StructuredWaterStep
	}{
		{"parent is an effort", []pages.StructuredWaterStep{{ID: "child", ParentID: valid.ID, Kind: "EFFORT"}}},
		{"repeat group is empty", []pages.StructuredWaterStep{{ID: "empty", Kind: "REPEAT_GROUP", Repeats: 2}}},
		{"effort declares repeats", []pages.StructuredWaterStep{{ID: "repeated", Kind: "EFFORT", Repeats: 2}}},
		{"unknown kind", []pages.StructuredWaterStep{{ID: "unknown", Kind: "UNRECOGNIZED"}}},
		{"cycle disconnected from root", []pages.StructuredWaterStep{
			{ID: "a", ParentID: "b", Kind: "REPEAT_GROUP", Repeats: 2},
			{ID: "b", ParentID: "a", Kind: "REPEAT_GROUP", Repeats: 2},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			steps := append([]pages.StructuredWaterStep{valid}, tc.steps...)
			if totals, err := calculateWaterTotalsChecked(steps); totals != nil || !errors.Is(err, errFocusedArithmetic) {
				t.Fatalf("malformed graph returned totals %v and error %v", totals, err)
			}
			totals := calculateWaterTotals(steps)
			if len(totals) != 3 {
				t.Fatalf("expected the three standard totals, got %v", totals)
			}
			for _, total := range totals {
				if total.Value != "Desconhecido" {
					t.Fatalf("malformed graph displayed a numeric total: %+v", total)
				}
			}
		})
	}
}

func TestStoredWaterTotalsRejectDepthBeyondSupportedGraph(t *testing.T) {
	var steps []pages.StructuredWaterStep
	var parent string
	for i := 0; i < focusedMaxWaterDepth; i++ {
		id := fmt.Sprintf("group-%d", i)
		steps = append(steps, pages.StructuredWaterStep{ID: id, ParentID: parent, Kind: "REPEAT_GROUP", Repeats: 1})
		parent = id
	}
	steps = append(steps, pages.StructuredWaterStep{ID: "leaf", ParentID: parent, Kind: "EFFORT", DurationSeconds: 60})
	if totals, err := calculateWaterTotalsChecked(steps); totals != nil || !errors.Is(err, errFocusedArithmetic) {
		t.Fatalf("overdeep graph returned totals %v and error %v", totals, err)
	}
}
