package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cloudwatchtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/cfcoimbra/mycfc/internal/privacyrequests"
	"github.com/cfcoimbra/mycfc/internal/storage"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	defaultPollInterval      = 5 * time.Second
	defaultLeaseDuration     = 2 * time.Minute
	defaultHeartbeatInterval = 20 * time.Second
	defaultStatusInterval    = 5 * time.Minute
	defaultMaxAttempts       = int32(5)
	defaultCompletionBatch   = int32(10)
	maximumKeyFileBytes      = 1 << 20
)

var safeIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,119}$`)

type workerConfig struct {
	databaseURL, region, bucket, s3Endpoint, brokerFunction, logGroup, detailBaseURL string
	forcePathStyle                                                                   bool
	pollInterval, leaseDuration, heartbeatInterval, statusInterval                   time.Duration
	maxAttempts, completionBatch                                                     int32
	uploadPrivate, uploadEvidence, objectPrivate, objectEvidence                     []byte
	tombstonePublic, tombstoneLocator, providerPrivate, providerEvidence             []byte
	completionDelivery                                                               []byte
	uploadEvidenceKeyID, objectEvidenceKeyID, tombstoneEncryptionKeyID               string
	tombstoneLocatorKeyID, providerEvidenceKeyID                                     string
	providerCredentialKeys                                                           map[string][]byte
}

type cloudWatchLogsAPI interface {
	CreateLogStream(context.Context, *cloudwatchlogs.CreateLogStreamInput, ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.CreateLogStreamOutput, error)
	PutLogEvents(context.Context, *cloudwatchlogs.PutLogEventsInput, ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.PutLogEventsOutput, error)
}

type eventSink struct {
	client cloudWatchLogsAPI
	group  string
	stream string
	output io.Writer
	now    func() time.Time
	mu     sync.Mutex
}

type runtime struct {
	pool           *pgxpool.Pool
	execution      privacyrequests.ExecutionWorker
	uploadCleanup  privacyrequests.UploadCleanupWorker
	objects        privacyrequests.ObjectExecutionWorker
	tombstones     privacyrequests.TombstoneExportWorker
	completion     privacyrequests.CompletionWorker
	events         *eventSink
	pollInterval   time.Duration
	statusInterval time.Duration
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Getenv, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "privacy_worker_failed")
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, getenv func(string) string, output io.Writer) error {
	if len(args) != 1 || (args[0] != "serve" && args[0] != "readiness") {
		return errors.New("privacy worker command rejected")
	}
	cfg, err := loadConfig(getenv)
	if err != nil {
		return err
	}
	workerRef := uuid.New()
	poolConfig, err := pgxpool.ParseConfig(cfg.databaseURL)
	if err != nil {
		return errors.New("parse executor database configuration")
	}
	poolConfig.MaxConns = 4
	poolConfig.MinConns = 1
	poolConfig.MaxConnLifetime = 30 * time.Minute
	poolConfig.MaxConnIdleTime = 5 * time.Minute
	poolConfig.HealthCheckPeriod = 30 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return errors.New("open executor database")
	}
	defer pool.Close()
	var currentRole string
	if err = pool.QueryRow(ctx, "SELECT current_user").Scan(&currentRole); err != nil || currentRole != "mycfc_privacy_executor" {
		return errors.New("executor database identity rejected")
	}

	completion := privacyrequests.CompletionWorker{Pool: pool, WorkerRef: workerRef, Key: cfg.completionDelivery, DetailBaseURL: cfg.detailBaseURL}
	ready, err := completion.ActivationReady(ctx)
	if err != nil || !ready {
		return errors.New("evidence-bound privacy activation is not ready")
	}
	if args[0] == "readiness" {
		fmt.Fprintln(output, "privacy_worker_readiness_ready")
		return nil
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.region))
	if err != nil {
		return errors.New("load privacy worker AWS configuration")
	}
	s3Client := s3.NewFromConfig(awsCfg, func(options *s3.Options) {
		options.UsePathStyle = cfg.forcePathStyle
		if cfg.s3Endpoint != "" {
			options.BaseEndpoint = aws.String(cfg.s3Endpoint)
		}
	})
	versionedObjects := storage.NewGuardedS3VersionedStore(s3Client, cfg.bucket, func(checkCtx context.Context) error {
		ready, checkErr := completion.ActivationReady(checkCtx)
		if checkErr != nil || !ready {
			return errors.New("privacy worker activation is not ready")
		}
		return nil
	})
	ledger, err := privacyrequests.NewLambdaTombstoneLedger(lambda.NewFromConfig(awsCfg), cfg.brokerFunction)
	if err != nil {
		return errors.New("configure restore ledger broker")
	}
	protector, err := privacyrequests.NewTombstoneProtector(cfg.tombstoneEncryptionKeyID, cfg.tombstonePublic, cfg.tombstoneLocatorKeyID, cfg.tombstoneLocator)
	if err != nil {
		return errors.New("configure restore tombstone protection")
	}
	events := &eventSink{client: cloudwatchlogs.NewFromConfig(awsCfg), group: cfg.logGroup, stream: "runtime", output: output, now: time.Now}
	if err = events.open(ctx); err != nil {
		return errors.New("open privacy worker log stream")
	}
	r := runtime{
		pool:      pool,
		execution: privacyrequests.ExecutionWorker{Pool: pool, WorkerRef: workerRef, LeaseDuration: cfg.leaseDuration, MaxAttempts: cfg.maxAttempts},
		uploadCleanup: privacyrequests.UploadCleanupWorker{Store: privacyrequests.PostgresUploadCleanupStore{DB: pool}, Objects: versionedObjects,
			WorkerRef: workerRef, PrivateKey: cfg.uploadPrivate, TranscriptKeyID: cfg.uploadEvidenceKeyID, TranscriptKey: cfg.uploadEvidence,
			LeaseDuration: cfg.leaseDuration, MaxAttempts: cfg.maxAttempts},
		objects: privacyrequests.ObjectExecutionWorker{Pool: pool, Objects: versionedObjects, WorkerRef: workerRef, PrivateKey: cfg.objectPrivate,
			TranscriptKeyID: cfg.objectEvidenceKeyID, TranscriptKey: cfg.objectEvidence},
		tombstones:     privacyrequests.TombstoneExportWorker{Store: pool, Ledger: ledger, Protector: protector, WorkerRef: workerRef},
		completion:     completion,
		events:         events,
		pollInterval:   cfg.pollInterval,
		statusInterval: cfg.statusInterval,
	}
	return r.serve(ctx, cfg.heartbeatInterval, cfg.completionBatch)
}

func loadConfig(getenv func(string) string) (workerConfig, error) {
	var cfg workerConfig
	if getenv("PRIVACY_WORKER_ENABLED") != "true" || getenv("PRIVACY_COMPLETION_ENABLED") != "true" {
		return cfg, errors.New("privacy worker is disabled")
	}
	cfg.databaseURL = strings.TrimSpace(getenv("PRIVACY_EXECUTOR_DATABASE_URL"))
	cfg.region = valueOrDefault(getenv("AWS_REGION"), "eu-west-1")
	cfg.bucket = strings.TrimSpace(getenv("S3_BUCKET_NAME"))
	cfg.s3Endpoint = strings.TrimSpace(getenv("S3_ENDPOINT"))
	cfg.brokerFunction = strings.TrimSpace(getenv("PRIVACY_TOMBSTONE_BROKER_FUNCTION_NAME"))
	cfg.logGroup = strings.TrimSpace(getenv("PRIVACY_WORKER_LOG_GROUP"))
	cfg.detailBaseURL = strings.TrimSpace(getenv("PRIVACY_COMPLETION_DETAIL_BASE_URL"))
	var err error
	if cfg.forcePathStyle, err = parseExactBool(getenv("S3_FORCE_PATH_STYLE")); err != nil {
		return workerConfig{}, err
	}
	if cfg.pollInterval, err = boundedDuration(getenv("PRIVACY_WORKER_POLL_INTERVAL"), defaultPollInterval, time.Second, time.Minute); err != nil {
		return workerConfig{}, err
	}
	if cfg.leaseDuration, err = boundedDuration(getenv("PRIVACY_WORKER_LEASE_DURATION"), defaultLeaseDuration, 30*time.Second, 15*time.Minute); err != nil {
		return workerConfig{}, err
	}
	if cfg.heartbeatInterval, err = boundedDuration(getenv("PRIVACY_WORKER_HEARTBEAT_INTERVAL"), defaultHeartbeatInterval, 5*time.Second, 5*time.Minute); err != nil || cfg.heartbeatInterval*2 >= cfg.leaseDuration {
		return workerConfig{}, errors.New("privacy worker heartbeat interval rejected")
	}
	if cfg.statusInterval, err = boundedDuration(getenv("PRIVACY_WORKER_STATUS_INTERVAL"), defaultStatusInterval, time.Minute, 5*time.Minute); err != nil {
		return workerConfig{}, err
	}
	if cfg.maxAttempts, err = boundedInt32(getenv("PRIVACY_WORKER_MAX_ATTEMPTS"), defaultMaxAttempts, 1, 100); err != nil {
		return workerConfig{}, err
	}
	if cfg.completionBatch, err = boundedInt32(getenv("PRIVACY_WORKER_COMPLETION_BATCH"), defaultCompletionBatch, 1, 100); err != nil {
		return workerConfig{}, err
	}
	if cfg.databaseURL == "" || cfg.bucket == "" || !safeIdentifier.MatchString(cfg.region) || !safeIdentifier.MatchString(cfg.brokerFunction) ||
		!strings.HasPrefix(cfg.logGroup, "/") || len(cfg.logGroup) > 512 || strings.ContainsAny(cfg.logGroup, "\r\n") || !validDetailBaseURL(cfg.detailBaseURL) {
		return workerConfig{}, errors.New("privacy worker configuration rejected")
	}
	if cfg.s3Endpoint != "" {
		endpoint, endpointErr := url.Parse(cfg.s3Endpoint)
		if endpointErr != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
			return workerConfig{}, errors.New("privacy worker object endpoint rejected")
		}
	}

	cfg.uploadEvidenceKeyID = strings.TrimSpace(getenv("PRIVACY_UPLOAD_EVIDENCE_KEY_ID"))
	cfg.objectEvidenceKeyID = strings.TrimSpace(getenv("PRIVACY_OBJECT_EVIDENCE_KEY_ID"))
	cfg.tombstoneEncryptionKeyID = strings.TrimSpace(getenv("PRIVACY_TOMBSTONE_ENCRYPTION_KEY_ID"))
	cfg.tombstoneLocatorKeyID = strings.TrimSpace(getenv("PRIVACY_TOMBSTONE_LOCATOR_KEY_ID"))
	cfg.providerEvidenceKeyID = strings.TrimSpace(getenv("PRIVACY_PROVIDER_EVIDENCE_KEY_ID"))
	for _, value := range []string{cfg.uploadEvidenceKeyID, cfg.objectEvidenceKeyID, cfg.tombstoneEncryptionKeyID, cfg.tombstoneLocatorKeyID, cfg.providerEvidenceKeyID} {
		if !safeIdentifier.MatchString(value) {
			return workerConfig{}, errors.New("privacy worker key identifier rejected")
		}
	}
	keyInputs := []struct {
		env, fallback string
		destination   *[]byte
	}{
		{"PRIVACY_UPLOAD_PRIVATE_KEY_FILE", "/run/secrets/mycfc/upload-private.key", &cfg.uploadPrivate},
		{"PRIVACY_UPLOAD_EVIDENCE_KEY_FILE", "/run/secrets/mycfc/upload-evidence.key", &cfg.uploadEvidence},
		{"PRIVACY_OBJECT_TARGET_PRIVATE_KEY_FILE", "/run/secrets/mycfc/object-target-private.key", &cfg.objectPrivate},
		{"PRIVACY_OBJECT_EVIDENCE_KEY_FILE", "/run/secrets/mycfc/object-evidence.key", &cfg.objectEvidence},
		{"PRIVACY_TOMBSTONE_PUBLIC_KEY_FILE", "/run/secrets/mycfc/tombstone-public.key", &cfg.tombstonePublic},
		{"PRIVACY_TOMBSTONE_LOCATOR_KEY_FILE", "/run/secrets/mycfc/tombstone-locator.key", &cfg.tombstoneLocator},
		{"PRIVACY_PROVIDER_TARGET_PRIVATE_KEY_FILE", "/run/secrets/mycfc/provider-target-private.key", &cfg.providerPrivate},
		{"PRIVACY_PROVIDER_EVIDENCE_KEY_FILE", "/run/secrets/mycfc/provider-evidence.key", &cfg.providerEvidence},
		{"PRIVACY_COMPLETION_DELIVERY_KEY_FILE", "/run/secrets/mycfc/completion-delivery.key", &cfg.completionDelivery},
	}
	for _, input := range keyInputs {
		*input.destination, err = readBase64Key(valueOrDefault(getenv(input.env), input.fallback))
		if err != nil {
			return workerConfig{}, err
		}
	}
	if len(cfg.uploadPrivate) != 32 || len(cfg.objectPrivate) != 32 || len(cfg.tombstonePublic) != 32 || len(cfg.tombstoneLocator) != sha256.Size ||
		len(cfg.providerPrivate) != 32 || len(cfg.uploadEvidence) < 32 || len(cfg.objectEvidence) < 32 || len(cfg.providerEvidence) < 32 || len(cfg.completionDelivery) < 32 {
		return workerConfig{}, errors.New("privacy worker key material rejected")
	}
	cfg.providerCredentialKeys, err = readKeyring(valueOrDefault(getenv("PRIVACY_PROVIDER_CREDENTIAL_DIGEST_KEYS_FILE"), "/run/secrets/mycfc/provider-credential-digest-keys.json"))
	if err != nil {
		return workerConfig{}, err
	}
	return cfg, nil
}

func (r runtime) serve(ctx context.Context, heartbeatInterval time.Duration, completionBatch int32) error {
	if err := r.events.emit(ctx, "privacy_worker_started", map[string]int64{"poll_seconds": int64(r.pollInterval / time.Second)}); err != nil {
		return err
	}
	if err := r.emitStatus(ctx); err != nil {
		return err
	}
	statusTicker := time.NewTicker(r.statusInterval)
	defer statusTicker.Stop()
	for {
		ready, err := r.completion.ActivationReady(ctx)
		if err != nil {
			return errors.New("privacy worker activation check failed")
		}
		if !ready {
			_ = r.events.emit(ctx, "privacy_worker_activation_not_ready", nil)
			return nil
		}
		worked, cycleErr := r.cycle(ctx, heartbeatInterval, completionBatch)
		if cycleErr != nil && !errors.Is(cycleErr, context.Canceled) {
			_ = r.events.emit(ctx, "privacy_worker_cycle_failed", nil)
		}
		select {
		case <-ctx.Done():
			stopCtx, stopCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			_ = r.events.emit(stopCtx, "privacy_worker_stopped", nil)
			stopCancel()
			return nil
		case <-statusTicker.C:
			if err := r.emitStatus(ctx); err != nil {
				return err
			}
		case <-time.After(durationIf(!worked || cycleErr != nil, r.pollInterval)):
		}
	}
}

func (r runtime) cycle(ctx context.Context, heartbeatInterval time.Duration, completionBatch int32) (bool, error) {
	worked := false
	if err := r.requireActivationReady(ctx); err != nil {
		return false, err
	}
	if cleaned, err := r.uploadCleanup.RunOnce(ctx); err != nil {
		_ = r.events.emit(ctx, "privacy_worker_upload_cleanup_failed", nil)
		worked = worked || cleaned
	} else if cleaned {
		worked = true
		_ = r.events.emit(ctx, "privacy_worker_upload_cleanup_succeeded", nil)
	}
	pending, err := r.completion.ListPending(ctx, completionBatch)
	if err != nil {
		return worked, err
	}
	for _, executionID := range pending {
		worked = true
		if err = r.requireActivationReady(ctx); err != nil {
			return worked, err
		}
		if err = r.completeExecution(ctx, executionID); err != nil {
			_ = r.events.emit(ctx, "privacy_worker_completion_unavailable", nil)
			return worked, err
		}
	}
	if err = r.requireActivationReady(ctx); err != nil {
		return worked, err
	}
	lease, err := r.execution.Claim(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return worked, nil
	}
	if errors.Is(err, privacyrequests.ErrRetryExhausted) || errors.Is(err, privacyrequests.ErrExecutorUnavailable) {
		_ = r.events.emit(ctx, "privacy_worker_terminal_failure", map[string]int64{"operation_claim_validation": 1})
		return true, nil
	}
	if err != nil {
		return worked, err
	}
	worked = true
	_ = r.events.emit(ctx, "privacy_worker_job_claimed", map[string]int64{"checkpoint_count": int64(len(lease.Checkpoints))})
	for _, checkpoint := range lease.Checkpoints {
		if checkpoint.Status == "SUCCEEDED" {
			continue
		}
		operation := checkpoint.OperationCode
		if err = r.requireActivationReady(ctx); err != nil {
			return worked, err
		}
		_ = r.events.emit(ctx, stepEvent(operation, "started"), nil)
		err = r.withHeartbeat(ctx, lease, heartbeatInterval, func(operationCtx context.Context) error {
			return r.completeCheckpoint(operationCtx, lease, operation, checkpoint.ActionVersion)
		})
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, privacyrequests.ErrLeaseLost) {
				return worked, err
			}
			failure := classifyFailure(operation, err)
			execution, failErr := r.execution.FailJob(ctx, lease, failure)
			if failErr != nil {
				return worked, failErr
			}
			if execution.Status == "TERMINAL_FAILED" {
				_ = r.events.emit(ctx, "privacy_worker_terminal_failure", map[string]int64{operationMetricName(operation): 1})
			} else {
				_ = r.events.emit(ctx, "privacy_worker_retry_scheduled", map[string]int64{operationMetricName(operation): 1})
			}
			return worked, nil
		}
		_ = r.events.emit(ctx, stepEvent(operation, "succeeded"), nil)
	}
	if err = r.requireActivationReady(ctx); err != nil {
		return worked, err
	}
	execution, err := r.execution.CompleteJob(ctx, lease)
	if err != nil {
		return worked, err
	}
	_ = r.events.emit(ctx, "privacy_worker_job_succeeded", nil)
	if execution.Status == "SUCCEEDED" {
		if err = r.completeExecution(ctx, execution.ID); err != nil {
			_ = r.events.emit(ctx, "privacy_worker_completion_unavailable", nil)
			return worked, err
		}
	}
	return worked, nil
}

func (r runtime) completeCheckpoint(ctx context.Context, lease privacyrequests.ExecutionLease, operation, actionVersion string) error {
	// Keep this check adjacent to the external or database mutation. The outer
	// cycle check alone is insufficient because activation can expire or be
	// revoked while the worker is waiting to enter an operation.
	if err := r.requireActivationReady(ctx); err != nil {
		return err
	}
	switch operation {
	case "OBJECT_VERSION_DELETE":
		_, err := r.objects.CompleteCheckpoint(ctx, lease)
		return err
	case "BACKUP_TOMBSTONE_REPLAY":
		return r.tombstones.Export(ctx, lease)
	case "PROVIDER_RECIPIENT_NOTIFY":
		// The production registry remains intentionally empty until factual #109
		// registrations and reviewed adapters are shipped together.
		return privacyrequests.ErrProviderRegistryUnavailable
	default:
		_, err := r.execution.CompleteCheckpoint(ctx, lease, operation, actionVersion)
		return err
	}
}

func (r runtime) completeExecution(ctx context.Context, executionID uuid.UUID) error {
	callCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := r.requireActivationReady(callCtx); err != nil {
		return err
	}
	if err := r.tombstones.ExportClosure(callCtx, executionID); err != nil {
		return err
	}
	if err := r.requireActivationReady(callCtx); err != nil {
		return err
	}
	result, err := r.completion.Complete(callCtx, executionID)
	if err != nil {
		return err
	}
	value := int64(0)
	if result.AlreadyComplete {
		value = 1
	}
	return r.events.emit(callCtx, "privacy_worker_completion_succeeded", map[string]int64{"already_complete": value})
}

func (r runtime) requireActivationReady(ctx context.Context) error {
	ready, err := r.completion.ActivationReady(ctx)
	if err != nil || !ready {
		return errors.New("privacy worker activation is not ready")
	}
	return nil
}

func (r runtime) withHeartbeat(ctx context.Context, lease privacyrequests.ExecutionLease, interval time.Duration, operation func(context.Context) error) error {
	operationCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- operation(operationCtx) }()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case err := <-result:
			return err
		case <-ctx.Done():
			cancel()
			return context.Canceled
		case <-ticker.C:
			heartbeatCtx, heartbeatCancel := context.WithTimeout(ctx, interval/2)
			_, err := r.execution.Heartbeat(heartbeatCtx, lease)
			heartbeatCancel()
			if err != nil {
				cancel()
				return privacyrequests.ErrLeaseLost
			}
		}
	}
}

func (r runtime) emitStatus(ctx context.Context) error {
	var pending, leased, retryable, terminal, aged int64
	err := r.pool.QueryRow(ctx, `SELECT pending,leased,retryable,terminal,aged_nonterminal FROM privacy_worker_status()`).Scan(&pending, &leased, &retryable, &terminal, &aged)
	if err != nil {
		return errors.New("privacy worker status query failed")
	}
	fields := map[string]int64{"pending": pending, "leased": leased, "retryable": retryable, "terminal": terminal, "aged_nonterminal": aged}
	if err = r.events.emit(ctx, "privacy_worker_heartbeat", fields); err != nil {
		return err
	}
	if aged > 0 {
		if err = r.events.emit(ctx, "privacy_worker_aged_nonterminal_breach", map[string]int64{"count": aged}); err != nil {
			return err
		}
	}
	if terminal > 0 {
		return r.events.emit(ctx, "privacy_worker_terminal_failure", map[string]int64{"count": terminal})
	}
	return nil
}

func (s *eventSink) open(ctx context.Context) error {
	if s == nil || s.client == nil || s.group == "" || s.stream == "" || s.output == nil || s.now == nil {
		return errors.New("privacy worker event sink rejected")
	}
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := s.client.CreateLogStream(callCtx, &cloudwatchlogs.CreateLogStreamInput{LogGroupName: &s.group, LogStreamName: &s.stream})
	var apiErr smithy.APIError
	if err != nil && (!errors.As(err, &apiErr) || apiErr.ErrorCode() != "ResourceAlreadyExistsException") {
		return errors.New("privacy worker log stream unavailable")
	}
	return nil
}

func (s *eventSink) emit(ctx context.Context, event string, fields map[string]int64) error {
	if s == nil || !safeIdentifier.MatchString(event) {
		return errors.New("privacy worker event rejected")
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		if !safeIdentifier.MatchString(key) {
			return errors.New("privacy worker event field rejected")
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var message strings.Builder
	message.WriteString("event=")
	message.WriteString(event)
	for _, key := range keys {
		fmt.Fprintf(&message, " %s=%d", key, fields[key])
	}
	line := message.String()
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Fprintln(s.output, line)
	now := s.now().UTC()
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := s.client.PutLogEvents(callCtx, &cloudwatchlogs.PutLogEventsInput{LogGroupName: &s.group, LogStreamName: &s.stream,
		LogEvents: []cloudwatchtypes.InputLogEvent{{Message: aws.String(line), Timestamp: aws.Int64(now.UnixMilli())}}})
	if err != nil {
		fmt.Fprintln(s.output, "event=privacy_worker_log_delivery_failed")
		return errors.New("privacy worker log delivery failed")
	}
	return nil
}

func classifyFailure(operation string, err error) privacyrequests.ExecutionFailure {
	if operation == "PROVIDER_RECIPIENT_NOTIFY" || errors.Is(err, privacyrequests.ErrInvalid) || errors.Is(err, privacyrequests.ErrExecutorUnavailable) ||
		errors.Is(err, privacyrequests.ErrProviderRegistryUnavailable) {
		return privacyrequests.ExecutionFailure{Classification: privacyrequests.FailureTerminal, Stage: privacyrequests.FailureStageVerify, Code: privacyrequests.FailureUnsupportedOperation}
	}
	return privacyrequests.ExecutionFailure{Classification: privacyrequests.FailureRetryable, Stage: privacyrequests.FailureStageExecute, Code: privacyrequests.FailureDependencyUnavailable}
}

func operationMetricName(operation string) string {
	return "operation_" + strings.ToLower(operation)
}

func stepEvent(operation, outcome string) string {
	return "privacy_worker_step_" + strings.ToLower(operation) + "_" + outcome
}

func validDetailBaseURL(value string) bool {
	u, err := url.Parse(value)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.Path == "/privacidade/conclusao" && u.RawQuery == "" && u.Fragment == ""
}

func readBase64Key(path string) ([]byte, error) {
	encoded, err := readBounded(path)
	if err != nil {
		return nil, err
	}
	value, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil {
		return nil, errors.New("privacy worker key file rejected")
	}
	return value, nil
}

func readKeyring(path string) (map[string][]byte, error) {
	payload, err := readBounded(path)
	if err != nil {
		return nil, err
	}
	var encoded map[string]string
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&encoded); err != nil || ensureEOF(decoder) != nil || encoded == nil || len(encoded) > 20 {
		return nil, errors.New("privacy provider keyring rejected")
	}
	result := make(map[string][]byte, len(encoded))
	for id, value := range encoded {
		decoded, decodeErr := base64.StdEncoding.DecodeString(value)
		if decodeErr != nil || !safeIdentifier.MatchString(id) || len(decoded) < 32 || len(decoded) > 64 {
			return nil, errors.New("privacy provider keyring rejected")
		}
		result[id] = decoded
	}
	return result, nil
}

func readBounded(path string) ([]byte, error) {
	if !filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
		return nil, errors.New("privacy worker key path rejected")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("privacy worker key file unavailable")
	}
	defer file.Close()
	payload, err := io.ReadAll(io.LimitReader(file, maximumKeyFileBytes+1))
	if err != nil || len(payload) == 0 || len(payload) > maximumKeyFileBytes {
		return nil, errors.New("privacy worker key file rejected")
	}
	return payload, nil
}

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON")
	}
	return nil
}

func boundedDuration(raw string, fallback, minimum, maximum time.Duration) (time.Duration, error) {
	if strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value < minimum || value > maximum {
		return 0, errors.New("privacy worker duration rejected")
	}
	return value, nil
}

func boundedInt32(raw string, fallback, minimum, maximum int32) (int32, error) {
	if strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || value < int64(minimum) || value > int64(maximum) {
		return 0, errors.New("privacy worker integer rejected")
	}
	return int32(value), nil
}

func parseExactBool(raw string) (bool, error) {
	switch raw {
	case "", "false":
		return false, nil
	case "true":
		return true, nil
	default:
		return false, errors.New("privacy worker boolean rejected")
	}
}

func valueOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func durationIf(condition bool, value time.Duration) time.Duration {
	if condition {
		return value
	}
	return 0
}
