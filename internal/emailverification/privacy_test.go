package emailverification

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/textproto"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/privacyrequests"
	"github.com/google/uuid"
)

type privacySenderFake struct {
	senderFake
	recipient, contact, kind string
	calls                    int
}

func (s *privacySenderFake) SendPrivacyNotification(_ context.Context, recipient, contact, kind string) error {
	s.recipient, s.contact, s.kind = recipient, contact, kind
	s.calls++
	return s.err
}

func privacyDeliveryFixture(t *testing.T, kind string) (*deliveryStoreFake, []byte, time.Time) {
	t.Helper()
	key := []byte("0123456789abcdef0123456789abcdef")
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	payload, err := privacyrequests.SealDelivery(key, privacyrequests.Delivery{
		Recipient: "private-recipient@example.test", ContactURL: "https://mycfc.example/legal/direitos",
	})
	if err != nil {
		t.Fatal(err)
	}
	return &deliveryStoreFake{item: dbgen.ClaimEmailOutboxRow{
		ID: uuid.New(), MessageType: kind, SealedPayload: payload, Attempts: 1,
		// Privacy rows have NULL token expiry in ClaimEmailOutbox.
		// No live account email or verification/reset token remains.
	}}, key, now
}

func TestPrivacyWorkerDeliversToSealedRecipientWithoutAccount(t *testing.T) {
	for _, kind := range []string{"PRIVACY_ACKNOWLEDGEMENT", "PRIVACY_DECISION", "PRIVACY_PROCESSING_STARTED"} {
		t.Run(kind, func(t *testing.T) {
			store, key, now := privacyDeliveryFixture(t, kind)
			sender := &privacySenderFake{}
			worker := Worker{Store: store, Sender: sender, PrivacyKey: key, Now: func() time.Time { return now }}
			worker.drain(context.Background())
			if !store.completed || store.failed || store.retried || sender.calls != 1 {
				t.Fatalf("unexpected delivery outcome: completed=%v failed=%v retried=%v calls=%d", store.completed, store.failed, store.retried, sender.calls)
			}
			if sender.recipient != "private-recipient@example.test" || sender.contact != "https://mycfc.example/legal/direitos" || sender.kind != kind {
				t.Fatal("did not use authenticated encrypted delivery fields")
			}
		})
	}
}

func TestPrivacyWorkerFailsClosedOnInvalidPayloadOrUnsupportedSender(t *testing.T) {
	for _, problem := range []string{"missing key", "wrong key", "tampered payload", "unsupported sender"} {
		t.Run(problem, func(t *testing.T) {
			store, key, now := privacyDeliveryFixture(t, "PRIVACY_DECISION")
			sender := &privacySenderFake{}
			worker := Worker{Store: store, Sender: sender, PrivacyKey: key, Now: func() time.Time { return now }}
			switch problem {
			case "missing key":
				worker.PrivacyKey = nil
			case "wrong key":
				worker.PrivacyKey = bytes.Repeat([]byte{42}, 32)
			case "tampered payload":
				store.item.SealedPayload[len(store.item.SealedPayload)-1] ^= 1
			case "unsupported sender":
				worker.Sender = &senderFake{}
			}
			worker.drain(context.Background())
			if !store.failed || store.completed || store.retried || sender.calls != 0 {
				t.Fatal("invalid privacy delivery was sent or retried")
			}
		})
	}
}

func TestPrivacyWorkerRetriesWithoutLoggingSensitiveSMTPError(t *testing.T) {
	for _, permanent := range []bool{false, true} {
		store, key, now := privacyDeliveryFixture(t, "PRIVACY_DECISION")
		sendErr := errors.New("network error for private-recipient@example.test https://mycfc.example/legal/direitos")
		if permanent {
			sendErr = &textproto.Error{Code: 550, Msg: sendErr.Error()}
		}
		var log bytes.Buffer
		worker := Worker{Store: store, Sender: &privacySenderFake{senderFake: senderFake{err: sendErr}}, PrivacyKey: key,
			Logger: slog.New(slog.NewTextHandler(&log, nil)), Now: func() time.Time { return now }}
		worker.drain(context.Background())
		if store.completed || store.retried == permanent || store.failed != permanent {
			t.Fatalf("wrong retry outcome for permanent=%v", permanent)
		}
		for _, sensitive := range []string{"private-recipient", "example.test", "https://", "network error"} {
			if strings.Contains(log.String(), sensitive) {
				t.Fatal("SMTP error or recipient leaked into operational logs")
			}
		}
	}
}

func TestPrivacyNotificationProvidesPublicContactWithoutDisclosingDecision(t *testing.T) {
	for _, kind := range []string{"PRIVACY_ACKNOWLEDGEMENT", "PRIVACY_DECISION", "PRIVACY_PROCESSING_STARTED"} {
		subject, plain, rich, err := privacyNotificationMessage(kind, "https://mycfc.example/legal/direitos?lang=pt&source=email")
		if err != nil || subject == "" {
			t.Fatalf("notification construction failed: %v", err)
		}
		if !strings.Contains(plain, "mesmo sem acesso à conta") || !strings.Contains(plain, "/legal/direitos") || !strings.Contains(rich, "&amp;source=email") {
			t.Fatal("public after-closure contact or HTML escaping missing")
		}
		for _, forbidden := range []string{"/perfil/", "APPROVED", "REFUSED", "medical", "subject_id"} {
			if strings.Contains(subject+plain+rich, forbidden) {
				t.Fatal("notification disclosed case details or required a private route")
			}
		}
		if (kind == "PRIVACY_DECISION" || kind == "PRIVACY_PROCESSING_STARTED") && !strings.Contains(plain, "não confirma que os dados foram apagados") {
			t.Fatal("update message implies erasure completion")
		}
		if kind == "PRIVACY_PROCESSING_STARTED" && !strings.Contains(plain, "acesso à conta afetada pode ter terminado") {
			t.Fatal("processing message omits the access consequence")
		}
	}
	if _, _, _, err := privacyNotificationMessage("ARBITRARY", "https://mycfc.example/legal/direitos"); err == nil {
		t.Fatal("unknown notification kind accepted")
	}
}
