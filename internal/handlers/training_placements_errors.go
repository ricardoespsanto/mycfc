package handlers

import (
	"errors"
	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var errTrainingPlacementUnavailable = errors.New("training placement runtime unavailable")

func trainingPlacementTransactionError(result db.SessionTxResult, err error) error {
	if result.Outcome == db.SessionTxOutcomeUnknown || errors.Is(err, db.ErrCommitOutcomeUnknown) {
		return errors.Join(db.ErrCommitOutcomeUnknown, err)
	}
	if result.Outcome == db.SessionTxCommitted {
		return nil
	}
	if err == nil {
		return errTrainingPlacementUnavailable
	}
	var p *pgconn.PgError
	if errors.As(err, &p) {
		switch p.Code {
		case "42501":
			return pgx.ErrNoRows
		case "40001", "55P03", "40P01", "23P01":
			return errTrainingPlacementChanged
		case "22023":
			return errTrainingPlacementInvalid
		case "54000":
			return errTrainingPlacementLimit
		case "55000":
			return errTrainingPlacementUnavailable
		case "23514":
			switch p.Message {
			case "training_placement_started_boundary_unresolved":
				return errTrainingPlacementPolicy
			case "training_placement_return_unavailable":
				return errTrainingPlacementReturn
			default:
				return &trainingPlacementFieldFailure{Field: "form", Message: "Não foi possível confirmar as datas ou a elegibilidade. Reveja a colocação."}
			}
		}
	}
	return err
}
