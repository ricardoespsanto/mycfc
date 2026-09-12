package guardianauthority

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func TestRenewalDeliveryIsAuthenticatedAndMetadataMinimal(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	value := RenewalDelivery{Recipient: "guardian@example.test", DashboardURL: "https://mycfc.example/dashboard/guardian", ExpiresAt: time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)}
	sealed, err := SealRenewalDelivery(key, value)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range [][]byte{[]byte(value.Recipient), []byte(value.DashboardURL)} {
		if bytes.Contains(sealed, private) {
			t.Fatal("delivery field persisted in plaintext")
		}
	}
	opened, err := OpenRenewalDelivery(key, sealed)
	if err != nil || opened != value {
		t.Fatalf("opened=%+v err=%v", opened, err)
	}
	sealed[len(sealed)-1] ^= 1
	if _, err = OpenRenewalDelivery(key, sealed); !errors.Is(err, ErrInvalidDelivery) {
		t.Fatalf("tampered payload error=%v", err)
	}
}

func TestRenewalDeliveryRejectsUnsafeFieldsAndKeys(t *testing.T) {
	valid := RenewalDelivery{Recipient: "guardian@example.test", DashboardURL: "https://mycfc.example/dashboard/guardian", ExpiresAt: time.Now().UTC()}
	for _, candidate := range []RenewalDelivery{
		{Recipient: "invalid", DashboardURL: valid.DashboardURL, ExpiresAt: valid.ExpiresAt},
		{Recipient: valid.Recipient, DashboardURL: "javascript:alert(1)", ExpiresAt: valid.ExpiresAt},
		{Recipient: valid.Recipient, DashboardURL: valid.DashboardURL},
	} {
		if _, err := SealRenewalDelivery(make([]byte, 32), candidate); !errors.Is(err, ErrInvalidDelivery) {
			t.Fatalf("unsafe delivery accepted: %+v err=%v", candidate, err)
		}
	}
	if _, err := SealRenewalDelivery([]byte("short"), valid); !errors.Is(err, ErrInvalidDelivery) {
		t.Fatalf("short key error=%v", err)
	}
}

func TestHandoffDeliveryIsAuthenticatedMinimalAndDomainSeparated(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	value := HandoffDelivery{Recipient: "young@example.test", ActionURL: "https://mycfc.example/transicao-18/verificar?token=opaque", EffectiveAt: time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)}
	sealed, err := SealHandoffDelivery(key, value)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range [][]byte{[]byte(value.Recipient), []byte(value.ActionURL)} {
		if bytes.Contains(sealed, private) {
			t.Fatal("handoff delivery field persisted in plaintext")
		}
	}
	opened, err := OpenHandoffDelivery(key, sealed)
	if err != nil || opened != value {
		t.Fatalf("opened=%+v err=%v", opened, err)
	}
	if _, err = OpenRenewalDelivery(key, sealed); !errors.Is(err, ErrInvalidDelivery) {
		t.Fatalf("handoff payload crossed renewal domain: %v", err)
	}
	sealed[len(sealed)-1] ^= 1
	if _, err = OpenHandoffDelivery(key, sealed); !errors.Is(err, ErrInvalidDelivery) {
		t.Fatalf("tampered handoff error=%v", err)
	}
}

func TestDeliveryRejectsShortKeysPayloadsAndAuthenticatedInvalidJSON(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	renewal := RenewalDelivery{Recipient: "guardian@example.test", DashboardURL: "https://mycfc.example/dashboard/guardian", ExpiresAt: time.Now().UTC()}
	handoff := HandoffDelivery{Recipient: "young@example.test", ActionURL: "https://mycfc.example/transicao-18", EffectiveAt: time.Now().UTC()}
	if _, err := SealHandoffDelivery([]byte("short"), handoff); !errors.Is(err, ErrInvalidDelivery) {
		t.Fatalf("short handoff key error=%v", err)
	}
	invalidHandoff := handoff
	invalidHandoff.Recipient = "invalid"
	if _, err := SealHandoffDelivery(key, invalidHandoff); !errors.Is(err, ErrInvalidDelivery) {
		t.Fatalf("invalid handoff error=%v", err)
	}
	for _, test := range []struct {
		name string
		open func([]byte, []byte) error
	}{
		{name: "renewal short key", open: func(k, payload []byte) error { _, err := OpenRenewalDelivery(k, payload); return err }},
		{name: "handoff short key", open: func(k, payload []byte) error { _, err := OpenHandoffDelivery(k, payload); return err }},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.open([]byte("short"), []byte("payload")); !errors.Is(err, ErrInvalidDelivery) {
				t.Fatalf("error=%v", err)
			}
			if err := test.open(key, []byte("short")); !errors.Is(err, ErrInvalidDelivery) {
				t.Fatalf("short payload error=%v", err)
			}
		})
	}

	renewalCipher, err := renewalDeliveryCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	renewalNonce := make([]byte, renewalCipher.NonceSize())
	renewalPayload := renewalCipher.Seal(renewalNonce, renewalNonce, []byte("{"), []byte("guardian-renewal-notification-v1"))
	if _, err = OpenRenewalDelivery(key, renewalPayload); !errors.Is(err, ErrInvalidDelivery) {
		t.Fatalf("authenticated invalid renewal JSON error=%v", err)
	}

	handoffCipher, err := handoffDeliveryCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	handoffNonce := make([]byte, handoffCipher.NonceSize())
	handoffPayload := handoffCipher.Seal(handoffNonce, handoffNonce, []byte("{"), []byte("guardian-age-handoff-notification-v1"))
	if _, err = OpenHandoffDelivery(key, handoffPayload); !errors.Is(err, ErrInvalidDelivery) {
		t.Fatalf("authenticated invalid handoff JSON error=%v", err)
	}
	if !validRenewalDelivery(renewal) || !validHandoffDelivery(handoff) {
		t.Fatal("valid delivery fixtures rejected")
	}
}
