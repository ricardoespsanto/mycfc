package pages

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/a-h/templ"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"html"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func reuseFixture() StructuredReuseTaskPage {
	return StructuredReuseTaskPage{
		CSRFField: templ.Raw(`<input type="hidden" name="_csrf" value="synthetic">`),
		Values:    url.Values{"source_kind": {"SESSION"}, "source_plan_id": {"source-plan"}, "source_session_id": {"source-session"}, "target_group_id": {"target-group"}, "season_id": {"configured-season"}, "week_start": {"2026-11-16"}, "week_title": {"Semana preparada"}, "mappings[m_1].date": {"2026-11-16"}, "mappings[m_1].start_time": {"16:00:30.100"}, "mappings[m_1].end_date": {"2026-11-16"}, "mappings[m_1].end_time": {"18:00"}},
		Source:    StructuredReuseContext{Label: "Sessão de origem", Group: "Grupo de origem", Season: "Época configurada", Week: "Semana de origem", Day: "2026-11-09", Item: "Água"}, Target: StructuredReuseContext{Group: "Grupo de destino", Season: "Época configurada", Week: "Semana escolhida", Day: "2026-11-16"},
		Groups: []StructuredWorkoutChoice{{Value: "target-group", Label: "Grupo de destino"}}, Seasons: []StructuredWorkoutChoice{{Value: "configured-season", Label: "Época configurada"}},
		Mappings: []StructuredReuseMapping{{Key: "m_1", SourceLabel: "9 novembro · Água", Precision: "INTERVAL", NeedsStart: true, NeedsEnd: true}},
		DraftURL: "/admin/treinos/estruturados/reutilizar/rascunho", PreviewURL: "/admin/treinos/estruturados/reutilizar/prever", ConfirmURL: "/admin/treinos/estruturados/reutilizar/confirmar", ReturnURL: "/admin/treinos/estruturados?group_id=group&week_id=week#training-plan", ReloadURL: "/admin/treinos/estruturados/reutilizar?source_kind=SESSION", RoutineURL: "/admin/treinos/estruturados/reutilizar?source_kind=ROUTINE",
	}
}
func renderReuse(t *testing.T, p StructuredReuseTaskPage) string {
	t.Helper()
	var out bytes.Buffer
	if err := structuredReuseContent(p).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	reuseWriteEvidence(t, body)
	return body
}
func reuseWriteEvidence(t *testing.T, body string) {
	t.Helper()
	if dir := os.Getenv("REUSE_COMPONENT_EVIDENCE"); dir != "" {
		if !filepath.IsAbs(dir) || !strings.HasPrefix(filepath.Clean(dir), filepath.Clean(os.TempDir())+string(os.PathSeparator)) {
			t.Fatal("evidence must use absolute temporary directory")
		}
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		document := `<!doctype html><html lang="pt-PT"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Reutilizar treinos — componente</title><link rel="stylesheet" href="/assets/app.css"><script defer src="/assets/app.js"></script></head><body class="app-body"><main id="conteudo-principal">` + body + `</main></body></html>`
		name := strings.ReplaceAll(t.Name(), "/", "-") + fmt.Sprintf("-%x.html", sha256.Sum256([]byte(body)))
		if err := os.WriteFile(filepath.Join(dir, name), []byte(document), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
func reuseCanonicalPair() (templ.Component, templ.Component) {
	source := focusedInstructionFixture()
	target := focusedInstructionFixture()
	target.ID = "destination-session"
	target.Title = "Treino independente"
	target.Segments[0].ID = "destination-segment"
	return StructuredWorkoutInstructions(source), StructuredWorkoutInstructions(target)
}
func TestReuseNativeEditorScopeAndMappingRetention(t *testing.T) {
	p := reuseFixture()
	p.Values.Set("week_description", "\nNotas <próprias>\nsegunda\tlinha")
	p.Values.Set("planned_load_percentage", "3e!&\"")
	p.ProposalJSON = `{"destination":"same-id"}`
	p.ReviewToken = `original-token<&"`
	p.Values.Set("prior_proposal", "untrusted-recursion")
	p.Values.Set("unexpected.private", "never-render")
	body := renderReuse(t, p)
	for name, want := range map[string]string{"source_kind": "SESSION", "source_plan_id": "source-plan", "source_session_id": "source-session", "mapping_order": "m_1", "prior_proposal": p.ProposalJSON, "prior_review_token": p.ReviewToken, "mappings[m_1].start_time": "16:00:30.100", "planned_load_percentage": "3e!&\""} {
		assertWorkoutValue(t, body, name, want)
	}
	if strings.Contains(body, "never-render") || strings.Contains(body, "untrusted-recursion") || strings.Contains(body, `name="target_plan_id"`) || strings.Contains(body, `name="mappings[m_1].mode"`) {
		t.Fatal("only fixed source/raw target grammar is allowed; precision is not authority")
	}
	if _, readonly := workoutAttr(workoutControl(t, body, "mappings[m_1].date"), "readonly"); readonly {
		t.Fatal("target date must be explicit editable mapping")
	}
	for _, tag := range workoutTags(body, "button") {
		if v, _ := workoutAttr(tag, "value"); v == "refresh" {
			if _, ok := workoutAttr(tag, "formnovalidate"); !ok {
				t.Fatal("draft refresh must work with incomplete required values")
			}
		}
	}
	if strings.Contains(body, `name="target_session_id"`) || strings.Contains(body, `name="target_segment_id"`) {
		t.Fatal("session copy must not offer fragment target selectors")
	}
	if !strings.Contains(body, "Época configurada") || !strings.Contains(body, "Origem") || !strings.Contains(body, "Destino") {
		t.Fatal("source and configured destination must be explicit")
	}
}
func TestReuseExactErrorsAndClockPrecisionStayNativeSafe(t *testing.T) {
	for _, tc := range []struct{ name, value, kind string }{{"seconds", "16:00:30", "time"}, {"milliseconds", "16:00:30.100", "time"}, {"nanoseconds", "16:00:30.123456789", "text"}, {"invalid", "25:92", "text"}} {
		t.Run(tc.name, func(t *testing.T) {
			p := reuseFixture()
			p.Values.Set("mappings[m_1].start_time", tc.value)
			p.Message = "Reveja o horário."
			p.Errors = validation.FieldErrors{"mappings[m_1].start_time": "Hora inválida.", "source_digest": "O contexto mudou."}
			body := renderReuse(t, p)
			control := workoutControl(t, body, "mappings[m_1].start_time")
			if kind, _ := workoutAttr(control, "type"); kind != tc.kind {
				t.Fatal(kind)
			}
			if step, _ := workoutAttr(control, "step"); step != "any" {
				t.Fatal("represented seconds must not be rounded")
			}
			assertWorkoutValue(t, body, "mappings[m_1].start_time", tc.value)
			if !strings.Contains(body, `href="#workout-mappings-m_1-start_time"`) || strings.Contains(body, `href="#workout-source_digest"`) || strings.Count(body, " autofocus") != 1 || strings.Index(body, `class="error-summary"`) > strings.Index(body, `class="task-conflict"`) {
				t.Fatal("exact field errors/focus must link only visible responsible fields")
			}
		})
	}
	p := reuseFixture()
	p.Mappings[0].Precision = "BAND"
	p.Mappings[0].Band = "Manhã"
	p.Mappings[0].NeedsStart = false
	p.Mappings[0].NeedsEnd = false
	for _, n := range []string{"start_time", "end_time", "end_date"} {
		p.Values.Del("mappings[m_1]." + n)
	}
	body := renderReuse(t, p)
	if strings.Contains(body, `name="mappings[m_1].start_time"`) || strings.Contains(body, `name="mappings[m_1].end_time"`) {
		t.Fatal("DAY/BAND must not manufacture clocks")
	}
	p.Values.Set("mappings[m_1].end_time", "raw-conflict")
	body = renderReuse(t, p)
	assertWorkoutValue(t, body, "mappings[m_1].end_time", "raw-conflict")
	p.Values.Set("mappings[m_1].date", "0000-01-01")
	body = renderReuse(t, p)
	if kind, _ := workoutAttr(workoutControl(t, body, "mappings[m_1].date"), "type"); kind != "text" {
		t.Fatal("invalid raw dates must survive native parsing")
	}
}
func TestReuseFragmentTargetsUseTrustedApplicabilityWithoutDefaults(t *testing.T) {
	p := reuseFixture()
	p.Values.Set("source_kind", "ROUTINE")
	p.Values.Set("routine_id", "configured-block")
	p.Values.Del("source_session_id")
	p.Values.Del("source_plan_id")
	p.Mappings = nil
	p.Sessions = []StructuredWorkoutChoice{}
	p.Segments = []StructuredWorkoutChoice{}
	body := renderReuse(t, p)
	for _, name := range []string{"target_session_id", "target_segment_id"} {
		if _, required := workoutAttr(workoutControl(t, body, name), "required"); !required {
			t.Fatal("applicable empty selector remains required")
		}
	}
	if !strings.Contains(body, "Não existem sessões elegíveis") || !strings.Contains(body, "Não existem atividades compatíveis") || strings.Contains(body, `name="mapping_order"`) || strings.Contains(body, `name="mappings[`) {
		t.Fatal("fragment must show honest target facts with no fabricated calendar mappings")
	}
	p.Sessions = nil
	p.Segments = nil
	p.Values.Set("target_segment_id", "retained-invalid-target")
	body = renderReuse(t, p)
	if !strings.Contains(body, `value="retained-invalid-target" selected`) {
		t.Fatal("raw contradictory target selection lost")
	}
}
func TestReuseReviewUsesCanonicalComponentsAndSeparateSignedConfirmation(t *testing.T) {
	p := reuseFixture()
	p.Reviewing = true
	p.SourcePreview, p.TargetPreview = reuseCanonicalPair()
	p.ProposalJSON = `{"target":"</script><img src=x>"}`
	p.ReviewToken = `signed-token<&"`
	body := renderReuse(t, p)
	forms := regexp.MustCompile(`(?s)<form\b.*?</form>`).FindAllString(body, -1)
	if len(forms) != 2 {
		t.Fatal("edit-back and confirm must be separate")
	}
	for name, want := range map[string]string{"command": "refresh", "prior_proposal": p.ProposalJSON, "prior_review_token": p.ReviewToken, "mapping_order": "m_1"} {
		assertWorkoutValue(t, forms[0], name, want)
	}
	for name, want := range map[string]string{"intent": "confirm", "proposal": p.ProposalJSON, "review_token": p.ReviewToken} {
		assertWorkoutValue(t, forms[1], name, want)
	}
	if strings.Contains(forms[1], `name="prior_`) || strings.Contains(body, "<img") || !strings.Contains(body, "não publica para os atletas") || !strings.Contains(body, "Confirmar cópia em rascunho") || strings.Count(body, "Objetivo do treinador") != 2 {
		t.Fatal("canonical independent draft review contract")
	}
	for _, button := range workoutTags(forms[1], "button") {
		if _, ok := workoutAttr(button, "name"); ok {
			t.Fatal("confirmation cannot depend on disabled named submitter")
		}
	}
	p.Errors = validation.FieldErrors{"target_digest": "O destino mudou."}
	if body = renderReuse(t, p); strings.Contains(body, `action="`+p.ConfirmURL+`"`) {
		t.Fatal("invalid review cannot confirm")
	}
	p.Errors = nil
	p.SourcePreview = nil
	if body = renderReuse(t, p); strings.Contains(body, `action="`+p.ConfirmURL+`"`) {
		t.Fatal("missing canonical source must not offer confirmation")
	}
}
func TestReuseRecoveryNoWorkUnknownAndWholeOverflow(t *testing.T) {
	t.Run("expired proof", func(t *testing.T) {
		p := reuseFixture()
		p.PreviewURL = ""
		p.Message = "Copie os dados e consulte a semana antes de iniciar outra tarefa."
		p.ProposalJSON = `{"original":"same-id"}`
		p.ReviewToken = "expired-proof"
		p.Values.Set("week_title", "Rascunho próprio")
		body := renderReuse(t, p)
		assertWorkoutValue(t, body, "week_title", "Rascunho próprio")
		assertWorkoutValue(t, body, "prior_review_token", "expired-proof")
		if strings.Contains(body, `value="preview"`) || !strings.Contains(body, "Voltar à semana") || !strings.Contains(body, p.Message) {
			t.Fatal("unusable proof keeps editable own data but no preview renewal")
		}
	})
	t.Run("unknown", func(t *testing.T) {
		p := reuseFixture()
		p.Unknown = true
		p.DestinationURL = "/destination"
		p.AdaptURL = "/adapt"
		p.SourcePreview = templ.Raw(`<p>must-not-appear</p>`)
		body := renderReuse(t, p)
		if strings.Contains(body, "<form") || strings.Contains(body, "must-not-appear") || strings.Contains(body, "Adaptar para") || strings.Contains(body, "Nenhuma cópia") || !strings.Contains(body, "Consultar destino") {
			t.Fatal("unknown result offers reconciliation links only")
		}
	})
	t.Run("no work", func(t *testing.T) {
		p := reuseFixture()
		p.NoWork = true
		body := renderReuse(t, p)
		if strings.Contains(body, "<form") || !strings.Contains(body, "Não existe conteúdo para copiar") {
			t.Fatal("empty source must not manufacture a copy")
		}
	})
	for _, bad := range []string{"duplicate", "invalid", "overflow"} {
		t.Run(bad, func(t *testing.T) {
			p := reuseFixture()
			switch bad {
			case "duplicate":
				p.Mappings = append(p.Mappings, p.Mappings[0])
			case "invalid":
				p.Mappings[0].Key = "foreign-key"
			case "overflow":
				for i := 2; i <= 65; i++ {
					p.Mappings = append(p.Mappings, StructuredReuseMapping{Key: fmt.Sprintf("m_%d", i)})
				}
			}
			body := renderReuse(t, p)
			if strings.Contains(body, "<form") || !strings.Contains(body, "operação completa") {
				t.Fatal("invalid topology refuses whole operation without truncation")
			}
		})
	}
}
func TestReuseLibraryUsesActualRowsScopedFiltersAndHonestEmptyState(t *testing.T) {
	p := reuseFixture()
	p.ShowRoutineLibrary = true
	p.Values.Set("routine_query", "Força")
	p.Values.Set("week_description", "private-own-notes")
	p.ProposalJSON = "never-in-url-proof"
	p.ReviewToken = "never-in-url-token"
	p.NextURL = "/routines?routine_page=2"
	body := renderReuse(t, p)
	if !strings.Contains(body, "Sem rotinas visíveis") || strings.Contains(body, "Força A</") || strings.Contains(body, "private-own-notes") || strings.Contains(body, "never-in-url-") || strings.Contains(body, `name="week_description"`) {
		t.Fatal("empty library cannot seed routines or leak own proof/free text into GET")
	}
	p.Routines = []StructuredReuseRoutine{{ID: "routine", Name: "Força A <configurada>", Kind: "BLOCK", Description: "Carga do treinador", URL: "/choose?routine_id=actual&group=target"}}
	body = renderReuse(t, p)
	if !strings.Contains(body, html.EscapeString(p.Routines[0].Name)) || strings.Contains(body, "<configurada>") || !strings.Contains(body, `href="/choose?routine_id=actual&amp;group=target"`) || !strings.Contains(body, `name="routine_tag"`) {
		t.Fatal("configured routine/catalog filters missing")
	}
	for len(p.Routines) <= 20 {
		p.Routines = append(p.Routines, p.Routines[0])
	}
	if body = renderReuse(t, p); strings.Contains(body, "<form") || strings.Contains(body, ">Usar Força") {
		t.Fatal("catalog sentinel must not become a truncated or bulk selectable library")
	}
}
func TestReuseCanonicalFragmentHasNoFabricatedAppointment(t *testing.T) {
	s := focusedInstructionFixture()
	var out bytes.Buffer
	if err := StructuredWorkoutFragment(s.Segments).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	if strings.Contains(body, "structured-session-") || !strings.Contains(body, `<h3 id="segment-segment">`) || strings.Contains(body, "2026-11-09") || strings.Contains(body, "16:00") || strings.Contains(body, "<form") || strings.Contains(body, "Resposta do atleta") || !strings.Contains(body, "Objetivo do treinador") || strings.Count(body, "Água livre") != 1 {
		t.Fatal("fragment must reuse only actual canonical activity instructions")
	}
}
func TestReuseAdaptContextUsesOneNativeExistingVariationForm(t *testing.T) {
	p := workspaceFixture()
	p.OpenForm = "variation"
	p.CSRFField = templ.Raw(`<input type="hidden" name="_csrf" value="synthetic">`)
	p.VariationTargets = []StructuredVariationTargetChoice{{Value: "ATHLETE:00000000-0000-4000-8000-000000000001", Name: "Destinatário de exemplo", GroupID: "group"}}
	p.SelectedWeekID = "selected-week"
	p.PlannerReturnURL = "/admin/treinos/estruturados?group_id=group&week_id=week&session_id=session&day=2026-11-09#training-plan"
	p.Weeks = []StructuredTrainingChoice{{ID: "selected-week", Name: "Semana atual", GroupID: "group"}, {ID: "other-week", Name: "Outra semana", GroupID: "other-group"}}
	p.VariationSubjects = []StructuredVariationSubjectChoice{{Value: "BLOCK:actual", Name: "Bloco atual", PlanID: "selected-week"}, {Value: "BLOCK:other", Name: "Outro bloco", PlanID: "other-week"}}
	body := renderScheduleComponent(t, structuredVariationManagement(p))
	reuseWriteEvidence(t, body)
	if !strings.Contains(body, `<details id="add-training-variation" class="week-native-task" open`) || strings.Count(body, `action="/admin/treinos/estruturados/variacoes"`) != 1 || strings.Contains(body, `<dialog id="add-training-variation"`) || strings.Contains(body, "BLOCK:other") || strings.Contains(body, `value="SESSION:`) {
		t.Fatal("context adaptation must be native, scoped and one existing protocol")
	}
	if !strings.Contains(body, `value="selected-week" data-group="group" selected`) || !strings.Contains(body, "Precedência:") || !strings.Contains(body, "conflito") || !strings.Contains(body, `name="return_to" value="`+html.EscapeString(p.PlannerReturnURL)+`"`) {
		t.Fatal("explicit plan/context and visible precedence required")
	}
	ids := map[string]bool{}
	for _, tag := range regexp.MustCompile(`<[^>]+\bid="[^"]+"[^>]*>`).FindAllString(body, -1) {
		id, _ := workoutAttr(tag, "id")
		if ids[id] {
			t.Fatal("duplicate variation ID", id)
		}
		ids[id] = true
	}
	p.OpenForm = ""
	body = renderScheduleComponent(t, structuredVariationManagement(p))
	if !strings.Contains(body, `<dialog id="add-training-variation"`) || !strings.Contains(body, "BLOCK:other") || strings.Count(body, `action="/admin/treinos/estruturados/variacoes"`) != 1 {
		t.Fatal("legacy advanced flow stays available with same shared form")
	}
}

func TestReuseMappingBoundRetainsAll64InTrustedOrder(t *testing.T) {
	p := reuseFixture()
	p.Mappings = nil
	for i := 1; i <= 64; i++ {
		key := fmt.Sprintf("m_%d", i)
		p.Mappings = append(p.Mappings, StructuredReuseMapping{Key: key, SourceLabel: fmt.Sprintf("Sessão %d", i), Precision: "DAY"})
		p.Values.Set("mappings["+key+"].date", "2026-11-16")
	}
	p.Values["mapping_order"] = []string{"untrusted-order"}
	body := renderReuse(t, p)
	order := []string{}
	for _, tag := range workoutTags(body, "input") {
		if name, _ := workoutAttr(tag, "name"); name == "mapping_order" {
			v, _ := workoutAttr(tag, "value")
			order = append(order, v)
		}
	}
	if len(order) != 64 || order[0] != "m_1" || order[63] != "m_64" || strings.Contains(body, "untrusted-order") || !strings.Contains(body, `value="preview"`) {
		t.Fatal("accepted bound must retain all trusted mappings without truncating/reordering")
	}
	assertWorkoutValue(t, body, "mappings[m_64].date", "2026-11-16")
}
