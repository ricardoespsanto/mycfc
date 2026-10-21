//go:build integration

package db

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/releasecontract"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TEST_RESET_OUTBOX_URL is an empty, disposable PG cluster with database mycfc.
// Never point it at a populated/shared database: fresh init is empty-only.
func TestTokenOutboxClaimsUnderIsolatedRuntimeRole(t *testing.T) {
	raw := os.Getenv("TEST_RESET_OUTBOX_URL")
	if raw == "" {
		t.Skip("requires disposable empty PostgreSQL for runtime-role proof")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	var name string
	if err := admin.QueryRow(ctx, "SELECT current_database()").Scan(&name); err != nil || name != releasecontract.Database {
		t.Fatalf("dedicated database required: %v", err)
	}
	var empty bool
	if err := admin.QueryRow(ctx, "SELECT to_regclass('public.users') IS NULL").Scan(&empty); err != nil || !empty {
		t.Fatalf("dedicated empty database required: %v", err)
	}
	roles := RoleCredentials{AppUsername: releasecontract.WebRole, AppPassword: "synthetic-app-password", MigrationUsername: "reset_fixture_migration", MigrationPassword: "synthetic-migration-password", MediaCleanupUsername: "reset_fixture_media", MediaCleanupPassword: "synthetic-media-password", DataRetentionUsername: "reset_fixture_retention", DataRetentionPassword: "synthetic-retention-password"}
	if err := FreshInitializeDatabase(ctx, admin, name, roles); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	parsed.User = url.UserPassword(roles.AppUsername, roles.AppPassword)
	web, err := pgxpool.New(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	defer web.Close()
	var currentUser string
	if err := web.QueryRow(ctx, "SELECT current_user").Scan(&currentUser); err != nil || currentUser != releasecontract.WebRole {
		t.Fatalf("runtime role mismatch: %v", err)
	}
	for _, relation := range []string{"guardian_authority_renewal_requests", "guardian_authority_renewal_reminders"} {
		var allowed bool
		if err := web.QueryRow(ctx, "SELECT has_table_privilege(current_user,$1,'SELECT')", relation).Scan(&allowed); err != nil || allowed {
			t.Fatalf("unexpected guardian SELECT on %s: %v", relation, err)
		}
		var count int
		var pgerr *pgconn.PgError
		if err := web.QueryRow(ctx, "SELECT count(*) FROM "+relation).Scan(&count); !errors.As(err, &pgerr) || pgerr.Code != "42501" {
			t.Fatalf("guardian access did not reject for %s: %v", relation, err)
		}
	}
	q := dbgen.New(web)
	now := time.Now().UTC().Truncate(time.Microsecond)
	claimAt := resetTimestamp(now)
	params := dbgen.ClaimTokenEmailOutboxParams{ClaimedAt: claimAt, StaleBefore: resetTimestamp(now.Add(-5 * time.Minute))}
	if _, err := q.ClaimEmailOutbox(ctx, dbgen.ClaimEmailOutboxParams{ClaimedAt: claimAt, StaleBefore: params.StaleBefore}); err == nil {
		t.Fatal("guardian-only path unexpectedly available under web role")
	}
	if _, err := q.CancelUndeliverableEmailOutbox(ctx, claimAt); err == nil {
		t.Fatal("guardian-only cancellation unexpectedly available under web role")
	}
	if n, err := q.CancelUndeliverableTokenEmailOutbox(ctx, claimAt); err != nil || n != 0 {
		t.Fatalf("token cancellation: %d %v", n, err)
	}

	userID, email := insertPasswordResetUser(t, ctx, admin)
	digest := sha256.Sum256([]byte(uuid.NewString()))
	tokenID, err := dbgen.New(admin).CreatePasswordReset(ctx, dbgen.CreatePasswordResetParams{UserID: userID, Email: email, TokenDigest: digest[:], SealedPayload: []byte("synthetic-sealed"), CreatedAt: claimAt, ExpiresAt: resetTimestamp(now.Add(time.Hour)), Throttle: false})
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			item, err := q.ClaimTokenEmailOutbox(ctx, params)
			if err == nil && (item.MessageType != "PASSWORD_RESET" || item.PasswordResetTokenID == nil || *item.PasswordResetTokenID != tokenID || item.Email != email || item.UserID != userID || item.Attempts != 1 || string(item.SealedPayload) != "synthetic-sealed") {
				err = errors.New("claim projection mismatch")
			}
			results <- err
		}()
	}
	group.Wait()
	close(results)
	success, emptyClaims := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, pgx.ErrNoRows) {
			emptyClaims++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || emptyClaims != 1 {
		t.Fatalf("concurrent claims: %d success %d empty", success, emptyClaims)
	}
	var status string
	var attempts int
	if err := admin.QueryRow(ctx, "SELECT status,attempts FROM email_outbox WHERE password_reset_token_id=$1", tokenID).Scan(&status, &attempts); err != nil || status != "SENDING" || attempts != 1 {
		t.Fatalf("outbox after claim %s/%d: %v", status, attempts, err)
	}
	// A still-valid stale lease is reclaimable; a new lease is not.
	later := now.Add(time.Minute)
	params.ClaimedAt = resetTimestamp(later)
	params.StaleBefore = resetTimestamp(now.Add(time.Second))
	item, err := q.ClaimTokenEmailOutbox(ctx, params)
	if err != nil || item.ID == uuid.Nil || item.Attempts != 2 {
		t.Fatalf("stale lease not recovered: %v", err)
	}
	if _, err := q.ClaimTokenEmailOutbox(ctx, params); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("new lease double-claimed: %v", err)
	}

	// Latest request revokes the previous token and cancels its outbox.
	laterDigest := sha256.Sum256([]byte(uuid.NewString()))
	newerID, err := dbgen.New(admin).CreatePasswordReset(ctx, dbgen.CreatePasswordResetParams{UserID: userID, Email: email, TokenDigest: laterDigest[:], SealedPayload: []byte("synthetic-new"), CreatedAt: resetTimestamp(later.Add(time.Second)), ExpiresAt: resetTimestamp(later.Add(time.Hour)), Throttle: false})
	if err != nil {
		t.Fatal(err)
	}
	params.ClaimedAt = resetTimestamp(later.Add(2 * time.Second))
	item, err = q.ClaimTokenEmailOutbox(ctx, params)
	if err != nil || item.PasswordResetTokenID == nil || *item.PasswordResetTokenID != newerID || item.Attempts != 1 {
		t.Fatalf("latest valid reset not claimed: %v", err)
	}
	if err := admin.QueryRow(ctx, "SELECT status FROM email_outbox WHERE password_reset_token_id=$1", tokenID).Scan(&status); err != nil || status != "CANCELLED" {
		t.Fatalf("revoked outbox status: %s %v", status, err)
	}
	if _, err := admin.Exec(ctx, "UPDATE users SET email=$1 WHERE id=$2", "new-"+email, userID); err != nil {
		t.Fatal(err)
	}
	if n, err := q.CancelUndeliverableTokenEmailOutbox(ctx, resetTimestamp(later.Add(2*time.Second))); err != nil || n != 1 {
		t.Fatalf("stale recipient cancellation: %d %v", n, err)
	}
	if _, err := q.ClaimTokenEmailOutbox(ctx, params); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("changed recipient claimed: %v", err)
	}
	if err := admin.QueryRow(ctx, "SELECT status FROM email_outbox WHERE password_reset_token_id=$1", newerID).Scan(&status); err != nil || status != "CANCELLED" {
		t.Fatalf("changed recipient outbox: %s %v", status, err)
	}
	// Expiry and revocation must reject even before the cancellation sweep.
	newEmail := "new-" + email
	expiredDigest := sha256.Sum256([]byte(uuid.NewString()))
	expiredID, err := dbgen.New(admin).CreatePasswordReset(ctx, dbgen.CreatePasswordResetParams{UserID: userID, Email: newEmail, TokenDigest: expiredDigest[:], SealedPayload: []byte("synthetic-expired"), CreatedAt: resetTimestamp(later.Add(3 * time.Second)), ExpiresAt: resetTimestamp(later.Add(4 * time.Second)), Throttle: false})
	if err != nil {
		t.Fatal(err)
	}
	params.ClaimedAt = resetTimestamp(later.Add(5 * time.Second))
	if _, err := q.ClaimTokenEmailOutbox(ctx, params); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expired reset claimed: %v", err)
	}
	if n, err := q.CancelUndeliverableTokenEmailOutbox(ctx, params.ClaimedAt); err != nil || n != 1 {
		t.Fatalf("expired cancellation: %d %v", n, err)
	}
	if err := admin.QueryRow(ctx, "SELECT status FROM email_outbox WHERE password_reset_token_id=$1", expiredID).Scan(&status); err != nil || status != "CANCELLED" {
		t.Fatalf("expired outbox: %s %v", status, err)
	}
	consumedDigest := sha256.Sum256([]byte(uuid.NewString()))
	consumedID, err := dbgen.New(admin).CreatePasswordReset(ctx, dbgen.CreatePasswordResetParams{UserID: userID, Email: newEmail, TokenDigest: consumedDigest[:], SealedPayload: []byte("synthetic-revoked"), CreatedAt: resetTimestamp(later.Add(6 * time.Second)), ExpiresAt: resetTimestamp(later.Add(time.Hour)), Throttle: false})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "UPDATE password_reset_tokens SET consumed_at=$1 WHERE id=$2", later.Add(7*time.Second), consumedID); err != nil {
		t.Fatal(err)
	}
	params.ClaimedAt = resetTimestamp(later.Add(8 * time.Second))
	if _, err := q.ClaimTokenEmailOutbox(ctx, params); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("revoked reset claimed: %v", err)
	}
	if n, err := q.CancelUndeliverableTokenEmailOutbox(ctx, params.ClaimedAt); err != nil || n != 1 {
		t.Fatalf("revoked cancellation: %d %v", n, err)
	}
	if err := admin.QueryRow(ctx, "SELECT status FROM email_outbox WHERE password_reset_token_id=$1", consumedID).Scan(&status); err != nil || status != "CANCELLED" {
		t.Fatalf("revoked outbox: %s %v", status, err)
	}
}
