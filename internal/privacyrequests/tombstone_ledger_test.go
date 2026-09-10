package privacyrequests

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"
)

func tombstoneFixture() RestoreTombstone {
	return RestoreTombstone{
		Version: TombstoneRecordVersion, ExecutionID: uuid.New(), RequestID: uuid.New(), RequestRef: uuid.New(),
		SubjectUserID: uuid.New(), PlanSHA256: bytes.Repeat([]byte{0x11}, 32), WorksetSHA256: bytes.Repeat([]byte{0x22}, 32),
		ExecutionStart: time.Date(2026, time.September, 10, 9, 30, 0, 0, time.UTC),
	}
}

func tombstoneProtectorFixture(t *testing.T) (*TombstoneProtector, []byte) {
	t.Helper()
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	protector, err := NewTombstoneProtector("restore-key-v1", private.PublicKey().Bytes(), "locator-key-v1", bytes.Repeat([]byte{0x33}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return protector, private.Bytes()
}

func TestTombstoneEnvelopeIsRandomizedBoundAndRoundTrips(t *testing.T) {
	protector, private := tombstoneProtectorFixture(t)
	record := tombstoneFixture()
	first, err := protector.Seal(record)
	if err != nil {
		t.Fatal(err)
	}
	second, err := protector.Seal(record)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first.Encoded, second.Encoded) || !bytes.Equal(first.Locator, second.Locator) {
		t.Fatal("encryption was deterministic or stable locator changed")
	}
	opened, err := OpenRestoreTombstone(private, record.ExecutionID, first.Envelope)
	if err != nil || opened.ExecutionID != record.ExecutionID || !bytes.Equal(opened.WorksetSHA256, record.WorksetSHA256) {
		t.Fatalf("opened=%+v err=%v", opened, err)
	}
	if _, err = OpenRestoreTombstone(private, uuid.New(), first.Envelope); !errors.Is(err, ErrTombstoneInvalid) {
		t.Fatalf("wrong execution error=%v", err)
	}
	tampered := first.Envelope
	tampered.Ciphertext = bytes.Clone(first.Envelope.Ciphertext)
	tampered.Ciphertext[0] ^= 0xff
	if _, err = OpenRestoreTombstone(private, record.ExecutionID, tampered); !errors.Is(err, ErrTombstoneInvalid) {
		t.Fatalf("tampered envelope error=%v", err)
	}
}

func TestClosureIsSeparateAndUsesExactCalendarEvidenceExpiry(t *testing.T) {
	protector, private := tombstoneProtectorFixture(t)
	record := tombstoneFixture()
	intent, err := protector.Seal(record)
	if err != nil {
		t.Fatal(err)
	}
	closedAt := time.Date(2028, time.February, 29, 12, 0, 0, 0, time.UTC)
	closure := TombstoneClosure{Version: TombstoneClosureVersion, Tombstone: record, ClosedAt: closedAt, EvidenceExpiresAt: closedAt.AddDate(0, 24, 0)}
	sealed, err := protector.SealClosure(closure)
	if err != nil {
		t.Fatal(err)
	}
	if sealed.Kind != "closure" || !sealed.RetainUntil.Equal(closure.EvidenceExpiresAt) || bytes.Equal(intent.Locator, sealed.Locator) {
		t.Fatalf("closure not domain-separated: %+v", sealed)
	}
	opened, err := OpenRestoreTombstoneClosure(private, record.ExecutionID, sealed.Envelope)
	if err != nil || !opened.ClosedAt.Equal(closedAt) || !opened.EvidenceExpiresAt.Equal(closedAt.AddDate(0, 24, 0)) {
		t.Fatalf("opened=%+v err=%v", opened, err)
	}
	if _, err = OpenRestoreTombstone(private, record.ExecutionID, sealed.Envelope); !errors.Is(err, ErrTombstoneInvalid) {
		t.Fatalf("closure opened as intent: %v", err)
	}
	closure.EvidenceExpiresAt = closure.EvidenceExpiresAt.Add(-time.Second)
	if _, err = protector.SealClosure(closure); !errors.Is(err, ErrTombstoneInvalid) {
		t.Fatalf("non-exact expiry error=%v", err)
	}
}

type tombstoneS3Fake struct {
	put       func(*s3.PutObjectInput) (*s3.PutObjectOutput, error)
	head      func(*s3.HeadObjectInput) (*s3.HeadObjectOutput, error)
	putCalls  int
	headCalls int
}

func (f *tombstoneS3Fake) PutObject(_ context.Context, input *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	f.putCalls++
	return f.put(input)
}

func (f *tombstoneS3Fake) HeadObject(_ context.Context, input *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	f.headCalls++
	return f.head(input)
}

func TestS3LedgerConditionallyCreatesAndVerifiesExactObjectVersion(t *testing.T) {
	protector, _ := tombstoneProtectorFixture(t)
	record := tombstoneFixture()
	sealed, err := protector.Seal(record)
	if err != nil {
		t.Fatal(err)
	}
	checksum := base64.StdEncoding.EncodeToString(sealed.SHA256)
	expectedKey := "private/tombstones/intent/" + hex.EncodeToString(sealed.Locator) + ".json"
	fake := &tombstoneS3Fake{}
	fake.put = func(input *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
		if aws.ToString(input.IfNoneMatch) != "*" || aws.ToString(input.Key) != expectedKey || strings.Contains(aws.ToString(input.Key), record.ExecutionID.String()) {
			t.Fatalf("mutable or identifying put: %+v", input)
		}
		if input.ObjectLockMode != "" || input.ObjectLockRetainUntilDate != nil {
			t.Fatalf("pre-destructive intent must not start the closure retention clock: %+v", input)
		}
		return &s3.PutObjectOutput{VersionId: aws.String("version-1")}, nil
	}
	fake.head = func(input *s3.HeadObjectInput) (*s3.HeadObjectOutput, error) {
		if aws.ToString(input.VersionId) != "version-1" || aws.ToString(input.Key) != expectedKey {
			t.Fatalf("did not verify exact version: %+v", input)
		}
		return &s3.HeadObjectOutput{VersionId: aws.String("version-1"), ChecksumSHA256: aws.String(checksum), ContentLength: aws.Int64(int64(len(sealed.Encoded)))}, nil
	}
	ledger, err := NewS3TombstoneLedger(fake, "private-ledger", "private/tombstones/")
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, time.September, 10, 10, 0, 0, 0, time.UTC)
	ledger.now = func() time.Time { clock = clock.Add(time.Second); return clock }
	receipt, err := ledger.Write(t.Context(), sealed)
	if err != nil || receipt.ObjectVersion != "version-1" || !bytes.Equal(receipt.CiphertextSHA, sealed.SHA256) || fake.putCalls != 1 || fake.headCalls != 1 {
		t.Fatalf("receipt=%+v calls=%d/%d err=%v", receipt, fake.putCalls, fake.headCalls, err)
	}
}

func TestS3LedgerClosureObjectIsImmutableThroughDatabaseExpiry(t *testing.T) {
	protector, _ := tombstoneProtectorFixture(t)
	record := tombstoneFixture()
	closedAt := time.Date(2026, time.September, 10, 10, 0, 0, 0, time.UTC)
	sealed, err := protector.SealClosure(TombstoneClosure{Version: TombstoneClosureVersion, Tombstone: record, ClosedAt: closedAt, EvidenceExpiresAt: closedAt.AddDate(0, 24, 0)})
	if err != nil {
		t.Fatal(err)
	}
	checksum := base64.StdEncoding.EncodeToString(sealed.SHA256)
	fake := &tombstoneS3Fake{}
	fake.put = func(input *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
		if aws.ToString(input.IfNoneMatch) != "*" || input.ObjectLockMode != types.ObjectLockModeCompliance || input.ObjectLockRetainUntilDate == nil || !input.ObjectLockRetainUntilDate.Equal(sealed.RetainUntil) {
			t.Fatalf("closure immutability contract missing: %+v", input)
		}
		return &s3.PutObjectOutput{VersionId: aws.String("closure-version")}, nil
	}
	fake.head = func(input *s3.HeadObjectInput) (*s3.HeadObjectOutput, error) {
		return &s3.HeadObjectOutput{VersionId: aws.String("closure-version"), ChecksumSHA256: aws.String(checksum), ContentLength: aws.Int64(int64(len(sealed.Encoded))),
			ObjectLockMode: types.ObjectLockModeCompliance, ObjectLockRetainUntilDate: aws.Time(sealed.RetainUntil)}, nil
	}
	ledger, err := NewS3TombstoneLedger(fake, "private-ledger", "private/tombstones/")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ledger.Write(t.Context(), sealed); err != nil {
		t.Fatal(err)
	}
	fake.head = func(input *s3.HeadObjectInput) (*s3.HeadObjectOutput, error) {
		return &s3.HeadObjectOutput{VersionId: aws.String("closure-version"), ChecksumSHA256: aws.String(checksum), ContentLength: aws.Int64(int64(len(sealed.Encoded))),
			ObjectLockMode: types.ObjectLockModeGovernance, ObjectLockRetainUntilDate: aws.Time(sealed.RetainUntil)}, nil
	}
	if _, err = ledger.Write(t.Context(), sealed); !errors.Is(err, ErrTombstoneUnavailable) {
		t.Fatalf("unverified closure retention error=%v", err)
	}
}

func TestS3LedgerIdempotentRetryRequiresMatchingImmutableVersion(t *testing.T) {
	protector, _ := tombstoneProtectorFixture(t)
	sealed, err := protector.Seal(tombstoneFixture())
	if err != nil {
		t.Fatal(err)
	}
	checksum := base64.StdEncoding.EncodeToString(sealed.SHA256)
	fake := &tombstoneS3Fake{put: func(*s3.PutObjectInput) (*s3.PutObjectOutput, error) {
		return nil, errors.New("precondition failed")
	}}
	fake.head = func(input *s3.HeadObjectInput) (*s3.HeadObjectOutput, error) {
		version := "existing-version"
		if fake.headCalls == 2 && aws.ToString(input.VersionId) != version {
			t.Fatalf("retry did not pin existing version: %+v", input)
		}
		return &s3.HeadObjectOutput{VersionId: aws.String(version), ChecksumSHA256: aws.String(checksum), ContentLength: aws.Int64(int64(len(sealed.Encoded)))}, nil
	}
	ledger, _ := NewS3TombstoneLedger(fake, "private-ledger", "private/tombstones/")
	if receipt, err := ledger.Write(t.Context(), sealed); err != nil || receipt.ObjectVersion != "existing-version" || fake.headCalls != 2 {
		t.Fatalf("receipt=%+v head calls=%d err=%v", receipt, fake.headCalls, err)
	}

	fake.headCalls = 0
	fake.head = func(*s3.HeadObjectInput) (*s3.HeadObjectOutput, error) {
		return &s3.HeadObjectOutput{VersionId: aws.String("other-version"), ChecksumSHA256: aws.String(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xff}, 32))), ContentLength: aws.Int64(int64(len(sealed.Encoded)))}, nil
	}
	if _, err = ledger.Write(t.Context(), sealed); !errors.Is(err, ErrTombstoneUnavailable) {
		t.Fatalf("conflicting object error=%v", err)
	}
}

func TestRetentionMaintenanceIsFailClosedWhileDisabled(t *testing.T) {
	_, err := (RetentionMaintenance{Enabled: false, WorkerRef: uuid.New(), BatchLimit: 100}).Run(t.Context())
	if !errors.Is(err, ErrRetentionUnavailable) {
		t.Fatalf("disabled retention error=%v", err)
	}
}
