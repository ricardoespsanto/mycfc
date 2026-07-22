package httpx

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestRequestIDMiddlewareAcceptsSafeInboundValue(t *testing.T) {
	handler := RequestIDMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := RequestID(r.Context()); got != "request-1234" {
			t.Fatalf("request ID in context = %q", got)
		}
	}))

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("X-Request-ID", "request-1234")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if got := response.Header().Get("X-Request-ID"); got != "request-1234" {
		t.Fatalf("response request ID = %q", got)
	}
}

func TestRequestIDMiddlewareReplacesUnsafeValue(t *testing.T) {
	handler := RequestIDMiddleware()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("X-Request-ID", "bad value with spaces")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if got := response.Header().Get("X-Request-ID"); len(got) != 32 || strings.Contains(got, " ") {
		t.Fatalf("generated request ID = %q", got)
	}
}

func TestTrustedProxyIgnoresSpoofedHeadersFromUntrustedPeer(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	handler := TrustedProxyMiddleware(trusted)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		ip, ok := RemoteIP(r.Context())
		if !ok || ip.String() != "203.0.113.10" {
			t.Fatalf("remote IP = %v, %v", ip, ok)
		}
		if got := Scheme(r.Context()); got != "http" {
			t.Fatalf("scheme = %q", got)
		}
	}))

	request := httptest.NewRequest(http.MethodGet, "http://example.test", nil)
	request.RemoteAddr = "203.0.113.10:42310"
	request.Header.Set("X-Forwarded-For", "198.51.100.99")
	request.Header.Set("X-Forwarded-Proto", "https")
	handler.ServeHTTP(httptest.NewRecorder(), request)
}

func TestTrustedProxyUsesRightmostUntrustedForwardedAddress(t *testing.T) {
	trusted := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("192.0.2.0/24"),
	}
	handler := TrustedProxyMiddleware(trusted)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		ip, _ := RemoteIP(r.Context())
		if ip.String() != "198.51.100.44" {
			t.Fatalf("remote IP = %v", ip)
		}
		if got := Scheme(r.Context()); got != "https" {
			t.Fatalf("scheme = %q", got)
		}
	}))

	request := httptest.NewRequest(http.MethodGet, "http://example.test", nil)
	request.RemoteAddr = "10.0.1.20:443"
	request.Header.Set("X-Forwarded-For", "203.0.113.7, 198.51.100.44, 192.0.2.40")
	request.Header.Set("X-Forwarded-Proto", "https")
	handler.ServeHTTP(httptest.NewRecorder(), request)
}

func TestSecurityHeaders(t *testing.T) {
	handler := SecurityHeadersMiddleware(true)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/login", nil))

	for _, header := range []string{"Strict-Transport-Security", "Content-Security-Policy", "X-Content-Type-Options", "Permissions-Policy"} {
		if response.Header().Get(header) == "" {
			t.Errorf("missing %s", header)
		}
	}
	if !strings.Contains(response.Header().Get("Content-Security-Policy"), "upgrade-insecure-requests") {
		t.Error("production CSP does not upgrade insecure requests")
	}
}

func TestRecoveryMiddleware(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	handler := RecoveryMiddleware(logger)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(WithRequestID(context.Background(), "request-1234"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), "Não foi possível") {
		t.Fatalf("body = %q", response.Body.String())
	}
	if !strings.Contains(logs.String(), "request-1234") {
		t.Fatalf("log = %q", logs.String())
	}
}

func TestMiddlewareOrder(t *testing.T) {
	var order []string
	wrap := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name+":before")
				next.ServeHTTP(w, r)
				order = append(order, name+":after")
			})
		}
	}
	handler := Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		order = append(order, "handler")
	}), wrap("outer"), wrap("inner"))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	got := strings.Join(order, ",")
	want := "outer:before,inner:before,handler,inner:after,outer:after"
	if got != want {
		t.Fatalf("order = %q, want %q", got, want)
	}
}
