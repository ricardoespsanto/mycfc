package emailverification

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/url"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	TokenLifetime  = 24 * time.Hour
	ResendInterval = time.Minute
)

var (
	ErrInvalidToken = errors.New("invalid email verification token")
	ErrTooSoon      = errors.New("email verification requested too recently")
)

type Store interface {
	CreateEmailVerification(context.Context, dbgen.CreateEmailVerificationParams) (uuid.UUID, error)
	GetEmailVerificationToken(context.Context, uuid.UUID) (dbgen.EmailVerificationToken, error)
	ConsumeEmailVerification(context.Context, dbgen.ConsumeEmailVerificationParams) (uuid.UUID, error)
}

type Service struct {
	Store   Store
	BaseURL string
	Key     []byte
	Now     func() time.Time
}

func (s Service) Issue(ctx context.Context, userID uuid.UUID, email string, throttle bool) (uuid.UUID, error) {
	now := s.now()
	result, err := s.Store.CreateEmailVerification(ctx, dbgen.CreateEmailVerificationParams{
		UserID: userID, Email: email,
		CreatedAt: timestamp(now), ExpiresAt: timestamp(now.Add(TokenLifetime)), Throttle: throttle,
	})
	var postgresErr *pgconn.PgError
	if errors.As(err, &postgresErr) && postgresErr.Code == "P0001" && postgresErr.Message == "email_verification_too_soon" {
		return uuid.Nil, ErrTooSoon
	}
	return result, err
}

func (s Service) Verify(ctx context.Context, rawID, signature string) (uuid.UUID, error) {
	id, err := uuid.Parse(rawID)
	if err != nil || !hmac.Equal([]byte(signature), []byte(s.Signature(id))) {
		return uuid.Nil, ErrInvalidToken
	}
	token, err := s.Store.GetEmailVerificationToken(ctx, id)
	if err != nil || token.ConsumedAt.Valid || !token.ExpiresAt.Valid || !token.ExpiresAt.Time.After(s.now()) {
		return uuid.Nil, ErrInvalidToken
	}
	verifiedID, err := s.Store.ConsumeEmailVerification(ctx, dbgen.ConsumeEmailVerificationParams{ID: id, ConsumedAt: timestamp(s.now())})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrInvalidToken
		}
		return uuid.Nil, err
	}
	return verifiedID, nil
}

func (s Service) Link(id uuid.UUID) string {
	u, _ := url.Parse(s.BaseURL)
	u.Path = "/verificar-email"
	u.RawQuery = url.Values{"id": {id.String()}, "signature": {s.Signature(id)}}.Encode()
	return u.String()
}

func (s Service) Signature(id uuid.UUID) string {
	mac := hmac.New(sha256.New, s.Key)
	_, _ = mac.Write([]byte(id.String()))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func timestamp(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }
