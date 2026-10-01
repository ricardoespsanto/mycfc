//go:build integration

package db

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestTemporaryCanoeCrewsOverlapWithoutParticipationWrites(t *testing.T) {
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
	actor, athlete := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{actor, athlete} {
		if _, err := tx.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Crew test',$2,'hash','1990-01-01')`, id, uuid.NewString()+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	var programme uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM programmes WHERE code='Leisure'`).Scan(&programme); err != nil {
		t.Fatal(err)
	}
	season, membership, group := uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC()
	from, until := now.AddDate(0, 0, -10), now.AddDate(0, 0, 10)
	if _, err := tx.Exec(ctx, `INSERT INTO seasons(id,code,name,starts_on,ends_on) VALUES($1,$2,'Test',$3,$4)`, season, "CR_"+uuid.NewString()[:8], from, until); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on) VALUES($1,$2,$3,$4,$5)`, membership, athlete, season, programme, from); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO training_groups(id,name,programme_id,created_by_id) VALUES($1,$2,$3,$4)`, group, "Crews "+uuid.NewString()[:8], programme, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO training_group_members(group_id,membership_id,added_by_id) VALUES($1,$2,$3)`, group, membership, actor); err != nil {
		t.Fatal(err)
	}
	for _, craft := range []string{"C2", "K2", "K4"} {
		var crew uuid.UUID
		err := tx.QueryRow(ctx, `INSERT INTO training_variation_groups(training_group_id,name,kind,craft_code,effective_from,effective_until,created_by_id) VALUES($1,$2,'CREW',$3,$4,$5,$6) RETURNING id`, group, "Crew "+craft, craft, from, until, actor).Scan(&crew)
		if err != nil {
			t.Fatalf("%s: %v", craft, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO training_variation_group_members(variation_group_id,membership_id,added_by_id) VALUES($1,$2,$3)`, crew, membership, actor); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM user_memberships WHERE user_id=$1`, athlete).Scan(&count); err != nil || count != 1 {
		t.Fatalf("participation count %d: %v", count, err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO training_variation_groups(training_group_id,name,kind,craft_code,effective_from,effective_until,created_by_id) VALUES($1,'Invalid craft','CREW','C4',$2,$3,$4)`, group, from, until, actor)
	if err == nil {
		t.Fatal("C4 accepted as temporary crew")
	}
}
