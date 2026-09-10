package privacyrequests

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/storage"
	"github.com/google/uuid"
)

type uploadIntentStoreFake struct {
	steps                                         []string
	clock                                         UploadIntentClock
	begin                                         UploadIntentBegin
	protected                                     UploadIntentProtectedRecord
	cleanupCode                                   string
	beginErr, finalizeErr, confirmErr, cleanupErr error
}

func (s *uploadIntentStoreFake) Begin(_ context.Context, input UploadIntentBegin) (UploadIntentClock, error) {
	s.steps = append(s.steps, "begin")
	s.begin = input
	return s.clock, s.beginErr
}
func (s *uploadIntentStoreFake) Finalize(_ context.Context, input UploadIntentProtectedRecord) error {
	s.steps = append(s.steps, "finalize")
	s.protected = input
	return s.finalizeErr
}
func (s *uploadIntentStoreFake) ConfirmPut(context.Context, uuid.UUID, []byte) error {
	s.steps = append(s.steps, "confirm")
	return s.confirmErr
}
func (s *uploadIntentStoreFake) MarkCleanup(_ context.Context, _ uuid.UUID, _ []byte, reason string) error {
	s.steps = append(s.steps, "cleanup")
	s.cleanupCode = reason
	return s.cleanupErr
}

type uploadObjectStoreFake struct {
	steps *[]string
	key   string
	err   error
}

func (s *uploadObjectStoreFake) PutObject(_ context.Context, key, _ string, _ int64, body io.Reader) error {
	*s.steps = append(*s.steps, "put")
	s.key = key
	_, _ = io.Copy(io.Discard, body)
	return s.err
}
func (*uploadObjectStoreFake) DeleteObject(context.Context, string) error { return nil }
func (*uploadObjectStoreFake) PresignGet(context.Context, string, time.Duration) (string, error) {
	return "", nil
}

func TestUploadCoordinatorReservesBeforePutAndConfirmsAfterward(t *testing.T) {
	now := time.Date(2026, 9, 10, 15, 0, 0, 0, time.UTC)
	store := &uploadIntentStoreFake{clock: UploadIntentClock{CreatedAt: now, CleanupAfter: now.Add(24 * time.Hour), HoldEpoch: 1}}
	objects := &uploadObjectStoreFake{steps: &store.steps}
	private, _ := ecdh.X25519().GenerateKey(rand.Reader)
	protector, _ := NewX25519UploadIntentProtector("upload-key", private.PublicKey().Bytes(), "upload-digest", bytes.Repeat([]byte{4}, 32))
	subject := uuid.New()
	result, err := (UploadCoordinator{Store: store, Objects: objects, Protector: protector, Now: func() time.Time { return now }, Random: bytes.NewReader(bytes.Repeat([]byte{5}, 32))}).Upload(context.Background(), UploadInput{SubjectUserID: &subject, ActorUserID: subject, SourceKind: "MEMBER_PROFILE_PHOTO", SourceRef: subject}, storage.ValidatedPhoto{Bytes: []byte("image"), ContentType: "image/png", Extension: "png", Size: 5})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(store.steps, ",") != "begin,finalize,put,confirm" {
		t.Fatalf("steps = %v", store.steps)
	}
	if result.IntentID == uuid.Nil || result.ObjectKey != objects.key || !strings.HasPrefix(result.ObjectKey, "profiles/2026/09/") || len(result.HoldToken) != 32 {
		t.Fatalf("result = %#v", result)
	}
	if store.begin.IntentID != result.IntentID || store.protected.IntentID != result.IntentID || store.begin.SubjectUserID == nil || *store.begin.SubjectUserID != subject {
		t.Fatal("intent binding was not preserved")
	}
	opened, err := OpenUploadIntentEnvelope(private.Bytes(), UploadIntentBinding{IntentID: result.IntentID, SubjectUserID: &subject, ProvenanceActorID: subject, SourceKind: "MEMBER_PROFILE_PHOTO", SourceRef: subject, Service: "private-media", TargetKind: "OBJECT_KEY", ProviderVersion: "s3-versioned/v1", ContentType: "image/png", SizeBytes: 5, CreatedAt: now, CleanupAfter: now.Add(24 * time.Hour)}, store.protected.Envelope)
	if err != nil || opened != result.ObjectKey {
		t.Fatalf("opened = %q, err = %v", opened, err)
	}
}

func TestUploadCoordinatorTreatsPutAndConfirmationFailuresAsAmbiguous(t *testing.T) {
	now := time.Date(2026, 9, 10, 15, 0, 0, 0, time.UTC)
	private, _ := ecdh.X25519().GenerateKey(rand.Reader)
	protector, _ := NewX25519UploadIntentProtector("upload-key", private.PublicKey().Bytes(), "upload-digest", bytes.Repeat([]byte{6}, 32))
	for _, tc := range []struct {
		name               string
		putErr, confirmErr error
		want               string
	}{
		{name: "put", putErr: errors.New("provider may include private/key.png"), want: "begin,finalize,put,cleanup"},
		{name: "confirm", confirmErr: errors.New("database may include private/key.png"), want: "begin,finalize,put,confirm,cleanup"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &uploadIntentStoreFake{clock: UploadIntentClock{CreatedAt: now, CleanupAfter: now.Add(24 * time.Hour), HoldEpoch: 1}, confirmErr: tc.confirmErr}
			objects := &uploadObjectStoreFake{steps: &store.steps, err: tc.putErr}
			_, err := (UploadCoordinator{Store: store, Objects: objects, Protector: protector, Now: func() time.Time { return now }, Random: bytes.NewReader(bytes.Repeat([]byte{7}, 32))}).Upload(context.Background(), UploadInput{ActorUserID: uuid.New(), SourceKind: "EQUIPMENT_PHOTO", SourceRef: uuid.New()}, storage.ValidatedPhoto{Bytes: []byte("x"), ContentType: "image/png", Extension: "png", Size: 1})
			if !errors.Is(err, ErrUploadProvenanceFailed) || strings.Contains(err.Error(), "private/key.png") {
				t.Fatalf("error = %v", err)
			}
			if strings.Join(store.steps, ",") != tc.want || store.cleanupCode != "PUT_AMBIGUOUS" {
				t.Fatalf("steps/code = %v/%s", store.steps, store.cleanupCode)
			}
		})
	}
}

func TestUploadCoordinatorFailsClosedWhenUnavailableOrEntropyFails(t *testing.T) {
	photo := storage.ValidatedPhoto{Bytes: []byte("x"), ContentType: "image/png", Extension: "png", Size: 1}
	if _, err := (UploadCoordinator{}).Upload(context.Background(), UploadInput{}, photo); !errors.Is(err, ErrUploadProvenanceUnavailable) {
		t.Fatalf("error = %v", err)
	}
	now := time.Now().UTC()
	store := &uploadIntentStoreFake{clock: UploadIntentClock{CreatedAt: now, CleanupAfter: now.Add(24 * time.Hour), HoldEpoch: 1}}
	private, _ := ecdh.X25519().GenerateKey(rand.Reader)
	protector, _ := NewX25519UploadIntentProtector("upload-key", private.PublicKey().Bytes(), "upload-digest", bytes.Repeat([]byte{8}, 32))
	_, err := (UploadCoordinator{Store: store, Objects: &uploadObjectStoreFake{steps: &store.steps}, Protector: protector, Random: errorReader{}}).Upload(context.Background(), UploadInput{ActorUserID: uuid.New(), SourceKind: "EQUIPMENT_PHOTO", SourceRef: uuid.New()}, photo)
	if !errors.Is(err, ErrUploadProvenanceFailed) {
		t.Fatalf("error = %v", err)
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("entropy unavailable") }

type uploadProtectorFake struct {
	sealErr, digestErr error
}

func (p uploadProtectorFake) SealUploadObjectKey(UploadIntentBinding, string) (ObjectTargetEnvelope, error) {
	return ObjectTargetEnvelope{}, p.sealErr
}
func (p uploadProtectorFake) DigestUploadObjectKey(string, string) (ObjectTargetDigest, error) {
	return ObjectTargetDigest{}, p.digestErr
}

func TestUploadCoordinatorCoversLifecycleFailurePaths(t *testing.T) {
	now := time.Date(2026, 9, 10, 15, 0, 0, 0, time.UTC)
	photo := storage.ValidatedPhoto{Bytes: []byte("x"), ContentType: "image/png", Extension: "png", Size: 1}
	input := UploadInput{ActorUserID: uuid.New(), SourceKind: "EQUIPMENT_PHOTO", SourceRef: uuid.New()}
	objects := &uploadObjectStoreFake{}
	for _, tc := range []struct {
		name      string
		store     *uploadIntentStoreFake
		protector UploadIntentProtector
	}{
		{name: "begin", store: &uploadIntentStoreFake{beginErr: errors.New("begin")}, protector: uploadProtectorFake{}},
		{name: "seal", store: &uploadIntentStoreFake{clock: UploadIntentClock{CreatedAt: now, CleanupAfter: now.Add(24 * time.Hour), HoldEpoch: 1}}, protector: uploadProtectorFake{sealErr: errors.New("seal")}},
		{name: "digest", store: &uploadIntentStoreFake{clock: UploadIntentClock{CreatedAt: now, CleanupAfter: now.Add(24 * time.Hour), HoldEpoch: 1}}, protector: uploadProtectorFake{digestErr: errors.New("digest")}},
		{name: "finalize", store: &uploadIntentStoreFake{clock: UploadIntentClock{CreatedAt: now, CleanupAfter: now.Add(24 * time.Hour), HoldEpoch: 1}, finalizeErr: errors.New("finalize")}, protector: uploadProtectorFake{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objects.steps = &tc.store.steps
			_, err := (UploadCoordinator{Store: tc.store, Objects: objects, Protector: tc.protector, Now: func() time.Time { return now }, Random: bytes.NewReader(bytes.Repeat([]byte{4}, 32))}).Upload(context.Background(), input, photo)
			if !errors.Is(err, ErrUploadProvenanceFailed) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	coordinator := UploadCoordinator{Store: &uploadIntentStoreFake{}}
	if err := coordinator.AttachmentFailed(context.Background(), PreparedUpload{}); !errors.Is(err, ErrUploadProvenanceUnavailable) {
		t.Fatalf("unavailable attachment cleanup error=%v", err)
	}
	upload := PreparedUpload{IntentID: uuid.New(), HoldToken: bytes.Repeat([]byte{3}, 32)}
	if err := coordinator.AttachmentFailed(context.Background(), upload); err != nil {
		t.Fatalf("attachment cleanup error=%v", err)
	}
	coordinator.Store = &uploadIntentStoreFake{cleanupErr: errors.New("cleanup")}
	if err := coordinator.AttachmentFailed(context.Background(), upload); !errors.Is(err, ErrUploadProvenanceFailed) {
		t.Fatalf("failed attachment cleanup error=%v", err)
	}
	if _, err := coordinator.objectKey("UNKNOWN", "png"); !errors.Is(err, ErrUploadProvenanceFailed) {
		t.Fatalf("source error=%v", err)
	}
	if _, err := coordinator.objectKey("EQUIPMENT_PHOTO", "gif"); !errors.Is(err, ErrUploadProvenanceFailed) {
		t.Fatalf("extension error=%v", err)
	}
}

type uploadIntentQueriesFake struct {
	payload []byte
	err     error
}

func (q uploadIntentQueriesFake) BeginPrivacyUploadIntent(context.Context, dbgen.BeginPrivacyUploadIntentParams) ([]byte, error) {
	return q.payload, q.err
}
func (q uploadIntentQueriesFake) FinalizePrivacyUploadIntent(context.Context, dbgen.FinalizePrivacyUploadIntentParams) error {
	return q.err
}
func (q uploadIntentQueriesFake) ConfirmPrivacyUploadPut(context.Context, dbgen.ConfirmPrivacyUploadPutParams) error {
	return q.err
}
func (q uploadIntentQueriesFake) MarkPrivacyUploadCleanup(context.Context, dbgen.MarkPrivacyUploadCleanupParams) error {
	return q.err
}

func TestPostgresUploadIntentStoreValidatesClockAndDelegates(t *testing.T) {
	ctx := context.Background()
	input := UploadIntentBegin{IntentID: uuid.New(), ActorUserID: uuid.New(), SourceRef: uuid.New()}
	store := PostgresUploadIntentStore{}
	if _, err := store.Begin(ctx, input); !errors.Is(err, ErrUploadProvenanceUnavailable) {
		t.Fatalf("nil begin error=%v", err)
	}
	if err := store.Finalize(ctx, UploadIntentProtectedRecord{}); !errors.Is(err, ErrUploadProvenanceUnavailable) {
		t.Fatalf("nil finalize error=%v", err)
	}
	if err := store.ConfirmPut(ctx, uuid.New(), nil); !errors.Is(err, ErrUploadProvenanceUnavailable) {
		t.Fatalf("nil confirm error=%v", err)
	}
	if err := store.MarkCleanup(ctx, uuid.New(), nil, "PUT_FAILED"); !errors.Is(err, ErrUploadProvenanceUnavailable) {
		t.Fatalf("nil cleanup error=%v", err)
	}
	store.Queries = uploadIntentQueriesFake{payload: []byte("not-json")}
	if _, err := store.Begin(ctx, input); !errors.Is(err, ErrUploadProvenanceFailed) {
		t.Fatalf("malformed clock error=%v", err)
	}
	now := time.Date(2026, 9, 10, 15, 0, 0, 0, time.UTC)
	payload, err := json.Marshal(map[string]any{"created_at": now, "cleanup_after": now.Add(24 * time.Hour), "hold_epoch": 1})
	if err != nil {
		t.Fatal(err)
	}
	store.Queries = uploadIntentQueriesFake{payload: payload}
	if clock, err := store.Begin(ctx, input); err != nil || !clock.CreatedAt.Equal(now) {
		t.Fatalf("clock=%#v err=%v", clock, err)
	}
	if err := store.Finalize(ctx, UploadIntentProtectedRecord{}); err != nil {
		t.Fatal(err)
	}
	if err := store.ConfirmPut(ctx, uuid.New(), bytes.Repeat([]byte{1}, 32)); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkCleanup(ctx, uuid.New(), bytes.Repeat([]byte{1}, 32), "PUT_FAILED"); err != nil {
		t.Fatal(err)
	}
}
