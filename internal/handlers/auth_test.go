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
	account    dbgen.GetActiveAccountByIDRow
	programmes []string
	err        error
}

func (l currentUserLookup) GetActiveAccountByID(context.Context, uuid.UUID) (dbgen.GetActiveAccountByIDRow, error) {
	return l.account, l.err
}
func (l currentUserLookup) ListActiveMembershipProgrammeCodesForUser(context.Context, uuid.UUID) ([]string, error) {
	return l.programmes, l.err
}

func TestAuthLoadsDatabaseAccountInsteadOfSessionData(t *testing.T) {
	id := uuid.New()
	auth := Auth{Users: currentUserLookup{account: dbgen.GetActiveAccountByIDRow{ID: id, IsActive: true}, programmes: []string{"Leisure"}}, Sessions: scs.New()}
	handler := auth.Load(auth.RequireProgramme("Leisure")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })))
	if response := authenticatedRequest(t, auth.Sessions, id.String(), handler); response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestAuthRejectsInvalidInactiveAndDependentAccounts(t *testing.T) {
	for _, lookup := range []currentUserLookup{{err: pgx.ErrNoRows}, {account: dbgen.GetActiveAccountByIDRow{IsActive: false}}, {account: dbgen.GetActiveAccountByIDRow{IsActive: true, IsDependent: true}}} {
		auth := Auth{Users: lookup, Sessions: scs.New()}
		handler := auth.Load(auth.RequireAuthenticated(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("protected handler called") })))
		if response := authenticatedRequest(t, auth.Sessions, uuid.NewString(), handler); response.Code != http.StatusSeeOther {
			t.Fatalf("status = %d", response.Code)
		}
	}
}

func TestAuthGuardsAdminAndMembership(t *testing.T) {
	id := uuid.New()
	auth := Auth{Users: currentUserLookup{account: dbgen.GetActiveAccountByIDRow{ID: id, IsActive: true}}, Sessions: scs.New()}
	handler := auth.Load(auth.RequireAdmin(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("admin handler called") })))
	if response := authenticatedRequest(t, auth.Sessions, id.String(), handler); response.Code != http.StatusForbidden {
		t.Fatalf("admin status = %d", response.Code)
	}
	handler = auth.Load(auth.RequireProgramme("Competition")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("programme handler called") })))
	if response := authenticatedRequest(t, auth.Sessions, id.String(), handler); response.Code != http.StatusForbidden {
		t.Fatalf("programme status = %d", response.Code)
	}
}

func TestAuthDashboardSelection(t *testing.T) {
	id := uuid.New()
	for _, tc := range []struct {
		programmes []string
		admin      bool
		want       string
	}{
		{[]string{"Leisure"}, false, "/dashboard/leisure"},
		{[]string{"Competition"}, true, "/dashboard/competitor"},
		{nil, true, "/admin/fleet"},
		{nil, false, "/dashboard/member"},
	} {
		auth := Auth{Users: currentUserLookup{account: dbgen.GetActiveAccountByIDRow{ID: id, IsActive: true, IsAdmin: tc.admin}, programmes: tc.programmes}, Sessions: scs.New()}
		response := authenticatedRequest(t, auth.Sessions, id.String(), auth.Load(http.HandlerFunc(auth.Dashboard)))
		if response.Header().Get("Location") != tc.want {
			t.Fatalf("location = %q, want %q", response.Header().Get("Location"), tc.want)
		}
	}
}

func TestAuthReturnsInternalErrorForLookupFailure(t *testing.T) {
	auth := Auth{Users: currentUserLookup{err: errors.New("database unavailable")}, Sessions: scs.New()}
	if response := authenticatedRequest(t, auth.Sessions, uuid.NewString(), auth.Load(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))); response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", response.Code)
	}
}

func authenticatedRequest(t *testing.T, sessions *scs.SessionManager, userID string, handler http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	setSession := sessions.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { sessions.Put(r.Context(), "user_id", userID) }))
	setupResponse := httptest.NewRecorder()
	setSession.ServeHTTP(setupResponse, httptest.NewRequest(http.MethodGet, "/", nil))
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.AddCookie(setupResponse.Result().Cookies()[0])
	sessions.LoadAndSave(handler).ServeHTTP(response, request)
	return response
}
