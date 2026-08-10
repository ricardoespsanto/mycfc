package passwordreset

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"
)

type storeFake struct {
	account     dbgen.FindPasswordResetAccountByEmailRow
	findEmail   string
	findErr     error
	created     dbgen.CreatePasswordResetParams
	createErr   error
	resolved    dbgen.ResolvePasswordResetTokenParams
	resolveRow  dbgen.ResolvePasswordResetTokenRow
	resolveErr  error
	consumed    dbgen.ConsumePasswordResetTokenParams
	consumeUser uuid.UUID
	consumeErr  error
}

func (f *storeFake) FindPasswordResetAccountByEmail(_ context.Context, email *string) (dbgen.FindPasswordResetAccountByEmailRow, error) {
	f.findEmail = *email
	return f.account, f.findErr
}
func (f *storeFake) CreatePasswordReset(_ context.Context, input dbgen.CreatePasswordResetParams) (uuid.UUID, error) {
	f.created = input
	return uuid.New(), f.createErr
}
func (f *storeFake) ResolvePasswordResetToken(_ context.Context, input dbgen.ResolvePasswordResetTokenParams) (dbgen.ResolvePasswordResetTokenRow, error) {
	f.resolved = input
	return f.resolveRow, f.resolveErr
}
func (f *storeFake) ConsumePasswordResetToken(_ context.Context, input dbgen.ConsumePasswordResetTokenParams) (uuid.UUID, error) {
	f.consumed = input
	return f.consumeUser, f.consumeErr
}

func TestIssueGeneratesOpaqueDigestAndSealedDeliveryLink(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	userID := uuid.New()
	store := &storeFake{account: dbgen.FindPasswordResetAccountByEmailRow{ID: userID, Email: "member@example.test"}}
	random := bytes.Repeat([]byte{0x42}, TokenBytes+12)
	service := Service{Store: store, BaseURL: "https://mycfc.example", Key: []byte("0123456789abcdef0123456789abcdef"), Rand: bytes.NewReader(random), Now: func() time.Time { return now }}

	if _, err := service.Issue(context.Background(), " Member@EXAMPLE.TEST ", true); err != nil {
		t.Fatal(err)
	}
	if store.findEmail != "member@example.test" || store.created.UserID != userID || store.created.Email != "member@example.test" {
		t.Fatalf("account lookup/creation = %q, %#v", store.findEmail, store.created)
	}
	raw := bytes.Repeat([]byte{0x42}, TokenBytes)
	wantDigest := sha256.Sum256(raw)
	if !bytes.Equal(store.created.TokenDigest, wantDigest[:]) || len(store.created.TokenDigest) != sha256.Size {
		t.Fatalf("stored digest = %x", store.created.TokenDigest)
	}
	if bytes.Contains(store.created.SealedPayload, raw) || store.created.ExpiresAt.Time.Sub(now) != TokenLifetime || !store.created.Throttle {
		t.Fatalf("unsafe or invalid issuance = %#v", store.created)
	}
	link, err := service.OpenDeliveryLink(store.created.SealedPayload, store.created.Email)
	if err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	if link != "https://mycfc.example/recuperar-palavra-passe/repor?token="+token {
		t.Fatalf("delivery link = %q", link)
	}
	if _, err := service.OpenDeliveryLink(store.created.SealedPayload, "other@example.test"); err == nil {
		t.Fatal("delivery payload was not bound to its recipient")
	}
}

func TestIssueMapsEligibilityAndRateLimits(t *testing.T) {
	service := Service{Store: &storeFake{findErr: pgx.ErrNoRows}}
	if _, err := service.Issue(context.Background(), "unknown@example.test", true); !errors.Is(err, ErrIneligible) {
		t.Fatalf("unknown account error = %v", err)
	}
	if _, err := service.Issue(context.Background(), "not an email", true); !errors.Is(err, ErrIneligible) {
		t.Fatalf("malformed email error = %v", err)
	}
	for message, want := range map[string]error{"password_reset_too_soon": ErrTooSoon, "password_reset_limit_exceeded": ErrRateLimited, "password_reset_ineligible": ErrIneligible} {
		store := &storeFake{account: dbgen.FindPasswordResetAccountByEmailRow{ID: uuid.New(), Email: "member@example.test"}, createErr: &pgconn.PgError{Code: "P0001", Message: message}}
		service = Service{Store: store, BaseURL: "https://mycfc.example", Key: []byte("key"), Rand: bytes.NewReader(make([]byte, TokenBytes+12))}
		if _, err := service.Issue(context.Background(), "member@example.test", true); !errors.Is(err, want) {
			t.Fatalf("%s error = %v", message, err)
		}
	}
}

func TestResolveRejectsMalformedAndHashesValidToken(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	userID := uuid.New()
	store := &storeFake{resolveRow: dbgen.ResolvePasswordResetTokenRow{UserID: userID}, consumeUser: userID}
	service := Service{Store: store, Now: func() time.Time { return now }}
	for _, malformed := range []string{"", "%%%", base64.RawURLEncoding.EncodeToString(make([]byte, TokenBytes-1))} {
		if _, err := service.Resolve(context.Background(), malformed); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("Resolve(%q) error = %v", malformed, err)
		}
	}
	raw := bytes.Repeat([]byte{0x7a}, TokenBytes)
	token := base64.RawURLEncoding.EncodeToString(raw)
	resolved, err := service.Resolve(context.Background(), token)
	if err != nil || resolved != userID {
		t.Fatalf("Resolve() = %s, %v", resolved, err)
	}
	wantDigest := sha256.Sum256(raw)
	if !bytes.Equal(store.resolved.TokenDigest, wantDigest[:]) || !store.resolved.ResolvedAt.Time.Equal(now) {
		t.Fatalf("resolve params = %#v", store.resolved)
	}
	password := "segura palavra 7"
	consumed, err := service.Consume(context.Background(), token, password)
	if err != nil || consumed != userID {
		t.Fatalf("Consume() = %s, %v", consumed, err)
	}
	if store.consumed.PasswordHash == nil || bcrypt.CompareHashAndPassword([]byte(*store.consumed.PasswordHash), []byte(password)) != nil {
		t.Fatal("password was not bcrypt hashed")
	}
	if strings.Contains(*store.consumed.PasswordHash, password) || !bytes.Equal(store.consumed.TokenDigest, wantDigest[:]) {
		t.Fatal("consume parameters expose plaintext or use the wrong digest")
	}
}

func TestResolveAndConsumeMapMissingRowsToInvalidToken(t *testing.T) {
	raw := base64.RawURLEncoding.EncodeToString(make([]byte, TokenBytes))
	store := &storeFake{resolveErr: pgx.ErrNoRows, consumeErr: pgx.ErrNoRows}
	service := Service{Store: store}
	if _, err := service.Resolve(context.Background(), raw); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("resolve error = %v", err)
	}
	if _, err := service.Consume(context.Background(), raw, "segura palavra 7"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("consume error = %v", err)
	}
}
