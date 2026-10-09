//go:build integration

package handlers

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	dated "github.com/cfcoimbra/mycfc/internal/db"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type publicationRaceFixture struct {
	pool                                                                *pgxpool.Pool
	actor, athlete, season, group, plan, session, membership, programme uuid.UUID
	today                                                               time.Time
	source                                                              time.Time
}

func newPublicationRaceFixture(t *testing.T, ctx context.Context) publicationRaceFixture {
	t.Helper()
	p, e := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(p.Close)
	f := publicationRaceFixture{pool: p, actor: uuid.New(), athlete: uuid.New(), season: uuid.New(), group: uuid.New(), plan: uuid.New(), session: uuid.New(), membership: uuid.New()}
	if e = p.QueryRow(ctx, `SELECT (now() AT TIME ZONE 'Europe/Lisbon')::date,(SELECT id FROM programmes WHERE code='Leisure')`).Scan(&f.today, &f.programme); e != nil {
		t.Fatal(e)
	}
	for _, id := range []uuid.UUID{f.actor, f.athlete} {
		if _, e = p.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Publication concurrency',$2,'hash','1990-01-01')`, id, uuid.NewString()+"@example.test"); e != nil {
			t.Fatal(e)
		}
	}
	start, end := f.today.AddDate(0, 0, -14), f.today.AddDate(0, 0, 30)
	for _, s := range []struct {
		q string
		a []any
	}{
		{`INSERT INTO seasons(id,code,name,starts_on,ends_on) VALUES($1,$2,'Publication concurrency',$3,$4)`, []any{f.season, "RC_" + uuid.NewString()[:8], start, end}},
		{`INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on,ends_on) VALUES($1,$2,$3,$4,$5,$6)`, []any{f.membership, f.athlete, f.season, f.programme, start, end}},
		{`INSERT INTO training_groups(id,name,programme_id,created_by_id) VALUES($1,'Publication concurrency',$2,$3)`, []any{f.group, f.programme, f.actor}},
		{`INSERT INTO training_group_members(group_id,membership_id,added_by_id) VALUES($1,$2,$3)`, []any{f.group, f.membership, f.actor}},
		{`INSERT INTO training_plans(id,title,programme_id,training_group_id,season_id,week_start,created_by_id) VALUES($1,'Publication concurrency',$2,$3,$4,$5,$6)`, []any{f.plan, f.programme, f.group, f.season, f.today.AddDate(0, 0, -(int(f.today.Weekday())+6)%7), f.actor}},
		{`INSERT INTO training_sessions(id,plan_id,title,starts_at,ends_at,created_by_id) VALUES($1,$2,'Future',($3::date+time '00:30') AT TIME ZONE 'Europe/Lisbon',($3::date+time '01:30') AT TIME ZONE 'Europe/Lisbon',$4)`, []any{f.session, f.plan, f.today.AddDate(0, 0, 1), f.actor}},
	} {
		if _, e = p.Exec(ctx, s.q, s.a...); e != nil {
			t.Fatal(e)
		}
	}
	if e = p.QueryRow(ctx, `SELECT updated_at FROM training_plans WHERE id=$1`, f.plan).Scan(&f.source); e != nil {
		t.Fatal(e)
	}
	return f
}
func (f publicationRaceFixture) input() StructuredPublicationInput {
	return StructuredPublicationInput{PlanID: f.plan, PublishedByID: f.actor, SourceUpdatedAt: pgtype.Timestamptz{Time: f.source, Valid: true}, ChangeSummary: "Race", Prescriptions: []StructuredPrescriptionInput{{SessionID: f.session, MembershipID: f.membership, AthleteUserID: f.athlete, Snapshot: []byte(`{"schema_version":1}`), SnapshotSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}}
}

func TestPublicationAfterCommittedShorteningCannotPublish(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	f := newPublicationRaceFixture(t, ctx)
	tx, e := f.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `UPDATE user_memberships SET ends_on=$1,updated_at=now() WHERE id=$2`, f.today, f.membership); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() {
		_, err := (PostgresStructuredTrainingStore{Pool: f.pool}).PublishStructuredTrainingPlan(ctx, f.input())
		done <- err
	}()
	waitForRaceQuery(t, ctx, f.pool, `SELECT plan.id, plan.updated_at`, "transactionid")
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	if e = <-done; !errors.Is(e, errStructuredTrainingPublicationConflict) {
		t.Fatalf("stale publication not rejected: %v", e)
	}
	var n int
	if e = f.pool.QueryRow(ctx, `SELECT count(*) FROM training_plan_publications WHERE plan_id=$1`, f.plan).Scan(&n); e != nil || n != 0 {
		t.Fatalf("partial publication %d %v", n, e)
	}
}

func TestFullTransitionRacesPublication(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	f := newPublicationRaceFixture(t, ctx)
	if _, e := f.pool.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, f.actor); e != nil {
		t.Fatal(e)
	}
	var initiation uuid.UUID
	if e := f.pool.QueryRow(ctx, `SELECT id FROM programmes WHERE code='Initiation'`).Scan(&initiation); e != nil {
		t.Fatal(e)
	}
	// Force the publisher to hold the plan lock while the real service updates
	// the old interval in a separate SERIALIZABLE connection.
	if _, e := f.pool.Exec(ctx, `CREATE OR REPLACE FUNCTION publication_transition_gate() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.plan_id = '`+f.plan.String()+`'::uuid THEN PERFORM pg_advisory_xact_lock(7100340); END IF; RETURN NEW; END $$`); e != nil {
		t.Fatal(e)
	}
	if _, e := f.pool.Exec(ctx, `CREATE TRIGGER publication_transition_gate BEFORE INSERT ON training_plan_publications FOR EACH ROW EXECUTE FUNCTION publication_transition_gate()`); e != nil {
		t.Fatal(e)
	}
	defer func() {
		f.pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS publication_transition_gate ON training_plan_publications`)
		f.pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS publication_transition_gate()`)
	}()
	gate, e := f.pool.Acquire(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer gate.Release()
	if _, e = gate.Exec(ctx, `SELECT pg_advisory_lock(7100340)`); e != nil {
		t.Fatal(e)
	}
	defer gate.Exec(context.Background(), `SELECT pg_advisory_unlock(7100340)`)
	publishDone := make(chan error, 1)
	go func() {
		_, err := (PostgresStructuredTrainingStore{Pool: f.pool}).PublishStructuredTrainingPlan(ctx, f.input())
		publishDone <- err
	}()
	waitForRaceQuery(t, ctx, f.pool, `INSERT INTO training_plan_publications`, "advisory")
	transitionDone := make(chan error, 1)
	go func() {
		_, err := (dated.DatedParticipationService{Pool: f.pool}).TransitionNextDay(ctx, f.membership, dated.OrdinaryDatedAssignment{ActorID: f.actor, MemberID: f.athlete, SeasonID: f.season, ProgrammeID: initiation, StartsOn: f.today.AddDate(0, 0, 1)})
		transitionDone <- err
	}()
	waitForRaceQuery(t, ctx, f.pool, `UPDATE user_memberships SET ends_on`, "transactionid")
	if _, e = gate.Exec(ctx, `SELECT pg_advisory_unlock(7100340)`); e != nil {
		t.Fatal(e)
	}
	pubErr, transitionErr := <-publishDone, <-transitionDone
	t.Logf("publisher=%v; full transition=%v", pubErr, transitionErr)
	if pubErr == nil && transitionErr == nil {
		t.Fatal("published future recipient and completed full transition")
	}
	if pubErr != nil && !errors.Is(pubErr, errStructuredTrainingPublicationConflict) {
		t.Fatalf("unsafe publication error: %v", pubErr)
	}
	var count int
	var endsOn time.Time
	if e = f.pool.QueryRow(ctx, `SELECT count(*) FROM training_prescriptions WHERE membership_id=$1`, f.membership).Scan(&count); e != nil {
		t.Fatal(e)
	}
	if e = f.pool.QueryRow(ctx, `SELECT ends_on FROM user_memberships WHERE id=$1`, f.membership).Scan(&endsOn); e != nil {
		t.Fatal(e)
	}
	if count != 0 && endsOn.Before(f.today.AddDate(0, 0, 1)) {
		t.Fatalf("future prescription survived transition: %d %v", count, endsOn)
	}
}

func TestGroupMemberInsertAfterConcurrentShortening(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	f := newPublicationRaceFixture(t, ctx)
	if _, e := f.pool.Exec(ctx, `DELETE FROM training_group_members WHERE group_id=$1 AND membership_id=$2`, f.group, f.membership); e != nil {
		t.Fatal(e)
	}
	tx, e := f.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `UPDATE user_memberships SET ends_on=$1,updated_at=now() WHERE id=$2`, f.today.AddDate(0, 0, -1), f.membership); e != nil {
		t.Fatal(e)
	}
	type outcome struct {
		rows int64
		err  error
	}
	done := make(chan outcome, 1)
	go func() {
		rows, err := dbgen.New(f.pool).AddStructuredTrainingGroupMember(ctx, dbgen.AddStructuredTrainingGroupMemberParams{GroupID: f.group, MembershipID: f.membership, AddedByID: f.actor})
		done <- outcome{rows, err}
	}()
	waitForRaceQuery(t, ctx, f.pool, `INSERT INTO training_group_members`, "transactionid")
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	got := <-done
	if got.err != nil || got.rows != 0 {
		t.Fatalf("ended membership associated: %+v", got)
	}
	var n int
	if e = f.pool.QueryRow(ctx, `SELECT count(*) FROM training_group_members WHERE group_id=$1 AND membership_id=$2`, f.group, f.membership).Scan(&n); e != nil || n != 0 {
		t.Fatalf("ended association persisted %d %v", n, e)
	}
}
