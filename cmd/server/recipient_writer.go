package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/jackc/pgx/v5"
)

const (
	recipientCLIAdmissionTimeout  = 30 * time.Second
	recipientCLIInspectionTimeout = 30 * time.Second
	recipientCLIOperationTimeout  = 120 * time.Second
	recipientCLICleanupTimeout    = 5 * time.Second
)

// Unit tests may inject control-flow observation. Production requires the
// original actual physical pgx connection; there is no environment bypass or
// fallback for a connection that cannot own the session admission lifecycle.
var runOriginalRecipientWriter = withOriginalRecipientWriter

func withOriginalRecipientWriter(ctx context.Context, conn databaseCommandConnection, body func(context.Context) error) error {
	return withRecipientPolicySession(ctx, conn, func(op context.Context) error {
		if err := requireRecipientMutationAdmission(op, conn); err != nil {
			return err
		}
		if err := body(op); err != nil {
			return err
		}
		original, ok := conn.(*pgx.Conn)
		if ok && original.Config().User == os.Getenv("POSTGRES_USER") {
			return completeLocalRecipientBootstrap(op, original)
		}
		return nil
	})
}

func withRecipientPolicySession(ctx context.Context, conn databaseCommandConnection, body func(context.Context) error) error {
	original, ok := conn.(*pgx.Conn)
	if !ok || body == nil {
		return errors.New("database mutation requires its original physical connection")
	}
	operationCtx, cancel := context.WithTimeout(ctx, recipientCLIOperationTimeout)
	defer cancel()
	result, err := db.WithGuardianPolicyWriter(operationCtx, original, db.PolicyWriterOptions{AdmissionTimeout: recipientCLIAdmissionTimeout, CleanupTimeout: recipientCLICleanupTimeout}, func(operationCtx context.Context, owned *pgx.Conn) error {
		if owned != original {
			return errors.New("database mutation connection changed")
		}
		return body(operationCtx)
	})
	if result.OperationCompleted && err != nil {
		return fmt.Errorf("database operation completed; connection cleanup failed; inspect current state: %w", err)
	}
	// Earlier body autocommits can survive even when the operation did not finish.
	// Preserve its error without claiming that the entire command rolled back.
	return err
}
