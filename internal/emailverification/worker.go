package emailverification

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/guardianauthority"
	"github.com/cfcoimbra/mycfc/internal/passwordreset"
	"github.com/cfcoimbra/mycfc/internal/privacyrequests"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	PollInterval = 5 * time.Second
	StaleAfter   = 5 * time.Minute
	MaxAttempts  = 10
)

type Sender interface {
	SendVerification(context.Context, string, string, time.Time) error
	SendPasswordReset(context.Context, string, string, time.Time) error
}

// PrivacySender is separate so existing verification/reset senders keep their
// contract. Missing support fails closed rather than silently dropping a notice.
type PrivacySender interface {
	SendPrivacyNotification(context.Context, string, string, string) error
}

type GuardianRenewalSender interface {
	SendGuardianRenewalReminder(context.Context, string, string, string, time.Time) error
}

type GuardianHandoffSender interface {
	SendGuardianAgeHandoff(context.Context, string, string, string, time.Time) error
}

type DeliveryStore interface {
	ClaimEmailOutbox(context.Context, dbgen.ClaimEmailOutboxParams) (dbgen.ClaimEmailOutboxRow, error)
	CompleteEmailOutbox(context.Context, dbgen.CompleteEmailOutboxParams) (int64, error)
	RetryEmailOutbox(context.Context, dbgen.RetryEmailOutboxParams) (int64, error)
	FailEmailOutbox(context.Context, dbgen.FailEmailOutboxParams) (int64, error)
	CancelUndeliverableEmailOutbox(context.Context, pgtype.Timestamptz) (int64, error)
}

type Worker struct {
	Store         DeliveryStore
	Sender        Sender
	Service       Service
	PasswordReset passwordreset.Service
	PrivacyKey    []byte
	GuardianKey   []byte
	Logger        *slog.Logger
	Now           func() time.Time
}

func (w Worker) Run(ctx context.Context) {
	w.drain(ctx)
	ticker := time.NewTicker(PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.drain(ctx)
		}
	}
}

func (w Worker) drain(ctx context.Context) {
	now := w.now()
	_, _ = w.Store.CancelUndeliverableEmailOutbox(ctx, timestamp(now))
	for ctx.Err() == nil {
		item, err := w.Store.ClaimEmailOutbox(ctx, dbgen.ClaimEmailOutboxParams{ClaimedAt: timestamp(now), StaleBefore: timestamp(now.Add(-StaleAfter))})
		if errors.Is(err, pgx.ErrNoRows) {
			return
		}
		if err != nil {
			w.log("email outbox claim failed", uuid.Nil, "database")
			return
		}
		w.deliver(ctx, item)
		now = w.now()
	}
}

func (w Worker) deliver(ctx context.Context, item dbgen.ClaimEmailOutboxRow) {
	now := w.now()
	var err error
	invalidPayload := false
	switch item.MessageType {
	case "EMAIL_VERIFICATION":
		if item.VerificationTokenID == nil {
			err = errors.New("missing verification token reference")
			invalidPayload = true
		} else {
			err = w.Sender.SendVerification(ctx, item.Email, w.Service.Link(*item.VerificationTokenID), item.ExpiresAt.Time)
		}
	case "PASSWORD_RESET":
		link, openErr := w.PasswordReset.OpenDeliveryLink(item.SealedPayload, item.Email)
		if openErr != nil {
			err = openErr
			invalidPayload = true
		} else {
			err = w.Sender.SendPasswordReset(ctx, item.Email, link, item.ExpiresAt.Time)
		}
	case "PRIVACY_ACKNOWLEDGEMENT", "PRIVACY_DECISION", "PRIVACY_PROCESSING_STARTED", "PRIVACY_COMPLETED":
		payload, openErr := privacyrequests.OpenDelivery(w.PrivacyKey, item.SealedPayload)
		sender, supported := w.Sender.(PrivacySender)
		if openErr != nil || !supported {
			err = errors.New("invalid privacy delivery configuration or payload")
			invalidPayload = true
		} else {
			// The encrypted recipient is intentionally independent of a current
			// account or token, which may disappear during later erasure execution.
			err = sender.SendPrivacyNotification(ctx, payload.Recipient, payload.ContactURL, item.MessageType)
		}
	case "GUARDIAN_RENEWAL_30_DAY", "GUARDIAN_RENEWAL_7_DAY":
		payload, openErr := guardianauthority.OpenRenewalDelivery(w.GuardianKey, item.SealedPayload)
		sender, supported := w.Sender.(GuardianRenewalSender)
		identityMatches := item.UserID != uuid.Nil && item.Email != "" && item.ExpiresAt.Valid &&
			strings.EqualFold(payload.Recipient, item.Email) && payload.ExpiresAt.Equal(item.ExpiresAt.Time)
		if openErr != nil || !supported || !identityMatches {
			err = errors.New("invalid guardian renewal delivery configuration or payload")
			invalidPayload = true
		} else {
			// The claim query revalidates the current guardian identity and verified
			// email binding. Never address mail from the sealed discovery snapshot.
			err = sender.SendGuardianRenewalReminder(ctx, item.Email, payload.DashboardURL, item.MessageType, item.ExpiresAt.Time)
		}
	case "GUARDIAN_AGE_18_30_DAY", "GUARDIAN_AGE_18_7_DAY", "GUARDIAN_AGE_18_EMAIL_VERIFY":
		payload, openErr := guardianauthority.OpenHandoffDelivery(w.GuardianKey, item.SealedPayload)
		sender, supported := w.Sender.(GuardianHandoffSender)
		identityMatches := item.UserID != uuid.Nil && item.Email != "" && item.ExpiresAt.Valid &&
			strings.EqualFold(payload.Recipient, item.Email) && payload.EffectiveAt.Equal(item.ExpiresAt.Time)
		if openErr != nil || !supported || !identityMatches {
			err = errors.New("invalid guardian age handoff delivery configuration or payload")
			invalidPayload = true
		} else {
			// The claim projection atomically rechecks the current recipient and
			// authority state. Never address mail from a stale discovery snapshot.
			err = sender.SendGuardianAgeHandoff(ctx, item.Email, payload.ActionURL, item.MessageType, item.ExpiresAt.Time)
		}
	default:
		err = errors.New("unsupported email outbox message type")
		invalidPayload = true
	}
	if err == nil {
		_, updateErr := w.Store.CompleteEmailOutbox(ctx, dbgen.CompleteEmailOutboxParams{ID: item.ID, CompletedAt: timestamp(now)})
		if updateErr != nil {
			w.log("email outbox completion failed", item.ID, "database")
		} else if item.MessageType == "PASSWORD_RESET" {
			w.passwordResetEvent("password_recovery_delivered", "success")
		}
		return
	}
	permanent := invalidPayload || IsPermanent(err)
	tokenMessage := item.MessageType == "EMAIL_VERIFICATION" || item.MessageType == "PASSWORD_RESET"
	tokenExpired := tokenMessage && !item.ExpiresAt.Time.After(now.Add(retryDelay(item.Attempts)))
	if permanent || item.Attempts >= MaxAttempts || tokenExpired {
		reason := "SMTP delivery failed permanently"
		_, updateErr := w.Store.FailEmailOutbox(ctx, dbgen.FailEmailOutboxParams{ID: item.ID, FailedAt: timestamp(now), LastError: &reason})
		if updateErr != nil {
			w.log("email outbox failure update failed", item.ID, "database")
		} else {
			w.log("email delivery stopped", item.ID, "permanent")
			if item.MessageType == "PASSWORD_RESET" {
				w.passwordResetEvent("password_recovery_delivery_failed", "permanent")
			}
		}
		return
	}
	reason := "temporary SMTP delivery failure"
	_, updateErr := w.Store.RetryEmailOutbox(ctx, dbgen.RetryEmailOutboxParams{ID: item.ID, FailedAt: timestamp(now), NextAttemptAt: timestamp(now.Add(retryDelay(item.Attempts))), LastError: &reason})
	if updateErr != nil {
		w.log("email outbox retry update failed", item.ID, "database")
	} else {
		w.log("email delivery scheduled for retry", item.ID, "temporary")
		if item.MessageType == "PASSWORD_RESET" {
			w.passwordResetEvent("password_recovery_delivery_failed", "retry_scheduled")
		}
	}
}

func (w Worker) passwordResetEvent(event, outcome string) {
	if w.Logger != nil {
		w.Logger.Info("password recovery event", "event", event, "outcome", outcome)
	}
}

func retryDelay(attempt int32) time.Duration {
	delay := 30 * time.Second
	for i := int32(1); i < attempt && delay < time.Hour; i++ {
		delay *= 2
	}
	if delay > time.Hour {
		return time.Hour
	}
	return delay
}

func (w Worker) now() time.Time {
	if w.Now != nil {
		return w.Now().UTC()
	}
	return time.Now().UTC()
}

func (w Worker) log(message string, id uuid.UUID, class string) {
	if w.Logger == nil {
		return
	}
	attributes := []any{"class", class}
	if id != uuid.Nil {
		attributes = append(attributes, "outbox_id", id.String())
	}
	w.Logger.Warn(message, attributes...)
}
