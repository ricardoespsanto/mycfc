//go:build integration

package handlers

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDefinitionScopedPostgresAuthoring(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	admin, coach, outsider := uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{admin, coach, outsider} {
		_, err = pool.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Definition actor',$2,'hash','1980-01-01')`, id, uuid.NewString()+"@example.test")
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err = pool.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, admin)
	if err != nil {
		t.Fatal(err)
	}
	var season, competition, initiation, leisure uuid.UUID
	err = pool.QueryRow(ctx, `SELECT id FROM seasons WHERE is_current`).Scan(&season)
	if err != nil {
		var today time.Time
		err = pool.QueryRow(ctx, `SELECT (now() AT TIME ZONE 'Europe/Lisbon')::date`).Scan(&today)
		if err != nil {
			t.Fatal(err)
		}
		err = pool.QueryRow(ctx, `INSERT INTO seasons(code,name,starts_on,ends_on,is_current) VALUES($1,'Época teste',$2,$3,true) RETURNING id`, "D_"+uuid.NewString()[:8], today.AddDate(0, 0, -30), today.AddDate(0, 0, 90)).Scan(&season)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, x := range []struct {
		code string
		id   *uuid.UUID
	}{{"Competition", &competition}, {"Initiation", &initiation}, {"Leisure", &leisure}} {
		if err = pool.QueryRow(ctx, `SELECT id FROM programmes WHERE code=$1`, x.code).Scan(x.id); err != nil {
			t.Fatal(err)
		}
	}
	grant := uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO staff_grants(id,user_id,capability,programme_id,granted_by_id) VALUES($1,$2,'COACH',$3,$4)`, grant, coach, competition, admin)
	if err != nil {
		t.Fatal(err)
	}
	store := PostgresDefinitionStore{Pool: pool}
	h := Definitions{Store: store}
	scope := season.String() + ":" + competition.String()
	get := func(actor uuid.UUID) (int, string) {
		r := httptest.NewRequest("GET", "/equipa/escaloes", nil).WithContext(context.WithValue(ctx, currentUserKey{}, CurrentUser{ID: actor}))
		w := httptest.NewRecorder()
		h.Get(w, r)
		return w.Code, w.Body.String()
	}
	post := func(actor uuid.UUID, values url.Values) int {
		r := httptest.NewRequest("POST", "/equipa/escaloes", strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r = r.WithContext(context.WithValue(ctx, currentUserKey{}, CurrentUser{ID: actor}))
		w := httptest.NewRecorder()
		h.Post(w, r)
		return w.Code
	}
	values := url.Values{"scope": {scope}, "code": {"JOVENS"}, "name": {"Jovens"}, "birth_date_from": {"2009-01-01"}, "birth_date_to": {"2010-12-31"}}
	var seasonStart time.Time
	if err = pool.QueryRow(ctx, `SELECT starts_on FROM seasons WHERE id=$1`, season).Scan(&seasonStart); err != nil {
		t.Fatal(err)
	}
	future := url.Values{"scope": {scope}, "code": {"FUTURE"}, "name": {"Futuro"}, "birth_date_from": {seasonStart.AddDate(0, 0, 1).Format("2006-01-02")}}
	if status := post(coach, future); status != 422 {
		t.Fatalf("birth after season start %d", status)
	}
	if status := post(coach, values); status != 303 {
		t.Fatalf("coach create %d", status)
	}
	var approver uuid.UUID
	var from, to time.Time
	err = pool.QueryRow(ctx, `SELECT approved_by_user_id,birth_date_from,birth_date_to FROM competition_categories WHERE season_id=$1 AND programme_id=$2 AND code='JOVENS'`, season, competition).Scan(&approver, &from, &to)
	if err != nil || approver != coach || from.Format("2006-01-02") != "2009-01-01" || to.Format("2006-01-02") != "2010-12-31" {
		t.Fatalf("provenance/range %s %s %s %v", approver, from, to, err)
	}
	if status := post(coach, values); status != 409 {
		t.Fatalf("duplicate %d", status)
	}
	if status, body := get(outsider); status != 404 || strings.Contains(body, "Jovens") {
		t.Fatalf("outsider read %d %s", status, body)
	}
	if status := post(outsider, values); status != 404 {
		t.Fatalf("outsider write %d", status)
	}
	if status, body := get(coach); status != 200 || !strings.Contains(body, "Jovens") {
		t.Fatalf("coach read %d", status)
	}
	other := url.Values{"scope": {season.String() + ":" + initiation.String()}, "code": {"OTHER"}, "name": {"Outro"}}
	if status := post(coach, other); status != 404 {
		t.Fatalf("wrong programme %d", status)
	}
	if status := post(admin, other); status != 303 {
		t.Fatalf("admin initiation %d", status)
	}
	if status, body := get(coach); status != 200 || strings.Contains(body, "Outro") {
		t.Fatalf("cross-programme definition leaked: status=%d", status)
	}
	other.Set("scope", season.String()+":"+leisure.String())
	if status := post(admin, other); status != 404 {
		t.Fatalf("leisure %d", status)
	}
	_, err = pool.Exec(ctx, `UPDATE staff_grants SET revoked_at=now(),revoked_by_id=$1,revoke_reason='test' WHERE id=$2`, admin, grant)
	if err != nil {
		t.Fatal(err)
	}
	if status := post(coach, url.Values{"scope": {scope}, "code": {"REVOKED"}, "name": {"Revogado"}}); status != 404 {
		t.Fatalf("revoked write %d", status)
	}
	if status, _ := get(coach); status != 404 {
		t.Fatalf("revoked read %d", status)
	}
	if err = store.Create(ctx, coach, DefinitionInput{SeasonID: season, ProgrammeID: competition, Code: "DIRECT", Name: "Direto"}); !errors.Is(err, errDefinitionScope) {
		t.Fatalf("direct store revocation %v", err)
	}
}
