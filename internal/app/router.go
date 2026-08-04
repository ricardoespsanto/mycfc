package app

import (
	"io/fs"
	"net/http"
	"regexp"
	"strings"

	"github.com/alexedwards/scs/v2"
	"github.com/cfcoimbra/mycfc/internal/handlers"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	staticassets "github.com/cfcoimbra/mycfc/ui/static"
)

var fingerprintedAsset = regexp.MustCompile(`-[0-9a-f]{12}\.(?:css|js|png)$`)

func newRouter(pool handlers.DBPinger, sessions *scs.SessionManager, landing handlers.Landing, login handlers.Login, registration handlers.Registration, auth handlers.Auth, dashboard handlers.Dashboard, repair handlers.Repair, events handlers.Events, announcements handlers.Announcements, training handlers.Training, members handlers.Members, news handlers.News, foundation handlers.Foundation) http.Handler {
	mux := http.NewServeMux()
	health := handlers.Health{DB: pool}
	system := handlers.System{PageMeta: foundation.PageMeta}

	mux.HandleFunc("GET /health/live", health.Live)
	mux.HandleFunc("GET /health/ready", health.Ready)
	mux.Handle("GET /assets/{path...}", assetHandler())

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		if httpx.UserID(r.Context()) != "" {
			http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
			return
		}
		landing.Get(w, r)
	})

	mux.Handle("GET /login", auth.AnonymousOnly(http.HandlerFunc(login.Get)))
	mux.Handle("POST /login", auth.AnonymousOnly(http.HandlerFunc(login.Post)))
	mux.Handle("GET /registo", auth.AnonymousOnly(http.HandlerFunc(registration.Get)))
	mux.Handle("POST /registo", auth.AnonymousOnly(http.HandlerFunc(registration.Post)))
	mux.Handle("POST /logout", auth.RequireAuthenticated(http.HandlerFunc(auth.Logout)))
	mux.Handle("GET /dashboard", auth.RequireAuthenticated(http.HandlerFunc(auth.Dashboard)))
	mux.Handle("GET /today", auth.RequireAuthenticated(http.HandlerFunc(dashboard.Today)))
	mux.Handle("GET /dashboard/member", auth.RequireAuthenticated(compatibilityRedirect("/today")))
	mux.Handle("GET /dashboard/competitor", auth.RequireProgramme("Competition", "Initiation", "Kayak_Polo")(compatibilityRedirect("/today")))
	mux.Handle("GET /dashboard/initiation", auth.RequireProgramme("Initiation")(http.HandlerFunc(dashboard.Initiation)))
	mux.Handle("GET /dashboard/competition", auth.RequireProgramme("Competition")(http.HandlerFunc(dashboard.Competition)))
	mux.Handle("GET /dashboard/kayak-polo", auth.RequireProgramme("Kayak_Polo")(http.HandlerFunc(dashboard.KayakPolo)))
	mux.Handle("GET /dashboard/leisure", auth.RequireProgramme("Leisure")(http.HandlerFunc(dashboard.Leisure)))
	mux.Handle("GET /dashboard/guardian", auth.RequireGuardian(http.HandlerFunc(dashboard.Guardian)))
	mux.Handle("GET /dashboard/coach", auth.RequireCoach(compatibilityRedirect("/events")))
	mux.Handle("GET /dashboard/moderator", auth.RequireModerator(http.HandlerFunc(dashboard.Moderator)))
	mux.Handle("GET /admin/fleet", auth.RequireAdmin(http.HandlerFunc(dashboard.Admin)))
	mux.Handle("GET /admin/sistema", auth.RequireAdmin(http.HandlerFunc(dashboard.ReleasesPage)))
	mux.Handle("POST /admin/maintenance", auth.RequireAdmin(http.HandlerFunc(dashboard.Maintenance)))
	mux.Handle("POST /admin/repairs/status", auth.RequireAdmin(http.HandlerFunc(dashboard.RepairStatus)))
	mux.Handle("POST /admin/maintenance/{id}/complete", auth.RequireAdmin(http.HandlerFunc(dashboard.CompleteMaintenance)))
	mux.Handle("GET /admin/membros", auth.RequireAdmin(http.HandlerFunc(members.Index)))
	mux.Handle("POST /admin/membros", auth.RequireAdmin(http.HandlerFunc(members.Create)))
	mux.Handle("GET /admin/membros/{id}", auth.RequireAdmin(http.HandlerFunc(members.Detail)))
	mux.Handle("POST /admin/membros/{id}/inscricao", auth.RequireAdmin(http.HandlerFunc(members.Membership)))
	mux.Handle("POST /admin/membros/{id}/desativar", auth.RequireAdmin(http.HandlerFunc(members.Deactivate)))
	mux.Handle("POST /admin/membros/{id}/credencial-menor", auth.RequireAdmin(http.HandlerFunc(members.IssueMinorCredential)))
	mux.Handle("GET /admin/noticias", auth.RequireAdmin(http.HandlerFunc(news.Index)))
	mux.Handle("POST /admin/noticias", auth.RequireAdmin(http.HandlerFunc(news.Create)))
	mux.Handle("POST /admin/noticias/{id}/publicar", auth.RequireAdmin(http.HandlerFunc(news.Publish)))
	mux.Handle("POST /admin/noticias/{id}/expirar", auth.RequireAdmin(http.HandlerFunc(news.Expire)))
	mux.Handle("POST /repairs", auth.RequireAuthenticated(http.HandlerFunc(repair.Post)))
	mux.Handle("POST /guardian/add-dependent", auth.RequireGuardian(http.HandlerFunc(dashboard.AddDependent)))
	mux.Handle("POST /dashboard/guardian/dependents/{id}/leaderboard-privacy", auth.RequireGuardian(http.HandlerFunc(dashboard.DependentLeaderboardPrivacy)))
	mux.Handle("POST /leaderboard/privacy", auth.RequireAuthenticated(http.HandlerFunc(dashboard.LeaderboardPrivacy)))
	mux.Handle("GET /events", auth.RequireAuthenticated(http.HandlerFunc(events.Index)))
	mux.Handle("GET /events/{id}", auth.RequireAuthenticated(http.HandlerFunc(events.Detail)))
	mux.Handle("POST /events/{id}/responses", auth.RequireAuthenticated(http.HandlerFunc(events.Respond)))
	mux.Handle("POST /admin/events", auth.RequireEventStaff(http.HandlerFunc(events.Create)))
	mux.Handle("POST /admin/events/{id}/confirm", auth.RequireEventStaff(http.HandlerFunc(events.Confirm)))
	mux.Handle("POST /admin/events/{id}/check-in", auth.RequireEventStaff(http.HandlerFunc(events.CheckIn)))
	mux.Handle("GET /announcements", auth.RequireAuthenticated(http.HandlerFunc(announcements.Index)))
	mux.Handle("GET /announcements/{id}", auth.RequireAuthenticated(http.HandlerFunc(announcements.Detail)))
	mux.Handle("POST /admin/announcements", auth.RequireEventStaff(http.HandlerFunc(announcements.Create)))
	mux.Handle("POST /admin/announcements/{id}/publish", auth.RequireEventStaff(http.HandlerFunc(announcements.Publish)))
	mux.Handle("POST /admin/announcements/{id}/expire", auth.RequireEventStaff(http.HandlerFunc(announcements.Expire)))
	mux.Handle("GET /treinos", auth.RequireAuthenticated(http.HandlerFunc(training.Index)))
	mux.Handle("POST /treinos/planos", auth.RequireEventStaff(http.HandlerFunc(training.CreatePlan)))
	mux.Handle("POST /treinos/sessoes", auth.RequireEventStaff(http.HandlerFunc(training.CreateSession)))
	mux.Handle("POST /treinos/sessoes/resultados", auth.RequireAuthenticated(http.HandlerFunc(training.ReportOutcome)))
	mux.Handle("POST /treinos/sessoes/distancia", auth.RequireAuthenticated(http.HandlerFunc(training.UpdateDistance)))
	mux.Handle("POST /treinos/documentos", auth.RequireEventStaff(http.HandlerFunc(training.CreateDocument)))

	return customNotFound(mux, system.NotFound)
}

// compatibilityRedirect keeps an approved legacy URL working without weakening
// the capability middleware wrapped around it. Query parameters are retained so
// bookmarked filters and return context are not silently discarded.
func compatibilityRedirect(target string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		location := target
		if r.URL.RawQuery != "" {
			location += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, location, http.StatusSeeOther)
	})
}

func assetHandler() http.Handler {
	dist, err := fs.Sub(staticassets.Files, "dist")
	if err != nil {
		panic(err)
	}
	files := http.FileServerFS(dist)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assetPath := strings.TrimPrefix(r.URL.Path, "/assets/")
		if fingerprintedAsset.MatchString(assetPath) {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		clone := r.Clone(r.Context())
		clonedURL := *r.URL
		clonedURL.Path = "/" + assetPath
		clone.URL = &clonedURL
		files.ServeHTTP(w, clone)
	})
}

func customNotFound(mux *http.ServeMux, notFound http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pattern := mux.Handler(r)
		if pattern != "" || matchesOtherMethod(mux, r) {
			// ServeMux attaches path values such as {id} before invoking a handler.
			mux.ServeHTTP(w, r)
			return
		}
		notFound(w, r)
	})
}

func matchesOtherMethod(mux *http.ServeMux, request *http.Request) bool {
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodHead} {
		if method == request.Method {
			continue
		}
		clone := request.Clone(request.Context())
		clone.Method = method
		_, pattern := mux.Handler(clone)
		if pattern != "" {
			return true
		}
	}
	return false
}
