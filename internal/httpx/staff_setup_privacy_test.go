package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStaffSetupPrivacyCoversEarlyDeniedAndUnmatchedResponses(t *testing.T) {
	for _, path := range []string{"/equipa/classificacao", "/equipa/classificacao/id", "/equipa/classificacao/id/selecoes", "/equipa/classificacao/missing/deep", "/equipa/escaloes", "/equipa/escaloes/missing/deep"} {
		for _, status := range []int{http.StatusSeeOther, http.StatusForbidden, http.StatusNotFound, http.StatusMethodNotAllowed} {
			h := SecurityHeadersMiddleware(false)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("POST", path+"?q=private&origin_day=2026-11-14", nil))
			if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" {
				t.Fatalf("%s status%d lacks privacy: %v", path, status, w.Header())
			}
		}
	}
	for _, path := range []string{"/equipa/classificacao.css", "/equipa/escaloes.css", "/equipa/classificacaox", "/equipa/escaloes-other"} {
		w := httptest.NewRecorder()
		SecurityHeadersMiddleware(false)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Header().Set("Cache-Control", "public, max-age=3600") })).ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Header().Get("Cache-Control") == "no-store" || w.Header().Get("Referrer-Policy") == "no-referrer" {
			t.Fatalf("unrelated/static path scoped as private: %s", path)
		}
	}
}
