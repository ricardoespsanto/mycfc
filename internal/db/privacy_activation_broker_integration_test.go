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

	"github.com/cfcoimbra/mycfc/internal/privacyrequests"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestPrivacyActivationDualSignerBrokerRealRoleBoundary(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `UPDATE privacy_request_activation SET enabled=false,fulfilment_ready=false,approval_id=NULL WHERE singleton;
			UPDATE privacy_worker_kill_switch SET engaged=true,activation_approval_id=NULL WHERE singleton`)
	})
	brokerIdentifier := quoteIdentifier(privacyActivationBrokerRole)
	var brokerRoleExists bool
	if err = admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)`, privacyActivationBrokerRole).Scan(&brokerRoleExists); err != nil {
		t.Fatal(err)
	}
	if !brokerRoleExists {
		if _, err = admin.Exec(ctx, `CREATE ROLE `+brokerIdentifier+` NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS`); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			cleanup, cleanupErr := pgx.Connect(context.Background(), dsn)
			if cleanupErr == nil {
				_, _ = cleanup.Exec(context.Background(), `DROP OWNED BY `+brokerIdentifier)
				_, _ = cleanup.Exec(context.Background(), `DROP ROLE `+brokerIdentifier)
				_ = cleanup.Close(context.Background())
			}
		})
	}
	for _, statement := range []string{
		`GRANT CONNECT ON DATABASE ` + quoteIdentifier(admin.Config().Database) + ` TO ` + brokerIdentifier,
		`GRANT USAGE ON SCHEMA public TO ` + brokerIdentifier,
		`GRANT EXECUTE ON FUNCTION privacy_activation_broker_material(text) TO ` + brokerIdentifier,
		`GRANT EXECUTE ON FUNCTION privacy_activation_broker_register_ceremony(uuid,uuid,text,text,uuid[],bytea,bytea,text,text,text,bytea,bytea,bytea,bytea,jsonb,timestamptz,timestamptz,uuid,text,text,bytea,bigint,text,uuid,text,text,bytea,bigint,text) TO ` + brokerIdentifier,
		`GRANT EXECUTE ON FUNCTION privacy_activation_broker_activate(uuid,bytea,jsonb,bytea,jsonb,bytea,bytea) TO ` + brokerIdentifier,
	} {
		if _, err = admin.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	var oldAPIExists bool
	if err = admin.QueryRow(ctx, `SELECT to_regprocedure('privacy_activation_broker_activate(uuid,text,uuid[],bytea,bytea,uuid,uuid,text,text,bytea,bytea,bytea,bytea,jsonb,jsonb,timestamptz,timestamptz,timestamptz,timestamptz)') IS NOT NULL`).Scan(&oldAPIExists); err != nil {
		t.Fatal(err)
	}
	if oldAPIExists {
		t.Fatal("v1 approval broker remains callable")
	}

	baseNow := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	if _, err = admin.Exec(ctx, `UPDATE privacy_worker_kill_switch SET engaged=true,activation_approval_id=NULL,changed_at=$1 WHERE singleton`, baseNow.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	administratorID, executorID := uuid.New(), uuid.New()
	for _, fixture := range []struct {
		id    uuid.UUID
		email string
	}{{administratorID, "dual-admin-" + uuid.NewString() + "@example.test"}, {executorID, "dual-executor-" + uuid.NewString() + "@example.test"}} {
		if _, err = admin.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Dual signer fixture',$2,'hash','1990-01-01')`, fixture.id, fixture.email); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = admin.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, administratorID); err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, `INSERT INTO privacy_executor_grants(user_id,granted_by,granted_at) VALUES($1,$2,$3)`, executorID, administratorID, baseNow); err != nil {
		t.Fatal(err)
	}
	policy := "dual-signer-" + uuid.NewString()
	if _, err = admin.Exec(ctx, `INSERT INTO privacy_request_policies(version,category_catalogue,executor_version,plan_schema_version,working_retention_days,adopted_at,adopted_by)
		VALUES($1,'[]','privacy-erasure-executor/v2','privacy-erasure-plan/v2',90,$2,$3)`, policy, baseNow, administratorID); err != nil {
		t.Fatal(err)
	}
	digest := bytes.Repeat([]byte{7}, sha256.Size)
	image := "sha256:" + strings.Repeat("7", 64)
	for _, kind := range []string{"RESTORE", "INFRASTRUCTURE", "PROVIDER", "SCHEMA"} {
		artifact := dualSignerArtifactFixture(kind, policy, image, digest)
		payload, _ := json.Marshal(artifact)
		var evidenceID uuid.UUID
		contract := map[string]string{"RESTORE": "mycfc/privacy-restore-drill-attestation/v2", "INFRASTRUCTURE": "mycfc/privacy-infrastructure-posture/v1", "PROVIDER": "mycfc/privacy-provider-registry/v2", "SCHEMA": "mycfc/schema-migration-inventory/v1"}[kind]
		kindDigest := sha256.Sum256([]byte(kind + uuid.NewString()))
		if err = admin.QueryRow(ctx, `SELECT privacy_activation_record_authenticated_evidence($1,$2,$3,$4,$5,$6,$7)`, administratorID, kind, kindDigest[:], contract, baseNow, baseNow.Add(90*24*time.Hour), payload).Scan(&evidenceID); err != nil {
			t.Fatal(err)
		}
	}

	broker, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close(ctx)
	if _, err = broker.Exec(ctx, `SET SESSION AUTHORIZATION `+brokerIdentifier); err != nil {
		t.Fatal(err)
	}
	var evidenceIDs []uuid.UUID
	var evidenceDigest, activationDigest, schemaDigest []byte
	var executorVersion, planVersion, materialImage string
	if err = broker.QueryRow(ctx, `SELECT evidence_ids,evidence_set_sha256,activation_sha256,executor_version,plan_schema_version,image_digest,schema_migration_digest FROM privacy_activation_broker_material($1)`, policy).
		Scan(&evidenceIDs, &evidenceDigest, &activationDigest, &executorVersion, &planVersion, &materialImage, &schemaDigest); err != nil {
		t.Fatal(err)
	}
	registry, registryDigest := dualSignerRegistryFixture(executorID, administratorID)
	preparedAt := time.Now().UTC().Truncate(time.Second)
	materialRaw, material, err := privacyrequests.GenerateActivationApprovalMaterial(privacyrequests.ActivationApprovalMaterial{
		SourceSHA: "0123456789abcdef0123456789abcdef01234567", PolicyVersion: policy, EvidenceIDs: evidenceIDs,
		EvidenceSetSHA256: hex.EncodeToString(evidenceDigest), ActivationSHA256: hex.EncodeToString(activationDigest),
		ExecutorVersion: executorVersion, PlanSchemaVersion: planVersion, ImageDigest: materialImage,
		SchemaMigrationDigest: hex.EncodeToString(schemaDigest), SignerRegistrySHA256: registryDigest,
	}, preparedAt)
	if err != nil {
		t.Fatal(err)
	}
	materialDigest := sha256.Sum256(materialRaw)
	registryDigestBytes, _ := hex.DecodeString(registryDigest)
	executorPublicDigest, _ := hex.DecodeString(registry.Signers.Executor.PublicKeySPKI256)
	administratorPublicDigest, _ := hex.DecodeString(registry.Signers.Administrator.PublicKeySPKI256)
	register := func(raw []byte, source string) error {
		var ceremonyID uuid.UUID
		return broker.QueryRow(ctx, `SELECT privacy_activation_broker_register_ceremony(
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15::jsonb,$16,$17,
			$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29)`,
			material.CeremonyID, material.ProposalID, source, material.PolicyVersion, material.EvidenceIDs,
			evidenceDigest, activationDigest, material.ExecutorVersion, material.PlanSchemaVersion, material.ImageDigest, schemaDigest,
			registryDigestBytes, materialDigest[:], raw, string(raw), material.PreparedAt, material.CeremonyExpiresAt,
			registry.Signers.Executor.ActorRef, registry.Signers.Executor.SigningKeyID, registry.Signers.Executor.KMSKeyARN,
			executorPublicDigest, int64(registry.Signers.Executor.GitHubActorID), registry.Signers.Executor.GitHubEnvironment,
			registry.Signers.Administrator.ActorRef, registry.Signers.Administrator.SigningKeyID, registry.Signers.Administrator.KMSKeyARN,
			administratorPublicDigest, int64(registry.Signers.Administrator.GitHubActorID), registry.Signers.Administrator.GitHubEnvironment).Scan(&ceremonyID)
	}
	if err = register(materialRaw, strings.Repeat("f", 40)); err == nil || !strings.Contains(err.Error(), "privacy_activation_ceremony_rejected") {
		t.Fatalf("material/source mismatch accepted: %v", err)
	}
	if err = register(materialRaw, material.SourceSHA); err != nil {
		t.Fatal(err)
	}

	executorRaw, executorNonce := dualSignerEnvelope(t, materialRaw, material, registry, privacyrequests.ActivationExecutorRole,
		registry.Signers.Executor.GitHubActorID, preparedAt)
	administratorRaw, administratorNonce := dualSignerEnvelope(t, materialRaw, material, registry, privacyrequests.ActivationAdministratorRole,
		registry.Signers.Administrator.GitHubActorID, preparedAt)
	executorNonceDigest := sha256.Sum256(executorNonce)
	administratorNonceDigest := sha256.Sum256(administratorNonce)
	activate := func(executor, administrator []byte) (uuid.UUID, error) {
		var approval uuid.UUID
		err := broker.QueryRow(ctx, `SELECT privacy_activation_broker_activate($1,$2,$3::jsonb,$4,$5::jsonb,$6,$7)`,
			material.CeremonyID, executor, string(executor), administrator, string(administrator),
			executorNonceDigest[:], administratorNonceDigest[:]).Scan(&approval)
		return approval, err
	}
	var changed map[string]any
	if err = json.Unmarshal(executorRaw, &changed); err != nil {
		t.Fatal(err)
	}
	changed["github_environment"] = privacyrequests.ActivationAdministratorEnvironment
	wrongEnvironment, _ := json.Marshal(changed)
	if _, err = activate(wrongEnvironment, administratorRaw); err == nil || !strings.Contains(err.Error(), "privacy_activation_signed_approval_rejected") {
		t.Fatalf("wrong environment accepted: %v", err)
	}
	if _, err = activate(nil, administratorRaw); err == nil {
		t.Fatal("single approval activated the worker")
	}
	approvalID, err := activate(executorRaw, administratorRaw)
	if err != nil || approvalID == uuid.Nil {
		t.Fatalf("valid dual approval rejected: id=%s error=%v", approvalID, err)
	}
	var enabled, ready, engaged bool
	var storedExecutorRaw, storedAdministratorRaw []byte
	var ceremonies int
	if err = admin.QueryRow(ctx, `SELECT activation.enabled,activation.fulfilment_ready,switch.engaged,
		(SELECT count(*) FROM privacy_protected.activation_ceremonies ceremony WHERE ceremony.ceremony_id=$1),
		(SELECT raw_envelope FROM privacy_protected.activation_signed_approvals WHERE ceremony_id=$1 AND signer_role='EXECUTOR'),
		(SELECT raw_envelope FROM privacy_protected.activation_signed_approvals WHERE ceremony_id=$1 AND signer_role='ADMINISTRATOR')
		FROM privacy_request_activation activation CROSS JOIN privacy_worker_kill_switch switch WHERE activation.singleton AND switch.singleton`, material.CeremonyID).
		Scan(&enabled, &ready, &engaged, &ceremonies, &storedExecutorRaw, &storedAdministratorRaw); err != nil {
		t.Fatal(err)
	}
	if !enabled || !ready || engaged || ceremonies != 1 || !bytes.Equal(storedExecutorRaw, executorRaw) || !bytes.Equal(storedAdministratorRaw, administratorRaw) {
		t.Fatalf("activation enabled=%t ready=%t engaged=%t ceremonies=%d exact_executor=%t exact_admin=%t",
			enabled, ready, engaged, ceremonies, bytes.Equal(storedExecutorRaw, executorRaw), bytes.Equal(storedAdministratorRaw, administratorRaw))
	}
	if _, err = activate(executorRaw, administratorRaw); err == nil ||
		(!strings.Contains(err.Error(), "privacy_activation_signed_approval_replay") && !strings.Contains(err.Error(), "privacy_activation_signed_approval_rejected")) {
		t.Fatalf("approval replay accepted: %v", err)
	}
}

func dualSignerEnvelope(t *testing.T, materialRaw []byte, material privacyrequests.ActivationApprovalMaterial,
	registry privacyrequests.ActivationSignerRegistry, role string, githubActorID uint64, issuedAt time.Time) ([]byte, []byte) {
	t.Helper()
	unsigned, _, _, err := privacyrequests.NewActivationApprovalUnsigned(materialRaw, material, registry, role,
		githubActorID, 7001, 1, issuedAt)
	if err != nil {
		t.Fatal(err)
	}
	envelope := privacyrequests.ActivationApprovalEnvelope{ActivationApprovalUnsigned: unsigned,
		SignatureDERBase64: base64.StdEncoding.EncodeToString([]byte{0x30, 0x06, 0x02, 0x01, 0x01, 0x02, 0x01, 0x01})}
	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	nonce, err := base64.StdEncoding.Strict().DecodeString(unsigned.Nonce)
	if err != nil {
		t.Fatal(err)
	}
	return raw, nonce
}

func dualSignerRegistryFixture(executorID, administratorID uuid.UUID) (privacyrequests.ActivationSignerRegistry, string) {
	registry := privacyrequests.ActivationSignerRegistry{Contract: privacyrequests.ActivationSignerRegistryContract,
		Signers: privacyrequests.ActivationRegistrySigners{
			Executor: privacyrequests.ActivationSigner{ActorRef: executorID, SigningKeyID: "executor-key-v2",
				KMSKeyARN:        "arn:aws:kms:eu-west-1:123456789012:key/11111111-1111-4111-8111-111111111111",
				PublicKeySPKI256: strings.Repeat("1", 64), GitHubActorID: 101, GitHubEnvironment: privacyrequests.ActivationExecutorEnvironment},
			Administrator: privacyrequests.ActivationSigner{ActorRef: administratorID, SigningKeyID: "administrator-key-v2",
				KMSKeyARN:        "arn:aws:kms:eu-west-1:123456789012:key/22222222-2222-4222-8222-222222222222",
				PublicKeySPKI256: strings.Repeat("2", 64), GitHubActorID: 202, GitHubEnvironment: privacyrequests.ActivationAdministratorEnvironment},
		}}
	raw, _ := json.Marshal(registry)
	digest := sha256.Sum256(raw)
	return registry, hex.EncodeToString(digest[:])
}

func dualSignerArtifactFixture(kind, policy, image string, digest []byte) map[string]any {
	artifact := map[string]any{"policy_version": policy, "executor_version": "privacy-erasure-executor/v2", "plan_schema_version": "privacy-erasure-plan/v2", "image_digest": image, "evidence_sha256": digest}
	switch kind {
	case "RESTORE":
		artifact["evidence_ref"], artifact["schema_migration_digest"] = "s3://fixture/restore?versionId=v1", digest
		artifact["restore_input_source"], artifact["restore_input_contract"] = "LIVE_LEDGER", "mycfc/privacy-restore-ledger-input/v2"
		artifact["restore_replay_contract"], artifact["restore_closure_contract"] = "relational-erasure-replay/v1", "restore-tombstone-closure/v4"
		artifact["restore_candidate_sha256"], artifact["restore_inventory_sha256"], artifact["restore_observer_sha256"] = digest, digest, digest
		artifact["restore_object_count"], artifact["restore_replayed_count"], artifact["restore_synthetic_count"] = 1, 1, 0
		artifact["restore_membership_postcondition_contract"], artifact["restore_membership_postcondition_sha256"] = "mycfc/membership-history-postcondition/v1", digest
		artifact["restore_membership_postcondition_verified_count"], artifact["restore_membership_count"], artifact["restore_variation_count"] = 1, 0, 0
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
		artifact["provider_registry_state"], artifact["provider_registration_count"], artifact["provider_registry_sha256"] = "READY", 0, digest
		artifact["provider_inventory_contract"] = "mycfc/privacy-provider-registry-source/v2"
	case "SCHEMA":
		artifact["evidence_ref"], artifact["signing_key_id"] = "s3://fixture/schema?versionId=v1", "fixture-key"
		artifact["schema_migration_digest"], artifact["baseline_includes_through"] = digest, baselineIncludesThrough
	}
	return artifact
}
