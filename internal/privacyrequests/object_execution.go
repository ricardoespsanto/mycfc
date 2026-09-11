package privacyrequests

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/storage"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrObjectExecutionUnavailable = errors.New("privacy object execution unavailable")
	ErrObjectExecutionFailed      = errors.New("privacy object execution failed")
)

const objectEvidenceTranscriptVersion = "mycfc/privacy-object-evidence-transcript/v1"

type capturedMediaSource struct {
	JobID, CheckpointID, SourceRef, UploadIntentID uuid.UUID
	PlanEntrySHA256                                []byte
	Category, SourceKind, ObjectKey                string
}

func (s Service) materializeObjectTargets(ctx context.Context, tx pgx.Tx, q *dbgen.Queries, execution dbgen.PrivacyErasureExecution, plan ExecutionPlan, subjectID uuid.UUID) error {
	for _, entry := range plan.Entries {
		if !containsOperation(entry.Operations, "OBJECT_VERSION_DELETE") {
			continue
		}
		if s.ObjectTargets == nil {
			return ErrExecutorUnavailable
		}
		rows, err := tx.Query(ctx, `SELECT job_id,checkpoint_id,plan_entry_sha256,category_key,source_kind,source_ref,upload_intent_id,object_key
FROM privacy_execution_capture_media_sources($1,$2,$3)`, execution.ID, subjectID, entry.Category)
		if err != nil {
			return ErrExecutorUnavailable
		}
		var sources []capturedMediaSource
		for rows.Next() {
			var source capturedMediaSource
			if err = rows.Scan(&source.JobID, &source.CheckpointID, &source.PlanEntrySHA256, &source.Category, &source.SourceKind,
				&source.SourceRef, &source.UploadIntentID, &source.ObjectKey); err != nil {
				rows.Close()
				return err
			}
			sources = append(sources, source)
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, source := range sources {
			contract, known := mediaSourceContractFor(source.SourceKind)
			if !known || contract.Category != source.Category {
				return ErrExecutorUnavailable
			}
			targetID := uuid.New()
			binding := ObjectTargetBinding{
				ExecutionID: execution.ID, JobID: source.JobID, CheckpointID: source.CheckpointID, TargetID: targetID,
				PlanEntrySHA256: source.PlanEntrySHA256, Category: source.Category, Service: "private-media", TargetKind: "OBJECT_KEY",
				SourceKind: source.SourceKind, SourceRef: source.SourceRef, OperationCode: "OBJECT_VERSION_DELETE",
				ActionVersion: SupportedActionVersion, ProviderContractVersion: "s3-versioned/v1",
			}
			envelope, sealErr := s.ObjectTargets.SealObjectKey(binding, source.ObjectKey)
			if sealErr != nil {
				return ErrExecutorUnavailable
			}
			digest, digestErr := s.ObjectTargets.DigestObjectKey(execution.ID, binding.Service, source.ObjectKey)
			if digestErr != nil {
				return ErrExecutorUnavailable
			}
			if _, err = q.MaterializePrivacyObjectTarget(ctx, dbgen.MaterializePrivacyObjectTargetParams{
				TargetID: targetID, ExecutionID: execution.ID, JobID: source.JobID, CheckpointID: source.CheckpointID,
				PlanEntrySha256: source.PlanEntrySHA256, CategoryKey: source.Category, SourceKind: source.SourceKind,
				SourceRef: source.SourceRef, UploadIntentID: source.UploadIntentID, ObjectKey: source.ObjectKey,
				EnvelopeVersion: envelope.Version, Algorithm: envelope.Algorithm, EncryptionKeyID: envelope.KeyID,
				Encapsulation: envelope.Encapsulation, Nonce: envelope.Nonce, Ciphertext: envelope.Ciphertext,
				DigestKeyID: digest.KeyID, LocatorDigest: digest.Digest,
			}); err != nil {
				return ErrExecutorUnavailable
			}
		}
		if _, err = q.CompletePrivacyObjectCapture(ctx, dbgen.CompletePrivacyObjectCaptureParams{ExecutionID: execution.ID, CategoryKey: entry.Category}); err != nil {
			return ErrExecutorUnavailable
		}
	}
	return nil
}

func containsOperation(operations []string, want string) bool {
	for _, operation := range operations {
		if operation == want {
			return true
		}
	}
	return false
}

// ObjectExecutionWorker handles only execution-bound object checkpoints. It
// never receives upload-lifecycle credentials and cannot complete relational
// work. Deletion may happen before evidence is committed; a retry therefore
// repeats authoritative listing and converges from already-absent state.
type ObjectExecutionWorker struct {
	Pool            dbgen.DBTX
	Objects         storage.VersionedObjectStore
	WorkerRef       uuid.UUID
	PrivateKey      []byte
	TranscriptKeyID string
	TranscriptKey   []byte
}

func (w ObjectExecutionWorker) CompleteCheckpoint(ctx context.Context, lease ExecutionLease) (dbgen.PrivacyErasureJobCheckpoint, error) {
	var zero dbgen.PrivacyErasureJobCheckpoint
	if w.Pool == nil || w.Objects == nil || w.WorkerRef == uuid.Nil || len(w.PrivateKey) != 32 ||
		!keyIdentifier.MatchString(w.TranscriptKeyID) || len(w.TranscriptKey) < 32 || !validLease(lease) {
		return zero, ErrObjectExecutionUnavailable
	}
	rows, err := w.Pool.Query(ctx, `SELECT target_id,execution_id,job_id,checkpoint_id,plan_entry_sha256,category_key,service_code,target_kind,
source_kind,source_ref,operation_code,action_version,provider_contract_version,envelope_version,algorithm,encryption_key_id,encapsulation,nonce,ciphertext
FROM privacy_worker_list_object_targets($1,$2,$3,$4,$5)`, lease.Job.ID, lease.Job.ActiveLeaseID, lease.Job.ActiveAttemptID, lease.Job.LeaseEpoch, w.WorkerRef)
	if err != nil {
		return zero, ErrObjectExecutionFailed
	}
	type protectedTarget struct {
		binding  ObjectTargetBinding
		envelope ObjectTargetEnvelope
	}
	var targets []protectedTarget
	for rows.Next() {
		var target protectedTarget
		if err = rows.Scan(&target.binding.TargetID, &target.binding.ExecutionID, &target.binding.JobID, &target.binding.CheckpointID,
			&target.binding.PlanEntrySHA256, &target.binding.Category, &target.binding.Service, &target.binding.TargetKind,
			&target.binding.SourceKind, &target.binding.SourceRef, &target.binding.OperationCode, &target.binding.ActionVersion,
			&target.binding.ProviderContractVersion, &target.envelope.Version, &target.envelope.Algorithm, &target.envelope.KeyID,
			&target.envelope.Encapsulation, &target.envelope.Nonce, &target.envelope.Ciphertext); err != nil {
			rows.Close()
			return zero, ErrObjectExecutionFailed
		}
		targets = append(targets, target)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return zero, ErrObjectExecutionFailed
	}
	rows.Close()
	q := dbgen.New(w.Pool)
	for _, target := range targets {
		objectKey, openErr := OpenObjectTargetEnvelope(w.PrivateKey, target.binding, target.envelope)
		if openErr != nil {
			return zero, ErrObjectExecutionFailed
		}
		evidence, deleteErr := w.Objects.DeleteAllVersions(ctx, objectKey)
		if deleteErr != nil {
			return zero, ErrObjectExecutionFailed
		}
		digest := objectEvidenceTranscriptDigest(w.TranscriptKey, target.binding, lease, evidence)
		_, err = q.RecordPrivacyWorkerObjectEvidence(ctx, dbgen.RecordPrivacyWorkerObjectEvidenceParams{
			TargetID: target.binding.TargetID, JobID: lease.Job.ID, LeaseID: lease.Job.ActiveLeaseID,
			AttemptID: lease.Job.ActiveAttemptID, LeaseEpoch: lease.Job.LeaseEpoch, WorkerRef: w.WorkerRef,
			DeletedVersions: int32(evidence.DeletedVersions), DeletedMarkers: int32(evidence.DeletedMarkers),
			ListCalls: int32(evidence.ListCalls), StableChecks: int32(evidence.StableChecks),
			TranscriptKeyID: w.TranscriptKeyID, TranscriptDigest: digest,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return zero, ErrLeaseLost
		}
		if err != nil {
			return zero, ErrObjectExecutionFailed
		}
	}
	checkpointID, err := q.CompletePrivacyWorkerObjectCheckpoint(ctx, dbgen.CompletePrivacyWorkerObjectCheckpointParams{
		JobID: lease.Job.ID, LeaseID: lease.Job.ActiveLeaseID, AttemptID: lease.Job.ActiveAttemptID,
		LeaseEpoch: lease.Job.LeaseEpoch, WorkerRef: w.WorkerRef,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return zero, ErrLeaseLost
	}
	if err != nil {
		return zero, ErrObjectExecutionFailed
	}
	checkpoint, err := q.GetPrivacyErasureJobCheckpoint(ctx, checkpointID)
	if err != nil {
		return zero, err
	}
	return checkpoint, nil
}

func objectEvidenceTranscriptDigest(key []byte, binding ObjectTargetBinding, lease ExecutionLease, evidence storage.VersionDeletionEvidence) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(encodeFields(objectEvidenceTranscriptVersion, binding.ExecutionID.String(), binding.JobID.String(), binding.CheckpointID.String(),
		binding.TargetID.String(), lease.Job.ActiveAttemptID.String(), canonicalInt64(lease.Job.LeaseEpoch),
		canonicalInt64(int64(evidence.DeletedVersions)), canonicalInt64(int64(evidence.DeletedMarkers)),
		canonicalInt64(int64(evidence.ListCalls)), canonicalInt64(int64(evidence.StableChecks))))
	return mac.Sum(nil)
}
