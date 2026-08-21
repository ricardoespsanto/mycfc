package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/emailverification"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type verificationStoreFake struct {
	token       dbgen.EmailVerificationToken
	createInput dbgen.CreateEmailVerificationParams
	createErr   error
}

func (f *verificationStoreFake) CreateEmailVerification(_ context.Context, input dbgen.CreateEmailVerificationParams) (uuid.UUID, error) {
	f.createInput = input
	return uuid.New(), f.createErr
}
func (f *verificationStoreFake) GetEmailVerificationToken(context.Context, uuid.UUID) (dbgen.EmailVerificationToken, error) {
	return f.token, nil
}
func (f *verificationStoreFake) ConsumeEmailVerification(context.Context, dbgen.ConsumeEmailVerificationParams) (uuid.UUID, error) {
	return f.token.UserID, nil
}

func TestEmailVerificationConfirmRendersAnonymousSuccess(t *testing.T) {
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	id := uuid.New()
	store := &verificationStoreFake{token: dbgen.EmailVerificationToken{ID: id, UserID: uuid.New(), ExpiresAt: pgtype.Timestamptz{Time: now.Add(time.Hour), Valid: true}}}
	service := emailverification.Service{Store: store, BaseURL: "https://mycfc.example", Key: []byte("0123456789abcdef0123456789abcdef"), Now: func() time.Time { return now }}
	handler := EmailVerification{Service: service, PageMeta: components.PageMeta{StylesheetURL: "/assets/app.css"}}
	request := httptest.NewRequest(http.MethodGet, service.Link(id), nil)
	response := httptest.NewRecorder()
	handler.Confirm(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Email confirmado") {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
}

func TestEmailVerificationConfirmRedirectsTheVerifiedSignedInMember(t *testing.T) {
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	verifiedID, tokenID := uuid.New(), uuid.New()
	store := &verificationStoreFake{token: dbgen.EmailVerificationToken{ID: tokenID, UserID: verifiedID, ExpiresAt: pgtype.Timestamptz{Time: now.Add(time.Hour), Valid: true}}}
	service := emailverification.Service{Store: store, BaseURL: "https://mycfc.example", Key: []byte("0123456789abcdef0123456789abcdef"), Now: func() time.Time { return now }}
	request := httptest.NewRequest(http.MethodGet, service.Link(tokenID), nil)
	request = request.WithContext(context.WithValue(request.Context(), currentUserKey{}, CurrentUser{ID: verifiedID}))
	response := httptest.NewRecorder()

	EmailVerification{Service: service}.Confirm(response, request)

	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/perfil" {
		t.Fatalf("response = %d location=%q", response.Code, response.Header().Get("Location"))
	}
}

func TestEmailVerificationConfirmRejectsInvalidSignature(t *testing.T) {
	handler := EmailVerification{Service: emailverification.Service{Store: &verificationStoreFake{}, Key: []byte("0123456789abcdef0123456789abcdef")}, PageMeta: components.PageMeta{}}
	response := httptest.NewRecorder()
	handler.Confirm(response, httptest.NewRequest(http.MethodGet, "/verificar-email?id="+uuid.NewString()+"&signature=wrong", nil))
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "Não foi possível confirmar") {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
}

func TestEmailVerificationResendIssuesThrottledLinkForUnverifiedAdult(t *testing.T) {
	store := &verificationStoreFake{}
	handler := EmailVerification{Service: emailverification.Service{Store: store}}
	user := CurrentUser{ID: uuid.New(), Email: "ana@example.test"}
	request := httptest.NewRequest(http.MethodPost, "/verificar-email/reenvio", nil)
	request = request.WithContext(context.WithValue(request.Context(), currentUserKey{}, user))
	response := httptest.NewRecorder()

	handler.Resend(response, request)

	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/perfil" {
		t.Fatalf("response = %d location=%q", response.Code, response.Header().Get("Location"))
	}
	if store.createInput.UserID != user.ID || store.createInput.Email != user.Email || !store.createInput.Throttle {
		t.Fatalf("create input = %#v", store.createInput)
	}
}

func TestEmailVerificationResendRecognizesThrottleAndSkipsDependent(t *testing.T) {
	t.Run("throttled", func(t *testing.T) {
		store := &verificationStoreFake{createErr: &pgconn.PgError{Code: "P0001", Message: "email_verification_too_soon"}}
		handler := EmailVerification{Service: emailverification.Service{Store: store}}
		request := httptest.NewRequest(http.MethodPost, "/verificar-email/reenvio", nil)
		request = request.WithContext(context.WithValue(request.Context(), currentUserKey{}, CurrentUser{ID: uuid.New(), Email: "ana@example.test"}))
		response := httptest.NewRecorder()

		handler.Resend(response, request)

		if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/perfil" {
			t.Fatalf("response = %d location=%q", response.Code, response.Header().Get("Location"))
		}
	})

	t.Run("dependent", func(t *testing.T) {
		store := &verificationStoreFake{}
		handler := EmailVerification{Service: emailverification.Service{Store: store}}
		request := httptest.NewRequest(http.MethodPost, "/verificar-email/reenvio", nil)
		request = request.WithContext(context.WithValue(request.Context(), currentUserKey{}, CurrentUser{ID: uuid.New(), Email: "dependente@example.test", IsDependent: true}))
		response := httptest.NewRecorder()

		handler.Resend(response, request)

		if response.Code != http.StatusSeeOther || store.createInput.UserID != uuid.Nil {
			t.Fatalf("response = %d create input=%#v", response.Code, store.createInput)
		}
	})
}

func TestEmailVerificationResendRequiresAuthenticationAndFailsClosedForDeliveryErrors(t *testing.T) {
	t.Run("anonymous", func(t *testing.T) {
		response := httptest.NewRecorder()
		EmailVerification{}.Resend(response, httptest.NewRequest(http.MethodPost, "/verificar-email/reenvio", nil))
		if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/login" {
			t.Fatalf("response = %d location=%q", response.Code, response.Header().Get("Location"))
		}
	})

	t.Run("delivery failure", func(t *testing.T) {
		handler := EmailVerification{Service: emailverification.Service{Store: &verificationStoreFake{createErr: errors.New("outbox unavailable")}}, System: System{}}
		request := httptest.NewRequest(http.MethodPost, "/verificar-email/reenvio", nil)
		request = request.WithContext(context.WithValue(request.Context(), currentUserKey{}, CurrentUser{ID: uuid.New(), Email: "ana@example.test"}))
		response := httptest.NewRecorder()

		handler.Resend(response, request)

		if response.Code != http.StatusInternalServerError {
			t.Fatalf("response = %d", response.Code)
		}
	})
}
