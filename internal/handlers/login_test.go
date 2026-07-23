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
	user  dbgen.User
	err   error
	email string
}

func (l *loginLookup) GetActiveUserByEmail(_ context.Context, email *string) (dbgen.User, error) {
	if email != nil {
		l.email = *email
	}
	return l.user, l.err
}

func TestLoginGetRendersForm(t *testing.T) {
	sessions := scs.New()
	handler := Login{
		Sessions: sessions,
		PageMeta: loginTestPageMeta(),
	}
	response := httptest.NewRecorder()
	handler.Get(response, httptest.NewRequest(http.MethodGet, "/login?next=%2Fdashboard%2Fleisure", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	for _, expected := range []string{
		`<h1 id="login-title">Iniciar sessão</h1>`,
		`action="/login"`,
		`name="next" value="/dashboard/leisure"`,
		`autocomplete="email"`,
		`autocomplete="current-password"`,
	} {
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
	lookup := &loginLookup{user: dbgen.User{
		ID:           uuid.New(),
		PasswordHash: &passwordHash,
		Role:         "Leisure",
	}}
	sessions := scs.New()
	handler := Login{
		Users:       lookup,
		Sessions:    sessions,
		PageMeta:    loginTestPageMeta(),
		FailureWait: func(context.Context) {},
	}
	form := url.Values{
		"email":    {" MEMBER@EXAMPLE.COM "},
		"password": {password},
		"next":     {"/dashboard/leisure"},
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
	if got := sessions.GetString(ctx, "role"); got != "Leisure" {
		t.Fatalf("session role = %q", got)
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
		Users: &loginLookup{user: dbgen.User{
			ID:           uuid.New(),
			PasswordHash: &passwordHash,
			Role:         "Competitor",
		}},
		Sessions: scs.New(),
		PageMeta: loginTestPageMeta(),
	}
	form := url.Values{"email": {"member@example.com"}, "password": {password}}
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

func TestLoginPostReturnsGenericFailure(t *testing.T) {
	lookup := &loginLookup{err: pgx.ErrNoRows}
	handler := Login{
		Users:       lookup,
		Sessions:    scs.New(),
		PageMeta:    loginTestPageMeta(),
		FailureWait: func(context.Context) {},
	}
	form := url.Values{"email": {"nobody@example.com"}, "password": {"incorrect password"}}
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
	form := url.Values{"email": {"member@example.com"}, "password": {"correct horse battery staple"}}
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
		Title:         "Iniciar sessão | MyCFC",
		StylesheetURL: "/assets/app.css",
		ScriptURL:     "/assets/app.js",
	}
}
