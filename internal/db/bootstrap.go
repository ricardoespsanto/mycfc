package db

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
)

//go:embed schema.sql
var baselineSchema string

//go:embed migrations/*.sql
var migrationFiles embed.FS

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
		{"grant database access", "GRANT CONNECT, CREATE ON DATABASE " + database + " TO " + migration},
		{"grant app database access", "GRANT CONNECT ON DATABASE " + database + " TO " + app},
		{"revoke public schema access", "REVOKE ALL ON SCHEMA public FROM PUBLIC"},
		{"set public schema owner", "ALTER SCHEMA public OWNER TO " + migration},
		{"grant app schema usage", "GRANT USAGE ON SCHEMA public TO " + app},
		{"create metadata schema", "CREATE SCHEMA IF NOT EXISTS mycfc_meta AUTHORIZATION " + migration},
		{"set metadata schema owner", "ALTER SCHEMA mycfc_meta OWNER TO " + migration},
		{"revoke public metadata access", "REVOKE ALL ON SCHEMA mycfc_meta FROM PUBLIC"},
		{"transfer existing schema objects", transferOwnershipStatement(credentials.MigrationUsername)},
		{"grant existing tables", "GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO " + app},
		{"grant existing sequences", "GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO " + app},
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

	if _, err := tx.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS mycfc_meta"); err != nil {
		return fmt.Errorf("create migration metadata schema: %w", err)
	}
	if _, err := tx.Exec(ctx, "CREATE TABLE IF NOT EXISTS mycfc_meta.schema_migrations (version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())"); err != nil {
		return fmt.Errorf("create migration marker: %w", err)
	}

	var installed bool
	if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM mycfc_meta.schema_migrations WHERE version = $1)", baselineVersion).Scan(&installed); err != nil {
		return fmt.Errorf("check baseline migration: %w", err)
	}
	if installed {
		if err := applyIncrementalMigrations(ctx, tx); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}

	var objectCount int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p', 'v', 'm', 'S', 'f')`).Scan(&objectCount); err != nil {
		return fmt.Errorf("inspect public schema: %w", err)
	}
	if objectCount != 0 {
		if err := verifyLegacyBaseline(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "INSERT INTO mycfc_meta.schema_migrations (version) VALUES ($1)", baselineVersion); err != nil {
			return fmt.Errorf("record adopted baseline migration: %w", err)
		}
		if err := applyIncrementalMigrations(ctx, tx); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if _, err := tx.Conn().PgConn().Exec(ctx, baselineSchema).ReadAll(); err != nil {
		return fmt.Errorf("apply reset baseline: %w", err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO mycfc_meta.schema_migrations (version) VALUES ($1)", baselineVersion); err != nil {
		return fmt.Errorf("record baseline migration: %w", err)
	}
	if err := applyIncrementalMigrations(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit baseline migration: %w", err)
	}
	return nil
}

func verifyLegacyBaseline(ctx context.Context, tx pgx.Tx) error {
	requiredTables := []string{"users", "training_session_outcomes", "sessions"}
	for _, table := range requiredTables {
		var exists bool
		if err := tx.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", "public."+table).Scan(&exists); err != nil {
			return fmt.Errorf("inspect legacy baseline table %s: %w", table, err)
		}
		if !exists {
			return fmt.Errorf("refusing to adopt unmarked public schema without %s table", table)
		}
	}
	return nil
}

func applyIncrementalMigrations(ctx context.Context, tx pgx.Tx) error {
	entries, err := fs.Glob(migrationFiles, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	for _, name := range entries {
		version := migrationVersion(name)
		var installed bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM mycfc_meta.schema_migrations WHERE version = $1)", version).Scan(&installed); err != nil {
			return fmt.Errorf("check migration %s: %w", version, err)
		}
		if installed {
			continue
		}
		sql, err := migrationFiles.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", version, err)
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("apply migration %s: %w", version, err)
		}
		if _, err := tx.Exec(ctx, "INSERT INTO mycfc_meta.schema_migrations (version) VALUES ($1)", version); err != nil {
			return fmt.Errorf("record migration %s: %w", version, err)
		}
	}
	return nil
}

func migrationVersion(path string) string {
	path = strings.TrimPrefix(path, "migrations/")
	return strings.TrimSuffix(path, ".sql")
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
		"CREATE ROLE " + quoteIdentifier(username) + " LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION; END IF; " +
		"ALTER ROLE " + quoteIdentifier(username) + " WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION PASSWORD " + quoteLiteral(password) + "; END $$"
}

func transferOwnershipStatement(migrationUsername string) string {
	owner := quoteLiteral(migrationUsername)
	return `DO $$ DECLARE object record; BEGIN
		FOR object IN SELECT n.nspname, c.relname, c.relkind FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname IN ('public', 'mycfc_meta') AND c.relkind IN ('r', 'p', 'S', 'v', 'm', 'f') AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_class'::regclass AND d.objid = c.oid AND d.deptype = 'e') LOOP
			EXECUTE format(CASE object.relkind WHEN 'S' THEN 'ALTER SEQUENCE %I.%I OWNER TO %I' WHEN 'v' THEN 'ALTER VIEW %I.%I OWNER TO %I' WHEN 'm' THEN 'ALTER MATERIALIZED VIEW %I.%I OWNER TO %I' WHEN 'f' THEN 'ALTER FOREIGN TABLE %I.%I OWNER TO %I' ELSE 'ALTER TABLE %I.%I OWNER TO %I' END, object.nspname, object.relname, ` + owner + `);
		END LOOP;
		FOR object IN SELECT n.nspname, p.proname, pg_get_function_identity_arguments(p.oid) AS arguments FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace WHERE n.nspname = 'public' AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_proc'::regclass AND d.objid = p.oid AND d.deptype = 'e') LOOP
			EXECUTE format('ALTER FUNCTION %I.%I(%s) OWNER TO %I', object.nspname, object.proname, object.arguments, ` + owner + `);
		END LOOP;
		FOR object IN SELECT n.nspname, t.typname FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace WHERE n.nspname = 'public' AND t.typtype IN ('d', 'e') AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_type'::regclass AND d.objid = t.oid AND d.deptype = 'e') LOOP
			EXECUTE format('ALTER TYPE %I.%I OWNER TO %I', object.nspname, object.typname, ` + owner + `);
		END LOOP;
	END $$`
}

func quoteIdentifier(value string) string { return `"` + strings.ReplaceAll(value, `"`, `""`) + `"` }
func quoteLiteral(value string) string    { return `'` + strings.ReplaceAll(value, `'`, `''`) + `'` }
