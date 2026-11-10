package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/google/uuid"
)

func TestDefinitionsUsesUnchangedSameOriginStylesUnderCSP(t *testing.T) {
	const originalCSS = `
*{box-sizing:border-box}body{font:1rem/1.5 system-ui;max-width:48rem;margin:auto;padding:1rem;overflow-wrap:anywhere}
label,input,select{display:block;max-width:100%}input,select{width:100%;min-height:2.75rem;margin:.3rem 0 1rem;font:inherit}
button{min-height:2.75rem;font:inherit;padding:.4rem 1rem}a,button,input,select{touch-action:manipulation}
:focus-visible,[tabindex="-1"]:focus{outline:3px solid #205493;outline-offset:3px}
.error{border:2px solid currentColor;padding:1rem;margin:1rem 0}[aria-invalid="true"]{border:2px solid #a52820}
@media(forced-colors:active){.error,[aria-invalid="true"]{border-color:CanvasText}:focus-visible,[tabindex="-1"]:focus{outline-color:Highlight}}
`
	w := httptest.NewRecorder()
	httpx.SecurityHeadersMiddleware(false)(http.HandlerFunc(DefinitionsStylesheet)).ServeHTTP(w, httptest.NewRequest("GET", "/equipa/escaloes.css", nil))
	if w.Code != 200 || w.Header().Get("Content-Type") != "text/css; charset=utf-8" || w.Header().Get("Cache-Control") != "public, max-age=3600" || w.Body.String() != originalCSS {
		t.Fatal("extracted static stylesheet changed content or serving contract")
	}
	csp := w.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "style-src 'self'") || strings.Contains(csp, "unsafe-inline") {
		t.Fatalf("style policy weakened: %s", csp)
	}
	actor, season, programme := uuid.New(), uuid.New(), uuid.New()
	h := Definitions{Store: &definitionFake{scopes: []DefinitionScope{{SeasonID: season, ProgrammeID: programme, Season: "Configured season", Programme: "Competição"}}}}
	r := httptest.NewRequest("GET", "/equipa/escaloes", nil)
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actor}))
	w = httptest.NewRecorder()
	h.Get(w, r)
	if w.Code != 200 || strings.Contains(w.Body.String(), "<style") || !strings.Contains(w.Body.String(), `<link rel="stylesheet" href="/equipa/escaloes.css">`) {
		t.Fatal("definition task still depends on CSP-blocked inline styling")
	}
}
