package handlers

import (
	"errors"
	"testing"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestPublicationErrorUnknownOutranksConcurrencyAndCommittedCleanup(t *testing.T) {
	conflict := &pgconn.PgError{Code: "40001"}
	if err := publicationTransactionError(db.SessionTxResult{Outcome: db.SessionTxOutcomeUnknown}, conflict); !errors.Is(err, db.ErrCommitOutcomeUnknown) || errors.Is(err, errStructuredTrainingPublicationConflict) {
		t.Fatal("unknown normalized as rollback conflict", err)
	}
	wrapped := errors.Join(db.ErrCommitOutcomeUnknown, conflict)
	if err := publicationTransactionError(db.SessionTxResult{Outcome: db.SessionTxNotCommitted}, wrapped); !errors.Is(err, db.ErrCommitOutcomeUnknown) {
		t.Fatal("wrapped unknown lost priority")
	}
	if err := publicationTransactionError(db.SessionTxResult{Outcome: db.SessionTxCommitted, CleanupFailed: true, Discarded: true}, db.ErrSessionAdmissionCleanup); err != nil {
		t.Fatal("known committed result downgraded by cleanup", err)
	}
	for _, code := range []string{"40001", "40P01"} {
		if err := publicationTransactionError(db.SessionTxResult{}, &pgconn.PgError{Code: code}); !errors.Is(err, errStructuredTrainingPublicationConflict) {
			t.Fatal("known transaction conflict not retained", err)
		}
	}
	unrelated := &pgconn.PgError{Code: "23505", ConstraintName: "unrelated_unique"}
	if err := publicationTransactionError(db.SessionTxResult{}, unrelated); err != unrelated {
		t.Fatal("unrelated uniqueness hidden as stale review")
	}
	if err := publicationTransactionError(db.SessionTxResult{}, &pgconn.PgError{Code: "23505", ConstraintName: "training_plan_publications_revision_unique"}); !errors.Is(err, errStructuredTrainingPublicationConflict) {
		t.Fatal("revision collision not review conflict")
	}
}

func TestPublicationNowaitErrorIsStageSpecific(t *testing.T) {
	busy := &pgconn.PgError{Code: "55P03"}
	if !errors.Is(publicationNowaitError(busy), errStructuredTrainingPublicationConflict) {
		t.Fatal("NOWAIT failed to request review")
	}
	if publicationTransactionError(db.SessionTxResult{}, busy) != busy {
		t.Fatal("unrelated lock error changed into review conflict")
	}
}

func TestPublicationDatabaseSnapshotLimitIsSpecific(t *testing.T) {
	if err := publicationTransactionError(db.SessionTxResult{}, &pgconn.PgError{Code: "23514", ConstraintName: "training_prescriptions_snapshot_valid"}); !errors.Is(err, errTrainingPublicationReviewLimit) {
		t.Fatal("actual snapshot limit not actionable", err)
	}
	unrelated := &pgconn.PgError{Code: "23514", ConstraintName: "unrelated_check"}
	if publicationTransactionError(db.SessionTxResult{}, unrelated) != unrelated {
		t.Fatal("unrelated invariant hidden")
	}
}
