package main

import (
	"context"
	"errors"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/cfcoimbra/mycfc/internal/releasecontract"
	"github.com/jackc/pgx/v5"
)

var connectFreshInspector = func(ctx context.Context, cfg *pgx.ConnConfig) (databaseCommandConnection, error) {
	return pgx.ConnectConfig(ctx, cfg)
}

// A safe inspection does not authorize executing migrations on a privileged
// original connection. Unstamped local explicit-connection behavior is retained.
func requireMigrationExecutionIdentity(ctx context.Context, conn databaseCommandConnection, migrationRole string) error {
	if releasecontract.Version == "" && releasecontract.Candidate == "" {
		return nil
	}
	var allowed bool
	err := conn.QueryRow(ctx, `SELECT session_user OPERATOR(pg_catalog.=) current_user
 AND session_user OPERATOR(pg_catalog.=) $1::pg_catalog.name AND NOT r.rolsuper
 FROM pg_catalog.pg_roles r WHERE r.rolname OPERATOR(pg_catalog.=) session_user`, migrationRole).Scan(&allowed)
	if err != nil || !allowed || migrationRole == "" {
		return errors.New("migration execution requires its authenticated non-superuser identity")
	}
	return nil
}

func inspectFreshInitialization(ctx context.Context, receipt databaseCommandConnection, roles db.RoleCredentials, database, version, candidate string) (bool, error) {
	// Preserve the old disposable path: it need not have a migration login yet.
	// This fixed catalog lookup never evaluates a migration-owned relation.
	var exists bool
	if err := receipt.QueryRow(ctx, `SELECT pg_catalog.to_regclass('mycfc_fresh_init.receipt') IS NOT NULL`).Scan(&exists); err != nil || !exists {
		return false, err
	}
	configured, ok := receipt.(interface{ Config() *pgx.ConnConfig })
	if !ok {
		// The compatibility API itself enforces authenticated non-superuser
		// inspection. There is no bootstrap privilege fallback.
		return db.FreshInitializationMatches(ctx, receipt, database, version, candidate)
	}
	cfg := configured.Config().Copy()
	if cfg.Database != database {
		return false, errors.New("fresh initialization database identity rejected")
	}
	if roles.MigrationUsername == "" {
		return false, errors.New("fresh initialization migration identity required")
	}
	if cfg.User == roles.MigrationUsername {
		return db.FreshInitializationMatchesWithInspector(ctx, receipt, receipt, roles.MigrationUsername, database, version, candidate)
	}
	if roles.MigrationPassword == "" {
		return false, errors.New("fresh initialization migration credentials required")
	}
	// Clone the authenticated receipt endpoint/TLS/database, changing only the
	// credentials. Never accept an independently supplied inspector URL.
	cfg.User, cfg.Password = roles.MigrationUsername, roles.MigrationPassword
	inspector, err := connectFreshInspector(ctx, cfg)
	if err != nil {
		return false, errors.New("fresh initialization migration connection rejected")
	}
	defer inspector.Close(context.WithoutCancel(ctx))
	return db.FreshInitializationMatchesWithInspector(ctx, receipt, inspector, roles.MigrationUsername, database, version, candidate)
}
