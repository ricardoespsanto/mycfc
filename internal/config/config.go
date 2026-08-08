package config

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/caarlos0/env/v11"
)

var (
	lowerHex40 = regexp.MustCompile(`^[0-9a-f]{40}$`)
	lowerHex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)
	bucketName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]{1,61}[a-z0-9])?$`)
)

const productionParameterPrefix = "/mycfc/production"
const productionSecretID = "/mycfc/production/app-secrets"

var productionParameterNames = map[string]string{
	"BASE_URL":                 productionParameterPrefix + "/base-url",
	"DB_HOST":                  productionParameterPrefix + "/db/host",
	"DB_PORT":                  productionParameterPrefix + "/db/port",
	"DB_NAME":                  productionParameterPrefix + "/db/name",
	"DB_USER":                  productionParameterPrefix + "/db/user",
	"POSTGRES_USER":            productionParameterPrefix + "/db/bootstrap-user",
	"MIGRATION_DB_USER":        productionParameterPrefix + "/db/migration-user",
	"DB_SSLMODE":               productionParameterPrefix + "/db/sslmode",
	"SMTP_HOST":                productionParameterPrefix + "/smtp/host",
	"SMTP_PORT":                productionParameterPrefix + "/smtp/port",
	"SMTP_FROM_ADDRESS":        productionParameterPrefix + "/smtp/from-address",
	"SMTP_FROM_NAME":           productionParameterPrefix + "/smtp/from-name",
	"SMTP_TLS_MODE":            productionParameterPrefix + "/smtp/tls-mode",
	"SMTP_TIMEOUT":             productionParameterPrefix + "/smtp/timeout",
	"TURNSTILE_SITE_KEY":       productionParameterPrefix + "/turnstile/site-key",
	"S3_BUCKET_NAME":           productionParameterPrefix + "/s3/bucket-name",
	"S3_FORCE_PATH_STYLE":      productionParameterPrefix + "/s3/force-path-style",
	"GALLERY_URL":              productionParameterPrefix + "/gallery-url",
	"CONSENT_TERMS_VERSION":    productionParameterPrefix + "/consent/terms/version",
	"CONSENT_TERMS_SHA256":     productionParameterPrefix + "/consent/terms/sha256",
	"CONSENT_TERMS_URL":        productionParameterPrefix + "/consent/terms/url",
	"CONSENT_IMAGE_VERSION":    productionParameterPrefix + "/consent/image/version",
	"CONSENT_IMAGE_SHA256":     productionParameterPrefix + "/consent/image/sha256",
	"CONSENT_IMAGE_URL":        productionParameterPrefix + "/consent/image/url",
	"CONSENT_MINOR_VERSION":    productionParameterPrefix + "/consent/minor/version",
	"CONSENT_MINOR_SHA256":     productionParameterPrefix + "/consent/minor/sha256",
	"CONSENT_MINOR_URL":        productionParameterPrefix + "/consent/minor/url",
	"LOG_LEVEL":                productionParameterPrefix + "/log-level",
	"TRUSTED_PROXY_CIDRS":      productionParameterPrefix + "/trusted-proxy-cidrs",
	"RELEASE_REPOSITORY":       productionParameterPrefix + "/release/repository",
	"DB_MAX_CONNS":             productionParameterPrefix + "/db/max-conns",
	"DB_MIN_CONNS":             productionParameterPrefix + "/db/min-conns",
	"DB_MAX_CONN_LIFETIME":     productionParameterPrefix + "/db/max-conn-lifetime",
	"DB_MAX_CONN_IDLE_TIME":    productionParameterPrefix + "/db/max-conn-idle-time",
	"DB_HEALTH_CHECK_PERIOD":   productionParameterPrefix + "/db/health-check-period",
	"SESSION_LIFETIME":         productionParameterPrefix + "/session/lifetime",
	"SESSION_IDLE_TIMEOUT":     productionParameterPrefix + "/session/idle-timeout",
	"MAX_REQUEST_BYTES":        productionParameterPrefix + "/http/max-request-bytes",
	"MAX_PHOTO_BYTES":          productionParameterPrefix + "/http/max-photo-bytes",
	"HTTP_READ_HEADER_TIMEOUT": productionParameterPrefix + "/http/read-header-timeout",
	"HTTP_READ_TIMEOUT":        productionParameterPrefix + "/http/read-timeout",
	"HTTP_WRITE_TIMEOUT":       productionParameterPrefix + "/http/write-timeout",
	"HTTP_IDLE_TIMEOUT":        productionParameterPrefix + "/http/idle-timeout",
	"SHUTDOWN_TIMEOUT":         productionParameterPrefix + "/http/shutdown-timeout",
	"RELEASE_CHECK_TIMEOUT":    productionParameterPrefix + "/release/check-timeout",
	"RELEASE_CHECK_CACHE_TTL":  productionParameterPrefix + "/release/check-cache-ttl",
}

var productionSecretFields = []string{
	"POSTGRES_PASSWORD",
	"APP_DB_PASSWORD",
	"MIGRATION_DB_PASSWORD",
	"CSRF_AUTH_KEY_B64",
	"EMAIL_VERIFICATION_HMAC_KEY_B64",
	"TURNSTILE_SECRET_KEY",
	"SMTP_USERNAME",
	"SMTP_PASSWORD",
}

// Secret redacts sensitive configuration from fmt output.
type Secret string

func (s *Secret) UnmarshalText(text []byte) error {
	*s = Secret(text)
	return nil
}

func (Secret) String() string   { return "[REDACTED]" }
func (Secret) GoString() string { return "[REDACTED]" }
func (s Secret) Value() string  { return string(s) }

type Config struct {
	AppEnv            string `env:"APP_ENV,required"`
	AppVersion        string `env:"APP_VERSION,required"`
	GITSHA            string `env:"GIT_SHA,required"`
	AppReleasedAt     string `env:"APP_RELEASED_AT"`
	ReleaseRepository string `env:"RELEASE_REPOSITORY" envDefault:"ricardoespsanto/mycfc"`
	Port              int    `env:"PORT" envDefault:"8080"`
	BaseURL           string `env:"BASE_URL"`

	DatabaseURL                 Secret `env:"DATABASE_URL"`
	DBHost                      string `env:"DB_HOST"`
	DBPort                      int    `env:"DB_PORT"`
	DBName                      string `env:"DB_NAME"`
	DBUser                      string `env:"DB_USER"`
	DBPassword                  Secret `env:"DB_PASSWORD"`
	PostgresUser                string `env:"POSTGRES_USER"`
	PostgresPassword            Secret `env:"POSTGRES_PASSWORD"`
	MigrationDBUser             string `env:"MIGRATION_DB_USER"`
	MigrationDBPassword         Secret `env:"MIGRATION_DB_PASSWORD"`
	DBSSLMode                   string `env:"DB_SSLMODE" envDefault:"require"`
	CSRFAuthKeyB64              Secret `env:"CSRF_AUTH_KEY_B64"`
	EmailVerificationHMACKeyB64 Secret `env:"EMAIL_VERIFICATION_HMAC_KEY_B64"`
	TurnstileSiteKey            string `env:"TURNSTILE_SITE_KEY"`
	TurnstileSecretKey          Secret `env:"TURNSTILE_SECRET_KEY"`

	SMTPHost        string        `env:"SMTP_HOST"`
	SMTPPort        int           `env:"SMTP_PORT" envDefault:"587"`
	SMTPUsername    string        `env:"SMTP_USERNAME"`
	SMTPPassword    Secret        `env:"SMTP_PASSWORD"`
	SMTPFromAddress string        `env:"SMTP_FROM_ADDRESS"`
	SMTPFromName    string        `env:"SMTP_FROM_NAME" envDefault:"MyCFC"`
	SMTPTLSMode     string        `env:"SMTP_TLS_MODE" envDefault:"starttls"`
	SMTPTimeout     time.Duration `env:"SMTP_TIMEOUT" envDefault:"10s"`

	AWSRegion        string `env:"AWS_REGION" envDefault:"eu-west-1"`
	S3BucketName     string `env:"S3_BUCKET_NAME"`
	S3Endpoint       string `env:"S3_ENDPOINT"`
	S3ForcePathStyle bool   `env:"S3_FORCE_PATH_STYLE" envDefault:"false"`

	GalleryURL string `env:"GALLERY_URL,required"`

	ConsentTermsVersion string `env:"CONSENT_TERMS_VERSION"`
	ConsentTermsSHA256  string `env:"CONSENT_TERMS_SHA256"`
	ConsentTermsURL     string `env:"CONSENT_TERMS_URL"`
	ConsentImageVersion string `env:"CONSENT_IMAGE_VERSION"`
	ConsentImageSHA256  string `env:"CONSENT_IMAGE_SHA256"`
	ConsentImageURL     string `env:"CONSENT_IMAGE_URL"`
	ConsentMinorVersion string `env:"CONSENT_MINOR_VERSION"`
	ConsentMinorSHA256  string `env:"CONSENT_MINOR_SHA256"`
	ConsentMinorURL     string `env:"CONSENT_MINOR_URL"`

	LogLevel string `env:"LOG_LEVEL" envDefault:"INFO"`

	DBMaxConns             int32         `env:"DB_MAX_CONNS" envDefault:"8"`
	DBMinConns             int32         `env:"DB_MIN_CONNS" envDefault:"1"`
	DBMaxConnLifetime      time.Duration `env:"DB_MAX_CONN_LIFETIME" envDefault:"30m"`
	DBMaxConnIdleTime      time.Duration `env:"DB_MAX_CONN_IDLE_TIME" envDefault:"5m"`
	DBHealthCheckPeriod    time.Duration `env:"DB_HEALTH_CHECK_PERIOD" envDefault:"30s"`
	SessionLifetime        time.Duration `env:"SESSION_LIFETIME" envDefault:"12h"`
	SessionIdleTimeout     time.Duration `env:"SESSION_IDLE_TIMEOUT" envDefault:"30m"`
	MaxRequestBytes        int64         `env:"MAX_REQUEST_BYTES" envDefault:"12582912"`
	MaxPhotoBytes          int64         `env:"MAX_PHOTO_BYTES" envDefault:"10485760"`
	HTTPReadHeaderTimeout  time.Duration `env:"HTTP_READ_HEADER_TIMEOUT" envDefault:"5s"`
	HTTPReadTimeout        time.Duration `env:"HTTP_READ_TIMEOUT" envDefault:"15s"`
	HTTPWriteTimeout       time.Duration `env:"HTTP_WRITE_TIMEOUT" envDefault:"30s"`
	HTTPIdleTimeout        time.Duration `env:"HTTP_IDLE_TIMEOUT" envDefault:"60s"`
	ShutdownTimeout        time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"20s"`
	ReleaseCheckTimeout    time.Duration `env:"RELEASE_CHECK_TIMEOUT" envDefault:"3s"`
	ReleaseCheckCacheTTL   time.Duration `env:"RELEASE_CHECK_CACHE_TTL" envDefault:"15m"`
	CookieDomain           string        `env:"COOKIE_DOMAIN"`
	TrustedProxyCIDRValues []string      `env:"TRUSTED_PROXY_CIDRS" envSeparator:","`
}

func Load(ctx context.Context) (Config, error) {
	cfg, err := env.ParseAs[Config]()
	if err != nil {
		return Config{}, fmt.Errorf("parse configuration: %w", err)
	}
	cfg.applyNonProductionEmailDefaults()
	if cfg.IsProduction() {
		if err := cfg.loadProductionParameters(ctx); err != nil {
			return Config{}, err
		}
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) loadProductionParameters(ctx context.Context) error {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(c.AWSRegion))
	if err != nil {
		return fmt.Errorf("load AWS configuration for SSM: %w", err)
	}

	type secretResult struct {
		values map[string]string
		err    error
	}
	secretResults := make(chan secretResult, 1)
	go func() {
		values, loadErr := loadProductionSecrets(ctx, secretsmanager.NewFromConfig(awsCfg))
		secretResults <- secretResult{values: values, err: loadErr}
	}()

	parameters, parameterErr := loadProductionParameterValues(ctx, ssm.NewFromConfig(awsCfg))
	secrets := <-secretResults
	if parameterErr != nil {
		return parameterErr
	}
	if secrets.err != nil {
		return secrets.err
	}
	return c.applyProductionRemoteConfig(parameters, secrets.values)
}

type parameterGetter interface {
	GetParameters(context.Context, *ssm.GetParametersInput, ...func(*ssm.Options)) (*ssm.GetParametersOutput, error)
}

func loadProductionParameterValues(ctx context.Context, client parameterGetter) (map[string]string, error) {
	fieldByName := make(map[string]string, len(productionParameterNames))
	names := make([]string, 0, len(productionParameterNames))
	for field, name := range productionParameterNames {
		fieldByName[name] = field
		names = append(names, name)
	}
	sort.Strings(names)

	type batchResult struct {
		output *ssm.GetParametersOutput
		names  []string
		err    error
	}
	const batchSize = 10
	batchCount := (len(names) + batchSize - 1) / batchSize
	results := make(chan batchResult, batchCount)
	for start := 0; start < len(names); start += batchSize {
		end := min(start+batchSize, len(names))
		batch := slices.Clone(names[start:end])
		go func() {
			output, loadErr := client.GetParameters(ctx, &ssm.GetParametersInput{Names: batch})
			results <- batchResult{output: output, names: batch, err: loadErr}
		}()
	}

	parameters := make(map[string]string, len(productionParameterNames))
	for range batchCount {
		result := <-results
		if result.err != nil {
			return nil, fmt.Errorf("load production parameters from SSM: %w", result.err)
		}
		if len(result.output.InvalidParameters) > 0 {
			return nil, fmt.Errorf("load production parameters from SSM: parameters not found: %s", strings.Join(result.output.InvalidParameters, ", "))
		}
		for _, parameter := range result.output.Parameters {
			name := awsStringValue(parameter.Name)
			field, ok := fieldByName[name]
			if !ok || parameter.Value == nil {
				return nil, fmt.Errorf("load production parameters from SSM: invalid response for %s", name)
			}
			parameters[field] = *parameter.Value
		}
	}
	if len(parameters) != len(productionParameterNames) {
		return nil, fmt.Errorf("load production parameters from SSM: received %d of %d values", len(parameters), len(productionParameterNames))
	}
	return parameters, nil
}

type secretGetter interface {
	GetSecretValue(context.Context, *secretsmanager.GetSecretValueInput, ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error)
}

func loadProductionSecrets(ctx context.Context, client secretGetter) (map[string]string, error) {
	result, err := client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: awsString(productionSecretID)})
	if err != nil {
		return nil, fmt.Errorf("load production secret %s: %w", productionSecretID, err)
	}
	if result.SecretString == nil {
		return nil, fmt.Errorf("load production secret %s: empty SecretString", productionSecretID)
	}
	var secrets map[string]string
	if err := json.Unmarshal([]byte(*result.SecretString), &secrets); err != nil {
		return nil, fmt.Errorf("parse production secret %s JSON: %w", productionSecretID, err)
	}
	return secrets, nil
}

func awsString(value string) *string { return &value }

func awsStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (c *Config) applyProductionRemoteConfig(parameters, secrets map[string]string) error {
	var problems Problems
	required := make([]string, 0, len(productionParameterNames))
	for field := range productionParameterNames {
		required = append(required, field)
	}
	slices.Sort(required)
	for _, field := range required {
		if strings.TrimSpace(parameters[field]) == "" {
			problems.Add(field, "production SSM parameter must not be empty")
		}
	}
	for _, field := range productionSecretFields {
		if strings.TrimSpace(secrets[field]) == "" {
			problems.Add(field, "production Secrets Manager value must not be empty")
		}
	}
	if err := problems.Err(); err != nil {
		return err
	}

	c.DatabaseURL = ""
	c.BaseURL = parameters["BASE_URL"]
	c.DBHost = parameters["DB_HOST"]
	c.DBName = parameters["DB_NAME"]
	c.DBUser = parameters["DB_USER"]
	c.DBPassword = Secret(secrets["APP_DB_PASSWORD"])
	c.PostgresUser = parameters["POSTGRES_USER"]
	c.PostgresPassword = Secret(secrets["POSTGRES_PASSWORD"])
	c.MigrationDBUser = parameters["MIGRATION_DB_USER"]
	c.MigrationDBPassword = Secret(secrets["MIGRATION_DB_PASSWORD"])
	c.DBSSLMode = parameters["DB_SSLMODE"]
	c.CSRFAuthKeyB64 = Secret(secrets["CSRF_AUTH_KEY_B64"])
	c.EmailVerificationHMACKeyB64 = Secret(secrets["EMAIL_VERIFICATION_HMAC_KEY_B64"])
	c.TurnstileSiteKey = parameters["TURNSTILE_SITE_KEY"]
	c.TurnstileSecretKey = Secret(secrets["TURNSTILE_SECRET_KEY"])
	c.SMTPHost = parameters["SMTP_HOST"]
	c.SMTPUsername = secrets["SMTP_USERNAME"]
	c.SMTPPassword = Secret(secrets["SMTP_PASSWORD"])
	c.SMTPFromAddress = parameters["SMTP_FROM_ADDRESS"]
	c.SMTPFromName = parameters["SMTP_FROM_NAME"]
	c.SMTPTLSMode = parameters["SMTP_TLS_MODE"]
	c.S3BucketName = parameters["S3_BUCKET_NAME"]
	c.S3Endpoint = ""
	c.GalleryURL = parameters["GALLERY_URL"]
	c.ConsentTermsVersion = parameters["CONSENT_TERMS_VERSION"]
	c.ConsentTermsSHA256 = parameters["CONSENT_TERMS_SHA256"]
	c.ConsentTermsURL = parameters["CONSENT_TERMS_URL"]
	c.ConsentImageVersion = parameters["CONSENT_IMAGE_VERSION"]
	c.ConsentImageSHA256 = parameters["CONSENT_IMAGE_SHA256"]
	c.ConsentImageURL = parameters["CONSENT_IMAGE_URL"]
	c.ConsentMinorVersion = parameters["CONSENT_MINOR_VERSION"]
	c.ConsentMinorSHA256 = parameters["CONSENT_MINOR_SHA256"]
	c.ConsentMinorURL = parameters["CONSENT_MINOR_URL"]
	c.LogLevel = parameters["LOG_LEVEL"]
	c.ReleaseRepository = parameters["RELEASE_REPOSITORY"]
	c.CookieDomain = ""
	c.TrustedProxyCIDRValues = splitCSV(parameters["TRUSTED_PROXY_CIDRS"])

	assignInt := func(field string, target *int) {
		value, err := parsePositiveInt(parameters[field])
		if err != nil {
			problems.Add(field, err.Error())
			return
		}
		*target = value
	}
	assignInt32 := func(field string, target *int32) {
		value, err := parsePositiveInt(parameters[field])
		if err != nil {
			problems.Add(field, err.Error())
			return
		}
		if value > int(^uint32(0)>>1) {
			problems.Add(field, "production SSM parameter is too large")
			return
		}
		*target = int32(value)
	}
	assignInt64 := func(field string, target *int64) {
		value, err := parsePositiveInt(parameters[field])
		if err != nil {
			problems.Add(field, err.Error())
			return
		}
		*target = int64(value)
	}
	assignDuration := func(field string, target *time.Duration) {
		value, err := time.ParseDuration(parameters[field])
		if err != nil {
			problems.Add(field, "production SSM parameter must be a valid duration")
			return
		}
		*target = value
	}
	assignBool := func(field string, target *bool) {
		value, err := strconv.ParseBool(parameters[field])
		if err != nil {
			problems.Add(field, "production SSM parameter must be a boolean")
			return
		}
		*target = value
	}
	assignInt("DB_PORT", &c.DBPort)
	assignInt("SMTP_PORT", &c.SMTPPort)
	assignDuration("SMTP_TIMEOUT", &c.SMTPTimeout)
	assignBool("S3_FORCE_PATH_STYLE", &c.S3ForcePathStyle)
	assignInt32("DB_MAX_CONNS", &c.DBMaxConns)
	assignInt32("DB_MIN_CONNS", &c.DBMinConns)
	assignDuration("DB_MAX_CONN_LIFETIME", &c.DBMaxConnLifetime)
	assignDuration("DB_MAX_CONN_IDLE_TIME", &c.DBMaxConnIdleTime)
	assignDuration("DB_HEALTH_CHECK_PERIOD", &c.DBHealthCheckPeriod)
	assignDuration("SESSION_LIFETIME", &c.SessionLifetime)
	assignDuration("SESSION_IDLE_TIMEOUT", &c.SessionIdleTimeout)
	assignInt64("MAX_REQUEST_BYTES", &c.MaxRequestBytes)
	assignInt64("MAX_PHOTO_BYTES", &c.MaxPhotoBytes)
	assignDuration("HTTP_READ_HEADER_TIMEOUT", &c.HTTPReadHeaderTimeout)
	assignDuration("HTTP_READ_TIMEOUT", &c.HTTPReadTimeout)
	assignDuration("HTTP_WRITE_TIMEOUT", &c.HTTPWriteTimeout)
	assignDuration("HTTP_IDLE_TIMEOUT", &c.HTTPIdleTimeout)
	assignDuration("SHUTDOWN_TIMEOUT", &c.ShutdownTimeout)
	assignDuration("RELEASE_CHECK_TIMEOUT", &c.ReleaseCheckTimeout)
	assignDuration("RELEASE_CHECK_CACHE_TTL", &c.ReleaseCheckCacheTTL)
	return problems.Err()
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			values = append(values, part)
		}
	}
	return values
}

func parsePositiveInt(raw string) (int, error) {
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("production SSM parameter must be an integer")
	}
	if value < 1 {
		return 0, fmt.Errorf("production SSM parameter must be positive")
	}
	return value, nil
}

func (c *Config) applyNonProductionEmailDefaults() {
	if c.IsProduction() {
		return
	}
	if c.EmailVerificationHMACKeyB64.Value() == "" {
		c.EmailVerificationHMACKeyB64 = Secret("MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	}
	if strings.TrimSpace(c.SMTPHost) == "" {
		c.SMTPHost = "localhost"
		c.SMTPPort = 1025
		c.SMTPTLSMode = "none"
	}
	if strings.TrimSpace(c.SMTPFromAddress) == "" {
		c.SMTPFromAddress = "mycfc@example.test"
	}
}

func (c Config) IsProduction() bool { return c.AppEnv == "production" }

func (c Config) HTTPAddress() string { return fmt.Sprintf(":%d", c.Port) }

// ResolvedDatabaseURL supports local URLs and production component secrets.
func (c Config) ResolvedDatabaseURL() (string, error) {
	if c.DatabaseURL.Value() != "" {
		return c.DatabaseURL.Value(), nil
	}
	return c.resolvedDatabaseURL(c.DBUser, c.DBPassword)
}

func (c Config) BootstrapDatabaseURL() (string, error) {
	return c.resolvedDatabaseURL(c.PostgresUser, c.PostgresPassword)
}

func (c Config) MigrationDatabaseURL() (string, error) {
	return c.resolvedDatabaseURL(c.MigrationDBUser, c.MigrationDBPassword)
}

func (c Config) resolvedDatabaseURL(username string, password Secret) (string, error) {
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(username, password.Value()),
		Host:   net.JoinHostPort(c.DBHost, fmt.Sprintf("%d", c.DBPort)),
		Path:   c.DBName,
	}
	query := u.Query()
	query.Set("sslmode", c.DBSSLMode)
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func (c Config) CSRFAuthKey() ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(c.CSRFAuthKeyB64.Value())
	if err != nil {
		return nil, errors.New("CSRF_AUTH_KEY_B64 is not valid base64")
	}
	if len(decoded) != 32 {
		return nil, errors.New("CSRF_AUTH_KEY_B64 must decode to exactly 32 bytes")
	}
	return decoded, nil
}

func (c Config) EmailVerificationHMACKey() ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(c.EmailVerificationHMACKeyB64.Value())
	if err != nil {
		return nil, errors.New("EMAIL_VERIFICATION_HMAC_KEY_B64 is not valid base64")
	}
	if len(decoded) != 32 {
		return nil, errors.New("EMAIL_VERIFICATION_HMAC_KEY_B64 must decode to exactly 32 bytes")
	}
	return decoded, nil
}

func (c Config) TrustedProxyCIDRs() ([]netip.Prefix, error) {
	prefixes := make([]netip.Prefix, 0, len(c.TrustedProxyCIDRValues))
	for _, raw := range c.TrustedProxyCIDRValues {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy CIDR %q: %w", raw, err)
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	return prefixes, nil
}

func (c Config) Validate() error {
	var problems Problems

	if !slices.Contains([]string{"local", "test", "production"}, c.AppEnv) {
		problems.Add("APP_ENV", "must be local, test or production")
	}
	if strings.TrimSpace(c.AppVersion) == "" {
		problems.Add("APP_VERSION", "must not be empty")
	}
	if strings.TrimSpace(c.AppReleasedAt) != "" {
		if _, err := time.Parse(time.RFC3339, c.AppReleasedAt); err != nil {
			problems.Add("APP_RELEASED_AT", "must be RFC3339 when set")
		}
	}
	if c.IsProduction() && !lowerHex40.MatchString(c.GITSHA) {
		problems.Add("GIT_SHA", "must be 40 lowercase hexadecimal characters in production")
	}
	if !validRepositoryName(c.ReleaseRepository) {
		problems.Add("RELEASE_REPOSITORY", "must be owner/repository")
	}
	if c.Port < 1 || c.Port > 65535 {
		problems.Add("PORT", "must be between 1 and 65535")
	}

	base, baseErr := validateAbsoluteURL(c.BaseURL, c.IsProduction(), true)
	if baseErr != nil {
		problems.Add("BASE_URL", baseErr.Error())
	}

	components := []string{c.DBHost, c.DBName, c.DBUser, c.DBPassword.Value()}
	usingURL := strings.TrimSpace(c.DatabaseURL.Value()) != ""
	usingComponents := c.DBPort != 0 || slices.ContainsFunc(components, func(value string) bool { return strings.TrimSpace(value) != "" })
	if usingURL && usingComponents {
		problems.Add("DATABASE_URL", "must not be set with DB_HOST, DB_PORT, DB_NAME, DB_USER, or DB_PASSWORD")
	}
	if !usingURL && (!usingComponents || c.DBPort < 1 || c.DBPort > 65535 || slices.ContainsFunc(components, func(value string) bool { return strings.TrimSpace(value) == "" })) {
		problems.Add("DATABASE_URL", "or complete DB_HOST, DB_PORT, DB_NAME, DB_USER, and DB_PASSWORD configuration is required")
	}
	if usingComponents && !slices.Contains([]string{"disable", "allow", "prefer", "require", "verify-ca", "verify-full"}, c.DBSSLMode) {
		problems.Add("DB_SSLMODE", "must be a supported PostgreSQL SSL mode")
	}
	if usingURL {
		if err := validateDatabaseURL(c.DatabaseURL.Value()); err != nil {
			problems.Add("DATABASE_URL", err.Error())
		}
	}
	if _, err := c.CSRFAuthKey(); err != nil {
		problems.Add("CSRF_AUTH_KEY_B64", err.Error())
	}
	if _, err := c.EmailVerificationHMACKey(); err != nil {
		problems.Add("EMAIL_VERIFICATION_HMAC_KEY_B64", err.Error())
	}
	if (strings.TrimSpace(c.TurnstileSiteKey) == "") != (strings.TrimSpace(c.TurnstileSecretKey.Value()) == "") {
		problems.Add("TURNSTILE_SITE_KEY", "and TURNSTILE_SECRET_KEY must either both be set or both be empty")
	}
	if c.IsProduction() && strings.TrimSpace(c.TurnstileSiteKey) == "" {
		problems.Add("TURNSTILE_SITE_KEY", "and TURNSTILE_SECRET_KEY are required in production")
	}
	if strings.TrimSpace(c.SMTPHost) == "" {
		problems.Add("SMTP_HOST", "must not be empty")
	}
	if c.SMTPPort < 1 || c.SMTPPort > 65535 {
		problems.Add("SMTP_PORT", "must be between 1 and 65535")
	}
	if (c.SMTPUsername == "") != (c.SMTPPassword.Value() == "") {
		problems.Add("SMTP_USERNAME", "and SMTP_PASSWORD must either both be set or both be empty")
	}
	if address, err := mail.ParseAddress(c.SMTPFromAddress); err != nil || address.Address != c.SMTPFromAddress {
		problems.Add("SMTP_FROM_ADDRESS", "must be a plain valid email address")
	}
	if strings.TrimSpace(c.SMTPFromName) == "" {
		problems.Add("SMTP_FROM_NAME", "must not be empty")
	}
	if !slices.Contains([]string{"starttls", "implicit", "none"}, c.SMTPTLSMode) {
		problems.Add("SMTP_TLS_MODE", "must be starttls, implicit or none")
	} else if c.IsProduction() && c.SMTPTLSMode == "none" {
		problems.Add("SMTP_TLS_MODE", "must use TLS in production")
	}
	if c.SMTPTimeout <= 0 {
		problems.Add("SMTP_TIMEOUT", "must be greater than zero")
	}
	if strings.TrimSpace(c.AWSRegion) == "" {
		problems.Add("AWS_REGION", "must not be empty")
	}
	if err := validateBucketName(c.S3BucketName); err != nil {
		problems.Add("S3_BUCKET_NAME", err.Error())
	}
	if c.IsProduction() {
		if strings.TrimSpace(c.S3Endpoint) != "" {
			problems.Add("S3_ENDPOINT", "must be empty in production")
		}
		if c.S3ForcePathStyle {
			problems.Add("S3_FORCE_PATH_STYLE", "must be false in production")
		}
	} else if c.S3Endpoint != "" {
		if _, err := validateAbsoluteURL(c.S3Endpoint, false, false); err != nil {
			problems.Add("S3_ENDPOINT", err.Error())
		}
	}

	if _, err := validateAbsoluteURL(c.GalleryURL, c.IsProduction(), true); err != nil {
		problems.Add("GALLERY_URL", err.Error())
	}
	if c.IsProduction() {
		u, err := url.Parse(c.GalleryURL)
		if err == nil && strings.HasSuffix(strings.ToLower(u.Hostname()), ".invalid") {
			problems.Add("GALLERY_URL", "must not use a reserved .invalid host in production")
		}
	}

	validateConsent := func(versionField, version, hashField, hash string) {
		if strings.TrimSpace(version) == "" {
			problems.Add(versionField, "must not be empty")
		}
		if !lowerHex64.MatchString(hash) {
			problems.Add(hashField, "must be 64 lowercase hexadecimal characters")
		}
		if c.IsProduction() && hash == strings.Repeat("0", 64) {
			problems.Add(hashField, "must not be the local zero hash in production")
		}
	}
	validateConsent("CONSENT_TERMS_VERSION", c.ConsentTermsVersion, "CONSENT_TERMS_SHA256", c.ConsentTermsSHA256)
	validateConsent("CONSENT_IMAGE_VERSION", c.ConsentImageVersion, "CONSENT_IMAGE_SHA256", c.ConsentImageSHA256)
	validateConsent("CONSENT_MINOR_VERSION", c.ConsentMinorVersion, "CONSENT_MINOR_SHA256", c.ConsentMinorSHA256)
	for field, value := range map[string]string{
		"CONSENT_TERMS_URL": c.ConsentTermsURL,
		"CONSENT_IMAGE_URL": c.ConsentImageURL,
		"CONSENT_MINOR_URL": c.ConsentMinorURL,
	} {
		if _, err := validateAbsoluteURL(value, c.IsProduction(), true); err != nil {
			problems.Add(field, err.Error())
		}
	}

	if !slices.Contains([]string{"DEBUG", "INFO", "WARN", "ERROR"}, strings.ToUpper(c.LogLevel)) {
		problems.Add("LOG_LEVEL", "must be DEBUG, INFO, WARN or ERROR")
	}
	if c.DBMaxConns < 1 {
		problems.Add("DB_MAX_CONNS", "must be at least 1")
	}
	if c.DBMinConns < 0 || c.DBMinConns > c.DBMaxConns {
		problems.Add("DB_MIN_CONNS", "must be between 0 and DB_MAX_CONNS")
	}
	for field, value := range map[string]time.Duration{
		"DB_MAX_CONN_LIFETIME":     c.DBMaxConnLifetime,
		"DB_MAX_CONN_IDLE_TIME":    c.DBMaxConnIdleTime,
		"DB_HEALTH_CHECK_PERIOD":   c.DBHealthCheckPeriod,
		"SESSION_LIFETIME":         c.SessionLifetime,
		"SESSION_IDLE_TIMEOUT":     c.SessionIdleTimeout,
		"HTTP_READ_HEADER_TIMEOUT": c.HTTPReadHeaderTimeout,
		"HTTP_READ_TIMEOUT":        c.HTTPReadTimeout,
		"HTTP_WRITE_TIMEOUT":       c.HTTPWriteTimeout,
		"HTTP_IDLE_TIMEOUT":        c.HTTPIdleTimeout,
		"SHUTDOWN_TIMEOUT":         c.ShutdownTimeout,
		"RELEASE_CHECK_TIMEOUT":    c.ReleaseCheckTimeout,
		"RELEASE_CHECK_CACHE_TTL":  c.ReleaseCheckCacheTTL,
	} {
		if value <= 0 {
			problems.Add(field, "must be greater than zero")
		}
	}
	if c.SessionIdleTimeout > c.SessionLifetime {
		problems.Add("SESSION_IDLE_TIMEOUT", "must not exceed SESSION_LIFETIME")
	}
	if c.MaxPhotoBytes < 1 {
		problems.Add("MAX_PHOTO_BYTES", "must be greater than zero")
	}
	if c.MaxRequestBytes <= c.MaxPhotoBytes {
		problems.Add("MAX_REQUEST_BYTES", "must be greater than MAX_PHOTO_BYTES")
	}

	if _, err := c.TrustedProxyCIDRs(); err != nil {
		problems.Add("TRUSTED_PROXY_CIDRS", err.Error())
	}
	if c.IsProduction() && len(c.TrustedProxyCIDRValues) == 0 {
		problems.Add("TRUSTED_PROXY_CIDRS", "must be configured in production")
	}

	if c.CookieDomain != "" && baseErr == nil {
		if err := validateCookieDomain(c.CookieDomain, base.Hostname()); err != nil {
			problems.Add("COOKIE_DOMAIN", err.Error())
		}
	}

	return problems.Err()
}

func validRepositoryName(value string) bool {
	owner, repo, ok := strings.Cut(strings.TrimSpace(value), "/")
	return ok && owner != "" && repo != "" && !strings.Contains(repo, "/")
}

func validateDatabaseURL(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return errors.New("must not be empty")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" {
		return errors.New("must be a valid PostgreSQL URL")
	}
	return nil
}

func validateAbsoluteURL(raw string, requireHTTPS, allowHTTP bool) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !u.IsAbs() || u.Host == "" {
		return nil, errors.New("must be an absolute URL")
	}
	if u.User != nil {
		return nil, errors.New("must not contain user information")
	}
	if requireHTTPS && u.Scheme != "https" {
		return nil, errors.New("must use HTTPS in production")
	}
	if !requireHTTPS && !allowHTTP && u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("must use HTTP or HTTPS")
	}
	if allowHTTP && u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("must use HTTP or HTTPS")
	}
	return u, nil
}

func validateBucketName(value string) error {
	if !bucketName.MatchString(value) || strings.Contains(value, "..") || net.ParseIP(value) != nil {
		return errors.New("must be a DNS-compatible S3 bucket name")
	}
	return nil
}

func validateCookieDomain(raw, baseHost string) error {
	domain := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(raw)), ".")
	host := strings.ToLower(baseHost)
	if domain == "" {
		return nil
	}
	if host == domain || strings.HasSuffix(host, "."+domain) {
		return nil
	}
	return errors.New("must equal the base URL host or a parent domain")
}
