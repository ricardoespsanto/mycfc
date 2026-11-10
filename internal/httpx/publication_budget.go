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
			if publicationPostPath(r) {
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
				r.Body = http.MaxBytesReader(w, r.Body, PublicationConfirmMaxBytes)
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
