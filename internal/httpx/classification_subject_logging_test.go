package httpx

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClassificationSubjectAccessLogsUseSafeRoutes(t *testing.T) {
	const subject = "12345678-1234-1234-1234-123456789abc"
	for _, tc := range []struct {
		method, path, want string
		routed             bool
	}{
		{"GET", "/equipa/classificacao/" + subject, "GET /equipa/classificacao/{id}", true},
		{"POST", "/equipa/classificacao/" + subject, "POST /equipa/classificacao/{id}", true},
		{"POST", "/equipa/classificacao/" + subject + "/selecoes", "POST /equipa/classificacao/{id}/selecoes", true},
		{"GET", "/equipa/classificacao/" + subject + "/unknown", "/equipa/classificacao/*", true},
		{"POST", "/equipa/classificacao/" + subject + "/selecoes", "/equipa/classificacao/*", false},
		{"GET", "/ordinary/" + subject, "/ordinary/" + subject, false},
	} {
		t.Run(tc.method+tc.path+tc.want, func(t *testing.T) {
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, nil))
			var target http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
			if tc.routed {
				mux := http.NewServeMux()
				for _, pattern := range []string{"GET /equipa/classificacao/{id}", "POST /equipa/classificacao/{id}", "POST /equipa/classificacao/{id}/selecoes"} {
					mux.Handle(pattern, target)
				}
				target = mux
			}
			AccessLogMiddleware(logger)(target).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(tc.method, tc.path+"?q=private-name", nil))
			var entry map[string]any
			if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
				t.Fatal(err)
			}
			if entry["path"] != tc.want {
				t.Errorf("path=%v want %s", entry["path"], tc.want)
			}
			if strings.HasPrefix(tc.path, "/equipa/classificacao/") && strings.Contains(logs.String(), subject) {
				t.Error("selected subject logged:", logs.String())
			}
			if strings.Contains(logs.String(), "private-name") {
				t.Error("query logged")
			}
			for _, key := range []string{"method", "status", "bytes", "duration_ms", "request_id", "remote_ip", "user_id"} {
				if _, ok := entry[key]; !ok {
					t.Errorf("missing general attribute %s", key)
				}
			}
		})
	}
}
