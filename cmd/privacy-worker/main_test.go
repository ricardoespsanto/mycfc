package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/cfcoimbra/mycfc/internal/privacyrequests"
)

type logClientFake struct {
	created int
	lines   []string
	putErr  error
}

func (f *logClientFake) CreateLogStream(context.Context, *cloudwatchlogs.CreateLogStreamInput, ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.CreateLogStreamOutput, error) {
	f.created++
	return &cloudwatchlogs.CreateLogStreamOutput{}, nil
}

func (f *logClientFake) PutLogEvents(_ context.Context, input *cloudwatchlogs.PutLogEventsInput, _ ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.PutLogEventsOutput, error) {
	for _, event := range input.LogEvents {
		f.lines = append(f.lines, *event.Message)
	}
	return &cloudwatchlogs.PutLogEventsOutput{}, f.putErr
}

func TestLoadConfigFailsClosedAndRequiresEveryMountedKey(t *testing.T) {
	directory := t.TempDir()
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	for _, name := range []string{"upload-private.key", "upload-evidence.key", "object-target-private.key", "object-evidence.key", "tombstone-public.key", "tombstone-locator.key", "provider-target-private.key", "provider-evidence.key", "completion-delivery.key"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(key), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	keyring := filepath.Join(directory, "provider-credential-digest-keys.json")
	if err := os.WriteFile(keyring, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"PRIVACY_WORKER_ENABLED": "true", "PRIVACY_COMPLETION_ENABLED": "true", "PRIVACY_EXECUTOR_DATABASE_URL": "postgres://private", "AWS_REGION": "eu-west-1",
		"S3_BUCKET_NAME": "private-media", "PRIVACY_TOMBSTONE_BROKER_FUNCTION_NAME": "privacy-broker",
		"PRIVACY_WORKER_LOG_GROUP": "/mycfc/production/privacy-worker", "PRIVACY_COMPLETION_DETAIL_BASE_URL": "https://mycfcoimbra.com/privacidade/conclusao",
		"PRIVACY_UPLOAD_EVIDENCE_KEY_ID": "upload-evidence-v1", "PRIVACY_OBJECT_EVIDENCE_KEY_ID": "object-evidence-v1",
		"PRIVACY_TOMBSTONE_ENCRYPTION_KEY_ID": "tombstone-v2", "PRIVACY_TOMBSTONE_LOCATOR_KEY_ID": "locator-v2",
		"PRIVACY_PROVIDER_EVIDENCE_KEY_ID":             "provider-evidence-v1",
		"PRIVACY_UPLOAD_PRIVATE_KEY_FILE":              filepath.Join(directory, "upload-private.key"),
		"PRIVACY_UPLOAD_EVIDENCE_KEY_FILE":             filepath.Join(directory, "upload-evidence.key"),
		"PRIVACY_OBJECT_TARGET_PRIVATE_KEY_FILE":       filepath.Join(directory, "object-target-private.key"),
		"PRIVACY_OBJECT_EVIDENCE_KEY_FILE":             filepath.Join(directory, "object-evidence.key"),
		"PRIVACY_TOMBSTONE_PUBLIC_KEY_FILE":            filepath.Join(directory, "tombstone-public.key"),
		"PRIVACY_TOMBSTONE_LOCATOR_KEY_FILE":           filepath.Join(directory, "tombstone-locator.key"),
		"PRIVACY_PROVIDER_TARGET_PRIVATE_KEY_FILE":     filepath.Join(directory, "provider-target-private.key"),
		"PRIVACY_PROVIDER_EVIDENCE_KEY_FILE":           filepath.Join(directory, "provider-evidence.key"),
		"PRIVACY_COMPLETION_DELIVERY_KEY_FILE":         filepath.Join(directory, "completion-delivery.key"),
		"PRIVACY_PROVIDER_CREDENTIAL_DIGEST_KEYS_FILE": keyring,
	}
	getenv := func(key string) string { return env[key] }
	if _, err := loadConfig(getenv); err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	for _, mutation := range []struct{ key, value string }{
		{"PRIVACY_WORKER_ENABLED", "false"},
		{"PRIVACY_COMPLETION_ENABLED", "false"},
		{"PRIVACY_WORKER_HEARTBEAT_INTERVAL", "1m"},
		{"PRIVACY_WORKER_LEASE_DURATION", "30s"},
		{"PRIVACY_WORKER_STATUS_INTERVAL", "10m"},
		{"PRIVACY_COMPLETION_DETAIL_BASE_URL", "https://mycfcoimbra.com/wrong"},
		{"PRIVACY_OBJECT_TARGET_PRIVATE_KEY_FILE", filepath.Join(directory, "missing")},
	} {
		original := env[mutation.key]
		env[mutation.key] = mutation.value
		if _, err := loadConfig(getenv); err == nil {
			t.Fatalf("loadConfig accepted %s=%q", mutation.key, mutation.value)
		}
		env[mutation.key] = original
	}
}

func TestEventSinkWritesSortedAggregateEvidenceAndRejectsDeliveryFailure(t *testing.T) {
	client := &logClientFake{}
	var output bytes.Buffer
	sink := &eventSink{client: client, group: "/private", stream: "runtime", output: &output, now: func() time.Time { return time.Unix(100, 0) }}
	if err := sink.open(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := sink.emit(context.Background(), "privacy_worker_heartbeat", map[string]int64{"terminal": 2, "pending": 1}); err != nil {
		t.Fatal(err)
	}
	want := "event=privacy_worker_heartbeat pending=1 terminal=2"
	if client.created != 1 || len(client.lines) != 1 || client.lines[0] != want || strings.TrimSpace(output.String()) != want {
		t.Fatalf("created=%d lines=%v output=%q", client.created, client.lines, output.String())
	}
	client.putErr = errors.New("secret upstream response")
	if err := sink.emit(context.Background(), "privacy_worker_heartbeat", nil); err == nil || strings.Contains(output.String(), "secret upstream response") {
		t.Fatalf("error=%v output=%q", err, output.String())
	}
}

func TestFailureClassificationKeepsProviderRegistryClosed(t *testing.T) {
	failure := classifyFailure("PROVIDER_RECIPIENT_NOTIFY", privacyrequests.ErrProviderRegistryUnavailable)
	if failure.Classification != privacyrequests.FailureTerminal || failure.Code != privacyrequests.FailureUnsupportedOperation {
		t.Fatalf("failure=%+v", failure)
	}
	retry := classifyFailure("OBJECT_VERSION_DELETE", errors.New("opaque dependency"))
	if retry.Classification != privacyrequests.FailureRetryable || retry.Code != privacyrequests.FailureDependencyUnavailable {
		t.Fatalf("retry=%+v", retry)
	}
}

func TestStepEventsExposeOnlyFixedPolicyOperationCodes(t *testing.T) {
	if got := stepEvent("OBJECT_VERSION_DELETE", "started"); got != "privacy_worker_step_object_version_delete_started" {
		t.Fatalf("stepEvent=%q", got)
	}
	if got := operationMetricName("BACKUP_TOMBSTONE_REPLAY"); got != "operation_backup_tombstone_replay" || !safeIdentifier.MatchString(got) {
		t.Fatalf("operationMetricName=%q", got)
	}
}
