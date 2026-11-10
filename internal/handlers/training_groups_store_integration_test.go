//go:build integration

package handlers

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestTrainingGroupStoreRejectsUnverifiedReturnsAndInvalidWrites(t *testing.T) {
	f := newPublicationDomainFixture(t)
	ctx := t.Context()
	store := PostgresTrainingGroupStore{Pool: f.web}
	for _, tc := range []struct {
		actor, week uuid.UUID
		allowed     bool
		err         error
	}{
		{actor: f.actor, week: f.plan, allowed: true},
		{actor: f.actor, week: uuid.New()},
		{actor: f.subject, week: f.plan, err: pgx.ErrNoRows},
	} {
		allowed, err := store.AllowsWeek(ctx, tc.actor, f.group, tc.week)
		if allowed != tc.allowed || !errors.Is(err, tc.err) {
			t.Fatalf("week authorization: %t, %v", allowed, err)
		}
	}
	seasons, err := store.PlacementSeasons(ctx, f.actor, f.group)
	if err != nil || len(seasons) != 1 || seasons[0].ID != f.season {
		t.Fatalf("scoped seasons: %+v, %v", seasons, err)
	}
	f.exec(t, `INSERT INTO seasons(code,name,starts_on,ends_on)
		SELECT 'Synthetic' || n,'Disposable season ' || n,CURRENT_DATE+n,CURRENT_DATE+n+1 FROM generate_series(1,100) n`)
	if seasons, err := store.PlacementSeasons(ctx, f.actor, f.group); !errors.Is(err, ErrTrainingGroupEligibility) || len(seasons) != 0 {
		t.Fatalf("overfull season list was silently truncated: %d, %v", len(seasons), err)
	}
	if rows, err := store.SearchAthletes(ctx, f.actor, f.group, "Synthetic", -1); !errors.Is(err, ErrTrainingGroupEligibility) || len(rows) != 0 {
		t.Fatalf("out-of-range search disclosed athletes: %d, %v", len(rows), err)
	}
	original, err := store.GetGroup(ctx, f.actor, f.group)
	if err != nil {
		t.Fatal(err)
	}
	rename := TrainingGroupRenameInput{ActorID: f.actor, GroupID: f.group, ExpectedUpdatedAt: original.UpdatedAt, Name: " untrimmed "}
	if _, err := store.RenameGroup(ctx, rename); !errors.Is(err, ErrTrainingGroupEligibility) {
		t.Fatalf("invalid rename accepted: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	for _, operation := range []struct {
		name string
		call func() error
	}{
		{"list", func() error { _, err := store.ListGroups(canceled, f.actor, 0); return err }},
		{"scopes", func() error { _, err := store.ListScopes(canceled, f.actor); return err }},
		{"rename", func() error {
			rename.Name = "Valid replacement"
			_, err := store.RenameGroup(canceled, rename)
			return err
		}},
	} {
		if err := operation.call(); !errors.Is(err, context.Canceled) {
			t.Fatalf("%s ignored cancellation: %v", operation.name, err)
		}
	}
	var day time.Time
	if err := f.web.QueryRow(ctx, `SELECT week_start FROM training_plans WHERE id=$1`, f.plan).Scan(&day); err != nil {
		t.Fatal(err)
	}
	candidate, membership := uuid.New(), uuid.New()
	f.exec(t, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Synthetic unplaced athlete',$2,'hash','1990-01-01')`, candidate, candidate.String()+"@example.test")
	f.exec(t, `INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on) VALUES($1,$2,$3,$4,CURRENT_DATE)`, membership, candidate, f.season, f.programme)
	in := trainingGroupFixtureReview(t, store, TrainingGroupRosterInput{ActorID: f.actor, GroupID: f.group, SeasonID: f.season, EffectiveOn: day, ExpectedUpdatedAt: original.UpdatedAt, MembershipIDs: []uuid.UUID{membership}})
	tampered := in
	tampered.GroupID = uuid.New()
	if _, err := store.AddAthletes(ctx, tampered); !errors.Is(err, ErrTrainingGroupEligibility) {
		t.Fatalf("changed placement target accepted the original proof: %v", err)
	}
	if _, err := (PostgresTrainingGroupStore{Pool: f.root}).AddAthletes(ctx, in); !errors.Is(err, errTrainingPlacementUnavailable) {
		t.Fatalf("placement used a connection outside the admitted pool: %v", err)
	}
	after, err := store.GetGroup(ctx, f.actor, f.group)
	if err != nil || after.Name != original.Name || !after.UpdatedAt.Equal(original.UpdatedAt) || after.MemberCount != original.MemberCount {
		t.Fatalf("failed requests changed group state: before=%+v, after=%+v, %v", original, after, err)
	}
}

func TestTrainingGroupTasksScopeRosterAndConcurrency(t *testing.T) {
	f := newPublicationDomainFixture(t)
	ctx := t.Context()
	pool := f.web
	privileged, err := pgxpool.New(ctx, f.raw)
	if err != nil {
		t.Fatal(err)
	}
	defer privileged.Close()
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
	var today time.Time
	if err := pool.QueryRow(ctx, `SELECT (clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date`).Scan(&today); err != nil {
		t.Fatal(err)
	}
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
	foundHidden, e := store.SearchAthletesForDate(ctx, coach, group.ID, season, today, prefix+"Private", 0)
	if e != nil || len(foundHidden) != 0 {
		t.Fatalf("hidden subject search %#v %v", foundHidden, e)
	}
	for _, id := range hidden {
		if _, e := store.AddAthletes(ctx, TrainingGroupRosterInput{ActorID: coach, GroupID: group.ID, ExpectedUpdatedAt: group.UpdatedAt, MembershipIDs: []uuid.UUID{id}}); !errors.Is(e, ErrTrainingGroupEligibility) {
			t.Fatalf("hidden subject add %v", e)
		}
	}
	first, second := membership("Literal % athlete", programme), membership("Second athlete", programme)
	blank, err := store.SearchAthletesForDate(ctx, coach, group.ID, season, today, "", 0)
	if !errors.Is(err, ErrTrainingGroupEligibility) || len(blank) != 0 {
		t.Fatalf("blank roster disclosure %#v %v", blank, err)
	}
	found, err := store.SearchAthletesForDate(ctx, coach, group.ID, season, today, prefix+"Literal %", 0)
	if err != nil || len(found) != 1 || found[0].MembershipID != first {
		t.Fatalf("wildcard must be literal %#v %v", found, err)
	}
	firstIn := trainingGroupFixtureReview(t, store, TrainingGroupRosterInput{ActorID: coach, GroupID: group.ID, SeasonID: season, EffectiveOn: today, ExpectedUpdatedAt: group.UpdatedAt, MembershipIDs: []uuid.UUID{first}})
	staleSecond := trainingGroupFixtureReview(t, store, TrainingGroupRosterInput{ActorID: coach, GroupID: group.ID, SeasonID: season, EffectiveOn: today, ExpectedUpdatedAt: group.UpdatedAt, MembershipIDs: []uuid.UUID{second}})
	added, err := store.AddAthletes(ctx, firstIn)
	if err != nil || added.Added != 1 || added.Group.MemberCount != 1 {
		t.Fatalf("add %#v %v", added, err)
	}
	if _, err := store.AddAthletes(ctx, staleSecond); !errors.Is(err, ErrTrainingGroupConflict) {
		t.Fatalf("stale roster %v", err)
	}
	same, err := store.AddAthletes(ctx, firstIn)
	if err != nil || same.Added != 1 || !same.Group.UpdatedAt.Equal(added.Group.UpdatedAt) {
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
	found, err = store.SearchAthletesForDate(ctx, coach, competitor.ID, season, today, prefix+"Literal", 0)
	if err != nil || len(found) != 1 || !found[0].Unavailable || found[0].AlreadyMember || found[0].GroupID != competitor.ID || found[0].PlacementID != uuid.Nil {
		t.Fatalf("unavailable placement exposed an enabled JOIN or foreign source %#v %v", found, err)
	}
	// Each protected read independently rechecks dependent authority after
	// the group-detail read, rather than trusting earlier eligibility.
	guardian := user("Guardian")
	policy := enableGuardianAuthorityTestPolicy(t, ctx, privileged, admin)
	verifyGuardianAuthorityTestRelationship(t, ctx, privileged, guardian, minor, admin, policy)
	eligibleMinor, e := store.SearchAthletesForDate(ctx, coach, group.ID, season, today, prefix+"Private unverified", 0)
	if e != nil || len(eligibleMinor) != 1 {
		t.Fatalf("verified dependent fixture %#v %v", eligibleMinor, e)
	}
	tr := &classificationRevocationTracer{match: "-- name: TrainingGroupAthleteChoices", revoke: func() {
		if _, e := privileged.Exec(ctx, `UPDATE guardian_authority_relationships SET state='SUSPENDED',version=version+1,updated_at=clock_timestamp() WHERE subject_user_id=$1`, minor); e != nil {
			t.Fatal(e)
		}
	}}
	config, e := pgxpool.ParseConfig(pool.Config().ConnString())
	if e != nil {
		t.Fatal(e)
	}
	config.ConnConfig.Tracer = tr
	traced, e := pgxpool.NewWithConfig(ctx, config)
	if e != nil {
		t.Fatal(e)
	}
	defer traced.Close()
	hiddenAfter, e := (PostgresTrainingGroupStore{Pool: traced}).SearchAthletesForDate(ctx, coach, group.ID, season, today, prefix+"Private unverified", 0)
	if e != nil || len(hiddenAfter) != 0 || !tr.fired {
		t.Fatalf("revoked dependent leaked %#v %v fired=%v", hiddenAfter, e, tr.fired)
	}
	for _, literal := range []string{"_", "\\"} {
		m := membership("Literal "+literal+" name", programme)
		choices, e := store.SearchAthletesForDate(ctx, coach, competitor.ID, season, today, prefix+"Literal "+literal, 0)
		if e != nil || len(choices) != 1 || choices[0].MembershipID != m {
			t.Fatalf("literal %q %#v %v", literal, choices, e)
		}
	}
	// Two different groups concurrently adding the same person serialize on that
	// person; exactly one wins without ending another placement.
	var wg sync.WaitGroup
	results := make(chan error, 2)
	var concurrent []TrainingGroupRosterInput
	for _, g := range []TrainingGroupRecord{renamed, competitor} {
		concurrent = append(concurrent, trainingGroupFixtureReview(t, store, TrainingGroupRosterInput{ActorID: coach, GroupID: g.ID, SeasonID: season, EffectiveOn: today, ExpectedUpdatedAt: g.UpdatedAt, MembershipIDs: []uuid.UUID{second}}))
	}
	for _, in := range concurrent {
		wg.Add(1)
		go func(in TrainingGroupRosterInput) {
			defer wg.Done()
			_, err := store.AddAthletes(ctx, in)
			results <- err
		}(in)
	}
	wg.Wait()
	close(results)
	wins, denied := 0, 0
	for e := range results {
		if e == nil {
			wins++
		} else if errors.Is(e, ErrTrainingGroupEligibility) || errors.Is(e, ErrTrainingGroupConflict) {
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

// Actual dated additions preserve the twenty-choice request bound and complete
// roster pagination across separate signed batches.
func TestDatedTrainingGroupRosterPreservesPaginationAndBatchBound(t *testing.T) {
	f := newPublicationDomainFixture(t)
	ctx := t.Context()
	pool := f.web
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	var err error
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
	store := PostgresTrainingGroupStore{Pool: pool}
	group, err := store.CreateGroup(ctx, TrainingGroupCreateInput{ActorID: actor, Name: "Dated large roster", ProgrammeID: &programme})
	if err != nil {
		t.Fatal(err)
	}
	var today time.Time
	if err = pool.QueryRow(ctx, `SELECT (clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date`).Scan(&today); err != nil {
		t.Fatal(err)
	}
	for _, ids := range [][]uuid.UUID{members[:20], members[20:]} {
		in := trainingGroupFixtureReview(t, store, TrainingGroupRosterInput{ActorID: actor, GroupID: group.ID, SeasonID: season, EffectiveOn: today, ExpectedUpdatedAt: group.UpdatedAt, MembershipIDs: ids})
		result, e := store.AddAthletes(ctx, in)
		if e != nil {
			t.Fatal("actual dated batch", e)
		}
		group = result.Group
	}
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

// Builds only disposable fixture reviews through the same current scoped read
// and signer as native search. No raw placement or undated roster INSERT.
func trainingGroupFixtureReview(t *testing.T, store PostgresTrainingGroupStore, in TrainingGroupRosterInput) TrainingGroupRosterInput {
	t.Helper()
	in.CommandID = uuid.New()
	rows := []TrainingGroupAthlete{}
	for _, member := range in.MembershipIDs {
		row := TrainingGroupAthlete{MembershipID: member, GroupID: in.GroupID, SeasonID: in.SeasonID, DisplayOn: in.EffectiveOn.Format("2006-01-02")}
		if e := store.Pool.QueryRow(t.Context(), `SELECT u.id,u.name FROM user_memberships m JOIN users u ON u.id=m.user_id WHERE m.id=$1`, member).Scan(&row.UserID, &row.Name); e != nil {
			t.Fatal(e)
		}
		rows = append(rows, row)
	}
	proof, e := store.ReviewAthleteSelection(t.Context(), in, rows)
	if e != nil {
		t.Fatal("fixture current review", e)
	}
	in.SelectionProof = proof
	return in
}
