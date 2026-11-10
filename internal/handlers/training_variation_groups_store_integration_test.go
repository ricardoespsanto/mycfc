//go:build integration

package handlers

import (
	"errors"
	"testing"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestDatedVariationGroupStoreChecksCompetitionAndRoster(t *testing.T) {
	w := newRhythmDomainFixture(t)
	ctx := t.Context()
	store := PostgresStructuredTrainingStore{Pool: w.web}
	event := func(days int32) uuid.UUID {
		t.Helper()
		id := uuid.New()
		w.exec(t, `INSERT INTO events(id,title,event_type,starts_at,ends_at,created_by_id) VALUES($1,'Disposable competition','COMPETITION',(CURRENT_DATE+$3::integer)+time '10:00',(CURRENT_DATE+$3::integer)+time '11:00',$2)`, id, w.actor, days)
		return id
	}
	competition := event(14)
	craft := "K2"
	in := StructuredVariationGroupInput{
		Params: dbgen.CreateTrainingVariationGroupParams{
			TrainingGroupID: w.group, Name: "Competition crew", Kind: dbgen.TrainingVariationGroupKindCREW,
			CraftCode: &craft, EffectiveFrom: placementDate(w.day(t, 7)), CompetitionEventID: &competition, CreatedByID: w.actor,
		},
		MembershipIDs: []uuid.UUID{w.membership, w.secondMember},
	}
	reject := func(t *testing.T, in StructuredVariationGroupInput, want error) {
		t.Helper()
		_, err := store.CreateTrainingVariationGroup(t.Context(), in)
		if !errors.Is(err, want) {
			t.Fatalf("variation group error=%v, want %v", err, want)
		}
	}
	unknown, early, absent := uuid.New(), event(2), uuid.New()
	otherSeason, repeatedPerson := uuid.New(), uuid.New()
	w.exec(t, `INSERT INTO seasons(id,code,name,starts_on,ends_on) VALUES($1,'OTHER','Alternate synthetic season',CURRENT_DATE-55,CURRENT_DATE-10)`, otherSeason)
	w.exec(t, `INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on,ends_on) VALUES($1,$2,$3,$4,CURRENT_DATE-55,CURRENT_DATE-10)`, repeatedPerson, w.subject, otherSeason, w.programme)
	for _, tc := range []struct {
		name     string
		update   func(*StructuredVariationGroupInput)
		expected error
	}{
		{"missing competition", func(in *StructuredVariationGroupInput) { in.Params.CompetitionEventID = &unknown }, errStructuredVariationMemberScope},
		{"competition before effective date", func(in *StructuredVariationGroupInput) { in.Params.CompetitionEventID = &early }, errStructuredVariationMemberScope},
		{"unknown membership", func(in *StructuredVariationGroupInput) { in.MembershipIDs = []uuid.UUID{w.membership, absent} }, errStructuredVariationMemberScope},
		{"same person twice", func(in *StructuredVariationGroupInput) { in.MembershipIDs = []uuid.UUID{w.membership, repeatedPerson} }, errStructuredVariationMemberScope},
		{"wrong craft capacity", func(in *StructuredVariationGroupInput) { in.MembershipIDs = []uuid.UUID{w.membership} }, errStructuredVariationCrewCapacity},
		{"actor outside group scope", func(in *StructuredVariationGroupInput) { in.Params.CreatedByID = w.subject }, errStructuredVariationMemberScope},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := in
			tc.update(&bad)
			reject(t, bad, tc.expected)
		})
	}
	// Each contender holds a real conflicting row lock on a separate connection.
	// A failed lock attempt must never leave a partially created group or roster.
	for _, tc := range []struct {
		name, sql string
		id        uuid.UUID
	}{
		{"actor", `SELECT id FROM users WHERE id=$1 FOR UPDATE`, w.actor},
		{"admin grant", `SELECT role_id FROM user_platform_roles WHERE user_id=$1 FOR UPDATE`, w.actor},
		{"athlete", `SELECT id FROM users WHERE id=$1 FOR UPDATE`, w.subject},
		{"placement head", `SELECT revision FROM training_placement_heads WHERE subject_user_id=$1 FOR UPDATE`, w.subject},
		{"training group", `SELECT id FROM training_groups WHERE id=$1 FOR UPDATE`, w.group},
		{"membership", `SELECT id FROM user_memberships WHERE id=$1 FOR UPDATE`, w.membership},
	} {
		t.Run(tc.name, func(t *testing.T) {
			holdTrainingFixtureRow(t, w.publicationDomainFixture, tc.sql, tc.id, func() {
				reject(t, in, errStructuredTrainingPublicationConflict)
			})
		})
	}
	group, err := store.CreateTrainingVariationGroup(ctx, in)
	if err != nil || group.ID == uuid.Nil || group.CompetitionEventID == nil || *group.CompetitionEventID != competition || group.EffectiveUntil.Valid {
		t.Fatalf("competition crew was not stored with its event-derived endpoint: %+v, %v", group, err)
	}
	var membershipCount int
	if err := w.web.QueryRow(ctx, `SELECT count(*) FROM training_variation_group_members WHERE variation_group_id=$1`, group.ID).Scan(&membershipCount); err != nil || membershipCount != 2 {
		t.Fatalf("competition crew membership count=%d, %v", membershipCount, err)
	}
	var pgErr *pgconn.PgError
	if _, err := store.CreateTrainingVariationGroup(ctx, in); !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("duplicate crew name unexpectedly accepted: %v", err)
	}
	selfMember := uuid.New()
	w.exec(t, `INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on,ends_on) VALUES($1,$2,$3,$4,CURRENT_DATE,CURRENT_DATE+300)`, selfMember, w.actor, w.season, w.programme)
	w.place(t, w.actor, w.group, w.season, w.day(t, 0))
	self := StructuredVariationGroupInput{
		Params:        dbgen.CreateTrainingVariationGroupParams{TrainingGroupID: w.group, Name: "Athlete who is also admin", Kind: dbgen.TrainingVariationGroupKindSUBGROUP, EffectiveFrom: in.Params.EffectiveFrom, CreatedByID: w.actor},
		MembershipIDs: []uuid.UUID{selfMember},
	}
	if group, err := store.CreateTrainingVariationGroup(ctx, self); err != nil || group.ID == uuid.Nil {
		t.Fatalf("eligible actor-athlete could not join subgroup: %+v, %v", group, err)
	}
	var groupCount int
	if err := w.web.QueryRow(ctx, `SELECT count(*) FROM training_variation_groups WHERE training_group_id=$1`, w.group).Scan(&groupCount); err != nil || groupCount != 2 {
		t.Fatalf("failed requests changed persisted groups: count=%d, %v", groupCount, err)
	}
}
