package privacyrequests

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	ActivationApprovalMaterialContract = "mycfc/privacy-activation-approval-material/v2"
	ActivationApprovalEnvelopeContract = "mycfc/privacy-activation-approval/v2"
	ActivationSignerRegistryContract   = "mycfc/privacy-activation-signer-registry/v1"
	ActivationSignatureAlgorithm       = "ECDSA_SHA_256"
	ActivationExecutorRole             = "EXECUTOR"
	ActivationAdministratorRole        = "ADMINISTRATOR"
	ActivationExecutorEnvironment      = "privacy-activation-executor"
	ActivationAdministratorEnvironment = "privacy-activation-administrator"
	ActivationCeremonyLifetime         = 15 * time.Minute
	ActivationApprovalLifetime         = 15 * time.Minute
	MaximumActivationApprovalBytes     = 64 << 10
	maximumActivationNumericID         = uint64(1<<63 - 1)
)

var (
	hex40Pattern       = regexp.MustCompile(`^[0-9a-f]{40}$`)
	hex64Pattern       = regexp.MustCompile(`^[0-9a-f]{64}$`)
	imageDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	keyIDPattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,119}$`)
	kmsARNPattern      = regexp.MustCompile(`^arn:aws[a-zA-Z-]*:kms:[a-z0-9-]+:[0-9]{12}:key/[0-9a-fA-F-]{36}$`)
)

// ActivationSignerRegistry is an immutable, separately digest-pinned mapping
// from each approval role to exactly one human and one non-exportable KMS key.
type ActivationSignerRegistry struct {
	Contract string                    `json:"contract"`
	Signers  ActivationRegistrySigners `json:"signers"`
}

type ActivationRegistrySigners struct {
	Executor      ActivationSigner `json:"EXECUTOR"`
	Administrator ActivationSigner `json:"ADMINISTRATOR"`
}

type ActivationSigner struct {
	ActorRef          uuid.UUID `json:"actor_ref"`
	SigningKeyID      string    `json:"signing_key_id"`
	KMSKeyARN         string    `json:"kms_key_arn"`
	PublicKeySPKI256  string    `json:"public_key_spki_sha256"`
	GitHubActorID     uint64    `json:"github_actor_id"`
	GitHubEnvironment string    `json:"github_environment"`
}

func (r ActivationSignerRegistry) Signer(role string) (ActivationSigner, bool) {
	switch role {
	case ActivationExecutorRole:
		return r.Signers.Executor, true
	case ActivationAdministratorRole:
		return r.Signers.Administrator, true
	default:
		return ActivationSigner{}, false
	}
}

// ActivationApprovalMaterial is the canonical, short-lived description of a
// single approval ceremony. Every approval repeats and cryptographically binds
// these fields.
type ActivationApprovalMaterial struct {
	Contract              string      `json:"contract"`
	CeremonyID            uuid.UUID   `json:"ceremony_id"`
	ProposalID            uuid.UUID   `json:"proposal_id"`
	SourceSHA             string      `json:"source_sha"`
	PolicyVersion         string      `json:"policy_version"`
	EvidenceIDs           []uuid.UUID `json:"evidence_ids"`
	EvidenceSetSHA256     string      `json:"evidence_set_sha256"`
	ActivationSHA256      string      `json:"activation_sha256"`
	ExecutorVersion       string      `json:"executor_version"`
	PlanSchemaVersion     string      `json:"plan_schema_version"`
	ImageDigest           string      `json:"image_digest"`
	SchemaMigrationDigest string      `json:"schema_migration_digest"`
	SignerRegistrySHA256  string      `json:"signer_registry_sha256"`
	PreparedAt            time.Time   `json:"prepared_at"`
	CeremonyExpiresAt     time.Time   `json:"ceremony_expires_at"`
}

// ActivationApprovalUnsigned is the exact byte sequence whose SHA-256 digest
// is passed to KMS Sign with MessageType=DIGEST.
type ActivationApprovalUnsigned struct {
	Contract              string      `json:"contract"`
	CeremonyID            uuid.UUID   `json:"ceremony_id"`
	ProposalID            uuid.UUID   `json:"proposal_id"`
	SourceSHA             string      `json:"source_sha"`
	PolicyVersion         string      `json:"policy_version"`
	EvidenceIDs           []uuid.UUID `json:"evidence_ids"`
	EvidenceSetSHA256     string      `json:"evidence_set_sha256"`
	ActivationSHA256      string      `json:"activation_sha256"`
	ExecutorVersion       string      `json:"executor_version"`
	PlanSchemaVersion     string      `json:"plan_schema_version"`
	ImageDigest           string      `json:"image_digest"`
	SchemaMigrationDigest string      `json:"schema_migration_digest"`
	SignerRegistrySHA256  string      `json:"signer_registry_sha256"`
	PreparedAt            time.Time   `json:"prepared_at"`
	CeremonyExpiresAt     time.Time   `json:"ceremony_expires_at"`
	MaterialSHA256        string      `json:"material_sha256"`
	Role                  string      `json:"role"`
	ActorRef              uuid.UUID   `json:"actor_ref"`
	SigningKeyID          string      `json:"signing_key_id"`
	KMSKeyARN             string      `json:"kms_key_arn"`
	PublicKeySPKI256      string      `json:"public_key_spki_sha256"`
	SignatureAlgorithm    string      `json:"signature_algorithm"`
	GitHubActorID         uint64      `json:"github_actor_id"`
	GitHubEnvironment     string      `json:"github_environment"`
	GitHubRunID           uint64      `json:"github_run_id"`
	GitHubRunAttempt      uint64      `json:"github_run_attempt"`
	IssuedAt              time.Time   `json:"issued_at"`
	ExpiresAt             time.Time   `json:"expires_at"`
	Nonce                 string      `json:"nonce"`
}

type ActivationApprovalEnvelope struct {
	ActivationApprovalUnsigned
	SignatureDERBase64 string `json:"signature_der_base64"`
}

type activationApprovalEnvelopeWire struct {
	Contract              string      `json:"contract"`
	CeremonyID            uuid.UUID   `json:"ceremony_id"`
	ProposalID            uuid.UUID   `json:"proposal_id"`
	SourceSHA             string      `json:"source_sha"`
	PolicyVersion         string      `json:"policy_version"`
	EvidenceIDs           []uuid.UUID `json:"evidence_ids"`
	EvidenceSetSHA256     string      `json:"evidence_set_sha256"`
	ActivationSHA256      string      `json:"activation_sha256"`
	ExecutorVersion       string      `json:"executor_version"`
	PlanSchemaVersion     string      `json:"plan_schema_version"`
	ImageDigest           string      `json:"image_digest"`
	SchemaMigrationDigest string      `json:"schema_migration_digest"`
	SignerRegistrySHA256  string      `json:"signer_registry_sha256"`
	PreparedAt            time.Time   `json:"prepared_at"`
	CeremonyExpiresAt     time.Time   `json:"ceremony_expires_at"`
	MaterialSHA256        string      `json:"material_sha256"`
	Role                  string      `json:"role"`
	ActorRef              uuid.UUID   `json:"actor_ref"`
	SigningKeyID          string      `json:"signing_key_id"`
	KMSKeyARN             string      `json:"kms_key_arn"`
	PublicKeySPKI256      string      `json:"public_key_spki_sha256"`
	SignatureAlgorithm    string      `json:"signature_algorithm"`
	GitHubActorID         uint64      `json:"github_actor_id"`
	GitHubEnvironment     string      `json:"github_environment"`
	GitHubRunID           uint64      `json:"github_run_id"`
	GitHubRunAttempt      uint64      `json:"github_run_attempt"`
	IssuedAt              time.Time   `json:"issued_at"`
	ExpiresAt             time.Time   `json:"expires_at"`
	Nonce                 string      `json:"nonce"`
	SignatureDERBase64    string      `json:"signature_der_base64"`
}

func (e ActivationApprovalEnvelope) MarshalJSON() ([]byte, error) {
	u := e.ActivationApprovalUnsigned
	return json.Marshal(activationApprovalEnvelopeWire{
		u.Contract, u.CeremonyID, u.ProposalID, u.SourceSHA, u.PolicyVersion, u.EvidenceIDs, u.EvidenceSetSHA256,
		u.ActivationSHA256, u.ExecutorVersion, u.PlanSchemaVersion, u.ImageDigest, u.SchemaMigrationDigest,
		u.SignerRegistrySHA256, u.PreparedAt, u.CeremonyExpiresAt, u.MaterialSHA256, u.Role, u.ActorRef,
		u.SigningKeyID, u.KMSKeyARN, u.PublicKeySPKI256, u.SignatureAlgorithm, u.GitHubActorID,
		u.GitHubEnvironment, u.GitHubRunID, u.GitHubRunAttempt, u.IssuedAt, u.ExpiresAt, u.Nonce,
		e.SignatureDERBase64,
	})
}

type ActivationMaterialExpectation struct {
	CeremonyID            uuid.UUID
	SourceSHA             string
	PolicyVersion         string
	ImageDigest           string
	SchemaMigrationDigest string
	SignerRegistrySHA256  string
}

type ActivationApprovalBundle struct {
	Executor      ActivationApprovalEnvelope
	Administrator ActivationApprovalEnvelope
	ExecutorNonce []byte
	AdminNonce    []byte
}

func ParseActivationSignerRegistry(raw []byte, pinnedSHA256 string) (ActivationSignerRegistry, error) {
	var registry ActivationSignerRegistry
	if !validApprovalDigest(pinnedSHA256) || decodeCanonicalActivationJSON(raw, &registry) != nil ||
		registry.Contract != ActivationSignerRegistryContract {
		return ActivationSignerRegistry{}, errors.New("privacy activation signer registry rejected")
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != pinnedSHA256 || !validActivationSigner(registry.Signers.Executor, ActivationExecutorEnvironment) ||
		!validActivationSigner(registry.Signers.Administrator, ActivationAdministratorEnvironment) ||
		!distinctActivationSigners(registry.Signers.Executor, registry.Signers.Administrator) {
		return ActivationSignerRegistry{}, errors.New("privacy activation signer registry rejected")
	}
	return registry, nil
}

func validActivationSigner(s ActivationSigner, expectedEnvironment string) bool {
	return s.ActorRef != uuid.Nil && keyIDPattern.MatchString(s.SigningKeyID) && kmsARNPattern.MatchString(s.KMSKeyARN) &&
		validApprovalDigest(s.PublicKeySPKI256) && s.GitHubActorID > 0 && s.GitHubActorID <= maximumActivationNumericID &&
		s.GitHubEnvironment == expectedEnvironment
}

func distinctActivationSigners(a, b ActivationSigner) bool {
	return a.ActorRef != b.ActorRef && a.SigningKeyID != b.SigningKeyID && a.KMSKeyARN != b.KMSKeyARN &&
		a.PublicKeySPKI256 != b.PublicKeySPKI256 && a.GitHubActorID != b.GitHubActorID
}

func VerifyActivationApprovalMaterial(raw []byte, expected ActivationMaterialExpectation, now time.Time) (ActivationApprovalMaterial, error) {
	var material ActivationApprovalMaterial
	if decodeCanonicalActivationJSON(raw, &material) != nil || !validActivationMaterial(material, now.UTC()) ||
		(expected.CeremonyID != uuid.Nil && material.CeremonyID != expected.CeremonyID) ||
		material.SourceSHA != expected.SourceSHA || material.PolicyVersion != expected.PolicyVersion ||
		material.ImageDigest != expected.ImageDigest || material.SchemaMigrationDigest != expected.SchemaMigrationDigest ||
		material.SignerRegistrySHA256 != expected.SignerRegistrySHA256 {
		return ActivationApprovalMaterial{}, errors.New("privacy activation approval material rejected")
	}
	return material, nil
}

func ParseActivationApprovalMaterial(raw []byte, registrySHA256 string, now time.Time) (ActivationApprovalMaterial, error) {
	var material ActivationApprovalMaterial
	if decodeCanonicalActivationJSON(raw, &material) != nil || !validActivationMaterial(material, now.UTC()) ||
		material.SignerRegistrySHA256 != registrySHA256 {
		return ActivationApprovalMaterial{}, errors.New("privacy activation approval material rejected")
	}
	return material, nil
}

func validActivationMaterial(m ActivationApprovalMaterial, now time.Time) bool {
	if m.Contract != ActivationApprovalMaterialContract || m.CeremonyID == uuid.Nil || m.ProposalID == uuid.Nil ||
		m.CeremonyID == m.ProposalID || !hex40Pattern.MatchString(m.SourceSHA) || strings.TrimSpace(m.PolicyVersion) == "" ||
		len(m.PolicyVersion) > 120 || len(m.EvidenceIDs) != 4 || !distinctUUIDs(m.EvidenceIDs) ||
		!validApprovalDigest(m.EvidenceSetSHA256) || !validApprovalDigest(m.ActivationSHA256) ||
		strings.TrimSpace(m.ExecutorVersion) == "" || strings.TrimSpace(m.PlanSchemaVersion) == "" ||
		!imageDigestPattern.MatchString(m.ImageDigest) || !validApprovalDigest(m.SchemaMigrationDigest) ||
		!validApprovalDigest(m.SignerRegistrySHA256) || !canonicalUTCTime(m.PreparedAt) || !canonicalUTCTime(m.CeremonyExpiresAt) {
		return false
	}
	return !m.PreparedAt.After(now) && m.CeremonyExpiresAt.After(now) && m.CeremonyExpiresAt.After(m.PreparedAt) &&
		!m.CeremonyExpiresAt.After(m.PreparedAt.Add(ActivationCeremonyLifetime))
}

func NewActivationApprovalUnsigned(materialRaw []byte, material ActivationApprovalMaterial, registry ActivationSignerRegistry,
	role string, githubActorID, githubRunID, githubRunAttempt uint64, now time.Time) (ActivationApprovalUnsigned, []byte, [sha256.Size]byte, error) {
	signer, ok := registry.Signer(role)
	if !ok || signer.GitHubActorID != githubActorID || githubRunID == 0 || githubRunID > maximumActivationNumericID ||
		githubRunAttempt == 0 || githubRunAttempt > maximumActivationNumericID {
		return ActivationApprovalUnsigned{}, nil, [sha256.Size]byte{}, errors.New("privacy activation approval signer rejected")
	}
	now = now.UTC().Truncate(time.Second)
	if !validActivationMaterial(material, now) {
		return ActivationApprovalUnsigned{}, nil, [sha256.Size]byte{}, errors.New("privacy activation approval ceremony rejected")
	}
	materialDigest := sha256.Sum256(materialRaw)
	nonce := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return ActivationApprovalUnsigned{}, nil, [sha256.Size]byte{}, errors.New("privacy activation approval nonce unavailable")
	}
	expiresAt := now.Add(ActivationApprovalLifetime)
	if expiresAt.After(material.CeremonyExpiresAt) {
		expiresAt = material.CeremonyExpiresAt
	}
	if !expiresAt.After(now) {
		return ActivationApprovalUnsigned{}, nil, [sha256.Size]byte{}, errors.New("privacy activation approval ceremony rejected")
	}
	u := ActivationApprovalUnsigned{
		Contract: ActivationApprovalEnvelopeContract, CeremonyID: material.CeremonyID, ProposalID: material.ProposalID,
		SourceSHA: material.SourceSHA, PolicyVersion: material.PolicyVersion, EvidenceIDs: append([]uuid.UUID(nil), material.EvidenceIDs...),
		EvidenceSetSHA256: material.EvidenceSetSHA256, ActivationSHA256: material.ActivationSHA256,
		ExecutorVersion: material.ExecutorVersion, PlanSchemaVersion: material.PlanSchemaVersion, ImageDigest: material.ImageDigest,
		SchemaMigrationDigest: material.SchemaMigrationDigest, SignerRegistrySHA256: material.SignerRegistrySHA256,
		PreparedAt: material.PreparedAt, CeremonyExpiresAt: material.CeremonyExpiresAt,
		MaterialSHA256: hex.EncodeToString(materialDigest[:]), Role: role, ActorRef: signer.ActorRef,
		SigningKeyID: signer.SigningKeyID, KMSKeyARN: signer.KMSKeyARN, PublicKeySPKI256: signer.PublicKeySPKI256,
		SignatureAlgorithm: ActivationSignatureAlgorithm, GitHubActorID: signer.GitHubActorID,
		GitHubEnvironment: signer.GitHubEnvironment, GitHubRunID: githubRunID, GitHubRunAttempt: githubRunAttempt,
		IssuedAt: now, ExpiresAt: expiresAt, Nonce: base64.StdEncoding.EncodeToString(nonce),
	}
	canonical, err := json.Marshal(u)
	if err != nil {
		return ActivationApprovalUnsigned{}, nil, [sha256.Size]byte{}, errors.New("privacy activation approval rejected")
	}
	return u, canonical, sha256.Sum256(canonical), nil
}

func AssembleActivationApproval(unsignedRaw, signatureDER, publicKeySPKIDER []byte, materialRaw []byte,
	material ActivationApprovalMaterial, registry ActivationSignerRegistry, expectedRole string, now time.Time) ([]byte, ActivationApprovalEnvelope, error) {
	var unsigned ActivationApprovalUnsigned
	if decodeCanonicalActivationJSON(unsignedRaw, &unsigned) != nil || !approvalUnsignedMatches(unsigned, materialRaw, material, registry, expectedRole, now.UTC()) {
		return nil, ActivationApprovalEnvelope{}, errors.New("privacy activation unsigned approval rejected")
	}
	if err := verifyActivationECDSA(publicKeySPKIDER, unsigned.PublicKeySPKI256, unsignedRaw, signatureDER); err != nil {
		return nil, ActivationApprovalEnvelope{}, err
	}
	envelope := ActivationApprovalEnvelope{ActivationApprovalUnsigned: unsigned, SignatureDERBase64: base64.StdEncoding.EncodeToString(signatureDER)}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return nil, ActivationApprovalEnvelope{}, errors.New("privacy activation approval rejected")
	}
	return raw, envelope, nil
}

func VerifyActivationApproval(raw, materialRaw []byte, material ActivationApprovalMaterial, registry ActivationSignerRegistry,
	expectedRole string, publicKeySPKIDER []byte, now time.Time) (ActivationApprovalEnvelope, []byte, error) {
	var envelope ActivationApprovalEnvelope
	if decodeCanonicalActivationJSON(raw, &envelope) != nil ||
		!approvalUnsignedMatches(envelope.ActivationApprovalUnsigned, materialRaw, material, registry, expectedRole, now.UTC()) {
		return ActivationApprovalEnvelope{}, nil, errors.New("privacy activation signed approval rejected")
	}
	signature, err := base64.StdEncoding.Strict().DecodeString(envelope.SignatureDERBase64)
	if err != nil || len(signature) == 0 || len(signature) > 80 {
		return ActivationApprovalEnvelope{}, nil, errors.New("privacy activation signed approval rejected")
	}
	unsignedRaw, err := json.Marshal(envelope.ActivationApprovalUnsigned)
	if err != nil || verifyActivationECDSA(publicKeySPKIDER, envelope.PublicKeySPKI256, unsignedRaw, signature) != nil {
		return ActivationApprovalEnvelope{}, nil, errors.New("privacy activation signed approval rejected")
	}
	nonce, err := base64.StdEncoding.Strict().DecodeString(envelope.Nonce)
	if err != nil || len(nonce) != 32 {
		return ActivationApprovalEnvelope{}, nil, errors.New("privacy activation signed approval rejected")
	}
	return envelope, nonce, nil
}

func VerifyActivationApprovalBundle(materialRaw []byte, material ActivationApprovalMaterial, registry ActivationSignerRegistry,
	executorRaw, administratorRaw, executorPublicSPKI, administratorPublicSPKI []byte, now time.Time) (ActivationApprovalBundle, error) {
	executor, executorNonce, err := VerifyActivationApproval(executorRaw, materialRaw, material, registry, ActivationExecutorRole, executorPublicSPKI, now)
	if err != nil {
		return ActivationApprovalBundle{}, err
	}
	administrator, administratorNonce, err := VerifyActivationApproval(administratorRaw, materialRaw, material, registry, ActivationAdministratorRole, administratorPublicSPKI, now)
	if err != nil || !distinctActivationSigners(registry.Signers.Executor, registry.Signers.Administrator) ||
		bytes.Equal(executorNonce, administratorNonce) {
		return ActivationApprovalBundle{}, errors.New("privacy activation independent approvals rejected")
	}
	return ActivationApprovalBundle{Executor: executor, Administrator: administrator, ExecutorNonce: executorNonce, AdminNonce: administratorNonce}, nil
}

func approvalUnsignedMatches(u ActivationApprovalUnsigned, materialRaw []byte, m ActivationApprovalMaterial,
	registry ActivationSignerRegistry, expectedRole string, now time.Time) bool {
	signer, ok := registry.Signer(expectedRole)
	materialDigest := sha256.Sum256(materialRaw)
	nonce, nonceErr := base64.StdEncoding.Strict().DecodeString(u.Nonce)
	return ok && u.Contract == ActivationApprovalEnvelopeContract && u.CeremonyID == m.CeremonyID && u.ProposalID == m.ProposalID &&
		u.SourceSHA == m.SourceSHA && u.PolicyVersion == m.PolicyVersion && equalActivationUUIDs(u.EvidenceIDs, m.EvidenceIDs) &&
		u.EvidenceSetSHA256 == m.EvidenceSetSHA256 && u.ActivationSHA256 == m.ActivationSHA256 &&
		u.ExecutorVersion == m.ExecutorVersion && u.PlanSchemaVersion == m.PlanSchemaVersion && u.ImageDigest == m.ImageDigest &&
		u.SchemaMigrationDigest == m.SchemaMigrationDigest && u.SignerRegistrySHA256 == m.SignerRegistrySHA256 &&
		u.PreparedAt.Equal(m.PreparedAt) && u.CeremonyExpiresAt.Equal(m.CeremonyExpiresAt) &&
		u.MaterialSHA256 == hex.EncodeToString(materialDigest[:]) && u.Role == expectedRole && u.ActorRef == signer.ActorRef &&
		u.SigningKeyID == signer.SigningKeyID && u.KMSKeyARN == signer.KMSKeyARN && u.PublicKeySPKI256 == signer.PublicKeySPKI256 &&
		u.SignatureAlgorithm == ActivationSignatureAlgorithm && u.GitHubActorID == signer.GitHubActorID &&
		u.GitHubEnvironment == signer.GitHubEnvironment && u.GitHubRunID > 0 && u.GitHubRunID <= maximumActivationNumericID &&
		u.GitHubRunAttempt > 0 && u.GitHubRunAttempt <= maximumActivationNumericID &&
		canonicalUTCTime(u.IssuedAt) && canonicalUTCTime(u.ExpiresAt) && !u.IssuedAt.Before(m.PreparedAt) &&
		!u.IssuedAt.After(now) && u.ExpiresAt.After(now) && u.ExpiresAt.After(u.IssuedAt) &&
		!u.ExpiresAt.After(u.IssuedAt.Add(ActivationApprovalLifetime)) &&
		!u.ExpiresAt.After(m.CeremonyExpiresAt) && nonceErr == nil && len(nonce) == 32
}

func verifyActivationECDSA(publicKeySPKIDER []byte, expectedDigest string, message, signatureDER []byte) error {
	digest := sha256.Sum256(publicKeySPKIDER)
	if hex.EncodeToString(digest[:]) != expectedDigest {
		return errors.New("privacy activation approval public key rejected")
	}
	parsed, err := x509.ParsePKIXPublicKey(publicKeySPKIDER)
	publicKey, ok := parsed.(*ecdsa.PublicKey)
	if err != nil || !ok || publicKey.Curve != elliptic.P256() {
		return errors.New("privacy activation approval public key rejected")
	}
	var values struct{ R, S *big.Int }
	rest, err := asn1.Unmarshal(signatureDER, &values)
	if err != nil || len(rest) != 0 || values.R == nil || values.S == nil || values.R.Sign() <= 0 || values.S.Sign() <= 0 ||
		values.R.Cmp(publicKey.Params().N) >= 0 || values.S.Cmp(publicKey.Params().N) >= 0 {
		return errors.New("privacy activation approval signature rejected")
	}
	canonicalDER, err := asn1.Marshal(values)
	messageDigest := sha256.Sum256(message)
	if err != nil || !bytes.Equal(canonicalDER, signatureDER) || !ecdsa.Verify(publicKey, messageDigest[:], values.R, values.S) {
		return errors.New("privacy activation approval signature rejected")
	}
	return nil
}

func GenerateActivationApprovalMaterial(core ActivationApprovalMaterial, now time.Time) ([]byte, ActivationApprovalMaterial, error) {
	now = now.UTC().Truncate(time.Second)
	core.Contract = ActivationApprovalMaterialContract
	if core.CeremonyID == uuid.Nil {
		core.CeremonyID = uuid.New()
	}
	if core.ProposalID == uuid.Nil {
		core.ProposalID = uuid.New()
	}
	core.PreparedAt = now
	core.CeremonyExpiresAt = now.Add(ActivationCeremonyLifetime)
	raw, err := json.Marshal(core)
	if err != nil || !validActivationMaterial(core, now) {
		return nil, ActivationApprovalMaterial{}, errors.New("privacy activation approval material rejected")
	}
	return raw, core, nil
}

func ParseActivationPublicKeySPKIBase64(value string) ([]byte, error) {
	der, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(value))
	if err != nil || len(der) == 0 || len(der) > 1024 {
		return nil, errors.New("privacy activation approval public key rejected")
	}
	return der, nil
}

func DecodeActivationSignatureBase64(value string) ([]byte, error) {
	signature, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(value))
	if err != nil || len(signature) == 0 || len(signature) > 80 {
		return nil, errors.New("privacy activation approval signature rejected")
	}
	return signature, nil
}

func CanonicalActivationApprovalDigest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func decodeCanonicalActivationJSON(raw []byte, target any) error {
	if len(raw) == 0 || len(raw) > MaximumActivationApprovalBytes {
		return errors.New("privacy activation JSON rejected")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("privacy activation trailing JSON rejected")
	}
	canonical, err := json.Marshal(target)
	if err != nil || !bytes.Equal(raw, canonical) {
		return errors.New("privacy activation JSON must be canonical")
	}
	return nil
}

func validApprovalDigest(value string) bool { return hex64Pattern.MatchString(value) }

func canonicalUTCTime(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC && value.Nanosecond() == 0
}

func distinctUUIDs(values []uuid.UUID) bool {
	seen := make(map[uuid.UUID]struct{}, len(values))
	for _, value := range values {
		if value == uuid.Nil {
			return false
		}
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func equalActivationUUIDs(left, right []uuid.UUID) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (m ActivationApprovalMaterial) String() string {
	return fmt.Sprintf("ceremony=%s proposal=%s", m.CeremonyID, m.ProposalID)
}
