package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alexedwards/scs/v2"
	"github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/featureflags"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type currentUserLookup struct {
	account         dbgen.GetActiveAccountByIDRow
	programmes      []string
	grants          []dbgen.ListActiveStaffGrantsForUserRow
	err             error
	profileFallback bool
}

type featureFlagLookup struct {
	rows []dbgen.ListFeatureFlagsRow
	err  error
}

func (l featureFlagLookup) ListFeatureFlags(context.Context) ([]dbgen.ListFeatureFlagsRow, error) {
	return l.rows, l.err
}

func (l currentUserLookup) GetActiveAccountByID(context.Context, uuid.UUID) (dbgen.GetActiveAccountByIDRow, error) {
	account := l.account
	if account.CredentialVersion == 0 {
		account.CredentialVersion = 1
	}
	return account, l.err
}
func (l currentUserLookup) GetActiveAccountByIDWithoutProfile(context.Context, uuid.UUID) (dbgen.GetActiveAccountByIDWithoutProfileRow, error) {
	return dbgen.GetActiveAccountByIDWithoutProfileRow{
		ID: l.account.ID, Name: l.account.Name, IsDependent: l.account.IsDependent,
		IsActive: l.account.IsActive, LeaderboardVisible: l.account.LeaderboardVisible,
		IsAdmin: l.account.IsAdmin, CredentialVersion: max(l.account.CredentialVersion, 1),
	}, nil
}
func (l currentUserLookup) ListActiveMembershipProgrammeCodesForUser(context.Context, uuid.UUID) ([]string, error) {
	if l.profileFallback {
		return l.programmes, nil
	}
	return l.programmes, l.err
}
func (l currentUserLookup) ListActiveStaffGrantsForUser(context.Context, uuid.UUID) ([]dbgen.ListActiveStaffGrantsForUserRow, error) {
	if l.profileFallback {
		return l.grants, nil
	}
	return l.grants, l.err
}

func TestAuthLoadsDatabaseAccountInsteadOfSessionData(t *testing.T) {
	id := uuid.New()
	auth := Auth{Users: currentUserLookup{account: dbgen.GetActiveAccountByIDRow{ID: id, IsActive: true}, programmes: []string{"Leisure"}}, Sessions: scs.New()}
	handler := auth.Load(auth.RequireProgramme("Leisure")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })))
	if response := authenticatedRequest(t, auth.Sessions, id.String(), handler); response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestAuthRejectsInvalidAndInactiveAccounts(t *testing.T) {
	for _, lookup := range []currentUserLookup{{err: pgx.ErrNoRows}, {account: dbgen.GetActiveAccountByIDRow{IsActive: false}}} {
		auth := Auth{Users: lookup, Sessions: scs.New()}
		handler := auth.Load(auth.RequireAuthenticated(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("protected handler called") })))
		if response := authenticatedRequest(t, auth.Sessions, uuid.NewString(), handler); response.Code != http.StatusSeeOther {
			t.Fatalf("status = %d", response.Code)
		}
	}
}

func TestAnonymousOnlyRedirectsSignedInUsersAndAllowsGuests(t *testing.T) {
	auth := Auth{}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	for _, tc := range []struct {
		name string
		user *CurrentUser
		want int
	}{
		{"guest", nil, http.StatusNoContent},
		{"signed in", &CurrentUser{ID: uuid.New()}, http.StatusSeeOther},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/login", nil)
			if tc.user != nil {
				r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, *tc.user))
			}
			w := httptest.NewRecorder()
			auth.AnonymousOnly(next).ServeHTTP(w, r)
			if w.Code != tc.want || (tc.user != nil && w.Header().Get("Location") != "/dashboard") {
				t.Fatalf("response=%d location=%q", w.Code, w.Header().Get("Location"))
			}
		})
	}
}

func TestRequireGuardianRejectsDependentAndAllowsGuardian(t *testing.T) {
	auth := Auth{}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	for _, tc := range []struct {
		name string
		user CurrentUser
		want int
	}{
		{"dependent", CurrentUser{ID: uuid.New(), IsDependent: true}, http.StatusForbidden},
		{"guardian", CurrentUser{ID: uuid.New()}, http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/dashboard/guardian", nil)
			r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, tc.user))
			w := httptest.NewRecorder()
			auth.RequireGuardian(next).ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("response=%d want=%d", w.Code, tc.want)
			}
		})
	}
}

func TestAuthRejectsSessionsFromOlderCredentialVersions(t *testing.T) {
	id := uuid.New()
	sessions := scs.New()
	auth := Auth{Users: currentUserLookup{account: dbgen.GetActiveAccountByIDRow{ID: id, IsActive: true, CredentialVersion: 2}}, Sessions: sessions}
	handler := auth.Load(auth.RequireAuthenticated(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("revoked session reached protected handler")
	})))

	for _, version := range []int64{0, 1} {
		response := authenticatedRequestVersion(t, sessions, id.String(), version, handler)
		if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/login?next=%2Fprotected" {
			t.Fatalf("version %d response = %d %q", version, response.Code, response.Header().Get("Location"))
		}
	}
}

func TestAuthAllowsSessionFromCurrentCredentialVersion(t *testing.T) {
	id := uuid.New()
	sessions := scs.New()
	auth := Auth{Users: currentUserLookup{account: dbgen.GetActiveAccountByIDRow{ID: id, IsActive: true, CredentialVersion: 2}}, Sessions: sessions}
	handler := auth.Load(auth.RequireAuthenticated(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})))
	if response := authenticatedRequestVersion(t, sessions, id.String(), 2, handler); response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestAuthFeatureAvailabilityModes(t *testing.T) {
	for _, tc := range []struct {
		name, mode    string
		administrator bool
		want          int
	}{
		{"disabled member", "DISABLED", false, http.StatusNotFound},
		{"disabled administrator", "DISABLED", true, http.StatusNotFound},
		{"administrator only member", "ADMIN_ONLY", false, http.StatusNotFound},
		{"administrator only administrator", "ADMIN_ONLY", true, http.StatusNoContent},
		{"enabled member", "ENABLED", false, http.StatusNoContent},
		{"enabled administrator", "ENABLED", true, http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := uuid.New()
			auth := Auth{
				Users:    currentUserLookup{account: dbgen.GetActiveAccountByIDRow{ID: id, IsActive: true, IsAdmin: tc.administrator}},
				Features: featureFlagLookup{rows: []dbgen.ListFeatureFlagsRow{{FeatureKey: string(featureflags.Suggestions), Mode: tc.mode}}},
				Sessions: scs.New(),
			}
			handler := auth.Load(auth.RequireFeature(featureflags.Suggestions, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			})))
			if response := authenticatedRequest(t, auth.Sessions, id.String(), handler); response.Code != tc.want {
				t.Fatalf("status = %d, want %d", response.Code, tc.want)
			}
		})
	}
}

func TestAuthFeatureReadFailureDoesNotBroadenAccess(t *testing.T) {
	id := uuid.New()
	auth := Auth{
		Users:    currentUserLookup{account: dbgen.GetActiveAccountByIDRow{ID: id, IsActive: true, IsAdmin: true}},
		Features: featureFlagLookup{err: errors.New("flags unavailable")},
		Sessions: scs.New(),
	}
	handler := auth.Load(auth.RequireFeature(featureflags.Suggestions, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("feature handler called after flag read failure")
	})))
	if response := authenticatedRequest(t, auth.Sessions, id.String(), handler); response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestAuthAllowsDependentButBarsGuardianAndStaffControls(t *testing.T) {
	id := uuid.New()
	auth := Auth{Users: currentUserLookup{account: dbgen.GetActiveAccountByIDRow{ID: id, IsActive: true, IsDependent: true, IsAdmin: true}, grants: []dbgen.ListActiveStaffGrantsForUserRow{{Capability: "COACH"}}}, Sessions: scs.New()}
	for _, guard := range []func(http.Handler) http.Handler{auth.RequireGuardian, auth.RequireAdmin, auth.RequireCoach, auth.RequireModerator} {
		response := authenticatedRequest(t, auth.Sessions, id.String(), auth.Load(guard(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("minor control handler called") }))))
		if response.Code != http.StatusForbidden {
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

func TestAuthAllowsCoachEventStaffButNotAdmin(t *testing.T) {
	id := uuid.New()
	auth := Auth{Users: currentUserLookup{account: dbgen.GetActiveAccountByIDRow{ID: id, IsActive: true}, grants: []dbgen.ListActiveStaffGrantsForUserRow{{Capability: "COACH", ProgrammeID: ptr(uuid.New())}}}, Sessions: scs.New()}
	if response := authenticatedRequest(t, auth.Sessions, id.String(), auth.Load(auth.RequireEventStaff(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })))); response.Code != http.StatusNoContent {
		t.Fatalf("event staff status = %d", response.Code)
	}
	if response := authenticatedRequest(t, auth.Sessions, id.String(), auth.Load(auth.RequireAdmin(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("admin handler called") })))); response.Code != http.StatusForbidden {
		t.Fatalf("admin status = %d", response.Code)
	}
}

func TestAuthRequiresActiveDelegatedGrantForStaffWorkspaces(t *testing.T) {
	id := uuid.New()
	for _, tc := range []struct {
		name   string
		grants []dbgen.ListActiveStaffGrantsForUserRow
		status int
	}{
		{"coach", []dbgen.ListActiveStaffGrantsForUserRow{{Capability: "COACH", ProgrammeID: ptr(uuid.New())}}, http.StatusNoContent},
		{"moderator", []dbgen.ListActiveStaffGrantsForUserRow{{Capability: "MODERATOR"}}, http.StatusNoContent},
		{"no grant", nil, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth := Auth{Users: currentUserLookup{account: dbgen.GetActiveAccountByIDRow{ID: id, IsActive: true}, grants: tc.grants}, Sessions: scs.New()}
			var guard func(http.Handler) http.Handler
			switch tc.name {
			case "coach", "no grant":
				guard = auth.RequireCoach
			default:
				guard = auth.RequireModerator
			}
			response := authenticatedRequest(t, auth.Sessions, id.String(), auth.Load(guard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))))
			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d", response.Code, tc.status)
			}
		})
	}
}

func TestAuthLimitsContentWorkspacesToAdministratorsAndModerators(t *testing.T) {
	id := uuid.New()
	for _, tc := range []struct {
		name   string
		admin  bool
		grants []dbgen.ListActiveStaffGrantsForUserRow
		status int
	}{
		{name: "administrator", admin: true, status: http.StatusNoContent},
		{name: "moderator", grants: []dbgen.ListActiveStaffGrantsForUserRow{{Capability: "MODERATOR"}}, status: http.StatusNoContent},
		{name: "coach", grants: []dbgen.ListActiveStaffGrantsForUserRow{{Capability: "COACH", ProgrammeID: ptr(uuid.New())}}, status: http.StatusForbidden},
		{name: "member", status: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth := Auth{Users: currentUserLookup{account: dbgen.GetActiveAccountByIDRow{ID: id, IsActive: true, IsAdmin: tc.admin}, grants: tc.grants}, Sessions: scs.New()}
			for name, guard := range map[string]func(http.Handler) http.Handler{"content": auth.RequireContentStaff, "suggestions": auth.RequireSuggestionStaff} {
				handler := auth.Load(guard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })))
				if response := authenticatedRequest(t, auth.Sessions, id.String(), handler); response.Code != tc.status {
					t.Fatalf("%s status = %d, want %d", name, response.Code, tc.status)
				}
			}
		})
	}
}

func ptr(id uuid.UUID) *uuid.UUID { return &id }

func TestAuthDashboardSelection(t *testing.T) {
	id := uuid.New()
	for _, tc := range []struct {
		programmes []string
		admin      bool
	}{
		{[]string{"Leisure"}, false},
		{[]string{"Competition"}, true},
		{nil, true},
		{nil, false},
	} {
		auth := Auth{Users: currentUserLookup{account: dbgen.GetActiveAccountByIDRow{ID: id, IsActive: true, IsAdmin: tc.admin}, programmes: tc.programmes}, Sessions: scs.New()}
		response := authenticatedRequest(t, auth.Sessions, id.String(), auth.Load(http.HandlerFunc(auth.Dashboard)))
		if response.Header().Get("Location") != "/today" {
			t.Fatalf("location = %q, want /today", response.Header().Get("Location"))
		}
	}
}

func TestAuthLogoutDestroysSessionAndRedirectsToLogin(t *testing.T) {
	auth := Auth{Sessions: scs.New()}
	r := httptest.NewRequest(http.MethodPost, "/logout", nil)
	w := httptest.NewRecorder()
	auth.Sessions.LoadAndSave(http.HandlerFunc(auth.Logout)).ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login" {
		t.Fatalf("response=%d location=%q", w.Code, w.Header().Get("Location"))
	}
}

func TestAuthReturnsInternalErrorForLookupFailure(t *testing.T) {
	auth := Auth{Users: currentUserLookup{err: errors.New("database unavailable")}, Sessions: scs.New()}
	if response := authenticatedRequest(t, auth.Sessions, uuid.NewString(), auth.Load(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))); response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestAuthFallsBackWhenProfileSchemaIsUnavailable(t *testing.T) {
	id := uuid.New()
	auth := Auth{Users: currentUserLookup{
		account:         dbgen.GetActiveAccountByIDRow{ID: id, Name: "Athlete", IsActive: true},
		err:             &pgconn.PgError{Code: "42P01"},
		profileFallback: true,
	}, Sessions: scs.New()}
	handler := auth.Load(auth.RequireAuthenticated(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := currentUser(r.Context())
		if !ok || user.ID != id || !user.ProfileComplete {
			t.Fatalf("current user = %#v, ok = %v", user, ok)
		}
		w.WriteHeader(http.StatusNoContent)
	})))
	if response := authenticatedRequest(t, auth.Sessions, id.String(), handler); response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
}

func authenticatedRequest(t *testing.T, sessions *scs.SessionManager, userID string, handler http.Handler) *httptest.ResponseRecorder {
	return authenticatedRequestVersion(t, sessions, userID, 1, handler)
}

func authenticatedRequestVersion(t *testing.T, sessions *scs.SessionManager, userID string, version int64, handler http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	setSession := sessions.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sessions.Put(r.Context(), "user_id", userID)
		if version > 0 {
			sessions.Put(r.Context(), "credential_version", version)
		}
	}))
	setupResponse := httptest.NewRecorder()
	setSession.ServeHTTP(setupResponse, httptest.NewRequest(http.MethodGet, "/", nil))
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.AddCookie(setupResponse.Result().Cookies()[0])
	sessions.LoadAndSave(handler).ServeHTTP(response, request)
	return response
}
