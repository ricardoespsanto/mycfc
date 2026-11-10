//go:build integration

package handlers

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestTrainingSetupAdapterContextAuthorityAndReadonlyJourneys(t *testing.T) {
	ctx := t.Context()
	pool := seasonTestPool(t)
	q := dbgen.New(pool)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, e := pool.Exec(ctx, sql, args...); e != nil {
			t.Fatal(e)
		}
	}
	var original *uuid.UUID
	if e := pool.QueryRow(ctx, `SELECT id FROM seasons WHERE is_current`).Scan(&original); e != nil && !errors.Is(e, pgx.ErrNoRows) {
		t.Fatal(e)
	}
	exec(`UPDATE seasons SET is_current=false WHERE is_current`)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `UPDATE seasons SET is_current=false WHERE is_current`)
		if original != nil {
			_, _ = pool.Exec(context.Background(), `UPDATE seasons SET is_current=true WHERE id=$1`, *original)
		}
	})
	prefix := "Setup " + uuid.NewString()[:8] + " "
	person := func(name string) uuid.UUID {
		id := uuid.New()
		exec(`INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,$2,$3,'hash','1990-01-01')`, id, prefix+name, id.String()+"@example.test")
		return id
	}
	admin, coach, teamCoach, subject := person("Administrator"), person("Programme coach"), person("Team coach"), person("Available subject")
	exec(`INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, admin)
	var programme uuid.UUID
	if e := pool.QueryRow(ctx, `SELECT id FROM programmes WHERE code='Competition'`).Scan(&programme); e != nil {
		t.Fatal(e)
	}
	today := time.Now().In(lisbonLocation())
	monday := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	monday = monday.AddDate(0, 0, -(int(monday.Weekday())+6)%7)
	season, team := uuid.New(), uuid.New()
	exec(`INSERT INTO seasons(id,code,name,starts_on,ends_on) VALUES($1,$2,'Explicit setup season',$3,$4)`, season, "S"+uuid.NewString()[:8], monday.AddDate(0, -1, 0), monday.AddDate(1, 0, 0))
	exec(`INSERT INTO teams(id,season_id,programme_id,code,name) VALUES($1,$2,$3,$4,'Existing authorized team')`, team, season, programme, "T"+uuid.NewString()[:8])
	exec(`INSERT INTO staff_grants(user_id,capability,programme_id,granted_by_id) VALUES($1,'COACH',$2,$3)`, coach, programme, admin)
	exec(`INSERT INTO staff_grants(user_id,capability,team_id,granted_by_id) VALUES($1,'COACH',$2,$3)`, teamCoach, team, admin)
	groups := PostgresTrainingGroupStore{Pool: pool}
	programmeGroup, e := groups.CreateGroup(ctx, TrainingGroupCreateInput{ActorID: admin, Name: prefix + "Programme group", ProgrammeID: &programme})
	if e != nil {
		t.Fatal(e)
	}
	teamGroup, e := groups.CreateGroup(ctx, TrainingGroupCreateInput{ActorID: admin, Name: prefix + "Team group", TeamID: &team})
	if e != nil {
		t.Fatal(e)
	}
	setup := PostgresTrainingSetupStore{Pool: pool}
	status, e := setup.Capabilities(ctx, coach, programmeGroup.ID)
	if e != nil || !status.MissingSeason || !status.ProgrammeAuthority || status.Administrator {
		t.Fatalf("missing-season status %#v %v", status, e)
	}
	page := pages.TrainingGroupsPage{Mode: "add"}
	r := setupRequest("GET", "/group", nil, coach, uuid.Nil)
	if e := (TrainingGroups{Setup: setup}).applySetup(ctx, r, CurrentUser{ID: coach, IsAdmin: true}, programmeGroup, &page); e != nil {
		t.Fatal(e)
	}
	if page.Setup.ClassificationURL != "" || page.Setup.DefinitionsURL != "" || page.Setup.SeasonURL != "" {
		t.Fatal("missing-season coach offered unusable/admin task")
	}
	page = pages.TrainingGroupsPage{Mode: "add"}
	if e := (TrainingGroups{Setup: setup}).applySetup(ctx, r, CurrentUser{ID: admin}, programmeGroup, &page); e != nil || page.Setup.SeasonURL == "" {
		t.Fatalf("administrator missing-season action %#v %v", page.Setup, e)
	}
	exec(`UPDATE seasons SET is_current=true WHERE id=$1`, season)
	week, e := q.CreateStructuredTrainingWeek(ctx, dbgen.CreateStructuredTrainingWeekParams{GroupID: programmeGroup.ID, Title: "Setup origin week", WeekStart: pgtype.Date{Time: monday, Valid: true}, CreatedByID: admin})
	if e != nil {
		t.Fatal(e)
	}
	day := monday.AddDate(0, 0, 5).Format("2006-01-02")
	origin := TrainingSetupContext{GroupID: programmeGroup.ID.String(), WeekID: week.ID.String(), Day: day}
	resolved, dest, e := setup.ResolveContext(ctx, coach, origin)
	want := structuredPlannerURL(origin.GroupID, origin.WeekID, "", origin.Day)
	if e != nil || resolved != origin || dest != want {
		t.Fatalf("exact origin %#v %q %v", resolved, dest, e)
	}
	for _, bad := range []TrainingSetupContext{{GroupID: origin.GroupID, WeekID: uuid.NewString(), Day: day}, {GroupID: origin.GroupID, WeekID: origin.WeekID, Day: monday.AddDate(0, 0, 7).Format("2006-01-02")}} {
		c, d, e := setup.ResolveContext(ctx, coach, bad)
		if e != nil || c.Day != "" || strings.Contains(d, "day=") {
			t.Fatalf("unsafe week/day retained %#v %q %v", c, d, e)
		}
	}
	if _, _, e = setup.ResolveContext(ctx, teamCoach, origin); !errors.Is(e, pgx.ErrNoRows) {
		t.Fatalf("cross-scope origin exposed %v", e)
	}
	// A group grant revoked between the first group lookup and scoped week lookup
	// must not be treated as merely an invalid week with retained group context.
	raceCoach := person("Racing programme coach")
	exec(`INSERT INTO staff_grants(user_id,capability,programme_id,granted_by_id) VALUES($1,'COACH',$2,$3)`, raceCoach, programme, admin)
	tracer := &classificationRevocationTracer{match: "-- name: TrainingGroupSetupWeek", revoke: func() {
		exec(`UPDATE staff_grants SET revoked_at=now(),revoked_by_id=$2,revoke_reason='Context revocation race' WHERE user_id=$1 AND revoked_at IS NULL`, raceCoach, admin)
	}}
	cfg, e := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.Tracer = tracer
	traced, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	_, _, e = (PostgresTrainingSetupStore{Pool: traced}).ResolveContext(ctx, raceCoach, origin)
	traced.Close()
	if !errors.Is(e, pgx.ErrNoRows) || !tracer.fired {
		t.Fatalf("revocation race retained origin: error=%v fired=%v", e, tracer.fired)
	}

	teamStatus, e := setup.Capabilities(ctx, teamCoach, teamGroup.ID)
	if e != nil || teamStatus.ProgrammeAuthority || teamStatus.Administrator {
		t.Fatalf("team grant broadened %#v %v", teamStatus, e)
	}
	// No category creation is invented by lookup; configured status changes only
	// after an explicit existing category insert in this fixture.
	status, e = setup.Capabilities(ctx, coach, programmeGroup.ID)
	if e != nil || status.CategoryConfigured {
		t.Fatalf("missing category state %#v %v", status, e)
	}
	exec(`INSERT INTO competition_categories(season_id,programme_id,code,name_pt,approved_by_user_id,approved_at) VALUES($1,$2,$3,'Configured category',$4,now())`, season, programme, "C"+uuid.NewString()[:8], admin)
	status, e = setup.Capabilities(ctx, coach, programmeGroup.ID)
	if e != nil || !status.CategoryConfigured {
		t.Fatalf("configured category state %#v %v", status, e)
	}
	minor := uuid.New()
	exec(`INSERT INTO users(id,name,is_dependent,date_of_birth) VALUES($1,$2,true,'2015-01-01')`, minor, prefix+"Private unverified child")
	classification := PostgresClassificationStore{Pool: pool}
	hidden, _, e := classification.Search(ctx, admin, prefix+"Private unverified", 1)
	if e != nil || len(hidden) != 0 {
		t.Fatalf("setup inferred hidden subject %#v %v", hidden, e)
	}
	snapshot := func() string {
		t.Helper()
		var out string
		if e := pool.QueryRow(ctx, `SELECT jsonb_build_object('categories',(SELECT jsonb_agg(to_jsonb(c) ORDER BY id) FROM competition_categories c),'memberships',(SELECT jsonb_agg(to_jsonb(m) ORDER BY id) FROM user_memberships m),'plans',(SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM training_plans p),'publications',(SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM training_plan_publications p),'prescriptions',(SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM training_prescriptions p),'outcomes',(SELECT jsonb_agg(to_jsonb(o) ORDER BY session_id,user_id) FROM training_session_outcomes o))::text`).Scan(&out); e != nil {
			t.Fatal(e)
		}
		return out
	}
	before := snapshot()
	ch := Classification{Store: classification, Definitions: PostgresDefinitionStore{Pool: pool}, Setup: setup}
	request := setupRequest("GET", classificationPersonSetupURL(subject, "Available", origin), nil, admin, subject)
	response := httptest.NewRecorder()
	ch.Get(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "origin_day="+day) {
		t.Fatalf("classification origin status=%d", response.Code)
	}
	defs := Definitions{Store: PostgresDefinitionStore{Pool: pool}, Setup: setup}
	response = httptest.NewRecorder()
	defs.Get(response, setupRequest("GET", setupContextURL("/equipa/escaloes", origin), nil, admin, uuid.Nil))
	if response.Code != 200 || !strings.Contains(response.Body.String(), "origin_day") {
		t.Fatalf("definitions origin status=%d", response.Code)
	}
	seasonStore := PostgresSeasonStore{Pool: pool}
	returned, d, e := seasonStore.ResolveReturn(ctx, admin, setupSeasonContext("classification", subject.String(), origin))
	if e != nil || returned.OriginDay != day || !strings.Contains(d, "origin_day="+day) {
		t.Fatalf("season task roundtrip %#v %s %v", returned, d, e)
	}
	if snapshot() != before {
		t.Fatal("setup navigation changed classification, plans, publications or outcomes")
	}
	exec(`UPDATE staff_grants SET revoked_at=now(),revoked_by_id=$2,revoke_reason='Setup revocation test' WHERE user_id=$1 AND revoked_at IS NULL`, coach, admin)
	response = httptest.NewRecorder()
	ch.Get(response, setupRequest("GET", classificationPersonSetupURL(subject, "Available", origin), nil, coach, subject))
	if response.Code != 404 || strings.Contains(response.Body.String(), prefix+"Available subject") {
		t.Fatalf("revoked origin retained person status=%d", response.Code)
	}
}

// Cancel only the group query after the real ADMIN check has succeeded. This
// models an infrastructure/read failure without altering stored application data.
type setupGroupReadFailureTracer struct{}

func (setupGroupReadFailureTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(data.SQL, "-- name: TrainingGroupRecords") {
		failed, cancel := context.WithCancel(ctx)
		cancel()
		return failed
	}
	return ctx
}
func (setupGroupReadFailureTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
func TestSeasonGroupReturnPropagatesUnexpectedReadFailure(t *testing.T) {
	pool := seasonTestPool(t)
	ctx := t.Context()
	admin, programme, group := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Return read failure admin',$2,'hash','1990-01-01')`, admin, admin.String()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, admin); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM programmes WHERE code='Leisure'`).Scan(&programme); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO training_groups(id,name,programme_id,created_by_id) VALUES($1,'Return read failure group',$2,$3)`, group, programme, admin); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Tracer = setupGroupReadFailureTracer{}
	traced, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer traced.Close()
	out, dest, err := (PostgresSeasonStore{Pool: traced}).ResolveReturn(ctx, admin, SeasonReturnContext{Kind: "group", ID: group.String()})
	if !errors.Is(err, context.Canceled) || dest != "" || out.ID != "" {
		t.Fatalf("read failure silently changed destination: out=%#v dest=%q err=%v", out, dest, err)
	}
	// An actual absent group remains the deliberate canonical list fallback.
	_, dest, err = (PostgresSeasonStore{Pool: pool}).ResolveReturn(ctx, admin, SeasonReturnContext{Kind: "group", ID: uuid.NewString()})
	if err != nil || dest != seasonsPath {
		t.Fatalf("missing group fallback %q %v", dest, err)
	}
}
