package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/emailverification"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type verificationStoreFake struct {
	token dbgen.EmailVerificationToken
}

func (f *verificationStoreFake) CreateEmailVerification(_ context.Context, input dbgen.CreateEmailVerificationParams) (uuid.UUID, error) {
	return uuid.New(), nil
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

func TestEmailVerificationConfirmRejectsInvalidSignature(t *testing.T) {
	handler := EmailVerification{Service: emailverification.Service{Store: &verificationStoreFake{}, Key: []byte("0123456789abcdef0123456789abcdef")}, PageMeta: components.PageMeta{}}
	response := httptest.NewRecorder()
	handler.Confirm(response, httptest.NewRequest(http.MethodGet, "/verificar-email?id="+uuid.NewString()+"&signature=wrong", nil))
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "Não foi possível confirmar") {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
}
