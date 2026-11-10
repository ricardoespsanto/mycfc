package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Authorization, CSRF and routing can reject requests before a season handler
// runs. Their responses must retain the task's privacy protections too.
func TestSeasonEarlyRejectionsRemainPrivate(t *testing.T) {
	for _, tc := range []struct {
		name, method, path string
		status             int
	}{
		{"authorization", http.MethodGet, "/admin/epocas?return_q=private", http.StatusForbidden},
		{"csrf", http.MethodPost, "/admin/epocas/criar", http.StatusForbidden},
		{"unmatched", http.MethodGet, "/admin/epocas/missing", http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := SecurityHeadersMiddleware(false)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
			}))
			response := httptest.NewRecorder()
			h.ServeHTTP(response, httptest.NewRequest(tc.method, tc.path, nil))
			if response.Code != tc.status || response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Referrer-Policy") != "no-referrer" {
				t.Fatalf("status=%d headers=%v", response.Code, response.Header())
			}
		})
	}
}
