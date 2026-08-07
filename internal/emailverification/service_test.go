package emailverification

import (
	"context"
	"errors"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

type serviceStoreFake struct {
	token      dbgen.EmailVerificationToken
	created    dbgen.CreateEmailVerificationParams
	createErr  error
	consumed   dbgen.ConsumeEmailVerificationParams
	consumeErr error
}

func (f *serviceStoreFake) CreateEmailVerification(_ context.Context, input dbgen.CreateEmailVerificationParams) (uuid.UUID, error) {
	f.created = input
	return uuid.New(), f.createErr
}
func (f *serviceStoreFake) GetEmailVerificationToken(context.Context, uuid.UUID) (dbgen.EmailVerificationToken, error) {
	return f.token, nil
}
func (f *serviceStoreFake) ConsumeEmailVerification(_ context.Context, input dbgen.ConsumeEmailVerificationParams) (uuid.UUID, error) {
	f.consumed = input
	return f.token.UserID, f.consumeErr
}

func TestServiceIssuesAndThrottlesVerification(t *testing.T) {
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	store := &serviceStoreFake{}
	service := Service{Store: store, Now: func() time.Time { return now }}
	userID := uuid.New()
	_, err := service.Issue(context.Background(), userID, "member@example.test", true)
	if err != nil {
		t.Fatal(err)
	}
	if store.created.UserID != userID || store.created.ExpiresAt.Time.Sub(now) != TokenLifetime {
		t.Fatalf("created verification = %#v", store.created)
	}
	store.createErr = &pgconn.PgError{Code: "P0001", Message: "email_verification_too_soon"}
	if _, err := service.Issue(context.Background(), userID, "member@example.test", true); !errors.Is(err, ErrTooSoon) {
		t.Fatalf("throttle error = %v", err)
	}
}

func TestServiceVerifiesSignedUnexpiredToken(t *testing.T) {
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	id, userID := uuid.New(), uuid.New()
	store := &serviceStoreFake{token: dbgen.EmailVerificationToken{ID: id, UserID: userID, ExpiresAt: timestamp(now.Add(time.Hour))}}
	service := Service{Store: store, BaseURL: "https://mycfc.example", Key: []byte("0123456789abcdef0123456789abcdef"), Now: func() time.Time { return now }}
	verified, err := service.Verify(context.Background(), id.String(), service.Signature(id))
	if err != nil || verified != userID {
		t.Fatalf("Verify() = %s, %v", verified, err)
	}
	if store.consumed.ID != id || !store.consumed.ConsumedAt.Time.Equal(now) {
		t.Fatalf("consumed = %#v", store.consumed)
	}
	if _, err := service.Verify(context.Background(), id.String(), "wrong"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("invalid signature error = %v", err)
	}
}

func TestServiceRejectsExpiredOrConsumedToken(t *testing.T) {
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	service := Service{BaseURL: "https://mycfc.example", Key: []byte("0123456789abcdef0123456789abcdef"), Now: func() time.Time { return now }}
	for _, token := range []dbgen.EmailVerificationToken{{ID: uuid.New(), ExpiresAt: timestamp(now.Add(-time.Second))}, {ID: uuid.New(), ExpiresAt: timestamp(now.Add(time.Hour)), ConsumedAt: timestamp(now)}} {
		store := &serviceStoreFake{token: token}
		service.Store = store
		if _, err := service.Verify(context.Background(), token.ID.String(), service.Signature(token.ID)); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("token %#v error = %v", token, err)
		}
	}
}
