package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/google/uuid"
)

func guardianAuthorityCoverageDashboard(store *guardianAuthorityStoreFake, dependents *guardianDependentStoreFake, now time.Time) Dashboard {
	dashboard := guardianDashboard(&guardianDashboardStore{}, dependents)
	dashboard.GuardianAuthority = store
	dashboard.GuardianInvitationKey = []byte("0123456789abcdef0123456789abcdef")
	dashboard.Now = func() time.Time { return now }
	return dashboard
}

func guardianAuthorityCoverageRequest(method, target string, form url.Values, actor, ref uuid.UUID) *http.Request {
	body := ""
	if form != nil {
		body = form.Encode()
	}
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	if form != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if ref != uuid.Nil {
		request.SetPathValue("ref", ref.String())
	}
	return request.WithContext(context.WithValue(request.Context(), currentUserKey{}, CurrentUser{ID: actor, Name: "Admin", IsAdmin: true, EmailVerified: true}))
}

func staleGuardianAuthorityContext(t *testing.T, dashboard *Dashboard, actor uuid.UUID, now time.Time) context.Context {
	t.Helper()
	sessions := scs.New()
	ctx, err := sessions.Load(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	sessions.Put(ctx, "authenticated_at", now.Add(-2*time.Hour).Format(time.RFC3339Nano))
	dashboard.Sessions = sessions
	return context.WithValue(ctx, currentUserKey{}, CurrentUser{ID: actor, Name: "Admin", IsAdmin: true, EmailVerified: true})
}

func TestGuardianAuthorityInvitationPageRendersEveryLifecycleState(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	consumed, revoked := now.Add(-time.Hour), now.Add(-2*time.Hour)
	store := &guardianAuthorityStoreFake{invitations: []GuardianAuthorityInvitation{
		{Reference: uuid.New(), Email: "active@example.test", ExpiresAt: now.Add(time.Hour)},
		{Reference: uuid.New(), Email: "used@example.test", ExpiresAt: now.Add(time.Hour), ConsumedAt: &consumed},
		{Reference: uuid.New(), Email: "revoked@example.test", ExpiresAt: now.Add(time.Hour), RevokedAt: &revoked},
		{Reference: uuid.New(), Email: "", ExpiresAt: now.Add(-time.Hour)},
	}}
	dashboard := guardianAuthorityCoverageDashboard(store, &guardianDependentStoreFake{}, now)
	response := httptest.NewRecorder()
	dashboard.GuardianAuthorityInvitations(response, guardianAuthorityCoverageRequest(http.MethodGet, "/admin/representacoes/convites", nil, uuid.New(), uuid.Nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	for _, want := range []string{"Ativo", "Utilizado", "Revogado", "Expirado", "Email removido"} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestGuardianAuthorityInvitationInputAndStorageFailuresStayClosed(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	actor, ref := uuid.New(), uuid.New()

	t.Run("issue malformed form", func(t *testing.T) {
		dashboard := guardianAuthorityCoverageDashboard(&guardianAuthorityStoreFake{}, &guardianDependentStoreFake{}, now)
		request := guardianAuthorityCoverageRequest(http.MethodPost, "/admin/representacoes/convites", nil, actor, uuid.Nil)
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Body = http.NoBody
		request.URL.RawQuery = "%"
		response := httptest.NewRecorder()
		dashboard.IssueGuardianAuthorityInvitation(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("status=%d", response.Code)
		}
	})

	t.Run("issue invalid email", func(t *testing.T) {
		dashboard := guardianAuthorityCoverageDashboard(&guardianAuthorityStoreFake{}, &guardianDependentStoreFake{}, now)
		response := httptest.NewRecorder()
		dashboard.IssueGuardianAuthorityInvitation(response, guardianAuthorityCoverageRequest(http.MethodPost, "/admin/representacoes/convites", url.Values{"email": {"invalid"}}, actor, uuid.Nil))
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status=%d", response.Code)
		}
	})

	t.Run("issue missing key", func(t *testing.T) {
		dashboard := guardianAuthorityCoverageDashboard(&guardianAuthorityStoreFake{}, &guardianDependentStoreFake{}, now)
		dashboard.GuardianInvitationKey = nil
		response := httptest.NewRecorder()
		dashboard.IssueGuardianAuthorityInvitation(response, guardianAuthorityCoverageRequest(http.MethodPost, "/admin/representacoes/convites", url.Values{"email": {"member@example.test"}}, actor, uuid.Nil))
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d", response.Code)
		}
	})

	t.Run("issue database failure", func(t *testing.T) {
		dashboard := guardianAuthorityCoverageDashboard(&guardianAuthorityStoreFake{err: errors.New("database unavailable")}, &guardianDependentStoreFake{}, now)
		response := httptest.NewRecorder()
		dashboard.IssueGuardianAuthorityInvitation(response, guardianAuthorityCoverageRequest(http.MethodPost, "/admin/representacoes/convites", url.Values{"email": {"member@example.test"}}, actor, uuid.Nil))
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d", response.Code)
		}
	})

	t.Run("revoke malformed and unknown reference", func(t *testing.T) {
		dashboard := guardianAuthorityCoverageDashboard(&guardianAuthorityStoreFake{}, &guardianDependentStoreFake{}, now)
		malformed := guardianAuthorityCoverageRequest(http.MethodPost, "/admin/representacoes/convites/x/revogar", nil, actor, uuid.Nil)
		malformed.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		malformed.URL.RawQuery = "%"
		response := httptest.NewRecorder()
		dashboard.RevokeGuardianAuthorityInvitation(response, malformed)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("malformed status=%d", response.Code)
		}
		response = httptest.NewRecorder()
		badReference := guardianAuthorityCoverageRequest(http.MethodPost, "/admin/representacoes/convites/x/revogar", url.Values{}, actor, uuid.Nil)
		badReference.SetPathValue("ref", "bad")
		dashboard.RevokeGuardianAuthorityInvitation(response, badReference)
		if response.Code != http.StatusNotFound {
			t.Fatalf("bad reference status=%d", response.Code)
		}
	})

	t.Run("revoke database failure", func(t *testing.T) {
		dashboard := guardianAuthorityCoverageDashboard(&guardianAuthorityStoreFake{err: errors.New("database unavailable")}, &guardianDependentStoreFake{}, now)
		response := httptest.NewRecorder()
		dashboard.RevokeGuardianAuthorityInvitation(response, guardianAuthorityCoverageRequest(http.MethodPost, "/admin/representacoes/convites/"+ref.String()+"/revogar", url.Values{}, actor, ref))
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d", response.Code)
		}
	})

	t.Run("list database failure", func(t *testing.T) {
		dashboard := guardianAuthorityCoverageDashboard(&guardianAuthorityStoreFake{err: errors.New("database unavailable")}, &guardianDependentStoreFake{}, now)
		response := httptest.NewRecorder()
		dashboard.GuardianAuthorityInvitations(response, guardianAuthorityCoverageRequest(http.MethodGet, "/admin/representacoes/convites", nil, actor, uuid.Nil))
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d", response.Code)
		}
	})
}

func TestGuardianAuthorityInvitationAuthenticationLimiterCoversIssueAndRevoke(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	actor, ref := uuid.New(), uuid.New()
	for _, action := range []string{"issue", "revoke"} {
		for _, tc := range []struct {
			name       string
			reauthErr  error
			reserveErr error
			want       int
		}{
			{name: "limited", reauthErr: ErrGuardianAuthentication, reserveErr: ErrGuardianApplicationLimited, want: http.StatusTooManyRequests},
			{name: "limiter unavailable", reauthErr: ErrGuardianAuthentication, reserveErr: errors.New("database unavailable"), want: http.StatusInternalServerError},
			{name: "authentication unavailable", reauthErr: errors.New("database unavailable"), want: http.StatusInternalServerError},
		} {
			t.Run(action+" "+tc.name, func(t *testing.T) {
				store := &guardianAuthorityStoreFake{invitations: []GuardianAuthorityInvitation{{Reference: ref, Email: "member@example.test", ExpiresAt: now.Add(time.Hour)}}}
				dependents := &guardianDependentStoreFake{reauthErr: tc.reauthErr, reserveErr: map[string]error{"AUTH_INVALID": tc.reserveErr}}
				dashboard := guardianAuthorityCoverageDashboard(store, dependents, now)
				ctx := staleGuardianAuthorityContext(t, &dashboard, actor, now)
				var request *http.Request
				if action == "issue" {
					request = guardianAuthorityCoverageRequest(http.MethodPost, "/admin/representacoes/convites", url.Values{"email": {"member@example.test"}, "password": {"wrong"}}, actor, uuid.Nil)
				} else {
					request = guardianAuthorityCoverageRequest(http.MethodPost, "/admin/representacoes/convites/"+ref.String()+"/revogar", url.Values{"password": {"wrong"}}, actor, ref)
				}
				response := httptest.NewRecorder()
				if action == "issue" {
					dashboard.IssueGuardianAuthorityInvitation(response, request.WithContext(ctx))
				} else {
					dashboard.RevokeGuardianAuthorityInvitation(response, request.WithContext(ctx))
				}
				if response.Code != tc.want {
					t.Fatalf("status=%d want=%d", response.Code, tc.want)
				}
			})
		}
	}
}

func TestGuardianAuthorityRenewalRejectsInvalidAndUnauthorizedMutations(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	actor, ref := uuid.New(), uuid.New()
	for _, tc := range []struct {
		name       string
		store      *guardianAuthorityStoreFake
		form       url.Values
		pathRef    string
		wantStatus int
	}{
		{name: "invalid form", store: &guardianAuthorityStoreFake{policyAvailable: true}, form: url.Values{"version": {"bad"}, "response_code": {"FORGED"}}, pathRef: ref.String(), wantStatus: http.StatusUnprocessableEntity},
		{name: "invalid state", store: &guardianAuthorityStoreFake{policyAvailable: true, renewalErr: ErrGuardianAuthorityInvalid}, form: url.Values{"version": {"1"}, "response_code": {"NADA_MUDOU"}}, pathRef: ref.String(), wantStatus: http.StatusUnprocessableEntity},
		{name: "forbidden", store: &guardianAuthorityStoreFake{policyAvailable: true, renewalErr: ErrGuardianAuthorityForbidden}, form: url.Values{"version": {"1"}, "response_code": {"NADA_MUDOU"}}, pathRef: ref.String(), wantStatus: http.StatusForbidden},
		{name: "database failure", store: &guardianAuthorityStoreFake{policyAvailable: true, renewalErr: errors.New("database unavailable")}, form: url.Values{"version": {"1"}, "response_code": {"NADA_MUDOU"}}, pathRef: ref.String(), wantStatus: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dashboard := guardianAuthorityCoverageDashboard(tc.store, &guardianDependentStoreFake{}, now)
			request := guardianAuthorityCoverageRequest(http.MethodPost, "/dashboard/guardian/representacoes/"+tc.pathRef+"/renovar", tc.form, actor, uuid.Nil)
			request.SetPathValue("ref", tc.pathRef)
			response := httptest.NewRecorder()
			dashboard.SubmitGuardianAuthorityRenewal(response, request)
			if response.Code != tc.wantStatus {
				t.Fatalf("status=%d want=%d body=%q", response.Code, tc.wantStatus, response.Body.String())
			}
		})
	}

	dashboard := guardianAuthorityCoverageDashboard(&guardianAuthorityStoreFake{policyAvailable: true}, &guardianDependentStoreFake{}, now)
	sessions := scs.New()
	ctx, err := sessions.Load(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	dashboard.Sessions = sessions
	request := guardianAuthorityCoverageRequest(http.MethodPost, "/dashboard/guardian/representacoes/"+ref.String()+"/renovar", url.Values{"version": {"1"}, "response_code": {"NADA_MUDOU"}}, actor, uuid.Nil)
	request.SetPathValue("ref", ref.String())
	response := httptest.NewRecorder()
	dashboard.SubmitGuardianAuthorityRenewal(response, request.WithContext(context.WithValue(ctx, currentUserKey{}, CurrentUser{ID: actor, Name: "Guardian", EmailVerified: true})))
	if response.Code != http.StatusSeeOther || sessions.GetString(ctx, "guardian_flash") == "" {
		t.Fatalf("status=%d flash=%q", response.Code, sessions.GetString(ctx, "guardian_flash"))
	}
}
