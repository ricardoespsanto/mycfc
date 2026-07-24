package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alexedwards/scs/v2"
	"github.com/cfcoimbra/mycfc/internal/handlers"
)

type routerPinger struct{ err error }

func (p routerPinger) Ping(context.Context) error { return p.err }

func TestRouterHealthAndMethodSemantics(t *testing.T) {
	router := newTestRouter(routerPinger{}, handlers.Login{}, handlers.Registration{}, handlers.Auth{}, handlers.Dashboard{})
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
		{http.MethodGet, "/login", http.StatusOK, "", ""},
		{http.MethodGet, "/registo", http.StatusOK, "", ""},
		{http.MethodGet, "/dashboard", http.StatusSeeOther, "", "/login?next=%2Fdashboard"},
		{http.MethodGet, "/admin/fleet", http.StatusSeeOther, "", "/login?next=%2Fadmin%2Ffleet"},
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
	router := newTestRouter(routerPinger{err: errors.New("down")}, handlers.Login{}, handlers.Registration{}, handlers.Auth{}, handlers.Dashboard{})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", response.Code)
	}
}

func newTestRouter(pinger routerPinger, login handlers.Login, registration handlers.Registration, auth handlers.Auth, dashboard handlers.Dashboard) http.Handler {
	sessions := scs.New()
	login.Sessions = sessions
	registration.Sessions = sessions
	auth.Sessions = sessions
	return sessions.LoadAndSave(auth.Load(newRouter(pinger, sessions, login, registration, auth, dashboard)))
}
