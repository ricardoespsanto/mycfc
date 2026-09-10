package main

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cfcoimbra/mycfc/internal/privacyrequests"
	"github.com/google/uuid"
	"golang.org/x/crypto/hkdf"
)

type replayEngineFake struct {
	results     []privacyrequests.TombstoneReplayResult
	err         error
	calls       int
	schemaCalls int
	recordCalls int
	closure     bool
}

var testReplayBindings = replayBindings{PolicyVersion: "policy-v1", ExecutorVersion: "executor-v1", PlanSchemaVersion: "plan-v1", ImageDigest: "sha256:" + strings.Repeat("a", 64)}

func (f *replayEngineFake) Replay(_ context.Context, authenticated privacyrequests.AuthenticatedReplayTombstone) (privacyrequests.TombstoneReplayResult, error) {
	f.calls++
	f.closure = authenticated.IsClosure()
	if f.err != nil {
		return privacyrequests.TombstoneReplayResult{}, f.err
	}
	return f.results[f.calls-1], nil
}

func (f *replayEngineFake) SchemaMigrationDigest(context.Context) (string, error) {
	f.schemaCalls++
	if f.err != nil {
		return "", f.err
	}
	return strings.Repeat("a", 64), nil
}

func (f *replayEngineFake) RecordAttestation(context.Context, replayAttestation, []uuid.UUID) error {
	f.recordCalls++
	return f.err
}

func replayFixture(t *testing.T) (*privacyrequests.TombstoneProtector, []byte, privacyrequests.RestoreTombstone) {
	t.Helper()
	privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	protector, err := privacyrequests.NewTombstoneProtector("restore-key-v2", privateKey.PublicKey().Bytes(), "locator-key-v2", bytes.Repeat([]byte{0x33}, 32))
	if err != nil {
		t.Fatal(err)
	}
	record := privacyrequests.RestoreTombstone{
		Version: privacyrequests.TombstoneRecordVersion, ExecutionID: uuid.New(), RequestID: uuid.New(), RequestRef: uuid.New(), SubjectUserID: uuid.New(),
		PlanSHA256: bytes.Repeat([]byte{0x11}, 32), WorksetSHA256: bytes.Repeat([]byte{0x22}, 32), ExecutionStart: time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC),
		Replay: &privacyrequests.RelationalReplayPrescription{Version: privacyrequests.TombstoneReplayVersion, ActionVersion: privacyrequests.SupportedActionVersion, Operations: []string{"AUTH_TOKEN_DELETE"}},
	}
	return protector, privateKey.Bytes(), record
}

func inventoryObject(sealed privacyrequests.SealedTombstone, keyByte byte) ledgerInventoryObject {
	writtenAt := time.Date(2026, 9, 10, 10, 0, 0, 123, time.UTC)
	retainUntil := (*time.Time)(nil)
	if !sealed.RetainUntil.IsZero() {
		value := sealed.RetainUntil
		retainUntil = &value
	}
	return ledgerInventoryObject{
		KeySHA256: hex.EncodeToString(bytes.Repeat([]byte{keyByte}, 32)), ObjectVersion: "immutable-version-" + string(rune('a'+keyByte)),
		CiphertextSHA256: hex.EncodeToString(sealed.SHA256), SizeBytes: int64(len(sealed.Encoded)), Payload: sealed.Encoded,
		WrittenAt: writtenAt, VerifiedAt: writtenAt.Add(time.Second), RetainUntil: retainUntil,
	}
}

func sealLegacyClosureV2ForTest(t *testing.T, privateKey []byte, record privacyrequests.RestoreTombstone) privacyrequests.SealedTombstone {
	t.Helper()
	const (
		algorithm    = "X25519-HKDF-SHA256-AES-256-GCM"
		envelopeV2   = "x25519-aes256gcm-hkdfsha256/v2"
		keyID        = "restore-key-v2"
		locatorKeyID = "locator-key-v2"
	)
	closedAt := record.ExecutionStart.Add(time.Hour)
	closure := privacyrequests.TombstoneClosure{
		Version: privacyrequests.TombstoneClosureVersionV2, Tombstone: record,
		ClosedAt: closedAt, EvidenceExpiresAt: closedAt.AddDate(0, 24, 0),
	}
	plaintext, err := json.Marshal(closure)
	if err != nil {
		t.Fatal(err)
	}
	private, err := ecdh.X25519().NewPrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	ephemeral, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := ephemeral.ECDH(private.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	locator := bytes.Repeat([]byte{0x77}, sha256.Size)
	aad := encodeTestFields("mycfc/restore-tombstone-aad/v2", "closure", envelopeV2, algorithm, keyID, locatorKeyID, hex.EncodeToString(locator))
	key := make([]byte, 32)
	if _, err = io.ReadFull(hkdf.New(sha256.New, shared, nil, aad), key); err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	envelope := privacyrequests.TombstoneEnvelope{
		Version: envelopeV2, Algorithm: algorithm, KeyID: keyID, Kind: "closure",
		LocatorKeyID: locatorKeyID, LocatorDigest: locator,
		Encapsulation: ephemeral.PublicKey().Bytes(), Nonce: nonce,
		Ciphertext: aead.Seal(nil, nonce, plaintext, aad),
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(encoded)
	return privacyrequests.SealedTombstone{
		Kind: "closure", LocatorKeyID: locatorKeyID, Locator: locator,
		Envelope: envelope, Encoded: encoded, SHA256: digest[:], RetainUntil: closure.EvidenceExpiresAt,
	}
}

func encodeTestFields(fields ...string) []byte {
	var encoded bytes.Buffer
	for _, field := range fields {
		if err := binary.Write(&encoded, binary.BigEndian, uint32(len(field))); err != nil {
			panic(err)
		}
		_, _ = encoded.WriteString(field)
	}
	return encoded.Bytes()
}

func TestExecuteReplayPrefersClosureAndEmitsOnlyNonIdentifyingCounts(t *testing.T) {
	protector, privateKey, record := replayFixture(t)
	intent, err := protector.Seal(record)
	if err != nil {
		t.Fatal(err)
	}
	closedAt := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	closure, err := protector.SealClosure(privacyrequests.TombstoneClosure{
		Version: privacyrequests.TombstoneClosureVersion, Tombstone: record, ClosedAt: closedAt, EvidenceExpiresAt: closedAt.AddDate(0, 24, 0), ErasureEffectiveAt: record.ExecutionStart,
	})
	if err != nil {
		t.Fatal(err)
	}
	legacyPayload, err := json.Marshal(privacyrequests.TombstoneEnvelope{
		Version: privacyrequests.TombstoneEnvelopeVersionV1, Algorithm: "X25519-HKDF-SHA256-AES-256-GCM", KeyID: "legacy-key-v1",
		Encapsulation: bytes.Repeat([]byte{0x51}, 32), Nonce: bytes.Repeat([]byte{0x52}, 12), Ciphertext: bytes.Repeat([]byte{0x53}, 16),
	})
	if err != nil {
		t.Fatal(err)
	}
	legacyDigest := sha256.Sum256(legacyPayload)
	// Closure-first ordering proves selection does not depend on inventory order.
	objects := []ledgerInventoryObject{inventoryObject(closure, 2), inventoryObject(intent, 1), {
		KeySHA256: strings.Repeat("f", 64), ObjectVersion: "legacy-version", CiphertextSHA256: hex.EncodeToString(legacyDigest[:]),
		SizeBytes: int64(len(legacyPayload)), Payload: legacyPayload, WrittenAt: closedAt, VerifiedAt: closedAt.Add(time.Second),
	}}
	digest, err := inventoryMetadataDigest(objects)
	if err != nil {
		t.Fatal(err)
	}
	engine := &replayEngineFake{results: []privacyrequests.TombstoneReplayResult{{RunID: uuid.New(), ClosureVersion: privacyrequests.TombstoneClosureVersion}}}
	attestation, err := executeReplay(t.Context(), ledgerInventory{Contract: ledgerInputContract, Source: "LIVE_LEDGER", InventorySHA256: digest, Objects: objects}, privateKey, engine, testReplayBindings)
	if err != nil {
		t.Fatal(err)
	}
	if attestation.Result != "SUCCEEDED" || attestation.ObjectCount != 3 || attestation.ImportedCount != 1 || attestation.ReplayedCount != 1 ||
		attestation.NonReplayableV1Count != 1 || attestation.AbsenceVerifiedCount != 1 || engine.calls != 1 || !engine.closure {
		t.Fatalf("attestation=%+v calls=%d closure=%t", attestation, engine.calls, engine.closure)
	}
	encoded, _ := json.Marshal(attestation)
	for _, identifier := range []string{record.ExecutionID.String(), record.SubjectUserID.String(), hex.EncodeToString(closure.Locator)} {
		if strings.Contains(string(encoded), identifier) {
			t.Fatalf("attestation exposed identifier")
		}
	}
}

func TestExecuteReplayDoesNotDowngradeTamperedV2ToLegacy(t *testing.T) {
	protector, privateKey, record := replayFixture(t)
	sealed, err := protector.Seal(record)
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if err = json.Unmarshal(sealed.Encoded, &envelope); err != nil {
		t.Fatal(err)
	}
	envelope["version"] = privacyrequests.TombstoneEnvelopeVersionV1
	tampered, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(tampered)
	object := inventoryObject(sealed, 4)
	object.Payload = tampered
	object.CiphertextSHA256 = hex.EncodeToString(digest[:])
	object.SizeBytes = int64(len(tampered))
	objects := []ledgerInventoryObject{object}
	inventoryDigest, err := inventoryMetadataDigest(objects)
	if err != nil {
		t.Fatal(err)
	}
	engine := &replayEngineFake{}
	if _, err = executeReplay(t.Context(), ledgerInventory{Contract: ledgerInputContract, Source: "LIVE_LEDGER", InventorySHA256: inventoryDigest, Objects: objects}, privateKey, engine, testReplayBindings); err == nil || engine.calls != 0 {
		t.Fatalf("downgraded envelope accepted: err=%v calls=%d", err, engine.calls)
	}
}

func TestExecuteReplayPreflightsMixedInventoryBeforeDatabaseMutation(t *testing.T) {
	protector, privateKey, currentRecord := replayFixture(t)
	closedAt := currentRecord.ExecutionStart.Add(time.Hour)
	currentClosure, err := protector.SealClosure(privacyrequests.TombstoneClosure{
		Version: privacyrequests.TombstoneClosureVersion, Tombstone: currentRecord,
		ClosedAt: closedAt, EvidenceExpiresAt: closedAt.AddDate(0, 24, 0), ErasureEffectiveAt: currentRecord.ExecutionStart,
	})
	if err != nil {
		t.Fatal(err)
	}
	legacyRecord := currentRecord
	legacyRecord.ExecutionID = uuid.New()
	legacyRecord.RequestID = uuid.New()
	legacyRecord.RequestRef = uuid.New()
	legacyRecord.SubjectUserID = uuid.New()
	legacyClosure := sealLegacyClosureV2ForTest(t, privateKey, legacyRecord)
	intentRecord := currentRecord
	intentRecord.ExecutionID = uuid.New()
	intentRecord.RequestID = uuid.New()
	intentRecord.RequestRef = uuid.New()
	intentRecord.SubjectUserID = uuid.New()
	intent, err := protector.Seal(intentRecord)
	if err != nil {
		t.Fatal(err)
	}
	objects := []ledgerInventoryObject{
		inventoryObject(currentClosure, 0x11),
		inventoryObject(legacyClosure, 0x12),
		inventoryObject(intent, 0x13),
	}
	digest, err := inventoryMetadataDigest(objects)
	if err != nil {
		t.Fatal(err)
	}
	engine := &replayEngineFake{}
	_, err = executeReplay(t.Context(), ledgerInventory{
		Contract: ledgerInputContract, Source: "LIVE_LEDGER", InventorySHA256: digest, Objects: objects,
	}, privateKey, engine, testReplayBindings)
	if err == nil {
		t.Fatal("mixed current, legacy-closure, and intent inventory was accepted")
	}
	if engine.calls != 0 || engine.schemaCalls != 0 || engine.recordCalls != 0 {
		t.Fatalf("database boundary crossed before whole-set preflight: replay=%d schema=%d record=%d", engine.calls, engine.schemaCalls, engine.recordCalls)
	}
}

func TestLedgerInventoryDigestIsMetadataOnlyCanonicalAndStrict(t *testing.T) {
	protector, _, record := replayFixture(t)
	sealed, err := protector.Seal(record)
	if err != nil {
		t.Fatal(err)
	}
	objects := []ledgerInventoryObject{inventoryObject(sealed, 9)}
	digest, err := inventoryMetadataDigest(objects)
	if err != nil {
		t.Fatal(err)
	}
	inventory := ledgerInventory{Contract: ledgerInputContract, Source: "LIVE_LEDGER", InventorySHA256: digest, Objects: objects}
	path := filepath.Join(t.TempDir(), "ledger.json")
	encoded, _ := json.Marshal(inventory)
	if err = os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = readLedgerInventory(path); err != nil {
		t.Fatal(err)
	}
	inventory.Objects[0].Payload[0] ^= 0xff
	encoded, _ = json.Marshal(inventory)
	if err = os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = readLedgerInventory(path); err == nil {
		t.Fatal("payload tampering passed ciphertext checksum verification")
	}
}

func TestCommandRequiresExplicitIsolationAndOwnerOnlyKey(t *testing.T) {
	if _, err := parseCommand([]string{"--ledger-input", "in", "--private-key-file", "key", "--attestation-output", "out"}); err == nil {
		t.Fatal("missing isolated restore mode accepted")
	}
	request, err := parseCommand([]string{"--isolated-restore", "--ledger-input", "in", "--private-key-file", "key", "--attestation-output", "out",
		"--policy-version", "policy-v1", "--executor-version", "executor-v1", "--plan-schema-version", "plan-v1", "--image-digest", "sha256:" + strings.Repeat("a", 64)})
	if err != nil || request.ledgerInput != "in" {
		t.Fatalf("request=%+v err=%v", request, err)
	}
	keyPath := filepath.Join(t.TempDir(), "key")
	key := bytes.Repeat([]byte{0x44}, 32)
	if err = os.WriteFile(keyPath, []byte(base64.StdEncoding.EncodeToString(key)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = readPrivateKey(keyPath); err == nil {
		t.Fatal("group/world-readable key accepted")
	}
	if err = os.Chmod(keyPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if decoded, err := readPrivateKey(keyPath); err != nil || !bytes.Equal(decoded, key) {
		t.Fatalf("decoded=%x err=%v", decoded, err)
	}
}

func TestSchemaMigrationDigestHasNoTrailingNewline(t *testing.T) {
	versions := []string{
		"202609100006_privacy_restore_ledger",
		"202609100007_retention_maintenance",
		"202609100008_privacy_tombstone_replay",
	}
	if got, want := schemaMigrationDigest(versions), "69c291cba88ab9b3dfe1278b14979adb337b047fed1c22209fa7bcb447d4f8e4"; got != want {
		t.Fatalf("digest=%s want=%s", got, want)
	}
}

func TestExecuteReplayFailsClosedOnDatabaseFailure(t *testing.T) {
	protector, privateKey, record := replayFixture(t)
	sealed, err := protector.Seal(record)
	if err != nil {
		t.Fatal(err)
	}
	objects := []ledgerInventoryObject{inventoryObject(sealed, 3)}
	digest, _ := inventoryMetadataDigest(objects)
	engine := &replayEngineFake{err: errors.New("database details must remain opaque")}
	if _, err = executeReplay(t.Context(), ledgerInventory{Contract: ledgerInputContract, Source: "LIVE_LEDGER", InventorySHA256: digest, Objects: objects}, privateKey, engine, testReplayBindings); err == nil || strings.Contains(err.Error(), "database details") {
		t.Fatalf("error=%v", err)
	}
}
