// Package privacyreceipt signs and verifies privacy-safe production operation
// receipts. Its wire format is deliberately closed and byte-canonical so a
// receipt cannot acquire unsigned fields or be re-encoded after it leaves the
// production host.
package privacyreceipt

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"time"
)

const (
	Contract               = "mycfc/privacy-production-operation-receipt/v2"
	MaximumReceiptBytes    = 16 << 10
	MaximumVerificationAge = 2 * time.Hour
	FutureClockSkew        = 30 * time.Second
	canonicalTimeLayout    = "2006-01-02T15:04:05Z"
)

var (
	requestIDPattern = regexp.MustCompile(`^[1-9][0-9]{0,19}-[1-9][0-9]{0,4}$`)
	sha40Pattern     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	sha256Pattern    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	imagePattern     = regexp.MustCompile(`^[0-9]{12}\.dkr\.ecr\.[a-z0-9-]+\.amazonaws\.com/mycfc-production@sha256:[0-9a-f]{64}$`)

	allowedOperations = stringSet(
		"preflight", "status", "infrastructure-observe", "policy-import", "activation-disable-provision",
		"receipt-key-provision", "receipt-key-rotate-prepare", "receipt-key-rotate-activate", "receipt-key-revoke",
		"retention-provision", "retention-rotate", "retention-revoke", "retention-run", "retention-enable", "retention-disable",
		"acceptance-provision", "acceptance-rotate", "acceptance-revoke", "acceptance-run",
		"acceptance-canary-retry", "acceptance-canary-failure", "acceptance-canary-aged", "acceptance-canary-heartbeat", "acceptance-canary-recovery",
		"legacy-inventory", "legacy-purge", "legacy-verify", "legacy-credential-remove",
		"backup-run", "backup-posture", "backup-cleanup-inventory", "restore-run", "restore-verify",
		"activation-record", "activation-courier-provision", "activation-courier-rotate", "activation-courier-revoke", "activation-ceremony-open", "activation-disable",
		"flags-enable", "flags-disable", "worker-enable", "worker-disable",
	)

	allowedReasons = stringSet(
		"acceptance_credential_failed", "acceptance_run_failed",
		"activation_ceremony_open_failed", "activation_ceremony_open_receipt_invalid", "activation_courier_credential_failed",
		"activation_disable_failed", "activation_disable_provision_failed", "activation_evidence_failed", "activation_evidence_invalid", "activation_operations_disabled",
		"backup_cleanup_inventory_failed", "backup_posture_failed", "backup_run_failed",
		"credential_operations_disabled", "destructive_operations_disabled", "expected_image_invalid", "image_not_active", "infrastructure_evidence_invalid",
		"legacy_absence_not_proven", "legacy_inventory_approval_missing", "legacy_inventory_failed", "legacy_purge_failed", "legacy_teardown_evidence_invalid",
		"operation_locked", "operation_not_allowlisted", "policy_import_failed", "privacy_activity_not_quiescent", "production_config_failed",
		"receipt_key_change_failed", "receipt_key_rotation_activation_failed", "receipt_key_revoke_stage_failed", "receipt_path_invalid",
		"release_locked", "request_expired_or_overlong", "request_id_invalid", "request_time_invalid",
		"restore_attestation_invalid", "restore_run_failed", "retention_config_failed", "retention_provision_failed", "retention_revoke_failed", "retention_rotate_failed", "retention_run_failed",
		"source_sha_not_active",
	)
)

// Services is the exact service-state observation made after the operation.
type Services struct {
	PrivacyWorker    bool `json:"privacy_worker"`
	PrivacyRetention bool `json:"privacy_retention"`
	PrivacyRestore   bool `json:"privacy_restore"`
	BackupCleanup    bool `json:"backup_cleanup"`
}

// Receipt is the unsigned host observation. Reason must be nil on success and
// an allowlisted, non-sensitive code on rejection.
type Receipt struct {
	Contract           string   `json:"contract"`
	RequestID          string   `json:"request_id"`
	Operation          string   `json:"operation"`
	SourceSHA          string   `json:"source_sha"`
	ExpectedImage      string   `json:"expected_image"`
	EvidenceSHA256     string   `json:"evidence_sha256"`
	RequestSHA256      string   `json:"request_sha256"`
	Result             string   `json:"result"`
	Reason             *string  `json:"reason"`
	IssuedAt           string   `json:"issued_at"`
	StartedAt          string   `json:"started_at"`
	FinishedAt         string   `json:"finished_at"`
	WorkflowRunID      uint64   `json:"workflow_run_id"`
	WorkflowRunAttempt uint32   `json:"workflow_run_attempt"`
	Services           Services `json:"services"`
}

type signedReceipt struct {
	Receipt
	SigningKeySPKISHA256 string `json:"signing_key_spki_sha256"`
	SignatureEd25519     string `json:"signature_ed25519"`
}

// OptionalBool lets callers require a particular service-state observation.
type OptionalBool struct {
	Set   bool
	Value bool
}

// Expectation binds verification to the exact request and workflow identity.
type Expectation struct {
	RequestID          string
	Operation          string
	SourceSHA          string
	ExpectedImage      string
	EvidenceSHA256     string
	RequestSHA256      string
	Result             string
	ReasonSet          bool
	Reason             *string
	IssuedAt           string
	WorkflowRunID      uint64
	WorkflowRunAttempt uint32
	Services           struct {
		PrivacyWorker    OptionalBool
		PrivacyRetention OptionalBool
		PrivacyRestore   OptionalBool
		BackupCleanup    OptionalBool
	}
	MaximumAge time.Duration
}

// EncodeUnsigned returns the one accepted byte representation for a valid
// unsigned receipt. The signing CLI still requires this representation as its
// input rather than silently normalizing arbitrary JSON.
func EncodeUnsigned(receipt Receipt) ([]byte, error) {
	if err := validateReceipt(receipt); err != nil {
		return nil, err
	}
	canonical, err := canonicalUnsigned(receipt)
	if err != nil {
		return nil, errors.New("receipt encoding failed")
	}
	return append(canonical, '\n'), nil
}

// SignCanonical validates a canonical unsigned receipt, binds the signing
// public-key fingerprint, and returns the canonical signed document. Canonical
// receipt files always end in exactly one LF; the signature covers the compact
// canonical object without that file terminator.
func SignCanonical(unsigned []byte, privateKey ed25519.PrivateKey) ([]byte, error) {
	if !validPrivateKey(privateKey) {
		return nil, errors.New("receipt signing key rejected")
	}
	receipt, err := decodeUnsignedCanonical(unsigned)
	if err != nil {
		return nil, err
	}
	digest, err := SPKISHA256(privateKey.Public().(ed25519.PublicKey))
	if err != nil {
		return nil, err
	}
	payload, err := canonicalSigningPayload(receipt, digest)
	if err != nil {
		return nil, errors.New("receipt signing payload rejected")
	}
	signature := ed25519.Sign(privateKey, payload)
	document, err := canonicalSigned(receipt, digest, base64.StdEncoding.EncodeToString(signature))
	if err != nil {
		return nil, errors.New("signed receipt encoding failed")
	}
	return append(document, '\n'), nil
}

// VerifyCanonical rejects noncanonical encodings, unpinned keys, invalid or
// replayed identities, stale receipts, unexpected fields and request binding
// mismatches. It returns the verified privacy-safe fields only.
func VerifyCanonical(document []byte, publicKey ed25519.PublicKey, expectedSPKISHA256 string, expectation Expectation, now time.Time) (Receipt, error) {
	if len(publicKey) != ed25519.PublicKeySize || !sha256Pattern.MatchString(expectedSPKISHA256) || now.IsZero() {
		return Receipt{}, errors.New("receipt verification inputs rejected")
	}
	signed, err := decodeSignedCanonical(document)
	if err != nil {
		return Receipt{}, err
	}
	actualDigest, err := SPKISHA256(publicKey)
	if err != nil || subtle.ConstantTimeCompare([]byte(actualDigest), []byte(expectedSPKISHA256)) != 1 ||
		subtle.ConstantTimeCompare([]byte(signed.SigningKeySPKISHA256), []byte(expectedSPKISHA256)) != 1 {
		return Receipt{}, errors.New("receipt signing key rejected")
	}
	signature, err := base64.StdEncoding.Strict().DecodeString(signed.SignatureEd25519)
	if err != nil || len(signature) != ed25519.SignatureSize || base64.StdEncoding.EncodeToString(signature) != signed.SignatureEd25519 {
		return Receipt{}, errors.New("receipt signature rejected")
	}
	payload, err := canonicalSigningPayload(signed.Receipt, signed.SigningKeySPKISHA256)
	if err != nil || !ed25519.Verify(publicKey, payload, signature) {
		return Receipt{}, errors.New("receipt signature rejected")
	}
	if err = validateExpectation(signed.Receipt, expectation, now.UTC()); err != nil {
		return Receipt{}, err
	}
	return signed.Receipt, nil
}

// SPKISHA256 returns the lower-case SHA-256 of the canonical SubjectPublicKeyInfo DER.
func SPKISHA256(publicKey ed25519.PublicKey) (string, error) {
	if len(publicKey) != ed25519.PublicKeySize {
		return "", errors.New("receipt public key rejected")
	}
	spki, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return "", errors.New("receipt public key rejected")
	}
	digest := sha256.Sum256(spki)
	return hex.EncodeToString(digest[:]), nil
}

func decodeUnsignedCanonical(payload []byte) (Receipt, error) {
	var receipt Receipt
	if len(payload) == 0 || len(payload) > MaximumReceiptBytes || decodeExact(payload, &receipt) != nil {
		return Receipt{}, errors.New("receipt input rejected")
	}
	if err := validateReceipt(receipt); err != nil {
		return Receipt{}, err
	}
	canonical, err := canonicalUnsigned(receipt)
	if err != nil || !bytes.Equal(payload, append(canonical, '\n')) {
		return Receipt{}, errors.New("receipt input is not canonical")
	}
	return receipt, nil
}

func decodeSignedCanonical(payload []byte) (signedReceipt, error) {
	var receipt signedReceipt
	if len(payload) == 0 || len(payload) > MaximumReceiptBytes || decodeExact(payload, &receipt) != nil {
		return signedReceipt{}, errors.New("signed receipt rejected")
	}
	if err := validateReceipt(receipt.Receipt); err != nil {
		return signedReceipt{}, err
	}
	if !sha256Pattern.MatchString(receipt.SigningKeySPKISHA256) || receipt.SignatureEd25519 == "" {
		return signedReceipt{}, errors.New("signed receipt rejected")
	}
	canonical, err := canonicalSigned(receipt.Receipt, receipt.SigningKeySPKISHA256, receipt.SignatureEd25519)
	if err != nil || !bytes.Equal(payload, append(canonical, '\n')) {
		return signedReceipt{}, errors.New("signed receipt is not canonical")
	}
	return receipt, nil
}

func validateReceipt(receipt Receipt) error {
	if receipt.Contract != Contract || !requestIDPattern.MatchString(receipt.RequestID) || !allowedOperations[receipt.Operation] ||
		!sha40Pattern.MatchString(receipt.SourceSHA) || !imagePattern.MatchString(receipt.ExpectedImage) ||
		!sha256Pattern.MatchString(receipt.EvidenceSHA256) || !sha256Pattern.MatchString(receipt.RequestSHA256) ||
		receipt.WorkflowRunID == 0 || receipt.WorkflowRunAttempt == 0 || receipt.RequestID != strconv.FormatUint(receipt.WorkflowRunID, 10)+"-"+strconv.FormatUint(uint64(receipt.WorkflowRunAttempt), 10) {
		return errors.New("receipt fields rejected")
	}
	issued, issuedErr := parseCanonicalTime(receipt.IssuedAt)
	started, startedErr := parseCanonicalTime(receipt.StartedAt)
	finished, finishedErr := parseCanonicalTime(receipt.FinishedAt)
	if issuedErr != nil || startedErr != nil || finishedErr != nil || started.Before(issued) || finished.Before(started) || finished.Sub(issued) > MaximumVerificationAge {
		return errors.New("receipt timestamps rejected")
	}
	switch receipt.Result {
	case "SUCCEEDED":
		if receipt.Reason != nil {
			return errors.New("receipt result and reason rejected")
		}
	case "REJECTED":
		if receipt.Reason == nil || !allowedReasons[*receipt.Reason] {
			return errors.New("receipt result and reason rejected")
		}
	default:
		return errors.New("receipt result rejected")
	}
	return nil
}

func validateExpectation(receipt Receipt, expected Expectation, now time.Time) error {
	if expected.MaximumAge <= 0 || expected.MaximumAge > MaximumVerificationAge || expected.RequestID == "" || expected.Operation == "" ||
		expected.SourceSHA == "" || expected.ExpectedImage == "" || expected.EvidenceSHA256 == "" || expected.RequestSHA256 == "" ||
		expected.Result == "" || !expected.ReasonSet || expected.IssuedAt == "" || expected.WorkflowRunID == 0 || expected.WorkflowRunAttempt == 0 {
		return errors.New("receipt expectation rejected")
	}
	if receipt.RequestID != expected.RequestID || receipt.Operation != expected.Operation || receipt.SourceSHA != expected.SourceSHA ||
		receipt.ExpectedImage != expected.ExpectedImage || receipt.EvidenceSHA256 != expected.EvidenceSHA256 || receipt.RequestSHA256 != expected.RequestSHA256 ||
		receipt.Result != expected.Result || !equalReason(receipt.Reason, expected.Reason) || receipt.WorkflowRunID != expected.WorkflowRunID ||
		receipt.WorkflowRunAttempt != expected.WorkflowRunAttempt || receipt.IssuedAt != expected.IssuedAt {
		return errors.New("receipt does not match request")
	}
	if mismatchOptional(receipt.Services.PrivacyWorker, expected.Services.PrivacyWorker) ||
		mismatchOptional(receipt.Services.PrivacyRetention, expected.Services.PrivacyRetention) ||
		mismatchOptional(receipt.Services.PrivacyRestore, expected.Services.PrivacyRestore) ||
		mismatchOptional(receipt.Services.BackupCleanup, expected.Services.BackupCleanup) {
		return errors.New("receipt service state does not match expectation")
	}
	issued, _ := parseCanonicalTime(receipt.IssuedAt)
	started, _ := parseCanonicalTime(receipt.StartedAt)
	finished, _ := parseCanonicalTime(receipt.FinishedAt)
	if issued.After(now.Add(FutureClockSkew)) || started.After(now.Add(FutureClockSkew)) || finished.After(now.Add(FutureClockSkew)) || now.Sub(finished) > expected.MaximumAge {
		return errors.New("receipt is stale or from the future")
	}
	return nil
}

func equalReason(actual, expected *string) bool {
	if actual == nil || expected == nil {
		return actual == nil && expected == nil
	}
	return *actual == *expected
}

func mismatchOptional(actual bool, expected OptionalBool) bool {
	return expected.Set && actual != expected.Value
}

func parseCanonicalTime(value string) (time.Time, error) {
	parsed, err := time.Parse(canonicalTimeLayout, value)
	if err != nil || parsed.Format(canonicalTimeLayout) != value {
		return time.Time{}, errors.New("noncanonical receipt timestamp")
	}
	return parsed, nil
}

func validPrivateKey(privateKey ed25519.PrivateKey) bool {
	if len(privateKey) != ed25519.PrivateKeySize {
		return false
	}
	canonical := ed25519.NewKeyFromSeed(privateKey.Seed())
	return subtle.ConstantTimeCompare(privateKey, canonical) == 1
}

func canonicalUnsigned(receipt Receipt) ([]byte, error) {
	return json.Marshal(receiptMap(receipt))
}

func canonicalSigningPayload(receipt Receipt, digest string) ([]byte, error) {
	value := receiptMap(receipt)
	value["signing_key_spki_sha256"] = digest
	return json.Marshal(value)
}

func canonicalSigned(receipt Receipt, digest, signature string) ([]byte, error) {
	value := receiptMap(receipt)
	value["signing_key_spki_sha256"] = digest
	value["signature_ed25519"] = signature
	return json.Marshal(value)
}

func receiptMap(receipt Receipt) map[string]any {
	var reason any
	if receipt.Reason != nil {
		reason = *receipt.Reason
	}
	return map[string]any{
		"contract":        receipt.Contract,
		"evidence_sha256": receipt.EvidenceSHA256,
		"expected_image":  receipt.ExpectedImage,
		"finished_at":     receipt.FinishedAt,
		"issued_at":       receipt.IssuedAt,
		"operation":       receipt.Operation,
		"reason":          reason,
		"request_id":      receipt.RequestID,
		"request_sha256":  receipt.RequestSHA256,
		"result":          receipt.Result,
		"services": map[string]bool{
			"backup_cleanup":    receipt.Services.BackupCleanup,
			"privacy_restore":   receipt.Services.PrivacyRestore,
			"privacy_retention": receipt.Services.PrivacyRetention,
			"privacy_worker":    receipt.Services.PrivacyWorker,
		},
		"source_sha":           receipt.SourceSHA,
		"started_at":           receipt.StartedAt,
		"workflow_run_attempt": receipt.WorkflowRunAttempt,
		"workflow_run_id":      receipt.WorkflowRunID,
	}
}

func decodeExact(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		return errors.New("more than one JSON value")
	}
	return nil
}

func stringSet(values ...string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		if result[value] {
			panic(fmt.Sprintf("duplicate closed-set value %q", value))
		}
		result[value] = true
	}
	return result
}
