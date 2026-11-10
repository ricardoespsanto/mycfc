//go:build integration

package handlers

import (
	"context"
	"errors"
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
	pool, web                                                           *pgxpool.Pool
	actor, athlete, season, group, plan, session, membership, programme uuid.UUID
	today                                                               time.Time
	source                                                              time.Time
}

func newPublicationRaceFixture(t *testing.T, ctx context.Context) publicationRaceFixture {
	t.Helper()
	_, p, current := currentTrainingFixture(t)
	f := publicationRaceFixture{pool: p, web: current.web, actor: current.actor, athlete: current.subject, season: current.season, group: current.group, plan: current.plan, session: current.session, membership: current.membership, programme: current.programme}
	if e := p.QueryRow(ctx, `SELECT (clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date`).Scan(&f.today); e != nil {
		t.Fatal(e)
	}
	if _, e := p.Exec(ctx, `UPDATE training_plans SET week_start=date_trunc('week',$1::date)::date WHERE id=$2`, f.today, f.plan); e != nil {
		t.Fatal(e)
	}
	if _, e := p.Exec(ctx, `UPDATE training_sessions SET starts_at=($1::date+time '00:30') AT TIME ZONE 'Europe/Lisbon',ends_at=($1::date+time '01:30') AT TIME ZONE 'Europe/Lisbon' WHERE id=$2`, f.today.AddDate(0, 0, 1), f.session); e != nil {
		t.Fatal(e)
	}
	if e := p.QueryRow(ctx, `SELECT updated_at FROM training_plans WHERE id=$1`, f.plan).Scan(&f.source); e != nil {
		t.Fatal(e)
	}
	return f
}
func (f publicationRaceFixture) input() publicationTestReview {
	return publicationTestReview{PlanID: f.plan, PublishedByID: f.actor, SourceUpdatedAt: pgtype.Timestamptz{Time: f.source, Valid: true}, ChangeSummary: "Race", Prescriptions: []StructuredPrescriptionInput{{SessionID: f.session, MembershipID: f.membership, AthleteUserID: f.athlete, Snapshot: []byte(`{"schema_version":1}`), SnapshotSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}}
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
		_, err := confirmPublicationTestReview(ctx, PostgresStructuredTrainingStore{Pool: f.web}, f.input())
		done <- err
	}()
	waitForRaceQuery(t, ctx, f.pool, `LockPublicationPlan`, "transactionid")
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	if e = <-done; !errors.Is(e, errStructuredTrainingPublicationConflict) {
		t.Fatalf("stale publication not rejected: %v", e)
	}
	var n int
	if e := f.pool.QueryRow(ctx, `SELECT count(*) FROM training_plan_publications WHERE plan_id=$1`, f.plan).Scan(&n); e != nil || n != 0 {
		t.Fatalf("partial publication %d %v", n, e)
	}
}

func TestFullTransitionRacesPublication(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	f := newPublicationRaceFixture(t, ctx)
	if _, e := f.pool.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN' ON CONFLICT DO NOTHING`, f.actor); e != nil {
		t.Fatal(e)
	}
	var initiation uuid.UUID
	if e := f.pool.QueryRow(ctx, `SELECT id FROM programmes WHERE code='Initiation'`).Scan(&initiation); e != nil {
		t.Fatal(e)
	}
	ctx, reached, resume := withPublicationTestGate(ctx)
	defer resume()
	publishDone := make(chan error, 1)
	go func() {
		_, err := confirmPublicationTestReview(ctx, PostgresStructuredTrainingStore{Pool: f.web}, f.input())
		publishDone <- err
	}()
	awaitPublicationTestGate(t, ctx, reached, publishDone)
	transitionDone := make(chan error, 1)
	go func() {
		_, err := (dated.DatedParticipationService{Pool: f.web}).TransitionNextDay(ctx, f.membership, dated.OrdinaryDatedAssignment{ActorID: f.actor, MemberID: f.athlete, SeasonID: f.season, ProgrammeID: initiation, StartsOn: f.today.AddDate(0, 0, 1)})
		transitionDone <- err
	}()
	// The current admitted protocol uses NOWAIT on the subject. The competing
	// transition must be refused while publication holds its locked proof.
	var transitionErr error
	select {
	case transitionErr = <-transitionDone:
		if !errors.Is(transitionErr, dated.ErrClassificationStale) {
			t.Fatalf("concurrent transition was not stale: %v", transitionErr)
		}
	case <-ctx.Done():
		t.Fatal("NOWAIT transition did not finish", ctx.Err())
	}
	resume()
	pubErr := <-publishDone
	t.Logf("publisher=%v; full transition=%v", pubErr, transitionErr)
	if pubErr == nil && transitionErr == nil {
		t.Fatal("published future recipient and completed full transition")
	}
	if pubErr != nil && !errors.Is(pubErr, errStructuredTrainingPublicationConflict) {
		t.Fatalf("unsafe publication error: %v", pubErr)
	}
	var count int
	var endsOn time.Time
	if e := f.pool.QueryRow(ctx, `SELECT count(*) FROM training_prescriptions WHERE membership_id=$1`, f.membership).Scan(&count); e != nil {
		t.Fatal(e)
	}
	if e := f.pool.QueryRow(ctx, `SELECT ends_on FROM user_memberships WHERE id=$1`, f.membership).Scan(&endsOn); e != nil {
		t.Fatal(e)
	}
	if count != 0 && endsOn.Before(f.today.AddDate(0, 0, 1)) {
		t.Fatalf("future prescription survived transition: %d %v", count, endsOn)
	}
}

func TestGroupMemberInsertAfterConcurrentShorteningCannotCreateFutureEligibility(t *testing.T) {
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
	if _, e = tx.Exec(ctx, `UPDATE user_memberships SET ends_on=$1,updated_at=now() WHERE id=$2`, f.today, f.membership); e != nil {
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
	if got.err != nil || got.rows != 1 {
		t.Fatalf("current-day compatibility association: %+v", got)
	}
	var n int
	if e := f.pool.QueryRow(ctx, `SELECT count(*) FROM training_group_members WHERE group_id=$1 AND membership_id=$2`, f.group, f.membership).Scan(&n); e != nil || n != 1 {
		t.Fatalf("compatibility association count %d %v", n, e)
	}
	// Undated compatibility rows are not publication authority. The current-day
	// association must not restore tomorrow's eligibility after shortening.
	rows, err := dbgen.New(f.web).PublicationEligibleRecipients(ctx, dbgen.PublicationEligibleRecipientsParams{ActorID: f.actor, PlanID: f.plan})
	if err != nil || len(rows) != 0 {
		t.Fatalf("shortened membership regained future eligibility: %v %v", rows, err)
	}

}
