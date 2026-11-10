package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// FreshInitializationInspector starts a stable inspection transaction on an
// independently authenticated migration connection. It must not be a bootstrap
// connection with a changed current role.
type FreshInitializationInspector interface {
	Begin(context.Context) (pgx.Tx, error)
}

var errFreshInspection = errors.New("fresh initialization ledger inspection rejected")

const freshLedgerShapeSQL = `SELECT c.oid::pg_catalog.text
 FROM pg_catalog.pg_class c
 JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
 JOIN pg_catalog.pg_roles owner ON owner.oid=c.relowner AND owner.oid=n.nspowner
 WHERE n.nspname='mycfc_meta' AND c.relname='schema_migrations'
 AND c.relkind='r' AND c.relpersistence='p' AND NOT c.relrowsecurity AND NOT c.relforcerowsecurity
 AND owner.rolname=$1 AND NOT owner.rolsuper
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_inherits i WHERE i.inhrelid=c.oid OR i.inhparent=c.oid)
 AND (SELECT count(*) FROM pg_catalog.pg_attribute a WHERE a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped)=2
 AND EXISTS(SELECT 1 FROM pg_catalog.pg_attribute a WHERE a.attrelid=c.oid AND a.attname='version'
   AND a.atttypid='pg_catalog.text'::pg_catalog.regtype AND a.attnotnull AND a.attnum>0
   AND NOT a.attisdropped AND a.attndims=0 AND a.attgenerated='' AND a.attidentity='')
 AND EXISTS(SELECT 1 FROM pg_catalog.pg_attribute a WHERE a.attrelid=c.oid AND a.attname='applied_at'
   AND a.atttypid='pg_catalog.timestamptz'::pg_catalog.regtype AND a.attnotnull AND a.attnum>0
   AND NOT a.attisdropped AND a.attndims=0 AND a.attgenerated='' AND a.attidentity='')
 AND EXISTS(SELECT 1 FROM pg_catalog.pg_constraint k JOIN pg_catalog.pg_attribute a
   ON a.attrelid=c.oid AND a.attname='version'
   JOIN pg_catalog.pg_index i ON i.indexrelid=k.conindid
   WHERE k.conrelid=c.oid AND k.contype='p' AND k.conkey=ARRAY[a.attnum]
   AND k.convalidated AND i.indisvalid AND i.indisready AND i.indpred IS NULL AND i.indexprs IS NULL)
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_rewrite r WHERE r.ev_class=c.oid)`

func inspectFreshInitializationLedger(ctx context.Context, receipt interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, inspector FreshInitializationInspector, expectedRole, database string) (pgx.Tx, string, error) {
	if inspector == nil {
		return nil, "", errFreshInspection
	}
	tx, err := inspector.Begin(ctx)
	if err != nil {
		return nil, "", errFreshInspection
	}
	success := false
	defer func() {
		if !success {
			_ = tx.Rollback(ctx)
		}
	}()
	// No untrusted relation/expression has been evaluated before this boundary.
	if _, err = tx.Exec(ctx, `SET TRANSACTION READ ONLY`); err != nil {
		return nil, "", errFreshInspection
	}
	if _, err = tx.Exec(ctx, `SET LOCAL search_path=pg_catalog`); err != nil {
		return nil, "", errFreshInspection
	}
	if _, err = tx.Exec(ctx, `SET LOCAL lock_timeout='5s'`); err != nil {
		return nil, "", errFreshInspection
	}
	if _, err = tx.Exec(ctx, `SET LOCAL statement_timeout='10s'`); err != nil {
		return nil, "", errFreshInspection
	}
	var authenticatedRole, databaseOID string
	var backendStarted time.Time
	var backendPID int32
	var allowed bool
	err = tx.QueryRow(ctx, `SELECT session_user::text, d.oid::text, pg_backend_pid(), activity.backend_start,
 session_user=current_user AND NOT r.rolsuper AND NOT r.rolcreaterole AND NOT r.rolreplication AND NOT r.rolbypassrls
	 AND NOT EXISTS(SELECT 1 FROM pg_roles privileged WHERE
	   (privileged.rolsuper OR privileged.rolcreaterole OR privileged.rolreplication OR privileged.rolbypassrls
	    OR privileged.rolname IN ('pg_execute_server_program','pg_read_server_files','pg_write_server_files'))
	   AND pg_has_role(session_user,privileged.oid,'MEMBER'))
 FROM pg_database d JOIN pg_roles r ON r.rolname=session_user
 JOIN pg_stat_activity activity ON activity.pid=pg_backend_pid()
 WHERE d.datname=current_database() AND d.datname=$1`, database).Scan(&authenticatedRole, &databaseOID, &backendPID, &backendStarted, &allowed)
	if err != nil || !allowed || expectedRole != "" && expectedRole != authenticatedRole {
		return nil, "", errFreshInspection
	}
	// Confirm the inspector's actual backend belongs to this database/server,
	// rather than trusting a second URL's database name or a SET ROLE identity.
	var sameBackend bool
	err = receipt.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_stat_activity a
 JOIN pg_catalog.pg_database d ON d.oid OPERATOR(pg_catalog.=) a.datid
	 WHERE a.pid OPERATOR(pg_catalog.=) $1 AND a.backend_start OPERATOR(pg_catalog.=) $2
 AND a.usename OPERATOR(pg_catalog.=) $3 AND d.oid::pg_catalog.text OPERATOR(pg_catalog.=) $4
 AND d.datname OPERATOR(pg_catalog.=) $5 AND d.datname OPERATOR(pg_catalog.=) pg_catalog.current_database())`, backendPID, backendStarted, authenticatedRole, databaseOID, database).Scan(&sameBackend)
	if err != nil || !sameBackend {
		return nil, "", errFreshInspection
	}
	var before, locked string
	if err = tx.QueryRow(ctx, freshLedgerShapeSQL, authenticatedRole).Scan(&before); err != nil {
		return nil, "", errFreshInspection
	}
	if _, err = tx.Exec(ctx, `LOCK TABLE ONLY mycfc_meta.schema_migrations IN ACCESS SHARE MODE`); err != nil {
		return nil, "", errFreshInspection
	}
	if err = tx.QueryRow(ctx, freshLedgerShapeSQL, authenticatedRole).Scan(&locked); err != nil || before != locked {
		return nil, "", errFreshInspection
	}
	// Read raw built-in text only. No SQL aggregation, custom ordering/operator,
	// user-defined casts, or migration-owned validation functions are invoked.
	rows, err := tx.Query(ctx, `SELECT version FROM ONLY mycfc_meta.schema_migrations LIMIT 10001`)
	if err != nil {
		return nil, "", errFreshInspection
	}
	var versions []string
	for rows.Next() {
		var version string
		if err = rows.Scan(&version); err != nil || len(version) > 256 || len(versions) >= 10000 {
			rows.Close()
			return nil, "", errFreshInspection
		}
		versions = append(versions, version)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, "", errFreshInspection
	}
	sort.Strings(versions)
	digest := sha256.Sum256([]byte(strings.Join(versions, "\n")))
	success = true
	return tx, hex.EncodeToString(digest[:]), nil
}
