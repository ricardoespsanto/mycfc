package guardianauthority

import (
	"context"
	"log/slog"
	"strings"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/jackc/pgx/v5/pgtype"
)

const ReconcileInterval = time.Minute

type Store interface {
	ReconcileGuardianAuthorityCutoffs(context.Context) (int64, error)
	PruneGuardianApplicationRateEvents(context.Context) (int64, error)
	ListDueGuardianRenewalReminders(context.Context, int32) ([]dbgen.ListDueGuardianRenewalRemindersRow, error)
	EnqueueGuardianRenewalReminder(context.Context, dbgen.EnqueueGuardianRenewalReminderParams) (bool, error)
	ListDueGuardianAgeHandoffNotices(context.Context, int32) ([]dbgen.ListDueGuardianAgeHandoffNoticesRow, error)
	EnqueueGuardianAgeHandoffNotice(context.Context, dbgen.EnqueueGuardianAgeHandoffNoticeParams) (bool, error)
}

// Worker materializes time-based authority cutoffs so credentials and indexed
// sessions are removed even when nobody opens the relationship or tries to log in.
type Worker struct {
	Store   Store
	Logger  *slog.Logger
	Key     []byte
	BaseURL string
}

func (w Worker) Run(ctx context.Context) {
	w.reconcile(ctx)
	ticker := time.NewTicker(ReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.reconcile(ctx)
		}
	}
}

func (w Worker) reconcile(ctx context.Context) {
	count, err := w.Store.ReconcileGuardianAuthorityCutoffs(ctx)
	if err != nil {
		w.logger().Error("guardian authority cutoff reconciliation failed", "error_class", "database")
	} else if count > 0 {
		w.logger().Info("guardian authority cutoffs reconciled", "relationship_count", count)
	}
	pruned, err := w.Store.PruneGuardianApplicationRateEvents(ctx)
	if err != nil {
		w.logger().Error("guardian application rate evidence pruning failed", "error_class", "database")
	} else if pruned > 0 {
		w.logger().Info("guardian application rate evidence pruned", "event_count", pruned)
	}
	w.queueRenewalReminders(ctx)
	w.queueAgeHandoffNotices(ctx)
}

func (w Worker) queueRenewalReminders(ctx context.Context) {
	items, err := w.Store.ListDueGuardianRenewalReminders(ctx, 100)
	if err != nil {
		w.logger().Error("guardian renewal reminder discovery failed", "error_class", "database")
		return
	}
	for _, item := range items {
		payload, sealErr := SealRenewalDelivery(w.Key, RenewalDelivery{
			Recipient: item.Recipient, DashboardURL: strings.TrimRight(w.BaseURL, "/") + "/dashboard/guardian", ExpiresAt: item.ExpiryAnchor.Time,
		})
		if sealErr != nil {
			w.logger().Error("guardian renewal reminder sealing failed", "error_class", "configuration")
			return
		}
		queued, queueErr := w.Store.EnqueueGuardianRenewalReminder(ctx, dbgen.EnqueueGuardianRenewalReminderParams{
			RelationshipRef: item.RelationshipRef, GuardianUserID: item.GuardianUserID, Recipient: item.Recipient,
			RecipientVerifiedAt: item.RecipientVerifiedAt, ExpiryAnchor: pgtype.Timestamptz{Time: item.ExpiryAnchor.Time, Valid: true},
			ReminderKind: item.ReminderKind, SealedPayload: payload,
		})
		if queueErr != nil {
			w.logger().Error("guardian renewal reminder enqueue failed", "error_class", "database")
			continue
		}
		if queued {
			w.logger().Info("guardian renewal reminder queued", "reminder_kind", item.ReminderKind)
		}
	}
}

func (w Worker) queueAgeHandoffNotices(ctx context.Context) {
	items, err := w.Store.ListDueGuardianAgeHandoffNotices(ctx, 100)
	if err != nil {
		w.logger().Error("guardian age handoff notice discovery failed", "error_class", "database")
		return
	}
	lisbon, locationErr := time.LoadLocation("Europe/Lisbon")
	if locationErr != nil {
		w.logger().Error("guardian age handoff notice timezone unavailable", "error_class", "configuration")
		return
	}
	for _, item := range items {
		birthday := item.Birthday.Time
		effectiveAt := time.Date(birthday.Year(), birthday.Month(), birthday.Day(), 0, 0, 0, 0, lisbon)
		payload, sealErr := SealHandoffDelivery(w.Key, HandoffDelivery{
			Recipient:   item.Recipient,
			ActionURL:   strings.TrimRight(w.BaseURL, "/") + "/dashboard/guardian",
			EffectiveAt: effectiveAt,
		})
		if sealErr != nil {
			w.logger().Error("guardian age handoff notice sealing failed", "error_class", "configuration")
			return
		}
		queued, queueErr := w.Store.EnqueueGuardianAgeHandoffNotice(ctx, dbgen.EnqueueGuardianAgeHandoffNoticeParams{
			HandoffRef: item.HandoffRef, RelationshipRef: item.RelationshipRef, GuardianUserID: item.GuardianUserID,
			Recipient: item.Recipient, RecipientVerifiedAt: item.RecipientVerifiedAt, Birthday: item.Birthday,
			NoticeKind: item.NoticeKind, SealedPayload: payload,
		})
		if queueErr != nil {
			w.logger().Error("guardian age handoff notice enqueue failed", "error_class", "database")
			continue
		}
		if queued {
			w.logger().Info("guardian age handoff notice queued", "notice_kind", item.NoticeKind)
		}
	}
}

func (w Worker) logger() *slog.Logger {
	if w.Logger != nil {
		return w.Logger
	}
	return slog.Default()
}
