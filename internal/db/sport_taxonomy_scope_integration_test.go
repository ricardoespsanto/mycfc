//go:build integration

package db

import (
	"context"
	"os"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestSportTaxonomyScopedAssignmentDoesNotGrantAuthority(t *testing.T) {
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
	admin, coach, outsider, member := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{admin, coach, outsider, member} {
		_, err = tx.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Pessoa teste',$2,'hash','1980-01-01')`, id, "sport-"+uuid.NewString()+"@example.test")
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, admin)
	if err != nil {
		t.Fatal(err)
	}
	season := uuid.New()
	today := time.Now().In(time.FixedZone("WEST", 3600))
	start, end := today.AddDate(0, 0, -20), today.AddDate(0, 0, 20)
	_, err = tx.Exec(ctx, `INSERT INTO seasons(id,code,name,starts_on,ends_on) VALUES($1,$2,'Época teste',$3,$4)`, season, "IT_"+uuid.NewString()[:8], start, end)
	if err != nil {
		t.Fatal(err)
	}
	var leisure uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT id FROM programmes WHERE code='Leisure'`).Scan(&leisure); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO user_memberships(user_id,season_id,programme_id,starts_on) VALUES($1,$2,$3,$4)`, member, season, leisure, start)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO staff_grants(user_id,capability,programme_id,granted_by_id) VALUES($1,'COACH',$2,$3)`, coach, leisure, admin)
	if err != nil {
		t.Fatal(err)
	}
	q := dbgen.New(tx)
	assign := func(actor uuid.UUID, code string) int64 {
		rows, e := q.AssignScopedSportingModality(ctx, dbgen.AssignScopedSportingModalityParams{ActorID: actor, UserID: member, ModalityCode: code})
		if e != nil {
			t.Fatal(e)
		}
		return rows
	}
	if n := assign(outsider, "SUP"); n != 0 {
		t.Fatalf("outsider wrote %d rows", n)
	}
	if n := assign(coach, "CANOEING"); n != 1 {
		t.Fatalf("coach wrote %d rows", n)
	}
	if n := assign(admin, "KAYAK_POLO"); n != 1 {
		t.Fatalf("admin wrote %d rows", n)
	}
	if n := assign(coach, "CANOEING"); n != 0 {
		t.Fatalf("duplicate wrote %d rows", n)
	}
	craft := func(actor uuid.UUID, code string) int64 {
		n, e := q.AssignScopedCanoeCraftClass(ctx, dbgen.AssignScopedCanoeCraftClassParams{ActorID: actor, UserID: member, CraftCode: code})
		if e != nil {
			t.Fatal(e)
		}
		return n
	}
	if n := craft(outsider, "K2"); n != 0 {
		t.Fatalf("outsider assigned craft: %d", n)
	}
	if n := craft(coach, "K2"); n != 1 {
		t.Fatalf("coach assigned craft: %d", n)
	}
	if _, err = tx.Exec(ctx, `UPDATE staff_grants SET revoked_at=now(),revoked_by_id=$1 WHERE user_id=$2 AND capability='COACH'`, admin, coach); err != nil {
		t.Fatal(err)
	}
	if n := assign(coach, "SUP"); n != 0 {
		t.Fatalf("revoked coach assigned sport: %d", n)
	}
	if n := craft(coach, "C2"); n != 0 {
		t.Fatalf("revoked coach assigned craft: %d", n)
	}
	rows, e := q.ListPersonSportingModalities(ctx, member)
	if e != nil || len(rows) != 2 {
		t.Fatalf("modalities=%v err=%v", rows, e)
	}
	var roles int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM user_platform_roles WHERE user_id=$1`, member).Scan(&roles); err != nil || roles != 0 {
		t.Fatalf("classification granted role=%d err=%v", roles, err)
	}
}
