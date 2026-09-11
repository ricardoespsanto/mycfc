package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cfcoimbra/mycfc/internal/privacyrequests"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type brokerRowFake struct{ scan func(...any) error }

func (r brokerRowFake) Scan(destinations ...any) error { return r.scan(destinations...) }

type brokerDatabaseFake struct {
	row       pgx.Row
	closed    bool
	arguments []any
}

func (f *brokerDatabaseFake) QueryRow(_ context.Context, _ string, arguments ...any) pgx.Row {
	f.arguments = arguments
	return f.row
}

func (f *brokerDatabaseFake) Close() { f.closed = true }

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

func TestApprovalMaterialAndBrokerDispatch(t *testing.T) {
	release := testReleaseBinding()
	evidenceIDs := []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	setDigest := bytes.Repeat([]byte{0xa1}, 32)
	activationDigest := bytes.Repeat([]byte{0xb2}, 32)
	prepareDB := &brokerDatabaseFake{row: brokerRowFake{scan: func(destinations ...any) error {
		*destinations[0].(*[]uuid.UUID) = evidenceIDs
		*destinations[1].(*[]byte) = setDigest
		*destinations[2].(*[]byte) = activationDigest
		*destinations[3].(*string) = release.ExecutorVersion
		*destinations[4].(*string) = release.PlanSchemaVersion
		*destinations[5].(*string) = release.ImageDigest
		decoded, _ := hex.DecodeString(release.SchemaMigrationDigest)
		*destinations[6].(*[]byte) = decoded
		return nil
	}}}
	withBrokerDatabase(t, prepareDB)
	material, err := prepareApprovalMaterial(t.Context(), "postgres://broker.invalid/database", release)
	if err != nil {
		t.Fatal(err)
	}
	if !prepareDB.closed || !equalUUIDs(material.EvidenceIDs, evidenceIDs) || material.EvidenceSetSHA256 != hex.EncodeToString(setDigest) || material.ActivationSHA256 != hex.EncodeToString(activationDigest) {
		t.Fatalf("prepared material=%+v closed=%t", material, prepareDB.closed)
	}

	executorPublic, executorPrivate, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	adminPublic, adminPrivate, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	executorRaw, err := makeApproval(material, "EXECUTOR", "executor-v1", uuid.New(), executorPrivate, now)
	if err != nil {
		t.Fatal(err)
	}
	administratorRaw, err := makeApproval(material, "ADMINISTRATOR", "administrator-v1", uuid.New(), adminPrivate, now)
	if err != nil {
		t.Fatal(err)
	}
	approvalID := uuid.New()
	activateDB := &brokerDatabaseFake{row: brokerRowFake{scan: func(destinations ...any) error {
		*destinations[0].(*uuid.UUID) = approvalID
		return nil
	}}}
	withBrokerDatabase(t, activateDB)
	if err = activateApprovedMaterial(t.Context(), "postgres://broker.invalid/database", material, executorRaw, administratorRaw,
		"executor-v1", "administrator-v1", executorPublic, adminPublic, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if !activateDB.closed || len(activateDB.arguments) != 19 {
		t.Fatalf("activate closed=%t arguments=%d", activateDB.closed, len(activateDB.arguments))
	}
}

func TestApprovalValidationAndIOBoundaries(t *testing.T) {
	directory := t.TempDir()
	material := testApprovalMaterial()
	payload, err := json.Marshal(material)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "material.json")
	if err = os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadMaterial(path)
	if err != nil || loaded.ProposalID != material.ProposalID {
		t.Fatalf("loadMaterial=%+v error=%v", loaded, err)
	}
	if !validHexDigest(material.ActivationSHA256) || validHexDigest(strings.ToUpper(material.ActivationSHA256)) || validHexDigest("short") {
		t.Fatal("hex digest validation mismatch")
	}
	if equalUUIDs(material.EvidenceIDs, material.EvidenceIDs[:3]) || equalUUIDs(material.EvidenceIDs, append([]uuid.UUID{uuid.New()}, material.EvidenceIDs[1:]...)) {
		t.Fatal("unequal UUID sequences accepted")
	}
	output := filepath.Join(directory, "exclusive.json")
	if err = writeExclusive(output, []byte(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	if err = writeExclusive(output, []byte(`{"ok":false}`)); err == nil {
		t.Fatal("exclusive output was overwritten")
	}
	seed := bytes.Repeat([]byte{0x44}, ed25519.SeedSize)
	seedPath := filepath.Join(directory, "seed.key")
	if err = os.WriteFile(seedPath, []byte(base64.StdEncoding.EncodeToString(seed)), 0o600); err != nil {
		t.Fatal(err)
	}
	if key, keyErr := readPrivateKey(seedPath); keyErr != nil || len(key) != ed25519.PrivateKeySize {
		t.Fatalf("seed private key length=%d error=%v", len(key), keyErr)
	}
	if _, err = makeApproval(material, "REVIEWER", "key", uuid.New(), ed25519.NewKeyFromSeed(seed), time.Now()); err == nil {
		t.Fatal("invalid approval role accepted")
	}
	if _, _, err = verifyApproval([]byte(`{}`), material, "EXECUTOR", "key", make(ed25519.PublicKey, ed25519.PublicKeySize), time.Now()); err == nil {
		t.Fatal("invalid approval accepted")
	}
	if _, err = trustedRelease(func(string) string { return "" }); err == nil {
		t.Fatal("empty trusted release accepted")
	}
}

func withBrokerDatabase(t *testing.T, database *brokerDatabaseFake) {
	t.Helper()
	original := openActivationBrokerDatabase
	openActivationBrokerDatabase = func(context.Context, string) (activationBrokerDatabase, error) { return database, nil }
	t.Cleanup(func() { openActivationBrokerDatabase = original })
}

func testReleaseBinding() privacyrequests.ActivationReleaseBinding {
	return privacyrequests.ActivationReleaseBinding{PolicyVersion: "policy-v1", ExecutorVersion: privacyrequests.SupportedExecutorVersion,
		PlanSchemaVersion: privacyrequests.SupportedPlanSchemaVersion, ImageDigest: "sha256:" + digestHex('c'), SchemaMigrationDigest: digestHex('d')}
}

func testApprovalMaterial() approvalMaterial {
	release := testReleaseBinding()
	return approvalMaterial{Contract: approvalMaterialContract, ProposalID: uuid.New(), PolicyVersion: release.PolicyVersion,
		EvidenceIDs: []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()}, EvidenceSetSHA256: digestHex('a'), ActivationSHA256: digestHex('b'),
		ExecutorVersion: release.ExecutorVersion, PlanSchemaVersion: release.PlanSchemaVersion, ImageDigest: release.ImageDigest, SchemaMigrationDigest: release.SchemaMigrationDigest}
}

func digestHex(value byte) string { return string(bytes.Repeat([]byte{value}, 64)) }
