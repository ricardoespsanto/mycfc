//go:build integration

package db

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

func TestPasswordResetConcurrentIssuanceLeavesOnlyNewestActive(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	userID, email := insertPasswordResetUser(t, ctx, pool)
	queries := dbgen.New(pool)
	now := time.Now().UTC().Truncate(time.Microsecond)
	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for i := range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			digest := sha256.Sum256([]byte(fmt.Sprintf("concurrent-token-%d-%s", i, uuid.NewString())))
			_, err := queries.CreatePasswordReset(ctx, dbgen.CreatePasswordResetParams{
				UserID: userID, Email: email, TokenDigest: digest[:], SealedPayload: []byte{byte(i + 1)},
				CreatedAt: resetTimestamp(now.Add(time.Duration(i) * time.Microsecond)), ExpiresAt: resetTimestamp(now.Add(time.Hour)), Throttle: false,
			})
			results <- err
		}()
	}
	close(start)
	group.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	var active, cancelled int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM password_reset_tokens WHERE user_id = $1 AND consumed_at IS NULL`, userID).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM email_outbox outbox JOIN password_reset_tokens token ON token.id = outbox.password_reset_token_id WHERE token.user_id = $1 AND outbox.status = 'CANCELLED'`, userID).Scan(&cancelled); err != nil {
		t.Fatal(err)
	}
	if active != 1 || cancelled != 1 {
		t.Fatalf("active tokens = %d, cancelled deliveries = %d", active, cancelled)
	}
}

func TestPasswordResetConsumptionIsAtomicAndPreservesVerification(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	userID, email := insertPasswordResetUser(t, ctx, pool)
	queries := dbgen.New(pool)
	now := time.Now().UTC().Truncate(time.Microsecond)
	digest := sha256.Sum256([]byte("single-use-password-reset-token"))
	resetTokenID, err := queries.CreatePasswordReset(ctx, dbgen.CreatePasswordResetParams{UserID: userID, Email: email, TokenDigest: digest[:], SealedPayload: []byte("sealed"), CreatedAt: resetTimestamp(now), ExpiresAt: resetTimestamp(now.Add(time.Hour)), Throttle: false})
	if err != nil {
		t.Fatal(err)
	}
	var outboxMessageType, outboxEmail, outboxStatus string
	var outboxTokenID uuid.UUID
	var outboxPayload []byte
	if err := pool.QueryRow(ctx, `SELECT outbox.message_type, outbox.password_reset_token_id, token.email, outbox.sealed_payload, outbox.status
		FROM email_outbox outbox
		JOIN password_reset_tokens token ON token.id = outbox.password_reset_token_id
		WHERE outbox.password_reset_token_id = $1`, resetTokenID).
		Scan(&outboxMessageType, &outboxTokenID, &outboxEmail, &outboxPayload, &outboxStatus); err != nil {
		t.Fatal(err)
	}
	if outboxMessageType != "PASSWORD_RESET" || outboxTokenID != resetTokenID || outboxEmail != email || string(outboxPayload) != "sealed" || outboxStatus != "PENDING" {
		t.Fatalf("password reset outbox = type %q, token %s, email %q, payload %q, status %q", outboxMessageType, outboxTokenID, outboxEmail, outboxPayload, outboxStatus)
	}
	var versionBefore int64
	if err := pool.QueryRow(ctx, `SELECT credential_version FROM users WHERE id = $1`, userID).Scan(&versionBefore); err != nil || versionBefore != 1 {
		t.Fatalf("version after request = %d, err = %v", versionBefore, err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("nova palavra 7"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	hashString := string(hash)
	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			_, err := queries.ConsumePasswordResetToken(ctx, dbgen.ConsumePasswordResetTokenParams{TokenDigest: digest[:], PasswordHash: &hashString, ConsumedAt: resetTimestamp(now.Add(time.Minute))})
			results <- err
		}()
	}
	close(start)
	group.Wait()
	close(results)
	successes, missing := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, pgx.ErrNoRows):
			missing++
		default:
			t.Fatal(err)
		}
	}
	if successes != 1 || missing != 1 {
		t.Fatalf("consume results = %d success, %d missing", successes, missing)
	}
	var storedHash string
	var credentialVersion int64
	var verifiedAt pgtype.Timestamptz
	if err := pool.QueryRow(ctx, `SELECT password_hash, email_verified_at, credential_version FROM users WHERE id = $1`, userID).Scan(&storedHash, &verifiedAt, &credentialVersion); err != nil {
		t.Fatal(err)
	}
	if storedHash != hashString || !verifiedAt.Valid || credentialVersion != 2 {
		t.Fatalf("password/verification/version = %q, %#v, %d", storedHash, verifiedAt, credentialVersion)
	}
	var consumedOutboxStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM email_outbox outbox JOIN password_reset_tokens token ON token.id = outbox.password_reset_token_id WHERE token.user_id = $1`, userID).Scan(&consumedOutboxStatus); err != nil {
		t.Fatal(err)
	}
	if consumedOutboxStatus != "CANCELLED" {
		t.Fatalf("consumed token outbox status = %q", consumedOutboxStatus)
	}
}

func TestPasswordResetExpiryAndSupersessionRejectWithoutSleeping(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	userID, email := insertPasswordResetUser(t, ctx, pool)
	queries := dbgen.New(pool)
	now := time.Now().UTC().Truncate(time.Microsecond)
	firstDigest := sha256.Sum256([]byte("superseded-" + uuid.NewString()))
	secondDigest := sha256.Sum256([]byte("expired-" + uuid.NewString()))
	for index, digest := range [][sha256.Size]byte{firstDigest, secondDigest} {
		createdAt := now.Add(time.Duration(index) * time.Minute)
		if _, err := queries.CreatePasswordReset(ctx, dbgen.CreatePasswordResetParams{
			UserID: userID, Email: email, TokenDigest: digest[:], SealedPayload: []byte("sealed"),
			CreatedAt: resetTimestamp(createdAt), ExpiresAt: resetTimestamp(createdAt.Add(time.Hour)), Throttle: false,
		}); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := queries.ResolvePasswordResetToken(ctx, dbgen.ResolvePasswordResetTokenParams{
		TokenDigest: firstDigest[:], ResolvedAt: resetTimestamp(now.Add(2 * time.Minute)),
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("superseded token resolve error = %v", err)
	}
	if _, err := queries.ResolvePasswordResetToken(ctx, dbgen.ResolvePasswordResetTokenParams{
		TokenDigest: secondDigest[:], ResolvedAt: resetTimestamp(now.Add(2 * time.Hour)),
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expired token resolve error = %v", err)
	}
	var credentialVersion int64
	if err := pool.QueryRow(ctx, `SELECT credential_version FROM users WHERE id = $1`, userID).Scan(&credentialVersion); err != nil || credentialVersion != 1 {
		t.Fatalf("credential version after rejected links = %d, err = %v", credentialVersion, err)
	}
}

func TestPasswordResetThrottleRollingLimitAndEligibilityCancellation(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	userID, email := insertPasswordResetUser(t, ctx, pool)
	queries := dbgen.New(pool)
	now := time.Now().UTC().Truncate(time.Microsecond)
	var lastDigest [sha256.Size]byte
	issue := func(at time.Time, throttle bool) error {
		digest := sha256.Sum256([]byte(uuid.NewString()))
		_, err := queries.CreatePasswordReset(ctx, dbgen.CreatePasswordResetParams{UserID: userID, Email: email, TokenDigest: digest[:], SealedPayload: []byte("sealed"), CreatedAt: resetTimestamp(at), ExpiresAt: resetTimestamp(at.Add(time.Hour)), Throttle: throttle})
		if err == nil {
			lastDigest = digest
		}
		return err
	}
	if err := issue(now, true); err != nil {
		t.Fatal(err)
	}
	assertPasswordResetDatabaseError(t, issue(now.Add(30*time.Second), true), "password_reset_too_soon")
	for minute := 2; minute <= 5; minute++ {
		if err := issue(now.Add(time.Duration(minute)*time.Minute), true); err != nil {
			t.Fatalf("issuance %d: %v", minute, err)
		}
	}
	assertPasswordResetDatabaseError(t, issue(now.Add(6*time.Minute), true), "password_reset_limit_exceeded")
	assertPasswordResetDatabaseError(t, issue(now.Add(7*time.Minute), false), "password_reset_limit_exceeded")

	if _, err := pool.Exec(ctx, `UPDATE users SET email = $2 WHERE id = $1`, userID, "changed-"+email); err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CancelUndeliverableEmailOutbox(ctx, resetTimestamp(now.Add(8*time.Minute))); err != nil {
		t.Fatal(err)
	}
	var cancelled int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM email_outbox outbox JOIN password_reset_tokens token ON token.id = outbox.password_reset_token_id WHERE token.user_id = $1 AND outbox.status = 'CANCELLED'`, userID).Scan(&cancelled); err != nil {
		t.Fatal(err)
	}
	if cancelled != 5 {
		t.Fatalf("cancelled password reset deliveries = %d, want 5", cancelled)
	}
	if _, err := queries.ResolvePasswordResetToken(ctx, dbgen.ResolvePasswordResetTokenParams{TokenDigest: lastDigest[:], ResolvedAt: resetTimestamp(now.Add(8 * time.Minute))}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("ineligible resolve error = %v", err)
	}

	inactiveID, inactiveEmail := insertPasswordResetUser(t, ctx, pool)
	inactiveDigest := sha256.Sum256([]byte("inactive-account-token" + uuid.NewString()))
	if _, err := queries.CreatePasswordReset(ctx, dbgen.CreatePasswordResetParams{UserID: inactiveID, Email: inactiveEmail, TokenDigest: inactiveDigest[:], SealedPayload: []byte("sealed"), CreatedAt: resetTimestamp(now), ExpiresAt: resetTimestamp(now.Add(time.Hour)), Throttle: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET is_active = false WHERE id = $1`, inactiveID); err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CancelUndeliverableEmailOutbox(ctx, resetTimestamp(now.Add(time.Minute))); err != nil {
		t.Fatal(err)
	}
	var inactiveStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM email_outbox outbox JOIN password_reset_tokens token ON token.id = outbox.password_reset_token_id WHERE token.user_id = $1`, inactiveID).Scan(&inactiveStatus); err != nil {
		t.Fatal(err)
	}
	if inactiveStatus != "CANCELLED" {
		t.Fatalf("inactive account outbox status = %q", inactiveStatus)
	}
}

func insertPasswordResetUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (uuid.UUID, string) {
	t.Helper()
	userID := uuid.New()
	email := "password-reset-" + uuid.NewString() + "@example.test"
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, name, email, email_verified_at, password_hash, date_of_birth) VALUES ($1, 'Pessoa de recuperação', $2, now(), 'old-hash', '1990-01-01')`, userID, email); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, userID) })
	return userID, email
}

func assertPasswordResetDatabaseError(t *testing.T, err error, message string) {
	t.Helper()
	var postgresErr *pgconn.PgError
	if !errors.As(err, &postgresErr) || postgresErr.Code != "P0001" || postgresErr.Message != message {
		t.Fatalf("database error = %v, want %q", err, message)
	}
}

func resetTimestamp(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}
