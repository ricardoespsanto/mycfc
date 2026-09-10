package privacyrequests

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
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

type tombstoneLambdaFake struct {
	invoke func(*awslambda.InvokeInput) (*awslambda.InvokeOutput, error)
	calls  int
}

func (f *tombstoneLambdaFake) Invoke(_ context.Context, input *awslambda.InvokeInput, _ ...func(*awslambda.Options)) (*awslambda.InvokeOutput, error) {
	f.calls++
	return f.invoke(input)
}

func brokerResponsePayload(t *testing.T, sealed SealedTombstone, mutate func(*tombstoneBrokerResponse)) []byte {
	t.Helper()
	writtenAt := time.Date(2026, time.September, 10, 10, 0, 0, 0, time.UTC)
	response := tombstoneBrokerResponse{
		Kind: sealed.Kind, LocatorKeyID: sealed.LocatorKeyID, LocatorDigest: bytes.Clone(sealed.Locator),
		ObjectVersion: "immutable-version", CiphertextSHA256: bytes.Clone(sealed.SHA256), SizeBytes: int64(len(sealed.Encoded)),
		WrittenAt: writtenAt, VerifiedAt: writtenAt.Add(time.Second),
	}
	if sealed.Kind == "closure" {
		retainUntil := sealed.RetainUntil.UTC()
		response.RetainUntil = &retainUntil
	}
	if mutate != nil {
		mutate(&response)
	}
	payload, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestLambdaLedgerInvokesOneExactBrokerWithOnlyBoundedEncryptedFields(t *testing.T) {
	protector, _ := tombstoneProtectorFixture(t)
	record := tombstoneFixture()
	closedAt := time.Date(2026, time.September, 10, 9, 59, 0, 0, time.UTC)
	tests := []struct {
		name   string
		sealed SealedTombstone
		keys   int
	}{
		{name: "intent", keys: 5},
		{name: "closure", keys: 6},
	}
	var err error
	tests[0].sealed, err = protector.Seal(record)
	if err != nil {
		t.Fatal(err)
	}
	tests[1].sealed, err = protector.SealClosure(TombstoneClosure{
		Version: TombstoneClosureVersion, Tombstone: record, ClosedAt: closedAt, EvidenceExpiresAt: closedAt.AddDate(0, 24, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := &tombstoneLambdaFake{}
			fake.invoke = func(input *awslambda.InvokeInput) (*awslambda.InvokeOutput, error) {
				if aws.ToString(input.FunctionName) != "mycfc-tombstone-broker" || input.InvocationType != lambdatypes.InvocationTypeRequestResponse || input.LogType != lambdatypes.LogTypeNone {
					t.Fatalf("unsafe broker invocation: %+v", input)
				}
				if len(input.Payload) == 0 || len(input.Payload) > maxBrokerRequestBytes || strings.Contains(string(input.Payload), record.ExecutionID.String()) {
					t.Fatal("broker request was unbounded or exposed a record identifier")
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(input.Payload, &fields); err != nil || len(fields) != test.keys {
					t.Fatalf("request field count=%d err=%v", len(fields), err)
				}
				for _, key := range []string{"kind", "locator_key_id", "locator_digest", "payload", "checksum"} {
					if _, ok := fields[key]; !ok {
						t.Fatalf("request omitted %q", key)
					}
				}
				_, hasRetention := fields["retain_until"]
				if hasRetention != (test.sealed.Kind == "closure") {
					t.Fatalf("retain_until presence=%t kind=%s", hasRetention, test.sealed.Kind)
				}
				var request tombstoneBrokerRequest
				if err := json.Unmarshal(input.Payload, &request); err != nil || request.Kind != test.sealed.Kind ||
					request.LocatorKeyID != test.sealed.LocatorKeyID || !bytes.Equal(request.LocatorDigest, test.sealed.Locator) ||
					!bytes.Equal(request.Payload, test.sealed.Encoded) || !bytes.Equal(request.Checksum, test.sealed.SHA256) {
					t.Fatalf("broker request did not match sealed record: %+v err=%v", request, err)
				}
				return &awslambda.InvokeOutput{StatusCode: 200, Payload: brokerResponsePayload(t, test.sealed, nil)}, nil
			}
			ledger, err := NewLambdaTombstoneLedger(fake, "mycfc-tombstone-broker")
			if err != nil {
				t.Fatal(err)
			}
			receipt, err := ledger.Write(t.Context(), test.sealed)
			if err != nil || fake.calls != 1 || receipt.ObjectVersion != "immutable-version" ||
				!bytes.Equal(receipt.LocatorDigest, test.sealed.Locator) || !bytes.Equal(receipt.CiphertextSHA, test.sealed.SHA256) {
				t.Fatalf("receipt=%+v calls=%d err=%v", receipt, fake.calls, err)
			}
		})
	}
}

func TestLambdaLedgerStrictlyRejectsUnverifiedOrUnboundedResponses(t *testing.T) {
	protector, _ := tombstoneProtectorFixture(t)
	record := tombstoneFixture()
	closedAt := time.Date(2026, time.September, 10, 10, 0, 0, 0, time.UTC)
	sealed, err := protector.SealClosure(TombstoneClosure{
		Version: TombstoneClosureVersion, Tombstone: record, ClosedAt: closedAt, EvidenceExpiresAt: closedAt.AddDate(0, 24, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	valid := brokerResponsePayload(t, sealed, nil)
	tests := []struct {
		name   string
		output *awslambda.InvokeOutput
		err    error
	}{
		{name: "invoke error", err: errors.New("opaque upstream error")},
		{name: "function error", output: &awslambda.InvokeOutput{StatusCode: 200, FunctionError: aws.String("Unhandled"), Payload: valid}},
		{name: "non success status", output: &awslambda.InvokeOutput{StatusCode: 202, Payload: valid}},
		{name: "oversized", output: &awslambda.InvokeOutput{StatusCode: 200, Payload: bytes.Repeat([]byte{'x'}, maxBrokerResponseBytes+1)}},
		{name: "unknown field", output: &awslambda.InvokeOutput{StatusCode: 200, Payload: append(valid[:len(valid)-1], []byte(`,"execution_id":"forbidden"}`)...)}},
		{name: "trailing document", output: &awslambda.InvokeOutput{StatusCode: 200, Payload: append(bytes.Clone(valid), []byte(` {}`)...)}},
		{name: "checksum mismatch", output: &awslambda.InvokeOutput{StatusCode: 200, Payload: brokerResponsePayload(t, sealed, func(response *tombstoneBrokerResponse) { response.CiphertextSHA256[0] ^= 0xff })}},
		{name: "locator mismatch", output: &awslambda.InvokeOutput{StatusCode: 200, Payload: brokerResponsePayload(t, sealed, func(response *tombstoneBrokerResponse) { response.LocatorDigest[0] ^= 0xff })}},
		{name: "retention mismatch", output: &awslambda.InvokeOutput{StatusCode: 200, Payload: brokerResponsePayload(t, sealed, func(response *tombstoneBrokerResponse) {
			changed := response.RetainUntil.Add(time.Second)
			response.RetainUntil = &changed
		})}},
		{name: "version missing", output: &awslambda.InvokeOutput{StatusCode: 200, Payload: brokerResponsePayload(t, sealed, func(response *tombstoneBrokerResponse) { response.ObjectVersion = "" })}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := &tombstoneLambdaFake{invoke: func(*awslambda.InvokeInput) (*awslambda.InvokeOutput, error) { return test.output, test.err }}
			ledger, err := NewLambdaTombstoneLedger(fake, "mycfc-tombstone-broker")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = ledger.Write(t.Context(), sealed); !errors.Is(err, ErrTombstoneUnavailable) || fake.calls != 1 {
				t.Fatalf("error=%v calls=%d", err, fake.calls)
			}
		})
	}
}

func TestLambdaLedgerFailsClosedBeforeInvocationForInvalidConfigurationOrPayload(t *testing.T) {
	fake := &tombstoneLambdaFake{invoke: func(*awslambda.InvokeInput) (*awslambda.InvokeOutput, error) {
		t.Fatal("invalid payload reached broker")
		return nil, nil
	}}
	if _, err := NewLambdaTombstoneLedger(fake, "*"); !errors.Is(err, ErrTombstoneInvalid) {
		t.Fatalf("invalid function name error=%v", err)
	}
	ledger, err := NewLambdaTombstoneLedger(fake, "arn:aws:lambda:eu-west-1:123456789012:function:mycfc-tombstone-broker:live")
	if err != nil {
		t.Fatal(err)
	}
	protector, _ := tombstoneProtectorFixture(t)
	sealed, err := protector.Seal(tombstoneFixture())
	if err != nil {
		t.Fatal(err)
	}
	sealed.Encoded = bytes.Repeat([]byte{0x01}, maxTombstonePayloadBytes+1)
	if _, err = ledger.Write(t.Context(), sealed); !errors.Is(err, ErrTombstoneInvalid) || fake.calls != 0 {
		t.Fatalf("oversized payload error=%v calls=%d", err, fake.calls)
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

func TestNonProductionS3LedgerConditionallyCreatesAndVerifiesExactObjectVersion(t *testing.T) {
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

func TestNonProductionS3LedgerClosureObjectIsImmutableThroughDatabaseExpiry(t *testing.T) {
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

func TestNonProductionS3LedgerIdempotentRetryRequiresMatchingImmutableVersion(t *testing.T) {
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
