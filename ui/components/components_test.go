package components

import (
	"context"
	"strings"
	"testing"
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

func TestBaseRendersAdminSubNavOnAdminPages(t *testing.T) {
	var output strings.Builder
	err := Base(PageMeta{
		Title:         "Gestão de membros | MyCFC",
		StylesheetURL: "/assets/app.css",
		ScriptURL:     "/assets/app.js",
		CurrentPath:   "/admin/membros",
	}, Flash("")).Render(context.Background(), &output)
	if err != nil {
		t.Fatalf("render base: %v", err)
	}
	body := output.String()
	if !strings.Contains(body, `class="admin-subnav"`) {
		t.Error("admin page should render the admin sub-nav")
	}
	if !strings.Contains(body, `href="/admin/membros" aria-current="page"`) {
		t.Error("admin sub-nav should mark the current section")
	}
	if strings.Contains(body, `href="/admin/fleet" aria-current="page"`) {
		t.Error("admin sub-nav should not mark other sections as current")
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

func TestNavigationGroupsSecondaryLinksInDisclosures(t *testing.T) {
	var output strings.Builder
	err := Navigation("/admin/membros", []NavigationGroup{
		{Items: []NavigationItem{{Label: "Eventos", Path: "/events"}}},
		{Label: "Administração", Items: []NavigationItem{{Label: "Membros", Path: "/admin/membros"}}},
	}).Render(context.Background(), &output)
	if err != nil {
		t.Fatalf("render navigation: %v", err)
	}
	body := output.String()
	if !strings.Contains(body, `<details class="site-nav__group">`) {
		t.Error("labelled group should render as a disclosure")
	}
	if !strings.Contains(body, `<summary>`) || !strings.Contains(body, `Administração`) {
		t.Error("labelled group should show its label in the summary")
	}
	if !strings.Contains(body, `site-nav__group-indicator`) {
		t.Error("group containing the current path should show an indicator")
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

func TestFlashUsesSharedStatusStyling(t *testing.T) {
	var output strings.Builder
	if err := Flash("Guardado.").Render(context.Background(), &output); err != nil {
		t.Fatalf("render flash: %v", err)
	}
	if !strings.Contains(output.String(), `class="flash status-message"`) {
		t.Fatalf("rendered flash = %q", output.String())
	}
}
