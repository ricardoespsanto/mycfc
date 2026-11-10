package main

import (
	"context"
	"errors"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type recipientFloorInput struct {
	Incarnation, Commit uuid.UUID
	Epoch               int64
	Manifest            string
	Reader              int32
}

func commitRecipientFloor(ctx context.Context, conn *pgx.Conn, inspected db.RecipientDatabaseInspection, in recipientFloorInput) (db.TrainingScheduleState, error) {
	var zero db.TrainingScheduleState
	s := inspected.ScheduleState
	if inspected.Phase != db.RecipientFinal || s == nil || s.DatabaseIncarnation != in.Incarnation || s.StateEpoch != in.Epoch || in.Reader < s.RequiredReader {
		return zero, errors.New("floor inspected state changed")
	}
	if in.Reader > s.RequiredReader && (inspected.Publisher.CanLogin || inspected.Publisher.BackendCount != 0 || inspected.Publisher.PreparedCount != 0 || inspected.Publisher.UnexpectedPublisherCount != 0) {
		return zero, errors.New("floor generation raise requires publisher NOLOGIN and drain")
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return zero, err
	}
	defer tx.Rollback(ctx)
	var epoch int64
	if err = tx.QueryRow(ctx, `SELECT training_schedule_control.commit_floor($1,$2,$3,$4,$5)`, in.Incarnation, in.Epoch, in.Commit, in.Manifest, in.Reader).Scan(&epoch); err != nil {
		return zero, err
	}
	state, err := db.TrainingScheduleStatus(ctx, tx)
	if err != nil {
		return zero, err
	}
	if state.StateEpoch != in.Epoch+1 || epoch != state.StateEpoch || state.RequiredReader != in.Reader || state.FloorCommitID == nil || *state.FloorCommitID != in.Commit || state.FloorManifestSHA256 == nil || *state.FloorManifestSHA256 != in.Manifest {
		return zero, errors.New("floor acknowledgement mismatch")
	}
	if err = tx.Commit(ctx); err != nil {
		return zero, errors.New("floor commit not acknowledged; inspect current state before retry")
	}
	return state, nil
}
