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
}

func TestNavigationMarksCurrentPath(t *testing.T) {
	var output strings.Builder
	err := Navigation("/dashboard", []NavigationItem{
		{Label: "Painel", Path: "/dashboard"},
		{Label: "Terminar sessão", Path: "/logout"},
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
