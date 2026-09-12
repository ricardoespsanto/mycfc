package handlers

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestGuardianDashboardRendersDependentsAndDeduplicatedCalendars(t *testing.T) {
	guardianID := uuid.New()
	store := &guardianDashboardStore{dependents: []dbgen.ListDependentsByGuardianRow{
		{Name: "Ana", DateOfBirth: pgtype.Date{Time: time.Date(2014, 7, 25, 0, 0, 0, 0, time.UTC), Valid: true}},
		{Name: "Bruno", DateOfBirth: pgtype.Date{Time: time.Date(2010, 7, 24, 0, 0, 0, 0, time.UTC), Valid: true}},
	}}
	dashboard := guardianDashboard(store, &guardianDependentStoreFake{})
	response := guardianResponse(t, dashboard.Guardian, guardianID, nil)
	body := response.Body.String()
	for _, want := range []string{"Ana", "11 anos", "Bruno", "16 anos", `href="https://example.test/responsabilidade"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q", want)
		}
	}
}

func TestAddDependentValidatesBeforeStore(t *testing.T) {
	store := &guardianDependentStoreFake{}
	dashboard := guardianDashboard(&guardianDashboardStore{}, store)
	form := validDependentForm()
	form.Set("date_of_birth", "2000-01-01")
	form.Del("accept_minor_responsibility")
	response := guardianResponse(t, dashboard.AddDependent, uuid.New(), form)
	if response.Code != http.StatusUnprocessableEntity || store.called {
		t.Fatalf("response = %d, called = %t", response.Code, store.called)
	}
	if len(store.reserveKinds) != 1 || store.reserveKinds[0] != "SUBMISSION" {
		t.Fatalf("rejected submission was not counted: %v", store.reserveKinds)
	}
	for _, want := range []string{"tem de ter menos de 18 anos", "Tem de aceitar a responsabilidade"} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("body does not contain %q", want)
		}
	}
}

func TestAddDependentPreservesAcceptedResponsibilityOnValidationError(t *testing.T) {
	store := &guardianDependentStoreFake{}
	dashboard := guardianDashboard(&guardianDashboardStore{}, store)
	form := validDependentForm()
	form.Set("name", "X")
	response := guardianResponse(t, dashboard.AddDependent, uuid.New(), form)
	if response.Code != http.StatusUnprocessableEntity || store.called {
		t.Fatalf("response = %d, called = %t", response.Code, store.called)
	}
	if !strings.Contains(response.Body.String(), `id="accept_minor_responsibility" name="accept_minor_responsibility" type="checkbox" required checked`) {
		t.Fatalf("accepted responsibility was not preserved: %q", response.Body.String())
	}
}

func TestAddDependentCreatesFromCurrentGuardianAndRedirects(t *testing.T) {
	guardianID := uuid.New()
	store := &guardianDependentStoreFake{}
	dashboard := guardianDashboard(&guardianDashboardStore{}, store)
	response := guardianResponse(t, dashboard.AddDependent, guardianID, validDependentForm())
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/dashboard/guardian" {
		t.Fatalf("response = %d %q", response.Code, response.Header().Get("Location"))
	}
	if !store.called || store.input.GuardianID != guardianID || store.input.Name != "Maria Silva" {
		t.Fatalf("input = %+v", store.input)
	}
	if store.input.ResponsibilityVersion != "1.0" || store.input.ResponsibilitySHA256 != strings.Repeat("c", 64) {
		t.Fatalf("consent input = %+v", store.input)
	}
}

func TestAddDependentMapsEligibilityAndInvitationFailuresWithoutLeakingState(t *testing.T) {
	for _, tc := range []struct {
		name       string
		createErr  error
		reserveErr error
		wantStatus int
		wantRetry  string
	}{
		{name: "ineligible applicant", createErr: ErrGuardianApplicantIneligible, wantStatus: http.StatusUnprocessableEntity},
		{name: "invalid invitation", createErr: ErrGuardianInvitationInvalid, wantStatus: http.StatusUnprocessableEntity},
		{name: "invalid invitation limited", createErr: ErrGuardianInvitationInvalid, reserveErr: ErrGuardianApplicationLimited, wantStatus: http.StatusTooManyRequests, wantRetry: "900"},
		{name: "invalid invitation limiter failure", createErr: ErrGuardianInvitationInvalid, reserveErr: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &guardianDependentStoreFake{err: tc.createErr, reserveErr: map[string]error{"INVITATION_INVALID": tc.reserveErr}}
			response := guardianResponse(t, guardianDashboard(&guardianDashboardStore{}, store).AddDependent, uuid.New(), validDependentForm())
			if response.Code != tc.wantStatus || response.Header().Get("Retry-After") != tc.wantRetry {
				t.Fatalf("status=%d retry=%q body=%q", response.Code, response.Header().Get("Retry-After"), response.Body.String())
			}
			if tc.wantStatus == http.StatusUnprocessableEntity && !strings.Contains(response.Body.String(), "Não foi possível enviar o pedido") {
				t.Fatalf("generic rejection missing: %q", response.Body.String())
			}
		})
	}
}

func TestAddDependentFailsClosedWhenRateOrAuthenticationStorageIsUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name       string
		reserveErr error
		wantStatus int
		retry      string
	}{
		{name: "submission limited", reserveErr: ErrGuardianApplicationLimited, wantStatus: http.StatusTooManyRequests, retry: "86400"},
		{name: "submission database failure", reserveErr: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &guardianDependentStoreFake{reserveErr: map[string]error{"SUBMISSION": tc.reserveErr}}
			response := guardianResponse(t, guardianDashboard(&guardianDashboardStore{}, store).AddDependent, uuid.New(), validDependentForm())
			if response.Code != tc.wantStatus || response.Header().Get("Retry-After") != tc.retry || store.called {
				t.Fatalf("status=%d retry=%q called=%v", response.Code, response.Header().Get("Retry-After"), store.called)
			}
		})
	}

	now := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name       string
		reauthErr  error
		limiterErr error
	}{
		{name: "reauthentication database failure", reauthErr: errors.New("database unavailable")},
		{name: "authentication limiter database failure", reauthErr: ErrGuardianAuthentication, limiterErr: errors.New("database unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &guardianDependentStoreFake{reauthErr: tc.reauthErr, reserveErr: map[string]error{"AUTH_INVALID": tc.limiterErr}}
			form := validDependentForm()
			form.Set("password", "current password")
			response, _, _, _ := guardianAuthenticatedResponse(t, guardianDashboard(&guardianDashboardStore{}, store), uuid.New(), form, now.Add(-2*time.Hour), now)
			if response.Code != http.StatusInternalServerError || store.called {
				t.Fatalf("status=%d called=%v", response.Code, store.called)
			}
		})
	}
}

func TestGuardianAuthenticationFreshUsesExplicitOneHourTimestamp(t *testing.T) {
	now := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	sessions := scs.New()
	ctx, err := sessions.Load(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	dashboard := Dashboard{Sessions: sessions, Now: func() time.Time { return now }}
	sessions.Put(ctx, "last_seen_at", now.Format(time.RFC3339Nano))
	if dashboard.authenticationFresh(ctx) {
		t.Fatal("last_seen_at was accepted as authentication evidence")
	}
	sessions.Put(ctx, "authenticated_at", now.Add(-time.Hour).Format(time.RFC3339Nano))
	if !dashboard.authenticationFresh(ctx) {
		t.Fatal("one-hour authentication was not accepted")
	}
	sessions.Put(ctx, "authenticated_at", now.Add(-time.Hour-time.Nanosecond).Format(time.RFC3339Nano))
	if dashboard.authenticationFresh(ctx) {
		t.Fatal("stale authentication was accepted")
	}
}

func TestGuardianApplicationNetworkBucketsUseExactIPv4AndIPv6Slash64(t *testing.T) {
	ipv4A := netip.MustParseAddr("192.0.2.10")
	ipv4B := netip.MustParseAddr("192.0.2.11")
	ipv6A := netip.MustParseAddr("2001:db8:abcd:12::1")
	ipv6B := netip.MustParseAddr("2001:db8:abcd:12:ffff::2")
	ipv6Other := netip.MustParseAddr("2001:db8:abcd:13::1")
	if got := guardianApplicationNetworkBucket(&ipv4A); got != "192.0.2.10" || got == guardianApplicationNetworkBucket(&ipv4B) {
		t.Fatalf("IPv4 buckets are not exact: %q %q", got, guardianApplicationNetworkBucket(&ipv4B))
	}
	if got := guardianApplicationNetworkBucket(&ipv6A); got != "2001:db8:abcd:12::/64" || got != guardianApplicationNetworkBucket(&ipv6B) || got == guardianApplicationNetworkBucket(&ipv6Other) {
		t.Fatalf("IPv6 /64 buckets are incorrect: %q %q %q", got, guardianApplicationNetworkBucket(&ipv6B), guardianApplicationNetworkBucket(&ipv6Other))
	}
	if got := guardianApplicationNetworkBucket(nil); got != "unavailable" {
		t.Fatalf("missing network bucket=%q", got)
	}
}

func TestAddDependentStaleAuthenticationConfirmationFlow(t *testing.T) {
	now := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	guardianID := uuid.New()

	t.Run("prompts and counts missing confirmation", func(t *testing.T) {
		store := &guardianDependentStoreFake{}
		response, _, _, _ := guardianAuthenticatedResponse(t, guardianDashboard(&guardianDashboardStore{}, store), guardianID, validDependentForm(), now.Add(-time.Hour-time.Second), now)
		if response.Code != http.StatusUnprocessableEntity || store.called || !strings.Contains(response.Body.String(), "Palavra-passe atual") {
			t.Fatalf("status=%d created=%t body=%q", response.Code, store.called, response.Body.String())
		}
		if !slices.Equal(store.reserveKinds, []string{"SUBMISSION", "AUTH_INVALID"}) {
			t.Fatalf("reserve kinds=%v", store.reserveKinds)
		}
	})

	t.Run("wrong password is counted", func(t *testing.T) {
		store := &guardianDependentStoreFake{reauthErr: ErrGuardianAuthentication}
		form := validDependentForm()
		form.Set("password", "wrong password")
		response, _, _, _ := guardianAuthenticatedResponse(t, guardianDashboard(&guardianDashboardStore{}, store), guardianID, form, now.Add(-2*time.Hour), now)
		if response.Code != http.StatusUnprocessableEntity || store.called || store.reauthPassword != "wrong password" {
			t.Fatalf("status=%d created=%t password=%q", response.Code, store.called, store.reauthPassword)
		}
		if !slices.Equal(store.reserveKinds, []string{"SUBMISSION", "AUTH_INVALID"}) {
			t.Fatalf("reserve kinds=%v", store.reserveKinds)
		}
	})

	t.Run("invalid authentication lockout lasts fifteen minutes", func(t *testing.T) {
		store := &guardianDependentStoreFake{reauthErr: ErrGuardianAuthentication, reserveErr: map[string]error{"AUTH_INVALID": ErrGuardianApplicationLimited}}
		form := validDependentForm()
		form.Set("password", "wrong password")
		response, _, _, _ := guardianAuthenticatedResponse(t, guardianDashboard(&guardianDashboardStore{}, store), guardianID, form, now.Add(-2*time.Hour), now)
		if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") != "900" || store.called {
			t.Fatalf("status=%d retry=%q created=%t", response.Code, response.Header().Get("Retry-After"), store.called)
		}
		if !strings.Contains(response.Body.String(), "Aguarde 15 minutos") {
			t.Fatalf("lockout guidance missing: %q", response.Body.String())
		}
	})

	t.Run("successful confirmation rotates and creates", func(t *testing.T) {
		store := &guardianDependentStoreFake{}
		form := validDependentForm()
		form.Set("password", "correct horse 7")
		response, sessions, sessionContext, oldToken := guardianAuthenticatedResponse(t, guardianDashboard(&guardianDashboardStore{}, store), guardianID, form, now.Add(-2*time.Hour), now)
		if response.Code != http.StatusSeeOther || !store.called || store.reauthPassword != "correct horse 7" {
			t.Fatalf("status=%d created=%t password=%q", response.Code, store.called, store.reauthPassword)
		}
		if nextToken := sessions.Token(sessionContext); nextToken == "" || nextToken == oldToken {
			t.Fatalf("session token was not rotated: before=%q after=%q", oldToken, nextToken)
		}
		if got := sessions.GetString(sessionContext, "authenticated_at"); got != now.Format(time.RFC3339Nano) {
			t.Fatalf("authenticated_at=%q", got)
		}
		if !slices.Equal(store.reserveKinds, []string{"SUBMISSION"}) {
			t.Fatalf("reserve kinds=%v", store.reserveKinds)
		}
	})
}

func guardianAuthenticatedResponse(t *testing.T, dashboard Dashboard, guardianID uuid.UUID, form url.Values, authenticatedAt, now time.Time) (*httptest.ResponseRecorder, *scs.SessionManager, context.Context, string) {
	t.Helper()
	sessions := scs.New()
	ctx, err := sessions.Load(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := sessions.RenewToken(ctx); err != nil {
		t.Fatal(err)
	}
	sessions.Put(ctx, "authenticated_at", authenticatedAt.Format(time.RFC3339Nano))
	oldToken := sessions.Token(ctx)
	dashboard.Sessions = sessions
	dashboard.Now = func() time.Time { return now }
	request := httptest.NewRequest(http.MethodPost, "/guardian/add-dependent", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx = context.WithValue(ctx, currentUserKey{}, CurrentUser{ID: guardianID, Name: "Guardião", EmailVerified: true})
	response := httptest.NewRecorder()
	dashboard.AddDependent(response, request.WithContext(ctx))
	return response, sessions, ctx, oldToken
}

func TestAdministratorInvitationUsesNormalizedEmailAndOneTimeRandomToken(t *testing.T) {
	actor := uuid.New()
	store := &guardianAuthorityStoreFake{}
	dashboard := guardianDashboard(&guardianDashboardStore{}, &guardianDependentStoreFake{})
	dashboard.GuardianAuthority = store
	dashboard.GuardianInvitationKey = []byte("0123456789abcdef0123456789abcdef")
	request := httptest.NewRequest(http.MethodPost, "/admin/representacoes/convites", strings.NewReader("email=INVITED%40EXAMPLE.TEST+"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request = request.WithContext(context.WithValue(request.Context(), currentUserKey{}, CurrentUser{ID: actor, IsAdmin: true}))
	response := httptest.NewRecorder()
	dashboard.IssueGuardianAuthorityInvitation(response, request)
	if response.Code != http.StatusCreated || store.invitationEmail != "invited@example.test" || len(store.invitationDigest) != 32 {
		t.Fatalf("status=%d email=%q digest=%x", response.Code, store.invitationEmail, store.invitationDigest)
	}
	body := response.Body.String()
	start, end := strings.Index(body, "<code>"), strings.Index(body, "</code>")
	if start < 0 || end <= start {
		t.Fatalf("invitation token missing: %q", body)
	}
	token := body[start+len("<code>") : end]
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		t.Fatalf("token is not 256-bit random data: %q err=%v", token, err)
	}
	if !bytes.Equal(store.invitationDigest, guardianInvitationDigest(dashboard.GuardianInvitationKey, token)) {
		t.Fatal("stored digest does not match issued token")
	}
}

func TestAdministratorInvitationMutationsRequireFreshRotatedAuthentication(t *testing.T) {
	now := time.Date(2026, 9, 12, 11, 0, 0, 0, time.UTC)
	actor, invitationRef := uuid.New(), uuid.New()
	store := &guardianAuthorityStoreFake{invitations: []GuardianAuthorityInvitation{{Reference: invitationRef, Email: "member@example.test", IssuedAt: now.Add(-time.Hour), ExpiresAt: now.Add(29 * 24 * time.Hour)}}}
	dependents := &guardianDependentStoreFake{reauthErr: ErrGuardianAuthentication}
	dashboard := guardianDashboard(&guardianDashboardStore{}, dependents)
	dashboard.GuardianAuthority = store
	dashboard.GuardianInvitationKey = []byte("0123456789abcdef0123456789abcdef")
	dashboard.Now = func() time.Time { return now }
	sessions := scs.New()
	ctx, err := sessions.Load(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := sessions.RenewToken(ctx); err != nil {
		t.Fatal(err)
	}
	sessions.Put(ctx, "authenticated_at", now.Add(-2*time.Hour).Format(time.RFC3339Nano))
	dashboard.Sessions = sessions
	ctx = context.WithValue(ctx, currentUserKey{}, CurrentUser{ID: actor, Name: "Admin", IsAdmin: true})

	request := httptest.NewRequest(http.MethodPost, "/admin/representacoes/convites", strings.NewReader("email=member%40example.test&password=wrong"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	dashboard.IssueGuardianAuthorityInvitation(response, request.WithContext(ctx))
	if response.Code != http.StatusUnprocessableEntity || store.invitationEmail != "" || !slices.Equal(dependents.reserveKinds, []string{"AUTH_INVALID"}) {
		t.Fatalf("wrong-password issue status=%d email=%q reserves=%v", response.Code, store.invitationEmail, dependents.reserveKinds)
	}

	dependents.reauthErr = nil
	oldToken := sessions.Token(ctx)
	request = httptest.NewRequest(http.MethodPost, "/admin/representacoes/convites", strings.NewReader("email=member%40example.test&password=correct+horse+7"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response = httptest.NewRecorder()
	dashboard.IssueGuardianAuthorityInvitation(response, request.WithContext(ctx))
	if response.Code != http.StatusCreated || store.invitationEmail != "member@example.test" || sessions.Token(ctx) == oldToken {
		t.Fatalf("confirmed issue status=%d email=%q token_rotated=%t", response.Code, store.invitationEmail, sessions.Token(ctx) != oldToken)
	}

	sessions.Put(ctx, "authenticated_at", now.Add(-2*time.Hour).Format(time.RFC3339Nano))
	oldToken = sessions.Token(ctx)
	request = httptest.NewRequest(http.MethodPost, "/admin/representacoes/convites/"+invitationRef.String()+"/revogar", strings.NewReader("password=correct+horse+7"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.SetPathValue("ref", invitationRef.String())
	response = httptest.NewRecorder()
	dashboard.RevokeGuardianAuthorityInvitation(response, request.WithContext(ctx))
	if response.Code != http.StatusSeeOther || store.revokedInvitation != invitationRef || sessions.Token(ctx) == oldToken {
		t.Fatalf("confirmed revoke status=%d ref=%s token_rotated=%t", response.Code, store.revokedInvitation, sessions.Token(ctx) != oldToken)
	}
}

func TestAddDependentReportsMaximumAndSupportsHTMX(t *testing.T) {
	dashboard := guardianDashboard(&guardianDashboardStore{}, &guardianDependentStoreFake{err: ErrMaximumDependents})
	response := guardianResponse(t, dashboard.AddDependent, uuid.New(), validDependentForm())
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), maximumDependentsMessage) {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}

	dashboard = guardianDashboard(&guardianDashboardStore{}, &guardianDependentStoreFake{})
	response = guardianResponse(t, dashboard.AddDependent, uuid.New(), validDependentForm())
	if response.Code != http.StatusSeeOther {
		t.Fatalf("normal response = %d", response.Code)
	}

	request := httptest.NewRequest(http.MethodPost, "/guardian/add-dependent", strings.NewReader(validDependentForm().Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("HX-Request", "true")
	ctx := context.WithValue(request.Context(), currentUserKey{}, CurrentUser{ID: uuid.New(), Name: "Guardião", EmailVerified: true})
	response = httptest.NewRecorder()
	guardianDashboard(&guardianDashboardStore{}, &guardianDependentStoreFake{}).AddDependent(response, request.WithContext(ctx))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `id="guardian-content"`) || !strings.Contains(response.Body.String(), "Pedido recebido.") {
		t.Fatalf("HTMX response = %d %q", response.Code, response.Body.String())
	}
}

func TestGuardianDashboardKeepsPendingRelationshipStatusOnly(t *testing.T) {
	guardianID, subjectID := uuid.New(), uuid.New()
	authority := &guardianAuthorityStoreFake{policyAvailable: true, relationships: []GuardianAuthorityRelationship{{Reference: uuid.New(), GuardianID: guardianID, SubjectID: subjectID, SubmittedLabel: "Pedido familiar", SubjectName: "Nome privado", State: "PENDING", DateOfBirth: time.Date(2012, 1, 2, 0, 0, 0, 0, time.UTC), MinorLoginIssued: true, ProfileComplete: true}}}
	dashboard := guardianDashboard(&guardianDashboardStore{}, &guardianDependentStoreFake{})
	dashboard.GuardianAuthority = authority
	response := guardianResponse(t, dashboard.Guardian, guardianID, nil)
	body := response.Body.String()
	for _, want := range []string{"Pedido familiar", "A aguardar verificação", "não permitem consultar dados"} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q", want)
		}
	}
	for _, forbidden := range []string{"Nome privado", subjectID.String(), "Perfil incompleto", "classificação", "Acesso individual"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("pending response exposes %q", forbidden)
		}
	}
	if response.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("cache control = %q", response.Header().Get("Cache-Control"))
	}
}

func TestGuardianDashboardClosesNewRequestsWithoutAdoptedPolicy(t *testing.T) {
	dashboard := guardianDashboard(&guardianDashboardStore{}, &guardianDependentStoreFake{})
	dashboard.GuardianAuthority = &guardianAuthorityStoreFake{}
	response := guardianResponse(t, dashboard.Guardian, uuid.New(), nil)
	body := response.Body.String()
	if !strings.Contains(body, "Novos pedidos temporariamente indisponíveis") || strings.Contains(body, "Pedir associação</a>") || strings.Contains(body, `action="/guardian/add-dependent"`) {
		t.Fatalf("closed policy response = %q", body)
	}
}

func TestAddDependentRejectsForgedPostWithoutAdoptedPolicy(t *testing.T) {
	dependents := &guardianDependentStoreFake{}
	dashboard := guardianDashboard(&guardianDashboardStore{}, dependents)
	dashboard.GuardianAuthority = &guardianAuthorityStoreFake{}
	response := guardianResponse(t, dashboard.AddDependent, uuid.New(), validDependentForm())
	if response.Code != http.StatusConflict || dependents.called || !strings.Contains(response.Body.String(), "política de verificação ainda não foi ativada") {
		t.Fatalf("response=%d called=%v body=%q", response.Code, dependents.called, response.Body.String())
	}
}

func TestGuardianHandlersFailClosedOnDependenciesAndStorage(t *testing.T) {
	guardianID := uuid.New()
	tests := []struct {
		name      string
		authority *guardianAuthorityStoreFake
		dependent *guardianDependentStoreFake
		want      int
		missing   bool
	}{
		{"missing authority", nil, &guardianDependentStoreFake{}, http.StatusInternalServerError, true},
		{"policy lookup", &guardianAuthorityStoreFake{err: errors.New("database unavailable")}, &guardianDependentStoreFake{}, http.StatusInternalServerError, false},
		{"policy changed", &guardianAuthorityStoreFake{policyAvailable: true}, &guardianDependentStoreFake{err: ErrGuardianAuthorityPolicyUnavailable}, http.StatusUnprocessableEntity, false},
		{"storage failure", &guardianAuthorityStoreFake{policyAvailable: true}, &guardianDependentStoreFake{err: errors.New("database unavailable")}, http.StatusInternalServerError, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dashboard := guardianDashboard(&guardianDashboardStore{}, tc.dependent)
			if tc.missing {
				dashboard.GuardianAuthority = nil
			} else {
				dashboard.GuardianAuthority = tc.authority
			}
			response := guardianResponse(t, dashboard.AddDependent, guardianID, validDependentForm())
			if response.Code != tc.want {
				t.Fatalf("status = %d, want %d", response.Code, tc.want)
			}
		})
	}

	dashboard := guardianDashboard(&guardianDashboardStore{}, &guardianDependentStoreFake{})
	dashboard.GuardianAuthority = &guardianAuthorityStoreFake{err: errors.New("database unavailable")}
	response := guardianResponse(t, dashboard.Guardian, guardianID, nil)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("guardian list failure status = %d", response.Code)
	}

	dashboard.GuardianAuthority = nil
	w := httptest.NewRecorder()
	dashboard.renderGuardianForm(w, guardianAuthorityGetRequest("/dashboard/guardian", "", guardianID), http.StatusOK, guardianDependentForm{})
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("render without authority status = %d", w.Code)
	}
}

func TestGuardianRenderingCoversVerifiedAndRequestStates(t *testing.T) {
	now := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	until := now.AddDate(0, 1, 0)
	dashboard := guardianDashboard(&guardianDashboardStore{}, &guardianDependentStoreFake{})
	dashboard.Now = func() time.Time { return now }
	dashboard.GuardianAuthority = &guardianAuthorityStoreFake{policyAvailable: true, relationships: []GuardianAuthorityRelationship{
		{Reference: uuid.New(), SubjectID: uuid.New(), SubjectName: "Menor verificado", State: "VERIFIED", DateOfBirth: time.Date(2012, 1, 1, 0, 0, 0, 0, time.UTC), VerifiedUntil: &until, ProfileComplete: false},
		{Reference: uuid.New(), SubmittedLabel: "Conflito", State: "SUSPENDED", Conflict: true},
		{Reference: uuid.New(), SubmittedLabel: "Suspenso", State: "SUSPENDED"},
		{Reference: uuid.New(), SubmittedLabel: "Expirado", State: "EXPIRED"},
		{Reference: uuid.New(), SubmittedLabel: "Rejeitado", State: "REJECTED"},
		{Reference: uuid.New(), SubmittedLabel: "Outro", State: "UNKNOWN"},
	}}
	response := guardianResponse(t, dashboard.Guardian, uuid.New(), nil)
	for _, want := range []string{"Menor verificado", "Representação verificada", "Conflito", "Suspenso", "Expirado", "Rejeitado", "Outro"} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestGuardianAuthorityDecisionValidatesMetadataAndConcurrency(t *testing.T) {
	actor, ref := uuid.New(), uuid.New()
	store := &guardianAuthorityStoreFake{policyAvailable: true, detail: GuardianAuthorityRelationship{Reference: ref, GuardianID: uuid.New(), SubjectName: "Minor", SubmittedLabel: "Guardian", State: "PENDING", Version: 2, DateOfBirth: time.Date(2012, 1, 2, 0, 0, 0, 0, time.UTC)}, evidenceTypes: []GuardianAuthorityEvidenceType{{Code: "COURT_ORDER", Label: "Decisão judicial"}}, reasonCodes: []GuardianAuthorityReasonCode{{Code: "EVIDENCE_CONFIRMED", Label: "Comprovativo confirmado"}}}
	dashboard := guardianDashboard(&guardianDashboardStore{}, &guardianDependentStoreFake{})
	dashboard.GuardianAuthority = store
	dashboard.Now = func() time.Time { return time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC) }

	invalid := guardianAuthorityRequest(ref, actor, url.Values{"version": {"2"}, "action": {"APPROVE"}, "reason_code": {"RELATIONSHIP_CONFIRMED"}, "attested": {"yes"}})
	w := httptest.NewRecorder()
	dashboard.GuardianAuthorityTransition(w, invalid)
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "categoria de verificação válida") {
		t.Fatalf("invalid response=%d %q", w.Code, w.Body.String())
	}

	form := url.Values{"version": {"2"}, "action": {"APPROVE"}, "evidence_category": {"COURT_OR_LEGAL_AUTHORITY"}, "reason_code": {"RELATIONSHIP_CONFIRMED"}, "attested": {"yes"}}
	w = httptest.NewRecorder()
	dashboard.GuardianAuthorityTransition(w, guardianAuthorityRequest(ref, actor, form))
	if w.Code != http.StatusSeeOther || store.transition.Reference != ref || store.transition.ActorID != actor || store.transition.ExpectedVersion != 2 || store.transition.EvidenceCategory != "COURT_OR_LEGAL_AUTHORITY" {
		t.Fatalf("transition response=%d input=%+v", w.Code, store.transition)
	}

	store.transitionErr = ErrGuardianAuthorityConflict
	w = httptest.NewRecorder()
	dashboard.GuardianAuthorityTransition(w, guardianAuthorityRequest(ref, actor, form))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "alterado por outra pessoa") {
		t.Fatalf("conflict response=%d %q", w.Code, w.Body.String())
	}
}

func TestGuardianAuthorityDecisionRequiresFreshRotatedAuthentication(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	actor, ref := uuid.New(), uuid.New()
	valid := url.Values{"version": {"2"}, "action": {"RENEW"}, "evidence_category": {"CLUB_REGISTRATION_RECORD"}, "reason_code": {"RELATIONSHIP_CONFIRMED"}, "attested": {"yes"}}
	request := func(t *testing.T, dashboard Dashboard, dependents *guardianDependentStoreFake, password string) (*httptest.ResponseRecorder, *scs.SessionManager, context.Context, string) {
		t.Helper()
		sessions := scs.New()
		ctx, err := sessions.Load(context.Background(), "")
		if err != nil {
			t.Fatal(err)
		}
		if err = sessions.RenewToken(ctx); err != nil {
			t.Fatal(err)
		}
		sessions.Put(ctx, "authenticated_at", now.Add(-2*time.Hour).Format(time.RFC3339Nano))
		oldToken := sessions.Token(ctx)
		dashboard.Sessions, dashboard.Now, dashboard.Dependents = sessions, func() time.Time { return now }, dependents
		form := url.Values{}
		for key, values := range valid {
			form[key] = append([]string(nil), values...)
		}
		if password != "" {
			form.Set("password", password)
		}
		r := httptest.NewRequest(http.MethodPost, "/admin/representacoes/"+ref.String(), strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.SetPathValue("ref", ref.String())
		ctx = context.WithValue(ctx, currentUserKey{}, CurrentUser{ID: actor, Name: "Admin", IsAdmin: true})
		w := httptest.NewRecorder()
		dashboard.GuardianAuthorityTransition(w, r.WithContext(ctx))
		return w, sessions, ctx, oldToken
	}

	for _, tc := range []struct {
		name, password string
		dependents     *guardianDependentStoreFake
		want           int
	}{
		{name: "missing password prompts", dependents: &guardianDependentStoreFake{}, want: http.StatusUnprocessableEntity},
		{name: "wrong password is counted", password: "wrong", dependents: &guardianDependentStoreFake{reauthErr: ErrGuardianAuthentication}, want: http.StatusUnprocessableEntity},
		{name: "invalid auth locks for fifteen minutes", password: "wrong", dependents: &guardianDependentStoreFake{reauthErr: ErrGuardianAuthentication, reserveErr: map[string]error{"AUTH_INVALID": ErrGuardianApplicationLimited}}, want: http.StatusTooManyRequests},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := authorityDetailStore(ref)
			dashboard := guardianDashboard(&guardianDashboardStore{}, tc.dependents)
			dashboard.GuardianAuthority = store
			w, _, _, _ := request(t, dashboard, tc.dependents, tc.password)
			if w.Code != tc.want || store.transition.ActorID != uuid.Nil || !slices.Equal(tc.dependents.reserveKinds, []string{"AUTH_INVALID"}) {
				t.Fatalf("status=%d transition=%+v reserves=%v", w.Code, store.transition, tc.dependents.reserveKinds)
			}
			if tc.want == http.StatusTooManyRequests && w.Header().Get("Retry-After") != "900" {
				t.Fatalf("retry=%q", w.Header().Get("Retry-After"))
			}
		})
	}

	t.Run("successful confirmation rotates then records", func(t *testing.T) {
		dependents := &guardianDependentStoreFake{}
		store := authorityDetailStore(ref)
		dashboard := guardianDashboard(&guardianDashboardStore{}, dependents)
		dashboard.GuardianAuthority = store
		w, sessions, ctx, oldToken := request(t, dashboard, dependents, "correct horse 7")
		if w.Code != http.StatusSeeOther || store.transition.ActorID != actor || store.transition.Action != "RENEW" || dependents.reauthPassword != "correct horse 7" {
			t.Fatalf("status=%d transition=%+v", w.Code, store.transition)
		}
		if sessions.Token(ctx) == oldToken || sessions.GetString(ctx, "authenticated_at") != now.Format(time.RFC3339Nano) {
			t.Fatal("successful reauthentication did not rotate and refresh session")
		}
	})
}

func TestGuardianAuthorityQueueAndFailurePaths(t *testing.T) {
	dashboard := guardianDashboard(&guardianDashboardStore{}, &guardianDependentStoreFake{})
	dashboard.GuardianAuthority = nil
	w := httptest.NewRecorder()
	dashboard.GuardianAuthorityQueue(w, guardianAuthorityGetRequest("/admin/representacoes", "", uuid.New()))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("nil store status = %d", w.Code)
	}

	dashboard.GuardianAuthority = &guardianAuthorityStoreFake{listPendingErr: errors.New("database unavailable")}
	w = httptest.NewRecorder()
	dashboard.GuardianAuthorityQueue(w, guardianAuthorityGetRequest("/admin/representacoes", "", uuid.New()))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("list failure status = %d", w.Code)
	}

	ref := uuid.New()
	dashboard.GuardianAuthority = &guardianAuthorityStoreFake{pending: []GuardianAuthorityRelationship{{Reference: ref, SubmittedLabel: "Pedido", State: "PENDING", CreatedAt: time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)}}}
	w = httptest.NewRecorder()
	dashboard.GuardianAuthorityQueue(w, guardianAuthorityGetRequest("/admin/representacoes", "", uuid.New()))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), ref.String()) || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("queue status=%d body=%q", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "/admin/representacoes/convites") {
		t.Fatal("non-admin verifier queue exposed the admin-only invitation link")
	}

	adminRequest := httptest.NewRequest(http.MethodGet, "/admin/representacoes", nil)
	adminRequest = adminRequest.WithContext(context.WithValue(adminRequest.Context(), currentUserKey{}, CurrentUser{ID: uuid.New(), Name: "Admin", IsAdmin: true, CanVerifyGuardianAuthority: true}))
	w = httptest.NewRecorder()
	dashboard.GuardianAuthorityQueue(w, adminRequest)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `href="/admin/representacoes/convites"`) {
		t.Fatalf("administrator queue invitation link status=%d body=%q", w.Code, w.Body.String())
	}
}

func TestGuardianAuthorityTransitionFailureMapping(t *testing.T) {
	actor, ref := uuid.New(), uuid.New()
	valid := url.Values{"version": {"2"}, "action": {"APPROVE"}, "evidence_category": {"COURT_OR_LEGAL_AUTHORITY"}, "reason_code": {"RELATIONSHIP_CONFIRMED"}, "attested": {"yes"}}
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"policy", ErrGuardianAuthorityPolicyUnavailable, http.StatusConflict},
		{"invalid", ErrGuardianAuthorityInvalid, http.StatusUnprocessableEntity},
		{"forbidden", ErrGuardianAuthorityForbidden, http.StatusForbidden},
		{"database", errors.New("database unavailable"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := authorityDetailStore(ref)
			store.transitionErr = tc.err
			dashboard := guardianDashboard(&guardianDashboardStore{}, &guardianDependentStoreFake{})
			dashboard.GuardianAuthority = store
			w := httptest.NewRecorder()
			dashboard.GuardianAuthorityTransition(w, guardianAuthorityRequest(ref, actor, valid))
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d", w.Code, tc.want)
			}
		})
	}

	dashboard := guardianDashboard(&guardianDashboardStore{}, &guardianDependentStoreFake{})
	dashboard.GuardianAuthority = nil
	w := httptest.NewRecorder()
	dashboard.GuardianAuthorityTransition(w, guardianAuthorityRequest(ref, actor, valid))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("nil store status = %d", w.Code)
	}
}

func TestGuardianAuthorityDecisionValidationBranches(t *testing.T) {
	actor, ref := uuid.New(), uuid.New()
	cases := []url.Values{
		{"version": {"bad"}, "action": {"UNKNOWN"}},
		{"version": {"0"}, "action": {"REJECT"}, "attested": {"yes"}, "evidence_category": {"UNAPPROVED"}, "reason_code": {"RELATIONSHIP_CONFIRMED"}},
		{"version": {"1"}, "action": {"APPROVE"}, "attested": {"yes"}, "evidence_category": {"COURT_OR_LEGAL_AUTHORITY"}, "reason_code": {"AUTHORITY_ENDED"}},
		{"version": {"1"}, "action": {"SUSPEND"}, "attested": {"yes"}, "evidence_category": {"CLUB_REGISTRATION_RECORD"}, "reason_code": {"CONFLICT"}},
	}
	for i, form := range cases {
		dashboard := guardianDashboard(&guardianDashboardStore{}, &guardianDependentStoreFake{})
		dashboard.GuardianAuthority = authorityDetailStore(ref)
		w := httptest.NewRecorder()
		dashboard.GuardianAuthorityTransition(w, guardianAuthorityRequest(ref, actor, form))
		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("case %d status = %d", i, w.Code)
		}
	}

	dashboard := guardianDashboard(&guardianDashboardStore{}, &guardianDependentStoreFake{})
	dashboard.GuardianAuthority = authorityDetailStore(ref)
	r := guardianAuthorityRequest(ref, actor, url.Values{})
	r.URL.RawQuery = "%"
	r.Body = http.NoBody
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	dashboard.GuardianAuthorityTransition(w, r)
	if w.Code != http.StatusBadRequest && w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("malformed form status = %d", w.Code)
	}
}

func TestGuardianAuthorityDetailFailurePathsAndLabels(t *testing.T) {
	actor, ref := uuid.New(), uuid.New()
	dashboard := guardianDashboard(&guardianDashboardStore{}, &guardianDependentStoreFake{})
	for _, tc := range []struct {
		name  string
		ref   string
		store *guardianAuthorityStoreFake
		want  int
	}{
		{"invalid reference", "bad", authorityDetailStore(ref), http.StatusNotFound},
		{"missing", ref.String(), &guardianAuthorityStoreFake{getErr: pgx.ErrNoRows}, http.StatusNotFound},
		{"get error", ref.String(), &guardianAuthorityStoreFake{getErr: errors.New("database unavailable")}, http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dashboard.GuardianAuthority = tc.store
			w := httptest.NewRecorder()
			dashboard.GuardianAuthorityDetail(w, guardianAuthorityGetRequest("/admin/representacoes/"+tc.ref, tc.ref, actor))
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d", w.Code, tc.want)
			}
		})
	}

	statuses := map[string]string{"PENDING": "A aguardar", "VERIFIED": "verificada", "SUSPENDED": "suspenso", "EXPIRED": "expirada", "REJECTED": "não aprovado", "UNKNOWN": "indisponível"}
	for state, want := range statuses {
		if got := guardianAuthorityStatus(state, false); !strings.Contains(got, want) {
			t.Errorf("status %s = %q", state, got)
		}
	}
	if guardianAuthorityStatus("PENDING", true) != "Em revisão pelo clube" {
		t.Fatal("conflict status not mapped")
	}
}

func authorityDetailStore(ref uuid.UUID) *guardianAuthorityStoreFake {
	return &guardianAuthorityStoreFake{
		detail:        GuardianAuthorityRelationship{Reference: ref, GuardianID: uuid.New(), GuardianName: "Requerente", SubjectID: uuid.New(), SubjectName: "Menor", State: "PENDING", Version: 2, DateOfBirth: time.Date(2012, 1, 2, 0, 0, 0, 0, time.UTC)},
		evidenceTypes: []GuardianAuthorityEvidenceType{{Code: "COURT_ORDER", Label: "Decisão judicial"}},
		reasonCodes:   []GuardianAuthorityReasonCode{{Code: "EVIDENCE_CONFIRMED", Label: "Comprovativo confirmado"}},
	}
}

func guardianAuthorityGetRequest(path, ref string, actor uuid.UUID) *http.Request {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	if ref != "" {
		r.SetPathValue("ref", ref)
	}
	return r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actor, Name: "Verifier", CanVerifyGuardianAuthority: true}))
}

func TestGuardianAuthorityConflictRecorderCannotSeeDecisionForm(t *testing.T) {
	actor, ref := uuid.New(), uuid.New()
	store := &guardianAuthorityStoreFake{
		detail: GuardianAuthorityRelationship{
			Reference: ref, GuardianID: uuid.New(), GuardianName: "Pessoa requerente", SubjectID: uuid.New(),
			SubjectName: "Menor", State: "SUSPENDED", Version: 3, Conflict: true, ConflictActorID: &actor,
			DateOfBirth: time.Date(2012, 1, 2, 0, 0, 0, 0, time.UTC),
		},
	}
	dashboard := guardianDashboard(&guardianDashboardStore{}, &guardianDependentStoreFake{})
	dashboard.GuardianAuthority = store
	r := httptest.NewRequest(http.MethodGet, "/admin/representacoes/"+ref.String(), nil)
	r.SetPathValue("ref", ref.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actor, Name: "Verifier", CanVerifyGuardianAuthority: true}))
	w := httptest.NewRecorder()
	dashboard.GuardianAuthorityDetail(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Outra pessoa administradora") || strings.Contains(w.Body.String(), `id="guardian-authority-decisions"`) {
		t.Fatalf("response=%d %q", w.Code, w.Body.String())
	}
}

func TestGuardianAuthorityExpiredDetailOffersRenewalAndMaterializedExpiry(t *testing.T) {
	actor, ref := uuid.New(), uuid.New()
	reviewDue := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	store := &guardianAuthorityStoreFake{detail: GuardianAuthorityRelationship{Reference: ref, GuardianID: uuid.New(), GuardianName: "Pessoa requerente", SubjectID: uuid.New(), SubjectName: "Menor", State: "EXPIRED", StoredState: "VERIFIED", Version: 2, ReviewDueAt: &reviewDue, RenewalStatus: "SUBMITTED", RenewalResponse: "NADA_MUDOU", DateOfBirth: time.Date(2012, 1, 2, 0, 0, 0, 0, time.UTC)}}
	dashboard := guardianDashboard(&guardianDashboardStore{}, &guardianDependentStoreFake{})
	dashboard.GuardianAuthority = store
	dashboard.Now = func() time.Time { return time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC) }
	r := httptest.NewRequest(http.MethodGet, "/admin/representacoes/"+ref.String(), nil)
	r.SetPathValue("ref", ref.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actor, Name: "Verifier", CanVerifyGuardianAuthority: true}))
	w := httptest.NewRecorder()
	dashboard.GuardianAuthorityDetail(w, r)
	body := w.Body.String()
	for _, want := range []string{`value="RENEW"`, `data-task-form`} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q", want)
		}
	}
	if strings.Contains(body, `value="REJECT"`) || strings.Contains(body, `value="SUSPEND"`) || strings.Contains(body, `value="END"`) {
		t.Fatal("effectively expired relationship offered an invalid action")
	}
}

func TestGuardianAuthorityTerminalAndMajorityDetailsAreReadOnly(t *testing.T) {
	now := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, state, stored, want string
		birth                     time.Time
	}{
		{name: "rejected", state: "REJECTED", stored: "REJECTED", birth: time.Date(2012, 1, 2, 0, 0, 0, 0, time.UTC), want: "Este pedido terminou sem aprovação"},
		{name: "majority", state: "EXPIRED", stored: "VERIFIED", birth: time.Date(2008, 9, 11, 0, 0, 0, 0, time.UTC), want: "estabelecer ou recuperar o acesso à conta adulta correta"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actor, ref := uuid.New(), uuid.New()
			store := &guardianAuthorityStoreFake{detail: GuardianAuthorityRelationship{Reference: ref, GuardianID: uuid.New(), GuardianName: "Pessoa requerente", SubjectID: uuid.New(), SubjectName: "Pessoa", State: tc.state, StoredState: tc.stored, Version: 3, DateOfBirth: tc.birth}}
			dashboard := guardianDashboard(&guardianDashboardStore{}, &guardianDependentStoreFake{})
			dashboard.GuardianAuthority = store
			dashboard.Now = func() time.Time { return now }
			r := httptest.NewRequest(http.MethodGet, "/admin/representacoes/"+ref.String(), nil)
			r.SetPathValue("ref", ref.String())
			r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actor, Name: "Verifier", CanVerifyGuardianAuthority: true}))
			w := httptest.NewRecorder()
			dashboard.GuardianAuthorityDetail(w, r)
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), tc.want) || strings.Contains(w.Body.String(), `id="guardian-authority-decisions"`) {
				t.Fatalf("response=%d %q", w.Code, w.Body.String())
			}
		})
	}
}

func TestGuardianDashboardShowsRenewalWindowAndSubmittedStates(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	expires := now.Add(20 * 24 * time.Hour)
	birthday := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	store := &guardianAuthorityStoreFake{policyAvailable: true, relationships: []GuardianAuthorityRelationship{
		{Reference: uuid.New(), SubjectID: uuid.New(), SubjectName: "Menor um", SubmittedLabel: "Menor um", State: "VERIFIED", Version: 4, DateOfBirth: now.AddDate(-12, 0, 0), VerifiedUntil: &expires, ProfileComplete: true, AgeHandoffStatus: "READY", AgeHandoffBirthday: &birthday},
		{Reference: uuid.New(), SubjectID: uuid.New(), SubjectName: "Menor dois", SubmittedLabel: "Menor dois", State: "VERIFIED", Version: 7, DateOfBirth: now.AddDate(-10, 0, 0), VerifiedUntil: &expires, RenewalStatus: "SUBMITTED", RenewalResponse: "NADA_MUDOU", ProfileComplete: true},
	}}
	dashboard := guardianDashboard(&guardianDashboardStore{}, &guardianDependentStoreFake{})
	dashboard.GuardianAuthority = store
	dashboard.Now = func() time.Time { return now }
	response := guardianResponse(t, dashboard.Guardian, uuid.New(), nil)
	body := response.Body.String()
	for _, want := range []string{"Validade atual:", "02/10/2026", "Renovação disponível", `value="NADA_MUDOU"`, `value="DADOS_MUDARAM"`, "Renovação enviada", "não foi prolongado automaticamente", "Transição aos 18 anos:", "Preparação concluída", "O seu acesso como responsável termina nesse dia."} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q", want)
		}
	}
}

func TestGuardianDashboardViewModelsCoverEveryHandoffAndRenewalOutcome(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	birthday := now.Add(30 * 24 * time.Hour)
	for status, want := range map[string]string{
		"EMAIL_VERIFIED":     "Confirmação do clube em falta",
		"IDENTITY_CONFIRMED": "Confirmação de email em falta",
		"RECOVERY_REQUIRED":  "Recuperação presencial necessária",
		"EMAIL_COLLISION":    "Recuperação presencial necessária",
		"COMPLETED":          "Transição concluída",
		"PENDING":            "Preparação em curso",
	} {
		view := guardianAgeHandoffView(GuardianAuthorityRelationship{AgeHandoffStatus: status, AgeHandoffBirthday: &birthday}, time.UTC)
		if view.Status != want || view.Birthday != "12/10/2026" {
			t.Errorf("status=%s view=%+v", status, view)
		}
	}
	expired := now.Add(-time.Hour)
	view := guardianRenewalView(GuardianAuthorityRelationship{Reference: uuid.New(), State: "EXPIRED", VerifiedUntil: &expired, RenewalStatus: "SUBMITTED"}, now, time.UTC)
	if view.Status != "Renovação expirada" || !strings.Contains(view.Detail, "continua em revisão") {
		t.Fatalf("expired submitted view=%+v", view)
	}
	future := now.Add(20 * 24 * time.Hour)
	view = guardianRenewalView(GuardianAuthorityRelationship{Reference: uuid.New(), State: "VERIFIED", VerifiedUntil: &future, RenewalStatus: "SUBMITTED", RenewalResponse: "DADOS_MUDARAM"}, now, time.UTC)
	if view.Status != "Renovação enviada" || !strings.Contains(view.Detail, "acesso foi suspenso") {
		t.Fatalf("changed renewal view=%+v", view)
	}
}

func TestGuardianRenewalSubmissionIsTypedAndOptimistic(t *testing.T) {
	actor, reference := uuid.New(), uuid.New()
	store := &guardianAuthorityStoreFake{policyAvailable: true}
	dashboard := guardianDashboard(&guardianDashboardStore{}, &guardianDependentStoreFake{})
	dashboard.GuardianAuthority = store
	form := url.Values{"version": {"6"}, "response_code": {"DADOS_MUDARAM"}}
	request := httptest.NewRequest(http.MethodPost, "/dashboard/guardian/representacoes/"+reference.String()+"/renovar", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.SetPathValue("ref", reference.String())
	request = request.WithContext(context.WithValue(request.Context(), currentUserKey{}, CurrentUser{ID: actor, Name: "Responsável", EmailVerified: true}))
	response := httptest.NewRecorder()
	dashboard.SubmitGuardianAuthorityRenewal(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/dashboard/guardian" {
		t.Fatalf("response=%d location=%q", response.Code, response.Header().Get("Location"))
	}
	if store.renewalActor != actor || store.renewalReference != reference || store.renewalVersion != 6 || store.renewalResponse != "DADOS_MUDARAM" {
		t.Fatalf("renewal input actor=%v ref=%v version=%d response=%q", store.renewalActor, store.renewalReference, store.renewalVersion, store.renewalResponse)
	}

	store.renewalErr = ErrGuardianAuthorityConflict
	response = httptest.NewRecorder()
	dashboard.SubmitGuardianAuthorityRenewal(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "foi alterada") {
		t.Fatalf("conflict response=%d body=%q", response.Code, response.Body.String())
	}
}

func guardianAuthorityRequest(ref, actor uuid.UUID, form url.Values) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/admin/representacoes/"+ref.String(), strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetPathValue("ref", ref.String())
	return r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actor, Name: "Verifier", CanVerifyGuardianAuthority: true}))
}

func guardianDashboard(store DashboardStore, dependents GuardianDependentStore) Dashboard {
	authority := &guardianAuthorityStoreFake{policyAvailable: true}
	if source, ok := store.(*guardianDashboardStore); ok {
		for _, dependent := range source.dependents {
			authority.relationships = append(authority.relationships, GuardianAuthorityRelationship{Reference: uuid.New(), SubjectID: dependent.ID, SubmittedLabel: dependent.Name, SubjectName: dependent.Name, State: "VERIFIED", Version: 1, DateOfBirth: dependent.DateOfBirth.Time, VerifiedUntil: timePointer(time.Date(2027, 7, 24, 0, 0, 0, 0, time.UTC)), LeaderboardVisible: dependent.LeaderboardVisible, ProfileComplete: dependent.ProfileComplete})
		}
	}
	return Dashboard{
		Store: store, Dependents: dependents, GuardianAuthority: authority, Location: time.UTC, Now: func() time.Time { return time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC) },
		PageMeta:              components.PageMeta{StylesheetURL: "/assets/app.css", ScriptURL: "/assets/app.js"},
		ResponsibilityVersion: "1.0", ResponsibilitySHA256: strings.Repeat("c", 64), ResponsibilityURL: "https://example.test/responsabilidade",
	}
}

func timePointer(value time.Time) *time.Time { return &value }

type guardianAuthorityStoreFake struct {
	policyAvailable                bool
	relationships                  []GuardianAuthorityRelationship
	pending                        []GuardianAuthorityRelationship
	detail                         GuardianAuthorityRelationship
	evidenceTypes                  []GuardianAuthorityEvidenceType
	reasonCodes                    []GuardianAuthorityReasonCode
	transition                     GuardianAuthorityTransitionInput
	err                            error
	listPendingErr                 error
	getErr                         error
	evidenceErr                    error
	reasonErr                      error
	transitionErr                  error
	invitationEmail                string
	invitationDigest               []byte
	invitations                    []GuardianAuthorityInvitation
	revokedInvitation              uuid.UUID
	renewalActor, renewalReference uuid.UUID
	renewalVersion                 int64
	renewalResponse                string
	renewalErr                     error
}

func (s *guardianAuthorityStoreFake) PolicyAvailable(context.Context) (bool, error) {
	return s.policyAvailable, s.err
}
func (s *guardianAuthorityStoreFake) EvidenceTypes(context.Context) ([]GuardianAuthorityEvidenceType, error) {
	if s.evidenceErr != nil {
		return nil, s.evidenceErr
	}
	return s.evidenceTypes, s.err
}
func (s *guardianAuthorityStoreFake) ReasonCodes(context.Context) ([]GuardianAuthorityReasonCode, error) {
	if s.reasonErr != nil {
		return nil, s.reasonErr
	}
	return s.reasonCodes, s.err
}
func (s *guardianAuthorityStoreFake) ListForGuardian(context.Context, uuid.UUID, int32) ([]GuardianAuthorityRelationship, error) {
	return s.relationships, s.err
}
func (s *guardianAuthorityStoreFake) ListPending(context.Context, uuid.UUID, int32, int32) ([]GuardianAuthorityRelationship, error) {
	if s.listPendingErr != nil {
		return nil, s.listPendingErr
	}
	return s.pending, s.err
}
func (s *guardianAuthorityStoreFake) GetForVerifier(context.Context, uuid.UUID, uuid.UUID) (GuardianAuthorityRelationship, error) {
	if s.getErr != nil {
		return GuardianAuthorityRelationship{}, s.getErr
	}
	return s.detail, s.err
}
func (s *guardianAuthorityStoreFake) Transition(_ context.Context, input GuardianAuthorityTransitionInput) error {
	s.transition = input
	return s.transitionErr
}
func (s *guardianAuthorityStoreFake) IssueInvitation(_ context.Context, _ uuid.UUID, email string, digest []byte) (GuardianAuthorityInvitation, error) {
	s.invitationEmail, s.invitationDigest = email, append([]byte(nil), digest...)
	return GuardianAuthorityInvitation{}, s.err
}
func (s *guardianAuthorityStoreFake) ListInvitations(context.Context, uuid.UUID, int32) ([]GuardianAuthorityInvitation, error) {
	return s.invitations, s.err
}
func (s *guardianAuthorityStoreFake) RevokeInvitation(_ context.Context, _ uuid.UUID, reference uuid.UUID) error {
	s.revokedInvitation = reference
	return s.err
}
func (s *guardianAuthorityStoreFake) SubmitRenewal(_ context.Context, actorID, reference uuid.UUID, version int64, response string) error {
	s.renewalActor, s.renewalReference, s.renewalVersion, s.renewalResponse = actorID, reference, version, response
	return s.renewalErr
}

func validDependentForm() url.Values {
	return url.Values{"name": {"  Maria   Silva "}, "date_of_birth": {"2010-07-24"}, "accept_minor_responsibility": {"on"}}
}

func guardianResponse(t *testing.T, handler http.HandlerFunc, guardianID uuid.UUID, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	method, body := http.MethodGet, ""
	if form != nil {
		method, body = http.MethodPost, form.Encode()
	}
	request := httptest.NewRequest(method, "/dashboard/guardian", strings.NewReader(body))
	if form != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	ctx := context.WithValue(request.Context(), currentUserKey{}, CurrentUser{ID: guardianID, Name: "Guardião", EmailVerified: true})
	response := httptest.NewRecorder()
	handler(response, request.WithContext(ctx))
	return response
}

type guardianDependentStoreFake struct {
	input          GuardianDependentInput
	err            error
	called         bool
	reserveKinds   []string
	reserveErr     map[string]error
	reauthErr      error
	reauthPassword string
}

func (s *guardianDependentStoreFake) ReserveAttempt(_ context.Context, _ uuid.UUID, _ *netip.Addr, kind string) error {
	s.reserveKinds = append(s.reserveKinds, kind)
	return s.reserveErr[kind]
}
func (s *guardianDependentStoreFake) Reauthenticate(_ context.Context, _ uuid.UUID, password string) error {
	s.reauthPassword = password
	return s.reauthErr
}

func (s *guardianDependentStoreFake) CreateDependent(_ context.Context, input GuardianDependentInput) error {
	s.called = true
	s.input = input
	return s.err
}

type guardianDashboardStore struct {
	dependents []dbgen.ListDependentsByGuardianRow
	err        error
}

func (s *guardianDashboardStore) ListRecentPerformanceMetrics(context.Context, dbgen.ListRecentPerformanceMetricsParams) ([]dbgen.PerformanceMetric, error) {
	return nil, errors.New("not used")
}
func (s *guardianDashboardStore) ListRecentTrainingLogs(context.Context, dbgen.ListRecentTrainingLogsParams) ([]dbgen.TrainingLog, error) {
	return nil, errors.New("not used")
}
func (s *guardianDashboardStore) ListPublishedNews(context.Context, int32) ([]dbgen.NewsItem, error) {
	return nil, errors.New("not used")
}
func (s *guardianDashboardStore) ListWhatsAppGroupsForUserProgramme(context.Context, dbgen.ListWhatsAppGroupsForUserProgrammeParams) ([]dbgen.WhatsappGroup, error) {
	return nil, errors.New("not used")
}
func (s *guardianDashboardStore) ListDependentsByGuardian(context.Context, dbgen.ListDependentsByGuardianParams) ([]dbgen.ListDependentsByGuardianRow, error) {
	return s.dependents, s.err
}
func (s *guardianDashboardStore) ListOperationalEquipment(context.Context, int32) ([]dbgen.Equipment, error) {
	return nil, nil
}
func (s *guardianDashboardStore) ListEventsForMember(context.Context, dbgen.ListEventsForMemberParams) ([]dbgen.ListEventsForMemberRow, error) {
	return nil, errors.New("not used")
}
func (s *guardianDashboardStore) ListUpcomingTrainingSessionsForDashboard(context.Context, dbgen.ListUpcomingTrainingSessionsForDashboardParams) ([]dbgen.ListUpcomingTrainingSessionsForDashboardRow, error) {
	return nil, errors.New("not used")
}
func (s *guardianDashboardStore) ListEventsForToday(context.Context, dbgen.ListEventsForTodayParams) ([]dbgen.ListEventsForTodayRow, error) {
	return nil, errors.New("not used")
}
func (s *guardianDashboardStore) ListVisibleAnnouncements(context.Context, dbgen.ListVisibleAnnouncementsParams) ([]dbgen.ListVisibleAnnouncementsRow, error) {
	return nil, errors.New("not used")
}
func (s *guardianDashboardStore) ListDistanceLeaderboard(context.Context, dbgen.ListDistanceLeaderboardParams) ([]dbgen.ListDistanceLeaderboardRow, error) {
	return nil, errors.New("not used")
}
func (s *guardianDashboardStore) UpdateOwnLeaderboardVisibility(context.Context, dbgen.UpdateOwnLeaderboardVisibilityParams) (int64, error) {
	return 0, errors.New("not used")
}
func (s *guardianDashboardStore) UpdateDependentLeaderboardVisibility(context.Context, dbgen.UpdateDependentLeaderboardVisibilityParams) (int64, error) {
	return 0, errors.New("not used")
}
