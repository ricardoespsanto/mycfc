//go:build integration

package db

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
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
