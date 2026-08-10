package emailverification

import (
	"bytes"
	"context"
	"errors"
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
}

func (f *deliveryStoreFake) ClaimEmailOutbox(context.Context, dbgen.ClaimEmailOutboxParams) (dbgen.ClaimEmailOutboxRow, error) {
	if f.claimed {
		return dbgen.ClaimEmailOutboxRow{}, pgx.ErrNoRows
	}
	f.claimed = true
	return f.item, nil
}
func (f *deliveryStoreFake) CompleteEmailOutbox(context.Context, dbgen.CompleteEmailOutboxParams) (int64, error) {
	f.completed = true
	return 1, nil
}
func (f *deliveryStoreFake) RetryEmailOutbox(context.Context, dbgen.RetryEmailOutboxParams) (int64, error) {
	f.retried = true
	return 1, nil
}
func (f *deliveryStoreFake) FailEmailOutbox(context.Context, dbgen.FailEmailOutboxParams) (int64, error) {
	f.failed = true
	return 1, nil
}
func (f *deliveryStoreFake) CancelUndeliverableEmailOutbox(context.Context, pgtype.Timestamptz) (int64, error) {
	f.cancelled = true
	return 0, nil
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
	worker := Worker{Store: store, Sender: sender, PasswordReset: resetService, Now: func() time.Time { return now }}
	worker.drain(context.Background())
	if !store.cancelled || !store.completed || !strings.HasPrefix(sender.link, "https://mycfc.example/recuperar-palavra-passe/repor?token=") {
		t.Fatalf("reset delivery = cancelled:%v completed:%v link:%q", store.cancelled, store.completed, sender.link)
	}

	invalid := &deliveryStoreFake{item: dbgen.ClaimEmailOutboxRow{ID: uuid.New(), MessageType: "PASSWORD_RESET", PasswordResetTokenID: &resetID, SealedPayload: []byte("invalid"), Email: "member@example.test", Attempts: 1, ExpiresAt: timestamp(now.Add(time.Hour))}}
	Worker{Store: invalid, Sender: &senderFake{}, PasswordReset: resetService, Now: func() time.Time { return now }}.drain(context.Background())
	if !invalid.failed || invalid.retried || invalid.completed {
		t.Fatalf("invalid payload state = failed:%v retried:%v completed:%v", invalid.failed, invalid.retried, invalid.completed)
	}
}
