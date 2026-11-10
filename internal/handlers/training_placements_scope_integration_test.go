//go:build integration

package handlers

import (
	"errors"
	"testing"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPlacementStoreOnlyReturnsScopedTimelineAndRetainedSource(t *testing.T) {
	f := newPublicationDomainFixture(t)
	ctx := t.Context()
	store := PostgresTrainingPlacementStore{Pool: f.web}
	scope, err := store.PlacementContext(ctx, f.actor, f.group)
	if err != nil || scope.Group.ID != f.group || scope.ProgrammeID != f.programme || len(scope.Seasons) != 1 || scope.Seasons[0].ID != f.season {
		t.Fatalf("authorized context=%+v err=%v", scope, err)
	}
	if _, err = store.PlacementContext(ctx, f.subject, f.group); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("unscoped actor read context: %v", err)
	}
	if _, err = store.PlacementView(ctx, f.actor, f.group, f.subject, uuid.New(), f.day(t, 0)); !errors.Is(err, errTrainingPlacementInvalid) {
		t.Fatalf("foreign season admitted: %v", err)
	}
	if _, err = store.PlacementView(ctx, f.actor, f.group, f.subject, f.season, "2026-02-30"); !errors.Is(err, errTrainingPlacementInvalid) {
		t.Fatalf("invalid date admitted: %v", err)
	}
	if _, err = store.PlacementView(ctx, f.actor, f.group, uuid.New(), f.season, f.day(t, 0)); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("handtyped person lookup admitted: %v", err)
	}
	view, err := store.PlacementView(ctx, f.actor, f.group, f.subject, f.season, f.day(t, 0))
	if err != nil || view.PersonID != f.subject || len(view.Timeline) != 1 || view.Timeline[0].ID != f.placement || len(view.Groups) != 1 {
		t.Fatalf("scoped timeline=%+v err=%v", view, err)
	}

	// Ending today preserves the recorded past. On a later configured date the
	// existing source placement remains the only valid directory anchor.
	f.exec(t, `UPDATE user_memberships SET ends_on=(clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date WHERE id=$1`, f.membership)
	view, err = store.PlacementView(ctx, f.actor, f.group, f.subject, f.season, f.day(t, 2))
	if err != nil || view.PersonID != f.subject || len(view.Timeline) != 1 || view.Timeline[0].ID != f.placement {
		t.Fatalf("retained source not available for review: %+v %v", view, err)
	}
	otherGroup := uuid.New()
	f.exec(t, `INSERT INTO training_groups(id,name,programme_id,created_by_id) VALUES($1,'Synthetic sibling group',$2,$3)`, otherGroup, f.programme, f.actor)
	if _, err = store.PlacementView(ctx, f.actor, otherGroup, f.subject, f.season, f.day(t, 2)); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("sibling group borrowed retained source: %v", err)
	}
	f.exec(t, `INSERT INTO training_groups(name,programme_id,created_by_id) SELECT 'Synthetic group '||n,$1,$2 FROM generate_series(1,$3::int)n`, f.programme, f.actor, trainingPlacementMaxTimeline)
	if _, err = store.PlacementView(ctx, f.actor, f.group, f.subject, f.season, f.day(t, 2)); !errors.Is(err, errTrainingPlacementLimit) {
		t.Fatalf("oversize target choices disclosed: %v", err)
	}
	f.exec(t, `INSERT INTO seasons(code,name,starts_on,ends_on) SELECT 'LIM_'||n,'Synthetic limit season',CURRENT_DATE-1,CURRENT_DATE+20 FROM generate_series(1,100)n`)
	if _, err = store.PlacementContext(ctx, f.actor, f.group); !errors.Is(err, errTrainingPlacementLimit) {
		t.Fatalf("oversize season choices disclosed: %v", err)
	}
}

func TestPlacementStoreRefusesReadsWhenRuntimeCannotBeVerified(t *testing.T) {
	f := newPublicationDomainFixture(t)
	closed, err := pgxpool.NewWithConfig(t.Context(), f.web.Config())
	if err != nil {
		t.Fatal(err)
	}
	closed.Close()
	store := PostgresTrainingPlacementStore{Pool: closed}
	ctx := t.Context()
	if _, err = store.ReadPlacement(ctx, f.actor, trainingPlacementCommand{}, false); !errors.Is(err, errTrainingPlacementUnavailable) {
		t.Fatalf("failed runtime admitted placement read: %v", err)
	}
	if _, err = store.ReadPlacementCancellation(ctx, f.actor, f.group, f.subject, uuid.New()); !errors.Is(err, errTrainingPlacementUnavailable) {
		t.Fatalf("failed runtime admitted cancellation read: %v", err)
	}
	if _, err = store.PlacementView(ctx, f.actor, f.group, f.subject, f.season, f.day(t, 0)); !errors.Is(err, errTrainingPlacementUnavailable) {
		t.Fatalf("failed runtime admitted timeline: %v", err)
	}
}

func TestTrainingGroupStoreSelfJoinUsesCurrentProofAndVersion(t *testing.T) {
	f := newPublicationDomainFixture(t)
	setPublicationFixtureEnabled(t, f, true)
	ctx := t.Context()
	store := PostgresTrainingGroupStore{Pool: f.web}
	record, err := store.GetGroup(ctx, f.actor, f.group)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ListGroups(ctx, f.actor, -1); !errors.Is(err, ErrTrainingGroupEligibility) {
		t.Fatalf("negative list offset: %v", err)
	}
	if _, err = store.ListRoster(ctx, f.actor, f.group, 1981); !errors.Is(err, ErrTrainingGroupEligibility) {
		t.Fatalf("oversize roster offset: %v", err)
	}
	if _, err = store.SearchAthletes(ctx, f.subject, f.group, "Recipient", 0); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("outsider searched: %v", err)
	}
	if _, err = store.PlacementSeasons(ctx, f.subject, f.group); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("outsider obtained seasons: %v", err)
	}
	if _, err = store.AllowsWeek(ctx, f.subject, f.group, f.plan); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("outsider obtained return context: %v", err)
	}
	if rows, err := store.SearchAthletes(ctx, f.actor, f.group, "Recipient", 0); err != nil || len(rows) == 0 {
		t.Fatalf("scoped compatibility search rows=%d err=%v", len(rows), err)
	}

	membership := uuid.New()
	f.exec(t, `INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on,ends_on) VALUES($1,$2,$3,$4,CURRENT_DATE,CURRENT_DATE+90)`, membership, f.actor, f.season, f.programme)
	in := TrainingGroupRosterInput{ActorID: f.actor, GroupID: f.group, SeasonID: f.season, CommandID: uuid.New(), EffectiveOn: placementDate(f.day(t, 0)).Time, ExpectedUpdatedAt: record.UpdatedAt, MembershipIDs: []uuid.UUID{membership}}
	rows := []TrainingGroupAthlete{{MembershipID: membership, UserID: f.actor, GroupID: f.group}}
	if _, err = store.ReviewAthleteSelection(ctx, in, nil); !errors.Is(err, errTrainingPlacementInvalid) {
		t.Fatalf("empty selection reviewed: %v", err)
	}
	wrong := append([]TrainingGroupAthlete(nil), rows...)
	wrong[0].MembershipID = uuid.New()
	if _, err = store.ReviewAthleteSelection(ctx, in, wrong); !errors.Is(err, errTrainingPlacementChanged) {
		t.Fatalf("substituted membership reviewed: %v", err)
	}
	wrong[0].GroupID = uuid.New()
	if _, err = store.ReviewAthleteSelection(ctx, in, wrong); !errors.Is(err, errTrainingPlacementInvalid) {
		t.Fatalf("foreign group reviewed: %v", err)
	}
	in.SelectionProof, err = store.ReviewAthleteSelection(ctx, in, rows)
	if err != nil || in.SelectionProof == "" {
		t.Fatalf("eligible coach could not review own athlete placement: %v", err)
	}
	if err = dbgen.New(f.web).TouchTaskTrainingGroup(ctx, f.group); err != nil {
		t.Fatal(err)
	}
	if _, err = store.AddAthletes(ctx, in); !errors.Is(err, ErrTrainingGroupConflict) {
		t.Fatalf("stale reviewed group accepted: %v", err)
	}
	if _, err = store.ReviewAthleteSelection(ctx, in, rows); !errors.Is(err, ErrTrainingGroupConflict) {
		t.Fatalf("stale group rereview accepted: %v", err)
	}
	current, err := store.GetGroup(ctx, f.actor, f.group)
	if err != nil {
		t.Fatal(err)
	}
	in.ExpectedUpdatedAt = current.UpdatedAt
	in.SelectionProof, err = store.ReviewAthleteSelection(ctx, in, rows)
	if err != nil {
		t.Fatal(err)
	}
	added, err := store.AddAthletes(ctx, in)
	if err != nil || added.Added != 1 {
		t.Fatalf("fresh self-join=%+v err=%v", added, err)
	}
	current, err = store.GetGroup(ctx, f.actor, f.group)
	if err != nil {
		t.Fatal(err)
	}
	in.ExpectedUpdatedAt = current.UpdatedAt
	in.CommandID = uuid.New()
	if proof, err := store.ReviewAthleteSelection(ctx, in, rows); err != nil || proof != "" || !rows[0].Unavailable {
		t.Fatalf("already placed actor received duplicate JOIN offer: available=%t err=%v", !rows[0].Unavailable, err)
	}
}
