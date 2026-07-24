package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alexedwards/scs/v2"
	"github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type currentUserLookup struct {
	user dbgen.User
	err  error
}

func (l currentUserLookup) GetUserByID(context.Context, uuid.UUID) (dbgen.User, error) {
	return l.user, l.err
}

func TestAuthLoadsDatabaseUserInsteadOfSessionRole(t *testing.T) {
	id := uuid.New()
	auth := Auth{Users: currentUserLookup{user: dbgen.User{ID: id, Role: "Guardian", IsActive: true}}, Sessions: scs.New()}
	handler := auth.Load(auth.RequireRole("Guardian")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := httpUserID(r.Context()); got != id.String() {
			t.Errorf("user ID = %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	})))
	response := authenticatedRequest(t, auth.Sessions, id.String(), "Admin", handler)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestAuthDestroysInvalidUserSessionAndRedirectsToLogin(t *testing.T) {
	auth := Auth{Users: currentUserLookup{err: pgx.ErrNoRows}, Sessions: scs.New()}
	handler := auth.Load(auth.RequireRole("Guardian")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("protected handler called") })))
	response := authenticatedRequest(t, auth.Sessions, uuid.New().String(), "Guardian", handler)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/login?next=%2Fprotected" {
		t.Fatalf("response = %d %q", response.Code, response.Header().Get("Location"))
	}
}

func TestAuthRejectsInactiveUser(t *testing.T) {
	auth := Auth{Users: currentUserLookup{user: dbgen.User{ID: uuid.New(), Role: "Guardian", IsActive: false}}, Sessions: scs.New()}
	handler := auth.Load(auth.RequireRole("Guardian")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("protected handler called") })))
	response := authenticatedRequest(t, auth.Sessions, uuid.New().String(), "Guardian", handler)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestAuthRejectsDependentAndWrongRole(t *testing.T) {
	id := uuid.New()
	auth := Auth{Users: currentUserLookup{user: dbgen.User{ID: id, Role: "Competitor", IsActive: true, IsDependent: true}}, Sessions: scs.New()}
	handler := auth.Load(auth.RequireRole("Competitor")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("protected handler called") })))
	if response := authenticatedRequest(t, auth.Sessions, id.String(), "Competitor", handler); response.Code != http.StatusSeeOther {
		t.Fatalf("dependent status = %d", response.Code)
	}

	auth.Users = currentUserLookup{user: dbgen.User{ID: id, Role: "Leisure", IsActive: true}}
	handler = auth.Load(auth.RequireRole("Competitor")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("protected handler called") })))
	if response := authenticatedRequest(t, auth.Sessions, id.String(), "Competitor", handler); response.Code != http.StatusForbidden {
		t.Fatalf("wrong role status = %d", response.Code)
	}
}

func TestAuthReturnsInternalErrorForLookupFailure(t *testing.T) {
	auth := Auth{Users: currentUserLookup{err: errors.New("database unavailable")}, Sessions: scs.New()}
	handler := auth.Load(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("next handler called") }))
	response := authenticatedRequest(t, auth.Sessions, uuid.New().String(), "Guardian", handler)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestAuthDashboardAndLogout(t *testing.T) {
	id := uuid.New()
	auth := Auth{Users: currentUserLookup{user: dbgen.User{ID: id, Role: "Leisure", IsActive: true}}, Sessions: scs.New()}
	response := authenticatedRequest(t, auth.Sessions, id.String(), "Guardian", auth.Load(auth.RequireRole("Admin", "Competitor", "Leisure", "Guardian")(http.HandlerFunc(auth.Dashboard))))
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/dashboard/leisure" {
		t.Fatalf("dashboard response = %d %q", response.Code, response.Header().Get("Location"))
	}
	response = authenticatedRequest(t, auth.Sessions, id.String(), "Leisure", auth.Load(http.HandlerFunc(auth.Logout)))
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/login" {
		t.Fatalf("logout response = %d %q", response.Code, response.Header().Get("Location"))
	}
}

func authenticatedRequest(t *testing.T, sessions *scs.SessionManager, userID, role string, handler http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	setSession := sessions.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sessions.Put(r.Context(), "user_id", userID)
		sessions.Put(r.Context(), "role", role)
	}))
	setupResponse := httptest.NewRecorder()
	setSession.ServeHTTP(setupResponse, httptest.NewRequest(http.MethodGet, "/", nil))
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.AddCookie(setupResponse.Result().Cookies()[0])
	response := httptest.NewRecorder()
	sessions.LoadAndSave(handler).ServeHTTP(response, request)
	return response
}

func httpUserID(ctx context.Context) string {
	user, ok := currentUser(ctx)
	if !ok {
		return ""
	}
	return user.ID.String()
}
