package handlers

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestStructuredPublicationConcurrencyErrorsAreConflicts(t *testing.T) {
	for _, code := range []string{"40001", "40P01"} {
		t.Run(code, func(t *testing.T) {
			if err := structuredPublicationConcurrencyError(&pgconn.PgError{Code: code}); !errors.Is(err, errStructuredTrainingPublicationConflict) {
				t.Fatalf("SQLSTATE %s: %v", code, err)
			}
		})
	}
	other := &pgconn.PgError{Code: "23514"}
	if got := structuredPublicationConcurrencyError(other); got != other {
		t.Fatalf("unrelated database failure remapped: %v", got)
	}
}
