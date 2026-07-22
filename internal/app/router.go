package app

import (
	"io/fs"
	"net/http"
	"regexp"
	"strings"

	"github.com/alexedwards/scs/v2"
	"github.com/cfcoimbra/mycfc/internal/handlers"
	staticassets "github.com/cfcoimbra/mycfc/ui/static"
)

var fingerprintedAsset = regexp.MustCompile(`-[0-9a-f]{12}\.(?:css|js)$`)

func newRouter(pool handlers.DBPinger, sessions *scs.SessionManager) http.Handler {
	mux := http.NewServeMux()
	health := handlers.Health{DB: pool}
	system := handlers.System{}

	mux.HandleFunc("GET /health/live", health.Live)
	mux.HandleFunc("GET /health/ready", health.Ready)
	mux.Handle("GET /assets/{path...}", assetHandler())

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if sessions.Exists(r.Context(), "user_id") {
			http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	})

	mux.HandleFunc("GET /login", system.NotImplemented)
	mux.HandleFunc("POST /login", system.NotImplemented)
	mux.HandleFunc("GET /registo", system.NotImplemented)
	mux.HandleFunc("POST /registo", system.NotImplemented)
	mux.HandleFunc("POST /logout", system.NotImplemented)
	mux.HandleFunc("GET /dashboard", system.NotImplemented)
	mux.HandleFunc("GET /dashboard/competitor", system.NotImplemented)
	mux.HandleFunc("GET /dashboard/leisure", system.NotImplemented)
	mux.HandleFunc("GET /dashboard/guardian", system.NotImplemented)
	mux.HandleFunc("GET /admin/fleet", system.NotImplemented)
	mux.HandleFunc("POST /admin/maintenance", system.NotImplemented)
	mux.HandleFunc("POST /repairs", system.NotImplemented)
	mux.HandleFunc("POST /guardian/add-dependent", system.NotImplemented)

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
