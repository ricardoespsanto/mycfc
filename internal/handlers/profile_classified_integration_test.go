//go:build integration

package handlers

import (
	"errors"
	"testing"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestClassifiedProfileUpdatePreservesBirthDateProtection(t *testing.T) {
	ctx, pool := integrationPool(t)
	subject, actor := uuid.New(), uuid.New()
	email := "classified-" + subject.String() + "@example.test"
	for _, id := range []uuid.UUID{subject, actor} {
		if _, err := pool.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Adulto',$2,'hash','2000-01-01')`, id, id.String()+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	var season, programme, category uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO seasons(code,name,starts_on,ends_on) VALUES($1,'Perfil classificado',CURRENT_DATE-30,CURRENT_DATE+30) RETURNING id`, "T"+subject.String()[:8]).Scan(&season); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM programmes WHERE code='Competition'`).Scan(&programme); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO competition_categories(season_id,programme_id,code,name_pt,approved_by_user_id,approved_at) VALUES($1,$2,$3,'Adultos',$4,now()) RETURNING id`, season, programme, "T"+subject.String()[:8], actor).Scan(&category); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_memberships(user_id,season_id,programme_id,competition_category_id,starts_on,ends_on) SELECT $1,id,$3,$4,CURRENT_DATE,ends_on FROM seasons WHERE id=$2`, subject, season, programme, category); err != nil {
		t.Fatal(err)
	}
	store := PostgresProfileStore{Pool: pool}
	current, err := store.View(ctx, actor, subject, true)
	if err != nil {
		t.Fatal(err)
	}
	number := "12345678"
	input := ProfileUpdate{ActorID: actor, SubjectID: subject, IsAdmin: true,
		Profile:       dbgen.UpdateMemberProfileParams{MedicalDeclaration: "UNKNOWN", FederationLicenceNumber: &number, ExpectedUpdatedAt: current.UpdatedAt},
		Identity:      &dbgen.UpdateMemberIdentityParams{Name: "Adulto atualizado", Email: &email, DateOfBirth: current.DateOfBirth, ExpectedUpdatedAt: current.IdentityUpdatedAt},
		ChangedFields: []string{"federation_licence_number"}, IdentityFields: []string{"name", "email"}}
	if err := store.Update(ctx, input); err != nil {
		t.Fatalf("non-DOB classified profile update: %v", err)
	}
	updated, err := store.View(ctx, actor, subject, true)
	if err != nil || updated.Name != "Adulto atualizado" || updated.FederationLicenceNumber == nil || *updated.FederationLicenceNumber != number || updated.DateOfBirth != current.DateOfBirth {
		t.Fatalf("updated profile=%#v err=%v", updated, err)
	}
	if err := store.Update(ctx, input); !errors.Is(err, ErrProfileConflict) {
		t.Fatalf("stale identity update=%v", err)
	}
	input.Profile.ExpectedUpdatedAt = updated.UpdatedAt
	input.Identity.ExpectedUpdatedAt = updated.IdentityUpdatedAt
	input.Identity.DateOfBirth = pgtype.Date{Time: current.DateOfBirth.Time.AddDate(0, 0, 1), Valid: true}
	err = store.Update(ctx, input)
	var constraint *pgconn.PgError
	if !errors.As(err, &constraint) || constraint.Code != "23514" {
		t.Fatalf("changed classified DOB should remain rejected: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT date_of_birth FROM users WHERE id=$1`, subject).Scan(&input.Identity.DateOfBirth); err != nil {
		t.Fatal(err)
	}
	if input.Identity.DateOfBirth != current.DateOfBirth {
		t.Fatal("rejected edit changed DOB")
	}
}
