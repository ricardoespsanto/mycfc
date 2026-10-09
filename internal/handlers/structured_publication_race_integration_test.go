//go:build integration

package handlers

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
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
	pool, err := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	actor, athlete, season, group, plan, session, membership := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	var today time.Time
	var programme uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT (now() AT TIME ZONE 'Europe/Lisbon')::date,(SELECT id FROM programmes WHERE code='Leisure')`).Scan(&today, &programme); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{actor, athlete} {
		if _, err := pool.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Race fixture',$2,'hash','1990-01-01')`, id, uuid.NewString()+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	start, end := today.AddDate(0, 0, -14), today.AddDate(0, 0, 30)
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO seasons(id,code,name,starts_on,ends_on) VALUES($1,$2,'Race',$3,$4)`, []any{season, "RA_" + uuid.NewString()[:8], start, end}},
		{`INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on,ends_on) VALUES($1,$2,$3,$4,$5,$6)`, []any{membership, athlete, season, programme, start, end}},
		{`INSERT INTO training_groups(id,name,programme_id,created_by_id) VALUES($1,'Race',$2,$3)`, []any{group, programme, actor}},
		{`INSERT INTO training_group_members(group_id,membership_id,added_by_id) VALUES($1,$2,$3)`, []any{group, membership, actor}},
		{`INSERT INTO training_plans(id,title,programme_id,training_group_id,season_id,week_start,created_by_id) VALUES($1,'Race',$2,$3,$4,$5,$6)`, []any{plan, programme, group, season, today.AddDate(0, 0, -(int(today.Weekday())+6)%7), actor}},
		{`INSERT INTO training_sessions(id,plan_id,title,starts_at,ends_at,created_by_id) VALUES($1,$2,'Future',($3::date+time '00:30') AT TIME ZONE 'Europe/Lisbon',($3::date+time '01:30') AT TIME ZONE 'Europe/Lisbon',$4)`, []any{session, plan, today.AddDate(0, 0, 1), actor}},
	}
	for _, s := range statements {
		if _, err := pool.Exec(ctx, s.sql, s.args...); err != nil {
			t.Fatal(err)
		}
	}
	var source time.Time
	if err := pool.QueryRow(ctx, `SELECT updated_at FROM training_plans WHERE id=$1`, plan).Scan(&source); err != nil {
		t.Fatal(err)
	}
	input := StructuredPublicationInput{PlanID: plan, SourceUpdatedAt: pgtype.Timestamptz{Time: source, Valid: true}, PublishedByID: actor, ChangeSummary: "Race", Prescriptions: []StructuredPrescriptionInput{{SessionID: session, MembershipID: membership, AthleteUserID: athlete, Snapshot: []byte(`{"schema_version":1}`), SnapshotSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}}
	// Test-only gate after publisher has locked the plan and inserted the header.
	// The advisory lock holds the publisher while the serializable update takes
	// the membership row and waits on the plan-touch trigger.
	if _, err := pool.Exec(ctx, `CREATE OR REPLACE FUNCTION publication_race_gate() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.plan_id = '`+plan.String()+`'::uuid THEN PERFORM pg_advisory_xact_lock(7100339); END IF; RETURN NEW; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE TRIGGER publication_race_gate BEFORE INSERT ON training_plan_publications FOR EACH ROW EXECUTE FUNCTION publication_race_gate()`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS publication_race_gate ON training_plan_publications`)
		pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS publication_race_gate()`)
	}()
	gate, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Release()
	if _, err := gate.Exec(ctx, `SELECT pg_advisory_lock(7100339)`); err != nil {
		t.Fatal(err)
	}
	defer gate.Exec(context.Background(), `SELECT pg_advisory_unlock(7100339)`)
	publishDone := make(chan error, 1)
	go func() {
		_, e := (PostgresStructuredTrainingStore{Pool: pool}).PublishStructuredTrainingPlan(ctx, input)
		publishDone <- e
	}()
	waitForRaceQuery(t, ctx, pool, `INSERT INTO training_plan_publications`, "advisory")
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
	if _, err := gate.Exec(ctx, `SELECT pg_advisory_unlock(7100339)`); err != nil {
		t.Fatal(err)
	}
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
