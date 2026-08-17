package pages

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"
)

func TestStructuredTrainingRendersAdminIntensityProfilesAndPlannedTotals(t *testing.T) {
	page := StructuredTrainingPage{
		Management:             true,
		CanManageWaterProfiles: true,
		CSRFField:              templ.Raw(""),
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
	for _, expected := range []string{"Perfis de intensidade", "Ritmo sustentável", "O limite inferior de R5", "Esforço planeado", "27 min", "revisão 2", "Resumo semanal", "Carga planeada", "70%", "Registo real do atleta", "Ver prescrições e respostas dos atletas"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("rendered page missing %q", expected)
		}
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
