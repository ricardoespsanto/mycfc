package handlers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type identityBeginnerFake struct {
	tx  pgx.Tx
	err error
}

func (b identityBeginnerFake) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	return b.tx, b.err
}

type identityTransactionFake struct {
	pgx.Tx
	err       error
	committed bool
}

func (tx *identityTransactionFake) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag("INSERT 0 1"), tx.err
}
func (tx *identityTransactionFake) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, tx.err
}
func (tx *identityTransactionFake) QueryRow(context.Context, string, ...any) pgx.Row {
	return identityRowFake{err: tx.err}
}
func (*identityTransactionFake) Rollback(context.Context) error  { return nil }
func (tx *identityTransactionFake) Commit(context.Context) error { tx.committed = true; return tx.err }

type identityRowFake struct{ err error }

func (r identityRowFake) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	for _, target := range dest {
		switch value := target.(type) {
		case *uuid.UUID:
			*value = uuid.New()
		case *int64:
			*value = 1
		case *pgtype.Timestamptz:
			*value = pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
		}
	}
	return nil
}

func TestIdentityStoresPropagateTransactionStartFailures(t *testing.T) {
	want := errors.New("database unavailable")
	registration := PostgresRegistrationStore{Pool: identityBeginnerFake{err: want}}
	if _, err := registration.RegisterAdult(t.Context(), RegistrationInput{}); !errors.Is(err, want) {
		t.Fatalf("registration error=%v", err)
	}
	guardian := PostgresGuardianDependentStore{Pool: identityBeginnerFake{err: want}}
	if err := guardian.CreateDependent(t.Context(), GuardianDependentInput{}); !errors.Is(err, want) {
		t.Fatalf("guardian error=%v", err)
	}
}

func TestIdentityStoresCommitCompleteRegistrationAndDependentTransactions(t *testing.T) {
	registrationTx := &identityTransactionFake{}
	result, err := (PostgresRegistrationStore{Pool: identityBeginnerFake{tx: registrationTx}}).RegisterAdult(t.Context(), RegistrationInput{Name: "Membro", Email: "member@example.test", PasswordHash: "hash", DateOfBirth: time.Now().AddDate(-20, 0, 0), TermsVersion: "v1", TermsSHA256: "hash"})
	if err != nil || result.UserID == uuid.Nil || !registrationTx.committed {
		t.Fatalf("registration=%#v committed=%v error=%v", result, registrationTx.committed, err)
	}
	guardianTx := &identityTransactionFake{}
	err = (PostgresGuardianDependentStore{Pool: identityBeginnerFake{tx: guardianTx}}).CreateDependent(t.Context(), GuardianDependentInput{Name: "Dependente", GuardianID: uuid.New(), DateOfBirth: time.Now().AddDate(-10, 0, 0), ResponsibilityVersion: "v1", ResponsibilitySHA256: "hash"})
	if err != nil || !guardianTx.committed {
		t.Fatalf("committed=%v error=%v", guardianTx.committed, err)
	}
}

var _ db.Beginner = identityBeginnerFake{}
