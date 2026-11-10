package pages

import (
	"bytes"
	"context"
	"github.com/a-h/templ"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"regexp"
	"strings"
	"testing"
)

func renderSeasons(t *testing.T, p SeasonsPage) string {
	t.Helper()
	p.CSRFField = templ.Raw(`<input type="hidden" name="csrf_token" value="fixture">`)
	var out bytes.Buffer
	if err := seasonsContent(p).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}
func TestSeasonCreateExplicitBlankDates(t *testing.T) {
	body := renderSeasons(t, SeasonsPage{Mode: "create", ActionURL: "/admin/epocas/criar", ContextKind: "group", ContextID: "group", ContextWeekID: "week"})
	for _, name := range []string{"code", "name", "starts_on", "ends_on"} {
		if !regexp.MustCompile(`name="` + name + `"[^>]*value=""`).MatchString(body) {
			t.Errorf("field %s must start blank", name)
		}
	}
	for _, want := range []string{`method="post"`, `name="return_kind" value="group"`, `name="return_id" value="group"`, `name="week_id" value="week"`, "A nova época fica sem seleção", `name="csrf_token"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, bad := range []string{`name="confirm"`, `name="return_to"`, `<dialog`, `data-task-open=`} {
		if strings.Contains(body, bad) {
			t.Errorf("unexpected %q", bad)
		}
	}
}
func TestSeasonCreateRetainsInvalidValuesAndFocusedErrors(t *testing.T) {
	body := renderSeasons(t, SeasonsPage{Mode: "create", Form: SeasonForm{Code: "clube", Name: "Época do clube", StartsOn: "2028-02-29", EndsOn: "2028-02-28", Errors: validation.FieldErrors{"ends_on": "Reveja a data de fim."}}, ContextKind: "member", ContextID: "member", ContextPage: "2", ContextQuery: "Ana"})
	for _, want := range []string{`value="2028-02-29"`, `value="2028-02-28"`, `href="#season-end"`, `role="alert" tabindex="-1" autofocus`, `name="return_page" value="2"`, `name="return_q" value="Ana"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, id := range []string{"season-code", "season-name", "season-start", "season-end"} {
		tag := regexp.MustCompile(`<input[^>]*id="` + id + `"[^>]*>`).FindString(body)
		want := `aria-invalid="false"`
		if id == "season-end" {
			want = `aria-invalid="true"`
		}
		if !strings.Contains(tag, want) {
			t.Errorf("invalid explicit ARIA for %s: %s", id, tag)
		}
	}
}
func TestSeasonSelectionRequiresPreviewBeforeConfirmation(t *testing.T) {
	p := SeasonsPage{Mode: "select", Target: SeasonRow{Name: "Época proposta", StartsOn: "2027-03-01", EndsOn: "2027-03-01"}}
	body := renderSeasons(t, p)
	if strings.Contains(body, `name="confirm"`) || !strings.Contains(body, "Pré-visualizar alteração") {
		t.Fatal("selection bypasses preview")
	}
	p.Mode = "preview"
	p.Token = "original-token"
	body = renderSeasons(t, p)
	for _, want := range []string{`type="hidden" name="confirm" value="yes"`, `name="preview_token" value="original-token"`, "Não existe uma época atual.", "não transfere planos nem publicações", "2027-03-01"} {
		if !strings.Contains(body, want) {
			t.Errorf("preview missing %q", want)
		}
	}
	button := regexp.MustCompile(`<button[^>]*>Confirmar época atual</button>`).FindString(body)
	if button == "" || strings.Contains(button, `name=`) || strings.Contains(button, `value=`) {
		t.Fatal("native confirmation must survive pending submitter disabling: hidden confirm field and unnamed button required")
	}
}
func TestSeasonInvalidPreviewCannotSubmitConfirmation(t *testing.T) {
	for _, p := range []SeasonsPage{
		{Mode: "preview", Token: "token", Conflict: "Seleção alterada"},
		{Mode: "preview", Token: "token", Form: SeasonForm{Errors: validation.FieldErrors{"form": "Pré-visualização expirada"}}},
		{Mode: "preview"},
	} {
		body := renderSeasons(t, p)
		if strings.Contains(body, `name="confirm"`) || strings.Contains(body, `>Confirmar época atual</button>`) {
			t.Fatal("invalid or tokenless preview offers confirmation")
		}
		if !strings.Contains(body, "Recarregar e rever a seleção") {
			t.Fatal("invalid preview lost recovery path")
		}
	}
}
func TestSeasonConflictPreservesTokenAndRequiresReload(t *testing.T) {
	body := renderSeasons(t, SeasonsPage{Mode: "preview", Token: "stale-original", Conflict: "Outra pessoa alterou a seleção.", ReviewURL: "/admin/epocas/target/selecionar?return_kind=group", Target: SeasonRow{Name: "Proposta mantida"}})
	for _, want := range []string{`name="preview_token" value="stale-original"`, `href="/admin/epocas/target/selecionar?return_kind=group"`, "Proposta mantida", `role="alert" tabindex="-1" autofocus`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, `name="confirm"`) {
		t.Fatal("stale preview still offers confirmation")
	}
}
func TestSeasonMissingMemberDetailRetainsAccountControls(t *testing.T) {
	var out bytes.Buffer
	p := MemberDetailPage{Member: MemberDetail{ID: "person", Name: "Pessoa exemplo", Active: true}, SeasonMissing: true, SeasonSetupURL: "/admin/epocas?return_kind=member&return_id=person", ReturnURL: "/admin/membros"}
	p.Meta.CSRFField = templ.Raw("")
	if err := memberDetailContent(p).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	for _, want := range []string{"Sem época atual", "Configurar épocas", "Abrir perfil", "Desativar conta", "Alterar participação e consultar histórico", `/equipa/classificacao/person`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing unaffected detail/guidance %q", want)
		}
	}
	if strings.Contains(body, `disabled`) {
		t.Fatal("missing season disabled unrelated account controls")
	}
}
func TestSeasonWeekGuidanceUsesOnlyProvidedAdminLink(t *testing.T) {
	for _, admin := range []bool{false, true} {
		p := StructuredTrainingPage{OpenForm: "week", SeasonSetupMessage: "Peça a um administrador para configurar a época.", CSRFField: templ.Raw("")}
		if admin {
			p.SeasonSetupURL = "/admin/epocas?return_kind=group&return_id=group"
		}
		var out bytes.Buffer
		if err := structuredWeekForm(p).Render(context.Background(), &out); err != nil {
			t.Fatal(err)
		}
		body := out.String()
		if strings.Contains(body, "Configurar épocas") != admin {
			t.Errorf("admin setup link present=%t", admin)
		}
		if !strings.Contains(body, p.SeasonSetupMessage) || !strings.Contains(body, `name="week_start"`) {
			t.Fatal("guidance lost message or original week form")
		}
	}
}
