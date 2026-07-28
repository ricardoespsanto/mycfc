package db

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

//go:embed schema.sql
var baselineSchema string

var postgresIdentifier = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,62}$`)

const baselineVersion = "reset-baseline-v1"

type RoleCredentials struct {
	AppUsername       string
	AppPassword       string
	MigrationUsername string
	MigrationPassword string
}

func BootstrapRoles(ctx context.Context, conn *pgx.Conn, databaseName string, credentials RoleCredentials) error {
	if err := validateBootstrapInput(databaseName, credentials); err != nil {
		return err
	}
	app := quoteIdentifier(credentials.AppUsername)
	migration := quoteIdentifier(credentials.MigrationUsername)
	database := quoteIdentifier(databaseName)

	statements := []struct{ name, sql string }{
		{"enable citext", "CREATE EXTENSION IF NOT EXISTS citext"},
		{"enable pgcrypto", "CREATE EXTENSION IF NOT EXISTS pgcrypto"},
		{"configure app role", roleStatement(credentials.AppUsername, credentials.AppPassword)},
		{"configure migration role", roleStatement(credentials.MigrationUsername, credentials.MigrationPassword)},
		{"grant migration membership", "GRANT " + migration + " TO CURRENT_USER"},
		{"revoke public database access", "REVOKE ALL ON DATABASE " + database + " FROM PUBLIC"},
		{"grant database access", "GRANT CONNECT ON DATABASE " + database + " TO " + app + ", " + migration},
		{"revoke public schema access", "REVOKE ALL ON SCHEMA public FROM PUBLIC"},
		{"set public schema owner", "ALTER SCHEMA public OWNER TO " + migration},
		{"grant app schema usage", "GRANT USAGE ON SCHEMA public TO " + app},
		{"create metadata schema", "CREATE SCHEMA IF NOT EXISTS mycfc_meta AUTHORIZATION " + migration},
		{"set metadata schema owner", "ALTER SCHEMA mycfc_meta OWNER TO " + migration},
		{"revoke public metadata access", "REVOKE ALL ON SCHEMA mycfc_meta FROM PUBLIC"},
		{"set table defaults", "ALTER DEFAULT PRIVILEGES FOR ROLE " + migration + " IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO " + app},
		{"set sequence defaults", "ALTER DEFAULT PRIVILEGES FOR ROLE " + migration + " IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO " + app},
	}
	for _, statement := range statements {
		if _, err := conn.Exec(ctx, statement.sql); err != nil {
			return fmt.Errorf("bootstrap database roles (%s): %w", statement.name, err)
		}
	}
	return nil
}

func ApplyBaseline(ctx context.Context, conn *pgx.Conn) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin baseline migration: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext('mycfc-reset-baseline'))"); err != nil {
		return fmt.Errorf("lock baseline migration: %w", err)
	}

	var installed bool
	err = tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM mycfc_meta.schema_migrations WHERE version = $1)", baselineVersion).Scan(&installed)
	if err == nil && installed {
		return tx.Commit(ctx)
	}
	if err != nil && !isUndefinedTable(err) {
		return fmt.Errorf("check baseline migration: %w", err)
	}

	var objectCount int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p', 'v', 'm', 'S', 'f')`).Scan(&objectCount); err != nil {
		return fmt.Errorf("inspect public schema: %w", err)
	}
	if objectCount != 0 {
		return errors.New("refusing to apply reset baseline to a non-empty unmarked public schema")
	}

	if _, err := tx.Exec(ctx, "CREATE TABLE mycfc_meta.schema_migrations (version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())"); err != nil {
		return fmt.Errorf("create baseline marker: %w", err)
	}
	if _, err := tx.Conn().PgConn().Exec(ctx, baselineSchema).ReadAll(); err != nil {
		return fmt.Errorf("apply reset baseline: %w", err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO mycfc_meta.schema_migrations (version) VALUES ($1)", baselineVersion); err != nil {
		return fmt.Errorf("record baseline migration: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit baseline migration: %w", err)
	}
	return nil
}

func validateBootstrapInput(databaseName string, credentials RoleCredentials) error {
	for name, value := range map[string]string{
		"database name":           databaseName,
		"app database user":       credentials.AppUsername,
		"migration database user": credentials.MigrationUsername,
	} {
		if !postgresIdentifier.MatchString(value) {
			return fmt.Errorf("%s must be a PostgreSQL identifier", name)
		}
	}
	if credentials.AppUsername == credentials.MigrationUsername {
		return errors.New("app and migration database users must differ")
	}
	if strings.TrimSpace(credentials.AppPassword) == "" || strings.TrimSpace(credentials.MigrationPassword) == "" {
		return errors.New("database role passwords must not be empty")
	}
	return nil
}

func roleStatement(username, password string) string {
	return "DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = " + quoteLiteral(username) + ") THEN " +
		"CREATE ROLE " + quoteIdentifier(username) + " LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION PASSWORD " + quoteLiteral(password) + "; END IF; END $$"
}

func quoteIdentifier(value string) string { return `"` + strings.ReplaceAll(value, `"`, `""`) + `"` }
func quoteLiteral(value string) string    { return `'` + strings.ReplaceAll(value, `'`, `''`) + `'` }

func isUndefinedTable(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42P01"
}
