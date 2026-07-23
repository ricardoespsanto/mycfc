package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alexedwards/scs/v2"
)

type routerPinger struct{ err error }

func (p routerPinger) Ping(context.Context) error { return p.err }

func TestRouterHealthAndMethodSemantics(t *testing.T) {
	router := newTestRouter(routerPinger{})
	for _, tc := range []struct {
		method   string
		path     string
		status   int
		allow    string
		location string
	}{
		{http.MethodGet, "/", http.StatusSeeOther, "", "/login"},
		{http.MethodGet, "/health/live", http.StatusOK, "", ""},
		{http.MethodPost, "/health/live", http.StatusMethodNotAllowed, "GET, HEAD", ""},
		{http.MethodGet, "/missing", http.StatusNotFound, "", ""},
		{http.MethodGet, "/login", http.StatusNotImplemented, "", ""},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(tc.method, tc.path, nil))
			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d", response.Code, tc.status)
			}
			if tc.allow != "" && response.Header().Get("Allow") != tc.allow {
				t.Fatalf("Allow = %q, want %q", response.Header().Get("Allow"), tc.allow)
			}
			if tc.location != "" && response.Header().Get("Location") != tc.location {
				t.Fatalf("Location = %q, want %q", response.Header().Get("Location"), tc.location)
			}
		})
	}
}

func TestRouterReadinessFailure(t *testing.T) {
	router := newTestRouter(routerPinger{err: errors.New("down")})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", response.Code)
	}
}

func newTestRouter(pinger routerPinger) http.Handler {
	sessions := scs.New()
	return sessions.LoadAndSave(newRouter(pinger, sessions))
}
