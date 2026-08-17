package components

import (
	"context"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/a-h/templ"
)

func TestBaseRendersDocumentContract(t *testing.T) {
	var output strings.Builder
	err := Base(PageMeta{
		Title:         "Iniciar sessão | MyCFC",
		StylesheetURL: "/assets/app.css",
		ScriptURL:     "/assets/app.js",
	}, Flash("Sessão terminada.")).Render(context.Background(), &output)
	if err != nil {
		t.Fatalf("render base: %v", err)
	}

	for _, expected := range []string{
		`<!doctype html>`,
		`<html lang="pt-PT">`,
		`<link rel="stylesheet" href="/assets/app.css">`,
		`<script src="/assets/app.js" defer></script>`,
		`href="#conteudo-principal"`,
		`<main id="conteudo-principal">`,
		`role="status"`,
	} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("rendered document does not contain %q", expected)
		}
	}
	if strings.Contains(output.String(), `admin-subnav`) {
		t.Error("non-admin page should not render the admin sub-nav")
	}
}

func TestBaseUsesGlobalNavigationAsTheOnlyAdminNavigation(t *testing.T) {
	var output strings.Builder
	err := Base(PageMeta{
		Title:           "Gestão de membros | MyCFC",
		StylesheetURL:   "/assets/app.css",
		ScriptURL:       "/assets/app.js",
		CurrentPath:     "/admin/membros",
		CurrentUserName: "Beatriz Administradora",
		Navigation:      []NavigationGroup{{Items: []NavigationItem{{Label: "Hoje", Path: "/today"}}}, {Label: "Administração", Items: []NavigationItem{{Label: "Membros", Path: "/admin/membros"}}}},
	}, Flash("")).Render(context.Background(), &output)
	if err != nil {
		t.Fatalf("render base: %v", err)
	}
	body := output.String()
	for _, expected := range []string{
		`class="app-shell"`,
		`data-announcement-trigger`,
		`href="/announcements"`,
		`id="announcement-panel"`,
		`<h2 id="announcement-panel-title" class="visually-hidden">Avisos</h2>`,
		`aria-label="Avisos"`,
		`aria-label="Conta atual"`,
		`Beatriz Administradora`,
		`Conta com responsabilidades cumulativas`,
		`aria-label="Localização atual"`,
		`<li>Administração</li>`,
		`<li aria-current="page">Membros</li>`,
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("authenticated shell does not contain %q", expected)
		}
	}
	if strings.Contains(body, `class="admin-subnav"`) {
		t.Error("admin destinations must not be duplicated in local navigation")
	}
	if got := strings.Count(body, `data-announcement-trigger`); got != 2 {
		t.Errorf("authenticated shell renders %d notification triggers, want desktop and mobile triggers", got)
	}
	if !strings.Contains(body, `href="/admin/membros" aria-current="page"`) {
		t.Error("global navigation should mark the current administration section")
	}
}

func TestBaseMarksUnverifiedEmailInDesktopAndMobileAccountContext(t *testing.T) {
	var output strings.Builder
	err := Base(PageMeta{Title: "Hoje | MyCFC", CurrentUserName: "Maria Silva", CurrentUserID: "member-1", EmailVerificationPending: true, Navigation: []NavigationGroup{{Items: []NavigationItem{{Label: "Hoje", Path: "/today"}}}}}, Flash("")).Render(context.Background(), &output)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(output.String(), `class="email-verification-cue" href="/perfil">Email por verificar</a>`); got != 3 {
		t.Fatalf("verification cue count = %d, want desktop, enhanced-mobile and fallback-mobile cues", got)
	}
}

func TestBaseSeparatesAccountMembershipsAndResponsibilities(t *testing.T) {
	var output strings.Builder
	err := Base(PageMeta{
		Title:           "Hoje | MyCFC",
		CurrentUserName: "Maria Silva",
		CurrentUserID:   "member-1",
		Navigation: []NavigationGroup{{
			Items:        []NavigationItem{{Label: "Hoje", Path: "/today"}},
			Memberships:  []string{"Competição"},
			Capabilities: []string{"Tutor", "Treinador"},
		}},
	}, Flash("")).Render(context.Background(), &output)
	if err != nil {
		t.Fatal(err)
	}
	body := output.String()
	for _, expected := range []string{"Conta", "Membro", "Inscrições", "Competição", "Responsabilidades", "Tutor", "Treinador"} {
		if !strings.Contains(body, expected) {
			t.Errorf("account context does not contain %q", expected)
		}
	}
	if strings.Contains(body, `capability-list"><span>Competição</span>`) {
		t.Error("programme membership is rendered in the responsibility list")
	}
}

func TestBaseRendersEnhancedMobileDrawerAndNoJSFallback(t *testing.T) {
	var output strings.Builder
	err := Base(PageMeta{
		Title:           "Hoje | MyCFC",
		CurrentUserName: "Maria Silva",
		Navigation:      []NavigationGroup{{Items: []NavigationItem{{Label: "Hoje", Path: "/today"}}}},
	}, Flash("")).Render(context.Background(), &output)
	if err != nil {
		t.Fatal(err)
	}
	body := output.String()
	for _, expected := range []string{
		`data-mobile-navigation-open hidden`,
		`data-mobile-navigation-fallback`,
		`<dialog id="mobile-navigation"`,
		`aria-labelledby="mobile-navigation-title"`,
		`data-mobile-navigation-close autofocus`,
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("mobile navigation contract does not contain %q", expected)
		}
	}
}

func TestBaseRendersSubjectContext(t *testing.T) {
	var output strings.Builder
	err := Base(PageMeta{
		Title:           "Detalhe do membro | MyCFC",
		StylesheetURL:   "/assets/app.css",
		ScriptURL:       "/assets/app.js",
		CurrentPath:     "/admin/membros/member-123",
		CurrentUserName: "Beatriz Administradora",
		Navigation:      []NavigationGroup{{Items: []NavigationItem{{Label: "Hoje", Path: "/today"}}}, {Label: "Administração", Items: []NavigationItem{{Label: "Membros", Path: "/admin/membros"}}}},
		PageLabel:       "Detalhe do membro",
		SubjectContext:  "Rui Atleta",
		Breadcrumbs:     []NavigationItem{{Label: "Membros", Path: "/admin/membros"}},
	}, Flash("")).Render(context.Background(), &output)
	if err != nil {
		t.Fatalf("render base: %v", err)
	}
	body := output.String()
	for _, expected := range []string{`<a href="/admin/membros">Membros</a>`, `<li aria-current="page">Detalhe do membro</li>`, `A atuar sobre`, `Rui Atleta`, `href="/admin/membros" aria-current="page"`} {
		if !strings.Contains(body, expected) {
			t.Errorf("member context does not contain %q", expected)
		}
	}
}

func TestResolvedAreaUsesOwningNavigationGroupForNestedRoutes(t *testing.T) {
	meta := PageMeta{Navigation: []NavigationGroup{
		{Label: "Atividade", Items: []NavigationItem{{Label: "Eventos", Path: "/events"}}},
		{Label: "Coordenação", Items: []NavigationItem{{Label: "Gerir eventos", Path: "/admin/eventos"}}},
		{Label: "Moderação", Items: []NavigationItem{{Label: "Gerir álbuns", Path: "/admin/albuns"}}},
	}}
	for _, tc := range []struct{ path, want string }{
		{"/events/event-id", "Atividade"},
		{"/admin/eventos/event-id/editar", "Coordenação"},
		{"/admin/albuns/album-id", "Moderação"},
		{"/admin/desconhecido", "Administração"},
	} {
		meta.CurrentPath = tc.path
		if got := resolvedArea(meta); got != tc.want {
			t.Errorf("resolvedArea(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestNavigationMarksCurrentPath(t *testing.T) {
	var output strings.Builder
	err := Navigation("/dashboard", []NavigationGroup{
		{Items: []NavigationItem{
			{Label: "Painel", Path: "/dashboard"},
			{Label: "Terminar sessão", Path: "/logout"},
		}},
	}).Render(context.Background(), &output)
	if err != nil {
		t.Fatalf("render navigation: %v", err)
	}
	if !strings.Contains(output.String(), `href="/dashboard" aria-current="page"`) {
		t.Error("current navigation item is not marked")
	}
	if strings.Contains(output.String(), `href="/logout" aria-current="page"`) {
		t.Error("non-current navigation item is marked")
	}
}

func TestNavigationKeepsCumulativeCapabilitiesVisible(t *testing.T) {
	var output strings.Builder
	err := Navigation("/admin/membros", []NavigationGroup{
		{Items: []NavigationItem{{Label: "Eventos", Path: "/events"}}},
		{Label: "Administração", Items: []NavigationItem{{Label: "Membros", Path: "/admin/membros"}}},
	}).Render(context.Background(), &output)
	if err != nil {
		t.Fatalf("render navigation: %v", err)
	}
	body := output.String()
	if strings.Contains(body, `<details`) {
		t.Error("capability navigation must not hide links in ambiguous menus")
	}
	for _, expected := range []string{`class="site-nav__section"`, `Administração`, `href="/admin/membros" aria-current="page"`} {
		if !strings.Contains(body, expected) {
			t.Errorf("navigation does not contain %q", expected)
		}
	}
}

func TestNavigationMarksOwningDestinationForNestedRoute(t *testing.T) {
	var output strings.Builder
	if err := Navigation("/events/event-123", []NavigationGroup{{Items: []NavigationItem{{Label: "Eventos", Path: "/events"}}}}).Render(context.Background(), &output); err != nil {
		t.Fatalf("render navigation: %v", err)
	}
	if !strings.Contains(output.String(), `href="/events" aria-current="page"`) {
		t.Error("nested route should mark its owning navigation destination")
	}
}

func TestErrorSummaryRendersFieldLinks(t *testing.T) {
	var output strings.Builder
	err := ErrorSummary([]FieldError{{Field: "email", Message: "Introduza um endereço válido."}}).Render(context.Background(), &output)
	if err != nil {
		t.Fatalf("render error summary: %v", err)
	}
	if !strings.Contains(output.String(), `role="alert"`) || !strings.Contains(output.String(), `href="#email"`) {
		t.Fatalf("rendered summary = %q", output.String())
	}
}

func TestFormContractRendersRequiredActionsAndFeedback(t *testing.T) {
	var output strings.Builder
	component := templ.Join(
		RequiredNote(),
		FieldLabel("name", "Nome", true),
		FieldHelp("name-help", "Use o nome completo."),
		FieldErrorMessage("name", ""),
		StatusMessage("Membro criado.", "success"),
	)
	if err := component.Render(context.Background(), &output); err != nil {
		t.Fatalf("render form contract: %v", err)
	}
	if err := FormActions("/members").Render(templ.WithChildren(context.Background(), templ.Raw(`<button type="submit">Criar membro</button>`)), &output); err != nil {
		t.Fatalf("render form actions: %v", err)
	}
	for _, expected := range []string{`Campos obrigatórios`, `for="name"`, `visually-hidden`, `id="name-error"`, `hidden`, `href="/members"`, `Cancelar`, `role="status"`, `tabindex="-1"`} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("form contract does not contain %q", expected)
		}
	}
}

func TestFieldErrorsForMapsValidationKeysToControlIDs(t *testing.T) {
	items := FieldErrorsFor(map[string]string{"date_of_birth": "Indique uma data válida."}, []FieldErrorRef{{Key: "date_of_birth", Field: "member-birth"}})
	if len(items) != 1 || items[0].Field != "member-birth" {
		t.Fatalf("mapped errors = %#v", items)
	}
}

func TestFlashUsesSharedStatusStyling(t *testing.T) {
	var output strings.Builder
	if err := Flash("Guardado.").Render(context.Background(), &output); err != nil {
		t.Fatalf("render flash: %v", err)
	}
	if !strings.Contains(output.String(), `class="flash status-message"`) {
		t.Fatalf("rendered flash = %q", output.String())
	}
}

func TestFoundationComponentsExposeSemanticStates(t *testing.T) {
	var output strings.Builder
	gallery := templ.Join(
		Badge("Pendente", "warning"),
		Callout("Erro", "Corrija os dados.", "error"),
		EmptyState("Sem resultados", "Altere os filtros."),
		DataRegion("Lista de membros", Flash("Conteúdo")),
	)
	if err := gallery.Render(context.Background(), &output); err != nil {
		t.Fatalf("render foundation components: %v", err)
	}
	body := output.String()
	for _, expected := range []string{`badge--warning`, `badge__cue`, `role="alert"`, `empty-state__icon`, `role="region"`, `aria-label="Lista de membros"`, `tabindex="0"`} {
		if !strings.Contains(body, expected) {
			t.Errorf("foundation output does not contain %q", expected)
		}
	}
}

func TestPageHeaderRendersActionContract(t *testing.T) {
	var output strings.Builder
	if err := PageHeader("Administração", "Membros", "Gerir contas.", []PageAction{{Label: "Criar conta", Href: "#criar", Variant: "primary"}}).Render(context.Background(), &output); err != nil {
		t.Fatalf("render page header: %v", err)
	}
	body := output.String()
	for _, expected := range []string{`class="page-header"`, `<h1>Membros</h1>`, `class="action action--primary"`, `href="#criar"`} {
		if !strings.Contains(body, expected) {
			t.Errorf("page header does not contain %q", expected)
		}
	}
}

func TestPageHeaderTaskActionKeepsGuardedFallback(t *testing.T) {
	var output strings.Builder
	action := PageAction{Label: "Criar conta", Href: "/admin/membros/criar", Variant: "primary", TaskID: "criar-conta"}
	if err := PageHeader("Administração", "Membros", "Gerir contas.", []PageAction{action}).Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`href="/admin/membros/criar"`, `aria-haspopup="dialog"`, `aria-controls="criar-conta"`, `data-task-open="criar-conta"`} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("task action does not contain %q: %s", expected, output.String())
		}
	}
}

func TestTaskSurfaceExposesDialogRouteActionsAndConflictContracts(t *testing.T) {
	var output strings.Builder
	config := TaskSurfaceConfig{ID: "create-member", TitleID: "create-member-title", Title: "Criar conta", Variant: "sheet", URL: "/admin/membros/criar", ReturnURL: "/admin/membros"}
	if err := TaskDialog(config).Render(templ.WithChildren(context.Background(), templ.Raw(`<form data-task-form></form>`)), &output); err != nil {
		t.Fatal(err)
	}
	if err := TaskRoute(config).Render(templ.WithChildren(context.Background(), TaskConflict("Dados alterados", "Reveja a versão atual.", "member-name", "/admin/membros", "Atualizar a lista")), &output); err != nil {
		t.Fatal(err)
	}
	if err := TaskActions("/admin/membros").Render(templ.WithChildren(context.Background(), templ.Raw(`<button type="submit">Criar conta</button>`)), &output); err != nil {
		t.Fatal(err)
	}
	if err := DiscardConfirmation().Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	body := output.String()
	for _, expected := range []string{`<dialog id="create-member"`, `data-task-surface`, `aria-labelledby="create-member-title"`, `data-task-url="/admin/membros/criar"`, `task-surface--route`, `<h1 id="create-member-title">Criar conta</h1>`, `role="alert"`, `href="#member-name"`, `data-task-cancel`, `data-task-discard-confirmation`, `Descartar alterações?`} {
		if !strings.Contains(body, expected) {
			t.Errorf("task primitives do not contain %q", expected)
		}
	}
}

func TestPageHeaderAllowsOnlyOnePrimaryAction(t *testing.T) {
	var output strings.Builder
	actions := []PageAction{
		{Label: "Criar conta", Href: "#criar", Variant: "primary"},
		{Label: "Importar contas", Href: "#importar", Variant: "primary"},
		{Label: "Ajuda", Href: "#ajuda", Variant: "unknown"},
	}
	if err := PageHeader("Administração", "Membros", "Gerir contas.", actions).Render(context.Background(), &output); err != nil {
		t.Fatalf("render page header: %v", err)
	}
	body := output.String()
	if got := strings.Count(body, `action--primary`); got != 1 {
		t.Fatalf("primary actions = %d, want exactly one; output = %q", got, body)
	}
	if got := strings.Count(body, `action--secondary`); got != 2 {
		t.Fatalf("secondary actions = %d, want downgraded duplicate and unknown variants", got)
	}
}

func TestFormAndStatusSummaryPrimitivesExposeStructure(t *testing.T) {
	var output strings.Builder
	fields := templ.Join(
		templ.Raw(`<div class="field-group"><label for="member-name">Nome completo da pessoa responsável</label><input id="member-name" disabled></div>`),
		templ.Raw(`<div class="field-group"><label for="member-email">Email</label><input id="member-email" aria-invalid="true"></div>`),
	)
	section := FormSection("Identificação e contacto", "Dados usados para identificar a pessoa.")
	if err := section.Render(templ.WithChildren(context.Background(), fields), &output); err != nil {
		t.Fatalf("render form section: %v", err)
	}
	if err := FieldGrid().Render(templ.WithChildren(context.Background(), fields), &output); err != nil {
		t.Fatalf("render field grid: %v", err)
	}
	if err := StatusSummary("Estado da conta", []StatusItem{{Label: "Conta", Value: "Ativa"}, {Label: "Email", Value: "Por verificar"}}).Render(context.Background(), &output); err != nil {
		t.Fatalf("render status summary: %v", err)
	}
	body := output.String()
	for _, expected := range []string{`<fieldset class="form-section">`, `<legend>Identificação e contacto</legend>`, `class="field-grid"`, `disabled`, `aria-invalid="true"`, `<dl class="status-summary" aria-label="Estado da conta">`, `<dt>Conta</dt><dd>Ativa</dd>`} {
		if !strings.Contains(body, expected) {
			t.Errorf("shared primitives do not contain %q", expected)
		}
	}
}

func TestAuthenticatedStylesDefineEveryConsumedToken(t *testing.T) {
	stylesheet, err := os.ReadFile("../static/src/app.css")
	if err != nil {
		t.Fatalf("read app stylesheet: %v", err)
	}
	definitionPattern := regexp.MustCompile(`(?m)(--[a-z0-9-]+)\s*:`)
	usagePattern := regexp.MustCompile(`var\((--[a-z0-9-]+)\b`)
	defined := make(map[string]bool)
	for _, match := range definitionPattern.FindAllStringSubmatch(string(stylesheet), -1) {
		defined[match[1]] = true
	}
	var undefined []string
	seen := make(map[string]bool)
	for _, match := range usagePattern.FindAllStringSubmatch(string(stylesheet), -1) {
		name := match[1]
		if !defined[name] && !seen[name] {
			undefined = append(undefined, name)
			seen[name] = true
		}
	}
	sort.Strings(undefined)
	if len(undefined) > 0 {
		t.Fatalf("consumed design tokens are undefined: %s", strings.Join(undefined, ", "))
	}
}

func TestRecordCollectionRendersSemanticContract(t *testing.T) {
	var output strings.Builder
	item := RecordItem("Treino técnico", "/treinos/1", "Hoje · 18:30")
	if err := RecordList("Próximas sessões").Render(templ.WithChildren(context.Background(), item), &output); err != nil {
		t.Fatalf("render record list: %v", err)
	}
	body := output.String()
	for _, expected := range []string{`class="record-list"`, `aria-label="Próximas sessões"`, `class="record-item"`, `href="/treinos/1"`, `Treino técnico</a></h3>`, `Hoje · 18:30`} {
		if !strings.Contains(body, expected) {
			t.Errorf("record collection does not contain %q", expected)
		}
	}
}

func TestSectionHeadingSupportsContextualActions(t *testing.T) {
	var output strings.Builder
	ctx := templ.WithChildren(context.Background(), templ.Raw(`<a href="#novo">Novo</a>`))
	if err := SectionHeading("Agenda", "Próximos eventos", "Atividade relevante.").Render(ctx, &output); err != nil {
		t.Fatalf("render section heading: %v", err)
	}
	body := output.String()
	for _, expected := range []string{`class="section-heading"`, `<h2>Próximos eventos</h2>`, `Atividade relevante.`, `href="#novo"`} {
		if !strings.Contains(body, expected) {
			t.Errorf("section heading does not contain %q", expected)
		}
	}
}

func TestCollectionToolbarAndRowActionMenuContracts(t *testing.T) {
	var output strings.Builder
	toolbar := CollectionToolbar(CollectionToolbarData{TitleID: "members-title", Title: "Diretório de membros", Count: "6 nesta página", ClearHref: "/admin/membros", ClearLabel: "Limpar pesquisa"})
	if err := toolbar.Render(templ.WithChildren(context.Background(), templ.Raw(`<form method="get"><input name="q"></form>`)), &output); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`id="members-title"`, `class="collection-toolbar__controls"`, `6 nesta página`, `href="/admin/membros"`, `Limpar pesquisa`} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("toolbar does not contain %q: %s", expected, output.String())
		}
	}
	output.Reset()
	menu := RowActionMenu("actions-equipment-1", "K1 Competição")
	if err := menu.Render(templ.WithChildren(context.Background(), templ.Raw(`<a href="/edit">Editar</a>`)), &output); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`<details class="row-action-menu" data-row-action-menu>`, `aria-controls="actions-equipment-1"`, `aria-label="Mais ações para K1 Competição"`, `data-row-action-menu-panel`, `href="/edit"`} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("row action menu does not contain %q: %s", expected, output.String())
		}
	}
}
