package privacyreceipt

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSignAndVerifyCanonicalReceipt(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 17, 8, 10, 0, 0, time.UTC)
	receipt := validReceipt(now)
	unsigned := mustUnsigned(t, receipt)
	signed, err := SignCanonical(unsigned, privateKey)
	if err != nil {
		t.Fatalf("sign receipt: %v", err)
	}
	repeated, err := SignCanonical(unsigned, privateKey)
	if err != nil || !bytes.Equal(signed, repeated) {
		t.Fatalf("signature was not deterministic: err=%v", err)
	}
	digest, err := SPKISHA256(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := VerifyCanonical(signed, publicKey, digest, validExpectation(receipt), now)
	if err != nil {
		t.Fatalf("verify receipt: %v", err)
	}
	if verified != receipt {
		t.Fatalf("verified receipt mismatch: got %#v want %#v", verified, receipt)
	}
	if signed[len(signed)-1] != '\n' || bytes.Count(signed, []byte{'\n'}) != 1 {
		t.Fatalf("signed receipt does not have one canonical LF: %q", signed)
	}
}

func TestSignRejectsMalformedOrNoncanonicalReceipt(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 17, 8, 10, 0, 0, time.UTC)
	valid := validReceipt(now)
	rejected := "release_locked"
	tests := []struct {
		name   string
		mutate func(*Receipt)
	}{
		{"contract", func(r *Receipt) { r.Contract = Contract + "/future" }},
		{"request identity", func(r *Receipt) { r.RequestID = "42-3" }},
		{"operation", func(r *Receipt) { r.Operation = "shell" }},
		{"source", func(r *Receipt) { r.SourceSHA = strings.Repeat("A", 40) }},
		{"image", func(r *Receipt) { r.ExpectedImage = "registry.invalid/image@sha256:" + strings.Repeat("a", 64) }},
		{"evidence digest", func(r *Receipt) { r.EvidenceSHA256 = strings.Repeat("a", 63) }},
		{"request digest", func(r *Receipt) { r.RequestSHA256 = strings.Repeat("a", 65) }},
		{"started before issue", func(r *Receipt) { r.StartedAt = now.Add(-3 * time.Minute).Format(canonicalTimeLayout) }},
		{"finished before start", func(r *Receipt) { r.FinishedAt = now.Add(-2 * time.Minute).Format(canonicalTimeLayout) }},
		{"overlong duration", func(r *Receipt) {
			r.FinishedAt = now.Add(MaximumVerificationAge + time.Second).Format(canonicalTimeLayout)
		}},
		{"noncanonical time", func(r *Receipt) { r.IssuedAt = "2026-09-17T08:00:00+00:00" }},
		{"unknown result", func(r *Receipt) { r.Result = "DONE" }},
		{"success with reason", func(r *Receipt) { r.Reason = &rejected }},
		{"rejection without reason", func(r *Receipt) { r.Result = "REJECTED" }},
		{"rejection unknown reason", func(r *Receipt) { reason := "database_error"; r.Result = "REJECTED"; r.Reason = &reason }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			receipt := valid
			test.mutate(&receipt)
			if _, err := SignCanonical(mustMarshalReceipt(t, receipt), privateKey); err == nil {
				t.Fatal("malformed receipt accepted")
			}
		})
	}

	canonical := mustUnsigned(t, valid)
	for name, payload := range map[string][]byte{
		"leading space": append([]byte(" "), canonical...),
		"missing LF":    bytes.TrimSuffix(canonical, []byte{'\n'}),
		"double LF":     append(append([]byte{}, canonical...), '\n'),
		"pretty":        mustIndent(t, canonical),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := SignCanonical(payload, privateKey); err == nil {
				t.Fatal("noncanonical receipt accepted")
			}
		})
	}

	var object map[string]any
	if err := json.Unmarshal(canonical, &object); err != nil {
		t.Fatal(err)
	}
	object["extra"] = true
	unknown, _ := json.Marshal(object)
	if _, err := SignCanonical(append(unknown, '\n'), privateKey); err == nil {
		t.Fatal("unknown field accepted")
	}

	badPrivate := append(ed25519.PrivateKey(nil), privateKey...)
	badPrivate[len(badPrivate)-1] ^= 1
	if _, err := SignCanonical(canonical, badPrivate); err == nil {
		t.Fatal("noncanonical private key accepted")
	}
}

func TestVerifyRejectsTamperingEncodingKeysFreshnessAndMismatch(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	otherPublic, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 17, 8, 10, 0, 0, time.UTC)
	receipt := validReceipt(now)
	signed, err := SignCanonical(mustUnsigned(t, receipt), privateKey)
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := SPKISHA256(publicKey)
	otherDigest, _ := SPKISHA256(otherPublic)
	expected := validExpectation(receipt)

	tests := []struct {
		name     string
		document []byte
		key      ed25519.PublicKey
		digest   string
		expected Expectation
		now      time.Time
	}{
		{"leading whitespace", append([]byte(" "), signed...), publicKey, digest, expected, now},
		{"missing LF", bytes.TrimSuffix(signed, []byte{'\n'}), publicKey, digest, expected, now},
		{"wrong public key", signed, otherPublic, otherDigest, expected, now},
		{"wrong pin", signed, publicKey, otherDigest, expected, now},
		{"stale", signed, publicKey, digest, expected, now.Add(expected.MaximumAge + time.Second)},
		{"future", signed, publicKey, digest, expected, now.Add(-FutureClockSkew - time.Second)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := VerifyCanonical(test.document, test.key, test.digest, test.expected, test.now); err == nil {
				t.Fatal("invalid signed receipt accepted")
			}
		})
	}

	var object map[string]any
	if err := json.Unmarshal(signed, &object); err != nil {
		t.Fatal(err)
	}
	object["extra"] = true
	unknown, _ := json.Marshal(object)
	if _, err := VerifyCanonical(append(unknown, '\n'), publicKey, digest, expected, now); err == nil {
		t.Fatal("unknown signed field accepted")
	}
	delete(object, "extra")
	object["result"] = "REJECTED"
	tampered, _ := json.Marshal(object)
	if _, err := VerifyCanonical(append(tampered, '\n'), publicKey, digest, expected, now); err == nil {
		t.Fatal("tampered signed field accepted")
	}
	object["result"] = "SUCCEEDED"
	object["signature_ed25519"] = base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
	badSignature, _ := json.Marshal(object)
	if _, err := VerifyCanonical(append(badSignature, '\n'), publicKey, digest, expected, now); err == nil {
		t.Fatal("invalid signature accepted")
	}

	mismatchCases := []struct {
		name   string
		mutate func(*Expectation)
	}{
		{"request id", func(e *Expectation) { e.RequestID = "7654322-3" }},
		{"operation", func(e *Expectation) { e.Operation = "preflight" }},
		{"source", func(e *Expectation) { e.SourceSHA = strings.Repeat("b", 40) }},
		{"image", func(e *Expectation) { e.ExpectedImage = strings.Replace(e.ExpectedImage, "a", "b", 1) }},
		{"evidence", func(e *Expectation) { e.EvidenceSHA256 = strings.Repeat("b", 64) }},
		{"request hash", func(e *Expectation) { e.RequestSHA256 = strings.Repeat("c", 64) }},
		{"result", func(e *Expectation) { e.Result = "REJECTED"; reason := "release_locked"; e.Reason = &reason }},
		{"reason", func(e *Expectation) { reason := "release_locked"; e.Reason = &reason }},
		{"issued at", func(e *Expectation) { e.IssuedAt = now.Add(-time.Hour).Format(canonicalTimeLayout) }},
		{"run id", func(e *Expectation) { e.WorkflowRunID++ }},
		{"run attempt", func(e *Expectation) { e.WorkflowRunAttempt++ }},
		{"worker service", func(e *Expectation) { e.Services.PrivacyWorker = OptionalBool{Set: true, Value: false} }},
		{"retention service", func(e *Expectation) { e.Services.PrivacyRetention = OptionalBool{Set: true, Value: true} }},
		{"restore service", func(e *Expectation) { e.Services.PrivacyRestore = OptionalBool{Set: true, Value: false} }},
		{"cleanup service", func(e *Expectation) { e.Services.BackupCleanup = OptionalBool{Set: true, Value: true} }},
	}
	for _, test := range mismatchCases {
		t.Run("mismatch "+test.name, func(t *testing.T) {
			changed := expected
			test.mutate(&changed)
			if _, err := VerifyCanonical(signed, publicKey, digest, changed, now); err == nil {
				t.Fatal("mismatched expectation accepted")
			}
		})
	}
}

func TestRejectedReceiptRequiresExactAllowlistedReason(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 17, 8, 10, 0, 0, time.UTC)
	receipt := validReceipt(now)
	reason := "release_locked"
	receipt.Result, receipt.Reason = "REJECTED", &reason
	signed, err := SignCanonical(mustUnsigned(t, receipt), privateKey)
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := SPKISHA256(publicKey)
	expected := validExpectation(receipt)
	if _, err = VerifyCanonical(signed, publicKey, digest, expected, now); err != nil {
		t.Fatalf("allowlisted rejection rejected: %v", err)
	}
	expected.Reason = nil
	if _, err = VerifyCanonical(signed, publicKey, digest, expected, now); err == nil {
		t.Fatal("rejection verified as success reason")
	}
}

func TestReceiptKeyLifecycleUsesOnlyClosedOperationAndReasonCodes(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 17, 8, 10, 0, 0, time.UTC)
	operations := []string{"receipt-key-provision", "receipt-key-rotate-prepare", "receipt-key-rotate-activate", "receipt-key-revoke"}
	for _, operation := range operations {
		t.Run(operation, func(t *testing.T) {
			receipt := validReceipt(now)
			receipt.Operation = operation
			if _, err := SignCanonical(mustUnsigned(t, receipt), privateKey); err != nil {
				t.Fatalf("closed receipt-key operation rejected: %v", err)
			}
		})
	}
	reasons := []string{"receipt_key_change_failed", "receipt_key_rotation_activation_failed", "receipt_key_revoke_stage_failed"}
	for _, reason := range reasons {
		t.Run(reason, func(t *testing.T) {
			receipt := validReceipt(now)
			receipt.Result, receipt.Reason = "REJECTED", &reason
			if _, err := SignCanonical(mustUnsigned(t, receipt), privateKey); err != nil {
				t.Fatalf("closed receipt-key reason rejected: %v", err)
			}
		})
	}
}

func validReceipt(now time.Time) Receipt {
	return Receipt{
		Contract: Contract, RequestID: "7654321-2", Operation: "worker-enable",
		SourceSHA:      strings.Repeat("a", 40),
		ExpectedImage:  "334960985019.dkr.ecr.eu-west-1.amazonaws.com/mycfc-production@sha256:" + strings.Repeat("a", 64),
		EvidenceSHA256: strings.Repeat("0", 64), RequestSHA256: strings.Repeat("b", 64),
		Result: "SUCCEEDED", Reason: nil,
		IssuedAt:  now.Add(-2 * time.Minute).Format(canonicalTimeLayout),
		StartedAt: now.Add(-time.Minute).Format(canonicalTimeLayout), FinishedAt: now.Format(canonicalTimeLayout),
		WorkflowRunID: 7654321, WorkflowRunAttempt: 2,
		Services: Services{PrivacyWorker: true, PrivacyRetention: false, PrivacyRestore: true, BackupCleanup: false},
	}
}

func validExpectation(receipt Receipt) Expectation {
	expected := Expectation{
		RequestID: receipt.RequestID, Operation: receipt.Operation, SourceSHA: receipt.SourceSHA,
		ExpectedImage: receipt.ExpectedImage, EvidenceSHA256: receipt.EvidenceSHA256, RequestSHA256: receipt.RequestSHA256,
		Result: receipt.Result, ReasonSet: true, Reason: receipt.Reason, IssuedAt: receipt.IssuedAt,
		WorkflowRunID: receipt.WorkflowRunID, WorkflowRunAttempt: receipt.WorkflowRunAttempt, MaximumAge: 15 * time.Minute,
	}
	expected.Services.PrivacyWorker = OptionalBool{Set: true, Value: receipt.Services.PrivacyWorker}
	expected.Services.PrivacyRetention = OptionalBool{Set: true, Value: receipt.Services.PrivacyRetention}
	expected.Services.PrivacyRestore = OptionalBool{Set: true, Value: receipt.Services.PrivacyRestore}
	expected.Services.BackupCleanup = OptionalBool{Set: true, Value: receipt.Services.BackupCleanup}
	return expected
}

func mustUnsigned(t *testing.T, receipt Receipt) []byte {
	t.Helper()
	payload, err := canonicalUnsigned(receipt)
	if err != nil {
		t.Fatal(err)
	}
	return append(payload, '\n')
}

func mustMarshalReceipt(t *testing.T, receipt Receipt) []byte {
	t.Helper()
	payload, err := canonicalUnsigned(receipt)
	if err != nil {
		t.Fatal(err)
	}
	return append(payload, '\n')
}

func mustIndent(t *testing.T, payload []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	if err := json.Indent(&output, bytes.TrimSpace(payload), "", "  "); err != nil {
		t.Fatal(err)
	}
	output.WriteByte('\n')
	return output.Bytes()
}
