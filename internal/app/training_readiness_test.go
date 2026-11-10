package app

import (
	"context"
	"errors"
	"github.com/alexedwards/scs/v2"
	"github.com/cfcoimbra/mycfc/internal/config"
	"github.com/cfcoimbra/mycfc/internal/handlers"
	"github.com/cfcoimbra/mycfc/internal/releasecontract"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type trainingProbeFake struct {
	calls int
	err   error
}

func (p *trainingProbeFake) Ping(context.Context) error { p.calls++; return p.err }
func TestTrainingAdmissionGuardFencesBeforeAuthenticationOrSessionAccess(t *testing.T) {
	p := &trainingProbeFake{err: errors.New("blocked external restore fence")}
	reached := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached++; w.WriteHeader(http.StatusOK) })
	guard := trainingAdmissionGuard(p, next)
	for _, path := range []string{"/login", "/static/app.css", "/health/ready", "/admin/treinos/estruturados", "/health/live/"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		guard.ServeHTTP(w, r)
		if w.Code != http.StatusServiceUnavailable || reached != 0 || w.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatalf("%s code%d reached%d", path, w.Code, reached)
		}
	}
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		r := httptest.NewRequest(method, "/health/live", nil)
		w := httptest.NewRecorder()
		before := p.calls
		guard.ServeHTTP(w, r)
		if w.Code != http.StatusOK || p.calls != before {
			t.Fatal("liveness used compatibility database")
		}
	}
	r := httptest.NewRequest(http.MethodPost, "/health/live", nil)
	w := httptest.NewRecorder()
	guard.ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatal("POST bypassed admission")
	}
	p.err = nil
	r = httptest.NewRequest(http.MethodGet, "/login", nil)
	w = httptest.NewRecorder()
	guard.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal("admitted request blocked")
	}
	p.err = errors.New("projection removed")
	r = httptest.NewRequest(http.MethodGet, "/login", nil)
	w = httptest.NewRecorder()
	guard.ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatal("cached successful admission after removal")
	}
}

func TestTrainingAdmissionLocalExceptionRequiresBothEmptyStampsAndKnownLocalEnvironment(t *testing.T) {
	originalVersion, originalCandidate := releasecontract.Version, releasecontract.Candidate
	t.Cleanup(func() { releasecontract.Version, releasecontract.Candidate = originalVersion, originalCandidate })
	for _, tc := range []struct {
		env, version, candidate, path string
		required                      bool
	}{
		{env: "local"}, {env: "test"}, {env: "", required: true}, {env: "development", required: true}, {env: "production", required: true},
		{env: "local", version: "v2.0.9", required: true}, {env: "test", candidate: "abc", required: true}, {env: "local", path: "/missing", required: true},
	} {
		releasecontract.Version, releasecontract.Candidate = tc.version, tc.candidate
		p := newTrainingReadiness(nil, config.Config{AppEnv: tc.env, TrainingScheduleAdmissionFile: tc.path})
		if p.requireAdmission != tc.required {
			t.Fatalf("%+v got required%v", tc, p.requireAdmission)
		}
	}
}

type unavailableLivenessSessionStore struct{ reads int }

func (s *unavailableLivenessSessionStore) Find(string) ([]byte, bool, error) {
	s.reads++
	return nil, false, errors.New("session database stopped")
}
func (s *unavailableLivenessSessionStore) Commit(string, []byte, time.Time) error { return nil }
func (s *unavailableLivenessSessionStore) Delete(string) error                    { return nil }
func TestTrainingLivenessWithCookieBypassesActualSessionAndAuthenticationChain(t *testing.T) {
	store := &unavailableLivenessSessionStore{}
	sessions := scs.New()
	sessions.Store = store
	probe := &trainingProbeFake{err: errors.New("restore fence blocked")}
	server := newHTTPServer(config.Config{AppEnv: "test"}, slog.New(slog.NewTextHandler(io.Discard, nil)), sessions, handlers.System{}, func(next http.Handler) http.Handler { return next }, nil, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("liveness entered authentication/router") }), probe)
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		r := httptest.NewRequest(method, "/health/live", nil)
		r.AddCookie(&http.Cookie{Name: sessions.Cookie.Name, Value: strings.Repeat("a", 43)})
		w := httptest.NewRecorder()
		server.Handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK || store.reads != 0 || probe.calls != 0 {
			t.Fatalf("%s code%d reads%d probes%d", method, w.Code, store.reads, probe.calls)
		}
	}
	r := httptest.NewRequest(http.MethodPost, "/health/live", nil)
	w := httptest.NewRecorder()
	server.Handler.ServeHTTP(w, r)
	if w.Code != 503 || store.reads != 0 {
		t.Fatal("POST liveness bypassed fence or entered session store")
	}
}
