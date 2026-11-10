package app

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cfcoimbra/mycfc/internal/handlers"
	"github.com/cfcoimbra/mycfc/internal/httpx"
)

func TestStaffSetupPublicStylesAndEarlyPrivacyWithRealMiddleware(t *testing.T) {
	router := newTestRouter(routerPinger{}, handlers.Landing{}, handlers.Login{}, handlers.Registration{}, handlers.Auth{}, handlers.Dashboard{})
	h := httpx.SecurityHeadersMiddleware(false)(csrfProtection(make([]byte, 32), handlers.System{})(router))
	for _, path := range []string{"/equipa/classificacao.css", "/equipa/escaloes.css"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/css") || w.Header().Get("Cache-Control") != "public, max-age=3600" {
			t.Fatalf("anonymous stylesheet %s status=%d headers=%v", path, w.Code, w.Header())
		}
	}
	for _, path := range []string{"/equipa/classificacao", "/equipa/classificacao/member", "/equipa/classificacao/member/selecoes", "/equipa/classificacao/missing/deep", "/equipa/escaloes", "/equipa/escaloes/missing/deep"} {
		for _, method := range []string{"GET", "POST"} {
			r := httptest.NewRequest(method, "https://mycfc.example"+path+"?q=private", strings.NewReader("origin_group=private"))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if method == "POST" {
				r.Header.Set("Sec-Fetch-Site", "cross-site")
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code < 300 || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" {
				t.Fatalf("early %s %s status=%d headers=%v", method, path, w.Code, w.Header())
			}
		}
	}
}
