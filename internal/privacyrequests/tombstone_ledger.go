package privacyrequests

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"golang.org/x/crypto/hkdf"
)

const (
	TombstoneRecordVersionV1   = "restore-tombstone/v1"
	TombstoneClosureVersionV1  = "restore-tombstone-closure/v1"
	TombstoneEnvelopeVersionV1 = "x25519-aes256gcm-hkdfsha256/v1"
	TombstoneRecordVersion     = "restore-tombstone/v2"
	TombstoneClosureVersion    = "restore-tombstone-closure/v2"
	TombstoneEnvelopeVersion   = "x25519-aes256gcm-hkdfsha256/v2"
	TombstoneReplayVersion     = "relational-erasure-replay/v1"
	tombstoneAlgorithm         = "X25519-HKDF-SHA256-AES-256-GCM"
	maxTombstonePayloadBytes   = 1 << 20
	maxBrokerRequestBytes      = 2 << 20
	maxBrokerResponseBytes     = 8 << 10
)

var (
	ErrTombstoneUnavailable = errors.New("privacy restore tombstone unavailable")
	ErrTombstoneInvalid     = errors.New("privacy restore tombstone invalid")
	ledgerPrefix            = regexp.MustCompile(`^[a-z0-9][a-z0-9/_-]{0,119}/$`)
	tombstoneLambdaFunction = regexp.MustCompile(`^(?:[A-Za-z0-9_-]{1,64}|arn:(?:aws|aws-us-gov|aws-cn):lambda:[a-z0-9-]+:[0-9]{12}:function:[A-Za-z0-9_-]{1,64}(?::[A-Za-z0-9_-]+)?)$`)
)

// RestoreTombstone is the minimum replay identity. It deliberately excludes
// names, addresses, credentials, object locators, and provider payloads.
type RestoreTombstone struct {
	Version        string                        `json:"version"`
	ExecutionID    uuid.UUID                     `json:"execution_id"`
	RequestID      uuid.UUID                     `json:"request_id"`
	RequestRef     uuid.UUID                     `json:"request_ref"`
	SubjectUserID  uuid.UUID                     `json:"subject_user_id"`
	PlanSHA256     []byte                        `json:"plan_sha256"`
	WorksetSHA256  []byte                        `json:"workset_sha256"`
	ExecutionStart time.Time                     `json:"execution_started_at"`
	Replay         *RelationalReplayPrescription `json:"replay,omitempty"`
}

// RelationalReplayPrescription is deliberately limited to versioned local
// database operations. It contains no object-store or provider instructions.
type RelationalReplayPrescription struct {
	Version       string   `json:"version"`
	ActionVersion string   `json:"action_version"`
	Operations    []string `json:"operations"`
}

type TombstoneEnvelope struct {
	Version       string `json:"version"`
	Algorithm     string `json:"algorithm"`
	KeyID         string `json:"key_id"`
	Kind          string `json:"kind,omitempty"`
	LocatorKeyID  string `json:"locator_key_id,omitempty"`
	LocatorDigest []byte `json:"locator_digest,omitempty"`
	Encapsulation []byte `json:"encapsulation"`
	Nonce         []byte `json:"nonce"`
	Ciphertext    []byte `json:"ciphertext"`
}

// TombstoneClosure starts the adopted 24-calendar-month evidence clock. It
// repeats the replay identity so the closure object remains sufficient even
// after the pre-destructive intent is eventually removed.
type TombstoneClosure struct {
	Version           string           `json:"version"`
	Tombstone         RestoreTombstone `json:"tombstone"`
	ClosedAt          time.Time        `json:"closed_at"`
	EvidenceExpiresAt time.Time        `json:"evidence_expires_at"`
}

type SealedTombstone struct {
	Kind         string
	LocatorKeyID string
	Locator      []byte
	Envelope     TombstoneEnvelope
	Encoded      []byte
	SHA256       []byte
	RetainUntil  time.Time
}

// TombstoneProtector is seal-only. The private replay key is never required by
// the web process or by the database.
type TombstoneProtector struct {
	keyID        string
	publicKey    *ecdh.PublicKey
	locatorKeyID string
	locatorKey   []byte
	random       io.Reader
}

func NewTombstoneProtector(keyID string, publicKey []byte, locatorKeyID string, locatorKey []byte) (*TombstoneProtector, error) {
	if !policyKey.MatchString(keyID) || !policyKey.MatchString(locatorKeyID) || len(locatorKey) != sha256.Size {
		return nil, ErrTombstoneInvalid
	}
	parsed, err := ecdh.X25519().NewPublicKey(publicKey)
	if err != nil {
		return nil, ErrTombstoneInvalid
	}
	return &TombstoneProtector{keyID: keyID, publicKey: parsed, locatorKeyID: locatorKeyID, locatorKey: bytes.Clone(locatorKey), random: rand.Reader}, nil
}

func (p *TombstoneProtector) Seal(record RestoreTombstone) (SealedTombstone, error) {
	if p == nil || p.publicKey == nil || p.random == nil || !validReplayableRestoreTombstone(record) {
		return SealedTombstone{}, ErrTombstoneInvalid
	}
	plaintext, err := json.Marshal(record)
	if err != nil {
		return SealedTombstone{}, ErrTombstoneInvalid
	}
	return p.seal("intent", record.ExecutionID, plaintext, time.Time{})
}

func (p *TombstoneProtector) SealClosure(closure TombstoneClosure) (SealedTombstone, error) {
	if p == nil || p.publicKey == nil || p.random == nil || closure.Version != TombstoneClosureVersion ||
		!validReplayableRestoreTombstone(closure.Tombstone) || closure.ClosedAt.IsZero() ||
		!closure.EvidenceExpiresAt.Equal(closure.ClosedAt.AddDate(0, 24, 0)) {
		return SealedTombstone{}, ErrTombstoneInvalid
	}
	plaintext, err := json.Marshal(closure)
	if err != nil {
		return SealedTombstone{}, ErrTombstoneInvalid
	}
	return p.seal("closure", closure.Tombstone.ExecutionID, plaintext, closure.EvidenceExpiresAt)
}

func (p *TombstoneProtector) seal(kind string, executionID uuid.UUID, plaintext []byte, retainUntil time.Time) (SealedTombstone, error) {
	var zero SealedTombstone
	locatorMAC := hmac.New(sha256.New, p.locatorKey)
	_, _ = locatorMAC.Write(encodeFields("mycfc/restore-tombstone-locator/v1", kind, executionID.String()))
	locator := locatorMAC.Sum(nil)
	ephemeral, err := ecdh.X25519().GenerateKey(p.random)
	if err != nil {
		return zero, ErrTombstoneInvalid
	}
	shared, err := ephemeral.ECDH(p.publicKey)
	if err != nil {
		return zero, ErrTombstoneInvalid
	}
	key := make([]byte, 32)
	if _, err = io.ReadFull(hkdf.New(sha256.New, shared, nil, tombstoneAADV2(kind, p.locatorKeyID, locator, p.keyID)), key); err != nil {
		return zero, ErrTombstoneInvalid
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return zero, ErrTombstoneInvalid
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return zero, ErrTombstoneInvalid
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = io.ReadFull(p.random, nonce); err != nil {
		return zero, ErrTombstoneInvalid
	}
	envelope := TombstoneEnvelope{
		Version: TombstoneEnvelopeVersion, Algorithm: tombstoneAlgorithm, KeyID: p.keyID,
		Kind: kind, LocatorKeyID: p.locatorKeyID, LocatorDigest: bytes.Clone(locator),
		Encapsulation: ephemeral.PublicKey().Bytes(), Nonce: nonce,
		Ciphertext: aead.Seal(nil, nonce, plaintext, tombstoneAADV2(kind, p.locatorKeyID, locator, p.keyID)),
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return zero, ErrTombstoneInvalid
	}
	digest := sha256.Sum256(encoded)
	return SealedTombstone{Kind: kind, LocatorKeyID: p.locatorKeyID, Locator: locator, Envelope: envelope, Encoded: encoded, SHA256: digest[:], RetainUntil: retainUntil}, nil
}

// OpenRestoreTombstone preserves the v1 read path. V1 records are useful as
// historical evidence but intentionally cannot produce a replay prescription.
func OpenRestoreTombstone(privateKey []byte, executionID uuid.UUID, envelope TombstoneEnvelope) (RestoreTombstone, error) {
	var zero RestoreTombstone
	if envelope.Version != TombstoneEnvelopeVersionV1 {
		return zero, ErrTombstoneInvalid
	}
	plaintext, err := openTombstoneEnvelopeV1(privateKey, "intent", executionID, envelope)
	if err != nil {
		return zero, err
	}
	var record RestoreTombstone
	decoder := json.NewDecoder(bytes.NewReader(plaintext))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&record); err != nil || ensureJSONEOF(decoder) != nil || !validLegacyRestoreTombstone(record) || record.ExecutionID != executionID {
		return zero, ErrTombstoneInvalid
	}
	return record, nil
}

// OpenRestoreTombstoneV2 authenticates locator-bound data discovered by
// listing opaque ledger objects; the execution UUID is learned only after AEAD
// verification succeeds.
func OpenRestoreTombstoneV2(privateKey []byte, locatorKeyID string, locator []byte, envelope TombstoneEnvelope) (RestoreTombstone, error) {
	var zero RestoreTombstone
	plaintext, err := openTombstoneEnvelopeV2(privateKey, "intent", locatorKeyID, locator, envelope)
	if err != nil {
		return zero, err
	}
	var record RestoreTombstone
	decoder := json.NewDecoder(bytes.NewReader(plaintext))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&record); err != nil || ensureJSONEOF(decoder) != nil || !validReplayableRestoreTombstone(record) {
		return zero, ErrTombstoneInvalid
	}
	return record, nil
}

func OpenRestoreTombstoneClosure(privateKey []byte, executionID uuid.UUID, envelope TombstoneEnvelope) (TombstoneClosure, error) {
	var zero TombstoneClosure
	if envelope.Version != TombstoneEnvelopeVersionV1 {
		return zero, ErrTombstoneInvalid
	}
	plaintext, err := openTombstoneEnvelopeV1(privateKey, "closure", executionID, envelope)
	if err != nil {
		return zero, err
	}
	var closure TombstoneClosure
	decoder := json.NewDecoder(bytes.NewReader(plaintext))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&closure); err != nil || ensureJSONEOF(decoder) != nil || closure.Version != TombstoneClosureVersionV1 ||
		!validLegacyRestoreTombstone(closure.Tombstone) || closure.Tombstone.ExecutionID != executionID || closure.ClosedAt.IsZero() ||
		!closure.EvidenceExpiresAt.Equal(closure.ClosedAt.AddDate(0, 24, 0)) {
		return zero, ErrTombstoneInvalid
	}
	return closure, nil
}

func OpenRestoreTombstoneClosureV2(privateKey []byte, locatorKeyID string, locator []byte, envelope TombstoneEnvelope) (TombstoneClosure, error) {
	var zero TombstoneClosure
	plaintext, err := openTombstoneEnvelopeV2(privateKey, "closure", locatorKeyID, locator, envelope)
	if err != nil {
		return zero, err
	}
	var closure TombstoneClosure
	decoder := json.NewDecoder(bytes.NewReader(plaintext))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&closure); err != nil || ensureJSONEOF(decoder) != nil || closure.Version != TombstoneClosureVersion ||
		!validReplayableRestoreTombstone(closure.Tombstone) || closure.ClosedAt.IsZero() ||
		!closure.EvidenceExpiresAt.Equal(closure.ClosedAt.AddDate(0, 24, 0)) {
		return zero, ErrTombstoneInvalid
	}
	return closure, nil
}

// IsLegacyTombstoneEnvelope recognizes only the bounded, strict outer shape
// emitted by v1. Its contents cannot be authenticated without the execution
// UUID, so callers may report it as non-replayable but must never import it.
func IsLegacyTombstoneEnvelope(encoded []byte) bool {
	if len(encoded) == 0 || len(encoded) > maxTombstonePayloadBytes {
		return false
	}
	var envelope TombstoneEnvelope
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	return decoder.Decode(&envelope) == nil && ensureJSONEOF(decoder) == nil &&
		envelope.Version == TombstoneEnvelopeVersionV1 && envelope.Algorithm == tombstoneAlgorithm && policyKey.MatchString(envelope.KeyID) &&
		envelope.Kind == "" && envelope.LocatorKeyID == "" && len(envelope.LocatorDigest) == 0 &&
		len(envelope.Encapsulation) == 32 && len(envelope.Nonce) == 12 && len(envelope.Ciphertext) >= 16
}

func openTombstoneEnvelopeV1(privateKey []byte, kind string, executionID uuid.UUID, envelope TombstoneEnvelope) ([]byte, error) {
	if executionID == uuid.Nil || envelope.Version != TombstoneEnvelopeVersionV1 || envelope.Algorithm != tombstoneAlgorithm || !policyKey.MatchString(envelope.KeyID) {
		return nil, ErrTombstoneInvalid
	}
	return openTombstoneEnvelope(privateKey, envelope, tombstoneAADV1(kind, executionID, envelope.KeyID))
}

func openTombstoneEnvelopeV2(privateKey []byte, kind, locatorKeyID string, locator []byte, envelope TombstoneEnvelope) ([]byte, error) {
	if (kind != "intent" && kind != "closure") || !policyKey.MatchString(locatorKeyID) || len(locator) != sha256.Size ||
		envelope.Version != TombstoneEnvelopeVersion || envelope.Algorithm != tombstoneAlgorithm || !policyKey.MatchString(envelope.KeyID) ||
		envelope.Kind != kind || envelope.LocatorKeyID != locatorKeyID || !hmac.Equal(envelope.LocatorDigest, locator) {
		return nil, ErrTombstoneInvalid
	}
	return openTombstoneEnvelope(privateKey, envelope, tombstoneAADV2(kind, locatorKeyID, locator, envelope.KeyID))
}

func openTombstoneEnvelope(privateKey []byte, envelope TombstoneEnvelope, aad []byte) ([]byte, error) {
	private, err := ecdh.X25519().NewPrivateKey(privateKey)
	if err != nil {
		return nil, ErrTombstoneInvalid
	}
	ephemeral, err := ecdh.X25519().NewPublicKey(envelope.Encapsulation)
	if err != nil {
		return nil, ErrTombstoneInvalid
	}
	shared, err := private.ECDH(ephemeral)
	if err != nil {
		return nil, ErrTombstoneInvalid
	}
	key := make([]byte, 32)
	if _, err = io.ReadFull(hkdf.New(sha256.New, shared, nil, aad), key); err != nil {
		return nil, ErrTombstoneInvalid
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrTombstoneInvalid
	}
	aead, err := cipher.NewGCM(block)
	if err != nil || len(envelope.Nonce) != aead.NonceSize() {
		return nil, ErrTombstoneInvalid
	}
	plaintext, err := aead.Open(nil, envelope.Nonce, envelope.Ciphertext, aad)
	if err != nil {
		return nil, ErrTombstoneInvalid
	}
	return plaintext, nil
}

func validRestoreTombstoneIdentity(record RestoreTombstone) bool {
	return record.ExecutionID != uuid.Nil && record.RequestID != uuid.Nil &&
		record.RequestRef != uuid.Nil && record.SubjectUserID != uuid.Nil && len(record.PlanSHA256) == sha256.Size &&
		len(record.WorksetSHA256) == sha256.Size && !record.ExecutionStart.IsZero()
}

func validLegacyRestoreTombstone(record RestoreTombstone) bool {
	return record.Version == TombstoneRecordVersionV1 && record.Replay == nil && validRestoreTombstoneIdentity(record)
}

func validReplayableRestoreTombstone(record RestoreTombstone) bool {
	return record.Version == TombstoneRecordVersion && validRestoreTombstoneIdentity(record) && validReplayPrescription(record.Replay)
}

func validReplayPrescription(prescription *RelationalReplayPrescription) bool {
	if prescription == nil || prescription.Version != TombstoneReplayVersion || prescription.ActionVersion != SupportedActionVersion ||
		len(prescription.Operations) == 0 || len(prescription.Operations) > len(relationalExecutableOperations) {
		return false
	}
	seen := make(map[string]bool, len(prescription.Operations))
	for _, operation := range prescription.Operations {
		if !relationalExecutableOperation(operation) || seen[operation] {
			return false
		}
		seen[operation] = true
	}
	return true
}

func tombstoneAADV1(kind string, executionID uuid.UUID, keyID string) []byte {
	return encodeFields("mycfc/restore-tombstone-aad/v1", kind, TombstoneEnvelopeVersionV1, tombstoneAlgorithm, keyID, executionID.String())
}

func tombstoneAADV2(kind, locatorKeyID string, locator []byte, keyID string) []byte {
	return encodeFields("mycfc/restore-tombstone-aad/v2", kind, TombstoneEnvelopeVersion, tombstoneAlgorithm, keyID, locatorKeyID, hex.EncodeToString(locator))
}

type TombstoneLedgerReceipt struct {
	LocatorKeyID  string
	LocatorDigest []byte
	ObjectVersion string
	CiphertextSHA []byte
	SizeBytes     int64
	WrittenAt     time.Time
	VerifiedAt    time.Time
}

type TombstoneLedger interface {
	Write(context.Context, SealedTombstone) (TombstoneLedgerReceipt, error)
}

type tombstoneLambdaAPI interface {
	Invoke(context.Context, *awslambda.InvokeInput, ...func(*awslambda.Options)) (*awslambda.InvokeOutput, error)
}

type tombstoneBrokerRequest struct {
	Kind          string     `json:"kind"`
	LocatorKeyID  string     `json:"locator_key_id"`
	LocatorDigest []byte     `json:"locator_digest"`
	Payload       []byte     `json:"payload"`
	Checksum      []byte     `json:"checksum"`
	RetainUntil   *time.Time `json:"retain_until,omitempty"`
}

type tombstoneBrokerResponse struct {
	Kind             string     `json:"kind"`
	LocatorKeyID     string     `json:"locator_key_id"`
	LocatorDigest    []byte     `json:"locator_digest"`
	ObjectVersion    string     `json:"object_version"`
	CiphertextSHA256 []byte     `json:"ciphertext_sha256"`
	SizeBytes        int64      `json:"size_bytes"`
	WrittenAt        time.Time  `json:"written_at"`
	VerifiedAt       time.Time  `json:"verified_at"`
	RetainUntil      *time.Time `json:"retain_until,omitempty"`
}

// LambdaTombstoneLedger is the production adapter. The application can invoke
// only one configured broker and sends it only encrypted, bounded material.
// The broker owns all S3 permissions and returns an independently verifiable
// immutable-object receipt.
type LambdaTombstoneLedger struct {
	client       tombstoneLambdaAPI
	functionName string
}

func NewLambdaTombstoneLedger(client tombstoneLambdaAPI, functionName string) (*LambdaTombstoneLedger, error) {
	functionName = strings.TrimSpace(functionName)
	if client == nil || !tombstoneLambdaFunction.MatchString(functionName) {
		return nil, ErrTombstoneInvalid
	}
	return &LambdaTombstoneLedger{client: client, functionName: functionName}, nil
}

func (l *LambdaTombstoneLedger) Write(ctx context.Context, sealed SealedTombstone) (TombstoneLedgerReceipt, error) {
	var zero TombstoneLedgerReceipt
	if l == nil || l.client == nil || !validSealedTombstone(sealed) {
		return zero, ErrTombstoneInvalid
	}
	request := tombstoneBrokerRequest{
		Kind: sealed.Kind, LocatorKeyID: sealed.LocatorKeyID, LocatorDigest: bytes.Clone(sealed.Locator),
		Payload: bytes.Clone(sealed.Encoded), Checksum: bytes.Clone(sealed.SHA256),
	}
	if sealed.Kind == "closure" {
		retainUntil := sealed.RetainUntil.UTC()
		request.RetainUntil = &retainUntil
	}
	payload, err := json.Marshal(request)
	if err != nil || len(payload) == 0 || len(payload) > maxBrokerRequestBytes {
		return zero, ErrTombstoneInvalid
	}
	output, err := l.client.Invoke(ctx, &awslambda.InvokeInput{
		FunctionName: aws.String(l.functionName), InvocationType: lambdatypes.InvocationTypeRequestResponse,
		LogType: lambdatypes.LogTypeNone, Payload: payload,
	})
	if err != nil || output == nil || output.StatusCode != 200 || output.FunctionError != nil ||
		len(output.Payload) == 0 || len(output.Payload) > maxBrokerResponseBytes {
		return zero, ErrTombstoneUnavailable
	}
	var response tombstoneBrokerResponse
	decoder := json.NewDecoder(bytes.NewReader(output.Payload))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&response); err != nil {
		return zero, ErrTombstoneUnavailable
	}
	if err = ensureJSONEOF(decoder); err != nil || !validBrokerResponse(response, sealed) {
		return zero, ErrTombstoneUnavailable
	}
	return TombstoneLedgerReceipt{
		LocatorKeyID: response.LocatorKeyID, LocatorDigest: bytes.Clone(response.LocatorDigest),
		ObjectVersion: response.ObjectVersion, CiphertextSHA: bytes.Clone(response.CiphertextSHA256),
		SizeBytes: response.SizeBytes, WrittenAt: response.WrittenAt.UTC(), VerifiedAt: response.VerifiedAt.UTC(),
	}, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra json.RawMessage
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return ErrTombstoneInvalid
}

func validSealedTombstone(sealed SealedTombstone) bool {
	digest := sha256.Sum256(sealed.Encoded)
	return (sealed.Kind == "intent" || sealed.Kind == "closure") && len(sealed.Locator) == sha256.Size &&
		len(sealed.SHA256) == sha256.Size && len(sealed.Encoded) > 0 && len(sealed.Encoded) <= maxTombstonePayloadBytes &&
		hmac.Equal(digest[:], sealed.SHA256) && policyKey.MatchString(sealed.LocatorKeyID) && ((sealed.Kind == "closure" && !sealed.RetainUntil.IsZero()) ||
		(sealed.Kind == "intent" && sealed.RetainUntil.IsZero()))
}

func validBrokerResponse(response tombstoneBrokerResponse, sealed SealedTombstone) bool {
	if response.Kind != sealed.Kind || response.LocatorKeyID != sealed.LocatorKeyID ||
		!hmac.Equal(response.LocatorDigest, sealed.Locator) || !hmac.Equal(response.CiphertextSHA256, sealed.SHA256) ||
		response.SizeBytes != int64(len(sealed.Encoded)) || response.ObjectVersion == "" ||
		response.ObjectVersion != strings.TrimSpace(response.ObjectVersion) || len(response.ObjectVersion) > 1024 ||
		response.WrittenAt.IsZero() || response.VerifiedAt.IsZero() || response.VerifiedAt.Before(response.WrittenAt) {
		return false
	}
	if sealed.Kind == "intent" {
		return response.RetainUntil == nil
	}
	return response.RetainUntil != nil && response.RetainUntil.Equal(sealed.RetainUntil.UTC()) &&
		!response.VerifiedAt.After(sealed.RetainUntil.UTC())
}

type tombstoneS3API interface {
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	HeadObject(context.Context, *s3.HeadObjectInput, ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
}

// S3TombstoneLedger is a non-production adapter for integration tests and
// controlled repair tooling. Production composition must use
// LambdaTombstoneLedger so the long-lived application role has no S3 ledger or
// object-retention permissions.
type S3TombstoneLedger struct {
	client tombstoneS3API
	bucket string
	prefix string
	now    func() time.Time
}

func NewS3TombstoneLedger(client tombstoneS3API, bucket, prefix string) (*S3TombstoneLedger, error) {
	prefix = strings.TrimSpace(prefix)
	if client == nil || strings.TrimSpace(bucket) == "" || !ledgerPrefix.MatchString(prefix) {
		return nil, ErrTombstoneInvalid
	}
	return &S3TombstoneLedger{client: client, bucket: bucket, prefix: prefix, now: time.Now}, nil
}

func (s *S3TombstoneLedger) Write(ctx context.Context, sealed SealedTombstone) (TombstoneLedgerReceipt, error) {
	var zero TombstoneLedgerReceipt
	if s == nil || s.client == nil || !validSealedTombstone(sealed) {
		return zero, ErrTombstoneInvalid
	}
	writtenAt := s.now().UTC()
	key := s.prefix + sealed.Kind + "/" + hex.EncodeToString(sealed.Locator) + ".json"
	checksum := base64.StdEncoding.EncodeToString(sealed.SHA256)
	input := &s3.PutObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(key), Body: bytes.NewReader(sealed.Encoded),
		ContentLength: aws.Int64(int64(len(sealed.Encoded))), ContentType: aws.String("application/json"),
		ChecksumSHA256: aws.String(checksum), IfNoneMatch: aws.String("*"),
	}
	if sealed.Kind == "closure" {
		input.ObjectLockMode = types.ObjectLockModeCompliance
		input.ObjectLockRetainUntilDate = aws.Time(sealed.RetainUntil.UTC())
	}
	output, putErr := s.client.PutObject(ctx, input)
	version := ""
	if output != nil {
		version = aws.ToString(output.VersionId)
	}
	if putErr != nil {
		// A retry after a crash may find the conditionally-created object. Only
		// its exact checksum is accepted; all other errors remain opaque.
		head, headErr := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key), ChecksumMode: "ENABLED"})
		if headErr != nil || !verifiedTombstoneHead(head, sealed, checksum, "") || aws.ToString(head.VersionId) == "" {
			return zero, ErrTombstoneUnavailable
		}
		version = aws.ToString(head.VersionId)
	}
	if version == "" {
		return zero, ErrTombstoneUnavailable
	}
	head, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key), VersionId: aws.String(version), ChecksumMode: "ENABLED"})
	if err != nil || !verifiedTombstoneHead(head, sealed, checksum, version) {
		return zero, ErrTombstoneUnavailable
	}
	return TombstoneLedgerReceipt{
		LocatorKeyID: sealed.LocatorKeyID, LocatorDigest: bytes.Clone(sealed.Locator), ObjectVersion: version,
		CiphertextSHA: bytes.Clone(sealed.SHA256), SizeBytes: int64(len(sealed.Encoded)), WrittenAt: writtenAt, VerifiedAt: s.now().UTC(),
	}, nil
}

func verifiedTombstoneHead(head *s3.HeadObjectOutput, sealed SealedTombstone, checksum, version string) bool {
	if head == nil || aws.ToString(head.ChecksumSHA256) != checksum || aws.ToInt64(head.ContentLength) != int64(len(sealed.Encoded)) ||
		(version != "" && aws.ToString(head.VersionId) != version) {
		return false
	}
	if sealed.Kind != "closure" {
		return true
	}
	return head.ObjectLockMode == types.ObjectLockModeCompliance && head.ObjectLockRetainUntilDate != nil &&
		head.ObjectLockRetainUntilDate.Equal(sealed.RetainUntil.UTC())
}

type TombstoneExportWorker struct {
	Store     dbgen.DBTX
	Ledger    TombstoneLedger
	Protector *TombstoneProtector
	WorkerRef uuid.UUID
}

// Export is fenced by the active execution lease. The database records the
// independently verified receipt and completes only the tombstone checkpoint.
func (w TombstoneExportWorker) Export(ctx context.Context, lease ExecutionLease) error {
	if w.Store == nil || w.Ledger == nil || w.Protector == nil || w.WorkerRef == uuid.Nil || !validLease(lease) || lease.Job.WorkerRef != w.WorkerRef {
		return ErrTombstoneInvalid
	}
	if len(lease.Checkpoints) != 1 || lease.Checkpoints[0].OperationCode != "BACKUP_TOMBSTONE_REPLAY" || lease.Checkpoints[0].ActionVersion != SupportedActionVersion {
		return ErrTombstoneInvalid
	}
	q := dbgen.New(w.Store)
	contextRow, err := q.PreparePrivacyRestoreTombstone(ctx, dbgen.PreparePrivacyRestoreTombstoneParams{
		JobID: lease.Job.ID, LeaseID: lease.Job.ActiveLeaseID, AttemptID: lease.Job.ActiveAttemptID,
		LeaseEpoch: lease.Job.LeaseEpoch, WorkerRef: w.WorkerRef,
	})
	if err != nil {
		return ErrTombstoneUnavailable
	}
	record := RestoreTombstone{
		Version: TombstoneRecordVersion, ExecutionID: contextRow.ExecutionID, RequestID: contextRow.RequestID,
		RequestRef: contextRow.RequestRef, SubjectUserID: contextRow.SubjectUserID,
		PlanSHA256: bytes.Clone(contextRow.PlanSha256), WorksetSHA256: bytes.Clone(contextRow.WorksetSha256),
		ExecutionStart: contextRow.ExecutionStartedAt.Time,
		Replay:         &RelationalReplayPrescription{Version: TombstoneReplayVersion, ActionVersion: SupportedActionVersion, Operations: slices.Clone(contextRow.ReplayOperations)},
	}
	sealed, err := w.Protector.Seal(record)
	if err != nil {
		return err
	}
	receipt, err := w.Ledger.Write(ctx, sealed)
	if err != nil {
		return err
	}
	_, err = q.ConfirmPrivacyRestoreTombstone(ctx, dbgen.ConfirmPrivacyRestoreTombstoneParams{
		JobID: lease.Job.ID, LeaseID: lease.Job.ActiveLeaseID, AttemptID: lease.Job.ActiveAttemptID,
		LeaseEpoch: lease.Job.LeaseEpoch, WorkerRef: w.WorkerRef, LedgerVersion: TombstoneRecordVersion,
		EncryptionKeyID: sealed.Envelope.KeyID, LocatorKeyID: receipt.LocatorKeyID, LocatorDigest: receipt.LocatorDigest,
		ObjectVersionID: receipt.ObjectVersion, CiphertextSha256: receipt.CiphertextSHA, SizeBytes: receipt.SizeBytes,
		WrittenAt: stamp(receipt.WrittenAt), VerifiedAt: stamp(receipt.VerifiedAt),
	})
	if err != nil {
		return ErrTombstoneUnavailable
	}
	return nil
}

// ExportClosure writes a distinct, non-overwriting closure record. The exact
// timestamps come from the database and are intended to be reused by #248's
// completion transaction; the S3 object is locked through evidence expiry.
func (w TombstoneExportWorker) ExportClosure(ctx context.Context, executionID uuid.UUID) error {
	if w.Store == nil || w.Ledger == nil || w.Protector == nil || w.WorkerRef == uuid.Nil || executionID == uuid.Nil {
		return ErrTombstoneInvalid
	}
	q := dbgen.New(w.Store)
	row, err := q.PreparePrivacyTombstoneClosure(ctx, dbgen.PreparePrivacyTombstoneClosureParams{ExecutionID: executionID, WorkerRef: w.WorkerRef})
	if err != nil {
		return ErrTombstoneUnavailable
	}
	record := RestoreTombstone{
		Version: TombstoneRecordVersion, ExecutionID: row.ExecutionID, RequestID: row.RequestID, RequestRef: row.RequestRef,
		SubjectUserID: row.SubjectUserID, PlanSHA256: bytes.Clone(row.PlanSha256), WorksetSHA256: bytes.Clone(row.WorksetSha256),
		ExecutionStart: row.ExecutionStartedAt.Time,
		Replay:         &RelationalReplayPrescription{Version: TombstoneReplayVersion, ActionVersion: SupportedActionVersion, Operations: slices.Clone(row.ReplayOperations)},
	}
	closure := TombstoneClosure{Version: TombstoneClosureVersion, Tombstone: record, ClosedAt: row.ClosedAt.Time, EvidenceExpiresAt: row.EvidenceExpiresAt.Time}
	sealed, err := w.Protector.SealClosure(closure)
	if err != nil {
		return err
	}
	receipt, err := w.Ledger.Write(ctx, sealed)
	if err != nil {
		return err
	}
	_, err = q.ConfirmPrivacyTombstoneClosure(ctx, dbgen.ConfirmPrivacyTombstoneClosureParams{
		ExecutionID: executionID, WorkerRef: w.WorkerRef, LedgerVersion: closure.Version, EncryptionKeyID: sealed.Envelope.KeyID,
		LocatorKeyID: receipt.LocatorKeyID, LocatorDigest: receipt.LocatorDigest, ObjectVersionID: receipt.ObjectVersion,
		CiphertextSha256: receipt.CiphertextSHA, SizeBytes: receipt.SizeBytes, WrittenAt: stamp(receipt.WrittenAt), VerifiedAt: stamp(receipt.VerifiedAt),
	})
	if err != nil {
		return ErrTombstoneUnavailable
	}
	return nil
}
