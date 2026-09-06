package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cfcoimbra/mycfc/ui/components"
)

func TestLegalDocumentsAreAllowlistedPublicAndVersioned(t *testing.T) {
	handler := NewLegal(components.PageMeta{StylesheetURL: "/assets/app.css"})
	for _, slug := range []string{"privacidade", "termos-gerais", "cookies", "uso-imagem", "responsabilidade-menor", "direitos", "privacidade-menores"} {
		t.Run(slug, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/legal/"+slug, nil)
			request.SetPathValue("slug", slug)
			response := httptest.NewRecorder()
			handler.Get(response, request)
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Versão: <code>2026-09-06</code>") || response.Header().Get("ETag") == "" {
				t.Fatalf("response=%d etag=%q body=%s", response.Code, response.Header().Get("ETag"), response.Body.String())
			}
		})
	}

	request := httptest.NewRequest(http.MethodGet, "/legal/interno", nil)
	request.SetPathValue("slug", "matriz-conservacao")
	response := httptest.NewRecorder()
	handler.Get(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("internal document status=%d", response.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/legal/termos-gerais/old", nil)
	request.SetPathValue("slug", "termos-gerais")
	request.SetPathValue("version", "old")
	response = httptest.NewRecorder()
	handler.Get(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("unknown version status=%d", response.Code)
	}
}
