package privacyrequests

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
	"slices"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrProviderRegistryUnavailable = errors.New("privacy provider registry unavailable")
	ErrProviderCaptureFailed       = errors.New("privacy provider capture failed")
	ErrProviderExecutionFailed     = errors.New("privacy provider execution failed")
)

const (
	ProviderTargetEnvelopeVersion = "x25519-aes256gcm-hkdfsha256/provider-target-v1"
	providerTargetAlgorithm       = "X25519-HKDF-SHA256-AES-256-GCM"
	providerTargetLocatorVersion  = "provider-opaque-target/v1"
	providerCredentialVersion     = "provider-minimum-credential/v1"
	providerEvidenceVersion       = "provider-structured-evidence/v1"
	providerEvidenceTranscript    = "mycfc/privacy-provider-evidence-transcript/v1"
)

type ProviderRole string

const (
	ProviderRoleProcessor           ProviderRole = "PROCESSOR"
	ProviderRoleAutonomousRecipient ProviderRole = "AUTONOMOUS_RECIPIENT"
)

type ProviderOutcome string

const (
	ProviderOutcomeDeletionVerified    ProviderOutcome = "REMOTE_DELETION_VERIFIED"
	ProviderOutcomeAlreadyDisconnected ProviderOutcome = "ALREADY_DISCONNECTED"
	ProviderOutcomeNotControllable     ProviderOutcome = "NOT_CONTROLLABLE"
)

// ProviderExecutionRegistration is one factually evidenced entry in the
// closed execution registry. Production deliberately constructs an empty
// registry until #109 supplies these facts; provider names alone are not facts.
type ProviderExecutionRegistration struct {
	ServiceCode            string
	Role                   ProviderRole
	ContractVersion        string
	RegistryEvidenceKeyID  string
	RegistryEvidenceDigest []byte
	Adapter                ProviderErasureAdapter
}

type ProviderExecutionRegistry struct {
	registrations map[string]ProviderExecutionRegistration
}

func NewProviderExecutionRegistry(registrations ...ProviderExecutionRegistration) (*ProviderExecutionRegistry, error) {
	r := &ProviderExecutionRegistry{registrations: make(map[string]ProviderExecutionRegistration, len(registrations))}
	for _, registration := range registrations {
		if !validProviderRegistration(registration) {
			return nil, ErrProviderRegistryUnavailable
		}
		if _, exists := r.registrations[registration.ServiceCode]; exists {
			return nil, ErrProviderRegistryUnavailable
		}
		registration.RegistryEvidenceDigest = slices.Clone(registration.RegistryEvidenceDigest)
		r.registrations[registration.ServiceCode] = registration
	}
	return r, nil
}

func validProviderRegistration(registration ProviderExecutionRegistration) bool {
	return policyKey.MatchString(registration.ServiceCode) &&
		(registration.Role == ProviderRoleProcessor || registration.Role == ProviderRoleAutonomousRecipient) &&
		keyIdentifier.MatchString(registration.ContractVersion) &&
		keyIdentifier.MatchString(registration.RegistryEvidenceKeyID) &&
		len(registration.RegistryEvidenceDigest) == sha256.Size && registration.Adapter != nil
}

// Ready is false for the intentionally empty production registry. This makes
// PROVIDER_RECIPIENT_NOTIFY a hard activation/start blocker until factual
// provider records and adapters are installed together.
func (r *ProviderExecutionRegistry) Ready() bool {
	return r != nil && len(r.registrations) > 0
}

func (r *ProviderExecutionRegistry) registration(service string, role ProviderRole, contract, evidenceKeyID string, evidenceDigest []byte) (ProviderExecutionRegistration, bool) {
	if r == nil {
		return ProviderExecutionRegistration{}, false
	}
	registration, ok := r.registrations[service]
	return registration, ok && registration.Role == role && registration.ContractVersion == contract && registration.RegistryEvidenceKeyID == evidenceKeyID &&
		hmac.Equal(registration.RegistryEvidenceDigest, evidenceDigest)
}

type ProviderErasureRequest struct {
	ServiceCode         string
	Role                ProviderRole
	ContractVersion     string
	Target              ProviderSecret
	Credential          ProviderSecret
	AlreadyDisconnected bool
}

// ProviderSecret prevents accidental formatting of an opaque target or
// quarantined credential returned to the isolated worker.
type ProviderSecret struct {
	keyID string
	value []byte
}

func (ProviderSecret) String() string   { return "[REDACTED]" }
func (ProviderSecret) GoString() string { return "[REDACTED]" }
func (s ProviderSecret) Bytes() []byte  { return slices.Clone(s.value) }
func (s ProviderSecret) KeyID() string  { return s.keyID }

type ProviderErasureResult struct {
	Outcome           ProviderOutcome
	EvidenceCode      string
	EvidenceReference []byte
	RecipientRole     string
	ChannelCode       string
	NotificationCode  string
	ReasonCode        string
	GuidanceCode      string
}

type ProviderErasureAdapter interface {
	EraseOrNotify(context.Context, ProviderErasureRequest) (ProviderErasureResult, error)
}

type retryableProviderError struct{}

func (retryableProviderError) Error() string { return "provider dependency unavailable" }

func RetryableProviderError(err error) error {
	if err == nil {
		return nil
	}
	return retryableProviderError{}
}

func isRetryableProviderError(err error) bool {
	var target retryableProviderError
	return errors.As(err, &target)
}

type ProviderTargetBinding struct {
	ExecutionID, JobID, CheckpointID, TargetID uuid.UUID
	PlanEntrySHA256                            []byte
	Category, Service                          string
	TargetKind                                 string
	Role                                       ProviderRole
	TargetVersion                              int64
	OperationCode, ActionVersion               string
	ProviderContractVersion                    string
}

type ProviderTargetEnvelope struct {
	Version, Algorithm, KeyID        string
	Encapsulation, Nonce, Ciphertext []byte
}

type ProviderTargetDigest struct {
	KeyID  string
	Digest []byte
}

type ProviderTargetProtector interface {
	SealProviderTarget(ProviderTargetBinding, string, []byte) (ProviderTargetEnvelope, error)
	SealProviderCredential(ProviderTargetBinding, string, []byte) (ProviderTargetEnvelope, error)
	DigestProviderTarget(ProviderTargetBinding, string, []byte) (ProviderTargetDigest, error)
	DigestProviderCredential(ProviderTargetBinding, string, []byte) (ProviderTargetDigest, error)
}

type X25519ProviderTargetProtector struct {
	encryptionKeyID       string
	publicKey             *ecdh.PublicKey
	targetDigestKeyID     string
	targetDigestKey       []byte
	credentialDigestKeyID string
	credentialDigestKey   []byte
	random                io.Reader
}

func NewX25519ProviderTargetProtector(encryptionKeyID string, publicKey []byte, targetDigestKeyID string, targetDigestKey []byte, credentialDigestKeyID string, credentialDigestKey []byte) (*X25519ProviderTargetProtector, error) {
	if !keyIdentifier.MatchString(encryptionKeyID) || !keyIdentifier.MatchString(targetDigestKeyID) ||
		!keyIdentifier.MatchString(credentialDigestKeyID) || encryptionKeyID == targetDigestKeyID || encryptionKeyID == credentialDigestKeyID ||
		targetDigestKeyID == credentialDigestKeyID || len(targetDigestKey) < 32 || len(credentialDigestKey) < 32 || hmac.Equal(targetDigestKey, credentialDigestKey) {
		return nil, ErrProviderCaptureFailed
	}
	parsed, err := ecdh.X25519().NewPublicKey(slices.Clone(publicKey))
	if err != nil {
		return nil, ErrProviderCaptureFailed
	}
	return &X25519ProviderTargetProtector{encryptionKeyID: encryptionKeyID, publicKey: parsed,
		targetDigestKeyID: targetDigestKeyID, targetDigestKey: slices.Clone(targetDigestKey),
		credentialDigestKeyID: credentialDigestKeyID, credentialDigestKey: slices.Clone(credentialDigestKey), random: rand.Reader}, nil
}

func (p *X25519ProviderTargetProtector) SealProviderTarget(binding ProviderTargetBinding, sourceKeyID string, value []byte) (ProviderTargetEnvelope, error) {
	return p.seal(binding, providerTargetLocatorVersion, sourceKeyID, value)
}

func (p *X25519ProviderTargetProtector) SealProviderCredential(binding ProviderTargetBinding, sourceKeyID string, value []byte) (ProviderTargetEnvelope, error) {
	return p.seal(binding, providerCredentialVersion, sourceKeyID, value)
}

func (p *X25519ProviderTargetProtector) seal(binding ProviderTargetBinding, purpose, sourceKeyID string, value []byte) (ProviderTargetEnvelope, error) {
	var zero ProviderTargetEnvelope
	if p == nil || p.publicKey == nil || p.random == nil || !validProviderTargetBinding(binding) ||
		(purpose != providerTargetLocatorVersion && purpose != providerCredentialVersion) || !keyIdentifier.MatchString(sourceKeyID) || len(value) == 0 || len(value) > 16384 {
		return zero, ErrProviderCaptureFailed
	}
	ephemeral, err := ecdh.X25519().GenerateKey(p.random)
	if err != nil {
		return zero, ErrProviderCaptureFailed
	}
	shared, err := ephemeral.ECDH(p.publicKey)
	if err != nil {
		return zero, ErrProviderCaptureFailed
	}
	aad := encodeProviderTargetBinding(binding, p.encryptionKeyID, purpose)
	key, err := hkdf.Key(sha256.New, shared, nil, string(append([]byte("mycfc/privacy-provider-target-key/v1\x00"), aad...)), 32)
	if err != nil {
		return zero, ErrProviderCaptureFailed
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return zero, ErrProviderCaptureFailed
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return zero, ErrProviderCaptureFailed
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = io.ReadFull(p.random, nonce); err != nil {
		return zero, ErrProviderCaptureFailed
	}
	plaintext := encodeFields(purpose, sourceKeyID, string(value))
	return ProviderTargetEnvelope{Version: ProviderTargetEnvelopeVersion, Algorithm: providerTargetAlgorithm, KeyID: p.encryptionKeyID,
		Encapsulation: ephemeral.PublicKey().Bytes(), Nonce: nonce, Ciphertext: aead.Seal(nil, nonce, plaintext, aad)}, nil
}

func (p *X25519ProviderTargetProtector) DigestProviderTarget(binding ProviderTargetBinding, sourceKeyID string, value []byte) (ProviderTargetDigest, error) {
	if p == nil {
		return ProviderTargetDigest{}, ErrProviderCaptureFailed
	}
	return digestProviderSecret(binding, sourceKeyID, value, p.targetDigestKeyID, p.targetDigestKey, "mycfc/privacy-provider-target-dedupe/v1", true)
}

func (p *X25519ProviderTargetProtector) DigestProviderCredential(binding ProviderTargetBinding, sourceKeyID string, value []byte) (ProviderTargetDigest, error) {
	if p == nil {
		return ProviderTargetDigest{}, ErrProviderCaptureFailed
	}
	return digestProviderSecret(binding, sourceKeyID, value, p.credentialDigestKeyID, p.credentialDigestKey, "mycfc/privacy-provider-credential-commitment/v1", false)
}

func digestProviderSecret(binding ProviderTargetBinding, sourceKeyID string, value []byte, digestKeyID string, digestKey []byte, domain string, stableTargetAlias bool) (ProviderTargetDigest, error) {
	if !validProviderTargetBinding(binding) || !keyIdentifier.MatchString(sourceKeyID) || len(value) == 0 || len(value) > 16384 ||
		len(digestKey) < 32 || !keyIdentifier.MatchString(digestKeyID) {
		return ProviderTargetDigest{}, ErrProviderCaptureFailed
	}
	mac := hmac.New(sha256.New, digestKey)
	if stableTargetAlias {
		_, _ = mac.Write(encodeFields(domain, binding.ExecutionID.String(), binding.Category, binding.Service, binding.TargetKind,
			string(binding.Role), canonicalInt64(binding.TargetVersion), binding.OperationCode, binding.ActionVersion,
			binding.ProviderContractVersion, sourceKeyID, string(value)))
	} else {
		_, _ = mac.Write(encodeFields(domain, string(encodeProviderTargetBinding(binding, digestKeyID, providerCredentialVersion)), sourceKeyID, string(value)))
	}
	return ProviderTargetDigest{KeyID: digestKeyID, Digest: mac.Sum(nil)}, nil
}

func OpenProviderTargetEnvelope(privateKey []byte, binding ProviderTargetBinding, purpose string, envelope ProviderTargetEnvelope) (ProviderSecret, error) {
	if !validProviderTargetBinding(binding) || (purpose != providerTargetLocatorVersion && purpose != providerCredentialVersion) ||
		envelope.Version != ProviderTargetEnvelopeVersion || envelope.Algorithm != providerTargetAlgorithm || !keyIdentifier.MatchString(envelope.KeyID) ||
		len(envelope.Encapsulation) != 32 || len(envelope.Nonce) != 12 || len(envelope.Ciphertext) < 17 || len(envelope.Ciphertext) > 18432 {
		return ProviderSecret{}, ErrProviderExecutionFailed
	}
	private, err := ecdh.X25519().NewPrivateKey(slices.Clone(privateKey))
	if err != nil {
		return ProviderSecret{}, ErrProviderExecutionFailed
	}
	ephemeral, err := ecdh.X25519().NewPublicKey(slices.Clone(envelope.Encapsulation))
	if err != nil {
		return ProviderSecret{}, ErrProviderExecutionFailed
	}
	shared, err := private.ECDH(ephemeral)
	if err != nil {
		return ProviderSecret{}, ErrProviderExecutionFailed
	}
	aad := encodeProviderTargetBinding(binding, envelope.KeyID, purpose)
	key, err := hkdf.Key(sha256.New, shared, nil, string(append([]byte("mycfc/privacy-provider-target-key/v1\x00"), aad...)), 32)
	if err != nil {
		return ProviderSecret{}, ErrProviderExecutionFailed
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return ProviderSecret{}, ErrProviderExecutionFailed
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return ProviderSecret{}, ErrProviderExecutionFailed
	}
	plaintext, err := aead.Open(nil, envelope.Nonce, envelope.Ciphertext, aad)
	if err != nil {
		return ProviderSecret{}, ErrProviderExecutionFailed
	}
	fields, ok := decodeFields(plaintext, 3)
	if !ok || fields[0] != purpose || !keyIdentifier.MatchString(fields[1]) || len(fields[2]) == 0 || len(fields[2]) > 16384 {
		return ProviderSecret{}, ErrProviderExecutionFailed
	}
	return ProviderSecret{keyID: fields[1], value: []byte(fields[2])}, nil
}

func validProviderTargetBinding(binding ProviderTargetBinding) bool {
	return binding.ExecutionID != uuid.Nil && binding.JobID != uuid.Nil && binding.CheckpointID != uuid.Nil && binding.TargetID != uuid.Nil &&
		len(binding.PlanEntrySHA256) == sha256.Size && policyKey.MatchString(binding.Category) && policyKey.MatchString(binding.Service) &&
		binding.TargetKind == "REMOTE_ACCOUNT" && (binding.Role == ProviderRoleProcessor || binding.Role == ProviderRoleAutonomousRecipient) &&
		binding.TargetVersion > 0 && binding.OperationCode == "PROVIDER_RECIPIENT_NOTIFY" && binding.ActionVersion == SupportedActionVersion &&
		keyIdentifier.MatchString(binding.ProviderContractVersion)
}

func encodeProviderTargetBinding(binding ProviderTargetBinding, keyID, purpose string) []byte {
	return encodeFields("mycfc/privacy-provider-target-aad/v1", ProviderTargetEnvelopeVersion, providerTargetAlgorithm, keyID, purpose,
		binding.ExecutionID.String(), binding.JobID.String(), binding.CheckpointID.String(), binding.TargetID.String(), string(binding.PlanEntrySHA256),
		binding.Category, binding.Service, binding.TargetKind, string(binding.Role), canonicalInt64(binding.TargetVersion), binding.OperationCode,
		binding.ActionVersion, binding.ProviderContractVersion)
}

type capturedProviderConnection struct {
	ConnectionID, JobID, CheckpointID                               uuid.UUID
	PlanEntrySHA256                                                 []byte
	Category, Service, Role, ContractVersion, LocalState            string
	TargetVersion                                                   int64
	RegistryEvidenceKeyID, TargetSourceKeyID, CredentialSourceKeyID string
	RegistryEvidenceDigest, TargetOpaque, CredentialOpaque          []byte
}

func (s Service) materializeProviderTargets(ctx context.Context, tx pgx.Tx, q *dbgen.Queries, execution dbgen.PrivacyErasureExecution, plan ExecutionPlan, subjectID uuid.UUID) error {
	for _, entry := range plan.Entries {
		if !containsOperation(entry.Operations, "PROVIDER_RECIPIENT_NOTIFY") {
			continue
		}
		if s.ProviderRegistry == nil || !s.ProviderRegistry.Ready() || s.ProviderTargets == nil {
			return ErrExecutorUnavailable
		}
		rows, err := tx.Query(ctx, `SELECT connection_id,job_id,checkpoint_id,plan_entry_sha256,category_key,service_code,provider_role,provider_contract_version,target_version,local_state,registry_evidence_key_id,registry_evidence_digest,target_source_key_id,target_opaque,credential_source_key_id,credential_opaque FROM privacy_execution_capture_provider_connections($1,$2,$3)`, execution.ID, subjectID, entry.Category)
		if err != nil {
			return ErrProviderCaptureFailed
		}
		var captures []capturedProviderConnection
		for rows.Next() {
			var c capturedProviderConnection
			if err = rows.Scan(&c.ConnectionID, &c.JobID, &c.CheckpointID, &c.PlanEntrySHA256, &c.Category, &c.Service, &c.Role,
				&c.ContractVersion, &c.TargetVersion, &c.LocalState, &c.RegistryEvidenceKeyID, &c.RegistryEvidenceDigest, &c.TargetSourceKeyID,
				&c.TargetOpaque, &c.CredentialSourceKeyID, &c.CredentialOpaque); err != nil {
				rows.Close()
				return ErrProviderCaptureFailed
			}
			captures = append(captures, c)
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return ErrProviderCaptureFailed
		}
		rows.Close()
		for _, capture := range captures {
			role := ProviderRole(capture.Role)
			registration, known := s.ProviderRegistry.registration(capture.Service, role, capture.ContractVersion, capture.RegistryEvidenceKeyID, capture.RegistryEvidenceDigest)
			if !known || registration.Adapter == nil || len(capture.TargetOpaque) == 0 ||
				(capture.LocalState == "ACTIVE" && len(capture.CredentialOpaque) == 0) ||
				(capture.LocalState != "ACTIVE" && capture.LocalState != "DISCONNECTED") {
				return ErrProviderRegistryUnavailable
			}
			targetID := uuid.New()
			binding := ProviderTargetBinding{ExecutionID: execution.ID, JobID: capture.JobID, CheckpointID: capture.CheckpointID, TargetID: targetID,
				PlanEntrySHA256: capture.PlanEntrySHA256, Category: capture.Category, Service: capture.Service, TargetKind: "REMOTE_ACCOUNT",
				Role: role, TargetVersion: capture.TargetVersion, OperationCode: "PROVIDER_RECIPIENT_NOTIFY", ActionVersion: SupportedActionVersion,
				ProviderContractVersion: capture.ContractVersion}
			targetEnvelope, sealErr := s.ProviderTargets.SealProviderTarget(binding, capture.TargetSourceKeyID, capture.TargetOpaque)
			if sealErr != nil {
				return ErrProviderCaptureFailed
			}
			targetDigest, digestErr := s.ProviderTargets.DigestProviderTarget(binding, capture.TargetSourceKeyID, capture.TargetOpaque)
			if digestErr != nil {
				return ErrProviderCaptureFailed
			}
			var credentialEnvelope ProviderTargetEnvelope
			var credentialCommitment ProviderTargetDigest
			if capture.LocalState == "ACTIVE" {
				credentialEnvelope, sealErr = s.ProviderTargets.SealProviderCredential(binding, capture.CredentialSourceKeyID, capture.CredentialOpaque)
				if sealErr != nil {
					return ErrProviderCaptureFailed
				}
				credentialCommitment, digestErr = s.ProviderTargets.DigestProviderCredential(binding, capture.CredentialSourceKeyID, capture.CredentialOpaque)
				if digestErr != nil {
					return ErrProviderCaptureFailed
				}
			}
			_, err = q.MaterializePrivacyProviderTarget(ctx, dbgen.MaterializePrivacyProviderTargetParams{
				TargetID: targetID, ConnectionID: capture.ConnectionID, ExecutionID: execution.ID, JobID: capture.JobID, CheckpointID: capture.CheckpointID,
				PlanEntrySha256: capture.PlanEntrySHA256, CategoryKey: capture.Category, ServiceCode: capture.Service, ProviderRole: capture.Role,
				ProviderContractVersion: capture.ContractVersion, TargetVersion: capture.TargetVersion, LocalState: capture.LocalState,
				RegistryEvidenceKeyID: registration.RegistryEvidenceKeyID, RegistryEvidenceDigest: registration.RegistryEvidenceDigest,
				TargetEnvelopeVersion: targetEnvelope.Version, TargetAlgorithm: targetEnvelope.Algorithm, TargetEncryptionKeyID: targetEnvelope.KeyID,
				TargetEncapsulation: targetEnvelope.Encapsulation, TargetNonce: targetEnvelope.Nonce, TargetCiphertext: targetEnvelope.Ciphertext,
				CredentialEnvelopeVersion: optionalString(credentialEnvelope.Version), CredentialAlgorithm: optionalString(credentialEnvelope.Algorithm),
				CredentialEncryptionKeyID: optionalString(credentialEnvelope.KeyID), CredentialEncapsulation: optionalBytes(credentialEnvelope.Encapsulation),
				CredentialNonce: optionalBytes(credentialEnvelope.Nonce), CredentialCiphertext: optionalBytes(credentialEnvelope.Ciphertext),
				CredentialCommitmentKeyID: optionalString(credentialCommitment.KeyID), CredentialSourceCommitment: optionalBytes(credentialCommitment.Digest),
				DigestKeyID: targetDigest.KeyID, TargetDigest: targetDigest.Digest,
			})
			if err != nil {
				return ErrProviderCaptureFailed
			}
		}
		if _, err = q.CompletePrivacyProviderCapture(ctx, dbgen.CompletePrivacyProviderCaptureParams{ExecutionID: execution.ID, CategoryKey: entry.Category}); err != nil {
			return ErrProviderCaptureFailed
		}
	}
	return nil
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
func optionalBytes(value []byte) []byte {
	if len(value) == 0 {
		return nil
	}
	return slices.Clone(value)
}

type ProviderExecutionWorker struct {
	Pool                 dbgen.DBTX
	Registry             *ProviderExecutionRegistry
	WorkerRef            uuid.UUID
	PrivateKey           []byte
	TranscriptKeyID      string
	TranscriptKey        []byte
	CredentialDigestKeys map[string][]byte
	AdapterAttempts      int
	AdapterTimeout       time.Duration
}

func (w ProviderExecutionWorker) attempts() int {
	if w.AdapterAttempts == 0 {
		return 3
	}
	return w.AdapterAttempts
}
func (w ProviderExecutionWorker) timeout() time.Duration {
	if w.AdapterTimeout == 0 {
		return 30 * time.Second
	}
	return w.AdapterTimeout
}

func (w ProviderExecutionWorker) valid() bool {
	return w.Pool != nil && w.Registry != nil && w.Registry.Ready() && w.WorkerRef != uuid.Nil && len(w.PrivateKey) == 32 &&
		keyIdentifier.MatchString(w.TranscriptKeyID) && len(w.TranscriptKey) >= 32 && validProviderDigestKeyring(w.CredentialDigestKeys, w.TranscriptKeyID, w.TranscriptKey) && w.attempts() >= 1 && w.attempts() <= 5 &&
		w.timeout() >= time.Second && w.timeout() <= 2*time.Minute
}

func validProviderDigestKeyring(keyring map[string][]byte, transcriptKeyID string, transcriptKey []byte) bool {
	if len(keyring) == 0 {
		return false
	}
	for keyID, key := range keyring {
		if !keyIdentifier.MatchString(keyID) || keyID == transcriptKeyID || len(key) < 32 || hmac.Equal(key, transcriptKey) {
			return false
		}
	}
	return true
}

func (w ProviderExecutionWorker) CompleteCheckpoint(ctx context.Context, lease ExecutionLease) (dbgen.PrivacyErasureJobCheckpoint, error) {
	var zero dbgen.PrivacyErasureJobCheckpoint
	if !w.valid() || !validLease(lease) {
		return zero, ErrProviderExecutionFailed
	}
	rows, err := w.Pool.Query(ctx, `SELECT target_id,execution_id,job_id,checkpoint_id,plan_entry_sha256,category_key,service_code,target_kind,provider_role,target_version,operation_code,action_version,provider_contract_version,registry_evidence_key_id,registry_evidence_digest,local_state,target_envelope_version,target_algorithm,target_encryption_key_id,target_encapsulation,target_nonce,target_ciphertext,credential_envelope_version,credential_algorithm,credential_encryption_key_id,credential_encapsulation,credential_nonce,credential_ciphertext,credential_commitment_key_id,credential_source_commitment FROM privacy_worker_list_provider_targets($1,$2,$3,$4,$5)`, lease.Job.ID, lease.Job.ActiveLeaseID, lease.Job.ActiveAttemptID, lease.Job.LeaseEpoch, w.WorkerRef)
	if err != nil {
		return zero, ErrProviderExecutionFailed
	}
	type protected struct {
		binding                   ProviderTargetBinding
		registryKeyID             string
		registryDigest            []byte
		credentialCommitmentKeyID string
		credentialCommitment      []byte
		localState                string
		target, credential        ProviderTargetEnvelope
	}
	var targets []protected
	for rows.Next() {
		var target protected
		var credentialVersion, credentialAlgorithm, credentialKeyID *string
		if err = rows.Scan(&target.binding.TargetID, &target.binding.ExecutionID, &target.binding.JobID, &target.binding.CheckpointID,
			&target.binding.PlanEntrySHA256, &target.binding.Category, &target.binding.Service, &target.binding.TargetKind, &target.binding.Role,
			&target.binding.TargetVersion, &target.binding.OperationCode, &target.binding.ActionVersion, &target.binding.ProviderContractVersion,
			&target.registryKeyID, &target.registryDigest, &target.localState, &target.target.Version, &target.target.Algorithm, &target.target.KeyID, &target.target.Encapsulation,
			&target.target.Nonce, &target.target.Ciphertext, &credentialVersion, &credentialAlgorithm, &credentialKeyID,
			&target.credential.Encapsulation, &target.credential.Nonce, &target.credential.Ciphertext,
			&target.credentialCommitmentKeyID, &target.credentialCommitment); err != nil {
			rows.Close()
			return zero, ErrProviderExecutionFailed
		}
		if credentialVersion != nil {
			target.credential.Version = *credentialVersion
		}
		if credentialAlgorithm != nil {
			target.credential.Algorithm = *credentialAlgorithm
		}
		if credentialKeyID != nil {
			target.credential.KeyID = *credentialKeyID
		}
		targets = append(targets, target)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return zero, ErrProviderExecutionFailed
	}
	rows.Close()
	q := dbgen.New(w.Pool)
	for _, target := range targets {
		registration, ok := w.Registry.registration(target.binding.Service, target.binding.Role, target.binding.ProviderContractVersion, target.registryKeyID, target.registryDigest)
		if !ok {
			return zero, ErrProviderRegistryUnavailable
		}
		locator, openErr := OpenProviderTargetEnvelope(w.PrivateKey, target.binding, providerTargetLocatorVersion, target.target)
		if openErr != nil {
			return zero, ErrProviderExecutionFailed
		}
		credential := ProviderSecret{}
		if target.localState == "ACTIVE" {
			credential, openErr = OpenProviderTargetEnvelope(w.PrivateKey, target.binding, providerCredentialVersion, target.credential)
			if openErr != nil {
				return zero, ErrProviderExecutionFailed
			}
			credentialDigestKey, knownKey := w.CredentialDigestKeys[target.credentialCommitmentKeyID]
			credentialDigest, digestErr := digestProviderSecret(target.binding, credential.KeyID(), credential.Bytes(), target.credentialCommitmentKeyID,
				credentialDigestKey, "mycfc/privacy-provider-credential-commitment/v1", false)
			if !knownKey || digestErr != nil || !hmac.Equal(credentialDigest.Digest, target.credentialCommitment) {
				return zero, ErrProviderExecutionFailed
			}
		} else if target.localState != "DISCONNECTED" {
			return zero, ErrProviderExecutionFailed
		}
		request := ProviderErasureRequest{ServiceCode: target.binding.Service, Role: target.binding.Role, ContractVersion: target.binding.ProviderContractVersion,
			Target: locator, Credential: credential, AlreadyDisconnected: target.localState == "DISCONNECTED"}
		result, attempts, adapterErr := executeProviderAdapter(ctx, registration.Adapter, request, w.attempts(), w.timeout())
		if adapterErr != nil || !validProviderResult(target.binding.Role, result) {
			return zero, ErrProviderExecutionFailed
		}
		digest := providerEvidenceDigest(w.TranscriptKeyID, w.TranscriptKey, target.binding, lease, result, attempts,
			target.credentialCommitmentKeyID, target.credentialCommitment)
		_, err = q.RecordPrivacyWorkerProviderEvidence(ctx, dbgen.RecordPrivacyWorkerProviderEvidenceParams{
			TargetID: target.binding.TargetID, JobID: lease.Job.ID, LeaseID: lease.Job.ActiveLeaseID, AttemptID: lease.Job.ActiveAttemptID,
			LeaseEpoch: lease.Job.LeaseEpoch, WorkerRef: w.WorkerRef, OutcomeCode: string(result.Outcome), AdapterAttempts: int32(attempts),
			EvidenceCode: result.EvidenceCode, RecipientRole: optionalString(result.RecipientRole), ChannelCode: optionalString(result.ChannelCode),
			NotificationCode: optionalString(result.NotificationCode), ReasonCode: optionalString(result.ReasonCode), GuidanceCode: optionalString(result.GuidanceCode),
			TranscriptKeyID: w.TranscriptKeyID, TranscriptDigest: digest,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return zero, ErrLeaseLost
		}
		if err != nil {
			return zero, ErrProviderExecutionFailed
		}
	}
	checkpointID, err := q.CompletePrivacyWorkerProviderCheckpoint(ctx, dbgen.CompletePrivacyWorkerProviderCheckpointParams{JobID: lease.Job.ID,
		LeaseID: lease.Job.ActiveLeaseID, AttemptID: lease.Job.ActiveAttemptID, LeaseEpoch: lease.Job.LeaseEpoch, WorkerRef: w.WorkerRef})
	if errors.Is(err, pgx.ErrNoRows) {
		return zero, ErrLeaseLost
	}
	if err != nil {
		return zero, ErrProviderExecutionFailed
	}
	return q.GetPrivacyErasureJobCheckpoint(ctx, checkpointID)
}

func executeProviderAdapter(ctx context.Context, adapter ProviderErasureAdapter, request ProviderErasureRequest, maxAttempts int, timeout time.Duration) (ProviderErasureResult, int, error) {
	var result ProviderErasureResult
	var adapterErr error
	attempts := 0
	for attempts < maxAttempts {
		attempts++
		callCtx, cancel := context.WithTimeout(ctx, timeout)
		result, adapterErr = adapter.EraseOrNotify(callCtx, request)
		cancel()
		if adapterErr == nil || !isRetryableProviderError(adapterErr) {
			break
		}
	}
	return result, attempts, adapterErr
}

func validProviderResult(role ProviderRole, result ProviderErasureResult) bool {
	if len(result.EvidenceReference) == 0 || len(result.EvidenceReference) > 4096 || !slices.Contains([]string{"REMOTE_DELETION_RECEIPT", "REMOTE_ALREADY_DISCONNECTED", "RECIPIENT_NOTIFICATION_CONFIRMED"}, result.EvidenceCode) {
		return false
	}
	if result.Outcome == ProviderOutcomeDeletionVerified || result.Outcome == ProviderOutcomeAlreadyDisconnected {
		return (result.Outcome != ProviderOutcomeDeletionVerified || result.EvidenceCode == "REMOTE_DELETION_RECEIPT") &&
			(result.Outcome != ProviderOutcomeAlreadyDisconnected || result.EvidenceCode == "REMOTE_ALREADY_DISCONNECTED") &&
			result.RecipientRole == "" && result.ChannelCode == "" && result.NotificationCode == "" && result.ReasonCode == "" && result.GuidanceCode == ""
	}
	if result.Outcome != ProviderOutcomeNotControllable || role != ProviderRoleAutonomousRecipient || result.EvidenceCode != "RECIPIENT_NOTIFICATION_CONFIRMED" {
		return false
	}
	return result.RecipientRole == "AUTONOMOUS_RECIPIENT" &&
		slices.Contains([]string{"API", "EMAIL", "PORTAL", "REGISTERED_POST"}, result.ChannelCode) &&
		slices.Contains([]string{"ACCEPTED", "DELIVERED"}, result.NotificationCode) &&
		result.ReasonCode == "REGISTRY_DECLARED_AUTONOMOUS" && result.GuidanceCode == "CONTACT_RECIPIENT"
}

func providerEvidenceDigest(keyID string, key []byte, binding ProviderTargetBinding, lease ExecutionLease, result ProviderErasureResult, attempts int, credentialCommitmentKeyID string, credentialCommitment []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(encodeFields(providerEvidenceTranscript, keyID,
		string(encodeProviderTargetBinding(binding, keyID, providerEvidenceVersion)), lease.Job.ActiveAttemptID.String(),
		canonicalInt64(lease.Job.LeaseEpoch), string(result.Outcome), result.EvidenceCode, canonicalInt64(int64(attempts)), result.RecipientRole,
		result.ChannelCode, result.NotificationCode, result.ReasonCode, result.GuidanceCode, credentialCommitmentKeyID,
		string(credentialCommitment), string(result.EvidenceReference)))
	return mac.Sum(nil)
}
