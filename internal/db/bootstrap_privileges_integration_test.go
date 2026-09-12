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

func TestProvisionGuardianActivationRoleHasOnlyFixedOperatorAPI(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	installGuardianActivationInventory(t, ctx, tx)
	if err = ProvisionGuardianActivationRole(ctx, tx, conn.Config().Database, guardianActivationRole, "integration-only-password"); err != nil {
		t.Fatal(err)
	}

	var connect, operatorUsage, publicUsage, metadataUsage, tableRead, runtimeRead, status, preflight, enable, disable, helper, releaseBind bool
	if err = tx.QueryRow(ctx, `SELECT
		has_database_privilege($1,current_database(),'CONNECT'),
		has_schema_privilege($1,'guardian_ops','USAGE'),
		has_schema_privilege($1,'public','USAGE'),
		has_schema_privilege($1,'mycfc_meta','USAGE'),
		has_table_privilege($1,'guardian_authority_policy_approvals','SELECT'),
		has_table_privilege($1,'guardian_ops.runtime_release_binding','SELECT'),
		has_function_privilege($1,'guardian_ops.status(text,text,text)','EXECUTE'),
		has_function_privilege($1,'guardian_ops.preflight(uuid,text,bytea,bytea,text,text,text)','EXECUTE'),
		has_function_privilege($1,'guardian_ops.enable(uuid,text,bytea,bytea,text,text,text)','EXECUTE'),
		has_function_privilege($1,'guardian_ops.disable(uuid,text)','EXECUTE'),
		has_function_privilege($1,'guardian_ops.schema_ready(text)','EXECUTE'),
		has_function_privilege($1,'guardian_ops.release_disable_and_bind(text,text,text)','EXECUTE')`, guardianActivationRole).
		Scan(&connect, &operatorUsage, &publicUsage, &metadataUsage, &tableRead, &runtimeRead, &status, &preflight, &enable, &disable, &helper, &releaseBind); err != nil {
		t.Fatal(err)
	}
	if !connect || !operatorUsage || publicUsage || metadataUsage || tableRead || runtimeRead || !status || !preflight || !enable || !disable || helper || releaseBind {
		t.Fatalf("operator boundary connect=%v ops=%v public=%v metadata=%v table=%v runtime=%v status=%v preflight=%v enable=%v disable=%v helper=%v release=%v",
			connect, operatorUsage, publicUsage, metadataUsage, tableRead, runtimeRead, status, preflight, enable, disable, helper, releaseBind)
	}
	var superuser, createDB, createRole, inherit, replication, bypassRLS bool
	if err = tx.QueryRow(ctx, `SELECT rolsuper,rolcreatedb,rolcreaterole,rolinherit,rolreplication,rolbypassrls FROM pg_roles WHERE rolname=$1`, guardianActivationRole).
		Scan(&superuser, &createDB, &createRole, &inherit, &replication, &bypassRLS); err != nil {
		t.Fatal(err)
	}
	if superuser || createDB || createRole || inherit || replication || bypassRLS {
		t.Fatalf("operator role attributes super=%v createdb=%v createrole=%v inherit=%v replication=%v bypassrls=%v", superuser, createDB, createRole, inherit, replication, bypassRLS)
	}
}

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
	migrationRole := "migration_release_" + suffix
	for _, role := range []string{appRole, executorRole, observerRole, migrationRole} {
		if _, err = tx.Exec(ctx, `CREATE ROLE `+quoteIdentifier(role)+` NOLOGIN`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = tx.Exec(ctx, `GRANT USAGE ON SCHEMA public TO `+quoteIdentifier(appRole)); err != nil {
		t.Fatal(err)
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
	if _, err = tx.Exec(ctx, `GRANT ALL PRIVILEGES ON TABLE guardian_authority_invitations, guardian_application_rate_events, guardian_application_intake_release, guardian_application_intake_release_events, guardian_authority_review_access_events, guardian_authority_renewal_requests, guardian_authority_renewal_events, guardian_authority_renewal_reminders, guardian_age_handoffs, guardian_age_handoff_events, guardian_age_handoff_email_tokens, guardian_age_handoff_notices, guardian_age_handoff_access_events TO `+quoteIdentifier(appRole)); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `GRANT ALL PRIVILEGES ON SEQUENCE guardian_age_handoff_events_id_seq,guardian_age_handoff_access_events_id_seq TO PUBLIC, `+quoteIdentifier(appRole)); err != nil {
		t.Fatal(err)
	}
	guardianAdminID := uuid.New()
	if _, err = tx.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth,email_verified_at) VALUES($1,'Guardian privilege administrator',$2,'unused','1990-01-01',clock_timestamp())`, guardianAdminID, "guardian-privilege-"+suffix+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, guardianAdminID); err != nil {
		t.Fatal(err)
	}
	credentials := RoleCredentials{
		AppUsername: appRole, AppPassword: "unused-app-password",
		MigrationUsername: migrationRole, MigrationPassword: "unused-migration-password",
		PrivacyExecutorUsername: executorRole, PrivacyExecutorPassword: "unused-executor-password",
		PrivacyRestoreObserverUsername: observerRole, PrivacyRestoreObserverPassword: "unused-observer-password",
	}
	for range 2 {
		if err = HardenPrivacyExecutionRoles(ctx, tx, conn.Config().Database, credentials); err != nil {
			t.Fatal(err)
		}
	}
	var webGuardianOps, webGuardianApprovalRead, webGuardianEnable bool
	if err = tx.QueryRow(ctx, `SELECT
		has_schema_privilege($1,'guardian_ops','USAGE'),
		has_table_privilege($1,'guardian_authority_policy_approvals','SELECT'),
		has_function_privilege($1,'guardian_ops.enable(uuid,text,bytea,bytea,text,text,text)','EXECUTE')`, appRole).
		Scan(&webGuardianOps, &webGuardianApprovalRead, &webGuardianEnable); err != nil {
		t.Fatal(err)
	}
	if webGuardianOps || webGuardianApprovalRead || webGuardianEnable {
		t.Fatalf("web guardian control plane ops=%v approval_read=%v enable=%v", webGuardianOps, webGuardianApprovalRead, webGuardianEnable)
	}
	var releaseBind, releaseEnable, releasePreflight, releaseDisable, releaseTableRead, schemaReady bool
	if err = tx.QueryRow(ctx, `SELECT
		has_function_privilege($1,'guardian_ops.release_disable_and_bind(text,text,text)','EXECUTE'),
		has_function_privilege($1,'guardian_ops.enable(uuid,text,bytea,bytea,text,text,text)','EXECUTE'),
		has_function_privilege($1,'guardian_ops.preflight(uuid,text,bytea,bytea,text,text,text)','EXECUTE'),
		has_function_privilege($1,'guardian_ops.disable(uuid,text)','EXECUTE'),
		has_table_privilege($1,'guardian_ops.runtime_release_binding','SELECT'),
		has_function_privilege($1,'guardian_ops.schema_ready(text)','EXECUTE')`, migrationRole).
		Scan(&releaseBind, &releaseEnable, &releasePreflight, &releaseDisable, &releaseTableRead, &schemaReady); err != nil {
		t.Fatal(err)
	}
	if releaseBind || releaseEnable || releasePreflight || releaseDisable || releaseTableRead || !schemaReady {
		t.Fatalf("release boundary bind=%t enable=%t preflight=%t disable=%t table=%t schema_ready=%t", releaseBind, releaseEnable, releasePreflight, releaseDisable, releaseTableRead, schemaReady)
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
		{"web cannot read guardian invitations", appRole, "guardian_authority_invitations", "SELECT", false},
		{"web cannot insert guardian invitations", appRole, "guardian_authority_invitations", "INSERT", false},
		{"web cannot update guardian invitations", appRole, "guardian_authority_invitations", "UPDATE", false},
		{"web cannot delete guardian invitations", appRole, "guardian_authority_invitations", "DELETE", false},
		{"web cannot read guardian rate events", appRole, "guardian_application_rate_events", "SELECT", false},
		{"web cannot insert guardian rate events", appRole, "guardian_application_rate_events", "INSERT", false},
		{"web cannot update guardian rate events", appRole, "guardian_application_rate_events", "UPDATE", false},
		{"web cannot delete guardian rate events", appRole, "guardian_application_rate_events", "DELETE", false},
		{"web cannot read guardian intake release gate", appRole, "guardian_application_intake_release", "SELECT", false},
		{"web cannot insert guardian intake release gate", appRole, "guardian_application_intake_release", "INSERT", false},
		{"web cannot update guardian intake release gate", appRole, "guardian_application_intake_release", "UPDATE", false},
		{"web cannot delete guardian intake release gate", appRole, "guardian_application_intake_release", "DELETE", false},
		{"web cannot read guardian intake release audit", appRole, "guardian_application_intake_release_events", "SELECT", false},
		{"web cannot insert guardian intake release audit", appRole, "guardian_application_intake_release_events", "INSERT", false},
		{"web cannot update guardian intake release audit", appRole, "guardian_application_intake_release_events", "UPDATE", false},
		{"web cannot delete guardian intake release audit", appRole, "guardian_application_intake_release_events", "DELETE", false},
		{"web cannot read guardian review access audit", appRole, "guardian_authority_review_access_events", "SELECT", false},
		{"web cannot insert guardian review access audit", appRole, "guardian_authority_review_access_events", "INSERT", false},
		{"web cannot update guardian review access audit", appRole, "guardian_authority_review_access_events", "UPDATE", false},
		{"web cannot delete guardian review access audit", appRole, "guardian_authority_review_access_events", "DELETE", false},
		{"web cannot read guardian renewal requests", appRole, "guardian_authority_renewal_requests", "SELECT", false},
		{"web cannot insert guardian renewal requests", appRole, "guardian_authority_renewal_requests", "INSERT", false},
		{"web cannot update guardian renewal events", appRole, "guardian_authority_renewal_events", "UPDATE", false},
		{"web cannot delete guardian renewal events", appRole, "guardian_authority_renewal_events", "DELETE", false},
		{"web cannot read guardian reminder evidence", appRole, "guardian_authority_renewal_reminders", "SELECT", false},
		{"web cannot insert guardian reminder evidence", appRole, "guardian_authority_renewal_reminders", "INSERT", false},
		{"web cannot read guardian handoffs", appRole, "guardian_age_handoffs", "SELECT", false},
		{"web cannot update guardian handoffs", appRole, "guardian_age_handoffs", "UPDATE", false},
		{"web cannot read guardian handoff events", appRole, "guardian_age_handoff_events", "SELECT", false},
		{"web cannot insert guardian handoff events", appRole, "guardian_age_handoff_events", "INSERT", false},
		{"web cannot read guardian handoff tokens", appRole, "guardian_age_handoff_email_tokens", "SELECT", false},
		{"web cannot delete guardian handoff tokens", appRole, "guardian_age_handoff_email_tokens", "DELETE", false},
		{"web cannot read guardian handoff notices", appRole, "guardian_age_handoff_notices", "SELECT", false},
		{"web cannot insert guardian handoff notices", appRole, "guardian_age_handoff_notices", "INSERT", false},
		{"web cannot read guardian handoff access audit", appRole, "guardian_age_handoff_access_events", "SELECT", false},
		{"web cannot mutate guardian handoff access audit", appRole, "guardian_age_handoff_access_events", "UPDATE", false},
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
	var appCanReadHandoff, appCanReadGuardianHandoff, appCanProposeHandoff, appCanConfirmHandoff bool
	if err := tx.QueryRow(ctx, `SELECT
		has_function_privilege($1,'guardian_age_handoff_for_subject(uuid)','EXECUTE'),
		has_function_privilege($1,'guardian_age_handoff_for_guardian(uuid,uuid)','EXECUTE'),
		has_function_privilege($1,'guardian_age_handoff_propose_email(uuid,bigint,citext,bytea,timestamptz,bytea)','EXECUTE'),
		has_function_privilege($1,'guardian_age_handoff_admin_confirm(uuid,uuid,bigint)','EXECUTE')`, appRole).Scan(&appCanReadHandoff, &appCanReadGuardianHandoff, &appCanProposeHandoff, &appCanConfirmHandoff); err != nil {
		t.Fatal(err)
	}
	if !appCanReadHandoff || !appCanReadGuardianHandoff || !appCanProposeHandoff || !appCanConfirmHandoff {
		t.Fatalf("handoff routine boundary subject_read=%v guardian_read=%v propose=%v confirm=%v", appCanReadHandoff, appCanReadGuardianHandoff, appCanProposeHandoff, appCanConfirmHandoff)
	}
	for _, sequence := range []string{"guardian_age_handoff_events_id_seq", "guardian_age_handoff_access_events_id_seq"} {
		var appUsage, publicUsage bool
		if err := tx.QueryRow(ctx, `SELECT has_sequence_privilege($1,$2,'USAGE'),EXISTS(
			SELECT 1 FROM pg_class sequence JOIN LATERAL aclexplode(COALESCE(sequence.relacl,acldefault('S',sequence.relowner))) acl ON true
			WHERE sequence.oid=$2::regclass AND acl.grantee=0 AND acl.privilege_type IN('USAGE','SELECT','UPDATE'))`, appRole, sequence).Scan(&appUsage, &publicUsage); err != nil {
			t.Fatal(err)
		}
		if appUsage || publicUsage {
			t.Fatalf("sequence %q remained available app=%v public=%v", sequence, appUsage, publicUsage)
		}
	}
	if _, err = tx.Exec(ctx, `SET LOCAL ROLE `+quoteIdentifier(appRole)); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT * FROM guardian_age_handoff_list_for_admin($1,1,0)`, guardianAdminID); err != nil {
		t.Fatalf("security-definer handoff audit append failed: %v", err)
	}
	if _, err = tx.Exec(ctx, `RESET ROLE`); err != nil {
		t.Fatal(err)
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
	var canExecute, canMutate, canBypass, canCallInner, canCallPostconditionInner, canCleanup, canUpload, canListObjects, canRecordObjectEvidence, canCompleteObjectCheckpoint, canCaptureObjects bool
	var canListProviders, canRecordProviderEvidence, canCompleteProviderCheckpoint, canCaptureProviders bool
	var canPrepareClosureV2, canConfirmClosureV2, canPrepareClosureV3, canConfirmClosureV3, canPrepareClosureV4, canConfirmClosureV4, canCallMembershipLock, canCheckCompletionPostcondition bool
	if err := tx.QueryRow(ctx, `SELECT
		has_function_privilege($1,'privacy_worker_claim(bigint,uuid)','EXECUTE'),
		has_function_privilege($1,'privacy_worker_execute_checkpoint(uuid,uuid,uuid,bigint,uuid,text,text)','EXECUTE'),
		has_function_privilege($1,'privacy_worker_complete_checkpoint(uuid,uuid,uuid,bigint,uuid,text,text)','EXECUTE'),
		has_function_privilege($1,'privacy_tombstone_confirm_closure_v3_inner_013(uuid,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz)','EXECUTE'),
		has_function_privilege($1,'privacy_worker_execute_checkpoint_inner_015(uuid,uuid,uuid,bigint,uuid,text,text)','EXECUTE'),
		has_function_privilege($1,'privacy_upload_cleanup_claim(bigint,uuid)','EXECUTE'),
		has_function_privilege($1,'privacy_upload_begin(uuid,uuid,uuid,text,uuid,text,text,bigint,bytea)','EXECUTE'),
		has_function_privilege($1,'privacy_worker_list_object_targets(uuid,uuid,uuid,bigint,uuid)','EXECUTE'),
		has_function_privilege($1,'privacy_worker_record_object_evidence(uuid,uuid,uuid,uuid,bigint,uuid,integer,integer,integer,integer,text,bytea)','EXECUTE'),
		has_function_privilege($1,'privacy_worker_complete_object_checkpoint(uuid,uuid,uuid,bigint,uuid)','EXECUTE'),
		has_function_privilege($1,'privacy_execution_capture_media_sources(uuid,uuid,text)','EXECUTE'),
		has_function_privilege($1,'privacy_worker_list_provider_targets(uuid,uuid,uuid,bigint,uuid)','EXECUTE'),
		has_function_privilege($1,'privacy_worker_record_provider_evidence(uuid,uuid,uuid,uuid,bigint,uuid,text,integer,text,text,text,text,text,text,text,bytea)','EXECUTE'),
		has_function_privilege($1,'privacy_worker_complete_provider_checkpoint(uuid,uuid,uuid,bigint,uuid)','EXECUTE'),
		has_function_privilege($1,'privacy_execution_capture_provider_connections(uuid,uuid,text)','EXECUTE'),
		has_function_privilege($1,'privacy_tombstone_prepare_closure_v2(uuid,uuid)','EXECUTE'),
		has_function_privilege($1,'privacy_tombstone_confirm_closure_v2(uuid,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz)','EXECUTE'),
		has_function_privilege($1,'privacy_tombstone_prepare_closure_v3(uuid,uuid)','EXECUTE'),
		has_function_privilege($1,'privacy_tombstone_confirm_closure_v3(uuid,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz)','EXECUTE'),
		has_function_privilege($1,'privacy_tombstone_prepare_closure_v4(uuid,uuid)','EXECUTE'),
		has_function_privilege($1,'privacy_tombstone_confirm_closure_v4(uuid,uuid,text,text,text,bytea,text,bytea,bigint,timestamptz,timestamptz)','EXECUTE'),
		has_function_privilege($1,'privacy_membership_history_lock_source(uuid)','EXECUTE'),
		has_function_privilege($1,'privacy_completion_membership_postcondition_ready(uuid)','EXECUTE')`, executorRole).Scan(&canExecute, &canMutate, &canBypass, &canCallInner, &canCallPostconditionInner, &canCleanup, &canUpload, &canListObjects, &canRecordObjectEvidence, &canCompleteObjectCheckpoint, &canCaptureObjects, &canListProviders, &canRecordProviderEvidence, &canCompleteProviderCheckpoint, &canCaptureProviders, &canPrepareClosureV2, &canConfirmClosureV2, &canPrepareClosureV3, &canConfirmClosureV3, &canPrepareClosureV4, &canConfirmClosureV4, &canCallMembershipLock, &canCheckCompletionPostcondition); err != nil {
		t.Fatal(err)
	}
	if !canExecute || !canMutate || canBypass || canCallInner || canCallPostconditionInner || !canCleanup || canUpload || !canListObjects || !canRecordObjectEvidence || !canCompleteObjectCheckpoint || canCaptureObjects || !canListProviders || !canRecordProviderEvidence || !canCompleteProviderCheckpoint || canCaptureProviders || canPrepareClosureV2 || canConfirmClosureV2 || canPrepareClosureV3 || canConfirmClosureV3 || !canPrepareClosureV4 || !canConfirmClosureV4 || canCallMembershipLock || canCheckCompletionPostcondition {
		t.Fatalf("worker function boundary claim=%v mutate=%v legacy_bypass=%v release_guard_inner=%v postcondition_inner=%v upload_cleanup=%v upload_lifecycle=%v object_list=%v object_evidence=%v object_complete=%v object_capture=%v provider_list=%v provider_evidence=%v provider_complete=%v provider_capture=%v closure_v2_prepare=%v closure_v2_confirm=%v closure_v3_prepare=%v closure_v3_confirm=%v closure_v4_prepare=%v closure_v4_confirm=%v membership_lock=%v completion_postcondition=%v",
			canExecute, canMutate, canBypass, canCallInner, canCallPostconditionInner, canCleanup, canUpload, canListObjects, canRecordObjectEvidence, canCompleteObjectCheckpoint, canCaptureObjects,
			canListProviders, canRecordProviderEvidence, canCompleteProviderCheckpoint, canCaptureProviders, canPrepareClosureV2, canConfirmClosureV2, canPrepareClosureV3, canConfirmClosureV3, canPrepareClosureV4, canConfirmClosureV4, canCallMembershipLock, canCheckCompletionPostcondition)
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
	var appCanIssueInvitation, appCanListInvitations, appCanRevokeInvitation, appCanReserveGuardianRate, appCanAuditReview, appCanReviewDecision bool
	var appCanSubmitRenewal, appCanListRenewalReminders, appCanEnqueueRenewalReminder bool
	if err = tx.QueryRow(ctx, `SELECT
		has_function_privilege($1,'guardian_authority_issue_invitation(uuid,citext,bytea)','EXECUTE'),
		has_function_privilege($1,'guardian_authority_list_invitations(uuid,integer)','EXECUTE'),
		has_function_privilege($1,'guardian_authority_revoke_invitation(uuid,uuid)','EXECUTE'),
		has_function_privilege($1,'guardian_application_reserve(bytea,bytea,text)','EXECUTE'),
		has_function_privilege($1,'guardian_authority_record_review_view(uuid,uuid)','EXECUTE'),
		has_function_privilege($1,'guardian_authority_admin_transition(uuid,uuid,bigint,text,text,text)','EXECUTE'),
		has_function_privilege($1,'guardian_authority_submit_renewal(uuid,uuid,bigint,text)','EXECUTE'),
		has_function_privilege($1,'guardian_authority_due_renewal_reminders(integer)','EXECUTE'),
		has_function_privilege($1,'guardian_authority_enqueue_renewal_reminder(uuid,uuid,citext,timestamptz,timestamptz,text,bytea)','EXECUTE')`, appRole).
		Scan(&appCanIssueInvitation, &appCanListInvitations, &appCanRevokeInvitation, &appCanReserveGuardianRate, &appCanAuditReview, &appCanReviewDecision,
			&appCanSubmitRenewal, &appCanListRenewalReminders, &appCanEnqueueRenewalReminder); err != nil {
		t.Fatal(err)
	}
	if !appCanIssueInvitation || !appCanListInvitations || !appCanRevokeInvitation || !appCanReserveGuardianRate || !appCanAuditReview || !appCanReviewDecision || !appCanSubmitRenewal || !appCanListRenewalReminders || !appCanEnqueueRenewalReminder {
		t.Fatalf("web guardian routine boundary issue=%t list=%t revoke=%t reserve=%t audit_review=%t decide=%t submit_renewal=%t list_reminders=%t enqueue_reminder=%t",
			appCanIssueInvitation, appCanListInvitations, appCanRevokeInvitation, appCanReserveGuardianRate, appCanAuditReview, appCanReviewDecision, appCanSubmitRenewal, appCanListRenewalReminders, appCanEnqueueRenewalReminder)
	}
	if _, err = tx.Exec(ctx, `SET LOCAL ROLE `+quoteIdentifier(appRole)); err != nil {
		t.Fatal(err)
	}
	digest := make([]byte, 32)
	for index := range digest {
		digest[index] = byte(index + 1)
	}
	var invitationRef uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT public_ref FROM guardian_authority_issue_invitation($1::uuid,$2::citext,$3::bytea)`, guardianAdminID, "invited-"+suffix+"@example.test", digest).Scan(&invitationRef); err != nil {
		t.Fatalf("web role could not issue invitation through intended routine: %v", err)
	}
	var invitationCount int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM guardian_authority_list_invitations($1::uuid,10)`, guardianAdminID).Scan(&invitationCount); err != nil || invitationCount != 1 {
		t.Fatalf("web role could not project invitations through intended routine: count=%d err=%v", invitationCount, err)
	}
	var revoked int64
	if err = tx.QueryRow(ctx, `SELECT guardian_authority_revoke_invitation($1::uuid,$2::uuid)`, guardianAdminID, invitationRef).Scan(&revoked); err != nil || revoked != 1 {
		t.Fatalf("web role could not revoke invitation through intended routine: changed=%d err=%v", revoked, err)
	}
	networkDigest := make([]byte, 32)
	copy(networkDigest, digest)
	networkDigest[0] = 255
	if _, err = tx.Exec(ctx, `SELECT guardian_application_reserve($1::bytea,$2::bytea,'SUBMISSION')`, digest, networkDigest); err != nil {
		t.Fatalf("web role could not reserve guardian rate through intended routine: %v", err)
	}
	if _, err = tx.Exec(ctx, `SELECT guardian_authority_record_review_view($1::uuid,NULL::uuid)`, guardianAdminID); err != nil {
		t.Fatalf("web role could not append review access through intended routine: %v", err)
	}
	if _, err = tx.Exec(ctx, `RESET ROLE`); err != nil {
		t.Fatal(err)
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
	if _, err = tx.Exec(ctx, `SAVEPOINT disabled_claim`); err != nil {
		t.Fatal(err)
	}
	var claimed int
	err = tx.QueryRow(ctx, `SELECT count(*) FROM privacy_worker_claim(1000,$1)`, uuid.New()).Scan(&claimed)
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "42501" || postgresError.Message != "privacy_worker_disabled" {
		t.Fatalf("restricted worker bypassed disabled claim: %v", err)
	}
	if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT disabled_claim`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `RESET ROLE`); err != nil {
		t.Fatal(err)
	}
}
