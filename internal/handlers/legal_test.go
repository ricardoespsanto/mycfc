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
			wantVersion := "2026-09-06"
			if slug == "privacidade" {
				wantVersion = "2026-09-11"
			}
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Versão: <code>"+wantVersion+"</code>") || response.Header().Get("ETag") == "" {
				t.Fatalf("response=%d etag=%q body=%s", response.Code, response.Header().Get("ETag"), response.Body.String())
			}
			if slug == "privacidade" && (!strings.Contains(response.Body.String(), "O servidor MyCFCoimbra não envia nem copia perfis") ||
				!strings.Contains(response.Body.String(), "o endereço de destino contém esse número de licença") ||
				strings.Contains(response.Body.String(), "No funcionamento desportivo corrente") ||
				response.Header().Get("ETag") != `"28f3d03363d0f312aefb46f5d7f4aa40c8517e832be49bd112335e5ff192f929"`) {
				t.Fatalf("current privacy notice omits the FPC browser-navigation boundary: %s", response.Body.String())
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

	request = httptest.NewRequest(http.MethodGet, "/legal/termos-gerais/2026-09-06", nil)
	request.SetPathValue("slug", "termos-gerais")
	request.SetPathValue("version", "2026-09-06")
	response = httptest.NewRecorder()
	handler.Get(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("versioned response=%d cache-control=%q", response.Code, response.Header().Get("Cache-Control"))
	}

	request = httptest.NewRequest(http.MethodGet, "/legal/privacidade/2026-09-06", nil)
	request.SetPathValue("slug", "privacidade")
	request.SetPathValue("version", "2026-09-06")
	response = httptest.NewRecorder()
	handler.Get(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Versão: <code>2026-09-06</code>") ||
		!strings.Contains(response.Body.String(), "No funcionamento desportivo corrente") ||
		response.Header().Get("ETag") != `"f8eb91b2d41f744bf502745aa9e62c51e1333b8b9115f6419654f30084089935"` ||
		response.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("historical privacy response=%d cache-control=%q", response.Code, response.Header().Get("Cache-Control"))
	}
}
