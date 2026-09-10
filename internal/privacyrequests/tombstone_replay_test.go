package privacyrequests

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/hkdf"
)

func listedTombstoneFixture(sealed SealedTombstone) ListedTombstoneObject {
	writtenAt := time.Date(2026, time.September, 10, 10, 0, 0, 0, time.UTC)
	return ListedTombstoneObject{
		Kind: sealed.Kind, LocatorKeyID: sealed.LocatorKeyID, LocatorDigest: bytes.Clone(sealed.Locator),
		Payload: bytes.Clone(sealed.Encoded), Checksum: bytes.Clone(sealed.SHA256), ObjectVersion: "immutable-version",
		WrittenAt: writtenAt, VerifiedAt: writtenAt.Add(time.Second), RetainUntil: sealed.RetainUntil,
	}
}

func TestReplayAuthenticationDiscoversV2IdentityFromOpaqueLocator(t *testing.T) {
	protector, privateKey := tombstoneProtectorFixture(t)
	record := tombstoneFixture()
	sealed, err := protector.Seal(record)
	if err != nil {
		t.Fatal(err)
	}
	listed := listedTombstoneFixture(sealed)
	authenticated, err := AuthenticateReplayTombstone(privateKey, listed)
	if err != nil {
		t.Fatal(err)
	}
	if authenticated.record.ExecutionID != record.ExecutionID || authenticated.record.SubjectUserID != record.SubjectUserID ||
		!bytes.Equal(authenticated.locatorDigest, sealed.Locator) || authenticated.record.Replay == nil ||
		len(authenticated.record.Replay.Operations) != len(record.Replay.Operations) {
		t.Fatalf("authenticated record mismatch: %+v", authenticated.record)
	}
	wrongLocator := listed
	wrongLocator.LocatorDigest = bytes.Clone(listed.LocatorDigest)
	wrongLocator.LocatorDigest[0] ^= 0xff
	if _, err = AuthenticateReplayTombstone(privateKey, wrongLocator); !errors.Is(err, ErrTombstoneReplayInvalid) {
		t.Fatalf("wrong locator error=%v", err)
	}
	tampered := listed
	tampered.Payload = bytes.Clone(listed.Payload)
	tampered.Payload[len(tampered.Payload)-2] ^= 0x01
	if _, err = AuthenticateReplayTombstone(privateKey, tampered); !errors.Is(err, ErrTombstoneReplayInvalid) {
		t.Fatalf("tampered payload error=%v", err)
	}
}

func TestReplayAuthenticationRequiresExactClosureRetention(t *testing.T) {
	protector, privateKey := tombstoneProtectorFixture(t)
	record := tombstoneFixture()
	closedAt := time.Date(2026, time.September, 10, 10, 0, 0, 0, time.UTC)
	sealed, err := protector.SealClosure(TombstoneClosure{
		Version: TombstoneClosureVersion, Tombstone: record, ClosedAt: closedAt, EvidenceExpiresAt: closedAt.AddDate(0, 24, 0), ErasureEffectiveAt: record.ExecutionStart,
	})
	if err != nil {
		t.Fatal(err)
	}
	listed := listedTombstoneFixture(sealed)
	if _, err = AuthenticateReplayTombstone(privateKey, listed); err != nil {
		t.Fatal(err)
	}
	listed.RetainUntil = listed.RetainUntil.Add(time.Second)
	if _, err = AuthenticateReplayTombstone(privateKey, listed); !errors.Is(err, ErrTombstoneReplayInvalid) {
		t.Fatalf("mismatched retention error=%v", err)
	}
}

func TestReplayAuthenticationReadsLegacyV2ClosureWithExecutionStartFallback(t *testing.T) {
	protector, privateKey := tombstoneProtectorFixture(t)
	record := tombstoneFixture()
	closedAt := record.ExecutionStart.Add(time.Hour)
	closure := TombstoneClosure{Version: TombstoneClosureVersionV2, Tombstone: record, ClosedAt: closedAt, EvidenceExpiresAt: closedAt.AddDate(0, 24, 0)}
	plaintext, err := json.Marshal(closure)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := protector.seal("closure", record.ExecutionID, plaintext, closure.EvidenceExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	authenticated, err := AuthenticateReplayTombstone(privateKey, listedTombstoneFixture(sealed))
	if err != nil {
		t.Fatal(err)
	}
	if authenticated.closureVersion != TombstoneClosureVersionV2 || !authenticated.effectiveAt.Equal(record.ExecutionStart) || authenticated.IsCurrentClosure() {
		t.Fatalf("legacy closure version=%q effective=%s current=%t", authenticated.closureVersion, authenticated.effectiveAt, authenticated.IsCurrentClosure())
	}
}

func TestV1RemainsReadableButCannotBeEmittedOrImportedForReplay(t *testing.T) {
	privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	record := tombstoneFixture()
	record.Version = TombstoneRecordVersionV1
	record.Replay = nil
	envelope := sealLegacyTombstoneForTest(t, privateKey.PublicKey(), record)
	opened, err := OpenRestoreTombstone(privateKey.Bytes(), record.ExecutionID, envelope)
	if err != nil || opened.Version != TombstoneRecordVersionV1 || opened.Replay != nil {
		t.Fatalf("legacy read opened=%+v err=%v", opened, err)
	}
	closedAt := time.Date(2026, time.September, 10, 12, 0, 0, 0, time.UTC)
	legacyClosure := TombstoneClosure{
		Version: TombstoneClosureVersionV1, Tombstone: record, ClosedAt: closedAt, EvidenceExpiresAt: closedAt.AddDate(0, 24, 0),
	}
	closureEnvelope := sealLegacyPayloadForTest(t, privateKey.PublicKey(), "closure", record.ExecutionID, legacyClosure)
	openedClosure, err := OpenRestoreTombstoneClosure(privateKey.Bytes(), record.ExecutionID, closureEnvelope)
	if err != nil || openedClosure.Version != TombstoneClosureVersionV1 || openedClosure.Tombstone.Replay != nil {
		t.Fatalf("legacy closure opened=%+v err=%v", openedClosure, err)
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(encoded)
	listed := ListedTombstoneObject{
		Kind: "intent", LocatorKeyID: "legacy-locator-v1", LocatorDigest: bytes.Repeat([]byte{0x42}, 32),
		Payload: encoded, Checksum: digest[:], ObjectVersion: "legacy-version",
		WrittenAt: time.Now().UTC().Add(-time.Minute), VerifiedAt: time.Now().UTC(),
	}
	if _, err = AuthenticateReplayTombstone(privateKey.Bytes(), listed); !errors.Is(err, ErrTombstoneReplayInvalid) {
		t.Fatalf("legacy replay authentication error=%v", err)
	}
	protector, _ := NewTombstoneProtector("restore-key-v2", privateKey.PublicKey().Bytes(), "locator-key-v2", bytes.Repeat([]byte{0x33}, 32))
	if _, err = protector.Seal(record); !errors.Is(err, ErrTombstoneInvalid) {
		t.Fatalf("legacy emission error=%v", err)
	}
}

func TestReplayPrescriptionRejectsUnsupportedDuplicateAndObjectOperations(t *testing.T) {
	protector, _ := tombstoneProtectorFixture(t)
	for _, operations := range [][]string{
		{"OBJECT_VERSION_DELETE"},
		{"AUTH_TOKEN_DELETE", "AUTH_TOKEN_DELETE"},
		{"UNSUPPORTED_DELETE"},
	} {
		record := tombstoneFixture()
		record.Replay.Operations = operations
		if _, err := protector.Seal(record); !errors.Is(err, ErrTombstoneInvalid) {
			t.Fatalf("operations=%v error=%v", operations, err)
		}
	}
}

func sealLegacyTombstoneForTest(t *testing.T, recipient *ecdh.PublicKey, record RestoreTombstone) TombstoneEnvelope {
	return sealLegacyPayloadForTest(t, recipient, "intent", record.ExecutionID, record)
}

func sealLegacyPayloadForTest(t *testing.T, recipient *ecdh.PublicKey, kind string, executionID uuid.UUID, payload any) TombstoneEnvelope {
	t.Helper()
	ephemeral, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := ephemeral.ECDH(recipient)
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	if _, err = io.ReadFull(hkdf.New(sha256.New, shared, nil, tombstoneAADV1(kind, executionID, "legacy-key-v1")), key); err != nil {
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
	plaintext, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return TombstoneEnvelope{
		Version: TombstoneEnvelopeVersionV1, Algorithm: tombstoneAlgorithm, KeyID: "legacy-key-v1",
		Encapsulation: ephemeral.PublicKey().Bytes(), Nonce: nonce,
		Ciphertext: aead.Seal(nil, nonce, plaintext, tombstoneAADV1(kind, executionID, "legacy-key-v1")),
	}
}
