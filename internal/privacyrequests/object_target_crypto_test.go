package privacyrequests

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestObjectTargetEnvelopeIsRandomizedBoundAndWorkerDecryptable(t *testing.T) {
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	protector, err := NewX25519ObjectTargetProtector("target-key-2026-09", private.PublicKey().Bytes(), "dedupe-key-2026-09", bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	binding := objectTargetTestBinding()
	first, err := protector.SealObjectKey(binding, "profiles/member/raw-photo.png")
	if err != nil {
		t.Fatal(err)
	}
	second, err := protector.SealObjectKey(binding, "profiles/member/raw-photo.png")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first.Encapsulation, second.Encapsulation) || bytes.Equal(first.Nonce, second.Nonce) || bytes.Equal(first.Ciphertext, second.Ciphertext) {
		t.Fatal("repeated sealing did not produce independent randomized envelopes")
	}
	key, err := OpenObjectTargetEnvelope(private.Bytes(), binding, first)
	if err != nil || key != "profiles/member/raw-photo.png" {
		t.Fatalf("key=%q err=%v", key, err)
	}

	tampered := binding
	tampered.Category = "object-storage"
	if _, err = OpenObjectTargetEnvelope(private.Bytes(), tampered, first); !errors.Is(err, ErrObjectTargetCrypto) {
		t.Fatalf("AAD substitution err=%v", err)
	}
	wrongPrivate, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = OpenObjectTargetEnvelope(wrongPrivate.Bytes(), binding, first); !errors.Is(err, ErrObjectTargetCrypto) {
		t.Fatalf("wrong private key err=%v", err)
	}
	first.Ciphertext[0] ^= 1
	if _, err = OpenObjectTargetEnvelope(private.Bytes(), binding, first); !errors.Is(err, ErrObjectTargetCrypto) {
		t.Fatalf("ciphertext tamper err=%v", err)
	}
}

func TestObjectTargetDigestIsSeparateExecutionScopedAndRotationAware(t *testing.T) {
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{9}, 32)
	protector, err := NewX25519ObjectTargetProtector("target-key-v1", private.PublicKey().Bytes(), "digest-key-v1", key)
	if err != nil {
		t.Fatal(err)
	}
	execution := uuid.New()
	first, err := protector.DigestObjectKey(execution, "private-media", "repairs/exact-key")
	if err != nil {
		t.Fatal(err)
	}
	repeat, _ := protector.DigestObjectKey(execution, "private-media", "repairs/exact-key")
	otherExecution, _ := protector.DigestObjectKey(uuid.New(), "private-media", "repairs/exact-key")
	rotated, err := NewX25519ObjectTargetProtector("target-key-v1", private.PublicKey().Bytes(), "digest-key-v2", bytes.Repeat([]byte{10}, 32))
	if err != nil {
		t.Fatal(err)
	}
	rotatedDigest, _ := rotated.DigestObjectKey(execution, "private-media", "repairs/exact-key")
	if first.KeyID != "digest-key-v1" || !bytes.Equal(first.Digest, repeat.Digest) || bytes.Equal(first.Digest, otherExecution.Digest) || bytes.Equal(first.Digest, rotatedDigest.Digest) {
		t.Fatal("dedupe digest contract is not deterministic, execution scoped, and rotation keyed")
	}
}

func TestObjectTargetProtectorRejectsMalformedInputsWithoutEchoingSecrets(t *testing.T) {
	secret := "repairs/private/user-name.png"
	if _, err := NewX25519ObjectTargetProtector("bad key id", nil, "digest", bytes.Repeat([]byte{1}, 32)); !errors.Is(err, ErrObjectTargetCrypto) || bytes.Contains([]byte(err.Error()), []byte(secret)) {
		t.Fatalf("constructor err=%v", err)
	}
	private, _ := ecdh.X25519().GenerateKey(rand.Reader)
	protector, _ := NewX25519ObjectTargetProtector("target-key", private.PublicKey().Bytes(), "digest-key", bytes.Repeat([]byte{1}, 32))
	if _, err := protector.SealObjectKey(ObjectTargetBinding{}, secret); !errors.Is(err, ErrObjectTargetCrypto) || bytes.Contains([]byte(err.Error()), []byte(secret)) {
		t.Fatalf("seal err=%v", err)
	}
}

func objectTargetTestBinding() ObjectTargetBinding {
	return ObjectTargetBinding{ExecutionID: uuid.New(), JobID: uuid.New(), CheckpointID: uuid.New(), TargetID: uuid.New(), PlanEntrySHA256: bytes.Repeat([]byte{3}, 32), Category: "profile-photo", Service: "private-media", TargetKind: "OBJECT_KEY", SourceKind: "MEMBER_PROFILE_PHOTO", SourceRef: uuid.New(), OperationCode: "OBJECT_VERSION_DELETE", ActionVersion: SupportedActionVersion, ProviderContractVersion: "s3-versioned/v1"}
}
