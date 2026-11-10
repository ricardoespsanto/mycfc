package handlers

import (
	"context"
	"errors"
	"testing"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestPostgresPublicationConfirmRequiresVerifiedReviewAndIndependentPool(t *testing.T) {
	store := PostgresStructuredTrainingStore{Pool: structuredTrainingDBFake{err: errors.New("unexpected database access")}}
	_, input, _ := publicationTransactionFixture(t)
	unverified := input
	unverified.Review.verified = false
	result, err := store.ConfirmPublication(t.Context(), unverified)
	if !errors.Is(err, errTrainingPublicationReviewInvalid) || result.PublicationID != uuid.Nil {
		t.Fatal("unverified review reached transaction admission", err)
	}
	result, err = store.ConfirmPublication(t.Context(), input)
	if !errors.Is(err, errPublicationSourceUnavailable) || result.PublicationID != uuid.Nil {
		t.Fatal("a query facade substituted for the independent publisher pool", err)
	}
}

type publicationFinalTupleRow struct {
	recipients, prescriptions []byte
	overflow                  bool
	err                       error
}

func (r publicationFinalTupleRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[11].(*bool) = r.overflow
	*dest[12].(*[]byte) = r.recipients
	*dest[13].(*[]byte) = r.prescriptions
	return nil
}

type publicationFinalTupleQuery struct {
	dbgen.DBTX
	row publicationFinalTupleRow
}

func (q publicationFinalTupleQuery) QueryRow(context.Context, string, ...any) pgx.Row { return q.row }

func TestPostgresPublicationFinalWitnessRefusesMalformedOrOversizeTuples(t *testing.T) {
	unavailable := errPublicationSourceUnavailable
	limit := errTrainingPublicationReviewLimit
	storage := errors.New("synthetic witness read error")
	for _, tc := range []struct {
		name string
		row  publicationFinalTupleRow
		want error
	}{
		{"witness lookup failed", publicationFinalTupleRow{err: storage}, storage},
		{"oversize result refused before decoding", publicationFinalTupleRow{recipients: make([]byte, publicationSourceMaxBytes+1)}, limit},
		{"unknown recipient properties", publicationFinalTupleRow{recipients: []byte(`[{"unexpected":1}]`)}, unavailable},
		{"trailing prescription data", publicationFinalTupleRow{recipients: []byte("[]"), prescriptions: []byte("[] []")}, unavailable},
		{"null recipient set is not an empty array", publicationFinalTupleRow{recipients: []byte("null"), prescriptions: []byte("[]")}, unavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := publicationPostgresQueries{queries: dbgen.New(publicationFinalTupleQuery{row: tc.row})}
			_, err := q.FinalFacts(t.Context(), uuid.New(), uuid.New(), uuid.New())
			if !errors.Is(err, tc.want) {
				t.Fatal("invalid final witness did not fail closed", err)
			}
		})
	}
	q := publicationPostgresQueries{queries: dbgen.New(publicationFinalTupleQuery{row: publicationFinalTupleRow{overflow: true}})}
	facts, err := q.FinalFacts(t.Context(), uuid.New(), uuid.New(), uuid.New())
	if err != nil || !facts.Overflow || len(facts.RecipientTuples) != 0 || len(facts.PublicationTuples) != 0 {
		t.Fatal("bounded overflow witness was not preserved", err)
	}
}
