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

func TestAgeExceptionScopedAndRecorded(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	admin, coach, outsider, member := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, u := range []struct {
		id  uuid.UUID
		dob string
	}{{admin, "1980-01-01"}, {coach, "1980-01-01"}, {outsider, "1980-01-01"}, {member, "2010-02-28"}} {
		if _, err = pool.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Teste',$2,'hash',$3::date)`, u.id, uuid.NewString()+"@example.test", u.dob); err != nil {
			t.Fatal(err)
		}
	}
	// Every row is removed below, before the pool closes.
	if _, err = pool.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, admin); err != nil {
		t.Fatal(err)
	}
	var competition, leisure uuid.UUID
	if err = pool.QueryRow(ctx, `SELECT id FROM programmes WHERE code='Competition'`).Scan(&competition); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT id FROM programmes WHERE code='Leisure'`).Scan(&leisure); err != nil {
		t.Fatal(err)
	}
	today := time.Now().In(time.FixedZone("WEST", 3600)) // service checks the database Lisbon day below
	if err = pool.QueryRow(ctx, `SELECT (now() AT TIME ZONE 'Europe/Lisbon')::date`).Scan(&today); err != nil {
		t.Fatal(err)
	}
	season := uuid.New()
	var prior uuid.UUID
	_ = pool.QueryRow(ctx, `SELECT id FROM seasons WHERE is_current`).Scan(&prior)
	if _, err = pool.Exec(ctx, `UPDATE seasons SET is_current=false WHERE is_current`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO seasons(id,code,name,starts_on,ends_on,is_current) VALUES($1,$2,'Teste',$3::date,$4::date,true)`, season, "AGE_"+uuid.NewString()[:8], today.AddDate(0, 0, -1), today.AddDate(0, 0, 30)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup := context.Background()
		_, _ = pool.Exec(cleanup, `DELETE FROM user_memberships WHERE user_id=$1`, member)
		_, _ = pool.Exec(cleanup, `DELETE FROM staff_grants WHERE user_id=$1`, coach)
		_, _ = pool.Exec(cleanup, `DELETE FROM competition_categories WHERE season_id=$1`, season)
		_, _ = pool.Exec(cleanup, `DELETE FROM seasons WHERE id=$1`, season)
		if prior != uuid.Nil {
			_, _ = pool.Exec(cleanup, `UPDATE seasons SET is_current=true WHERE id=$1`, prior)
		}
		_, _ = pool.Exec(cleanup, `DELETE FROM user_platform_roles WHERE user_id=$1`, admin)
		_, _ = pool.Exec(cleanup, `DELETE FROM users WHERE id IN ($1,$2,$3,$4)`, admin, coach, outsider, member)
	})
	var cat uuid.UUID
	if err = pool.QueryRow(ctx, `INSERT INTO competition_categories(season_id,programme_id,code,name_pt,birth_date_from,birth_date_to,approved_by_user_id,approved_at) VALUES($1,$2,'AGE','Categoria teste','2009-01-01','2009-12-31',$3,now()) RETURNING id`, season, competition, admin).Scan(&cat); err != nil {
		t.Fatal(err)
	}
	var eligibleCat uuid.UUID
	if err = pool.QueryRow(ctx, `INSERT INTO competition_categories(season_id,programme_id,code,name_pt,birth_date_from,birth_date_to,approved_by_user_id,approved_at) VALUES($1,$2,'ELIGIBLE','Categoria normal','2010-01-01','2010-12-31',$3,now()) RETURNING id`, season, competition, admin).Scan(&eligibleCat); err != nil {
		t.Fatal(err)
	}
	var old uuid.UUID
	if err = pool.QueryRow(ctx, `INSERT INTO user_memberships(user_id,season_id,programme_id,competition_category_id,starts_on) VALUES($1,$2,$3,$4,$5::date) RETURNING id`, member, season, competition, eligibleCat, today.AddDate(0, 0, -1)).Scan(&old); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO staff_grants(user_id,capability,programme_id,granted_by_id) VALUES($1,'COACH',$2,$3)`, coach, competition, admin); err != nil {
		t.Fatal(err)
	}
	service := DatedParticipationService{Pool: pool}
	input := AgeExceptionAssignment{ActorID: coach, MemberID: member, SeasonID: season, ProgrammeID: competition, CategoryID: cat, StartsOn: today.AddDate(0, 0, 1), Reason: "  Exceção aprovada  "}
	if _, err = service.TransitionNextDayAgeException(ctx, old, input); err != nil {
		t.Fatalf("scoped coach: %v", err)
	}
	var reason string
	var actor uuid.UUID
	var recorded time.Time
	if err = pool.QueryRow(ctx, `SELECT age_exception_reason,age_exception_by_id,age_exception_at FROM user_memberships WHERE user_id=$1 AND competition_category_id=$2`, member, cat).Scan(&reason, &actor, &recorded); err != nil {
		t.Fatal(err)
	}
	if reason != "Exceção aprovada" || actor != coach || recorded.IsZero() {
		t.Fatal("provenance missing or invalid")
	}
	if _, err = pool.Exec(ctx, `UPDATE user_memberships SET age_exception_by_id=$1 WHERE user_id=$2 AND competition_category_id=$3`, admin, member, cat); !sqlState(err, "23514") {
		t.Fatalf("history rewrite: %v", err)
	}
	input.ActorID = outsider
	input.StartsOn = today.AddDate(0, 0, 1)
	if _, err = service.CreateAgeException(ctx, input); !errors.Is(err, ErrDatedParticipationForbidden) {
		t.Fatalf("outsider: %v", err)
	}
	input.ActorID = admin
	input.Reason = " "
	if _, err = service.CreateAgeException(ctx, input); !errors.Is(err, ErrAgeExceptionReason) {
		t.Fatalf("blank reason accepted: %v", err)
	}
	input.Reason = "Exceção"
	input.CategoryID = eligibleCat
	if _, err = service.CreateAgeException(ctx, input); !sqlState(err, "23514") {
		t.Fatalf("exception without mismatch: %v", err)
	}
	input.CategoryID = cat
	if _, err = pool.Exec(ctx, `INSERT INTO user_memberships(user_id,season_id,programme_id,competition_category_id,starts_on,age_exception_reason,age_exception_by_id,age_exception_at) VALUES($1,$2,$3,$4,$5::date,$6,$7,now())`, member, season, competition, cat, today.AddDate(0, 0, 1), input.Reason, outsider); !sqlState(err, "23514") {
		t.Fatalf("forged unscoped actor: %v", err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO user_memberships(user_id,season_id,programme_id,competition_category_id,starts_on,age_exception_reason) VALUES($1,$2,$3,$4,$5::date,$6)`, member, season, competition, cat, today.AddDate(0, 0, 1), input.Reason); !sqlState(err, "23514") {
		t.Fatalf("missing provenance: %v", err)
	}
	firstSubject := uuid.New()
	if _, err = pool.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Primeira atleta',$2,'hash','2010-02-28')`, firstSubject, uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM user_memberships WHERE user_id=$1`, firstSubject)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, firstSubject)
	})
	firstInput := AgeExceptionAssignment{ActorID: coach, MemberID: firstSubject, SeasonID: season, ProgrammeID: competition, CategoryID: cat, StartsOn: today, Reason: "Exceção inicial"}
	if _, err = service.CreateAgeException(ctx, firstInput); err != nil {
		t.Fatalf("first coach exception: %v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE staff_grants SET revoked_at=now(),revoked_by_id=$1 WHERE user_id=$2`, admin, coach); err != nil {
		t.Fatal(err)
	}
	input.ActorID = coach
	if _, err = service.CreateAgeException(ctx, input); !errors.Is(err, ErrDatedParticipationForbidden) {
		t.Fatalf("revoked coach: %v", err)
	}
	var preserved time.Time
	if err = pool.QueryRow(ctx, `SELECT ends_on FROM user_memberships WHERE id=$1`, old).Scan(&preserved); err != nil || !preserved.Equal(today) {
		t.Fatalf("prior interval rewritten: %v %v", preserved, err)
	}
	// Revocation cannot erase prior evidence or block shortening an exception
	// interval later; the recorded-history guard still controls that shortening.
	if _, err = pool.Exec(ctx, `UPDATE user_memberships SET ends_on=starts_on WHERE user_id=$1 AND competition_category_id=$2`, member, cat); err != nil {
		t.Fatalf("shorten existing exception after coach revocation: %v", err)
	}
	other := uuid.New()
	if _, err = pool.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Outro atleta',$2,'hash','2010-02-28')`, other, uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM user_memberships WHERE user_id=$1`, other)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, other)
	})
	adminInput := AgeExceptionAssignment{ActorID: admin, MemberID: other, SeasonID: season, ProgrammeID: competition, CategoryID: cat, StartsOn: today, Reason: "Exceção administrativa"}
	if _, err = service.CreateAgeException(ctx, adminInput); err != nil {
		t.Fatalf("administrator exception: %v", err)
	}
	adminInput.StartsOn = today.AddDate(0, 0, 1)
	adminInput.CategoryID = eligibleCat
	if _, err = service.CreateAgeException(ctx, adminInput); !sqlState(err, "23514") {
		t.Fatalf("eligible category with reason: %v", err)
	}
	adminInput.CategoryID = cat
	adminInput.ProgrammeID = leisure
	if _, err = service.CreateAgeException(ctx, adminInput); !sqlState(err, "23514") {
		t.Fatalf("category outside programme: %v", err)
	}
	adminInput.ProgrammeID = competition
	adminInput.SeasonID = uuid.New()
	if _, err = service.CreateAgeException(ctx, adminInput); err == nil {
		t.Fatal("unknown season accepted")
	}
	adminInput.SeasonID = season
	adminInput.StartsOn = today.AddDate(0, 0, -2)
	if _, err = service.CreateAgeException(ctx, adminInput); !errors.Is(err, ErrDatedParticipationWindow) {
		t.Fatalf("backdated exception: %v", err)
	}
}
