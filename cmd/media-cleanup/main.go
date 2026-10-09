package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/cfcoimbra/mycfc/internal/mediauploads"
	"github.com/cfcoimbra/mycfc/internal/storage"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const maximumKeyFileBytes = 1 << 20

type config struct {
	databaseURL, region, bucket, endpoint   string
	forcePathStyle                          bool
	pollInterval, leaseDuration, retryDelay time.Duration
	maxAttempts                             int32
	privateKey, transcriptKey               []byte
	transcriptKeyID                         string
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Getenv, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "media_cleanup_failed")
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, getenv func(string) string, output io.Writer) error {
	if len(args) != 1 || args[0] != "serve" {
		return errors.New("media cleanup command rejected")
	}
	cfg, err := loadConfig(getenv)
	if err != nil {
		return err
	}
	poolConfig, err := pgxpool.ParseConfig(cfg.databaseURL)
	if err != nil {
		return errors.New("open media cleanup database")
	}
	poolConfig.AfterConnect = activateCapabilityRole("mycfc_media_cleanup")
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return errors.New("open media cleanup database")
	}
	defer pool.Close()
	var activeRole string
	if err = pool.QueryRow(ctx, "SELECT current_user").Scan(&activeRole); err != nil || activeRole != "mycfc_media_cleanup" {
		return errors.New("media cleanup database identity rejected")
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.region))
	if err != nil {
		return errors.New("load media cleanup AWS configuration")
	}
	client := s3.NewFromConfig(awsCfg, func(options *s3.Options) {
		options.UsePathStyle = cfg.forcePathStyle
		if cfg.endpoint != "" {
			options.BaseEndpoint = aws.String(cfg.endpoint)
		}
	})
	workerRef := uuid.New()
	worker := mediauploads.UploadCleanupWorker{
		Store:     mediauploads.PostgresUploadCleanupStore{DB: pool},
		Objects:   storage.NewS3VersionedStore(client, cfg.bucket),
		WorkerRef: workerRef, PrivateKey: cfg.privateKey,
		TranscriptKeyID: cfg.transcriptKeyID, TranscriptKey: cfg.transcriptKey,
		LeaseDuration: cfg.leaseDuration, MaxAttempts: cfg.maxAttempts, RetryDelay: cfg.retryDelay,
	}
	fmt.Fprintln(output, "event=media_cleanup_started")
	for {
		worked, cycleErr := worker.RunOnce(ctx)
		if cycleErr != nil && !errors.Is(cycleErr, context.Canceled) {
			fmt.Fprintln(output, "event=media_cleanup_cycle_failed")
		} else if worked {
			fmt.Fprintln(output, "event=media_cleanup_succeeded")
		}
		delay := time.Duration(0)
		if !worked || cycleErr != nil {
			delay = cfg.pollInterval
		}
		select {
		case <-ctx.Done():
			fmt.Fprintln(output, "event=media_cleanup_stopped")
			return nil
		case <-time.After(delay):
		}
	}
}

func activateCapabilityRole(role string) func(context.Context, *pgx.Conn) error {
	return func(ctx context.Context, conn *pgx.Conn) error {
		var allowed bool
		if err := conn.QueryRow(ctx, "SELECT pg_has_role(session_user,$1,'MEMBER')", role).Scan(&allowed); err != nil || !allowed {
			return errors.New("database capability membership rejected")
		}
		if _, err := conn.Exec(ctx, "SET ROLE "+role); err != nil {
			return errors.New("activate database capability")
		}
		var active string
		if err := conn.QueryRow(ctx, "SELECT current_user").Scan(&active); err != nil || active != role {
			return errors.New("database capability activation rejected")
		}
		return nil
	}
}

func loadConfig(getenv func(string) string) (config, error) {
	var cfg config
	if getenv("MEDIA_CLEANUP_ENABLED") != "true" {
		return cfg, errors.New("media cleanup is disabled")
	}
	cfg.databaseURL = strings.TrimSpace(getenv("MEDIA_CLEANUP_DATABASE_URL"))
	cfg.region = valueOr(getenv("AWS_REGION"), "eu-west-1")
	cfg.bucket = strings.TrimSpace(getenv("S3_BUCKET_NAME"))
	cfg.endpoint = strings.TrimSpace(getenv("S3_ENDPOINT"))
	cfg.transcriptKeyID = strings.TrimSpace(getenv("MEDIA_CLEANUP_EVIDENCE_KEY_ID"))
	var err error
	if cfg.forcePathStyle, err = exactBool(getenv("S3_FORCE_PATH_STYLE")); err != nil {
		return config{}, err
	}
	if cfg.pollInterval, err = boundedDuration(getenv("MEDIA_CLEANUP_POLL_INTERVAL"), 5*time.Second, time.Second, time.Minute); err != nil {
		return config{}, err
	}
	if cfg.leaseDuration, err = boundedDuration(getenv("MEDIA_CLEANUP_LEASE_DURATION"), 2*time.Minute, 30*time.Second, 15*time.Minute); err != nil {
		return config{}, err
	}
	if cfg.retryDelay, err = boundedDuration(getenv("MEDIA_CLEANUP_RETRY_DELAY"), time.Minute, time.Second, time.Hour); err != nil {
		return config{}, err
	}
	if cfg.maxAttempts, err = boundedInt32(getenv("MEDIA_CLEANUP_MAX_ATTEMPTS"), 5, 1, 100); err != nil {
		return config{}, err
	}
	if cfg.databaseURL == "" || cfg.bucket == "" || !safeID(cfg.region) || !safeID(cfg.transcriptKeyID) {
		return config{}, errors.New("media cleanup configuration rejected")
	}
	if cfg.endpoint != "" {
		parsed, parseErr := url.Parse(cfg.endpoint)
		if parseErr != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return config{}, errors.New("media cleanup endpoint rejected")
		}
	}
	cfg.privateKey, err = readBase64Key(valueOr(getenv("MEDIA_UPLOAD_PRIVATE_KEY_FILE"), "/run/secrets/mycfc/media-upload-private.key"))
	if err != nil || len(cfg.privateKey) != 32 {
		return config{}, errors.New("media cleanup private key rejected")
	}
	cfg.transcriptKey, err = readBase64Key(valueOr(getenv("MEDIA_CLEANUP_EVIDENCE_KEY_FILE"), "/run/secrets/mycfc/media-cleanup-evidence.key"))
	if err != nil || len(cfg.transcriptKey) < 32 {
		return config{}, errors.New("media cleanup evidence key rejected")
	}
	return cfg, nil
}

func readBase64Key(path string) ([]byte, error) {
	if !filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
		return nil, errors.New("media cleanup key path rejected")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("media cleanup key unavailable")
	}
	defer file.Close()
	payload, err := io.ReadAll(io.LimitReader(file, maximumKeyFileBytes+1))
	if err != nil || len(payload) == 0 || len(payload) > maximumKeyFileBytes {
		return nil, errors.New("media cleanup key rejected")
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(payload)))
	if err != nil {
		return nil, errors.New("media cleanup key rejected")
	}
	return decoded, nil
}

func safeID(value string) bool {
	if value == "" || len(value) > 120 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("_.:/-", r) {
			continue
		}
		return false
	}
	return true
}

func boundedDuration(raw string, fallback, minimum, maximum time.Duration) (time.Duration, error) {
	if strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value < minimum || value > maximum {
		return 0, errors.New("media cleanup duration rejected")
	}
	return value, nil
}

func boundedInt32(raw string, fallback, minimum, maximum int32) (int32, error) {
	if strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || value < int64(minimum) || value > int64(maximum) {
		return 0, errors.New("media cleanup integer rejected")
	}
	return int32(value), nil
}

func exactBool(raw string) (bool, error) {
	switch raw {
	case "", "false":
		return false, nil
	case "true":
		return true, nil
	default:
		return false, errors.New("media cleanup boolean rejected")
	}
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}
