package privacyrequests

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestUploadIntentEnvelopeIsRandomizedBoundAndDistinctFromExecutionTargets(t *testing.T) {
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	protector, err := NewX25519UploadIntentProtector("upload-key-v1", private.PublicKey().Bytes(), "upload-digest-v1", bytes.Repeat([]byte{8}, 32))
	if err != nil {
		t.Fatal(err)
	}
	binding := uploadIntentTestBinding("MEMBER_PROFILE_PHOTO")
	key := "profiles/2026/09/photo.png"
	first, err := protector.SealUploadObjectKey(binding, key)
	if err != nil {
		t.Fatal(err)
	}
	second, err := protector.SealUploadObjectKey(binding, key)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first.Encapsulation, second.Encapsulation) || bytes.Equal(first.Nonce, second.Nonce) || bytes.Equal(first.Ciphertext, second.Ciphertext) {
		t.Fatal("upload intent envelopes must be randomized")
	}
	opened, err := OpenUploadIntentEnvelope(private.Bytes(), binding, first)
	if err != nil || opened != key {
		t.Fatalf("opened = %q, err = %v", opened, err)
	}
	tampered := binding
	tampered.SourceRef = uuid.New()
	if _, err = OpenUploadIntentEnvelope(private.Bytes(), tampered, first); !errors.Is(err, ErrUploadIntentCrypto) {
		t.Fatalf("tampered binding error = %v", err)
	}
	if _, err = OpenObjectTargetEnvelope(private.Bytes(), objectTargetTestBinding(), first); !errors.Is(err, ErrObjectTargetCrypto) {
		t.Fatalf("upload envelope accepted as execution target: %v", err)
	}
}

func TestUploadIntentDigestIsStableRotationAwareAndSeparateFromExecutionDigest(t *testing.T) {
	private, _ := ecdh.X25519().GenerateKey(rand.Reader)
	key := bytes.Repeat([]byte{9}, 32)
	protector, _ := NewX25519UploadIntentProtector("upload-key", private.PublicKey().Bytes(), "digest-v1", key)
	first, err := protector.DigestUploadObjectKey("private-media", "repairs/2026/09/photo.png")
	if err != nil {
		t.Fatal(err)
	}
	second, _ := protector.DigestUploadObjectKey("private-media", "repairs/2026/09/photo.png")
	if !bytes.Equal(first.Digest, second.Digest) {
		t.Fatal("upload intent digest is not stable")
	}
	executionProtector, _ := NewX25519ObjectTargetProtector("target-key", private.PublicKey().Bytes(), "target-digest", key)
	execution, _ := executionProtector.DigestObjectKey(uuid.New(), "private-media", "repairs/2026/09/photo.png")
	if bytes.Equal(first.Digest, execution.Digest) {
		t.Fatal("upload and execution digest domains must differ")
	}
	rotated, _ := NewX25519UploadIntentProtector("upload-key", private.PublicKey().Bytes(), "digest-v2", bytes.Repeat([]byte{10}, 32))
	rotatedDigest, _ := rotated.DigestUploadObjectKey("private-media", "repairs/2026/09/photo.png")
	if rotatedDigest.KeyID == first.KeyID || bytes.Equal(rotatedDigest.Digest, first.Digest) {
		t.Fatal("digest rotation was not reflected")
	}
}

func TestUploadIntentProtectorRejectsWrongFamiliesAndInvalidBindingsWithoutSecrets(t *testing.T) {
	private, _ := ecdh.X25519().GenerateKey(rand.Reader)
	protector, _ := NewX25519UploadIntentProtector("upload-key", private.PublicKey().Bytes(), "digest-key", bytes.Repeat([]byte{11}, 32))
	secret := "equipment/private-secret.png"
	tests := []struct {
		name    string
		binding UploadIntentBinding
		key     string
	}{
		{name: "wrong prefix", binding: uploadIntentTestBinding("MEMBER_PROFILE_PHOTO"), key: secret},
		{name: "missing actor", binding: func() UploadIntentBinding {
			b := uploadIntentTestBinding("EQUIPMENT_PHOTO")
			b.ProvenanceActorID = uuid.Nil
			return b
		}(), key: secret},
		{name: "equipment subject", binding: func() UploadIntentBinding {
			b := uploadIntentTestBinding("EQUIPMENT_PHOTO")
			id := uuid.New()
			b.SubjectUserID = &id
			return b
		}(), key: secret},
		{name: "expired", binding: func() UploadIntentBinding {
			b := uploadIntentTestBinding("EQUIPMENT_PHOTO")
			b.CleanupAfter = b.CreatedAt
			return b
		}(), key: secret},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := protector.SealUploadObjectKey(test.binding, test.key)
			if !errors.Is(err, ErrUploadIntentCrypto) || bytes.Contains([]byte(err.Error()), []byte(secret)) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	if _, err := protector.DigestUploadObjectKey("bad service", secret); !errors.Is(err, ErrUploadIntentCrypto) {
		t.Fatalf("digest error = %v", err)
	}
}

func uploadIntentTestBinding(sourceKind string) UploadIntentBinding {
	now := time.Date(2026, 9, 10, 14, 0, 0, 0, time.UTC)
	binding := UploadIntentBinding{
		IntentID: uuid.New(), ProvenanceActorID: uuid.New(), SourceKind: sourceKind, SourceRef: uuid.New(),
		Service: "private-media", TargetKind: "OBJECT_KEY", ProviderVersion: "s3-versioned/v1",
		ContentType: "image/png", SizeBytes: 42, CreatedAt: now, CleanupAfter: now.Add(24 * time.Hour),
	}
	if sourceKind != "EQUIPMENT_PHOTO" {
		subject := uuid.New()
		binding.SubjectUserID = &subject
	}
	return binding
}
