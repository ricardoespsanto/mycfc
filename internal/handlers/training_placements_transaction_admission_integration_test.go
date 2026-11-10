//go:build integration

package handlers

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestPlacementPGConfirmRequiresRuntimePrivileges(t *testing.T) {
	f := newPublicationDomainFixture(t)
	ctx := t.Context()
	store := PostgresTrainingPlacementStore{Pool: f.web}
	other := uuid.New()
	f.exec(t, `INSERT INTO training_groups(id,name,programme_id,created_by_id) VALUES($1,'Scoped destination',$2,$3)`, other, f.programme, f.actor)
	webRole := pgx.Identifier{f.roles.AppUsername}.Sanitize()
	for _, tc := range []struct {
		name, function string
		enablePartial  bool
		want           error
	}{
		{"runtime status", "training_schedule_control.status()", false, errTrainingPlacementUnavailable},
		{"partial admission", "public.training_schedule_admit_partial(uuid,bigint)", true, pgx.ErrNoRows},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.enablePartial {
				setPublicationFixtureEnabled(t, f, true)
			}
			review := f.placementReview(t, trainingPlacementCommand{Kind: placementMove, SubjectID: f.subject, ContextGroupID: f.group, SourcePlacementID: f.placement, TargetGroupID: other, EffectiveOn: f.day(t, 2)})
			before := f.placementAggregate(t)
			if _, err := f.root.Exec(ctx, `REVOKE EXECUTE ON FUNCTION `+tc.function+` FROM `+webRole); err != nil {
				t.Fatal(err)
			}
			out, err := store.ConfirmPlacement(ctx, review)
			if _, grantErr := f.root.Exec(ctx, `GRANT EXECUTE ON FUNCTION `+tc.function+` TO `+webRole); grantErr != nil {
				t.Fatal(grantErr)
			}
			if !errors.Is(err, tc.want) || out != (trainingPlacementSaveResult{}) || f.placementAggregate(t) != before {
				t.Fatalf("runtime privilege loss returned result=%+v err=%v or changed the placement aggregate", out, err)
			}
		})
	}
}

func TestPlacementPGBatchLockConflictsPreserveCohortAndVersion(t *testing.T) {
	f := newPublicationDomainFixture(t)
	ctx := t.Context()
	store := PostgresTrainingGroupStore{Pool: f.web}
	// The previous placement ends before the selected date but leaves an existing
	// revision head for the batch to lock alongside a brand-new athlete.
	f.changePlacement(t, trainingPlacementCommand{Kind: placementLeave, SubjectID: f.subject, ContextGroupID: f.group, SourcePlacementID: f.placement, EffectiveOn: f.day(t, 1)})
	newPerson, newMember := uuid.New(), uuid.New()
	f.exec(t, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Recipient new batch athlete',$2,'hash','1990-01-01')`, newPerson, newPerson.String()+"@example.test")
	f.exec(t, `INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on,ends_on) VALUES($1,$2,$3,$4,CURRENT_DATE,CURRENT_DATE+90)`, newMember, newPerson, f.season, f.programme)
	on := placementDate(f.day(t, 2)).Time
	group, err := store.GetGroup(ctx, f.actor, f.group)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := store.SearchAthletesForDate(ctx, f.actor, f.group, f.season, on, "Recipient", 0)
	if err != nil || len(rows) != 2 || rows[0].Unavailable || rows[1].Unavailable {
		t.Fatalf("expected two eligible athletes, got %+v, err=%v", rows, err)
	}
	in := TrainingGroupRosterInput{ActorID: f.actor, GroupID: f.group, SeasonID: f.season, CommandID: uuid.New(), EffectiveOn: on, ExpectedUpdatedAt: group.UpdatedAt, MembershipIDs: []uuid.UUID{f.membership, newMember}}
	denied := in
	denied.ActorID = f.subject
	if proof, err := store.ReviewAthleteSelection(ctx, denied, rows); !errors.Is(err, pgx.ErrNoRows) || proof != "" {
		t.Fatalf("outside actor obtained review proof=%t err=%v", proof != "", err)
	}
	forged := append([]TrainingGroupAthlete(nil), rows...)
	forged[0].UserID = uuid.New()
	if proof, err := store.ReviewAthleteSelection(ctx, in, forged); !errors.Is(err, pgx.ErrNoRows) || proof != "" {
		t.Fatalf("missing athlete obtained review proof=%t err=%v", proof != "", err)
	}
	in.SelectionProof, err = store.ReviewAthleteSelection(ctx, in, rows)
	if err != nil || in.SelectionProof == "" {
		t.Fatalf("valid cohort did not receive a proof: %v", err)
	}
	before := f.placementAggregate(t)
	for _, tc := range []struct {
		name, lock string
		id         uuid.UUID
	}{
		{"actor row", `SELECT id FROM users WHERE id=$1 FOR UPDATE`, f.actor},
		{"authority row", `SELECT role_id FROM user_platform_roles WHERE user_id=$1 FOR UPDATE`, f.actor},
		{"athlete row", `SELECT id FROM users WHERE id=$1 FOR UPDATE`, newPerson},
		{"existing placement head", `SELECT programme_id FROM training_placement_heads WHERE subject_user_id=$1 FOR UPDATE`, f.subject},
		{"group version row", `SELECT id FROM training_groups WHERE id=$1 FOR UPDATE`, f.group},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := f.root.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			var id uuid.UUID
			if err = tx.QueryRow(t.Context(), tc.lock, tc.id).Scan(&id); err != nil {
				t.Fatal(err)
			}
			out, err := store.AddAthletes(t.Context(), in)
			if !errors.Is(err, ErrTrainingGroupConflict) || out.Added != 0 || out.Group.ID != uuid.Nil {
				t.Fatalf("blocked cohort returned result=%+v err=%v", out, err)
			}
			if err = tx.Rollback(t.Context()); err != nil {
				t.Fatal(err)
			}
			current, err := store.GetGroup(t.Context(), f.actor, f.group)
			if err != nil || !current.UpdatedAt.Equal(group.UpdatedAt) || f.placementAggregate(t) != before {
				t.Fatalf("conflict modified group or placement aggregate: %v", err)
			}
		})
	}
	out, err := store.AddAthletes(ctx, in)
	if err != nil || out.Added != 2 || !out.Group.UpdatedAt.After(group.UpdatedAt) {
		t.Fatalf("unchanged reviewed cohort could not commit after locks were released: %+v %v", out, err)
	}
}
