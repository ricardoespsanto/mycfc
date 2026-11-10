//go:build integration

package app

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// New group eligibility is a dated placement, not a legacy roster attachment.
// Use the real independently authenticated publisher and guarded JOIN command.
func joinFreshAppTrainingPlacement(t *testing.T, dsn string, actor, subject, programme, season, group uuid.UUID) {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	tx, err := conn.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var revision int64
	if err := tx.QueryRow(t.Context(), `SELECT COALESCE((SELECT revision FROM training_placement_heads WHERE subject_user_id=$1 AND programme_id=$2),0)`, subject, programme).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `SELECT * FROM apply_training_placement_change($1,$2,$3,$4,$5,'JOIN',NULL,$6,(clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date,NULL,$7,$8)`, actor, subject, programme, season, uuid.New(), group, revision, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
}
