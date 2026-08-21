package app

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/cfcoimbra/mycfc/internal/config"
	"github.com/cfcoimbra/mycfc/internal/handlers"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestApplicationRunShutsDownAfterContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	application := &Application{
		Config: config.Config{AppVersion: "test", ShutdownTimeout: time.Second},
		Logger: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
		Server: &http.Server{Addr: "127.0.0.1:0", Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})},
	}

	result := make(chan error, 1)
	go func() { result <- application.Run(ctx) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not finish after context cancellation")
	}
}

func TestApplicationRunWrapsListenFailures(t *testing.T) {
	application := &Application{
		Config: config.Config{AppVersion: "test"},
		Logger: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
		Server: &http.Server{Addr: "bad address", Handler: http.NotFoundHandler()},
	}
	if err := application.Run(context.Background()); err == nil {
		t.Fatal("Run() succeeded with an invalid address")
	}
}

func TestApplicationCloseAcceptsPartiallyConstructedApplication(t *testing.T) {
	(&Application{}).Close()
}

func TestApplicationNewReturnsConfigurationErrorsBeforeStartingResources(t *testing.T) {
	t.Setenv("APP_ENV", "")
	if _, err := New(t.Context()); err == nil || !strings.Contains(err.Error(), "parse configuration") {
		t.Fatalf("New() error=%v", err)
	}
}

func TestNewHTTPServerAppliesSecurityAndTrustedProxyMiddleware(t *testing.T) {
	cfg := config.Config{AppEnv: "production", Port: 8443, HTTPReadHeaderTimeout: time.Second, HTTPReadTimeout: 2 * time.Second, HTTPWriteTimeout: 3 * time.Second, HTTPIdleTimeout: 4 * time.Second}
	server := newHTTPServer(cfg, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), scs.New(), handlers.System{}, func(next http.Handler) http.Handler { return next }, []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Forwarded-For") == "10.1.2.3" {
			w.Header().Set("X-Trusted-Proxy", "yes")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "https://mycfc.example", nil)
	request.RemoteAddr = "10.1.2.3:1234"
	request.Header.Set("X-Forwarded-For", "10.1.2.3")
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || response.Header().Get("X-Content-Type-Options") != "nosniff" || response.Header().Get("X-Trusted-Proxy") != "yes" || server.Addr != ":8443" || server.IdleTimeout != 4*time.Second {
		t.Fatalf("response=%d headers=%v server=%#v", response.Code, response.Header(), server)
	}
}

func TestApplicationNewAssemblesConfiguredServerWithoutExternalConnections(t *testing.T) {
	originalLoad, originalOpen, originalPing, originalAWS := loadApplicationConfig, openApplicationPool, pingApplicationPool, loadApplicationAWS
	t.Cleanup(func() {
		loadApplicationConfig, openApplicationPool, pingApplicationPool, loadApplicationAWS = originalLoad, originalOpen, originalPing, originalAWS
	})
	loadApplicationConfig = func(context.Context) (config.Config, error) {
		return config.Config{AppEnv: "local", AppVersion: "test", GITSHA: strings.Repeat("0", 40), ReleaseRepository: "cfcoimbra/mycfc", Port: 8080, BaseURL: "http://localhost:8080", DatabaseURL: config.Secret("postgres://mycfc:secret@localhost:5432/mycfc?sslmode=disable"), DBMaxConns: 8, DBMinConns: 1, DBMaxConnLifetime: time.Minute, DBMaxConnIdleTime: time.Minute, DBHealthCheckPeriod: time.Minute, SessionLifetime: time.Hour, SessionIdleTimeout: time.Minute, AWSRegion: "eu-west-1", S3BucketName: "mycfc-local", S3Endpoint: "http://localhost:9000", S3ForcePathStyle: true, CSRFAuthKeyB64: config.Secret("MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="), EmailVerificationHMACKeyB64: config.Secret("YWJjZGVmMDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODk="), SMTPHost: "localhost", SMTPPort: 1025, SMTPFromAddress: "mycfc@example.test", SMTPFromName: "MyCFC", SMTPTLSMode: "none", SMTPTimeout: time.Second, MaxRequestBytes: 1024, MaxPhotoBytes: 512, ConsentTermsVersion: "dev", ConsentTermsSHA256: strings.Repeat("0", 64), ConsentTermsURL: "http://localhost:8080/terms", ConsentImageVersion: "dev", ConsentImageSHA256: strings.Repeat("0", 64), ConsentImageURL: "http://localhost:8080/image", ConsentMinorVersion: "dev", ConsentMinorSHA256: strings.Repeat("0", 64), ConsentMinorURL: "http://localhost:8080/minor", ReleaseCheckTimeout: time.Second, ReleaseCheckCacheTTL: time.Minute}, nil
	}
	openApplicationPool = func(ctx context.Context, poolConfig *pgxpool.Config) (*pgxpool.Pool, error) {
		return pgxpool.NewWithConfig(ctx, poolConfig)
	}
	pingApplicationPool = func(context.Context, *pgxpool.Pool) error { return nil }
	loadApplicationAWS = func(context.Context, ...func(*awsconfig.LoadOptions) error) (aws.Config, error) {
		return aws.Config{Region: "eu-west-1"}, nil
	}
	application, err := New(t.Context())
	if err != nil || application.Server == nil || application.EmailWorker == nil || application.Server.Addr != ":8080" || application.Sessions.Cookie.Name != "mycfc_session" {
		t.Fatalf("application=%#v error=%v", application, err)
	}
}

func TestApplicationNewPropagatesConfigurationAndPoolStartupFailures(t *testing.T) {
	originalLoad, originalOpen := loadApplicationConfig, openApplicationPool
	t.Cleanup(func() { loadApplicationConfig, openApplicationPool = originalLoad, originalOpen })
	loadApplicationConfig = func(context.Context) (config.Config, error) {
		return config.Config{}, errors.New("configuration unavailable")
	}
	if _, err := New(t.Context()); err == nil || !strings.Contains(err.Error(), "configuration unavailable") {
		t.Fatalf("configuration error=%v", err)
	}
	loadApplicationConfig = func(context.Context) (config.Config, error) {
		return config.Config{DatabaseURL: config.Secret("postgres://mycfc:secret@localhost:5432/mycfc?sslmode=disable")}, nil
	}
	openApplicationPool = func(context.Context, *pgxpool.Config) (*pgxpool.Pool, error) {
		return nil, errors.New("database unavailable")
	}
	if _, err := New(t.Context()); err == nil || !strings.Contains(err.Error(), "open database pool") {
		t.Fatalf("pool error=%v", err)
	}
}

func TestApplicationNewCleansUpAfterPostPoolStartupFailures(t *testing.T) {
	originalLoad, originalOpen, originalPing, originalAWS := loadApplicationConfig, openApplicationPool, pingApplicationPool, loadApplicationAWS
	t.Cleanup(func() {
		loadApplicationConfig, openApplicationPool, pingApplicationPool, loadApplicationAWS = originalLoad, originalOpen, originalPing, originalAWS
	})
	loadApplicationConfig = func(context.Context) (config.Config, error) { return applicationStartupTestConfig(), nil }
	openApplicationPool = func(ctx context.Context, poolConfig *pgxpool.Config) (*pgxpool.Pool, error) {
		return pgxpool.NewWithConfig(ctx, poolConfig)
	}
	loadApplicationAWS = func(context.Context, ...func(*awsconfig.LoadOptions) error) (aws.Config, error) {
		return aws.Config{Region: "eu-west-1"}, nil
	}

	pingApplicationPool = func(context.Context, *pgxpool.Pool) error { return errors.New("database unavailable") }
	if _, err := New(t.Context()); err == nil || !strings.Contains(err.Error(), "database ping failed") {
		t.Fatalf("ping error=%v", err)
	}

	pingApplicationPool = func(context.Context, *pgxpool.Pool) error { return nil }
	loadApplicationAWS = func(context.Context, ...func(*awsconfig.LoadOptions) error) (aws.Config, error) {
		return aws.Config{}, errors.New("AWS unavailable")
	}
	if _, err := New(t.Context()); err == nil || !strings.Contains(err.Error(), "load AWS configuration") {
		t.Fatalf("AWS error=%v", err)
	}

	loadApplicationAWS = func(context.Context, ...func(*awsconfig.LoadOptions) error) (aws.Config, error) {
		return aws.Config{Region: "eu-west-1"}, nil
	}
	loadApplicationConfig = func(context.Context) (config.Config, error) {
		cfg := applicationStartupTestConfig()
		cfg.CSRFAuthKeyB64 = config.Secret("not-base64")
		return cfg, nil
	}
	if _, err := New(t.Context()); err == nil || !strings.Contains(err.Error(), "CSRF") {
		t.Fatalf("CSRF key error=%v", err)
	}
}

func applicationStartupTestConfig() config.Config {
	return config.Config{AppEnv: "local", AppVersion: "test", GITSHA: strings.Repeat("0", 40), ReleaseRepository: "cfcoimbra/mycfc", Port: 8080, BaseURL: "http://localhost:8080", DatabaseURL: config.Secret("postgres://mycfc:secret@localhost:5432/mycfc?sslmode=disable"), DBMaxConns: 8, DBMinConns: 1, DBMaxConnLifetime: time.Minute, DBMaxConnIdleTime: time.Minute, DBHealthCheckPeriod: time.Minute, SessionLifetime: time.Hour, SessionIdleTimeout: time.Minute, AWSRegion: "eu-west-1", S3BucketName: "mycfc-local", S3Endpoint: "http://localhost:9000", S3ForcePathStyle: true, CSRFAuthKeyB64: config.Secret("MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="), EmailVerificationHMACKeyB64: config.Secret("YWJjZGVmMDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODk="), SMTPHost: "localhost", SMTPPort: 1025, SMTPFromAddress: "mycfc@example.test", SMTPFromName: "MyCFC", SMTPTLSMode: "none", SMTPTimeout: time.Second, MaxRequestBytes: 1024, MaxPhotoBytes: 512, ConsentTermsVersion: "dev", ConsentTermsSHA256: strings.Repeat("0", 64), ConsentTermsURL: "http://localhost:8080/terms", ConsentImageVersion: "dev", ConsentImageSHA256: strings.Repeat("0", 64), ConsentImageURL: "http://localhost:8080/image", ConsentMinorVersion: "dev", ConsentMinorSHA256: strings.Repeat("0", 64), ConsentMinorURL: "http://localhost:8080/minor", ReleaseCheckTimeout: time.Second, ReleaseCheckCacheTTL: time.Minute}
}
