package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/cfcoimbra/mycfc/internal/releasecontract"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// FreshInitializeDatabase is an explicit, empty-database-only alternative to
// the predecessor-bound disposable replacement. It never drops or adopts data.
// Run it with the bootstrap superuser, using existing, operator-supplied role
// credentials. A separate `admin create` step provisions the first adult admin.
func FreshInitializeDatabase(ctx context.Context, conn interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	Begin(context.Context) (pgx.Tx, error)
}, database string, roles RoleCredentials) error {
	if err := validateBootstrapInput(database, roles); err != nil {
		return err
	}
	if strings.TrimSpace(roles.MediaCleanupUsername) == "" || strings.TrimSpace(roles.MediaCleanupPassword) == "" ||
		strings.TrimSpace(roles.DataRetentionUsername) == "" || strings.TrimSpace(roles.DataRetentionPassword) == "" {
		return errors.New("fresh initialization requires both maintenance login credentials")
	}
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock(hashtext('mycfc-explicit-fresh-init'))"); err != nil {
		return fmt.Errorf("lock fresh initialization: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock(hashtext('mycfc-explicit-fresh-init'))")
	}()
	var allowed bool
	if err := conn.QueryRow(ctx, `SELECT current_database()=$1 AND
  (SELECT rolsuper FROM pg_roles WHERE rolname=current_user) AND
  NOT EXISTS(SELECT 1 FROM pg_depend d WHERE d.refclassid='pg_namespace'::regclass
    AND d.refobjid='public'::regnamespace) AND
  NOT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname NOT IN ('public','pg_catalog','information_schema')
    AND nspname NOT LIKE 'pg_toast%' AND nspname NOT LIKE 'pg_temp%') AND
  NOT EXISTS(SELECT 1 FROM pg_extension WHERE extnamespace='public'::regnamespace)`, database).Scan(&allowed); err != nil {
		return fmt.Errorf("inspect empty database: %w", err)
	}
	if !allowed {
		return errors.New("fresh initialization refused: requires the named empty database and bootstrap superuser; existing databases use the separate guarded upgrade path")
	}
	if err := BootstrapRoles(ctx, conn, database, roles); err != nil {
		return fmt.Errorf("fresh bootstrap roles: %w", err)
	}
	if _, err := conn.Exec(ctx, "SET ROLE "+quoteIdentifier(roles.MigrationUsername)); err != nil {
		return fmt.Errorf("assume migration role: %w", err)
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), "RESET ROLE") }()
	if err := ApplyBaselineAndHarden(ctx, conn, database, roles); err != nil {
		return fmt.Errorf("fresh baseline and migrations: %w", err)
	}
	if _, err := conn.Exec(ctx, "RESET ROLE"); err != nil {
		return fmt.Errorf("restore bootstrap role: %w", err)
	}
	// This private receipt is NOT a disposable-reset completion marker. Its
	// superuser-owned rows are read-only for the migration identity.
	for _, sql := range []string{
		"CREATE SCHEMA mycfc_fresh_init",
		"REVOKE ALL ON SCHEMA mycfc_fresh_init FROM PUBLIC",
		"CREATE TABLE mycfc_fresh_init.receipt(database_name text PRIMARY KEY, version text NOT NULL, candidate text NOT NULL, schema_digest text NOT NULL)",
		"REVOKE ALL ON TABLE mycfc_fresh_init.receipt FROM PUBLIC",
		"GRANT USAGE ON SCHEMA mycfc_fresh_init TO " + quoteIdentifier(roles.MigrationUsername),
		"GRANT SELECT ON mycfc_fresh_init.receipt TO " + quoteIdentifier(roles.MigrationUsername),
	} {
		if _, err := conn.Exec(ctx, sql); err != nil {
			return fmt.Errorf("record private fresh initialization receipt: %w", err)
		}
	}
	if _, err := conn.Exec(ctx, `INSERT INTO mycfc_fresh_init.receipt(database_name,version,candidate,schema_digest) VALUES($1,$2,$3,$4)`, database, releasecontract.Version, releasecontract.Candidate, EmbeddedMigrationDigest()); err != nil {
		return fmt.Errorf("seal private fresh initialization receipt: %w", err)
	}
	return nil
}

// FreshInitializationMatches is read-only and never substitutes for the
// predecessor-bound disposable completion. It binds the private receipt to
// the exact binary and the ACTUAL migration ledger and schema postcondition.
func FreshInitializationMatches(ctx context.Context, conn interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, database, version, candidate string) (bool, error) {
	var exists bool
	if err := conn.QueryRow(ctx, `SELECT to_regclass('mycfc_fresh_init.receipt') IS NOT NULL`).Scan(&exists); err != nil || !exists {
		return false, err
	}
	// The migration login owns the application schema and can create a matching
	// ledger and a lookalike receipt. Only the bootstrap superuser can have
	// created the private receipt; never trust its contents alone.
	var protected bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS (
  SELECT 1 FROM pg_class c
  JOIN pg_namespace n ON n.oid=c.relnamespace
  JOIN pg_roles owner ON owner.oid=c.relowner AND owner.oid=n.nspowner
  WHERE n.nspname='mycfc_fresh_init' AND c.relname='receipt'
    AND c.relkind='r' AND owner.rolsuper
)`).Scan(&protected); err != nil {
		return false, err
	}
	if !protected {
		return false, errors.New("fresh initialization receipt is not bootstrap-superuser-owned")
	}
	var receipt bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM mycfc_fresh_init.receipt WHERE database_name=$1 AND version=$2 AND candidate=$3 AND schema_digest=$4)`, database, version, candidate, EmbeddedMigrationDigest()).Scan(&receipt); err != nil {
		return false, err
	}
	if !receipt {
		return false, errors.New("fresh initialization receipt identity mismatch")
	}
	var ledger string
	if err := conn.QueryRow(ctx, `SELECT string_agg(version,E'\n' ORDER BY version) FROM mycfc_meta.schema_migrations`).Scan(&ledger); err != nil {
		return false, err
	}
	actual := sha256.Sum256([]byte(ledger))
	if hex.EncodeToString(actual[:]) != EmbeddedMigrationDigest() {
		return false, errors.New("fresh initialization ledger mismatch")
	}
	if err := VerifyDatedParticipationContract(ctx, conn); err != nil {
		return false, errors.New("fresh initialization schema mismatch")
	}
	return true, nil
}
