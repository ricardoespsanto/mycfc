// Package activity defines the provider-neutral boundary for wearable activity
// integrations. Provider implementations translate their APIs into these types;
// persistence and training-session matching remain owned by MyCFCoimbra.
package activity

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

var providerCode = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,39}$`)

var (
	ErrUnsupported = errors.New("activity provider capability is unsupported")
	ErrDuplicate   = errors.New("activity provider is already registered")
)

// Provider is a stable, lower-case storage key such as polar, garmin, strava,
// or whoop. It is deliberately not a closed enum so adding a provider does not
// require changing shared code or a database type.
type Provider string

func ParseProvider(value string) (Provider, error) {
	if !providerCode.MatchString(value) {
		return "", fmt.Errorf("provider must match %s", providerCode.String())
	}
	return Provider(value), nil
}

type Capabilities struct {
	RecentSync bool
	Backfill   bool
	Webhooks   bool
	Disconnect bool
}

// Secret contains decrypted provider credentials only while an adapter call is
// in progress. Its formatting methods are intentionally redacted.
type Secret struct{ value []byte }

func NewSecret(value []byte) Secret {
	return Secret{value: slices.Clone(value)}
}

func (Secret) String() string   { return "[REDACTED]" }
func (Secret) GoString() string { return "[REDACTED]" }
func (s Secret) Bytes() []byte  { return slices.Clone(s.value) }

// SealedCredentials is the only credential representation suitable for
// persistence. Ciphertext is opaque to the database and must be produced by a
// CredentialVault backed by an application-managed encryption key.
type SealedCredentials struct {
	Ciphertext []byte
	KeyID      string
	ExpiresAt  *time.Time
	Scopes     []string
}

func (c SealedCredentials) Validate() error {
	if len(c.Ciphertext) == 0 {
		return errors.New("credential ciphertext must not be empty")
	}
	if strings.TrimSpace(c.KeyID) == "" || len(c.KeyID) > 120 {
		return errors.New("credential key id must contain 1 to 120 characters")
	}
	for _, scope := range c.Scopes {
		if strings.TrimSpace(scope) == "" {
			return errors.New("credential scopes must not contain empty values")
		}
	}
	return nil
}

type CredentialVault interface {
	Seal(ctx context.Context, provider Provider, userID string, credentials Secret) (SealedCredentials, error)
	Open(ctx context.Context, provider Provider, userID string, credentials SealedCredentials) (Secret, error)
}

type ConnectionStatus struct {
	Connected               bool
	RequiresReauthorization bool
	RemoteUserID            string
}

type SyncRequest struct {
	Since      time.Time
	Until      time.Time
	Checkpoint string
	Limit      int
}

func (r SyncRequest) Validate() error {
	if r.Since.IsZero() || r.Until.IsZero() || !r.Since.Before(r.Until) {
		return errors.New("sync window must have a start before its end")
	}
	if r.Limit < 1 || r.Limit > 1000 {
		return errors.New("sync limit must be between 1 and 1000")
	}
	return nil
}

type NormalizedActivity struct {
	ProviderActivityID   string
	ProviderUpdatedAt    *time.Time
	StartsAt             time.Time
	EndsAt               time.Time
	Sport                string
	NormalizedSport      string
	DurationSeconds      int
	MovingSeconds        *int
	DistanceMetres       *float64
	AverageHeartRate     *int16
	MaximumHeartRate     *int16
	ProviderMetricsJSON  []byte
	RawSummaryJSON       []byte
	PayloadSHA256        [32]byte
	NormalizationVersion int
	DeletedAt            *time.Time
}

func (a NormalizedActivity) Validate() error {
	if strings.TrimSpace(a.ProviderActivityID) == "" || len(a.ProviderActivityID) > 255 {
		return errors.New("provider activity id must contain 1 to 255 characters")
	}
	if a.StartsAt.IsZero() || a.EndsAt.IsZero() || !a.StartsAt.Before(a.EndsAt) {
		return errors.New("activity start must precede activity end")
	}
	if strings.TrimSpace(a.Sport) == "" || strings.TrimSpace(a.NormalizedSport) == "" {
		return errors.New("activity sport values must not be empty")
	}
	if a.DurationSeconds <= 0 || (a.MovingSeconds != nil && *a.MovingSeconds < 0) {
		return errors.New("activity durations must be positive")
	}
	if a.DistanceMetres != nil && *a.DistanceMetres < 0 {
		return errors.New("activity distance must not be negative")
	}
	if !validHeartRate(a.AverageHeartRate) || !validHeartRate(a.MaximumHeartRate) {
		return errors.New("activity heart rate must be between 20 and 260")
	}
	if a.AverageHeartRate != nil && a.MaximumHeartRate != nil && *a.AverageHeartRate > *a.MaximumHeartRate {
		return errors.New("average heart rate must not exceed maximum heart rate")
	}
	if a.NormalizationVersion < 1 {
		return errors.New("normalization version must be positive")
	}
	return nil
}

func validHeartRate(value *int16) bool {
	return value == nil || (*value >= 20 && *value <= 260)
}

type SyncPage struct {
	Activities     []NormalizedActivity
	NextCheckpoint string
	Complete       bool
}

type WebhookEnvelope struct {
	Headers map[string][]string
	Body    []byte
}

type WebhookEvent struct {
	ProviderEventID    string
	ProviderUserID     string
	ProviderActivityID string
	Kind               string
}

// Adapter owns provider protocol behavior. Implementations must be safe for
// concurrent use. Methods for unsupported capabilities return ErrUnsupported.
type Adapter interface {
	Provider() Provider
	Capabilities() Capabilities
	ConnectionStatus(ctx context.Context, credentials Secret) (ConnectionStatus, error)
	SyncRecent(ctx context.Context, credentials Secret, request SyncRequest) (SyncPage, error)
	Backfill(ctx context.Context, credentials Secret, request SyncRequest) (SyncPage, error)
	IngestWebhook(ctx context.Context, envelope WebhookEnvelope) ([]WebhookEvent, error)
	Disconnect(ctx context.Context, credentials Secret) error
}

type Registry struct {
	mu       sync.RWMutex
	adapters map[Provider]Adapter
}

func NewRegistry() *Registry {
	return &Registry{adapters: make(map[Provider]Adapter)}
}

func (r *Registry) Register(adapter Adapter) error {
	if adapter == nil {
		return errors.New("activity provider adapter must not be nil")
	}
	provider, err := ParseProvider(string(adapter.Provider()))
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.adapters[provider]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicate, provider)
	}
	r.adapters[provider] = adapter
	return nil
}

func (r *Registry) Get(provider Provider) (Adapter, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	adapter, ok := r.adapters[provider]
	return adapter, ok
}
