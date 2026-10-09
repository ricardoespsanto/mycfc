//go:build integration

package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The real SQL runs against PostgreSQL. The tracer commits suspension on a
// separate connection immediately before the protected read, after the name
// eligibility check, without sleeps, mocks, or production test hooks.
type classificationRevocationTracer struct {
	match  string
	revoke func()
	fired  bool
}

func (tr *classificationRevocationTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if !tr.fired && strings.Contains(data.SQL, tr.match) {
		tr.fired = true
		tr.revoke()
	}
	return ctx
}
func (*classificationRevocationTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {
}

func TestClassificationReadsRecheckGuardianAfterEligibility(t *testing.T) {
	ctx, pool := integrationPool(t)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	admin, coach, guardian, subject, season := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{admin, coach, guardian} {
		exec(`INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Revocation staff',$2,'hash','1980-01-01')`, id, uuid.NewString()+"@example.test")
	}
	exec(`INSERT INTO users(id,name,is_dependent,date_of_birth) VALUES($1,'Protected dependent',true,'2015-06-15')`, subject)
	policy := enableGuardianAuthorityTestPolicy(t, ctx, pool, admin)
	verifyGuardianAuthorityTestRelationship(t, ctx, pool, guardian, subject, admin, policy)
	var prior uuid.UUID
	_ = pool.QueryRow(ctx, `SELECT id FROM seasons WHERE is_current`).Scan(&prior)
	exec(`UPDATE seasons SET is_current=false WHERE is_current`)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `UPDATE seasons SET is_current=false WHERE id=$1`, season)
		if prior != uuid.Nil {
			_, _ = pool.Exec(ctx, `UPDATE seasons SET is_current=true WHERE id=$1`, prior)
		}
	})
	exec(`INSERT INTO seasons(id,code,name,starts_on,ends_on,is_current) VALUES($1,$2,'Revocation season',CURRENT_DATE-10,CURRENT_DATE+60,true)`, season, "RV_"+uuid.NewString()[:8])
	var programme, category uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM programmes WHERE code='Competition'`).Scan(&programme); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO staff_grants(user_id,capability,programme_id,granted_by_id) VALUES($1,'COACH',$2,$3)`, coach, programme, admin)
	if err := pool.QueryRow(ctx, `INSERT INTO competition_categories(season_id,programme_id,code,name_pt,birth_date_from,birth_date_to,approved_by_user_id,approved_at) VALUES($1,$2,'REV','Protected category','2015-01-01','2015-12-31',$3,now()) RETURNING id`, season, programme, admin).Scan(&category); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO user_memberships(user_id,season_id,programme_id,competition_category_id,starts_on) VALUES($1,$2,$3,$4,CURRENT_DATE-5)`, subject, season, programme, category)
	exec(`INSERT INTO person_sporting_modalities(user_id,modality_code,created_at) VALUES($1,'CANOEING',now())`, subject)
	exec(`INSERT INTO person_canoe_craft_classes(user_id,craft_code,created_at) VALUES($1,'K1',now())`, subject)
	for _, actor := range []uuid.UUID{admin, coach} {
		for _, target := range []string{"FROM user_memberships m JOIN", "SELECT c.id,c.name_pt,CASE", "SELECT sm.code,sm.name_pt", "SELECT c.code,c.name_pt FROM person_canoe", "jsonb_agg(modality_code", "jsonb_build_array(id,season_id"} {
			t.Run(actor.String()+target, func(t *testing.T) {
				exec(`UPDATE guardian_authority_relationships SET state='VERIFIED' WHERE subject_user_id=$1`, subject)
				baseline := PostgresClassificationStore{Pool: pool}
				if _, history, err := baseline.View(ctx, actor, subject); err != nil || len(history) != 1 {
					t.Fatalf("eligible history fixture: %v %v", history, err)
				}
				if mods, crafts, err := baseline.Selections(ctx, actor, subject); err != nil || len(mods) != 1 || len(crafts) != 1 {
					t.Fatalf("eligible selections fixture: %v %v %v", mods, crafts, err)
				}
				options, err := baseline.Options(ctx, actor, subject)
				if err != nil || len(options) == 0 {
					t.Fatalf("eligible options fixture: %v %v", options, err)
				}
				found := false
				for _, option := range options {
					for _, c := range option.Categories {
						if c.ID == category && c.Eligible && !c.Masked {
							found = true
						}
					}
				}
				if !found {
					t.Fatal("eligible category fixture not disclosed before suspension")
				}
				tr := &classificationRevocationTracer{match: target, revoke: func() {
					exec(`UPDATE guardian_authority_relationships SET state='SUSPENDED',version=version+1,updated_at=clock_timestamp() WHERE subject_user_id=$1`, subject)
				}}
				config, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
				if err != nil {
					t.Fatal(err)
				}
				config.ConnConfig.Tracer = tr
				reads, err := pgxpool.NewWithConfig(ctx, config)
				if err != nil {
					t.Fatal(err)
				}
				defer reads.Close()
				store := PostgresClassificationStore{Pool: reads}
				switch target {
				case "FROM user_memberships m JOIN":
					_, history, err := store.View(ctx, actor, subject)
					if err != nil {
						t.Fatal(err)
					}
					if len(history) != 0 {
						t.Errorf("history disclosed after suspension: %+v", history)
					}
				case "SELECT c.id,c.name_pt,CASE":
					options, err := store.Options(ctx, actor, subject)
					if err != nil {
						t.Fatal(err)
					}
					for _, option := range options {
						if len(option.Categories) != 0 {
							t.Errorf("DOB/eligibility disclosed after suspension: %+v", option.Categories)
						}
					}
				case "jsonb_agg(modality_code", "jsonb_build_array(id,season_id":
					sport, dated, err := store.Versions(ctx, actor, subject)
					if !errors.Is(err, ErrClassificationDenied) || sport != "" || dated != "" {
						t.Errorf("version disclosed after suspension: sport=%q dated=%q err=%v", sport, dated, err)
					}
				default:
					mods, crafts, err := store.Selections(ctx, actor, subject)
					if err != nil {
						t.Fatal(err)
					}
					if target == "SELECT sm.code,sm.name_pt" && len(mods) != 0 {
						t.Errorf("modalities disclosed after suspension: %+v", mods)
					}
					if len(crafts) != 0 {
						t.Errorf("crafts disclosed after suspension: %+v", crafts)
					}
				}
				if !tr.fired {
					t.Fatal("revocation barrier was not reached")
				}
				var current bool
				if err := pool.QueryRow(ctx, `SELECT guardian_authority_current($1,$2)`, guardian, subject).Scan(&current); err != nil || current {
					t.Fatalf("authority not suspended: %v %v", current, err)
				}
				h := Classification{Store: store}
				mux := http.NewServeMux()
				mux.HandleFunc("GET /equipa/classificacao/{id}", h.Get)
				mux.HandleFunc("POST /equipa/classificacao/{id}", h.Post)
				mux.HandleFunc("POST /equipa/classificacao/{id}/selecoes", h.ReplaceSelections)
				for _, route := range []struct{ method, suffix string }{{"GET", ""}, {"POST", ""}, {"POST", "/selecoes"}} {
					r := httptest.NewRequest(route.method, "/equipa/classificacao/"+subject.String()+route.suffix, strings.NewReader("category_id=forged"))
					r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					r = r.WithContext(context.WithValue(ctx, currentUserKey{}, CurrentUser{ID: actor}))
					w := httptest.NewRecorder()
					mux.ServeHTTP(w, r)
					if w.Code != 404 || w.Body.String() != "404 page not found\n" {
						t.Fatalf("suspended routed %s %s disclosed: %d %s", route.method, route.suffix, w.Code, w.Body.String())
					}
				}
			})
		}
	}
}
