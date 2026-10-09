package httpx

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClassificationSearchTermsNeverEnterAccessLogs(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	handler := AccessLogMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Pattern = "GET /equipa/classificacao"
		w.WriteHeader(http.StatusOK)
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/equipa/classificacao?q=never-log-person-name&page=2", nil))
	if strings.Contains(logs.String(), "never-log-person-name") || strings.Contains(logs.String(), "q=") {
		t.Fatalf("search query logged: %s", logs.String())
	}
	if !strings.Contains(logs.String(), `"path":"/equipa/classificacao"`) {
		t.Fatal(logs.String())
	}
}
