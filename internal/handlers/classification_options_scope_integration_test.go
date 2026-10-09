//go:build integration

package handlers

import (
	"context"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestClassificationOptionsMaskOtherGrantedProgramme(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	admin, coach, subject, season := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{admin, coach, subject} {
		if _, err = pool.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Scope subject',$2,'hash','1980-06-15')`, id, uuid.NewString()+"@example.test"); err != nil {
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
	today := time.Now().In(lisbonLocation())
	if _, err = pool.Exec(ctx, `INSERT INTO seasons(id,code,name,starts_on,ends_on,is_current) VALUES($1,$2,'Scope season',$3,$4,true)`, season, "OS_"+uuid.NewString()[:8], today.AddDate(0, 0, -10), today.AddDate(0, 0, 60)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM user_memberships WHERE user_id=$1`, subject)
		_, _ = pool.Exec(ctx, `DELETE FROM staff_grants WHERE user_id=$1`, coach)
		_, _ = pool.Exec(ctx, `DELETE FROM competition_categories WHERE season_id=$1`, season)
		_, _ = pool.Exec(ctx, `DELETE FROM seasons WHERE id=$1`, season)
		if prior != uuid.Nil {
			_, _ = pool.Exec(ctx, `UPDATE seasons SET is_current=true WHERE id=$1`, prior)
		}
		_, _ = pool.Exec(ctx, `DELETE FROM user_platform_roles WHERE user_id=$1`, admin)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id IN ($1,$2,$3)`, admin, coach, subject)
	})

	var a, b uuid.UUID
	if err = pool.QueryRow(ctx, `SELECT id FROM programmes WHERE code='Competition'`).Scan(&a); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT id FROM programmes WHERE code='Initiation'`).Scan(&b); err != nil {
		t.Fatal(err)
	}
	for _, programme := range []uuid.UUID{a, b} {
		if _, err = pool.Exec(ctx, `INSERT INTO staff_grants(user_id,capability,programme_id,granted_by_id) VALUES($1,'COACH',$2,$3)`, coach, programme, admin); err != nil {
			t.Fatal(err)
		}
	}
	var categoryA, categoryB uuid.UUID
	if err = pool.QueryRow(ctx, `INSERT INTO competition_categories(season_id,programme_id,code,name_pt,birth_date_from,birth_date_to,approved_by_user_id,approved_at) VALUES($1,$2,'AGE_A','Intervalo A','1980-01-01','1980-12-31',$3,now()) RETURNING id`, season, a, admin).Scan(&categoryA); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO competition_categories(season_id,programme_id,code,name_pt,birth_date_from,birth_date_to,approved_by_user_id,approved_at) VALUES($1,$2,'AGE_B','Intervalo B','2010-01-01','2010-12-31',$3,now()) RETURNING id`, season, b, admin).Scan(&categoryB); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO user_memberships(user_id,season_id,programme_id,competition_category_id,starts_on) VALUES($1,$2,$3,$4,$5)`, subject, season, a, categoryA, today.AddDate(0, 0, -5)); err != nil {
		t.Fatal(err)
	}

	store := PostgresClassificationStore{Pool: pool}
	check := func(actor uuid.UUID, expectMasked bool) {
		t.Helper()
		options, err := store.Options(ctx, actor, subject)
		if err != nil {
			t.Fatal(err)
		}
		seen := map[uuid.UUID]ClassificationCategory{}
		for _, option := range options {
			for _, category := range option.Categories {
				seen[category.ID] = category
			}
		}
		if len(seen) != 2 || !seen[categoryA].Eligible || !strings.Contains(seen[categoryA].Eligibility, "1980-01-01") {
			t.Fatalf("anchored A not disclosed correctly: %+v", seen)
		}
		if expectMasked {
			if seen[categoryB].Eligible || seen[categoryB].Eligibility != "Elegibilidade verificada ao guardar" {
				t.Fatalf("unanchored B leaked age/eligibility: %+v", seen[categoryB])
			}
		} else if seen[categoryB].Eligible || !strings.Contains(seen[categoryB].Eligibility, "2010-01-01") {
			t.Fatalf("administrator B not disclosed correctly: %+v", seen[categoryB])
		}
	}
	check(coach, true)
	check(admin, false)

	r := httptest.NewRequest("GET", "/equipa/classificacao/"+subject.String(), nil)
	r.SetPathValue("id", subject.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: coach}))
	w := httptest.NewRecorder()
	(Classification{Store: store}).Get(w, r)
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, "1980-01-01") || !strings.Contains(body, "Elegibilidade verificada ao guardar") || strings.Contains(body, "2010-01-01") || strings.Contains(body, "2010-12-31") {
		t.Fatalf("GET leaked unanchored B age: status=%d body=%s", w.Code, body)
	}

	// A masked first assignment must still use actual eligibility at save time.
	first := uuid.New()
	if _, err = pool.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'First assignment',$2,'hash','2010-06-15')`, first, uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM user_memberships WHERE user_id=$1`, first)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, first)
	})
	firstOptions, err := store.Options(ctx, coach, first)
	if err != nil {
		t.Fatal(err)
	}
	firstFound := false
	for _, option := range firstOptions {
		for _, category := range option.Categories {
			if category.ID == categoryB {
				firstFound = true
				if category.Eligible || category.Eligibility != "Elegibilidade verificada ao guardar" {
					t.Fatalf("first-assignment B not masked: %+v", category)
				}
			}
		}
	}
	if !firstFound {
		t.Fatal("first-assignment B category missing")
	}
	values := url.Values{"scope": {season.String() + ":" + b.String()}, "starts_on": {today.Format("2006-01-02")}, "category_id": {categoryB.String()}}
	post := httptest.NewRequest("POST", "/equipa/classificacao/"+first.String(), strings.NewReader(values.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.SetPathValue("id", first.String())
	post = post.WithContext(context.WithValue(post.Context(), currentUserKey{}, CurrentUser{ID: coach}))
	posted := httptest.NewRecorder()
	(Classification{Store: store}).Post(posted, post)
	if posted.Code != 200 {
		t.Fatalf("eligible first assignment rejected because options masked: %d %s", posted.Code, posted.Body.String())
	}
	confirmDatedValues(t, Classification{Store: store}, coach, first, values, posted)
	var persisted int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM user_memberships WHERE user_id=$1 AND competition_category_id=$2 AND age_exception_reason IS NULL`, first, categoryB).Scan(&persisted); err != nil || persisted != 1 {
		t.Fatalf("eligible first assignment not saved normally: count=%d err=%v", persisted, err)
	}

	// Conversely a masked mismatch still requires a reason at save time.
	mismatch := uuid.New()
	if _, err = pool.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Mismatch assignment',$2,'hash','1980-06-15')`, mismatch, uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM user_memberships WHERE user_id=$1`, mismatch)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, mismatch)
	})
	postMismatch := func() *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("POST", "/equipa/classificacao/"+mismatch.String(), strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.SetPathValue("id", mismatch.String())
		r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: coach}))
		w := httptest.NewRecorder()
		(Classification{Store: store}).Post(w, r)
		return w
	}
	if w := postMismatch(); w.Code != 422 || !strings.Contains(w.Body.String(), `id="age_exception_reason"`) {
		t.Fatalf("masked mismatch without reason: %d %s", w.Code, w.Body.String())
	}
	values.Set("age_exception_reason", "Diferença confirmada")
	values.Del("confirm")
	values.Del("preview_token")
	previewMismatch := postMismatch()
	values.Set("preview_token", extractPreviewToken(t, previewMismatch.Body.String()))
	values.Set("confirm", "yes")
	if w := postMismatch(); w.Code != 200 {
		t.Fatalf("masked mismatch with reason: %d %s", w.Code, w.Body.String())
	}
	var reason string
	if err = pool.QueryRow(ctx, `SELECT age_exception_reason FROM user_memberships WHERE user_id=$1 AND competition_category_id=$2`, mismatch, categoryB).Scan(&reason); err != nil || reason != "Diferença confirmada" {
		t.Fatalf("age exception not saved: reason=%q err=%v", reason, err)
	}
}
