package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cfcoimbra/mycfc/internal/privacyreceipt"
)

func TestVerifyCommandChecksExactBindingsAndPrintsOnlyStatus(t *testing.T) {
	nowValue := time.Date(2026, 9, 17, 8, 10, 0, 0, time.UTC)
	previousNow := now
	now = func() time.Time { return nowValue }
	t.Cleanup(func() { now = previousNow })

	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	receipt := commandReceipt(nowValue)
	unsigned, err := privacyreceipt.EncodeUnsigned(receipt)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := privacyreceipt.SignCanonical(unsigned, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := privacyreceipt.SPKISHA256(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	receiptPath := filepath.Join(directory, "receipt.json")
	publicPath := filepath.Join(directory, "receipt-public.pem")
	writeMode(t, receiptPath, signed, 0o600)
	spki, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	writeMode(t, publicPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: spki}), 0o644)

	args := verifyArgs(receiptPath, publicPath, digest, receipt)
	var output bytes.Buffer
	if err = run(args, &output); err != nil {
		t.Fatalf("verify command: %v", err)
	}
	if output.String() != "receipt_verified\n" {
		t.Fatalf("unexpected verification output %q", output.String())
	}
	for _, sensitive := range []string{receipt.ExpectedImage, receipt.RequestSHA256, string(signed)} {
		if strings.Contains(output.String(), sensitive) {
			t.Fatalf("verification output exposed receipt material %q", sensitive)
		}
	}
	reason := "retention_run_failed"
	receipt.Reason = &reason
	receipt.Result = "REJECTED"
	unsigned, err = privacyreceipt.EncodeUnsigned(receipt)
	if err != nil {
		t.Fatal(err)
	}
	signed, err = privacyreceipt.SignCanonical(unsigned, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	writeMode(t, receiptPath, signed, 0o600)
	args = replaceFlag(verifyArgs(receiptPath, publicPath, digest, receipt), "--expected-reason", reason)
	output.Reset()
	if err = run(args, &output); err != nil || output.String() != "receipt_verified\n" {
		t.Fatalf("verify receipt reason: err=%v output=%q", err, output.String())
	}

	bad := append([]string(nil), args...)
	for index := range bad {
		if bad[index] == receipt.Operation {
			bad[index] = "status"
			break
		}
	}
	output.Reset()
	if err = run(bad, &output); err == nil || output.Len() != 0 {
		t.Fatalf("mismatched receipt accepted or printed output: err=%v output=%q", err, output.String())
	}
}

func TestVerifyCommandRejectsUnsafeFilesAndArguments(t *testing.T) {
	nowValue := time.Now().UTC().Truncate(time.Second)
	previousNow := now
	now = func() time.Time { return nowValue }
	t.Cleanup(func() { now = previousNow })
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	receipt := commandReceipt(nowValue)
	unsigned, _ := privacyreceipt.EncodeUnsigned(receipt)
	signed, _ := privacyreceipt.SignCanonical(unsigned, privateKey)
	digest, _ := privacyreceipt.SPKISHA256(publicKey)
	spki, _ := x509.MarshalPKIXPublicKey(publicKey)
	directory := t.TempDir()
	receiptPath := filepath.Join(directory, "receipt.json")
	publicPath := filepath.Join(directory, "public.der")
	writeMode(t, receiptPath, signed, 0o600)
	writeMode(t, publicPath, spki, 0o644)
	valid := verifyArgs(receiptPath, publicPath, digest, receipt)

	tests := []struct {
		name  string
		setup func() []string
	}{
		{"unknown command", func() []string { return []string{"inspect"} }},
		{"extra argument", func() []string { return append(append([]string(nil), valid...), "extra") }},
		{"overlong age", func() []string { return replaceFlag(valid, "--max-age", "2h0m1s") }},
		{"zero age", func() []string { return replaceFlag(valid, "--max-age", "0s") }},
		{"noncanonical bool", func() []string { return replaceFlag(valid, "--expected-privacy-worker-active", "TRUE") }},
		{"writable receipt", func() []string {
			if err := os.Chmod(receiptPath, 0o644); err != nil {
				t.Fatal(err)
			}
			return valid
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			defer func() { _ = os.Chmod(receiptPath, 0o600) }()
			var output bytes.Buffer
			if err := run(test.setup(), &output); err == nil || output.Len() != 0 {
				t.Fatalf("unsafe invocation accepted: err=%v output=%q", err, output.String())
			}
		})
	}

	link := filepath.Join(directory, "receipt-link.json")
	if err := os.Symlink(receiptPath, link); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := run(replaceFlag(valid, "--input", link), &output); err == nil {
		t.Fatal("symlink receipt accepted")
	}
	if err := os.Chmod(publicPath, 0o666); err != nil {
		t.Fatal(err)
	}
	if err := run(valid, &output); err == nil {
		t.Fatal("writable public key accepted")
	}
}

func TestKeyParsersRequireCanonicalEd25519SPKIAndPKCS8(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})
	parsedPrivate, err := parsePrivateKey(privatePEM)
	if err != nil || !bytes.Equal(parsedPrivate, privateKey) {
		t.Fatalf("canonical private key rejected: %v", err)
	}
	publicDER, _ := x509.MarshalPKIXPublicKey(publicKey)
	for name, input := range map[string][]byte{
		"DER": publicDER,
		"PEM": pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}),
	} {
		t.Run(name, func(t *testing.T) {
			parsed, parseErr := parsePublicKey(input)
			if parseErr != nil || !bytes.Equal(parsed, publicKey) {
				t.Fatalf("canonical public key rejected: %v", parseErr)
			}
		})
	}
	if _, err = parsePrivateKey(pem.EncodeToMemory(&pem.Block{Type: "ED25519 PRIVATE KEY", Bytes: privateDER})); err == nil {
		t.Fatal("wrong private PEM type accepted")
	}
	if _, err = parsePublicKey(append(publicDER, 0)); err == nil {
		t.Fatal("public key with trailing data accepted")
	}
	if _, err = parsePublicKey(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Headers: map[string]string{"Comment": "bad"}, Bytes: publicDER})); err == nil {
		t.Fatal("public PEM headers accepted")
	}
}

func TestSignCommandIsRootOnly(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("test exercises the ordinary non-root development environment")
	}
	var output bytes.Buffer
	if err := run([]string{"sign", "--input", "/missing", "--output", "/missing", "--private-key", "/missing"}, &output); err == nil || output.Len() != 0 {
		t.Fatalf("non-root signing accepted: err=%v output=%q", err, output.String())
	}
}

func TestSignCommandRootRoundTrip(t *testing.T) {
	nowValue := time.Now().UTC().Truncate(time.Second)
	previousNow := now
	now = func() time.Time { return nowValue }
	t.Cleanup(func() { now = previousNow })
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	receipt := commandReceipt(nowValue)
	unsigned, err := privacyreceipt.EncodeUnsigned(receipt)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	if err = os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	inputPath := filepath.Join(directory, "unsigned.json")
	privatePath := filepath.Join(directory, "private.pem")
	outputPath := filepath.Join(directory, "signed.json")
	publicPath := filepath.Join(directory, "public.pem")
	writeMode(t, inputPath, unsigned, 0o600)
	writeMode(t, privatePath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}), 0o600)
	var output bytes.Buffer
	args := []string{"--input", inputPath, "--output", outputPath, "--private-key", privatePath}
	if err = runSignAs(args, &output, 0, uint32(os.Geteuid()), uint32(os.Getegid())); err != nil {
		t.Fatalf("root sign command: %v", err)
	}
	if output.String() != "receipt_signed\n" {
		t.Fatalf("unexpected sign output %q", output.String())
	}
	info, err := os.Lstat(outputPath)
	if err != nil || info.Mode().Perm() != 0o600 || !info.Mode().IsRegular() {
		t.Fatalf("signed output mode rejected: info=%v err=%v", info, err)
	}
	output.Reset()
	if err = runSignAs(args, &output, 0, uint32(os.Geteuid()), uint32(os.Getegid())); err == nil {
		t.Fatal("existing signed output overwritten")
	}
	spki, _ := x509.MarshalPKIXPublicKey(publicKey)
	writeMode(t, publicPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: spki}), 0o644)
	digest, _ := privacyreceipt.SPKISHA256(publicKey)
	output.Reset()
	if err = run(verifyArgs(outputPath, publicPath, digest, receipt), &output); err != nil || output.String() != "receipt_verified\n" {
		t.Fatalf("verify root-signed receipt: err=%v output=%q", err, output.String())
	}
}

func TestSignCommandRejectsBadArgumentsInputsAndKeys(t *testing.T) {
	uid, gid := uint32(os.Geteuid()), uint32(os.Getegid())
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	inputPath := filepath.Join(directory, "unsigned.json")
	keyPath := filepath.Join(directory, "private.pem")
	outputPath := filepath.Join(directory, "signed.json")
	writeMode(t, inputPath, []byte(`{"not":"a canonical receipt"}`), 0o600)
	writeMode(t, keyPath, []byte("not a private key"), 0o600)

	for name, args := range map[string][]string{
		"missing":       nil,
		"extra":         {"--input", inputPath, "--output", outputPath, "--private-key", keyPath, "extra"},
		"missing input": {"--output", outputPath, "--private-key", keyPath},
	} {
		t.Run(name, func(t *testing.T) {
			if err := runSignAs(args, io.Discard, 0, uid, gid); err == nil {
				t.Fatal("invalid signing invocation accepted")
			}
		})
	}
	if err := runSignAs([]string{"--input", filepath.Join(directory, "missing"), "--output", outputPath, "--private-key", keyPath}, io.Discard, 0, uid, gid); err == nil {
		t.Fatal("missing input accepted")
	}
	if err := runSignAs([]string{"--input", inputPath, "--output", outputPath, "--private-key", filepath.Join(directory, "missing")}, io.Discard, 0, uid, gid); err == nil {
		t.Fatal("missing private key accepted")
	}
	if err := runSignAs([]string{"--input", inputPath, "--output", outputPath, "--private-key", keyPath}, io.Discard, 0, uid, gid); err == nil {
		t.Fatal("invalid private key accepted")
	}
}

func TestProtectedFileAndOutputBoundaries(t *testing.T) {
	uid, gid := uint32(os.Geteuid()), uint32(os.Getegid())
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "input")
	writeMode(t, path, []byte("payload"), 0o600)
	policy := filePolicy{owners: map[uint32]bool{uid: true}, exactModes: map[os.FileMode]bool{0o600: true}, requiredGroup: true, groupID: gid}
	if got, err := readProtected(path, 7, policy); err != nil || string(got) != "payload" {
		t.Fatalf("protected file = %q, %v", got, err)
	}
	for name, maximum := range map[string]int64{"zero maximum": 0, "too small": 6} {
		t.Run(name, func(t *testing.T) {
			if _, err := readProtected(path, maximum, policy); err == nil {
				t.Fatal("invalid size accepted")
			}
		})
	}
	empty := filepath.Join(directory, "empty")
	writeMode(t, empty, nil, 0o600)
	if _, err := readProtected(empty, 1, policy); err == nil {
		t.Fatal("empty protected file accepted")
	}
	wrongGroup := policy
	wrongGroup.groupID++
	if _, err := readProtected(path, 7, wrongGroup); err == nil {
		t.Fatal("wrong group accepted")
	}

	outputPath := filepath.Join(directory, "output")
	if err := writeExclusive(outputPath, []byte("signed"), uid, gid); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(outputPath); err != nil || string(got) != "signed" {
		t.Fatalf("exclusive output = %q, %v", got, err)
	}
	if err := writeExclusive(outputPath, []byte("replacement"), uid, gid); err == nil {
		t.Fatal("exclusive output overwritten")
	}
	if err := writeExclusive(filepath.Join(directory, "empty-output"), nil, uid, gid); err == nil {
		t.Fatal("empty output accepted")
	}
	if err := writeExclusive(filepath.Join(directory, "wrong-owner"), []byte("signed"), uid+1, gid); err == nil {
		t.Fatal("wrong output owner accepted")
	}
	unsafeDirectory := filepath.Join(directory, "unsafe")
	if err := os.Mkdir(unsafeDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeExclusive(filepath.Join(unsafeDirectory, "output"), []byte("signed"), uid, gid); err == nil {
		t.Fatal("unsafe output directory accepted")
	}
}

func TestReceiptUtilityBoundaries(t *testing.T) {
	if err := run(nil, io.Discard); err == nil {
		t.Fatal("missing command accepted")
	}
	var flag optionalBoolFlag
	if flag.String() != "" {
		t.Fatal("unset optional boolean rendered")
	}
	if err := flag.Set("false"); err != nil || flag.String() != "false" || !flag.optional().Set || flag.optional().Value {
		t.Fatalf("optional boolean = %+v, %v", flag, err)
	}
	if _, err := parsePrivateKey([]byte("not PEM")); err == nil {
		t.Fatal("malformed private key accepted")
	}
	if _, err := parsePublicKey([]byte("not DER")); err == nil {
		t.Fatal("malformed public key accepted")
	}
	if _, err := secureAbsolutePath(""); err == nil {
		t.Fatal("empty path accepted")
	}
	if _, ok := infoSys(nil); ok {
		t.Fatal("nil file info accepted")
	}
}

func commandReceipt(now time.Time) privacyreceipt.Receipt {
	return privacyreceipt.Receipt{
		Contract: privacyreceipt.Contract, RequestID: "7654321-2", Operation: "worker-enable",
		SourceSHA:      strings.Repeat("a", 40),
		ExpectedImage:  "334960985019.dkr.ecr.eu-west-1.amazonaws.com/mycfc-production@sha256:" + strings.Repeat("b", 64),
		EvidenceSHA256: strings.Repeat("0", 64), RequestSHA256: strings.Repeat("c", 64), Result: "SUCCEEDED",
		IssuedAt:  now.Add(-2 * time.Minute).Format("2006-01-02T15:04:05Z"),
		StartedAt: now.Add(-time.Minute).Format("2006-01-02T15:04:05Z"), FinishedAt: now.Format("2006-01-02T15:04:05Z"),
		WorkflowRunID: 7654321, WorkflowRunAttempt: 2,
		Services: privacyreceipt.Services{PrivacyWorker: true, PrivacyRetention: false, PrivacyRestore: true, BackupCleanup: false},
	}
}

func verifyArgs(receiptPath, publicPath, digest string, receipt privacyreceipt.Receipt) []string {
	return []string{
		"verify", "--input", receiptPath, "--public-key", publicPath, "--public-key-spki-sha256", digest,
		"--expected-request-id", receipt.RequestID, "--expected-operation", receipt.Operation,
		"--expected-source-sha", receipt.SourceSHA, "--expected-image", receipt.ExpectedImage,
		"--expected-evidence-sha256", receipt.EvidenceSHA256, "--expected-request-sha256", receipt.RequestSHA256,
		"--expected-result", receipt.Result, "--expected-reason", "none", "--expected-issued-at", receipt.IssuedAt,
		"--expected-workflow-run-id", "7654321", "--expected-workflow-run-attempt", "2", "--max-age", "15m",
		"--expected-privacy-worker-active", "true", "--expected-privacy-retention-active", "false",
		"--expected-privacy-restore-active", "true", "--expected-backup-cleanup-active", "false",
	}
}

func writeMode(t *testing.T, path string, payload []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, payload, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func replaceFlag(args []string, name, value string) []string {
	replaced := append([]string(nil), args...)
	for index := 0; index+1 < len(replaced); index++ {
		if replaced[index] == name {
			replaced[index+1] = value
			return replaced
		}
	}
	return replaced
}
