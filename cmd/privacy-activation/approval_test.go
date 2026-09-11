package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestReadPrivateKeyRequiresPrivateSecretFile(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "approval-key")
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(privateKey)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivateKey(path); err != nil {
		t.Fatalf("private approval key rejected: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivateKey(path); err == nil {
		t.Fatal("world-readable private approval key accepted")
	}
}

func TestApprovalEnvelopeIsCanonicalSignedAndReleaseBound(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 10, 20, 0, 0, 0, time.UTC)
	material := approvalMaterial{Contract: approvalMaterialContract, ProposalID: uuid.New(), PolicyVersion: "policy-v1",
		EvidenceIDs: []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()}, EvidenceSetSHA256: digestHex('a'), ActivationSHA256: digestHex('b'),
		ExecutorVersion: "privacy-erasure-executor/v2", PlanSchemaVersion: "privacy-erasure-plan/v2", ImageDigest: "sha256:" + digestHex('c'), SchemaMigrationDigest: digestHex('d')}
	raw, err := makeApproval(material, "EXECUTOR", "executor-key-v1", uuid.New(), privateKey, now)
	if err != nil {
		t.Fatal(err)
	}
	envelope, nonce, err := verifyApproval(raw, material, "EXECUTOR", "executor-key-v1", publicKey, now.Add(time.Minute))
	if err != nil || envelope.Role != "EXECUTOR" || len(nonce) != 32 {
		t.Fatalf("verify error=%v envelope=%+v nonce=%d", err, envelope, len(nonce))
	}

	var changed map[string]any
	if err = json.Unmarshal(raw, &changed); err != nil {
		t.Fatal(err)
	}
	changed["expires_at"] = now.Add(30 * time.Minute).Format(time.RFC3339)
	tampered, _ := json.Marshal(changed)
	if _, _, err = verifyApproval(tampered, material, "EXECUTOR", "executor-key-v1", publicKey, now); err == nil {
		t.Fatal("tampered expiry accepted")
	}
	changed["expires_at"] = now.Add(15 * time.Minute).Format(time.RFC3339)
	changed["unexpected"] = true
	extra, _ := json.Marshal(changed)
	if _, _, err = verifyApproval(extra, material, "EXECUTOR", "executor-key-v1", publicKey, now); err == nil {
		t.Fatal("extra field accepted")
	}
	if _, _, err = verifyApproval(append([]byte(" "), raw...), material, "EXECUTOR", "executor-key-v1", publicKey, now); err == nil {
		t.Fatal("non-canonical whitespace accepted")
	}
	wrong := material
	wrong.ImageDigest = "sha256:" + digestHex('e')
	if _, _, err = verifyApproval(raw, wrong, "EXECUTOR", "executor-key-v1", publicKey, now); err == nil {
		t.Fatal("wrong current release accepted")
	}
	if bytes.Contains(raw, privateKey) {
		t.Fatal("private key appeared in approval")
	}
}

func digestHex(value byte) string { return string(bytes.Repeat([]byte{value}, 64)) }
