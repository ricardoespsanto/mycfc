package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/smithy-go"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/privacyrequests"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type logClientFake struct {
	created   int
	lines     []string
	createErr error
	putErr    error
}

type emittedEvent struct {
	name   string
	fields map[string]int64
}

type eventRuntimeFake struct {
	mu     sync.Mutex
	events []emittedEvent
	errors map[string]error
	onEmit func(string)
}

func (f *eventRuntimeFake) emit(_ context.Context, name string, fields map[string]int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, emittedEvent{name: name, fields: fields})
	if f.onEmit != nil {
		f.onEmit(name)
	}
	return f.errors[name]
}

func (f *eventRuntimeFake) count(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, event := range f.events {
		if event.name == name {
			count++
		}
	}
	return count
}

type readinessResult struct {
	ready bool
	err   error
}

type completionRuntimeFake struct {
	readiness      []readinessResult
	readinessCalls int
	pending        []uuid.UUID
	listErr        error
	listHook       func()
	completeResult privacyrequests.CompletionResult
	completeErr    error
	completed      []uuid.UUID
}

func (f *completionRuntimeFake) ActivationReady(context.Context) (bool, error) {
	index := f.readinessCalls
	f.readinessCalls++
	if index < len(f.readiness) {
		return f.readiness[index].ready, f.readiness[index].err
	}
	return true, nil
}

func (f *completionRuntimeFake) ListPending(context.Context, int32) ([]uuid.UUID, error) {
	if f.listHook != nil {
		f.listHook()
	}
	return append([]uuid.UUID(nil), f.pending...), f.listErr
}

func (f *completionRuntimeFake) Complete(_ context.Context, executionID uuid.UUID) (privacyrequests.CompletionResult, error) {
	f.completed = append(f.completed, executionID)
	result := f.completeResult
	result.ExecutionID = executionID
	return result, f.completeErr
}

type executionRuntimeFake struct {
	lease                privacyrequests.ExecutionLease
	claimErr             error
	claimHook            func()
	heartbeatErr         error
	heartbeatHook        func()
	heartbeatCalls       int
	checkpointErr        error
	checkpointOperations []string
	completeResult       dbgen.PrivacyErasureExecution
	completeErr          error
	failResult           dbgen.PrivacyErasureExecution
	failErr              error
	failures             []privacyrequests.ExecutionFailure
}

func (f *executionRuntimeFake) Claim(context.Context) (privacyrequests.ExecutionLease, error) {
	if f.claimHook != nil {
		f.claimHook()
	}
	return f.lease, f.claimErr
}

func (f *executionRuntimeFake) Heartbeat(context.Context, privacyrequests.ExecutionLease) (dbgen.PrivacyErasureJobLease, error) {
	f.heartbeatCalls++
	if f.heartbeatHook != nil {
		f.heartbeatHook()
	}
	return dbgen.PrivacyErasureJobLease{}, f.heartbeatErr
}

func (f *executionRuntimeFake) CompleteCheckpoint(_ context.Context, _ privacyrequests.ExecutionLease, operation, _ string) (dbgen.PrivacyErasureJobCheckpoint, error) {
	f.checkpointOperations = append(f.checkpointOperations, operation)
	return dbgen.PrivacyErasureJobCheckpoint{}, f.checkpointErr
}

func (f *executionRuntimeFake) CompleteJob(context.Context, privacyrequests.ExecutionLease) (dbgen.PrivacyErasureExecution, error) {
	return f.completeResult, f.completeErr
}

func (f *executionRuntimeFake) FailJob(_ context.Context, _ privacyrequests.ExecutionLease, failure privacyrequests.ExecutionFailure) (dbgen.PrivacyErasureExecution, error) {
	f.failures = append(f.failures, failure)
	return f.failResult, f.failErr
}

type uploadCleanupRuntimeFake struct {
	cleaned bool
	err     error
}

func (f *uploadCleanupRuntimeFake) RunOnce(context.Context) (bool, error) { return f.cleaned, f.err }

type objectExecutionRuntimeFake struct {
	calls int
	err   error
}

func (f *objectExecutionRuntimeFake) CompleteCheckpoint(context.Context, privacyrequests.ExecutionLease) (dbgen.PrivacyErasureJobCheckpoint, error) {
	f.calls++
	return dbgen.PrivacyErasureJobCheckpoint{}, f.err
}

type tombstoneRuntimeFake struct {
	exports          int
	exportErr        error
	closureIDs       []uuid.UUID
	exportClosureErr error
}

func (f *tombstoneRuntimeFake) Export(context.Context, privacyrequests.ExecutionLease) error {
	f.exports++
	return f.exportErr
}

func (f *tombstoneRuntimeFake) ExportClosure(_ context.Context, executionID uuid.UUID) error {
	f.closureIDs = append(f.closureIDs, executionID)
	return f.exportClosureErr
}

type statusRowFake struct {
	values [5]int64
	err    error
}

func (r statusRowFake) Scan(destinations ...any) error {
	if r.err != nil {
		return r.err
	}
	for index, destination := range destinations {
		*destination.(*int64) = r.values[index]
	}
	return nil
}

type statusQueryerFake struct{ rows []statusRowFake }

func (f *statusQueryerFake) QueryRow(context.Context, string, ...any) pgx.Row {
	if len(f.rows) == 0 {
		return statusRowFake{err: errors.New("status unavailable")}
	}
	row := f.rows[0]
	f.rows = f.rows[1:]
	return row
}

type runtimeFixture struct {
	runtime    runtime
	execution  *executionRuntimeFake
	upload     *uploadCleanupRuntimeFake
	objects    *objectExecutionRuntimeFake
	tombstones *tombstoneRuntimeFake
	completion *completionRuntimeFake
	events     *eventRuntimeFake
	status     *statusQueryerFake
}

func newRuntimeFixture() runtimeFixture {
	execution := &executionRuntimeFake{claimErr: pgx.ErrNoRows}
	upload := &uploadCleanupRuntimeFake{}
	objects := &objectExecutionRuntimeFake{}
	tombstones := &tombstoneRuntimeFake{}
	completion := &completionRuntimeFake{}
	events := &eventRuntimeFake{errors: map[string]error{}}
	status := &statusQueryerFake{rows: []statusRowFake{{}}}
	return runtimeFixture{
		runtime: runtime{pool: status, execution: execution, uploadCleanup: upload, objects: objects, tombstones: tombstones,
			completion: completion, events: events, pollInterval: time.Millisecond, statusInterval: time.Hour},
		execution: execution, upload: upload, objects: objects, tombstones: tombstones, completion: completion, events: events, status: status,
	}
}

func (f *logClientFake) CreateLogStream(context.Context, *cloudwatchlogs.CreateLogStreamInput, ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.CreateLogStreamOutput, error) {
	f.created++
	return &cloudwatchlogs.CreateLogStreamOutput{}, f.createErr
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
		{"S3_FORCE_PATH_STYLE", "yes"},
		{"PRIVACY_WORKER_POLL_INTERVAL", "invalid"},
		{"PRIVACY_WORKER_LEASE_DURATION", "invalid"},
		{"PRIVACY_WORKER_HEARTBEAT_INTERVAL", "1m"},
		{"PRIVACY_WORKER_LEASE_DURATION", "30s"},
		{"PRIVACY_WORKER_STATUS_INTERVAL", "10m"},
		{"PRIVACY_WORKER_MAX_ATTEMPTS", "0"},
		{"PRIVACY_WORKER_COMPLETION_BATCH", "101"},
		{"AWS_REGION", "invalid region"},
		{"S3_ENDPOINT", "http://minio.invalid"},
		{"PRIVACY_COMPLETION_DETAIL_BASE_URL", "https://mycfcoimbra.com/wrong"},
		{"PRIVACY_PROVIDER_EVIDENCE_KEY_ID", "invalid key id"},
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

func TestActivationReadinessDistinguishesInactiveFromErrors(t *testing.T) {
	if err := activationReadinessError("readiness", true, nil); err != nil {
		t.Fatalf("ready activation returned error: %v", err)
	}

	inactive := activationReadinessError("readiness", false, nil)
	if !errors.Is(inactive, errActivationRequired) {
		t.Fatalf("inactive readiness error = %v", inactive)
	}
	message, status := failureStatus(inactive)
	if message != "privacy_worker_readiness_activation_required" || status != activationRequiredExit {
		t.Fatalf("inactive failure status = (%q, %d)", message, status)
	}

	for name, err := range map[string]error{
		"database error": activationReadinessError("readiness", false, errors.New("database unavailable")),
		"serve inactive": activationReadinessError("serve", false, nil),
		"config error":   errors.New("privacy worker configuration rejected"),
	} {
		t.Run(name, func(t *testing.T) {
			if errors.Is(err, errActivationRequired) {
				t.Fatalf("error was classified as activation required: %v", err)
			}
			message, status := failureStatus(err)
			if message != "privacy_worker_failed" || status != 1 {
				t.Fatalf("failure status = (%q, %d)", message, status)
			}
		})
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

func TestEventSinkHandlesExistingStreamAndRejectsInvalidInputs(t *testing.T) {
	valid := func(client *logClientFake) *eventSink {
		return &eventSink{client: client, group: "/private", stream: "runtime", output: io.Discard, now: time.Now}
	}
	client := &logClientFake{createErr: &smithy.GenericAPIError{Code: "ResourceAlreadyExistsException", Message: "exists"}}
	if err := valid(client).open(t.Context()); err != nil {
		t.Fatalf("existing stream rejected: %v", err)
	}
	for name, sink := range map[string]*eventSink{
		"nil":            nil,
		"missing client": {group: "/private", stream: "runtime", output: io.Discard, now: time.Now},
		"missing group":  {client: &logClientFake{}, stream: "runtime", output: io.Discard, now: time.Now},
	} {
		t.Run(name, func(t *testing.T) {
			if err := sink.open(t.Context()); err == nil {
				t.Fatal("invalid sink accepted")
			}
		})
	}
	for name, createErr := range map[string]error{
		"service error":   &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "denied"},
		"transport error": errors.New("unavailable"),
	} {
		t.Run(name, func(t *testing.T) {
			if err := valid(&logClientFake{createErr: createErr}).open(t.Context()); err == nil {
				t.Fatal("log stream failure accepted")
			}
		})
	}
	sink := valid(&logClientFake{})
	if err := sink.emit(t.Context(), "invalid event", nil); err == nil {
		t.Fatal("invalid event accepted")
	}
	if err := sink.emit(t.Context(), "privacy_worker_heartbeat", map[string]int64{"invalid field": 1}); err == nil {
		t.Fatal("invalid event field accepted")
	}
}

func TestWorkerConfigUtilityBoundaries(t *testing.T) {
	if got, err := boundedDuration("2s", time.Second, time.Second, time.Minute); err != nil || got != 2*time.Second {
		t.Fatalf("boundedDuration valid=(%s,%v)", got, err)
	}
	for _, raw := range []string{"invalid", "500ms", "2m"} {
		if _, err := boundedDuration(raw, time.Second, time.Second, time.Minute); err == nil {
			t.Fatalf("boundedDuration accepted %q", raw)
		}
	}
	if got, err := boundedInt32("7", 5, 1, 10); err != nil || got != 7 {
		t.Fatalf("boundedInt32 valid=(%d,%v)", got, err)
	}
	for _, raw := range []string{"invalid", "0", "11"} {
		if _, err := boundedInt32(raw, 5, 1, 10); err == nil {
			t.Fatalf("boundedInt32 accepted %q", raw)
		}
	}
	for raw, want := range map[string]bool{"": false, "false": false, "true": true} {
		got, err := parseExactBool(raw)
		if err != nil || got != want {
			t.Fatalf("parseExactBool(%q)=(%t,%v)", raw, got, err)
		}
	}
	if _, err := parseExactBool("TRUE"); err == nil {
		t.Fatal("non-exact boolean accepted")
	}
	if valueOrDefault("  value  ", "fallback") != "value" || valueOrDefault("  ", "fallback") != "fallback" {
		t.Fatal("valueOrDefault boundary mismatch")
	}
	if durationIf(true, time.Second) != time.Second || durationIf(false, time.Second) != 0 {
		t.Fatal("durationIf boundary mismatch")
	}
	for _, value := range []string{
		"https://example.invalid/privacidade/conclusao?token=secret",
		"http://example.invalid/privacidade/conclusao",
		"not a URL",
	} {
		if validDetailBaseURL(value) {
			t.Fatalf("invalid detail URL accepted: %q", value)
		}
	}
}

func TestWorkerKeyFileAndKeyringBoundaries(t *testing.T) {
	directory := t.TempDir()
	write := func(name string, payload []byte) string {
		t.Helper()
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, payload, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	validKey := bytes.Repeat([]byte{0x24}, 32)
	encodedKey := base64.StdEncoding.EncodeToString(validKey)
	if got, err := readBase64Key(write("valid.key", []byte("  "+encodedKey+"\n"))); err != nil || !bytes.Equal(got, validKey) {
		t.Fatalf("readBase64Key valid=(%x,%v)", got, err)
	}
	if _, err := readBase64Key(write("invalid.key", []byte("not-base64"))); err == nil {
		t.Fatal("invalid base64 accepted")
	}
	for name, path := range map[string]string{
		"relative":  "relative.key",
		"missing":   filepath.Join(directory, "missing.key"),
		"directory": directory,
		"empty":     write("empty.key", nil),
		"oversized": write("oversized.key", bytes.Repeat([]byte{'x'}, maximumKeyFileBytes+1)),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := readBounded(path); err == nil {
				t.Fatal("invalid bounded file accepted")
			}
		})
	}
	validRingPayload, err := json.Marshal(map[string]string{"provider-v1": encodedKey})
	if err != nil {
		t.Fatal(err)
	}
	ring, err := readKeyring(write("valid-ring.json", validRingPayload))
	if err != nil || !bytes.Equal(ring["provider-v1"], validKey) {
		t.Fatalf("readKeyring valid=(%v,%v)", ring, err)
	}
	invalidRings := map[string]string{
		"malformed": `{`,
		"trailing":  `{} {}`,
		"null":      `null`,
		"bad id":    `{"bad id":"` + encodedKey + `"}`,
		"bad key":   `{"provider-v1":"short"}`,
	}
	tooMany := make(map[string]string, 21)
	for index := 0; index < 21; index++ {
		tooMany[fmt.Sprintf("provider-%d", index)] = encodedKey
	}
	tooManyPayload, err := json.Marshal(tooMany)
	if err != nil {
		t.Fatal(err)
	}
	invalidRings["too many"] = string(tooManyPayload)
	for name, payload := range invalidRings {
		t.Run(name, func(t *testing.T) {
			if _, err := readKeyring(write(strings.ReplaceAll(name, " ", "-")+".json", []byte(payload))); err == nil {
				t.Fatal("invalid keyring accepted")
			}
		})
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

func TestRunRejectsInvalidCommandAndDisabledConfiguration(t *testing.T) {
	if err := run(t.Context(), []string{"unknown"}, func(string) string { return "" }, io.Discard); err == nil {
		t.Fatal("unknown command accepted")
	}
	if err := run(t.Context(), []string{"readiness"}, func(string) string { return "" }, io.Discard); err == nil {
		t.Fatal("disabled worker configuration accepted")
	}
	env := workerTestEnvironment(t, "://invalid")
	if err := run(t.Context(), []string{"readiness"}, func(name string) string { return env[name] }, io.Discard); err == nil {
		t.Fatal("invalid executor database URL accepted")
	}
}

func TestCycleProcessesCheckpointsAndCompletesExecution(t *testing.T) {
	fixture := newRuntimeFixture()
	executionID := uuid.New()
	fixture.execution.claimErr = nil
	fixture.execution.lease = privacyrequests.ExecutionLease{Checkpoints: []dbgen.PrivacyErasureJobCheckpoint{
		{OperationCode: "ALREADY_DONE", Status: "SUCCEEDED"},
		{OperationCode: "AUTH_TOKEN_DELETE", ActionVersion: "v1"},
		{OperationCode: "OBJECT_VERSION_DELETE", ActionVersion: "v1"},
		{OperationCode: "BACKUP_TOMBSTONE_REPLAY", ActionVersion: "v1"},
	}}
	fixture.execution.completeResult = dbgen.PrivacyErasureExecution{ID: executionID, Status: "SUCCEEDED"}
	fixture.completion.completeResult = privacyrequests.CompletionResult{AlreadyComplete: true}

	worked, err := fixture.runtime.cycle(t.Context(), time.Hour, 10)
	if err != nil || !worked {
		t.Fatalf("cycle worked=%t error=%v", worked, err)
	}
	if got := fixture.execution.checkpointOperations; len(got) != 1 || got[0] != "AUTH_TOKEN_DELETE" {
		t.Fatalf("relational checkpoint operations=%v", got)
	}
	if fixture.objects.calls != 1 || fixture.tombstones.exports != 1 || len(fixture.tombstones.closureIDs) != 1 ||
		fixture.tombstones.closureIDs[0] != executionID || len(fixture.completion.completed) != 1 {
		t.Fatalf("objects=%d exports=%d closures=%v completions=%v", fixture.objects.calls, fixture.tombstones.exports,
			fixture.tombstones.closureIDs, fixture.completion.completed)
	}
	if fixture.events.count("privacy_worker_job_claimed") != 1 || fixture.events.count("privacy_worker_job_succeeded") != 1 ||
		fixture.events.count("privacy_worker_completion_succeeded") != 1 {
		t.Fatalf("events=%v", fixture.events.events)
	}
}

func TestCycleHandlesCleanupPendingAndClaimOutcomes(t *testing.T) {
	t.Run("cleanup and pending completion", func(t *testing.T) {
		fixture := newRuntimeFixture()
		pendingID := uuid.New()
		fixture.upload.cleaned = true
		fixture.completion.pending = []uuid.UUID{pendingID}
		worked, err := fixture.runtime.cycle(t.Context(), time.Hour, 10)
		if err != nil || !worked || len(fixture.completion.completed) != 1 || fixture.completion.completed[0] != pendingID {
			t.Fatalf("worked=%t error=%v completed=%v", worked, err, fixture.completion.completed)
		}
		if fixture.events.count("privacy_worker_upload_cleanup_succeeded") != 1 {
			t.Fatalf("events=%v", fixture.events.events)
		}
	})

	t.Run("cleanup failure remains observable and claim can be empty", func(t *testing.T) {
		fixture := newRuntimeFixture()
		fixture.upload.cleaned = true
		fixture.upload.err = errors.New("cleanup unavailable")
		worked, err := fixture.runtime.cycle(t.Context(), time.Hour, 10)
		if err != nil || !worked || fixture.events.count("privacy_worker_upload_cleanup_failed") != 1 {
			t.Fatalf("worked=%t error=%v events=%v", worked, err, fixture.events.events)
		}
	})

	for name, claimErr := range map[string]error{
		"retry exhausted":      privacyrequests.ErrRetryExhausted,
		"executor unavailable": privacyrequests.ErrExecutorUnavailable,
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newRuntimeFixture()
			fixture.execution.claimErr = claimErr
			worked, err := fixture.runtime.cycle(t.Context(), time.Hour, 10)
			if err != nil || !worked || fixture.events.count("privacy_worker_terminal_failure") != 1 {
				t.Fatalf("worked=%t error=%v events=%v", worked, err, fixture.events.events)
			}
		})
	}

	t.Run("list and claim dependency errors fail the cycle", func(t *testing.T) {
		fixture := newRuntimeFixture()
		fixture.completion.listErr = errors.New("completion unavailable")
		if _, err := fixture.runtime.cycle(t.Context(), time.Hour, 10); err == nil {
			t.Fatal("completion list failure was ignored")
		}
		fixture = newRuntimeFixture()
		fixture.execution.claimErr = errors.New("claim unavailable")
		if _, err := fixture.runtime.cycle(t.Context(), time.Hour, 10); err == nil {
			t.Fatal("claim failure was ignored")
		}
	})
}

func TestCycleClassifiesCheckpointFailureAndPreservesLeaseLoss(t *testing.T) {
	tests := []struct {
		name           string
		operation      string
		operationError error
		status         string
		wantCode       privacyrequests.FailureCode
		wantEvent      string
	}{
		{name: "retryable object failure", operation: "OBJECT_VERSION_DELETE", operationError: errors.New("object unavailable"), status: "RETRY_WAIT",
			wantCode: privacyrequests.FailureDependencyUnavailable, wantEvent: "privacy_worker_retry_scheduled"},
		{name: "terminal provider failure", operation: "PROVIDER_RECIPIENT_NOTIFY", status: "TERMINAL_FAILED",
			wantCode: privacyrequests.FailureUnsupportedOperation, wantEvent: "privacy_worker_terminal_failure"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRuntimeFixture()
			fixture.execution.claimErr = nil
			fixture.execution.lease = privacyrequests.ExecutionLease{Checkpoints: []dbgen.PrivacyErasureJobCheckpoint{{OperationCode: test.operation, ActionVersion: "v1"}}}
			fixture.execution.failResult.Status = test.status
			fixture.objects.err = test.operationError
			worked, err := fixture.runtime.cycle(t.Context(), time.Hour, 10)
			if err != nil || !worked || len(fixture.execution.failures) != 1 || fixture.execution.failures[0].Code != test.wantCode ||
				fixture.events.count(test.wantEvent) != 1 {
				t.Fatalf("worked=%t error=%v failures=%v events=%v", worked, err, fixture.execution.failures, fixture.events.events)
			}
		})
	}

	fixture := newRuntimeFixture()
	fixture.execution.claimErr = nil
	fixture.execution.lease = privacyrequests.ExecutionLease{Checkpoints: []dbgen.PrivacyErasureJobCheckpoint{{OperationCode: "AUTH_TOKEN_DELETE", ActionVersion: "v1"}}}
	fixture.execution.checkpointErr = context.Canceled
	worked, err := fixture.runtime.cycle(t.Context(), time.Hour, 10)
	if !worked || !errors.Is(err, context.Canceled) || len(fixture.execution.failures) != 0 {
		t.Fatalf("worked=%t error=%v failures=%v", worked, err, fixture.execution.failures)
	}
}

func TestCycleFailsClosedWhenActivationChanges(t *testing.T) {
	fixture := newRuntimeFixture()
	fixture.completion.readiness = []readinessResult{{ready: false}}
	worked, err := fixture.runtime.cycle(t.Context(), time.Hour, 10)
	if worked || err == nil || fixture.execution.claimErr == nil {
		t.Fatalf("worked=%t error=%v", worked, err)
	}

	fixture = newRuntimeFixture()
	fixture.completion.pending = []uuid.UUID{uuid.New()}
	fixture.completion.readiness = []readinessResult{{ready: true}, {ready: false}}
	worked, err = fixture.runtime.cycle(t.Context(), time.Hour, 10)
	if !worked || err == nil || len(fixture.completion.completed) != 0 {
		t.Fatalf("worked=%t error=%v completed=%v", worked, err, fixture.completion.completed)
	}
}

func TestCompleteExecutionChecksActivationAtEveryBoundary(t *testing.T) {
	executionID := uuid.New()
	t.Run("already complete succeeds", func(t *testing.T) {
		fixture := newRuntimeFixture()
		fixture.completion.completeResult.AlreadyComplete = true
		if err := fixture.runtime.completeExecution(t.Context(), executionID); err != nil {
			t.Fatal(err)
		}
		if len(fixture.tombstones.closureIDs) != 1 || fixture.events.count("privacy_worker_completion_succeeded") != 1 ||
			fixture.events.events[len(fixture.events.events)-1].fields["already_complete"] != 1 {
			t.Fatalf("closures=%v events=%v", fixture.tombstones.closureIDs, fixture.events.events)
		}
	})

	tests := []struct {
		name        string
		readiness   []readinessResult
		exportErr   error
		completeErr error
	}{
		{name: "inactive before export", readiness: []readinessResult{{ready: false}}},
		{name: "closure export fails", exportErr: errors.New("ledger unavailable")},
		{name: "inactive before delivery", readiness: []readinessResult{{ready: true}, {ready: false}}},
		{name: "completion fails", completeErr: errors.New("completion unavailable")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRuntimeFixture()
			fixture.completion.readiness = test.readiness
			fixture.completion.completeErr = test.completeErr
			fixture.tombstones.exportClosureErr = test.exportErr
			if err := fixture.runtime.completeExecution(t.Context(), executionID); err == nil {
				t.Fatal("completion boundary failure was ignored")
			}
		})
	}
}

func TestWithHeartbeatReturnsOperationCancellationAndLeaseLoss(t *testing.T) {
	t.Run("operation result", func(t *testing.T) {
		fixture := newRuntimeFixture()
		want := errors.New("operation result")
		if err := fixture.runtime.withHeartbeat(t.Context(), privacyrequests.ExecutionLease{}, time.Hour, func(context.Context) error { return want }); !errors.Is(err, want) {
			t.Fatalf("error=%v", err)
		}
	})

	t.Run("parent cancellation", func(t *testing.T) {
		fixture := newRuntimeFixture()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := fixture.runtime.withHeartbeat(ctx, privacyrequests.ExecutionLease{}, time.Hour, func(operationCtx context.Context) error {
			<-operationCtx.Done()
			return operationCtx.Err()
		}); !errors.Is(err, context.Canceled) {
			t.Fatalf("error=%v", err)
		}
	})

	t.Run("heartbeat failure loses lease and cancels operation", func(t *testing.T) {
		fixture := newRuntimeFixture()
		fixture.execution.heartbeatErr = errors.New("lease unavailable")
		cancelled := make(chan struct{})
		err := fixture.runtime.withHeartbeat(t.Context(), privacyrequests.ExecutionLease{}, time.Millisecond, func(operationCtx context.Context) error {
			<-operationCtx.Done()
			close(cancelled)
			return operationCtx.Err()
		})
		if !errors.Is(err, privacyrequests.ErrLeaseLost) || fixture.execution.heartbeatCalls == 0 {
			t.Fatalf("error=%v heartbeats=%d", err, fixture.execution.heartbeatCalls)
		}
		select {
		case <-cancelled:
		case <-time.After(time.Second):
			t.Fatal("operation was not cancelled after heartbeat failure")
		}
	})
}

func TestEmitStatusReportsAggregateBreaches(t *testing.T) {
	fixture := newRuntimeFixture()
	fixture.status.rows = []statusRowFake{{values: [5]int64{1, 2, 3, 4, 5}}}
	if err := fixture.runtime.emitStatus(t.Context()); err != nil {
		t.Fatal(err)
	}
	if fixture.events.count("privacy_worker_heartbeat") != 1 || fixture.events.count("privacy_worker_aged_nonterminal_breach") != 1 ||
		fixture.events.count("privacy_worker_terminal_failure") != 1 {
		t.Fatalf("events=%v", fixture.events.events)
	}

	fixture = newRuntimeFixture()
	fixture.status.rows = []statusRowFake{{err: errors.New("database unavailable")}}
	if err := fixture.runtime.emitStatus(t.Context()); err == nil {
		t.Fatal("status query failure was ignored")
	}

	fixture = newRuntimeFixture()
	fixture.events.errors["privacy_worker_heartbeat"] = errors.New("log unavailable")
	if err := fixture.runtime.emitStatus(t.Context()); err == nil {
		t.Fatal("heartbeat delivery failure was ignored")
	}
}

func TestServeHandlesInactiveFailureAndGracefulStop(t *testing.T) {
	t.Run("start event failure", func(t *testing.T) {
		fixture := newRuntimeFixture()
		fixture.events.errors["privacy_worker_started"] = errors.New("log unavailable")
		if err := fixture.runtime.serve(t.Context(), time.Hour, 10); err == nil {
			t.Fatal("start event failure was ignored")
		}
	})

	t.Run("initial status failure", func(t *testing.T) {
		fixture := newRuntimeFixture()
		fixture.status.rows = []statusRowFake{{err: errors.New("database unavailable")}}
		if err := fixture.runtime.serve(t.Context(), time.Hour, 10); err == nil {
			t.Fatal("initial status failure was ignored")
		}
	})

	t.Run("activation becomes inactive", func(t *testing.T) {
		fixture := newRuntimeFixture()
		fixture.completion.readiness = []readinessResult{{ready: false}}
		if err := fixture.runtime.serve(t.Context(), time.Hour, 10); err != nil || fixture.events.count("privacy_worker_activation_not_ready") != 1 {
			t.Fatalf("error=%v events=%v", err, fixture.events.events)
		}
	})

	t.Run("activation query fails", func(t *testing.T) {
		fixture := newRuntimeFixture()
		fixture.completion.readiness = []readinessResult{{err: errors.New("database unavailable")}}
		if err := fixture.runtime.serve(t.Context(), time.Hour, 10); err == nil {
			t.Fatal("activation query failure was ignored")
		}
	})

	t.Run("cancellation emits stopped", func(t *testing.T) {
		fixture := newRuntimeFixture()
		ctx, cancel := context.WithCancel(t.Context())
		fixture.execution.claimHook = cancel
		if err := fixture.runtime.serve(ctx, time.Hour, 10); err != nil || fixture.events.count("privacy_worker_stopped") != 1 {
			t.Fatalf("error=%v events=%v", err, fixture.events.events)
		}
	})

	t.Run("cycle failure is logged before stop", func(t *testing.T) {
		fixture := newRuntimeFixture()
		ctx, cancel := context.WithCancel(t.Context())
		fixture.completion.listErr = errors.New("completion unavailable")
		fixture.completion.listHook = cancel
		if err := fixture.runtime.serve(ctx, time.Hour, 10); err != nil || fixture.events.count("privacy_worker_cycle_failed") != 1 ||
			fixture.events.count("privacy_worker_stopped") != 1 {
			t.Fatalf("error=%v events=%v", err, fixture.events.events)
		}
	})
}

func workerTestEnvironment(t *testing.T, databaseURL string) map[string]string {
	t.Helper()
	directory := t.TempDir()
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
	keyPath := func(name string) string {
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, []byte(key), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	keyringPath := filepath.Join(directory, "provider-credential-digest-keys.json")
	if err := os.WriteFile(keyringPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	return map[string]string{
		"PRIVACY_WORKER_ENABLED":                       "true",
		"PRIVACY_COMPLETION_ENABLED":                   "true",
		"PRIVACY_EXECUTOR_DATABASE_URL":                databaseURL,
		"AWS_REGION":                                   "eu-west-1",
		"S3_BUCKET_NAME":                               "integration-private-media",
		"PRIVACY_TOMBSTONE_BROKER_FUNCTION_NAME":       "integration-privacy-ledger",
		"PRIVACY_WORKER_LOG_GROUP":                     "/mycfc/integration/privacy-worker",
		"PRIVACY_COMPLETION_DETAIL_BASE_URL":           "https://example.invalid/privacidade/conclusao",
		"PRIVACY_UPLOAD_EVIDENCE_KEY_ID":               "upload-evidence-v1",
		"PRIVACY_OBJECT_EVIDENCE_KEY_ID":               "object-evidence-v1",
		"PRIVACY_TOMBSTONE_ENCRYPTION_KEY_ID":          "tombstone-v1",
		"PRIVACY_TOMBSTONE_LOCATOR_KEY_ID":             "locator-v1",
		"PRIVACY_PROVIDER_EVIDENCE_KEY_ID":             "provider-evidence-v1",
		"PRIVACY_UPLOAD_PRIVATE_KEY_FILE":              keyPath("upload-private.key"),
		"PRIVACY_UPLOAD_EVIDENCE_KEY_FILE":             keyPath("upload-evidence.key"),
		"PRIVACY_OBJECT_TARGET_PRIVATE_KEY_FILE":       keyPath("object-private.key"),
		"PRIVACY_OBJECT_EVIDENCE_KEY_FILE":             keyPath("object-evidence.key"),
		"PRIVACY_TOMBSTONE_PUBLIC_KEY_FILE":            keyPath("tombstone-public.key"),
		"PRIVACY_TOMBSTONE_LOCATOR_KEY_FILE":           keyPath("tombstone-locator.key"),
		"PRIVACY_PROVIDER_TARGET_PRIVATE_KEY_FILE":     keyPath("provider-private.key"),
		"PRIVACY_PROVIDER_EVIDENCE_KEY_FILE":           keyPath("provider-evidence.key"),
		"PRIVACY_COMPLETION_DELIVERY_KEY_FILE":         keyPath("completion-delivery.key"),
		"PRIVACY_PROVIDER_CREDENTIAL_DIGEST_KEYS_FILE": keyringPath,
	}
}
