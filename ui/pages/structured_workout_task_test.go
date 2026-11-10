package pages

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"html"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/cfcoimbra/mycfc/internal/validation"
)

func workoutFixture() StructuredWorkoutTaskPage {
	return StructuredWorkoutTaskPage{
		CSRFField: templ.Raw(`<input type="hidden" name="_csrf" value="synthetic">`),
		DraftURL:  "/workout/draft", PreviewURL: "/workout/preview", ConfirmURL: "/workout/confirm", ReturnURL: "/week?day=2026-11-09", AdvancedURL: "/advanced", GroupName: "Cadetes", WeekTitle: "9–15 novembro",
		Context:    StructuredWorkoutContext{GroupID: "group", PlanID: "plan", Day: "2026-11-09", ExpectedPlanUpdatedAt: "original-plan-version", SourceDigest: "source-hash", DatabaseIncarnation: "incarnation", StateEpoch: "7"},
		Values:     url.Values{"title": {"Treino de água"}, "entry_kind": {"TRAINING"}, "schedule.mode": {"START_ONLY"}, "schedule.date": {"2026-11-09"}, "schedule.start_time": {"16:00"}, "activities[a_1].modality": {"WATER"}, "activities[a_1].phases[p_1].purpose": {"MAIN"}, "activities[a_1].phases[p_1].instructions": {"10x4’/2’ R2 — texto do treinador"}},
		Activities: []StructuredWorkoutActivity{{Key: "a_1", ID: "segment-existing", Phases: []StructuredWorkoutPhase{{Key: "p_1", ID: "phase-existing"}}}},
	}
}
func renderWorkout(t *testing.T, p StructuredWorkoutTaskPage) string {
	t.Helper()
	var out bytes.Buffer
	if err := structuredWorkoutTaskContent(p).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	if dir := os.Getenv("WORKOUT_COMPONENT_EVIDENCE"); dir != "" {
		if !filepath.IsAbs(dir) || !strings.HasPrefix(filepath.Clean(dir), filepath.Clean(os.TempDir())+string(os.PathSeparator)) {
			t.Fatal("component evidence must use an absolute temporary directory")
		}
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256([]byte(body))
		name := strings.ReplaceAll(t.Name(), "/", "-") + fmt.Sprintf("-%x.html", hash[:6])
		document := `<!doctype html><html lang="pt-PT"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Componente de preparação de treino</title><link rel="stylesheet" href="/assets/app.css"><script defer src="/assets/app.js"></script></head><body class="app-body"><main id="conteudo-principal">` + body + `</main></body></html>`
		if err := os.WriteFile(filepath.Join(dir, name), []byte(document), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return body
}
func workoutTags(body, tag string) []string {
	return regexp.MustCompile(`(?s)<`+regexp.QuoteMeta(tag)+`\b([^>]*)>`).FindAllString(body, -1)
}
func workoutAttr(tag, name string) (string, bool) {
	match := regexp.MustCompile(`(?:\s|<)` + regexp.QuoteMeta(name) + `(?:="([^"]*)")?(?:\s|/?>|$)`).FindStringSubmatch(tag)
	if match == nil {
		return "", false
	}
	return html.UnescapeString(match[1]), true
}
func workoutControl(t *testing.T, body, name string) string {
	t.Helper()
	for _, kind := range []string{"input", "select", "textarea"} {
		for _, tag := range workoutTags(body, kind) {
			if value, _ := workoutAttr(tag, "name"); value == name {
				return tag
			}
		}
	}
	t.Fatalf("missing control %s", name)
	return ""
}
func workoutOptionValues(t *testing.T, body, name string) []string {
	t.Helper()
	for _, match := range regexp.MustCompile(`(?s)<select\b[^>]*>.*?</select>`).FindAllString(body, -1) {
		start := strings.SplitN(match, ">", 2)[0] + ">"
		if value, _ := workoutAttr(start, "name"); value != name {
			continue
		}
		out := []string{}
		for _, tag := range workoutTags(match, "option") {
			v, _ := workoutAttr(tag, "value")
			out = append(out, v)
		}
		return out
	}
	t.Fatalf("missing select %s", name)
	return nil
}
func assertWorkoutValue(t *testing.T, body, name, want string) {
	t.Helper()
	if got, _ := workoutAttr(workoutControl(t, body, name), "value"); got != want {
		t.Errorf("%s=%q want%q", name, got, want)
	}
}

func TestFocusedWorkoutKindTimingAndNoInventedDefaults(t *testing.T) {
	p := workoutFixture()
	p.Values.Set("schedule.start_time", "")
	body := renderWorkout(t, p)
	if got := strings.Join(workoutOptionValues(t, body, "schedule.mode"), ","); got != ",START_ONLY,INTERVAL" {
		t.Fatal(got)
	}
	start := workoutControl(t, body, "schedule.start_time")
	if _, required := workoutAttr(start, "required"); !required {
		t.Fatal("training actual start not required")
	}
	assertWorkoutValue(t, body, "schedule.start_time", "")
	if strings.Contains(body, `name="schedule.end_time"`) || strings.Contains(body, `name="schedule.band"`) {
		t.Fatal("invented end/band on start-only form")
	}
	for _, kind := range []string{"REST", "LOGISTICS"} {
		p.Values.Set("entry_kind", kind)
		p.Values.Set("schedule.mode", "BAND")
		p.Values.Set("schedule.band", "MORNING")
		body = renderWorkout(t, p)
		if got := strings.Join(workoutOptionValues(t, body, "schedule.mode"), ","); got != ",START_ONLY,INTERVAL,DAY,BAND" {
			t.Fatal(got)
		}
		if strings.Contains(body, `name="schedule.start_time"`) {
			t.Fatal("band invented a clock")
		}
	}
	p.Values.Set("entry_kind", "COMPETITION")
	p.Values.Set("schedule.mode", "DAY")
	p.Values.Set("schedule.start_time", "hora incompleta")
	p.Errors = validation.FieldErrors{"schedule.mode": "Selecione um horário compatível."}
	body = renderWorkout(t, p)
	if !strings.Contains(body, "Hora por definir") || !strings.Contains(body, "Valor introduzido:") {
		t.Fatal("contradictory kind switch lost original mode")
	}
	assertWorkoutValue(t, body, "schedule.start_time", "hora incompleta")
	if typ, _ := workoutAttr(workoutControl(t, body, "schedule.start_time"), "type"); typ != "text" {
		t.Fatal("invalid raw time sanitized")
	}
	p.Editing = true
	p.Context.EntryKind = "TRAINING"
	p.Values.Set("entry_kind", "REST")
	body = renderWorkout(t, p)
	assertWorkoutValue(t, body, "entry_kind", "TRAINING")
	if strings.Contains(body, `<select id="workout-entry_kind"`) {
		t.Fatal("existing kind became editable")
	}
}

func TestFocusedWorkoutNativeCommandsAndIdentityUseTrustedTopology(t *testing.T) {
	p := workoutFixture()
	p.Activities[0].Phases[0].WaterNodes = []StructuredWorkoutWaterNode{{Key: "n_1", ID: "group-node"}, {Key: "n_2", ID: "effort-node", ParentKey: "n_1"}}
	prefix := "activities[a_1].phases[p_1].water.nodes[n_1]"
	p.Values.Set(prefix+".kind", "REPEAT_GROUP")
	p.Values.Set(prefix+".name", "Série principal")
	p.Values.Set(prefix+".repeats", "10")
	p.Values.Set("activities[a_1].phases[p_1].water.nodes[n_2].kind", "EFFORT")
	p.Values.Set("activities[a_1].phases[p_1].water.nodes[n_2].duration_seconds", "240")
	p.Values.Set("activities[a_1].phases[p_1].water.nodes[n_2].parent_key", "forged-parent")
	p.Values.Set("activities[a_1].id", "forged-id")
	p.Values.Set("activity_order", "forged-order")
	body := renderWorkout(t, p)
	assertWorkoutValue(t, body, "activities[a_1].id", "segment-existing")
	assertWorkoutValue(t, body, "activity_order", "a_1")
	assertWorkoutValue(t, body, "activities[a_1].phases[p_1].water.nodes[n_2].parent_key", "n_1")
	if strings.Contains(body, "forged-") {
		t.Fatal("identity/topology taken from raw values")
	}
	if strings.Contains(body, "task-surface--sheet") || !strings.Contains(body, `class="task-route-page"`) {
		t.Fatal("long route must use natural-height task surface and standard readable width")
	}
	if strings.Index(body, `aria-label="Outras opções do treino"`) > strings.Index(body, `class="task-surface__actions"`) {
		t.Fatal("recovery after negative-margin final actions would overlap")
	}
	if !strings.Contains(body, `<div class="task-form">`) {
		t.Fatal("task action margins require the standard padded task-form wrapper")
	}
	if strings.Contains(body, "data-task-form") || strings.Contains(body, "data-local-mutation") || strings.Contains(body, "hx-post") {
		t.Fatal("native named commands intercepted by disabling script")
	}
	commands := map[string]bool{}
	for _, button := range workoutTags(body, "button") {
		name, _ := workoutAttr(button, "name")
		if name != "command" {
			continue
		}
		command, _ := workoutAttr(button, "value")
		commands[command] = true
		if command != "preview" {
			if _, ok := workoutAttr(button, "formnovalidate"); !ok {
				t.Errorf("draft command %q blocks incomplete input", command)
			}
		}
	}
	for _, want := range []string{"refresh", "activity_add", "activity_up:a_1", "activity_down:a_1", "activity_remove:a_1", "phase_add:a_1", "phase_remove:a_1:p_1", "water_effort_add:a_1:p_1:_root", "water_group_add:a_1:p_1:_root", "water_effort_add:a_1:p_1:n_1", "water_up:a_1:p_1:n_2", "water_down:a_1:p_1:n_2", "water_remove:a_1:p_1:n_2", "preview"} {
		if !commands[want] {
			t.Errorf("missing command %s", want)
		}
	}
	if !strings.Contains(body, "Recuperação entre repetições deste grupo") || !strings.Contains(body, "Recuperação depois deste esforço") || !strings.Contains(body, "Sem recuperação de grupo depois da última") || !strings.Contains(body, "incluindo a última") || !strings.Contains(body, "Dentro do grupo: Série principal") {
		t.Fatal("nested recovery levels became ambiguous")
	}
}

func TestFocusedWorkoutRawErrorsStayLinkedWithoutMarkingValidTime(t *testing.T) {
	p := workoutFixture()
	field := "activities[a_1].phases[p_1].water.target_distance_metres"
	p.Values.Set(field, "8km talvez")
	p.Values.Set("schedule.date", "2026-99-00")
	p.Errors = validation.FieldErrors{workoutErrorKey(field): "Indique metros ou deixe por definir.", "form": "A fonte mudou; reveja os dados.", "schedule.date": "Introduza uma data válida."}
	body := renderWorkout(t, p)
	assertWorkoutValue(t, body, field, "8km talvez")
	assertWorkoutValue(t, body, "schedule.date", "2026-99-00")
	for _, name := range []string{field, "schedule.date"} {
		control := workoutControl(t, body, name)
		if value, _ := workoutAttr(control, "aria-invalid"); value != "true" {
			t.Error("missing invalid", name)
		}
		if !strings.Contains(body, `href="#`+workoutFieldID(name)+`"`) {
			t.Error("missing field link", name)
		}
		if described, _ := workoutAttr(control, "aria-describedby"); !strings.Contains(described, workoutFieldID(name)+"-error") {
			t.Error("missing described error", name)
		}
	}
	if value, _ := workoutAttr(workoutControl(t, body, "schedule.start_time"), "aria-invalid"); value != "false" {
		t.Fatal("unrelated error marked valid start invalid")
	}
	if strings.Contains(body, `href="#workout-form"`) || strings.Contains(body, `href="#"`) {
		t.Fatal("global conflict falsely linked to a field")
	}
	if !strings.Contains(body, `tabindex="-1" autofocus`) {
		t.Fatal("error summary not focusable")
	}
	ids := map[string]bool{}
	for _, m := range regexp.MustCompile(`\sid="([^"]+)"`).FindAllStringSubmatch(body, -1) {
		if ids[m[1]] {
			t.Errorf("duplicate id %s", m[1])
		}
		ids[m[1]] = true
	}
	for _, m := range regexp.MustCompile(`\sfor="([^"]+)"`).FindAllStringSubmatch(body, -1) {
		if !ids[m[1]] {
			t.Errorf("orphan label %s", m[1])
		}
	}
	for _, m := range regexp.MustCompile(`\saria-describedby="([^"]+)"`).FindAllStringSubmatch(body, -1) {
		for _, id := range strings.Fields(m[1]) {
			if !ids[id] {
				t.Errorf("orphan description %s", id)
			}
		}
	}
}

func TestFocusedWorkoutPlainPhasesAndOptionalGymPreserveRawValues(t *testing.T) {
	p := workoutFixture()
	p.Activities[0].ID = ""
	p.Activities[0].Phases[0].ID = ""
	p.Values.Set("activities[a_1].phases[p_1].instructions", "")
	body := renderWorkout(t, p)
	if _, required := workoutAttr(workoutControl(t, body, "activities[a_1].phases[p_1].instructions"), "required"); required {
		t.Fatal("blank unused new phase became required placeholder")
	}
	if !strings.Contains(body, "Apenas instruções escritas") {
		t.Fatal("plain instructions not the primary detail choice")
	}
	p.Values.Set("activities[a_1].modality", "GYM")
	p.Values.Set("activities[a_1].phases[p_1].purpose", "CUSTOM")
	p.Values.Set("activities[a_1].phases[p_1].title", "Força A")
	p.Values.Set("activities[a_1].phases[p_1].instructions", "Mobilidade e força segundo as indicações.")
	p.Values.Set("activities[a_1].phases[p_1].detail_mode", "GYM")
	p.Values.Set("activities[a_1].phases[p_1].gym.structure", "SUPERSET")
	p.Values.Set("activities[a_1].phases[p_1].gym.objective", "CORE")
	p.Values.Set("activities[a_1].phases[p_1].gym.rounds", "4 voltas?")
	p.Activities[0].Phases[0].Exercises = []StructuredWorkoutExercise{{Key: "e_1", ID: "exercise"}}
	ep := "activities[a_1].phases[p_1].gym.exercises[e_1]"
	p.Values.Set(ep+".name", "Supino")
	p.Values.Set(ep+".resistance_kind", "COACH_INSTRUCTION")
	p.Values.Set(ep+".resistance_text", "Segundo indicação do treinador")
	p.Errors = validation.FieldErrors{workoutErrorKey(ep + ".resistance_value"): "Reveja a resistência."}
	body = renderWorkout(t, p)
	assertWorkoutValue(t, body, "activities[a_1].phases[p_1].gym.rounds", "4 voltas?")
	for _, f := range []string{"sets", "repetitions", "duration_seconds", "distance_metres", "recovery_seconds", "resistance_kind", "resistance_value", "resistance_text", "execution_intent", "tempo", "notes"} {
		workoutControl(t, body, ep+"."+f)
	}
	for _, want := range []string{"Força A", "Detalhar exercícios", "Circuito", "Supersérie", "Percentagem de 1RM", "Instrução do treinador", `value="gym_up:a_1:p_1:e_1"`, `value="gym_down:a_1:p_1:e_1"`, `value="gym_remove:a_1:p_1:e_1"`} {
		if !strings.Contains(body, want) {
			t.Error("missing", want)
		}
	}
	if !strings.Contains(body, `<details open><summary>Carga e execução`) {
		t.Fatal("hidden invalid resistance details were not opened")
	}
}

func TestFocusedWorkoutPreviewUsesCallerRendererAndBoundConfirmOnly(t *testing.T) {
	p := workoutFixture()
	p.Reviewing = true
	p.Preview = templ.Raw(`<p data-canonical-preview>INSTRUÇÃO CANÓNICA</p>`)
	p.ProposalJSON = `{"title":"</script><img src=x onerror=alert(1)>"}`
	p.ReviewToken = `signed"token`
	p.Values.Set("unexpected.private", "must-not-be-rendered")
	p.Removed = []StructuredWorkoutRemoval{{Kind: "PHASE", Key: "r_1", ID: "removed-id", Label: "Fase retirada", Payload: `{"instructions":"Texto incompleto </script>","node":"n_1"}`}}
	body := renderWorkout(t, p)
	if strings.Count(body, "INSTRUÇÃO CANÓNICA") != 1 || strings.Contains(body, `name="title" type=`) {
		t.Fatal("preview duplicated renderer or left editable live fields alongside confirmation")
	}
	assertWorkoutValue(t, body, "proposal", p.ProposalJSON)
	assertWorkoutValue(t, body, "review_token", p.ReviewToken)
	assertWorkoutValue(t, body, "intent", "confirm")
	assertWorkoutValue(t, body, "command", "edit")
	assertWorkoutValue(t, body, "removed[r_1].payload", p.Removed[0].Payload)
	if strings.Contains(body, "must-not-be-rendered") || strings.Contains(body, "<img") || strings.Contains(body, "<script") {
		t.Fatal("unallowlisted field or unescaped proposal leaked into markup")
	}
	if len(workoutTags(body, "form")) != 2 {
		t.Fatal("edit-back and confirmation must be separate forms")
	}
	for _, button := range workoutTags(body, "button") {
		if _, name := workoutAttr(button, "name"); name {
			t.Fatal("confirmation relies on disabled named submitter")
		}
	}
	p.Preview = nil
	body = renderWorkout(t, p)
	if strings.Contains(body, "Guardar treino") || strings.Contains(body, `action="/workout/confirm"`) {
		t.Fatal("missing canonical preview still allowed confirmation")
	}
	p.Preview = templ.Raw(`<p>Canonical</p>`)
	p.ReviewToken = ""
	body = renderWorkout(t, p)
	if strings.Contains(body, "Guardar treino") {
		t.Fatal("unsigned preview allowed confirmation")
	}
}

func TestFocusedWorkoutReadOnlyAndUnsupportedTopologyHaveNoMutationForms(t *testing.T) {
	for _, mode := range []string{"readonly", "excess", "cycle", "missing-parent", "duplicate-key", "foreign-key"} {
		t.Run(mode, func(t *testing.T) {
			p := workoutFixture()
			switch mode {
			case "readonly":
				p.ReadOnly = true
			case "excess":
				for len(p.Activities) < 9 {
					p.Activities = append(p.Activities, StructuredWorkoutActivity{Key: "a_" + strings.Repeat("x", len(p.Activities))})
				}
			case "cycle":
				p.Activities[0].Phases[0].WaterNodes = []StructuredWorkoutWaterNode{{Key: "n_1", ParentKey: "n_2"}, {Key: "n_2", ParentKey: "n_1"}}
			case "missing-parent":
				p.Activities[0].Phases[0].WaterNodes = []StructuredWorkoutWaterNode{{Key: "n_1", ParentKey: "n_missing"}}
			case "duplicate-key":
				p.Activities = append(p.Activities, p.Activities[0])
			case "foreign-key":
				p.Activities[0].Key = `a_1"><script>`
			}
			p.Preview = templ.Raw(`<p>Lecture sûre</p>`)
			p.ReviewToken = "signed"
			p.ProposalJSON = "{}"
			body := renderWorkout(t, p)
			if strings.Contains(body, "<form") || strings.Contains(body, "Guardar treino") || !strings.Contains(body, "Mais opções de planeamento") || !strings.Contains(body, "Voltar à semana") {
				t.Fatal("read-only fallback allowed mutation or lost recovery")
			}
		})
	}
}

func TestFocusedWorkoutEscapesTextAndNeverPrefillsCoachingNumbers(t *testing.T) {
	p := workoutFixture()
	attack := `"><img src=x onerror="alert(1)">`
	p.Values.Set("title", attack)
	p.GroupName = attack
	p.Values.Set("activities[a_1].phases[p_1].instructions", attack)
	body := renderWorkout(t, p)
	assertWorkoutValue(t, body, "title", attack)
	if strings.Contains(body, "<img") || strings.Contains(body, "<script") || !strings.Contains(body, "&lt;img") {
		t.Fatal("coach text escaped incorrectly")
	}
	for _, name := range []string{"activities[a_1].phases[p_1].water.target_distance_metres", "activities[a_1].phases[p_1].water.method", "activities[a_1].phases[p_1].water.profile_id"} {
		tag := workoutControl(t, body, name)
		if v, _ := workoutAttr(tag, "value"); v != "" {
			t.Errorf("default coaching field %s=%s", name, v)
		}
	}
	p.Values.Set("schedule.mode", "INTERVAL")
	p.Values.Set("schedule.end_date", "2026-11-10")
	p.Values.Set("schedule.end_time", "00:30")
	p.StartOccurrences = []StructuredWorkoutChoice{{Value: "first", Label: "Primeira ocorrência · UTC+01:00"}, {Value: "second", Label: "Segunda ocorrência · UTC+00:00"}}
	body = renderWorkout(t, p)
	for _, want := range []string{"Data de fim", "Ocorrência da hora de início", "UTC+01:00", "UTC+00:00"} {
		if !strings.Contains(body, want) {
			t.Error("missing explicit overnight/fold field", want)
		}
	}
}

func TestFocusedWorkoutTopologyBoundsAndRemovalConflict(t *testing.T) {
	p := workoutFixture()
	for i := 1; i <= 8; i++ {
		parent := ""
		if i > 1 {
			parent = fmt.Sprintf("n_%d", i-1)
		}
		p.Activities[0].Phases[0].WaterNodes = append(p.Activities[0].Phases[0].WaterNodes, StructuredWorkoutWaterNode{Key: fmt.Sprintf("n_%d", i), ParentKey: parent})
	}
	if !workoutTopologyValid(p) {
		t.Fatal("accepted depth8 was refused")
	}
	p.Activities[0].Phases[0].WaterNodes = append(p.Activities[0].Phases[0].WaterNodes, StructuredWorkoutWaterNode{Key: "n_9", ParentKey: "n_8"})
	if workoutTopologyValid(p) {
		t.Fatal("depth9 could recurse through editor")
	}
	p = workoutFixture()
	p.Activities[0].Phases[0].WaterNodes = nil
	for i := 0; i < 256; i++ {
		p.Activities[0].Phases[0].WaterNodes = append(p.Activities[0].Phases[0].WaterNodes, StructuredWorkoutWaterNode{Key: fmt.Sprintf("n_%d", i)})
	}
	if !workoutTopologyValid(p) {
		t.Fatal("accepted256nodes refused")
	}
	p.Activities[0].Phases[0].WaterNodes = append(p.Activities[0].Phases[0].WaterNodes, StructuredWorkoutWaterNode{Key: "n_256"})
	if workoutTopologyValid(p) {
		t.Fatal("257nodes accepted")
	}
	p = workoutFixture()
	p.Activities[0].Phases[0].WaterNodes = []StructuredWorkoutWaterNode{{Key: "n_1"}, {Key: "n_2"}, {Key: "n_3", ParentKey: "n_1"}}
	if workoutTopologyValid(p) {
		t.Fatal("preorder mismatch could display different order from hidden node_order")
	}
	p = workoutFixture()
	p.Removed = []StructuredWorkoutRemoval{{Key: "r_1", ID: p.Activities[0].ID, Payload: "{}"}}
	if workoutTopologyValid(p) {
		t.Fatal("same identity active and removed")
	}
}
func TestFocusedWorkoutInvalidReviewCannotOfferSave(t *testing.T) {
	p := workoutFixture()
	p.Reviewing = true
	p.Preview = templ.Raw("<p>Canonical</p>")
	p.ProposalJSON = "{}"
	p.ReviewToken = "old-review"
	p.Errors = validation.FieldErrors{"form": "A fonte mudou. Reveja o treino."}
	body := renderWorkout(t, p)
	if strings.Contains(body, "Guardar treino") || strings.Contains(body, `action="/workout/confirm"`) {
		t.Fatal("failed review retained live confirmation")
	}
	if !strings.Contains(body, "Continuar a editar") {
		t.Fatal("failed review lost original edit-back proposal")
	}
}

func TestFocusedWorkoutNativeDateFallbackRetainsStartAndEndYearZero(t *testing.T) {
	for _, value := range []string{"0000-01-01", "-0001-01-01", "0001-01-01", "2026-11-09"} {
		t.Run(value, func(t *testing.T) {
			p := workoutFixture()
			p.Values.Set("schedule.mode", "INTERVAL")
			p.Values.Set("schedule.date", value)
			p.Values.Set("schedule.end_date", value)
			p.Values.Set("schedule.end_time", "17:00")
			if strings.HasPrefix(value, "0000") || strings.HasPrefix(value, "-") {
				p.Errors = validation.FieldErrors{"schedule.date": "Introduza um ano positivo.", "schedule.end_date": "Introduza um ano positivo."}
			}
			body := renderWorkout(t, p)
			for _, name := range []string{"schedule.date", "schedule.end_date"} {
				assertWorkoutValue(t, body, name, value)
				want := "date"
				if len(p.Errors) > 0 {
					want = "text"
				}
				if got, _ := workoutAttr(workoutControl(t, body, name), "type"); got != want {
					t.Errorf("%s value%q type%q want%q", name, value, got, want)
				}
			}
		})
	}
}
func TestFocusedWorkoutSingleIntentionalAutofocus(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		errors        validation.FieldErrors
		want          string
	}{
		{"message only", "Reveja o treino.", nil, "message"},
		{"fields only", "", validation.FieldErrors{"title": "Indique o título."}, "summary"},
		{"message and fields", "A fonte mudou.", validation.FieldErrors{"title": "Indique o título."}, "summary"},
		{"empty error is not summary", "Reveja o treino.", validation.FieldErrors{"title": ""}, "message"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := workoutFixture()
			p.Message = tc.message
			p.Errors = tc.errors
			body := renderWorkout(t, p)
			count := 0
			target := ""
			for _, tag := range workoutTags(body, "section") {
				if _, ok := workoutAttr(tag, "autofocus"); !ok {
					continue
				}
				count++
				if labelled, _ := workoutAttr(tag, "aria-labelledby"); labelled == "workout-error-title" {
					target = "summary"
				} else if class, _ := workoutAttr(tag, "class"); class == "task-conflict" {
					target = "message"
				}
			}
			if count != 1 || target != tc.want {
				t.Fatalf("autofocus count%d target%q want%q", count, target, tc.want)
			}
		})
	}
}
