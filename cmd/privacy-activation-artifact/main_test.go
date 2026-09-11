package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
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

func TestProviderEvidenceIsReadyForCompleteClosedEmptyRegistry(t *testing.T) {
	directory := t.TempDir()
	registry := writeTestFile(t, directory, "registry.json", []byte(`{"contract":"mycfc/privacy-provider-registry-source/v2","inventory_scope":"SUBJECT_SPECIFIC_EXTERNAL_INTEGRATIONS","inventory_state":"COMPLETE","providers":[]}`), 0o600)
	observation, err := observeProvider(registry)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Result != "READY" || observation.RegistryState != "READY" || observation.RegistrationCount != 0 ||
		observation.InventoryContract != "mycfc/privacy-provider-registry-source/v2" {
		t.Fatalf("complete empty registry was not eligible: %#v", observation)
	}
	artifact := providerArtifact(commonOptions{}, observation)
	if artifact["contract"] != "mycfc/privacy-provider-registry/v2" || artifact["result"] != "SUCCEEDED" ||
		artifact["registry_state"] != "READY" || artifact["registration_count"] != int64(0) {
		t.Fatalf("complete empty registry artifact was not ready: %#v", artifact)
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
		observation.BaselineThrough != "202609110003_privacy_activation_emergency_fence" {
		t.Fatalf("unexpected schema observation: %#v", observation)
	}
	if err := os.WriteFile(versionsPath, append(versions, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := observeSchema(versionsPath); err == nil {
		t.Fatal("non-canonical schema inventory was accepted")
	}
}

func TestArtifactCommandDispatchObservesAndSignsEveryKind(t *testing.T) {
	directory := t.TempDir()
	image := "sha256:" + strings.Repeat("a", 64)
	productionState := writeTestFile(t, directory, "dispatch-production-state.json", []byte(`{"version":4,"terraform_version":"1.15.8","serial":12,"lineage":"production","resources":[]}`), 0o600)
	hetznerState := writeTestFile(t, directory, "dispatch-hetzner-state.json", []byte(`{"version":4,"terraform_version":"1.15.8","serial":8,"lineage":"hetzner","resources":[]}`), 0o600)
	productionPlan := writeTestFile(t, directory, "dispatch-production-plan.json", planJSON(t, map[string]any{
		"privacy_worker_infrastructure_enabled": true, "privacy_worker_s3_deletion_enabled": true,
		"privacy_worker_ledger_broker_invoke_enabled": true, "privacy_worker_monitoring_enabled": true, "image_digest": image,
	}), 0o600)
	hetznerPlan := writeTestFile(t, directory, "dispatch-hetzner-plan.json", planJSON(t, map[string]any{
		"privacy_restore_infrastructure_enabled": true, "privacy_restore_ledger_write_enabled": true,
	}), 0o600)
	registry := writeTestFile(t, directory, "dispatch-registry.json", []byte(`{"contract":"mycfc/privacy-provider-registry-source/v2","inventory_scope":"SUBJECT_SPECIFIC_EXTERNAL_INTEGRATIONS","inventory_state":"COMPLETE","providers":[]}`), 0o600)
	versions := writeTestFile(t, directory, "dispatch-versions.txt", []byte(strings.Join(db.EmbeddedMigrationInventory(), "\n")), 0o600)
	_, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	privatePath := writeTestFile(t, directory, "dispatch-signing.key", []byte(base64.StdEncoding.EncodeToString(private)), 0o600)
	cases := []struct {
		name, observe, sign string
		sourceArgs          []string
	}{
		{"infrastructure", "infrastructure-observe", "infrastructure-sign", []string{"--production-state", productionState, "--hetzner-state", hetznerState, "--production-plan", productionPlan, "--hetzner-plan", hetznerPlan, "--image-digest", image}},
		{"provider", "provider-observe", "provider-sign", []string{"--registry", registry}},
		{"schema", "schema-observe", "schema-sign", []string{"--schema-versions", versions}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var observed bytes.Buffer
			if err := run(append([]string{testCase.observe}, testCase.sourceArgs...), &observed); err != nil {
				t.Fatalf("observe: %v", err)
			}
			if observed.Len() == 0 || observed.Bytes()[observed.Len()-1] != '\n' {
				t.Fatalf("non-canonical observation output %q", observed.String())
			}
			evidencePath := writeTestFile(t, directory, testCase.name+"-observation.json", observed.Bytes(), 0o600)
			sum := sha256.Sum256(observed.Bytes())
			head, marshalErr := json.Marshal(evidenceHead{VersionID: "version-1", ContentLength: int64(observed.Len()), ChecksumSHA256: base64.StdEncoding.EncodeToString(sum[:]),
				ServerSideEncryption: "aws:kms", SSEKMSKeyID: "arn:aws:kms:eu-west-1:123456789012:key/test"})
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			headPath := writeTestFile(t, directory, testCase.name+"-head.json", head, 0o600)
			signArgs := append([]string{testCase.sign}, testCase.sourceArgs...)
			signArgs = append(signArgs,
				"--image-digest", image,
				"--policy-version", "policy-v1",
				"--observed-at", "2026-09-10T12:00:00Z",
				"--signing-key-id", "operations-v1",
				"--evidence-ref", "s3://privacy-evidence/"+testCase.name+".json?versionId=version-1",
				"--evidence-file", evidencePath,
				"--evidence-head", headPath,
				"--kms-key-arn", "arn:aws:kms:eu-west-1:123456789012:key/test",
				"--private-key-file", privatePath,
			)
			var signed bytes.Buffer
			if err := run(signArgs, &signed); err != nil {
				t.Fatalf("sign: %v", err)
			}
			var artifact map[string]any
			if err := json.Unmarshal(signed.Bytes(), &artifact); err != nil || artifact["signature_ed25519"] == nil {
				t.Fatalf("signed artifact=%v error=%v", artifact, err)
			}
		})
	}
}

func TestArtifactCommandAndSourceBoundaries(t *testing.T) {
	for _, args := range [][]string{nil, {"unknown"}, {"provider-observe", "unexpected"}, {"schema-observe", "--unknown"}} {
		if err := run(args, io.Discard); err == nil {
			t.Fatalf("invalid arguments accepted: %v", args)
		}
	}
	directory := t.TempDir()
	for name, payload := range map[string]string{
		"legacy-incomplete": `{"contract":"mycfc/privacy-provider-registry-source/v1","registry_state":"EMPTY","providers":[]}`,
		"wrong-contract":    `{"contract":"wrong","inventory_scope":"SUBJECT_SPECIFIC_EXTERNAL_INTEGRATIONS","inventory_state":"COMPLETE","providers":[]}`,
		"wrong-scope":       `{"contract":"mycfc/privacy-provider-registry-source/v2","inventory_scope":"ALL_PROVIDERS","inventory_state":"COMPLETE","providers":[]}`,
		"incomplete":        `{"contract":"mycfc/privacy-provider-registry-source/v2","inventory_scope":"SUBJECT_SPECIFIC_EXTERNAL_INTEGRATIONS","inventory_state":"INCOMPLETE","providers":[]}`,
		"missing-providers": `{"contract":"mycfc/privacy-provider-registry-source/v2","inventory_scope":"SUBJECT_SPECIFIC_EXTERNAL_INTEGRATIONS","inventory_state":"COMPLETE"}`,
		"nonempty":          `{"contract":"mycfc/privacy-provider-registry-source/v2","inventory_scope":"SUBJECT_SPECIFIC_EXTERNAL_INTEGRATIONS","inventory_state":"COMPLETE","providers":[{}]}`,
		"unknown-field":     `{"contract":"mycfc/privacy-provider-registry-source/v2","inventory_scope":"SUBJECT_SPECIFIC_EXTERNAL_INTEGRATIONS","inventory_state":"COMPLETE","providers":[],"unknown":true}`,
		"trailing":          `{"contract":"mycfc/privacy-provider-registry-source/v2","inventory_scope":"SUBJECT_SPECIFIC_EXTERNAL_INTEGRATIONS","inventory_state":"COMPLETE","providers":[]} {}`,
	} {
		if _, err := observeProvider(writeTestFile(t, directory, name+".json", []byte(payload), 0o600)); err == nil {
			t.Fatalf("invalid provider source accepted: %s", name)
		}
	}
	if _, _, err := loadState(writeTestFile(t, directory, "bad-state.json", []byte(`{"version":3}`), 0o600)); err == nil {
		t.Fatal("invalid state accepted")
	}
	if validImageDigest("sha256:"+strings.Repeat("A", 64)) || validImageDigest("sha512:"+strings.Repeat("a", 64)) {
		t.Fatal("invalid image digest accepted")
	}
}

func TestArtifactDispatchAndInfrastructureFailureBoundaries(t *testing.T) {
	directory := t.TempDir()
	image := "sha256:" + strings.Repeat("a", 64)
	missing := filepath.Join(directory, "missing")
	for name, args := range map[string][]string{
		"infrastructure arguments": {"infrastructure-observe", "unexpected"},
		"infrastructure source":    {"infrastructure-observe", "--image-digest", "invalid"},
		"provider source":          {"provider-observe", "--registry", missing},
		"schema source":            {"schema-observe", "--schema-versions", missing},
	} {
		t.Run(name, func(t *testing.T) {
			if err := run(args, io.Discard); err == nil {
				t.Fatalf("invalid source accepted: %v", args)
			}
		})
	}

	productionState := writeTestFile(t, directory, "failure-production-state.json", []byte(`{"version":4,"terraform_version":"1.15.8","serial":12,"lineage":"production","resources":[]}`), 0o600)
	hetznerState := writeTestFile(t, directory, "failure-hetzner-state.json", []byte(`{"version":4,"terraform_version":"1.15.8","serial":8,"lineage":"hetzner","resources":[]}`), 0o600)
	productionValues := func(selectedImage string) map[string]any {
		return map[string]any{
			"privacy_worker_infrastructure_enabled": true, "privacy_worker_s3_deletion_enabled": true,
			"privacy_worker_ledger_broker_invoke_enabled": true, "privacy_worker_monitoring_enabled": true,
			"image_digest": selectedImage,
		}
	}
	hetznerValues := func() map[string]any {
		return map[string]any{"privacy_restore_infrastructure_enabled": true, "privacy_restore_ledger_write_enabled": true}
	}
	productionPlan := writeTestFile(t, directory, "failure-production-plan.json", planJSON(t, productionValues(image)), 0o600)
	hetznerPlan := writeTestFile(t, directory, "failure-hetzner-plan.json", planJSON(t, hetznerValues()), 0o600)
	common := commonOptions{imageDigest: image}

	for name, paths := range map[string][4]string{
		"production state": {missing, hetznerState, productionPlan, hetznerPlan},
		"hetzner state":    {productionState, missing, productionPlan, hetznerPlan},
		"hetzner plan":     {productionState, hetznerState, productionPlan, missing},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := observeInfrastructure(common, paths[0], paths[1], paths[2], paths[3]); err == nil {
				t.Fatalf("missing %s accepted", name)
			}
		})
	}

	productionNotReady := productionValues(image)
	productionNotReady["privacy_worker_monitoring_enabled"] = false
	productionNotReadyPath := writeTestFile(t, directory, "failure-production-not-ready.json", planJSON(t, productionNotReady), 0o600)
	if _, err := observeInfrastructure(common, productionState, hetznerState, productionNotReadyPath, hetznerPlan); err == nil {
		t.Fatal("production plan with a disabled readiness gate was accepted")
	}
	hetznerNotReady := hetznerValues()
	hetznerNotReady["privacy_restore_ledger_write_enabled"] = false
	hetznerNotReadyPath := writeTestFile(t, directory, "failure-hetzner-not-ready.json", planJSON(t, hetznerNotReady), 0o600)
	if _, err := observeInfrastructure(common, productionState, hetznerState, productionPlan, hetznerNotReadyPath); err == nil {
		t.Fatal("restore plan with a disabled readiness gate was accepted")
	}
	mismatchedImagePath := writeTestFile(t, directory, "failure-image-mismatch.json", planJSON(t, productionValues("sha256:"+strings.Repeat("b", 64))), 0o600)
	if _, err := observeInfrastructure(common, productionState, hetznerState, mismatchedImagePath, hetznerPlan); err == nil {
		t.Fatal("production plan for a different image was accepted")
	}
}

func TestArtifactIOAndEvidenceBoundaries(t *testing.T) {
	directory := t.TempDir()
	outputPath := filepath.Join(directory, "artifact.json")
	if err := writeCanonical(commonOptions{output: outputPath}, map[string]string{"contract": "test"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if payload, err := os.ReadFile(outputPath); err != nil || string(payload) != "{\"contract\":\"test\"}\n" {
		t.Fatalf("canonical file=%q error=%v", payload, err)
	}
	if err := writeCanonical(commonOptions{output: outputPath}, map[string]string{"contract": "test"}, io.Discard); err == nil {
		t.Fatal("exclusive output overwrote an existing artifact")
	}
	for name, path := range map[string]string{
		"missing": filepath.Join(directory, "missing"),
		"empty":   writeTestFile(t, directory, "empty", nil, 0o600),
		"large":   writeTestFile(t, directory, "large", bytes.Repeat([]byte{'x'}, 5), 0o600),
	} {
		if _, err := readBounded(path, 4); err == nil {
			t.Fatalf("invalid bounded source accepted: %s", name)
		}
	}
	badMode := writeTestFile(t, directory, "bad-mode.key", []byte(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{'k'}, ed25519.PrivateKeySize))), 0o644)
	if _, err := loadPrivateKey(badMode); err == nil {
		t.Fatal("insecure private key accepted")
	}
	badEncoding := writeTestFile(t, directory, "bad-encoding.key", []byte("not-base64"), 0o600)
	if _, err := loadPrivateKey(badEncoding); err == nil {
		t.Fatal("invalid private key accepted")
	}
	common := commonOptions{evidenceRef: "https://example.invalid/evidence", evidenceHead: filepath.Join(directory, "missing-head")}
	if _, err := verifyEvidenceObject(common, []byte("evidence")); err == nil {
		t.Fatal("mutable evidence reference accepted")
	}
	common.evidenceRef = "s3://bucket/evidence?versionId=v1"
	common.kmsKeyARN = "arn:aws:kms:eu-west-1:123456789012:key/test"
	common.evidenceHead = writeTestFile(t, directory, "bad-head.json", []byte(`{"VersionId":"v1"}`), 0o600)
	if _, err := verifyEvidenceObject(common, []byte("evidence")); err == nil {
		t.Fatal("incomplete evidence metadata accepted")
	}
	if err := validateCommon(commonOptions{}); err == nil {
		t.Fatal("empty common fields accepted")
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
