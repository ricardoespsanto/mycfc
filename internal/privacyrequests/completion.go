package privacyrequests

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrCompletionUnavailable     = errors.New("privacy completion is unavailable")
	ErrCompletionLinkUnavailable = errors.New("privacy completion link is unavailable")
	ErrRequeueUnavailable        = errors.New("privacy terminal requeue is unavailable")
	ErrActivationUnavailable     = errors.New("privacy activation evidence is unavailable")
)

const completionTokenBytes = 32

type CompletionWorker struct {
	Pool          *pgxpool.Pool
	WorkerRef     uuid.UUID
	Key           []byte
	DetailBaseURL string
	Random        func([]byte) (int, error)
}

type CompletionResult struct {
	ExecutionID     uuid.UUID
	AlreadyComplete bool
}

func (w CompletionWorker) ListPending(ctx context.Context, limit int32) ([]uuid.UUID, error) {
	if !w.valid() || limit < 1 || limit > 100 {
		return nil, ErrInvalid
	}
	rows, err := w.Pool.Query(ctx, `SELECT execution_id FROM privacy_completion_list_pending($1,$2)`, w.WorkerRef, limit)
	if err != nil {
		return nil, completionControlError(err, ErrCompletionUnavailable)
	}
	defer rows.Close()
	result := make([]uuid.UUID, 0, limit)
	for rows.Next() {
		var id uuid.UUID
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		result = append(result, id)
	}
	return result, rows.Err()
}

func (w CompletionWorker) ActivationReady(ctx context.Context) (bool, error) {
	if w.Pool == nil || w.WorkerRef == uuid.Nil {
		return false, ErrInvalid
	}
	var ready bool
	if err := w.Pool.QueryRow(ctx, `SELECT privacy_worker_activation_ready()`).Scan(&ready); err != nil {
		return false, completionControlError(err, ErrActivationUnavailable)
	}
	return ready, nil
}

type CompletionDetail struct {
	RequestReference uuid.UUID
	CompletedAt      time.Time
	Status           string
	ManifestSHA256   string
	Categories       int32
	Checkpoints      int32
	ObjectTargets    int32
	ProviderTargets  int32
}

func (w CompletionWorker) valid() bool {
	return w.Pool != nil && w.WorkerRef != uuid.Nil && len(w.Key) >= 32 && validCompletionBaseURL(w.DetailBaseURL)
}

func validCompletionBaseURL(value string) bool {
	u, err := url.Parse(value)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.RawQuery == "" && u.Fragment == "" && !strings.ContainsAny(value, "\r\n")
}

func (w CompletionWorker) random() func([]byte) (int, error) {
	if w.Random != nil {
		return w.Random
	}
	return rand.Read
}

func completionLink(baseURL, token string) (string, error) {
	if !validCompletionBaseURL(baseURL) || token == "" || strings.ContainsAny(token, "/?&#\r\n") {
		return "", ErrInvalid
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", ErrInvalid
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + token
	return u.String(), nil
}

func (w CompletionWorker) Complete(ctx context.Context, executionID uuid.UUID) (CompletionResult, error) {
	var result CompletionResult
	if !w.valid() || executionID == uuid.Nil {
		return result, ErrInvalid
	}
	var exists bool
	if err := w.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM privacy_erasure_completion_manifests WHERE execution_id=$1)`, executionID).Scan(&exists); err != nil {
		return result, completionControlError(err, ErrCompletionUnavailable)
	}
	if exists {
		return CompletionResult{ExecutionID: executionID, AlreadyComplete: true}, nil
	}
	var sealedTarget []byte
	if err := w.Pool.QueryRow(ctx, `SELECT sealed_delivery FROM privacy_completion_prepare($1,$2)`, executionID, w.WorkerRef).Scan(&sealedTarget); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return result, ErrCompletionUnavailable
		}
		return result, completionControlError(err, ErrCompletionUnavailable)
	}
	payload, digest, _, err := prepareCompletionDelivery(w.Key, sealedTarget, w.DetailBaseURL, w.random())
	if err != nil {
		return result, completionControlError(err, ErrCompletionUnavailable)
	}
	var completedID uuid.UUID
	if err = w.Pool.QueryRow(ctx, `SELECT privacy_completion_finalize($1,$2,$3,$4)`, executionID, w.WorkerRef, digest, payload).Scan(&completedID); err != nil {
		if checkErr := w.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM privacy_erasure_completion_manifests WHERE execution_id=$1)`, executionID).Scan(&exists); checkErr == nil && exists {
			return CompletionResult{ExecutionID: executionID, AlreadyComplete: true}, nil
		}
		return result, err
	}
	if completedID != executionID {
		return result, ErrCompletionUnavailable
	}
	return CompletionResult{ExecutionID: completedID}, nil
}

func prepareCompletionDelivery(key, sealedTarget []byte, baseURL string, random func([]byte) (int, error)) ([]byte, []byte, string, error) {
	target, err := OpenDelivery(key, sealedTarget)
	if err != nil || random == nil {
		return nil, nil, "", ErrCompletionUnavailable
	}
	rawToken := make([]byte, completionTokenBytes)
	if n, randomErr := random(rawToken); randomErr != nil || n != len(rawToken) {
		if randomErr != nil {
			return nil, nil, "", randomErr
		}
		return nil, nil, "", ErrCompletionUnavailable
	}
	token := base64.RawURLEncoding.EncodeToString(rawToken)
	link, err := completionLink(baseURL, token)
	if err != nil {
		return nil, nil, "", err
	}
	payload, err := SealDelivery(key, Delivery{Recipient: target.Recipient, ContactURL: link})
	if err != nil {
		return nil, nil, "", err
	}
	digest := sha256.Sum256(rawToken)
	return payload, digest[:], token, nil
}

func (s Service) ConsumeCompletionDetail(ctx context.Context, token string) (CompletionDetail, error) {
	var result CompletionDetail
	digest, valid := completionTokenDigest(token)
	if s.Pool == nil || !valid {
		return result, ErrCompletionLinkUnavailable
	}
	var completed pgtype.Timestamptz
	var summary struct {
		Status          string `json:"status"`
		ManifestSHA256  string `json:"manifest_sha256"`
		Categories      int32  `json:"categories"`
		Checkpoints     int32  `json:"checkpoints"`
		ObjectTargets   int32  `json:"object_targets"`
		ProviderTargets int32  `json:"provider_targets"`
	}
	var summaryBytes []byte
	err := s.Pool.QueryRow(ctx, `SELECT request_ref,completed_at,summary FROM privacy_completion_consume($1)`, digest).Scan(&result.RequestReference, &completed, &summaryBytes)
	if errors.Is(err, pgx.ErrNoRows) {
		return CompletionDetail{}, ErrCompletionLinkUnavailable
	}
	if err != nil || !completed.Valid || json.Unmarshal(summaryBytes, &summary) != nil || summary.Status != "COMPLETED" {
		if err != nil {
			return CompletionDetail{}, err
		}
		return CompletionDetail{}, ErrCompletionLinkUnavailable
	}
	if _, err = hex.DecodeString(summary.ManifestSHA256); err != nil || len(summary.ManifestSHA256) != sha256.Size*2 {
		return CompletionDetail{}, ErrCompletionLinkUnavailable
	}
	result.CompletedAt = completed.Time
	result.Status = summary.Status
	result.ManifestSHA256 = summary.ManifestSHA256
	result.Categories = summary.Categories
	result.Checkpoints = summary.Checkpoints
	result.ObjectTargets = summary.ObjectTargets
	result.ProviderTargets = summary.ProviderTargets
	return result, nil
}

func (s Service) ValidateCompletionLink(ctx context.Context, token string) error {
	digest, valid := completionTokenDigest(token)
	if s.Pool == nil || !valid {
		return ErrCompletionLinkUnavailable
	}
	var available bool
	if err := s.Pool.QueryRow(ctx, `SELECT privacy_completion_validate($1)`, digest).Scan(&available); err != nil {
		return completionControlError(err, ErrCompletionLinkUnavailable)
	}
	if !available {
		return ErrCompletionLinkUnavailable
	}
	return nil
}

func completionTokenDigest(token string) ([]byte, bool) {
	if len(token) != base64.RawURLEncoding.EncodedLen(completionTokenBytes) {
		return nil, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != completionTokenBytes {
		return nil, false
	}
	digest := sha256.Sum256(raw)
	return digest[:], true
}

type TerminalRequeueProposal struct {
	ID     uuid.UUID
	Digest []byte
}

type ControlProposal struct {
	ID         uuid.UUID
	Digest     []byte
	ProposedAt time.Time
}

type CompletionControlJob struct {
	JobID                  uuid.UUID
	CategoryCode           string
	PurposeCode            string
	Status                 string
	AttemptCount           int32
	FailureStage           string
	FailureCode            string
	CanProposeRequeue      bool
	CanApproveRequeue      bool
	PendingRequeueProposal *ControlProposal
}

type CompletionControlSnapshot struct {
	RequestReference uuid.UUID
	RequestStatus    string
	ExecutionID      uuid.UUID
	ExecutionStatus  string
	Jobs             []CompletionControlJob
}

func (s Service) CompletionControlSnapshot(ctx context.Context, actorID, requestReference uuid.UUID) (CompletionControlSnapshot, error) {
	var result CompletionControlSnapshot
	if s.Pool == nil || actorID == uuid.Nil || requestReference == uuid.Nil {
		return result, ErrInvalid
	}
	rows, err := s.Pool.Query(ctx, `SELECT request_reference,request_status,execution_id,execution_status,job_id,category_code,purpose_code,
job_status,attempt_count,failure_stage,failure_code,proposal_id,proposal_sha256,proposed_at,can_propose,can_approve
FROM privacy_completion_control_snapshot($1,$2)`, actorID, requestReference)
	if err != nil {
		return result, completionControlError(err, ErrForbidden)
	}
	defer rows.Close()
	for rows.Next() {
		var job CompletionControlJob
		var failureStage, failureCode pgtype.Text
		var proposalID pgtype.UUID
		var proposalDigest []byte
		var proposedAt pgtype.Timestamptz
		if err = rows.Scan(&result.RequestReference, &result.RequestStatus, &result.ExecutionID, &result.ExecutionStatus,
			&job.JobID, &job.CategoryCode, &job.PurposeCode, &job.Status, &job.AttemptCount, &failureStage, &failureCode,
			&proposalID, &proposalDigest, &proposedAt, &job.CanProposeRequeue, &job.CanApproveRequeue); err != nil {
			return CompletionControlSnapshot{}, err
		}
		job.FailureStage, job.FailureCode = failureStage.String, failureCode.String
		if proposalID.Valid && proposedAt.Valid && len(proposalDigest) == sha256.Size {
			job.PendingRequeueProposal = &ControlProposal{ID: proposalID.Bytes, Digest: append([]byte(nil), proposalDigest...), ProposedAt: proposedAt.Time}
		}
		result.Jobs = append(result.Jobs, job)
	}
	if err = rows.Err(); err != nil {
		return CompletionControlSnapshot{}, err
	}
	if len(result.Jobs) == 0 {
		return CompletionControlSnapshot{}, ErrCompletionUnavailable
	}
	return result, nil
}

func (s Service) ProposeTerminalRequeue(ctx context.Context, actorID, jobID uuid.UUID) (TerminalRequeueProposal, error) {
	var result TerminalRequeueProposal
	if s.Pool == nil || actorID == uuid.Nil || jobID == uuid.Nil {
		return result, ErrInvalid
	}
	err := s.Pool.QueryRow(ctx, `SELECT proposal_id,proposal_sha256 FROM privacy_terminal_requeue_propose($1,$2)`, jobID, actorID).Scan(&result.ID, &result.Digest)
	if err != nil {
		return TerminalRequeueProposal{}, completionControlError(err, ErrRequeueUnavailable)
	}
	return result, nil
}

func (s Service) ApproveTerminalRequeue(ctx context.Context, actorID, proposalID uuid.UUID, digest []byte) error {
	if s.Pool == nil || actorID == uuid.Nil || proposalID == uuid.Nil || len(digest) != sha256.Size {
		return ErrInvalid
	}
	var jobID uuid.UUID
	if err := s.Pool.QueryRow(ctx, `SELECT privacy_terminal_requeue_approve($1,$2,$3)`, proposalID, digest, actorID).Scan(&jobID); err != nil {
		return completionControlError(err, ErrRequeueUnavailable)
	}
	if jobID == uuid.Nil {
		return ErrRequeueUnavailable
	}
	return nil
}

type ActivationEvidence struct {
	ID     uuid.UUID
	Kind   string
	Digest []byte
}

type ActivationReleaseBinding struct {
	PolicyVersion         string
	ExecutorVersion       string
	PlanSchemaVersion     string
	ImageDigest           string
	SchemaMigrationDigest string
}

func (binding ActivationReleaseBinding) valid() bool {
	return policyKey.MatchString(binding.PolicyVersion) && binding.ExecutorVersion == SupportedExecutorVersion &&
		binding.PlanSchemaVersion == SupportedPlanSchemaVersion && validImageDigest(binding.ImageDigest) && validSHA256Hex(binding.SchemaMigrationDigest)
}

type VerifiedActivationEvidence struct {
	kind       string
	digest     []byte
	reference  string
	observedAt time.Time
	expiresAt  time.Time
	artifact   activationArtifactRecord
}

type activationArtifactRecord struct {
	PolicyVersion                               string `json:"policy_version"`
	ExecutorVersion                             string `json:"executor_version"`
	PlanSchemaVersion                           string `json:"plan_schema_version"`
	ImageDigest                                 string `json:"image_digest"`
	EvidenceRef                                 string `json:"evidence_ref,omitempty"`
	EvidenceSHA256                              []byte `json:"evidence_sha256"`
	SigningKeyID                                string `json:"signing_key_id,omitempty"`
	SchemaMigrationDigest                       []byte `json:"schema_migration_digest,omitempty"`
	BaselineIncludesThrough                     string `json:"baseline_includes_through,omitempty"`
	ProductionStateSerial                       int64  `json:"production_state_serial,omitempty"`
	HetznerStateSerial                          int64  `json:"hetzner_state_serial,omitempty"`
	ProductionStateSHA256                       []byte `json:"production_state_sha256,omitempty"`
	HetznerStateSHA256                          []byte `json:"hetzner_state_sha256,omitempty"`
	ProductionPlanSHA256                        []byte `json:"production_plan_sha256,omitempty"`
	HetznerPlanSHA256                           []byte `json:"hetzner_plan_sha256,omitempty"`
	WorkerIdentityEnabled                       *bool  `json:"worker_identity_enabled,omitempty"`
	S3VersionDeletionEnabled                    *bool  `json:"s3_version_deletion_enabled,omitempty"`
	LedgerBrokerInvokeEnabled                   *bool  `json:"ledger_broker_invoke_enabled,omitempty"`
	WorkerMonitoringEnabled                     *bool  `json:"worker_monitoring_enabled,omitempty"`
	RestoreInfrastructureEnabled                *bool  `json:"restore_infrastructure_enabled,omitempty"`
	RestoreLedgerWriteEnabled                   *bool  `json:"restore_ledger_write_enabled,omitempty"`
	ProviderRegistryState                       string `json:"provider_registry_state,omitempty"`
	ProviderRegistrationCount                   *int64 `json:"provider_registration_count,omitempty"`
	ProviderRegistrySHA256                      []byte `json:"provider_registry_sha256,omitempty"`
	ProviderInventoryContract                   string `json:"provider_inventory_contract,omitempty"`
	RestoreInputSource                          string `json:"restore_input_source,omitempty"`
	RestoreInputContract                        string `json:"restore_input_contract,omitempty"`
	RestoreReplayContract                       string `json:"restore_replay_contract,omitempty"`
	RestoreClosureContract                      string `json:"restore_closure_contract,omitempty"`
	RestoreCandidateSHA256                      []byte `json:"restore_candidate_sha256,omitempty"`
	RestoreInventorySHA256                      []byte `json:"restore_inventory_sha256,omitempty"`
	RestoreObjectCount                          int64  `json:"restore_object_count,omitempty"`
	RestoreReplayedCount                        int64  `json:"restore_replayed_count,omitempty"`
	RestoreSyntheticCount                       *int64 `json:"restore_synthetic_count,omitempty"`
	RestoreObserverSHA256                       []byte `json:"restore_observer_sha256,omitempty"`
	RestoreMembershipPostconditionContract      string `json:"restore_membership_postcondition_contract,omitempty"`
	RestoreMembershipPostconditionSHA256        []byte `json:"restore_membership_postcondition_sha256,omitempty"`
	RestoreMembershipPostconditionVerifiedCount int64  `json:"restore_membership_postcondition_verified_count,omitempty"`
	RestoreMembershipCount                      int64  `json:"restore_membership_count,omitempty"`
	RestoreVariationCount                       int64  `json:"restore_variation_count,omitempty"`
}

type ActivationProposal struct {
	ID     uuid.UUID
	Digest []byte
}

type ActivationEvidenceSummary struct {
	ID         uuid.UUID
	Kind       string
	ObservedAt time.Time
}

type ActivationControlSnapshot struct {
	PolicyVersion   string
	Ready           bool
	Evidence        []ActivationEvidenceSummary
	PendingProposal *ControlProposal
	CanPropose      bool
	CanRenew        bool
	CanApprove      bool
}

func (s Service) RecordActivationEvidence(ctx context.Context, actorID uuid.UUID, evidence VerifiedActivationEvidence) (ActivationEvidence, error) {
	var result ActivationEvidence
	if s.Pool == nil || actorID == uuid.Nil || len(evidence.digest) != sha256.Size || evidence.observedAt.IsZero() ||
		evidence.expiresAt.IsZero() || len(evidence.artifact.EvidenceSHA256) != sha256.Size {
		return result, ErrInvalid
	}
	artifact, err := json.Marshal(evidence.artifact)
	if err != nil {
		return result, ErrInvalid
	}
	err = s.Pool.QueryRow(ctx, `SELECT privacy_activation_broker_record_authenticated_evidence($1,$2,$3,$4,$5,$6,$7)`,
		actorID, evidence.kind, evidence.digest, evidence.reference, evidence.observedAt, evidence.expiresAt, artifact).Scan(&result.ID)
	if err != nil {
		return ActivationEvidence{}, completionControlError(err, ErrActivationUnavailable)
	}
	result.Kind, result.Digest = evidence.kind, append([]byte(nil), evidence.digest...)
	return result, nil
}

func (s Service) VerifyAndRecordActivationArtifact(ctx context.Context, actorID uuid.UUID, payload []byte, trustedKeys map[string]ed25519.PublicKey, release ActivationReleaseBinding, now time.Time) (ActivationEvidence, error) {
	evidence, err := VerifyActivationArtifact(payload, trustedKeys, release, now)
	if err != nil {
		return ActivationEvidence{}, err
	}
	return s.RecordActivationEvidence(ctx, actorID, evidence)
}

func (s Service) VerifyAndRecordRestoreActivationEvidence(ctx context.Context, actorID uuid.UUID, payload, authenticationKey []byte, release ActivationReleaseBinding, now time.Time) (ActivationEvidence, error) {
	evidence, err := VerifyRestoreActivationAttestation(payload, authenticationKey, release, now)
	if err != nil {
		return ActivationEvidence{}, err
	}
	return s.RecordActivationEvidence(ctx, actorID, evidence)
}

var activationArtifactContracts = map[string]string{
	"INFRASTRUCTURE": "mycfc/privacy-infrastructure-posture/v1",
	"PROVIDER":       "mycfc/privacy-provider-registry/v2",
	"SCHEMA":         "mycfc/schema-migration-inventory/v1",
}

type signedActivationArtifact struct {
	Contract          string `json:"contract"`
	Result            string `json:"result"`
	ObservedAt        string `json:"observed_at"`
	PolicyVersion     string `json:"policy_version"`
	ExecutorVersion   string `json:"executor_version"`
	PlanSchemaVersion string `json:"plan_schema_version"`
	ImageDigest       string `json:"image_digest"`
	EvidenceRef       string `json:"evidence_ref"`
	EvidenceSHA256    string `json:"evidence_sha256"`
	SigningKeyID      string `json:"signing_key_id"`
	SignatureEd25519  string `json:"signature_ed25519"`
}

type infrastructureActivationArtifact struct {
	signedActivationArtifact
	ProductionStateSerial        int64  `json:"production_state_serial"`
	HetznerStateSerial           int64  `json:"hetzner_state_serial"`
	ProductionStateSHA256        string `json:"production_state_sha256"`
	HetznerStateSHA256           string `json:"hetzner_state_sha256"`
	ProductionPlanSHA256         string `json:"production_plan_sha256"`
	HetznerPlanSHA256            string `json:"hetzner_plan_sha256"`
	WorkerIdentityEnabled        bool   `json:"worker_identity_enabled"`
	S3VersionDeletionEnabled     bool   `json:"s3_version_deletion_enabled"`
	LedgerBrokerInvokeEnabled    bool   `json:"ledger_broker_invoke_enabled"`
	WorkerMonitoringEnabled      bool   `json:"worker_monitoring_enabled"`
	RestoreInfrastructureEnabled bool   `json:"restore_infrastructure_enabled"`
	RestoreLedgerWriteEnabled    bool   `json:"restore_ledger_write_enabled"`
}

type providerActivationArtifact struct {
	signedActivationArtifact
	RegistryState          string `json:"registry_state"`
	RegistrationCount      int64  `json:"registration_count"`
	ProviderRegistrySHA256 string `json:"provider_registry_sha256"`
	InventoryContract      string `json:"inventory_contract"`
}

type schemaActivationArtifact struct {
	signedActivationArtifact
	SchemaMigrationDigest   string `json:"schema_migration_digest"`
	BaselineIncludesThrough string `json:"baseline_includes_through"`
}

func VerifyActivationArtifact(payload []byte, trustedKeys map[string]ed25519.PublicKey, release ActivationReleaseBinding, now time.Time) (VerifiedActivationEvidence, error) {
	if len(payload) == 0 || len(payload) > 1<<20 || now.IsZero() || len(trustedKeys) == 0 || !release.valid() {
		return VerifiedActivationEvidence{}, ErrActivationUnavailable
	}
	var header signedActivationArtifact
	if !decodeExactJSON(payload, &header) {
		// The common header intentionally rejects kind-specific fields, so use a
		// small untyped pass only to select the exact closed schema below.
		var selector struct {
			Contract string `json:"contract"`
		}
		if json.Unmarshal(payload, &selector) != nil {
			return VerifiedActivationEvidence{}, ErrActivationUnavailable
		}
		header.Contract = selector.Contract
	}
	kind := ""
	for candidate, contract := range activationArtifactContracts {
		if header.Contract == contract {
			kind = candidate
			break
		}
	}
	var record activationArtifactRecord
	switch kind {
	case "INFRASTRUCTURE":
		var artifact infrastructureActivationArtifact
		if !decodeExactJSON(payload, &artifact) || artifact.ProductionStateSerial <= 0 || artifact.HetznerStateSerial <= 0 ||
			!validSHA256Hex(artifact.ProductionStateSHA256) || !validSHA256Hex(artifact.HetznerStateSHA256) ||
			!validSHA256Hex(artifact.ProductionPlanSHA256) || !validSHA256Hex(artifact.HetznerPlanSHA256) ||
			!artifact.WorkerIdentityEnabled || !artifact.S3VersionDeletionEnabled || !artifact.LedgerBrokerInvokeEnabled ||
			!artifact.WorkerMonitoringEnabled || !artifact.RestoreInfrastructureEnabled || !artifact.RestoreLedgerWriteEnabled {
			return VerifiedActivationEvidence{}, ErrActivationUnavailable
		}
		header = artifact.signedActivationArtifact
		record.ProductionStateSerial, record.HetznerStateSerial = artifact.ProductionStateSerial, artifact.HetznerStateSerial
		record.ProductionStateSHA256, _ = hex.DecodeString(artifact.ProductionStateSHA256)
		record.HetznerStateSHA256, _ = hex.DecodeString(artifact.HetznerStateSHA256)
		record.ProductionPlanSHA256, _ = hex.DecodeString(artifact.ProductionPlanSHA256)
		record.HetznerPlanSHA256, _ = hex.DecodeString(artifact.HetznerPlanSHA256)
		enabled := true
		record.WorkerIdentityEnabled, record.S3VersionDeletionEnabled, record.LedgerBrokerInvokeEnabled = &enabled, &enabled, &enabled
		record.WorkerMonitoringEnabled, record.RestoreInfrastructureEnabled, record.RestoreLedgerWriteEnabled = &enabled, &enabled, &enabled
	case "PROVIDER":
		var artifact providerActivationArtifact
		if !decodeExactJSON(payload, &artifact) || artifact.RegistryState != "READY" || artifact.RegistrationCount != 0 ||
			artifact.InventoryContract != "mycfc/privacy-provider-registry-source/v2" || !validSHA256Hex(artifact.ProviderRegistrySHA256) {
			return VerifiedActivationEvidence{}, ErrActivationUnavailable
		}
		header = artifact.signedActivationArtifact
		record.ProviderRegistryState, record.ProviderRegistrationCount = artifact.RegistryState, &artifact.RegistrationCount
		record.ProviderRegistrySHA256, _ = hex.DecodeString(artifact.ProviderRegistrySHA256)
		record.ProviderInventoryContract = artifact.InventoryContract
	case "SCHEMA":
		var artifact schemaActivationArtifact
		if !decodeExactJSON(payload, &artifact) || !validSHA256Hex(artifact.SchemaMigrationDigest) ||
			artifact.SchemaMigrationDigest != release.SchemaMigrationDigest || artifact.BaselineIncludesThrough != "202609110004_guardian_authority_verification" {
			return VerifiedActivationEvidence{}, ErrActivationUnavailable
		}
		header = artifact.signedActivationArtifact
		record.SchemaMigrationDigest, _ = hex.DecodeString(artifact.SchemaMigrationDigest)
		record.BaselineIncludesThrough = artifact.BaselineIncludesThrough
	default:
		return VerifiedActivationEvidence{}, ErrActivationUnavailable
	}
	observedAt, err := time.Parse(time.RFC3339, header.ObservedAt)
	if err != nil || observedAt.Format(time.RFC3339) != header.ObservedAt || observedAt.After(now) || !observedAt.After(now.Add(-90*24*time.Hour)) ||
		header.Result != "SUCCEEDED" || !policyKey.MatchString(header.PolicyVersion) || header.PolicyVersion != release.PolicyVersion || header.ExecutorVersion != release.ExecutorVersion ||
		header.PlanSchemaVersion != release.PlanSchemaVersion || header.ImageDigest != release.ImageDigest ||
		!validImageDigest(header.ImageDigest) || !validSHA256Hex(header.EvidenceSHA256) || !validActivationEvidenceRef(header.EvidenceRef) ||
		!keyIdentifier.MatchString(header.SigningKeyID) {
		return VerifiedActivationEvidence{}, ErrActivationUnavailable
	}
	key, ok := trustedKeys[header.SigningKeyID]
	signature, signatureErr := base64.StdEncoding.DecodeString(header.SignatureEd25519)
	canonical, canonicalErr := canonicalJSONWithoutField(payload, "signature_ed25519")
	if !ok || len(key) != ed25519.PublicKeySize || signatureErr != nil || len(signature) != ed25519.SignatureSize || canonicalErr != nil ||
		!ed25519.Verify(key, canonical, signature) {
		return VerifiedActivationEvidence{}, ErrActivationUnavailable
	}
	record.PolicyVersion, record.ExecutorVersion, record.PlanSchemaVersion = header.PolicyVersion, header.ExecutorVersion, header.PlanSchemaVersion
	record.ImageDigest, record.EvidenceRef, record.SigningKeyID = header.ImageDigest, header.EvidenceRef, header.SigningKeyID
	record.EvidenceSHA256, _ = hex.DecodeString(header.EvidenceSHA256)
	digest := sha256.Sum256(payload)
	return VerifiedActivationEvidence{kind: kind, digest: digest[:], reference: header.Contract, observedAt: observedAt.UTC(), expiresAt: observedAt.Add(90 * 24 * time.Hour).UTC(), artifact: record}, nil
}

func decodeExactJSON(payload []byte, target any) bool {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if decoder.Decode(target) != nil {
		return false
	}
	var trailing any
	return decoder.Decode(&trailing) == io.EOF
}

func canonicalJSONWithoutField(payload []byte, field string) ([]byte, error) {
	var document map[string]any
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if decoder.Decode(&document) != nil {
		return nil, ErrActivationUnavailable
	}
	delete(document, field)
	return json.Marshal(document)
}

func validActivationEvidenceRef(value string) bool {
	if len(value) > 2048 {
		return false
	}
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "s3" || u.Host == "" || u.User != nil || u.Fragment != "" || u.Path == "" || u.Path == "/" {
		return false
	}
	query, err := url.ParseQuery(u.RawQuery)
	versionID := query.Get("versionId")
	return err == nil && len(query) == 1 && len(query["versionId"]) == 1 && len(versionID) <= 1024 && versionID != "" &&
		u.RawQuery == "versionId="+url.QueryEscape(versionID)
}

type immutableRestoreObject struct {
	Ref            string `json:"ref"`
	SHA256         string `json:"sha256"`
	ChecksumSHA256 string `json:"checksum_sha256"`
	KMSKeyARN      string `json:"kms_key_arn"`
	SizeBytes      int64  `json:"size_bytes"`
}

type restoreActivationAttestation struct {
	Contract              string `json:"contract"`
	Result                string `json:"result"`
	ObservedAt            string `json:"observed_at"`
	ValidUntil            string `json:"valid_until"`
	PolicyVersion         string `json:"policy_version"`
	ExecutorVersion       string `json:"executor_version"`
	PlanSchemaVersion     string `json:"plan_schema_version"`
	ImageDigest           string `json:"image_digest"`
	SchemaMigrationDigest string `json:"schema_migration_digest"`
	AuthHMACSHA256        string `json:"auth_hmac_sha256"`
	Contracts             struct {
		Backup           string `json:"backup"`
		LedgerInput      string `json:"ledger_input"`
		ReplayResult     string `json:"replay_result"`
		Replay           string `json:"replay"`
		Closure          string `json:"closure"`
		SyntheticFixture string `json:"synthetic_fixture"`
	} `json:"contracts"`
	Backup struct {
		CreatedAt string                 `json:"created_at"`
		Manifest  immutableRestoreObject `json:"manifest"`
		Dump      immutableRestoreObject `json:"dump"`
	} `json:"backup"`
	Ledger struct {
		InputSource     string `json:"input_source"`
		InventorySHA256 string `json:"inventory_sha256"`
		ObjectCount     int64  `json:"object_count"`
	} `json:"ledger"`
	Candidate struct {
		ResultSHA256                         string `json:"result_sha256"`
		ObjectCount                          int64  `json:"object_count"`
		ImportedCount                        int64  `json:"imported_count"`
		ReplayedCount                        int64  `json:"replayed_count"`
		AlreadyAppliedCount                  int64  `json:"already_applied_count"`
		NonReplayableV1Count                 int64  `json:"non_replayable_v1_count"`
		AbsenceVerifiedCount                 int64  `json:"absence_verified_count"`
		SyntheticReplayedCount               int64  `json:"synthetic_replayed_count"`
		ClosureV4Count                       int64  `json:"closure_v4_count"`
		IntentOnlyCount                      int64  `json:"intent_only_count"`
		LegacyClosureV2Count                 int64  `json:"legacy_closure_v2_count"`
		ErasureEffectiveAtVerifiedCount      int64  `json:"erasure_effective_at_verified_count"`
		FailedCount                          int64  `json:"failed_count"`
		MembershipPostconditionContract      string `json:"membership_postcondition_contract"`
		MembershipPostconditionSHA256        string `json:"membership_postcondition_sha256"`
		MembershipPostconditionVerifiedCount int64  `json:"membership_postcondition_verified_count"`
		MembershipCount                      int64  `json:"membership_count"`
		VariationCount                       int64  `json:"variation_count"`
	} `json:"candidate"`
	Observer struct {
		ImageDigest                          string `json:"image_digest"`
		ReplayCount                          int64  `json:"replay_count"`
		SourceAlreadyAppliedCount            int64  `json:"source_already_applied_count"`
		SyntheticCount                       int64  `json:"synthetic_count"`
		VerifiedRunCount                     int64  `json:"verified_run_count"`
		ExpectedCheckpointCount              int64  `json:"expected_checkpoint_count"`
		SucceededCheckpointCount             int64  `json:"succeeded_checkpoint_count"`
		ProviderAbsentCount                  int64  `json:"provider_absent_count"`
		ConsentClockVerifiedCount            int64  `json:"consent_clock_verified_count"`
		ClosureV4Count                       int64  `json:"closure_v4_count"`
		ErasureEffectiveAtVerifiedCount      int64  `json:"erasure_effective_at_verified_count"`
		MembershipPostconditionContract      string `json:"membership_postcondition_contract"`
		MembershipPostconditionSHA256        string `json:"membership_postcondition_sha256"`
		MembershipPostconditionVerifiedCount int64  `json:"membership_postcondition_verified_count"`
		MembershipCount                      int64  `json:"membership_count"`
		VariationCount                       int64  `json:"variation_count"`
		EvidenceSHA256                       string `json:"evidence_sha256"`
	} `json:"observer"`
	Evidence immutableRestoreObject `json:"evidence"`
}

func VerifyRestoreActivationAttestation(payload, authenticationKey []byte, release ActivationReleaseBinding, now time.Time) (VerifiedActivationEvidence, error) {
	if len(payload) == 0 || len(payload) > 1<<20 || len(authenticationKey) != sha256.Size || !release.valid() {
		return VerifiedActivationEvidence{}, ErrActivationUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	var attestation restoreActivationAttestation
	if decoder.Decode(&attestation) != nil {
		return VerifiedActivationEvidence{}, ErrActivationUnavailable
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return VerifiedActivationEvidence{}, ErrActivationUnavailable
	}
	observedAt, observedErr := time.Parse(time.RFC3339, attestation.ObservedAt)
	validUntil, validErr := time.Parse(time.RFC3339, attestation.ValidUntil)
	backupCreatedAt, backupErr := time.Parse(time.RFC3339, attestation.Backup.CreatedAt)
	if observedErr != nil || validErr != nil || backupErr != nil || attestation.Contract != "mycfc/privacy-restore-drill-attestation/v2" || attestation.Result != "SUCCEEDED" ||
		observedAt.Format(time.RFC3339) != attestation.ObservedAt || validUntil.Format(time.RFC3339) != attestation.ValidUntil || backupCreatedAt.Format(time.RFC3339) != attestation.Backup.CreatedAt ||
		observedAt.After(now) || backupCreatedAt.After(observedAt) || !validUntil.After(now) || !validUntil.Equal(observedAt.Add(90*24*time.Hour)) || !observedAt.After(now.Add(-90*24*time.Hour)) ||
		attestation.PolicyVersion != release.PolicyVersion || attestation.ExecutorVersion != release.ExecutorVersion || attestation.PlanSchemaVersion != release.PlanSchemaVersion ||
		attestation.ImageDigest != release.ImageDigest || attestation.SchemaMigrationDigest != release.SchemaMigrationDigest || !validSHA256Hex(attestation.AuthHMACSHA256) ||
		attestation.Contracts.Backup != "mycfc/postgres-backup/v3" || attestation.Contracts.LedgerInput != "mycfc/privacy-restore-ledger-input/v2" ||
		attestation.Contracts.ReplayResult != "mycfc/privacy-restore-replay-result/v2" || attestation.Contracts.Replay != "relational-erasure-replay/v1" ||
		attestation.Contracts.Closure != TombstoneClosureVersion || attestation.Contracts.SyntheticFixture != "mycfc/privacy-restore-synthetic-fixture/v1" ||
		!validImmutableRestoreObject(attestation.Backup.Manifest) || !validImmutableRestoreObject(attestation.Backup.Dump) || !validImmutableRestoreObject(attestation.Evidence) ||
		!validSHA256Hex(attestation.Ledger.InventorySHA256) || attestation.Ledger.ObjectCount < 0 || !validSHA256Hex(attestation.Candidate.ResultSHA256) ||
		attestation.Candidate.ObjectCount != attestation.Ledger.ObjectCount || attestation.Candidate.ImportedCount < 0 || attestation.Candidate.ReplayedCount <= 0 ||
		attestation.Candidate.AlreadyAppliedCount < 0 || attestation.Candidate.ReplayedCount != attestation.Candidate.ImportedCount+attestation.Candidate.AlreadyAppliedCount ||
		attestation.Candidate.AbsenceVerifiedCount != attestation.Candidate.ReplayedCount || attestation.Candidate.ClosureV4Count != attestation.Candidate.ReplayedCount ||
		attestation.Candidate.ErasureEffectiveAtVerifiedCount != attestation.Candidate.ReplayedCount || attestation.Candidate.NonReplayableV1Count != 0 ||
		attestation.Candidate.IntentOnlyCount != 0 || attestation.Candidate.LegacyClosureV2Count != 0 || attestation.Candidate.FailedCount != 0 ||
		attestation.Candidate.MembershipPostconditionContract != MembershipHistoryPostconditionVersion || !validSHA256Hex(attestation.Candidate.MembershipPostconditionSHA256) ||
		attestation.Candidate.MembershipPostconditionVerifiedCount != attestation.Candidate.ReplayedCount || attestation.Candidate.MembershipCount < 0 || attestation.Candidate.VariationCount < 0 ||
		!validImageDigest(attestation.Observer.ImageDigest) || !validSHA256Hex(attestation.Observer.EvidenceSHA256) ||
		attestation.Observer.ReplayCount != attestation.Candidate.ReplayedCount || attestation.Observer.SourceAlreadyAppliedCount != attestation.Candidate.AlreadyAppliedCount ||
		attestation.Observer.SyntheticCount != attestation.Candidate.SyntheticReplayedCount || attestation.Observer.VerifiedRunCount != attestation.Candidate.ReplayedCount ||
		attestation.Observer.ExpectedCheckpointCount <= 0 || attestation.Observer.SucceededCheckpointCount != attestation.Observer.ExpectedCheckpointCount ||
		attestation.Observer.ProviderAbsentCount != attestation.Candidate.ReplayedCount || attestation.Observer.ConsentClockVerifiedCount != attestation.Candidate.ReplayedCount ||
		attestation.Observer.ClosureV4Count != attestation.Candidate.ReplayedCount || attestation.Observer.ErasureEffectiveAtVerifiedCount != attestation.Candidate.ReplayedCount ||
		attestation.Observer.MembershipPostconditionContract != attestation.Candidate.MembershipPostconditionContract ||
		attestation.Observer.MembershipPostconditionSHA256 != attestation.Candidate.MembershipPostconditionSHA256 ||
		attestation.Observer.MembershipPostconditionVerifiedCount != attestation.Candidate.ReplayedCount ||
		attestation.Observer.MembershipCount != attestation.Candidate.MembershipCount || attestation.Observer.VariationCount != attestation.Candidate.VariationCount ||
		!((attestation.Ledger.InputSource == "LIVE_LEDGER" && attestation.Candidate.SyntheticReplayedCount == 0) ||
			(attestation.Ledger.InputSource == "SYNTHETIC_BOOTSTRAP" && attestation.Candidate.SyntheticReplayedCount == attestation.Candidate.ReplayedCount)) {
		return VerifiedActivationEvidence{}, ErrActivationUnavailable
	}
	var canonical map[string]any
	canonicalDecoder := json.NewDecoder(bytes.NewReader(payload))
	canonicalDecoder.UseNumber()
	if canonicalDecoder.Decode(&canonical) != nil {
		return VerifiedActivationEvidence{}, ErrActivationUnavailable
	}
	delete(canonical, "auth_hmac_sha256")
	canonicalBytes, err := json.Marshal(canonical)
	if err != nil {
		return VerifiedActivationEvidence{}, ErrActivationUnavailable
	}
	wantMAC, err := hex.DecodeString(attestation.AuthHMACSHA256)
	if err != nil {
		return VerifiedActivationEvidence{}, ErrActivationUnavailable
	}
	mac := hmac.New(sha256.New, authenticationKey)
	_, _ = mac.Write(canonicalBytes)
	if !hmac.Equal(wantMAC, mac.Sum(nil)) {
		return VerifiedActivationEvidence{}, ErrActivationUnavailable
	}
	digest := sha256.Sum256(payload)
	schemaDigest, _ := hex.DecodeString(attestation.SchemaMigrationDigest)
	inventoryDigest, _ := hex.DecodeString(attestation.Ledger.InventorySHA256)
	candidateDigest, _ := hex.DecodeString(attestation.Candidate.ResultSHA256)
	observerDigest, _ := hex.DecodeString(attestation.Observer.EvidenceSHA256)
	membershipDigest, _ := hex.DecodeString(attestation.Candidate.MembershipPostconditionSHA256)
	evidenceDigest, _ := hex.DecodeString(attestation.Evidence.SHA256)
	syntheticCount := attestation.Candidate.SyntheticReplayedCount
	return VerifiedActivationEvidence{kind: "RESTORE", digest: digest[:], reference: attestation.Contract, observedAt: observedAt.UTC(), expiresAt: validUntil.UTC(), artifact: activationArtifactRecord{
		PolicyVersion: attestation.PolicyVersion, ExecutorVersion: attestation.ExecutorVersion, PlanSchemaVersion: attestation.PlanSchemaVersion,
		ImageDigest: attestation.ImageDigest, EvidenceRef: attestation.Evidence.Ref, EvidenceSHA256: evidenceDigest, SchemaMigrationDigest: schemaDigest,
		RestoreInputSource: attestation.Ledger.InputSource, RestoreInputContract: attestation.Contracts.LedgerInput,
		RestoreReplayContract: attestation.Contracts.Replay, RestoreClosureContract: attestation.Contracts.Closure,
		RestoreCandidateSHA256: candidateDigest, RestoreInventorySHA256: inventoryDigest,
		RestoreObjectCount: attestation.Ledger.ObjectCount, RestoreReplayedCount: attestation.Candidate.ReplayedCount,
		RestoreSyntheticCount: &syntheticCount, RestoreObserverSHA256: observerDigest,
		RestoreMembershipPostconditionContract:      attestation.Candidate.MembershipPostconditionContract,
		RestoreMembershipPostconditionSHA256:        membershipDigest,
		RestoreMembershipPostconditionVerifiedCount: attestation.Candidate.MembershipPostconditionVerifiedCount,
		RestoreMembershipCount:                      attestation.Candidate.MembershipCount, RestoreVariationCount: attestation.Candidate.VariationCount,
	}}, nil
}

func validImmutableRestoreObject(object immutableRestoreObject) bool {
	return validActivationEvidenceRef(object.Ref) && validSHA256Hex(object.SHA256) && object.ChecksumSHA256 == object.SHA256 &&
		strings.HasPrefix(object.KMSKeyARN, "arn:aws:kms:") && len(object.KMSKeyARN) <= 2048 && object.SizeBytes > 0
}

func validImageDigest(value string) bool {
	return strings.HasPrefix(value, "sha256:") && validSHA256Hex(strings.TrimPrefix(value, "sha256:"))
}

func validSHA256Hex(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
}

func (s Service) ProposeActivation(ctx context.Context, actorID uuid.UUID, policyVersion string, evidenceIDs []uuid.UUID) (ActivationProposal, error) {
	var result ActivationProposal
	if s.Pool == nil || actorID == uuid.Nil || strings.TrimSpace(policyVersion) == "" || len(evidenceIDs) != 4 {
		return result, ErrInvalid
	}
	err := s.Pool.QueryRow(ctx, `SELECT proposal_id,activation_sha256 FROM privacy_activation_propose($1,$2,$3)`, actorID, policyVersion, evidenceIDs).Scan(&result.ID, &result.Digest)
	if err != nil {
		return ActivationProposal{}, completionControlError(err, ErrActivationUnavailable)
	}
	return result, nil
}

func (s Service) ApproveActivation(ctx context.Context, actorID, proposalID uuid.UUID, digest []byte) error {
	if s.Pool == nil || actorID == uuid.Nil || proposalID == uuid.Nil || len(digest) != sha256.Size {
		return ErrInvalid
	}
	var approvalID uuid.UUID
	if err := s.Pool.QueryRow(ctx, `SELECT privacy_activation_approve($1,$2,$3)`, actorID, proposalID, digest).Scan(&approvalID); err != nil {
		return completionControlError(err, ErrActivationUnavailable)
	}
	if approvalID == uuid.Nil {
		return ErrActivationUnavailable
	}
	return nil
}

func (s Service) ActivationControlSnapshot(ctx context.Context, actorID uuid.UUID) (ActivationControlSnapshot, error) {
	var result ActivationControlSnapshot
	if s.Pool == nil || actorID == uuid.Nil {
		return result, ErrInvalid
	}
	rows, err := s.Pool.Query(ctx, `SELECT policy_version,ready,evidence_id,evidence_kind,evidence_observed_at,
proposal_id,proposal_sha256,proposal_created_at,can_propose,can_renew,can_approve FROM privacy_activation_control_snapshot($1)`, actorID)
	if err != nil {
		return result, completionControlError(err, ErrForbidden)
	}
	defer rows.Close()
	for rows.Next() {
		var evidenceID, proposalID pgtype.UUID
		var evidenceKind pgtype.Text
		var observedAt, proposedAt pgtype.Timestamptz
		var proposalDigest []byte
		if err = rows.Scan(&result.PolicyVersion, &result.Ready, &evidenceID, &evidenceKind, &observedAt,
			&proposalID, &proposalDigest, &proposedAt, &result.CanPropose, &result.CanRenew, &result.CanApprove); err != nil {
			return ActivationControlSnapshot{}, err
		}
		if evidenceID.Valid && evidenceKind.Valid && observedAt.Valid {
			result.Evidence = append(result.Evidence, ActivationEvidenceSummary{ID: evidenceID.Bytes, Kind: evidenceKind.String, ObservedAt: observedAt.Time})
		}
		if result.PendingProposal == nil && proposalID.Valid && proposedAt.Valid && len(proposalDigest) == sha256.Size {
			result.PendingProposal = &ControlProposal{ID: proposalID.Bytes, Digest: append([]byte(nil), proposalDigest...), ProposedAt: proposedAt.Time}
		}
	}
	if err = rows.Err(); err != nil {
		return ActivationControlSnapshot{}, err
	}
	if result.PolicyVersion == "" {
		return ActivationControlSnapshot{}, ErrActivationUnavailable
	}
	return result, nil
}

func completionControlError(err, fallback error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		return fallback
	}
	return err
}
