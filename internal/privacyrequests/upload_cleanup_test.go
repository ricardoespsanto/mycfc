package privacyrequests

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/cfcoimbra/mycfc/internal/storage"
	"github.com/google/uuid"
)

type uploadCleanupStoreFake struct {
	claim    UploadCleanupClaim
	claimErr error
	complete *UploadCleanupResult
	failed   bool
	retry    bool
}

func (s *uploadCleanupStoreFake) Claim(context.Context, time.Duration, uuid.UUID) (UploadCleanupClaim, error) {
	return s.claim, s.claimErr
}
func (s *uploadCleanupStoreFake) Complete(_ context.Context, result UploadCleanupResult) error {
	s.complete = &result
	return nil
}
func (s *uploadCleanupStoreFake) Fail(_ context.Context, _ UploadCleanupClaim, _ uuid.UUID, retryable bool, _ time.Duration) error {
	s.failed, s.retry = true, retryable
	return nil
}

type uploadCleanupObjectsFake struct {
	key      string
	evidence storage.VersionDeletionEvidence
	err      error
}

func (s *uploadCleanupObjectsFake) DeleteAllVersions(_ context.Context, key string) (storage.VersionDeletionEvidence, error) {
	s.key = key
	return s.evidence, s.err
}

func TestUploadCleanupWorkerUsesSealedLocatorAndFencedEvidence(t *testing.T) {
	claim, privateKey := cleanupWorkerClaim(t)
	store := &uploadCleanupStoreFake{claim: claim}
	objects := &uploadCleanupObjectsFake{evidence: storage.VersionDeletionEvidence{DeletedVersions: 2, DeletedMarkers: 1, ListCalls: 4, StableChecks: 2}}
	worker := UploadCleanupWorker{Store: store, Objects: objects, WorkerRef: uuid.New(), PrivateKey: privateKey, TranscriptKeyID: "evidence-v1", TranscriptKey: bytes.Repeat([]byte{8}, 32)}
	worked, err := worker.RunOnce(context.Background())
	if err != nil || !worked || objects.key != "profiles/2026/09/private.png" || store.complete == nil || len(store.complete.TranscriptDigest) != 32 || store.failed {
		t.Fatalf("worked=%t err=%v key=%q complete=%#v failed=%t", worked, err, objects.key, store.complete, store.failed)
	}
}

func TestUploadCleanupWorkerBoundsRetriesAndFailsClosedOnCiphertext(t *testing.T) {
	claim, privateKey := cleanupWorkerClaim(t)
	for _, tc := range []struct {
		name      string
		mutate    func(*UploadCleanupClaim)
		objectErr error
		attempts  int32
		wantRetry bool
	}{
		{name: "retryable version failure", objectErr: storage.ErrVersionListing, attempts: 2, wantRetry: true},
		{name: "retry exhausted", objectErr: storage.ErrVersionDeletion, attempts: 5, wantRetry: false},
		{name: "corrupt ciphertext", mutate: func(c *UploadCleanupClaim) { c.Envelope.Ciphertext[0] ^= 0xff }, wantRetry: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current := claim
			current.Envelope.Ciphertext = bytes.Clone(claim.Envelope.Ciphertext)
			current.AttemptCount = tc.attempts
			if tc.mutate != nil {
				tc.mutate(&current)
			}
			store := &uploadCleanupStoreFake{claim: current}
			objects := &uploadCleanupObjectsFake{err: tc.objectErr}
			worker := UploadCleanupWorker{Store: store, Objects: objects, WorkerRef: uuid.New(), PrivateKey: privateKey, TranscriptKeyID: "evidence-v1", TranscriptKey: bytes.Repeat([]byte{8}, 32), MaxAttempts: 5}
			worked, err := worker.RunOnce(context.Background())
			if !worked || !errors.Is(err, ErrUploadCleanupFailed) || !store.failed || store.retry != tc.wantRetry || store.complete != nil {
				t.Fatalf("worked=%t err=%v failed=%t retry=%t complete=%#v", worked, err, store.failed, store.retry, store.complete)
			}
		})
	}
}

func cleanupWorkerClaim(t *testing.T) (UploadCleanupClaim, []byte) {
	t.Helper()
	privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	protector, err := NewX25519UploadIntentProtector("upload-key-v1", privateKey.PublicKey().Bytes(), "upload-digest-v1", bytes.Repeat([]byte{2}, 32))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 10, 15, 0, 0, 0, time.UTC)
	subject := uuid.New()
	binding := UploadIntentBinding{IntentID: uuid.New(), SubjectUserID: &subject, ProvenanceActorID: subject, SourceKind: "MEMBER_PROFILE_PHOTO", SourceRef: subject, Service: "private-media", TargetKind: "OBJECT_KEY", ProviderVersion: "s3-versioned/v1", ContentType: "image/png", SizeBytes: 5, CreatedAt: now, CleanupAfter: now.Add(24 * time.Hour)}
	envelope, err := protector.SealUploadObjectKey(binding, "profiles/2026/09/private.png")
	if err != nil {
		t.Fatal(err)
	}
	return UploadCleanupClaim{IntentID: binding.IntentID, AttemptID: uuid.New(), LeaseEpoch: 1, AttemptCount: 1, Binding: binding, Envelope: envelope}, privateKey.Bytes()
}
