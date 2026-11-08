//go:build integration

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/alexedwards/scs/v2"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/handlers"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The container image's /app/admin creates this synthetic account first.
// This test then uses the actual HTTP login handler and the application's
// active-account and ADMIN lookup on the same disposable PostgreSQL database.
func TestFreshImageAdminLogin(t *testing.T) {
	dbURL, email, passwordFile := os.Getenv("TEST_FRESH_ADMIN_URL"), os.Getenv("TEST_FRESH_ADMIN_EMAIL"), os.Getenv("TEST_FRESH_ADMIN_PASSWORD_FILE")
	if dbURL == "" || email == "" || passwordFile == "" {
		t.Skip("requires disposable image-provisioned administrator")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := dbgen.New(pool)
	account, err := store.GetAccountByEmail(ctx, &email)
	if err != nil {
		t.Fatal(err)
	}
	if !account.IsActive || account.IsDependent || !account.IsAdmin {
		t.Fatal("image provisioned account is not an active adult ADMIN")
	}
	passwordBytes, err := os.ReadFile(passwordFile)
	if err != nil {
		t.Fatal(err)
	}
	password := strings.TrimSpace(string(passwordBytes))
	sessions := scs.New()
	login := handlers.Login{Users: store, Sessions: sessions}
	form := url.Values{"identifier": {email}, "password": {password}}
	request := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	sessions.LoadAndSave(http.HandlerFunc(login.Post)).ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/dashboard" {
		t.Fatalf("provisioned login status=%d location=%q", response.Code, response.Header().Get("Location"))
	}
	wrong := url.Values{"identifier": {email}, "password": {password + "not-valid"}}
	request = httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(wrong.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response = httptest.NewRecorder()
	login.FailureWait = func(context.Context) {}
	sessions.LoadAndSave(http.HandlerFunc(login.Post)).ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid credential accepted: %d", response.Code)
	}
}
