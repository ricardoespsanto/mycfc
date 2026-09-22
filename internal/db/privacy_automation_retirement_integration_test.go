//go:build integration

package db

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestPrivacyAutomationRetirementIsPermanentAndPreservesEvidence(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	const appRole = "mycfc_retirement_web_test"
	const migrationRole = "mycfc_retirement_migrate_test"
	const parentRole = "mycfc_retirement_parent_test"
	if _, err = tx.Exec(ctx, `DO $$BEGIN
		IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='mycfc_retirement_web_test') THEN CREATE ROLE mycfc_retirement_web_test NOLOGIN; END IF;
		IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='mycfc_retirement_migrate_test') THEN CREATE ROLE mycfc_retirement_migrate_test NOLOGIN; END IF;
		IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='mycfc_retirement_parent_test') THEN CREATE ROLE mycfc_retirement_parent_test NOLOGIN; END IF;
		IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='mycfc_privacy_executor') THEN CREATE ROLE mycfc_privacy_executor LOGIN; ELSE ALTER ROLE mycfc_privacy_executor LOGIN; END IF;
	END$$;
	GRANT mycfc_privacy_executor TO mycfc_retirement_web_test;
	GRANT mycfc_retirement_parent_test TO mycfc_privacy_executor;
	GRANT SELECT ON data_erasure_requests TO mycfc_retirement_web_test;
	GRANT EXECUTE ON FUNCTION privacy_worker_status() TO mycfc_retirement_web_test;`); err != nil {
		t.Fatal(err)
	}
	var requestID string
	if err = tx.QueryRow(ctx, `INSERT INTO data_erasure_requests(
		idempotency_key,subject_kind,scope_kind,status,received_at,due_at,closed_at,evidence_expires_at,working_expires_at,working_erased_at,updated_at)
		VALUES(gen_random_uuid(),'SELF','ACCOUNT_CLOSURE','CANCELLED',clock_timestamp(),clock_timestamp()+interval '30 days',clock_timestamp(),clock_timestamp()+interval '1 year',clock_timestamp(),clock_timestamp(),clock_timestamp()) RETURNING id::text`).Scan(&requestID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO email_outbox(message_type,sealed_payload,privacy_request_id,privacy_event_key,status,claimed_at)
		VALUES('PRIVACY_DECISION','payload',$1,gen_random_uuid(),'SENDING',clock_timestamp())`, requestID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, retireLegacyPrivacyRolesSQL); err != nil {
		t.Fatal(err)
	}
	migration, err := migrationFiles.ReadFile("migrations/202609220001_privacy_automation_retirement.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	var databaseName string
	if err = tx.QueryRow(ctx, `SELECT current_database()`).Scan(&databaseName); err != nil {
		t.Fatal(err)
	}
	if err = HardenPrivacyExecutionRoles(ctx, tx, databaseName, RoleCredentials{AppUsername: appRole, MigrationUsername: migrationRole}); err != nil {
		t.Fatal(err)
	}

	var enabled, fulfilmentReady, killSwitchEngaged bool
	if err = tx.QueryRow(ctx, `SELECT
		COALESCE((SELECT enabled FROM privacy_request_activation WHERE singleton),false),
		COALESCE((SELECT fulfilment_ready FROM privacy_request_activation WHERE singleton),false),
		COALESCE((SELECT engaged FROM privacy_worker_kill_switch WHERE singleton),true)`).Scan(&enabled, &fulfilmentReady, &killSwitchEngaged); err != nil {
		t.Fatal(err)
	}
	if enabled || fulfilmentReady || !killSwitchEngaged {
		t.Fatalf("terminal state enabled=%t ready=%t kill_switch=%t", enabled, fulfilmentReady, killSwitchEngaged)
	}
	var queuedStatus string
	if err = tx.QueryRow(ctx, `SELECT status FROM email_outbox WHERE privacy_request_id=$1`, requestID).Scan(&queuedStatus); err != nil {
		t.Fatal(err)
	}
	if queuedStatus != "CANCELLED" {
		t.Fatalf("queued privacy notification status=%q", queuedStatus)
	}

	var activationFence bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='privacy_request_activation'::regclass AND tgname='privacy_automation_activation_retired' AND tgenabled <> 'D')`).Scan(&activationFence); err != nil {
		t.Fatal(err)
	}
	if !activationFence {
		t.Fatal("activation retirement trigger is missing")
	}

	if _, err = tx.Exec(ctx, "SAVEPOINT retired_switch_check"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE privacy_worker_kill_switch SET engaged=false WHERE singleton`); err == nil || !strings.Contains(err.Error(), "privacy_automation_retired") {
		t.Fatalf("kill-switch retirement fence error=%v", err)
	}
	if _, err = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT retired_switch_check"); err != nil {
		t.Fatal(err)
	}

	var mediaRole, retentionRole bool
	if err = tx.QueryRow(ctx, `SELECT
		pg_has_role('mycfc_media_cleanup','mycfc_media_cleanup','MEMBER'),
		pg_has_role('mycfc_data_retention','mycfc_data_retention','MEMBER')`).Scan(&mediaRole, &retentionRole); err != nil {
		t.Fatal(err)
	}
	if !mediaRole || !retentionRole {
		t.Fatal("narrow maintenance roles are missing")
	}
	var legacyCanLogin, inboundMembership, outboundMembership, webTableAccess, webRoutineAccess bool
	if err = tx.QueryRow(ctx, `SELECT
		(SELECT rolcanlogin FROM pg_roles WHERE rolname='mycfc_privacy_executor'),
		pg_has_role($1,'mycfc_privacy_executor','MEMBER'),
		pg_has_role('mycfc_privacy_executor',$2,'MEMBER'),
		has_table_privilege($1,'data_erasure_requests','SELECT'),
		has_function_privilege($1,'privacy_worker_status()','EXECUTE')`, appRole, parentRole).Scan(
		&legacyCanLogin, &inboundMembership, &outboundMembership, &webTableAccess, &webRoutineAccess,
	); err != nil {
		t.Fatal(err)
	}
	if legacyCanLogin || inboundMembership || outboundMembership || webTableAccess || webRoutineAccess {
		t.Fatalf("retirement privileges remain login=%t inbound=%t outbound=%t table=%t routine=%t",
			legacyCanLogin, inboundMembership, outboundMembership, webTableAccess, webRoutineAccess)
	}
	for role, signature := range map[string]string{
		"mycfc_media_cleanup":  "media_upload_cleanup_claim(bigint,uuid)",
		"mycfc_data_retention": "data_retention_status()",
	} {
		var allowed bool
		if err = tx.QueryRow(ctx, `SELECT has_function_privilege($1,$2,'EXECUTE')`, role, signature).Scan(&allowed); err != nil {
			t.Fatal(err)
		}
		if !allowed {
			t.Fatalf("%s lacks %s", role, signature)
		}
	}

	var historicalTables int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM pg_class relation JOIN pg_namespace namespace ON namespace.oid=relation.relnamespace
		WHERE namespace.nspname='public' AND relation.relname IN ('data_erasure_requests','privacy_erasure_executions','privacy_request_activation_events')`).Scan(&historicalTables); err != nil {
		t.Fatal(err)
	}
	if historicalTables != 3 {
		t.Fatalf("historical privacy tables preserved=%d", historicalTables)
	}
}
