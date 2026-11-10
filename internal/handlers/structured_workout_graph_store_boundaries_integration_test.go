//go:build integration

package handlers

import (
	"errors"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func workoutGraphStoreRequest(t *testing.T, f *publicationDomainFixture) FocusedWorkoutRequest {
	t.Helper()
	var week time.Time
	if err := f.web.QueryRow(t.Context(), `SELECT week_start FROM training_plans WHERE id=$1`, f.plan).Scan(&week); err != nil {
		t.Fatal(err)
	}
	return FocusedWorkoutRequest{ActorID: f.actor, PlanID: f.plan, Day: week.Format("2006-01-02")}
}

func TestWorkoutGraphPGPreviewScopesAndBoundsProfiles(t *testing.T) {
	f := newPublicationDomainFixture(t)
	enableGraphFixture(t, f)
	ctx := t.Context()
	req := workoutGraphStoreRequest(t, f)
	store := PostgresFocusedWorkoutStore{Pool: f.web}
	first := uuid.MustParse("39100000-0000-0000-0000-000000000101")
	second := uuid.MustParse("39100000-0000-0000-0000-000000000102")
	inactive := uuid.MustParse("39100000-0000-0000-0000-000000000103")
	if _, err := f.low.Exec(ctx, `INSERT INTO water_intensity_profiles(id,name,craft,revision,is_active,created_by_id) VALUES ($1,'Kayak synthetic','KAYAK',1,true,$4),($2,'Canoe synthetic','CANOE',2,true,$4),($3,'Inactive synthetic','KAYAK',1,false,$4)`, first, second, inactive, f.actor); err != nil {
		t.Fatal(err)
	}
	if _, err := dbgen.New(f.low).CreateWaterIntensityZone(ctx, dbgen.CreateWaterIntensityZoneParams{ProfileID: first, Code: "R2", Label: "Synthetic zone", Meaning: "Synthetic coaching guidance"}); err != nil {
		t.Fatal(err)
	}
	choices, capped, err := store.ListFocusedGraphProfileChoices(ctx, req)
	if err != nil || capped || len(choices) != 2 {
		t.Fatalf("scoped active profile choices=%+v capped=%t err=%v", choices, capped, err)
	}
	wantChoices := map[uuid.UUID]focusedGraphProfileChoice{
		first:  {ID: first, Name: "Kayak synthetic", Craft: "KAYAK", Revision: 1},
		second: {ID: second, Name: "Canoe synthetic", Craft: "CANOE", Revision: 2},
	}
	for _, choice := range choices {
		if choice != wantChoices[choice.ID] {
			t.Fatalf("inactive or incorrect revision offered: %+v", choice)
		}
		delete(wantChoices, choice.ID)
	}
	out, err := store.ReadFocusedGraphPreview(ctx, req, []uuid.UUID{second, first})
	if err != nil || out.Context.PlanID != f.plan || len(out.References.Profiles) != 2 || out.References.Profiles[0].ID != first || out.References.Profiles[1].ID != second || len(out.References.Zones) != 1 || out.References.Zones[0].ProfileID != first {
		t.Fatalf("selected profile closure does not match the exact request: %+v err=%v", out.References, err)
	}
	for _, tc := range []struct {
		name   string
		change func(*FocusedWorkoutRequest)
		ids    []uuid.UUID
		want   error
	}{
		{"invalid civil date", func(in *FocusedWorkoutRequest) { in.Day = "2027-02-30" }, nil, pgx.ErrNoRows},
		{"date outside the configured week", func(in *FocusedWorkoutRequest) { in.Day = "2000-01-01" }, nil, pgx.ErrNoRows},
		{"actor outside the group", func(in *FocusedWorkoutRequest) { in.ActorID = f.subject }, []uuid.UUID{first}, pgx.ErrNoRows},
		{"inactive profile", func(*FocusedWorkoutRequest) {}, []uuid.UUID{inactive}, errFocusedWorkoutConflict},
		{"missing profile", func(*FocusedWorkoutRequest) {}, []uuid.UUID{uuid.New()}, errFocusedWorkoutConflict},
		{"duplicate profile", func(*FocusedWorkoutRequest) {}, []uuid.UUID{first, first}, errFocusedWorkoutInvalid},
		{"missing profile identity", func(*FocusedWorkoutRequest) {}, []uuid.UUID{uuid.Nil}, errFocusedWorkoutInvalid},
		{"oversize selection", func(*FocusedWorkoutRequest) {}, make([]uuid.UUID, 33), errFocusedWorkoutLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := req
			tc.change(&in)
			out, err := store.ReadFocusedGraphPreview(t.Context(), in, tc.ids)
			if !errors.Is(err, tc.want) || len(out.References.Profiles) != 0 || len(out.References.Zones) != 0 {
				t.Fatalf("invalid preview returned references=%+v err=%v", out.References, err)
			}
		})
	}
	outside := req
	outside.ActorID = f.subject
	if choices, capped, err = store.ListFocusedGraphProfileChoices(ctx, outside); err != nil || len(choices) != 0 || capped {
		t.Fatalf("outside actor received profile data: count=%d capped=%t err=%v", len(choices), capped, err)
	}
	if _, err = f.low.Exec(ctx, `INSERT INTO water_intensity_zones(profile_id,position,code,label,meaning) SELECT $1,n,'Z'||n,'Synthetic zone '||n,'Synthetic guidance' FROM generate_series(2,257) n`, first); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReadFocusedGraphPreview(ctx, req, []uuid.UUID{first}); !errors.Is(err, errFocusedWorkoutLimit) {
		t.Fatalf("zone witness cap accepted oversize review: %v", err)
	}
	if _, err = f.low.Exec(ctx, `INSERT INTO water_intensity_profiles(name,craft,revision,created_by_id) SELECT 'Synthetic profile '||n,'KAYAK',1,$1 FROM generate_series(1,99)n`, f.actor); err != nil {
		t.Fatal(err)
	}
	if choices, capped, err = store.ListFocusedGraphProfileChoices(ctx, req); err != nil || !capped || len(choices) != 0 {
		t.Fatalf("profile picker returned a partial list: count=%d capped=%t err=%v", len(choices), capped, err)
	}
	closed, err := pgxpool.NewWithConfig(ctx, f.web.Config())
	if err != nil {
		t.Fatal(err)
	}
	closed.Close()
	offline := PostgresFocusedWorkoutStore{Pool: closed}
	if _, err = offline.ReadFocusedGraphPreview(ctx, req, nil); err == nil {
		t.Fatal("closed pool returned an admitted preview")
	}
	if rows, capped, err := offline.ListFocusedGraphProfileChoices(ctx, req); err == nil || len(rows) != 0 || capped {
		t.Fatalf("closed pool returned a choice result: count=%d capped=%t err=%v", len(rows), capped, err)
	}
}

func TestWorkoutGraphPGSaveRequiresCurrentAuthorityAndSelectedProfile(t *testing.T) {
	f := newPublicationDomainFixture(t)
	enableGraphFixture(t, f)
	ctx := t.Context()
	store := PostgresFocusedWorkoutStore{Pool: f.web}
	profile, err := dbgen.New(f.low).CreateWaterIntensityProfile(ctx, dbgen.CreateWaterIntensityProfileParams{Name: "Signed synthetic profile", Craft: "KAYAK", CreatedByID: f.actor})
	if err != nil {
		t.Fatal(err)
	}
	_, raw := focusedGraphWaterExample()
	raw.Set("activities[a_1].phases[p_1].water.profile_id", profile.ID.String())
	payload, review := graphRealReview(t, f, store, raw)
	req := workoutGraphStoreRequest(t, f)
	assertNoDraft := func(t *testing.T) {
		t.Helper()
		var exists bool
		if err := f.web.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM training_sessions WHERE id=$1)`, payload.Proposal.SessionID).Scan(&exists); err != nil || exists {
			t.Fatalf("failed transaction retained the signed draft: exists=%t err=%v", exists, err)
		}
	}
	unverified := review
	unverified.verified = false
	if out, err := store.SaveFocusedGraph(ctx, unverified); !errors.Is(err, errFocusedWorkoutInvalid) || out != (FocusedWorkoutSaveResult{}) {
		t.Fatalf("unverified proof produced a save: %+v %v", out, err)
	}
	assertNoDraft(t)
	// Keep a separate active administrator so the last-admin protection remains
	// enforced when the actor who owns this review loses access.
	backup := uuid.New()
	f.exec(t, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Synthetic secondary administrator',$2,'hash','1980-01-01')`, backup, backup.String()+"@example.test")
	f.exec(t, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, backup)
	if _, err = f.root.Exec(ctx, `UPDATE users SET is_active=false WHERE id=$1`, f.actor); err != nil {
		t.Fatal(err)
	}
	if out, err := store.SaveFocusedGraph(ctx, review); !errors.Is(err, pgx.ErrNoRows) || out != (FocusedWorkoutSaveResult{}) {
		t.Fatalf("inactive actor retained authority: %+v %v", out, err)
	}
	assertNoDraft(t)
	if _, err = f.root.Exec(ctx, `UPDATE users SET is_active=true WHERE id=$1`, f.actor); err != nil {
		t.Fatal(err)
	}
	if _, err = f.low.Exec(ctx, `UPDATE water_intensity_profiles SET is_active=false WHERE id=$1`, profile.ID); err != nil {
		t.Fatal(err)
	}
	if out, err := store.SaveFocusedGraph(ctx, review); !errors.Is(err, errFocusedWorkoutConflict) || out != (FocusedWorkoutSaveResult{}) {
		t.Fatalf("retired reference retained write admission: %+v %v", out, err)
	}
	assertNoDraft(t)
	if _, err = f.low.Exec(ctx, `UPDATE water_intensity_profiles SET is_active=true WHERE id=$1`, profile.ID); err != nil {
		t.Fatal(err)
	}
	payload, review = graphRealReview(t, f, store, raw)
	webRole := pgx.Identifier{f.roles.AppUsername}.Sanitize()
	if _, err = f.root.Exec(ctx, `REVOKE EXECUTE ON FUNCTION training_schedule_control.status() FROM `+webRole); err != nil {
		t.Fatal(err)
	}
	if out, err := store.ReadFocusedGraphPreview(ctx, req, nil); err == nil || out.Context != (FocusedWorkoutContext{}) {
		t.Fatalf("preview admitted unknown runtime: %+v %v", out.Context, err)
	}
	if out, err := store.SaveFocusedGraph(ctx, review); err == nil || out != (FocusedWorkoutSaveResult{}) {
		t.Fatalf("save admitted unknown runtime: %+v %v", out, err)
	}
	assertNoDraft(t)
	if _, err = f.root.Exec(ctx, `GRANT EXECUTE ON FUNCTION training_schedule_control.status() TO `+webRole); err != nil {
		t.Fatal(err)
	}
	if out, err := store.SaveFocusedGraph(ctx, review); err != nil || out.SessionID != payload.Proposal.SessionID || out.AlreadyCreated {
		t.Fatalf("valid review could not commit after runtime access was restored: %+v %v", out, err)
	}
}
