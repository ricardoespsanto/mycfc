//go:build integration

package db

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestPrivacyActivationBrokerRejectsUnboundEnvelopeAndPersistsExactCanonicalBytes(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	adminConn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer adminConn.Close(ctx)
	t.Cleanup(func() {
		cleanup, cleanupErr := pgx.Connect(context.Background(), dsn)
		if cleanupErr == nil {
			_, _ = cleanup.Exec(context.Background(), `DELETE FROM privacy_request_activation WHERE singleton;
				UPDATE privacy_worker_kill_switch SET engaged=true,activation_approval_id=NULL WHERE singleton`)
			_ = cleanup.Close(context.Background())
		}
	})
	const brokerPassword = "integration-only-activation-broker"
	if _, err = adminConn.Exec(ctx, `DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='mycfc_privacy_activation_broker') THEN CREATE ROLE mycfc_privacy_activation_broker LOGIN NOINHERIT NOBYPASSRLS; END IF; END $$;
		ALTER ROLE mycfc_privacy_activation_broker WITH LOGIN NOINHERIT NOBYPASSRLS PASSWORD '`+brokerPassword+`';
		GRANT CONNECT ON DATABASE `+quoteIdentifier(adminConn.Config().Database)+` TO mycfc_privacy_activation_broker;
		GRANT USAGE ON SCHEMA public TO mycfc_privacy_activation_broker;
		GRANT EXECUTE ON FUNCTION privacy_activation_broker_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb),privacy_activation_broker_material(text),privacy_activation_broker_activate(uuid,text,uuid[],bytea,bytea,uuid,uuid,text,text,bytea,bytea,bytea,bytea,jsonb,jsonb,timestamptz,timestamptz,timestamptz,timestamptz) TO mycfc_privacy_activation_broker`); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	adminID, executorID := uuid.New(), uuid.New()
	for _, fixture := range []struct {
		id    uuid.UUID
		email string
	}{{adminID, "broker-admin-" + uuid.NewString() + "@example.test"}, {executorID, "broker-executor-" + uuid.NewString() + "@example.test"}} {
		if _, err = adminConn.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Broker fixture',$2,'hash','1990-01-01')`, fixture.id, fixture.email); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = adminConn.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, adminID); err != nil {
		t.Fatal(err)
	}
	if _, err = adminConn.Exec(ctx, `INSERT INTO privacy_executor_grants(user_id,granted_by,granted_at) VALUES($1,$2,$3)`, executorID, adminID, now); err != nil {
		t.Fatal(err)
	}
	policy := "broker-policy-" + uuid.NewString()
	if _, err = adminConn.Exec(ctx, `INSERT INTO privacy_request_policies(version,category_catalogue,executor_version,plan_schema_version,working_retention_days,adopted_at,adopted_by)
		VALUES($1,'[]','privacy-erasure-executor/v2','privacy-erasure-plan/v2',90,$2,$3)`, policy, now, adminID); err != nil {
		t.Fatal(err)
	}
	digest := bytes.Repeat([]byte{7}, sha256.Size)
	image := "sha256:" + strings.Repeat("7", 64)
	for _, kind := range []string{"RESTORE", "INFRASTRUCTURE", "PROVIDER", "SCHEMA"} {
		artifact := brokerArtifactFixture(kind, policy, image, digest)
		payload, _ := json.Marshal(artifact)
		var evidenceID uuid.UUID
		contract := map[string]string{"RESTORE": "mycfc/privacy-restore-drill-attestation/v2", "INFRASTRUCTURE": "mycfc/privacy-infrastructure-posture/v1", "PROVIDER": "mycfc/privacy-provider-registry/v1", "SCHEMA": "mycfc/schema-migration-inventory/v1"}[kind]
		kindDigest := sha256.Sum256([]byte(kind + uuid.NewString()))
		if err = adminConn.QueryRow(ctx, `SELECT privacy_activation_record_authenticated_evidence($1,$2,$3,$4,$5,$6,$7)`, adminID, kind, kindDigest[:], contract, now, now.Add(90*24*time.Hour), payload).Scan(&evidenceID); err != nil {
			t.Fatal(err)
		}
	}

	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.User, config.Password = privacyActivationBrokerRole, brokerPassword
	brokerConn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer brokerConn.Close(ctx)
	var evidenceIDs []uuid.UUID
	var evidenceDigest, activationDigest, schemaDigest []byte
	var executorVersion, planVersion, materialImage string
	if err = brokerConn.QueryRow(ctx, `SELECT evidence_ids,evidence_set_sha256,activation_sha256,executor_version,plan_schema_version,image_digest,schema_migration_digest FROM privacy_activation_broker_material($1)`, policy).
		Scan(&evidenceIDs, &evidenceDigest, &activationDigest, &executorVersion, &planVersion, &materialImage, &schemaDigest); err != nil {
		t.Fatal(err)
	}
	proposalID := uuid.New()
	executorNonceSum := sha256.Sum256([]byte(uuid.NewString()))
	administratorNonceSum := sha256.Sum256([]byte(uuid.NewString()))
	executorNonce, administratorNonce := executorNonceSum[:], administratorNonceSum[:]
	executorEnvelope := brokerApprovalFixture(proposalID, policy, evidenceIDs, evidenceDigest, activationDigest, executorVersion, planVersion, materialImage, schemaDigest, executorID, "EXECUTOR", "executor-key", executorNonce, now)
	administratorEnvelope := brokerApprovalFixture(proposalID, policy, evidenceIDs, evidenceDigest, activationDigest, executorVersion, planVersion, materialImage, schemaDigest, adminID, "ADMINISTRATOR", "administrator-key", administratorNonce, now)
	executorRaw, _ := json.Marshal(executorEnvelope)
	administratorRaw, _ := json.Marshal(administratorEnvelope)
	executorNonceDigest, administratorNonceDigest := sha256.Sum256(executorNonce), sha256.Sum256(administratorNonce)
	call := func(executorJSON []byte, executorIssued time.Time) error {
		var approval uuid.UUID
		return brokerConn.QueryRow(ctx, `SELECT privacy_activation_broker_activate($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14::jsonb,$15::jsonb,$16,$17,$18,$19)`,
			proposalID, policy, evidenceIDs, evidenceDigest, activationDigest, executorID, adminID, "executor-key", "administrator-key",
			executorNonceDigest[:], administratorNonceDigest[:], executorJSON, administratorRaw, string(executorJSON), string(administratorRaw),
			executorIssued, now.Add(15*time.Minute), now, now.Add(15*time.Minute)).Scan(&approval)
	}
	if err = call(executorRaw, now.Add(time.Second)); err == nil || !strings.Contains(err.Error(), "privacy_activation_signed_approval_rejected") {
		t.Fatalf("mismatched issued_at accepted: %v", err)
	}
	var extra map[string]any
	_ = json.Unmarshal(executorRaw, &extra)
	extra["unexpected"] = true
	extraRaw, _ := json.Marshal(extra)
	if err = call(extraRaw, now); err == nil || !strings.Contains(err.Error(), "privacy_activation_signed_approval_rejected") {
		t.Fatalf("extra envelope field accepted: %v", err)
	}
	if err = call(executorRaw, now); err != nil {
		t.Fatal(err)
	}
	var stored []byte
	if err = adminConn.QueryRow(ctx, `SELECT raw_envelope FROM privacy_protected.activation_signed_approvals WHERE proposal_id=$1 AND signer_role='EXECUTOR'`, proposalID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, executorRaw) {
		t.Fatal("broker did not persist the exact canonical bytes supplied by the verifier")
	}
}

func brokerArtifactFixture(kind, policy, image string, digest []byte) map[string]any {
	artifact := map[string]any{"policy_version": policy, "executor_version": "privacy-erasure-executor/v2", "plan_schema_version": "privacy-erasure-plan/v2", "image_digest": image, "evidence_sha256": digest}
	switch kind {
	case "RESTORE":
		artifact["evidence_ref"], artifact["schema_migration_digest"] = "s3://fixture/restore?versionId=v1", digest
		artifact["restore_input_source"], artifact["restore_input_contract"] = "LIVE_LEDGER", "mycfc/privacy-restore-ledger-input/v2"
		artifact["restore_replay_contract"], artifact["restore_closure_contract"] = "relational-erasure-replay/v1", "restore-tombstone-closure/v3"
		artifact["restore_candidate_sha256"], artifact["restore_inventory_sha256"], artifact["restore_observer_sha256"] = digest, digest, digest
		artifact["restore_object_count"], artifact["restore_replayed_count"], artifact["restore_synthetic_count"] = 1, 1, 0
	case "INFRASTRUCTURE":
		artifact["evidence_ref"], artifact["signing_key_id"] = "s3://fixture/infrastructure?versionId=v1", "fixture-key"
		artifact["production_state_serial"], artifact["hetzner_state_serial"] = 1, 1
		for _, field := range []string{"production_state_sha256", "hetzner_state_sha256", "production_plan_sha256", "hetzner_plan_sha256"} {
			artifact[field] = digest
		}
		for _, field := range []string{"worker_identity_enabled", "s3_version_deletion_enabled", "ledger_broker_invoke_enabled", "worker_monitoring_enabled", "restore_infrastructure_enabled", "restore_ledger_write_enabled"} {
			artifact[field] = true
		}
	case "PROVIDER":
		artifact["evidence_ref"], artifact["signing_key_id"] = "s3://fixture/provider?versionId=v1", "fixture-key"
		artifact["provider_registry_state"], artifact["provider_registration_count"], artifact["provider_registry_sha256"] = "READY", 1, digest
	case "SCHEMA":
		artifact["evidence_ref"], artifact["signing_key_id"] = "s3://fixture/schema?versionId=v1", "fixture-key"
		artifact["schema_migration_digest"], artifact["baseline_includes_through"] = digest, "202609100014_privacy_activation_broker"
	}
	return artifact
}

func brokerApprovalFixture(proposal uuid.UUID, policy string, evidenceIDs []uuid.UUID, evidenceDigest, activationDigest []byte, executorVersion, planVersion, image string, schemaDigest []byte, actor uuid.UUID, role, key string, nonce []byte, issued time.Time) map[string]any {
	return map[string]any{"contract": "mycfc/privacy-activation-approval/v1", "proposal_id": proposal, "activation_sha256": hex.EncodeToString(activationDigest),
		"evidence_set_sha256": hex.EncodeToString(evidenceDigest), "evidence_ids": evidenceIDs, "policy_version": policy, "executor_version": executorVersion,
		"plan_schema_version": planVersion, "image_digest": image, "schema_migration_digest": hex.EncodeToString(schemaDigest), "signing_key_id": key,
		"actor_ref": actor, "role": role, "issued_at": issued, "expires_at": issued.Add(15 * time.Minute), "nonce": base64.StdEncoding.EncodeToString(nonce),
		"signature_ed25519": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 64))}
}
