package httpx

import (
	"context"
	"net/http"
	"strings"
	"time"
)

type publicationRequestStartKey struct{}

const PublicationConfirmMaxBytes int64 = 16 << 10

// PublicationIngressMiddleware records the business clock before admission,
// sessions, CSRF parsing and handler prework. The bounded task body must be
// installed here too, before CSRF can parse the general application body limit.
func PublicationIngressMiddleware(writeTimeout time.Duration) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if publicationPostPath(r) || focusedWorkoutPostPath(r) {
				limit := PublicationConfirmMaxBytes
				if focusedWorkoutPostPath(r) {
					limit = 1 << 20
				}
				started := time.Now()
				business := min(20*time.Second, writeTimeout-10*time.Second)
				if business <= 0 {
					w.Header().Set("Cache-Control", "no-store")
					w.Header().Set("Referrer-Policy", "no-referrer")
					http.Error(w, "indisponível", http.StatusServiceUnavailable)
					return
				}
				ctx := context.WithValue(r.Context(), publicationRequestStartKey{}, started)
				ctx, cancel := context.WithDeadline(ctx, started.Add(business))
				defer cancel()
				r = r.WithContext(ctx)
				r.Body = http.MaxBytesReader(w, r.Body, limit)
			}
			next.ServeHTTP(w, r)
		})
	}
}

func publicationPostPath(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	const prefix = "/admin/treinos/estruturados/semanas/"
	const suffix = "/publicar"
	if !strings.HasPrefix(r.URL.Path, prefix) || !strings.HasSuffix(r.URL.Path, suffix) {
		return false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix), suffix)
	return id != "" && !strings.Contains(id, "/")
}

// PublicationDeadline never restarts the clock at the store boundary. Missing
// ingress metadata is refused; direct handler tests must use the real wrapper.
func PublicationDeadline(ctx context.Context, writeTimeout time.Duration) (time.Time, bool) {
	started, ok := ctx.Value(publicationRequestStartKey{}).(time.Time)
	business := min(20*time.Second, writeTimeout-10*time.Second)
	if !ok || business <= 0 {
		return time.Time{}, false
	}
	deadline := started.Add(business)
	if parent, ok := ctx.Deadline(); ok && parent.Before(deadline) {
		deadline = parent
	}
	return deadline, time.Now().Before(deadline)
}

func focusedWorkoutPostPath(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	const reuse = "/admin/treinos/estruturados/reutilizar/"
	if strings.HasPrefix(r.URL.Path, reuse) {
		task := strings.TrimPrefix(r.URL.Path, reuse)
		return task == "rascunho" || task == "prever" || task == "confirmar"
	}
	const prefix = "/admin/treinos/estruturados/semanas/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, prefix), "/")
	if len(parts) < 4 || parts[0] == "" || parts[1] != "treinos" {
		return false
	}
	if len(parts) == 4 && parts[2] == "criar" {
		return parts[3] == "rascunho" || parts[3] == "prever" || parts[3] == "confirmar"
	}
	if parts[2] == "" {
		return false
	}
	if len(parts) == 4 {
		return parts[3] == "cancelar"
	}
	return len(parts) == 5 && parts[3] == "editar" && (parts[4] == "rascunho" || parts[4] == "prever" || parts[4] == "confirmar")
}
