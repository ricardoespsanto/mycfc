//go:build integration

package db

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestHardenPrivacyExecutionRolesEnforcesWorkerBoundary(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })

	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	appRole := "privacy_web_" + suffix
	executorRole := "privacy_worker_" + suffix
	observerRole := "privacy_observer_" + suffix
	for _, role := range []string{appRole, executorRole, observerRole} {
		if _, err = tx.Exec(ctx, `CREATE ROLE `+quoteIdentifier(role)+` NOLOGIN`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = tx.Exec(ctx, `DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='mycfc_privacy_retention') THEN CREATE ROLE mycfc_privacy_retention NOLOGIN; END IF; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `GRANT EXECUTE ON FUNCTION privacy_upload_cleanup_claim(bigint,uuid) TO `+quoteIdentifier(appRole)); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `GRANT EXECUTE ON FUNCTION privacy_upload_begin(uuid,uuid,uuid,text,uuid,text,text,bigint,bytea) TO `+quoteIdentifier(executorRole)); err != nil {
		t.Fatal(err)
	}
	credentials := RoleCredentials{
		AppUsername: appRole, AppPassword: "unused-app-password",
		MigrationUsername: "unused_migrator", MigrationPassword: "unused-migration-password",
		PrivacyExecutorUsername: executorRole, PrivacyExecutorPassword: "unused-executor-password",
		PrivacyRestoreObserverUsername: observerRole, PrivacyRestoreObserverPassword: "unused-observer-password",
	}
	for range 2 {
		if err = HardenPrivacyExecutionRoles(ctx, tx, conn.Config().Database, credentials); err != nil {
			t.Fatal(err)
		}
	}

	for _, check := range []struct {
		name, role, table, privilege string
		want                         bool
	}{
		{"web reads execution", appRole, "privacy_erasure_executions", "SELECT", true},
		{"web cannot update execution", appRole, "privacy_erasure_executions", "UPDATE", false},
		{"web cannot read leases", appRole, "privacy_erasure_job_leases", "SELECT", false},
		{"worker reads immutable plan", executorRole, "privacy_request_execution_plans", "SELECT", true},
		{"worker has no table-wide request read", executorRole, "data_erasure_requests", "SELECT", false},
		{"worker cannot read accounts", executorRole, "users", "SELECT", false},
		{"worker cannot delete sessions", executorRole, "sessions", "DELETE", false},
		{"worker cannot mutate access revocations", executorRole, "privacy_erasure_access_revocations", "INSERT", false},
		{"worker cannot mutate plan", executorRole, "privacy_request_execution_plans", "UPDATE", false},
		{"worker cannot read request events", executorRole, "data_erasure_request_events", "SELECT", false},
		{"web cannot read restricted records", appRole, "privacy_erasure_restricted_records", "SELECT", false},
		{"web cannot read pseudonymous principals", appRole, "privacy_pseudonymous_principals", "SELECT", false},
		{"worker reads retention anchors", executorRole, "privacy_erasure_retention_anchors", "SELECT", true},
		{"worker cannot update memberships", executorRole, "user_memberships", "UPDATE", false},
		{"worker cannot update equipment audit", executorRole, "equipment_audit_events", "UPDATE", false},
		{"worker cannot update pseudonymous principals", executorRole, "privacy_pseudonymous_principals", "UPDATE", false},
		{"web cannot read protected targets", appRole, "privacy_protected.object_targets", "SELECT", false},
		{"web cannot insert protected targets", appRole, "privacy_protected.object_targets", "INSERT", false},
		{"worker cannot read protected targets directly", executorRole, "privacy_protected.object_targets", "SELECT", false},
		{"worker cannot read protected evidence directly", executorRole, "privacy_protected.object_evidence", "SELECT", false},
		{"web cannot read protected upload intents", appRole, "privacy_protected.object_upload_intents", "SELECT", false},
		{"web cannot append protected upload events", appRole, "privacy_protected.object_upload_intent_events", "INSERT", false},
		{"worker cannot read protected upload intents directly", executorRole, "privacy_protected.object_upload_intents", "SELECT", false},
		{"worker cannot append protected upload events directly", executorRole, "privacy_protected.object_upload_intent_events", "INSERT", false},
	} {
		t.Run(check.name, func(t *testing.T) {
			var got bool
			if err := tx.QueryRow(ctx, `SELECT has_table_privilege($1, $2, $3)`, check.role, check.table, check.privilege).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != check.want {
				t.Fatalf("has_table_privilege(%q, %q, %q)=%t want %t", check.role, check.table, check.privilege, got, check.want)
			}
		})
	}
	for _, role := range []string{appRole, executorRole} {
		var usage bool
		if err := tx.QueryRow(ctx, `SELECT has_schema_privilege($1,'privacy_protected','USAGE')`, role).Scan(&usage); err != nil {
			t.Fatal(err)
		}
		if usage {
			t.Fatalf("role %q unexpectedly has privacy_protected schema usage", role)
		}
	}

	for _, check := range []struct {
		name, role, table, column, privilege string
		want                                 bool
	}{
		{"web inserts handoff identity", appRole, "privacy_erasure_executions", "request_id", "INSERT", true},
		{"web cannot insert execution status", appRole, "privacy_erasure_executions", "status", "INSERT", false},
		{"worker cannot directly update execution status", executorRole, "privacy_erasure_executions", "status", "UPDATE", false},
		{"worker cannot change execution request", executorRole, "privacy_erasure_executions", "request_id", "UPDATE", false},
		{"worker cannot directly insert lease owner", executorRole, "privacy_erasure_job_leases", "worker_ref", "INSERT", false},
		{"worker cannot replace lease owner", executorRole, "privacy_erasure_job_leases", "worker_ref", "UPDATE", false},
		{"worker cannot directly transition request status", executorRole, "data_erasure_requests", "status", "UPDATE", false},
		{"worker reads request status", executorRole, "data_erasure_requests", "status", "SELECT", true},
		{"worker cannot read request subject", executorRole, "data_erasure_requests", "subject_user_id", "SELECT", false},
		{"worker cannot change request decision", executorRole, "data_erasure_requests", "decision_code", "UPDATE", false},
		{"worker cannot directly append request event", executorRole, "data_erasure_request_events", "action", "INSERT", false},
	} {
		t.Run(check.name, func(t *testing.T) {
			var got bool
			if err := tx.QueryRow(ctx, `SELECT has_column_privilege($1, $2, $3, $4)`, check.role, check.table, check.column, check.privilege).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != check.want {
				t.Fatalf("has_column_privilege(%q, %q, %q, %q)=%t want %t", check.role, check.table, check.column, check.privilege, got, check.want)
			}
		})
	}
	var canExecute, canMutate, canBypass, canCleanup, canUpload, canListObjects, canRecordObjectEvidence, canCompleteObjectCheckpoint, canCaptureObjects bool
	var canListProviders, canRecordProviderEvidence, canCompleteProviderCheckpoint, canCaptureProviders bool
	if err := tx.QueryRow(ctx, `SELECT
		has_function_privilege($1,'privacy_worker_claim(bigint,uuid)','EXECUTE'),
		has_function_privilege($1,'privacy_worker_execute_checkpoint(uuid,uuid,uuid,bigint,uuid,text,text)','EXECUTE'),
		has_function_privilege($1,'privacy_worker_complete_checkpoint(uuid,uuid,uuid,bigint,uuid,text,text)','EXECUTE'),
		has_function_privilege($1,'privacy_upload_cleanup_claim(bigint,uuid)','EXECUTE'),
		has_function_privilege($1,'privacy_upload_begin(uuid,uuid,uuid,text,uuid,text,text,bigint,bytea)','EXECUTE'),
		has_function_privilege($1,'privacy_worker_list_object_targets(uuid,uuid,uuid,bigint,uuid)','EXECUTE'),
		has_function_privilege($1,'privacy_worker_record_object_evidence(uuid,uuid,uuid,uuid,bigint,uuid,integer,integer,integer,integer,text,bytea)','EXECUTE'),
		has_function_privilege($1,'privacy_worker_complete_object_checkpoint(uuid,uuid,uuid,bigint,uuid)','EXECUTE'),
		has_function_privilege($1,'privacy_execution_capture_media_sources(uuid,uuid,text)','EXECUTE'),
		has_function_privilege($1,'privacy_worker_list_provider_targets(uuid,uuid,uuid,bigint,uuid)','EXECUTE'),
		has_function_privilege($1,'privacy_worker_record_provider_evidence(uuid,uuid,uuid,uuid,bigint,uuid,text,integer,text,text,text,text,text,text,text,bytea)','EXECUTE'),
		has_function_privilege($1,'privacy_worker_complete_provider_checkpoint(uuid,uuid,uuid,bigint,uuid)','EXECUTE'),
		has_function_privilege($1,'privacy_execution_capture_provider_connections(uuid,uuid,text)','EXECUTE')`, executorRole).Scan(&canExecute, &canMutate, &canBypass, &canCleanup, &canUpload, &canListObjects, &canRecordObjectEvidence, &canCompleteObjectCheckpoint, &canCaptureObjects, &canListProviders, &canRecordProviderEvidence, &canCompleteProviderCheckpoint, &canCaptureProviders); err != nil {
		t.Fatal(err)
	}
	if !canExecute || !canMutate || canBypass || !canCleanup || canUpload || !canListObjects || !canRecordObjectEvidence || !canCompleteObjectCheckpoint || canCaptureObjects || !canListProviders || !canRecordProviderEvidence || !canCompleteProviderCheckpoint || canCaptureProviders {
		t.Fatalf("worker function boundary claim=%v mutate=%v legacy_bypass=%v upload_cleanup=%v upload_lifecycle=%v object_list=%v object_evidence=%v object_complete=%v object_capture=%v provider_list=%v provider_evidence=%v provider_complete=%v provider_capture=%v",
			canExecute, canMutate, canBypass, canCleanup, canUpload, canListObjects, canRecordObjectEvidence, canCompleteObjectCheckpoint, canCaptureObjects,
			canListProviders, canRecordProviderEvidence, canCompleteProviderCheckpoint, canCaptureProviders)
	}
	var appCanUpload, appCanCleanup, appCanCapture, appCanMaterialize, appCanCompleteCapture, appCanListObjects bool
	var appCanCaptureProviders, appCanMaterializeProvider, appCanCompleteProviderCapture, appCanListProviders bool
	if err := tx.QueryRow(ctx, `SELECT
		has_function_privilege($1,'privacy_upload_begin(uuid,uuid,uuid,text,uuid,text,text,bigint,bytea)','EXECUTE'),
		has_function_privilege($1,'privacy_upload_cleanup_claim(bigint,uuid)','EXECUTE'),
		has_function_privilege($1,'privacy_execution_capture_media_sources(uuid,uuid,text)','EXECUTE'),
		has_function_privilege($1,'privacy_execution_materialize_object_target(uuid,uuid,uuid,uuid,bytea,text,text,uuid,uuid,text,text,text,text,bytea,bytea,bytea,text,bytea)','EXECUTE'),
		has_function_privilege($1,'privacy_execution_complete_object_capture(uuid,text)','EXECUTE'),
		has_function_privilege($1,'privacy_worker_list_object_targets(uuid,uuid,uuid,bigint,uuid)','EXECUTE'),
		has_function_privilege($1,'privacy_execution_capture_provider_connections(uuid,uuid,text)','EXECUTE'),
		has_function_privilege($1,'privacy_execution_materialize_provider_target(uuid,uuid,uuid,uuid,uuid,bytea,text,text,text,text,bigint,text,text,bytea,text,text,text,bytea,bytea,bytea,text,text,text,bytea,bytea,bytea,text,bytea,text,bytea)','EXECUTE'),
		has_function_privilege($1,'privacy_execution_complete_provider_capture(uuid,text)','EXECUTE'),
		has_function_privilege($1,'privacy_worker_list_provider_targets(uuid,uuid,uuid,bigint,uuid)','EXECUTE')`, appRole).Scan(&appCanUpload, &appCanCleanup, &appCanCapture, &appCanMaterialize, &appCanCompleteCapture, &appCanListObjects, &appCanCaptureProviders, &appCanMaterializeProvider, &appCanCompleteProviderCapture, &appCanListProviders); err != nil {
		t.Fatal(err)
	}
	if !appCanUpload || appCanCleanup || !appCanCapture || !appCanMaterialize || !appCanCompleteCapture || appCanListObjects || !appCanCaptureProviders || !appCanMaterializeProvider || !appCanCompleteProviderCapture || appCanListProviders {
		t.Fatalf("web function boundary upload=%v cleanup=%v capture=%v materialize=%v complete_capture=%v object_list=%v provider_capture=%v provider_materialize=%v provider_complete=%v provider_list=%v",
			appCanUpload, appCanCleanup, appCanCapture, appCanMaterialize, appCanCompleteCapture, appCanListObjects,
			appCanCaptureProviders, appCanMaterializeProvider, appCanCompleteProviderCapture, appCanListProviders)
	}
	var retentionLogin, retentionReadsUsers, retentionReadsRuns, retentionProtectedUsage bool
	var retentionRuns, retentionStatus, retentionInternal, retentionCleanup bool
	if err = tx.QueryRow(ctx, `SELECT
	 (SELECT rolcanlogin FROM pg_roles WHERE rolname='mycfc_privacy_retention'),
	 has_table_privilege('mycfc_privacy_retention','users','SELECT'),
	 has_table_privilege('mycfc_privacy_retention','privacy_retention_runs','SELECT'),
	 has_schema_privilege('mycfc_privacy_retention','privacy_protected','USAGE'),
	 has_function_privilege('mycfc_privacy_retention','privacy_retention_run(uuid,integer)','EXECUTE'),
	 has_function_privilege('mycfc_privacy_retention','privacy_retention_status()','EXECUTE'),
	 has_function_privilege('mycfc_privacy_retention','privacy_retention_pseudonymize_audit(integer)','EXECUTE'),
	 has_function_privilege('mycfc_privacy_retention','privacy_upload_cleanup_claim(bigint,uuid)','EXECUTE')`).Scan(
		&retentionLogin, &retentionReadsUsers, &retentionReadsRuns, &retentionProtectedUsage,
		&retentionRuns, &retentionStatus, &retentionInternal, &retentionCleanup); err != nil {
		t.Fatal(err)
	}
	if retentionLogin || retentionReadsUsers || retentionReadsRuns || retentionProtectedUsage || !retentionRuns || !retentionStatus || retentionInternal || retentionCleanup {
		t.Fatalf("retention boundary login=%v users=%v runs_table=%v protected=%v run=%v status=%v internal=%v cleanup=%v",
			retentionLogin, retentionReadsUsers, retentionReadsRuns, retentionProtectedUsage, retentionRuns, retentionStatus, retentionInternal, retentionCleanup)
	}
	var observerReadsUsers, observerReadsAttestations, observerProtectedUsage, observerRunsReplay, observerRunsAggregate bool
	if err = tx.QueryRow(ctx, `SELECT
		has_table_privilege($1,'users','SELECT'),
		has_table_privilege($1,'privacy_protected.restore_replay_inventory_attestations','SELECT'),
		has_schema_privilege($1,'privacy_protected','USAGE'),
		has_function_privilege($1,'privacy_restore_begin_replay_hardened(uuid,uuid)','EXECUTE'),
		has_function_privilege($1,'privacy_restore_observe_inventory(text,bytea,bytea,text,text,text,text)','EXECUTE')`, observerRole).
		Scan(&observerReadsUsers, &observerReadsAttestations, &observerProtectedUsage, &observerRunsReplay, &observerRunsAggregate); err != nil {
		t.Fatal(err)
	}
	if observerReadsUsers || observerReadsAttestations || observerProtectedUsage || observerRunsReplay || !observerRunsAggregate {
		t.Fatalf("observer boundary users=%v attestations=%v protected=%v replay=%v aggregate=%v",
			observerReadsUsers, observerReadsAttestations, observerProtectedUsage, observerRunsReplay, observerRunsAggregate)
	}
	if _, err = tx.Exec(ctx, `SET LOCAL ROLE `+quoteIdentifier(executorRole)); err != nil {
		t.Fatal(err)
	}
	var claimed int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM privacy_worker_claim(1000,$1)`, uuid.New()).Scan(&claimed); err != nil {
		t.Fatalf("restricted worker could not invoke fenced claim: %v", err)
	}
	if _, err = tx.Exec(ctx, `RESET ROLE`); err != nil {
		t.Fatal(err)
	}
}
