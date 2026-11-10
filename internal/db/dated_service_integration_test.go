//go:build integration

package db

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestOrdinaryDatedParticipationService(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	actor, unauthorized, member, another, season, old := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{actor, unauthorized, member, another} {
		if _, err := pool.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Dated test',$2,'hash','1980-01-01')`, id, uuid.NewString()+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, actor); err != nil {
		t.Fatal(err)
	}
	var today time.Time
	if err := pool.QueryRow(ctx, `SELECT (now() AT TIME ZONE 'Europe/Lisbon')::date`).Scan(&today); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO seasons(id,code,name,starts_on,ends_on) VALUES($1,$2,'Dated service',$3,$4)`, season, "DS_"+uuid.NewString()[:8], today.AddDate(0, 0, -10), today.AddDate(0, 0, 60)); err != nil {
		t.Fatal(err)
	}
	var leisure, initiation uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM programmes WHERE code='Leisure'`).Scan(&leisure); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM programmes WHERE code='Initiation'`).Scan(&initiation); err != nil {
		t.Fatal(err)
	}
	service := DatedParticipationService{Pool: pool}
	input := OrdinaryDatedAssignment{ActorID: actor, MemberID: another, SeasonID: season, ProgrammeID: leisure, StartsOn: today}
	if _, err := service.CreateOrdinaryAssignment(ctx, OrdinaryDatedAssignment{ActorID: unauthorized, MemberID: another, SeasonID: season, ProgrammeID: leisure, StartsOn: today}); !errors.Is(err, ErrDatedParticipationForbidden) {
		t.Fatalf("unauthorized=%v", err)
	}
	backdated := input
	backdated.StartsOn = today.AddDate(0, 0, -1)
	if _, err := service.CreateOrdinaryAssignment(ctx, backdated); !errors.Is(err, ErrDatedParticipationWindow) {
		t.Fatalf("backdated=%v", err)
	}
	created, err := service.CreateOrdinaryAssignment(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateOrdinaryAssignment(ctx, input); !sqlState(err, "23P01") {
		t.Fatalf("overlap=%v", err)
	}
	var sameDayAffected int64
	if result, err := pool.Exec(ctx, `UPDATE user_memberships SET ends_on=(now() AT TIME ZONE 'Europe/Lisbon')::date-1 WHERE id=$1 AND starts_on<(now() AT TIME ZONE 'Europe/Lisbon')::date`, created.ID); err != nil {
		t.Fatal(err)
	} else {
		sameDayAffected = result.RowsAffected()
	}
	if sameDayAffected != 0 {
		t.Fatal("same-day reversal accepted")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on,ends_on) VALUES($1,$2,$3,$4,$5,$6)`, old, member, season, leisure, today.AddDate(0, 0, -5), today.AddDate(0, 0, 60)); err != nil {
		t.Fatal(err)
	}
	transition := OrdinaryDatedAssignment{ActorID: actor, MemberID: member, SeasonID: season, ProgrammeID: initiation, StartsOn: today.AddDate(0, 0, 1)}
	next, err := service.TransitionNextDay(ctx, old, transition)
	if err != nil {
		t.Fatal(err)
	}
	if next.ID == old || next.StartsOn.Time.Format("2006-01-02") != transition.StartsOn.Format("2006-01-02") {
		t.Fatalf("new identity/date=%v", next)
	}
	var originalEnd string
	if err := pool.QueryRow(ctx, `SELECT ends_on::text FROM user_memberships WHERE id=$1`, old).Scan(&originalEnd); err != nil || originalEnd != today.Format("2006-01-02") {
		t.Fatalf("old end=%s err=%v", originalEnd, err)
	}
	if _, err := service.TransitionNextDay(ctx, old, transition); !errors.Is(err, ErrDatedParticipationTransition) {
		t.Fatalf("repeated transition=%v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM user_memberships WHERE user_id=$1`, member).Scan(&count); err != nil || count != 2 {
		t.Fatalf("rollback changed identities count=%d err=%v", count, err)
	}
	var competition uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM programmes WHERE code='Competition'`).Scan(&competition); err != nil {
		t.Fatal(err)
	}
	var secondOld uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO user_memberships(user_id,season_id,programme_id,starts_on,ends_on) VALUES($1,$2,$3,$4,$5) RETURNING id`, unauthorized, season, leisure, today.AddDate(0, 0, -4), today.AddDate(0, 0, 60)).Scan(&secondOld); err != nil {
		t.Fatal(err)
	}
	invalid := OrdinaryDatedAssignment{ActorID: actor, MemberID: unauthorized, SeasonID: season, ProgrammeID: competition, StartsOn: today.AddDate(0, 0, 1)}
	if _, err := service.TransitionNextDay(ctx, secondOld, invalid); !sqlState(err, "23514") {
		t.Fatalf("competition without category accepted: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT ends_on::text FROM user_memberships WHERE id=$1`, secondOld).Scan(&originalEnd); err != nil || originalEnd != today.AddDate(0, 0, 60).Format("2006-01-02") {
		t.Fatalf("failed transition shortened original interval: end=%s err=%v", originalEnd, err)
	}
}

func TestDatedParticipationSubjectLocksAndDayBoundary(t *testing.T) {
	w := newPlacementFixture(t)
	ctx := t.Context()
	config, err := pgxpool.ParseConfig(w.f.url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.User, config.ConnConfig.Password = w.f.roles.AppUsername, w.f.roles.AppPassword
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	service := DatedParticipationService{Pool: pool}
	var today time.Time
	if err := pool.QueryRow(ctx, `SELECT (clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date`).Scan(&today); err != nil {
		t.Fatal(err)
	}
	in := OrdinaryDatedAssignment{ActorID: w.actor, MemberID: w.actor, SeasonID: w.season, ProgrammeID: w.programme, StartsOn: today}
	own, err := service.CreateOrdinaryAssignment(ctx, in)
	if err != nil || own.UserID == nil || *own.UserID != w.actor {
		t.Fatalf("administrator self-assignment failed: %v", err)
	}
	before := w.revision(t)
	var sourceBefore string
	if err := pool.QueryRow(ctx, `SELECT to_jsonb(membership)::text FROM user_memberships membership WHERE id=$1`, own.ID).Scan(&sourceBefore); err != nil {
		t.Fatal(err)
	}
	foreign := in
	foreign.MemberID, foreign.StartsOn = w.subject, today.AddDate(0, 0, 1)
	if _, err := service.TransitionNextDay(ctx, own.ID, foreign); !errors.Is(err, ErrDatedParticipationTransition) {
		t.Fatalf("another member's interval accepted as transition source: %v", err)
	}
	if w.revision(t) != before {
		t.Fatal("rejected transition changed the subject's placement revision")
	}
	var sourceAfter string
	if err := pool.QueryRow(ctx, `SELECT to_jsonb(membership)::text FROM user_memberships membership WHERE id=$1`, own.ID).Scan(&sourceAfter); err != nil || sourceAfter != sourceBefore {
		t.Fatalf("rejected transition changed its foreign source: %v", err)
	}

	t.Run("actor update conflict", func(t *testing.T) {
		tx, err := w.f.low.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(t.Context())
		recipientSchemaExec(t, tx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, w.actor)
		if _, err := service.CreateOrdinaryAssignment(t.Context(), in); !errors.Is(err, ErrClassificationStale) {
			t.Fatalf("locked self-actor did not return a retryable classification conflict: %v", err)
		}
	})

	t.Run("calendar day changes before commit", func(t *testing.T) {
		in.MemberID = uuid.New()
		recipientSchemaExec(t, w.web, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Synthetic day boundary',$2,'hash','1980-01-01')`, in.MemberID, in.MemberID.String()+"@example.test")
		outcome, err := WithRecipientPublicationTx(t.Context(), pool, recipientTestOptions(), func(ctx context.Context, commands dbgen.DBTX) error {
			// PostgreSQL executes every query and write. Advance only the final
			// calendar-date observation to exercise the midnight rollback.
			_, err := writeDatedParticipation(ctx, &datedDayBoundaryTx{DBTX: commands}, in, nil, "")
			return err
		})
		if !errors.Is(err, ErrClassificationStale) || outcome.Outcome != SessionTxNotCommitted {
			t.Fatalf("day boundary did not roll back assignment: outcome=%v error=%v", outcome.Outcome, err)
		}
		var memberships, heads int
		if err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM user_memberships WHERE user_id=$1),(SELECT count(*) FROM training_placement_heads WHERE subject_user_id=$1)`, in.MemberID).Scan(&memberships, &heads); err != nil || memberships != 0 || heads != 0 {
			t.Fatalf("day boundary left membership or placement head: %d %d %v", memberships, heads, err)
		}
	})
}

type datedDayBoundaryTx struct {
	dbgen.DBTX
	dayReads int
}

func (tx *datedDayBoundaryTx) QueryRow(ctx context.Context, statement string, args ...any) pgx.Row {
	if statement == `SELECT (clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date` {
		tx.dayReads++
		if tx.dayReads == 2 {
			statement += " + 1"
		}
	}
	return tx.DBTX.QueryRow(ctx, statement, args...)
}
