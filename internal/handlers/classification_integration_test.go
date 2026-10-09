//go:build integration

package handlers

import (
	"context"
	"errors"
	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestClassificationPostgresScopedReadAndRevocation(t *testing.T) {
	ctx := context.Background()
	pool, e := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	var today time.Time
	if e = pool.QueryRow(ctx, `SELECT (now() AT TIME ZONE 'Europe/Lisbon')::date`).Scan(&today); e != nil {
		t.Fatal(e)
	}
	admin, coach, subject, other, season := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{admin, coach, subject, other} {
		_, e = pool.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Scope subject',$2,'hash','1980-01-01')`, id, uuid.NewString()+"@example.test")
		if e != nil {
			t.Fatal(e)
		}
	}
	_, e = pool.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, admin)
	if e != nil {
		t.Fatal(e)
	}
	var prior uuid.UUID
	_ = pool.QueryRow(ctx, `SELECT id FROM seasons WHERE is_current`).Scan(&prior)
	_, e = pool.Exec(ctx, `UPDATE seasons SET is_current=false WHERE is_current`)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `UPDATE seasons SET is_current=false WHERE id=$1`, season)
		if prior != uuid.Nil {
			_, _ = pool.Exec(ctx, `UPDATE seasons SET is_current=true WHERE id=$1`, prior)
		}
	})
	_, e = pool.Exec(ctx, `INSERT INTO seasons(id,code,name,starts_on,ends_on,is_current) VALUES($1,$2,'Scope season',$3,$4,true)`, season, "CR_"+uuid.NewString()[:8], today.AddDate(0, 0, -10), today.AddDate(0, 0, 60))
	if e != nil {
		t.Fatal(e)
	}
	var leisure uuid.UUID
	e = pool.QueryRow(ctx, `SELECT id FROM programmes WHERE code='Leisure'`).Scan(&leisure)
	if e != nil {
		t.Fatal(e)
	}
	_, e = pool.Exec(ctx, `INSERT INTO user_memberships(user_id,season_id,programme_id,starts_on) VALUES($1,$2,$3,$4)`, subject, season, leisure, today.AddDate(0, 0, -5))
	if e != nil {
		t.Fatal(e)
	}
	grant := uuid.New()
	_, e = pool.Exec(ctx, `INSERT INTO staff_grants(id,user_id,capability,programme_id,granted_by_id) VALUES($1,$2,'COACH',$3,$4)`, grant, coach, leisure, admin)
	if e != nil {
		t.Fatal(e)
	}
	store := PostgresClassificationStore{Pool: pool}
	firstName, firstRows, e := store.View(ctx, coach, other)
	if e != nil || firstName != "Scope subject" || len(firstRows) != 0 {
		t.Fatalf("first-assignment view name=%q rows=%v err=%v", firstName, firstRows, e)
	}
	firstOptions, e := store.Options(ctx, coach, other)
	if e != nil || len(firstOptions) != 1 || firstOptions[0].Code != "Leisure" {
		t.Fatalf("first-assignment options=%v err=%v", firstOptions, e)
	}
	missingName, missingRows, e := store.View(ctx, coach, uuid.New())
	if !errors.Is(e, ErrClassificationDenied) || missingName != "" || len(missingRows) != 0 {
		t.Fatalf("unknown direct URL leaked existence: %q %v %v", missingName, missingRows, e)
	}
	if _, _, e = store.Selections(ctx, coach, other); e != nil {
		t.Fatalf("first-assignment selections %v", e)
	}
	firstReq := httptest.NewRequest("GET", "/equipa/classificacao/"+other.String(), nil)
	firstReq.SetPathValue("id", other.String())
	firstReq = firstReq.WithContext(context.WithValue(firstReq.Context(), currentUserKey{}, CurrentUser{ID: coach}))
	firstResponse := httptest.NewRecorder()
	(Classification{Store: store}).Get(firstResponse, firstReq)
	if firstResponse.Code != 200 || !strings.Contains(firstResponse.Body.String(), "Scope subject") || strings.Contains(firstResponse.Body.String(), `id="taxon-title"`) {
		t.Fatalf("first GET status=%d body=%s", firstResponse.Code, firstResponse.Body.String())
	}
	firstValues := url.Values{"scope": {season.String() + ":" + leisure.String()}, "starts_on": {today.Format("2006-01-02")}}
	firstPost := httptest.NewRequest("POST", "/equipa/classificacao/"+other.String(), strings.NewReader(firstValues.Encode()))
	firstPost.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	firstPost.SetPathValue("id", other.String())
	firstPost = firstPost.WithContext(context.WithValue(firstPost.Context(), currentUserKey{}, CurrentUser{ID: coach}))
	postResponse := httptest.NewRecorder()
	(Classification{Store: store}).Post(postResponse, firstPost)
	if postResponse.Code != 200 {
		t.Fatalf("first POST status=%d body=%s", postResponse.Code, postResponse.Body.String())
	}
	firstValues.Set("preview_token", extractPreviewToken(t, postResponse.Body.String()))
	firstValues.Set("confirm", "yes")
	firstPost = httptest.NewRequest("POST", "/equipa/classificacao/"+other.String(), strings.NewReader(firstValues.Encode()))
	firstPost.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	firstPost.SetPathValue("id", other.String())
	firstPost = firstPost.WithContext(context.WithValue(firstPost.Context(), currentUserKey{}, CurrentUser{ID: coach}))
	postResponse = httptest.NewRecorder()
	(Classification{Store: store}).Post(postResponse, firstPost)
	if postResponse.Code != 200 {
		t.Fatalf("confirm status %d: %s", postResponse.Code, postResponse.Body.String())
	}
	var firstCount int
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM user_memberships WHERE user_id=$1 AND season_id=$2 AND programme_id=$3`, other, season, leisure).Scan(&firstCount); e != nil || firstCount != 1 {
		t.Fatalf("first assignment count=%d err=%v", firstCount, e)
	}
	name, rows, e := store.View(ctx, coach, subject)
	if e != nil || name == "" || len(rows) != 1 {
		t.Fatalf("scoped read name=%q rows=%v err=%v", name, rows, e)
	}
	options, e := store.Options(ctx, coach, subject)
	if e != nil || len(options) != 1 || options[0].Code != "Leisure" {
		t.Fatalf("options=%v err=%v", options, e)
	}
	sportVersion, _, _ := store.Versions(ctx, coach, subject)
	if e := store.ReplaceSelections(ctx, db.SportAssignmentCorrection{ExpectedVersion: sportVersion, ActorID: coach, MemberID: subject, Kind: db.SportingModalityAssignment, Codes: []string{"CANOEING"}, Reason: "Preparação da classificação"}); e != nil {
		t.Fatalf("assign modality %v", e)
	}
	sportVersion, _, _ = store.Versions(ctx, coach, subject)
	if e := store.ReplaceSelections(ctx, db.SportAssignmentCorrection{ExpectedVersion: sportVersion, ActorID: coach, MemberID: subject, Kind: db.CanoeCraftAssignment, Codes: []string{"K1"}, Reason: "Preparação da classificação"}); e != nil {
		t.Fatalf("assign craft %v", e)
	}
	mods, crafts, e := store.Selections(ctx, coach, subject)
	if e != nil || len(mods) != 1 || len(crafts) != 1 {
		t.Fatalf("selections %v %v %v", mods, crafts, e)
	}
	correction := func(actorID uuid.UUID, kind string, codes []string, reason string) int {
		t.Helper()
		version, _ := db.SportingClassificationVersion(ctx, pool, subject)
		values := url.Values{"kind": {kind}, "codes": codes, "reason": {reason}, "original_token": {signClassificationConfirmation(confirmationBinding(actorID, subject, kind, nil), version, time.Now())}}
		r := httptest.NewRequest("POST", "/equipa/classificacao/"+subject.String()+"/selecoes", strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.SetPathValue("id", subject.String())
		r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actorID}))
		w := httptest.NewRecorder()
		(Classification{Store: store}).ReplaceSelections(w, r)
		return w.Code
	}
	if status := correction(coach, "SPORT", []string{"SUP"}, "Troca de modalidade"); status != 200 {
		t.Fatalf("replace status %d", status)
	}
	mods, crafts, e = store.Selections(ctx, coach, subject)
	if e != nil || len(mods) != 1 || mods[0].Code != "SUP" || len(crafts) != 0 {
		t.Fatalf("cascade selections %v %v %v", mods, crafts, e)
	}
	var events int
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM person_sport_assignment_events WHERE subject_user_id=$1 AND actor_user_id=$2 AND reason='Troca de modalidade'`, subject, coach).Scan(&events); e != nil || events != 3 {
		t.Fatalf("audit events %d %v", events, e)
	}
	if status := correction(coach, "CRAFT", []string{"K1"}, "Tentativa inválida"); status != 422 {
		t.Fatalf("craft without canoe status %d", status)
	}
	if status := correction(coach, "SPORT", nil, "Remover todas"); status != 200 {
		t.Fatalf("remove all status %d", status)
	}
	_, e = pool.Exec(ctx, `UPDATE staff_grants SET revoked_at=now(),revoked_by_id=$1,revoke_reason='test' WHERE id=$2`, admin, grant)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = store.View(ctx, coach, subject); !errors.Is(e, ErrClassificationDenied) {
		t.Fatalf("revoked view %v", e)
	}
	if _, _, e = store.View(ctx, coach, uuid.New()); !errors.Is(e, ErrClassificationDenied) {
		t.Fatalf("revoked first-assignment view %v", e)
	}
	if status := correction(coach, "SPORT", []string{"SUP"}, "Tentativa revogada"); status != 404 {
		t.Fatalf("revoked correction status %d", status)
	}
}
