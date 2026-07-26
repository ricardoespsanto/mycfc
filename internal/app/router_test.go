package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alexedwards/scs/v2"
	"github.com/cfcoimbra/mycfc/internal/handlers"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/gorilla/csrf"
)

type routerPinger struct{ err error }

func (p routerPinger) Ping(context.Context) error { return p.err }

func TestRouterHealthAndMethodSemantics(t *testing.T) {
	router := newTestRouter(routerPinger{}, handlers.Landing{}, handlers.Login{}, handlers.Registration{}, handlers.Auth{}, handlers.Dashboard{})
	for _, tc := range []struct {
		method   string
		path     string
		status   int
		allow    string
		location string
	}{
		{http.MethodGet, "/", http.StatusOK, "", ""},
		{http.MethodGet, "/health/live", http.StatusOK, "", ""},
		{http.MethodPost, "/health/live", http.StatusMethodNotAllowed, "GET, HEAD", ""},
		{http.MethodGet, "/missing", http.StatusNotFound, "", ""},
		{http.MethodGet, "/login", http.StatusOK, "", ""},
		{http.MethodGet, "/registo", http.StatusOK, "", ""},
		{http.MethodGet, "/dashboard", http.StatusSeeOther, "", "/login?next=%2Fdashboard"},
		{http.MethodGet, "/admin/fleet", http.StatusSeeOther, "", "/login?next=%2Fadmin%2Ffleet"},
		{http.MethodGet, "/admin/membros", http.StatusSeeOther, "", "/login?next=%2Fadmin%2Fmembros"},
		{http.MethodGet, "/admin/noticias", http.StatusSeeOther, "", "/login?next=%2Fadmin%2Fnoticias"},
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
	router := newTestRouter(routerPinger{err: errors.New("down")}, handlers.Landing{}, handlers.Login{}, handlers.Registration{}, handlers.Auth{}, handlers.Dashboard{})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestPlaintextCSRFMiddleware(t *testing.T) {
	handler := plaintextCSRFMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if enabled, _ := r.Context().Value(csrf.PlaintextHTTPContextKey).(bool); !enabled {
			t.Fatal("plaintext CSRF context not enabled")
		}
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request = request.WithContext(httpx.WithScheme(request.Context(), "http"))
	handler.ServeHTTP(httptest.NewRecorder(), request)
}

func TestLandingRedirectsAuthenticatedVisitors(t *testing.T) {
	router := newRouter(routerPinger{}, scs.New(), handlers.Landing{}, handlers.Login{}, handlers.Registration{}, handlers.Auth{}, handlers.Dashboard{}, handlers.Repair{}, handlers.Events{}, handlers.Announcements{}, handlers.Training{}, handlers.Members{}, handlers.News{})
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request = request.WithContext(httpx.WithUserID(request.Context(), "current-user"))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/dashboard" {
		t.Fatalf("response = %d %q", response.Code, response.Header().Get("Location"))
	}
}

func TestLandingRendersPublicCallsToAction(t *testing.T) {
	router := newTestRouter(routerPinger{}, handlers.Landing{}, handlers.Login{}, handlers.Registration{}, handlers.Auth{}, handlers.Dashboard{})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	for _, expected := range []string{"A promover a canoagem", `href="/registo"`, `href="/login"`, "cfluvialcoimbra@gmail.com"} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Errorf("landing does not contain %q", expected)
		}
	}
}

func newTestRouter(pinger routerPinger, landing handlers.Landing, login handlers.Login, registration handlers.Registration, auth handlers.Auth, dashboard handlers.Dashboard) http.Handler {
	sessions := scs.New()
	login.Sessions = sessions
	registration.Sessions = sessions
	auth.Sessions = sessions
	return sessions.LoadAndSave(auth.Load(newRouter(pinger, sessions, landing, login, registration, auth, dashboard, handlers.Repair{}, handlers.Events{}, handlers.Announcements{}, handlers.Training{}, handlers.Members{}, handlers.News{})))
}
