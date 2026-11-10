package handlers

import (
	"errors"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
)

func TestReuseStoreReferenceClosureRejectsIncompleteOrInconsistentFacts(t *testing.T) {
	pin, other := uuid.New(), uuid.New()
	for _, tc := range []struct {
		name   string
		change func(*[]uuid.UUID, *focusedGraphProfileFacts)
		want   error
	}{
		{"complete", func(*[]uuid.UUID, *focusedGraphProfileFacts) {}, nil},
		{"missing profile", func(_ *[]uuid.UUID, facts *focusedGraphProfileFacts) { facts.Profiles = nil }, errReuseChanged},
		{"zero pin", func(expected *[]uuid.UUID, _ *focusedGraphProfileFacts) { (*expected)[0] = uuid.Nil }, errFocusedWorkoutInvalid},
		{"duplicate pin", func(expected *[]uuid.UUID, facts *focusedGraphProfileFacts) {
			*expected = append(*expected, pin)
			facts.Profiles = append(facts.Profiles, facts.Profiles[0])
		}, errFocusedWorkoutInvalid},
		{"wrong profile", func(_ *[]uuid.UUID, facts *focusedGraphProfileFacts) { facts.Profiles[0].ID = other }, errReuseChanged},
		{"invalid revision", func(_ *[]uuid.UUID, facts *focusedGraphProfileFacts) { facts.Profiles[0].Revision = 0 }, errReuseChanged},
		{"unrelated zone", func(_ *[]uuid.UUID, facts *focusedGraphProfileFacts) { facts.Zones[0].ProfileID = other }, errReuseChanged},
		{"duplicate zone code", func(_ *[]uuid.UUID, facts *focusedGraphProfileFacts) {
			facts.Zones = append(facts.Zones, dbgen.WaterIntensityZone{ID: uuid.New(), ProfileID: pin, Code: "R2", Position: 2})
		}, errReuseChanged},
		{"duplicate zone position", func(_ *[]uuid.UUID, facts *focusedGraphProfileFacts) {
			facts.Zones = append(facts.Zones, dbgen.WaterIntensityZone{ID: uuid.New(), ProfileID: pin, Code: "R3", Position: 1})
		}, errReuseChanged},
		{"zone budget exceeded", func(_ *[]uuid.UUID, facts *focusedGraphProfileFacts) {
			facts.Zones = make([]dbgen.WaterIntensityZone, 257)
		}, errFocusedWorkoutLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expected := []uuid.UUID{pin}
			facts := focusedGraphProfileFacts{
				Profiles: []dbgen.WaterIntensityProfile{{ID: pin, Revision: 1}},
				Zones:    []dbgen.WaterIntensityZone{{ID: uuid.New(), ProfileID: pin, Code: "R2", Position: 1}},
			}
			tc.change(&expected, &facts)
			if err := validateReuseReferenceClosure(expected, facts); !errors.Is(err, tc.want) {
				t.Fatalf("reference closure error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestReuseStoreReferenceUnionKeepsBothCraftsAndRejectsDrift(t *testing.T) {
	kayak, canoe := uuid.MustParse("00000000-0000-0000-0000-000000000001"), uuid.MustParse("00000000-0000-0000-0000-000000000002")
	kayakProfile := dbgen.WaterIntensityProfile{ID: kayak, Craft: dbgen.PaddlingCraftKAYAK, Revision: 1}
	kayakProfile.CreatedAt.Valid = true
	kayakProfile.CreatedAt.Time = time.Date(2026, 11, 10, 12, 0, 0, 0, time.FixedZone("offset", 3600))
	canoeProfile := dbgen.WaterIntensityProfile{ID: canoe, Craft: dbgen.PaddlingCraftCANOE, Revision: 1}
	kayakR1 := dbgen.WaterIntensityZone{ID: uuid.New(), ProfileID: kayak, Code: "R1", Position: 1, Meaning: "Kayak R1 guidance"}
	kayakR2 := dbgen.WaterIntensityZone{ID: uuid.New(), ProfileID: kayak, Code: "R2", Position: 2, Meaning: "Kayak R2 guidance"}
	canoeR1 := dbgen.WaterIntensityZone{ID: uuid.New(), ProfileID: canoe, Code: "R1", Position: 1, Meaning: "Canoe R1 guidance"}
	source := focusedGraphProfileFacts{Profiles: []dbgen.WaterIntensityProfile{kayakProfile}, Zones: []dbgen.WaterIntensityZone{kayakR2, kayakR1}}
	target := focusedGraphProfileFacts{Profiles: []dbgen.WaterIntensityProfile{canoeProfile, kayakProfile}, Zones: []dbgen.WaterIntensityZone{canoeR1, kayakR1}}

	union, err := mergeReuseReferences(source, target)
	if err != nil {
		t.Fatal(err)
	}
	if len(union.Profiles) != 2 || union.Profiles[0].ID != kayak || union.Profiles[1].ID != canoe ||
		union.Profiles[0].CreatedAt.Time.Location() != time.UTC || !union.Profiles[0].CreatedAt.Time.Equal(kayakProfile.CreatedAt.Time) {
		t.Fatalf("unexpected profile union: %+v", union.Profiles)
	}
	if len(union.Zones) != 3 || union.Zones[0] != kayakR1 || union.Zones[1] != kayakR2 || union.Zones[2] != canoeR1 {
		t.Fatalf("zones lost their craft or order: %+v", union.Zones)
	}
	target.Profiles[1].Revision++
	if _, err = mergeReuseReferences(source, target); !errors.Is(err, errReuseChanged) {
		t.Fatalf("inconsistent profile revision error = %v", err)
	}
	target.Profiles[1].Revision--
	target.Zones[1].Meaning = "Changed target guidance"
	if _, err = mergeReuseReferences(source, target); !errors.Is(err, errReuseChanged) {
		t.Fatalf("inconsistent pinned zone error = %v", err)
	}
}

func TestReuseStoreReferenceUnionBudgetsBothSides(t *testing.T) {
	source, target := focusedGraphProfileFacts{}, focusedGraphProfileFacts{}
	for i := 0; i < 32; i++ {
		source.Profiles = append(source.Profiles, dbgen.WaterIntensityProfile{ID: uuid.New(), Revision: 1})
	}
	target.Profiles = []dbgen.WaterIntensityProfile{source.Profiles[0]}
	union, err := mergeReuseReferences(source, target)
	if err != nil || len(union.Profiles) != 32 {
		t.Fatalf("overlapping boundary pin was counted twice: %d, %v", len(union.Profiles), err)
	}
	target.Profiles[0].ID = uuid.New()
	if _, err = mergeReuseReferences(source, target); !errors.Is(err, errFocusedWorkoutLimit) {
		t.Fatalf("over-budget source and target union error = %v", err)
	}
}

func TestReuseStoreExpectedReferenceIDsRejectNilPin(t *testing.T) {
	graph := focusedWorkoutGraph{Activities: []focusedGraphActivityInput{{Phases: []focusedGraphPhaseInput{{Water: &focusedGraphWaterInput{ProfileID: focusedPointer(uuid.Nil)}}}}}}
	if _, err := reuseExpectedProfileIDs([]focusedWorkoutGraph{graph}); !errors.Is(err, errFocusedWorkoutInvalid) {
		t.Fatalf("nil pin error = %v", err)
	}
}
