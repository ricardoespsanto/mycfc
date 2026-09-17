package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cfcoimbra/mycfc/internal/privacyreceipt"
)

const maximumKeyBytes = 8 << 10

var now = time.Now

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "privacy_operation_receipt_failed")
		os.Exit(1)
	}
}

func run(args []string, output io.Writer) error {
	if len(args) == 0 {
		return errors.New("receipt command required")
	}
	switch args[0] {
	case "sign":
		return runSign(args[1:], output)
	case "verify":
		return runVerify(args[1:], output)
	default:
		return errors.New("receipt command rejected")
	}
}

func runSign(args []string, output io.Writer) error {
	if os.Geteuid() != 0 {
		return errors.New("receipt signing requires root")
	}
	set := flag.NewFlagSet("sign", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	inputPath := set.String("input", "", "")
	outputPath := set.String("output", "", "")
	privateKeyPath := set.String("private-key", "", "")
	if set.Parse(args) != nil || set.NArg() != 0 || *inputPath == "" || *outputPath == "" || *privateKeyPath == "" {
		return errors.New("receipt signing arguments rejected")
	}
	input, err := readProtected(*inputPath, privacyreceipt.MaximumReceiptBytes, filePolicy{
		owners: map[uint32]bool{0: true}, exactModes: map[os.FileMode]bool{0o400: true, 0o600: true},
	})
	if err != nil {
		return err
	}
	privateBytes, err := readProtected(*privateKeyPath, maximumKeyBytes, filePolicy{
		owners: map[uint32]bool{0: true}, exactModes: map[os.FileMode]bool{0o400: true, 0o600: true}, rootGroup: true,
	})
	if err != nil {
		return err
	}
	defer clear(privateBytes)
	privateKey, err := parsePrivateKey(privateBytes)
	if err != nil {
		return err
	}
	defer clear(privateKey)
	signed, err := privacyreceipt.SignCanonical(input, privateKey)
	if err != nil {
		return err
	}
	if err = writeExclusiveRoot(*outputPath, signed); err != nil {
		return err
	}
	_, err = io.WriteString(output, "receipt_signed\n")
	return err
}

func runVerify(args []string, output io.Writer) error {
	set := flag.NewFlagSet("verify", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	inputPath := set.String("input", "", "")
	publicKeyPath := set.String("public-key", "", "")
	pinnedDigest := set.String("public-key-spki-sha256", "", "")
	expectedRequestID := set.String("expected-request-id", "", "")
	expectedOperation := set.String("expected-operation", "", "")
	expectedSourceSHA := set.String("expected-source-sha", "", "")
	expectedImage := set.String("expected-image", "", "")
	expectedEvidenceSHA := set.String("expected-evidence-sha256", "", "")
	expectedRequestSHA := set.String("expected-request-sha256", "", "")
	expectedResult := set.String("expected-result", "", "")
	expectedReason := set.String("expected-reason", "", "")
	expectedIssuedAt := set.String("expected-issued-at", "", "")
	expectedRunID := set.Uint64("expected-workflow-run-id", 0, "")
	expectedRunAttempt := set.Uint64("expected-workflow-run-attempt", 0, "")
	maximumAgeText := set.String("max-age", "", "")
	var worker, retention, restore, cleanup optionalBoolFlag
	set.Var(&worker, "expected-privacy-worker-active", "")
	set.Var(&retention, "expected-privacy-retention-active", "")
	set.Var(&restore, "expected-privacy-restore-active", "")
	set.Var(&cleanup, "expected-backup-cleanup-active", "")
	if set.Parse(args) != nil || set.NArg() != 0 || *inputPath == "" || *publicKeyPath == "" || *pinnedDigest == "" ||
		*expectedRequestID == "" || *expectedOperation == "" || *expectedSourceSHA == "" || *expectedImage == "" ||
		*expectedEvidenceSHA == "" || *expectedRequestSHA == "" || *expectedResult == "" || *expectedReason == "" ||
		*expectedIssuedAt == "" || *expectedRunID == 0 || *expectedRunAttempt == 0 || *maximumAgeText == "" || *expectedRunAttempt > uint64(^uint32(0)) {
		return errors.New("receipt verification arguments rejected")
	}
	maximumAge, err := time.ParseDuration(*maximumAgeText)
	if err != nil || maximumAge <= 0 || maximumAge > privacyreceipt.MaximumVerificationAge {
		return errors.New("receipt maximum age rejected")
	}
	uid := uint32(os.Geteuid())
	input, err := readProtected(*inputPath, privacyreceipt.MaximumReceiptBytes, filePolicy{
		owners: map[uint32]bool{uid: true}, exactModes: map[os.FileMode]bool{0o400: true, 0o600: true},
	})
	if err != nil {
		return err
	}
	publicBytes, err := readProtected(*publicKeyPath, maximumKeyBytes, filePolicy{
		owners: map[uint32]bool{0: true, uid: true}, publicReadOnly: true,
	})
	if err != nil {
		return err
	}
	publicKey, err := parsePublicKey(publicBytes)
	if err != nil {
		return err
	}
	expectation := privacyreceipt.Expectation{
		RequestID: *expectedRequestID, Operation: *expectedOperation, SourceSHA: *expectedSourceSHA,
		ExpectedImage: *expectedImage, EvidenceSHA256: *expectedEvidenceSHA, RequestSHA256: *expectedRequestSHA,
		Result: *expectedResult, ReasonSet: true, IssuedAt: *expectedIssuedAt, WorkflowRunID: *expectedRunID,
		WorkflowRunAttempt: uint32(*expectedRunAttempt), MaximumAge: maximumAge,
	}
	if *expectedReason != "none" {
		reason := *expectedReason
		expectation.Reason = &reason
	}
	expectation.Services.PrivacyWorker = worker.optional()
	expectation.Services.PrivacyRetention = retention.optional()
	expectation.Services.PrivacyRestore = restore.optional()
	expectation.Services.BackupCleanup = cleanup.optional()
	if _, err = privacyreceipt.VerifyCanonical(input, publicKey, *pinnedDigest, expectation, now().UTC()); err != nil {
		return err
	}
	_, err = io.WriteString(output, "receipt_verified\n")
	return err
}

type optionalBoolFlag struct {
	set, value bool
}

func (f *optionalBoolFlag) String() string {
	if !f.set {
		return ""
	}
	return strconv.FormatBool(f.value)
}

func (f *optionalBoolFlag) Set(value string) error {
	parsed, err := strconv.ParseBool(value)
	if err != nil || (value != "true" && value != "false") {
		return errors.New("expected service state rejected")
	}
	f.set, f.value = true, parsed
	return nil
}

func (f optionalBoolFlag) optional() privacyreceipt.OptionalBool {
	return privacyreceipt.OptionalBool{Set: f.set, Value: f.value}
}

func parsePrivateKey(payload []byte) (ed25519.PrivateKey, error) {
	der, err := decodePEM(payload, "PRIVATE KEY")
	if err != nil {
		return nil, errors.New("receipt private key rejected")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	privateKey, ok := parsed.(ed25519.PrivateKey)
	if err != nil || !ok || len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("receipt private key rejected")
	}
	canonical, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil || !bytes.Equal(der, canonical) {
		return nil, errors.New("receipt private key rejected")
	}
	return privateKey, nil
}

func parsePublicKey(payload []byte) (ed25519.PublicKey, error) {
	der := payload
	if block, rest := pem.Decode(payload); block != nil {
		if block.Type != "PUBLIC KEY" || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 {
			return nil, errors.New("receipt public key rejected")
		}
		der = block.Bytes
	}
	parsed, err := x509.ParsePKIXPublicKey(der)
	publicKey, ok := parsed.(ed25519.PublicKey)
	if err != nil || !ok || len(publicKey) != ed25519.PublicKeySize {
		return nil, errors.New("receipt public key rejected")
	}
	canonical, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil || !bytes.Equal(der, canonical) {
		return nil, errors.New("receipt public key rejected")
	}
	return publicKey, nil
}

func decodePEM(payload []byte, kind string) ([]byte, error) {
	block, rest := pem.Decode(payload)
	if block == nil || block.Type != kind || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("PEM rejected")
	}
	return block.Bytes, nil
}

type filePolicy struct {
	owners         map[uint32]bool
	exactModes     map[os.FileMode]bool
	publicReadOnly bool
	rootGroup      bool
}

func readProtected(path string, maximum int64, policy filePolicy) ([]byte, error) {
	abs, err := secureAbsolutePath(path)
	if err != nil || maximum <= 0 {
		return nil, errors.New("receipt input rejected")
	}
	before, err := os.Lstat(abs)
	if err != nil || !acceptableFile(before, policy) {
		return nil, errors.New("receipt input rejected")
	}
	file, err := os.OpenFile(abs, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, errors.New("receipt input unavailable")
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || !acceptableFile(after, policy) {
		return nil, errors.New("receipt input rejected")
	}
	payload, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || len(payload) == 0 || int64(len(payload)) > maximum {
		return nil, errors.New("receipt input rejected")
	}
	final, err := file.Stat()
	beforeStat, beforeOK := after.Sys().(*syscall.Stat_t)
	finalStat, finalOK := final.Sys().(*syscall.Stat_t)
	if err != nil || !beforeOK || !finalOK || !os.SameFile(after, final) || after.Size() != final.Size() ||
		after.Mode() != final.Mode() || !after.ModTime().Equal(final.ModTime()) || beforeStat.Ctim != finalStat.Ctim {
		return nil, errors.New("receipt input changed while reading")
	}
	return payload, nil
}

func acceptableFile(info os.FileInfo, policy filePolicy) bool {
	if !info.Mode().IsRegular() {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !policy.owners[stat.Uid] || stat.Nlink != 1 || (policy.rootGroup && stat.Gid != 0) {
		return false
	}
	permissions := info.Mode().Perm()
	if policy.publicReadOnly {
		return permissions&0o022 == 0 && permissions&0o111 == 0
	}
	return policy.exactModes[permissions]
}

func secureAbsolutePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" || strings.ContainsRune(path, '\x00') {
		return "", errors.New("path rejected")
	}
	abs, err := filepath.Abs(path)
	if err != nil || filepath.Clean(abs) != abs {
		return "", errors.New("path rejected")
	}
	parent := filepath.Dir(abs)
	for current := parent; ; current = filepath.Dir(current) {
		info, statErr := os.Lstat(current)
		if statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("path rejected")
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return "", errors.New("path rejected")
		}
		writable := info.Mode().Perm()&0o022 != 0
		stickyRoot := stat.Uid == 0 && info.Mode()&os.ModeSticky != 0
		if (writable && !stickyRoot) || (stat.Uid != 0 && stat.Uid != uint32(os.Geteuid())) {
			return "", errors.New("path rejected")
		}
		if current == string(filepath.Separator) {
			break
		}
	}
	return abs, nil
}

func writeExclusiveRoot(path string, payload []byte) error {
	abs, err := secureAbsolutePath(path)
	if err != nil || len(payload) == 0 {
		return errors.New("receipt output rejected")
	}
	parent := filepath.Dir(abs)
	info, err := os.Lstat(parent)
	stat, ok := infoSys(info)
	if err != nil || !ok || !info.IsDir() || info.Mode().Perm() != 0o700 || stat.Uid != 0 || stat.Gid != 0 {
		return errors.New("receipt output directory rejected")
	}
	file, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return errors.New("receipt output unavailable")
	}
	remove := true
	defer func() {
		_ = file.Close()
		if remove {
			_ = os.Remove(abs)
		}
	}()
	if _, err = file.Write(payload); err != nil || file.Chmod(0o600) != nil || file.Sync() != nil || file.Close() != nil {
		return errors.New("receipt output unavailable")
	}
	created, err := os.Lstat(abs)
	createdStat, createdOK := infoSys(created)
	if err != nil || !createdOK || !created.Mode().IsRegular() || created.Mode().Perm() != 0o600 || createdStat.Uid != 0 || createdStat.Gid != 0 || createdStat.Nlink != 1 {
		return errors.New("receipt output rejected")
	}
	directory, err := os.Open(parent)
	if err != nil {
		return errors.New("receipt output unavailable")
	}
	err = directory.Sync()
	_ = directory.Close()
	if err != nil {
		return errors.New("receipt output unavailable")
	}
	remove = false
	return nil
}

func infoSys(info os.FileInfo) (*syscall.Stat_t, bool) {
	if info == nil {
		return nil, false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return stat, ok
}
