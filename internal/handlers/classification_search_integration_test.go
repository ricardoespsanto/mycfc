//go:build integration

package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestClassificationNameSelectionJourney(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, e := pool.Exec(ctx, sql, args...); e != nil {
			t.Fatal(e)
		}
	}
	admin, coach, teamCoach, guardian, dependent, inactive, erased, season := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	prefix := "Selecionável " + uuid.NewString()[:8]
	for _, id := range []uuid.UUID{admin, coach, teamCoach, guardian, inactive} {
		exec(`INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,$2,$3,'hash','1980-01-01')`, id, prefix+" staff", uuid.NewString()+"@example.test")
	}
	exec(`UPDATE users SET is_active=false WHERE id=$1`, inactive)
	exec(`INSERT INTO users(id,name,is_dependent,date_of_birth) VALUES($1,$2,true,'2015-01-01')`, dependent, prefix+" dependent")
	// A retired erased principal fixture, not an erasure operation. Bypass only
	// foreign-key provenance on this isolated superuser connection; all CHECKs
	// and all application queries remain real. No privacy workflow is activated.
	conn, e := pool.Acquire(ctx)
	if e != nil {
		t.Fatal(e)
	}
	_, e = conn.Exec(ctx, `SET session_replication_role=replica`)
	if e == nil {
		_, e = conn.Exec(ctx, `INSERT INTO users(id,name,date_of_birth,is_active,leaderboard_visible,erased_at,erasure_execution_id) VALUES($1,'Conta eliminada','1900-01-01',false,false,now(),$2)`, erased, uuid.New())
	}
	_, resetErr := conn.Exec(ctx, `SET session_replication_role=origin`)
	conn.Release()
	if e != nil || resetErr != nil {
		t.Fatalf("erased fixture %v %v", e, resetErr)
	}
	exec(`INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, admin)
	var prior uuid.UUID
	_ = pool.QueryRow(ctx, `SELECT id FROM seasons WHERE is_current`).Scan(&prior)
	exec(`UPDATE seasons SET is_current=false WHERE is_current`)
	today := time.Now().In(lisbonLocation())
	exec(`INSERT INTO seasons(id,code,name,starts_on,ends_on,is_current) VALUES($1,$2,'Search season',$3,$4,true)`, season, "NS"+uuid.NewString()[:8], today.AddDate(0, 0, -10), today.AddDate(0, 0, 30))
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `UPDATE seasons SET is_current=false WHERE id=$1`, season)
		if prior != uuid.Nil {
			_, _ = pool.Exec(ctx, `UPDATE seasons SET is_current=true WHERE id=$1`, prior)
		}
	})
	var leisure, polo uuid.UUID
	if err = pool.QueryRow(ctx, `SELECT id FROM programmes WHERE code='Leisure'`).Scan(&leisure); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT id FROM programmes WHERE code='Kayak_Polo'`).Scan(&polo); err != nil {
		t.Fatal(err)
	}
	grant, team := uuid.New(), uuid.New()
	exec(`INSERT INTO staff_grants(id,user_id,capability,programme_id,granted_by_id) VALUES($1,$2,'COACH',$3,$4)`, grant, coach, leisure, admin)
	exec(`INSERT INTO teams(id,name,code,season_id,programme_id) VALUES($1,'Search Polo','SEARCH_POLO',$2,$3)`, team, season, polo)
	exec(`INSERT INTO staff_grants(user_id,capability,team_id,granted_by_id) VALUES($1,'COACH',$2,$3)`, teamCoach, team, admin)
	people := make([]uuid.UUID, 25)
	for i := range people {
		people[i] = uuid.New()
		exec(`INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,$2,$3,'hash','1980-01-01')`, people[i], fmt.Sprintf("%s pessoa %02d", prefix, i), uuid.NewString()+"@example.test")
	}
	store := PostgresClassificationStore{Pool: pool}
	h := Classification{Store: store}
	run := func(actor, subject uuid.UUID, method, path string, values url.Values) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if subject != uuid.Nil {
			r.SetPathValue("id", subject.String())
		}
		r = r.WithContext(context.WithValue(ctx, currentUserKey{}, CurrentUser{ID: actor}))
		w := httptest.NewRecorder()
		if subject == uuid.Nil {
			h.Search(w, r)
		} else if method == "GET" {
			h.Get(w, r)
		} else {
			h.Post(w, r)
		}
		return w
	}
	if found, more, e := store.Search(ctx, coach, "", 1); e != nil || len(found) != 0 || more {
		t.Fatalf("blank roster %v %v %v", found, more, e)
	}
	query := prefix + " pessoa"
	first, more, e := store.Search(ctx, coach, query, 1)
	if e != nil || len(first) != 20 || !more {
		t.Fatalf("first page %d %v %v", len(first), more, e)
	}
	second, more, e := store.Search(ctx, coach, query, 2)
	if e != nil || len(second) != 5 || more {
		t.Fatalf("second page %d %v %v", len(second), more, e)
	}
	seen := map[uuid.UUID]bool{}
	for _, p := range append(first, second...) {
		if seen[p.ID] {
			t.Fatal("duplicate page result")
		}
		seen[p.ID] = true
	}
	if len(seen) != 25 {
		t.Fatal(len(seen))
	}
	for _, query := range []string{"%", "_", strings.Repeat("x", 101)} {
		if found, _, e := store.Search(ctx, coach, query, 1); e != nil || len(found) != 0 {
			t.Fatalf("unbounded/wildcard search %v %v", found, e)
		}
	}
	search := run(coach, uuid.Nil, "GET", classificationSearchURL(prefix+" pessoa 00", 1), nil)
	if search.Code != 200 || !strings.Contains(search.Body.String(), "Selecionar "+prefix+" pessoa 00") || search.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("search %d %s", search.Code, search.Body.String())
	}
	for _, secret := range []string{"@example.test", "1980-01-01", "guardian_id", "password_hash", "minor_login"} {
		if strings.Contains(search.Body.String(), secret) {
			t.Fatal("search exposed " + secret)
		}
	}
	detail := run(coach, people[0], "GET", "/equipa/classificacao/"+people[0].String(), nil)
	if detail.Code != 200 || !strings.Contains(detail.Body.String(), prefix+" pessoa 00") || strings.Contains(detail.Body.String(), `id="taxon-title"`) {
		t.Fatalf("unanchored selection %d %s", detail.Code, detail.Body.String())
	}
	values := url.Values{"scope": {season.String() + ":" + leisure.String()}, "starts_on": {today.Format("2006-01-02")}}
	preview := run(coach, people[0], "POST", "/equipa/classificacao/"+people[0].String(), values)
	values.Set("preview_token", extractPreviewToken(t, preview.Body.String()))
	values.Set("confirm", "yes")
	if w := run(coach, people[0], "POST", "/equipa/classificacao/"+people[0].String(), values); w.Code != 200 {
		t.Fatalf("first assignment %d %s", w.Code, w.Body.String())
	}
	var saved int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM user_memberships WHERE user_id=$1 AND programme_id=$2`, people[0], leisure).Scan(&saved); err != nil || saved != 1 {
		t.Fatalf("saved %d %v", saved, err)
	}
	wrong := ClassificationWrite{ActorID: coach, MemberID: people[1], SeasonID: season, ProgrammeID: polo, StartsOn: today}
	if err = store.Write(ctx, wrong); !errors.Is(err, db.ErrDatedParticipationForbidden) {
		t.Fatalf("wrong programme service write %v", err)
	}
	forged := url.Values{"scope": {season.String() + ":" + polo.String()}, "starts_on": {today.Format("2006-01-02")}, "member_id": {people[0].String()}}
	if w := run(coach, people[1], "POST", "/equipa/classificacao/"+people[1].String(), forged); w.Code != 422 {
		t.Fatalf("forged programme/subject %d", w.Code)
	}
	exec(`INSERT INTO user_memberships(user_id,season_id,programme_id,team_id,starts_on) VALUES($1,$2,$3,$4,$5)`, people[2], season, polo, team, today)
	correction := db.SportAssignmentCorrection{ActorID: teamCoach, MemberID: people[2], Kind: db.SportingModalityAssignment, Codes: []string{"SUP"}, Reason: "Task-specific correction scope"}
	if e = store.ReplaceSelections(ctx, correction); !errors.Is(e, db.ErrSportAssignmentForbidden) {
		t.Fatalf("classification team-only correction service %v", e)
	}
	if e = (db.SportAssignmentService{Pool: pool}).Replace(ctx, correction); e != nil {
		t.Fatalf("generic existing team-scoped correction changed %v", e)
	}
	if mods, crafts, e := store.Selections(ctx, coach, people[2]); e != nil || len(mods) != 0 || len(crafts) != 0 {
		t.Fatalf("unanchored sporting selections leaked %v %v %v", mods, crafts, e)
	}
	if w := run(coach, people[2], "GET", "/equipa/classificacao/"+people[2].String(), nil); w.Code != 200 || strings.Contains(w.Body.String(), `id="taxon-title"`) || strings.Contains(w.Body.String(), "Modalidade: SUP") {
		t.Fatalf("unanchored other-programme details leaked %d %s", w.Code, w.Body.String())
	}
	for _, actor := range []uuid.UUID{teamCoach, guardian} {
		if _, _, e = store.Search(ctx, actor, query, 1); !errors.Is(e, ErrClassificationDenied) {
			t.Fatalf("unprivileged search %v", e)
		}
		if w := run(actor, people[1], "POST", "/equipa/classificacao/"+people[1].String(), values); w.Code != 404 {
			t.Fatal(w.Code)
		}
	}
	if w := run(coach, uuid.Nil, "GET", classificationSearchURL(prefix+" absent", 1), nil); w.Code != 200 || !strings.Contains(w.Body.String(), "Não foram encontradas pessoas disponíveis para classificação com este nome.") {
		t.Fatalf("empty name-search state %d %s", w.Code, w.Body.String())
	}
	if w := run(admin, uuid.Nil, "GET", classificationSearchURL(prefix+" pessoa 00", 1), nil); w.Code != 200 {
		t.Fatalf("admin search %d", w.Code)
	}
	for _, subject := range []uuid.UUID{uuid.New(), inactive, dependent, erased} {
		for _, method := range []string{"GET", "POST"} {
			w := run(coach, subject, method, "/equipa/classificacao/"+subject.String(), values)
			if w.Code != 404 || w.Body.String() != "404 page not found\n" {
				t.Fatalf("unavailable %s %d %s", method, w.Code, w.Body.String())
			}
		}
		if e = store.ReplaceSelections(ctx, db.SportAssignmentCorrection{ActorID: admin, MemberID: subject, Kind: db.SportingModalityAssignment, Codes: []string{"SUP"}, Reason: "Classification eligibility check"}); !errors.Is(e, db.ErrSportAssignmentForbidden) {
			t.Errorf("unavailable classification correction service: %v", e)
		}
		wrong.MemberID = subject
		wrong.ProgrammeID = leisure
		if e = store.Write(ctx, wrong); !errors.Is(e, db.ErrDatedParticipationForbidden) {
			t.Fatalf("unavailable service %v", e)
		}
	}
	available, _, e := store.Search(ctx, coach, prefix, 1)
	if e != nil {
		t.Fatal(e)
	}
	for _, p := range available {
		if p.ID == inactive || p.ID == dependent || p.ID == erased {
			t.Fatal("ineligible result")
		}
	}
	exec(`UPDATE staff_grants SET revoked_at=now(),revoked_by_id=$1,revoke_reason='selection revocation' WHERE id=$2`, admin, grant)
	if w := run(coach, uuid.Nil, "GET", classificationSearchURL(query, 1), nil); w.Code != 404 {
		t.Fatal(w.Code)
	}
	for _, method := range []string{"GET", "POST"} {
		if w := run(coach, people[1], method, "/equipa/classificacao/"+people[1].String(), values); w.Code != 404 {
			t.Fatalf("revoked %s %d", method, w.Code)
		}
	}
	correction.ActorID = coach
	correction.MemberID = people[0]
	if e = store.ReplaceSelections(ctx, correction); !errors.Is(e, db.ErrSportAssignmentForbidden) {
		t.Fatalf("revoked classification correction service %v", e)
	}
	wrong.MemberID = people[1]
	if e = store.Write(ctx, wrong); !errors.Is(e, db.ErrDatedParticipationForbidden) {
		t.Fatalf("revoked service %v", e)
	}
}
