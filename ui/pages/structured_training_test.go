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

func TestStructuredWaterBlockTaskRendersAFullTaskWithRecoverableErrors(t *testing.T) {
	page := StructuredWaterBlockTaskPage{
		Meta: components.PageMeta{Title: "Adicionar bloco de água | MyCFC"},
		CSRFField: templ.Raw(""),
		ActionURL: "/admin/treinos/estruturados/sessoes/session-1/segmentos/segment-1/agua",
		ReturnURL: "/admin/treinos/estruturados?group_id=group-1&week_id=week-1&session_id=session-1#training-plan",
		GroupName: "Seniores", WeekTitle: "Semana 41", SessionTitle: "Série de manhã", SegmentTitle: "Água",
		Form: StructuredWaterBlockTaskForm{Title: "Intervalos", StepName: "500 m", Errors: validation.FieldErrors{"step_name": "Indique um esforço válido."}},
	}
	var output bytes.Buffer
	if err := StructuredWaterBlockTask(page).Render(context.Background(), &output); err != nil { t.Fatal(err) }
	html := output.String()
	for _, expected := range []string{"<h1 id=\"water-block-task-title\">Adicionar bloco de água", "Plano · Seniores · Semana 41", "Sessão Série de manhã", "value=\"Intervalos\"", "value=\"500 m\"", "Indique um esforço válido.", "return_to"} {
		if !strings.Contains(html, expected) { t.Fatalf("water task missing %q: %s", expected, html) }
	}
	if strings.Contains(html, "<dialog") { t.Fatalf("complex water authoring must not render as a nested modal: %s", html) }
}
