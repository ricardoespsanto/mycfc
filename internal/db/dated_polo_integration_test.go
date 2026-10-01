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

func TestDatedPoloTeamAutomaticAssignment(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	actor, member, transitionMember, historyMember, season, otherSeason := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{actor, member, transitionMember, historyMember} {
		if _, err = pool.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Polo fixture',$2,'hash','1980-01-01')`, id, uuid.NewString()+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = pool.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, actor); err != nil {
		t.Fatal(err)
	}
	var today time.Time
	if err = pool.QueryRow(ctx, `SELECT (now() AT TIME ZONE 'Europe/Lisbon')::date`).Scan(&today); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{season, otherSeason} {
		if _, err = pool.Exec(ctx, `INSERT INTO seasons(id,code,name,starts_on,ends_on) VALUES($1,$2,'Polo fixture',$3,$4)`, id, "PT_"+uuid.NewString()[:8], today.AddDate(0, 0, -5), today.AddDate(0, 0, 60)); err != nil {
			t.Fatal(err)
		}
	}
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
	if _, err = pool.Exec(ctx, `UPDATE seasons SET is_current=true WHERE id=$1`, season); err != nil {
		t.Fatal(err)
	}
	var polo, leisure uuid.UUID
	if err = pool.QueryRow(ctx, `SELECT id FROM programmes WHERE code='Kayak_Polo'`).Scan(&polo); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT id FROM programmes WHERE code='Leisure'`).Scan(&leisure); err != nil {
		t.Fatal(err)
	}
	svc := DatedParticipationService{Pool: pool}
	in := OrdinaryDatedAssignment{ActorID: actor, MemberID: member, SeasonID: season, ProgrammeID: polo, StartsOn: today}
	if _, err = svc.CreateOrdinaryAssignment(ctx, in); !errors.Is(err, ErrDatedPoloTeamUnavailable) {
		t.Fatalf("missing team: %v", err)
	}
	var count int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM user_memberships WHERE user_id=$1`, member).Scan(&count)
	if count != 0 {
		t.Fatalf("inserted without team: %d", count)
	}
	wrong, team := uuid.New(), uuid.New()
	for _, v := range []struct{ id, season, programme uuid.UUID }{{wrong, otherSeason, polo}, {team, season, polo}} {
		if _, err = pool.Exec(ctx, `INSERT INTO teams(id,season_id,programme_id,code,name) VALUES($1,$2,$3,$4,'Equipa Polo')`, v.id, v.season, v.programme, "PT_"+uuid.NewString()[:8]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = pool.Exec(ctx, `INSERT INTO teams(season_id,programme_id,code,name) VALUES($1,$2,$3,'Equipa duplicada')`, season, polo, "PT_"+uuid.NewString()[:8]); !sqlState(err, "23505") {
		t.Fatalf("second shared team accepted: %v", err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO user_memberships(user_id,season_id,programme_id,team_id,starts_on) VALUES($1,$2,$3,$4,$5)`, transitionMember, season, polo, wrong, today); !sqlState(err, "23503") {
		t.Fatalf("cross-season team FK: %v", err)
	}
	var wrongProgramme uuid.UUID
	if err = pool.QueryRow(ctx, `INSERT INTO teams(season_id,programme_id,code,name) VALUES($1,$2,$3,'Outra equipa') RETURNING id`, season, leisure, "PT_"+uuid.NewString()[:8]).Scan(&wrongProgramme); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO user_memberships(user_id,season_id,programme_id,team_id,starts_on) VALUES($1,$2,$3,$4,$5)`, transitionMember, season, polo, wrongProgramme, today); !sqlState(err, "23503") {
		t.Fatalf("wrong-programme team FK: %v", err)
	}
	row, err := svc.CreateOrdinaryAssignment(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if row.TeamID == nil || *row.TeamID != team {
		t.Fatalf("team=%v want %s", row.TeamID, team)
	}
	if _, err = svc.CreateOrdinaryAssignment(ctx, OrdinaryDatedAssignment{ActorID: member, MemberID: transitionMember, SeasonID: season, ProgrammeID: polo, StartsOn: today}); !errors.Is(err, ErrDatedParticipationForbidden) {
		t.Fatalf("unscoped actor: %v", err)
	}
	if _, err = svc.CreateAgeException(ctx, AgeExceptionAssignment{ActorID: actor, MemberID: member, SeasonID: season, ProgrammeID: polo, CategoryID: uuid.New(), StartsOn: today.AddDate(0, 0, 1), Reason: "Exceção"}); !sqlState(err, "23514") && !sqlState(err, "23503") {
		t.Fatalf("Polo category combination: %v", err)
	}
	var invalidCount int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM user_memberships WHERE user_id=$1`, member).Scan(&invalidCount); err != nil || invalidCount != 1 {
		t.Fatalf("invalid insert changed history count=%d err=%v", invalidCount, err)
	}
	var old uuid.UUID
	if err = pool.QueryRow(ctx, `INSERT INTO user_memberships(user_id,season_id,programme_id,starts_on) VALUES($1,$2,$3,$4) RETURNING id`, transitionMember, season, leisure, today.AddDate(0, 0, -2)).Scan(&old); err != nil {
		t.Fatal(err)
	}
	next, err := svc.TransitionNextDay(ctx, old, OrdinaryDatedAssignment{ActorID: actor, MemberID: transitionMember, SeasonID: season, ProgrammeID: polo, StartsOn: today.AddDate(0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	if next.TeamID == nil || *next.TeamID != team {
		t.Fatalf("transition team=%v", next.TeamID)
	}
	var priorTeam *uuid.UUID
	var end time.Time
	if err = pool.QueryRow(ctx, `SELECT team_id,ends_on FROM user_memberships WHERE id=$1`, old).Scan(&priorTeam, &end); err != nil || priorTeam != nil || end.Format("2006-01-02") != today.Format("2006-01-02") {
		t.Fatalf("history team=%v end=%s err=%v", priorTeam, end, err)
	}
	var poloOld uuid.UUID
	if err = pool.QueryRow(ctx, `INSERT INTO user_memberships(user_id,season_id,programme_id,team_id,starts_on) VALUES($1,$2,$3,$4,$5) RETURNING id`, historyMember, season, polo, team, today.AddDate(0, 0, -2)).Scan(&poloOld); err != nil {
		t.Fatal(err)
	}
	plain, err := svc.TransitionNextDay(ctx, poloOld, OrdinaryDatedAssignment{ActorID: actor, MemberID: historyMember, SeasonID: season, ProgrammeID: leisure, StartsOn: today.AddDate(0, 0, 1)})
	if err != nil || plain.TeamID != nil {
		t.Fatalf("non-Polo interval team=%v err=%v", plain.TeamID, err)
	}
	if err = pool.QueryRow(ctx, `SELECT team_id,ends_on FROM user_memberships WHERE id=$1`, poloOld).Scan(&priorTeam, &end); err != nil || priorTeam == nil || *priorTeam != team || end.Format("2006-01-02") != today.Format("2006-01-02") {
		t.Fatalf("historical Polo identity/team changed team=%v end=%s err=%v", priorTeam, end, err)
	}
}
