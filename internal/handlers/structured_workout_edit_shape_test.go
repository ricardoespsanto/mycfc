package handlers

import (
	"errors"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
)

func workoutEditGymSource(t *testing.T) focusedEditSource {
	t.Helper()
	c, _ := focusedWorkoutFixture()
	activity := focusedActivityPrefix("a_1")
	phase := focusedGraphPhasePrefix("a_1", "p_1")
	gym := phase + ".gym"
	exercise := focusedExercisePrefix("a_1", "p_1", "e_1")
	v := url.Values{
		"title": {"Circuito de força"}, "entry_kind": {"TRAINING"}, "schedule.date": {c.Day},
		"schedule.mode": {"INTERVAL"}, "schedule.start_time": {"18:00"}, "schedule.end_date": {c.Day}, "schedule.end_time": {"19:30"},
		"activity_order": {"a_1"}, activity + ".modality": {"GYM"}, activity + ".phase_order": {"p_1"},
		phase + ".purpose": {"MAIN"}, phase + ".instructions": {"Executar o circuito com controlo."}, phase + ".detail_mode": {"GYM"},
		gym + ".structure": {"CIRCUIT"}, gym + ".objective": {"STRENGTH_ENDURANCE"}, gym + ".rounds": {"3"},
		gym + ".round_recovery_seconds": {"90"}, gym + ".exercise_order": {"e_1"},
		exercise + ".name": {"Agachamento"}, exercise + ".sets": {"2"}, exercise + ".repetitions": {"12"},
		exercise + ".resistance_kind": {"PERCENT_1RM"}, exercise + ".resistance_value": {"72.5"}, exercise + ".tempo": {"3-1-1"},
	}
	p := normalizedGraphExample(t, c, v)
	allocateFocusedGraphIDs(&p, nil)
	source := focusedEditSource{Context: c, Graph: p}
	source.Raw.Session.ID = p.SessionID
	return source
}

func TestWorkoutEditGymDraftRoundTripAndExerciseChanges(t *testing.T) {
	source := workoutEditGymSource(t)
	draft, err := focusedEditLosslessDraft(source)
	if err != nil {
		t.Fatal(err)
	}
	exercise := focusedExercisePrefix("a_1", "p_1", "e_1")
	if draft.Values.Get(exercise+".resistance_value") != "72.5" || draft.Values.Get(exercise+".tempo") != "3-1-1" {
		t.Fatal("edit form lost the stored gym resistance or tempo")
	}
	old := source.Graph.Activities[0].Phases[0].Gym.Exercises[0].ID
	proposal, fields := normalizeFocusedEdit(source, draft)
	if len(fields) > 0 {
		t.Fatal(fields)
	}
	if err = allocateFocusedEditIDs(&proposal, source.Graph, nil); err != nil {
		t.Fatal(err)
	}
	diff, err := buildFocusedEditDiff(source.Graph, proposal, draft)
	if err != nil || !slices.Equal(diff.RetainedExercises, []uuid.UUID{old}) {
		t.Fatalf("existing exercise did not keep its identity: %+v %v", diff, err)
	}

	removed, err := applyFocusedEditCommand(draft, "gym_remove:a_1:p_1:e_1", source.Graph)
	if err != nil {
		t.Fatal(err)
	}
	added, err := applyFocusedEditCommand(removed, "gym_add:a_1:p_1", source.Graph)
	if err != nil {
		t.Fatal(err)
	}
	order := focusedGraphPhasePrefix("a_1", "p_1") + ".gym.exercise_order"
	keys := added.Values[order]
	if len(keys) != 1 || keys[0] == "e_1" {
		t.Fatalf("new exercise reused the removed draft key: %v", keys)
	}
	newExercise := focusedExercisePrefix("a_1", "p_1", keys[0])
	added.Values.Set(newExercise+".name", "Remada")
	added.Values.Set(newExercise+".repetitions", "10")
	proposal, fields = normalizeFocusedEdit(source, added)
	if len(fields) > 0 {
		t.Fatal(fields)
	}
	if err = allocateFocusedEditIDs(&proposal, source.Graph, nil); err != nil {
		t.Fatal(err)
	}
	diff, err = buildFocusedEditDiff(source.Graph, proposal, added)
	if err != nil || !slices.Equal(diff.RemovedExercises, []uuid.UUID{old}) || len(diff.AddedExercises) != 1 || diff.AddedExercises[0] == old || len(diff.RemovedGym) != 0 {
		t.Fatalf("exercise replacement did not preserve the reviewed removal and new identity: %+v %v", diff, err)
	}
}

func TestWorkoutEditRefusesOutOfWeekOrDifferentEntryKind(t *testing.T) {
	source := focusedEditFixture(t)
	draft, err := focusedEditLosslessDraft(source)
	if err != nil {
		t.Fatal(err)
	}
	draft.Values.Set("schedule.date", source.Context.WeekStart.AddDate(0, 0, 7).Format("2006-01-02"))
	draft.Values.Set("entry_kind", "COMPETITION")
	proposal, fields := normalizeFocusedEdit(source, draft)
	if fields["schedule.date"] == "" || fields["entry_kind"] == "" || proposal.Kind != source.Graph.Kind || draft.Values.Get("entry_kind") != "COMPETITION" {
		t.Fatalf("invalid edit lost the submitted kind or field feedback: %v", fields)
	}
}

func TestWorkoutEditDiffRefusesInvalidIdentitiesAndRemovals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*focusedWorkoutGraph, *focusedWorkoutGraph, *focusedGraphDraft)
	}{
		{"different session", func(_, next *focusedWorkoutGraph, _ *focusedGraphDraft) { next.SessionID = uuid.New() }},
		{"malformed source identity", func(old, _ *focusedWorkoutGraph, _ *focusedGraphDraft) { old.Activities[0].ID = uuid.Nil }},
		{"duplicate phase identity", func(_, next *focusedWorkoutGraph, _ *focusedGraphDraft) {
			next.Activities[0].Phases[0].ID = next.Activities[0].ID
		}},
		{"water step assigned to unknown parent", func(_, next *focusedWorkoutGraph, _ *focusedGraphDraft) {
			next.Activities[0].Phases[0].Water.Nodes[1].ParentKey = "unknown"
		}},
		{"water step retained under a different parent", func(_, next *focusedWorkoutGraph, _ *focusedGraphDraft) {
			next.Activities[0].Phases[0].Water.Nodes[1].ParentKey = ""
		}},
		{"invalid tombstone", func(_, _ *focusedWorkoutGraph, draft *focusedGraphDraft) {
			draft.Removed = []focusedGraphRemoval{{Payload: "invalid"}}
		}},
		{"remove root still present in graph", func(old, _ *focusedWorkoutGraph, draft *focusedGraphDraft) {
			a := old.Activities[0]
			b := a.Phases[0]
			prefix := focusedGraphPhasePrefix(a.Key, b.Key)
			draft.Removed = []focusedGraphRemoval{{Payload: focusedGraphTombstoneJSON(focusedGraphTombstone{
				Kind: "phase", Activity: a.Key, Phase: b.Key, Values: url.Values{prefix + ".id": {b.ID.String()}},
			})}}
		}},
		{"removal identity from different workout", func(old, _ *focusedWorkoutGraph, draft *focusedGraphDraft) {
			a := old.Activities[0]
			b := a.Phases[0]
			prefix := focusedGraphPhasePrefix(a.Key, b.Key)
			draft.Removed = []focusedGraphRemoval{{Payload: focusedGraphTombstoneJSON(focusedGraphTombstone{
				Kind: "phase", Activity: a.Key, Phase: b.Key, Values: url.Values{prefix + ".id": {uuid.NewString()}},
			})}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := focusedEditFixture(t)
			proposed, err := cloneReuseGraph(source.Graph)
			if err != nil {
				t.Fatal(err)
			}
			draft := focusedGraphDraft{}
			tc.change(&source.Graph, &proposed, &draft)
			if _, err = buildFocusedEditDiff(source.Graph, proposed, draft); !errors.Is(err, errFocusedWorkoutInvalid) {
				t.Fatalf("invalid edit diff returned %v", err)
			}
		})
	}
}

func TestWorkoutEditPreservesBothOccurrencesOfLisbonFold(t *testing.T) {
	source := focusedEditFixture(t)
	c := &source.Context
	c.WeekStart = time.Date(2026, time.October, 19, 0, 0, 0, 0, time.UTC)
	c.Day = "2026-10-25"
	source.Graph.Context = *c
	start := time.Date(2026, time.October, 25, 0, 30, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	source.Graph.Schedule = TrainingSessionSchedule{Precision: TrainingScheduleInterval, Date: c.Day, StartsAt: &start, EndsAt: &end}
	draft, err := focusedEditLosslessDraft(source)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Values.Get("schedule.start_time") != "01:30" || draft.Values.Get("schedule.end_time") != "01:30" ||
		draft.Values.Get("schedule.start_occurrence") != start.Format(time.RFC3339Nano) || draft.Values.Get("schedule.end_occurrence") != end.Format(time.RFC3339Nano) {
		t.Fatal("the edit form collapsed distinct occurrences of the repeated hour")
	}
}
