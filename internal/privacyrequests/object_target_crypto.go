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
	"encoding/binary"
	"errors"
	"io"
	"regexp"

	"github.com/google/uuid"
)

const (
	ObjectTargetEnvelopeVersion = "x25519-aes256gcm-hkdfsha256/v1"
	ObjectTargetLocatorVersion  = "object-key/v1"
	objectTargetAlgorithm       = "X25519-HKDF-SHA256-AES-256-GCM"
)

var (
	ErrObjectTargetCrypto = errors.New("privacy object target protection failed")
	keyIdentifier         = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$`)
)

// ObjectTargetBinding is the immutable, non-secret identity authenticated with
// a protected locator. It prevents a ciphertext from being moved between
// executions, jobs, checkpoints, categories, services, targets, or actions.
type ObjectTargetBinding struct {
	ExecutionID             uuid.UUID
	JobID                   uuid.UUID
	CheckpointID            uuid.UUID
	TargetID                uuid.UUID
	PlanEntrySHA256         []byte
	Category                string
	Service                 string
	TargetKind              string
	SourceKind              string
	SourceRef               uuid.UUID
	OperationCode           string
	ActionVersion           string
	ProviderContractVersion string
}

type ObjectTargetEnvelope struct {
	Version       string
	Algorithm     string
	KeyID         string
	Encapsulation []byte
	Nonce         []byte
	Ciphertext    []byte
}

type ObjectTargetDigest struct {
	KeyID  string
	Digest []byte
}

// ObjectTargetProtector is deliberately seal-only. The web service may be
// given this interface without receiving the worker's private decryption key.
type ObjectTargetProtector interface {
	SealObjectKey(ObjectTargetBinding, string) (ObjectTargetEnvelope, error)
	DigestObjectKey(uuid.UUID, string, string) (ObjectTargetDigest, error)
}

type X25519ObjectTargetProtector struct {
	encryptionKeyID string
	publicKey       *ecdh.PublicKey
	digestKeyID     string
	digestKey       []byte
	random          io.Reader
}

func NewX25519ObjectTargetProtector(encryptionKeyID string, publicKey []byte, digestKeyID string, digestKey []byte) (*X25519ObjectTargetProtector, error) {
	if !keyIdentifier.MatchString(encryptionKeyID) || !keyIdentifier.MatchString(digestKeyID) || len(digestKey) < 32 {
		return nil, ErrObjectTargetCrypto
	}
	parsed, err := ecdh.X25519().NewPublicKey(bytes.Clone(publicKey))
	if err != nil {
		return nil, ErrObjectTargetCrypto
	}
	return &X25519ObjectTargetProtector{encryptionKeyID: encryptionKeyID, publicKey: parsed, digestKeyID: digestKeyID, digestKey: bytes.Clone(digestKey), random: rand.Reader}, nil
}

func (p *X25519ObjectTargetProtector) SealObjectKey(binding ObjectTargetBinding, objectKey string) (ObjectTargetEnvelope, error) {
	var zero ObjectTargetEnvelope
	if p == nil || p.publicKey == nil || p.random == nil || !validObjectTargetBinding(binding) || !validObjectKey(objectKey) {
		return zero, ErrObjectTargetCrypto
	}
	ephemeral, err := ecdh.X25519().GenerateKey(p.random)
	if err != nil {
		return zero, ErrObjectTargetCrypto
	}
	shared, err := ephemeral.ECDH(p.publicKey)
	if err != nil {
		return zero, ErrObjectTargetCrypto
	}
	aad := encodeObjectTargetBinding(binding, p.encryptionKeyID)
	key, err := hkdf.Key(sha256.New, shared, nil, string(append([]byte("mycfc/privacy-object-target-key/v1\x00"), aad...)), 32)
	if err != nil {
		return zero, ErrObjectTargetCrypto
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return zero, ErrObjectTargetCrypto
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return zero, ErrObjectTargetCrypto
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = io.ReadFull(p.random, nonce); err != nil {
		return zero, ErrObjectTargetCrypto
	}
	plaintext := encodeFields(ObjectTargetLocatorVersion, objectKey)
	return ObjectTargetEnvelope{
		Version: ObjectTargetEnvelopeVersion, Algorithm: objectTargetAlgorithm, KeyID: p.encryptionKeyID,
		Encapsulation: ephemeral.PublicKey().Bytes(), Nonce: nonce, Ciphertext: aead.Seal(nil, nonce, plaintext, aad),
	}, nil
}

func (p *X25519ObjectTargetProtector) DigestObjectKey(executionID uuid.UUID, service, objectKey string) (ObjectTargetDigest, error) {
	var zero ObjectTargetDigest
	if p == nil || executionID == uuid.Nil || !policyKey.MatchString(service) || !validObjectKey(objectKey) || len(p.digestKey) < 32 || !keyIdentifier.MatchString(p.digestKeyID) {
		return zero, ErrObjectTargetCrypto
	}
	mac := hmac.New(sha256.New, p.digestKey)
	_, _ = mac.Write(encodeFields("mycfc/privacy-object-target-dedupe/v1", executionID.String(), service, ObjectTargetLocatorVersion, objectKey))
	return ObjectTargetDigest{KeyID: p.digestKeyID, Digest: mac.Sum(nil)}, nil
}

// OpenObjectTargetEnvelope is worker-side functionality. Keeping the private
// key as an explicit argument prevents it from being smuggled into Service.
func OpenObjectTargetEnvelope(privateKey []byte, binding ObjectTargetBinding, envelope ObjectTargetEnvelope) (string, error) {
	if !validObjectTargetBinding(binding) || envelope.Version != ObjectTargetEnvelopeVersion || envelope.Algorithm != objectTargetAlgorithm ||
		!keyIdentifier.MatchString(envelope.KeyID) || len(envelope.Encapsulation) != 32 || len(envelope.Nonce) != 12 || len(envelope.Ciphertext) < 16 || len(envelope.Ciphertext) > 2048 {
		return "", ErrObjectTargetCrypto
	}
	private, err := ecdh.X25519().NewPrivateKey(bytes.Clone(privateKey))
	if err != nil {
		return "", ErrObjectTargetCrypto
	}
	ephemeral, err := ecdh.X25519().NewPublicKey(bytes.Clone(envelope.Encapsulation))
	if err != nil {
		return "", ErrObjectTargetCrypto
	}
	shared, err := private.ECDH(ephemeral)
	if err != nil {
		return "", ErrObjectTargetCrypto
	}
	aad := encodeObjectTargetBinding(binding, envelope.KeyID)
	key, err := hkdf.Key(sha256.New, shared, nil, string(append([]byte("mycfc/privacy-object-target-key/v1\x00"), aad...)), 32)
	if err != nil {
		return "", ErrObjectTargetCrypto
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", ErrObjectTargetCrypto
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", ErrObjectTargetCrypto
	}
	plaintext, err := aead.Open(nil, envelope.Nonce, envelope.Ciphertext, aad)
	if err != nil {
		return "", ErrObjectTargetCrypto
	}
	fields, ok := decodeFields(plaintext, 2)
	if !ok || fields[0] != ObjectTargetLocatorVersion || !validObjectKey(fields[1]) {
		return "", ErrObjectTargetCrypto
	}
	return fields[1], nil
}

func validObjectTargetBinding(binding ObjectTargetBinding) bool {
	return binding.ExecutionID != uuid.Nil && binding.JobID != uuid.Nil && binding.CheckpointID != uuid.Nil && binding.TargetID != uuid.Nil &&
		len(binding.PlanEntrySHA256) == sha256.Size &&
		policyKey.MatchString(binding.Category) && policyKey.MatchString(binding.Service) && binding.TargetKind == "OBJECT_KEY" &&
		validObjectTargetSource(binding.SourceKind) && binding.SourceRef != uuid.Nil &&
		binding.OperationCode == "OBJECT_VERSION_DELETE" && binding.ActionVersion == SupportedActionVersion && binding.ProviderContractVersion == "s3-versioned/v1"
}

func validObjectTargetSource(value string) bool {
	return value == "MEMBER_PROFILE_PHOTO" || value == "REPAIR_ATTACHMENT" || value == "EQUIPMENT_PHOTO"
}

func validObjectKey(value string) bool {
	return value != "" && len(value) <= 1024 && !bytes.ContainsRune([]byte(value), 0)
}

func encodeObjectTargetBinding(binding ObjectTargetBinding, keyID string) []byte {
	return encodeFields("mycfc/privacy-object-target-aad/v1", ObjectTargetEnvelopeVersion, objectTargetAlgorithm, keyID,
		binding.ExecutionID.String(), binding.JobID.String(), binding.CheckpointID.String(), binding.TargetID.String(), string(binding.PlanEntrySHA256),
		binding.Category, binding.Service, binding.TargetKind, binding.SourceKind, binding.SourceRef.String(), binding.OperationCode, binding.ActionVersion, binding.ProviderContractVersion)
}

func encodeFields(fields ...string) []byte {
	var out bytes.Buffer
	for _, field := range fields {
		_ = binary.Write(&out, binary.BigEndian, uint32(len(field)))
		_, _ = out.WriteString(field)
	}
	return out.Bytes()
}

func decodeFields(payload []byte, count int) ([]string, bool) {
	fields := make([]string, 0, count)
	for len(payload) > 0 && len(fields) < count {
		if len(payload) < 4 {
			return nil, false
		}
		size := int(binary.BigEndian.Uint32(payload[:4]))
		payload = payload[4:]
		if size < 0 || size > len(payload) {
			return nil, false
		}
		fields = append(fields, string(payload[:size]))
		payload = payload[size:]
	}
	return fields, len(fields) == count && len(payload) == 0
}
