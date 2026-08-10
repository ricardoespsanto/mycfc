package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/cfcoimbra/mycfc/internal/passwordreset"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/google/uuid"
)

type passwordRecoveryServiceFake struct {
	issueEmail, consumedToken, consumedPassword string
	issueCalls, resolveCalls, consumeCalls      int
	issueErr, resolveErr, consumeErr            error
}

func (f *passwordRecoveryServiceFake) Issue(_ context.Context, email string, _ bool) (uuid.UUID, error) {
	f.issueCalls++
	f.issueEmail = email
	return uuid.New(), f.issueErr
}
func (f *passwordRecoveryServiceFake) Resolve(context.Context, string) (uuid.UUID, error) {
	f.resolveCalls++
	return uuid.New(), f.resolveErr
}
func (f *passwordRecoveryServiceFake) Consume(_ context.Context, token, password string) (uuid.UUID, error) {
	f.consumeCalls++
	f.consumedToken, f.consumedPassword = token, password
	return uuid.New(), f.consumeErr
}

type recoveryLimiterFake struct {
	allowed bool
	address netip.Addr
	calls   int
}

func (f *recoveryLimiterFake) Allow(address netip.Addr, _ time.Time) bool {
	f.calls++
	f.address = address
	return f.allowed
}

func TestPasswordRecoveryRequestRendersAccessibleNoStoreForm(t *testing.T) {
	handler := passwordRecoveryTestHandler(&passwordRecoveryServiceFake{})
	response := httptest.NewRecorder()
	handler.RequestGet(response, httptest.NewRequest(http.MethodGet, "/recuperar-palavra-passe", nil))

	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("response = %d, cache %q", response.Code, response.Header().Get("Cache-Control"))
	}
	for _, expected := range []string{
		`<body class="auth-body">`, `<h1 id="password-recovery-title">Recuperar palavra-passe</h1>`,
		`action="/recuperar-palavra-passe"`, `autocomplete="email"`,
		`href="/login">Voltar a iniciar sessão</a>`, "identificadores CFC de menores",
	} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Errorf("body does not contain %q", expected)
		}
	}
}

func TestPasswordRecoveryRequestReturnsGenericBoundedResponse(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	remote := netip.MustParseAddr("203.0.113.24")
	var baseline string
	for _, tc := range []struct {
		name       string
		identifier string
		issueErr   error
		allowed    bool
		wantCalls  int
		wantEmail  string
	}{
		{name: "eligible", identifier: " Member@Example.TEST ", allowed: true, wantCalls: 1, wantEmail: "member@example.test"},
		{name: "unknown", identifier: "unknown@example.test", issueErr: passwordreset.ErrIneligible, allowed: true, wantCalls: 1, wantEmail: "unknown@example.test"},
		{name: "inactive", identifier: "inactive@example.test", issueErr: passwordreset.ErrIneligible, allowed: true, wantCalls: 1, wantEmail: "inactive@example.test"},
		{name: "throttled", identifier: "member@example.test", issueErr: passwordreset.ErrTooSoon, allowed: true, wantCalls: 1, wantEmail: "member@example.test"},
		{name: "malformed", identifier: "not an email", allowed: true},
		{name: "dependent identifier", identifier: "CFC-AB12CD34", allowed: true},
		{name: "client limited", identifier: "member@example.test", allowed: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &passwordRecoveryServiceFake{issueErr: tc.issueErr}
			limiter := &recoveryLimiterFake{allowed: tc.allowed}
			waits := 0
			handler := passwordRecoveryTestHandler(service)
			handler.Limiter = limiter
			handler.Now = func() time.Time { return now }
			handler.ResponseWait = func(context.Context, time.Time) { waits++ }
			form := url.Values{"identifier": {tc.identifier}}
			request := httptest.NewRequest(http.MethodPost, "/recuperar-palavra-passe", strings.NewReader(form.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request = request.WithContext(httpx.WithRemoteIP(request.Context(), remote))
			response := httptest.NewRecorder()
			handler.RequestPost(response, request)

			if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || waits != 1 {
				t.Fatalf("response = %d, cache %q, waits %d", response.Code, response.Header().Get("Cache-Control"), waits)
			}
			if service.issueCalls != tc.wantCalls || service.issueEmail != tc.wantEmail {
				t.Fatalf("issuance = %d calls, email %q", service.issueCalls, service.issueEmail)
			}
			if limiter.calls != 1 || limiter.address != remote {
				t.Fatalf("limiter = %d calls, address %s", limiter.calls, limiter.address)
			}
			body := response.Body.String()
			if !strings.Contains(body, "Se os dados corresponderem a uma conta adulta elegível") || strings.Contains(body, tc.identifier) {
				t.Fatalf("confirmation leaked outcome or identifier: %q", body)
			}
			if baseline == "" {
				baseline = body
			} else if body != baseline {
				t.Fatal("generic confirmation differs between account outcomes")
			}
		})
	}
}

func TestPasswordRecoveryLimiterUsesClientWindow(t *testing.T) {
	limiter := NewPasswordRecoveryLimiter()
	client, other := netip.MustParseAddr("203.0.113.10"), netip.MustParseAddr("203.0.113.11")
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	for attempt := 1; attempt <= recoveryClientLimit; attempt++ {
		if !limiter.Allow(client, now) {
			t.Fatalf("attempt %d was unexpectedly limited", attempt)
		}
	}
	if limiter.Allow(client, now) {
		t.Fatal("request above the per-client limit was allowed")
	}
	if !limiter.Allow(other, now) {
		t.Fatal("one client limited a different address")
	}
	if !limiter.Allow(client, now.Add(recoveryClientWindow)) {
		t.Fatal("client window did not reset")
	}
}

func TestPasswordRecoveryResetRendersTokenFormAndSafeInvalidState(t *testing.T) {
	token := "opaque-token-that-must-not-enter-the-title"
	service := &passwordRecoveryServiceFake{}
	handler := passwordRecoveryTestHandler(service)
	response := httptest.NewRecorder()
	handler.ResetGet(response, httptest.NewRequest(http.MethodGet, "/recuperar-palavra-passe/repor?token="+token, nil))
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("response = %d, cache %q, referrer %q", response.Code, response.Header().Get("Cache-Control"), response.Header().Get("Referrer-Policy"))
	}
	for _, expected := range []string{`name="token" value="` + token + `"`, `autocomplete="new-password"`, `id="password_confirmation"`} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Errorf("body does not contain %q", expected)
		}
	}
	if strings.Contains(response.Body.String(), "<title>"+token) {
		t.Fatal("token leaked into the page title")
	}

	var invalidBody string
	for _, raw := range []string{"", "bad-token"} {
		service.resolveErr = passwordreset.ErrInvalidToken
		response = httptest.NewRecorder()
		handler.ResetGet(response, httptest.NewRequest(http.MethodGet, "/recuperar-palavra-passe/repor?token="+raw, nil))
		if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "Este link já não está disponível") || strings.Contains(response.Body.String(), raw) && raw != "" {
			t.Fatalf("invalid response = %d %q", response.Code, response.Body.String())
		}
		if invalidBody == "" {
			invalidBody = response.Body.String()
		} else if response.Body.String() != invalidBody {
			t.Fatal("invalid token states render different responses")
		}
	}
}

func TestPasswordRecoveryValidationLeavesTokenUsableAndPasswordsEmpty(t *testing.T) {
	service := &passwordRecoveryServiceFake{}
	handler := passwordRecoveryTestHandler(service)
	form := url.Values{"token": {"still-valid-token"}, "password": {"short"}, "password_confirmation": {"different secret"}}
	request := httptest.NewRequest(http.MethodPost, "/recuperar-palavra-passe/repor", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	handler.ResetPost(response, request)

	if response.Code != http.StatusUnprocessableEntity || service.consumeCalls != 0 || service.resolveCalls != 1 {
		t.Fatalf("response = %d, resolve %d, consume %d", response.Code, service.resolveCalls, service.consumeCalls)
	}
	for _, expected := range []string{`class="error-summary"`, `autofocus`, `name="token" value="still-valid-token"`, "As palavras-passe não coincidem."} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Errorf("body does not contain %q", expected)
		}
	}
	for _, secret := range []string{"short", "different secret"} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatalf("password was repopulated: %q", secret)
		}
	}
}

func TestPasswordRecoveryConsumesOnceAndSetsLoginFlash(t *testing.T) {
	sessions := scs.New()
	service := &passwordRecoveryServiceFake{}
	handler := passwordRecoveryTestHandler(service)
	handler.Sessions = sessions
	password := "nova palavra 7"
	form := url.Values{"token": {"valid-token"}, "password": {password}, "password_confirmation": {password}}
	request := httptest.NewRequest(http.MethodPost, "/recuperar-palavra-passe/repor", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	sessions.LoadAndSave(http.HandlerFunc(handler.ResetPost)).ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/login" {
		t.Fatalf("response = %d %q", response.Code, response.Header().Get("Location"))
	}
	if service.consumeCalls != 1 || service.consumedToken != "valid-token" || service.consumedPassword != password {
		t.Fatalf("consumption = %d, %q, %q", service.consumeCalls, service.consumedToken, service.consumedPassword)
	}
	cookies := response.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("success did not persist a flash session")
	}
	ctx, err := sessions.Load(context.Background(), cookies[0].Value)
	if err != nil {
		t.Fatal(err)
	}
	if got := sessions.GetString(ctx, "login_flash"); got != "A palavra-passe foi alterada. Já pode iniciar sessão." {
		t.Fatalf("login flash = %q", got)
	}
}

func TestPasswordRecoveryReplayUsesSafeInvalidLinkOutcome(t *testing.T) {
	service := &passwordRecoveryServiceFake{consumeErr: passwordreset.ErrInvalidToken}
	handler := passwordRecoveryTestHandler(service)
	password := "nova palavra 7"
	form := url.Values{"token": {"used-token"}, "password": {password}, "password_confirmation": {password}}
	request := httptest.NewRequest(http.MethodPost, "/recuperar-palavra-passe/repor", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	handler.ResetPost(response, request)
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "Este link já não está disponível") || strings.Contains(response.Body.String(), "used-token") {
		t.Fatalf("replay response = %d %q", response.Code, response.Body.String())
	}
}

func TestPasswordRecoveryUnexpectedResolveFailureUsesSystemError(t *testing.T) {
	service := &passwordRecoveryServiceFake{resolveErr: errors.New("database unavailable")}
	handler := passwordRecoveryTestHandler(service)
	response := httptest.NewRecorder()
	handler.ResetGet(response, httptest.NewRequest(http.MethodGet, "/recuperar-palavra-passe/repor?token=opaque", nil))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", response.Code)
	}
}

func passwordRecoveryTestHandler(service PasswordRecoveryService) PasswordRecovery {
	return PasswordRecovery{
		Service:      service,
		PageMeta:     components.PageMeta{StylesheetURL: "/assets/app.css", ScriptURL: "/assets/app.js", BrandImageURL: "/assets/logo.png"},
		System:       System{PageMeta: components.PageMeta{}},
		ResponseWait: func(context.Context, time.Time) {},
	}
}
