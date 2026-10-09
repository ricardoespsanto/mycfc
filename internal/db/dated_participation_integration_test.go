//go:build integration

package db

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDatedParticipationDatabaseBoundaries(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	var contractInstalled bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conname='user_memberships_one_participation_per_day')`).Scan(&contractInstalled); err != nil {
		t.Fatal(err)
	}
	if !contractInstalled {
		t.Fatal("dated participation exclusion missing from final schema")
	}
	actor, member := uuid.New(), uuid.New()
	season := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id,name,email,password_hash,date_of_birth) VALUES
 ($1,'Staff de teste',$3,'hash','1980-01-01'),($2,'Atleta de teste',$4,'hash','2010-02-28')`,
		actor, member, "staff-"+uuid.NewString()+"@example.test", "member-"+uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO seasons (id,code,name,starts_on,ends_on) VALUES ($1,$2,'Época teste','2026-01-01','2026-12-31')`, season, "IT_"+uuid.NewString()[:8]); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM user_memberships WHERE user_id=$1`, member)
		_, _ = pool.Exec(context.Background(), `DELETE FROM competition_categories WHERE season_id=$1`, season)
		_, _ = pool.Exec(context.Background(), `DELETE FROM seasons WHERE id=$1`, season)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id IN ($1,$2)`, actor, member)
	})
	q := dbgen.New(pool)
	leisure, err := q.GetProgrammeByCode(ctx, "Leisure")
	if err != nil {
		t.Fatal(err)
	}
	initiation, err := q.GetProgrammeByCode(ctx, "Initiation")
	if err != nil {
		t.Fatal(err)
	}
	competition, err := q.GetProgrammeByCode(ctx, "Competition")
	if err != nil {
		t.Fatal(err)
	}
	polo, err := q.GetProgrammeByCode(ctx, "Kayak_Polo")
	if err != nil {
		t.Fatal(err)
	}
	day := func(value string) pgtype.Date {
		parsed, err := time.Parse("2006-01-02", value)
		if err != nil {
			t.Fatal(err)
		}
		return pgtype.Date{Time: parsed, Valid: true}
	}
	create := func(code uuid.UUID, start, end string) (dbgen.UserMembership, error) {
		p := dbgen.CreateDatedParticipationParams{UserID: &member, SeasonID: season, ProgrammeID: code, StartsOn: day(start)}
		if end != "" {
			p.EndsOn = day(end)
		}
		return q.CreateDatedParticipation(ctx, p)
	}
	first, err := create(leisure.ID, "2026-01-01", "2026-02-28")
	if err != nil {
		t.Fatal(err)
	}
	modality, err := q.GetModalityByCode(ctx, "K1")
	if err != nil {
		t.Fatal(err)
	}
	if err := q.AddMembershipModality(ctx, dbgen.AddMembershipModalityParams{MembershipID: first.ID, ModalityID: modality.ID}); err != nil {
		t.Fatal(err)
	}
	second, err := create(initiation.ID, "2026-03-01", "2026-03-31")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID {
		t.Fatal("sequential participation reused identity")
	}
	for _, tc := range []struct {
		name, from, to string
		code           uuid.UUID
		sqlstate       string
	}{
		{"overlap another programme", "2026-02-28", "2026-03-02", polo.ID, "23P01"},
		{"competition requires category", "2026-04-01", "2026-04-02", competition.ID, "23514"},
		{"overlap same programme", "2026-03-31", "2026-04-01", initiation.ID, "23P01"},
		{"outside season", "2027-01-01", "", leisure.ID, "23514"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := create(tc.code, tc.from, tc.to)
			var pgerr *pgconn.PgError
			if !errors.As(err, &pgerr) || pgerr.Code != tc.sqlstate {
				t.Fatalf("got %v, want SQLSTATE %s", err, tc.sqlstate)
			}
		})
	}
	// A later return to the same programme must make a new row, not reopen first.
	returned, err := create(leisure.ID, "2026-04-01", "")
	if err != nil {
		t.Fatal(err)
	}
	if returned.ID == first.ID {
		t.Fatal("history overwritten")
	}
	var referenced uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT membership_id FROM membership_modalities WHERE membership_id=$1 AND modality_id=$2`, first.ID, modality.ID).Scan(&referenced); err != nil || referenced != first.ID {
		t.Fatalf("prior membership reference changed: %v %v", referenced, err)
	}
	if !returned.EndsOn.Valid || returned.EndsOn.Time.Format("2006-01-02") != "2026-12-31" {
		t.Fatalf("open interval must end at season boundary: %v", returned.EndsOn)
	}
	if _, err := q.UpsertCurrentSeasonMembership(ctx, dbgen.UpsertCurrentSeasonMembershipParams{UserID: member, SeasonID: season, ProgrammeID: leisure.ID, StartsOn: day("2026-04-02")}); err != nil {
		t.Fatalf("repeat current request: %v", err)
	}
	var priorEnd time.Time
	if err := pool.QueryRow(ctx, `SELECT ends_on FROM user_memberships WHERE id=$1`, first.ID).Scan(&priorEnd); err != nil || priorEnd.Format("2006-01-02") != "2026-02-28" {
		t.Fatalf("prior end changed: %v %v", priorEnd, err)
	}
	// A separate subject exercises category rules without conflicting with open interval.
	other := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Outra atleta',$2,'hash','2010-02-28')`, other, "other-"+uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM user_memberships WHERE user_id=$1`, other)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, other)
	})
	category, err := q.CreateCompetitionCategory(ctx, dbgen.CreateCompetitionCategoryParams{SeasonID: season, ProgrammeID: competition.ID, Code: "IT_RANGE", NamePt: "Faixa de teste", BirthDateFrom: day("2009-01-01"), BirthDateTo: day("2009-12-31"), ApprovedByUserID: actor, ApprovedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	input := dbgen.CreateDatedParticipationParams{UserID: &other, SeasonID: season, ProgrammeID: competition.ID, CompetitionCategoryID: &category.ID, StartsOn: day("2026-04-01")}
	if _, err := q.CreateDatedParticipation(ctx, input); !sqlState(err, "23514") {
		t.Fatalf("out-of-range category without exception: %v", err)
	}
	reason := "Exceção etária verificada com registo do clube"
	input.AgeExceptionReason = &reason
	if _, err := q.CreateDatedParticipation(ctx, input); !sqlState(err, "23514") {
		t.Fatalf("exception without service: %v", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('mycfc.actor_id',$1,true)`, actor.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := dbgen.New(tx).CreateDatedParticipation(ctx, input); !sqlState(err, "23514") {
		t.Fatalf("forged actor GUC bypassed disabled exception workflow: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	// Two independent direct writes against the same dates cannot both commit.
	third := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Terceira atleta',$2,'hash','2010-01-01')`, third, "third-"+uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM user_memberships WHERE user_id=$1`, third)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, third)
	})
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, code := range []uuid.UUID{leisure.ID, initiation.ID} {
		wg.Add(1)
		go func(code uuid.UUID) {
			defer wg.Done()
			<-start
			_, e := q.CreateDatedParticipation(ctx, dbgen.CreateDatedParticipationParams{UserID: &third, SeasonID: season, ProgrammeID: code, StartsOn: day("2026-05-01")})
			results <- e
		}(code)
	}
	close(start)
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for e := range results {
		if e == nil {
			successes++
		} else if sqlState(e, "23P01") {
			conflicts++
		} else {
			t.Fatalf("unexpected concurrent error: %v", e)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent results success=%d conflicts=%d", successes, conflicts)
	}
}

func TestDatedParticipationHistoryProtected(t *testing.T) {
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
	actor, member, season := uuid.New(), uuid.New(), uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Staff teste',$3,'hash','1980-01-01'),($2,'Atleta teste',$4,'hash','2010-02-28')`, actor, member, "staff-"+uuid.NewString()+"@example.test", "athlete-"+uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO seasons(id,code,name,starts_on,ends_on) VALUES($1,$2,'Época teste','2026-01-01','2026-12-31')`, season, "IT_"+uuid.NewString()[:8]); err != nil {
		t.Fatal(err)
	}
	q := dbgen.New(tx)
	competition, err := q.GetProgrammeByCode(ctx, "Competition")
	if err != nil {
		t.Fatal(err)
	}
	category, err := q.CreateCompetitionCategory(ctx, dbgen.CreateCompetitionCategoryParams{SeasonID: season, ProgrammeID: competition.ID, Code: "IT_HISTORY", NamePt: "História teste", BirthDateFrom: pgtype.Date{Time: time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC), Valid: true}, BirthDateTo: pgtype.Date{Time: time.Date(2010, 12, 31, 0, 0, 0, 0, time.UTC), Valid: true}, ApprovedByUserID: actor, ApprovedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	var membership uuid.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO user_memberships(user_id,season_id,programme_id,competition_category_id,starts_on,ends_on) VALUES($1,$2,$3,$4,'2026-04-01','2026-06-30') RETURNING id`, member, season, competition.ID, category.ID).Scan(&membership); err != nil {
		t.Fatal(err)
	}
	for _, change := range []struct {
		name, sql string
		args      []any
	}{
		{"start rewrite", `UPDATE user_memberships SET starts_on='2026-04-02' WHERE id=$1`, []any{membership}},
		{"reopen", `UPDATE user_memberships SET ends_on='2026-12-31' WHERE id=$1`, []any{membership}},
		{"category reassignment", `UPDATE user_memberships SET competition_category_id=NULL WHERE id=$1`, []any{membership}},
		{"forged exception", `UPDATE user_memberships SET age_exception_reason='Motivo fabricado',age_exception_by_id=$2,age_exception_at=now() WHERE id=$1`, []any{membership, actor}},
		{"DOB rewrite", `UPDATE users SET date_of_birth='2011-02-28' WHERE id=$1`, []any{member}},
		{"definition rewrite", `UPDATE competition_categories SET birth_date_to='2011-12-31' WHERE id=$1`, []any{category.ID}},
	} {
		t.Run(change.name, func(t *testing.T) {
			save, err := tx.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			_, err = save.Exec(ctx, change.sql, change.args...)
			if !sqlState(err, "23514") {
				t.Errorf("history rewrite error=%v, want 23514", err)
			}
			if err := save.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDatedParticipationPostconditionRejectsExpandMarkerAndTampering(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS mycfc_meta; CREATE TABLE IF NOT EXISTS mycfc_meta.schema_migrations(version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		t.Fatal(err)
	}
	// Other integration cases may record a final marker in the shared fixture.
	// Isolate the ledger assertion in this rollback-only transaction.
	if _, err := tx.Exec(ctx, `DELETE FROM mycfc_meta.schema_migrations WHERE version IN ('202609290002_dated_participation_contract','202609290003_recorded_participation_end_guard','202609300004_age_exception_provenance')`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO mycfc_meta.schema_migrations(version) VALUES('202609290001_dated_participation') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if err := VerifyDatedParticipationContract(ctx, tx); err == nil {
		t.Fatal("expand marker alone accepted as dated contract")
	}
	if err := VerifyDatedParticipationRuntime(ctx, tx); err != nil {
		t.Fatalf("web-role schema-only check rejected valid final schema: %v", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO mycfc_meta.schema_migrations(version) VALUES('202609290002_dated_participation_contract') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if err := VerifyDatedParticipationContract(ctx, tx); err == nil {
		t.Fatal("002 marker without recorded-history guard accepted")
	}
	if _, err := tx.Exec(ctx, `INSERT INTO mycfc_meta.schema_migrations(version) VALUES('202609290003_recorded_participation_end_guard') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if err := VerifyDatedParticipationContract(ctx, tx); err == nil {
		t.Fatal("history guard without exception provenance migration accepted")
	}
	if _, err := tx.Exec(ctx, `INSERT INTO mycfc_meta.schema_migrations(version) VALUES('202609300004_age_exception_provenance') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if err := VerifyDatedParticipationContract(ctx, tx); err != nil {
		t.Fatalf("final contract rejected: %v", err)
	}
	if _, err := tx.Exec(ctx, `ALTER TABLE user_memberships DISABLE TRIGGER user_memberships_dated_participation_valid`); err != nil {
		t.Fatal(err)
	}
	if err := VerifyDatedParticipationContract(ctx, tx); err == nil {
		t.Fatal("disabled validation trigger accepted")
	}
}

func sqlState(err error, code string) bool {
	var pgerr *pgconn.PgError
	return errors.As(err, &pgerr) && pgerr.Code == code
}
