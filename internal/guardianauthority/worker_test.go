package guardianauthority

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type workerStore struct {
	calls            int
	pruneCalls       int
	reconcileErr     error
	pruneErr         error
	reminderErr      error
	handoffErr       error
	reminderQueueErr error
	handoffQueueErr  error
	reminderCalls    int
	reminders        []dbgen.ListDueGuardianRenewalRemindersRow
	enqueued         []dbgen.EnqueueGuardianRenewalReminderParams
	handoffNotices   []dbgen.ListDueGuardianAgeHandoffNoticesRow
	handoffEnqueued  []dbgen.EnqueueGuardianAgeHandoffNoticeParams
}

func (s *workerStore) ReconcileGuardianAuthorityCutoffs(context.Context) (int64, error) {
	s.calls++
	return 1, s.reconcileErr
}
func (s *workerStore) PruneGuardianApplicationRateEvents(context.Context) (int64, error) {
	s.pruneCalls++
	return 1, s.pruneErr
}
func (s *workerStore) ListDueGuardianRenewalReminders(context.Context, int32) ([]dbgen.ListDueGuardianRenewalRemindersRow, error) {
	s.reminderCalls++
	return s.reminders, s.reminderErr
}
func (s *workerStore) EnqueueGuardianRenewalReminder(_ context.Context, input dbgen.EnqueueGuardianRenewalReminderParams) (bool, error) {
	s.enqueued = append(s.enqueued, input)
	return true, s.reminderQueueErr
}
func (s *workerStore) ListDueGuardianAgeHandoffNotices(context.Context, int32) ([]dbgen.ListDueGuardianAgeHandoffNoticesRow, error) {
	return s.handoffNotices, s.handoffErr
}
func (s *workerStore) EnqueueGuardianAgeHandoffNotice(_ context.Context, input dbgen.EnqueueGuardianAgeHandoffNoticeParams) (bool, error) {
	s.handoffEnqueued = append(s.handoffEnqueued, input)
	return true, s.handoffQueueErr
}

func TestWorkerReconcilesImmediatelyAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := &workerStore{}
	(Worker{Store: store}).Run(ctx)
	if store.calls != 1 || store.pruneCalls != 1 || store.reminderCalls != 1 {
		t.Fatalf("calls = %d prune=%d, want 1 each", store.calls, store.pruneCalls)
	}
}

func TestWorkerQueuesTypedSealedAgeHandoffNotice(t *testing.T) {
	birthday := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	verifiedAt := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	store := &workerStore{handoffNotices: []dbgen.ListDueGuardianAgeHandoffNoticesRow{{
		HandoffRef: uuid.New(), RelationshipRef: uuid.New(), GuardianUserID: uuid.New(), Recipient: "guardian@example.test",
		RecipientVerifiedAt: pgtype.Timestamptz{Time: verifiedAt, Valid: true}, Birthday: pgtype.Date{Time: birthday, Valid: true}, NoticeKind: "GUARDIAN_AGE_18_30_DAY",
	}}}
	key := []byte("0123456789abcdef0123456789abcdef")
	(Worker{Store: store, Key: key, BaseURL: "https://mycfc.example/"}).reconcile(context.Background())
	if len(store.handoffEnqueued) != 1 || store.handoffEnqueued[0].Recipient != "guardian@example.test" {
		t.Fatalf("enqueued=%+v", store.handoffEnqueued)
	}
	payload, err := OpenHandoffDelivery(key, store.handoffEnqueued[0].SealedPayload)
	if err != nil || payload.Recipient != "guardian@example.test" || payload.ActionURL != "https://mycfc.example/dashboard/guardian" || payload.EffectiveAt.In(time.UTC).Format("2006-01-02") != "2026-10-11" {
		t.Fatalf("payload=%+v err=%v", payload, err)
	}
}

func TestWorkerTreatsReconciliationFailureAsRetryable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := &workerStore{reconcileErr: errors.New("database unavailable")}
	(Worker{Store: store}).Run(ctx)
	if store.calls != 1 || store.pruneCalls != 1 || store.reminderCalls != 1 {
		t.Fatalf("reconcile calls = %d prune calls = %d, want 1 each", store.calls, store.pruneCalls)
	}
}

func TestWorkerUsesInjectedLogger(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if got := (Worker{Logger: logger}).logger(); got != logger {
		t.Fatal("injected logger was not returned")
	}
}

func TestWorkerQueuesTypedSealedRenewalReminder(t *testing.T) {
	expiry := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	guardianID := uuid.New()
	verifiedAt := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	store := &workerStore{reminders: []dbgen.ListDueGuardianRenewalRemindersRow{{
		RelationshipRef: uuid.New(), GuardianUserID: guardianID, Recipient: "guardian@example.test",
		RecipientVerifiedAt: pgtype.Timestamptz{Time: verifiedAt, Valid: true},
		ExpiryAnchor:        pgtype.Timestamptz{Time: expiry, Valid: true}, ReminderKind: "GUARDIAN_RENEWAL_30_DAY",
	}}}
	key := []byte("0123456789abcdef0123456789abcdef")
	(Worker{Store: store, Key: key, BaseURL: "https://mycfc.example/"}).reconcile(context.Background())
	if len(store.enqueued) != 1 || store.enqueued[0].ReminderKind != "GUARDIAN_RENEWAL_30_DAY" ||
		store.enqueued[0].GuardianUserID != guardianID || store.enqueued[0].Recipient != "guardian@example.test" ||
		!store.enqueued[0].RecipientVerifiedAt.Time.Equal(verifiedAt) {
		t.Fatalf("enqueued=%+v", store.enqueued)
	}
	payload, err := OpenRenewalDelivery(key, store.enqueued[0].SealedPayload)
	if err != nil || payload.Recipient != "guardian@example.test" || payload.DashboardURL != "https://mycfc.example/dashboard/guardian" || !payload.ExpiresAt.Equal(expiry) {
		t.Fatalf("payload=%+v err=%v", payload, err)
	}
}

func TestWorkerContinuesAcrossDiscoveryPruningAndQueueFailures(t *testing.T) {
	want := errors.New("database unavailable")
	store := &workerStore{pruneErr: want, reminderErr: want, handoffErr: want}
	(Worker{Store: store, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}).reconcile(t.Context())
	if store.pruneCalls != 1 || store.reminderCalls != 1 {
		t.Fatalf("prune=%d reminder=%d", store.pruneCalls, store.reminderCalls)
	}

	expiry := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	queueStore := &workerStore{
		reminders:        []dbgen.ListDueGuardianRenewalRemindersRow{{RelationshipRef: uuid.New(), GuardianUserID: uuid.New(), Recipient: "guardian@example.test", ExpiryAnchor: pgtype.Timestamptz{Time: expiry, Valid: true}, ReminderKind: "GUARDIAN_RENEWAL_7_DAY"}},
		handoffNotices:   []dbgen.ListDueGuardianAgeHandoffNoticesRow{{HandoffRef: uuid.New(), RelationshipRef: uuid.New(), GuardianUserID: uuid.New(), Recipient: "guardian@example.test", Birthday: pgtype.Date{Time: expiry, Valid: true}, NoticeKind: "GUARDIAN_AGE_18_7_DAY"}},
		reminderQueueErr: want,
		handoffQueueErr:  want,
	}
	(Worker{Store: queueStore, Key: []byte("0123456789abcdef0123456789abcdef"), BaseURL: "https://mycfc.example", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}).reconcile(t.Context())
	if len(queueStore.enqueued) != 1 || len(queueStore.handoffEnqueued) != 1 {
		t.Fatalf("renewal queue=%d handoff queue=%d", len(queueStore.enqueued), len(queueStore.handoffEnqueued))
	}
}

func TestWorkerRejectsUnsealableReminderAndHandoffPayloads(t *testing.T) {
	expiry := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	store := &workerStore{
		reminders:      []dbgen.ListDueGuardianRenewalRemindersRow{{RelationshipRef: uuid.New(), GuardianUserID: uuid.New(), Recipient: "guardian@example.test", ExpiryAnchor: pgtype.Timestamptz{Time: expiry, Valid: true}, ReminderKind: "GUARDIAN_RENEWAL_30_DAY"}},
		handoffNotices: []dbgen.ListDueGuardianAgeHandoffNoticesRow{{HandoffRef: uuid.New(), RelationshipRef: uuid.New(), GuardianUserID: uuid.New(), Recipient: "guardian@example.test", Birthday: pgtype.Date{Time: expiry, Valid: true}, NoticeKind: "GUARDIAN_AGE_18_30_DAY"}},
	}
	(Worker{Store: store, Key: []byte("short"), BaseURL: "https://mycfc.example", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}).reconcile(t.Context())
	if len(store.enqueued) != 0 || len(store.handoffEnqueued) != 0 {
		t.Fatalf("invalid payloads enqueued: renewal=%d handoff=%d", len(store.enqueued), len(store.handoffEnqueued))
	}
}
