package featureflags

import "testing"

func TestAvailabilityDecisionMatrix(t *testing.T) {
	for _, tc := range []struct {
		name          string
		mode          Mode
		administrator bool
		want          bool
	}{
		{"disabled member", Disabled, false, false},
		{"disabled administrator", Disabled, true, false},
		{"administrator only member", AdminOnly, false, false},
		{"administrator only administrator", AdminOnly, true, true},
		{"enabled member", Enabled, false, true},
		{"enabled administrator", Enabled, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Available(map[Key]Mode{Suggestions: tc.mode}, Suggestions, tc.administrator); got != tc.want {
				t.Fatalf("Available() = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestRegistryDefaultsAndUnknownValuesFailClosed(t *testing.T) {
	if !Available(nil, Suggestions, false) {
		t.Fatal("suggestions should retain its enabled default")
	}
	if Available(nil, PhotoSubmissions, true) {
		t.Fatal("photo submissions should retain its disabled default")
	}
	if !Available(nil, StructuredTrainingPlanning, true) || Available(nil, StructuredTrainingPlanning, false) {
		t.Fatal("structured training should retain its administrator-only default")
	}
	if Available(map[Key]Mode{Suggestions: Mode("BROKEN")}, Suggestions, true) {
		t.Fatal("invalid stored mode should fail closed")
	}
	if Available(nil, Key("unknown"), true) {
		t.Fatal("unknown feature should fail closed")
	}
}

func TestRegistryReturnsAnIndependentCompleteDefinitionList(t *testing.T) {
	definitions := Registry()
	if len(definitions) != 3 || definitions[0].Key != Suggestions || definitions[2].Key != StructuredTrainingPlanning {
		t.Fatalf("definitions=%#v", definitions)
	}
	definitions[0].Label = "changed"
	if Registry()[0].Label == "changed" {
		t.Fatal("registry caller must not mutate shared definitions")
	}
}
