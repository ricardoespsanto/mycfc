//go:build integration

package handlers

import (
	"context"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWeekWorkspaceNavigationReadsActualLifecycleWithoutMutatingDrafts(t *testing.T) {
	pool := seasonTestPool(t)
	ctx := t.Context()
	q := dbgen.New(pool)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, e := pool.Exec(ctx, query, args...); e != nil {
			t.Fatal(e)
		}
	}
	actor, season := uuid.New(), uuid.New()
	exec(`INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Workspace coach',$2,'hash','1990-01-01')`, actor, actor.String()+"@example.test")
	exec(`INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, actor)
	var programme uuid.UUID
	if e := pool.QueryRow(ctx, `SELECT id FROM programmes WHERE code='Leisure'`).Scan(&programme); e != nil {
		t.Fatal(e)
	}
	monday := time.Date(2100, 11, 9, 0, 0, 0, 0, time.UTC)
	monday = monday.AddDate(0, 0, -(int(monday.Weekday())+6)%7)
	exec(`INSERT INTO seasons(id,code,name,starts_on,ends_on) VALUES($1,$2,'Workspace explicit dates',$3,$4)`, season, "W"+uuid.NewString()[:8], monday, monday.AddDate(0, 0, 6))
	groups := PostgresTrainingGroupStore{Pool: pool}
	group, e := groups.CreateGroup(ctx, TrainingGroupCreateInput{ActorID: actor, Name: "Empty draft workspace", ProgrammeID: &programme})
	if e != nil {
		t.Fatal(e)
	}
	week, e := q.CreateStructuredTrainingWeek(ctx, dbgen.CreateStructuredTrainingWeekParams{GroupID: group.ID, Title: "Seven-day workspace", WeekStart: pgtype.Date{Time: monday, Valid: true}, CreatedByID: actor})
	if e != nil {
		t.Fatal(e)
	}
	titles := []string{"First afternoon work", "Second afternoon work", "Saturday morning work", "Explicit morning rest"}
	starts := []time.Time{monday.Add(16 * time.Hour), monday.Add(18 * time.Hour), monday.AddDate(0, 0, 5).Add(10*time.Hour + 30*time.Minute), monday.AddDate(0, 0, 1).Add(7 * time.Hour)}
	sessions := make([]uuid.UUID, 0, len(titles))
	for i, title := range titles {
		kind := dbgen.TrainingEntryKindTRAINING
		if i == 3 {
			kind = dbgen.TrainingEntryKindREST
		}
		session, e := q.CreateStructuredTrainingSession(ctx, dbgen.CreateStructuredTrainingSessionParams{PlanID: week.ID, Title: title, StartsAt: pgtype.Timestamptz{Time: starts[i], Valid: true}, EndsAt: pgtype.Timestamptz{Time: starts[i].Add(45 * time.Minute), Valid: true}, EntryKind: kind, CreatedByID: actor})
		if e != nil {
			t.Fatal(e)
		}
		sessions = append(sessions, session.ID)
	}
	exec(`UPDATE training_sessions SET status='CANCELLED',cancelled_at=now(),cancelled_by_id=$2,cancellation_reason='Actual cancelled fixture' WHERE id=$1`, sessions[1], actor)
	snapshot := func() string {
		t.Helper()
		var value string
		if e := pool.QueryRow(ctx, `SELECT jsonb_build_object('plan',(SELECT to_jsonb(p) FROM training_plans p WHERE id=$1),'sessions',(SELECT jsonb_agg(to_jsonb(s) ORDER BY id) FROM training_sessions s WHERE plan_id=$1))::text`, week.ID).Scan(&value); e != nil {
			t.Fatal(e)
		}
		return value
	}
	before := snapshot()
	store := PostgresStructuredTrainingStore{Pool: pool}
	rows, e := store.ListStructuredTrainingOverviewForManager(ctx, dbgen.ListStructuredTrainingOverviewForManagerParams{IsAdmin: true, UserID: actor})
	if e != nil {
		t.Fatal(e)
	}
	foundCancelled := false
	for _, row := range rows {
		if row.SessionID != nil && *row.SessionID == sessions[1] {
			foundCancelled = row.SessionStatus != nil && *row.SessionStatus == "CANCELLED"
		}
	}
	if !foundCancelled {
		t.Fatal("actual cancellation lifecycle not projected")
	}
	// Selecting one appointment still renders the entire calendar, including the
	// second afternoon, Saturday work and explicit rest. This is a read, not publish.
	r := httptest.NewRequest("GET", strings.Split(structuredPlannerURL(group.ID.String(), week.ID.String(), sessions[0].String(), monday.Format("2006-01-02")), "#")[0], nil)
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actor, Name: "Workspace coach", IsAdmin: true}))
	w := httptest.NewRecorder()
	(StructuredTraining{Store: store, Groups: groups, Location: lisbonLocation()}).Index(w, r)
	if w.Code != 200 {
		t.Fatalf("workspace status=%d body=%s", w.Code, w.Body.String())
	}
	for _, title := range titles {
		if !strings.Contains(w.Body.String(), title) {
			t.Fatalf("calendar lost %q after session filter", title)
		}
	}
	if snapshot() != before {
		t.Fatal("navigation changed plan/session IDs, intervals, lifecycle or content")
	}
	var count int
	if e := pool.QueryRow(ctx, `SELECT count(*) FROM training_plan_publications WHERE plan_id=$1`, week.ID).Scan(&count); e != nil || count != 0 {
		t.Fatalf("draft navigation published count=%d error=%v", count, e)
	}
}
