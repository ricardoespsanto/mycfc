package pages

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/a-h/templ"
	"strings"
	"testing"
)

func renderScheduleComponent(t *testing.T, c templ.Component) string {
	t.Helper()
	var out bytes.Buffer
	if err := c.Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}
func TestTrainingScheduleFourShapesAndLegacy(t *testing.T) {
	cases := []struct {
		name string
		v    TrainingScheduleView
		want string
	}{
		{"interval", TrainingScheduleView{Known: true, Precision: "INTERVAL", Date: "2026-11-09", StartsAt: "16:00", EndsAt: "17:45", EndsOn: "2026-11-09"}, "2026-11-09 · 16:00–17:45"},
		{"overnight", TrainingScheduleView{Known: true, Precision: "INTERVAL", Date: "2026-11-09", StartsAt: "23:00", EndsAt: "00:30", EndsOn: "2026-11-10"}, "2026-11-09 · 23:00–2026-11-10 00:30"},
		{"start", TrainingScheduleView{Known: true, Precision: "START_ONLY", Date: "2026-11-09", StartsAt: "16:00", EndsAt: "invented"}, "2026-11-09 · 16:00 · Fim por definir"},
		{"day", TrainingScheduleView{Known: true, Precision: "DAY", Date: "2026-11-09", StartsAt: "invented", EndsAt: "invented"}, "2026-11-09 · Hora por definir"},
		{"morning", TrainingScheduleView{Known: true, Precision: "BAND", Date: "2026-11-09", Band: "MORNING", StartsAt: "invented"}, "2026-11-09 · Manhã · hora por definir"},
		{"afternoon", TrainingScheduleView{Known: true, Precision: "BAND", Date: "2026-11-09", Band: "AFTERNOON"}, "2026-11-09 · Tarde · hora por definir"},
		{"legacy", TrainingScheduleView{Precision: "DAY", Date: "should-not-invent", Label: "not-frozen"}, "frozen historical When"},
		{"label", TrainingScheduleView{Known: true, Precision: "DAY", Label: "9 de novembro · Hora por definir"}, "9 de novembro · Hora por definir"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := trainingScheduleLabel(c.v, "frozen historical When"); got != c.want {
				t.Errorf("got%q want%q", got, c.want)
			}
		})
	}
	for _, v := range []TrainingScheduleView{{Unavailable: true}, {Known: true, Unavailable: true, Label: "must-not-display"}, {Known: true, Precision: "UNKNOWN"}} {
		got := trainingScheduleLabel(v, "must-not-display")
		if !strings.Contains(got, "Não foi possível confirmar") || strings.Contains(got, "must-not-display") {
			t.Fatal("unavailable schedule hid failure behind legacy/label")
		}
	}
}
func TestTrainingSchedulePresentationPreservesFrozenV1Bytes(t *testing.T) {
	s := StructuredTrainingSession{ID: "s", Title: "frozen", When: "frozen range", EntryKind: "TRAINING", Schedule: TrainingScheduleView{Known: true, Precision: "DAY", Date: "2026-11-09"}, WriteUnsupported: true, UnsupportedMessage: "reader-only"}
	got, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"ID":"s","PrescriptionID":"","Title":"frozen","Description":"","When":"frozen range","EntryKind":"TRAINING","Modalities":null,"ActualDurationMinutes":0,"ActualDistanceMetres":0,"Segments":null,"Changes":null}`
	if string(got) != want {
		t.Fatalf("v1 presentation changed JSON/order:\n%s", got)
	}
}
func TestTrainingScheduleDayHasSeparateWorkspaceBucket(t *testing.T) {
	p := workspaceFixture()
	p.Workspace.Days[0].Morning = nil
	p.Workspace.Days[0].Afternoon = nil
	p.Workspace.Days[0].Unscheduled = []StructuredWorkspaceAppointment{{Title: "Dia de descanso", EntryKind: "REST", Precision: "DAY", URL: "/session/day"}}
	body := renderScheduleComponent(t, structuredWeekWorkspace(p))
	start := strings.Index(body, `aria-label="Hora por definir"`)
	end := strings.Index(body, `aria-label="Manhã"`)
	if start < 0 || end < start || !strings.Contains(body[start:end], "Dia de descanso") {
		t.Fatal("DAY lost distinct unscheduled bucket")
	}
	for _, c := range []struct {
		v    StructuredWorkspaceAppointment
		want string
	}{{StructuredWorkspaceAppointment{Precision: "BAND", Band: "MORNING", StartsAt: "00:00"}, "Manhã · hora por definir"}, {StructuredWorkspaceAppointment{Precision: "START_ONLY", StartsAt: "16:00", EndsAt: "17:00"}, "16:00 · Fim por definir"}, {StructuredWorkspaceAppointment{TimeUnknown: true, StartsAt: "00:00"}, "Hora por definir"}} {
		if got := structuredWorkspaceTime(c.v); got != c.want {
			t.Errorf("workspace got%q want%q", got, c.want)
		}
	}
}
func TestTrainingScheduleUnsupportedDirectActionsHaveNoForms(t *testing.T) {
	edit := TrainingSessionEditPage{WriteUnsupported: true, UnsupportedMessage: "Esta sessão está disponível para consulta.", Form: TrainingSessionForm{Title: "Sessão parcial", Description: "Descrição mantida"}}
	cancel := TrainingSessionCancelPage{WriteUnsupported: true, UnsupportedMessage: edit.UnsupportedMessage, Title: "Sessão parcial", When: "2026-11-09 · Hora por definir"}
	for _, c := range []templ.Component{trainingSessionEditContent(edit), trainingSessionCancelContent(cancel)} {
		body := renderScheduleComponent(t, c)
		if strings.Contains(body, "<form") || strings.Contains(body, `datetime-local`) || !strings.Contains(body, edit.UnsupportedMessage) || !strings.Contains(body, "Voltar aos treinos") {
			t.Fatal("unsupported action enabled writes or lost recovery")
		}
	}
	exact := edit
	exact.WriteUnsupported = false
	exact.CSRFField = templ.Raw("")
	body := renderScheduleComponent(t, trainingSessionEditContent(exact))
	if !strings.Contains(body, `name="starts_at" type="datetime-local"`) || !strings.Contains(body, "Guardar alterações") {
		t.Fatal("existing exact edit form changed")
	}
}
func TestTrainingScheduleUnsupportedRoutineStaysReadable(t *testing.T) {
	p := StructuredTrainingPage{CSRFField: templ.Raw(""), Routines: []StructuredTrainingRoutine{{ID: "r", Kind: "SESSION", Name: "Rotina parcial", Preview: "Manhã · hora por definir", WriteUnsupported: true}}}
	body := renderScheduleComponent(t, structuredRoutineLibrary(p))
	if !strings.Contains(body, "Rotina parcial") || !strings.Contains(body, "Manhã · hora por definir") || strings.Contains(body, `rotinas/r/inserir`) || strings.Contains(body, "Usar rotina</button>") {
		t.Fatal("unsupported routine was hidden or remained writable")
	}
}
func TestTrainingScheduleReadFailureNotEmptyClaim(t *testing.T) {
	for _, c := range []templ.Component{trainingContent(TrainingPage{Error: "Não foi possível consultar a sessão."}), structuredTrainingContent(StructuredTrainingPage{Error: "Não foi possível consultar a sessão."})} {
		body := renderScheduleComponent(t, c)
		if !strings.Contains(body, "Não foi possível consultar a sessão.") || strings.Contains(body, "Sem sessões disponíveis") || strings.Contains(body, "Ainda sem planeamento estruturado") {
			t.Fatal("decoder failure became an empty collection")
		}
	}
}
func TestTrainingSchedulePartialRowRetainsOutcomeWithoutMutation(t *testing.T) {
	p := TrainingPage{CSRFField: templ.Raw(""), Sessions: []TrainingSession{{ID: "s", Title: "Sessão parcial", When: "not-authoritative", Schedule: TrainingScheduleView{Known: true, Precision: "DAY", Date: "2026-11-09"}, WriteUnsupported: true, OutcomeWriteUnsupported: true, OutcomeUnsupportedMessage: "Hora de início desconhecida.", Outcome: "COMPLETED", ActualDuration: "30 min"}}}
	body := renderScheduleComponent(t, trainingContent(p))
	if !strings.Contains(body, "Sessão parcial") || !strings.Contains(body, "Hora por definir") || !strings.Contains(body, "30 min") || strings.Contains(body, `action="/treinos/sessoes/feedback"`) || strings.Contains(body, "not-authoritative") {
		t.Fatal("partial row lost truthful schedule/history or exposed write")
	}
}

func TestTrainingScheduleStructuredUnsupportedPreservesContentWithoutControls(t *testing.T) {
	p := StructuredTrainingPage{Management: true, CSRFField: templ.Raw(""), Audiences: []StructuredTrainingAudience{{GroupName: "Grupo", Weeks: []StructuredTrainingWeek{{ID: "w", Title: "Semana", Sessions: []StructuredTrainingSession{{ID: "partial", Title: "Sessão parcial", When: "frozen", Schedule: TrainingScheduleView{Known: true, Precision: "DAY", Date: "2026-11-09"}, WriteUnsupported: true, EntryKind: "TRAINING", Segments: []StructuredTrainingSegment{{ID: "segment", Modality: "GYM", Title: "Ginásio mantido", Blocks: []StructuredTrainingBlock{{ID: "block", Title: "Bloco mantido", Instructions: "Instruções conservadas"}}}}}}}}}}}
	body := renderScheduleComponent(t, structuredTrainingPlan(p))
	for _, want := range []string{"Sessão parcial", "Hora por definir", "Ginásio mantido", "Bloco mantido", "Instruções conservadas", "Voltar à semana"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing readonly content %q", want)
		}
	}
	for _, bad := range []string{`save-routine-partial`, `copy-session-partial`, `add-segment-partial`, `add-block-segment`, `save-routine-block`} {
		if strings.Contains(body, bad) {
			t.Errorf("unsupported content control rendered %q", bad)
		}
	}
}

func TestTrainingScheduleBlockedPostRetainsProposedValuesAsText(t *testing.T) {
	edit := TrainingSessionEditPage{WriteUnsupported: true, Form: TrainingSessionForm{Title: "Proposta mantida", Description: "Descrição mantida", StartsAt: "2026-11-09T16:00", EndsAt: "2026-11-09T18:00"}}
	body := renderScheduleComponent(t, trainingSessionEditContent(edit))
	for _, want := range []string{"Proposta mantida", "Descrição mantida", "Início proposto", "2026-11-09T16:00", "Fim proposto", "2026-11-09T18:00"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing retained value %q", want)
		}
	}
	cancel := TrainingSessionCancelPage{WriteUnsupported: true, CancellationReason: "Motivo conservado", Conflict: "Reveja a sessão."}
	body = renderScheduleComponent(t, trainingSessionCancelContent(cancel))
	if !strings.Contains(body, "Motivo conservado") || !strings.Contains(body, "Reveja a sessão.") {
		t.Fatal("blocked cancellation lost safe input/recovery")
	}
	if strings.Contains(body, "<form") {
		t.Fatal("blocked cancellation retained mutable form")
	}
}

func TestTrainingKnownStartV2RetainsOutcomeWritesWithoutMakingScheduleEditable(t *testing.T) {
	p := TrainingPage{CSRFField: templ.Raw(""), Sessions: []TrainingSession{
		{ID: "start-only", Title: "Início conhecido", Schedule: TrainingScheduleView{Known: true, Precision: "START_ONLY", Date: "2026-11-09", StartsAt: "16:00"}, WriteUnsupported: true, Outcome: "COMPLETED", ActualDuration: "42 min", OutcomeVersion: 2},
		{ID: "other-known-start", Title: "Outra sessão conhecida", Schedule: TrainingScheduleView{Known: true, Precision: "START_ONLY", Date: "2026-11-10", StartsAt: "17:00"}, WriteUnsupported: true},
	}}
	body := renderScheduleComponent(t, trainingContent(p))
	for _, want := range []string{`action="/treinos/sessoes/feedback"`, `action="/treinos/sessoes/resultados"`, `<option value="start-only">`, `42 min`, `Fim por definir`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(body, `name="ends_at"`) {
		t.Fatal("actual feedback supplied planned end")
	}
}

func TestWorkspaceRetainsAuthoritativeQualifiedTimingLabel(t *testing.T) {
	label := "2026-10-25 01:30 UTC+01:00–2026-10-25 01:30 UTC+00:00"
	got := structuredWorkspaceTime(StructuredWorkspaceAppointment{Precision: "INTERVAL", StartsAt: "01:30", EndsAt: "01:30", Label: label})
	if got != label {
		t.Fatalf("qualified recorded instants lost: %s", got)
	}
}
