package privacyrequests

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
	"time"

	"github.com/google/uuid"
)

const (
	UploadIntentEnvelopeVersion = "x25519-aes256gcm-hkdfsha256/upload-intent-v1"
	uploadIntentAADVersion      = "mycfc/privacy-object-upload-intent-aad/v1"
	uploadIntentKeyVersion      = "mycfc/privacy-object-upload-intent-key/v1"
	uploadIntentDigestVersion   = "mycfc/privacy-object-upload-intent-dedupe/v1"
)

var ErrUploadIntentCrypto = errors.New("privacy upload intent protection failed")

// UploadIntentBinding is the immutable public identity authenticated with an
// upload-time locator. It is deliberately distinct from ObjectTargetBinding:
// an upload intent must never be substituted for an execution-authorised
// deletion target.
type UploadIntentBinding struct {
	IntentID          uuid.UUID
	SubjectUserID     *uuid.UUID
	ProvenanceActorID uuid.UUID
	SourceKind        string
	SourceRef         uuid.UUID
	Service           string
	TargetKind        string
	ProviderVersion   string
	ContentType       string
	SizeBytes         int64
	CleanupAfter      time.Time
	CreatedAt         time.Time
}

type UploadIntentProtector interface {
	SealUploadObjectKey(UploadIntentBinding, string) (ObjectTargetEnvelope, error)
	DigestUploadObjectKey(string, string) (ObjectTargetDigest, error)
}

type X25519UploadIntentProtector struct {
	encryptionKeyID string
	publicKey       *ecdh.PublicKey
	digestKeyID     string
	digestKey       []byte
	random          io.Reader
}

func NewX25519UploadIntentProtector(encryptionKeyID string, publicKey []byte, digestKeyID string, digestKey []byte) (*X25519UploadIntentProtector, error) {
	if !keyIdentifier.MatchString(encryptionKeyID) || !keyIdentifier.MatchString(digestKeyID) || len(digestKey) < 32 {
		return nil, ErrUploadIntentCrypto
	}
	parsed, err := ecdh.X25519().NewPublicKey(bytes.Clone(publicKey))
	if err != nil {
		return nil, ErrUploadIntentCrypto
	}
	return &X25519UploadIntentProtector{encryptionKeyID: encryptionKeyID, publicKey: parsed, digestKeyID: digestKeyID, digestKey: bytes.Clone(digestKey), random: rand.Reader}, nil
}

func (p *X25519UploadIntentProtector) SealUploadObjectKey(binding UploadIntentBinding, objectKey string) (ObjectTargetEnvelope, error) {
	var zero ObjectTargetEnvelope
	if p == nil || p.publicKey == nil || p.random == nil || !validUploadIntentBinding(binding) || !validUploadObjectKey(binding.SourceKind, objectKey) {
		return zero, ErrUploadIntentCrypto
	}
	ephemeral, err := ecdh.X25519().GenerateKey(p.random)
	if err != nil {
		return zero, ErrUploadIntentCrypto
	}
	shared, err := ephemeral.ECDH(p.publicKey)
	if err != nil {
		return zero, ErrUploadIntentCrypto
	}
	aad := encodeUploadIntentBinding(binding, p.encryptionKeyID)
	key, err := hkdf.Key(sha256.New, shared, nil, string(append([]byte(uploadIntentKeyVersion+"\x00"), aad...)), 32)
	if err != nil {
		return zero, ErrUploadIntentCrypto
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return zero, ErrUploadIntentCrypto
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return zero, ErrUploadIntentCrypto
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = io.ReadFull(p.random, nonce); err != nil {
		return zero, ErrUploadIntentCrypto
	}
	plaintext := encodeFields(ObjectTargetLocatorVersion, objectKey)
	return ObjectTargetEnvelope{
		Version: UploadIntentEnvelopeVersion, Algorithm: objectTargetAlgorithm, KeyID: p.encryptionKeyID,
		Encapsulation: ephemeral.PublicKey().Bytes(), Nonce: nonce, Ciphertext: aead.Seal(nil, nonce, plaintext, aad),
	}, nil
}

func (p *X25519UploadIntentProtector) DigestUploadObjectKey(service, objectKey string) (ObjectTargetDigest, error) {
	var zero ObjectTargetDigest
	if p == nil || !policyKey.MatchString(service) || !validObjectKey(objectKey) || len(p.digestKey) < 32 || !keyIdentifier.MatchString(p.digestKeyID) {
		return zero, ErrUploadIntentCrypto
	}
	mac := hmac.New(sha256.New, p.digestKey)
	_, _ = mac.Write(encodeFields(uploadIntentDigestVersion, service, ObjectTargetLocatorVersion, objectKey))
	return ObjectTargetDigest{KeyID: p.digestKeyID, Digest: mac.Sum(nil)}, nil
}

func OpenUploadIntentEnvelope(privateKey []byte, binding UploadIntentBinding, envelope ObjectTargetEnvelope) (string, error) {
	if !validUploadIntentBinding(binding) || envelope.Version != UploadIntentEnvelopeVersion || envelope.Algorithm != objectTargetAlgorithm ||
		!keyIdentifier.MatchString(envelope.KeyID) || len(envelope.Encapsulation) != 32 || len(envelope.Nonce) != 12 || len(envelope.Ciphertext) < 16 || len(envelope.Ciphertext) > 2048 {
		return "", ErrUploadIntentCrypto
	}
	private, err := ecdh.X25519().NewPrivateKey(bytes.Clone(privateKey))
	if err != nil {
		return "", ErrUploadIntentCrypto
	}
	ephemeral, err := ecdh.X25519().NewPublicKey(bytes.Clone(envelope.Encapsulation))
	if err != nil {
		return "", ErrUploadIntentCrypto
	}
	shared, err := private.ECDH(ephemeral)
	if err != nil {
		return "", ErrUploadIntentCrypto
	}
	aad := encodeUploadIntentBinding(binding, envelope.KeyID)
	key, err := hkdf.Key(sha256.New, shared, nil, string(append([]byte(uploadIntentKeyVersion+"\x00"), aad...)), 32)
	if err != nil {
		return "", ErrUploadIntentCrypto
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", ErrUploadIntentCrypto
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", ErrUploadIntentCrypto
	}
	plaintext, err := aead.Open(nil, envelope.Nonce, envelope.Ciphertext, aad)
	if err != nil {
		return "", ErrUploadIntentCrypto
	}
	fields, ok := decodeFields(plaintext, 2)
	if !ok || fields[0] != ObjectTargetLocatorVersion || !validUploadObjectKey(binding.SourceKind, fields[1]) {
		return "", ErrUploadIntentCrypto
	}
	return fields[1], nil
}

func validUploadIntentBinding(binding UploadIntentBinding) bool {
	if binding.IntentID == uuid.Nil || binding.ProvenanceActorID == uuid.Nil || binding.SourceRef == uuid.Nil ||
		!validObjectTargetSource(binding.SourceKind) || !policyKey.MatchString(binding.Service) || binding.TargetKind != "OBJECT_KEY" ||
		binding.ProviderVersion != "s3-versioned/v1" || !validUploadContentType(binding.ContentType) || binding.SizeBytes < 1 || binding.SizeBytes > 10<<20 ||
		binding.CreatedAt.IsZero() || !binding.CleanupAfter.After(binding.CreatedAt) {
		return false
	}
	if binding.SourceKind == "EQUIPMENT_PHOTO" {
		return binding.SubjectUserID == nil
	}
	return binding.SubjectUserID != nil && *binding.SubjectUserID != uuid.Nil
}

func validUploadContentType(value string) bool {
	return value == "image/jpeg" || value == "image/png" || value == "image/webp"
}

func validUploadObjectKey(sourceKind, value string) bool {
	if !validObjectKey(value) {
		return false
	}
	prefix := map[string]string{"MEMBER_PROFILE_PHOTO": "profiles/", "REPAIR_ATTACHMENT": "repairs/", "EQUIPMENT_PHOTO": "equipment/"}[sourceKind]
	return prefix != "" && len(value) > len(prefix) && value[:len(prefix)] == prefix
}

func encodeUploadIntentBinding(binding UploadIntentBinding, keyID string) []byte {
	subject := ""
	if binding.SubjectUserID != nil {
		subject = binding.SubjectUserID.String()
	}
	return encodeFields(uploadIntentAADVersion, UploadIntentEnvelopeVersion, objectTargetAlgorithm, keyID,
		binding.IntentID.String(), subject, binding.ProvenanceActorID.String(), binding.SourceKind, binding.SourceRef.String(),
		binding.Service, binding.TargetKind, binding.ProviderVersion, binding.ContentType, canonicalInt64(binding.SizeBytes),
		canonicalInt64(binding.CleanupAfter.UTC().UnixNano()), canonicalInt64(binding.CreatedAt.UTC().UnixNano()))
}

func canonicalInt64(value int64) string {
	var encoded [8]byte
	for index := range encoded {
		encoded[len(encoded)-1-index] = byte(value)
		value >>= 8
	}
	return string(encoded[:])
}
