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
		invalid := strings.Contains(opening, `aria-invalid`)
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
