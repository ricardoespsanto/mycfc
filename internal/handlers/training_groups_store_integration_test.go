//go:build integration

package handlers

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestTrainingGroupTasksScopeRosterAndConcurrency(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	prefix := "Task " + uuid.NewString()[:8] + " "
	user := func(name string) uuid.UUID {
		id := uuid.New()
		exec(`INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,$2,$3,'hash','1990-01-01')`, id, prefix+name, id.String()+"@example.test")
		return id
	}
	admin, coach, teamCoach := user("Group admin"), user("Group coach"), user("Team coach")
	exec(`INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, admin)
	var programme, otherProgramme uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM programmes WHERE code='Leisure'`).Scan(&programme); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM programmes WHERE code='Competition'`).Scan(&otherProgramme); err != nil {
		t.Fatal(err)
	}
	season, team := uuid.New(), uuid.New()
	exec(`INSERT INTO seasons(id,code,name,starts_on,ends_on) VALUES($1,$2,'Task season',CURRENT_DATE-10,CURRENT_DATE+10)`, season, "G"+uuid.NewString()[:8])
	exec(`INSERT INTO teams(id,season_id,programme_id,code,name) VALUES($1,$2,$3,$4,'Empty team')`, team, season, programme, "T"+uuid.NewString()[:8])
	exec(`INSERT INTO staff_grants(user_id,capability,programme_id,granted_by_id) VALUES($1,'COACH',$2,$3)`, coach, programme, admin)
	exec(`INSERT INTO staff_grants(user_id,capability,team_id,granted_by_id) VALUES($1,'COACH',$2,$3)`, teamCoach, team, admin)
	store := PostgresTrainingGroupStore{Pool: pool}
	scopes, err := store.ListScopes(ctx, coach)
	if err != nil || len(scopes) != 1 || scopes[0].ProgrammeID == nil || *scopes[0].ProgrammeID != programme || scopes[0].TeamID != nil {
		t.Fatalf("configured empty programme scopes %#v %v", scopes, err)
	}
	scopes, err = store.ListScopes(ctx, teamCoach)
	if err != nil || len(scopes) != 1 || scopes[0].TeamID == nil || *scopes[0].TeamID != team || scopes[0].ProgrammeID != nil {
		t.Fatalf("exact team scopes %#v %v", scopes, err)
	}
	if _, err := store.CreateGroup(ctx, TrainingGroupCreateInput{ActorID: coach, Name: "Forged team", TeamID: &team}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("programme grant broadened to team %v", err)
	}
	if _, err := store.CreateGroup(ctx, TrainingGroupCreateInput{ActorID: teamCoach, Name: "Forged programme", ProgrammeID: &programme}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("team grant broadened %v", err)
	}
	group, err := store.CreateGroup(ctx, TrainingGroupCreateInput{ActorID: coach, Name: "Empty group", ProgrammeID: &programme})
	if err != nil {
		t.Fatal(err)
	}
	if group.MemberCount != 0 || group.LatestWeekID != nil {
		t.Fatalf("empty group manufactured setup %#v", group)
	}
	if _, err := store.GetGroup(ctx, teamCoach, group.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("unscoped group read %v", err)
	}
	membership := func(name string, p uuid.UUID) uuid.UUID {
		id := user(name)
		m := uuid.New()
		exec(`INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on) VALUES($1,$2,$3,$4,CURRENT_DATE-1)`, m, id, season, p)
		return m
	}

	// Hidden/ineligible subjects cannot leak via search or be smuggled into an add batch.
	var hidden []uuid.UUID
	inactive := membership("Private inactive", programme)
	exec(`UPDATE users SET is_active=false WHERE id=(SELECT user_id FROM user_memberships WHERE id=$1)`, inactive)
	hidden = append(hidden, inactive)
	minor, minorMembership := uuid.New(), uuid.New()
	exec(`INSERT INTO users(id,name,is_dependent,date_of_birth) VALUES($1,$2,true,'2015-01-01')`, minor, prefix+"Private unverified minor")
	exec(`INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on) VALUES($1,$2,$3,$4,CURRENT_DATE-1)`, minorMembership, minor, season, programme)
	hidden = append(hidden, minorMembership)
	foundHidden, e := store.SearchAthletes(ctx, coach, group.ID, prefix+"Private", 0)
	if e != nil || len(foundHidden) != 0 {
		t.Fatalf("hidden subject search %#v %v", foundHidden, e)
	}
	for _, id := range hidden {
		if _, e := store.AddAthletes(ctx, TrainingGroupRosterInput{ActorID: coach, GroupID: group.ID, ExpectedUpdatedAt: group.UpdatedAt, MembershipIDs: []uuid.UUID{id}}); !errors.Is(e, ErrTrainingGroupEligibility) {
			t.Fatalf("hidden subject add %v", e)
		}
	}
	first, second := membership("Literal % athlete", programme), membership("Second athlete", programme)
	blank, err := store.SearchAthletes(ctx, coach, group.ID, "", 0)
	if err != nil || len(blank) != 0 {
		t.Fatalf("blank roster disclosure %#v %v", blank, err)
	}
	found, err := store.SearchAthletes(ctx, coach, group.ID, prefix+"Literal %", 0)
	if err != nil || len(found) != 1 || found[0].MembershipID != first {
		t.Fatalf("wildcard must be literal %#v %v", found, err)
	}
	added, err := store.AddAthletes(ctx, TrainingGroupRosterInput{ActorID: coach, GroupID: group.ID, ExpectedUpdatedAt: group.UpdatedAt, MembershipIDs: []uuid.UUID{first}})
	if err != nil || added.Added != 1 || added.Group.MemberCount != 1 {
		t.Fatalf("add %#v %v", added, err)
	}
	if _, err := store.AddAthletes(ctx, TrainingGroupRosterInput{ActorID: coach, GroupID: group.ID, ExpectedUpdatedAt: group.UpdatedAt, MembershipIDs: []uuid.UUID{second}}); !errors.Is(err, ErrTrainingGroupConflict) {
		t.Fatalf("stale roster %v", err)
	}
	same, err := store.AddAthletes(ctx, TrainingGroupRosterInput{ActorID: coach, GroupID: group.ID, ExpectedUpdatedAt: added.Group.UpdatedAt, MembershipIDs: []uuid.UUID{first}})
	if err != nil || same.Added != 0 || !same.Group.UpdatedAt.Equal(added.Group.UpdatedAt) {
		t.Fatalf("idempotent duplicate %#v %v", same, err)
	}
	if _, err := store.AddAthletes(ctx, TrainingGroupRosterInput{ActorID: coach, GroupID: group.ID, ExpectedUpdatedAt: same.Group.UpdatedAt, MembershipIDs: []uuid.UUID{second, uuid.New()}}); !errors.Is(err, ErrTrainingGroupEligibility) {
		t.Fatalf("invalid batch %v", err)
	}
	roster, err := store.ListRoster(ctx, coach, group.ID, 0)
	if err != nil || len(roster) != 1 || roster[0].MembershipID != first {
		t.Fatalf("batch partially wrote %#v %v", roster, err)
	}
	renamed, err := store.RenameGroup(ctx, TrainingGroupRenameInput{ActorID: coach, GroupID: group.ID, ExpectedUpdatedAt: same.Group.UpdatedAt, Name: "Renamed group"})
	if err != nil || renamed.ID != group.ID || renamed.Name != "Renamed group" {
		t.Fatalf("rename %#v %v", renamed, err)
	}
	if _, err := store.RenameGroup(ctx, TrainingGroupRenameInput{ActorID: coach, GroupID: group.ID, ExpectedUpdatedAt: same.Group.UpdatedAt, Name: "Stale rename"}); !errors.Is(err, ErrTrainingGroupConflict) {
		t.Fatalf("stale rename %v", err)
	}
	competitor, err := store.CreateGroup(ctx, TrainingGroupCreateInput{ActorID: coach, Name: "Other group", ProgrammeID: &programme})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddAthletes(ctx, TrainingGroupRosterInput{ActorID: coach, GroupID: competitor.ID, ExpectedUpdatedAt: competitor.UpdatedAt, MembershipIDs: []uuid.UUID{first}}); !errors.Is(err, ErrTrainingGroupEligibility) {
		t.Fatalf("overlap allowed %v", err)
	}
	found, err = store.SearchAthletes(ctx, coach, competitor.ID, prefix+"Literal", 0)
	if err != nil || len(found) != 0 {
		t.Fatalf("unavailable placement offered %#v %v", found, err)
	}
	// Each protected read independently rechecks dependent authority after
	// the group-detail read, rather than trusting earlier eligibility.
	guardian := user("Guardian")
	policy := enableGuardianAuthorityTestPolicy(t, ctx, pool, admin)
	verifyGuardianAuthorityTestRelationship(t, ctx, pool, guardian, minor, admin, policy)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `UPDATE guardian_application_intake_release SET enabled=false WHERE singleton; UPDATE guardian_authority_policies SET enabled=false,enabled_at=NULL,enabled_by=NULL WHERE enabled`)
	})
	eligibleMinor, e := store.SearchAthletes(ctx, coach, group.ID, prefix+"Private unverified", 0)
	if e != nil || len(eligibleMinor) != 1 {
		t.Fatalf("verified dependent fixture %#v %v", eligibleMinor, e)
	}
	tr := &classificationRevocationTracer{match: "-- name: TrainingGroupAthleteChoices", revoke: func() {
		exec(`UPDATE guardian_authority_relationships SET state='SUSPENDED',version=version+1,updated_at=clock_timestamp() WHERE subject_user_id=$1`, minor)
	}}
	config, e := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if e != nil {
		t.Fatal(e)
	}
	config.ConnConfig.Tracer = tr
	traced, e := pgxpool.NewWithConfig(ctx, config)
	if e != nil {
		t.Fatal(e)
	}
	defer traced.Close()
	hiddenAfter, e := (PostgresTrainingGroupStore{Pool: traced}).SearchAthletes(ctx, coach, group.ID, prefix+"Private unverified", 0)
	if e != nil || len(hiddenAfter) != 0 || !tr.fired {
		t.Fatalf("revoked dependent leaked %#v %v fired=%v", hiddenAfter, e, tr.fired)
	}
	for _, literal := range []string{"_", "\\"} {
		m := membership("Literal "+literal+" name", programme)
		choices, e := store.SearchAthletes(ctx, coach, competitor.ID, prefix+"Literal "+literal, 0)
		if e != nil || len(choices) != 1 || choices[0].MembershipID != m {
			t.Fatalf("literal %q %#v %v", literal, choices, e)
		}
	}
	// Two different groups concurrently adding the same person serialize on that
	// person; exactly one wins without ending another placement.
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, g := range []TrainingGroupRecord{renamed, competitor} {
		wg.Add(1)
		go func(g TrainingGroupRecord) {
			defer wg.Done()
			_, err := store.AddAthletes(ctx, TrainingGroupRosterInput{ActorID: coach, GroupID: g.ID, ExpectedUpdatedAt: g.UpdatedAt, MembershipIDs: []uuid.UUID{second}})
			results <- err
		}(g)
	}
	wg.Wait()
	close(results)
	wins, denied := 0, 0
	for e := range results {
		if e == nil {
			wins++
		} else if errors.Is(e, ErrTrainingGroupEligibility) {
			denied++
		} else {
			t.Fatal(e)
		}
	}
	if wins != 1 || denied != 1 {
		t.Fatalf("concurrent placement wins=%d denied=%d", wins, denied)
	}
	// Stale middleware identity cannot keep a revoked coach authorized.
	exec(`UPDATE staff_grants SET revoked_at=now(),revoked_by_id=$2,revoke_reason='Synthetic revocation' WHERE user_id=$1 AND revoked_at IS NULL`, coach, admin)
	if _, err := store.GetGroup(ctx, coach, group.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("revoked read %v", err)
	}
	if _, err := store.RenameGroup(ctx, TrainingGroupRenameInput{ActorID: coach, GroupID: group.ID, ExpectedUpdatedAt: renamed.UpdatedAt, Name: "Revoked change"}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("revoked write %v", err)
	}
}

// The legacy combined form can submit its complete roster. The dedicated add
// task alone has a twenty-choice page bound.
func TestLegacyTrainingGroupCreatePreservesLargeInitialRoster(t *testing.T) {
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	actor, season := uuid.New(), uuid.New()
	exec(`INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Legacy roster coach',$2,'hash','1990-01-01')`, actor, actor.String()+"@example.test")
	exec(`INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, actor)
	var programme uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM programmes WHERE code='Leisure'`).Scan(&programme); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO seasons(id,code,name,starts_on,ends_on) VALUES($1,$2,'Legacy roster season',CURRENT_DATE-10,CURRENT_DATE+10)`, season, "L"+uuid.NewString()[:8])
	members := make([]uuid.UUID, 21)
	for i := range members {
		person := uuid.New()
		members[i] = uuid.New()
		exec(`INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Legacy athlete',$2,'hash','1990-01-01')`, person, person.String()+"@example.test")
		exec(`INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on) VALUES($1,$2,$3,$4,CURRENT_DATE-1)`, members[i], person, season, programme)
	}
	legacy := PostgresStructuredTrainingStore{Pool: pool}
	group, err := legacy.CreateGroup(ctx, StructuredTrainingGroupInput{Params: dbgen.CreateStructuredTrainingGroupParams{Name: "Legacy large roster", CreatedByID: actor, ProgrammeID: &programme}, MembershipIDs: members})
	if err != nil {
		t.Fatalf("legacy initial twenty-one athletes: %v", err)
	}
	store := PostgresTrainingGroupStore{Pool: pool}
	current, err := store.GetGroup(ctx, actor, group.ID)
	if err != nil || current.MemberCount != 21 {
		t.Fatalf("legacy initial roster count=%d error=%v", current.MemberCount, err)
	}
	first, err := store.ListRoster(ctx, actor, group.ID, 0)
	if err != nil || len(first) != 20 {
		t.Fatalf("first roster page count=%d error=%v", len(first), err)
	}
	second, err := store.ListRoster(ctx, actor, group.ID, 20)
	if err != nil || len(second) != 1 {
		t.Fatalf("second roster page count=%d error=%v", len(second), err)
	}
	if _, err := store.AddAthletes(ctx, TrainingGroupRosterInput{ActorID: actor, GroupID: group.ID, MembershipIDs: members, ExpectedUpdatedAt: current.UpdatedAt}); !errors.Is(err, ErrTrainingGroupEligibility) {
		t.Fatalf("dedicated add must retain twenty-choice bound: %v", err)
	}
	after, err := store.GetGroup(ctx, actor, group.ID)
	if err != nil || after.MemberCount != 21 || !after.UpdatedAt.Equal(current.UpdatedAt) {
		t.Fatalf("rejected dedicated batch changed roster: %#v %v", after, err)
	}
}
