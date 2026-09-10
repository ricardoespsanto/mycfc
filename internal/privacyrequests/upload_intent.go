package privacyrequests

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/cfcoimbra/mycfc/internal/storage"
	"github.com/google/uuid"
)

var (
	ErrUploadProvenanceUnavailable = errors.New("upload provenance unavailable")
	ErrUploadProvenanceFailed      = errors.New("upload provenance failed")
)

type UploadIntentBegin struct {
	IntentID, ActorUserID, SourceRef uuid.UUID
	SubjectUserID                    *uuid.UUID
	SourceKind, ServiceCode          string
	ContentType                      string
	SizeBytes                        int64
	HoldToken                        []byte
}

type UploadIntentClock struct {
	CreatedAt, CleanupAfter time.Time
	HoldEpoch               int64
}

type UploadIntentProtectedRecord struct {
	IntentID          uuid.UUID
	HoldToken         []byte
	LocatorCommitment []byte
	Envelope          ObjectTargetEnvelope
	Digest            ObjectTargetDigest
}

type UploadIntentStore interface {
	Begin(context.Context, UploadIntentBegin) (UploadIntentClock, error)
	Finalize(context.Context, UploadIntentProtectedRecord) error
	ConfirmPut(context.Context, uuid.UUID, []byte) error
	MarkCleanup(context.Context, uuid.UUID, []byte, string) error
}

type UploadInput struct {
	SubjectUserID *uuid.UUID
	ActorUserID   uuid.UUID
	SourceKind    string
	SourceRef     uuid.UUID
}

type PreparedUpload struct {
	IntentID    uuid.UUID
	HoldEpoch   int64
	HoldToken   []byte
	ObjectKey   string
	ContentType string
	SizeBytes   int64
}

type UploadCoordinator struct {
	Store     UploadIntentStore
	Objects   storage.ObjectStore
	Protector UploadIntentProtector
	Now       func() time.Time
	Random    io.Reader
}

func (c UploadCoordinator) Upload(ctx context.Context, input UploadInput, photo storage.ValidatedPhoto) (PreparedUpload, error) {
	var zero PreparedUpload
	if c.Store == nil || c.Objects == nil || c.Protector == nil || input.ActorUserID == uuid.Nil || input.SourceRef == uuid.Nil ||
		!validUploadContentType(photo.ContentType) || photo.Size < 1 || int64(len(photo.Bytes)) != photo.Size {
		return zero, ErrUploadProvenanceUnavailable
	}
	intentID := uuid.New()
	objectKey, err := c.objectKey(input.SourceKind, photo.Extension)
	if err != nil {
		return zero, ErrUploadProvenanceFailed
	}
	holdToken := make([]byte, 32)
	reader := c.Random
	if reader == nil {
		reader = rand.Reader
	}
	if _, err = io.ReadFull(reader, holdToken); err != nil {
		return zero, ErrUploadProvenanceFailed
	}
	begin := UploadIntentBegin{
		IntentID: intentID, SubjectUserID: input.SubjectUserID, ActorUserID: input.ActorUserID, SourceKind: input.SourceKind,
		SourceRef: input.SourceRef, ServiceCode: "private-media", ContentType: photo.ContentType, SizeBytes: photo.Size, HoldToken: holdToken,
	}
	clock, err := c.Store.Begin(ctx, begin)
	if err != nil {
		return zero, ErrUploadProvenanceFailed
	}
	binding := UploadIntentBinding{
		IntentID: intentID, SubjectUserID: input.SubjectUserID, ProvenanceActorID: input.ActorUserID, SourceKind: input.SourceKind,
		SourceRef: input.SourceRef, Service: "private-media", TargetKind: "OBJECT_KEY", ProviderVersion: "s3-versioned/v1",
		ContentType: photo.ContentType, SizeBytes: photo.Size, CreatedAt: clock.CreatedAt, CleanupAfter: clock.CleanupAfter,
	}
	envelope, err := c.Protector.SealUploadObjectKey(binding, objectKey)
	if err != nil {
		return zero, ErrUploadProvenanceFailed
	}
	digest, err := c.Protector.DigestUploadObjectKey(binding.Service, objectKey)
	if err != nil {
		return zero, ErrUploadProvenanceFailed
	}
	locatorCommitment := sha256.Sum256([]byte(objectKey))
	if err = c.Store.Finalize(ctx, UploadIntentProtectedRecord{IntentID: intentID, HoldToken: holdToken, LocatorCommitment: locatorCommitment[:], Envelope: envelope, Digest: digest}); err != nil {
		return zero, ErrUploadProvenanceFailed
	}
	if err = c.Objects.PutObject(ctx, objectKey, photo.ContentType, photo.Size, bytes.NewReader(photo.Bytes)); err != nil {
		_ = c.Store.MarkCleanup(context.WithoutCancel(ctx), intentID, holdToken, "PUT_AMBIGUOUS")
		return zero, ErrUploadProvenanceFailed
	}
	if err = c.Store.ConfirmPut(ctx, intentID, holdToken); err != nil {
		_ = c.Store.MarkCleanup(context.WithoutCancel(ctx), intentID, holdToken, "PUT_AMBIGUOUS")
		return zero, ErrUploadProvenanceFailed
	}
	return PreparedUpload{IntentID: intentID, HoldEpoch: clock.HoldEpoch, HoldToken: holdToken, ObjectKey: objectKey, ContentType: photo.ContentType, SizeBytes: photo.Size}, nil
}

func (c UploadCoordinator) AttachmentFailed(ctx context.Context, upload PreparedUpload) error {
	if c.Store == nil || upload.IntentID == uuid.Nil || len(upload.HoldToken) != 32 {
		return ErrUploadProvenanceUnavailable
	}
	if err := c.Store.MarkCleanup(ctx, upload.IntentID, upload.HoldToken, "ATTACH_FAILED"); err != nil {
		return ErrUploadProvenanceFailed
	}
	return nil
}

func (c UploadCoordinator) objectKey(sourceKind, extension string) (string, error) {
	prefix := map[string]string{"MEMBER_PROFILE_PHOTO": "profiles", "REPAIR_ATTACHMENT": "repairs", "EQUIPMENT_PHOTO": "equipment"}[sourceKind]
	if prefix == "" || (extension != "jpg" && extension != "png" && extension != "webp") {
		return "", ErrUploadProvenanceFailed
	}
	now := time.Now()
	if c.Now != nil {
		now = c.Now()
	}
	return fmt.Sprintf("%s/%s/%s.%s", prefix, now.UTC().Format("2006/01"), uuid.New(), extension), nil
}
