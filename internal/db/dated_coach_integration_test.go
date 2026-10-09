//go:build integration

package db

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"testing"
	"time"
)

func TestDatedCoachGrantRequiresActiveSubjectScopeAtWrite(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var today time.Time
	if err := pool.QueryRow(ctx, `SELECT (now() AT TIME ZONE 'Europe/Lisbon')::date`).Scan(&today); err != nil {
		t.Fatal(err)
	}
	actor, member, other, season := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{actor, member, other} {
		_, err = pool.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Scope test',$2,'hash','1980-01-01')`, id, uuid.NewString()+"@example.test")
		if err != nil {
			t.Fatal(err)
		}
	}
	// Isolated integration database only; preserve any prior current-season flag.
	var prior uuid.UUID
	_ = pool.QueryRow(ctx, `SELECT id FROM seasons WHERE is_current`).Scan(&prior)
	if _, err = pool.Exec(ctx, `UPDATE seasons SET is_current=false WHERE is_current`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `UPDATE seasons SET is_current=false WHERE id=$1`, season)
		if prior != uuid.Nil {
			_, _ = pool.Exec(ctx, `UPDATE seasons SET is_current=true WHERE id=$1`, prior)
		}
	})
	_, err = pool.Exec(ctx, `INSERT INTO seasons(id,code,name,starts_on,ends_on,is_current) VALUES($1,$2,'Coach scope',$3,$4,true)`, season, "CS_"+uuid.NewString()[:8], today.AddDate(0, 0, -10), today.AddDate(0, 0, 60))
	if err != nil {
		t.Fatal(err)
	}
	var leisure, initiation uuid.UUID
	for code, dest := range map[string]*uuid.UUID{"Leisure": &leisure, "Initiation": &initiation} {
		if err = pool.QueryRow(ctx, `SELECT id FROM programmes WHERE code=$1`, code).Scan(dest); err != nil {
			t.Fatal(err)
		}
	}
	var old uuid.UUID
	err = pool.QueryRow(ctx, `INSERT INTO user_memberships(user_id,season_id,programme_id,starts_on) VALUES($1,$2,$3,$4) RETURNING id`, member, season, leisure, today.AddDate(0, 0, -5)).Scan(&old)
	if err != nil {
		t.Fatal(err)
	}
	grant := uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO staff_grants(id,user_id,capability,programme_id,granted_by_id) VALUES($1,$2,'COACH',$3,$2)`, grant, actor, leisure)
	if err != nil {
		t.Fatal(err)
	}
	service := DatedParticipationService{Pool: pool}
	in := OrdinaryDatedAssignment{ActorID: actor, MemberID: member, SeasonID: season, ProgrammeID: leisure, StartsOn: today.AddDate(0, 0, 1)}
	first, err := service.CreateOrdinaryAssignment(ctx, OrdinaryDatedAssignment{ActorID: actor, MemberID: other, SeasonID: season, ProgrammeID: leisure, StartsOn: today})
	if err != nil || first.UserID == nil || *first.UserID != other || first.ProgrammeID != leisure {
		t.Fatalf("first assignment row=%v err=%v", first, err)
	}
	if _, err = service.CreateOrdinaryAssignment(ctx, OrdinaryDatedAssignment{ActorID: actor, MemberID: uuid.New(), SeasonID: season, ProgrammeID: initiation, StartsOn: today}); !errors.Is(err, ErrDatedParticipationForbidden) {
		t.Fatalf("other programme %v", err)
	}
	team := uuid.New()
	if _, err = pool.Exec(ctx, `INSERT INTO teams(id,season_id,programme_id,code,name) VALUES($1,$2,$3,$4,'Restricted team')`, team, season, leisure, "T_"+uuid.NewString()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO staff_grants(user_id,capability,team_id,granted_by_id) VALUES($1,'COACH',$2,$1)`, other, team); err != nil {
		t.Fatal(err)
	}
	if _, err = service.CreateOrdinaryAssignment(ctx, OrdinaryDatedAssignment{ActorID: other, MemberID: member, SeasonID: season, ProgrammeID: leisure, StartsOn: today.AddDate(0, 0, 1)}); !errors.Is(err, ErrDatedParticipationForbidden) {
		t.Fatalf("team-only grant %v", err)
	}
	dependent := uuid.New()
	if _, err = pool.Exec(ctx, `INSERT INTO users(id,name,is_dependent,date_of_birth) VALUES($1,'Dependent test',true,'2012-01-01')`, dependent); err != nil {
		t.Fatal(err)
	}
	if _, err = service.CreateOrdinaryAssignment(ctx, OrdinaryDatedAssignment{ActorID: actor, MemberID: dependent, SeasonID: season, ProgrammeID: leisure, StartsOn: today}); !errors.Is(err, ErrDatedParticipationForbidden) {
		t.Fatalf("unverified guardian subject %v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE users SET is_active=false WHERE id=$1`, actor); err != nil {
		t.Fatal(err)
	}
	if _, err = service.CreateOrdinaryAssignment(ctx, OrdinaryDatedAssignment{ActorID: actor, MemberID: dependent, SeasonID: season, ProgrammeID: leisure, StartsOn: today}); !errors.Is(err, ErrDatedParticipationForbidden) {
		t.Fatalf("inactive coach %v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE users SET is_active=true WHERE id=$1`, actor); err != nil {
		t.Fatal(err)
	}
	if _, err = service.TransitionNextDay(ctx, old, OrdinaryDatedAssignment{ActorID: actor, MemberID: member, SeasonID: season, ProgrammeID: initiation, StartsOn: in.StartsOn}); !errors.Is(err, ErrDatedParticipationForbidden) {
		t.Fatalf("other programme %v", err)
	}
	if _, err = service.TransitionNextDay(ctx, old, in); err != nil {
		t.Fatalf("scoped transition %v", err)
	}
	var closedOn time.Time
	if err = pool.QueryRow(ctx, `SELECT ends_on FROM user_memberships WHERE id=$1`, old).Scan(&closedOn); err != nil || closedOn.Format("2006-01-02") != today.Format("2006-01-02") {
		t.Fatalf("open-ended old assignment closedOn=%v err=%v", closedOn, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE staff_grants SET revoked_at=now(),revoked_by_id=$1,revoke_reason='test' WHERE id=$2`, actor, grant); err != nil {
		t.Fatal(err)
	}
	if _, err = service.CreateOrdinaryAssignment(ctx, OrdinaryDatedAssignment{ActorID: actor, MemberID: member, SeasonID: season, ProgrammeID: leisure, StartsOn: today}); !errors.Is(err, ErrDatedParticipationForbidden) {
		t.Fatalf("revoked grant %v", err)
	}
}
