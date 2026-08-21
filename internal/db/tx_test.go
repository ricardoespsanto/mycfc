package db

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

type transactionFake struct {
	pgx.Tx
	commitErr  error
	committed  bool
	rolledBack bool
}

func (t *transactionFake) Commit(context.Context) error {
	t.committed = true
	return t.commitErr
}

func (t *transactionFake) Rollback(context.Context) error {
	t.rolledBack = true
	return nil
}

type beginnerFake struct {
	tx  pgx.Tx
	err error
}

func (b beginnerFake) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	return b.tx, b.err
}

func TestWithinTxCommitsOnlySuccessfulCallback(t *testing.T) {
	tx := &transactionFake{}
	called := false
	err := WithinTx(context.Background(), beginnerFake{tx: tx}, pgx.TxOptions{}, func(got pgx.Tx) error {
		called = got == tx
		return nil
	})
	if err != nil || !called || !tx.committed || tx.rolledBack {
		t.Fatalf("err=%v called=%t committed=%t rolledBack=%t", err, called, tx.committed, tx.rolledBack)
	}
}

func TestWithinTxRollsBackCallbackAndCommitFailures(t *testing.T) {
	callbackErr := errors.New("callback failed")
	t.Run("callback", func(t *testing.T) {
		tx := &transactionFake{}
		err := WithinTx(context.Background(), beginnerFake{tx: tx}, pgx.TxOptions{}, func(pgx.Tx) error { return callbackErr })
		if !errors.Is(err, callbackErr) || tx.committed || !tx.rolledBack {
			t.Fatalf("err=%v committed=%t rolledBack=%t", err, tx.committed, tx.rolledBack)
		}
	})
	t.Run("commit", func(t *testing.T) {
		commitErr := errors.New("commit failed")
		tx := &transactionFake{commitErr: commitErr}
		err := WithinTx(context.Background(), beginnerFake{tx: tx}, pgx.TxOptions{}, func(pgx.Tx) error { return nil })
		if !errors.Is(err, commitErr) || !tx.committed || !tx.rolledBack {
			t.Fatalf("err=%v committed=%t rolledBack=%t", err, tx.committed, tx.rolledBack)
		}
	})
}

func TestWithinTxRollsBackThenRepresentsPanic(t *testing.T) {
	tx := &transactionFake{}
	defer func() {
		if recovered := recover(); recovered != "boom" || !tx.rolledBack || tx.committed {
			t.Fatalf("panic=%v committed=%t rolledBack=%t", recovered, tx.committed, tx.rolledBack)
		}
	}()
	_ = WithinTx(context.Background(), beginnerFake{tx: tx}, pgx.TxOptions{}, func(pgx.Tx) error { panic("boom") })
}

func TestWithinTxWrapsBeginFailure(t *testing.T) {
	beginErr := errors.New("database unavailable")
	err := WithinTx(context.Background(), beginnerFake{err: beginErr}, pgx.TxOptions{}, func(pgx.Tx) error { return nil })
	if !errors.Is(err, beginErr) {
		t.Fatalf("err=%v", err)
	}
}
