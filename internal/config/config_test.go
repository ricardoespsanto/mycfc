package config

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"
)

func validConfig() Config {
	return Config{
		AppEnv:                 "local",
		AppVersion:             "dev",
		GITSHA:                 strings.Repeat("0", 40),
		Port:                   8080,
		BaseURL:                "http://localhost:8080",
		DatabaseURL:            Secret("postgres://mycfc:secret@localhost:5432/mycfc?sslmode=disable"),
		CSRFAuthKeyB64:         Secret(base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))),
		AWSRegion:              "eu-west-1",
		S3BucketName:           "mycfc-local",
		S3Endpoint:             "http://localhost:9000",
		S3ForcePathStyle:       true,
		GoogleCalendarAPIKey:   "local-browser-key",
		CalendarCompetitionID:  "competition@example.test",
		CalendarTrainingID:     "training@example.test",
		CalendarSocialID:       "social@example.test",
		CalendarCleanupsID:     "cleanups@example.test",
		GalleryURL:             "https://example.invalid/gallery",
		ConsentTermsVersion:    "dev-v1",
		ConsentTermsSHA256:     strings.Repeat("0", 64),
		ConsentTermsURL:        "http://localhost:8080/legal/termos-gerais",
		ConsentImageVersion:    "dev-v1",
		ConsentImageSHA256:     strings.Repeat("0", 64),
		ConsentImageURL:        "http://localhost:8080/legal/uso-imagem",
		ConsentMinorVersion:    "dev-v1",
		ConsentMinorSHA256:     strings.Repeat("0", 64),
		ConsentMinorURL:        "http://localhost:8080/legal/responsabilidade-menor",
		LogLevel:               "INFO",
		DBMaxConns:             8,
		DBMinConns:             1,
		DBMaxConnLifetime:      30 * time.Minute,
		DBMaxConnIdleTime:      5 * time.Minute,
		DBHealthCheckPeriod:    30 * time.Second,
		SessionLifetime:        12 * time.Hour,
		SessionIdleTimeout:     30 * time.Minute,
		MaxRequestBytes:        12_582_912,
		MaxPhotoBytes:          10_485_760,
		HTTPReadHeaderTimeout:  5 * time.Second,
		HTTPReadTimeout:        15 * time.Second,
		HTTPWriteTimeout:       30 * time.Second,
		HTTPIdleTimeout:        60 * time.Second,
		ShutdownTimeout:        20 * time.Second,
		TrustedProxyCIDRValues: nil,
	}
}

func TestValidateAcceptsLocalConfiguration(t *testing.T) {
	cfg := validConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateAcceptsProductionDatabaseComponents(t *testing.T) {
	cfg := validConfig()
	cfg.DatabaseURL = ""
	cfg.DBHost = "database.internal"
	cfg.DBPort = 5432
	cfg.DBName = "mycfc"
	cfg.DBUser = "mycfc_app"
	cfg.DBPassword = Secret("database-password")
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	url, err := cfg.ResolvedDatabaseURL()
	if err != nil || !strings.Contains(url, "database.internal:5432") || !strings.Contains(url, "sslmode=require") {
		t.Fatalf("ResolvedDatabaseURL() = %q, %v", url, err)
	}
}

func TestValidateAggregatesAndSortsProblems(t *testing.T) {
	cfg := validConfig()
	cfg.Port = 0
	cfg.AppVersion = " "
	cfg.CSRFAuthKeyB64 = Secret("not-base64")

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() returned nil")
	}
	message := err.Error()
	for _, expected := range []string{"APP_VERSION", "CSRF_AUTH_KEY_B64", "PORT"} {
		if !strings.Contains(message, expected) {
			t.Errorf("error %q does not include %s", message, expected)
		}
	}
	if strings.Index(message, "APP_VERSION") > strings.Index(message, "PORT") {
		t.Errorf("problems are not sorted: %q", message)
	}
}

func TestValidateRejectsUnsafeProductionConfiguration(t *testing.T) {
	cfg := validConfig()
	cfg.AppEnv = "production"
	cfg.GITSHA = strings.Repeat("a", 40)
	cfg.BaseURL = "http://mycfc.pt"
	cfg.GalleryURL = "https://example.invalid/gallery"
	cfg.ConsentTermsURL = "http://mycfc.pt/legal/termos-gerais"
	cfg.S3Endpoint = "http://minio:9000"
	cfg.S3ForcePathStyle = true
	cfg.TrustedProxyCIDRValues = nil

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() returned nil")
	}
	for _, expected := range []string{"BASE_URL", "GALLERY_URL", "CONSENT_TERMS_URL", "S3_ENDPOINT", "S3_FORCE_PATH_STYLE", "TRUSTED_PROXY_CIDRS"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("error %q does not include %s", err, expected)
		}
	}
}

func TestValidateCookieDomain(t *testing.T) {
	cfg := validConfig()
	cfg.BaseURL = "https://app.mycfc.pt"
	cfg.CookieDomain = ".mycfc.pt"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("parent domain rejected: %v", err)
	}

	cfg.CookieDomain = "attacker.example"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "COOKIE_DOMAIN") {
		t.Fatalf("invalid cookie domain error = %v", err)
	}
}

func TestSecretIsRedacted(t *testing.T) {
	secret := Secret("super-secret")
	for _, rendered := range []string{fmt.Sprintf("%v", secret), fmt.Sprintf("%#v", secret)} {
		if strings.Contains(rendered, "super-secret") {
			t.Fatalf("secret leaked in %q", rendered)
		}
	}
}
