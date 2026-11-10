package pages

import (
	"bytes"
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/cfcoimbra/mycfc/internal/validation"
)

func TestLegacyStructuredGroupDialogOffersNativeEmptyCreateWithoutAthleteSelection(t *testing.T) {
	p := StructuredTrainingPage{CSRFField: templ.Raw(`<input type="hidden" name="csrf" value="not-required-for-navigation">`), OpenForm: "group", PlannerReturnURL: "/admin/treinos/estruturados?group_id=source-group&week_id=reviewed-week&day=2027-01-11&session_id=selected-session&person_id=not-for-create#training-plan", GroupForm: StructuredTrainingGroupForm{Name: "Obsolete draft must not be echoed", TeamID: "old-team", ProgrammeID: "old-programme", MembershipIDs: map[string]bool{"old-episode": true}, Errors: validation.FieldErrors{"scope": "Obsolete error"}}, Teams: []StructuredTrainingChoice{{ID: "old-team", Name: "Old team must not be listed"}}, Programmes: []StructuredTrainingChoice{{ID: "old-programme", Name: "Old programme must not be listed"}}, Memberships: []StructuredTrainingMembershipChoice{{ID: "old-episode", Athlete: "Old person must not be listed", Scope: "Old scope"}}}
	var out bytes.Buffer
	if err := structuredGroupForm(p).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	for _, want := range []string{`id="criar-grupo-treino"`, `aria-labelledby="structured-group-title"`, `id="structured-group-title"`, "Criar grupo de treino", "Abrir criação do grupo", "sem adicionar atletas", "época e data explícitas"} {
		if !strings.Contains(body, want) {
			t.Fatalf("native task discovery missing %q", want)
		}
	}
	for _, bad := range []string{"<form", `name="membership_id"`, `name="team_id"`, `name="programme_id"`, "Obsolete draft", "Old person", "Old team", "Old programme", "Obsolete error"} {
		if strings.Contains(body, bad) {
			t.Fatalf("obsolete undated create transport/data still rendered: %q", bad)
		}
	}
	found := 0
	for _, tag := range publicationTags(body, "a") {
		href, _ := publicationAttr(tag, "href")
		u, err := url.Parse(href)
		if err == nil && u.Path == "/admin/treinos/estruturados/grupos/criar" {
			found++
			if u.IsAbs() || u.Host != "" || len(u.Query()) != 2 || u.Query().Get("week_id") != "reviewed-week" || u.Query().Get("day") != "2027-01-11" {
				t.Fatalf("native creation lost or broadened scoped context: %s", href)
			}
		}
	}
	if found != 1 {
		t.Fatalf("expected one direct native empty-create link, got %d", found)
	}
}
func TestNativeStructuredGroupCreateLinkDoesNotInventPlannerContext(t *testing.T) {
	for _, current := range []string{"", "/admin/treinos/estruturados#training-plan", "%invalid"} {
		if got := structuredNativeGroupCreateURL(StructuredTrainingPage{PlannerReturnURL: current, SelectedWeekID: "not-a-return-context-default", SelectedDay: "2027-01-11"}); got != "/admin/treinos/estruturados/grupos/criar" {
			t.Fatalf("invented context for %q: %s", current, got)
		}
	}
}
