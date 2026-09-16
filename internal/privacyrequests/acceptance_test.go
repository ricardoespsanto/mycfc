package privacyrequests

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestAcceptanceEvidenceSignatureBindsExactPrivateSafeContract(t *testing.T) {
	public, key, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now().UTC()
	digest := strings.Repeat("a", 64)
	value := AcceptanceEvidence{Contract: AcceptanceContract, Mode: "run", Outcome: "COMPLETED", ImageDigest: "sha256:" + digest, SchemaDigest: digest, FixtureSHA256: digest, ManifestSHA256: digest, PolicySHA256: digest, StartedAt: now, ObservedAt: now, SimulatedNotices: 2, Checkpoints: 31, Conditions: []string{}}
	signed, err := SignAcceptanceEvidence(value, "test-key", key)
	if err != nil {
		t.Fatal(err)
	}
	signature, err := base64.StdEncoding.DecodeString(signed.Signature)
	if err != nil {
		t.Fatal(err)
	}
	message := append([]byte(AcceptanceContract+"\x00test-key\x00"), signed.Payload...)
	if !ed25519.Verify(public, message, signature) {
		t.Fatal("invalid signature")
	}
	sha := sha256.Sum256(signed.Payload)
	if signed.PayloadSHA256 != hex.EncodeToString(sha[:]) {
		t.Fatal("digest mismatch")
	}
	message[len(message)-2] ^= 1
	if ed25519.Verify(public, message, signature) {
		t.Fatal("tampered payload verified")
	}
	encoded, _ := json.Marshal(signed)
	for _, private := range []string{"subject_ref", "request_id", "worker_ref", "password", "proof", "email", "database_url"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("evidence leaks field %s", private)
		}
	}
	for _, mutation := range []func(*AcceptanceEvidence){func(v *AcceptanceEvidence) { v.Conditions = []string{"person@example.test"} }, func(v *AcceptanceEvidence) { v.Outcome = "SUCCEEDED" }, func(v *AcceptanceEvidence) { v.ManifestSHA256 = "" }, func(v *AcceptanceEvidence) { v.Mode = "person-id" }, func(v *AcceptanceEvidence) { v.PolicySHA256 = "private text" }} {
		invalid := value
		mutation(&invalid)
		if _, err = SignAcceptanceEvidence(invalid, "test-key", key); err == nil {
			t.Fatal("invalid evidence signed")
		}
	}
	terminal := value
	terminal.Mode = "canary-failure"
	terminal.Outcome = "CANARY_VERIFIED"
	terminal.Conditions = []string{"failure"}
	terminal.ManifestSHA256 = ""
	terminal.SimulatedNotices = 0
	terminal.Checkpoints = 0
	if _, err = SignAcceptanceEvidence(terminal, "test-key", key); err != nil {
		t.Fatal(err)
	}
	terminal.Outcome = "COMPLETED"
	terminal.ManifestSHA256 = digest
	terminal.SimulatedNotices = 1
	terminal.Checkpoints = 1
	if _, err = SignAcceptanceEvidence(terminal, "test-key", key); err == nil {
		t.Fatal("terminal canary signed as completed acceptance")
	}
	malformed := append([]byte(nil), key...)
	malformed[63] ^= 1
	if _, err = SignAcceptanceEvidence(value, "test-key", malformed); err == nil {
		t.Fatal("inconsistent private key accepted")
	}
}
func TestAcceptanceFailClosedWithoutAnyIdentityInputs(t *testing.T) {
	for _, mode := range []string{"run", "canary-retry", "canary-failure", "canary-aged", "canary-heartbeat", "canary-recovery", "existing-person"} {
		if _, err := RunSyntheticAcceptance(context.Background(), AcceptanceOptions{Mode: mode}); err == nil {
			t.Fatal("unbound acceptance succeeded")
		}
	}
	if _, err := (acceptanceNoObjects{}).DeleteAllVersions(context.Background(), "real/member/key"); err == nil {
		t.Fatal("unexpected live object target allowed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := acceptanceWait(ctx, time.Hour); err == nil {
		t.Fatal("cancelled canary wait continued")
	}
}
