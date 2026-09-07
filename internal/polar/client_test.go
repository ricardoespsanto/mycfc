package polar

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestAuthorizationAndTokenExchangeUseFixedRedirectAndBasicAuth(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientID, secret, ok := r.BasicAuth()
		if !ok || clientID != "client" || secret != "secret" {
			t.Errorf("basic auth = %q %q %v", clientID, secret, ok)
		}
		body, _ := io.ReadAll(r.Body)
		values, _ := url.ParseQuery(string(body))
		if values.Get("redirect_uri") != "https://mycfcoimbra.com/oauth/polar/callback" || values.Get("code") != "one-time" {
			t.Errorf("token form = %v", values)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "token", "token_type": "bearer", "expires_in": 3600, "x_user_id": int64(42)})
	}))
	defer server.Close()
	client, err := NewClient(Config{ClientID: "client", ClientSecret: "secret", RedirectURL: "https://mycfcoimbra.com/oauth/polar/callback", AuthorizationURL: server.URL, TokenURL: server.URL, APIBaseURL: server.URL, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := client.AuthorizationURL(strings.Repeat("s", 32))
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(authorization)
	if parsed.Query().Get("scope") != AuthorizationScope || parsed.Query().Get("redirect_uri") != "https://mycfcoimbra.com/oauth/polar/callback" {
		t.Fatalf("authorization URL = %s", authorization)
	}
	credentials, err := client.ExchangeCode(context.Background(), "one-time")
	if err != nil || credentials.RemoteUserID != 42 || !credentials.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("credentials=%#v err=%v", credentials, err)
	}
}

func TestClientRejectsRedirectsWithoutForwardingBearerToken(t *testing.T) {
	var redirected bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected = true }))
	defer target.Close()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer provider.Close()
	client, err := NewClient(Config{ClientID: "client", ClientSecret: "secret", RedirectURL: "https://mycfcoimbra.com/oauth/polar/callback", AuthorizationURL: provider.URL, TokenURL: provider.URL, APIBaseURL: provider.URL})
	if err != nil {
		t.Fatal(err)
	}
	credentials := Credentials{AccessToken: "private-token", TokenType: "bearer", RemoteUserID: 42, ExpiresAt: time.Now().Add(time.Hour)}
	if _, err := client.Exercises(context.Background(), credentials); err == nil {
		t.Fatal("redirect response accepted")
	}
	if redirected {
		t.Fatal("redirect followed and bearer token could have been forwarded")
	}
}

func TestExercisesRequestAndPersistenceSummaryAreAllowlisted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "samples=false&zones=true&route=false" {
			t.Errorf("query = %q", r.URL.RawQuery)
		}
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("missing bearer token")
		}
		io.WriteString(w, `[{"id":"exercise-1","upload_time":"2026-08-23T11:00:00Z","start_time":"2026-08-23T10:00:00","start_time_utc_offset":60,"duration":"PT1H","distance":12000,"sport":"CANOEING","detailed_sport_info":"KAYAK","heart_rate":{"average":150,"maximum":180},"heart_rate_zones":[{"index":1,"lower-limit":100,"upper-limit":120,"in-zone":"PT5M"}],"training_load":90,"route":[{"latitude":1}],"samples":[{"device_id":"private"}],"device":{"id":"private"}}]`)
	}))
	defer server.Close()
	client, _ := NewClient(Config{ClientID: "client", ClientSecret: "secret", RedirectURL: "https://mycfcoimbra.com/oauth/polar/callback", AuthorizationURL: server.URL, TokenURL: server.URL, APIBaseURL: server.URL})
	items, err := client.Exercises(context.Background(), Credentials{AccessToken: "token", TokenType: "bearer", RemoteUserID: 42, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%#v err=%v", items, err)
	}
	if items[0].NormalizedSport != "paddling" || items[0].AverageHeartRate == nil || *items[0].AverageHeartRate != 150 {
		t.Fatalf("normalized item = %#v", items[0])
	}
	stored := string(items[0].RawSummaryJSON) + string(items[0].ProviderMetricsJSON)
	for _, forbidden := range []string{"route", "samples", "device", "latitude", "private"} {
		if strings.Contains(stored, forbidden) {
			t.Fatalf("stored allowlist contains %q: %s", forbidden, stored)
		}
	}
	if strings.Contains(stored, `"training_load":`) {
		t.Fatalf("legacy training_load was persisted: %s", stored)
	}
}

func TestUnavailableCardioLoadNeverBecomesZero(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `[{"date":"2026-08-23","cardio_load_status":"LOAD_STATUS_NOT_AVAILABLE","cardio_load":0,"strain":0,"tolerance":0,"cardio_load_ratio":0}]`)
	}))
	defer server.Close()
	client, _ := NewClient(Config{ClientID: "client", ClientSecret: "secret", RedirectURL: "https://mycfcoimbra.com/oauth/polar/callback", AuthorizationURL: server.URL, TokenURL: server.URL, APIBaseURL: server.URL, Now: func() time.Time { return now }})
	loads, err := client.CardioLoads(context.Background(), Credentials{AccessToken: "token", TokenType: "bearer", RemoteUserID: 42, ExpiresAt: now.Add(time.Hour)})
	if err != nil || len(loads) != 1 {
		t.Fatalf("loads=%#v err=%v", loads, err)
	}
	if loads[0].LoadValue != nil || loads[0].Strain7Days != nil || loads[0].Tolerance28Days != nil || loads[0].Ratio != nil {
		t.Fatalf("unavailable load persisted numeric zero: %#v", loads[0])
	}
}

func TestRegistrationConflictIsNotTreatedAsSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusConflict) }))
	defer server.Close()
	client, _ := NewClient(Config{ClientID: "client", ClientSecret: "secret", RedirectURL: "https://mycfcoimbra.com/oauth/polar/callback", AuthorizationURL: server.URL, TokenURL: server.URL, APIBaseURL: server.URL})
	err := client.RegisterUser(context.Background(), Credentials{AccessToken: "token", TokenType: "bearer", RemoteUserID: 42, ExpiresAt: time.Now().Add(time.Hour)}, "opaque-member")
	if ErrorKindOf(err) != ErrorConflict {
		t.Fatalf("registration conflict error = %v", err)
	}
}

func TestRegistrationRejectsUndocumentedEmptySuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer server.Close()
	client, _ := NewClient(Config{ClientID: "client", ClientSecret: "secret", RedirectURL: "https://mycfcoimbra.com/oauth/polar/callback", AuthorizationURL: server.URL, TokenURL: server.URL, APIBaseURL: server.URL})
	err := client.RegisterUser(context.Background(), Credentials{AccessToken: "token", TokenType: "bearer", RemoteUserID: 42, ExpiresAt: time.Now().Add(time.Hour)}, "opaque-member")
	if ErrorKindOf(err) != ErrorInvalidResponse {
		t.Fatalf("undocumented registration response = %v", err)
	}
}
