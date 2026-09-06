package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/alexedwards/scs/v2"
	"github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

type loginLookup struct {
	user  dbgen.GetActiveUserByEmailRow
	minor dbgen.GetActiveDependentByLoginIDRow
	err   error
	email string
}

func (l *loginLookup) GetActiveUserByEmail(_ context.Context, email *string) (dbgen.GetActiveUserByEmailRow, error) {
	if email != nil {
		l.email = *email
	}
	user := l.user
	if user.CredentialVersion == 0 {
		user.CredentialVersion = 1
	}
	return user, l.err
}
func (l *loginLookup) GetActiveDependentByLoginID(_ context.Context, _ *string) (dbgen.GetActiveDependentByLoginIDRow, error) {
	minor := l.minor
	if minor.CredentialVersion == 0 {
		minor.CredentialVersion = 1
	}
	return minor, l.err
}

func TestLoginGetRendersForm(t *testing.T) {
	sessions := scs.New()
	handler := Login{
		Sessions: sessions,
		PageMeta: loginTestPageMeta(),
	}
	response := httptest.NewRecorder()
	sessions.LoadAndSave(http.HandlerFunc(handler.Get)).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/login?next=%2Fdashboard%2Fleisure", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	for _, expected := range []string{
		`<body class="auth-body">`,
		`aria-label="MyCFCoimbra, voltar à página inicial"`,
		`<h1 id="login-title">Iniciar sessão</h1>`,
		`action="/login"`,
		`name="next" value="/dashboard/leisure"`,
		`autocomplete="username"`,
		`autocomplete="current-password"`,
		`href="/recuperar-palavra-passe">Recuperar palavra-passe</a>`,
		`href="/registo">Criar conta MyCFCoimbra</a>`,
	} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Errorf("body does not contain %q", expected)
		}
	}
	for _, forbidden := range []string{`class="app-shell"`, `aria-label="Navegação principal"`, `Terminar sessão`} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Errorf("authentication shell unexpectedly contains %q", forbidden)
		}
	}
}

func TestLoginShowsPasswordResetConfirmationOnce(t *testing.T) {
	sessions := scs.New()
	seedResponse := httptest.NewRecorder()
	sessions.LoadAndSave(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		sessions.Put(r.Context(), "login_flash", "A palavra-passe foi alterada. Já pode iniciar sessão.")
	})).ServeHTTP(seedResponse, httptest.NewRequest(http.MethodGet, "/", nil))
	seedCookies := seedResponse.Result().Cookies()
	if len(seedCookies) == 0 {
		t.Fatal("session cookie was not created")
	}

	handler := Login{Sessions: sessions, PageMeta: loginTestPageMeta()}
	firstRequest := httptest.NewRequest(http.MethodGet, "/login", nil)
	firstRequest.AddCookie(seedCookies[0])
	firstResponse := httptest.NewRecorder()
	sessions.LoadAndSave(http.HandlerFunc(handler.Get)).ServeHTTP(firstResponse, firstRequest)
	if !strings.Contains(firstResponse.Body.String(), "A palavra-passe foi alterada. Já pode iniciar sessão.") {
		t.Fatalf("first response = %q", firstResponse.Body.String())
	}

	secondRequest := httptest.NewRequest(http.MethodGet, "/login", nil)
	flashCookie := seedCookies[0]
	for _, cookie := range firstResponse.Result().Cookies() {
		if cookie.Name == flashCookie.Name {
			flashCookie = cookie
		}
	}
	secondRequest.AddCookie(flashCookie)
	secondResponse := httptest.NewRecorder()
	sessions.LoadAndSave(http.HandlerFunc(handler.Get)).ServeHTTP(secondResponse, secondRequest)
	if strings.Contains(secondResponse.Body.String(), "A palavra-passe foi alterada") {
		t.Fatalf("flash was rendered more than once: %q", secondResponse.Body.String())
	}
}

func TestLoginValidationPreservesIdentifierAndUsesSharedErrorContract(t *testing.T) {
	handler := Login{Sessions: scs.New(), PageMeta: loginTestPageMeta(), FailureWait: func(context.Context) {}}
	form := url.Values{"identifier": {"member@example.com"}}
	request := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	handler.Post(response, request)

	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", response.Code)
	}
	for _, expected := range []string{`value="member@example.com"`, `class="error-summary"`, `aria-invalid="true"`, `aria-describedby="identifier-help identifier-error"`, `id="identifier-error"`} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Errorf("body does not contain %q", expected)
		}
	}
}

func TestLoginPostRedirectsAndNormalizesEmail(t *testing.T) {
	password := "correct horse battery staple"
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}
	passwordHash := string(hash)
	lookup := &loginLookup{user: dbgen.GetActiveUserByEmailRow{
		ID:           uuid.New(),
		PasswordHash: &passwordHash,
	}}
	sessions := scs.New()
	handler := Login{
		Users:       lookup,
		Sessions:    sessions,
		PageMeta:    loginTestPageMeta(),
		FailureWait: func(context.Context) {},
	}
	form := url.Values{
		"identifier": {" MEMBER@EXAMPLE.COM "},
		"password":   {password},
		"next":       {"/dashboard/leisure"},
	}
	request := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	sessions.LoadAndSave(http.HandlerFunc(handler.Post)).ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusSeeOther)
	}
	if got := response.Header().Get("Location"); got != "/dashboard/leisure" {
		t.Fatalf("Location = %q", got)
	}
	if lookup.email != "member@example.com" {
		t.Fatalf("email = %q", lookup.email)
	}
	if len(response.Result().Cookies()) == 0 {
		t.Fatal("successful login did not set a session cookie")
	}
	ctx, err := sessions.Load(context.Background(), response.Result().Cookies()[0].Value)
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	if got := sessions.GetString(ctx, "user_id"); got != lookup.user.ID.String() {
		t.Fatalf("session user_id = %q", got)
	}
	if got := sessions.GetInt64(ctx, "credential_version"); got != 1 {
		t.Fatalf("session credential_version = %d", got)
	}
	if sessions.GetString(ctx, "last_seen_at") == "" {
		t.Fatal("session last_seen_at is empty")
	}
}

func TestLoginPostReturnsHTMXRedirect(t *testing.T) {
	password := "correct horse battery staple"
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}
	passwordHash := string(hash)
	handler := Login{
		Users: &loginLookup{user: dbgen.GetActiveUserByEmailRow{
			ID:           uuid.New(),
			PasswordHash: &passwordHash,
		}},
		Sessions: scs.New(),
		PageMeta: loginTestPageMeta(),
	}
	form := url.Values{"identifier": {"member@example.com"}, "password": {password}}
	request := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("HX-Request", "true")
	response := httptest.NewRecorder()
	handler.Sessions.LoadAndSave(http.HandlerFunc(handler.Post)).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("HX-Redirect"); got != "/dashboard" {
		t.Fatalf("HX-Redirect = %q", got)
	}
}

func TestLoginPostAllowsIssuedMinorIdentifier(t *testing.T) {
	password := "correct horse battery staple"
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	passwordHash := string(hash)
	minorID := uuid.New()
	handler := Login{Users: &loginLookup{minor: dbgen.GetActiveDependentByLoginIDRow{ID: minorID, PasswordHash: &passwordHash}}, Sessions: scs.New(), PageMeta: loginTestPageMeta(), FailureWait: func(context.Context) {}}
	form := url.Values{"identifier": {"cfc-ab12cd34"}, "password": {password}}
	request := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	handler.Sessions.LoadAndSave(http.HandlerFunc(handler.Post)).ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestLoginPostReturnsGenericFailure(t *testing.T) {
	lookup := &loginLookup{err: pgx.ErrNoRows}
	handler := Login{
		Users:       lookup,
		Sessions:    scs.New(),
		PageMeta:    loginTestPageMeta(),
		FailureWait: func(context.Context) {},
	}
	form := url.Values{"identifier": {"nobody@example.com"}, "password": {"incorrect password"}}
	request := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	handler.Sessions.LoadAndSave(http.HandlerFunc(handler.Post)).ServeHTTP(response, request)

	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnprocessableEntity)
	}
	if !strings.Contains(response.Body.String(), invalidLoginMessage) {
		t.Fatalf("body = %q", response.Body.String())
	}
}

func TestLoginPostReturnsInternalErrorForLookupFailure(t *testing.T) {
	handler := Login{
		Users:       &loginLookup{err: errors.New("database unavailable")},
		Sessions:    scs.New(),
		PageMeta:    loginTestPageMeta(),
		FailureWait: func(context.Context) {},
	}
	form := url.Values{"identifier": {"member@example.com"}, "password": {"correct horse battery staple"}}
	request := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	handler.Sessions.LoadAndSave(http.HandlerFunc(handler.Post)).ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
}

func loginTestPageMeta() components.PageMeta {
	return components.PageMeta{
		Title:         "Iniciar sessão | MyCFCoimbra",
		StylesheetURL: "/assets/app.css",
		ScriptURL:     "/assets/app.js",
	}
}
