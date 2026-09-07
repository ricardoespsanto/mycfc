package handlers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ"
	"github.com/alexedwards/scs/v2"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/polar"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type polarServiceFake struct {
	connected     int
	disconnected  int
	userID        uuid.UUID
	credentials   polar.Credentials
	memberID      string
	connection    PolarConnection
	connectionErr error
}

func (s *polarServiceFake) Connection(context.Context, uuid.UUID) (PolarConnection, error) {
	return s.connection, s.connectionErr
}
func (s *polarServiceFake) Connect(_ context.Context, userID uuid.UUID, credentials polar.Credentials, memberID string, _ int64) error {
	s.connected++
	s.userID = userID
	s.credentials = credentials
	s.memberID = memberID
	return nil
}
func (*polarServiceFake) Sync(context.Context, uuid.UUID) error         { return nil }
func (s *polarServiceFake) Disconnect(context.Context, uuid.UUID) error { s.disconnected++; return nil }

func TestPolarPagesBuildRequestCSRFFields(t *testing.T) {
	client, err := polar.NewClient(polar.Config{ClientID: "client", ClientSecret: "secret", RedirectURL: "https://mycfcoimbra.com/oauth/polar/callback"})
	if err != nil {
		t.Fatal(err)
	}
	sessions := scs.New()
	handler := Integrations{
		Service:  &polarServiceFake{connectionErr: pgx.ErrNoRows},
		Client:   client,
		Sessions: sessions,
	}
	user := CurrentUser{ID: uuid.New(), Programmes: map[string]bool{"Competition": true}}

	indexResponse := httptest.NewRecorder()
	servePolarTest(sessions, user, handler.Index).ServeHTTP(indexResponse, httptest.NewRequest(http.MethodGet, "/perfil/integracoes", nil))
	if indexResponse.Code != http.StatusOK || !strings.Contains(indexResponse.Body.String(), ">Ligar Polar</button>") || strings.Count(indexResponse.Body.String(), "<!doctype html>") != 1 {
		t.Fatalf("integration page = %d %q", indexResponse.Code, indexResponse.Body.String())
	}

	disconnectResponse := httptest.NewRecorder()
	servePolarTest(sessions, user, handler.DisconnectPage).ServeHTTP(disconnectResponse, httptest.NewRequest(http.MethodGet, "/perfil/integracoes/polar/desligar", nil))
	if disconnectResponse.Code != http.StatusOK || !strings.Contains(disconnectResponse.Body.String(), ">Desligar Polar</button>") || strings.Count(disconnectResponse.Body.String(), "<!doctype html>") != 1 {
		t.Fatalf("disconnect page = %d %q", disconnectResponse.Code, disconnectResponse.Body.String())
	}
}

func TestPolarOAuthStateIsUserBoundSingleUseAndCallbackIsNoStore(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	tokenCalls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenCalls++
		io.WriteString(w, `{"access_token":"token","token_type":"bearer","expires_in":3600,"x_user_id":42}`)
	}))
	defer provider.Close()
	client, err := polar.NewClient(polar.Config{ClientID: "client", ClientSecret: "secret", RedirectURL: "https://mycfcoimbra.com/oauth/polar/callback", AuthorizationURL: provider.URL, TokenURL: provider.URL, APIBaseURL: provider.URL, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	sessions := scs.New()
	service := &polarServiceFake{}
	handler := Integrations{Service: service, Client: client, Sessions: sessions, PageMeta: components.PageMeta{CSRFField: templ.NopComponent}, Now: func() time.Time { return now }}
	user := CurrentUser{ID: uuid.New(), Programmes: map[string]bool{"Competition": true}}

	connectRequest := httptest.NewRequest(http.MethodPost, "/perfil/integracoes/polar/ligar", nil)
	connectResponse := httptest.NewRecorder()
	servePolarTest(sessions, user, handler.Connect).ServeHTTP(connectResponse, connectRequest)
	if connectResponse.Code != http.StatusSeeOther {
		t.Fatalf("connect status = %d", connectResponse.Code)
	}
	authorization, _ := url.Parse(connectResponse.Header().Get("Location"))
	state := authorization.Query().Get("state")
	if len(state) < 32 {
		t.Fatalf("state = %q", state)
	}
	cookie := connectResponse.Result().Cookies()[0]

	callbackRequest := httptest.NewRequest(http.MethodGet, "/oauth/polar/callback?state="+url.QueryEscape(state)+"&code=one-time", nil)
	callbackRequest.AddCookie(cookie)
	callbackResponse := httptest.NewRecorder()
	servePolarTest(sessions, user, handler.Callback).ServeHTTP(callbackResponse, callbackRequest)
	if callbackResponse.Code != http.StatusSeeOther || callbackResponse.Header().Get("Location") != "/perfil/integracoes" {
		t.Fatalf("callback = %d %q", callbackResponse.Code, callbackResponse.Header().Get("Location"))
	}
	if callbackResponse.Header().Get("Cache-Control") != "no-store" || callbackResponse.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("callback privacy headers = %v", callbackResponse.Header())
	}
	if service.connected != 1 || service.userID != user.ID || service.credentials.RemoteUserID != 42 || !strings.HasPrefix(service.memberID, "mycfc-") {
		t.Fatalf("service = %#v", service)
	}

	replayCookie := cookie
	if cookies := callbackResponse.Result().Cookies(); len(cookies) > 0 {
		replayCookie = cookies[0]
	}
	replayRequest := httptest.NewRequest(http.MethodGet, "/oauth/polar/callback?state="+url.QueryEscape(state)+"&code=replay", nil)
	replayRequest.AddCookie(replayCookie)
	replayResponse := httptest.NewRecorder()
	servePolarTest(sessions, user, handler.Callback).ServeHTTP(replayResponse, replayRequest)
	if service.connected != 1 || tokenCalls != 1 {
		t.Fatalf("replay reached provider/service: token=%d connect=%d", tokenCalls, service.connected)
	}
}

func TestPolarOAuthTamperingConsumesStateBeforeExchange(t *testing.T) {
	providerCalls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { providerCalls++ }))
	defer provider.Close()
	client, _ := polar.NewClient(polar.Config{ClientID: "client", ClientSecret: "secret", RedirectURL: "https://mycfcoimbra.com/oauth/polar/callback", AuthorizationURL: provider.URL, TokenURL: provider.URL, APIBaseURL: provider.URL})
	sessions := scs.New()
	service := &polarServiceFake{}
	handler := Integrations{Service: service, Client: client, Sessions: sessions, PageMeta: components.PageMeta{CSRFField: templ.NopComponent}}
	user := CurrentUser{ID: uuid.New(), Programmes: map[string]bool{"Competition": true}}
	response := httptest.NewRecorder()
	servePolarTest(sessions, user, handler.Connect).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/perfil/integracoes/polar/ligar", nil))
	cookie := response.Result().Cookies()[0]
	request := httptest.NewRequest(http.MethodGet, "/oauth/polar/callback?state=tampered&code=secret-code", nil)
	request.AddCookie(cookie)
	result := httptest.NewRecorder()
	servePolarTest(sessions, user, handler.Callback).ServeHTTP(result, request)
	if providerCalls != 0 || service.connected != 0 {
		t.Fatal("tampered state reached provider")
	}
}

func TestPolarOAuthStartedBeforeConnectionVersionChangeIsRejected(t *testing.T) {
	providerCalls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { providerCalls++ }))
	defer provider.Close()
	client, _ := polar.NewClient(polar.Config{ClientID: "client", ClientSecret: "secret", RedirectURL: "https://mycfcoimbra.com/oauth/polar/callback", AuthorizationURL: provider.URL, TokenURL: provider.URL, APIBaseURL: provider.URL})
	sessions := scs.New()
	service := &polarServiceFake{connection: PolarConnection{Version: 1, Status: "ACTIVE"}}
	handler := Integrations{Service: service, Client: client, Sessions: sessions, PageMeta: components.PageMeta{CSRFField: templ.NopComponent}}
	user := CurrentUser{ID: uuid.New(), Programmes: map[string]bool{"Competition": true}}
	response := httptest.NewRecorder()
	servePolarTest(sessions, user, handler.Connect).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/perfil/integracoes/polar/ligar", nil))
	authorization, _ := url.Parse(response.Header().Get("Location"))
	state := authorization.Query().Get("state")
	cookie := response.Result().Cookies()[0]
	service.connection.Version = 2
	request := httptest.NewRequest(http.MethodGet, "/oauth/polar/callback?state="+url.QueryEscape(state)+"&code=stale", nil)
	request.AddCookie(cookie)
	result := httptest.NewRecorder()
	servePolarTest(sessions, user, handler.Callback).ServeHTTP(result, request)
	if providerCalls != 0 || service.connected != 0 {
		t.Fatal("stale callback reached provider after disconnect/reconnect version change")
	}
}

func TestFormerAthleteCanSeeAndDisconnectOwnedPolarConnection(t *testing.T) {
	sessions := scs.New()
	service := &polarServiceFake{connection: PolarConnection{Version: 2, Status: "ACTIVE"}}
	client, _ := polar.NewClient(polar.Config{ClientID: "client", ClientSecret: "secret", RedirectURL: "https://mycfcoimbra.com/oauth/polar/callback"})
	handler := Integrations{Service: service, Client: client, Sessions: sessions, PageMeta: components.PageMeta{CSRFField: templ.NopComponent}}
	formerAthlete := CurrentUser{ID: uuid.New(), Programmes: map[string]bool{}}
	response := httptest.NewRecorder()
	servePolarTest(sessions, formerAthlete, handler.Index).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/perfil/integracoes", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "ainda pode desligar") || strings.Contains(response.Body.String(), "Sincronizar agora") {
		t.Fatalf("former athlete page = %d %q", response.Code, response.Body.String())
	}
	pageResponse := httptest.NewRecorder()
	servePolarTest(sessions, formerAthlete, handler.DisconnectPage).ServeHTTP(pageResponse, httptest.NewRequest(http.MethodGet, "/perfil/integracoes/polar/desligar", nil))
	if pageResponse.Code != http.StatusOK {
		t.Fatalf("disconnect page status = %d", pageResponse.Code)
	}
}

func TestDisconnectedConnectionCannotChangePolarIdentity(t *testing.T) {
	connection := dbgen.ActivityConnection{Status: "DISCONNECTED", ProviderUserID: "old-polar-id"}
	if polarConnectionAcceptsIdentity(connection, "new-polar-id") {
		t.Fatal("disconnected connection accepted a different retained-evidence identity")
	}
	if !polarConnectionAcceptsIdentity(connection, "old-polar-id") {
		t.Fatal("same Polar identity was rejected")
	}
}

func servePolarTest(sessions *scs.SessionManager, user CurrentUser, handler http.HandlerFunc) http.Handler {
	return sessions.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), currentUserKey{}, user)
		handler(w, r.WithContext(ctx))
	}))
}
