package activity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestProviderRegistryAcceptsExtensibleProviderCodes(t *testing.T) {
	registry := NewRegistry()
	for _, name := range []Provider{"polar", "garmin", "strava", "whoop"} {
		if err := registry.Register(stubAdapter{provider: name}); err != nil {
			t.Fatalf("Register(%q) error = %v", name, err)
		}
		if _, ok := registry.Get(name); !ok {
			t.Fatalf("provider %q was not registered", name)
		}
	}
	if err := registry.Register(stubAdapter{provider: "strava"}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate error = %v", err)
	}
	if err := registry.Register(stubAdapter{provider: "Garmin Connect"}); err == nil {
		t.Fatal("invalid provider code was accepted")
	}
}

func TestSecretsAndSealedCredentialsDoNotFormatPlaintext(t *testing.T) {
	secret := NewSecret([]byte("access-token refresh-token"))
	for _, rendered := range []string{fmt.Sprint(secret), fmt.Sprintf("%#v", secret)} {
		if strings.Contains(rendered, "access-token") {
			t.Fatalf("secret leaked in %q", rendered)
		}
	}
	sealed := SealedCredentials{Ciphertext: []byte("ciphertext"), KeyID: "activity-key-v1", Scopes: []string{"activity:read"}}
	if err := sealed.Validate(); err != nil {
		t.Fatal(err)
	}
	sealed.Ciphertext = nil
	if err := sealed.Validate(); err == nil {
		t.Fatal("empty ciphertext was accepted")
	}
}

func TestNormalizedActivityValidation(t *testing.T) {
	now := time.Now().UTC()
	distance := 5200.5
	average, maximum := int16(142), int16(181)
	activity := NormalizedActivity{
		ProviderActivityID: "remote-123", StartsAt: now, EndsAt: now.Add(time.Hour),
		Sport: "Kayaking", NormalizedSport: "paddling", DurationSeconds: 3600,
		DistanceMetres: &distance, AverageHeartRate: &average, MaximumHeartRate: &maximum,
		NormalizationVersion: 1,
	}
	if err := activity.Validate(); err != nil {
		t.Fatal(err)
	}
	activity.MaximumHeartRate = ptrInt16(120)
	if err := activity.Validate(); err == nil {
		t.Fatal("average heart rate above maximum was accepted")
	}
}

func TestSyncRequestRequiresBoundedWindow(t *testing.T) {
	now := time.Now().UTC()
	if err := (SyncRequest{Since: now.Add(-time.Hour), Until: now, Limit: 100}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (SyncRequest{Since: now, Until: now, Limit: 100}).Validate(); err == nil {
		t.Fatal("empty sync window was accepted")
	}
}

func ptrInt16(value int16) *int16 { return &value }

type stubAdapter struct{ provider Provider }

func (s stubAdapter) Provider() Provider       { return s.provider }
func (stubAdapter) Capabilities() Capabilities { return Capabilities{} }
func (stubAdapter) ConnectionStatus(context.Context, Secret) (ConnectionStatus, error) {
	return ConnectionStatus{}, ErrUnsupported
}
func (stubAdapter) SyncRecent(context.Context, Secret, SyncRequest) (SyncPage, error) {
	return SyncPage{}, ErrUnsupported
}
func (stubAdapter) Backfill(context.Context, Secret, SyncRequest) (SyncPage, error) {
	return SyncPage{}, ErrUnsupported
}
func (stubAdapter) IngestWebhook(context.Context, WebhookEnvelope) ([]WebhookEvent, error) {
	return nil, ErrUnsupported
}
func (stubAdapter) Disconnect(context.Context, Secret) error { return ErrUnsupported }
