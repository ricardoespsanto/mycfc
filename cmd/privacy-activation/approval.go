package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/cfcoimbra/mycfc/internal/privacyrequests"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	approvalMaterialContract = "mycfc/privacy-activation-approval-material/v1"
	approvalEnvelopeContract = "mycfc/privacy-activation-approval/v1"
)

type activationBrokerDatabase interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Close()
}

var openActivationBrokerDatabase = func(ctx context.Context, databaseURL string) (activationBrokerDatabase, error) {
	return pgxpool.New(ctx, databaseURL)
}

type approvalMaterial struct {
	Contract              string      `json:"contract"`
	ProposalID            uuid.UUID   `json:"proposal_id"`
	PolicyVersion         string      `json:"policy_version"`
	EvidenceIDs           []uuid.UUID `json:"evidence_ids"`
	EvidenceSetSHA256     string      `json:"evidence_set_sha256"`
	ActivationSHA256      string      `json:"activation_sha256"`
	ExecutorVersion       string      `json:"executor_version"`
	PlanSchemaVersion     string      `json:"plan_schema_version"`
	ImageDigest           string      `json:"image_digest"`
	SchemaMigrationDigest string      `json:"schema_migration_digest"`
}

type approvalUnsigned struct {
	Contract              string      `json:"contract"`
	ProposalID            uuid.UUID   `json:"proposal_id"`
	ActivationSHA256      string      `json:"activation_sha256"`
	EvidenceSetSHA256     string      `json:"evidence_set_sha256"`
	EvidenceIDs           []uuid.UUID `json:"evidence_ids"`
	PolicyVersion         string      `json:"policy_version"`
	ExecutorVersion       string      `json:"executor_version"`
	PlanSchemaVersion     string      `json:"plan_schema_version"`
	ImageDigest           string      `json:"image_digest"`
	SchemaMigrationDigest string      `json:"schema_migration_digest"`
	SigningKeyID          string      `json:"signing_key_id"`
	ActorRef              uuid.UUID   `json:"actor_ref"`
	Role                  string      `json:"role"`
	IssuedAt              time.Time   `json:"issued_at"`
	ExpiresAt             time.Time   `json:"expires_at"`
	Nonce                 string      `json:"nonce"`
}

type approvalEnvelope struct {
	approvalUnsigned
	Signature string `json:"signature_ed25519"`
}

func (e approvalEnvelope) MarshalJSON() ([]byte, error) {
	type wire struct {
		Contract              string      `json:"contract"`
		ProposalID            uuid.UUID   `json:"proposal_id"`
		ActivationSHA256      string      `json:"activation_sha256"`
		EvidenceSetSHA256     string      `json:"evidence_set_sha256"`
		EvidenceIDs           []uuid.UUID `json:"evidence_ids"`
		PolicyVersion         string      `json:"policy_version"`
		ExecutorVersion       string      `json:"executor_version"`
		PlanSchemaVersion     string      `json:"plan_schema_version"`
		ImageDigest           string      `json:"image_digest"`
		SchemaMigrationDigest string      `json:"schema_migration_digest"`
		SigningKeyID          string      `json:"signing_key_id"`
		ActorRef              uuid.UUID   `json:"actor_ref"`
		Role                  string      `json:"role"`
		IssuedAt              time.Time   `json:"issued_at"`
		ExpiresAt             time.Time   `json:"expires_at"`
		Nonce                 string      `json:"nonce"`
		Signature             string      `json:"signature_ed25519"`
	}
	return json.Marshal(wire{e.Contract, e.ProposalID, e.ActivationSHA256, e.EvidenceSetSHA256, e.EvidenceIDs, e.PolicyVersion,
		e.ExecutorVersion, e.PlanSchemaVersion, e.ImageDigest, e.SchemaMigrationDigest, e.SigningKeyID, e.ActorRef, e.Role,
		e.IssuedAt, e.ExpiresAt, e.Nonce, e.Signature})
}

func loadMaterial(path string) (approvalMaterial, error) {
	var material approvalMaterial
	payload, err := readSecret(path)
	if err != nil || decodeExactJSON(payload, &material) != nil || material.Contract != approvalMaterialContract || material.ProposalID == uuid.Nil ||
		len(material.EvidenceIDs) != 4 || !validHexDigest(material.EvidenceSetSHA256) || !validHexDigest(material.ActivationSHA256) ||
		!validHexDigest(material.SchemaMigrationDigest) || !strings.HasPrefix(material.ImageDigest, "sha256:") {
		return approvalMaterial{}, errors.New("privacy activation approval material rejected")
	}
	return material, nil
}

func makeApproval(material approvalMaterial, role, keyID string, actor uuid.UUID, privateKey ed25519.PrivateKey, now time.Time) ([]byte, error) {
	if (role != "EXECUTOR" && role != "ADMINISTRATOR") || actor == uuid.Nil || keyID == "" || len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("privacy activation signer rejected")
	}
	nonce := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, errors.New("privacy activation nonce unavailable")
	}
	unsigned := approvalUnsigned{
		Contract: approvalEnvelopeContract, ProposalID: material.ProposalID, ActivationSHA256: material.ActivationSHA256,
		EvidenceSetSHA256: material.EvidenceSetSHA256, EvidenceIDs: material.EvidenceIDs, PolicyVersion: material.PolicyVersion,
		ExecutorVersion: material.ExecutorVersion, PlanSchemaVersion: material.PlanSchemaVersion, ImageDigest: material.ImageDigest,
		SchemaMigrationDigest: material.SchemaMigrationDigest, SigningKeyID: keyID, ActorRef: actor, Role: role,
		IssuedAt: now.UTC(), ExpiresAt: now.UTC().Add(15 * time.Minute), Nonce: base64.StdEncoding.EncodeToString(nonce),
	}
	signed, err := json.Marshal(unsigned)
	if err != nil {
		return nil, errors.New("privacy activation approval rejected")
	}
	return json.Marshal(approvalEnvelope{approvalUnsigned: unsigned, Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, signed))})
}

func verifyApproval(raw []byte, material approvalMaterial, expectedRole, expectedKeyID string, publicKey ed25519.PublicKey, now time.Time) (approvalEnvelope, []byte, error) {
	var envelope approvalEnvelope
	if decodeExactJSON(raw, &envelope) != nil {
		return envelope, nil, errors.New("privacy activation signed approval rejected")
	}
	canonical, err := json.Marshal(envelope)
	if err != nil || !bytes.Equal(raw, canonical) {
		return envelope, nil, errors.New("privacy activation approval must use canonical JSON")
	}
	if envelope.Contract != approvalEnvelopeContract || envelope.Role != expectedRole || envelope.SigningKeyID != expectedKeyID ||
		envelope.ProposalID != material.ProposalID || envelope.ActivationSHA256 != material.ActivationSHA256 ||
		envelope.EvidenceSetSHA256 != material.EvidenceSetSHA256 || !equalUUIDs(envelope.EvidenceIDs, material.EvidenceIDs) ||
		envelope.PolicyVersion != material.PolicyVersion || envelope.ExecutorVersion != material.ExecutorVersion ||
		envelope.PlanSchemaVersion != material.PlanSchemaVersion || envelope.ImageDigest != material.ImageDigest ||
		envelope.SchemaMigrationDigest != material.SchemaMigrationDigest || envelope.ActorRef == uuid.Nil || envelope.IssuedAt.After(now) ||
		!envelope.ExpiresAt.After(now) || envelope.ExpiresAt.After(envelope.IssuedAt.Add(15*time.Minute)) {
		return envelope, nil, errors.New("privacy activation signed approval rejected")
	}
	nonce, nonceErr := base64.StdEncoding.Strict().DecodeString(envelope.Nonce)
	signature, signatureErr := base64.StdEncoding.Strict().DecodeString(envelope.Signature)
	unsigned, marshalErr := json.Marshal(envelope.approvalUnsigned)
	if nonceErr != nil || len(nonce) != 32 || signatureErr != nil || len(signature) != ed25519.SignatureSize || marshalErr != nil ||
		len(publicKey) != ed25519.PublicKeySize || !ed25519.Verify(publicKey, unsigned, signature) {
		return envelope, nil, errors.New("privacy activation signed approval rejected")
	}
	return envelope, nonce, nil
}

func prepareApprovalMaterial(ctx context.Context, databaseURL string, release privacyrequests.ActivationReleaseBinding) (approvalMaterial, error) {
	pool, err := openActivationBrokerDatabase(ctx, databaseURL)
	if err != nil {
		return approvalMaterial{}, errors.New("open privacy activation broker database")
	}
	defer pool.Close()
	var ids []uuid.UUID
	var setDigest, activationDigest, schemaDigest []byte
	var executor, plan, image string
	err = pool.QueryRow(ctx, `SELECT evidence_ids,evidence_set_sha256,activation_sha256,executor_version,plan_schema_version,image_digest,schema_migration_digest FROM privacy_activation_broker_material($1)`, release.PolicyVersion).
		Scan(&ids, &setDigest, &activationDigest, &executor, &plan, &image, &schemaDigest)
	if err != nil || executor != release.ExecutorVersion || plan != release.PlanSchemaVersion || image != release.ImageDigest || hex.EncodeToString(schemaDigest) != release.SchemaMigrationDigest {
		return approvalMaterial{}, errors.New("privacy activation broker material rejected")
	}
	return approvalMaterial{Contract: approvalMaterialContract, ProposalID: uuid.New(), PolicyVersion: release.PolicyVersion, EvidenceIDs: ids,
		EvidenceSetSHA256: hex.EncodeToString(setDigest), ActivationSHA256: hex.EncodeToString(activationDigest), ExecutorVersion: executor,
		PlanSchemaVersion: plan, ImageDigest: image, SchemaMigrationDigest: hex.EncodeToString(schemaDigest)}, nil
}

func activateApprovedMaterial(ctx context.Context, databaseURL string, material approvalMaterial, executorRaw, administratorRaw []byte,
	executorKeyID, administratorKeyID string, executorPublic, administratorPublic ed25519.PublicKey, now time.Time) error {
	executor, executorNonce, err := verifyApproval(executorRaw, material, "EXECUTOR", executorKeyID, executorPublic, now)
	if err != nil {
		return err
	}
	administrator, administratorNonce, err := verifyApproval(administratorRaw, material, "ADMINISTRATOR", administratorKeyID, administratorPublic, now)
	if err != nil || executor.ActorRef == administrator.ActorRef || executor.SigningKeyID == administrator.SigningKeyID {
		return errors.New("privacy activation independent approvals rejected")
	}
	setDigest, _ := hex.DecodeString(material.EvidenceSetSHA256)
	activationDigest, _ := hex.DecodeString(material.ActivationSHA256)
	executorNonceDigest := sha256.Sum256(executorNonce)
	administratorNonceDigest := sha256.Sum256(administratorNonce)
	pool, err := openActivationBrokerDatabase(ctx, databaseURL)
	if err != nil {
		return errors.New("open privacy activation broker database")
	}
	defer pool.Close()
	var approvalID uuid.UUID
	err = pool.QueryRow(ctx, `SELECT privacy_activation_broker_activate($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14::jsonb,$15::jsonb,$16,$17,$18,$19)`,
		material.ProposalID, material.PolicyVersion, material.EvidenceIDs, setDigest, activationDigest, executor.ActorRef, administrator.ActorRef,
		executor.SigningKeyID, administrator.SigningKeyID, executorNonceDigest[:], administratorNonceDigest[:], executorRaw, administratorRaw,
		string(executorRaw), string(administratorRaw), executor.IssuedAt, executor.ExpiresAt, administrator.IssuedAt, administrator.ExpiresAt).Scan(&approvalID)
	if err != nil || approvalID == uuid.Nil {
		return errors.New("privacy activation broker rejected approvals")
	}
	return nil
}

func decodeExactJSON(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}

func validHexDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
}

func equalUUIDs(left, right []uuid.UUID) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func readPrivateKey(path string) (ed25519.PrivateKey, error) {
	payload, err := readSecret(path)
	if err != nil {
		return nil, err
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(string(payload)))
	if err != nil || (len(decoded) != ed25519.SeedSize && len(decoded) != ed25519.PrivateKeySize) {
		return nil, errors.New("privacy activation signing key rejected")
	}
	if len(decoded) == ed25519.SeedSize {
		return ed25519.NewKeyFromSeed(decoded), nil
	}
	return ed25519.PrivateKey(decoded), nil
}

func writeExclusive(path string, payload []byte) error {
	file, err := os.OpenFile(strings.TrimSpace(path), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errors.New("privacy activation output unavailable")
	}
	defer file.Close()
	if _, err = file.Write(append(payload, '\n')); err != nil || file.Sync() != nil {
		return errors.New("privacy activation output unavailable")
	}
	return nil
}

func trustedRelease(getenv func(string) string) (privacyrequests.ActivationReleaseBinding, error) {
	release := privacyrequests.ActivationReleaseBinding{PolicyVersion: strings.TrimSpace(getenv("PRIVACY_ACTIVATION_POLICY_VERSION")),
		ExecutorVersion: privacyrequests.SupportedExecutorVersion, PlanSchemaVersion: privacyrequests.SupportedPlanSchemaVersion,
		ImageDigest: strings.TrimSpace(getenv("PRIVACY_ACTIVATION_CURRENT_IMAGE_DIGEST")), SchemaMigrationDigest: db.EmbeddedMigrationDigest()}
	if release.PolicyVersion == "" || len(release.ImageDigest) != 71 || !strings.HasPrefix(release.ImageDigest, "sha256:") {
		return release, errors.New("privacy activation current release rejected")
	}
	return release, nil
}
