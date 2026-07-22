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
	router := newRouter(routerPinger{}, scs.New())
	for _, tc := range []struct {
		method string
		path   string
		status int
		allow  string
	}{
		{http.MethodGet, "/health/live", http.StatusOK, ""},
		{http.MethodPost, "/health/live", http.StatusMethodNotAllowed, "GET, HEAD"},
		{http.MethodGet, "/missing", http.StatusNotFound, ""},
		{http.MethodGet, "/login", http.StatusNotImplemented, ""},
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
		})
	}
}

func TestRouterReadinessFailure(t *testing.T) {
	router := newRouter(routerPinger{err: errors.New("down")}, scs.New())
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", response.Code)
	}
}
