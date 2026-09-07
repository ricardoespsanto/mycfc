//go:build integration

package handlers

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cfcoimbra/mycfc/internal/activity"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/polar"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresPolarServiceConnectSyncRateLimitAndDisconnectRetention(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	userID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id,name,email,password_hash,date_of_birth) VALUES ($1,'Atleta Polar',$2,'hash','1990-01-01')`, userID, "polar-service-"+uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, userID) })

	var rateLimited, disconnectFails atomic.Bool
	var exerciseRequests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v3/users":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/v3/exercises":
			exerciseRequests.Add(1)
			if rateLimited.Load() {
				w.Header().Set("Retry-After", "600")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			io.WriteString(w, `[{"id":"exercise-1","upload_time":"2026-08-23T11:00:00Z","start_time":"2026-08-23T10:00:00","start_time_utc_offset":60,"duration":"PT1H","distance":12000,"sport":"CANOEING","detailed_sport_info":"KAYAK","heart_rate":{"average":150,"maximum":180},"heart_rate_zones":[],"training_load_pro":{"date":"2026-08-23","cardio-load":90}}]`)
		case r.Method == http.MethodGet && r.URL.Path == "/v3/users/cardio-load":
			io.WriteString(w, `[{"date":"2026-08-23","cardio_load_status":"LOAD_STATUS_AVAILABLE","cardio_load":90,"strain":80,"tolerance":70,"cardio_load_ratio":1.14}]`)
		case r.Method == http.MethodDelete:
			if disconnectFails.Load() {
				w.WriteHeader(http.StatusServiceUnavailable)
			} else {
				w.WriteHeader(http.StatusNoContent)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	client, err := polar.NewClient(polar.Config{ClientID: "client", ClientSecret: "secret", RedirectURL: "https://mycfcoimbra.com/oauth/polar/callback", AuthorizationURL: provider.URL, TokenURL: provider.URL, APIBaseURL: provider.URL, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	vault, _ := activity.NewAESGCMVault(bytes.Repeat([]byte{7}, 32), "activity-v1")
	service := PostgresPolarIntegrationService{Pool: pool, Client: client, Vault: vault, Now: func() time.Time { return now }}
	credentials := polar.Credentials{AccessToken: "private-token", TokenType: "bearer", RemoteUserID: 42, ExpiresAt: now.Add(24 * time.Hour)}
	if err := service.Connect(ctx, userID, credentials, "mycfc-opaque", 0); err != nil {
		t.Fatal(err)
	}
	if err := service.Sync(ctx, userID); err != nil {
		t.Fatal(err)
	}
	var activities, loads int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM synced_activities WHERE user_id=$1), (SELECT count(*) FROM activity_load_observations WHERE user_id=$1)`, userID).Scan(&activities, &loads); err != nil {
		t.Fatal(err)
	}
	if activities != 1 || loads != 1 {
		t.Fatalf("persisted activities=%d loads=%d", activities, loads)
	}

	now = now.Add(2 * time.Minute)
	rateLimited.Store(true)
	if err := service.Sync(ctx, userID); polar.ErrorKindOf(err) != polar.ErrorRateLimited {
		t.Fatalf("rate limit error = %v", err)
	}
	connection, err := dbgen.New(pool).GetActivityConnectionForUser(ctx, dbgen.GetActivityConnectionForUserParams{UserID: userID, Provider: "polar"})
	if err != nil || !connection.ProviderRetryAfter.Valid || !connection.ProviderRetryAfter.Time.Equal(now.Add(10*time.Minute)) {
		t.Fatalf("retry deadline = %#v err=%v", connection.ProviderRetryAfter, err)
	}
	requestsBefore := exerciseRequests.Load()
	if err := service.Sync(ctx, userID); !errors.Is(err, ErrPolarSyncTooSoon) {
		t.Fatalf("immediate retry error = %v", err)
	}
	if exerciseRequests.Load() != requestsBefore {
		t.Fatal("provider called before retry deadline")
	}
	var failedJobs int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM activity_sync_jobs WHERE connection_id=$1 AND status='FAILED'`, connection.ID).Scan(&failedJobs); err != nil || failedJobs != 1 {
		t.Fatalf("failed jobs=%d err=%v", failedJobs, err)
	}

	disconnectFails.Store(true)
	if err := service.Disconnect(ctx, userID); polar.ErrorKindOf(err) != polar.ErrorTransient {
		t.Fatalf("disconnect provider error = %v", err)
	}
	disconnected, err := dbgen.New(pool).GetActivityConnectionForUser(ctx, dbgen.GetActivityConnectionForUserParams{UserID: userID, Provider: "polar"})
	if err != nil || disconnected.Status != "DISCONNECTED" || len(disconnected.CredentialsCiphertext) != 0 {
		t.Fatalf("disconnected=%#v err=%v", disconnected, err)
	}
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM synced_activities WHERE user_id=$1), (SELECT count(*) FROM activity_load_observations WHERE user_id=$1)`, userID).Scan(&activities, &loads); err != nil || activities != 1 || loads != 1 {
		t.Fatalf("retained activities=%d loads=%d err=%v", activities, loads, err)
	}
}

type failingPolarVault struct{}

func (failingPolarVault) Seal(context.Context, activity.Provider, string, activity.Secret) (activity.SealedCredentials, error) {
	return activity.SealedCredentials{}, errors.New("vault unavailable")
}
func (failingPolarVault) Open(context.Context, activity.Provider, string, activity.SealedCredentials) (activity.Secret, error) {
	return activity.Secret{}, errors.New("vault unavailable")
}

func TestPostgresPolarServiceCompensatesRegistrationWhenVaultFails(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	userID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id,name,email,password_hash,date_of_birth) VALUES ($1,'Atleta Polar',$2,'hash','1990-01-01')`, userID, "polar-vault-"+uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, userID) })
	var registered, deregistered atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			registered.Add(1)
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodDelete {
			deregistered.Add(1)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	}))
	defer provider.Close()
	now := time.Now().UTC()
	client, _ := polar.NewClient(polar.Config{ClientID: "client", ClientSecret: "secret", RedirectURL: "https://mycfcoimbra.com/oauth/polar/callback", AuthorizationURL: provider.URL, TokenURL: provider.URL, APIBaseURL: provider.URL, Now: func() time.Time { return now }})
	service := PostgresPolarIntegrationService{Pool: pool, Client: client, Vault: failingPolarVault{}, Now: func() time.Time { return now }}
	err = service.Connect(ctx, userID, polar.Credentials{AccessToken: "token", TokenType: "bearer", RemoteUserID: 43, ExpiresAt: now.Add(time.Hour)}, "mycfc-opaque", 0)
	if err == nil || registered.Load() != 1 || deregistered.Load() != 1 {
		t.Fatalf("error=%v registered=%d deregistered=%d", err, registered.Load(), deregistered.Load())
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM activity_connections WHERE user_id=$1`, userID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("connections=%d err=%v", count, err)
	}
}
