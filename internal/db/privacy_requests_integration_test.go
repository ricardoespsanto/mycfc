//go:build integration

package db

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func privacyDB(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool, ctx
}
func privacyParams(user uuid.UUID) dbgen.CreatePrivacyRequestParams {
	now := time.Now().UTC().Truncate(time.Microsecond)
	return dbgen.CreatePrivacyRequestParams{PublicRef: uuid.New(), IdempotencyKey: uuid.New(), SubjectUserID: &user, RequesterUserID: &user, SubjectKind: "SELF", ScopeKind: "ACCOUNT_CLOSURE", Categories: []string{}, ReceivedAt: resetTimestamp(now), DueAt: resetTimestamp(now.AddDate(0, 1, 0))}
}
func requirePrivacyPGCode(t *testing.T, err error, code string) {
	t.Helper()
	var e *pgconn.PgError
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("want PostgreSQL %s, got %v", code, err)
	}
}
func TestPrivacyRequestAtomicReceiptAndMetadataAudit(t *testing.T) {
	pool, ctx := privacyDB(t)
	user, _ := insertPasswordResetUser(t, ctx, pool)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	q := dbgen.New(tx)
	params := privacyParams(user)
	r, err := q.CreatePrivacyRequest(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	if r.ID == r.PublicRef {
		t.Fatal("public reference exposes primary key")
	}
	event, err := q.AppendPrivacyRequestEvent(ctx, dbgen.AppendPrivacyRequestEventParams{RequestID: r.ID, ActorRole: "REQUESTER", ActorRef: uuid.New(), Action: "RECEIVED", ReasonCode: "REQUEST_RECEIVED", ToStatus: "RECEIVED", Version: 1, OccurredAt: params.ReceivedAt})
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `UPDATE data_erasure_request_events SET action='CANCELLED' WHERE id=$1`, event.ID)
	requirePrivacyPGCode(t, err, "P0001")
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = dbgen.New(pool).GetPrivacyRequest(ctx, r.ID)
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("rolled-back receipt persisted: %v", err)
	}
}
func TestPrivacyRequestDuplicateAndConcurrentVersion(t *testing.T) {
	pool, ctx := privacyDB(t)
	user, _ := insertPasswordResetUser(t, ctx, pool)
	q := dbgen.New(pool)
	p := privacyParams(user)
	r, err := q.CreatePrivacyRequest(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	// No events are committed here, allowing isolation cleanup without bypassing audit immutability.
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM data_erasure_requests WHERE id=$1`, r.ID) })
	_, err = q.CreatePrivacyRequest(ctx, p)
	requirePrivacyPGCode(t, err, "23505")
	p.PublicRef = uuid.New()
	p.IdempotencyKey = uuid.New()
	_, err = q.CreatePrivacyRequest(ctx, p)
	requirePrivacyPGCode(t, err, "23505")
	other, _ := insertPasswordResetUser(t, ctx, pool)
	_, err = q.GetPrivacyRequestByIdempotency(ctx, dbgen.GetPrivacyRequestByIdempotencyParams{RequesterUserID: &other, IdempotencyKey: r.IdempotencyKey})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("idempotency leaked other requester: %v", err)
	}
	updates := make(chan error, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, e := q.UpdatePrivacyRequest(ctx, dbgen.UpdatePrivacyRequestParams{ID: r.ID, ExpectedVersion: 1, Status: "RECEIVED", CategoryDecisions: []byte(`[]`), UpdatedAt: resetTimestamp(time.Now())})
			updates <- e
		}()
	}
	close(start)
	wg.Wait()
	close(updates)
	ok, stale := 0, 0
	for e := range updates {
		if e == nil {
			ok++
		} else if errors.Is(e, pgx.ErrNoRows) {
			stale++
		} else {
			t.Fatal(e)
		}
	}
	if ok != 1 || stale != 1 {
		t.Fatalf("version updates successes=%d stale=%d", ok, stale)
	}
	_, err = pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, user)
	requirePrivacyPGCode(t, err, "23503")
}
func TestPrivacyDecisionOutboxSurvivesAccountDisableAndDetachment(t *testing.T) {
	pool, ctx := privacyDB(t)
	user, _ := insertPasswordResetUser(t, ctx, pool)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	q := dbgen.New(tx)
	p := privacyParams(user)
	r, err := q.CreatePrivacyRequest(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	eventID := uuid.New()
	created := resetTimestamp(time.Now().AddDate(-10, 0, 0))
	id, err := q.EnqueuePrivacyRequestEmail(ctx, dbgen.EnqueuePrivacyRequestEmailParams{MessageType: "PRIVACY_DECISION", PrivacyRequestID: &r.ID, PrivacyRequesterID: &user, PrivacyEventKey: &eventID, SealedPayload: []byte("encrypted"), CreatedAt: created})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET is_active=false WHERE id=$1`, user); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE email_outbox SET privacy_requester_id=NULL WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	now := resetTimestamp(time.Now())
	if _, err = q.CancelUndeliverableEmailOutbox(ctx, now); err != nil {
		t.Fatal(err)
	}
	claimed, err := q.ClaimEmailOutbox(ctx, dbgen.ClaimEmailOutboxParams{ClaimedAt: now, StaleBefore: resetTimestamp(time.Now().Add(-time.Minute))})
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ID != id || claimed.MessageType != "PRIVACY_DECISION" || claimed.Email != "" || claimed.UserID != uuid.Nil || claimed.ExpiresAt.Valid {
		t.Fatalf("invalid private notice claim: %+v", claimed)
	}
	// event identity prevents duplicate notices transactionally.
	_, err = q.EnqueuePrivacyRequestEmail(ctx, dbgen.EnqueuePrivacyRequestEmailParams{MessageType: "PRIVACY_DECISION", PrivacyRequestID: &r.ID, PrivacyRequesterID: &user, PrivacyEventKey: &eventID, SealedPayload: []byte("encrypted"), CreatedAt: created})
	requirePrivacyPGCode(t, err, "23505")
}
func TestPrivacyAdoptedPolicyImmutableAndNoSeededActivation(t *testing.T) {
	pool, ctx := privacyDB(t)
	user, _ := insertPasswordResetUser(t, ctx, pool)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM privacy_request_activation`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("activation seeded: %d %v", count, err)
	}
	version := "integration-" + uuid.NewString()
	_, err = tx.Exec(ctx, `INSERT INTO privacy_request_policies(version,category_catalogue,adopted_at,adopted_by) VALUES($1,'[]',now(),$2)`, version, user)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `UPDATE privacy_request_policies SET category_catalogue='[{"category":"changed"}]' WHERE version=$1`, version)
	requirePrivacyPGCode(t, err, "P0001")
}
