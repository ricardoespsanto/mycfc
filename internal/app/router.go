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

var fingerprintedAsset = regexp.MustCompile(`-[0-9a-f]{12}\.(?:css|js)$`)

func newRouter(pool handlers.DBPinger, sessions *scs.SessionManager, login handlers.Login, registration handlers.Registration, auth handlers.Auth, dashboard handlers.Dashboard, repair handlers.Repair) http.Handler {
	mux := http.NewServeMux()
	health := handlers.Health{DB: pool}
	system := handlers.System{}

	mux.HandleFunc("GET /health/live", health.Live)
	mux.HandleFunc("GET /health/ready", health.Ready)
	mux.Handle("GET /assets/{path...}", assetHandler())

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		if httpx.UserID(r.Context()) != "" {
			http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	})

	mux.Handle("GET /login", auth.AnonymousOnly(http.HandlerFunc(login.Get)))
	mux.Handle("POST /login", auth.AnonymousOnly(http.HandlerFunc(login.Post)))
	mux.Handle("GET /registo", auth.AnonymousOnly(http.HandlerFunc(registration.Get)))
	mux.Handle("POST /registo", auth.AnonymousOnly(http.HandlerFunc(registration.Post)))
	mux.Handle("POST /logout", auth.RequireRole("Admin", "Competitor", "Leisure", "Guardian")(http.HandlerFunc(auth.Logout)))
	mux.Handle("GET /dashboard", auth.RequireRole("Admin", "Competitor", "Leisure", "Guardian")(http.HandlerFunc(auth.Dashboard)))
	mux.Handle("GET /dashboard/competitor", auth.RequireRole("Competitor")(http.HandlerFunc(dashboard.Competitor)))
	mux.Handle("GET /dashboard/leisure", auth.RequireRole("Leisure")(http.HandlerFunc(dashboard.Leisure)))
	mux.Handle("GET /dashboard/guardian", auth.RequireRole("Guardian")(http.HandlerFunc(dashboard.Guardian)))
	mux.Handle("GET /admin/fleet", auth.RequireRole("Admin")(http.HandlerFunc(dashboard.Admin)))
	mux.Handle("POST /admin/maintenance", auth.RequireRole("Admin")(http.HandlerFunc(system.NotImplemented)))
	mux.Handle("POST /repairs", auth.RequireRole("Admin", "Competitor", "Leisure", "Guardian")(http.HandlerFunc(repair.Post)))
	mux.Handle("POST /guardian/add-dependent", auth.RequireRole("Guardian")(http.HandlerFunc(dashboard.AddDependent)))

	return customNotFound(mux, system.NotFound)
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
		handler, pattern := mux.Handler(r)
		if pattern != "" || matchesOtherMethod(mux, r) {
			handler.ServeHTTP(w, r)
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
