//go:build integration

package db

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestPrivacyEmptyProviderRegistryForwardMigrationPreservesHistoryAndFailsClosed(t *testing.T) {
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

	migration, err := migrationFiles.ReadFile("migrations/202609110002_privacy_empty_provider_registry_activation.sql")
	if err != nil {
		t.Fatal(err)
	}
	const marker = "-- #248 distinguishes a complete zero-registration provider inventory"
	baselineIndex := strings.LastIndex(baselineSchema, marker)
	nextIndex := strings.LastIndex(baselineSchema, "-- Baseline through 202609110003_privacy_activation_emergency_fence.")
	if baselineIndex < 0 || nextIndex <= baselineIndex || strings.TrimSpace(baselineSchema[baselineIndex:nextIndex]) != strings.TrimSpace(string(migration)) {
		t.Fatal("empty provider registry migration is not the exact baseline segment")
	}

	// Restore the exact record routine and constraint from the immediately
	// preceding migration, then reapply this migration over live-shaped data.
	predecessor, err := migrationFiles.ReadFile("migrations/202609100015_privacy_membership_postcondition.sql")
	if err != nil {
		t.Fatal(err)
	}
	recordPredecessor := migrationFunctionSegment(t, string(predecessor),
		"CREATE OR REPLACE FUNCTION privacy_activation_record_authenticated_evidence(",
		"-- Completion may proceed only from the current authenticated closure")
	recordPredecessor = strings.ReplaceAll(recordPredecessor,
		"202609100015_privacy_membership_postcondition", "202609110001_privacy_upload_finalize_execution_fence")
	if _, err = tx.Exec(ctx, recordPredecessor); err != nil {
		t.Fatal(err)
	}
	digestStart := strings.Index(string(predecessor), "CREATE OR REPLACE FUNCTION privacy_activation_authenticated_set_digest(")
	if digestStart < 0 {
		t.Fatal("activation digest predecessor not found")
	}
	digestPredecessor := strings.TrimSpace(string(predecessor)[digestStart:])
	digestPredecessor = strings.ReplaceAll(digestPredecessor,
		"202609100015_privacy_membership_postcondition", "202609110001_privacy_upload_finalize_execution_fence")
	if _, err = tx.Exec(ctx, digestPredecessor); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `ALTER TABLE privacy_activation_authenticated_artifacts DISABLE TRIGGER privacy_activation_authenticated_artifacts_immutable;
		DELETE FROM privacy_activation_authenticated_artifacts;
		ALTER TABLE privacy_activation_authenticated_artifacts ENABLE TRIGGER privacy_activation_authenticated_artifacts_immutable`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `ALTER TABLE privacy_activation_authenticated_artifacts DROP CONSTRAINT privacy_activation_authenticated_artifacts_v8_check`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `ALTER TABLE privacy_activation_authenticated_artifacts DROP COLUMN provider_inventory_contract`); err != nil {
		t.Fatal(err)
	}
	previous, err := migrationFiles.ReadFile("migrations/202609110001_privacy_upload_finalize_execution_fence.sql")
	if err != nil {
		t.Fatal(err)
	}
	constraintPredecessor := migrationFunctionSegment(t, string(previous),
		"ALTER TABLE privacy_activation_authenticated_artifacts ADD CONSTRAINT privacy_activation_authenticated_artifacts_v4_check",
		"DO $$DECLARE definition text;old_clause text;new_clause text;")
	if _, err = tx.Exec(ctx, constraintPredecessor); err != nil {
		t.Fatal(err)
	}

	adminID := uuid.New()
	now := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	policy := "empty-provider-migration-" + uuid.NewString()
	if _, err = tx.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Provider migration fixture',$2,'hash','1990-01-01')`,
		adminID, "provider-migration-"+uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, adminID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO privacy_request_policies(version,category_catalogue,executor_version,plan_schema_version,working_retention_days,adopted_at,adopted_by)
		VALUES($1,'[]','privacy-erasure-executor/v2','privacy-erasure-plan/v2',90,$2,$3)`, policy, now, adminID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO privacy_request_activation(singleton,policy_version,enabled,fulfilment_ready,updated_by)
		VALUES(true,$1,false,false,$2) ON CONFLICT(singleton) DO UPDATE SET policy_version=EXCLUDED.policy_version,
		enabled=false,fulfilment_ready=false,approval_id=NULL,updated_by=EXCLUDED.updated_by`, policy, adminID); err != nil {
		t.Fatal(err)
	}

	evidenceValue := bytes.Repeat([]byte{0x42}, sha256.Size)
	legacyArtifact := providerMigrationArtifact(policy, evidenceValue, 1, false)
	legacyID := recordProviderMigrationEvidence(t, ctx, tx, adminID, "mycfc/privacy-provider-registry/v1", now, legacyArtifact)
	var switchVersion int64
	if err = tx.QueryRow(ctx, `UPDATE privacy_worker_kill_switch SET engaged=false,version=version+1,activation_approval_id=NULL,changed_at=clock_timestamp()
		WHERE singleton RETURNING version`).Scan(&switchVersion); err != nil {
		t.Fatal(err)
	}

	if _, err = tx.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	var legacyContract *string
	if err = tx.QueryRow(ctx, `SELECT provider_inventory_contract FROM privacy_activation_authenticated_artifacts WHERE evidence_id=$1`, legacyID).Scan(&legacyContract); err != nil {
		t.Fatal(err)
	}
	if legacyContract != nil {
		t.Fatalf("historical v1 evidence was rewritten: %q", *legacyContract)
	}

	readyArtifact := providerMigrationArtifact(policy, evidenceValue, 0, true)
	if id := recordProviderMigrationEvidence(t, ctx, tx, adminID, "mycfc/privacy-provider-registry/v2", now.Add(time.Minute), readyArtifact); id == uuid.Nil {
		t.Fatal("complete zero-registration provider evidence was not recorded")
	}
	assertProviderMigrationEvidenceRejected(t, ctx, tx, adminID, "mycfc/privacy-provider-registry/v2", now.Add(2*time.Minute),
		providerMigrationArtifact(policy, evidenceValue, 1, true))
	assertProviderMigrationEvidenceRejected(t, ctx, tx, adminID, "mycfc/privacy-provider-registry/v2", now.Add(3*time.Minute),
		providerMigrationArtifact(policy, evidenceValue, 0, false))
	assertProviderMigrationEvidenceRejected(t, ctx, tx, adminID, "mycfc/privacy-provider-registry/v1", now.Add(4*time.Minute), legacyArtifact)

	var engaged, activationEnabled bool
	var currentVersion int64
	if err = tx.QueryRow(ctx, `SELECT engaged,version FROM privacy_worker_kill_switch WHERE singleton`).Scan(&engaged, &currentVersion); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT enabled FROM privacy_request_activation WHERE singleton`).Scan(&activationEnabled); err != nil {
		t.Fatal(err)
	}
	if !engaged || activationEnabled || currentVersion != switchVersion+1 {
		t.Fatalf("migration did not invalidate activation: engaged=%t enabled=%t version=%d predecessor=%d", engaged, activationEnabled, currentVersion, switchVersion)
	}
}

func providerMigrationArtifact(policy string, digest []byte, count int64, complete bool) map[string]any {
	artifact := map[string]any{
		"policy_version": policy, "executor_version": "privacy-erasure-executor/v2", "plan_schema_version": "privacy-erasure-plan/v2",
		"image_digest": "sha256:" + strings.Repeat("4", 64), "evidence_ref": "s3://fixture/provider?versionId=v2",
		"evidence_sha256": digest, "signing_key_id": "fixture-key", "provider_registry_state": "READY",
		"provider_registration_count": count, "provider_registry_sha256": digest,
	}
	if complete {
		artifact["provider_inventory_contract"] = "mycfc/privacy-provider-registry-source/v2"
	}
	return artifact
}

func recordProviderMigrationEvidence(t *testing.T, ctx context.Context, tx pgx.Tx, actor uuid.UUID, contract string, observedAt time.Time, artifact map[string]any) uuid.UUID {
	t.Helper()
	payload, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(append(payload, byte(observedAt.Nanosecond())))
	var evidenceID uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT privacy_activation_record_authenticated_evidence($1,'PROVIDER',$2,$3,$4,$5,$6)`,
		actor, digest[:], contract, observedAt, observedAt.Add(90*24*time.Hour), payload).Scan(&evidenceID); err != nil {
		t.Fatal(err)
	}
	return evidenceID
}

func assertProviderMigrationEvidenceRejected(t *testing.T, ctx context.Context, tx pgx.Tx, actor uuid.UUID, contract string, observedAt time.Time, artifact map[string]any) {
	t.Helper()
	if _, err := tx.Exec(ctx, `SAVEPOINT reject_provider_evidence`); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(append(payload, byte(observedAt.Nanosecond())))
	var ignored uuid.UUID
	err = tx.QueryRow(ctx, `SELECT privacy_activation_record_authenticated_evidence($1,'PROVIDER',$2,$3,$4,$5,$6)`,
		actor, digest[:], contract, observedAt, observedAt.Add(90*24*time.Hour), payload).Scan(&ignored)
	if rollbackErr := func() error {
		_, rollbackErr := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT reject_provider_evidence`)
		return rollbackErr
	}(); rollbackErr != nil {
		t.Fatal(rollbackErr)
	}
	if err == nil {
		t.Fatal("unsupported provider evidence was accepted")
	}
}
