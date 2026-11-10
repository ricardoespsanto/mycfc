package pages

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/a-h/templ"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func workspaceFixture() StructuredTrainingPage {
	w := StructuredWeekWorkspace{Enabled: true, HasWeek: true, GroupID: "group", GroupName: "Grupo de exemplo", Scope: "Competição", WeekID: "week", WeekTitle: "Semana do clube", Season: "Época configurada", DateRange: "9–15 de novembro de 2026", MemberCount: 2, GroupsURL: "/grupos", GroupURL: "/grupos/group?week_id=week", AddSessionURL: "/planner?group_id=group&week_id=week&day=2026-11-09&task=session#criar-sessao-estruturada", CreateWeekURL: "/planner?group_id=group&task=week#criar-semana-treino", PublicationURL: "#training-publication", AdvancedURL: "#training-cycles", RefreshURL: "/planner?group_id=group&week_id=week", Publication: StructuredWorkspacePublication{State: "never"}, Weeks: []StructuredWorkspaceWeekChoice{{ID: "week", Title: "Semana do clube", StartsOn: "2026-11-09", EndsOn: "2026-11-15", Selected: true}}}
	for i := 0; i < 7; i++ {
		date := fmt.Sprintf("2026-11-%02d", 9+i)
		w.Days = append(w.Days, StructuredWorkspaceDay{Date: date, Label: fmt.Sprintf("Dia %d", i+1), ShortLabel: fmt.Sprint(i + 1), Selected: i == 0, URL: "/planner?group_id=group&week_id=week&day=" + date, AddSessionURL: "/planner?group_id=group&week_id=week&day=" + date + "&task=session#criar-sessao-estruturada"})
	}
	w.Days[0].Morning = []StructuredWorkspaceAppointment{{ID: "rest", Title: "Recuperação", EntryKind: "REST", StartsOn: "2026-11-09", EndsOn: "2026-11-09", StartsAt: "09:00", EndsAt: "09:30", URL: "/session/rest"}}
	w.Days[0].Afternoon = []StructuredWorkspaceAppointment{{ID: "hybrid", Title: "Água e ginásio", EntryKind: "TRAINING", StartsOn: "2026-11-09", EndsOn: "2026-11-09", StartsAt: "16:00", EndsAt: "18:00", URL: "/session/hybrid", Modalities: []string{"WATER", "GYM"}, Selected: true}, {ID: "cancelled", Title: "Sessão cancelada", EntryKind: "LOGISTICS", StartsOn: "2026-11-09", EndsOn: "2026-11-10", StartsAt: "23:00", EndsAt: "00:15", Status: "CANCELLED", URL: "/session/cancelled"}}
	return StructuredTrainingPage{Management: true, Workspace: w, SelectedDay: "2026-11-09", CSRFField: templ.Raw(""), PlannerReturnURL: "/planner?group_id=group&week_id=week&day=2026-11-09"}
}
func renderWorkspace(t *testing.T, p StructuredTrainingPage) string {
	t.Helper()
	var out bytes.Buffer
	if err := structuredWeekWorkspace(p).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}
func TestWeekWorkspaceSevenDaysAndTruthfulAppointments(t *testing.T) {
	p := workspaceFixture()
	p.SelectedSessionID = "hybrid"
	body := renderWorkspace(t, p)
	for i := 0; i < 7; i++ {
		if !strings.Contains(body, fmt.Sprintf(`id="week-day-2026-11-%02d"`, 9+i)) {
			t.Errorf("missing day %d", i)
		}
	}
	for _, want := range []string{`name="group_id" value="group"`, `name="week_id"`, `aria-current="date"`, `data-selected="false"`, `aria-label="Adicionar sessão · Dia 1, 2026-11-09"`, "Descanso", "Ainda sem planeamento", "16:00–18:00", "23:00–2026-11-10 00:15", "Cancelada", "Água + Ginásio", "Rascunho · nunca publicado"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Count(body, `href="/session/hybrid"`) != 1 {
		t.Fatal("hybrid duplicated instead of one appointment")
	}
}
func TestWeekWorkspacePublicationStates(t *testing.T) {
	for _, state := range []string{"never", "current", "changes", "unavailable", ""} {
		p := workspaceFixture()
		p.Workspace.Publication = StructuredWorkspacePublication{State: state, Revision: 3, ConflictCount: 2}
		body := renderWorkspace(t, p)
		label := structuredWorkspacePublicationLabel(p.Workspace.Publication)
		if !strings.Contains(body, label) {
			t.Errorf("missing state label %s", state)
		}
		unavailable := state == "unavailable" || state == ""
		if strings.Contains(body, "Voltar a confirmar o estado") != unavailable {
			t.Errorf("state %q recovery mismatch", state)
		}
		if unavailable && strings.Contains(body, "Rascunho") {
			t.Fatal("unknown publication claimed draft")
		}
		if state == "changes" && !strings.Contains(body, "continuam a ver a versão publicada") {
			t.Fatal("missing published-version explanation")
		}
	}
}
func TestWeekWorkspaceEmptyGroupHasExplicitWeekTask(t *testing.T) {
	p := workspaceFixture()
	p.Workspace.HasWeek = false
	p.Workspace.MemberCount = 0
	body := renderWorkspace(t, p)
	if !strings.Contains(body, "Sem destinatários") || !strings.Contains(body, "Criar semana") || !strings.Contains(body, "data explícita") {
		t.Fatal("missing empty group task")
	}
	if strings.Contains(body, `name="week_id"`) || strings.Contains(body, "2026-11-09") {
		t.Fatal("empty group invented week dates")
	}
}
func TestWeekWorkspaceNativeTaskAndAdvancedContext(t *testing.T) {
	p := workspaceFixture()
	p.OpenForm = "session"
	p.SelectedSessionID = "hybrid"
	p.SessionForm = StructuredTrainingSessionForm{PlanID: "week", Errors: validation.FieldErrors{"title": "Introduza um título."}}
	var out bytes.Buffer
	if err := structuredTrainingContent(p).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	for _, want := range []string{`id="workspace-details" class="week-advanced" open`, `id="criar-sessao-estruturada" class="week-native-task" open`, `name="starts_at" type="datetime-local" value=""`, `name="ends_at" type="datetime-local" value=""`, `name="return_to" value="/planner?group_id=group&amp;week_id=week&amp;day=2026-11-09"`, "Dia selecionado: 2026-11-09"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing native context %q", want)
		}
	}
	if strings.Contains(body, "Áreas do planeamento") || strings.Contains(body, `data-collection-tabs`) {
		t.Fatal("peer tabs remain main hierarchy")
	}
}
func TestWeekWorkspaceNewPresentationFieldsExcludedFromSnapshots(t *testing.T) {
	value := struct {
		Week        StructuredTrainingWeek
		Publication StructuredPublicationState
	}{Week: StructuredTrainingWeek{StartsOn: "raw-start", EndsOn: "raw-end", Sessions: []StructuredTrainingSession{{StartsOn: "session-date", EndsOn: "session-end", StartsAt: "raw-time", EndsAt: "end-time", Status: "CANCELLED"}}}, Publication: StructuredPublicationState{Current: true, Unavailable: true}}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"StartsOn", "EndsOn", "StartsAt", "EndsAt", "CANCELLED", "Current", "Unavailable", "raw-time"} {
		if strings.Contains(string(raw), bad) {
			t.Errorf("presentation leaked into snapshot: %s", bad)
		}
	}
}
func TestWeekWorkspaceMissingTimeNeverFabricatesInterval(t *testing.T) {
	if got := structuredWorkspaceTime(StructuredWorkspaceAppointment{}); got != "Hora por definir" {
		t.Fatal(got)
	}
	if got := structuredWorkspaceTime(StructuredWorkspaceAppointment{StartsAt: "16:00"}); got != "16:00 · Fim por definir" {
		t.Fatal(got)
	}
}

func TestWeekWorkspacePublicationUnavailableHasNoMutation(t *testing.T) {
	p := workspaceFixture()
	p.PublicationUnavailable = true
	p.Publications = []StructuredPublicationState{{PlanID: "week", Plan: "Semana", Revision: 2, ChangedCount: 1, PrescriptionCount: 1}}
	var out bytes.Buffer
	if err := structuredPublicationManagement(p).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	if !strings.Contains(body, "Não foi possível confirmar o estado de publicação") || strings.Contains(body, `/week/publicar`) {
		t.Fatal("unavailable publication offered a mutation or missing recovery")
	}
}
func TestWeekWorkspaceAthleteViewKeepsPublishedAudience(t *testing.T) {
	p := workspaceFixture()
	p.Management = false
	p.Audiences = []StructuredTrainingAudience{{AthleteName: "Atleta exemplo", GroupName: "Grupo autorizado", Weeks: []StructuredTrainingWeek{{ID: "published", Title: "Semana publicada"}}}}
	var out bytes.Buffer
	if err := structuredTrainingContent(p).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	if !strings.Contains(body, "Semana publicada") || !strings.Contains(body, "Atividade · Treinos") || strings.Contains(body, `class="week-workspace"`) || strings.Contains(body, "Coordenação · Planeamento semanal") {
		t.Fatal("management workspace changed athlete content")
	}
}
func TestWeekWorkspaceValidationOpensOnlyAffectedNativeWeekTask(t *testing.T) {
	p := workspaceFixture()
	p.WeekForm.Errors = validation.FieldErrors{"week_start": "Reveja a data."}
	var out bytes.Buffer
	if err := structuredWeekForm(p).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	if !strings.Contains(body, `id="criar-semana-treino" class="week-native-task" open`) || !strings.Contains(body, `href="#structured-week-start"`) || !strings.Contains(body, `aria-invalid="true"`) || !strings.Contains(body, `role="alert" tabindex="-1" autofocus`) {
		t.Fatal("week validation lost native recovery")
	}
}

func TestWeekWorkspacePublicationReviewScopedToExactWeek(t *testing.T) {
	p := workspaceFixture()
	p.SelectedWeekID = "week"
	p.Publications = []StructuredPublicationState{{PlanID: "other", Plan: "Outra semana"}, {PlanID: "week", Plan: "Semana selecionada"}}
	var out bytes.Buffer
	if err := structuredPublicationManagement(p).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	if strings.Contains(body, "Outra semana") || !strings.Contains(body, "Semana selecionada") {
		t.Fatal("publication review escaped selected week")
	}
	p.Workspace.Enabled = false
	if len(structuredWorkspacePublications(p)) != 2 {
		t.Fatal("legacy publication list changed")
	}
}

func TestWorkspacePrescriptionCountIsPreviewNotDeliveryClaim(t *testing.T) {
	body := renderWorkspace(t, StructuredTrainingPage{Workspace: StructuredWeekWorkspace{Enabled: true, HasWeek: true, GroupID: "g", GroupName: "Grupo", WeekID: "w", Publication: StructuredWorkspacePublication{State: "never", PrescriptionCount: 3, AthleteCount: 2}}})
	if !strings.Contains(body, "3 prescrições previstas") {
		t.Fatalf("prospective count label missing: %s", body)
	}
}

func TestNativeWorkspaceGetFormsRetainSelectedEmptyDay(t *testing.T) {
	p := workspaceFixture()
	p.SelectedDay = "2026-11-14"
	p.Workspace.SelectedDay = p.SelectedDay
	p.SelectedSessionID = ""
	if body := renderWorkspace(t, p); !strings.Contains(body, `name="day" value="2026-11-14"`) {
		t.Fatal("week selector drops exact day")
	}
	var out bytes.Buffer
	if err := structuredRoutineLibrary(p).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `name="day" value="2026-11-14"`) || !strings.Contains(out.String(), `action="/admin/treinos/estruturados#training-routines"`) {
		t.Fatal("routine filter drops day/native review target")
	}
}
func TestWorkspaceSessionFormConstrainsPlanWithoutChangingLegacyChoices(t *testing.T) {
	p := workspaceFixture()
	p.SelectedWeekID = "selected-week"
	p.SessionForm.PlanID = p.SelectedWeekID
	p.Weeks = []StructuredTrainingChoice{{ID: p.SelectedWeekID, Name: "Selected week"}, {ID: "other-week", Name: "Other week"}}
	for _, enabled := range []bool{true, false} {
		p.Workspace.Enabled = enabled
		var out bytes.Buffer
		if err := structuredSessionForm(p).Render(context.Background(), &out); err != nil {
			t.Fatal(err)
		}
		hasOther := strings.Contains(out.String(), `value="other-week"`)
		if hasOther == enabled {
			t.Fatalf("workspace=%v alternate choice=%v", enabled, hasOther)
		}
	}
}
func TestUnavailablePublicationComparisonDoesNotClaimNoMaterialChanges(t *testing.T) {
	p := workspaceFixture()
	p.SelectedWeekID = "actual-week"
	p.Publications = []StructuredPublicationState{{PlanID: p.SelectedWeekID, Plan: "Actual plan", Revision: 2, PrescriptionCount: 4, Unavailable: true}}
	var out bytes.Buffer
	if err := structuredPublicationManagement(p).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	if !strings.Contains(body, "Não foi possível confirmar o estado de publicação") || strings.Contains(body, "Não há alterações materiais") || strings.Contains(body, "/publicar") || strings.Contains(body, "4 prescrições") {
		t.Fatalf("failed comparison asserted a publication fact: %s", body)
	}
}

func TestWeekAndSessionErrorsHaveUniqueAccessibleAssociations(t *testing.T) {
	p := workspaceFixture()
	p.OpenForm = "session"
	p.SessionForm.Errors = validation.FieldErrors{"title": "O título deve ter entre 2 e 180 caracteres.", "description": "Reveja as instruções."}
	p.WeekForm.Errors = validation.FieldErrors{"planned_load_percentage": "Indique uma percentagem até 100.", "description": "Reveja as notas."}
	p.WeekForm.PlannedLoad = "101"
	var out bytes.Buffer
	if err := structuredTrainingContent(p).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	for _, owner := range []string{"week", "session"} {
		fields := []string{"group", "name", "start", "load", "description"}
		if owner == "session" {
			fields = []string{"week", "name", "kind", "start", "end", "description"}
		}
		for _, field := range fields {
			id := "structured-" + owner + "-" + field
			if strings.Count(body, `id="`+id+`-error"`) != 1 {
				t.Errorf("error target %s must be unique", id)
			}
			opening := regexp.MustCompile(`<[^>]+id="` + id + `"[^>]*>`).FindString(body)
			if !strings.Contains(opening, id+"-error") {
				t.Errorf("control %s lost its own error association: %s", id, opening)
			}
		}
	}
	for _, expected := range []string{`href="#structured-week-load"`, `id="structured-week-load" aria-invalid="true"`, `value="101"`, `id="structured-session-name-error" class="field-error">O título deve ter entre 2 e 180 caracteres.`, `href="#structured-session-description"`, `href="#structured-week-description"`} {
		if !strings.Contains(body, expected) {
			t.Errorf("missing accessible feedback %q", expected)
		}
	}
}

func TestWeekWorkspaceFocusedWaterEntryRetainsGenericPaths(t *testing.T) {
	p := workspaceFixture()
	p.Workspace.WaterCreateURL = "/admin/treinos/estruturados/semanas/week/treinos/criar?day=2026-11-09&context=week"
	for i := range p.Workspace.Days {
		p.Workspace.Days[i].WaterCreateURL = "/admin/treinos/estruturados/semanas/week/treinos/criar?day=" + p.Workspace.Days[i].Date
	}
	body := renderWorkspace(t, p)
	for _, href := range append([]string{p.Workspace.WaterCreateURL, p.Workspace.AddSessionURL, p.Workspace.AdvancedURL}, p.Workspace.Days[0].AddSessionURL, p.Workspace.Days[6].WaterCreateURL) {
		if !strings.Contains(body, `href="`+html.EscapeString(href)+`"`) {
			t.Fatalf("lost scoped entry %s", href)
		}
	}
	if strings.Count(body, ">Treino de água</a>") != 8 || strings.Count(body, ">Adicionar sessão</a>") != 8 || !strings.Contains(body, `aria-label="Treino de água · Dia 7, 2026-11-15"`) {
		t.Fatal("header/day focused links must coexist with generic entries")
	}
	if dir := os.Getenv("WORKSPACE_COMPONENT_EVIDENCE"); dir != "" {
		if !filepath.IsAbs(dir) || !strings.HasPrefix(filepath.Clean(dir), filepath.Clean(os.TempDir())+string(os.PathSeparator)) {
			t.Fatal("evidence must use absolute temporary directory")
		}
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		document := `<!doctype html><html lang="pt-PT"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Componente da semana</title><link rel="stylesheet" href="/assets/app.css"><script defer src="/assets/app.js"></script></head><body class="app-body"><main id="conteudo-principal" class="page-shell">` + body + `</main></body></html>`
		if err := os.WriteFile(filepath.Join(dir, "week-links.html"), []byte(document), 0600); err != nil {
			t.Fatal(err)
		}
	}
	p.Workspace.WaterCreateURL = ""
	p.Workspace.Days[0].WaterCreateURL = ""
	body = renderWorkspace(t, p)
	if strings.Count(body, ">Treino de água</a>") != 6 || strings.Count(body, ">Adicionar sessão</a>") != 8 {
		t.Fatal("only empty focused URLs should hide focused entries")
	}
	p.Workspace.HasWeek = false
	if body = renderWorkspace(t, p); strings.Contains(body, "Treino de água") || !strings.Contains(body, "Criar semana") {
		t.Fatal("no week must retain explicit week creation")
	}
}
