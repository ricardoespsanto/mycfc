package pages

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/cfcoimbra/mycfc/internal/validation"
)

// Literal examples observed in the two private sheets, used only as UI test
// fixtures. This does not create production coaching values or season dates.
func rhythmReferences() []StructuredWaterProfile {
	return []StructuredWaterProfile{
		{ID: "kayak-reference", Name: "Kayak · referência de ensaio", Craft: "Kayak", CraftCode: "KAYAK", Revision: 3, Zones: []StructuredWaterZone{{Code: "R4", Label: "Ritmo quatro", Cadence: "100–115"}, {Code: "R5", Label: "Ritmo cinco", Cadence: "110–130"}, {Code: "R6", Label: "Máximo", Cadence: "Máx"}, {Code: "R7", Label: "Prova", Cadence: "RP"}}},
		{ID: "canoe-younger", Name: "Canoa · tabela dos mais novos (ensaio)", Craft: "Canoa", CraftCode: "CANOE", Revision: 2, Zones: []StructuredWaterZone{{Code: "R2", Label: "Ritmo dois", Cadence: "36–42"}}},
		{ID: "canoe-older", Name: "Canoa · tabela Cadetes a Masters (ensaio)", Craft: "Canoa", CraftCode: "CANOE", Revision: 5, Zones: []StructuredWaterZone{{Code: "R0", Label: "Recuperação", Cadence: "<34"}, {Code: "R1", Label: "Ritmo um", Cadence: "35–40"}, {Code: "R2", Label: "Ritmo dois", Cadence: "40–45"}}},
	}
}
func renderRhythm(t *testing.T, c templ.Component) string {
	t.Helper()
	var out bytes.Buffer
	if err := c.Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}
func rhythmSession(profile *StructuredWaterProfile) StructuredTrainingSession {
	return StructuredTrainingSession{ID: "common-session", Title: "Trabalho comum R2", EntryKind: "TRAINING", Segments: []StructuredTrainingSegment{{ID: "water", Modality: "WATER", Blocks: []StructuredTrainingBlock{{ID: "common-block", Purpose: "MAIN", Instructions: "Prescrição comum sem duplicar a semana", WaterMethod: "Intervalos", WaterTarget: "Continuar até 12 km", WaterReference: profile, WaterSteps: []StructuredWaterStep{{ID: "parent", Kind: "REPEAT_GROUP", Name: "Grupo comum", Repeats: 4, Recovery: 180}, {ID: "child", ParentID: "parent", Kind: "EFFORT", Name: "Esforço comum", DurationSeconds: 300, Recovery: 60, IntensityCode: "R2", Intensity: "R2 · orientação escolhida"}}}}}}}
}
func TestRhythmCanonicalReferenceKeepsOverlapAndDistinctCanoeTables(t *testing.T) {
	refs := rhythmReferences()
	for i := range refs {
		p := refs[i]
		session := rhythmSession(&p)
		before, _ := json.Marshal(session)
		body := renderRhythm(t, StructuredWorkoutInstructions(session))
		after, _ := json.Marshal(session)
		if !bytes.Equal(before, after) {
			t.Fatal("readonly canonical reference changed common workout")
		}
		for _, want := range []string{p.Name, p.Craft, "revisão " + []string{"3", "2", "5"}[i], "4 repetições", "180 s de recuperação", "60 s de recuperação", "Continuar até 12 km", "Esforço comum", "sobrepor-se intencionalmente"} {
			if !strings.Contains(body, want) {
				t.Fatalf("reference%d lost %q", i, want)
			}
		}
		for _, zone := range p.Zones {
			if !strings.Contains(body, html.EscapeString(zone.Cadence)) {
				t.Fatalf("literal range/symbol %q was changed", zone.Cadence)
			}
		}
		if strings.Count(body, `aria-label="Referência de ritmos selecionada"`) != 1 || strings.Contains(body, "<form") || strings.Contains(body, "<button") {
			t.Fatal("athlete preview must contain exactly one readonly selected reference")
		}
		if i == 1 && strings.Contains(body, "40–45") {
			t.Fatal("younger canoe reference acquired older table")
		}
		if i == 2 && strings.Contains(body, "36–42") {
			t.Fatal("older canoe reference acquired younger table")
		}
		if i == 0 && (strings.Contains(body, "116–130") || strings.Contains(body, "por confirmar")) {
			t.Fatal("intentional overlap was normalised or contradicted")
		}
	}
}
func TestRhythmWaterEditorShowsOnlyExplicitPermittedReferenceAndOwnPin(t *testing.T) {
	refs := rhythmReferences()
	p := workoutFixture()
	first := "activities[a_1].phases[p_1].water.profile_id"
	p.Values.Set("activities[a_1].phases[p_1].detail_mode", "WATER")
	p.ProfileReferences = map[string]StructuredWaterProfile{}
	for _, ref := range refs {
		p.Profiles = append(p.Profiles, StructuredWorkoutChoice{Value: ref.ID, Label: ref.Name})
		p.ProfileReferences[ref.ID] = ref
	}
	body := renderWorkout(t, p)
	if strings.Contains(body, `aria-label="Referência de ritmos selecionada"`) {
		t.Fatal("editor selected first craft/profile without explicit choice")
	}
	p.Values.Set(first, refs[1].ID)
	body = renderWorkout(t, p)
	if !strings.Contains(body, "36–42") || strings.Contains(body, "40–45") || !strings.Contains(body, `value="canoe-younger" selected`) {
		t.Fatal("selected editor reference missing or mixed with other canoe table")
	}
	p.Profiles = []StructuredWorkoutChoice{{Value: refs[0].ID, Label: refs[0].Name}}
	p.PinnedProfiles = map[string]StructuredWorkoutChoice{first: {Value: refs[2].ID, Label: refs[2].Name}}
	p.Values.Set(first, refs[2].ID)
	p.Activities[0].Phases = append(p.Activities[0].Phases, StructuredWorkoutPhase{Key: "p_2"})
	second := "activities[a_1].phases[p_2].water.profile_id"
	p.Values.Set("activities[a_1].phases[p_2].detail_mode", "WATER")
	p.Values.Set(second, refs[2].ID)
	body = renderWorkout(t, p)
	if strings.Count(body, "40–45") != 1 {
		t.Fatal("same-block pin must render once and never leak into another phase")
	}
	p.Values.Set(first, "")
	body = renderWorkout(t, p)
	if strings.Contains(body, "40–45") {
		t.Fatal("cleared or unoffered pin was restored by reference catalog")
	}
}
func TestRhythmAdaptRetainsProfileTargetSummaryAndErrorContext(t *testing.T) {
	refs := rhythmReferences()
	p := StructuredTrainingPage{CSRFField: templ.Raw(`<input name="csrf" type="hidden" value="actual-csrf">`), OpenForm: "variation", SelectedWeekID: "week", PlannerReturnURL: "/admin/treinos/estruturados?group_id=group&week_id=week#training-plan", Weeks: []StructuredTrainingChoice{{ID: "week", Name: "Semana revista", GroupID: "group"}}, VariationTargets: []StructuredVariationTargetChoice{{Value: "athlete:member", Name: "Pessoa autorizada", GroupID: "group"}, {Value: "crew:crew", Name: "Tripulação autorizada", GroupID: "group"}}, VariationSubjects: []StructuredVariationSubjectChoice{{Value: "BLOCK:block", Name: "Bloco de água", PlanID: "week"}}, VariationProfiles: refs, VariationValues: url.Values{"plan_id": {"week"}, "target": {"athlete:member"}, "subject": {"BLOCK:block"}, "operation": {"OVERRIDE"}, "change_summary": {"\nResumo original <seguro>"}, "instructions": {"\nInstrução original"}, "intensity_profile_id": {refs[1].ID}, "repeats": {"4"}, "recovery_seconds": {"180"}}, VariationErrors: validation.FieldErrors{"form": "A fonte mudou; reveja os valores.", "intensity_profile_id": "Reveja a referência indicada."}}
	body := renderRhythm(t, structuredVariationForm(p))
	for _, want := range []string{`name="intensity_profile_id"`, `value="canoe-younger" selected`, `value="athlete:member" data-group="group" selected`, `value="BLOCK:block" data-plan="week" selected`, `value="OVERRIDE" selected`, `name="return_to"`, `value="actual-csrf"`, `href="#variation-profile"`, `aria-invalid="true"`, "36–42", "Resumo original &lt;seguro&gt;", "Instrução original", `name="repeats" type="number" min="1" max="10000" value="4"`, `name="recovery_seconds" type="number" min="1" max="86400" value="180"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("native Adapt lost retained field/reference %q", want)
		}
	}
	ids := map[string]bool{}
	for _, match := range regexp.MustCompile(`\sid="([^"]+)"`).FindAllStringSubmatch(body, -1) {
		if ids[match[1]] {
			t.Fatalf("duplicateID%s", match[1])
		}
		ids[match[1]] = true
	}
	for _, tag := range publicationTags(body, "label") {
		id, _ := publicationAttr(tag, "for")
		if !ids[id] {
			t.Fatalf("missing labelled control %s", id)
		}
	}
	if strings.Count(body, "<form ") != 1 || strings.Count(body, "</form>") != 1 || strings.Contains(body, "<seguro>") {
		t.Fatal("Adapt form nesting/escaping invalid")
	}
	p.VariationValues.Set("intensity_profile_id", "own-invalid-profile")
	body = renderRhythm(t, structuredVariationForm(p))
	if !strings.Contains(body, `value="own-invalid-profile" selected`) || strings.Contains(body, "36–42") {
		t.Fatal("unknown profile must retain own raw value without showing guessed guidance")
	}
	p.VariationValues.Del("intensity_profile_id")
	body = renderRhythm(t, structuredVariationForm(p))
	if !strings.Contains(body, `value="" selected>Manter a referência do bloco`) || strings.Contains(body, `aria-label="Referência de ritmos selecionada"`) {
		t.Fatal("ambiguous craft received a default reference")
	}
	for key, value := range map[string]string{"plan_id": "own-invalid-plan", "target": "own-invalid-target", "subject": "own-invalid-subject", "operation": "own-invalid-operation", "modality": "own-invalid-modality", "repeats": "own-invalid-number"} {
		p.VariationValues.Set(key, value)
	}
	body = renderRhythm(t, structuredVariationForm(p))
	for _, raw := range []string{"own-invalid-plan", "own-invalid-target", "own-invalid-subject", "own-invalid-operation", "own-invalid-modality"} {
		if !strings.Contains(body, `value="`+raw+`"`) {
			t.Fatalf("invalid own choice %s disappeared", raw)
		}
	}
	if !strings.Contains(body, `name="repeats" type="text"`) || !strings.Contains(body, `value="own-invalid-number"`) {
		t.Fatal("invalid numeric raw value must remain editable text")
	}

}
func TestRhythmOptionalTypedFactsDoNotAddNullFieldsToLegacyEmptyData(t *testing.T) {
	for _, value := range []any{StructuredTrainingBlock{}, StructuredWaterStep{}, StructuredWaterProfile{}, StructuredWaterZone{}} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"WaterReference", "IntensityCode", "CadenceSPM", "CraftCode", "CadenceMin", "CadenceMax"} {
			if strings.Contains(string(raw), `"`+key+`"`) {
				t.Fatalf("absent new fact %s changed legacy JSON shape: %s", key, raw)
			}
		}
	}
}

func TestRhythmLiteralOneSidedAndSymbolicGuidanceAppearsOnce(t *testing.T) {
	limit := int32(60)
	for _, literal := range []string{"<60", "Máx", "RP"} {
		profile := StructuredWaterProfile{ID: "literal-reference", Name: "Referência literal", Craft: "Kayak", Revision: 3, Zones: []StructuredWaterZone{{Code: "R0", Label: "Orientação", Cadence: literal, Meaning: literal, CadenceMax: &limit}}}
		before, _ := json.Marshal(profile)
		body := renderRhythm(t, structuredWaterRhythmReference(profile))
		after, _ := json.Marshal(profile)
		if strings.Count(body, html.EscapeString(literal)) != 1 {
			t.Fatalf("literal %q must appear exactly once: %s", literal, body)
		}
		if strings.Contains(body, "até 60") || strings.Contains(body, "≤60") || !bytes.Equal(before, after) {
			t.Fatal("literal rendering must not invent an inclusive limit or mutate raw typed bounds")
		}
	}
	profile := StructuredWaterProfile{Name: "Referência distinta", Craft: "Canoa", Revision: 2, Zones: []StructuredWaterZone{{Code: "R2", Label: "Ritmo dois", Cadence: "70–80 remadas/min", Meaning: "Trabalho aeróbio"}}}
	body := renderRhythm(t, structuredWaterRhythmReference(profile))
	for _, value := range []string{"70–80 remadas/min", "Trabalho aeróbio"} {
		if strings.Count(body, value) != 1 {
			t.Fatalf("distinct range and meaning must both remain literal: %s", body)
		}
	}
}
