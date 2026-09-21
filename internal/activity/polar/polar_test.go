package polar

import (
	"context"
	"github.com/cfcoimbra/mycfc/internal/activity"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

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
