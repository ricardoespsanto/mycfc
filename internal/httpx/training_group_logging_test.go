package httpx

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTrainingGroupAccessLogsOmitSearchAndUnmatchedNames(t *testing.T) {
	for _, route := range []string{"GET /admin/treinos/estruturados/grupos/{id}/atletas", ""} {
		var logs bytes.Buffer
		h := AccessLogMiddleware(slog.New(slog.NewJSONHandler(&logs, nil)))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { r.Pattern = route; w.WriteHeader(404) }))
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/admin/treinos/estruturados/grupos/PRIVATE-PATH/atletas?q=PRIVATE-NAME", nil))
		if strings.Contains(logs.String(), "PRIVATE") || strings.Contains(logs.String(), "q=") {
			t.Fatal(logs.String())
		}
	}
}

func TestTrainingGroupDeniedAndUnmatchedResponsesRemainPrivate(t *testing.T) {
	h := SecurityHeadersMiddleware(false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) }))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/admin/treinos/estruturados/grupos/private/atletas?q=name", nil))
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal(w.Header())
	}
}
