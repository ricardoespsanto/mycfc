//go:build integration

package db

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func activatePrivacyDatabaseIntegrationFixture(t *testing.T, ctx context.Context, tx pgx.Tx, adminID, executorID uuid.UUID) {
	t.Helper()
	now := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Microsecond)
	policy := "db-activation-" + uuid.NewString()
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN' ON CONFLICT DO NOTHING`, []any{adminID}},
		{`INSERT INTO privacy_executor_grants(user_id,granted_by,granted_at) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, []any{executorID, adminID, now}},
		{`INSERT INTO privacy_request_policies(version,category_catalogue,executor_version,plan_schema_version,working_retention_days,adopted_at,adopted_by)
		 VALUES($1,'[]','privacy-erasure-executor/v2','privacy-erasure-plan/v2',90,$2,$3)`, []any{policy, now, adminID}},
	} {
		if _, err := tx.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	value := bytes.Repeat([]byte{7}, sha256.Size)
	evidenceIDs := make([]uuid.UUID, 0, 4)
	for _, kind := range []string{"RESTORE", "INFRASTRUCTURE", "PROVIDER", "SCHEMA"} {
		artifact := map[string]any{
			"policy_version": policy, "executor_version": "privacy-erasure-executor/v2", "plan_schema_version": "privacy-erasure-plan/v2",
			"image_digest": "sha256:" + strings.Repeat("7", 64), "evidence_sha256": value,
		}
		contract := ""
		switch kind {
		case "RESTORE":
			contract = "mycfc/privacy-restore-drill-attestation/v2"
			artifact["evidence_ref"], artifact["schema_migration_digest"] = "s3://fixture/restore?versionId=v1", value
			artifact["restore_input_source"], artifact["restore_input_contract"] = "LIVE_LEDGER", "mycfc/privacy-restore-ledger-input/v2"
			artifact["restore_replay_contract"], artifact["restore_closure_contract"] = "relational-erasure-replay/v1", "restore-tombstone-closure/v4"
			artifact["restore_candidate_sha256"], artifact["restore_inventory_sha256"] = value, value
			artifact["restore_object_count"], artifact["restore_replayed_count"], artifact["restore_synthetic_count"] = 1, 1, 0
			artifact["restore_observer_sha256"] = value
			artifact["restore_membership_postcondition_contract"], artifact["restore_membership_postcondition_sha256"] = "mycfc/membership-history-postcondition/v1", value
			artifact["restore_membership_postcondition_verified_count"], artifact["restore_membership_count"], artifact["restore_variation_count"] = 1, 0, 0
		case "INFRASTRUCTURE":
			contract = "mycfc/privacy-infrastructure-posture/v1"
			artifact["evidence_ref"], artifact["signing_key_id"] = "s3://fixture/infrastructure?versionId=v1", "fixture-key"
			artifact["production_state_serial"], artifact["hetzner_state_serial"] = 1, 1
			for _, field := range []string{"production_state_sha256", "hetzner_state_sha256", "production_plan_sha256", "hetzner_plan_sha256"} {
				artifact[field] = value
			}
			for _, field := range []string{"worker_identity_enabled", "s3_version_deletion_enabled", "ledger_broker_invoke_enabled", "worker_monitoring_enabled", "restore_infrastructure_enabled", "restore_ledger_write_enabled"} {
				artifact[field] = true
			}
		case "PROVIDER":
			contract = "mycfc/privacy-provider-registry/v2"
			artifact["evidence_ref"], artifact["signing_key_id"] = "s3://fixture/provider?versionId=v1", "fixture-key"
			artifact["provider_registry_state"], artifact["provider_registration_count"], artifact["provider_registry_sha256"] = "READY", 0, value
			artifact["provider_inventory_contract"] = "mycfc/privacy-provider-registry-source/v2"
		case "SCHEMA":
			contract = "mycfc/schema-migration-inventory/v1"
			artifact["evidence_ref"], artifact["signing_key_id"] = "s3://fixture/schema?versionId=v1", "fixture-key"
			artifact["schema_migration_digest"], artifact["baseline_includes_through"] = value, "202609120005_guardian_authority_activation"
		}
		encoded, err := json.Marshal(artifact)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256([]byte(kind + uuid.NewString()))
		var evidenceID uuid.UUID
		if err = tx.QueryRow(ctx, `SELECT privacy_activation_record_authenticated_evidence($1,$2,$3,$4,$5,$6,$7)`, adminID, kind, digest[:], contract, now, now.Add(90*24*time.Hour), encoded).Scan(&evidenceID); err != nil {
			t.Fatal(err)
		}
		evidenceIDs = append(evidenceIDs, evidenceID)
	}
	var proposalID uuid.UUID
	var activationDigest []byte
	if err := tx.QueryRow(ctx, `SELECT proposal_id,activation_sha256 FROM privacy_activation_propose($1,$2,$3)`, executorID, policy, evidenceIDs).Scan(&proposalID, &activationDigest); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT privacy_activation_approve($1,$2,$3)`, adminID, proposalID, activationDigest); err != nil {
		t.Fatal(err)
	}
}

func TestPrivacyWorkerDatabaseKillSwitchGuardsEveryExecutorCheckpoint(t *testing.T) {
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

	var ready, engaged bool
	if err = conn.QueryRow(ctx, `SELECT privacy_worker_activation_ready(),engaged FROM privacy_worker_kill_switch WHERE singleton`).Scan(&ready, &engaged); err != nil {
		t.Fatal(err)
	}
	if ready || !engaged {
		t.Fatalf("unsafe initial activation ready=%t kill_switch=%t", ready, engaged)
	}

	id := uuid.New()
	digest := make([]byte, 32)
	calls := []string{
		`SELECT * FROM privacy_upload_cleanup_claim(1000,$1)`,
		`SELECT privacy_upload_cleanup_complete($1,$1,1,$1,0,0,2,2,'key',$2)`,
		`SELECT privacy_upload_cleanup_fail($1,$1,1,$1,true,1000)`,
		`SELECT * FROM privacy_worker_claim(1000,$1)`,
		`SELECT privacy_worker_heartbeat($1,$1,$1,1,$1,1000)`,
		`SELECT privacy_worker_execute_checkpoint($1,$1,$1,1,$1,'IDENTITY_CLEAR','v1')`,
		`SELECT privacy_worker_complete_job($1,$1,$1,1,$1)`,
		`SELECT privacy_worker_fail_job($1,$1,$1,1,$1,'RETRYABLE',1000,'STAGE','CODE',$2)`,
		`SELECT privacy_worker_sync($1,$1,$1,1,$1)`,
		`SELECT * FROM privacy_worker_list_object_targets($1,$1,$1,1,$1)`,
		`SELECT privacy_worker_record_object_evidence($1,$1,$1,$1,1,$1,0,0,2,2,'key',$2)`,
		`SELECT privacy_worker_complete_object_checkpoint($1,$1,$1,1,$1)`,
		`SELECT * FROM privacy_tombstone_prepare_v2($1,$1,$1,1,$1)`,
		`SELECT privacy_tombstone_confirm_v2($1,$1,$1,1,$1,'restore-tombstone/v2','key','key',$2,'version',$2,1,clock_timestamp(),clock_timestamp())`,
		`SELECT * FROM privacy_tombstone_prepare_closure_v2($1,$1)`,
		`SELECT privacy_tombstone_confirm_closure_v2($1,$1,'restore-tombstone-closure/v2','key','key',$2,'version',$2,1,clock_timestamp(),clock_timestamp())`,
		`SELECT * FROM privacy_tombstone_prepare_closure_v3($1,$1)`,
		`SELECT privacy_tombstone_confirm_closure_v3($1,$1,'restore-tombstone-closure/v3','key','key',$2,'version',$2,1,clock_timestamp(),clock_timestamp())`,
		`SELECT * FROM privacy_worker_list_provider_targets($1,$1,$1,1,$1)`,
		`SELECT privacy_worker_record_provider_evidence($1,$1,$1,$1,1,$1,'REMOTE_DELETION_VERIFIED',1,'REMOTE_DELETION_RECEIPT',NULL,NULL,NULL,NULL,NULL,'key',$2)`,
		`SELECT privacy_worker_complete_provider_checkpoint($1,$1,$1,1,$1)`,
		`SELECT * FROM privacy_completion_prepare($1,$1)`,
		`SELECT * FROM privacy_completion_list_pending($1,1)`,
		`SELECT privacy_completion_finalize($1,$1,$2,$2)`,
	}
	for _, call := range calls {
		arguments := []any{id}
		if strings.Contains(call, "$2") {
			arguments = append(arguments, digest)
		}
		_, callErr := conn.Exec(ctx, call, arguments...)
		var postgresError *pgconn.PgError
		if !errors.As(callErr, &postgresError) || postgresError.Code != "42501" || postgresError.Message != "privacy_worker_disabled" {
			t.Errorf("unguarded call %q error=%v", call, callErr)
		}
	}
}

func TestLegacyActivationEvidenceCannotClearReleaseGuard(t *testing.T) {
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
	_, err = conn.Exec(ctx, `SELECT privacy_activation_record_evidence($1,'SCHEMA',$2,'mycfc/schema-migration-inventory/v1',clock_timestamp())`, uuid.New(), make([]byte, 32))
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Message != "privacy_activation_authenticated_evidence_required" {
		t.Fatalf("legacy evidence accepted: %v", err)
	}
}
