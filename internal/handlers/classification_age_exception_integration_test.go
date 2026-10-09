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

func TestClassificationAgeExceptionPostgresHandler(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	admin, member, outsider, season := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{admin, member, outsider} {
		if _, err = pool.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Idade teste',$2,'hash','1980-01-01')`, id, uuid.NewString()+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = pool.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, admin); err != nil {
		t.Fatal(err)
	}
	var prior uuid.UUID
	_ = pool.QueryRow(ctx, `SELECT id FROM seasons WHERE is_current`).Scan(&prior)
	if _, err = pool.Exec(ctx, `UPDATE seasons SET is_current=false WHERE is_current`); err != nil {
		t.Fatal(err)
	}
	var today time.Time
	if err = pool.QueryRow(ctx, `SELECT (now() AT TIME ZONE 'Europe/Lisbon')::date`).Scan(&today); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO seasons(id,code,name,starts_on,ends_on,is_current) VALUES($1,$2,'Idade teste',$3,$4,true)`, season, "IH_"+uuid.NewString()[:8], today.AddDate(0, 0, -10), today.AddDate(0, 0, 60)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM user_memberships WHERE user_id=$1`, member)
		_, _ = pool.Exec(ctx, `DELETE FROM competition_categories WHERE season_id=$1`, season)
		_, _ = pool.Exec(ctx, `DELETE FROM seasons WHERE id=$1`, season)
		if prior != uuid.Nil {
			_, _ = pool.Exec(ctx, `UPDATE seasons SET is_current=true WHERE id=$1`, prior)
		}
		_, _ = pool.Exec(ctx, `DELETE FROM user_platform_roles WHERE user_id=$1`, admin)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id IN ($1,$2,$3)`, admin, member, outsider)
	})
	var programme, category uuid.UUID
	if err = pool.QueryRow(ctx, `SELECT id FROM programmes WHERE code='Competition'`).Scan(&programme); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO competition_categories(season_id,programme_id,code,name_pt,birth_date_from,birth_date_to,approved_by_user_id,approved_at) VALUES($1,$2,'MISMATCH','Fora do intervalo','2010-01-01','2010-12-31',$3,now()) RETURNING id`, season, programme, admin).Scan(&category); err != nil {
		t.Fatal(err)
	}
	store := PostgresClassificationStore{Pool: pool}
	options, err := store.Options(ctx, admin, member)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, o := range options {
		for _, c := range o.Categories {
			if c.ID == category && !c.Eligible && strings.Contains(c.Eligibility, "idade no início da época:") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("mismatch category missing")
	}
	v := url.Values{"scope": {season.String() + ":" + programme.String()}, "starts_on": {today.Format("2006-01-02")}, "category_id": {category.String()}, "age_exception_reason": {"Diferença confirmada"}}
	post := func(actor uuid.UUID, values url.Values) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/equipa/classificacao/"+member.String(), strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.SetPathValue("id", member.String())
		r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actor}))
		w := httptest.NewRecorder()
		(Classification{Store: store}).Post(w, r)
		return w
	}
	if w := post(outsider, v); w.Code != 404 || strings.Contains(w.Body.String(), "Idade teste") {
		t.Fatalf("outsider %d %s", w.Code, w.Body.String())
	}
	forged := url.Values{}
	for k, values := range v {
		forged[k] = append([]string(nil), values...)
	}
	forged.Set("actor_id", admin.String())
	if w := post(outsider, forged); w.Code != 404 {
		t.Fatalf("forged outsider %d", w.Code)
	}
	preview := post(admin, v)
	v.Set("preview_token", extractPreviewToken(t, preview.Body.String()))
	v.Set("confirm", "yes")
	if w := post(admin, v); w.Code != 200 || strings.Contains(w.Body.String(), "Diferença confirmada") {
		t.Fatalf("admin %d %s", w.Code, w.Body.String())
	}
	var reason string
	var actor uuid.UUID
	if err = pool.QueryRow(ctx, `SELECT age_exception_reason,age_exception_by_id FROM user_memberships WHERE user_id=$1`, member).Scan(&reason, &actor); err != nil || reason != "Diferença confirmada" || actor != admin {
		t.Fatalf("provenance %s %s %v", reason, actor, err)
	}
	if w := post(admin, v); w.Code != 409 || !strings.Contains(w.Body.String(), `id="error-summary"`) {
		t.Fatalf("stale %d", w.Code)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM user_memberships WHERE user_id=$1`, member).Scan(&count); err != nil || count != 1 {
		t.Fatalf("overlap persisted %d %v", count, err)
	}
	// A coach can only transition an existing subject in the exact programme.
	coach, scoped := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{coach, scoped} {
		if _, err = pool.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Âmbito teste',$2,'hash','1980-01-01')`, id, uuid.NewString()+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM user_memberships WHERE user_id=$1`, scoped)
		_, _ = pool.Exec(ctx, `DELETE FROM staff_grants WHERE user_id=$1`, coach)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id IN ($1,$2)`, coach, scoped)
	})
	var eligible uuid.UUID
	if err = pool.QueryRow(ctx, `INSERT INTO competition_categories(season_id,programme_id,code,name_pt,birth_date_from,birth_date_to,approved_by_user_id,approved_at) VALUES($1,$2,'ELIGIBLE','Intervalo normal','1979-01-01','1981-12-31',$3,now()) RETURNING id`, season, programme, admin).Scan(&eligible); err != nil {
		t.Fatal(err)
	}
	var previous uuid.UUID
	if err = pool.QueryRow(ctx, `INSERT INTO user_memberships(user_id,season_id,programme_id,competition_category_id,starts_on) VALUES($1,$2,$3,$4,$5::date) RETURNING id`, scoped, season, programme, eligible, today.AddDate(0, 0, -1)).Scan(&previous); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO staff_grants(user_id,capability,programme_id,granted_by_id) VALUES($1,'COACH',$2,$3)`, coach, programme, admin); err != nil {
		t.Fatal(err)
	}
	coachValues := url.Values{"scope": {season.String() + ":" + programme.String()}, "starts_on": {today.AddDate(0, 0, 1).Format("2006-01-02")}, "category_id": {category.String()}, "previous_id": {previous.String()}, "age_exception_reason": {"Diferença confirmada pela equipa"}}
	coachPost := func(actor uuid.UUID) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/equipa/classificacao/"+scoped.String(), strings.NewReader(coachValues.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.SetPathValue("id", scoped.String())
		r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actor}))
		w := httptest.NewRecorder()
		(Classification{Store: store}).Post(w, r)
		return w
	}
	if w := coachPost(outsider); w.Code != 404 {
		t.Fatalf("out of scope %d", w.Code)
	}
	coachPreview := coachPost(coach)
	coachValues.Set("preview_token", extractPreviewToken(t, coachPreview.Body.String()))
	coachValues.Set("confirm", "yes")
	if w := coachPost(coach); w.Code != 200 {
		t.Fatalf("scoped coach %d %s", w.Code, w.Body.String())
	}
	if err = pool.QueryRow(ctx, `SELECT age_exception_by_id FROM user_memberships WHERE user_id=$1 AND competition_category_id=$2`, scoped, category).Scan(&actor); err != nil || actor != coach {
		t.Fatalf("coach provenance %s %v", actor, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE staff_grants SET revoked_at=now(),revoked_by_id=$1 WHERE user_id=$2`, admin, coach); err != nil {
		t.Fatal(err)
	}
	if w := coachPost(coach); w.Code != 404 {
		t.Fatalf("revoked coach %d", w.Code)
	}
}
