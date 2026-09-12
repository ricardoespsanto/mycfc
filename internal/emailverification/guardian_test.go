package emailverification

import (
	"context"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/guardianauthority"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type guardianRenewalSenderFake struct {
	senderFake
	recipient, link, kind string
	expires               time.Time
	calls                 int
}

func (s *guardianRenewalSenderFake) SendGuardianRenewalReminder(_ context.Context, recipient, link, kind string, expires time.Time) error {
	s.recipient, s.link, s.kind, s.expires = recipient, link, kind, expires
	s.calls++
	return s.err
}

func (s *guardianRenewalSenderFake) SendGuardianAgeHandoff(_ context.Context, recipient, link, kind string, effectiveAt time.Time) error {
	s.recipient, s.link, s.kind, s.expires = recipient, link, kind, effectiveAt
	s.calls++
	return s.err
}

func TestEmailWorkerDeliversTypedGuardianRenewalReminder(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	expires := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	payload, err := guardianauthority.SealRenewalDelivery(key, guardianauthority.RenewalDelivery{Recipient: "guardian@example.test", DashboardURL: "https://mycfc.example/dashboard/guardian", ExpiresAt: expires})
	if err != nil {
		t.Fatal(err)
	}
	store := &deliveryStoreFake{item: dbgen.ClaimEmailOutboxRow{ID: uuid.New(), MessageType: "GUARDIAN_RENEWAL_7_DAY", SealedPayload: payload,
		UserID: uuid.New(), Email: "guardian@example.test", ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true}, Attempts: 1}}
	sender := &guardianRenewalSenderFake{}
	(Worker{Store: store, Sender: sender, GuardianKey: key, Now: func() time.Time { return expires.Add(-7 * 24 * time.Hour) }}).drain(context.Background())
	if !store.completed || sender.calls != 1 || sender.recipient != "guardian@example.test" || sender.kind != "GUARDIAN_RENEWAL_7_DAY" || !sender.expires.Equal(expires) {
		t.Fatalf("delivery completed=%v sender=%+v", store.completed, sender)
	}
}

func TestEmailWorkerRejectsGuardianRenewalSnapshotThatDoesNotMatchClaimedIdentity(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	expires := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	payload, err := guardianauthority.SealRenewalDelivery(key, guardianauthority.RenewalDelivery{
		Recipient: "stale@example.test", DashboardURL: "https://mycfc.example/dashboard/guardian", ExpiresAt: expires,
	})
	if err != nil {
		t.Fatal(err)
	}
	store := &deliveryStoreFake{item: dbgen.ClaimEmailOutboxRow{ID: uuid.New(), MessageType: "GUARDIAN_RENEWAL_7_DAY", SealedPayload: payload,
		UserID: uuid.New(), Email: "current@example.test", ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true}, Attempts: 1}}
	sender := &guardianRenewalSenderFake{}
	(Worker{Store: store, Sender: sender, GuardianKey: key, Now: func() time.Time { return expires.Add(-7 * 24 * time.Hour) }}).drain(context.Background())
	if !store.failed || store.completed || sender.calls != 0 {
		t.Fatalf("failed=%v completed=%v sender calls=%d", store.failed, store.completed, sender.calls)
	}
}

func TestGuardianRenewalMessageStatesReviewAndNoAutomaticExtension(t *testing.T) {
	for _, kind := range []string{"GUARDIAN_RENEWAL_30_DAY", "GUARDIAN_RENEWAL_7_DAY"} {
		_, plain, rich, err := guardianRenewalReminderMessage(kind, "https://mycfc.example/dashboard/guardian?lang=pt&from=email", time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC))
		if err != nil || !strings.Contains(plain, "12/10/2026") || !strings.Contains(plain, "não prolonga o acesso automaticamente") || !strings.Contains(rich, "&amp;from=email") {
			t.Fatalf("kind=%s plain=%q rich=%q err=%v", kind, plain, rich, err)
		}
	}
	if _, _, _, err := guardianRenewalReminderMessage("UNKNOWN", "https://mycfc.example", time.Now()); err == nil {
		t.Fatal("unknown reminder kind accepted")
	}
}

func TestEmailWorkerDeliversTypedAgeHandoffAndRejectsStaleRecipient(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	effectiveAt := time.Date(2026, 10, 11, 23, 0, 0, 0, time.UTC)
	payload, err := guardianauthority.SealHandoffDelivery(key, guardianauthority.HandoffDelivery{Recipient: "guardian@example.test", ActionURL: "https://mycfc.example/dashboard/guardian", EffectiveAt: effectiveAt})
	if err != nil {
		t.Fatal(err)
	}
	store := &deliveryStoreFake{item: dbgen.ClaimEmailOutboxRow{ID: uuid.New(), MessageType: "GUARDIAN_AGE_18_7_DAY", SealedPayload: payload, UserID: uuid.New(), Email: "guardian@example.test", ExpiresAt: pgtype.Timestamptz{Time: effectiveAt, Valid: true}, Attempts: 1}}
	sender := &guardianRenewalSenderFake{}
	(Worker{Store: store, Sender: sender, GuardianKey: key, Now: func() time.Time { return effectiveAt.Add(-7 * 24 * time.Hour) }}).drain(context.Background())
	if !store.completed || sender.calls != 1 || sender.recipient != "guardian@example.test" || sender.kind != "GUARDIAN_AGE_18_7_DAY" {
		t.Fatalf("completed=%v sender=%+v", store.completed, sender)
	}

	staleStore := &deliveryStoreFake{item: dbgen.ClaimEmailOutboxRow{ID: uuid.New(), MessageType: "GUARDIAN_AGE_18_7_DAY", SealedPayload: payload, UserID: uuid.New(), Email: "changed@example.test", ExpiresAt: pgtype.Timestamptz{Time: effectiveAt, Valid: true}, Attempts: 1}}
	staleSender := &guardianRenewalSenderFake{}
	(Worker{Store: staleStore, Sender: staleSender, GuardianKey: key, Now: func() time.Time { return effectiveAt.Add(-7 * 24 * time.Hour) }}).drain(context.Background())
	if !staleStore.failed || staleSender.calls != 0 {
		t.Fatalf("stale failed=%v calls=%d", staleStore.failed, staleSender.calls)
	}
}

func TestGuardianAgeHandoffMessagesAreClosedAndMetadataMinimal(t *testing.T) {
	for _, kind := range []string{"GUARDIAN_AGE_18_30_DAY", "GUARDIAN_AGE_18_7_DAY", "GUARDIAN_AGE_18_EMAIL_VERIFY"} {
		_, plain, rich, err := guardianAgeHandoffMessage(kind, "https://mycfc.example/transicao-18?from=email&lang=pt", time.Date(2026, 10, 11, 23, 0, 0, 0, time.UTC))
		if err != nil || !strings.Contains(plain, "12/10/2026") || !strings.Contains(rich, "&amp;lang=pt") || strings.Contains(plain, "Jovem") {
			t.Fatalf("kind=%s plain=%q rich=%q err=%v", kind, plain, rich, err)
		}
	}
	if _, _, _, err := guardianAgeHandoffMessage("UNKNOWN", "https://mycfc.example", time.Now()); err == nil {
		t.Fatal("unknown handoff mail accepted")
	}
}
