//go:build integration

package db

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestGuardianReleaseBindLoginHasOnlyDestructiveReleaseCutoff(t *testing.T) {
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	setupTx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	installGuardianActivationInventory(t, ctx, setupTx)
	if err = setupTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	password := "integration-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err = ProvisionGuardianReleaseBindRole(ctx, admin, admin.Config().Database, guardianReleaseBindRole, password); err != nil {
		t.Fatal(err)
	}

	var connect, temp, opsUsage, opsCreate, publicUsage, metadataUsage, approvalRead, runtimeRead bool
	var releaseBind, status, preflight, enable, disable, helper, privacy bool
	if err = admin.QueryRow(ctx, `SELECT
		has_database_privilege($1,current_database(),'CONNECT'),
		has_database_privilege($1,current_database(),'TEMP'),
		has_schema_privilege($1,'guardian_ops','USAGE'),
		has_schema_privilege($1,'guardian_ops','CREATE'),
		has_schema_privilege($1,'public','USAGE'),
		COALESCE(has_schema_privilege($1,to_regnamespace('mycfc_meta'),'USAGE'),false),
		has_table_privilege($1,'guardian_authority_policy_approvals','SELECT'),
		has_table_privilege($1,'guardian_ops.runtime_release_binding','SELECT'),
		has_function_privilege($1,'guardian_ops.release_disable_and_bind(text,text,text)','EXECUTE'),
		has_function_privilege($1,'guardian_ops.status(text,text,text)','EXECUTE'),
		has_function_privilege($1,'guardian_ops.preflight(uuid,text,bytea,bytea,text,text,text)','EXECUTE'),
		has_function_privilege($1,'guardian_ops.enable(uuid,text,bytea,bytea,text,text,text)','EXECUTE'),
		has_function_privilege($1,'guardian_ops.disable(uuid,text)','EXECUTE'),
		has_function_privilege($1,'guardian_ops.schema_ready(text)','EXECUTE'),
		has_function_privilege($1,'privacy_worker_status()','EXECUTE')`, guardianReleaseBindRole).Scan(
		&connect, &temp, &opsUsage, &opsCreate, &publicUsage, &metadataUsage, &approvalRead, &runtimeRead,
		&releaseBind, &status, &preflight, &enable, &disable, &helper, &privacy); err != nil {
		t.Fatal(err)
	}
	if !connect || temp || !opsUsage || opsCreate || publicUsage || metadataUsage || approvalRead || runtimeRead ||
		!releaseBind || status || preflight || enable || disable || helper || privacy {
		t.Fatalf("release role boundary connect=%v temp=%v ops_usage=%v ops_create=%v public=%v metadata=%v approval=%v runtime=%v bind=%v status=%v preflight=%v enable=%v disable=%v helper=%v privacy=%v",
			connect, temp, opsUsage, opsCreate, publicUsage, metadataUsage, approvalRead, runtimeRead,
			releaseBind, status, preflight, enable, disable, helper, privacy)
	}

	var superuser, createDB, createRole, inherit, replication, bypassRLS bool
	var memberships int
	if err = admin.QueryRow(ctx, `SELECT rolsuper,rolcreatedb,rolcreaterole,rolinherit,rolreplication,rolbypassrls,
		(SELECT count(*) FROM pg_auth_members WHERE member=role.oid)
		FROM pg_roles role WHERE rolname=$1`, guardianReleaseBindRole).Scan(
		&superuser, &createDB, &createRole, &inherit, &replication, &bypassRLS, &memberships); err != nil {
		t.Fatal(err)
	}
	if superuser || createDB || createRole || inherit || replication || bypassRLS || memberships != 0 {
		t.Fatalf("release role attributes super=%v createdb=%v createrole=%v inherit=%v replication=%v bypassrls=%v memberships=%d",
			superuser, createDB, createRole, inherit, replication, bypassRLS, memberships)
	}

	var functionOwner, tableOwner, sequenceOwner string
	if err = admin.QueryRow(ctx, `SELECT
		pg_get_userbyid((SELECT proowner FROM pg_proc WHERE oid='guardian_ops.release_disable_and_bind(text,text,text)'::regprocedure)),
		pg_get_userbyid((SELECT relowner FROM pg_class WHERE oid='guardian_ops.runtime_release_binding'::regclass)),
		pg_get_userbyid((SELECT relowner FROM pg_class WHERE oid='guardian_ops.runtime_release_binding_events_id_seq'::regclass))`).Scan(
		&functionOwner, &tableOwner, &sequenceOwner); err != nil {
		t.Fatal(err)
	}
	if functionOwner == guardianReleaseBindRole || tableOwner == guardianReleaseBindRole || sequenceOwner == guardianReleaseBindRole {
		t.Fatalf("release login owns control-plane objects function=%q table=%q sequence=%q", functionOwner, tableOwner, sequenceOwner)
	}

	releaseConfig := admin.Config().Copy()
	releaseConfig.User = guardianReleaseBindRole
	releaseConfig.Password = password
	release, err := pgx.ConnectConfig(ctx, releaseConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = release.Close(context.Background()) })
	var currentUser, sessionUser string
	if err = release.QueryRow(ctx, `SELECT current_user,session_user`).Scan(&currentUser, &sessionUser); err != nil {
		t.Fatal(err)
	}
	if currentUser != guardianReleaseBindRole || sessionUser != guardianReleaseBindRole {
		t.Fatalf("did not exercise real release login current=%q session=%q", currentUser, sessionUser)
	}

	imageDigest := "sha256:" + strings.Repeat("a", 64)
	releaseTx, err := release.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = BindGuardianRuntimeRelease(ctx, releaseTx, admin.Config().Database, imageDigest); err != nil {
		_ = releaseTx.Rollback(ctx)
		t.Fatalf("dedicated release login could not invoke cutoff: %v", err)
	}
	if err = releaseTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	assertDenied := func(name, statement string) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			tx, beginErr := release.Begin(ctx)
			if beginErr != nil {
				t.Fatal(beginErr)
			}
			_, execErr := tx.Exec(ctx, statement)
			_ = tx.Rollback(ctx)
			var databaseErr *pgconn.PgError
			if !errors.As(execErr, &databaseErr) || databaseErr.Code != "42501" {
				t.Fatalf("error=%v, want permission denied", execErr)
			}
		})
	}
	assertDenied("approval table", `SELECT * FROM public.guardian_authority_policy_approvals`)
	assertDenied("runtime table", `SELECT * FROM guardian_ops.runtime_release_binding`)
	assertDenied("status API", `SELECT * FROM guardian_ops.status('sha256:`+strings.Repeat("a", 64)+`','`+EmbeddedMigrationDigest()+`','`+admin.Config().Database+`')`)
	assertDenied("enable API", `SELECT * FROM guardian_ops.enable(NULL,NULL,NULL,NULL,NULL,NULL,NULL)`)
	assertDenied("preflight API", `SELECT * FROM guardian_ops.preflight(NULL,NULL,NULL,NULL,NULL,NULL,NULL)`)
	assertDenied("operator disable API", `SELECT * FROM guardian_ops.disable(NULL,NULL)`)
	assertDenied("generic guardian helper", `SELECT public.guardian_authority_reconcile_cutoffs()`)
	assertDenied("privacy helper", `SELECT * FROM public.privacy_worker_status()`)
	assertDenied("create operator object", `CREATE TABLE guardian_ops.release_bind_forbidden(id integer)`)
	assertDenied("alter release function", `ALTER FUNCTION guardian_ops.release_disable_and_bind(text,text,text) RENAME TO release_bind_forbidden`)
	assertDenied("alter runtime table", `ALTER TABLE guardian_ops.runtime_release_binding ADD COLUMN release_bind_forbidden integer`)

	setRoleTx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer setRoleTx.Rollback(ctx)
	if _, err = setRoleTx.Exec(ctx, `SET LOCAL ROLE `+quoteIdentifier(guardianReleaseBindRole)); err != nil {
		t.Fatal(err)
	}
	if err = setRoleTx.QueryRow(ctx, `SELECT current_user`).Scan(&currentUser); err != nil {
		t.Fatal(err)
	}
	if currentUser != guardianReleaseBindRole {
		t.Fatalf("SET ROLE current_user=%q", currentUser)
	}
	if err = BindGuardianRuntimeRelease(ctx, setRoleTx, admin.Config().Database, imageDigest); err != nil {
		t.Fatalf("SET ROLE release cutoff failed: %v", err)
	}
}

func TestGuardianReleaseBindFirstRolloutStagesOn004AndActivatesAfter005(t *testing.T) {
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })

	rewind, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	reconstructExactGuardian004(t, ctx, rewind)
	if _, err = rewind.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS mycfc_meta;
		CREATE TABLE IF NOT EXISTS mycfc_meta.schema_migrations(version text PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now());
		DELETE FROM mycfc_meta.schema_migrations`); err != nil {
		_ = rewind.Rollback(ctx)
		t.Fatal(err)
	}
	for _, version := range EmbeddedMigrationInventory() {
		if version == "202609120005_guardian_authority_activation" || version == "202609120006_guardian_schema_ready_owner" {
			continue
		}
		if _, err = rewind.Exec(ctx, `INSERT INTO mycfc_meta.schema_migrations(version) VALUES($1)`, version); err != nil {
			_ = rewind.Rollback(ctx)
			t.Fatal(err)
		}
	}
	if err = rewind.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	password := "first-rollout-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	for range 2 {
		if err = ProvisionGuardianReleaseBindRole(ctx, admin, admin.Config().Database, guardianReleaseBindRole, password); err != nil {
			t.Fatalf("stage release role on exact 004: %v", err)
		}
	}
	var opsExists, connect, createDatabase, temp, publicUsage, metadataUsage, privacyDisableUsage, privacyProtectedUsage, userRead, privacyExecute bool
	var superuser, createDB, createRole, inherit, replication, bypassRLS bool
	var memberships int
	if err = admin.QueryRow(ctx, `SELECT
		to_regnamespace('guardian_ops') IS NOT NULL,
		has_database_privilege($1,current_database(),'CONNECT'),
		has_database_privilege($1,current_database(),'CREATE'),
		has_database_privilege($1,current_database(),'TEMP'),
		has_schema_privilege($1,'public','USAGE'),
		has_schema_privilege($1,'mycfc_meta','USAGE'),
		COALESCE(has_schema_privilege($1,to_regnamespace('privacy_disable'),'USAGE'),false),
		COALESCE(has_schema_privilege($1,to_regnamespace('privacy_protected'),'USAGE'),false),
		has_table_privilege($1,'public.users','SELECT'),
		has_function_privilege($1,'public.privacy_worker_status()','EXECUTE'),
		role.rolsuper,role.rolcreatedb,role.rolcreaterole,role.rolinherit,role.rolreplication,role.rolbypassrls,
		(SELECT count(*) FROM pg_auth_members WHERE member=role.oid)
		FROM pg_roles role WHERE role.rolname=$1`, guardianReleaseBindRole).Scan(
		&opsExists, &connect, &createDatabase, &temp, &publicUsage, &metadataUsage, &privacyDisableUsage, &privacyProtectedUsage, &userRead, &privacyExecute,
		&superuser, &createDB, &createRole, &inherit, &replication, &bypassRLS, &memberships); err != nil {
		t.Fatal(err)
	}
	if opsExists || !connect || createDatabase || temp || publicUsage || metadataUsage || privacyDisableUsage || privacyProtectedUsage || userRead || privacyExecute ||
		superuser || createDB || createRole || inherit || replication || bypassRLS || memberships != 0 {
		t.Fatalf("staged 004 role ops=%v connect=%v database_create=%v temp=%v public=%v metadata=%v privacy_disable=%v privacy_protected=%v users=%v privacy=%v super=%v createdb=%v createrole=%v inherit=%v replication=%v bypassrls=%v memberships=%d",
			opsExists, connect, createDatabase, temp, publicUsage, metadataUsage, privacyDisableUsage, privacyProtectedUsage, userRead, privacyExecute,
			superuser, createDB, createRole, inherit, replication, bypassRLS, memberships)
	}

	releaseConfig := admin.Config().Copy()
	releaseConfig.User = guardianReleaseBindRole
	releaseConfig.Password = password
	release, err := pgx.ConnectConfig(ctx, releaseConfig)
	if err != nil {
		t.Fatalf("connect staged release login: %v", err)
	}
	t.Cleanup(func() { _ = release.Close(context.Background()) })
	assertGuardianReleasePermissionDenied(t, ctx, release, "pre-005 public table", `SELECT * FROM public.users`)
	assertGuardianReleasePermissionDenied(t, ctx, release, "pre-005 temporary table", `CREATE TEMP TABLE release_bind_forbidden(id integer)`)

	migration, err := migrationFiles.ReadFile("migrations/202609120005_guardian_authority_activation.sql")
	if err != nil {
		t.Fatal(err)
	}
	upgrade, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = upgrade.Exec(ctx, string(migration)); err != nil {
		_ = upgrade.Rollback(ctx)
		t.Fatalf("apply exact staged 004 -> 005 migration: %v", err)
	}
	if _, err = upgrade.Exec(ctx, `INSERT INTO mycfc_meta.schema_migrations(version) VALUES('202609120005_guardian_authority_activation')`); err != nil {
		_ = upgrade.Rollback(ctx)
		t.Fatal(err)
	}
	repair, err := migrationFiles.ReadFile("migrations/202609120006_guardian_schema_ready_owner.sql")
	if err != nil {
		_ = upgrade.Rollback(ctx)
		t.Fatal(err)
	}
	if _, err = upgrade.Exec(ctx, string(repair)); err != nil {
		_ = upgrade.Rollback(ctx)
		t.Fatalf("apply exact 005 -> 006 repair migration: %v", err)
	}
	if _, err = upgrade.Exec(ctx, `INSERT INTO mycfc_meta.schema_migrations(version) VALUES('202609120006_guardian_schema_ready_owner')`); err != nil {
		_ = upgrade.Rollback(ctx)
		t.Fatal(err)
	}
	if err = upgrade.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	// The production release sequence runs db-bootstrap before migrations and
	// hardening. Recreate its fixed retention capability role here because this
	// test deliberately reconstructed schema 004 without invoking BootstrapRoles.
	if _, err = admin.Exec(ctx, noLoginRoleStatement(privacyRetentionRole)); err != nil {
		t.Fatalf("recreate pre-migration bootstrap role: %v", err)
	}

	appRole := "guardian_first_rollout_web_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	if _, err = admin.Exec(ctx, `CREATE ROLE `+quoteIdentifier(appRole)+` NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DROP OWNED BY `+quoteIdentifier(appRole))
		_, _ = admin.Exec(context.Background(), `DROP ROLE IF EXISTS `+quoteIdentifier(appRole))
	})
	var migrationRole string
	if err = admin.QueryRow(ctx, `SELECT current_user`).Scan(&migrationRole); err != nil {
		t.Fatal(err)
	}
	if err = HardenPrivacyExecutionRoles(ctx, admin, admin.Config().Database, RoleCredentials{
		AppUsername: appRole, AppPassword: "unused",
		MigrationUsername: migrationRole, MigrationPassword: "unused",
	}); err != nil {
		t.Fatalf("harden after staged 005 and 006: %v", err)
	}

	var bind, status, preflight, enable, disable, runtimeRead, approvalRead, opsCreate bool
	if err = admin.QueryRow(ctx, `SELECT
		has_function_privilege($1,'guardian_ops.release_disable_and_bind(text,text,text)','EXECUTE'),
		has_function_privilege($1,'guardian_ops.status(text,text,text)','EXECUTE'),
		has_function_privilege($1,'guardian_ops.preflight(uuid,text,bytea,bytea,text,text,text)','EXECUTE'),
		has_function_privilege($1,'guardian_ops.enable(uuid,text,bytea,bytea,text,text,text)','EXECUTE'),
		has_function_privilege($1,'guardian_ops.disable(uuid,text)','EXECUTE'),
		has_table_privilege($1,'guardian_ops.runtime_release_binding','SELECT'),
		has_table_privilege($1,'public.guardian_authority_policy_approvals','SELECT'),
		has_schema_privilege($1,'guardian_ops','CREATE'),
		COALESCE(has_schema_privilege($1,to_regnamespace('privacy_disable'),'USAGE'),false),
		COALESCE(has_schema_privilege($1,to_regnamespace('privacy_protected'),'USAGE'),false)`, guardianReleaseBindRole).Scan(
		&bind, &status, &preflight, &enable, &disable, &runtimeRead, &approvalRead, &opsCreate, &privacyDisableUsage, &privacyProtectedUsage); err != nil {
		t.Fatal(err)
	}
	if !bind || status || preflight || enable || disable || runtimeRead || approvalRead || opsCreate || privacyDisableUsage || privacyProtectedUsage {
		t.Fatalf("post-005 release role bind=%v status=%v preflight=%v enable=%v disable=%v runtime=%v approval=%v create=%v privacy_disable=%v privacy_protected=%v",
			bind, status, preflight, enable, disable, runtimeRead, approvalRead, opsCreate, privacyDisableUsage, privacyProtectedUsage)
	}

	imageDigest := "sha256:" + strings.Repeat("b", 64)
	bindTx, err := release.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = BindGuardianRuntimeRelease(ctx, bindTx, admin.Config().Database, imageDigest); err != nil {
		_ = bindTx.Rollback(ctx)
		t.Fatalf("first-rollout release login could not bind candidate: %v", err)
	}
	if err = bindTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var boundImage, boundSchema, boundDatabase string
	if err = admin.QueryRow(ctx, `SELECT image_digest,schema_migration_digest,database_name
		FROM guardian_ops.runtime_release_binding WHERE singleton`).Scan(&boundImage, &boundSchema, &boundDatabase); err != nil {
		t.Fatal(err)
	}
	if boundImage != imageDigest || boundSchema != EmbeddedMigrationDigest() || boundDatabase != admin.Config().Database {
		t.Fatalf("candidate binding image=%q schema=%q database=%q", boundImage, boundSchema, boundDatabase)
	}

	assertGuardianReleasePermissionDenied(t, ctx, release, "post-005 status", `SELECT * FROM guardian_ops.status(NULL,NULL,NULL)`)
	assertGuardianReleasePermissionDenied(t, ctx, release, "post-005 enable", `SELECT * FROM guardian_ops.enable(NULL,NULL,NULL,NULL,NULL,NULL,NULL)`)
	assertGuardianReleasePermissionDenied(t, ctx, release, "post-005 approval table", `SELECT * FROM public.guardian_authority_policy_approvals`)
	assertGuardianReleasePermissionDenied(t, ctx, release, "post-005 DDL", `ALTER FUNCTION guardian_ops.release_disable_and_bind(text,text,text) RENAME TO release_bind_forbidden`)
}

func assertGuardianReleasePermissionDenied(t *testing.T, ctx context.Context, conn *pgx.Conn, name, statement string) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, execErr := tx.Exec(ctx, statement)
		_ = tx.Rollback(ctx)
		var databaseErr *pgconn.PgError
		if !errors.As(execErr, &databaseErr) || databaseErr.Code != "42501" {
			t.Fatalf("error=%v, want permission denied", execErr)
		}
	})
}
