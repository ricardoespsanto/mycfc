package privacyrequests

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/storage"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrUploadCleanupUnavailable = errors.New("privacy upload cleanup unavailable")
	ErrUploadCleanupFailed      = errors.New("privacy upload cleanup failed")
)

const uploadCleanupTranscriptVersion = "mycfc/privacy-upload-cleanup-transcript/v1"

type UploadCleanupClaim struct {
	IntentID, AttemptID uuid.UUID
	LeaseEpoch          int64
	AttemptCount        int32
	Binding             UploadIntentBinding
	Envelope            ObjectTargetEnvelope
}

type UploadCleanupResult struct {
	IntentID, AttemptID uuid.UUID
	LeaseEpoch          int64
	WorkerRef           uuid.UUID
	Evidence            storage.VersionDeletionEvidence
	TranscriptKeyID     string
	TranscriptDigest    []byte
}

type UploadCleanupStore interface {
	Claim(context.Context, time.Duration, uuid.UUID) (UploadCleanupClaim, error)
	Complete(context.Context, UploadCleanupResult) error
	Fail(context.Context, UploadCleanupClaim, uuid.UUID, bool, time.Duration) error
}

type PostgresUploadCleanupStore struct{ DB dbgen.DBTX }

func (s PostgresUploadCleanupStore) Claim(ctx context.Context, lease time.Duration, workerRef uuid.UUID) (UploadCleanupClaim, error) {
	var claim UploadCleanupClaim
	if s.DB == nil || workerRef == uuid.Nil || lease < time.Second || lease > time.Hour {
		return claim, ErrUploadCleanupUnavailable
	}
	var subject *uuid.UUID
	err := s.DB.QueryRow(ctx, `SELECT intent_id,lease_epoch,attempt_id,attempt_count,subject_user_id,provenance_actor_user_id,source_kind,source_ref,
service_code,target_kind,provider_contract_version,envelope_version,algorithm,encryption_key_id,encapsulation,nonce,ciphertext,
content_type,size_bytes,cleanup_after,created_at FROM privacy_upload_cleanup_claim($1,$2)`, lease.Milliseconds(), workerRef).Scan(
		&claim.IntentID, &claim.LeaseEpoch, &claim.AttemptID, &claim.AttemptCount, &subject, &claim.Binding.ProvenanceActorID,
		&claim.Binding.SourceKind, &claim.Binding.SourceRef, &claim.Binding.Service, &claim.Binding.TargetKind, &claim.Binding.ProviderVersion,
		&claim.Envelope.Version, &claim.Envelope.Algorithm, &claim.Envelope.KeyID, &claim.Envelope.Encapsulation, &claim.Envelope.Nonce,
		&claim.Envelope.Ciphertext, &claim.Binding.ContentType, &claim.Binding.SizeBytes, &claim.Binding.CleanupAfter, &claim.Binding.CreatedAt,
	)
	if err != nil {
		return claim, err
	}
	claim.Binding.IntentID, claim.Binding.SubjectUserID = claim.IntentID, subject
	return claim, nil
}

func (s PostgresUploadCleanupStore) Complete(ctx context.Context, result UploadCleanupResult) error {
	if s.DB == nil {
		return ErrUploadCleanupUnavailable
	}
	return dbgen.New(s.DB).CompletePrivacyUploadCleanup(ctx, dbgen.CompletePrivacyUploadCleanupParams{
		IntentID: result.IntentID, AttemptID: result.AttemptID, LeaseEpoch: result.LeaseEpoch, WorkerRef: result.WorkerRef,
		DeletedVersions: int32(result.Evidence.DeletedVersions), DeletedMarkers: int32(result.Evidence.DeletedMarkers),
		ListCalls: int32(result.Evidence.ListCalls), StableChecks: int32(result.Evidence.StableChecks),
		TranscriptKeyID: result.TranscriptKeyID, TranscriptDigest: result.TranscriptDigest,
	})
}

func (s PostgresUploadCleanupStore) Fail(ctx context.Context, claim UploadCleanupClaim, workerRef uuid.UUID, retryable bool, delay time.Duration) error {
	if s.DB == nil {
		return ErrUploadCleanupUnavailable
	}
	return dbgen.New(s.DB).FailPrivacyUploadCleanup(ctx, dbgen.FailPrivacyUploadCleanupParams{
		IntentID: claim.IntentID, AttemptID: claim.AttemptID, LeaseEpoch: claim.LeaseEpoch, WorkerRef: workerRef,
		Retryable: retryable, RetryDelayMilliseconds: delay.Milliseconds(),
	})
}

type UploadCleanupWorker struct {
	Store           UploadCleanupStore
	Objects         storage.VersionedObjectStore
	WorkerRef       uuid.UUID
	PrivateKey      []byte
	TranscriptKeyID string
	TranscriptKey   []byte
	LeaseDuration   time.Duration
	MaxAttempts     int32
	RetryDelay      time.Duration
}

func (w UploadCleanupWorker) RunOnce(ctx context.Context) (bool, error) {
	if w.Store == nil || w.Objects == nil || w.WorkerRef == uuid.Nil || len(w.PrivateKey) != 32 || !keyIdentifier.MatchString(w.TranscriptKeyID) || len(w.TranscriptKey) < 32 {
		return false, ErrUploadCleanupUnavailable
	}
	lease := w.LeaseDuration
	if lease == 0 {
		lease = 5 * time.Minute
	}
	claim, err := w.Store.Claim(ctx, lease, w.WorkerRef)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, ErrUploadCleanupFailed
	}
	objectKey, err := OpenUploadIntentEnvelope(w.PrivateKey, claim.Binding, claim.Envelope)
	if err != nil {
		if failErr := w.Store.Fail(ctx, claim, w.WorkerRef, false, 0); failErr != nil {
			return true, ErrUploadCleanupFailed
		}
		return true, ErrUploadCleanupFailed
	}
	evidence, err := w.Objects.DeleteAllVersions(ctx, objectKey)
	if err != nil {
		maxAttempts := w.MaxAttempts
		if maxAttempts < 1 {
			maxAttempts = 5
		}
		retryable := claim.AttemptCount < maxAttempts
		delay := w.RetryDelay
		if delay <= 0 {
			delay = time.Minute
		}
		if failErr := w.Store.Fail(ctx, claim, w.WorkerRef, retryable, delay); failErr != nil {
			return true, ErrUploadCleanupFailed
		}
		return true, ErrUploadCleanupFailed
	}
	digest := uploadCleanupTranscriptDigest(w.TranscriptKey, claim, evidence)
	if err = w.Store.Complete(ctx, UploadCleanupResult{IntentID: claim.IntentID, AttemptID: claim.AttemptID, LeaseEpoch: claim.LeaseEpoch, WorkerRef: w.WorkerRef, Evidence: evidence, TranscriptKeyID: w.TranscriptKeyID, TranscriptDigest: digest}); err != nil {
		return true, ErrUploadCleanupFailed
	}
	return true, nil
}

func uploadCleanupTranscriptDigest(key []byte, claim UploadCleanupClaim, evidence storage.VersionDeletionEvidence) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(encodeFields(uploadCleanupTranscriptVersion, claim.IntentID.String(), claim.AttemptID.String(), canonicalInt64(claim.LeaseEpoch),
		canonicalInt64(int64(evidence.DeletedVersions)), canonicalInt64(int64(evidence.DeletedMarkers)), canonicalInt64(int64(evidence.ListCalls)), canonicalInt64(int64(evidence.StableChecks))))
	return mac.Sum(nil)
}
