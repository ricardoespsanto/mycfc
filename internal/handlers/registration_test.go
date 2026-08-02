package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"
)

type registrationStore struct {
	input  RegistrationInput
	err    error
	called bool
}

func (s *registrationStore) RegisterAdult(_ context.Context, input RegistrationInput) (RegistrationResult, error) {
	s.called = true
	s.input = input
	return RegistrationResult{UserID: uuid.MustParse("1b7b67c8-5072-4a4f-a7f3-7cc3934bb8b0")}, s.err
}

func TestRegistrationGetRendersForm(t *testing.T) {
	handler := registrationHandler(&registrationStore{})
	response := httptest.NewRecorder()
	handler.Get(response, httptest.NewRequest(http.MethodGet, "/registo", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	for _, expected := range []string{`<body class="auth-body">`, `<h1 id="registration-title">Criar conta</h1>`, `autocomplete="email"`, `autocomplete="bday"`, `autocomplete="new-password"`, `id="password-help"`, `name="accept_terms"`, `name="accept_image_use"`, `href="https://example.test/termos"`, `href="https://example.test/imagem"`, `href="/login">Iniciar sessão</a>`} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Errorf("body does not contain %q", expected)
		}
	}
}

func TestRegistrationPostValidatesBeforeStore(t *testing.T) {
	store := &registrationStore{}
	handler := registrationHandler(store)
	form := validRegistrationForm()
	form.Set("date_of_birth", "2020-01-01")
	form.Del("accept_terms")
	response := submitRegistration(t, handler, form)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", response.Code)
	}
	if store.called {
		t.Fatal("store called for invalid registration")
	}
	for _, expected := range []string{"Tem de ter pelo menos 18 anos", "Tem de aceitar os termos gerais"} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Errorf("body does not contain %q", expected)
		}
	}
	for _, expected := range []string{`value="Maria Silva"`, `value="member@example.com"`, `value="2020-01-01"`, `name="accept_image_use" type="checkbox" required checked`, `class="error-summary"`, `aria-invalid="true"`} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Errorf("validation response does not preserve shared contract %q", expected)
		}
	}
}

func TestRegistrationPostCreatesAccountAndSession(t *testing.T) {
	store := &registrationStore{}
	handler := registrationHandler(store)
	response := submitRegistration(t, handler, validRegistrationForm())
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/dashboard" {
		t.Fatalf("response = %d %q", response.Code, response.Header().Get("Location"))
	}
	if !store.called || store.input.Email != "member@example.com" {
		t.Fatalf("registration input = %+v", store.input)
	}
	if bcrypt.CompareHashAndPassword([]byte(store.input.PasswordHash), []byte("correct horse 7")) != nil {
		t.Fatal("password was not bcrypt hashed")
	}
	if store.input.TermsVersion != "1.0" || store.input.ImageVersion != "2.0" {
		t.Fatalf("consent versions = %q, %q", store.input.TermsVersion, store.input.ImageVersion)
	}
	if len(response.Result().Cookies()) == 0 {
		t.Fatal("successful registration did not set a session cookie")
	}
}

func TestRegistrationPostReportsDuplicateEmail(t *testing.T) {
	store := &registrationStore{err: &pgconn.PgError{Code: "23505"}}
	response := submitRegistration(t, registrationHandler(store), validRegistrationForm())
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), duplicateEmailMessage) {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
}

func registrationHandler(store RegistrationStore) Registration {
	return Registration{
		Store: store, Sessions: scs.New(), PageMeta: loginTestPageMeta(), Location: time.UTC,
		Now:          func() time.Time { return time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC) },
		TermsVersion: "1.0", TermsSHA256: strings.Repeat("a", 64), TermsURL: "https://example.test/termos", ImageVersion: "2.0", ImageSHA256: strings.Repeat("b", 64), ImageURL: "https://example.test/imagem",
	}
}

func validRegistrationForm() url.Values {
	return url.Values{"name": {"  Maria   Silva "}, "email": {" MEMBER@EXAMPLE.COM "}, "date_of_birth": {"1990-01-01"}, "password": {"correct horse 7"}, "password_confirmation": {"correct horse 7"}, "accept_terms": {"on"}, "accept_image_use": {"on"}}
}

func submitRegistration(t *testing.T, handler Registration, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/registo", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	handler.Sessions.LoadAndSave(http.HandlerFunc(handler.Post)).ServeHTTP(response, request)
	return response
}
