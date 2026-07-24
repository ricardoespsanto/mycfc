package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
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
	AppEnv     string `env:"APP_ENV,required"`
	AppVersion string `env:"APP_VERSION,required"`
	GITSHA     string `env:"GIT_SHA,required"`
	Port       int    `env:"PORT" envDefault:"8080"`
	BaseURL    string `env:"BASE_URL,required"`

	DatabaseURL    Secret `env:"DATABASE_URL,required"`
	CSRFAuthKeyB64 Secret `env:"CSRF_AUTH_KEY_B64,required"`

	AWSRegion        string `env:"AWS_REGION,required"`
	S3BucketName     string `env:"S3_BUCKET_NAME,required"`
	S3Endpoint       string `env:"S3_ENDPOINT"`
	S3ForcePathStyle bool   `env:"S3_FORCE_PATH_STYLE" envDefault:"false"`

	GoogleCalendarAPIKey  string `env:"GOOGLE_CALENDAR_API_KEY,required"`
	CalendarCompetitionID string `env:"CALENDAR_COMPETITION_ID,required"`
	CalendarTrainingID    string `env:"CALENDAR_TRAINING_ID,required"`
	CalendarSocialID      string `env:"CALENDAR_SOCIAL_ID,required"`
	CalendarCleanupsID    string `env:"CALENDAR_CLEANUPS_ID,required"`
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
	CookieDomain           string        `env:"COOKIE_DOMAIN"`
	TrustedProxyCIDRValues []string      `env:"TRUSTED_PROXY_CIDRS" envSeparator:","`
}

func Load() (Config, error) {
	cfg, err := env.ParseAs[Config]()
	if err != nil {
		return Config{}, fmt.Errorf("parse configuration: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) IsProduction() bool { return c.AppEnv == "production" }

func (c Config) HTTPAddress() string { return fmt.Sprintf(":%d", c.Port) }

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
	if c.IsProduction() && !lowerHex40.MatchString(c.GITSHA) {
		problems.Add("GIT_SHA", "must be 40 lowercase hexadecimal characters in production")
	}
	if c.Port < 1 || c.Port > 65535 {
		problems.Add("PORT", "must be between 1 and 65535")
	}

	base, baseErr := validateAbsoluteURL(c.BaseURL, c.IsProduction(), true)
	if baseErr != nil {
		problems.Add("BASE_URL", baseErr.Error())
	}

	if err := validateDatabaseURL(c.DatabaseURL.Value()); err != nil {
		problems.Add("DATABASE_URL", err.Error())
	}
	if _, err := c.CSRFAuthKey(); err != nil {
		problems.Add("CSRF_AUTH_KEY_B64", err.Error())
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

	for field, value := range map[string]string{
		"GOOGLE_CALENDAR_API_KEY": c.GoogleCalendarAPIKey,
		"CALENDAR_COMPETITION_ID": c.CalendarCompetitionID,
		"CALENDAR_TRAINING_ID":    c.CalendarTrainingID,
		"CALENDAR_SOCIAL_ID":      c.CalendarSocialID,
		"CALENDAR_CLEANUPS_ID":    c.CalendarCleanupsID,
	} {
		if strings.TrimSpace(value) == "" {
			problems.Add(field, "must not be empty")
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
