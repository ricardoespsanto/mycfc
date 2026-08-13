package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/handlers"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/google/uuid"
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
		{http.MethodGet, "/admin/componentes", http.StatusNotFound, "", ""},
		{http.MethodGet, "/login", http.StatusOK, "", ""},
		{http.MethodGet, "/registo", http.StatusOK, "", ""},
		{http.MethodGet, "/recuperar-palavra-passe", http.StatusOK, "", ""},
		{http.MethodGet, "/recuperar-palavra-passe/repor", http.StatusUnprocessableEntity, "", ""},
		{http.MethodGet, "/verificar-email", http.StatusUnprocessableEntity, "", ""},
		{http.MethodPost, "/perfil/email-verificacao/reenviar", http.StatusSeeOther, "", "/login?next=%2Fperfil%2Femail-verificacao%2Freenviar"},
		{http.MethodGet, "/dashboard", http.StatusSeeOther, "", "/login?next=%2Fdashboard"},
		{http.MethodGet, "/perfil", http.StatusSeeOther, "", "/login?next=%2Fperfil"},
		{http.MethodGet, "/fleet", http.StatusSeeOther, "", "/login?next=%2Ffleet"},
		{http.MethodGet, "/admin/fleet", http.StatusSeeOther, "", "/login?next=%2Fadmin%2Ffleet"},
		{http.MethodGet, "/admin/fleet/equipment/00000000-0000-0000-0000-000000000000/edit", http.StatusSeeOther, "", "/login?next=%2Fadmin%2Ffleet%2Fequipment%2F00000000-0000-0000-0000-000000000000%2Fedit"},
		{http.MethodGet, "/admin/sistema", http.StatusSeeOther, "", "/login?next=%2Fadmin%2Fsistema"},
		{http.MethodGet, "/admin/membros", http.StatusSeeOther, "", "/login?next=%2Fadmin%2Fmembros"},
		{http.MethodGet, "/admin/noticias", http.StatusSeeOther, "", "/login?next=%2Fadmin%2Fnoticias"},
		{http.MethodGet, "/sugestoes", http.StatusSeeOther, "", "/login?next=%2Fsugestoes"},
		{http.MethodGet, "/admin/sugestoes", http.StatusSeeOther, "", "/login?next=%2Fadmin%2Fsugestoes"},
		{http.MethodPost, "/admin/sugestoes/00000000-0000-0000-0000-000000000000", http.StatusSeeOther, "", "/login?next=%2Fadmin%2Fsugestoes%2F00000000-0000-0000-0000-000000000000"},
		{http.MethodGet, "/admin/eventos/00000000-0000-0000-0000-000000000000/editar", http.StatusSeeOther, "", "/login?next=%2Fadmin%2Feventos%2F00000000-0000-0000-0000-000000000000%2Feditar"},
		{http.MethodPost, "/admin/events/00000000-0000-0000-0000-000000000000", http.StatusSeeOther, "", "/login?next=%2Fadmin%2Fevents%2F00000000-0000-0000-0000-000000000000"},
		{http.MethodPost, "/admin/events/00000000-0000-0000-0000-000000000000/cancel", http.StatusSeeOther, "", "/login?next=%2Fadmin%2Fevents%2F00000000-0000-0000-0000-000000000000%2Fcancel"},
		{http.MethodGet, "/admin/treinos/sessoes/00000000-0000-0000-0000-000000000000/editar", http.StatusSeeOther, "", "/login?next=%2Fadmin%2Ftreinos%2Fsessoes%2F00000000-0000-0000-0000-000000000000%2Feditar"},
		{http.MethodPost, "/admin/treinos/sessoes/00000000-0000-0000-0000-000000000000", http.StatusSeeOther, "", "/login?next=%2Fadmin%2Ftreinos%2Fsessoes%2F00000000-0000-0000-0000-000000000000"},
		{http.MethodPost, "/admin/treinos/sessoes/00000000-0000-0000-0000-000000000000/cancelar", http.StatusSeeOther, "", "/login?next=%2Fadmin%2Ftreinos%2Fsessoes%2F00000000-0000-0000-0000-000000000000%2Fcancelar"},
		{http.MethodGet, "/treinos/estruturados", http.StatusSeeOther, "", "/login?next=%2Ftreinos%2Festruturados"},
		{http.MethodGet, "/admin/treinos/estruturados", http.StatusSeeOther, "", "/login?next=%2Fadmin%2Ftreinos%2Festruturados"},
		{http.MethodPost, "/admin/treinos/estruturados/sessoes/00000000-0000-0000-0000-000000000000/segmentos", http.StatusSeeOther, "", "/login?next=%2Fadmin%2Ftreinos%2Festruturados%2Fsessoes%2F00000000-0000-0000-0000-000000000000%2Fsegmentos"},
		{http.MethodPost, "/admin/treinos/estruturados/segmentos/00000000-0000-0000-0000-000000000000/ginasio", http.StatusSeeOther, "", "/login?next=%2Fadmin%2Ftreinos%2Festruturados%2Fsegmentos%2F00000000-0000-0000-0000-000000000000%2Fginasio"},
		{http.MethodPost, "/admin/treinos/estruturados/blocos/00000000-0000-0000-0000-000000000000/exercicios", http.StatusSeeOther, "", "/login?next=%2Fadmin%2Ftreinos%2Festruturados%2Fblocos%2F00000000-0000-0000-0000-000000000000%2Fexercicios"},
		{http.MethodPost, "/admin/treinos/estruturados/exercicios/00000000-0000-0000-0000-000000000000/mover", http.StatusSeeOther, "", "/login?next=%2Fadmin%2Ftreinos%2Festruturados%2Fexercicios%2F00000000-0000-0000-0000-000000000000%2Fmover"},
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

func TestPasswordRecoveryRoutesRenderCSRFAndRejectCrossSitePosts(t *testing.T) {
	sessions := scs.New()
	auth := handlers.Auth{Sessions: sessions}
	recovery := handlers.PasswordRecovery{Sessions: sessions, ResponseWait: func(context.Context, time.Time) {}}
	router := newRouter(routerPinger{}, sessions, handlers.Landing{}, handlers.Login{Sessions: sessions}, handlers.Registration{Sessions: sessions}, handlers.EmailVerification{}, recovery, auth, handlers.Dashboard{}, handlers.Repair{}, handlers.Events{}, handlers.Announcements{}, handlers.Training{}, handlers.StructuredTraining{}, handlers.Members{}, handlers.Profile{}, handlers.News{}, handlers.Suggestions{}, handlers.PhotoAlbums{}, handlers.Foundation{})
	handler := httpx.SecurityHeadersMiddleware(false)(csrfProtection(make([]byte, 32), handlers.System{})(router))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/recuperar-palavra-passe", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `name="gorilla.csrf.Token"`) {
		t.Fatalf("GET response = %d %q", response.Code, response.Body.String())
	}

	request := httptest.NewRequest(http.MethodPost, "https://mycfc.example/recuperar-palavra-passe", strings.NewReader("identifier=member%40example.test"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("POST response = %d, cache %q", response.Code, response.Header().Get("Cache-Control"))
	}
}

type routerCurrentUserLookup struct{ id uuid.UUID }

func (l routerCurrentUserLookup) GetActiveAccountByID(context.Context, uuid.UUID) (dbgen.GetActiveAccountByIDRow, error) {
	return dbgen.GetActiveAccountByIDRow{ID: l.id, IsActive: true, CredentialVersion: 1}, nil
}
func (l routerCurrentUserLookup) GetActiveAccountByIDWithoutProfile(context.Context, uuid.UUID) (dbgen.GetActiveAccountByIDWithoutProfileRow, error) {
	return dbgen.GetActiveAccountByIDWithoutProfileRow{ID: l.id, IsActive: true, CredentialVersion: 1}, nil
}
func (routerCurrentUserLookup) ListActiveMembershipProgrammeCodesForUser(context.Context, uuid.UUID) ([]string, error) {
	return nil, nil
}
func (routerCurrentUserLookup) ListActiveStaffGrantsForUser(context.Context, uuid.UUID) ([]dbgen.ListActiveStaffGrantsForUserRow, error) {
	return nil, nil
}

func TestPasswordRecoveryRoutesRedirectAuthenticatedAccounts(t *testing.T) {
	sessions := scs.New()
	id := uuid.New()
	auth := handlers.Auth{Sessions: sessions, Users: routerCurrentUserLookup{id: id}}
	recovery := handlers.PasswordRecovery{Sessions: sessions, ResponseWait: func(context.Context, time.Time) {}}
	router := sessions.LoadAndSave(auth.Load(newRouter(routerPinger{}, sessions, handlers.Landing{}, handlers.Login{Sessions: sessions}, handlers.Registration{Sessions: sessions}, handlers.EmailVerification{}, recovery, auth, handlers.Dashboard{}, handlers.Repair{}, handlers.Events{}, handlers.Announcements{}, handlers.Training{}, handlers.StructuredTraining{}, handlers.Members{}, handlers.Profile{}, handlers.News{}, handlers.Suggestions{}, handlers.PhotoAlbums{}, handlers.Foundation{})))

	seed := httptest.NewRecorder()
	sessions.LoadAndSave(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		sessions.Put(r.Context(), "user_id", id.String())
		sessions.Put(r.Context(), "credential_version", int64(1))
	})).ServeHTTP(seed, httptest.NewRequest(http.MethodGet, "/", nil))
	cookie := seed.Result().Cookies()[0]

	for _, path := range []string{"/recuperar-palavra-passe", "/recuperar-palavra-passe/repor?token=opaque"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.AddCookie(cookie)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/dashboard" {
			t.Fatalf("%s response = %d %q", path, response.Code, response.Header().Get("Location"))
		}
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

func TestCompatibilityRedirectPreservesQueryParameters(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/dashboard/coach?scope=competition&return=%2Ftoday", nil)
	response := httptest.NewRecorder()
	compatibilityRedirect("/events").ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/events?scope=competition&return=%2Ftoday" {
		t.Fatalf("response = %d %q", response.Code, response.Header().Get("Location"))
	}
}

func TestCSRFProtectionRejectsCrossSiteBrowserRequest(t *testing.T) {
	called := false
	handler := csrfProtection(make([]byte, 32), handlers.System{})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	request := httptest.NewRequest(http.MethodPost, "https://mycfc.example/logout", nil)
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
	}
	if called {
		t.Fatal("cross-site request reached protected handler")
	}
}

func TestLandingRedirectsAuthenticatedVisitors(t *testing.T) {
	router := newRouter(routerPinger{}, scs.New(), handlers.Landing{}, handlers.Login{}, handlers.Registration{}, handlers.EmailVerification{}, handlers.PasswordRecovery{}, handlers.Auth{}, handlers.Dashboard{}, handlers.Repair{}, handlers.Events{}, handlers.Announcements{}, handlers.Training{}, handlers.StructuredTraining{}, handlers.Members{}, handlers.Profile{}, handlers.News{}, handlers.Suggestions{}, handlers.PhotoAlbums{}, handlers.Foundation{})
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
	return sessions.LoadAndSave(auth.Load(newRouter(pinger, sessions, landing, login, registration, handlers.EmailVerification{}, handlers.PasswordRecovery{Sessions: sessions, ResponseWait: func(context.Context, time.Time) {}}, auth, dashboard, handlers.Repair{}, handlers.Events{}, handlers.Announcements{}, handlers.Training{}, handlers.StructuredTraining{}, handlers.Members{}, handlers.Profile{}, handlers.News{}, handlers.Suggestions{}, handlers.PhotoAlbums{}, handlers.Foundation{})))
}
