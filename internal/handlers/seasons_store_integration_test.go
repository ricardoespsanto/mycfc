//go:build integration

package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"
)

func seasonTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	pool, e := pgxpool.New(t.Context(), dsn)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(pool.Close)
	return pool
}
func TestSeasonManagementExplicitSelectionConcurrencyAndHistory(t *testing.T) {
	ctx, pool, fixture := currentTrainingFixture(t)
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
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `UPDATE seasons SET is_current=false WHERE is_current`)
		if original != nil {
			_, _ = pool.Exec(context.Background(), `UPDATE seasons SET is_current=true WHERE id=$1`, *original)
		}
	})
	exec(`UPDATE seasons SET is_current=false WHERE is_current`)
	person := func(name string) uuid.UUID {
		id := uuid.New()
		exec(`INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,$2,$3,'hash','1990-01-01')`, id, name, id.String()+"@example.test")
		return id
	}
	admin, coach, athlete := person("Season administrator"), person("Season coach"), person("Historical athlete")
	exec(`INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, admin)
	store := PostgresSeasonStore{Pool: pool}
	q := dbgen.New(pool)
	// A legacy read and missing-setup POST must leave an empty current selection
	// unchanged, even with the actual generated database adapter.
	var seasonCountBefore, seasonCountAfter int
	if e := pool.QueryRow(ctx, `SELECT count(*) FROM seasons`).Scan(&seasonCountBefore); e != nil {
		t.Fatal(e)
	}
	memberHandler := Members{Store: q}
	for _, method := range []string{"GET", "POST"} {
		r := seasonTestRequest(method, "/admin/membros/"+athlete.String(), url.Values{"programme_id": {uuid.NewString()}}, admin, athlete)
		w := httptest.NewRecorder()
		if method == "GET" {
			memberHandler.Detail(w, r)
		} else {
			memberHandler.Membership(w, r)
		}
		want := 200
		if method == "POST" {
			want = 422
		}
		if w.Code != want {
			t.Fatalf("missing-season member %s status=%d body=%s", method, w.Code, w.Body.String())
		}
	}
	if e := pool.QueryRow(ctx, `SELECT count(*) FROM seasons`).Scan(&seasonCountAfter); e != nil || seasonCountAfter != seasonCountBefore {
		t.Fatalf("legacy member paths created season %d->%d %v", seasonCountBefore, seasonCountAfter, e)
	}

	today := time.Now().UTC()
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	create := func(name string, start, end time.Time) dbgen.Season {
		t.Helper()
		row, e := store.CreateSeason(ctx, admin, SeasonInput{Code: uuid.NewString()[:20], Name: name, StartsOn: start, EndsOn: end})
		if e != nil {
			t.Fatal(e)
		}
		return row
	}
	a := create("Season A", today.AddDate(-1, 0, 0), today.AddDate(1, 0, 0))
	var count int
	if e := pool.QueryRow(ctx, `SELECT count(*) FROM seasons WHERE is_current`).Scan(&count); e != nil || count != 0 {
		t.Fatalf("creation selected season count=%d %v", count, e)
	}
	leap, _ := time.Parse("2006-01-02", "2024-02-29")
	day := create("Equal endpoint leap day", leap, leap)
	if !day.StartsOn.Time.Equal(leap) || !day.EndsOn.Time.Equal(leap) {
		t.Fatal("explicit leap day changed")
	}
	if _, e := store.CreateSeason(ctx, admin, SeasonInput{Code: a.Code, Name: "Duplicate code", StartsOn: leap, EndsOn: leap}); !errors.Is(e, ErrSeasonDuplicate) {
		t.Fatalf("duplicate code %v", e)
	}
	if _, e := store.CreateSeason(ctx, admin, SeasonInput{Code: "reversed", Name: "Reverse dates", StartsOn: leap.AddDate(0, 0, 1), EndsOn: leap}); !errors.Is(e, ErrSeasonInvalid) {
		t.Fatalf("reverse dates %v", e)
	}
	b := create("Overlapping Season B", a.StartsOn.Time, a.EndsOn.Time)
	c := create("Overlapping Season C", a.StartsOn.Time, a.EndsOn.Time)
	selectSeason := func(id uuid.UUID) {
		t.Helper()
		state, e := store.GetSelectionState(ctx, admin, id)
		if e != nil {
			t.Fatal(e)
		}
		if e = store.SelectCurrentSeason(ctx, admin, id, state.Version); e != nil {
			t.Fatal(e)
		}
	}
	selectSeason(a.ID)
	var programme uuid.UUID
	if e := pool.QueryRow(ctx, `SELECT id FROM programmes WHERE code='Competition'`).Scan(&programme); e != nil {
		t.Fatal(e)
	}
	category, membership := uuid.New(), uuid.New()
	exec(`INSERT INTO competition_categories(id,season_id,programme_id,code,name_pt,approved_by_user_id,approved_at) VALUES($1,$2,$3,'History','Historical category',$4,now())`, category, a.ID, programme, admin)
	exec(`INSERT INTO user_memberships(id,user_id,season_id,programme_id,competition_category_id,starts_on) VALUES($1,$2,$3,$4,$5,$6)`, membership, athlete, a.ID, programme, category, a.StartsOn)
	groups := PostgresStructuredTrainingStore{Pool: fixture.web}
	group, e := groups.CreateGroup(ctx, StructuredTrainingGroupInput{Params: dbgen.CreateStructuredTrainingGroupParams{Name: "Season history group", ProgrammeID: &programme, CreatedByID: admin}})
	if e != nil {
		t.Fatal(e)
	}
	currentTrainingPlacement(t, fixture, admin, athlete, programme, a.ID, group.ID, today)
	// The legacy week-inference query still reads its compatibility roster.
	// This row supplies season preference only; signed placement grants delivery.
	exec(`INSERT INTO training_group_members(group_id,membership_id,added_by_id) VALUES($1,$2,$3)`, group.ID, membership, admin)
	monday := today.AddDate(0, 0, -(int(today.Weekday())+6)%7+7)
	week, e := q.CreateStructuredTrainingWeek(ctx, dbgen.CreateStructuredTrainingWeekParams{GroupID: group.ID, Title: "Historical draft", WeekStart: pgtype.Date{Time: monday, Valid: true}, CreatedByID: admin})
	if e != nil {
		t.Fatal(e)
	}
	start := monday.Add(10 * time.Hour)
	session, e := q.CreateStructuredTrainingSession(ctx, dbgen.CreateStructuredTrainingSessionParams{PlanID: week.ID, Title: "Historical session", EntryKind: dbgen.TrainingEntryKindTRAINING, StartsAt: pgtype.Timestamptz{Time: start, Valid: true}, EndsAt: pgtype.Timestamptz{Time: start.Add(time.Hour), Valid: true}, CreatedByID: admin})
	if e != nil {
		t.Fatal(e)
	}
	var source time.Time
	if e = pool.QueryRow(ctx, `SELECT updated_at FROM training_plans WHERE id=$1`, week.ID).Scan(&source); e != nil {
		t.Fatal(e)
	}
	payload := []byte(`{"schema_version":1,"session":{"title":"Preserved instruction"}}`)
	sum := sha256.Sum256(payload)
	if _, e = confirmPublicationTestReview(ctx, groups, publicationTestReview{PlanID: week.ID, SourceUpdatedAt: pgtype.Timestamptz{Time: source, Valid: true}, ChangeSummary: "Historical publication", PublishedByID: admin, Prescriptions: []StructuredPrescriptionInput{{SessionID: session.ID, MembershipID: membership, AthleteUserID: athlete, Snapshot: payload, SnapshotSHA256: hex.EncodeToString(sum[:])}}}); e != nil {
		t.Fatal(e)
	}
	if n, e := q.SaveTrainingSessionOutcome(ctx, dbgen.SaveTrainingSessionOutcomeParams{SessionID: session.ID, UserID: athlete, Status: dbgen.TrainingOutcomeStatusCOMPLETED}); e != nil || n != 1 {
		t.Fatalf("historical outcome %d %v", n, e)
	}
	snapshot := func() map[string]string {
		t.Helper()
		out := map[string]string{}
		for _, table := range []string{"seasons", "competition_categories", "user_memberships", "training_plans", "training_sessions", "training_plan_publications", "training_prescriptions", "training_session_outcomes"} {
			row := "to_jsonb(x)"
			if table == "seasons" {
				row = "to_jsonb(x)-'is_current'"
			}
			var value string
			if e := pool.QueryRow(ctx, `SELECT coalesce(jsonb_agg(`+row+` ORDER BY (`+row+`)::text),'[]'::jsonb)::text FROM `+table+` x`).Scan(&value); e != nil {
				t.Fatal(e)
			}
			out[table] = value
		}
		return out
	}
	before := snapshot()
	// Both different targets were previewed against precisely the same current row.
	sb, e := store.GetSelectionState(ctx, admin, b.ID)
	if e != nil {
		t.Fatal(e)
	}
	sc, e := store.GetSelectionState(ctx, admin, c.ID)
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, v := range []SeasonSelection{sb, sc} {
		wg.Add(1)
		go func(v SeasonSelection) {
			defer wg.Done()
			results <- store.SelectCurrentSeason(ctx, admin, v.Target.ID, v.Version)
		}(v)
	}
	wg.Wait()
	close(results)
	wins, conflicts := 0, 0
	for e := range results {
		if e == nil {
			wins++
		} else if errors.Is(e, ErrSeasonConflict) {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("concurrent wins=%d conflicts=%d", wins, conflicts)
	}
	if e := pool.QueryRow(ctx, `SELECT count(*) FROM seasons WHERE is_current`).Scan(&count); e != nil || count != 1 {
		t.Fatalf("current cardinality=%d %v", count, e)
	}
	// A->B->A has the same identities but a new xmin; the older preview is stale.
	selectSeason(a.ID)
	stale, e := store.GetSelectionState(ctx, admin, b.ID)
	if e != nil {
		t.Fatal(e)
	}
	selectSeason(b.ID)
	selectSeason(a.ID)
	if e = store.SelectCurrentSeason(ctx, admin, b.ID, stale.Version); !errors.Is(e, ErrSeasonConflict) {
		t.Fatalf("ABA preview accepted %v", e)
	}
	if _, e = store.GetSelectionState(ctx, admin, uuid.New()); !errors.Is(e, pgx.ErrNoRows) {
		t.Fatalf("missing target %v", e)
	}
	// A failure after the old selection was cleared must restore that selection.
	exec(`CREATE FUNCTION season_test_reject_selection() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.name='Overlapping Season B' AND NEW.is_current THEN RAISE EXCEPTION 'synthetic selection failure'; END IF; RETURN NEW; END $$`)
	exec(`CREATE TRIGGER season_test_reject_selection BEFORE UPDATE ON seasons FOR EACH ROW EXECUTE FUNCTION season_test_reject_selection()`)
	failed, e := store.GetSelectionState(ctx, admin, b.ID)
	if e != nil {
		t.Fatal(e)
	}
	e = store.SelectCurrentSeason(ctx, admin, b.ID, failed.Version)
	exec(`DROP TRIGGER season_test_reject_selection ON seasons`)
	exec(`DROP FUNCTION season_test_reject_selection()`)
	if e == nil {
		t.Fatal("injected transaction failure accepted")
	}
	var current uuid.UUID
	if e = pool.QueryRow(ctx, `SELECT id FROM seasons WHERE is_current`).Scan(&current); e != nil || current != a.ID {
		t.Fatalf("failed switch lost old current %s %v", current, e)
	}
	if _, e = pool.Exec(ctx, `UPDATE seasons SET is_current=true WHERE id=$1`, b.ID); !isUniqueViolation(e) {
		t.Fatalf("unique current protection %v", e)
	}
	// Existing week matching uses roster season even when another overlapping
	// season is current. No resolution query was changed by season management.
	selectSeason(b.ID)
	next, e := q.CreateStructuredTrainingWeek(ctx, dbgen.CreateStructuredTrainingWeekParams{GroupID: group.ID, Title: "Same historical season", WeekStart: pgtype.Date{Time: monday.AddDate(0, 0, 7), Valid: true}, CreatedByID: admin})
	if e != nil || next.SeasonID == nil || *next.SeasonID != a.ID {
		t.Fatalf("existing season resolution %#v %v", next, e)
	}
	// Exclude the newly created draft from the preservation comparison.
	exec(`DELETE FROM training_plans WHERE id=$1`, next.ID)
	after := snapshot()
	for table, want := range before {
		if after[table] != want {
			t.Errorf("historical %s changed across selection", table)
		}
	}
	// Exact return context only: no arbitrary URL, cross-group week or stale member.
	good := SeasonReturnContext{Kind: "group", ID: group.ID.String(), WeekID: week.ID.String()}
	resolved, dest, e := store.ResolveReturn(ctx, admin, good)
	if e != nil || resolved.WeekID != week.ID.String() || dest != structuredPlannerURL(group.ID.String(), week.ID.String(), "") {
		t.Fatalf("valid group context %#v %q %v", resolved, dest, e)
	}
	forged := good
	forged.WeekID = uuid.NewString()
	resolved, dest, e = store.ResolveReturn(ctx, admin, forged)
	if e != nil || resolved.WeekID != "" || dest != trainingGroupsPath+"/"+group.ID.String() {
		t.Fatalf("forged group week %#v %q %v", resolved, dest, e)
	}
	for _, bad := range []SeasonReturnContext{{Kind: "https://outside.example"}, {Kind: "member", ID: athlete.String(), Query: string(make([]byte, 101))}, {Kind: "member", ID: uuid.NewString()}} {
		_, dest, e = store.ResolveReturn(ctx, admin, bad)
		if e != nil || dest != seasonsPath {
			t.Fatalf("unsafe context %q %v", dest, e)
		}
	}
	// Fresh role state controls reads and writes despite a previously loaded actor.
	for _, kind := range []string{"coach", "revoked", "inactive", "erased"} {
		t.Run(kind, func(t *testing.T) {
			who := coach
			if kind != "coach" {
				who = person("Season stale administrator")
				exec(`INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, who)
			}
			preview, e := store.GetSelectionState(ctx, admin, c.ID)
			if e != nil {
				t.Fatal(e)
			}
			switch kind {
			case "revoked":
				exec(`DELETE FROM user_platform_roles WHERE user_id=$1`, who)
			case "inactive":
				exec(`UPDATE users SET is_active=false WHERE id=$1`, who)
			case "erased":
				// Synthetic retired principal only: bypass FK provenance on one
				// isolated superuser connection, preserving the actual CHECK shape.
				conn, err := pool.Acquire(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = conn.Exec(ctx, `SET session_replication_role=replica`); err == nil {
					_, err = conn.Exec(ctx, `UPDATE users SET erased_at=now(),erasure_execution_id=$2,is_active=false,leaderboard_visible=false,email=NULL,email_verified_at=NULL,password_hash=NULL,name='Conta eliminada',date_of_birth='1900-01-01' WHERE id=$1`, who, uuid.New())
				}
				_, resetErr := conn.Exec(ctx, `SET session_replication_role=origin`)
				conn.Release()
				if err != nil || resetErr != nil {
					t.Fatalf("erased fixture: %v %v", err, resetErr)
				}
			}
			if _, e = store.ListSeasons(ctx, who, 0); !errors.Is(e, pgx.ErrNoRows) {
				t.Fatalf("read authority %v", e)
			}
			if e = store.SelectCurrentSeason(ctx, who, c.ID, preview.Version); !errors.Is(e, pgx.ErrNoRows) {
				t.Fatalf("write authority %v", e)
			}
		})
	}
}

func TestSeasonStoreScopedReturnAndCurrentReads(t *testing.T) {
	f := newPublicationDomainFixture(t)
	ctx := t.Context()
	store := PostgresSeasonStore{Pool: f.web}
	for _, offset := range []int32{-1, 1981} {
		if _, err := store.ListSeasons(ctx, f.actor, offset); !errors.Is(err, ErrSeasonInvalid) {
			t.Fatalf("offset %d: %v", offset, err)
		}
	}
	rows, err := store.ListSeasons(ctx, f.actor, 0)
	if err != nil || len(rows) != 1 || rows[0].ID != f.season || rows[0].IsCurrent {
		t.Fatalf("initial season collection: %+v, %v", rows, err)
	}
	if has, err := store.HasCurrentSeason(ctx, f.actor); err != nil || has {
		t.Fatalf("creating a season changed the current selection: %t, %v", has, err)
	}
	state, err := store.GetSelectionState(ctx, f.actor, f.season)
	if err != nil || state.Current != nil {
		t.Fatalf("initial selection state: %+v, %v", state, err)
	}
	if err := store.SelectCurrentSeason(ctx, f.actor, f.season, state.Version); err != nil {
		t.Fatal(err)
	}
	if has, err := store.HasCurrentSeason(ctx, f.actor); err != nil || !has {
		t.Fatalf("selection missing from current status: %t, %v", has, err)
	}
	selected, err := store.GetSelectionState(ctx, f.actor, f.season)
	if err != nil || selected.Current == nil || selected.Current.ID != f.season {
		t.Fatalf("selected season: %+v, %v", selected, err)
	}
	if err := store.SelectCurrentSeason(ctx, f.actor, f.season, selected.Version); err != nil {
		t.Fatalf("selecting the already-current season: %v", err)
	}
	if unchanged, err := store.GetSelectionState(ctx, f.actor, f.season); err != nil || unchanged.Version != selected.Version {
		t.Fatalf("idempotent selection changed the version: %+v, %v", unchanged, err)
	}
	if err := store.SelectCurrentSeason(ctx, f.actor, uuid.New(), selected.Version); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("missing selection target: %v", err)
	}

	var day string
	if err := f.web.QueryRow(ctx, `SELECT week_start::text FROM training_plans WHERE id=$1`, f.plan).Scan(&day); err != nil {
		t.Fatal(err)
	}
	origin := SeasonReturnContext{OriginGroup: f.group.String(), OriginWeek: f.plan.String(), OriginDay: day}
	definitions := origin
	definitions.Kind = "definitions"
	classification := origin
	classification.Kind, classification.ID = "classification", f.subject.String()
	member := SeasonReturnContext{Kind: "member", ID: f.subject.String(), Page: "02", Query: "Membro & família"}
	originQuery := url.Values{"origin_group": {origin.OriginGroup}, "origin_week": {origin.OriginWeek}, "origin_day": {day}}.Encode()
	collection := "/admin/membros?" + url.Values{"page": {"2"}, "q": {member.Query}}.Encode()
	memberURL := "/admin/membros/" + member.ID
	for _, tc := range []struct {
		name string
		in   SeasonReturnContext
		want SeasonReturnContext
		dest string
	}{
		{"definitions with verified origin", definitions, definitions, "/equipa/escaloes?" + originQuery},
		{"classification with verified origin", classification, classification, "/equipa/classificacao/" + classification.ID + "?" + originQuery},
		{"member preserves canonical collection", member, SeasonReturnContext{Kind: "member", ID: member.ID, Page: "2", Query: member.Query}, memberURL + "?return_to=" + url.QueryEscape(collection)},
		{"member with no collection filters", SeasonReturnContext{Kind: "member", ID: member.ID}, SeasonReturnContext{Kind: "member", ID: member.ID}, memberURL},
		{"malformed member", SeasonReturnContext{Kind: "member", ID: "bad-id"}, SeasonReturnContext{Kind: "list"}, seasonsPath},
		{"unknown group", SeasonReturnContext{Kind: "group", ID: uuid.NewString()}, SeasonReturnContext{Kind: "list"}, seasonsPath},
		{"empty group", SeasonReturnContext{Kind: "group"}, SeasonReturnContext{Kind: "list"}, seasonsPath},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, dest, err := store.ResolveReturn(t.Context(), f.actor, tc.in)
			if err != nil || got != tc.want || dest != tc.dest {
				t.Fatalf("return = %+v, %q, %v; want %+v, %q", got, dest, err, tc.want, tc.dest)
			}
		})
	}
	if _, dest, err := store.ResolveReturn(ctx, f.actor, SeasonReturnContext{Kind: "definitions", OriginGroup: uuid.NewString()}); !errors.Is(err, pgx.ErrNoRows) || dest != "" {
		t.Fatalf("unknown origin returned a destination: %q, %v", dest, err)
	}
	if _, dest, err := store.ResolveReturn(ctx, f.subject, member); !errors.Is(err, pgx.ErrNoRows) || dest != "" {
		t.Fatalf("non-administrator obtained a destination: %q, %v", dest, err)
	}
	if has, err := store.HasCurrentSeason(ctx, f.subject); !errors.Is(err, pgx.ErrNoRows) || has {
		t.Fatalf("non-administrator observed a current season: %t, %v", has, err)
	}
	if _, err := store.CreateSeason(ctx, f.subject, SeasonInput{Code: "DENIED", Name: "Forbidden creation", StartsOn: selected.Target.StartsOn.Time, EndsOn: selected.Target.EndsOn.Time}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("non-administrator attempted creation: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.GetSelectionState(canceled, f.actor, f.season); !errors.Is(err, context.Canceled) {
		t.Fatalf("selection ignored canceled database read: %v", err)
	}
	if err := store.SelectCurrentSeason(canceled, f.actor, f.season, selected.Version); !errors.Is(err, context.Canceled) {
		t.Fatalf("selection ignored canceled database write: %v", err)
	}
	if _, err := store.ListSeasons(canceled, f.actor, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("collection ignored canceled authorization: %v", err)
	}
}
