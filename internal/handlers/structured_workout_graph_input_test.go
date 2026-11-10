package handlers

import (
	"errors"
	"net/url"
	"slices"
	"testing"
)

func TestWorkoutGraphCommandsMoveOnlyExistingSiblings(t *testing.T) {
	source := workoutEditGymSource(t)
	draft, err := focusedEditLosslessDraft(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"phase_add:a_1", "gym_add:a_1:p_1", "phase_down:a_1:p_1", "gym_down:a_1:p_1:e_1"} {
		draft, err = applyFocusedEditCommand(draft, command, source.Graph)
		if err != nil {
			t.Fatalf("%s: %v", command, err)
		}
	}
	phaseOrder := focusedActivityPrefix("a_1") + ".phase_order"
	exerciseOrder := focusedGraphPhasePrefix("a_1", "p_1") + ".gym.exercise_order"
	if !slices.Equal(draft.Values[phaseOrder], []string{"p_2", "p_1"}) ||
		!slices.Equal(draft.Values[exerciseOrder], []string{"e_2", "e_1"}) {
		t.Fatalf("moves changed the wrong lists: phases=%v exercises=%v", draft.Values[phaseOrder], draft.Values[exerciseOrder])
	}
	for _, command := range []string{"phase_up:a_1:p_2", "gym_down:a_1:p_1:e_1", "gym_up:a_1:p_1:e_unknown"} {
		before := focusedGraphValues(draft).Encode()
		out, err := applyFocusedEditCommand(draft, command, source.Graph)
		if !errors.Is(err, errFocusedWorkoutInvalid) || focusedGraphValues(out).Encode() != before {
			t.Fatalf("%s changed the draft or returned %v", command, err)
		}
	}
}

func TestWorkoutGraphRejectsCommandsThatReferOutsideTheDraft(t *testing.T) {
	_, values := focusedGraphWaterExample()
	draft, err := parseFocusedGraph(values)
	if err != nil {
		t.Fatal(err)
	}
	before := focusedGraphValues(draft).Encode()
	for _, command := range []string{
		"refresh:a_1", "activity_add:a_1", "phase_add:a_1:p_1", "water_group_add:a_1:p_1",
		"water_effort_add:a_1:p_1:n_2", "gym_add:a_1:p_1:extra", "activity_up:a_1:p_1",
		"phase_down:a_1", "water_up:a_1:p_1:n_unknown", "water_down:a_1:p_1",
		"activity_remove:a_1:p_1", "phase_remove:a_1", "water_remove:a_1:p_1",
		"gym_remove:a_1:p_1", "restore:r_unknown", "phase_up:a_unknown:p_1",
	} {
		t.Run(command, func(t *testing.T) {
			out, err := applyFocusedGraphCommand(draft, command)
			if !errors.Is(err, errFocusedWorkoutInvalid) || focusedGraphValues(out).Encode() != before {
				t.Fatalf("invalid command changed the draft or returned %v", err)
			}
		})
	}
}

func TestWorkoutGraphValidationRetainsContradictoryWaterInputs(t *testing.T) {
	a := focusedActivityPrefix("a_1")
	p := focusedGraphPhasePrefix("a_1", "p_1")
	group := focusedNodePrefix("a_1", "p_1", "n_1")
	effort := focusedNodePrefix("a_1", "p_1", "n_2")
	for _, tc := range []struct {
		name string
		raw  url.Values
		want string
	}{
		{"unsupported entry", url.Values{"entry_kind": {"UNKNOWN"}}, "entry_kind"},
		{"rest retains effort", url.Values{"entry_kind": {"REST"}}, "entry_kind"},
		{"unknown modality", url.Values{a + ".modality": {"UNKNOWN"}}, a + ".modality"},
		{"unknown phase", url.Values{p + ".purpose": {"UNKNOWN"}}, p + ".purpose"},
		{"custom phase missing name", url.Values{p + ".purpose": {"CUSTOM"}, p + ".title": {""}}, p + ".title"},
		{"unset detail retains water", url.Values{p + ".detail_mode": {""}}, p + ".detail_mode"},
		{"unknown detail", url.Values{p + ".detail_mode": {"UNKNOWN"}}, p + ".detail_mode"},
		{"gym activity with water detail", url.Values{a + ".modality": {"GYM"}}, p + ".detail_mode"},
		{"unknown method", url.Values{p + ".water.method": {"UNKNOWN"}}, p + ".water.method"},
		{"malformed profile", url.Values{p + ".water.profile_id": {"not-a-uuid"}}, p + ".water.profile_id"},
		{"group missing repeats", url.Values{group + ".repeats": {""}}, group + ".repeats"},
		{"effort with repeats", url.Values{effort + ".repeats": {"3"}}, effort + ".repeats"},
		{"effort without measure or instruction", url.Values{effort + ".duration_seconds": {""}, effort + ".instructions": {""}}, effort + ".instructions"},
		{"wrong parent kind", url.Values{group + ".kind": {"EFFORT"}}, effort + ".kind"},
		{"unknown water step", url.Values{effort + ".kind": {"UNKNOWN"}}, effort + ".kind"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, values := focusedGraphWaterExample()
			for key, entries := range tc.raw {
				values[key] = entries
			}
			draft, err := parseFocusedGraph(values)
			if err != nil {
				t.Fatal(err)
			}
			before := draft.Values.Encode()
			_, fields := normalizeFocusedGraph(c, draft)
			if fields[focusedFieldKey(tc.want)] == "" || draft.Values.Encode() != before {
				t.Fatalf("validation dropped submitted fields or omitted %s: %v", tc.want, fields)
			}
		})
	}
}

func TestWorkoutGraphValidationKeepsGymResistanceModesDistinct(t *testing.T) {
	exercise := focusedExercisePrefix("a_1", "p_1", "e_1")
	for _, tc := range []struct {
		name, kind, value, text, field string
	}{
		{"missing kind", "", "70", "", "resistance_kind"},
		{"unknown kind", "UNKNOWN", "70", "", "resistance_kind"},
		{"numeric with text", "PERCENT_1RM", "70", "optional", "resistance_text"},
		{"body weight with load", "BODY_WEIGHT", "70", "", "resistance_value"},
		{"band without instruction", "BAND", "", "", "resistance_text"},
		{"band with numeric load", "BAND", "70", "Banda leve", "resistance_value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := workoutEditGymSource(t)
			draft, err := focusedEditLosslessDraft(source)
			if err != nil {
				t.Fatal(err)
			}
			draft.Values.Set(exercise+".resistance_kind", tc.kind)
			draft.Values.Set(exercise+".resistance_value", tc.value)
			draft.Values.Set(exercise+".resistance_text", tc.text)
			_, fields := normalizeFocusedEdit(source, draft)
			if fields[focusedFieldKey(exercise+"."+tc.field)] == "" {
				t.Fatalf("incompatible gym resistance lacked field error %s: %v", tc.field, fields)
			}
		})
	}
}
