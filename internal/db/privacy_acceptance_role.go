package db

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
)

const PrivacyAcceptanceRole = "mycfc_privacy_acceptance"

// ConfigurePrivacyAcceptanceRole is an explicit operator action. No migration
// creates a login, consumes its password or invokes this function.
func ConfigurePrivacyAcceptanceRole(ctx context.Context, conn interface {
	Begin(context.Context) (pgx.Tx, error)
}, expectedDatabase, password string, revoke bool) error {
	if !postgresIdentifier.MatchString(expectedDatabase) || (!revoke && (len(password) < 32 || strings.ContainsAny(password, "\x00\r\n"))) {
		return errors.New("acceptance credential rejected")
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var actual string
	if err = tx.QueryRow(ctx, "SELECT current_database()").Scan(&actual); err != nil || actual != expectedDatabase {
		return errors.New("acceptance database binding rejected")
	}
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(274,5)"); err != nil {
		return err
	}
	var exists bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)", PrivacyAcceptanceRole).Scan(&exists); err != nil {
		return err
	}
	if !exists && revoke {
		return tx.Commit(ctx)
	}
	if !exists {
		if _, err = tx.Exec(ctx, "CREATE ROLE mycfc_privacy_acceptance NOLOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS"); err != nil {
			return err
		}
	}
	// Never repurpose an object owner or retain any role membership. SQL uses a
	// fixed role name; only database identifiers pass through identifier quoting.
	statements := []string{
		`DO $$BEGIN IF EXISTS(SELECT 1 FROM pg_shdepend WHERE refclassid='pg_authid'::regclass AND refobjid=(SELECT oid FROM pg_roles WHERE rolname='mycfc_privacy_acceptance') AND deptype='o') THEN RAISE EXCEPTION 'acceptance_role_owns_objects'; END IF; END$$`,
		`ALTER ROLE mycfc_privacy_acceptance NOLOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD NULL`,
		`DO $$DECLARE r record;BEGIN FOR r IN SELECT parent.rolname FROM pg_auth_members membership JOIN pg_roles parent ON parent.oid=membership.roleid JOIN pg_roles child ON child.oid=membership.member WHERE child.rolname='mycfc_privacy_acceptance' LOOP EXECUTE format('REVOKE %I FROM mycfc_privacy_acceptance',r.rolname); END LOOP; FOR r IN SELECT child.rolname FROM pg_auth_members membership JOIN pg_roles parent ON parent.oid=membership.roleid JOIN pg_roles child ON child.oid=membership.member WHERE parent.rolname='mycfc_privacy_acceptance' LOOP EXECUTE format('REVOKE mycfc_privacy_acceptance FROM %I',r.rolname); END LOOP; END$$`,
		`REVOKE USAGE ON SCHEMA public FROM PUBLIC`,
		"REVOKE TEMPORARY ON DATABASE " + quoteIdentifier(expectedDatabase) + " FROM PUBLIC",
		`DO $$DECLARE s record;BEGIN FOR s IN SELECT nspname FROM pg_namespace WHERE nspname NOT LIKE 'pg_%' AND nspname<>'information_schema' LOOP EXECUTE format('REVOKE ALL ON SCHEMA %I FROM mycfc_privacy_acceptance',s.nspname); EXECUTE format('REVOKE ALL ON ALL TABLES IN SCHEMA %I FROM mycfc_privacy_acceptance',s.nspname); EXECUTE format('REVOKE ALL ON ALL SEQUENCES IN SCHEMA %I FROM mycfc_privacy_acceptance',s.nspname); EXECUTE format('REVOKE ALL ON ALL FUNCTIONS IN SCHEMA %I FROM mycfc_privacy_acceptance',s.nspname); END LOOP; END$$`,
		"REVOKE ALL ON DATABASE " + quoteIdentifier(expectedDatabase) + " FROM mycfc_privacy_acceptance",
	}
	if !revoke {
		statements = append(statements,
			"GRANT CONNECT ON DATABASE "+quoteIdentifier(expectedDatabase)+" TO mycfc_privacy_acceptance",
			`GRANT USAGE ON SCHEMA privacy_protected TO mycfc_privacy_acceptance`,
			`GRANT EXECUTE ON FUNCTION privacy_protected.acceptance_create(text,text,text),privacy_protected.acceptance_observe(uuid,bytea),privacy_protected.acceptance_finish(uuid,bytea) TO mycfc_privacy_acceptance`,
			roleStatement(PrivacyAcceptanceRole, password),
		)
	}
	for _, sql := range statements {
		if _, err = tx.Exec(ctx, sql); err != nil {
			return errors.New("acceptance role configuration failed")
		}
	}
	// PostgreSQL backend termination is deliberately part of rotation/revocation;
	// sessions authenticated with the previous credential cannot outlive it.
	if _, err = tx.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename='mycfc_privacy_acceptance' AND pid<>pg_backend_pid()`); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	// Terminate again after the password/NOLOGIN change commits. A connection
	// authenticated just before commit must not survive the rotation window.
	cleanup, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer cleanup.Rollback(ctx)
	if _, err = cleanup.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename='mycfc_privacy_acceptance' AND pid<>pg_backend_pid()`); err != nil {
		return err
	}
	return cleanup.Commit(ctx)
}
