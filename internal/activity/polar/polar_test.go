package polar

import (
	"context"
	"errors"
	"github.com/cfcoimbra/mycfc/internal/activity"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientProtocolFailures(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		want       error
	}{
		{"unauthorized", "", http.StatusUnauthorized, ErrUnauthorized},
		{"rate limited", "", http.StatusTooManyRequests, ErrRateLimited},
		{"other status", "", http.StatusBadGateway, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			client := Client{ClientID: "id", ClientSecret: "secret", RedirectURL: "https://club.example/callback", TokenURL: server.URL, HTTPClient: server.Client()}
			_, err := client.ExchangeCode(context.Background(), "code")
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("error=%v", err)
			}
			if tc.want == nil && err == nil {
				t.Fatal("expected error")
			}
		})
	}
	for _, status := range []int{http.StatusBadGateway, http.StatusUnauthorized} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v3/users" {
				w.WriteHeader(http.StatusConflict)
				return
			}
			if r.Method == http.MethodPost {
				w.WriteHeader(status)
				return
			}
			w.WriteHeader(status)
		}))
		client := Client{APIURL: server.URL + "/v3", HTTPClient: server.Client()}
		_, err := client.SyncRecent(context.Background(), Credential(Token{AccessToken: "token", UserID: "42"}), activity.SyncRequest{Since: time.Now().Add(-time.Hour), Until: time.Now(), Limit: 1})
		server.Close()
		if err == nil {
			t.Fatal("unexpected successful failure response")
		}
	}

	for _, body := range []string{"not-json", `{"access_token":""}`, `{"access_token":"token","x_user_id":""}`} {
		t.Run("malformed token response", func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer server.Close()
			client := Client{ClientID: "id", ClientSecret: "secret", RedirectURL: "https://club.example/callback", TokenURL: server.URL, HTTPClient: server.Client()}
			if _, err := client.ExchangeCode(context.Background(), "code"); err == nil {
				t.Fatal("expected error")
			}
		})
	}
	if _, err := (Client{}).ExchangeCode(context.Background(), "code"); err == nil {
		t.Fatal("unconfigured exchange succeeded")
	}
	if !(Client{}).Capabilities().Disconnect {
		t.Fatal("capabilities missing disconnect")
	}
	if _, err := (Client{}).Backfill(context.Background(), activity.Secret{}, activity.SyncRequest{}); !errors.Is(err, activity.ErrUnsupported) {
		t.Fatal(err)
	}
	if _, err := (Client{}).IngestWebhook(context.Background(), activity.WebhookEnvelope{}); !errors.Is(err, activity.ErrUnsupported) {
		t.Fatal(err)
	}
	if err := (Client{}).Disconnect(context.Background(), activity.Secret{}); err != nil {
		t.Fatal(err)
	}
	status, err := (Client{}).ConnectionStatus(context.Background(), activity.Secret{})
	if err != nil || !status.RequiresReauthorization {
		t.Fatalf("status=%#v err=%v", status, err)
	}
}

func TestSyncRecentFailureResponses(t *testing.T) {
	valid := activity.SyncRequest{Since: time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC), Until: time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC), Limit: 10}
	if _, err := (Client{}).SyncRecent(context.Background(), activity.NewSecret([]byte("not-json")), valid); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err=%v", err)
	}
	if _, err := (Client{}).SyncRecent(context.Background(), activity.NewSecret(nil), activity.SyncRequest{}); err == nil {
		t.Fatal("invalid request succeeded")
	}
	for _, tc := range []struct {
		name                             string
		register, transaction, exercises string
		status                           int
	}{
		{"registration forbidden", "register", "", "", http.StatusForbidden},
		{"transaction malformed", "", "{}", "", http.StatusCreated},
		{"exercise malformed", "", `{"transaction-id":"tx"}`, "no-json", http.StatusOK},
		{"exercise invalid", "", `{"transaction-id":"tx"}`, `{"exercises":[{"id":"x","start_time":"bad","duration":"PT1H","sport":"run"}]}`, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/v3/users":
					if tc.register != "" {
						w.WriteHeader(tc.status)
					} else {
						w.WriteHeader(http.StatusConflict)
					}
				case r.Method == http.MethodPost:
					w.WriteHeader(http.StatusCreated)
					_, _ = w.Write([]byte(tc.transaction))
				case r.Method == http.MethodGet:
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(tc.exercises))
				case r.Method == http.MethodDelete:
					w.WriteHeader(http.StatusNoContent)
				}
			}))
			defer server.Close()
			client := Client{APIURL: server.URL + "/v3", HTTPClient: server.Client()}
			if _, err := client.SyncRecent(context.Background(), Credential(Token{AccessToken: "token", UserID: "42"}), valid); err == nil {
				t.Fatal("expected sync error")
			}
		})
	}
}

func TestNormalizeExerciseRejectsInvalidData(t *testing.T) {
	for _, value := range []exercise{{StartTime: "bad", Duration: "PT1H", Sport: "run"}, {StartTime: "2026-09-20T08:00:00Z", Duration: "bad", Sport: "run"}, {StartTime: "2026-09-20T08:00:00Z", Duration: "PT1H"}} {
		if _, err := normalizeExercise(value); err == nil {
			t.Fatal("invalid exercise accepted")
		}
	}
	for _, raw := range []string{"", "PT", "P1H", "PT1", "PT-1H", "PT1D"} {
		if _, err := parseDuration(raw); err == nil {
			t.Fatalf("duration %q accepted", raw)
		}
	}
	if normalizeSport("  ") != "other" || normalizeSport("Trail-Run") != "trail_run" {
		t.Fatal("sport normalization failed")
	}
}

func TestAuthorizeURLIncludesStateAndCallback(t *testing.T) {
	c := Client{ClientID: "client", ClientSecret: "secret", RedirectURL: "https://club.example/callback", AuthorizationURL: "https://polar.example/authorize"}
	got := c.AuthorizeURL("state-value")
	want := "client_id=client"
	if !contains(got, want) || !contains(got, "state=state-value") || !contains(got, "redirect_uri=https%3A%2F%2Fclub.example%2Fcallback") {
		t.Fatalf("url=%s", got)
	}
}
func TestSyncRecentNormalizesExercisesAndCleansTransaction(t *testing.T) {
	var deleted bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v3/users":
			w.WriteHeader(http.StatusConflict)
		case r.Method == http.MethodPost && r.URL.Path == "/v3/users/42/exercise-transactions":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"transaction-id":"tx"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v3/users/42/exercise-transactions/tx":
			_, _ = w.Write([]byte(`{"exercises":[{"id":"e1","start_time":"2026-09-20T08:00:00Z","duration":"PT1H2M3S","sport":"Kayaking","distance":1234.5,"heart_rate":{"average":120,"maximum":151}}]}`))
		case r.Method == http.MethodDelete:
			deleted = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	c := Client{HTTPClient: server.Client(), APIURL: server.URL + "/v3"}
	page, err := c.SyncRecent(context.Background(), Credential(Token{AccessToken: "token", UserID: "42"}), activity.SyncRequest{Since: time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC), Until: time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC), Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Activities) != 1 || page.Activities[0].DurationSeconds != 3723 || page.Activities[0].NormalizedSport != "kayaking" {
		t.Fatalf("page=%#v", page)
	}
	if !deleted {
		t.Fatal("transaction was not deleted")
	}
}
func contains(s, part string) bool {
	for i := 0; i+len(part) <= len(s); i++ {
		if s[i:i+len(part)] == part {
			return true
		}
	}
	return false
}

func TestParseDurationAcceptsShortPolarDurations(t *testing.T) {
	for raw, want := range map[string]time.Duration{"PT30M": 30 * time.Minute, "PT45S": 45 * time.Second, "PT1.5H": 90 * time.Minute} {
		got, err := parseDuration(raw)
		if err != nil || got != want {
			t.Fatalf("%s: got %s err=%v", raw, got, err)
		}
	}
}
