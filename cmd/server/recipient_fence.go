package main

import (
	"context"
	"errors"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/jackc/pgx/v5"
)

type recipientFenceInput struct {
	OID   uint32
	Login bool
}

func requireRecipientBootstrapIdentity(ctx context.Context, c *pgx.Conn, expected string) error {
	var valid bool
	if err := c.QueryRow(ctx, `SELECT session_user::pg_catalog.text OPERATOR(pg_catalog.=) $1 AND current_user::pg_catalog.text OPERATOR(pg_catalog.=) $1 AND EXISTS(SELECT 1 FROM pg_catalog.pg_roles WHERE rolname OPERATOR(pg_catalog.=) session_user AND rolsuper)`, expected).Scan(&valid); err != nil || !valid {
		return errors.New("publisher fence requires original authenticated configured bootstrap")
	}
	return nil
}
func setRecipientPublisherLogin(ctx context.Context, c *pgx.Conn, name string, oid uint32, login bool) (db.RecipientPublisherState, error) {
	var zero db.RecipientPublisherState
	tx, err := c.Begin(ctx)
	if err != nil {
		return zero, err
	}
	defer tx.Rollback(ctx)
	before, err := db.InspectTrainingRecipientPublisher(ctx, tx, name)
	if err != nil {
		return zero, err
	}
	if before.OID != oid || before.UnexpectedPublisherCount != 0 {
		return zero, errors.New("publisher role identity changed")
	}
	clause := "NOLOGIN"
	if login {
		clause = "LOGIN"
	}
	if _, err = tx.Exec(ctx, "ALTER ROLE "+pgx.Identifier{name}.Sanitize()+" "+clause); err != nil {
		return zero, errors.New("publisher LOGIN change failed")
	}
	after, err := db.InspectTrainingRecipientPublisher(ctx, tx, name)
	if err != nil {
		return zero, err
	}
	if after.OID != oid || after.CanLogin != login || after.UnexpectedPublisherCount != 0 {
		return zero, errors.New("publisher role acknowledgement changed")
	}
	if err = tx.Commit(ctx); err != nil {
		return zero, errors.New("publisher LOGIN commit not acknowledged; inspect current state before retry")
	}
	return after, nil
}
