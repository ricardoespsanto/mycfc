package emailverification

import (
	"context"
	"errors"
	"log/slog"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/passwordreset"
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
	if permanent || item.Attempts >= MaxAttempts || !item.ExpiresAt.Time.After(now.Add(retryDelay(item.Attempts))) {
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
