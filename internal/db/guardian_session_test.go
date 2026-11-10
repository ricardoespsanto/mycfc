package db

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestGuardianSessionOptionsAndFacade(t *testing.T) {
	body := func(context.Context, dbgen.DBTX) error { t.Fatal("invalid helper invoked body"); return nil }
	result, err := WithRecipientPublicationTx(t.Context(), nil, RecipientTxOptions{time.Second, time.Second, time.Second}, body)
	if err == nil || result.Outcome != SessionTxNotCommitted {
		t.Fatal("invalid options accepted")
	}
	if _, err = WithGuardianPolicyWriter(t.Context(), nil, PolicyWriterOptions{time.Second, time.Second}, func(context.Context, *pgx.Conn) error { return nil }); err == nil {
		t.Fatal("nil original accepted")
	}
	facade := reflect.TypeOf(guardianTxCommands{})
	if facade.NumMethod() != 3 {
		t.Fatal("command facade exposes lifecycle methods")
	}
	for _, name := range []string{"Begin", "Commit", "Rollback", "Conn", "Close"} {
		if _, ok := facade.MethodByName(name); ok {
			t.Fatalf("facade exposes %s", name)
		}
	}
	if (guardianCommandRows{}).Conn() != nil {
		t.Fatal("rows expose connection")
	}
}

func TestGuardianSessionCommitClassification(t *testing.T) {
	for _, err := range []error{context.Canceled, errors.Join(pgx.ErrTxCommitRollback, context.Canceled), errors.New("network failure"), &pgconn.PgError{Code: "40001"}, &pgconn.PgError{Code: "40P01"}, errors.Join(&pgconn.PgError{Code: "40001"}, context.Canceled)} {
		if recipientCommitNotCommitted(err, nil) {
			t.Fatalf("unacknowledged %T classified rolled back", err)
		}
	}
	if !recipientCommitNotCommitted(pgx.ErrTxCommitRollback, nil) {
		t.Fatal("acknowledged rollback not recognized")
	}
}
