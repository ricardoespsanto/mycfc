package passwordreset

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/url"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/pbkdf2"
)

const (
	TokenBytes     = 32
	TokenLifetime  = time.Hour
	ResendInterval = time.Minute
	HourlyLimit    = 5
)

var (
	ErrInvalidToken = errors.New("invalid password reset token")
	ErrIneligible   = errors.New("account is not eligible for password reset")
	ErrTooSoon      = errors.New("password reset requested too recently")
	ErrRateLimited  = errors.New("password reset request limit exceeded")
)

type Store interface {
	FindPasswordResetAccountByEmail(context.Context, *string) (dbgen.FindPasswordResetAccountByEmailRow, error)
	CreatePasswordReset(context.Context, dbgen.CreatePasswordResetParams) (uuid.UUID, error)
	ResolvePasswordResetToken(context.Context, dbgen.ResolvePasswordResetTokenParams) (dbgen.ResolvePasswordResetTokenRow, error)
	ConsumePasswordResetToken(context.Context, dbgen.ConsumePasswordResetTokenParams) (uuid.UUID, error)
}

type Service struct {
	Store   Store
	BaseURL string
	Key     []byte
	Rand    io.Reader
	Now     func() time.Time
}

func (s Service) Issue(ctx context.Context, rawEmail string, throttle bool) (uuid.UUID, error) {
	email, err := validation.NormalizeEmail(rawEmail)
	if err != nil {
		return uuid.Nil, ErrIneligible
	}
	account, err := s.Store.FindPasswordResetAccountByEmail(ctx, &email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrIneligible
		}
		return uuid.Nil, err
	}

	raw := make([]byte, TokenBytes)
	if _, err := io.ReadFull(s.random(), raw); err != nil {
		return uuid.Nil, fmt.Errorf("generate password reset token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	digest := sha256.Sum256(raw)
	sealed, err := s.seal([]byte(s.Link(token)), account.Email)
	if err != nil {
		return uuid.Nil, err
	}
	now := s.now()
	id, err := s.Store.CreatePasswordReset(ctx, dbgen.CreatePasswordResetParams{
		UserID: account.ID, Email: account.Email, TokenDigest: digest[:], SealedPayload: sealed,
		CreatedAt: timestamp(now), ExpiresAt: timestamp(now.Add(TokenLifetime)), Throttle: throttle,
	})
	if err == nil {
		return id, nil
	}
	var postgresErr *pgconn.PgError
	if errors.As(err, &postgresErr) && postgresErr.Code == "P0001" {
		switch postgresErr.Message {
		case "password_reset_too_soon":
			return uuid.Nil, ErrTooSoon
		case "password_reset_limit_exceeded":
			return uuid.Nil, ErrRateLimited
		case "password_reset_ineligible":
			return uuid.Nil, ErrIneligible
		}
	}
	return uuid.Nil, err
}

func (s Service) Resolve(ctx context.Context, token string) (uuid.UUID, error) {
	digest, err := tokenDigest(token)
	if err != nil {
		return uuid.Nil, ErrInvalidToken
	}
	resolved, err := s.Store.ResolvePasswordResetToken(ctx, dbgen.ResolvePasswordResetTokenParams{TokenDigest: digest, ResolvedAt: timestamp(s.now())})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrInvalidToken
		}
		return uuid.Nil, err
	}
	return resolved.UserID, nil
}

func (s Service) Consume(ctx context.Context, token, password string) (uuid.UUID, error) {
	if err := validation.ValidatePassword(password); err != nil {
		return uuid.Nil, err
	}
	digest, err := tokenDigest(token)
	if err != nil {
		return uuid.Nil, ErrInvalidToken
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return uuid.Nil, fmt.Errorf("hash password: %w", err)
	}
	hashString := string(hash)
	userID, err := s.Store.ConsumePasswordResetToken(ctx, dbgen.ConsumePasswordResetTokenParams{TokenDigest: digest, PasswordHash: &hashString, ConsumedAt: timestamp(s.now())})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrInvalidToken
	}
	return userID, err
}

func (s Service) Link(token string) string {
	u, _ := url.Parse(s.BaseURL)
	u.Path = "/recuperar-palavra-passe/repor"
	u.RawQuery = url.Values{"token": {token}}.Encode()
	return u.String()
}

func (s Service) OpenDeliveryLink(sealed []byte, email string) (string, error) {
	block, err := aes.NewCipher(s.encryptionKey())
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(sealed) < gcm.NonceSize() {
		return "", errors.New("invalid password reset delivery payload")
	}
	plain, err := gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], deliveryAAD(email))
	if err != nil {
		return "", errors.New("invalid password reset delivery payload")
	}
	return string(plain), nil
}

func (s Service) seal(plain []byte, email string) ([]byte, error) {
	block, err := aes.NewCipher(s.encryptionKey())
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(s.random(), nonce); err != nil {
		return nil, fmt.Errorf("seal password reset delivery: %w", err)
	}
	return gcm.Seal(nonce, nonce, plain, deliveryAAD(email)), nil
}

func deliveryAAD(email string) []byte { return []byte("password-reset-link-v1\x00" + email) }

func tokenDigest(token string) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != TokenBytes {
		return nil, ErrInvalidToken
	}
	digest := sha256.Sum256(raw)
	return digest[:], nil
}

func (s Service) encryptionKey() []byte {
	return pbkdf2.Key([]byte(s.Key), []byte("mycfc/password-reset/outbox/v1\x00"), 100000, 32, sha1.New)
}

func (s Service) random() io.Reader {
	if s.Rand != nil {
		return s.Rand
	}
	return rand.Reader
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func timestamp(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }
