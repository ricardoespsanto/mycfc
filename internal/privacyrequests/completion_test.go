package privacyrequests

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/hmac"
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
			"replay_result": "mycfc/privacy-restore-replay-result/v2", "replay": "relational-erasure-replay/v1", "closure": "restore-tombstone-closure/v3", "synthetic_fixture": "mycfc/privacy-restore-synthetic-fixture/v1"},
		"backup": map[string]any{"created_at": now.Add(-24 * time.Hour).Format(time.RFC3339), "manifest": immutableObject("manifest.json"), "dump": immutableObject("database.dump")},
		"ledger": map[string]any{"input_source": "LIVE_LEDGER", "inventory_sha256": hexDigest, "object_count": 2},
		"candidate": map[string]any{"result_sha256": hexDigest, "object_count": 2, "imported_count": 1, "replayed_count": 2, "already_applied_count": 1,
			"non_replayable_v1_count": 0, "absence_verified_count": 2, "synthetic_replayed_count": 0, "closure_v3_count": 2, "intent_only_count": 0,
			"legacy_closure_v2_count": 0, "erasure_effective_at_verified_count": 2, "failed_count": 0},
		"observer": map[string]any{"image_digest": "sha256:" + hexDigest, "replay_count": 2, "source_already_applied_count": 1, "synthetic_count": 0,
			"verified_run_count": 2, "expected_checkpoint_count": 4, "succeeded_checkpoint_count": 4, "provider_absent_count": 2,
			"consent_clock_verified_count": 2, "closure_v3_count": 2, "erasure_effective_at_verified_count": 2, "evidence_sha256": hexDigest},
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
		"contract": "mycfc/privacy-provider-registry/v1", "result": "SUCCEEDED", "observed_at": now.Add(-time.Hour).Format(time.RFC3339),
		"policy_version": "privacy-v1", "executor_version": "privacy-erasure-executor/v2", "plan_schema_version": "privacy-erasure-plan/v2",
		"image_digest": "sha256:" + hexDigest, "evidence_ref": "s3://evidence/provider.json?versionId=version-1", "evidence_sha256": hexDigest,
		"signing_key_id": "activation-key-1", "registry_state": "READY", "registration_count": 1, "provider_registry_sha256": hexDigest,
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
	document["registry_state"] = "EMPTY"
	canonical, _ = json.Marshal(mapWithoutSignature(document))
	document["signature_ed25519"] = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, canonical))
	payload, _ = json.Marshal(document)
	if _, err = VerifyActivationArtifact(payload, map[string]ed25519.PublicKey{"activation-key-1": publicKey}, release, now); !errors.Is(err, ErrActivationUnavailable) {
		t.Fatalf("empty provider registry error=%v", err)
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
