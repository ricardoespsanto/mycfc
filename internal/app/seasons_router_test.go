package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alexedwards/scs/v2"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/handlers"
	"github.com/google/uuid"
)

type seasonRouterUsers struct {
	routerCurrentUserLookup
	admin bool
}

func (s seasonRouterUsers) GetActiveAccountByID(context.Context, uuid.UUID) (dbgen.GetActiveAccountByIDRow, error) {
	return dbgen.GetActiveAccountByIDRow{ID: s.id, IsActive: true, IsAdmin: s.admin, CredentialVersion: 1, ProfileComplete: true, EmailVerified: true}, nil
}

type seasonDisabledFeatures struct{}

func (seasonDisabledFeatures) ListFeatureFlags(context.Context) ([]dbgen.ListFeatureFlagsRow, error) {
	return []dbgen.ListFeatureFlagsRow{{FeatureKey: "structured_training_planning", Mode: "DISABLED"}}, nil
}

type routerSeasonStore struct {
	handlers.SeasonStore
	reads int
}

func (s *routerSeasonStore) Authorize(context.Context, uuid.UUID) error { s.reads++; return nil }
func (s *routerSeasonStore) ListSeasons(context.Context, uuid.UUID, int32) ([]dbgen.Season, error) {
	return nil, nil
}
func (s *routerSeasonStore) ResolveReturn(context.Context, uuid.UUID, handlers.SeasonReturnContext) (handlers.SeasonReturnContext, string, error) {
	return handlers.SeasonReturnContext{Kind: "list"}, "/admin/epocas", nil
}
func TestSeasonSetupRoutesRemainAvailableWhenPlanningDisabled(t *testing.T) {
	for _, admin := range []bool{true, false} {
		sessions := scs.New()
		id := uuid.New()
		auth := handlers.Auth{Sessions: sessions, Features: seasonDisabledFeatures{}, Users: seasonRouterUsers{routerCurrentUserLookup: routerCurrentUserLookup{id: id}, admin: admin}}
		store := &routerSeasonStore{}
		members := handlers.Members{Seasons: handlers.Seasons{Store: store}}
		router := sessions.LoadAndSave(auth.Load(newRouter(routerPinger{}, sessions, handlers.Landing{}, handlers.Login{}, handlers.Registration{}, handlers.EmailVerification{}, handlers.PasswordRecovery{}, auth, handlers.Dashboard{}, handlers.Repair{}, handlers.Events{}, handlers.Announcements{}, handlers.Training{}, handlers.StructuredTraining{}, members, handlers.Profile{}, handlers.News{}, handlers.Suggestions{}, handlers.PhotoAlbums{}, handlers.Foundation{}, handlers.PolarIntegration{})))
		seed := httptest.NewRecorder()
		sessions.LoadAndSave(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			sessions.Put(r.Context(), "user_id", id.String())
			sessions.Put(r.Context(), "credential_version", int64(1))
		})).ServeHTTP(seed, httptest.NewRequest("GET", "/", nil))
		cookie := seed.Result().Cookies()[0]
		for _, path := range []string{"/admin/epocas", "/admin/epocas/criar"} {
			r := httptest.NewRequest("GET", path, nil)
			r.AddCookie(cookie)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			want := 403
			if admin {
				want = 200
			}
			if w.Code != want {
				t.Fatalf("admin=%v path=%s status=%d body=%s", admin, path, w.Code, w.Body.String())
			}
		}
		r := httptest.NewRequest("GET", "/admin/treinos/estruturados", nil)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != 404 {
			t.Fatalf("planning should remain disabled status=%d", w.Code)
		}
		if !admin && store.reads != 0 {
			t.Fatal("non-admin reached season management")
		}
	}
}

func (s *routerSeasonStore) HasCurrentSeason(context.Context, uuid.UUID) (bool, error) {
	return false, nil
}
