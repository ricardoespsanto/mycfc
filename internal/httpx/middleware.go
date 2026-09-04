package httpx

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type Middleware func(http.Handler) http.Handler

func Chain(handler http.Handler, middleware ...Middleware) http.Handler {
	for i := len(middleware) - 1; i >= 0; i-- {
		handler = middleware[i](handler)
	}
	return handler
}

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{8,128}$`)

func RequestIDMiddleware() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestID := r.Header.Get("X-Request-ID")
			if !requestIDPattern.MatchString(requestID) {
				requestID = newRequestID()
			}
			w.Header().Set("X-Request-ID", requestID)
			next.ServeHTTP(w, r.WithContext(WithRequestID(r.Context(), requestID)))
		})
	}
}

func newRequestID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		// crypto/rand failure means the process cannot safely meet the request-ID
		// contract. Panic so recovery logs and fails the request rather than using
		// a predictable identifier.
		panic(fmt.Errorf("generate request ID: %w", err))
	}
	return hex.EncodeToString(bytes[:])
}

func RecoveryMiddleware(logger *slog.Logger, errorHandlers ...http.Handler) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if recovered := recover(); recovered != nil {
					requestID := RequestID(r.Context())
					if requestID == "" {
						requestID = w.Header().Get("X-Request-ID")
					}
					logger.ErrorContext(r.Context(), "panic recovered",
						"request_id", requestID,
						"panic", fmt.Sprint(recovered),
					)
					if len(errorHandlers) > 0 && errorHandlers[0] != nil {
						errorHandlers[0].ServeHTTP(w, r)
						return
					}
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = io.WriteString(w, "<!doctype html><html lang=\"pt-PT\"><title>Erro interno</title><h1>Não foi possível concluir o pedido</h1><p>Tente novamente mais tarde.</p></html>")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

func TrustedProxyMiddleware(trusted []netip.Prefix) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			peer, ok := parseRemoteAddr(r.RemoteAddr)
			client := peer
			scheme := "http"
			if r.TLS != nil {
				scheme = "https"
			}

			if ok && isTrusted(peer, trusted) {
				client = forwardedClientIP(r.Header.Get("X-Forwarded-For"), peer, trusted)
				if forwardedProto := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0])); forwardedProto == "http" || forwardedProto == "https" {
					scheme = forwardedProto
				}
			}

			ctx := r.Context()
			if client.IsValid() {
				ctx = WithRemoteIP(ctx, client)
			}
			ctx = WithScheme(ctx, scheme)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func parseRemoteAddr(raw string) (netip.Addr, bool) {
	if host, _, err := net.SplitHostPort(raw); err == nil {
		address, parseErr := netip.ParseAddr(host)
		return address.Unmap(), parseErr == nil
	}
	address, err := netip.ParseAddr(raw)
	return address.Unmap(), err == nil
}

func forwardedClientIP(raw string, peer netip.Addr, trusted []netip.Prefix) netip.Addr {
	parts := strings.Split(raw, ",")
	chain := make([]netip.Addr, 0, len(parts)+1)
	for _, part := range parts {
		address, err := netip.ParseAddr(strings.TrimSpace(part))
		if err == nil {
			chain = append(chain, address.Unmap())
		}
	}
	chain = append(chain, peer)
	for i := len(chain) - 1; i >= 0; i-- {
		if !isTrusted(chain[i], trusted) {
			return chain[i]
		}
	}
	if len(chain) > 0 {
		return chain[0]
	}
	return peer
}

func isTrusted(address netip.Addr, trusted []netip.Prefix) bool {
	for _, prefix := range trusted {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func SecurityHeadersMiddleware(production bool, imageOrigins ...string) Middleware {
	imageSources := []string{"'self'", "data:", "blob:"}
	seenImageSources := map[string]bool{"'self'": true, "data:": true, "blob:": true}
	for _, raw := range imageOrigins {
		origin, ok := normalizeCSPOrigin(raw)
		if !ok || seenImageSources[origin] {
			continue
		}
		seenImageSources[origin] = true
		imageSources = append(imageSources, origin)
	}
	imageDirective := strings.Join(imageSources, " ")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.Path, "/health/") {
				w.Header().Set("X-Content-Type-Options", "nosniff")
				w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
				if strings.HasPrefix(r.URL.Path, "/recuperar-palavra-passe") {
					w.Header().Set("Cache-Control", "no-store")
				}
				if strings.HasPrefix(r.URL.Path, "/recuperar-palavra-passe/repor") {
					w.Header().Set("Referrer-Policy", "no-referrer")
				}
				w.Header().Set("X-Frame-Options", "DENY")
				w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
				w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
				csp := "default-src 'self'; base-uri 'self'; object-src 'none'; frame-ancestors 'none'; form-action 'self'; img-src " + imageDirective + "; style-src 'self'; script-src 'self' https://challenges.cloudflare.com; frame-src https://challenges.cloudflare.com; connect-src 'self' https://www.googleapis.com; font-src 'self'"
				if production {
					w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
					csp += "; upgrade-insecure-requests"
				}
				w.Header().Set("Content-Security-Policy", csp)
			}
			next.ServeHTTP(w, r)
		})
	}
}

func normalizeCSPOrigin(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", false
	}
	return (&url.URL{Scheme: u.Scheme, Host: u.Host}).String(), true
}

func AccessLogMiddleware(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started := time.Now()
			recorder := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(recorder, r)
			remoteIP := ""
			if address, ok := RemoteIP(r.Context()); ok {
				remoteIP = address.String()
			}
			logger.InfoContext(r.Context(), "http request",
				"method", r.Method,
				"path", r.URL.Path,
				"route", r.Pattern,
				"status", recorder.status,
				"bytes", recorder.bytes,
				"duration_ms", time.Since(started).Milliseconds(),
				"request_id", RequestID(r.Context()),
				"remote_ip", remoteIP,
				"user_id", UserID(r.Context()),
			)
		})
	}
}

type statusWriter struct {
	http.ResponseWriter
	status      int
	bytes       int64
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(body []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	count, err := w.ResponseWriter.Write(body)
	w.bytes += int64(count)
	return count, err
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) ReadFrom(reader io.Reader) (int64, error) {
	if readerFrom, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		count, err := readerFrom.ReadFrom(reader)
		w.bytes += count
		return count, err
	}
	return io.Copy(struct{ io.Writer }{w}, reader)
}

func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("response writer does not support hijacking")
	}
	return hijacker.Hijack()
}
