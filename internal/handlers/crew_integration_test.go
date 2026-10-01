//go:build integration

package handlers

import (
	"context"
	"errors"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"testing"
	"time"
)

func TestPostgresTemporaryCrewsRequireDatedScopedComposition(t *testing.T) {
	ctx, pool := integrationPool(t)
	actor, outsider := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{actor, outsider} {
		if _, err := pool.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Crew actor',$2,'hash','1980-01-01')`, id, uuid.NewString()+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	var programme uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM programmes WHERE code='Leisure'`).Scan(&programme); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO staff_grants(user_id,capability,programme_id,granted_by_id) VALUES($1,'COACH',$2,$3)`, actor, programme, actor); err != nil {
		t.Fatal(err)
	}
	season, group := uuid.New(), uuid.New()
	now := time.Now().UTC()
	from, until := now.AddDate(0, 0, -10), now.AddDate(0, 0, 10)
	if _, err := pool.Exec(ctx, `INSERT INTO seasons(id,code,name,starts_on,ends_on) VALUES($1,$2,'Crews',$3,$4)`, season, "CG_"+uuid.NewString()[:8], from, until); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO training_groups(id,name,programme_id,created_by_id) VALUES($1,$2,$3,$4)`, group, "Crews "+uuid.NewString()[:8], programme, actor); err != nil {
		t.Fatal(err)
	}
	members := make([]uuid.UUID, 4)
	users := make([]uuid.UUID, 4)
	for i := range members {
		users[i], members[i] = uuid.New(), uuid.New()
		if _, err := pool.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Athlete',$2,'hash','1990-01-01')`, users[i], uuid.NewString()+"@example.test"); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on) VALUES($1,$2,$3,$4,$5)`, members[i], users[i], season, programme, from); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO training_group_members(group_id,membership_id,added_by_id) VALUES($1,$2,$3)`, group, members[i], actor); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM training_groups WHERE id=$1`, group)
		_, _ = pool.Exec(c, `DELETE FROM user_memberships WHERE id=ANY($1)`, members)
		_, _ = pool.Exec(c, `DELETE FROM seasons WHERE id=$1`, season)
		_, _ = pool.Exec(c, `DELETE FROM staff_grants WHERE user_id=$1`, actor)
		_, _ = pool.Exec(c, `DELETE FROM users WHERE id=ANY($1)`, append(users, actor, outsider))
	})
	store := PostgresStructuredTrainingStore{Pool: pool}
	create := func(code, name string, actorID uuid.UUID, ids []uuid.UUID, start time.Time) error {
		_, err := store.CreateTrainingVariationGroup(ctx, StructuredVariationGroupInput{Params: dbgen.CreateTrainingVariationGroupParams{TrainingGroupID: group, Name: name, Kind: dbgen.TrainingVariationGroupKindCREW, CraftCode: &code, EffectiveFrom: pgtype.Date{Time: start, Valid: true}, EffectiveUntil: pgtype.Date{Time: until, Valid: true}, CreatedByID: actorID}, MembershipIDs: ids})
		return err
	}
	if _, err := store.CreateTrainingVariationGroup(ctx, StructuredVariationGroupInput{Params: dbgen.CreateTrainingVariationGroupParams{TrainingGroupID: uuid.New(), Kind: dbgen.TrainingVariationGroupKindCREW, CreatedByID: actor}}); err == nil {
		t.Fatal("missing crew group accepted")
	}
	if err := create("C2", "C2 crew", actor, members[:2], from); err != nil {
		t.Fatal(err)
	}
	if err := create("K2", "K2 crew", actor, members[1:3], from); err != nil {
		t.Fatal(err)
	}
	if err := create("K4", "K4 crew", actor, members, from); err != nil {
		t.Fatal(err)
	}
	if err := create("K2", "undersized", actor, members[:1], from); !errors.Is(err, errStructuredVariationCrewCapacity) {
		t.Fatalf("undersized: %v", err)
	}
	if err := create("K2", "duplicate", actor, []uuid.UUID{members[0], members[0]}, from); !errors.Is(err, errStructuredVariationMemberScope) {
		t.Fatalf("duplicate: %v", err)
	}
	if err := create("K2", "unscoped", outsider, members[:2], from); !errors.Is(err, errStructuredVariationMemberScope) {
		t.Fatalf("unscoped actor: %v", err)
	}
	if err := create("C2", "outside dates", actor, members[:2], from.AddDate(0, 0, -1)); !errors.Is(err, errStructuredVariationMemberScope) {
		t.Fatalf("outside dates: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM user_memberships WHERE user_id=$1`, users[1]).Scan(&n); err != nil || n != 1 {
		t.Fatalf("participation=%d err=%v", n, err)
	}
}
