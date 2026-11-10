package httpx

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPublicationIngressBoundsBeforeParsingAndIncludesPrework(t *testing.T) {
	path := "/admin/treinos/estruturados/semanas/11111111-1111-4111-8111-111111111111/publicar"
	for _, size := range []int{int(PublicationConfirmMaxBytes), int(PublicationConfirmMaxBytes) + 1} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			handler := PublicationIngressMiddleware(30 * time.Second)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				started, ok := r.Context().Value(publicationRequestStartKey{}).(time.Time)
				if !ok {
					t.Fatal("missing ingress time")
				}
				_, err := io.ReadAll(r.Body)
				if (err != nil) != (size > int(PublicationConfirmMaxBytes)) {
					t.Fatalf("body boundary: %v", err)
				}
				ingressDeadline, hasDeadline := r.Context().Deadline()
				if !hasDeadline || !ingressDeadline.Equal(started.Add(20*time.Second)) {
					t.Fatal("middleware prework has no business deadline")
				}
				deadline, ok := PublicationDeadline(r.Context(), 30*time.Second)
				if !ok || !deadline.Equal(started.Add(20*time.Second)) {
					t.Fatal("business clock restarted")
				}
			}))
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, path, strings.NewReader(strings.Repeat("a", size))))
		})
	}
}

func TestPublicationDeadlineRefusesMissingExhaustedAndClipsTimeout(t *testing.T) {
	if _, ok := PublicationDeadline(context.Background(), 30*time.Second); ok {
		t.Fatal("missing ingress permitted")
	}
	start := time.Now()
	ctx := context.WithValue(context.Background(), publicationRequestStartKey{}, start)
	deadline, ok := PublicationDeadline(ctx, 15*time.Second)
	if !ok || !deadline.Equal(start.Add(5*time.Second)) {
		t.Fatal("configured reserve not preserved")
	}
	parent, cancel := context.WithDeadline(ctx, start.Add(time.Second))
	defer cancel()
	if got, ok := PublicationDeadline(parent, 30*time.Second); !ok || !got.Equal(start.Add(time.Second)) {
		t.Fatal("parent deadline ignored")
	}
	for _, timeout := range []time.Duration{0, 9 * time.Second, 10 * time.Second} {
		if _, ok := PublicationDeadline(ctx, timeout); ok {
			t.Fatal("no response/cleanup reserve")
		}
	}
	old := context.WithValue(context.Background(), publicationRequestStartKey{}, start.Add(-21*time.Second))
	if _, ok := PublicationDeadline(old, 30*time.Second); ok {
		t.Fatal("exhausted prework budget restarted")
	}
}

func TestPublicationIngressLeavesOtherTasksUnchanged(t *testing.T) {
	for _, path := range []string{"/admin/treinos/estruturados/semanas/id/editar", "/admin/treinos/estruturados/semanas/id/extra/publicar", "/admin/treinos"} {
		PublicationIngressMiddleware(30*time.Second)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := r.Context().Value(publicationRequestStartKey{}).(time.Time); ok {
				t.Fatal("other task clock changed")
			}
			if _, err := io.ReadAll(r.Body); err != nil {
				t.Fatal("other task body capped")
			}
		})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, path, strings.NewReader(strings.Repeat("a", int(PublicationConfirmMaxBytes)+1))))
	}
}

func TestPublicationIngressRefusesExhaustedReserveBeforeDispatch(t *testing.T) {
	called := false
	handler := PublicationIngressMiddleware(10 * time.Second)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/admin/treinos/estruturados/semanas/id/publicar", strings.NewReader("safe")))
	if called || w.Code != http.StatusServiceUnavailable || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("publication dispatched without response reserve")
	}
}
