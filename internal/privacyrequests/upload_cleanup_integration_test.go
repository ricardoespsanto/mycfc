//go:build integration

package privacyrequests

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/storage"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type cleanupVersionedStoreFake struct{ key string }

func (s *cleanupVersionedStoreFake) DeleteAllVersions(_ context.Context, key string) (storage.VersionDeletionEvidence, error) {
	s.key = key
	return storage.VersionDeletionEvidence{DeletedVersions: 2, DeletedMarkers: 1, ListCalls: 4, StableChecks: 2}, nil
}

func TestUploadCleanupLifecycleIsFencedAndRecordsStableAbsence(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	userID := uuid.New()
	if _, err = pool.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Upload cleanup integration',$2,'hash','1990-01-01')`, userID, uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	protector, err := NewX25519UploadIntentProtector("upload-integration-key", privateKey.PublicKey().Bytes(), "upload-integration-digest", bytes.Repeat([]byte{4}, 32))
	if err != nil {
		t.Fatal(err)
	}
	steps := []string{}
	objects := &uploadObjectStoreFake{steps: &steps}
	coordinator := UploadCoordinator{Store: PostgresUploadIntentStore{Queries: dbgen.New(pool)}, Objects: objects, Protector: protector}
	upload, err := coordinator.Upload(ctx, UploadInput{SubjectUserID: &userID, ActorUserID: userID, SourceKind: "MEMBER_PROFILE_PHOTO", SourceRef: userID}, storage.ValidatedPhoto{Bytes: []byte("image"), ContentType: "image/png", Extension: "png", Size: 5})
	if err != nil {
		t.Fatal(err)
	}
	if err = coordinator.AttachmentFailed(ctx, upload); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE privacy_protected.object_upload_cleanup_jobs SET next_attempt_at='2000-01-01' WHERE intent_id=$1`, upload.IntentID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE privacy_protected.object_upload_cleanup_jobs SET status='SUCCEEDED',updated_at=clock_timestamp() WHERE intent_id<>$1 AND status IN ('PENDING','RETRY_WAIT')`, upload.IntentID); err != nil {
		t.Fatal(err)
	}

	versioned := &cleanupVersionedStoreFake{}
	workerRef := uuid.New()
	worker := UploadCleanupWorker{
		Store: PostgresUploadCleanupStore{DB: pool}, Objects: versioned, WorkerRef: workerRef, PrivateKey: privateKey.Bytes(),
		TranscriptKeyID: "cleanup-evidence-v1", TranscriptKey: bytes.Repeat([]byte{8}, 32), LeaseDuration: time.Minute,
	}
	worked, err := worker.RunOnce(ctx)
	if err != nil || !worked {
		t.Fatalf("worked=%t err=%v", worked, err)
	}
	if versioned.key != upload.ObjectKey || !strings.HasPrefix(versioned.key, "profiles/") {
		t.Fatalf("deleted unexpected key %q", versioned.key)
	}
	var status, jobStatus string
	var evidenceCount int
	if err = pool.QueryRow(ctx, `SELECT e.status,j.status,(SELECT count(*) FROM privacy_protected.object_upload_absence_evidence x WHERE x.intent_id=$1)
	 FROM privacy_protected.object_upload_intent_events e JOIN privacy_protected.object_upload_cleanup_jobs j ON j.intent_id=e.intent_id
	 WHERE e.intent_id=$1 ORDER BY e.sequence DESC LIMIT 1`, upload.IntentID).Scan(&status, &jobStatus, &evidenceCount); err != nil {
		t.Fatal(err)
	}
	if status != "ABSENCE_VERIFIED" || jobStatus != "SUCCEEDED" || evidenceCount != 1 {
		t.Fatalf("status=%q job=%q evidence=%d", status, jobStatus, evidenceCount)
	}
}

func TestUploadCleanupPermanentlyDeletesEveryMinIOVersionAndMarker(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	awsConfig, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(os.Getenv("AWS_REGION")))
	if err != nil {
		t.Fatal(err)
	}
	client := s3.NewFromConfig(awsConfig, func(options *s3.Options) {
		options.UsePathStyle = true
		options.BaseEndpoint = aws.String(os.Getenv("S3_ENDPOINT"))
	})
	bucket := os.Getenv("S3_BUCKET_NAME")
	if _, err = client.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{Bucket: aws.String(bucket), VersioningConfiguration: &types.VersioningConfiguration{Status: types.BucketVersioningStatusEnabled}}); err != nil {
		t.Fatal(err)
	}
	userID := uuid.New()
	if _, err = pool.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'MinIO cleanup integration',$2,'hash','1990-01-01')`, userID, uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	protector, err := NewX25519UploadIntentProtector("upload-minio-key", privateKey.PublicKey().Bytes(), "upload-minio-digest", bytes.Repeat([]byte{5}, 32))
	if err != nil {
		t.Fatal(err)
	}
	objects := storage.NewS3Store(client, bucket)
	coordinator := UploadCoordinator{Store: PostgresUploadIntentStore{Queries: dbgen.New(pool)}, Objects: objects, Protector: protector}
	upload, err := coordinator.Upload(ctx, UploadInput{SubjectUserID: &userID, ActorUserID: userID, SourceKind: "MEMBER_PROFILE_PHOTO", SourceRef: userID}, storage.ValidatedPhoto{Bytes: []byte("first"), ContentType: "image/png", Extension: "png", Size: 5})
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.PutObject(ctx, upload.ObjectKey, "image/png", 6, strings.NewReader("second")); err != nil {
		t.Fatal(err)
	}
	if err = objects.DeleteObject(ctx, upload.ObjectKey); err != nil {
		t.Fatal(err)
	}
	if err = coordinator.AttachmentFailed(ctx, upload); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE privacy_protected.object_upload_cleanup_jobs SET next_attempt_at='2000-01-01' WHERE intent_id=$1`, upload.IntentID); err != nil {
		t.Fatal(err)
	}
	worker := UploadCleanupWorker{
		Store: PostgresUploadCleanupStore{DB: pool}, Objects: storage.NewS3VersionedStore(client, bucket), WorkerRef: uuid.New(), PrivateKey: privateKey.Bytes(),
		TranscriptKeyID: "cleanup-minio-evidence-v1", TranscriptKey: bytes.Repeat([]byte{9}, 32), LeaseDuration: time.Minute,
	}
	worked, err := worker.RunOnce(ctx)
	if err != nil || !worked {
		t.Fatalf("worked=%t err=%v", worked, err)
	}
	listed, err := client.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{Bucket: aws.String(bucket), Prefix: aws.String(upload.ObjectKey)})
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range listed.Versions {
		if aws.ToString(version.Key) == upload.ObjectKey {
			t.Fatalf("object version remains after cleanup: %q", aws.ToString(version.VersionId))
		}
	}
	for _, marker := range listed.DeleteMarkers {
		if aws.ToString(marker.Key) == upload.ObjectKey {
			t.Fatalf("delete marker remains after cleanup: %q", aws.ToString(marker.VersionId))
		}
	}
}

func TestUploadCleanupClaimAllowsOneWorkerAndFencesExpiredLease(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	userID := uuid.New()
	if _, err = pool.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Cleanup claim integration',$2,'hash','1990-01-01')`, userID, uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	protector, err := NewX25519UploadIntentProtector("upload-claim-key", privateKey.PublicKey().Bytes(), "upload-claim-digest", bytes.Repeat([]byte{6}, 32))
	if err != nil {
		t.Fatal(err)
	}
	steps := []string{}
	coordinator := UploadCoordinator{Store: PostgresUploadIntentStore{Queries: dbgen.New(pool)}, Objects: &uploadObjectStoreFake{steps: &steps}, Protector: protector}
	upload, err := coordinator.Upload(ctx, UploadInput{SubjectUserID: &userID, ActorUserID: userID, SourceKind: "MEMBER_PROFILE_PHOTO", SourceRef: userID}, storage.ValidatedPhoto{Bytes: []byte("claim"), ContentType: "image/png", Extension: "png", Size: 5})
	if err != nil {
		t.Fatal(err)
	}
	if err = coordinator.AttachmentFailed(ctx, upload); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE privacy_protected.object_upload_cleanup_jobs SET next_attempt_at='2000-01-01' WHERE intent_id=$1`, upload.IntentID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE privacy_protected.object_upload_cleanup_jobs SET status='SUCCEEDED',updated_at=clock_timestamp() WHERE intent_id<>$1`, upload.IntentID); err != nil {
		t.Fatal(err)
	}
	store := PostgresUploadCleanupStore{DB: pool}
	type claimResult struct {
		claim  UploadCleanupClaim
		worker uuid.UUID
		err    error
	}
	results := make(chan claimResult, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			workerRef := uuid.New()
			claim, claimErr := store.Claim(ctx, time.Second, workerRef)
			results <- claimResult{claim: claim, worker: workerRef, err: claimErr}
		}()
	}
	group.Wait()
	close(results)
	var first UploadCleanupClaim
	var firstWorker uuid.UUID
	successes, empty := 0, 0
	for result := range results {
		if result.err == nil {
			first, firstWorker, successes = result.claim, result.worker, successes+1
		} else if errors.Is(result.err, pgx.ErrNoRows) {
			empty++
		} else {
			t.Fatal(result.err)
		}
	}
	if successes != 1 || empty != 1 {
		t.Fatalf("concurrent claims successes=%d empty=%d", successes, empty)
	}
	time.Sleep(1100 * time.Millisecond)
	secondWorker := uuid.New()
	second, err := store.Claim(ctx, time.Second, secondWorker)
	if err != nil {
		t.Fatal(err)
	}
	if second.IntentID != first.IntentID || second.LeaseEpoch <= first.LeaseEpoch {
		t.Fatalf("replacement claim=%+v first=%+v", second, first)
	}
	if err = store.Complete(ctx, UploadCleanupResult{IntentID: first.IntentID, AttemptID: first.AttemptID, LeaseEpoch: first.LeaseEpoch, WorkerRef: firstWorker, Evidence: storage.VersionDeletionEvidence{ListCalls: 2, StableChecks: 2}, TranscriptKeyID: "stale-evidence", TranscriptDigest: bytes.Repeat([]byte{1}, 32)}); err == nil {
		t.Fatal("expired cleanup lease completed after replacement claim")
	}
}

func TestUploadCleanupMarksOnlyExpiredUnheldIntentsStale(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	userID := uuid.New()
	if _, err = pool.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Stale upload integration',$2,'hash','1990-01-01')`, userID, uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	staleIntent := insertAgedUploadIntent(t, ctx, pool, userID, false)
	activeIntent := insertAgedUploadIntent(t, ctx, pool, userID, true)
	var staleStatus, activeStatus string
	for range 20 {
		if _, err = pool.Exec(ctx, `SELECT * FROM privacy_upload_cleanup_claim(60000,$1)`, uuid.New()); err != nil {
			t.Fatal(err)
		}
		if err = pool.QueryRow(ctx, `SELECT status FROM privacy_protected.object_upload_intent_events WHERE intent_id=$1 ORDER BY sequence DESC LIMIT 1`, staleIntent).Scan(&staleStatus); err != nil {
			t.Fatal(err)
		}
		if staleStatus == "CLEANUP_REQUIRED" {
			break
		}
	}
	if err = pool.QueryRow(ctx, `SELECT status FROM privacy_protected.object_upload_intent_events WHERE intent_id=$1 ORDER BY sequence DESC LIMIT 1`, activeIntent).Scan(&activeStatus); err != nil {
		t.Fatal(err)
	}
	if staleStatus != "CLEANUP_REQUIRED" || activeStatus != "PREPARED" {
		t.Fatalf("stale=%q active=%q", staleStatus, activeStatus)
	}
}

func insertAgedUploadIntent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID, activelyHeld bool) uuid.UUID {
	t.Helper()
	intentID := uuid.New()
	heldUntil := time.Now().Add(-time.Hour)
	if activelyHeld {
		heldUntil = time.Now().Add(time.Hour)
	}
	token := bytes.Repeat([]byte{3}, 32)
	if _, err := pool.Exec(ctx, `INSERT INTO privacy_protected.object_upload_intent_reservations(id,subject_user_id,provenance_actor_user_id,source_kind,source_ref,service_code,content_type,size_bytes,token_digest,created_at,cleanup_after,finalized_at)
VALUES($1,$2,$2,'MEMBER_PROFILE_PHOTO',$2,'private-media','image/png',5,digest($3,'sha256'),'2000-01-01'::timestamptz,'2000-01-02'::timestamptz,'2000-01-01'::timestamptz)`, intentID, userID, token); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO privacy_protected.object_upload_intents(id,subject_user_id,provenance_actor_user_id,source_kind,source_ref,service_code,target_kind,provider_contract_version,envelope_version,algorithm,encryption_key_id,encapsulation,nonce,ciphertext,digest_key_id,locator_digest,content_type,size_bytes,cleanup_after,created_at)
VALUES($1,$2,$2,'MEMBER_PROFILE_PHOTO',$2,'private-media','OBJECT_KEY','s3-versioned/v1','x25519-aes256gcm-hkdfsha256/upload-intent-v1','X25519-HKDF-SHA256-AES-256-GCM','stale-upload-key',$3,$4,$5,'stale-upload-digest',$6,'image/png',5,'2000-01-02'::timestamptz,'2000-01-01'::timestamptz)`, intentID, userID, randomTestBytes(t, 32), randomTestBytes(t, 12), randomTestBytes(t, 32), randomTestBytes(t, 32)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO privacy_protected.object_upload_intent_events VALUES($1,1,'PREPARED','UPLOAD_RESERVED','2000-01-01'::timestamptz)`, intentID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO privacy_protected.object_upload_intent_holds(intent_id,token_digest,held_until,updated_at) VALUES($1,digest($2,'sha256'),$3,'2000-01-01'::timestamptz)`, intentID, token, heldUntil); err != nil {
		t.Fatal(err)
	}
	return intentID
}

func randomTestBytes(t *testing.T, size int) []byte {
	t.Helper()
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		t.Fatal(err)
	}
	return value
}
