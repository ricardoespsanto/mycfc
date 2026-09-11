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
	results      []privacyrequests.TombstoneReplayResult
	err          error
	replayErr    error
	schemaErr    error
	recordErr    error
	schemaDigest string
	calls        int
	schemaCalls  int
	recordCalls  int
	closure      bool
}

func TestMembershipPostconditionSetDigestIsOrderStableAndBindsAllDigests(t *testing.T) {
	first := privacyrequests.MembershipHistoryPostcondition{Contract: privacyrequests.MembershipHistoryPostconditionVersion,
		SHA256: bytes.Repeat([]byte{0x11}, sha256.Size)}
	second := privacyrequests.MembershipHistoryPostcondition{Contract: privacyrequests.MembershipHistoryPostconditionVersion,
		SHA256: bytes.Repeat([]byte{0x22}, sha256.Size)}
	forward := membershipPostconditionSetDigest([]privacyrequests.MembershipHistoryPostcondition{first, second})
	reversed := membershipPostconditionSetDigest([]privacyrequests.MembershipHistoryPostcondition{second, first})
	if forward == "" || forward != reversed {
		t.Fatalf("membership set digest is not stable: forward=%q reversed=%q", forward, reversed)
	}
	second.SHA256[0] ^= 0xff
	if changed := membershipPostconditionSetDigest([]privacyrequests.MembershipHistoryPostcondition{first, second}); changed == forward {
		t.Fatal("membership set digest did not bind a changed row digest")
	}
}

var testReplayBindings = replayBindings{PolicyVersion: "policy-v1", ExecutorVersion: "executor-v1", PlanSchemaVersion: "plan-v1", ImageDigest: "sha256:" + strings.Repeat("a", 64)}

func (f *replayEngineFake) Replay(_ context.Context, authenticated privacyrequests.AuthenticatedReplayTombstone) (privacyrequests.TombstoneReplayResult, error) {
	f.calls++
	f.closure = authenticated.IsClosure()
	if f.replayErr != nil {
		return privacyrequests.TombstoneReplayResult{}, f.replayErr
	}
	if f.err != nil {
		return privacyrequests.TombstoneReplayResult{}, f.err
	}
	return f.results[f.calls-1], nil
}

func (f *replayEngineFake) SchemaMigrationDigest(context.Context) (string, error) {
	f.schemaCalls++
	if f.schemaErr != nil {
		return "", f.schemaErr
	}
	if f.err != nil {
		return "", f.err
	}
	if f.schemaDigest != "" {
		return f.schemaDigest, nil
	}
	return strings.Repeat("a", 64), nil
}

func (f *replayEngineFake) RecordAttestation(context.Context, replayAttestation, []uuid.UUID) error {
	f.recordCalls++
	if f.recordErr != nil {
		return f.recordErr
	}
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

func currentClosureRecord(record privacyrequests.RestoreTombstone) privacyrequests.RestoreTombstone {
	copy := record
	replay := *record.Replay
	replay.Operations = append([]string(nil), record.Replay.Operations...)
	replay.MembershipHistoryPostcondition = &privacyrequests.MembershipHistoryPostcondition{
		Contract: privacyrequests.MembershipHistoryPostconditionVersion, SHA256: bytes.Repeat([]byte{0x44}, 32), MembershipCount: 1, VariationCount: 2,
	}
	copy.Replay = &replay
	return copy
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

func sealLegacyClosureForTest(t *testing.T, privateKey []byte, record privacyrequests.RestoreTombstone, version string) privacyrequests.SealedTombstone {
	t.Helper()
	const (
		algorithm    = "X25519-HKDF-SHA256-AES-256-GCM"
		envelopeV2   = "x25519-aes256gcm-hkdfsha256/v2"
		keyID        = "restore-key-v2"
		locatorKeyID = "locator-key-v2"
	)
	closedAt := record.ExecutionStart.Add(time.Hour)
	closure := privacyrequests.TombstoneClosure{
		Version: version, Tombstone: record,
		ClosedAt: closedAt, EvidenceExpiresAt: closedAt.AddDate(0, 24, 0),
	}
	if version == privacyrequests.TombstoneClosureVersionV3 {
		closure.ErasureEffectiveAt = record.ExecutionStart
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
		Version: privacyrequests.TombstoneClosureVersion, Tombstone: currentClosureRecord(record), ClosedAt: closedAt, EvidenceExpiresAt: closedAt.AddDate(0, 24, 0), ErasureEffectiveAt: record.ExecutionStart,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Closure-first ordering proves selection does not depend on inventory order.
	objects := []ledgerInventoryObject{inventoryObject(closure, 2), inventoryObject(intent, 1)}
	digest, err := inventoryMetadataDigest(objects)
	if err != nil {
		t.Fatal(err)
	}
	engine := &replayEngineFake{results: []privacyrequests.TombstoneReplayResult{{RunID: uuid.New(), ClosureVersion: privacyrequests.TombstoneClosureVersion,
		MembershipHistoryPostcondition: *currentClosureRecord(record).Replay.MembershipHistoryPostcondition}}}
	attestation, err := executeReplay(t.Context(), ledgerInventory{Contract: ledgerInputContract, Source: "LIVE_LEDGER", InventorySHA256: digest, Objects: objects}, privateKey, engine, testReplayBindings)
	if err != nil {
		t.Fatal(err)
	}
	if attestation.Result != "SUCCEEDED" || attestation.ObjectCount != 2 || attestation.ImportedCount != 1 || attestation.ReplayedCount != 1 ||
		attestation.NonReplayableV1Count != 0 || attestation.AbsenceVerifiedCount != 1 || engine.calls != 1 || !engine.closure {
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
		Version: privacyrequests.TombstoneClosureVersion, Tombstone: currentClosureRecord(currentRecord),
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
	legacyClosure := sealLegacyClosureForTest(t, privateKey, legacyRecord, privacyrequests.TombstoneClosureVersionV2)
	legacyV3Record := currentRecord
	legacyV3Record.ExecutionID = uuid.New()
	legacyV3Record.RequestID = uuid.New()
	legacyV3Record.RequestRef = uuid.New()
	legacyV3Record.SubjectUserID = uuid.New()
	legacyV3Closure := sealLegacyClosureForTest(t, privateKey, legacyV3Record, privacyrequests.TombstoneClosureVersionV3)
	intentRecord := currentRecord
	intentRecord.ExecutionID = uuid.New()
	intentRecord.RequestID = uuid.New()
	intentRecord.RequestRef = uuid.New()
	intentRecord.SubjectUserID = uuid.New()
	intent, err := protector.Seal(intentRecord)
	if err != nil {
		t.Fatal(err)
	}
	legacyV1Payload, err := json.Marshal(privacyrequests.TombstoneEnvelope{
		Version: privacyrequests.TombstoneEnvelopeVersionV1, Algorithm: "X25519-HKDF-SHA256-AES-256-GCM", KeyID: "legacy-key-v1",
		Encapsulation: bytes.Repeat([]byte{0x51}, 32), Nonce: bytes.Repeat([]byte{0x52}, 12), Ciphertext: bytes.Repeat([]byte{0x53}, 16),
	})
	if err != nil {
		t.Fatal(err)
	}
	legacyV1Digest := sha256.Sum256(legacyV1Payload)
	objects := []ledgerInventoryObject{
		inventoryObject(currentClosure, 0x11),
		inventoryObject(legacyClosure, 0x12),
		inventoryObject(legacyV3Closure, 0x13),
		inventoryObject(intent, 0x14),
		{KeySHA256: strings.Repeat("f", 64), ObjectVersion: "legacy-version", CiphertextSHA256: hex.EncodeToString(legacyV1Digest[:]),
			SizeBytes: int64(len(legacyV1Payload)), Payload: legacyV1Payload, WrittenAt: closedAt, VerifiedAt: closedAt.Add(time.Second)},
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
	second := objects[0]
	second.ObjectVersion = "another-version"
	if _, err = inventoryMetadataDigest([]ledgerInventoryObject{objects[0], second}); err != nil {
		t.Fatalf("same key with distinct immutable versions rejected: %v", err)
	}
	if _, err = inventoryMetadataDigest([]ledgerInventoryObject{objects[0], objects[0]}); err == nil {
		t.Fatal("duplicate ledger object accepted")
	}
}

func TestCommandRequiresExplicitIsolationAndOwnerOnlyKey(t *testing.T) {
	if _, err := parseCommand([]string{"--ledger-input", "in", "--private-key-file", "key", "--attestation-output", "out"}); err == nil {
		t.Fatal("missing isolated restore mode accepted")
	}
	if _, err := parseCommand([]string{"--isolated-restore", "--private-key-file", "key", "--bootstrap-synthetic-fixture"}); err == nil {
		t.Fatal("synthetic bootstrap without output accepted")
	}
	if _, err := parseCommand([]string{"--isolated-restore", "--private-key-file", "key"}); err == nil {
		t.Fatal("normal replay without input and attestation accepted")
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

func TestExecuteReplayFailureBoundariesAfterCurrentClosurePreflight(t *testing.T) {
	inventory, privateKey, validResult := currentReplayInventory(t)
	if _, err := executeReplay(t.Context(), inventory, privateKey, &replayEngineFake{}, replayBindings{}); err == nil {
		t.Fatal("invalid release bindings accepted")
	}
	syntheticMismatch := inventory
	syntheticMismatch.Source = "SYNTHETIC_BOOTSTRAP"
	if _, err := executeReplay(t.Context(), syntheticMismatch, privateKey, &replayEngineFake{}, testReplayBindings); err == nil {
		t.Fatal("synthetic source accepted a live closure")
	}
	tests := []struct {
		name   string
		engine *replayEngineFake
	}{
		{"replay", &replayEngineFake{results: []privacyrequests.TombstoneReplayResult{validResult}, replayErr: errors.New("unavailable")}},
		{"postcondition", &replayEngineFake{results: []privacyrequests.TombstoneReplayResult{{RunID: uuid.New(), ClosureVersion: privacyrequests.TombstoneClosureVersion}}}},
		{"schema error", &replayEngineFake{results: []privacyrequests.TombstoneReplayResult{validResult}, schemaErr: errors.New("unavailable")}},
		{"schema digest", &replayEngineFake{results: []privacyrequests.TombstoneReplayResult{validResult}, schemaDigest: "invalid"}},
		{"record", &replayEngineFake{results: []privacyrequests.TombstoneReplayResult{validResult}, recordErr: errors.New("unavailable")}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := executeReplay(t.Context(), inventory, privateKey, test.engine, testReplayBindings); err == nil {
				t.Fatal("failure boundary accepted")
			}
		})
	}
}

func TestExecuteReplayRejectsLegacyOnlyAndInvalidResultVersions(t *testing.T) {
	_, privateKey, _ := currentReplayInventory(t)
	legacyPayload, err := json.Marshal(privacyrequests.TombstoneEnvelope{
		Version: privacyrequests.TombstoneEnvelopeVersionV1, Algorithm: "X25519-HKDF-SHA256-AES-256-GCM", KeyID: "legacy-key-v1",
		Encapsulation: bytes.Repeat([]byte{0x51}, 32), Nonce: bytes.Repeat([]byte{0x52}, 12), Ciphertext: bytes.Repeat([]byte{0x53}, 16),
	})
	if err != nil {
		t.Fatal(err)
	}
	legacyDigest := sha256.Sum256(legacyPayload)
	legacyObject := ledgerInventoryObject{KeySHA256: strings.Repeat("f", 64), ObjectVersion: "legacy-version", CiphertextSHA256: hex.EncodeToString(legacyDigest[:]),
		SizeBytes: int64(len(legacyPayload)), Payload: legacyPayload, WrittenAt: time.Now().UTC(), VerifiedAt: time.Now().UTC()}
	legacyInventory := ledgerInventory{Contract: ledgerInputContract, Source: "LIVE_LEDGER", Objects: []ledgerInventoryObject{legacyObject}}
	if _, err = executeReplay(t.Context(), legacyInventory, privateKey, &replayEngineFake{}, testReplayBindings); err == nil {
		t.Fatal("legacy-only inventory accepted")
	}

	inventory, privateKey, result := currentReplayInventory(t)
	for name, closureVersion := range map[string]string{"legacy closure": privacyrequests.TombstoneClosureVersionV2, "intent result": "intent"} {
		t.Run(name, func(t *testing.T) {
			changed := result
			changed.ClosureVersion = closureVersion
			if _, replayErr := executeReplay(t.Context(), inventory, privateKey, &replayEngineFake{results: []privacyrequests.TombstoneReplayResult{changed}}, testReplayBindings); replayErr == nil {
				t.Fatal("non-current replay result accepted")
			}
		})
	}
	result.AlreadyApplied = true
	attestation, err := executeReplay(t.Context(), inventory, privateKey, &replayEngineFake{results: []privacyrequests.TombstoneReplayResult{result}}, testReplayBindings)
	if err != nil || attestation.AlreadyAppliedCount != 1 || attestation.ImportedCount != 0 {
		t.Fatalf("already-applied attestation=%+v error=%v", attestation, err)
	}
}

func TestReplayInputOutputAndCommandFailureBoundaries(t *testing.T) {
	inventory, privateKey, _ := currentReplayInventory(t)
	directory := t.TempDir()
	ledgerPath := filepath.Join(directory, "ledger.json")
	if err := writeLedgerInventory(ledgerPath, inventory); err != nil {
		t.Fatal(err)
	}
	loaded, err := readLedgerInventory(ledgerPath)
	if err != nil || loaded.InventorySHA256 != inventory.InventorySHA256 {
		t.Fatalf("loaded inventory=%+v error=%v", loaded, err)
	}
	if err = writeLedgerInventory(ledgerPath, inventory); err == nil {
		t.Fatal("ledger output was overwritten")
	}
	attestationPath := filepath.Join(directory, "attestation.json")
	attestation := replayAttestation{Contract: replayResultContract, Result: "SUCCEEDED", InventorySHA256: inventory.InventorySHA256}
	if err = writeAttestation(attestationPath, attestation); err != nil {
		t.Fatal(err)
	}
	if err = writeAttestation(attestationPath, attestation); err == nil {
		t.Fatal("attestation output was overwritten")
	}

	for name, payload := range map[string][]byte{
		"empty":    nil,
		"trailing": append(mustJSON(t, inventory), []byte(` {}`)...),
		"digest": func() []byte {
			changed := inventory
			changed.InventorySHA256 = strings.Repeat("0", 64)
			return mustJSON(t, changed)
		}(),
		"metadata": func() []byte {
			changed := inventory
			changed.Objects = append([]ledgerInventoryObject(nil), inventory.Objects...)
			changed.Objects[0].ObjectVersion = ""
			changed.InventorySHA256, _ = inventoryMetadataDigest(changed.Objects)
			return mustJSON(t, changed)
		}(),
	} {
		path := filepath.Join(directory, name+".json")
		if err = os.WriteFile(path, payload, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err = readLedgerInventory(path); err == nil {
			t.Fatalf("invalid ledger input accepted: %s", name)
		}
	}
	if _, err = readLedgerInventory(filepath.Join(directory, "missing.json")); err == nil {
		t.Fatal("missing ledger input accepted")
	}
	badKey := filepath.Join(directory, "bad.key")
	if err = os.WriteFile(badKey, []byte("not-base64"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = readPrivateKey(badKey); err == nil {
		t.Fatal("invalid private key accepted")
	}
	if policyValue("") || policyValue("bad value") || !policyValue("policy-v1") {
		t.Fatal("policy value validation mismatch")
	}

	if err = run(t.Context(), nil); err == nil {
		t.Fatal("invalid command accepted")
	}
	missingKeyArgs := []string{"--isolated-restore", "--ledger-input", ledgerPath, "--private-key-file", filepath.Join(directory, "missing-private.key"),
		"--attestation-output", filepath.Join(directory, "missing-key-attestation.json"), "--policy-version", "policy-v1", "--executor-version", "executor-v1",
		"--plan-schema-version", "plan-v1", "--image-digest", "sha256:" + strings.Repeat("a", 64)}
	if err = run(t.Context(), missingKeyArgs); err == nil {
		t.Fatal("missing private replay key accepted")
	}
	if err = (databaseReplayEngine{}).RecordAttestation(t.Context(), replayAttestation{InventorySHA256: "invalid"}, nil); err == nil {
		t.Fatal("invalid attestation inventory digest accepted")
	}
	keyPath := filepath.Join(directory, "private.key")
	if err = os.WriteFile(keyPath, []byte(base64.StdEncoding.EncodeToString(privateKey)), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--isolated-restore", "--ledger-input", ledgerPath, "--private-key-file", keyPath, "--attestation-output", filepath.Join(directory, "run-attestation.json"),
		"--policy-version", "policy-v1", "--executor-version", "executor-v1", "--plan-schema-version", "plan-v1", "--image-digest", "sha256:" + strings.Repeat("a", 64)}
	t.Setenv("DATABASE_URL", "")
	if err = run(t.Context(), args); err == nil {
		t.Fatal("missing database URL accepted")
	}
	t.Setenv("DATABASE_URL", "://invalid")
	if err = run(t.Context(), args); err == nil {
		t.Fatal("invalid database URL accepted")
	}
}

func currentReplayInventory(t *testing.T) (ledgerInventory, []byte, privacyrequests.TombstoneReplayResult) {
	t.Helper()
	protector, privateKey, record := replayFixture(t)
	record = currentClosureRecord(record)
	closedAt := record.ExecutionStart.Add(time.Hour)
	closure, err := protector.SealClosure(privacyrequests.TombstoneClosure{Version: privacyrequests.TombstoneClosureVersion, Tombstone: record,
		ClosedAt: closedAt, EvidenceExpiresAt: closedAt.AddDate(0, 24, 0), ErasureEffectiveAt: record.ExecutionStart})
	if err != nil {
		t.Fatal(err)
	}
	objects := []ledgerInventoryObject{inventoryObject(closure, 0x31)}
	digest, err := inventoryMetadataDigest(objects)
	if err != nil {
		t.Fatal(err)
	}
	result := privacyrequests.TombstoneReplayResult{RunID: uuid.New(), ClosureVersion: privacyrequests.TombstoneClosureVersion,
		MembershipHistoryPostcondition: *record.Replay.MembershipHistoryPostcondition}
	return ledgerInventory{Contract: ledgerInputContract, Source: "LIVE_LEDGER", InventorySHA256: digest, Objects: objects}, privateKey, result
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}
