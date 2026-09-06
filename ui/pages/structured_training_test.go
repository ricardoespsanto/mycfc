package pages

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/cfcoimbra/mycfc/ui/components"
)

func TestStructuredTrainingRendersAdminIntensityProfilesAndPlannedTotals(t *testing.T) {
	page := StructuredTrainingPage{
		Management:             true,
		CanManageWaterProfiles: true,
		CSRFField:              templ.Raw(""),
		SelectedGroupID:        "group-1",
		SelectedWeekID:         "week-1",
		SelectedSessionID:      "session-1",
		WaterProfiles: []StructuredWaterProfile{{
			ID: "profile-1", Name: "Perfil do clube", Craft: "Kayak", Revision: 2,
			Zones: []StructuredWaterZone{{Code: "R7", Label: "Ritmo de prova", Cadence: "Sem cadência fixa", Meaning: "Ritmo sustentável para a duração prescrita"}},
		}},
		Audiences: []StructuredTrainingAudience{{GroupID: "group-1", GroupName: "Seniores", Weeks: []StructuredTrainingWeek{{ID: "week-1", Title: "M41", Season: "2026", DateRange: "17/08/2026–23/08/2026", PlannedLoad: "70%", Summary: StructuredTrainingWeekSummary{PlannedWater: []StructuredTrainingSummaryItem{{Label: "Distância planeada", Value: "26 km", Certainty: "estimada"}}, Actual: []StructuredTrainingSummaryItem{{Label: "Duração real", Value: "2 h", Certainty: "registada em 2 sessões"}}}, Sessions: []StructuredTrainingSession{{ID: "session-1", Title: "Água", When: "17/08/2026 10:00–11:00", EntryKind: "TRAINING", Segments: []StructuredTrainingSegment{{ID: "segment-1", Modality: "WATER", Blocks: []StructuredTrainingBlock{{ID: "block-1", Purpose: "MAIN", Title: "Série", Instructions: "Manter qualidade", WaterMethod: "Intervalos", WaterProfile: "Perfil do clube · Kayak · revisão 2", WaterTotals: []StructuredWaterTotal{{Label: "Esforço planeado", Value: "27 min", Certainty: "exato"}}}}}}}}}}}},
	}
	var output bytes.Buffer
	if err := structuredTrainingContent(page).Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	html := output.String()
	for _, expected := range []string{"Perfis de intensidade", "Ritmo sustentável", "O limite inferior de R5", "Esforço planeado", "27 min", "revisão 2", "Resumo semanal", "Carga planeada", "70%", "Registo real do atleta", "Ver prescrições e respostas dos atletas", "Contexto de edição", "Plano selecionado", "Sessões da semana selecionada"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("rendered page missing %q", expected)
		}
	}
	if !strings.Contains(html, "name=\"group_id\"") || !strings.Contains(html, "aria-current=\"page\"") {
		t.Fatalf("planner context did not retain its selected group/session: %s", html)
	}
}

func TestStructuredTrainingHidesProfileAdministrationFromCoach(t *testing.T) {
	page := StructuredTrainingPage{Management: true, CSRFField: templ.Raw("")}
	var output bytes.Buffer
	if err := structuredTrainingContent(page).Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "Criar perfil ou nova revisão") {
		t.Fatal("profile administration rendered without administrator permission")
	}
}

func TestStructuredTrainingRendersOptionalCyclesWithTextualDistributionAndIndependentCopy(t *testing.T) {
	cycleID := "00000000-0000-0000-0000-000000000166"
	weekOneID := "00000000-0000-0000-0000-000000000167"
	weekTwoID := "00000000-0000-0000-0000-000000000168"
	page := StructuredTrainingPage{
		Management: true, CSRFField: templ.Raw(""),
		Groups: []StructuredTrainingChoice{{ID: "00000000-0000-0000-0000-000000000169", Name: "Cadetes"}},
		Weeks:  []StructuredTrainingChoice{{ID: weekOneID, Name: "Cadetes · M41"}, {ID: weekTwoID, Name: "Cadetes · M42"}},
		Cycles: []StructuredTrainingCycle{{
			ID: cycleID, GroupID: "00000000-0000-0000-0000-000000000169", GroupName: "Cadetes", Season: "2026/2027",
			Name: "Transformação", LevelLabel: "Mesociclo", Goals: "Preparar a Taça", Focus: "Técnica sob fadiga", Version: 4,
			Warning: "Distribuição incompleta: 1 de 2 semanas sem carga planeada; 1 de 2 semanas sem modalidades estruturadas.",
			Targets: []StructuredTrainingCycleTarget{{ID: "00000000-0000-0000-0000-000000000170", Label: "03/10/2026 · Taça"}},
			Weeks: []StructuredTrainingCycleWeek{
				{ID: weekOneID, Title: "M41", DateRange: "05/10/2026–11/10/2026", PlannedLoad: "70%", Modalities: []string{"Água", "Ginásio"}, PlannerURL: "/admin/treinos/estruturados?group_id=00000000-0000-0000-0000-000000000169&week_id=" + weekOneID + "#training-plan"},
				{ID: weekTwoID, Title: "M42", DateRange: "12/10/2026–18/10/2026", PlannerURL: "/admin/treinos/estruturados?group_id=00000000-0000-0000-0000-000000000169&week_id=" + weekTwoID + "#training-plan"},
			},
		}},
	}
	var output bytes.Buffer
	if err := structuredTrainingContent(page).Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	html := output.String()
	for _, expected := range []string{"Ciclos", "Transformação", "Mesociclo", "Preparar a Taça", "Técnica sob fadiga", "Carga planeada: 70%", "Modalidades: Água, Ginásio", "Distribuição incompleta", "Carga planeada não definida", "Publicações, respostas de atletas e competições-alvo não são copiadas", "Estruturas incluídas", "value=\"4\""} {
		if !strings.Contains(html, expected) {
			t.Fatalf("cycle UI missing %q: %s", expected, html)
		}
	}
	if strings.Contains(html, "readiness") || strings.Contains(html, "recomendação automática para o ciclo") {
		t.Fatalf("cycle UI invented load advice: %s", html)
	}
}

func TestStructuredTrainingParentEditKeepsExistingChildSelectable(t *testing.T) {
	parentID := "00000000-0000-0000-0000-000000000171"
	childID := "00000000-0000-0000-0000-000000000172"
	page := StructuredTrainingPage{
		Management: true, CSRFField: templ.Raw(""),
		Cycles: []StructuredTrainingCycle{
			{ID: parentID, GroupID: "00000000-0000-0000-0000-000000000169", GroupName: "Competição", Season: "2026", Name: "Macrociclo", Version: 1},
			{ID: childID, GroupID: "00000000-0000-0000-0000-000000000169", GroupName: "Competição", Season: "2026", Name: "Mesociclo", ParentID: parentID, ParentName: "Macrociclo", Version: 1},
		},
	}
	var output bytes.Buffer
	if err := structuredTrainingContent(page).Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	html := output.String()
	needle := `name="child_cycle_id" value="` + childID + `" checked`
	if !strings.Contains(html, needle) {
		t.Fatalf("parent edit did not retain child checkbox: %s", html)
	}
}

func TestStructuredTrainingCycleErrorDialogReopensAndPreservesCopyInput(t *testing.T) {
	cycleID := "00000000-0000-0000-0000-000000000166"
	page := StructuredTrainingPage{
		Management: true, OpenForm: "cycle-copy-" + cycleID, CSRFField: templ.Raw(""),
		CycleCopyForm: StructuredTrainingCycleCopyForm{CycleID: cycleID, Name: "Nome preservado", FirstMonday: "2026-09-08", Errors: validation.FieldErrors{"first_monday": "Selecione uma segunda-feira válida."}},
		Cycles:        []StructuredTrainingCycle{{ID: cycleID, GroupID: "00000000-0000-0000-0000-000000000169", GroupName: "Competição", Season: "2026", Name: "Fonte", Version: 1}},
	}
	var output bytes.Buffer
	if err := structuredTrainingContent(page).Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	html := output.String()
	for _, expected := range []string{`data-task-open-on-load`, `open`, `value="Nome preservado"`, `value="2026-09-08"`, "Selecione uma segunda-feira válida."} {
		if !strings.Contains(html, expected) {
			t.Fatalf("missing %q in cycle error dialog", expected)
		}
	}
}

func TestStructuredWaterBlockTaskRendersAFullTaskWithRecoverableErrors(t *testing.T) {
	page := StructuredWaterBlockTaskPage{
		Meta:      components.PageMeta{Title: "Adicionar bloco de água | MyCFCoimbra"},
		CSRFField: templ.Raw(""),
		ActionURL: "/admin/treinos/estruturados/sessoes/session-1/segmentos/segment-1/agua",
		ReturnURL: "/admin/treinos/estruturados?group_id=group-1&week_id=week-1&session_id=session-1#training-plan",
		GroupName: "Seniores", WeekTitle: "Semana 41", SessionTitle: "Série de manhã", SegmentTitle: "Água",
		Form: StructuredWaterBlockTaskForm{Title: "Intervalos", StepName: "500 m", Errors: validation.FieldErrors{"step_name": "Indique um esforço válido."}},
	}
	var output bytes.Buffer
	if err := StructuredWaterBlockTask(page).Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	html := output.String()
	for _, expected := range []string{"<h1 id=\"water-block-task-title\">Adicionar bloco de água", "Plano · Seniores · Semana 41", "Sessão Série de manhã", "value=\"Intervalos\"", "value=\"500 m\"", "Indique um esforço válido.", "return_to"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("water task missing %q: %s", expected, html)
		}
	}
	if strings.Contains(html, "<dialog") {
		t.Fatalf("complex water authoring must not render as a nested modal: %s", html)
	}
}
