package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

var (
	lowerHex40 = regexp.MustCompile(`^[0-9a-f]{40}$`)
	lowerHex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)
	bucketName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]{1,61}[a-z0-9])?$`)
)

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
	BaseURL           string `env:"BASE_URL,required"`

	DatabaseURL                 Secret `env:"DATABASE_URL"`
	DBHost                      string `env:"DB_HOST"`
	DBPort                      int    `env:"DB_PORT"`
	DBName                      string `env:"DB_NAME"`
	DBUser                      string `env:"DB_USER"`
	DBPassword                  Secret `env:"DB_PASSWORD"`
	DBSSLMode                   string `env:"DB_SSLMODE" envDefault:"require"`
	CSRFAuthKeyB64              Secret `env:"CSRF_AUTH_KEY_B64,required"`
	EmailVerificationHMACKeyB64 Secret `env:"EMAIL_VERIFICATION_HMAC_KEY_B64"`

	SMTPHost        string        `env:"SMTP_HOST"`
	SMTPPort        int           `env:"SMTP_PORT" envDefault:"587"`
	SMTPUsername    string        `env:"SMTP_USERNAME"`
	SMTPPassword    Secret        `env:"SMTP_PASSWORD"`
	SMTPFromAddress string        `env:"SMTP_FROM_ADDRESS"`
	SMTPFromName    string        `env:"SMTP_FROM_NAME" envDefault:"MyCFC"`
	SMTPTLSMode     string        `env:"SMTP_TLS_MODE" envDefault:"starttls"`
	SMTPTimeout     time.Duration `env:"SMTP_TIMEOUT" envDefault:"10s"`

	AWSRegion        string `env:"AWS_REGION,required"`
	S3BucketName     string `env:"S3_BUCKET_NAME,required"`
	S3Endpoint       string `env:"S3_ENDPOINT"`
	S3ForcePathStyle bool   `env:"S3_FORCE_PATH_STYLE" envDefault:"false"`

	GalleryURL            string `env:"GALLERY_URL,required"`

	ConsentTermsVersion string `env:"CONSENT_TERMS_VERSION,required"`
	ConsentTermsSHA256  string `env:"CONSENT_TERMS_SHA256,required"`
	ConsentTermsURL     string `env:"CONSENT_TERMS_URL,required"`
	ConsentImageVersion string `env:"CONSENT_IMAGE_VERSION,required"`
	ConsentImageSHA256  string `env:"CONSENT_IMAGE_SHA256,required"`
	ConsentImageURL     string `env:"CONSENT_IMAGE_URL,required"`
	ConsentMinorVersion string `env:"CONSENT_MINOR_VERSION,required"`
	ConsentMinorSHA256  string `env:"CONSENT_MINOR_SHA256,required"`
	ConsentMinorURL     string `env:"CONSENT_MINOR_URL,required"`

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

func Load() (Config, error) {
	cfg, err := env.ParseAs[Config]()
	if err != nil {
		return Config{}, fmt.Errorf("parse configuration: %w", err)
	}
	cfg.applyNonProductionEmailDefaults()
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
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
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(c.DBUser, c.DBPassword.Value()),
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
