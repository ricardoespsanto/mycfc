package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/cfcoimbra/mycfc/internal/storage"
)

type purgeRunnerFake struct {
	evidence storage.LegacyMediaPurgeEvidence
	err      error
	request  storage.LegacyMediaPurgeRequest
}

func (f *purgeRunnerFake) Run(_ context.Context, request storage.LegacyMediaPurgeRequest) (storage.LegacyMediaPurgeEvidence, error) {
	f.request = request
	return f.evidence, f.err
}

type purgeErrorWriter struct{}

func (purgeErrorWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestLegacyMediaPurgeCommandDefaultsToDryRunAndRequiresTypedExecuteConfirmation(t *testing.T) {
	request, err := parseRequest(nil)
	if err != nil || request.execute || request.confirmation != "" {
		t.Fatalf("default request=%+v err=%v", request, err)
	}
	digest := strings.Repeat("a", 64)
	request, err = parseRequest([]string{"--execute", "--inventory-digest", digest, "--confirm", storage.LegacyMediaPurgeConfirmation})
	if err != nil || !request.execute || request.confirmation != storage.LegacyMediaPurgeConfirmation || request.inventoryDigest != digest {
		t.Fatalf("execute request=%+v err=%v", request, err)
	}
	for _, args := range [][]string{{"--confirm", storage.LegacyMediaPurgeConfirmation}, {"--inventory-digest", digest}, {"unexpected"}, {"--unknown"}} {
		if _, err = parseRequest(args); err == nil {
			t.Fatalf("invalid arguments accepted: %v", args)
		}
	}
}

func TestLegacyMediaPurgeCommandRequiresSeparateEvidenceKey(t *testing.T) {
	if _, err := decodeEvidenceKey("not-base64"); err == nil || !strings.Contains(err.Error(), "EVIDENCE_KEY") {
		t.Fatalf("invalid evidence key error=%v", err)
	}
	raw := []byte(strings.Repeat("e", 32))
	decoded, err := decodeEvidenceKey(base64.StdEncoding.EncodeToString(raw))
	if err != nil || string(decoded) != string(raw) {
		t.Fatalf("decoded=%d err=%v", len(decoded), err)
	}
}

func TestLegacyMediaPurgeEvidenceKeyFileIsProtectedAndExclusive(t *testing.T) {
	path := t.TempDir() + "/evidence.key"
	raw := bytes.Repeat([]byte{0x51}, 32)
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(raw)), 0o600); err != nil {
		t.Fatal(err)
	}
	decoded, err := loadEvidenceKey("", path)
	if err != nil || !bytes.Equal(decoded, raw) {
		t.Fatalf("decoded=%d err=%v", len(decoded), err)
	}
	if _, err = loadEvidenceKey(base64.StdEncoding.EncodeToString(raw), path); err == nil {
		t.Fatal("simultaneous evidence key sources accepted")
	}
	if _, err = loadEvidenceKey("", ""); err == nil {
		t.Fatal("missing evidence key source accepted")
	}
	if err = os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = loadEvidenceKey("", path); err == nil {
		t.Fatal("over-permissive evidence key file accepted")
	}
	link := t.TempDir() + "/evidence-link.key"
	if err = os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err = loadEvidenceKey("", link); err == nil {
		t.Fatal("evidence key symlink accepted")
	}
}

func TestLegacyMediaPurgeRunDispatchesDryRunAndFailsClosed(t *testing.T) {
	originalEnvironment := legacyMediaPurgeEnvironment
	originalLoad := loadLegacyMediaPurgeAWSConfig
	originalNew := newLegacyMediaPurgeRunner
	t.Cleanup(func() {
		legacyMediaPurgeEnvironment = originalEnvironment
		loadLegacyMediaPurgeAWSConfig = originalLoad
		newLegacyMediaPurgeRunner = originalNew
	})
	env := map[string]string{
		"S3_BUCKET_NAME":                      "private-media",
		"LEGACY_MEDIA_PURGE_EVIDENCE_KEY_ID":  "purge-evidence-v1",
		"LEGACY_MEDIA_PURGE_EVIDENCE_KEY_B64": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32)),
		"AWS_REGION":                          "eu-west-1",
	}
	legacyMediaPurgeEnvironment = func(name string) string { return env[name] }
	loadLegacyMediaPurgeAWSConfig = func(context.Context, ...func(*awsconfig.LoadOptions) error) (aws.Config, error) {
		return aws.Config{}, nil
	}
	runner := &purgeRunnerFake{evidence: storage.LegacyMediaPurgeEvidence{Mode: "DRY_RUN"}}
	newLegacyMediaPurgeRunner = func(aws.Config, string, string, []byte) (legacyMediaPurgeRunner, error) { return runner, nil }
	var output bytes.Buffer
	if err := run(t.Context(), nil, &output); err != nil {
		t.Fatal(err)
	}
	if runner.request.Execute || runner.request.Enabled != legacyMediaPurgeExecutionEnabled || !strings.Contains(output.String(), `"mode": "DRY_RUN"`) {
		t.Fatalf("request=%+v output=%q", runner.request, output.String())
	}

	env["LEGACY_MEDIA_PURGE_EVIDENCE_KEY_B64"] = "invalid"
	if err := run(t.Context(), nil, io.Discard); err == nil {
		t.Fatal("invalid evidence key accepted")
	}
	env["LEGACY_MEDIA_PURGE_EVIDENCE_KEY_B64"] = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
	loadLegacyMediaPurgeAWSConfig = func(context.Context, ...func(*awsconfig.LoadOptions) error) (aws.Config, error) {
		return aws.Config{}, errors.New("configuration unavailable")
	}
	if err := run(t.Context(), nil, io.Discard); err == nil {
		t.Fatal("AWS configuration failure accepted")
	}
	loadLegacyMediaPurgeAWSConfig = func(context.Context, ...func(*awsconfig.LoadOptions) error) (aws.Config, error) {
		return aws.Config{}, nil
	}
	newLegacyMediaPurgeRunner = func(aws.Config, string, string, []byte) (legacyMediaPurgeRunner, error) {
		return nil, errors.New("runner rejected")
	}
	if err := run(t.Context(), nil, io.Discard); err == nil {
		t.Fatal("runner construction failure accepted")
	}
	runner.err = errors.New("purge unavailable")
	newLegacyMediaPurgeRunner = func(aws.Config, string, string, []byte) (legacyMediaPurgeRunner, error) { return runner, nil }
	if err := run(t.Context(), nil, io.Discard); err == nil {
		t.Fatal("purge failure accepted")
	}
	runner.err = nil
	if err := run(t.Context(), nil, purgeErrorWriter{}); err == nil {
		t.Fatal("evidence output failure accepted")
	}
	if err := run(t.Context(), []string{"unexpected"}, io.Discard); err == nil {
		t.Fatal("invalid command accepted")
	}
}
