package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cfcoimbra/mycfc/internal/db"
)

func TestInfrastructureArtifactDerivesPlanStateAndImmutableObjectBindings(t *testing.T) {
	directory := t.TempDir()
	image := "sha256:" + strings.Repeat("a", 64)
	productionState := writeTestFile(t, directory, "production-state.json", []byte(`{"version":4,"terraform_version":"1.15.8","serial":12,"lineage":"production","resources":[]}`), 0o600)
	hetznerState := writeTestFile(t, directory, "hetzner-state.json", []byte(`{"version":4,"terraform_version":"1.15.8","serial":8,"lineage":"hetzner","resources":[]}`), 0o600)
	productionPlan := planJSON(t, map[string]any{
		"privacy_worker_infrastructure_enabled": true, "privacy_worker_s3_deletion_enabled": true,
		"privacy_worker_ledger_broker_invoke_enabled": true, "privacy_worker_monitoring_enabled": true,
		"image_digest": image,
	})
	hetznerPlan := planJSON(t, map[string]any{"privacy_restore_infrastructure_enabled": true, "privacy_restore_ledger_write_enabled": true})
	productionPlanPath := writeTestFile(t, directory, "production-plan.json", productionPlan, 0o600)
	hetznerPlanPath := writeTestFile(t, directory, "hetzner-plan.json", hetznerPlan, 0o600)
	common := commonOptions{imageDigest: image, policyVersion: "privacy-v1", observedAt: "2026-09-10T12:00:00Z", signingKeyID: "operations-v1",
		evidenceRef: "s3://privacy-evidence/infrastructure.json?versionId=v1", kmsKeyARN: "arn:aws:kms:eu-west-1:123456789012:key/example"}
	observation, err := observeInfrastructure(common, productionState, hetznerState, productionPlanPath, hetznerPlanPath)
	if err != nil {
		t.Fatal(err)
	}
	if observation.ProductionStateSerial != 12 || observation.HetznerStateSerial != 8 || observation.ImageDigest != image {
		t.Fatalf("unexpected observation: %#v", observation)
	}
	observationJSON, err := json.Marshal(observation)
	if err != nil {
		t.Fatal(err)
	}
	common.evidenceFile = writeTestFile(t, directory, "observation.json", append(observationJSON, '\n'), 0o600)
	sum := sha256.Sum256(append(observationJSON, '\n'))
	head, err := json.Marshal(evidenceHead{VersionID: "v1", ContentLength: int64(len(observationJSON) + 1),
		ChecksumSHA256: base64.StdEncoding.EncodeToString(sum[:]), ServerSideEncryption: "aws:kms", SSEKMSKeyID: common.kmsKeyARN})
	if err != nil {
		t.Fatal(err)
	}
	common.evidenceHead = writeTestFile(t, directory, "head.json", head, 0o600)
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	common.privateKeyFile = writeTestFile(t, directory, "signing.key", []byte(base64.StdEncoding.EncodeToString(private)), 0o600)
	var output bytes.Buffer
	if err = signArtifact(common, observation, infrastructureArtifact(common, observation), &output); err != nil {
		t.Fatal(err)
	}
	var artifact map[string]any
	if err = json.Unmarshal(output.Bytes(), &artifact); err != nil {
		t.Fatal(err)
	}
	signature, err := base64.StdEncoding.DecodeString(artifact["signature_ed25519"].(string))
	if err != nil {
		t.Fatal(err)
	}
	delete(artifact, "signature_ed25519")
	canonical, err := json.Marshal(artifact)
	if err != nil || !ed25519.Verify(public, canonical, signature) {
		t.Fatal("artifact signature did not bind the derived observation")
	}
	if artifact["evidence_sha256"] != digest(append(observationJSON, '\n')) || artifact["image_digest"] != image {
		t.Fatal("artifact omitted immutable observation bindings")
	}
	var changed map[string]any
	if err = json.Unmarshal(productionPlan, &changed); err != nil {
		t.Fatal(err)
	}
	changed["resource_changes"] = []any{map[string]any{"change": map[string]any{"actions": []string{"update"}}}}
	changedPlan, err := json.Marshal(changed)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(productionPlanPath, changedPlan, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = observeInfrastructure(common, productionState, hetznerState, productionPlanPath, hetznerPlanPath); err == nil {
		t.Fatal("infrastructure observation accepted a plan with changes")
	}
}

func TestProviderEvidenceStaysNotReadyForShippedEmptyRegistry(t *testing.T) {
	directory := t.TempDir()
	registry := writeTestFile(t, directory, "registry.json", []byte(`{"contract":"mycfc/privacy-provider-registry-source/v1","registry_state":"EMPTY","providers":[]}`), 0o600)
	observation, err := observeProvider(registry)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Result != "NOT_READY" || observation.RegistrationCount != 0 {
		t.Fatalf("empty registry became eligible: %#v", observation)
	}
	artifact := providerArtifact(commonOptions{}, observation)
	if artifact["result"] != "NOT_READY" || artifact["registry_state"] != "EMPTY" {
		t.Fatalf("empty registry artifact became eligible: %#v", artifact)
	}
}

func TestSchemaObservationMatchesExactMigrationInventory(t *testing.T) {
	directory := t.TempDir()
	versions := []byte(strings.Join(db.EmbeddedMigrationInventory(), "\n"))
	versionsPath := writeTestFile(t, directory, "versions.txt", versions, 0o600)
	observation, err := observeSchema(versionsPath)
	if err != nil {
		t.Fatal(err)
	}
	if observation.SchemaMigrationDigest != db.EmbeddedMigrationDigest() || observation.MigrationCount != len(db.EmbeddedMigrationInventory())-1 ||
		observation.BaselineThrough != "202609100015_privacy_membership_postcondition" {
		t.Fatalf("unexpected schema observation: %#v", observation)
	}
	if err := os.WriteFile(versionsPath, append(versions, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := observeSchema(versionsPath); err == nil {
		t.Fatal("non-canonical schema inventory was accepted")
	}
}

func planJSON(t *testing.T, values map[string]any) []byte {
	t.Helper()
	variables := make(map[string]any, len(values))
	for name, value := range values {
		variables[name] = map[string]any{"value": value}
	}
	payload, err := json.Marshal(map[string]any{"format_version": "1.2", "terraform_version": "1.15.8", "complete": true, "errored": false,
		"variables": variables, "resource_changes": []any{}, "resource_drift": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func writeTestFile(t *testing.T, directory, name string, payload []byte, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, payload, mode); err != nil {
		t.Fatal(err)
	}
	return path
}
