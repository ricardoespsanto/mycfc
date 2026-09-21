package handlers

import (
	"context"
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
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type polarStoreFake struct {
	connection   dbgen.ActivityConnection
	upserted     dbgen.UpsertActivityConnectionParams
	activities   []dbgen.UpsertSyncedActivityParams
	succeeded    bool
	errored      *dbgen.RecordActivityConnectionErrorParams
	disconnected bool
}

func (s *polarStoreFake) GetActivityConnectionForUser(context.Context, dbgen.GetActivityConnectionForUserParams) (dbgen.ActivityConnection, error) {
	if s.connection.ID == uuid.Nil {
		return dbgen.ActivityConnection{}, pgx.ErrNoRows
	}
	return s.connection, nil
}
func (s *polarStoreFake) UpsertActivityConnection(_ context.Context, p dbgen.UpsertActivityConnectionParams) (dbgen.ActivityConnection, error) {
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
