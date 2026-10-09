//go:build integration

package handlers

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

type interveningClassificationStore struct {
	PostgresClassificationStore
	beforeWrite func()
}

func (s interveningClassificationStore) Write(ctx context.Context, in ClassificationWrite) error {
	s.beforeWrite()
	return s.PostgresClassificationStore.Write(ctx, in)
}

func TestClassificationPreviewRechecksIntervalInsideTransaction(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	admin, member, season := uuid.New(), uuid.New(), uuid.New()
	var today time.Time
	if err := pool.QueryRow(ctx, `SELECT (now() AT TIME ZONE 'Europe/Lisbon')::date`).Scan(&today); err != nil {
		t.Fatal(err)
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []uuid.UUID{admin, member} {
		exec(`INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Preview',$2,'hash','1980-01-01')`, id, id.String()+"@example.test")
	}
	exec(`INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, admin)
	var prior uuid.UUID
	_ = pool.QueryRow(ctx, `SELECT id FROM seasons WHERE is_current`).Scan(&prior)
	exec(`UPDATE seasons SET is_current=false WHERE is_current`)
	defer func() {
		_, _ = pool.Exec(ctx, `UPDATE seasons SET is_current=false WHERE id=$1`, season)
		if prior != uuid.Nil {
			_, _ = pool.Exec(ctx, `UPDATE seasons SET is_current=true WHERE id=$1`, prior)
		}
	}()
	exec(`INSERT INTO seasons(id,code,name,starts_on,ends_on,is_current) VALUES($1,$2,'Preview',$3,$4,true)`, season, "PV_"+uuid.NewString()[:8], today.AddDate(0, 0, -10), today.AddDate(0, 0, 60))
	var leisure, initiation, old uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM programmes WHERE code='Leisure'`).Scan(&leisure); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM programmes WHERE code='Initiation'`).Scan(&initiation); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO user_memberships(user_id,season_id,programme_id,starts_on) VALUES($1,$2,$3,$4) RETURNING id`, member, season, leisure, today.AddDate(0, 0, -5)).Scan(&old); err != nil {
		t.Fatal(err)
	}
	store := PostgresClassificationStore{Pool: pool}
	v := url.Values{"scope": {season.String() + ":" + initiation.String()}, "starts_on": {today.AddDate(0, 0, 1).Format("2006-01-02")}, "previous_id": {old.String()}}
	post := func(h Classification) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/equipa/classificacao/"+member.String(), strings.NewReader(v.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.SetPathValue("id", member.String())
		r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: admin}))
		w := httptest.NewRecorder()
		h.Post(w, r)
		return w
	}
	preview := post(Classification{Store: store})
	if !strings.Contains(preview.Body.String(), "de "+today.AddDate(0, 0, 1).Format("2006-01-02")+" até "+today.AddDate(0, 0, 60).Format("2006-01-02")) {
		t.Fatalf("preview must show actual season end: %s", preview.Body.String())
	}
	if preview.Code != 200 || !strings.Contains(preview.Body.String(), "Intervalo anterior") || !strings.Contains(preview.Body.String(), today.Format("2006-01-02")) {
		t.Fatalf("preview %d %s", preview.Code, preview.Body.String())
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM user_memberships WHERE user_id=$1`, member).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("preview wrote")
	}
	v.Set("preview_token", extractPreviewToken(t, preview.Body.String()))
	v.Set("confirm", "yes")
	validToken := v.Get("preview_token")
	for _, bad := range []string{"forged", "changed-date", "expired"} {
		switch bad {
		case "forged":
			v.Set("preview_token", validToken+"x")
		case "changed-date":
			v.Set("starts_on", today.Format("2006-01-02"))
		case "expired":
			start, _ := time.Parse("2006-01-02", v.Get("starts_on"))
			in := ClassificationWrite{ActorID: admin, MemberID: member, SeasonID: season, ProgrammeID: initiation, StartsOn: start, PreviousID: &old}
			_, version, err := store.Versions(ctx, admin, member)
			if err != nil {
				t.Fatal(err)
			}
			v.Set("preview_token", signClassificationConfirmation(confirmationBinding(admin, member, "DATED", in), version, time.Now().Add(-16*time.Minute)))
		}
		denied := post(Classification{Store: store})
		want := 409
		if bad == "changed-date" {
			want = 422
		}
		if denied.Code != want {
			t.Fatalf("%s status=%d", bad, denied.Code)
		}
		v.Set("starts_on", today.AddDate(0, 0, 1).Format("2006-01-02"))
		v.Set("preview_token", validToken)
	}
	intervening := interveningClassificationStore{PostgresClassificationStore: store, beforeWrite: func() {
		exec(`UPDATE user_memberships SET ends_on=$2,updated_at=now() WHERE id=$1`, old, today.AddDate(0, 0, 3))
	}}
	response := post(Classification{Store: intervening})
	if response.Code != 409 {
		t.Fatalf("stale transaction %d %s", response.Code, response.Body.String())
	}
	var end string
	if err := pool.QueryRow(ctx, `SELECT ends_on::text FROM user_memberships WHERE id=$1`, old).Scan(&end); err != nil || end != today.AddDate(0, 0, 3).Format("2006-01-02") {
		t.Fatalf("intervening end overwritten %q %v", end, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM user_memberships WHERE user_id=$1`, member).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("stale inserted %d", count)
	}
	// An explicit refreshed preview and confirmation succeeds, old history IDs survive.
	v.Del("confirm")
	v.Del("preview_token")
	fresh := post(Classification{Store: store})
	confirmDatedValues(t, Classification{Store: store}, admin, member, v, fresh)
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM user_memberships WHERE user_id=$1`, member).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("confirmed count %d", count)
	}
}
