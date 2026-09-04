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
	"github.com/cfcoimbra/mycfc/internal/passwordreset"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type deliveryStoreFake struct {
	item                       dbgen.ClaimEmailOutboxRow
	claimed                    bool
	completed, retried, failed bool
	cancelled                  bool
	claimErr, completeErr      error
	retryErr, failErr          error
	cancelErr                  error
}

func (f *deliveryStoreFake) ClaimEmailOutbox(context.Context, dbgen.ClaimEmailOutboxParams) (dbgen.ClaimEmailOutboxRow, error) {
	if f.claimErr != nil {
		return dbgen.ClaimEmailOutboxRow{}, f.claimErr
	}
	if f.claimed {
		return dbgen.ClaimEmailOutboxRow{}, pgx.ErrNoRows
	}
	f.claimed = true
	return f.item, nil
}
func (f *deliveryStoreFake) CompleteEmailOutbox(context.Context, dbgen.CompleteEmailOutboxParams) (int64, error) {
	f.completed = true
	return 1, f.completeErr
}
func (f *deliveryStoreFake) RetryEmailOutbox(context.Context, dbgen.RetryEmailOutboxParams) (int64, error) {
	f.retried = true
	return 1, f.retryErr
}
func (f *deliveryStoreFake) FailEmailOutbox(context.Context, dbgen.FailEmailOutboxParams) (int64, error) {
	f.failed = true
	return 1, f.failErr
}
func (f *deliveryStoreFake) CancelUndeliverableEmailOutbox(context.Context, pgtype.Timestamptz) (int64, error) {
	f.cancelled = true
	return 0, f.cancelErr
}

type senderFake struct {
	err  error
	link string
}

func (f *senderFake) SendVerification(_ context.Context, _, link string, _ time.Time) error {
	f.link = link
	return f.err
}
func (f *senderFake) SendPasswordReset(_ context.Context, _, link string, _ time.Time) error {
	f.link = link
	return f.err
}

func TestWorkerCompletesSuccessfulDelivery(t *testing.T) {
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	tokenID := uuid.New()
	store := &deliveryStoreFake{item: dbgen.ClaimEmailOutboxRow{ID: uuid.New(), MessageType: "EMAIL_VERIFICATION", VerificationTokenID: &tokenID, Email: "member@example.test", Attempts: 1, ExpiresAt: timestamp(now.Add(time.Hour))}}
	sender := &senderFake{}
	worker := Worker{Store: store, Sender: sender, Service: Service{BaseURL: "https://mycfc.example", Key: []byte("0123456789abcdef0123456789abcdef")}, Now: func() time.Time { return now }}
	worker.drain(context.Background())
	if !store.completed || store.retried || store.failed {
		t.Fatalf("delivery state = completed:%v retried:%v failed:%v", store.completed, store.retried, store.failed)
	}
	if sender.link != worker.Service.Link(tokenID) {
		t.Fatalf("link = %q", sender.link)
	}
}

func TestWorkerRetriesTransientAndFailsPermanentDelivery(t *testing.T) {
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name        string
		err         error
		retry, fail bool
	}{{"transient", errors.New("network unavailable"), true, false}, {"permanent", &textproto.Error{Code: 550, Msg: "rejected"}, false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			tokenID := uuid.New()
			store := &deliveryStoreFake{item: dbgen.ClaimEmailOutboxRow{ID: uuid.New(), MessageType: "EMAIL_VERIFICATION", VerificationTokenID: &tokenID, Email: "member@example.test", Attempts: 1, ExpiresAt: timestamp(now.Add(time.Hour))}}
			worker := Worker{Store: store, Sender: &senderFake{err: tc.err}, Service: Service{BaseURL: "https://mycfc.example", Key: []byte("0123456789abcdef0123456789abcdef")}, Now: func() time.Time { return now }}
			worker.drain(context.Background())
			if store.retried != tc.retry || store.failed != tc.fail {
				t.Fatalf("retry=%v fail=%v", store.retried, store.failed)
			}
		})
	}
}

type resetIssuanceStoreFake struct {
	created dbgen.CreatePasswordResetParams
}

func (f *resetIssuanceStoreFake) FindPasswordResetAccountByEmail(context.Context, *string) (dbgen.FindPasswordResetAccountByEmailRow, error) {
	return dbgen.FindPasswordResetAccountByEmailRow{ID: uuid.New(), Email: "member@example.test"}, nil
}
func (f *resetIssuanceStoreFake) CreatePasswordReset(_ context.Context, input dbgen.CreatePasswordResetParams) (uuid.UUID, error) {
	f.created = input
	return uuid.New(), nil
}
func (f *resetIssuanceStoreFake) ResolvePasswordResetToken(context.Context, dbgen.ResolvePasswordResetTokenParams) (dbgen.ResolvePasswordResetTokenRow, error) {
	return dbgen.ResolvePasswordResetTokenRow{}, nil
}
func (f *resetIssuanceStoreFake) ConsumePasswordResetToken(context.Context, dbgen.ConsumePasswordResetTokenParams) (uuid.UUID, error) {
	return uuid.Nil, nil
}

func TestWorkerDeliversPasswordResetAndFailsInvalidPayload(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	key := []byte("0123456789abcdef0123456789abcdef")
	issuance := &resetIssuanceStoreFake{}
	resetService := passwordreset.Service{Store: issuance, BaseURL: "https://mycfc.example", Key: key, Rand: bytes.NewReader(make([]byte, passwordreset.TokenBytes+12)), Now: func() time.Time { return now }}
	if _, err := resetService.Issue(context.Background(), "member@example.test", true); err != nil {
		t.Fatal(err)
	}
	resetID := uuid.New()
	store := &deliveryStoreFake{item: dbgen.ClaimEmailOutboxRow{ID: uuid.New(), MessageType: "PASSWORD_RESET", PasswordResetTokenID: &resetID, SealedPayload: issuance.created.SealedPayload, Email: "member@example.test", Attempts: 1, ExpiresAt: timestamp(now.Add(time.Hour))}}
	sender := &senderFake{}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	worker := Worker{Store: store, Sender: sender, PasswordReset: resetService, Logger: logger, Now: func() time.Time { return now }}
	worker.drain(context.Background())
	if !store.cancelled || !store.completed || !strings.HasPrefix(sender.link, "https://mycfc.example/recuperar-palavra-passe/repor?token=") {
		t.Fatalf("reset delivery = cancelled:%v completed:%v link:%q", store.cancelled, store.completed, sender.link)
	}

	invalid := &deliveryStoreFake{item: dbgen.ClaimEmailOutboxRow{ID: uuid.New(), MessageType: "PASSWORD_RESET", PasswordResetTokenID: &resetID, SealedPayload: []byte("invalid"), Email: "member@example.test", Attempts: 1, ExpiresAt: timestamp(now.Add(time.Hour))}}
	Worker{Store: invalid, Sender: &senderFake{}, PasswordReset: resetService, Logger: logger, Now: func() time.Time { return now }}.drain(context.Background())
	if !invalid.failed || invalid.retried || invalid.completed {
		t.Fatalf("invalid payload state = failed:%v retried:%v completed:%v", invalid.failed, invalid.retried, invalid.completed)
	}
	for _, event := range []string{"password_recovery_delivered", "password_recovery_delivery_failed"} {
		if !strings.Contains(logs.String(), event) {
			t.Errorf("logs do not contain %q: %s", event, logs.String())
		}
	}
	for _, forbidden := range []string{"member@example.test", sender.link, "token="} {
		if strings.Contains(logs.String(), forbidden) {
			t.Fatalf("sensitive value %q leaked into logs: %s", forbidden, logs.String())
		}
	}
}

func TestWorkerStopsOrRecordsFailuresAcrossOutboxBoundaries(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	base := func(item dbgen.ClaimEmailOutboxRow) Worker {
		return Worker{Store: &deliveryStoreFake{item: item}, Sender: &senderFake{err: errors.New("temporary failure")}, Now: func() time.Time { return now }}
	}
	for _, tc := range []struct {
		name  string
		item  dbgen.ClaimEmailOutboxRow
		check func(*deliveryStoreFake)
	}{
		{"missing verification token is permanent", dbgen.ClaimEmailOutboxRow{ID: uuid.New(), MessageType: "EMAIL_VERIFICATION", ExpiresAt: timestamp(now.Add(time.Hour))}, func(s *deliveryStoreFake) {
			if !s.failed || s.retried {
				t.Fatalf("failed=%v retried=%v", s.failed, s.retried)
			}
		}},
		{"unsupported type is permanent", dbgen.ClaimEmailOutboxRow{ID: uuid.New(), MessageType: "UNKNOWN", ExpiresAt: timestamp(now.Add(time.Hour))}, func(s *deliveryStoreFake) {
			if !s.failed {
				t.Fatal("unsupported type was not failed")
			}
		}},
		{"attempt limit is permanent", dbgen.ClaimEmailOutboxRow{ID: uuid.New(), MessageType: "EMAIL_VERIFICATION", VerificationTokenID: ptrUUID(uuid.New()), Attempts: MaxAttempts, ExpiresAt: timestamp(now.Add(time.Hour))}, func(s *deliveryStoreFake) {
			if !s.failed {
				t.Fatal("attempt limit was not failed")
			}
		}},
		{"expired before retry is permanent", dbgen.ClaimEmailOutboxRow{ID: uuid.New(), MessageType: "EMAIL_VERIFICATION", VerificationTokenID: ptrUUID(uuid.New()), Attempts: 1, ExpiresAt: timestamp(now.Add(time.Second))}, func(s *deliveryStoreFake) {
			if !s.failed {
				t.Fatal("expired message was not failed")
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			worker := base(tc.item)
			store := worker.Store.(*deliveryStoreFake)
			worker.drain(t.Context())
			tc.check(store)
		})
	}

	for _, tc := range []struct {
		name  string
		store *deliveryStoreFake
	}{
		{"claim database failure", &deliveryStoreFake{claimErr: errors.New("database unavailable")}},
		{"completion database failure", &deliveryStoreFake{item: dbgen.ClaimEmailOutboxRow{ID: uuid.New(), MessageType: "EMAIL_VERIFICATION", VerificationTokenID: ptrUUID(uuid.New()), ExpiresAt: timestamp(now.Add(time.Hour))}, completeErr: errors.New("database unavailable")}},
		{"retry database failure", &deliveryStoreFake{item: dbgen.ClaimEmailOutboxRow{ID: uuid.New(), MessageType: "EMAIL_VERIFICATION", VerificationTokenID: ptrUUID(uuid.New()), ExpiresAt: timestamp(now.Add(time.Hour))}, retryErr: errors.New("database unavailable")}},
		{"failure database failure", &deliveryStoreFake{item: dbgen.ClaimEmailOutboxRow{ID: uuid.New(), MessageType: "UNKNOWN", ExpiresAt: timestamp(now.Add(time.Hour))}, failErr: errors.New("database unavailable")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sender := &senderFake{}
			if tc.name == "retry database failure" {
				sender.err = errors.New("temporary failure")
			}
			Worker{Store: tc.store, Sender: sender, Now: func() time.Time { return now }}.drain(t.Context())
		})
	}
}

func TestWorkerRunStopsImmediatelyWhenContextIsAlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	store := &deliveryStoreFake{}
	done := make(chan struct{})
	go func() {
		Worker{Store: store}.Run(ctx)
		close(done)
	}()
	select {
	case <-done:
		if !store.cancelled {
			t.Fatal("worker did not perform safe outbox cleanup")
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not stop after cancellation")
	}
}

func ptrUUID(value uuid.UUID) *uuid.UUID { return &value }
