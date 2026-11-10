//go:build integration

package db

import (
	"context"
	"errors"
	"os"
	"testing"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestTrainingGroupSetupFactsStayScopedAndFresh(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	q := dbgen.New(tx)
	admin, coach, teamCoach, outsider, young := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{admin, coach, teamCoach, outsider, young} {
		exec(`INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Synthetic setup actor',$2,'hash','1980-01-01')`, id, id.String()+"@example.test")
	}
	exec(`UPDATE users SET date_of_birth=(now() AT TIME ZONE 'Europe/Lisbon')::date-interval '17 years' WHERE id=$1`, young)
	for _, id := range []uuid.UUID{admin, young} {
		exec(`INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, id)
	}
	programme := func(code string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM programmes WHERE code=$1`, code).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	competition, initiation, leisure, polo := programme("Competition"), programme("Initiation"), programme("Leisure"), programme("Kayak_Polo")
	season, oldSeason := uuid.New(), uuid.New()
	// Keep global current-season changes transaction-local and restore on exit.
	exec(`UPDATE seasons SET is_current=false WHERE is_current`)
	for _, id := range []uuid.UUID{season, oldSeason} {
		exec(`INSERT INTO seasons(id,code,name,starts_on,ends_on) VALUES($1,$2,'Synthetic setup season',CURRENT_DATE-20,CURRENT_DATE+400)`, id, "S"+id.String()[:8])
	}
	team, otherTeam := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{team, otherTeam} {
		exec(`INSERT INTO teams(id,season_id,programme_id,code,name) VALUES($1,$2,$3,$4,'Synthetic setup team')`, id, season, competition, "T"+id.String()[:8])
	}
	group, initGroup, leisureGroup, poloGroup, teamGroup, otherTeamGroup := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, pair := range [][2]uuid.UUID{{group, competition}, {initGroup, initiation}, {leisureGroup, leisure}, {poloGroup, polo}} {
		exec(`INSERT INTO training_groups(id,name,programme_id,created_by_id) VALUES($1,'Synthetic empty group',$2,$3)`, pair[0], pair[1], admin)
	}
	for _, pair := range [][2]uuid.UUID{{teamGroup, team}, {otherTeamGroup, otherTeam}} {
		exec(`INSERT INTO training_groups(id,name,team_id,created_by_id) VALUES($1,'Synthetic empty team group',$2,$3)`, pair[0], pair[1], admin)
	}
	coachGrant, teamGrant := uuid.New(), uuid.New()
	exec(`INSERT INTO staff_grants(id,user_id,capability,programme_id,granted_by_id) VALUES($1,$2,'COACH',$3,$4)`, coachGrant, coach, competition, admin)
	exec(`INSERT INTO staff_grants(id,user_id,capability,team_id,granted_by_id) VALUES($1,$2,'COACH',$3,$4)`, teamGrant, teamCoach, team, admin)
	status := func(actor, group uuid.UUID) dbgen.TrainingGroupSetupStatusRow {
		t.Helper()
		row, err := q.TrainingGroupSetupStatus(ctx, dbgen.TrainingGroupSetupStatusParams{ActorID: actor, GroupID: group})
		if err != nil {
			t.Fatal(err)
		}
		return row
	}
	denied := func(actor, group uuid.UUID) {
		t.Helper()
		if _, err := q.TrainingGroupSetupStatus(ctx, dbgen.TrainingGroupSetupStatusParams{ActorID: actor, GroupID: group}); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("setup allowed actor=%s group=%s error=%v", actor, group, err)
		}
	}
	row := status(coach, group)
	if row.ProgrammeCode != "Competition" || row.Administrator || !row.ProgrammeAuthority || !row.MissingSeason || row.CategoryConfigured {
		t.Fatalf("empty unconfigured group facts: %+v", row)
	}
	row = status(teamCoach, teamGroup)
	if row.ProgrammeCode != "Competition" || row.ProgrammeAuthority || row.Administrator {
		t.Fatalf("team authority broadened: %+v", row)
	}
	denied(coach, teamGroup)
	denied(teamCoach, group)
	denied(teamCoach, otherTeamGroup)
	denied(outsider, group)
	denied(young, group)
	for _, g := range []uuid.UUID{group, initGroup, leisureGroup, poloGroup, teamGroup} {
		r := status(admin, g)
		if !r.Administrator || !r.ProgrammeAuthority {
			t.Fatalf("actual admin facts: %+v", r)
		}
	}
	exec(`UPDATE seasons SET is_current=true WHERE id=$1`, season)
	if status(coach, group).MissingSeason {
		t.Fatal("configured current season reported missing")
	}
	category := func(season, programme uuid.UUID) {
		t.Helper()
		exec(`INSERT INTO competition_categories(season_id,programme_id,code,name_pt,approved_by_user_id,approved_at) VALUES($1,$2,$3,'Synthetic definition',$4,now())`, season, programme, "C"+uuid.NewString()[:8], admin)
	}
	category(season, initiation)
	category(oldSeason, competition)
	if status(coach, group).CategoryConfigured {
		t.Fatal("other programme or non-current definition leaked into competition setup")
	}
	if !status(admin, initGroup).CategoryConfigured {
		t.Fatal("current initiation definition omitted")
	}
	category(season, competition)
	if !status(coach, group).CategoryConfigured {
		t.Fatal("current competition definition omitted")
	}
	for _, g := range []uuid.UUID{leisureGroup, poloGroup} {
		if status(admin, g).CategoryConfigured {
			t.Fatal("category prerequisite introduced for leisure or polo")
		}
	}
	teamProgrammeGrant := uuid.New()
	exec(`INSERT INTO staff_grants(id,user_id,capability,programme_id,granted_by_id) VALUES($1,$2,'COACH',$3,$4)`, teamProgrammeGrant, teamCoach, competition, admin)
	if !status(teamCoach, teamGroup).ProgrammeAuthority {
		t.Fatal("additional exact programme grant not recognized")
	}
	exec(`UPDATE staff_grants SET revoked_at=clock_timestamp(),revoked_by_id=$2,revoke_reason='Synthetic fixture revoke' WHERE id=$1`, teamProgrammeGrant, admin)
	if status(teamCoach, teamGroup).ProgrammeAuthority {
		t.Fatal("revoked programme authority retained through exact team read")
	}
	week, otherWeek := uuid.New(), uuid.New()
	for _, pair := range [][2]uuid.UUID{{week, group}, {otherWeek, teamGroup}} {
		exec(`INSERT INTO training_plans(id,title,programme_id,team_id,training_group_id,season_id,week_start,created_by_id) SELECT $1,'Synthetic setup week',g.programme_id,g.team_id,g.id,$3,date_trunc('week',CURRENT_DATE)::date,$4 FROM training_groups g WHERE g.id=$2`, pair[0], pair[1], season, admin)
	}
	date, err := q.TrainingGroupSetupWeek(ctx, dbgen.TrainingGroupSetupWeekParams{ActorID: coach, GroupID: group, WeekID: week})
	if err != nil || !date.Valid {
		t.Fatalf("authorized bounded week: %+v %v", date, err)
	}
	for _, params := range []dbgen.TrainingGroupSetupWeekParams{
		{ActorID: admin, GroupID: group, WeekID: otherWeek}, {ActorID: coach, GroupID: teamGroup, WeekID: otherWeek}, {ActorID: teamCoach, GroupID: group, WeekID: week},
	} {
		if _, err := q.TrainingGroupSetupWeek(ctx, params); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("cross-group/week authority accepted: %v", err)
		}
	}
	// Simulate revocation after the initial group read: each protected query
	// independently checks the latest grant, instead of trusting GetGroup.
	rows, err := q.TrainingGroupRecords(ctx, dbgen.TrainingGroupRecordsParams{ActorID: coach, GroupID: &group, RowLimit: 1})
	if err != nil || len(rows) != 1 {
		t.Fatalf("initial authorized group read: %v", err)
	}
	exec(`UPDATE staff_grants SET revoked_at=clock_timestamp(),revoked_by_id=$2,revoke_reason='Synthetic fixture revoke' WHERE id=$1`, coachGrant, admin)
	denied(coach, group)
	if _, err := q.TrainingGroupSetupWeek(ctx, dbgen.TrainingGroupSetupWeekParams{ActorID: coach, GroupID: group, WeekID: week}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("revoked group-week access: %v", err)
	}
	exec(`UPDATE staff_grants SET revoked_at=clock_timestamp(),revoked_by_id=$2,revoke_reason='Synthetic fixture revoke' WHERE id=$1`, teamGrant, admin)
	denied(teamCoach, teamGroup)
	// Preserve the last-active-administrator invariant while testing an
	// individual administrator becoming inactive or losing its role.
	exec(`INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, outsider)
	exec(`UPDATE users SET is_active=false WHERE id=$1`, admin)
	denied(admin, group)
	exec(`UPDATE users SET is_active=true WHERE id=$1`, admin)
	exec(`DELETE FROM user_platform_roles WHERE user_id=$1`, admin)
	denied(admin, group)
}
