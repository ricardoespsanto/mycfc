package handlers

import (
	"errors"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/jackc/pgx/v5/pgconn"
)

func publicationTransactionError(outcome db.SessionTxResult, err error) error {
	// A wrapped server error is not a rollback proof after an ambiguous COMMIT.
	if outcome.Outcome == db.SessionTxOutcomeUnknown || errors.Is(err, db.ErrCommitOutcomeUnknown) {
		if err == nil {
			return db.ErrCommitOutcomeUnknown
		}
		return errors.Join(db.ErrCommitOutcomeUnknown, err)
	}
	if outcome.Outcome == db.SessionTxCommitted {
		return nil
	}
	if err == nil {
		return errPublicationSourceUnavailable
	}
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) {
		switch pgError.Code {
		case "40001", "40P01":
			return errStructuredTrainingPublicationConflict
		case "23514":
			if pgError.ConstraintName == "training_prescriptions_snapshot_valid" {
				return errTrainingPublicationReviewLimit
			}
		case "23505":
			if pgError.ConstraintName == "training_plan_publications_revision_unique" {
				return errStructuredTrainingPublicationConflict
			}
		}
	}
	return trainingScheduleWriteError(err)
}

// Only the intentional recipient/event NOWAIT stages use this55P03 mapping.
func publicationNowaitError(err error) error {
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) && pgError.Code == "55P03" {
		return errStructuredTrainingPublicationConflict
	}
	return err
}
