package privacyrequests

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCompletionLinkIsSinglePurposeAndContainsOnlyToken(t *testing.T) {
	raw := bytes.Repeat([]byte{9}, completionTokenBytes)
	token := base64.RawURLEncoding.EncodeToString(raw)
	link, err := completionLink("https://mycfc.example/privacy/completion", token)
	if err != nil || link != "https://mycfc.example/privacy/completion/"+token {
		t.Fatalf("link=%q err=%v", link, err)
	}
	for _, forbidden := range []string{"subject", "request", "category", "email", "?"} {
		if strings.Contains(link, forbidden) {
			t.Fatalf("completion link exposed %q", forbidden)
		}
	}
	for _, invalid := range []string{"http://mycfc.example/privacy/completion", "https://mycfc.example/privacy/completion?request=1", "https://mycfc.example/privacy/completion#result", ""} {
		if _, err = completionLink(invalid, token); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid base %q accepted: %v", invalid, err)
		}
	}
}

func TestRestoreActivationAttestationRequiresAuthenticatedCurrentExactContract(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	key := bytes.Repeat([]byte{4}, sha256.Size)
	hexDigest := strings.Repeat("a", sha256.Size*2)
	release := ActivationReleaseBinding{PolicyVersion: "privacy-v1", ExecutorVersion: "privacy-erasure-executor/v2", PlanSchemaVersion: "privacy-erasure-plan/v2", ImageDigest: "sha256:" + hexDigest, SchemaMigrationDigest: hexDigest}
	immutableObject := func(name string) map[string]any {
		return map[string]any{"ref": "s3://evidence/" + name + "?versionId=version-1", "sha256": hexDigest, "checksum_sha256": hexDigest,
			"kms_key_arn": "arn:aws:kms:eu-west-1:123456789012:key/key-1", "size_bytes": 128}
	}
	document := map[string]any{
		"contract": "mycfc/privacy-restore-drill-attestation/v2", "result": "SUCCEEDED",
		"observed_at": now.Add(-time.Hour).Format(time.RFC3339), "valid_until": now.Add(-time.Hour).Add(90 * 24 * time.Hour).Format(time.RFC3339),
		"policy_version": "privacy-v1", "executor_version": "privacy-erasure-executor/v2", "plan_schema_version": "privacy-erasure-plan/v2",
		"image_digest": "sha256:" + hexDigest, "schema_migration_digest": hexDigest,
		"contracts": map[string]any{"backup": "mycfc/postgres-backup/v3", "ledger_input": "mycfc/privacy-restore-ledger-input/v2",
			"replay_result": "mycfc/privacy-restore-replay-result/v2", "replay": "relational-erasure-replay/v1", "closure": "restore-tombstone-closure/v4", "synthetic_fixture": "mycfc/privacy-restore-synthetic-fixture/v1"},
		"backup": map[string]any{"created_at": now.Add(-24 * time.Hour).Format(time.RFC3339), "manifest": immutableObject("manifest.json"), "dump": immutableObject("database.dump")},
		"ledger": map[string]any{"input_source": "LIVE_LEDGER", "inventory_sha256": hexDigest, "object_count": 2},
		"candidate": map[string]any{"result_sha256": hexDigest, "object_count": 2, "imported_count": 1, "replayed_count": 2, "already_applied_count": 1,
			"non_replayable_v1_count": 0, "absence_verified_count": 2, "synthetic_replayed_count": 0, "closure_v4_count": 2, "intent_only_count": 0,
			"legacy_closure_v2_count": 0, "erasure_effective_at_verified_count": 2, "membership_postcondition_contract": MembershipHistoryPostconditionVersion,
			"membership_postcondition_sha256": hexDigest, "membership_postcondition_verified_count": 2, "membership_count": 1, "variation_count": 1, "failed_count": 0},
		"observer": map[string]any{"image_digest": "sha256:" + hexDigest, "replay_count": 2, "source_already_applied_count": 1, "synthetic_count": 0,
			"verified_run_count": 2, "expected_checkpoint_count": 4, "succeeded_checkpoint_count": 4, "provider_absent_count": 2,
			"consent_clock_verified_count": 2, "closure_v4_count": 2, "erasure_effective_at_verified_count": 2,
			"membership_postcondition_contract": MembershipHistoryPostconditionVersion, "membership_postcondition_sha256": hexDigest,
			"membership_postcondition_verified_count": 2, "membership_count": 1, "variation_count": 1, "evidence_sha256": hexDigest},
		"evidence": immutableObject("restore-evidence.json"),
	}
	canonical, _ := json.Marshal(document)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(canonical)
	document["auth_hmac_sha256"] = hex.EncodeToString(mac.Sum(nil))
	payload, _ := json.Marshal(document)
	evidence, err := VerifyRestoreActivationAttestation(payload, key, release, now)
	if err != nil || evidence.kind != "RESTORE" || evidence.reference != "mycfc/privacy-restore-drill-attestation/v2" || len(evidence.digest) != sha256.Size || !evidence.observedAt.Equal(now.Add(-time.Hour)) {
		t.Fatalf("evidence=%+v err=%v", evidence, err)
	}
	tampered := append([]byte(nil), payload...)
	tampered[len(tampered)-2] ^= 1
	if _, err = VerifyRestoreActivationAttestation(tampered, key, release, now); !errors.Is(err, ErrActivationUnavailable) {
		t.Fatalf("tampered attestation error=%v", err)
	}
	if _, err = VerifyRestoreActivationAttestation(payload, bytes.Repeat([]byte{5}, sha256.Size), release, now); !errors.Is(err, ErrActivationUnavailable) {
		t.Fatalf("wrong authentication key error=%v", err)
	}
	document["image_digest"] = hexDigest
	canonical, _ = json.Marshal(mapWithoutAuthentication(document))
	mac.Reset()
	_, _ = mac.Write(canonical)
	document["auth_hmac_sha256"] = hex.EncodeToString(mac.Sum(nil))
	payload, _ = json.Marshal(document)
	if _, err = VerifyRestoreActivationAttestation(payload, key, release, now); !errors.Is(err, ErrActivationUnavailable) {
		t.Fatalf("unprefixed image digest error=%v", err)
	}
}

func mapWithoutAuthentication(document map[string]any) map[string]any {
	copy := make(map[string]any, len(document)-1)
	for key, value := range document {
		if key != "auth_hmac_sha256" {
			copy[key] = value
		}
	}
	return copy
}

func TestActivationArtifactsAreComputedNotCallerAsserted(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	hexDigest := strings.Repeat("a", sha256.Size*2)
	release := ActivationReleaseBinding{PolicyVersion: "privacy-v1", ExecutorVersion: "privacy-erasure-executor/v2", PlanSchemaVersion: "privacy-erasure-plan/v2", ImageDigest: "sha256:" + hexDigest, SchemaMigrationDigest: hexDigest}
	document := map[string]any{
		"contract": "mycfc/privacy-provider-registry/v2", "result": "SUCCEEDED", "observed_at": now.Add(-time.Hour).Format(time.RFC3339),
		"policy_version": "privacy-v1", "executor_version": "privacy-erasure-executor/v2", "plan_schema_version": "privacy-erasure-plan/v2",
		"image_digest": "sha256:" + hexDigest, "evidence_ref": "s3://evidence/provider.json?versionId=version-1", "evidence_sha256": hexDigest,
		"signing_key_id": "activation-key-1", "registry_state": "READY", "registration_count": 0, "provider_registry_sha256": hexDigest,
		"inventory_contract": "mycfc/privacy-provider-registry-source/v2",
	}
	canonical, _ := json.Marshal(document)
	document["signature_ed25519"] = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, canonical))
	payload, _ := json.Marshal(document)
	evidence, err := VerifyActivationArtifact(payload, map[string]ed25519.PublicKey{"activation-key-1": publicKey}, release, now)
	want := sha256.Sum256(payload)
	if err != nil || evidence.kind != "PROVIDER" || !bytes.Equal(evidence.digest, want[:]) {
		t.Fatalf("evidence=%+v err=%v", evidence, err)
	}
	wrongRelease := release
	wrongRelease.ImageDigest = "sha256:" + strings.Repeat("b", sha256.Size*2)
	if _, err = VerifyActivationArtifact(payload, map[string]ed25519.PublicKey{"activation-key-1": publicKey}, wrongRelease, now); !errors.Is(err, ErrActivationUnavailable) {
		t.Fatalf("artifact from a different release error=%v", err)
	}
	if _, err = VerifyActivationArtifact(payload, map[string]ed25519.PublicKey{"activation-key-1": bytes.Repeat([]byte{9}, ed25519.PublicKeySize)}, release, now); !errors.Is(err, ErrActivationUnavailable) {
		t.Fatalf("artifact with untrusted signature error=%v", err)
	}
	document["registration_count"] = 1
	canonical, _ = json.Marshal(mapWithoutSignature(document))
	document["signature_ed25519"] = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, canonical))
	payload, _ = json.Marshal(document)
	if _, err = VerifyActivationArtifact(payload, map[string]ed25519.PublicKey{"activation-key-1": publicKey}, release, now); !errors.Is(err, ErrActivationUnavailable) {
		t.Fatalf("unsupported nonempty provider registry error=%v", err)
	}
	document["registration_count"] = 0
	document["inventory_contract"] = "mycfc/privacy-provider-registry-source/v1"
	canonical, _ = json.Marshal(mapWithoutSignature(document))
	document["signature_ed25519"] = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, canonical))
	payload, _ = json.Marshal(document)
	if _, err = VerifyActivationArtifact(payload, map[string]ed25519.PublicKey{"activation-key-1": publicKey}, release, now); !errors.Is(err, ErrActivationUnavailable) {
		t.Fatalf("legacy incomplete provider registry error=%v", err)
	}
}

func mapWithoutSignature(document map[string]any) map[string]any {
	copy := make(map[string]any, len(document)-1)
	for key, value := range document {
		if key != "signature_ed25519" {
			copy[key] = value
		}
	}
	return copy
}

func TestCompletionWorkerFailsClosedBeforeDatabaseAccess(t *testing.T) {
	worker := CompletionWorker{WorkerRef: uuid.New(), Key: bytes.Repeat([]byte{1}, sha256.Size), DetailBaseURL: "https://mycfc.example/privacy/completion"}
	if _, err := worker.ListPending(context.Background(), 10); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil pool list pending error=%v", err)
	}
	if _, err := worker.ActivationReady(context.Background()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil pool activation readiness error=%v", err)
	}
	if _, err := worker.Complete(context.Background(), uuid.New()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil pool completion error=%v", err)
	}
	for _, token := range []string{"", "not-a-token", base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, completionTokenBytes-1))} {
		if _, err := (Service{}).ConsumeCompletionDetail(context.Background(), token); !errors.Is(err, ErrCompletionLinkUnavailable) {
			t.Fatalf("invalid token %q error=%v", token, err)
		}
		if err := (Service{}).ValidateCompletionLink(context.Background(), token); !errors.Is(err, ErrCompletionLinkUnavailable) {
			t.Fatalf("invalid validation token %q error=%v", token, err)
		}
	}
}

func TestActivationEvidenceWrappersRejectInvalidInputBeforeDatabaseAccess(t *testing.T) {
	evidence := VerifiedActivationEvidence{
		kind: "SCHEMA", digest: bytes.Repeat([]byte{1}, sha256.Size), reference: "mycfc/schema-migration-inventory/v1",
		observedAt: time.Now().UTC(), expiresAt: time.Now().UTC().Add(time.Hour),
		artifact: activationArtifactRecord{EvidenceSHA256: bytes.Repeat([]byte{2}, sha256.Size)},
	}
	if _, err := (Service{}).RecordActivationEvidence(context.Background(), uuid.New(), evidence); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil pool evidence error=%v", err)
	}
	if _, err := (Service{}).VerifyAndRecordActivationArtifact(context.Background(), uuid.New(), []byte("invalid"), nil, ActivationReleaseBinding{}, time.Now()); !errors.Is(err, ErrActivationUnavailable) {
		t.Fatalf("invalid signed artifact error=%v", err)
	}
	if _, err := (Service{}).VerifyAndRecordRestoreActivationEvidence(context.Background(), uuid.New(), []byte("invalid"), nil, ActivationReleaseBinding{}, time.Now()); !errors.Is(err, ErrActivationUnavailable) {
		t.Fatalf("invalid restore artifact error=%v", err)
	}
}

func TestCompletionDeliveryUsesOnlyCapturedEncryptedRecipient(t *testing.T) {
	key := bytes.Repeat([]byte{3}, sha256.Size)
	captured, err := SealDelivery(key, Delivery{Recipient: "captured@example.test", ContactURL: "https://mycfc.example/legal/direitos"})
	if err != nil {
		t.Fatal(err)
	}
	payload, digest, token, err := prepareCompletionDelivery(key, captured, "https://mycfc.example/privacy/completion", func(out []byte) (int, error) {
		return copy(out, bytes.Repeat([]byte{6}, completionTokenBytes)), nil
	})
	if err != nil || len(digest) != sha256.Size || token == "" {
		t.Fatalf("digest=%x token=%q err=%v", digest, token, err)
	}
	delivery, err := OpenDelivery(key, payload)
	if err != nil || delivery.Recipient != "captured@example.test" || delivery.ContactURL != "https://mycfc.example/privacy/completion/"+token {
		t.Fatalf("delivery=%+v err=%v", delivery, err)
	}
	if strings.Contains(delivery.ContactURL, "captured") || strings.Contains(delivery.ContactURL, "example.test@") {
		t.Fatal("completion URL copied recipient or identity")
	}
}

func TestCompletionWorkerRequiresHTTPSAndStrongKey(t *testing.T) {
	base := CompletionWorker{WorkerRef: uuid.New(), Key: bytes.Repeat([]byte{1}, sha256.Size), DetailBaseURL: "https://mycfc.example/privacy/completion"}
	if base.valid() {
		t.Fatal("worker without a pool was valid")
	}
	if validCompletionBaseURL("http://mycfc.example/privacy/completion") || validCompletionBaseURL("https://mycfc.example/privacy/completion?x=1") {
		t.Fatal("unsafe completion base URL accepted")
	}
}

func TestCompletionDeliveryRejectsCryptographicAndRandomnessFailures(t *testing.T) {
	key := bytes.Repeat([]byte{3}, sha256.Size)
	sealed, err := SealDelivery(key, Delivery{Recipient: "captured@example.test", ContactURL: "https://mycfc.example/legal/direitos"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = prepareCompletionDelivery(key[:16], sealed, "https://mycfc.example/privacy/completion", rand.Read); !errors.Is(err, ErrCompletionUnavailable) {
		t.Fatalf("weak key error=%v", err)
	}
	randomFailure := errors.New("entropy unavailable")
	if _, _, _, err = prepareCompletionDelivery(key, sealed, "https://mycfc.example/privacy/completion", func([]byte) (int, error) {
		return 0, randomFailure
	}); !errors.Is(err, randomFailure) {
		t.Fatalf("random failure error=%v", err)
	}
	if _, _, _, err = prepareCompletionDelivery(key, sealed, "https://mycfc.example/privacy/completion", func(target []byte) (int, error) {
		return copy(target, bytes.Repeat([]byte{1}, len(target)-1)), nil
	}); !errors.Is(err, ErrCompletionUnavailable) {
		t.Fatalf("short random read error=%v", err)
	}
	if _, _, _, err = prepareCompletionDelivery(key, sealed, "http://mycfc.example/privacy/completion", func(target []byte) (int, error) {
		return copy(target, bytes.Repeat([]byte{1}, len(target))), nil
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unsafe base URL error=%v", err)
	}
}

func TestActivationArtifactSupportsInfrastructureAndSchemaContracts(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	hexDigest := strings.Repeat("a", sha256.Size*2)
	release := ActivationReleaseBinding{
		PolicyVersion: "privacy-v1", ExecutorVersion: SupportedExecutorVersion, PlanSchemaVersion: SupportedPlanSchemaVersion,
		ImageDigest: "sha256:" + hexDigest, SchemaMigrationDigest: hexDigest,
	}
	base := map[string]any{
		"result": "SUCCEEDED", "observed_at": now.Add(-time.Hour).Format(time.RFC3339),
		"policy_version": release.PolicyVersion, "executor_version": release.ExecutorVersion, "plan_schema_version": release.PlanSchemaVersion,
		"image_digest": release.ImageDigest, "evidence_ref": "s3://evidence/artifact.json?versionId=version-1",
		"evidence_sha256": hexDigest, "signing_key_id": "activation-key-1",
	}
	signedPayload := func(fields map[string]any) []byte {
		t.Helper()
		document := make(map[string]any, len(base)+len(fields)+1)
		for key, value := range base {
			document[key] = value
		}
		for key, value := range fields {
			document[key] = value
		}
		canonical, marshalErr := json.Marshal(document)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		document["signature_ed25519"] = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, canonical))
		payload, marshalErr := json.Marshal(document)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		return payload
	}

	infrastructure := signedPayload(map[string]any{
		"contract": "mycfc/privacy-infrastructure-posture/v1", "production_state_serial": 1, "hetzner_state_serial": 2,
		"production_state_sha256": hexDigest, "hetzner_state_sha256": hexDigest,
		"production_plan_sha256": hexDigest, "hetzner_plan_sha256": hexDigest,
		"worker_identity_enabled": true, "s3_version_deletion_enabled": true, "ledger_broker_invoke_enabled": true,
		"worker_monitoring_enabled": true, "restore_infrastructure_enabled": true, "restore_ledger_write_enabled": true,
	})
	evidence, err := VerifyActivationArtifact(infrastructure, map[string]ed25519.PublicKey{"activation-key-1": publicKey}, release, now)
	if err != nil || evidence.kind != "INFRASTRUCTURE" || evidence.artifact.WorkerIdentityEnabled == nil || !*evidence.artifact.WorkerIdentityEnabled {
		t.Fatalf("infrastructure evidence=%+v err=%v", evidence, err)
	}

	schema := signedPayload(map[string]any{
		"contract": "mycfc/schema-migration-inventory/v1", "schema_migration_digest": hexDigest,
		"baseline_includes_through": "202609120007_guardian_release_status",
	})
	evidence, err = VerifyActivationArtifact(schema, map[string]ed25519.PublicKey{"activation-key-1": publicKey}, release, now)
	if err != nil || evidence.kind != "SCHEMA" || evidence.artifact.BaselineIncludesThrough == "" {
		t.Fatalf("schema evidence=%+v err=%v", evidence, err)
	}
	if _, err = VerifyActivationArtifact(signedPayload(map[string]any{
		"contract": "mycfc/privacy-infrastructure-posture/v1", "production_state_serial": 1, "hetzner_state_serial": 2,
		"production_state_sha256": hexDigest, "hetzner_state_sha256": hexDigest, "production_plan_sha256": hexDigest, "hetzner_plan_sha256": hexDigest,
		"worker_identity_enabled": false, "s3_version_deletion_enabled": true, "ledger_broker_invoke_enabled": true,
		"worker_monitoring_enabled": true, "restore_infrastructure_enabled": true, "restore_ledger_write_enabled": true,
	}), map[string]ed25519.PublicKey{"activation-key-1": publicKey}, release, now); !errors.Is(err, ErrActivationUnavailable) {
		t.Fatalf("inactive infrastructure artifact error=%v", err)
	}
	if _, err = VerifyActivationArtifact(signedPayload(map[string]any{
		"contract": "mycfc/schema-migration-inventory/v1", "schema_migration_digest": hexDigest,
		"baseline_includes_through": "202609100014_privacy_activation_broker",
	}), map[string]ed25519.PublicKey{"activation-key-1": publicKey}, release, now); !errors.Is(err, ErrActivationUnavailable) {
		t.Fatalf("stale schema artifact error=%v", err)
	}

	for name, payload := range map[string][]byte{
		"invalid-release":  infrastructure,
		"malformed-json":   []byte("{"),
		"unknown-contract": signedPayload(map[string]any{"contract": "mycfc/privacy-unknown/v1"}),
	} {
		t.Run(name, func(t *testing.T) {
			candidateRelease := release
			candidatePayload := payload
			if name == "invalid-release" {
				candidateRelease.ImageDigest = "invalid"
			}
			if _, verifyErr := VerifyActivationArtifact(candidatePayload, map[string]ed25519.PublicKey{"activation-key-1": publicKey}, candidateRelease, now); !errors.Is(verifyErr, ErrActivationUnavailable) {
				t.Fatalf("VerifyActivationArtifact() error=%v", verifyErr)
			}
		})
	}
}

func TestActivationEvidenceReferenceAndCanonicalJSONBoundaries(t *testing.T) {
	if validActivationEvidenceRef("s3://bucket/" + strings.Repeat("a", 2048) + "?versionId=v1") {
		t.Fatal("oversized evidence reference accepted")
	}
	for _, value := range []string{"https://bucket/evidence?versionId=v1", "s3:///evidence?versionId=v1", "s3://bucket/evidence#fragment?versionId=v1"} {
		if validActivationEvidenceRef(value) {
			t.Fatalf("invalid evidence reference %q accepted", value)
		}
	}
	if _, err := canonicalJSONWithoutField([]byte("{"), "signature"); !errors.Is(err, ErrActivationUnavailable) {
		t.Fatalf("canonical malformed JSON error=%v", err)
	}
	release := ActivationReleaseBinding{PolicyVersion: "privacy-v1", ExecutorVersion: SupportedExecutorVersion, PlanSchemaVersion: SupportedPlanSchemaVersion,
		ImageDigest: "sha256:" + strings.Repeat("a", sha256.Size*2), SchemaMigrationDigest: strings.Repeat("a", sha256.Size*2)}
	if _, err := VerifyRestoreActivationAttestation(nil, bytes.Repeat([]byte{1}, sha256.Size), release, time.Now()); !errors.Is(err, ErrActivationUnavailable) {
		t.Fatalf("empty restore attestation error=%v", err)
	}
	if _, err := VerifyRestoreActivationAttestation([]byte("{} {}"), bytes.Repeat([]byte{1}, sha256.Size), release, time.Now()); !errors.Is(err, ErrActivationUnavailable) {
		t.Fatalf("trailing restore attestation error=%v", err)
	}
}
