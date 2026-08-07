package emailverification

import (
	"context"
	"errors"
	"net/textproto"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type deliveryStoreFake struct {
	item                       dbgen.ClaimEmailOutboxRow
	claimed                    bool
	completed, retried, failed bool
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

func TestWorkerCompletesSuccessfulDelivery(t *testing.T) {
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	tokenID := uuid.New()
	store := &deliveryStoreFake{item: dbgen.ClaimEmailOutboxRow{ID: uuid.New(), VerificationTokenID: tokenID, Email: "member@example.test", Attempts: 1, ExpiresAt: timestamp(now.Add(time.Hour))}}
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
			store := &deliveryStoreFake{item: dbgen.ClaimEmailOutboxRow{ID: uuid.New(), VerificationTokenID: uuid.New(), Email: "member@example.test", Attempts: 1, ExpiresAt: timestamp(now.Add(time.Hour))}}
			worker := Worker{Store: store, Sender: &senderFake{err: tc.err}, Service: Service{BaseURL: "https://mycfc.example", Key: []byte("0123456789abcdef0123456789abcdef")}, Now: func() time.Time { return now }}
			worker.drain(context.Background())
			if store.retried != tc.retry || store.failed != tc.fail {
				t.Fatalf("retry=%v fail=%v", store.retried, store.failed)
			}
		})
	}
}
