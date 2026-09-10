package privacyrequests

import (
	"bytes"
	"context"
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
	document := map[string]any{
		"contract": "mycfc/privacy-restore-drill-attestation/v1", "result": "SUCCEEDED",
		"completed_at": now.Add(-time.Hour).Format(time.RFC3339), "valid_until": now.Add(89 * 24 * time.Hour).Format(time.RFC3339),
		"image_digest": "sha256:" + hexDigest, "schema_migration_digest": hexDigest,
		"backup": map[string]any{"created_at": now.Add(-24 * time.Hour).Format(time.RFC3339), "manifest_key_sha256": hexDigest,
			"manifest_version": "version-1", "manifest_sha256": hexDigest, "dump_key_sha256": hexDigest, "dump_version": "version-2", "dump_sha256": hexDigest},
		"ledger": map[string]any{"inventory_sha256": hexDigest, "object_count": 2},
		"replay": map[string]any{"imported_count": 1, "replayed_count": 2, "already_applied_count": 1, "absence_verified_count": 2},
	}
	canonical, _ := json.Marshal(document)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(canonical)
	document["auth_hmac_sha256"] = hex.EncodeToString(mac.Sum(nil))
	payload, _ := json.Marshal(document)
	evidence, err := VerifyRestoreActivationAttestation(payload, key, now)
	if err != nil || evidence.kind != "RESTORE" || evidence.reference != "mycfc/privacy-restore-drill-attestation/v1" || len(evidence.digest) != sha256.Size || !evidence.observedAt.Equal(now.Add(-time.Hour)) {
		t.Fatalf("evidence=%+v err=%v", evidence, err)
	}
	tampered := append([]byte(nil), payload...)
	tampered[len(tampered)-2] ^= 1
	if _, err = VerifyRestoreActivationAttestation(tampered, key, now); !errors.Is(err, ErrActivationUnavailable) {
		t.Fatalf("tampered attestation error=%v", err)
	}
	if _, err = VerifyRestoreActivationAttestation(payload, bytes.Repeat([]byte{5}, sha256.Size), now); !errors.Is(err, ErrActivationUnavailable) {
		t.Fatalf("wrong authentication key error=%v", err)
	}
	document["image_digest"] = hexDigest
	canonical, _ = json.Marshal(mapWithoutAuthentication(document))
	mac.Reset()
	_, _ = mac.Write(canonical)
	document["auth_hmac_sha256"] = hex.EncodeToString(mac.Sum(nil))
	payload, _ = json.Marshal(document)
	if _, err = VerifyRestoreActivationAttestation(payload, key, now); !errors.Is(err, ErrActivationUnavailable) {
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
	payload := []byte(`{"contract":"mycfc/privacy-provider-registry/v1","result":"SUCCEEDED","observed_at":"` + now.Add(-time.Hour).Format(time.RFC3339) + `","providers":[]}`)
	evidence, err := VerifyActivationArtifact("PROVIDER", "mycfc/privacy-provider-registry/v1", payload, now.Add(-time.Hour), now)
	want := sha256.Sum256(payload)
	if err != nil || evidence.kind != "PROVIDER" || !bytes.Equal(evidence.digest, want[:]) {
		t.Fatalf("evidence=%+v err=%v", evidence, err)
	}
	if _, err = VerifyActivationArtifact("RESTORE", "mycfc/privacy-restore-drill-attestation/v1", payload, now, now); !errors.Is(err, ErrActivationUnavailable) {
		t.Fatalf("unsigned restore artifact error=%v", err)
	}
	if _, err = VerifyActivationArtifact("PROVIDER", "mycfc/privacy-provider-registry/v1", payload, now.Add(-2*time.Hour), now); !errors.Is(err, ErrActivationUnavailable) {
		t.Fatalf("caller observation mismatch error=%v", err)
	}
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
