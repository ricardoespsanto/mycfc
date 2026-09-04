package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cfcoimbra/mycfc/ui/components"
)

func TestLandingRendersPublicClubPageWithConfiguredAssets(t *testing.T) {
	response := httptest.NewRecorder()
	Landing{PageMeta: components.PageMeta{StylesheetURL: "/assets/app.css", ScriptURL: "/assets/app.js", BrandImageURL: "/assets/logo.png"}, HeroURL: "/assets/hero.png"}.Get(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("response=%d headers=%v", response.Code, response.Header())
	}
	for _, expected := range []string{"/assets/app.css", "/assets/app.js", "/assets/logo.png", "/assets/hero.png"} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Errorf("body missing %q", expected)
		}
	}
}
