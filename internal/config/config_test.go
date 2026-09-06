package config

import (
	"context"
	"encoding/base64"
	"fmt"
	urlpkg "net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/caarlos0/env/v11"
)

type recordingParameterGetter struct {
	mu      sync.Mutex
	batches [][]string
	err     error
	invalid []string
	empty   bool
}

type recordingSecretGetter struct {
	output *secretsmanager.GetSecretValueOutput
	err    error
	secret string
}

func (g *recordingSecretGetter) GetSecretValue(_ context.Context, input *secretsmanager.GetSecretValueInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	if input.SecretId != nil {
		g.secret = *input.SecretId
	}
	return g.output, g.err
}

func (g *recordingParameterGetter) GetParameters(_ context.Context, input *ssm.GetParametersInput, _ ...func(*ssm.Options)) (*ssm.GetParametersOutput, error) {
	g.mu.Lock()
	g.batches = append(g.batches, append([]string(nil), input.Names...))
	g.mu.Unlock()

	if g.err != nil {
		return nil, g.err
	}
	output := &ssm.GetParametersOutput{InvalidParameters: g.invalid, Parameters: make([]types.Parameter, 0, len(input.Names))}
	if g.empty {
		return output, nil
	}
	for _, name := range input.Names {
		name, value := name, "value-for-"+name
		output.Parameters = append(output.Parameters, types.Parameter{Name: &name, Value: &value})
	}
	return output, nil
}

func TestLoadProductionParameterValuesBatchesRequests(t *testing.T) {
	client := &recordingParameterGetter{}
	parameters, err := loadProductionParameterValues(t.Context(), client)
	if err != nil {
		t.Fatal(err)
	}
	for field, name := range productionParameterNames {
		if parameters[field] != "value-for-"+name {
			t.Errorf("parameter %s = %q", field, parameters[field])
		}
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	wantBatches := (len(productionParameterNames) + 9) / 10
	if len(client.batches) != wantBatches {
		t.Fatalf("GetParameters calls = %d, want %d", len(client.batches), wantBatches)
	}
	for _, batch := range client.batches {
		if len(batch) == 0 || len(batch) > 10 {
			t.Errorf("batch size = %d", len(batch))
		}
	}
}

func TestLoadProductionParameterValuesRejectsFailedInvalidAndIncompleteResponses(t *testing.T) {
	tests := []struct {
		name   string
		client recordingParameterGetter
		want   string
	}{
		{name: "SSM failure", client: recordingParameterGetter{err: fmt.Errorf("access denied")}, want: "access denied"},
		{name: "invalid parameter", client: recordingParameterGetter{invalid: []string{"/missing"}}, want: "parameters not found"},
		{name: "incomplete response", client: recordingParameterGetter{empty: true}, want: "received 0"},
	}
	for index := range tests {
		tc := &tests[index]
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadProductionParameterValues(t.Context(), &tc.client)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestLoadProductionSecretsValidatesAWSResponsesAndParsesJSON(t *testing.T) {
	valid := `{"SMTP_PASSWORD":"smtp-pass","CSRF_AUTH_KEY_B64":"key"}`
	for _, tc := range []struct {
		name   string
		getter recordingSecretGetter
		want   string
	}{
		{name: "valid JSON", getter: recordingSecretGetter{output: &secretsmanager.GetSecretValueOutput{SecretString: &valid}}},
		{name: "AWS failure", getter: recordingSecretGetter{err: fmt.Errorf("access denied")}, want: "access denied"},
		{name: "empty secret", getter: recordingSecretGetter{output: &secretsmanager.GetSecretValueOutput{}}, want: "empty SecretString"},
		{name: "malformed JSON", getter: recordingSecretGetter{output: &secretsmanager.GetSecretValueOutput{SecretString: awsString("{")}}, want: "JSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values, err := loadProductionSecrets(t.Context(), &tc.getter)
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("error=%v", err)
				}
				return
			}
			if err != nil || values["SMTP_PASSWORD"] != "smtp-pass" || tc.getter.secret != productionSecretID {
				t.Fatalf("values=%#v secret=%q error=%v", values, tc.getter.secret, err)
			}
		})
	}
}

func TestProductionEnvironmentParseAllowsSSMSourcedGalleryURL(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("APP_VERSION", "release-test")
	t.Setenv("GIT_SHA", strings.Repeat("a", 40))
	t.Setenv("GALLERY_URL", "")

	if _, err := env.ParseAs[Config](); err != nil {
		t.Fatalf("parse production bootstrap environment: %v", err)
	}
}

func TestLoadRejectsMissingRequiredEnvironmentBeforeExternalConfiguration(t *testing.T) {
	t.Setenv("APP_ENV", "")
	if _, err := Load(t.Context()); err == nil || !strings.Contains(err.Error(), "parse configuration") {
		t.Fatalf("Load() error=%v", err)
	}
}

func TestLoadParsesAndValidatesCompleteLocalEnvironment(t *testing.T) {
	for name, value := range map[string]string{
		"APP_ENV": "local", "APP_VERSION": "test", "GIT_SHA": strings.Repeat("0", 40), "BASE_URL": "http://localhost:8080",
		"DATABASE_URL": "postgres://mycfc:secret@localhost:5432/mycfc?sslmode=disable", "DB_HOST": "", "DB_PORT": "", "DB_NAME": "", "DB_USER": "", "DB_PASSWORD": "", "DB_SSLMODE": "",
		"CSRF_AUTH_KEY_B64": base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")), "EMAIL_VERIFICATION_HMAC_KEY_B64": base64.StdEncoding.EncodeToString([]byte("abcdef0123456789abcdef0123456789")),
		"SMTP_HOST": "localhost", "SMTP_PORT": "1025", "SMTP_USERNAME": "", "SMTP_PASSWORD": "", "SMTP_FROM_ADDRESS": "mycfc@example.test", "SMTP_FROM_NAME": "MyCFCoimbra", "SMTP_TLS_MODE": "none",
		"AWS_REGION": "eu-west-1", "S3_BUCKET_NAME": "mycfc-local", "S3_ENDPOINT": "http://localhost:9000", "S3_FORCE_PATH_STYLE": "true", "GALLERY_URL": "https://example.invalid/gallery",
		"CONSENT_TERMS_VERSION": "dev-v1", "CONSENT_TERMS_SHA256": strings.Repeat("0", 64), "CONSENT_TERMS_URL": "http://localhost:8080/legal/termos",
		"CONSENT_IMAGE_VERSION": "dev-v1", "CONSENT_IMAGE_SHA256": strings.Repeat("0", 64), "CONSENT_IMAGE_URL": "http://localhost:8080/legal/imagem",
		"CONSENT_MINOR_VERSION": "dev-v1", "CONSENT_MINOR_SHA256": strings.Repeat("0", 64), "CONSENT_MINOR_URL": "http://localhost:8080/legal/menores", "TRUSTED_PROXY_CIDRS": "",
		"PRIVACY_NOTICE_URL": "http://localhost:8080/legal/privacidade", "COOKIE_NOTICE_URL": "http://localhost:8080/legal/cookies", "DATA_RIGHTS_CONTACT": "privacy@example.test",
	} {
		t.Setenv(name, value)
	}
	cfg, err := Load(t.Context())
	if err != nil || cfg.DatabaseURL.Value() == "" || cfg.Port != 8080 || cfg.DBMaxConns != 8 || cfg.SMTPTLSMode != "none" {
		t.Fatalf("config=%#v error=%v", cfg, err)
	}
}

func validConfig() Config {
	return Config{
		AppEnv:                      "local",
		AppVersion:                  "dev",
		GITSHA:                      strings.Repeat("0", 40),
		ReleaseRepository:           "ricardoespsanto/mycfc",
		Port:                        8080,
		BaseURL:                     "http://localhost:8080",
		DatabaseURL:                 Secret("postgres://mycfc:secret@localhost:5432/mycfc?sslmode=disable"),
		CSRFAuthKeyB64:              Secret(base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))),
		EmailVerificationHMACKeyB64: Secret(base64.StdEncoding.EncodeToString([]byte("abcdef0123456789abcdef0123456789"))),
		SMTPHost:                    "localhost",
		SMTPPort:                    1025,
		SMTPFromAddress:             "mycfc@example.test",
		SMTPFromName:                "MyCFCoimbra",
		SMTPTLSMode:                 "none",
		SMTPTimeout:                 10 * time.Second,
		AWSRegion:                   "eu-west-1",
		S3BucketName:                "mycfc-local",
		S3Endpoint:                  "http://localhost:9000",
		S3ForcePathStyle:            true,
		GalleryURL:                  "https://example.invalid/gallery",
		ConsentTermsVersion:         "dev-v1",
		ConsentTermsSHA256:          strings.Repeat("0", 64),
		ConsentTermsURL:             "http://localhost:8080/legal/termos-gerais",
		ConsentImageVersion:         "dev-v1",
		ConsentImageSHA256:          strings.Repeat("0", 64),
		ConsentImageURL:             "http://localhost:8080/legal/uso-imagem",
		ConsentMinorVersion:         "dev-v1",
		ConsentMinorSHA256:          strings.Repeat("0", 64),
		ConsentMinorURL:             "http://localhost:8080/legal/responsabilidade-menor",
		PrivacyNoticeURL:            "http://localhost:8080/legal/privacidade",
		CookieNoticeURL:             "http://localhost:8080/legal/cookies",
		DataRightsContact:           "privacy@example.test",
		LogLevel:                    "INFO",
		DBMaxConns:                  8,
		DBMinConns:                  1,
		DBMaxConnLifetime:           30 * time.Minute,
		DBMaxConnIdleTime:           5 * time.Minute,
		DBHealthCheckPeriod:         30 * time.Second,
		SessionLifetime:             12 * time.Hour,
		SessionIdleTimeout:          30 * time.Minute,
		MaxRequestBytes:             12_582_912,
		MaxPhotoBytes:               10_485_760,
		HTTPReadHeaderTimeout:       5 * time.Second,
		HTTPReadTimeout:             15 * time.Second,
		HTTPWriteTimeout:            30 * time.Second,
		HTTPIdleTimeout:             60 * time.Second,
		ShutdownTimeout:             20 * time.Second,
		ReleaseCheckTimeout:         3 * time.Second,
		ReleaseCheckCacheTTL:        15 * time.Minute,
		TrustedProxyCIDRValues:      nil,
	}
}

func TestObjectStorageOriginUsesOnlyTheConfiguredBrowserOrigin(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  Config
		want string
	}{
		{
			name: "local endpoint strips path query and fragment",
			cfg:  Config{S3Endpoint: "http://127.0.0.1:9000/storage?ignored=yes#fragment"},
			want: "http://127.0.0.1:9000",
		},
		{
			name: "production regional bucket",
			cfg:  Config{S3BucketName: "mycfc-production-repairs", AWSRegion: "eu-west-1"},
			want: "https://mycfc-production-repairs.s3.eu-west-1.amazonaws.com",
		},
		{
			name: "regional path style",
			cfg:  Config{S3BucketName: "mycfc-local", AWSRegion: "eu-west-1", S3ForcePathStyle: true},
			want: "https://s3.eu-west-1.amazonaws.com",
		},
		{
			name: "endpoint with credentials fails closed",
			cfg:  Config{S3Endpoint: "https://user:secret@objects.example.test"},
		},
		{
			name: "invalid region fails closed",
			cfg:  Config{S3BucketName: "mycfc-production-repairs", AWSRegion: "eu-west-1; img-src *"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.ObjectStorageOrigin(); got != tc.want {
				t.Fatalf("ObjectStorageOrigin() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestValidateRejectsUnsafeAWSRegionName(t *testing.T) {
	cfg := validConfig()
	cfg.AWSRegion = "eu-west-1; img-src *"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "AWS_REGION") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateAcceptsLocalConfiguration(t *testing.T) {
	cfg := validConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestDatabaseRoleURLsAndHTTPAddressUseConfiguredComponents(t *testing.T) {
	cfg := validConfig()
	cfg.DatabaseURL = ""
	cfg.DBHost, cfg.DBPort, cfg.DBName, cfg.DBSSLMode = "db.example.test", 5433, "mycfc", "require"
	cfg.DBUser, cfg.DBPassword = "mycfc_app", Secret("app:password")
	cfg.PostgresUser, cfg.PostgresPassword = "postgres", Secret("bootstrap password")
	cfg.MigrationDBUser, cfg.MigrationDBPassword = "mycfc_migrate", Secret("migration/password")
	cfg.Port = 8443

	if cfg.HTTPAddress() != ":8443" {
		t.Fatalf("HTTPAddress=%q", cfg.HTTPAddress())
	}
	for name, build := range map[string]func() (string, error){
		"application": cfg.ResolvedDatabaseURL,
		"bootstrap":   cfg.BootstrapDatabaseURL,
		"migration":   cfg.MigrationDatabaseURL,
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := build()
			if err != nil {
				t.Fatal(err)
			}
			u, err := urlpkg.Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			_, passwordSet := u.User.Password()
			if u.Scheme != "postgres" || u.Host != "db.example.test:5433" || u.Path != "/mycfc" || u.Query().Get("sslmode") != "require" || u.User.Username() == "" || !passwordSet {
				t.Fatalf("database URL=%q", raw)
			}
		})
	}
}

func TestNonProductionEmailDefaultsSupportExistingLocalEnvironment(t *testing.T) {
	cfg := validConfig()
	cfg.EmailVerificationHMACKeyB64 = ""
	cfg.SMTPHost = ""
	cfg.SMTPPort = 587
	cfg.SMTPFromAddress = ""
	cfg.SMTPTLSMode = "starttls"

	cfg.applyNonProductionEmailDefaults()

	if cfg.SMTPHost != "localhost" || cfg.SMTPPort != 1025 || cfg.SMTPTLSMode != "none" {
		t.Fatalf("local SMTP defaults = %s:%d (%s)", cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPTLSMode)
	}
	if cfg.SMTPFromAddress != "mycfc@example.test" {
		t.Fatalf("SMTPFromAddress = %q", cfg.SMTPFromAddress)
	}
	if _, err := cfg.EmailVerificationHMACKey(); err != nil {
		t.Fatalf("EmailVerificationHMACKey() error = %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestProductionEmailConfigurationHasNoDefaults(t *testing.T) {
	cfg := validConfig()
	cfg.AppEnv = "production"
	cfg.EmailVerificationHMACKeyB64 = ""
	cfg.SMTPHost = ""
	cfg.SMTPFromAddress = ""

	cfg.applyNonProductionEmailDefaults()

	if cfg.EmailVerificationHMACKeyB64.Value() != "" || cfg.SMTPHost != "" || cfg.SMTPFromAddress != "" {
		t.Fatal("production email configuration received local defaults")
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() returned nil")
	}
	for _, expected := range []string{"EMAIL_VERIFICATION_HMAC_KEY_B64", "SMTP_FROM_ADDRESS", "SMTP_HOST"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("error %q does not include %s", err, expected)
		}
	}
}

func TestApplyProductionRemoteConfigOverwritesEnvironmentValues(t *testing.T) {
	cfg := validConfig()
	cfg.AppEnv = "production"
	cfg.EmailVerificationHMACKeyB64 = Secret("from-env")
	cfg.TurnstileSiteKey = "from-env"
	cfg.TurnstileSecretKey = Secret("from-env")
	cfg.SMTPHost = "from-env"
	cfg.SMTPPort = 25
	cfg.SMTPUsername = "from-env"
	cfg.SMTPPassword = Secret("from-env")
	cfg.SMTPFromAddress = "from-env@example.test"
	cfg.SMTPFromName = "from-env"
	cfg.SMTPTLSMode = "none"
	cfg.SMTPTimeout = time.Second
	cfg.S3Endpoint = "http://minio:9000"
	cfg.S3ForcePathStyle = true

	parameters := validProductionParameters()
	secrets := validProductionSecrets()
	if err := cfg.applyProductionRemoteConfig(parameters, secrets); err != nil {
		t.Fatalf("applyProductionRemoteConfig() error = %v", err)
	}

	if cfg.BaseURL != parameters["BASE_URL"] || cfg.DBHost != parameters["DB_HOST"] || cfg.DBPassword.Value() != secrets["APP_DB_PASSWORD"] {
		t.Fatal("database/base config was not loaded from AWS values")
	}
	if cfg.PostgresUser != parameters["POSTGRES_USER"] || cfg.PostgresPassword.Value() != secrets["POSTGRES_PASSWORD"] {
		t.Fatal("bootstrap database config was not loaded from AWS values")
	}
	if cfg.MigrationDBUser != parameters["MIGRATION_DB_USER"] || cfg.MigrationDBPassword.Value() != secrets["MIGRATION_DB_PASSWORD"] {
		t.Fatal("migration database config was not loaded from AWS values")
	}
	if cfg.CSRFAuthKeyB64.Value() != secrets["CSRF_AUTH_KEY_B64"] || cfg.EmailVerificationHMACKeyB64.Value() != secrets["EMAIL_VERIFICATION_HMAC_KEY_B64"] {
		t.Fatal("signing keys were not loaded from Secrets Manager values")
	}
	if cfg.TurnstileSiteKey != "site-key" || cfg.TurnstileSecretKey.Value() != "secret-key" {
		t.Fatal("Turnstile values were not loaded from AWS values")
	}
	if cfg.SMTPHost != "email-smtp.eu-west-1.amazonaws.com" || cfg.SMTPPort != 587 || cfg.SMTPTimeout != 10*time.Second {
		t.Fatalf("SMTP values were not loaded from AWS values: host=%q port=%d timeout=%s", cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPTimeout)
	}
	if cfg.SMTPUsername != "smtp-user" || cfg.SMTPPassword.Value() != "smtp-pass" || cfg.SMTPFromAddress != "no-reply@mycfcoimbra.com" {
		t.Fatal("SMTP credentials/sender were not loaded from SSM values")
	}
	if cfg.S3Endpoint != "" || cfg.S3ForcePathStyle {
		t.Fatalf("production S3 local settings were not cleared: endpoint=%q pathStyle=%t", cfg.S3Endpoint, cfg.S3ForcePathStyle)
	}
	if cfg.TrustedProxyCIDRValues[0] != "172.30.0.0/24" || cfg.MaxRequestBytes != 12582912 {
		t.Fatal("operational settings were not loaded from SSM values")
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() after remote config error = %v", err)
	}
}

func TestApplyProductionRemoteConfigRequiresEveryParameterAndSecret(t *testing.T) {
	parameters := validProductionParameters()
	secrets := validProductionSecrets()
	delete(parameters, "SMTP_HOST")
	delete(secrets, "TURNSTILE_SECRET_KEY")

	cfg := validConfig()
	err := cfg.applyProductionRemoteConfig(parameters, secrets)
	if err == nil {
		t.Fatal("applyProductionRemoteConfig() returned nil")
	}
	for _, expected := range []string{"SMTP_HOST", "TURNSTILE_SECRET_KEY"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("error %q does not include %s", err, expected)
		}
	}
}

func TestApplyProductionRemoteConfigRejectsInvalidNumbersDurationsAndBooleans(t *testing.T) {
	parameters := validProductionParameters()
	parameters["SMTP_PORT"] = "587abc"
	parameters["SMTP_TIMEOUT"] = "soon"
	parameters["S3_FORCE_PATH_STYLE"] = "definitely"

	cfg := validConfig()
	err := cfg.applyProductionRemoteConfig(parameters, validProductionSecrets())
	if err == nil {
		t.Fatal("applyProductionRemoteConfig() returned nil")
	}
	for _, expected := range []string{"S3_FORCE_PATH_STYLE", "SMTP_PORT", "SMTP_TIMEOUT"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("error %q does not include %s", err, expected)
		}
	}
}

func TestValidateRequiresTurnstilePair(t *testing.T) {
	cfg := validConfig()
	cfg.TurnstileSiteKey = "site-key"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "TURNSTILE_SITE_KEY") {
		t.Fatalf("missing turnstile secret error = %v", err)
	}

	cfg.TurnstileSiteKey = ""
	cfg.TurnstileSecretKey = Secret("secret-key")
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "TURNSTILE_SITE_KEY") {
		t.Fatalf("missing turnstile site key error = %v", err)
	}
}

func validProductionParameters() map[string]string {
	return map[string]string{
		"BASE_URL":                 "https://mycfcoimbra.com",
		"DB_HOST":                  "postgres",
		"DB_PORT":                  "5432",
		"DB_NAME":                  "mycfc",
		"DB_USER":                  "mycfc_app",
		"POSTGRES_USER":            "mycfc",
		"MIGRATION_DB_USER":        "mycfc_migrator",
		"DB_SSLMODE":               "disable",
		"SMTP_HOST":                "email-smtp.eu-west-1.amazonaws.com",
		"SMTP_PORT":                "587",
		"SMTP_FROM_ADDRESS":        "no-reply@mycfcoimbra.com",
		"SMTP_FROM_NAME":           "MyCFCoimbra",
		"SMTP_TLS_MODE":            "starttls",
		"SMTP_TIMEOUT":             "10s",
		"TURNSTILE_SITE_KEY":       "site-key",
		"S3_BUCKET_NAME":           "mycfc-production-repairs",
		"S3_FORCE_PATH_STYLE":      "false",
		"GALLERY_URL":              "https://mycfcoimbra.com/gallery",
		"CONSENT_TERMS_VERSION":    "0.0.1",
		"CONSENT_TERMS_SHA256":     strings.Repeat("a", 64),
		"CONSENT_TERMS_URL":        "https://mycfcoimbra.com/legal/termos-gerais",
		"CONSENT_IMAGE_VERSION":    "0.0.1",
		"CONSENT_IMAGE_SHA256":     strings.Repeat("b", 64),
		"CONSENT_IMAGE_URL":        "https://mycfcoimbra.com/legal/uso-imagem",
		"CONSENT_MINOR_VERSION":    "0.0.1",
		"CONSENT_MINOR_SHA256":     strings.Repeat("c", 64),
		"CONSENT_MINOR_URL":        "https://mycfcoimbra.com/legal/responsabilidade-menor",
		"PRIVACY_NOTICE_URL":       "https://mycfcoimbra.com/legal/privacidade",
		"COOKIE_NOTICE_URL":        "https://mycfcoimbra.com/legal/cookies",
		"DATA_RIGHTS_CONTACT":      "cfluvialcoimbra@gmail.com",
		"LOG_LEVEL":                "INFO",
		"TRUSTED_PROXY_CIDRS":      "172.30.0.0/24",
		"RELEASE_REPOSITORY":       "ricardoespsanto/mycfc",
		"DB_MAX_CONNS":             "8",
		"DB_MIN_CONNS":             "1",
		"DB_MAX_CONN_LIFETIME":     "30m",
		"DB_MAX_CONN_IDLE_TIME":    "5m",
		"DB_HEALTH_CHECK_PERIOD":   "30s",
		"SESSION_LIFETIME":         "12h",
		"SESSION_IDLE_TIMEOUT":     "30m",
		"MAX_REQUEST_BYTES":        "12582912",
		"MAX_PHOTO_BYTES":          "10485760",
		"HTTP_READ_HEADER_TIMEOUT": "5s",
		"HTTP_READ_TIMEOUT":        "15s",
		"HTTP_WRITE_TIMEOUT":       "30s",
		"HTTP_IDLE_TIMEOUT":        "60s",
		"SHUTDOWN_TIMEOUT":         "20s",
		"RELEASE_CHECK_TIMEOUT":    "3s",
		"RELEASE_CHECK_CACHE_TTL":  "15m",
	}
}

func validProductionSecrets() map[string]string {
	return map[string]string{
		"POSTGRES_PASSWORD":               "postgres-pass",
		"APP_DB_PASSWORD":                 "app-db-pass",
		"MIGRATION_DB_PASSWORD":           "migration-db-pass",
		"CSRF_AUTH_KEY_B64":               base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")),
		"EMAIL_VERIFICATION_HMAC_KEY_B64": base64.StdEncoding.EncodeToString([]byte("abcdef0123456789abcdef0123456789")),
		"TURNSTILE_SECRET_KEY":            "secret-key",
		"SMTP_USERNAME":                   "smtp-user",
		"SMTP_PASSWORD":                   "smtp-pass",
	}
}

func TestValidateRequiresTurnstileInProduction(t *testing.T) {
	cfg := validConfig()
	cfg.AppEnv = "production"
	cfg.GITSHA = strings.Repeat("a", 40)
	cfg.BaseURL = "https://mycfc.pt"
	cfg.GalleryURL = "https://mycfc.pt/gallery"
	cfg.ConsentTermsURL = "https://mycfc.pt/legal/termos-gerais"
	cfg.ConsentImageURL = "https://mycfc.pt/legal/uso-imagem"
	cfg.ConsentMinorURL = "https://mycfc.pt/legal/responsabilidade-menor"
	cfg.PrivacyNoticeURL = "https://mycfc.pt/legal/privacidade"
	cfg.CookieNoticeURL = "https://mycfc.pt/legal/cookies"
	cfg.ConsentTermsSHA256 = strings.Repeat("a", 64)
	cfg.ConsentImageSHA256 = strings.Repeat("b", 64)
	cfg.ConsentMinorSHA256 = strings.Repeat("c", 64)
	cfg.SMTPTLSMode = "starttls"
	cfg.S3Endpoint = ""
	cfg.S3ForcePathStyle = false
	cfg.TrustedProxyCIDRValues = []string{"172.30.0.0/24"}

	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "TURNSTILE_SITE_KEY") {
		t.Fatalf("missing production turnstile error = %v", err)
	}

	cfg.TurnstileSiteKey = "site-key"
	cfg.TurnstileSecretKey = Secret("secret-key")
	if err := cfg.Validate(); err != nil {
		t.Fatalf("production turnstile configuration rejected: %v", err)
	}
}

func TestValidateAcceptsProductionDatabaseComponents(t *testing.T) {
	cfg := validConfig()
	cfg.DatabaseURL = ""
	cfg.DBHost = "database.internal"
	cfg.DBPort = 5432
	cfg.DBName = "mycfc"
	cfg.DBUser = "mycfc_app"
	cfg.DBPassword = Secret("database:/?# password")
	cfg.DBSSLMode = "disable"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	url, err := cfg.ResolvedDatabaseURL()
	if err != nil || !strings.Contains(url, "database.internal:5432") || !strings.Contains(url, "sslmode=disable") {
		t.Fatalf("ResolvedDatabaseURL() = %q, %v", url, err)
	}
	parsed, err := urlpkg.Parse(url)
	if err != nil {
		t.Fatal(err)
	}
	password, _ := parsed.User.Password()
	if password != "database:/?# password" {
		t.Fatalf("database password = %q", password)
	}
}

func TestValidateAggregatesAndSortsProblems(t *testing.T) {
	cfg := validConfig()
	cfg.Port = 0
	cfg.AppVersion = " "
	cfg.ReleaseRepository = "bad/name/extra"
	cfg.CSRFAuthKeyB64 = Secret("not-base64")

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() returned nil")
	}
	message := err.Error()
	for _, expected := range []string{"APP_VERSION", "CSRF_AUTH_KEY_B64", "PORT", "RELEASE_REPOSITORY"} {
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

func TestValidateRequiresSafeLegalDiscoveryConfiguration(t *testing.T) {
	cfg := validConfig()
	cfg.PrivacyNoticeURL = ""
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "PRIVACY_NOTICE_URL") {
		t.Fatalf("missing privacy URL error = %v", err)
	}

	cfg = validConfig()
	cfg.DataRightsContact = "not-an-email"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "DATA_RIGHTS_CONTACT") {
		t.Fatalf("malformed rights contact error = %v", err)
	}

	cfg = validConfig()
	cfg.AppEnv = "production"
	cfg.GITSHA = strings.Repeat("a", 40)
	cfg.BaseURL = "https://mycfcoimbra.com"
	cfg.GalleryURL = "https://mycfcoimbra.com/gallery"
	cfg.ConsentTermsURL = "https://mycfcoimbra.com/legal/termos-gerais"
	cfg.ConsentImageURL = "https://mycfcoimbra.com/legal/uso-imagem"
	cfg.ConsentMinorURL = "https://mycfcoimbra.com/legal/responsabilidade-menor"
	cfg.PrivacyNoticeURL = "http://mycfcoimbra.com/legal/privacidade"
	cfg.CookieNoticeURL = "https://mycfcoimbra.com/legal/cookies"
	cfg.ConsentTermsSHA256 = strings.Repeat("a", 64)
	cfg.ConsentImageSHA256 = strings.Repeat("b", 64)
	cfg.ConsentMinorSHA256 = strings.Repeat("c", 64)
	cfg.SMTPTLSMode = "starttls"
	cfg.S3Endpoint = ""
	cfg.S3ForcePathStyle = false
	cfg.TrustedProxyCIDRValues = []string{"172.30.0.0/24"}
	cfg.TurnstileSiteKey = "site-key"
	cfg.TurnstileSecretKey = Secret("secret-key")
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "PRIVACY_NOTICE_URL") {
		t.Fatalf("HTTP production privacy URL error = %v", err)
	}
	cfg.PrivacyNoticeURL = "https://mycfcoimbra.com/legal/privacidade"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid production legal configuration rejected: %v", err)
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

func TestConfigurationValueHelpersValidateBoundaryCases(t *testing.T) {
	var secret Secret
	if err := secret.UnmarshalText([]byte("loaded-from-environment")); err != nil || secret.Value() != "loaded-from-environment" {
		t.Fatalf("UnmarshalText() secret=%q error=%v", secret.Value(), err)
	}

	for _, tc := range []struct {
		name  string
		check func() error
		want  string
	}{
		{"database URL empty", func() error { return validateDatabaseURL("") }, "must not be empty"},
		{"database URL wrong scheme", func() error { return validateDatabaseURL("https://db.example.test/mycfc") }, "PostgreSQL"},
		{"absolute URL user info", func() error { _, err := validateAbsoluteURL("https://user:pass@example.test", false, true); return err }, "user information"},
		{"absolute URL unsupported scheme", func() error { _, err := validateAbsoluteURL("ftp://example.test", false, true); return err }, "HTTP or HTTPS"},
		{"bucket IP address", func() error { return validateBucketName("127.0.0.1") }, "DNS-compatible"},
		{"bucket repeated dot", func() error { return validateBucketName("my..bucket") }, "DNS-compatible"},
		{"cookie domain unrelated", func() error { return validateCookieDomain("attacker.example", "app.mycfc.pt") }, "parent domain"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.check()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v, want %q", err, tc.want)
			}
		})
	}

	for _, raw := range []string{"postgres://db.example.test/mycfc", "postgresql://db.example.test/mycfc"} {
		if err := validateDatabaseURL(raw); err != nil {
			t.Fatalf("validateDatabaseURL(%q) error=%v", raw, err)
		}
	}
	prefixes, err := (Config{TrustedProxyCIDRValues: []string{" 10.0.1.7/16 ", "", "2001:db8::1/64"}}).TrustedProxyCIDRs()
	if err != nil || len(prefixes) != 2 || prefixes[0].String() != "10.0.0.0/16" || prefixes[1].String() != "2001:db8::/64" {
		t.Fatalf("TrustedProxyCIDRs()=%v, %v", prefixes, err)
	}
	if _, err := (Config{TrustedProxyCIDRValues: []string{"not-a-cidr"}}).TrustedProxyCIDRs(); err == nil {
		t.Fatal("TrustedProxyCIDRs() accepted invalid CIDR")
	}
}
