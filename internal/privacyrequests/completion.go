package privacyrequests

import (
	"bytes"
	"context"
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

type VerifiedActivationEvidence struct {
	kind       string
	digest     []byte
	reference  string
	observedAt time.Time
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
	if s.Pool == nil || actorID == uuid.Nil || len(evidence.digest) != sha256.Size || evidence.observedAt.IsZero() {
		return result, ErrInvalid
	}
	err := s.Pool.QueryRow(ctx, `SELECT privacy_activation_record_evidence($1,$2,$3,$4,$5)`, actorID, evidence.kind, evidence.digest, evidence.reference, evidence.observedAt).Scan(&result.ID)
	if err != nil {
		return ActivationEvidence{}, completionControlError(err, ErrActivationUnavailable)
	}
	result.Kind, result.Digest = evidence.kind, append([]byte(nil), evidence.digest...)
	return result, nil
}

func (s Service) VerifyAndRecordActivationArtifact(ctx context.Context, actorID uuid.UUID, kind, contract string, payload []byte, observedAt, now time.Time) (ActivationEvidence, error) {
	evidence, err := VerifyActivationArtifact(kind, contract, payload, observedAt, now)
	if err != nil {
		return ActivationEvidence{}, err
	}
	return s.RecordActivationEvidence(ctx, actorID, evidence)
}

func (s Service) VerifyAndRecordRestoreActivationEvidence(ctx context.Context, actorID uuid.UUID, payload, authenticationKey []byte, now time.Time) (ActivationEvidence, error) {
	evidence, err := VerifyRestoreActivationAttestation(payload, authenticationKey, now)
	if err != nil {
		return ActivationEvidence{}, err
	}
	return s.RecordActivationEvidence(ctx, actorID, evidence)
}

var activationArtifactContracts = map[string]string{
	"INFRASTRUCTURE": "mycfc/privacy-infrastructure-posture/v1",
	"PROVIDER":       "mycfc/privacy-provider-registry/v1",
	"SCHEMA":         "mycfc/schema-migration-inventory/v1",
}

func VerifyActivationArtifact(kind, contract string, payload []byte, observedAt, now time.Time) (VerifiedActivationEvidence, error) {
	want, ok := activationArtifactContracts[kind]
	if !ok || contract != want || len(payload) == 0 || len(payload) > 1<<20 || observedAt.IsZero() || observedAt.After(now) || !observedAt.After(now.AddDate(0, 0, -90)) {
		return VerifiedActivationEvidence{}, ErrActivationUnavailable
	}
	var artifact struct {
		Contract   string `json:"contract"`
		Result     string `json:"result"`
		ObservedAt string `json:"observed_at"`
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if decoder.Decode(&artifact) != nil {
		return VerifiedActivationEvidence{}, ErrActivationUnavailable
	}
	var trailing any
	parsedObservedAt, observedErr := time.Parse(time.RFC3339, artifact.ObservedAt)
	if decoder.Decode(&trailing) != io.EOF || observedErr != nil || artifact.Contract != contract || artifact.Result != "SUCCEEDED" ||
		parsedObservedAt.Format(time.RFC3339) != artifact.ObservedAt || !parsedObservedAt.Equal(observedAt) {
		return VerifiedActivationEvidence{}, ErrActivationUnavailable
	}
	digest := sha256.Sum256(payload)
	return VerifiedActivationEvidence{kind: kind, digest: digest[:], reference: contract, observedAt: observedAt.UTC()}, nil
}

type restoreActivationAttestation struct {
	Contract              string `json:"contract"`
	Result                string `json:"result"`
	CompletedAt           string `json:"completed_at"`
	ValidUntil            string `json:"valid_until"`
	ImageDigest           string `json:"image_digest"`
	SchemaMigrationDigest string `json:"schema_migration_digest"`
	AuthHMACSHA256        string `json:"auth_hmac_sha256"`
	Backup                struct {
		CreatedAt         string `json:"created_at"`
		ManifestKeySHA256 string `json:"manifest_key_sha256"`
		ManifestVersion   string `json:"manifest_version"`
		ManifestSHA256    string `json:"manifest_sha256"`
		DumpKeySHA256     string `json:"dump_key_sha256"`
		DumpVersion       string `json:"dump_version"`
		DumpSHA256        string `json:"dump_sha256"`
	} `json:"backup"`
	Ledger struct {
		InventorySHA256 string `json:"inventory_sha256"`
		ObjectCount     int64  `json:"object_count"`
	} `json:"ledger"`
	Replay struct {
		ImportedCount        int64 `json:"imported_count"`
		ReplayedCount        int64 `json:"replayed_count"`
		AlreadyAppliedCount  int64 `json:"already_applied_count"`
		AbsenceVerifiedCount int64 `json:"absence_verified_count"`
	} `json:"replay"`
}

func VerifyRestoreActivationAttestation(payload, authenticationKey []byte, now time.Time) (VerifiedActivationEvidence, error) {
	if len(payload) == 0 || len(payload) > 1<<20 || len(authenticationKey) != sha256.Size {
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
	completedAt, completedErr := time.Parse(time.RFC3339, attestation.CompletedAt)
	validUntil, validErr := time.Parse(time.RFC3339, attestation.ValidUntil)
	if completedErr != nil || validErr != nil || attestation.Contract != "mycfc/privacy-restore-drill-attestation/v1" || attestation.Result != "SUCCEEDED" ||
		completedAt.Format(time.RFC3339) != attestation.CompletedAt || validUntil.Format(time.RFC3339) != attestation.ValidUntil ||
		completedAt.After(now) || !validUntil.After(now) || validUntil.Sub(completedAt) > 90*24*time.Hour || !completedAt.After(now.AddDate(0, 0, -90)) ||
		!validImageDigest(attestation.ImageDigest) || !validSHA256Hex(attestation.SchemaMigrationDigest) || !validSHA256Hex(attestation.AuthHMACSHA256) ||
		!validSHA256Hex(attestation.Backup.ManifestKeySHA256) || !validSHA256Hex(attestation.Backup.ManifestSHA256) ||
		!validSHA256Hex(attestation.Backup.DumpKeySHA256) || !validSHA256Hex(attestation.Backup.DumpSHA256) ||
		attestation.Backup.CreatedAt == "" || attestation.Backup.ManifestVersion == "" || attestation.Backup.DumpVersion == "" ||
		!validSHA256Hex(attestation.Ledger.InventorySHA256) || attestation.Ledger.ObjectCount < 0 || attestation.Replay.ImportedCount < 0 ||
		attestation.Replay.ReplayedCount <= 0 || attestation.Replay.AlreadyAppliedCount < 0 || attestation.Replay.AbsenceVerifiedCount != attestation.Replay.ReplayedCount ||
		attestation.Ledger.ObjectCount < attestation.Replay.ReplayedCount || attestation.Replay.ReplayedCount != attestation.Replay.ImportedCount+attestation.Replay.AlreadyAppliedCount {
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
	return VerifiedActivationEvidence{kind: "RESTORE", digest: digest[:], reference: attestation.Contract, observedAt: completedAt.UTC()}, nil
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
