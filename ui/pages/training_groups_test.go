package pages

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/cfcoimbra/mycfc/internal/validation"
)

func renderTrainingGroups(t *testing.T, page TrainingGroupsPage) string {
	t.Helper()
	if page.CSRFField == nil {
		page.CSRFField = templ.Raw(`<input name="csrf_token" value="fixture" type="hidden">`)
	}
	var out bytes.Buffer
	if err := trainingGroupsContent(page).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestTrainingGroupCreateNeedsOnlyNameAndConfiguredScope(t *testing.T) {
	page := TrainingGroupsPage{Mode: "create", ActionURL: "/admin/treinos/estruturados/grupos/criar", ReturnURL: "/admin/treinos/estruturados/grupos",
		Scopes: []TrainingGroupScopeChoice{{Value: "programme:empty", Name: "Competição"}}, Form: TrainingGroupForm{Name: "Iniciados e Infantis", Scope: "programme:empty"}}
	body := renderTrainingGroups(t, page)
	for _, want := range []string{`method="post"`, `action="/admin/treinos/estruturados/grupos/criar"`, `value="Iniciados e Infantis"`, `value="programme:empty" selected`, `name="csrf_token"`, "Pode adicionar atletas depois.", "Não altera a participação nem o escalão"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing empty-group setup contract %q", want)
		}
	}
	for _, forbidden := range []string{`name="membership_id"`, `name="season_id"`, `data-task-open=`, "<dialog"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("independent group creation includes %q", forbidden)
		}
	}
}

func TestTrainingGroupScopeErrorDoesNotInvalidateRetainedName(t *testing.T) {
	body := renderTrainingGroups(t, TrainingGroupsPage{Mode: "create", Form: TrainingGroupForm{Name: "Grupo conservado", Errors: validation.FieldErrors{"scope": "Escolha uma participação disponível."}}})
	for _, id := range []string{"training-group-name", "training-group-scope"} {
		opening := regexp.MustCompile(`<[^>]+id="` + id + `"[^>]*>`).FindString(body)
		invalid := strings.Contains(opening, `aria-invalid="true"`)
		if invalid != (id == "training-group-scope") {
			t.Errorf("field %s invalid=%t: %s", id, invalid, opening)
		}
	}
	if !strings.Contains(body, `href="#training-group-scope"`) || !strings.Contains(body, `value="Grupo conservado"`) || !strings.Contains(body, `role="alert" tabindex="-1" autofocus`) {
		t.Fatal("scope validation lost error target, focus or retained name")
	}
}

func TestTrainingGroupAddUsesNecessaryNamesAndPreservesConflictReview(t *testing.T) {
	page := TrainingGroupsPage{Mode: "add", ActionURL: "/admin/treinos/estruturados/grupos/g/atletas", ReturnURL: "/admin/treinos/estruturados/grupos/g?week_id=w", ReviewURL: "/admin/treinos/estruturados/grupos/g/atletas?q=Ana&week_id=w", WeekID: "w", SearchPerformed: true,
		Conflict: "Reveja o grupo antes de guardar.", SearchPage: 2, Group: TrainingGroupRow{Name: "Iniciados", ScopeName: "Competição", Version: "new-version"},
		Form:    TrainingGroupForm{Query: "Ana", Version: "submitted-version", SelectedIDs: map[string]bool{"membership-selected": true}},
		Results: []TrainingGroupAthleteChoice{{MembershipID: "membership-selected", Name: "Ana elegível"}, {MembershipID: "membership-present", Name: "Ana presente", AlreadyMember: true}, {MembershipID: "membership-unavailable", Name: "Ana indisponível", Unavailable: true}}}
	body := renderTrainingGroups(t, page)
	for _, want := range []string{`name="q"`, `name="version" value="submitted-version"`, `name="week_id" value="w"`, `name="page" value="2"`, `value="membership-selected" checked`, "Já pertence ao grupo", "Não está disponível para adição", "As escolhas aplicam-se apenas a esta página", `href="#training-group-members"`, `q=Ana&amp;week_id=w`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing safe roster/conflict contract %q", want)
		}
	}
	for _, forbidden := range []string{"new-version", `/membros/`, `type="email"`, "data-task-open="} {
		if strings.Contains(body, forbidden) {
			t.Errorf("roster view disclosed or rebased %q", forbidden)
		}
	}
}

func TestTrainingGroupBlankSearchDoesNotRenderProvidedAthletes(t *testing.T) {
	body := renderTrainingGroups(t, TrainingGroupsPage{Mode: "add", Results: []TrainingGroupAthleteChoice{{Name: "Do not display", MembershipID: "hidden"}}})
	if strings.Contains(body, "Do not display") || strings.Contains(body, `value="hidden"`) || !strings.Contains(body, "Pesquise por um nome para escolher atletas.") {
		t.Fatal("blank search must not expose an athlete roster")
	}
}

func TestTrainingGroupDetailKeepsCanonicalGroupAndWeekActions(t *testing.T) {
	page := TrainingGroupsPage{Mode: "detail", Group: TrainingGroupRow{Name: "Grupo com nome longo", ScopeName: "Competição"}, PlannerURL: "/admin/treinos/estruturados?group_id=g&week_id=w", AddURL: "/admin/treinos/estruturados/grupos/g/atletas?week_id=w", RenameURL: "/admin/treinos/estruturados/grupos/g/nome?week_id=w", CollectionURL: "/admin/treinos/estruturados/grupos"}
	body := renderTrainingGroups(t, page)
	for _, want := range []string{"Ainda sem atletas", "Pode preparar uma semana em rascunho", "Atletas do grupo", `group_id=g&amp;week_id=w`, `grupos/g/atletas?week_id=w`, `grupos/g/nome?week_id=w`} {
		if !strings.Contains(body, want) {
			t.Errorf("group landing missing %q", want)
		}
	}
	if strings.Contains(body, ">Publicado<") || strings.Contains(body, "<dialog") {
		t.Fatal("group landing must not claim group publication or require a dialog")
	}
}

func TestTrainingGroupWeekActionReflectsExistingWeek(t *testing.T) {
	for _, hasWeek := range []bool{false, true} {
		page := TrainingGroupsPage{Mode: "detail", Group: TrainingGroupRow{Name: "Grupo", HasWeek: hasWeek}, PlannerURL: "/admin/treinos/estruturados?group_id=g"}
		body := renderTrainingGroups(t, page)
		want, absent := "Criar semana", "Abrir semana"
		if hasWeek {
			want, absent = absent, want
		}
		if !strings.Contains(body, want) || strings.Contains(body, absent) {
			t.Errorf("existing week=%t must offer %q rather than %q", hasWeek, want, absent)
		}
	}
}

func TestTrainingGroupValidationUsesAriaBooleanStrings(t *testing.T) {
	for _, test := range []struct {
		mode   string
		fields []string
	}{
		{"create", []string{"training-group-name", "training-group-scope"}},
		{"add", []string{"training-group-query", "training-group-members"}},
	} {
		body := renderTrainingGroups(t, TrainingGroupsPage{Mode: test.mode, SearchPerformed: true,
			Results: []TrainingGroupAthleteChoice{{Name: "Atleta", MembershipID: "fixture"}},
			Form:    TrainingGroupForm{Errors: validation.FieldErrors{"name": "Nome inválido", "scope": "Participação inválida", "q": "Pesquisa inválida", "members": "Seleção inválida"}}})
		for _, id := range test.fields {
			opening := regexp.MustCompile(`<[^>]+id="` + id + `"[^>]*>`).FindString(body)
			if !strings.Contains(opening, `aria-invalid="true"`) {
				t.Errorf("%s needs an ARIA boolean string: %s", id, opening)
			}
		}
	}
	body := renderTrainingGroups(t, TrainingGroupsPage{Mode: "create"})
	if strings.Contains(body, `aria-invalid=""`) || strings.Contains(body, `aria-invalid="true"`) {
		t.Fatal("valid controls must not be marked invalid")
	}
}

func TestTrainingGroupSetupHelpIsOptionalAndCapabilityScoped(t *testing.T) {
	setup := TrainingGroupSetup{Show: true, MissingSeason: true, CanClassify: true, CanDefine: true, CategoryApplicable: true, CategoryRequired: true, ClassificationURL: "/equipa/classificacao?origin_group=g&origin_week=w&origin_day=2026-11-11", DefinitionsURL: "/equipa/escaloes?origin_group=g&origin_week=w&origin_day=2026-11-11", SeasonURL: "/admin/epocas?return_kind=group&return_id=g&week_id=w&day=2026-11-11"}
	page := TrainingGroupsPage{Mode: "detail", Setup: setup, Group: TrainingGroupRow{Name: "Grupo", HasWeek: true}, PlannerURL: "/planner?group_id=g&week_id=w&day=2026-11-11"}
	body := renderTrainingGroups(t, page)
	for _, want := range []string{"Competição é uma participação; Iniciados é um escalão; Iniciados e Infantis pode ser um grupo de treino", `<details class="module disclosure">`, "Não existe uma época atual configurada.", "Configurar épocas", "Rever a participação de uma pessoa", "Gerir escalões da época", "Abrir semana", "Pode continuar a preparar a semana em rascunho"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing setup help %q", want)
		}
	}
	if strings.Contains(body, `class="module disclosure" open`) || strings.Contains(body, "Ainda não existem escalões configurados nesta participação e época.") {
		t.Fatal("optional help is forced or claims configured current season while missing")
	}
	page.Setup.Show = false
	body = renderTrainingGroups(t, page)
	if strings.Contains(body, "Precisa de rever a configuração?") || !strings.Contains(body, "Abrir semana") {
		t.Fatal("ready coach has forced setup or loses direct week")
	}
}
func TestTrainingGroupSetupTeamOnlyHasNoUnusableLinks(t *testing.T) {
	page := TrainingGroupsPage{Mode: "add", Setup: TrainingGroupSetup{Show: true, MissingSeason: true, CategoryApplicable: true, ClassificationURL: "/forbidden-classification", DefinitionsURL: "/forbidden-definitions"}}
	body := renderTrainingGroups(t, page)
	for _, bad := range []string{`href="/forbidden-classification"`, `href="/forbidden-definitions"`, `href="/admin/epocas"`, `/perfil`} {
		if strings.Contains(body, bad) {
			t.Errorf("unusable task exposed %s", bad)
		}
	}
	if !strings.Contains(body, "Peça a um administrador para configurar e selecionar a época.") || !strings.Contains(body, "peça ajuda a um administrador") {
		t.Fatal("missing safe administrator guidance")
	}
}
func TestTrainingGroupSetupCategoryOnlyForApplicableParticipation(t *testing.T) {
	for _, applicable := range []bool{false, true} {
		p := TrainingGroupsPage{Mode: "add", Setup: TrainingGroupSetup{Show: true, CanDefine: true, CategoryApplicable: applicable, CategoryConfigured: true, DefinitionsURL: "/equipa/escaloes?origin_group=g"}}
		body := renderTrainingGroups(t, p)
		if strings.Contains(body, "Gerir escalões da época") != applicable || strings.Contains(body, "A elegibilidade de cada pessoa é verificada") != applicable {
			t.Errorf("category applicability=%t mishandled", applicable)
		}
		if applicable && !strings.Contains(body, "O escalão é opcional na participação deste grupo.") {
			t.Fatal("optional category claimed mandatory")
		}
	}
}
func TestTrainingGroupSearchEmptyResultDoesNotClaimHiddenPeopleAbsent(t *testing.T) {
	body := renderTrainingGroups(t, TrainingGroupsPage{Mode: "add", SearchPerformed: true, Form: TrainingGroupForm{Query: "Nome"}})
	if !strings.Contains(body, "Não foram encontradas pessoas disponíveis para este grupo com esta pesquisa.") || strings.Contains(body, "Não foram encontradas pessoas elegíveis neste grupo.") {
		t.Fatal("search absence overstates scope")
	}
}
func TestTrainingGroupVerifiedDayRetainedAcrossNativeForms(t *testing.T) {
	for _, mode := range []string{"create", "rename", "add"} {
		p := TrainingGroupsPage{Mode: mode, ContextDay: "2026-11-11", WeekID: "week", Conflict: "O grupo mudou.", Form: TrainingGroupForm{Version: "original"}}
		body := renderTrainingGroups(t, p)
		count := 1
		if mode == "add" {
			count = 2
		}
		if got := strings.Count(body, `name="day" value="2026-11-11"`); got != count {
			t.Errorf("%s day fields=%d want%d", mode, got, count)
		}
		if !strings.Contains(body, `name="week_id" value="week"`) || !strings.Contains(body, `name="version" value="original"`) {
			t.Errorf("%s context/version lost", mode)
		}
	}
}
