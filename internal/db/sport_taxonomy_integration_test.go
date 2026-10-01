//go:build integration

package db

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestSportTaxonomyIndependentOfParticipationAndLegacyReferences(t *testing.T) {
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
	_, err = tx.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES
 ($1,'Staff teste',$3,'hash','1980-01-01'),($2,'Membro teste',$4,'hash','2000-01-01')`, actor, member, "actor-"+uuid.NewString()+"@example.test", "member-"+uuid.NewString()+"@example.test")
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO seasons(id,code,name,starts_on,ends_on) VALUES($1,$2,'Época teste','2026-01-01','2026-12-31')`, season, "IT_"+uuid.NewString()[:8])
	if err != nil {
		t.Fatal(err)
	}
	var leisure, polo, legacy uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT id FROM programmes WHERE code='Leisure'`).Scan(&leisure); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT id FROM programmes WHERE code='Kayak_Polo'`).Scan(&polo); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT id FROM modalities WHERE code='K2'`).Scan(&legacy); err != nil {
		t.Fatal(err)
	}
	var membership uuid.UUID
	if err = tx.QueryRow(ctx, `INSERT INTO user_memberships(user_id,season_id,programme_id,starts_on) VALUES($1,$2,$3,'2026-01-01') RETURNING id`, member, season, leisure).Scan(&membership); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO membership_modalities(membership_id,modality_id) VALUES($1,$2)`, membership, legacy); err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"CANOEING", "KAYAK_POLO", "SUP"} {
		if _, err = tx.Exec(ctx, `INSERT INTO person_sporting_modalities(user_id,modality_code) VALUES($1,$2)`, member, code); err != nil {
			t.Fatalf("%s: %v", code, err)
		}
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM person_sporting_modalities WHERE user_id=$1`, member).Scan(&count); err != nil || count != 3 {
		t.Fatalf("sporting modalities count=%d err=%v", count, err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO person_canoe_craft_classes(user_id,modality_code,craft_code) VALUES($1,'CANOEING','K2')`, member); err != nil {
		t.Fatal(err)
	}
	reject := func(statement string, args []any, state string) {
		t.Helper()
		nested, e := tx.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		_, e = nested.Exec(ctx, statement, args...)
		if !sqlState(e, state) {
			t.Errorf("wanted SQLSTATE %s, got %v", state, e)
		}
		if e = nested.Rollback(ctx); e != nil {
			t.Fatal(e)
		}
	}
	reject(`INSERT INTO person_canoe_craft_classes(user_id,modality_code,craft_code) VALUES($1,'SUP','K1')`, []any{member}, "23514")
	reject(`INSERT INTO person_canoe_craft_classes(user_id,modality_code,craft_code) VALUES($1,'CANOEING','K3')`, []any{member}, "23503")
	reject(`INSERT INTO person_sporting_modalities(user_id,modality_code) VALUES($1,'K1')`, []any{member}, "23503")
	reject(`UPDATE sporting_modalities SET name_pt='Caiaque individual' WHERE code='CANOEING'`, nil, "23514")
	reject(`DELETE FROM canoe_craft_classes WHERE code='C4'`, nil, "23514")
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM membership_modalities WHERE membership_id=$1 AND modality_id=$2`, membership, legacy).Scan(&count); err != nil || count != 1 {
		t.Fatalf("legacy reference changed: %d %v", count, err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO teams(season_id,programme_id,code,name) VALUES($1,$2,'POLO','Equipa de polo')`, season, polo); err != nil {
		t.Fatal(err)
	}
	reject(`INSERT INTO teams(season_id,programme_id,code,name) VALUES($1,$2,'POLO2','Outra equipa')`, []any{season, polo}, "23505")
}
