package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/cfcoimbra/mycfc/internal/activity"
	"github.com/cfcoimbra/mycfc/internal/activity/polar"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestPolarHandlersCoverUnavailableAndConnectionActions(t *testing.T) {
	user := CurrentUser{ID: uuid.New(), Name: "Atleta"}
	sessions := scs.New()
	disabled := PolarIntegration{Sessions: sessions}
	begin := httptest.NewRecorder()
	withPolarUser(sessions, user, http.HandlerFunc(disabled.Begin)).ServeHTTP(begin, httptest.NewRequest(http.MethodPost, "/start", nil))
	if begin.Code != http.StatusOK {
		t.Fatalf("disabled begin=%d", begin.Code)
	}

	for _, tc := range []struct {
		name   string
		store  *polarStoreFake
		action http.HandlerFunc
	}{
		{"sync absent", &polarStoreFake{}, nil},
		{"sync failure", &polarStoreFake{getErr: errors.New("database")}, nil},
		{"disconnect absent", &polarStoreFake{}, nil},
		{"disconnect failure", &polarStoreFake{connection: dbgen.ActivityConnection{ID: uuid.New(), UserID: user.ID}, disconnectErr: errors.New("database")}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := PolarIntegration{Store: tc.store, Sessions: sessions}
			if strings.HasPrefix(tc.name, "sync") {
				tc.action = h.Sync
			} else {
				tc.action = h.Disconnect
			}
			response := httptest.NewRecorder()
			withPolarUser(sessions, user, tc.action).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", nil))
			if response.Code != http.StatusSeeOther && response.Code != http.StatusInternalServerError {
				t.Fatalf("status=%d", response.Code)
			}
		})
	}
	store := &polarStoreFake{connection: dbgen.ActivityConnection{ID: uuid.New(), UserID: user.ID}}
	h := PolarIntegration{Store: store, Sessions: sessions, Now: func() time.Time { return time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC) }}
	response := httptest.NewRecorder()
	withPolarUser(sessions, user, http.HandlerFunc(h.Disconnect)).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", nil))
	if response.Code != http.StatusSeeOther || !store.disconnected {
		t.Fatalf("disconnect=%d did=%t", response.Code, store.disconnected)
	}
}

func TestPolarStartRecentSyncAndHelpers(t *testing.T) {
	user := uuid.New()
	h := PolarIntegration{}
	h.StartRecentSync(context.Background(), user)
	h = PolarIntegration{Store: &polarStoreFake{}, Client: polar.Client{ClientID: "id", ClientSecret: "secret", RedirectURL: "https://club.example/callback"}, Vault: nil}
	h.StartRecentSync(context.Background(), user)
	if polarErrorCode(polar.ErrUnauthorized) != "UNAUTHORIZED" || polarErrorCode(polar.ErrRateLimited) != "RATE_LIMITED" || polarErrorCode(errors.New("remote")) != "REMOTE_ERROR" {
		t.Fatal("codes")
	}
	if timeValue(nil).Valid {
		t.Fatal("nil time is valid")
	}
	now := time.Now()
	if !timeValue(&now).Valid {
		t.Fatal("time not valid")
	}
	if (PolarIntegration{}).now().IsZero() {
		t.Fatal("clock is zero")
	}
}

func TestPolarCallbackAndSyncAdditionalFailures(t *testing.T) {
	user := CurrentUser{ID: uuid.New()}
	sessions := scs.New()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	defer provider.Close()
	h := PolarIntegration{Store: &polarStoreFake{}, Vault: nil, Client: polar.Client{ClientID: "id", ClientSecret: "secret", RedirectURL: "https://club.example/callback", TokenURL: provider.URL, HTTPClient: provider.Client()}, Sessions: sessions}
	start := httptest.NewRecorder()
	wrapped := withPolarUser(sessions, user, http.HandlerFunc(h.Begin))
	wrapped.ServeHTTP(start, httptest.NewRequest(http.MethodPost, "/start", nil))
	state, _ := url.Parse(start.Header().Get("Location"))
	callback := httptest.NewRequest(http.MethodGet, "/callback?code=code&state="+state.Query().Get("state"), nil)
	callback.AddCookie(start.Result().Cookies()[0])
	response := httptest.NewRecorder()
	withPolarUser(sessions, user, http.HandlerFunc(h.Callback)).ServeHTTP(response, callback)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("callback=%d", response.Code)
	}

	vault, _ := activity.NewAESGCMVault(make([]byte, 32), "key")
	sealed, _ := vault.Seal(context.Background(), polar.Provider, user.ID.String(), polar.Credential(polar.Token{AccessToken: "token", UserID: "42"}))
	key := sealed.KeyID
	store := &polarStoreFake{connection: dbgen.ActivityConnection{ID: uuid.New(), UserID: user.ID, Status: "ACTIVE", CredentialsCiphertext: sealed.Ciphertext, CredentialKeyID: &key}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			if r.URL.Path == "/users" {
				w.WriteHeader(http.StatusConflict)
			} else {
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"transaction-id":"tx"}`))
			}
		case http.MethodGet:
			_, _ = w.Write([]byte(`{"exercises":[]}`))
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()
	h = PolarIntegration{Store: store, Vault: vault, Client: polar.Client{ClientID: "id", ClientSecret: "secret", RedirectURL: "https://club.example/callback", APIURL: server.URL, HTTPClient: server.Client()}, Now: func() time.Time { return time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC) }}
	h.StartRecentSync(context.Background(), user.ID)
	if !store.succeeded {
		t.Fatal("recent sync did not persist success")
	}
	store.succeeded = false
	last := pgtype.Timestamptz{Time: time.Date(2026, 9, 22, 11, 59, 0, 0, time.UTC), Valid: true}
	store.connection.LastSuccessfulSyncAt = last
	h.StartRecentSync(context.Background(), user.ID)
	if store.succeeded {
		t.Fatal("cooldown did not suppress sync")
	}
}

func TestPolarIndexShowsConnectionStateAndCredentialFailure(t *testing.T) {
	user := CurrentUser{ID: uuid.New(), Name: "Atleta"}
	message := "provider error"
	key := "key"
	store := &polarStoreFake{connection: dbgen.ActivityConnection{ID: uuid.New(), UserID: user.ID, Status: "REAUTHORIZATION_REQUIRED", CredentialsCiphertext: []byte("invalid"), CredentialKeyID: &key, LastErrorMessage: &message}}
	vault, _ := activity.NewAESGCMVault(make([]byte, 32), key)
	h := PolarIntegration{Store: store, Vault: vault, Client: polar.Client{ClientID: "id", ClientSecret: "secret", RedirectURL: "https://club.example/callback"}, Sessions: scs.New(), PageMeta: components.PageMeta{}}
	response := httptest.NewRecorder()
	withPolarUser(h.Sessions, user, http.HandlerFunc(h.Index)).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/perfil/integracoes/polar", nil))
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "private, no-store" || !strings.Contains(response.Body.String(), message) {
		t.Fatalf("index=%d body=%s", response.Code, response.Body.String())
	}
	h.sync(context.Background(), user.ID, store.connection)
	if store.errored == nil || !store.errored.RequiresReauthorization {
		t.Fatalf("sync error=%#v", store.errored)
	}
}

type polarStoreFake struct {
	connection    dbgen.ActivityConnection
	getErr        error
	upsertErr     error
	disconnectErr error
	upserted      dbgen.UpsertActivityConnectionParams
	activities    []dbgen.UpsertSyncedActivityParams
	succeeded     bool
	errored       *dbgen.RecordActivityConnectionErrorParams
	disconnected  bool
}

func (s *polarStoreFake) GetActivityConnectionForUser(context.Context, dbgen.GetActivityConnectionForUserParams) (dbgen.ActivityConnection, error) {
	if s.getErr != nil {
		return dbgen.ActivityConnection{}, s.getErr
	}
	if s.connection.ID == uuid.Nil {
		return dbgen.ActivityConnection{}, pgx.ErrNoRows
	}
	return s.connection, nil
}
func (s *polarStoreFake) UpsertActivityConnection(_ context.Context, p dbgen.UpsertActivityConnectionParams) (dbgen.ActivityConnection, error) {
	if s.upsertErr != nil {
		return dbgen.ActivityConnection{}, s.upsertErr
	}
	s.upserted = p
	s.connection = dbgen.ActivityConnection{ID: uuid.New(), UserID: p.UserID, Provider: p.Provider, ProviderUserID: p.ProviderUserID, Status: "ACTIVE", CredentialsCiphertext: p.CredentialsCiphertext, CredentialKeyID: p.CredentialKeyID, Scopes: p.Scopes}
	return s.connection, nil
}
func (s *polarStoreFake) UpsertSyncedActivity(_ context.Context, p dbgen.UpsertSyncedActivityParams) (dbgen.SyncedActivity, error) {
	s.activities = append(s.activities, p)
	return dbgen.SyncedActivity{ID: uuid.New()}, nil
}
func (s *polarStoreFake) RecordActivityConnectionSyncSuccess(context.Context, dbgen.RecordActivityConnectionSyncSuccessParams) (dbgen.ActivityConnection, error) {
	s.succeeded = true
	return s.connection, nil
}
func (s *polarStoreFake) RecordActivityConnectionError(_ context.Context, p dbgen.RecordActivityConnectionErrorParams) (dbgen.ActivityConnection, error) {
	s.errored = &p
	return s.connection, nil
}
func (s *polarStoreFake) DisconnectActivityConnection(context.Context, dbgen.DisconnectActivityConnectionParams) (dbgen.DisconnectActivityConnectionRow, error) {
	if s.disconnectErr != nil {
		return dbgen.DisconnectActivityConnectionRow{}, s.disconnectErr
	}
	s.disconnected = true
	return dbgen.DisconnectActivityConnectionRow{}, nil
}

func TestPolarCallbackConsumesStateStoresEncryptedCredentialsAndSyncs(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_, _ = w.Write([]byte(`{"access_token":"access-token","x_user_id":"polar-42"}`))
		case "/v3/users":
			w.WriteHeader(http.StatusConflict)
		case "/v3/users/polar-42/exercise-transactions":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"transaction-id":"tx"}`))
		case "/v3/users/polar-42/exercise-transactions/tx":
			if r.Method == http.MethodDelete {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			_, _ = w.Write([]byte(`{"exercises":[{"id":"exercise-1","start_time":"2026-09-22T08:00:00Z","duration":"PT30M","sport":"Kayaking"}]}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer provider.Close()
	vault, err := activity.NewAESGCMVault(make([]byte, 32), "activity-v1")
	if err != nil {
		t.Fatal(err)
	}
	store := &polarStoreFake{}
	h := PolarIntegration{Store: store, Vault: vault, Client: polar.Client{ClientID: "client", ClientSecret: "secret", RedirectURL: "https://mycfcoimbra.com/oauth/polar/callback", AuthorizationURL: provider.URL + "/authorize", TokenURL: provider.URL + "/token", APIURL: provider.URL + "/v3", HTTPClient: provider.Client()}, Sessions: scs.New(), Now: func() time.Time { return time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC) }}
	user := CurrentUser{ID: uuid.New(), Name: "Atleta"}
	handler := withPolarUser(h.Sessions, user, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			h.Begin(w, r)
		} else {
			h.Callback(w, r)
		}
	}))
	start := httptest.NewRecorder()
	handler.ServeHTTP(start, httptest.NewRequest(http.MethodPost, "/start", nil))
	if start.Code != http.StatusSeeOther {
		t.Fatalf("start=%d", start.Code)
	}
	location, err := url.Parse(start.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	state := location.Query().Get("state")
	if state == "" {
		t.Fatal("missing state")
	}
	cookie := start.Result().Cookies()[0]
	callback := httptest.NewRequest(http.MethodGet, "/callback?code=code&state="+url.QueryEscape(state), nil)
	callback.AddCookie(cookie)
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, callback)
	if result.Code != http.StatusSeeOther || store.upserted.ProviderUserID != "polar-42" || len(store.upserted.CredentialsCiphertext) == 0 || strings.Contains(string(store.upserted.CredentialsCiphertext), "access-token") || len(store.activities) != 1 || !store.succeeded {
		t.Fatalf("callback=%d connection=%#v activities=%d success=%t", result.Code, store.upserted, len(store.activities), store.succeeded)
	}
	replay := httptest.NewRequest(http.MethodGet, "/callback?code=code&state="+url.QueryEscape(state), nil)
	replay.AddCookie(result.Result().Cookies()[0])
	replayResult := httptest.NewRecorder()
	handler.ServeHTTP(replayResult, replay)
	if replayResult.Code != http.StatusSeeOther || len(store.activities) != 1 {
		t.Fatalf("state replay succeeded: status=%d activities=%d", replayResult.Code, len(store.activities))
	}
}
func TestPolarSyncMarksReauthorizationWithoutInterruptingMember(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer provider.Close()
	vault, _ := activity.NewAESGCMVault(make([]byte, 32), "activity-v1")
	user := uuid.New()
	sealed, _ := vault.Seal(context.Background(), polar.Provider, user.String(), polar.Credential(polar.Token{AccessToken: "token", UserID: "polar-42"}))
	key := sealed.KeyID
	store := &polarStoreFake{connection: dbgen.ActivityConnection{ID: uuid.New(), UserID: user, Provider: "polar", Status: "ACTIVE", CredentialsCiphertext: sealed.Ciphertext, CredentialKeyID: &key}}
	h := PolarIntegration{Store: store, Vault: vault, Client: polar.Client{ClientID: "client", ClientSecret: "secret", RedirectURL: "https://mycfcoimbra.com/oauth/polar/callback", APIURL: provider.URL, HTTPClient: provider.Client()}, Sessions: scs.New()}
	request := httptest.NewRequest(http.MethodPost, "/perfil/integracoes/polar/sincronizar", nil)
	response := httptest.NewRecorder()
	withPolarUser(h.Sessions, CurrentUser{ID: user}, http.HandlerFunc(h.Sync)).ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || store.errored == nil || !store.errored.RequiresReauthorization {
		t.Fatalf("status=%d error=%#v", response.Code, store.errored)
	}
}
func withPolarUser(sessions *scs.SessionManager, user CurrentUser, next http.Handler) http.Handler {
	return sessions.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), currentUserKey{}, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	}))
}
