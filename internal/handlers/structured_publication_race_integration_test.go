//go:build integration

package handlers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The first writer has updated the membership but is stalled by the plan row
// held by the publisher. A row lock on the membership must prevent publishing
// against the old version, rather than allowing both commits.
func TestPublicationRacesMembershipShortening(t *testing.T) {
	for _, iso := range []pgx.TxIsoLevel{pgx.Serializable, pgx.ReadCommitted} {
		t.Run(string(iso), func(t *testing.T) { testPublicationRacesMembershipShortening(t, iso) })
	}
}

func testPublicationRacesMembershipShortening(t *testing.T, iso pgx.TxIsoLevel) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	f := newPublicationRaceFixture(t, ctx)
	pool := f.pool
	actor, athlete, plan, session, membership, today, source := f.actor, f.athlete, f.plan, f.session, f.membership, f.today, f.source
	input := publicationTestReview{PlanID: plan, SourceUpdatedAt: pgtype.Timestamptz{Time: source, Valid: true}, PublishedByID: actor, ChangeSummary: "Race", Prescriptions: []StructuredPrescriptionInput{{SessionID: session, MembershipID: membership, AthleteUserID: athlete, Snapshot: []byte(`{"schema_version":1}`), SnapshotSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}}
	// Pause the real admitted transaction in Go; changing publication triggers
	// would correctly violate the runtime catalog and test a different failure.
	ctx, reached, resume := withPublicationTestGate(ctx)
	defer resume()
	publishDone := make(chan error, 1)
	go func() {
		_, e := confirmPublicationTestReview(ctx, PostgresStructuredTrainingStore{Pool: f.web}, input)
		publishDone <- e
	}()
	awaitPublicationTestGate(t, ctx, reached, publishDone)
	updateDone := make(chan error, 1)
	go func() {
		tx, e := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: iso})
		if e != nil {
			updateDone <- e
			return
		}
		defer tx.Rollback(ctx)
		_, e = tx.Exec(ctx, `UPDATE user_memberships SET ends_on=$1::date,updated_at=now() WHERE id=$2`, today, membership)
		if e == nil {
			e = tx.Commit(ctx)
		}
		updateDone <- e
	}()
	waitForRaceQuery(t, ctx, pool, `UPDATE user_memberships SET ends_on`, "transactionid")
	resume()
	pubErr, updateErr := <-publishDone, <-updateDone
	t.Logf("publisher=%v; shortening=%v", pubErr, updateErr)
	if pubErr == nil && updateErr == nil {
		t.Fatal("publication and shortening both committed")
	}
	if pubErr != nil && !errors.Is(pubErr, errStructuredTrainingPublicationConflict) {
		t.Fatalf("unsafe publisher error: %v", pubErr)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM training_prescriptions WHERE membership_id=$1`, membership).Scan(&count); err != nil {
		t.Fatal(err)
	}
	var endsOn time.Time
	if err := pool.QueryRow(ctx, `SELECT ends_on FROM user_memberships WHERE id=$1`, membership).Scan(&endsOn); err != nil {
		t.Fatal(err)
	}
	if count != 0 && endsOn.Before(today.AddDate(0, 0, 1)) {
		t.Fatalf("stale immutable prescription: %d ends %v", count, endsOn)
	}
	var headers int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM training_plan_publications WHERE plan_id=$1`, plan).Scan(&headers); err != nil {
		t.Fatal(err)
	}
	if (count == 0 && headers != 0) || (count != 0 && headers != 1) {
		t.Fatalf("partial publication: prescriptions=%d headers=%d", count, headers)
	}
}

func waitForRaceQuery(t *testing.T, ctx context.Context, pool *pgxpool.Pool, fragment, wait string) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var found bool
		err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE state='active' AND query LIKE '%'||$1||'%' AND wait_event=$2 AND pid<>pg_backend_pid())`, fragment, wait).Scan(&found)
		if err != nil {
			t.Fatal(err)
		}
		if found {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("writer did not reach %s wait: %v", wait, ctx.Err())
		case <-ticker.C:
		}
	}
}
