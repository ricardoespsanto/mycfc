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
	"github.com/jackc/pgx/v5/pgconn"
)

//go:embed schema.sql
var baselineSchema string

//go:embed migrations/*.sql
var migrationFiles embed.FS

var postgresIdentifier = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,62}$`)

const (
	baselineVersion         = "reset-baseline-v1"
	baselineIncludesThrough = "202609100011_privacy_completion_control"
	privacyRetentionRole    = "mycfc_privacy_retention"
)

type RoleCredentials struct {
	AppUsername             string
	AppPassword             string
	MigrationUsername       string
	MigrationPassword       string
	PrivacyExecutorUsername string
	PrivacyExecutorPassword string
}

type namedStatement struct{ name, sql string }

type bootstrapConnection interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

type baselineConnection interface {
	Begin(context.Context) (pgx.Tx, error)
}

func BootstrapRoles(ctx context.Context, conn bootstrapConnection, databaseName string, credentials RoleCredentials) error {
	if err := validateBootstrapInput(databaseName, credentials); err != nil {
		return err
	}
	app := quoteIdentifier(credentials.AppUsername)
	migration := quoteIdentifier(credentials.MigrationUsername)
	database := quoteIdentifier(databaseName)

	statements := []namedStatement{
		{"enable citext", "CREATE EXTENSION IF NOT EXISTS citext"},
		{"enable pgcrypto", "CREATE EXTENSION IF NOT EXISTS pgcrypto"},
		{"configure app role", roleStatement(credentials.AppUsername, credentials.AppPassword)},
		{"configure migration role", roleStatement(credentials.MigrationUsername, credentials.MigrationPassword)},
		{"configure privacy retention capability role", noLoginRoleStatement(privacyRetentionRole)},
		{"grant migration membership", "GRANT " + migration + " TO CURRENT_USER"},
		{"revoke public database access", "REVOKE ALL ON DATABASE " + database + " FROM PUBLIC"},
		{"grant database access", "GRANT CONNECT, CREATE ON DATABASE " + database + " TO " + migration},
		{"grant app database access", "GRANT CONNECT ON DATABASE " + database + " TO " + app},
		{"grant privacy retention database access", "GRANT CONNECT ON DATABASE " + database + " TO " + quoteIdentifier(privacyRetentionRole)},
		{"revoke public schema access", "REVOKE ALL ON SCHEMA public FROM PUBLIC"},
		{"set public schema owner", "ALTER SCHEMA public OWNER TO " + migration},
		{"grant app schema usage", "GRANT USAGE ON SCHEMA public TO " + app},
		{"grant privacy retention schema usage", "GRANT USAGE ON SCHEMA public TO " + quoteIdentifier(privacyRetentionRole)},
		{"create metadata schema", "CREATE SCHEMA IF NOT EXISTS mycfc_meta AUTHORIZATION " + migration},
		{"set metadata schema owner", "ALTER SCHEMA mycfc_meta OWNER TO " + migration},
		{"revoke public metadata access", "REVOKE ALL ON SCHEMA mycfc_meta FROM PUBLIC"},
		{"transfer existing schema objects", transferOwnershipStatement(credentials.MigrationUsername)},
		{"grant existing tables", "GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO " + app},
		{"grant existing sequences", "GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO " + app},
		{"set table defaults", "ALTER DEFAULT PRIVILEGES FOR ROLE " + migration + " IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO " + app},
		{"set sequence defaults", "ALTER DEFAULT PRIVILEGES FOR ROLE " + migration + " IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO " + app},
	}
	if privacyExecutorConfigured(credentials) {
		executor := quoteIdentifier(credentials.PrivacyExecutorUsername)
		statements = append(statements,
			namedStatement{"configure privacy executor role", roleStatement(credentials.PrivacyExecutorUsername, credentials.PrivacyExecutorPassword)},
			namedStatement{"grant privacy executor database access", "GRANT CONNECT ON DATABASE " + database + " TO " + executor},
			namedStatement{"grant privacy executor schema usage", "GRANT USAGE ON SCHEMA public TO " + executor},
		)
	}
	for _, statement := range statements {
		if _, err := conn.Exec(ctx, statement.sql); err != nil {
			return fmt.Errorf("bootstrap database roles (%s): %w", statement.name, err)
		}
	}
	return nil
}

// HardenPrivacyExecutionRoles reapplies the #244 table boundary after migrations
// have created the execution tables. The web role may materialize the immutable
// handoff graph, but only the optional executor role may mutate worker state.
// Keeping this separate from BootstrapRoles makes the required post-migration
// ordering explicit and allows a disabled rollout with no executor credential.
func HardenPrivacyExecutionRoles(ctx context.Context, conn bootstrapConnection, databaseName string, credentials RoleCredentials) error {
	if err := validateDatabaseRoleIdentifiers(databaseName, credentials); err != nil {
		return err
	}
	app := quoteIdentifier(credentials.AppUsername)
	retention := quoteIdentifier(privacyRetentionRole)
	executionTables := strings.Join([]string{
		"privacy_pseudonymous_principals",
		"privacy_erasure_executions",
		"privacy_erasure_access_revocations",
		"privacy_erasure_category_jobs",
		"privacy_erasure_job_leases",
		"privacy_erasure_job_attempts",
		"privacy_erasure_job_checkpoints",
		"privacy_erasure_failures",
		"privacy_erasure_retention_anchors",
		"privacy_erasure_restricted_records",
		"privacy_erasure_completion_manifests",
	}, ", ")
	retentionTables := "privacy_retention_runs, privacy_outbox_delivery_evidence"
	webHandoffTables := strings.Join([]string{
		"privacy_erasure_executions",
		"privacy_erasure_access_revocations",
		"privacy_erasure_category_jobs",
		"privacy_erasure_job_checkpoints",
	}, ", ")
	completionControlTables := strings.Join([]string{
		"privacy_completion_access_links",
		"privacy_terminal_requeue_proposals",
		"privacy_terminal_requeue_approvals",
		"privacy_activation_evidence",
		"privacy_activation_proposals",
		"privacy_activation_approvals",
	}, ", ")
	statements := []namedStatement{
		{"revoke public execution table access", "REVOKE ALL PRIVILEGES ON TABLE " + executionTables + " FROM PUBLIC"},
		{"revoke public retention table access", "REVOKE ALL PRIVILEGES ON TABLE " + retentionTables + " FROM PUBLIC"},
		{"revoke public completion control table access", "REVOKE ALL PRIVILEGES ON TABLE " + completionControlTables + " FROM PUBLIC"},
		{"revoke public protected schema access", "REVOKE ALL ON SCHEMA privacy_protected FROM PUBLIC"},
		{"revoke public protected table access", "REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA privacy_protected FROM PUBLIC"},
		{"revoke public protected sequence access", "REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA privacy_protected FROM PUBLIC"},
		{"revoke web execution table access", "REVOKE ALL PRIVILEGES ON TABLE " + executionTables + " FROM " + app},
		{"revoke web retention table access", "REVOKE ALL PRIVILEGES ON TABLE " + retentionTables + " FROM " + app},
		{"revoke web completion control table access", "REVOKE ALL PRIVILEGES ON TABLE " + completionControlTables + " FROM " + app},
		{"restrict web consent evidence writes", "REVOKE UPDATE, DELETE ON TABLE consent_forms FROM " + app},
		{"revoke web protected schema access", "REVOKE ALL ON SCHEMA privacy_protected FROM " + app},
		{"revoke web protected table access", "REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA privacy_protected FROM " + app},
		{"revoke web protected sequence access", "REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA privacy_protected FROM " + app},
		{"grant web execution reads", "GRANT SELECT ON TABLE " + webHandoffTables + " TO " + app},
		{"grant web execution insert", "GRANT INSERT (request_id, plan_sha256, executor_version, schema_version, request_version_at_start, started_by_ref, accepted_at, updated_at) ON TABLE privacy_erasure_executions TO " + app},
		{"grant web access revocation insert", "GRANT INSERT (execution_id, grant_kind, capability_code, revoked_count, actor_ref, occurred_at) ON TABLE privacy_erasure_access_revocations TO " + app},
		{"grant web category job insert", "GRANT INSERT (execution_id, plan_entry_position, entry_sha256, category_key, purpose_code, next_attempt_at, created_at, updated_at) ON TABLE privacy_erasure_category_jobs TO " + app},
		{"grant web checkpoint insert", "GRANT INSERT (job_id, operation_position, operation_code, action_version, created_at) ON TABLE privacy_erasure_job_checkpoints TO " + app},
		{"revoke web upload capabilities", "REVOKE EXECUTE ON FUNCTION privacy_upload_source_lock(text,uuid), privacy_upload_pointer_references(uuid,text,uuid), privacy_upload_pointer_matches(uuid,text,uuid,bytea,text,bigint), privacy_upload_enforce_pointer_state(), privacy_upload_begin(uuid,uuid,uuid,text,uuid,text,text,bigint,bytea), privacy_upload_finalize(uuid,bytea,text,text,text,bytea,bytea,bytea,text,bytea,bytea), privacy_upload_confirm_put(uuid,bytea), privacy_upload_mark_cleanup(uuid,bytea,text), privacy_upload_attach(uuid,bytea,uuid,text,uuid,text,text,bigint), privacy_upload_remove(uuid,uuid,text,uuid), privacy_upload_cleanup_claim(bigint,uuid), privacy_upload_cleanup_complete(uuid,uuid,bigint,uuid,integer,integer,integer,integer,text,bytea), privacy_upload_cleanup_fail(uuid,uuid,bigint,uuid,boolean,bigint) FROM " + app},
		{"grant web upload lifecycle", "GRANT EXECUTE ON FUNCTION privacy_upload_begin(uuid,uuid,uuid,text,uuid,text,text,bigint,bytea), privacy_upload_finalize(uuid,bytea,text,text,text,bytea,bytea,bytea,text,bytea,bytea), privacy_upload_confirm_put(uuid,bytea), privacy_upload_mark_cleanup(uuid,bytea,text), privacy_upload_attach(uuid,bytea,uuid,text,uuid,text,text,bigint), privacy_upload_remove(uuid,uuid,text,uuid) TO " + app},
		{"revoke web object execution internals", "REVOKE EXECUTE ON FUNCTION privacy_media_subject_lock(uuid), privacy_execution_capture_media_sources(uuid,uuid,text), privacy_execution_materialize_object_target(uuid,uuid,uuid,uuid,bytea,text,text,uuid,uuid,text,text,text,text,bytea,bytea,bytea,text,bytea), privacy_execution_complete_object_capture(uuid,text), privacy_worker_list_object_targets(uuid,uuid,uuid,bigint,uuid), privacy_worker_record_object_evidence(uuid,uuid,uuid,uuid,bigint,uuid,integer,integer,integer,integer,text,bytea), privacy_worker_complete_object_checkpoint(uuid,uuid,uuid,bigint,uuid) FROM " + app},
		{"grant web object capture routines", "GRANT EXECUTE ON FUNCTION privacy_execution_capture_media_sources(uuid,uuid,text), privacy_execution_materialize_object_target(uuid,uuid,uuid,uuid,bytea,text,text,uuid,uuid,text,text,text,text,bytea,bytea,bytea,text,bytea), privacy_execution_complete_object_capture(uuid,text) TO " + app},
		{"revoke web provider worker routines", "REVOKE EXECUTE ON FUNCTION privacy_worker_list_provider_targets(uuid,uuid,uuid,bigint,uuid), privacy_worker_record_provider_evidence(uuid,uuid,uuid,uuid,bigint,uuid,text,integer,text,text,text,text,text,text,text,bytea), privacy_worker_complete_provider_checkpoint(uuid,uuid,uuid,bigint,uuid) FROM " + app},
		{"grant web provider capture routines", "GRANT EXECUTE ON FUNCTION privacy_execution_capture_provider_connections(uuid,uuid,text), privacy_execution_materialize_provider_target(uuid,uuid,uuid,uuid,uuid,bytea,text,text,text,text,bigint,text,text,bytea,text,text,text,bytea,bytea,bytea,text,text,text,bytea,bytea,bytea,text,bytea,text,bytea), privacy_execution_complete_provider_capture(uuid,text) TO " + app},
		{"revoke web completion worker routines", "REVOKE EXECUTE ON FUNCTION privacy_completion_prepare(uuid,uuid), privacy_completion_list_pending(uuid,integer), privacy_completion_finalize(uuid,uuid,bytea,bytea), privacy_worker_activation_ready(), privacy_worker_status() FROM " + app},
		{"grant web completion control routines", "GRANT EXECUTE ON FUNCTION privacy_execution_capture_completion_notice(uuid,uuid), privacy_completion_consume(bytea), privacy_completion_validate(bytea), privacy_completion_notice_deliverable(uuid,timestamptz), privacy_terminal_requeue_propose(uuid,uuid), privacy_terminal_requeue_approve(uuid,bytea,uuid), privacy_completion_control_snapshot(uuid,uuid), privacy_activation_ready(text), privacy_activation_record_evidence(uuid,text,bytea,text,timestamptz), privacy_activation_propose(uuid,text,uuid[]), privacy_activation_approve(uuid,uuid,bytea), privacy_activation_control_snapshot(uuid) TO " + app},
		{"revoke web restore and retention routines", "REVOKE EXECUTE ON FUNCTION privacy_tombstone_prepare(uuid,uuid,uuid,bigint,uuid), privacy_tombstone_confirm(uuid,uuid,uuid,bigint,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz), privacy_tombstone_prepare_closure(uuid,uuid), privacy_tombstone_confirm_closure(uuid,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz), privacy_tombstone_prepare_v2(uuid,uuid,uuid,bigint,uuid), privacy_tombstone_confirm_v2(uuid,uuid,uuid,bigint,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz), privacy_tombstone_prepare_closure_v2(uuid,uuid), privacy_tombstone_confirm_closure_v2(uuid,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz), privacy_restore_import_authenticated_v2(uuid,text,text,text,text,text,bytea,bytea,text,timestamptz,timestamptz,timestamptz,uuid,uuid,uuid,uuid,bytea,bytea,timestamptz,text,text,text[],bytea,bytea), privacy_restore_begin_replay(uuid,uuid), privacy_restore_apply_relational_operation(uuid,uuid,timestamptz,text), privacy_restore_execute_checkpoint(uuid,uuid,smallint,text,text,bytea), privacy_retention_run(uuid,integer) FROM " + app},
		{"revoke web retention internals", "REVOKE EXECUTE ON FUNCTION privacy_retention_queue_repair_attachments(integer), privacy_retention_pseudonymize_audit(integer), privacy_retention_status() FROM " + app},
		{"grant web consent cessation", "GRANT EXECUTE ON FUNCTION privacy_consent_cease(uuid,text,uuid,text,timestamptz) TO " + app},
		{"revoke privacy retention table access", "REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM " + retention},
		{"revoke privacy retention protected table access", "REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA privacy_protected FROM " + retention},
		{"revoke privacy retention sequence access", "REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public FROM " + retention},
		{"revoke privacy retention protected sequence access", "REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA privacy_protected FROM " + retention},
		{"revoke privacy retention internals", "REVOKE EXECUTE ON FUNCTION privacy_consent_cease(uuid,text,uuid,text,timestamptz), privacy_retention_queue_repair_attachments(integer), privacy_retention_pseudonymize_audit(integer) FROM " + retention},
		{"grant privacy retention fixed API", "GRANT EXECUTE ON FUNCTION privacy_retention_run(uuid,integer), privacy_retention_status() TO " + retention},
	}
	if privacyExecutorConfigured(credentials) {
		executor := quoteIdentifier(credentials.PrivacyExecutorUsername)
		statements = append(statements,
			namedStatement{"revoke privacy executor upload lifecycle", "REVOKE EXECUTE ON FUNCTION privacy_upload_source_lock(text,uuid), privacy_upload_pointer_references(uuid,text,uuid), privacy_upload_pointer_matches(uuid,text,uuid,bytea,text,bigint), privacy_upload_enforce_pointer_state(), privacy_upload_begin(uuid,uuid,uuid,text,uuid,text,text,bigint,bytea), privacy_upload_finalize(uuid,bytea,text,text,text,bytea,bytea,bytea,text,bytea,bytea), privacy_upload_confirm_put(uuid,bytea), privacy_upload_mark_cleanup(uuid,bytea,text), privacy_upload_attach(uuid,bytea,uuid,text,uuid,text,text,bigint), privacy_upload_remove(uuid,uuid,text,uuid), privacy_upload_cleanup_claim(bigint,uuid), privacy_upload_cleanup_complete(uuid,uuid,bigint,uuid,integer,integer,integer,integer,text,bytea), privacy_upload_cleanup_fail(uuid,uuid,bigint,uuid,boolean,bigint) FROM " + executor},
			namedStatement{"revoke privacy executor schema creation", "REVOKE CREATE ON SCHEMA public FROM " + executor},
			namedStatement{"grant privacy executor schema usage", "GRANT USAGE ON SCHEMA public TO " + executor},
			namedStatement{"revoke privacy executor protected schema access", "REVOKE ALL ON SCHEMA privacy_protected FROM " + executor},
			namedStatement{"revoke privacy executor protected table access", "REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA privacy_protected FROM " + executor},
			namedStatement{"revoke privacy executor protected sequence access", "REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA privacy_protected FROM " + executor},
			namedStatement{"revoke privacy executor table access", "REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM " + executor},
			namedStatement{"revoke privacy executor completion control table access", "REVOKE ALL PRIVILEGES ON TABLE " + completionControlTables + " FROM " + executor},
			namedStatement{"revoke privacy executor sequence access", "REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public FROM " + executor},
			namedStatement{"revoke legacy checkpoint bypass", "REVOKE EXECUTE ON FUNCTION privacy_worker_complete_checkpoint(uuid,uuid,uuid,bigint,uuid,text,text) FROM " + executor},
			namedStatement{"grant privacy executor execution reads", "GRANT SELECT ON TABLE " + executionTables + ", privacy_request_execution_plans TO " + executor},
			namedStatement{"grant privacy executor request lifecycle reads", "GRANT SELECT (id, status, version, updated_at) ON TABLE data_erasure_requests TO " + executor},
			namedStatement{"grant privacy executor fenced routines", "GRANT EXECUTE ON FUNCTION privacy_worker_claim(bigint,uuid), privacy_worker_heartbeat(uuid,uuid,uuid,bigint,uuid,bigint), privacy_worker_execute_checkpoint(uuid,uuid,uuid,bigint,uuid,text,text), privacy_worker_complete_job(uuid,uuid,uuid,bigint,uuid), privacy_worker_fail_job(uuid,uuid,uuid,bigint,uuid,text,bigint,text,text,bytea), privacy_worker_sync(uuid,uuid,uuid,bigint,uuid) TO " + executor},
			namedStatement{"grant privacy executor upload cleanup routines", "GRANT EXECUTE ON FUNCTION privacy_upload_cleanup_claim(bigint,uuid), privacy_upload_cleanup_complete(uuid,uuid,bigint,uuid,integer,integer,integer,integer,text,bytea), privacy_upload_cleanup_fail(uuid,uuid,bigint,uuid,boolean,bigint) TO " + executor},
			namedStatement{"grant privacy executor object routines", "GRANT EXECUTE ON FUNCTION privacy_worker_list_object_targets(uuid,uuid,uuid,bigint,uuid), privacy_worker_record_object_evidence(uuid,uuid,uuid,uuid,bigint,uuid,integer,integer,integer,integer,text,bytea), privacy_worker_complete_object_checkpoint(uuid,uuid,uuid,bigint,uuid) TO " + executor},
			namedStatement{"revoke privacy executor provider capture routines", "REVOKE EXECUTE ON FUNCTION privacy_execution_capture_provider_connections(uuid,uuid,text), privacy_execution_materialize_provider_target(uuid,uuid,uuid,uuid,uuid,bytea,text,text,text,text,bigint,text,text,bytea,text,text,text,bytea,bytea,bytea,text,text,text,bytea,bytea,bytea,text,bytea,text,bytea), privacy_execution_complete_provider_capture(uuid,text) FROM " + executor},
			namedStatement{"grant privacy executor provider routines", "GRANT EXECUTE ON FUNCTION privacy_worker_list_provider_targets(uuid,uuid,uuid,bigint,uuid), privacy_worker_record_provider_evidence(uuid,uuid,uuid,uuid,bigint,uuid,text,integer,text,text,text,text,text,text,text,bytea), privacy_worker_complete_provider_checkpoint(uuid,uuid,uuid,bigint,uuid) TO " + executor},
			namedStatement{"revoke privacy executor legacy and offline restore routines", "REVOKE EXECUTE ON FUNCTION privacy_tombstone_prepare(uuid,uuid,uuid,bigint,uuid), privacy_tombstone_confirm(uuid,uuid,uuid,bigint,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz), privacy_tombstone_prepare_closure(uuid,uuid), privacy_tombstone_confirm_closure(uuid,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz), privacy_restore_import_authenticated_v2(uuid,text,text,text,text,text,bytea,bytea,text,timestamptz,timestamptz,timestamptz,uuid,uuid,uuid,uuid,bytea,bytea,timestamptz,text,text,text[],bytea,bytea), privacy_restore_begin_replay(uuid,uuid), privacy_restore_apply_relational_operation(uuid,uuid,timestamptz,text), privacy_restore_execute_checkpoint(uuid,uuid,smallint,text,text,bytea) FROM " + executor},
			namedStatement{"grant privacy executor tombstone routines", "GRANT EXECUTE ON FUNCTION privacy_tombstone_prepare_v2(uuid,uuid,uuid,bigint,uuid), privacy_tombstone_confirm_v2(uuid,uuid,uuid,bigint,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz), privacy_tombstone_prepare_closure_v2(uuid,uuid), privacy_tombstone_confirm_closure_v2(uuid,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz) TO " + executor},
			namedStatement{"revoke privacy executor operator completion routines", "REVOKE EXECUTE ON FUNCTION privacy_execution_capture_completion_notice(uuid,uuid), privacy_completion_consume(bytea), privacy_completion_validate(bytea), privacy_completion_notice_deliverable(uuid,timestamptz), privacy_terminal_requeue_propose(uuid,uuid), privacy_terminal_requeue_approve(uuid,bytea,uuid), privacy_completion_control_snapshot(uuid,uuid), privacy_activation_ready(text), privacy_activation_record_evidence(uuid,text,bytea,text,timestamptz), privacy_activation_propose(uuid,text,uuid[]), privacy_activation_approve(uuid,uuid,bytea), privacy_activation_control_snapshot(uuid) FROM " + executor},
			namedStatement{"grant privacy executor completion routines", "GRANT EXECUTE ON FUNCTION privacy_completion_prepare(uuid,uuid), privacy_completion_list_pending(uuid,integer), privacy_completion_finalize(uuid,uuid,bytea,bytea), privacy_worker_activation_ready(), privacy_worker_status() TO " + executor},
			namedStatement{"revoke privacy executor retention routine", "REVOKE EXECUTE ON FUNCTION privacy_consent_cease(uuid,text,uuid,text,timestamptz), privacy_retention_queue_repair_attachments(integer), privacy_retention_pseudonymize_audit(integer), privacy_retention_run(uuid,integer), privacy_retention_status() FROM " + executor},
		)
	}
	for _, statement := range statements {
		if _, err := conn.Exec(ctx, statement.sql); err != nil {
			return fmt.Errorf("harden privacy execution roles (%s): %w", statement.name, err)
		}
	}
	return nil
}

func ApplyBaseline(ctx context.Context, conn baselineConnection) error {
	return applyBaseline(ctx, conn, nil)
}

// ApplyBaselineAndHarden applies every pending schema migration and the
// execution-role boundary in the same transaction. This prevents a newly
// created execution table from inheriting the web role's broad default DML
// privileges for even a brief post-migration window.
func ApplyBaselineAndHarden(ctx context.Context, conn baselineConnection, databaseName string, credentials RoleCredentials) error {
	if err := validateDatabaseRoleIdentifiers(databaseName, credentials); err != nil {
		return err
	}
	return applyBaseline(ctx, conn, func(ctx context.Context, tx pgx.Tx) error {
		if err := HardenPrivacyExecutionRoles(ctx, tx, databaseName, credentials); err != nil {
			return fmt.Errorf("harden roles before migration commit: %w", err)
		}
		return nil
	})
}

func applyBaseline(ctx context.Context, conn baselineConnection, beforeCommit func(context.Context, pgx.Tx) error) error {
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
		return commitBaseline(ctx, tx, beforeCommit)
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
		return commitBaseline(ctx, tx, beforeCommit)
	}
	if _, err := tx.Conn().PgConn().Exec(ctx, baselineSchema).ReadAll(); err != nil {
		return fmt.Errorf("apply reset baseline: %w", err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO mycfc_meta.schema_migrations (version) VALUES ($1)", baselineVersion); err != nil {
		return fmt.Errorf("record baseline migration: %w", err)
	}
	if err := recordBaselineMigrations(ctx, tx); err != nil {
		return err
	}
	if err := applyIncrementalMigrations(ctx, tx); err != nil {
		return err
	}
	return commitBaseline(ctx, tx, beforeCommit)
}

func commitBaseline(ctx context.Context, tx pgx.Tx, beforeCommit func(context.Context, pgx.Tx) error) error {
	if beforeCommit != nil {
		if err := beforeCommit(ctx, tx); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit baseline migration: %w", err)
	}
	return nil
}

func recordBaselineMigrations(ctx context.Context, tx pgx.Tx) error {
	entries, err := fs.Glob(migrationFiles, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("list baseline migrations: %w", err)
	}
	foundCutoff := false
	for _, name := range entries {
		version := migrationVersion(name)
		if version > baselineIncludesThrough {
			break
		}
		if _, err := tx.Exec(ctx, "INSERT INTO mycfc_meta.schema_migrations (version) VALUES ($1) ON CONFLICT DO NOTHING", version); err != nil {
			return fmt.Errorf("record baseline migration %s: %w", version, err)
		}
		foundCutoff = foundCutoff || version == baselineIncludesThrough
	}
	if !foundCutoff {
		return fmt.Errorf("baseline migration cutoff %s is not embedded", baselineIncludesThrough)
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
	if err := validateDatabaseRoleIdentifiers(databaseName, credentials); err != nil {
		return err
	}
	if strings.TrimSpace(credentials.AppPassword) == "" || strings.TrimSpace(credentials.MigrationPassword) == "" {
		return errors.New("database role passwords must not be empty")
	}
	executorUser := strings.TrimSpace(credentials.PrivacyExecutorUsername)
	executorPassword := strings.TrimSpace(credentials.PrivacyExecutorPassword)
	if (executorUser == "") != (executorPassword == "") {
		return errors.New("privacy executor database user and password must either both be set or both be empty")
	}
	return nil
}

func validateDatabaseRoleIdentifiers(databaseName string, credentials RoleCredentials) error {
	for name, value := range map[string]string{
		"database name":           databaseName,
		"app database user":       credentials.AppUsername,
		"migration database user": credentials.MigrationUsername,
	} {
		if !postgresIdentifier.MatchString(value) {
			return fmt.Errorf("%s %q must be a PostgreSQL identifier", name, value)
		}
	}
	if credentials.AppUsername == credentials.MigrationUsername {
		return errors.New("app and migration database users must differ")
	}
	executorUser := strings.TrimSpace(credentials.PrivacyExecutorUsername)
	if executorUser != "" {
		if !postgresIdentifier.MatchString(credentials.PrivacyExecutorUsername) {
			return fmt.Errorf("privacy executor database user %q must be a PostgreSQL identifier", credentials.PrivacyExecutorUsername)
		}
		if credentials.PrivacyExecutorUsername == credentials.AppUsername || credentials.PrivacyExecutorUsername == credentials.MigrationUsername {
			return errors.New("privacy executor, app, and migration database users must differ")
		}
	}
	return nil
}

func privacyExecutorConfigured(credentials RoleCredentials) bool {
	return strings.TrimSpace(credentials.PrivacyExecutorUsername) != "" && strings.TrimSpace(credentials.PrivacyExecutorPassword) != ""
}

func roleStatement(username, password string) string {
	return "DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = " + quoteLiteral(username) + ") THEN " +
		"CREATE ROLE " + quoteIdentifier(username) + " LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS; END IF; " +
		"ALTER ROLE " + quoteIdentifier(username) + " WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS PASSWORD " + quoteLiteral(password) + "; END $$"
}

func noLoginRoleStatement(username string) string {
	return "DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = " + quoteLiteral(username) + ") THEN " +
		"CREATE ROLE " + quoteIdentifier(username) + " NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS; END IF; " +
		"ALTER ROLE " + quoteIdentifier(username) + " WITH NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS; END $$"
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
